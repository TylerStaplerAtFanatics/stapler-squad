# Implementation Plan: Triage Pipeline Validation

## Executive Summary

This item validates the triage pipeline fix from commit 19ef4431, which repaired three critical bugs
that completely broke the pipeline: prompt delivery (instructions went to system context only, not
Claude's user turn), session exit tracking (ended_at never set for triage sessions), and oneshot
behavior (Claude stayed interactive instead of exiting). The validation approach is to run the
existing E2E test (triage-pipeline-validation.spec.ts) against a live server to confirm Claude
receives a prompt and submits triage results end-to-end. No new code is required.

## Implementation Approach

This is a **run-and-verify task**. The deliverable is evidence that the pipeline executes correctly.

### What Was Fixed

1. Prompt delivery (backlog_service.go:1162): Prompt now passed as positional Prompt field
   (claude -p PROMPT) instead of AppendSystemPrompt (system context only).
   
2. Session exit tracking (backlog_lifecycle.go:109-113): UpdateItemSessionEnded now called
   before the work-role guard, so triage sessions record ended_at on exit.
   
3. OneShot mode (instance_tmux.go:46-48): -p flag now added when OneShot=true, so Claude
   runs in non-interactive mode and exits after completing work.

### Validation Steps

1. Build and install: `make install-service`
2. Start test server: `STAPLER_SQUAD_USE_CONTROL_MODE=false STAPLER_SQUAD_INSTANCE=e2e-local ./stapler-squad --tmux-keep-server &`
3. Run E2E: `TRIAGE_VALIDATION=true cd tests/e2e && npx playwright test triage-pipeline-validation.spec.ts`
4. Confirm pass (within 6 minutes): triage-review-panel appears with summary text

### What the Test Verifies

- TriggerTriage spawns a session (loading indicator appears)
- Claude receives the prompt (session transitions to active)
- Claude calls submit_triage_result (review panel appears)
- Session exits cleanly (ended_at set, no orphan)
- Summary text is present and non-empty (>10 chars)

## Task Breakdown

| Task | Estimate | Category |
|------|----------|----------|
| Build latest server with fix | 5m | infra |
| Start test server instance | 2m | infra |
| Run triage-pipeline-validation.spec.ts | 10m | test |
| Verify loading indicator appears | 1m | test |
| Verify review panel appears with summary | 1m | test |
| Document pass/fail result | 5m | docs |

Total: ~25 minutes (dominated by actual Claude triage execution time)

## Dependencies and Blockers

- Requires: Live stapler-squad server with MCP endpoint accessible
- Requires: Valid Claude API key for triage session to execute
- Requires: TRIAGE_VALIDATION=true env var (test is skipped otherwise)
- Risk: Claude API rate limits may cause session to hang
- Risk: MCP HTTP endpoint must be reachable from spawned tmux session

## Key Code Locations

- Prompt delivery fix: session/instance_tmux.go:46-48 and server/services/backlog_service.go:1162
- EndedAt fix: session/backlog_lifecycle.go:109-113
- E2E test: tests/e2e/triage-pipeline-validation.spec.ts
