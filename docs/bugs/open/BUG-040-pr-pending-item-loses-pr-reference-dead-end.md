# BUG-040: `pr_pending` Item Can Lose Its PR Reference and Become Permanently Unreconcilable [SEVERITY: High]

**Status**: 🔴 Open
**Discovered**: 2026-07-22/23, `backlog-feature-improvement` audit — live item `9264efe7-b4c2-455a-9e2a-ab0196a63ecd` ("Backlog History feature Broken", the same item named the sole real stuck item in this doc's 2026-07-22 update, PR #173)
**Impact**: An item can end up with `status = pr_pending` but `pr_url = ""` and `pr_number = 0`. `ReconcilePRPending` (`session/backlog_lifecycle.go:3204+`) only acts on items in `pr_pending` status, and every action it takes (`GetPRStatus`, `AutoReopenForPRFix`'s fix context, `EnablePRAutoMerge`) requires a real `PrNumber`. Once the fields are empty, the item has nothing left to poll and sits in `pr_pending` forever — the terminal dead end this whole subsystem's self-heal machinery (`docs/tasks/backlog-feature-improvement.md`) was built to avoid. `ListStuckBacklogItems` does flag the item, but only via the unrelated `STUCK_REASON_AUTONOMOUS_STUCK` (stale, from an earlier 20-turn-cap event) — nothing in the detector set describes "pr_pending with no PR," and nothing auto-resolves `autonomous_stuck` once the item leaves active driver control (a known, separately-tracked gap from the 2026-07-19 audit update).

## Live Evidence

```
$ sqlite3 sessions.db "SELECT status, pr_url, pr_number FROM backlog_items WHERE id='9264efe7-...'"
pr_pending|| 0
```

PR #173 itself: `state: CLOSED`, `mergedAt: null`, `mergeStateStatus: DIRTY`, `mergeable: CONFLICTING`, closed `2026-07-22T17:29:31Z` — closed without merging, not by this reconciler's own `closeIfSupersededByMain` path (that path always transitions straight to `done`, never leaves the item in `pr_pending`).

Status-event history for the item's last two transitions:
```
in_progress → review    2026-07-22T17:12:13Z  (no note)
review      → pr_pending 2026-07-22T23:01:25Z  (no note)
```
No transition since. 16 historical `work`-role sessions recorded for this item, none currently unended — ruling out both `AutoReopenForPRFix`'s `hasActiveWorkSession` guard and the (configured) rework cap of 20 as the reason nothing has reopened it since.

## Root Cause (two candidate code paths, not yet distinguished by a live trace — logs for the relevant window had already rotated out by the time this was investigated)

Both are real, independently-verified structural gaps in `session/backlog_lifecycle.go` and either explains the observed state:

**1. `pushAndCreatePR`'s field-persist is best-effort and non-blocking (lines 2686-2722, 2743-2747).** When it creates a brand-new PR (the `else` branch, no cached `PrNumber`/`PrURL` to reuse), it calls `l.storage.UpdateBacklogItem(...)` to cache `PrURL`/`PrNumber` — but a failure there is only `log.WarningLog`'d (line 2720), never returned or checked. The function proceeds unconditionally to `resolveToPRPending(ctx, item.ID, "", "pushAndCreatePR")` (line 2744, empty note — matches the observed no-note transition exactly) regardless of whether the persist succeeded. A transient storage error at exactly that point silently produces this bug's exact shape: `pr_pending` status, empty `PrURL`/`PrNumber`.

**2. `ReconcilePRPending`'s closed-PR branch (lines 3320-3359) clears fields before delegating recovery.** When it detects `prStatus.IsClosed` on a `pr_pending` item (and `closeIfSupersededByMain` doesn't apply), it clears `PrURL`/`PrNumber` to empty *first* (lines 3337-3344), then calls `fixSpawner.AutoReopenForPRFix` to actually reopen the item for a fresh attempt. `AutoReopenForPRFix` (`server/services/backlog_service_triage.go:1276+`) has several early-return no-op paths — an error from any of its own storage calls, or (in principle, for other items) the `hasActiveWorkSession`/rework-cap guards — that leave the item exactly where this branch left it: `pr_pending`, fields already cleared, but never actually reopened. Nothing re-tries; the next `ReconcilePRPending` tick finds `PrNumber == 0`, `GetPRStatus(0)` errors, and the loop just `continue`s (line 3307-3311) — permanently.

Both paths converge on the same failure shape: **a `pr_pending` item with no PR reference is invisible to every downstream reconciler**, since every one of them is keyed on `PrNumber`/`PrURL` being present.

## Suggested Fix Direction

- `pushAndCreatePR`: if `UpdateBacklogItem` fails to persist the new PR fields, do not proceed to `resolveToPRPending` — treat it the same as a push/PR-creation failure (`stayInReviewAndNotify`), since silently entering `pr_pending` with no way to look the PR back up is worse than staying in `review` with a durable `push_failed`-style stuck row.
- `ReconcilePRPending`'s closed-PR branch: don't clear `PrURL`/`PrNumber` unconditionally before the reopen. Either (a) only clear them once `AutoReopenForPRFix` has confirmed it actually transitioned the item off `pr_pending`, or (b) add a dedicated `StuckReason` (e.g. `pr_pending_no_pr`) that `ListStuckBacklogItems` surfaces whenever `status == pr_pending && pr_number == 0`, so this specific dead end is at least visible and retry-able from `/unfinished` even if the root write-ordering bug isn't fixed in the same pass.
- Minimum viable fix for item `9264efe7` itself: a manual `AutoReopenForPRFix` call (or equivalent RPC) once the code fix lands, since the item cannot self-recover as things stand.

## Recommended Routing

`sdd:fix-bug`, scoped narrowly to this shape. Start with a live trace this investigation didn't have time for (grep the *current* `staplersquad.log` — not yet rotated — the next time this reproduces) to confirm which of the two candidate paths actually fired for `9264efe7`, since the fix differs slightly by path. Independent of the 2026-07-19 bounce-loop fix and the 2026-07-22 dead-review-pane fix (both already shipped) — this is a new instance of the recurring "an event/write silently doesn't happen, and nothing detects the resulting dead end" shape this doc has repeatedly named across its update history.
