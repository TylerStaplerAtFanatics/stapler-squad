# Stack Research

## Go and Key Dependencies
- ConnectRPC (connectrpc.com/connect) for BacklogService RPCs
- Ent ORM (entgo.io/ent) for BacklogItem, ItemSession entities
- MCP server (github.com/mark3labs/mcp-go) exposes submit_triage_result to Claude
- tmux 3.4 (pinned, embedded) for session isolation

## Claude CLI Invocation (session/instance_tmux.go:26)

buildLaunchCommand assembles the final CLI string. Key flags after fix 19ef4431:
- -p: added when OneShot=true; makes Claude non-interactive, run-and-exit
- Positional prompt: i.Prompt appended as last CLI arg (triggers immediate work)
- --mcp-config: injects stapler-squad HTTP MCP server URL
- --dangerously-skip-permissions: set when AutoYes=true (all triage sessions)

Pre-fix bugs: prompt was --append-system-prompt (system context, no user turn)
; -p was never added (session stayed interactive forever).

## MCP Server
HTTP MCP at MCPServerURL (default http://localhost:8543/mcp):
- submit_triage_result(item_id, summary, suggestions, tasks, plan_artifact_path)
- get_backlog_item(item_id)
- STAPLER_SESSION_UUID injected via tmux SetExtraEnv for per-session auth

## Triage Session Type
- SessionTypeDirectory at item.RepoPath
- OneShot=true, AutoYes=true
- Tags: backlog:triage
- Title: triage:<slug> (deterministic, stale-session cleanup on re-trigger)
- ItemSession.SessionRole: triage (gates submit_triage_result permission)