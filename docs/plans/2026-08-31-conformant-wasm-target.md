# Conformant WebAssembly Target Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Export NornRune's validated immutable Program and evaluator semantics through a deterministic, versioned WebAssembly ABI with bounded memory and native parity.

**Architecture:** A cold host exporter writes an explicitly bounded little-endian artifact containing canonical Program columns, schema metadata, capability requirements, and SHA-256 integrity. A `wasip1/wasm` module validates and loads that artifact into one module-owned runtime, accepts typed SoA input frames through caller-owned linear memory, evaluates with the existing native executor, and exposes fixed result frames through versioned scalar exports. Host conformance uses wazero and Node's independent WASI runtime; SIMD and vendor-specific host profiles remain disabled until separately proven.

**Tech Stack:** Go 1.27, `//go:wasmexport`, WASI preview 1, wazero, Node WASI, SHA-256, existing `internal/program`, `internal/eval`, and `internal/result` packages.

---

### Task 1: Public ABI And Manifest Contract

**Files:**
- Create: `target/wasm/abi.go`
- Create: `target/wasm/manifest.go`
- Test: `target/wasm/export_test.go`

**Step 1: Write failing contract tests**

Cover ABI/schema versions, magic values, fixed operation/error enums, bounded host capabilities, base WASI/browser profiles, and rejection of unknown required capabilities. Assert that the manifest contains no free-form runtime labels and that proxy profiles cannot imply evidence, clock, storage, or network capabilities.

**Step 2: Run the focused test**

Run: `timeout 90s go test -count=1 -timeout 60s ./target/wasm`
Expected: FAIL because the package and contracts do not exist.

**Step 3: Implement the minimal public contract**

Define fixed-width `ABIVersion`, `SchemaVersion`, `Operation`, `ErrorCode`, `Capability`, `Profile`, `Limits`, `Metadata`, and `Manifest` types. Validation must reject zero/unknown versions, unknown capability bits, inconsistent limits, and base profiles requiring optional host capabilities.

**Step 4: Re-run the focused test**

Run: `timeout 90s go test -count=1 -timeout 60s ./target/wasm`
Expected: PASS.

### Task 2: Deterministic Program Artifact Envelope

**Files:**
- Create: `target/wasm/export.go`
- Create: `internal/target/wasm/layout.go`
- Test: `target/wasm/export_test.go`
- Test: `internal/target/wasm/layout_test.go`

**Step 1: Write failing exporter tests**

Compile a representative policy and assert byte-identical exports, little-endian headers, ordered non-overlapping sections, exact SHA-256, copied source ownership, stable metadata, and caller-supplied destination reuse. Add malformed header, overflow, excessive count, overlap, alignment, checksum, schema-version, and trailing-byte cases.

**Step 2: Run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./target/wasm ./internal/target/wasm`
Expected: FAIL on missing export/layout functions.

**Step 3: Implement envelope sizing and encoding**

Use checked `uint64` arithmetic. Emit a fixed header and descriptor table before payloads. Encode each canonical Program slab by a schema-versioned section ID; cold reflection may enumerate the immutable numeric columns only when a pinned layout digest makes an unversioned field change fail tests and reject export. Never use reflection in evaluation, gob, maps, native struct memory, or Go pointers. Size once, append into caller `dst`, align sections deterministically, and hash every byte except the hash slot itself.

**Step 4: Implement structural preflight**

Parse only the fixed header/descriptors first. Validate total bytes, descriptor count, ascending IDs, alignment, non-overlap, element widths/counts, configured limits, and checksum before allocating decoded Program columns.

**Step 5: Re-run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./target/wasm ./internal/target/wasm`
Expected: PASS.

### Task 3: Program Decode And Native Round Trip

**Files:**
- Modify: `internal/target/wasm/layout.go`
- Test: `internal/target/wasm/layout_test.go`

**Step 1: Write failing round-trip tests**

Round-trip every Program column, result table, source span, symbol/value slab, field schema, applicability index, fact index specification, and fixed scalar. Assert independent ownership, rebuilt borrowed result tables/resolver, and exact evaluation parity. Mutate every descriptor class and cross-reference family and require a bounded error without panic.

**Step 2: Run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/target/wasm -run 'TestArtifact'`
Expected: FAIL because decode is incomplete.

**Step 3: Implement explicit decoding**

Allocate one typed slice per validated descriptor, decode little-endian values, rebuild nested Program table headers over owned columns, rebuild deterministic indexes where derivable, call Program validation, and reject invalid instruction/value/symbol/result references before publication.

**Step 4: Re-run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/target/wasm -run 'TestArtifact'`
Expected: PASS.

