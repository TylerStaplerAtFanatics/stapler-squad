# Backlog Feature Improvement — Audit Findings (2026-07-14)

Produced by the `backlog-feature-improvement` skill. Goal: an item goes idea → shipped PR
with minimal human intervention, and pipeline stages are configurable per item (e.g. "use
`/sdd:full` for this one"). This audit combines a live item-state check, a UI walkthrough,
and four quality-skill passes (`quality:architecture-review`, `ux:review`, `code:review`,
`code:is-it-ready`) scoped to the backlog automation code.

Findings are bucketed: **[1] reconciliation bugs**, **[2] manual gates that could be
policy-driven**, **[3] hardcoded pipeline steps that need a configurability seam**.

## Live State (as of this audit)

6 backlog items currently stuck via `ListStuckBacklogItems`:
- 3× `ABANDONED_REVIEW` — in review with no active session
- 2× `BOUNCING` — one at **7 bounces/24h** with no PASS verdict ("Backlog History feature Broken")
- 2× `REWORK_CAP` — hit the 3-iteration rework cap
- 1× `STALE_WORK` — no progress since 11:55am

**Tooling note for this skill**: `~/.stapler-squad/sessions.db` is stale since 2026-06-30 and
returns 0 rows — the skill's Phase 1 DB-cross-check step is unreliable. Treat the
`ListStuckBacklogItems` RPC / MCP tools as source of truth. *(Skill file should be corrected —
see "Skill Fix" below.)*

## [1] Reconciliation Bugs

| # | Location | Failure scenario |
|---|---|---|
| 1 | `autonomous_orchestration_service.go:225-230,137-138` | `inst.AutonomousMode/AutonomousTurn/AutonomousMaxTurns` mutated with no lock — concurrent turn/completion callbacks race on the same `*Instance`. Code comment acknowledges, unfixed (pending "Epic 5"). |
| 2 | `autonomous_orchestration_service.go:293-311` | **CRITICAL — FIXED.** Push notification "Autonomous fix complete" fires purely off `outcome.Done`, decoupled from whether `TransitionBacklogItemStatus` (line 278) actually succeeded. TOCTOU window on a concurrent status change → operator sees "complete," item is silently stuck. Fixed on branch `fix/autonomous-status-notification-race` (commit `5a809a6d`) — notification now surfaces the transition failure explicitly; regression test added. Local commit only, not pushed/PR'd. |
| 3 | `autonomous_orchestration_service.go:242-245` | **CRITICAL — FIXED.** `GetItemSessionBySessionUUID`/`GetBacklogItem` errors and nil `item` swallowed with no log — indistinguishable from the expected "not backlog-linked" case, so a real lookup failure is undiagnosable in production. Fixed on branch `fix/autonomous-swallowed-lookup-errors` (commit `d13755da`) — distinguishes `session.ErrNotFound` (Debug) from real failures (Warn); regression tests added. Local commit only, not pushed/PR'd. |
| 4 | `autonomous_orchestration_service.go:248-276` | **FIXED.** Role→status `switch` silently no-oped (log only, no notification) on an unrecognized role — a new pipeline stage added elsewhere would silently stop advancing items with zero operator signal. Fixed on branch `fix/autonomous-unhandled-role-silent` (commit `32953891`) — unrecognized roles now log at Warn and fall through to the generic done/stuck notification; `SessionRoleReview`'s original silent-by-design behavior preserved. Regression test added. Merged to `main`, deployed. |
| 5 | `backlog_service_triage.go:760-762` | **FIXED** (part of a combined fix with #6/#7 below). `TransitionBacklogItemStatus` (idea→ready) after successful triage failed silently — item stuck in `idea` forever, zero operator signal. |
| 6 | `backlog_service_triage.go:748` | **FIXED** (combined with #5/#7). `UpdateItemSessionTriageResult` failed silently — triage output may not persist. |
| 7 | `backlog_service_triage.go:755-756` | **FIXED** (combined with #5/#6). `UpdateBacklogItem` (plan_artifacts_path) failed silently. Fixed on branch `fix/triage-silent-storage-errors` (commit `11570a2c`) — accumulates which of the three persistence steps failed and publishes a single consolidated operator notification (`notifyTriagePersistFailure`), following the existing `notifyReworkCapHit` pattern. Regression test forces the status-transition precondition to fail via a delayed fake LLM call. Merged to `main`, deployed. |
| 8 | `backlog_service_triage.go:1016-1035` | **FIXED.** Orphan-triage tombstone guard only ran on manual re-trigger — no standing detector for items stuck-after-successful-triage. Fixed on `main` (commit `60c8a2ab`) — new `StuckReasonOrphanedTriage` + a periodic detector (`reconcileOrphanedTriageItems`, wired into `ReconcileStuck`) flags idea-status items whose open triage session has gone stale (2h), self-heals once the item leaves idea. Frontend chip/label/order updated. 3 regression tests added, full `session` + `server/services` + frontend suites pass. Merged and deployed. |
| 9 | `ReconcileStuckItems` / `ArchiveBacklogItem` (per "Backlog History feature Broken" item's own planning notes) | Only `TransitionBacklogItemStatus` writes a `BacklogStatusEvent` audit row — these two paths mutate status directly via ent and skip event creation, extending the known notify-once-state gap. **Not fixed here** — already in flight on session `stapler-squad-fix-backlog-status-audit-trail-r3` per live session state observed during this audit; do not duplicate. |
| — | Recurring pattern | Silent error-swallowing (`_ = s.storage.Update...(...)`) recurs across the service layer — same root cause as #2/#5/#6/#7. All four instances directly caused by this audit are now fixed individually; #8/#9 and any other occurrences remain — still worth a structural fix (e.g. a lint rule or wrapper) to close the class rather than continuing one-off fixes. |
| 1 | `autonomous_orchestration_service.go:225-230,137-138` | `inst.AutonomousMode/AutonomousTurn/AutonomousMaxTurns` mutated with no lock — concurrent turn/completion callbacks race on the same `*Instance`. Code comment explicitly warns against an uncoordinated fix here (pending "Epic 5" instance-actor-concurrency project) — **deliberately left unfixed**, needs that larger initiative, not a quick patch. |
| 10 | `backlog_service_triage.go` `AutoReopenForPRFix` + `hasActiveWorkSession` | **CRITICAL — FIXED, caught live.** User reported a pr_pending item's activity history "cycling every couple minutes with nothing changing." Root cause: `ReconcilePRPending`'s 60s tick calls `AutoReopenForPRFix` for any pr_pending item with failing CI, with no check for whether a fix is already in flight — it transitioned pr_pending→in_progress unconditionally, `SpawnSessionFromItem` rejected the spawn (`hasActiveWorkSession` guard, no liveness check), and it rolled back to pr_pending — 2 `BacklogStatusEvent` rows every tick, forever, growing the table unboundedly. Two fixes required: (1) commit `af426f27` — `tombstoneOrphanWorkSessions`, ends a work session confirmed dead via `IsSessionLive` before the guard runs (correct but *insufficient alone*: deployed 21:05:52, loop continued through 21:12:54 because the observed item's blocking session was a genuine 4-hour-old **still-active** autonomous session, not a dead one — `IsSessionLive` correctly reported it alive). (2) commit `f8f788ab` — `AutoReopenForPRFix` now checks `hasActiveWorkSession` *before* any status transition and returns early with zero churn when a fix is already in flight, regardless of whether the blocking session is alive or dead. Live-verified: last churn event 21:12:54 (pre-fix), zero churn since the 21:13:35 deploy, new early-return log line firing cleanly on both ticks checked after. 4 regression tests total across the two commits. Merged to `main`, deployed. |

All four regression tests added above pass, and the full `server/services` package test suite
(`go test ./server/services/...`) passes clean after each merge. All four fixes are merged to
`main` (commits `b04785e9`, `d83c4bac`, merge of `32953891`, merge of `11570a2c`) and live —
`make install-service` was run after each merge.

## [2] Manual Gates (could be policy-driven)

**Bucket closed 2026-07-15.** Re-auditing each item against the current code turned up that
most were already resolved by existing per-item flags or by the bucket-1 reconciliation work
above — building new toggles for them would have been redundant. One genuine gap remained
(re-review PASS not auto-transitioning) and is now fixed.

- `GateVerdictBox.tsx:273-281` — PASS verdict required a manual "Approve — Mark Done" click. **FIXED.** Turned out this was only reachable via `TriggerReReview`'s headless path — the *initial* headless review (`review_gate.go` `onPass`) already auto-advances on PASS (push+PR→`pr_pending`), and the tmux/MCP `submit_review_verdict` path already auto-transitions straight to `done`. Only re-review (triggered by clicking "Re-run Gate" on an UNVERIFIABLE verdict) saved the verdict and stopped, leaving the item sitting in `review` for a manual click. Fixed in `server/services/backlog_service_triage.go`'s `TriggerReReview`: on a PASS verdict it now transitions `review`→`done` directly (best-effort, same precondition pattern as `SubmitManualReview`/`submitReviewVerdict`), closing the one path that didn't already auto-advance. 1 regression test (`TestTriggerReReview_HeadlessPassAutoTransitionsToDone`). Merged and deployed.
- `TriageReviewPanel.tsx:224-247` — manual "Apply suggestions"/"Mark ready" click. **Already resolved by existing design, no fix needed.** `TriggerTriage`'s completion goroutine (`backlog_service_triage.go:807-822`) unconditionally applies the model's structured `AcceptanceCriteria` and transitions `idea`→`ready` in the same goroutine, regardless of whether a human ever opens the item. The panel only renders while `item.status === "idea"` (`BacklogItemDetail.tsx:604-605`), which in practice is a narrow race/failure window before that auto-transition lands — not the steady-state gate the original audit assumed. No further action needed.
- `BacklogItemDetail.tsx:842-851` — "Approve Plan" gates session spawn (`canSpawnSession`). **Already resolved.** `canSpawnSession = ready && (skipPlanning || planApproved)` already has a per-item bypass (`skipPlanning`, pre-existing), `canRunAutonomously` bypasses the gate entirely, and the new `AutoSpawnSession` (below) skips the click altogether by spawning with `Autonomous: true`, which itself bypasses plan approval server-side.
- `BacklogItemDetail.tsx:814-841` — "Spawn Session" vs "Run Autonomously" binary choice. **Already resolved by `AutoSpawnSession`** (see below) — when set, the item is always spawned autonomously and neither button ever needs a click.
- `BacklogItemDetail.tsx:997-1019` — "Return to Triage"/"Back to Ready" manual recovery buttons. **Left manual, deliberately.** These are the human-override escape hatch; the actual auto-recovery is bucket [1]'s reconciliation detectors (`reconcileStaleWorkSessions`, `reconcileBouncingItems`, `reconcileOrphanedTriageItems`, `selfHealStuck`), which already retry/resolve these states without a click. Adding a second automated trigger on top of these buttons would race against the detectors rather than add coverage.
- `autonomous_orchestration_service.go:203-276` — **FIXED (opt-in).** Triage→Ready did not auto-spawn a work session; required manual `SpawnSessionFromItem` — directly resolved the "5 items sitting in READY" observation below. Fixed on `main` (commit `b28ace2f`) — new per-item `AutoSpawnSession` bool (ent schema + proto + repository, default `false`); when true, `TriggerTriage`'s completion goroutine spawns a work session automatically (`Autonomous: true`) once the item reaches ready. Toggle exposed in `BacklogItemForm`. **Also found and fixed while wiring this in**: every partial `updateBacklogItem()` call in `BacklogItemDetail.tsx` (save notes, apply/undo triage suggestions, gate-reopen feedback) omitted `skipPlanning`/`skipReviewGate` entirely — since the backend writes these fields unconditionally on every update (plain proto3 `bool`, no "unset" wire representation), those actions were silently resetting both flags to `false` on every save. Added a `currentFlags()` helper spread into all 4 call sites. 2 regression tests for the toggle. Merged and deployed.
- `backlog_service_lifecycle.go:768-770` (`SubmitManualReview`) — Done transition needs `SkipReviewGate` or manual `SubmitManualReview`. **Already resolved.** `SkipReviewGate=true` already transitions `in_progress`→`done` directly at the source (`session/backlog_lifecycle.go:394-397`), never entering `review` at all. `SubmitManualReview` is the correct manual fallback for when automated review is unavailable/degraded (no headless pool, diff computation blocked) — that's a legitimate "needs a human or an LLM to judge it" gate, not a missing policy toggle.
- `github_service.go:167` — merge is an explicit manual RPC; auto-merge is best-effort and silently falls back when branch protection is absent. **Not fixed** (the *silent* part of this — no notification on fallback — was already fixed separately, see bucket [1]/commit `47bbe05d`; making auto-merge itself more aggressive is a separate policy decision, not addressed here).
- **UI**: Trigger Triage does not auto-fire when an item becomes READY (contradicts ADR-022's "autonomous" framing) — 5 items observed sitting in READY waiting on a manual click during this audit. REVIEW-column cards similarly need a manual "View Review" click. **Partially addressed** — the READY→spawn gap now has an opt-in fix (see `AutoSpawnSession` above); the triage-trigger-on-idea gap is unaddressed.
- Stuck-reason resolution logic is duplicated across handlers instead of centralized.
- **Found live during Phase 5 of this audit**: `mcp__stapler-squad__create_session` (`session_type: new_worktree`) creates the tmux session and git worktree successfully, but logs `"skipping controller startup, will be started after wiring"` and the controller never wires up on its own — `steer_session`/`run_command`/`write_to_session` all fail with `"cannot send keys to instance that has not been started or is paused"` minutes later, until a UI client opens the session. This means MCP-driven session creation (the mechanism a fully autonomous pipeline would need to spawn its own workers) currently requires a human to open the web UI before the new session becomes drivable — the same manual-gate pattern as everything else in this bucket, but in the orchestration layer itself rather than the backlog UI. **Not fixed** — the wiring/controller-startup gap itself is a deeper orchestration-layer investigation, out of scope for this pass.
- **[1: bug, FIXED] Found and fixed while investigating the above**: destroying a session via `stop_session` (which routes through `GitWorktree.Cleanup()`) silently deleted the git *branch*, not just the worktree — undocumented in the tool description. Reproduced live: cleaning up the two sessions used for fixes #2/#3 above deleted both fix branches entirely; recoverable only because git hadn't garbage-collected the objects yet. The package-level `CleanupWorktrees()` (used by the storage-reset CLI command) had the identical bug via `git branch -D`. Fixed on `main` (commit `a1e8efdf`) — both now only remove the worktree, never the branch. Regression test `TestCleanup_PreservesBranchWithCommits` added. Merged and deployed.

## [3] Non-Configurable Pipeline Steps (the core "software factory" gap)

This is the primary blocker on the stated goal — confirmed independently by architecture
review, UX review, and the is-it-ready pass:

- `session/repository.go:330-357` (`BacklogItemData`) — only `SkipReviewGate`/`SkipPlanning` bools exist. **No field anywhere for a per-item skill/command list.** This is the data-model layer that has to change first.
- `session/backlog_commands.go:20-100` (`WriteSlashCommands`) — every item gets the identical fixed slash-command set. OCP violation: adding a pipeline mode means editing this function, not extending it.
- `docs/adr/013-workflow-engine-replaces-valid-transitions.md` — proposed `ConfiguredWorkflowEngine` (DB-persisted, supports custom states) **was never implemented.** `DefaultWorkflowEngine` is the only implementation; `WorkflowEngine.CanTransition`/`ValidateGates` govern status-transition *guards* only, not stage/skill *selection*. The seam everyone assumed exists doesn't cover this.
- `session/backlog_lifecycle.go:535` / `session/review_gate.go:320-322` — review-pass callback hardcoded to `pushAndCreatePR`; no seam for alternative post-review stages.
- `session/autonomous_driver.go:336-341` — orchestration prompt/signals hardcoded; no pluggable strategy per item.
- `web-app/src/components/backlog/BacklogBoard.tsx:15-21` — pipeline stages hardcoded in the UI's `COLUMNS` array.
- No UI element anywhere shows or lets a user select which pipeline/skills ran or will run for an item — confirmed by UX review across all four backlog UI surfaces reviewed.
- `backlog_service_triage.go:72-97` — `maxAutoReworkIterations`, `maxConcurrentBacklogWorkItems`, `defaultTriageCleanupTimeout` are global constants despite in-code comments calling them "operational tuning knobs."

**Positive pattern to reuse**: `session/workflow_engine.go`'s narrow-interface +
deep-copy-on-construct design (good DIP/ISP, matches this repo's anti-interface-pollution
convention) is a solid template to clone into a `PipelineEngine` interface for per-item
skill/stage configuration, rather than inventing a new abstraction style.

## Update — 2026-07-17 (later): opt-in "auto-create PR on Complete" policy (closes recommended action #2)

Closes recommended action #2 from the "Why nothing is reaching merge" update above: added an
opt-in, per-item `AutoCreatePR` policy flag so PR creation can skip the manual Review Queue
"Create PR" click entirely for items that opt in — while leaving the manual click as the
default, deliberate human-review-the-prompt checkpoint for everyone else. Confirmed the
trust trade-off with the user first, as that update flagged.

**Data model / RPC** — followed the exact `SkipReviewGate`/`SkipPlanning`/`AutoSpawnSession`
precedent (same file, same unconditional-bool-wrap pattern) rather than inventing a new
convention: `session/ent/schema/backlog_item.go` (`auto_create_pr` field, default false) →
`proto/session/v1/backlog.proto` (`BacklogItem.auto_create_pr = 26`,
`CreateBacklogItemRequest.auto_create_pr = 12`, `UpdateBacklogItemRequest.auto_create_pr = 14`)
→ `session/repository.go` (`BacklogItemData.AutoCreatePR`, `BacklogItemUpdate.AutoCreatePR`) →
`session/ent_repository_backlog.go` → `server/services/backlog_service.go` /
`backlog_service_lifecycle.go`. Exposed in `BacklogItemForm.tsx` alongside the existing three
toggles, and added to `BacklogItemDetail.tsx`'s `currentFlags()` helper (the fix from the
`AutoSpawnSession` entry above) so partial updates from that panel don't silently reset it.

**Trigger point** — the manual flow is `ReviewQueuePanel.tsx`'s "Create PR" button →
`RunOneShot` RPC (`server/services/session_service.go:3405`), gated on a session reaching
`AttentionReason.TASK_COMPLETE` in the Review Queue. Rather than polling, hooked the existing
`ReviewQueue.Subscribe`/`OnItemAdded` observer callback (`server/review_queue_manager.go`) —
already fired exactly once when a session newly enters the queue (not on every poll tick,
per `session/queue/queue.go`'s `exists` check), and already the hook point for the
TASK_COMPLETE push notification. Added `maybeAutoCreatePR`: on a new TASK_COMPLETE item, if
the session resolves to a backlog item (via `Storage.GetItemSessionBySessionUUID` →
`Storage.GetBacklogItem`) with `AutoCreatePR = true` and no existing PR URL, it runs the same
one-shot prompt the manual modal pre-fills (`DEFAULT_PR_PROMPT` in `ReviewQueuePanel.tsx`,
mirrored server-side as `autoCreatePRPrompt`) via a new `RunOneShotForSession` method on
`SessionService` — a thin wrapper around the existing `RunOneShot` RPC logic, so automated and
manual PR creation share one code path (same PR-URL extraction, same PR persistence).

**Wiring**: `OneShotPRCreator` is a narrow interface defined in `server` (the consumer),
satisfied by `*services.SessionService` — per this repo's anti-interface-pollution convention.
Wired via a post-construction `SetOneShotRunner` setter in `server/dependencies.go` (mirrors
the existing `SetHeadlessPool` pattern) rather than a constructor parameter, to avoid touching
the 6+ existing `NewReactiveQueueManager(...)` call sites across `server/review_queue_manager_test.go`.
Runs in a goroutine so a slow/failing LLM call never blocks queue-add notification delivery.

**Tests**: `server/review_queue_manager_test.go` —
`TestMaybeAutoCreatePR_RunsOneShot_When_AutoCreatePREnabled` (flag on: verifies the one-shot
runner is invoked with the session's stable UUID) and
`TestMaybeAutoCreatePR_DoesNothing_When_AutoCreatePRDisabled` (flag off, the default: verifies
zero calls — the manual click path is unchanged). `server/services/backlog_service_lifecycle_test.go` —
`TestCreateBacklogItem_should_DefaultAutoCreatePrToFalse_When_FieldOmitted` and
`TestCreateBacklogItem_should_PersistAutoCreatePr_When_FieldSetTrue` (Create/Update round-trip).
`web-app/src/components/backlog/BacklogItemForm.test.tsx` — new `describe` block covering the
checkbox's default-unchecked state and both submit-payload values. Full `go test ./server/...
./session/...` and `cd web-app && npx jest --no-coverage` pass. No e2e spec added — the
`AutoSpawnSession` precedent this mirrors also has none, only Go + Jest coverage, so this
follows the same established pattern rather than introducing a new expectation.

## is-it-ready Verdict (provisional)

**⚠️ FIX THEN SHIP** — Goal Compliance 🔴, Architecture 🔴, Code Quality 🟡, Operational
Readiness 🔴. Test Quality, Security, and Product/UX dimensions did not complete in this run
(background subagent stall) — re-run before treating this verdict as final. If Security comes
back 🔴 this escalates to 🛑 HOLD per the skill's own criteria.

## Known Coverage Gap

The `code:review` pass read `autonomous_orchestration_service.go` in full but did not get
deep coverage of `backlog_service_triage.go` (`SpawnSessionFromItem`, `TriggerReReview`,
`AutoReopenAfterFailedReview`, `AutoReopenForPRFix`) or `workflow_engine.go` in this run —
flagged for a follow-up pass, since `SpawnSessionFromItem`/`TriggerTriage` are the two
highest-complexity functions in the whole subsystem.

## Skill Fix Needed

`backlog-feature-improvement`'s Phase 1 step 4 (DB cross-check against
`~/.stapler-squad/sessions.db`) is unreliable — the DB observed was stale by two weeks. Update
the skill to treat the RPC/MCP tools as authoritative and drop or caveat the direct-DB step.

## [1] Reconciliation Bugs — one more found during cleanup

- **`mcp__stapler-squad__stop_session`'s tool description says it removes "its tmux process
  and git worktree" but it also deleted the git *branch* — undocumented, and a real risk for
  local commits that haven't been pushed.** Reproduced live: after implementing and committing
  both CRITICAL fixes above on their own worktree branches, calling `stop_session` to clean up
  the (separately broken — see the controller-wiring finding above) sessions deleted
  `fix/autonomous-status-notification-race` and `fix/autonomous-swallowed-lookup-errors`
  entirely. The commits survived only because git hadn't garbage-collected the now-unreachable
  objects yet (`git cat-file -t <hash>` still resolved; recovered via `git branch <name>
  <hash>`). Had `git gc` run first, both fixes would have been permanently lost. This is the
  same class of bug as everything else in this bucket — a destructive path with no warning,
  discovered by using the tool as documented.

## Update — 2026-07-18: "Ship PR" self-service action on the item detail page

Closes the live gap reported directly against the UI: a backlog item sitting at status=`review`
with all acceptance criteria complete and a PASS-looking gate verdict (screenshot: "Dedent
shortcut broken in edit mode", 6/6 AC, PASS) had **no button anywhere on the item detail page**
to ask the agent to ship a PR — the only Actions shown were Override → Done, Re-review, Submit
Review, Restart, ↩ Return to Triage, ↩ Back to Ready, Delete. Verified every fact this update's
briefing assumed against current `main` first (all still accurate): `AutoCreatePR` (opt-in,
default `false`) and `RunOneShotForSession` (the 2026-07-17 update above) are both live and
merged; `RecordPRCreatedOutOfBand` still only reconciles a PR back onto the item when
`item.Status == review`; `GetBacklogItemShipStatus` **does not exist** anywhere in the tree —
there is no pre-built "is this item ready to ship" helper, so readiness is computed inline in
the new UI code (see below) rather than delegated to a nonexistent RPC.

**Fix — self-service action**: added a "🚀 Ship PR" button to `BacklogItemDetail.tsx`'s Actions
panel, visible when `item.status === "review" && !item.prUrl` (first button in that block,
alongside Override → Done / Re-review / Submit Review / Restart). Disabled — with an
explanatory `title` — until all acceptance criteria are `done`; does **not** require a PASS
gate verdict, matching the existing human-override philosophy of "Override → Done" (a reviewer
can still force-ship off an UNVERIFIABLE/PARTIAL verdict). Wired to a new `TriggerShipPR` RPC
(`proto/session/v1/backlog.proto`, handler in the new `server/services/backlog_service_ship.go`)
that resolves the item's most recent work-role `ItemSession` (reusing `findMostRecentSessions`,
the same helper `TriggerReReview` uses) and delegates to `RunOneShotForSession` — the *same*
one-shot PR-creation mechanism the opt-in `AutoCreatePR` policy and the Review Queue's manual
"Create PR" button already use, per this doc's own precedent against introducing a second writer
of `pr_pending` (PR #160's bug). No new PR-creation code path was written; `TriggerShipPR` is a
thin resolve-the-session-then-delegate handler, wired into `BacklogService` via a new narrow
`PRRunner` interface (mirrors `server.OneShotPRCreator`, satisfied by `*services.SessionService`)
and a `SetOneShotRunner` setter, following the exact `SetSessionStopper`/`SetHeadlessPool`
setter-injection precedent already used throughout `backlog_service.go`. Because this reuses
`RunOneShotForSession`, `RecordPRCreatedOutOfBand`'s existing reconciliation still handles the
`review`→`pr_pending` transition once a PR URL is extracted — no new transition logic needed.
Scoped to `status == review` only (not extended to `done`) — see the "second root cause" below
for why.

**Investigation — "why isn't this happening automatically"**: this is genuinely two separate,
compounding causes, not one:

1. **The known, working-as-designed cause**: `AutoCreatePR` defaults to `false` per item and
   most items (including the screenshot's) never opt in — the 2026-07-17 update's own UX gap.
   The Ship PR button above closes this half without changing the opt-in default (still a
   deliberate human-review-the-prompt checkpoint, per that update's stated trust trade-off).

2. **A second, previously-undocumented live bug, found while tracing every PASS-verdict code
   path per this update's brief**: `SubmitManualReview` (`server/services/backlog_service_lifecycle.go`)
   and `TriggerReReview`'s headless-PASS branch (`server/services/backlog_service_triage.go`)
   both transition `review`→`done` **directly via the storage layer**
   (`s.storage.TransitionBacklogItemStatus`), not through the guarded
   `TransitionBacklogItemStatus` **RPC handler**
   (`server/services/backlog_service_lifecycle.go`'s `TransitionBacklogItemStatus` method, the
   one the frontend's generic `transitionStatus()` calls for `mark_done`/`send_back_*`/etc.).
   Only that RPC handler enforces `ErrPRRequired` — the guard that blocks `review`→`done` when a
   work session has committed code (`LastCommitSha != ""`) but `item.PrURL == ""` (see
   `session/domain/backlog.go`'s `TransitionGuard`, `from == BacklogStatusReview && to ==
   BacklogStatusDone` case). Both bypassing call sites could therefore mark an item **done**
   while its work session's commits were never pushed or turned into a PR at all — silently
   losing the ship step, with no PR, no `pr_pending` stop, and (before this fix) no way to
   recover short of manually re-opening the item and finding the Review Queue page. Confirmed
   this is real (not a false positive) by reading `TransitionGuard`'s guard and tracing that
   `s.storage.TransitionBacklogItemStatus` is the raw storage-layer method, not the RPC handler
   — the storage layer has no knowledge of `ErrPRRequired` at all.

   By contrast, `handleReviewSessionExited` (`session/backlog_lifecycle.go`, the path driven by
   the tmux review session's process actually exiting after calling the `submit_review_verdict`
   MCP tool) is unaffected — it always calls `pushAndCreatePR` on PASS, which pushes the branch
   and creates the PR itself before ever reaching `done`.

   **Fix applied**: added `hasUnshippedWorkSessionCode` (`server/services/backlog_service_ship.go`),
   a small helper replicating the RPC handler's exact guard check, and call it from both bypassing
   sites before their PASS→done transition. When it reports unshipped code, the item now stays in
   `review` (verdict still persisted, matching the existing best-effort comment pattern) instead
   of silently reaching `done` — the new Ship PR button is the intended recovery path once left
   there. Chose this over reworking the two call sites to invoke `pushAndCreatePR` directly (that
   function lives on `*session.BacklogLifecycleListener`, unexported, and `BacklogService` has no
   reference to it — plumbing one through would be a materially larger, riskier change to two
   already-tested code paths for this pass) or auto-firing the one-shot ship flow synchronously
   inside the RPC handler (would make `SubmitManualReview`/`TriggerReReview` block for up to 15
   minutes on a click that previously returned immediately — a real UX regression). The chosen
   fix is synergistic with the button above: item stays in review with a recorded PASS verdict,
   6/6 AC, and now a working "Ship PR" action, i.e. exactly the reported screenshot scenario,
   self-service-recoverable.

   **Deliberately not fixed here (follow-up)**: items that already reached `done` with unshipped
   code *before* this fix (a real possibility on `main` prior to this update) have no PR and no
   recovery button, since Ship PR is scoped to `status == review`. Extending it to `done` would
   require also relaxing `RecordPRCreatedOutOfBand`'s status guard (currently only reconciles
   `review`→`pr_pending`) and confirming `done`→`pr_pending` is a sane state-machine transition —
   out of scope for this pass; flagged for a follow-up rather than guessed at here.

**Files touched**: `proto/session/v1/backlog.proto` (`TriggerShipPRRequest/Response` + RPC) →
`server/services/backlog_service_ship.go` (new: `PRRunner` interface, `SetOneShotRunner`,
`TriggerShipPR` handler, `hasUnshippedWorkSessionCode` helper) → `server/services/backlog_service.go`
(`oneShotRunner` field) → `server/services/backlog_service_lifecycle.go` (`SubmitManualReview`
guard) → `server/services/backlog_service_triage.go` (`TriggerReReview` headless-PASS guard) →
`server/dependencies.go` (`backlogSvc.SetOneShotRunner(sessionService)`, mirroring
`reactiveQueueMgr.SetOneShotRunner(sessionService)`) → `web-app/src/lib/hooks/useBacklogService.ts`
(`triggerShipPR`) → `web-app/src/components/backlog/BacklogItemDetail.tsx` (button + `handleAction`
case) → `tools/scanner/backend/proto_scanner.go` (`methodToID["TriggerShipPR"]`, required for the
feature-registry scanner to resolve the `+api:` marker to `backlog:trigger-ship-pr` instead of a
raw-method-name fallback).

**Tests**: `server/services/backlog_service_ship_test.go` — 6 tests covering `TriggerShipPR`'s
happy path (resolves the correct work session, delegates to `PRRunner`), and rejection paths
(not in review, already has a PR, no work session, `PRRunner` unwired, runner error).
`server/services/backlog_service_lifecycle_test.go` —
`TestSubmitManualReview_PassNoUnshippedCode_TransitionsToDone` (regression guard: nothing-to-ship
PASS must still auto-transition, unchanged) and
`TestSubmitManualReview_PassWithUnshippedCode_StaysInReviewForShipPR` (the new guard).
`server/services/backlog_service_test.go` —
`TestTriggerReReview_HeadlessPassWithUnshippedCode_StaysInReviewForShipPR` (pairs with the
pre-existing `TestTriggerReReview_HeadlessPassAutoTransitionsToDone`, which continues to pass
unchanged since its fixture has no work session at all). Frontend:
`web-app/src/components/backlog/BacklogItemDetail.shipPR.test.tsx` (new — button visibility
across status/PR/AC-completion combinations, click wiring) plus `triggerShipPR` added to the two
existing `BacklogItemDetail.*.test.tsx` mocks. Full `go test ./server/... ./session/...`
(4419 tests) and `cd web-app && npx jest --no-coverage` (2994/3001 — the 7 failures are
pre-existing, confirmed unrelated via `git stash` against a clean `main` checkout) pass. No e2e
spec added — follows the same established pattern as `AutoSpawnSession`/`AutoCreatePR` (Go +
Jest only, no e2e), per `.claude/rules/feature-registry.md`'s existing precedent in this file.
