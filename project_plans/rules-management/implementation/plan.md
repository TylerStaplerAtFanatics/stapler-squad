# Implementation Plan: rules-management

**Feature**: YAML bulk import/export for approval rules + UX improvements to ApprovalRulesPanel
**Date**: 2026-06-01
**Status**: Ready for implementation
**ADRs**: None — all technology choices use existing stack (gopkg.in/yaml.v3, ConnectRPC, vanilla-extract)

---

## Dependency Visualization

```
Phase 1: Proto + Backend Foundation
  Task 1.1.1a  ──→  Task 1.1.1b (generate-proto)
                         │
                         ▼
  Task 1.2.1a  ──→  Task 1.2.1b (ValidateRules Go impl)
  Task 1.2.2a  ──→  Task 1.2.2b (ExportRules Go impl)
  Task 1.2.3a  ──→  Task 1.2.3b (BulkUpsertRules Go impl)
                         │
  ─────────────────────────────────────────
Phase 2: Frontend Hooks (after generate-proto)
  Task 2.1.1a (useValidateRules)
  Task 2.1.2a (useExportRules)
  Task 2.1.3a (useBulkUpsertRules)
                         │
                         ▼
Phase 3: Frontend Components (after hooks)
  Task 3.1.1a  ──→  Task 3.1.1b (ImportRulesModal)
  Task 3.1.2a  (ParsedRuleCard)
  Task 3.2.1a  (ExportButton)
  Task 3.3.1a  (UX fixes - parallel with modal)
                         │
                         ▼
Phase 4: Tests + Registry
  All test tasks run after their respective implementation tasks
  Registry update is last
```

---

## Phase 1: Proto and Backend

### Epic 1.1: Proto Definitions
**Goal**: Add ValidateRules, ExportRules, and BulkUpsertRules RPCs to the proto and regenerate bindings.

#### Story 1.1.1: Define new RPCs and message types
**As a** developer, **I want** the three new RPCs defined in proto, **so that** the Go and TypeScript bindings are generated and both sides compile against the same contract.
**Acceptance Criteria**:
- `ValidateRules`, `ExportRules`, `BulkUpsertRules` are added to the `SessionService` service block
- All new message types compile with `make generate-proto`
- `make build` passes after regeneration
**Files**:
- `proto/session/v1/session.proto`
- `session/gen/session/v1/session_pb.go` (generated)
- `web-app/src/gen/session/v1/session_pb.ts` (generated)

##### Task 1.1.1a: Add RPCs and messages to session.proto (~4 min)
- Open `proto/session/v1/session.proto`
- After the existing `GenerateSuggestedRule` RPC (line ~165), add three new RPCs to the `SessionService` service block:
  ```protobuf
  // ValidateRules parses and validates a YAML rules file without applying it.
  // Returns per-rule results including any parse or validation errors.
  rpc ValidateRules(ValidateRulesRequest) returns (ValidateRulesResponse) {}

  // ExportRules serializes user-authored rules to YAML format for download.
  // Passing rule_ids limits export to those rules; empty = export all user rules.
  rpc ExportRules(ExportRulesRequest) returns (ExportRulesResponse) {}

  // BulkUpsertRules creates or updates multiple user-defined rules in one call.
  // Rebuilds the in-memory classifier exactly once after all rules are stored.
  rpc BulkUpsertRules(BulkUpsertRulesRequest) returns (BulkUpsertRulesResponse) {}
  ```
- After the existing `GenerateSuggestedRuleRequest`/`Response` messages, add:
  ```protobuf
  message ValidateRulesRequest {
    string yaml_content = 1;
  }

  message ValidateRulesResponse {
    repeated ParsedRuleResult results = 1;
    int32 valid_count = 2;
    int32 error_count = 3;
  }

  message ParsedRuleResult {
    ApprovalRuleProto rule = 1;
    repeated string errors = 2;
    bool valid = 3;
    string original_name = 4;
  }

  message ExportRulesRequest {
    repeated string rule_ids = 1;
  }

  message ExportRulesResponse {
    string yaml_content = 1;
  }

  message BulkUpsertRulesRequest {
    repeated ApprovalRuleProto rules = 1;
    bool overwrite_duplicates = 2;
  }

  message BulkUpsertRulesResponse {
    int32 created = 1;
    int32 updated = 2;
    int32 skipped = 3;
    repeated string errors = 4;
  }
  ```
- Files: `proto/session/v1/session.proto`

##### Task 1.1.1b: Regenerate proto bindings (~2 min)
- Run `make generate-proto`
- Run `make build` to confirm no compile errors
- Commit all generated files in `session/gen/session/v1/` and `web-app/src/gen/session/v1/`
- Files: `session/gen/session/v1/session_pb.go`, `web-app/src/gen/session/v1/session_pb.ts`

---

### Epic 1.2: Backend Service Methods
**Goal**: Implement ValidateRules, ExportRules, and BulkUpsertRules on RulesService with proper security guards.

#### Story 1.2.1: Implement ValidateRules
**As a** server, **I want** to parse and validate a YAML payload without persisting anything, **so that** the client can show per-rule errors before the user applies them.
**Acceptance Criteria**:
- Rejects YAML > 512 KB before unmarshal
- Rejects rule count > 500
- Returns per-rule results; does not short-circuit on first error
- Invalid regex patterns produce an error entry (not a panic)
- Decision field `"allow"`/`"deny"`/`"escalate"` maps correctly; unrecognized values produce an explicit error
- `tool` and `tool_pattern` both set → validation error per rule
- KnownFields enforcement: unrecognized YAML keys produce an error
**Files**:
- `server/services/rules_service.go`

