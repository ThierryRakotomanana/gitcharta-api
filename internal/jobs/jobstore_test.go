package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"githubaudience/internal/model"
)

func TestCreate_ReturnsIndependentCopy(t *testing.T) {
	s := NewJobStore(5)

	job, _, err := s.Create("octocat", model.AudienceFollowers)
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

	if _, _, err := s.Create("a", model.AudienceFollowers); err != nil {
		t.Fatalf("unexpected error on job 1: %v", err)
	}
	if _, _, err := s.Create("b", model.AudienceFollowers); err != nil {
		t.Fatalf("unexpected error on job 2: %v", err)
	}
	if _, _, err := s.Create("c", model.AudienceFollowers); err != ErrTooManyJobs {
		t.Fatalf("job 3: got err %v, want ErrTooManyJobs", err)
	}
}

func TestFail_FreesUpASlot(t *testing.T) {
	s := NewJobStore(1)

	job, _, err := s.Create("a", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, _, err := s.Create("b", model.AudienceFollowers); err != ErrTooManyJobs {
		t.Fatalf("expected pool to be full, got err %v", err)
	}

	s.Fail(job.ID, fmt.Errorf("boom"))

	if _, _, err := s.Create("c", model.AudienceFollowers); err != nil {
		t.Fatalf("expected slot to be freed after Fail, got err %v", err)
	}

	failed, ok := s.Get(job.ID)
	if !ok || failed.Status != model.StatusFailed || failed.Error != "boom" {
		t.Fatalf("job not marked failed correctly: %+v (ok=%v)", failed, ok)
	}
}

func TestComplete_SetsPartialVsCompletedStatus(t *testing.T) {
	s := NewJobStore(5)

	completeJob, _, _ := s.Create("a", model.AudienceFollowers)
	s.Complete(completeJob.ID, model.ReconciledAudienceResult{Partial: false})
	got, _ := s.Get(completeJob.ID)
	if got.Status != model.StatusCompleted {
		t.Errorf("status = %q, want %q", got.Status, model.StatusCompleted)
	}

	partialJob, _, _ := s.Create("b", model.AudienceFollowers)
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

func TestCreate_DedupsInFlightRequestsForSameLoginAndType(t *testing.T) {
	s := NewJobStore(5)

	first, created1, err := s.Create("torvalds", model.AudienceFollowers)
	if err != nil || !created1 {
		t.Fatalf("first Create: created=%v err=%v, want created=true", created1, err)
	}

	second, created2, err := s.Create("torvalds", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("second Create: unexpected error: %v", err)
	}
	if created2 {
		t.Fatal("second Create for same in-flight (login,type) should return created=false")
	}
	if second.ID != first.ID {
		t.Fatalf("second Create returned a different job (%s) than the in-flight one (%s)", second.ID, first.ID)
	}

	if _, _, err := s.Create("a", model.AudienceFollowers); err != nil {
		t.Errorf("expected a free slot (dedup should not double-count), got err: %v", err)
	}
}

func TestCreate_DifferentAudienceTypeIsNotDeduped(t *testing.T) {
	s := NewJobStore(5)

	followers, created1, _ := s.Create("torvalds", model.AudienceFollowers)
	following, created2, _ := s.Create("torvalds", model.AudienceFollowing)

	if !created1 || !created2 {
		t.Fatalf("both jobs should be newly created, got created1=%v created2=%v", created1, created2)
	}
	if followers.ID == following.ID {
		t.Fatal("followers and following jobs for the same login must not be deduped together")
	}
}

func TestCreate_DedupIsCaseInsensitive(t *testing.T) {
	s := NewJobStore(5)

	first, _, _ := s.Create("torvalds", model.AudienceFollowers)
	second, created, _ := s.Create("Torvalds", model.AudienceFollowers)

	if created {
		t.Fatal("expected dedup across differently-cased logins")
	}
	if second.ID != first.ID {
		t.Fatal("expected same job ID across differently-cased logins")
	}
}

func TestCreate_AllowsNewJobAfterPreviousOneFinished(t *testing.T) {
	s := NewJobStore(5)

	first, _, _ := s.Create("torvalds", model.AudienceFollowers)
	s.Complete(first.ID, model.ReconciledAudienceResult{})

	second, created, err := s.Create("torvalds", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !created {
		t.Fatal("expected a brand new job once the previous one had finished")
	}
	if second.ID == first.ID {
		t.Fatal("expected a different job ID after the previous one completed")
	}
}

func TestCancel_StopsJobAndReleasesResources(t *testing.T) {
	s := NewJobStore(2)

	job, _, err := s.Create("torvalds", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cancelled := false
	s.SetCancel(job.ID, func() { cancelled = true })

	got, err := s.Cancel(job.ID)
	if err != nil {
		t.Fatalf("Cancel: unexpected error: %v", err)
	}
	if got.Status != model.StatusCancelled {
		t.Fatalf("status = %q, want %q", got.Status, model.StatusCancelled)
	}
	if !cancelled {
		t.Fatal("registered cancel func was not invoked")
	}

	stored, ok := s.Get(job.ID)
	if !ok || stored.Status != model.StatusCancelled {
		t.Fatalf("job in store should also read Cancelled, got %+v (ok=%v)", stored, ok)
	}

	if _, _, err := s.Create("linus", model.AudienceFollowers); err != nil {
		t.Errorf("expected free slot after cancel, got err: %v", err)
	}

	fresh, created, err := s.Create("torvalds", model.AudienceFollowers)
	if err != nil {
		t.Fatalf("unexpected error re-requesting torvalds: %v", err)
	}
	if !created || fresh.ID == job.ID {
		t.Fatalf("expected a brand new job for torvalds after cancellation, got created=%v id=%s (old id=%s)", created, fresh.ID, job.ID)
	}
}

func TestCancel_UnknownJobReturnsErrJobNotFound(t *testing.T) {
	s := NewJobStore(5)
	if _, err := s.Cancel("does-not-exist"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("got %v, want ErrJobNotFound", err)
	}
}

func TestCancel_AlreadyFinishedJobReturnsErrJobNotCancellable(t *testing.T) {
	s := NewJobStore(5)
	job, _, _ := s.Create("torvalds", model.AudienceFollowers)
	s.Complete(job.ID, model.ReconciledAudienceResult{})

	if _, err := s.Cancel(job.ID); !errors.Is(err, ErrJobNotCancellable) {
		t.Fatalf("got %v, want ErrJobNotCancellable", err)
	}
}

func TestCancel_RaceWithInFlightUpdateProgress(t *testing.T) {
	s := NewJobStore(5)
	job, _, _ := s.Create("torvalds", model.AudienceFollowers)
	s.SetCancel(job.ID, func() {})

	if _, err := s.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	total := 100
	s.UpdateProgress(job.ID, model.StageGraphQL, 50, &total)

	got, _ := s.Get(job.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("status = %q after stale UpdateProgress, want it to remain %q", got.Status, model.StatusCancelled)
	}
}

func TestCancel_RaceWithInFlightComplete(t *testing.T) {
	s := NewJobStore(5)
	job, _, _ := s.Create("torvalds", model.AudienceFollowers)
	s.SetCancel(job.ID, func() {})

	if _, err := s.Cancel(job.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	s.Complete(job.ID, model.ReconciledAudienceResult{})

	got, _ := s.Get(job.ID)
	if got.Status != model.StatusCancelled {
		t.Fatalf("status = %q after stale Complete, want it to remain %q", got.Status, model.StatusCancelled)
	}
}

func TestCleanupOnce_EvictsStaleJobsAndFreesSlots(t *testing.T) {
	s := NewJobStore(2)

	stale, _, _ := s.Create("stale", model.AudienceFollowers)
	fresh, _, _ := s.Create("fresh", model.AudienceFollowers)

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

	if _, _, err := s.Create("new1", model.AudienceFollowers); err != nil {
		t.Errorf("expected free slot after cleanup, got err: %v", err)
	}
	if _, _, err := s.Create("new2", model.AudienceFollowers); err != ErrTooManyJobs {
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

			job, _, err := s.Create(fmt.Sprintf("user-%d", n), model.AudienceFollowers)
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
