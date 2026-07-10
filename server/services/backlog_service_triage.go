package services

// backlog_service_triage.go — session spawning and triage/review orchestration handlers
// for BacklogService. Covers the full lifecycle of headless triage, review, and re-review.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/log"
	"github.com/tstapler/stapler-squad/session"
	"github.com/tstapler/stapler-squad/session/ent"
	"github.com/tstapler/stapler-squad/session/headless"
)

// headlessTriageUUIDPrefix is prepended to all synthetic ItemSession UUIDs created by the
// headless triage path. The orphan guard uses this prefix to identify sessions that have no
// live tmux process and can be safely tombstoned on re-trigger.
const headlessTriageUUIDPrefix = "headless-triage-"

// maxAutoReworkIterations caps how many automated work sessions can be spawned for a single
// backlog item by the auto-reopen loop. When this ceiling is hit, the item stays in review
// so a human can inspect it rather than spinning indefinitely on a persistent FAIL verdict.
const maxAutoReworkIterations = 3

// defaultTriageCleanupTimeout bounds the DB writes TriggerTriage's goroutine makes
// after its headless LLM call returns (persist result, update plan_artifacts_path,
// transition idea->ready, mark session ended). See BacklogService.triageCleanupTimeout
// for why this needed to become configurable rather than a global.
const defaultTriageCleanupTimeout = 10 * time.Second

// defaultTriggerSyncTimeout bounds a single manual TriggerSync RPC call. The
// GitHub PRs plugin issues one extra HTTP call per open PR (for CI status), so
// this is generous relative to the "seconds, not minutes" expectation for a
// single page of items — without it, a slow/rate-limited GitHub response would
// block the RPC handler for however long the client's transport allows.
const defaultTriggerSyncTimeout = 2 * time.Minute

// maxTriageSessionAge is the maximum age of an open triage ItemSession before it is
// treated as orphaned in the re-trigger guard. This prevents a hung or leaked session
// from blocking re-trigger indefinitely.
const maxTriageSessionAge = 2 * time.Hour

// slugify converts s to a lowercase hyphen-delimited slug safe for file paths.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// triageShortTitle extracts the triage-suggested short title from the most recent
// completed triage ItemSession, falling back to a truncated slug of itemTitle.
func triageShortTitle(sessions []*ent.ItemSession, itemTitle string) string {
	for i := len(sessions) - 1; i >= 0; i-- {
		s := sessions[i]
		if s.SessionRole != string(session.SessionRoleTriage) || s.TriageResult == "" {
			continue
		}
		var r session.HeadlessTriageResult
		if err := json.Unmarshal([]byte(s.TriageResult), &r); err == nil && r.Title != "" {
			return r.Title
		}
	}
	// Fallback: first 4 words of the slug.
	parts := strings.SplitN(slugify(itemTitle), "-", 5)
	if len(parts) > 4 {
		parts = parts[:4]
	}
	return strings.Join(parts, "-")
}