##### Task 1.2.1a: Define YAML structs (~3 min)
- Add at the top of `rules_service.go` (or in a new `yaml_rules.go` if preferred):
  ```go
  const (
      maxYAMLBytes = 512 * 1024
      maxRuleCount = 500
  )

  type yamlRulesFile struct {
      Rules []yamlRuleEntry `yaml:"rules"`
  }

  type yamlRuleEntry struct {
      Name           string   `yaml:"name"`
      Tool           string   `yaml:"tool"`
      ToolPattern    string   `yaml:"tool_pattern"`
      Programs       []string `yaml:"programs"`
      Subcommands    []string `yaml:"subcommands"`
      CommandPattern string   `yaml:"command_pattern"`
      FilePattern    string   `yaml:"file_pattern"`
      Decision       string   `yaml:"decision"`
      Reason         string   `yaml:"reason"`
      Alternative    string   `yaml:"alternative"`
      Priority       int      `yaml:"priority"`
      Enabled        *bool    `yaml:"enabled"`
  }
  ```
- Files: `server/services/rules_service.go`

##### Task 1.2.1b: Implement ValidateRules method and helpers (~5 min)
- Add to `rules_service.go`:
  ```go
  func (rs *RulesService) ValidateRules(
      ctx context.Context,
      req *connect.Request[sessionv1.ValidateRulesRequest],
  ) (*connect.Response[sessionv1.ValidateRulesResponse], error) {
      yaml_content := req.Msg.YamlContent

      // Security: size guard before any parse
      if len(yaml_content) > maxYAMLBytes {
          return nil, connect.NewError(connect.CodeInvalidArgument,
              fmt.Errorf("YAML payload too large: %d bytes (max %d)", len(yaml_content), maxYAMLBytes))
      }

      // Parse with KnownFields to reject typos
      var file yamlRulesFile
      dec := yaml.NewDecoder(strings.NewReader(yaml_content))
      dec.KnownFields(true)
      if err := dec.Decode(&file); err != nil {
          return nil, connect.NewError(connect.CodeInvalidArgument,
              fmt.Errorf("YAML parse error: %w", err))
      }

      // Rule count guard (post-parse, catches alias expansion)
      if len(file.Rules) > maxRuleCount {
          return nil, connect.NewError(connect.CodeInvalidArgument,
              fmt.Errorf("too many rules: %d (max %d)", len(file.Rules), maxRuleCount))
      }

      results := make([]*sessionv1.ParsedRuleResult, 0, len(file.Rules))
      validCount, errorCount := int32(0), int32(0)
      for _, entry := range file.Rules {
          rule, errs := validateYAMLEntry(entry)
          pr := &sessionv1.ParsedRuleResult{
              OriginalName: entry.Name,
              Valid:        len(errs) == 0,
              Errors:       errs,
          }
          if len(errs) == 0 {
              pr.Rule = rule
              validCount++
          } else {
              errorCount++
          }
          results = append(results, pr)
      }

      return connect.NewResponse(&sessionv1.ValidateRulesResponse{
          Results:    results,
          ValidCount: validCount,
          ErrorCount: errorCount,
      }), nil
  }

  // validateYAMLEntry validates a single YAML rule entry and converts it to proto.
  // Returns all validation errors found, not just the first.
  func validateYAMLEntry(e yamlRuleEntry) (*sessionv1.ApprovalRuleProto, []string) {
      var errs []string

      if strings.TrimSpace(e.Name) == "" {
          errs = append(errs, "name is required")
      }

      // Mutually exclusive tool fields
      if e.Tool != "" && e.ToolPattern != "" {
          errs = append(errs, "tool and tool_pattern are mutually exclusive; use one or the other")
      }

      // decision mapping
      decisionMap := map[string]sessionv1.AutoDecision{
          "allow":   sessionv1.AutoDecision_AUTO_DECISION_ALLOW,
          "deny":    sessionv1.AutoDecision_AUTO_DECISION_DENY,
          "escalate": sessionv1.AutoDecision_AUTO_DECISION_ESCALATE,
      }
      decision, ok := decisionMap[e.Decision]
      if !ok {
          errs = append(errs, fmt.Sprintf("invalid decision %q: must be allow, deny, or escalate", e.Decision))
      }

      // Regex validation
      if e.ToolPattern != "" {
          if _, err := regexp.Compile(e.ToolPattern); err != nil {
              errs = append(errs, fmt.Sprintf("tool_pattern is not a valid regex: %v", err))
          }
      }
      if e.CommandPattern != "" {
          if _, err := regexp.Compile(e.CommandPattern); err != nil {
              errs = append(errs, fmt.Sprintf("command_pattern is not a valid regex: %v", err))
          }
      }
      if e.FilePattern != "" {
          if _, err := regexp.Compile(e.FilePattern); err != nil {
              errs = append(errs, fmt.Sprintf("file_pattern is not a valid regex: %v", err))
          }
      }

      if len(errs) > 0 {
          return nil, errs
      }

      priority := int32(e.Priority)
      if priority == 0 {
          priority = 10
      }
      enabled := true
      if e.Enabled != nil {
          enabled = *e.Enabled
      }

      return &sessionv1.ApprovalRuleProto{
          Name:               e.Name,
          ToolName:           e.Tool,
          ToolPattern:        e.ToolPattern,
          CriteriaPrograms:   e.Programs,
          CriteriaSubcommands: e.Subcommands,
          CommandPattern:     e.CommandPattern,
          FilePattern:        e.FilePattern,
          Decision:           decision,
          Reason:             e.Reason,
          Alternative:        e.Alternative,
          Priority:           priority,
          Enabled:            enabled,
          Source:             "user",
      }, nil
  }
  ```
