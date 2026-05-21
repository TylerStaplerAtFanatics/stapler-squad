---
description: Connect to the running stapler-squad pprof endpoint, interpret the profiles, identify the top performance bottlenecks, propose concrete improvements ranked by impact, and codify each fix with a test or lint rule so regressions cannot silently reappear.
prompt: |
  # perf:make-it-faster — Profiling → Proposal → Enforcement

  You are performing a live performance audit of the running stapler-squad process.
  Work through four phases in order and produce concrete, actionable output.

  ---

  ## Phase 0 — Connect and Capture

  The server must be running with `--profile` to expose pprof. Check first:

  ```bash
  curl -s http://localhost:6060/debug/pprof/ | head -5
  ```

  If it returns HTML, capture all four profiles:

  ```bash
  # Goroutine states (qualitative: what are all goroutines doing right now?)
  curl -s "http://localhost:6060/debug/pprof/goroutine?debug=2" > /tmp/goroutines.txt

  # Mutex contention (quantitative: which mutexes are hot?)
  curl -s "http://localhost:6060/debug/pprof/mutex?debug=1" > /tmp/mutex.txt

  # Scheduler blocking (quantitative: which goroutines block the scheduler longest?)
  curl -s "http://localhost:6060/debug/pprof/block?debug=1" > /tmp/block.txt

  # In-use heap allocations (what is alive right now?)
  curl -s "http://localhost:6060/debug/pprof/heap?debug=1" > /tmp/heap.txt

  # Allocation rate (what allocates most often, even if short-lived?)
  curl -s "http://localhost:6060/debug/pprof/allocs?debug=1" > /tmp/allocs.txt
  ```

  If the server is not running with `--profile`, restart it:
  ```bash
  make restart-web PROFILE_FLAGS="--profile"
  ```

  ---

  ## Phase 1 — Read the Profiles

  ### How to interpret each profile

  **mutex** — the most actionable for latency.
  - Format: `<cycles> <count> @ <addrs>`
  - `cycles` = total CPU cycles spent waiting on this lock (higher → more contention)
  - `count` = how many times goroutines waited (high count at low cycles = many short waits; low count at high cycles = long waits)
  - Look for: your own packages (`github.com/tstapler/stapler-squad`) in the stack, especially inside loops or hot-path handlers.
  - Red flag: `log.(*Logger).output` in the stack — stdlib log holds a mutex per write; any hot-path debug `Printf` call serializes every goroutine that hits it.

  **block** — scheduler delays from channel/select operations.
  - Same format as mutex.
  - High `cycles` on `runtime.selectgo` inside event loops is normal (timer fires). Abnormally high `count` (>10K) on a per-connection goroutine is a sign of excessive goroutine wake-ups.
  - Red flag: >10K blocks on a `streamVia*` or `handleClient` goroutine with a short lifetime.

  **allocs** — allocation rate (lifetime may be short).
  - Format: `<in-use-count>: <in-use-bytes> [<total-count>: <total-bytes>]`
  - Second pair `[total-count: total-bytes]` is the rate metric — even if objects are freed quickly, allocating millions of them adds GC pressure.
  - Red flag: proto `Marshal`/`Unmarshal` allocating on every streaming frame, or ORM queries returning full rows when only one field is needed.

  **heap** — live allocations at snapshot time.
  - Same format; first pair is the rate metric here.
  - Red flag: compression encoder `blockEnc.init` without a `sync.Pool` — should show pool-resident objects, not fresh allocations.

  **goroutines** — qualitative health check.
  - Count goroutines by state with:
    ```bash
    grep "^goroutine" /tmp/goroutines.txt | sed 's/goroutine [0-9]* //' | sort | uniq -c | sort -rn
    ```
  - Normal states: `[select]`, `[chan receive]`, `[IO wait]`
  - Red flags: many goroutines in `[semacquire]` (lock contention) or `[sleep, X minutes]` (goroutine leak)

  ---

  ## Phase 2 — Rank Bottlenecks

  Extract the top-5 stacks from mutex and block profiles, filtering to stapler-squad frames:

  ```bash
  grep -E "^[0-9]+ [0-9]+ @|#.*github.com/tstapler" /tmp/mutex.txt | head -60
  grep -E "^[0-9]+ [0-9]+ @|#.*github.com/tstapler" /tmp/block.txt | head -60
  grep -E "^[0-9]+: [0-9]+ \[[0-9]+:|#.*github.com/tstapler" /tmp/allocs.txt | head -60
  ```

  Fill in this table (sort by cycles × count for mutex; by count for block):

  | Rank | Profile | Location | cycles | count | Root cause hypothesis |
  |------|---------|----------|--------|-------|-----------------------|
  | 1 | mutex | file:line | ... | ... | ... |
  | 2 | block | file:line | ... | ... | ... |
  | … | … | … | … | … | … |

  ### Known recurring hotspots in this codebase (as of 2026-05-02 profiling session)

  | Issue | Location | Profile signal | Impact |
  |-------|----------|----------------|--------|
  | `log.DebugLog.Printf` in hot poll loop | `session/instance_status.go:78` (`GetStatus`) | mutex: 2.2B cycles, 5094 events | Every review queue tick serializes on log mutex |
  | `log.DebugLog.Printf` in content cache hot path | `session/review_queue_poller.go:557,574,581` | mutex: 1.4B cycles, 2607 events | Same pattern — no `DebugLog != nil` guard |
  | `log.DebugLog.Printf` on every `%output` event | `session/tmux/control_mode.go:331` | mutex: 2.7B cycles, 94 events | tmux output path — fires on every terminal byte |
  | `log.DebugLog.Printf` inside streaming send loop | `server/services/connectrpc_websocket.go:629` | block: 23T cycles, 26437 events | Per-frame log call in WebSocket stream goroutine |
  | `EntRepository.Get` before every field update | `session/ent_repository.go:622` via `storage.go:285` | allocs: full row read per update | Should be a direct `UPDATE … WHERE id=?` |

  ---

  ## Phase 3 — Propose Improvements

  For each bottleneck, propose a concrete fix at the **earliest achievable enforcement level**:

  ```
  1. Compile time  → type change, interface constraint
  2. Lint rule     → custom golangci-lint rule, existing staticcheck rule
  3. Benchmark     → must regress detectably if the fix is reverted
  4. Unit test     → asserts correct behavior before/after
  5. CLAUDE.md     → only when 1–4 are genuinely unreachable
  ```

  ### Template for each proposal

  ```
  ### [PerfFix-N] Short title

  **Profile signal**: mutex / block / allocs — file:line — X cycles, Y events
  **Root cause**: one sentence
  **Fix**: what to change and where
  **Enforcement**: lint rule name / benchmark name / test name that would have caught it
  **Estimated impact**: low / medium / high — why
  ```

  ---

  ## Phase 4 — Codify (Reflect & Fix)

  Apply the Reflect & Fix framework to every fix you propose.

  For **mutex contention from hot-path logging**:
  - Category: **Semantic/Intent** — the debug log is syntactically valid but semantically wrong in a tight loop
  - Enforcement: lint rule that flags `log.DebugLog.Printf` calls not guarded by `if log.DebugLog != nil` inside functions whose names match `*poll*`, `*check*`, `*stream*`, `*handle*`
  - Write the rule in `buildSrc/` or as a golangci-lint custom check; add a test that fires on the bad pattern and is silent on the guarded form
  - Add to `.golangci.yml` under `custom-gcl` or `revive` rules

  For **allocation-per-frame in streaming paths**:
  - Category: **Integration Gap** — proto allocation per frame is correct in isolation but adds up at stream throughput
  - Enforcement: benchmark `BenchmarkStreamViaControlMode` that asserts `allocs/op == 0` for the hot path (use `testing.AllocsPerRun`)
  - Must fail before the fix (pooled protos not yet introduced) and pass after

  For **read-before-write in ORM updates**:
  - Category: **API Contract Gap** — the update method's interface doesn't signal that it does a read first
  - Enforcement: integration test `TestUpdateFieldInRepo_UsesDirectUpdate` that counts SQL statements and asserts `SELECT` count == 0 for a field update

  ### Verification table

  | Fix | Enforcement | Pre-fix behaviour | Verdict |
  |----|------------|------------------|---------|
  | Remove hot-path `DebugLog.Printf` | lint rule | fires on pre-fix code ✓ | catches it |
  | Pool proto in stream loop | `BenchmarkStream_AllocsPerOp` | allocs > 0 ✓ | catches it |
  | Direct SQL update | `TestUpdateFieldInRepo_NoSelect` | sees SELECT ✓ | catches it |

  ---

  ## Output Format

  Produce:
  1. The filled-in Phase 2 ranking table
  2. One `### [PerfFix-N]` block per proposed fix (minimum 3, maximum 10)
  3. The Phase 4 verification table
  4. A prioritised "what to tackle first" recommendation (2–3 sentences)

  Do **not** implement the fixes — this command produces proposals for agent hand-off.
  Do **not** add a CLAUDE.md note unless every other enforcement level is unreachable.

  ---

  ## Phase 5 — Browser / React Profiling

  Run this phase in parallel with or after Go profiling. The app runs at `http://localhost:8543`.
  Playwright is available at `tests/e2e/node_modules/.bin/playwright`.

  ### 5a — Capture numeric baseline via Playwright

  Write and run `/tmp/ss-browser-baseline.js`:

  ```javascript
  const { chromium } = require('/Users/tylerstapler/IdeaProjects/stapler-squad/tests/e2e/node_modules/playwright-core');

  async function captureBaseline(label, scenarioFn) {
    const browser = await chromium.launch();
    const page = await browser.newPage();

    await page.addInitScript(() => {
      window.__perfData__ = { longTasks: [] };
      new PerformanceObserver(list => {
        list.getEntries().forEach(e => window.__perfData__.longTasks.push({
          duration: e.duration, startTime: e.startTime
        }));
      }).observe({ entryTypes: ['longtask'] });
    });

    await browser.startTracing(page, {
      path: `/tmp/trace-${label}.json`,
      screenshots: false,
      categories: ['devtools.timeline', 'v8', 'blink.user_timing', 'disabled-by-default-v8.cpu_profiler'],
    });

    const before = await page.metrics();
    await page.goto('http://localhost:8543', { waitUntil: 'networkidle' });
    await scenarioFn(page);
    const after = await page.metrics();
    await browser.stopTracing();

    const longTasks = await page.evaluate(() => window.__perfData__.longTasks);
    console.log(`\n=== ${label} ===`);
    console.log({
      scriptDuration:  (after.ScriptDuration  - before.ScriptDuration).toFixed(3) + 's',
      layoutCount:      after.LayoutCount      - before.LayoutCount,
      recalcStyleCount: after.RecalcStyleCount - before.RecalcStyleCount,
      heapGrowthMB:    ((after.JSHeapUsedSize  - before.JSHeapUsedSize) / 1024 / 1024).toFixed(2) + 'MB',
      nodes:            after.Nodes            - before.Nodes,
    });
    console.log(`Long tasks (>50ms): ${longTasks.length}`, longTasks.map(t => Math.round(t.duration) + 'ms'));
    console.log(`Trace saved: /tmp/trace-${label}.json`);
    await browser.close();
  }

  captureBaseline('initial-load', async (page) => {
    await page.waitForSelector('body');
    await page.waitForTimeout(1000);
  }).then(() =>
  captureBaseline('session-list-scroll', async (page) => {
    await page.waitForSelector('body');
    for (let i = 0; i < 5; i++) {
      await page.keyboard.press('ArrowDown');
      await page.waitForTimeout(100);
    }
  })).catch(console.error);
  ```

  ```bash
  node /tmp/ss-browser-baseline.js
  ```

  ### 5b — Interpret results

  **Long tasks (>50ms)**: each one blocks user input and shows up as red-flagged bars in the Performance panel.
  Load `/tmp/trace-initial-load.json` into Chrome DevTools → Performance tab for the flamechart.

  **Key metrics to flag**:
  | Metric | Warning threshold | Critical threshold |
  |--------|------------------|--------------------|
  | `scriptDuration` on initial load | > 0.5s | > 1.0s |
  | `layoutCount` per interaction | > 10 | > 50 |
  | `heapGrowthMB` after 10 interactions | > 5MB | > 20MB |
  | Long task count on load | > 3 | > 10 |
  | Single long task duration | > 100ms | > 500ms |

  ### 5c — React-specific checks

  Add a temporary `<Profiler>` wrapper in the dev build around the sessions list:

  ```tsx
  import { Profiler, type ProfilerOnRenderCallback } from 'react';

  const onRender: ProfilerOnRenderCallback = (id, phase, actualDuration, baseDuration) => {
    if (actualDuration > 16)
      console.warn(`[Profiler] ${id} (${phase}): ${actualDuration.toFixed(1)}ms  ratio: ${(actualDuration/baseDuration).toFixed(2)}`);
  };

  <Profiler id="SessionList" onRender={onRender}>
    <SessionList />
  </Profiler>
  ```

  Key ratio: `actualDuration / baseDuration` → near 1.0 = memoization absent; near 0.1 = working.

  ### 5d — Bundle size check

  ```bash
  cd web-app && npm run build 2>/dev/null | tail -20
  # Then inspect the largest chunks
  ls -lah web-app/.next/static/chunks/*.js 2>/dev/null | sort -k5 -rh | head -10
  # or for Vite/CRA:
  ls -lah web-app/dist/assets/*.js 2>/dev/null | sort -k5 -rh | head -10
  ```

  ### 5e — Browser fix proposals (same template as Phase 3)

  For each browser bottleneck found, produce a `### [PerfFix-Browser-N]` block:
  - **Signal**: metric name + value
  - **Root cause**: one sentence
  - **Fix**: what component/hook to change
  - **Enforcement**: Jest/RTL test or Playwright perf assertion that would catch regression

