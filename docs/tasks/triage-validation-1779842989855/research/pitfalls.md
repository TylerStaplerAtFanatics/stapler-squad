# Triage Pipeline Pitfalls & Known Failure Modes

## Executive Summary
The stapler-squad triage pipeline has experienced three categories of critical failures that prevented triage from actually executing:
1. **Prompt Injection Bug**: Triage instructions were delivered via system context only, blocking Claude from starting work
2. **Session Lifecycle Tracking**: EndedAt never recorded for non-work sessions, UI stuck permanently in "running" state
3. **OneShot Flag Ignored**: Sessions stayed interactive despite being configured as one-shot

Additionally, two secondary systemic issues create edge cases:
4. **Session List Wipe on Service Updates**: UpdateSession/RenameSession could silently orphan sessions
5. **Race Conditions on Session Startup**: Timing windows between MCP connection, prompt delivery, and lifecycle event firing

---

## Critical Failures (Fixed in commit 19ef4431)

### 1. Prompt Injection — Triage Never Starts (Claude waits for user input)

**Root Cause:**
- `CreateDirectorySession()` accepted `appendSystemPrompt` parameter
- Parameter was mapped to `InstanceOptions.AppendSystemPrompt` (becomes `--append-system-prompt` CLI flag)
- Claude receives `--append-system-prompt` instruction in system context only
- **Claude architecture**: Agent waits for user message to start working when no initial prompt is provided as positional argument
- Triage sessions had no initial user message, so Claude remained idle indefinitely

**Evidence in Code:**
```go
// BEFORE (broken):
func (s *SessionService) CreateDirectorySession(
    ctx context.Context, 
    title, path, appendSystemPrompt string,  // <-- system context only
    tags []string, oneShot bool
) (*session.Instance, error) {
    opts := session.InstanceOptions{
        AppendSystemPrompt: appendSystemPrompt,  // <-- becomes --append-system-prompt
    }
}

// AFTER (fixed):
func (s *SessionService) CreateDirectorySession(
    ctx context.Context, 
    title, path, prompt string,  // <-- positional prompt
    tags []string, oneShot bool
) (*session.Instance, error) {
    opts := session.InstanceOptions{
        Prompt: prompt,  // <-- becomes positional arg
    }
}

// In buildLaunchCommand:
if i.Prompt != "" && claudeSessionID == "" && strings.Contains(program, "claude") {
    program = fmt.Sprintf("%s %q", program, i.Prompt)  // <-- Prompt is positional
}
```

**Failure Mode:**
- Triage triggered, session started, triage window opened
- Loading indicator visible (session running)
- **Claude never receives any message**
- Claude waits indefinitely for first user input
- After 6-minute timeout, test fails with "triage panel never appeared"
- UI stuck showing "running" because endedAt never set

**Detection Signals:**
- Triage session status stays `running` indefinitely
- No MCP calls attempted (Claude never started working)
- `.backlog-context.md` file never created in session worktree
- Session exits after timeout or manual cancellation

### 2. EndedAt Never Set for Non-Work Sessions (UI permanently "running")

**Root Cause:**
- `BacklogLifecycleListener.onSessionExited()` had early return for non-work roles
- Only work sessions triggered `UpdateItemSessionEnded()`
- Triage and review sessions exited but `endedAt` remained `NULL`
- UI queries `endedAt IS NULL` to determine if session is running

**Evidence in Code:**
```go
// BEFORE (broken):
func (l *BacklogLifecycleListener) onSessionExited(sessionUUID string) {
    // ... get ItemSession ...
    
    // ❌ WRONG: Guards before recording end time
    if is.SessionRole != SessionRoleWork {
        return  // <-- Early exit! endedAt never recorded
    }
    
    now := time.Now()
    if err := l.storage.UpdateItemSessionEnded(ctx, is.ID.String(), now); err != nil {
        // ...
    }
}

// AFTER (fixed):
func (l *BacklogLifecycleListener) onSessionExited(sessionUUID string) {
    // ... get ItemSession ...
    
    // ✅ CORRECT: Record end time for ALL session roles
    now := time.Now()
    if err := l.storage.UpdateItemSessionEnded(ctx, is.ID.String(), now); err != nil {
        // ...
    }
    
    // Only drive transitions for work sessions
    if is.SessionRole != SessionRoleWork {
        return  // <-- Guard moved after UpdateItemSessionEnded
    }
}
```

