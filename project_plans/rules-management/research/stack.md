# Stack Research — Rules Management UX

## Go Backend

### YAML Parsing: `gopkg.in/yaml.v3`
Already in `go.mod` as a direct dependency (`gopkg.in/yaml.v3 v3.0.1`). No new dependency needed. Key characteristics for this feature:

- `yaml.v3` uses struct tags (`yaml:"field_name"`) for mapping YAML keys to Go fields
- Supports `yaml:",inline"` for embedding structs
- Decodes into `interface{}` or strongly-typed structs; use typed structs for validation
- Does NOT have a built-in size or recursion limit — this is critical for YAML bomb protection (see pitfalls)
- The `yaml.Decoder` (stream-based) is preferred over `yaml.Unmarshal` for large payloads; set `KnownFields(true)` on the decoder to reject unknown fields

**Recommended decode pattern:**
```go
type YAMLRulesFile struct {
    Rules []YAMLRuleEntry `yaml:"rules"`
}

type YAMLRuleEntry struct {
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
    Enabled        *bool    `yaml:"enabled"` // pointer to distinguish "absent" from false
}
```

### ConnectRPC Pattern
Existing pattern in `server/services/rules_service.go`: methods are Go functions receiving `context.Context` + `*connect.Request[T]` → `(*connect.Response[R], error)`. `RulesService` methods are delegated to by `SessionService`, which implements `sessionv1connect.SessionServiceHandler`. New `ValidateRules` RPC must:
1. Be defined in `proto/session/v1/session.proto`
2. Run `make generate-proto` to regenerate bindings in `session/gen/session/v1/`
3. Be implemented as a method on `RulesService` (following `ListApprovalRules`/`UpsertApprovalRule` patterns)
4. Be proxied through `SessionService` (which holds a `*RulesService` field)

### Regex Validation: `regexp` stdlib
Already used extensively in `rules_store.go` (pattern: `regexp.Compile(pat)`). The same validation pattern can be reused verbatim in `ValidateRules`.

### Size protection: `io.LimitReader` or explicit byte limit
The HTTP handler can reject requests exceeding 500 KB before YAML parsing begins. ConnectRPC reads the full request body into memory before calling the handler; apply a `strings.NewReader` + length check at the service layer.

## TypeScript / React Frontend

### ConnectRPC TypeScript Client
Pattern established in `useApprovalRules.ts`: use `createClient(SessionService, getConnectTransport())`. The generated TypeScript client for `ValidateRules` will be auto-available after `make generate-proto`. Import pattern:
```ts
import { ValidateRulesRequest, ValidateRulesRequestSchema } from "@/gen/session/v1/session_pb";
import { create } from "@bufbuild/protobuf";
```

### YAML-to-Preview: No Client-Side Parse Needed
The requirements send raw YAML text to `ValidateRules` RPC and show server-returned `ParsedRuleResult[]`. No client-side YAML parsing library is needed. This keeps the bundle clean and validation authoritative on the server.

### File Download (Export)
Standard browser pattern: create a `Blob` from the YAML string, call `URL.createObjectURL`, click a programmatic `<a download="rules.yaml">`. No library needed. The YAML serialization must happen server-side to guarantee lossless roundtrip.

**Export YAML generation in Go:**
```go
import "gopkg.in/yaml.v3"
data, err := yaml.Marshal(yamlRulesFile)
```
Return as `text/yaml` content-type, or embed in a proto `bytes` field for a ConnectRPC response.

### CSS: vanilla-extract (`.css.ts`)
All new styles go in `ApprovalRulesPanel.css.ts` (extend existing file) or a new colocated `ImportRulesModal.css.ts`. Use `vars` from `@/styles/theme.css` — no hardcoded colors or sizes. The import modal needs: textarea container, validation card, error inline, apply button, duplicate-mode radio group.

### Modal: Existing `Modal` Component
The existing `Modal`/`ModalContent`/`ModalTitle`/`ModalClose` components (used by `ApprovalRulesPanel` already) handle portal, overlay, focus trap, and Escape key. Use these for the Import YAML modal — no new modal infrastructure.

### State Management: Local useState
Both import flow (parsing state, validation results, duplicate mode, apply progress) and export flow (download progress) are local to `ApprovalRulesPanel`. No global state or context needed.

## Build & Generation
- `make generate-proto` regenerates both Go and TypeScript bindings from proto changes
- `make install-service` deploys the full stack (Go binary + web UI)
- `make quick-check` validates build + lint + tests before PR

## Dependency Summary

| Need | Solution | Already in repo? |
|---|---|---|
| YAML parse (Go) | `gopkg.in/yaml.v3` | Yes |
| ConnectRPC server | `connectrpc.com/connect` | Yes |
| ConnectRPC client (TS) | `@connectrpc/connect` | Yes |
| YAML parse (TS) | Not needed | N/A |
| File download | Browser API Blob | Yes |
| Modal UI | Existing Modal component | Yes |
| CSS | vanilla-extract | Yes |
| Regex validation | `regexp` stdlib | Yes |
| Proto codegen | `make generate-proto` | Yes |
