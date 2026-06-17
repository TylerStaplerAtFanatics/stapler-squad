# Triage Pipeline Pitfalls & Known Risks

## Recent Fix Breakdown (Commit 19ef4431)

The triage pipeline suffered three critical bugs that prevented triages from completing:

### 1. Prompt Injection via Wrong Field

**Bug**: Triage prompts were passed to `AppendSystemPrompt` (→ `--append-system-prompt` CLI flag), which injects text into the system prompt. Claude waits for an explicit user message before responding. Without that message, Claude never started working.

**Root Cause**: Interface signature used generic `appendSystemPrompt` parameter name; confusion about `--append-system-prompt` vs initial prompt injection.

**Risk Going Forward**:
- Easy regression: if refactoring session creation, accidentally mapping prompt to `AppendSystemPrompt` again
- No compile-time guard: both are strings; only runtime behavior reveals the bug
- Solution: Clearly use `Prompt` field for initial user message; reserve `AppendSystemPrompt` for system-only context

### 2. Session EndedAt Never Set (Triage Sessions Stuck "Running")

**Bug**: `onSessionExited()` had an early guard returning for non-work roles (`triage` and `review`) before calling `UpdateItemSessionEnded()`. Result: triage sessions never recorded exit time; UI showed them as permanently "running" with no status progression.

**Root Cause**: Guard logic intended to prevent in_progress→review transitions on non-work sessions, but overly broad—it also skipped the mandatory record-keeping.

**Risk Going Forward**:
- Session lifecycle corruption: any session role (triage/review/work) exiting can silently fail to record end time
- UI gets stuck showing running sessions that are actually dead
- Reconciliation failures: subsequent re-trigger attempts misidentify orphaned vs. live sessions
- Solution: Always record EndedAt first, then apply role-specific guards for state transitions

### 3. OneShot Flag Ignored (Claude Stays Interactive)

**Bug**: `OneShot=true` was set on triage sessions but `buildLaunchCommand()` never added the `-p` (print/non-interactive) flag. Claude finished the task but stayed in interactive mode waiting for input, blocking session cleanup.

**Root Cause**: `-p` flag logic was missing; `OneShot` field existed but had no corresponding CLI emission.

**Risk Going Forward**:
- Silent hangs: sessions marked `OneShot=true` will not exit cleanly without the `-p` flag
- Resource leaks: tmux processes remain alive, blocking re-triggers (which fail on "session already exists")
- Worktree orphans: if server crashes before cleanup, worktree remains locked
- Solution: Enforce invariant that `OneShot=true` → `-p` flag is always added in `buildLaunchCommand()`

---

## Timing & Race Condition Risks

### Session Start Timing
- **StartedAt=NULL Gap**: Between `CreateItemSession()` (creates record) and `UpdateItemSessionStarted()` (records start time), a brief window exists where sessions look "never started." If server crashes in this window, the session appears orphaned forever.
- **Risk**: Re-trigger orphan detection treats unstarted sessions as safe to replace, but they may actually be running in tmux.
- **Mitigation**: Triage re-trigger now kills stale tmux sessions by title before spawning fresh ones, bypassing the orphan detection ambiguity.

### Prompt Delivery Race
- **MCP URL Timing**: MCPServerURL is set via `SetMCPServerURL()` during server startup. If called after session creation, MCP injection fails silently.
- **Risk**: Triage sessions spawn without MCP tool availability; `submit_triage_result` fails with "tool not found."
- **Mitigation**: Verify `SetMCPServerURL()` is called in server initialization order before `SessionService` methods are called.

---

## MCP Tool Availability Gaps

### MCP Config Injection
- **Mechanism**: `--mcp-config '{"stapler-squad":{"type":"http",...}}'` is built in `buildLaunchCommand()` and injected if `MCPServerURL != ""`.
- **Risk**: If `MCPServerURL` is empty or not propagated to `CreateDirectorySession()`, triage sessions lack tool access.
- **Silent Failure**: No error is raised; Claude simply cannot call `submit_triage_result`.

### Session Resume & MCP
- **Bug Window**: When resuming a session (via `--resume <uuid>`), the Prompt field is not re-injected, but MCP config is. If the resumed session was previously created without MCP, it stays MCP-less.
- **Risk**: Resuming a triage session after server restart drops the initial prompt context but keeps MCP tools alive.

---

## Error Handling Gaps

### No Validation of OneShot Status
- If `OneShot=false` by mistake, `-p` is not added, and sessions stay interactive indefinitely.
- No warning in logs; pipeline silently hangs.

### Orphan Detection False Positives
- `IsSessionLive()` depends on the in-memory `ReviewQueuePoller` state. After a server restart that kills the poller, all sessions appear "not live" even if their tmux processes are still running.
- The fix (killing tmux by title first) mitigates but doesn't eliminate the gap.

### Missing Transaction Boundary
- Between spawning the session and creating the `ItemSession` record, if the session starts working but crashes before record creation, the work is orphaned.
- Conversely, if `CreateItemSession` fails after session spawn, the tmux process remains as a zombie.

---

## Validation Checklist

1. Confirm `-p` flag is emitted when `OneShot=true`
2. Confirm `EndedAt` is set for all session roles on exit (not just work)
3. Confirm `Prompt` field is used for initial triage prompt (not `AppendSystemPrompt`)
4. Confirm `MCPServerURL` is non-empty and propagated through `CreateDirectorySession()`
5. Confirm `SetMCPServerURL()` is called before any session creation
6. Confirm test orphan guard covers: live sessions, unstarted sessions, and sessions with stale tmux names
7. Confirm triage exit does not block on role guard (record EndedAt first, guard transitions after)
