# Validation Plan: Session Creation UX

**Phase**: 4 — Validation
**Date**: 2026-04-12

---

## Requirements Coverage Matrix

| Requirement | Test | Status |
|---|---|---|
| Path suggestions sorted by most-recently-used | `frecency.test.ts` > "ranks a recently-used path above a stale-but-frequent path when recency dominates" | Done |
| Path suggestions weight frequency (multi-session paths rank higher) | `frecency.test.ts` > "ranks a frequently-used path above a rarely-used path of equal recency" | Done |
| Path suggestions use max timestamp across all sessions for a path | `frecency.test.ts` > "uses the maximum timestamp across all sessions for a path" | Done |
| Path with no valid timestamps falls to bottom | `frecency.test.ts` > "paths with no valid timestamps receive score 0 and sort to the bottom" | Done |
| Future timestamps clamped to age 0 (no negative age) | `frecency.test.ts` > "clamps negative age (future timestamps) to 0" | Done |
| Score halves at exactly one half-life (7 days) | `frecency.test.ts` > "halves the score at exactly one half-life (7 days by default)" | Done |
| Score quarters at two half-lives (14 days) | `frecency.test.ts` > "quarters the score at two half-lives (14 days)" | Done |
| No suggestions when no sessions exist — fallback hint paths shown | `useRepositorySuggestions.ts` fallback branch; manual test | Manual |
| "New Workspace" pre-fills path, program, category; clears title and branch | Manual test — wizard pre-fill | Manual |
| Branch defaults to session title with no checkbox or disabled input | Manual test — branch preview row | Manual |
| "Customize" link reveals branch text input | Manual test — progressive disclosure expand | Manual |
| "Use session name instead" resets useTitleAsBranch to true | Manual test — progressive disclosure collapse | Manual |
| Review step shows resolved branch name when useTitleAsBranch is true | Manual test — review step | Manual |
| Step 1 validation blocks advance with empty branch in custom mode | Manual test — validation gating | Manual |

---

## Unit Tests

### frecency.ts (`web-app/src/lib/utils/__tests__/frecency.test.ts`)

17 tests, all passing. The suite uses a fixed `NOW = 1_000_000_000_000` ms and a `DAY_MS` constant to produce fully deterministic results without mocking `Date.now()`.

#### `computeFrecencyScore` (7 tests)

| Test | Property Verified |
|---|---|
| Returns 0 when `mostRecentMs` is 0 (no known activity) | Sentinel value for missing timestamps returns a score of exactly 0, preventing phantom paths from appearing in rankings |
| Returns frequency when age is 0 (used right now) | At age 0, `0.5^0 = 1`, so score equals raw frequency — the upper bound of the scoring range |
| Halves the score at exactly one half-life (7 days by default) | The defining property of half-life decay: score at `t = halfLifeMs` is exactly `frequency / 2` |
| Quarters the score at two half-lives (14 days) | Compound decay: score at `t = 2 × halfLifeMs` is exactly `frequency / 4` |
| A high-frequency stale path ties a low-frequency fresh path at the right crossover age | 8 sessions at 7 days ago equals 4 sessions used today — confirms frequency and recency trade off at the correct rate |
| Accepts a custom half-life | The `halfLifeMs` parameter is injectable; a 1-day half-life is exercised directly |
| Clamps negative age (future timestamps) to 0 | `Math.max(0, nowMs - mostRecentMs)` prevents a future `mostRecentMs` from producing a score above the frequency ceiling |

#### `rankPathsByFrecency` (10 tests)

