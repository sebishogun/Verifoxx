# Rego Frontend Definedness Semantics Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement the bounded Rego v1 frontend while preserving OPA negation-as-failure exactly for present and missing bound fields.

**Architecture:** Append one generic `Defined` leaf to the public semantic table and shared core. It reads existing field presence masks as a classical Boolean, while the existing native `Exists` operation retains its missing-is-unknown contract. The Rego frontend rewrites `not E(field)` into `NOT DEFINED(field) OR NOT E(field)` before using the shared compiler.

**Tech Stack:** Go 1.27, OPA/Rego v1.19.1, existing frontend SoA/CSR builder, append-only AST/Program enums, presence-mask evaluator storage, and the shared semantic compiler.

---

## Fixed Constraints

- Use TDD and observe each intended RED failure before production edits.
- Bound every test, build, vet, fuzz, and analyzer command with an outer timeout; every `go test` also gets `-timeout`.
- Append enum values. Never renumber existing AST, Program, or frontend values.
- Keep `Exists` unchanged: absent input remains unknown with `ReasonMissing`.
- Keep OPA types inside `frontend/rego`; evaluator packages must not import OPA or `frontend`.
- Preserve exact UTF-8 byte spans from OPA `Location.Offset` and `Location.Text`.
- Keep source order and deterministic bounded diagnostics.
- Do not commit unless the user explicitly requests it.

### Task 1: Append The Public Definedness Node

**Files:**
- Modify: `frontend/frontend.go`
- Modify: `frontend/frontend_test.go`
- Modify: `frontend/builder.go`
- Modify: `frontend/builder_test.go`
- Modify: `frontend/fuzz_test.go`

**Step 1: Write RED tests**

Require `NodeKindDefined == 6`, strict text name `defined`, and:

```go
defined, err := builder.AddDefined(1, Span{Start: 0, End: 13})
```

The row carries one field, no operation, literal, children, or list, and its
exact span. Invalid fields, spans, and limits must not mutate prior columns.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s ./frontend
```

Expected: FAIL because `NodeKindDefined` and `AddDefined` do not exist.

**Step 3: Implement and run GREEN**

Append the enum/name and implement `AddDefined` by validating the field and
calling `checkAppend(span, 1, 0, 0, 0)` before appending a payload-free leaf.
Add it to `FuzzBuilder`.

```bash
timeout 90s go test -count=1 -timeout 60s ./frontend
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzBuilder$' -fuzztime=5s ./frontend
```

Expected: PASS.

### Task 2: Append Definedness To The Shared Core

**Files:**
- Modify: `internal/ast/kind.go`
- Modify: `internal/ast/builder.go`
- Modify: `internal/ast/builder_test.go`
- Modify: `internal/compile/validate.go`
- Modify: `internal/compile/normalize.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/schedule.go`
- Modify: `internal/compile/lower_api_test.go`
- Modify: `internal/program/instruction.go`
- Modify: `internal/program/program_test.go`
- Modify: `internal/eval/predicate.go`
- Modify: `internal/eval/predicate_test.go`
- Modify: `internal/eval/executor_test.go`
- Modify: `internal/adapters/cli/tui.go`
- Modify: `internal/adapters/cli/graph_data.go`
- Modify: `internal/adapters/cli/graph_data_test.go`

**Step 1: Write AST, Program, and evaluator RED tests**

Require append-only values `ast.CompareOpDefined == 9` and
`program.OpcodeDefined == 14`. `ast.Builder.AddDefined` must append a compare
row with a valid field, zero value, and no list.

For rows `0, 1, 63, 64, 65, 1024`, require Defined to write:

- Positive = valid presence bits;
- Negative = valid bits absent from the presence mask;
- every reason plane = zero;
- tail bits = zero;
- `0 B/op` and `0 allocs/op` after evaluator binding.

Keep existing Exists tests unchanged and explicitly assert missing Exists is
unknown while missing Defined is false.

**Step 2: Run RED**

```bash
timeout 150s go test -count=1 -timeout 120s ./internal/ast ./internal/program ./internal/compile ./internal/eval ./internal/adapters/cli
```

Expected: FAIL on missing `CompareOpDefined`, `OpcodeDefined`, and evaluator
handling.

**Step 3: Implement the minimal append-only operation**

Append `CompareOpDefined`; make `RequiresValue` false for Exists and Defined;
add `ast.Builder.AddDefined`; and validate Defined with a field, zero scalar
value, and an empty list.

Append `OpcodeDefined`, map it in `compareOpcode`, and resize every schedule
array/range from `OpcodeBoolean` to `OpcodeDefined`. Update stable CLI/graph
labels to `defined`.

In scalar predicate evaluation, validate Defined like a value-free field leaf,
then run one word loop:

```go
valid := leafWordMask(word, words, rows)
present := presence[word] & valid
dst.Positive[word] = present
dst.Negative[word] = valid &^ present
```

Call `resetLeafOutputs` first so all reasons are clear. Do not route Defined
through SIMD comparisons or fact indexes; the scalar word loop is already a
bulk bitwise kernel.

**Step 4: Run GREEN and allocation checks**

```bash
timeout 150s go test -count=1 -timeout 120s ./internal/ast ./internal/program ./internal/compile ./internal/eval ./internal/adapters/cli
timeout 120s go test -timeout 90s -run '^$' -bench '^BenchmarkExecutorDefined$' -benchmem -count=6 ./internal/eval
```

Expected: PASS and `0 B/op`, `0 allocs/op`.

### Task 3: Validate And Lower Definedness Through The Shared Frontend

**Files:**
- Modify: `internal/frontend/semantic.go`
- Modify: `internal/frontend/semantic_test.go`
- Modify: `internal/frontend/lower.go`
- Modify: `internal/frontend/lower_test.go`
- Modify: `internal/frontend/fuzz_test.go`

**Step 1: Write RED tests**

Reject Defined rows with field zero/out of range, operation, literal, child/list
edges, or invalid spans. Lower one valid row to `OpcodeDefined` and evaluate a
present false-valued field as Approve and an absent field as Reject.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/frontend
```

