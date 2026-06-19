# Features Research

## End-to-End Triage Flow

1. Operator clicks Trigger Triage in backlog detail UI
2. TriggerTriage RPC (server/services/backlog_service.go:1058):
   a. Load item; validate status (idea or ready) and repo_path
   b. Orphan guard: tombstone any open triage ItemSession no longer live
   c. If item is ready, reset to idea
   d. Build slug and artifactAbsPath (docs/tasks/<slug>/)
   e. Kill stale tmux session with title triage:<slug>
   f. os.MkdirAll(artifactAbsPath)
   g. buildTriagePrompt(item, artifactAbsPath, slug) -> multi-step instructions
   h. CreateDirectorySession(title=triage:<slug>, path=item.RepoPath, prompt=..., oneShot=true)
   i. CreateItemSession with role=triage, AcSnapshot
3. Session lifecycle (session/backlog_lifecycle.go):
   - EventStarted -> UpdateItemSessionStarted
   - EventExited -> UpdateItemSessionEnded for ALL roles (post-fix)
   - Work-role exit: drive in_progress->review/done state machine
   - Triage-role exit: only EndedAt is set; status stays idea

## Prompt Injection

buildTriagePrompt (server/services/backlog_service.go:1186) builds:
- Role preamble: You are a senior software architect...
- Item title, item_id, description, acceptance criteria
- Step-by-step instructions: 4 parallel research subagents -> plan.md -> validation.md -> submit_triage_result
- plan_artifact_path embedded as absolute path

The prompt is passed as i.Prompt (positional CLI arg), not --append-system-prompt. This is the fix.

## submit_triage_result MCP Tool (server/mcp/tools_backlog.go:402)

1. Verify STAPLER_SESSION_UUID in context
2. Validate item_id and summary (required)
3. Verify ItemSession(session_uuid, item_id) with role=triage
4. Parse suggestions array (text + rationale)
5. Parse tasks array (text + estimate + category), cap at 12
6. UpdateBacklogItem.PlanArtifactsPath if plan_artifact_path provided
7. UpdateItemSessionTriageResult (persists JSON to ItemSession.triage_result)
8. Publish NOTIFICATION_TYPE_INPUT_REQUIRED event via EventBus
9. Return success message

## Existing Tests

- session/backlog_lifecycle_test.go -- lifecycle transition unit tests
- session/backlog_integration_test.go -- includes TestTriggerTriage_DoubleTriggerGuard
- server/services/backlog_service_test.go -- TriggerTriage handler tests
- server/mcp/tools_backlog_test.go -- MCP tool unit tests
- tests/e2e/triage-pipeline-validation.spec.ts -- NEW live e2e test (TRIAGE_VALIDATION=true)

## Frontend: TriageReviewPanel

web-app/src/components/backlog/TriageReviewPanel.tsx:
- Shown when item has triage_result and endedAt set on triage ItemSession
- data-testid=triage-review-panel
- Displays summary, suggestions (rationale != question), tasks checklist