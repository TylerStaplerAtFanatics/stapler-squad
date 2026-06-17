# Triage Pipeline Architecture

## Overview

The triage pipeline is a multi-component system that orchestrates the creation of planning artifacts (research, analysis, validation) for backlog items via an AI agent session. The flow moves data through server RPCs, session spawning, MCP tool injection, Claude execution, and finally result submission back to the server.

## Full Flow: Backlog Item → Triage Session → MCP Result

### 1. Triage Trigger (Server RPC)
- **Entry**: `BacklogService.TriggerTriage()` (server/services/backlog_service.go:1058)
- **Input**: `TriggerTriageRequest` with `item_id` (UUID of backlog item)
- **Preconditions**:
  - Item status must be "idea" or "ready"
  - Item must have a `repo_path` set
  - No other open triage session for this item (checked, orphaned sessions cleaned up)
  - Artifact directory `docs/tasks/{slug}/` must be createable

### 2. Session Creation
- **Handler**: `BacklogService.TriggerTriage()` steps 4–8
- **Session Parameters**:
  - Title: `"triage:{slug}"` (deterministic, derived from item title)
  - Path: `item.RepoPath` (item's git repository root)
  - Role: `"backlog:triage"` (tagged in session)
  - OneShot: `true` (session exits after completion)
  - Prompt: `buildTriagePrompt()` injected at startup (server/services/backlog_service.go:1186)

### 3. Prompt Content
The triage prompt passed to Claude includes:
- Item title, description, acceptance criteria
- **Item ID**: The UUID to pass to `submit_triage_result`
- **Artifact paths**: Absolute paths where research files must be written (`docs/tasks/{slug}/research/*.md`, `docs/tasks/{slug}/plan.md`, etc.)
- **Task breakdown**: 5-step workflow (research → synthesis → validation → submit)
- **Tool instructions**: Reference to `submit_triage_result` MCP call

### 4. MCP Server Injection
When `sessionCreator.CreateDirectorySession()` is called:
- A new tmux session is spawned with Claude
- **MCP injection occurs**: `InjectMCPConfig()` (server/services/mcp_injector.go:24)
  - Writes to `<repo_path>/.claude/settings.local.json`
  - Injects MCP server entry pointing to stapler-squad binary
  - Command: `stapler-squad --mcp`
  - Entry type: `"stdio"` (bidirectional text protocol)
- Claude starts with `--mcp-config` flag containing the injected server config

### 5. Session UUID Binding
- **ItemSession created**: `BacklogService.TriggerTriage()` step 9
  - Links the backlog item to the newly spawned session UUID
  - Records `session_role = "triage"`
  - Stores snapshot of acceptance criteria
- **Environment injection**: Session gets `STAPLER_SESSION_UUID={uuid}` env var
  - Passed via tmux environment (session/instance_tmux.go:81)
  - Used by MCP handlers to verify session-to-item authorization

### 6. Claude Execution & MCP Tool Calls
- Claude reads the triage prompt and begins execution
- As Claude works, it calls MCP tools via the injected stapler-squad server:
  - `get_backlog_item(item_id)` — reads item details with role-specific guidance
  - `report_progress(item_id, criteria_index, status)` — marks AC criteria as pass/fail (work role only)
  - `request_review()` — signals review readiness (work role only)
  - **`submit_triage_result(item_id, ...)`** — **final submission** (triage role only)

### 7. MCP Tool Execution: Role-Based Authorization
Each MCP tool handler (server/mcp/tools_backlog.go) performs:

**a) Session UUID Extraction**
```go
callerUUID, err := callerSessionUUID(ctx)  // From STAPLER_SESSION_UUID header
if err != nil {
    return errResult(ErrPermissionDenied, ...)
}
```

**b) Session-to-Item Link Verification**
```go
itemSession, linkErr := h.storage.GetItemSessionBySessionAndItem(ctx, callerUUID, itemID)
```

**c) Role Check**
```go
if itemSession.SessionRole != "triage" {
    return errResult(ErrPermissionDenied, "session role is %q — only 'triage' role may submit", ...)
}
```

