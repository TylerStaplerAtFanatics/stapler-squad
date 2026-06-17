# Implementation Plan: Fast Startup with Background Session Restore

**v2 - patched 2026-06-17** — Verdict changed from BLOCKED to PATCHED after adversarial review. See patch notes inline at each modified story.

## Overview

This plan reduces HTTP server bind time from ~23s to ~6s by making two synchronous startup blockers async (analytics DB open ~11s, external discovery scan ~2s), removing a 200ms per-session stagger (~5s for 25+ sessions), and adding a transient `Restoring` status so the UI can show users why sessions are not yet interactive during the startup window.

All changes are in-memory or in the startup goroutine; no ent ORM schema migration is required.

---

## Epics

### Epic 1: Go Backend — StatusRestoring constant and proto wire value

**Goal**: Add `Restoring Status = 5` to Go and `SESSION_STATUS_RESTORING = 9` to proto, wire through the adapter, and regenerate bindings — no DB persistence changes.

#### Story 1.1: Add Go constant

**Files**: `session/instance.go`

**Tasks**:
- [ ] After line 43 (the `Loading = Creating` deprecated alias), add a new block comment and constant:
  ```go
  // Restoring is a transient in-memory-only status set during server startup
  // while inst.Start(false) is executing for a previously-persisted session.
  // This value is NEVER written to the database; SaveInstances skips Restoring instances.
  Restoring Status = 5
  ```
- [ ] In the `String()` switch (line 46), add a case before `default`:
  ```go
  case Restoring:
      return "Restoring"
  ```

#### Story 1.2: Add proto enum value

**Files**: `proto/session/v1/types.proto`

**Tasks**:
- [ ] In the `SessionStatus` enum (line 289), after `SESSION_STATUS_HIBERNATED = 8` (line 311), add:
  ```protobuf
  // Session is being restored from a previous run (transient startup state).
  // Never persisted to the database. Transitions to ACTIVE or CREATING on completion.
  SESSION_STATUS_RESTORING = 9;
  ```
- [ ] Run `make generate-proto` to regenerate `session/gen/session/v1/*.go` and `web-app/src/gen/session/v1/*_pb.ts`.
- [ ] Verify the generated Go file `session/gen/session/v1/types_pb.go` contains `SESSION_STATUS_RESTORING`.
- [ ] Verify the generated TypeScript file `web-app/src/gen/session/v1/types_pb.ts` contains `SessionStatus.RESTORING`.

#### Story 1.3: Update StatusToProto adapter

**Files**: `server/adapters/instance_adapter.go`

**Tasks**:
- [ ] In `StatusToProto()` (line 258), add a case before the `default` branch:
  ```go
  case session.Restoring:
      return sessionv1.SessionStatus_SESSION_STATUS_RESTORING
  ```
- [ ] In `StatusStringToProto()` (line 281), add a case before `default`:
  ```go
  case "Restoring":
      return sessionv1.SessionStatus_SESSION_STATUS_RESTORING
  ```
- [ ] Run `make build` and confirm no compile errors.

---

### Epic 2: Go Backend — Analytics DB async open

**Goal**: Move `analytics.OpenAnalyticsDB` into a dedicated background goroutine launched from `BuildRuntimeDeps`, completely independent of the session restore goroutine, and use an `atomic.Pointer[ent.Client]` (propagated through both `RuntimeDeps` and `ServerDependencies` via `ToServerDeps()`) so `wireDepsIntoServer` can late-bind the analytics provider per-request once the DB opens. This matches ADR-019.

> **Patch note (CRITICAL-1 + CRITICAL-2 fix)**: The original plan used a `chan *ent.Client` field on `RuntimeDeps` only, and placed the analytics open at "Step 5.5" inside the existing restore goroutine. Both were wrong: (a) `wireDepsIntoServer` receives `*ServerDependencies` not `*RuntimeDeps` — the channel would be nil after `ToServerDeps()` copies fields; (b) placing analytics open before Step 6 in the same goroutine keeps analytics and session restores serial in the background, defeating the concurrency goal. Fix: use `atomic.Pointer[ent.Client]` added to BOTH structs and copied in `ToServerDeps()`, and launch analytics in its OWN independent goroutine.

#### Story 2.1: Extract analytics wiring into a late-bind atomic pointer

**Files**: `server/dependencies.go`, `server/server.go`

**Tasks**:

