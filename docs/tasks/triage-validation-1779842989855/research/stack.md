# Stapler Squad Triage Pipeline Technology Stack

## Overview

Stapler Squad's triage pipeline orchestrates AI-assisted pre-implementation analysis via a sophisticated Go-based backend that combines ConnectRPC services, MCP (Model Context Protocol) tools, and Claude Code CLI integration. The system manages the complete lifecycle from backlog item creation through triage analysis submission.

## Core Technology Stack

### Go Version & Dependencies

- **Go Version**: 1.25.0
- **Key Packages**:
  - `connectrpc.com/connect` (v1.19.0) - ConnectRPC protocol implementation
  - `connectrpc.com/otelconnect` (v0.8.0) - OpenTelemetry integration for ConnectRPC
  - `entgo.io/ent` (v0.14.5) - Entity framework for type-safe ORM
  - `github.com/mark3labs/mcp-go` - MCP (Model Context Protocol) server implementation
  - `google.golang.org/protobuf` (v1.36.10) - Protocol Buffers for RPC definitions
  - `github.com/gorilla/websocket` (v1.5.3) - WebSocket streaming support
  - `go.opentelemetry.io/*` (v1.39.0+) - Distributed tracing and observability
  - `github.com/go-git/go-git/v5` (v5.14.0) - Git operations

### Session Management

Located in `/server/session/`:
- **instance.go**: Core session instance lifecycle (Creating → Active → Paused → Stopped → Hibernated)
- **Session Lifecycle States**:
  - `Creating` (0): Instance initialization
  - `Active` (1): Live AI process
  - `Paused` (2): Worktree removed, branch preserved
  - `Stopped` (3): Terminal state
  - `Hibernated` (4): Checkpointed state

The `Instance` struct carries immutable UUID, stable ID, Title, Path, and WorkingDir for repository navigation.

## Triage Session Creation & Management

### BacklogService Architecture

**File**: `/server/services/backlog_service.go`

The `BacklogService` struct orchestrates triage lifecycle:

```go
type BacklogService struct {
    storage        *session.Storage          // Database persistence
    sourceBackend  itemSourceBackend         // Item source operations
    sessionCreator SessionCreator            // Session spawning interface
    sessionStopper SessionStopper            // Session cleanup interface
    cfg            *config.Config            // Configuration management
    engine         session.WorkflowEngine    // Workflow orchestration
    worktreeMu     sync.Mutex                // Serializes concurrent writes
}
```

**Key Interfaces**:
- `SessionCreator`: Spawns directory sessions with `CreateDirectorySession(ctx, title, path, prompt, tags, oneShot bool)`
- `SessionStopper`: Manages orphaned session cleanup via tmux integration
  - `StopSessionByUUID()`: Direct session termination
  - `KillTmuxSessionByTitle()`: Deterministic tmux session killing
  - `IsSessionLive()`: Checks in-memory poller for active sessions

### Triage Prompt Construction

**Function**: `buildTriagePrompt()` (backlog_service.go:1188-1259)

Generates a structured multi-step prompt that guides Claude through:

1. **Research Phase** (parallel subagents):
   - `{artifactAbsPath}/research/stack.md` — Technology & compatibility
   - `{artifactAbsPath}/research/features.md` — Existing patterns
   - `{artifactAbsPath}/research/architecture.md` — Component design
   - `{artifactAbsPath}/research/pitfalls.md` — Known risks

2. **Synthesis Phase**:
   - `{artifactAbsPath}/plan.md` — Summary, approach, task breakdown, dependencies

3. **Validation Phase**:
   - `{artifactAbsPath}/validation.md` — Test mapping, edge cases

4. **Submission Phase**:
   - Calls `submit_triage_result` MCP tool with analysis results

Each artifact path is absolute (verified via `os.Stat`) to ensure Claude can write directly to disk.

### Session Spawn Flow

**TriggerTriage RPC Handler** (backlog_service.go:1050-1184):

1. **Item Validation**: Load backlog item, check existing sessions
2. **Orphan Detection**: Clean up stale sessions (not started or already advanced)
3. **Status Reset**: If item is "ready", transition back to "idea" for re-triage
4. **Artifact Directory**: Create `docs/tasks/{slug}/` path
5. **Tmux Cleanup**: Kill any existing tmux session with same title
6. **One-Shot Session Creation**: Call `sessionCreator.CreateDirectorySession(..., oneShot=true)`
7. **ItemSession Linking**: Record session UUID with `role=triage`

