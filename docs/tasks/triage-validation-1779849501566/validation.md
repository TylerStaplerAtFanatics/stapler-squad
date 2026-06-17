# Validation: Triage Pipeline Fix

## Acceptance Criteria

The triage pipeline fix is valid when:
1. Claude receives the triage prompt (not silently waiting)
2. Claude calls `submit_triage_result` with a valid result
3. `ended_at` is recorded on the `ItemSession` after Claude exits

## Test Plan

### TC-1: Prompt Reception (maps to fix #1 — prompt injection)
**Approach:** Observational — this session is the test. If Claude wrote research files and is calling `submit_triage_result`, it received the prompt.
**Pass condition:** `submit_triage_result` is called with a non-empty summary.
**Code path verified:** `instance_tmux.go` uses `Prompt` field → positional `-p "<prompt>"` arg, not `--append-system-prompt`.

### TC-2: Session Exit (maps to fix #2 — OneShot `-p` flag)
**Approach:** After `submit_triage_result` is called, the session process should exit.
**Pass condition:** Session status transitions to a terminal state; tmux pane exits.
**Code path verified:** `buildLaunchCommand` in `instance_tmux.go` prepends `-p` when `OneShot=true`.

### TC-3: EndedAt Written (maps to fix #3 — lifecycle guard)
**Approach:** Query DB or observe UI after session exits.
**Pass condition:** `ItemSession.ended_at` is non-null after session terminates.
**Code path verified:** `backlog_lifecycle.go` calls `UpdateItemSessionEnded` before `SessionRole != work` guard.

### TC-4: MCP Authentication Chain
**Approach:** `submit_triage_result` succeeds (not 401/403).
**Pass condition:** Tool call completes without auth error.
**Code path verified:** `X-Stapler-Session-UUID` header in `--mcp-config` → middleware → `callerSessionUUID` → role check `"triage"`.

## Edge Cases and Error Scenarios

| Scenario | Expected Behavior |
|----------|-------------------|
| Claude exits without calling `submit_triage_result` | `ended_at` is still written; item stays in `idea` state |
| `sessionStopper` is nil (double-trigger guard disabled) | Second trigger creates a second triage session; no crash |
| Prompt contains special characters / shell metacharacters | Prompt is JSON-quoted in MCP config; positional arg is quoted by exec |
| Session UUID not in tmux env | MCP tool calls fail with 401; triage cannot complete |
| `submit_triage_result` called by non-triage role | Returns error; result not persisted |

## Regression Test Recommendation

Add a Go integration test in `server/services/` that:
1. Creates a backlog item
2. Calls `TriggerTriage`
3. Asserts the spawned session has `OneShot=true` and the command contains `-p`
4. Simulates `submit_triage_result` call with valid session UUID
5. Asserts `ItemSession.triage_result` is populated and `ended_at` is set

This covers all three fixed bugs in a single reproducible test.
