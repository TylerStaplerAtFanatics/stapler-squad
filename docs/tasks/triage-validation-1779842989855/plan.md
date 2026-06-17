# Plan: Validate Triage Pipeline Fix

## Executive Summary

Three critical bugs were fixed in commit `19ef4431` — prompt injection (AppendSystemPrompt → Prompt), EndedAt never set for non-work sessions, and OneShot flag silently ignored. This task validates all three fixes are correct and regression-proof: via the existing live e2e test (`triage-pipeline-validation.spec.ts`) plus unit tests for each fix that do not require a real Claude session.

## Implementation Approach

The e2e test already exists and covers the full happy path end-to-end. The gap is fast, deterministic unit tests that directly exercise each of the three fixed code paths. These catch regressions without a 5-minute Claude run.

### Fix 1: Prompt injection (`instance_tmux.go`)
Verify `buildLaunchCommand` uses `Prompt` (positional arg) not `AppendSystemPrompt` when `OneShot=true`.

### Fix 2: EndedAt symmetry (`backlog_lifecycle.go`)
Verify `onSessionExited` records `EndedAt` for triage sessions (role ≠ work) before the role guard.

### Fix 3: OneShot → `-p` flag (`instance_tmux.go`)
Verify `buildLaunchCommand` appends `-p` when `OneShot=true` and program contains `claude`.

## Task Breakdown

| # | Task | Estimate | Category |
|---|------|----------|----------|
| 1 | Run live e2e test with `TRIAGE_VALIDATION=true` and document pass/fail | 30m | test |
| 2 | Add unit test: `buildLaunchCommand` includes `-p` when `OneShot=true` | 30m | test |
| 3 | Add unit test: `buildLaunchCommand` uses positional `Prompt` (not `--append-system-prompt`) | 30m | test |
| 4 | Add unit test: `onSessionExited` calls `UpdateItemSessionEnded` for triage role before role guard | 45m | test |
| 5 | Add unit test: `onSessionExited` does NOT call `UpdateItemSessionEnded` twice for work role | 20m | test |
| 6 | Run `make quick-check` to confirm all tests pass | 10m | test |

## Dependencies and Blockers

- Requires a live stapler-squad server + real Claude API key for the e2e test
- Unit tests are self-contained (no external deps)
- `make build` must succeed before `go test` (proto generation)

## Triage-Run Observation (2026-05-26)

This triage was executed interactively (not via the pipeline) — `STAPLER_SESSION_UUID` was not set, causing the `submit_triage_result` MCP tool call to fail with `PERMISSION_DENIED`. Code inspection confirms all three fixes are in place in `session/instance_tmux.go` (lines 41-46). The inability to call the MCP tool is consistent with running outside the pipeline, not a regression. To fully validate end-to-end, `TriggerTriage` must spawn a properly-configured tmux session.