- Files: `server/services/rules_service.go`

##### Task 1.2.1c: Add SessionService delegation for ValidateRules (~2 min)
- In `server/services/session_service.go`, find the `ListApprovalRules` delegation and add below it:
  ```go
  func (s *SessionService) ValidateRules(ctx context.Context, req *connect.Request[sessionv1.ValidateRulesRequest]) (*connect.Response[sessionv1.ValidateRulesResponse], error) {
      return s.rulesSvc.ValidateRules(ctx, req)
  }
  ```
- Files: `server/services/session_service.go`

#### Story 1.2.2: Implement ExportRules
**As a** server, **I want** to serialize user-authored rules to YAML, **so that** clients can download a portable rule file.
**Acceptance Criteria**:
- Only exports `source == "user"` rules (excludes seed and claude-settings)
- `rule_ids` filter respected: empty = all user rules, non-empty = only matching IDs
- Optional fields with zero values are omitted (no `command_pattern: ""` in output)
- Result is valid YAML that roundtrips through `ValidateRules` without errors
**Files**:
- `server/services/rules_service.go`

##### Task 1.2.2a: Add ExportRules Go method (~4 min)
- Define YAML export struct (separate from import struct — `omitempty` tags needed):
  ```go
  type yamlRuleExport struct {
      Name           string   `yaml:"name"`
      Tool           string   `yaml:"tool,omitempty"`
      ToolPattern    string   `yaml:"tool_pattern,omitempty"`
      Programs       []string `yaml:"programs,omitempty"`
      Subcommands    []string `yaml:"subcommands,omitempty"`
      CommandPattern string   `yaml:"command_pattern,omitempty"`
      FilePattern    string   `yaml:"file_pattern,omitempty"`
      Decision       string   `yaml:"decision"`
      Reason         string   `yaml:"reason,omitempty"`
      Alternative    string   `yaml:"alternative,omitempty"`
      Priority       int32    `yaml:"priority,omitempty"`
      Enabled        *bool    `yaml:"enabled,omitempty"`
  }

  type yamlRulesExportFile struct {
      Rules []yamlRuleExport `yaml:"rules"`
  }
  ```
- Add `ExportRules` method on `RulesService`:
  ```go
  func (rs *RulesService) ExportRules(
      ctx context.Context,
      req *connect.Request[sessionv1.ExportRulesRequest],
  ) (*connect.Response[sessionv1.ExportRulesResponse], error) {
      allSpecs := rs.rulesStore.All()

      decisionNames := map[sessionv1.AutoDecision]string{
          sessionv1.AutoDecision_AUTO_DECISION_ALLOW:   "allow",
          sessionv1.AutoDecision_AUTO_DECISION_DENY:    "deny",
          sessionv1.AutoDecision_AUTO_DECISION_ESCALATE: "escalate",
      }

      filterIDs := make(map[string]bool, len(req.Msg.RuleIds))
      for _, id := range req.Msg.RuleIds {
          filterIDs[id] = true
      }

      var entries []yamlRuleExport
      for _, spec := range allSpecs {
          if spec.Source != "user" {
              continue
          }
          if len(filterIDs) > 0 && !filterIDs[spec.ID] {
              continue
          }
          enabled := spec.Enabled
          entry := yamlRuleExport{
              Name:           spec.Name,
              Tool:           spec.ToolName,
              ToolPattern:    spec.ToolPattern,
              Programs:       spec.CriteriaPrograms,
              Subcommands:    spec.CriteriaSubcommands,
              CommandPattern: spec.CommandPattern,
              FilePattern:    spec.FilePattern,
              Decision:       decisionNames[spec.Decision],
              Reason:         spec.Reason,
              Alternative:    spec.Alternative,
              Priority:       spec.Priority,
              Enabled:        &enabled,
          }
          // Omit enabled field if true (default)
          if enabled {
              entry.Enabled = nil
          }
          entries = append(entries, entry)
      }

      if entries == nil {
          entries = []yamlRuleExport{}
      }

      out, err := yaml.Marshal(yamlRulesExportFile{Rules: entries})
      if err != nil {
          return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("YAML marshal failed: %w", err))
      }

      return connect.NewResponse(&sessionv1.ExportRulesResponse{
          YamlContent: string(out),
      }), nil
  }
  ```
- Files: `server/services/rules_service.go`

##### Task 1.2.2b: Add SessionService delegation for ExportRules (~1 min)
- In `server/services/session_service.go`, add:
  ```go
  func (s *SessionService) ExportRules(ctx context.Context, req *connect.Request[sessionv1.ExportRulesRequest]) (*connect.Response[sessionv1.ExportRulesResponse], error) {
      return s.rulesSvc.ExportRules(ctx, req)
  }
  ```
- Files: `server/services/session_service.go`

