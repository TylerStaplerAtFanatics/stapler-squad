package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	sessiongit "github.com/tstapler/stapler-squad/session/git"
	"golang.org/x/sync/singleflight"
)

// ErrNoPR is returned by GetPRForBranch when no pull request exists for the branch.
var ErrNoPR = errors.New("no pull request found for branch")

// authResult caches the outcome of a gh auth status check with an expiry time.
type authResult struct {
	err    error
	expiry time.Time
}

var (
	ghAuthState atomic.Value       // stores authResult
	ghAuthGroup singleflight.Group //nolint:exhaustruct
)

const ghAuthTTL = 5 * time.Minute

// PRInfo contains metadata about a GitHub pull request
type PRInfo struct {
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	Body         string    `json:"body"`
	HeadRef      string    `json:"headRefName"`
	BaseRef      string    `json:"baseRefName"`
	State        string    `json:"state"`
	Author       string    `json:"author"`
	Labels       []string  `json:"labels"`
	HTMLURL      string    `json:"url"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	IsDraft      bool      `json:"isDraft"`
	Mergeable    string    `json:"mergeable"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	ChangedFiles int       `json:"changedFiles"`

	// Review and CI status fields (populated by GetPRInfo with extended fields)
	ReviewDecision        string // "approved" / "changes_requested" / "review_required" / ""
	ApprovedCount         int    // Count of current non-dismissed APPROVED reviews
	ChangesRequestedCount int    // Count of current non-dismissed CHANGES_REQUESTED reviews
	CheckConclusion       string // "success" / "failure" / "pending" / "action_required" / "neutral" / ""
	CheckStatus           string // "completed" / "in_progress" / ""
}