**dependencies.go changes:**
- [ ] Add the following import if not already present: `"sync/atomic"`.
- [ ] At line 716 (the `// Open the dedicated analytics database` block, lines 716–727), delete the synchronous open entirely. Replace with a comment:
  ```go
  // analyticsEntClient is populated asynchronously by a dedicated goroutine below.
  // All handlers nil-guard on the pointer load; HTTP server starts with log-only fallback
  // and upgrades silently when the DB opens.
  ```
- [ ] Add a new field to the `RuntimeDeps` struct (search for `type RuntimeDeps struct`):
  ```go
  // AnalyticsClientPtr is an atomic pointer populated asynchronously once
  // analytics.OpenAnalyticsDB completes. wireDepsIntoServer reads this per-request.
  AnalyticsClientPtr atomic.Pointer[ent.Client]
  ```
- [ ] In `ToServerDeps()` (lines 83–113), after copying all existing fields, add:
  ```go
  // Propagate the atomic analytics client pointer so wireDepsIntoServer can read it.
  sd.AnalyticsClientPtr = &deps.AnalyticsClientPtr
  ```
  Also add `AnalyticsClientPtr *atomic.Pointer[ent.Client]` to the `ServerDependencies` struct so the field exists on the receiving side. Both structs must carry the field for the pointer to survive `ToServerDeps()`.
- [ ] **In `BuildRuntimeDeps`, launch a SEPARATE dedicated goroutine** for analytics — completely independent of (and concurrent with) the existing restore goroutine that handles Steps 6–6.6. Add it immediately before or after the restore goroutine start, not inside it:
  ```go
  // Dedicated analytics open goroutine — runs concurrently with session restore.
  // This goroutine is independent: analytics open (~11s) and session restores run in parallel.
  go func() {
      configDir, configErr := config.GetConfigDir()
      if configErr != nil {
          log.Warn("could not determine config dir for analytics DB", "err", configErr)
          return
      }
      ctx := context.Background()
      ac, acErr := analytics.OpenAnalyticsDB(ctx, configDir)
      if acErr != nil {
          log.Warn("could not open analytics DB (will use log-only fallback)", "err", acErr)
          return
      }
      log.Info("analytics DB opened (async)", "path", configDir+"/analytics.db")
      deps.AnalyticsClientPtr.Store(ac)
  }()
  ```

**server.go changes:**
- [ ] Locate `wireDepsIntoServer` (line 131). Find the analytics wiring block (lines 473–514). Keep the existing nil-guard wiring unchanged — it will run with a nil client and wire the `LogAnalyticsProvider` fallback. This is the correct initial state.
- [ ] After the existing analytics wiring block, add a late-bind goroutine that polls/waits on the atomic pointer. The goroutine needs `serverCtx`, `cfg`, and local handler references — all already in scope in `wireDepsIntoServer`:
  ```go
  // Late-bind analytics once the dedicated open goroutine populates the atomic pointer.
  if deps.AnalyticsClientPtr != nil {
      go func() {
          // Poll until the analytics client is stored or the server shuts down.
          // The DB open takes ~11s; 500ms poll interval adds negligible overhead.
          for {
              if ac := deps.AnalyticsClientPtr.Load(); ac != nil {
                  log.Info("analytics DB ready (async): upgrading to SQLite provider")
                  analyticsProvider := analytics.NewSQLiteAnalyticsProvider(ac)
                  analytics.StartRetentionEnforcer(serverCtx, ac)
                  escapeWriter := analytics.NewEscapeEventBatchWriter(ac, cfg.EscapeAnalyticsMaxRowsPerSession)
                  go escapeWriter.Start(serverCtx)
                  pkganalytics.SetGlobalEscapeWriter(escapeWriter)
                  deps.SessionService.SetAnalyticsClient(ac)
                  deps.SessionService.SetAnalyticsProvider(analyticsProvider)
                  analyticsHandler.SetClient(ac)
                  log.Info("analytics SQLite provider active")
                  return
              }
              select {
              case <-serverCtx.Done():
                  return
              case <-time.After(500 * time.Millisecond):
              }
          }
      }()
  }
  ```
  Note: `analyticsHandler` must be declared as a local variable earlier in `wireDepsIntoServer` (before the existing anonymous construction) so the goroutine closure can capture it. Refactor the inline `handlers.NewAnalyticsHandlerWithClient(...)` assignment into a named variable if it is not already one.

  > **Patch note (CONCERN-2 fix)**: The original plan called `analytics.NewEscapeEventBatchWriter(ac, deps.EventBus, serverCtx)` with 3 args and `escapeWriter.Start()` with no args. The actual constructor is `analytics.NewEscapeEventBatchWriter(client, maxRowsPerSession)` and `Start` takes `serverCtx`. The corrected call matches `server.go:492–494`. Also added the required `pkganalytics.SetGlobalEscapeWriter(escapeWriter)` call so `ResponseStream` instances pick up the writer.

