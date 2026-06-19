# Architecture Research — Rules Management UX

## Integration Points with Existing Systems

### 1. Proto / RPC Layer

`ValidateRules` belongs in `proto/session/v1/session.proto` under the `SessionService` service definition (same service as `ListApprovalRules`, `UpsertApprovalRule`, `DeleteApprovalRule`). The SessionService proto already lives at lines 143–153 of `session.proto`.

New message types to add:
```protobuf
rpc ValidateRules(ValidateRulesRequest) returns (ValidateRulesResponse) {}

message ValidateRulesRequest {
  string yaml_content = 1;
}

message ValidateRulesResponse {
  repeated ParsedRuleResult results = 1;
  int32 valid_count = 2;
  int32 error_count = 3;
}

message ParsedRuleResult {
  ApprovalRuleProto rule = 1;   // populated if valid; id/source/created_at will be empty
  repeated string errors = 2;  // one entry per validation failure
  bool valid = 3;
  string original_name = 4;    // name from YAML (even if invalid) for display
}
```

The `valid_count` and `error_count` summary fields allow the frontend to render "Apply 12 rules (3 have errors)" in the button label without counting client-side.

### 2. `RulesService` — New Method

`ValidateRules` belongs on `RulesService` (not directly on `SessionService`). The delegation pattern: `SessionService` delegates to `rs.rulesSvc.ValidateRules(ctx, req)`. This mirrors how `ListApprovalRules`, `UpsertApprovalRule`, and `DeleteApprovalRule` are implemented.

The method signature:
```go
func (rs *RulesService) ValidateRules(
    ctx context.Context,
    req *connect.Request[sessionv1.ValidateRulesRequest],
) (*connect.Response[sessionv1.ValidateRulesResponse], error)
```

Internal flow:
1. Length check: `len(req.Msg.YamlContent) > 512*1024` → `connect.CodeInvalidArgument`
2. `yaml.Unmarshal` into `YAMLRulesFile` struct (with alias depth limit — see pitfalls)
3. For each entry, call a pure `validateYAMLEntry(entry YAMLRuleEntry) (ApprovalRuleProto, []string)` helper that validates fields and compiles regexes
4. Map results to `ParsedRuleResult` proto
5. Return all results — do not short-circuit

### 3. YAML → `ApprovalRuleProto` Mapping

The YAML field names differ slightly from proto field names (deliberate human-friendly shortening in YAML):

| YAML field | Proto field | Notes |
|---|---|---|
| `name` | `name` | required |
| `tool` | `tool_name` | exact match; mutually exclusive with `tool_pattern` |
| `tool_pattern` | `tool_pattern` | regex |
| `programs` | `criteria_programs` | list |
| `subcommands` | `criteria_subcommands` | list |
| `command_pattern` | `command_pattern` | regex |
| `file_pattern` | `file_pattern` | regex |
| `decision` | `decision` (AutoDecision) | "allow" → ALLOW, "deny" → DENY, "escalate" → ESCALATE |
| `reason` | `reason` | optional |
| `alternative` | `alternative` | optional |
| `priority` | `priority` | int, default 10 |
| `enabled` | `enabled` | bool, default true |

`id`, `source`, `created_at`, `risk_level` are not in the YAML — set by server on apply (`id = "user-" + uuid`, `source = "user"`, `created_at = now()`).

The `decision` mapping ("allow"/"deny"/"escalate" in YAML vs "auto_allow"/"auto_deny"/"escalate" in `RuleSpec`) requires a translation step. Use the shorter user-facing strings in YAML (more readable) and convert in the handler.

### 4. YAML Export Architecture

Export is a new RPC or can return data through the existing `ListApprovalRules` (filtered to `source=user`). Two options:

**Option A: Client-side YAML serialization from `ListApprovalRules` response**
- No new RPC needed
- Client converts `ApprovalRuleProto[]` to YAML text using a TS YAML library
- Con: requires a client-side YAML library (adds ~25 KB to bundle); serialization may not match import format exactly

**Option B: New `ExportRules` RPC returning `bytes` (recommended)**
- Server marshals rules to YAML using `gopkg.in/yaml.v3` with `omitempty` tags
- Returns as a `bytes export_yaml = 1` field or `string yaml_content = 1` in response
- Client creates a `Blob` from the response and triggers download
- Roundtrip fidelity is guaranteed because the same Go struct definitions are used for both import and export
- Consistent with the existing proto-first pattern in this codebase