// PRComment represents a comment on a PR (either issue comment or review comment)
type PRComment struct {
	ID        int       `json:"id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
	Path      string    `json:"path,omitempty"`     // For review comments
	Line      int       `json:"line,omitempty"`     // For review comments
	IsReview  bool      `json:"isReview,omitempty"` // True if this is a review comment
}

// ghReviewItem is one review from GET /pulls/{number}/reviews.
type ghReviewItem struct {
	User  struct{ Login string `json:"login"` } `json:"user"`
	State string                                `json:"state"` // APPROVED, CHANGES_REQUESTED, DISMISSED, COMMENTED, PENDING
	Body  string                                `json:"body"`
}

// ghStatusCheckItem is one check-run or commit-status entry.
type ghStatusCheckItem struct {
	Name       string `json:"name"`
	State      string `json:"state"`      // commit status: success, failure, pending, error
	Status     string `json:"status"`     // check-run: queued, in_progress, completed
	Conclusion string `json:"conclusion"` // check-run: success, failure, action_required, etc.
}

// CheckGHAuth verifies GitHub authentication via GET /user using the native HTTP
// client. No subprocess is invoked — avoids forkExec lock contention.
// Results are cached for 5 minutes. Concurrent callers share a single inflight
// call via singleflight.
func CheckGHAuth() error {
	// Fast path: return cached result if still fresh.
	if v := ghAuthState.Load(); v != nil {
		if r := v.(authResult); time.Now().Before(r.expiry) {
			return r.err
		}
	}

	// Slow path: at most one goroutine calls the API; others wait and reuse the result.
	res, err, _ := ghAuthGroup.Do("auth", func() (interface{}, error) {
		authCheckCtx, authCheckCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer authCheckCancel()

		req, buildErr := newGHRequest(authCheckCtx, "user")
		if buildErr != nil {
			authErr := fmt.Errorf("GitHub auth check: failed to build request: %w", buildErr)
			ghAuthState.Store(authResult{err: authErr, expiry: time.Now().Add(ghAuthTTL)})
			return authErr, nil
		}

		resp, doErr := ghHTTPClient.Do(req)
		if doErr != nil {
			authErr := fmt.Errorf("GitHub auth check: request failed: %w", doErr)
			ghAuthState.Store(authResult{err: authErr, expiry: time.Now().Add(ghAuthTTL)})
			return authErr, nil
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)

		var authErr error
		switch resp.StatusCode {
		case http.StatusOK:
			// authenticated — no error
		case http.StatusUnauthorized, http.StatusForbidden:
			authErr = fmt.Errorf("GitHub is not authenticated (HTTP %d). Set GITHUB_TOKEN or run 'gh auth login'", resp.StatusCode)
		default:
			authErr = fmt.Errorf("GitHub auth check: unexpected status %d", resp.StatusCode)
		}

		ghAuthState.Store(authResult{err: authErr, expiry: time.Now().Add(ghAuthTTL)})
		return authErr, nil
	})

	// singleflight itself never returns a non-nil error here (we always return
	// nil from the Do closure and carry the real error in res), but handle it
	// defensively.
	if err != nil {
		return err
	}
	if res != nil {
		return res.(error)
	}
	return nil
}

// GetCurrentUserLogin returns the GitHub login of the authenticated user via
// GET /user. Returns an empty string (not an error) when unauthenticated so
// callers can degrade gracefully.
func GetCurrentUserLogin(ctx context.Context) (string, error) {
	req, err := newGHRequest(ctx, "user")
	if err != nil {
		return "", fmt.Errorf("build /user request: %w", err)
	}
	return fetchLoginFromRequest(req)
}

// GetCurrentUserLoginWithToken fetches the GitHub login for an explicit token.
// Returns ("", nil) when the token is invalid or unauthenticated.
func GetCurrentUserLoginWithToken(ctx context.Context, token string) (string, error) {
	req, err := newGHRequestWithToken(ctx, "user", token)
	if err != nil {
		return "", fmt.Errorf("build /user request: %w", err)
	}
	return fetchLoginFromRequest(req)
}

func fetchLoginFromRequest(req *http.Request) (string, error) {
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("/user request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("/user: unexpected status %d", resp.StatusCode)
	}

	var u struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", fmt.Errorf("decode /user response: %w", err)
	}
	return u.Login, nil
}

// GetPRInfo fetches metadata for a pull request including review and CI status.
func GetPRInfo(owner, repo string, prNumber int) (*PRInfo, error) {
	return GetPRInfoCtx(context.Background(), owner, repo, prNumber)
}

// GetPRInfoCtx fetches PR metadata using native GitHub REST API calls.
// Three requests: GET /pulls/{n}, GET /pulls/{n}/reviews, GET /commits/{sha}/check-runs + /statuses.
func GetPRInfoCtx(ctx context.Context, owner, repo string, prNumber int) (*PRInfo, error) {
	if err := CheckGHAuth(); err != nil {
		return nil, err
	}

	// 1. Core PR data.
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	req, err := newGHRequest(ctx, apiPath)
	if err != nil {
		return nil, fmt.Errorf("build PR request: %w", err)
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PR request failed: %w", err)
	}
	prBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("read PR response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("GitHub API: unauthorized (401)")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API: status %d for PR %d", resp.StatusCode, prNumber)
	}

	var pr struct {
		Number       int    `json:"number"`
		Title        string `json:"title"`
		Body         string `json:"body"`
		State        string `json:"state"`
		HTMLURL      string `json:"html_url"`
		Draft        bool   `json:"draft"`
		Merged       bool   `json:"merged"`
		Mergeable    *bool  `json:"mergeable"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		CreatedAt    string `json:"created_at"`
		UpdatedAt    string `json:"updated_at"`
		Head         struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(prBody, &pr); err != nil {
		return nil, fmt.Errorf("parse PR response: %w", err)
	}

	// 2. Reviews.
	reviews := fetchPRReviews(ctx, owner, repo, prNumber)

	// 3. Check-runs + commit statuses.
	checks := fetchCommitChecks(ctx, owner, repo, pr.Head.SHA)

	labels := make([]string, len(pr.Labels))
	for i, l := range pr.Labels {
		labels[i] = l.Name
	}

	state := pr.State
	if pr.Merged {
		state = "merged"
	}

	mergeableStr := "UNKNOWN"
	if pr.Mergeable != nil {
		if *pr.Mergeable {
			mergeableStr = "MERGEABLE"
		} else {
			mergeableStr = "CONFLICTING"
		}
	}

	approvedCount, changesReqCount := parseReviewCounts(reviews)
	checkConclusion, checkStatus := getCheckConclusion(checks)

	createdAt, _ := time.Parse(time.RFC3339, pr.CreatedAt)
	updatedAt, _ := time.Parse(time.RFC3339, pr.UpdatedAt)

	return &PRInfo{
		Number:                pr.Number,
		Title:                 pr.Title,
		Body:                  pr.Body,
		HeadRef:               pr.Head.Ref,
		BaseRef:               pr.Base.Ref,
		State:                 state,
		Author:                pr.User.Login,
		Labels:                labels,
		HTMLURL:               pr.HTMLURL,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		IsDraft:               pr.Draft,
		Mergeable:             mergeableStr,
		Additions:             pr.Additions,
		Deletions:             pr.Deletions,
		ChangedFiles:          pr.ChangedFiles,
		ApprovedCount:         approvedCount,
		ChangesRequestedCount: changesReqCount,
		CheckConclusion:       checkConclusion,
		CheckStatus:           checkStatus,
	}, nil
}

