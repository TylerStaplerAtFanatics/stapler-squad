# Triage Pipeline Features and Patterns Research

## Existing Triage Pipeline

### Overview
The stapler-squad triage pipeline is an automated multi-stage planning system that prepares backlog items for implementation.

Key Components:
1. TriggerTriage RPC (server/services/backlog_service.go, lines 1056-1184)
   - Entry point for triage process
   - Creates a one-shot triage session
   - Spans a dedicated directory-type session with role "backlog:triage"

2. Session Creation Flow
   - Function: BacklogService.TriggerTriage()
   - Validates item status (must be "idea" or "ready")
   - Requires repo path
   - Creates artifact directory at repo_path/docs/tasks/slug/
   - Spawns session: CreateDirectorySession(ctx, title, item.RepoPath, triagePrompt, tags, true)

3. Key Design Patterns
   - One-shot mode: oneShot=true ensures session exits after work completes
   - Deterministic session naming: "triage:" + slug prevents duplicates
   - Orphan detection guard (lines 1088-1114):
     * Checks if triage session already running via IsSessionLive()
     * Tombstones orphaned sessions
     * Only allows re-trigger on genuinely orphaned sessions
   - Status reset on re-trigger (lines 1118-1124):
     * Moves item back to "idea" status for UI clarity
   - Stale tmux cleanup (lines 1131-1142):
     * Kills existing tmux session by name before spawning
     * Critical for prompt injection: prevents reattach

### Item Session Tracking
- Created via: storage.CreateItemSession() with role="triage"
- Fields:
  * SessionUUID: Link to running Instance
  * SessionRole: Always "triage"
  * AcSnapshot: Current acceptance criteria at triage time
  * StartedAt: Set by BacklogLifecycleListener when session starts
  * EndedAt: Set by BacklogLifecycleListener when session exits
  * TriageResult: JSON-serialized findings (populated by MCP tool)

---

## Prompt Injection Pattern

### The Bug and The Fix
Historical Issue (commit 19ef4431):
- Triage prompt mapped to AppendSystemPrompt field
- Became --append-system-prompt CLI flag
- Claude waits for user input to start working
- Triage sessions stalled indefinitely

Solution (commit 19ef4431):
- Changed parameter from appendSystemPrompt to prompt in CreateDirectorySession interface
- Maps to positional prompt argument (not system context)
- Claude immediately sees the task and begins work

### Prompt Construction
File: server/services/backlog_service.go, lines 1186-1259 (buildTriagePrompt())

Structure:
1. Role statement: "You are a senior software architect..."
2. Item metadata: Title, item_id (critical for MCP calls)
3. Item description and acceptance criteria
4. Detailed task breakdown (5 steps):
   - Step 1: Research (write 4 files in parallel)
   - Step 2: Synthesis (plan.md)
   - Step 3: Validation (validation.md)
   - Step 4: Submit (call submit_triage_result MCP tool)
   - Step 5: Optional clarifying questions

Key Elements:
- item_id embedded in plain text for agent reference
- plan_artifact_path as absolute path
- Research file paths fully specified
- MCP tool call signature provided

### Tmux Launch Command Assembly
File: session/instance_tmux.go, lines 26-52 (buildLaunchCommand())

Key Fix (from commit 19ef4431):
Lines 46-48:
  if i.OneShot && strings.Contains(program, "claude") {
      program = program + " -p"
  }

This ensures -p (print/non-interactive) flag is added for one-shot sessions.

### MCP Server Context Injection
File: server/mcp/tools_backlog.go, lines 20-42

Mechanism:
- WithSessionUUID injects session UUID into context
- callerSessionUUID extracts it during MCP tool calls

Setup (session/instance_tmux.go, line 81):
  if i.UUID != "" {
      session.SetExtraEnv([]string{"STAPLER_SESSION_UUID=" + i.UUID})
  }

Claude CLI passes this to MCP tool handler, enabling server to validate session legitimacy.

---

## Session Exit Handling

### Lifecycle Events
File: session/backlog_lifecycle.go, lines 96-153

Event Flow:
1. Session process exits (detected via tmux polling)
2. Instance emits EventExited lifecycle event
3. BacklogLifecycleListener receives event via callback
4. Handler dispatches to goroutine (non-blocking)

### EndedAt Recording (Critical Fix)
Bug (pre-commit 19ef4431):
- onSessionExited had early return for non-work roles before UpdateItemSessionEnded
- Triage sessions never got EndedAt set
- UI stuck in "running" state indefinitely

