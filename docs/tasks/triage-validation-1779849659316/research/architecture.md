# Architecture Research

## Component Diagram (text)

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Frontend (React/TypeScript)                  │
│                         web-app/src                                  │
├─────────────────────────────────────────────────────────────────────┤
│  BacklogItemDetail.tsx              BacklogBoard.tsx                 │
│    - UI for item details             - List of items                 │
│    - handleAction("trigger_triage")  - Triage action dispatch        │
│         ↓                                  ↓                         │
│  useBacklogService.ts               useBacklogService.ts            │
│    - triggerTriage(itemId)          - Calls same hook               │
│    - Calls client.triggerTriage()   - via useCallback               │
│         ↓                                  ↓                         │
└────────────────┬──────────────────────────┬──────────────────────────┘
                 │ ConnectRPC HTTP Request  │
                 │ POST /session.v1.BacklogService/TriggerTriage
                 ↓
┌─────────────────────────────────────────────────────────────────────┐
│                    Backend (Go/ConnectRPC)                           │
│                    server/services                                   │
├─────────────────────────────────────────────────────────────────────┤
│  BacklogService.TriggerTriage(ctx, req) — server/services           │
│    - Load BacklogItem from storage                                  │
│    - Validate status (idea/ready) and repo_path                     │
│    - Check for orphaned triage sessions                             │
│    - Kill stale tmux session by deterministic name                  │
│    - Create artifact dir (docs/tasks/<slug>)                        │
│    - Build triage prompt (buildTriagePrompt)                        │
│         ↓                                                            │
│  SessionService.CreateDirectorySession() — "one-shot" triage        │
│    - Creates session.Instance with OneShot=true                     │
│    - Wires MCPServerURL = <HTTP endpoint to MCP server>             │
│    - Calls instance.Start(true)  — starts tmux                      │
│    - Registers in live poller                                       │
│    - Creates ItemSession(role="triage") in database                 │
│         ↓                                                            │
│  session.Instance.initTmuxSession()   [session/instance_tmux.go]   │
│    - buildLaunchCommand() injects flags:                            │
│      * --mcp-config '{"mcpServers":{"stapler-squad":{...}}}'        │
│      * -p (one-shot mode)                                           │
│      * <triage prompt>                                              │
│    - Sets STAPLER_SESSION_UUID env var = UUID                       │
│    - Spawns: claude -p --mcp-config ... <prompt>                    │
│         ↓                                                            │
└────────────────┬────────────────────────────────────────────────────┘
                 │ STDIO connection to MCP server
                 ├─ STAPLER_SESSION_UUID=<uuid>
                 └─ --mcp-config provided by claude CLI
                 ↓
