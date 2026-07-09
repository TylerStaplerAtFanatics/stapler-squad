package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/tstapler/stapler-squad/log"
	"github.com/tstapler/stapler-squad/session/ent"
	"github.com/tstapler/stapler-squad/session/git"
	"github.com/tstapler/stapler-squad/session/headless"
)

// ReviewGateSpawner can create a short-lived review session for a backlog item.
// Deprecated: use headless.Pool via NewBacklogLifecycleListenerWithSpawner instead.
// Retained for backward compatibility with existing tests and callers.
type ReviewGateSpawner interface {
	// SpawnReviewSession creates a one-shot review session for item using prompt.
	// itemSessionID is the UUID of the work ItemSession being reviewed.
	SpawnReviewSession(ctx context.Context, item *ent.BacklogItem, itemSessionID string, prompt string) (*Instance, error)
}

// AutoReopenSpawner can automatically reopen a backlog item for rework after a
// failed review verdict (FAIL or PARTIAL). It transitions the item back to
// in_progress and spawns a new work session so the review→rework cycle is
// fully automated.
type AutoReopenSpawner interface {
	AutoReopenAfterFailedReview(ctx context.Context, itemID string) error
}

// PRFixSpawner can reopen a pr_pending item for rework when CI checks fail or
// reviewers request changes. The fixContext string contains a summary of the
// failures/comments to pass as context to the new work session.
type PRFixSpawner interface {
	AutoReopenForPRFix(ctx context.Context, itemID string, fixContext string) error
}

// maxConcurrentReviewGates is the maximum number of review gates that can run
// concurrently. This caps goroutine fan-out when many sessions exit simultaneously.
const maxConcurrentReviewGates = 8

// BacklogLifecycleListener drives backlog item state transitions in response to
// session lifecycle events. It must be registered via Instance.RegisterLifecycleListener.
//
// OnLifecycleEvent is non-blocking; all DB work is dispatched to a goroutine.
// Call SetEnabled(false) to make all callbacks no-ops without unwiring.
type BacklogLifecycleListener struct {
	storage        *Storage
	sessionCreator ReviewGateSpawner

	// poolMu guards headlessPool for concurrent Set/get access.
	poolMu       sync.RWMutex
	headlessPool *headless.Pool

	// autoReopenMu guards autoReopener for concurrent Set/get access.
	autoReopenMu sync.RWMutex
	autoReopener AutoReopenSpawner

	// prFixMu guards prFixSpawner for concurrent Set/get access.
	prFixMu      sync.RWMutex
	prFixSpawner PRFixSpawner

	// reviewSem limits concurrent review gate goroutines.
	reviewSem chan struct{}

	// shutdownCtx is cancelled by Shutdown(); used by long-running review gate calls.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc

	enabled atomic.Bool
}

// SetEnabled toggles whether this listener processes lifecycle events.
// Safe to call concurrently.
func (l *BacklogLifecycleListener) SetEnabled(v bool) { l.enabled.Store(v) }

// SetHeadlessPool wires in the headless LLM pool after construction.
// Calling this enables the headless review gate path even when the listener was
// created via NewBacklogLifecycleListenerWithSpawner.
func (l *BacklogLifecycleListener) SetHeadlessPool(p *headless.Pool) {
	l.poolMu.Lock()
	defer l.poolMu.Unlock()
	l.headlessPool = p
}

// SetAutoReopener wires in the spawner used to automatically reopen items for
// rework when a review verdict is FAIL or PARTIAL.
func (l *BacklogLifecycleListener) SetAutoReopener(r AutoReopenSpawner) {
	l.autoReopenMu.Lock()
	defer l.autoReopenMu.Unlock()
	l.autoReopener = r
}

// getAutoReopener returns the current auto-reopener under a read lock.
func (l *BacklogLifecycleListener) getAutoReopener() AutoReopenSpawner {
	l.autoReopenMu.RLock()
	defer l.autoReopenMu.RUnlock()
	return l.autoReopener
}

