# Applicability And Field Indexes Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add immutable field-to-column mappings and conservative symbolic applicability masks to every compiled Program.

**Architecture:** `internal/index` owns pointerless SoA/CSR index data and a reusable cold-path builder. Candidate bits represent zero-based Program requirement rows. `compile.Lowerer` extracts only necessary positive symbol constraints from applicability `Equal`, `In`, and `All` instructions, builds indexes into reusable private output, and relies on `program.Freeze` for exact published ownership.

**Tech Stack:** Go 1.27, typed `schema` IDs, uint64 bitmaps, sorted SoA/CSR columns, iterative compiler traversal, table-driven tests, `testing.AllocsPerRun`.

---

### Task 1: Build Dense Field-To-Column Mappings

**Files:**
- Create: `internal/index/schema.go`
- Create: `internal/index/schema_test.go`

**Step 1: Write failing schema tests**

Define tests for this API:

```go
var (
    ErrInvalidSchema = errors.New("index: invalid field schema")
    ErrInvalidPolicy = errors.New("index: invalid applicability constraints")
    ErrInvalidQuery  = errors.New("index: invalid candidate query")
    ErrIndexTooLarge = errors.New("index: fixed-width limit exceeded")
)

type Schema struct {
    Kinds   []schema.ValueKind
    Columns []uint32
    Counts  [6]uint32
}

func BuildSchema(dst *Schema, kinds []schema.ValueKind) error
func (s Schema) Lookup(field schema.FieldID) (schema.ValueKind, uint32, bool)
func (s Schema) ColumnCount(kind schema.ValueKind) (uint32, bool)
func (s Schema) Clone() Schema
```

Use kinds `[symbol, integer, symbol, boolean, timestamp, presence]`. Assert
columns `[0, 0, 1, 0, 0, 0]`, exact per-kind counts, invalid zero/out-of-range
lookups, exact-capacity clone storage, and clone independence after rebuilding
the source. Add malformed-kind and nil-destination cases that leave a populated
destination unchanged.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/index -run '^TestSchema'
```

Expected: compile failure because `internal/index` does not exist.

**Step 3: Implement the field index**

Validate every kind and `len(kinds) <= math.MaxUint32` before changing `dst`.
Count kinds in a local fixed array, resize `dst.Kinds` and `dst.Columns` once,
then fill them in FieldID order with kind-local zero-based offsets. Reuse
destination capacities but clear/truncate all active storage. `Clone` must
return nil for empty slices and exact-capacity copies otherwise.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/index/schema.go internal/index/schema_test.go
go test -count=1 -timeout 60s ./internal/index -run '^TestSchema'
GOARCH=386 go test -count=1 -timeout 60s ./internal/index -run '^TestSchema'
```

Expected: PASS on amd64 and 386.

**Step 5: Commit**

```bash
git add internal/index/schema.go internal/index/schema_test.go
git commit -m "feat: map fields to typed columns"
```

### Task 2: Canonicalize Applicability Constraint Masks

**Files:**
- Create: `internal/index/policy.go`
- Create: `internal/index/policy_test.go`

**Step 1: Write failing construction tests**

Define pointerless constraint input and immutable output columns:

```go
type Constraints struct {
    Rows        []uint32
    Fields      []schema.FieldID
    ValueStarts []uint32
    ValueCounts []uint32
    Values      []schema.SymbolID
}

type Policy struct {
    FieldIDs         []schema.FieldID
    FieldValueStarts []uint32
    FieldValueCounts []uint32
    WildcardMasks    []uint64
    Values           []schema.SymbolID
    ValueMasks       []uint64
    AllMask          []uint64
    RequirementCount uint32
    WordCount        uint32
}

type PolicyBuilder struct {
    // unexported reusable typed scratch
}

func (b *PolicyBuilder) Build(dst *Policy, requirementCount uint32, constraints Constraints) error
func (p Policy) Clone() Policy
```

Build five requirement rows with trust, action, and resource constraints,
including one two-value action constraint and one wildcard requirement. Assert:

