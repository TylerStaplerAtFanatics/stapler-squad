# Requirements: Fast Startup with Background Session Restore

## Problem Statement

Stapler Squad currently takes ~23 seconds from process start to HTTP server ready. The UI is completely unavailable during this time. Three synchronous blockers are responsible for most of the delay:

1. **Analytics DB open (~11s)**: `analytics.OpenAnalyticsDB()` opens + migrates analytics.db (512MB SQLite) synchronously inside `BuildRuntimeDeps`, blocking the HTTP server.
2. **External discovery scan (~2s)**: `ExternalDiscovery.Start()` calls `ScanFromUserOptions()` which runs `tmux list-sessions` synchronously in `wireDepsIntoServer`, blocking the HTTP port from binding.
3. **Session restore stagger (~5s)**: The background goroutine sleeps 200ms between each of 25+ session restores (already in background, but delays sessions being available).

When the HTTP server does start early, sessions won't be immediately usable — they will be mid-restoration — so the UI needs a clear visual state for this.

## Goals

1. Reduce HTTP server bind time from ~23s to ~6s by making the two synchronous blockers async.
2. Remove the 200ms stagger entirely (restores are hot-attaches that don't fork processes).
3. Add a "Restoring" visual state to session cards so users understand why sessions aren't interactive yet.
4. Allow navigation to restoring sessions with a loading terminal state.

## Non-Goals

- Parallelizing session restores with a worker pool (out of scope for this PR).
- Changing how the sessions.db or sessions.db migration works (Phase 1 baseline).
- Any changes to the tmux session structure.

## User Stories

### US-1: Fast UI availability
**As a user**, when I restart Stapler Squad, I want the web UI to be accessible within ~6 seconds (down from ~23s) so I can see my sessions immediately.

### US-2: Restoring session state
**As a user**, when a session is mid-restoration, I want to see a greyed-out card with a "Restoring…" label so I understand why it's not yet fully interactive.

**Acceptance criteria:**
- Session cards with status `Restoring` are visually dimmed (reduced opacity)
- A "Restoring…" status label is shown instead of Running/Paused/Idle
- The card is still visible and present in the list (not hidden)

### US-3: Loading terminal for restoring sessions
**As a user**, when I click on a restoring session, I want to see a loading state in the terminal panel rather than an error or blank screen.

**Acceptance criteria:**
- Clicking a restoring session navigates to it
- The terminal panel shows a visual loading indicator
- Once restore completes, the terminal attaches automatically without manual refresh

### US-4: Analytics DB non-blocking
**As a developer/operator**, the analytics database open and migration should not block HTTP server startup.

**Acceptance criteria:**
- HTTP server binds before analytics.db is fully opened
- If analytics DB is unavailable at request time, requests that need it gracefully wait or return a "not yet available" response (non-fatal)
- No existing analytics functionality is broken once the DB is ready

### US-5: External discovery non-blocking
**As a developer/operator**, the initial external tmux session scan should not block HTTP server startup.

**Acceptance criteria:**
- `ExternalDiscovery.Start()` returns immediately; `ScanFromUserOptions()` runs in a goroutine
- External sessions may appear slightly after startup (acceptable)
- No race conditions on the session map

### US-6: No session restore stagger
**As a user**, session restores should complete faster on restart since hot-attaches don't fork new processes.

**Acceptance criteria:**
- The 200ms `time.Sleep` between hot-attach restores is removed
- Cold restores (new sessions) may still have a configurable stagger if needed
- No observable increase in fork pressure on normal restart

## Technical Constraints

- The `Restoring` status must flow through the existing proto/ent/Go/React status pipeline
- Session status changes must be pushed to connected clients via the existing event bus / `WatchSessions` stream
- The analytics ent client must be nil-safe at all call sites — existing code that calls `GetEntClient()` or uses `analyticsClient` must handle the async availability window
- `ScanFromUserOptions()` races are prevented with the existing mutex on `ExternalSessionDiscovery`

## Out of Scope

- Changing the ent ORM schema for sessions.db Phase 1 migration speed
- Parallelizing the actual tmux restore calls
- Any changes to the review queue or controller startup ordering

## Key Files

| File | Change |
|------|--------|
| `session/instance.go` | Add `StatusRestoring` constant |
| `session/ent/schema/` | Potentially add `restoring` as a valid status string |
| `server/dependencies.go` | Move analytics DB open to goroutine; remove stagger |
| `session/external_discovery.go` | Make `ScanFromUserOptions` call in goroutine |
| `server/server.go` | Ensure nil-safe analytics client wiring |
| `web-app/src/` | Add Restoring state to session card + terminal loading state |
| `proto/session/v1/types.proto` | Add `SESSION_STATUS_RESTORING` if status is proto-typed |
