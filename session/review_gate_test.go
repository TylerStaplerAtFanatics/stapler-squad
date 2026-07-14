package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tstapler/stapler-squad/session/git"
	"github.com/tstapler/stapler-squad/session/headless"
)

// TestReviewGateRunner_SkipReviewGate verifies that Run returns immediately
// without calling the pool or onPass when item.SkipReviewGate is true.
func TestReviewGateRunner_SkipReviewGate(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	// Construct an item with SkipReviewGate set — pool and onPass must not be called.
	item := &BacklogItemData{
		ID:             uuid.New().String(),
		RepoPath:       "/some/repo",
		SkipReviewGate: true,
	}
	is := ItemSessionSummary{
		ID:          uuid.New().String(),
		SessionUUID: uuid.New().String(),
	}

	var poolCalled atomic.Bool
	var onPassCalled atomic.Bool

	// If the pool is consulted, panic so the test fails loudly.
	getPool := func() *headless.Pool {
		poolCalled.Store(true)
		return nil
	}
	getAutoReopener := func() AutoReopenSpawner { return nil }

	runner := NewReviewGateRunner(storage, getPool, getAutoReopener, func() Notifier { return nil }, nil)

	runner.Run(context.Background(), item, is, func(ctx context.Context, item *BacklogItemData, is ItemSessionSummary) {
		onPassCalled.Store(true)
	})

	assert.False(t, poolCalled.Load(), "pool getter must not be consulted when SkipReviewGate is true")
	assert.False(t, onPassCalled.Load(), "onPass must not be called when SkipReviewGate is true")
}

// TestReviewGateRunner_HeadlessPassPath verifies the happy path where the headless
// pool returns a PASS verdict: onPass is called and a review ItemSession with a
// PASS verdict is persisted to storage.
func TestReviewGateRunner_HeadlessPassPath(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Persist a BacklogItem so CreateItemSessionWithVerdict can satisfy its FK.
	itemData := BacklogItemData{
		Title:              "Headless Pass Test",
		Description:        "Testing the headless PASS path",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		RepoPath:           t.TempDir(), // non-git dir; GetGitDiff will error gracefully
	}
	createdItemData, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	// Persist a work ItemSession so the runner can look it up.
	workSessionUUID := uuid.New().String()
	workIS, err := storage.CreateItemSession(ctx, ItemSessionData{
		ItemID:      createdItemData.ID,
		SessionUUID: workSessionUUID,
		SessionRole: SessionRoleWork,
	})
	require.NoError(t, err)

	item := &BacklogItemData{
		ID:       createdItemData.ID,
		RepoPath: createdItemData.RepoPath,
	}

	// Build the JSON response expected by pool.CallBlockingWithCost.
	// The outer envelope is firstCallJSONResult; its "result" field contains the
	// verdict JSON that ParseHeadlessVerdictResult will parse.
	verdictJSON := `{"overall":"PASS","summary":"all criteria met","verdicts":[]}`
	verdictJSONEncoded, marshalErr := json.Marshal(verdictJSON)
	require.NoError(t, marshalErr)
	fakeResponse := fmt.Sprintf(`{"session_id":"test-s1","result":%s,"cost_usd":0.01}`, verdictJSONEncoded)

	fakeRunner := headless.NewFakeRunner(fakeResponse)
	pool := headless.NewPoolWithRunner(headless.PoolConfig{
		MaxCallsPerSession:    1,
		MaxConcurrentSessions: 1,
	}, fakeRunner)

	getPool := func() *headless.Pool { return pool }
	getAutoReopener := func() AutoReopenSpawner { return nil }

	runner := NewReviewGateRunner(storage, getPool, getAutoReopener, func() Notifier { return nil }, nil)

	var onPassCalled atomic.Bool
	runner.Run(ctx, item, workIS, func(ctx context.Context, item *BacklogItemData, is ItemSessionSummary) {
		onPassCalled.Store(true)
	})

	assert.True(t, onPassCalled.Load(), "onPass must be called when headless pool returns PASS")
	assert.Equal(t, 1, fakeRunner.CallCount(), "pool must be called exactly once")
}

