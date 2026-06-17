# Stack Research: Fast Startup with Background Session Restore

## 1. Lazy/Async Dependency Initialization in Go

### Problem
`analytics.OpenAnalyticsDB()` (~11s) and `ExternalDiscovery.ScanFromUserOptions()` (~2s) currently block before the HTTP server binds. We need them to run in goroutines with nil-safe call sites.

### Existing Patterns in This Codebase

**Pattern A: Background goroutine with nil-pointer guard (current analytics pattern)**

The analytics client is already a `*ent.Client` pointer in `ServerDependencies.AnalyticsEntClient`. Every call site in `server/server.go` is already nil-guarded:
```go
if deps.AnalyticsEntClient != nil {
    analyticsProvider = analytics.NewSQLiteAnalyticsProvider(deps.AnalyticsEntClient)
    ...
}
```
This nil-check pattern is already used at 5+ sites. The analytics client can be made async with minimal call-site change if the struct field is promoted to an `atomic.Pointer[ent.Client]` or a channel-based ready gate.

**Pattern B: `sync.Once` for idempotent one-shot initialization**

`session/cdp/deps_check.go` uses `sync.Once` for a cached, lazy dep check:
```go
var (
    depsOnce   sync.Once
    cachedDeps DepsResult
)
func CheckDependencies() DepsResult {
    depsOnce.Do(func() { cachedDeps = checkDependencies() })
    return cachedDeps
}
```
`sync.Once` is idiomatic for "compute once, cache forever" but it blocks callers until the work completes — it does not yield nil immediately. It suits background pre-warming but not "give me nil now, fill me in later."

**Pattern C: `atomic.Pointer[T]` for lock-free swap-in**

`session/controller_manager.go` and `session/claude_controller.go` use `atomic.Pointer[T]` extensively for sub-components set once in `Start()` and cleared in `Stop()`:
```go
statusManager atomic.Pointer[InstanceStatusManager]
ptyAccess     atomic.Pointer[PTYAccess]
```
This is the codebase's preferred pattern for "set once, read many times concurrently without a lock." It returns nil immediately before the value is set, exactly what is needed for the analytics client.

**Pattern D: Background goroutine in `BuildRuntimeDeps`**

The session restore loop is already in a background goroutine (see `dependencies.go:479`). The `ExternalDiscovery.Start()` call and analytics open can follow the same pattern.

### Recommended Approach for Analytics DB

Wrap `ServerDependencies.AnalyticsEntClient` (or a shared holder struct) in an `atomic.Pointer[ent.Client]`. Start a goroutine in `BuildRuntimeDeps` that opens the DB and calls `ptr.Store(client)` when done. All existing `if deps.AnalyticsEntClient != nil` guards become `if ptr.Load() != nil` — semantically identical, zero blocking.

For components wired in `wireDepsIntoServer` that need the client (e.g. `SessionService.SetAnalyticsClient`, escape writer), they can be wired lazily from the background goroutine after the store, or polled with a ticker until non-nil. A simpler approach: pass the `*atomic.Pointer[ent.Client]` to services that need it, and let them do `ptr.Load()` on each use.

**Alternative: channel-based ready gate**

```go
type AsyncAnalyticsClient struct {
    readyCh chan struct{}
    client  *ent.Client
    err     error
}
func (a *AsyncAnalyticsClient) Wait(ctx context.Context) (*ent.Client, error) {
    select {
    case <-a.readyCh: return a.client, a.err
    case <-ctx.Done(): return nil, ctx.Err()
    }
}
func (a *AsyncAnalyticsClient) TryGet() *ent.Client { ... } // non-blocking, returns nil
```

This is clean but introduces a new abstraction. The `atomic.Pointer` approach requires less structural change to the existing codebase.

### ExternalDiscovery.Start() async pattern

`session/external_discovery.go:69` shows `ScanFromUserOptions()` called synchronously before polling starts. The `sessionsMu` RWMutex in `ExternalSessionDiscovery` already guards concurrent map access. Making the call async is safe: move `ScanFromUserOptions()` inside a goroutine inside `Start()`, and the mutex ensures no race on the `sessions` map.

---

## 2. In-Memory-Only `Restoring` Status

### Current Status Type

`session/instance.go` defines `Status` as `type Status int` with five constants:
```go
Creating    Status = 0
Active      Status = 1
Paused      Status = 2
Stopped     Status = 3
Hibernated  Status = 4
```

The status is persisted as an `int` in the ent schema (`field.Int("status")` in `session/ent/schema/session.go`). The `StatusToProto` switch in `server/adapters/instance_adapter.go` is exhaustive; unknown values fall through to `SESSION_STATUS_UNSPECIFIED`.