func (s *BacklogService) SpawnSessionFromItem(
	ctx context.Context,
	req *connect.Request[sessionv1.SpawnSessionFromItemRequest],
) (*connect.Response[sessionv1.SpawnSessionFromItemResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// 1. Load item.
	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	// 2. If force=true, clear any in-flight sessions and reset status so the normal
	// path below can proceed. Handles both in_progress (stop work session) and review
	// (stop review session + transition back to in_progress so restart begins from
	// the work phase where the git worktree and slash commands are set up).
	if req.Msg.Force && (item.Status == string(session.BacklogStatusInProgress) ||
		item.Status == string(session.BacklogStatusReview)) {

		// Load sessions early so we can stop them before the status transition.
		earlyPrior, _ := s.storage.ListItemSessions(ctx, item.ID)
		for _, ps := range earlyPrior {
			if ps.EndedAt != nil {
				continue
			}
			if ps.SessionRole != string(session.SessionRoleWork) && ps.SessionRole != string(session.SessionRoleReview) {
				continue
			}
			if s.sessionStopper != nil {
				_ = s.sessionStopper.StopSessionByUUID(ctx, ps.SessionUUID)
			}
			_ = s.storage.UpdateItemSessionEnded(ctx, ps.ID.String(), time.Now())
		}

		// If the item is in review, transition it back to in_progress so the spawn
		// path treats this as a reopen (not a first spawn requiring plan approval).
		if item.Status == string(session.BacklogStatusReview) {
			updated, transErr := s.storage.TransitionBacklogItemStatus(ctx, item.ID, session.BacklogStatusInProgress, nil)
			if transErr != nil {
				return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to reset item to in_progress for restart: %w", transErr))
			}
			item = updated
		}
	}

	// 3. Validate status. Allow ready (first spawn) or in_progress (re-spawn after reopen).
	isReopen := item.Status == string(session.BacklogStatusInProgress)
	if item.Status != string(session.BacklogStatusReady) && !isReopen {
		log.InfoLog.Printf("[SpawnSessionFromItem] status gate blocked spawn item=%s status=%s autonomous=%v", item.ID, item.Status, req.Msg.Autonomous)
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("item must be in %q or %q status to spawn a session, got %q — use TriggerTriage to advance from %q",
				session.BacklogStatusReady, session.BacklogStatusInProgress, item.Status, item.Status))
	}

	// 4. Planning gate (only for fresh spawns; on reopen planning is already approved).
	// Autonomous mode bypasses the gate — the driver handles its own planning loop.
	if !isReopen && !item.SkipPlanning && !item.PlanApproved && !req.Msg.Autonomous {
		log.InfoLog.Printf("[SpawnSessionFromItem] planning gate blocked spawn item=%s status=%s autonomous=false", item.ID, item.Status)
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("run TriggerTriage and approve the plan before spawning, or use 'Run Autonomously' to skip the planning gate"))
	}

	// 5. Repo path required.
	if item.RepoPath == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("set repo_path before spawning a session"))
	}

	// 6. Require SessionCreator before doing any DB writes.
	// degraded: sessionCreator unavailable — return CodeUnimplemented so callers can detect the gap.
	if s.sessionCreator == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("SessionCreator not wired — contact admin"))
	}

	// 7. Snapshot current AC.
	acSnapshot := item.AcceptanceCriteria

	// 8. Load prior sessions for context.
	priorSessions, err := s.storage.ListItemSessions(ctx, item.ID)
	if err != nil {
		log.WarningLog.Printf("[SpawnSessionFromItem] failed to load prior sessions for item %s: %v", item.ID, err)
		priorSessions = nil
	}

	// 8b. Guard against spawning a duplicate work session when one is already active.
	for _, ps := range priorSessions {
		if ps.SessionRole == session.SessionRoleWork && ps.EndedAt == nil {
			return nil, connect.NewError(connect.CodeAlreadyExists,
				fmt.Errorf("a work session is already active for this item; wait for it to finish or kill it first"))
		}
	}

	// 8. Build agent prompt.
	// Parse item.ID as UUID for the ent struct (needed by BuildTokenBudgetedPrompt for logging).
	itemUUID, _ := uuid.Parse(item.ID)
	entItem := &ent.BacklogItem{
		ID:                 itemUUID,
		Title:              item.Title,
		Description:        item.Description,
		AcceptanceCriteria: string(item.AcceptanceCriteria),
		Priority:           item.Priority,
		Status:             item.Status,
		Notes:              item.Notes,
		PlanArtifactsPath:  item.PlanArtifactsPath,
		PlanApproved:       item.PlanApproved,
		SkipPlanning:       item.SkipPlanning,
	}
	prompt := session.BuildTokenBudgetedPrompt(entItem, priorSessions)

	// 9. Generate session title.
	// On reopen, append a revision number (r2, r3…) based on how many work sessions
	// already exist so the session list shows distinct, human-readable names.
	repoName := slugify(filepath.Base(item.RepoPath))
	baseTitle := repoName + "-" + triageShortTitle(priorSessions, item.Title)
	title := baseTitle
	if isReopen {
		workCount := 0
		for _, s := range priorSessions {
			if s.SessionRole == string(session.SessionRoleWork) {
				workCount++
			}
		}
		title = fmt.Sprintf("%s-r%d", baseTitle, workCount+1)
	}

	// 10. Create a dedicated git worktree for this work session. Falls back to a plain
	// directory session if the repo is not git-managed (or worktree creation fails for
	// any other reason — e.g. a bare clone, a detached HEAD, or disk quota hit).
	// Files must be written to the session path BEFORE spawning.
	// worktreeMu guards concurrent spawns from interleaving writes to the same path.
	worktreePath, wtErr := session.CreateBacklogWorktree(item.RepoPath, slugify(title))
	useWorktree := wtErr == nil
	if !useWorktree {
		log.WarningLog.Printf("[SpawnSessionFromItem] worktree creation failed (%v), falling back to directory mode", wtErr)
		var pathErr error
		worktreePath, pathErr = session.ResolveSessionPath(item.RepoPath)
		if pathErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid repo_path: %w", pathErr))
		}
		if dirErr := session.EnsureDirectorySessionPath(worktreePath); dirErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to prepare session directory: %w", dirErr))
		}
	}

	s.worktreeMu.Lock()
	if wErr := session.WriteSlashCommands(entItem, worktreePath); wErr != nil {
		s.worktreeMu.Unlock()
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("WriteSlashCommands: %w", wErr))
	}
	if wErr := session.WriteBacklogContextFile(entItem, priorSessions, worktreePath); wErr != nil {
		s.worktreeMu.Unlock()
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("WriteBacklogContextFile: %w", wErr))
	}
	s.worktreeMu.Unlock()

	// 11. Spawn session first so we have the real UUID before creating the ItemSession record.
	spawnTags := []string{session.TagBacklogWork}
	if isReopen {
		spawnTags = append(spawnTags, session.TagBacklogRevision)
	}
	if req.Msg.Autonomous {
		spawnTags = append(spawnTags, session.TagAutonomous)
	}
	var inst *session.Instance
	if useWorktree {
		inst, err = s.sessionCreator.CreateWorktreeSession(ctx, title, item.RepoPath, worktreePath, prompt,
			spawnTags, false, false)
	} else {
		inst, err = s.sessionCreator.CreateDirectorySession(ctx, title, worktreePath, prompt,
			spawnTags, false, false)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to spawn session: %w", err))
	}

	if req.Msg.Autonomous {
		if s.autonomousStarter != nil {
			log.InfoLog.Printf("[SpawnSessionFromItem] starting autonomous driver item=%s session=%s", item.ID, inst.UUID)
			s.autonomousStarter.StartAutonomousDriverForInstance(inst)
		} else {
			log.WarningLog.Printf("[SpawnSessionFromItem] autonomous=true but no driver starter wired item=%s session=%s — session will need manual approval", item.ID, inst.UUID)
		}
	}

	// 12. Create ItemSession with the real session UUID (avoids "<pending>" orphan records on failure).
	is, err := s.storage.CreateItemSession(ctx, session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: inst.UUID,
		SessionRole: session.SessionRoleWork,
		AcSnapshot:  acSnapshot,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create item session: %w", err))
	}

	// 12b. Capture the pre-work HEAD SHA so the review gate can diff base..HEAD across
	// all commits the agent makes (not just HEAD~1..HEAD at review time).
	if baseSHA, shaErr := session.GetGitHeadSHA(worktreePath); shaErr == nil && baseSHA != "" {
		_ = s.storage.UpdateItemSessionGitActivity(ctx, is.ID.String(), baseSHA, "", time.Now(), 0)
		inst.SetDirBaseSHA(baseSHA)
	}

	// 12c. On reopen, clean up git worktrees from prior work sessions now that the
	// new session is safely persisted. Best-effort only — errors are logged, not returned.
	if isReopen {
		s.cleanupItemWorktrees(ctx, priorSessions)
	}

	// 13. Transition item to in_progress (no-op if already in_progress on reopen).
	if !isReopen {
		if _, transErr := s.storage.TransitionBacklogItemStatus(ctx, item.ID, session.BacklogStatusInProgress, nil); transErr != nil {
			log.ErrorLog.Printf("[SpawnSessionFromItem] failed to transition item to in_progress: %v", transErr)
		}
	}

	return connect.NewResponse(&sessionv1.SpawnSessionFromItemResponse{
		SessionUuid: inst.UUID,
		ItemSession: itemSessionToProto(is, s.buildCostLookup()),
	}), nil
}

