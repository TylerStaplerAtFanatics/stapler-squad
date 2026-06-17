# Validation Plan -- Triage Pipeline Validation

## Acceptance Criteria Mapping

The backlog item description is: Validate the triage pipeline fix: Claude should receive a prompt and submit results.

### AC1: Claude receives a triage prompt

Test: e2e triage-pipeline-validation.spec.ts step 4
- Trigger triage via UI
- Confirm loading indicator / trigger button hidden within 30s
- Indirect evidence: if prompt injection was still broken, session would exit in <1s with no work done;
  a multi-minute execution confirms Claude is processing the prompt

Unit test coverage:
- server/services/backlog_service_test.go: TriggerTriage spawns session with correct prompt field
- session/instance_tmux.go test (manual review): Prompt set -> positional CLI arg

### AC2: Claude submits triage results via submit_triage_result MCP tool

Test: e2e triage-pipeline-validation.spec.ts step 5
- Wait for data-testid=triage-review-panel to appear (up to 5 min)
- The panel only appears when: (a) endedAt is set on the triage ItemSession AND (b) triage_result JSON is present
- Both conditions are only met if submit_triage_result was called successfully

Server log test:
- [mcp:submit_triage_result] session=<uuid> item=<uuid> triage_result=... must appear in logs

Unit test coverage:
- server/mcp/tools_backlog_test.go: submitTriageResult persists result and fires notification

## Edge Cases and Error Scenarios

### MCP server unreachable
Expected: Claude exits (OneShot), no triage_result in DB, no TriageReviewPanel
Test: not covered by e2e (would require stopping MCP server mid-session)

### Triage called on item with no repo_path
Expected: CodeFailedPrecondition returned immediately
Test: unit test in backlog_service_test.go

### Re-trigger while triage is live
Expected: CodeAlreadyExists returned (if session is genuinely live)
Test: TestTriggerTriage_DoubleTriggerGuard (fixed in 19ef4431)

### Re-trigger on ready item (session is dead)
Expected: item reset to idea, stale session tombstoned, new session spawned
Test: unit test in backlog_service_test.go

### Subagent failures
Expected: Claude submits partial research, submit_triage_result still called
Test: not directly testable (Anthropic API dependency)

## Test Execution Commands

1. Unit tests (no server needed):
   make build
   go test ./server/services/... ./session/... ./server/mcp/...

2. Live e2e validation (server required):
   make install-service
   cd tests/e2e
   TRIAGE_VALIDATION=true TEST_SERVER_URL=http://localhost:8543 npx playwright test triage-pipeline-validation.spec.ts --project=chromium

3. Log verification:
   tail -f ~/.stapler-squad/logs/stapler-squad.log | grep -E "TriggerTriage|submit_triage"
