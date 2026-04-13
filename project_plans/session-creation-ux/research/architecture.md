# Research: Architecture — Patterns & Tradeoffs

**Dimension**: Architecture
**Date**: 2026-04-12

## Summary

The three UX changes in this feature (recency-sorted autocomplete, progressive branch disclosure, and "New Workspace" pre-fill) each map to well-studied design patterns. Pure recency sorting is the correct choice for repository paths given the small, personal dataset and the dominance of "the repo I'm working on right now." The progressive-disclosure pattern chosen — hiding the branch input behind a "Customize" text link — matches established best-practice guidelines for optional advanced fields. The "New Workspace" action correctly maps to the design-systems concept of "fork with fresh identity" rather than "duplicate." The one implementation risk is the `setValue("useTitleAsBranch", …)` approach, which is idiomatic and correct for react-hook-form but requires the field to be `watch()`-ed (already done) to keep React state in sync; no additional registration is needed.

---

## Progressive Disclosure

### The Pattern

Progressive disclosure is the practice of hiding secondary or advanced options until the user actively signals intent to use them. Nielsen Norman Group identifies two canonical forms:

1. **Staged disclosure** — a wizard that presents only the fields relevant to the current step (already used in `SessionWizard` with its 4-step flow).
2. **Contextual reveal** — a "More options" / "Customize" link or chevron that expands additional fields inline without a page transition.

The branch field in `SessionWizard` uses the second form: the default state shows a read-only preview (`sessionTitle`), and a "Customize" button transitions the widget to an editable `AutocompleteInput`. The "Use session name instead" link reverses the transition.

### UX Research Findings

- **When to hide**: Fields should be hidden when (a) a sensible default covers the majority of use cases, and (b) the default is visibly communicated, not silently applied. The current implementation satisfies both: the branch preview row shows the computed value and labels it clearly.
- **When to show**: Advanced fields should be one gesture away — no modal, no new page. An inline expand is preferred over an accordion for single-field reveals because it doesn't shift surrounding content unexpectedly.
- **Label the default explicitly**: Research shows users are anxious about hidden options they don't know exist. The implementation addresses this by rendering the resolved branch name in the preview (`{sessionTitle || "(enter session title first)"}`), confirming the default is active and what it will produce.
- **Reversibility**: The "Use session name instead" link returning `useTitleAsBranch` to `true` is critical — users must be able to undo the customisation. This requirement is already captured as a "Should Have" in the requirements.
- **Accessibility**: The expand/collapse pattern should use a `<button type="button">` (not an `<a>` tag) to ensure keyboard operability and correct `role`. The current implementation uses `<button type="button">`, which is correct.

### Comparison to Common Implementations

| Product | Approach | Notes |
|---|---|---|
| GitHub new-repo form | "Add .gitignore" expander | Inline reveal, no page change |
| Vercel deploy wizard | "Advanced" section at bottom | Full section expand behind a chevron |
| Linear issue creation | Optional fields collapsed by default | Keyboard shortcut to reveal |
| Current implementation | Branch preview + "Customize" link | Correct — inline, single-field, reversible |

The current approach is appropriate and well-aligned with established patterns. One possible refinement: add a `▸` or `⚙` icon to the "Customize" button to signal its nature as an expand action rather than a navigation link, reducing ambiguity.

---

## Autocomplete Ranking Strategies

### The Problem

Given a set of known repository paths from existing sessions, which path should appear first in the autocomplete dropdown when the user opens the new-session wizard?

### Frequency vs. Recency vs. Frecency

**Frequency** counts how many times a path has been used. For paths this means counting sessions per path. This is appropriate when usage patterns are stable over time but becomes stale when projects change — an old project with 20 sessions would outrank a new active project.

**Recency (pure)** ranks by the timestamp of the most recent interaction, regardless of how often the item has been accessed. This is what the current implementation uses: `max(updatedAt, lastMeaningfulOutput, createdAt)` per path, then sort descending.

**Frecency** (frequency + recency combined) is the algorithm used by Firefox's address bar (awesomebar), zsh's `z` plugin, and many IDEs. It combines a decay function on past accesses: items accessed frequently but not recently decay below items accessed recently even if only once.

