package session

import (
	"bytes"
	"context"
	stdlog "log"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tstapler/stapler-squad/log"
	"github.com/tstapler/stapler-squad/session/git"
)

// waitWithTimeout waits for the done channel to be closed or fails the test after 2 seconds.
func waitWithTimeout(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for goroutine to complete")
	}
}

// TestBacklogLifecycleListener_OnSessionStarted verifies that when a session UUID
// maps to an ItemSession, UpdateItemSessionStarted is called. When session UUID
// has no ItemSession (ErrNotFound), no error is propagated.
func TestBacklogLifecycleListener_OnSessionStarted(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem with status "in_progress".
	itemData := BacklogItemData{
		Title:              "Test Item",
		Description:        "A test item",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)
	require.NotNil(t, createdItem)

	// Create an ItemSession linked to the BacklogItem with a specific session UUID.
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "work",
	}
	createdIS, err := storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)
	require.NotNil(t, createdIS)

	// Create the BacklogLifecycleListener and call onSessionStarted.
	listener := NewBacklogLifecycleListener(storage)

	// Use a WaitGroup to synchronize with the goroutine spawned by onSessionStarted.
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		listener.onSessionStarted(sessionUUID)
	}()
	go func() {
		wg.Wait()
		close(done)
	}()
	waitWithTimeout(t, done)

	// Verify that UpdateItemSessionStarted was called by checking StartedAt is set.
	repo := storage.repo.(*EntRepository)
	fetchedIS, err := repo.GetItemSession(ctx, createdIS.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedIS.StartedAt)
}

// TestBacklogLifecycleListener_OnSessionStarted_NotFound verifies that when a
// session UUID has no linked ItemSession, no error is logged or propagated.
func TestBacklogLifecycleListener_OnSessionStarted_NotFound(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	listener := NewBacklogLifecycleListener(storage)

	// Call onSessionStarted with a non-existent UUID. This should not panic or error.
	nonExistentUUID := uuid.New().String()
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.onSessionStarted(nonExistentUUID)
	}()
	waitWithTimeout(t, done)

	// If we reach here without panic, the test passes.
	// The method silently returns on ErrNotFound, so there's no observable state change.
}

// TestBacklogLifecycleListener_OnSessionExited_WorkSession_TransitionsToReview
// verifies that when a work session exits and item is in_progress, item transitions
// to review (when SkipReviewGate=false).
func TestBacklogLifecycleListener_OnSessionExited_WorkSession_TransitionsToReview(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem with status "in_progress" and SkipReviewGate=false.
	itemData := BacklogItemData{
		Title:              "Test Item",
		Description:        "A test item",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		SkipReviewGate:     false,
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)
	require.NotNil(t, createdItem)

	// Create an ItemSession linked to the BacklogItem with SessionRole="work".
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "work",
	}
	createdIS, err := storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)
	require.NotNil(t, createdIS)

	// Create the BacklogLifecycleListener and call onSessionExited.
	listener := NewBacklogLifecycleListener(storage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.onSessionExited(sessionUUID)
	}()
	waitWithTimeout(t, done)

	// Verify that the item transitioned to review.
	fetchedItem, err := storage.GetBacklogItem(ctx, createdItem.ID)
	require.NoError(t, err)
	require.Equal(t, string(BacklogStatusReview), fetchedItem.Status)

	// Verify that the ItemSession has EndedAt set.
	repo := storage.repo.(*EntRepository)
	fetchedIS, err := repo.GetItemSession(ctx, createdIS.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedIS.EndedAt)
}