// fetchPRReviews returns reviews for a PR; returns nil on error (degraded mode).
func fetchPRReviews(ctx context.Context, owner, repo string, prNumber int) []ghReviewItem {
	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	req, err := newGHRequest(ctx, apiPath)
	if err != nil {
		return nil
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	var reviews []ghReviewItem
	if err := json.NewDecoder(resp.Body).Decode(&reviews); err != nil {
		return nil
	}
	return reviews
}

// fetchCommitChecks returns a unified list of check-runs and commit statuses for a SHA.
func fetchCommitChecks(ctx context.Context, owner, repo, sha string) []ghStatusCheckItem {
	if sha == "" {
		return nil
	}
	var items []ghStatusCheckItem

	// GitHub Actions / app check-runs.
	crPath := fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), sha)
	if req, err := newGHRequest(ctx, crPath); err == nil {
		if resp, doErr := ghHTTPClient.Do(req); doErr == nil {
			if resp.StatusCode == http.StatusOK {
				var result struct {
					CheckRuns []struct {
						Name       string `json:"name"`
						Status     string `json:"status"`
						Conclusion string `json:"conclusion"`
					} `json:"check_runs"`
				}
				if jsonErr := json.NewDecoder(resp.Body).Decode(&result); jsonErr == nil {
					for _, cr := range result.CheckRuns {
						items = append(items, ghStatusCheckItem{Name: cr.Name, Status: cr.Status, Conclusion: cr.Conclusion})
					}
				}
			}
			resp.Body.Close()
		}
	}

	// Legacy commit statuses.
	csPath := fmt.Sprintf("repos/%s/%s/commits/%s/statuses?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), sha)
	if req, err := newGHRequest(ctx, csPath); err == nil {
		if resp, doErr := ghHTTPClient.Do(req); doErr == nil {
			if resp.StatusCode == http.StatusOK {
				var statuses []struct {
					State   string `json:"state"`
					Context string `json:"context"`
				}
				if jsonErr := json.NewDecoder(resp.Body).Decode(&statuses); jsonErr == nil {
					for _, s := range statuses {
						items = append(items, ghStatusCheckItem{Name: s.Context, State: s.State})
					}
				}
			}
			resp.Body.Close()
		}
	}

	return items
}

// parseReviewCounts derives approved/changes-requested counts from review items.
// Uses latest non-dismissed, non-comment state per reviewer.
func parseReviewCounts(reviews []ghReviewItem) (approved, changesRequested int) {
	latestState := make(map[string]string)
	for _, r := range reviews {
		login := r.User.Login
		state := strings.ToUpper(r.State)
		if state == "DISMISSED" {
			delete(latestState, login)
			continue
		}
		// COMMENTED does not override a blocking or approving review
		if state == "COMMENTED" {
			continue
		}
		latestState[login] = state
	}
	for _, state := range latestState {
		switch state {
		case "APPROVED":
			approved++
		case "CHANGES_REQUESTED":
			changesRequested++
		}
	}
	return
}

