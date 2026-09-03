# Policy Compatibility Frontends Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add bounded CEL, Rego, Cedar, and Protobuf compatibility frontends that compile documented subsets into the existing NornRune AST and immutable Program without changing native-policy behavior or forking evaluation.

**Architecture:** Public `frontend` types hold one bounded struct-of-arrays semantic policy with CSR child and list edges, exact byte spans, stable diagnostics, field bindings, and capability metadata. CEL, Rego, and Cedar packages use their official parsers, translate only documented constructs into that table, and reject everything else; `internal/frontend` validates and lowers the table through `ast.Builder` and `compile.Lowerer`. A single Boolean-literal node/opcode extends the shared core so source defaults and static authorization policies remain exact, while all parser objects and reflection stay outside evaluator packages.

**Tech Stack:** Go 1.27, `cel.dev/cel-go` v0.32.0, OPA/Rego v1.19.1, `github.com/cedar-policy/cedar-go` v1.8.0, protobuf v1.36.12, Buf v1.72.0, Cobra, existing SoA/CSR AST and evaluator.

---

## Fixed Boundaries

- Keep native JSON compilation and default CLI output byte-identical.
- Add only explicit `native`, `cel`, `rego`, and `cedar` format selection. Do not add automatic detection.
- Keep upstream parser/checker objects in `frontend/cel`, `frontend/rego`, and `frontend/cedar`; evaluator, scheduler, program, result, truth, and index packages must not import frontend or upstream language packages.
- Check source bytes before calling an upstream parser. Check translated node, edge, field, literal, string, diagnostic, and depth limits again while building the semantic table.
- Reject unsupported syntax at its smallest available source token or expression span. Never approximate unsupported semantics.
- Permit only one field reference and one scalar literal in comparisons. Reject field-to-field comparisons even when an upstream checker accepts them.
- Keep frontend compilation cold-path. Warm scalar, SIMD, indexed, and scheduler evaluation must retain zero allocations.
- Preserve source-language missing/default behavior through the clause resolution table: CEL and Cedar missing data escalate; Rego without a default escalates; explicit Rego Boolean defaults resolve undefined evaluation to that default.
- Preserve source order in semantic columns and diagnostics. Sort diagnostics by start, end, code, then row before returning.
- Do not add frontend formats to the persisted policy registry in this task. Registry migration requires durable source-format metadata and belongs in a separate design.

### Task 1: Define The Public Frontend Contract

**Files:**
- Create: `frontend/frontend.go`
- Create: `frontend/diagnostic.go`
- Create: `frontend/builder.go`
- Create: `frontend/frontend_test.go`
- Create: `frontend/builder_test.go`
- Create: `frontend/fuzz_test.go`

**Step 1: Write failing public-contract tests**

Lock these behaviors:

- `Language`, `ValueKind`, `FieldGroup`, `NodeKind`, `CompareOp`, `DefaultDecision`, `Support`, and `DiagnosticCode` use stable nonzero values and reject unknown values.
- `DefaultLimits()` is nonzero and bounded by `MaxSourceBytes=4 MiB`, `MaxNodes=65536`, `MaxDepth=128`, `MaxFields=4096`, `MaxLiterals=131072`, `MaxChildren=65536`, `MaxStringBytes=1 MiB`, and `MaxDiagnostics=128`.
- `BindingSet.Validate` rejects empty name/version, duplicate source or target names, invalid kinds/groups, malformed dotted names, and dimensions over limits without mutating the binding set.
- A builder copies caller source/bindings, appends Boolean, presence, compare, `in`, all, any, and not nodes into equal-length SoA columns, and stores child/list relationships in CSR arrays.
- Every successful `Finish` owns exact-capacity columns and a valid nonzero root. Every failed append and finish leaves prior columns unchanged and returns no partial policy.
- Unicode spans are half-open byte ranges, not rune offsets.
- Diagnostics contain no source excerpt or parser-owned object and sort deterministically.
- `Policy` slice element types and `Diagnostic` are pointerless; field order passes the pinned analyzer.

Use this public shape as the contract:

