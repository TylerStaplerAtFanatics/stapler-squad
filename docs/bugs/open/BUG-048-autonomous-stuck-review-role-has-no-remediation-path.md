# BUG-048: `autonomous_stuck` Rows Opened by a Stuck Review-Role Session Have No Remediation Path — `next_remediation_at` Drifts Forever Without Ever Firing [SEVERITY: Medium]

**Status**: 🔴 Open
**Discovered**: 2026-07-24, same investigation as BUG-047 — backlog item `12981e9d` ("Unfinished page needs CSS work for sizing", PR #210) had an `autonomous_stuck` stuck-state row whose `next_remediation_at` was over 2 hours in the past (`2026-07-24 12:09:32`, checked against a current time of `14:09`+) with `resolved_at` still `NULL`, and no code path was ever going to act on it.
**Impact**: For items that run through the autonomous pipeline (`queued_autonomous=1`), a **review-role** autonomous driver session that exits without producing a verdict opens a durable `autonomous_stuck` stuck-state row (via the shared `MarkStuck` call that fires for *any* role's non-`Done` exit) — but the remediation/respawn logic that's supposed to act once `next_remediation_at` is due only exists for the **work**-role case. A review-role stuck row has no analogous respawn call, so once opened, it sits forever: `next_remediation_at` becomes arbitrarily overdue and nothing ever revisits it, clears it, or surfaces it beyond the one-time notification fired when it was created. In this investigation this was a secondary, latent finding — not the primary explanation for item `12981e9d`'s specific 2.5-hour stall (that was BUG-047, and the item's live work session was still genuinely active/idle-waiting, not blocked by this gap) — but it is a real gap that will bite the next item whose review-role autonomous session gets stuck with no live work session left to eventually resolve it via the unconditional `resolveAutonomousStuck` call on a later `Done` transition.

## Live Evidence

```
$ sqlite3 sessions.db "SELECT reason, first_detected_at, remediation_attempts, next_remediation_at, resolved_at
                        FROM backlog_stuck_states WHERE item_id='12981e9d-...' ORDER BY first_detected_at;"

bouncing          2026-07-22 22:16:01  attempts=2  next_remediation_at=2026-07-24 14:12:32  resolved_at=NULL   (this gate IS ticking — confirmed live, see below)
autonomous_stuck  2026-07-24 11:39:32  attempts=1  next_remediation_at=2026-07-24 12:09:32  resolved_at=NULL   (2+ hours overdue, never revisited)
```

Log grep for `autonomous_stuck` across the current log window shows every occurrence originates from `onAutonomousDriverComplete`'s shared `MarkStuck`/`MarkStuckNotified` calls; there is no corresponding `RemediationDue(autonomous_stuck)`/respawn log line anywhere for a review-role session — only the work-role branch ever calls `RemediationDue`.

By contrast, the sibling `bouncing` gate for the same item **was** confirmed actively ticking: its `next_remediation_at` moved from `14:12:32` to `22:13:05` between two checks minutes apart during this investigation, proving the periodic reconcile sweep is alive and does act once a gate's deadline passes — `autonomous_stuck` (for a review-triggered occurrence) simply has no equivalent action to take.

## Root Cause (confirmed by code read)

`server/services/autonomous_orchestration_service.go`, `onAutonomousDriverComplete`:

```go
// Applies to ANY role — Triage, Work, or Review — whenever the driver exits
// without a DONE signal:
if !outcome.Done {
    if _, markErr := concreteStorage.MarkStuck(ctx, item.ID, domain.StuckReasonAutonomousStuck, ...); markErr != nil {
        ...
    } else if _, notifyErr := concreteStorage.MarkStuckNotified(ctx, item.ID, domain.StuckReasonAutonomousStuck); notifyErr != nil {
        ...
    }
}

switch is.Role {
case session.SessionRoleTriage:
    // stuck triage: notify only, item stays at 'idea'
case session.SessionRoleWork:
    if !outcome.Done {
        // *** the only place RemediationDue(autonomous_stuck) + AutoRespawnAutonomousWork
        //     are ever called ***
        due, justParked, gateErr := concreteStorage.RemediationDue(ctx, itemID, domain.StuckReasonAutonomousStuck)
        ...
        if due {
            go func() { respawner.AutoRespawnAutonomousWork(...) }()
        }
    }
case session.SessionRoleReview:
    // Only resolves the row on outcome.Done == true. A stuck (non-Done) review
    // exit falls through to "log and return" with NO remediation attempt at all.
    if outcome.Done {
        a.resolveAutonomousStuck(ctx, concreteStorage, item.ID)
    }
    log.Info("[AutonomousDriver] skipping status transition for role", "role", is.Role, "item", item.ID)
    return
}
```

So: a review-role session that exits without a verdict correctly opens/refreshes the `autonomous_stuck` row (same as work-role would), but the `case session.SessionRoleReview` branch has no analog to work-role's `RemediationDue` + `respawner.AutoRespawnAutonomousWork` block — it just logs and returns. The row's `next_remediation_at` is set once at creation and never checked again by anything, because nothing ever calls `RemediationDue(autonomous_stuck)` for a review-originated occurrence.

The row *can* still be cleared eventually — but only as a side effect of something else entirely succeeding later: `resolveAutonomousStuck` is called unconditionally whenever *any* subsequent `onAutonomousDriverComplete` call has `outcome.Done == true` and successfully transitions the item (line ~426-428), or when a later review-role run itself completes with `outcome.Done == true` (line ~397-399). In other words, the row silently rides along, doing nothing, until something unrelated eventually resolves it as a bystander — not because anything actively remediated the review-role stuck condition itself.

## Suggested Fix

Add a review-role analog to the work-role respawn block: when `SessionRoleReview` exits with `!outcome.Done`, check `RemediationDue(ctx, itemID, domain.StuckReasonAutonomousStuck)` the same way work-role does, and if due, respawn a fresh headless review session for the item (likely via the same `reviewGateTrigger.TriggerReviewForSession` mechanism used after a work session completes, applied to the item's current review-role session context) instead of leaving the item to rely on the separate `bouncing`/`autoReopenWithBackoffGate` path in `session/backlog_lifecycle.go` to eventually reopen it back to `in_progress`. Needs design attention on:
- Whether respawning a review directly is preferable to just deferring to the existing `bouncing` backoff gate (which *does* independently work for this same "review exited without a verdict" condition, per `session/backlog_lifecycle.go`'s `handleReviewSessionExited`/`autoReopenWithBackoffGate`) — there may be two parallel systems both nominally responsible for the same recovery here (`session/backlog_lifecycle.go`'s bouncing-gate reopen vs. `autonomous_orchestration_service.go`'s autonomous_stuck respawn), and it's not obvious from the code alone whether `autonomous_stuck`'s review-role gap is a real second responder that's missing, or whether `bouncing`'s existing handling already fully covers this case and `autonomous_stuck` for review is effectively vestigial/redundant and should instead just be resolved (not respawned) once `bouncing` has already reopened the item.
- If the two systems are meant to be independent responders (defense in depth), the review-role branch should at minimum resolve its own `autonomous_stuck` row once `bouncing`'s reopen has already handled the same underlying condition, so it doesn't sit falsely "still open" indefinitely even when the item has, in fact, recovered via the other path.

## Recommended Routing

`sdd:fix-bug` or a short `plan:fix-bug` pass — the fix itself (adding a respawn/resolve branch) is small, but understanding how it should interact with the pre-existing `bouncing` gate in `session/backlog_lifecycle.go` (same condition, different subsystem) needs a deliberate design decision, not a mechanical port of the work-role branch. Add a regression test mirroring `TestMarkAbandonedReview_SkipsRespawn_WhenBouncingGateNotDue`'s shape but for a review-role `onAutonomousDriverComplete` exit, asserting that once `RemediationDue(autonomous_stuck)` reports true, *something* actually happens (respawn or explicit hand-off/resolve) — not just a log line.

## Related

- Discovered during the same investigation as BUG-047 (`docs/bugs/fixed/BUG-047-write-to-session-uses-newline-instead-of-carriage-return.md`), which was the confirmed primary cause of item `12981e9d`'s specific stall.
- Same general "gate exists, nothing ever checks it for this branch" family as BUG-043 (`docs/bugs/fixed/BUG-043-chronic-abandoned-review-respawn-failures.md`) and BUG-046 — this codebase has now surfaced this shape multiple times across different subsystems (`session/backlog_lifecycle.go`'s bouncing gate, `server/services/autonomous_orchestration_service.go`'s autonomous_stuck gate) independently.
