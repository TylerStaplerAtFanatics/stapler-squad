# Validation: Triage Pipeline Fix

## Test Plan

### AC Coverage Matrix

| Acceptance Criterion | Test | Type | File |
|---|---|---|---|
| Claude receives a prompt (not just system context) | `TestBuildLaunchCommand_PositionalPrompt_WhenOneShotTrue` | unit | `session/instance_tmux_test.go` |
| Session exits automatically after work (oneshot) | `TestBuildLaunchCommand_AppendsNonInteractiveFlag_WhenOneShot` | unit | `session/instance_tmux_test.go` |
| EndedAt set for triage session on exit | `TestOnSessionExited_RecordsEndedAt_ForTriageRole` | unit | `session/backlog_lifecycle_test.go` |
| submit_triage_result persists results & closes session | `e2e:triage-pipeline - triage starts, receives prompt, completes, shows review panel` | e2e | `tests/e2e/triage-pipeline-validation.spec.ts` |

---

## Unit Tests

### Fix 1 — OneShot appends `-p` flag

**File:** `session/instance_tmux_test.go`

```
TestBuildLaunchCommand_AppendsNonInteractiveFlag_WhenOneShot
  Given: Instance{OneShot: true, program: "claude"}
  When: buildLaunchCommand("") called
  Then: result contains " -p"
  
TestBuildLaunchCommand_NoNonInteractiveFlag_WhenNotOneShot
  Given: Instance{OneShot: false, program: "claude"}
  When: buildLaunchCommand("") called
  Then: result does NOT contain " -p"
  
TestBuildLaunchCommand_NoNonInteractiveFlag_ForNonClaudeProgram
  Given: Instance{OneShot: true, program: "aider"}
  When: buildLaunchCommand("") called
  Then: result does NOT contain " -p"
```

### Fix 2 — Prompt is positional, not system context

**File:** `session/instance_tmux_test.go`

```
TestBuildLaunchCommand_PositionalPrompt_WhenOneShotTrue
  Given: Instance{OneShot: true, Prompt: "do work", program: "claude"}
  When: buildLaunchCommand("") called
  Then: result contains `"do work"` quoted as positional arg
  And:  result does NOT contain "--append-system-prompt"

TestBuildLaunchCommand_NoPrompt_WhenResumingExistingSession
  Given: Instance{Prompt: "do work", program: "claude"}, claudeSessionID != ""
  When: buildLaunchCommand("abc123") called
  Then: result does NOT contain "do work" (prompt skipped on resume)
```

### Fix 3 — EndedAt set for all session roles

**File:** `session/backlog_lifecycle_test.go`

```
TestOnSessionExited_RecordsEndedAt_ForTriageRole
  Given: ItemSession{SessionRole: "triage", EndedAt: nil}
  When: onSessionExited fires
  Then: storage.UpdateItemSessionEnded called with non-zero time
  And:  no status transition attempted (no SpawnSessionFromItem call)

TestOnSessionExited_RecordsEndedAt_ForReviewRole  
  Given: ItemSession{SessionRole: "review", EndedAt: nil}
  When: onSessionExited fires
  Then: storage.UpdateItemSessionEnded called
  And:  no status transition attempted

TestOnSessionExited_RecordsEndedAt_ForWorkRole
  Given: ItemSession{SessionRole: "work", EndedAt: nil}
  When: onSessionExited fires
  Then: storage.UpdateItemSessionEnded called (before role-specific logic)
  And:  status transition logic executed
```

---

## E2E Test

**File:** `tests/e2e/triage-pipeline-validation.spec.ts`  
**Run:** `TRIAGE_VALIDATION=true TRIAGE_REPO_PATH=/path/to/repo npx playwright test triage-pipeline-validation.spec.ts`

### Happy Path (`e2e:triage-pipeline`)
1. Create item with `repo_path` set
2. Click "Trigger triage" button
3. **Assert:** loading indicator appears within 30s (session started, prompt received)
4. **Assert:** `[data-testid="triage-review-panel"]` visible within 5min (submit_triage_result called)
5. **Assert:** summary text > 10 chars (triage_result JSON round-tripped correctly)

---

## Edge Cases

| Scenario | Expected Behavior | Where to Test |
|---|---|---|
| Trigger triage with no `repo_path` | `FailedPrecondition` error | unit: `TestTriggerTriage_NoRepoPath` (existing) |
| Re-trigger while triage session is live | `CodeAlreadyExists` | unit: `TestTriggerTriage_DoubleTriggerGuard` (existing) |
| Re-trigger after session orphaned | Succeeds, orphan tombstoned | unit: extend `TestTriggerTriage_DoubleTriggerGuard` |
| `submit_triage_result` from non-triage role | `PermissionDenied` | unit: mock wrong role |
| `submit_triage_result` with >12 tasks | Tasks truncated to 12 | unit: verify length cap |
| Session exits without calling `submit_triage_result` | EndedAt set, TriageResult nil, item stays IDEA | unit: `TestOnSessionExited_RecordsEndedAt_ForTriageRole` |

---

## Pass Criteria

- All new unit tests pass: `go test ./session/... ./server/services/...`
- E2E test passes with `TRIAGE_VALIDATION=true` (real Claude run)
- `make quick-check` exits 0 (build + lint + tests)
- No regressions: existing `TestTriggerTriage_*` tests still pass