// TestReviewGateRunner_ThreadsVerificationNotesIntoPrompt verifies that verification
// evidence recorded on the work session (via request_review's verification_notes
// argument) reaches the headless reviewer prompt, not just the diff and AC list.
// This is the regression guard for the UNVERIFIABLE-despite-real-verification gap:
// criteria describing test runs or manual UI checks are invisible in the diff, so the
// reviewer's only window into that evidence is this threaded-through text.
func TestReviewGateRunner_ThreadsVerificationNotesIntoPrompt(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	itemData := BacklogItemData{
		Title:              "Verification Notes Threading Test",
		Description:        "Testing that verification_notes reaches the reviewer prompt",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		RepoPath:           t.TempDir(),
	}
	createdItemData, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	verificationNotes := "ran `go test ./session/...` -> ok (41 tests); confirmed via UI that sessions group under Category=Backlog"

	workSessionUUID := uuid.New().String()
	workIS, err := storage.CreateItemSession(ctx, ItemSessionData{
		ItemID:            createdItemData.ID,
		SessionUUID:       workSessionUUID,
		SessionRole:       SessionRoleWork,
		VerificationNotes: verificationNotes,
	})
	require.NoError(t, err)

	item := &BacklogItemData{
		ID:       createdItemData.ID,
		RepoPath: createdItemData.RepoPath,
	}

	verdictJSON := `{"overall":"PASS","summary":"all criteria met","verdicts":[]}`
	verdictJSONEncoded, marshalErr := json.Marshal(verdictJSON)
	require.NoError(t, marshalErr)
	fakeResponse := fmt.Sprintf(`{"session_id":"test-s1","result":%s,"cost_usd":0.01}`, verdictJSONEncoded)

	fakeRunner := headless.NewFakeRunner(fakeResponse)
	pool := headless.NewPoolWithRunner(headless.PoolConfig{
		MaxCallsPerSession:    1,
		MaxConcurrentSessions: 1,
	}, fakeRunner)

	getPool := func() *headless.Pool { return pool }
	getAutoReopener := func() AutoReopenSpawner { return nil }

	runner := NewReviewGateRunner(storage, getPool, getAutoReopener, func() Notifier { return nil }, nil)
	runner.Run(ctx, item, workIS, func(ctx context.Context, item *BacklogItemData, is ItemSessionSummary) {})

	require.Equal(t, 1, fakeRunner.CallCount(), "pool must be called exactly once")

	// The user prompt is passed via stdin (not args) so it doesn't leak into
	// /proc/<pid>/cmdline — see Pool.call in headless/caller.go.
	prompt := fakeRunner.StdinForCall(0)
	assert.True(t,
		strings.Contains(prompt, "Verification Evidence") && strings.Contains(prompt, "Category=Backlog"),
		"reviewer prompt must contain the labeled Verification Evidence section with the work session's reported notes; got prompt: %s", prompt)
}

// fakeAutoReopenSpawner is a test double implementing AutoReopenSpawner, recording
// every call and signaling on a channel so async callers (spawnReviewGate invokes
// it in a goroutine) can be synchronized with in tests.
type fakeAutoReopenSpawner struct {
	called chan string // item IDs, one per call
}

func newFakeAutoReopenSpawner() *fakeAutoReopenSpawner {
	return &fakeAutoReopenSpawner{called: make(chan string, 8)}
}

func (f *fakeAutoReopenSpawner) AutoReopenAfterFailedReview(ctx context.Context, itemID string) error {
	f.called <- itemID
	return nil
}

// runGitOrFail runs a git command in dir and fails the test on error, for building
// the small real repo fixtures below.
func runGitOrFail(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %v failed: %s", args, out)
}

