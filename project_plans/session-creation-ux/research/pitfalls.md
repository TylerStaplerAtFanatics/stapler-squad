# Research: Pitfalls — Failure Modes & Risks

**Dimension**: Pitfalls
**Date**: 2026-04-12

## Summary

Five distinct failure modes were traced through the actual code. Two are confirmed bugs that can cause incorrect data to be submitted silently: the duplicate-flow "lurking old branch" issue and the branch validation bypass when `useTitleAsBranch` is true but `branch` is empty. One is a confirmed UX defect: the Review step never shows the resolved branch name when `useTitleAsBranch` is true, so users always see an empty branch field on step 3. The stale-suggestions risk is real but low-blast-radius. The empty-title guard at step 0 works correctly because `trigger(["title","category"])` will fail on an empty `title`.

---

## Finding 1: Lurking Old Branch Value in Duplicate Flow

**Severity**: High
**Status**: Confirmed bug

**Description**: `handleDuplicateSession` (page.tsx:280) sets `initialData` with `branch: session.branch` but does NOT set `useTitleAsBranch`. This means `useTitleAsBranch` falls through to `defaultValues.useTitleAsBranch = true`. When the user never clicks "Customize", `useTitleAsBranch` remains `true` and the wizard UI shows the title as the branch preview. However, the `branch` field in form state still holds `session.branch` (the old session's branch). When the user submits, `handleWizardComplete` evaluates: `data.useTitleAsBranch ? data.title : (data.branch || "")`. Because `useTitleAsBranch` is `true`, the backend correctly receives `title` as the branch name — so the submitted value is correct.

However, consider the reverse scenario: the user clicks "Customize", sees the pre-filled old branch name (from `session.branch`), then clicks "Use session name instead". This calls `setValue("useTitleAsBranch", true)` but does NOT call `setValue("branch", "")`. The `branch` field still holds `session.branch`. If the user later clicks "Customize" again, they see the original session's branch still filled in — which may be confusing and lead them to accidentally submit the old branch if they forget to change it.

More critically: if the user manually clears the branch field while in custom mode (branch = ""), then clicks "Use session name instead" and then "Customize" again, the field is now empty. Zod refine line 77 in sessionSchema.ts will fail: `sessionType === "new_worktree" && !useTitleAsBranch` → branch is required. But this is the "lurking empty" state — the user may not realize the field cleared.

**Evidence**:
- `web-app/src/app/page.tsx:280-294` — `handleDuplicateSession` sets `branch: session.branch` without `useTitleAsBranch`
- `web-app/src/components/sessions/SessionWizard.tsx:256-261` — "Use session name instead" only calls `setValue("useTitleAsBranch", true)`, never clears `branch`
- `web-app/src/lib/validation/sessionSchema.ts:90-102` — `defaultValues` sets `useTitleAsBranch: true`, so the duplicate flow inherits `true` via spread

**Mitigation**: Either (a) explicitly set `useTitleAsBranch: false` in `handleDuplicateSession` since the intent is to preserve the old branch, or (b) call `setValue("branch", "")` alongside `setValue("useTitleAsBranch", true)` in the "Use session name instead" click handler to keep state consistent.

---

## Finding 2: Step 1 Branch Validation — Cross-Field Refine Not Triggered by `trigger(["branch"])`

**Severity**: High
**Status**: Confirmed bug

**Description**: The `stepFields` for step 1 is `["path", "workingDir", "sessionType", "branch", "existingWorktree"]` (SessionWizard.tsx:64-65). When `validateStep` calls `trigger(fields)`, react-hook-form runs field-level validators for those specific fields. However, the critical validation "branch is required when `new_worktree` and `!useTitleAsBranch`" is implemented as a **root-level `z.refine`** on the entire schema object (sessionSchema.ts:74-86), not as a field-level refinement. 

Root-level `refine` validators are only evaluated when the full schema is parsed (i.e., on final `handleSubmit`). Calling `trigger(["branch"])` on a field that is `z.string().optional()` will pass even when branch is an empty string and `useTitleAsBranch` is false, because field-level optional refinement does not trigger the cross-field constraint.

Concretely: a user can set `sessionType = new_worktree`, click "Customize", leave the branch field blank, and then click "Next". The step-1 validation will pass (branch is optional at the field level), and the user will reach step 3 where they can click "Create Session". `handleSubmit` will then invoke the full zod parse — which WILL catch the error — but the error lands on `branch` which is no longer visible (the user is on step 3). The form will silently block submission and the user will see no error because `errors.branch` is only rendered on step 1. The "Create Session" button will appear to do nothing.

**Evidence**:
- `web-app/src/lib/validation/sessionSchema.ts:29-35` — `branch` field is `z.string().optional()` with only a character-format refine; no min-length constraint
- `web-app/src/lib/validation/sessionSchema.ts:74-86` — the "branch required for new_worktree" constraint is a root-level `.refine()`, not a field `.refine()`
- `web-app/src/components/sessions/SessionWizard.tsx:63-73` — `trigger(stepFields[step])` only validates named fields, not root-level refinements
- `web-app/src/components/sessions/SessionWizard.tsx:251-253` — `errors.branch` is only rendered in `{step === 1}` block

**Mitigation**: Move the branch-required constraint into the `branch` field itself using a conditional, OR re-check the cross-field conditions inside `validateStep` using `getValues()` before calling `trigger`, OR navigate back to step 1 when `handleSubmit` encounters errors on step-1 fields.

---

## Finding 3: Review Step Does Not Show Resolved Branch for `useTitleAsBranch` = true

**Severity**: Medium
**Status**: Confirmed UX defect

**Description**: On step 3 (Review), the branch display is (SessionWizard.tsx:403-409):
```tsx
{formValues.sessionType === "new_worktree" && formValues.branch && (
  <div className={styles.reviewItem}>
    <span className={styles.reviewLabel}>Git Branch:</span>
    <span className={styles.reviewValue}>{formValues.branch}</span>
  </div>
)}
```
When `useTitleAsBranch` is true (the default), `formValues.branch` is `""` (empty string from defaultValues). The condition `formValues.branch &&` evaluates to `false`, so the branch row is entirely hidden. The user sees no branch information on the review step even though a branch WILL be created (equal to the session title) when they submit.

**Evidence**:
- `web-app/src/components/sessions/SessionWizard.tsx:403-409` — branch review row gated on `formValues.branch` being truthy
- `web-app/src/lib/validation/sessionSchema.ts:96` — `branch: ""` is the default value
- `web-app/src/app/page.tsx:307` — `handleWizardComplete` resolves `branchName = data.useTitleAsBranch ? data.title : (data.branch || "")`, so a branch IS created silently

**Mitigation**: Replace the `formValues.branch &&` guard with a derived display value: if `formValues.sessionType === "new_worktree"`, always show branch as either `formValues.branch` (when custom) or `formValues.title` (when `useTitleAsBranch` is true), with a "(auto from title)" annotation.

---

## Finding 4: Stale Suggestions After Session Deletion

**Severity**: Low
**Status**: Theoretical risk — low blast radius

**Description**: `useRepositorySuggestions` fetches `listSessions` once on mount (the `useEffect` has `[baseUrl]` as its dependency array — useRepositorySuggestions.ts:81). Deleting a session while the wizard is open will not remove its path from the dropdown. However, this is low risk: the suggestions are repository paths, not session IDs. A path appearing in autocomplete after its associated session is deleted is harmless — the path likely still exists on disk. The worst outcome is a slightly stale autocomplete. The dropdown is also an input, not a strict enum, so the user is never forced to pick a stale value.

If the user closes and reopens the wizard, a fresh mount occurs and suggestions re-fetch.

**Evidence**:
- `web-app/src/lib/hooks/useRepositorySuggestions.ts:22,81` — single `useEffect` with `[baseUrl]` dependency; no subscription or polling

**Mitigation**: Acceptable as-is. If path freshness becomes an issue, consider a short `staleTime` cache or refetch-on-window-focus pattern, but this is not needed for the current use case.

---

## Finding 5: Empty-Title Guard at Step 0 — Works Correctly

**Severity**: N/A
**Status**: Non-issue (verified)

**Description**: The concern was whether a New Workspace flow (which initializes `title: ""`) could allow the user to skip to step 3 and submit without entering a title. Tracing the code: `validateStep` calls `trigger(["title", "category"])` for step 0. The `title` field schema is `z.string().min(1, "Session title is required")` (sessionSchema.ts:4-11). Calling `trigger(["title"])` on an empty string will fail with the "required" error and set `errors.title`. The `handleNext` function only advances (`setStep(step + 1)`) when `isValid === true` (SessionWizard.tsx:77-79). There is no way to skip steps — the wizard has no direct step-jump navigation. The "Create Session" submit button only appears on step 3 (`step === steps.length - 1`). Therefore an empty title blocks forward progress at step 0 correctly.

**Evidence**:
- `web-app/src/components/sessions/SessionWizard.tsx:76-80` — `handleNext` gates on `isValid`
- `web-app/src/lib/validation/sessionSchema.ts:4-11` — `title` has `.min(1)`
- `web-app/src/components/sessions/SessionWizard.tsx:463-479` — submit button only appears on final step

**Mitigation**: None needed.

---

## Finding 6: "Use session name instead" Does Not Clear Branch Field Value

**Severity**: Medium
**Status**: Confirmed state-management defect

**Description**: When the user is in custom-branch mode and clicks "Use session name instead", only `setValue("useTitleAsBranch", true)` is called (SessionWizard.tsx:258). The `branch` field value is left intact. This creates an invisible inconsistency: `useTitleAsBranch` is `true` but `branch` holds a non-empty custom value. If the user later toggles "Customize" again, they will see their previous custom branch still in the input. This is a mild UX confusion in the normal flow.

More seriously, consider this path: user enters custom branch "feature/foo", clicks "Use session name instead" (branch field still holds "feature/foo"), then navigates to step 3 and submits. `handleWizardComplete` correctly uses `data.title` as the branch because `data.useTitleAsBranch` is `true`. So the final outcome is correct. However, the inconsistent state could become a source of bugs if future code reads `branch` directly without checking `useTitleAsBranch`.

**Evidence**:
- `web-app/src/components/sessions/SessionWizard.tsx:256-261` — "Use session name instead" only sets `useTitleAsBranch`, does not reset `branch`
- `web-app/src/app/page.tsx:307` — submission logic correctly reads `useTitleAsBranch` first, so runtime behavior is currently safe

**Mitigation**: Add `setValue("branch", "")` alongside `setValue("useTitleAsBranch", true)` in the "Use session name instead" click handler to keep form state canonical. This is a low-effort one-line fix.

---

## Verdict

**Must fix before shipping**:

1. **Finding 2 (Cross-field branch validation bypass)** — Confirmed bug that causes silent submission failure on step 3 with no visible error. Users will click "Create Session" and nothing appears to happen. This needs a fix in either the schema (field-level validation) or the step-navigation logic (back-navigation on errors).

2. **Finding 3 (Review step hides resolved branch)** — The review step misleads users about what branch will be created when `useTitleAsBranch` is true. The branch row should display the resolved value.

**Should fix before shipping**:

3. **Finding 6 (Stale branch value after "Use session name instead")** — One-line fix; keeps form state canonical and prevents future bugs.

**Can defer**:

4. **Finding 1 (Duplicate flow lurking old branch)** — Current submission behavior is correct because `handleWizardComplete` gates on `useTitleAsBranch`. The confusion is a future maintenance risk, not a present data-correctness bug.

5. **Finding 4 (Stale suggestions)** — Cosmetic/low-impact; paths remain valid even after session deletion.
