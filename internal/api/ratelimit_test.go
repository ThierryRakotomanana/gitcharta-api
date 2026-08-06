package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestRateLimitMiddleware_AllowsUpToBurstThenBlocks(t *testing.T) {
	mw := RateLimitMiddleware(rate.Limit(0.0001), 2)
	handler := mw(noopHandler())

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/audience/jobs", nil)
		req.RemoteAddr = "203.0.113.10:12345"
		return req
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, newReq())
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200 (within burst)", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 3: status = %d, want 429 (burst exhausted)", rec.Code)
	}
}

func TestRateLimitMiddleware_TracksIPsIndependently(t *testing.T) {
	mw := RateLimitMiddleware(rate.Limit(0.0001), 1)
	handler := mw(noopHandler())

	reqFor := func(ip string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/audience/jobs", nil)
		req.RemoteAddr = ip + ":12345"
		return req
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, reqFor("203.0.113.10"))
	if rec.Code != http.StatusOK {
		t.Fatalf("client A first request: status = %d, want 200", rec.Code)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reqFor("203.0.113.10"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("client A second request: status = %d, want 429", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reqFor("203.0.113.99"))
	if rec.Code != http.StatusOK {
		t.Fatalf("client B request: status = %d, want 200 (independent budget)", rec.Code)
	}
}

func TestClientIP_HandlesMissingPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "not-a-valid-host-port"
	if got := clientIP(req); got != "not-a-valid-host-port" {
		t.Errorf("clientIP fallback = %q, want original RemoteAddr unchanged", got)
	}
}

func TestClientIP_StripsPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:54321"
	if got := clientIP(req); got != "203.0.113.10" {
		t.Errorf("clientIP = %q, want %q", got, "203.0.113.10")
	}
}