**Recommended: Option B.** Export endpoint:
```protobuf
rpc ExportRules(ExportRulesRequest) returns (ExportRulesResponse) {}

message ExportRulesRequest {
  repeated string rule_ids = 1;  // empty = export all user rules
}

message ExportRulesResponse {
  string yaml_content = 1;
}
```

### 5. Apply Flow Architecture

The "Apply N rules" step calls `UpsertApprovalRule` once per valid rule. Options:

**Option A: N sequential RPC calls from client**
- Simple, uses existing endpoint
- N=500 means 500 round trips — violates the 30-second criterion for large imports
- Error handling per-rule is trivially trackable

**Option B: New `BulkUpsertRules` RPC (recommended)**
- Single RPC, server loops
- `rebuildClassifier()` called once at the end, not N times
- Duplicate handling (skip/overwrite) is a server-side decision parameter
- Matches the NFR of 500 rules without timeout

```protobuf
rpc BulkUpsertRules(BulkUpsertRulesRequest) returns (BulkUpsertRulesResponse) {}

message BulkUpsertRulesRequest {
  repeated ApprovalRuleProto rules = 1;
  bool overwrite_duplicates = 2;  // false = skip if name already exists
}

message BulkUpsertRulesResponse {
  int32 created = 1;
  int32 updated = 2;
  int32 skipped = 3;
  repeated string errors = 4;  // per-rule errors during apply (should be rare)
}
```

### 6. SessionService Delegation Pattern

`SessionService` (in `session_service.go`) holds a `rulesSvc *RulesService` field (line 72). New RPC methods on `SessionService` delegate:
```go
func (s *SessionService) ValidateRules(ctx context.Context, req *connect.Request[sessionv1.ValidateRulesRequest]) (*connect.Response[sessionv1.ValidateRulesResponse], error) {
    return s.rulesSvc.ValidateRules(ctx, req)
}
```
This is the same pattern used for `ListApprovalRules` and `UpsertApprovalRule`.

### 7. Frontend Architecture

**Component hierarchy:**
```
ApprovalRulesPanel (existing)
├── [existing table, tabs, form modal]
├── ImportRulesModal (new)
│   ├── YAML textarea + live validate (debounced)
│   ├── ParsedRulesPreviewList
│   │   └── ParsedRuleCard (per result)
│   ├── DuplicateModeRadio (skip / overwrite)
│   └── ApplyButton
└── ExportButton (new, in header button row)
```

**New hooks:**
- `useValidateRules(yamlContent: string, debounceMs: number)` — debounced call to `ValidateRules` RPC, returns `{ results, loading, validCount, errorCount }`
- `useBulkUpsertRules()` — calls `BulkUpsertRules` RPC, returns `{ apply, loading, result, error }`
- `useExportRules()` — calls `ExportRules` RPC + triggers browser download

**State machine for import modal:**
```
idle → editing (paste YAML) → validating (debounce) → previewing (results shown)
    → applying (on "Apply" click) → success (modal closes + table refresh)
                                 → partial_error (some rules failed to apply)
```

### 8. Data Flow and Consistency

- `ValidateRules` is read-only — no side effects, safe to call on every keystroke (with debounce)
- `BulkUpsertRules` calls `rs.rebuildClassifier()` once at the end, ensuring the in-memory classifier is consistent after bulk apply
- The existing `exportRulesLocked()` in `RulesStore` (writes to `~/.config/stapler-squad/rules.json`) is called inside `Upsert()`, so bulk upsert needs to call it once after all upserts, or call `Upsert` N times (which calls it N times — acceptable, just redundant). A dedicated `BulkUpsert` method that calls `exportRulesLocked()` once at the end is cleaner.
- No transactional guarantee is needed for bulk apply: partial success is acceptable and should be surfaced to the user. The alternative (full rollback) is complex and not required by the spec.

### 9. Frontend Transport
`getConnectTransport()` is used throughout the hooks (`useApprovalRules.ts` line 52, `useGenerateRule.ts`). New hooks follow the same pattern: lazy-initialize a `createClient(SessionService, getConnectTransport())` ref.