| Test | Property Verified |
|---|---|
| Returns empty array for empty input | Degenerate case: no sessions produces no paths |
| Returns empty array when all sessions lack a path | Sessions with `path: undefined` or `path: ""` are skipped entirely; the output is empty even if timestamps are valid |
| Ranks a frequently-used path above a rarely-used path of equal recency | When two paths have the same most-recent timestamp, the one with more sessions ranks first |
| Ranks a recently-used path above a stale-but-frequent path when recency dominates | 1 session from 1 minute ago beats 5 sessions all older than 21 days; recency overrides frequency when the age gap is large enough |
| Uses the maximum timestamp across all sessions for a path | A path with one very recent session and two old sessions uses the recent timestamp — only the most active signal counts |
| Ignores zero timestamps when computing `mostRecentMs`, uses the non-zero one | A session with `[0, 0, NOW - DAY_MS]` uses the non-zero timestamp; zeros are filtered before the max computation |
| Paths with no valid timestamps receive score 0 and sort to the bottom | A session with `[0, 0]` produces `mostRecentMs = 0`, triggering the sentinel and placing the path last |
| Uses the maximum timestamp across multiple timestamp fields per session | The three fields (`updatedAt`, `lastMeaningfulOutput`, `createdAt`) are all considered; the highest wins even if earlier fields are older |
| Handles a single session | Smoke test for the trivial one-path case |
| Is stable for equal scores (same path order preserved) | Two paths with identical frequency and identical recency both appear in the result — no silent deduplication or crash on tie |

---

## Integration Tests (Manual)

The following scenarios require the server running (`make restart-web`) and at least one existing session.

### 1. Path autocomplete shows frecency-ranked suggestion first

**Setup**: Create two groups of sessions — for example, 3 sessions on `/projects/main-app` with `updatedAt` 8 days ago, and 1 session on `/projects/quickfix` with `updatedAt` today.

**Steps**:
1. Open the new-session wizard (click `+` or the new-session button).
2. Focus the path input field.

**Expected**: `/projects/quickfix` appears first (1 session × ≈1.0 decay ≈ 1.0), above `/projects/main-app` (3 sessions × 0.5^(8/7) ≈ 1.35). If the numbers are close, adjust the age gap until the frecency winner is unambiguous.

**Regression**: With pure recency, `/projects/quickfix` also won — but any high-frequency path used 3–4 days ago would incorrectly beat a low-frequency path used today. Verify that scenario too.

### 2. "New Workspace" action pre-fills path, program, and category; leaves title empty

**Setup**: Any session card with a known path, program, and category.

**Steps**:
1. Click the "New Workspace" action on a session card.
2. Observe the wizard that opens.

**Expected**:
- Path field pre-filled with the source session's path.
- Program field pre-filled (e.g., "claude").
- Category field pre-filled.
- Title field is empty with placeholder text.
- Branch field shows `(enter session title first)` placeholder — it has not been pre-filled.

**Not expected**: Title or branch copied from the source session.

### 3. Branch defaults to title without any click required

**Steps**:
1. Open the new-session wizard.
2. Type a session title, e.g. `fix-login-bug`.
3. Proceed to the branch step without clicking anything in the branch row.

**Expected**: The branch preview row displays `fix-login-bug` (or a slugified/normalised form of it). The branch text input is not visible. No checkbox is shown.

### 4. "Customize" reveals branch input; "Use session name instead" clears it

**Steps**:
1. Open the wizard and type a title.
2. On the branch step, click "Customize".

**Expected**: A text input appears, pre-filled with the derived branch name. The "Use session name instead" link is visible.

3. Edit the branch name to something custom, e.g., `JIRA-123-fix-login`.
4. Click "Use session name instead".

**Expected**: The text input disappears. The preview row shows the title-derived branch name again. The custom value is discarded.

### 5. Review step shows resolved branch name when `useTitleAsBranch` is true

**Steps**:
1. Open the wizard, fill in a title, leave branch as default (`useTitleAsBranch = true`).
2. Advance to the review/confirmation step.

**Expected**: The review step shows the branch name that will be created (the derived form of the title), not "auto" or blank. The user can confirm what branch will be created before submitting.

### 6. Step 1 validation — cannot advance with empty branch when in custom-branch mode

