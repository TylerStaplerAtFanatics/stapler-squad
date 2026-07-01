# Validation: perf-mutex-hotspots-2026-07

**Date**: 2026-07-01
**Plan**: `project_plans/perf-mutex-hotspots-2026-07/implementation/plan.md`
**Source files reviewed**:
- `session/unfinished/gogit_vcs_reader.go` (implementation target, pre-change)
- `session/unfinished/gogit_vcs_reader_limits_test.go` (existing white-box tests + `initRepoInternal`)
- `session/unfinished/vcsreader_bench_test.go` (benchmarks, package `unfinished_test`)
- `session/unfinished/vcsreader_test.go` (black-box tests, package `unfinished_test`)

---

## 1. Requirements-to-Test Coverage Matrix

### Story 1.1.1 — Add singleflight.Group fields and hasUncommittedCache

| Acceptance Criterion | Existing Test | Epic 1.3 Tests | Gap? |
|---|---|---|---|
| `GoGitVCSReader` has three new `singleflight.Group` fields (`aheadBehindSF`, `diffStatSF`, `hasUncommittedSF`) | None — no field-existence test exists | Not directly asserted; implied by compilation and the concurrency test invoking `r.AheadBehind(...)` on a `*GoGitVCSReader{}` | **Minor gap**: no compile-time field presence test, but compilation failure is the effective guard. Acceptable. |
| `GoGitVCSReader` has a new `hasUncommittedCache sync.Map` field | None | `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` directly accesses `r.hasUncommittedCache.Store(...)` — this **will not compile** if the field is absent (white-box test, package `unfinished`). Effective coverage. | No gap. |
| `hasUncommittedEntry` struct defined with `result bool` and `expiry time.Time` | None | Same test types `hasUncommittedEntry{result: !got, expiry: time.Now().Add(30 * time.Second)}` — compilation enforces field names and types. | No gap. |
| Import `golang.org/x/sync/singleflight` added | None | Compilation of the singleflight calls enforces import. | No gap. |
| `GoGitVCSReader{}` zero value is valid (no nil-pointer panic) | `BenchmarkFullScanCycle` and all existing `TestGoGitVCSReader_*` tests allocate `&GoGitVCSReader{}` without a constructor. | New tests also use `r := &GoGitVCSReader{}`. | No gap. |

### Story 1.1.2 — Wrap AheadBehind and DiffShortstat slow paths in singleflight.Do

| Acceptance Criterion | Existing Test | Epic 1.3 Tests | Gap? |
|---|---|---|---|
| AheadBehind fast path (TTL check) stays before `Do` call | `TestGoGitVCSReader_AheadBehind_BehindCount` + `BenchmarkAheadBehind` exercise the method; `BenchmarkDiffShortstatCached` asserts 0 allocs on warm cache (catches if fast path broke). | N/A | **Partial**: no test explicitly asserts the fast path executes on second call for `AheadBehind`. `BenchmarkDiffShortstatCached` only covers `DiffShortstat`. Functional regression would be caught only by benchmark alloc assertions, not unit tests. |
| `entry.mu.Lock()` is acquired with `defer entry.mu.Unlock()` inside Do body (no scattered explicit unlocks) | None — correctness of defer usage is structural, not functionally tested. | Panic recovery test (`TestGoGitVCSReader_AheadBehind_PanicRecovery`) is specified but **not yet written in Epic 1.3**; it would catch the deadlock scenario indirectly. | **Gap**: No panic-under-lock test exists for `AheadBehind` or `DiffShortstat` to prove the defer pattern is correct. The plan specifies this test but it is listed as a future task, not present in the current test file. |
| Deferred panic recovery inside Do converts panics to errors | None | `TestGoGitVCSReader_AheadBehind_PanicRecovery` is specified in the plan but **absent from the current test file**. | **P1 Gap** (see verdict below). |
| Result stored to cache inside `Do` is also returned to callers | `BenchmarkDiffShortstatCached` (0-alloc warm path) implicitly catches cache-store regression for `DiffShortstat`. No equivalent for `AheadBehind`. | `TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers` verifies all 4 callers get the same value, which requires the result propagation to work. | Covered for the singleflight case. Cache-store is an implied prerequisite. |
| Caller signature `(int, int, error)` / `(DiffStat, error)` unchanged | Existing `TestGoGitVCSReader_AheadBehind_BehindCount`, `TestGoGitVCSReader_HasUncommitted_*`, `BenchmarkDiffShortstat` compile-check the signature. | N/A | No gap. |