// TestBacklogLifecycleListener_OnSessionExited_WorkSession_TransitionsToDone_WhenSkipReviewGate
// verifies that when SkipReviewGate=true, item transitions directly to done.
func TestBacklogLifecycleListener_OnSessionExited_WorkSession_TransitionsToDone_WhenSkipReviewGate(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem with status "in_progress" and SkipReviewGate=true.
	itemData := BacklogItemData{
		Title:              "Test Item",
		Description:        "A test item",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		SkipReviewGate:     true,
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)
	require.NotNil(t, createdItem)

	// Create an ItemSession linked to the BacklogItem with SessionRole="work".
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "work",
	}
	createdIS, err := storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)
	require.NotNil(t, createdIS)

	// Create the BacklogLifecycleListener and call onSessionExited.
	listener := NewBacklogLifecycleListener(storage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.onSessionExited(sessionUUID)
	}()
	waitWithTimeout(t, done)

	// Verify that the item transitioned to done (not review).
	fetchedItem, err := storage.GetBacklogItem(ctx, createdItem.ID)
	require.NoError(t, err)
	require.Equal(t, string(BacklogStatusDone), fetchedItem.Status)

	// Verify that the ItemSession has EndedAt set.
	repo := storage.repo.(*EntRepository)
	fetchedIS, err := repo.GetItemSession(ctx, createdIS.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedIS.EndedAt)
}

// TestBacklogLifecycleListener_OnSessionExited_ReviewSession_NoTransition
// verifies that when a review/triage session exits (SessionRole != "work"),
// no transition happens (recursion guard).
func TestBacklogLifecycleListener_OnSessionExited_ReviewSession_NoTransition(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem with status "in_progress".
	itemData := BacklogItemData{
		Title:              "Test Item",
		Description:        "A test item",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)
	require.NotNil(t, createdItem)

	// Create an ItemSession linked to the BacklogItem with SessionRole="review".
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "review",
	}
	createdIS, err := storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)
	require.NotNil(t, createdIS)

	// Create the BacklogLifecycleListener and call onSessionExited.
	listener := NewBacklogLifecycleListener(storage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.onSessionExited(sessionUUID)
	}()
	waitWithTimeout(t, done)

	// Verify that the item status did NOT change (still in_progress).
	fetchedItem, err := storage.GetBacklogItem(ctx, createdItem.ID)
	require.NoError(t, err)
	require.Equal(t, string(BacklogStatusInProgress), fetchedItem.Status)

	// Verify that the ItemSession EndedAt IS set (exit is recorded for all roles).
	repo := storage.repo.(*EntRepository)
	fetchedIS, err := repo.GetItemSession(ctx, createdIS.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedIS.EndedAt, "review session should have EndedAt recorded when it exits")
}

// TestBacklogLifecycleListener_OnSessionExited_NotFound_NoError
// verifies that when session UUID has no ItemSession, no panic or error occurs.
func TestBacklogLifecycleListener_OnSessionExited_NotFound_NoError(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	listener := NewBacklogLifecycleListener(storage)

	// Call onSessionExited with a non-existent UUID. This should not panic or error.
	nonExistentUUID := uuid.New().String()
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.onSessionExited(nonExistentUUID)
	}()
	waitWithTimeout(t, done)

	// If we reach here without panic, the test passes.
}

// TestBacklogLifecycleListener_OnSessionExited_ItemNotInProgress_NoTransition
// verifies that if the item is not in in_progress status, no transition occurs
// (e.g., item is already in review or done).
func TestBacklogLifecycleListener_OnSessionExited_ItemNotInProgress_NoTransition(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem with status "review" (not in_progress).
	itemData := BacklogItemData{
		Title:              "Test Item",
		Description:        "A test item",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusReview),
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)
	require.NotNil(t, createdItem)

	// Create an ItemSession linked to the BacklogItem with SessionRole="work".
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "work",
	}
	createdIS, err := storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)
	require.NotNil(t, createdIS)

	// Create the BacklogLifecycleListener and call onSessionExited.
	listener := NewBacklogLifecycleListener(storage)
	done := make(chan struct{})
	go func() {
		defer close(done)
		listener.onSessionExited(sessionUUID)
	}()
	waitWithTimeout(t, done)

	// Verify that the item status did NOT change (still review).
	fetchedItem, err := storage.GetBacklogItem(ctx, createdItem.ID)
	require.NoError(t, err)
	require.Equal(t, string(BacklogStatusReview), fetchedItem.Status)

	// Verify that the ItemSession has EndedAt set (the exit was recorded).
	repo := storage.repo.(*EntRepository)
	fetchedIS, err := repo.GetItemSession(ctx, createdIS.ID)
	require.NoError(t, err)
	require.NotNil(t, fetchedIS.EndedAt)
}

