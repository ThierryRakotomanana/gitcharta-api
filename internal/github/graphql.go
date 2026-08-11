package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"githubaudience/internal/model"
)

const (
	githubGraphQLURL  = "https://api.github.com/graphql"
	githubMaxPageSize = 100
)

type UserProfile struct {
	ProfileNode    model.ProfileNode
	FollowersCount int
	FollowingCount int
}

type AudienceConnection struct {
	TotalCount int `json:"totalCount"`
	PageInfo   struct {
		HasNextPage bool    `json:"hasNextPage"`
		EndCursor   *string `json:"endCursor"`
	} `json:"pageInfo"`
	Nodes []model.ProfileNode `json:"nodes"`
}

type AllAudienceResult struct {
	Nodes      []model.ProfileNode
	TotalCount int
}

type CostEstimate struct {
	PointsNeeded int
	Remaining    int
	WillExceed   bool
}

type PaginationRateLimitError struct {
	ResetAt      time.Time
	PartialNodes []model.ProfileNode
	TotalCount   int
}

func (e *PaginationRateLimitError) Error() string { return "pagination interrupted by rate limit" }

func quotaBuffer(rl model.RateLimit) int {
	return computeQuotaBuffer(rl.Limit)
}

func isQuotaLow(rl model.RateLimit) bool {
	return rl.Remaining <= quotaBuffer(rl)
}

const profileFragment = `
fragment ProfileFields on User {
	login
	id
	name
	avatarUrl
	url
	company
	location
	twitterUsername
	isSiteAdmin
}`

const rateLimitFields = `
rateLimit {
	limit
	cost
	remaining
	resetAt
}`

var userProfileQuery = profileFragment + `
query UserProfile($login: String!) {
	user(login: $login) {
		...ProfileFields
		followers { totalCount }
		following { totalCount }
	}
	` + rateLimitFields + `
}`

func audienceQuery(audienceType model.AudienceType) (string, error) {
	var field string
	switch audienceType {
	case model.AudienceFollowers:
		field = "followers"
	case model.AudienceFollowing:
		field = "following"
	default:
		return "", &model.GithubAPIError{Msg: fmt.Sprintf("unsupported audience type: %q", audienceType), Status: http.StatusBadRequest}
	}
	return fmt.Sprintf(`%s
query AudiencePage($login: String!, $first: Int!, $after: String) {
	user(login: $login) {
		login
		%s(first: $first, after: $after) {
			totalCount
			pageInfo { hasNextPage endCursor }
			nodes { ...ProfileFields }
		}
	}
	%s
}`, profileFragment, field, rateLimitFields), nil
}

type profileFieldsData struct {
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

func (p profileFieldsData) toNode() model.ProfileNode {
	return model.ProfileNode{
		Login:           p.Login,
		ID:              p.ID,
		Name:            p.Name,
		AvatarURL:       p.AvatarURL,
		URL:             p.URL,
		Company:         p.Company,
		Location:        p.Location,
		TwitterUsername: p.TwitterUsername,
		IsSiteAdmin:     p.IsSiteAdmin,
	}
}

type userProfileQueryData struct {
	User *struct {
		profileFieldsData
		Followers struct {
			TotalCount int `json:"totalCount"`
		} `json:"followers"`
		Following struct {
			TotalCount int `json:"totalCount"`
		} `json:"following"`
	} `json:"user"`
	RateLimit model.RateLimit `json:"rateLimit"`
}

type audienceQueryData struct {
	User *struct {
		Login     string              `json:"login"`
		Followers *AudienceConnection `json:"followers,omitempty"`
		Following *AudienceConnection `json:"following,omitempty"`
	} `json:"user"`
	RateLimit model.RateLimit `json:"rateLimit"`
}

func (d audienceQueryData) connection(audienceType model.AudienceType) *AudienceConnection {
	if audienceType == model.AudienceFollowers {
		return d.User.Followers
	}
	return d.User.Following
}

type graphQLError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type rateLimitedGraphQLError struct{}

func (e *rateLimitedGraphQLError) Error() string { return "graphql rate limited" }

type GraphQLClient struct {
	HTTPClient *http.Client
}

func NewGraphQLClient(httpClient *http.Client) *GraphQLClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second, Transport: DefaultTransport()}
	}
	return &GraphQLClient{HTTPClient: httpClient}
}

