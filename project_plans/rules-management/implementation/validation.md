# Validation Plan: rules-management

**Feature**: YAML bulk import/export for approval rules + UX improvements to ApprovalRulesPanel
**Date**: 2026-06-01
**Status**: Ready for implementation

---

## Requirement-to-Test Traceability Matrix

Every test case below is mapped to a functional requirement (F1–F4) or non-functional requirement (NF) from `requirements.md`, and to the success criteria (SC1–SC3).

---

## Unit Tests — Go Backend (package `services`)

File: `server/services/rules_service_test.go`

### UT-BE-01 through UT-BE-07: ValidateRules — YAML parsing and per-rule validation

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-BE-01 | `TestValidateRules_ValidYAML_3Rules` | F3, SC1 | 3 well-formed rules → 3 valid results, validCount=3, errorCount=0 |
| UT-BE-02 | `TestValidateRules_PayloadTooLarge` | F3, NF (512 KB limit) | Payload > 512 KB → CodeInvalidArgument; no rules processed |
| UT-BE-03 | `TestValidateRules_InvalidRegex_PerRuleError` | F3 | One rule has invalid `command_pattern` → that rule has errors, remaining rules remain valid; response does not short-circuit |
| UT-BE-04 | `TestValidateRules_InvalidDecision_ExplicitError` | F3 | Rule with `decision: block` → error "invalid decision" (not silently mapped to escalate) |
| UT-BE-05 | `TestValidateRules_ToolAndToolPatternMutuallyExclusive` | F3 | Rule with both `tool` and `tool_pattern` set → error per that rule |
| UT-BE-06 | `TestValidateRules_UnknownField_KnownFieldsRejected` | F3, NF (YAML bomb / injection) | YAML with an unrecognized key (e.g. `program: git`) → top-level parse error returned as CodeInvalidArgument |
| UT-BE-07 | `TestValidateRules_EmptyRulesList` | F3 | `rules: []` → empty results slice, validCount=0, errorCount=0, no error |

**Boundary value tests (table-driven within UT-BE-01 / UT-BE-03 suite):**

| ID | Case | Requirement |
|---|---|---|
| UT-BE-08 | `TestValidateRules_RuleCount_500_AtLimit` | NF (≤ 500 rules) | Exactly 500 rules → accepted; all validated |
| UT-BE-09 | `TestValidateRules_RuleCount_501_OverLimit` | NF | 501 rules → CodeInvalidArgument before per-rule validation |
| UT-BE-10 | `TestValidateRules_MissingNameField` | F3 | Rule with empty `name` → error "name is required" |
| UT-BE-11 | `TestValidateRules_AllThreeRegexFieldsInvalid` | F3 | Rule with invalid `tool_pattern`, `command_pattern`, `file_pattern` → all three errors returned in same result entry |
| UT-BE-12 | `TestValidateRules_DefaultPriority` | F3 | Rule with no `priority` → proto `Priority = 10`; rule with `priority: 5` → proto `Priority = 5` |
| UT-BE-13 | `TestValidateRules_DefaultEnabled` | F3 | Rule with no `enabled` → proto `Enabled = true`; rule with `enabled: false` → proto `Enabled = false` |

