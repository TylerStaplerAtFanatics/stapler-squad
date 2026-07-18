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

// ─── pipeline_mode presence gating (Story 1.4.4) ───────────────────────────────

// TestUpdateBacklogItem_should_PreserveExistingPipelineMode_When_FieldOmittedFromRequest
// is the regression test for the proto3-bool-clobbering bug class this field was
// specifically designed to avoid: an UpdateBacklogItem request that omits
// pipeline_mode entirely (req.Msg.PipelineMode == nil) must never clobber the
// item's existing stored mode back to "".
func TestUpdateBacklogItem_should_PreserveExistingPipelineMode_When_FieldOmittedFromRequest(t *testing.T) {
	svc := newBacklogService(t)

	quick := "quick"
	created, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title:        "item using quick mode",
		PipelineMode: &quick,
	}))
	require.NoError(t, err)
	require.NotNil(t, created.Msg.Item.PipelineMode)
	require.Equal(t, "quick", *created.Msg.Item.PipelineMode)

	// pipeline_mode is deliberately left unset (nil) on this request.
	updated, err := svc.UpdateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.UpdateBacklogItemRequest{
		ItemId: created.Msg.Item.Id,
		Title:  "renamed item",
	}))
	require.NoError(t, err)
	require.NotNil(t, updated.Msg.Item.PipelineMode)
	assert.Equal(t, "quick", *updated.Msg.Item.PipelineMode, "omitted pipeline_mode must not clobber the item's existing mode")
}

// TestUpdateBacklogItem_should_ResetPipelineModeToEmptyString_When_FieldExplicitlySetToEmptyString
// proves the other half of presence-gating: an explicitly-present empty string
// (req.Msg.PipelineMode != nil && *req.Msg.PipelineMode == "") is a real reset
// request, distinct from "omitted", and must be honored.
func TestUpdateBacklogItem_should_ResetPipelineModeToEmptyString_When_FieldExplicitlySetToEmptyString(t *testing.T) {
	svc := newBacklogService(t)

	quick := "quick"
	created, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title:        "item using quick mode",
		PipelineMode: &quick,
	}))
	require.NoError(t, err)

	empty := ""
	updated, err := svc.UpdateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.UpdateBacklogItemRequest{
		ItemId:       created.Msg.Item.Id,
		PipelineMode: &empty,
	}))
	require.NoError(t, err)
	require.NotNil(t, updated.Msg.Item.PipelineMode)
	assert.Equal(t, "", *updated.Msg.Item.PipelineMode, "explicit empty pipeline_mode must reset the item's mode")
}

// TestCreateBacklogItem_should_SetPipelineModeFromRequest_When_FieldPresent verifies
// CreateBacklogItem persists a non-default pipeline_mode supplied on the request
// (Story 1.4.4).
func TestCreateBacklogItem_should_SetPipelineModeFromRequest_When_FieldPresent(t *testing.T) {
	svc := newBacklogService(t)

	quick := "quick"
	resp, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title:        "item using quick mode",
		PipelineMode: &quick,
	}))
	require.NoError(t, err)
	require.NotNil(t, resp.Msg.Item.PipelineMode)
	assert.Equal(t, "quick", *resp.Msg.Item.PipelineMode)
}

// ─── auto_create_pr policy flag (opt-in "auto-create PR on Complete") ─────────

// TestCreateBacklogItem_should_DefaultAutoCreatePrToFalse_When_FieldOmitted is the
// default-behavior guard for the opt-in AutoCreatePR policy — an item created
// without the flag must not have it silently enabled.
func TestCreateBacklogItem_should_DefaultAutoCreatePrToFalse_When_FieldOmitted(t *testing.T) {
	svc := newBacklogService(t)

	resp, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title: "item without auto-create-pr",
	}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.Item.AutoCreatePr)
}

