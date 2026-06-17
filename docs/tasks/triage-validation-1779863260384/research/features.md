# Triage Pipeline Features & Patterns Research

## Overview

The stapler-squad codebase implements a sophisticated **backlog item triage pipeline** that drives pre-implementation AI planning for features and bug fixes. This document synthesizes end-to-end pipeline architecture, existing patterns, and the bugs that were recently fixed.

---

## 1. End-to-End Triage Pipeline Flow

### High-Level Sequence

1. **Trigger**: `CreateBacklogItem` RPC or manual `TriggerTriage` call
2. **Spawn**: One-shot triage session created with AI agent + specialized prompt
3. **Research**: Claude runs 4 parallel research subagents writing markdown files
4. **Synthesis**: Claude synthesizes research into plan.md and validation.md
5. **Submit**: Claude calls MCP `submit_triage_result` tool with item_id, summary, suggestions, tasks
6. **Record**: Triage result JSON persisted on ItemSession; status event logged
7. **Notify**: Operator UI shows triage review panel with suggestions and task checklist

### Status Transitions During Triage

- Item starts in `idea` status
- When `TriggerTriage` re-triggers on an existing `ready` item, item moves back to `idea`
- When triage session exits, status remains in `idea` (unlike work sessions which move to `review`)
- Operator manually advances item to `ready` via UI after reviewing triage results

---

## 2. Backlog/Triage Session Creation Flow

### Key Files Involved

- **`server/services/backlog_service.go`** → `TriggerTriage()` RPC handler
- **`server/services/session_service.go`** → Session lifecycle & MCP context injection
- **`session/backlog_lifecycle.go`** → Lifecycle listener (records start/end times)
- **`session/backlog_context.go`** → Prompt building (token-budgeted)
- **`session/backlog_commands.go`** → Slash command & context file generation

### Triage Session Creation (`TriggerTriage`)

```
TriggerTriage(ctx, itemID)
  ├─ Load BacklogItem
  ├─ Validate status in {idea, ready}
  ├─ Validate repo_path set
  ├─ Guard: Check for existing live triage sessions (orphan-aware)
  │  └─ If item status advanced or session not live → tombstone old session
  ├─ Move ready items back to idea
  ├─ Create artifact directory: docs/tasks/<slug>/research/
  ├─ Kill any stale tmux session by title "triage:<slug>"
  ├─ Build triage prompt (calls buildTriagePrompt)
  ├─ Spawn one-shot session: CreateDirectorySession(title, repo, prompt, tags, oneShot=true)
  ├─ Create ItemSession(itemID, sessionUUID, role=triage)
  └─ Return ItemSession proto
```

### Critical: Artifact Directory Structure

```
docs/tasks/<slug>/
  ├─ research/
  │  ├─ stack.md        (tech stack research)
  │  ├─ features.md     (existing feature patterns)
  │  ├─ architecture.md (proposed architecture)
  │  └─ pitfalls.md     (known risks)
  ├─ plan.md            (synthesis: approach, tasks, blockers)
  └─ validation.md      (test plan, edge cases)
```

---

## 3. Prompt Construction & Delivery Patterns

### Triage Prompt (buildTriagePrompt)

Generated in `backlog_service.go:buildTriagePrompt()` and includes:

