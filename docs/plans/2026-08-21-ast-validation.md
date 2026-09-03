# AST Validation Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add deterministic, allocation-free-on-reuse structural, semantic, cycle, and reachability validation for any `ast.Document` before lowering.

**Architecture:** A reusable `compile.Validator` appends stable diagnostics into caller-supplied storage. It executes three explicit phases: bounds-safe structural checks, semantic checks over safe rows, and iterative graph checks using reusable byte state and frame stacks.

**Tech Stack:** Go 1.27, existing `internal/ast` and `internal/schema` SoA/CSR types, standard library only.

---

## Invariants

- Keep `jsonpolicy.Error` and compile diagnostics separate.
- Never mutate `ast.Document` or `schema.Schema`.
- Never index a public AST column from an unchecked peer column.
- Return every independent diagnostic in deterministic order.
- Do not use maps, reflection, recursion, interface trees, `fmt`, or `sync.Pool`.
- A zero-value `Validator` works. It is not safe for concurrent use.
- Warm validation of a valid document with adequate capacities is `0 B/op`, `0 allocs/op`.
- Every test, build, benchmark, and escape command has an explicit timeout.
- Do not commit unless the user explicitly requests it.

## Milestone 1: Stable Diagnostic API

**Files:**
- Create: `internal/compile/diagnostic.go`
- Create: `internal/compile/diagnostic_test.go`
- Create: `internal/compile/validate.go`
- Create: `internal/compile/validate_test.go`

### Step 1: Write failing diagnostic tests

Test that every non-zero code and table kind is valid, has a unique fixed
string, zero and out-of-range values return `"invalid"`, table/row context is
retained, and strong-ID fields remain distinct. Lock the pointer-free
`Diagnostic` layout at 52 bytes with 4-byte alignment.

Use this bounded API:

```go
type DiagnosticCode uint8

const (
    CodeInvalidDocument DiagnosticCode = iota + 1
    CodeColumnLength
    CodeInvalidSourceSpan
    CodeInvalidNodeKind
    CodeInvalidPayloadRef
    CodeInvalidCSRRange
    CodeInvalidNodeReference
    CodeInvalidField
    CodeInvalidValue
    CodeTypeMismatch
    CodeInvalidArity
    CodeInvalidEvidence
    CodeInvalidOutcome
    CodeMissingResolution
    CodeInvalidRemediation
    CodeDuplicateID
    CodeDuplicateName
    CodeCycle
    CodeUnreachableNode
    CodeInvalidID
)

type Diagnostic struct {
    Code        DiagnosticCode
    Table       TableKind
    Member      MemberKind
    Row         uint32
    Span        ast.SourceSpan
    Node        schema.NodeID
    Clause      schema.ClauseID
    Requirement schema.RequirementID
    Field       schema.FieldID
    Value       schema.ValueID
    Outcome     schema.OutcomeID
    Remediation schema.RemediationID
    EvidenceKind schema.EvidenceKindID
    EvidenceState schema.EvidenceStateID
}
```

`TableKind` is an append-only `uint8` enum for document, node, compare, group,
not, evidence-node, value, evidence-kind, evidence-state, outcome,
remediation, clause, and requirement tables. It has fixed `Valid()` and
`String()` methods. `Row` is one-based and zero only when no row exists.
`MemberKind` is an append-only discriminator for row fields and resolution
slots; it occupies an existing padding byte. `Diagnostic` remains pointer-free
at 52 bytes with 4-byte alignment.

### Step 2: Verify the red test

Run:

```bash
go test -count=1 -timeout 60s ./internal/compile
```

Expected: build failure because the package/API does not exist.

### Step 3: Implement `DiagnosticCode`

Use a fixed string array indexed by `code-1`. Do not add a free-form message
field. Add `Valid()` and `String()` with bounds checks.

### Step 4: Add the validator shell

```go
type visitFrame struct {
    node schema.NodeID
    next uint32
}

type Validator struct {
    nodeState   []uint8
    clauseState []uint8
    stack       []visitFrame
}

func Validate(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
    var validator Validator
    return validator.Validate(dst, doc, fields)
}

func (v *Validator) Validate(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
    if doc == nil || fields == nil {
        return append(dst, Diagnostic{Code: CodeInvalidDocument, Table: TableDocument})
    }
    return dst
}
```

Document that package-level `Validate` is the cold convenience path.

### Step 5: Add valid canonical-policy setup

