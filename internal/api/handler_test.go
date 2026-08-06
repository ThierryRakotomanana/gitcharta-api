package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"githubaudience/internal/github"
	"githubaudience/internal/jobs"
	"githubaudience/internal/model"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	pool, err := github.NewTokenPool([]string{"test-token"})
	if err != nil {
		t.Fatalf("failed to build token pool: %v", err)
	}
	return NewServer(pool, jobs.NewJobStore(5))
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode error body: %v", err)
	}
	return body["error"]
}

func TestHandleCreateAudienceJob_RejectsMissingLogin(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/audience/jobs?type=followers", nil)
	rec := httptest.NewRecorder()

	s.HandleCreateAudienceJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCreateAudienceJob_RejectsInvalidAudienceType(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/audience/jobs?login=octocat&type=friends", nil)
	rec := httptest.NewRecorder()

	s.HandleCreateAudienceJob(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCreateAudienceJob_RejectsMalformedLogin(t *testing.T) {
	s := newTestServer(t)
	badLogins := []string{"-leading-hyphen", "trailing-hyphen-", "has spaces", "way-too-long-of-a-login-name-for-github-to-ever-accept"}

	for _, login := range badLogins {
		target := "/api/audience/jobs?login=" + url.QueryEscape(login) + "&type=followers"
		req := httptest.NewRequest(http.MethodPost, target, nil)
		rec := httptest.NewRecorder()

		s.HandleCreateAudienceJob(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("login %q: status = %d, want 400", login, rec.Code)
		}
	}
}

func TestHandleGetAudienceJob_NotFound(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/audience/jobs/does-not-exist", nil)
	req.SetPathValue("id", "does-not-exist")
	rec := httptest.NewRecorder()

	s.HandleGetAudienceJob(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := decodeErr(t, rec); got != "job not found" {
		t.Errorf("error message = %q, want %q", got, "job not found")
	}
}

func TestHandleGetAudienceJob_ReturnsExistingJob(t *testing.T) {
	s := newTestServer(t)
	job, err := s.Jobs.Create("octocat", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/audience/jobs/"+job.ID, nil)
	req.SetPathValue("id", job.ID)
	rec := httptest.NewRecorder()

	s.HandleGetAudienceJob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got model.AudienceJob
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != job.ID || got.Login != "octocat" {
		t.Errorf("got job %+v, want ID=%s Login=octocat", got, job.ID)
	}
}

func TestWriteError_KnownAPIErrorPassesThroughMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, &model.GithubAPIError{Msg: "invalid login or type parameter", Status: http.StatusBadRequest})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := decodeErr(t, rec); got != "invalid login or type parameter" {
		t.Errorf("error = %q, want passthrough message", got)
	}
}

func TestWriteError_UnknownErrorIsSanitized(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, errors.New("dial tcp 10.0.0.5:5432: connection refused"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	got := decodeErr(t, rec)
	if got != "internal server error" {
		t.Errorf("error = %q, leaked internal detail instead of generic message", got)
	}
}

func TestWriteError_ZeroStatusAPIErrorIsSanitized(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, &model.GithubAPIError{Msg: "some internal json decode error"})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := decodeErr(t, rec); got != "internal server error" {
		t.Errorf("error = %q, want generic message for zero-status API error", got)
	}
}

func TestNewServer_WiresDefaultTransport(t *testing.T) {
	pool, _ := github.NewTokenPool([]string{"tok"})
	s := NewServer(pool, jobs.NewJobStore(1))

	if s.GraphQL.HTTPClient.Timeout != 60*time.Second {
		t.Errorf("GraphQL client timeout = %v, want 60s", s.GraphQL.HTTPClient.Timeout)
	}
	if s.GraphQL.HTTPClient.Transport == nil {
		t.Error("GraphQL client transport should be explicitly set to DefaultTransport(), not left nil")
	}
}