// SetPRFixSpawner wires in the spawner used to automatically reopen pr_pending
// items for rework when CI checks fail or reviewers request changes.
func (l *BacklogLifecycleListener) SetPRFixSpawner(s PRFixSpawner) {
	l.prFixMu.Lock()
	defer l.prFixMu.Unlock()
	l.prFixSpawner = s
}

// getPRFixSpawner returns the current PR fix spawner under a read lock.
func (l *BacklogLifecycleListener) getPRFixSpawner() PRFixSpawner {
	l.prFixMu.RLock()
	defer l.prFixMu.RUnlock()
	return l.prFixSpawner
}

// getHeadlessPool returns the current headless pool under a read lock.
func (l *BacklogLifecycleListener) getHeadlessPool() *headless.Pool {
	l.poolMu.RLock()
	defer l.poolMu.RUnlock()
	return l.headlessPool
}

// Shutdown cancels in-flight review gate calls. Safe to call concurrently.
func (l *BacklogLifecycleListener) Shutdown() {
	if l.shutdownCancel != nil {
		l.shutdownCancel()
	}
}

// newListenerBase initialises fields common to all BacklogLifecycleListener constructors.
func newListenerBase(storage *Storage) *BacklogLifecycleListener {
	ctx, cancel := context.WithCancel(context.Background())
	return &BacklogLifecycleListener{
		storage:        storage,
		reviewSem:      make(chan struct{}, maxConcurrentReviewGates),
		shutdownCtx:    ctx,
		shutdownCancel: cancel,
	}
}

// NewBacklogLifecycleListener creates a listener backed by the given storage.
// The review gate is disabled (sessionCreator=nil, headlessPool=nil).
func NewBacklogLifecycleListener(storage *Storage) *BacklogLifecycleListener {
	return newListenerBase(storage)
}

// NewBacklogLifecycleListenerWithSpawner creates a listener that will spawn a
// review gate session when a work session exits and SkipReviewGate is false.
func NewBacklogLifecycleListenerWithSpawner(storage *Storage, spawner ReviewGateSpawner) *BacklogLifecycleListener {
	l := newListenerBase(storage)
	l.sessionCreator = spawner
	return l
}

// NewBacklogLifecycleListenerWithPool creates a listener that uses a headless.Pool
// for review gate calls instead of spawning a tmux session.
func NewBacklogLifecycleListenerWithPool(storage *Storage, pool *headless.Pool) *BacklogLifecycleListener {
	l := newListenerBase(storage)
	l.headlessPool = pool
	return l
}

// instanceBacklogListener is a per-instance shim that binds the instance UUID into
// every lifecycle callback. Created and registered via WireToInstance.
type instanceBacklogListener struct {
	parent       *BacklogLifecycleListener
	instanceUUID string
}

func (il *instanceBacklogListener) OnLifecycleEvent(event LifecycleEvent, _ string) {
	if !il.parent.enabled.Load() {
		return
	}
	switch event {
	case EventStarted:
		go il.parent.onSessionStarted(il.instanceUUID)
	case EventExited:
		go il.parent.onSessionExited(il.instanceUUID)
	}
}

// WireToInstance creates a per-instance listener shim and registers it on inst.
// Call this for every Instance that should participate in backlog lifecycle tracking.
func (l *BacklogLifecycleListener) WireToInstance(inst *Instance) {
	inst.RegisterLifecycleListener(&instanceBacklogListener{
		parent:       l,
		instanceUUID: inst.UUID,
	})
}

// onSessionStarted records the start time for the ItemSession linked to sessionUUID.
func (l *BacklogLifecycleListener) onSessionStarted(sessionUUID string) {
	ctx := context.Background()
	is, err := l.storage.GetItemSessionBySessionUUID(ctx, sessionUUID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return
		}
		log.ErrorLog.Printf("[BacklogLifecycle] GetItemSessionBySessionUUID(%s) error: %v", sessionUUID, err)
		return
	}
	if err := l.storage.UpdateItemSessionStarted(ctx, is.ID.String(), time.Now()); err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] UpdateItemSessionStarted(%s) error: %v", is.ID, err)
	}
}