Example title format: `"triage:" + slug` (e.g., `"triage:add-jwt-auth"`)

## Claude Code CLI Invocation

### CLIAgentSpec Architecture

**File**: `/server/services/cli_ai_client.go`

Stapler Squad invokes Claude Code in **one-shot mode** with stdin prompt delivery:

```go
type CLIAgentSpec struct {
    Name            string              // "claude", "gemini", "opencode"
    Binary          string              // Executable name
    Args            func() []string     // One-shot mode arguments
    PromptSeparator string              // System/user prompt delimiter
}
```

**Claude Code Specification**:
```go
{
    Name:            "claude",
    Binary:          "claude",
    Args:            func() []string { return []string{"--print"} },
    PromptSeparator: "\n\n---\n\n",
}
```

**Invocation Pattern**:
1. Resolve `claude` binary via `exec.LookPath()`
2. Construct combined prompt: `systemPrompt + "\n\n---\n\n" + userPrompt`
3. Spawn process: `claude --print` with stdin receiving combined prompt
4. Apply 55-second timeout (headroom for 60-second deadline)
5. Capture stdout and trim whitespace

**Complete(*CLIAIClient)** method (cli_ai_client.go:81-95):
- Uses `executor.ShortLivedCmd` for context cancellation, timeout, and audit logging
- Returns stdout as string; errors on stderr or timeout

### CLI Agent Selection

**NewBestAvailableAIClient()** priority order:
1. **CLI agents** (first found in PATH): claude, gemini, opencode
2. **Anthropic API** (fallback): If `anthropicAPIKey` non-empty
3. **None**: Return `(nil, "")`

CLI agents preferred because they manage their own auth and model selection.

## MCP Tool Integration

### MCP Server Architecture

**File**: `/server/mcp/server.go`

The MCP server is launched via `--mcp` flag and communicates over stdio transport:

```go
func RunServer(ctx context.Context, store session.InstanceStore, 
               svc *services.SessionService, sbMgr *scrollback.ScrollbackManager, 
               storage *session.Storage, eventBus *events.EventBus) error
```

**Core Setup**:
- Session UUID injected from `STAPLER_SESSION_UUID` environment variable
- Stdlib stdio transport: `mcpserver.NewStdioServer()`
- All tools registered via `NewCore()` factory

### Backlog Tools Registration

**File**: `/server/mcp/tools_backlog.go`

**registerBacklogTools()** registers four MCP tools:

1. **get_backlog_item** (line 543-551)
   - Fetches full backlog item details + role-specific workflow guidance
   - Returns triage/work/review role instructions if session is linked

2. **report_progress** (line 554-575)
   - Updates AC criterion status during implementation
   - Role: work only
   - Statuses: "pass" (done), "fail" (blocked), "in_progress" (active)

3. **request_review** (line 578-590)
   - Signals implementation complete → transitions item to "review" status
   - Role: work only

4. **submit_review_verdict** (line 593-618)
   - Per-criterion review verdicts (PASS/FAIL/PARTIAL/UNVERIFIABLE)
   - Role: review only
   - Auto-transitions item to "done" if all PASS

5. **submit_triage_result** (line 621-659)
   - **Role**: triage only
   - **Required Parameters**:
     - `item_id` (UUID)
     - `summary` (2-3 sentence overview)
   - **Optional Parameters**:
     - `suggestions` (array of {text, rationale})
     - `tasks` (array of {text, estimate, category}, max 12)
     - `plan_artifact_path` (absolute path to `docs/tasks/[slug]`)

### submit_triage_result Implementation

**Handler**: `backlogHandlers.submitTriageResult()` (tools_backlog.go:402-536)

**Execution Flow**:

1. **Authentication**: Extract caller session UUID from context
2. **Permission Check**: Verify session is linked to item with `role=triage`
3. **Suggestion Parsing**: Unmarshal JSON array into `[]triageSuggestion`
4. **Task Parsing**: Unmarshal JSON array into `[]triageTask` (capped at 12)
5. **Payload Construction**:
   ```json
   {
     "summary": "...",
     "suggestions": [{"text": "...", "rationale": "..."}],
     "tasks": [{"text": "...", "estimate": "2h", "category": "backend"}]
   }
   ```
