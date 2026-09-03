# WebAssembly Target

NornRune's WebAssembly target is a portable WASI preview 1 reactor over the
same immutable Program, four-valued evaluator, resolution tables, and
provenance columns used by native execution. It does not implement a second
policy engine.

## Artifact And ABI

ABI version `1` and Program schema version `1` are fixed-width values in a
little-endian envelope. Schema version 1 pins the unreleased canonical
104-byte Program envelope below; input and result frames retain a separate
64-byte frame header. Each canonical Program column is encoded as an owned
numeric section.

| Offset | Width | Program artifact value |
| ---: | ---: | --- |
| 0 | 4 | artifact magic |
| 4 | 2 | ABI version |
| 6 | 2 | Program schema version |
| 8 | 1 | profile |
| 9 | 3 | reserved zero bytes |
| 12 | 4 | required capability bits |
| 16 | 4 | section count |
| 20 | 4 | descriptor width, exactly 24 |
| 24 | 8 | total artifact bytes |
| 32 | 32 | artifact SHA-256 |
| 64 | 8 | maximum artifact bytes |
| 72 | 8 | maximum input bytes |
| 80 | 8 | maximum output bytes |
| 88 | 8 | maximum fuel |
| 96 | 4 | maximum rows |
| 100 | 4 | maximum Program columns |
| 104 | 24 x section count | ordered section descriptors |

The checksum covers the complete artifact with its own 32-byte hash slot
treated as zero, so all six artifact limits are hash-bound. A loader first
applies its trusted maximum artifact size, then validates the artifact's own
manifest and requires ABI, schema, profile, capabilities, and all six limits to
exactly match the trusted host manifest. Mismatch is rejected before descriptor
or Program-column allocation; metadata after load reports limits decoded from
the artifact rather than substituting host values.

The loader also checks section order, widths, alignment, overlap, counts,
Program source hash, symbol table, result tables, and execution semantics before
publishing the Program. Pinned Program, artifact-envelope, input-frame, and
result-frame layouts include field order, container shape, width, signedness,
and fixed offsets. Any future wire-layout change requires a schema version
change; otherwise tests fail and the codec rejects the operation.

The versioned exports are:

- `nornrune_abi_version` and `nornrune_schema_version`;
- `nornrune_metadata_length` and `nornrune_read_metadata`;
- `nornrune_alloc`, `nornrune_load_program`, and `nornrune_upload_input`;
- `nornrune_set_fuel`, `nornrune_cancel`, and `nornrune_evaluate`;
- `nornrune_result_length` and `nornrune_read_result`;
- `nornrune_reset`, `nornrune_last_error_length`, and
  `nornrune_read_last_error`.

Inputs and outputs use their own checksummed little-endian frames. Each 64-byte frame
header is followed immediately by its ordered 24-byte descriptors. Frames carry
typed struct-of-arrays fact columns, bit planes, evidence CSR, outcome IDs, and
all result provenance CSR columns. They do not carry Go pointers, JSON,
transport objects, policy source strings, or host object references.

`nornrune_read_metadata` returns one fixed 128-byte record:

| Offset | Width | Value |
| ---: | ---: | --- |
| 0 | 4 | metadata magic |
| 4 | 2 | ABI version |
| 6 | 2 | Program schema version |
| 8 | 1 | profile |
| 9 | 3 | reserved zero bytes |
| 12 | 4 | required capability bits |
| 16 | 8 | maximum artifact bytes |
| 24 | 8 | maximum input bytes |
| 32 | 8 | maximum output bytes |
| 40 | 8 | maximum fuel |
| 48 | 4 | maximum rows |
| 52 | 4 | maximum Program columns |
| 56 | 32 | artifact SHA-256 |
| 88 | 32 | Program source SHA-256 |
| 120 | 8 | reserved zero bytes |

All integers are little-endian. Before a Program is loaded, both hashes are
zero; after a successful load they identify the published artifact and Program.
The public `target/wasm.DecodeMetadata` API validates the record, versions,
profile, capabilities, limits, and reserved bytes.

## Memory Ownership

`nornrune_alloc` creates one caller-visible transfer region in module linear
memory. The next allocation replaces that region, so a host writes and consumes
one artifact, input, or output transfer before allocating another. Program,
request, evaluator scratch, result, and encoded output storage are module-owned.
The loader and frame decoder copy from transfer memory. A host cannot retain a
Go pointer.

The module's 64 MiB constant bounds a single caller-visible transfer region and
the encoded output, not the aggregate of Go runtime state, decoded Program,
input columns, evaluator scratch, result columns, and transfer memory. Hosts
that require an aggregate linear-memory ceiling must enforce it at the runtime
or process boundary.

Artifact, input, output, row, section, and fuel limits are checked before
slicing or growing module-owned storage. Invalid pointers and short output
regions return fixed error codes. Fuel is charged before execution from
load-time Program work counts and the uploaded batch shape. The conservative
cost includes row/instruction work, Boolean operand edges, literal-list items,
input cells, evidence records and references, reference traversal by every
evidence instruction, requirement/clause resolution, and worst-case result
columns and edges. Thus a small row count cannot hide a large Boolean, evidence,
or output workload. Cancellation
is checked at the operation boundary; the current scalar reactor does not claim
asynchronous interruption inside one running batch. Every exported operation
contains a panic boundary: unexpected failures become `ErrorInternal` for
error-code operations or zero for scalar length/version operations, and
evaluation state is cleared instead of propagating a module trap.

The JavaScript conformance helper reads the module's pre-load metadata, checks
that the artifact manifest exactly matches it, and supplies the advertised
maximum fuel. The module repeats the same checks; the host-side comparison is
not a replacement for module validation.

## Profiles And Security

WASI and browser are distinct base profile values with no policy host
capabilities. Artifacts are not interchangeable across profiles: a runtime
rejects a profile mismatch before publication. The shipped reactor is compiled
as `ProfileWASI`. The browser harness certifies a fail-closed browser host for
that WASI reactor; it does not claim that the current build emits or certifies a
native `ProfileBrowser` module. Envoy, Istio, and Cloudflare are reserved
manifest profiles and are **not certified** or enabled host adapters. A future
adapter must explicitly negotiate bounded request metadata, clock, storage,
network, or logging capabilities. It cannot weaken required evidence,
verified-environment rules, disclosure restrictions, or pre-execution approval.

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
and fail-closed browser host for the WASI reactor driven by the Node conformance
gate. Tests generate `nornrune.wasm`, `program.bin`, `input.bin`, and
`result.bin` in temporary storage; serve copies beside the harness to drive a
browser. No generated module or policy artifact is tracked.

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
