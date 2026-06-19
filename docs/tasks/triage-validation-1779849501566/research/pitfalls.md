# Pitfalls Research — Triage Pipeline Validation

**Date**: 2026-05-26
**Scope**: Known risks, failure modes, race conditions, and edge cases for validating the triage pipeline fix.

---

## Known Issues Fixed

### Bug 1: Prompt injection — `AppendSystemPrompt` vs `Prompt`

**What broke**: `buildTriagePrompt` output was passed as `appendSystemPrompt` to `CreateDirectorySession`, which mapped to the `--append-system-prompt` CLI flag. This injects content into Claude's system context only. Claude waits for a human turn (user message) before starting work — so with no user message, triage sessions would start and then block indefinitely waiting for input.

**Fix applied** (commit `19ef4431`): The `CreateDirectorySession` interface parameter was renamed from `appendSystemPrompt` to `prompt`, and the session option now uses `Prompt` (→ positional CLI argument) instead of `AppendSystemPrompt`. The `buildLaunchCommand` function appends the prompt as a quoted positional argument: `claude -p "...prompt..."`.

**Validation signal**: A triage session that received the prompt correctly will begin writing research files within seconds of starting. A session still broken on this bug would sit at the Claude interactive prompt indefinitely.

---

### Bug 2: `EndedAt` never set for triage sessions

**What broke**: `onSessionExited` in `backlog_lifecycle.go` had a guard — `if is.SessionRole != SessionRoleWork { return }` — placed *before* the `UpdateItemSessionEnded` call. Triage sessions exited but `EndedAt` was never written. The UI showed the triage session as permanently "running" after exit, and the double-trigger guard (`EndedAt == nil`) would permanently block re-triggers.

**Fix applied** (commit `19ef4431`): `UpdateItemSessionEnded` was moved to execute *before* the role guard. All roles (triage, review, work) now record `EndedAt` on exit. The role guard only controls status transitions (in_progress→review/done), not exit recording.

**Validation signal**: After a triage session completes, `ItemSession.ended_at` must be non-null. The triage session's status indicator in the UI should change from "running" to ended.

---

### Bug 3: OneShot flag ignored — Claude stayed in interactive mode

**What broke**: `InstanceOptions.OneShot = true` was set for triage sessions, but `buildLaunchCommand` never added the `-p` (print/non-interactive) flag to the Claude command. Claude launched in interactive mode, processed the prompt, printed output, but never exited — the session stayed alive indefinitely.

**Fix applied** (commit `19ef4431`): `buildLaunchCommand` now prepends `-p` to the program string when `OneShot=true` and the program contains "claude". This ensures the CLI runs non-interactively and exits after producing output.

**Critical ordering**: The `-p` flag must come *before* the prompt positional argument. Current implementation appends `-p` to the program string before the prompt is appended, so the final command is `claude -p "...prompt..."`. If the order were reversed (`claude "...prompt..." -p`), the flag would be treated as a positional argument.

---

### Bug 4: `TestTriggerTriage_DoubleTriggerGuard` false pass

**What broke**: The test was passing `nil` as the `sessionStopper` and creating an item session with no `started_at`. A `nil` stopper is treated as "session not live" (`notLive = true`), so the orphan path was taken regardless — the test never exercised the actual guard. The session was also unmarked as "started", which also triggers the orphan path.

**Fix applied** (commit `19ef4431`): Test now wires a `mockSessionStopper` that returns `true` for the test UUID, and marks the item session as started via `UpdateItemSessionStarted`. The guard now exercises the correct code path and the test legitimately rejects the double-trigger.

---

## Remaining Risks

### R1: Prompt delivery confirmation gap

