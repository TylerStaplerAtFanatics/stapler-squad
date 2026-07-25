You are ready to ship your work as a pull request - either because /backlog/review
just returned PASS, or because review has looped without reaching a PASS and it is time
to hand the work to a human instead of retrying indefinitely.

Before shipping, confirm all acceptance criteria are marked complete by running
/backlog/status.

Steps:
1. Create the pull request: run /github:pr-ship. This drives the PR through local CI,
code review, remote CI, and merge-conflict resolution. It will stop short of actually
merging, the final merge is left to the human reviewer.
2. Once /github:pr-ship reports all gates green: if this work has NOT already received a
PASS verdict (you are shipping because review looped without converging, not because it
passed), request the automated review with the PR number included by running
/backlog/review with a 2-3 sentence summary of what was built and the PR number. If
review already returned PASS before you got here, skip this step, running it again will
fail since the item is no longer in_progress, and there is nothing left for it to check.

If the repository has no GitHub remote, run gh pr create manually. Do NOT use the --fill
flag, since it just concatenates commit messages with no test plan. Write the --title
using Conventional Commits format and a --body structured with a Summary section
describing why this change was made (from the backlog item above), a What Changed bullet
list, and a Test plan checklist of concrete verification steps. Then run /backlog/review.