```go
type Binding struct {
    Source string     `json:"source"`
    Target string     `json:"target"`
    Kind   ValueKind  `json:"kind"`
    Group  FieldGroup `json:"group"`
}

type BindingSet struct {
    Name     string    `json:"name"`
    Version  string    `json:"version"`
    Decision string    `json:"decision,omitempty"`
    Fields   []Binding `json:"fields"`
}

type Policy struct {
    Source []byte
    Name   []byte
    Version []byte

    NodeKinds        []NodeKind
    NodeOps          []CompareOp
    NodeFields       []FieldID
    NodeLiterals     []LiteralID
    NodeChildStarts  []uint32
    NodeChildCounts  []uint16
    NodeListStarts   []uint32
    NodeListCounts   []uint16
    NodeSourceStarts []uint32
    NodeSourceEnds   []uint32
    ChildNodeIDs     []NodeID
    ListLiteralIDs   []LiteralID

    FieldNameStarts   []uint32
    FieldNameLengths  []uint32
    FieldTargetStarts []uint32
    FieldTargetLengths []uint32
    FieldKinds        []ValueKind
    FieldGroups       []FieldGroup
    FieldBytes        []byte

    LiteralKinds    []ValueKind
    LiteralRefs     []uint32
    SymbolStarts    []uint32
    SymbolLengths   []uint32
    SymbolBytes     []byte
    IntegerValues   []int64
    BooleanValues   []uint8

    Root    NodeID
    Default DefaultDecision
}
```

Keep all IDs one-based. `NodeKindBoolean` references one Boolean literal. Append `NodeKindDefined` without renumbering existing kinds; it references one field and has no literal or edges. `NodeKindCompare` references one field and scalar literal, except `CompareOpIn`, whose literal is zero and whose list range is nonempty. Groups contain at least two children; parsers may return a child directly instead of creating a one-child group.

**Step 2: Run RED**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./frontend
```

Expected: FAIL because the public package does not exist.

**Step 3: Implement enums, limits, bindings, diagnostics, and builder**

Use append-only enum tables with `Valid`, `String`, and JSON text parsing methods. Keep lookup linear over the bounded field declarations; this is compile-time work and avoids maps in the semantic representation. Pre-size every builder column from validated limits and binding counts, roll back all related columns on append failure, and clone exact capacities in `Finish`.

Expose fixed capability records:

```go
type Capability struct {
    Name    string  `json:"name"`
    Support Support `json:"support"`
}
```

Language packages return source-ordered static capability slices. The public package owns only the types, not language-specific tables.

**Step 4: Run GREEN, fuzz seeds, and field alignment**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./frontend
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzBuilder$' -fuzztime=5s ./frontend
timeout 300s ./scripts/check-fieldalignment.sh
```

Expected: PASS. Fuzz seeds return normally or bounded diagnostics and never panic.

### Task 2: Add Exact Boolean Constants To The Shared Core

**Files:**
- Modify: `internal/ast/kind.go`
- Modify: `internal/ast/document.go`
- Modify: `internal/ast/builder.go`
- Modify: `internal/ast/value.go`
- Modify: `internal/ast/builder_test.go`
- Modify: `internal/compile/validate.go`
- Modify: `internal/compile/validate_corrupt_columns_test.go`
- Modify: `internal/compile/normalize_instructions.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_api_test.go`
- Modify: `internal/program/instruction.go`
- Modify: `internal/program/program_test.go`
- Modify: `internal/eval/executor.go`
- Modify: `internal/eval/executor_test.go`
- Modify: `internal/eval/executor_bench_test.go`
- Modify: `internal/truth/planes.go`
- Modify: `internal/truth/planes_test.go`
- Modify: `internal/adapters/cli/graph_data.go`
- Modify: `internal/adapters/cli/graph_data_test.go`
- Modify: `internal/adapters/cli/tui.go`

**Step 1: Write failing Boolean-node tests**

Add tests proving:

- `ast.Builder.AddBoolean(true|false, span)` appends a Boolean value and `NodeKindBoolean` atomically.
- structural validation rejects a Boolean node with a missing, non-Boolean, or out-of-range value.
- lowering emits appended `program.OpcodeBoolean` values without renumbering any existing opcode.
- true and false constants evaluate correctly for row counts `0, 1, 63, 64, 65, 1024`, clear tail bits, clear all reason planes, and allocate zero times after binding.
- constants participate in CSE, scheduling, liveness, graph snapshots, and result resolution.
- native JSON policy compilation and the five canonical results remain byte-identical.

**Step 2: Run RED**