// AutoReopenAfterFailedReview implements session.AutoReopenSpawner.
// It transitions the item from review back to in_progress and spawns a new
// work session so the review→rework cycle runs without manual intervention.
func (s *BacklogService) AutoReopenAfterFailedReview(ctx context.Context, itemID string) error {
	if s.storage == nil {
		return fmt.Errorf("storage not available")
	}

	// Load item to check current status and obtain updated_at for the precondition.
	item, err := s.storage.GetBacklogItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("load item: %w", err)
	}

	// Iteration cap: count prior work sessions so we don't spin forever on a
	// persistent FAIL verdict. Fail-safe: if the DB query errors we cannot know
	// the true count, so we bail rather than risk an unbounded loop.
	sessions, sessErr := s.storage.ListItemSessions(ctx, item.ID)
	if sessErr != nil {
		return fmt.Errorf("list sessions for cap check: %w", sessErr)
	}
	workCount := 0
	for _, is := range sessions {
		if is.SessionRole == session.SessionRoleWork {
			workCount++
		}
	}
	if workCount >= maxAutoReworkIterations {
		log.InfoLog.Printf("[AutoReopenAfterFailedReview] item %s has %d work sessions (cap %d); leaving in review for manual action", itemID, workCount, maxAutoReworkIterations)
		return nil
	}

	// Transition review → in_progress with a precondition to guard against races
	// (e.g. concurrent manual reopen firing at the same time).
	updatedAt := item.UpdatedAt
	precondition := &session.BacklogItemPrecondition{
		ExpectedStatus:    string(session.BacklogStatusReview),
		ExpectedUpdatedAt: &updatedAt,
		Note:              "auto-reopened after failed review verdict",
	}
	if _, err := s.storage.TransitionBacklogItemStatus(ctx, itemID, session.BacklogStatusInProgress, precondition); err != nil {
		return fmt.Errorf("transition to in_progress: %w", err)
	}

	_, spawnErr := s.SpawnSessionFromItem(ctx, connect.NewRequest(&sessionv1.SpawnSessionFromItemRequest{
		ItemId:     itemID,
		Autonomous: true,
	}))
	if spawnErr != nil {
		// Roll back: item should stay in review rather than stranded in in_progress
		// with no active session. ReconcileStuckItems is an eventual fallback, but
		// an explicit rollback provides faster recovery.
		if _, rollbackErr := s.storage.TransitionBacklogItemStatus(ctx, itemID, session.BacklogStatusReview, nil); rollbackErr != nil {
			log.ErrorLog.Printf("[AutoReopenAfterFailedReview] rollback to review failed for item %s: %v", itemID, rollbackErr)
		}
		return fmt.Errorf("spawn session: %w", spawnErr)
	}
	return nil
}

