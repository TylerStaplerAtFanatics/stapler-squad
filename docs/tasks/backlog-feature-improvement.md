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

- `GateVerdictBox.tsx:273-281` — PASS verdict still requires a manual "Approve — Mark Done" click; no auto-approve-on-PASS policy.
- `TriageReviewPanel.tsx:224-247` — manual "Apply suggestions"/"Mark ready" click required after triage.
- `BacklogItemDetail.tsx:842-851` — "Approve Plan" gates session spawn (`canSpawnSession`); only escape hatch is the static `skipPlanning` flag.
- `BacklogItemDetail.tsx:814-841` — "Spawn Session" vs "Run Autonomously" is a binary choice made by clicking at spawn time, not a stored per-item policy.
- `BacklogItemDetail.tsx:997-1019` — "Return to Triage"/"Back to Ready" are manual recovery buttons — UI symptom of the reconciliation-loop bugs above; no auto-retry/auto-requeue policy.
- `autonomous_orchestration_service.go:203-276` — triage→Ready does not auto-spawn a work session; requires manual `SpawnSessionFromItem`.
- `backlog_service_lifecycle.go:768-770` — Done transition needs `SkipReviewGate` or manual `SubmitManualReview`.
- `github_service.go:167` — merge is an explicit manual RPC; auto-merge is best-effort and silently falls back when branch protection is absent.
- **UI**: Trigger Triage does not auto-fire when an item becomes READY (contradicts ADR-022's "autonomous" framing) — 5 items observed sitting in READY waiting on a manual click during this audit. REVIEW-column cards similarly need a manual "View Review" click.
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
