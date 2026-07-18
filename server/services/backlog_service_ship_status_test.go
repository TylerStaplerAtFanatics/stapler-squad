package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/session"
)

// attachWorkSessionWithCommit creates a work ItemSession for item, records
// sha as its last commit, and registers worktree data (repoPath, branchName)
// so GetBacklogItemShipStatus can resolve both the commit and the branch.
func attachWorkSessionWithCommit(t *testing.T, storage *session.Storage, repo *session.EntRepository, itemID, sessionUUID, repoPath, worktreePath, branchName, sha string) {
	t.Helper()
	is, err := storage.CreateItemSession(t.Context(), session.ItemSessionData{
		ItemID:      itemID,
		SessionUUID: sessionUUID,
		SessionRole: session.SessionRoleWork,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpdateItemSessionGitActivity(t.Context(), is.ID, sha, "work commit", time.Now(), 1))

	now := time.Now()
	require.NoError(t, repo.Create(t.Context(), session.InstanceData{
		Title:      sessionUUID,
		UUID:       sessionUUID,
		Path:       worktreePath,
		WorkingDir: worktreePath,
		Branch:     branchName,
		Status:     session.Paused,
		Program:    "claude",
		CreatedAt:  now,
		UpdatedAt:  now,
		Worktree: session.GitWorktreeData{
			RepoPath:      repoPath,
			WorktreePath:  worktreePath,
			SessionName:   sessionUUID,
			BranchName:    branchName,
			BaseCommitSHA: sha,
		},
	}))
}

// TestGetBacklogItemShipStatus_should_ReportShippedDirect_When_CommitOnMainNoPrURL
// covers the "committed directly to main, no PR" case — the shipped_via must read
// "direct", and since the branch is main itself, ahead/behind must both be 0.
func TestGetBacklogItemShipStatus_should_ReportShippedDirect_When_CommitOnMainNoPrURL(t *testing.T) {
	_, repoPath := setupPRFixSyncRepo(t)
	sha := strings.TrimSpace(runGitTestCmd(t, repoPath, "rev-parse", "HEAD"))

	storage, repo := createTestStorageWithRepo(t)
	svc := NewBacklogService(storage, nil, nil, nil, nil, nil)

	item, err := storage.CreateBacklogItem(t.Context(), session.BacklogItemData{
		Title:    "direct commit item",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusDone),
	})
	require.NoError(t, err)
	attachWorkSessionWithCommit(t, storage, repo, item.ID, "direct-work", repoPath, repoPath, "main", sha)

	resp, err := svc.GetBacklogItemShipStatus(t.Context(), connect.NewRequest(&sessionv1.GetBacklogItemShipStatusRequest{ItemId: item.ID}))
	require.NoError(t, err)
	st := resp.Msg.Status
	assert.Empty(t, st.Error)
	assert.True(t, st.Shipped)
	assert.Equal(t, "direct", st.ShippedVia)
	assert.Equal(t, sha, st.LastCommitSha)
}

// TestGetBacklogItemShipStatus_should_ReportShippedViaPr_When_PrUrlSetAndMerged
// covers the "opened a PR, it got merged" case — shipped_via must read "pr", and the
// branch (now merged into main) must report zero ahead, since main already contains it.
func TestGetBacklogItemShipStatus_should_ReportShippedViaPr_When_PrUrlSetAndMerged(t *testing.T) {
	_, repoPath := setupPRFixSyncRepo(t)

	runGitTestCmd(t, repoPath, "checkout", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("work\n"), 0o644))
	runGitTestCmd(t, repoPath, "add", "feature.txt")
	runGitTestCmd(t, repoPath, "commit", "-m", "feature work")
	sha := strings.TrimSpace(runGitTestCmd(t, repoPath, "rev-parse", "HEAD"))
	runGitTestCmd(t, repoPath, "checkout", "main")
	runGitTestCmd(t, repoPath, "merge", "--no-ff", "--no-edit", "feature") // simulates a merged PR

	storage, repo := createTestStorageWithRepo(t)
	svc := NewBacklogService(storage, nil, nil, nil, nil, nil)

	item, err := storage.CreateBacklogItem(t.Context(), session.BacklogItemData{
		Title:    "PR-shipped item",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusDone),
	})
	require.NoError(t, err)
	prURL := "https://github.com/example/repo/pull/42"
	_, err = storage.UpdateBacklogItem(t.Context(), item.ID, session.BacklogItemUpdate{PrURL: &prURL}, nil)
	require.NoError(t, err)
	attachWorkSessionWithCommit(t, storage, repo, item.ID, "pr-work", repoPath, repoPath, "feature", sha)

	resp, err := svc.GetBacklogItemShipStatus(t.Context(), connect.NewRequest(&sessionv1.GetBacklogItemShipStatusRequest{ItemId: item.ID}))
	require.NoError(t, err)
	st := resp.Msg.Status
	assert.Empty(t, st.Error)
	assert.True(t, st.Shipped)
	assert.Equal(t, "pr", st.ShippedVia)
	assert.True(t, st.BranchExists, "branch still exists locally in this test repo")
	assert.Equal(t, int32(0), st.AheadOfMain, "main already contains the merged feature branch")
}

