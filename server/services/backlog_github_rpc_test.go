package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	gh "github.com/tstapler/stapler-squad/github"
)

// resetGhBaseURL overrides GhBaseURL for a test and returns a restore func.
func resetGhBaseURL(ts *httptest.Server) func() {
	gh.GhBaseURL = ts.URL + "/"
	return func() { gh.GhBaseURL = "https://api.github.com/" }
}

// ─── SearchGitHubRepos ────────────────────────────────────────────────────────

func TestSearchGitHubRepos_NilStorage(t *testing.T) {
	svc := newBacklogServiceNilStorage()
	_, err := svc.SearchGitHubRepos(context.Background(),
		connect.NewRequest(&sessionv1.SearchGitHubReposRequest{}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnavailable, connectErr.Code())
}

func TestSearchGitHubRepos_DefaultLimit(t *testing.T) {
	var capturedPerPage string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPerPage = r.URL.Query().Get("per_page")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer ts.Close()
	defer resetGhBaseURL(ts)()
	t.Setenv("GITHUB_TOKEN", "fake-token")

	svc := newBacklogService(t)
	_, err := svc.SearchGitHubRepos(context.Background(),
		connect.NewRequest(&sessionv1.SearchGitHubReposRequest{Limit: 0}))
	require.NoError(t, err)
	assert.Equal(t, "30", capturedPerPage)
}

func TestSearchGitHubRepos_ReturnsRepos(t *testing.T) {
	repos := []map[string]any{
		{"name": "repo1", "owner": map[string]any{"login": "acme"}, "description": "first", "private": false, "pushed_at": "2024-01-01"},
		{"name": "repo2", "owner": map[string]any{"login": "acme"}, "description": "second", "private": true, "pushed_at": "2024-01-02"},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(repos)
	}))
	defer ts.Close()
	defer resetGhBaseURL(ts)()
	t.Setenv("GITHUB_TOKEN", "fake-token")

	svc := newBacklogService(t)
	resp, err := svc.SearchGitHubRepos(context.Background(),
		connect.NewRequest(&sessionv1.SearchGitHubReposRequest{Limit: 30}))
	require.NoError(t, err)
	require.Len(t, resp.Msg.Repos, 2)
	assert.Equal(t, "acme", resp.Msg.Repos[0].Owner)
	assert.Equal(t, "repo1", resp.Msg.Repos[0].Repo)
	assert.False(t, resp.Msg.Repos[0].IsLocal)
}

// ─── ListGitHubIssues ─────────────────────────────────────────────────────────

func TestListGitHubIssues_NilStorage(t *testing.T) {
	svc := newBacklogServiceNilStorage()
	_, err := svc.ListGitHubIssues(context.Background(),
		connect.NewRequest(&sessionv1.ListGitHubIssuesRequest{Owner: "a", Repo: "b"}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeUnavailable, connectErr.Code())
}

func TestListGitHubIssues_EmptyOwner(t *testing.T) {
	svc := newBacklogService(t)
	_, err := svc.ListGitHubIssues(context.Background(),
		connect.NewRequest(&sessionv1.ListGitHubIssuesRequest{Owner: "", Repo: "repo"}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

func TestListGitHubIssues_EmptyRepo(t *testing.T) {
	svc := newBacklogService(t)
	_, err := svc.ListGitHubIssues(context.Background(),
		connect.NewRequest(&sessionv1.ListGitHubIssuesRequest{Owner: "owner", Repo: ""}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

func TestListGitHubIssues_InvalidOwnerChars(t *testing.T) {
	svc := newBacklogService(t)
	_, err := svc.ListGitHubIssues(context.Background(),
		connect.NewRequest(&sessionv1.ListGitHubIssuesRequest{Owner: "a b", Repo: "repo"}))
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	assert.Equal(t, connect.CodeInvalidArgument, connectErr.Code())
}

func TestListGitHubIssues_DefaultState(t *testing.T) {
	var capturedQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer ts.Close()
	defer resetGhBaseURL(ts)()
	t.Setenv("GITHUB_TOKEN", "fake-token")

	svc := newBacklogService(t)
	_, err := svc.ListGitHubIssues(context.Background(),
		connect.NewRequest(&sessionv1.ListGitHubIssuesRequest{Owner: "tstapler", Repo: "stapler-squad", State: ""}))
	require.NoError(t, err)
	assert.Contains(t, capturedQuery, "state=open", "empty state should default to 'open'")
}

func TestListGitHubIssues_SearchUsesSearchAPI(t *testing.T) {
	var capturedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer ts.Close()
	defer resetGhBaseURL(ts)()
	t.Setenv("GITHUB_TOKEN", "fake-token")

	svc := newBacklogService(t)
	_, err := svc.ListGitHubIssues(context.Background(),
		connect.NewRequest(&sessionv1.ListGitHubIssuesRequest{
			Owner:  "tstapler",
			Repo:   "stapler-squad",
			Search: "login bug",
		}))
	require.NoError(t, err)
	assert.Equal(t, "/search/issues", capturedPath)
}
