# Research: Features — Comparable Tools

**Dimension**: Features
**Date**: 2026-04-12

## Summary

Across the tmux session-manager ecosystem, IDEs, and git-worktree-aware tools, a consistent pattern emerges: the most effective tools surface recently- or frequently-used project paths at the top of a selection list (via zoxide, MRU lists, or recency-sorted history) and allow a single action to create a new session pre-seeded with a known project root. No tool surveyed offers a dedicated "new workspace from existing" shortcut equivalent to stapler-squad's proposed "New Workspace" card action — most require the user to re-select the path manually. GitHub Codespaces has explicitly documented the UX problem of same-repository sessions being indistinguishable in the Recent list, confirming that per-session labelling (title/branch) is important. Git-worktree-aware tools (lazygit, LazyWorktree, GitLens) treat each worktree as a first-class isolated context but leave session/terminal management to the user, creating a gap that stapler-squad's design directly fills.

---

## Tool Survey

### tmuxinator

- **Multi-session / same-project workflow**: tmuxinator maps one YAML config file to one named tmux session. Launching the same config a second time re-attaches to the existing session instead of creating a new one. Users who want two sessions from the same config must manually rename sessions or maintain duplicate config files — there is no built-in "start another instance of this project" command. GitHub issue #206 ("Use same project config to open multiple sessions") has been open since 2014 with no resolution.
- **Recent/favourite projects UX**: No built-in recent-project list. Discovery of projects is entirely file-system-based (YAML files in `~/.tmuxinator/`). Integrators layer fzf on top for fuzzy selection.
- **Relevance**: Validates the pain point. The lack of "open a second session from the same template" is a known, unsolved gap in the tmux ecosystem that stapler-squad's "New Workspace" action directly addresses.

---

### sesh / tmux-session-wizard / t-smart-tmux-session-manager

These tools represent the modern tmux session-manager pattern (fzf + zoxide).

- **Multi-session / same-project workflow**: The canonical UX is a popup (bound to `prefix+T`) that merges two lists: (1) existing live sessions and (2) recently-visited directories ranked by zoxide's frequency+recency score. Selecting a directory that already has a session switches to it; selecting one that does not creates a new session rooted there. This means you can create a second session for a project by navigating to the same directory entry again — but the tool does not distinguish "open session on this dir" vs "new session on this dir."
- **Recent/favourite projects UX**: Ranking is delegated entirely to zoxide, which scores directories by a weighted combination of frequency and recency of `cd` visits. This is the most prominent real-world implementation of MRU-ranked path suggestions.
- **Relevance**: The zoxide ranking model (recency + frequency weighted score) is the industry reference for "show me the most relevant project paths first." Our `useRepositorySuggestions` hook's timestamp-based sort (updatedAt → lastMeaningfulOutput → createdAt) is the session-aware equivalent — using session activity as the recency signal rather than filesystem navigation.

---

### VS Code "Open Recent"

- **Multi-session / same-project workflow**: Every VS Code window is an independent workspace. Opening the same folder in a second window is a first-class operation: `File > New Window` then `File > Open Recent` (or `Cmd+Shift+N` followed by `Cmd+R`). The "Open Recent" list is a flat MRU list of folders and `.code-workspace` files, sorted by last-opened time. `.code-workspace` files get a "(Workspace)" suffix label so users can tell them apart from plain folders.
- **Recent/favourite projects UX**: The `Ctrl+R` / `Cmd+R` quick-open recent picker is the canonical entry point. It is purely recency-sorted, no frequency weighting.
- **New workspace from existing**: No shortcut. Users must open a new window and re-navigate to the path. There is no "clone this window's workspace" action.
- **Relevance**: VS Code's recency-only MRU list for the path picker is the mental model most developers carry. Our autocomplete improvement (sort by session recency) matches this expectation. The absence of a "duplicate workspace" shortcut in VS Code confirms the gap our "New Workspace" card action fills.

---

### JetBrains Fleet (discontinued December 2025)

- **Multi-session / same-project workflow**: Fleet's workspace = a folder. Opening the same folder again re-opens into the same shared collaborative workspace; there was no distinct "open a second independent session on this folder" flow because Fleet's model was one shared environment per folder, not per-user-session.
- **Recent/favourite projects UX**: Fleet maintained a "Recent" workspaces list on its home screen. Community feedback noted the absence of a dedicated workspace manager (pin, remove, organise favourites) — users wanted more than just a flat recency list.
- **Relevance**: Fleet's model was fundamentally different (collaboration-first, single shared state per folder), so its UX decisions don't transfer directly. The community request for "more than a flat recent list" is worth noting as a future direction but is out of scope here.

---

### Zed Editor

