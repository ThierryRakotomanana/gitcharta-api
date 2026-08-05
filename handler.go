package githubaudience

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"
)

type Server struct {
	GraphQL *GraphQLClient
	REST    *RESTClient
	Pool    *TokenPool
}

func NewServer(pool *TokenPool) *Server {
	httpClient := &http.Client{Timeout: 60 * time.Second}
	return &Server{
		GraphQL: NewGraphQLClient(httpClient),
		REST:    NewRESTClient(httpClient),
		Pool:    pool,
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("failed to write response: %v", err)
	}
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "internal server error"

	var apiErr *GithubAPIError
	var rlErr *RateLimitError
	var exhausted *AllTokensExhaustedError

	switch {
	case errors.As(err, &apiErr):
		msg = apiErr.Msg
		status = apiErr.Status
		if status == 0 {
			status = http.StatusBadGateway
		}
	case errors.As(err, &rlErr):
		msg = rlErr.Error()
		status = http.StatusTooManyRequests
	case errors.As(err, &exhausted):
		msg = exhausted.Error()
		status = http.StatusTooManyRequests
	default:
		log.Printf("unhandled error: %v", err)
	}

	writeJSON(w, status, errorResponse{Error: msg})
}

func (s *Server) HandleAudience(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, &GithubAPIError{Msg: "method not allowed", Status: http.StatusMethodNotAllowed})
		return
	}

	login := r.URL.Query().Get("login")
	if login == "" {
		writeError(w, &GithubAPIError{Msg: "query parameter \"login\" is required", Status: http.StatusBadRequest})
		return
	}

	audienceType := AudienceType(r.URL.Query().Get("type"))
	if !audienceType.Valid() {
		writeError(w, &GithubAPIError{Msg: `query parameter "type" must be "followers" or "following"`, Status: http.StatusBadRequest})
		return
	}

	result, err := FetchAllAudienceReconciled(r.Context(), s.GraphQL, s.REST, s.Pool, login, audienceType, nil)
	if err != nil {
		writeError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, result)
}
