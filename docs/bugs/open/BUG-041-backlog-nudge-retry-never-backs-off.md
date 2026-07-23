# BUG-041: Backlog Work-Session Nudge Retries Forever at Full Speed on Send Failure, Never Backs Off or Gives Up [SEVERITY: Medium]

**Status**: 🔴 Open
**Discovered**: 2026-07-22/23, log review requested during `backlog-feature-improvement` follow-up
**Impact**: `SessionDriver`'s idle-nudge logic (`session/session_driver.go`) sends a one-time "you appear to have paused" reminder to a backlog work session once it's been idle past `driverBacklogNudgeDelay`. If that `SendKeys` call fails, the driver retries the *identical* send on every subsequent driver tick, indefinitely — no backoff, no attempt cap, no escalation to a stuck-reason or notification. Live: **392 consecutive failed sends over ~13 minutes** (2026-07-22T20:51:57 → 21:04:59, roughly every 2s) against tmux session `stapler-squad-expose-backlog-item-id-r8`, every one `err: "invalid argument"` — the signature of a dead/gone tmux pane (same failure shape as this project's other previously-documented dead-pane bugs, e.g. the `9264efe7` review-pane-death investigation in `docs/tasks/backlog-feature-improvement.md`). This session belongs to backlog item `693c2700` ("Expose ID functionality in Backlog"), one of the items currently cycling through the new bounded remediation-backoff system — this bug is a smaller, adjacent busy-loop sitting underneath that cycle, wasting driver-tick cycles hammering a pane that cannot possibly succeed.

## Live Evidence

```
{"time":"2026-07-22T20:51:57...","level":"WARN","msg":"SessionDriver: failed to send backlog nudge","session":"stapler-squad-expose-backlog-item-id-r8","err":"invalid argument"}
{"time":"2026-07-22T20:51:59...","level":"WARN","msg":"SessionDriver: failed to send backlog nudge","session":"stapler-squad-expose-backlog-item-id-r8","err":"invalid argument"}
... (392 total, same session, same error, ~every 2s, spanning ~13 minutes)
{"time":"2026-07-22T21:04:59...","level":"WARN","msg":"SessionDriver: failed to send backlog nudge","session":"stapler-squad-expose-backlog-item-id-r8","err":"invalid argument"}
```

## Root Cause

`session/session_driver.go:409-424`:

```go
if inst.HasTag(TagBacklogWork) && nudgeSentAt.IsZero() && idle > driverBacklogNudgeDelay {
    nudge := "..."
    if sendErr := inst.SendKeys(nudge + "\r"); sendErr != nil {
        log.Warn("SessionDriver: failed to send backlog nudge", "session", inst.Title, "err", sendErr)
    } else {
        log.Info("SessionDriver: sent backlog nudge", ...)
        nudgeSentAt = time.Now()
    }
    continue
}
```

`nudgeSentAt` — the guard that's supposed to make this a one-time nudge — is only assigned in the success (`else`) branch. On a `SendKeys` error, `nudgeSentAt` stays its zero value, so the very next driver tick re-evaluates the same `nudgeSentAt.IsZero() && idle > driverBacklogNudgeDelay` condition as still true and retries immediately. If the underlying cause is permanent (e.g. the tmux pane is dead, which `invalid argument` strongly suggests), this retries at the driver's tick interval forever, with no backoff, no attempt cap, and no path to surface "this session can't be nudged" as an actionable signal (no `MarkStuck`, no notification, nothing).

## Suggested Fix

Set `nudgeSentAt = time.Now()` (or a dedicated `lastNudgeAttemptAt`) on failure too, so the retry is rate-limited the same way a successful nudge is — the driver already has `driverBacklogNudgeGrace` for the post-nudge grace period; reuse that cadence (or a shorter dedicated backoff) rather than retrying every tick. Additionally, after some small bounded number of consecutive failures, this is a strong signal the pane itself is dead — that should feed into whatever this project's existing dead-pane detection surfaces (worth checking whether `IsSessionLive`/the tombstoning helpers already used elsewhere in `backlog_service_triage.go` apply here, or whether `SessionDriver` needs its own equivalent), rather than silently retrying forever.

## Recommended Routing

`sdd:fix-bug`, scoped narrowly — this is a self-contained fix in `session/session_driver.go` with a clear regression test (assert a failed `SendKeys` doesn't cause the very next tick to retry immediately). Independent of BUG-040 (different file, different mechanism) — safe to run as its own parallel fix.
