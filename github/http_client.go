package github

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/tstapler/stapler-squad/log"
)

// ghHTTPClient is the shared HTTP client used for all native GitHub REST calls.
// The 30-second timeout matches the existing gh CLI call timeout.
var ghHTTPClient = &http.Client{Timeout: 30 * time.Second}

// getGHToken returns a GitHub personal access token for native HTTP calls.
// Precedence: GITHUB_TOKEN env → GH_TOKEN env.
// Returns an empty string (not an error) when no token source is available so
// callers can decide whether to degrade gracefully.
func getGHToken(_ context.Context) string {
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	return ""
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

// newGHPostRequest creates an authenticated POST request to the GitHub API with a JSON body.
func newGHPostRequest(ctx context.Context, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.github.com/"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if tok := getGHToken(ctx); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

// checkRateLimitHeaders inspects GitHub rate-limit and SSO headers and logs warnings.
// Callers should invoke this on every response from the GitHub API.
func checkRateLimitHeaders(resp *http.Response) {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	if remaining != "" {
		n, err := strconv.Atoi(remaining)
		if err == nil && n < 100 {
			log.Warn("GitHub API rate limit low", "remaining", n,
				"reset", resp.Header.Get("X-RateLimit-Reset"))
		}
	}
	if retry := resp.Header.Get("Retry-After"); retry != "" {
		log.Warn("GitHub API Retry-After header present", "retry_after", retry)
	}
	if sso := resp.Header.Get("X-GitHub-Sso"); sso != "" {
		log.Warn("GitHub SSO enforcement header present", "x_github_sso", sso)
	}
}
