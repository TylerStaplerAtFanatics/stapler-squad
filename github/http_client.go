package github

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tstapler/stapler-squad/executor/safeexec"
	"github.com/tstapler/stapler-squad/log"
	"golang.org/x/sync/singleflight"
)

// ghHTTPClient is the shared HTTP client used for all native GitHub REST calls.
// The 30-second timeout matches the existing gh CLI call timeout.
var ghHTTPClient = &http.Client{Timeout: 30 * time.Second}

const ghTokenTTL = time.Hour

type tokenResult struct {
	token  string
	expiry time.Time
}

var (
	ghTokenState atomic.Value       // stores tokenResult
	ghTokenGroup singleflight.Group //nolint:exhaustruct
)

// getGHToken returns a GitHub personal access token for native HTTP calls.
// Precedence: GITHUB_TOKEN env → GH_TOKEN env → gh auth token (cached 1 hour).
// Returns an empty string (not an error) when no token source is available so
// callers can decide whether to degrade gracefully.
func getGHToken(ctx context.Context) string {
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}

	if v := ghTokenState.Load(); v != nil {
		if r := v.(tokenResult); time.Now().Before(r.expiry) {
			return r.token
		}
	}

	res, _, _ := ghTokenGroup.Do("token", func() (interface{}, error) {
		cmd := safeexec.CommandContext(ctx, "gh", "auth", "token")
		out, err := cmd.Output()
		tok := ""
		if err == nil {
			tok = strings.TrimSpace(string(out))
		}
		ghTokenState.Store(tokenResult{token: tok, expiry: time.Now().Add(ghTokenTTL)})
		return tok, nil
	})
	if res == nil {
		return ""
	}
	return res.(string)
}

// rateLimitWarningThreshold triggers a warning log when X-RateLimit-Remaining
// drops below this value, indicating that the token is close to exhaustion.
const rateLimitWarningThreshold = 500

// maxRetryAfterSleep caps the duration we will sleep in response to a
// Retry-After header so a misbehaving server cannot block us indefinitely.
const maxRetryAfterSleep = 60 * time.Second

// checkRateLimitHeaders inspects X-RateLimit-Remaining, Retry-After, and
// X-GitHub-Sso headers on any GitHub API response. It returns a non-zero
// duration if the caller should pause before the next request.
//
// This must be called on every response (200, 304, 429, 403, …) so monitoring
// is consistent. The caller is responsible for actually sleeping the returned
// duration.
func checkRateLimitHeaders(resp *http.Response) time.Duration {
	if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining != "" {
		if n, err := strconv.Atoi(remaining); err == nil && n < rateLimitWarningThreshold {
			log.Warn("github API: rate limit running low", "remaining", n)
		}
	}

	if resp.StatusCode == http.StatusForbidden {
		if sso := resp.Header.Get("X-GitHub-Sso"); sso != "" {
			log.Warn("github API: SSO authorization required — re-authorize your token at the URL in X-GitHub-Sso", "url", sso)
		}
	}

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			if secs, err := strconv.Atoi(retryAfter); err == nil && secs > 0 {
				d := time.Duration(secs) * time.Second
				if d > maxRetryAfterSleep {
					d = maxRetryAfterSleep
				}
				log.Warn("github API: rate limited", "status", resp.StatusCode, "retry_after_s", secs, "sleeping_s", d.Seconds())
				return d
			}
		}
	}

	return 0
}

// newGHRequest creates an authenticated GET request to the GitHub REST API.
func newGHRequest(ctx context.Context, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/"+path, nil)
	if err != nil {
		return nil, err
	}
	if tok := getGHToken(ctx); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}