// TestReviewGateRunner_DiffComputationFailure_BlocksReviewInsteadOfFalseUnverifiable
// is a regression test for a live-data bug: when a session's worktree exists but its
// recorded base_commit_sha points at a git object that no longer exists (e.g. pruned,
// corrupted, or copied from elsewhere), GetGitDiff and its GetGitDiffRef fallback both
// error out. The runner used to silently swallow both errors and proceed to call the
// reviewer with an empty diff, producing a false UNVERIFIABLE/FAIL verdict indistinguishable
// from "no changes were made" — masking a real infrastructure bug and, via the
// auto-reopen loop, causing the item to spin in review forever. The fix blocks the
// review entirely, records a distinct FAIL verdict describing the diff failure, notifies,
// and still feeds the auto-reopen/cap machinery so persistent failures eventually reach
// notifyReworkCapHit instead of looping silently.
func TestReviewGateRunner_DiffComputationFailure_BlocksReviewInsteadOfFalseUnverifiable(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()
	ctx := context.Background()

	// Real git repo with one commit so `git diff <bogus-sha>..HEAD` fails with a real
	// "unknown revision" error rather than "not a git repository".
	repoDir := t.TempDir()
	runGitOrFail(t, repoDir, "init")
	runGitOrFail(t, repoDir, "config", "user.email", "test@example.com")
	runGitOrFail(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(repoDir+"/file.txt", []byte("hello\n"), 0o644))
	runGitOrFail(t, repoDir, "add", "file.txt")
	runGitOrFail(t, repoDir, "commit", "-m", "initial commit")

	itemData := BacklogItemData{
		Title:              "Diff compute failure test",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		RepoPath:           repoDir,
	}
	createdItemData, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	workSessionUUID := uuid.New().String()
	workIS, err := storage.CreateItemSession(ctx, ItemSessionData{
		ItemID:      createdItemData.ID,
		SessionUUID: workSessionUUID,
		SessionRole: SessionRoleWork,
	})
	require.NoError(t, err)

	// Record a worktree for the work session whose base_commit_sha is a well-formed but
	// nonexistent SHA — this is what a pruned/corrupted commit looks like in practice.
	inst := newTestInstance("diff-error-test")
	inst.UUID = workSessionUUID
	inst.gitManager.worktree = git.NewGitWorktreeFromStorage(
		repoDir, repoDir, "diff-error-test", "master", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, storage.SaveInstances([]*Instance{inst}))

	item := &BacklogItemData{ID: createdItemData.ID, RepoPath: repoDir}

	// The pool must never be consulted — the review should be blocked before reaching it.
	fakeRunner := headless.NewFakeRunner(`{"session_id":"unused","result":"unused","cost_usd":0}`)
	pool := headless.NewPoolWithRunner(headless.PoolConfig{
		MaxCallsPerSession:    1,
		MaxConcurrentSessions: 1,
	}, fakeRunner)
	getPool := func() *headless.Pool { return pool }

	reopener := newFakeAutoReopenSpawner()
	getAutoReopener := func() AutoReopenSpawner { return reopener }

	notifier := &fakeNotifier{}
	getNotifier := func() Notifier { return notifier }

	runner := NewReviewGateRunner(storage, getPool, getAutoReopener, getNotifier, nil)

	var onPassCalled atomic.Bool
	runner.Run(ctx, item, workIS, func(ctx context.Context, item *BacklogItemData, is ItemSessionSummary) {
		onPassCalled.Store(true)
	})

	assert.Equal(t, 0, fakeRunner.CallCount(), "reviewer pool must not be called when the diff could not be computed")
	assert.False(t, onPassCalled.Load(), "onPass must not fire for a blocked review")

	outcome, err := storage.GetMostRecentReviewVerdictForItem(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, ReviewVerdictFail, outcome, "a distinct FAIL verdict must be recorded, not a silent pass-through")

	assert.Contains(t, notifier.calls, "Review blocked — diff computation failed")

	select {
	case gotItemID := <-reopener.called:
		assert.Equal(t, item.ID, gotItemID, "auto-reopen must still be invoked so the cap/notify machinery eventually engages")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for AutoReopenAfterFailedReview to be called")
	}
}

