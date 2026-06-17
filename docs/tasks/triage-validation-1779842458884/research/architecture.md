# Triage Pipeline Architecture

## Overview
The Stapler Squad triage pipeline orchestrates a flow from backlog item creation through Claude agent execution to result submission. The architecture spans five layers: Backlog API → Session Creation → MCP Injection → Claude Execution → Status Update.

## Layer 1: Backlog Service API
**File**: `server/services/backlog_service.go`

- **TriggerTriage()**: Initiates triage for a backlog item. Guards preconditions (status must be "idea" or "ready", repo_path required), kills stale tmux sessions by deterministic name, creates artifact directories (docs/tasks/{slug}/research), builds a one-shot triage prompt, and spawns a session via SessionCreator.
- **CreateItemSession()**: Creates an ItemSession record linking the spawned session UUID to the backlog item with role="triage".

## Layer 2: Session Creation & Prompt Injection
**File**: `server/services/session_service.go:CreateDirectorySession()`

- Creates a `session.Instance` with user-provided prompt, program, and options.
- Sets `oneShot=true` for triage sessions (auto-exits after Claude completes).
- Calls `instance.Start()` to spawn tmux session with `--append-system-prompt` containing the triage prompt.
- Registers instance in storage and wires BacklogLifecycleListener (fires status transitions on session exit).

## Layer 3: MCP Server Registration
**File**: `server/mcp/server.go` + `server/mcp/tools_backlog.go`

- **Session UUID Injection**: When Claude session starts, it receives `STAPLER_SESSION_UUID` environment variable from backlog service.
- **NewCore()**: Registers backlog tools (get_backlog_item, submit_triage_result, report_progress, etc.) if storage is wired.
- **backlogHandlers**: Holds reference to storage and optional eventBus for notifications.

## Layer 4: Claude Execution & Result Submission
**File**: `server/mcp/tools_backlog.go:submitTriageResult()`

- Claude agent calls `submit_triage_result` MCP tool with item_id, summary, suggestions, tasks, plan_artifact_path.
- Tool validates: session UUID from context, session role=="triage", item link exists.
- Persists triage result JSON to ItemSession via `UpdateItemSessionTriageResult()`.
- If eventBus wired: publishes `NotificationEvent` (type=INPUT_REQUIRED) notifying operator of completion.

## Layer 5: Status Updates & Lifecycle
**File**: `server/services/backlog_service.go` + `session/backlog_lifecycle_listener.go`

- **BacklogLifecycleListener**: Registered on session exit, triggers state transitions:
  - On successful triage submission: item transitions to "planning_approved" (if suggestions < threshold).
  - Stores triage artifacts path for downstream spawn operations.
- **ItemSession record**: Persists triage_result JSON, AC snapshot at triage time, session link.

## Data Flow
```
Backlog Item (db)
  ↓ TriggerTriage()
Session UUID generated, prompt built
  ↓ CreateDirectorySession()
tmux session spawned with STAPLER_SESSION_UUID env var
  ↓ MCP Injection
Claude reads STAPLER_SESSION_UUID from env
  ↓ submit_triage_result()
Session validated (role=triage), result saved to ItemSession
  ↓ EventBus notification
Operator notified of completion, item transitions to next status
```

## Key Architectural Boundaries

**Backlog Layer**: Pure RPC/service logic, no tmux or MCP knowledge.
**Session Layer**: Tmux orchestration, prompt injection, lifecycle events.
**MCP Layer**: Tool registration, context injection (session UUID), tool handler dispatch.
**Storage Layer**: Persists ItemSession, triage results, artifact paths.
**Event Layer**: Optional notifications via EventBus (nil-safe degradation).

## Risk Areas for Triage Fix

1. **Session UUID Environment Injection**: Must be set before Claude initialization. If not injected or lost, submit_triage_result fails with permission denied.
2. **ItemSession Role Validation**: Only "triage" role may call submit_triage_result. Cross-role calls blocked at MCP layer.
3. **Session Lifecycle Cleanup**: Stale tmux sessions (same deterministic name) killed before spawn to ensure fresh --append-system-prompt injection.
4. **EventBus Null Handling**: If eventBus is nil, no notifications fire (graceful degradation).

