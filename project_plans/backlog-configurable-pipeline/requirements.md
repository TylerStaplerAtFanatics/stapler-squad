# Requirements: backlog-configurable-pipeline

**Date**: 2026-07-15
**Type**: feature addition (system design — touches data model, proto, backend orchestration, UI)
**Complexity**: 3 — system design, multiple layers must move in lockstep, no external integration

Source: bucket [3] of `docs/tasks/backlog-feature-improvement.md` (audit run 2026-07-14 by the
`backlog-feature-improvement` skill). Per that skill's Phase 5 routing table, this requirements
doc is hand-written directly from the audit findings — the `sdd:1-ideate` interview is skipped
because the audit already answered problem/why. Each requirement below is traceable back to a
specific finding; see the "Source" line under each.

## Problem Statement

The backlog feature's stated end goal (`.claude/skills/backlog-feature-improvement/SKILL.md`,
opening line) is: an item goes idea → shipped PR with minimal human intervention, and the
pipeline stages themselves (triage → plan → implement → review → merge) are **configurable
per item** — a user should be able to say "use `/sdd:full` for this item" or "skip the
planning stage for this one, it's trivial."

Today the pipeline is fixed and hardcoded. Bucket [2] of the same audit (now closed) proved
that most *manual-click* friction was already solvable with narrow per-item bool flags
(`SkipReviewGate`, `SkipPlanning`, `AutoSpawnSession`). Bucket [3] is different in kind: no
existing flag lets a user choose *which skills or commands run* for an item, or *how many
stages* a pipeline has. That is a data-model and orchestration gap, not a UI-click gap.

## Baseline

Today, every backlog item gets the identical fixed slash-command set
(`session/backlog_commands.go:20-100`, `WriteSlashCommands`) and the identical fixed sequence
of stages (triage → review, hardcoded in `session/backlog_lifecycle.go` and
`session/review_gate.go`). The only per-item variation is three boolean short-circuits
(`SkipReviewGate`, `SkipPlanning`, `AutoSpawnSession`) that skip a stage entirely — none let a
user *substitute* a different stage, skill, or command. A user who wants "this item should go
through my `/sdd:full` workflow" or "this item only needs a quick fix, use `/sdd:quick`" has no
way to express that; they'd have to manually drive a session themselves, defeating the
automation. `session/repository.go:330-357` (`BacklogItemData`) has no field that could even
hold such a choice today.

## Users / Consumers

- The backlog UI operator (currently just the repo owner) configuring items via
  `BacklogItemForm.tsx` / `BacklogItemDetail.tsx`.
- The backend orchestration code that currently hardcodes stage sequencing:
  `session/backlog_lifecycle.go` (`BacklogLifecycleListener`), `session/review_gate.go`
  (`ReviewGateRunner`), `session/autonomous_driver.go`, `server/services/backlog_service_triage.go`.
- Downstream: `web-app/src/components/backlog/BacklogBoard.tsx` (`COLUMNS`), which will need to
  reflect whatever stage set an item is actually configured to run, not a fixed 6-status list.

## Success Metrics

- A user can set, per backlog item, which skill/command set drives triage and implementation
  (e.g. "default", "sdd:quick", "sdd:full") and the pipeline actually invokes that choice —
  verified by inspecting the slash-commands written into the item's worktree
  (`WriteSlashCommands` output) and/or the prompt handed to the headless/triage call.
- The UI surfaces, for any item, *what pipeline/skills ran or will run* — closing the UX-review
  finding that no current screen shows this (bucket [3] audit, UX pass).
- Adding a new pipeline mode requires *extending* a registry/config, not editing
  `WriteSlashCommands`'s body — closing the OCP violation flagged in the audit.
- Existing items with no explicit pipeline choice behave exactly as today (default pipeline) —
  zero regression for the current single-pipeline behavior.

## Appetite

Large (3–6 weeks). This is explicitly the "core software-factory gap" per the audit's
`is-it-ready` verdict (Architecture 🔴, Goal Compliance 🔴) — not a quick patch. Scope must be
cut (see Out of Scope) to fit; do not extend the timeline instead.

## Constraints

- Must reuse the narrow-interface + deep-copy-on-construct pattern already established by
  `session/workflow_engine.go` (`WorkflowEngine`) rather than inventing a new abstraction
  style — flagged in the audit as "positive pattern to reuse," and required by
  `.claude/rules/interface-pollution-checklist.md` (no speculative interfaces, no
  Java/Spring-shaped layering).
- Any new per-item field follows the existing `BacklogItemData`/`BacklogItemUpdate` pattern in
  `session/repository.go` (plain struct fields, `*T` for optional partial-update semantics) —
  see the bucket-[2] finding that non-optional proto3 bools already caused a silent
  flag-clobbering bug; a new field must not repeat that mistake.
