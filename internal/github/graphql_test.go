package github

import (
	"errors"
	"testing"

	"githubaudience/internal/model"
)

func TestEstimateAudienceCost_WithinBudget(t *testing.T) {
	rl := model.RateLimit{Limit: 5000, Remaining: 4000}
	est := EstimateAudienceCost(150, 50, rl)
	if est.WillExceed {
		t.Fatalf("expected WillExceed=false, got true (est=%+v)", est)
	}
	if est.PointsNeeded != 3 {
		t.Fatalf("PointsNeeded = %d, want 3", est.PointsNeeded)
	}
}

func TestEstimateAudienceCost_ExceedsRemainingQuota(t *testing.T) {
	rl := model.RateLimit{Limit: 5000, Remaining: 5}
	est := EstimateAudienceCost(200_000, 0, rl)
	if !est.WillExceed {
		t.Fatalf("expected WillExceed=true for huge audience vs tiny remaining quota, got %+v", est)
	}
}

func TestEstimateAudienceCost_NeverGoesBelowOnePoint(t *testing.T) {
	rl := model.RateLimit{Limit: 5000, Remaining: 5000}
	est := EstimateAudienceCost(0, 0, rl)
	if est.PointsNeeded != 1 {
		t.Fatalf("PointsNeeded = %d, want minimum of 1", est.PointsNeeded)
	}
}

func TestWrapErr_PassesThroughGithubAPIError(t *testing.T) {
	original := &model.GithubAPIError{Msg: "not found", Status: 404}
	got := wrapErr(original)

	var apiErr *model.GithubAPIError
	if !errors.As(got, &apiErr) || apiErr.Status != 404 {
		t.Fatalf("wrapErr should pass through GithubAPIError unchanged, got %#v", got)
	}
}

func TestWrapErr_PassesThroughAllTokensExhausted(t *testing.T) {
	original := &AllTokensExhaustedError{}
	got := wrapErr(original)

	var exhausted *AllTokensExhaustedError
	if !errors.As(got, &exhausted) {
		t.Fatalf("wrapErr should pass through AllTokensExhaustedError, got %#v", got)
	}
}

func TestWrapErr_WrapsGenericErrorsWithUnwrap(t *testing.T) {
	sentinel := errors.New("network blip")
	got := wrapErr(sentinel)

	if !errors.Is(got, sentinel) {
		t.Fatalf("wrapErr(%v) lost the original error; errors.Is failed on the wrapped result", sentinel)
	}

	var apiErr *model.GithubAPIError
	if !errors.As(got, &apiErr) {
		t.Fatalf("expected wrapErr to produce a *model.GithubAPIError, got %T", got)
	}
	if apiErr.Msg != sentinel.Error() {
		t.Errorf("Msg = %q, want %q", apiErr.Msg, sentinel.Error())
	}
}

func TestWrapErr_Nil(t *testing.T) {
	if got := wrapErr(nil); got != nil {
		t.Fatalf("wrapErr(nil) = %v, want nil", got)
	}
}
