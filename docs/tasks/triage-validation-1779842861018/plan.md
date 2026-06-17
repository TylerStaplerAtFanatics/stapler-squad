# Plan -- Triage Pipeline Validation

## Executive Summary

The triage pipeline fix (19ef4431) repaired three bugs that caused triage sessions to start but never work:
prompt injected via --append-system-prompt instead of positional arg; endedAt never set for non-work roles;
OneShot flag never adding -p to the CLI. All three are now fixed. This validation confirms the full pipeline:
trigger -> Claude receives prompt -> executes research+planning -> calls submit_triage_result -> TriageReviewPanel appears.

## Implementation Approach

No new code is needed. Validation only:
1. Confirm unit tests pass
2. Run the new live e2e test (tests/e2e/triage-pipeline-validation.spec.ts)
3. Verify server logs confirm MCP submission

## Task Breakdown

### T1: Run unit tests (30m)
make build then go test ./server/services/... ./session/... ./server/mcp/...
Expected: all pass including TestTriggerTriage_DoubleTriggerGuard

### T2: Verify buildLaunchCommand (15m)
Review session/instance_tmux.go lines 41-43 (OneShot -> -p) and 44-46 (i.Prompt as positional arg)

### T3: Run live e2e test (6-10 min runtime)
TRIAGE_VALIDATION=true TEST_SERVER_URL=http://localhost:8543 npx playwright test triage-pipeline-validation.spec.ts
Success: data-testid=triage-review-panel appears with non-empty summary text

### T4: Verify server logs (10m)
Confirm: [TriggerTriage] spawned and [mcp:submit_triage_result] triage_result=...

## Dependencies and Blockers

- stapler-squad server must be running at http://localhost:8543
- ANTHROPIC_API_KEY or Bedrock credentials required
- No code changes; validation only
