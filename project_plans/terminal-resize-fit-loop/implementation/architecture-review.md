# Architecture Review: terminal-resize-fit-loop
**Date**: 2026-07-24
**Verdict**: CONCERNS

## Constitution Violations
- N/A — no `docs/adr/ADR-000-architecture-constitution.md` exists in this repo.

## Summary

The plan is unusually well-grounded: every cited line number in `XtermTerminal.tsx`,
`useTerminalFlowControl.ts`, `useTerminalStream.ts`, and `TerminalOutput.tsx` was checked
against the actual current source and matches exactly (verified directly, not taken on
faith). The three-call-site claim for `resize()` (327/351/510 in `TerminalOutput.tsx`) is
confirmed exhaustive by repo-wide grep — `TerminalOutput.tsx` is the sole consumer of
`useTerminalStream`, and the only other `resize(` hits in the repo are unrelated calls to
xterm.js's own `Terminal.resize()` inside `DeltaApplicator.ts`/`StateApplicator.ts`. The
`force?: boolean` addition is a genuinely additive, non-breaking signature change. The
approach (decoupled sampler, Reading A, hand-built dead-band + `@xterm/addon-canvas`) matches
`research/build-vs-buy.md`'s recommendations point-for-point, including the "wire
`onContextLoss` AND the bespoke mismatch heuristic side by side" guidance (Stories 3.1 and
3.2 both feed the same `triggerCanvasFallback()` latch, not either/or).

One structural gap (sampler give-up not explicitly resetting its own "running" state) is
severe enough to flag as a blocker despite the plan's overall quality, because it is exactly
the class of illegal-state bug that's cheap to close off now (one sentence of task spec + one
test assertion) and expensive to discover later (a silent, permanent loss of resize
functionality with no error, only 1-2 log lines many minutes/hours earlier).

## Blockers