// TestBacklogLifecycleListener_WireToInstance verifies that WireToInstance correctly
// registers a per-instance listener shim that fires on lifecycle events.
func TestBacklogLifecycleListener_WireToInstance(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	listener := NewBacklogLifecycleListener(storage)
	listener.SetEnabled(true)

	// Create a minimal Instance with a known UUID (without starting tmux).
	inst := &Instance{
		UUID: uuid.New().String(),
	}

	// Wire the listener to the instance.
	listener.WireToInstance(inst)

	// Verify a listener was registered by checking the slice length.
	inst.lifecycleListenersMu.Lock()
	count := len(inst.lifecycleListeners)
	inst.lifecycleListenersMu.Unlock()
	require.Equal(t, 1, count, "WireToInstance should register exactly one lifecycle listener")

	// Create a BacklogItem and ItemSession linked to inst.UUID so that
	// firing EventStarted updates the session's StartedAt.
	itemData := BacklogItemData{
		Title:              "WireToInstance test item",
		Description:        "Testing wire",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: inst.UUID,
		SessionRole: "work",
	}
	createdIS, err := storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)

	// Fire EventStarted through the registered shim. The shim dispatches to a goroutine.
	done := make(chan struct{})
	go func() {
		defer close(done)
		inst.fireLifecycleEvent(EventStarted, "")
	}()
	waitWithTimeout(t, done)

	// Allow the goroutine inside onSessionStarted to complete.
	// Since the shim spawns its own goroutine, we poll briefly.
	require.Eventually(t, func() bool {
		repo := storage.repo.(*EntRepository)
		fetchedIS, ferr := repo.GetItemSession(ctx, createdIS.ID)
		return ferr == nil && fetchedIS.StartedAt != nil
	}, 2*time.Second, 20*time.Millisecond, "EventStarted should trigger UpdateItemSessionStarted")
}

// TestBacklogLifecycleListener_NewBacklogLifecycleListener creates a listener
// without a spawner and verifies it's initialized correctly.
func TestBacklogLifecycleListener_NewBacklogLifecycleListener(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	listener := NewBacklogLifecycleListener(storage)
	require.NotNil(t, listener)
	require.Equal(t, storage, listener.storage)
	require.Nil(t, listener.sessionCreator)
}

// TestBacklogLifecycleListener_NewBacklogLifecycleListenerWithSpawner creates
// a listener with a spawner and verifies it's initialized correctly.
func TestBacklogLifecycleListener_NewBacklogLifecycleListenerWithSpawner(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	// Create a mock spawner.
	mockSpawner := &mockReviewGateSpawner{}

	listener := NewBacklogLifecycleListenerWithSpawner(storage, mockSpawner)
	require.NotNil(t, listener)
	require.Equal(t, storage, listener.storage)
	require.Equal(t, mockSpawner, listener.sessionCreator)
}

// mockReviewGateSpawner is a mock implementation of ReviewGateSpawner for testing.
type mockReviewGateSpawner struct {
	spawnCalled bool
	lastItem    *BacklogItemData
}

func (m *mockReviewGateSpawner) SpawnReviewSession(ctx context.Context, item *BacklogItemData, itemSessionID string, prompt string) (*Instance, error) {
	m.spawnCalled = true
	m.lastItem = item
	return &Instance{}, nil
}

// fakePRPendingChecker is a test double implementing prPendingChecker, used to
// inject canned IsPRMerged/GetPRStatus results without a live git worktree or
// authenticated gh CLI.
type fakePRPendingChecker struct {
	merged    bool
	mergedErr error
	status    *git.PRStatus
	statusErr error
}

func (f *fakePRPendingChecker) IsPRMerged(prNumber int) (bool, error) {
	return f.merged, f.mergedErr
}

func (f *fakePRPendingChecker) GetPRStatus(prNumber int) (*git.PRStatus, error) {
	return f.status, f.statusErr
}

// fakePRFixSpawner is a test double implementing PRFixSpawner, recording
// whether/how AutoReopenForPRFix was called. Same shape as mockReviewGateSpawner.
type fakePRFixSpawner struct {
	spawnCalled    bool
	lastFixContext string
}

func (f *fakePRFixSpawner) AutoReopenForPRFix(ctx context.Context, itemID string, fixContext string) error {
	f.spawnCalled = true
	f.lastFixContext = fixContext
	return nil
}

