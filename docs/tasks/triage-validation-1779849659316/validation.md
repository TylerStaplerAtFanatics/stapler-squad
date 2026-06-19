# Validation Plan: Triage Pipeline Fix

## Acceptance Criteria Mapping

The backlog item has one acceptance criterion: **Claude should receive a prompt and submit results.**

This decomposes into three verifiable sub-conditions matching the three bugs that were fixed:

| Sub-condition | Test type | Test location |
|--------------|-----------|---------------|
| Claude receives the prompt (not system-appended) | Unit | `TestBuildLaunchCommand_PromptIsPositionalArg` |
| Session records `EndedAt` when triage session exits | Unit/integration | `TestOnSessionExited_TriageRoleRecordsEndedAt` |
| OneShot session gets `-p` flag | Unit | `TestBuildLaunchCommand_OneShotAddsPFlag` |
| Full pipeline: trigger → submit → UI reflects result | E2E | `triage-pipeline-validation.spec.ts` |

## Specific Tests

### Unit: Prompt is positional arg (not --append-system-prompt)

**Assertion**: When `Instance.Prompt` is set and `OneShot=false`, `buildLaunchCommand()` includes the prompt as a quoted positional argument after the program name — not as `--append-system-prompt <value>`.

```go
// Expected output contains: claude <mcp-flags> "triage prompt text"
// Must NOT contain: --append-system-prompt
```

**File**: `session/instance_tmux_test.go` or `session/backlog_service_test.go`

---

### Unit: OneShot injects `-p` flag

**Assertion**: When `Instance.OneShot=true` and program contains "claude", `buildLaunchCommand()` appends `-p` to the program string.

```go
// Expected: "claude -p"
// Not: "claude" (interactive mode)
```

**File**: `session/instance_tmux_test.go`

---

### Unit/Integration: EndedAt recorded for triage role

**Assertion**: When a session with `SessionRole=triage` exits, `onSessionExited()` calls `UpdateItemSessionEnded()` and the `ItemSession.EndedAt` field is non-nil. The item does NOT transition to a new status (triage exit is informational only).

```go
// After onSessionExited fires for a triage-role session:
// itemSession.EndedAt != nil ✓
// item.Status == "idea" (unchanged) ✓
```

**File**: `session/backlog_integration_test.go`

---

### E2E: Full triage pipeline

**File**: `tests/e2e/triage-pipeline-validation.spec.ts`

**Steps**:
1. Create a backlog item with `repo_path` pointing to a real git repo
2. Call `TriggerTriage` on the item
3. Wait for triage session to appear (status: running)
4. Wait for triage session to exit (status: ended) — timeout: 5 minutes
5. Assert `ItemSession.EndedAt` is non-nil
6. Assert triage review panel appears in UI
7. Assert panel contains non-empty `summary`
8. Assert panel contains at least one task in the task list

**Timeout**: 6 minutes total (Claude triage takes ~2-4 minutes in practice)

---

## Edge Cases and Error Scenarios

### Edge Case: Re-trigger while session is running
- **Scenario**: User triggers triage twice in quick succession
- **Expected**: Second trigger detects live session (orphan guard), returns error or no-ops
- **Test**: `TestTriggerTriage_DoubleTriggerGuard` (already exists, fixed in this commit)

### Edge Case: Re-trigger after session exits but before result submitted
- **Scenario**: Claude exited but never called `submit_triage_result` (crash, network error)
- **Expected**: Orphan guard treats ended session as orphaned; new session spawns; new prompt injected
- **Validation**: Check that `EndedAt != nil` causes session to be treated as non-live in orphan guard

### Edge Case: `submit_triage_result` called without STAPLER_SESSION_UUID
- **Scenario**: Session spawned without UUID (config issue)
- **Expected**: MCP handler returns `ErrPermissionDenied`; triage result not stored
- **Validation**: Ensure `buildLaunchCommand()` always sets UUID env var and header when UUID is non-empty

### Error Scenario: MCP server unreachable
- **Scenario**: Stapler-squad server is down but tmux session still runs
- **Expected**: Claude fails to discover MCP tools; session completes writing files but cannot call `submit_triage_result`; session exits; item stays in `idea` without triage result
- **Validation**: Confirm item remains in `idea` (not stuck in unknown state) — operator can re-trigger

### Error Scenario: Repo path does not exist
- **Scenario**: `TriggerTriage` called on item where `repo_path` is invalid
- **Expected**: `TriggerTriage` returns error before spawning session
- **Validation**: Assert RPC returns `CodeInvalidArgument` or `CodeNotFound`

## Pass Criteria

The fix is validated when:
- [ ] All unit tests in `make test` pass
- [ ] `triage-pipeline-validation.spec.ts` passes end-to-end without modification
- [ ] The triage review panel in the UI shows the submitted summary and tasks
- [ ] No `ItemSession` rows with `role=triage` and `ended_at=NULL` exist after the test run