// onSessionExited drives the in_progress→review (or in_progress→done for skip_review_gate) transition.
func (l *BacklogLifecycleListener) onSessionExited(sessionUUID string) {
	ctx := context.Background()

	is, err := l.storage.GetItemSessionBySessionUUID(ctx, sessionUUID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return
		}
		log.ErrorLog.Printf("[BacklogLifecycle] GetItemSessionBySessionUUID(%s) error: %v", sessionUUID, err)
		return
	}

	// Record end time for all session roles (triage, review, work).
	now := time.Now()
	if err := l.storage.UpdateItemSessionEnded(ctx, is.ID.String(), now); err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] UpdateItemSessionEnded(%s) error: %v", is.ID, err)
	}

	// Only drive in_progress→review/done transitions for work sessions.
	if is.SessionRole != SessionRoleWork {
		return
	}

	// BacklogItem edge is eager-loaded by GetItemSessionBySessionUUID.
	item, err := is.Edges.BacklogItemOrErr()
	if err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] BacklogItemOrErr for session %s: %v", sessionUUID, err)
		return
	}

	if BacklogStatus(item.Status) != BacklogStatusInProgress {
		log.DebugLog.Printf("[BacklogLifecycle] item %s is %s (not in_progress); skipping", item.ID, item.Status)
		return
	}

	toStatus := BacklogStatusReview
	if item.SkipReviewGate {
		toStatus = BacklogStatusDone
	}

	updatedAt := item.UpdatedAt
	precondition := &BacklogItemPrecondition{
		ExpectedStatus:    string(BacklogStatusInProgress),
		ExpectedUpdatedAt: &updatedAt,
	}
	if _, err := l.storage.TransitionBacklogItemStatus(ctx, item.ID.String(), toStatus, precondition); err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] TransitionBacklogItemStatus item=%s to=%s: %v", item.ID, toStatus, err)
		return
	}

	log.InfoLog.Printf("[BacklogLifecycle] item %s transitioned to %s (session %s exited)", item.ID, toStatus, sessionUUID)

	// Spawn review gate if the item moved to review and a review mechanism is configured.
	if toStatus == BacklogStatusReview && !item.SkipReviewGate && (l.getHeadlessPool() != nil || l.sessionCreator != nil) {
		go func() {
			// Acquire the bounded semaphore to prevent unbounded goroutine fan-out
			// when many sessions exit simultaneously.
			select {
			case l.reviewSem <- struct{}{}:
			case <-l.shutdownCtx.Done():
				return
			}
			defer func() { <-l.reviewSem }()
			l.spawnReviewGate(item, is)
		}()
	}
}

// TriggerReviewForSession immediately spawns a review gate for the work session
// identified by workSessionUUID. Used by the autonomous driver to trigger review
// as soon as the driver signals DONE, rather than waiting for ReconcileStuck.
// No-op if the listener is disabled or no review mechanism is configured.
func (l *BacklogLifecycleListener) TriggerReviewForSession(workSessionUUID string) {
	if !l.enabled.Load() {
		return
	}
	if l.getHeadlessPool() == nil && l.sessionCreator == nil {
		return
	}
	go func() {
		select {
		case l.reviewSem <- struct{}{}:
		case <-l.shutdownCtx.Done():
			return
		}
		defer func() { <-l.reviewSem }()

		ctx := l.shutdownCtx
		is, err := l.storage.GetItemSessionBySessionUUID(ctx, workSessionUUID)
		if err != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] TriggerReviewForSession GetItemSessionBySessionUUID(%s): %v", workSessionUUID, err)
			return
		}
		item, err := is.Edges.BacklogItemOrErr()
		if err != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] TriggerReviewForSession BacklogItemOrErr session=%s: %v", workSessionUUID, err)
			return
		}
		if item.SkipReviewGate {
			return
		}
		log.InfoLog.Printf("[BacklogLifecycle] TriggerReviewForSession: spawning immediate review gate item=%s session=%s", item.ID, workSessionUUID)
		l.spawnReviewGate(item, is)
	}()
}

