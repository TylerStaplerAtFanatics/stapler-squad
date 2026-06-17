# ADR-019: Analytics DB Late-Binding via atomic.Pointer

**Status**: Accepted
**Date**: 2026-06-17

## Context

`analytics.OpenAnalyticsDB()` opens and migrates a 512 MB SQLite database synchronously inside `BuildRuntimeDeps`, blocking the HTTP server from binding for approximately 11 seconds. Moving this call to a background goroutine would let the HTTP server bind immediately, but creates a late-binding problem: `wireDepsIntoServer` reads `deps.AnalyticsEntClient` synchronously after `BuildRuntimeDeps` returns. If the field is nil at that point, the code wires a `LogAnalyticsProvider` fallback and the SQLite provider is never activated for the lifetime of the process — silently degrading all analytics writes to log-only.

Additionally, two components started inside `wireDepsIntoServer` — `analytics.StartRetentionEnforcer` and `analytics.NewEscapeEventBatchWriter` — are gated on `deps.AnalyticsEntClient != nil`. These are not restartable from outside the goroutine that opens the DB; they must be started from within that goroutine once the client is ready.

All existing call sites already handle a nil analytics client gracefully: `server/server.go` nil-guards every use, `analytics_handler.go` returns empty responses immediately, and `analytics_escape_service.go` returns `CodeUnavailable`. The codebase's preferred pattern for "set once, read many times concurrently without a lock" is `atomic.Pointer[T]`, used extensively in `session/controller_manager.go` and `session/claude_controller.go`.

## Decision

`analytics.OpenAnalyticsDB()` is moved to a background goroutine started in `BuildRuntimeDeps`. The analytics client is exposed as an `atomic.Pointer[ent.Client]` rather than a plain pointer field:

1. `ServerDependencies.AnalyticsEntClient` (or a shared holder struct accessible to services) is promoted to `atomic.Pointer[ent.Client]`.
2. The background goroutine calls `OpenAnalyticsDB`, then calls `ptr.Store(client)` on success.
3. All existing `if deps.AnalyticsEntClient != nil` call sites become `if ptr.Load() != nil` — semantically identical, zero blocking.
4. After `ptr.Store`, the goroutine activates the SQLite provider via a registered callback (or direct call), and starts `StartRetentionEnforcer` and `NewEscapeEventBatchWriter` from within the same goroutine. This guarantees those components are started exactly once, with a valid client, regardless of when the DB opens.
5. Services that need per-request access to the client (e.g., `SessionService`, `AnalyticsHandler`) receive the `*atomic.Pointer[ent.Client]` at construction time and call `ptr.Load()` on each use — no polling, no blocking.
6. `wireDepsIntoServer` wires the `LogAnalyticsProvider` fallback as before, but also registers an "on-ready" callback that swaps it for the `SQLiteAnalyticsProvider` once `ptr.Store` fires. The callback is invoked from the background goroutine, not from `wireDepsIntoServer`, so the swap happens automatically without polling.

## Consequences

### Positive
- HTTP server binds in approximately 6 seconds instead of 23 seconds; the 11-second analytics DB open is fully off the critical path.
- The `atomic.Pointer` pattern is already established in this codebase; no new abstraction is introduced.
- All existing nil-safety guarantees are preserved — the analytics client was already nullable at every call site.
- `StartRetentionEnforcer` and `NewEscapeEventBatchWriter` are started with a valid client exactly once, with no polling loop required.
- Analytics functionality is fully available within seconds of the DB opening; there is no permanent degradation.

### Negative / Risks
- Analytics writes issued in the first ~11 seconds after startup (while the DB is still opening) are handled by the `LogAnalyticsProvider` fallback and are not durably stored. This is a narrow startup window, not a steady-state regression.
- The on-ready callback pattern (swapping `LogAnalyticsProvider` → `SQLiteAnalyticsProvider`) adds coupling between the background goroutine and the provider wiring in `wireDepsIntoServer`. If the callback is not registered before the goroutine fires (e.g., due to a very fast open on a warm OS cache), the swap is lost.
- Components that hold a direct reference to the `LogAnalyticsProvider` at construction time (rather than going through the swappable wrapper) will not benefit from the late swap.

### Mitigations
- The on-ready callback is registered before the goroutine is started — the goroutine is launched last in `BuildRuntimeDeps` after all wiring is complete, ensuring no race on callback registration.
- If the analytics DB opens faster than expected (warm cache), `ptr.Store` and the callback still fire correctly because the goroutine starts after callback registration.
- Services that need the client reference the `*atomic.Pointer[ent.Client]` directly rather than a snapshot taken at construction time, ensuring they always see the current value from `ptr.Load()`.
- The `LogAnalyticsProvider` fallback is monitored in tests with a short timeout to verify the swap to `SQLiteAnalyticsProvider` occurs; the swap is observable via a `ptr.Load() != nil` check.