- Any new session-creation-adjacent surface (a "which pipeline" selector) must satisfy
  `.claude/rules/session-creation-registry.md`'s 7-touchpoint checklist if it behaves like a
  session-creation mode; must satisfy `.claude/rules/feature-registry.md` regardless.
- ADR-013 (`docs/adr/013-workflow-engine-replaces-valid-transitions.md`) is **Proposed**, not
  fully implemented — its Phase 2 (`ConfiguredWorkflowEngine`, custom states) was never built.
  This project's `PipelineEngine` is a sibling concept (which skills/commands run) not the same
  as `WorkflowEngine` (which status transitions are legal) — do not conflate the two seams, but
  do follow ADR-013's Phase 2 design as precedent for how a DB-backed, per-item-configurable
  engine should be structured.

## Non-functional Requirements

- **Performance SLO**: pipeline/skill resolution must not add a synchronous DB round-trip to
  every triage/review call beyond what already exists (`BacklogItemData` is already loaded on
  each of these paths — resolution should ride along, not add a new query).
- **Scalability**: not applicable — single-operator tool, no multi-tenant concerns.
- **Security classification**: internal. A user-configurable "which skill set runs" surface
  must not let arbitrary shell/command injection into the headless pool call — validate against
  a known registry of pipeline modes, never interpolate a free-text field directly into a
  prompt or command line.
- **Data residency**: not applicable.

## Scope

### In Scope

- A new data-model field (or small set of fields) on `BacklogItemData` holding a per-item
  pipeline/skill-set choice, following the existing optional-field pattern.
- Proto changes to expose the new field(s) through `CreateBacklogItemRequest`,
  `UpdateBacklogItemRequest`, and `BacklogItem` (mirroring how `AutoSpawnSession` was added).
- A `PipelineEngine`-shaped seam (interface defined in the consuming package, narrow,
  1-3 methods) that `WriteSlashCommands`, the triage prompt builder, and the review-gate runner
  consult instead of hardcoding behavior — start with a `DefaultPipelineEngine` that reproduces
  today's fixed behavior exactly (zero regression), then add one or two real alternative modes
  (e.g. "sdd:quick"-equivalent, "sdd:full"-equivalent) to prove the seam actually varies
  behavior, not just adds an unused abstraction layer.
- UI: a selector in `BacklogItemForm.tsx` for the pipeline choice (same pattern as the
  `autoSpawnSession` checkbox added in bucket [2]), and a read-only "what ran" surface in
  `BacklogItemDetail.tsx` (addresses the UX-review finding).
- `docs/adr/` — a new ADR (or an update to ADR-013 marking its scope as WorkflowEngine-only, with
  a fresh ADR for PipelineEngine) recording the design decision before implementation, per this
  project's own SDD conventions.

### Out of Scope

- `docs/adr/013`'s Phase 2 `ConfiguredWorkflowEngine` (custom **states**, not custom
  **skills/stages**) — a related but separate initiative; do not implement it as part of this
  project even though the two would eventually compose.
- Reworking `BacklogBoard.tsx`'s hardcoded `COLUMNS` to support fully custom status sets — only
  the *skill/command* configurability is in scope; the status state machine itself stays as-is.
- `session/autonomous_driver.go:336-341`'s hardcoded orchestration prompt/signals — flagged in
  the audit as a related hardcoding instance, but reworking the `AutonomousDriver` itself is a
  larger, separate concern from picking *which* skill/command set an item uses. Defer to a
  follow-up project once `PipelineEngine` exists and this becomes a natural extension point.
- Multi-tenant / multi-user pipeline permissions (who is allowed to choose which pipeline) — this
  remains a single-operator tool.
- `backlog_service_triage.go:72-97`'s global tuning constants (`maxAutoReworkIterations`,
  `maxConcurrentBacklogWorkItems`, `defaultTriageCleanupTimeout`) — noted in the audit as
  hardcoded "operational tuning knobs," but they are process-level concurrency/safety limits,
  not pipeline/skill selection; out of scope here.

## Rabbit Holes

- **`WriteSlashCommands` currently writes files into the item's git worktree
  (`session/backlog_commands.go`)** — a `PipelineEngine` that varies *which* commands get
  written touches on-disk file generation, not just an in-memory decision. Scope creep risk:
  don't rebuild the slash-command templating system, just parameterize which fixed set gets
  selected.
- **Prompt construction for headless triage/review (`BuildHeadlessTriagePrompt`,
  `BuildHeadlessReviewPrompt`, `BuildReviewPrompt`) is currently free-text string building.**
  Making these pluggable per pipeline mode could balloon into a full templating engine. Resist
  that; start with 2-3 concrete prompt variants selected by mode, not a generic template DSL.