┌─────────────────────────────────────────────────────────────────────┐
│              MCP Server (running in session's claude)                │
│              server/mcp/tools_backlog.go                             │
├─────────────────────────────────────────────────────────────────────┤
│  Claude loads MCP server at startup (via STDIO)                      │
│    - Extracts STAPLER_SESSION_UUID from context/request headers     │
│    - Registers MCP tools:                                           │
│      * get_backlog_item(item_id)                                    │
│      * submit_triage_result(item_id, summary, suggestions, tasks)   │
│      * report_progress, request_review, submit_review_verdict       │
│         ↓                                                            │
│  get_backlog_item(ctx, req)                                         │
│    - sessionUUIDFromContext(ctx) → STAPLER_SESSION_UUID             │
│    - Load item + role-specific guidance                             │
│    - Return to Claude                                               │
│         ↓                                                            │
│  [Claude runs triage research & synthesis]                          │
│    - Executes subagents in parallel                                 │
│    - Writes: docs/tasks/<slug>/research/*.md                        │
│    - Writes: docs/tasks/<slug>/plan.md                              │
│    - Writes: docs/tasks/<slug>/validation.md                        │
│         ↓                                                            │
│  submit_triage_result(ctx, req)                                     │
│    - callerSessionUUID(ctx) → STAPLER_SESSION_UUID                  │
│    - Verify link: ItemSession(session_uuid, item_id, role=triage)   │
│    - Serialize suggestions + tasks to JSON                          │
│    - Storage.UpdateItemSessionTriageResult(triage_result_json)      │
│    - Storage.UpdateBacklogItem(plan_artifacts_path)                 │
│    - Publish notification event                                     │
│    - Return success to Claude                                       │
│         ↓                                                            │
└────────────────┬────────────────────────────────────────────────────┘
                 │
                 ↓
┌─────────────────────────────────────────────────────────────────────┐
│  Database (SQLite / session/ent)                                     │
│                                                                       │
│  BacklogItem:                                                        │
│    - plan_artifacts_path (set by submit_triage_result)              │
│                                                                       │
│  ItemSession:                                                        │
│    - session_uuid (points to Instance.UUID)                         │
│    - item_id (points to BacklogItem.id)                             │
│    - session_role = "triage"                                        │
│    - triage_result (JSON: summary, suggestions, tasks)              │
│    - ac_snapshot (acceptance criteria at triage start)              │
└─────────────────────────────────────────────────────────────────────┘
```

## Session Creation for Triage

### Trigger Point
The triage pipeline starts when a user clicks "Trigger Triage" on a backlog item:

**Frontend (React):**
```
BacklogItemDetail.tsx:handleAction("trigger_triage")
  → useBacklogService.triggerTriage(itemId)
  → client.triggerTriage({itemId})  [ConnectRPC call]
```

**Backend (Go):**
```
BacklogService.TriggerTriage(ctx, req *connect.Request[TriggerTriageRequest])
  → Validates: item exists, status is "idea" or "ready", repo_path set
  → Checks for orphaned triage sessions (killed if not live)
  → Creates artifact directory: <repo_path>/docs/tasks/<slug>
  → Builds triage prompt with instructions
  → Calls SessionService.CreateDirectorySession(...)
```

### Key Parameters
- **title**: Deterministic name `"triage:<slug>"` derived from item title
- **path**: Item's RepoPath (where triage writes artifacts)
- **prompt**: Complete triage instructions with artifact paths
- **tags**: `["backlog:triage"]` for identification
- **oneShot**: **true** — critical flag that runs claude in `-p` mode (one-shot prompt evaluation)

### Session Instance Creation
```go
// server/services/session_service.go:529
CreateDirectorySession(ctx, title, path, prompt, tags, oneShot=true)
  ↓
session.NewInstance(InstanceOptions{
  Title:       title,
  Path:        path,
  Prompt:      prompt,
  Tags:        tags,
  OneShot:     true,      // ← runs claude -p (exits when done)
  MCPServerURL: s.mcpServerURL,  // ← HTTP endpoint for MCP calls
  Program:     "claude",
  AutoYes:     true,      // ← skip permission prompts
  CreateIfMissing: true,
})
  ↓
instance.Start(true)      // ← starts tmux session
  ↓
session.StartSessionDriver(instance, path)  // ← driver goroutine for lifecycle
```

### Database Tracking
After session starts, an ItemSession record is created:
```go
storage.CreateItemSession(ctx, session.ItemSessionData{
  ItemID:      item.ID,
  SessionUUID: inst.UUID,        // ← links to Instance
  SessionRole: "triage",         // ← identifies role
  AcSnapshot:  item.AcceptanceCriteria,  // ← snapshot at start
})
```

## MCP Injection Flow

### Mechanism 1: --mcp-config Flag (Primary for Triage)
When MCPServerURL is set on the Instance, it is injected via command-line flag at tmux start:

**Location:** `session/instance_tmux.go:buildLaunchCommand()`

```go
// Line 31-39
if i.MCPServerURL != "" && strings.Contains(program, "claude") {
  var mcpFlag string
  if i.UUID != "" {
    // With session UUID in headers for MCP tool context
    mcpFlag = fmt.Sprintf(`--mcp-config '{"mcpServers":{"stapler-squad":{
      "type":"http",
      "url":%q,
      "headers":{"X-Stapler-Session-UUID":%q}
    }}}'`, i.MCPServerURL, i.UUID)
  } else {
    mcpFlag = fmt.Sprintf(`--mcp-config '{"mcpServers":{"stapler-squad":{
      "type":"http",
      "url":%q
    }}}'`, i.MCPServerURL)
  }
  program = program + " " + mcpFlag
}
```

**Final Claude Command:**
```
claude -p \
  --mcp-config '{"mcpServers":{"stapler-squad":{"type":"http","url":"http://...:8080","headers":{"X-Stapler-Session-UUID":"<uuid>"}}}}' \
  "<triage prompt>"
```

### Mechanism 2: Environment Variable (Fallback)
The session's tmux environment also includes:

```go
// session/instance_tmux.go:81
session.SetExtraEnv([]string{"STAPLER_SESSION_UUID=" + i.UUID})
```

This allows MCP tools to extract the session UUID from context if needed.

### Mechanism 3: Settings File Injection (Alternative, NOT used for triage)
The `server/services/mcp_injector.go` provides file-based injection:
```go
InjectMCPConfig(rootDir, binaryPath)
  → Merges stapler-squad entry into .claude/settings.local.json
  → Entry: {"command": "<binary>", "type": "stdio", "args": ["--mcp"]}
```
This is **not** used for triage sessions (they use --mcp-config flag instead) but is available for manual sessions.

## Prompt Delivery Mechanism

### Prompt Construction
The triage prompt is built by `buildTriagePrompt()` in `server/services/backlog_service.go:1188`:

```go
func buildTriagePrompt(item *BacklogItemData, artifactAbsPath, slug string) string
  ├─ Title and description
  ├─ Acceptance criteria (if any)
  ├─ Task breakdown:
  │   ├─ Step 1: Run 4 research subagents in parallel
  │   │   ├─ /research/stack.md — Technology choices
  │   │   ├─ /research/features.md — Similar patterns
  │   │   ├─ /research/architecture.md — Component design
  │   │   └─ /research/pitfalls.md — Risks & gotchas
  │   ├─ Step 2: Synthesis (plan.md)
  │   ├─ Step 3: Validation (validation.md)
  │   ├─ Step 4: Submit results via submit_triage_result MCP tool
  │   └─ Step 5: Optional clarifying questions
  └─ Absolute path placeholders for all artifacts
```

### Delivery Method
The prompt is passed as the last argument to claude:
```go
// session/instance_tmux.go:49-51
if i.Prompt != "" && claudeSessionID == "" && strings.Contains(program, "claude") {
  program = fmt.Sprintf("%s %q", program, i.Prompt)
}
```

Result:
```
claude -p --mcp-config '...' "You are a senior software architect..."
```

Claude reads the prompt from stdin/args and begins execution in one-shot mode.

## Result Submission Path

### MCP Tool: submit_triage_result
**File:** `server/mcp/tools_backlog.go:402-536`

**Signature:**
```
submit_triage_result(
  item_id: UUID,
  summary: string,
  suggestions: [{text, rationale}, ...],
  tasks: [{text, estimate, category}, ...],
  plan_artifact_path: string
)
```

### Execution Flow
1. **Context Extraction:**
   ```go
   callerUUID, err := callerSessionUUID(ctx)
   // Extracts from context (set via STAPLER_SESSION_UUID env var or header)
   ```

2. **Authorization Check:**
   ```go
   itemSession, _ := storage.GetItemSessionBySessionAndItem(ctx, callerUUID, itemID)
   if itemSession.SessionRole != "triage" {
     return error("only 'triage' role may submit triage results")
   }
   ```

3. **Persistence:**
   ```go
   // Serialize to JSON
   triagePayload := {
     Summary: summary,
     Suggestions: suggestions,
     Tasks: tasks,
   }
   payloadJSON := json.Marshal(triagePayload)
   
   // Store on ItemSession
   storage.UpdateItemSessionTriageResult(itemSession.ID, string(payloadJSON))
   
   // Store plan_artifacts_path on BacklogItem if provided
   if planArtifactsPath != "" {
     storage.UpdateBacklogItem(itemID, {PlanArtifactsPath: planArtifactsPath})
   }
   ```

4. **Notification:**
   ```go
   // Publish notification event so UI shows "triage complete"
   eventBus.Publish(NewNotificationEvent(
     callerUUID,
     "Triage complete",
     fmt.Sprintf("%s — %d suggestion(s). Click to review.", itemTitle, len(suggestions))
   ))
   ```

5. **Response:**
   ```
   "Triage result submitted for item <id>. <N> suggestion(s) recorded."
   ```

### Data Transformation
- **Suggestions** (acceptance criteria improvements):
  ```json
  [
    {
      "text": "Add rate-limiting to API endpoints",
      "rationale": "Prevents DDoS attacks"
    },
    {
      "text": "What is the expected timeout behavior?",
      "rationale": "question"
    }
  ]
  ```

- **Tasks** (implementation checklist):
  ```json
  [
    {
      "text": "Implement core HTTP server",
      "estimate": "3h",
      "category": "backend"
    }
  ]
  ```

## Data Flow Summary

### Complete Message Path

```
User clicks "Trigger Triage" in UI
  ↓
[Frontend] triggerTriage(itemId) → HTTP POST
  ↓
[Backend] BacklogService.TriggerTriage()
  ├─ Validates item & session state
  ├─ Kills stale tmux sessions
  └─ Creates artifact directory
  ↓
[Backend] SessionService.CreateDirectorySession()
  ├─ Builds Instance with OneShot=true, MCPServerURL=<MCP endpoint>
  ├─ instance.Start() → tmux session spawns
  └─ Creates ItemSession(role=triage) record
  ↓
[tmux] buildLaunchCommand() generates final command:
  claude -p --mcp-config '{"mcpServers":{"stapler-squad":{"type":"http","url":"..."}}}' "<prompt>"
  ├─ -p flag: one-shot mode (exit on completion)
  ├─ --mcp-config: MCP server endpoint injected inline
  └─ Prompt: Full triage instructions with artifact paths
  ↓
[Claude] Receives one-shot prompt with MCP tools available
  ├─ Calls get_backlog_item(item_id) → Backend validates link via ItemSession
  ├─ Runs parallel research & synthesis
  ├─ Writes artifacts to disk: <repo>/docs/tasks/<slug>/...
  └─ Calls submit_triage_result(...) → Backend persists result
  ↓
[Backend MCP] submit_triage_result handler:
  ├─ Extracts STAPLER_SESSION_UUID from context
  ├─ Verifies ItemSession link (session_role=triage)
  ├─ Serializes suggestions & tasks to JSON
  ├─ Persists to database (ItemSession.triage_result, BacklogItem.plan_artifacts_path)
  └─ Publishes notification event
  ↓
[Claude] Exits (one-shot mode, -p flag)
  ↓
[UI] Receives notification → "Triage Complete"
  ├─ Shows triage suggestions & implementation tasks
  └─ Allows user to approve suggestions or manually edit AC
```

### Key Fields Involved

**BacklogItem:**
- `id` (UUID) — item identifier
- `title` — slugified to determine session name
- `description` — included in prompt
- `acceptance_criteria` (JSON) — included in prompt, snapshot stored in ItemSession
- `repo_path` — where session operates
- `status` — must be "idea" or "ready" to trigger triage
- `plan_artifacts_path` (set by MCP tool) — path to docs/tasks/<slug>

**ItemSession:**
- `id` (UUID) — unique record identifier
- `item_id` (FK to BacklogItem)
- `session_uuid` (FK to Instance.UUID) — links to session
- `session_role` = "triage" — identifies triage sessions
- `ac_snapshot` (JSON) — acceptance criteria at triage start
- `triage_result` (JSON) — serialized {summary, suggestions, tasks}
- `created_at`, `started_at`, `ended_at` — lifecycle timestamps

**Instance:**
- `UUID` — session identifier, embedded in MCP requests
- `Title` = "triage:" + slug — deterministic name
- `OneShot` = true — claude runs in -p mode
- `MCPServerURL` — HTTP endpoint for MCP callbacks
- `Prompt` — full triage instructions
- `STAPLER_SESSION_UUID` (env var) — context for MCP tool calls

## OneShot/OneOff Session Flags

### OneShot Flag (Triage Sessions)

**Definition:** Runs claude in `-p` mode (one-shot prompt evaluation).

**File:** `session/instance.go:189-190`
```go
// OneShot runs claude in -p mode; the session exits after the task completes.
OneShot bool
```

**Behavior:**
- Claude reads the initial prompt and processes it to completion
- No interactive REPL — session exits when the task is done
- Perfect for autonomous triage jobs that don't need user interaction

**Usage in Triage:**
```go
// backlog_service.go:1162
inst, err := s.sessionCreator.CreateDirectorySession(ctx, title, item.RepoPath, triagePrompt,
  []string{"backlog:triage"}, true)  // ← oneShot=true
```

**Implementation:**
```go
// instance_tmux.go:46-48
if i.OneShot && strings.Contains(program, "claude") {
  program = program + " -p"
}
```

### MCP Injection for OneShot Sessions

For triage (one-shot) sessions, MCP is injected via `--mcp-config` flag:

**Rationale:**
- File injection (mcp_injector.go) writes to .claude/settings.local.json on disk
- For one-shot sessions that exit immediately, file injection is unnecessary overhead
- CLI flag injection is atomic and requires no file writes
- Cleaner isolation: each triage session carries its own MCP config in the command line

**Flow:**
```
SessionService.CreateDirectorySession(MCPServerURL="http://...")
  ↓
Instance.Start()
  ↓
buildLaunchCommand() detects MCPServerURL
  ├─ Builds --mcp-config JSON with full stapler-squad endpoint
  └─ Appends to program string
  ↓
tmux spawns: claude -p --mcp-config '...' "<prompt>"
```

### Other Flags Relevant to Triage

**AutoYes:** `AutoYes=true` skips permission prompts (automatically approved for automated sessions)
```go
// instance_tmux.go:43-45
if i.AutoYes && strings.Contains(program, "claude") {
  program = program + " --dangerously-skip-permissions"
}
```

**AppendSystemPrompt:** Optionally injects extra system-level instructions (not used for triage, but available for future enhancements)
```go
// instance_tmux.go:40-42
if i.AppendSystemPrompt != "" && strings.Contains(program, "claude") {
  program = fmt.Sprintf("%s --append-system-prompt %q", program, i.AppendSystemPrompt)
}
```

## Session Lifecycle for Triage

### State Transitions

```
Creating (SessionService.CreateDirectorySession builds Instance)
  ↓ (instance.Start() called)
Active (tmux session started, claude -p running)
  ↓ (Claude executes triage, calls submit_triage_result, exits)
Completed (process exits, session driver detects completion)
  ↓ (SessionDriver marks ended_at timestamp)
[Session remains in database for audit trail]
```

### Cleanup

When triage completes:
1. Claude process exits (one-shot mode, -p flag)
2. SessionDriver detects exit and marks `ItemSession.ended_at`
3. Session stays in database for history/audit
4. ItemSession.triage_result contains full result JSON
5. BacklogItem.plan_artifacts_path points to docs/tasks/<slug>

### Orphan Detection & Cleanup

When re-triggering triage on the same item:
```go
// backlog_service.go:1094-1114
existingSessions, _ := storage.ListItemSessions(itemID)
for _, is := range existingSessions {
  if is.SessionRole != "triage" || is.EndedAt != nil {
    continue  // ← Skip closed sessions
  }
  
  // Check if session is genuinely still running
  neverStarted := is.StartedAt == nil
  notLive := neverStarted || !sessionStopper.IsSessionLive(is.SessionUUID)
  statusAdvanced := item.Status != "idea"
  
  if notLive || statusAdvanced {
    // Mark as ended (tombstone the session)
    storage.UpdateItemSessionEnded(is.ID, now)
    sessionStopper.StopSessionByUUID(is.SessionUUID)
  } else {
    // Genuinely live — block re-trigger
    return error("triage session already running")
  }
}
```

Also kills stale tmux session by deterministic name:
```go
// backlog_service.go:1138-1142
if sessionStopper != nil {
  sessionStopper.KillTmuxSessionByTitle("triage:" + slug)
}
```

This ensures a fresh tmux session when re-triggering, so the new `--mcp-config` flag is injected cleanly.
