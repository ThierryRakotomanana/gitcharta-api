package githubaudience

import "time"

type AudienceType string

const (
	Followers AudienceType = "followers"
	Following AudienceType = "following"
)

type GithubProfileNode struct {
	Login           string  `json:"login"`
	ID              string  `json:"id"`
	Name            *string `json:"name,omitempty"`
	AvatarURL       string  `json:"avatarUrl"`
	URL             string  `json:"url"`
	Company         *string `json:"company,omitempty"`
	Location        *string `json:"location,omitempty"`
	TwitterUsername *string `json:"twitterUsername,omitempty"`
	IsSiteAdmin     bool    `json:"isSiteAdmin"`
}

type LocalizedGithubProfile struct {
	GithubProfileNode
	Country string `json:"country"`
}

type RateLimit struct {
	Limit     int       `json:"limit"`
	Cost      int       `json:"cost"`
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

type ReconciledAudienceResult struct {
	Nodes            []GithubProfileNode `json:"nodes"`
	GraphqlTotal     int                 `json:"graphqlTotalCount"`
	RestTotal        int                 `json:"restTotalCount"`
	RecoveredLogins  []string            `json:"recoveredLogins"`
	UnresolvedLogins []string            `json:"unresolvedLogins"`
}

type AudienceData struct {
	Followers []LocalizedGithubProfile `json:"followers"`
	Following []LocalizedGithubProfile `json:"following"`
	Ghosts    []LocalizedGithubProfile `json:"ghosts"`
}