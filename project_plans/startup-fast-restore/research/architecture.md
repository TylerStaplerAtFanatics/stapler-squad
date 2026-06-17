# Architecture Research: Startup Sequence & Parallelization

## 1. BuildRuntimeDeps Dependency Graph

### Where analytics DB open sits in the call graph

`BuildRuntimeDeps` is called synchronously inside `BuildDependencies`, which is
called synchronously inside `NewServer` before `wireDepsIntoServer` and before
the HTTP server binds.  The analytics open call is near the **end** of
`BuildRuntimeDeps` (lines ~716-727 in `server/dependencies.go`):

```go
var analyticsClient *ent.Client
if configDir, configErr := config.GetConfigDir(); configErr == nil {
    ctx := context.Background()
    if ac, acErr := analytics.OpenAnalyticsDB(ctx, configDir); acErr != nil {
        log.Warn("could not open analytics DB (will use log-only fallback)", "err", acErr)
    } else {
        analyticsClient = ac
    }
}
```

`analyticsClient` is returned as `RuntimeDeps.AnalyticsEntClient` and flows into
`ServerDependencies.AnalyticsEntClient`.

### Call sites in server.go (wireDepsIntoServer)

All five call sites are already guarded with `if deps.AnalyticsEntClient != nil`:

| Line | Use |
|------|-----|
| 473 | `analytics.NewSQLiteAnalyticsProvider(deps.AnalyticsEntClient)` |
| 483 | `analytics.StartRetentionEnforcer(...)` |
| 491 | `analytics.NewEscapeEventBatchWriter(deps.AnalyticsEntClient, ...)` |
| 504 | `deps.SessionService.SetAnalyticsClient(deps.AnalyticsEntClient)` |
| 514 | `handlers.NewAnalyticsHandlerWithClient(analyticsProvider, deps.AnalyticsEntClient)` |

**Key finding**: every consumer already handles `nil` gracefully via the `!= nil`
guard. The fallback `analytics.NewLogAnalyticsProvider()` is already used when
`analyticsClient == nil` (line 477). There is **no component that hard-requires
`analyticsClient` to be non-nil before the HTTP server can start**.

### SessionService.analyticsClient (analytics_escape_service.go)

`session_service.analyticsClient` is set via `SetAnalyticsClient`. Both RPC
methods in `analytics_escape_service.go` already gate on `s.analyticsClient == nil`
and return an appropriate error. No nil-safety work needed here.

### Components that do NOT need analyticsClient

Everything else: `SessionService`, `Storage`, `EventBus`, `ReviewQueue`,
`StatusManager`, `ReviewQueuePoller`, `PRStatusPoller`, `ScrollbackManager`,
`TmuxStreamerManager`, `ExternalDiscovery`, all instance lifecycle operations,
`WatchSessions`, terminal streaming, session create/start/stop/pause/resume,
`WorkflowScheduler`, `InsightsService`, `HeadlessPool`.

### Safe async window

`analyticsClient` is consumed **only inside `wireDepsIntoServer`**, which runs
after `BuildRuntimeDeps` returns. If `BuildRuntimeDeps` launches analytics open
in a goroutine and stores the result in a channel or atomic pointer, the main
startup path can proceed. `wireDepsIntoServer` would need to receive the
analytics client from that channel (with a short context deadline so it can still
fall back to `LogAnalyticsProvider` if the open is not done yet).

---

## 2. ExternalDiscovery Startup: Mutex Safety

### The synchronous blocker

`ExternalSessionDiscovery.Start()` in `session/external_discovery.go` calls
`e.discovery.ScanFromUserOptions()` **synchronously** (line 69) before starting
the polling goroutine. `ScanFromUserOptions` runs `tmux list-sessions` (~2s cold).

### Mutex structure in mux.Discovery

`ScanFromUserOptions` acquires `d.mu.Lock()` to merge results into `d.sessions`
(lines 163-175), then releases the lock before firing callbacks. The callbacks
(added via `OnSessionChange`) are invoked outside the lock.

The `ExternalSessionDiscovery.sessions` map is protected by its own
`e.sessionsMu sync.RWMutex` (separate from `mux.Discovery.mu`). All reads/writes
go through `handleNewSession` / `handleRemovedSession`, which correctly lock
`e.sessionsMu`.

### Is ScanFromUserOptions safe in a goroutine?

