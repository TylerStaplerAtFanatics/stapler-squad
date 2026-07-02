# BUG-025: Escape Analytics Reads as Broken — session_id Mismatch Hides All Data, Mangle Detection Never Wired [SEVERITY: High]

**Status**: ✅ FIXED (2026-07-01)
**Discovered**: 2026-07-01
**Fixed**: 2026-07-01 — `pkg/analytics/escape_code_parser.go`, `session/response_stream.go`,
`session/claude_controller.go`, `server/services/connectrpc_websocket.go`, `config/config.go`
**Reported by**: User — "Escape analytics feature seems broken, not capturing any escape codes,
so we don't know which codes need support, and can't tell if things are getting mangled."

## Plan soundness check (requested alongside this bug)

`project_plans/terminal-analytics/implementation/plan.md` is sound: Story 4.1 explicitly calls
for `instance.GetStableID()` (not the tmux session name) when constructing the escape parser,
and Epic 5 explicitly designs `RecordStage1`/`CheckStage2` as distinct calls plus a Stage 2
base-offset scheme in ADR-TBA-4. **The plan was not followed during implementation on both
points**, and the two regression tests the plan called for for exactly this class of gap
(`E2-S3-T1` batch writer test, `E5-S2-T1` Stage1/Stage2 integration test) were never written —
which is why these shipped silently.

## Problem Description

The feature is **not** failing to capture data. Querying the live analytics DB for this
worktree's own session traffic:

```
$ sqlite3 analytics.db "SELECT COUNT(*) FROM escape_events;"
185008
$ sqlite3 analytics.db "SELECT stage, COUNT(*) FROM escape_events GROUP BY stage;"
pty_read|151320
transport|33688
$ sqlite3 analytics.db "SELECT mangled, COUNT(*) FROM escape_events GROUP BY mangled;"
0|185008
```

Two distinct bugs together produce "seems broken":

### Root Cause 1: `session_id` is the tmux session name, not the stable session UUID

`session/response_stream.go` `newEscapeParserForSession(sessionName)` is constructed with
`cc.sessionName` (`session/claude_controller.go:205`, sourced from `instance.GetTitle()`).
Sample `session_id` values actually stored: `dotfiles`, `fanapp-backend`,
`pr-1220-actions-build-extraction` — human-readable titles, not UUIDs.

The web UI's session selector (`EscapeAnalyticsPage.tsx`) populates its dropdown from
`session.id` — the stable UUID returned by `GetStableID()`, the identifier convention used
everywhere else in the app (`instance_vnc.go`, `instance_cdp.go`, `notification_service.go`,
etc.). `QueryEscapeAnalytics`/`GetEscapeAnalyticsSummary`
(`server/services/analytics_escape_service.go`) filter by whatever `session_id` the UI sends,
with no translation.

Result: the UI queries by stable UUID, the rows are keyed by tmux title. For any session using
the modern UUID scheme these values never match, so the analytics page returns **zero rows for
every session**, even though ~185K rows exist. From the user's vantage point (the only place
they'd look) the feature appears to capture nothing.

### Root Cause 2: Mangle detection (Epic 5) is entirely unwired — `mangled` is always `false`

`grep -rn "SetCorrelator\(" --include="*.go" .` outside of tests returns nothing.
`MangleCorrelator` (`pkg/analytics/mangle_correlator.go`) is fully implemented and unit-tested
in isolation, but no production code ever constructs one or attaches it to an
`EscapeCodeParser`. Confirmed empirically: 0 of 185,008 captured rows have `mangled=1`, across
5 days and 11 heterogeneous sessions — statistically conclusive that detection never fires, not
that nothing was ever mangled.

Two additional bugs sit underneath this one, found while tracing why wiring the correlator
alone would still not work:

1. **`emitEventWithStageAndSeq` always calls `RecordStage1`, never `CheckStage2`**
   (`pkg/analytics/escape_code_parser.go`). The `stage` parameter (`StagePTYRead` vs.
   `StageTransport`) is accepted but never inspected before touching the correlator, so even a
   wired correlator would treat every Stage 2 observation as a fresh Stage 1 recording instead
   of checking it against the matching Stage 1 entry.