**Failure Mode:**
- Triage session exits (via timeout, error, or completion)
- ItemSession.EndedAt remains NULL
- UI displays running spinner indefinitely
- Cannot trigger new triage (CodeAlreadyExists guard sees no endedAt)
- Orphan guard in TriggerTriage only tombstones if `statusAdvanced || notLive` — stale sessions stuck

**Detection Signals:**
- ItemSession row in DB has `started_at` populated but `ended_at` is NULL
- UI loading indicator never disappears
- Second TriggerTriage fails with "triage session already running"
- Manual DB query: `SELECT * FROM item_sessions WHERE ended_at IS NULL AND session_role='triage'`

### 3. OneShot Flag Ignored (Sessions stay interactive after finishing)

**Root Cause:**
- Triage/review sessions set `OneShot=true` during creation
- `buildLaunchCommand()` checks many flags but `OneShot` was never used
- **Missing implementation**: No `-p` (print/non-interactive) flag added to claude CLI
- Sessions stay in interactive mode waiting for EOF after work finishes

**Evidence in Code:**
```go
// BEFORE (broken):
func (i *Instance) buildLaunchCommand(claudeSessionID string) string {
    // ... handle resume, mcp-config, append-system-prompt, AutoYes ...
    
    // ❌ MISSING: OneShot flag not used
    // if i.OneShot && strings.Contains(program, "claude") {
    //     program = program + " -p"  // <-- Not added
    // }
    
    if i.Prompt != "" && claudeSessionID == "" {
        program = fmt.Sprintf("%s %q", program, i.Prompt)
    }
    return program
}

// AFTER (fixed):
func (i *Instance) buildLaunchCommand(claudeSessionID string) string {
    // ... handle other flags ...
    
    // ✅ ADDED: OneShot maps to -p flag
    if i.OneShot && strings.Contains(program, "claude") {
        program = program + " -p"
    }
    
    if i.Prompt != "" && claudeSessionID == "" {
        program = fmt.Sprintf("%s %q", program, i.Prompt)
    }
    return program
}
```

**Failure Mode:**
- Triage/review session finishes work and calls `submit_triage_result` MCP
- MCP succeeds, session continues waiting for user input
- Claude never exits on its own; waits at interactive prompt
- EventExited never fires (session still running)
- Lifecycle listener never calls onSessionExited
- ItemSession.EndedAt stays NULL
- Timeout-based session exit (6+ minutes later)

**Detection Signals:**
- Session stays open after MCP call completes
- Claude shows interactive prompt (user can type commands)
- Manual tmux `capture-pane -p` shows `claude> ` at end
- LaunchCommand in Instance.json missing `-p` flag

---

## Secondary Systemic Issues

### 4. Session List Wipe on Service Updates (Fixed in commit 39b73465)

**Root Cause:**
- `UpdateSession()` and `RenameSession()` called `LoadInstances()` to refresh the poller
- `LoadInstances()` → `FromInstanceData()` → calls `Start()` on every Active session
- `Start()` on unavailable tmux sessions silently drops them from returned list
- `SetInstances()` then **replaced** the entire poller list with this truncated list
- Sessions disappeared from `ListSessions` (reads poller) but remained in DB

**Failure Mode:**
- Session created successfully, appears in UI
- User renames session or updates metadata
- UpdateSession triggers LoadInstances → SetInstances clobber
- Session vanishes from ListSessions if tmux process unavailable
- Re-create session request fails: "already exists" (reads DB not poller)
- Duplicate check reads raw DB, not live list → conflict