// TestBacklogLifecycleListener_IgnoresEventsWhenDisabled verifies that when the listener
// is disabled via SetEnabled(false), lifecycle events from an Instance are silently dropped
// and no storage side effects occur.
func TestBacklogLifecycleListener_IgnoresEventsWhenDisabled(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem in in_progress status.
	itemData := BacklogItemData{
		Title:              "Disabled gate test item",
		Description:        "Testing enabled gate",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	// Create an ItemSession linked to the item.
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "work",
	}
	_, err = storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)

	// Build a listener and wire it to a minimal Instance.
	listener := NewBacklogLifecycleListener(storage)
	listener.SetEnabled(false) // explicitly disabled

	inst := &Instance{UUID: sessionUUID}
	listener.WireToInstance(inst)

	// Fire EventExited — the gate should stop processing immediately.
	// Allow time for any goroutine that might have been started to settle.
	require.Eventually(t, func() bool {
		inst.fireLifecycleEvent(EventExited, "")
		// Check that the item was NOT transitioned.
		fetched, ferr := storage.GetBacklogItem(ctx, createdItem.ID)
		return ferr == nil && fetched.Status == string(BacklogStatusInProgress)
	}, 500*time.Millisecond, 20*time.Millisecond,
		"disabled listener should not transition item status")
}

// TestBacklogLifecycleListener_ProcessesEventsWhenEnabled verifies that when the listener
// is enabled via SetEnabled(true), lifecycle events ARE processed and storage is updated.
func TestBacklogLifecycleListener_ProcessesEventsWhenEnabled(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create a BacklogItem in in_progress status.
	itemData := BacklogItemData{
		Title:              "Enabled gate test item",
		Description:        "Testing enabled gate",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		SkipReviewGate:     true, // go straight to done to make assertion easy
	}
	createdItem, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	// Create an ItemSession linked to the item.
	sessionUUID := uuid.New().String()
	isData := ItemSessionData{
		ItemID:      createdItem.ID,
		SessionUUID: sessionUUID,
		SessionRole: "work",
	}
	_, err = storage.CreateItemSession(ctx, isData)
	require.NoError(t, err)

	// Build a listener, enable it, and wire to an Instance.
	listener := NewBacklogLifecycleListener(storage)
	listener.SetEnabled(true)

	inst := &Instance{UUID: sessionUUID}
	listener.WireToInstance(inst)

	// Fire EventExited — the listener must process it and transition the item.
	inst.fireLifecycleEvent(EventExited, "")

	require.Eventually(t, func() bool {
		fetched, ferr := storage.GetBacklogItem(ctx, createdItem.ID)
		return ferr == nil && fetched.Status == string(BacklogStatusDone)
	}, 2*time.Second, 20*time.Millisecond,
		"enabled listener should transition item from in_progress to done")
}

// TestCreateItemSessionWithVerdict_Atomic verifies that CreateItemSessionWithVerdict
// creates both ItemSession and ReviewVerdict atomically — both records must exist,
// and the verdict is linked to the session.
func TestCreateItemSessionWithVerdict_Atomic(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	item, err := storage.CreateBacklogItem(ctx, BacklogItemData{
		Title:              "Atomic Verdict Test",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
	})
	require.NoError(t, err)

	sessionUUID := "headless-review-" + uuid.New().String()
	is, err := storage.CreateItemSessionWithVerdict(ctx, ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: sessionUUID,
		SessionRole: SessionRoleReview,
	}, ReviewVerdictData{
		OverallOutcome: ReviewVerdictFail,
		Summary:        "Blocked by security check.",
	})
	require.NoError(t, err)
	assert.Equal(t, sessionUUID, is.SessionUUID)
	require.NotNil(t, is.ReviewVerdict, "ReviewVerdict must be linked to the ItemSession")
	assert.Equal(t, string(ReviewVerdictFail), is.ReviewVerdict.OverallOutcome)
	assert.Equal(t, "Blocked by security check.", is.ReviewVerdict.Summary)

	// Both records must be queryable from the same DB — verifies the commit succeeded.
	sessions, listErr := storage.ListItemSessions(ctx, item.ID)
	require.NoError(t, listErr)
	require.Len(t, sessions, 1)
	assert.Equal(t, sessionUUID, sessions[0].SessionUUID)
	require.NotNil(t, sessions[0].ReviewVerdict, "ReviewVerdict must be linked to the ItemSession")
	assert.Equal(t, string(ReviewVerdictFail), sessions[0].ReviewVerdict.OverallOutcome)
}