Run:

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/ast ./internal/program ./internal/truth ./internal/compile ./internal/eval ./internal/adapters/cli
```

Expected: FAIL because Boolean nodes and opcodes do not exist.

**Step 3: Implement the minimal core extension**

Append `NodeKindBoolean` and `OpcodeBoolean` so every shipped numeric value remains unchanged. Reuse existing value columns: a Boolean AST node and instruction store a Boolean `ValueID` in `NodeRefs`/`Program.Values`; do not add another Program payload column.

Add:

```go
func Set(dst Planes, value bool, rows uint32)
```

`truth.Set` writes all words in one pass, selects the positive plane for true or negative plane for false, clears the other plane, and masks the final word. `Executor.executeInstructionMode` calls it and clears the destination reason words for `OpcodeBoolean`.

Update validation, CSE hashing, schedule opcode arrays, graph labels, and TUI labels. Use `[program.OpcodeBoolean + 1]uint32` rather than another hand-maintained schedule array length. Constants have no operands, field, list, evidence payload, or runtime input access.

**Step 4: Run GREEN and allocation benchmarks**

Run:

```bash
timeout 150s go test -count=1 -timeout 120s ./internal/ast ./internal/program ./internal/truth ./internal/compile ./internal/eval ./internal/adapters/cli ./internal/conformance
timeout 120s go test -timeout 90s -run '^$' -bench '^BenchmarkExecutorBoolean$' -benchmem -count=6 ./internal/eval
```

Expected: PASS. Warm Boolean execution reports `0 B/op` and `0 allocs/op`.

### Task 3: Validate And Lower The Shared Semantic Policy

**Files:**
- Create: `internal/frontend/semantic.go`
- Create: `internal/frontend/semantic_test.go`
- Create: `internal/frontend/lower.go`
- Create: `internal/frontend/lower_test.go`
- Create: `internal/frontend/fuzz_test.go`
- Create: `internal/frontend/testdata_test.go`

**Step 1: Write failing malformed-table and lowering tests**

Cover:

- nil policy, unequal columns, bad source spans, invalid IDs/ranges, duplicate source/target fields, invalid UTF-8 names, mixed `in` literals, incompatible compare types, illegal ordered Boolean comparisons, cycles/forward references, unreachable nodes, and depth/size overflows;
- deterministic diagnostic ordering and `MaxDiagnostics` truncation;
- all failures return no Program and leave a caller-owned destination unchanged;
- field bindings become a `schema.Schema` in declaration order using target names and declared groups;
- source literals and metadata are copied into an `ast.Document` with exact spans;
- one requirement and one clause are emitted with `Approve`, `Reject`, `Revise`, and `Escalate` catalogs;
- the applicability root is `expr OR NOT expr`;
- true resolves to Approve, false to Reject, and unresolved resolves according to `Policy.Default`;
- generic decision and unresolved explanations satisfy the existing explanation validator without retaining source excerpts;
- compiled Programs own exact-capacity storage and remain valid after the semantic policy/compiler is reused.

**Step 2: Run RED**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/frontend
```

Expected: FAIL because shared validation/lowering does not exist.

**Step 3: Implement one reusable compiler**

Use this API:

```go
type Compiler struct {
    // reusable builders, remaps, diagnostics, and compile.Lowerer
}

func Compile(policy *frontend.Policy) (*program.Program, []frontend.Diagnostic, error)
func (compiler *Compiler) Compile(dst *program.Program, policy *frontend.Policy) ([]frontend.Diagnostic, error)
```

The validator scans each column once, uses caller/compiler-owned typed slices for state and depth, and emits diagnostics without maps. The lowerer performs these steps only after zero semantic diagnostics:

1. Intern target field names and build the schema.
2. Set retained source bytes and copy scalar literals.
3. Translate source-ordered semantic nodes to `ast.Builder` node IDs.
4. Add fixed metadata, outcome names, empty assumptions, and generic explanation templates.
5. Build the clause resolution table and applicability tautology.
6. Call the existing `compile.Lowerer` into scratch and publish atomically.

Use requirement ID `1` and clause ID `1`. Outcome precedence remains `Approve=1`, `Revise=2`, `Escalate=3`, `Reject=4`; all four names are present even though compatibility policies do not produce Revise.

**Step 4: Run GREEN and fuzz seeds**

