# Triage Pipeline Features Research

**Date**: 2026-05-26  
**Focus**: End-to-end triage pipeline patterns, oneshot sessions, submit_triage_result MCP tool, backlog lifecycle, and session exit behavior.

---

## Existing Triage Features

### 1. End-to-End Triage Pipeline

The triage pipeline follows a complete workflow from item creation through result submission:

#### **Step 1: Triage Session Spawning**
- **Trigger**: `TriggerTriage()` RPC in `backlog_service.go:1056–1184`
- **Session Type**: One-shot directory session with `oneShot=true`
- **Mechanism**: 
  - Creates artifact directory at `{repo_path}/docs/tasks/{slug}/` for research outputs
  - Spawns session via `sessionCreator.CreateDirectorySession()` with `oneShot=true` flag (line 1162)
  - Injects system prompt via `buildTriagePrompt()` (lines 1187–1259)
  - Creates `ItemSession` record with `session_role="triage"` (line 1169)

#### **Step 2: Prompt Construction**
The triage prompt (built in `buildTriagePrompt()`, lines 1187–1259) provides:
- Item ID, title, description, acceptance criteria
- Structured task breakdown:
  - **Step 1**: Research phase (4 parallel subagents: stack.md, features.md, architecture.md, pitfalls.md)
  - **Step 2**: Synthesis (write plan.md with executive summary, implementation approach, task breakdown)
  - **Step 3**: Validation (write validation.md with test plan mapping each AC to specific test)
  - **Step 4**: Submit results via `submit_triage_result` MCP tool
  - **Step 5**: Clarifying questions (optional, with rationale="question" marker)

#### **Step 3: Result Submission**
The MCP tool `submit_triage_result()` in `tools_backlog.go:402–536` captures triage completion:
- **Caller context**: Must be called from a session with `STAPLER_SESSION_UUID` environment variable set (line 404)
- **Role enforcement**: Session must have `session_role="triage"` (line 431)
- **Payload structure**:
  ```json
  {
    "summary": "...",
    "suggestions": [{ "text": "...", "rationale": "..." }],
    "tasks": [{ "text": "...", "estimate": "2h", "category": "backend" }],
    "clarifying_questions": []  // optional
  }
  ```
- **Storage**: Persisted as JSON string in `ItemSession.triage_result` field (line 507)
- **Plan artifacts**: Optional `plan_artifact_path` parameter updates `BacklogItem.plan_artifacts_path` (lines 496–503)
- **Notifications**: Publishes event via `eventBus` if wired (lines 512–530), notifying operator that triage is complete

#### **Step 4: Backlog Item State Transitions**
Triage triggers automatic status transitions via the state machine in `backlog.go`:
- **Valid pre-triage states**: `idea` or `ready` (line 1076)
- **Re-triage guard**: When triggered on a `ready` item, item is reset to `idea` (lines 1118–1123)
- **Orphan detection**: Open triage sessions are tombstoned if:
  - Session never started (`started_at == NULL`) (line 1101)
  - Session is not live in memory (line 1102)
  - Item status has advanced past `idea` (line 1103)

---

### 2. OneShot Flag: Triage-Specific Behavior

The `oneShot` flag controls session lifecycle and prompt injection:

#### **Session Creation**
- **Type**: Boolean flag passed to `CreateDirectorySession()` interface (line 26 in `backlog_service.go`)
- **Current implementations**:
  - Triage: `oneShot=true` (line 1162)
  - Work sessions: `oneShot=false` (line 922)
  - Re-review: `oneShot=true` (line 1517)

#### **Session Behavior (from `session_service.go:529–542`)**
- Passed through to session instantiation as `OneShot=oneShot` (line 540)
- Likely controls:
  - Prompt injection strategy (whether to append system prompt on start vs. replace)
  - Session exit behavior (cleanup, worktree preservation)
  - Lifecycle listeners behavior

#### **Key Pattern**: Triage and review sessions are both one-shot (short-lived, high-signal), while work sessions persist across multiple interactions.

---

### 3. submit_triage_result MCP Tool Flow

