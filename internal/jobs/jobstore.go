package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"githubaudience/internal/model"
)

var (
	ErrTooManyJobs       = errors.New("too many jobs in flight; try again shortly")
	ErrJobNotFound       = errors.New("job not found")
	ErrJobNotCancellable = errors.New("job already finished")
)

const defaultMaxConcurrentJobs = 10

type JobStore struct {
	mu            sync.RWMutex
	jobs          map[string]*model.AudienceJob
	cancels       map[string]context.CancelFunc
	activeByKey   map[string]string
	slotHeld      map[string]bool
	maxConcurrent int
	active        int
}

func NewJobStore(maxConcurrent int) *JobStore {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentJobs
	}
	store := &JobStore{
		jobs:          make(map[string]*model.AudienceJob),
		cancels:       make(map[string]context.CancelFunc),
		activeByKey:   make(map[string]string),
		slotHeld:      make(map[string]bool),
		maxConcurrent: maxConcurrent,
	}
	go store.startCleanupLoop(1 * time.Hour)
	return store
}

func activeKey(login string, audienceType model.AudienceType) string {
	return string(audienceType) + ":" + strings.ToLower(login)
}

func (s *JobStore) Create(login string, audienceType model.AudienceType) (job *model.AudienceJob, created bool, err error) {
	key := activeKey(login, audienceType)

	s.mu.Lock()
	if existingID, ok := s.activeByKey[key]; ok {
		if existing, exists := s.jobs[existingID]; exists {
			jobCopy := *existing
			s.mu.Unlock()
			return &jobCopy, false, nil
		}
	}
	if s.active >= s.maxConcurrent {
		s.mu.Unlock()
		return nil, false, ErrTooManyJobs
	}
	s.active++
	s.mu.Unlock()

	b := make([]byte, 16)
	if _, randErr := rand.Read(b); randErr != nil {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
		return nil, false, randErr
	}
	id := hex.EncodeToString(b)

	newJob := &model.AudienceJob{
		ID:        id,
		Status:    model.StatusPending,
		Login:     login,
		Type:      audienceType,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	s.mu.Lock()
	s.jobs[id] = newJob
	s.activeByKey[key] = id
	s.slotHeld[id] = true
	jobCopy := *newJob
	s.mu.Unlock()

	return &jobCopy, true, nil
}

func (s *JobStore) SetCancel(id string, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancels[id] = cancel
}

func (s *JobStore) Cancel(id string) (*model.AudienceJob, error) {
	s.mu.Lock()
	job, exists := s.jobs[id]
	if !exists {
		s.mu.Unlock()
		return nil, ErrJobNotFound
	}
	if job.Status != model.StatusPending && job.Status != model.StatusRunning {
		s.mu.Unlock()
		return nil, ErrJobNotCancellable
	}

	job.Status = model.StatusCancelled
	job.UpdatedAt = time.Now()
	cancel := s.cancels[id]
	s.releaseSlotLocked(id, activeKey(job.Login, job.Type))
	jobCopy := *job
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
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
	if job, exists := s.jobs[id]; exists && job.Status != model.StatusCancelled {
		job.Status = model.StatusRunning
		job.Progress = model.JobProgress{Stage: stage, Done: done, Total: total}
		job.UpdatedAt = time.Now()
	}
}

func (s *JobStore) Complete(id string, result model.ReconciledAudienceResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, exists := s.jobs[id]
	if !exists || job.Status == model.StatusCancelled {
		return
	}
	if result.Partial {
		job.Status = model.StatusPartial
	} else {
		job.Status = model.StatusCompleted
	}
	job.Result = &result
	job.UpdatedAt = time.Now()
	s.releaseSlotLocked(id, activeKey(job.Login, job.Type))
}

func (s *JobStore) Fail(id string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, exists := s.jobs[id]
	if !exists || job.Status == model.StatusCancelled {
		return
	}
	job.Status = model.StatusFailed
	job.Error = err.Error()
	job.UpdatedAt = time.Now()
	s.releaseSlotLocked(id, activeKey(job.Login, job.Type))
}

func (s *JobStore) releaseSlotLocked(id, key string) {
	if s.slotHeld[id] {
		delete(s.slotHeld, id)
		if s.active > 0 {
			s.active--
		}
	}
	if s.activeByKey[key] == id {
		delete(s.activeByKey, key)
	}
	delete(s.cancels, id)
}

func (s *JobStore) startCleanupLoop(ttl time.Duration) {
	ticker := time.NewTicker(15 * time.Minute)
	for range ticker.C {
		s.cleanupOnce(ttl)
	}
}

func (s *JobStore) cleanupOnce(ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, job := range s.jobs {
		if now.Sub(job.UpdatedAt) > ttl {
			if cancel := s.cancels[id]; cancel != nil {
				cancel()
			}
			s.releaseSlotLocked(id, activeKey(job.Login, job.Type))
			delete(s.jobs, id)
		}
	}
}