Run:

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend ./internal/frontend ./internal/compile ./internal/eval
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzSemanticPolicy$' -fuzztime=5s ./internal/frontend
```

Expected: PASS with no partial Program on any malformed seed.

### Task 4: Implement The CEL Frontend First

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `frontend/cel/parser.go`
- Create: `frontend/cel/lower.go`
- Create: `frontend/cel/capabilities.go`
- Create: `frontend/cel/cel_test.go`
- Create: `frontend/cel/fuzz_test.go`
- Create: `testdata/frontends/cel/scalars.cel`
- Create: `testdata/frontends/cel/selection.cel`
- Create: `testdata/frontends/cel/unsupported.cel`

**Step 1: Pin CEL and write failing parser/lowering tests**

Pin `cel.dev/cel-go v0.32.0`. Tests must cover:

- declared top-level `bool`, `int`, and `string` variables;
- explicitly bound object selection such as `request.team`;
- `==`, `!=`, `<`, `<=`, `>`, `>=`, `&&`, `||`, `!`, and `in` over a nonempty homogeneous constant list;
- reversed literal/field comparisons with the ordered operator reversed correctly;
- Boolean field shorthand lowered as `field == true` and Boolean literals lowered as constants;
- missing activation values mapped to NornRune unknown/Escalate;
- syntax/type errors and unsupported calls, member dispatch, maps, messages, comprehensions, macros, optional syntax, dynamic field-to-field comparisons, unsupported scalar types, and mixed lists;
- exact Unicode byte spans and deterministic capabilities/diagnostics;
- source, node, depth, field, literal, child, and diagnostic limits;
- differential true/false/missing cases against the official CEL evaluator.

Use a package-local parsed wrapper so benchmarks can separate parse/check from semantic translation without exposing parser ownership to the evaluator:

```go
type Parsed struct { /* checked official AST and source metadata */ }

func Parse(source []byte, bindings frontend.BindingSet, limits frontend.Limits) (*Parsed, []frontend.Diagnostic)
func Lower(source []byte, parsed *Parsed, bindings frontend.BindingSet, limits frontend.Limits) (*frontend.Policy, []frontend.Diagnostic)
func Compile(source []byte, bindings frontend.BindingSet, limits frontend.Limits) (*frontend.Policy, []frontend.Diagnostic)
func Capabilities() []frontend.Capability
```

**Step 2: Run RED**

Run:

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/cel
```

Expected: FAIL because the CEL package does not exist.

**Step 3: Parse and check with bounded official APIs**

Build a CEL environment with `ClearMacros`, `HomogeneousAggregateLiterals`, `ParserExpressionSizeLimit`, `ParserRecursionLimit`, `ExpressionNodeLimit`, and `ExpressionNestingDepthLimit`. Declare scalar variables with exact CEL types. Declare only explicitly bound selection roots as dynamic maps, then require every selected path to match one binding and verify its literal type from that binding.

Call parse and check separately so syntax and type diagnostics retain distinct stable codes. Convert CEL code-point offsets to UTF-8 byte offsets before storing spans. Traverse the checked native AST iteratively/postorder and accept only operator names from `common/operators` for the capability matrix. Build no map per node.

**Step 4: Implement differential tests and fuzzing**

Evaluate the same typed rows through CEL and the lowered NornRune Program. Map a CEL unknown/error caused solely by an omitted declared value to Escalate. Any other official evaluator error fails the corpus test rather than being treated as compatible.

