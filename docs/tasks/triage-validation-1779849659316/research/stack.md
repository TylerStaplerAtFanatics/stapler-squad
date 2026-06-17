# Stapler Squad Triage Pipeline Technology Stack

## Overview

The Stapler Squad triage pipeline is a sophisticated system for managing backlog items through structured lifecycle workflows. It enables Claude-based AI agents to conduct triage analysis, implement features, and perform code reviews through multi-stage sessions with MCP (Model Context Protocol) tool integration. The stack is built primarily in Go with Protocol Buffers for RPC definitions and leverages the Claude Code CLI for agent invocation.

## Key Technologies

### Core Language & Runtime
- **Go 1.25.0** - Primary implementation language for the backend server and session management
- **Protocol Buffers 3** (protobuf v1.36.10) - Used for RPC service definitions and message serialization

### RPC & Communication
- **ConnectRPC v1.19.0** - Modern Go RPC framework replacing legacy gRPC, provides both Connect and gRPC protocols
- **OTel Connect v0.8.0** - OpenTelemetry instrumentation for ConnectRPC tracing
- **Gorilla WebSocket v1.5.3** - WebSocket support for real-time bidirectional communication
- **Mark3Labs MCP Go v0.48.0** - MCP (Model Context Protocol) library for tool integration; stdio and HTTP transport support

### Database & Persistence
- **Ent v0.14.5** - Entity framework for schema management and database operations
- **SQLite3 v1.14.40** - Primary backing storage (mattn/go-sqlite3)
- **go-git v5.14.0** - Git repository operations and worktree management

### AI & Claude Integration
- **Claude Haiku 4.5** (claude-haiku-4-5-20251001) - The pinned AI model for triage, work, and review agents
- **Anthropic Messages API v2023-06-01** - Direct HTTP API client for one-shot completions when Claude CLI is unavailable
- **Claude Code CLI** (binary: `claude` in PATH) - Primary invocation method for interactive AI agent sessions
  - Supports one-shot mode via `claude --print` with prompt on stdin
  - Sessions run in worktrees with MCP server injection

### Session Management
- **tmux** - Terminal multiplexer for persistent session isolation
- **creack/pty v1.1.24** - PTY allocation and management for terminal interaction
- **CDP (Chrome DevTools Protocol)** - Browser automation and VNC proxy support

### Observability & Monitoring
- **OpenTelemetry SDK v1.39.0** - Tracing and metrics collection
- **OTLP gRPC Exporter v1.39.0** - Export traces to observability backends
- **Grafana Pyroscope v1.2.8** - Continuous profiling support

### CLI & Tool Support
- **Cobra v1.10.1** - Command-line interface framework
- **Buf v1.57.2** - Protocol Buffer tooling and code generation
- **mvdan.cc/sh v3.13.0** - Shell script parsing and analysis

## Versions & Dependencies

### Critical Versions
| Component | Version | Purpose |
|-----------|---------|---------|
| Go | 1.25.0 | Baseline runtime |
| Claude Model | claude-haiku-4-5-20251001 | AI model for triage/work/review |
| Ent | 0.14.5 | Database/entity management |
| ConnectRPC | 1.19.0 | RPC framework |
| Anthropic API | v2023-06-01 | Fallback API client |
| Mark3Labs MCP | 0.48.0 | MCP server implementation |
| OpenTelemetry | 1.39.0 | Observability |

### Claude Code Integration
- **Claude CLI Binary Resolution**: Via `exec.LookPath("claude")` with fallback to Anthropic HTTP API
- **CLI Invocation Pattern**: `claude --print` with combined system+user prompt on stdin
- **Prompt Separator**: `\n\n---\n\n` (newline, two hyphens, newline)
- **Stdin Delivery**: All prompts delivered via stdin, not as command-line arguments
- **Session Environment**: `STAPLER_SESSION_UUID` injected into session context for MCP tool correlation

## Architecture Highlights

### Triage Pipeline Flow
1. **Item Creation** → `CreateBacklogItem` RPC call
2. **Triage Trigger** → `TriggerTriage` spawns triage session with Claude
   - Session role: `triage`
   - Artifacts: `docs/tasks/{slug}/research/` and `docs/tasks/{slug}/plan.md`
3. **Triage Session**
   - Claude runs in worktree with MCP server injected
   - Parallel research subagents write: `stack.md`, `features.md`, `architecture.md`, `pitfalls.md`
   - Synthesizes into `plan.md` and `validation.md`
   - Calls `submit_triage_result` MCP tool
4. **Work Session** → `SpawnSessionFromItem` with session role `work`
   - Agent implements acceptance criteria
   - Calls `report_progress` for per-criterion tracking
   - Calls `request_review` when ready
5. **Review Session** → Spawned separately with role `review`
   - Reviewer validates acceptance criteria
   - Calls `submit_review_verdict` with per-criterion outcomes
   - Outcomes: PASS → done, FAIL → back to work

