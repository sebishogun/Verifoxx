# NornRune Product CLI Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a scriptable Cobra CLI that validates, compiles, evaluates, explains, and simulates the NornRune policy pack from embedded or caller-provided inputs.

**Architecture:** Thin Cobra commands call one in-memory policy pipeline built from the existing decoders, compiler, evaluator, and result encoder. A reusable NornRune policy-pack package owns the embedded semantic policy and field schema; explain and simulate compact one selected request into a fresh one-row SoA batch before evaluation.

**Tech Stack:** Go 1.27, Cobra v1.10.2, embedded files, pointerless AST, immutable compiled Program, SoA request batches, SIMD-capable evaluator, append-based JSON output.

**Repository Rule:** Do not create commits unless the user explicitly requests them. The commit messages below are checkpoints only.

---

### Task 1: Create The Reusable NornRune Policy Pack

**Files:**
- Create: `policies/nornrune/pack.go`
- Create: `policies/nornrune/pack_test.go`
- Modify: `internal/conformance/nornrune_test.go:22-112`

**Step 1: Write the failing policy-pack tests**

Add tests in package `nornrune_test` that assert:

- `nornrune.Source()` is non-empty and contains the semantic policy.
- `nornrune.NewSchema()` returns seven fields in the approved order.
- Every field is `schema.ValueKindSymbol` and has the expected group.
- Each field name resolves through the returned interner.
- The embedded source decodes and lowers with the returned schema and interner.

Use a fixed expected table rather than deriving expectations from production
metadata:

```go
var wantFields = []struct {
    name  string
    group schema.FieldGroup
}{
    {"requester.team", schema.FieldGroupSubject},
    {"requester.trust", schema.FieldGroupSubject},
    {"action.type", schema.FieldGroupAction},
    {"action.output", schema.FieldGroupOutput},
    {"action.dataset", schema.FieldGroupResource},
    {"environment.execution_env", schema.FieldGroupContext},
    {"environment.usage", schema.FieldGroupContext},
}
```

**Step 2: Run the focused test and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./policies/nornrune
```

Expected: FAIL because the directory has no Go package and `Source` and
`NewSchema` do not exist.

**Step 3: Implement the policy pack**

In `pack.go`:

```go
package nornrune

import (
    _ "embed"

    "github.com/sebishogun/nornrune/internal/schema"
)

//go:embed policy.json
var source string

func Source() string { return source }

func NewSchema() (*schema.Schema, *schema.Interner, error) {
    symbols := schema.NewSymbolInterner(16)
    fields := schema.NewBuilder()
    // Add the seven static rows in FieldID order and return the first error.
    return fields.Finish(), symbols, nil
}
```

Keep the field specification as one static array. Do not export mutable slices
or return embedded `[]byte` storage.

**Step 4: Run the policy-pack tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./policies/nornrune
```

Expected: PASS.

**Step 5: Replace the conformance-only schema and filesystem read**

Update `internal/conformance/nornrune_test.go` to call
`nornrune.NewSchema()` and use `[]byte(nornrune.Source())`. Delete
`conformanceSchema` and remove its `os.ReadFile` use. Keep filesystem reads for
the two golden output files.

**Step 6: Run conformance and policy-pack tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./policies/nornrune ./internal/conformance
```

Expected: PASS and unchanged conformance output.

**Step 7: Optional commit checkpoint**

If the user requests a commit:

```bash
git add policies/nornrune/pack.go policies/nornrune/pack_test.go internal/conformance/nornrune_test.go
git commit -m "feat: add reusable nornrune policy pack"
```

### Task 2: Add Cobra Root And Preserve Process Contracts

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/adapters/cli/root.go`
- Create: `internal/adapters/cli/cli_test.go`
- Modify: `internal/app/app.go`
- Modify: `internal/app/app_test.go`
- Modify: `cmd/nornrune/main.go`

**Step 1: Write failing root command tests**

