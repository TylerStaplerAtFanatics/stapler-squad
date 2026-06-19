# Adversarial Review: Fast Startup with Background Session Restore

**Date**: 2026-06-17
**Reviewer role**: Adversarial — find fatal flaws before code is written

---

## Verdict: BLOCKED

---

## Critical Issues (BLOCKED)

### CRITICAL-1: Plan targets the wrong struct for analytics late-bind

**Claim in plan**: Epic 2 Story 2.1 says to add `OnAnalyticsReady func(*ent.Client)` and `analyticsReadyCh chan *ent.Client` to `RuntimeDeps`, then access them from `wireDepsIntoServer`.

**What's actually true**: `wireDepsIntoServer` takes `*ServerDependencies` (line 131 of `server.go`), not `*RuntimeDeps`. The conversion path is `RuntimeDeps → ToServerDeps() → ServerDependencies`. `ToServerDeps()` is a projection function (lines 83–113 of `dependencies.go`) that copies fields one-by-one. A `chan *ent.Client` field added to `RuntimeDeps` but NOT added to `ServerDependencies` and NOT copied in `ToServerDeps()` will be nil when `wireDepsIntoServer` reads it — silently. The late-bind goroutine in `wireDepsIntoServer` will never fire.

The plan also says (line 149): "Rename `analyticsReadyCh` to exported `AnalyticsReadyCh chan *ent.Client` in the `RuntimeDeps` struct so `wireDepsIntoServer` can access it." This shows the author did not realize `wireDepsIntoServer` does not receive a `RuntimeDeps` — it receives a `ServerDependencies`.

**Why it matters**: The entire analytics async open (11s savings, the biggest single gain) will silently not work. The field will be nil after `ToServerDeps()`, the goroutine in `wireDepsIntoServer` won't start, and the fallback `LogAnalyticsProvider` will be permanent. US-4 fails completely, and the bug produces no error — just permanently degraded analytics.

**Fix required**: Either (a) add `AnalyticsReadyCh chan *ent.Client` to BOTH `RuntimeDeps` AND `ServerDependencies`, and copy it in `ToServerDeps()`, or (b) move the late-bind goroutine inside `BuildRuntimeDeps` itself (where `RuntimeDeps` is accessible) and have it store a `atomic.Pointer[ent.Client]` accessible from `deps.AnalyticsEntClient`-equivalent accessor. ADR-019's atomic pointer approach may be cleaner here.

---

### CRITICAL-2: `analyticsClient` is set synchronously at line 716 then returned in `RuntimeDeps` — moving it async leaves the field nil at return time, but the plan also needs `SaveInstances` to not be blocked

**Claim in plan**: Story 2.1 says to delete the synchronous open at lines 716–727 and move it to the background goroutine (Step 5.5). The existing `RuntimeDeps.AnalyticsEntClient` field would be nil at construction time and populated later.

**What's actually true**: This is not inherently broken, BUT the plan says to place Step 5.5 "before Step 6" in the background goroutine (line 94 of plan). However, the actual background goroutine in `dependencies.go` starts at line 479 and already contains Steps 6, 6b, 6c, 6.5, 6.6 and more. The analytics open happening at Step 5.5 (before Step 6's restore loop) could still take ~11s before the restore loop starts — it has just been moved from synchronous to the goroutine. If the intent is to open analytics AND restore sessions concurrently, the plan needs to either (a) open analytics in a separate goroutine within the background goroutine, or (b) interleave them differently. As written, placing analytics open before Step 6 in the same goroutine means the restore loop is still blocked behind the analytics open — just now asynchronously from the HTTP server's perspective. The 5s session stagger savings and the 11s analytics savings are NOT concurrent; they are sequential in the background goroutine.

**Why it matters**: The requirements claim "reduce HTTP server bind time from ~23s to ~6s." If analytics open and session restores are serial in the background goroutine, the HTTP server binds fast but users still wait ~11s for analytics and then ~5s+ for sessions to restore (minus stagger removal). US-3 (terminal auto-attach once restore completes) depends on sessions being Restoring briefly, not for 11+ extra seconds. The architecture needs a dedicated goroutine for analytics, separate from the restore goroutine.

