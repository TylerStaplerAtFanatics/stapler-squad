# Available Backlog Commands (sdd pipeline mode)

- /backlog/status - Show current item status and checklist
- /backlog/done-N and /backlog/fail-N for each of the 8 acceptance
criteria (see /backlog/status for the numbered list of valid N values)
- /backlog/review - Run sdd:6-verify, then submit for review with a summary
- /backlog/ship - Create a PR with /github:pr-ship and submit for review

This item runs the sdd pipeline mode: use sdd:2-research, sdd:3-plan, and sdd:4-validate
to plan your work before implementing, and sdd:6-verify before requesting review.
