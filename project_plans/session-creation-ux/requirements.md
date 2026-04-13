# Requirements: Session Creation UX

**Status**: Draft | **Phase**: 1 — Ideation complete
**Created**: 2026-04-12

## Problem Statement

Creating a second (or third) session on the same project is harder than it should be. Users frequently work on the same repository across multiple concurrent sessions, but the new-session wizard doesn't surface recently-used paths prominently, the duplicate/fork actions don't have a dedicated "new workspace, same project" shortcut, and the branch-naming field adds friction through an unintuitive opt-in checkbox.

Primary users: developers running stapler-squad to manage multiple Claude Code sessions on one or more git repos.

## Success Criteria

- Opening the new-session wizard shows the most-recently-used repository path at the top of autocomplete suggestions, with no extra steps
- A single-click "New Workspace" action on any session card opens the wizard pre-filled with that session's path, program, and category — requiring only a title before submission
- The branch field in the wizard defaults to the session title without the user touching any checkbox; customisation is an explicit opt-in

## Scope

### Must Have (MoSCoW)

- Repository path suggestions sorted by most-recently-used (not alphabetically)
- "New Workspace" action on session cards that pre-fills path/program/category and leaves title/branch empty
- Branch field defaults to title-as-branch with no visible disabled input; a "Customize" link reveals manual entry

### Should Have

- "New Workspace" also accessible from the duplicate/fork flow (same wizard path, different intent)
- `useTitleAsBranch` state resets to `true` when switching back from custom branch via "Use session name instead"

### Out of Scope

- Changing the underlying branch-creation logic in the Go backend
- Adding a "recent projects" panel separate from the existing path autocomplete
- Persistent user-level path favourites or pinning

## Constraints

- **Tech stack**: React (Next.js app router), react-hook-form + zod, ConnectRPC, Go backend — no new dependencies
- **Timeline**: Shipped within the current branch (`stapler-squad-recent-search-history`)
- **Dependencies**: `useRepositorySuggestions` hook reads from `listSessions` RPC; `SessionWizard` form state uses `defaultValues` + `setValue`

## Context

### Existing Work

All three changes were implemented in this session:

1. `web-app/src/lib/hooks/useRepositorySuggestions.ts` — changed from alphabetical sort to sort-by-recency using `updatedAt`, `lastMeaningfulOutput`, and `createdAt` timestamps
2. `web-app/src/components/sessions/SessionCard.tsx` / `SessionList.tsx` / `web-app/src/app/page.tsx` — added `onNewWorkspace` prop chain and `handleNewWorkspaceSession` handler that pre-fills path/program/category but clears title/branch
3. `web-app/src/components/sessions/SessionWizard.tsx` + `SessionWizard.module.css` — replaced checkbox+disabled-input pattern with branch-preview row ("Customize" link) and "Use session name instead" link; `useTitleAsBranch: true` is the silent default

TypeScript compiled clean after all changes.

### Stakeholders

- Tyler (sole developer/user) — primary reporter of the friction points
- Any future stapler-squad users who manage multi-session workflows on a single repository

## Research Dimensions Needed

- [ ] Stack — evaluate technology options (N/A — implementation already chosen and complete)
- [ ] Features — survey comparable tools/approaches (CI: how do JetBrains Fleet, VS Code workspaces, tmuxinator handle same-project multi-session)
- [ ] Architecture — design patterns and tradeoffs (form UX patterns for progressive disclosure; autocomplete ranking strategies)
- [ ] Pitfalls — known failure modes and risks (stale suggestions after session deletion; useTitleAsBranch reset edge cases in duplicate flow)