#### Story 2.2: Verify AnalyticsHandler supports late-bind

**Files**: `server/handlers/analytics_handler.go`, `server/server.go`

**Tasks**:
- [ ] Open `server/handlers/analytics_handler.go`. Confirm `NewAnalyticsHandlerWithClient` stores `client` in a struct field. Confirm the handler's `HandleSummary` method (line 207) checks `h.client == nil` before use.
- [ ] If `AnalyticsHandler` has a `SetClient(*ent.Client)` or equivalent setter, call it from the late-bind goroutine (Story 2.1). If not, add:
  ```go
  func (h *AnalyticsHandler) SetClient(client *ent.Client) {
      h.client = client
  }
  ```
  and call `analyticsHandler.SetClient(ac)` from the late-bind goroutine in `wireDepsIntoServer`. Store the `analyticsHandler` reference in a local variable in `wireDepsIntoServer` for the goroutine closure to capture.
- [ ] Run `make build` to confirm no compile errors.

---

### Epic 3: Go Backend — ExternalDiscovery async scan

**Goal**: Make `ExternalSessionDiscovery.Start()` return immediately by wrapping `ScanFromUserOptions()` in a goroutine, eliminating the ~2s synchronous blocker.

#### Story 3.1: Move ScanFromUserOptions into a goroutine

**Files**: `session/external_discovery.go`

**Tasks**:
- [ ] In `Start()` (line 55), change lines 67–71 from:
  ```go
  if _, err := e.discovery.ScanFromUserOptions(); err != nil {
      log.Warn("ScanFromUserOptions failed", "err", err)
  }
  ```
  to:
  ```go
  go func() {
      if _, err := e.discovery.ScanFromUserOptions(); err != nil {
          log.Warn("ScanFromUserOptions failed", "err", err)
      }
  }()
  ```
  This is a 2-line change: wrap the call in `go func() { ... }()`.

#### Story 3.2: Fix approval monitor wiring order (Race 2b mitigation)

**Files**: `server/server.go`

**Tasks**:
- [ ] In `wireDepsIntoServer` (line 291), find the three-line block:
  ```go
  deps.ExternalDiscovery.Start(5 * time.Second)
  deps.ExternalApprovalMonitor.Start()
  deps.ExternalApprovalMonitor.IntegrateWithDiscoveryTmux(deps.ExternalDiscovery, deps.TmuxStreamerManager)
  ```
- [ ] Reorder so `IntegrateWithDiscoveryTmux` is called **before** `Start()`:
  ```go
  deps.ExternalApprovalMonitor.Start()
  deps.ExternalApprovalMonitor.IntegrateWithDiscoveryTmux(deps.ExternalDiscovery, deps.TmuxStreamerManager)
  deps.ExternalDiscovery.Start(5 * time.Second)
  ```
  This closes Race 2b from the pitfalls research: the approval monitor is wired before the scan goroutine can fire callbacks.
- [ ] Run `make build` to confirm no compile errors.

---

### Epic 4: Go Backend — Background restore with Restoring status and stagger removal

**Goal**: Set `inst.Status = Restoring` before each `inst.Start(false)` call in the restore goroutine, publish a status event so connected clients see the state immediately, remove the 200ms stagger for hot-attach restores, and guard `SaveInstances` to never persist Restoring instances.

#### Story 4.1: Set Restoring status before Start() in the restore loop

> **Patch note (CRITICAL-3 fix)**: The original plan had a two-pass approach: a pre-pass marking ALL not-yet-started instances as `Restoring`, then a restore pass checking `inst.Status == session.Restoring`. This broke the crash-recovery path in Step 6b, which checks `inst.Status == session.Stopped` to detect sessions that were `Stopped` in the DB but still have a live tmux. Pre-marking those as `Restoring` made Step 6b's check always false — those sessions would be stuck `Restoring` forever. Fix: set `inst.Status = Restoring` INSIDE the restore loop, right before calling `inst.Start(false)`, and ONLY for sessions that are NOT in `Stopped` status. `Stopped` sessions are left untouched for Step 6b to handle as before.

