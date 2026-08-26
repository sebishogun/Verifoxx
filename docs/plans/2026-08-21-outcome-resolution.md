# Outcome Resolution Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add allocation-free reason masks and policy-defined outcome, remediation, and resolution tables that distinguish uncertainty causes and select deterministic results.

**Architecture:** Engine reasons are fixed one-based IDs represented by a scalar `uint16` mask. Policy-owned outcomes, bounded remediations, and fixed nine-row resolution blocks use non-owning structure-of-arrays views. `NewResolver` validates immutable tables once; `Resolve` performs a stable nine-reason scan without maps, strings, callbacks, or allocation.

**Tech Stack:** Go 1.27, typed IDs from `internal/schema`, `internal/truth`, standard-library tests

---

Read `docs/plans/2026-08-21-outcome-resolution-design.md` before implementation.
Invoke `@superpowers:test-driven-development` before changing production code.
Every test and build command below has an explicit timeout. Do not commit unless
the user explicitly requests it; commit commands are checkpoints only.

### Task 1: Define The Nine Reason Bits

**Files:**
- Create: `internal/truth/reason.go`
- Create: `internal/truth/reason_test.go`

**Step 1: Write the failing reason-mask tests**

Create tests that enumerate, in order:

```go
[]schema.ReasonID{
    ReasonMissing,
    ReasonStale,
    ReasonUnclear,
    ReasonUnverifiable,
    ReasonWrongScope,
    ReasonWrongSubject,
    ReasonWrongTiming,
    ReasonInvalid,
    ReasonConflict,
}
```

Assert that:

- The IDs are exactly 1 through 9.
- `ReasonBit` returns nine unique one-hot bits.
- Repeated `With` calls produce `AllReasonsMask`.
- `Has` recognizes exactly the inserted reasons.
- Zero and IDs above `ReasonConflict` produce no bit and are never present.
- `ReasonMask(1 << ReasonCount)` is invalid while zero and
  `AllReasonsMask` are valid.

**Step 2: Run the focused test to verify RED**

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestReason'
```

Expected: FAIL because the reason constants and mask API do not exist.

**Step 3: Add the minimal reason representation**

Create `internal/truth/reason.go`:

```go
package truth

import "github.com/sebishogun/nornrune/internal/schema"

const ReasonCount = 9

const (
	ReasonMissing schema.ReasonID = iota + 1
	ReasonStale
	ReasonUnclear
	ReasonUnverifiable
	ReasonWrongScope
	ReasonWrongSubject
	ReasonWrongTiming
	ReasonInvalid
	ReasonConflict
)

type ReasonMask uint16

const AllReasonsMask ReasonMask = 1<<ReasonCount - 1

func ReasonBit(reason schema.ReasonID) ReasonMask {
	if reason < ReasonMissing || reason > ReasonConflict {
		return 0
	}
	return 1 << (reason - 1)
}

func (mask ReasonMask) With(reason schema.ReasonID) ReasonMask {
	return mask | ReasonBit(reason)
}

func (mask ReasonMask) Has(reason schema.ReasonID) bool {
	bit := ReasonBit(reason)
	return bit != 0 && mask&bit != 0
}

func (mask ReasonMask) Valid() bool {
	return mask&^AllReasonsMask == 0
}
```

Add concise comments explaining that `ReasonInvalid` means invalid evidence,
not the zero invalid ID.

**Step 4: Verify GREEN**

```bash
go test -count=1 -timeout 60s ./internal/truth -run '^TestReason'
go test -count=1 -timeout 60s ./internal/truth
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/truth/reason.go internal/truth/reason_test.go
git commit -m "feat: add uncertainty reason masks"
```

### Task 2: Add The Policy-Defined Outcome Table

**Files:**
- Create: `internal/result/outcome.go`
- Create: `internal/result/resolution_test.go`

**Step 1: Write failing lookup and precedence tests**

Use a table with four arbitrary symbol IDs and the NornRune fixture ordering:

```text
OutcomeID 1: precedence 1, terminal true
OutcomeID 2: precedence 4, terminal true
OutcomeID 3: precedence 2, terminal false
OutcomeID 4: precedence 3, terminal true
```

Test `Lookup` for all rows, zero, and out-of-range IDs. Test `Prefer` for:

- Zero plus a valid candidate.
- Higher numeric precedence in either argument order.
- Equal IDs.
- Equal precedence with the lower `OutcomeID` winning regardless of argument
  order.
- Invalid nonzero IDs returning `ok=false`.

The tests use only numeric IDs; do not branch on decision labels.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestOutcome'
```

Expected: FAIL because `OutcomeTable`, `Outcome`, `Lookup`, and `Prefer` do not
exist.

**Step 3: Implement the non-owning SoA table**

Create `internal/result/outcome.go` with:

```go
type OutcomeTable struct {
	Names      []schema.SymbolID
	Precedence []uint8
	Terminal   []bool
}

type Outcome struct {
	Name       schema.SymbolID
	Precedence uint8
	Terminal   bool
}
```

Implement:

