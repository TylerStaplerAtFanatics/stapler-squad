package services

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tstapler/stapler-squad/pkg/events"
	"github.com/tstapler/stapler-squad/session"
	"github.com/tstapler/stapler-squad/session/domain"
)

// --- Story 2.1.2: rework_cap durable write (notifyReworkCapHit) ---

// TestNotifyReworkCapHit_should_markStuckReworkCapImmediately_When_CapHit
// verifies that hitting the rework cap writes a durable rework_cap stuck row
// (threshold 0 — the cap hit is a discrete, definitive event) with a
// cap-describing context, in addition to the existing notification.
func TestNotifyReworkCapHit_should_markStuckReworkCapImmediately_When_CapHit(t *testing.T) {
	storage := createTestStorage(t)
	ctx := context.Background()

	item, err := storage.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "Rework cap test item",
		Status: string(session.BacklogStatusReview),
	})
	require.NoError(t, err)

	svc := NewBacklogService(storage, nil, nil, nil)
	svc.notifyReworkCapHit(ctx, item.ID, item.Title, session.BacklogStatusReview, "after a failed review verdict")

	open, err := storage.FindOpenStuckStates(ctx)
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, domain.StuckReasonReworkCap, open[0].Reason)
	assert.Equal(t, item.ID, open[0].ItemID)
	assert.Contains(t, open[0].Context, "rework cap")
	assert.NotNil(t, open[0].NotifiedAt, "dedup must be pre-set since the notification already fired")
}

// TestNotifyReworkCapHit_should_stillPublishNotification_When_MarkStuckReturnsError
// verifies the durable write is additive, not a gate: when MarkStuck errors
// (forced here via an invalid item ID so the ent UUID parse fails), the
// operator notification must still publish — a storage hiccup must never
// silently suppress the cap-hit signal.
func TestNotifyReworkCapHit_should_stillPublishNotification_When_MarkStuckReturnsError(t *testing.T) {
	storage := createTestStorage(t)
	ctx := context.Background()

	svc := NewBacklogService(storage, nil, nil, nil)
	bus := events.NewEventBus(4)
	svc.SetEventBus(bus)

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, _ := bus.Subscribe(subCtx)

	svc.notifyReworkCapHit(ctx, "not-a-valid-item-uuid", "Broken Item", session.BacklogStatusReview, "after a failed review verdict")

	select {
	case ev := <-ch:
		assert.Equal(t, events.EventNotification, ev.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected a notification event even though MarkStuck errored")
	}
}

// TestNotifyReworkCapHit_should_persistRowSurvivingRestart_When_CapHit verifies
// the rework_cap row survives a simulated server restart (DB close/reopen
// from the same file) — the whole point of moving off the in-memory
// notify-once map (root cause #3).
func TestNotifyReworkCapHit_should_persistRowSurvivingRestart_When_CapHit(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "rework-cap-restart.db")

	var itemID string
	func() {
		repo, err := session.NewEntRepository(session.WithDatabasePath(dbPath))
		require.NoError(t, err)
		defer repo.Close()
		storage, err := session.NewStorageWithRepository(repo)
		require.NoError(t, err)

		item, err := storage.CreateBacklogItem(context.Background(), session.BacklogItemData{
			Title:  "Restart-surviving rework cap item",
			Status: string(session.BacklogStatusPRPending),
		})
		require.NoError(t, err)
		itemID = item.ID

		svc := NewBacklogService(storage, nil, nil, nil)
		svc.notifyReworkCapHit(context.Background(), itemID, item.Title, session.BacklogStatusPRPending, "while fixing PR #7")
	}()

	repo2, err := session.NewEntRepository(session.WithDatabasePath(dbPath))
	require.NoError(t, err)
	defer repo2.Close()

	open, err := repo2.FindOpenStuckStates(context.Background())
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, itemID, open[0].ItemID)
	assert.Equal(t, domain.StuckReasonReworkCap, open[0].Reason)
}

// --- AutoReopenForPRFix: live-bug regression (ReconcilePRPending churn) ---