The system has no mechanism to verify that Claude actually received and parsed the prompt correctly. `buildLaunchCommand` quotes the prompt, but:
- Very long prompts (the triage prompt is ~1500 characters) could hit shell argument length limits in rare environments.
- Shell quoting of the prompt uses `%q` (Go's `fmt.Sprintf`), which escapes special characters. If the prompt contains sequences that interact with tmux's key-send mechanism, they could be corrupted.

**Mitigation gap**: There is no health check confirming the triage session actually began executing (wrote its first file) within a timeout window.

---

### R2: Race between `CreateItemSession` and lifecycle event

`TriggerTriage` calls `CreateDirectorySession` (step 8) and then `CreateItemSession` (step 9). The `BacklogLifecycleListener` fires `onSessionStarted` and `onSessionExited` based on the session UUID. If a triage session is extremely fast (unlikely but theoretically possible in tests), `onSessionExited` could fire before `CreateItemSession` persists the row, causing `GetItemSessionBySessionUUID` to return `ErrNotFound` — the exit is silently dropped and `EndedAt` is never set.

**Current mitigation**: The `onSessionExited` goroutine runs after the session's tmux exit, which requires tmux to detect exit and fire the lifecycle event. This is at minimum several hundred milliseconds, well after the DB write. Risk is low in production, but a test that directly fires `EventExited` immediately after session creation could hit this window.

---

### R3: Orphan detection depends on `sessionStopper` being wired

The double-trigger guard has four conditions that classify a session as orphaned (safe to replace):
1. `StartedAt == nil` (never started)
2. `sessionStopper == nil` (stopper not wired — always treats as orphan)
3. `!stopper.IsSessionLive(uuid)` (not in live poller)
4. `item.Status != "idea"` (item advanced past idea)

Condition 2 is a silent footgun: if `BacklogService.SetSessionStopper` is never called (e.g., in a test or degraded production config), *all* triage sessions are treated as orphaned and re-trigger is always allowed. This bypasses the guard entirely. In production the stopper is wired in `server/dependencies.go`, but any configuration where it is omitted will silently disable double-trigger protection.

---

### R4: Stale tmux session not killed if `sessionStopper` is nil

`TriggerTriage` step 4.5 calls `sessionStopper.KillTmuxSessionByTitle` before spawning the new session. If `sessionStopper` is nil, this step is skipped and an old tmux session with the same title may still exist. When `TmuxSession.Start()` is called, it will reattach to the existing tmux session instead of creating a fresh one — silently skipping the new prompt injection.

---

### R5: `submit_triage_result` requires `STAPLER_SESSION_UUID`

The MCP tool `submit_triage_result` validates that the caller has `STAPLER_SESSION_UUID` set in its environment. Triage sessions are spawned with `session.SetExtraEnv([]string{"STAPLER_SESSION_UUID=" + i.UUID})`. If for any reason the env var is not propagated (e.g., the session was created without a UUID, or tmux strips the env), the tool call will fail with `ErrPermissionDenied` and triage results will never be persisted.

---

### R6: `triageStatus` "failed" not distinguished from "completed"

The frontend derives `triageStatus` from `ItemSession.endedAt`. A triage session that exited due to a crash or error will appear identical to a successful triage in the UI — both have `endedAt` set. Only the presence of `triageResult.summary` distinguishes them, but the UI logic (in `useBacklogService.ts`) maps `endedAt != nil` to `triageStatus = "completed"` regardless of whether any triage result was written.

---

## Edge Cases

### EC1: Claude exits without calling `submit_triage_result`

If Claude finishes writing research files but exits (oneshot `-p` terminates after printing output) without calling the MCP tool:
- `ItemSession.triage_result` remains empty
- `ItemSession.ended_at` is set (Bug 2 fix ensures this)
- `BacklogItem.plan_artifacts_path` is never updated
- The item stays in "idea" status
- The UI will show `triageStatus = "completed"` (EC2 risk: see R6) or no triage result

The operator would need to manually trigger triage again or manually approve the plan.

**Detection**: Check that `ItemSession.triage_result` is non-empty after triage session exits.

---

### EC2: Malformed prompt — missing `item_id` in prompt body

`buildTriagePrompt` includes the item UUID in the prompt body: `item_id (pass this as item_id to submit_triage_result): <UUID>`. If Claude fails to parse or loses this UUID (e.g., truncation due to context limits), `submit_triage_result` will be called with the wrong `item_id` or fail UUID validation.

The tool has `validateUUID` as a guard, so a malformed UUID will return `ErrInvalidArgument` — the result will not be persisted, but Claude may not surface the error in a way that triggers a retry.

---

### EC3: Prompt too long / context overflow

The triage prompt (~1500 chars) plus the item description and acceptance criteria are combined. For items with very long descriptions, the total prompt could approach Claude's context limit or the shell argument length limit (~2MB on macOS). If the prompt is truncated by the shell, Claude may receive partial instructions and write incomplete research files.

---

### EC4: Re-trigger on a "ready" item resets status to "idea"

`TriggerTriage` step 3b resets a "ready" item back to "idea" when re-triggering. If the UI does not clearly communicate this regression, operators may be confused that a previously-ready item is back in "idea" status. Additionally, if a work session was spawned between the item becoming "ready" and the re-trigger, the item transitions to "in_progress" — but `TriggerTriage` has a status guard that rejects anything other than "idea" or "ready", so re-trigger on an in-progress item returns `CodeFailedPrecondition`.

---

### EC5: Concurrent `TriggerTriage` calls (TOCTOU)

The double-trigger guard reads existing sessions and checks for open ones, then creates a new session. There is no database-level lock between the check and the creation. Two concurrent `TriggerTriage` calls could both read zero open sessions, both pass the guard, and both proceed to create duplicate triage `ItemSession` records.

**Mitigation in place**: The guard is implemented in application code only. The risk exists on rapid concurrent calls but is unlikely in normal operation. A database-level unique constraint on (item_id, session_role, ended_at=NULL) would close this gap.

---

### EC6: Triage session does not have MCP server URL

`TriggerTriage` calls `CreateDirectorySession` which internally uses `s.mcpServerURL` (from `SessionService`). If `MCPServerURL` is empty, the triage session launches without MCP config — the `submit_triage_result` tool is not available to Claude, and triage can never complete. The session exits after writing files (if it does any work at all) but never calls the MCP tool.

**Detection**: Triage sessions should have `--mcp-config` in their launch command. This can be verified in session logs or by inspecting `Instance.LaunchCommand` after creation.

---

## Failure Modes

### FM1: Silent failure — triage appears to start but does nothing

**Symptoms**: ItemSession created, session shows as "running", no files written in `docs/tasks/<slug>/research/`.

**Root causes**:
- `Prompt` field empty or `AppendSystemPrompt` used instead of `Prompt` (Bug 1 — fixed)
- `-p` flag missing so Claude stays interactive (Bug 3 — fixed)
- MCP server URL not wired (EC6)
- tmux session creation failed silently

**How to detect**: Check `Instance.LaunchCommand` in session logs. It should contain both `-p` and the quoted prompt. Example: `claude --dangerously-skip-permissions -p --mcp-config '...' "You are a senior software architect..."`.

---

### FM2: Triage runs to completion but results never surface in UI

**Symptoms**: Research files exist on disk, session exited, but `ItemSession.triage_result` is empty and `BacklogItem.plan_artifacts_path` is unset.

**Root causes**:
- `submit_triage_result` not called by Claude (EC1)
- `submit_triage_result` called but failed (STAPLER_SESSION_UUID missing — R5, wrong item_id — EC2, MCP tool not registered)
- MCP HTTP request to stapler-squad failed after tool was called (network error, server restart during triage)

---

### FM3: Session stuck "running" permanently

**Symptoms**: ItemSession has no `ended_at`, triage button remains disabled, UI shows session as running.

**Root causes** (all fixed by Bug 2):
- Previously: `EndedAt` never written for non-work roles
- Remaining: If tmux session exits but the stapler-squad lifecycle event is never fired (e.g., the polling goroutine missed the exit), `EndedAt` is never written

**Safety net**: `ReconcileStuck` (periodic ticker) identifies items with `ended_at`-set sessions but item still in in_progress status. However, `ReconcileStuck` only handles in_progress→review transitions for work sessions; a stuck "idea" item with an open triage session is not covered.

---

### FM4: Double-trigger creates orphan triage session records

**Symptoms**: Multiple `ItemSession` records with `role=triage` for same item, some with no `ended_at`.

**Root causes**:
- `sessionStopper` not wired (R3) — orphan detection always succeeds, always creates a new session
- TOCTOU race (EC5)

**Impact**: Extra orphan records in the DB. The UI shows all item sessions, so orphan rows appear as extra triage sessions. `ListItemSessions` returns them and the UI renders all of them, which can be confusing.

---

### FM5: Wrong prompt format causes Claude to wait for input

**Symptoms**: Session starts, Claude displays interactive prompt (`>`) and waits.

**Root causes**:
- `-p` flag not prepended (Bug 3 — fixed)
- `-p` flag placed after the prompt argument (ordering issue — see Bug 3 critical note)
- `Prompt` field empty but `OneShot=true` — `-p` is added but with no prompt argument, Claude runs non-interactively with no task

**Detection**: Capture tmux pane content shortly after session start. If the first visible output contains `>` (Claude REPL prompt), the session is interactive.
