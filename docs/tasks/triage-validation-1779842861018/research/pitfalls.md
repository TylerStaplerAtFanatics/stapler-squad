# Pitfalls Research

## Bug 1: Prompt Injection
Root cause: prompt was --append-system-prompt (system context), not positional arg.
Claude in OneShot mode had no user turn to start working from.
Fix: Mapped to i.Prompt field -> positional CLI arg.

## Bug 2: EndedAt Never Set for Triage Sessions
Root cause: onSessionExited early-returned for non-work roles BEFORE UpdateItemSessionEnded.
Fix: UpdateItemSessionEnded called unconditionally before role guard.

## Bug 3: OneShot Flag Ignored
Root cause: OneShot=true was set but buildLaunchCommand never added the -p flag.
Fix: Added OneShot guard in buildLaunchCommand (session/instance_tmux.go:41-43).

## Remaining Risks

### MCP Tool Unavailability
If MCPServerURL is empty/unreachable, submit_triage_result fails silently.
Claude exits (OneShot) but triage_result never persists; no TriageReviewPanel appears.

### Subagent Auth Failures
Triage spawns 4 parallel subagents. If API credentials unavailable, subagents fail.
Claude may still call submit_triage_result with partial research (acceptable degradation).

### Double-Trigger Race
Rapid re-trigger may tombstone the live session (started_at=NULL before EventStarted).
Orphan guard is correct but confusing to operators triggering quickly.

## Verification
1. Run e2e: TRIAGE_VALIDATION=true npx playwright test triage-pipeline-validation.spec.ts
2. Server logs: [TriggerTriage] spawned ... then [mcp:submit_triage_result] triage_result=...
3. UI: data-testid=triage-review-panel appears within 6 min
4. DB: ItemSession has triage_result JSON and non-null endedAt