Expected: FAIL until shared validation and lowering recognize Defined.

**Step 3: Implement and run GREEN**

Validate Defined as a depth-one leaf with exactly one field and no other
payload. Lower it through `ast.Builder.AddDefined`.

```bash
timeout 150s go test -count=1 -timeout 120s ./frontend ./internal/frontend ./internal/ast ./internal/compile ./internal/eval
timeout 45s go test -count=1 -timeout 35s -run '^$' -fuzz '^FuzzSemanticPolicy$' -fuzztime=5s ./internal/frontend
```

Expected: PASS.

### Task 4: Establish The Rego Frontend Contract

**Files:**
- Create: `frontend/rego/rego_test.go`
- Create: `frontend/rego/fuzz_test.go`
- Create: `testdata/frontends/rego/allow.rego`
- Create: `testdata/frontends/rego/default.rego`
- Create: `testdata/frontends/rego/unsupported.rego`

**Step 1: Write supported and rejected subset tests**

Use static bindings such as `input.team`, `input.count`, and `input.enabled`,
with `BindingSet.Decision = "allow"`. Cover complete Boolean rules,
conjunctive bodies, multiple-rule OR, Boolean shorthand/constants, scalar and
reversed comparisons, homogeneous array/set membership, no/false/true defaults,
ownership, exact Unicode byte spans, all limits, deterministic diagnostics, and
stable capabilities.

Reject imports, `data`, functions, `else`, recursion, comprehensions, variables,
assignment, unification, partial documents, non-Boolean heads, with-modifiers,
unsupported built-ins, field-to-field comparisons, unrelated rules, duplicate
defaults, and undeclared input paths.

**Step 2: Write exact OPA negation tests**

For `allow if { not input.enabled }`, require:

| Input | OPA | Verifoxx |
|---|---|---|
| `{"enabled": true}` | undefined | Reject |
| `{"enabled": false}` | true | Approve |
| `{}` | true | Approve |

Inspect the semantic tree and require `NodeKindDefined` in the negation
expansion.

**Step 3: Run RED**

```bash
timeout 240s go test -count=1 -timeout 210s ./frontend/rego
```