- `Lookup(id schema.OutcomeID) (Outcome, bool)` with one-based indexing and
  bounds checks against every column.
- `Prefer(current, candidate schema.OutcomeID) (schema.OutcomeID, bool)`.
- An unexported constant-time `preferKnown` for the validated resolver path.
- An unexported `valid` method that requires equal non-empty columns and
  nonzero name IDs.

`Prefer` treats zero as no candidate, chooses higher precedence, and uses lower
`OutcomeID` for an equal-precedence tie. It does not use `Terminal` to
short-circuit.

**Step 4: Verify GREEN**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestOutcome'
go test -count=1 -timeout 60s ./internal/result
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/result/outcome.go internal/result/resolution_test.go
git commit -m "feat: add policy outcome table"
```

### Task 3: Add Bounded Remediation Rows

**Files:**
- Create: `internal/result/remediation.go`
- Modify: `internal/result/resolution_test.go`

**Step 1: Write failing remediation tests**

Build a two-row table:

1. Set `context.usage` to `standard` using nonzero `FieldID` and `ValueID`.
2. Request one allowed usage-adjustment `EvidenceKindID`.

Test exact lookup records, zero/out-of-range IDs, kind validity, and the payload
rules:

- Set-field requires field/value and forbids evidence kind.
- Add-evidence requires evidence kind and forbids field/value.
- Invalid kinds and malformed payloads make the table invalid.
- An empty, column-aligned table remains valid for policies with no
  remediation.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestRemediation'
```

Expected: FAIL because the remediation API does not exist.

**Step 3: Implement the runtime remediation table**

Create `internal/result/remediation.go` with:

```go
type RemediationKind uint8

const (
	RemediationInvalid RemediationKind = iota
	RemediationSetField
	RemediationAddEvidence
)

type RemediationTable struct {
	Kinds         []RemediationKind
	Fields        []schema.FieldID
	Values        []schema.ValueID
	EvidenceKinds []schema.EvidenceKindID
}

type Remediation struct {
	Kind         RemediationKind
	Field        schema.FieldID
	Value        schema.ValueID
	EvidenceKind schema.EvidenceKindID
}
```

Add `RemediationKind.Valid`, `RemediationTable.Lookup`, and an unexported
`RemediationTable.valid`. Storage stays in the four parallel slices; the
returned record is a stack value.

**Step 4: Verify GREEN**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestRemediation'
go test -count=1 -timeout 60s ./internal/result
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/result/remediation.go internal/result/resolution_test.go
git commit -m "feat: add bounded remediation table"
```

### Task 4: Validate Immutable Resolution Tables Once

**Files:**
- Create: `internal/result/resolution.go`
- Modify: `internal/result/resolution_test.go`

**Step 1: Add valid and malformed constructor tests**

Define a one-rule-set fixture with exactly `truth.ReasonCount` rows. Add a
table-driven test that independently corrupts:

- Outcome column lengths and a zero outcome name.
- Remediation column lengths, kind, and payload shape.
- Resolution column lengths and a row count not divisible by `ReasonCount`.
- A zero and an out-of-range outcome reference.
- A CSR range beyond `RemediationIDs`.
- A zero and an out-of-range remediation edge.

Each malformed fixture must return the corresponding static constructor error;
the valid fixture must construct without copying or mutating any input slice.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestNewResolver'
```

Expected: FAIL because the resolution table and constructor do not exist.

**Step 3: Add the table and one-time validator**

Create `internal/result/resolution.go` with:

```go
type RuleSetID uint32

type ResolutionTable struct {
	OutcomeIDs        []schema.OutcomeID
	RemediationStarts []uint32
	RemediationCounts []uint16
	RemediationIDs    []schema.RemediationID
}

type Resolver struct {
	outcomes     OutcomeTable
	remediations RemediationTable
	rules        ResolutionTable
}
```

Define static errors:

```go
ErrInvalidOutcomeTable
ErrInvalidRemediationTable
ErrInvalidResolutionTable
ErrInvalidOutcomeReference
ErrInvalidRemediationReference
```

Implement `NewResolver(outcomes, remediations, rules) (Resolver, error)` to:

1. Validate outcome and remediation table shapes/payloads.
2. Require non-empty, equal resolution row columns whose row count is a
   multiple of `truth.ReasonCount`.
3. Validate every nonzero outcome reference through `OutcomeTable.Lookup`.
4. Validate every CSR range with widened arithmetic.
5. Validate every remediation edge through `RemediationTable.Lookup`.
6. Return borrowed slice headers without copying data.

Every rule row requires a nonzero outcome. Empty remediation tables and empty
ranges remain valid.

