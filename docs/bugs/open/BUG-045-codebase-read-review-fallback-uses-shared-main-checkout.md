# BUG-045: Codebase-Read Review Fallback Grants the Reviewer Live Filesystem Access to the Shared Main Checkout — Not the Item's Own State — When the Item's Worktree Is Gone [SEVERITY: High]

**Status**: 🔴 Open
**Discovered**: 2026-07-24, investigating a stale/wrong FAIL verdict on item `693c2700` while manually reopening it for revision
**Impact**: When a headless review's primary diff computation comes back empty (`getWorkSessionDiff` finds nothing) and the item's dedicated worktree has already been cleaned up, `resolveCodebaseWorkDir` (`server/services/backlog_service_triage.go:2358`) falls back to `repoPath` and the reviewer is granted live Read/Grep/Glob filesystem access to that directory as "the codebase to review." For every backlog item in this project, `repoPath` resolves to the same shared path: `/home/tstapler/Programming/stapler-squad` — the single main checkout this operator (and this Claude Code session) actively works in, merges into, and runs `git stash`/`make build`/`make install-service` against all day. Whatever uncommitted, unrelated, in-progress work happens to be sitting in that checkout's working tree at the exact moment a fallback review runs gets handed to the reviewer as if it were the item's own diff — producing a plausible-sounding but completely wrong FAIL verdict.

## Live Evidence

Item `693c2700` ("Expose ID functionality in Backlog"): its work session (`stapler-squad-add-backlog-item-id-deep-link`, spawned 2026-07-24 09:12) did the real, correct work — item ID display, copy/deep-link, board-pane restore — went through a full 3-agent code review + e2e test cycle, and opened a clean **PR #216** with all 22 CI checks green. The session's own final summary flagged the problem itself:

> "The one loose end is that this backlog item's own status tracking is stuck (visible 'FAIL' verdict on the item is actually stale, describing a completely different, unrelated diff from an earlier session)."

The review session that produced that FAIL (`review:693c2700`, spawned 09:15, dead by 09:25) reported:

> "Review submitted: overall **FAIL**. The worktree's diff is entirely unrelated to this backlog item — it contains a tmux orphaned-client fix (BUG-042) and log-stream debug gating changes, not any of the required ID display/copy/deep-link/board-pane work described in the plan."

That description — "tmux orphaned-client fix" and "log-stream debug gating changes" — matches, almost word for word, an entirely unrelated, long-running **uncommitted** piece of work (fixing BUG-042) that has been sitting in this operator's main checkout (`/home/tstapler/Programming/stapler-squad`) all day, repeatedly `git stash push`/`git stash pop`'d around unrelated merge-and-deploy cycles for BUG-040/041/043/044. The item's worktree (`stapler-squad-expose-backlog-item-id_18c4cdae22fbef0b`) had been reaped by the time this review ran (confirmed: `worktrees` table's `repo_path` column for this item is `/home/tstapler/Programming/stapler-squad`, and the dedicated worktree directory no longer exists on disk) — so the review fell into the codebase-read fallback, and read *this operator's own unrelated in-progress work* off the live filesystem instead.

## Root Cause

`resolveCodebaseWorkDir` (`server/services/backlog_service_triage.go:2358-2367`):
```go
func (s *BacklogService) resolveCodebaseWorkDir(ctx context.Context, repoPath string, workSession *session.ItemSessionSummary) (dir string, exists bool) {
	dir = repoPath
	if workSession != nil {
		if wt, wtErr := s.storage.GetWorktreeDataBySessionUUID(ctx, workSession.SessionUUID); wtErr == nil && wt.WorktreePath != "" {
			dir = wt.WorktreePath
		}
	}
	info, statErr := os.Stat(dir)
	return dir, statErr == nil && info.IsDir()
}
```
When the worktree row's directory no longer exists on disk, `dir` stays `repoPath` — the shared main checkout — and `exists` is `true` (the main checkout obviously exists), so the caller proceeds into codebase-read mode using it. Contrast this with `getWorkSessionDiff`'s fallback (same file, ~line 2380), which is already careful about exactly this trap: it uses `GetGitDiffRef` with an *explicit branch ref* precisely so the diff reads from the git object store (branch tip vs base SHA) rather than the fallback directory's live working-tree/HEAD state — the code even has a comment calling this out: *"dir's own checked-out HEAD is used as the diff target. That's correct when dir is the session's own worktree... but wrong when dir is a fallback directory such as the shared main repo checkout."* `resolveCodebaseWorkDir`'s codebase-read fallback has no equivalent protection: Read/Grep/Glob tools operate on the literal filesystem, which has no notion of "diff against a ref" — there's no way to scope filesystem tool access to "only what's committed on branch X" without either (a) refusing the fallback entirely, or (b) materializing the branch into a throwaway location first.

## Suggested Fix Direction

1. **Refuse the codebase-read fallback when there's no real per-item worktree**, mirroring `ReviewGateRunner.Run`'s established refusal to hand the reviewer a diff it can't positively compute (referenced directly in `resolveCodebaseWorkDir`'s own doc comment as the pattern to follow). A review that can't get real, isolated evidence should report an infrastructure/inconclusive state — not silently review the wrong thing and call it FAIL.
2. If codebase-read mode is worth keeping as a fallback at all, it must operate on a *materialized, isolated copy* of the item's actual branch (e.g. a throwaway `git worktree add` at the base SHA + branch, cleaned up after), never the shared main checkout's live, arbitrary working-tree state.
3. Shorter-term, cheaper mitigation: since this shares its root trigger with BUG-040's "worktree row outlives worktree directory" finding, tightening worktree-reap timing/preconditions (don't reap a worktree an item might still need for review) may reduce how often this fallback path is even reached, independent of hardening the fallback itself.

## Recommended Routing

`sdd:fix-bug` for the minimal, well-scoped version (refuse-with-inconclusive-state, matching `ReviewGateRunner.Run`'s existing pattern) — the throwaway-worktree-materialization option is more involved and may warrant its own follow-up if the minimal fix isn't sufficient. Concrete repro: reproducing this exactly requires an item with a reaped worktree and an empty primary diff at the same moment the shared main checkout happens to be dirty with unrelated content — hard to force deterministically, but the code-level defect (no working-tree isolation in the fallback) is confirmed by inspection regardless of live reproduction. `693c2700` itself doesn't need re-fixing for this specific bug — its real PR (#216) is already correct and ready to merge; only its *displayed* status/verdict is wrong, and that's a downstream symptom.
