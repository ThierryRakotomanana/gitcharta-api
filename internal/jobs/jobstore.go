package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"githubaudience/internal/model"
)

type JobStore struct {
	mu   sync.RWMutex
	jobs map[string]*model.AudienceJob
}

func NewJobStore() *JobStore {
	store := &JobStore{
		jobs: make(map[string]*model.AudienceJob),
	}
	go store.startCleanupLoop(1 * time.Hour)
	return store
}

func (s *JobStore) Create(login string, audienceType model.AudienceType) (*model.AudienceJob, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(b)

	job := &model.AudienceJob{
		ID:        id,
		Status:    model.StatusPending,
		Login:     login,
		Type:      audienceType,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	s.mu.Lock()
	s.jobs[id] = job
	s.mu.Unlock()

	return job, nil
}

func (s *JobStore) Get(id string) (*model.AudienceJob, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, exists := s.jobs[id]
	if !exists {
		return nil, false
	}
	jobCopy := *job
	return &jobCopy, true
}

func (s *JobStore) UpdateProgress(id string, stage model.ReconcileStage, done int, total *int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, exists := s.jobs[id]; exists {
		job.Status = model.StatusRunning
		job.Progress = model.JobProgress{Stage: stage, Done: done, Total: total}
		job.UpdatedAt = time.Now()
	}
}

func (s *JobStore) Complete(id string, result model.ReconciledAudienceResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, exists := s.jobs[id]; exists {
		job.Status = model.StatusCompleted
		job.Result = &result
		job.UpdatedAt = time.Now()
	}
}

func (s *JobStore) Fail(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, exists := s.jobs[id]; exists {
		job.Status = model.StatusFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
	}
}

func (s *JobStore) startCleanupLoop(ttl time.Duration) {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, job := range s.jobs {
			if now.Sub(job.UpdatedAt) > ttl {
				delete(s.jobs, id)
			}
		}
		s.mu.Unlock()
	}
}