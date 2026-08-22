# JSON Batch Decoder Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Decode bounded request and evidence JSON directly into reusable typed SoA batches with stable positional errors and zero warm allocation.

**Architecture:** A reusable hand-written scanner first counts rows and CSR edges, then begins one exact `eval.Builder` shape and semantically decodes evidence followed by requests. Program-bound open-address tables resolve fields and evidence catalogs; reusable ID/bitset scratch detects duplicates and missing references.

**Tech Stack:** Go 1.27, byte-slice JSON scanner, Task 13 `eval.Builder`, immutable `program.Program`, fixed-width IDs, CSR, Go fuzzing and allocation tests.

---

### Task 1: Define Errors, Limits, Scanner, And Abort Lifecycle

**Files:**
- Create: `internal/adapters/jsonbatch/errors.go`
- Create: `internal/adapters/jsonbatch/decoder.go`
- Create: `internal/adapters/jsonbatch/decoder_test.go`
- Modify: `internal/eval/builder.go`
- Modify: `internal/eval/builder_test.go`

**Step 1: Write failing tests**

Test stable request/evidence input labels and error codes for empty, truncated,
malformed, trailing, invalid UTF-8, oversized source/string, and unsupported
version inputs. Test that `Builder.Abort` prevents `Finish`, keeps capacity for
the next `Begin`, and is safe on nil/inactive builders.

**Step 2: Verify RED**

Run: `go test -timeout 60s ./internal/eval ./internal/adapters/jsonbatch`

Expected: FAIL because `jsonbatch` and `Builder.Abort` do not exist.

**Step 3: Implement scanner primitives and errors**

Add `Input`, `ErrorCode`, `Error`, and zero-disables `Limits`. Implement reusable
string escape/UTF-8 parsing, int64 parsing, literal parsing, punctuation,
whitespace, bounded value skipping, and exact end-of-input checks over `[]byte`.
Messages stay static; no string conversion occurs on the success path.

**Step 4: Implement `Builder.Abort`**

`Abort` only seals the active build. It does not clear capacity or expose the
partial batch; the next successful `Begin` clears all active ranges.

**Step 5: Verify GREEN and commit**

Run: `go test -timeout 60s ./internal/eval ./internal/adapters/jsonbatch`

```bash
git add internal/eval internal/adapters/jsonbatch
git commit -m "feat: scan bounded batch json"
```

### Task 2: Count Shapes And Bind Program Catalogs

**Files:**
- Modify: `internal/adapters/jsonbatch/decoder.go`
- Modify: `internal/adapters/jsonbatch/decoder_test.go`

**Step 1: Write failing tests**

Count zero and nonzero request/evidence arrays and total evidence references in
arbitrary root-key order. Cover duplicate/unknown/missing root keys, wrong root
types, count/depth limits, malformed Programs, and pack-name mismatch. Build a
Program with multiple field names/kinds and evidence kind/state names; assert
deterministic field/catalog lookups after rebinding and capacity reuse.

**Step 2: Verify RED**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch -run 'Test(Count|Bind|Root)'`

**Step 3: Implement count and bind paths**

Add a lightweight structural pass returning:

```go
type shape struct {
	requests uint32
	evidence uint32
	refs     uint32
}
```

Bind a different Program by constructing reusable power-of-two tables for
`FieldNames`, `EvidenceKindNames`, and `EvidenceStateNames`. Reject duplicate,
zero, out-of-range, or wrong-kind catalog IDs before `Builder.Begin`.

**Step 4: Verify and commit**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch`

```bash
git add internal/adapters/jsonbatch
git commit -m "feat: size and bind json batches"
```

### Task 3: Decode Evidence Rows

**Files:**
- Modify: `internal/adapters/jsonbatch/decoder.go`
- Modify: `internal/adapters/jsonbatch/decoder_test.go`

**Step 1: Write failing tests**

Decode all four supplied evidence records and assert exact IDs, kind/state IDs,
subject/scope/reviewer/timing symbols, and zero timestamps. Cover arbitrary key
order, duplicate IDs/keys/semantic aliases, unknown kinds/states/attributes,
wrong types, missing required keys, and canonical `E<n>` boundaries.

Test `timestamp_state=stale`, invalid attestation, and
`reviewer_state=one_valid_one_revoked` overriding the primary status through
the Program state catalog.

**Step 2: Verify RED**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch -run 'TestDecodeEvidence'`

**Step 3: Implement evidence decoding**

Decode into one local `eval.EvidenceRecord`, resolve strings through
`Builder.InternSymbol`, and call `SetEvidence` once after the object validates.
Build a reusable `EvidenceID -> row+1` open-address table while rejecting
duplicates. Apply only the normalization rules in the approved design.

**Step 4: Verify and commit**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch`

```bash
git add internal/adapters/jsonbatch
git commit -m "feat: decode evidence soa rows"
```

### Task 4: Decode Request Facts And CSR

**Files:**
- Modify: `internal/adapters/jsonbatch/decoder.go`
- Modify: `internal/adapters/jsonbatch/decoder_test.go`