- ascending `FieldIDs` and ascending values inside each field;
- exact `FieldValueStarts/Counts`;
- `AllMask` has only bits 0..4 set;
- each wildcard mask contains exactly unconstrained rows for that field;
- each value mask equals wildcard rows OR rows allowing that value;
- reversed constraint-row input produces an exactly equal `Policy`;
- clone slices are exact-capacity and independent.

Add malformed parallel columns, widened bad CSR ranges, zero field/value IDs,
row outside `RequirementCount`, duplicate `(field,row)` constraints, nil
destination/builder, and overflow guards. Every error must be bounded and leave
an existing destination unchanged.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/index -run '^TestPolicyBuild'
```

Expected: compile failure because the policy builder is absent.

**Step 3: Implement canonical bitmap construction**

Perform a validation pass before touching `dst`. Reuse builder scratch to:

1. Copy, sort, and compact distinct FieldIDs.
2. For each field, gather all CSR values, sort, and compact distinct SymbolIDs.
3. Detect duplicate constraint rows with a reusable `[]uint8` row marker.
4. Size output mask slabs as `fieldCount*words` and `valueCount*words` using
   widened multiplication.
5. Initialize `AllMask`, copy it to every wildcard mask, clear constrained row
   bits, set allowed row bits in value masks, then OR the corresponding
   wildcard mask into every value mask.

Tail bits above `RequirementCount` remain zero. Use `slices.Sort` over typed
scratch, no maps, reflection, per-row objects, or string conversion.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/index/policy.go internal/index/policy_test.go
go test -count=1 -timeout 60s ./internal/index -run '^TestPolicyBuild'
GOARCH=386 go test -count=1 -timeout 60s ./internal/index -run '^TestPolicyBuild'
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/index/policy.go internal/index/policy_test.go
git commit -m "feat: build canonical applicability masks"
```

### Task 3: Query Candidates Conservatively

**Files:**
- Modify: `internal/index/policy.go`
- Modify: `internal/index/policy_test.go`

**Step 1: Write failing candidate-query tests**

Add:

```go
func (p Policy) Candidates(
    dst []uint64,
    fields []schema.FieldID,
    values []schema.SymbolID,
    present []uint8,
) error
```

Against the five-row fixture, assert exact candidate bits for:

- known action, resource, and trust selectors together;
- each selector independently;
- a missing trust selector (`present=0`) retaining both trust-constrained rows;
- a present unknown action (`value=0`) retaining only action-wildcard rows;
- a known field with a symbol absent from its value table using the same
  wildcard mask;
- an unindexed known field performing no filtering;
- selector order producing identical masks;
- zero requirements and a non-word-aligned tail.

Malformed destination size or parallel query columns must return
`ErrInvalidQuery` before changing `dst`.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/index -run '^TestPolicyCandidates'
```

Expected: compile failure because `Candidates` is absent.

**Step 3: Implement allocation-free lookup**

Validate query shapes first, copy `AllMask` to `dst`, then for each present
selector:

1. Binary-search sorted `FieldIDs`; skip an unindexed field.
2. Binary-search the field's sorted value range.
3. Select the value mask, or the field wildcard mask when no value matches.
4. AND complete words into `dst` and stop early if every word becomes zero.

Missing selectors perform no lookup or intersection. Do not allocate temporary
masks or construct strings.

**Step 4: Verify GREEN and warm allocations**

Prime one Policy and fixed query slices, then require
`testing.AllocsPerRun(1000, ...) == 0`.

```bash
gofmt -w internal/index/policy.go internal/index/policy_test.go
go test -count=1 -timeout 60s ./internal/index -run '^TestPolicyCandidates'
```

Expected: PASS with zero warm allocations.

**Step 5: Commit**

```bash
git add internal/index/policy.go internal/index/policy_test.go
git commit -m "feat: prune applicability candidates"
```

### Task 4: Store And Freeze Program Indexes

**Files:**
- Modify: `internal/program/program.go`
- Modify: `internal/program/freeze.go`
- Modify: `internal/program/freeze_test.go`
- Modify: `internal/program/program_test.go`

**Step 1: Write failing freeze tests**

Add populated `index.Schema` and `index.Policy` values to the valid Freeze
fixture. Assert that frozen indexes compare equal, every nonempty slice has
exact capacity and distinct backing storage, and source mutation does not alter
the frozen Program. Extend the pointerless-column test through the nested index
structs.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/program -run '^(TestFreezeCopiesIndexes|TestProgramPointerless)'
```