1. **Item Data**: Title, description, acceptance criteria, item_id
2. **Task Instructions**: 5-step workflow
   - Step 1: Research (4 parallel subagents, write research/*.md)
   - Step 2: Synthesis (write plan.md)
   - Step 3: Validation (write validation.md)
   - Step 4: Submit (call submit_triage_result MCP tool)
   - Step 5: Clarifying Questions (optional, rationale="question")
3. **Artifact Paths**: Absolute paths (enables os.Stat validation)

**Critical Detail**: Prompt is mapped to `Prompt` field (positional CLI arg), NOT `AppendSystemPrompt` (system context only).

### Session-Linked Work Prompt (BuildTokenBudgetedPrompt)

For work sessions spawned from ready items, includes:

- BACKLOG_ITEM_DATA envelope (inert data marker)
- Item title, description, acceptance criteria
- Prior attempts (ended sessions with verdicts)
- Task protocol block (slash command instructions)

**Token Budget**: ~4000 tokens max (len(output)/4 estimation)
- Pass 1: Drop prior sessions if over budget
- Pass 2: Truncate description to 500 chars if still over

### Context File Generation

**`.backlog-context.md`** written to worktree root on session spawn:

```
[Full BuildSessionInitialPrompt output]

## Fallback Instructions
If MCP tools are unavailable, continue using acceptance criteria above.
Record completed criteria in commit messages. Run git commit after each criterion.
```

**Slash Commands** written to `.claude/commands/backlog/`:

- `status.md` → calls get_backlog_item
- `done-N.md` → calls report_progress with status=pass
- `fail-N.md` → calls report_progress with status=fail
- `review.md` → calls request_review
- `help.md` → lists all available commands

---

## 4. Submit Triage Result MCP Tool

### File: `server/mcp/tools_backlog.go`

Implements handler `submitTriageResult()` which:

#### Validation

1. Verifies STAPLER_SESSION_UUID environment variable set
2. Validates item_id is valid UUID
3. Checks session is linked to item with role=triage
4. Validates summary is non-empty

#### Input Parsing

- **suggestions** array: `{text, rationale}` (rationale="question" for clarifying questions)
- **tasks** array: `{text, estimate, category}` (capped at 12 items)
  - Categories: backend | frontend | test | infra | docs
  - Estimate format: "2h", "30m", "1d"
- **plan_artifact_path**: Absolute path to docs/tasks/<slug>

#### Persisting Results

1. Serialize triage result to canonical JSON struct:
   ```json
   {
     "summary": "...",
     "suggestions": [...],
     "tasks": [...]
   }
   ```

2. Persist on ItemSession.TriageResult field (JSON string)

3. Update BacklogItem.PlanArtifactsPath if provided

#### EventBus Notification

If EventBus wired, publish `NOTIFICATION_TYPE_INPUT_REQUIRED` event:
- Message: "{ItemTitle} — {N} suggestion(s). Click to review."
- Metadata: {"item_id": itemID}

#### Return

Success message: "Triage result submitted for item {ID}. {N} suggestion(s) recorded."

---

## 5. Recent Triage Pipeline Fixes (Commit 19ef4431)

### The Three Bugs That Broke Triage

#### Bug #1: Prompt Injection Failure

**Problem**: Triage prompt passed via `AppendSystemPrompt` (→ `--append-system-prompt` flag, system context only). Claude waits for a user message before starting work.

**Symptom**: Session would start but never process the prompt; Claude would sit idle waiting for input.

**Fix**: Map prompt to `Prompt` field instead (→ positional CLI argument, user context).

**File**: `session/backlog_lifecycle.go` changed to use correct field.

#### Bug #2: Session Exit Not Recorded (EndedAt Never Set)

**Problem**: In `session_service.go:onSessionExited()`, the guard `if is.SessionRole != SessionRoleWork { return }` was placed AFTER checking endedAt but BEFORE calling `UpdateItemSessionEnded()`. Triage/review sessions would return early without setting endedAt.

**Symptom**: UI remained stuck on "loading" state forever because endedAt was nil.

**Fix**: Move `UpdateItemSessionEnded()` call BEFORE the role guard so all session types (triage, work, review) record their end time.

**File**: `session_service.go:onSessionExited()` — reorder guard.

#### Bug #3: OneShot Flag Ignored

**Problem**: OneShot=true was set on triage sessions but `buildLaunchCommand` never added the `-p` (print/non-interactive) flag based on OneShot.

**Symptom**: After Claude finished writing triage results, the session stayed in interactive mode instead of exiting.

**Fix**: Add conditional to buildLaunchCommand: if OneShot, append `-p` flag.

**File**: `session/instance_tmux.go` — add OneShot flag handling.

#### Bug #4: Test Double-Trigger Guard Was Flaky

**Problem**: `TestTriggerTriage_DoubleTriggerGuard` passed nil sessionStopper + unstarted session, both triggering orphan path. Test would randomly pass/fail.

**Fix**: Wire mock stopper that reports session live; mark session as started.

**File**: `server/services/backlog_service_test.go` — fix test setup.

---

## 6. Existing Tests for Triage Pipeline

### Unit Tests

#### MCP Tool Tests (`server/mcp/tools_backlog_test.go`)

- `TestReportProgress_RejectsWhenNoSessionUUID` — validates PERMISSION_DENIED without session context
- `TestReportProgress_RejectsWhenSessionNotLinkedToItem` — validates session-item linking guard

#### Service Tests (`server/services/backlog_service_test.go`)

- `TestTriggerTriage_DoubleTriggerGuard` — ensures re-trigger guards work (updated in fix commit)

#### Backlog Integration Tests (`session/backlog_integration_test.go`)

- Tests for item session creation, status transitions

#### Lifecycle Tests (`session/backlog_lifecycle_test.go`)

- Tests for onSessionStarted, onSessionExited handlers

### E2E Tests

#### Playwright (`tests/e2e/triage-pipeline-validation.spec.ts`)

**Feature**: `@feature backlog:triage`

**Test**: `e2e:triage-pipeline - triage starts, receives prompt, completes, shows review panel`

**Preconditions**:
- TRIAGE_VALIDATION=true environment variable
- TRIAGE_REPO_PATH (defaults to stapler-squad repo)
- Live server running on TEST_SERVER_URL

**Flow**:
1. Create new backlog item with title, description, repo path
2. Trigger triage (or confirm auto-triggered from CreateBacklogItem)
3. Wait for loading indicator (session started, prompt injected)
4. Wait for triage-review-panel (session completed, results submitted)
5. Verify summary text present and > 10 characters

**Timeout**: 6 minutes (real Claude triage time)

### Feature Registry

File: `docs/registry/features/backend/backlog/trigger-triage.json`

```json
{
  "id": "backlog:trigger-triage",
  "type": "backend",
  "service": "BacklogService",
  "method": "TriggerTriage",
  "protoFile": "proto/session/v1/backlog.proto",
  "markerFound": true,
  "handlerFile": "server/services/backlog_service.go",
  "tested": false,
  "testIds": [],
  "lastModified": "2026-05-18T08:01:05.099337344-07:00"
}
```

Note: `tested: false` — indicates no automated unit test coverage linked yet.

---

## 7. Patterns to Reuse

### 1. **Status Machine Guards**

Pattern: Use `BacklogItemTransitionInput` + `TransitionGuard()` to validate business rules before status changes.

```go
guardInput := session.BacklogItemTransitionInput{
  Status:            from,
  AcCriteriaJSON:    item.AcceptanceCriteria,
  PlanApproved:      item.PlanApproved,
  SkipPlanning:      item.SkipPlanning,
  PlanArtifactsPath: item.PlanArtifactsPath,
  OverallOutcome:    overallOutcome,
  OverrideReason:    req.Msg.OverrideReason,
}
if guardErr := s.engine.ValidateGates(guardInput, to); guardErr != nil {
  // return error
}
```

**Reuse**: Apply same pattern for custom transitions or new gates.

### 2. **Orphan Session Detection**

Pattern: Check session.StartedAt == nil (never confirmed running) OR sessionStopper.IsSessionLive() == false to detect orphaned sessions before re-triggering.

```go
neverStarted := is.StartedAt == nil
notLive := neverStarted || s.sessionStopper == nil || !s.sessionStopper.IsSessionLive(is.SessionUUID)
statusAdvanced := item.Status != string(session.BacklogStatusIdea)
if notLive || statusAdvanced {
  // tombstone old session
}
```

**Reuse**: Apply when re-triggering any workflow (re-review, re-triage, etc.).

### 3. **Token-Budgeted Prompts**

Pattern: Call `BuildTokenBudgetedPrompt()` which estimates tokens (len(output)/4) and truncates in two passes:
- Pass 1: Drop prior sessions if over 4000 tokens
- Pass 2: Truncate description to 500 chars

```go
prompt := session.BuildTokenBudgetedPrompt(entItem, priorSessions)
```

**Reuse**: Use for any agent prompt that could exceed context windows.

### 4. **Mutex-Protected Context File Writes**

Pattern: Serialize concurrent `.backlog-context.md` writes to same worktree using `worktreeMu.Lock()`:

```go
s.worktreeMu.Lock()
if wErr := session.WriteBacklogContextFile(entItem, worktreePath); wErr != nil {
  s.worktreeMu.Unlock()
  return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("WriteBacklogContextFile: %w", wErr))
}
s.worktreeMu.Unlock()
```

**Reuse**: Use for any session context file writing to prevent interleaved writes.

### 5. **One-Shot Session Spawning**

Pattern: Pass `oneShot=true` when spawning triage/review sessions so they auto-exit after completion.

```go
inst, err := s.sessionCreator.CreateDirectorySession(ctx, title, item.RepoPath, triagePrompt,
  []string{"backlog:triage"}, true) // oneShot=true
```

**Fix Applied**: Ensure `buildLaunchCommand` includes `-p` flag when `OneShot=true`.

**Reuse**: Use for any short-lived planning/review workflow.

### 6. **Session Role-Aware MCP Tool Guards**

Pattern: Check session role at tool invocation to prevent cross-role calls:

```go
if itemSession.SessionRole != "triage" {
  return errResult(ErrPermissionDenied, 
    fmt.Sprintf("session role is %q — only 'triage' role may submit triage results", itemSession.SessionRole), "")
}
```

**Reuse**: Apply to all MCP tools that should only be callable from specific session roles.

### 7. **Lifecycle Event-Driven State Transitions**

Pattern: Register `BacklogLifecycleListener` on sessions to drive state machine transitions on session exit.

```go
listener := session.NewBacklogLifecycleListenerWithSpawner(storage, spawner, engine)
listener.WireToInstance(inst)
```

**Flow**:
- `EventExited` → `onSessionExited()` → sets endedAt, may trigger in_progress→review transition

**Reuse**: Use for any session that drives downstream workflows (review gates, escalations).

---

## 8. What the "Triage Pipeline Fix" Likely Refers To

Based on commit history and test validation, "triage pipeline fix" encompasses:

1. **Prompt Injection Fix** (Bug #1): Prompt must reach Claude's user context, not just system context
2. **Session Exit Tracking** (Bug #2): endedAt must be recorded for ALL session roles
3. **Interactive Mode Exit** (Bug #3): OneShot=true must add `-p` flag to shell
4. **Test Reliability** (Bug #4): Tests must account for orphan session detection

These three bugs caused **triage sessions to start but never complete**:
- Claude received the prompt but sat idle waiting for user input (Bug #1)
- Even if Claude worked and submitted results, UI stayed "loading" (Bug #2)
- Session never exited, Claude waited for input indefinitely (Bug #3)

The fix ensures:
✅ Claude receives prompt in user context (starts working immediately)
✅ Session exit recorded immediately (UI shows completion)
✅ Non-interactive mode enabled (session exits after work completes)
✅ Test coverage validates end-to-end flow

---

## 9. Summary: Key Takeaways for Implementation

### Architectural Decisions

1. **One-Shot Sessions**: Triage/review workflows use one-shot sessions that auto-exit
2. **Async Lifecycle Events**: Session exit triggers async state machine transitions
3. **Role-Based MCP Guards**: Tools restricted to specific session roles (triage, work, review)
4. **Artifact Directories**: Pre-created on trigger; agent writes structured markdown files
5. **Token-Budgeted Prompts**: Prompts trimmed to fit within token budgets

### Common Pitfalls

1. Don't pass prompts to system context (AppendSystemPrompt) — use Prompt field
2. Always record session.EndedAt before checking session role
3. Remember to add `-p` flag when OneShot=true
4. Use mutex for concurrent writes to shared worktree files
5. Check session.StartedAt == nil for "never confirmed" sessions

### Testing Strategy

1. Unit tests: MCP tool guards, role validation, precondition checks
2. Integration tests: Artifact directory creation, file writes, lifecycle transitions
3. E2E tests: Full pipeline validation with real Claude (gated behind env var)

