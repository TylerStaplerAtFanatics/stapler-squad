# BUG-043: Chronic `abandoned_review` Respawn Failures — Review Sessions Never Actually Deliver Their Initial Prompt, So the Same Items Cycle Through Every Backoff Attempt Without Resolving [SEVERITY: High]

**Status**: 🔴 Open
**Discovered**: 2026-07-23, during a live audit of items that hadn't moved recently (follow-up to the BUG-040/BUG-041 deploy)
**Impact**: At least 3 real backlog items have been stuck in `review` for 8–20+ hours with no active session and no PR, each having burned **4 of the 5** automated `abandoned_review` remediation attempts (`AutoRespawnReview` → `TriggerReReview`) without resolving. The 5th and final automated attempt for each is ~24h out per the backoff schedule (`session/backlog_remediation.go`'s `remediationBackoffSchedule`); after that they park permanently and need a manual `ResetStuckRemediation` call. This isn't three unrelated flukes — it's the same failure mode repeating on every respawn, live evidence below.

## Live Evidence

**DB state** (`~/.stapler-squad/workspaces/d685c4b1a423cca3/sessions.db`, `backlog_stuck_states` table):

| item_id | title | reason | remediation_attempts | next_remediation_at |
|---|---|---|---|---|
| `e1fb6825-39b2-4f06-9bf8-c9d1678a6824` | Develop a system for our sessions to have awareness of other sessions working in the same workspace | `abandoned_review` | 4/5 | 2026-07-24 08:32 |
| `693c2700-d6b8-4d98-aaa4-c0e5eb2d42c5` | Expose ID functionality in Backlog | `abandoned_review` | 4/5 | 2026-07-24 08:32 |
| `12981e9d-0ad5-4a79-af99-a2be35b22856` | Unfinished page needs CSS work for sizing | `abandoned_review` | 4/5 | 2026-07-24 08:17 |

(`e1fb6825` and `12981e9d` also independently show `bouncing` at 4/5 attempts — likely a downstream symptom of the same root failure, not a separate cause.) All three: `status = review`, no active tmux session, `pr_number = 0`.

**Log evidence** (from the 2026-07-22 20:53 window, before the log file most recently rotated on deploy):

```
{"level":"WARN","msg":"SessionDriver: session stuck — no output for inactivity timeout","session":"review:12981e9d","inactivity":600000000000}
{"level":"WARN","msg":"SessionDriver: session stuck — no output for inactivity timeout","session":"review:e1fb6825","inactivity":600000000000}
{"level":"ERROR","msg":"SessionDriver: giving up on initial prompt after 3 failed attempts","session":"review:12981e9d"}
{"level":"ERROR","msg":"SessionDriver: giving up on initial prompt after 3 failed attempts","session":"review:e1fb6825"}
```

This log window was originally (incorrectly) written off in an earlier audit pass as stale residue from before BUG-039 landed. The DB's `remediation_attempts` history shows it wasn't a one-time artifact — it's recurred on every automated respawn since.

## Root Cause (hypothesis — not yet confirmed by a live trace)

`AutoRespawnReview` (`server/services/backlog_service_triage.go:1411`) calls `TriggerReReview`, which spawns a fresh review-role session. `SessionDriver`'s initial-prompt injection loop (`session/session_driver.go:307-341`) then tries to send that session's opening prompt via `inst.SendKeys(...)`. When `SendKeys` itself errors 3 times in a row, the driver logs `"giving up on initial prompt after 3 failed attempts"` and — critically — **sets `sentInitial = true` anyway** (line 339), meaning the driver believes the prompt was delivered even though it never was. The review session then sits idle forever with nothing actually asked of it: no review happens, the item is still "abandoned" on the next stuck-sweep pass, `AutoRespawnReview` fires again, and the same `SendKeys` failure repeats — consuming one remediation attempt per cycle without ever making progress. This matches the observed 4/5-attempts-and-still-stuck shape exactly.

The `SendKeys` failure signature (repeated errors immediately after session creation) is the same shape as BUG-041 (dead/not-yet-ready tmux pane), but BUG-041 was specifically about the *nudge* send retry-forever bug on an already-running session — this is a **different call site** (initial-prompt injection on a freshly spawned review session) hitting what looks like the same underlying dead-pane class of problem. Worth checking whether review-role sessions specifically have a pane-readiness race at spawn time that work-role sessions don't (e.g. a timing difference in how `TriggerReReview`'s spawn path hands off to `SessionDriver` versus a normal work-session spawn).

## Suggested Fix Direction

1. Live-trace an actual in-progress failure (current logs have rotated past the last occurrence — need to catch the *next* automated attempt, e.g. one of these three items' 5th and final attempt around 2026-07-24 08:17–08:32, or trigger `TriggerRemediationNow` manually and tail logs live) to confirm the exact `SendKeys` error and whether it's pane-readiness, a race with session creation, or something else.
2. `session/session_driver.go:335-341`: setting `sentInitial = true` after 3 failed `SendKeys` attempts silently converts a real failure into "looks fine, nothing to see here" from every downstream consumer's perspective (including `AutoRespawnReview`'s caller, which sees no error and assumes the respawn worked). At minimum this give-up path should surface as a distinguishable state (a stuck reason, a notification, or a non-nil error bubbled back through the spawn call) rather than pretending success — same "silent dead end, nothing detects it" shape this project has hit repeatedly (BUG-040, and the `docs/tasks/backlog-feature-improvement.md` audit trail generally).
3. Once the pane-readiness root cause is confirmed, the real fix is likely in how review-role sessions are spawned/handed off to the driver (or in `SessionDriver`'s retry/backoff for the initial-prompt send, analogous to BUG-041's fix).

## Recommended Routing

`sdd:fix-bug`, scoped to: "review sessions spawned via `AutoRespawnReview`/`TriggerReReview` chronically fail to receive their initial prompt, burning remediation attempts without ever completing a review." Start the trace at `session/session_driver.go:307-341` and `TriggerReReview`'s spawn path. Concrete repro candidates: items `e1fb6825`, `693c2700`, `12981e9d` — all three should be live-observable at their next scheduled remediation attempt (2026-07-24, ~08:17–08:32) if not fixed before then, or can be forced immediately via `TriggerRemediationNow` (item_id, reason=`STUCK_REASON_ABANDONED_REVIEW`) while tailing `~/.stapler-squad/logs/staplersquad.log` live. This is a `bucket 1` reconciliation-class bug per the `backlog-feature-improvement` skill's routing table — independent of BUG-040/BUG-041 (different call site) but worth checking for a shared root cause with BUG-041 given the identical `SendKeys`-fails-repeatedly signature.
