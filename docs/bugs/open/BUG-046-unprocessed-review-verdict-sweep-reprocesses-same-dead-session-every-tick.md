# BUG-046: `reconcileUnprocessedReviewVerdicts` Reprocesses the Same Dead Review Session on Every Sweep Tick, Spamming Notifications While the Backoff Gate Correctly Blocks Any Real Action [SEVERITY: Medium]

**Status**: 🔴 Open
**Discovered**: 2026-07-24, live on the mobile Alerts page — a single "Review session ended without a verdict" notification on item `12981e9d` reached `occurrence_count: 95` over ~94 minutes (one per ~60s tick), still climbing.
**Impact**: Once a review session exits without calling `submit_review_verdict` while its item's `bouncing` backoff gate isn't due yet, the periodic reconcile sweep re-detects and re-"handles" the exact same dead session on every tick — re-logging a WARNING, re-firing an ERROR notification (deduped into one record, but its `occurrence_count`/`last_occurred_at` keep incrementing forever), and re-invoking the gated reopen path, which correctly no-ops but is invoked regardless. No real state ever changes and the backoff gate itself is working correctly — the bug is purely that the sweep has no way to distinguish "already handled, just gated" from "never handled," so it treats the same condition as fresh on every pass. Purely a notification/log-spam and wasted-work bug — not a stuck-item or data-loss bug (the underlying item, `12981e9d`, has continued progressing normally via a separate live work session throughout).

## Live Evidence

Notification record (`~/.stapler-squad/workspaces/d685c4b1a423cca3/notifications.json`):
```json
{
  "session_id": "12981e9d-0ad5-4a79-af99-a2be35b22856",
  "title": "Review session ended without a verdict",
  "message": "Unfinished page needs CSS work for sizing — the review session exited without calling submit_review_verdict. Treating as a failed review.",
  "created_at": "2026-07-24T11:40:32-07:00",
  "occurrence_count": 95,
  "last_occurred_at": "2026-07-24T13:14:55-07:00"
}
```
95 occurrences over 94 minutes ≈ one every 59 seconds — matching this sweep's poll cadence, not a legitimate one-review-attempt-per-occurrence cadence (the `bouncing` backoff schedule's tiers are 30min/2h/8h/24h/72h; nothing in this system legitimately retries a full review cycle every 60 seconds). Correlated log pattern (`~/.stapler-squad/logs/staplersquad.log`), also recurring every ~60s for the same item ID:
```
DEBUG ReactiveQueueManager user interaction 12981e9d-0ad5-4a79-af99-a2be35b22856
DEBUG ReactiveQueueManager instance not found 12981e9d-0ad5-4a79-af99-a2be35b22856
```
(This second pattern may be a related or separate symptom of the same underlying dead/orphaned `review:12981e9d` tmux pane — worth checking during the fix, but the primary confirmed defect is in `reconcileUnprocessedReviewVerdicts`/`handleReviewSessionExited`, detailed below.)

## Root Cause

`reconcileUnprocessedReviewVerdicts` (`session/backlog_lifecycle.go:1609`) is a periodic sweep that finds items whose most recent review-role session is dead with no verdict, and calls `handleReviewSessionExited(ctx, ..., forcePush=true)` (line 1672) to process it. Its own doc comment already identifies the general shape of this risk (lines 1630-1651): *"FindReviewItemsWithUnprocessedVerdict has no notion of 'already consumed'... if the item later re-enters 'review' for any other reason... this sweep would treat that stale, already-shipped verdict as fresh and reprocess it."* The existing guard against that (comparing the dead session's `CreatedAt` against the item's most recent transition-into-review timestamp, `GetMostRecentStatusEventAt`) only catches the "item left review and came back" case — it does **not** catch the case live here: the item **never leaves `review` at all**, because `handleReviewSessionExited`'s no-verdict branch (`session/backlog_lifecycle.go:870-883`) calls `l.notify(...)` **unconditionally**, then calls `autoReopenWithBackoffGate` (line 943), which checks `RemediationDue(ctx, itemID, domain.StuckReasonBouncing)` and — correctly, when the backoff gate isn't due yet — just logs and returns without transitioning the item anywhere. Since the item's review-entry timestamp never advances (nothing transitioned it), the sweep's existing guard doesn't skip it on the next tick either, and the identical dead session gets "handled" again: same WARNING log, same `notify()` call (incrementing the deduped notification's `occurrence_count`), same gated no-op reopen attempt. Repeats every sweep tick until the backoff gate finally opens (or the item's state changes for some unrelated reason).

## Suggested Fix Direction

The cleanest fix is likely at the `notify()` call site inside `handleReviewSessionExited`'s no-verdict branch (`session/backlog_lifecycle.go:875-880`): only notify (and only log at WARNING) the **first** time this dead session is detected for a given review-entry cycle, not on every re-detection. Since `autoReopenWithBackoffGate` already has the real gating logic (`RemediationDue`), one option: check `RemediationDue`/the existing `bouncing` stuck-state row *before* deciding whether to notify at all — if a `bouncing` row is already open for this item (i.e. this exact condition was already recorded on a prior tick), skip the redundant notify+log and only re-run `autoReopenWithBackoffGate` (which itself already gates the real action correctly) — mirroring the idempotency pattern BUG-043 already established for `abandoned_review`'s attempt-budget guard. Alternatively/additionally, `reconcileUnprocessedReviewVerdicts`'s own sweep could track "already surfaced this dead SessionUUID once" more directly (e.g. via the tombstoned `EndedAt` write it already does at line 1654 — right now that write happens but nothing *reads* it as a "don't re-notify" signal on a later tick, only as a "this is dead" signal).

Also worth a quick check while in this code: the correlated `ReactiveQueueManager ... instance not found` log pattern firing on the same ~60s cadence for the same item — confirm whether it's a symptom of the same root cause (e.g. some other component still holding a reference to the dead session and re-emitting a "user interaction" event for it every tick) or an unrelated, separate minor issue.

## Recommended Routing

`sdd:fix-bug` — small, well-scoped, single-file fix with a clear regression-test shape (assert that calling `handleReviewSessionExited` twice in a row for the same dead SessionUUID with the backoff gate not due only notifies/logs once). Live repro is item `12981e9d` — the notification's `occurrence_count` should stop climbing once the fix is deployed; can verify by checking `~/.stapler-squad/workspaces/*/notifications.json` before/after.