// AutoReopenForPRFix implements session.PRFixSpawner. It transitions the item
// from pr_pending back to in_progress and spawns a new autonomous work session
// pre-loaded with the CI/review failure context so the agent can fix and push.
func (s *BacklogService) AutoReopenForPRFix(ctx context.Context, itemID string, fixContext string) error {
	if s.storage == nil {
		return fmt.Errorf("storage not available")
	}

	item, err := s.storage.GetBacklogItem(ctx, itemID)
	if err != nil {
		return fmt.Errorf("load item: %w", err)
	}
	if session.BacklogStatus(item.Status) != session.BacklogStatusPRPending {
		return fmt.Errorf("item %s is not pr_pending (got %s)", itemID, item.Status)
	}

	// Reuse the same iteration cap as the review rework cycle.
	sessions, sessErr := s.storage.ListItemSessions(ctx, item.ID)
	if sessErr != nil {
		return fmt.Errorf("list sessions for cap check: %w", sessErr)
	}
	workCount := 0
	for _, is := range sessions {
		if is.SessionRole == session.SessionRoleWork {
			workCount++
		}
	}
	if workCount >= maxAutoReworkIterations {
		log.InfoLog.Printf("[AutoReopenForPRFix] item %s has %d work sessions (cap %d); leaving in pr_pending for manual action", itemID, workCount, maxAutoReworkIterations)
		return nil
	}

	updatedAt := item.UpdatedAt
	precondition := &session.BacklogItemPrecondition{
		ExpectedStatus:    string(session.BacklogStatusPRPending),
		ExpectedUpdatedAt: &updatedAt,
		Note:              "auto-reopened for PR fix (CI/review)",
	}
	if _, err := s.storage.TransitionBacklogItemStatus(ctx, itemID, session.BacklogStatusInProgress, precondition); err != nil {
		return fmt.Errorf("transition to in_progress: %w", err)
	}

	// Prepend the PR failure context to the item's notes so the spawned session
	// prompt includes it. Restore original notes after spawning.
	originalNotes := item.Notes
	prFixNote := fmt.Sprintf("[PR Fix context - PR #%d (%s)]\n%s", item.PrNumber, item.PrURL, fixContext)
	combinedNotes := prFixNote
	if originalNotes != "" {
		combinedNotes = prFixNote + "\n\n---\n\n" + originalNotes
	}
	if _, noteErr := s.storage.UpdateBacklogItem(ctx, itemID, session.BacklogItemUpdate{
		Notes: &combinedNotes,
	}, nil); noteErr != nil {
		log.WarningLog.Printf("[AutoReopenForPRFix] set fix notes item=%s: %v", itemID, noteErr)
	}

	_, spawnErr := s.SpawnSessionFromItem(ctx, connect.NewRequest(&sessionv1.SpawnSessionFromItemRequest{
		ItemId:     itemID,
		Autonomous: true,
	}))

	// Restore original notes regardless of spawn outcome.
	if _, noteErr := s.storage.UpdateBacklogItem(ctx, itemID, session.BacklogItemUpdate{
		Notes: &originalNotes,
	}, nil); noteErr != nil {
		log.WarningLog.Printf("[AutoReopenForPRFix] restore notes item=%s: %v", itemID, noteErr)
	}

	if spawnErr != nil {
		// Roll back to pr_pending so the reconciler can retry.
		if _, rollbackErr := s.storage.TransitionBacklogItemStatus(ctx, itemID, session.BacklogStatusPRPending, nil); rollbackErr != nil {
			log.ErrorLog.Printf("[AutoReopenForPRFix] rollback to pr_pending failed for item %s: %v", itemID, rollbackErr)
		}
		return fmt.Errorf("spawn session: %w", spawnErr)
	}

	log.InfoLog.Printf("[AutoReopenForPRFix] item %s → in_progress for PR fix session", itemID)
	return nil
}

// AttachSessionToItem links an existing session to a backlog item.

func (s *BacklogService) AttachSessionToItem(
	ctx context.Context,
	req *connect.Request[sessionv1.AttachSessionToItemRequest],
) (*connect.Response[sessionv1.AttachSessionToItemResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// 1. Validate inputs.
	if req.Msg.ItemId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("item_id is required"))
	}
	if req.Msg.SessionUuid == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("session_uuid is required"))
	}

	// 2. Load and validate item.
	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	if item.Status != string(session.BacklogStatusIdea) &&
		item.Status != string(session.BacklogStatusReady) &&
		item.Status != string(session.BacklogStatusInProgress) {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("item must be in %q, %q, or %q status to attach a session, got %q",
				session.BacklogStatusIdea, session.BacklogStatusReady, session.BacklogStatusInProgress, item.Status))
	}

	// 3. Snapshot current AC.
	acSnapshot := item.AcceptanceCriteria

	// 4. Load prior sessions BEFORE creating this attach's own ItemSession, so the
	// "prior sessions" list passed to WriteBacklogContextFile never transiently includes
	// the session being attached (mirrors SpawnSessionFromItem's ordering).
	attachPriorSessions, priorErr := s.storage.ListItemSessions(ctx, item.ID)
	if priorErr != nil {
		log.WarningLog.Printf("[AttachSessionToItem] failed to load prior sessions for item %s: %v", item.ID, priorErr)
		attachPriorSessions = nil
	}

	// 5. Create ItemSession.
	is, err := s.storage.CreateItemSession(ctx, session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: req.Msg.SessionUuid,
		SessionRole: session.SessionRoleWork,
		AcSnapshot:  acSnapshot,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create item session: %w", err))
	}

	// 6. Write slash commands to session worktree if instance is reachable.
	attachItemUUID, _ := uuid.Parse(item.ID)
	entItem := &ent.BacklogItem{
		ID:                 attachItemUUID,
		Title:              item.Title,
		Description:        item.Description,
		AcceptanceCriteria: string(item.AcceptanceCriteria),
		Priority:           item.Priority,
		Status:             item.Status,
		Notes:              item.Notes,
		PlanArtifactsPath:  item.PlanArtifactsPath,
		PlanApproved:       item.PlanApproved,
		SkipPlanning:       item.SkipPlanning,
	}

	instances, loadErr := s.storage.LoadInstances()
	if loadErr == nil {
		for _, inst := range instances {
			if inst.UUID == req.Msg.SessionUuid && inst.Path != "" {
				worktreePath := inst.GetEffectiveRootDir()
				// Write synchronously under mutex to prevent concurrent write races.
				s.worktreeMu.Lock()
				if wErr := session.WriteSlashCommands(entItem, worktreePath); wErr != nil {
					s.worktreeMu.Unlock()
					return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("WriteSlashCommands: %w", wErr))
				}
				if wErr := session.WriteBacklogContextFile(entItem, attachPriorSessions, worktreePath); wErr != nil {
					s.worktreeMu.Unlock()
					return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("WriteBacklogContextFile: %w", wErr))
				}
				s.worktreeMu.Unlock()
				// Capture pre-work HEAD SHA so the review gate can diff base..HEAD
				// across all commits the agent makes (same as SpawnSessionFromItem step 12b).
				if baseSHA, shaErr := session.GetGitHeadSHA(worktreePath); shaErr == nil && baseSHA != "" {
					_ = s.storage.UpdateItemSessionGitActivity(ctx, is.ID.String(), baseSHA, "", time.Now(), 0)
					inst.SetDirBaseSHA(baseSHA)
				}
				break
			}
		}
	}

	// 7. Transition item to in_progress (only if the state machine permits it).
	if session.CanTransitionBacklog(session.BacklogStatus(item.Status), session.BacklogStatusInProgress) {
		if _, transErr := s.storage.TransitionBacklogItemStatus(ctx, item.ID, session.BacklogStatusInProgress, nil); transErr != nil {
			log.ErrorLog.Printf("[AttachSessionToItem] failed to transition item to in_progress: %v", transErr)
		}
	}

	return connect.NewResponse(&sessionv1.AttachSessionToItemResponse{
		ItemSession: itemSessionToProto(is, s.buildCostLookup()),
	}), nil
}