6. **Persistence**:
   - Store JSON on `ItemSession.triage_result` field
   - Update `BacklogItem.plan_artifacts_path` if provided
7. **Notification**: Publish triage-complete event to EventBus (if wired)
   - Notification type: `NOTIFICATION_TYPE_INPUT_REQUIRED`
   - Priority: `NOTIFICATION_PRIORITY_MEDIUM`
   - Message: `"{title} — {N} suggestion(s). Click to review."`

**Error Handling**:
- `ErrPermissionDenied`: Session not linked or role mismatch
- `ErrInvalidArgument`: Missing/malformed parameters
- `ErrInternalError`: Database or JSON serialization failures

## Proto Definitions

### Backlog Protocol Schema

**File**: `/proto/session/v1/backlog.proto`

**Key Message Types**:

#### TriageResult (line 50-56)
```protobuf
message TriageResult {
  string summary = 1;
  repeated TriageSuggestion suggestions = 2;
  repeated string clarifying_questions = 3;
  repeated TriageTask tasks = 4;
}
```

#### TriageSuggestion (line 37-41)
```protobuf
message TriageSuggestion {
  string text = 1;
  string rationale = 2;  // "question" marker for clarifying questions
}
```

#### TriageTask (line 43-48)
```protobuf
message TriageTask {
  string text = 1;      // one-line task description
  string estimate = 2;  // e.g. "2h", "30m"
  string category = 3;  // e.g. "backend", "frontend", "test", "infra", "docs"
}
```

#### ItemSession (line 58-72)
```protobuf
message ItemSession {
  string id = 1;
  string session_uuid = 2;
  string session_role = 3;         // "triage", "work", "review"
  google.protobuf.Timestamp started_at = 4;
  google.protobuf.Timestamp ended_at = 5;
  string last_commit_message = 6;
  google.protobuf.Timestamp last_commit_at = 7;
  int32 commit_count_since_spawn = 8;
  google.protobuf.Timestamp last_file_touch_at = 9;
  google.protobuf.Timestamp created_at = 10;
  ReviewVerdict review_verdict = 11;
  TriageResult triage_result = 12;
}
```

### BacklogService RPC Definition (line 316-375)

```protobuf
service BacklogService {
  rpc TriggerTriage(TriggerTriageRequest) returns (TriggerTriageResponse) {}
  rpc SpawnSessionFromItem(SpawnSessionFromItemRequest) 
      returns (SpawnSessionFromItemResponse) {}
  rpc GetBacklogItem(GetBacklogItemRequest) returns (GetBacklogItemResponse) {}
  rpc ListBacklogItems(ListBacklogItemsRequest) returns (ListBacklogItemsResponse) {}
  rpc UpdateBacklogItem(UpdateBacklogItemRequest) returns (UpdateBacklogItemResponse) {}
  rpc TransitionBacklogItemStatus(TransitionBacklogItemStatusRequest)
      returns (TransitionBacklogItemStatusResponse) {}
  // ... additional RPC methods
}
```

## ConnectRPC Integration

### Service Registration

**File**: `/server/server.go` (line 344-350)

```go
if deps.BacklogService != nil {
    blPath, blHandler := sessionv1connect.NewBacklogServiceHandler(
        deps.BacklogService, 
        ConnectOptions(deps.ErrorRegistry)...)
    blAPIPath := "/api" + blPath
    srv.RegisterConnectHandler(blAPIPath, http.StripPrefix("/api", blHandler))
}
```

**Generated Path**: `/api/session.v1.BacklogService/`

### ConnectRPC Middleware

- **OTel Instrumentation**: `otelconnect` intercepts all RPC calls for distributed tracing
- **Error Registry**: Custom error code mapping
- **WebSocket Streaming**: Custom `connectrpc_websocket.go` handler for terminal streams

### HTTP/Protobuf Transport

- **Protocol**: ConnectRPC (HTTP/1.1 compatible, not gRPC)
- **Serialization**: Protocol Buffers with JSON fallback
- **Streaming**: Supported for long-lived connections (terminal, events)
- **CORS**: Configured per origin type (localhost, HTTPS, auth-gated)

