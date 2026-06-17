# Triage Pipeline Features & Validation Patterns

## Overview
The triage pipeline (commit 19ef4431) is a one-shot workflow: Claude receives a backlog item prompt, performs triage analysis, writes result files to disk, then calls `submit_triage_result` MCP tool to record findings.

## The Three Bugs Fixed (19ef4431)

### 1. Prompt Injection Bug
**Problem**: Triage prompt was passed via `AppendSystemPrompt` (system context only), forcing Claude to wait for user input.
**Fix**: Mapped prompt to `Prompt` field → positional CLI arg (`claude "<prompt>"`).
**Impact**: Claude now receives the prompt immediately and starts triage work without waiting.

### 2. Session Lifecycle Bug  
**Problem**: `onSessionExited` had early return for non-work roles before calling `UpdateItemSessionEnded`, leaving triage sessions with `ended_at=NULL`.
**Fix**: Moved `UpdateItemSessionEnded` before the role guard (line 109–113 in backlog_lifecycle.go).
**Impact**: UI no longer stuck "running" — triage sessions properly close when Claude exits.

### 3. OneShot Flag Bug
**Problem**: `OneShot=true` set on triage sessions but `buildLaunchCommand` never added `-p` (print/non-interactive) flag.
**Fix**: Added conditional: `if i.OneShot && strings.Contains(program, "claude") { program = program + " -p" }` (instance_tmux.go:46–48).
**Impact**: Claude exits after finishing (print mode) instead of staying interactive.

## Triage Session Lifecycle

### Creation: TriggerTriage RPC
- Validates item status (must be "idea" or "ready")
- Requires repo_path (artifact directory path)
- Builds triage prompt using `buildTriagePrompt` (backlog_service.go:1188) with:
  - Item title, description, acceptance criteria
  - `item_id` for `submit_triage_result` call
  - Artifact directory path
- **Spawns one-shot session**: `CreateDirectorySession(title, repoPath, prompt, tags, oneShot=true)`
- Creates ItemSession with `SessionRole="triage"`

### Execution: Claude Agent
1. Receives full triage prompt as positional argument (not system prompt)
2. Researches item by writing markdown files to artifact directory:
   - `docs/tasks/<slug>/research/*.md` (stack, architecture, pitfalls, features)
   - `docs/tasks/<slug>/plan.md`
   - `docs/tasks/<slug>/validation.md`
3. Calls `submit_triage_result` MCP tool with:
   - `item_id` (must match ItemSession.item_id)
   - `summary` (triage analysis overview)
   - `suggestions` (array of {text, rationale})
   - `tasks` (optional array of {text, estimate, category})
   - `plan_artifact_path` (absolute path to docs/tasks/<slug>)

### Completion: MCP Handler
- `submitTriageResult` (tools_backlog.go:402–536) validates:
  - Caller session is linked to item via ItemSession with `session_role="triage"`
  - Serializes suggestions/tasks to JSON
  - Stores in ItemSession.triage_result
  - Updates BacklogItem.plan_artifacts_path if provided
  - Publishes "Triage complete" notification via EventBus

### Lifecycle Close: BacklogLifecycleListener
- When session exits, `onSessionExited` fires (backlog_lifecycle.go:97–153)
- Sets ItemSession.ended_at (fixes bug #2)
- **Non-work sessions stop here** — no status transition
- Review gate only spawned for work sessions (SessionRole="work")

## Expected Behavior After Fix

| Stage | Observable State |
|-------|------------------|
| **Trigger triage** | Loading indicator appears; ItemSession.started_at set |
| **Claude runs** | Session stays open; Claude runs with `-p` flag |
| **submit_triage_result called** | ItemSession.triage_result filled; UI shows review panel |
| **Session exits** | ItemSession.ended_at set; UI hides loading indicator |

## Validation Pattern (E2E Test)

`tests/e2e/triage-pipeline-validation.spec.ts` demonstrates expected flow:
1. Create item with repo_path = real stapler-squad repo
2. Click "Trigger Triage"
3. Assert loading indicator visible within 30s (prompt received)
4. Assert triage-review-panel visible within 5min (results submitted)
5. Assert summary text present (non-empty findings)

Key assertions:
- Trigger button disappears (session started)
- Review panel appears (triage complete)
- Summary text exists (findings recorded)

## Integration Points

- **Session creation**: `SessionCreator.CreateDirectorySession` (Prompt field now used)
- **MCP tool**: `submit_triage_result` (validates session role, persists result)
- **Lifecycle**: `BacklogLifecycleListener.onSessionExited` (sets ended_at for all roles)
- **CLI flags**: `-p` (print/exit after), `Prompt` (positional arg, not system prompt)
