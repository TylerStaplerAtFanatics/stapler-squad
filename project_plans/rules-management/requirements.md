# Rules Management UX — Requirements

## Problem

The `/rules` page (`ApprovalRulesPanel`) has two friction points:

1. **Single-rule-at-a-time form** — Creating many rules requires filling the modal form repeatedly. There is no bulk authoring path; power users who want to define a full rule set have to click through the form N times.

2. **No portable format** — Rules live only in the stapler-squad database. There's no way to version-control them, share them across instances, or bootstrap a new installation with a known-good rule set.

## Goal

- **YAML rule files**: a user-defined YAML format for bulk authoring and importing approval rules, with server-side validation before apply.
- **UX improvements** to the existing rules page based on a UX review: identify friction, inconsistency, or missing affordances, then fix them.

## Proposed YAML Format

```yaml
# stapler-squad approval rules — importable via /rules page
rules:
  - name: Allow git log
    tool: Bash
    programs: [git]
    subcommands: [log, show, diff, status]
    decision: allow
    reason: Read-only git inspection

  - name: Deny force push
    tool: Bash
    command_pattern: "git push.*--force"
    decision: deny
    reason: Force pushes rewrite history on shared branches
    alternative: Use --force-with-lease instead

  - name: Allow all reads
    tool: Read
    decision: allow
    priority: 5
```

Schema maps to `ApprovalRuleProto` fields:
- `name` → `name` (required)
- `tool` → `toolName` (exact match) OR `tool_pattern` → `toolPattern` (regex)
- `programs` → `criteriaPrograms` (list)
- `subcommands` → `criteriaSubcommands` (list)
- `command_pattern` → `commandPattern` (regex)
- `file_pattern` → `filePattern` (regex)
- `decision` → `decision` (allow | deny | escalate)
- `reason` → `reason`
- `alternative` → `alternative`
- `priority` → `priority` (default: 10)
- `enabled` → `enabled` (default: true)

## Functional Requirements

### F1 — YAML import via UI
- "Import YAML" button on the `/rules` page opens a modal with a textarea
- User pastes YAML; client sends it to a new `ValidateRules` RPC before showing a preview
- Preview shows each parsed rule as a card: name, match fields, decision
- Validation errors (bad regex, unknown field, missing `name`, invalid decision) shown inline per rule
- "Apply N rules" button saves all valid rules via `UpsertApprovalRule` (one per rule in the YAML)
- Duplicate names: option to skip or overwrite (radio on the import modal)

### F2 — YAML export
- "Export YAML" button on the `/rules` page downloads all user-authored rules as a `.yaml` file
- Optionally: export selected rules (checkbox per row)

### F3 — New `ValidateRules` RPC
```protobuf
rpc ValidateRules(ValidateRulesRequest) returns (ValidateRulesResponse);

message ValidateRulesRequest {
  string yaml_content = 1;
}

message ValidateRulesResponse {
  repeated ParsedRuleResult results = 1;
}

message ParsedRuleResult {
  ApprovalRuleProto rule = 1;    // populated if valid
  repeated string errors = 2;    // populated if invalid
  bool valid = 3;
}
```

Server-side validation:
- Parse YAML (reject non-YAML)
- Validate each rule: name required, decision must be allow/deny/escalate, all regex fields must compile
- Return per-rule results; do not short-circuit on first error (show all errors at once)

### F4 — UX improvements (to be identified by UX review)
Run a UX review of `ApprovalRulesPanel.tsx` and `page.tsx` and identify improvements. Minimum expected findings:
- Discoverability: is the YAML import button placement obvious?
- Mobile: the "Generate Suggestions" and "Add Rule" buttons are hidden on mobile — is that the right call?
- Form usability: the "Match conditions" section has 6 fields; are they ordered well? Are labels clear?
- Empty state: does the empty state explain what rules *do* (not just that none exist)?

## Non-Functional Requirements

- YAML files up to 500 rules (≤ 500 KB) must import without timeout
- Path-traversal and YAML bomb (deeply nested aliases) protection at the server
- All new RPCs follow the existing ConnectRPC pattern in `server/services/`

## Out of Scope

- Automatic sync from a git-tracked YAML file (file-watch-based import)
- Rule conflict detection between imported and existing rules
- Sharing/publishing rule packs to a registry

## Success Criteria

1. A user can import 20+ rules from a YAML file in under 30 seconds, including seeing validation errors for any malformed rules.
2. Exported YAML roundtrips: importing a just-exported file produces identical rules (no data loss).
3. UX improvements identified by the review are implemented and the rules page passes a re-review.