- [ ] **Task 1.2.2 (Story 1.2, Epic 1)** — The `MAX_SAMPLES` give-up branch is specified only
  as "increments `sampleCount`, gives up with a `console.warn` at `MAX_SAMPLES`", in contrast
  to the other two branches which explicitly say "calls `stopSampler()`". Read literally, this
  omission means give-up may never reset `samplerActive = false` / `pendingProposedDims = null`
  / `sampleCount = 0`. Since `startSamplerIfNeeded()` is expected to no-op when
  `samplerActive` is already `true` (that's the whole point of "decoupled, not reset by every
  RO delivery" — ADR-002 Decision 1), a give-up event that doesn't clear `samplerActive` makes
  the sampler permanently inert for the remaining lifetime of that `XtermTerminal` mount:
  every subsequent `ResizeObserver` delivery — including a completely legitimate, one-shot
  window resize weeks later — would silently fail to ever call `fit()` again, with no error,
  only a single `console.warn` logged once at the original give-up. This is a **worse**
  end-state than the bug being fixed (that one froze the CPU/UI; this one silently and
  permanently breaks resizing for the session, indistinguishable from a hang except via
  console archaeology). ADR-002's own GWT for sustained oscillation only asserts "the sampler
  stops (no further `setTimeout` is pending)" — it does not assert that a *later* qualifying
  resize successfully restarts the sampler and converges, so this gap would not be caught by
  the pinned regression test as currently scoped.
  **Remediation**: Amend Task 1.2.2 (and ADR-002's Decision §3 / GWT) to state explicitly that
  give-up calls `stopSampler()` (same reset as the other two branches: `samplerActive = false`,
  `pendingProposedDims = null`, `sampleCount = 0`, clear any pending `sampleTimeout`) after
  logging the warning. Add a regression test to Story 4.1 (extending Task 4.1.4): after a
  simulated give-up (50 ticks of non-repeating candidates), fire one more `ResizeObserver`
  delivery whose `proposeDimensions()` converges cleanly on the next two ticks, and assert
  `fit()` **is** called — proving the sampler is truly reusable, not a one-shot latch.

## Concerns

- [ ] **Task 2.1.1 (Story 2.1, Epic 2) vs. §2 Domain Glossary** — The glossary states
  `ResizeDimensions` (defined in `XtermTerminal.tsx`) covers "internal state" including
  `LastSentDimensions` in `useTerminalFlowControl.ts`, but Task 2.1.1's literal instruction is
  `const lastSentDimsRef = useRef<{ cols: number; rows: number } | null>(null);` — an inline
  anonymous type, not an import of `ResizeDimensions`. This is precisely the "declared once,
  bypassed by raw pairs elsewhere" anti-pattern the plan otherwise avoids (per its own
  reasoning in §3 Pattern Selection). It's not a compile-time defect (TS structural typing
  makes the two shapes interchangeable), but it means the plan doesn't actually deliver the
  "one value type, one source of truth" benefit it claims for the type it introduces, and
  perpetuates the pattern already visible elsewhere in this file family (`TerminalOutput.tsx`'s
  `lastResizeRef`, `dimensionSyncRef` in `useTerminalFlowControl.ts` — both separately-typed
  `{cols, rows}` shapes). There's also a layering smell in the fix-as-literally-glossed: since
  `ResizeDimensions` is defined in a *component* file (`XtermTerminal.tsx`), having
  `useTerminalFlowControl.ts` (a hook `XtermTerminal.tsx` doesn't even import) import a type
  from it would be a backward dependency (hook → leaf component).
  **Remediation**: Move `ResizeDimensions` (and `ShouldScheduleFitResult` can stay local since
  it's `shouldScheduleFit`-specific) to a small shared module, e.g.
  `web-app/src/lib/terminal/types.ts`, imported by both `XtermTerminal.tsx` and
  `useTerminalFlowControl.ts`. Update Task 2.1.1 to `useRef<ResizeDimensions | null>(null)`.

- [ ] **Task 3.2.2 (Story 3.2, Epic 3)** — `checkWebglCellMismatch(terminal: Terminal,
  containerEl: HTMLElement): boolean` takes live `Terminal`/`HTMLElement` references and
  performs its own private-API extraction (`(terminal as any)._core?._renderService?.dimensions`)
  and DOM measurement (`containerEl.getBoundingClientRect()`) *inside* the function, rather
  than being parameterized on the already-extracted primitives
  (`actualPixelsPerCol: number, cellWidthPx: number`). Unlike `shouldScheduleFit` — which the
  plan deliberately built as a pure function "so tests can drive it without mounting a
  component" — `checkWebglCellMismatch` mixes extraction (reaching into private xterm.js
  internals and the live DOM) with the actual decision logic (the `Number.isFinite` guard +
  tolerance comparison AC5 requires). Task 4.1.5 needs to test this function's mismatch/latch
  behavior, but as specified it can only be exercised by mocking `Terminal`'s private
  `_core._renderService.dimensions` shape and a `getBoundingClientRect`-returning DOM element,
  not by passing in plain numbers for the boundary cases (e.g., the `Infinity` case from
  `terminal.cols === 0`) that AC5 is actually about.
  **Remediation**: Split into two functions — an impure one-liner that extracts
  `{ actualPixelsPerCol, cellWidthPx }` from `terminal`/`containerEl`, and a pure
  `isCellMismatch(actualPixelsPerCol: number, cellWidthPx: number): boolean` (or fold the
  `Number.isFinite` guard into a small shared `isFiniteDimensions`/`isFiniteNumber` helper)
  that Task 4.1.5's tests call directly with numeric fixtures, mirroring the isolation already
  achieved for `shouldScheduleFit`.

- [ ] **Task 3.2.3 (Story 3.2, Epic 3)** — The post-fallback `fit()` call is specified as
  "guarding that `fit()` call with the sampler's existing `shouldScheduleFit`-style
  `Number.isFinite` checks on the resulting `proposeDimensions()` before applying (reuse
  `checkWebglCellMismatch`'s guard pattern...)" — this asks the implementer to improvise a
  third, unnamed guard rather than pointing at one canonical function. This is the "scattered
  ad hoc guard, not a single boundary" pattern the AC5 wording is trying to avoid. Given the
  Concern above already proposes extracting a pure `isFiniteDimensions`-style helper out of
  `checkWebglCellMismatch`, the same helper should be the one thing Task 3.2.3 calls, not a
  restated inline description.
  **Remediation**: Name the concrete guard function once (e.g.
  `isFiniteResizeDimensions(d): d is ResizeDimensions`, colocated with `ResizeDimensions` per
  the first Concern's remediation) and have both Task 3.2.2/3.2.3's mismatch check and the
  post-fallback `fit()` guard call it explicitly, instead of "reuse the pattern."

- [ ] **Story 1.2 / Story 3.2 testability (Epic 1 & 3, Lens 1 Q4)** — `shouldScheduleFit()` is
  correctly isolated (pure, exported, no closure/ref dependency — confirmed against ADR-002's
  code sample: all three inputs are plain parameters). However, the *sampler orchestration*
  itself (`startSamplerIfNeeded()`, `sampleTick()`, `stopSampler()`) is specified as unexported
  `let`-closures living inside the mount effect (Task 1.2.2), so the actual tick-counting,
  `MAX_SAMPLES` boundary, and give-up/reset logic — exactly where the Blocker above lives — can
  only be exercised via full component-mount + mocked-`ResizeObserver` + fake-timer tests
  (Task 4.1.4), which are slower and more brittle than a direct unit test would be. The Test
  Strategy Summary table itself concedes this ("Component/sampler integration... Mocked
  `ResizeObserver`, `jest.advanceTimersByTime()`"), so the plan isn't hiding the tradeoff — but
  given how easy the give-up-state bug above is to introduce and how only an
  integration-level test would catch it, this is worth reconsidering.
  **Remediation**: Consider factoring the sampler into a small exported factory (e.g.
  `createResizeSampler({ proposeDimensions, getApplied, onFit, onGiveUp })` returning
  `{ start, tick, stop }`) that the mount effect wires up with real refs/callbacks. This would
  let Task 4.1.4's assertions run as fast, direct unit tests against the factory instead of
  requiring a full component mount for the sampler's own state-machine correctness — mounting
  would then only be needed to prove the wiring, not the tick logic itself. Not required to
  ship the fix, but meaningfully reduces the risk class the Blocker above is an instance of.

## Nitpicks

- `webglFallbackTriggered`'s one-directional-latch property (AC5, ADR-001) is enforced only by
  convention — a plain closure `let` that happens to be written from exactly one place
  (`triggerCanvasFallback()`) in the current task list, not by any type-system or
  control-flow guarantee. This matches the file's existing convention for similar state
  (`resizeCount`, `lastContainerSize` are equally convention-scoped), so it's not a new risk
  class being introduced, just worth a one-line comment at the declaration
  (`// monotonic: set only inside triggerCanvasFallback(); never assign directly elsewhere`)
  so a future edit doesn't casually add a second write site.
- AC5's literal wording ("guards against `proposeDimensions()` returning `Infinity`") doesn't
  quite match where the `Number.isFinite` guard actually needs to live: per the plan's own
  `ProposedDimensions` glossary entry, `FitAddon.proposeDimensions()` never itself returns
  `Infinity` (only `undefined` or integer-valued dims); the real `Infinity` risk is in
  `checkWebglCellMismatch`'s `containerEl.getBoundingClientRect().width / terminal.cols`
  division when `cols === 0`. The plan resolves this correctly (Task 3.2.2's GWT nails the
  actual failure mode), but a one-line note cross-referencing "AC5 says proposeDimensions(),
  the actual guard lives in the mismatch-tracker's division" would close the traceability gap
  for a future reader comparing the ticket text to the code.
