# Feature Research: Fast Startup with Background Session Restore

## 1. Existing Status Rendering in the Frontend

### Status badge / chip

**`StatusBadge.tsx`** (`web-app/src/components/sessions/StatusBadge.tsx`) — used for attention-reason
badges (approval, error, idle, etc.) and terminal-detected statuses ("Ready", "Processing", etc.).
It is **not** the primary lifecycle-status indicator. It uses a `styleVariants` map in
`StatusBadge.css.ts` backed by `vars.statusBadge.*` tokens.

**`SessionCard.tsx`** owns the main lifecycle label. Two functions drive it:

```ts
// Maps SessionStatus enum → CSS class name
getStatusColor(status: SessionStatus): string
  ACTIVE         → statusRunning   (green)
  PAUSED         → statusPausedDistinct (amber-ish)
  LOADING/CREATING → statusLoading (muted/grey)
  STOPPED/HIBERNATED → statusPaused (dim)

// Maps SessionStatus enum → text label
getStatusText(status: SessionStatus): string
  ACTIVE         → "Active"
  PAUSED         → "Paused"
  CREATING       → "Starting…"
  STOPPED        → "Stopped"
  HIBERNATED     → "Hibernated"
```

The card root also applies `cardPaused` (opacity: 0.75) when `isPaused === true`.  There is an
`isCreating` flag but no dedicated `cardCreating` dim — the creating state uses `statusLoading`
color only, no card-level opacity reduction.

There is a `creationSpinner` exported from `SessionCard.css.ts` that is rendered when
`isCreating === true` (line 746–748 in `SessionCard.tsx`).

**`SessionDetailView.tsx`** has a separate `getStatusLabel(status)` switch (line 82) used for
the detail pane header.  It still references the deprecated `SessionStatus.RUNNING` alias.

**Key gap for Restoring**: Neither `getStatusColor`, `getStatusText`, nor `getStatusLabel` handle
a `RESTORING` / `Restoring` value — that case would currently fall through to `statusUnknown` /
`"Unknown"`.

---

## 2. Terminal Loading States

### `TerminalOutput.tsx` — `isLoadingInitialContent` flag

The terminal uses a local boolean state `isLoadingInitialContent` (initialized `true`).  The
loading overlay is rendered while this is `true`:

```tsx
{isVisible !== false && isLoadingInitialContent && (
  <div className={styles.loadingOverlay}>
    <div className={styles.loadingSpinner} />
    <div className={styles.loadingText}>
      {isWaitingForStableSize ? "Initializing terminal..." : "Loading terminal content..."}
    </div>
  </div>
)}
```

`isLoadingInitialContent` is cleared (→ `false`) when:
- First terminal output arrives (line 343)
- Connection is established but no content arrives within timeout (line 396)
- Connection **drops** while still loading (line 701) — reveals disconnect state instead
- Max reconnect attempts reached (line 719)

The overlay is a full-area `position: absolute` panel with `backdropFilter: blur(2px)` and a
CSS `spin` keyframe spinner — exactly the right primitive for a "Restoring" terminal state.

### Paused overlay in `SessionDetailView.tsx`

When `session.status === SessionStatus.PAUSED`, a separate `pausedOverlay` is rendered (line 669)
**above** the TerminalOutput pool.  It shows an icon, title, optional reason, and a Resume button.
The overlay is defined in `SessionDetailView.css.ts` using `position: absolute; inset: 0`.

**For Restoring sessions**, the same overlay pattern can be reused: check
`session.status === SessionStatus.RESTORING` and show a "Restoring…" overlay instead of the
Paused overlay.  Alternatively, pass a prop to `TerminalOutput` to force `isLoadingInitialContent`
to stay `true` until the server signals that restore is complete — matching the existing loading
spinner without needing a second overlay component.

---

## 3. Proto Status Types

### `proto/session/v1/types.proto` — `SessionStatus` enum

Current values (with wire integers):