2. **Stage 2 `session_seq` is computed as the buffer's current total, not the coalesced
   frame's start offset** (`server/services/connectrpc_websocket.go:751`):
   `escapeParser.ParseStage2(buf, instance.GetTotalBytesWritten())`. `GetTotalBytesWritten()`
   is read *after* the coalescing loop finishes draining `updateChan`, by which point the
   circular buffer already reflects every byte in `buf` (`response_stream.go`'s `streamLoop`
   writes to the buffer, then broadcasts — in that order, on a single goroutine — so the
   broadcast a subscriber receives always trails the buffer write for the same chunk). Passing
   the *current* total as the *start* offset means every sequence's reconstructed
   `session_seq` (`sessionSeq + code.StartOffset`) is off by `len(buf)`, so `CheckStage2` would
   almost never find the Stage 1 entry it should correlate against, even with (1) fixed. Fix:
   `instance.GetTotalBytesWritten() - int64(len(buf))`, mirroring how Stage 1 captures its
   offset *before* writing (`response_stream.go` line ~274).

### Related: data race on parser lifetime counters

`p.totalSequences` / `p.totalMangled` in `EscapeCodeParser` are plain `int64` fields incremented
without synchronization in `emitEventWithStageAndSeq`. Stage 1 (`Parse`, called from the PTY
read goroutine) and Stage 2 (`ParseStage2`, called from the WebSocket output goroutine) both
reach this method concurrently on the *same* parser instance for a session — an unguarded
concurrent read-modify-write. `go test -race` did not previously catch this only because Stage
2 never emitted anything with a correlator attached to correctness-check against (no path
exercised both stages against the same parser in a test). Wiring the correlator makes this race
newly reachable in tests; fixed alongside as part of the same change.

### Related, lower severity: `DefaultConfig()` doesn't mirror `LoadConfigFromPath`'s escape defaults

`config/config.go` has an explicit comment above the `SessionDefaults` init in `DefaultConfig()`:
"`LoadConfigFromPath` applies the same guards after JSON decode; `DefaultConfig` must mirror
them so the two code paths are equivalent." The escape analytics fields
(`EscapeAnalyticsCaptureLevel`, `EscapeAnalyticsSamplingRate`, `EscapeAnalyticsMaxRowsPerSession`,
`EscapeAnalyticsRetentionDays`) were added to `LoadConfigFromPath`'s defaulting block but never
mirrored into `DefaultConfig()`. Currently masked by a second, independent defensive default in
`response_stream.go`'s `loadAnalyticsConfig()`, so this hasn't caused a visible symptom — but on
a machine's very first boot (`config.json` doesn't exist yet), `cfg.EscapeAnalyticsMaxRowsPerSession`
passed into `NewEscapeEventBatchWriter` at `server.go:534` would be `0`, which disables the
per-session row cap entirely (`if w.maxRowsPerSession > 0` guard in
`escape_event_batch_writer.go`) instead of applying the intended 10,000-row default.

## Files Affected

- `session/claude_controller.go` — `InstanceContext` interface, `Start()`
- `session/claude_controller_test.go` — `mockInstance`
- `session/response_stream.go` — `newEscapeParserForSession`, `Start()`
- `pkg/analytics/escape_code_parser.go` — `SetSessionID`, `emitEventWithStageAndSeq`,
  counter fields
- `server/services/connectrpc_websocket.go` — Stage 2 tap call site
- `config/config.go` — `DefaultConfig()`

## Fix Approach

See commit for full diff. Summary:

1. Add `GetStableID() string` to `InstanceContext`; thread it into the escape parser via a new
   `ResponseStream.SetStableSessionID` / `EscapeCodeParser.SetSessionID`, called once right
   after `NewResponseStream` in `ClaudeController.Start()`. `cc.sessionName` (tmux name) is left
   untouched everywhere else it's used (PTY naming, command queue/history persistence, rate
   limiting, idle detection) — this is scoped to the escape-analytics session key only.