#### **Tool Implementation** (`tools_backlog.go:621–659`)
- **Description**: "Record completed triage analysis for a backlog item. Role: triage only."
- **Preconditions**:
  - Must be called from within a triage session (verified via `callerSessionUUID` context injection, line 403)
  - Session must have `session_role="triage"` (line 431)
- **Parameters**:
  - `item_id` (UUID, required): Backlog item being triaged
  - `summary` (string, required): 2-3 sentence executive summary
  - `suggestions` (array, optional): Proposed AC improvements or clarifying questions
  - `tasks` (array, optional): Implementation task breakdown (max 12, each with text, estimate, category)
  - `plan_artifact_path` (string, optional): Absolute path to `docs/tasks/{slug}/` directory

#### **Tool Handler Logic**
1. **Caller context extraction** (line 403): Verifies STAPLER_SESSION_UUID is set
2. **Argument validation** (lines 410–473):
   - Validates UUID format for item_id
   - Parses suggestions array (triageSuggestion struct with text + rationale)
   - Parses tasks array (triageTask struct with text, estimate, category), caps at 12
   - Auto-downgrades to PARTIAL if evidence is empty (line 343)
3. **Session-item link verification** (line 424): Ensures session is linked to item via ItemSession record
4. **Role enforcement** (line 431): Confirms session_role == "triage" (blocks work/review sessions)
5. **Serialization** (lines 475–489): Marshals suggestions + tasks to canonical JSON struct
6. **Persistence** (lines 492–509):
   - Updates `ItemSession.triage_result` with JSON payload
   - Updates `BacklogItem.plan_artifacts_path` if provided (with optimistic concurrency guard via precondition)
7. **Notification** (lines 512–530): Publishes INPUT_REQUIRED notification with item title + suggestion count

#### **Response**: Returns confirmation message with item ID and suggestion count

---

### 4. Backlog Item Lifecycle: Status State Machine

#### **Valid States**
- `idea`: Initial state, accepts AC and description
- `refining`: Intermediate state for AC work (optional)
- `ready`: Ready for work session (requires AC + approved plan)
- `in_progress`: Work session is active
- `review`: Review gate is running/pending
- `done`: Completed and verified
- `archived`: Soft-deleted

#### **Triage Impact on Lifecycle**
- **Pre-triage**: Item is in `idea` or `ready`
- **Auto-trigger on create**: If item has `repo_path` and `skip_triage != true`, triage spawns immediately in background (async)
- **After triage completes**: Item remains in `idea` (awaiting operator approval) or transitions to `ready` via operator action (apply suggestions)
- **Re-triage on ready item**: Item is reset to `idea` before new triage session spawns (line 1119)

#### **Status Transitions Controlled By**
- **Manual**: `TransitionBacklogItemStatus` RPC (with state machine validation via `engine.ValidateGates()`)
- **Automatic**:
  - Work session exit → `in_progress` → `review` (via `BacklogLifecycleListener.onSessionExited()`)
  - Review verdict PASS → `review` → `done`

---

### 5. Sessions Exit After Completing Triage Work

#### **Session Lifecycle Events** (from `backlog_lifecycle.go:59–153`)

**Event 1: SessionStarted**
- Fired when triage session starts
- Handler: `onSessionStarted()` (lines 81–94)
- Action: Records `ItemSession.started_at = now()`
- Applies to all session roles (triage, work, review)

**Event 2: SessionExited**
- Fired when triage session process exits
- Handler: `onSessionExited()` (lines 97–153)
- Action: Records `ItemSession.ended_at = now()`

#### **Triage Session Exit Behavior**
1. **One-shot nature**: Triage sessions are spawned as one-shot (`oneShot=true`), meaning they're designed to run once, exit cleanly, and not persist
2. **For triage role** (lines 116–118): Exit handler only drives state transitions for `SessionRoleWork`; triage sessions exit without further action
3. **ItemSession closure**: `ended_at` is set on all roles (line 111), marking the session as complete
4. **No automatic next-step**: Triage completion is signaled via `submit_triage_result` MCP call + notification, not by session exit
5. **Work sessions differ**: Work sessions automatically trigger `in_progress` → `review` transition on exit (lines 132–152)

