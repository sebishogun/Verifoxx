# Machine-Readable Result Encoding Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Append the complete deterministic decision-result document into caller-owned JSON storage with zero warm allocations.

**Architecture:** A reusable `jsonresult.Encoder` binds one immutable Program and one reusable Explainer, then writes the fixed output schema directly into a supplied byte slice. It materializes one row at a time, preserves all CSR order, performs one-pass byte-level JSON escaping, and rolls the logical destination length back on any error.

**Tech Stack:** Go 1.27, immutable Program tables, `result.Explainer`, append/`strconv` encoding, SoA/CSR result batches.

---

All test, vet, benchmark, and analyzer commands below carry explicit timeouts.
Do not commit unless the user explicitly requests it.

### Task 1: Define The Encoder Contract And Golden Acceptance

**Files:**
- Create: `internal/adapters/jsonresult/encoder.go`
- Create: `internal/adapters/jsonresult/encoder_test.go`
- Modify: `internal/result/explain.go`
- Modify: `internal/result/explain_test.go`

**Step 1: Write failing public-contract tests**

Add tests for:

- a zero-value Encoder rejecting Append before Bind;
- Bind rejecting nil/malformed Programs without replacing a prior valid bind;
- exact production-policy output matching `testdata/golden/requests.json`;
- deterministic repeated output and append after a caller prefix;
- request/result row mismatch, zero RequestID, nil result, zero/multiple drivers,
  and invalid driver explanation provenance returning a static error while the
  logical destination remains unchanged; and
- zero rows producing a valid empty `results` array.

Expose `result.ReasonName(schema.ReasonID) (string, bool)` by renaming the
existing fixed-name helper; pin all nine names and invalid IDs in result tests.

**Step 2: Run the RED gate**

```bash
timeout 60s go test -count=1 -timeout 45s ./internal/result ./internal/adapters/jsonresult -run 'ReasonName|Encoder|Golden'
```

Expected: FAIL because `jsonresult` and the exported reason helper do not yet
exist. Do not commit.

**Step 3: Add the API skeleton and static sentinels**

Define:

```go
type Encoder struct {
    explainer    result.Explainer
    materialized result.Materialized
    program      *program.Program
}

func (e *Encoder) Bind(p *program.Program) error
func (e *Encoder) Append(dst []byte, requestIDs []schema.RequestID, batch *result.Batch, engineVersion []byte) ([]byte, error)
```

Use package sentinels `ErrInvalidProgram` and `ErrInvalidResult`. Bind through a
temporary Explainer and commit only after validating the seven explanation IDs
per clause. A same-Program rebind is a no-op.

### Task 2: Implement Allocation-Free JSON Primitives

**Files:**
- Modify: `internal/adapters/jsonresult/encoder.go`
- Test: `internal/adapters/jsonresult/encoder_test.go`

**Step 1: Add RED primitive tests**

Table-test exact encoding for empty/ASCII strings, quotes, backslashes, all
short control escapes, generic control bytes, `<`, `>`, `&`, U+2028/U+2029,
valid multibyte UTF-8, and invalid UTF-8 replacement. Test prefixed maximum
uint32 IDs and lowercase SHA-256 hex.

**Step 2: Run the RED gate**

```bash
timeout 60s go test -count=1 -timeout 45s ./internal/adapters/jsonresult -run 'Escape|Primitive'
```

Expected: FAIL because append helpers are absent.

**Step 3: Implement byte append helpers**

Implement one-pass `appendJSONString`, bulk-copying safe spans and emitting only
required escapes. Use `utf8.DecodeRune` without temporary strings. Add helpers
for indentation, prefixed decimal IDs via `strconv.AppendUint`, and direct hash
hex. No maps, reflection, `fmt`, `encoding/json`, or `[]byte`-to-string
conversion may enter production code.

**Step 4: Run focused and 386 tests**

```bash
timeout 60s go test -count=1 -timeout 45s ./internal/adapters/jsonresult -run 'Escape|Primitive'
timeout 60s env GOARCH=386 go test -count=1 -timeout 45s ./internal/adapters/jsonresult -run 'Escape|Primitive'
```

Expected: PASS. Do not commit.

### Task 3: Encode Rows, Drivers, Arrays, And Remediation

**Files:**
- Modify: `internal/adapters/jsonresult/encoder.go`
- Modify: `internal/adapters/jsonresult/encoder_test.go`

**Step 1: Add RED row-shape tests**

