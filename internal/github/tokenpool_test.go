package github

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestNewTokenPool_DedupsTrimsAndRejectsEmpty(t *testing.T) {
	pool, err := NewTokenPool([]string{" tok1 ", "tok1", "", "tok2", "  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pool.Size() != 2 {
		t.Fatalf("Size() = %d, want 2 (dedup + trim)", pool.Size())
	}
}

func TestNewTokenPool_ErrorsWhenNoUsableTokens(t *testing.T) {
	if _, err := NewTokenPool([]string{"", "  ", ""}); err == nil {
		t.Fatal("expected error for all-empty token list")
	}
}

func TestAcquire_IsStickyOnCurrentUsableToken(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b", "c"})

	first := pool.Acquire(nil)
	for i := 0; i < 3; i++ {
		if got := pool.Acquire(nil); got != first {
			t.Fatalf("Acquire() call %d = %q, want sticky %q (no exclusions, token still usable)", i, got, first)
		}
	}
}

func TestAcquire_AdvancesPastExcludedTokens(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b", "c"})

	seen := map[string]bool{}
	exclude := map[string]bool{}
	for i := 0; i < 3; i++ {
		tok := pool.Acquire(exclude)
		if tok == "" {
			t.Fatalf("Acquire returned empty on iteration %d (exclude=%v)", i, exclude)
		}
		seen[tok] = true
		exclude[tok] = true
	}
	if len(seen) != 3 {
		t.Fatalf("expected all 3 tokens to be reachable by excluding tried ones, got %v", seen)
	}
}

func TestAcquire_ExcludesGivenTokens(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})

	tok := pool.Acquire(map[string]bool{"a": true})
	if tok != "b" {
		t.Fatalf("Acquire with exclusion = %q, want %q", tok, "b")
	}
}

func TestAcquire_ReturnsEmptyWhenAllExcluded(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})

	tok := pool.Acquire(map[string]bool{"a": true, "b": true})
	if tok != "" {
		t.Fatalf("Acquire = %q, want empty string when all tokens excluded", tok)
	}
}

func TestAcquire_SkipsTokenBelowQuotaBufferUntilReset(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})

	future := time.Now().Add(time.Hour)
	pool.Report("a", 1, 100, future)

	tok := pool.Acquire(map[string]bool{"b": true})
	if tok != "" {
		t.Fatalf("Acquire (b excluded) = %q, want empty: 'a' is under its quota buffer and not yet reset", tok)
	}

	past := time.Now().Add(-time.Minute)
	pool.Report("a", 1, 100, past)

	tok = pool.Acquire(map[string]bool{"b": true})
	if tok != "a" {
		t.Fatalf("Acquire (b excluded) = %q, want %q now that its reset time has passed", tok, "a")
	}
}

func TestAcquire_TokenWithAmpleQuotaStaysUsable(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a"})
	pool.Report("a", 4999, 5000, time.Now().Add(time.Hour))

	if tok := pool.Acquire(nil); tok != "a" {
		t.Fatalf("Acquire = %q, want %q (well above buffer)", tok, "a")
	}
}

func TestMarkExhausted_MakesTokenUnusableUntilReset(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})
	future := time.Now().Add(time.Hour)

	pool.MarkExhausted("a", future)

	tok := pool.Acquire(nil)
	if tok != "b" {
		t.Fatalf("Acquire = %q, want %q after marking 'a' exhausted", tok, "b")
	}
}

func TestEarliestResetAt_ReturnsSoonestAcrossTokens(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})
	soon := time.Now().Add(5 * time.Minute)
	later := time.Now().Add(50 * time.Minute)

	pool.MarkExhausted("a", later)
	pool.MarkExhausted("b", soon)

	got := pool.EarliestResetAt()
	if !got.Equal(soon) {
		t.Fatalf("EarliestResetAt = %v, want %v (soonest of the two)", got, soon)
	}
}

func TestWithTokenRotation_SucceedsOnFirstUsableToken(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a"})

	calls := 0
	result, err := withTokenRotation(pool, func(token string) (string, error) {
		calls++
		return "ok:" + token, nil
	}, func(error) (time.Time, bool) { return time.Time{}, false })

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "ok:a" || calls != 1 {
		t.Fatalf("result=%q calls=%d, want ok:a / 1", result, calls)
	}
}

func TestWithTokenRotation_RotatesPastRateLimitedTokens(t *testing.T) {
	pool, _ := NewTokenPool([]string{"bad1", "bad2", "good"})

	attempted := []string{}
	result, err := withTokenRotation(pool, func(token string) (string, error) {
		attempted = append(attempted, token)
		if token == "good" {
			return "success", nil
		}
		return "", errors.New("rate limited")
	}, func(err error) (time.Time, bool) {
		return time.Now().Add(time.Minute), true
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "success" {
		t.Fatalf("result = %q, want %q", result, "success")
	}
	if len(attempted) != 3 {
		t.Fatalf("expected all 3 tokens to be tried before success, tried %v", attempted)
	}
}

func TestWithTokenRotation_ReturnsAllTokensExhaustedWhenNoneWork(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})

	_, err := withTokenRotation(pool, func(token string) (string, error) {
		return "", errors.New("always rate limited")
	}, func(error) (time.Time, bool) {
		return time.Now().Add(time.Minute), true
	})

	var exhausted *AllTokensExhaustedError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected *AllTokensExhaustedError, got %T: %v", err, err)
	}
}

func TestWithTokenRotation_NonRateLimitErrorStopsImmediately(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b"})

	calls := 0
	wantErr := errors.New("not found")
	_, err := withTokenRotation(pool, func(token string) (string, error) {
		calls++
		return "", wantErr
	}, func(error) (time.Time, bool) {
		return time.Time{}, false
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("got err %v, want wrapped %v", err, wantErr)
	}
	if calls != 1 {
		t.Fatalf("expected exactly 1 attempt for a non-rate-limit error, got %d", calls)
	}
}

func TestTokenPool_ConcurrentUse(t *testing.T) {
	pool, _ := NewTokenPool([]string{"a", "b", "c", "d", "e"})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			tok := pool.Acquire(nil)
			if tok == "" {
				return
			}
			if n%3 == 0 {
				pool.Report(tok, 50, 100, time.Now().Add(time.Hour))
			} else if n%3 == 1 {
				pool.MarkExhausted(tok, time.Now().Add(time.Second))
			} else {
				_ = pool.EarliestResetAt()
			}
		}(i)
	}
	wg.Wait()
}
