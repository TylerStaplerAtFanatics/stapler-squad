# Plan — Triage Pipeline Validation

## Executive Summary

This item validates that the triage pipeline fix (commit `19ef4431`) is working correctly end-to-end: Claude receives the triage prompt as a positional CLI argument (not system context), executes the 5-step triage workflow, and calls `submit_triage_result` to persist findings. The fix addressed three co-located bugs — prompt injection field, `EndedAt` recording for all session roles, and the OneShot `-p` flag — all of which are observable through unit tests and the live pipeline behavior.

The validation is inherently self-referential: this item's own triage session IS the test. If you are reading this plan, the pipeline is working.

## Implementation Approach

No code changes are required. This is a validation/smoke-test item. The approach is:

1. Confirm the three fix points hold in source (read-only audit of the key files)
2. Confirm unit test coverage exists for each fix point
3. Confirm the e2e test file exists and covers the full pipeline
4. Observe that this triage session itself completed successfully (meta-validation)

## Task Breakdown

| Task | Estimate | Category |
|------|---------|---------|
| Audit `instance_tmux.go` lines 41-53: OneShot `-p` flag injection present | 15m | test |
| Audit `backlog_lifecycle.go` lines 106-113: `UpdateItemSessionEnded()` before role guard | 15m | test |
| Audit `backlog_service.go`: prompt passed to `Instance.Prompt` not `AppendSystemPrompt` | 15m | test |
| Verify `backlog_lifecycle_test.go` covers `EndedAt` for triage/review roles | 20m | test |
| Verify `tools_backlog_test.go` covers `submitTriageResult` happy path | 20m | test |
| Verify `tests/e2e/triage-pipeline-validation.spec.ts` exists and has correct assertions | 20m | test |
| Run `make build && make test` to confirm no regressions | 15m | test |

**Total estimate**: ~2h

## Dependencies and Blockers

- No blockers — this is a read/validate task with no code changes
- Depends on: the pipeline fix commit `19ef4431` having been merged to main
- Depends on: `make test` passing (current `instance_tmux.go` has unstaged changes per git status)

## Key Fix Points to Verify

```
Fix 1: session/instance_tmux.go:46-47
  if i.OneShot && strings.Contains(program, "claude") → append -p

Fix 2: session/backlog_lifecycle.go:106-113
  UpdateItemSessionEnded() BEFORE role guard for non-work roles

Fix 3: server/services/backlog_service.go + session_service.go
  CreateDirectorySession prompt param → Instance.Prompt → positional CLI arg
```

## Known Risks

- `submit_triage_result` errors logged but not returned to Claude (silent failures)
- No triage session timeout — hung sessions block re-trigger
- Program name detection for OneShot uses substring match (fragile to renames)

See `research/pitfalls.md` for full risk inventory.
