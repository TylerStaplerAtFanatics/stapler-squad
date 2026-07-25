# Implementation Plan: backlog-session-thrashing

**Feature**: Close the per-item duplicate-work-session TOCTOU gap in `DequeueNextQueuedItems`, and redesign autonomous-driver turn-cap handling so genuine progress isn't punished, exit reasons aren't conflated into one "max turns reached" bucket, and a driver-abandoned session no longer silently blocks its own respawn for ~20-25 minutes.
**Date**: 2026-07-25
**Status**: Ready for implementation
**ADRs**: ADR-001-progress-adaptive-turn-budget.md

---

## Dependency Visualization

```
Phase 1 (dedup TOCTOU)                Phase 2 (driver-level exit classification + adaptive budget)
  Epic 1.1 ─────────────┐               Epic 2.1 (ExitKind + Turns fix)
    1.1.1a → 1.1.1b        │                 2.1.1a → 2.1.1b → 2.1.1c → 2.1.1d
    → 1.1.1c               │               Epic 2.2 (malformed-response sub-cap) ── depends on 2.1.1
  Epic 1.2                 │                 2.2.1a → 2.2.1b
    1.2.1a → 1.2.1b ───────┤               Epic 2.3 (progress-adaptive soft/hard cap) ── depends on 2.1.1
    (depends on 1.1.1)     │                 2.3.1a → 2.3.1b → 2.3.1c
                           │               Epic 2.4 (configurable base maxTurns) ── independent
                           │                 2.4.1a → 2.4.1b → 2.4.1c
                           │                          │
                           │                          ▼
                           │               Phase 3 (onAutonomousDriverComplete + respawn-delay fix)
                           │               depends on Phase 2 Epic 2.1 (needs ExitKind/Turns)
                           │                 Epic 3.1 (skip stuck-marking on cancellation)
                           │                   3.1.1a → 3.1.1b
                           │                 Epic 3.2 (kind-specific reason text)
                           │                   3.2.1a → 3.2.1b  (depends on 3.1.1)
                           │                 Epic 3.3 (close accidental respawn-delay gap)
                           │                   3.3.1a → 3.3.1b → 3.3.1c  (independent of 3.1/3.2)
                           │                          │
                           └──────────────────────────┴────────────┐
                                                                     ▼
                                                        Phase 4 (full regression pass)
                                                          4.1.1a → 4.1.1b
```

Phase 1 is fully independent of Phases 2-3 and can be implemented/reviewed/shipped separately if desired — it touches only `server/services/backlog_service*.go`. Phases 2-3 touch `session/autonomous_driver.go` and `server/services/autonomous_orchestration_service.go`/`backlog_service_triage.go` and should land together since Phase 3 depends on the `ExitKind`/`Turns` fields Phase 2 introduces.

---

## Phase 1: Close the DequeueNextQueuedItems TOCTOU Gap

### Epic 1.1: Relocate the `spawnInFlight` guard to the shared spawn chokepoint
**Goal**: Both entry points that can create a new work session for a backlog item (`SpawnSessionFromItem` and `DequeueNextQueuedItems`) acquire the *same* per-item mutual-exclusion guard at the *same* point, closing the confirmed race where a manual/automated `SpawnSessionFromItem` call can double-spawn during the narrow window between `DequeueNextQueuedItems`'s `queued→in_progress` CAS claim and its call into `spawnSessionAfterGates` (architecture.md §3b, confirmed live-incident-shaped, not merely hypothesized).

#### Story 1.1.1: Move the `spawnInFlight.LoadOrStore`/`Delete` guard from `SpawnSessionFromItem` into `spawnSessionAfterGates`
**As an** operator, **I want** every code path that can spawn a work session for a backlog item to share one dedup guard, **so that** the specific `DequeueNextQueuedItems` bypass documented in architecture.md §3b can no longer double-spawn.
**Acceptance Criteria**:
- `spawnSessionAfterGates` acquires `s.spawnInFlight.LoadOrStore(item.ID, struct{}{})` as its first action and releases via `defer s.spawnInFlight.Delete(item.ID)`; returns `connect.NewError(connect.CodeAlreadyExists, ...)` immediately if already held.
- `SpawnSessionFromItem`'s own guard block (current step "1b", `backlog_service_triage.go:354-368`) is removed — the function no longer acquires `spawnInFlight` itself, only its call into `spawnSessionAfterGates` does.
- `DequeueNextQueuedItems` (which calls `spawnSessionAfterGates` directly, `backlog_service_triage.go:575`) now transitively acquires the same guard with zero code changes to `DequeueNextQueuedItems` itself.
- Existing test `TestSpawnSessionFromItem_ConcurrentSpawns_OnlyOneWorkSessionCreated` (`backlog_service_test.go:1550`) still passes unmodified — the guard's effective behavior for the `SpawnSessionFromItem`-only race is unchanged, just relocated one call-frame deeper.
**Files**: `server/services/backlog_service_triage.go`

##### Task 1.1.1a: Remove the guard block from `SpawnSessionFromItem` (~3 min)
- In `server/services/backlog_service_triage.go`, delete the `spawnInFlight` guard block currently at lines 354-368 (the `// 1b. Atomic check-and-set...` comment through `defer s.spawnInFlight.Delete(item.ID)`).
- Leave the function's step numbering comments as-is for now (renumbered in Task 1.1.1c) — do not renumber mid-edit to keep the diff reviewable.
- Files: `server/services/backlog_service_triage.go`

