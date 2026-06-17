# Triage Pipeline Technology Stack

## Overview

The stapler-squad triage pipeline is a Go-based backend system orchestrating Claude AI agents in isolated tmux sessions. One-shot triage sessions execute in print-mode (-p), generate artifacts, and self-terminate.

## Technology Stack

### Language and Runtime
- Go Version: 1.25.0
- Protocol Format: Protocol Buffers v3 (protobuf v1.36.10)
- gRPC/HTTP Framework: ConnectRPC v1.19.0

### Key Dependencies
- entgo.io/ent v0.14.5 - Entity framework for database operations
- creack/pty v1.1.24 - PTY for tmux terminal I/O
- go-git v5.14.0 - Git operations for worktree diffs
- lumberjack v2.2.1 - Log rotation

## Proto Definitions

### Key Messages

**Session** (types.proto line 9)
- string prompt = 13 - Initial prompt passed on startup
- tags, project_id, launch_command - Session metadata
- NOTE: OneShot is NOT in proto (Go-specific field)

**BacklogItem** (backlog.proto line 84)
- string status - idea, ready, in_progress, review, done
- repeated ItemSession item_sessions
- string repo_path - Repository directory
- string plan_artifacts_path - Triage output location

**ItemSession** (backlog.proto line 59)
- string session_uuid - Reference to Instance.UUID
- string session_role - triage, work, review
- Timestamp started_at, ended_at
- TriageResult triage_result - Populated by agent

## Key Implementation Files

### backlog_service.go
**TriggerTriage** (lines 1056-1184)
- Validates item status (idea/ready)
- Creates artifact directory
- Builds triage prompt via buildTriagePrompt
- Calls sessionCreator.CreateDirectorySession with:
  - title: "triage:" + slug
  - prompt: Full triage instructions
  - tags: ["backlog:triage"]
  - oneShot: true
- Key Detail: Uses Prompt field (positional CLI), NOT AppendSystemPrompt

### session_service.go
**CreateDirectorySession** (lines 525-566)
- Maps request to InstanceOptions:
  - Prompt = prompt (positional argument)
  - OneShot = oneShot (controls -p flag)
  - AutoYes = true
  - MCPServerURL (enables MCP tools)
- Starts instance and wires BacklogLifecycleListener

### instance_tmux.go
**buildLaunchCommand** (lines 26-53)
- Prompt injection (lines 49-51): Appends as positional argument
- OneShot injection (lines 46-48): Adds -p flag when true

### backlog_lifecycle.go
**onSessionExited** (lines 97-153)
- Commit 19ef4431 fix: UpdateItemSessionEnded moved before role check
- Records EndedAt for all session roles
- Drives status transitions (idea to ready for triage)

## The OneShot Flag

**Purpose**: Session exits after task completion (-p mode)

**Implementation**:
- Instance.OneShot bool (line 190)
- When true, buildLaunchCommand adds " -p" to program

**Detection** (session_driver.go lines 347-349):
- Tags "backlog:triage" or "backlog:review" mark sessions as one-shot
- Prevents auto-restart of critical sessions

## Commit 19ef4431 Fixes

**1. Prompt Injection Bug**
- Problem: Used --append-system-prompt flag (system context only)
- Fix: Use Prompt field as positional CLI argument
- Result: Claude starts working on user message

**2. EndedAt Not Set**
- Problem: Early return before UpdateItemSessionEnded
- Fix: Move UpdateItemSessionEnded before role check
- Result: EndedAt set for all roles

**3. OneShot Flag Ignored**
- Problem: OneShot=true but -p not added to launch command
- Fix: Add -p when OneShot=true
- Result: Claude exits after completion

## Triage Pipeline Flow

1. Trigger: User calls TriggerTriage(item_id)
2. Create: SessionCreator spawns instance with prompt and tags
3. Launch: buildLaunchCommand constructs launch command with -p flag
4. Execute: Claude receives prompt as user message
5. Complete: Agent calls submit_triage_result MCP tool
6. Exit: OneShot flag causes claude to exit after task (-p mode)
7. Update: BacklogLifecycleListener.onSessionExited sets EndedAt and transitions status

## Session Creation Interface

```
type SessionCreator interface {
    CreateDirectorySession(ctx context.Context, title, path, 
        prompt string, tags []string, oneShot bool) (*session.Instance, error)
}
```

BacklogService uses this to spawn triage, work, and review sessions.

## Key Design Patterns

**Orphan Detection**: Sessions orphaned if StartedAt=NULL or not live in memory
**Deterministic Names**: Format "triage:<slug>" for killing stale sessions
**Tag Classification**: "backlog:triage" and "backlog:review" tags prevent restart
**Lifecycle Listener**: BacklogLifecycleListener wired to each session for state transitions

## Summary

Technology combines:
- Go 1.25.0 plus ConnectRPC for cross-platform transport
- Protobuf v3 for RPC contracts
- ent ORM for persistence
- tmux for isolation
- OneShot flag (-p mode) for non-interactive exit
- Prompt as positional CLI argument (not --append-system-prompt)
- MCP tool integration for backlog state transitions
- Tag-based session classification
- Orphan session detection preventing state corruption

This architecture enables reliable, repeatable triage execution with automatic cleanup and state transitions.
