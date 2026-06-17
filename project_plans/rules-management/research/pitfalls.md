# Pitfalls Research — Rules Management UX

## Security Pitfalls

### 1. YAML Bomb (Alias Expansion Attack) — HIGH RISK

The most dangerous YAML-specific attack. A malicious payload uses YAML anchors and aliases to achieve exponential expansion:

```yaml
a: &a ["lol","lol","lol","lol","lol","lol","lol","lol","lol"]
b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]
c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]
d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]
e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]
```
9 levels deep → 9^9 = 387 million nodes from a ~100-byte input.

**`gopkg.in/yaml.v3` behavior:** It WILL try to expand aliases by default. This can exhaust memory and/or CPU.

**Mitigations:**
1. **Byte limit check before parse**: reject `len(yamlContent) > 512*1024` at the handler level before calling `yaml.Unmarshal`. This prevents the attack entirely since a legitimate 500-rule file rarely exceeds this limit.
2. **Node count limit during walk**: after parsing into `yaml.Node` (the low-level API), walk the node tree and count nodes; abort above a threshold (e.g., 100,000 nodes). This is the defense-in-depth layer.
3. **Do NOT use `yaml.Unmarshal` directly on untrusted input without the byte limit.** The byte limit alone is sufficient for the requirement, but both layers are recommended for production.

**Implementation note:** The recommended approach is a two-phase parse:
```go
// Phase 1: byte limit (fast reject)
if len(yamlContent) > maxYAMLBytes {
    return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("YAML too large"))
}
// Phase 2: unmarshal into typed struct (expansion happens here)
var file YAMLRulesFile
if err := yaml.Unmarshal([]byte(yamlContent), &file); err != nil { ... }
// Phase 3: validate rule count
if len(file.Rules) > maxRuleCount {
    return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("too many rules"))
}
```

### 2. Catastrophic Backtracking in Regex Patterns — LOW RISK (mitigated by Go)

Go's `regexp` package uses RE2 semantics (guaranteed linear-time matching). There is **no catastrophic backtracking possible** with Go's stdlib `regexp`. This is a real risk in other languages (Python, JavaScript, Java) but not here.

However: patterns should still be validated with `regexp.Compile` before storing, as invalid regex syntax causes compile errors that must be caught at validation time, not at match time.

### 3. ID Injection

The YAML format does not include `id` or `source` fields. If an attacker somehow passes a rule with a crafted `id`, they could overwrite an existing rule. **Defense:** The server generates IDs for YAML-imported rules (`id = "user-" + uuid.New().String()`), ignoring any `id` field in YAML. The `source` field is hardcoded to `"user"` on import. This is already the pattern in `UpsertApprovalRule` for new rules.

### 4. Path Traversal / Filesystem Access

Not applicable to this feature — the YAML is submitted as text in the request body, not as a file path. No file reads from user-controlled paths occur.

## UX Pitfalls

### 5. Import Modal Requires Three Round Trips (or Feels Slow) — MEDIUM RISK

The flow: paste YAML → validate → preview → apply. If each step requires a full page re-render or a slow spinner, users abandon after validation. Key UX requirements:
- Validation must be debounced (350-500ms), not triggered by a "Validate" button click
- The preview must render incrementally (show results as they arrive, not wait for all 500)
- "Apply" must give per-rule feedback, not just "done" (especially for partial failures)

**Pitfall:** Implementing "Validate" as a button click rather than debounced onChange makes the UX feel like 1990s web forms. Users expect live feedback when they paste into a textarea.

### 6. Overwrite Mode Confusion — MEDIUM RISK

The duplicate handling (skip/overwrite) is a binary choice at import time applying to ALL duplicates. This can cause accidental data loss if the user forgets they have a rule named "Allow git" and imports a YAML with a different definition of "Allow git" in overwrite mode.

**Mitigations:**
- The preview must visually distinguish "new" (green), "will overwrite" (yellow), and "will skip" (grey) rules
- The "will overwrite" cards should show a diff — old values vs new values — at minimum showing the fields that would change
- The overwrite mode radio default should be **Skip** (conservative), requiring explicit opt-in to overwrite

### 7. Empty State After Filter — LOW RISK

The current empty state has two code paths:
1. `sourceFilter === "all"` and no rules → shows "No rules found. Use the '+ Add Rule' button..."
2. `sourceFilter === "seed"` and no seed rules → shows "No rules found." (no call to action)

After implementing import, there's a third path: user just exported all user rules, deleted them, then needs to re-import. The empty state for `sourceFilter === "user"` should be updated to also offer "Import YAML" as an action.

### 8. The 30-Second Success Criterion Requires Bulk Apply RPC