Define a package-local test helper:

```go
func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
    t.Helper()
    var out, errOut bytes.Buffer
    code = Execute(args, bytes.NewReader(nil), &out, &errOut)
    return code, out.String(), errOut.String()
}
```

Test these contracts before adding command implementations:

- No arguments: exit 0, help on stdout, empty stderr.
- `--help`, `-h`, and `help`: exit 0 and non-empty help.
- `--version`: exactly `buildinfo.Version()+"\n"` on stdout.
- Unknown command: exit 2, empty stdout, usage/error on stderr.
- Extra root arguments: exit 2.
- A failing stdout returns 1 for help and version.
- A failing stderr returns 1 while reporting a usage error.

**Step 2: Run the focused test and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli
```

Expected: FAIL because package `cli` and `Execute` do not exist.

**Step 3: Pin Cobra**

Run:

```bash
timeout 120s go get github.com/spf13/cobra@v1.10.2
```

Inspect `go.mod` and `go.sum`; Cobra must be a direct dependency at exactly
`v1.10.2`.

**Step 4: Implement root execution and error classes**

In `root.go`, expose only the process-facing function:

```go
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int
```

Use an unexported constructor for tests and production dependencies:

```go
type dependencies struct {
    readFile func(string) ([]byte, error)
    policy   string
    requests string
    evidence string
    version  string
}
```

Order pointer-bearing fields first and run `fieldalignment` later. Production
dependencies use `os.ReadFile`, `nornrune.Source()`, fixture request/evidence
strings, and `buildinfo.Version()`.

Configure Cobra with `SilenceErrors: true` and `SilenceUsage: true`. Implement
an internal status error carrying code 1 or 2 and a `quiet` flag for validation
diagnostics already written to stdout. Parse `--version` as a root Boolean flag
so trailing arguments still fail argument validation.

`Execute` must:

1. Normalize nil readers/writers to safe failure behavior rather than panic.
2. Bind args and the three I/O streams to the root command.
3. Execute once.
4. Return 0 on success.
5. Render operational errors once on stderr and return 1.
6. Render argument errors plus usage on stderr and return 2.
7. Return 1 if writing the error or usage fails.

Do not call `os.Exit` below `cmd/nornrune`.

**Step 5: Implement app delegation without breaking callers**

Keep:

```go
func Run(args []string, stdout, stderr io.Writer) int
```

Add:

```go
func RunWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer) int
```

`Run` delegates with an empty reader. `RunWithInput` delegates to
`cli.Execute`. Update `main.go` to pass `os.Stdin`.

**Step 6: Run CLI and app tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli ./internal/app ./cmd/nornrune
```

Expected: PASS.

**Step 7: Optional commit checkpoint**

If requested:

```bash
git add go.mod go.sum internal/adapters/cli/root.go internal/adapters/cli/cli_test.go internal/app/app.go internal/app/app_test.go cmd/nornrune/main.go
git commit -m "feat: add cobra command root"
```

### Task 3: Add Input Resolution And The Shared Policy Pipeline

**Files:**
- Modify: `internal/adapters/cli/root.go`
- Create: `internal/adapters/cli/evaluate.go`
- Modify: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing source-resolution tests**

Use a test `dependencies.readFile` function backed by fixed path/value pairs,
not a production map. Cover:

- Omitted paths select embedded policy, requests, and evidence.
- Each external path replaces only its selected input.
- A path of `-` reads stdin.
- More than one `-` is a usage error with exit 2.
- A missing file is an operational error with exit 1 and empty stdout.
- Input slices returned by the loader are not mutated.

