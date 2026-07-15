# Implementation Plan: backlog-configurable-pipeline

**Feature**: Per-backlog-item, runtime-definable pipeline modes (which slash-commands/prompts drive triage, work, and review) via a new DB-persisted `PipelineMode` table, a `PipelineEngine` seam consulted at the three (four) hot-path call sites, and UI to select/manage/inspect modes.
**Date**: 2026-07-15
**Status**: Ready for implementation
**ADRs**: `project_plans/backlog-configurable-pipeline/decisions/ADR-001-pipeline-mode-db-persisted.md`

---

## Step 0.5 — Alternatives Explored

1. **Closed Go-code registry** (`map[string]PipelineEngine`, mirroring `session/backlog_plugin.go`'s `PluginRegistry`). *Strength*: zero persistence/caching design needed — free, in-memory, safest against malformed content. *Weakness*: every new mode requires a PR + `make install-service` deploy, which fails the requirement's decisive success metric ("no engineering involvement" to add a mode). **Rejected** — see ADR-001.
2. **DB-persisted modes, queried per-call** (mirror `WorkflowRepository` exactly, no cache). *Strength*: least new code — copy an existing, working pattern verbatim. *Weakness*: adds a synchronous DB read to `TriggerTriage`/`WriteSlashCommands`/`ReviewGateRunner.Run`, directly violating the NFR and repeating ADR-013's already-rejected "Alt B". **Rejected**.
3. **DB-persisted modes with an explicit copy-on-write in-process cache**, empty-string mode short-circuiting the cache/DB entirely. *Strength*: satisfies both the runtime-configurability requirement and the no-uncached-DB-read NFR; the default (99% common) path touches neither cache nor DB. *Weakness*: genuinely new design work — no existing precedent for the caching layer specifically (only for schema/CRUD shape). **Selected** — the weakness is accepted and budgeted for explicitly in Phase 1, Epic 1.3.

---

## Domain Glossary

| Term | Definition | Notes |
|------|-----------|-------|
| `PipelineMode` (ent entity) | A DB row: a named, slug-addressed, user-creatable definition of which slash-commands and prompt content a backlog item's pipeline uses. | New. `session/ent/schema/pipeline_mode.go`. Table `pipeline_modes`. Mirrors `session.Workflow`'s slug/name/description/enabled shape — not the same type. |
| `PipelineModeSlug` | The string identifier of a `PipelineMode` (e.g. `"quick"`, `"full"`). Stored on `BacklogItemData.PipelineMode` as a plain `string`; `""` means "no mode chosen — use built-in default." | Not a wrapped Go type — plain `string`, following the same convention as `BacklogStatus`'s sibling fields being plain strings validated at the Go layer, not `field.Enum`. |
| `PipelineModeDefault` | The Go constant `PipelineMode = ""` (empty string). Resolving this value never touches the cache or the DB — it dispatches straight to the pre-existing hardcoded functions. | `session/pipeline_engine.go`. This is the concrete mechanism that keeps the NFR ("no uncached DB read on the hot path for the common case") true by construction. |
| `PipelineEngine` | The narrow interface (4 methods) that `WriteSlashCommands`, triage-prompt building, review-prompt building, and initial-prompt building consult instead of calling the hardcoded functions directly. | `session/pipeline_engine.go`. Sibling of `WorkflowEngine`, not a wrapper around it — see Pattern Decisions. |
| `CachingPipelineEngine` | The single concrete implementation of `PipelineEngine`. Resolves `PipelineModeDefault` for free (no lookup); resolves any other slug via `pipelineModeCache`; falls back to default behavior + a Warn log on any unresolvable/malformed slug. | `session/pipeline_engine.go`. Constructed once via `NewPipelineEngine(repo PipelineModeRepository) *CachingPipelineEngine`, shared by `BacklogService` and `BacklogLifecycleListener`. |
| `pipelineModeCache` | An in-process, copy-on-write cache (`atomic.Pointer[map[string]resolvedPipelineMode]`) populated from `PipelineModeRepository.ListEnabled` at construction, replaced wholesale on `Invalidate`. | `session/pipeline_engine.go`. Read path is a single atomic load + map lookup — no locks, no per-call DB I/O. |
| `resolvedPipelineMode` | An unexported, immutable snapshot struct (slug, name, and the 9 rendered-template fields) held inside the cache. Deep-copied from `*ent.PipelineMode` on load so concurrent readers never see a partially-updated ent object during a cache swap. | `session/pipeline_engine.go`. Mirrors `NewDefaultWorkflowEngine`'s deep-copy-on-construct discipline. |
| `PipelineModeRepository` | The persistence interface for `PipelineMode` CRUD: `Create/Update/Delete/GetByID/GetBySlug/ListAll/ListEnabled`. | `session/pipeline_mode_repository.go`. Interface shape mirrors `WorkflowRepository` exactly (same method names/shape) per the Constraints section of requirements.md. |
| `EntPipelineModeRepository` | The ent-backed implementation of `PipelineModeRepository`. | `session/ent_pipeline_mode_repository.go`. Mirrors `EntWorkflowRepository`. |
| `PipelineModeCreateInput` / `PipelineModeUpdateInput` | Plain structs for repository Create/Update calls; `Update` uses `*T` pointer fields for partial-update semantics (only non-nil fields are applied). | Mirrors `WorkflowCreateInput`/`WorkflowUpdateInput`. |
| content-template field | One of 9 typed `string` columns on `PipelineMode` (`StatusCommandTemplate`, `DoneCommandTemplate`, `FailCommandTemplate`, `ReviewCommandTemplate`, `ShipCommandTemplate`, `HelpCommandTemplate`, `TriagePromptTemplate`, `ReviewPromptTemplate`, `InitialPromptTemplate`). Each supports a small fixed set of `{{placeholder}}` substitutions, never a general templating DSL. | This is the concrete answer to the NFR's "structured, not a single free-text blob" requirement. See Migration Plan. |
| `renderTemplate` | The unexported function that performs the fixed-placeholder substitution (`strings.NewReplacer`, not `text/template`) on a content-template field. Deliberately not Turing-complete — no conditionals/loops — to resist the "templating engine" rabbit hole flagged in requirements.md. | `session/pipeline_engine.go`. |
| `SlashCommandSet` | `PipelineEngine` method: `SlashCommandSet(item *BacklogItemData) (map[string]string, error)` — returns the filename→rendered-content map that `WriteSlashCommands` writes to `.claude/commands/backlog/`. | Replaces the hardcoded block in `session/backlog_commands.go`. |
| `TriagePromptFor` | `PipelineEngine` method: builds the headless-triage prompt for a given mode. Delegates to `BuildHeadlessTriagePrompt` for the default mode. | `session/pipeline_engine.go`. |
| `ReviewPromptFor` | `PipelineEngine` method: builds the headless-review prompt for a given mode. Delegates to `BuildHeadlessReviewPrompt` for the default mode. | `session/pipeline_engine.go`. |
| `InitialPromptFor` | `PipelineEngine` method: builds the interactive/autonomous session's initial prompt (`inst.Prompt`). Delegates to `session.BuildTokenBudgetedPrompt`/`BuildSessionInitialPrompt` for the default mode. **4th method** — see Pattern Decisions for why this exceeds the "1-3 methods" guidance. | `session/pipeline_engine.go`. This is the method that makes the seam behavior-changing for `AutonomousDriver`-backed sessions without touching `autonomous_driver.go` (per `research/architecture.md` §2). |
| `BacklogItemData.PipelineMode` | New `string` field (default `""`) on the existing domain struct, holding the resolved item's chosen mode slug. | `session/repository.go:341` region. Plain `string` (not pointer) — mirrors `RepoPath`/`Status`, since a resident domain value always has a concrete (possibly empty) value. |
| `BacklogItemUpdate.PipelineMode` | New `*string` field on the existing partial-update struct — `nil` means "field omitted, don't touch"; non-nil `""` means "explicit reset to default." | `session/repository.go:417` region. Mirrors `SkipReviewGate *bool` etc., but as `*string` (proto `optional string`, not `bool`) specifically to close the proto3-bool-clobbering bug class at its source — see `research/pitfalls.md` §2. |
| `ItemSessionSummary.PipelineModeSnapshot` | New field capturing the *resolved* mode identifier (and a content version/hash) at the moment a triage/work session first starts for an item, so later mode edits/deletions don't retroactively change what a historical session is shown to have run. | `session/repository.go:283` region. Mirrors the existing `AcSnapshot` field on the same struct — same struct, same snapshotting discipline, per `research/pitfalls.md` §4. |
| "what ran" surface | The read-only UI panel in `BacklogItemDetail.tsx` showing which `PipelineModeSnapshot` value drove a given `ItemSession`. | Frontend-only; reads `ItemSession.pipelineModeSnapshot`, never the item's live (possibly since-changed) `pipeline_mode` field. |
| `RadioGroup` (shared component) | A generalized, parameterized version of `OmnibarCreationPanel.tsx`'s hand-rolled `SessionTypeRadioGroup` (options array → ARIA radiogroup), extracted so the pipeline-mode selector doesn't duplicate the implementation. | `web-app/src/components/ui/RadioGroup.tsx` (new). Two a11y bugs present in the original are fixed during extraction — see Epic 3.1. |
| `PipelineModeOverridesSection` | The visually-grouped sub-section in `BacklogItemForm.tsx` containing the existing 3 checkboxes (`skipPlanning`, `skipReviewGate`, `autoSpawnSession`), relabeled as "Overrides" independent of the mode choice, per `research/ux.md` §2's compose-not-subsume UX recommendation. | `web-app/src/components/backlog/BacklogItemForm.tsx`. No new state — visual grouping only. |

---

## Pattern Decisions

| Component | Pattern Chosen | Source | Alternative Rejected | Reason |
|-----------|---------------|--------|---------------------|--------|
| `PipelineEngine` persistence model | DB-persisted table + explicit in-process cache (copy-on-write) | `session.Workflow`/`WorkflowRepository` shape + new caching layer | Closed Go-code registry (`PluginRegistry` style) | Fails the "no engineering involvement to add a mode" success metric. See ADR-001. |
| `PipelineEngine` interface location | Defined in `session` package (shared domain layer), consumed by both `server/services.BacklogService` and `session.BacklogLifecycleListener`/`ReviewGateRunner` | Mirrors `WorkflowEngine`'s existing placement in `session` | Define in `server/services` (strict "consumer package" reading of the interface-pollution checklist) | `session` is consumed by two independent packages (`server/services` and `session` itself, via `BacklogLifecycleListener`); placing it in either leaf package would force the other to import a "consumer" package for a shared seam. `WorkflowEngine` already establishes this exact precedent in this exact codebase — consistency wins over a mechanical rule reading. |
| `PipelineEngine` concurrency-safe caching | `atomic.Pointer[map[string]resolvedPipelineMode]`, copy-on-write, swapped wholesale on invalidate | go-concurrency idiom: atomic.Pointer copy-on-write for read-heavy/write-rare shared state | `sync.RWMutex` guarding a live map | Reads (every triage/spawn/review call) vastly outnumber writes (operator edits a mode definition rarely); atomic.Pointer gives lock-free reads with no risk of readers blocking behind a writer, and a wholesale swap is simpler to reason about than mutating a shared map under a lock. |
| `PipelineMode` cache invalidation | Explicit `Invalidate(ctx)` call after every Create/Update/Delete/Enable/Disable RPC handler | `DetectorRegistry`'s `Register`/`Lookup` shape for the in-memory half | Short-TTL background refresh | Single-operator tool: writes are rare and always operator-initiated through the same process that also serves reads — invalidate-on-write gives immediate consistency with less code than a poll loop, and there's no multi-instance/multi-writer scenario where TTL staleness would matter. |
| `PipelineMode` content shape | 9 separate typed `string` columns (one per slash-command file + one per prompt), each with fixed-placeholder substitution only | requirements.md NFR ("structured... not a single unstructured free-text blob") + rabbit-hole warning against a templating DSL | Single JSON blob column (`map[string]string`) | A JSON blob is not meaningfully more "structured" than free text from the DB's point of view (no column-level typing/constraints) and would need its own ad hoc validation layer; separate columns get per-field `NOT NULL`/length constraints from ent directly and are trivially diffable in a migration review. |
| `PipelineEngine` method count | 4 methods (`SlashCommandSet`, `TriagePromptFor`, `ReviewPromptFor`, `InitialPromptFor`) | `research/architecture.md` §2's recommendation (a): add `InitialPromptFor` rather than leave autonomous-mode prompt content mode-unaware | 3 methods, defer `InitialPromptFor` to a follow-up | requirements.md's Scope section suggests "1-3 methods," but `AutoSpawnSession`/autonomous mode are live, actively-used flags on the exact same code path this feature targets; shipping without `InitialPromptFor` would make the seam cosmetic for every autonomous-mode session (see `research/architecture.md` §2's "risk of ending up cosmetic" analysis). The 4th method is one more get/render call, not a new responsibility — interface segregation is about avoiding unrelated responsibilities, not a strict method-count ceiling. Explicitly overriding the "1-3" guidance here, as instructed by the research file's own open question. |
| `PipelineEngine` ↔ `WorkflowEngine` relationship | Fully separate, sibling interfaces, both held as independent fields on `BacklogService`/`BacklogLifecycleListener`; no calls between them | `research/architecture.md` §1's "separate interface, composed by the caller" recommendation | Extend `WorkflowEngine` with pipeline methods, or have `PipelineEngine` call into `WorkflowEngine` | Disjoint call-site sets (state-transition legality vs. within-status content selection) and disjoint reasons to change; coupling them would pull ADR-013 Phase 2 (custom states) back into scope, which is explicitly out of scope here. |
| Mode vs. `Skip*`/`AutoSpawnSession` booleans | **Compose** — mode selection and the 3 existing booleans are independent; mode never auto-sets or overrides a checkbox | `research/features.md` §3's recommendation (option 2) | **Subsume** — mode fully replaces the 3 booleans | Composing is the lower-risk, ship-`DefaultPipelineEngine`-first-with-zero-regression path the Risk Control section requires; subsuming would force every existing item's implicit boolean state to be reinterpreted as a mode choice on migration day, which is unnecessary scope for this phase. |
| Mode mutability | Immutable-after-first-triage-session, via a resolved-mode snapshot on `ItemSessionSummary` | `research/pitfalls.md` §4, extending the existing `AcSnapshot` precedent | Always resolve live from the item's current `pipeline_mode` field | Since modes are now DB-mutable, resolving live would let an in-flight item's triage and review stages silently run under two different mode definitions (or definitions-as-they-existed-at-different-times) with no record of which was used — `AcSnapshot` already solves exactly this class of problem on the same struct. |
| Item-level `pipeline_mode` proto field | `optional string pipeline_mode = N;` (synthesized oneof, real wire presence), handler gated on presence not truthiness | `research/pitfalls.md` §2, closing the proto3-bool-clobbering bug class at its origin | Plain `string` (no `optional`), or a proto `enum PipelineMode` | Plain `string` repeats the exact bug class the `SkipReviewGate`/`SkipPlanning`/`AutoSpawnSession` incident already taught this codebase (omitted vs. explicit-empty are indistinguishable). `enum` requires a fixed compile-time value set, which directly contradicts runtime-definable modes (ADR-001). |
| `PipelineModeRepository` return type | Return `*ent.PipelineMode` directly from repository methods, no separate domain DTO | Mirrors `WorkflowRepository`'s existing (ent-coupled) return-type convention | Introduce a `PipelineModeData` domain struct decoupled from ent, mirroring `BacklogItemData` | No current consumer needs decoupling from ent for this type (only `pipelineModeCache`'s internal `resolvedPipelineMode` needs a stable, deep-copied snapshot, which is a separate internal type anyway); adding a parallel DTO here would be a translation layer with no behavior, which `.claude/rules/interface-pollution-checklist.md` explicitly flags as a smell (forwarding-only wrapper). |
| Pipeline-mode selector UI | Extract `SessionTypeRadioGroup` into a shared, parameterized `RadioGroup` component; reuse for pipeline-mode selection | `research/ux.md` §1's recommendation | Build a second, near-identical hand-rolled ARIA radiogroup | Zero session-specific logic is baked into `SessionTypeRadioGroup`'s rendering today; duplicating it would also duplicate its 2 known a11y gaps (missing `aria-labelledby`/`aria-describedby`) instead of fixing them once. |
| Management CRUD surface | Dedicated settings page (`/settings/pipeline-modes`), mirroring `/settings/backlog-sources` | requirements.md Scope + `web-app/src/app/settings/backlog-sources/page.tsx` precedent | Inline modal/drawer off the backlog board | A modal would need to duplicate `/settings`'s existing layout/nav chrome (`web-app/src/app/settings/layout.tsx`) for no benefit; a dedicated settings page is the established location for this class of "operator-only configuration, not a per-item action" feature (`/settings/backlog-sources` is the direct sibling). |

---

## Migration Plan

- **Migration file**: generated by `go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./session/ent/schema` after adding `session/ent/schema/pipeline_mode.go` and the `pipeline_mode` field on `session/ent/schema/backlog_item.go` and the `pipeline_mode_snapshot`/`pipeline_mode_snapshot_version` fields on `session/ent/schema/item_session.go`. Ent auto-migrates on startup for this codebase (no separate `.sql` migration file checked in — confirmed by the absence of a `migrations/` directory alongside `session/ent/schema/`; `ent.Client.Schema.Create(ctx)` runs at boot). New table: `pipeline_modes` (columns: `id` uuid PK, `slug` string unique, `name` string, `description` string optional, `enabled` bool default true, `status_command_template` string, `done_command_template` string, `fail_command_template` string, `review_command_template` string, `ship_command_template` string, `help_command_template` string, `triage_prompt_template` string, `review_prompt_template` string, `initial_prompt_template` string, `created_at`/`updated_at` time). New columns on `backlog_items`: `pipeline_mode` string default `""`. New columns on `item_sessions`: `pipeline_mode_snapshot` string default `""`.
- **Reversibility**: ent's auto-migrate is additive-only (new table, new nullable/defaulted columns) — no destructive change to existing tables/columns, so no down-migration is required for rollback; reverting the code (not the schema) is sufficient, and the new columns/table are simply unused by older code.
- **Zero-downtime strategy**: single-operator/single-instance deployment (`make install-service` restarts one systemd user service) — no rolling-deploy coordination needed. New columns default to `""`/`true` so pre-existing rows never contain `NULL` in a `NOT NULL`-equivalent field.
- **Rollback procedure**: `git revert` the schema-adding commit(s) and re-run `make install-service`; the `pipeline_modes` table and new columns are simply left orphaned in SQLite/the configured DB (no data loss, no destructive down-migration needed given the single-operator/no-multi-tenant context).

## Observability Plan
- **Logs**: `[PipelineEngine]`-prefixed (new prefix, following the `[BacklogLifecycle]`/`[TriggerTriage]` convention) — Info on every resolved mode at triage-start and review-start (`item=<id> mode=<slug-or-"default">`); Debug on cache load/invalidate/miss; Warn (never silent) whenever a stored `pipeline_mode` slug fails to resolve, including the item ID and the unresolved slug, on every one of the 4 call sites independently (not just once).
- **Metrics**: none required — single-operator tool, no oncall rotation, per requirements.md.
- **Alerts**: none required — same rationale.

## Risk Control
- **Feature flag**: none required for `PipelineEngine` itself — the `PipelineModeDefault` (`""`) short-circuit IS the flag: every existing item is unaffected until a mode is explicitly chosen, and Phase 1 ships with zero UI to choose one yet (the selector lands in Phase 3). This is the concrete "reviewable in isolation, no-op by construction" milestone requirements.md's Risk Control section requires.
- **Rollback procedure**: Phase 1 is a single reviewable commit set (Epic 1.1–1.7) landed before any second mode exists or any CRUD UI is exposed; `git revert` of that commit range fully removes the seam with no data-loss risk (see Migration Plan reversibility above).
- **Staged rollout**: Phase 1 (seam + zero-regression characterization tests) → Phase 2 (CRUD RPCs, still no UI to reach them) → Phase 3 (frontend selector + management UI + "what ran" surface) → Phase 4 (a real second mode defined through the now-live UI, proving the seam is not cosmetic). Each phase is independently shippable and reviewable; do not collapse phases into one PR.

## Unresolved Questions
- [ ] Should the 9 content-template placeholder names (`{{item_id}}`, `{{criteria_index}}`, etc.) be validated against a fixed allow-list at write time (reject unknown `{{...}}` tokens) or only at render time (silently leave unknown tokens un-substituted)? — blocks Story 2.3.1 — owner: implementer, default to write-time allow-list rejection (fail loud, matches the "fail closed and loud" observability requirement) unless a concrete reason emerges to defer.
- [ ] Exact wording/threshold for when the management UI's mode list should collapse into "More" progressive disclosure (`research/ux.md` §1 flags this for "once the list is long" but doesn't set a number) — blocks Story 3.1.2 — owner: implementer, default to reusing `OmnibarCreationPanel.tsx`'s existing split point (2 primary + rest behind "More") until real usage data suggests otherwise.
- [ ] Whether `PipelineMode.Delete` should hard-block deletion when any `BacklogItemData.PipelineMode` still references the slug, or allow it and rely on the fail-closed-to-default resolution path — blocks Story 2.2.3 — owner: implementer, default to **allow deletion, rely on fail-closed resolution** (simpler, and the fail-closed path is being built regardless as the answer to the "mode deleted after items reference it" edge case in `research/features.md` §3).