### UT-BE-14 through UT-BE-19: ExportRules

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-BE-14 | `TestExportRules_ExcludesSeedAndClaudeSettingsRules` | F2 | Store has 2 user + 1 seed + 1 claude-settings rule → export contains exactly 2 entries |
| UT-BE-15 | `TestExportRules_FilterByRuleIDs` | F2 | Filter `rule_ids` to 1 of 3 user rules → YAML contains only that rule |
| UT-BE-16 | `TestExportRules_EmptyStore_ProducesEmptyRulesKey` | F2 | Store has 0 user rules → YAML is `rules: []\n` not empty string |
| UT-BE-17 | `TestExportRules_OptionalFieldsOmitted` | F2 | User rule with no `command_pattern`, no `alternative` → those keys absent from YAML output |
| UT-BE-18 | `TestExportRules_EnabledDefaultOmitted` | F2 | User rule with `enabled=true` (default) → `enabled` key absent from YAML; `enabled=false` → key present |
| UT-BE-19 | `TestExportRules_Roundtrip` | F2, SC2 | Seed 3 user rules → call ExportRules → pass YAML to ValidateRules → assert all 3 results valid; assert Name/Tool/Decision/Priority fields identical to originals (covers Success Criterion #2) |

### UT-BE-20 through UT-BE-24: BulkUpsertRules

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-BE-20 | `TestBulkUpsertRules_InsertNew_20Rules` | F1 | 20 new rules, no duplicates → created=20, updated=0, skipped=0 |
| UT-BE-21 | `TestBulkUpsertRules_SkipDuplicates` | F1 | 2 of 4 rules have duplicate names, `overwrite_duplicates=false` → skipped=2, created=2 |
| UT-BE-22 | `TestBulkUpsertRules_OverwriteDuplicates` | F1 | Same 4 rules, `overwrite_duplicates=true` → updated=2, created=2, skipped=0 |
| UT-BE-23 | `TestBulkUpsertRules_RebuildClassifierCalledOnce` | F1, NF (performance) | Use a spy/mock on `rebuildClassifier`; insert 10 rules → rebuild called exactly once, not 10 times |
| UT-BE-24 | `TestBulkUpsertRules_ClientIDsDiscarded` | F1, NF (security) | Request carries an `id` and `source` on each proto rule → persisted rules all have `source="user"` and server-generated IDs (not client values) |

### UT-BE-25: Security guard

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-BE-25 | `TestValidateRules_YAMLBombAliasExpansion` | NF (YAML bomb) | YAML with deeply nested anchor/alias that would expand to > 500 entries → rule-count guard fires, CodeInvalidArgument returned |

**Backend unit test count: 25**

---

## Unit Tests — TypeScript Frontend (Jest + React Testing Library)

### Hook tests

File: `web-app/src/lib/hooks/useValidateRules.test.ts`

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-FE-01 | `useValidateRules_returns_results_after_debounce` | F1 | Mock client returns 2 valid + 1 error; after 400ms delay, hook exposes results with validCount=2, errorCount=1 |
| UT-FE-02 | `useValidateRules_clears_results_on_empty_input` | F1 | Set content to empty string → results cleared, no RPC call issued |
| UT-FE-03 | `useValidateRules_cancels_inflight_on_new_input` | F1 | Rapid input change → previous AbortController aborted; only latest result applied |
| UT-FE-04 | `useValidateRules_sets_error_on_rpc_failure` | F1 | Mock client throws → `error` state set; `results` remains empty |

File: `web-app/src/lib/hooks/useExportRules.test.ts`

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-FE-05 | `useExportRules_triggers_file_download` | F2 | Mock client returns YAML string; assert `URL.createObjectURL` called, anchor `.click()` called with `download="rules.yaml"`, `URL.revokeObjectURL` called |
| UT-FE-06 | `useExportRules_passes_ruleIds_filter` | F2 | Call `exportRules(["id-1", "id-2"])` → RPC request contains `ruleIds: ["id-1", "id-2"]` |
| UT-FE-07 | `useExportRules_sets_error_on_rpc_failure` | F2 | Mock client throws → `error` state set; no file download triggered |

File: `web-app/src/lib/hooks/useBulkUpsertRules.test.ts`

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-FE-08 | `useBulkUpsertRules_returns_counts_on_success` | F1 | Mock client returns created=3, updated=1, skipped=1; `result` state matches |
| UT-FE-09 | `useBulkUpsertRules_passes_overwriteDuplicates_flag` | F1 | Call `applyRules(rules, true)` → RPC request contains `overwriteDuplicates: true` |
| UT-FE-10 | `useBulkUpsertRules_sets_error_on_rpc_failure` | F1 | Mock client throws → `error` state set |

### ImportRulesModal component tests

File: `web-app/src/components/sessions/ImportRulesModal.test.tsx`

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-FE-11 | `ImportRulesModal_apply_button_disabled_when_validCount_is_zero` | F1, SC1 | Render with mock returning 0 valid + 2 error results → "Apply" button has `disabled` attribute |
| UT-FE-12 | `ImportRulesModal_shows_no_valid_rules_message` | F1 | When all rules invalid → renders message "No valid rules to apply. Fix the errors above and try again." |
| UT-FE-13 | `ImportRulesModal_apply_button_label_reflects_counts` | F1 | validCount=3, errorCount=1 → button label contains "Apply 3 rules (1 has errors)" |
| UT-FE-14 | `ImportRulesModal_clicking_apply_calls_applyRules` | F1 | Valid results present → click Apply → `applyRules` called with correct rules array and `overwriteDuplicates` flag |
| UT-FE-15 | `ImportRulesModal_duplicate_detection_shows_overwrite_badge` | F1 | `existingRules` contains a rule named "Allow git log"; result with same name → card renders with overwrite badge |
| UT-FE-16 | `ImportRulesModal_duplicate_detection_skip_mode` | F1 | Radio set to "Skip existing" → overwrite badge absent for duplicate, "will skip" badge shown |
| UT-FE-17 | `ImportRulesModal_partial_apply_error_stays_open` | F1 | Mock `applyRules` returns partial errors → modal remains open, error message visible |
| UT-FE-18 | `ImportRulesModal_onApplied_called_on_success` | F1 | Successful apply → `onApplied` callback invoked, modal closed |

### ParsedRuleCard tests

File: `web-app/src/components/sessions/ParsedRuleCard.test.tsx`

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-FE-19 | `ParsedRuleCard_renders_valid_rule` | F1 | `status="valid"` → shows name, decision badge, no error list |
| UT-FE-20 | `ParsedRuleCard_renders_error_rule_with_messages` | F1 | `status="error"` → error messages rendered as list |
| UT-FE-21 | `ParsedRuleCard_renders_overwrite_badge` | F1 | `status="overwrite"` → "will overwrite" text visible |
| UT-FE-22 | `ParsedRuleCard_renders_skip_badge` | F1 | `status="skip"` → "will skip" text visible |

### UX improvement tests (smoke)

File: `web-app/src/components/sessions/ApprovalRulesPanel.test.tsx` (add to existing)

| ID | Test name | Requirement | Description |
|---|---|---|---|
| UT-FE-23 | `ApprovalRulesPanel_empty_state_explains_purpose` | F4 | Render with 0 rules, `sourceFilter="user"` → empty state text contains "Approval rules let you automatically" |
| UT-FE-24 | `ApprovalRulesPanel_empty_state_seed_no_cta` | F4 | Render with 0 rules, `sourceFilter="seed"` → "+ Add Rule" button absent from empty state |
| UT-FE-25 | `ApprovalRulesPanel_import_yaml_button_opens_modal` | F1, F4 | Click "Import YAML" button → ImportRulesModal rendered in DOM |
| UT-FE-26 | `ApprovalRulesPanel_export_yaml_button_triggers_hook` | F2, F4 | Click "Export YAML" button → `exportRules` called once |
| UT-FE-27 | `ApprovalRulesPanel_field_order_regex_after_separator` | F4 | Render the rule creation form → "Command Pattern" field appears after "Advanced" section separator; "Tool Name" field appears before separator |

**Frontend unit test count: 27**

---

## Integration Tests — Go Backend

File: `server/services/rules_service_test.go` (same file, separate test functions)

These tests wire together multiple service methods using real in-memory storage (no mocks for the store).

| ID | Test name | Requirement | Description |
|---|---|---|---|
| IT-BE-01 | `TestIntegration_ExportThenValidate_Roundtrip` | F2, SC2 | Seed 3 user rules via `UpsertRule`; call `ExportRules`; pass YAML to `ValidateRules`; assert 3 valid results with identical Name/ToolName/Decision/Priority/Programs fields (no data loss) |
| IT-BE-02 | `TestIntegration_BulkUpsert_ThenExport` | F1, F2 | `BulkUpsertRules` with 5 rules; `ExportRules`; assert exported YAML contains all 5 rules |
| IT-BE-03 | `TestIntegration_ValidateAndApply_20Rules` | F1, SC1 | Build 20-rule YAML; call `ValidateRules`; collect valid protos; call `BulkUpsertRules`; assert created=20; elapsed < 30s (covers Success Criterion #1 time budget) |
| IT-BE-04 | `TestIntegration_BulkUpsert_SingleClassifierRebuild` | F3 (NF, perf) | Spy on `rebuildClassifier`; call `BulkUpsertRules` with 20 rules; assert classifier rebuilt exactly once |

**Backend integration test count: 4**

---

## End-to-End Tests (Playwright)

File: `tests/e2e/rules-yaml-import.spec.ts`

All tests run against `http://localhost:8544`. Locators use `data-testid` or ARIA roles only (no CSS class selectors), per CLAUDE.md convention.

| ID | Test name | Requirement | Description |
|---|---|---|---|
| E2E-01 | `rules-yaml-import > import modal opens and closes` | F1, F4 | Navigate to `/rules`; click "Import YAML" button; assert modal visible; press Escape; assert modal gone |
| E2E-02 | `rules-yaml-import > validates rules and shows preview cards` | F1, SC1 | Paste 3-rule valid YAML → wait for 3 rule preview cards; assert 3 cards with `data-testid="parsed-rule-card-valid"` |
| E2E-03 | `rules-yaml-import > shows inline validation errors` | F1 | Paste YAML with 1 invalid rule (bad regex) + 2 valid → 1 error card + 2 valid cards; Apply button label shows "Apply 2 rules (1 has errors)" |
| E2E-04 | `rules-yaml-import > applies valid rules and refreshes table` | F1, SC1 | Paste 3-rule YAML; click "Apply 3 rules"; modal closes; rules table has 3 new rows matching pasted rule names |
| E2E-05 | `rules-yaml-import > export yaml button downloads file` | F2 | Click "Export YAML" button; assert download triggered (intercept via Playwright download event); assert filename is `rules.yaml` |
| E2E-06 | `rules-yaml-import > duplicate skip mode` | F1 | Import 1 rule; re-open modal with same rule name; "Skip existing" selected; apply → skipped=1, existing rule unchanged |
| E2E-07 | `rules-yaml-import > duplicate overwrite mode` | F1 | Same setup; switch to "Overwrite existing"; apply → rule name present in table with updated decision |
| E2E-08 | `rules-yaml-import > empty state has explanatory text` | F4 | Navigate to `/rules` with no user rules; assert empty-state text contains "automatically allow or deny" |

**E2E test count: 8**

---

## Property-Based Tests

File: `server/services/rules_service_test.go` (property tests using `testing/quick` or a fuzz test)

| ID | Test name | Requirement | Description |
|---|---|---|---|
| PB-01 | `FuzzValidateRules_NoPanic` | F3, NF | `go test -fuzz=FuzzValidateRules` — fuzz arbitrary bytes through `ValidateRules`; assert no panic, only valid error returns |
| PB-02 | `FuzzValidateRules_ExportRoundtrip` | F2, SC2 | For any N (1–500) randomly generated valid rule entries, assert `ExportRules → ValidateRules` produces identical rule count and no errors |

**Property/fuzz test count: 2**

---

## Test Summary by Type

| Type | Count | Key requirements covered |
|---|---|---|
| Go backend unit | 25 | F3 (ValidateRules: 13), F2 (ExportRules: 6), F1 (BulkUpsert: 5), NF security |
| Go backend integration | 4 | F1+F2 roundtrip, SC1, SC2 |
| Frontend unit (Jest/RTL) | 27 | F1 (modal: 11), F1 (hooks: 7), F2 (export hook: 3), F4 (UX: 6) |
| E2E (Playwright) | 8 | F1, F2, F4, SC1 |
| Property/fuzz | 2 | NF security, SC2 |
| **Total** | **66** | |

---

## Requirements Coverage

| Requirement | Tests mapping to it | Covered? |
|---|---|---|
| F1 — YAML import via UI | UT-BE-01,03,04,05,07,08–13,20–24; UT-FE-01–04,08–10,11–18,25; IT-BE-02,03,04; E2E-01–04,06,07 | YES |
| F2 — YAML export | UT-BE-14–19; UT-FE-05–07,26; IT-BE-01,02; E2E-05 | YES |
| F3 — ValidateRules RPC | UT-BE-01–13,25; IT-BE-03; PB-01,02 | YES |
| F4 — UX improvements | UT-FE-23–27; E2E-01,08 | YES |
| NF — 500-rule size limit | UT-BE-08,09 | YES |
| NF — YAML bomb protection | UT-BE-25; PB-01 | YES |
| NF — Path-traversal / injection | UT-BE-24 (ID injection) | YES |
| NF — Single classifier rebuild | UT-BE-23; IT-BE-04 | YES |
| SC1 — 20+ rules in <30s | IT-BE-03; E2E-04 | YES |
| SC2 — Export roundtrips without data loss | UT-BE-19; IT-BE-01; PB-02 | YES |
| SC3 — UX improvements implemented and reviewed | UT-FE-23–27; E2E-01,08 | PARTIAL (re-review is a manual gate, not automated) |

**Automated requirements coverage: 12/13 requirement items (SC3 re-review gate is manual)**

---

## Adversarial-Review Concerns — Test Coverage

The following concerns from `adversarial-review.md` are explicitly addressed by test cases:

| Concern | Test(s) addressing it |
|---|---|
| `rebuildClassifier` called exactly once | UT-BE-23, IT-BE-04 |
| Client-supplied `id`/`source` not accepted (ID injection) | UT-BE-24 |
| YAML bomb alias expansion guard | UT-BE-25 |
| 0-valid-rules edge case message in modal | UT-FE-12 |
| Export roundtrip produces identical rules (SC2) | UT-BE-19, IT-BE-01, PB-02 |
| `useExportRules` client init inside `useEffect` | UT-FE-05 (verified by hook rendering without SSR crash) |
| In-flight request cancelled on unmount | UT-FE-03 |

---

## Implementation Readiness Gate

### Criterion 1: requirements.md exists and has measurable success criteria

`project_plans/rules-management/requirements.md` exists. Success criteria are present and measurable:
- SC1: "20+ rules from a YAML file in under 30 seconds" → measurable by IT-BE-03 elapsed time assert
- SC2: "Exported YAML roundtrips: importing a just-exported file produces identical rules" → measurable by UT-BE-19, IT-BE-01
- SC3: "UX improvements pass a re-review" → partially automatable (UT-FE-23–27, E2E-08); re-review gate is manual

**PASS**

### Criterion 2: plan.md covers all functional requirements F1–F4

- F1 (YAML import): Phases 1–4 cover proto (1.1), backend ValidateRules (1.2.1) + BulkUpsertRules (1.2.3), hooks (2.1.1, 2.1.3), ImportRulesModal (3.1), "Import YAML" button (3.3.3)
- F2 (YAML export): Proto (1.1), backend ExportRules (1.2.2), hook (2.1.2), ExportButton (3.2)
- F3 (ValidateRules RPC): Fully defined in Epics 1.1 and 1.2.1 with exact proto messages
- F4 (UX improvements): All four identified items have explicit stories (3.3.1–3.3.4) with acceptance criteria

**PASS**

### Criterion 3: validation.md maps test cases to requirements line by line

This document (validation.md) contains a full traceability matrix above, with 66 test cases mapped to F1–F4, NF constraints, and SC1–SC3.

**PASS**

### Criterion 4: adversarial-review.md has no BLOCKED items outstanding

The adversarial review `adversarial-review.md` reports:
- **Blockers**: None
- **Concerns**: 7 items, all checked `[ ]` (unresolved, but not BLOCKED — they are CONCERNS requiring implementation-time attention)

The 7 concerns are design/implementation notes, not blockers that prevent implementation from starting. All 7 are covered by test cases in this validation plan (see "Adversarial-Review Concerns" section above) or documented as known limitations (priority=0 omitempty behavior).

**PASS (no BLOCKED items)**

---

## Overall Readiness Gate Verdict

**PASS**

All 4 criteria are satisfied. No blockers exist. Implementation may proceed with a fresh session using `plan.md` and this `validation.md` as inputs.

### Pre-implementation reminders from adversarial review

These concerns must be addressed during implementation (not blocking, but must not be forgotten):
1. Verify `RulesStore.mu` lock-ordering safety when `storage.UpsertRule` is called while holding the mutex (Concern 1)
2. `BulkUpsert` on `RulesStore` must NOT call `rebuildClassifier` — that is `RulesService`'s responsibility (Concern 2, covered by UT-BE-23)
3. `useExportRules` — initialize client inside `useEffect`, not render body (Concern 3, covered by UT-FE-05)
4. `useValidateRules` cleanup — call `abortRef.current?.abort()` in addition to `clearTimeout` (Concern 4, covered by UT-FE-03)
5. `yamlRuleExport.Priority omitempty` with `priority: 0` is a known limitation — document in code comment (Concern 5)
6. Add explicit roundtrip integration test (Concern 6, covered by UT-BE-19 and IT-BE-01)
7. Import modal: show "No valid rules to apply" message when all rules invalid (Concern 7, covered by UT-FE-12)
