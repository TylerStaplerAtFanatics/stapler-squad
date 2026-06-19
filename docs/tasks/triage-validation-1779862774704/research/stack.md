# Stack Research: Triage Pipeline Validation

## Technologies

**Backend (Go 1.25.0)**
- Protocol Buffers v3 — RPC definitions
- ConnectRPC (connectrpc.com/connect v1.19.0) — HTTP/2 RPC transport
- Entgo ORM (entgo.io/ent v0.14.5) — SQLite-backed persistence
- MCP Go SDK (github.com/mark3labs/mcp-go v0.48.0) — Model Context Protocol server
- SQLite (github.com/mattn/go-sqlite3 v1.14.40) — primary data store

**Infrastructure**
- tmux — terminal session multiplexing for Claude agent isolation
- HTTP MCP transport — stapler-squad exposes `/mcp` endpoint with session UUID header auth

**Testing**
- Playwright (tests/e2e/) — E2E browser automation against live server
- Go testing (server/services/backlog_service_test.go) — unit/integration tests

## Key Files

| File | Role |
|------|------|
| `server/services/backlog_service.go:1056-1259` | TriggerTriage RPC + buildTriagePrompt |
| `server/mcp/tools_backlog.go:402-536` | submit_triage_result MCP tool handler |
| `server/mcp/server.go` | MCP server init, session UUID injection from header |
| `session/instance_tmux.go:26-52` | buildLaunchCommand — -p flag, MCP config injection |
| `session/backlog_lifecycle.go:97-153` | onSessionExited — sets ended_at for all roles |
| `proto/session/v1/backlog.proto` | TriageResult, ItemSession proto definitions |
| `tests/e2e/triage-pipeline-validation.spec.ts` | Full pipeline E2E validation test |

## Versions/Dependencies

```
go 1.25.0
connectrpc.com/connect v1.19.0
entgo.io/ent v0.14.5
github.com/mark3labs/mcp-go v0.48.0
github.com/mattn/go-sqlite3 v1.14.40
```

## Triage Flow Overview

1. `TriggerTriage` RPC validates item status, orphan guard, creates artifact dir
2. `buildTriagePrompt()` constructs instructions with item_id, description, AC, artifact path
3. `CreateDirectorySession()` spawns Claude with `OneShot=true` -> `-p` CLI flag
4. Tmux env sets `STAPLER_SESSION_UUID={UUID}`; `--mcp-config` injects HTTP MCP endpoint with session UUID header
5. Claude runs non-interactively: writes research artifacts -> calls `submit_triage_result`
6. `onSessionExited` fires, sets `ended_at`, UI updates to show triage review panel

## Recent Fix (Commit 19ef4431)

Three bugs fixed that completely broke the pipeline:
1. Prompt passed via `AppendSystemPrompt` (system context only) instead of `Prompt` (positional arg) — Claude got no instructions
2. `onSessionExited` early-returned before `UpdateItemSessionEnded` for non-work roles — `ended_at` never set
3. `buildLaunchCommand` didn't add `-p` flag despite `OneShot=true` — Claude stayed interactive
