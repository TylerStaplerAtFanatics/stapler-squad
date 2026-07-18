package services

import (
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

// ─── SubmitManualReview PASS→done guard (2026-07-18 finding) ──────────────────
//
// docs/tasks/backlog-feature-improvement.md's 2026-07-18 update: SubmitManualReview
// transitions review->done via the storage layer directly
// (s.storage.TransitionBacklogItemStatus), bypassing the guarded
// TransitionBacklogItemStatus RPC handler's ErrPRRequired check entirely. These
// tests cover both the pre-existing intended behavior (nothing to ship — PASS
// still marks done) and the new guard (unshipped code — stays in review).

// TestSubmitManualReview_PassNoUnshippedCode_TransitionsToDone is the happy-path
// regression test: an item with no work-session commits (nothing to ship) must
// still auto-transition straight to done on a PASS manual review, matching the
// pre-existing behavior this guard must not regress.
func TestSubmitManualReview_PassNoUnshippedCode_TransitionsToDone(t *testing.T) {
	svc := newBacklogService(t)

	created, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title:        "item with nothing to ship",
		SkipPlanning: true,
		AcceptanceCriteria: []*sessionv1.AcCriterion{
			{Index: 0, Text: "test", Status: "pending"},
		},
	}))
	require.NoError(t, err)
	itemID := created.Msg.Item.Id

	for _, status := range []string{
		string(session.BacklogStatusReady),
		string(session.BacklogStatusInProgress),
		string(session.BacklogStatusReview),
	} {
		_, err = svc.TransitionBacklogItemStatus(t.Context(), connect.NewRequest(&sessionv1.TransitionBacklogItemStatusRequest{
			ItemId:       itemID,
			TargetStatus: status,
		}))
		require.NoError(t, err)
	}

	resp, err := svc.SubmitManualReview(t.Context(), connect.NewRequest(&sessionv1.SubmitManualReviewRequest{
		ItemId:         itemID,
		OverallOutcome: "PASS",
		Summary:        "looks good",
	}))
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusDone), resp.Msg.Item.Status,
		"PASS manual review with nothing to ship should still auto-transition to done")
}

// TestSubmitManualReview_PassWithUnshippedCode_StaysInReviewForShipPR is the
// regression test for the guard itself: a PASS manual review must not mark an
// item done while its work session has committed code that was never pushed or
// turned into a PR — it must stay in review so the new "Ship PR" action
// (backlog_service_ship.go) can recover it.
func TestSubmitManualReview_PassWithUnshippedCode_StaysInReviewForShipPR(t *testing.T) {
	svc := newBacklogService(t)
	storage := svc.storage

	created, err := svc.CreateBacklogItem(t.Context(), connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
		Title:        "item with unshipped code",
		SkipPlanning: true,
		AcceptanceCriteria: []*sessionv1.AcCriterion{
			{Index: 0, Text: "test", Status: "pending"},
		},
	}))
	require.NoError(t, err)
	itemID := created.Msg.Item.Id

	for _, status := range []string{
		string(session.BacklogStatusReady),
		string(session.BacklogStatusInProgress),
	} {
		_, err = svc.TransitionBacklogItemStatus(t.Context(), connect.NewRequest(&sessionv1.TransitionBacklogItemStatusRequest{
			ItemId:       itemID,
			TargetStatus: status,
		}))
		require.NoError(t, err)
	}

	workIS, err := storage.CreateItemSession(t.Context(), session.ItemSessionData{
		ItemID:      itemID,
		SessionUUID: "work-session-uuid",
		SessionRole: session.SessionRoleWork,
	})
	require.NoError(t, err)
	require.NoError(t, storage.UpdateItemSessionGitActivity(t.Context(), workIS.ID, "abc123", "wip", time.Now(), 1))

	_, err = svc.TransitionBacklogItemStatus(t.Context(), connect.NewRequest(&sessionv1.TransitionBacklogItemStatusRequest{
		ItemId:       itemID,
		TargetStatus: string(session.BacklogStatusReview),
	}))
	require.NoError(t, err)

	resp, err := svc.SubmitManualReview(t.Context(), connect.NewRequest(&sessionv1.SubmitManualReviewRequest{
		ItemId:         itemID,
		OverallOutcome: "PASS",
		Summary:        "looks good",
	}))
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusReview), resp.Msg.Item.Status,
		"PASS manual review with unshipped work-session code and no PR must stay in review for the Ship PR action")
}