---

# perf:make-it-faster

Connect to the live pprof endpoint, read all five profiles, rank hotspots by CPU cycles
and allocation rate, produce numbered fix proposals with enforcement stubs, and verify
each proposal would have caught the regression via the Reflect & Fix ladder.

## Quick start

```bash
# Server must be running with --profile
make restart-web PROFILE_FLAGS="--profile"

# Capture all profiles in one shot
for p in goroutine mutex block heap allocs; do
  curl -s "http://localhost:6060/debug/pprof/${p}?debug=1" > /tmp/ss-${p}.txt
done

# Then invoke this command — it reads the files and does the rest
```

## Profile quick-reference

| Profile | Primary metric | What to look for |
|---------|---------------|-----------------|
| `mutex` | cycles waiting for a lock | stdlib `log.Printf` in hot paths; RWMutex on read-heavy paths |
| `block` | cycles blocked in select/chan | abnormally high `count` on per-connection goroutines |
| `allocs` | total-bytes column `[N: X]` | proto Marshal per frame, ORM full-row reads |
| `heap` | in-use objects | large objects without pool; compress encoder per request |
| `goroutine` | goroutine count and state | leaks (`[sleep, X minutes]`), lock storms (`[semacquire]`) |

## Enforcement ladder

```
1. Compile time  → type / interface change
2. Lint rule     → golangci-lint custom check or existing rule
3. Benchmark     → AllocsPerRun or ns/op regression gate
4. Unit test     → asserts pre-fix code fails
5. CLAUDE.md     → last resort only
```