### Session Management
- **Session Creator**: Interface allowing BacklogService to spawn sessions without importing handler internals
- **Session Stopper**: Allows BacklogService to kill orphaned sessions and clean tmux state
- **Item Session Tracking**: Records session UUID, role, commit counts, and lifecycle timestamps
- **Worktree Isolation**: Each session gets a dedicated tmux session and git worktree via `.claude/worktrees/`

### MCP Tool Integration
- **Stdio Transport**: MCP server runs as subprocess with environment variable injection
- **HTTP Transport**: Optional streamable HTTP transport for non-subprocess sessions
- **Backlog Tools**: Registered only when storage is available
  - `get_backlog_item` - Retrieve item with role-aware workflow guidance
  - `report_progress` - Mark per-criterion completion
  - `submit_triage_result` - Finalize triage analysis
  - `submit_review_verdict` - Submit review verdicts
  - `request_review` - Transition to review workflow

### Prompt Construction
- **Token Budget**: 4000 token limit for backlog item prompts
- **Prompt Building**: `BuildTokenBudgetedPrompt` in `/session/backlog_context.go`
  - Includes item title, description, acceptance criteria checklist, notes, prior attempts
  - Appends task protocol block with step-by-step instructions
  - Token reduction passes: drop prior sessions → truncate description to 500 chars
- **Triage Prompt**: `buildTriagePrompt` in `/server/services/backlog_service.go`
  - Describes 5-step triage process: Research → Synthesis → Validation → Submit → Questions
  - Directs agent to spawn parallel research subagents
  - Specifies output file locations and structure

## Compatibility Notes

### Claude Model Selection
- Pinned to `claude-haiku-4-5-20251001` for cost and latency optimization
- Anthropic HTTP API client defaults to same model
- Configurable via `anthropicModel` constant in `anthropic_client.go`

### AI Client Priority
The system uses multi-tier fallback:
1. **Claude Code CLI** (primary) - Handles auth, model selection, one-shot mode via `claude --print`
2. **Anthropic HTTP API** (fallback) - Used if ANTHROPIC_API_KEY is set and CLI unavailable
3. **No AI** (degraded) - Some operations continue without AI if both unavailable

### Database & Storage
- **SQLite** as backing store with Ent entity framework
- **Atomic Writes**: Settings file updates use temp file + rename pattern
- **Encryption**: Optional AES-GCM token encryption with `config.GetOrCreateEncryptionKey()`

### Git & Worktree
- **Git 2.x+** required for worktree support
- **Worktree Paths**: `.claude/worktrees/{name}` with deterministic naming based on item slug
- **Session State**: Tracked in both in-memory instances and database; orphan detection reconciles after server restarts

### MCP Configuration
- **Settings File**: `.claude/settings.local.json` (project-local) or `~/.claude/settings.json` (global)
- **MCP Server Entry**: `{"type": "stdio", "command": "/path/to/stapler-squad", "args": ["--mcp"]}`
- **Injection**: Automatic by `InjectMCPConfig` when spawning sessions with `inject_mcp=true`

### OpenTelemetry
- **Tracing**: OTLP gRPC exporter configured for observability backends
- **Instrumentation**: HTTP, gRPC, and ConnectRPC spans automatically collected
- **Optional**: Can be disabled if no observability backend is available

## Performance & Limits

- **Rate Limiting**: MCP tools have per-second token bucket limits (`writeRateLimitPerSec`)
- **Timeouts**:
  - Claude CLI timeout: 55 seconds (leaves 5-second headroom for context propagation)
  - Anthropic HTTP timeout: 30 seconds
- **Token Budgets**: Backlog prompts capped at 4000 estimated tokens (len/4 heuristic)

## Key Files

| Path | Purpose |
|------|---------|
| `/server/services/backlog_service.go` | Backlog RPC handlers: CreateBacklogItem, TriggerTriage, SpawnSessionFromItem, etc. |
| `/server/services/cli_ai_client.go` | Claude CLI and Anthropic API client implementations |
| `/server/services/mcp_injector.go` | MCP server config injection into `.claude/settings.local.json` |
| `/session/backlog.go` | Backlog status types, transitions, and business logic |
| `/session/workflow_engine.go` | Workflow state machine implementation |
| `/session/backlog_context.go` | Prompt building for work/review sessions |
| `/server/mcp/tools_backlog.go` | MCP tool implementations: get_backlog_item, report_progress, etc. |
| `/server/mcp/server.go` | MCP server setup and tool registration |
| `/proto/session/v1/backlog.proto` | Protobuf definitions for backlog RPCs and messages |

## Summary

The triage pipeline is built on Go 1.25, uses ConnectRPC for RPC, and orchestrates Claude AI agents through tmux-isolated sessions with MCP tool access. The Claude Haiku model is pinned for cost efficiency, with fallback to Anthropic HTTP API. Session state is persisted in SQLite with Ent, and MCP tools are injected into session environments for structured backlog lifecycle management. The system supports multi-role workflows (triage → work → review) with fine-grained token budgets, prompt optimization, and comprehensive observability through OpenTelemetry.