### Story 1.1.3 — Add TTL cache and singleflight to HasUncommitted

| Acceptance Criterion | Existing Test | Epic 1.3 Tests | Gap? |
|---|---|---|---|
| `HasUncommitted` returns cached result immediately if entry is valid | None | `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` — pre-populates cache with inverted value and asserts second call returns the inverted value. **Directly covers this criterion.** | No gap. |
| On cache miss, exactly one goroutine performs the index walk via `hasUncommittedSF.Do` | None | The plan specifies the concurrency test structure for `AheadBehind`; a parallel `HasUncommitted` test is **not written** in Epic 1.3. `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` does not test concurrent callers. | **Gap**: no concurrency test for `HasUncommitted`. Singleflight deduplication is not regression-tested. |
| Do body has a deferred panic recovery | None | Not written. | **Gap** (same as Story 1.1.2 panic test). |
| `entry.mu.Lock()` is held inside Do for go-git phase and released before OS-only phase | `TestGoGitVCSReader_HasUncommitted_StagedChange`, `TestGoGitVCSReader_HasUncommitted_StagedDeletion`, `TestGoGitVCSReader_HasUncommitted_MergeConflict` exercise the method functionally. | N/A — structural: defer/explicit unlock usage is not testable at the function level without a deliberate panic. | Functional correctness covered; panic-deadlock scenario has a gap (see above). |
| Inner helper `hasUncommittedGoGitPhase` extracted (no scattered explicit unlocks in Do body) | None — this is structural. | No test validates the refactor exists; compilation is the only guard. | **Minor gap**: no test drives the panic path that proves deadlock is eliminated. Acceptable given the defer pattern is compile-time verifiable. |
| Final result stored to `hasUncommittedCache` with 30s TTL | None | `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` — cold call populates cache; warm call reads it. **Implicitly** covers cache store on the slow path. | No gap. |
| `go test -race ./session/unfinished/...` passes | All existing white-box tests pass under `-race` today (no data races in the pre-change code). | New tests are in the white-box package and will be run with `-race` per the plan's verification step. | Covered by plan step 1.4.1. |

### Story 1.2.1 — InvalidateDirtyCache on Resume and Stop

| Acceptance Criterion | Existing Test | Epic 1.3 Tests | Gap? |
|---|---|---|---|
| `GitWorktreeManager.InvalidateDirtyCache()` calls `gm.worktree.InvalidateDirtyCache()` if worktree != nil | `git/worktree_git.go` already has `InvalidateDirtyCache()` on `*GitWorktree`. No test for the manager wrapper. | None — Epic 1.3 does not specify a test for Story 1.2.1. | **Gap**: no unit test for `GitWorktreeManager.InvalidateDirtyCache()`. |
| `GitManager` interface includes `InvalidateDirtyCache()` | Compilation enforces interface satisfaction (`var _ GitManager = (*GitWorktreeManager)(nil)` at line 249 of `git_worktree_manager.go`). | N/A | No gap (compile-time check). |
| `Pause()` calls `i.gitManager.InvalidateDirtyCache()` after `transitionTo(Paused)` | None | None | **Gap**: no integration/unit test that verifies cache invalidation occurs on Pause. |
| `Resume()` calls `i.gitManager.InvalidateDirtyCache()` after `transitionTo(Active)` | None | None | **Gap**: no integration/unit test that verifies cache invalidation occurs on Resume. |
| No panic if `gitManager.worktree == nil` | None | None | **Gap**: no nil-guard test for `InvalidateDirtyCache`. |

