package githubaudience

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

const lowQuotaRatio = 0.02

type tokenState struct {
	token     string
	remaining *int
	limit     *int
	resetAt   *time.Time
}

type TokenPool struct {
	mu     sync.Mutex
	states []*tokenState
	cursor int
}

type AllTokensExhaustedError struct {
	ResetAt time.Time
}

func (e *AllTokensExhaustedError) Error() string {
	return fmt.Sprintf("all tokens exhausted, earliest reset at %s", e.ResetAt.Format(time.Kitchen))
}

func NewTokenPool(tokens []string) (*TokenPool, error) {
	seen := make(map[string]bool)
	states := make([]*tokenState, 0, len(tokens))
	for _, raw := range tokens {
		t := strings.TrimSpace(raw)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		states = append(states, &tokenState{token: t})
	}
	if len(states) == 0 {
		return nil, errors.New("token pool requires at least one non-empty token")
	}
	return &TokenPool{states: states}, nil
}

func (p *TokenPool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.states)
}

func isUsable(s *tokenState, now time.Time) bool {
	if s.remaining == nil {
		return true
	}
	buffer := 1
	if s.limit != nil {
		if b := int(math.Ceil(float64(*s.limit) * lowQuotaRatio)); b > buffer {
			buffer = b
		}
	}
	if *s.remaining > buffer {
		return true
	}
	return s.resetAt != nil && !s.resetAt.After(now)
}

func (p *TokenPool) Acquire(exclude map[string]bool) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	n := len(p.states)
	for i := 0; i < n; i++ {
		idx := (p.cursor + i) % n
		s := p.states[idx]
		if exclude[s.token] {
			continue
		}
		if isUsable(s, now) {
			p.cursor = idx
			return s.token
		}
	}
	return ""
}

func (p *TokenPool) Report(token string, remaining, limit int, resetAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.states {
		if s.token == token {
			r, l := remaining, limit
			s.remaining, s.limit, s.resetAt = &r, &l, &resetAt
			return
		}
	}
}

func (p *TokenPool) MarkExhausted(token string, resetAt time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, s := range p.states {
		if s.token == token {
			zero := 0
			s.remaining, s.resetAt = &zero, &resetAt
			return
		}
	}
}

func (p *TokenPool) EarliestResetAt() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	earliest := time.Now().Add(24 * time.Hour)
	for _, s := range p.states {
		reset := time.Now().Add(time.Minute)
		if s.resetAt != nil {
			reset = *s.resetAt
		}
		if reset.Before(earliest) {
			earliest = reset
		}
	}
	return earliest
}

func withTokenRotation[T any](pool *TokenPool, fn func(token string) (T, error), classify func(error) (time.Time, bool)) (T, error) {
	tried := make(map[string]bool)
	for {
		token := pool.Acquire(tried)
		if token == "" {
			var zero T
			return zero, &AllTokensExhaustedError{ResetAt: pool.EarliestResetAt()}
		}

		result, err := fn(token)
		if err == nil {
			return result, nil
		}

		resetAt, isRateLimit := classify(err)
		if !isRateLimit {
			return result, err
		}
		pool.MarkExhausted(token, resetAt)
		tried[token] = true
	}
}

func resetAtFromHeaders(headers map[string][]string) time.Time {
	get := func(key string) string {
		for k, v := range headers {
			if strings.EqualFold(k, key) && len(v) > 0 {
				return v[0]
			}
		}
		return ""
	}
	if ra := get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			return time.Now().Add(time.Duration(secs) * time.Second)
		}
	}
	if rr := get("X-RateLimit-Reset"); rr != "" {
		if unix, err := strconv.ParseInt(rr, 10, 64); err == nil {
			return time.Unix(unix, 0)
		}
	}
	return time.Now().Add(60 * time.Second)
}
