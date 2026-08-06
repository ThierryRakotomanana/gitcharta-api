package github

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestComputeQuotaBuffer(t *testing.T) {
	cases := []struct {
		limit int
		want  int
	}{
		{limit: 0, want: 1},
		{limit: 10, want: 1},
		{limit: 100, want: 2},
		{limit: 5000, want: 100},
	}
	for _, c := range cases {
		if got := computeQuotaBuffer(c.limit); got != c.want {
			t.Errorf("computeQuotaBuffer(%d) = %d, want %d", c.limit, got, c.want)
		}
	}
}

func TestResetAtFromHeaders_PrefersRetryAfter(t *testing.T) {
	headers := map[string][]string{
		"Retry-After":       {"30"},
		"X-RateLimit-Reset": {strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)},
	}
	before := time.Now()
	got := resetAtFromHeaders(headers)
	if got.Sub(before) > 40*time.Second {
		t.Fatalf("resetAtFromHeaders used X-RateLimit-Reset instead of Retry-After: got %v", got)
	}
}

func TestResetAtFromHeaders_FallsBackToXRateLimitReset(t *testing.T) {
	target := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	headers := map[string][]string{
		"X-RateLimit-Reset": {strconv.FormatInt(target.Unix(), 10)},
	}
	got := resetAtFromHeaders(headers)
	if !got.Equal(target) {
		t.Fatalf("resetAtFromHeaders = %v, want %v", got, target)
	}
}

func TestResetAtFromHeaders_IsCaseInsensitive(t *testing.T) {
	headers := map[string][]string{
		"retry-after": {"5"},
	}
	before := time.Now()
	got := resetAtFromHeaders(headers)
	if got.Before(before) || got.After(before.Add(10*time.Second)) {
		t.Fatalf("resetAtFromHeaders did not match lowercase header, got %v", got)
	}
}

func TestResetAtFromHeaders_DefaultsWhenNoUsefulHeaders(t *testing.T) {
	before := time.Now()
	got := resetAtFromHeaders(map[string][]string{})
	if got.Before(before.Add(50*time.Second)) || got.After(before.Add(70*time.Second)) {
		t.Fatalf("resetAtFromHeaders default = %v, want ~60s from now", got)
	}
}

func TestClassifyForbidden_RetryAfterMeansRateLimited(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "10")
	_, isRateLimit := classifyForbidden(h)
	if !isRateLimit {
		t.Fatal("expected Retry-After header to be classified as rate limit")
	}
}

func TestClassifyForbidden_RemainingZeroMeansRateLimited(t *testing.T) {
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "0")
	_, isRateLimit := classifyForbidden(h)
	if !isRateLimit {
		t.Fatal("expected X-RateLimit-Remaining: 0 to be classified as rate limit")
	}
}

func TestClassifyForbidden_PlainForbiddenIsNotRateLimit(t *testing.T) {
	h := http.Header{}
	_, isRateLimit := classifyForbidden(h)
	if isRateLimit {
		t.Fatal("plain 403 with no rate-limit headers should not be classified as rate limit")
	}
}