---

### CRITICAL-3: The plan re-uses the loop variable check incorrectly after setting Restoring status

**Claim in plan**: Story 4.1's second loop (the restore loop) checks `if inst.Status == session.Restoring` to decide whether to call `inst.Start(false)`.

**What's actually true**: Step 6b (lines 506–516 of `dependencies.go`) also iterates over the same `instances` slice and calls `inst.Start(false)` for sessions where `inst.Status == session.Stopped && inst.TmuxSessionExists()`. Step 6b runs AFTER Step 6. If `inst.Status` was `Stopped` when loaded but Step 6's first loop set it to `Restoring` (the pre-marking pass), Step 6b's check `inst.Status == session.Stopped` will be false — the session will not be reconciled by Step 6b even if it has a live tmux session.

More critically: the plan's new loop checks `inst.Status == session.Restoring` to decide whether to call `Start()`. But Step 6b immediately follows and will skip `Stopped`-but-alive sessions that the plan inadvertently converted to `Restoring`. Sessions that were `Stopped` in the DB but have a live tmux session — a known crash-recovery scenario — will be left stuck in `Restoring` status permanently (never started by either loop).

**Why it matters**: Silent data corruption for a documented crash-recovery path. Users with sessions that were `Stopped` at last shutdown but whose tmux survived will see `Restoring…` forever with no recovery path.

---

## Concerns (non-fatal but significant)

### CONCERN-1: ADR-018 requires a startup safety guard scan; plan omits it