### Story 1.3.1 — New concurrency and cache tests

| Acceptance Criterion | Existing Test | Epic 1.3 Tests | Gap? |
|---|---|---|---|
| `TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers` in `gogit_vcs_reader_limits_test.go` | None | **Specified and written in plan**. Not yet in repo — the file currently ends at line 253. Will be added during implementation. | No gap (test is fully specified in the plan). |
| `TestGoGitVCSReader_AheadBehind_PanicRecovery` | None | **Mentioned** in the plan but **no test body is written**. The plan says "inject a repo whose Head() call panics via a mock (or verify via deliberately malformed temp repo)" but provides no code. | **Gap**: this test is not implemented. |
| `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` | None | **Specified and written in plan** with complete code. | No gap (test is fully specified). |

---

## 2. Test Sufficiency Verdict Per Story

### Story 1.1.1 — Add singleflight.Group fields and hasUncommittedCache
**Verdict: SUFFICIENT**

The key structural criteria (field presence, struct layout, zero-value safety) are enforced by compilation. The `hasUncommittedCache` and `hasUncommittedEntry` fields are directly exercised by `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue`, which will fail to compile if either is absent or malformed. Existing tests cover zero-value instantiation. No P1 acceptance criterion is uncovered.

### Story 1.1.2 — Wrap AheadBehind and DiffShortstat slow paths in singleflight.Do
**Verdict: PARTIAL**

`TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers` covers the coalescing requirement and return-value consistency. `BenchmarkDiffShortstatCached` (0-alloc assertion) guards the `DiffShortstat` fast path. **The panic recovery path has no test** (`TestGoGitVCSReader_AheadBehind_PanicRecovery` is mentioned but not written). The defer-vs-explicit-unlock correctness is structurally unverified. This is the highest-risk gap given that incorrect mutex handling (explicit unlocks without defer inside `Do`) causes permanent deadlocks.

### Story 1.1.3 — Add TTL cache and singleflight to HasUncommitted
**Verdict: PARTIAL**

The cache fast path is well-tested by `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue`. Functional correctness of the slow path is covered by the existing `TestGoGitVCSReader_HasUncommitted_*` tests in `vcsreader_test.go`. **Two gaps remain**: (1) no concurrency test verifying singleflight deduplication for `HasUncommitted`; (2) no panic recovery test. The `hasUncommittedGoGitPhase` inner-helper extraction (which eliminates the deadlock risk) has no test that exercises the panic path.

### Story 1.2.1 — Add InvalidateDirtyCache to GitWorktreeManager and call from Pause/Resume
**Verdict: INSUFFICIENT**

No test was written for this story. The compile-time interface check (`var _ GitManager = (*GitWorktreeManager)(nil)`) will catch missing method implementations, but: the nil guard, the Pause/Resume call sites, and the end-to-end cache-invalidation behavior have zero test coverage. The plan explicitly notes Epic 1.3 does not cover Story 1.2.1.

### Story 1.3.1 — New singleflight concurrency test and panic recovery test
**Verdict: PARTIAL**

`TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers` and `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` are fully specified with complete code in the plan. `TestGoGitVCSReader_AheadBehind_PanicRecovery` is mentioned but has **no implementation body** — it is a future task marker, not a completed test. This leaves the panic recovery path unverified by any test.

---

## 3. Race Detector Coverage

Both new tests are in package `unfinished` (white-box, same package as `gogit_vcs_reader.go`). They are plain `*testing.T` unit tests with no `//go:build` constraints, so they will be included in `go test -race ./session/unfinished/...`.

### TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers

**Runnable under `go test -race`: YES**