func doGraphQL[T any](ctx context.Context, client *GraphQLClient, token, query string, variables map[string]any) (T, error) {
	var zero T

	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return zero, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubGraphQLURL, bytes.NewReader(body))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return zero, &model.GithubAPIError{Msg: "Invalid or expired GitHub token.", Status: 401, Headers: resp.Header}
	case http.StatusForbidden:
		return zero, &model.GithubAPIError{Msg: "GitHub API rate limit exceeded.", Status: 403, Headers: resp.Header}
	}
	if resp.StatusCode >= 400 {
		return zero, &model.GithubAPIError{Msg: fmt.Sprintf("GitHub API error: %d", resp.StatusCode), Status: resp.StatusCode, Headers: resp.Header}
	}

	var parsed struct {
		Data   T              `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return zero, err
	}

	for _, e := range parsed.Errors {
		switch e.Type {
		case "NOT_FOUND":
			return zero, &model.GithubAPIError{Msg: "user not found", Status: 404}
		case "RATE_LIMITED":
			return zero, &rateLimitedGraphQLError{}
		}
	}
	if len(parsed.Errors) > 0 {
		return zero, &model.GithubAPIError{Msg: parsed.Errors[0].Message}
	}
	return parsed.Data, nil
}

func classifyGraphQLRateLimitError(err error) (time.Time, bool) {
	var rl *rateLimitedGraphQLError
	if errors.As(err, &rl) {
		return time.Now().Add(60 * time.Second), true
	}
	var apiErr *model.GithubAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.Status {
		case 429:
			return resetAtFromHeaders(apiErr.Headers), true
		case 403:
			return classifyForbidden(http.Header(apiErr.Headers))
		}
	}
	return time.Time{}, false
}

func wrapErr(err error) error {
	if err == nil {
		return nil
	}
	var exhausted *AllTokensExhaustedError
	if errors.As(err, &exhausted) {
		return err
	}
	var apiErr *model.GithubAPIError
	if errors.As(err, &apiErr) {
		return err
	}
	return &model.GithubAPIError{Msg: err.Error(), Err: err}
}

func FetchUserProfile(ctx context.Context, client *GraphQLClient, pool *TokenPool, login string) (UserProfile, model.RateLimit, error) {
	data, err := withTokenRotation(pool, func(token string) (userProfileQueryData, error) {
		d, err := doGraphQL[userProfileQueryData](ctx, client, token, userProfileQuery, map[string]any{"login": login})
		if err != nil {
			return d, err
		}
		pool.Report(token, d.RateLimit.Remaining, d.RateLimit.Limit, d.RateLimit.Reset)
		return d, nil
	}, classifyGraphQLRateLimitError)

	if err != nil {
		return UserProfile{}, model.RateLimit{}, wrapErr(err)
	}
	if data.User == nil {
		return UserProfile{}, model.RateLimit{}, &model.GithubAPIError{Msg: fmt.Sprintf("User not found: %s", login), Status: 404}
	}
	profile := UserProfile{
		ProfileNode:    data.User.profileFieldsData.toNode(),
		FollowersCount: data.User.Followers.TotalCount,
		FollowingCount: data.User.Following.TotalCount,
	}
	return profile, data.RateLimit, nil
}

type AudiencePageOptions struct {
	First int
	After *string
}

type AudiencePageResult struct {
	Login     string
	Audience  AudienceConnection
	RateLimit model.RateLimit
}

func FetchAudiencePage(ctx context.Context, client *GraphQLClient, pool *TokenPool, login string, audienceType model.AudienceType, opts AudiencePageOptions) (AudiencePageResult, error) {
	first := opts.First
	if first <= 0 {
		first = githubMaxPageSize
	}
	if first > githubMaxPageSize {
		first = githubMaxPageSize
	}
	query, err := audienceQuery(audienceType)
	if err != nil {
		return AudiencePageResult{}, err
	}

	data, err := withTokenRotation(pool, func(token string) (audienceQueryData, error) {
		d, err := doGraphQL[audienceQueryData](ctx, client, token, query, map[string]any{
			"login": login,
			"first": first,
			"after": opts.After,
		})
		if err != nil {
			return d, err
		}
		pool.Report(token, d.RateLimit.Remaining, d.RateLimit.Limit, d.RateLimit.Reset)
		return d, nil
	}, classifyGraphQLRateLimitError)

	if err != nil {
		return AudiencePageResult{}, wrapErr(err)
	}
	if data.User == nil {
		return AudiencePageResult{}, &model.GithubAPIError{Msg: fmt.Sprintf("User not found: %s", login), Status: 404}
	}
	conn := data.connection(audienceType)
	if conn == nil {
		return AudiencePageResult{}, &model.GithubAPIError{
			Msg: fmt.Sprintf("GitHub returned no %s data for user %q (the account may be suspended, blocked, or otherwise restricted).", audienceType, login),
		}
	}
	return AudiencePageResult{Login: data.User.Login, Audience: *conn, RateLimit: data.RateLimit}, nil
}

func FetchAllAudience(ctx context.Context, client *GraphQLClient, pool *TokenPool, login string, audienceType model.AudienceType, onProgress func(done, total int)) (AllAudienceResult, error) {
	collected := make(map[string]model.ProfileNode)
	order := make([]string, 0)
	var after *string
	var totalCount int

	partialResult := func(resetAt time.Time) (AllAudienceResult, error) {
		nodes := make([]model.ProfileNode, 0, len(order))
		for _, id := range order {
			nodes = append(nodes, collected[id])
		}
		return AllAudienceResult{}, &PaginationRateLimitError{ResetAt: resetAt, PartialNodes: nodes, TotalCount: totalCount}
	}

	for {
		page, err := FetchAudiencePage(ctx, client, pool, login, audienceType, AudiencePageOptions{After: after})
		if err != nil {
			var exhausted *AllTokensExhaustedError
			if errors.As(err, &exhausted) {
				return partialResult(exhausted.ResetAt)
			}
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return partialResult(time.Now().Add(time.Minute))
			}
			return AllAudienceResult{}, err
		}

		for _, node := range page.Audience.Nodes {
			if _, exists := collected[node.ID]; !exists {
				order = append(order, node.ID)
			}
			collected[node.ID] = node
		}
		totalCount = page.Audience.TotalCount

		if onProgress != nil {
			onProgress(len(collected), totalCount)
		}

		if !page.Audience.PageInfo.HasNextPage {
			break
		}

		next := page.Audience.PageInfo.EndCursor
		cursorAdvanced := next != nil && (after == nil || *next != *after)
		if !cursorAdvanced {
			log.Printf("[github-audience] %s page for %q reported hasNextPage=true with a non-advancing cursor; stopping pagination early. This matches a known GitHub GraphQL API pagination bug (github/community#30687).", audienceType, login)
			break
		}
		after = next
	}

	nodes := make([]model.ProfileNode, 0, len(order))
	for _, id := range order {
		nodes = append(nodes, collected[id])
	}
	return AllAudienceResult{Nodes: nodes, TotalCount: totalCount}, nil
}

func EstimateAudienceCost(followersCount, followingCount int, rl model.RateLimit) CostEstimate {
	followerPages := int(math.Ceil(float64(followersCount) / githubMaxPageSize))
	followingPages := int(math.Ceil(float64(followingCount) / githubMaxPageSize))
	pointsNeeded := followerPages + followingPages
	if pointsNeeded < 1 {
		pointsNeeded = 1
	}
	return CostEstimate{
		PointsNeeded: pointsNeeded,
		Remaining:    rl.Remaining,
		WillExceed:   pointsNeeded > rl.Remaining-quotaBuffer(rl),
	}
}