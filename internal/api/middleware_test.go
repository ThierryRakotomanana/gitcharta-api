package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func noopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestCORSMiddleware_AllowedOriginGetsHeaders(t *testing.T) {
	mw := CORSMiddleware([]string{"https://gitcharta.app"})
	handler := mw(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/audience/jobs/123", nil)
	req.Header.Set("Origin", "https://gitcharta.app")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://gitcharta.app" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://gitcharta.app")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCORSMiddleware_DisallowedOriginGetsNoHeaders(t *testing.T) {
	mw := CORSMiddleware([]string{"https://gitcharta.app"})
	handler := mw(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/api/audience/jobs/123", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for disallowed origin", got)
	}
	
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (request still passes through)", rec.Code)
	}
}

func TestCORSMiddleware_TrimsTrailingSlashAndWhitespaceInConfig(t *testing.T) {
	mw := CORSMiddleware([]string{" https://gitcharta.app/ ", ""})
	handler := mw(noopHandler())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://gitcharta.app")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://gitcharta.app" {
		t.Errorf("expected trimmed config entry to match, got Access-Control-Allow-Origin = %q", got)
	}
}

func TestCORSMiddleware_PreflightOptionsShortCircuits(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})
	mw := CORSMiddleware([]string{"https://gitcharta.app"})
	handler := mw(next)

	req := httptest.NewRequest(http.MethodOptions, "/api/audience/jobs", nil)
	req.Header.Set("Origin", "https://gitcharta.app")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	if called {
		t.Error("OPTIONS preflight should not reach the wrapped handler")
	}
}