## Dependency Visualization
```
Phase 1: Foundation (seam wraps existing hardcoded behavior — zero regression)
  Epic 1.1 (ent schema: PipelineMode)
        |
        v
  Epic 1.2 (PipelineModeRepository) ----+
        |                               |
        v                               v
  Epic 1.3 (PipelineEngine + cache) <---+
        |
        +--------------------------------------+
        v                                       v
  Epic 1.4 (item-level pipeline_mode field)   Epic 1.6 (ItemSession snapshot field)
        |                                       |
        v                                       |
  Epic 1.5 (wire PipelineEngine into 4 call sites) <--+
        |
        v
  Epic 1.7 (characterization tests + observability + isolated commit gate)  <== Phase 1 ships here
        |
        v
Phase 2: CRUD RPCs
  Epic 2.1 (proto: PipelineMode message + RPCs) --> Epic 2.2 (Go handlers + cache invalidation) --> Epic 2.3 (structural validation)
        |
        v
Phase 3: Frontend
  Epic 3.1 (shared RadioGroup) --> Epic 3.2 (item selector) --> Epic 3.3 (management CRUD page)
        |                                                              |
        v                                                              v
  Epic 3.4 ("what ran" surface, depends on Epic 1.6's snapshot field) <-+
        |
        v
Phase 4: Proof of seam + registry + e2e
  Epic 4.1 (define real "quick" mode via live UI) --> Epic 4.2 (observability polish) --> Epic 4.3 (feature registry + e2e tests)
```

---

## Phase 1: Foundation — `PipelineMode` Data Model & `PipelineEngine` Seam

### Epic 1.1: `PipelineMode` ent schema
**Goal**: A new ent-backed table exists, matching the shape recorded in the Migration Plan, ready for a repository layer to sit on top of it.

