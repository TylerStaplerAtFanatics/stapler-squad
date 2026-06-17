# Plan: Validate Triage Pipeline Fix

## Executive Summary

The triage pipeline had three critical bugs fixed in commit 19ef4431: Claude was never receiving the prompt (passed via system context instead of positional arg), triage sessions never recorded their exit time, and the OneShot flag was not appending `-p` to the Claude CLI invocation. Validation requires confirming all three fixes hold end-to-end: Claude receives the prompt, executes autonomously, calls `submit_triage_result`, and the session exits cleanly with results persisted.

## Implementation Approach

This is a **validation task, not an implementation task**. No source code changes are needed. The goal is to verify the fix is correct and complete by:

1. Running existing unit tests that cover the fixed code paths
2. Running or reviewing the existing e2e test (`triage-pipeline-validation.spec.ts`)
3. Manually triggering a triage session and observing the full pipeline
4. Confirming the three fix areas hold under the test suite

## Task Breakdown

| # | Task | Estimate | Category |
|---|------|----------|----------|
| 1 | Run unit test suite covering triage pipeline | 10m | test |
| 2 | Review `instance_tmux.go` OneShot fix (lines 41-43) and confirm `-p` flag logic | 15m | backend |
| 3 | Review `backlog_lifecycle.go` EndedAt fix (line 110-113) and confirm all roles record exit | 15m | backend |
| 4 | Review `backlog_service.go` prompt injection — confirm `prompt` param reaches positional arg | 15m | backend |
| 5 | Run `go test ./server/services/ -run TestTriggerTriage` | 10m | test |
| 6 | Run `go test ./session/ -run TestBacklogLifecycle` | 10m | test |
| 7 | Review `triage-pipeline-validation.spec.ts` for completeness | 20m | test |
| 8 | Run e2e test against live server (requires running instance) | 30m | test |
| 9 | Confirm `submit_triage_result` MCP tool returns success and TriageResult persists | 20m | backend |

**Total estimate: ~2.5 hours**

## Dependencies and Blockers

- Running instance of stapler-squad for e2e test (`make install-service`)
- A backlog item in "idea" or "ready" status with a valid `repo_path` set
- E2e test requires server running on `localhost:8543` (test port `8544`)

## Acceptance Criteria Mapping

1. **Claude receives the prompt** → verified by: session produces output / artifact files written to `docs/tasks/`
2. **Claude calls submit_triage_result** → verified by: `[data-testid="triage-review-panel"]` visible in UI; TriageResult non-null in DB
3. **Session exits cleanly** → verified by: `ItemSession.EndedAt` set; UI shows session as ended
4. **OneShot works** → verified by: no hanging tmux session after completion
5. **Double-trigger guard** → verified by: `TestTriggerTriage_DoubleTriggerGuard` passes

## Known Risks

- E2e test has a 5-minute timeout — slow CI environments may flake
- `submit_triage_result` failure leaves item with EndedAt but null TriageResult (silent failure)
- `AppendSystemPrompt` dead field in `Instance` is a future regression risk (no active callers, but confusing)