// applyVerdictsToACs updates the acceptance criteria status fields on a backlog
// item to reflect the review verdict for each criterion:
//
//	PASS  → "done"
//	PARTIAL → "in_progress"
//	FAIL / UNVERIFIABLE → unchanged (stay "pending")
//
// Best-effort: errors are logged but do not block the caller.
func applyVerdictsToACs(ctx context.Context, storage *Storage, item *ent.BacklogItem, acSnapshot []AcCriterion, verdicts []CriterionVerdict) {
	if len(verdicts) == 0 || len(acSnapshot) == 0 {
		return
	}

	outcomeByIdx := make(map[int]string, len(verdicts))
	for _, v := range verdicts {
		outcomeByIdx[v.CriterionIndex] = v.Outcome
	}

	updated := make([]AcCriterion, len(acSnapshot))
	copy(updated, acSnapshot)
	changed := false
	for i, ac := range updated {
		outcome, ok := outcomeByIdx[ac.Index]
		if !ok {
			continue
		}
		var newStatus string
		switch outcome {
		case ReviewVerdictPass:
			newStatus = "done"
		case ReviewVerdictPartial:
			newStatus = "in_progress"
		default:
			continue // FAIL / UNVERIFIABLE: leave as-is
		}
		if newStatus != ac.Status {
			updated[i].Status = newStatus
			changed = true
		}
	}

	if !changed {
		return
	}

	newJSON, err := SerializeAcCriteria(updated)
	if err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] applyVerdictsToACs serialize item=%s: %v", item.ID, err)
		return
	}
	if _, err := storage.UpdateBacklogItem(ctx, item.ID.String(), BacklogItemUpdate{AcceptanceCriteria: &newJSON}, nil); err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] applyVerdictsToACs update item=%s: %v", item.ID, err)
		return
	}
	log.InfoLog.Printf("[BacklogLifecycle] applyVerdictsToACs: updated AC statuses for item=%s (%d criteria)", item.ID, len(updated))
}