A typical frecency formula:
```
score = visits / age_in_days^decay_factor
```
Or alternatively (z/fasd style):
```
score = rank * decay(last_access_time)
```

**What browsers and tools actually use:**

- **Firefox awesomebar**: Adaptive frecency — each visit adds `recency_bonus + type_bonus`, with a `0.975^days_old` decay multiplier applied to the total score. Pure recency wins for very fresh items; frequency catches things used regularly weeks ago.
- **Chrome omnibox**: Primarily frecency + typed-match boosting. Recent typed accesses heavily outrank old autonavigated ones.
- **VS Code "Quick Open" (Ctrl+P)**: Frecency within the current workspace session, but resets across window restarts. Recent opens in the current session dominate.
- **zsh history (built-in)**: Pure recency — `HISTFILE` is LIFO, Ctrl+R searches backward chronologically.
- **zsh `z` / `zoxide`**: Frecency — visits increment a rank, rank decays by time since last visit. This is the model most developers intuitively recognise from the shell.
- **JetBrains recent projects**: Pure recency — "Recent Projects" dialog is a straight reverse-chronological list.

### Recommendation for This Feature

For repository paths in `useRepositorySuggestions`, **pure recency is the correct choice** given:

1. **Dataset size**: A single developer managing ~10–50 sessions has a small, high-signal dataset where "what I touched most recently" is almost always "what I want now."
2. **No cross-session frequency signal**: Each `listSessions` call returns current sessions only, not a full history of deleted ones. Frequency counts from live sessions are misleading (an old 20-session project shouldn't beat a new 1-session project).
3. **Cognitive match**: Developers expect "what I just worked on" at the top, matching mental models from JetBrains recent projects and shell history.
4. **Implementation simplicity**: The current approach (max of three timestamps, sort descending) is correct and has no decay parameters to tune.

If session history were persisted across deletion, frecency would become worth considering. For now, pure recency is optimal.

### Current Implementation Assessment

The existing `useRepositorySuggestions.ts` implementation is sound:
- Deduplicates paths correctly using a `Map<string, number>`
- Picks the most active timestamp across three fields, which is a reasonable proxy for "last meaningful use of this path"
- Falls back to placeholder paths when no sessions exist — these placeholders are macOS-biased (`/Users/username/...`), which is a minor portability issue for Linux users (the running environment is Linux, per the env block)

---

## Duplicate vs. New Workspace UX Distinction

### The Design Problem

Two different intents can superficially look like the same action:
1. **Duplicate** — copy an existing item including its current state, name, and identity (spreadsheet "Copy Sheet", Figma "Duplicate Frame")
2. **New Workspace / Fork** — create a new item that shares the *context* (project path, tool configuration) of an existing item but starts with a fresh identity (empty title, new branch, clean slate)

If these are presented identically, users get confused about what they'll get. Design systems that handle both distinguish them through:

### How Design Systems Distinguish These

**Figma**: "Duplicate" (Cmd+D) clones everything including name (appended "Copy"). A new file on the same project is a separate affordance via the project sidebar.

**GitHub**: "Fork" (fork with fresh identity — new owner, new name, branches diverge) vs. "Use this template" (new repo derived from template, identity reset). The UI treats them as fundamentally different actions with different entry points.

**Notion**: "Duplicate page" copies all content and appends "(copy)" to the title. "New page" in the same space is a clean blank start. These are separate buttons.

**Linear**: "Duplicate issue" copies everything and shows "(copy)" suffix. A new issue in the same project is a separate action.

**Common pattern**: "Duplicate" preserves identity + state. "New from template / New Workspace" resets identity but inherits context. The visual distinction is usually:
- Duplicate: copy icon, appears in the item's context menu near other state-preserving operations
- New Workspace: "+" icon or a workspace/branch icon, often labeled with the destination concept ("New branch", "New worktree", "New workspace")

### Current Implementation Assessment

The current `onNewWorkspace` action in `SessionCard` correctly pre-fills `path`, `program`, and `category` (contextual inheritance) while clearing `title` and `branch` (identity reset). This maps precisely to the "New Workspace" pattern, not "Duplicate."

The risk is in **labeling and iconography**. If the button is labeled "Duplicate" or uses a copy icon (two overlapping squares), users may expect the title to be preserved. The button should use a label like "New Workspace" or "Fork to New Session" and a distinct icon (e.g., a branch icon or `+` with a folder). The requirements confirm the intent is "New Workspace, same project" — the implementation needs its surface label to match.

---

## react-hook-form: setValue vs register for Toggles

### The Context

`useTitleAsBranch` is a boolean field in the form schema. It is never rendered as a visible `<input type="checkbox">` in the new branch preview flow — it is a purely programmatic flag toggled by two `<button type="button">` elements via `setValue("useTitleAsBranch", …)`.

### Trade-off Analysis

**Option A: `setValue` only (current approach)**

```tsx
// Toggle on
<button onClick={() => setValue("useTitleAsBranch", false)}>Customize</button>

// Toggle off
<button onClick={() => setValue("useTitleAsBranch", true)}>Use session name instead</button>
```

The field is not registered with `register()` and has no corresponding DOM input. The value exists only in react-hook-form's internal store.

Pros:
- Clean JSX — no hidden `<input>` cluttering the DOM
- Direct, explicit intent — the buttons are the UI, the boolean is an implementation detail
- No hydration/SSR concerns with hidden inputs

Cons:
- The field value is not validated during `trigger()` calls unless explicitly included — but since `useTitleAsBranch` is handled as a cross-field refinement in the zod schema's `.refine()`, this is only relevant if `trigger(["useTitleAsBranch"])` is ever called explicitly (it is not in the current step validation)
- If the field is set via `setValue` without `{ shouldValidate: true }`, cross-field refinements won't re-run until the next validation pass (e.g., `handleSubmit`)
- `watch("useTitleAsBranch")` works correctly regardless — react-hook-form's store is updated synchronously by `setValue`

**Option B: `register` with a hidden input**

```tsx
<input type="hidden" {...register("useTitleAsBranch")} value={useTitleAsBranch ? "true" : "false"} />
```

Pros:
- Participates fully in form serialisation and native HTML form submission
- Triggers validation on change if `mode: "onChange"` is set

Cons:
- HTML `<input type="hidden">` cannot hold a boolean — it would need to be serialised as a string and then parsed back, which fights against the zod schema expecting `z.boolean()`
- A `<input type="checkbox" style={{display: 'none'}}>` is accessible-hostile and confuses screen readers
- Adds noise to the DOM with no user-visible benefit
- react-hook-form's `Controller` component with `render` prop is the idiomatic way to register non-standard inputs, not a hidden DOM node

**Option C: `Controller` with no rendered input**

```tsx
<Controller
  name="useTitleAsBranch"
  control={control}
  render={() => null}  // No rendered input
/>
```

This registers the field in react-hook-form's field array (enabling `trigger()` on it) while rendering nothing. The toggle buttons still call `setValue`. This is a niche pattern used when you need the field to participate in array-level validation but have no visual input.

### Recommendation

**The current `setValue`-only approach (Option A) is correct and idiomatic** for this use case. The reasons:

1. `useTitleAsBranch` is a derived UI state flag, not a user-entered value. Users never directly toggle it — the two affordances ("Customize" / "Use session name instead") are the real controls.
2. `watch("useTitleAsBranch")` in the component body picks up every `setValue` call synchronously, which drives the conditional rendering correctly.
3. The cross-field zod refinement fires at `handleSubmit` time, which is sufficient — there's no scenario where the branch validation needs to run mid-form-entry.
4. Adding `{ shouldValidate: true }` to the `setValue` calls would trigger cross-field refinement on each toggle if desired: `setValue("useTitleAsBranch", false, { shouldValidate: true })`.

The one concrete improvement to consider: pass `{ shouldDirty: true }` to `setValue` calls so the form correctly tracks whether the user has deviated from the default. This matters if the parent ever checks `formState.isDirty` to decide whether to show an "unsaved changes" warning before cancellation.

---

## Recommendations

Based on these findings, the current implementation is well-aligned with established patterns. The following targeted improvements are worth considering:

1. **Branch "Customize" button label/icon**: Add a visual cue (pencil icon or `▸` arrow) to the "Customize" button to signal it expands the field rather than navigating away. This eliminates the ambiguity between a link-style button and a navigation action.

2. **"New Workspace" affordance label**: Ensure the `SessionCard` action button uses "New Workspace" or "Fork" language with a branch/fork icon — not copy-icon/duplicate language — so users correctly predict what will be inherited vs. reset.

3. **Fallback path suggestions on Linux**: The placeholder paths in `useRepositorySuggestions.ts` use `/Users/username/...` (macOS convention). Since the application runs on Linux, change these to `/home/username/...` patterns, or better, derive the actual home directory from the `HOME` environment variable or a server-provided hint.

4. **`setValue` with `shouldDirty`**: Add `{ shouldDirty: true }` to the `setValue("useTitleAsBranch", …)` calls so `formState.isDirty` accurately reflects user interaction with the branch toggle.

5. **No frecency needed**: The current pure-recency sort in `useRepositorySuggestions.ts` is the right algorithm for this dataset. No frecency implementation is warranted until session history is persisted across deletions.

---

## Frecency Scoring

> **Status**: Implemented. The recommendation in the section above ("No frecency needed") was superseded once the implementation revealed that pure recency produces clearly wrong rankings in common workflows. The analysis and rationale below document the decision made during Phase 5.

### What Frecency Is

Frecency (a portmanteau of *frequency* and *recency*) ranks items by combining how often they have been accessed with how recently the last access occurred. Neither dimension alone is sufficient; frecency penalises stale-but-popular items and promotes items that are accessed regularly right now.

The concept was popularised by Mozilla's **Adaptive Places** system, introduced in Firefox 3 as the "Awesome Bar." Each URL in the Places database accumulates a frecency score that rises with typed visits and falls with the passage of time. The Awesome Bar's ability to surface the page you use every day — not just the one you visited two minutes ago — made it the defining feature of that Firefox release and established frecency as the standard model for personal-history ranking.

The same idea underlies `z` (the shell `cd` replacement), `zoxide`, VS Code's "recently opened" weighting, and JetBrains' project-switcher ranking.

### Why Pure Recency Was Insufficient

Pure recency (`max(updatedAt, lastMeaningfulOutput, createdAt)` per path, then sort descending) fails in the following scenario, which any active stapler-squad user encounters within a week:

- A developer runs 20 sessions on `/projects/client-app` across a two-week sprint. They then take a few days off that project.
- They open one session on `/projects/quickfix` yesterday.
- The autocomplete shows `/projects/quickfix` at the top because its timestamp is newer, even though the developer spends 95% of their time in `/projects/client-app`.

The "wrong" answer appears at the top every time until the developer re-opens a `/projects/client-app` session, creating a small but persistent friction for the most common case: returning to your main project after a brief detour.

### Why Pure Frequency Was Insufficient

Pure frequency (session count per path, sort descending) fails in the opposite direction:

- A project with 30 sessions from a completed engagement three months ago dominates the list forever, even though it has not been touched since.
- There is no way for new, actively-used paths to rise in the rankings without accumulating an impractical number of sessions.
- Deleting sessions would be the only way to "demote" a stale project, which is a poor user experience.

### The Algorithm: Half-Life Decay

The implemented algorithm is:

```
score = frequency × 0.5^(ageMs / halfLifeMs)
```

Where:
- `frequency` = number of current sessions whose `path` field matches this path
- `ageMs` = `now − mostRecentMs`, where `mostRecentMs` is the maximum of all non-zero activity timestamps (`updatedAt`, `lastMeaningfulOutput`, `createdAt`) across all sessions for this path
- `halfLifeMs` = 7 days (604,800,000 ms); injectable for testing

Implemented in `web-app/src/lib/utils/frecency.ts` as `computeFrecencyScore` and `rankPathsByFrecency`.

**Concrete examples with the 7-day half-life:**

| Scenario | frequency | age | score |
|---|---|---|---|
| 4 sessions, used today | 4 | 0 days | 4.00 |
| 4 sessions, used 7 days ago | 4 | 7 days | 2.00 |
| 8 sessions, used 7 days ago | 8 | 7 days | 4.00 (ties with 4 fresh sessions) |
| 1 session, used today | 1 | 0 days | 1.00 |
| 5 sessions, all used 21+ days ago | 5 | 21 days | 0.625 (loses to 1 fresh session) |

**Why half-life decay was chosen:**

1. **Intuitive mental model**: "The score halves every 7 days" is immediately graspable. Developers can reason about why a path moved up or down without reading documentation.
2. **No hyperparameter tuning**: The only parameter is the half-life period, and a single value (7 days) covers the most important rankings correctly without requiring per-user calibration.
3. **Bounded output**: The score is always in `[0, frequency]`. It never grows unboundedly (unlike additive visit-counting schemes), making rankings stable over time.
4. **Smooth decay**: There is no cliff — a path used 6 days ago is not suddenly worthless at day 7. The exponential curve degrades gracefully.

### Alternative Algorithms Considered

**zoxide / `z` style: `score = matches / (now - last_access)`**

zoxide uses a ranking formula where score rises with each visit and falls as time since last visit grows. This is unbounded: a path accessed 1 second ago gets an astronomically high score. It is also highly sensitive to accidental or one-off very-recent accesses — opening a path you don't care about right before opening the wizard would push it to the top. Additionally, it requires persisting a per-path visit counter across sessions, which stapler-squad does not currently have.

**Firefox Adaptive Frecency: multi-bucket weighted scoring**

Firefox awards different point values depending on how the URL was accessed (typed, bookmarked, linked, etc.) and applies different decay curves per bucket. This produces excellent results for a general-purpose browser history of tens of thousands of URLs but is entirely over-engineered for a personal tool managing tens of repository paths. The added complexity would make the algorithm opaque and the parameters unmaintainable.

**Linear decay: `score = frequency × max(0, 1 − age / window)`**

A linear falloff to zero over a fixed window (e.g., 30 days) is simpler but produces cliff-edge behaviour: a path used 29 days ago scores 1/30 of its initial value, and at day 30 it drops to exactly zero and disappears. This is jarring — a project you worked on a month ago is still a plausible destination; it should rank below a recent project, not be excluded entirely.

### The 7-Day Half-Life Rationale

Seven days was chosen to match the typical sprint/iteration cycle in software development:

- A project untouched for exactly one sprint (7 days) has its score halved relative to a path used today with the same session count. It is meaningfully de-ranked but still visible.
- A project untouched for two sprints (14 days) scores at 25% — clearly subordinate to anything touched recently, but not gone.
- A project untouched for a month (≈ 4.3 half-lives) scores at about 5% of its nominal frequency — effectively relegated to the bottom of any non-trivial list.

This maps well to how developers think about "current work" vs. "old work."

The half-life is injected as a parameter in both `computeFrecencyScore` and `rankPathsByFrecency`, so it can be changed or overridden in tests without modifying source.

### Trade-offs and Known Limitations

**Frequency data is bounded by current live sessions.** The `listSessions` RPC returns only sessions that currently exist. Deleted sessions do not contribute to a path's frequency count. This means:

- Deleting sessions on a path reduces its frecency score even if you plan to return to that path.
- A developer who aggressively cleans up finished sessions will see their active projects rank lower than a developer who lets sessions accumulate.
- There is no "ghost frequency" from historical sessions — only what is live today is counted.

This is an accepted limitation given the "no new dependencies, no schema changes" constraint from the requirements. If session access history were persisted independently of session lifetime, the frequency signal would become far more reliable.

**The most-recent timestamp is taken across all live sessions for a path.** If you have three old sessions and one new one for the same path, the new one's timestamp is used. This is correct behaviour — the path was recently active — but it means a single recently-created session can "rescue" an otherwise-stale path's ranking even if the older sessions represent finished work.
