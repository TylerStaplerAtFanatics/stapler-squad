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