**Step 1: Write failing tests**

Decode typed symbol, integer, Boolean, timestamp, and presence fields across
multiple same-kind columns. Assert nested path flattening, column offsets,
presence masks, explicit null/missing facts, request IDs, request-order
preservation, and exact evidence ranges/references.

Cover duplicate/unknown paths, duplicate request IDs/references, missing
evidence IDs, wrong scalar types, nested arrays/objects, missing request ID, and
canonical `R<n>` boundaries.

**Step 2: Verify RED**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch -run 'TestDecodeRequest'`

**Step 3: Implement request decoding**

Use a reusable path buffer and Program-bound field table. Clear one field-seen
bitset per row, dispatch by `FieldIndex.Lookup`, and call the matching typed
setter. Accumulate exact `rows+1` offsets and zero-based evidence rows in
decoder-owned reusable slices, then call `SetEvidenceCSR` once.

**Step 4: Verify and commit**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch`

```bash
git add internal/adapters/jsonbatch
git commit -m "feat: decode request facts and evidence csr"
```

### Task 5: Decode Supplied Packs And Prove Recovery

**Files:**
- Modify: `internal/adapters/jsonbatch/decoder.go`
- Modify: `internal/adapters/jsonbatch/decoder_test.go`

**Step 1: Write failing integration tests**

Decode embedded `fixtures.RequestsJSON` and `fixtures.EvidenceJSON` against a
fixture-compatible Program. Assert rows R1-R5, evidence E1-E4, every supplied
fact, exact CSR ranges, unknown environment extension-symbol identity, and
deterministic repeated output.

Submit malformed input after a successful decode, assert no partial batch can
finish, then decode valid input again and compare capacities/results.

**Step 2: Verify RED**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch -run 'TestDecode(Supplied|Failure)'`

**Step 3: Complete public API and recovery**

Implement `Decoder.Decode` and fresh `Decode` wrapper. Preflight limits and
both shapes, bind the Program, call `Begin`, decode evidence then requests,
install CSR, and call `Finish`. Defer source clearing; call `Abort` on every
post-Begin error.

**Step 4: Verify and commit**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch`

```bash
git add internal/adapters/jsonbatch
git commit -m "feat: decode json request batches"
```

### Task 6: Fuzz And Prove Warm Allocation

**Files:**
- Create: `internal/adapters/jsonbatch/fuzz_test.go`
- Create: `internal/adapters/jsonbatch/decoder_bench_test.go`
- Modify: `internal/adapters/jsonbatch/decoder_test.go`

**Step 1: Add fuzz and poisoning coverage**

Seed valid, truncated, escaped, duplicate-key, wrong-type, and missing-reference
documents. Fuzz both sources with strict byte/count/depth limits; assert no
panic and that errors have in-range offsets. Poison decoder/builder scratch,
decode a smaller batch, and compare with a fresh decoder.

**Step 2: Add warm allocation test and benchmark**

Prime the supplied shape, then repeatedly decode it with the same Decoder and
Builder. Require zero warm allocations and benchmark `DecodeBatch` with
`-benchmem`.

**Step 3: Verify**

Run: `go test -timeout 60s ./internal/adapters/jsonbatch`

Run: `go test -timeout 120s -run '^$' -bench=DecodeBatch -benchmem ./internal/adapters/jsonbatch`

Run: `go test -timeout 60s -fuzz=FuzzDecodeBatch -fuzztime=10s ./internal/adapters/jsonbatch`

**Step 4: Commit**

```bash
git add internal/adapters/jsonbatch
git commit -m "test: harden json batch decoding"
```

### Task 7: Run Cross-Cutting Gates And Review

**Files:**
- Modify only files required by a failing gate.

**Step 1: Run package portability gates**

Run: `go test -count=1 -timeout 60s -race ./internal/eval ./internal/adapters/jsonbatch`

Run: `GOARCH=386 go test -count=1 -timeout 60s ./internal/eval ./internal/adapters/jsonbatch`

**Step 2: Run repository gates**

Run: `go test -count=1 -timeout 60s ./...`

Run: `timeout 60s go vet ./...`

Run: `timeout 60s go build ./cmd/verifoxx`

Run: `timeout 60s gofmt -l .`

Run: `timeout 60s go mod tidy -diff`

**Step 3: Inspect layout and escapes**

Run: `timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/eval ./internal/adapters/jsonbatch`

Run: `timeout 120s go build -gcflags=-m ./internal/adapters/jsonbatch`

Expected: no production layout finding; growth allocations may escape, while
the warmed decoder benchmark remains zero-allocation.

**Step 4: Request review and fix confirmed findings with RED/GREEN tests**

Review the full Task 14 commit range against the design, emphasizing malformed
input safety, stale scratch, symbol identity, missing/reference semantics,
bounded work, and Task 15 usability.

**Step 5: Commit gate corrections if needed**

```bash
git add internal/eval internal/adapters/jsonbatch docs/plans
git commit -m "fix: harden json batch boundaries"
```
