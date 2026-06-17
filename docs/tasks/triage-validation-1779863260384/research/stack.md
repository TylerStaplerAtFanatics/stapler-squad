# Stack Research — Triage Pipeline Validation

## Technologies

**Backend: Go 1.25.0**
- Module: `github.com/tstapler/stapler-squad`
- ORM: Ent v0.14.5 (`session/ent/schema`)
- RPC: ConnectRPC v1.19.0 (`*connect.Request[Msg]` / `*connect.Response[Msg]`)
- Protocol: Protobuf 3 (`make generate-proto`)
- MCP Go lib: `github.com/mark3labs/mcp-go v0.48.0`

**Frontend: React 19.0.0 + TypeScript**
- Next.js 15.3.2 (App Router)
- @connectrpc/connect-web v2.1.1
- vanilla-extract v0.5.7 (CSS)

## How Claude Code Gets Invoked for Triage

1. `TriggerTriage` RPC (`server/services/backlog_service.go:1058`) creates a one-shot triage session
2. Command built in `instance_tmux.go:buildLaunchCommand()`:
   - `--mcp-config` with HTTP MCP server URL + session UUID header
   - `-p` one-shot flag (added by fix in commit 19ef4431)
   - Prompt passed as positional CLI argument via `%q` formatting
3. `STAPLER_SESSION_UUID` env var injected for MCP tool context validation

## MCP Tool for Result Submission

`submit_triage_result` is registered in `server/mcp/tools_backlog.go:402`:
- Transport: HTTP Streamable at `/mcp` endpoint
- Session UUID extracted from `STAPLER_SESSION_UUID` env var
- Validates session linkage before persisting

## Session Types

- **Triage**: `OneShot=true`, `role="triage"`, title=`"triage:{slug}"`
- **Work**: `OneShot=false`, `role="work"`
- **Review**: `OneShot=true`, `role="review"`

## Key Files

| File | Purpose |
|------|---------|
| `server/mcp/tools_backlog.go` | MCP tool implementations |
| `server/services/backlog_service.go` | TriggerTriage RPC + buildTriagePrompt |
| `session/instance_tmux.go` | buildLaunchCommand, OneShot -p flag |
| `session/backlog_lifecycle.go` | onSessionExited, EndedAt recording |
| `server/mcp/server.go` | MCP server registration |
| `proto/session/v1/backlog.proto` | Protobuf definitions |

## Versions / Dependencies

| Dependency | Version |
|-----------|---------|
| Go | 1.25.0 |
| Ent ORM | 0.14.5 |
| ConnectRPC | 1.19.0 |
| mcp-go | 0.48.0 |
| React | 19.0.0 |
| Next.js | 15.3.2 |
| @connectrpc/connect-web | 2.1.1 |