### Task 4: Bounded Binary Input And Result Frames

**Files:**
- Create: `internal/target/wasm/frame.go`
- Test: `internal/target/wasm/frame_test.go`

**Step 1: Write failing frame tests**

Cover all symbol/integer/Boolean/timestamp/presence/evidence SoA input columns and all fixed/CSR result columns. Assert deterministic encoding, Unicode symbol payloads, zero rows, tail words, maximum configured batch size, truncation, overlap, overflow, unknown sections, invalid CSR, and caller buffer reuse.

**Step 2: Run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/target/wasm -run 'Test.*Frame'`
Expected: FAIL on missing frame codec.

**Step 3: Implement explicit frame codecs**

Use the same descriptor discipline as artifacts. Decode directly into reusable runtime-owned typed slabs after structural preflight. Encode result columns into caller-visible linear memory without JSON, maps, reflection, or per-row allocations.

**Step 4: Re-run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/target/wasm -run 'Test.*Frame'`
Expected: PASS.

### Task 5: Reusable Module Runtime

**Files:**
- Create: `internal/target/wasm/runtime.go`
- Test: `internal/target/wasm/runtime_test.go`

**Step 1: Write failing runtime tests**

Cover metadata before load, one validated Program load, replacement load, input upload, evaluation, result read, reset, cancellation, fuel exhaustion, output-too-small, stale result rejection, malformed state transitions, panic containment, and deterministic last-error codes. Poison reusable buffers before each operation.

**Step 2: Run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/target/wasm -run 'TestRuntime'`
Expected: FAIL on missing runtime.

**Step 3: Implement the state machine**

One `Runtime` owns Program, input slabs, evaluator scratch, result slabs, output bytes, bounded error bytes, generation counters, cancellation state, and fuel. Validate all lengths before slicing. Fuel is charged by bounded rows/instructions before execution; cancellation is checked at operation boundaries. Warm evaluation must not grow memory or call hosts.

**Step 4: Re-run focused tests**

Run: `timeout 90s go test -count=1 -timeout 60s ./internal/target/wasm -run 'TestRuntime'`
Expected: PASS.

### Task 6: WASI Export Surface And Reproducible Build

**Files:**
- Create: `cmd/nornrune-wasm/main.go`
- Create: `cmd/nornrune-wasm/exports.go`
- Create: `cmd/nornrune-wasm/exports_stub.go`
- Create: `scripts/build-wasm.sh`
- Create: `scripts/check-wasm.sh`
- Modify: `internal/devx/checks.go`
- Test: `cmd/nornrune-wasm/main_test.go`

**Step 1: Write failing ABI/build tests**

Assert the command builds for `GOOS=wasip1 GOARCH=wasm`, exports the exact versioned functions, imports no socket ABI, receives no filesystem preopens, and produces byte-identical modules from two bounded builds with the same toolchain and flags. Go's WASI runtime imports inert clock, random, and filesystem functions even when the host grants no corresponding policy capability.

**Step 2: Run focused tests**

Run: `timeout 180s go test -count=1 -timeout 150s ./cmd/nornrune-wasm`
Expected: FAIL because the module command does not exist.

**Step 3: Implement exports**

Expose metadata, allocation/grow, Program load, input upload, evaluate, result length/read, reset, cancel, fuel, and last-error operations with integer pointers/lengths only. Every export recovers traps into a fixed error code and validates linear-memory ranges before use. Non-Wasm stubs preserve host testability.

**Step 4: Add reproducible scripts and drift check**

Build into a temporary directory only; compare hashes and inspect imports/exports. Do not commit generated `.wasm` binaries. Wire the bounded drift check into devx.

**Step 5: Re-run focused checks**

Run: `timeout 240s go test -count=1 -timeout 180s ./cmd/nornrune-wasm`
Run: `timeout 240s ./scripts/check-wasm.sh`
Expected: PASS.

### Task 7: Wazero Native/Module Conformance

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `internal/target/wasm/conformance_test.go`
- Create: `testdata/wasm/README.md`

**Step 1: Promote wazero and write failing conformance tests**

Compile the module once, instantiate it with no optional host capabilities, load the representative artifact, upload conformance batches, and compare every result/provenance column against native evaluation. Cover malformed artifact/input, memory limits, fuel, cancellation, traps, Unicode, large batches, repeated calls, and deterministic bytes.

**Step 2: Run focused conformance**

Run: `timeout 240s go test -count=1 -timeout 210s ./internal/target/wasm -run 'TestConformanceWazero'`
Expected: FAIL until the host harness and all ABI paths are connected.

**Step 3: Implement the wazero harness and close parity gaps**

Use wazero as a direct dependency. Configure bounded runtime memory, no inherited process environment, no filesystem, no network, and no wall clock. Reuse encoded input/output buffers across test rows.

**Step 4: Re-run focused conformance**

Run: `timeout 240s go test -count=1 -timeout 210s ./internal/target/wasm -run 'TestConformanceWazero'`
Expected: PASS.

### Task 8: Independent Node WASI And Browser-Compatible Harness

**Files:**
- Create: `testdata/wasm/conformance.mjs`
- Create: `testdata/wasm/browser-harness.html`
- Create: `scripts/test-wasm-node.sh`
- Test: `internal/target/wasm/node_test.go`

**Step 1: Write failing Node harness test**

Build the module to a temporary path, execute it under Node's WASI implementation, and compare the same fixture's exact encoded output hash with native/wazero output. Assert bounded handling for malformed input, fuel exhaustion, and reset.

**Step 2: Run focused Node test**

Run: `timeout 240s go test -count=1 -timeout 210s ./internal/target/wasm -run 'TestConformanceNode'`
Expected: FAIL until the harness exists.

**Step 3: Implement shared JavaScript harness**

Keep ABI calls in one browser-compatible ES module. The HTML harness supplies a WASI adapter without network/storage capabilities and reports fixed pass/fail records; the Node wrapper uses the same calls and fixture bytes.

**Step 4: Re-run Node and wazero conformance**

Run: `timeout 300s ./scripts/test-wasm-node.sh`
Run: `timeout 300s go test -count=1 -timeout 270s ./internal/target/wasm -run 'TestConformance(Wazero|Node)'`
Expected: PASS.

### Task 9: Benchmarks And Allocation Proofs

**Files:**
- Create: `internal/target/wasm/benchmark_test.go`
- Modify: `docs/performance.md`

**Step 1: Add separated benchmarks**

Benchmark artifact sizing/export, module compile/startup, Program load, input copy/decode, warm native evaluation, warm wazero evaluation, result encoding/read, and total host round-trip at representative batch sizes. Report host/runtime versions, module hash, bytes, rows, policy shape, and scalar/SIMD status.

**Step 2: Run allocation and throughput benchmarks**

Run: `timeout 300s go test -run '^$' -bench 'BenchmarkWASM' -benchmem -count=1 -timeout 270s ./internal/target/wasm`
Expected: warm native runtime evaluation reports `0 B/op` and `0 allocs/op`; host/runtime boundary costs are reported separately.

**Step 3: Inspect escapes**

Run: `timeout 180s go test -run '^$' -gcflags=-m -timeout 150s ./internal/target/wasm`
Expected: no newly escaping per-row values in runtime evaluation/frame kernels.

### Task 10: Documentation And Full Verification

**Files:**
- Create: `docs/wasm.md`
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `internal/doccheck/docs_test.go`
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine.md`

