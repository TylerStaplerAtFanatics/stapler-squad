# Triage Pipeline Risks and Pitfalls

## Executive Summary

The triage pipeline (commit 19ef4431) fixed three critical bugs that caused triage sessions to start but never complete. This document identifies residual risks, edge cases, and gaps that could still cause silent failures, incomplete submissions, or race conditions.

## 1. Prompt Injection and Shell Escaping Risks

### Current Implementation
- Location: server/services/backlog_service.go:1157-1158, session/instance_tmux.go:49-51
- Mechanism: Triage prompt passed via positional CLI argument using fmt.Sprintf with %q format
- The %q format quotes string and escapes special characters

### Known Risks

#### 1.1 Unvalidated User Input in Item Description
- Risk: If backlog item description contains problematic sequences, escaping may be insufficient
- Example: Description with backticks, newlines, control characters
- Impact: Claude receives malformed/truncated prompt
- Status: Partially mitigated by %q quoting, not tested against adversarial input

#### 1.2 Path Traversal in Artifact Directory
- Risk: artifactAbsPath constructed from user-controlled item.Title via slugify()
- If slug generation fails or sanitization incomplete, path could reference unintended directories
- Status: slugify() only allows alphanumeric + hyphens (safe), but relies on MkdirAll() without validation

#### 1.3 JSON Serialization in Triage Prompt
- Risk: Prompt embeds item title and description directly in markdown
- If description contains markdown special chars (triple-backticks, HTML), Claude's parsing may fail
- Status: Partially mitigated via SanitizeForAgentContext() but max 2000 chars — truncation may orphan formatting

## 2. Session Exit Detection and Timing Race Conditions

### Current Implementation
- Location: session/backlog_lifecycle.go:96-118, session/instance_tmux.go:46-51
- Mechanism: onSessionExited() called when tmux session dies, UpdateItemSessionEnded() records endedAt

### Known Risks

#### 2.1 Race Condition: EndedAt Set Before Submit Completes
- Risk: Triage session exits after Claude calls submit_triage_result, before MCP handler persists result
- Timeline: Claude calls submit → Session exits → onSessionExited runs → UpdateItemSessionEnded sets endedAt → MCP saves result
- Status: NOT fixed by commit — endedAt set before role check, but triage result save not coordinated

#### 2.2 OneShot Flag: Process Exit Without Clean Session Cleanup
- Risk: With -p flag, Claude exits immediately. If cleanup races with onSessionExited(), files orphaned
- -p flag now added correctly (fixed), but no grace period for cleanup
- Status: Fixed in buildLaunchCommand, but no race condition handling

#### 2.3 Session Never Receives Prompt (Timing)
- Risk: Session created and started, but prompt not injected when Claude initializes
- Before fix: Prompt via --append-system-prompt (system context), Claude waits for message
- After fix: Prompt now positional argument, Claude should receive and start working
- Status: Partially fixed — now correct method, but no handshake to verify receipt

#### 2.4 Orphaned Sessions on Server Restart
- Risk: If server dies while triage running, session unkilled in tmux but not tracked in-memory
- Guard: TriggerTriage() checks sessionStopper.IsSessionLive() to detect orphans
- Risk: If sessionStopper nil or crashes, orphan detection skipped
- Status: Mitigated by notLive || statusAdvanced guard, but requires sessionStopper wired

## 3. OneShot Flag and Non-Interactive Exit

### Known Risks

#### 3.1 Flag Added After Resume
- Risk: If session paused and resumed, -p flag not re-added
- buildLaunchCommand() called during initTmuxSession() only
- On resume, existing tmux session reused, so -p never added
- Status: Unclear if oneShot sessions support pause/resume

#### 3.2 Interaction with --resume Flag
- Risk: If both --resume conversation-id and -p present, Claude may not restore context before exiting
- Status: Needs verification — test that -p does not break session resumption

#### 3.3 Silent Exit Without Error Reporting
- Risk: With -p, Claude exits without writing errors or incomplete results
- If error during triage, -p exits without user seeing error
- Status: Partially mitigated by MCP error handling, but no rollback if Claude crashes

## 4. Submit Triage Result Flow Failures

### Location: server/mcp/tools_backlog.go:402-536

