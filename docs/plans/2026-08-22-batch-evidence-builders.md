# Batch And Evidence Builders Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build reusable request and evidence struct-of-arrays buffers with typed setters, CSR links, and batch-local symbol identity.

**Architecture:** `eval.Builder` borrows an immutable compiled `program.Program`, sizes one owned `Batch` from exact counts, and exposes validated setters over column-major and bit-packed storage. `Finish` returns a borrowed view valid until the builder's next successful `Begin`; all active ranges and bitmap tails are cleared on reuse.

**Tech Stack:** Go 1.27, typed slices, fixed-width IDs, open-addressed `schema.Interner`, CSR offsets/references, `testing.AllocsPerRun`, standard Go benchmarks.

---

### Task 1: Define Batch And Evidence Storage

**Files:**
- Create: `internal/eval/batch.go`
- Create: `internal/eval/evidence.go`
- Test: `internal/eval/builder_test.go`

**Step 1: Write the failing layout tests**

Add tests that construct a four-field `index.Schema`, begin three rows, and
assert exact lengths for symbol, integer, timestamp, Boolean, presence,
request-ID, evidence, and CSR slabs. Assert an empty batch has no value words
and exactly one zero CSR offset.

**Step 2: Run the tests to verify RED**

Run: `go test -timeout 60s ./internal/eval`

Expected: FAIL because package `internal/eval` and `Batch` do not exist.

**Step 3: Add the pointer-grouped SoA types**

Create `Batch` with parallel value slices, request IDs, evidence CSR slices,
an `EvidenceBatch`, and a scalar `Rows` tail. Create `EvidenceBatch` with the
eight parallel evidence columns from the design. Add safe `WordCount`,
`Present`, `Boolean`, and `EvidenceRange` read helpers.

Representative shape:

```go
type Batch struct {
	SymbolValues    []schema.SymbolID
	IntegerValues   []int64
	TimestampValues []int64
	BooleanValues   []uint64
	PresenceMasks   []uint64
	RequestIDs      []schema.RequestID
	EvidenceOffsets []uint32
	EvidenceRefs    []uint32
	Evidence        EvidenceBatch
	Rows            uint32
}
```

**Step 4: Add the minimal builder sizing path**

Create `internal/eval/builder.go` with sentinel errors, `Builder.Begin`, schema
validation, checked `uint64` products, and clear-and-reuse helpers. Validate the
entire shape before mutating builder state. Resize each active slab exactly and
clear it, including final bitmap words.

**Step 5: Run the tests to verify GREEN**

Run: `go test -timeout 60s ./internal/eval`

Expected: PASS.

**Step 6: Commit**

```bash
git add internal/eval/batch.go internal/eval/evidence.go internal/eval/builder.go internal/eval/builder_test.go
git commit -m "feat: size soa request batches"
```

### Task 2: Populate Typed Fact Columns

**Files:**
- Modify: `internal/eval/builder.go`
- Modify: `internal/eval/builder_test.go`

**Step 1: Write failing typed-setter tests**

Test multiple same-kind fields so the second kind-local column proves the
`column*rows+row` calculation. Cover symbol, integer zero, timestamp, Boolean
true and false, and presence-only fields. Assert each successful setter also
sets the FieldID presence plane.

Add boundary tests for invalid rows, zero/out-of-range fields, mismatched value
kinds, zero symbols, calls before `Begin`, and calls after `Finish`. Snapshot
the active slabs and prove each rejected setter leaves them unchanged.

**Step 2: Run the focused tests to verify RED**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilder(Set|Reject)'`

Expected: FAIL because typed setters do not exist.

**Step 3: Implement typed setters**

Add:

```go
func (b *Builder) SetRequestID(row uint32, id schema.RequestID) error
func (b *Builder) SetSymbol(row uint32, field schema.FieldID, value schema.SymbolID) error
func (b *Builder) SetInteger(row uint32, field schema.FieldID, value int64) error
func (b *Builder) SetTimestamp(row uint32, field schema.FieldID, value int64) error
func (b *Builder) SetBoolean(row uint32, field schema.FieldID, value bool) error
func (b *Builder) SetPresent(row uint32, field schema.FieldID) error
```

Validate active state, row, field, and kind before calculating any index or
writing. Store scalar facts in column-major slices; store Boolean values and
all presence in bitplanes.

**Step 4: Run the focused and package tests**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilder(Set|Reject)'`

Run: `go test -timeout 60s ./internal/eval`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/builder.go internal/eval/builder_test.go
git commit -m "feat: populate typed batch columns"
```

### Task 3: Populate Evidence And CSR Links

**Files:**
- Modify: `internal/eval/evidence.go`
- Modify: `internal/eval/builder.go`
- Modify: `internal/eval/builder_test.go`

**Step 1: Write failing evidence tests**

Populate several evidence rows and a relation where requests have zero, one,
and multiple records. Assert every evidence column and every
`Batch.EvidenceRange`. Cover malformed offset length, nonzero first offset,
decreasing offsets, wrong final offset, and out-of-range references.

Install a valid CSR relation, submit each malformed relation, and assert the
valid destination remains byte-for-byte unchanged.

**Step 2: Run focused tests to verify RED**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilderEvidence|TestEvidenceRange'`

Expected: FAIL because evidence setters do not exist.

**Step 3: Implement evidence writes**