## MCP Server Integration with Claude Code

### Session UUID Injection

**File**: `/server/mcp/server.go` (line 63-65)

When triage session spawns:
1. `sessionCreator.CreateDirectorySession()` returns `Instance` with UUID
2. Hook injector writes `STAPLER_SESSION_UUID={uuid}` to session environment
3. Claude Code subprocess receives UUID in environment
4. MCP server (stdio) extracts UUID from `os.Getenv("STAPLER_SESSION_UUID")`
5. Context carries UUID via `WithSessionUUID()` for all tool calls

### Authentication & Authorization

- **Tool Access**: Gated by session UUID presence in context
- **Backlog Tool Access**: Additionally requires `ItemSession` link with matching role
- **Permission Levels**:
  - Triage: Can call `submit_triage_result` only
  - Work: Can call `report_progress`, `request_review` only
  - Review: Can call `submit_review_verdict` only

### MCP Server Launch

**Entry Point**: `cmd/stapler-squad --mcp`

Reads from stdin, writes to stdout. Claude Code's stdio transport connects directly.

**Optional Wiring**:
- If `storage` is nil: Backlog tools not registered
- If `eventBus` is nil: Triage-complete notifications disabled
- Graceful degradation for test environments

## Dependency Injection

### BacklogService Wiring

**File**: `/server/dependencies.go`

BuildDependencies() constructs:
1. `session.Storage` (Ent ORM with SQLite backend)
2. `SessionCreator` (handler implementing CreateDirectorySession)
3. `SessionStopper` (background manager checking live session state)
4. `BacklogService` wrapping all three

All dependencies wired into HTTP server at startup via `wireDepsIntoServer()`.

## Key Workflows

### Complete Triage Flow

1. **Backlog Item Created**: Operator creates item with title, description, acceptance criteria
2. **TriggerTriage RPC**: UI calls `BacklogService.TriggerTriage(item_id)`
3. **Session Spawn**: BacklogService calls `CreateDirectorySession(triage:{slug}, repo, prompt, tags, oneShot=true)`
   - `claude --print` subprocess launched
   - Combined prompt sent via stdin
   - UUID injected to environment
4. **Triage Execution**: Claude analyzes item, writes research/*.md, plan.md, validation.md
5. **MCP Call**: Claude calls `submit_triage_result(item_id, summary, suggestions, tasks, plan_artifact_path)`
6. **Persistence**: ItemSession.triage_result updated, notification published
7. **Operator Review**: UI shows triage summary + suggestions for approval/revision

### Artifact Storage

- **Location**: `{repo_path}/docs/tasks/{slug}/`
- **Structure**:
  ```
  docs/tasks/{slug}/
    research/
      stack.md
      features.md
      architecture.md
      pitfalls.md
    plan.md
    validation.md
  ```
- **Access**: Claude writes directly; MCP backend verifies via `os.Stat()` on absolute paths

## Observability

### Logging

- **Info Logs**: Session spawn, triage completion, service registration
- **Error Logs**: MCP tool failures, database errors, process execution issues
- **Format**: Structured logging via `/log` package

### Tracing

- **OpenTelemetry Integration**: All ConnectRPC calls traced
- **Span Attributes**: Session UUID, item ID, user/reviewer IDs
- **Export**: Configurable (OTLP gRPC to collector)

## Security Considerations

1. **Session UUID**: Prevents unauthorized tool access; session-specific permission checks
2. **Artifact Paths**: Absolute paths verified before writes; prevents directory traversal
3. **Role-Based Access**: Triage/work/review roles enforce workflow integrity
4. **Orphan Cleanup**: Stale sessions killed before re-triage to prevent state leaks
5. **Atomic Writes**: Worktree context file writes serialized to prevent race conditions

## Summary

The Stapler Squad triage pipeline is a production Go system leveraging:

- **ConnectRPC** for strongly-typed, protobuf-based RPC
- **MCP** for Claude Code tool integration via stdio
- **Claude Code CLI** (`claude --print`) for one-shot AI execution
- **Ent ORM** for type-safe database operations
- **tmux/PTY** for session lifecycle management
- **OpenTelemetry** for observability

The architecture enables safe, traceable, and auditable AI-assisted code analysis within a structured workflow.
