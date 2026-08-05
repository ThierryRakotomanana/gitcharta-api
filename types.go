package githubaudience

import (
	"fmt"
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

type PageInfo struct {
	HasNextPage bool    `json:"hasNextPage"`
	EndCursor   *string `json:"endCursor"`
}

type AudienceConnection struct {
	TotalCount int           `json:"totalCount"`
	PageInfo   PageInfo      `json:"pageInfo"`
	Nodes      []ProfileNode `json:"nodes"`
}

type RateLimit struct {
	Limit     int       `json:"limit"`
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

type UserProfile struct {
	ProfileNode
	FollowersCount int `json:"followersCount"`
	FollowingCount int `json:"followingCount"`
}

type AllAudienceResult struct {
	Nodes      []ProfileNode
	TotalCount int
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
}

type CostEstimate struct {
	PointsNeeded int  `json:"pointsNeeded"`
	Remaining    int  `json:"remaining"`
	WillExceed   bool `json:"willExceed"`
}

type ReconciliationCostEstimate struct {
	GraphQLPoints          int `json:"graphqlPoints"`
	RESTRequests           int `json:"restRequests"`
	WorstCaseBackfillPoint int `json:"worstCaseBackfillPoints"`
}

type GithubAPIError struct {
	Msg     string
	Status  int
	Headers map[string][]string
}

func (e *GithubAPIError) Error() string { return e.Msg }

type RateLimitError struct {
	ResetAt      time.Time
	PartialNodes []ProfileNode
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded, resets at %s", e.ResetAt.Format(time.Kitchen))
}
