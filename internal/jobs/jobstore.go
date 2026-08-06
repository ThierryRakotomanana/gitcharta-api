package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"githubaudience/internal/model"
)

var ErrTooManyJobs = errors.New("too many jobs in flight; try again shortly")

const defaultMaxConcurrentJobs = 10

type JobStore struct {
	mu            sync.RWMutex
	jobs          map[string]*model.AudienceJob
	maxConcurrent int
	active        int
}

func NewJobStore(maxConcurrent int) *JobStore {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentJobs
	}
	store := &JobStore{
		jobs:          make(map[string]*model.AudienceJob),
		maxConcurrent: maxConcurrent,
	}
	go store.startCleanupLoop(1 * time.Hour)
	return store
}

func (s *JobStore) Create(login string, audienceType model.AudienceType) (*model.AudienceJob, error) {
	s.mu.Lock()
	if s.active >= s.maxConcurrent {
		s.mu.Unlock()
		return nil, ErrTooManyJobs
	}
	s.active++
	s.mu.Unlock()

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
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
	jobCopy := *job
	s.mu.Unlock()
	return &jobCopy, nil
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
		if result.Partial {
			job.Status = model.StatusPartial
		} else {
			job.Status = model.StatusCompleted
		}
		job.Result = &result
		job.UpdatedAt = time.Now()
		if s.active > 0 {
			s.active--
		}
	}
}

func (s *JobStore) Fail(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, exists := s.jobs[id]; exists {
		job.Status = model.StatusFailed
		job.Error = err.Error()
		job.UpdatedAt = time.Now()
		if s.active > 0 {
			s.active--
		}
	}
}

func (s *JobStore) startCleanupLoop(ttl time.Duration) {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, job := range s.jobs {
			if now.Sub(job.UpdatedAt) > ttl {
				if job.Status == model.StatusPending || job.Status == model.StatusRunning {
					if s.active > 0 {
						s.active--
					}
				}
				delete(s.jobs, id)
			}
		}
		s.mu.Unlock()
	}
}