Analysis of constructs:
- Uses `sync.WaitGroup` correctly: `wg.Add(workers)` before goroutine launch, `wg.Wait()` before reading `results`. The race detector will correctly instrument the goroutine-to-main channel.
- `results := make([]result, workers)` — each goroutine writes to a distinct index (`results[idx]`). No concurrent writes to the same index. No data race.
- Reads `results[0].ahead` / `results[0].behind` after `wg.Wait()` — safe.
- `r := &GoGitVCSReader{}` — shared reader accessed concurrently. After the implementation is in place, `singleflight.Group`, `sync.Map`, and `atomic` operations are all race-detector-compatible. **If the singleflight changes are NOT yet applied** (pre-change code), this test will expose data races on `entry.mu` contention — which is the intended regression.
- One concern: the test reads `results[i]` in the final loop without a lock. This is safe because all goroutines have exited via `wg.Wait()` before the loop begins. The race detector will confirm this.

**No constructs prevent race detection.**

### TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue

**Runnable under `go test -race`: YES**

Analysis of constructs:
- Single-goroutine test. No concurrent access. The race detector has nothing to flag here.
- `r.hasUncommittedCache.Store(dir, hasUncommittedEntry{...})` — `sync.Map` is race-detector-safe.
- The test does not call `t.Parallel()`, so it runs sequentially relative to other tests. No cross-test sharing.
- The warm-call `r.HasUncommitted(dir)` after the Store exercises the cache fast path. If the fast path is missing (pre-change), the function re-enters the slow path and returns the actual (non-inverted) value, causing the test to fail — correct behavior.

**No constructs prevent race detection.**

### Additional note on pre-change state

The **current** `gogit_vcs_reader.go` (as read) has no `singleflight` fields, no `hasUncommittedCache`, and no `hasUncommittedEntry` type. Running the two new tests against the pre-change code will result in:
- `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` — **compile failure** (references `r.hasUncommittedCache` and `hasUncommittedEntry` which do not exist). This is the correct sentinel behavior: the test cannot even be compiled until Story 1.1.1 is implemented.
- `TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers` — **compiles and passes** against pre-change code (it only calls the public `AheadBehind` method), but `go test -race` may surface data races if concurrent `entry.mu` acquisitions race. This is useful as a regression gate.

---

## 4. Readiness Gate Verdict

### P1 Criteria Assessment

The following acceptance criteria are classified P1 (blocking for merge):

| ID | Criterion | Coverage |
|---|---|---|
| P1-A | `hasUncommittedCache` field and `hasUncommittedEntry` struct exist and are exercised | Covered by `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue` (compile + runtime). |
| P1-B | Singleflight coalescing for `AheadBehind` works (N callers → 1 Do execution, consistent results) | Covered by `TestGoGitVCSReader_AheadBehind_SingleflightCollapsesParallelCallers`. |
| P1-C | `go test -race` passes (no data races) | Both new tests are race-detector compatible. Plan mandates `go test -race ./session/unfinished/...` in Story 1.4.1. |
| P1-D | Panic recovery path converts go-git panics to errors (no deadlock, no crash propagation) | **NOT COVERED** — `TestGoGitVCSReader_AheadBehind_PanicRecovery` is specified but not implemented. |
| P1-E | `HasUncommitted` TTL cache fast path returns cached value | Covered by `TestGoGitVCSReader_HasUncommitted_CacheHitReturnsCachedValue`. |

P1-D has no test coverage. However, the plan documents the panic recovery requirement extensively and the implementation mandates `defer entry.mu.Unlock()` + named return `doErr` — the correctness is structurally verifiable by code review at merge time. The absence of a panic recovery test is a quality gap but the plan explicitly defers it to a future task marker ("OR verify the panic recovery path doesn't crash the caller by testing with a deliberately malformed temp repo") without a code body.

**Interpreting the gate strictly**: P1-D (panic recovery) has NO test coverage. The criterion states panics must be converted to errors and must not deadlock. There is no test for this. This is a P1 acceptance criterion with no test.

**GATE: FAIL** — P1 acceptance criterion P1-D (panic recovery in AheadBehind/HasUncommitted Do body) has no test coverage; `TestGoGitVCSReader_AheadBehind_PanicRecovery` is mentioned in the plan but not implemented.
