# Architecture Review: review-session-notification-cleanup
**Date**: 2026-07-25
**Verdict**: BLOCKED

## Constitution Violations
- None. `docs/adr/ADR-000-architecture-constitution.md` does not exist in this repository
  (confirmed via filesystem search); no constitution constraints apply.

## Blockers

- [ ] **Task 4.3.1a / Story 4.3 (`server/server.go`)** — the plan's code snippet closes over
  `startTime` as "already bound at server/server.go:111," but `startTime` is a local variable
  scoped to `NewServer` (lines 107-121). The insertion point the plan names —
  `wireDepsIntoServer` (line 138+), where `notifStore` is actually constructed — is a **different
  function** with no `startTime` in scope; as written this is a compile error (undefined
  identifier). It's worse than a typo: `NewServerWithDeps` (the second, Warren-lifecycle
  construction entry point, called directly by external callers with pre-built deps) invokes
  `wireDepsIntoServer` without ever computing a `startTime` at all, so there is no single
  "process start" instant available at the point the closure needs one. This directly
  undermines the plan's own named Risk Control mitigation for AC3's most dangerous failure mode
  (mass-pruning live notifications for sessions that exist but haven't finished reconciling after
  a restart).
  **Remediation**: add a `startedAt time.Time` field to the `Server` struct, set once in
  `newServerBase` (shared by both `NewServer` and `NewServerWithDeps`), and have the
  `SetSessionExistenceChecker` closure in `wireDepsIntoServer` reference `srv.startedAt` instead
  of a function-local `startTime`.

