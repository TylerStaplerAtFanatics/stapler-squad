# Triage Pipeline Features Research

## Overview

The Stapler Squad triage pipeline provides a structured, agent-driven pre-implementation planning workflow. The pipeline ingests backlog items, spawns one-shot triage sessions that research the codebase in parallel, synthesizes findings into planning artifacts (plan.md, validation.md, research/*.md), and produces an interactive implementation checklist for operators.

**Recent Fix (commit 19ef4431):** Three critical bugs were repaired May 22, 2026:
1. **Prompt injection bug:** Triage instructions passed via `--append-system-prompt` (system-context-only) instead of positional `Prompt` argument, causing Claude to wait for user message before starting work
2. **Missing EndedAt:** Triage sessions never recorded end time due to early return in `onSessionExited`, leaving UI permanently stuck "running"
3. **OneShot flag ignored:** OneShot=true was set but `buildLaunchCommand` didn't add `-p` flag, leaving triage in interactive mode after completion

---

## 1. Existing Triage Flow

### Status Lifecycle
Items flow through states controlled by `WorkflowEngine` and `BacklogLifecycleListener`:
```
idea → [TriggerTriage] → in_progress (triage session runs)
         → [triage exits] → ready (success) or back to idea (re-trigger)
       → [plan approved] → in_progress (work session)
```

**Key Statuses:**
- `idea`: New item, triage not started
- `ready`: Triage complete, plan approved, awaiting work session
- `in_progress`: Active session (triage or work)
- `review`: Work complete, awaiting review gate
- `done`: Approved by review gate or manual override

### TriggerTriage RPC (`server/services/backlog_service.go:1056`)

**Preconditions:**
- Item status: `idea` or `ready` (can re-trigger from ready to re-plan)
- Repo path required (worktree location)
- Orphan detection: Reuses tmux session name `"triage:" + slug` but kills stale sessions before spawning (lines 1131-1142)

**Process:**
1. Load item and validate status/repo_path
2. Check for orphaned triage sessions (started_at=NULL, not live, or item status advanced)
3. Reset item status to `idea` if re-triggering from `ready`
4. Create artifact directory `docs/tasks/{slug}/` with subdirectories `/research`
5. Build triage prompt via `buildTriagePrompt()` (lines 1188-1255)
6. Spawn one-shot session: `CreateDirectorySession(title="triage:{slug}", prompt=triagePrompt, oneShot=true)`
7. Create `ItemSession` record with `SessionRole=triage` and AC snapshot

### Prompt Construction (`buildTriagePrompt`)

The triage prompt is a structured instruction set guiding agents through 5 phases:

**Phase 1 — Parallel Research (subagents):**
```
{artifactAbsPath}/research/stack.md      (tech stack, versions)
{artifactAbsPath}/research/features.md   (similar features, patterns)
{artifactAbsPath}/research/architecture.md (proposed design)
{artifactAbsPath}/research/pitfalls.md   (risks, gotchas)
```

**Phase 2 — Synthesis:**
```
{artifactAbsPath}/plan.md
  - Executive summary
  - Implementation approach
  - Task breakdown (time estimates)
  - Dependencies & blockers
```

**Phase 3 — Validation:**
```
{artifactAbsPath}/validation.md
  - Test plan (each AC → test)
  - Edge cases
```

**Phase 4 & 5 — Submit & Clarifications:**
- Call `submit_triage_result` MCP tool with: `item_id`, `summary`, `suggestions[]`, `tasks[]`, `plan_artifact_path`
- Optional: Include clarifying questions in `suggestions` with `rationale="question"`
- Max 12 tasks; each with `text`, `estimate` (e.g., "2h", "1d"), `category` (backend|frontend|test|infra|docs)

**Key Design Patterns:**
- Absolute paths for `plan_artifact_path` so `os.Stat()` verification succeeds
- Prompt starts with role statement: "You are a senior software architect"
- No code modification allowed; research/planning only
- Deterministic artifact paths enable re-triggering without orphaning old artifacts

---

## 2. Session Launch & Prompt Mapping

### Instance Fields (session/instance.go)

**Triage sessions use:**
- `Title`: "triage:{slug}" (for tmux session name reuse detection)
- `Prompt`: Initial prompt (positional CLI argument to `claude`)
- `OneShot`: true (triggers `-p` flag → exit after task completion)
- `AutoYes`: true (skip permission prompts; automated context)
- `MCPServerURL`: HTTP endpoint for MCP tools (stapler-squad backlog tools)

**Why NOT `AppendSystemPrompt`:**
The old code passed `appendSystemPrompt` flag (`--append-system-prompt`). This injects into the *system* context only and does NOT trigger Claude to start work — it waits for a user message. Fixed in commit 19ef4431 to use `Prompt` instead (positional argument = user message).

### Launch Command Building (session/instance_tmux.go:24-48)

The `buildLaunchCommand()` method constructs the full tmux command:

```go
program := i.Program  // "claude"

// 1. Resume from saved conversation (if available)
if claudeSessionID != "" {
    program += " --resume " + claudeSessionID
}

// 2. Inject MCP server config
if i.MCPServerURL != "" {
    program += " --mcp-config '{...}'"
}

// 3. [REMOVED: --append-system-prompt] System prompts no longer used
// (was: program += " --append-system-prompt " + AppendSystemPrompt)

// 4. Auto-yes for permissions
if i.AutoYes {
    program += " --dangerously-skip-permissions"
}

// 5. OneShot print mode (exit after task)
if i.OneShot {
    program += " -p"  // FIXED in commit 19ef4431
}

// 6. Initial prompt (user message)
if i.Prompt != "" && claudeSessionID == "" {
    program += " " + quotePrompt(i.Prompt)
}
```

**Key Change (commit 19ef4431):**
- Moved from `AppendSystemPrompt` → `Prompt`
- Added `if i.OneShot { program += " -p" }` (line 41-42)
- `Prompt` passed as positional argument so Claude starts working immediately

---

## 3. MCP Tool Registration & submit_triage_result

### Backlog Tools Registration (server/mcp/tools_backlog.go:540-660)

Five backlog-related tools registered via `registerBacklogTools()`:

1. **get_backlog_item** (line 542-550)
   - Fetch item details + AC checklist
   - Role-aware workflow guidance (triage/work/review)

2. **report_progress** (line 553-575)
   - Role: `work` only
   - Update AC criterion status (pass/fail/in_progress)
   - Auto-maps to storage: "pass" → "done", "fail" → "fail"

3. **request_review** (line 577-590)
   - Role: `work` only
   - Notify reviewer when all ACs done
   - Logs event for external notification system

4. **submit_review_verdict** (line 592-618)
   - Role: `review` only
   - Per-criterion verdicts (PASS/FAIL/PARTIAL/UNVERIFIABLE)
   - Auto-downgrade empty evidence to PARTIAL
   - PASS on all → item transitions to `done`

5. **submit_triage_result** (line 620-659) **[Our focus]**
   - Role: `triage` only
   - Parameters:
     - `item_id` (UUID)
     - `summary` (2-3 sentence executive summary, required)
     - `suggestions` (optional array with `text` + `rationale`)
     - `tasks` (optional array, max 12, each with `text`, `estimate`, `category`)
     - `plan_artifact_path` (optional absolute path to docs/tasks/{slug})
   - Actions:
     - Verify session linked to item with role=triage
     - Serialize suggestions + tasks to JSON
     - Store on ItemSession.triage_result field
     - Update BacklogItem.plan_artifacts_path if provided
     - Publish notification event (if EventBus wired)

### Tool Handler Pattern (backlogHandlers struct)

```go
type backlogHandlers struct {
    storage  *session.Storage           // DB + entity access
    store    session.InstanceStore      // Session discovery
    eventBus *events.EventBus          // Notifications (optional)
}
```

**Session UUID Injection:**
- Context carries session UUID via `WithSessionUUID(ctx, sessionID)`
- Extracted in handlers: `callerSessionUUID(ctx)` → returns session UUID
- Validated against ItemSession to prevent cross-item tool use

**Permission Guard (submitTriageResult:423-433):**
```go
itemSession, linkErr := h.storage.GetItemSessionBySessionAndItem(ctx, callerUUID, itemID)
if linkErr != nil {
    if errors.Is(linkErr, session.ErrNotFound) {
        return errResult(ErrPermissionDenied, "this session is not linked to the specified backlog item", ...)
    }
    // ...
}
if itemSession.SessionRole != "triage" {
    return errResult(ErrPermissionDenied, "session role is %q — only 'triage' role may submit triage results", ...)
}
```

---

## 4. Triage Session Lifecycle & Patterns

### Session Role Routing (BacklogLifecycleListener)

The `onSessionExited()` callback (backlog_lifecycle.go:96-153) handles all session roles:

**All Roles:**
1. Record end time: `UpdateItemSessionEnded(ctx, isID, now)` (line 111)
   - **BUG FIX (commit 19ef4431):** Previously guarded by `if role == work` check, skipping triage/review. Now always sets EndedAt.

**Work Sessions Only (line 115-147):**
```go
if is.SessionRole != SessionRoleWork {
    return  // Early exit; no transition for triage/review
}
```

Then for work sessions:
- Load linked item (eager-loaded)
- Check if item is still `in_progress` (precondition)
- Transition to `review` OR `done` (if `skip_review_gate=true`)
- If transitioning to review: spawn review gate session (if spawner configured)

**Triage Sessions:**
- Just record EndedAt
- No status transition (item stays in state set by TriggerTriage guards)
- Status transitions from `idea` → `ready` happen via external RPC (not automatic)

### Pattern Reuse: Work Session Flow (server/services/backlog_service.go:843-961)

`SpawnSessionFromItem()` mirrors TriggerTriage pattern:

1. Load item + validate status=`ready`
2. Planning gate: if `!skip_planning && !plan_approved` → error
3. Repo path required
4. SessionCreator required
5. Build prompt: `BuildTokenBudgetedPrompt(item, priorSessions)`
   - Appends `plan.md` + `validation.md` paths if available
6. Spawn non-oneshot session: `CreateDirectorySession(..., oneShot=false)`
   - Triage: oneShot=true (exit after planning)
   - Work: oneShot=false (interactive; user decides when done)
7. Create ItemSession with `SessionRole=work`, AC snapshot
8. Write slash commands + context file (sync'd under mutex)
9. Transition item to `in_progress`

**Slash Commands (session/backlog_commands.go):**
- `/mcp-tool`: List available backlog MCP tools
- `/report`: Shorthand for `report_progress`
- `/submit`: Shorthand for `submit_triage_result` or `submit_review_verdict`

---

## 5. Triage Result Persistence & Proto Mapping

### Storage Format (server/services/backlog_service.go:226-248)

Triage result stored as JSON on `ItemSession.triage_result`:

```go
type triageResultJSON struct {
    Summary             string                 `json:"summary"`
    Suggestions         []triageSuggestionJSON `json:"suggestions"`
    ClarifyingQuestions []string               `json:"clarifying_questions,omitempty"`
    Tasks               []triageTaskJSON       `json:"tasks,omitempty"`
}

type triageSuggestionJSON struct {
    Text      string `json:"text"`
    Rationale string `json:"rationale"`  // "question" for clarifications
}

type triageTaskJSON struct {
    Text     string `json:"text"`
    Estimate string `json:"estimate"`
    Category string `json:"category"`
}
```

### Proto Deserialization (itemSessionToProto:201-222)

When loaded for RPC responses:
```go
if is.TriageResult != "" {
    var tr triageResultJSON
    json.Unmarshal([]byte(is.TriageResult), &tr)
    // → Populate proto TriageResult with deserialized structs
}
```

Proto representation (gen/proto/go/session/v1/sessionv1connect/...):
```proto
message TriageResult {
    string summary = 1;
    repeated TriageSuggestion suggestions = 2;
    repeated TriageTask tasks = 3;
    repeated string clarifying_questions = 4;
}
```

---

## 6. Existing Tests & Validation

### Test Coverage (server/services/backlog_service_test.go)

**TriggerTriage Tests:**
- `TestTriggerTriage_StatusGuard`: Validates precondition (idea/ready status)
- `TestTriggerTriage_RepoPathRequired`: Repo path must be set
- `TestTriggerTriage_DoubleTriggerGuard`: Orphan detection + live session blocking
  - **Fixed in commit 19ef4431:** Now wires mock stopper with `liveUUIDs` map and marks session as started
- `TestCreateBacklogItem_SkipsTriageWhenRepoPathEmpty`: Auto-triage skipped if no repo

**Integration Tests (session/backlog_integration_test.go):**
- `TestBacklogIntegration_IT006_ReviewSessionExitDoesNotTransition`: Verifies EndedAt is set for review sessions
  - **Fixed in commit 19ef4431:** Test comment updated; EndedAt now always recorded

### Mock Infrastructure

**mockSessionCreator (line 17-50):**
- Records all `CreateDirectorySession` calls for assertion
- Tracks: title, path, prompt, tags, oneShot flag
- Optional error injection

**mockSessionStopper (line 22-35):**
- Maps UUID → live status
- Implements `IsSessionLive()`, `StopSessionByUUID()`, `KillTmuxSessionByTitle()`

---

## 7. Known Architectural Decisions

### Why One-Shot Sessions for Triage?

- Triage is deterministic: research + synthesize + submit → done
- OneShot (`-p` flag) exits Claude after initial prompt completes
- Prevents accidental interactive continuation or hung sessions
- Clear signal to operators: session exit = triage finished

### Why Session UUID in Context?

- MCP tools need to verify caller identity without trusting HTTP headers
- `STAPLER_SESSION_UUID` env var set by tmux (line 76 in instance_tmux.go)
- Context carries it through mcp-go handler chain
- Enables permission guards: "this session is not linked to item X"

### Why Artifact Paths on Backlog Item?

- Allows operators to review planning artifacts in UI
- Enables work session to fetch plan.md/validation.md
- Survives session restarts; detached from session lifecycle
- Absolute paths enable `os.Stat()` verification in ApprovePlan

### Why Orphan Detection on Re-trigger?

- Sessions may crash, server restart, or become stale
- Tmux session name is deterministic (same across re-triggers)
- Without killing stale tmux: new `claude` process reattaches to old session
- Old session doesn't receive new prompt (only `--append-system-prompt` injected at spawn time)
- Solution: Kill by title before spawning ensures clean slate

---

## 8. Error Handling & Degradation

### Graceful Degradation When Missing Dependencies

**If SessionCreator is nil:**
```go
if s.sessionCreator == nil {
    return nil, connect.NewError(connect.CodeUnimplemented, ...)
}
```
- TriggerTriage returns `CodeUnimplemented`
- Callers can detect gap and retry/notify

**If SessionStopper is nil:**
```go
if s.sessionStopper != nil {
    _ = s.sessionStopper.StopSessionByUUID(ctx, is.SessionUUID)
}
```
- Optional; gracefully skipped if absent
- Orphan detection still works via `neverStarted` flag

**If EventBus is nil:**
```go
if h.eventBus != nil {
    h.eventBus.Publish(notificationEvent)
}
```
- Notifications fire-and-forget; missing bus doesn't block operation

### Permission Errors

All role-gated tools return `ErrPermissionDenied`:
- Session not linked to item
- Session has wrong role (e.g., submit_triage_result on work session)
- Item not found or permission check failed

---

## 9. Backlog Item Lifecycle State Machine

```
idea ──────────→ ready ──────────→ in_progress ──────────→ review ──────────→ done
 ↑               ↓                  ↓                        ↓                  
 │          TriggerTriage      SpawnSession         [review gate]          TriggerReReview
 │               │                  │                  [if fail]           (manual)
 │               │                  │ ←────────────────────┘
 └───────────────┴──────────────────┘
    
    [ready→idea on re-trigger]
    [skip_review_gate skips review→done]
    [archived: soft-delete timestamp]
```

**Transitions Guarded By:**
- `WorkflowEngine.CanTransition()`: Valid path exists
- `WorkflowEngine.ValidateGates()`: Business rules (AC required, plan approved, verdict required, etc.)
- `BacklogItemPrecondition`: Optimistic concurrency (ExpectedStatus + ExpectedUpdatedAt)

---

## Summary

The triage pipeline is a well-structured, agent-driven planning system that:

1. **Ingests** backlog items with AC and optional descriptions
2. **Spawns** one-shot triage sessions with parallelized research prompts
3. **Collects** findings via MCP tools (submit_triage_result)
4. **Stores** plans in versioned artifact directories
5. **Presents** interactive checklists to operators for approval
6. **Feeds** approved plans into work sessions

Key patterns reused across triage/work/review:
- Session role identity (SessionRole field)
- ItemSession linking to backlog items
- MCP tool permission guards (session UUID + role)
- Lifecycle callbacks (onSessionExited, onSessionStarted)
- Deterministic session names for reuse detection
- AC snapshots for versioning

Recent fixes (commit 19ef4431) corrected critical prompt injection, session end-time tracking, and oneshot flag handling to restore full pipeline functionality.
