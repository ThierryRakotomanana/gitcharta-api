package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"githubaudience/internal/model"
)

type redirectingTransport struct {
	base *url.URL
}

func (t *redirectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = t.base.Scheme
	clone.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func TestFetchAllAudience_ContextDeadlineReturnsPartialProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody struct {
			Variables struct {
				After *string `json:"after"`
			} `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&reqBody)

		if reqBody.Variables.After == nil {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"data": {
					"user": {
						"login": "octocat",
						"followers": {
							"totalCount": 2,
							"pageInfo": {"hasNextPage": true, "endCursor": "cursor1"},
							"nodes": [{"login": "alice", "id": "id-alice"}]
						}
					},
					"rateLimit": {"limit": 5000, "cost": 1, "remaining": 4999, "resetAt": "2099-01-01T00:00:00Z"}
				}
			}`))
			return
		}

		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	pool, _ := NewTokenPool([]string{"test-token"})
	client := &GraphQLClient{HTTPClient: &http.Client{Transport: &redirectingTransport{base: base}}}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, fetchErr := FetchAllAudience(ctx, client, pool, "octocat", model.AudienceFollowers, nil)

	var partial *PaginationRateLimitError
	if !errors.As(fetchErr, &partial) {
		t.Fatalf("expected *PaginationRateLimitError on context deadline, got %T: %v", fetchErr, fetchErr)
	}
	if len(partial.PartialNodes) != 1 || partial.PartialNodes[0].Login != "alice" {
		t.Fatalf("expected partial results to retain the first page's node, got %+v", partial.PartialNodes)
	}
}