// newPRPendingTestItem creates a pr_pending BacklogItem with the given PR
// number/URL and a non-empty RepoPath (ReconcilePRPending skips items with an
// empty RepoPath). The repo path is a placeholder — newPRPendingChecker is
// overridden in these tests, so no real git/gh call is ever made against it.
//
// CreateBacklogItem does not persist PrURL/PrNumber (see ent_repository_backlog.go),
// so those fields are set via a follow-up UpdateBacklogItem call, mirroring how
// pushAndCreatePR itself stores them (backlog_lifecycle.go:500-506).
func newPRPendingTestItem(t *testing.T, storage *Storage, prNumber int) *BacklogItemData {
	t.Helper()
	ctx := context.Background()
	item, err := storage.CreateBacklogItem(ctx, BacklogItemData{
		Title:              "PR pending test item",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusPRPending),
		RepoPath:           "/tmp/fake-repo",
	})
	require.NoError(t, err)

	prURL := "https://github.com/TylerStaplerAtFanatics/stapler-squad/pull/152"
	updated, err := storage.UpdateBacklogItem(ctx, item.ID, BacklogItemUpdate{
		PrURL:    &prURL,
		PrNumber: &prNumber,
	}, nil)
	require.NoError(t, err)
	return updated
}

// overridePRPendingChecker swaps newPRPendingChecker for the duration of the
// test and restores the original on cleanup (mirrors the timeNow seam
// override pattern used elsewhere in this package).
func overridePRPendingChecker(t *testing.T, checker *fakePRPendingChecker) {
	t.Helper()
	orig := newPRPendingChecker
	newPRPendingChecker = func(repoPath string) prPendingChecker { return checker }
	t.Cleanup(func() { newPRPendingChecker = orig })
}

// redirectInfoLog swaps log.InfoLog for a logger writing to buf for the
// duration of the test and restores the original on cleanup. Equivalent to
// log.NewDummyLogger(buf, prefix) (log/log_test.go), reimplemented here
// because that helper lives in a _test.go file in package log and is not
// importable from other packages.
func redirectInfoLog(t *testing.T, buf *bytes.Buffer) {
	t.Helper()
	orig := log.InfoLog
	log.InfoLog = stdlog.New(buf, "INFO: ", 0)
	t.Cleanup(func() { log.InfoLog = orig })
}

// TestReconcilePRPending_SpawnsFixSession_WhenHasConflictsTrue_Alone verifies
// that HasConflicts=true alone (CI/reviews both false) is sufficient to spawn
// a fix session, and that the fix context carries the "## Merge conflict"
// section from FeedbackText.
func TestReconcilePRPending_SpawnsFixSession_WhenHasConflictsTrue_Alone(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	newPRPendingTestItem(t, storage, 152)

	overridePRPendingChecker(t, &fakePRPendingChecker{
		merged: false,
		status: &git.PRStatus{
			HasConflicts: true,
			FeedbackText: "## Merge conflict\n...",
		},
	})

	listener := NewBacklogLifecycleListener(storage)
	fakeSpawner := &fakePRFixSpawner{}
	listener.SetPRFixSpawner(fakeSpawner)

	er := storage.repo.(*EntRepository)
	listener.ReconcilePRPending(context.Background(), er)

	assert.True(t, fakeSpawner.spawnCalled, "conflict-only PRStatus should trigger a fix-session spawn")
	assert.Contains(t, fakeSpawner.lastFixContext, "## Merge conflict")
}