| Name | Wire | Notes |
|------|------|-------|
| `SESSION_STATUS_UNSPECIFIED` | 0 | |
| `SESSION_STATUS_ACTIVE` | 1 | replaces RUNNING (same wire) |
| `SESSION_STATUS_READY` | 2 | deprecated |
| `SESSION_STATUS_LOADING` | 3 | deprecated |
| `SESSION_STATUS_PAUSED` | 4 | |
| `SESSION_STATUS_NEEDS_APPROVAL` | 5 | deprecated (now sub-status) |
| `SESSION_STATUS_CREATING` | 6 | |
| `SESSION_STATUS_STOPPED` | 7 | terminal state |
| `SESSION_STATUS_HIBERNATED` | 8 | |

`SESSION_STATUS_RESTORING = 9` is the next available wire value.  The enum has
`option allow_alias = true` to support the deprecated aliases.

### Go `session.Status` constants (`session/instance.go`)

```go
const (
    Creating   Status = 0
    Active     Status = 1
    Paused     Status = 2
    Stopped    Status = 3
    Hibernated Status = 4
)
```

`Restoring Status = 5` would be the new constant.

### Adapter (`server/adapters/instance_adapter.go`)

`StatusToProto()` and `StatusStringToProto()` at lines 257–297 are the translation layer.
Both need a `case session.Restoring: → SESSION_STATUS_RESTORING` branch.

### ent ORM schema (`session/ent/schema/session.go`)

The `status` field is stored as `field.Int(...)` (line 34), not a string enum — it stores the
`session.Status` integer value directly.  Adding `Restoring = 5` requires no schema migration
(it's just a new valid integer value).

---

## 4. Event Bus / WatchSessions

### How status updates reach the frontend

1. **`eventBus.Publish(events.NewSessionUpdatedEvent(instance, []string{"status"}))`** — called
   from `session_service.go` wherever status changes (lines 1167, 1214, 1452, 1516, 1561, 2376).
   Also `NewSessionStatusChangedEvent` (line 1443) for the dedicated status-changed event type.

2. **Event types** (`pkg/events/types.go`):
   - `EventSessionUpdated = "session.updated"` — carries full instance snapshot
   - `EventSessionStatusChanged = "session.status_changed"` — carries `OldStatus` + `NewStatus`

3. **`WatchSessions` RPC** (`session_service.go` line 1662) subscribes to the event bus,
   converts events via `convertEventToProto()` in `event_converter.go`, and streams them to
   connected clients.  The initial snapshot comes from `reviewQueuePoller.GetInstances()` then
   streams live events.  Status filter is applied at `adapters.StatusToProto(inst.Status)`.

4. **Frontend** listens to the `WatchSessions` stream and upserts sessions into local state.
   Session cards re-render when the proto `SessionStatus` field changes.

### Where to publish Restoring status

In `server/dependencies.go`, the background goroutine (line ~480) iterates `instances` and calls
`inst.Start(false)`.  To broadcast the Restoring state:

- Before the loop (or per-session before `inst.Start()`): set `inst.Status = session.Restoring`
  and publish `NewSessionUpdatedEvent(inst, []string{"status"})` via the event bus.
- After `inst.Start()` succeeds: the existing `Active` status transition in `inst.Start()` / the
  tmux attach path already publishes a status update, so no extra publish is needed for the
  transition back to Active.

The event bus must be available in the goroutine — it is accessible via `sessionService.EventBus()`
(a getter that returns the `*events.EventBus`; line 540 in `session_service.go`).

---

## Summary of Key Implementation Gaps

| Concern | Current state | What's needed |
|---------|---------------|---------------|
| Proto enum | Next wire value is 9 | Add `SESSION_STATUS_RESTORING = 9` |
| Go constant | No `Restoring` constant | Add `Restoring Status = 5` |
| Adapter | `StatusToProto` has no Restoring case | Add case in `instance_adapter.go` |
| SessionCard label | Falls through to "Unknown" | Add `RESTORING → "Restoring…"` + `statusLoading` CSS class |
| SessionCard card-level dim | `cardPaused` (0.75 opacity) only applied for Paused | Apply `cardPaused` (or new `cardRestoring`) for Restoring too |
| Terminal overlay | `isLoadingInitialContent` covers it if kept `true` | Or reuse `pausedOverlay` pattern with Restoring variant |
| Event publish | No publish in bg restore loop | Add `Publish(NewSessionUpdatedEvent(inst, ["status"]))` before each `inst.Start()` |