Expected: compile failure because Program has no index fields.

**Step 3: Add Program fields and exact cloning**

Add these pointer-bearing fields before Program's scalar tail:

```go
FieldIndex         index.Schema
ApplicabilityIndex index.Policy
```

Import `internal/index` from Program. This does not cycle because `index`
imports only `schema`. In `Freeze`, call each index's `Clone` before rebuilding
the Resolver. Keep all index slices exact-capacity in the published Program.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/program/program.go internal/program/freeze.go internal/program/freeze_test.go internal/program/program_test.go
go test -count=1 -timeout 60s ./internal/program
```

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/program/program.go internal/program/freeze.go internal/program/freeze_test.go internal/program/program_test.go
git commit -m "feat: freeze compiled indexes"
```

### Task 5: Extract Safe Applicability Constraints

**Files:**
- Create: `internal/compile/index.go`
- Create: `internal/compile/index_test.go`
- Modify: `internal/compile/lower.go`

**Step 1: Write failing extraction tests**

Build direct final-Program fixtures and call a private extraction helper. Cover:

- an `All` root containing action `Equal`, resource `In`, and trust `Equal`;
- an unrelated non-selector assertion that contributes no constraint;
- `Any`, `Not`, `NotEqual`, and non-symbol fields remaining wildcard;
- a duplicate same-field constraint remaining wildcard;
- a shared positive leaf reached once through structural CSE;
- malformed requirement roots, instruction columns, value/list CSR, and symbol
  refs returning `ErrInvalidGeneratedProgram` without panic.

Assert exact pointerless constraint columns in requirement-row then FieldID
order. No test should depend on source NodeIDs or names.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^TestApplicabilityIndexExtract'
```

Expected: compile failure because extraction is absent.

**Step 3: Add reusable compiler scratch and iterative extraction**

Add pointer-bearing scratch before `Lowerer.output`:

```go
indexBuilder         index.PolicyBuilder
indexStack           []schema.InstructionID
indexFieldState      []uint8
indexFieldValueStart []uint32
indexFieldValueCount []uint32
indexConstraintRows  []uint32
indexConstraintField []schema.FieldID
indexConstraintStart []uint32
indexConstraintCount []uint32
indexConstraintValue []schema.SymbolID
```

For each requirement row, seed its applicability root and iteratively process:

- `All`: push operands in reverse so visitation remains deterministic.
- Symbol `Equal`: decode one Program `ValueID -> SymbolID`.
- Symbol `In`: decode the validated ListValues range.
- Everything else: stop that branch.

Use `indexFieldState[field-1]` values unseen/single/ambiguous. A second
constraint for a field marks it ambiguous; emit only single constraints after
the walk by scanning fields in ascending ID order. Reset all active scratch
ranges between requirements and calls.

**Step 4: Verify GREEN**

```bash
gofmt -w internal/compile/index.go internal/compile/index_test.go internal/compile/lower.go
go test -count=1 -timeout 60s ./internal/compile -run '^TestApplicabilityIndexExtract'
GOARCH=386 go test -count=1 -timeout 60s ./internal/compile -run '^TestApplicabilityIndexExtract'
```

Expected: PASS on amd64 and 386.

**Step 5: Commit**

```bash
git add internal/compile/index.go internal/compile/index_test.go internal/compile/lower.go
git commit -m "feat: extract applicability selectors"
```

### Task 6: Integrate Index Construction Into Atomic Lowering

**Files:**
- Modify: `internal/compile/index.go`
- Modify: `internal/compile/lower.go`
- Modify: `internal/compile/lower_api_test.go`
- Modify: `internal/compile/lower_boundaries_test.go`

**Step 1: Write failing integration tests**

Extend public lowering tests to assert:

- `FieldIndex` maps every Program field to the expected kind-local column;
- the fixture applicability selector produces the exact requirement candidate;
- a missing selector leaves every requirement candidate;
- index output is identical across cold and warm Lower calls;
- frozen index slices have exact capacity and survive another Lowerer call and
  source mutation;
- malformed private index construction leaves public `dst` unchanged;
- interleaving a smaller fixture cannot leave stale fields, values, or mask
  words in a later larger result.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/compile -run '^(TestLowerAPI|TestLowerOwnership|TestLowerDeterministic|TestLowerIntegration|TestLowerIndex)'
```

