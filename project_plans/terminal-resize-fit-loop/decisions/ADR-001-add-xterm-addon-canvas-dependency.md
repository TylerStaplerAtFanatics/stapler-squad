# ADR-001: Add `@xterm/addon-canvas` as an explicit new dependency for the WebGL fallback

**Status**: Accepted
**Date**: 2026-07-24
**Project**: terminal-resize-fit-loop

## Context

Requirements AC5 (verbatim): "The WebGL actual-vs-expected pixels-per-column discrepancy is
corrected or mitigated so `fit()` converges under WebGL rendering: a sustained mismatch beyond
a defined tolerance triggers a one-directional fallback to the **canvas renderer**..."

The requirements' Constraints section also states "No new dependencies expected" for this
change, and this is flagged explicitly in requirements.md as a tension needing a plan-level
decision.

Investigation (Phase 2 build-vs-buy + stack research, confirmed directly against
`web-app/package.json` and `web-app/package-lock.json` in this repo):

- `@xterm/addon-webgl@^0.18.0` is installed and used in
  `web-app/src/components/sessions/XtermTerminal.tsx` (lines 7, 150-155).
- `@xterm/addon-canvas` is **not** installed anywhere in this repo (`node_modules`,
  `package.json` dependencies, and `package-lock.json` all confirmed absent).
- xterm.js core has no automatic WebGL→canvas fallback. When a `WebglAddon` is disposed
  (e.g., via `onContextLoss` or manual `dispose()`), xterm.js core falls back to its
  built-in **DOM** renderer, not canvas. A real canvas-based fallback requires loading
  `@xterm/addon-canvas`'s `CanvasAddon` explicitly — it is a distinct, separately-installed
  addon, not a built-in mode.
- `@xterm/addon-canvas@0.7.0`'s peer dependency is `@xterm/xterm: ^5.0.0`. The installed
  `@xterm/xterm@^5.5.0` satisfies this range — confirmed compatible, no version conflict.
- A one-time test-harness precedent exists at
  `web-app/src/app/test/terminal-stress/page.tsx` (lines ~106-125) that disposes
  `WebglAddon` on context loss and labels the resulting state `'canvas'` in its UI —
  but that page does **not** load `CanvasAddon`; it mislabels the DOM-renderer fallback as
  "canvas." This is existing, pre-fix terminology drift in test-only code, not evidence that
  a canvas renderer is already wired up anywhere.

## Decision

**Add `@xterm/addon-canvas@^0.7.0` as a new production dependency** in `web-app/package.json`,
and load a real `CanvasAddon` as the fallback target when the WebGL mismatch-tracker or
`onContextLoss` handler trips.

## Rationale

1. **Literal requirement compliance.** AC5's text says "canvas renderer," not "DOM renderer"
   or "non-WebGL renderer." Treating "canvas" as loose terminology for the DOM fallback would
   silently under-deliver against a criterion that was written with a specific rendering tier
   in mind (the existing docstring in `XtermTerminal.tsx` lines 71-73 already documents a
   3-tier mental model: "Canvas-based rendering (10-100x faster than DOM)" / "WebGL
   acceleration (2x faster than canvas)" — implying DOM < Canvas < WebGL was always the
   intended hierarchy, and "falling back" from WebGL was always meant to land on Canvas, not
   skip past it to DOM).
2. **Real, not cosmetic, performance value.** A canvas fallback is a meaningfully faster
   perf tier than the DOM renderer for a terminal actively receiving high-frequency output —
   exactly the kind of session this bug report describes (multiple terminals, heavy scrollback).
   Falling all the way back to DOM would recover correctness (no more sizing feedback loop
   from mismeasurement) but needlessly sacrifice throughput that Canvas would have preserved.
3. **Peer-dependency compatibility is confirmed, low-risk.** `^5.0.0` peer range covers the
   installed `@xterm/xterm@^5.5.0` with no version pin conflicts against the other installed
   `@xterm/addon-*` packages (fit `^0.10.0`, search `^0.15.0`, web-links `^0.11.0`, webgl
   `^0.18.0` — all published for the `@xterm/xterm@5.x` line).
4. **One-line, low-blast-radius addition.** This is a single new leaf dependency with no
   transitive footprint of its own beyond the xterm.js addon family already in the tree — not
   a new class of tooling, build step, or architectural surface. The "no new dependencies
   expected" constraint reads as a *default expectation* set before this specific tension was
   discovered during Phase 2 research, not as an absolute prohibition; the constraint's intent
   (avoid unnecessary dependency sprawl for a bug fix) is honored by picking the smallest
   possible addition that satisfies the literal, deliberately-worded acceptance criterion.

## Alternatives Considered

- **Redefine "canvas" as loose terminology for the DOM fallback (no new dependency).**
  Rejected: avoids the dependency but does not literally satisfy AC5's wording, permanently
  bakes the `terminal-stress/page.tsx` mislabeling into production code/comments, and throws
  away a real, cheap performance tier for exactly the workload (multiple concurrent
  high-throughput terminals) this bug report is about.
- **Skip a rendering-tier fallback entirely; just stop calling `fit()` when mismatch is
  detected.** Rejected: does not address AC5's literal requirement, and leaves the terminal
  running under a renderer known to be measuring itself inconsistently — the actual defect,
  not just its downstream symptom (the resize loop), would remain unaddressed.

## Consequences

- `web-app/package.json` and `web-app/package-lock.json` gain one new dependency:
  `@xterm/addon-canvas@^0.7.0`.
- `XtermTerminal.tsx` gains an import of `CanvasAddon` and a code path that loads it only when
  the WebGL fallback trips (not loaded eagerly on every session, keeping the common-case
  bundle/runtime cost limited to the WebGL happy path already in place).
- Future maintainers reading "canvas renderer" in code comments/logs get a renderer that is
  actually Canvas, not a mislabeled DOM fallback — resolves the terminology drift from
  `terminal-stress/page.tsx` rather than propagating it.