**Files**: `server/dependencies.go`

**Tasks**:
- [ ] Add an import for the `events` package if not already present (search for `"github.com/tstapler/stapler-squad/pkg/events"` in the import block; it is likely already present since `eventBus.Publish` is used at line 471).
- [ ] In the background goroutine, **Step 6** (line 488–499), replace the existing loop:
  ```go
  for i, inst := range instances {
      if !inst.Started() {
          if i > 0 {
              time.Sleep(200 * time.Millisecond)
          }
          if err := inst.Start(false); err != nil {
              log.Error("failed to start loaded instance", "session", inst.Title, "err", err)
          } else {
              log.Info("started loaded instance", "session", inst.Title)
          }
      }
  }
  ```
  with:
  ```go
  // Restore sessions serially. The 200ms stagger is removed: hot-attach restores
  // call RestoreWithWorkDir (tmux attach-session) which forks no new processes.
  // Cold restores (new tmux sessions) also proceed without stagger; the desktop
  // ulimit -u limit is not reached at typical session counts (<50).
  //
  // IMPORTANT: Only mark non-Stopped sessions as Restoring. Sessions with
  // inst.Status == session.Stopped are left for Step 6b (crash-recovery: Stopped
  // sessions with a live tmux). Pre-marking them as Restoring would make Step 6b's
  // check always false and leave those sessions stuck in Restoring forever.
  for _, inst := range instances {
      if !inst.Started() && inst.Status != session.Stopped {
          // Set Restoring INSIDE the loop, immediately before Start(), not in a pre-pass.
          inst.Status = session.Restoring
          eventBus.Publish(events.NewSessionUpdatedEvent(inst, []string{"status"}))
          if err := inst.Start(false); err != nil {
              log.Error("failed to start loaded instance", "session", inst.Title, "err", err)
              // On failure, revert to Creating so the session is not stuck Restoring.
              inst.Status = session.Creating
              eventBus.Publish(events.NewSessionUpdatedEvent(inst, []string{"status"}))
          } else {
              log.Info("started loaded instance", "session", inst.Title)
              // inst.Start() internally transitions status to Active (or stays Creating
              // for new sessions). An event is published by the status transition path
              // inside Start(); no second publish needed here.
          }
      }
  }
  // Step 6b (crash-recovery) follows below, unchanged. It checks inst.Status == session.Stopped
  // and handles live-tmux sessions that were Stopped at shutdown. Because we did NOT
  // pre-mark Stopped instances as Restoring above, this check remains effective.
  ```
  **Note**: `eventBus` is a local variable in `BuildRuntimeDeps` (wired to instances at line 471). It must be accessible in the goroutine closure. Confirm `eventBus` is in scope at line 479 (it should be — the goroutine is a closure over all local variables in `BuildRuntimeDeps`).

#### Story 4.2: Publish status transition event after Start() (delta stream correctness)

**Files**: `session/instance.go` (or `server/dependencies.go`)

**Tasks**:
- [ ] Confirm that `inst.Start(false)` internally calls `transitionTo(ctx, Active)` (or equivalent) and that this transition publishes an event via the `onStatusChange` callbacks wired to `eventBus`. Search `session/instance_state.go` for `onStatusChange` and confirm the event bus publish happens there.
- [ ] If `Start()` does NOT publish a status event (only updates `inst.Status`), add an explicit publish after `inst.Start(false)` returns with `nil` error in the loop above:
  ```go
  eventBus.Publish(events.NewSessionUpdatedEvent(inst, []string{"status"}))
  ```
  This ensures `WatchSessions` delta subscribers see `Restoring → Active` without waiting for the next polling cycle (up to 5s).

#### Story 4.3: Guard SaveInstances against persisting Restoring status

**Files**: `session/storage.go` (or wherever `SaveInstances` is defined)

**Tasks**:
- [ ] Find `SaveInstances` (referenced at `dependencies.go:527`). In the `session/storage.go` file, locate the `SaveInstances` function (approximately line 246 per the stack research).
- [ ] The existing guard is `if !inst.Started() { continue }` which already skips instances mid-restore (since `inst.started` is false until `Start()` completes). Verify this guard is present and covers the Restoring window.
- [ ] Add an **explicit** additional guard as defense-in-depth, immediately after the `!inst.Started()` check:
  ```go
  if inst.Status == session.Restoring {
      log.Warn("SaveInstances: skipping Restoring instance (transient status)", "session", inst.Title)
      continue
  }
  ```
  This makes the invariant explicit and protects against future refactors that might inadvertently set `inst.started = true` before restore completes.

