# Validation — Triage Pipeline Validation

## Acceptance Criteria → Test Mapping

### AC1: Claude receives the triage prompt

| Test Type | Test Location | Assertion |
|-----------|-------------|----------|
| Unit | `session/instance_tmux_test.go` | `buildLaunchCommand()` with `OneShot=true` and non-empty `Prompt` produces `claude -p "<prompt>"` |
| Unit | `server/services/backlog_service_test.go` | `TriggerTriage()` calls `CreateDirectorySession` with non-empty `prompt` arg |
| Integration | `server/services/session_service_test.go` | `CreateDirectorySession` maps `prompt` param → `Instance.Prompt` (not `AppendSystemPrompt`) |
| E2E | `tests/e2e/triage-pipeline-validation.spec.ts` | Session visible in UI for >30s (not immediate exit = Claude is running the prompt) |
| Meta | This file's existence | If `plan.md` is written, Claude received the prompt and executed |

### AC2: Claude submits results via submit_triage_result

| Test Type | Test Location | Assertion |
|-----------|-------------|----------|
| Unit | `server/mcp/tools_backlog_test.go` | `submitTriageResult` with valid session UUID + role persists JSON to `ItemSession.TriageResult` |
| Unit | `server/mcp/tools_backlog_test.go` | `submitTriageResult` with wrong role returns error |
| Unit | `server/mcp/tools_backlog_test.go` | `submitTriageResult` publishes `NOTIFICATION_TYPE_INPUT_REQUIRED` event |
| E2E | `tests/e2e/triage-pipeline-validation.spec.ts` | `TriageReviewPanel` appears in UI within 5 minutes of triage trigger |
| Meta | `ItemSession.TriageResult` non-null in DB | Pipeline health check: query for recent triage sessions with NULL TriageResult |

### AC3: EndedAt recorded for all session roles

| Test Type | Test Location | Assertion |
|-----------|-------------|----------|
| Unit | `session/backlog_lifecycle_test.go` | `onSessionExited` with `role="triage"` → `UpdateItemSessionEnded()` called |
| Unit | `session/backlog_lifecycle_test.go` | `onSessionExited` with `role="review"` → `UpdateItemSessionEnded()` called |
| Unit | `session/backlog_lifecycle_test.go` | `onSessionExited` with `role="work"` → `UpdateItemSessionEnded()` called |
| Integration | DB state after E2E | All ItemSessions have non-null `ended_at` after session process exits |

## Edge Cases and Error Scenarios

### Edge Case 1: Re-trigger after orphaned triage session
- **Setup**: ItemSession with `endedAt=nil`, `IsSessionLive=false`
- **Expected**: Orphan guard detects not-live session, marks ended, allows re-trigger
- **Test**: `backlog_service_test.go:TestTriggerTriage_OrphanedSessionAllowsRetrigger`

### Edge Case 2: Re-trigger while triage session is live
- **Setup**: ItemSession with `endedAt=nil`, `IsSessionLive=true`
- **Expected**: `CodeAlreadyExists` returned; no duplicate session spawned
- **Test**: `backlog_service_test.go:TestTriggerTriage_LiveSessionBlocksRetrigger`

### Edge Case 3: submit_triage_result called from wrong session
- **Setup**: Valid item_id but calling session UUID not linked to item
- **Expected**: MCP tool returns error; no TriageResult persisted
- **Test**: `tools_backlog_test.go:TestSubmitTriageResult_WrongSessionReturnsError`

### Edge Case 4: submit_triage_result called from non-triage role
- **Setup**: Valid item_id, valid session UUID, but role="work"
- **Expected**: MCP tool returns error (role guard)
- **Test**: `tools_backlog_test.go:TestSubmitTriageResult_WrongRoleReturnsError`

### Edge Case 5: Triage prompt contains special shell characters
- **Setup**: BacklogItem title with quotes, backticks, `$variables`
- **Expected**: Prompt safely escaped via `%q` format; Claude receives correct prompt
- **Test**: `instance_tmux_test.go:TestBuildLaunchCommand_SpecialCharsInPromptEscaped`

### Error Scenario: Session exits before submit_triage_result
- **State after**: `endedAt` set (fix #2), `TriageResult` NULL
- **UI state**: Session appears ended; no TriageReviewPanel
- **Recovery**: Re-trigger allowed if orphan guard passes
- **Detection**: Monitor for `ItemSession WHERE ended_at IS NOT NULL AND triage_result IS NULL AND session_role = 'triage'`

### Error Scenario: MCP server unavailable
- **State after**: Claude logs tool-not-found error, exits, `endedAt` set
- **UI state**: Session appears ended; no TriageReviewPanel
- **Recovery**: Re-trigger after ensuring MCP server ready

## Regression Guards

These assertions guard the three specific bugs fixed in `19ef4431`:

```
REGRESSION-1: OneShot -p flag
  buildLaunchCommand(OneShot=true) MUST contain " -p" in output
  buildLaunchCommand(OneShot=false) MUST NOT contain " -p"

REGRESSION-2: EndedAt for all roles
  onSessionExited(role="triage") → UpdateItemSessionEnded called
  onSessionExited(role="review") → UpdateItemSessionEnded called
  (not just work sessions)

REGRESSION-3: Prompt as positional arg
  CreateDirectorySession with prompt → Instance.Prompt set
  Instance.Prompt != "" → positional arg in launch command
  AppendSystemPrompt MUST NOT be used for triage prompt delivery
```
