package session_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
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

// TestUpdateBacklogItem_should_publishChangedFieldNames_When_TitleIsUpdated
// updates only the Title field and asserts the published event's
// UpdatedFields contains exactly "title" (Task 2.2.1b, R5 happy path).
func TestUpdateBacklogItem_should_publishChangedFieldNames_When_TitleIsUpdated(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "Old title",
		Status: string(session.BacklogStatusIdea),
	})
	require.NoError(t, err)

	newTitle := "New title"
	_, err = repo.UpdateBacklogItem(ctx, item.ID, session.BacklogItemUpdate{Title: &newTitle}, nil)
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.Equal(t, pkgevents.EventBacklogItemChanged, ev.Type)
		require.NotNil(t, ev.BacklogItemPayload)
		assert.Equal(t, pkgevents.BacklogChangeItemUpdated, ev.BacklogItemPayload.Kind)
		assert.Equal(t, []string{"title"}, ev.BacklogItemPayload.UpdatedFields)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for BacklogItemChanged event")
	}
}

// TestUpdateBacklogItem_should_notPublishMisleadingFieldList_When_NoParamsAreSet
// calls UpdateBacklogItem with every field left nil (a no-op update) and
// asserts the diff detector never fabricates a field name — the published
// UpdatedFields list (if an event is published at all) must be empty
// (Task 2.2.1a, R5 error/edge path).
func TestUpdateBacklogItem_should_notPublishMisleadingFieldList_When_NoParamsAreSet(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "item for no-op update test",
		Status: string(session.BacklogStatusIdea),
	})
	require.NoError(t, err)

	_, err = repo.UpdateBacklogItem(ctx, item.ID, session.BacklogItemUpdate{}, nil)
	require.NoError(t, err)

	select {
	case ev := <-sub:
		assert.Empty(t, ev.BacklogItemPayload.UpdatedFields)
	case <-time.After(200 * time.Millisecond):
		// Also acceptable: no event published at all for a no-op update.
	}
}

// TestUpdateBacklogItem_should_deliverEventThroughRealBus_When_MultipleFieldsChange
// updates several fields together and asserts one event carries all their
// names in UpdatedFields plus the full updated BacklogItem snapshot (R5
// integration). Uses Title+Description+Notes rather than plan.md's literal
// "planText" example — there is no PlanText field on BacklogItemUpdate in this
// codebase, so Notes stands in as the third real field.
func TestUpdateBacklogItem_should_deliverEventThroughRealBus_When_MultipleFieldsChange(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "Old title",
		Status: string(session.BacklogStatusIdea),
	})
	require.NoError(t, err)

	newTitle := "New title"
	newDescription := "New description"
	newNotes := "New notes"
	_, err = repo.UpdateBacklogItem(ctx, item.ID, session.BacklogItemUpdate{
		Title:       &newTitle,
		Description: &newDescription,
		Notes:       &newNotes,
	}, nil)
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.NotNil(t, ev.BacklogItemPayload)
		assert.ElementsMatch(t, []string{"title", "description", "notes"}, ev.BacklogItemPayload.UpdatedFields)
		require.NotNil(t, ev.BacklogItemPayload.Item)
		assert.Equal(t, newTitle, ev.BacklogItemPayload.Item.Title)
		assert.Equal(t, newDescription, ev.BacklogItemPayload.Item.Description)
		assert.Equal(t, newNotes, ev.BacklogItemPayload.Item.Notes)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for BacklogItemChanged event")
	}
}

// TestArchiveBacklogItem_should_publishArchivedAtTimestamp_When_DoneItemIsArchived
// archives a done item and asserts the published event carries a non-nil
// ArchivedAt timestamp (Task 2.2.2a/c, R6 happy path).
func TestArchiveBacklogItem_should_publishArchivedAtTimestamp_When_DoneItemIsArchived(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "item to archive",
		Status: string(session.BacklogStatusDone),
	})
	require.NoError(t, err)

	_, err = repo.ArchiveBacklogItem(ctx, item.ID)
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.NotNil(t, ev.BacklogItemPayload)
		assert.Equal(t, pkgevents.BacklogChangeItemArchived, ev.BacklogItemPayload.Kind)
		require.NotNil(t, ev.BacklogItemPayload.ArchivedAt)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for BacklogItemChanged event")
	}
}

// TestDeleteBacklogItem_should_notPublish_When_ItemIDDoesNotExist confirms a
// delete against a nonexistent item id returns its existing not-found error
// and never reaches the publish call (Task 2.2.2b, R6 error path).
func TestDeleteBacklogItem_should_notPublish_When_ItemIDDoesNotExist(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	err := repo.DeleteBacklogItem(ctx, uuid.NewString())
	require.Error(t, err)
	require.ErrorIs(t, err, session.ErrNotFound)

	select {
	case ev := <-sub:
		t.Fatalf("expected no event when deleting a nonexistent item, got: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// Expected: no event within the timeout.
	}
}

// TestDeleteBacklogItem_should_publishRemovedNotUpdated_When_ExistingItemIsDeleted
// deletes an existing item and asserts the subscriber receives
// ChangeItemRemoved, not ChangeItemUpdated — confirming the downstream RPC
// handler will route this to a BacklogItemRemovedEvent (a delete signal), not
// an upsert (Task 2.2.2c, R6 integration).
func TestDeleteBacklogItem_should_publishRemovedNotUpdated_When_ExistingItemIsDeleted(t *testing.T) {
	repo, cleanup := newTestEntRepositoryForEvents(t)
	defer cleanup()
	ctx := context.Background()

	bus := pkgevents.NewEventBus(10)
	repo.SetItemChangePublisher(&services.BacklogItemEventPublisher{Bus: bus})

	sub, subID := bus.Subscribe(ctx)
	defer bus.Unsubscribe(subID)

	item, err := repo.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "item to delete",
		Status: string(session.BacklogStatusIdea),
	})
	require.NoError(t, err)

	err = repo.DeleteBacklogItem(ctx, item.ID)
	require.NoError(t, err)

	select {
	case ev := <-sub:
		require.NotNil(t, ev.BacklogItemPayload)
		assert.Equal(t, pkgevents.BacklogChangeItemRemoved, ev.BacklogItemPayload.Kind)
		assert.NotEqual(t, pkgevents.BacklogChangeItemUpdated, ev.BacklogItemPayload.Kind)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for BacklogItemChanged event")
	}
}
