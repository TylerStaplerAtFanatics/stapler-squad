package github

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"
)

// rateLimitTransport wraps an http.RoundTripper and automatically updates
// DefaultRateLimiter on every GitHub API response. All native GitHub HTTP
// calls go through this transport so rate-limit state is always current.
type rateLimitTransport struct {
	base    http.RoundTripper
	limiter *RateLimiter
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	t.limiter.Update(resp)
	return resp, nil
}

// ghHTTPClient is the shared HTTP client used for all native GitHub REST calls.
// It wraps rateLimitTransport so every response automatically updates
// DefaultRateLimiter — callers never need to inspect rate-limit headers manually.
var ghHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &rateLimitTransport{
		base:    http.DefaultTransport,
		limiter: DefaultRateLimiter,
	},
}

// GhBaseURL is the GitHub REST API base URL. Tests override this to point at
// an httptest.Server so requests never reach the real API.
var GhBaseURL = "https://api.github.com/"

// getGHToken returns a GitHub personal access token for native HTTP calls.
// Precedence: GITHUB_TOKEN env → GH_TOKEN env → OS keychain.
// Returns an empty string (not an error) when no token source is available so
// callers can decide whether to degrade gracefully.
func getGHToken(_ context.Context) string {
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		return tok
	}
	if tok := os.Getenv("GH_TOKEN"); tok != "" {
		return tok
	}
	if tok := GetKeychainToken(); tok != "" {
		return tok
	}
	return ""
}

// newGHRequest creates an authenticated GET request to the GitHub REST API.
func newGHRequest(ctx context.Context, path string) (*http.Request, error) {
	return newGHRequestWithToken(ctx, path, getGHToken(ctx))
}

// newGHRequestWithToken creates a GET request authenticated with an explicit token.
func newGHRequestWithToken(ctx context.Context, path, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GhBaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

// newGHPostRequestWithToken creates a POST request authenticated with an explicit token.
func newGHPostRequestWithToken(ctx context.Context, path string, body io.Reader, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, GhBaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	return req, nil
}

// newGHWriteRequest creates an authenticated request for write operations (POST, PUT, PATCH).
func newGHWriteRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, GhBaseURL+path, body)
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
