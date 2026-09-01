# WebAssembly Target

NornRune's WebAssembly target is a portable WASI preview 1 reactor over the
same immutable Program, four-valued evaluator, resolution tables, and
provenance columns used by native execution. It does not implement a second
policy engine.

## Artifact And ABI

ABI version `1` and Program schema version `1` are fixed-width values in a
little-endian envelope. The envelope contains magic, profile and capability
bits, ordered typed section descriptors, total bytes, and a SHA-256 checksum.
Each canonical Program column is encoded as an owned numeric section. The
loader checks the total size, checksum, section order, widths, alignment,
overlap, counts, Program source hash, symbol table, result tables, and execution
semantics before publishing the Program. Pinned Program, input-frame, and
result-frame layout digests include field order, container shape, width, and
signedness. Any wire-layout change requires a schema version change; otherwise
tests fail and the codec rejects the operation.

The versioned exports are:

- `nornrune_abi_version` and `nornrune_schema_version`;
- `nornrune_alloc`, `nornrune_load_program`, and `nornrune_upload_input`;
- `nornrune_set_fuel`, `nornrune_cancel`, and `nornrune_evaluate`;
- `nornrune_result_length` and `nornrune_read_result`;
- `nornrune_reset`, `nornrune_last_error_length`, and
  `nornrune_read_last_error`.

Inputs and outputs use their own checksummed little-endian frames. They carry
typed struct-of-arrays fact columns, bit planes, evidence CSR, outcome IDs, and
all result provenance CSR columns. They do not carry Go pointers, JSON,
transport objects, policy source strings, or host object references.

## Memory Ownership

`nornrune_alloc` creates one caller-visible transfer region in module linear
memory. The next allocation replaces that region, so a host writes and consumes
one artifact, input, or output transfer before allocating another. Program,
request, evaluator scratch, result, and encoded output storage are module-owned.
The loader and frame decoder copy from transfer memory. A host cannot retain a
Go pointer.

Artifact, input, output, row, section, and fuel limits are checked before
slicing or growing module-owned storage. Invalid pointers and short output
regions return fixed error codes. Fuel is charged as rows multiplied by
instructions before execution. Cancellation is checked at the operation
boundary; the current scalar reactor does not claim asynchronous interruption
inside one running batch.

## Profiles And Security

WASI and browser are base profiles with no policy host capabilities. Envoy,
Istio, and Cloudflare are reserved manifest profiles and are **not certified**
or enabled host adapters. A future adapter must explicitly negotiate bounded
request metadata, clock, storage, network, or logging capabilities. It cannot
weaken required evidence, verified-environment rules, disclosure restrictions,
or pre-execution approval.

The reactor receives no filesystem preopens, environment, arguments, sockets,
or network API in conformance tests. Traces, policy data, request facts,
evidence, and credentials are not sent to a host callback. WebAssembly SIMD is
disabled: the current target is scalar until runtime feature detection and
differential tests justify a separate SIMD module.

## Build And Conformance

Build a WASI reactor to an explicit path:

```bash
timeout 240s ./scripts/build-wasm.sh /tmp/nornrune.wasm
```

Check two byte-identical builds, required exports, and print the module hash:

```bash
timeout 240s ./scripts/check-wasm.sh
timeout 300s go run ./cmd/devx wasm:check
```

Wazero and Node's WASI implementation are independent runtime gates. Both load
the same artifact and input frame and compare the exact output frame with native
evaluation:

```bash
timeout 240s go test -count=1 -timeout 210s ./internal/target/wasm -run TestConformanceWazero
timeout 300s ./scripts/test-wasm-node.sh
```

`testdata/wasm/browser-harness.html` uses the same dependency-free ABI helper
and fail-closed browser WASI host driven by the Node conformance gate. Tests
generate `nornrune.wasm`, `program.bin`, `input.bin`, and `result.bin` in
temporary storage; serve copies beside the harness to drive a browser. No
generated module or policy artifact is tracked.

## Performance

Program export, module startup, Program load, host copies, and frame decoding
are cold or boundary costs and must be reported separately. The warmed module
runtime reuses evaluator, result, section, and output storage. Its native
runtime control test and benchmark require `0 B/op` and `0 allocs/op` for warm
evaluation and result-frame production:

```bash
timeout 300s go test -run '^$' -bench BenchmarkWASMWarmRuntimeEvaluate -benchmem -count=1 -timeout 270s ./internal/target/wasm
```

On the documented development machine, the one-row scalar control measured
about 2.3 microseconds per operation at `0 B/op`; this is not a cross-runtime
throughput claim. Wazero startup, host copies, and Node startup remain separate
measurements.
