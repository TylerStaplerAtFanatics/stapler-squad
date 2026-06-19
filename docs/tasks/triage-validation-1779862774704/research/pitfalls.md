# Triage Pipeline Pitfalls: Critical Bugs Fixed in Commit 19ef4431

## Three Critical Bugs (All Fixed)

### 1. Prompt Injection
**Problem:** Prompt passed via `--append-system-prompt` (system context). Claude's OneShot mode waits for user message. Result: Claude idle, no work.
**Fix:** Moved to positional CLI arg (`claude "<prompt>"`). Claude gets prompt as first user turn.
**Files:** server/services/session_service.go:540, session/instance_tmux.go:49-51

### 2. EndedAt Never Set for Triage
**Problem:** `onSessionExited` had early return before `UpdateItemSessionEnded()` for non-work roles. Triage/review sessions never recorded exit time.
**Fix:** Moved `UpdateItemSessionEnded()` before role guard. All roles record EndedAt.
**Files:** session/backlog_lifecycle.go:109-118

### 3. OneShot Flag Ignored
**Problem:** `OneShot=true` set but `buildLaunchCommand()` never added `-p` flag. Claude stayed interactive.
**Fix:** Added OneShot check in buildLaunchCommand. Now adds ` -p` when OneShot true.
**Files:** session/instance_tmux.go:46-48

## Remaining Active Risks

### High Risk: MCP Server Unavailability
- If MCPServerURL unreachable, `submit_triage_result` fails silently
- Triage completes, result never persists to database
- UI shows empty review panel
- **Mitigation:** Verify MCP server healthy before triage

### Medium Risk: Double-Trigger Race
- Rapid re-trigger may orphan session before StartedAt is recorded
- Guard detects via `neverStarted := is.StartedAt == nil`
- **Mitigation:** Wait 5-10 seconds between triggers

### Medium Risk: Subagent Failures
- If subagent spawner unavailable, research is incomplete
- Claude still completes with degraded plan
- **Mitigation:** Acceptable trade-off. Weak plan better than no plan.

### Medium Risk: Timeout (E2E Tests)
- Large codebases may take 5+ minutes
- E2E test has 6 min timeout (reasonable)
- **Mitigation:** Manual verification if test times out

### Low Risk: Session Exit Race
- Manual kill vs lifecycle goroutine both try to set EndedAt
- EndedAt is idempotent (no functional issue)
- **Mitigation:** Let sessions exit naturally

### Low Risk: Artifact Dir Creation Fails
- If RepoPath unwritable or docs/tasks missing
- TriggerTriage fails with CodeInternal error
- **Mitigation:** Verify RepoPath is writable before trigger

## Verification Checklist

1. **Pre-Trigger:** MCP server running, session manager healthy, RepoPath writable
2. **After Trigger:** Logs show `[TriggerTriage] spawned...`, ItemSession created with SessionUUID
3. **Session Starts:** Logs show `UpdateItemSessionStarted`, ItemSession.StartedAt set
4. **Claude Works:** Research files appear in docs/tasks/slug/research/
5. **Completes:** Logs show `[mcp:submit_triage_result]`, ItemSession.EndedAt set
6. **Result Persists:** triage-review-panel appears, ItemSession.TriageResult contains JSON

## E2E Test

Location: tests/e2e/triage-pipeline-validation.spec.ts
Command: `TRIAGE_VALIDATION=true TRIAGE_REPO_PATH=/path npx playwright test triage-pipeline-validation.spec.ts`
Timeout: 6 minutes (real Claude triage time)

Test verifies:
- Session starts (trigger button hidden or loading indicator visible)
- triage-review-panel appears (session completed with results)
- Summary text is non-empty

## Debug

**Session started but no work:**
- Check logs: `grep TriggerTriage | grep submit_triage_result`
- If no submit_triage_result: Claude crashed or MCP failed

**Triage completes, no review panel:**
- Check database: `SELECT triage_result FROM item_sessions WHERE ...`
- If triage_result IS NULL: MCP server was unavailable

**EndedAt never set (pre-19ef4431 bug):**
- Verify deployed: `git log | grep 19ef4431`
- Check: `SELECT ended_at FROM item_sessions WHERE session_role='triage'`

## Test Coverage

- Unit: TestTriggerTriage_DoubleTriggerGuard (orphan guard)
- Integration: TestBacklogIntegration_IT006 (confirms EndedAt for all roles)
- E2E: triage-pipeline-validation.spec.ts (full end-to-end)