**Edge Case:**
- Server restart while sessions paused
- tmux not yet running when UpdateSession called
- New sessions can't be created with same name (DB unique constraint)
- Old session orphaned: not in poller, won't be reconciled

**Fix:**
Bypass LoadInstances side-effect:
```go
// Use live poller directly, avoid Start() on all Active sessions
var instances []*session.Instance
if s.reviewQueuePoller != nil {
    instances = s.reviewQueuePoller.GetInstances()  // ✅ Live list
} else {
    instances, _ = s.loadInstancesWithWiring()
}
// Don't call SetInstances() — already have current list from poller
```

### 5. Timing/Race Conditions on Session Startup

**Race Window 1: MCP Connection Established After Prompt Delivery**
- Instance.Start() fires EventStarted before MCP server necessarily responds to ping
- Lifecycle listener fires onSessionStarted goroutine immediately
- Triage system may record ItemSession.StartedAt before MCP is ready
- If MCP call fails shortly after, ambiguous whether "started" means MCP-ready

**Race Window 2: Prompt Delivered vs. Claude Ready to Accept Input**
- `buildLaunchCommand()` adds Prompt as positional arg at CLI time
- tmux may still be setting up PTY when program executes
- Claude startup race: reads prompt from STDIN vs. receives it on command line
- Could result in prompt not being visible/parsed if tmux startup slow

**Race Window 3: Lifecycle Event Fired Before Session Fully Online**
- EventStarted fired at end of `start()` but controller not yet fully initialized
- `StartController()` can fail and retry, async VNC/CDP startup separate
- Concurrent lifecycle listeners could query session state before controller ready
- MCP tool calls fail with "session not available" despite EventStarted fired

**Detection Signals:**
- MCP timeouts in first 2-3 seconds of session start
- "session not available" errors from MCP calls on freshly started sessions
- Intermittent failures only during rapid start-stop cycles
- Session status transitions appear out-of-order in logs

---

## Known Hardest-to-Detect Edge Cases

### 1. Silent Orphan Sessions (Poller/DB Desync)
**Problem:** Session disappears from UI but exists in DB
- No error messages in logs
- User can't recover the session
- Disk space leaked (worktree still exists but unreachable)
- Re-trigger creates duplicate

**Detection:** Query DB for sessions not in poller:
```sql
SELECT s.id, s.title, s.status FROM sessions s
WHERE s.id NOT IN (SELECT id FROM poller_cache)
  AND s.status IN ('active', 'paused')
```

### 2. Hung Triage Sessions (Prompt Never Delivered)
**Problem:** Session running but Claude never received prompt
- No errors in logs (session is technically "running")
- MCP calls never attempted
- Test timeout only failure signal
- Hard to distinguish from slow work

**Detection:** Check if `.backlog-context.md` exists in session after 5 seconds:
```bash
if [ -z "$(ls -1 /path/to/session/.backlog-context.md 2>/dev/null)" ]; then
    echo "Prompt not delivered — session hung"
fi
```

### 3. Stale Triage Sessions After Server Restart
**Problem:** Pre-restart session still in DB as "running"
- orphan guard only tombstones if: not-live OR statusAdvanced
- If status still "idea" and session process dead after reboot, orphan detection fails
- Manual check for StartedAt=NULL catches only never-started sessions

**Detection:** Check for `started_at IS NOT NULL AND ended_at IS NULL AND (NOW() - started_at) > 15 minutes`

### 4. OneShot Flag Silently Ignored (Hard to Notice)
**Problem:** Looks like normal session, actually interactive
- User doesn't notice session didn't exit (happens after MCP call, session looks done)
- No error messages (interactive mode is "valid", just not intended)
- LaunchCommand logged but rarely reviewed
- Affects triage/review sessions only, so limited scope initially

**Detection:** Check Instance.json or tmux pane:
```bash
tmux capture-pane -t $SESSION -p | tail -1 | grep -E "claude>|>>>" || echo "Not interactive"
```