#### **Session Worktree Cleanup** (implicit from oneShot behavior)
- One-shot sessions likely clean up their worktree on exit
- Plan artifacts are explicitly saved to `{repo_path}/docs/tasks/{slug}/` by the session before exit
- ItemSession record persists for audit trail

---

### 6. Triage Test Files and Patterns

#### **E2E Test: `tests/e2e/triage-pipeline-validation.spec.ts`**
- **Scope**: Full pipeline validation (create → trigger → wait for results)
- **Key steps**:
  1. Create item with repo path (auto-triggers triage if `skipTriage != true`)
  2. Open item detail pane
  3. Confirm loading indicator appears (session started, prompt injected)
  4. Wait for triage-review-panel to appear (session exited, results submitted)
  5. Verify summary text is populated
- **Timeout**: 6 minutes (real Claude triage time, line 20)
- **Feature gate**: `TRIAGE_VALIDATION=true` environment variable

#### **Unit Tests: `server/services/backlog_service_test.go`**
- Mock implementation: `mockSessionCreator.CreateDirectorySession()` (line 45)
- Captures calls including `oneShot` flag (line 42)
- Allows test scenarios to verify:
  - Triage spawning with correct flags
  - Error handling on session creation failure
  - ItemSession record creation

#### **Proto Tests**: Generated bindings validate proto message shape

---

## Patterns to Reuse

### 1. Session Spawning Pattern
**Reuse in**: Any new workflow that needs a one-shot session

**Pattern** (from `TriggerTriage`, lines 1160–1166):
```go
inst, err := s.sessionCreator.CreateDirectorySession(
    ctx,
    "triage:" + slug,           // title (used as tmux session name)
    item.RepoPath,              // working directory
    triagePrompt,               // initial system prompt
    []string{"backlog:triage"}, // tags for categorization
    true,                       // oneShot: true for short-lived sessions
)
```

### 2. ItemSession Link Pattern
**Reuse in**: Any feature that needs to track AI sessions linked to backlog items

**Pattern** (from `TriggerTriage`, lines 1169–1174):
```go
is, err := s.storage.CreateItemSession(ctx, session.ItemSessionData{
    ItemID:      item.ID,
    SessionUUID: inst.UUID,
    SessionRole: session.SessionRoleTriage,
    AcSnapshot:  item.AcceptanceCriteria,
})
```

### 3. MCP Tool Role Enforcement Pattern
**Reuse in**: Any MCP tool that should only be called by specific session roles

**Pattern** (from `submitTriageResult`, lines 402–433):
```go
callerUUID, err := callerSessionUUID(ctx)  // Get session UUID from context
// ...
itemSession, linkErr := h.storage.GetItemSessionBySessionAndItem(ctx, callerUUID, itemID)
if itemSession.SessionRole != "triage" {
    return errResult(ErrPermissionDenied, fmt.Sprintf("session role is %q — only 'triage' role may...", itemSession.SessionRole), "")
}
```

### 4. Result Serialization & Persistence Pattern
**Reuse in**: Any feature that needs to store structured agent output as JSON

**Pattern** (from `submitTriageResult`, lines 476–509):
```go
// Define canonical struct
type triageResultPayload struct {
    Summary     string            `json:"summary"`
    Suggestions []triageSuggestion `json:"suggestions"`
    Tasks       []triageTask       `json:"tasks,omitempty"`
}

// Marshal to JSON
payloadJSON, err := json.Marshal(triagePayload)

// Persist as string field on linked record
updateErr := h.storage.UpdateItemSessionTriageResult(ctx, itemSession.ID.String(), string(payloadJSON))
```

### 5. Notification Publication Pattern
**Reuse in**: Any feature that needs to notify operators of completion events