// TriggerTriage kicks off a headless triage planning call for a backlog item.
// Returns immediately after creating an ItemSession; actual triage runs in a goroutine.
// +api: backlog:trigger-triage
func (s *BacklogService) TriggerTriage(
	ctx context.Context,
	req *connect.Request[sessionv1.TriggerTriageRequest],
) (*connect.Response[sessionv1.TriggerTriageResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// 1. Load item.
	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	// 2. Status guard — triage is only valid for idea or ready items.
	if item.Status != string(session.BacklogStatusIdea) && item.Status != string(session.BacklogStatusReady) {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("item must be in %q or %q status to trigger triage, got %q",
				session.BacklogStatusIdea, session.BacklogStatusReady, item.Status))
	}

	// 3. Repo path required.
	if item.RepoPath == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("set repo_path before triggering triage"))
	}

	// 3a. Orphan-aware guard: if an open triage session exists, check whether it is
	// genuinely still running. Headless sessions are always orphaned if not ended
	// (no live tmux session to check) — tombstone them and allow re-trigger.
	existingSessions, listErr := s.storage.ListItemSessions(ctx, req.Msg.ItemId)
	if listErr != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list triage sessions: %w", listErr))
	}
	for _, is := range existingSessions {
		if is.SessionRole != string(session.SessionRoleTriage) || is.EndedAt != nil {
			continue
		}
		// Headless triage sessions have no live in-memory instance; treat as orphaned.
		// Sessions older than maxTriageSessionAge are also treated as orphaned to prevent
		// a hung or leaked session from blocking re-trigger indefinitely.
		isHeadless := strings.HasPrefix(is.SessionUUID, headlessTriageUUIDPrefix)
		isStale := time.Since(is.CreatedAt) > maxTriageSessionAge
		notLive := isHeadless || isStale || s.sessionStopper == nil || !s.sessionStopper.IsSessionLive(is.SessionUUID)
		statusAdvanced := item.Status != string(session.BacklogStatusIdea)
		if notLive || statusAdvanced {
			now := time.Now()
			_ = s.storage.UpdateItemSessionEnded(ctx, is.ID.String(), now)
			continue
		}
		return nil, connect.NewError(connect.CodeAlreadyExists,
			fmt.Errorf("triage session already running for item %s", req.Msg.ItemId))
	}

	// 3b. If re-triggering on a "ready" item, move it back to "idea".
	if item.Status == string(session.BacklogStatusReady) {
		if _, transErr := s.storage.TransitionBacklogItemStatus(ctx, req.Msg.ItemId,
			session.BacklogStatusIdea, nil); transErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] failed to reset status to idea: %v", transErr)
		}
	}

	// 3c. Feedback-driven refine: find the most recent completed triage result to
	// revise. Refining requires one to exist — feedback on an item with no completed
	// triage falls back to a confusing fresh run, so reject explicitly instead.
	feedback := strings.TrimSpace(req.Msg.Feedback)
	var priorResult session.HeadlessTriageResult
	var havePrior bool
	for i := len(existingSessions) - 1; i >= 0; i-- {
		is := existingSessions[i]
		if is.SessionRole != string(session.SessionRoleTriage) || is.TriageResult == "" {
			continue
		}
		if jsonErr := json.Unmarshal([]byte(is.TriageResult), &priorResult); jsonErr == nil {
			havePrior = true
			break
		}
	}
	if feedback != "" && !havePrior {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("no completed triage result to refine for item %s — trigger initial triage first", req.Msg.ItemId))
	}
	nextIteration := priorResult.Iteration + 1

	// 4. Build artifact dir path under ~/.stapler-squad/triage-artifacts/<item-id>/
	//    so triage workers don't write into the item's git repo.
	triageBase, triageBaseErr := s.cfg.TriageArtifactDirOrDefault()
	if triageBaseErr != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to resolve triage artifact dir: %w", triageBaseErr))
	}
	artifactAbsPath := filepath.Join(triageBase, item.ID)

	// 5. Create artifact dir.
	if mkErr := os.MkdirAll(artifactAbsPath, 0o755); mkErr != nil {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("failed to create artifact dir %s: %w", artifactAbsPath, mkErr))
	}

	// 6. Require headless pool.
	if s.headlessPool == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("headless pool not available — ensure claude binary is installed"))
	}

	// 7. Build triage prompt — a fresh triage, or a feedback-driven refine of the
	// most recent completed result.
	var triagePrompt string
	if feedback != "" {
		triagePrompt = session.BuildHeadlessRetriagePrompt(item, artifactAbsPath, priorResult, feedback)
	} else {
		triagePrompt = session.BuildHeadlessTriagePrompt(item, artifactAbsPath)
	}

	// 8. Create ItemSession synchronously before goroutine (prevents TOCTOU on orphan guard).
	triageSessionUUID := headlessTriageUUIDPrefix + uuid.New().String()
	is, err := s.storage.CreateItemSession(ctx, session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: triageSessionUUID,
		SessionRole: session.SessionRoleTriage,
		AcSnapshot:  item.AcceptanceCriteria,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create triage item session: %w", err))
	}

	log.InfoLog.Printf("[TriggerTriage] headless triage started item=%s session=%s path=%s", item.ID, triageSessionUUID, artifactAbsPath)

	// 9. Drive triage asynchronously so the RPC returns immediately.
	itemID := item.ID
	itemRepoPath := item.RepoPath
	isID := is.ID.String()
	iteration := nextIteration
	go func() {
		// Acquire concurrency semaphore (max 8 concurrent triage calls).
		select {
		case s.triageSem <- struct{}{}:
		case <-s.shutdownCtx.Done():
			// cleanupCtx is a separate context for DB writes that must complete even
			// after shutdownCtx is cancelled. Passing shutdownCtx here would cause the
			// write to fail immediately with context.Canceled.
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			_ = s.storage.UpdateItemSessionEnded(cleanupCtx, isID, time.Now())
			return
		}
		defer func() { <-s.triageSem }()

		triageCtx, cancel := context.WithTimeout(s.shutdownCtx, 30*time.Minute)
		defer cancel()

		raw, callErr := s.headlessPool.CallBlockingWithOptions(triageCtx,
			headless.FeatureKeyTriage,
			headless.HeadlessTriageSystemPrompt(),
			triagePrompt,
			headless.CallOptions{WorkDir: itemRepoPath},
		)

		// cleanupCtx outlives shutdownCtx so DB writes succeed even during graceful
		// shutdown. Created HERE, after CallBlockingWithOptions returns, not before
		// it: the LLM call above routinely takes 7-15 minutes (4 parallel research
		// subagents), so a cleanupCtx created before it would have its 10s budget
		// already expired by the time these persistence calls run below — every
		// successful triage would silently fail to ever mark the item ready. This
		// was a live, 100%-reproducible bug: see the backlog cross-platform audit.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), s.triageCleanupTimeout)
		defer cleanupCancel()

		if callErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] headless triage failed item=%s: %v", itemID, callErr)
			_ = s.storage.UpdateItemSessionEnded(cleanupCtx, isID, time.Now())
			return
		}

		result, parseErr := session.ParseHeadlessTriageResult(raw)
		if parseErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] parse result failed item=%s: %v", itemID, parseErr)
			_ = s.storage.UpdateItemSessionEnded(cleanupCtx, isID, time.Now())
			return
		}
		result.Iteration = iteration
		result.Feedback = feedback

		payloadJSON, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] marshal triage result item=%s: %v", itemID, marshalErr)
			_ = s.storage.UpdateItemSessionEnded(cleanupCtx, isID, time.Now())
			return
		}
		if updateErr := s.storage.UpdateItemSessionTriageResult(cleanupCtx, isID, string(payloadJSON)); updateErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] persist triage result item=%s: %v", itemID, updateErr)
		}

		pap := artifactAbsPath
		update := session.BacklogItemUpdate{PlanArtifactsPath: &pap}
		if len(result.AcceptanceCriteria) > 0 {
			// Re-index to ensure 0-based contiguous indices regardless of what the model output.
			for i := range result.AcceptanceCriteria {
				result.AcceptanceCriteria[i].Index = i
				if result.AcceptanceCriteria[i].Status == "" {
					result.AcceptanceCriteria[i].Status = "pending"
				}
			}
			if acJSON, marshalErr := session.SerializeAcCriteria(result.AcceptanceCriteria); marshalErr == nil {
				update.AcceptanceCriteria = &acJSON
			}
		}
		if _, updateErr := s.storage.UpdateBacklogItem(cleanupCtx, itemID, update, nil); updateErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] update plan_artifacts_path item=%s: %v", itemID, updateErr)
		}

		precondition := &session.BacklogItemPrecondition{ExpectedStatus: string(session.BacklogStatusIdea)}
		if _, transErr := s.storage.TransitionBacklogItemStatus(cleanupCtx, itemID,
			session.BacklogStatusReady, precondition); transErr != nil {
			log.ErrorLog.Printf("[TriggerTriage] status transition idea→ready item=%s: %v", itemID, transErr)
		}

		_ = s.storage.UpdateItemSessionEnded(cleanupCtx, isID, time.Now())
		log.InfoLog.Printf("[TriggerTriage] headless triage complete item=%s suggestions=%d tasks=%d",
			itemID, len(result.Suggestions), len(result.Tasks))
	}()

	return connect.NewResponse(&sessionv1.TriggerTriageResponse{
		ItemSession: itemSessionToProto(is, s.buildCostLookup()),
	}), nil
}

