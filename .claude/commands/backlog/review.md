Before requesting review, run the sdd:6-verify skill against your changes: it
dispatches parallel idiom and architecture review agents, then runs a correctness and
test gate. Resolve every BLOCKER and REFACTOR finding it surfaces. Only continue once it
reports PASS, or you have deliberately accepted and documented any remaining CONCERNS.

Once sdd:6-verify is clean, call request_review with item_id=7728f6df-268a-4578-9066-c300ff69269b and a 2-3
sentence summary of what was built, including the sdd:6-verify verdict.

Do NOT end your session after this. Wait a bit, then call get_backlog_item (or run
/backlog/status) again - the verdict appears under Latest Review Verdict once the
reviewer submits it.

PASS leads to running /backlog/ship now to open the pull request yourself - it drives
/github:pr-ship through local CI, code review, remote CI, and merge-conflict resolution.
Do not stop here, shipping the PR is part of this task, not a separate step someone else
does.

FAIL or PARTIAL means fixing the noted gaps in this same session and running
/backlog/review again. Keep count of how many times you have run /backlog/review in THIS
session - nothing tracks it for you. After 3 review cycles without a PASS, stop looping:
run /backlog/ship anyway to open a PR so a human can pick up the review directly, rather
than retrying /backlog/review again.