// TestAutoReopenForPRFix_ActiveWorkSession_SkipsWithoutStatusChurn is the regression
// test for a live production incident: ReconcilePRPending calls AutoReopenForPRFix on
// every ~60s tick for any pr_pending item with failing CI, with no check for whether a
// fix is already in flight. When a work session was still genuinely active (a real
// multi-hour autonomous session, not dead), the old code transitioned pr_pending->
// in_progress, discovered SpawnSessionFromItem was blocked by that same active session,
// and rolled back to pr_pending — writing two BacklogStatusEvent rows every tick,
// forever, with zero progress. AutoReopenForPRFix must now check for an active work
// session FIRST and return early with no status transition at all.
func TestAutoReopenForPRFix_ActiveWorkSession_SkipsWithoutStatusChurn(t *testing.T) {
	storage := createTestStorage(t)
	creator := &mockSessionCreator{}
	svc := NewBacklogService(storage, creator, nil, nil)
	stopper := &mockSessionStopper{liveUUIDs: map[string]bool{"active-work-uuid": true}}
	svc.SetSessionStopper(stopper)

	repoPath := t.TempDir()
	initGitRepoWithCommit(t, repoPath)

	item, err := storage.CreateBacklogItem(context.Background(), session.BacklogItemData{
		Title:    "PR-pending item with an active fix session",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusPRPending),
		PrNumber: 42,
		PrURL:    "https://github.com/example/repo/pull/42",
	})
	require.NoError(t, err)
	_, err = storage.CreateItemSession(context.Background(), session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: "active-work-uuid",
		SessionRole: session.SessionRoleWork,
	})
	require.NoError(t, err)

	reopenErr := svc.AutoReopenForPRFix(context.Background(), item.ID, "CI failing: tests broke")
	require.NoError(t, reopenErr, "must not error — this is the expected 'already in flight' outcome, not a failure")

	fetched, err := storage.GetBacklogItem(context.Background(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusPRPending), fetched.Status, "status must never churn while a fix session is already active")
	assert.Empty(t, creator.calls, "no new session should be spawned while one is already active")
}

// TestAutoReopenForPRFix_DeadWorkSession_TombstonesThenReopens verifies the other half:
// a work session that IS confirmed dead (not live) must be tombstoned automatically so
// the reopen can proceed normally, rather than blocking forever like the bug above.
func TestAutoReopenForPRFix_DeadWorkSession_TombstonesThenReopens(t *testing.T) {
	storage := createTestStorage(t)
	creator := &mockSessionCreator{}
	svc := NewBacklogService(storage, creator, nil, nil)
	stopper := &mockSessionStopper{liveUUIDs: map[string]bool{}} // nothing is live
	svc.SetSessionStopper(stopper)

	repoPath := t.TempDir()
	initGitRepoWithCommit(t, repoPath)

	item, err := storage.CreateBacklogItem(context.Background(), session.BacklogItemData{
		Title:    "PR-pending item with a dead prior session",
		RepoPath: repoPath,
		Status:   string(session.BacklogStatusPRPending),
		PrNumber: 43,
		PrURL:    "https://github.com/example/repo/pull/43",
	})
	require.NoError(t, err)
	deadIS, err := storage.CreateItemSession(context.Background(), session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: "dead-work-uuid",
		SessionRole: session.SessionRoleWork,
	})
	require.NoError(t, err)

	reopenErr := svc.AutoReopenForPRFix(context.Background(), item.ID, "CI failing: tests broke")
	require.NoError(t, reopenErr)

	fetched, err := storage.GetBacklogItem(context.Background(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusInProgress), fetched.Status, "reopen must proceed once the dead session is cleared")
	assert.Len(t, creator.calls, 1, "a new fix session must be spawned")

	deadFetched, err := storage.GetItemSession(context.Background(), deadIS.ID)
	require.NoError(t, err)
	assert.NotNil(t, deadFetched.EndedAt, "the dead session must be tombstoned")
}