#### Story 1.2.3: Implement BulkUpsertRules
**As a** server, **I want** to apply multiple rules in one RPC call with a single classifier rebuild, **so that** importing 500 rules completes within the 30-second timeout.
**Acceptance Criteria**:
- All rules stored before `rebuildClassifier()` is called (exactly one rebuild)
- `overwrite_duplicates=false`: rules whose name already exists in the store are skipped (counted in `skipped`)
- `overwrite_duplicates=true`: rules whose name already exists are overwritten
- Partial failures return errors per rule, not a top-level error; the response includes created/updated/skipped counts
- New rules get `id = "user-" + uuid`, `source = "user"`, `created_at = now()`
**Files**:
- `server/services/rules_service.go`
- `server/services/rules_store.go`

##### Task 1.2.3a: Add BulkUpsert to RulesStore (~4 min)
- **Critical**: `BulkUpsert` must NOT call `rebuildClassifier()` — that is `RulesService`'s responsibility. It calls `exportRulesLocked()` once at the end (for JSON file backup only).
- In `rules_store.go`, add a `BulkUpsert` method that takes `[]RuleSpec` and `overwriteDuplicates bool`:
  ```go
  type BulkUpsertResult struct {
      Created int
      Updated int
      Skipped int
      Errors  []string
  }

  func (s *RulesStore) BulkUpsert(specs []RuleSpec, overwriteDuplicates bool) BulkUpsertResult {
      s.mu.Lock()
      defer s.mu.Unlock()

      // Build name index for duplicate check
      nameIndex := make(map[string]string) // name → id
      for _, existing := range s.rules {
          nameIndex[existing.Name] = existing.ID
      }

      result := BulkUpsertResult{}
      for _, spec := range specs {
          existingID, isDuplicate := nameIndex[spec.Name]
          if isDuplicate && !overwriteDuplicates {
              result.Skipped++
              continue
          }
          if isDuplicate {
              spec.ID = existingID
              result.Updated++
          } else {
              if spec.ID == "" {
                  spec.ID = "user-" + uuid.New().String()
              }
              result.Created++
          }
          spec.Source = "user"
          if spec.CreatedAt.IsZero() {
              spec.CreatedAt = time.Now()
          }
          // Upsert via storage (skip exportRulesLocked per-rule)
          if err := s.storage.UpsertRule(spec); err != nil {
              result.Errors = append(result.Errors, fmt.Sprintf("rule %q: %v", spec.Name, err))
              if isDuplicate {
                  result.Updated--
              } else {
                  result.Created--
              }
              continue
          }
          nameIndex[spec.Name] = spec.ID
          // Update in-memory slice
          found := false
          for i, r := range s.rules {
              if r.ID == spec.ID {
                  s.rules[i] = spec
                  found = true
                  break
              }
          }
          if !found {
              s.rules = append(s.rules, spec)
          }
      }
      s.exportRulesLocked() // one write at the end
      return result
  }
  ```
- Files: `server/services/rules_store.go`

##### Task 1.2.3b: Implement BulkUpsertRules on RulesService (~3 min)
- In `rules_service.go`, add:
  ```go
  func (rs *RulesService) BulkUpsertRules(
      ctx context.Context,
      req *connect.Request[sessionv1.BulkUpsertRulesRequest],
  ) (*connect.Response[sessionv1.BulkUpsertRulesResponse], error) {
      specs := make([]RuleSpec, 0, len(req.Msg.Rules))
      for _, proto := range req.Msg.Rules {
          specs = append(specs, ruleProtoToSpec(proto))
      }

      res := rs.rulesStore.BulkUpsert(specs, req.Msg.OverwriteDuplicates)
      rs.rebuildClassifier()

      return connect.NewResponse(&sessionv1.BulkUpsertRulesResponse{
          Created: int32(res.Created),
          Updated: int32(res.Updated),
          Skipped: int32(res.Skipped),
          Errors:  res.Errors,
      }), nil
  }
  ```
- Add `ruleProtoToSpec` helper that converts `*sessionv1.ApprovalRuleProto` → `RuleSpec`. **Security**: always discard the incoming `id` and `source` fields and regenerate them (`id = "user-" + uuid.New().String()`, `source = "user"`) — never accept client-supplied IDs, as this would allow ID injection to overwrite arbitrary rules.
- Files: `server/services/rules_service.go`

##### Task 1.2.3c: Add SessionService delegation for BulkUpsertRules (~1 min)
- In `server/services/session_service.go`, add:
  ```go
  func (s *SessionService) BulkUpsertRules(ctx context.Context, req *connect.Request[sessionv1.BulkUpsertRulesRequest]) (*connect.Response[sessionv1.BulkUpsertRulesResponse], error) {
      return s.rulesSvc.BulkUpsertRules(ctx, req)
  }
  ```
- Files: `server/services/session_service.go`

---

### Epic 1.3: Backend Tests
**Goal**: Unit tests for the new backend methods.

#### Story 1.3.1: ValidateRules tests
**As a** developer, **I want** unit tests for every validation path, **so that** regressions in YAML parsing or validation logic are caught immediately.
**Acceptance Criteria**:
- Test: valid YAML with 3 rules → 3 valid results
- Test: YAML > 512 KB → CodeInvalidArgument error
- Test: rule with invalid regex → error in results, other rules valid
- Test: rule with `decision: block` → explicit error (not silent escalate)
- Test: `tool` + `tool_pattern` both set → error per rule
- Test: KnownFields: unrecognized key `program: git` → parse error
- Test: empty `rules: []` → empty results, no error
**Files**: `server/services/rules_service_test.go`

