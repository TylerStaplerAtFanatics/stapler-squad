# Triage Pipeline Architecture

## Overview

The triage pipeline is a specialized workflow within stapler-squad that spawns one-shot Claude AI sessions to perform pre-implementation analysis of backlog items. The pipeline orchestrates backlog item persistence, session creation, MCP server integration, and result capture through multiple coordinated subsystems.

## 1. Triage Session Creation: Backlog Item → Session → Claude Invocation

### 1.1 Entry Point: `TriggerTriage` RPC Handler

**Location**: `server/services/backlog_service.go:1058-1184` (`BacklogService.TriggerTriage`)

The workflow is initiated by an RPC call to `TriggerTriage`:

```
Client → TriggerTriage RPC
  ├─ ItemID validation
  ├─ Status guard (item must be "idea" or "ready")
  ├─ RepoPath validation
  ├─ Orphan session detection & cleanup
  ├─ Status reset (if re-triggering "ready" item, move back to "idea")
  ├─ Create artifact directory (docs/tasks/{slug}/
  └─ Spawn one-shot session
```

### 1.2 Key Pre-spawn Validations

1. **Item Status Guard** (line 1076-1079):
   - Only "idea" and "ready" items can enter triage
   - Once work/review sessions exist, triage is blocked

2. **Orphan Session Detection** (line 1088-1114):
   - Checks for existing triage ItemSessions with `SessionRole="triage"` and no `EndedAt`
   - A session is considered "orphaned" if:
     - `started_at` is NULL (never confirmed running)
     - Session UUID is not live in memory (`sessionStopper.IsSessionLive()` returns false)
     - Item status has advanced past "idea" (triage cycle completed)
   - Orphaned sessions are tombstoned (EndedAt set, session stopped)
   - Only genuinely live sessions block with `CodeAlreadyExists`

3. **Stale Tmux Session Cleanup** (line 4.5, lines 1138-1142):
   - Kills any existing tmux session with the deterministic name `"triage:<slug>"`
   - Critical: prevents reattachment to old session that would skip `--append-system-prompt` injection
   - Ensures fresh slate for the new session

### 1.3 Artifact Directory Structure

```
{item.RepoPath}/docs/tasks/{slug}/
├── research/
│   ├── stack.md          # Technology choices, versions, compatibility
│   ├── features.md       # Similar existing features, patterns to reuse
│   ├── architecture.md   # Proposed architecture, component boundaries
│   └── pitfalls.md       # Known risks, gotchas, failure modes
├── plan.md               # Executive summary, implementation approach, task breakdown
└── validation.md         # Test plan, edge cases, error scenarios
```

The absolute path is passed to the Claude session so it can verify os.Stat() when calling `submit_triage_result`.

### 1.4 Session Creation

**Location**: `server/services/backlog_service.go:1162-1177`

```go
inst, err := s.sessionCreator.CreateDirectorySession(
    ctx,
    title,                                    // "triage:<slug>"
    item.RepoPath,                           // Working directory
    triagePrompt,                            // System prompt
    []string{"backlog:triage"},              // Tags
    true,                                    // OneShot = true
)
```

**Key Parameters**:
- **Title**: `"triage:" + slugify(item.Title)` — deterministic and stable
- **OneShot**: `true` — enables `-p` flag in Claude command (protocol mode, exits after task completes)
- **Tags**: `["backlog:triage"]` — identifies this session as a triage session

The returned `Instance` has:
- `UUID`: Unique session identifier (used in MCP context injection)
- `Path`: Worktree repository path
- `Title`: Triage session title
- `OneShot`: true (triggers `-p` flag in tmux launch command)

### 1.5 ItemSession Creation

**Location**: `server/services/backlog_service.go:1169-1177`

```go
is, err := s.storage.CreateItemSession(ctx, session.ItemSessionData{
    ItemID:      item.ID,
    SessionUUID: inst.UUID,
    SessionRole: session.SessionRoleTriage,   // "triage"
    AcSnapshot:  item.AcceptanceCriteria,
})
```

Creates an `ItemSession` entity linking:
- The backlog item to the spawned Claude session
- Captured AC snapshot (for later comparison if item is modified)
- Session role = "triage" (distinguishes from "work" and "review" roles)

## 2. Claude Invocation: OneShot and Command Construction

### 2.1 OneShot Flag Effect

**Location**: `session/instance_tmux.go:46-48` (buildLaunchCommand method)

```go
if i.OneShot && strings.Contains(program, "claude") {
    program = program + " -p"
}
```

The `-p` flag puts Claude in "protocol mode":
- Claude reads input from stdin and writes structured output to stdout
- Exits immediately after task completion (no REPL loop)
- Ideal for one-shot workloads (triage, review)