Tests may not import jsonpolicy test helpers. Build a small field schema and
interner locally, decode `testdata/policies/valid-full.json` through
`jsonpolicy.Decode`, then assert `Validator.Validate(dst[:0], doc, fields)`
returns no diagnostics.

### Step 6: Verify milestone 1

```bash
go test -count=1 -timeout 60s ./internal/compile
go test -count=1 -timeout 60s ./...
timeout 30s gofmt -l internal/compile
```

Expected: all pass; formatter output empty.

## Milestone 2: Bounds-Safe Structural Phase

**Files:**
- Modify: `internal/compile/validate.go`
- Modify: `internal/compile/validate_test.go`

### Step 1: Write structural mutation tests

For each case decode/build a fresh valid document, mutate exactly one column,
and assert the exact code and ID without a panic:

- nil document or schema;
- mismatched `NodeKinds`, `NodeRefs`, `SourceStarts`, `SourceEnds` lengths;
- invalid node kind;
- compare/group/not/evidence payload ref out of range;
- mismatched payload-table parallel columns;
- group, `In`, requirement-clause, clause-evidence, and
  clause-remediation CSR range overflow/out of bounds;
- zero or out-of-range child/node/value/field/catalog/outcome/clause/
  remediation IDs;
- invalid node, catalog, outcome, remediation, clause, and requirement spans;
- mismatched requirement, clause, catalog, outcome, remediation, and value
  parallel columns.

Add a multi-corruption test that locks fixed table-order diagnostics.

### Step 2: Verify red tests

```bash
go test -count=1 -timeout 60s -run 'TestValidateStructural' ./internal/compile
```

Expected: failures because structural checks are absent.

### Step 3: Add reusable state sizing

Resize only when capacity is insufficient, then clear the active range:

```go
func resizeBytes(dst []byte, n int) []byte {
    if cap(dst) < n {
        return make([]byte, n)
    }
    dst = dst[:n]
    clear(dst)
    return dst
}
```

Size node state from `len(doc.NodeKinds)` and clause state from
`len(doc.ClauseAssertionRoots)`. Define an unsafe-row bit. Preserve capacity
between calls.

### Step 4: Implement column preflight

Check every parallel-table length in a documented fixed order. Append one
`CodeColumnLength` per mismatched table, not one per missing cell. Later scans
use the minimum safe row count for that table.

### Step 5: Implement safe row and CSR checks

For each top-level node row:

1. Validate kind, source span, and payload ref before indexing payload columns.
2. Validate payload-specific CSR range with subtraction-safe bounds checks:
   `start <= total && count <= total-start`.
3. Validate every referenced strong ID before following it.
4. Mark the node unsafe when graph traversal cannot safely use its payload.

Apply the same pattern to value, catalog, outcome, remediation, clause, and
requirement rows. A corrupt span uses zero span in its own diagnostic.

### Step 6: Verify milestone 2

```bash
go test -count=1 -timeout 60s ./internal/compile
go test -count=1 -timeout 60s ./...
timeout 120s go vet ./internal/compile
```

Expected: all pass and no panic in any corrupt-column case.

## Milestone 3: Semantic Phase

**Files:**
- Modify: `internal/compile/validate.go`
- Modify: `internal/compile/validate_test.go`

### Step 1: Write semantic tests

Cover these exact rules:

- `Exists`: field is valid, scalar value is zero, list count is zero;
- `In`: field is not presence, scalar value is zero, list is non-empty, every
  list value kind equals field kind;
- scalar compares: one non-zero value matching field kind and no list values;
- ordered comparisons (`less`, `less_equal`, `greater`, `greater_equal`) only
  accept integer or timestamp fields;
- `All` and `Any` have at least one child;
- `Not` has one valid child;
- evidence nodes reference valid kind/state rows;
- catalog and outcome names are symbol values and names are unique by bytes;
- requirement IDs are non-zero and unique, applicability roots are valid, and
  clause ranges are non-empty;
- clause assertions are valid, clause evidence rows point only to
  `NodeKindEvidence`, and all seven outcome IDs are non-zero and valid;
- set-field remediation has field/value only and matching kinds;
- add-evidence remediation has evidence kind only;
- invalid remediation kind is diagnosed.

Add a test with several independent defects and assert accumulation and order.

### Step 2: Verify red tests

```bash
go test -count=1 -timeout 60s -run 'TestValidateSemantic' ./internal/compile
```

Expected: semantic cases fail because only structural checks exist.

### Step 3: Implement semantic helpers

Keep helpers allocation-free and typed:

```go
func ordered(kind schema.ValueKind) bool {
    return kind == schema.ValueKindInteger || kind == schema.ValueKindTimestamp
}
```