- **Multi-session / same-project workflow**: Each Zed window corresponds to exactly one project. Opening a second window on the same project is unsupported in the UI — attempting it switches focus to the existing window. The only workaround is the CLI flag `zed --new path/to/project`. This is an actively requested feature (issue #22338).
- **Recent/favourite projects UX**: `Cmd+Alt+O` opens a recent-projects picker. Holding `Cmd` while selecting opens the project in a new window instead of the current one — a modifier-key "open in new context" pattern.
- **Relevance**: Zed's modifier-key variant ("hold Cmd to open in new window") is an interesting progressive-disclosure pattern. It shows the field hasn't converged on a standard; dedicated UI (like a "New Workspace" button) is more discoverable than a modifier key.

---

### lazygit

- **Worktree / multi-session UX**: lazygit added a Worktrees tab (accessible from the Branches view via `w`). Creating a worktree from a branch switches the working directory to the new worktree path. Session/terminal management is left to the user — lazygit does not create or manage tmux sessions.
- **Default naming**: When creating a worktree from a local branch, the worktree directory defaults to the branch name. This matches our requirement that the branch field in the wizard defaults to the session title.
- **Relevance**: Confirms the "branch name = worktree name" convention. Our branch-field defaulting to title mirrors what lazygit proposes as the sensible default in its own worktree-creation flow.

---

### LazyWorktree

- **Worktree / multi-session UX**: LazyWorktree is a keyboard-first TUI that treats isolated worktrees as the primary unit of work. Each task/branch gets its own worktree, and the tool manages associated tmux or zellij sessions, notes, and CI status per worktree. Creating a new task = create a new worktree = create a new session, all in one action.
- **Recent/favourite projects UX**: Navigation is within the repository; there is no cross-repository recent-projects list.
- **Relevance**: LazyWorktree most closely mirrors stapler-squad's architecture (worktree = session). Its single-action "new task creates worktree + session" is the direct analogue of our "New Workspace" card action. The key insight: the creation flow defaults everything inferrable (repo, program, base branch) and only asks for the one piece of context that is new (task name / branch name).

---

### GitKraken Workspaces / GitLens Worktrees

- **Worktree / multi-session UX**: GitKraken Workspaces are about grouping multiple repositories into a dashboard view (not about multiple sessions on one repo). GitLens Worktrees (VS Code extension) surfaces a Worktrees panel in the sidebar listing all worktrees of the current repo with status (current branch, modified files). Creating a new worktree is a palette command that prompts for branch name.
- **Recent/favourite projects UX**: GitKraken has a Cloud Workspaces home screen showing saved workspace groups, not a recent-path picker.
- **Relevance**: GitLens's Worktrees panel (one entry per worktree, all in the same repo) provides a model for a future "sessions on this repo" grouping view. Not directly applicable to the current scope.

---

### GitHub Codespaces

- **Multi-session / same-repository workflow**: Users can create multiple Codespaces for the same repository and branch. The creation wizard (advanced flow) prompts for branch, region, machine type, and dev container configuration. Each Codespace gets a randomly generated name.
- **Recent/favourite projects UX**: VS Code's Getting Started page shows a "Recent" list. The documented UX problem: multiple Codespaces for the same repo appear with identical labels (just the repo name), making disambiguation impossible. GitHub has not yet resolved this with meaningful per-codespace labels.
- **Relevance**: This is a direct cautionary example. When multiple sessions share the same project path, the differentiating signal must be visible in the list item — for us, the session title and branch name are that signal. Our "New Workspace" flow leaves title and branch blank precisely so the user is forced to supply a unique name before submission.

---

## Patterns Worth Adopting

- **Recency-ranked path suggestions as the primary discovery mechanism** (VS Code Open Recent, sesh+zoxide, tmux-session-wizard): MRU ordering for path autocomplete is the established convention. Our `useRepositorySuggestions` recency sort is aligned with the field.

- **Existing sessions surface at the top, new-session creation is a secondary action** (sesh, tmux-session-wizard): Show what the user most likely wants (attach to running session) before offering to create a new one. Our autocomplete could visually separate "active sessions on this path" from "create new session here."

- **Default all inferrable fields; ask only for what is novel** (LazyWorktree, lazygit worktree creation): When creating from an existing context, pre-fill path, program, category, and branch prefix — require only the title. This is exactly what our "New Workspace" pre-fill strategy implements.

- **Branch name defaults to the task/feature identifier** (lazygit, LazyWorktree): In both tools the branch name tracks the task name by default. Our `useTitleAsBranch: true` default is consistent with this convention.

- **Progressive disclosure for branch customization** (general pattern): Rather than showing a disabled input with a checkbox, show a preview and a "Customize" link. This matches the Zed modifier-key philosophy (simpler default, explicit opt-in for advanced behavior) applied to form UX.

- **Per-session distinguishing label is mandatory when multiple sessions share a path** (GitHub Codespaces anti-pattern): Require a non-empty, unique title before the wizard can be submitted when a session with the same path already exists, or at minimum display a warning.

---

## Gaps / Differentiators

| Area | Field behavior | stapler-squad approach | Assessment |
|---|---|---|---|
| "New session from existing" shortcut | No tool surveyed has a dedicated per-session-card action for this | "New Workspace" button on session cards | **Intentional differentiator** — stapler-squad is ahead of the field here |
| Session disambiguation in recents | GitHub Codespaces has an open bug; VS Code shows "(Workspace)" suffix | Session title + branch shown on card | **Already solved** in our design; no gap |
| Branch-name defaulting | lazygit/LazyWorktree default to branch=task-name | `useTitleAsBranch: true` silently, "Customize" for override | **Aligned** with industry convention |
| MRU path ranking | All major tools use recency (some add frequency via zoxide) | Recency-only via session timestamps | **Minor gap**: no frequency weighting. Out of scope for this iteration but worth revisiting if users have many low-activity old sessions |
| Cross-repository recent list | sesh+zoxide, VS Code Open Recent provide cross-repo recency | Filtered to sessions the user has already created | **Intentional scope limitation** (requirements mark a separate "recent projects panel" as out of scope) |
| Worktree-aware session grouping | GitLens shows all worktrees for one repo in a panel | Sessions are flat-listed; worktree relationship implicit via path | **Gap worth tracking**: a future "sessions on this repo" grouped view would mirror GitLens/LazyWorktree, but out of scope for this iteration |