### In-Memory-Only Status Strategy

Adding `StatusRestoring Status = 5` works cleanly because:

1. **Ent schema is `field.Int`**: Not a string enum. Ent does not enforce a closed set of integers at the schema level for `field.Int`. No `SchemaType` or `GoType` enum validation is applied. A value of `5` written to SQLite simply stores `5`. **Risk: zero.** If a Restoring session were accidentally persisted, it would reload as an unknown `5` which maps to `Status(5)`. The `String()` method would return `"Status(5)"` and `StatusToProto` would return `UNSPECIFIED` — graceful degradation.

2. **Preventing accidental persistence**: The `Restoring` status is a transient startup window state. The implementation plan must ensure `UpdateInstance` / `storage.SaveInstances` is not called while status is `Restoring`. Since restores happen in the background goroutine before `SaveInstances` is called at Step 6.5 (`dependencies.go:527`), the window is clear: set `Restoring` before `inst.Start()`, clear it (to `Active`/`Creating`) inside `inst.Start()` itself. `SaveInstances` at Step 6.5 runs after all starts complete, so any session that finishes restore will have a stable status.

3. **Event bus push**: `events.NewSessionStatusChangedEvent` and `events.NewSessionUpdatedEvent` are already called from the service layer and poller. Adding `StatusRestoring` requires adding it to `StatusToProto` as `SESSION_STATUS_RESTORING` (after adding to the proto enum). The event bus will push the state change to all `WatchSessions` subscribers naturally.

4. **Proto pipeline**: `SESSION_STATUS_RESTORING = 9` (next available integer) should be added to `proto/session/v1/types.proto`. The React frontend reads this enum value from the generated TypeScript bindings.

### What NOT to Do

Do not add `restoring` to the `field.Int("status")` ent schema — the field is already an unconstrained int, so no schema change is needed. Do not write `Restoring` to the DB in `ent_repository.go:333` (`SetStatus(int(data.Status))`). A guard in `UpdateInstance` or at the call site that skips persistence when `status == StatusRestoring` is the safest approach.

---

## 3. Ent ORM and Integer Status Enum

### Schema Definition

`session/ent/schema/session.go` line 34:
```go
field.Int("status").Comment("Session status: Running, Paused, etc."),
```

This is a plain `int` field — **not** a `field.Enum()` or a field with `GoType()` override. Ent does not generate any validation for integer ranges. The generated `session_create.go` and `session_update.go` will call `SetStatus(int)` accepting any value.

### No String-Enum Risk

There is no `field.Enum("status").Values(...)` definition anywhere in the session schema. This means:
- No generated `sessionv1.StatusValidator` that would reject `5`.
- No SQLite `CHECK` constraint on the column.
- No migration needed to add `StatusRestoring`.

### Ent String Enum Behavior (for contrast)

For reference, if the field *were* a `field.Enum().Values("active", "paused", ...)`, ent generates a `Validator` function called during `Save()` that returns an error for unknown values. The ent docs call this "enum validation." Since the session status field is `field.Int`, this validator is never generated and the risk does not apply.

### Loading Back from DB

When the server restarts and loads sessions from DB (`ent_repository.go:EntRepository.List`), the status integer is mapped back to `session.Status` via `int(sess.Status)`. If a `5` had been written (which the implementation must prevent), it would load as `Status(5)`. The `String()` switch falls through to `fmt.Sprintf("Status(%d)", int(s))` — safe, no panic.

---

## Key Files

| File | Relevance |
|------|-----------|
| `session/instance.go` | `Status` type (int), existing constants 0–4 |
| `session/ent/schema/session.go` | `field.Int("status")` — no enum validation |
| `session/ent_repository.go:333` | `SetStatus(int(data.Status))` — the persistence write |
| `server/dependencies.go:479–587` | Background goroutine for session starts; Step 6.5 SaveInstances |
| `server/dependencies.go:715–727` | Synchronous `OpenAnalyticsDB` call (the 11s blocker) |
| `session/external_discovery.go:55–77` | `Start()` with synchronous `ScanFromUserOptions()` (the 2s blocker) |
| `server/adapters/instance_adapter.go:257–278` | `StatusToProto` switch — needs `Restoring` case |
| `proto/session/v1/types.proto:289–312` | `SessionStatus` enum — needs `SESSION_STATUS_RESTORING = 9` |
| `server/server.go:473–514` | `AnalyticsEntClient` nil-guard pattern — already correct |
| `session/cdp/deps_check.go` | `sync.Once` pattern reference |
| `session/controller_manager.go` | `atomic.Pointer[T]` pattern reference |
