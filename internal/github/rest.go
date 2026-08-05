package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const githubRESTBaseURL = "https://api.github.com"

const BackfillConcurrency = 20

type RESTClient struct {
	HTTPClient *http.Client
}

func NewRESTClient(httpClient *http.Client) *RESTClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &RESTClient{HTTPClient: httpClient}
}

type githubProfileRest struct {
	Login           string  `json:"login"`
	NodeID          string  `json:"node_id"`
	Name            *string `json:"name"`
	AvatarURL       string  `json:"avatar_url"`
	HTMLURL         string  `json:"html_url"`
	Company         *string `json:"company"`
	Location        *string `json:"location"`
	TwitterUsername *string `json:"twitter_username"`
	SiteAdmin       bool    `json:"site_admin"`
}

func (u githubProfileRest) toNode() ProfileNode {
	return ProfileNode{
		Login:           u.Login,
		ID:              u.NodeID,
		Name:            u.Name,
		AvatarURL:       u.AvatarURL,
		URL:             u.HTMLURL,
		Company:         u.Company,
		Location:        u.Location,
		TwitterUsername: u.TwitterUsername,
		IsSiteAdmin:     u.SiteAdmin,
	}
}

func audienceRoute(audienceType AudienceType) string {
	if audienceType == AudienceFollowers {
		return "/users/%s/followers"
	}
	return "/users/%s/following"
}

func doREST[T any](ctx context.Context, client *RESTClient, token, path string) (T, http.Header, error) {
	var zero T
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubRESTBaseURL+path, nil)
	if err != nil {
		return zero, nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.HTTPClient.Do(req)
	if err != nil {
		return zero, nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotFound:
		return zero, resp.Header, &GithubAPIError{Msg: "not found", Status: 404, Headers: resp.Header}
	case http.StatusUnauthorized:
		return zero, resp.Header, &GithubAPIError{Msg: "Invalid or expired GitHub token.", Status: 401, Headers: resp.Header}
	case http.StatusForbidden:
		return zero, resp.Header, &GithubAPIError{Msg: "GitHub REST API rate limit exceeded.", Status: 403, Headers: resp.Header}
	}
	if resp.StatusCode >= 400 {
		return zero, resp.Header, &GithubAPIError{Msg: fmt.Sprintf("GitHub REST API error: %d", resp.StatusCode), Status: resp.StatusCode, Headers: resp.Header}
	}

	if err := json.NewDecoder(resp.Body).Decode(&zero); err != nil {
		return zero, resp.Header, err
	}
	return zero, resp.Header, nil
}

func classifyRESTRateLimitError(err error) (time.Time, bool) {
	var apiErr *GithubAPIError
	if errors.As(err, &apiErr) && (apiErr.Status == 403 || apiErr.Status == 429) {
		return resetAtFromHeaders(apiErr.Headers), true
	}
	return time.Time{}, false
}

func reportRESTUsage(pool *TokenPool, token string, headers http.Header) {
	remaining, errR := strconv.Atoi(headers.Get("X-RateLimit-Remaining"))
	limit, errL := strconv.Atoi(headers.Get("X-RateLimit-Limit"))
	reset, errT := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64)
	if errR == nil && errL == nil && errT == nil {
		pool.Report(token, remaining, limit, time.Unix(reset, 0))
	}
}

func hasNextLink(header http.Header) bool {
	return strings.Contains(header.Get("Link"), `rel="next"`)
}

type restAudiencePage struct {
	Logins      []string
	HasNextPage bool
}

func fetchAudiencePageRest(ctx context.Context, client *RESTClient, pool *TokenPool, login string, audienceType AudienceType, page int) (restAudiencePage, error) {
	path := fmt.Sprintf(audienceRoute(audienceType), login) + fmt.Sprintf("?per_page=%d&page=%d", githubMaxPageSize, page)

	type restUser struct {
		Login string `json:"login"`
	}

	result, err := withTokenRotation(pool, func(token string) (restAudiencePage, error) {
		var users []restUser
		var headers http.Header
		var err error
		users, headers, err = doREST[[]restUser](ctx, client, token, path)
		if err != nil {
			return restAudiencePage{}, err
		}
		reportRESTUsage(pool, token, headers)

		logins := make([]string, 0, len(users))
		for _, u := range users {
			logins = append(logins, u.Login)
		}
		return restAudiencePage{Logins: logins, HasNextPage: hasNextLink(headers)}, nil
	}, classifyRESTRateLimitError)
	if err != nil {
		var exhausted *AllTokensExhaustedError
		if errors.As(err, &exhausted) {
			return restAudiencePage{}, err
		}
		return restAudiencePage{}, wrapErr(err)
	}
	return result, nil
}

func FetchAllAudienceLoginsRest(ctx context.Context, client *RESTClient, pool *TokenPool, login string, audienceType AudienceType, onProgress func(done int)) (map[string]struct{}, error) {
	logins := make(map[string]struct{})
	page := 1
	hasNextPage := true

	for hasNextPage {
		result, err := fetchAudiencePageRest(ctx, client, pool, login, audienceType, page)
		if err != nil {
			return nil, err
		}
		for _, l := range result.Logins {
			logins[l] = struct{}{}
		}
		if onProgress != nil {
			onProgress(len(logins))
		}
		hasNextPage = result.HasNextPage
		page++
	}
	return logins, nil
}

func FetchUserProfileRest(ctx context.Context, client *RESTClient, pool *TokenPool, login string) (*ProfileNode, error) {
	result, err := withTokenRotation(pool, func(token string) (*ProfileNode, error) {
		user, headers, err := doREST[githubProfileRest](ctx, client, token, "/users/"+login)
		if err != nil {
			return nil, err
		}
		reportRESTUsage(pool, token, headers)
		node := user.toNode()
		return &node, nil
	}, classifyRESTRateLimitError)
	if err != nil {
		var exhausted *AllTokensExhaustedError
		if errors.As(err, &exhausted) {
			return nil, err
		}
		var apiErr *GithubAPIError
		if errors.As(err, &apiErr) && apiErr.Status == 404 {
			return nil, nil
		}
		return nil, wrapErr(err)
	}
	return result, nil
}

type BatchProfilesResult struct {
	Profiles   map[string]ProfileNode
	Unresolved []string
}

func FetchProfilesByLoginRest(ctx context.Context, client *RESTClient, pool *TokenPool, logins []string, concurrency int) (BatchProfilesResult, error) {
	if concurrency <= 0 {
		concurrency = BackfillConcurrency
	}
	if concurrency > len(logins) {
		concurrency = len(logins)
	}
	if concurrency == 0 {
		return BatchProfilesResult{Profiles: map[string]ProfileNode{}}, nil
	}

	var (
		mu         sync.Mutex
		profiles   = make(map[string]ProfileNode)
		unresolved []string
		firstErr   error
	)

	jobs := make(chan string)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for login := range jobs {
			node, err := FetchUserProfileRest(ctx, client, pool, login)
			if err != nil {
				var exhausted *AllTokensExhaustedError
				if errors.As(err, &exhausted) {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()
					continue
				}
				mu.Lock()
				unresolved = append(unresolved, login)
				mu.Unlock()
				continue
			}
			mu.Lock()
			if node != nil {
				profiles[login] = *node
			} else {
				unresolved = append(unresolved, login)
			}
			mu.Unlock()
		}
	}

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go worker()
	}
	for _, login := range logins {
		jobs <- login
	}
	close(jobs)
	wg.Wait()

	if firstErr != nil {
		return BatchProfilesResult{}, firstErr
	}
	return BatchProfilesResult{Profiles: profiles, Unresolved: unresolved}, nil
}