If `BulkUpsertRules` is not implemented and the frontend makes 20+ sequential `UpsertApprovalRule` calls:
- Each call rebuilds the classifier in-memory
- Each call writes to SQLite
- 20 calls × ~50ms each = 1 second (manageable)
- 500 calls × ~50ms each = 25 seconds (approaches the limit)

**Pitfall:** Using sequential single-rule upserts for bulk import is an O(N) latency pattern that will fail the 500-rule NFR. The `BulkUpsertRules` RPC with a single classifier rebuild at the end is the correct architecture. If forced to use sequential calls, at minimum skip intermediate `rebuildClassifier()` calls and do one final rebuild.

### 9. YAML Serialization Round-Trip Fidelity — MEDIUM RISK

`gopkg.in/yaml.v3` behavior with strings that look like booleans or numbers:
- `decision: allow` → might serialize as an unquoted string (correct)
- `priority: 10` → numeric, serializes cleanly
- A rule `name: "true"` or `name: "null"` → YAML has reserved words; `yaml.Marshal` with strings quotes them correctly, but hand-edited YAML may lose quotes

**Mitigation:** Use `yaml.Marshal` with a struct that has proper types. Never serialize to YAML via string concatenation. The roundtrip test (export → import → same rules) must be in the test plan.

**Also:** `omitempty` tags in the export struct ensure that empty optional fields (`command_pattern: ""`) are omitted rather than serialized as empty strings. This keeps exported YAML clean and readable.

### 10. Proto Field Number Collision — LOW RISK (process)

Adding new message types (`ValidateRulesRequest`, `ParsedRuleResult`, etc.) to `session.proto` requires choosing field numbers that don't collide with existing reserved ranges. The proto file is large (65.7 KB). 

**Pitfall:** Accidentally reusing a field number that was previously used and removed (reserved) causes wire incompatibility. Always add new messages with `= 1` starting field numbers (new message types have their own namespace). For new fields in existing messages, check the current max field number in that message.

**Action:** Before adding new RPCs to `session.proto`, check the end of the `SessionService` service block for the last field number in use. The existing approval rule RPCs are at lines 145–152; find the next available slot for `ValidateRules`, `ExportRules`, and `BulkUpsertRules`.

### 11. Decision Field Naming Inconsistency — MEDIUM RISK

The YAML format uses `allow`/`deny`/`escalate`. The `RuleSpec` struct uses `auto_allow`/`auto_deny`/`escalate`. The `AutoDecision` proto enum uses `AUTO_DECISION_ALLOW`/`AUTO_DECISION_DENY`/`AUTO_DECISION_ESCALATE`. Three different naming conventions that must be correctly mapped.

**Pitfall:** Using the wrong string in the YAML→RuleSpec mapper produces silent `escalate` (the `stringToAutoDecision` default fallback) for what should be `allow`. A user imports "decision: allow" and sees the rule as "Escalate" in the table.

**Mitigation:** The `validateYAMLEntry` function must explicitly error on unrecognized decision strings rather than silently defaulting to escalate. Valid values: `"allow"`, `"deny"`, `"escalate"`.

### 12. Frontend State Desync After Bulk Apply — LOW RISK

If `BulkUpsertRules` partially succeeds (some rules applied, some failed) and the frontend optimistically shows all rules, the UI will be out of sync with the server state.

**Mitigation:** Always call `refresh()` (re-fetch `ListApprovalRules`) after bulk apply completes, regardless of whether all rules succeeded. Never optimistically update the rules table on bulk apply — wait for the server refresh. The single-rule flow already follows this pattern (`await upsertRule(...)` → `await refresh()`).

### 13. YAML with Unknown Fields — UX Pitfall

If a user writes `decision: block` (a typo) or `programs: git` (a string instead of a list), `yaml.v3` behavior depends on decoder configuration:
- By default, unknown fields are silently ignored
- Using `.KnownFields(true)` on the yaml Decoder causes an error for unknown keys

**Recommendation:** Use `KnownFields(true)` during the validation step so that typos like `program: [git]` (missing 's') produce an explicit error rather than silently producing a rule with no program filter. This is the most user-hostile silent failure mode in YAML import features.

### 14. Missing `make generate-proto` in Development Workflow — LOW RISK (process)

New proto types require `make generate-proto` to regenerate Go and TypeScript bindings. If a developer adds proto types but forgets to regenerate and commit the generated files, CI will fail with type errors. This is a known friction point for all proto changes in this codebase.

**Mitigation:** Document in the implementation plan that generated files must be committed. The CLAUDE.md already covers the ent ORM generate workflow — the same reminder applies to proto codegen.