Run:

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/cel ./internal/frontend
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzCompile$' -fuzztime=5s ./frontend/cel
```

Expected: PASS.

### Task 5: Implement The Rego Frontend Second

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `frontend/frontend.go`
- Modify: `frontend/builder.go`
- Modify: `frontend/frontend_test.go`
- Modify: `internal/ast/kind.go`
- Modify: `internal/ast/builder.go`
- Modify: `internal/ast/builder_test.go`
- Modify: `internal/program/instruction.go`
- Modify: `internal/program/program_test.go`
- Modify: `internal/compile/normalize.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/schedule.go`
- Modify: `internal/eval/predicate.go`
- Modify: `internal/eval/predicate_test.go`
- Modify: `internal/eval/executor_test.go`
- Modify: `internal/frontend/semantic.go`
- Modify: `internal/frontend/semantic_test.go`
- Modify: `internal/frontend/lower.go`
- Modify: `internal/frontend/lower_test.go`
- Create: `frontend/rego/parser.go`
- Create: `frontend/rego/lower.go`
- Create: `frontend/rego/capabilities.go`
- Create: `frontend/rego/rego_test.go`
- Create: `frontend/rego/fuzz_test.go`
- Create: `testdata/frontends/rego/allow.rego`
- Create: `testdata/frontends/rego/default.rego`
- Create: `testdata/frontends/rego/unsupported.rego`

**Step 1: Pin OPA and write failing subset tests**

Pin `github.com/open-policy-agent/opa v1.19.1`. Test:

- one Rego v1 package and one configured Boolean decision name;
- complete `allow if { ... }` rules whose bodies are conjunctions;
- multiple decision rules lowered as OR;
- `input.<path>` Boolean fields, scalar field/literal comparisons, constant homogeneous array/set membership, and presence-aware `not` semantics for true, false, and missing input;
- optional `default allow := false` and `default allow := true` semantics;
- undefined input without a default maps to Escalate;
- imports, `data`, functions, `else`, recursion, comprehensions, variables, binding/unification, partial documents, object/set results, with-modifiers, mutation-like/other built-ins, field-to-field comparisons, and non-Boolean heads are rejected;
- duplicate/default conflicts, exact OPA byte spans, malformed/Unicode/depth/size limits, and deterministic diagnostics;
- differential true/false/undefined cases against OPA's official evaluator.

Expose the same `Parsed`, `Parse`, `Lower`, `Compile`, and `Capabilities` shape as CEL.

**Step 2: Run RED**

Run:

```bash
timeout 240s go test -count=1 -timeout 210s ./frontend/rego
```

Expected: FAIL because the Rego package does not exist.

**Step 3: Parse Rego v1 and lower only complete decisions**

Use `ast.ParseModuleWithOpts` with `ParserOptions{RegoVersion: ast.RegoV1}`. Reject imports before translation. Select rules by exact `Head.Name`, require non-default rules to produce Boolean true, lower each body as all, and lower rules as any. Read comparisons through official `Expr.Operator`/`Operands`; allow only `equal`, `neq`, `lt`, `lte`, `gt`, `gte`, and `internal.member_2` with one bound input path and constants.

Use OPA `Location.Offset` and `Location.Text` directly for byte spans. An explicit true default folds the root to a true Boolean node. False/no defaults retain rule roots and set `Policy.Default` to Reject/Escalate respectively.

OPA v1.19.1 makes a missing bare reference or equality succeed under `not`, but
missing `!=`, ordered comparisons, and membership remain undefined. Existing
`Exists` deliberately maps absence to unknown and must not be changed. Append a
shared semantic `NodeKindDefined`, core
`CompareOpDefined`, and `OpcodeDefined`; the new leaf returns true for present,
false for absent, and clears reasons. Lower negated bare references and equality
as `NOT DEFINED(field) OR NOT E(field)`; use ordinary `Not` for the other
supported operators and constants. Reject negated expressions that are not one
supported scalar atom. Add
public-builder, core, and shared-validator/lowerer RED/GREEN tests before the
Rego implementation.

**Step 4: Run GREEN and fuzz seeds**

Run:

```bash
timeout 240s go test -count=1 -timeout 210s ./frontend/rego ./internal/frontend
timeout 60s go test -count=1 -timeout 50s -run '^$' -fuzz '^FuzzCompile$' -fuzztime=5s ./frontend/rego
```

Expected: PASS.

### Task 6: Implement The Cedar Frontend Third

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `frontend/cedar/parser.go`
- Create: `frontend/cedar/lower.go`
- Create: `frontend/cedar/span.go`
- Create: `frontend/cedar/capabilities.go`
- Create: `frontend/cedar/cedar_test.go`
- Create: `frontend/cedar/fuzz_test.go`
- Create: `testdata/frontends/cedar/permit.cedar`
- Create: `testdata/frontends/cedar/forbid.cedar`
- Create: `testdata/frontends/cedar/unsupported.cedar`

**Step 1: Pin Cedar and write failing authorization tests**

Pin `github.com/cedar-policy/cedar-go v1.8.0`. Cover:

- static permit and forbid policy lists;
- principal, action, and resource equality scopes using declared symbol bindings;
- Boolean `when` and `unless` conditions over declared `context.<path>` fields;
- scalar context comparisons and Boolean composition;
- multiple permits ORed, multiple forbids ORed, and final `anyPermit AND NOT anyForbid`;
- no permit rejects, a matching forbid wins, and missing context escalates;
- hierarchy `in`, `is`, entity attributes, sets, records, extension calls, templates, annotations, arithmetic, like/has/tag operations, and undeclared paths are rejected;
- malformed source, Unicode, source/node/depth limits, exact policy/token spans, and deterministic diagnostics;
- differential allow/deny/error cases against `cedar.Authorize` with an empty entity map.

Expose the same public parser/lowerer shape as the other language packages.

**Step 2: Run RED**

Run:

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/cedar
```

