# Plan: Validate Triage Pipeline Fix

## Executive Summary

The triage pipeline was broken by three coordinated bugs (wrong prompt injection field, missing `EndedAt` recording, and ignored oneshot `-p` flag), all fixed in commit `19ef4431`. Validation requires an end-to-end test that confirms Claude actually receives the injected prompt, autonomously calls `submit_triage_result`, and the UI reflects the completed triage — without any additional fixes to source code.

## Implementation Approach

This is a **validation-only** task. No source code modifications. The goal is to confirm the fix works by verifying the pipeline delivers a prompt to Claude, Claude submits a triage result, and the system records it correctly.

Validation has two levels:
1. **Unit/integration level** — assert the three specific fix points at the code level (command construction, lifecycle ordering, oneshot flag)
2. **E2E level** — confirm the full pipeline works against a live server using Playwright

The e2e spec `tests/e2e/triage-pipeline-validation.spec.ts` already exists (untracked in git status) and appears to be the artifact of a previous triage attempt. Review it, confirm it covers the acceptance criteria, and run it.

## Task Breakdown

| # | Task | Estimate | Category |
|---|------|----------|----------|
| 1 | Review existing `triage-pipeline-validation.spec.ts` for completeness | 15m | test |
| 2 | Verify unit test coverage of `-p` flag injection in `instance_tmux.go` | 20m | test |
| 3 | Verify unit test coverage of `EndedAt` set for triage role in `backlog_lifecycle.go` | 20m | test |
| 4 | Verify unit test coverage of prompt as positional arg (not `--append-system-prompt`) | 20m | test |
| 5 | Run `make build && make test` to confirm all unit tests pass | 10m | test |
| 6 | Start test server: `STAPLER_SQUAD_INSTANCE=e2e-local` with `--tmux-keep-server` | 10m | infra |
| 7 | Run `triage-pipeline-validation.spec.ts` against live server | 30m | test |
| 8 | Confirm triage review panel appears in UI with tasks/summary populated | 10m | test |

**Total estimate**: ~2h 15m

## Dependencies and Blockers

- **Live server required**: E2E test needs a running stapler-squad instance with a real Claude binary
- **Claude binary must be authenticated**: Triage session will call Claude, which must have valid credentials
- **MCP server must be wired**: The HTTP MCP endpoint must be reachable from the spawned tmux session
- **Real backlog item needed**: The e2e test must create or use an item with `repo_path` pointing to a real git repo

## Key Files

| File | Role |
|------|------|
| `session/instance_tmux.go:46-51` | OneShot `-p` flag + prompt positional arg injection |
| `session/backlog_lifecycle.go:97-170` | Session exit handler — `EndedAt` recording order |
| `server/services/backlog_service.go:1056-1259` | `TriggerTriage` handler + prompt builder |
| `server/mcp/tools_backlog.go:402-536` | `submit_triage_result` MCP tool |
| `tests/e2e/triage-pipeline-validation.spec.ts` | Existing e2e validation spec |