**Steps**:
1. Open the wizard, fill in a title.
2. On the branch step, click "Customize".
3. Clear the branch input entirely (leave it blank).
4. Attempt to advance to the next step.

**Expected**: Validation fires. An error message appears on the branch field (e.g., "Branch name is required"). The wizard does not advance.

**Also verify**: With `useTitleAsBranch = true` (default, no "Customize" clicked), the wizard advances normally even if the branch text input would be empty — because the branch is derived from the title, not the input.

### 7. Frecency re-ranking after time passes

**Setup**: Create scenario from test 1 above, but use a fixed clock offset. With real time this requires waiting or faking `Date.now` in the browser console.

**Alternative manual approach**: Open the wizard with the browser devtools open. In the console, verify the `rankPathsByFrecency` output by calling it directly with controlled timestamps:

```javascript
// In browser console (after importing or via window exposure in dev mode)
// Confirm that a path with 3 sessions 8 days old loses to 1 session today
```

This is documented as a gap in automated coverage (see below).

---

## Edge Cases to Verify

- [ ] Creating first session (no existing sessions) — the `useRepositorySuggestions` hook falls back to OS-appropriate placeholder paths (`/home/username/projects`, `/home/username/code` on Linux) rather than showing an empty autocomplete.
- [ ] Deleting all sessions for a path — after deletion, the path no longer appears in `listSessions`, so `rankPathsByFrecency` receives no sessions for it, and it disappears from suggestions on the next wizard open.
- [ ] Session with no timestamps (all three fields zero or absent) — `mostRecentMs` stays 0; `computeFrecencyScore` returns 0; the path sorts to the bottom. Confirmed by unit test "paths with no valid timestamps receive score 0 and sort to the bottom."
- [ ] Duplicate flow with `useTitleAsBranch = true` — when the user opens a duplicate/fork and does not click "Customize," the old session's branch name must not be submitted. The `branch` field in the form payload should be empty or omitted; only the derived title-as-branch should reach the backend.
- [ ] Future-dated session timestamp (clock skew) — `computeFrecencyScore` clamps negative age to 0, so a session whose `updatedAt` is in the future is treated as age 0 and scores at full frequency. This is the most generous possible interpretation and is unlikely to cause harm.

---

## What Is NOT Tested

**No automated test for `useRepositorySuggestions` hook.** The hook itself (`web-app/src/lib/hooks/useRepositorySuggestions.ts`) has no unit or integration test. Only the pure `rankPathsByFrecency` function is tested directly. The hook's correctness depends on:
- The ConnectRPC `listSessions` call returning the right shape (untested against a real or mock server in Jest).
- The timestamp extraction logic (`Number(session.updatedAt.seconds) * 1000`, etc.) correctly mapping protobuf `Timestamp` fields to milliseconds (untested).
- The fallback path logic for empty sessions (untested in isolation).

To address this gap, a Jest test mocking `@connectrpc/connect` and `@connectrpc/connect-web` (matching the pattern used in `usePathCompletions.test.ts`) would be the minimal addition needed.

**No E2E test for autocomplete ranking order.** There is no Playwright test that verifies `/projects/frequent-path` appears above `/projects/rare-path` in the wizard autocomplete dropdown. The ranking is exercised only at the unit level (`frecency.test.ts`). An E2E test would require seeding the server with controlled session data and a controlled clock, which is non-trivial.

**No test for branch-preview row behaviour in `SessionWizard`.** The progressive disclosure expand/collapse (Customize / Use session name instead), the branch preview rendering, and the step-validation gating for empty custom branches are all covered only by the manual integration tests listed above. No Jest component test exercises `SessionWizard` at this level.

**No test for `onNewWorkspace` pre-fill behaviour.** The `handleNewWorkspaceSession` handler in `page.tsx` and the prop chain through `SessionList` → `SessionCard` are not unit-tested. The correctness of which fields are pre-filled vs. cleared is verified only manually.