##### Task 1.1.1b: Add the guard block to the top of `spawnSessionAfterGates` (~3 min)
- In `server/services/backlog_service_triage.go`, at the very start of `spawnSessionAfterGates` (currently line 596, before the existing "// 4b. Planning-gate defense-in-depth" comment at line 602), insert:
  ```go
  // Atomic check-and-set: only one spawn attempt for this item may be in flight
  // at a time, across BOTH entry points that reach this function — the
  // SpawnSessionFromItem RPC (and its Autonomous respawn/reopen callers) and
  // DequeueNextQueuedItems, which calls this function directly. Previously this
  // guard lived only in SpawnSessionFromItem, so DequeueNextQueuedItems's direct
  // call bypassed it entirely — a real, confirmed TOCTOU window (see
  // project_plans/backlog-session-thrashing/research/architecture.md §3b):
  // DequeueNextQueuedItems's CAS claims queued->in_progress, then in the gap
  // before this function's CreateItemSession call lands, a concurrent
  // SpawnSessionFromItem call for the same item sees status=in_progress
  // (isReopen=true), finds no ItemSession row yet, and spawns a second work
  // session. Moving the guard here closes that gap for both entry points at
  // once. Released via defer so every return path frees the item for the next
  // attempt.
  if _, alreadyInFlight := s.spawnInFlight.LoadOrStore(item.ID, struct{}{}); alreadyInFlight {
      log.InfoLog.Printf("[spawnSessionAfterGates] spawn already in flight for item=%s; rejecting concurrent attempt", item.ID)
      return nil, connect.NewError(connect.CodeAlreadyExists,
          fmt.Errorf("a session spawn is already in progress for this item; wait for it to finish"))
  }
  defer s.spawnInFlight.Delete(item.ID)
  ```
- Files: `server/services/backlog_service_triage.go`

##### Task 1.1.1c: Update doc comments referencing the guard's old location (~4 min)
- In `server/services/backlog_service.go`, update `spawnInFlight`'s field doc comment (lines 138-163) to say the guard is now acquired inside `spawnSessionAfterGates` (the shared callee for both `SpawnSessionFromItem` and `DequeueNextQueuedItems`), not inside `SpawnSessionFromItem` alone — and note this closes the `DequeueNextQueuedItems` gap described in `project_plans/backlog-session-thrashing/research/architecture.md`.
- In `server/services/backlog_service_triage.go`, update `DequeueNextQueuedItems`'s doc comment (lines 492-516, specifically the paragraph starting "That per-item CAS alone does not prevent...") to note that `spawnSessionAfterGates` now also serializes this call path against `SpawnSessionFromItem` via `spawnInFlight`, not just against itself via `dequeueMu`.
- Renumber `spawnSessionAfterGates`'s remaining inline step comments (currently starting at "4b.") if the new guard block's insertion makes the numbering confusing — optional, only if it aids readability.
- Files: `server/services/backlog_service.go`, `server/services/backlog_service_triage.go`

#### Story 1.1.2: Regression test for the `DequeueNextQueuedItems` vs. manual-spawn race
**As a** future maintainer, **I want** a test that would fail on the pre-fix code, **so that** this specific race class cannot silently regress.
**Acceptance Criteria**:
- A new test races concurrent calls to `DequeueNextQueuedItems` and `SpawnSessionFromItem` targeting the *same* queued item and asserts that, after all goroutines complete, `ListItemSessions` shows at most one open (`EndedAt == nil`) work-role `ItemSession` for that item.
- Test passes with `go test -race`.
- Test is documented as the regression test for architecture.md §3b, mirroring the doc-comment style of `TestSpawnSessionFromItem_ConcurrentSpawns_OnlyOneWorkSessionCreated`.
**Files**: `server/services/backlog_service_triage_test.go`