// TestReconcilePRPending_LogsConflictTrue_WhenConflictTriggersSpawn verifies
// that the spawn log line records conflict=true when HasConflicts triggered it.
func TestReconcilePRPending_LogsConflictTrue_WhenConflictTriggersSpawn(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	newPRPendingTestItem(t, storage, 152)

	overridePRPendingChecker(t, &fakePRPendingChecker{
		status: &git.PRStatus{
			HasConflicts: true,
			FeedbackText: "## Merge conflict\n...",
		},
	})

	listener := NewBacklogLifecycleListener(storage)
	listener.SetPRFixSpawner(&fakePRFixSpawner{})

	var buf bytes.Buffer
	redirectInfoLog(t, &buf)

	er := storage.repo.(*EntRepository)
	listener.ReconcilePRPending(context.Background(), er)

	assert.Contains(t, buf.String(), "conflict=true")
}

// TestReconcilePRPending_SpawnsFixSession_WhenCIFailingTrue is a regression
// test for the pre-existing CIFailing trigger, previously untested at the
// gate level. It also asserts the log line reports conflict=false so the
// extended log format doesn't spuriously report a conflict when only CI failed.
func TestReconcilePRPending_SpawnsFixSession_WhenCIFailingTrue(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	newPRPendingTestItem(t, storage, 152)

	overridePRPendingChecker(t, &fakePRPendingChecker{
		status: &git.PRStatus{
			CIFailing:    true,
			FeedbackText: "## Failing CI checks\n- build FAILED\n",
		},
	})

	listener := NewBacklogLifecycleListener(storage)
	fakeSpawner := &fakePRFixSpawner{}
	listener.SetPRFixSpawner(fakeSpawner)

	var buf bytes.Buffer
	redirectInfoLog(t, &buf)

	er := storage.repo.(*EntRepository)
	listener.ReconcilePRPending(context.Background(), er)

	assert.True(t, fakeSpawner.spawnCalled, "CIFailing alone should trigger a fix-session spawn")
	assert.Contains(t, buf.String(), "CI=true")
	assert.Contains(t, buf.String(), "conflict=false")
}

// TestReconcilePRPending_SpawnsFixSession_WhenHasBlockingReviewsTrue is a
// regression test for the pre-existing HasBlockingReviews trigger, previously
// untested at the gate level. It also asserts the log line reports
// conflict=false so the extended log format doesn't spuriously report a
// conflict when only a review blocked.
func TestReconcilePRPending_SpawnsFixSession_WhenHasBlockingReviewsTrue(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	newPRPendingTestItem(t, storage, 152)

	overridePRPendingChecker(t, &fakePRPendingChecker{
		status: &git.PRStatus{
			HasBlockingReviews: true,
			FeedbackText:       "## Review: changes requested by @reviewer1\n",
		},
	})

	listener := NewBacklogLifecycleListener(storage)
	fakeSpawner := &fakePRFixSpawner{}
	listener.SetPRFixSpawner(fakeSpawner)

	var buf bytes.Buffer
	redirectInfoLog(t, &buf)

	er := storage.repo.(*EntRepository)
	listener.ReconcilePRPending(context.Background(), er)

	assert.True(t, fakeSpawner.spawnCalled, "HasBlockingReviews alone should trigger a fix-session spawn")
	assert.Contains(t, buf.String(), "reviews=true")
	assert.Contains(t, buf.String(), "conflict=false")
}

// TestReconcilePRPending_NoSpawn_WhenAllSignalsFalse verifies that a healthy
// PR (all three signals false) does not trigger a spawn and leaves the item
// in pr_pending — the extended 3-way gate must not over-trigger.
func TestReconcilePRPending_NoSpawn_WhenAllSignalsFalse(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	item := newPRPendingTestItem(t, storage, 152)

	overridePRPendingChecker(t, &fakePRPendingChecker{
		status: &git.PRStatus{},
	})

	listener := NewBacklogLifecycleListener(storage)
	fakeSpawner := &fakePRFixSpawner{}
	listener.SetPRFixSpawner(fakeSpawner)

	er := storage.repo.(*EntRepository)
	listener.ReconcilePRPending(context.Background(), er)

	assert.False(t, fakeSpawner.spawnCalled, "healthy PR (all signals false) must not trigger a spawn")

	fetched, err := storage.GetBacklogItem(context.Background(), item.ID)
	require.NoError(t, err)
	assert.Equal(t, string(BacklogStatusPRPending), fetched.Status, "item status must remain pr_pending")
}
