# Pitfalls & Race Conditions: Fast Startup with Background Session Restore

## 1. Analytics DB Nil-Safety Audit

### Call sites

| File | Line(s) | How it accesses the client | Nil-safe? |
|---|---|---|---|
| `server/server.go` | 473–505 | Guards every use with `if deps.AnalyticsEntClient != nil` | Yes |
| `server/handlers/analytics_handler.go` | 207–221 | `HandleSummary` returns an empty response immediately when `h.client == nil` | Yes |
| `server/services/analytics_escape_service.go` | 26, 124 | `QueryEscapeAnalytics` / `GetEscapeAnalyticsSummary` return `CodeUnavailable` when `s.analyticsClient == nil` | Yes |
| `server/handlers/telemetry_handler.go` | (whole file) | Receives an `AnalyticsProvider` interface that is always non-nil (falls back to `LogAnalyticsProvider` when the DB is absent) | Yes |
| `session/storage.go` | 229–231 | `GetEntClient()` returns nil when storage is not ent-backed; every caller guards with `if client != nil` | Yes |

### Verdict

All existing call sites already handle a nil analytics client gracefully. Making `OpenAnalyticsDB` async introduces **no new nil-dereference risk** provided the wiring in `wireDepsIntoServer` runs after the goroutine has set `AnalyticsEntClient`. However, there is a **timing window pitfall** (see section 1a below).

### 1a. Timing window: `wireDepsIntoServer` reads `deps.AnalyticsEntClient` synchronously

`BuildRuntimeDeps` constructs `analyticsClient` synchronously today (lines 715–727 of `server/dependencies.go`). If this is moved to a goroutine, `BuildRuntimeDeps` will return with `AnalyticsEntClient == nil`, and `wireDepsIntoServer` will wire the `LogAnalyticsProvider` fallback permanently — the SQLite provider will **never** be activated once the DB opens.

The fix requires an explicit late-binding mechanism, e.g.:
- A `chan *ent.Client` that `wireDepsIntoServer` listens on in a background goroutine to swap the provider after the DB opens, **or**
- An `atomic.Pointer[ent.Client]` in `SessionService` / `AnalyticsHandler` that the goroutine writes once open.

Without this mechanism, the async move silently degrades all analytics writes to log-only for the lifetime of the process.

### 1b. Retention enforcer and EscapeEventBatchWriter miss the late client

`analytics.StartRetentionEnforcer` and `analytics.NewEscapeEventBatchWriter` are also gated on `deps.AnalyticsEntClient != nil` inside `wireDepsIntoServer` (lines 483–494 of `server/server.go`). If the client arrives late, neither is started. These are not restartable without a dedicated retry loop — they must be started from within whatever goroutine eventually receives the opened client.

---

## 2. External Discovery Race: ScanFromUserOptions in a Goroutine

### Current structure

`ExternalSessionDiscovery.Start()` (`session/external_discovery.go`, lines 55–77) runs synchronously in `wireDepsIntoServer`:

```
ScanFromUserOptions()   ← blocks ~2s (single tmux list-sessions call)
StartPolling(ctx, 5s)   ← kicks off background ticker
```

### What the proposal changes

Move the `ScanFromUserOptions()` call inside a goroutine so `Start()` returns immediately.

### Race analysis

The `ExternalSessionDiscovery.sessions` map is protected by `sessionsMu sync.RWMutex`. All reads in `GetSessions()`, `GetSession()`, `GetSessionByTmux()`, and writes in `handleNewSession()` / `handleRemovedSession()` hold the appropriate lock. `ScanFromUserOptions()` within `mux.Discovery` also takes `d.mu.Lock()` before mutating `d.sessions`.

There is **no race on the map itself** from moving the scan to a goroutine. However, there are two **semantic races**:

**Race 2a — `OnSessionAdded` callback fires before `ReviewQueuePoller` and `PRStatusPoller` instances are set**

The callbacks registered with `OnSessionAdded` in `BuildRuntimeDeps` (lines 611–624) call:
- `storage.AddInstance(instance)`
- `reviewQueuePoller.AddInstance(instance)`
- `svc.PRStatusPoller.AddInstance(instance)`