##### Task 1.1.2a: Add `TestSpawnSessionFromItem_RacesWithDequeue_OnlyOneWorkSessionCreated` (~5 min)
- In `server/services/backlog_service_triage_test.go`, following the pattern of `TestDequeueNextQueuedItems_should_ClaimOnlyOneItem_When_CalledConcurrentlyWithOneFreeSlot` (line 396) and `TestSpawnSessionFromItem_ConcurrentSpawns_OnlyOneWorkSessionCreated` (`backlog_service_test.go:1550`):
  - Create a `ready` item, spawn+queue it via `createReadyItemForSpawn` + `svc.queueBacklogItem` (SkipPlanning to avoid the planning gate) so it starts in `queued` status with one free WIP slot.
  - Launch `N` (e.g. 6) goroutines split evenly between calling `svc.DequeueNextQueuedItems(ctx)` and calling `svc.SpawnSessionFromItem(ctx, ...)` for the same item ID, released together via a shared start channel, run with `-race`.
  - After `wg.Wait()`, assert via `storage.ListItemSessions` that at most 1 work-role session has `EndedAt == nil`, and `len(creator.calls) <= 1` for that item (the mock `SessionCreator` only records calls it received — if the test's WIP setup allows more than one item, scope the assertion to sessions/calls tied to this specific item's worktree/title).
  - Because the exact TOCTOU window (`DequeueNextQueuedItems`'s CAS-claim-to-spawn gap) isn't independently controllable without an injected pause hook, accept that the race may not trigger the double-spawn on every run pre-fix — the test's purpose is to assert the *invariant* holds under concurrent pressure, not to deterministically force the exact interleaving; run it multiple times (`go test -race -count=20`) during review to build confidence it doesn't flake AND that it would have caught the pre-fix bug (temporarily revert Task 1.1.1a/b locally to confirm the test fails without the fix, then re-apply).
- Files: `server/services/backlog_service_triage_test.go`

---

## Phase 2: Driver-Level Exit Classification and Progress-Adaptive Turn Budget

### Epic 2.1: Typed exit-reason classification (`ExitKind`) + `Turns`-accuracy fix
**Goal**: `AutonomousDriverOutcome` distinguishes genuine turn exhaustion from context cancellation, LLM call errors, SendKeys failures, rate-limit-wait timeout, and startup timeout — the conflation identified in stack.md §1 and architecture.md §1 ("every non-DONE exit path is reported... as 'max turns reached'"). `Turns` reports the actual turn count reached instead of being hardcoded to `d.maxTurns`.

#### Story 2.1.1: Add `DriverExitReason` type + `ExitKind` field, set precisely at each exit point
**As an** operator, **I want** the Unfinished tab and stuck-reason text to say *why* a driver stopped, not just "max turns reached" for every non-DONE exit, **so that** I can tell a genuine turn-cap exhaustion apart from an orchestrator infra hiccup or an intentional stop.
**Acceptance Criteria**:
- `session.DriverExitReason` is a typed string with constants: `DriverExitDone`, `DriverExitMaxTurns`, `DriverExitContextCancelled`, `DriverExitLLMCallError`, `DriverExitSendKeysFailed`, `DriverExitRateLimitTimeout`, `DriverExitStartupTimeout`.
- `AutonomousDriverOutcome` gains an `ExitKind DriverExitReason` field.
- Every exit path in `run()` sets `ExitKind` to the exact reason it exited, and `Turns` to the actual `turnCount` reached (not `d.maxTurns`).
- Zero-value `ExitKind` (`""`) is never reached in practice for a `Stuck: true` outcome after this change — every break site sets it explicitly.
**Files**: `session/autonomous_driver.go`

##### Task 2.1.1a: Add the `DriverExitReason` type and constants (~2 min)
- In `session/autonomous_driver.go`, near the `AutonomousDriverOutcome` struct (currently lines 24-31), add:
  ```go
  // DriverExitReason classifies why AutonomousDriver.run() stopped without a
  // DONE signal. Previously every non-DONE exit (context cancellation, an LLM
  // call error, a SendKeys failure, a rate-limit-wait timeout, and genuine
  // turn-cap exhaustion) was reported identically as Stuck:true with
  // Reason:"max turns reached" — this type lets callers (onAutonomousDriverComplete)
  // treat an intentional stop differently from a genuine failure, and surface a
  // kind-specific reason to the operator instead of one conflated bucket.
  type DriverExitReason string

  const (
      DriverExitDone              DriverExitReason = "done"
      DriverExitMaxTurns          DriverExitReason = "max_turns"
      DriverExitContextCancelled  DriverExitReason = "context_cancelled"
      DriverExitLLMCallError      DriverExitReason = "llm_call_error"
      DriverExitSendKeysFailed    DriverExitReason = "send_keys_failed"
      DriverExitRateLimitTimeout  DriverExitReason = "rate_limit_timeout"
      DriverExitStartupTimeout    DriverExitReason = "startup_timeout"
  )
  ```
- Files: `session/autonomous_driver.go`

##### Task 2.1.1b: Add `ExitKind` field to `AutonomousDriverOutcome` (~1 min)
- In `session/autonomous_driver.go`, add `ExitKind DriverExitReason` to the `AutonomousDriverOutcome` struct (lines 25-31), with a doc comment cross-referencing `DriverExitReason`.
- Files: `session/autonomous_driver.go`

##### Task 2.1.1c: Set `ExitKind` and accurate `Turns` at every exit point in `run()` (~5 min)
- In `session/autonomous_driver.go`'s `run()` method:
  - Startup-timeout early return (line 184): add `ExitKind: session.DriverExitStartupTimeout` to the existing `AutonomousDriverOutcome{Stuck: true, Reason: "startup timeout"}` literal (this one is a same-package literal, no `session.` prefix needed).
  - `ctx.Err() != nil` break (line 193-195): before `break`, record `exitKind = DriverExitContextCancelled` (a new local var, see below).
  - `waitForRateLimitClear` error break (line 198-200): record `exitKind = DriverExitRateLimitTimeout`.
  - `CallBlocking` error break (line 211-214): record `exitKind = DriverExitLLMCallError`.
  - `SendKeys(nextMsg)` failure break (line 249-252) and submit-keystroke `SendKeys(EnterKeySequence)` failure break (line 254-257): record `exitKind = DriverExitSendKeysFailed`.
  - Natural loop exhaustion (falls out of the loop with no break): `exitKind` stays at its zero-initialized default, which should be set to `DriverExitMaxTurns` at loop entry (not left as `""`).
  - Add a local `exitKind := DriverExitMaxTurns` declaration right before the loop (so natural exhaustion needs no extra assignment) and a local `lastTurnCount := 0` updated at the top of each iteration (`lastTurnCount = turnCount`) so the post-loop fallthrough block (currently lines 270-276) can use `lastTurnCount` instead of the incorrect `d.maxTurns` for `outcome.Turns`.
  - Update the post-loop fallthrough (lines 270-276) to: `outcome = AutonomousDriverOutcome{Stuck: true, Reason: reason, Turns: lastTurnCount, ExitKind: exitKind}`.
  - The `DONE:` return path (line 223-234) already sets `Turns: turnCount + 1`; add `ExitKind: DriverExitDone` to that literal too (even though `Done: true` already implies it, for consistency when callers switch on `ExitKind` uniformly).
- Files: `session/autonomous_driver.go`

##### Task 2.1.1d: Unit tests for each `ExitKind` classification (~5 min)
- In `session/autonomous_driver_test.go`, following the existing `fakeHeadlessPool`/`TestAutonomousDriver_*` conventions (e.g. `TestAutonomousDriver_MaxTurnsLimit`, `TestAutonomousDriver_Stop_CancelsLoop`):
  - `TestAutonomousDriver_ExitKind_MaxTurns_When_LoopExhaustsNaturally`: existing max-turns setup, assert `outcome.ExitKind == DriverExitMaxTurns` and `outcome.Turns == maxTurns`.
  - `TestAutonomousDriver_ExitKind_ContextCancelled_When_StopCalled`: mirror `TestAutonomousDriver_Stop_CancelsLoop`'s setup, assert `outcome.ExitKind == DriverExitContextCancelled` and `outcome.Turns` reflects however many turns actually ran (not the full budget).
  - `TestAutonomousDriver_ExitKind_LLMCallError_When_CallBlockingFails`: use a `fakeHeadlessPool` configured to return an error, assert `outcome.ExitKind == DriverExitLLMCallError`.
  - `TestAutonomousDriver_ExitKind_Done_When_DoneSignalReceived`: existing DONE-signal test, assert `outcome.ExitKind == DriverExitDone`.
- Files: `session/autonomous_driver_test.go`

### Epic 2.2: Malformed-response sub-cap
**Goal**: A chatty/confused orchestrator LLM can no longer silently burn the entire turn budget on malformed replies with zero real progress (architecture.md finding #4) — abort early with a distinguishable reason instead.

#### Story 2.2.1: Abort after N consecutive malformed orchestrator responses
**As an** operator, **I want** a run dominated by malformed orchestrator replies to stop and surface quickly, **so that** it doesn't consume the full 20-turn (or extended) budget with zero real injected turns.
**Acceptance Criteria**:
- After `maxConsecutiveMalformedResponses` (3) consecutive malformed responses, the loop breaks early with `Reason` distinguishing this from ordinary turn-cap exhaustion (e.g. `"aborted after 3 consecutive malformed orchestrator responses"`) and `ExitKind: DriverExitMaxTurns` (still counts as a turn-cap-family exit for downstream handling — see Epic 3.2's kind-specific text, which reads the malformed count already tracked separately).
- A single malformed response followed by a valid `NEXT_MESSAGE`/`DONE` reply resets the consecutive counter to 0 — an occasional parse hiccup does not trip the sub-cap.
**Files**: `session/autonomous_driver.go`

##### Task 2.2.1a: Add consecutive-malformed tracking and early-break (~3 min)
- In `session/autonomous_driver.go`'s `run()` loop, add `const maxConsecutiveMalformedResponses = 3` and a `consecutiveMalformed := 0` local. On `parseErr != nil` (line 217-221), increment both `malformedResponseCount` (existing) and `consecutiveMalformed`; if `consecutiveMalformed >= maxConsecutiveMalformedResponses`, break out of the loop instead of `continue`, with `reason` set to reference the consecutive-malformed count. On a successful parse (either `NEXT_MESSAGE` or `DONE` branch), reset `consecutiveMalformed = 0`.
- Files: `session/autonomous_driver.go`

##### Task 2.2.1b: Unit tests for the malformed-response sub-cap (~4 min)
- In `session/autonomous_driver_test.go`: `TestAutonomousDriver_AbortsEarly_When_ThreeConsecutiveMalformedResponses` (fake pool returns garbage 3 times, assert the loop exits well before `maxTurns` with the distinguishing reason text) and `TestAutonomousDriver_MalformedResponse_ResetsConsecutiveCounter_When_FollowedByValidReply` (garbage, then valid, then garbage, then valid... — assert the driver does NOT abort early since no 3-in-a-row streak occurs).
- Files: `session/autonomous_driver_test.go`

### Epic 2.3: Progress-adaptive soft/hard turn cap (ADR-001)
**Goal**: Implement the design from `ADR-001-progress-adaptive-turn-budget.md` — extend the effective turn budget in place when the target session shows recent genuine output, up to a hard ceiling.

#### Story 2.3.1: Soft-cap extension using `Instance.GetTimeSinceLastMeaningfulOutput`
**As an** operator, **I want** a session that's still actively working when it hits 20 turns to keep going instead of being cut off, **so that** realistic multi-step tasks aren't punished purely for taking more than 20 orchestrator round-trips.
**Acceptance Criteria**:
- `run()` tracks `effectiveMaxTurns` (init `d.maxTurns`) separately from `hardMaxTurns` (`d.maxTurns * turnBudgetHardCapMultiplier`).
- When `turnCount` reaches `effectiveMaxTurns` and `d.inst.GetTimeSinceLastMeaningfulOutput() < turnCapProgressWindow`, `effectiveMaxTurns` is raised by `turnBudgetExtensionIncrement`, capped at `hardMaxTurns`, and the loop continues instead of stopping.
- When the above condition is false (no recent output, or already at `hardMaxTurns`), the loop stops with `ExitKind: DriverExitMaxTurns` exactly as before this epic.
- `outcome.Turns` reflects the true `turnCount` reached, including any extensions (already guaranteed by Task 2.1.1c).
**Files**: `session/autonomous_driver.go`

##### Task 2.3.1a: Add the named constants (~2 min)
- In `session/autonomous_driver.go`, add:
  ```go
  // turnCapProgressWindow mirrors maxReworkBlockStaleness
  // (server/services/backlog_service_triage.go) — 15 minutes, already validated
  // for "is this live session genuinely still working" per
  // project_plans/review-gate-stale-session-rework/decisions/ADR-001-staleness-
  // threshold-recalibration.md. Reused rather than re-derived — see this
  // project's own ADR-001-progress-adaptive-turn-budget.md for why.
  const turnCapProgressWindow = 15 * time.Minute
  // turnBudgetExtensionIncrement is how many additional turns are granted each
  // time the soft cap is reached with recent genuine output still present.
  const turnBudgetExtensionIncrement = 10
  // turnBudgetHardCapMultiplier bounds total extension: the effective budget
  // can never exceed maxTurns * this value, regardless of how much progress is
  // observed — an absolute ceiling so a session that keeps producing SOME
  // output forever cannot run unbounded.
  const turnBudgetHardCapMultiplier = 3
  ```
- Files: `session/autonomous_driver.go`

##### Task 2.3.1b: Implement the soft/hard cap check in the loop (~5 min)
- In `session/autonomous_driver.go`'s `run()`, restructure the `for turnCount := 0; turnCount < d.maxTurns; turnCount++` loop to a `for` with an explicit condition check at the top of each iteration:
  - Before the existing loop, initialize `effectiveMaxTurns := d.maxTurns` and `hardMaxTurns := d.maxTurns * turnBudgetHardCapMultiplier`.
  - At the top of each iteration (replacing the `for` clause's implicit bound check), if `turnCount >= effectiveMaxTurns`: check `d.inst.GetTimeSinceLastMeaningfulOutput() < turnCapProgressWindow`; if true AND `effectiveMaxTurns < hardMaxTurns`, set `effectiveMaxTurns = min(effectiveMaxTurns+turnBudgetExtensionIncrement, hardMaxTurns)`, log at Info (`"AutonomousDriver: extending turn budget, recent progress detected"`, including old/new `effectiveMaxTurns`), and continue the loop body as normal (do NOT break); otherwise set `exitKind = DriverExitMaxTurns` and `break`.
  - Keep the loop's other break conditions (`ctx.Err()`, rate-limit, LLM error, SendKeys failure) exactly as updated in Task 2.1.1c — they take priority and fire regardless of `effectiveMaxTurns`.
  - Use Go's builtin `min` (Go 1.21+; this repo is on Go 1.26.3 per stack.md §6) rather than a hand-rolled min helper.
- Files: `session/autonomous_driver.go`

##### Task 2.3.1c: Unit tests for the soft/hard cap (~5 min)
- In `session/autonomous_driver_test.go`:
  - `TestAutonomousDriver_ExtendsTurnBudget_When_RecentProgressAtSoftCap`: construct an `Instance` whose `GetTimeSinceLastMeaningfulOutput()` reports a small duration (directly set the field(s) `GetTimeSinceLastMeaningfulOutput` reads, matching how existing tests in this file construct `&Instance{}` — check `TestAutonomousDriver_MaxTurnsLimit`'s existing instance setup for the pattern), configure `fakeHeadlessPool` to keep returning `NEXT_MESSAGE:` replies well past `maxTurns`, and assert the driver runs past `maxTurns` up to `hardMaxTurns` before finally stopping (or reaches DONE later, whichever the fake is configured for) — i.e. it does NOT stop exactly at the base `maxTurns`.
  - `TestAutonomousDriver_StopsAtBaseBudget_When_NoRecentProgress`: same setup but `GetTimeSinceLastMeaningfulOutput()` reports a large duration (>= `turnCapProgressWindow`) — assert the driver stops at exactly `maxTurns`, `ExitKind == DriverExitMaxTurns`.
  - `TestAutonomousDriver_HardCapWinsRegardlessOfProgress`: recent-progress signal held true throughout, assert the driver never exceeds `hardMaxTurns` (`maxTurns * turnBudgetHardCapMultiplier`).
- Files: `session/autonomous_driver_test.go`

### Epic 2.4: Configurable base `maxTurns`
**Goal**: The base turn budget is no longer a pure Go literal with zero external configurability (stack.md §3 finding) — an operator can raise/lower it via `config.json` without a rebuild, even though no UI surface is added in this pass (out of scope per requirements.md — UI wiring is a follow-up).

#### Story 2.4.1: Add `MaxAutonomousTurns` config field
**As an** operator, **I want** to change the base turn budget without rebuilding the binary, **so that** I can tune it based on observed thrashing/completion patterns.
**Acceptance Criteria**:
- `config.Config` gains `MaxAutonomousTurns int` and `MaxAutonomousTurnsOrDefault() int` (default 20 when unset/`<=0`), mirroring `MaxAutoReworkIterationsOrDefault`'s exact pattern (`config/config.go:568-581`).
- All three production call sites that currently pass literal `0` to `NewAutonomousDriver` (`server/services/autonomous_orchestration_service.go:189,207`, `server/services/session_service.go:1573`) instead pass `config.LoadConfig().MaxAutonomousTurnsOrDefault()`.
- No proto changes, no frontend UI changes in this pass — a config-file-only knob, matching this project's scope constraint against speculative rearchitecture.
**Files**: `config/config.go`, `server/services/autonomous_orchestration_service.go`, `server/services/session_service.go`

##### Task 2.4.1a: Add the config field and default accessor (~3 min)
- In `config/config.go`, add `MaxAutonomousTurns int \`json:"max_autonomous_turns,omitempty"\`` near `MaxAutoReworkIterations` (line 302-307), and `MaxAutonomousTurnsOrDefault()` near `MaxAutoReworkIterationsOrDefault` (line 568-581), returning 20 when `c == nil || c.MaxAutonomousTurns <= 0`.
- Files: `config/config.go`

##### Task 2.4.1b: Wire the config value into the three call sites (~4 min)
- In `server/services/autonomous_orchestration_service.go`, change `session.NewAutonomousDriver(inst, a.pool, inst.Prompt, 0)` (line 189) and `session.NewAutonomousDriver(inst, a.pool, inst.Prompt, 0, session.WithStartupTimeout(startupTimeout))` (line 207) to pass `config.LoadConfig().MaxAutonomousTurnsOrDefault()` instead of `0`. Add the `config` import if not already present.
- In `server/services/session_service.go`, change `session.NewAutonomousDriver(instance, s.headlessPool, instance.Prompt, 0)` (line 1573) the same way.
- Files: `server/services/autonomous_orchestration_service.go`, `server/services/session_service.go`

##### Task 2.4.1c: Config default/round-trip unit test (~3 min)
- In `config/config_test.go` (or the existing test file covering `MaxAutoReworkIterationsOrDefault`), add a mirroring test for `MaxAutonomousTurnsOrDefault`: nil config → 20, zero value → 20, negative → 20, explicit positive value → that value.
- Files: `config/config_test.go`

---

## Phase 3: `onAutonomousDriverComplete` Redesign + Close the Accidental Respawn-Delay Gap

### Epic 3.1: Skip stuck-marking entirely on intentional cancellation
**Goal**: A driver stopped by an explicit, intentional action (autonomous-mode toggled off, session hibernated, session deleted, or a review verdict submitted — all confirmed real call sites of `.Stop()`/`StopDriverForSession` in normal operation, not just process shutdown) no longer gets marked `autonomous_stuck` or fires an "Autonomous fix stuck" notification — that framing is misleading for an intentional stop and was previously indistinguishable from a genuine turn-cap exhaustion.

#### Story 3.1.1: `DriverExitContextCancelled` short-circuits before any stuck-marking
**As an** operator, **I want** manually disabling autonomous mode (or hibernating/deleting a session) to not spuriously mark the item `autonomous_stuck`, **so that** the Unfinished tab only shows items that are actually stuck, not ones I intentionally stopped.
**Acceptance Criteria**:
- In `onAutonomousDriverComplete`, when `outcome.ExitKind == session.DriverExitContextCancelled`, the function logs at Info and returns before reaching the `MarkStuck`/`MarkStuckNotified` block (currently `autonomous_orchestration_service.go:293-301`) and before the role-specific switch — for every role (triage, work, review), not just work.
- The final "Autonomous fix stuck" push notification (lines 518-545) is also skipped for a cancelled outcome — it does not fire at all, matching the "this was intentional" framing (contrast with a genuine stuck outcome, which still notifies).
- The instance-level bookkeeping already at the top of the function (clearing `AutonomousMode`/`Turn`/`MaxTurns`, setting `AutonomousOutcome`, lines 253-261) still runs unconditionally — only the backlog-item stuck-marking and notification are skipped.
**Files**: `server/services/autonomous_orchestration_service.go`

##### Task 3.1.1a: Add the early-return check (~4 min)
- In `server/services/autonomous_orchestration_service.go`'s `onAutonomousDriverComplete`, immediately after the instance bookkeeping block (after line 261's `a.bus.Publish(events.NewSessionUpdatedEvent(...))`) and before the backlog-item lookup block (`if a.storageGetter != nil { ... }`, line 268), add:
  ```go
  if outcome.ExitKind == session.DriverExitContextCancelled {
      log.Info("[AutonomousDriver] driver stopped via intentional cancellation, skipping stuck-marking and notification", "session", instanceName)
      return
  }
  ```
- Files: `server/services/autonomous_orchestration_service.go`

##### Task 3.1.1b: Unit test — cancelled outcome produces no `MarkStuck` call and no notification (~4 min)
- In `server/services/autonomous_orchestration_service_test.go`, following the pattern of `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_MarksAutonomousStuck_When_NotDone` (line 235): `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_SkipsStuckMarking_When_ContextCancelled` — construct an `outcome` with `Done: false, ExitKind: session.DriverExitContextCancelled`, assert the fake storage's `MarkStuck` was never called and no notification event was published.
- Files: `server/services/autonomous_orchestration_service_test.go`

### Epic 3.2: Kind-specific reason text using the corrected `Turns`
**Goal**: The stuck-reason message and operator-facing notification distinguish "ran out of turns" from "the orchestrator's own LLM call failed" from "SendKeys failed" from "hit the rate-limit-wait ceiling" — the requirements doc's stated gap ("the system's response to hitting that cap... is not well understood or well designed").

#### Story 3.2.1: Branch the `MarkStuck` message and notification body on `ExitKind`
**As an** operator reading the Unfinished tab, **I want** the stuck reason to tell me *what kind* of exit happened, **so that** I don't have to open the session transcript to guess whether it's a genuine turn-cap or an infra hiccup.
**Acceptance Criteria**:
- The `MarkStuck` call in `onAutonomousDriverComplete` (currently line 294-297, format string `"autonomous driver stopped after %d turns without a DONE signal (%s)"`) branches on `outcome.ExitKind` to produce a distinct, human-readable reason per kind (e.g. `"hit its turn cap after %d turns"` for `DriverExitMaxTurns`, `"the orchestrator's LLM call failed after %d turns (%s)"` for `DriverExitLLMCallError`, `"failed to inject a prompt (SendKeys) after %d turns (%s)"` for `DriverExitSendKeysFailed`, `"hit the rate-limit wait ceiling after %d turns"` for `DriverExitRateLimitTimeout`, `"never became idle at startup"` for `DriverExitStartupTimeout`), using the now-accurate `outcome.Turns`.
- The final "Autonomous fix stuck" notification body (line 528-530) similarly reflects the kind, not a single generic "stopped after N turns without completing" for every case.
- All existing tests asserting the old generic message text are updated to the new kind-specific text (they construct outcomes with `ExitKind` unset/zero-value today — update those literals to set `ExitKind: session.DriverExitMaxTurns` explicitly so they continue exercising the "genuine turn cap" branch, matching their original intent).
**Files**: `server/services/autonomous_orchestration_service.go`, `server/services/autonomous_orchestration_service_test.go`

##### Task 3.2.1a: Branch the reason strings on `ExitKind` (~5 min)
- In `server/services/autonomous_orchestration_service.go`, replace the single `fmt.Sprintf("autonomous driver stopped after %d turns without a DONE signal (%s)", outcome.Turns, outcome.Reason)` (line 296) with a small helper (e.g. `stuckReasonForExitKind(outcome session.AutonomousDriverOutcome) string`) implementing the per-kind text from the story's acceptance criteria, called both at the `MarkStuck` site and the final notification body site (line 529).
- Files: `server/services/autonomous_orchestration_service.go`

##### Task 3.2.1b: Update existing tests + add per-kind reason-text tests (~5 min)
- In `server/services/autonomous_orchestration_service_test.go`, update `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_MarksAutonomousStuck_When_NotDone` and `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_KeepsAutonomousStuck_When_WorkStillStuck` to set `ExitKind: session.DriverExitMaxTurns` on their constructed outcomes (so they keep exercising the turn-cap branch, not an unset-zero-value branch) and assert the new kind-specific text where the old test asserted the old generic text.
- Add `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_ReasonText_When_LLMCallError` and `..._When_SendKeysFailed` asserting the distinct reason strings for those kinds.
- Files: `server/services/autonomous_orchestration_service_test.go`

### Epic 3.3: Close the accidental ~20-25 minute respawn-delay gap
**Goal**: `AutoRespawnAutonomousWork` currently only proceeds once the item's stale `ItemSession` row has been ended — but nothing in the turn-cap-without-DONE completion path ends that row, so the *first* remediation attempt (fired immediately per `RemediationDue`'s schedule) almost always no-ops on `hasActiveWorkSession`, and the respawn only actually happens once the unrelated `HibernationSweeper` (5-minute tick, 20-minute idle threshold by default) eventually kills the idle tmux pane and `onSessionExited` ends the row — a confirmed, previously-undocumented ~20-25 minute accidental delay discovered during this project's own verification pass (see Note below). This directly serves the requirements' turn-cap-redesign scope, not just the dedup scope.

> **Note for the record**: this gap was not identified in the Phase 2 research docs (stack.md/architecture.md/features.md/pitfalls.md) — it was found by tracing `AutoRespawnAutonomousWork`'s `hasActiveWorkSession` guard against `onAutonomousDriverComplete`'s turn-cap branch during Phase 3 planning verification, and independently confirmed against `session/hibernation_sweeper.go` and `session/instance_hibernate.go`'s kill→EOF→`onSessionExited` chain. It is in-scope per requirements.md's explicit instruction to redesign "what happens on cap" — it is not a rearchitecture of `AutonomousDriver`/`autonomous_orchestration_service.go` beyond what's needed for this fix (out-of-scope guard), since the fix is confined to `AutoRespawnAutonomousWork`'s own existing kill+end pattern, already used identically by its sibling `RemediateStaleWorkSession`.

#### Story 3.3.1: `AutoRespawnAutonomousWork` ends the driver-abandoned session instead of skipping
**As an** operator, **I want** a turn-capped work session's respawn to happen promptly (bounded by the remediation backoff schedule, not by an unrelated 20-minute hibernation timer), **so that** a stuck item doesn't sit fully idle for up to 25 minutes before getting a fresh attempt.
**Acceptance Criteria**:
- `AutoRespawnAutonomousWork` (`server/services/backlog_service_triage.go:1267-1318`), on finding an active work session via `hasActiveWorkSession(sessions)`, no longer returns early with "skipping respawn" — instead it ends that session (`s.storage.UpdateItemSessionEnded`) and best-effort kills its tmux pane (`s.sessionStopper.KillTmuxPaneOnly`), mirroring `RemediateStaleWorkSession`'s existing pattern (`backlog_service_triage.go:1378-1394`), then proceeds to the rework-cap check and respawn exactly as it does today for the "no active session" case.
- This is safe specifically because `AutoRespawnAutonomousWork` is only ever invoked (a) from `onAutonomousDriverComplete`'s turn-cap-without-DONE branch, after that same function has already unconditionally deregistered/stopped the one driver that was watching this session (`stopAndDeregisterDriver`, called at function entry, `autonomous_orchestration_service.go:231`), or (b) from `RemediateStaleWorkSession`, which has already ended the session itself before calling this function — in both cases the driving mechanism is guaranteed to have already stopped by the time this code runs, so ending the session here does not violate the "never force-kill a still-driving live session" policy (project_plans/review-gate-stale-session-rework's ADR, pitfalls.md §5) — it only ever acts on a session the orchestrator has already abandoned.
- The stale doc comment at `backlog_service_triage.go:1288-1289` ("the driver-complete callback that triggered this call already ended the session record") — confirmed incorrect for the work-role path during this project's verification — is corrected to describe the actual (now-fixed) behavior.
**Files**: `server/services/backlog_service_triage.go`

##### Task 3.3.1a: Replace the skip with end+kill (~5 min)
- In `server/services/backlog_service_triage.go`'s `AutoRespawnAutonomousWork`, replace:
  ```go
  s.tombstoneOrphanWorkSessions(ctx, itemID, sessions)
  if hasActiveWorkSession(sessions) {
      log.InfoLog.Printf("[AutoRespawnAutonomousWork] item %s already has an active work session; skipping respawn", itemID)
      return nil
  }
  ```
  (lines 1291-1295) with logic that, after `tombstoneOrphanWorkSessions`, finds the active work `ItemSessionSummary` (if any) and — instead of returning — best-effort kills its tmux pane via `s.sessionStopper.KillTmuxPaneOnly(ctx, active.SessionUUID)` (nil-checked, log-and-continue on error, mirroring `RemediateStaleWorkSession` lines 1384-1388) and ends the row via `s.storage.UpdateItemSessionEnded(ctx, active.ID, time.Now())` (returning the error if this fails, mirroring `RemediateStaleWorkSession` line 1391-1393), then falls through to the existing rework-cap check and `SpawnSessionFromItem` call unchanged.
- Files: `server/services/backlog_service_triage.go`

##### Task 3.3.1b: Correct the stale doc comment (~2 min)
- In `server/services/backlog_service_triage.go`, update the comment at lines 1287-1290 ("Tombstone any work session confirmed dead before checking liveness, mirroring AutoReopenForPRFix's identical guard — the driver-complete callback that triggered this call already ended the session record...") to accurately describe the corrected behavior: this function itself now ends any still-open work session it finds, because by the time it runs the driving orchestrator has already stopped (see the function's updated top-level doc comment for the full safety argument).
- Files: `server/services/backlog_service_triage.go`

##### Task 3.3.1c: Regression test — respawn no longer blocked by an idle-but-tracked-live session (~5 min)
- In `server/services/backlog_service_test.go` or `backlog_service_triage_test.go` (match whichever file houses `AutoRespawnAutonomousWork`'s existing tests, if any — otherwise colocate with `TestSpawnSessionFromItem_LiveWorkSession_StillBlocksSpawn`-style tests in `backlog_service_test.go`): `TestAutoRespawnAutonomousWork_EndsAbandonedSession_When_StillMarkedActive` — set up an `in_progress` item with one open work-role `ItemSession` whose `SessionUUID` the `mockSessionStopper` reports `IsSessionLive == true` for (simulating the idle-but-alive post-turn-cap state), call `svc.AutoRespawnAutonomousWork(ctx, itemID)`, and assert: (a) the original `ItemSession` row now has `EndedAt != nil`, (b) `mockSessionStopper`'s kill-pane call was recorded for that session UUID, (c) exactly one new work-role `ItemSession` was created (via `creator.calls`), (d) this is the regression test for the ~20-25 minute accidental respawn-delay gap found during this project's Phase 3 verification — document that in the test's doc comment.
- Files: `server/services/backlog_service_test.go`

---

## Phase 4: Full Regression Pass

### Epic 4.1: Verify the whole change set together, confirm no regression of PR #222's invariant
**Goal**: All new/changed tests pass, `make lint`/`make build` are green, and the PR #222 guarantee ("never transition a work item out of `in_progress` via `Done=true` or turn-cap-without-`Done` — only `request_review` does that") is still enforced by its existing regression test.

#### Story 4.1.1: Run the full targeted test suite and confirm the PR #222 guard test still passes unmodified
**As a** reviewer, **I want** confirmation that this project's changes don't regress the specific bug PR #222 fixed, **so that** the "orchestrator hallucinates DONE" premature-review-transition bug cannot silently return.
**Acceptance Criteria**:
- `go test ./session/... ./server/services/... -race` passes.
- `make lint` and `make build` pass.
- `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_DoesNotForceReview_When_OrchestratorClaimsDoneWithoutRequestReview` (`autonomous_orchestration_service_test.go:348`) passes with NO modification to its assertions — only touch this test if a compile error forces adding `ExitKind: session.DriverExitDone` to its outcome literal (the `Done: true` semantics it tests are otherwise untouched by this project).
**Files**: none (verification-only story)

##### Task 4.1.1a: Run the targeted test + lint + build pass (~3 min)
- Run `go test ./session/... ./server/services/... -race` and `make lint` and `make build` from the repo root; fix any compile errors surfaced by the new `ExitKind` field on `AutonomousDriverOutcome` literals elsewhere in the codebase (e.g. any other test file constructing this struct) by adding an explicit `ExitKind` value matching that test's intent.
- Files: none (may touch any file the compiler flags — expected to be limited to test files constructing `AutonomousDriverOutcome` literals)

##### Task 4.1.1b: Confirm the PR #222 guard test is untouched in substance (~2 min)
- Read `TestAutonomousOrchestrationService_OnAutonomousDriverComplete_DoesNotForceReview_When_OrchestratorClaimsDoneWithoutRequestReview` (`autonomous_orchestration_service_test.go:348`) and confirm its assertions (item stays `in_progress`, no `toStatus` transition fires) are unchanged from before this project's edits — if the struct literal needed an `ExitKind: session.DriverExitDone` addition per Task 4.1.1a, confirm that's the only change and it doesn't alter the test's assertions.
- Files: `server/services/autonomous_orchestration_service_test.go` (read-only verification; edit only if Task 4.1.1a already required it)