Expected: failure because public lowering does not build indexes.

**Step 3: Build indexes before Freeze**

Add `lowerIndexes` after semantic lowering and slot planning, before Freeze:

```go
if err := l.lowerIndexes(&l.output); err != nil {
    return err
}
```

It calls `index.BuildSchema`, extracts constraints, and calls the reusable
`PolicyBuilder`. Translate `index.ErrIndexTooLarge` to `ErrProgramTooLarge` and
all other impossible generated-data errors to `ErrInvalidGeneratedProgram`.
Reset private index output at the start of instruction lowering so failed later
stages cannot retain stale logical columns. Public `dst` remains unchanged until
the exact frozen Program succeeds.

**Step 4: Verify GREEN and warm builder reuse**

Prime `lowerIndexes` on a direct Program, then require zero allocations from
repeated calls at unchanged capacity. Program Freeze allocations are outside
this assertion because publication intentionally transfers exact ownership.

```bash
gofmt -w internal/compile/index.go internal/compile/lower.go internal/compile/lower_api_test.go internal/compile/lower_boundaries_test.go
go test -count=1 -timeout 60s ./internal/index ./internal/program ./internal/compile
GOARCH=386 go test -count=1 -timeout 60s ./internal/index ./internal/program ./internal/compile
```

Expected: PASS with zero warm index-builder allocations.

**Step 5: Commit**

```bash
git add internal/compile/index.go internal/compile/lower.go internal/compile/lower_api_test.go internal/compile/lower_boundaries_test.go
git commit -m "feat: publish applicability indexes"
```

### Task 7: Run Cross-Cutting Verification And Review

**Files:**
- Modify only files required by confirmed findings.

**Step 1: Run the complete bounded gate set**

```bash
go test -count=1 -timeout 60s ./internal/index ./internal/program ./internal/compile ./internal/truth
go test -race -count=1 -timeout 120s ./internal/index ./internal/program ./internal/compile ./internal/truth
GOARCH=386 go test -count=1 -timeout 60s ./internal/index ./internal/program ./internal/compile ./internal/truth
go test -count=1 -timeout 60s ./...
timeout 120s go vet ./...
timeout 120s go build ./cmd/verifoxx
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/index ./internal/program ./internal/compile
timeout 120s go build -gcflags=-m ./internal/index ./internal/program ./internal/compile
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
git diff --check
git status --short --branch
```

Expected: all commands exit zero; analyzer, formatting, module, and diff checks
print nothing. Escape output may show bulk retained scratch and frozen ownership,
but candidate lookup and warm building must not allocate per requirement,
selector, or mask word.

**Step 2: Review the Task 12 commit range**

Review for false-negative pruning, missing-versus-unknown confusion, tail-bit
leaks, noncanonical ordering, CSR overflow, duplicate constraints, stale mask
words, zero-ID handling, 32-bit narrowing, Program/index import cycles, frozen
borrowing, warm allocations, and Task 13/20 scope creep. Fix every confirmed
Critical or Important issue through a new focused RED/GREEN cycle, then repeat
Step 1.

**Step 3: Commit review fixes only when needed**

```bash
git add internal/index internal/program internal/compile
git commit -m "fix: harden applicability index boundaries"
```