// getCheckConclusion derives a single conclusion from statusCheckRollup items.
func getCheckConclusion(checks []ghStatusCheckItem) (conclusion, status string) {
	if len(checks) == 0 {
		return "", ""
	}
	hasInProgress := false
	hasFailure := false
	allSuccess := true

	for _, check := range checks {
		c := strings.ToLower(check.Conclusion)
		s := strings.ToLower(check.Status)
		st := strings.ToLower(check.State)
		if c == "" {
			c = st
		}
		switch {
		case c == "failure" || c == "error" || c == "action_required" || c == "timed_out":
			hasFailure = true
			allSuccess = false
		case c == "success":
			// success
		case s == "in_progress" || s == "queued" || c == "pending":
			hasInProgress = true
			allSuccess = false
		default:
			allSuccess = false
		}
	}
	if hasFailure {
		return "failure", "completed"
	}
	if hasInProgress {
		return "pending", "in_progress"
	}
	if allSuccess {
		return "success", "completed"
	}
	return "neutral", "completed"
}

// GetPRForBranchConditional is GetPRForBranch with ETag conditional request support.
// Pass the previously returned newEtag (empty string for first call).
// Returns (nil, etag, false, nil) on 304 Not Modified — caller should treat as unchanged.
func GetPRForBranchConditional(ctx context.Context, owner, repo, branch, etag string) (info *PRInfo, newEtag string, changed bool, err error) {
	apiPath := fmt.Sprintf("repos/%s/%s/pulls?head=%s&state=all&per_page=10",
		url.PathEscape(owner), url.PathEscape(repo),
		url.QueryEscape(owner+":"+branch))

	req, err := newGHRequest(ctx, apiPath)
	if err != nil {
		return nil, etag, false, fmt.Errorf("build PR list request: %w", err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return nil, etag, false, fmt.Errorf("PR list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, false, nil
	}

	respEtag := resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, etag, false, fmt.Errorf("GitHub API: unauthorized (401)")
	}
	if resp.StatusCode == http.StatusForbidden {
		if resp.Header.Get("Retry-After") != "" {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil, etag, false, fmt.Errorf("GitHub API: secondary rate limit (403)")
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			_, _ = io.Copy(io.Discard, resp.Body)
			return nil, etag, false, fmt.Errorf("GitHub API: primary rate limit exhausted (403)")
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, etag, false, fmt.Errorf("GitHub API: forbidden (403)")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, etag, false, fmt.Errorf("GitHub API: rate limited (429)")
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, etag, false, fmt.Errorf("GitHub API returned status %d for PR list", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, etag, false, fmt.Errorf("read PR list response: %w", err)
	}

	var prs []struct {
		Number    int    `json:"number"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(body, &prs); err != nil {
		return nil, etag, false, fmt.Errorf("parse PR list: %w", err)
	}
	if len(prs) == 0 {
		return nil, respEtag, true, ErrNoPR
	}

	sort.Slice(prs, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, prs[i].UpdatedAt)
		tj, _ := time.Parse(time.RFC3339, prs[j].UpdatedAt)
		return ti.After(tj)
	})

	prInfo, err := GetPRInfoCtx(ctx, owner, repo, prs[0].Number)
	if err != nil {
		return nil, respEtag, false, err
	}
	return prInfo, respEtag, true, nil
}

// GetPRForBranch finds the GitHub PR associated with a branch.
// Uses the GitHub REST API directly to avoid forkExec lock contention.
// Returns ErrNoPR when no pull request exists for the branch.
func GetPRForBranch(ctx context.Context, owner, repo, branch string) (*PRInfo, error) {
	apiPath := fmt.Sprintf("repos/%s/%s/pulls?head=%s&state=all&per_page=10",
		url.PathEscape(owner), url.PathEscape(repo),
		url.QueryEscape(owner+":"+branch))

	req, err := newGHRequest(ctx, apiPath)
	if err != nil {
		return nil, fmt.Errorf("build PR list request: %w", err)
	}

	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PR list request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("GitHub API: unauthorized (401) – run 'gh auth login'")
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("GitHub API: forbidden (403) – check token permissions")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d for PR list", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read PR list response: %w", err)
	}

	var prs []struct {
		Number    int    `json:"number"`
		UpdatedAt string `json:"updated_at"`
	}
	if err := json.Unmarshal(body, &prs); err != nil {
		return nil, fmt.Errorf("parse PR list: %w", err)
	}
	if len(prs) == 0 {
		return nil, ErrNoPR
	}

	// Use most recently updated PR when multiple PRs target the same branch.
	sort.Slice(prs, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, prs[i].UpdatedAt)
		tj, _ := time.Parse(time.RFC3339, prs[j].UpdatedAt)
		return ti.After(tj)
	})

	return GetPRInfoCtx(ctx, owner, repo, prs[0].Number)
}

// IsForkRepo reports whether the given repo is a fork of another repository.
func IsForkRepo(ctx context.Context, owner, repo string) (bool, error) {
	if err := CheckGHAuth(); err != nil {
		return false, err
	}
	apiPath := fmt.Sprintf("repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	req, err := newGHRequest(ctx, apiPath)
	if err != nil {
		return false, fmt.Errorf("build repo request: %w", err)
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("repo request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return false, fmt.Errorf("GitHub API: status %d for repo info", resp.StatusCode)
	}
	var r struct {
		Fork bool `json:"fork"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return false, fmt.Errorf("parse repo response: %w", err)
	}
	return r.Fork, nil
}

// GetPRComments fetches all comments on a pull request (issue comments + review comments).
func GetPRComments(owner, repo string, prNumber int) ([]PRComment, error) {
	if err := CheckGHAuth(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var comments []PRComment

	// Issue comments (general discussion).
	issuePath := fmt.Sprintf("repos/%s/%s/issues/%d/comments?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	if req, err := newGHRequest(ctx, issuePath); err == nil {
		if resp, doErr := ghHTTPClient.Do(req); doErr == nil {
			if resp.StatusCode == http.StatusOK {
				var items []struct {
					ID        int    `json:"id"`
					Body      string `json:"body"`
					CreatedAt string `json:"created_at"`
					User      struct {
						Login string `json:"login"`
					} `json:"user"`
				}
				if jsonErr := json.NewDecoder(resp.Body).Decode(&items); jsonErr == nil {
					for _, item := range items {
						createdAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
						comments = append(comments, PRComment{
							ID:        item.ID,
							Author:    item.User.Login,
							Body:      item.Body,
							CreatedAt: createdAt,
						})
					}
				}
			}
			resp.Body.Close()
		}
	}

	// Review comments (inline code comments).
	reviewPath := fmt.Sprintf("repos/%s/%s/pulls/%d/comments?per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	if req, err := newGHRequest(ctx, reviewPath); err == nil {
		if resp, doErr := ghHTTPClient.Do(req); doErr == nil {
			if resp.StatusCode == http.StatusOK {
				var items []struct {
					ID        int    `json:"id"`
					Body      string `json:"body"`
					CreatedAt string `json:"created_at"`
					Path      string `json:"path"`
					Line      int    `json:"line"`
					User      struct {
						Login string `json:"login"`
					} `json:"user"`
				}
				if jsonErr := json.NewDecoder(resp.Body).Decode(&items); jsonErr == nil {
					for _, item := range items {
						createdAt, _ := time.Parse(time.RFC3339, item.CreatedAt)
						comments = append(comments, PRComment{
							ID:        item.ID,
							Author:    item.User.Login,
							Body:      item.Body,
							CreatedAt: createdAt,
							Path:      item.Path,
							Line:      item.Line,
							IsReview:  item.Path != "",
						})
					}
				}
			}
			resp.Body.Close()
		}
	}

	return comments, nil
}

// GetPRDiff fetches the diff for a pull request using the GitHub diff media type.
func GetPRDiff(owner, repo string, prNumber int) (string, error) {
	if err := CheckGHAuth(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	req, err := newGHRequest(ctx, apiPath)
	if err != nil {
		return "", fmt.Errorf("build diff request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.diff")

	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("diff request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("GitHub API: status %d for diff", resp.StatusCode)
	}
	diffBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read diff response: %w", err)
	}
	return string(diffBody), nil
}

// PostPRComment posts a comment on a pull request.
func PostPRComment(owner, repo string, prNumber int, body string) error {
	if err := CheckGHAuth(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	apiPath := fmt.Sprintf("repos/%s/%s/issues/%d/comments",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("marshal comment: %w", err)
	}
	req, err := newGHWriteRequest(ctx, http.MethodPost, apiPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build comment request: %w", err)
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post comment failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("GitHub API: status %d for post comment", resp.StatusCode)
	}
	return nil
}

// MergePR merges a pull request. method must be "merge", "squash", or "rebase".
func MergePR(owner, repo string, prNumber int, method string) error {
	if err := CheckGHAuth(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mergeMethod := "merge"
	switch method {
	case "squash", "rebase":
		mergeMethod = method
	}

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d/merge",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	payload, err := json.Marshal(map[string]string{"merge_method": mergeMethod})
	if err != nil {
		return fmt.Errorf("marshal merge request: %w", err)
	}
	req, err := newGHWriteRequest(ctx, http.MethodPut, apiPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build merge request: %w", err)
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("merge request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API: status %d for merge", resp.StatusCode)
	}
	return nil
}

// ClosePR closes a pull request without merging.
func ClosePR(owner, repo string, prNumber int) error {
	if err := CheckGHAuth(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	apiPath := fmt.Sprintf("repos/%s/%s/pulls/%d",
		url.PathEscape(owner), url.PathEscape(repo), prNumber)
	payload, err := json.Marshal(map[string]string{"state": "closed"})
	if err != nil {
		return fmt.Errorf("marshal close request: %w", err)
	}
	req, err := newGHWriteRequest(ctx, http.MethodPatch, apiPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build close request: %w", err)
	}
	resp, err := ghHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("close request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API: status %d for close", resp.StatusCode)
	}
	return nil
}

// GetOwnerRepoFromRemote returns a RepoRef for a local git repository by
// reading the origin remote URL and parsing it. Returns an invalid zero-value
// RepoRef (not an error) when the remote is not a GitHub URL.
func GetOwnerRepoFromRemote(repoPath string) (RepoRef, error) {
	remoteURL, err := sessiongit.RemoteURL(repoPath, "origin")
	if err != nil {
		return RepoRef{}, err
	}
	ref, parseErr := ParseGitHubRef(remoteURL)
	if parseErr != nil {
		return RepoRef{}, nil // not a GitHub URL — callers check IsValid()
	}
	r, _ := NewRepoRef(ref.Owner, ref.Repo)
	return r, nil
}

// GeneratePRPrompt generates a context prompt from PR information
// This can be used to initialize a Claude Code session with PR context
func GeneratePRPrompt(pr *PRInfo, includeDescription bool) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Working on PR #%d: %s\n", pr.Number, pr.Title)
	fmt.Fprintf(&sb, "Branch: %s → %s\n", pr.HeadRef, pr.BaseRef)
	fmt.Fprintf(&sb, "Author: %s | State: %s\n", pr.Author, pr.State)

	if pr.ChangedFiles > 0 {
		fmt.Fprintf(&sb, "Changes: +%d/-%d across %d files\n", pr.Additions, pr.Deletions, pr.ChangedFiles)
	}

	if len(pr.Labels) > 0 {
		fmt.Fprintf(&sb, "Labels: %s\n", strings.Join(pr.Labels, ", "))
	}

	if includeDescription && pr.Body != "" {
		sb.WriteString("\n## PR Description\n")
		sb.WriteString(pr.Body)
		sb.WriteString("\n")
	}

	return sb.String()
}