// TestCreateBacklogItem_should_PersistAutoCreatePr_When_FieldSetTrue verifies
// CreateBacklogItem persists an explicitly-enabled auto_create_pr flag, and
// UpdateBacklogItem round-trips it (unconditional-bool-wrap pattern, same as
// SkipReviewGate/SkipPlanning/AutoSpawnSession).
func TestCreateBacklogItem_should_PersistAutoCreatePr_When_FieldSetTrue(t *testing.T) {
	svc := newBacklogService(t)

	created, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title:        "item with auto-create-pr",
		AutoCreatePr: true,
	}))
	require.NoError(t, err)
	assert.True(t, created.Msg.Item.AutoCreatePr)

	updated, err := svc.UpdateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.UpdateBacklogItemRequest{
		ItemId:       created.Msg.Item.Id,
		AutoCreatePr: true,
	}))
	require.NoError(t, err)
	assert.True(t, updated.Msg.Item.AutoCreatePr, "UpdateBacklogItem must persist auto_create_pr")
}

// TestTransitionBacklogItemStatus_should_BlockDone_When_PrURLSetButCommitNotOnMain is
// the regression test for the bug Tyler reported: an item could reach "done" once a
// PrURL existed at all, regardless of whether that PR was ever actually merged — an
// open, unmerged PR still has PrURL set, so it silently satisfied the old guard.
func TestTransitionBacklogItemStatus_should_BlockDone_When_PrURLSetButCommitNotOnMain(t *testing.T) {
	_, repoPath := setupPRFixSyncRepo(t)

	// A commit that exists only on a feature branch — never merged anywhere —
	// mirroring a PR that was opened but never actually merged.
	runGitTestCmd(t, repoPath, "checkout", "-b", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "feature.txt"), []byte("unshipped work\n"), 0o644))
	runGitTestCmd(t, repoPath, "add", "feature.txt")
	runGitTestCmd(t, repoPath, "commit", "-m", "work that never actually merged")
	unshippedSHA := strings.TrimSpace(runGitTestCmd(t, repoPath, "rev-parse", "HEAD"))

	storage, repo := createTestStorageWithRepo(t)
	svc := NewBacklogService(storage, nil, nil, nil, nil, nil)

	item, err := storage.CreateBacklogItem(t.Context(), session.BacklogItemData{
		Title:    "item with an open, unmerged PR",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusReview),
		PrURL:    "https://github.com/example/repo/pull/999", // set, but the PR was never merged
	})
	require.NoError(t, err)

	// A PASS review verdict, so the failure below is unambiguously the code-on-main
	// gate (ErrCodeNotOnMain), not the separate, already-covered verdict gate.
	_, err = storage.CreateItemSessionWithVerdict(t.Context(), session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: "review-session",
		SessionRole: session.SessionRoleReview,
	}, session.ReviewVerdictData{
		OverallOutcome: session.ReviewVerdictPass,
	})
	require.NoError(t, err)

	is, err := storage.CreateItemSession(t.Context(), session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: "unmerged-work-session",
		SessionRole: session.SessionRoleWork,
	})
	require.NoError(t, err)
	require.NoError(t, repo.UpdateItemSessionGitActivity(t.Context(), is.ID, unshippedSHA, "work that never actually merged", time.Now(), 1))

	_, err = svc.TransitionBacklogItemStatus(t.Context(), connect.NewRequest(&sessionv1.TransitionBacklogItemStatusRequest{
		ItemId:       item.ID,
		TargetStatus: "done",
	}))
	require.Error(t, err, "an item whose only PR was never merged must not reach done just because PrURL is set")
	assert.Contains(t, err.Error(), "must actually be on main")

	fetched, err := storage.GetBacklogItem(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusReview), fetched.Status, "the item must stay in review, not silently reach done")

	// Now actually merge the commit to main — the item must be allowed to reach
	// done once the code is verifiably shipped.
	runGitTestCmd(t, repoPath, "checkout", "main")
	runGitTestCmd(t, repoPath, "merge", "--no-edit", "feature")

	_, err = svc.TransitionBacklogItemStatus(t.Context(), connect.NewRequest(&sessionv1.TransitionBacklogItemStatusRequest{
		ItemId:       item.ID,
		TargetStatus: "done",
	}))
	require.NoError(t, err, "once the commit is actually merged to main, done must be allowed")

	fetched, err = storage.GetBacklogItem(t.Context(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusDone), fetched.Status)
}
