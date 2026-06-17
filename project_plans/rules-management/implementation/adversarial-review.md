# Adversarial Review: rules-management

**Date**: 2026-06-01
**Verdict**: CONCERNS

---

## Blockers

*(None)*

---

## Concerns

- [ ] **BulkUpsert mutex not held across the full operation** — Task 1.2.3a's `BulkUpsert` acquires `s.mu.Lock()` but the name-index is built from `s.rules` and then the same lock is held for individual `storage.UpsertRule` calls. If `storage.UpsertRule` itself acquires a storage-layer lock, this creates a lock-ordering risk. Recommendation: verify that `RulesStore.mu` is never held while calling into `session.Storage` by checking the existing `Upsert` method's locking discipline; if it is held there too, the bulk path is safe — document it explicitly in a comment.

- [ ] **`rebuildClassifier()` called twice in `BulkUpsertRules`** — Task 1.2.3b calls `rs.rebuildClassifier()` after `rs.rulesStore.BulkUpsert(...)`, but `BulkUpsert` already calls `s.exportRulesLocked()` at the end. The plan does NOT have `BulkUpsert` call `rebuildClassifier` internally (correctly), but the text of Task 1.2.3b says "RulesService.rebuildClassifier()" which is fine. However, the plan must make explicit that `BulkUpsert` on `RulesStore` must NOT call `rebuildClassifier` — that is the `RulesService`'s responsibility. The current plan implies this but does not state it as a requirement. Recommendation: add a note "BulkUpsert must not call rebuildClassifier; the caller (RulesService) owns that responsibility."

- [ ] **`useExportRules` initializes client outside useEffect** — Task 2.1.2a initializes `clientRef.current` with `if (!clientRef.current) { clientRef.current = createClient(...) }` in the render body, not in a `useEffect`. This is inconsistent with `useApprovalRules.ts` which uses `useEffect(() => { clientRef.current = ... }, [])` and can cause SSR hydration issues or double-initialization in React 18 strict mode. Recommendation: move client initialization into a `useEffect` to match the established pattern.

- [ ] **No abort/cleanup for debounced validation on modal unmount** — Task 2.1.1a creates an `AbortController` per call but the cleanup function only clears the `setTimeout`. If the component unmounts while a fetch is in-flight, the state update on the unmounted component will trigger a React warning. Recommendation: add a `isMounted` ref or call `abortRef.current?.abort()` in the `useEffect` cleanup alongside `clearTimeout(timer)`.

- [ ] **`yamlRuleExport.Priority` omitempty with zero value** — In Task 1.2.2a, `Priority int32 \`yaml:"priority,omitempty"\`` will omit the priority field when it is 0, but a rule with explicitly-set `priority: 0` would be silently changed to `priority: 10` (the default) on re-import. The YAML format specifies default priority = 10, so priority 0 is not a valid user-intended value — this is acceptable but should be documented as a known limitation.

- [ ] **No test for the roundtrip success criterion (Success Criteria #2)** — The plan has a `TestExportRules` story, but the acceptance criteria for the roundtrip test says "importing a just-exported file produces identical rules." The plan's test acceptance criteria check individual export behaviors but do not include an explicit integration test that: (a) seeds 3 rules, (b) calls ExportRules, (c) parses the YAML back through ValidateRules, (d) asserts field-for-field equality. Recommendation: add one roundtrip integration test case to Story 1.3.2's acceptance criteria.

- [ ] **Import modal does not handle the 0-valid-rules edge case clearly** — The plan says "Apply N rules button disabled when `validCount === 0`." But the plan does not specify what is shown to the user when ALL rules are invalid — the button is disabled but there is no guidance. The features research explicitly calls out "All rules invalid: 'Apply 0 rules' button should be disabled or greyed out, not hidden." Recommendation: add to Story 3.1.1's acceptance criteria: "When errorCount > 0 and validCount === 0, show a message 'No valid rules to apply. Fix the errors above and try again.'"

- [ ] **BulkUpsertRulesRequest accepts server-side generated fields from client** — Task 1.2.3b's `ruleProtoToSpec` function receives `ApprovalRuleProto` from the client, which includes `id` and `source` fields. The plan says "strip `id`/`source` from the proto and regenerating them" but does not provide the exact implementation. A subtle bug would be to use a non-empty `id` from the proto to update an arbitrary rule (an ID injection attack on the apply path). Recommendation: in the `ruleProtoToSpec` helper, explicitly discard the incoming `id` and always set `source = "user"`, and document this in a comment.

---

## Minors

- **`ExportRulesRequest.rule_ids` naming**: The proto field is `rule_ids` (snake_case) but the TypeScript generated name will be `ruleIds`. This is standard protobuf behavior — just ensure the plan's `useExportRules` hook uses `ruleIds` (camelCase) consistently, which it does.

- **`ParsedRuleResult.original_name` populated even for valid rules**: The plan shows `OriginalName: entry.Name` is set in the result, but for valid rules the `Rule.Name` field also has the name. This is fine and intentional for the duplicate-detection logic on the frontend, but could be noted as intentional redundancy.

- **`KnownFields(true)` rejects the `programs: git` (string instead of list) case**: The pitfalls research notes this as a silent failure mode. However, `KnownFields(true)` only rejects *unknown keys*, not wrong types. A `programs: git` (string value where a list is expected) will produce a yaml.v3 type error during `Decode`, which is caught. This is correct behavior — no action needed, but the comment in `validateYAMLEntry` should note that type mismatches are caught by the decoder, not the validator.

- **No E2E test specified**: The plan has no Playwright E2E test story. The CLAUDE.md rules require every new user-facing feature to have at least one Playwright E2E test. Recommendation: either add a brief E2E test task (open modal, paste YAML, assert preview card counts, click apply, assert table row count increases) or document why the existing unit tests are sufficient.

- **Feature registry file location**: The plan places the registry at `docs/registry/features/rules-yaml-import.json`. Verify this matches the existing per-feature file naming convention in `docs/registry/features/`.

- **`ImportRulesModal.css.ts` imports**: The plan references `vars` from `@/styles/theme.css` but the project's theme contract is at `web-app/src/styles/theme.css.ts`. Ensure the import path in new CSS files matches the actual file, not a `.css` extension.
