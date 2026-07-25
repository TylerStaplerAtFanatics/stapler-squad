# Adversarial Review: backlog-session-thrashing

**Date**: 2026-07-25
**Verdict**: BLOCKED

Reviewed against the actual code in the worktree (not just the plan's own citations):
`session/autonomous_driver.go`, `server/services/autonomous_orchestration_service.go`,
`server/services/backlog_service_triage.go`, `server/services/backlog_service.go`,
`server/services/autonomous_orchestration_service_test.go`,
`server/services/backlog_service_test.go`, `session/backlog_lifecycle.go`,
`server/services/session_service.go`, `server/mcp/tools_backlog.go`, `go.mod`.

## Blockers

- [ ] **Epic 3.1's blanket early-return on `DriverExitContextCancelled` regresses BUG-048's
  fix for abandoned review sessions.** The task says the early return fires "for every role
  (triage, work, review), not just work," before the role-specific switch runs. But the
  *only* confirmed real production caller of `Stop()`/`StopDriverForSession` outside the
  driver's own natural-completion path is `submit_review_verdict`
  (`server/mcp/tools_backlog.go:535-539`), which calls it as a documented
  "belt-and-suspenders" stop **while the review driver may still be actively running**, and
  its own comment says explicitly: *"a subsequent Stuck fireCompletion is harmless because
  the role-aware callback skips transitions for SessionRoleReview."* That comment is true
  only under the *current* code: `onAutonomousDriverComplete`'s `SessionRoleReview` switch's
  `default` branch (still-genuinely-stuck case, `autonomous_orchestration_service.go:449-477`)
  calls `UpdateItemSessionEnded` on the review `ItemSession` row — this is the exact fix
  BUG-048 shipped so an abandoned review session (driver stopped, but the underlying tmux
  session never killed — confirmed true for both turn-cap and Stop()-triggered exits, since
  neither kills the tmux pane) becomes visible to the `abandoned_review` detector, which
  explicitly excludes any item with an `EndedAt`-nil review session
  (`FindStuckReviewItems`/`session/storage_backlog.go`, per the comment at
  `autonomous_orchestration_service.go:461-463`). Item status is still `review` at the moment
  this fires — `submit_review_verdict` deliberately does no status transition itself
  (comment at `tools_backlog.go:523-530`) — so under the current code this path lands in
  exactly the "still genuinely stuck" branch and ends the row every time a verdict is
  submitted while the driver is still mid-loop. Epic 3.1's early return skips this entirely,
  so the row is never ended, the item silently drops out of both the stuck-marking system
  (no `MarkStuck`) and the `abandoned_review` detector's reach (row stays open forever) — a
  "vanished/forgotten item," which is precisely what requirements.md's success metric says
  the redesign must avoid. **Recommendation**: don't blanket-skip the role-specific switch
  for `DriverExitContextCancelled`. Skip the stuck-marking/notification (that part is
  correctly motivated — an intentional stop isn't "stuck"), but still run each role's
  existing row-resolution bookkeeping, or explicitly special-case `SessionRoleReview` to
  preserve BUG-048's `UpdateItemSessionEnded` behavior regardless of `ExitKind`.

- [ ] **Relocating `spawnInFlight` into `spawnSessionAfterGates` narrows the guarded critical
  section — it is not the claimed "unchanged behavior, just one call-frame deeper."** In the
  current code the guard (`SpawnSessionFromItem` step "1b") is acquired immediately after
  loading the item and held for the *entire* rest of the function: force-reset (step 2,
  `forceResetItem`), status validation (step 3), the planning gate (step 3b), and the WIP-cap
  gate/queueing decision (step 4, `queueBacklogItem`) all run inside the guarded section
  today. Moving the guard to the top of `spawnSessionAfterGates` (Task 1.1.1b) removes all of
  those steps from any per-item serialization. Concretely: (1) `forceResetItem` calls
  `TransitionBacklogItemStatus(... review -> in_progress ...)` with **no precondition at
  all** (`nil`, `backlog_service_triage.go:837`) — two concurrent `Force=true` calls for the
  same item can now run this concurrently, unguarded, where before the second call would
  have failed fast with `CodeAlreadyExists` before ever reaching it; (2) two concurrent
  fresh-spawn calls that both observe the WIP cap as not-yet-hit can now both reach
  `queueBacklogItem` concurrently (previously impossible — the second would already have
  been rejected at the guard). The existing regression test
  (`TestSpawnSessionFromItem_ConcurrentSpawns_OnlyOneWorkSessionCreated`,
  `backlog_service_test.go:1550`) still passes, but only because it doesn't exercise either
  of these paths (one `ready` item, no `Force`, WIP cap nowhere near its limit) — all 8
  racers still serialize correctly at the (now-deeper) guard before ever creating a session,
  since none of them races through `forceResetItem`/`queueBacklogItem` in that specific
  scenario. Its passing is not evidence the narrower guard is safe in general, and Story
  1.1.1's acceptance criterion ("the guard's effective behavior for the
  `SpawnSessionFromItem`-only race is unchanged, just relocated one call-frame deeper") is
  factually incorrect as written. **Recommendation**: either keep a lightweight per-item
  guard around the *entire* `SpawnSessionFromItem` body in addition to (or instead of) the
  deeper one in `spawnSessionAfterGates`, or explicitly analyze/accept the narrower race
  window with reasoning — don't ship the incorrect equivalence claim as-is.

## Concerns

- [ ] **Epic 3.3's safety claim doesn't hold for the `RemediateStaleWorkSession` call site.**
  The plan justifies `AutoRespawnAutonomousWork` unconditionally killing+ending an "active"
  work session by asserting "the driving mechanism is guaranteed to have already stopped by
  the time this code runs" for both call sites. True for `onAutonomousDriverComplete`
  (`stopAndDeregisterDriver` runs at function entry, `autonomous_orchestration_service.go:231`).
  Not demonstrated for `RemediateStaleWorkSession`
  (`backlog_service_triage.go:1343-1397`): that function is triggered purely by
  `ItemSession.LastProgressAt` staleness (`maxWorkSessionStaleness` = 2h, a git-commit/
  file-touch signal, `session/backlog_lifecycle.go:1998-2002`) — a completely independent
  signal from whether an `AutonomousDriver` is still alive and actively injecting turns
  (`RemediateStaleWorkSession` never calls `stopAndDeregisterDriver` or checks driver
  liveness). Phase 2's own soft/hard-cap change extends the effective turn budget up to 3x
  based on *terminal* activity (`GetTimeSinceLastMeaningfulOutput`) — a different signal than
  *git* progress — which makes it more, not less, likely that a still-actively-driven session
  (lots of terminal chatter, zero commits) simultaneously trips the independent 2h
  git-staleness threshold while a driver is mid-turn. This gap pre-dates this plan (the
  existing `RemediateStaleWorkSession` already does an unconditional kill), so it's not a new
  bug introduced here, but the plan's blanket safety assertion is inaccurate as written and
  should be corrected/scoped to call site (a) only, with the pre-existing risk at call site
  (b) flagged explicitly rather than silently assumed away — especially since Phase 2
  arguably makes it more likely to matter in practice.

- [ ] **Task 3.3.1a's "best-effort" kill can leave two tmux panes alive for one item.** The
  task explicitly specifies "log-and-continue on error" for the `KillTmuxPaneOnly` call, then
  unconditionally proceeds to end the DB row and spawn a new session regardless of whether
  the kill actually succeeded. `SessionService.KillTmuxPaneOnly` (`session_service.go:567-577`)
  takes no timeout and doesn't even use the passed `ctx` — it's a synchronous
  `inst.KillSession()` call. If it fails or hangs (a documented live hazard — see
  `docs/bugs/open/BUG-042` / `.claude/rules/tmux-keep-server-on-restart.md`'s orphaned
  control-mode client scenario, which requirements.md explicitly calls out as adjacent
  fragility to check for entanglement), the old pane — and its `AutonomousDriver`, since
  nothing in this path stops it either — can remain alive on the SAME git worktree/branch a
  reopen spawn reuses (`spawnSessionAfterGates` step 10, "backlog/<item>" branch shared across
  reopens). That's two live agents on one worktree/branch — the exact "duplicate work session
  for one item" shape this whole project exists to eliminate, reintroduced via the kill's
  best-effort escape hatch. Recommend failing the respawn (not proceeding to spawn) when the
  kill errors, or polling `IsSessionLive` to confirm the pane is actually gone before
  proceeding.

- [ ] **No task or test verifies Phase 2's three loop-modifying epics compose correctly.**
  Epics 2.1 (ExitKind + accurate `Turns`), 2.2 (malformed-response sub-cap), and 2.3
  (progress-adaptive soft/hard cap) each modify the same ~80-line loop in
  `AutonomousDriver.run()`, described as three separate sequential prose diffs — Task 2.3.1b
  in particular describes converting the bounded `for turnCount := 0; turnCount < d.maxTurns;
  turnCount++` into a manual-break loop with an extension check, but the plan never shows a
  single consolidated listing of the loop's end state after all three epics land. No task
  tests the interaction (e.g., a malformed-response streak of exactly 3 occurring right as
  `effectiveMaxTurns` would otherwise extend). Given this plan is meant for subagent-per-task
  execution (this repo's `subagent-driven-development` convention), where different tasks may
  be implemented somewhat independently, this is a real integration-risk gap. Recommend the
  plan include a consolidated final-state code listing for the loop, plus at least one test
  exercising the malformed-cap/soft-cap interaction.

- [ ] **Task 1.1.2a's regression test is not deterministic, and the plan accepts that.** The
  task itself admits "the race may not trigger the double-spawn on every run pre-fix," and
  the only verification step offered is a manual, one-time "temporarily revert the fix
  locally, confirm the test fails, then re-apply" during code review — not an automated,
  repeatable gate. That doesn't meet this repo's "no completion claim without proof" bar for
  a durable regression test: a later refactor could silently reintroduce the exact TOCTOU gap
  with no CI signal. Recommend an injectable pause hook (a test-only channel/callback that
  pauses `DequeueNextQueuedItems` between its CAS claim and its `spawnSessionAfterGates` call)
  to make the race deterministic — this codebase already has precedent for exactly this
  pattern (`paneSettlePollInterval`/`paneSettleMaxWait` in `session/autonomous_driver.go` are
  `var`s, not `const`s, specifically so tests can control timing).

- [ ] **The plan contains no explicit audit of "does every automated respawn call site funnel
  through the guard," despite requirements.md asking for exactly that.**
  `pitfalls.md` §7 names four call sites (`AutoReopenAfterFailedReview`,
  `AutoRespawnAutonomousWork`, `AutoReopenForPRFix`, and a fourth) and explicitly says "this
  project's design should explicitly verify every current and any newly-added respawn call
  site funnels through `SpawnSessionFromItem`." I traced this directly: all four call
  `s.SpawnSessionFromItem(ctx, ...)` (`backlog_service_triage.go:1218, 1309, 1494, 1924`), so
  they remain covered transitively after the relocation — it does hold today. But the plan
  itself contains no task that performs or records this audit, so there's no artifact for a
  future reviewer to check against, and no guard against a future call site that skips
  `SpawnSessionFromItem` entirely (writing to storage/session-creation directly) rather than
  calling `spawnSessionAfterGates`. Recommend adding a short verification task (or a comment
  audit trail) enumerating the call sites and confirming each funnels through the guard.

- [ ] **`stuckReasonForExitKind` has no specified default/fallback branch.** Task 3.2.1a
  lists per-kind text for the five named `ExitKind` values but doesn't specify what happens
  for an unrecognized or zero-value `ExitKind` (e.g., any `Stuck: true` outcome constructed
  without setting it — `DriverExitReason` is a plain typed string, not a closed/exhaustive
  enum, so nothing enforces "every `Stuck:true` outcome has a non-zero `ExitKind`" beyond
  code-review discipline). Recommend an explicit `default` case falling back to the original
  generic message, rather than relying solely on "should never happen in practice."

## Minors

- ADR-001's "hard cap ... an absolute ceiling" framing doesn't account for server restarts.
  Turn-count/soft-cap state lives only in the `AutonomousDriver` goroutine's local variables
  and is lost entirely on restart (confirmed via `stack.md`'s "in-flight AutonomousDriver
  goroutines are lost on restart," and no reattach/resume-on-restart mechanism was found in
  this review of `session/autonomous_driver.go` or the orchestration service). A restart
  mid-run resets the effective turn counter to 0 for whatever driver run comes next on the
  same item, so the "hard cap" is a per-process-lifetime ceiling, not a true absolute one
  across an item's full history. Worth a one-line caveat in the ADR (this doesn't block
  implementation — it's a documentation-accuracy nit, and restarts are already an accepted
  systemic characteristic per `.claude/rules/tmux-keep-server-on-restart.md`).
- Go 1.26.3 is confirmed in `go.mod`; `min()` builtin usage (Task 2.3.1b) is valid (available
  since Go 1.21). No issue.
- `DriverExitReason` as a typed string + const block matches this codebase's existing idiom
  (`session.BacklogStatus`, `session.SessionRole`, `domain.StuckReason*` are all the same
  pattern) — not a gratuitous new convention.
- Task 4.1.1a's framing ("fix any compile errors surfaced by the new `ExitKind` field") is
  imprecise: every `AutonomousDriverOutcome{...}` literal in the codebase uses named fields,
  so adding `ExitKind` causes zero compile breaks by itself. The actual risk at that task is a
  *runtime test-assertion* failure from Task 3.2.1a's reason-text change, not a compile
  error — checked this directly against `autonomous_orchestration_service_test.go` and
  confirmed only the two tests the plan already names (`..._MarksAutonomousStuck_When_NotDone`,
  `..._KeepsAutonomousStuck_When_WorkStillStuck`) assert on the old generic message text, so
  no stray test is missed — but the task's wording should say "test failures (compile or
  assertion)," not just "compile errors," so whoever executes it doesn't stop looking after a
  clean `go build`.