// TestGetBacklogItemShipStatus_should_ReportNotShipped_When_BranchNeverMerged covers
// the exact regression case: a PR URL is set but the branch was never actually merged.
func TestGetBacklogItemShipStatus_should_ReportNotShipped_When_BranchNeverMerged(t *testing.T) {
	_, repoPath := setupPRFixSyncRepo(t)

	runGitTestCmd(t, repoPath, "checkout", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("work\n"), 0o644))
	runGitTestCmd(t, repoPath, "add", "feature.txt")
	runGitTestCmd(t, repoPath, "commit", "-m", "feature work, never merged")
	sha := strings.TrimSpace(runGitTestCmd(t, repoPath, "rev-parse", "HEAD"))

	storage, repo := createTestStorageWithRepo(t)
	svc := NewBacklogService(storage, nil, nil, nil, nil, nil)

	item, err := storage.CreateBacklogItem(t.Context(), session.BacklogItemData{
		Title:    "item with an open, unmerged PR",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusReview),
	})
	require.NoError(t, err)
	prURL := "https://github.com/example/repo/pull/999"
	_, err = storage.UpdateBacklogItem(t.Context(), item.ID, session.BacklogItemUpdate{PrURL: &prURL}, nil)
	require.NoError(t, err)
	attachWorkSessionWithCommit(t, storage, repo, item.ID, "unmerged-work", repoPath, repoPath, "feature", sha)

	resp, err := svc.GetBacklogItemShipStatus(t.Context(), connect.NewRequest(&sessionv1.GetBacklogItemShipStatusRequest{ItemId: item.ID}))
	require.NoError(t, err)
	st := resp.Msg.Status
	assert.Empty(t, st.Error)
	assert.False(t, st.Shipped)
	assert.Empty(t, st.ShippedVia)
	assert.True(t, st.BranchExists)
	assert.Equal(t, int32(1), st.AheadOfMain, "the unmerged feature commit must count as ahead of main")
}

// TestGetBacklogItemShipStatus_should_ReturnErrorField_When_NoWorkSessionEverCommitted
// verifies the no-code case reports a descriptive error rather than a false "shipped".
func TestGetBacklogItemShipStatus_should_ReturnErrorField_When_NoWorkSessionEverCommitted(t *testing.T) {
	_, repoPath := setupPRFixSyncRepo(t)
	storage, _ := createTestStorageWithRepo(t)
	svc := NewBacklogService(storage, nil, nil, nil, nil, nil)

	item, err := storage.CreateBacklogItem(t.Context(), session.BacklogItemData{
		Title:    "item with no code",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusDone),
	})
	require.NoError(t, err)

	resp, err := svc.GetBacklogItemShipStatus(t.Context(), connect.NewRequest(&sessionv1.GetBacklogItemShipStatusRequest{ItemId: item.ID}))
	require.NoError(t, err)
	assert.NotEmpty(t, resp.Msg.Status.Error)
	assert.False(t, resp.Msg.Status.Shipped)
}
