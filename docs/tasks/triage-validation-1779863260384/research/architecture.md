# Triage Pipeline Architecture: Complete Data Flow & Validation Approach

## Executive Summary

The triage pipeline orchestrates a one-shot Claude session to analyze a backlog item and produce structured planning artifacts. It spans six major boundaries: backlog RPC service, session spawning, MCP tool injection, session lifecycle, database persistence, and UI notification.

## Complete Data Flow: Seven Steps

### Step 1: User Triggers Triage
- BacklogService.TriggerTriage(item_id) RPC called
- Validates: item exists, status in [idea, ready], repo_path set
- Orphan session check: query triage sessions, verify live, block if concurrent
- Side-effect: reset to idea if re-triggering from ready

### Step 2: Spawn One-Shot Session
- Build slug from title, artifact path = docs/tasks/<slug>
- Build system prompt with embedded item context
- Call SessionService.CreateDirectorySession(title="triage:<slug>", path=repo, prompt=..., tags=["backlog:triage"], oneShot=true)
- oneShot=true: Claude CLI -p flag, exits after task

### Step 3: Session Creation & MCP Injection
- Create tmux session, git worktree
- InjectMCPConfig: write .claude/settings.local.json with MCP server entry
- InjectHooksConfig: register HTTP hooks for permission approval
- Set STAPLER_SESSION_UUID environment variable
- instance.Start(true): spawn Claude process
- EventStarted fires: UpdateItemSessionStarted(started_at)

### Step 4: Claude Execution
- Receives system prompt, MCP tools from settings
- Runs research, synthesis, validation tasks
- Calls submit_triage_result MCP tool with summary, suggestions, tasks

### Step 5: MCP Tool Submission
- Handler: server/mcp/tools_backlog.go:submitTriageResult()
- Extract session UUID from context
- Verify ItemSession link + role="triage"
- Build JSON: summary, suggestions, tasks
- Storage.UpdateItemSessionTriageResult(json)
- Storage.UpdateBacklogItem(plan_artifacts_path)
- eventBus.Publish(triage-complete notification)

### Step 6: Session Exit
- Claude process terminates (oneShot)
- EventExited fires: UpdateItemSessionEnded(ended_at)
- No state transition (triage sessions don't drive status changes)

### Step 7: UI Notification
- Web UI receives NOTIFICATION_TYPE_INPUT_REQUIRED
- Review queue displays triage card with summary, suggestions, tasks
- Operator decides: reject, accept+ready, accept+in_progress, archive

## MCP Tool Injection Mechanism

"Prompt injection" = systematic embedding of:
1. System instructions (task definition)
2. Item context (title, description, AC)
3. MCP server reference (settings.local.json)
4. Session UUID (environment variable)
5. Target directory (artifact path)

Session creation writes .claude/settings.local.json with:
- mcpServers.stapler-squad.type: stdio
- mcpServers.stapler-squad.command: <binaryPath>
- mcpServers.stapler-squad.args: ["--mcp"]

Claude CLI discovers server, registers tools, enables submit_triage_result

## Session Lifecycle for One-Shot/Triage

[Created] -> InjectMCPConfig/InjectHooksConfig -> instance.Start(true)
-> [Running] (EventStarted fired)
-> Claude executes triage
-> Claude process exits
-> [Stopped] (EventExited fired)

Duration: 2-10 minutes typically

## Backlog Item State Machine

States: idea, refining, ready, in_progress, review, done, archived

TriggerTriage guards:
- Item must be idea or ready
- If ready, transition back to idea (re-triage)

After triage completes:
- Status: UNCHANGED (still idea)
- TriageResult: ADVISORY (suggestions, questions, tasks)
- Operator: DECIDES next action (no auto-transition)

BacklogLifecycleListener:
- Triage sessions: only record timestamps, no state change
- Work sessions: drive in_progress -> review on exit
- Review sessions: no auto-transition

## Component Boundaries

BacklogService:
- TriggerTriage RPC handler
- Validate item, orphan check, build prompt, create ItemSession
- Dependencies: SessionCreator, Storage, Config

SessionService:
- CreateDirectorySession method
- Instantiate, inject MCP/hooks, start process, wire listener
- Dependencies: InstanceStore, EventBus, BacklogLifecycleListener

MCP Server (server/mcp/tools_backlog.go):
- submit_triage_result tool handler
- Extract UUID, verify link+role, persist JSON, publish event
- Dependencies: Storage, EventBus

Storage Layer:
- GetBacklogItem, CreateItemSession, UpdateItemSessionTriageResult, UpdateBacklogItem
- Persist and query backlog/session data
- Dependencies: Ent ORM

BacklogLifecycleListener:
- onSessionStarted, onSessionExited handlers
- Record timestamps, drive state transitions (work only)
- Dependencies: Storage, ReviewGateSpawner

## Validation Checklist

Data Flow:
- item_id flows from UI -> prompt -> MCP tool args
- TriageResult JSON persists and round-trips
- plan_artifacts_path set on BacklogItem

Session Lifecycle:
- oneShot=true creates -p flag, exits after task
- EventStarted records started_at
- EventExited records ended_at
- Terminal streamed to UI

MCP Access Control:
- STAPLER_SESSION_UUID env var set
- ItemSession link verified (session_uuid, item_id)
- SessionRole verified ("triage" required)
- ErrPermissionDenied on failure

Orphan Session Handling:
- started_at=NULL: orphaned
- IsSessionLive()=false: orphaned
- item.Status != "idea": orphaned
- Orphaned sessions tombstoned
- Live sessions block re-trigger (CodeAlreadyExists)

Settings Injection:
- MCP server entry in .claude/settings.local.json
- Command path absolute and correct
- Permission hook injected
- Atomic write (temp + rename)

UI Integration:
- Notification published on triage-complete
- Type: NOTIFICATION_TYPE_INPUT_REQUIRED
- Title: "Triage complete"
- Message includes item title + suggestion count
- Review queue renders card with suggestions + tasks
- Session card transitions to completed state

Error Handling:
- Item not found: CodeNotFound
- Status guard failure: CodeFailedPrecondition
- Concurrent triage: CodeAlreadyExists
- Session link failure: ErrPermissionDenied
- Invalid UUID: ErrInvalidArgument

## Known Limitations

1. No resume support (each triage is fresh conversation)
2. Artifact cleanup (docs/tasks/<slug>/ accumulate)
3. Settings re-injection idempotence (checked, skipped if present)
4. Orphan detection gaps (sessionStopper.IsSessionLive() can have false positives)
5. Naming collision risk (slug used for tmux session name)

## Key Code Paths

BacklogService.TriggerTriage: /server/services/backlog_service.go:1058-1184
SessionService.CreateDirectorySession: /server/services/session_service.go:529-566
MCP Tool Handler: /server/mcp/tools_backlog.go:402-536
Prompt Builder: /server/services/backlog_service.go:1186-1259
MCP Injection: /server/services/mcp_injector.go, /server/services/hook_injector.go
Session Lifecycle: /session/backlog_lifecycle.go
Backlog State Machine: /session/backlog.go, /session/backlog_lifecycle.go