#### Story 4.4: Startup safety guard — reset persisted Restoring to Creating at boot

> **New story (CONCERN-1 fix)**: ADR-018 Decision point 4 requires a startup safety guard that scans loaded instances on boot and resets any that carry status `Restoring` to `Creating`. Without this, a future regression that accidentally persists `Restoring` to the DB would cause all affected sessions to appear stuck as `Restoring` on every subsequent boot with no automatic recovery path.

**Files**: `server/dependencies.go`

**Tasks**:
- [ ] In `BuildRuntimeDeps`, immediately **after** instances are loaded from storage (before the restore goroutine is launched), add a safety guard scan:
  ```go
  // Startup safety guard (ADR-018 §4): if a previous run accidentally persisted
  // Restoring status (which must never be written to the DB), reset those sessions
  // to Creating so they participate in the normal restore loop.
  for _, inst := range instances {
      if inst.Status == session.Restoring {
          log.Warn("startup safety guard: found persisted Restoring status; resetting to Creating",
              "session", inst.Title)
          inst.Status = session.Creating
      }
  }
  ```
- [ ] This scan runs synchronously before the restore goroutine starts, so there are no race conditions with the restore loop.
- [ ] Confirm that `SaveInstances` in Step 6.5 will not re-persist `Creating` for instances that were erroneously reset — it should not, since `Creating` is a valid persisted status and these instances will be started normally by the restore loop.
- [ ] Run `make build` to confirm no compile errors.

---

### Epic 5: Frontend — Restoring state UI

**Goal**: Add `RESTORING` to `getStatusText`/`getStatusColor` in `SessionCard.tsx`, apply the `cardPaused` opacity dim to Restoring cards, update `getStatusLabel` in `SessionDetailView.tsx`, and show the terminal loading overlay for Restoring sessions.

#### Story 5.1: Add Restoring to SessionCard status functions

**Files**: `web-app/src/components/sessions/SessionCard.tsx`

**Tasks**:
- [ ] At line 157–158, add a new `isRestoring` derived flag alongside the existing `isCreating`/`isPaused` flags:
  ```ts
  const isRestoring = session.status === SessionStatus.RESTORING;
  ```
  Note: `SessionStatus.RESTORING` will exist after `make generate-proto` completes in Epic 1 Story 1.2.
- [ ] In `getStatusColor()` (line 162), add a case before `default`:
  ```ts
  case SessionStatus.RESTORING:
    return statusLoading;
  ```
  This reuses the existing muted/grey CSS class — no new CSS needed.
- [ ] In `getStatusText()` (line 185), add a case before `default`:
  ```ts
  case SessionStatus.RESTORING:
    return "Restoring…";
  ```
- [ ] In the card root `className` array (line 376–384), add `isRestoring` to the opacity-dim condition alongside `isPaused`:
  ```ts
  isPaused || isRestoring ? cardPaused : "",
  ```
  Replace the existing `isPaused ? cardPaused : ""` entry with the above line.
- [ ] In the `data-paused` attribute (line 387), add a matching `data-restoring` attribute:
  ```tsx
  data-restoring={isRestoring ? "true" : undefined}
  ```

#### Story 5.2: Add Restoring to SessionDetailView status label

**Files**: `web-app/src/components/sessions/SessionDetailView.tsx`

**Tasks**:
- [ ] In `getStatusLabel()` (line 81), add a case before `default`:
  ```ts
  case SessionStatus.RESTORING: return "Restoring";
  ```
- [ ] In the paused overlay block (line 669), add a parallel Restoring overlay immediately after the closing `}` of the PAUSED block (after line 695):
  ```tsx
  {session.status === SessionStatus.RESTORING && (
    <div
      className={pausedOverlay}
      role="status"
      aria-live="polite"
      aria-label="Session is restoring"
    >
      <span className={pausedOverlayIcon} aria-hidden="true">⏳</span>
      <p className={pausedOverlayTitle}>Restoring session…</p>
      <p className={pausedOverlayReason}>
        This session is reconnecting to the terminal. It will be ready shortly.
      </p>
    </div>
  )}
  ```
  This uses the existing `pausedOverlay`, `pausedOverlayIcon`, `pausedOverlayTitle`, `pausedOverlayReason` CSS classes (no new CSS), matching the established overlay pattern. The Resume button is intentionally absent since the user cannot interact with a Restoring session.

