# Validation Plan: Triage Pipeline

## Acceptance Criteria Map

This item has one core acceptance criterion: Claude should receive a prompt and submit results.

### AC1: Claude receives a prompt
**Test**: triage-pipeline-validation.spec.ts - loading indicator appears after trigger
**Evidence**: TriageLoadingIndicator component renders (session is active, not idle)
**Pass condition**: Loading indicator visible within 30s of triggering triage

### AC2: Claude submits triage results via submit_triage_result MCP tool
**Test**: triage-pipeline-validation.spec.ts - triage-review-panel appears with summary text
**Evidence**: TriageReviewPanel renders with summary length > 10 characters
**Pass condition**: Review panel visible within 6 minutes; summary text non-empty

### AC3 (implicit): Session exits cleanly after submission
**Test**: Manual check - ItemSession.ended_at is set in DB after test passes
**Evidence**: UI no longer shows loading indicator; review panel is stable
**Pass condition**: Session not stuck "running" after review panel appears

## Test Plan

### Primary E2E Test

File: tests/e2e/triage-pipeline-validation.spec.ts
Gate: TRIAGE_VALIDATION=true env var required
Timeout: 360000ms (6 minutes)
Server: http://localhost:8543 (or TEST_SERVER_URL override)

Steps:
1. Navigate to /backlog
2. Click "New Item" button
3. Fill form: title + description + repo path (TRIAGE_REPO_PATH or stapler-squad root)
4. Submit (auto-triggers triage since skip_triage=false and repo_path is set)
5. Open item detail pane
6. Assert: loading indicator is visible (AC1 pass)
7. Wait up to 5 minutes for triage-review-panel to appear (AC2 pass)
8. Assert: summary text length > 10 chars

Run command:
  TRIAGE_VALIDATION=true rtk playwright test triage-pipeline-validation.spec.ts --config playwright.live.config.ts --timeout 360000

### Unit Test Verification

File: server/services/backlog_service_test.go
Run: go test ./server/services/ -run TestTriggerTriage

Key assertions:
- TriggerTriage calls CreateDirectorySession with oneShot=true
- TriggerTriage creates ItemSession with role=triage
- Double-trigger guard prevents concurrent sessions

### Regression Check

Files modified in 19ef4431:
- session/instance_tmux.go (oneshot -p flag)
- session/backlog_lifecycle.go (ended_at for non-work roles)
- server/services/backlog_service.go (Prompt vs AppendSystemPrompt)

Verify these are correct in the current HEAD:
  go build ./... && go test ./session/... ./server/services/...

## Error Scenarios

### Triage session hangs (no submit_triage_result called)
Detection: Test times out after 6 minutes with no review panel
Root cause: Claude received no prompt (regression) or API error
Action: Check tmux session for claude output, verify -p flag is present in process args

### Review panel appears but summary is empty
Detection: summary.length <= 10 assertion fails
Root cause: submit_triage_result called with empty summary
Action: Check ItemSession.triage_result in DB for actual JSON content

### Loading indicator never appears
Detection: TriageLoadingIndicator not found within 30s
Root cause: Session creation failed, or UI not polling correctly
Action: Check server logs at ~/.stapler-squad/logs/stapler-squad.log

### Session stuck "running" after review panel appears
Detection: Manual check -- ItemSession.ended_at is null
Root cause: Regression in backlog_lifecycle.go onSessionExited ended_at fix
Action: Verify UpdateItemSessionEnded call is before work-role guard
