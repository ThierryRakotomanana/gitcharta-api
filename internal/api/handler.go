package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
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
	var apiErr *model.GithubAPIError
	if errors.As(err, &apiErr) && apiErr.Status != 0 {
		writeJSON(w, apiErr.Status, map[string]string{"error": apiErr.Msg})
		return
	}

	log.Printf("internal error: %v", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

func (s *Server) HandleCreateAudienceJob(w http.ResponseWriter, r *http.Request) {
	login := r.URL.Query().Get("login")
	audienceType := model.AudienceType(r.URL.Query().Get("type"))

	if login == "" || !audienceType.Valid() || !model.ValidLogin(login) {
		writeError(w, &model.GithubAPIError{Msg: "invalid login or type parameter", Status: http.StatusBadRequest})
		return
	}

	checkCtx, checkCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer checkCancel()

	if _, _, err := github.FetchUserProfile(checkCtx, s.GraphQL, s.Pool, login); err != nil {
		writeError(w, err)
		return
	}

	job, created, err := s.Jobs.Create(login, audienceType)
	if err != nil {
		writeError(w, err)
		return
	}

	if !created {
		writeJSON(w, http.StatusOK, job)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
	s.Jobs.SetCancel(job.ID, cancel)

	go func(jobID, login string, audType model.AudienceType, ctx context.Context, cancel context.CancelFunc) {
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("panic in audience job %s: %v", jobID, r)
				s.Jobs.Fail(jobID, fmt.Errorf("internal error while processing job"))
			}
		}()

		progressCb := func(stage model.ReconcileStage, done int, total *int) {
			s.Jobs.UpdateProgress(jobID, stage, done, total)
		}

		result, err := github.FetchAllAudienceReconciled(ctx, s.GraphQL, s.REST, s.Pool, login, audType, progressCb)
		if err != nil {
			s.Jobs.Fail(jobID, err)
			return
		}

		s.Jobs.Complete(jobID, result)
	}(job.ID, login, audienceType, ctx, cancel)

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

func (s *Server) HandleCancelAudienceJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		writeError(w, &model.GithubAPIError{Msg: "job id required", Status: http.StatusBadRequest})
		return
	}

	job, err := s.Jobs.Cancel(jobID)
	if err != nil {
		switch {
		case errors.Is(err, jobs.ErrJobNotFound):
			writeError(w, &model.GithubAPIError{Msg: "job not found", Status: http.StatusNotFound})
		case errors.Is(err, jobs.ErrJobNotCancellable):
			writeError(w, &model.GithubAPIError{Msg: "job already finished", Status: http.StatusConflict})
		default:
			writeError(w, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, job)
}