### 8. submit_triage_result Handler
- **Function**: `backlogHandlers.submitTriageResult()` (server/mcp/tools_backlog.go:402)
- **Input**:
  - `item_id` (UUID)
  - `summary` (required, 2–3 sentences)
  - `suggestions` (optional, array of {text, rationale})
  - `tasks` (optional, max 12, each with text, estimate, category)
  - `plan_artifact_path` (optional, absolute path to docs/tasks/{slug})

- **Processing**:
  1. Session UUID extracted from context
  2. Item-to-session link verified with `role == "triage"` check
  3. Suggestions/tasks parsed and validated
  4. Payload JSON serialized
  5. JSON stored in `ItemSession.triage_result` column
  6. If `plan_artifact_path` provided, update `BacklogItem.plan_artifacts_path`
  7. Notification published to event bus (if wired)

### 9. Data Storage
- **ItemSession table**: `triage_result` JSON column populated
  - Contains: `{summary, suggestions, tasks}`
  - Deserialized on retrieval by `itemSessionToProto()` (server/services/backlog_service.go:201)
- **BacklogItem table**: `plan_artifacts_path` column updated
  - Points to `docs/tasks/{slug}/` directory containing research/*.md, plan.md, validation.md

### 10. Status Transition
- Triage session typically triggers item status → "ready" (once artifacts are approved)
- Item can then spawn work sessions (role: "work") or be archived

---

## Component Boundaries

### server/
Core backend services and MCP handlers.

#### **services/backlog_service.go**
- `BacklogService` struct: Main orchestrator for backlog operations
- `TriggerTriage()`: Entry point for triage flow (steps 1–9 above)
- `SpawnSessionFromItem()`: Spawns work sessions (requires planning gate pass)
- Data converters: `itemSessionToProto()`, `backlogItemToProto()` (steps 9–10)

#### **services/mcp_injector.go**
- `InjectMCPConfig()`: Writes/merges MCP server config into `.claude/settings.local.json`
  - Checks for idempotency (entry already present?)
  - Atomic write (temp file + rename)
  - Repair logic for corrupted JSON
- `RemoveMCPConfig()`: Removes MCP entry when session stops

#### **mcp/tools_backlog.go**
- `backlogHandlers` struct: Delegates all backlog MCP tool handlers
- `getBacklogItem()`: Fetches item + role-specific workflow guidance
- `reportProgress()`: Marks AC criteria status (work role)
- `requestReview()`: Signals readiness for review (work role)
- `submitReviewVerdict()`: Submits per-criterion review verdicts (review role)
- **`submitTriageResult()`**: Main triage result submission handler (triage role)
  - Validates role=triage
  - Deserializes suggestions/tasks from JSON
  - Persists triage_result on ItemSession
  - Publishes notification event

#### **mcp/server.go**
- MCP server lifecycle (startup, shutdown, tool registration)
- Session UUID context injection via HTTP headers
- Tool discovery and dispatch

### session/
Session instance lifecycle, tmux integration, and data models.

#### **session/instance.go**
- `Instance` struct: In-memory representation of an active Claude session
  - `UUID`: Unique session identifier (passed via STAPLER_SESSION_UUID env var)
  - `Title`: Human-readable name (e.g., "triage:my-feature")
  - `SessionRole`: Role tag ("backlog:triage", "backlog:work", etc.)
  - `Status`: Current state (Running, Paused, Stopped)

#### **session/instance_tmux.go** (with uncommitted changes)
- `buildLaunchCommand()`: Constructs Claude invocation with MCP flags
  - Injects `--mcp-config '{"mcpServers":{"stapler-squad":...}}'`
  - Passes `STAPLER_SESSION_UUID` via env var injection
- `initTmuxSession()`: Creates tmux.TmuxSession object
  - Sets `STAPLER_SESSION_UUID` in tmux environment
- Environment injection: `SetExtraEnv()` adds session UUID to tmux session env

#### **session/storage.go**
- `Storage` struct: Persistence layer for backlog items, item sessions, and triage results
- `CreateItemSession()`: Creates ItemSession record linking session UUID to backlog item + role
- `GetItemSessionBySessionAndItem()`: Fetches link record for authorization (used by MCP handlers)
- `UpdateItemSessionTriageResult()`: Persists triage_result JSON on ItemSession
- `UpdateBacklogItem()`: Updates plan_artifacts_path field

#### **session/types.go**
- Data models for in-memory instances
- `ItemSessionData`: Struct passed to storage during item session creation
- Enums: `SessionRole`, `BacklogStatus`

### proto/session/v1/
Protocol buffer definitions for RPC contracts and data models.

#### **proto/session/v1/backlog.proto** (lines 50–72)
- `ItemSession` message: Links session UUID to backlog item with role
  - `session_uuid`: UUID of the tmux/Claude session
  - `session_role`: Role enum ("triage", "work", "review")
  - `triage_result`: TriageResult message (populated after submit_triage_result)
  - `review_verdict`: ReviewVerdict message (populated after submit_review_verdict)

#### **proto/session/v1/backlog.proto** (lines 50–56)
- `TriageResult` message: Output of triage phase
  - `summary`: Executive summary
  - `suggestions`: Array of {text, rationale}
  - `tasks`: Array of {text, estimate, category}
  - Deserialized from JSON in ItemSession.triage_result column

#### **RPC: TriggerTriageRequest/Response** (lines 226–232)
- Request: `item_id` (UUID)
- Response: `ItemSession` (newly created, role="triage")

#### **RPC: BacklogService.TriggerTriage()** (lines 343–344)
- gRPC service definition; implemented by BacklogService.TriggerTriage()

---

## MCP Tool Injection into Triage Sessions

### Injection Timing
1. **Before session start** (BacklogService.TriggerTriage → sessionCreator.CreateDirectorySession)
2. **During tmux session initialization** (session/instance.go → initTmuxSession)
3. **After git worktree created** (worktree already prepared by sessionCreator)

### Injection Mechanism
```go
// server/services/mcp_injector.go: InjectMCPConfig()
InjectMCPConfig(rootDir, os.Executable())
// Writes to: rootDir/.claude/settings.local.json

// Result in settings.local.json:
{
  "mcpServers": {
    "stapler-squad": {
      "type": "stdio",
      "command": "/path/to/stapler-squad",
      "args": ["--mcp"]
    }
  }
}
```

### Claude Invocation
When Claude starts, it reads the settings file and connects to the MCP server:
```bash
claude --mcp-config '{"mcpServers":{"stapler-squad":{"type":"stdio","command":"...","args":["--mcp"]}}}'
```

### Session UUID Header
- Claude's MCP client sends HTTP headers with X-Stapler-Session-UUID
- Server's MCP handler extracts UUID from header → context.Value()
- All subsequent tool calls access UUID via `sessionUUIDFromContext()`

---

## Data Model for Backlog Items and Triage State

### BacklogItem (database)
```
id (UUID)
title
description
acceptance_criteria (JSON array)
priority
status (idea | ready | inprogress | review | done | archived)
repo_path
plan_artifacts_path (absolute path to docs/tasks/{slug}/)
plan_approved (boolean)
plan_approved_at (timestamp)
skip_review_gate (boolean)
skip_planning (boolean)
notes
created_at, updated_at
```

### ItemSession (database, links session to item)
```
id (UUID)
item_id (FK to BacklogItem)
session_uuid (UUID of tmux/Claude session)
session_role (triage | work | review)
started_at, ended_at (timestamps)
ac_snapshot (JSON array of acceptance criteria at spawn time)
commit_count_since_spawn
last_commit_message, last_commit_at
last_file_touch_at
triage_result (JSON: {summary, suggestions, tasks})
review_verdict (JSON: {overall_outcome, per_criterion[]})
created_at
```

### TriageResult (JSON, stored in ItemSession.triage_result)
```json
{
  "summary": "2-3 sentence executive summary",
  "suggestions": [
    {"text": "...", "rationale": "..."},
    {"text": "What is X?", "rationale": "question"}
  ],
  "tasks": [
    {"text": "...", "estimate": "2h", "category": "backend"}
  ],
  "clarifying_questions": ["..."]
}
```

### Triage State Machine
```
BacklogItem.status:
  idea
    ↓ (TriggerTriage spawns session)
  idea (session running, triage_result empty)
    ↓ (submit_triage_result called)
  ready (artifacts written, operator review pending)
    ↓ (ApprovePlan RPC)
  ready (plan approved, operator can spawn work session)
    ↓ (SpawnSessionFromItem)
  inprogress (work session running)
    ↓ (request_review after all AC done)
  review (work complete, reviewer evaluates)
    ↓ (submit_review_verdict PASS)
  done (triage + work + review complete)
```

---

## How submit_triage_result Flows Back to Server

### 1. Claude MCP Call
```
Tool: submit_triage_result
Arguments:
  item_id: "550e8400-e29b-41d4-a716-446655440000"
  summary: "..."
  suggestions: [...]
  tasks: [...]
  plan_artifact_path: "/Users/tylerstapler/IdeaProjects/myrepo/docs/tasks/feature-x/"
```

### 2. MCP Handler Receipt
- **Handler**: `backlogHandlers.submitTriageResult()` (tools_backlog.go:402)
- **Context**: Receives HTTP request with X-Stapler-Session-UUID header
- **Dispatch**: MCP server routes call to registered tool handler

### 3. Session UUID Validation
```go
callerUUID, err := callerSessionUUID(ctx)
// Extracts from context (set by MCP server from HTTP header)
// Validates UUID format
```

### 4. Link Verification
```go
itemSession, linkErr := h.storage.GetItemSessionBySessionAndItem(ctx, callerUUID, itemID)
// Verifies:
//   1. Session UUID exists in database
//   2. Session is linked to this item
//   3. Session role is "triage"
```

### 5. Payload Validation & Serialization
```go
// Parse and validate suggestions, tasks from JSON
// Cap tasks at 12
// Build canonical TriageResultPayload struct (prevents schema drift)
triagePayload := triageResultPayload{
    Summary:     summary,
    Suggestions: suggestions,
    Tasks:       tasks,
}
payloadJSON, _ := json.Marshal(triagePayload)
```

### 6. Persistence
```go
// Update plan_artifacts_path on BacklogItem if provided
h.storage.UpdateBacklogItem(ctx, itemID, update, nil)

// Store triage_result JSON on ItemSession
h.storage.UpdateItemSessionTriageResult(ctx, itemSession.ID.String(), string(payloadJSON))

// Log for debugging
log.InfoLog.Printf("[mcp:submit_triage_result] session=%s item=%s triage_result=%s", ...)
```

### 7. Event Publishing (Notification)
```go
if h.eventBus != nil {
    event := events.NewNotificationEvent(
        callerUUID,                           // source session
        "",
        uuid.New().String(),                  // notification ID
        NotificationType_INPUT_REQUIRED,      // type
        NotificationPriority_MEDIUM,          // priority
        "Triage complete",                    // title
        fmt.Sprintf("%s — %d suggestion(s). Click to review.", itemTitle, len(suggestions)),
        map[string]string{"item_id": itemID}, // metadata
    )
    h.eventBus.Publish(event)
}
```
This notification is broadcast to:
- Web UI (updates review queue)
- All connected clients via gRPC streaming
- Operator sees "Triage complete" alert

### 8. MCP Response
```go
return mcpgo.NewToolResultText(fmt.Sprintf(
    "Triage result submitted for item %s. %d suggestion(s) recorded.\n\nSummary: %s",
    itemID, len(suggestions), summary,
))
```
Claude receives confirmation that triage was recorded.

### 9. Session Termination
- One-shot session exits (was started with `oneShot=true`)
- tmux session killed automatically after Claude process exits
- ItemSession.ended_at populated (optional, best-effort)

### 10. Operator Review (Out of Scope for This Doc)
- Operator sees triage complete notification
- Navigates to backlog item detail
- Reviews plan artifacts (research/*.md, plan.md, validation.md in `plan_artifacts_path`)
- Calls ApprovePlan RPC to transition item to ready
- Or calls TriggerTriage again to re-run triage

---

## Key Design Decisions

### 1. Deterministic Session Titles
- Triage session title derived from item title: `"triage:{slug}"`
- Enables stale tmux session cleanup (same slug = same session name)
- Prevents orphaned processes from blocking re-trigger

### 2. One-Shot Sessions for Triage
- Triage session runs with `oneShot=true` → auto-exits after completion
- Prevents operator from accidentally interacting with triage session
- Simplifies cleanup; no manual session management needed

### 3. Session UUID in Environment
- `STAPLER_SESSION_UUID` passed via tmux environment
- Available to Claude subprocess, used by MCP HTTP client
- Enables MCP client to include X-Stapler-Session-UUID header in RPC calls
- MCP server extracts header and injects into RPC context

### 4. Role-Based MCP Authorization
- Each ItemSession has `session_role` (triage, work, review)
- MCP handlers check: `itemSession.SessionRole != "triage"` → deny
- Prevents work sessions from calling submit_triage_result
- Prevents review sessions from calling report_progress

### 5. Atomic MCP Injection
- settings.local.json written atomically (temp file + rename)
- Idempotent: checked before writing (entry already present? skip)
- Repair logic for corrupted JSON (log warning, reset)
- Ensures Claude always has valid MCP config

### 6. Artifact Paths on Disk
- Triage creates `docs/tasks/{slug}/research/*.md`, `plan.md`, `validation.md`
- Absolute path passed to Claude in prompt and via plan_artifact_path param
- Claude and operators can verify artifacts exist via os.Stat
- SpawnSessionFromItem requires plan_artifacts_path to be populated (planning gate)

### 7. JSON Canonical Forms
- Triage results stored as JSON in ItemSession.triage_result
- Separate Go structs in tools_backlog.go (triageSuggestion, triageTask) prevent schema drift
- itemSessionToProto() deserializes JSON → proto for wire protocol
- All conversions use explicit struct fields (no map[string]interface{})

---

## Error Handling & Recovery

### Orphaned Session Cleanup (lines 1088–1114)
If re-triggering on a "ready" item:
- Checks for open triage session
- Session is orphaned if:
  - `started_at == NULL` (never confirmed running)
  - Session UUID not in live in-memory tracker
  - Item status already advanced past "idea"
- Orphaned sessions tombstoned (ended_at set) before new spawn
- Live sessions block with CodeAlreadyExists

### MCP Injection Failure (ignored, non-fatal)
- If InjectMCPConfig fails, session still spawns
- Claude runs without MCP tools → fails with "tool not found" error
- Operator gets error message, can retry TriggerTriage

### Settings File Corruption (recovery)
- InjectMCPConfig detects invalid JSON
- Attempts repair using repairSettingsJSON()
- Falls back to reset if repair fails (logs warning)
- New MCP entry written cleanly

### Session Link Not Found (authorization error)
- If session UUID not linked to item in database
- MCP handler returns ErrPermissionDenied
- Claude receives error, cannot proceed with triage
- Operator must call TriggerTriage again to spawn properly linked session

---

## Configuration & Dependencies

### Wiring (server/handlers.go or main)
```go
storage := session.NewStorage(db)
sessionCreator := handlers.NewSessionCreator(...)
backlogService := services.NewBacklogService(storage, sessionCreator, config, engine)
backlogService.SetSessionStopper(sessionStopper)

// MCP server setup
mcpServer := mcp.NewMCPServer(storage, eventBus)
```

### Environment Variables (in triage session)
```
STAPLER_SESSION_UUID={uuid}  // From instance.go SetExtraEnv()
```

### Claude Flags (in instance_tmux.go buildLaunchCommand)
```
claude --mcp-config '{"mcpServers":{"stapler-squad":...}}' --resume {uuid}
```

### Settings File Location
```
{repo_path}/.claude/settings.local.json
```

---

## Testing Implications

### Unit Tests
- Mock Storage for item session creation/linking
- Mock SessionCreator to avoid real tmux spawning
- Test MCP handler authorization logic in isolation

### Integration Tests
- Create real item, trigger triage, capture session UUID
- Simulate MCP call with context containing UUID
- Verify triage_result persisted on ItemSession

### E2E Tests
- Spawn real triage session
- Have Claude call submit_triage_result
- Verify artifacts written to disk, ItemSession updated