**Yes.** The mutex structure is already correct:
- `d.mu` protects `d.sessions` (mux-level map) — acquired/released within `ScanFromUserOptions`.
- `e.sessionsMu` protects `e.sessions` (instance-level map) — acquired/released within `handleNewSession`.
- No caller requires `GetSessions()` to be populated before the HTTP server is up.

**The only caller of `ScanFromUserOptions` is `Start()`**. Changing `Start()` to
launch the scan in a goroutine and return immediately would be a 2-line change
with no race hazards:

```go
func (e *ExternalSessionDiscovery) Start(interval time.Duration) {
    e.ctx, e.cancel = context.WithCancel(context.Background())
    e.discovery.OnSessionChange(func(discovered *mux.DiscoveredSession, isNew bool) { ... })
    go func() {
        if _, err := e.discovery.ScanFromUserOptions(); err != nil {
            log.Warn("ScanFromUserOptions failed", "err", err)
        }
    }()
    e.discovery.StartPolling(e.ctx, interval)
    log.Info("external session discovery started", "interval", interval)
}
```

---

## 3. Session Restore Goroutine: Hot-Attach vs. Cold Restore

### The 200ms stagger (dependencies.go lines 488-498)

```go
for i, inst := range instances {
    if !inst.Started() {
        if i > 0 {
            time.Sleep(200 * time.Millisecond)
        }
        if err := inst.Start(false); err != nil { ... }
    }
}
```

With 25+ sessions this is 25 × 200ms = 5s of serialized sleep.

### What `inst.Start(false)` does on hot-attach vs. cold restore

**Hot-attach (tmux session alive)**:
- `i.pm().IsAlive()` returns `true`.
- Calls `i.pm().RestoreWithWorkDir(workDir)`.
- `TmuxSession.RestoreWithWorkDir` verifies session existence (up to 5 retries
  with backoff) then calls `tmux attach-session` to get a PTY handle.
- **No new process is forked.** The agent process already exists inside tmux.
  The only subprocess spawned is `tmux attach-session`, which is a lightweight
  tmux client, not a new agent.

**Cold restore (tmux session dead)**:
- `i.pm().IsAlive()` returns `false`.
- Calls `i.pm().Start(startPath)` which runs `tmux new-session` with the agent
  command — **this forks a new process**.
- The original stagger comment says "avoid a fork burst that saturates cgroup
  `pids.max`".

### Risk assessment for removing the 200ms stagger

- **Hot-attach**: No fork pressure. Stagger is unnecessary. 25 concurrent
  `RestoreWithWorkDir` calls simply open 25 PTY connections to already-running
  tmux sessions. Safe to remove entirely.
- **Cold restore**: Each call runs `tmux new-session ... claude ...`. Running 25
  simultaneously on a machine with a tight `pids.max` cgroup (CI/container
  environments) could hit the limit. For normal desktop use it is unlikely to
  matter, but the requirements explicitly list this as a concern for cold starts.

**Recommendation**: The `Restoring` status approach in the requirements makes
this moot for hot-attaches: mark all instances as `Restoring` upfront, then
restore them. For the cold-restore sub-path, a configurable stagger (or worker
pool) is still defensible; the stagger for hot-attaches should be removed
unconditionally since they do not fork.

---

## 4. Status Update Flow: SetStatus → EventBus → WatchSessions

### State machine

`instance.transitionTo(ctx, to Status)` in `instance_state.go:32` is the only
sanctioned operational way to change an instance's lifecycle status. It:
1. Validates the `(from, to)` edge exists in `transitionIndex` (from `state_machine.go`).
2. Calls the optional `After` hook (used today only for Hibernated↔Active to
   launch async checkpoint I/O).
3. Updates `i.Status`.

There is **no built-in event-bus publish inside `transitionTo`**. Status changes
surface to clients via a separate publish:

### How status changes reach WatchSessions

`SessionService` owns the event bus (`s.eventBus`). When a session's status
changes it calls:

```go
s.eventBus.Publish(events.NewSessionUpdatedEvent(instance, []string{"status"}))
```

`WatchSessions` (session_service.go:1662) subscribes to the event bus
(`s.eventBus.Subscribe(ctx)`) and forwards `EventSessionUpdated` events to
connected clients.

For a new `Restoring` status, the implementation needs to:
1. Set `inst.Status = Restoring` (either via `ForceStatus` or by adding
   `Restoring` to the state machine).