// CancelTriage stops a running triage session for a backlog item.
// +api: backlog:cancel-triage
func (s *BacklogService) CancelTriage(
	ctx context.Context,
	req *connect.Request[sessionv1.CancelTriageRequest],
) (*connect.Response[sessionv1.CancelTriageResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	existingSessions, err := s.storage.ListItemSessions(ctx, req.Msg.ItemId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list sessions: %w", err))
	}

	cancelled := false
	now := time.Now()
	for _, is := range existingSessions {
		if is.SessionRole != string(session.SessionRoleTriage) || is.EndedAt != nil {
			continue
		}
		if s.sessionStopper != nil {
			_ = s.sessionStopper.StopSessionByUUID(ctx, is.SessionUUID)
		}
		_ = s.storage.UpdateItemSessionEnded(ctx, is.ID.String(), now)
		cancelled = true
	}

	return connect.NewResponse(&sessionv1.CancelTriageResponse{Cancelled: cancelled}), nil
}

// TriggerReReview re-runs the review gate for a backlog item.
// +api: backlog:trigger-re-review
func (s *BacklogService) TriggerReReview(
	ctx context.Context,
	req *connect.Request[sessionv1.TriggerReReviewRequest],
) (*connect.Response[sessionv1.TriggerReReviewResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// 1. Load item.
	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	// 2. Validate item is in review status.
	if item.Status != string(session.BacklogStatusReview) {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("item must be in %q status to re-trigger review, got %q", session.BacklogStatusReview, item.Status))
	}

	// 3. Repo path required.
	if item.RepoPath == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("set repo_path before triggering re-review"))
	}

	// 4. Find the most recent review and work ItemSessions for this item.
	sessions, err := s.storage.ListItemSessions(ctx, item.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list item sessions: %w", err))
	}

	var mostRecentReviewSession *ent.ItemSession
	var mostRecentWorkSession *ent.ItemSession
	for _, is := range sessions {
		switch is.SessionRole {
		case session.SessionRoleReview:
			if mostRecentReviewSession == nil || is.CreatedAt.After(mostRecentReviewSession.CreatedAt) {
				mostRecentReviewSession = is
			}
		case session.SessionRoleWork:
			if mostRecentWorkSession == nil || is.CreatedAt.After(mostRecentWorkSession.CreatedAt) {
				mostRecentWorkSession = is
			}
		}
	}

	// 5. Note: We don't need to delete the old verdict; a new one will overwrite it when the re-review
	// session submits its findings via the MCP tool.

	// 6. Get git diff from the most recent work session's worktree using its base SHA.
	// Fall back to item.RepoPath / HEAD~1 only for directory-mode sessions.
	var workSessionDiff string
	if mostRecentWorkSession != nil {
		diffDir := item.RepoPath
		diffBaseSHA := "HEAD~1"
		wt, wtErr := s.storage.GetWorktreeDataBySessionUUID(ctx, mostRecentWorkSession.SessionUUID)
		if wtErr == nil && wt.WorktreePath != "" {
			diffDir = wt.WorktreePath
			diffBaseSHA = wt.BaseCommitSHA
		} else if mostRecentWorkSession.LastCommitSha != "" {
			diffBaseSHA = mostRecentWorkSession.LastCommitSha
		}
		diff, _, diffErr := session.GetGitDiff(ctx, diffDir, diffBaseSHA)
		if diffErr != nil {
			log.WarningLog.Printf("[TriggerReReview] GetGitDiff failed: %v", diffErr)
		} else {
			workSessionDiff = diff
		}
	}

	// 7. Deserialize AC snapshot (from most recent work session or item AC).
	var acSnapshot []session.AcCriterion
	if mostRecentWorkSession != nil && mostRecentWorkSession.AcSnapshot != "" {
		acSnapshot, _ = session.ParseAcCriteria(session.AcCriteriaJSON(mostRecentWorkSession.AcSnapshot))
	}
	if len(acSnapshot) == 0 {
		acSnapshot, _ = session.ParseAcCriteria(item.AcceptanceCriteria)
	}

	// 8. Build re-review prompt.
	acSnapshotJSON, _ := json.Marshal(acSnapshot)

	priorVerdictSection := ""
	if mostRecentReviewSession != nil && mostRecentReviewSession.Edges.ReviewVerdict != nil {
		rv := mostRecentReviewSession.Edges.ReviewVerdict
		priorVerdictSection = fmt.Sprintf("\n## Prior Review Verdict\nOutcome: %s\nSummary: %s\n", rv.OverallOutcome, rv.Summary)
	}

	reReviewPrompt := fmt.Sprintf(`You are re-reviewing a backlog item that previously entered the review state.

# Item: %s

## Description
%s
%s
## Acceptance Criteria (at time of work session)
`, item.Title, item.Description, priorVerdictSection)

	for _, ac := range acSnapshot {
		reReviewPrompt += fmt.Sprintf("%d. %s (status: %s)\n", ac.Index, ac.Text, ac.Status)
	}

	reReviewPrompt += fmt.Sprintf(`
## Recent Changes
The work session made the following changes to the codebase:

%s

## Your Task
Perform a comprehensive review and submit your verdict using the submit_review_verdict MCP tool:
- Assess each acceptance criterion listed above
- Evaluate the diff against the requirements
- For each criterion provide: criterion_index, outcome (PASS/FAIL/PARTIAL), evidence

Call submit_review_verdict with:
  item_id: "%s"
  summary: "<overall summary of your findings>"
  verdicts: [{"criterion_index": N, "outcome": "PASS|FAIL|PARTIAL", "evidence": "<specific evidence>"}]

Do not modify the code. Only write the review verdict.
`, session.SanitizeDiff(workSessionDiff), item.ID)

	// 9. Require SessionCreator to spawn review session.
	// degraded: sessionCreator unavailable — return a placeholder response so the
	// caller knows re-review was acknowledged, even without a live session spawner.
	if s.sessionCreator == nil {
		// No spawner configured; just return a placeholder indicating re-review was triggered.
		log.InfoLog.Printf("[TriggerReReview] triggered for item %s but no SessionCreator available", item.ID)
		return connect.NewResponse(&sessionv1.TriggerReReviewResponse{
			ItemSession: &sessionv1.ItemSession{
				Id:          item.ID,
				SessionRole: "re-review-triggered",
			},
		}), nil
	}

	// 10. Spawn re-review session — AutonomousDriver mode if available, oneShot fallback.
	slug := slugify(item.Title)
	title := "re-review:" + slug
	useAutonomous := s.autonomousStarter != nil

	// Kill any stale tmux session with this title so the new session gets a fresh
	// pane and the autonomous driver can deliver its prompt without attaching to an
	// old, idle session that was left behind from a previous (possibly crashed) attempt.
	if s.sessionStopper != nil {
		_ = s.sessionStopper.KillTmuxSessionByTitle(ctx, title)
	}

	inst, spawnErr := s.sessionCreator.CreateDirectorySession(ctx, title, item.RepoPath, reReviewPrompt,
		[]string{"backlog:review"}, !useAutonomous /*oneShot*/, true /*hidden*/)
	if spawnErr != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to spawn re-review session: %w", spawnErr))
	}
	if useAutonomous {
		s.autonomousStarter.StartAutonomousDriverWithTimeout(inst, 5*time.Minute)
	}

	// 11. Create ItemSession with role=review.
	is, err := s.storage.CreateItemSession(ctx, session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: inst.UUID,
		SessionRole: session.SessionRoleReview,
		AcSnapshot:  session.AcCriteriaJSON(acSnapshotJSON),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create re-review item session: %w", err))
	}

	// Capture the pre-review HEAD SHA so diffs against base..HEAD work correctly.
	if baseSHA, shaErr := session.GetGitHeadSHA(item.RepoPath); shaErr == nil && baseSHA != "" {
		_ = s.storage.UpdateItemSessionGitActivity(ctx, is.ID.String(), baseSHA, "", time.Now(), 0)
	}

	log.InfoLog.Printf("[TriggerReReview] spawned re-review session %s for item %s", inst.UUID, item.ID)

	return connect.NewResponse(&sessionv1.TriggerReReviewResponse{
		ItemSession: itemSessionToProto(is, s.buildCostLookup()),
	}), nil
}