## Browser quick-reference

```bash
# Run browser baseline (app must be on localhost:8543)
node /tmp/ss-browser-baseline.js

# Inspect bundle chunks by size
ls -lah web-app/.next/static/chunks/*.js 2>/dev/null | sort -k5 -rh | head -10

# JS coverage (unused code)
# Run captureBaseline with page.coverage.startJSCoverage() — see browser-profiling skill
```

| Signal | Tool | Where to look |
|--------|------|---------------|
| Long tasks on load | Playwright `page.metrics()` + `PerformanceObserver longtask` | > 3 tasks or > 100ms each |
| React re-render cascade | `<Profiler onRender>` | `actualDuration / baseDuration` near 1.0 |
| Layout thrashing | Performance panel → "Forced reflow" | `layoutCount` > 10 per interaction |
| Memory leak | Playwright heap delta across 10 cycles | > 5MB growth |
| Oversized bundle | `source-map-explorer` or `.next/static/chunks` | chunk > 500KB unparsed |

## Known hotspots (as of 2026-05-02)

| Location | Profile | Cycles | Count | Fix direction |
|----------|---------|--------|-------|---------------|
| `session/instance_status.go:78` | mutex | 2.2B | 5094 | remove debug Printf from GetStatus hot path |
| `session/review_queue_poller.go:557` | mutex | 1.4B | 2607 | gate behind `DebugLog != nil` or remove |
| `session/tmux/control_mode.go:331` | mutex | 2.7B | 94 | remove Printf from %output hot path |
| `server/services/connectrpc_websocket.go:629` | block | 23T | 26437 | remove per-frame debug log from stream goroutine |
| `session/ent_repository.go:622` via `storage.go:285` | allocs | — | — | direct UPDATE instead of Get + update |