##### Task 1.3.1a: Write ValidateRules unit tests (~5 min)
- Add `TestValidateRules_*` test functions covering the acceptance criteria cases above
- Use table-driven tests for the per-rule validation cases
- Files: `server/services/rules_service_test.go`

#### Story 1.3.2: ExportRules tests
**Acceptance Criteria**:
- Test: export all user rules excludes seed and claude-settings rules
- Test: roundtrip — marshal 3 user rules, unmarshal the YAML back through `ValidateRules`, get identical fields
- Test: export with `rule_ids` filter returns only requested rules
- Test: export with 0 user rules produces `rules: []\n` (not empty string)
- **Roundtrip integration test**: seed 3 user rules → call ExportRules → pass returned YAML to ValidateRules → assert all 3 results are valid and field values are identical to originals (covers Success Criterion #2)
**Files**: `server/services/rules_service_test.go`

##### Task 1.3.2a: Write ExportRules unit tests (~4 min)
- Files: `server/services/rules_service_test.go`

#### Story 1.3.3: BulkUpsertRules tests
**Acceptance Criteria**:
- Test: bulk insert 20 new rules → created=20, updated=0, skipped=0
- Test: overwrite_duplicates=false, 2 duplicate names → skipped=2
- Test: overwrite_duplicates=true, 2 duplicate names → updated=2
- Test: `rebuildClassifier` called exactly once (not N times) — verify by counting via a mock or spy
**Files**: `server/services/rules_service_test.go`

##### Task 1.3.3a: Write BulkUpsertRules unit tests (~4 min)
- Files: `server/services/rules_service_test.go`

---

## Phase 2: Frontend Hooks

### Epic 2.1: New React Hooks for YAML RPCs
**Goal**: Three new hooks that wrap the new RPCs with debouncing, loading state, and error handling.

#### Story 2.1.1: useValidateRules hook
**As a** frontend component, **I want** a debounced hook that calls `ValidateRules` on YAML input, **so that** the ImportRulesModal shows live feedback without hammering the server.
**Acceptance Criteria**:
- Debounce delay configurable (default 400ms)
- Returns `{ results, loading, validCount, errorCount, error }`
- Clears results when YAML input is empty (don't call RPC for empty string)
- AbortController cancels in-flight request when new input arrives
**Files**: `web-app/src/lib/hooks/useValidateRules.ts`

##### Task 2.1.1a: Create useValidateRules.ts (~4 min)
```typescript
"use client";

import { useEffect, useState, useRef, useCallback } from "react";
import { createClient } from "@connectrpc/connect";
import { SessionService } from "@/gen/session/v1/session_pb";
import { ValidateRulesRequestSchema, type ParsedRuleResult } from "@/gen/session/v1/session_pb";
import { create } from "@bufbuild/protobuf";
import { getConnectTransport } from "@/lib/api/transport";

interface UseValidateRulesReturn {
  results: ParsedRuleResult[];
  loading: boolean;
  validCount: number;
  errorCount: number;
  error: Error | null;
}

export function useValidateRules(
  yamlContent: string,
  debounceMs = 400
): UseValidateRulesReturn {
  const [results, setResults] = useState<ParsedRuleResult[]>([]);
  const [loading, setLoading] = useState(false);
  const [validCount, setValidCount] = useState(0);
  const [errorCount, setErrorCount] = useState(0);
  const [error, setError] = useState<Error | null>(null);

  const clientRef = useRef<ReturnType<typeof createClient<typeof SessionService>> | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    clientRef.current = createClient(SessionService, getConnectTransport());
  }, []);

  useEffect(() => {
    if (!yamlContent.trim()) {
      setResults([]);
      setValidCount(0);
      setErrorCount(0);
      setError(null);
      return;
    }

    const timer = setTimeout(async () => {
      if (!clientRef.current) return;
      abortRef.current?.abort();
      abortRef.current = new AbortController();

      setLoading(true);
      setError(null);
      try {
        const req = create(ValidateRulesRequestSchema, { yamlContent });
        const resp = await clientRef.current.validateRules(req, {
          signal: abortRef.current.signal,
        });
        setResults(resp.results ?? []);
        setValidCount(resp.validCount);
        setErrorCount(resp.errorCount);
      } catch (err) {
        if ((err as Error).name === "AbortError") return;
        setError(err instanceof Error ? err : new Error("Validation failed"));
      } finally {
        setLoading(false);
      }
    }, debounceMs);

    return () => {
      clearTimeout(timer);
      abortRef.current?.abort(); // cancel in-flight request on unmount or dependency change
    };
  }, [yamlContent, debounceMs]);

  return { results, loading, validCount, errorCount, error };
}
```
- Files: `web-app/src/lib/hooks/useValidateRules.ts`

#### Story 2.1.2: useExportRules hook
**As a** frontend component, **I want** a hook that calls `ExportRules` and triggers a browser file download, **so that** the user gets a `.yaml` file without navigating away.
**Acceptance Criteria**:
- Calls `ExportRules` with optional `ruleIds` filter
- On success: creates a `Blob`, `URL.createObjectURL`, triggers `<a download="rules.yaml">` click, then revokes the URL
- Returns `{ exportRules, loading, error }`
**Files**: `web-app/src/lib/hooks/useExportRules.ts`

##### Task 2.1.2a: Create useExportRules.ts (~3 min)
```typescript
"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { SessionService } from "@/gen/session/v1/session_pb";
import { ExportRulesRequestSchema } from "@/gen/session/v1/session_pb";
import { create } from "@bufbuild/protobuf";
import { getConnectTransport } from "@/lib/api/transport";

interface UseExportRulesReturn {
  exportRules: (ruleIds?: string[]) => Promise<void>;
  loading: boolean;
  error: Error | null;
}

export function useExportRules(): UseExportRulesReturn {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const clientRef = useRef<ReturnType<typeof createClient<typeof SessionService>> | null>(null);

  // Initialize inside useEffect to match the established hook pattern and avoid SSR issues
  useEffect(() => {
    clientRef.current = createClient(SessionService, getConnectTransport());
  }, []);

  const exportRules = useCallback(async (ruleIds?: string[]) => {
    if (!clientRef.current) return;
    setLoading(true);
    setError(null);
    try {
      const req = create(ExportRulesRequestSchema, { ruleIds: ruleIds ?? [] });
      const resp = await clientRef.current.exportRules(req);
      const blob = new Blob([resp.yamlContent], { type: "text/yaml" });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "rules.yaml";
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      setError(err instanceof Error ? err : new Error("Export failed"));
    } finally {
      setLoading(false);
    }
  }, []);

  return { exportRules, loading, error };
}
```
- Files: `web-app/src/lib/hooks/useExportRules.ts`

#### Story 2.1.3: useBulkUpsertRules hook
**As a** frontend component, **I want** a hook that calls `BulkUpsertRules` and returns apply results, **so that** the ImportRulesModal can show a success/partial-error state.
**Acceptance Criteria**:
- Accepts `rules: ApprovalRuleProto[]` and `overwriteDuplicates: boolean`
- Returns `{ applyRules, loading, result, error }`
- `result` includes created/updated/skipped counts and per-rule errors
**Files**: `web-app/src/lib/hooks/useBulkUpsertRules.ts`

##### Task 2.1.3a: Create useBulkUpsertRules.ts (~3 min)
- Follow the same pattern as `useValidateRules.ts`; call `bulkUpsertRules` on the client
- Files: `web-app/src/lib/hooks/useBulkUpsertRules.ts`

---

## Phase 3: Frontend Components

### Epic 3.1: Import YAML Modal
**Goal**: A modal that lets users paste YAML, see live validation results, and apply valid rules.

#### Story 3.1.1: ImportRulesModal component
**As a** user, **I want** to paste YAML and see per-rule validation results before applying, **so that** I can fix errors without losing my work or applying bad data.
**Acceptance Criteria**:
- Textarea for YAML input; live validation fires ~400ms after last keystroke
- Loading spinner visible in textarea border during validation
- Preview list shows a `ParsedRuleCard` per result (green=valid, red=invalid)
- "will overwrite" badges shown when duplicate names detected (compared to current `rules` prop)
- DuplicateModeRadio: "Skip existing" (default) / "Overwrite existing"
- "Apply N rules" button disabled when `validCount === 0`
- When `validCount === 0` and `errorCount > 0`: show a message "No valid rules to apply. Fix the errors above and try again."
- Button label: "Apply N rules (M have errors)" when errorCount > 0 and validCount > 0
- Apply calls `applyRules`, on success: modal closes, `onApplied()` callback fires (parent refreshes)
- Partial apply error state: modal stays open, shows which rules failed
- Collapsible example YAML snippet below the textarea
- Escape key closes modal (handled by existing `Modal` component)
**Files**:
- `web-app/src/components/sessions/ImportRulesModal.tsx`
- `web-app/src/components/sessions/ImportRulesModal.css.ts`

##### Task 3.1.1a: Create ImportRulesModal.css.ts (~3 min)
- Define styles using vanilla-extract and `vars` from `@/styles/theme.css`:
  - `textarea` style: monospace font, min-height 240px, full width, loading border animation
  - `previewList` style: max-height 360px, overflow-y auto, gap between cards
  - `ruleCard` recipe: variants `valid` (green left border), `error` (red left border), `skip` (grey), `overwrite` (amber)
  - `errorList` style: small font, red color, margin-left
  - `applyButton` style: primary button, full width
  - `exampleToggle` style: small muted text link
  - `exampleBlock` style: monospace pre, small font, muted background
  - `duplicateRadio` style: flex row, gap
- Files: `web-app/src/components/sessions/ImportRulesModal.css.ts`

##### Task 3.1.1b: Create ImportRulesModal.tsx (~5 min)
- Component props: `{ open: boolean; onClose: () => void; onApplied: () => void; existingRules: ApprovalRuleProto[] }`
- Internal state: `yamlContent`, `duplicateMode: "skip" | "overwrite"`, `applyResult`
- Use `useValidateRules(yamlContent)` for live validation
- Use `useBulkUpsertRules()` for apply
- Detect duplicates: build `existingNames = new Set(existingRules.map(r => r.name))`, then for each `ParsedRuleResult` where `valid && existingNames.has(result.originalName)`, mark as duplicate
- Apply button handler:
  - Collect valid `ParsedRuleResult.rule` items
  - Call `applyRules(validRules, duplicateMode === "overwrite")`
  - On success: call `onApplied()` and `onClose()`
  - On partial error: show error state inside modal
- Use existing `Modal`, `ModalContent`, `ModalTitle`, `ModalClose` components
- Files: `web-app/src/components/sessions/ImportRulesModal.tsx`

#### Story 3.1.2: ParsedRuleCard sub-component
**As a** user reviewing an import preview, **I want** each rule shown as a readable card, **so that** I can verify what will be created/updated before applying.
**Acceptance Criteria**:
- Shows rule name, decision badge (color-coded), match fields as chips
- Valid rules: green left border
- Invalid rules: red left border, error messages listed below the card
- Duplicate rules: amber left border + "will overwrite" or grey + "will skip" badge
**Files**:
- `web-app/src/components/sessions/ParsedRuleCard.tsx`
- (styles in `ImportRulesModal.css.ts`)

##### Task 3.1.2a: Create ParsedRuleCard.tsx (~3 min)
- Props: `{ result: ParsedRuleResult; status: "valid" | "error" | "overwrite" | "skip" }`
- Render name, decision as colored badge, match chips (tool/programs/pattern)
- If `status === "error"`: render `result.errors` as a bullet list
- If `status === "overwrite"`: render amber "will overwrite" badge
- If `status === "skip"`: render grey "will skip" badge
- Files: `web-app/src/components/sessions/ParsedRuleCard.tsx`

---

### Epic 3.2: Export Button
**Goal**: A header button that triggers YAML download.

#### Story 3.2.1: ExportButton in ApprovalRulesPanel header
**As a** user, **I want** an "Export YAML" button on the rules page, **so that** I can download my rules for backup or sharing.
**Acceptance Criteria**:
- Button visible in header alongside "Generate Suggestions" and "+ Add Rule"
- Button shows loading state while export RPC is in-flight
- On error: a banner/toast shows the error message
- Mobile: button is hidden on mobile (same treatment as other header buttons, consistent with existing pattern — mobile users can use the FAB)
**Files**:
- `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

##### Task 3.2.1a: Add ExportButton to ApprovalRulesPanel header (~3 min)
- Import `useExportRules` hook
- Add "Export YAML" button to the header button row, positioned between "Generate Suggestions" and "+ Add Rule"
- Apply `headerButtonsHiddenOnMobile` class (consistent with existing buttons)
- On click: call `exportRules()` from hook; display error banner if `error` is set
- Files: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

---

### Epic 3.3: UX Improvements
**Goal**: Fix the four identified friction points in ApprovalRulesPanel.

#### Story 3.3.1: Improve empty state
**As a** new user landing on an empty rules page, **I want** to understand what rules do and how to create them, **so that** I'm not confused by a bare "No rules found" message.
**Acceptance Criteria**:
- Empty state explains purpose: "Approval rules let you automatically allow or deny tool calls from Claude without manual review."
- Empty state for `sourceFilter === "all"` or `sourceFilter === "user"`: shows "+ Add Rule" and "Import YAML" as calls to action
- Empty state for `sourceFilter === "seed"` or `sourceFilter === "claude-settings"`: shows explanatory text without misleading action buttons
**Files**: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

##### Task 3.3.1a: Rewrite empty state in ApprovalRulesPanel.tsx (~2 min)
- Find the existing empty state block
- Replace with a structured empty state that covers all four `sourceFilter` cases
- Files: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

#### Story 3.3.2: Reorder match conditions fields
**As a** user creating a rule, **I want** the most common fields (Tool Name, Programs, Subcommands) grouped at the top, **so that** I'm not tempted to use regex when structured fields would work better.
**Acceptance Criteria**:
- Field order: Tool Name → Programs → Subcommands → [visual separator] → Command Pattern → Tool Pattern → File Pattern
- Visual separator is a horizontal rule or a section label "Advanced: regex patterns"
- A hint below the separator reads: "Regex patterns are powerful but hard to maintain. Use the fields above when possible."
**Files**: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

##### Task 3.3.2a: Reorder form fields and add section separator (~3 min)
- Locate the "Match conditions" section in the modal form
- Reorder the six fields to match the specified order
- Add a `<div>` or `<hr>` with a label "Advanced: regex patterns" between the structured and regex groups
- Add hint text below the separator
- Files: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

#### Story 3.3.3: Add "Import YAML" button trigger
**As a** user, **I want** an "Import YAML" button in the rules page header, **so that** I can discover the bulk import feature without guessing it exists.
**Acceptance Criteria**:
- "Import YAML" button is in the header button row
- Clicking it opens `ImportRulesModal`
- Modal's `onApplied` callback triggers `refresh()` on the rules list
**Files**: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

##### Task 3.3.3a: Wire ImportRulesModal into ApprovalRulesPanel (~3 min)
- Add `importModalOpen` boolean state
- Add "Import YAML" button that sets `importModalOpen = true`
- Render `<ImportRulesModal open={importModalOpen} onClose={() => setImportModalOpen(false)} onApplied={refresh} existingRules={rules} />`
- Files: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

#### Story 3.3.4: Source badge tooltip and mobile FAB label
**As a** user viewing the rules table, **I want** to understand what "Built-in" and "Claude Settings" mean, **so that** I'm not confused about where my rules came from.
**Acceptance Criteria**:
- "Built-in" badge: tooltip "These rules ship with stapler-squad and cannot be deleted"
- "Claude Settings" badge: tooltip "These rules come from your ~/.claude/settings.json file"
- Mobile FAB label updated from "Add Rule" to "Add / Import"
**Files**:
- `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

##### Task 3.3.4a: Add tooltips to source badges and update FAB label (~2 min)
- Find source badge rendering; add `title` attributes with the specified tooltip text
- Find the mobile FAB; update its label
- Files: `web-app/src/components/sessions/ApprovalRulesPanel.tsx`

---

## Phase 4: Frontend Tests and Registry

### Epic 4.1: Frontend Unit Tests
**Goal**: Test coverage for new hooks and ImportRulesModal.

#### Story 4.1.1: Hook tests
**Acceptance Criteria**:
- `useValidateRules`: mock client returns 2 valid + 1 error result; component renders counts correctly
- `useExportRules`: mock client returns YAML; Blob + anchor click verified
- `useBulkUpsertRules`: mock client returns created=3, skipped=1; state reflects correctly
**Files**:
- `web-app/src/lib/hooks/useValidateRules.test.ts`
- `web-app/src/lib/hooks/useExportRules.test.ts`
- `web-app/src/lib/hooks/useBulkUpsertRules.test.ts`

##### Task 4.1.1a: Write hook unit tests (~4 min each)
- Use Jest + MSW or mock `getConnectTransport` to inject a mock client
- Files: per-test-file above

#### Story 4.1.2: ImportRulesModal tests
**Acceptance Criteria**:
- "Apply N rules" button disabled when `validCount === 0`
- Clicking Apply calls `applyRules` with correct arguments
- Duplicate detection: rule with name matching existing rule shown with overwrite badge
- Partial apply error: error message visible in modal after apply
**Files**: `web-app/src/components/sessions/ImportRulesModal.test.tsx`

##### Task 4.1.2a: Write ImportRulesModal unit tests (~5 min)
- Files: `web-app/src/components/sessions/ImportRulesModal.test.tsx`

#### Story 4.1.3: E2E Playwright test (required by CLAUDE.md for all user-facing features)
**As a** QA engineer, **I want** an E2E test that exercises the full import flow end-to-end, **so that** integration regressions are caught in CI.
**Acceptance Criteria**:
- Navigate to `/rules` page
- Click "Import YAML" button → modal opens
- Paste a 3-rule valid YAML → wait for preview cards to appear
- Assert 3 "valid" cards shown (by test-id)
- Click "Apply 3 rules" → modal closes
- Assert rules table has 3 new rows (by name)
- Run against `http://localhost:8544` (test server)
**Files**: `tests/e2e/rules-yaml-import.spec.ts`

##### Task 4.1.3a: Write E2E Playwright test (~5 min)
- Create `tests/e2e/rules-yaml-import.spec.ts`
- Add `// @feature rules:yaml-import` annotation at the top
- Use `data-testid` locators; no CSS class selectors
- Files: `tests/e2e/rules-yaml-import.spec.ts`

---

### Epic 4.2: Feature Registry
**Goal**: Update backend and frontend feature registries to track the new RPCs and UI.

#### Story 4.2.1: Update feature registry
**Acceptance Criteria**:
- `docs/registry/features/rules-yaml-import.json` created with entries for all three new RPCs
- Frontend entry for `ImportRulesModal` and Export button
- `make registry-generate` runs without error
**Files**:
- `docs/registry/features/rules-yaml-import.json`

##### Task 4.2.1a: Create registry file and run registry-generate (~2 min)
- Create `docs/registry/features/rules-yaml-import.json` with entries for `rules:validate`, `rules:export`, `rules:bulk-upsert`
- Run `make registry-generate`
- Files: `docs/registry/features/rules-yaml-import.json`

---

## Summary

| Phase | Epics | Stories | Tasks |
|---|---|---|---|
| 1: Proto + Backend | 3 | 6 | 10 |
| 2: Frontend Hooks | 1 | 3 | 3 |
| 3: Frontend Components | 3 | 6 | 7 |
| 4: Tests + Registry | 2 | 4 | 5 |
| 3: Frontend Components | 3 | 6 | 7 |
| 4: Tests + Registry | 2 | 4 | 5 |
| **Total** | **9** | **19** | **25** |

## Implementation Order

For a solo developer, the recommended linear order is:
1. Task 1.1.1a → 1.1.1b (proto + generate first — unblocks everything)
2. Tasks 1.2.1a → 1.2.1b → 1.2.1c (ValidateRules backend)
3. Tasks 1.2.2a → 1.2.2b (ExportRules backend)
4. Tasks 1.2.3a → 1.2.3b → 1.2.3c (BulkUpsertRules backend)
5. Tasks 1.3.1a → 1.3.2a → 1.3.3a (backend tests)
6. Tasks 2.1.1a → 2.1.2a → 2.1.3a (frontend hooks)
7. Tasks 3.1.1a → 3.1.1b → 3.1.2a (ImportRulesModal)
8. Task 3.2.1a (ExportButton)
9. Tasks 3.3.1a → 3.3.2a → 3.3.3a → 3.3.4a (UX fixes)
10. Tasks 4.1.1a → 4.1.2a → 4.1.3a → 4.2.1a (tests + E2E + registry)

Run `make quick-check` after each phase. Run `make install-service` to manually test before opening a PR.