Fix (commit 19ef4431, session/backlog_lifecycle.go lines 109-118):
```
Record end time for ALL session roles
now := time.Now()
if err := l.storage.UpdateItemSessionEnded(ctx, is.ID.String(), now); err != nil {
    log.ErrorLog.Printf("[BacklogLifecycle] UpdateItemSessionEnded error")
}

Only drive transitions for work sessions
if is.SessionRole != SessionRoleWork {
    return
}
```

Impact:
- Triage sessions now record completion time
- UI receives completion signal
- Review panel can display results

### Status Transitions
Triage Session Exit (no transition):
- Sets EndedAt only
- Does not transition item status
- Reason: Triage is planning; item stays in "idea" until reviewed

Work Session Exit (full transition):
- Sets EndedAt
- Transitions item from "in_progress" to "review" or "done"
- Potentially spawns review gate session

### Orphan Detection and Cleanup
File: server/services/backlog_service.go, lines 1088-1142

Guards:
1. Session alive check (lines 1102-1104):
   - is.StartedAt == nil means orphaned (never confirmed running)
   - !sessionStopper.IsSessionLive(uuid) means process exited
   - item.Status != "idea" means item advanced

2. Tombstone stale sessions (lines 1106-1110):
   - Call UpdateItemSessionEnded() to set EndedAt
   - Call StopSessionByUUID() to clean up lingering processes

3. Block genuinely live sessions (line 1112):
   - Return CodeAlreadyExists if session confirmed live

---

## Test Patterns to Reuse

### Backlog Lifecycle Tests
File: session/backlog_lifecycle_test.go

Pattern 1: Session Start Recording
- Create item and ItemSession
- Call listener.onSessionStarted(sessionUUID)
- Verify StartedAt was set

Pattern 2: Session Exit Recording (All Roles)
- Create review ItemSession
- Call listener.onSessionExited(sessionUUID)
- VERIFY: EndedAt IS set (not nil)
- VERIFY: Item status did NOT transition

### Backlog Service Tests
File: server/services/backlog_service_test.go

Pattern 3: Double Trigger Guard
- Wire mock sessionStopper reporting sessions as live
- Create item with repo path
- Manually insert unended triage ItemSession
- Call TriggerTriage again
- VERIFY: Returns CodeAlreadyExists

Key Mock Setup:
- mockSessionStopper.liveUUIDs tracks which sessions are "live"
- IsSessionLive() returns true for configured UUIDs
- StopSessionByUUID() and KillTmuxSessionByTitle() are no-ops

### Submit Triage Result MCP Tests
File: server/mcp/tools_backlog.go, lines 402-536 (submitTriageResult())

Call Sequence:
1. Extract caller UUID from context
2. Validate item_id and summary
3. Verify session linked to item with role="triage"
4. Parse suggestions and tasks
5. Serialize to canonical JSON
6. Update ItemSession.TriageResult
7. Publish notification event

Error Handling:
- Missing item_id or summary: ErrInvalidArgument
- Caller UUID absent: ErrPermissionDenied
- Wrong session role: ErrPermissionDenied
- Session not linked to item: ErrPermissionDenied

### E2E Test Pattern
File: tests/e2e/triage-pipeline-validation.spec.ts

Stages:
1. Create item via UI (title, description, repo path)
2. Verify item in backlog table
3. Open item detail
4. Trigger triage
5. Confirm loading indicator (session received prompt)
6. Wait for review panel (session completed)
7. Verify summary text present

Key Assertions:
- Trigger button disappears when triage starts
- Review panel appears within 5 minutes
- Summary text is non-empty and > 10 characters
- Timeout: 360 seconds (6 minutes for real Claude triage)

---

## Integration Points

### Controller to Backlog Lifecycle
- BacklogLifecycleListener registers via WireToInstance()
- Listens to EventStarted and EventExited
- Drives item status transitions asynchronously

### MCP Tool to Storage
- submitTriageResult() validates session role
- Persists triage JSON to ItemSession.TriageResult
- Publishes notification event if EventBus available
- Tasks capped at 12 for UI scannability

### Session Creation to Prompt Injection
- Interface change: appendSystemPrompt to prompt
- Session/instance_tmux.go builds launch command
- OneShot flag ensures -p flag is added
- Prompt becomes positional CLI argument
- Claude starts immediately