// TestReviewGateRunner_DiffComputationFailure_AutoRepairsFromDivergentBranch is the
// positive counterpart to the blocking test above: it reproduces the exact live-data
// shape of backlog item ae1e2070-db02-4ad7-8580-633ef9904f31 — a real feature branch
// with real committed work, whose recorded base_commit_sha is a well-formed but
// nonexistent SHA (simulating a pruned/corrupted commit) — and verifies the review
// proceeds on the recovered (real) diff instead of blocking, because repoPath's
// checked-out HEAD ("main") and the work branch ("feature") have a genuine common
// ancestor that RecoverBaseCommitSHA can find and a non-empty diff results.
func TestReviewGateRunner_DiffComputationFailure_AutoRepairsFromDivergentBranch(t *testing.T) {
	storage, cleanup := createTestStorage(t)
	defer cleanup()
	ctx := context.Background()

	repoDir := t.TempDir()
	runGitOrFail(t, repoDir, "init", "-b", "main")
	runGitOrFail(t, repoDir, "config", "user.email", "test@example.com")
	runGitOrFail(t, repoDir, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(repoDir+"/README.md", []byte("base\n"), 0o644))
	runGitOrFail(t, repoDir, "add", "README.md")
	runGitOrFail(t, repoDir, "commit", "-m", "initial")

	// Real feature branch with real committed work — the same shape as ae1e2070's
	// stelekit worktree, which had a genuine 302+/88- fix already committed.
	runGitOrFail(t, repoDir, "branch", "feature")
	runGitOrFail(t, repoDir, "checkout", "feature")
	require.NoError(t, os.WriteFile(repoDir+"/feature.txt", []byte("real work\n"), 0o644))
	runGitOrFail(t, repoDir, "add", "feature.txt")
	runGitOrFail(t, repoDir, "commit", "-m", "real fix")
	runGitOrFail(t, repoDir, "checkout", "main")

	itemData := BacklogItemData{
		Title:              "Diff auto-repair test",
		AcceptanceCriteria: `[]`,
		Priority:           1,
		Status:             string(BacklogStatusInProgress),
		RepoPath:           repoDir,
	}
	createdItemData, err := storage.CreateBacklogItem(ctx, itemData)
	require.NoError(t, err)

	workSessionUUID := uuid.New().String()
	workIS, err := storage.CreateItemSession(ctx, ItemSessionData{
		ItemID:      createdItemData.ID,
		SessionUUID: workSessionUUID,
		SessionRole: SessionRoleWork,
	})
	require.NoError(t, err)

	// Same corruption as the blocking test: a well-formed but nonexistent base SHA.
	// worktreePath == repoDir keeps the fixture simple; GetGitDiff(worktree) fails the
	// same way GetGitDiffRef(repo fallback) does, exactly as observed for ae1e2070.
	inst := newTestInstance("diff-repair-test")
	inst.UUID = workSessionUUID
	inst.gitManager.worktree = git.NewGitWorktreeFromStorage(
		repoDir, repoDir, "diff-repair-test", "feature", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	require.NoError(t, storage.SaveInstances([]*Instance{inst}))

	item := &BacklogItemData{ID: createdItemData.ID, RepoPath: repoDir}

	verdictJSON := `{"overall":"PASS","summary":"all good","verdicts":[]}`
	verdictJSONEncoded, marshalErr := json.Marshal(verdictJSON)
	require.NoError(t, marshalErr)
	fakeResponse := fmt.Sprintf(`{"session_id":"test-s1","result":%s,"cost_usd":0.01}`, verdictJSONEncoded)
	fakeRunner := headless.NewFakeRunner(fakeResponse)
	pool := headless.NewPoolWithRunner(headless.PoolConfig{
		MaxCallsPerSession:    1,
		MaxConcurrentSessions: 1,
	}, fakeRunner)
	getPool := func() *headless.Pool { return pool }
	getAutoReopener := func() AutoReopenSpawner { return nil }

	notifier := &fakeNotifier{}
	getNotifier := func() Notifier { return notifier }

	runner := NewReviewGateRunner(storage, getPool, getAutoReopener, getNotifier, nil)

	var onPassCalled atomic.Bool
	runner.Run(ctx, item, workIS, func(ctx context.Context, item *BacklogItemData, is ItemSessionSummary) {
		onPassCalled.Store(true)
	})

	require.Equal(t, 1, fakeRunner.CallCount(), "the reviewer must actually be called on the recovered diff instead of the review being blocked")
	assert.True(t, onPassCalled.Load(), "onPass must fire — the recovered diff contains real, reviewable work")

	prompt := fakeRunner.StdinForCall(0)
	assert.Contains(t, prompt, "feature.txt", "the reviewer prompt must contain the real diff recovered via merge-base, not an empty one")

	assert.NotContains(t, notifier.calls, "Review blocked — diff computation failed", "the review must not be reported as blocked when auto-repair succeeded")
	assert.Contains(t, notifier.calls, "Review auto-repaired a broken diff", "the operator must still be told a repair happened, since the stored base_commit_sha is still wrong")
}