Use AST accessors only after structural safety is established. For duplicate
names and requirement IDs, use deterministic linear scans; decoded limits keep
these policy-load scans bounded and maps would violate the layout contract.

### Step 4: Preserve independent diagnostics

Do not stop after the first semantic error. Skip only checks that require an
already-invalid row/ref. Avoid cascades such as reporting a type mismatch for
a value ID already known to be out of range.

### Step 5: Verify milestone 3

```bash
go test -count=1 -timeout 60s ./internal/compile
go test -count=1 -timeout 60s ./...
timeout 120s go build -gcflags=-m ./internal/compile
```

Expected: all tests pass; only diagnostic growth and first-use reusable scratch
may escape, with no per-row object allocation.

## Milestone 4: Iterative Cycle And Reachability Phase

**Files:**
- Modify: `internal/compile/validate.go`
- Modify: `internal/compile/validate_test.go`

### Step 1: Write graph tests

Construct or mutate documents for:

- self-cycle through `Not`;
- two-node cycle through group/not edges;
- cycle in a graph reachable from a requirement;
- cycle in an otherwise unreachable component;
- orphan leaf and orphan nested subtree;
- nodes reachable through requirement applicability;
- nodes reachable through assertions/evidence of clauses referenced by a
  requirement;
- clause not referenced by any requirement, whose nodes remain unreachable;
- invalid edges skipped without panic or duplicate graph diagnostics;
- deterministic cycle and unreachable ordering.

### Step 2: Verify red tests

```bash
go test -count=1 -timeout 60s -run 'TestValidateGraph' ./internal/compile
```

Expected: graph cases fail because traversal is absent.

### Step 3: Implement reachability roots

Walk requirements in row order. Mark clauses in each valid requirement CSR
range. Seed requirement applicability roots, then seed assertion/evidence roots
only for marked clauses. Do not seed every clause globally.

### Step 4: Implement iterative tri-color DFS

Use state bits for unsafe, white/gray/black, and reachable. Stack frames retain
the next outgoing edge index. On a gray target append `CodeCycle` for the
source edge. Skip invalid/unsafe targets already diagnosed structurally.

Process semantic roots first with the reachable bit, then scan all remaining
nodes in ascending ID to find cycles in orphan components. Finally append one
`CodeUnreachableNode` per node without the reachable bit, ascending by ID.

### Step 5: Verify milestone 4

```bash
go test -count=1 -timeout 60s ./internal/compile
go test -race -count=1 -timeout 120s ./internal/compile
go test -count=1 -timeout 60s ./...
```

Expected: all pass.

## Milestone 5: Reuse Benchmarks And Final Verification

**Files:**
- Create: `internal/compile/validate_bench_test.go`
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine-design.md`

### Step 1: Add benchmark document builder

Build valid documents with 16, 128, 1,024, and 8,192 nodes. Make every node
reachable from requirement/clause roots, size all AST columns before timing,
and prime the validator and diagnostic destination once.

### Step 2: Add fresh and reuse benchmarks

```go
func BenchmarkValidateReuse(b *testing.B) {
    // sub-benchmarks by node count
    // ReportAllocs and report ns/node.
}
```

Consume the returned diagnostic length so validation cannot be eliminated.
Valid documents must produce zero diagnostics.

### Step 3: Run isolated benchmark

```bash
go test -timeout 60s -run '^$' -bench 'BenchmarkValidate' -benchmem -benchtime=500ms ./internal/compile
```

Expected: warm cases report `0 B/op`, `0 allocs/op`. Record actual ns/node;
do not claim a latency target before measurement.

### Step 4: Run bounded fuzz-style corrupt-column smoke test

Add a deterministic test that truncates each public column at every boundary
used by the canonical document and calls validation. It must never panic. A
full randomized validator fuzz target remains Task 46.

### Step 5: Run final controller verification

```bash
go test -count=1 -timeout 60s ./internal/compile
go test -count=1 -timeout 60s ./...
go test -race -count=1 -timeout 120s ./internal/compile
timeout 120s go vet ./...
timeout 120s go build ./cmd/nornrune
timeout 120s go build -gcflags=-m ./internal/compile
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
```

Expected: all commands pass; formatting and module-diff commands print
nothing. Review every changed line before marking Task 7 complete.

### Step 6: Update measured architecture records

Record only the isolated benchmark numbers and confirmed allocation behavior
in the main design. Do not add speculative optimization or fuse passes unless
measurement shows policy-load validation is material.