// TriggerSync initiates a synchronous, on-demand sync run for an external item
// source, regardless of its Enabled flag. Runs inline (not backgrounded like
// TriggerTriage) because a single external-API fetch is expected to complete
// in seconds, not the 7-15 minutes a headless LLM triage call takes.
// +api: backlog:trigger-sync
func (s *BacklogService) TriggerSync(
	ctx context.Context,
	req *connect.Request[sessionv1.TriggerSyncRequest],
) (*connect.Response[sessionv1.TriggerSyncResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}
	if s.pluginRegistry == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("sync not configured — no plugin registry wired"))
	}
	if s.syncFeatureEnabled != nil && !s.syncFeatureEnabled() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("backlog sync is disabled"))
	}
	if req.Msg.SourceId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("source_id is required"))
	}
	if _, parseErr := uuid.Parse(req.Msg.SourceId); parseErr != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid source_id %q: %w", req.Msg.SourceId, parseErr))
	}

	var sl *session.SyncLoop
	if s.syncKeyFunc != nil {
		sl = session.NewSyncLoopWithKeyProvider(s.storage, s.pluginRegistry, s.syncKeyFunc)
	} else {
		sl = session.NewSyncLoop(s.storage, s.pluginRegistry)
	}

	syncCtx, cancel := context.WithTimeout(ctx, defaultTriggerSyncTimeout)
	defer cancel()

	if err := sl.SyncByID(syncCtx, req.Msg.SourceId); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("item source %q not found", req.Msg.SourceId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("sync failed: %w", err))
	}

	return connect.NewResponse(&sessionv1.TriggerSyncResponse{}), nil
}

func (s *BacklogService) ImportGitHubIssue(ctx context.Context, req *connect.Request[sessionv1.ImportGitHubIssueRequest]) (*connect.Response[sessionv1.ImportGitHubIssueResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("ImportGitHubIssue not yet implemented"))
}