- [ ] **Story 4.2 / Task 4.2.1b (`server/notifications/store.go`, `enforceRetention`)** — wiring
  `s.pruneOrphanedRecords(s.existenceChecker)` into `enforceRetention()`, which runs on **every**
  `Append()` call, means the injected existence check
  (`storage.FindInstanceDataByID` → `Storage.ListInstanceData()` →
  `EntRepository.List(ctx)` — confirmed at `session/ent_repository.go:737`, a
  `Session.Query().WithWorktree().WithTags().WithProject().WithClaudeSession(...).All(ctx)`, a
  real multi-edge-eager-loaded SQL query, not a cheap map lookup) executes **once per eligible
  record** (`SessionScoped && item_id == ""`) in the up-to-500-record store, **on every single
  append**, while holding the store's global `s.mu` lock (blocking all concurrent notification
  reads/writes for the duration). `Append()` fires roughly every 500ms in a busy system (the
  subscriber's coalesce-flush interval, `server/notifications/subscriber.go`'s
  `DefaultCoalesceInterval`). This is an O(eligible-records) DB-query storm on a hot path, not
  analyzed anywhere in the plan's Risk Control table (which only reasons about I/O added to
  `OnItemAdded`, never this compounding cost inside the notification store's own retention pass).
  It will look fine in development (0-1 orphan candidates) and silently degrade under the exact
  production scenario this feature exists to fix — a growing backlog of stale session-scoped
  notifications.
  **Remediation**: (a) call `storage.ListInstanceData()` **once** per prune pass, build an
  in-memory `map[string]struct{}` of existing stable IDs, and check membership instead of calling
  `FindInstanceDataByID` (which re-queries) per record; and (b) decouple the orphan sweep from the
  `Append()` hot path — run it on its own periodic ticker (matching the existing age-based
  retention's already-coarse cadence) rather than synchronously on every single append.

## Concerns

- [ ] **ADR-001 / Tasks 2.2.1a, 3.2.1b, 4.1.1b — magic-string metadata convention duplicated with
  no shared constant.** ADR-001 rejects ID-prefix sniffing as "an inferred, unaudited signal" a
  future producer could silently violate, choosing an explicit typed `SessionScoped` field
  instead — but the mechanism that *populates* that field is itself an inferred, unaudited
  signal one level down: `metadata["session_scoped"] = "true"` is written as an independent
  string literal in `server/review_queue_manager.go` (Task 2.2.1a) and
  `server/services/autonomous_orchestration_service.go` (Task 3.2.1b), and read back via a
  separate literal comparison in `server/notifications/subscriber.go` (Task 4.1.1b), with no
  shared constant anywhere. A typo divergence (`"sessionScoped"`, a stray space, `"True"`) between
  producer and consumer silently defaults `SessionScoped` to `false` — not data-loss, but a
  silent per-producer regression of AC3 with zero compiler or lint signal.
  **Remediation**: define one exported constant pair near `pkg/events.NewNotificationEvent`
  (`MetadataKeySessionScoped`, `MetadataValueTrue`) used at all three sites. Better: since
  `OnItemAdded` and the generic autonomous notifier already build near-identical metadata maps
  (`{"item_id": ..., "session_scoped": "true"}`), extract one shared helper (e.g.
  `events.SessionScopedMetadata(itemID string) map[string]string`) — the "generalize once 2+ real
  call sites need identical logic" case the design-patterns skill endorses, and materially cheaper
  than the already-correctly-rejected `NotificationPolicy` interface (Alternative B).

- [ ] **No enforcement ladder for a third, future producer forgetting to opt in.** The plan/ADR-001
  knowingly accepts that any future notification producer must remember to set
  `session_scoped` — there is no compile-time, lint-time, or test-time check that every
  session-identified `events.NewNotificationEvent(...)` call site sets the key. This is a
  documented trade-off (ADR-001 Consequences), not an oversight, so it does not block this plan,
  but it is the exact "eliminate the class, not the instance" gap worth naming.
  **Recommendation**: add one test in the `notifications`/producer packages asserting the two
  known-current producer call sites include the metadata key, so a future new producer at least
  has a place a reviewer would think to update, even though nothing forces it before merge.

- [ ] **Design Decision 6 applies an inconsistent reachability standard.** The plan declines to add
  a `Hidden` gate to the "Triage stuck" call site (Story 3.1) because it's "currently dead code in
  practice... unverifiable and untestable," yet adds exactly that category of gate to the generic
  done/stuck notifier (Story 3.2), whose own acceptance-criteria example is explicitly labeled "a
  hypothetical future Hidden autonomous-driver-run instance" — also unreachable today, since
  `SessionRoleReview`/`SessionRoleTriage` both `return` before reaching this notifier and
  `SessionRoleWork`-linked instances are never `Hidden`. The two decisions apply opposite
  reachability standards to structurally the same "defensive guard against an unreachable state"
  situation without acknowledging it.
  **Recommendation**: no code change needed (the 3.2 gate is cheap, correct, harmless insurance
  worth keeping) — just have the plan's write-up either apply the same reasoning to both sites or
  explain why 3.2 gets the benefit of the doubt that 3.1 didn't.

- [ ] **Story 2.1 — burst-transition latency not analyzed.** `OnItemAdded`'s new
  `GetItemSessionBySessionUUID` lookup (Task 2.1.1b) runs synchronously in the calling goroutine
  (unlike `maybeAutoCreatePR`'s async-goroutine pattern it otherwise mirrors), bounded to 2s.
  `ReviewQueue.Add()` (confirmed at `session/queue/queue.go:258-264`) releases its own lock before
  notifying observers, so there's no deadlock risk, but observer notification is sequential — N
  simultaneous new queue entries in one tick or one `StartupScanner.Scan` pass (e.g., a fleet
  reconciling after a service restart) would serially block the calling goroutine for up to
  N×2s. The Risk Control table only justifies "once per transition, not per tick," not the
  cumulative burst case.
  **Recommendation**: document why the realistic worst-case N is acceptable (likely small in
  steady state), or move the lookup to a bounded async dispatch if startup-fleet-size N could
  ever be large enough to matter.

## Nitpicks

- `NotificationRecord.SessionID` overloading two domain concepts (real session ID vs. backlog item
  ID) would be more cleanly modeled as two distinct optional fields or a small discriminated
  `NotificationSubject` type rather than a sibling `SessionScoped bool`. The ADR's minimal patch is
  a reasonable, proportionate trade-off given `SessionID` is already read elsewhere (`ListOptions`
  filtering, subscriber coalescing) — a full remodel would be disproportionate to this plan's
  scope. Flagging for awareness, not action.
- Parse-at-boundary is only half-applied: the new `SessionScoped bool` is a good typed field at
  the persisted-record boundary, but the upstream signal (`event.NotificationMetadata`) remains a
  raw `map[string]string` end-to-end. Correctly out of scope here (fixing it broadly would touch
  ~9 unrelated `NewNotificationEvent` call sites, per Alternative B's rejection) — noted for a
  future, larger notification-metadata cleanup.
- Task 2.1.1b: when `rqm.poller.FindInstance(item.SessionID)` returns `nil`, `resolvedID` stays
  the raw title string, and `GetItemSessionBySessionUUID(lookupCtx, resolvedID)` is queried with a
  title rather than a UUID — it will simply miss (`ErrNotFound`), silently skipping `item_id`
  enrichment even for a genuinely backlog-linked session in that edge case. Low severity (largely
  foreclosed by Epic 1's fix), but worth a one-line comment at the call site.