// spawnReviewGate creates a one-shot review session for item, using the diff
// from the work session's worktree.
func (l *BacklogLifecycleListener) spawnReviewGate(item *ent.BacklogItem, is *ent.ItemSession) {
	ctx := l.shutdownCtx

	// Precondition: repo_path must be set or we have nothing to review.
	if item.RepoPath == "" {
		log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate item=%s has no repo path set; skipping review gate", item.ID)
		return
	}

	// Get the git diff from the session's dedicated worktree (if one exists).
	// Fall back to the item's repo path for directory-mode sessions.
	diffDir := item.RepoPath
	diffBaseSHA := is.LastCommitSha
	if wt, wtErr := l.storage.GetWorktreeDataBySessionUUID(ctx, is.SessionUUID); wtErr == nil && wt.WorktreePath != "" {
		diffDir = wt.WorktreePath
		diffBaseSHA = wt.BaseCommitSHA
	}
	diff, truncated, diffErr := GetGitDiff(ctx, diffDir, diffBaseSHA)
	if diffErr != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate GetGitDiff item=%s: %v", item.ID, diffErr)
		diff = ""
	}

	// Security check — block if secrets detected.
	if secErr := RunPreGateSecurityCheck(diff); secErr != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate security check blocked item=%s: %v", item.ID, secErr)
		// Record a failed review ItemSession with a FAIL verdict so the gate verdict
		// is visible in the UI and operators can act (override or re-review).
		summary := fmt.Sprintf("Review blocked by security check: %v. Override required to proceed.", secErr)
		secIS, _, secCreateErr := l.storage.CreateItemSessionWithVerdict(ctx, ItemSessionData{
			ItemID:      item.ID.String(),
			SessionUUID: "review-blocked-" + item.ID.String(),
			SessionRole: SessionRoleReview,
		}, ReviewVerdictData{
			OverallOutcome: ReviewVerdictFail,
			Summary:        summary,
		})
		if secCreateErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate CreateItemSessionWithVerdict (security block) item=%s: %v", item.ID, secCreateErr)
			return
		}
		if updateErr := l.storage.UpdateItemSessionEnded(ctx, secIS.ID.String(), time.Now()); updateErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate UpdateItemSessionEnded (security block) item=%s: %v", item.ID, updateErr)
		}
		log.InfoLog.Printf("[BacklogLifecycle] spawnReviewGate security check blocked for item %s — FAIL verdict recorded", item.ID)
		return
	}

	// Deserialize AC snapshot.
	acSnapshot, _ := ParseAcCriteria(is.AcSnapshot)
	if len(acSnapshot) == 0 {
		acSnapshot, _ = ParseAcCriteria(item.AcceptanceCriteria)
	}

	prompt := BuildReviewPrompt(item, acSnapshot, diff, truncated, is.ID.String())

	pool := l.getHeadlessPool()
	if pool != nil {
		// Headless path: call LLM directly without spawning a tmux session.
		// Use JSON-output prompts because headless claude -p has no tool access.
		reviewCtx, reviewCancel := context.WithTimeout(ctx, headless.DefaultCallTimeout)
		defer reviewCancel()

		headlessPrompt := BuildHeadlessReviewPrompt(item, acSnapshot, diff, truncated)
		reviewResult, callCostUSD, callErr := pool.CallBlockingWithCost(reviewCtx, headless.FeatureKeyReview, headless.HeadlessReviewSystemPrompt(), headlessPrompt)
		if callErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate headless.CallBlocking item=%s: %v", item.ID, callErr)
			// Record a FAIL verdict so the item is not stuck in review with no actionable result.
			failUUID := "headless-review-" + uuid.New().String()
			failIS, _, createErr := l.storage.CreateItemSessionWithVerdict(ctx, ItemSessionData{
				ItemID:      item.ID.String(),
				SessionUUID: failUUID,
				SessionRole: SessionRoleReview,
				AcSnapshot:  is.AcSnapshot,
			}, ReviewVerdictData{
				OverallOutcome: ReviewVerdictFail,
				Summary:        fmt.Sprintf("Review failed: %v", callErr),
			})
			if createErr != nil {
				log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate CreateItemSessionWithVerdict (headless fail) item=%s: %v", item.ID, createErr)
			} else if updateErr := l.storage.UpdateItemSessionEnded(ctx, failIS.ID.String(), time.Now()); updateErr != nil {
				log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate UpdateItemSessionEnded (headless fail) item=%s: %v", item.ID, updateErr)
			}
			return
		}

		overall, perCriterion, summary := ParseHeadlessVerdictResult(reviewResult)
		perCriterionJSON, _ := json.Marshal(perCriterion)

		// Update AC statuses on the item to reflect what was verified.
		applyVerdictsToACs(ctx, l.storage, item, acSnapshot, perCriterion)

		// Create a synthetic ItemSession and its ReviewVerdict atomically so there
		// is never a dangling session with no verdict if the verdict write fails.
		reviewSessionUUID := "headless-review-" + uuid.New().String()
		reviewIS, _, createErr := l.storage.CreateItemSessionWithVerdict(ctx, ItemSessionData{
			ItemID:           item.ID.String(),
			SessionUUID:      reviewSessionUUID,
			SessionRole:      SessionRoleReview,
			AcSnapshot:       is.AcSnapshot,
			EstimatedCostUsd: callCostUSD,
		}, ReviewVerdictData{
			OverallOutcome: overall,
			PerCriterion:   string(perCriterionJSON),
			Summary:        summary,
		})
		if createErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate CreateItemSessionWithVerdict (headless) item=%s: %v", item.ID, createErr)
			return
		}
		if updateErr := l.storage.UpdateItemSessionEnded(ctx, reviewIS.ID.String(), time.Now()); updateErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate UpdateItemSessionEnded (headless) item=%s: %v", item.ID, updateErr)
		}

		log.InfoLog.Printf("[BacklogLifecycle] spawnReviewGate headless review complete for item %s (session %s, outcome %s)", item.ID, reviewIS.ID, overall)

		// Auto-reopen: if verdict is FAIL or PARTIAL, immediately transition the item
		// back to in_progress and spawn a new work session so the review→rework cycle
		// is fully automated without requiring manual intervention.
		if reopener := l.getAutoReopener(); (overall == ReviewVerdictFail || overall == ReviewVerdictPartial) && reopener != nil {
			go func() {
				if err := reopener.AutoReopenAfterFailedReview(l.shutdownCtx, item.ID.String()); err != nil {
					log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate AutoReopenAfterFailedReview item=%s: %v", item.ID, err)
				} else {
					log.InfoLog.Printf("[BacklogLifecycle] spawnReviewGate auto-reopened item %s for rework (verdict %s)", item.ID, overall)
				}
			}()
		}

		// On PASS: push the branch, create a PR, and move to pr_pending so the work
		// is visible on GitHub and a human (or the reconciler) can merge it to done.
		// Falls back to direct done transition when no worktree is available.
		if overall == ReviewVerdictPass {
			l.pushAndCreatePR(ctx, item, is)
		}
		return
	}

	// Legacy path: spawn a tmux review session via sessionCreator.
	if l.sessionCreator == nil {
		log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate item=%s: no review mechanism configured", item.ID)
		return
	}

	reviewInst, spawnErr := l.sessionCreator.SpawnReviewSession(ctx, item, is.ID.String(), prompt)
	if spawnErr != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate SpawnReviewSession item=%s: %v", item.ID, spawnErr)
		return
	}

	// Create ItemSession linking the new review session to the backlog item.
	if _, createErr := l.storage.CreateItemSession(ctx, ItemSessionData{
		ItemID:      item.ID.String(),
		SessionUUID: reviewInst.UUID,
		SessionRole: SessionRoleReview,
		AcSnapshot:  is.AcSnapshot,
	}); createErr != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] spawnReviewGate CreateItemSession item=%s review=%s: %v", item.ID, reviewInst.UUID, createErr)
		return
	}

	log.InfoLog.Printf("[BacklogLifecycle] spawnReviewGate spawned review session %s for item %s", reviewInst.UUID, item.ID)
}