Expected: FAIL because the Cedar package does not exist.

**Step 3: Lower the official Cedar AST**

Parse with `cedar.NewPolicyListFromBytes`, inspect each public `Policy.AST`, and type-switch only the documented `x/exp/ast` scope and expression nodes. Canonicalize entity UID literals with `EntityUID.String`; request rows use those exact strings.

Because cedar-go exposes policy positions but not child positions, add a bounded UTF-8 lexer in `span.go` that records token byte spans without interpreting policy semantics. Official AST nodes remain the semantic authority; the token table is used only to attach the smallest matching source span and to locate parser-error line/column positions. Reject any AST/token mismatch instead of emitting an approximate span.

**Step 4: Run GREEN and fuzz seeds**

Run:

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/cedar ./internal/frontend
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzCompile$' -fuzztime=5s ./frontend/cedar
```

Expected: PASS.

### Task 7: Add Protobuf Options And A Deterministic Plugin

**Files:**
- Create: `frontend/proto/options.proto`
- Create: `frontend/proto/options.pb.go` (generated)
- Create: `frontend/proto/plugin.go`
- Create: `frontend/proto/plugin_test.go`
- Create: `cmd/protoc-gen-nornrune/main.go`
- Create: `buf.frontend.gen.yaml`
- Modify: `buf.yaml`
- Modify: `buf.gen.yaml`
- Modify: `cmd/devx/cmd/build.go`
- Modify: `cmd/devx/cmd/status.go`
- Modify: `cmd/devx/cmd/root_test.go`
- Modify: `internal/adapters/grpcapi/generated_test.go`
- Create: `testdata/frontends/proto/policy.proto`
- Create: `testdata/frontends/proto/policy_nornrune.pb.go`
- Create: `testdata/frontends/proto/want_binding.go`

**Step 1: Write failing option/plugin tests**

Define message options for policy name, version, and CEL expression plus one field option for canonical target name. Tests construct `pluginpb.CodeGeneratorRequest` values and require:

- deterministic static `frontend.BindingSet` output for string, bool, and signed integer fields;
- source field names use protobuf JSON names and target names use the explicit canonical field option;
- group inference from the target prefix is deterministic;
- repeated, map, oneof, message, enum, float/double, unsigned/fixed, bytes, optional-presence ambiguity, missing options, duplicate targets, and unsupported editions/features return plugin errors;
- generated code contains no descriptor walk, reflection call, map, or init-time parser;
- no partial response files on any error;
- generated fixture code compiles and remains byte-identical after regeneration.

**Step 2: Run RED**

Run:

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/proto ./cmd/protoc-gen-nornrune
```

Expected: FAIL because options and plugin packages do not exist.

**Step 3: Generate options and implement the plugin**

Use non-conflicting custom option numbers above 50000 and a `go_package` of `github.com/sebishogun/nornrune/frontend/proto;frontproto`. Keep option reflection inside `frontend/proto/plugin.go`; generated bindings contain only static literals and `frontend` value enums.

Expose:

```go
func Generate(request *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorResponse, error)
func GeneratePlugin(plugin *protogen.Plugin) error
```

`cmd/protoc-gen-nornrune` contains only `protogen.Options{}.Run(frontproto.GeneratePlugin)` and process error handling.

Add a separate pinned Buf template for the frontend option output and teach `devx proto:gen`/`proto:check` to run both templates. Extend the containerized drift test inputs and expected generated files rather than adding an unpinned local protoc path.

**Step 4: Run GREEN and generated drift**