2. Call `eventBus.Publish(events.NewSessionUpdatedEvent(inst, []string{"status"}))`.

The proto adapter in `server/services/adapters/` maps `session.Status` to
`sessionv1.SessionStatus`. A new `Restoring` enum value will need to be threaded
through `proto/session/v1/types.proto` → generated Go → `adapters/` mapping →
frontend proto bindings.

### WatchSessions initial snapshot

On first connect, `WatchSessions` sends a snapshot of all instances from
`s.reviewQueuePoller.GetInstances()`. If instances are added to the poller
**before** the HTTP server starts (which is the current flow — instances are
loaded in `BuildRuntimeDeps`), they will appear in the initial snapshot with
their `Restoring` status immediately when the client connects.

---

## 5. Startup Ordering Invariants

### Hard ordering requirements (must not be parallelized)

1. **tmux.EnsureServerRunning must precede BuildRuntimeDeps** (enforced by
   `TmuxServerReady` token type). Without this, `DoesSessionExist()` may trigger
   `recoverFromServerFailure` and cold-restore all sessions.

2. **BuildCoreDeps → BuildServiceDeps → BuildRuntimeDeps** (phases 1→2→3). Each
   phase depends on outputs of the prior phase. Cannot be parallelized.

3. **Instance wiring (SetReviewQueue, SetStatusManager) must precede
   inst.Start(false)**. `Start()` calls `StartController()` only when
   `GetStatusManager() != nil`.

4. **WorkflowRepository must be initialized before WorkflowScheduler**
   (already enforced in `BuildRuntimeDeps`).

### Soft ordering (safe to defer until after HTTP bind)

5. **Analytics DB open**: All consumers already nil-guard on `AnalyticsEntClient`.
   Can be opened in a goroutine; `wireDepsIntoServer` can receive the client via
   a channel with a timeout, falling back to `LogAnalyticsProvider` immediately.

6. **ExternalDiscovery.ScanFromUserOptions()**: No component depends on the
   initial scan result before the HTTP server is ready. External sessions appear
   in `WatchSessions` snapshots via the `GetSessions()` path, which works with
   zero or partial results.

7. **Session restore goroutine** (the `go func()` at line 479): Already fully
   async. The HTTP server is supposed to start after `BuildRuntimeDeps` returns,
   not after this goroutine finishes. The instances are registered in
   `reviewQueuePoller` before the goroutine runs, so `WatchSessions` will see
   them immediately — just with `Restoring` status.

### Ordering invariants that would break with naive parallelization

- **`storage.GetEntClient()` for WorkflowRepository**: This ent client is the
  **sessions** DB ent client (from `NewSessionServiceFromConfig`), not the
  analytics ent client. They are completely separate. `storage.GetEntClient()`
  is available as soon as Phase 1 (BuildCoreDeps) completes.

- **`analyticsClient` vs `storage.GetEntClient()`**: These are two different
  `*ent.Client` values pointing to two different SQLite files (`sessions.db` vs
  `analytics.db`). Making analytics async does not affect `storage.GetEntClient()`.

- **`ErrorRegistry` depends on `storage.GetEntClient()`** (sessions DB) — already
  initialized in Phase 1, no impact.

---

## Summary of Key Findings

1. **Analytics DB is fully isolatable**: All five call sites in `wireDepsIntoServer`
   already nil-guard `deps.AnalyticsEntClient`. Moving `analytics.OpenAnalyticsDB`
   into a goroutine and threading the result via a channel (with an immediate
   `LogAnalyticsProvider` fallback) requires no nil-safety work at call sites — only
   a small change to how the client flows from `BuildRuntimeDeps` to
   `wireDepsIntoServer`.

2. **ExternalDiscovery.Start() has no race hazard for goroutinization**: Both the
   `mux.Discovery.mu` and `ExternalSessionDiscovery.sessionsMu` mutexes already
   protect all concurrent access to session maps. `ScanFromUserOptions` can safely
   run in a goroutine; no callers require the result before the HTTP server binds.

3. **Hot-attach restores fork zero processes**: `inst.Start(false)` on a
   hot-attach path (tmux session alive) only calls `tmux attach-session` to open
   a PTY — no new agent process is spawned. The 200ms stagger was introduced for
   fork pressure during cold restores. Removing it for the hot-attach path (the
   normal restart case for existing sessions) carries no fork-pressure risk and
   eliminates ~5s of serialized sleep for a 25-session setup.