**Step 2: Run the focused tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test(Source|External|Stdin)' ./internal/adapters/cli
```

Expected: FAIL because shared source flags and loading do not exist.

**Step 3: Implement source flags and input loading**

Use one value type shared by relevant commands:

```go
type sourceFlags struct {
    policyPath   string
    requestPath  string
    evidencePath string
}
```

Bind `--policy`, `--requests`, and `--evidence`. Load only documents required by
the command. Count `-` before reading and reject a count above one. Convert
embedded strings to caller-owned bytes. Use `io.ReadAll` for stdin in Task 26;
Task 47 will centralize stream limits.

**Step 4: Write failing pipeline tests**

Test unexported helpers directly in package `cli`:

- Embedded policy decodes and compiles.
- Malformed policy returns a decode-stage error.
- Semantically invalid policy returns stable diagnostics before lowering.
- Embedded requests/evidence decode to five rows.
- Evaluation returns five result rows and request IDs R1-R5.

**Step 5: Run the pipeline tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestPipeline' ./internal/adapters/cli
```

Expected: FAIL because the pipeline helpers do not exist.

**Step 6: Implement the shared pipeline**

Keep all reusable mutable state owned by one command invocation:

```go
type engine struct {
    policyDecoder jsonpolicy.Decoder
    batchDecoder  jsonbatch.Decoder
    validator     compile.Validator
    lowerer       compile.Lowerer
    batchBuilder  eval.Builder
    executor      eval.Executor
    results       result.Batch
    diagnostics   []compile.Diagnostic
}
```

Review field order with `fieldalignment`; preserve logical locality if a
suggestion would separate frequently used state.

Implement helpers that:

1. Build the NornRune field schema and symbol interner.
2. Create `ast.NewBuilder` with source-byte capacity and conservative hints.
3. Decode through a reusable `jsonpolicy.Decoder`.
4. Validate into caller-owned diagnostic storage.
5. Lower into an engine-owned `program.Program` only after validation succeeds.
6. Decode request/evidence bytes through `jsonbatch.Decoder` and
   `eval.Builder`.
7. Evaluate into the engine-owned `result.Batch`.

Use nonzero local decoder limits for the quadratic catalog and requirement
dimensions. Keep constants in the CLI adapter until Task 47 introduces common
security limits. Do not add limits inside evaluator kernels.

**Step 7: Run source and pipeline tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli
```

Expected: PASS for all root, source, and pipeline tests implemented so far.

**Step 8: Optional commit checkpoint**

If requested:

```bash
git add internal/adapters/cli/root.go internal/adapters/cli/evaluate.go internal/adapters/cli/cli_test.go
git commit -m "feat: add cli policy pipeline"
```

### Task 4: Implement Validate And Compile Commands

**Files:**
- Create: `internal/adapters/cli/validate.go`
- Create: `internal/adapters/cli/compile.go`
- Create: `internal/adapters/cli/output.go`
- Modify: `internal/adapters/cli/root.go`
- Modify: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing validate command tests**

Cover:

- Embedded `validate`: exit 0, exact
  `{"valid":true,"diagnostics":[]}\n`, empty stderr.
- A semantic defect: exit 1, `valid:false`, deterministic diagnostic order,
  stable code/table/row/member/span/typed ID fields, empty stderr.
- Malformed JSON: exit 1, empty stdout, one bounded stderr error.
- Unexpected positional arguments: exit 2.
- `--policy` uses injected external content.

**Step 2: Run validate tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestValidate' ./internal/adapters/cli
```

Expected: FAIL because `validate` is not registered.

**Step 3: Implement append-based JSON output helpers**

In `output.go`, implement unexported helpers for:

- Canonical JSON string escaping from `[]byte` and `string`.
- Decimal `uint32`/`uint64` appends with `strconv.AppendUint`.
- Signed integer appends with `strconv.AppendInt`.
- Lowercase SHA-256 appends.
- Complete-write checking that returns `io.ErrShortWrite`.
- Deterministic validation diagnostic encoding.

Match the existing `jsonresult` UTF-8 policy: invalid input bytes become
`\ufffd`; control characters, quotes, and backslashes are escaped. Do not use
maps, reflection, `fmt.Sprintf`, or `encoding/json`.

