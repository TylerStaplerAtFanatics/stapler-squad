# Features Research — Rules Management UX

## Existing Feature Landscape

### Current ApprovalRulesPanel State (from code review)
The existing `ApprovalRulesPanel.tsx` (783 lines) provides:
- Source filter tabs (All / Custom / Built-in / Claude Settings)
- Rules table with: Name+Reason+Alternative column, Match chips column, Decision badge, Source badge, Priority, Enabled toggle, Delete button
- Modal form for creating a single custom rule with 3 sections: Rule identity (name, decision, priority), Match conditions (6 fields), Guidance for Claude (reason, alternative)
- AI "Generate Suggestions" panel (analytics-gap-driven AI suggestions)
- AI "Generate from command" collapsible inside the form (command-sample-driven AI suggestions)
- URL param prefill (`?tool=Bash&program=git&open=true`) from analytics drill-down
- Mobile FAB for "+ Add Rule" (header buttons hidden on mobile)
- 7-day analytics summary bar

### UX Friction Points Identified from Code

**1. Empty state is thin:**
```tsx
<div className={empty}>
  No rules found.{" "}
  {sourceFilter === "all" || sourceFilter === "user"
    ? "Use the '+ Add Rule' button above to create one."
    : ""}
</div>
```
This tells users mechanics but not purpose — no explanation of what rules *do*, what an approval rule is for, or why they would want one. A new user landing here has no orientation.

**2. "Add Rule" and "Generate Suggestions" are hidden on mobile:**
Both use `headerButtonsHiddenOnMobile` CSS class. The mobile FAB covers "Add Rule" but there is no mobile path for "Generate Suggestions." This is an intentional call per comments in the code, but is worth revisiting — at minimum the mobile FAB label could be "Add or Import" once YAML import exists.

**3. Match conditions section field ordering:**
Current order: Tool Name → Programs → Subcommands → Command Pattern (regex) → Tool Pattern (regex) → File Pattern (regex). The most common use case is program/subcommand-based rules (which use structured fields), but regex fields appear right after, which may lead users to reach for regex when structured fields would be better. Suggested reorder: Tool Name → Programs → Subcommands (structured group), then Command Pattern → Tool Pattern → File Pattern (regex group), with a visual separator and a hint.

**4. No bulk authoring path:**
The entire current UX is single-rule. There is no affordance indicating that more rules could be added in bulk or that a YAML format exists. The "Import YAML" button does not yet exist.

**5. Form validation is client-side only:**
`handleSave` validates that name is non-empty and at least one match condition is present. Regex fields are not validated before save — invalid patterns are caught server-side in `RulesStore.Upsert` but the user experience is an error banner, not inline validation. The new `ValidateRules` RPC could be used to add live regex validation in the single-rule form too (opportunistic improvement).

**6. No edit capability:**
Rules can be toggled and deleted, but not edited. To change a rule, you delete and re-create it. This is a gap the requirements don't explicitly address, but the YAML import (which can overwrite by name) partially mitigates it.

**7. Priority hint is buried:**
The priority hint ("Lower numbers run first. Custom rules (default: 10) run before built-in rules (default: 1000).") appears only inside the modal form. The table header says "Priority ⓘ" with a tooltip, which is better but the tooltip only appears on hover (not accessible on touch). The table column header tooltip currently says `title="Lower numbers run first..."` which is fine on desktop.

**8. Source badge semantics are confusing:**
"Built-in" for seed rules and "Claude Settings" for claude-settings rules are meaningful only if users understand the difference. There is no tooltip or explanation. Users who imported rules from `~/.claude/settings.json` may not know where "Claude Settings" rules came from.

## Industry Comparable Features

### Terraform/Pulumi Import: File-based resource definitions
The most analogous workflow: users author infrastructure in YAML/HCL, validate with `plan`, then apply. Key lesson: the "plan/preview" step (showing what will change before applying) is the most important UX safety net. The YAML import modal's preview step directly parallels Terraform plan output.

### GitHub Actions / Dependabot config
YAML-based configuration files that are validated on push. Inline validation errors in the GitHub UI (per-line error markers) are highly expected by power users who work with YAML configs daily.

### OPA/Rego Policy Import
Comparable to rule import: bulk upload of policy definitions, server-side validation of syntax + semantics, preview before apply. OPA's `opa check` as a separate validation step before `opa eval` is the same separation the `ValidateRules` RPC provides.

### VS Code Settings Sync / JetBrains Settings Repository
Portable configuration formats. The roundtrip guarantee (export → import → same state) is the canonical requirement for any settings portability feature. Users immediately test this in the first 5 minutes.

## Edge Cases and Failure Modes

### Import edge cases
1. **Empty YAML** (`rules: []` or no `rules` key): should succeed with 0 rules to apply, not error
2. **YAML without top-level `rules` key**: parse error vs validation error distinction matters
3. **Single invalid rule in 50**: should show that rule's errors inline, still allow applying the 49 valid ones
4. **All rules invalid**: "Apply 0 rules" button should be disabled or greyed out, not hidden
5. **Duplicate name, skip mode**: the skipped rules should still appear in the preview with a "will skip" badge, not silently disappear
6. **Duplicate name, overwrite mode**: user needs to understand which fields will change (the preview must show "will overwrite")
7. **500-rule YAML**: table virtualization may be needed in the preview list if showing 500 cards — or pagination
8. **Regex with catastrophic backtracking**: `^(a+)+$` compiles but can DOS the server. Go's `regexp` stdlib uses RE2 semantics which are immune to catastrophic backtracking — this is a free safety property.

### Export edge cases
1. **Export with 0 rules**: should produce valid YAML with empty `rules: []`, not an error or a blank file
2. **Seed/claude-settings rules**: requirements say export "user-authored rules" — seed and claude-settings rules should be excluded (they are not user-authored and may be re-injected on next startup)
3. **Rules with null/empty optional fields**: omitempty in YAML marshaling avoids cluttering the output with empty fields like `command_pattern: ""`
4. **Rule with both `tool` and `tool_pattern`**: the YAML format must document that these are mutually exclusive; the validator should warn or error if both are set

### Roundtrip fidelity
The `source` field should not be exported (it is set to "user" on import regardless). The `id` field is auto-generated on import (not from YAML). The `created_at` timestamp is not in scope for export. All this means the "roundtrip losslessly" criterion applies to the semantic content of rules, not the metadata.

## Unstated User Needs

1. **Syntax guidance inside the textarea**: users authoring YAML from scratch need an example snippet visible in or near the textarea (not just documentation). A "Copy example" button or a collapsible with the example YAML from the requirements would dramatically reduce first-use friction.

2. **Validation feedback while typing**: the 30-second success criterion implies a fast iteration loop. Live validation (debounced, ~500ms after last keystroke) rather than a separate "Validate" button would feel more responsive.

3. **Rule library / starter packs**: out of scope per requirements, but users will immediately ask "can I get common rules for git, npm, cargo?" The export format is the foundation for sharing these. The implementation should not preclude this.

4. **Ability to edit existing rules**: even if edit is out of scope, the YAML export + import-with-overwrite path provides a workaround. Documenting this as the edit path in the UI ("To edit a rule, export, modify the YAML, and re-import with Overwrite mode") would help.

5. **Visibility into which rules are imported vs pre-existing**: the preview list should clearly distinguish "new" from "will overwrite" from "will skip" — three distinct states with distinct visual treatment.
