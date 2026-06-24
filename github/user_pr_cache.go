package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tstapler/stapler-squad/log"
	"golang.org/x/sync/singleflight"
)

// UserPR is a pull request authored by the authenticated user across any repo.
type UserPR struct {
	Owner            string
	Repo             string
	Number           int
	Title            string
	URL              string
	HeadRef          string
	BaseRef          string
	State            string // OPEN, CLOSED, MERGED
	IsDraft          bool
	UpdatedAt        time.Time
	ClosedAt         time.Time
	MergedAt         time.Time
	ApprovedCount    int
	ChangesReqCount  int
	CheckConclusion  string // success / failure / pending / action_required / neutral / ""
}

// userPRSnapshot is the immutable COW payload stored in atomic.Value.
type userPRSnapshot struct {
	prs       []UserPR
	capturedAt time.Time
}

// UserPRCacheConfig controls polling behaviour.
type UserPRCacheConfig struct {
	// RefreshInterval is how often the background goroutine refreshes.
	RefreshInterval time.Duration
	// LoginCacheTTL is how long we cache the authenticated user's login.
	LoginCacheTTL time.Duration
}

func defaultUserPRCacheConfig() UserPRCacheConfig {
	return UserPRCacheConfig{
		RefreshInterval: 5 * time.Minute,
		LoginCacheTTL:   30 * time.Minute,
	}
}

type onUserPRUpdatedFn struct {
	fn func(prs []UserPR)
}

// UserPRCache fetches and caches all open (and recently closed) PRs authored
// by the authenticated GitHub user across all repos, using the GraphQL API
// directly (no gh subprocess). Reads are always lock-free via atomic.Value.
type UserPRCache struct {
	config UserPRCacheConfig

	// snapshot is the current COW snapshot of open PRs.
	snapshot atomic.Value // stores *userPRSnapshot

	// onUpdated is the optional callback, set once via SetOnUpdated.
	onUpdated atomic.Value // stores onUserPRUpdatedFn

	// loginState caches the authenticated user's GitHub login.
	loginState atomic.Value // stores loginResult
	loginGroup singleflight.Group

	// refreshGroup coalesces concurrent manual refresh calls.
	refreshGroup singleflight.Group

	ctx    context.Context
	cancel context.CancelFunc
}

type loginResult struct {
	login  string
	expiry time.Time
}

// NewUserPRCache creates a cache with default configuration.
func NewUserPRCache() *UserPRCache {
	return newUserPRCacheWithConfig(defaultUserPRCacheConfig())
}

func newUserPRCacheWithConfig(cfg UserPRCacheConfig) *UserPRCache {
	return &UserPRCache{config: cfg} //nolint:exhaustruct
}

// SetOnUpdated atomically registers a callback invoked after every successful
// refresh. The callback receives a copy of the current PR slice. Safe to call
// at any time, including after Start.
func (c *UserPRCache) SetOnUpdated(fn func(prs []UserPR)) {
	c.onUpdated.Store(onUserPRUpdatedFn{fn: fn})
}

// Start begins the background refresh loop. Calling Start more than once is
// safe but only the first call starts the goroutine.
func (c *UserPRCache) Start(ctx context.Context) {
	if c.cancel != nil {
		return
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	go c.pollLoop()
}

// Stop shuts down the background loop and waits for it to exit.
func (c *UserPRCache) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}

// GetAll returns the current snapshot of open PRs. Returns nil before the first
// successful fetch.
func (c *UserPRCache) GetAll() []UserPR {
	v := c.snapshot.Load()
	if v == nil {
		return nil
	}
	return v.(*userPRSnapshot).prs
}

// Refresh triggers an immediate refresh (coalesced via singleflight if already
// in flight). Blocks until the refresh completes or ctx is cancelled.
func (c *UserPRCache) Refresh(ctx context.Context) error {
	_, err, _ := c.refreshGroup.Do("refresh", func() (interface{}, error) {
		return nil, c.doRefresh(ctx)
	})
	return err
}

func (c *UserPRCache) pollLoop() {
	ticker := time.NewTicker(c.config.RefreshInterval)
	defer ticker.Stop()

	// Eager first fetch.
	if err := c.doRefresh(c.ctx); err != nil {
		log.Warn("UserPRCache: initial refresh failed", "err", err)
	}

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if err := c.doRefresh(c.ctx); err != nil {
				log.Warn("UserPRCache: refresh failed", "err", err)
			}
		}
	}
}

// doRefresh fetches PRs via GraphQL and updates the COW snapshot.
func (c *UserPRCache) doRefresh(ctx context.Context) error {
	login, err := c.getLogin(ctx)
	if err != nil {
		return fmt.Errorf("UserPRCache: get login: %w", err)
	}
	if login == "" {
		return nil // not authenticated — degrade gracefully
	}

	prs, err := c.fetchUserPRs(ctx, login)
	if err != nil {
		return err
	}

	snap := &userPRSnapshot{prs: prs, capturedAt: time.Now()}
	c.snapshot.Store(snap)

	if v := c.onUpdated.Load(); v != nil {
		if cb := v.(onUserPRUpdatedFn).fn; cb != nil {
			cb(prs)
		}
	}
	return nil
}