**Step 4: Implement `validate`**

Register a no-positional-argument command with `--policy`. Decode once and run
`compile.Validator`. Write valid or invalid JSON. Return a quiet status-1 error
after successfully writing invalid diagnostics so `Execute` does not append a
stderr message.

**Step 5: Run validate tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestValidate' ./internal/adapters/cli
```

Expected: PASS.

**Step 6: Write failing compile command tests**

Cover exact field order and values for the embedded policy:

```text
name
version
sha256
instructions
requirements
clauses
truth_slots
reason_slots
```

Also cover external policy loading, malformed input, semantic failure, no
positional arguments, and stdout writer failure.

**Step 7: Run compile tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestCompile' ./internal/adapters/cli
```

Expected: FAIL because `compile` is not registered.

**Step 8: Implement `compile`**

Decode and lower through the shared pipeline. Resolve policy name/version with
`Program.Symbol`, append the program hash in lowercase hexadecimal, and append
counts directly from immutable program columns and slot scalars. Emit one
compact JSON object plus newline.

**Step 9: Run validate and compile tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli
```

Expected: PASS.

**Step 10: Optional commit checkpoint**

If requested:

```bash
git add internal/adapters/cli/root.go internal/adapters/cli/validate.go internal/adapters/cli/compile.go internal/adapters/cli/output.go internal/adapters/cli/cli_test.go
git commit -m "feat: validate and compile policies from cli"
```

### Task 5: Implement Full-Batch Evaluation

**Files:**
- Modify: `internal/adapters/cli/evaluate.go`
- Modify: `internal/adapters/cli/root.go`
- Modify: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing evaluate tests**

Cover:

- `evaluate` with no flags exits 0 and is byte-identical to
  `testdata/golden/requests.json`.
- Stdout contains no help, progress, tier, or log text.
- Stderr is empty on success.
- External policy, request, and evidence paths all reach the same pipeline.
- A malformed request/evidence document exits 1 with empty stdout.
- Positional arguments and conflicting stdin paths exit 2.
- A short or failing stdout writer exits 1.

Read the golden in the test; do not duplicate a second literal copy.

**Step 2: Run evaluate tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestEvaluate' ./internal/adapters/cli
```

Expected: FAIL because `evaluate` is not registered.

**Step 3: Implement `evaluate`**

Register a no-positional-argument command with all three source flags. Run the
shared compile, batch decode, and evaluation sequence. Bind one
`jsonresult.Encoder` to the compiled program and call:

```go
encoded, err := encoder.Append(dst[:0], batch.RequestIDs, &results, []byte(version))
```

Write the complete document only after encoding succeeds. Keep encoded storage
owned by the command invocation.

**Step 4: Run evaluate and conformance tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli ./internal/conformance
```

Expected: PASS and byte-identical outputs.

**Step 5: Run the required executable comparison**

Run:

```bash
timeout 120s go run ./cmd/nornrune evaluate > /tmp/opencode/nornrune-task26-results.json
```

Expected: both commands exit 0 and `cmp` prints nothing.

**Step 6: Optional commit checkpoint**

If requested:

```bash
git add internal/adapters/cli/evaluate.go internal/adapters/cli/root.go internal/adapters/cli/cli_test.go
git commit -m "feat: evaluate policy inputs from cli"
```

### Task 6: Compact And Explain One Request

**Files:**
- Create: `internal/adapters/cli/explain.go`
- Modify: `internal/adapters/cli/root.go`
- Modify: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing request-ID and row-compaction tests**

Test `parseRequestID` with valid `R1`, `R5`, and `R4294967295`; reject missing
`R`, lowercase prefixes, zero, signs, trailing bytes, and overflow.

Test an unexported row selector with synthetic typed fields and evidence:

- Copies exactly one request ID.
- Copies present symbol, integer, timestamp, Boolean, and presence fields.
- Keeps absent fields absent.
- Resolves source extension symbols and re-interns them in the destination.
- Copies only evidence referenced by the selected row.
- Remaps evidence references to dense rows and preserves qualifiers.
- Rejects malformed source ranges and out-of-range rows without partial output.

**Step 2: Run row-selection tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test(ParseRequestID|CompactRow)' ./internal/adapters/cli
```