These are safe to call concurrently — each method is independently mutex-protected. **No race.**

**Race 2b — `ExternalApprovalMonitor.IntegrateWithDiscoveryTmux` called in `wireDepsIntoServer` after `Start()` returns**

`wireDepsIntoServer` calls `Start()` then `IntegrateWithDiscoveryTmux` (line 292 of `server/server.go`). If the goroutine inside the new async `Start()` fires `handleNewSession` before `IntegrateWithDiscoveryTmux` is wired, the approval monitor will not yet be connected to the streamer manager. External sessions discovered in this window will miss the approval integration.

**Risk level: low.** The window is only the few microseconds between the goroutine scheduler starting the scan goroutine and `IntegrateWithDiscoveryTmux` completing in the calling goroutine. In practice the scan takes ~2s so the monitor is wired long before results arrive. But it is a real data race on behavior if a session happens to be discovered via a very fast cache hit.

**Mitigation:** Move `IntegrateWithDiscoveryTmux` to before `Start()`, or pass the monitor into `Start()` so wiring is complete before the goroutine fires.

**Race 2c — `WatchSessions` snapshot may miss external sessions discovered during the scan window**

If a client connects and calls `WatchSessions` after the HTTP server binds but before `ScanFromUserOptions` completes, the initial snapshot (from `reviewQueuePoller.GetInstances()`) will not include external sessions. These will appear as `SessionCreated` delta events once the scan fires the `OnSessionAdded` callback. This is **acceptable behavior** per US-5 ("External sessions may appear slightly after startup").

---

## 3. Stagger Removal Risk

### What the stagger protects against

Comment at `server/dependencies.go` line 486–487:
> "Stagger starts by 200ms each to avoid a fork burst that saturates the cgroup pids.max limit when many sessions restore simultaneously."

The stagger is 200ms × N sessions. At 25 sessions this is 5s of added startup time.

### Hot-attach vs cold restore — fork cost

`instance.go` lines 790–810 show the hot-restore path:
```
i.pm().RestoreWithWorkDir(workDir)   ← attaches a PTY to the existing tmux session
```

`tmux/tmux.go` `RestoreWithWorkDir` calls `tmux attach-session` or re-opens a PTY — **it does not call `tmux new-session`**. No new child process is forked.

The fork metrics system (`session/tmux/fork_metrics.go`) tracks `new-session` spawns via the `timestampRing`. A hot-attach bypasses the `new-session` command path entirely, so it does **not register as a fork event**.

**Conclusion:** Removing the stagger for hot-attaches (tmux session alive) poses no fork-pressure risk. The comment was written conservatively for cold restores (which do call `tmux new-session`).

### macOS-specific fork limits

macOS does not expose `cgroup pids.max` (that is a Linux cgroups v2 concept). The `fork_metrics.go` implementation is a pure in-process counter — it does not interact with any OS-level limit. On macOS the risk model is:
- `launchctl` resource limits (rare; only applies to daemons in restricted domains)
- `ulimit -u` (per-user process limit, default 1333 on macOS 14+)

At 25 cold restores all firing simultaneously, 25 `tmux new-session` calls would spawn at most 25 child processes. This is well within typical `ulimit -u` limits. The real risk on macOS is file-descriptor exhaustion, not fork pressure.

### Recommendation

Remove the stagger unconditionally for the hot-attach path. Retain a configurable stagger (e.g., 50ms, not 200ms) for cold restores that call `pm().Start()`, since those do fork real processes.

---

## 4. Restoring Status Persistence Risk

### Does `Restoring` get written to the DB?

`SaveInstances` in `session/storage.go` (line 246) has this guard:
```go
if !inst.Started() {
    continue
}
```

`Instance.Started()` (`session/instance_state.go` line 197) returns `i.started`, which is set to `true` only after `instance.go` line 878 or 1253 — both within the successful completion of `Start()`.

**During the restore window** (`inst.Start(false)` has been called but has not yet returned), `inst.started` is `false`. Therefore:

- If the server crashes **during** a hot-restore (while the goroutine in `BuildRuntimeDeps` is inside `Start()`), `SaveInstances` at Step 6.5 (line 527) will skip this instance entirely — **no `Restoring` status is persisted**. The DB retains the pre-crash status (`Active`, `Creating`, or `Stopped`).
- If the proposed `Restoring` status is set **before** `Start()` is called (i.e., `inst.Status = Restoring` + `inst.started = true` is set to allow persistence), then a crash during restore would leave the session stuck in `Restoring` on the next boot.

**The current design is actually crash-safe** as long as `Restoring` is only an in-memory transient status:
- Never write `inst.started = true` before the restore completes.
- Never call `SaveInstances` with a `Restoring`-status instance.

If `Restoring` is added to the ent schema as a valid status value and accidentally written during the window (e.g., by `UpdateInstanceStatus` called from a controller), sessions will be stuck on next boot. A startup migration to clear any persisted `Restoring` → `Creating` is required.

**Action items:**
1. Add `StatusRestoring` to `instance.go` but keep it transient (never persist to DB).
2. If the ent schema is extended to include `restoring` as a valid value, add a startup migration: `UPDATE sessions SET status = 'creating' WHERE status = 'restoring'`.
3. `SaveInstances` (and `saveInstancesToRepo`) must explicitly skip instances in `Restoring` status, not just instances where `!inst.Started()`.

---

## 5. WatchSessions Race During the Restore Window

### Snapshot correctness

`WatchSessions` reads the session list from `reviewQueuePoller.GetInstances()` (line 1688 of `session_service.go`). This returns the in-memory slice set during `BuildRuntimeDeps` at the `ReviewQueuePoller.SetInstances(instances)` call (which happens **before** the background goroutine starts restoring sessions — `warren.SetAlways` runs synchronously at line 468).

Clients connecting before any session finishes restoring will receive a snapshot with each session in whatever status it was loaded from the DB (`Active`, `Creating`, etc.). If the proposal sets `inst.Status = Restoring` **before** the `SetInstances` call, the snapshot correctly shows `Restoring`.

### Delta stream correctness

When a session transitions from `Restoring → Active`, an event must be published to the `EventBus` so `WatchSessions` can push a `SessionUpdated` or `SessionStatusChanged` delta. The existing `setStatus()` / `transitionTo()` path in `instance_state.go` publishes events via `onStatusChange` callbacks which ultimately call `eventBus.Publish`. This path is correct **if** `Restoring → Active` uses the same `transitionTo` mechanism.

**Risk:** If the restore goroutine sets `inst.Status = Active` directly (bypassing `transitionTo`) after a hot-attach, no event is published and connected `WatchSessions` clients will never see the status flip until the next polling cycle (up to 5s). Use `transitionTo(ctx, Active)` (or the equivalent `setStatus(Active)` + explicit `eventBus.Publish`) to guarantee delta delivery.

### Status filter race

`WatchSessions` applies a `StatusFilter` to both the snapshot (line 1703) and to real-time events (line 1736). If a client connects with `StatusFilter = ACTIVE` during the restore window, `Restoring` sessions are excluded from the snapshot. When those sessions flip to `Active` they emit a `StatusChanged` event which passes the filter and is sent to the client — the client sees a sudden `SessionUpdated` with no prior `SessionCreated`. Clients must handle this as an implicit create (upsert semantics). This is the existing pattern for reconnecting clients per the comment at line 1667, so it is **by design**, but should be documented for the frontend implementer.

---

## Summary of Critical Risks

| # | Risk | Severity | Mitigation |
|---|---|---|---|
| 1a | Async analytics open silently keeps log-only fallback forever | **High** | Late-bind the analytics provider via atomic pointer or a post-open callback |
| 1b | Retention enforcer and batch writer never start | **Medium** | Start them from the goroutine that opens the DB, not from `wireDepsIntoServer` |
| 2b | Approval monitor not wired before scan goroutine fires | **Low** | Move `IntegrateWithDiscoveryTmux` to before `Start()` |
| 4 | Persisted `Restoring` status leaves sessions stuck on next boot | **High** | Keep `Restoring` transient; add startup migration if schema extended |
| 5 | `Restoring → Active` via direct assignment skips EventBus | **Medium** | Always use `transitionTo` or `setStatus` + explicit publish |
