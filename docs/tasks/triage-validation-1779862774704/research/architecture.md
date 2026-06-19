# Architecture Research: Triage Pipeline

## System Components

BacklogService (backlog_service.go) -> TriggerTriage() -> SessionService.CreateDirectorySession()
-> Instance (instance_tmux.go) -> buildLaunchCommand() -> spawns claude -p with mcp-config
-> MCP HTTP Server (/mcp endpoint) -> submit_triage_result tool handler

## Data Flow

1. User calls TriggerTriage RPC
2. Validate: status=idea/ready, repo_path set, no live orphan session
3. mkdir docs/tasks/{slug}/
4. Kill stale tmux "triage:{slug}" if exists
5. buildTriagePrompt(item) -> prompt string with item_id + absolute artifact path
6. CreateDirectorySession(prompt, OneShot=true, tags=["backlog:triage"])
   -> NewInstance(opts) with OneShot=true
   -> instance.Start() -> tmux new-session -> claude -p "$PROMPT" --mcp-config ...
7. CreateItemSession(item_id, session_uuid, role=triage)

Claude agent (oneshot -p mode):
- Receives prompt with item_id + absolute artifact path
- Spawns parallel subagents for research/*.md
- Writes plan.md, validation.md
- Calls submit_triage_result MCP tool via HTTP POST to /mcp
- claude exits (oneshot -p mode)

Session exit:
- onSessionExited fires (backlog_lifecycle.go:97-153)
- UpdateItemSessionEnded sets ended_at (FIXED in 19ef4431)
- UI polls, shows triage-review-panel when ended_at is set

## Session Lifecycle

States:      Creating -> Active -> Exited
ItemSession: created (started_at set) -> completed (ended_at set)

## MCP Integration

MCP config injected at launch (instance_tmux.go) includes HTTP transport pointing to
localhost:{port}/mcp with X-Stapler-Session-UUID header for authentication.

Session UUID threading:
1. STAPLER_SESSION_UUID env var set in tmux environment
2. X-Stapler-Session-UUID HTTP header on every MCP request
3. MCP HTTP handler reads header, injects into context via WithSessionUUID()
4. callerSessionUUID(ctx) validates session is linked to item with role=triage

## Prompt Construction

buildTriagePrompt() embeds:
- Role framing: "You are a senior software architect..."
- Item data: item_id UUID, title, description, AC list (numbered)
- Artifact path: absolute docs/tasks/{slug}/ path for writing research files
- Step-by-step instructions: parallel research -> synthesis -> validation -> MCP call

CRITICAL: Prompt is the positional Prompt field passed as "claude -p PROMPT"
-- not AppendSystemPrompt (this was the bug fixed in 19ef4431).