Expected: FAIL because parsing and compaction do not exist.

**Step 3: Implement request parsing and row compaction**

Use `strconv.ParseUint(id[1:], 10, 32)` after exact prefix checks. Return
`schema.RequestID`, never `schema.RequirementID`.

Add a reusable selector with caller-owned scratch:

```go
type rowSelector struct {
    builder eval.Builder
    refs    []uint32
}
```

The compaction algorithm must follow the approved design order. Index typed
fact columns with widened `uint64` arithmetic before converting to `int`. Use
`Batch.Present`, `Batch.Boolean`, `Program.FieldIndex.Lookup`,
`sourceBuilder.Symbol`, destination `Builder.InternSymbol`, typed setters,
`SetEvidence`, `SetEvidenceCSR`, and `Finish`.

Do not introduce a generic callback in the field loop. Simulation overrides
will be passed as a sorted or linearly searched typed slice; one selected row
does not justify a map.

**Step 4: Run row-selection tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test(ParseRequestID|CompactRow)' ./internal/adapters/cli
```

Expected: PASS.

**Step 5: Write failing explain command tests**

For each R1-R5, assert:

- Exit 0.
- One result is present.
- Its request ID and decision match the corresponding golden row.
- Policy and engine metadata remain present.
- Stderr is empty.

Also cover missing ID, malformed ID, absent request, extra arguments, external
inputs, and output writer failure.

**Step 6: Run explain tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestExplain' ./internal/adapters/cli
```

Expected: FAIL because `explain` is not registered.

**Step 7: Implement `explain`**

Register `cobra.ExactArgs(1)` plus all three source flags. Parse the request ID,
decode the full input once, find the request row with a linear scan, compact it,
evaluate one row, and encode through the unchanged production result encoder.

**Step 8: Run explain and full CLI tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli
```

Expected: PASS.

**Step 9: Optional commit checkpoint**

If requested:

```bash
git add internal/adapters/cli/explain.go internal/adapters/cli/root.go internal/adapters/cli/cli_test.go
git commit -m "feat: explain one request from cli"
```

### Task 7: Implement Typed Simulation Overrides

**Files:**
- Create: `internal/adapters/cli/simulate.go`
- Modify: `internal/adapters/cli/explain.go`
- Modify: `internal/adapters/cli/root.go`
- Modify: `internal/adapters/cli/cli_test.go`

**Step 1: Write failing override parser tests**

Define a compact typed override record ordered for alignment:

```go
type fieldOverride struct {
    value string
    field schema.FieldID
    kind  schema.ValueKind
    // Parsed scalar payloads follow after pointer-bearing fields.
}
```

Test:

- Exact `field=value` splitting at the first `=`.
- Empty field or value rejection.
- Field resolution through compiled `FieldNames`.
- Duplicate target rejection even when text is repeated.
- Symbol bytes preserved.
- Base-10 signed integer and timestamp parsing with overflow rejection.
- Exact lowercase `true` and `false` Boolean/presence parsing.
- Unknown fields and incompatible values are usage errors.

**Step 2: Run parser tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestParseOverrides' ./internal/adapters/cli
```

Expected: FAIL because override parsing does not exist.

**Step 3: Implement typed override parsing**

Resolve a field by looking up its name symbol in the immutable Program and then
finding that symbol in `Program.FieldNames`. Read the kind from
`Program.FieldKinds`. Store parsed values in a small caller-owned slice and use
a linear duplicate scan. Preserve the original symbol string until row
compaction interns it.

Do not use `map[string]any`, reflection, or JSON rewriting.