// ReconcileStuck calls ReconcileStuckItems and logs the result.
// Intended to be called on a periodic ticker as a safety net for abnormal session exits.
// No-op when the listener is disabled.
func (l *BacklogLifecycleListener) ReconcileStuck(ctx context.Context) {
	if !l.enabled.Load() {
		return
	}
	er, ok := l.storage.repo.(*EntRepository)
	if !ok {
		return
	}
	n, err := er.ReconcileStuckItems(ctx)
	if err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] ReconcileStuckItems error: %v", err)
		return
	}
	if n > 0 {
		log.InfoLog.Printf("[BacklogLifecycle] ReconcileStuckItems: transitioned %d stuck items to review", n)
	} else {
		log.DebugLog.Printf("[BacklogLifecycle] ReconcileStuckItems: no stuck items found")
	}

	// Re-spawn review gates for items stuck in "review" with no review session.
	// Occurs when the headless pool was unavailable at the time of the work session exit.
	if l.getHeadlessPool() == nil && l.sessionCreator == nil {
		return
	}
	items, gateErr := er.FindReviewItemsWithoutGate(ctx)
	if gateErr != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] FindReviewItemsWithoutGate error: %v", gateErr)
		return
	}
	for _, item := range items {
		var workSession *ent.ItemSession
		if len(item.Edges.ItemSessions) > 0 {
			workSession = item.Edges.ItemSessions[0]
		}
		if workSession == nil {
			log.DebugLog.Printf("[BacklogLifecycle] ReconcileStuckReviewGates: item %s has no work session, skipping", item.ID)
			continue
		}
		log.InfoLog.Printf("[BacklogLifecycle] ReconcileStuckReviewGates: re-spawning review gate for item %s", item.ID)
		is := workSession
		go func(itemCopy *ent.BacklogItem, isCopy *ent.ItemSession) {
			select {
			case l.reviewSem <- struct{}{}:
			case <-l.shutdownCtx.Done():
				return
			}
			defer func() { <-l.reviewSem }()
			l.spawnReviewGate(itemCopy, isCopy)
		}(item, is)
	}

	// Poll pr_pending items: auto-transition to done when the PR is merged.
	l.ReconcilePRPending(ctx, er)
}