### 5. MCP Server Unavailable During Triage Execution
**Problem:** Session starts successfully but MCP calls fail mid-work
- Triage can't call `submit_triage_result`
- ItemSession.EndedAt stays NULL (exit handler fires but MCP failure not logged)
- User's work (research files) already written to disk but not submitted
- UI shows session "running" forever

**Detection:** 
- Monitor MCP server connectivity before session start
- Log all MCP call attempts with timestamps
- Check for artifact files but missing ItemSession.ended_at:
```sql
SELECT item_id FROM item_sessions 
WHERE session_role='triage' AND ended_at IS NULL 
  AND created_at < NOW() - INTERVAL '10 minutes'
```

---

## Hardest-to-Debug Root Causes

### Why Prompt-Injection Bug Was Subtle
1. System prompt IS delivered (--append-system-prompt works technically)
2. Session IS running (tmux process active, accepting input)
3. No error messages (Claude is waiting, not erroring)
4. Takes full timeout (6 min) to fail in e2e test
5. Only detected by checking MCP call logs (none attempted)

### Why OneShot Bug Was Missed
1. Code path exists (OneShot field defined, passed to Instance)
2. Similar fields checked in buildLaunchCommand (resume, mcp-config, append-system-prompt)
3. Looks intentional but incomplete (not all flags added)
4. Sessions technically work (interactive mode is valid, just wrong mode)
5. Only affects short-lived automated sessions (easy to miss in manual testing)

### Why Session Lifecycle Was Fragile
1. Early-return guard was reasonable (skip status transitions for triage/review)
2. But guard placement (before vs. after UpdateItemSessionEnded) changed semantics
3. UI behavior dependent on single column (endedAt IS NULL)
4. No consistency check between DB state and actual process status
5. Multiple code paths to record end time (lifecycle listener + manual cleanup)

---

## Prevention & Validation Checklist

### Pre-Commit Validation
- [ ] **Prompt delivery**: Verify Prompt vs. AppendSystemPrompt semantics in buildLaunchCommand
- [ ] **Lifecycle symmetry**: Recording start/end time for ALL session roles, not just work
- [ ] **Flag completeness**: If adding new boolean flag to Instance, check all uses in buildLaunchCommand
- [ ] **Poller consistency**: Never mix LoadInstances() side-effects with SetInstances() clobber
- [ ] **MCP resilience**: Test session startup when MCP server unavailable

### E2E Test Coverage
- [ ] Triage session: check `.backlog-context.md` exists within 5 seconds of start
- [ ] Triage completion: verify ItemSession.ended_at is set after session exits
- [ ] OneShot behavior: confirm claude exits without interactive prompt after work finishes
- [ ] Session lifecycle: verify no sessions in DB with ended_at=NULL after 15+ minutes
- [ ] Rapid updates: trigger multiple UpdateSession calls while sessions active (poller sync test)

### Operational Monitoring
- [ ] Alert on: ItemSession with ended_at=NULL and started_at older than 15 minutes
- [ ] Alert on: Session in DB but not in poller (orphan detection)
- [ ] Monitor: MCP call latency spike during session startup
- [ ] Log: All oneshot=true session exits (verify they exit promptly)
- [ ] Dashboard: Triage session duration histogram (detect hung sessions >10 min)

---

## References

- **Commit Fix**: `19ef4431` — repair triage pipeline (prompt injection, session exit, oneshot flag)
- **Commit Fix**: `39b73465` — prevent session list wipe on UpdateSession/RenameSession
- **Test Coverage**: `triage-pipeline-validation.spec.ts` — e2e validation (needs duration/artifact checks)
- **Key Files**:
  - `session/backlog_lifecycle.go` — lifecycle event handling
  - `session/instance_tmux.go` — buildLaunchCommand implementation
  - `server/services/backlog_service.go` — TriggerTriage logic, orphan guard
  - `server/services/session_service.go` — poller management, UpdateSession