**Step 1: Write failing documentation contracts**

Require ABI/schema versions, artifact/frame limits, ownership, capabilities, fuel/cancellation, deterministic build command, wazero/Node conformance commands, browser harness instructions, scalar-only status, and explicit non-certification of proxy profiles.

**Step 2: Write the documentation**

Document setup, export/load/evaluate flow, error codes, security boundary, memory ownership, compatibility policy, reproducibility metadata, performance interpretation, and deferred SIMD/vendor profiles. Link it from README and architecture docs.

**Step 3: Run the full bounded matrix**

Run: `timeout 300s go test -count=1 -timeout 240s ./...`
Run: `timeout 360s go test -count=1 -timeout 300s -race -gcflags=all=-d=checkptr=2 ./...`
Run: `timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...`
Run: `timeout 300s go test -count=1 -tags=purego -timeout 240s ./...`
Run: `timeout 420s go test -count=1 -tags=integration -timeout 360s ./...`
Run: `timeout 180s go vet ./...`
Run: `timeout 300s ./scripts/check-fieldalignment.sh`
Run: `timeout 300s ./scripts/check-wasm.sh`
Run: `timeout 300s ./scripts/test-wasm-node.sh`
Run: `timeout 300s go run ./cmd/devx policy:check`
Run: `timeout 300s go run ./cmd/devx results:check`
Run: `timeout 300s go run ./cmd/devx proto:check`
Run: `timeout 300s go run ./cmd/devx build`
Run: `timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check`
Run: `timeout 30s git diff --check`
Expected: all commands PASS.

**Step 4: Mark Task 56 complete**

Add `**Status:** Complete (2026-08-31)` beneath the Task 56 heading only after the full matrix passes.

**Step 5: Commit when explicitly requested**

Stage only Task 56 files and commit with `feat: add conformant wasm target` when the user requests a commit.