Cover exact field order and indentation, all nine reason names, satisfied versus
condition-false classification through Clause ExplanationIDs, empty/nonempty
ordered arrays, maximum IDs, Missing issue text without an EvidenceID,
nonmissing conflict with an EvidenceID, and both remediation forms:

```json
{"action":"add_evidence","evidence_kind":"..."}
{"action":"set_field","field":"...","value":...}
```

Set-field symbols are JSON strings; integers, Booleans, and timestamps preserve
their rendered JSON scalar type.

**Step 2: Run the RED gate**

```bash
timeout 60s go test -count=1 -timeout 45s ./internal/adapters/jsonresult -run 'Encoder|Remediation|Driver'
```

Expected: FAIL at the first unimplemented row encoder.

**Step 3: Implement one-pass document encoding**

For each row, call `Explainer.Materialize` into Encoder-owned reusable storage,
validate exactly one driver, classify its reason, and append every output field
in the frozen schema order. On any failure return `dst[:base]` and a static
sentinel. Range loops must be safe at `math.MaxUint32`; all length arithmetic
must widen before host-int conversion.

**Step 4: Run package, race/checkptr, and 386 gates**

```bash
timeout 60s go test -count=1 -timeout 45s ./internal/adapters/jsonresult
timeout 90s go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/adapters/jsonresult
timeout 60s env GOARCH=386 go test -count=1 -timeout 45s ./internal/adapters/jsonresult
```

Expected: PASS. Do not commit.

### Task 4: Prove Reuse And Replace The Temporary Projector

**Files:**
- Create: `internal/adapters/jsonresult/encoder_bench_test.go`
- Modify: `internal/conformance/nornrune_test.go`
- Verify: `results/requests.json`
- Verify: `testdata/golden/requests.json`

**Step 1: Add poison/reuse and allocation tests**

Prime Encoder materialization and a destination with golden-sized capacity.
Poison active byte/range storage, encode again, and compare with a fresh
encoder. Require `testing.AllocsPerRun` to report zero after priming.

**Step 2: Add the benchmark**

Benchmark only `Encoder.Append` with a pre-sized caller destination and a bound,
primed Encoder. Report output bytes and allocations.

**Step 3: Replace conformance projection**

Delete conformance-only output DTOs, `projectResults`, `projectRow`, materialized
range helpers, and duplicated reason-name logic. Keep direct numeric
decision/requirement assertions, then encode through `jsonresult.Encoder` and
byte-compare both required output files.

**Step 4: Run adapter-to-conformance and benchmark gates**

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/adapters/jsonresult ./internal/conformance
timeout 120s go test -run '^$' -bench='Encode' -benchmem -benchtime=500ms -count=6 -timeout 90s ./internal/adapters/jsonresult
```

Expected: tests PASS and warmed benchmark reports `0 B/op`, `0 allocs/op`.
The generated bytes must already match both checked-in files. Do not commit.

### Task 5: Run Portability, Static, Review, And Repository Gates

**Files:**
- Verify: all Task 25 production, test, design, plan, and golden files

**Step 1: Run formatting and static gates**

```bash
timeout 30s gofmt -w internal/adapters/jsonresult internal/result internal/conformance
timeout 90s go vet ./internal/adapters/jsonresult ./internal/result ./internal/program ./internal/conformance
timeout 90s env GOARCH=386 go vet ./internal/adapters/jsonresult ./internal/result ./internal/program ./internal/conformance
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/adapters/jsonresult ./internal/result
```

**Step 2: Inspect escape output**

Confirm Encoder-owned cold growth may escape, while primed Append allocates
zero. Confirm production encoder code contains no map, reflection, `fmt`,
`encoding/json`, or byte/string conversion.

**Step 3: Request independent specification and quality reviews**

Review against Task 25, the approved architecture, this design, the assignment
output contract, malformed-input safety, deterministic order, and exact golden
bytes. Fix every Critical and Important finding with a RED regression.

**Step 4: Run final bounded gates**

```bash
timeout 120s go test -count=1 -timeout 60s ./...
timeout 120s env GOARCH=386 go test -count=1 -timeout 60s ./internal/adapters/jsonresult ./internal/result ./internal/program ./internal/conformance
timeout 180s go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 90s ./internal/adapters/jsonresult ./internal/result ./internal/program ./internal/conformance
timeout 120s go test -count=1 -tags=purego -timeout 60s ./...
timeout 30s gofmt -l .
timeout 30s git diff --check
```

Expected: every command exits zero and formatting checks print nothing. Do not
commit; the roadmap commit message, if later requested, is
`feat: encode decision results`.