Run:

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/proto ./cmd/protoc-gen-nornrune ./internal/adapters/grpcapi ./cmd/devx/cmd
timeout 300s env PATH="/tmp/opencode/nornrune-tools:$PATH" go run ./cmd/devx proto:check
timeout 30s git diff --check
```

Expected: PASS and regeneration produces no diff.

### Task 8: Add Explicit CLI Format Selection

**Files:**
- Create: `internal/adapters/cli/frontend.go`
- Create: `internal/adapters/cli/frontend_test.go`
- Modify: `internal/adapters/cli/compile.go`
- Modify: `internal/adapters/cli/validate.go`
- Modify: `internal/adapters/cli/evaluate.go`
- Modify: `internal/adapters/cli/output.go`
- Modify: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing CLI contract tests**

Add `--format` and `--bindings` only to `compile`, `validate`, and `evaluate`. Test:

- omitted `--format` remains native and produces byte-identical help, compile summary, validation output, and five-request results;
- `--format native` rejects `--bindings` and otherwise matches omission;
- CEL/Rego/Cedar require explicit `--policy` and `--bindings` paths; stdin may supply only one input;
- strict bounded binding JSON rejects unknown/duplicate fields, trailing JSON, unsupported enum text, oversized input, and inconsistent declarations as usage errors;
- each explicit format compiles/evaluates one corpus policy through the same scheduler/evaluator and canonical result encoder;
- frontend diagnostics are emitted as bounded JSON containing language, code, span, row, and field, then exit quietly with code 1;
- unknown formats and protobuf runtime selection are usage errors; Protobuf remains a build-time plugin frontend;
- automatic content detection is absent.

**Step 2: Run RED**

Run:

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/adapters/cli
```

Expected: FAIL because frontend flags/dispatch do not exist.

**Step 3: Implement cold-path dispatch**

Decode bindings into `frontend.BindingSet` using a strict `encoding/json.Decoder` over a size-limited byte slice. Dispatch by explicit enum to the language package, then call `internal/frontend.Compiler`. Keep the native `engine.decodePolicy` and `engine.compilePolicy` path unchanged.

For `evaluate`, compile the selected frontend before the existing JSON batch decoder and scheduler. Binding targets therefore remain the only field names accepted in request JSON. Do not add parser checks, maps, or language switches to evaluator methods.

**Step 4: Run GREEN and native byte-identity tests**

Run:

```bash
timeout 240s go test -count=1 -timeout 210s ./internal/adapters/cli ./internal/conformance
timeout 60s go run ./cmd/nornrune compile
timeout 60s go run ./cmd/nornrune evaluate
```

Expected: PASS. Native command output matches checked-in canonical fixtures.

### Task 9: Lock Conformance, Dependency Boundaries, Fuzzing, And Performance

**Files:**
- Create: `internal/frontend/conformance_test.go`
- Create: `internal/frontend/benchmark_test.go`
- Create: `internal/doccheck/frontends_test.go`
- Modify: `docs/performance.md`
- Modify: `docs/development.md`
- Modify: `.github/workflows/ci.yml`

**Step 1: Write failing cross-frontend and boundary tests**

Build equivalent CEL, Rego, and Cedar policies for Boolean, integer, string, conjunction, disjunction, negation, and missing-field cases. Require identical NornRune decisions and stable semantic diagnostics.

Add static import checks proving `internal/eval`, `internal/scheduler`, `internal/program`, `internal/result`, `internal/truth`, and `internal/index` do not import `frontend`, CEL, OPA, Cedar, or protobuf reflection. Check capability docs contain every stable capability name and pinned upstream version.

**Step 2: Run RED**

Run:

```bash
timeout 240s go test -count=1 -timeout 210s ./internal/frontend ./internal/doccheck
```

Expected: FAIL until conformance, docs, and boundary checks exist.

**Step 3: Add stage-separated benchmarks**

Benchmark, with setup outside timed sections:

- official parse/check;
- semantic translation;
- shared validation/lowering;
- cold end-to-end compilation;
- warm scalar, automatic/SIMD, and scheduler evaluation at `1, 64, 256, 4096` rows.

Report `-benchmem`; warm evaluation must remain `0 B/op` and `0 allocs/op`. Do not publish a cross-engine speed claim. Document only measured supported-subset timings with CPU, Go version, upstream versions, policy shape, rows, mode, and setup boundary.

**Step 4: Add bounded CI coverage**