ADR-018 Decision point 4 explicitly requires: "A startup safety guard scans loaded instances on boot and resets any that carry status 5 (Restoring) to Creating." The plan has no story for this. Without it, if a future regression persists `Restoring` to the DB (e.g., via `UpdateInstance` as warned in ADR-018's Risks section), every subsequent boot will show all sessions as `Restoring` indefinitely with no automatic recovery path. The ADR was written at the same time as the plan and the plan does not implement one of the ADR's own required mitigations.

### CONCERN-2: `NewEscapeEventBatchWriter` signature mismatch in the late-bind goroutine

The plan's late-bind goroutine (Story 2.1) calls:
```go
escapeWriter := analytics.NewEscapeEventBatchWriter(ac, deps.EventBus, serverCtx)
escapeWriter.Start()
```
But the actual call site at `server.go:492–494` is:
```go
escapeWriter := analytics.NewEscapeEventBatchWriter(deps.AnalyticsEntClient, cfg.EscapeAnalyticsMaxRowsPerSession)
go escapeWriter.Start(serverCtx)
```
The plan's version passes `deps.EventBus` and `serverCtx` to the constructor (3 args), but the real constructor signature takes `(client, maxRowsPerSession int)` and `Start` takes `serverCtx`. Additionally, the late-bind goroutine has no access to `serverCtx` (which lives in `wireDepsIntoServer`). This will produce a compile error. The plan also omits calling `pkganalytics.SetGlobalEscapeWriter(escapeWriter)` which is required for `ResponseStream` instances to pick up the writer.

### CONCERN-3: `AnalyticsHandler` holds a direct `*ent.Client` snapshot, not a pointer-to-pointer

Story 2.2 asks the implementer to check if `AnalyticsHandler` has a `SetClient` setter and add one if not. The actual construction at `server.go:514` is `handlers.NewAnalyticsHandlerWithClient(analyticsProvider, deps.AnalyticsEntClient)`. If `AnalyticsEntClient` is nil at construction time (because it's now async), the handler is built with a nil client. Even if a `SetClient` method is added and called from the late-bind goroutine, the `analyticsProvider` argument (already a `LogAnalyticsProvider`) will not be replaced — the handler holds both. The plan says "See Story 2.3 for AnalyticsHandler nil-client upgrade" but Story 2.3 does not exist in the plan.

### CONCERN-4: Race between approvalMonitor and discovery scan ordering fix is based on wrong line numbers

Story 3.2 shows the current code as:
```go
deps.ExternalDiscovery.Start(5 * time.Second)
deps.ExternalApprovalMonitor.Start()
deps.ExternalApprovalMonitor.IntegrateWithDiscoveryTmux(...)
```
The actual code at `server.go:290–292` IS exactly that order. The fix is correct — reorder to call `IntegrateWithDiscoveryTmux` before `Start()`. However, the plan says this closes "Race 2b from the pitfalls research." This fix is independent of Epic 3 (making `ScanFromUserOptions` async) and should be noted as a standalone correctness fix regardless of whether Epic 3 is implemented. As written, a reader might think this only matters if Epic 3 is done first.

### CONCERN-5: `inst.Start(false)` status transition publishing is unverified

Story 4.2 says to verify whether `inst.Start()` internally publishes a `Restoring → Active` event. The plan acknowledges this is unverified ("Search `session/instance_state.go` for `onStatusChange`"). This is not a concern per se, but the plan defers verification to implementation time. If `Start()` does NOT publish the transition event, connected clients will stay showing `Restoring` until the next polling cycle (up to 5s). This is a known gap; the plan notes it but the implementer must check immediately.

### CONCERN-6: `SaveInstances` guard is described as relying on `!inst.Started()` — verify actual behavior under new flow

The plan (Story 4.3) claims the existing `!inst.Started()` guard in `SaveInstances` naturally skips Restoring instances because `inst.started` is false until `Start()` completes. This is correct as described. However, Step 6.5 in `dependencies.go` (lines 526–532) calls `storage.SaveInstances(instances)` AFTER the restore loop completes. At that point, successfully restored instances have `inst.started = true` and `inst.Status = Active`. But if any instance is still in `Restoring` (e.g., `Start()` returned an error and the plan's error path sets status back to `Creating`, not `Restoring`), then the instance will NOT be skipped by `!inst.Started()` — it has `started = false`, so it IS skipped. The explicit Restoring guard is defense-in-depth as stated, but the guard's framing in the plan needs to be precise: the primary protection is `!inst.Started()`, the explicit guard protects against future code changes that might set `started = true` early.

---

## Minor Notes

1. **Line number drift**: The plan's line references (e.g., "line 716", "line 479", "line 488") are accurate to the codebase at review time. These should be treated as approximate — the implementer must verify current line numbers before editing, not use the plan's numbers blindly.

2. **`StatusStringToProto` for "Restoring"**: Story 1.3 correctly adds a case for `"Restoring"` to `StatusStringToProto`. This is needed because `StatusStringToProto` is used for `ReviewItem` statuses stored as strings. However, `Restoring` sessions should never appear in the `ReviewQueue` (they are transient startup-only). Adding the case is low-risk but implementers should confirm no code path enqueues Restoring sessions to the review queue.

3. **`cardPaused` CSS class for Restoring**: Story 5.1 reuses `cardPaused` opacity dim for Restoring sessions. This is sensible. However, `data-paused` is also used for attribute selectors in the CSS. Adding a separate `data-restoring` attribute (as the plan proposes) is the right approach and avoids conflating the two states in CSS selectors.

4. **Proto enum value gap**: `SESSION_STATUS_RESTORING = 9` skips values 5–8. Looking at the existing proto enum, values 1–8 appear to be in use (UNSPECIFIED=0, RUNNING=1, CREATING=2, PAUSED=3, STOPPED=4, ACTIVE=5, NEEDS_APPROVAL=6, IDLE=7, HIBERNATED=8 based on convention). The plan says `SESSION_STATUS_HIBERNATED = 8` and assigns 9 to RESTORING, which is the next available value. Confirm the proto file's actual current highest value before assigning 9 — a collision would cause a silent enum alias.

5. **`SetAnalyticsProvider` existence**: The plan's Architectural Choice 2 flags that `SetAnalyticsProvider` may not exist on `SessionService`. This must be verified before implementing Epic 2 — if the method does not exist, its addition is a required prerequisite, not an optional follow-up.