// pushAndCreatePR commits any dirty state, pushes the branch, creates a GitHub PR,
// stores the PR URL and number on the item, then transitions to pr_pending.
// Falls back to direct done transition when no worktree exists or gh CLI is unavailable.
func (l *BacklogLifecycleListener) pushAndCreatePR(ctx context.Context, item *ent.BacklogItem, is *ent.ItemSession) {
	fallbackToDone := func(reason string) {
		log.InfoLog.Printf("[BacklogLifecycle] pushAndCreatePR item=%s falling back to done: %s", item.ID, reason)
		precondition := &BacklogItemPrecondition{ExpectedStatus: string(BacklogStatusReview)}
		if _, transErr := l.storage.TransitionBacklogItemStatus(ctx, item.ID.String(), BacklogStatusDone, precondition); transErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] pushAndCreatePR fallback done item=%s: %v", item.ID, transErr)
		}
	}

	wt, wtErr := l.storage.GetWorktreeDataBySessionUUID(ctx, is.SessionUUID)
	if wtErr != nil || wt.WorktreePath == "" {
		fallbackToDone("no worktree")
		return
	}

	g := git.NewGitWorktreeFromStorage(wt.RepoPath, wt.WorktreePath, wt.SessionName, wt.BranchName, wt.BaseCommitSHA)

	// Commit any remaining dirty state.
	commitMsg := fmt.Sprintf("[claudesquad] work complete for %q (pre-PR)", item.Title)
	if commitErr := g.CommitChanges(commitMsg); commitErr != nil {
		log.WarningLog.Printf("[BacklogLifecycle] pushAndCreatePR commit item=%s: %v", item.ID, commitErr)
	}

	// Push branch to origin.
	if pushErr := g.PushBranch(); pushErr != nil {
		log.WarningLog.Printf("[BacklogLifecycle] pushAndCreatePR push item=%s: %v", item.ID, pushErr)
		fallbackToDone("push failed")
		return
	}

	// Create (or locate existing) PR.
	var prURL string
	var prNumber int
	if item.PrNumber > 0 && item.PrURL != "" {
		// PR already exists from a previous attempt — just use it.
		prURL = item.PrURL
		prNumber = item.PrNumber
		log.InfoLog.Printf("[BacklogLifecycle] pushAndCreatePR item=%s reusing existing PR #%d", item.ID, prNumber)
	} else {
		prTitle := item.Title
		prBody := fmt.Sprintf("Automated PR for backlog item: %s\n\nItem ID: %s", item.Title, item.ID)
		var prErr error
		prURL, prNumber, prErr = g.CreatePR(prTitle, prBody)
		if prErr != nil {
			log.WarningLog.Printf("[BacklogLifecycle] pushAndCreatePR create PR item=%s: %v", item.ID, prErr)
			fallbackToDone("PR creation failed")
			return
		}
		// Cache PR URL + number on the item so the reconciler and UI can use them.
		prURLCopy := prURL
		prNumCopy := prNumber
		if _, updateErr := l.storage.UpdateBacklogItem(ctx, item.ID.String(), BacklogItemUpdate{
			PrURL:    &prURLCopy,
			PrNumber: &prNumCopy,
		}, nil); updateErr != nil {
			log.WarningLog.Printf("[BacklogLifecycle] pushAndCreatePR store PR fields item=%s: %v", item.ID, updateErr)
		}
	}

	// Enable GitHub auto-merge so the PR merges automatically once CI passes.
	// Best-effort: repos without branch protection or auto-merge enabled will fail here,
	// and ReconcilePRPending will detect the merge via polling instead.
	if autoErr := g.EnablePRAutoMerge(prNumber); autoErr != nil {
		log.WarningLog.Printf("[BacklogLifecycle] pushAndCreatePR auto-merge item=%s pr=%d: %v", item.ID, prNumber, autoErr)
	} else {
		log.InfoLog.Printf("[BacklogLifecycle] pushAndCreatePR item=%s PR #%d auto-merge enabled", item.ID, prNumber)
	}

	// Transition to pr_pending.
	precondition := &BacklogItemPrecondition{ExpectedStatus: string(BacklogStatusReview)}
	if _, transErr := l.storage.TransitionBacklogItemStatus(ctx, item.ID.String(), BacklogStatusPRPending, precondition); transErr != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] pushAndCreatePR pr_pending transition item=%s: %v", item.ID, transErr)
	} else {
		log.InfoLog.Printf("[BacklogLifecycle] pushAndCreatePR item=%s → pr_pending (PR #%d %s)", item.ID, prNumber, prURL)
	}
}