**Pattern** (from `submitTriageResult`, lines 512–530):
```go
if h.eventBus != nil {
    event := events.NewNotificationEvent(
        callerUUID,
        "",
        uuid.New().String(),
        int32(sessionv1.NotificationType_NOTIFICATION_TYPE_INPUT_REQUIRED),
        int32(sessionv1.NotificationPriority_NOTIFICATION_PRIORITY_MEDIUM),
        "Triage complete",
        fmt.Sprintf("%s — %d suggestion(s). Click to review.", itemTitle, len(suggestions)),
        map[string]string{"item_id": itemID},
    )
    h.eventBus.Publish(event)
}
```

### 6. Proto Deserialization Pattern (Backlog Service)
**Reuse in**: Mapping stored JSON fields to proto messages

**Pattern** (from `itemSessionToProto`, lines 201–222):
```go
// Deserialize JSON from DB string field
if is.TriageResult != "" {
    var tr triageResultJSON
    if jsonErr := json.Unmarshal([]byte(is.TriageResult), &tr); jsonErr == nil {
        // Map to proto message
        p.TriageResult = &sessionv1.TriageResult{
            Summary:     tr.Summary,
            Suggestions: suggs,
            Tasks:       tasks,
        }
    }
}
```

---

## Integration Points

### 1. Proto Contract
- **File**: `proto/session/v1/backlog.proto`
- **Relevant types**: `TriageResult`, `TriageSuggestion`, `TriageTask`, `ItemSession`
- **Integration point**: New fields added here propagate to TypeScript bindings via code generation

### 2. Service Layer (RPCs)
- **File**: `server/services/backlog_service.go`
- **Key functions**:
  - `CreateBacklogItem()` — Can auto-trigger triage in background
  - `TriggerTriage()` — Spawns triage session with prompt and artifacts directory
  - `TransitionBacklogItemStatus()` — Enforces state machine including triage preconditions
  - `BuildTokenBudgetedPrompt()` — Builds work session prompt with triage plan context (line 912)

### 3. MCP Tool Layer
- **File**: `server/mcp/tools_backlog.go`
- **Key tools**:
  - `get_backlog_item()` — Returns role-specific guidance (includes triage workflow for triage role)
  - `submit_triage_result()` — Captures and persists triage output
  - `report_progress()`, `request_review()` — Used by work sessions after triage plan is approved

### 4. Lifecycle Listener
- **File**: `session/backlog_lifecycle.go`
- **Integration**: Automatically transitions work sessions to review on exit, but leaves triage sessions as-is
- **Hook point**: `OnLifecycleEvent()` fires for all session role types

### 5. Frontend Hooks
- **File**: `web-app/src/lib/hooks/useBacklogService.ts`
- **Functions**:
  - `mapItemSession()` — Maps proto ItemSession to LinkedSession (can expose triageResult)
  - `getTriageStatus()` — Derives "running" / "completed" / "idle" from linked triage sessions
  - `createBacklogItem()` — Maps response including `triageTriggered` flag

### 6. React Components
- **Files**: `web-app/src/components/backlog/`
  - `BacklogItemDetail.tsx` — Renders TriageLoadingIndicator or TriageReviewPanel
  - `TriageLoadingIndicator.tsx` — Shows loading state while session runs
  - `TriageReviewPanel.tsx` — Displays suggestions and plan artifacts (file references)
  - `VaguenessPromptModal.tsx` — Warns if item description is too vague before triage

---

## Summary

The triage pipeline is fully realized as a one-shot session workflow:

1. **Spawn**: `TriggerTriage()` creates a one-shot session with `oneShot=true` and injects a structured prompt
2. **Execute**: AI agent performs research and planning in parallel, writing outputs to `docs/tasks/{slug}/`
3. **Report**: `submit_triage_result()` MCP tool captures and persists results as JSON in `ItemSession.triage_result`
4. **Lifecycle**: Session exits cleanly after calling the MCP tool; `ended_at` is recorded automatically
5. **Notify**: EventBus publishes INPUT_REQUIRED notification for operator review
6. **Transition**: Operator approves suggestions via UI, triggering `updateBacklogItem` + `transitionStatus` to `ready`

The `oneShot` flag enables one-shot behavior for triage and review sessions. Role-based MCP tool access ensures triage results can only be submitted from triage sessions. Artifact paths ensure plan documents persist for operator and work session review.
