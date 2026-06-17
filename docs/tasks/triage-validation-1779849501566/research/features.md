# Features Research: Triage Pipeline Validation

## Existing Patterns

### Triage Session Lifecycle (end-to-end)

The triage pipeline is a one-shot automated workflow. Entry point is the `TriggerTriage` RPC in `server/services/backlog_service.go:1056`. The flow:

1. **Status guard** — item must be `idea` or `ready`; if `ready`, status is reset to `idea` before spawning
2. **Orphan-aware double-trigger guard** — open triage sessions without a live tmux process are tombstoned; genuine live sessions return `CodeAlreadyExists`
3. **Stale tmux kill** — `KillTmuxSessionByTitle("triage:<slug>")` runs before spawning to avoid reattach bugs
4. **Artifact directory creation** — `os.MkdirAll(docs/tasks/<slug>/...)` on the `repo_path`
5. **Prompt construction** — `buildTriagePrompt()` at line 1188; inlines item_id, description, AC, and step-by-step instructions for subagent research + `submit_triage_result` call
6. **Session spawn** — `SessionCreator.CreateDirectorySession(ctx, "triage:<slug>", repoPath, triagePrompt, ["backlog:triage"], oneShot=true)` — the `prompt` parameter maps to `Instance.Prompt` (positional CLI arg, not system prompt)
7. **ItemSession creation** — `CreateItemSession` with `SessionRole=SessionRoleTriage` binds the tmux session UUID to the item

### How Claude Receives the Prompt

`Instance.Prompt` is written to the Claude CLI as a quoted positional argument in `buildLaunchCommand` (`session/instance_tmux.go:49-51`):

```go
if i.Prompt != "" && claudeSessionID == "" && strings.Contains(program, "claude") {
    program = fmt.Sprintf("%s %q", program, i.Prompt)
}
```

The `OneShot=true` flag adds `-p` (print/non-interactive mode) immediately before the prompt arg (`instance_tmux.go:46-48`):

```go
if i.OneShot && strings.Contains(program, "claude") {
    program = program + " -p"
}
```

This is the bug that was fixed in commit `19ef4431`: before the fix, `-p` was never added, so Claude entered interactive mode and waited for user input instead of executing and exiting.

### How Claude Authenticates to MCP

The MCP server URL is passed to Claude via `--mcp-config` flag (`instance_tmux.go:31-38`):

```
--mcp-config '{"mcpServers":{"stapler-squad":{"type":"http","url":"<url>","headers":{"X-Stapler-Session-UUID":"<uuid>"}}}}'
```

The header value is `instance.UUID`. On the server side (`server/server.go:403-410`), a middleware wrapper reads `X-Stapler-Session-UUID` from every MCP request and calls `servermcp.WithSessionUUID(r.Context(), uuid)`. This makes `callerSessionUUID(ctx)` work in all tool handlers.

### `submit_triage_result` MCP Tool Handler

Located in `server/mcp/tools_backlog.go:402-536`. Steps:

1. Extracts caller UUID via `callerSessionUUID(ctx)` — fails with `ErrPermissionDenied` if UUID absent
2. Validates `item_id` UUID format
3. Calls `GetItemSessionBySessionAndItem(ctx, callerUUID, itemID)` — fails if session not linked to item
4. **Role guard**: `itemSession.SessionRole != "triage"` → `ErrPermissionDenied` (prevents work/review sessions from submitting triage results)
5. Parses `suggestions` (array of `{text, rationale}`) and `tasks` (array of `{text, estimate, category}`, capped at 12)
6. Serializes to JSON `triageResultPayload{Summary, Suggestions, Tasks}`
7. If `plan_artifact_path` provided: calls `UpdateBacklogItem` to set `PlanArtifactsPath`
8. Calls `UpdateItemSessionTriageResult(ctx, itemSession.ID, jsonPayload)` to persist
9. Publishes `NotificationType_NOTIFICATION_TYPE_INPUT_REQUIRED` event via EventBus: "Triage complete — N suggestion(s). Click to review."

### Session Exit / EndedAt Recording

`BacklogLifecycleListener.onSessionExited` (`session/backlog_lifecycle.go:97-153`) fires when a session exits:

1. `UpdateItemSessionEnded(ctx, is.ID, now)` — **called for all session roles** (triage, review, work)
2. Role guard: `if is.SessionRole != SessionRoleWork { return }` — only work sessions proceed to status transition
3. Work sessions: transitions item to `review` (or `done` if `SkipReviewGate`) and optionally spawns review gate

The second bug in `19ef4431` was that `UpdateItemSessionEnded` was called **after** the role guard, so triage sessions never had `ended_at` set. The UI showed a permanent "running" spinner.

### `getBacklogItem` MCP Tool — Role-Aware Guidance

When Claude calls `get_backlog_item`, the handler (`tools_backlog.go:78-164`) checks the caller's session role. For `role == "triage"`, it injects:

```
## Your Role: Triage
Analyze the codebase and produce planning artifacts. Do NOT modify source code.
Workflow:
1. Run parallel research subagents → write research/*.md files
2. Synthesize into plan.md + validation.md
3. Call submit_triage_result with: item_id, summary, suggestions, tasks (max 12), plan_artifact_path
```

This is a secondary guidance mechanism; the primary prompt is injected via `buildTriagePrompt` at spawn time.

### Previous Triage Validation Runs

Four prior triage validation runs exist under `docs/tasks/`:
- `triage-validation-1779842458884/` — contains `research/{architecture,features,pitfalls,stack}.md`, `plan.md`, `validation.md`
- `triage-validation-1779842861018/` — contains `plan.md`, `validation.md`
- `triage-validation-1779842989855/` — contains `plan.md`, `validation.md`