- **The relationship between `PipelineEngine` and the existing `WorkflowEngine`
  (ADR-013) is genuinely ambiguous** — a pipeline mode might legitimately want to add a status
  (e.g. a "quick-fix" mode that skips `review` entirely, not just via `SkipReviewGate`). Phase 3
  planning must explicitly decide whether `PipelineEngine` calls into `WorkflowEngine` or stays
  fully separate; don't let this get decided implicitly by whatever's easiest to code first.
- **`AutonomousDriver`'s hardcoded signals** (out of scope above) are still the actual execution
  engine most pipeline modes would drive through — there's a real risk that "add a
  `PipelineEngine` seam" ends up being cosmetic (selects a mode name) without actually changing
  runtime behavior, because the thing that would need to vary (`autonomous_driver.go`) is
  explicitly out of scope. Phase 3 planning must resolve this tension: either pull one concrete
  `AutonomousDriver` behavior change into scope as the "proof it's not cosmetic," or explicitly
  document that this phase only wires the seam and defers behavior-varying to the follow-up.

## Alternatives Considered

- **Free-text per-item "custom instructions" field appended to every prompt** — simplest
  possible implementation, no new enum/registry. Rejected as the primary mechanism: fails the
  security NFR (arbitrary text flowing into a prompt/command context is exactly the injection
  surface called out above) and doesn't give the UI anything structured to display ("what
  ran"). May still be worth a *secondary*, clearly-labeled free-text field layered on top of a
  structured mode choice — Phase 3 planning can decide.
- **Let the user pick an arbitrary existing `/sdd:*` or other slash command by name, stored as a
  raw string** — similar rejection: no validation surface, easy to typo into a silent no-op,
  and doesn't compose with `WriteSlashCommands`'s existing fixed-set generation without either
  a registry lookup (which is basically `PipelineEngine` anyway) or raw string interpolation
  (security risk).
- **Skip the seam, hardcode 2-3 more `Skip*`-style booleans** (mirroring bucket [2]'s
  successful pattern) — rejected as the long-term approach because it doesn't scale: bucket [2]
  worked because there were ~6 known gates; bucket 3's ask is open-ended ("use my SDD skills for
  this item type"), which needs a real extensibility seam, not N more booleans.

## Feasibility Risks

- `session/workflow_engine.go`'s pattern was designed for status-transition legality, not
  skill/stage selection — reusing its *style* (narrow interface, deep-copy-on-construct) is
  low-risk, but the *shape* of `PipelineEngine`'s interface is unproven and needs its own
  design pass in Phase 3, not a mechanical copy.
- Risk that this becomes primarily a UI feature (a dropdown that doesn't change runtime
  behavior) if `autonomous_driver.go` changes are deferred out of scope — see the rabbit hole
  above. Phase 3 planning must explicitly resolve this before implementation starts.
- `ent` schema changes require the `--feature sql/upsert` regeneration flag
  (`.claude/rules/ent-schema-generation.md`) — low risk, well-documented, but a known footgun if
  skipped (silently breaks `UpsertRule`-style methods).

## Observability Requirements

*(complexity ≥ 3)* Log which pipeline mode was resolved for a given item at each stage
transition (triage start, review start) at Info level, following the existing
`[BacklogLifecycle]`/`[TriggerTriage]` log-prefix convention — needed to debug "why did this
item run the wrong skill set" without new tooling. No new metrics/alerts required; this is a
single-operator tool with no oncall rotation.

## Risk Control

*(complexity ≥ 3)* No feature flag needed — the `DefaultPipelineEngine` (in-scope, first
milestone) must be behaviorally identical to today's hardcoded path, so shipping it is a no-op
by construction. Risk control is structural: land `DefaultPipelineEngine` as its own reviewable
commit before adding any second mode, so a regression in the seam itself (not a specific new
mode) is caught in isolation.

## Open Questions

- Does `PipelineEngine` need to be DB-persisted/configurable at runtime (like ADR-013's planned
  `ConfiguredWorkflowEngine`), or is a small Go-code-defined registry of named modes
  (`"default"`, `"quick"`, `"full"`) sufficient for the actual stated need ("use my SDD skills
  for this item")? Bias toward the simpler code-defined registry unless research turns up a
  concrete need for runtime/DB configurability.
- Should pipeline-mode selection happen once at item creation (immutable) or be changeable
  per-transition (e.g. escalate from "quick" to "full" mid-flight after a failed review)? The
  audit didn't surface a concrete user story for the latter — Phase 2 research should check
  whether it's needed or speculative.
- Exactly how does a chosen pipeline mode map to `WriteSlashCommands`'s output — a different
  fixed command set per mode, or a different *set of skills* invoked within the same command
  set? These are different implementations; Phase 2/3 must pick one grounded in what
  `/sdd:quick` vs `/sdd:full` actually differ by today (re-read those skill definitions before
  designing).
