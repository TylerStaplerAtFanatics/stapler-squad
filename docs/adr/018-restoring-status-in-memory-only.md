# ADR-018: StatusRestoring as In-Memory-Only Transient Status

**Status**: Accepted
**Date**: 2026-06-17

## Context

The fast-startup initiative requires sessions to be visible in the UI while they are mid-restoration at startup. A new `StatusRestoring` constant is needed so session cards can render a "Restoring…" state with dimmed opacity rather than appearing fully interactive before tmux re-attachment completes.

The session status field in the ent schema is `field.Int("status")` — a plain integer with no closed-set validation, no SQLite `CHECK` constraint, and no generated ent validator. `SaveInstances` in `session/storage.go` already gates persistence on `inst.Started()` returning true, and `inst.started` is only set to `true` after `Start()` successfully completes. During the restore window, `inst.started` is `false`, so a `Restoring`-status session is naturally skipped by `SaveInstances`.

The risk of accidental persistence is real if `Restoring` is ever paired with `inst.started = true`, or if `UpdateInstanceStatus` is called from a controller path while the session is mid-restore. A crash during restore while `Restoring` is persisted would leave sessions permanently stuck in that status on the next boot.

## Decision

`StatusRestoring` is added as a Go constant (`Status = 5`) in `session/instance.go` and kept **strictly in-memory**:

1. `StatusRestoring` is never written to the database. `SaveInstances` (and `saveInstancesToRepo`) must explicitly skip instances whose status is `Restoring`, independent of the `!inst.Started()` guard.
2. `inst.started` is never set to `true` before the restore completes — `Restoring` is set before `inst.Start()` is called, and `Start()` itself transitions the status to `Active` (or back to `Creating` on failure) before returning.
3. No ent schema change is required. The `field.Int("status")` column already stores any integer value without validation; adding `StatusRestoring` requires no migration.
4. A startup safety guard scans loaded instances on boot and resets any that carry status `5` (Restoring) to `Creating`. This guards against any future regression where a crash path manages to persist a `Restoring` value.
5. `StatusToProto` in `server/adapters/instance_adapter.go` maps `StatusRestoring` to `SESSION_STATUS_RESTORING` (proto enum value 9), which is added to `proto/session/v1/types.proto`.
6. Status transitions from `Restoring → Active` must use `transitionTo` or the `setStatus` + explicit `eventBus.Publish` path — never direct field assignment — so `WatchSessions` subscribers receive the delta event.

## Consequences

### Positive
- No database migration is needed; existing sessions.db is unaffected.
- No ent schema regeneration is required.
- Sessions stuck in `Restoring` on an unexpected crash are impossible under correct implementation, and the startup guard provides a safety net against regressions.
- The proto/React pipeline receives the new status via the existing `StatusToProto` → TypeScript bindings path with a single enum addition.
- `WatchSessions` clients that connected with a status filter of `ACTIVE` during the restore window will receive a `SessionUpdated` delta when the session transitions — consistent with the existing upsert-on-reconnect contract.

### Negative / Risks
- Two independent guards (`!inst.Started()` and explicit `status != Restoring` skip) must stay in sync. If either is removed, the other may not be sufficient.
- If any code path calls `UpdateInstance` while a session is in `Restoring` status (e.g., a controller reacting to a tmux event), the status value `5` would be written to the DB unless `ent_repository.go:SetStatus` also guards against it.
- The startup scan that resets `Restoring → Creating` adds a small O(N) pass over loaded sessions at boot; negligible at current session counts.

### Mitigations
- Add an explicit `if inst.Status == StatusRestoring { continue }` guard in both `SaveInstances` and `saveInstancesToRepo`, documented with a comment explaining the transient-only contract.
- Add a guard in `ent_repository.go` `UpdateInstance` (or at the call site in `instance_state.go`) that logs a warning and skips the DB write when `status == StatusRestoring`.
- The startup scan (`status == 5 → Creating`) is a single SQL `UPDATE sessions SET status = 0 WHERE status = 5` or equivalent ent query, run once in `BuildRuntimeDeps` before instances are loaded into memory.