**Step 4: Extend compaction to apply overrides**

For each FieldID in stable order:

1. Find an override for that field.
2. If present, write the override with the destination builder's typed setter.
3. For a false presence-only override, leave the field absent.
4. Otherwise copy the source field when present.

An override therefore replaces rather than duplicates the source value.

**Step 5: Run parser and compaction tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test(ParseOverrides|CompactRow)' ./internal/adapters/cli
```

Expected: PASS.

**Step 6: Write failing simulate command tests**

Cover meaningful policy transitions, for example:

- Simulating R3 with `environment.usage=standard` changes `Revise` to the
  policy-derived result expected from the remaining evidence.
- Simulating R2 with `action.output=aggregate_counts` removes the direct
  individual-disclosure violation but still respects all other requirements.
- Multiple `--set` flags are applied in flag order after duplicate rejection.

Also cover missing `--set`, malformed assignments, unknown fields, absent or
malformed request IDs, external inputs, no input mutation, JSON-only stdout,
and writer failure.

Do not hardcode decisions in implementation; expected tests come from policy
semantics.

**Step 7: Run simulate tests and verify failure**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestSimulate' ./internal/adapters/cli
```

Expected: FAIL because `simulate` is not registered.

**Step 8: Implement `simulate`**

Register `cobra.ExactArgs(1)`, all three source flags, and repeatable
`--set`. Require at least one assignment. Compile first so override fields and
kinds resolve against immutable metadata; decode once; compact the selected row
with overrides; evaluate and encode one result.

**Step 9: Run all CLI tests**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli
```

Expected: PASS.

**Step 10: Optional commit checkpoint**

If requested:

```bash
git add internal/adapters/cli/simulate.go internal/adapters/cli/explain.go internal/adapters/cli/root.go internal/adapters/cli/cli_test.go
git commit -m "feat: simulate typed request changes"
```

### Task 8: Verify Task 26 End To End

**Files:**
- Modify only files identified by verification failures.

**Step 1: Format changed Go files**

Run:

```bash
timeout 30s gofmt -w policies/nornrune/*.go internal/adapters/cli/*.go internal/app/*.go internal/conformance/nornrune_test.go cmd/nornrune/main.go
```

Expected: exit 0.

**Step 2: Tidy and inspect dependencies**

Run:

```bash
timeout 120s go mod tidy
```

Expected: Cobra remains pinned at `v1.10.2`; only required transitive modules
remain.

**Step 3: Run focused tests freshly**

Run:

```bash
timeout 120s go test -count=1 -timeout 60s ./policies/nornrune ./internal/adapters/cli ./internal/app ./internal/conformance
```

Expected: PASS.

**Step 4: Reproduce the required result file**

Run:

```bash
timeout 120s go run ./cmd/nornrune evaluate > /tmp/opencode/nornrune-task26-results.json
```

Expected: both exit 0 and `cmp` prints nothing.

**Step 5: Run the complete native and fallback suites**

Run independently:

```bash
timeout 120s go test -count=1 -timeout 60s ./...
```

Expected: all PASS.

**Step 6: Run race and checkptr**

Run:

```bash
timeout 180s go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./...
```

Expected: PASS with no race or checkptr report.

**Step 7: Run static checks**

Run independently:

```bash
timeout 120s go vet ./...
```

Expected: every command exits 0; analyzers and format checks print nothing.

**Step 8: Inspect final changes**

Run independently:

```bash
git status --short
```

Expected: only intended Task 26 changes plus pre-existing Tasks 18-25 work are
present. Do not revert unrelated dirty files.

**Step 9: Optional final commit checkpoint**

Only if explicitly requested:

```bash
git add go.mod go.sum cmd/nornrune internal/app internal/adapters/cli internal/conformance policies/nornrune docs/plans/2026-08-23-product-cli-design.md docs/plans/2026-08-23-product-cli.md
```
