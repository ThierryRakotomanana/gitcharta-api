package jobs

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"githubaudience/internal/model"
)

func TestCreate_ReturnsIndependentCopy(t *testing.T) {
	s := NewJobStore(5)

	job, err := s.Create("octocat", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	job.Status = model.StatusFailed
	job.Login = "mutated"

	stored, ok := s.Get(job.ID)
	if !ok {
		t.Fatalf("job %s not found in store", job.ID)
	}
	if stored.Status != model.StatusPending {
		t.Errorf("store status = %q, want unaffected %q (Create leaked internal pointer)", stored.Status, model.StatusPending)
	}
	if stored.Login != "octocat" {
		t.Errorf("store login = %q, want unaffected %q (Create leaked internal pointer)", stored.Login, "octocat")
	}
}

func TestCreate_RejectsWhenAtMaxConcurrency(t *testing.T) {
	s := NewJobStore(2)

	if _, err := s.Create("a", model.AudienceFollowers); err != nil {
		t.Fatalf("unexpected error on job 1: %v", err)
	}
	if _, err := s.Create("b", model.AudienceFollowers); err != nil {
		t.Fatalf("unexpected error on job 2: %v", err)
	}
	if _, err := s.Create("c", model.AudienceFollowers); err != ErrTooManyJobs {
		t.Fatalf("job 3: got err %v, want ErrTooManyJobs", err)
	}
}

func TestFail_FreesUpASlot(t *testing.T) {
	s := NewJobStore(1)

	job, err := s.Create("a", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := s.Create("b", model.AudienceFollowers); err != ErrTooManyJobs {
		t.Fatalf("expected pool to be full, got err %v", err)
	}

	s.Fail(job.ID, fmt.Errorf("boom"))

	if _, err := s.Create("c", model.AudienceFollowers); err != nil {
		t.Fatalf("expected slot to be freed after Fail, got err %v", err)
	}

	failed, ok := s.Get(job.ID)
	if !ok || failed.Status != model.StatusFailed || failed.Error != "boom" {
		t.Fatalf("job not marked failed correctly: %+v (ok=%v)", failed, ok)
	}
}

func TestComplete_SetsPartialVsCompletedStatus(t *testing.T) {
	s := NewJobStore(5)

	completeJob, _ := s.Create("a", model.AudienceFollowers)
	s.Complete(completeJob.ID, model.ReconciledAudienceResult{Partial: false})
	got, _ := s.Get(completeJob.ID)
	if got.Status != model.StatusCompleted {
		t.Errorf("status = %q, want %q", got.Status, model.StatusCompleted)
	}

	partialJob, _ := s.Create("b", model.AudienceFollowers)
	s.Complete(partialJob.ID, model.ReconciledAudienceResult{Partial: true})
	got, _ = s.Get(partialJob.ID)
	if got.Status != model.StatusPartial {
		t.Errorf("status = %q, want %q", got.Status, model.StatusPartial)
	}
}

func TestGet_UnknownID(t *testing.T) {
	s := NewJobStore(5)
	if _, ok := s.Get("does-not-exist"); ok {
		t.Fatal("expected ok=false for unknown job id")
	}
}

func TestCleanupOnce_EvictsStaleJobsAndFreesSlots(t *testing.T) {
	s := NewJobStore(2)

	stale, _ := s.Create("stale", model.AudienceFollowers)
	fresh, _ := s.Create("fresh", model.AudienceFollowers)

	s.mu.Lock()
	s.jobs[stale.ID].UpdatedAt = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()

	s.cleanupOnce(1 * time.Hour)

	if _, ok := s.Get(stale.ID); ok {
		t.Error("stale job should have been evicted")
	}
	if _, ok := s.Get(fresh.ID); !ok {
		t.Error("fresh job should not have been evicted")
	}

	if _, err := s.Create("new1", model.AudienceFollowers); err != nil {
		t.Errorf("expected free slot after cleanup, got err: %v", err)
	}
	if _, err := s.Create("new2", model.AudienceFollowers); err != ErrTooManyJobs {
		t.Errorf("expected pool full again (fresh+new1 = 2), got err: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewJobStore(1000)
	const workers = 50

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			job, err := s.Create(fmt.Sprintf("user-%d", n), model.AudienceFollowers)
			if err != nil {
				t.Errorf("Create: %v", err)
				return
			}

			var innerWG sync.WaitGroup
			innerWG.Add(2)
			go func() {
				defer innerWG.Done()
				for j := 0; j < 10; j++ {
					total := 100
					s.UpdateProgress(job.ID, model.StageGraphQL, j, &total)
				}
			}()
			go func() {
				defer innerWG.Done()
				for j := 0; j < 10; j++ {
					_ = json.NewEncoder(io.Discard).Encode(job)
				}
			}()
			innerWG.Wait()

			s.Complete(job.ID, model.ReconciledAudienceResult{})
		}(i)
	}
	wg.Wait()
}