#### Story 1.1.1: Create the `PipelineMode` ent schema file
**As a** backend developer, **I want** a `PipelineMode` ent entity, **so that** pipeline-mode definitions can be persisted and queried like `session.Workflow` is today.
**Acceptance Criteria**:
- The file `session/ent/schema/pipeline_mode.go` defines an ent `PipelineMode` schema with fields `id (uuid)`, `slug (string, unique)`, `name (string)`, `description (string, optional)`, `enabled (bool, default true)`, the 9 content-template `string` fields listed in the Migration Plan, and `created_at`/`updated_at` (time, matching `session/ent/schema/workflow.go`'s timestamp convention).
  - *Given* the file `session/ent/schema/pipeline_mode.go` does not yet exist, *When* it is created with `field.String("slug").Unique()` and `field.Bool("enabled").Default(true)`, *Then* `go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./session/ent/schema` (per `.claude/rules/ent-schema-generation.md`) succeeds and generates `session/ent/pipelinemode/`, `session/ent/pipelinemode_create.go`, etc.
- **Files**: `session/ent/schema/pipeline_mode.go`

##### Task 1.1.1a: Write the `PipelineMode` ent schema struct + `Fields()` (~5 min)
- Create `session/ent/schema/pipeline_mode.go` following the exact structure of `session/ent/schema/workflow.go` (same package, same `ent.Schema` embedding pattern, same `Fields() []ent.Field` shape).
- Add `id`, `slug` (`.Unique()`), `name`, `description` (`.Optional()`), `enabled` (`.Default(true)`), the 9 content-template string fields (each `.Comment(...)`-documented per the codebase's `Comment()` convention seen on `session/ent/schema/backlog_item.go:39-43`), `created_at`, `updated_at`.
- Files: `session/ent/schema/pipeline_mode.go`

##### Task 1.1.1b: Regenerate ent bindings (~2 min)
- Run `go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./session/ent/schema` (the exact command from `session/ent/generate.go` — never bare `ent generate`).
- Run `go build ./...` to confirm the generated code compiles.
- Files: `session/ent/pipelinemode/*.go` (generated), `session/ent/pipelinemode_*.go` (generated), `session/ent/client.go` (generated), `session/ent/runtime/runtime.go` (generated)

---

### Epic 1.2: `PipelineModeRepository`
**Goal**: A CRUD persistence interface + ent implementation exists, mirroring `WorkflowRepository`/`EntWorkflowRepository` exactly in shape.

#### Story 1.2.1: Define `PipelineModeRepository` interface and input structs
**As a** backend developer, **I want** a narrow repository interface for `PipelineMode` CRUD, **so that** `CachingPipelineEngine` and the future RPC handlers depend on an interface, not a concrete ent client.
**Acceptance Criteria**:
- `session/pipeline_mode_repository.go` defines `PipelineModeRepository` with methods `Create(ctx, PipelineModeCreateInput) (*ent.PipelineMode, error)`, `Update(ctx, uuid.UUID, PipelineModeUpdateInput) (*ent.PipelineMode, error)`, `Delete(ctx, uuid.UUID) error`, `GetByID(ctx, uuid.UUID) (*ent.PipelineMode, error)`, `GetBySlug(ctx, string) (*ent.PipelineMode, error)`, `ListAll(ctx) ([]*ent.PipelineMode, error)`, `ListEnabled(ctx) ([]*ent.PipelineMode, error)` — the exact method set of `WorkflowRepository` (`session/workflow_repository.go:12-19`), substituting `PipelineMode` for `Workflow`.
  - *Given* `session/workflow_repository.go`'s `WorkflowRepository` interface as the shape template, *When* `PipelineModeRepository` is defined with the same 7 methods, *Then* `session/pipeline_mode_repository.go` compiles and a mock/fake implementing it satisfies the interface with no extra methods required.
- **Files**: `session/pipeline_mode_repository.go`

##### Task 1.2.1a: Write `PipelineModeRepository` interface + `PipelineModeCreateInput` (~4 min)
- Create `session/pipeline_mode_repository.go`, copying `session/workflow_repository.go:1-36`'s structure: package `session`, the 7-method interface, and `PipelineModeCreateInput` struct with fields `Slug, Name, Description string`, `Enabled bool`, and the 9 content-template `string` fields.
- Files: `session/pipeline_mode_repository.go`

##### Task 1.2.1b: Write `PipelineModeUpdateInput` (~3 min)
- Add `PipelineModeUpdateInput` to the same file with all fields as `*T` pointers (partial-update semantics), mirroring `WorkflowUpdateInput` (`session/workflow_repository.go:40-53`).
- Files: `session/pipeline_mode_repository.go`

#### Story 1.2.2: Implement `EntPipelineModeRepository`
**As a** backend developer, **I want** an ent-backed implementation of `PipelineModeRepository`, **so that** pipeline modes are actually persisted.
**Acceptance Criteria**:
- `session/ent_pipeline_mode_repository.go` implements all 7 `PipelineModeRepository` methods using the `*ent.Client`, following `session/ent_workflow_repository.go`'s conditional-setter pattern (only call `.SetX(...)` when the input field is non-zero/non-nil).
  - *Given* a `PipelineModeCreateInput{Slug: "quick", Name: "Quick Fix", Enabled: true, TriagePromptTemplate: "Fix {{item_id}} quickly."}`, *When* `EntPipelineModeRepository.Create(ctx, input)` is called against a fresh test DB, *Then* it returns a `*ent.PipelineMode` with `Slug == "quick"` and a subsequent `GetBySlug(ctx, "quick")` returns the same row.
  - *Given* an existing `PipelineMode` with slug `"quick"`, *When* `Create` is called again with `Slug: "quick"`, *Then* it returns an `ent.ConstraintError` (unique constraint on `slug`), matching `EntWorkflowRepository.Create`'s documented duplicate-slug behavior.
- **Files**: `session/ent_pipeline_mode_repository.go`

##### Task 1.2.2a: Implement `NewEntPipelineModeRepository` + `Create` (~5 min)
- Create `session/ent_pipeline_mode_repository.go`, copying `session/ent_workflow_repository.go:1-60`'s structure for the constructor and `Create` method.
- Files: `session/ent_pipeline_mode_repository.go`

##### Task 1.2.2b: Implement `Update`, `Delete`, `GetByID`, `GetBySlug` (~5 min)
- Add the remaining 4 methods, mirroring `EntWorkflowRepository`'s equivalents (find them via `grep -n "func (r \*EntWorkflowRepository)" session/ent_workflow_repository.go`).
- Files: `session/ent_pipeline_mode_repository.go`

##### Task 1.2.2c: Implement `ListAll`, `ListEnabled` (~4 min)
- Add the 2 list methods, mirroring `EntWorkflowRepository.ListAll`/`ListEnabled` (the `enabled=true` predicate equivalent to `cron_enabled=true` in the workflow version).
- Files: `session/ent_pipeline_mode_repository.go`

##### Task 1.2.2d: Unit tests for `EntPipelineModeRepository` (~5 min)
- Add `session/ent_pipeline_mode_repository_test.go` covering: Create + GetBySlug round-trip, duplicate-slug ConstraintError, Update partial-field semantics (only supplied fields change), Delete removes the row, ListEnabled excludes disabled rows.
- Files: `session/ent_pipeline_mode_repository_test.go`

---

### Epic 1.3: `PipelineEngine` interface, cache, and default-mode wrapping
**Goal**: The `PipelineEngine` seam exists and, for `PipelineModeDefault`, produces byte-identical output to today's hardcoded functions — with zero new DB/cache dependency on that path.

#### Story 1.3.1: Define `PipelineEngine` interface and `PipelineModeDefault`
**As a** backend developer, **I want** a narrow `PipelineEngine` interface with 4 methods, **so that** `WriteSlashCommands`/triage/review/initial-prompt call sites depend on an interface instead of hardcoded free functions.
**Acceptance Criteria**:
- `session/pipeline_engine.go` defines `type PipelineMode string`, `const PipelineModeDefault PipelineMode = ""`, and `type PipelineEngine interface` with `SlashCommandSet(item *BacklogItemData) (map[string]string, error)`, `TriagePromptFor(item *BacklogItemData, artifactAbsPath string) string`, `ReviewPromptFor(item *BacklogItemData, acSnapshot []AcCriterion, diff string, diffTruncated bool, verificationNotes string) string`, `InitialPromptFor(item *BacklogItemData, priorSessions []ItemSessionSummary) string`.
  - *Given* the file does not yet exist, *When* `session/pipeline_engine.go` is created with this interface, *Then* `go build ./session/...` succeeds with no implementations yet required (interface-only compiles).
- **Files**: `session/pipeline_engine.go`

##### Task 1.3.1a: Write the `PipelineEngine` interface + `PipelineMode`/`PipelineModeDefault` types (~4 min)
- Create `session/pipeline_engine.go` with the package doc comment explaining the sibling (not extension) relationship to `WorkflowEngine` (cite `research/architecture.md` §1's reasoning inline).
- Files: `session/pipeline_engine.go`

#### Story 1.3.2: Implement `pipelineModeCache` (copy-on-write)
**As a** backend developer, **I want** a lock-free, copy-on-write in-process cache of enabled pipeline modes, **so that** resolving a non-default mode never blocks on a DB round trip on the hot path.
**Acceptance Criteria**:
- `pipelineModeCache` wraps `atomic.Pointer[map[string]resolvedPipelineMode]`; `Load(ctx, repo)` populates it from `repo.ListEnabled`, deep-copying each `*ent.PipelineMode` into a `resolvedPipelineMode` value; `Get(slug)` does a single atomic `Load()` + map lookup with no locking; `Invalidate(ctx, repo)` re-populates and atomically swaps the pointer.
  - *Given* a `pipelineModeCache` populated with one enabled mode `{Slug: "quick", ...}`, *When* `Get("quick")` is called from 50 concurrent goroutines while `Invalidate` runs concurrently on another goroutine, *Then* every `Get` call returns either the pre- or post-invalidate snapshot in full (never a torn/partial read) — verified by a `-race`-clean test with `go test -race`.
  - *Given* a slug `"missing"` not present in the cache, *When* `Get("missing")` is called, *Then* it returns `(resolvedPipelineMode{}, false)` — the caller (Epic 1.3.3) is responsible for the Warn-log-and-fallback behavior, not the cache itself.
- **Files**: `session/pipeline_engine.go`

##### Task 1.3.2a: Define `resolvedPipelineMode` struct + `pipelineModeCache.Load`/`Get` (~5 min)
- Add `resolvedPipelineMode` (unexported: `Slug, Name string`, the 9 rendered-template-source fields) and `pipelineModeCache` with `Load(ctx, PipelineModeRepository) error` and `Get(slug string) (resolvedPipelineMode, bool)`.
- Files: `session/pipeline_engine.go`

##### Task 1.3.2b: Implement `pipelineModeCache.Invalidate` (~3 min)
- Add `Invalidate(ctx, PipelineModeRepository) error`, identical body to `Load` (re-fetch + atomic swap) — exposed as a separate method name for call-site clarity at RPC write handlers (Epic 2.2), not because the implementation differs.
- Files: `session/pipeline_engine.go`

##### Task 1.3.2c: Race-safe concurrency test for `pipelineModeCache` (~5 min)
- Add `session/pipeline_engine_test.go` with a `-race` test: concurrent `Get` + `Invalidate` calls against a fake `PipelineModeRepository`, asserting no torn reads and no data race.
- Files: `session/pipeline_engine_test.go`

#### Story 1.3.3: Implement `CachingPipelineEngine` with fail-closed resolution
**As a** backend developer, **I want** the single `PipelineEngine` implementation to resolve `PipelineModeDefault` for free and any other slug via the cache, falling back to default behavior with a Warn log on any miss, **so that** an unresolvable/deleted mode never silently no-ops or crashes a live item.
**Acceptance Criteria**:
- `CachingPipelineEngine.SlashCommandSet(item)`: if `item.PipelineMode == PipelineModeDefault`, calls `buildDefaultSlashCommandSet(item)` (Story 1.3.4) directly — no cache/DB touch. Otherwise, calls `cache.Get(item.PipelineMode)`; on hit, renders that mode's 6 command templates via `renderTemplate`; on miss, logs `log.WarningLog.Printf("[PipelineEngine] unresolved pipeline_mode=%q item=%s — falling back to default", item.PipelineMode, item.ID)` and calls `buildDefaultSlashCommandSet(item)`.
  - *Given* a `BacklogItemData{ID: "abc-123", PipelineMode: ""}`, *When* `CachingPipelineEngine.SlashCommandSet(item)` is called, *Then* the returned map is byte-identical to what today's hardcoded `WriteSlashCommands` body produces for the same item, and no call is made to `pipelineModeCache.Get` (verified via a test double that fails the test if `Get` is invoked).
  - *Given* a `BacklogItemData{ID: "abc-123", PipelineMode: "deleted-mode"}` where `"deleted-mode"` is not present in the cache, *When* `CachingPipelineEngine.SlashCommandSet(item)` is called, *Then* it returns the same default command set as the empty-mode case AND a single `[PipelineEngine]`-prefixed Warn log line is emitted containing `item=abc-123` and `deleted-mode`.
- The same default-short-circuit + cache-hit + Warn-fallback pattern is implemented for `TriagePromptFor`, `ReviewPromptFor`, and `InitialPromptFor`.
  - *Given* a `BacklogItemData{PipelineMode: ""}` and `artifactAbsPath: "/tmp/plan.md"`, *When* `TriagePromptFor(item, "/tmp/plan.md")` is called, *Then* it returns exactly `session.BuildHeadlessTriagePrompt(item, "/tmp/plan.md")`'s output.
- **Files**: `session/pipeline_engine.go`

##### Task 1.3.3a: Implement `NewPipelineEngine` constructor + `SlashCommandSet` (~5 min)
- Add `CachingPipelineEngine` struct (`repo PipelineModeRepository`, `cache *pipelineModeCache`) and `NewPipelineEngine(repo PipelineModeRepository) (*CachingPipelineEngine, error)` which calls `cache.Load` once at construction (mirrors `NewDefaultWorkflowEngine`'s construct-time-population discipline).
- Implement `SlashCommandSet` per the acceptance criteria above.
- Files: `session/pipeline_engine.go`

##### Task 1.3.3b: Implement `TriagePromptFor` and `ReviewPromptFor` (~5 min)
- Implement both methods with the default-short-circuit + cache-hit + Warn-fallback pattern.
- Files: `session/pipeline_engine.go`

##### Task 1.3.3c: Implement `InitialPromptFor` (~4 min)
- Implement with the same pattern, delegating to `session.BuildTokenBudgetedPrompt(item, priorSessions)` for the default case (the exact call currently inline at `server/services/backlog_service_triage.go:260`).
- Files: `session/pipeline_engine.go`

##### Task 1.3.3d: Implement `renderTemplate` fixed-placeholder substitution (~4 min)
- Add `renderTemplate(tmpl string, placeholders map[string]string) string` using `strings.NewReplacer`, with an allow-list of recognized placeholder names (`item_id`, `criteria_index`) — unrecognized `{{...}}` tokens are left as-is (per the Unresolved Questions entry, revisit if write-time validation is added in Story 2.3.1).
- Files: `session/pipeline_engine.go`

##### Task 1.3.3e: Unit tests for `CachingPipelineEngine` fail-closed behavior (~5 min)
- Add tests to `session/pipeline_engine_test.go`: default-mode short-circuits the cache (no `Get` call), unresolved-slug falls back to default output + emits the Warn log (capture via a log-writer test double), resolved-slug renders its own templates (not the default ones).
- Files: `session/pipeline_engine_test.go`

#### Story 1.3.4: Extract today's hardcoded slash-command body into `buildDefaultSlashCommandSet`
**As a** backend developer, **I want** `WriteSlashCommands`'s current hardcoded file-content generation extracted into a pure function returning `map[string]string`, **so that** `CachingPipelineEngine`'s default path and the on-disk writer share one source of truth with zero behavior drift.
**Acceptance Criteria**:
- `session/backlog_commands.go` gains `buildDefaultSlashCommandSet(item *BacklogItemData) (map[string]string, error)`, containing exactly the content-generation logic currently inline in `WriteSlashCommands` (lines ~35-70+ per the current file read at `session/backlog_commands.go`, covering `status.md`, per-criterion `done-N.md`/`fail-N.md`, `review.md`, `ship.md`, `help.md`). `WriteSlashCommands` itself is refactored to call `buildDefaultSlashCommandSet` and write the returned map's entries to disk (retaining its existing `MkdirAll` retry-3-times logic), rather than building content inline.
  - *Given* a `BacklogItemData` with 2 AC criteria, *When* `buildDefaultSlashCommandSet(item)` is called, *Then* the returned map has exactly the keys `status.md, done-0.md, fail-0.md, done-1.md, fail-1.md, review.md, ship.md, help.md` with content identical to what the pre-refactor `WriteSlashCommands` wrote to disk for the same item (verified by a golden-file characterization test — see Story 1.7.1).
- **Files**: `session/backlog_commands.go`

##### Task 1.3.4a: Extract `buildDefaultSlashCommandSet` from `WriteSlashCommands` (~5 min)
- Move the content-building logic (the `fmt.Sprintf`/`writeFile`-content-construction parts, not the `os.MkdirAll`/disk-write parts) into the new function; `WriteSlashCommands` now: builds the dir, calls `buildDefaultSlashCommandSet` (or, after Story 1.3.3, the injected `PipelineEngine.SlashCommandSet`), then loops over the returned map calling the existing `writeFile` helper.
- Files: `session/backlog_commands.go`

##### Task 1.3.4b: Update `WriteSlashCommands` signature to accept a `PipelineEngine` (~4 min)
- Change `WriteSlashCommands(item *BacklogItemData, worktreePath string) error` to `WriteSlashCommands(engine PipelineEngine, item *BacklogItemData, worktreePath string) error` (engine-first, matching Go convention for a "policy object" parameter, and consistent with how `TransitionBacklogItemStatus` already takes an engine-shaped param in nearby code). Update both call sites: `server/services/backlog_service_triage.go:436` and `server/services/backlog_service_sync.go:93` (deferred to Epic 1.5 — this task only changes the signature and leaves call sites broken/to-be-fixed by Epic 1.5's tasks, OR do both in the same task if under the 3-5 file cap; given exactly 3 files are touched, do both here).
- Files: `session/backlog_commands.go`, `server/services/backlog_service_triage.go`, `server/services/backlog_service_sync.go`

---

### Epic 1.4: Item-level `pipeline_mode` field
**Goal**: `BacklogItemData`/`BacklogItemUpdate` carry the chosen mode slug end-to-end (ent → domain → proto → RPC handlers), using the presence-gated `optional string` pattern that avoids the proto3-bool-clobbering bug class.

#### Story 1.4.1: Add `pipeline_mode` ent field to `BacklogItem`
**As a** backend developer, **I want** a `pipeline_mode` column on `backlog_items`, **so that** an item's chosen mode slug is durable.
**Acceptance Criteria**:
- `session/ent/schema/backlog_item.go` gains `field.String("pipeline_mode").Default("").Comment("Slug of the PipelineMode this item uses to drive triage/work/review content. Empty string means the built-in default (today's fixed hardcoded pipeline).")`, placed after the existing `auto_spawn_session` field (line 43 region) for locality with its sibling per-item configuration flags.
  - *Given* the current schema has no `pipeline_mode` field, *When* it is added with `.Default("")`, *Then* `go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert ./session/ent/schema` regenerates successfully and every existing `BacklogItem` row (pre-migration) reads back `PipelineMode == ""` after the server restarts (ent auto-migrate backfills the default for existing rows).
- **Files**: `session/ent/schema/backlog_item.go`

##### Task 1.4.1a: Add the ent field + regenerate (~4 min)
- Add the field per the acceptance criterion; run the regeneration command; run `go build ./...`.
- Files: `session/ent/schema/backlog_item.go`, `session/ent/*` (generated)

#### Story 1.4.2: Add `pipeline_mode` proto field (3 messages, `optional string`)
**As a** backend/frontend developer, **I want** `pipeline_mode` exposed on the wire with real presence semantics, **so that** "omitted" and "explicit reset to default" are distinguishable — closing the proto3-bool-clobbering bug class at its source.
**Acceptance Criteria**:
- `proto/session/v1/backlog.proto` gains `optional string pipeline_mode = 25;` on `BacklogItem` (next available field number after `auto_spawn_session = 24`, confirmed at `proto/session/v1/backlog.proto:117`), `optional string pipeline_mode = 11;` on `CreateBacklogItemRequest` (next after `auto_spawn_session = 10` at line 157), and `optional string pipeline_mode = 13;` on `UpdateBacklogItemRequest` (next after `auto_spawn_session = 12` at line 196).
  - *Given* `UpdateBacklogItemRequest` with `pipeline_mode` unset (field omitted entirely), *When* the message is serialized and deserialized, *Then* the generated Go/TS accessor reports "not present" (`req.Msg.PipelineMode == nil` in Go), distinct from an explicit `pipeline_mode: ""` which reports `req.Msg.PipelineMode != nil && *req.Msg.PipelineMode == ""`.
- `make proto-gen` regenerates `session/gen/proto/go/session/v1/backlog.pb.go` and `web-app/src/gen/session/v1/backlog_pb.ts` with the new `optional` field producing a nilable Go pointer / TS `string | undefined`.
- **Files**: `proto/session/v1/backlog.proto`

##### Task 1.4.2a: Add the 3 `optional string pipeline_mode` fields (~3 min)
- Edit `proto/session/v1/backlog.proto` at the 3 line locations above.
- Files: `proto/session/v1/backlog.proto`

##### Task 1.4.2b: Run `make proto-gen` and verify generated bindings (~3 min)
- Run `make proto-gen`; confirm `session/gen/proto/go/session/v1/backlog.pb.go` gains a `PipelineMode *string` field on the 3 generated Go structs and `web-app/src/gen/session/v1/backlog_pb.ts` gains an optional `pipelineMode?: string` field.
- Files: `session/gen/proto/go/session/v1/backlog.pb.go` (generated), `web-app/src/gen/session/v1/backlog_pb.ts` (generated)

#### Story 1.4.3: Add `PipelineMode` to `BacklogItemData`/`BacklogItemUpdate` + ent repository mapping
**As a** backend developer, **I want** the domain struct and ent repository layer to carry `PipelineMode`, **so that** storage reads/writes round-trip the field.
**Acceptance Criteria**:
- `session/repository.go`'s `BacklogItemData` (line ~341 region) gains `PipelineMode string` (plain, not pointer — mirrors `RepoPath`). `BacklogItemUpdate` (line ~417 region) gains `PipelineMode *string` (pointer, for partial-update presence).
  - *Given* a `BacklogItemUpdate{PipelineMode: nil}` (field omitted), *When* `EntRepository.UpdateBacklogItem` is called, *Then* the stored `pipeline_mode` column is untouched. *Given* `BacklogItemUpdate{PipelineMode: ptr("")}` (explicit reset), *When* the same method is called, *Then* the stored column becomes `""`.
- `session/ent_repository_backlog.go` gains `SetPipelineMode(data.PipelineMode)` in the create path (mirroring `SetAutoSpawnSession(data.AutoSpawnSession)` at line 207) and an `if update.PipelineMode != nil { u.SetPipelineMode(*update.PipelineMode) }` block in the update path (mirroring lines 439-440), plus the field added to whatever `fromEnt`-style mapping function reads `item.AutoSpawnSession` at line 143 into `BacklogItemData`.
- **Files**: `session/repository.go`, `session/ent_repository_backlog.go`

##### Task 1.4.3a: Add `PipelineMode` to `BacklogItemData` and `BacklogItemUpdate` (~3 min)
- Files: `session/repository.go`

##### Task 1.4.3b: Wire `PipelineMode` through ent create/update/read mapping (~5 min)
- Add the 3 mapping points in `session/ent_repository_backlog.go` per the acceptance criterion.
- Files: `session/ent_repository_backlog.go`

#### Story 1.4.4: Wire `pipeline_mode` through Create/Update RPC handlers (presence-gated)
**As a** backend developer, **I want** the `CreateBacklogItem`/`UpdateBacklogItem` handlers to gate on proto field presence, **so that** an omitted `pipeline_mode` never silently clobbers an item's existing mode.
**Acceptance Criteria**:
- `server/services/backlog_service_lifecycle.go`'s `CreateBacklogItem` (line ~125) sets `PipelineMode: req.Msg.GetPipelineMode()` (proto-generated getter returns `""` for `nil`, which is the correct default for a new item) in the domain struct construction, mirroring line 160's `AutoSpawnSession: req.Msg.AutoSpawnSession`.
- `UpdateBacklogItem` (line ~195) gains, alongside the existing block at lines 231-236, an explicit presence check: `if req.Msg.PipelineMode != nil { update.PipelineMode = req.Msg.PipelineMode }` — NOT an unconditional wrap like the pre-existing `SkipReviewGate`/`SkipPlanning`/`AutoSpawnSession` lines (231-236), which is precisely the bug class being avoided here.
  - *Given* an existing item with `PipelineMode: "quick"`, *When* `UpdateBacklogItem` is called with a request that sets `title` but leaves `pipeline_mode` unset (`req.Msg.PipelineMode == nil`), *Then* the item's stored `pipeline_mode` remains `"quick"` after the update (not clobbered to `""`).
  - *Given* the same item, *When* `UpdateBacklogItem` is called with `pipeline_mode` explicitly set to `""`, *Then* the item's stored `pipeline_mode` becomes `""` (explicit reset honored).
- **Files**: `server/services/backlog_service_lifecycle.go`

##### Task 1.4.4a: Wire `CreateBacklogItem` (~3 min)
- Files: `server/services/backlog_service_lifecycle.go`

##### Task 1.4.4b: Wire `UpdateBacklogItem` with presence gating (~4 min)
- Files: `server/services/backlog_service_lifecycle.go`

##### Task 1.4.4c: Regression test proving omitted `pipeline_mode` doesn't clobber (~5 min)
- Add a test to `server/services/backlog_service_lifecycle_test.go` (or the file containing existing `UpdateBacklogItem` tests) mirroring the exact structure of whatever regression test already exists for the `SkipReviewGate`/`AutoSpawnSession` clobbering bug (search `grep -n "clobber\|currentFlags" server/services/*_test.go web-app/src/**/*.test.ts` to find and mirror it), asserting the two Given-When-Then scenarios above.
- Files: `server/services/backlog_service_lifecycle_test.go`

#### Story 1.4.5: Map `PipelineMode` in `backlogItemToProto`
**As a** frontend developer, **I want** `pipeline_mode` returned on every `BacklogItem` proto response, **so that** the UI can read an item's current mode.
**Acceptance Criteria**:
- `server/services/backlog_service.go`'s proto-mapping function (line ~471 region, alongside `AutoSpawnSession: item.AutoSpawnSession`) sets `PipelineMode: &item.PipelineMode` (or the ptr-helper this codebase uses for `optional string` proto fields — check existing usage via `grep -n "proto.String\|&item\." server/services/backlog_service.go` for the established idiom before writing this).
  - *Given* a `BacklogItemData{PipelineMode: "quick"}`, *When* `backlogItemToProto(item)` is called, *Then* the resulting `*sessionv1.BacklogItem.PipelineMode` dereferences to `"quick"`.
- **Files**: `server/services/backlog_service.go`

##### Task 1.4.5a: Add the mapping line (~2 min)
- Files: `server/services/backlog_service.go`

---

### Epic 1.5: Wire `PipelineEngine` into the 4 call sites
**Goal**: `WriteSlashCommands`, headless triage, review-gate, and initial-prompt construction all consult the shared `PipelineEngine` instance instead of calling the hardcoded functions directly — and both `WriteSlashCommands` callers (sync path and triage path) are updated together so neither is left on the old path.

#### Story 1.5.1: Construct and share one `PipelineEngine` instance
**As a** backend developer, **I want** exactly one `CachingPipelineEngine` instance shared by `BacklogService` and `BacklogLifecycleListener`, **so that** the two never observe divergent cache state after a write.
**Acceptance Criteria**:
- `server/dependencies.go` constructs `pipelineEngine, err := session.NewPipelineEngine(pipelineModeRepo)` near the existing `workflowEngine := session.NewDefaultWorkflowEngine()` (line ~459), using the same `pipelineModeRepo := session.NewEntPipelineModeRepository(entClient)` construction pattern as `workflowRepo` (lines 989-994). The same `pipelineEngine` value is passed into `services.NewBacklogService(storage, sessionService, cfg, workflowEngine, pipelineEngine)` (extending the call at line 871) AND into whatever constructs `BacklogLifecycleListener`/`NewReviewGateRunner` (`session/backlog_lifecycle.go:296`).
  - *Given* `server/dependencies.go`'s `BuildRuntimeDeps`, *When* it runs, *Then* exactly one `*session.CachingPipelineEngine` value exists in the dependency graph and both `BacklogService.pipelineEngine` and `BacklogLifecycleListener.runner.pipelineEngine` point at the same instance (verified by a pointer-equality assertion in an integration test).
- **Files**: `server/dependencies.go`

##### Task 1.5.1a: Construct `pipelineModeRepo` and `pipelineEngine` in `dependencies.go` (~4 min)
- Files: `server/dependencies.go`

##### Task 1.5.1b: Add `pipelineEngine session.PipelineEngine` field + constructor param to `BacklogService` (~4 min)
- `server/services/backlog_service.go`: add field (alongside the existing `engine session.WorkflowEngine` field) and thread it through `NewBacklogService`'s signature and the nil-guard fallback (mirroring the existing `engine` nil-guard pattern in the same constructor).
- Files: `server/services/backlog_service.go`

##### Task 1.5.1c: Add `pipelineEngine PipelineEngine` field + constructor param to `ReviewGateRunner`/`BacklogLifecycleListener` (~5 min)
- `session/review_gate.go`: add field to `ReviewGateRunner` struct + `NewReviewGateRunner` param. `session/backlog_lifecycle.go`: thread the same value through to line 296's `NewReviewGateRunner(...)` call, adding a `pipelineEngine` param/field to `BacklogLifecycleListener` itself.
- Files: `session/review_gate.go`, `session/backlog_lifecycle.go`

##### Task 1.5.1d: Update all `NewReviewGateRunner`/`NewBacklogService` call sites (tests) (~5 min)
- Update the ~20 test call sites found via `grep -rn "NewReviewGateRunner(" --include="*.go" .` (`session/review_gate_test.go`, `session/review_gate_integration_test.go`) to pass a test-double `PipelineEngine` (a simple struct returning today's default behavior, or nil if the runner is made nil-tolerant like other optional deps — follow the codebase's existing nil-guard convention for optional constructor params).
- Files: `session/review_gate_test.go`, `session/review_gate_integration_test.go`

#### Story 1.5.2: `WriteSlashCommands` call sites consult `PipelineEngine`
**As a** backend developer, **I want** both callers of `WriteSlashCommands` to pass the shared engine, **so that** the sync-time path and the triage-time path never diverge in which command set they write.
**Acceptance Criteria**:
- `server/services/backlog_service_triage.go:436` calls `session.WriteSlashCommands(s.pipelineEngine, item, worktreePath)`.
- `server/services/backlog_service_sync.go:93` calls `session.WriteSlashCommands(s.pipelineEngine, item, worktreePath)`.
  - *Given* a `BacklogItemData{PipelineMode: "quick"}` synced in via `AttachSessionToItem` (the `backlog_service_sync.go:93` call site), *When* the sync path runs, *Then* the written `.claude/commands/backlog/*.md` files reflect the `"quick"` mode's templates, not the default set — proving the two callers can no longer drift (the exact regression `research/pitfalls.md` §5 point 1 warns about).
- **Files**: `server/services/backlog_service_triage.go`, `server/services/backlog_service_sync.go`

##### Task 1.5.2a: Update the 2 call sites (~3 min)
- Files: `server/services/backlog_service_triage.go`, `server/services/backlog_service_sync.go`

##### Task 1.5.2b: Regression test proving both callers use the same engine output (~5 min)
- Add a test asserting `SpawnSessionFromItem`'s and `AttachSessionToItem`'s written command files are identical for the same item+mode — a direct test for the "2 independent callers must not drift" blast-radius risk.
- Files: `server/services/backlog_service_triage_test.go`

#### Story 1.5.3: Triage prompt building consults `PipelineEngine.TriagePromptFor`
**As a** backend developer, **I want** `TriggerTriage`'s prompt construction to go through the engine, **so that** a non-default mode changes what the triage LLM call actually sees.
**Acceptance Criteria**:
- `server/services/backlog_service_triage.go:718` (`triagePrompt = session.BuildHeadlessTriagePrompt(item, artifactAbsPath)`) becomes `triagePrompt = s.pipelineEngine.TriagePromptFor(item, artifactAbsPath)`. The retriage branch at line 716 (`BuildHeadlessRetriagePrompt`) is left unchanged and NOT routed through `PipelineEngine` — explicitly documented as mode-independent per `research/architecture.md` §3's recommendation ("refine the existing plan" is inherently mode-independent).
  - *Given* `item.PipelineMode == "quick"` with a custom `TriagePromptTemplate`, *When* `TriggerTriage` runs its first-triage (non-retriage) branch, *Then* the LLM call receives the `"quick"` mode's rendered triage prompt, not `BuildHeadlessTriagePrompt`'s default text.
- **Files**: `server/services/backlog_service_triage.go`

##### Task 1.5.3a: Update the call site + add a comment documenting the retriage exclusion (~3 min)
- Files: `server/services/backlog_service_triage.go`

#### Story 1.5.4: Review-gate prompt building consults `PipelineEngine.ReviewPromptFor`
**As a** backend developer, **I want** both `ReviewGateRunner.Run` and `TriggerReReview` to build their review prompt via the engine, **so that** review behavior varies by mode too.
**Acceptance Criteria**:
- Before writing this task's code, re-read the CURRENT state of `session/review_gate.go` (around the `BuildHeadlessReviewPrompt` call, previously at line ~251 per stale research but confirmed changed by PR #155 — locate via `grep -n "BuildHeadlessReviewPrompt" session/review_gate.go`) and `server/services/backlog_service_triage.go`'s `TriggerReReview` (confirmed at line 887 onward in this plan's own verification pass) — do not trust old line numbers.
- `ReviewGateRunner.Run`'s call to `BuildHeadlessReviewPrompt(...)` becomes `r.pipelineEngine.ReviewPromptFor(...)` with identical arguments. `TriggerReReview`'s direct call to the same function makes the identical substitution using `s.pipelineEngine`.
- `Run`'s existing `if item.SkipReviewGate { return }` short-circuit (confirmed at `session/review_gate.go:121`) is untouched — mode selection does not gate whether review runs at all, only its prompt content, per the compose-not-subsume Pattern Decision.
  - *Given* `item.PipelineMode == "quick"` and `item.SkipReviewGate == false`, *When* `ReviewGateRunner.Run` is invoked, *Then* the review LLM call receives the `"quick"` mode's rendered review prompt AND the review gate still runs (is not skipped).
- **Files**: `session/review_gate.go`, `server/services/backlog_service_triage.go`

##### Task 1.5.4a: Re-verify current `BuildHeadlessReviewPrompt` call site in `review_gate.go` and update it (~4 min)
- Files: `session/review_gate.go`

##### Task 1.5.4b: Update `TriggerReReview`'s call site (~4 min)
- Files: `server/services/backlog_service_triage.go`

#### Story 1.5.5: `SpawnSessionFromItem`'s initial prompt consults `PipelineEngine.InitialPromptFor`
**As a** backend developer, **I want** the prompt handed to `inst.Prompt` (and therefore to `AutonomousDriver`'s `goal`) to go through the engine, **so that** autonomous-mode sessions genuinely change behavior under a non-default mode, not just interactive slash-command sets.
**Acceptance Criteria**:
- `server/services/backlog_service_triage.go:260`'s `prompt := session.BuildTokenBudgetedPrompt(item, priorSessions)` becomes `prompt := s.pipelineEngine.InitialPromptFor(item, priorSessions)`.
  - *Given* `item.PipelineMode == "quick"` and `item.AutoSpawnSession == true`, *When* `SpawnSessionFromItem` runs and the item is autonomous, *Then* `NewAutonomousDriver`'s `goal` parameter (passed `inst.Prompt` verbatim, per `research/architecture.md` §2's traced call chain) contains the `"quick"` mode's rendered initial-prompt content, not the default `BuildTokenBudgetedPrompt` output.
- **Files**: `server/services/backlog_service_triage.go`

##### Task 1.5.5a: Update the call site (~3 min)
- Files: `server/services/backlog_service_triage.go`

---

### Epic 1.6: Snapshot resolved mode onto `ItemSession`
**Goal**: An item's mode choice is immutable-after-first-triage-session in effect, by recording what was actually resolved at session-start time — mirroring the existing `AcSnapshot` field.

#### Story 1.6.1: Add `pipeline_mode_snapshot` field to `ItemSession`
**As a** backend developer, **I want** an `ItemSession`-level snapshot of the resolved mode slug, **so that** a later mode edit/deletion doesn't retroactively change what a historical session is shown to have run.
**Acceptance Criteria**:
- `session/ent/schema/item_session.go` gains `field.String("pipeline_mode_snapshot").Default("").Comment("The PipelineMode slug resolved and in effect when this session first started — snapshotted so later edits to the item's live pipeline_mode, or to the mode definition itself, don't retroactively change what this session is shown to have run. Mirrors ac_snapshot's discipline.")`.
- `session/repository.go`'s `ItemSessionSummary` (line ~283 region) gains `PipelineModeSnapshot string`, placed near `AcSnapshot` (line 288).
  - *Given* an `ItemSessionSummary` created before this field existed, *When* it is read back, *Then* `PipelineModeSnapshot == ""` (safe zero-value default, distinguishable from "was `default` mode" only in that both render identically — acceptable since `""` already means "default" everywhere else in this design).
- **Files**: `session/ent/schema/item_session.go`, `session/repository.go`

##### Task 1.6.1a: Add ent field + regenerate (~4 min)
- Files: `session/ent/schema/item_session.go`, `session/ent/*` (generated)

##### Task 1.6.1b: Add `PipelineModeSnapshot` to `ItemSessionSummary` + ent mapping (~4 min)
- Files: `session/repository.go`, `session/ent_repository_backlog.go` (wherever `ItemSessionSummary` is populated from ent — locate via `grep -n "AcSnapshot:" session/ent_repository_backlog.go`)

#### Story 1.6.2: Populate the snapshot at session-start call sites
**As a** backend developer, **I want** the snapshot written exactly once, when a session/triage first starts, **so that** it reflects what was actually resolved at that moment.
**Acceptance Criteria**:
- `SpawnSessionFromItem` (`server/services/backlog_service_triage.go:157`) sets `PipelineModeSnapshot: item.PipelineMode` when creating the new `ItemSession` row (locate the exact `CreateItemSession`-equivalent call within this function).
- `TriggerTriage`'s session-creation path does the same for headless-triage-spawned sessions.
  - *Given* `item.PipelineMode == "quick"` at the moment `SpawnSessionFromItem` is called, *When* the resulting `ItemSession` is later read back (even after the item's `pipeline_mode` field is subsequently changed to `"full"`), *Then* `ItemSessionSummary.PipelineModeSnapshot == "quick"` (frozen), while `BacklogItemData.PipelineMode == "full"` (live, changed).
- **Files**: `server/services/backlog_service_triage.go`

##### Task 1.6.2a: Wire the snapshot into `SpawnSessionFromItem`'s session-creation call (~3 min)
- Files: `server/services/backlog_service_triage.go`

##### Task 1.6.2b: Wire the snapshot into `TriggerTriage`'s session-creation call (~3 min)
- Files: `server/services/backlog_service_triage.go`

---

### Epic 1.7: Characterization tests, observability, and the isolated zero-regression commit gate
**Goal**: Prove Phase 1's default-mode path is byte-identical to pre-change behavior, and add the Info/Debug/Warn logging the Observability Requirements mandate — then land Phase 1 as its own reviewable commit before Phase 2 begins.

#### Story 1.7.1: Golden-file characterization tests for the default mode
**As a** backend developer, **I want** a snapshot test comparing pre- and post-`PipelineEngine` output for `WriteSlashCommands`, triage prompt, review prompt, and initial prompt, **so that** a silent behavior drift in the default path is caught by CI, not by a live item misbehaving.
**Acceptance Criteria**:
- A new test file captures the exact output of `buildDefaultSlashCommandSet`, `BuildHeadlessTriagePrompt`, `BuildHeadlessReviewPrompt`, and `BuildTokenBudgetedPrompt` for 2-3 representative `BacklogItemData` fixtures (varying AC-criteria count, at least one with 0 criteria) BEFORE any `PipelineEngine` wiring, stored as golden fixture files; a second test asserts `CachingPipelineEngine`'s equivalent methods (`SlashCommandSet`, `TriagePromptFor`, `ReviewPromptFor`, `InitialPromptFor`) produce byte-identical output against the same fixtures.
  - *Given* the golden fixture for a 2-criteria item captured pre-refactor, *When* `CachingPipelineEngine{}.SlashCommandSet(sameItem)` is called post-refactor, *Then* `reflect.DeepEqual` (or a byte-for-byte string comparison per map key) reports no difference.
- **Files**: `session/pipeline_engine_characterization_test.go`, `session/testdata/pipeline_engine/*.golden` (new fixture directory)

##### Task 1.7.1a: Capture golden fixtures for 3 representative items (~5 min)
- Files: `session/testdata/pipeline_engine/*.golden`

##### Task 1.7.1b: Write the characterization test comparing engine output to fixtures (~5 min)
- Files: `session/pipeline_engine_characterization_test.go`

#### Story 1.7.2: Observability logging at the 4 call sites
**As an** operator, **I want** Info-level logs of which mode was resolved at triage-start and review-start, Debug-level cache activity, and Warn-level unresolved-mode fallbacks, **so that** "why did this item run the wrong skill set" is debuggable without new tooling.
**Acceptance Criteria**:
- `TriggerTriage` logs `log.InfoLog.Printf("[PipelineEngine] item=%s stage=triage mode=%q", item.ID, resolvedModeLabel(item.PipelineMode))` once per triage start (`resolvedModeLabel` renders `""` as `"default"` for log readability).
- `ReviewGateRunner.Run` logs the same shape with `stage=review`.
- `pipelineModeCache.Load`/`Invalidate` log at Debug: `log.DebugLog.Printf("[PipelineEngine] cache refreshed: %d enabled modes", len(modes))`.
- Every fallback path from Story 1.3.3 logs at Warn (already specified there — this story verifies all 4 `PipelineEngine` methods do it consistently, not just `SlashCommandSet`).
  - *Given* `TriggerTriage` runs for an item with `PipelineMode == "quick"`, *When* the triage LLM call is dispatched, *Then* the server log contains exactly one line matching `[PipelineEngine] item=<id> stage=triage mode="quick"`.
- **Files**: `server/services/backlog_service_triage.go`, `session/review_gate.go`, `session/pipeline_engine.go`

##### Task 1.7.2a: Add Info logs at triage-start and review-start (~4 min)
- Files: `server/services/backlog_service_triage.go`, `session/review_gate.go`

##### Task 1.7.2b: Add Debug logs to cache Load/Invalidate; verify Warn logs are consistent across all 4 engine methods (~4 min)
- Files: `session/pipeline_engine.go`

#### Story 1.7.3: Land Phase 1 as an isolated, reviewable commit
**As a** backend developer, **I want** Phase 1 (Epics 1.1-1.7) committed as its own PR before Phase 2 begins, **so that** a regression in the seam itself is caught in isolation from any second-mode or CRUD-UI change, per the Risk Control section.
**Acceptance Criteria**:
- `make build && make test` and `make lint` (per `make quick-check`) pass with only Phase 1's files changed; no `PipelineMode` CRUD RPCs, no frontend selector, and no second mode exist yet in this commit range — `item.PipelineMode` is reachable only via direct DB write (e.g. a manual test fixture), not via any shipped UI or RPC.
  - *Given* a fresh clone at the tip of the Phase 1 commit range, *When* `make ci` is run, *Then* it passes, and manually setting an item's `pipeline_mode` column to a nonexistent slug via direct SQL and triggering triage produces the Warn-log-and-default-fallback behavior (Story 1.3.3), not a crash or silent no-op.
- **Files**: N/A (process gate, not a file change)

##### Task 1.7.3a: Run `make ci` and confirm zero-regression on existing backlog test suite (~5 min)
- Files: N/A

---

## Phase 2: CRUD RPCs for `PipelineMode`

### Epic 2.1: Proto `PipelineMode` message + CRUD RPCs
**Goal**: The wire contract for creating/editing/enabling/disabling/listing pipeline modes exists, mirroring `ItemSource`'s CRUD RPC shape (the closest existing precedent within `backlog.proto`) and `WorkflowRepository`'s method surface.

#### Story 2.1.1: Define the `PipelineMode` proto message
**As a** frontend developer, **I want** a `PipelineMode` message on the wire, **so that** the UI can list/create/edit mode definitions.
**Acceptance Criteria**:
- `proto/session/v1/backlog.proto` gains, near the existing `ItemSource` message (line ~122), a new `message PipelineMode { string id = 1; string slug = 2; string name = 3; string description = 4; bool enabled = 5; string status_command_template = 6; string done_command_template = 7; string fail_command_template = 8; string review_command_template = 9; string ship_command_template = 10; string help_command_template = 11; string triage_prompt_template = 12; string review_prompt_template = 13; string initial_prompt_template = 14; google.protobuf.Timestamp created_at = 15; google.protobuf.Timestamp updated_at = 16; }`.
  - *Given* the message definition above, *When* `make proto-gen` runs, *Then* `session/gen/proto/go/session/v1/backlog.pb.go` gains a `PipelineMode` Go struct with all 14 fields and `web-app/src/gen/session/v1/backlog_pb.ts` gains the matching TS type.
- **Files**: `proto/session/v1/backlog.proto`

##### Task 2.1.1a: Write the `PipelineMode` message (~4 min)
- Files: `proto/session/v1/backlog.proto`

#### Story 2.1.2: Define CRUD request/response messages + RPCs on `BacklogService`
**As a** frontend developer, **I want** `CreatePipelineMode`/`UpdatePipelineMode`/`DeletePipelineMode`/`GetPipelineMode`/`ListPipelineModes` RPCs, **so that** the management UI (Epic 3.3) has an API to call.
**Acceptance Criteria**:
- `proto/session/v1/backlog.proto`'s `service BacklogService` (line 354) gains 5 new `rpc` declarations, request/response messages following the exact shape of `CreateItemSourceRequest`/`Response` etc. (lines 404-413): `CreatePipelineModeRequest{slug, name, description, enabled, ...9 template fields}` → `CreatePipelineModeResponse{PipelineMode item}`; `UpdatePipelineModeRequest{id, ...optional variants of the same fields}` → `UpdatePipelineModeResponse{PipelineMode item}`; `DeletePipelineModeRequest{id}` → `DeletePipelineModeResponse{}`; `GetPipelineModeRequest{slug}` → `GetPipelineModeResponse{PipelineMode item}`; `ListPipelineModesRequest{}` → `ListPipelineModesResponse{repeated PipelineMode items}`.
  - *Given* the RPC definitions, *When* `make proto-gen` runs, *Then* `session/gen/proto/go/session/v1/backlog.connect.go` (or equivalent generated ConnectRPC file) gains 5 new handler method stubs on the `BacklogServiceHandler` interface.
- **Files**: `proto/session/v1/backlog.proto`

##### Task 2.1.2a: Write `CreatePipelineMode`/`UpdatePipelineMode` request/response messages + RPCs (~5 min)
- Files: `proto/session/v1/backlog.proto`

##### Task 2.1.2b: Write `DeletePipelineMode`/`GetPipelineMode`/`ListPipelineModes` request/response messages + RPCs (~5 min)
- Files: `proto/session/v1/backlog.proto`

##### Task 2.1.2c: Run `make proto-gen` and verify generated handler stubs (~3 min)
- Files: `session/gen/proto/go/session/v1/*.go` (generated), `web-app/src/gen/session/v1/*.ts` (generated)

---

### Epic 2.2: Go service handlers + cache invalidation
**Goal**: The 5 RPCs are implemented on `BacklogService`, and every write path calls `pipelineEngine.cache.Invalidate` so reads never see stale data after an operator edit.

#### Story 2.2.1: Implement `CreatePipelineMode`/`UpdatePipelineMode` handlers
**As a** backend developer, **I want** the create/update RPC handlers implemented, **so that** the management UI can persist mode definitions.
**Acceptance Criteria**:
- A new file `server/services/backlog_service_pipeline_mode.go` implements `CreatePipelineMode` (calls `s.pipelineModeRepo.Create(...)`, then `s.pipelineEngine.InvalidateCache(ctx)` — new exported method on `CachingPipelineEngine` wrapping the internal cache's `Invalidate`) and `UpdatePipelineMode` (partial-update via `PipelineModeUpdateInput`, same invalidation call).
  - *Given* an empty `pipeline_modes` table, *When* `CreatePipelineMode` is called with `{slug: "quick", name: "Quick Fix", enabled: true, triage_prompt_template: "Fix {{item_id}} fast."}`, *Then* the response's `item.slug == "quick"` AND a subsequent `SlashCommandSet`/`TriagePromptFor` call for an item with `PipelineMode: "quick"` immediately reflects the new mode (no stale-cache window) because `InvalidateCache` was called synchronously before the handler returned.
- **Files**: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.2.1a: Implement `CreatePipelineMode` (~5 min)
- Files: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.2.1b: Implement `UpdatePipelineMode` (~5 min)
- Files: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.2.1c: Add `InvalidateCache(ctx)` method to `CachingPipelineEngine` (~3 min)
- Files: `session/pipeline_engine.go`

#### Story 2.2.2: Implement `DeletePipelineMode`/`GetPipelineMode`/`ListPipelineModes` handlers
**As a** backend developer, **I want** the remaining 3 RPCs implemented, **so that** the management UI can list and remove mode definitions.
**Acceptance Criteria**:
- `DeletePipelineMode` calls `s.pipelineModeRepo.Delete(...)` then `s.pipelineEngine.InvalidateCache(ctx)` — per the Unresolved Questions default, does NOT block on existing item references (relies on fail-closed resolution, Story 1.3.3).
- `GetPipelineMode`/`ListPipelineModes` are read-only, calling `GetBySlug`/`ListAll` respectively (not `ListEnabled` — the management UI must see disabled modes too, to allow re-enabling them).
  - *Given* a mode `"quick"` referenced by an existing `BacklogItemData.PipelineMode`, *When* `DeletePipelineMode` is called for `"quick"`'s ID, *Then* the delete succeeds (no referential-integrity error) and a subsequent triage for that item falls back to default mode with a Warn log (Story 1.3.3's behavior, now exercised via a real delete instead of a test fixture).
- **Files**: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.2.2a: Implement `DeletePipelineMode` (~4 min)
- Files: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.2.2b: Implement `GetPipelineMode`/`ListPipelineModes` (~4 min)
- Files: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.2.2c: Wire `pipelineModeRepo` into `BacklogService` construction (~3 min)
- `server/services/backlog_service.go`: add `pipelineModeRepo session.PipelineModeRepository` field + constructor param; `server/dependencies.go`: pass the same `pipelineModeRepo` constructed in Story 1.5.1a.
- Files: `server/services/backlog_service.go`, `server/dependencies.go`

#### Story 2.2.3: Register the 5 handlers + integration tests
**As a** backend developer, **I want** the handlers registered on the ConnectRPC mux and covered by integration tests, **so that** the RPCs are actually reachable and correct end-to-end.
**Acceptance Criteria**:
- `server/server.go` (or wherever `BacklogService`'s handler is mounted) requires no change if `BacklogService` already implements the full `BacklogServiceHandler` interface (ConnectRPC generates a single interface per service) — confirm via `go build ./...` that `BacklogService` still satisfies `sessionv1connect.BacklogServiceHandler` after the 5 new methods are added.
- Integration tests in `server/services/backlog_service_pipeline_mode_test.go` cover: full Create→Get→Update→Delete round trip; cache invalidation is observable (a `SlashCommandSet` call before and after an `UpdatePipelineMode` reflects the change without a process restart); `ListPipelineModes` includes disabled modes, `ListEnabled`-backed cache does not.
  - *Given* a `PipelineMode` created via `CreatePipelineMode` then immediately updated via `UpdatePipelineMode` to change its `triage_prompt_template`, *When* `TriagePromptFor` is called for an item referencing that mode within the same test (no restart), *Then* it reflects the updated template, not the original one.
- **Files**: `server/services/backlog_service_pipeline_mode_test.go`

##### Task 2.2.3a: Verify interface satisfaction + write the CRUD round-trip test (~5 min)
- Files: `server/services/backlog_service_pipeline_mode_test.go`

##### Task 2.2.3b: Write the cache-invalidation-observability test (~4 min)
- Files: `server/services/backlog_service_pipeline_mode_test.go`

---

### Epic 2.3: Structural validation of content-template fields
**Goal**: A malformed mode definition fails predictably (validation error) at write time, per the NFR's "structural integrity, not access control" requirement.

#### Story 2.3.1: Validate content-template fields on Create/Update
**As an** operator, **I want** a malformed mode definition rejected with a clear error, **so that** a typo doesn't silently produce a broken prompt or invalid slash-command file days later.
**Acceptance Criteria**:
- `CreatePipelineMode`/`UpdatePipelineMode` handlers call a new `session.ValidatePipelineModeContent(fields)` function before persisting: rejects an empty `slug` or a `slug` containing characters outside `[a-z0-9-]` (mirrors whatever slug validation `WorkflowRepository`'s create path already uses — locate via `grep -n "slug" session/ent_workflow_repository.go`), and rejects any content-template field containing raw shell metacharacters intended to prevent accidental future misuse if a template is ever read into a shell context (defense in depth — the design itself never does this per the Constraints/NFR, but validation makes the invariant enforced, not just documented).
  - *Given* `CreatePipelineModeRequest{slug: "Quick Fix!", ...}` (invalid slug — uppercase + space + punctuation), *When* `CreatePipelineMode` is called, *Then* it returns `connect.CodeInvalidArgument` with a message naming the invalid field, and no row is written.
  - *Given* `CreatePipelineModeRequest{slug: "quick", ...}` (valid), *When* `CreatePipelineMode` is called, *Then* it succeeds.
- **Files**: `session/pipeline_engine.go` (or a new `session/pipeline_mode_validation.go`), `server/services/backlog_service_pipeline_mode.go`

##### Task 2.3.1a: Implement `ValidatePipelineModeContent` (~5 min)
- Files: `session/pipeline_mode_validation.go`

##### Task 2.3.1b: Call validation from both Create and Update handlers (~3 min)
- Files: `server/services/backlog_service_pipeline_mode.go`

##### Task 2.3.1c: Validation unit tests (~4 min)
- Files: `session/pipeline_mode_validation_test.go`

---

## Phase 3: Frontend

### Epic 3.1: Shared `RadioGroup` component (extracted from `SessionTypeRadioGroup`)
**Goal**: A reusable, parameterized ARIA radiogroup component exists, with the 2 known a11y gaps fixed, ready to back both the existing session-type selector and the new pipeline-mode selector.

#### Story 3.1.1: Extract `RadioGroup` from `OmnibarCreationPanel.tsx`
**As a** frontend developer, **I want** `SessionTypeRadioGroup`'s rendering logic generalized into a standalone component, **so that** the pipeline-mode selector doesn't duplicate ~130 lines of ARIA radiogroup implementation.
**Acceptance Criteria**:
- `web-app/src/components/ui/RadioGroup.tsx` exports `RadioGroup<T extends string>({ options, value, onChange, groupLabel, hintForValue }: RadioGroupProps<T>)`, where `options: {value: T; label: string; description?: string}[]`, implementing: `role="radiogroup"` + `role="radio"` + `aria-checked` per button (not `aria-selected`), roving tabindex + arrow-key cycling (arrow keys move AND select, no Space/Enter requirement) — logic ported verbatim from `OmnibarCreationPanel.tsx`'s current `SessionTypeRadioGroup` implementation (lines ~105-150).
  - *Given* `<RadioGroup options={[{value:"a",label:"A"},{value:"b",label:"B"}]} value="a" onChange={fn} groupLabel="Test" />`, *When* the user presses ArrowRight while option "a" is focused, *Then* `fn` is called with `"b"` and the second button's `aria-checked` becomes `"true"`.
- `OmnibarCreationPanel.tsx` is refactored to use the new `RadioGroup` for its `SESSION_TYPES` selector, with no behavior change (verified by existing Omnibar tests continuing to pass unmodified).
- **Files**: `web-app/src/components/ui/RadioGroup.tsx`, `web-app/src/components/sessions/OmnibarCreationPanel.tsx`

##### Task 3.1.1a: Create `RadioGroup.tsx` with the ported rendering logic (~5 min)
- Files: `web-app/src/components/ui/RadioGroup.tsx`

##### Task 3.1.1b: Refactor `OmnibarCreationPanel.tsx` to use `RadioGroup` (~5 min)
- Files: `web-app/src/components/sessions/OmnibarCreationPanel.tsx`

##### Task 3.1.1c: Run existing Omnibar tests to confirm no behavior change (~3 min)
- `cd web-app && npx jest --no-coverage --testPathPatterns="OmnibarCreationPanel"`
- Files: N/A

#### Story 3.1.2: Fix the 2 known a11y gaps during extraction
**As a** screen-reader user, **I want** the radiogroup's visible label and hint text programmatically linked, **so that** I hear both when tabbing to the group — not just when the label is visually adjacent.
**Acceptance Criteria**:
- `RadioGroup` accepts a `groupLabelId` (or generates one via `useId()`) and sets `aria-labelledby={groupLabelId}` on the `role="radiogroup"` div, replacing the original's `aria-label` string duplicate.
- `RadioGroup` accepts an optional hint span, gives it a stable `id`, and sets `aria-describedby` on the radiogroup div pointing at it.
  - *Given* a `RadioGroup` with `groupLabel="Pipeline mode"` and a hint span "Choose which skills drive this item", *When* a screen reader (or `@testing-library`'s accessible-name query) inspects the radiogroup, *Then* its accessible name is "Pipeline mode" (via `aria-labelledby`) and its accessible description includes the hint text (via `aria-describedby`) — both verified via `getByRole("radiogroup", { name: "Pipeline mode", description: /Choose which skills/ })` in a test.
- **Files**: `web-app/src/components/ui/RadioGroup.tsx`

##### Task 3.1.2a: Implement `aria-labelledby`/`aria-describedby` wiring (~4 min)
- Files: `web-app/src/components/ui/RadioGroup.tsx`

##### Task 3.1.2b: Add `RadioGroup.test.tsx` covering the a11y wiring (~5 min)
- Files: `web-app/src/components/ui/RadioGroup.test.tsx`

---

### Epic 3.2: Pipeline-mode selector in `BacklogItemForm.tsx`
**Goal**: A user can choose an existing pipeline mode on a backlog item, with the 3 existing checkboxes visually regrouped as independent "Overrides" per the compose-not-subsume UX decision.

#### Story 3.2.1: Fetch available modes and add `pipelineMode` form state
**As a** backlog UI operator, **I want** the form to load the list of enabled pipeline modes, **so that** I can select one when creating/editing an item.
**Acceptance Criteria**:
- `web-app/src/lib/hooks/useBacklogService.ts` gains a `listPipelineModes()` function calling the new `ListPipelineModes` RPC (mirroring the existing pattern for `autoSpawnSession`'s data flow at lines 87/128/270/430/460 — locate the equivalent `list*`-style hook for a comparable precedent, e.g. `useWorkflows` if one exists, else the `ItemSource` list hook).
- `BacklogItemForm.tsx` gains `const [pipelineMode, setPipelineMode] = useState(initialValues?.pipelineMode ?? "")` (line ~41 region, alongside the 3 existing `useState` calls) and includes `pipelineMode` in the `onSubmit` payload (line ~80) and the `useCallback` dependency array (line ~88).
  - *Given* a form with no `initialValues` (new item), *When* the form mounts, *Then* `pipelineMode` state is `""` and the rendered `RadioGroup` shows "Default" selected.
- **Files**: `web-app/src/lib/hooks/useBacklogService.ts`, `web-app/src/components/backlog/BacklogItemForm.tsx`

##### Task 3.2.1a: Add `listPipelineModes` to `useBacklogService.ts` (~4 min)
- Files: `web-app/src/lib/hooks/useBacklogService.ts`

##### Task 3.2.1b: Add `pipelineMode` state + submit wiring to `BacklogItemForm.tsx` (~4 min)
- Files: `web-app/src/components/backlog/BacklogItemForm.tsx`

#### Story 3.2.2: Render the `RadioGroup` selector + regroup the 3 checkboxes as "Overrides"
**As a** backlog UI operator, **I want** the mode selector shown as the primary control with the 3 checkboxes visually subordinate and labeled "Overrides," **so that** I understand mode selection and the checkboxes are independent, per `research/ux.md` §2.
**Acceptance Criteria**:
- `BacklogItemForm.tsx` renders `<RadioGroup options={pipelineModes} value={pipelineMode} onChange={setPipelineMode} groupLabel="Pipeline mode" />` above the existing checkbox block (currently lines 214-268 region), and wraps the 3 existing checkboxes (`skipPlanning`/`skipReviewGate`/`autoSpawnSession`, lines 221/239/257) in a `<fieldset><legend>Overrides (independent of pipeline mode)</legend>...</fieldset>` — no change to the checkboxes' own state/logic, purely visual regrouping.
- Each `RadioGroup` option button has `data-testid={`backlog-pipeline-mode-${mode.slug || "default"}`}` per `.claude/rules/e2e-test-conventions.md`.
  - *Given* the form is rendered, *When* a user inspects the DOM, *Then* the mode selector appears before the "Overrides" fieldset in document order, and the fieldset's `<legend>` text is "Overrides (independent of pipeline mode)".
- **Files**: `web-app/src/components/backlog/BacklogItemForm.tsx`

##### Task 3.2.2a: Render `RadioGroup` + add `data-testid`s (~4 min)
- Files: `web-app/src/components/backlog/BacklogItemForm.tsx`

##### Task 3.2.2b: Wrap the 3 checkboxes in the "Overrides" fieldset (~3 min)
- Files: `web-app/src/components/backlog/BacklogItemForm.tsx`

##### Task 3.2.2c: Update `BacklogItemForm.test.tsx` for the new selector + fieldset (~5 min)
- Files: `web-app/src/components/backlog/BacklogItemForm.test.tsx`

#### Story 3.2.3: Feature registry entry for the selector
**As a** maintainer, **I want** the new selector registered per `.claude/rules/feature-registry.md`, **so that** coverage tooling tracks it.
**Acceptance Criteria**:
- `docs/registry/features/frontend/backlog-pipeline-mode-selector.json` is created with `id: "backlog-pipeline-mode-selector"`, `filePath: "web-app/src/components/backlog/BacklogItemForm.tsx"`, `tested: false` initially (flips to `true` once Epic 4.3's e2e test lands).
  - *Given* the new file, *When* `make registry-generate` runs, *Then* `docs/registry/frontend-features.json` (generated aggregate) includes the new entry with no errors.
- **Files**: `docs/registry/features/frontend/backlog-pipeline-mode-selector.json`

##### Task 3.2.3a: Create the registry entry + run `make registry-generate` (~3 min)
- Files: `docs/registry/features/frontend/backlog-pipeline-mode-selector.json`

---

### Epic 3.3: Management CRUD page (`/settings/pipeline-modes`)
**Goal**: An operator can create/edit/enable/disable pipeline-mode definitions through a dedicated settings page, mirroring `/settings/backlog-sources`.

#### Story 3.3.1: Scaffold the settings page + list view
**As an** operator, **I want** a page listing all pipeline modes (enabled and disabled), **so that** I can see what exists before creating a new one.
**Acceptance Criteria**:
- `web-app/src/app/settings/pipeline-modes/page.tsx` is created, following `web-app/src/app/settings/backlog-sources/page.tsx`'s structure (client component, fetches via `useBacklogService`'s `listPipelineModes` from Story 3.2.1, renders a table/list of `{slug, name, enabled}`).
- `web-app/src/app/settings/page.tsx` (the settings index) gains a nav link/card to `/settings/pipeline-modes`, mirroring the existing link to `/settings/backlog-sources`.
  - *Given* 2 pipeline modes exist (`"quick"` enabled, `"legacy"` disabled), *When* an operator navigates to `/settings/pipeline-modes`, *Then* both rows render, with `"legacy"`'s row visually indicating disabled state (e.g. dimmed/badge), matching `/settings/backlog-sources`'s existing enabled/disabled treatment.
- **Files**: `web-app/src/app/settings/pipeline-modes/page.tsx`, `web-app/src/app/settings/page.tsx`

##### Task 3.3.1a: Scaffold `page.tsx` with the list view (~5 min)
- Files: `web-app/src/app/settings/pipeline-modes/page.tsx`

##### Task 3.3.1b: Add the nav link on the settings index (~2 min)
- Files: `web-app/src/app/settings/page.tsx`

#### Story 3.3.2: Create/edit form + enable/disable/delete actions
**As an** operator, **I want** to create a new mode, edit an existing one's content-template fields, and enable/disable/delete it, **so that** I can iterate on pipeline modes without an engineer.
**Acceptance Criteria**:
- The page includes a form (new component `web-app/src/app/settings/pipeline-modes/PipelineModeForm.tsx`) with fields for `slug` (disabled/read-only on edit — slugs are immutable after creation, matching `Workflow`'s convention if confirmed via a quick check of `web-app/src/app/settings` workflow-editing UI, else document this as a new, explicitly-stated invariant), `name`, `description`, `enabled` toggle, and 9 `<textarea>` inputs for the content-template fields, each labeled with its target file/prompt name (e.g. "Triage prompt", "review.md content").
- Submitting calls `CreatePipelineMode`/`UpdatePipelineMode`; a "Delete" button (with a confirm dialog, matching existing delete-confirmation UX elsewhere in `/settings`) calls `DeletePipelineMode`.
  - *Given* the create form filled with `slug: "quick", name: "Quick Fix", triage_prompt_template: "Fix {{item_id}} fast."` and all other content-template fields left blank, *When* the operator submits, *Then* `CreatePipelineMode` is called with those values and, on success, the new mode appears in the list view from Story 3.3.1 without a page reload.
  - *Given* an invalid slug (`"Quick Fix!"`), *When* the operator submits, *Then* the form displays the `CodeInvalidArgument` error message returned by the backend validation (Story 2.3.1) inline, and no navigation/list-refresh occurs.
- **Files**: `web-app/src/app/settings/pipeline-modes/PipelineModeForm.tsx`, `web-app/src/app/settings/pipeline-modes/page.tsx`

##### Task 3.3.2a: Build `PipelineModeForm.tsx` with the 9 content-template textareas (~5 min)
- Files: `web-app/src/app/settings/pipeline-modes/PipelineModeForm.tsx`

##### Task 3.3.2b: Wire Create/Update submit handlers + inline error display (~5 min)
- Files: `web-app/src/app/settings/pipeline-modes/PipelineModeForm.tsx`

##### Task 3.3.2c: Wire enable/disable toggle + delete-with-confirm (~5 min)
- Files: `web-app/src/app/settings/pipeline-modes/page.tsx`

##### Task 3.3.2d: `PipelineModeForm.test.tsx` covering create, validation error, and delete-confirm (~5 min)
- Files: `web-app/src/app/settings/pipeline-modes/PipelineModeForm.test.tsx`

#### Story 3.3.3: Feature registry entry for the management page
**Acceptance Criteria**:
- `docs/registry/features/frontend/backlog-pipeline-mode-management.json` created per `.claude/rules/feature-registry.md`'s template.
  - *Given* the new file, *When* `make registry-generate` runs, *Then* it appears in the aggregated `frontend-features.json` with `tested: false` until Epic 4.3.
- **Files**: `docs/registry/features/frontend/backlog-pipeline-mode-management.json`

##### Task 3.3.3a: Create the registry entry + run `make registry-generate` (~3 min)
- Files: `docs/registry/features/frontend/backlog-pipeline-mode-management.json`

---

### Epic 3.4: "What ran" read-only surface in `BacklogItemDetail.tsx`
**Goal**: For any item, an operator can see which mode actually drove each session — reading the frozen `ItemSession.pipelineModeSnapshot`, never the item's live (possibly since-changed) field.

#### Story 3.4.1: Render the snapshot per `ItemSession` in `BacklogItemDetail.tsx`
**As a** backlog UI operator, **I want** to see which pipeline mode drove each session on an item, **so that** I can verify a risky change didn't silently ride through a reduced-scrutiny mode, per the Trust job-to-be-done in `research/ux.md` §5.
**Acceptance Criteria**:
- `BacklogItemDetail.tsx` (near the existing `<GateVerdictBox>` render at line ~702) adds a `role="group"` labeled "Pipeline" section per `ItemSession`, showing `session.pipelineModeSnapshot || "default"`. If the snapshot slug is not found in the currently-fetched mode list (deleted/renamed), it degrades to `"custom (unrecognized mode: '<slug>')"` rather than a blank — per `research/ux.md` §4's explicit fallback requirement.
  - *Given* an `ItemSession` with `pipelineModeSnapshot: "quick"` and `"quick"` still exists in the current mode list, *When* `BacklogItemDetail` renders that session, *Then* the "Pipeline" group shows "Quick Fix" (the mode's `name`, resolved by matching `slug`).
  - *Given* an `ItemSession` with `pipelineModeSnapshot: "legacy-fast"` where `"legacy-fast"` no longer exists in the current mode list, *When* `BacklogItemDetail` renders that session, *Then* the "Pipeline" group shows `"custom (unrecognized mode: 'legacy-fast')"`, not a blank or `undefined`.
- **Files**: `web-app/src/components/backlog/BacklogItemDetail.tsx`

##### Task 3.4.1a: Render the per-session "Pipeline" group with the found/unrecognized fallback (~5 min)
- Files: `web-app/src/components/backlog/BacklogItemDetail.tsx`

##### Task 3.4.1b: Update `BacklogItemDetail.test.tsx` covering both the found and unrecognized-mode cases (~5 min)
- Files: `web-app/src/components/backlog/BacklogItemDetail.test.tsx`

---

## Phase 4: Proof of Seam + Registry + E2E

### Epic 4.1: Define a real "quick" pipeline mode via the live UI
**Goal**: Prove the seam is not cosmetic — a mode created entirely through the UI (no code change, no redeploy) measurably changes what `WriteSlashCommands` writes and what the triage/review LLM calls receive, end to end.

#### Story 4.1.1: Create and use the "quick" mode on a real item
**As an** operator, **I want** to define a "quick" mode through `/settings/pipeline-modes` and select it on a backlog item, **so that** the success metric ("adding a new mode requires no engineering involvement... immediately use it on a backlog item") is demonstrably true.
**Acceptance Criteria**:
- Using the UI built in Phase 3, an operator creates a `PipelineMode{slug: "quick", name: "Quick Fix", triage_prompt_template: "This is a quick-fix item — skip deep architecture analysis, focus on the smallest correct change for: {{item_id}}.", ...}` with distinct content in at least 2 of the 9 template fields (to prove more than one call site varies).
- The operator selects `"quick"` on an existing (or new) backlog item via `BacklogItemForm.tsx`'s selector (Epic 3.2) and triggers triage.
  - *Given* the `"quick"` mode created above and selected on item `X`, *When* triage is triggered for item `X`, *Then* the `[PipelineEngine] item=X stage=triage mode="quick"` log line appears (Story 1.7.2) AND the LLM call's prompt contains the custom `triage_prompt_template` text, verified by inspecting the stored `TriageResult`/prompt-capture mechanism already used for triage debugging.
- **Files**: N/A (manual/scripted verification against the running system, not a code change)

##### Task 4.1.1a: Create the "quick" mode via the UI and verify persistence (~3 min)
- Files: N/A

##### Task 4.1.1b: Select "quick" on a test item, trigger triage, verify the log line and prompt content (~5 min)
- Files: N/A

##### Task 4.1.1c: Verify `WriteSlashCommands` output differs from default for the same item (~4 min)
- Compare the written `.claude/commands/backlog/*.md` files for the test item against a default-mode item's files — confirm at least one file's content differs, proving `SlashCommandSet` genuinely varies, not just logs a mode name.
- Files: N/A

---

### Epic 4.2: Observability polish
**Goal**: Confirm the full Observability Requirements are met end-to-end, not just per-unit-test.

#### Story 4.2.1: End-to-end log verification across all 4 call sites + cache events
**Acceptance Criteria**:
- Running the "quick" mode item from Epic 4.1 through triage, a work session spawn, and a review produces, in order: an Info log at triage-start, an Info log at review-start, at least one Debug cache-load log at process startup, and (by deleting the mode mid-flow on a second test item) a Warn fallback log — all captured in one manual verification pass against `~/.stapler-squad/logs/stapler-squad.log`.
  - *Given* the log file after the full flow above, *When* grepped for `[PipelineEngine]`, *Then* it contains at least one Info line per stage, at least one Debug cache line, and (from the deletion sub-case) exactly one Warn line naming the deleted slug.
- **Files**: N/A

##### Task 4.2.1a: Run the full flow and grep the log file for the expected `[PipelineEngine]` lines (~5 min)
- Files: N/A

---

### Epic 4.3: Feature registry + e2e tests
**Goal**: Close out the feature-registry and e2e-coverage obligations from `.claude/rules/feature-registry.md` and `.claude/rules/feature-testing-registry.md`.

#### Story 4.3.1: Backend feature registry entries for the 5 new RPCs
**Acceptance Criteria**:
- `docs/registry/features/backend/` gains one `.json` file per new RPC (`create-pipeline-mode.json`, `update-pipeline-mode.json`, `delete-pipeline-mode.json`, `get-pipeline-mode.json`, `list-pipeline-modes.json`), each with `markerFound: true` once a `// +api: pipeline-mode:create` (etc.) marker is added to the corresponding handler in `server/services/backlog_service_pipeline_mode.go`.
  - *Given* the 5 new files and markers, *When* `make registry-generate` runs, *Then* `docs/registry/backend-features.json` includes all 5 with no `coverage-gaps.json` net increase once `testIds` are populated by Story 4.3.2.
- **Files**: `docs/registry/features/backend/create-pipeline-mode.json`, `docs/registry/features/backend/update-pipeline-mode.json`, `docs/registry/features/backend/delete-pipeline-mode.json`, `docs/registry/features/backend/get-pipeline-mode.json`, `docs/registry/features/backend/list-pipeline-modes.json`, `server/services/backlog_service_pipeline_mode.go`

##### Task 4.3.1a: Add `// +api:` markers to the 5 handlers (~3 min)
- Files: `server/services/backlog_service_pipeline_mode.go`

##### Task 4.3.1b: Create the 5 registry JSON files + run `make registry-generate` (~5 min)
- Files: `docs/registry/features/backend/*.json`

#### Story 4.3.2: E2E test for mode selection + management
**As a** QA reviewer, **I want** an e2e test exercising the mode selector and management page, **so that** this feature has the required Playwright coverage per `.claude/rules/e2e-test-conventions.md`.
**Acceptance Criteria**:
- `tests/e2e/backlog-pipeline-mode.spec.ts` starts with `// @feature backlog:pipeline-mode-select, backlog:pipeline-mode-manage` and covers: (1) creating a mode via `/settings/pipeline-modes`, (2) selecting it on a backlog item via `getByTestId("backlog-pipeline-mode-quick")`, (3) verifying the "what ran" surface shows the mode name after a triage run, using only `data-testid`/ARIA-role locators (no CSS class selectors) and no `waitForTimeout` (uses `expect(locator).toHaveValue(...)`/`waitForSelector` per the convention).
  - *Given* a running test server (`STAPLER_SQUAD_INSTANCE=e2e-local`), *When* `npx playwright test backlog-pipeline-mode.spec.ts` runs, *Then* all 3 scenarios pass.
- **Files**: `tests/e2e/backlog-pipeline-mode.spec.ts`

##### Task 4.3.2a: Write the mode-creation + selection e2e scenario (~5 min)
- Files: `tests/e2e/backlog-pipeline-mode.spec.ts`

##### Task 4.3.2b: Write the "what ran" verification e2e scenario (~5 min)
- Files: `tests/e2e/backlog-pipeline-mode.spec.ts`

##### Task 4.3.2c: Run the new e2e spec, then flip `tested: true` + populate `testIds` on all 4.3.1/3.2.3/3.3.3 registry entries, run `make registry-generate` (~5 min)
- Files: `docs/registry/features/backend/*.json`, `docs/registry/features/frontend/backlog-pipeline-mode-selector.json`, `docs/registry/features/frontend/backlog-pipeline-mode-management.json`