These directories were written by Claude triage agents in earlier validation runs. Their presence confirms the pipeline was executing research subagents and calling `submit_triage_result` (which sets `plan_artifacts_path`).

### Existing Unit Tests

Key test names for CI verification:
- `TestTriggerTriage_DoubleTriggerGuard` — `server/services/backlog_service_test.go`
- `TestBacklogLifecycleListener_OnSessionExited_ReviewSession_NoTransition` — `session/backlog_lifecycle_test.go`
- `TestSubmitTriageResult_PublishesNotificationOnSuccess` — `server/mcp/tools_backlog_test.go`
- `TestSubmitTriageResult_NoNotificationWhenEventBusNil` — `server/mcp/tools_backlog_test.go`

### E2E Test

`tests/e2e/triage-pipeline-validation.spec.ts` — gated behind `TRIAGE_VALIDATION=true` env var. 6-minute timeout. Asserts:
1. Item created with real `repo_path`
2. `TriggerTriage` button clicked
3. Loading indicator or trigger button hidden within 30s (session started)
4. `[data-testid="triage-review-panel"]` visible within 5 minutes (submit_triage_result called)
5. Summary text non-empty (> 10 chars)

## Reusable Components

| Component | Location | Reuse Notes |
|---|---|---|
| `buildTriagePrompt()` | `server/services/backlog_service.go:1188` | Generates structured step-by-step agent prompt; follow this pattern for review/work prompts |
| `CreateDirectorySession()` | `server/services/session_service.go:529` | Canonical one-shot session spawner; sets `Prompt`, `OneShot`, `MCPServerURL`, wires lifecycle |
| `callerSessionUUID(ctx)` | `server/mcp/tools_backlog.go:36` | Standard pattern for MCP tool auth; used in all 4 tool handlers |
| `WithSessionUUID` / middleware | `server/server.go:403-410` + `tools_backlog.go:25` | Header→context injection; already wired globally |
| `BacklogLifecycleListener.WireToInstance` | `session/backlog_lifecycle.go:73` | Wire any new auto-session to lifecycle tracking; called in `CreateDirectorySession` |
| Role-aware `get_backlog_item` response | `server/mcp/tools_backlog.go:127-154` | Extend `switch role` block for any new session roles |
| `ItemSession.SessionRole` constants | `session/backlog_*.go` | `SessionRoleTriage`, `SessionRoleWork`, `SessionRoleReview` — use for any new automated roles |
| Notification publish pattern | `tools_backlog.go:512-530` | `events.NewNotificationEvent(...)` → `h.eventBus.Publish(...)` — reuse for any completion event |

## Pipeline Fix (commit 19ef4431)

Three bugs were fixed. All three must hold for Claude to receive the prompt and submit results.

### Fix 1 — Prompt Injection (was: system prompt; now: positional arg)

**File**: `server/services/backlog_service.go:26`  
**Change**: Interface renamed `appendSystemPrompt string` → `prompt string`  
**Effect**: `TriggerTriage` now passes the triage instructions as `Instance.Prompt`, which `buildLaunchCommand` appends as a quoted positional argument (`claude -p "<prompt>"`). Before the fix, the prompt went via `AppendSystemPrompt` → `--append-system-prompt`, which only provides system context. Claude would start with the right context but no user message, so it waited silently.

**Verification**: `instance_tmux.go:49-51` — `i.Prompt != ""` path adds `%q` to program string. Observable: Claude writes research files to `docs/tasks/<slug>/`.

### Fix 2 — EndedAt Never Set for Triage Sessions

**File**: `session/backlog_lifecycle.go:109-118`  
**Change**: `UpdateItemSessionEnded` moved to execute **before** the `SessionRole != SessionRoleWork` early return  
**Effect**: Triage (and review) sessions now have `ended_at` set when Claude exits. The UI uses this to hide the loading spinner and update session status.

**Verification**: `TestBacklogLifecycleListener_OnSessionExited_ReviewSession_NoTransition` — confirms `UpdateItemSessionEnded` is called before the role guard. Observable: UI no longer shows "running" after triage completes.

### Fix 3 — OneShot Flag Not Appending `-p`

**File**: `session/instance_tmux.go:46-48`  
**Change**: Added `if i.OneShot && strings.Contains(program, "claude") { program = program + " -p" }`  
**Effect**: Triage sessions run `claude -p "<prompt>"`. The `-p` flag puts Claude in print mode — it executes the prompt non-interactively and exits when done. Before the fix, `OneShot=true` was set but had no effect; Claude stayed in interactive mode after finishing, the session never exited, and `onSessionExited` never fired.

**Verification**: `instance_tmux.go:46-48` present in current code. Observable: tmux session exits after triage; `ItemSession.ended_at` is set.

### Interaction Between Fixes

The three bugs were interdependent. Even with Fix 1 (prompt reaches Claude), without Fix 3 (no `-p`), Claude would complete triage and wait for more input — never exiting. Without Fix 2 (EndedAt), even if Claude did exit, the UI would still show "running". All three must be present for the full observable behavior: Claude works → submits results → exits → UI shows review panel.

### Current State Confirmed

All three fixes are present in `main` as of commit `19ef4431`. The modified files are:
- `session/instance_tmux.go` — `-p` flag conditional (lines 46-48)
- `session/backlog_lifecycle.go` — `UpdateItemSessionEnded` before role guard (lines 109-113)
- `server/services/backlog_service.go` — interface uses `prompt` not `appendSystemPrompt` (line 26)
