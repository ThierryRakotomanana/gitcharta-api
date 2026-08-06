package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"githubaudience/internal/github"
	"githubaudience/internal/jobs"
	"githubaudience/internal/model"
)

type Server struct {
	GraphQL *github.GraphQLClient
	REST    *github.RESTClient
	Pool    *github.TokenPool
	Jobs    *jobs.JobStore
}

func NewServer(pool *github.TokenPool, jobsStore *jobs.JobStore) *Server {
	httpClient := &http.Client{
		Timeout:   60 * time.Second,
		Transport: github.DefaultTransport(),
	}
	return &Server{
		GraphQL: github.NewGraphQLClient(httpClient),
		REST:    github.NewRESTClient(httpClient),
		Pool:    pool,
		Jobs:    jobsStore,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	msg := "internal server error"

	var apiErr *model.GithubAPIError
	if errors.As(err, &apiErr) {
		msg = apiErr.Msg
		status = apiErr.Status
		if status == 0 {
			status = http.StatusBadGateway
		}
	}

	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) HandleCreateAudienceJob(w http.ResponseWriter, r *http.Request) {
	login := r.URL.Query().Get("login")
	audienceType := model.AudienceType(r.URL.Query().Get("type"))

	if login == "" || !audienceType.Valid() {
		writeError(w, &model.GithubAPIError{Msg: "invalid login or type parameter", Status: http.StatusBadRequest})
		return
	}

	job, err := s.Jobs.Create(login, audienceType)
	if err != nil {
		writeError(w, err)
		return
	}

	go func(jobID, login string, audType model.AudienceType) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in audience job %s: %v\n%s", jobID, r, debug.Stack())
				s.Jobs.Fail(jobID, fmt.Errorf("internal error while processing job"))
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()

		progressCb := func(stage model.ReconcileStage, done int, total *int) {
			s.Jobs.UpdateProgress(jobID, stage, done, total)
		}

		result, err := github.FetchAllAudienceReconciled(ctx, s.GraphQL, s.REST, s.Pool, login, audType, progressCb)
		if err != nil {
			s.Jobs.Fail(jobID, err)
			return
		}

		s.Jobs.Complete(jobID, result)
	}(job.ID, login, audienceType)

	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) HandleGetAudienceJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		jobID = r.URL.Query().Get("id")
	}

	job, exists := s.Jobs.Get(jobID)
	if !exists {
		writeError(w, &model.GithubAPIError{Msg: "job not found", Status: http.StatusNotFound})
		return
	}

	writeJSON(w, http.StatusOK, job)
}