Add `EvidenceRecord`, `SetEvidence`, and `SetEvidenceCSR`. Require nonzero
record ID, kind, and state. Validate the entire source CSR before copying into
the already-sized destination. Keep references as zero-based evidence-row
indices.

**Step 4: Run focused and package tests**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilderEvidence|TestEvidenceRange'`

Run: `go test -timeout 60s ./internal/eval`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/evidence.go internal/eval/builder.go internal/eval/builder_test.go
git commit -m "feat: build evidence csr columns"
```

### Task 4: Add Batch-Local Extension Symbols

**Files:**
- Modify: `internal/eval/builder.go`
- Modify: `internal/eval/builder_test.go`

**Step 1: Write failing symbol identity tests**

Build a Program with one frozen symbol. Assert `InternSymbol` returns its
Program `SymbolID`, assigns consecutive IDs above `ProgramSymbolCount` to two
unknown values, deduplicates equal unknown bytes across fact/evidence use, and
returns the same deterministic extension IDs after the next `Begin`.

Test nil/malformed Programs, inactive builder calls, and extension-ID overflow.
Assert failed `Begin` retains the previous batch and extension identities.
Resolve Program and extension IDs through the builder after `Finish`, and
prove the next successful `Begin` invalidates prior extension bytes.

**Step 2: Run focused tests to verify RED**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilderIntern|TestBuilderBeginFailure'`

Expected: FAIL because `InternSymbol` does not exist.

**Step 3: Implement reusable extension interning**

Store a zero-value-compatible `schema.Interner` in the Builder. Reset it only
after all `Begin` validation succeeds. Probe `Program.LookupSymbol` first;
otherwise check `ProgramSymbolCount + localID` in `uint64` and publish the
combined `schema.SymbolID`. Validate a newly bound Program's frozen symbol
range before reserving extension IDs, and expose `Builder.Symbol` for the
owning context to resolve either namespace.

**Step 4: Run focused and package tests**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilderIntern|TestBuilderBeginFailure'`

Run: `go test -timeout 60s ./internal/eval`

Expected: PASS.

**Step 5: Commit**

```bash
git add internal/eval/builder.go internal/eval/builder_test.go
git commit -m "feat: intern batch extension symbols"
```

### Task 5: Publish Complete Batches And Prove Reuse

**Files:**
- Modify: `internal/eval/builder.go`
- Modify: `internal/eval/builder_test.go`
- Create: `internal/eval/builder_bench_test.go`

**Step 1: Write failing completion and poison tests**

Require `Finish` to reject zero request IDs, zero required evidence IDs/kinds/
states, and incomplete CSR while leaving the builder active for correction.
Assert successful `Finish` seals setters until the next `Begin`.

Build 130 poisoned rows with every bit set, then rebuild 65 rows and assert all
active scalar cells are zero, all absent presence/Boolean bits are clear, and
all bits above row 64 in the final word remain zero. Compare capacities before
and after a smaller rebuild.

**Step 2: Run focused tests to verify RED**

Run: `go test -timeout 60s ./internal/eval -run 'TestBuilder(Finish|Reuse|Tail)'`

Expected: FAIL until lifecycle validation and clearing are complete.

**Step 3: Implement `Finish` and lifecycle checks**

Scan only required ID columns and fixed CSR metadata. On success, mark the
builder inactive and return its `Batch` by value. Do not clone any slice.

**Step 4: Add the warm benchmark and allocation assertion**

Benchmark repeated `Begin`, typed writes, evidence writes, CSR copy, symbol
interning, and `Finish`. Prime capacities and both known/unknown symbol paths
before measurement. Add `testing.AllocsPerRun` expecting zero allocations for
the warmed fixed shape.

**Step 5: Run tests and benchmark**

Run: `go test -timeout 60s ./internal/eval`

Run: `go test -timeout 120s -run '^$' -bench=BatchBuilder -benchmem ./internal/eval`

Expected: PASS and `0 B/op`, `0 allocs/op` for the warm benchmark.

**Step 6: Commit**

```bash
git add internal/eval/builder.go internal/eval/builder_test.go internal/eval/builder_bench_test.go
git commit -m "test: prove batch builder reuse"
```

### Task 6: Run Portability And Repository Gates

**Files:**
- Modify only files required by a failing gate.

**Step 1: Format and inspect modules**

Run: `gofmt -w internal/eval/*.go`

Run: `timeout 60s go mod tidy -diff`

Expected: no output from the module check.

**Step 2: Run race and 386 coverage**

Run: `go test -timeout 60s -race ./internal/eval`

Run: `GOARCH=386 go test -timeout 60s ./internal/eval`

Expected: PASS.

**Step 3: Run repository correctness gates**

Run: `go test -timeout 60s ./...`

Run: `go vet -timeout 60s ./...`

Run: `timeout 60s go build ./cmd/nornrune`

Expected: PASS.

**Step 4: Inspect layout and escapes**

Run: `timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/eval`

Run: `timeout 120s go build -gcflags=-m ./internal/eval`

Expected: no field-alignment finding; builder-owned backing arrays may escape
at growth points, while the warm benchmark remains allocation-free.

**Step 5: Check and commit any gate-only corrections**

Run: `git diff --check`

If a gate required changes:

```bash
git add internal/eval
git commit -m "fix: harden soa batch boundaries"
```

Otherwise leave the worktree clean.