Add a frontend job with an explicit job timeout and bounded conformance/fuzz-seed commands. Keep short fuzz campaigns local/release-manual rather than running unbounded fuzzing in CI. Extend native job timeout only if measured dependency compilation exceeds the current bound.

Run:

```bash
timeout 240s go test -count=1 -timeout 210s ./frontend/... ./internal/frontend ./internal/doccheck
timeout 240s go test -timeout 210s -run '^$' -bench '^BenchmarkFrontend' -benchmem -count=6 ./internal/frontend
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkExecutor|BenchmarkScheduled' -benchmem -count=6 ./internal/eval ./internal/scheduler
timeout 300s ./scripts/check-fieldalignment.sh
```

Expected: PASS. Every warmed evaluator benchmark reports zero allocations.

### Task 10: Document, Audit, Verify, Commit, And Push

**Files:**
- Create: `docs/frontends.md`
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/development.md`
- Modify: `docs/performance.md`
- Modify: `docs/operations.md`
- Modify: `.goreleaser.yaml` only if the plugin is included in release archives

**Step 1: Write documentation contracts first**

Require documentation to state:

- exact supported/restricted/rejected matrices for all four frontends;
- explicit-format CLI examples and binding JSON shape;
- true/false/missing/default mappings and Cedar forbid precedence;
- source/AST/field/literal/depth/diagnostic limits;
- no drop-in/full-language compatibility claim;
- no fixed performance claim;
- official engine versions and differential corpus scope;
- Protobuf generation/install commands and rejected field types;
- parser dependencies are compile-time and evaluator kernels remain shared;
- persisted registry policies remain native JSON in this task.

Run RED:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/doccheck
```

Expected: FAIL until docs are complete.

**Step 2: Complete docs and run focused verification**

Run:

```bash
timeout 300s go test -count=1 -timeout 240s ./frontend/... ./internal/frontend ./internal/adapters/cli ./internal/ast ./internal/compile ./internal/program ./internal/eval ./internal/conformance ./internal/doccheck
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 180s go mod tidy -diff
timeout 30s git diff --check
```

Expected: PASS with no tidy or whitespace diff.

**Step 3: Run the full release matrix**

Run each command once:

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 360s go test -count=1 -timeout 300s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -tags=purego -timeout 240s ./...
timeout 420s go test -count=1 -tags=integration -timeout 360s ./...
timeout 300s env PATH="/tmp/opencode/nornrune-tools:$PATH" go run ./cmd/devx policy:check
timeout 300s env PATH="/tmp/opencode/nornrune-tools:$PATH" go run ./cmd/devx results:check
timeout 300s env PATH="/tmp/opencode/nornrune-tools:$PATH" go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 300s go build -trimpath ./cmd/protoc-gen-nornrune
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
```

Expected: PASS. If Docker is unavailable, report only the integration/generated checks that could not run; do not weaken them.

**Step 4: Request independent review and fix findings with RED/GREEN tests**

Use `superpowers:requesting-code-review`. Review against the approved design, capability matrices, differential semantics, exact spans, parser limits, generated-code drift, dependency boundaries, native byte identity, and zero-allocation warmed evaluation. For each Critical, High, or Medium defect, add a focused failing test before the fix and rerun the affected package plus full native tests.

**Step 5: Stage only Task 52 files and commit**

Inspect `git status`, `git diff`, `git diff --check`, and `git log --oneline -10`. Never stage `/home/sebishogun/Learning/Go/NornRune/AGENTS.md` or unrelated user changes.

Commit and push:

```bash
git add frontend cmd/protoc-gen-nornrune internal/frontend testdata/frontends \
  docs/plans/2026-08-24-policy-compatibility-frontends-design.md \
  docs/plans/2026-08-24-policy-compatibility-frontends.md \
  go.mod go.sum buf.yaml buf.gen.yaml buf.frontend.gen.yaml \
  cmd/devx/cmd internal/ast internal/compile internal/program internal/eval internal/truth \
  internal/adapters/cli internal/adapters/grpcapi internal/conformance internal/doccheck \
  README.md docs .github/workflows/ci.yml .goreleaser.yaml
git diff --cached --check
git commit -m "feat: add policy language compatibility frontends"
git push origin main
```

Expected: one reviewed Task 52 commit on `main`. Mark Task 52 complete and begin Task 53; keep Task 60 pending.