**Step 4: Verify GREEN**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestNewResolver'
go test -count=1 -timeout 60s ./internal/result
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/result/resolution.go internal/result/resolution_test.go
git commit -m "feat: validate outcome resolution tables"
```

### Task 5: Resolve Reasons By Policy Precedence

**Files:**
- Modify: `internal/result/resolution.go`
- Modify: `internal/result/resolution_test.go`

**Step 1: Add the full behavior tests**

Use outcome IDs in catalogue order Approve, Reject, Revise, Escalate with
precedence 1, 4, 2, 3. Build one nine-row rule set where Missing selects Revise
with remediation IDs 1 and 2, and the other unresolved reasons select
Escalate with empty remediation ranges.

Add tests for:

- Every one of the nine one-hot reason masks returning the same driver
  `ReasonID` that was set.
- Missing returning Revise, terminal=false, and both bounded remediation IDs.
- Lookup of remediation 1 as set-usage-to-standard and remediation 2 as an
  allowed usage-adjustment evidence request.
- Stale returning Escalate, terminal=true, with no remediation.
- Missing plus stale selecting Escalate under precedence 3 > 2.
- A second policy table with Revise precedence 5 selecting Revise for the same
  mask, proving precedence is policy-defined.
- Equal-precedence different outcomes selecting lower `OutcomeID` regardless
  of reason order.
- The same outcome driven by multiple reasons retaining lower `ReasonID`.
- Empty mask returning `(Resolution{}, false)`.
- Invalid high reason bits and zero/out-of-range `RuleSetID` panicking before
  reading rule rows.

**Step 2: Verify RED**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestResolve'
```

Expected: FAIL because `Resolution` and `Resolver.Resolve` do not exist.

**Step 3: Implement the fixed nine-reason reduction**

Add:

```go
type Resolution struct {
	Outcome      schema.OutcomeID
	Reason       schema.ReasonID
	Terminal     bool
	Remediations []schema.RemediationID
}
```

`Resolve(ruleSet RuleSetID, reasons truth.ReasonMask) (Resolution, bool)` must:

- Return false for an empty valid mask.
- Panic with static strings for invalid mask bits or invalid rule-set IDs.
- Calculate the block base once.
- Scan reason IDs 1 through 9 in ascending order.
- Compare candidate precedence directly from the already validated table.
- Replace on greater precedence or equal precedence with lower `OutcomeID`.
- Keep the first reason when candidate and current outcome IDs are equal.
- Slice the winning remediation range once after the scan.
- Return borrowed IDs and terminal metadata without allocation.

Do not add strings, maps, interfaces, function values, or per-reason objects.

**Step 4: Verify GREEN**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^TestResolve'
go test -count=1 -timeout 60s ./internal/result
go test -count=1 -timeout 60s ./internal/truth ./internal/result
```

Expected: PASS.

**Step 5: Commit only when requested**

```bash
git add internal/result/resolution.go internal/result/resolution_test.go
git commit -m "feat: resolve uncertainty outcomes"
```

### Task 6: Prove Reuse, Immutability, And Zero Allocation

**Files:**
- Modify: `internal/result/resolution_test.go`

**Step 1: Add reuse and allocation tests**

Add a test that snapshots every outcome, remediation, rule, and edge slice;
resolves defect, empty, and defect masks repeatedly; then proves all backing
data is unchanged and no stale resolution survives an empty call.

Use `testing.AllocsPerRun(1000, ...)` on a preconstructed resolver and fixed
mask. Assign the returned scalar fields and one remediation ID to package-level
numeric sinks. Require exactly zero allocations per call.

**Step 2: Verify the new tests**

```bash
go test -count=1 -timeout 60s ./internal/result -run '^(TestResolverReuse|TestResolveAllocs)$'
```

Expected: PASS. If the allocation test fails, inspect escape analysis before
changing the assertion.

**Step 3: Run escape analysis**

```bash
timeout 120s go build -gcflags=-m ./internal/truth ./internal/result
```

Expected: `Resolver.Resolve` inputs and borrowed remediation slices do not
escape through the valid path. Static constructor errors and panic strings may
escape only on invalid paths.

**Step 4: Run final verification**

```bash
timeout 30s gofmt -w internal/truth/reason.go internal/truth/reason_test.go internal/result/outcome.go internal/result/remediation.go internal/result/resolution.go internal/result/resolution_test.go
go test -count=1 -timeout 60s ./internal/truth ./internal/result
go test -race -count=1 -timeout 120s ./internal/truth ./internal/result
go test -count=1 -timeout 60s ./...
timeout 120s go vet ./...
timeout 120s go build ./cmd/nornrune
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/truth ./internal/result
timeout 30s gofmt -l .
timeout 60s go mod tidy -diff
git diff --check
```

Expected: all commands exit zero; listing/diff commands print nothing.

**Step 5: Request final review**

Invoke `@superpowers:requesting-code-review`. Review specifically for reason-bit
collisions, reversed precedence, unstable ties, terminal short-circuiting,
malformed CSR overflow, invalid remediation payloads, borrowed-slice lifetime,
hidden allocations, and engine branches on NornRune labels. Fix confirmed
findings with a new RED/GREEN cycle and rerun Step 4.

**Step 6: Commit only when requested**

```bash
git add internal/truth/reason.go internal/truth/reason_test.go internal/result docs/plans/2026-08-21-outcome-resolution-design.md docs/plans/2026-08-21-outcome-resolution.md
git commit -m "feat: add generic outcome resolution"
```
