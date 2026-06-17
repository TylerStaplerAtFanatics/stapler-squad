# Triage Pipeline Technology Stack Research

## Overview
The stapler-squad triage pipeline orchestrates a multi-stage workflow where Claude analyzes backlog items and submits structured results via MCP tools. The stack integrates Go backend components, tmux session management, MCP server protocol, and Claude CLI.

## Core Components

### Backend (Go 1.25.0)
- **Entgo**: Entity ORM for SQLite-backed storage (`entgo.io/ent v0.14.5`)
- **Connect RPC**: gRPC-compatible RPC framework (`connectrpc.com/connect v1.19.0`)
- **Protocol Buffers**: Message serialization for backlog items and session state
- **SQLite**: Default persistence layer via Mattn driver (`github.com/mattn/go-sqlite3 v1.14.40`)

### Session Management
- **tmux**: Terminal multiplexer for Claude CLI process isolation
- **Go PTY**: Pseudo-terminal control via `creack/pty` for capture and input
- **MCP Go Server**: Mark Labs' MCP server implementation (`github.com/mark3labs/mcp-go v0.48.0`)

### Key Services
- **BacklogService**: Handles item creation, spawning triage sessions, and status transitions
- **SessionService**: Creates tmux sessions, injects MCP config, wires session drivers
- **MCPInjector**: Writes MCP server entry to Claude's `.claude/settings.local.json`

## Triage Pipeline Flow

### 1. Item Creation → Triage Session
Backlog item transitions to "ready" status → `SpawnSessionFromItem()` RPC creates session with:
- **Session Type**: Directory or new git worktree
- **Environment**: `STAPLER_SESSION_UUID` injected into tmux session env
- **MCP Config**: Stapler Squad binary registered in `.claude/settings.local.json`
  - Type: `"stdio"` (local binary)
  - Command: Absolute path to `stapler-squad --mcp`
  - Args: `["--mcp"]` flag to enable MCP mode

### 2. Prompt Injection
Claude session receives initial prompt via:
- **buildTriagePrompt()**: Constructs triage task instructions
- **LaunchCommand**: Claude CLI invoked with `<prompt>` as positional argument
- **SessionDriver**: Background goroutine auto-answers startup dialogs, sends initial prompt at Ready status

### 3. Claude Execution
- Claude runs one-shot session (`-p` flag in launch command)
- Accesses backlog item context via `get_backlog_item()` MCP tool
- Creates artifact files in `.claude/worktrees/[branch]/docs/tasks/[slug]/research/`
- Calls `submit_triage_result()` MCP tool with findings

### 4. Result Submission
The `submit_triage_result()` MCP tool:
- Validates caller session role is "triage" (checked against `STAPLER_SESSION_UUID`)
- Persists triage JSON (summary, suggestions, tasks) to `ItemSession.triage_result`
- Stores `plan_artifact_path` for review/implementation phases
- Triggers state transition via BacklogLifecycleListener

## MCP Tool Stack
- **Server Mode**: Stapler Squad binary runs as MCP server when invoked with `--mcp` flag
- **Request/Response**: Uses mcp-go library for tool marshaling and error handling
- **Session Context**: UUID passed via environment variable + validated in every tool call
- **Rate Limiting**: create_session limited to 3/minute via rate limiter

## Persistence Layer
- **Database**: SQLite with Ent ORM entities
- **Tables**: backlog_items, item_sessions, review_verdicts
- **Workflow**: State machine validates transitions (idea → ready → in_progress → review → done)

## Integration Points
- **Claude CLI**: Launched via subprocess with `--append-system-prompt` for role-specific guidance
- **Git**: Optional worktree creation for isolated triage environment
- **File System**: Artifacts written to standardized path, stored in backlog_context.md

## Dependencies Summary
Key external libraries: mcp-go (protocol), entgo (ORM), connectrpc (RPC), go-git (version control), protobuf (serialization)
