# Triage Pipeline Architecture

## Overview
The triage pipeline in stapler-squad is an end-to-end workflow that allows Claude to perform pre-implementation analysis on backlog items. The fix in commit 19ef4431 resolved three critical bugs that prevented the pipeline from working.

## Component Boundaries

### 1. BacklogService (server/services/backlog_service.go)
Role: Orchestrates triage session creation and lifecycle management.
- Validates item status and preconditions
- Checks repo_path requirement
- Guards against orphaned/duplicate triage sessions
- Builds triage prompts with instructions and acceptance criteria
- Spawns one-shot sessions via SessionCreator

### 2. SessionService (server/services/session_service.go)
Role: Creates and manages all session types (directory, new worktree, existing worktree).
Key method: CreateDirectorySession(ctx, title, path, prompt, tags, oneShot)
Parameters:
- title: Session name (e.g. triage:feature-xyz)
- path: Repository root directory
- prompt: Initial user message (positional CLI argument, NOT system prompt)
- tags: Organizational labels
- oneShot: When true, exits after task completes (-p flag)

### 3. Session/Instance (session/instance.go, session/instance_tmux.go)
Role: Represents a running Claude session with a tmux backend.
Lifecycle: NewInstance -> instance.Start -> buildLaunchCommand

### 4. Backlog Lifecycle Listener (session/backlog_lifecycle.go)
Role: Drives backlog state transitions in response to session events.
EventStarted: Records ItemSession.StartedAt
EventExited: Records ItemSession.EndedAt, then transitions item status

### 5. MCP Tools (server/mcp/tools_backlog.go)
Role: Provides Claude with backlog-related functions.
Key tool: submit_triage_result
- Validates caller is linked to item with role=triage
- Persists on ItemSession.TriageResult as JSON

## The Three Bugs Fixed (Commit 19ef4431)

### Bug 1: Prompt Injection Failure
Problem: Instructions passed via AppendSystemPrompt flag. Injects to system context only. Claude waits for user message but has none.
Fix: Use Prompt field as positional CLI argument. Claude receives as first user message and starts immediately.
Code: session/instance_tmux.go lines 49-51

### Bug 2: EndedAt Never Set for Triage
Problem: onSessionExited had early guard (if role != work return) BEFORE UpdateItemSessionEnded. Triage sessions skipped EndedAt. UI showed running forever.
Fix: Moved UpdateItemSessionEnded BEFORE role guard. ALL roles record EndedAt. Only work sessions drive transitions.
Code: session/backlog_lifecycle.go lines 109-118

### Bug 3: OneShot Flag Ignored
Problem: OneShot=true set but buildLaunchCommand never added -p flag. Claude stayed interactive. Session never exited.
Fix: Added -p flag when OneShot is true.
Code: session/instance_tmux.go lines 46-48

## Data Flow: End-to-End

Phase 1 - Initiation:
Operator calls TriggerTriage RPC -> BacklogService validates item status (must be idea or ready) and repo_path -> Orphan guard checks existing triage sessions (if live, return error; if stale, mark ended) -> Create artifact directory -> Kill stale tmux session -> Call buildTriagePrompt

Phase 2 - Session Creation:
BacklogService calls CreateDirectorySession with prompt=triagePrompt, oneShot=true -> SessionService creates Instance with Prompt field -> buildLaunchCommand constructs: claude --mcp-config ... -p triagePrompt -> tmux spawns this command -> Claude receives prompt as first user message -> BacklogService creates ItemSession with role=triage

Phase 3 - Task Execution:
Claude calls get_backlog_item MCP tool -> Performs research and planning -> Writes artifact files to /docs/tasks/<slug>/ -> Calls submit_triage_result MCP tool -> MCP tool persists triage result on ItemSession.TriageResult

Phase 4 - Session Exit:
Claude completes task in -p (print) mode -> Claude exits due to -p flag -> tmux detects process exit -> Instance lifecycle listeners fire EventExited -> BacklogLifecycleListener.onSessionExited loads ItemSession and records EndedAt for ALL roles (triage, review, work) -> Guard check: if role != work return (no status transition) -> UI polling shows session as completed

## How Fix Addresses Pipeline

Before:
- AppendSystemPrompt (wrong field) leads to no user input
- No -p flag (OneShot ignored) so Claude stays interactive
- EndedAt never set (early return before UpdateItemSessionEnded)
- Result: UI stuck showing session as running

After:
- Prompt field (positional arg) provides user message
- -p flag honored so Claude exits after task
- EndedAt recorded for all roles (moved before role guard)
- Result: UI shows session as completed

## Session Lifecycle: Triage Creation to Exit

Idea status -> TriggerTriage RPC -> Session created (type=Directory, Prompt=triagePrompt, OneShot=true, MCPServerURL=set) -> instance.Start() -> ItemSession created (role=triage, StartedAt=null, EndedAt=null) -> tmux launches claude with prompt -> Claude receives prompt as first user message -> Claude executes triage workflow -> submit_triage_result called -> Claude completes and exits (due to -p flag) -> EventExited fires -> UpdateItemSessionEnded called -> ItemSession.EndedAt set -> UI shows completed

## MCP Tool: submit_triage_result

Access Control:
- Caller must provide STAPLER_SESSION_UUID environment variable
- MCP server looks up ItemSession by sessionUUID
- Verifies link to requested itemID
- Enforces role=triage

Payload:
- item_id: UUID of backlog item
- summary: 2-3 sentence executive summary
- plan_artifact_path: absolute path to docs/tasks/<slug>
- suggestions: array of {text, rationale}
- tasks: array of {text, estimate, category}

Storage and Notification:
- Persists payload JSON on ItemSession.TriageResult
- Updates BacklogItem.PlanArtifactsPath if provided
- Publishes NotificationEvent to EventBus
- UI receives triage-complete notification

## Backlog Item State Flow

Idea -> TriggerTriage -> Triage session (oneshot, -p flag, prompt) -> Claude submit_triage_result -> ItemSession.TriageResult saved -> Manual approval (operator reviews in UI) -> Ready -> TriggerSpawnWorkSession -> Work session (long-lived, interactive) -> Implements acceptance criteria -> Session exits on completion -> Transition in_progress to review -> Review session (oneshot, reviews diff) -> Claude submit_review_verdict -> Review verdict saved -> Manual approval/override -> Done

Key Invariant: Only work sessions (role=work) drive item status transitions. Triage and review sessions record timing only. This prevents cascading transitions from nested sessions.

## Testing Validation

The fix includes test updates that verify:

1. EndedAt is recorded for all roles: TestBacklogIntegration_IT006_ReviewSessionExitDoesNotTransition now expects EndedAt != null
2. Orphan guard works: TestTriggerTriage_DoubleTriggerGuard wires mock stopper that tracks live sessions and marks sessions as started
3. Session can be re-triggered: After orphaning stale session, re-trigger succeeds

## Summary

The triage pipeline is a three-phase workflow:

1. Creation: BacklogService spawns a one-shot directory session with triage instructions as the prompt
2. Execution: Claude receives prompt as first message, accesses MCP tools, submits triage results
3. Transition: Session exits due to -p flag, EventExited fires, ItemSession gets EndedAt, UI shows completion

The fix enables this by:
- Using Prompt field (positional arg) instead of AppendSystemPrompt (system context)
- Adding -p flag when OneShot=true so Claude exits after task
- Recording UpdateItemSessionEnded before checking role, so all sessions track completion time

This creates a clean feedback loop where Claude can perform focused triage work and the backlog state machine advances to ready awaiting operator review.