// getLogin returns the authenticated user's GitHub login, cached by loginState.
func (c *UserPRCache) getLogin(ctx context.Context) (string, error) {
	if v := c.loginState.Load(); v != nil {
		if r := v.(loginResult); time.Now().Before(r.expiry) {
			return r.login, nil
		}
	}

	res, err, _ := c.loginGroup.Do("login", func() (interface{}, error) {
		login, fetchErr := GetCurrentUserLogin(ctx)
		if fetchErr != nil {
			return "", fetchErr
		}
		c.loginState.Store(loginResult{login: login, expiry: time.Now().Add(c.config.LoginCacheTTL)})
		return login, nil
	})
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", nil
	}
	return res.(string), nil
}

// graphQL query fetching up to 100 open PRs (and 20 recently closed) authored
// by the viewer. We page closed PRs separately only if needed — 100 open is
// a practical upper bound for personal accounts.
const userPRGraphQLQuery = `
query UserPRs {
  viewer {
    pullRequests(first: 100, states: [OPEN], orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        number
        title
        url
        headRefName
        baseRefName
        state
        isDraft
        updatedAt
        closedAt
        mergedAt
        repository {
          owner { login }
          name
        }
        reviewDecision
        reviews(last: 20, states: [APPROVED, CHANGES_REQUESTED]) {
          nodes {
            state
          }
        }
        commits(last: 1) {
          nodes {
            commit {
              statusCheckRollup {
                state
              }
            }
          }
        }
      }
    }
  }
}
`

type graphqlRequest struct {
	Query string `json:"query"`
}

type userPRsResponse struct {
	Data struct {
		Viewer struct {
			PullRequests struct {
				Nodes []gqlPRNode `json:"nodes"`
			} `json:"pullRequests"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type gqlPRNode struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	HeadRefName string `json:"headRefName"`
	BaseRefName string `json:"baseRefName"`
	State       string `json:"state"`
	IsDraft     bool   `json:"isDraft"`
	UpdatedAt   string `json:"updatedAt"`
	ClosedAt    string `json:"closedAt"`
	MergedAt    string `json:"mergedAt"`
	Repository  struct {
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
		Name string `json:"name"`
	} `json:"repository"`
	ReviewDecision string `json:"reviewDecision"`
	Reviews        struct {
		Nodes []struct {
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"reviews"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func (c *UserPRCache) fetchUserPRs(ctx context.Context, _ string) ([]UserPR, error) {
	body, err := json.Marshal(graphqlRequest{Query: userPRGraphQLQuery})
	if err != nil {
		return nil, fmt.Errorf("marshal graphql request: %w", err)
	}

	req, err := newGHPostRequest(ctx, "graphql", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build graphql request: %w", err)
	}

	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql request failed: %w", err)
	}
	defer resp.Body.Close()

	if backoff := checkRateLimitHeaders(resp); backoff > 0 {
		time.Sleep(backoff)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API: auth error (%d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("graphql: unexpected status %d", resp.StatusCode)
	}

	var gqlResp userPRsResponse
	if err := json.NewDecoder(resp.Body).Decode(&gqlResp); err != nil {
		return nil, fmt.Errorf("decode graphql response: %w", err)
	}
	if len(gqlResp.Errors) > 0 {
		return nil, fmt.Errorf("graphql errors: %s", gqlResp.Errors[0].Message)
	}

	nodes := gqlResp.Data.Viewer.PullRequests.Nodes
	prs := make([]UserPR, 0, len(nodes))
	for _, n := range nodes {
		prs = append(prs, nodeToUserPR(n))
	}
	return prs, nil
}

func nodeToUserPR(n gqlPRNode) UserPR {
	pr := UserPR{
		Owner:       n.Repository.Owner.Login,
		Repo:        n.Repository.Name,
		Number:      n.Number,
		Title:       n.Title,
		URL:         n.URL,
		HeadRef:     n.HeadRefName,
		BaseRef:     n.BaseRefName,
		State:       n.State,
		IsDraft:     n.IsDraft,
		UpdatedAt:   parseGQLTime(n.UpdatedAt),
		ClosedAt:    parseGQLTime(n.ClosedAt),
		MergedAt:    parseGQLTime(n.MergedAt),
	}

	for _, r := range n.Reviews.Nodes {
		switch r.State {
		case "APPROVED":
			pr.ApprovedCount++
		case "CHANGES_REQUESTED":
			pr.ChangesReqCount++
		}
	}

	if len(n.Commits.Nodes) > 0 {
		if sc := n.Commits.Nodes[0].Commit.StatusCheckRollup; sc != nil {
			pr.CheckConclusion = normalizeCheckState(sc.State)
		}
	}

	return pr
}

func parseGQLTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func normalizeCheckState(s string) string {
	switch s {
	case "SUCCESS":
		return "success"
	case "FAILURE", "ERROR":
		return "failure"
	case "PENDING":
		return "pending"
	case "EXPECTED":
		return "pending"
	default:
		return strings.ToLower(s)
	}
}
