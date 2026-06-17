# Triage Pipeline: Known Risks and Failure Modes

## 1. Session Hangs if Claude Never Calls submit_triage_result
Risk: MEDIUM | Type: Indefinite Wait

Claude completes research but crashes or forgets submit_triage_result. OneShot exits session correctly, but TriageResult stays empty.

Gaps: No timeout, no retry, no mandatory verification before exit, no health check.

---

## 2. Prompt Injection Race / Stale Tmux Sessions
Risk: LOW | Type: Prompt Delivery

Pre-spawn cleanup kills stale tmux sessions (lines 1131-1142, backlog_service.go), but no verification that kill succeeded. Old session might reattach.

Gaps: No verification of successful kill, race window exists.

---

## 3. OneShot Session Doesn't Auto-Exit
Risk: LOW | Type: Session Lingers

OneShot flag might not add -p if program name doesn't contain "claude". Substring matching is fragile (line 46, instance_tmux.go).

Gaps: Program name detection is brittle.

---

## 4. No Explicit Timeout for Triage Sessions
Risk: MEDIUM | Type: Resource Leak

No context.WithTimeout enforces maximum session lifetime. Claude could loop indefinitely.

Gaps: No time limit in prompt, no server-side timeout, no heartbeat.

---

## 5. submit_triage_result Failures Not Handled
Risk: HIGH | Type: Silent Failure, No Recovery

If UpdateItemSessionTriageResult fails, error is logged but NOT returned to Claude. Claude thinks it succeeded. TriageResult never persists.

Consequence: Item stuck. Cannot re-trigger (CodeAlreadyExists from guard) unless manually tombstoned.

Gaps: No auto-retry, no error propagation, no rollback on partial updates.

---

## 6. Ambiguous Triage Success vs. Failure
Risk: MEDIUM | Type: Observability

Success = EndedAt != nil AND TriageResult != empty. But no explicit status enum (success/failed/partial).

Gaps: No error_event table, operators infer from silence.

---

## 7. Rate Limiting Gaps
Risk: LOW | Type: Rate Limit

No rate limit on submit_triage_result. Claude API limits not enforced by Stapler.

Gaps: No token budget check, no backoff on 429.

---

## 8. Backlog Item Orphaned if Session Crashes
Risk: MEDIUM | Type: State Corruption

Session crashes before submit_triage_result. Artifact directory /docs/tasks/{slug} created with partial files. Re-trigger allowed by orphan guard, but stale files remain.

Gaps: No cleanup on re-trigger, no versioning.

---

## 9. onSessionExited Async Race
Risk: MEDIUM | Type: Async Coordination

onSessionExited dispatched as goroutine (line 67, backlog_lifecycle.go). Between EventExited and EndedAt write, UI could see stale state (EndedAt still nil).

Gaps: No sync, EventBus notifications fire early.

---

## 10. No Error Event Recording
Risk: MEDIUM | Type: No Audit Trail

UpdateItemSessionEnded errors logged but not persisted. No queryable error history. Operators must grep logs.

Gaps: No error_event entries, no post-facto inspection.

---

## 11. Concurrent TriggerTriage Calls
Risk: LOW | Type: Race Condition

No lock or database constraint. Two operators could spawn two sessions. Last submit_triage_result result wins.

Gaps: No mutex, no uniqueness constraint on ItemID+SessionRole.

---

## 12. BacklogLifecycleListener Not Wired
Risk: LOW | Type: Missing Feature

If listener not wired, EventExited never triggers onSessionExited. But listener wired during standard creation, so rare.

---

## High-Risk Summary

submit_triage_result failure = HIGHEST RISK
- Silent failure
- Blocks re-trigger (CodeAlreadyExists)
- Item stuck indefinitely

Medium risks:
- No timeout on triage
- No audit trail for errors
- Async coordination races

Low risks:
- OneShot flag, prompt injection (mostly fixed)

---

## Recommended Mitigations

1. Mandatory result submission - block exit if TriageResult empty
2. Timeout on triage - context.WithTimeout(ctx, 2*time.Hour)
3. Error event recording - persist failures (async)
4. Explicit status enum - TriageStatus field on ItemSession
5. Error propagation - return error from submitTriageResult
6. Artifact versioning - timestamp directories
7. Database constraint - unique (ItemID, SessionRole) for triage
8. Health check - find orphaned sessions (EndedAt set, TriageResult empty, >1h old)

---

## Key Takeaways

Operators:
- Verify EndedAt != nil AND TriageResult != empty to confirm success
- Tail logs for [BacklogLifecycle] and [mcp:submit_triage_result] errors
- Manually tombstone if stuck with CodeAlreadyExists
- Re-trigger if triage fails (orphan guard should allow it)

Developers:
- Focus on mandatory result submission and timeouts
- Add error event recording
- Test failure scenarios (submit errors, timeouts, crashes)
- Consider database constraints to prevent concurrent triage
