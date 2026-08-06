package model

import (
	"fmt"
	"regexp"
	"time"
)

type AudienceType string

const (
	AudienceFollowers AudienceType = "followers"
	AudienceFollowing AudienceType = "following"
)

func (t AudienceType) Valid() bool {
	return t == AudienceFollowers || t == AudienceFollowing
}

var loginPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)

func ValidLogin(login string) bool {
	return loginPattern.MatchString(login)
}

type ProfileNode struct {
	Login           string  `json:"login"`
	ID              string  `json:"id"`
	Name            *string `json:"name"`
	AvatarURL       string  `json:"avatarUrl"`
	URL             string  `json:"url"`
	Company         *string `json:"company"`
	Location        *string `json:"location"`
	TwitterUsername *string `json:"twitterUsername"`
	IsSiteAdmin     bool    `json:"isSiteAdmin"`
}

type ReconcileStage string

const (
	StageGraphQL  ReconcileStage = "graphql"
	StageREST     ReconcileStage = "rest"
	StageBackfill ReconcileStage = "backfill"
)

type ProgressFunc func(stage ReconcileStage, done int, total *int)

type ReconciledAudienceResult struct {
	Nodes             []ProfileNode `json:"nodes"`
	GraphQLTotalCount int           `json:"graphqlTotalCount"`
	RESTTotalCount    int           `json:"restTotalCount"`
	RecoveredLogins   []string      `json:"recoveredLogins"`
	UnresolvedLogins  []string      `json:"unresolvedLogins"`
	Partial           bool          `json:"partial"`
	ResumeAfter       *time.Time    `json:"resumeAfter,omitempty"`
}

type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusRunning   JobStatus = "running"
	StatusCompleted JobStatus = "completed"
	StatusPartial JobStatus = "partial"
	StatusFailed  JobStatus = "failed"
)

type JobProgress struct {
	Stage ReconcileStage `json:"stage"`
	Done  int            `json:"done"`
	Total *int           `json:"total"`
}

type AudienceJob struct {
	ID        string                    `json:"id"`
	Status    JobStatus                 `json:"status"`
	Login     string                    `json:"login"`
	Type      AudienceType              `json:"type"`
	Progress  JobProgress               `json:"progress"`
	Result    *ReconciledAudienceResult `json:"result,omitempty"`
	Error     string                    `json:"error,omitempty"`
	CreatedAt time.Time                 `json:"createdAt"`
	UpdatedAt time.Time                 `json:"updatedAt"`
}

type GithubAPIError struct {
	Msg     string
	Status  int
	Headers map[string][]string
}

func (e *GithubAPIError) Error() string { return e.Msg }

type RateLimit struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

type RateLimitError struct {
	Limit RateLimit
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded, resets at %v", e.Limit.Reset)
}