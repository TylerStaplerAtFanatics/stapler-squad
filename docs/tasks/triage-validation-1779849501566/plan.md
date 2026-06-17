# Plan: Validate Triage Pipeline Fix

## Executive Summary

The triage pipeline was broken by three bugs: prompt injection via `--append-system-prompt` (no user turn), missing `-p` flag (Claude never exited), and `ended_at` never written for non-work sessions. All three were fixed in commit `19ef4431`. This validation item confirms the fixes are working end-to-end by observing Claude successfully receiving a prompt and calling `submit_triage_result`.

The fact that this triage session is running and will submit results IS the validation. No code changes are required — this is an observational/smoke test item.

## Implementation Approach

This item requires no code changes. Validation is achieved by:
1. Observing this triage session complete successfully (Claude received prompt, submits results)
2. Verifying the three fixed code paths are present in the current codebase
3. Confirming `ended_at` is recorded after session exit
4. Optionally running the e2e test suite to cover the triage pipeline

## Task Breakdown

| Task | Estimate | Category |
|------|----------|----------|
| Verify prompt injection fix in `instance_tmux.go` (Prompt field → `-p` positional arg) | 15m | backend |
| Verify `-p` flag present when `OneShot=true` in `buildLaunchCommand` | 15m | backend |
| Verify `UpdateItemSessionEnded` called before role guard in `backlog_lifecycle.go` | 15m | backend |
| Confirm `STAPLER_SESSION_UUID` propagated to tmux env and MCP header | 15m | backend |
| Run existing e2e tests to catch regressions | 30m | test |
| Write targeted integration test for triage prompt → submit flow | 1h | test |

**Total estimated effort: ~2.5h** (mostly verification + one new test)

## Dependencies and Blockers

- No blockers. All fixes are already in `main` (commit `19ef4431`).
- A running stapler-squad instance is needed for integration testing.
- The e2e Playwright suite at `tests/e2e/` covers session lifecycle but may not cover triage-specific flow.

## Key Files

- `session/instance_tmux.go` — `buildLaunchCommand()`, `-p` flag injection
- `server/services/backlog_service.go` — `TriggerTriage()`, prompt construction
- `server/services/backlog_lifecycle.go` — `onSessionExited()`, `UpdateItemSessionEnded`
- `server/services/backlog_mcp.go` — `submitTriageResult` handler, role validation