// ReconcilePRPending polls items in pr_pending status. It transitions to done
// when the PR is merged, and spawns a fix session when CI fails or reviewers
// request changes.
func (l *BacklogLifecycleListener) ReconcilePRPending(ctx context.Context, er *EntRepository) {
	items, err := er.FindPRPendingItems(ctx)
	if err != nil {
		log.ErrorLog.Printf("[BacklogLifecycle] ReconcilePRPending query error: %v", err)
		return
	}
	for _, item := range items {
		if item.PrNumber == 0 || item.PrURL == "" {
			continue
		}
		repoPath := item.RepoPath
		if repoPath == "" {
			continue
		}
		g := git.NewGitWorktreeFromStorage(repoPath, repoPath, "", "", "")

		// 1. Check if the PR has been merged → done.
		merged, mergedErr := g.IsPRMerged(item.PrNumber)
		if mergedErr != nil {
			log.DebugLog.Printf("[BacklogLifecycle] ReconcilePRPending IsPRMerged item=%s pr=%d: %v", item.ID, item.PrNumber, mergedErr)
			continue
		}
		if merged {
			precondition := &BacklogItemPrecondition{ExpectedStatus: string(BacklogStatusPRPending)}
			if _, transErr := l.storage.TransitionBacklogItemStatus(ctx, item.ID.String(), BacklogStatusDone, precondition); transErr != nil {
				log.ErrorLog.Printf("[BacklogLifecycle] ReconcilePRPending done transition item=%s: %v", item.ID, transErr)
			} else {
				log.InfoLog.Printf("[BacklogLifecycle] ReconcilePRPending item=%s → done (PR #%d merged)", item.ID, item.PrNumber)
			}
			continue
		}

		// 2. PR still open — check CI status and reviews.
		prStatus, statusErr := g.GetPRStatus(item.PrNumber)
		if statusErr != nil {
			log.DebugLog.Printf("[BacklogLifecycle] ReconcilePRPending GetPRStatus item=%s pr=%d: %v", item.ID, item.PrNumber, statusErr)
			continue
		}
		if !prStatus.CIFailing && !prStatus.HasBlockingReviews {
			continue // PR is open and healthy — wait for merge.
		}

		// 3. CI failure or review changes requested → spawn fix session.
		fixSpawner := l.getPRFixSpawner()
		if fixSpawner == nil {
			log.WarningLog.Printf("[BacklogLifecycle] ReconcilePRPending item=%s: CI/review issues found but no PRFixSpawner configured", item.ID)
			continue
		}
		fixCtx := fmt.Sprintf("PR #%d (%s) needs fixes:\n\n%s", item.PrNumber, item.PrURL, prStatus.FeedbackText)
		log.InfoLog.Printf("[BacklogLifecycle] ReconcilePRPending item=%s → in_progress for PR fix (CI=%v, reviews=%v)",
			item.ID, prStatus.CIFailing, prStatus.HasBlockingReviews)
		if fixErr := fixSpawner.AutoReopenForPRFix(ctx, item.ID.String(), fixCtx); fixErr != nil {
			log.ErrorLog.Printf("[BacklogLifecycle] ReconcilePRPending AutoReopenForPRFix item=%s: %v", item.ID, fixErr)
		}
	}
}
