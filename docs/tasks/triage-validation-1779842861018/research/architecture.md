# Architecture Research

## Component Flow

Operator browser
  |-- TriggerTriage RPC -->
BacklogService (server/services/backlog_service.go)
  | validate item, build slug, kill stale session
  | buildTriagePrompt -> i.Prompt (positional CLI arg)
  |-- CreateDirectorySession -->
SessionService
  | Instance{OneShot=true, AutoYes=true, Prompt=triagePrompt, MCPServerURL}
  | buildLaunchCommand: claude --mcp-config ... -p "<prompt>"
  |-- start tmux session with STAPLER_SESSION_UUID env var -->
Claude CLI (oneshot)
  | reads full triage instructions from positional arg
  | spawns 4 research subagents (parallel)
  |   writes research/*.md to artifactAbsPath
  | writes plan.md and validation.md
  |-- submit_triage_result MCP call -->
MCP HTTP handler (server/mcp/tools_backlog.go)
  | auth: STAPLER_SESSION_UUID -> ItemSession.SessionRole==triage
  | UpdateItemSessionTriageResult (persists JSON)
  | UpdateBacklogItem.PlanArtifactsPath
  | publish INPUT_REQUIRED notification
Claude CLI exits (OneShot -p)
  |-- EventExited -->
BacklogLifecycleListener.onSessionExited
  | UpdateItemSessionEnded (all roles, post-fix)
  | non-work role guard: no status transition for triage

## BacklogItem State Machine (triage path)

idea --[TriggerTriage]--> idea (running triage session)
                              |
                         [submit_triage_result called + endedAt set]
                              |
                              v
               UI shows TriageReviewPanel (operator approves plan)
                              |
                         [operator approves]
                              v
                            ready --[SpawnSession]--> in_progress

## Key Data Structures

ItemSession fields relevant to triage:
- SessionUUID: links to tmux Instance
- SessionRole: triage | work | review
- StartedAt / EndedAt: lifecycle timestamps
- TriageResult: JSON blob {summary, suggestions, tasks}
- AcSnapshot: acceptance criteria snapshot at session start
- PlanArtifactsPath: on BacklogItem, set by submit_triage_result

## Authentication Model

MCP server middleware injects STAPLER_SESSION_UUID from the session UUID header.
submit_triage_result verifies: GetItemSessionBySessionAndItem(callerUUID, itemID)
and checks SessionRole == triage before allowing the call.

## Validation Trigger (tests/e2e/triage-pipeline-validation.spec.ts)

The e2e test validates the full pipeline by:
1. Creating a backlog item with repo_path set
2. Triggering triage via the UI
3. Confirming loading indicator appears (session started)
4. Waiting up to 5 minutes for data-testid=triage-review-panel
5. Verifying summary text is non-empty