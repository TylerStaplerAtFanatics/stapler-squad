# Validation Plan: Triage Pipeline Fix

## Test Coverage Map

| Acceptance Criterion | Test(s) | Type | Status |
|---|---|---|---|
| Claude receives prompt as positional arg | `TestTriggerTriage*` + e2e artifact file check | unit + e2e | existing |
| Claude calls submit_triage_result | `TestSubmitTriageResult_PublishesNotificationOnSuccess` + e2e review panel | unit + e2e | existing |
| Session EndedAt recorded for all roles | `TestBacklogLifecycleListener_OnSessionExited_ReviewSession_NoTransition` | unit | existing (fixed) |
| OneShot adds `-p` flag | `triage-pipeline-validation.spec.ts` (implicit: session exits) | e2e | existing |
| Double-trigger guard | `TestTriggerTriage_DoubleTriggerGuard` | unit | existing (fixed) |
| Orphan cleanup (stale tmux killed) | `TestTriggerTriage_DoubleTriggerGuard` + manual | unit | existing |

## Test Cases

### TC-01: Prompt Injection via Positional Arg
**Goal**: Confirm Claude receives instructions and begins work without user input  
**Method**: Inspect `buildLaunchCommand()` in `instance_tmux.go` — verify `i.Prompt` appended as quoted arg  
**Pass Condition**: `instance_tmux.go:44-46` adds `"<prompt>"` when `i.Prompt != "" && claudeSessionID == ""`  
**Automated**: No unit test for command building; covered implicitly by e2e (Claude produces output)

### TC-02: OneShot Adds `-p` Flag
**Goal**: Confirm triage sessions exit non-interactively after task completion  
**Method**: Inspect `instance_tmux.go:41-43`; run e2e test and verify session exits  
**Pass Condition**: `i.OneShot && strings.Contains(program, "claude")` → `program = program + " -p"`  
**Automated**: `triage-pipeline-validation.spec.ts` (implicit via session state)

### TC-03: EndedAt Recorded for Triage Role
**Goal**: Confirm `ItemSession.EndedAt` is set when triage session exits  
**Method**: `go test ./session/ -run TestBacklogLifecycleListener_OnSessionExited`  
**Pass Condition**: Test passes; `UpdateItemSessionEnded` called before role check  
**Automated**: Unit test in `backlog_lifecycle_test.go`

### TC-04: submit_triage_result Persists TriageResult
**Goal**: Confirm MCP tool stores summary, suggestions, tasks on ItemSession  
**Method**: `go test ./server/mcp/ -run TestSubmitTriageResult`  
**Pass Condition**: TriageResult non-null; plan_artifacts_path updated; notification published  
**Automated**: `TestSubmitTriageResult_PublishesNotificationOnSuccess`

### TC-05: Double-Trigger Guard Blocks Concurrent Triage
**Goal**: Confirm second TriggerTriage call returns AlreadyExists while first is live  
**Method**: `go test ./server/services/ -run TestTriggerTriage_DoubleTriggerGuard`  
**Pass Condition**: Test passes; mock IsSessionLive returns true → CodeAlreadyExists  
**Automated**: `backlog_service_test.go:368-402`

### TC-06: End-to-End Pipeline (Full Integration)
**Goal**: Confirm full flow from trigger → prompt → execution → submit → exit  
**Method**: Run `triage-pipeline-validation.spec.ts` against live server  
**Pass Condition**:
  1. Triage session created (UI shows loading indicator / trigger hidden)
  2. Within 5 minutes: `[data-testid="triage-review-panel"]` visible
  3. TriageResult has non-empty summary
  4. Session EndedAt is set (session shows as ended in UI)
**Automated**: Playwright e2e test

## Edge Cases

| Scenario | Expected Behavior | Test Coverage |
|---|---|---|
| Item has empty AcceptanceCriteria | Triage proceeds; Claude may include clarifying questions in suggestions | Manual only |
| submit_triage_result called with invalid role | `CodePermissionDenied` returned | `TestSubmitTriageResult_*` |
| Session exits before calling submit_triage_result | EndedAt set, TriageResult=NULL; item stays in current status | `IT-006` covers exit-without-transition |
| TriggerTriage on item already in "ready" status | `CodeFailedPrecondition` | `TestTriggerTriage_*` |
| Stale tmux session exists with same title | Killed by `KillTmuxSessionByTitle` before new session spawned | `TestTriggerTriage_DoubleTriggerGuard` |
| Claude context overflow mid-triage | Session exits; reconciliation catches stuck item after timeout | Manual only |

## Verification Commands

```bash
# Unit tests for triage pipeline
make build && go test ./server/services/ -run TestTriggerTriage -v
go test ./session/ -run TestBacklogLifecycle -v
go test ./server/mcp/ -run TestSubmitTriageResult -v

# Full test suite
make test

# E2e test (requires running server)
make install-service
cd tests/e2e && npx playwright test triage-pipeline-validation.spec.ts
```

## Pass/Fail Criteria

**PASS**: All unit tests green; e2e test completes within timeout with review panel visible  
**FAIL**: Any unit test regression; e2e timeout; TriageResult null after session exits cleanly; EndedAt not set