### 2.2 Complete Command Construction

**Location**: `session/instance_tmux.go:26-52` (buildLaunchCommand method)

The final command is constructed as:

```
claude [--resume <sessionId>] [--mcp-config {...}] [--append-system-prompt "..."] [-p] "<prompt>"
```

**Order of injection**:
1. Base program: `claude`
2. Resume flag (if resuming a prior Claude session)
3. MCP config flag (--mcp-config)
4. Append system prompt flag (--append-system-prompt)
5. AutoYes flag (--dangerously-skip-permissions)
6. **OneShot flag (-p)** — for triage sessions, this is critical
7. Initial prompt (quoted, only if not resuming)

**For triage specifically**:
```
claude --mcp-config '{...}' --append-system-prompt "..." -p "<triagePrompt>"
```

### 2.3 Tmux Session Startup

**Location**: `session/instance_tmux.go:55-84` (initTmuxSession method)

The tmux session is created with:
- Session name: `staplersquad_{title}` → `staplersquad_triage:<slug>`
- Extra environment: `STAPLER_SESSION_UUID=<instanceUUID>`
- Command: The enriched launch command from step 2.2

The STAPLER_SESSION_UUID is injected into the session's environment so MCP tools can extract it from context and validate session ownership.

## 3. MCP Server Integration

### 3.1 MCP Configuration Injection

**Location**: `server/services/mcp_injector.go:24-87` (InjectMCPConfig function)

When a session is created, the MCP server configuration is written to `.claude/settings.local.json` in the session's worktree:

```json
{
  "mcpServers": {
    "stapler-squad": {
      "type": "stdio",
      "command": "/path/to/stapler-squad",
      "args": ["--mcp"]
    }
  }
}
```

**Behavior**:
- If entry already points to the same binary, it's a no-op
- If file exists without entry, entry is merged in
- If file doesn't exist, it's created
- Atomic write (temp file + rename)

### 3.2 MCP Runtime Invocation

**Location**: `session/instance_tmux.go:31-39` (buildLaunchCommand method)

The MCP server URL is injected as an HTTP flag in the claude command:

```go
if i.MCPServerURL != "" && strings.Contains(program, "claude") {
    mcpFlag = fmt.Sprintf(
        `--mcp-config '{"mcpServers":{"stapler-squad":{"type":"http","url":%q,"headers":{"X-Stapler-Session-UUID":%q}}}}'`,
        i.MCPServerURL,
        i.UUID,
    )
    program = program + " " + mcpFlag
}
```

This allows Claude to communicate with the stapler-squad HTTP MCP endpoint, passing the session UUID in the `X-Stapler-Session-UUID` header.

### 3.3 Session UUID Context Injection in MCP Tools

**Location**: `server/mcp/tools_backlog.go:20-42`

The MCP server receives the session UUID from the HTTP header and injects it into the context:

```go
// WithSessionUUID injects a session UUID into the context
func WithSessionUUID(ctx context.Context, id string) context.Context {
    return context.WithValue(ctx, sessionUUIDKey{}, id)
}

// callerSessionUUID returns the session UUID from context
func callerSessionUUID(ctx context.Context) (string, bool) {
    v, ok := ctx.Value(sessionUUIDKey{}).(string)
    return v, ok && v != ""
}
```

All MCP tools that modify backlog state (submit_triage_result, submit_review_verdict, report_progress) call `callerSessionUUID()` to:
1. Verify the session has a valid UUID
2. Verify the session is linked to the backlog item (ownership check)
3. Prevent unauthorized modifications

## 4. Triage Prompt Construction

### 4.1 Prompt Building

**Location**: `server/services/backlog_service.go:1186-1259` (buildTriagePrompt function)

The triage prompt is constructed with:

1. **Role statement**: "You are a senior software architect performing pre-implementation triage."

2. **Item metadata**:
   - Title
   - Item ID (passed to submit_triage_result)
   - Description
   - Acceptance Criteria