#### Story 5.3: Force terminal loading overlay for Restoring sessions

**Files**: `web-app/src/components/sessions/SessionDetailView.tsx` (or `TerminalOutput.tsx`)

**Tasks**:
- [ ] The Restoring overlay in Story 5.2 renders above the terminal pool (same as the Paused overlay). This overlay approach is sufficient: it covers the terminal area and shows the restoring state without requiring changes to `TerminalOutput.tsx`. No additional `isLoadingInitialContent` prop changes are needed since the overlay sits above the terminal DOM.
- [ ] Confirm that clicking a Restoring session navigates to it successfully (the `SessionDetailView` should render with the overlay visible over the terminal). No route-level guard is needed — allow navigation so the overlay is immediately visible and the terminal can auto-attach once the status transitions to Active.
- [ ] Verify that the `TerminalOutput` component's own `isLoadingInitialContent` overlay does not conflict with the Restoring overlay. Since both are `position: absolute` with `inset: 0`, they stack; the outer Restoring overlay (rendered outside the `TerminalOutput` component) takes visual precedence. This is acceptable — both convey "not ready yet."

#### Story 5.4: TypeScript type safety

**Files**: `web-app/src/components/sessions/SessionCard.tsx`, `web-app/src/components/sessions/SessionDetailView.tsx`

**Tasks**:
- [ ] After `make generate-proto`, run `cd web-app && npx tsc --noEmit` to confirm no TypeScript errors from the new `SessionStatus.RESTORING` enum value usage.
- [ ] Run `cd web-app && npx jest --no-coverage` to confirm no unit test regressions.
- [ ] If any Jest snapshot test fails because the status enum grew a new value, update the snapshot.

---

## Architectural Choices Flagged

1. **Analytics late-bind: `atomic.Pointer[ent.Client]` in both structs** (patched from channel approach): The plan uses `atomic.Pointer[ent.Client]` added to BOTH `RuntimeDeps` and `ServerDependencies`, with the pointer copied (not the value) in `ToServerDeps()`. This matches ADR-019 and avoids the struct-boundary nil problem that would have silently broken the channel approach. The `wireDepsIntoServer` late-bind goroutine polls the atomic pointer with a 500ms sleep loop and `serverCtx.Done()` cancellation so it exits cleanly on shutdown.

2. **`SetAnalyticsProvider` on SessionService**: The late-bind goroutine calls `deps.SessionService.SetAnalyticsProvider(analyticsProvider)`. Confirm this setter exists on `SessionService` (search `server/services/session_service.go` for `SetAnalyticsProvider`). If absent, add it. Without this, the `LogAnalyticsProvider` wired at startup is never replaced.

3. **Restoring → Active event publish inside `inst.Start()`**: Story 4.2 verifies whether `inst.Start()` internally publishes the status transition event. If `transitionTo()` in `instance_state.go` fires `onStatusChange` callbacks which call `eventBus.Publish`, no extra publish is needed in the restore loop. If it does not, the explicit publish must be added. Check `session/instance_state.go` line ~32 and trace the `After` hook chain.

4. **Cold restore stagger**: The plan removes the 200ms stagger unconditionally. The pitfalls research confirms hot-attach restores fork no processes. For cold restores (dead tmux session), macOS `ulimit -u` defaults allow 25 simultaneous `tmux new-session` calls without hitting the process limit. If future CI testing on Linux containers with tight `pids.max` fails, a configurable cold-restore stagger (e.g., `STAPLER_SQUAD_COLD_RESTORE_STAGGER_MS`) can be added without reverting the main change.

5. **`AnalyticsClientPtr` field export**: Both `RuntimeDeps.AnalyticsClientPtr` (type `atomic.Pointer[ent.Client]`) and `ServerDependencies.AnalyticsClientPtr` (type `*atomic.Pointer[ent.Client]`) must be exported (capital letter) since both files are in `package server`. The pointer-to-pointer indirection in `ServerDependencies` is intentional: `ToServerDeps()` copies the pointer address (`&deps.AnalyticsClientPtr`), so both structs share the same underlying atomic pointer and the late-bind goroutine's `Store` is immediately visible via either reference.