Expected: FAIL because the Rego package does not exist.

### Task 5: Parse Bounded Rego v1 Modules

**Files:**
- Create: `frontend/rego/parser.go`

**Step 1: Validate and parse**

Before OPA, validate limits, source size, UTF-8, bindings, a configured
decision, and binding sources rooted at `input`. Parse with:

```go
ast.ParseModuleWithOpts("policy.rego", string(source), ast.ParserOptions{
    RegoVersion: ast.RegoV1,
})
```

Own the module, cloned source/bindings, and limits in package-local `Parsed`.
Reject imports and malformed module structure.

**Step 2: Convert diagnostics and spans**

Convert at most `MaxDiagnostics` OPA errors. Use zero-based `Location.Offset`
and `Offset + len(Location.Text)` directly as a half-open byte span; clamp only
malformed upstream locations. Never convert through rune columns.

**Step 3: Run parser tests**

```bash
timeout 180s go test -count=1 -timeout 150s -run 'TestParse|Test.*Span|Test.*Malformed' ./frontend/rego
```

Expected: parser and ownership cases pass; lowering remains RED.

### Task 6: Lower Complete Boolean Decisions

**Files:**
- Create: `frontend/rego/lower.go`

**Step 1: Validate rule heads and atoms**

Require the configured complete Boolean decision, no args/key/dynamic ref/else,
at most one Boolean default, and true non-default heads. Accept direct Boolean
constants and bound Boolean refs. Accept only `equal`, `neq`, `lt`, `lte`, `gt`,
`gte`, and `internal.member_2`, with one field and matching scalar constants.
Reverse ordered operations when the literal is left. Reuse typed scratch.

**Step 2: Lower exact negation**

For base field node `E` and field `F`, append:

```text
definedF  = Defined(F)
notDefined = Not(definedF)
notE      = Not(E)
result    = Any(notDefined, notE)
```

Negated constants use ordinary Not. Reject every other negated form.

**Step 3: Build roots and defaults**

Return a single child directly; use All for multi-expression bodies and Any for
multiple rules. No default uses Escalate, false default uses Reject, and true
default validates rules but publishes only a true root. A default-only false
policy uses a false root; no rules and no default is invalid.

**Step 4: Run GREEN**

```bash
timeout 240s go test -count=1 -timeout 210s ./frontend/rego ./internal/frontend
```

Expected: supported, rejected, default, span, limit, ownership, and exact
negation tests pass.

### Task 7: Add Capabilities, Differential Tests, And Fuzzing

**Files:**
- Create: `frontend/rego/capabilities.go`
- Complete: `frontend/rego/rego_test.go`
- Complete: `frontend/rego/fuzz_test.go`

**Step 1: Publish stable caller-owned capabilities**

Cover Rego v1 modules, complete Boolean decisions/defaults, multiple rules,
conjunctive bodies, static input references, scalar comparisons, constant
membership, presence-aware negation, and rejected syntax families.

**Step 2: Differential and fuzz verification**

Use OPA `rego.New` with `Query`, `Module`, `Input`, and
`SetRegoVersion(ast.RegoV1)`. Compare positive true/false/missing, defaults,
multiple rules, array/set membership, and negation. Fuzz valid, unsupported,
malformed, and invalid UTF-8 seeds; compile every returned semantic policy
through `internal/frontend`.

```bash
timeout 60s go test -count=1 -timeout 50s -run '^$' -fuzz '^FuzzCompile$' -fuzztime=5s ./frontend/rego
timeout 240s go test -count=1 -timeout 210s -race ./frontend/rego ./internal/frontend
```

Expected: PASS.

### Task 8: Tidy And Verify Task 52.5

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Tidy and focused verification**

```bash
timeout 180s go mod tidy
timeout 240s go test -count=1 -timeout 210s ./frontend/... ./internal/frontend ./internal/ast ./internal/compile ./internal/program ./internal/eval
```

Expected: OPA v1.19.1 is direct and focused tests pass.

**Step 2: Repository gates**

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 30s git diff --check
```

Expected: PASS. Do not create a commit.
