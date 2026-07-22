package session_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pkgevents "github.com/tstapler/stapler-squad/pkg/events"
	"github.com/tstapler/stapler-squad/server/services"
	"github.com/tstapler/stapler-squad/session"
)

// This file lives in the external session_test package (not the internal
// session package like ent_repository_backlog_test.go) because it needs to
// import server/services.BacklogItemEventPublisher (the real adapter), which
// itself imports session — an internal test file in package session cannot
// import anything that transitively imports session without Go's "import
// cycle not allowed in test" build error. See Epic 2.1's implementation notes
// for the exact error this avoids.

// newTestEntRepositoryForEvents creates a temporary ent-backed *session.EntRepository,
// mirroring session package's own unexported createTestEntRepository test helper
// (session/ent_repository_test.go), which is not reachable from this external
// test package.
func newTestEntRepositoryForEvents(t *testing.T) (*session.EntRepository, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("test-%d.db", time.Now().UnixNano()))

	repo, err := session.NewEntRepository(session.WithDatabasePath(dbPath))
	require.NoError(t, err)

	cleanup := func() {
		repo.Close()
		os.Remove(dbPath)
		os.Remove(dbPath + "-wal")
		os.Remove(dbPath + "-shm")
	}
	return repo, cleanup
}

// panickingItemChangePublisher is a test double for Task 2.1.1d: its
// PublishItemChanged always panics, simulating a broken publisher wired
// directly (i.e. not wrapped by the real adapter's own recover()), so the
// test can prove a hooked repository method is unaffected by a panic inside
// the publish step regardless of which ItemChangePublisher implementation is
// wired in.
type panickingItemChangePublisher struct{}

func (panickingItemChangePublisher) PublishItemChanged(item *session.BacklogItemData, change session.BacklogItemChange) {
	panic("boom")
}

// TestTransitionBacklogItemStatus_should_publishOldAndNewStatus_When_CASSucceeds
// wires a real *pkgevents.EventBus into the repository via SetItemChangePublisher
// (through the real server/services.BacklogItemEventPublisher adapter) and asserts
// a subscriber receives a BacklogItemChanged event with the correct old/new status
// after a successful CAS transition (Task 2.1.1b, R4 happy path).
func TestTransitionBacklogItemStatus_should_publishOldAndNewStatus_When_CASSucceeds(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "item for status transition publish test",
		Status: string(session.BacklogStatusInProgress),
	})
	require.NoError(t, err)

	_, err = repo.TransitionBacklogItemStatus(ctx, item.ID, session.BacklogStatusDone, &session.BacklogItemPrecondition{
		ExpectedStatus: string(session.BacklogStatusInProgress),
	})
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.Equal(t, pkgevents.EventBacklogItemChanged, ev.Type)
		require.NotNil(t, ev.BacklogItemPayload)
		assert.Equal(t, pkgevents.BacklogChangeStatusTransition, ev.BacklogItemPayload.Kind)
		assert.Equal(t, string(session.BacklogStatusInProgress), ev.BacklogItemPayload.OldStatus)
		assert.Equal(t, string(session.BacklogStatusDone), ev.BacklogItemPayload.NewStatus)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for BacklogItemChanged event")
	}
}

// TestTransitionBacklogItemStatus_should_notPublish_When_CASAffectsZeroRows
// simulates a lost CAS race (a stale expectedOldStatus) and asserts no event
// reaches the subscriber — the precondition-failed path must never publish
// (Task 2.1.1c, R4 error path).
func TestTransitionBacklogItemStatus_should_notPublish_When_CASAffectsZeroRows(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "item for lost-race no-publish test",
		Status: string(session.BacklogStatusInProgress),
	})
	require.NoError(t, err)

	// Stale precondition: the item is actually "in_progress", not "review", so
	// the CAS WHERE clause matches zero rows.
	_, err = repo.TransitionBacklogItemStatus(ctx, item.ID, session.BacklogStatusDone, &session.BacklogItemPrecondition{
		ExpectedStatus: string(session.BacklogStatusReview),
	})
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrPreconditionFailed)

	select {
	case ev := <-sub:
		t.Fatalf("expected no event on a failed CAS, got: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// Expected: no event within the timeout.
	}
}

// TestTransitionBacklogItemStatus_should_returnSuccessAndPersistRow_When_ItemChangePublisherPanics
// wires a deliberately panicking ItemChangePublisher into a real ent-backed
// repository and confirms the panic is contained entirely within the publish
// step: the transition still returns success (no panic reaches this test
// goroutine) and the row is actually persisted (re-fetched and checked)
// (Task 2.1.1d, R4 integration — proves the panic-recovery guarantee added at
// the repository call site protects this real hooked method end-to-end, even
// against a raw ItemChangePublisher implementation that has no recover() of
// its own).
func TestTransitionBacklogItemStatus_should_returnSuccessAndPersistRow_When_ItemChangePublisherPanics(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	repo.SetItemChangePublisher(panickingItemChangePublisher{})

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "item for panicking publisher regression test",
		Status: string(session.BacklogStatusInProgress),
	})
	require.NoError(t, err)

	require.NotPanics(t, func() {
		updated, transErr := repo.TransitionBacklogItemStatus(ctx, item.ID, session.BacklogStatusDone, &session.BacklogItemPrecondition{
			ExpectedStatus: string(session.BacklogStatusInProgress),
		})
		require.NoError(t, transErr)
		require.NotNil(t, updated)
	})

	fetched, err := repo.GetBacklogItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, string(session.BacklogStatusDone), fetched.Status)
}