3. **Task breakdown**: Four steps with file paths:
   - Step 1 — Research (run 4 subagents in parallel, write research/*.md)
   - Step 2 — Synthesis (after research, write plan.md and validation.md)
   - Step 3 — Validation (write validation.md)
   - Step 4 — Submit (call submit_triage_result MCP tool)
   - Step 5 — Clarifying Questions (optional)

4. **Artifact path**: Absolute path passed to submit_triage_result for validation

### 4.2 Prompt Injection via --append-system-prompt

**Location**: `session/instance_tmux.go:40-42`

The triage prompt is NOT passed directly as the initial prompt. Instead, it's injected as an append-system-prompt:

```go
if i.AppendSystemPrompt != "" && strings.Contains(program, "claude") {
    program = fmt.Sprintf("%s --append-system-prompt %q", program, i.AppendSystemPrompt)
}
```

This ensures:
- The prompt is appended to Claude's system prompt (not user-supplied)
- The prompt is injected cleanly without interfering with claude's startup
- The prompt persists across session resumption (if triage is resumed)

### 4.3 OneShot Mode Execution

In oneshot mode (`-p`), Claude:
1. Reads the appended system prompt
2. Starts executing the task (no user prompt needed)
3. Writes output to stdout
4. When the task completes (or timeout), exits

For triage, this means:
- Claude begins performing research immediately
- Spawns subagents for parallel research
- Synthesizes findings into planning documents
- Calls submit_triage_result via MCP to notify completion

## 5. OneShot/Control Mode Flags

### 5.1 OneShot Flag

**Field**: `Instance.OneShot` (boolean)

**Effect**: Adds `-p` flag to claude command, enabling protocol/oneshot mode

**When set**:
- Triage sessions: always `true`
- Review sessions: always `true`
- Work sessions: always `false` (interactive REPL)

**Impact on session behavior**:
- Session exits after task completes (no hang waiting for user input)
- Claude in protocol mode is more deterministic
- Suitable for automated workflows

### 5.2 AppendSystemPrompt Field

**Field**: `Instance.AppendSystemPrompt` (string)

**Injection**: Via `--append-system-prompt` flag in buildLaunchCommand

**For triage**: Set to the full triage prompt text

**For work sessions**: Not used (empty string)

**For review sessions**: Set to the re-review prompt

### 5.3 Session Type and Worktree Handling

**Field**: `Instance.SessionType` (enum)

**Values**:
- `SessionTypeDirectory` — Work in an existing directory (no git worktree)
- `SessionTypeNewWorktree` — Create a new git worktree from current branch
- `SessionTypeExistingWorktree` — Reuse an existing worktree

**For triage**: Always `SessionTypeDirectory` (triage works in the main repo path)

**Worktree cleanup on re-trigger**: When re-triggering triage, the old tmux session is killed by name (line 1138-1142) before spawning a new one. This ensures:
- Fresh session gets `--append-system-prompt` injection
- No silent reattachment to old session
- Clean slate for Claude to perform triage

## 6. submit_triage_result Flow

### 6.1 MCP Tool Handler

**Location**: `server/mcp/tools_backlog.go:402-536` (submitTriageResult function)

The tool accepts:
- `item_id` (UUID): Backlog item being triaged
- `summary` (string): Executive summary of triage findings
- `suggestions` (array): Proposed AC improvements or clarifying questions
- `tasks` (array, max 12): Implementation task breakdown (text, estimate, category)
- `plan_artifact_path` (string): Absolute path to docs/tasks/{slug}/ directory

**Validation steps**:
1. Extract caller session UUID from context
2. Verify item_id is valid UUID format
3. Verify summary is not empty
4. Verify session is linked to item with role="triage" (ownership check)

### 6.2 Triage Result Persistence

**Location**: `server/mcp/tools_backlog.go:475-510`

The triage result is serialized to JSON and stored in two places:

1. **On BacklogItem**: `plan_artifacts_path` field is updated
   ```go
   update := session.BacklogItemUpdate{
       PlanArtifactsPath: &planArtifactsPath,
   }
   h.storage.UpdateBacklogItem(ctx, itemID, update, nil)
   ```

2. **On ItemSession**: `triage_result` field contains the full JSON payload
   ```go
   type triageResultJSON struct {
       Summary     string
       Suggestions []triageSuggestion
       Tasks       []triageTask
   }
   ```

### 6.3 Back-reference in ItemSessionToProto

**Location**: `server/services/backlog_service.go:201-222` (itemSessionToProto function)

When retrieving an ItemSession, the triage result JSON is deserialized back to proto:

```go
if is.TriageResult != "" {
    var tr triageResultJSON
    if jsonErr := json.Unmarshal([]byte(is.TriageResult), &tr); jsonErr == nil {
        suggs := make([]*sessionv1.TriageSuggestion, len(tr.Suggestions))
        for i, sg := range tr.Suggestions {
            suggs[i] = &sessionv1.TriageSuggestion{Text: sg.Text, Rationale: sg.Rationale}
        }
        tasks := make([]*sessionv1.TriageTask, len(tr.Tasks))
        for i, t := range tr.Tasks {
            tasks[i] = &sessionv1.TriageTask{Text: t.Text, Estimate: t.Estimate, Category: t.Category}
        }
        p.TriageResult = &sessionv1.TriageResult{
            Summary:     tr.Summary,
            Suggestions: suggs,
            Tasks:       tasks,
        }
    }
}
```

This ensures the triage result is round-tripped cleanly for UI display.

### 6.4 Notification and Transition

**Location**: `server/mcp/tools_backlog.go:512-530`

After storing the triage result:

1. **EventBus notification** (if configured):
   - Publishes a `NOTIFICATION_TYPE_INPUT_REQUIRED` event
   - Message: "{ItemTitle} — N suggestion(s). Click to review."
   - Allows operator to review triage output

2. **Session completion**:
   - In oneshot mode, Claude exits after calling submit_triage_result
   - The session's `Status` becomes `Stopped`
   - The ItemSession remains in the database with triage result

### 6.5 Item Status Transition

**Note**: `submit_triage_result` does NOT automatically transition the item status. The item remains in "idea" status until the operator manually approves the triage results, at which point:

1. Operator reviews suggestions and tasks
2. Operator calls `ApprovePlan` RPC to mark plan as approved
3. Item transitions to "ready" status
4. Item can now be spawned as a work session

## 7. Control Mode and Idle Detection

### 7.1 Tmux Control Mode

**Location**: `session/external_tmux_streamer.go`, `session/tmux_process_manager.go`

For external sessions (not directly managed by stapler-squad), tmux control mode is used to:
- Stream terminal output in real-time without blocking
- Detect when Claude is idle (waiting for input)
- Detect status changes (◇ Ready, esc to interrupt, Thinking…)

For triage sessions (managed directly):
- The claude_controller manages PTY reading and status detection
- Idle detection prevents false timeouts
- Status detection identifies when Claude is waiting for input or finished

### 7.2 Status Detection

**Location**: `session/claude_controller.go:66-200` (ClaudeController)

The ClaudeController runs a background status detector that:
1. Reads PTY output in tail chunks (4KB by default)
2. Detects terminal status indicators (last 15 lines examined)
3. Caches status detection by FNV hash of tail content
4. Fires StatusChangeListener when status transitions
5. Handles idle state detection with historical timestamps

**For oneshot triage sessions**:
- Controller detects when Claude exits (or becomes idle)
- Lifecycle event `EventExited` is fired
- The session manager marks the session as `Stopped`
- ItemSession record is updated with end time

## 8. Summary: Complete Triage Flow

```
1. User calls TriggerTriage RPC
   ↓
2. BacklogService validates item, cleans up orphans, kills stale tmux
   ↓
3. BacklogService calls sessionCreator.CreateDirectorySession()
   ↓
4. Session creation:
   a. Instance with OneShot=true is created
   b. MCP config written to .claude/settings.local.json
   c. Tmux session initialized with enriched command:
      claude --mcp-config {...} --append-system-prompt "..." -p
   ↓
5. ItemSession created linking item + session UUID
   ↓
6. Tmux session started, Claude begins executing triage prompt
   ↓
7. Claude spawns subagents, creates research/*.md files
   ↓
8. Claude synthesizes plan.md and validation.md
   ↓
9. Claude calls submit_triage_result via MCP tool
   ↓
10. MCP handler:
    - Validates session UUID and item ownership
    - Stores triage result JSON on ItemSession
    - Updates BacklogItem.plan_artifacts_path
    - Publishes notification event
    ↓
11. Claude exits (oneshot mode -p)
    ↓
12. Session marked as Stopped
    ↓
13. Operator reviews triage output, calls ApprovePlan
    ↓
14. Item transitions to "ready", can spawn work session
```

## Key Architecture Decisions

1. **Deterministic Session Naming**: `"triage:<slug>"` allows orphan detection and stale tmux cleanup
2. **OneShot Mode**: Ensures triage Claude exits after completing tasks, no hanging processes
3. **AppendSystemPrompt**: Injects triage prompt cleanly without interfering with claude startup
4. **MCP HTTP vs Stdio**: Triage sessions use HTTP MCP (via MCPServerURL) for cleaner integration
5. **Session UUID Context Injection**: Prevents unauthorized access to backlog items from unlinked sessions
6. **Artifact Path Validation**: Claude validates on-disk path before submitting (prevents path injection attacks)
7. **ItemSession Linking**: Creates explicit backlog item ↔ session association for audit and permission checks

## Failure Modes and Recovery

- **Orphaned sessions**: Detected by StartedAt=null or session UUID not live; tombstoned gracefully
- **Stale tmux sessions**: Killed before re-trigger to prevent silent reattachment
- **MCP tool permission denied**: Session not linked to item or session role mismatch
- **Missing artifact path**: submit_triage_result fails if path does not exist (prevents race conditions)
- **Task limit exceeded**: Max 12 tasks enforced; excess tasks silently truncated (line 468-471)