2. Construct a `MangleCorrelator` per parser in `newEscapeParserForSession` and start its
   eviction goroutine from `ResponseStream.Start(ctx)`.
3. Branch `emitEventWithStageAndSeq` on `stage`: `StagePTYRead` → `RecordStage1`,
   `StageTransport` → `CheckStage2` (sets `record.Mangled`/`record.MangleType`).
4. Fix the Stage 2 base-offset arithmetic in `connectrpc_websocket.go`.
5. Convert `totalSequences`/`totalMangled` to `atomic.Int64`.
6. Mirror the escape analytics defaults into `DefaultConfig()`.

## Verification

- `pkg/analytics`: new test asserting `SetSessionID` changes emitted `EscapeEventRecord.SessionID`.
- `pkg/analytics`: new test driving a Stage 1 `Parse` + a mutated Stage 2 `ParseStage2` through
  the same parser with a correlator attached, asserting the second emitted record has
  `Mangled=true`.
- `config`: new test asserting `DefaultConfig()` and `LoadConfigFromPath` (missing-file case)
  produce identical escape analytics defaults.
- `go test ./pkg/analytics/... ./config/... ./session/... -race` green.
- The `connectrpc_websocket.go` arithmetic fix has no dedicated new test — the existing test
  file has no harness for driving `streamViaControlMode`'s coalescing goroutine, and building
  one is out of scope for this fix. Verified by manual trace (documented above) instead.
  Flagging as a follow-up: an integration test matching the plan's AC-3 ("stripped/mutated OSC
  sequence flows through Stage 1+2 into a `mangled=true` SQLite row") still doesn't exist.

## Related

- `project_plans/terminal-analytics/` — original feature plan and research
- Follow-up recommended: AC-3 end-to-end integration test (Stage 1 → Stage 2 → SQLite,
  `mangled=true`) through the real WebSocket streaming path, not just the correlator unit level.

## Resolution

All fixes applied and verified:

1. `InstanceContext.GetStableID()` + `ResponseStream.SetStableSessionID` +
   `EscapeCodeParser.SetSessionID` — escape_event rows now tagged with the stable session UUID.
   `cc.sessionName` (tmux name) is unchanged everywhere else.
2. `MangleCorrelator` constructed per-parser in `newEscapeParserForSession`, eviction goroutine
   started from `ResponseStream.Start`.
3. `emitEventWithStageAndSeq` now branches `RecordStage1` (Stage 1) vs. `CheckStage2` (Stage 2)
   instead of always calling `RecordStage1`.
4. `connectrpc_websocket.go` Stage 2 tap now passes `GetTotalBytesWritten() - len(buf)` as the
   base offset instead of the buffer's current (post-write) total.
5. `totalSequences`/`totalMangled` converted to `atomic.Int64` (both Stage 1 and Stage 2
   goroutines write through the same parser instance).
6. `DefaultConfig()` now mirrors `LoadConfigFromPath`'s escape analytics defaults.

Tests added: `TestSetSessionIDOverridesConstructorSessionID`,
`TestMangleCorrelationStage1ThenStage2Match`, `TestMangleCorrelationStage1ThenStage2Mutated`,
`TestMangleCorrelationTotalsAreConcurrencySafe` (pkg/analytics);
`TestResponseStream_SetStableSessionID` (session, exercises the real production wiring path);
`TestDefaultConfigMirrorsEscapeAnalyticsDefaults` (config).

Verified: `go build ./...`, `go vet`, `golangci-lint run` (0 issues), and
`go test ./pkg/analytics/... ./config/... ./server/analytics/... ./session/... ./server/services/... -race`
all green.

Known residual gap (documented, not blocking): the `connectrpc_websocket.go` arithmetic fix has
no dedicated automated test — the existing test file has no harness for driving
`streamViaControlMode`'s coalescing goroutine. Verified by manual trace only. A true end-to-end
integration test (Stage 1 → Stage 2 → SQLite, `mangled=true`, matching the original plan's AC-3)
remains a good follow-up.