### Known Risks

#### 4.1 Partial Failure in MCP Tool
- Risk: If UpdateItemSessionTriageResult() fails, no error propagated to Claude
- Code: Error logged but NOT returned to Claude — MCP tool returns success even if DB save fails
- Claude thinks result submitted, but it is lost
- Status: CRITICAL GAP — NOT fixed

#### 4.2 Notification Published Before DB Commit
- Risk: EventBus notification published after save, but if publishing fails, save already committed
- If EventBus down, operator never notified. No retry mechanism.
- Status: Non-critical (UI can still find result), but operator misses alert

#### 4.3 PlanArtifactsPath Update Fails Silently
- Risk: If UpdateBacklogItem() for plan_artifacts_path fails, tool fails but triage result not saved yet
- Better approach: Save triage result first, fail later on artifacts path
- Status: Design issue — precedence is wrong

#### 4.4 Concurrent Submit Calls
- Risk: If Claude accidentally calls submit_triage_result twice, second overwrites first
- No idempotency key or deduplication
- Status: Not prevented — no guard at tool level

#### 4.5 Session UUID Not Set in Environment
- Risk: If STAPLER_SESSION_UUID not set in triage session environment, submitTriageResult() fails
- Set during session spawn (line 81 in instance_tmux.go)
- Risk: If env var cleared or not propagated to child processes, submit fails
- Status: Properly set, relies on tmux/claude correctly inheriting env

## 5. Session Exit Detection Gaps

### Known Risks

#### 5.1 Callback Fires But Listener Disabled
- Risk: If BacklogLifecycleListener disabled after session exits, onSessionExited() returns early
- ItemSession.EndedAt never set
- UI shows session as running even though done
- Status: Partially mitigated — listener normally enabled, but no guard against races

#### 5.2 Context.Background() Ignores Parent Cancellation
- Risk: onSessionExited() uses context.Background() instead of accepting parameter
- If server shutting down, DB writes abandoned or rejected
- Status: Design issue — should accept context from caller

## 6. Test Coverage Gaps

### Current Tests
- backlog_lifecycle_test.go: onSessionStarted, onSessionExited for work/review/triage roles
- backlog_service_test.go: TriggerTriage double-trigger guard, orphan detection
- triage-pipeline-validation.spec.ts: Full flow end-to-end

### Missing Coverage
- Prompt injection tests (quotes, newlines, unicode, control chars)
- Concurrent submit tests
- Session exit race tests
- OneShot flag tests with -p, --resume interaction
- MCP tool error handling (DB save failure, partial save)
- Environment variable tests
- Orphan cleanup tests

## 7. Silent Failure Scenarios

1. Claude receives truncated prompt — item description too long
2. MCP tool succeeds but DB write fails — result lost in logs
3. Session exits before prompt received — prompt lost in shell parsing
4. EventBus publish fails — operator never notified
5. Orphan session not cleaned — re-trigger gets CodeAlreadyExists

## 8. Recommendations for Hardening

### High Priority
1. submitTriageResult: Return error if DB save fails
2. Add prompt receipt verification (handshake)
3. Prevent concurrent triage submits (idempotency)
4. Validate plan_artifacts_path exists before passing to Claude

### Medium Priority
5. Add context to onSessionExited() with timeout
6. Log triage prompt hash for debugging
7. Increase prompt generation test coverage
8. Add adversarial input tests

### Low Priority
9. Timeout-based orphan cleanup (reconciliation task)
10. Retry EventBus publish with exponential backoff
11. E2E test with slow Claude startup

## Conclusion

The commit fixed three critical bugs preventing triage from working. However, residual risks remain in:
- Prompt safety (no input validation)
- MCP error handling (tool succeeds even if DB save fails)
- Concurrency (no deduplication, possible race on endedAt)
- Visibility (no handshake to verify prompt receipt)
- Test coverage (missing injection, concurrency, edge case tests)

The pipeline now functional but fragile. Operators should monitor logs for failed save warnings indicating silent failures where Claude thought it succeeded but result was lost.

**Next steps**: Fix submitTriageResult error handling (high priority, quick), add e2e prompt validation (catches injections), add prompt receipt logs (debugging aid), add idempotency checks (prevents duplicate submissions).
