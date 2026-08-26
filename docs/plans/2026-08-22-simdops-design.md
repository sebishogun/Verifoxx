# SIMD Operations Facade Design

**Date:** 2026-08-22

**Status:** Approved

## Goal

Expose only the whole-slice SIMD operations needed by the evaluator and bitmap
indexes while preserving typed policy IDs, pure-Go portability, exact scalar
semantics, and zero allocation on hot paths.

## Boundary

`internal/simdops` owns the dependency on `github.com/sebishogun/simd` and the
only unsafe conversions. Callers continue to use typed `schema` IDs and
`[]uint64` bitplanes. Private checked views reinterpret defined `~uint32` and
`~int64` slices as their built-in types and word slices as bytes. The word view
is valid on every endian because AND, OR, XOR, and AND-NOT operate independently
on each bit.

The bounded API contains:

- scalar comparisons over `~uint32` and `~int64` columns;
- AND, OR, XOR, and AND-NOT over `[]uint64` bitplanes;
- AND, OR, XOR, and NOT over `[]bool` masks; and
- stable compression of `~uint32` values under a Boolean mask.

Comparison mode is selected once per whole-slice call, never per element. An
invalid mode panics before output mutation. Operations process the shortest
input, matching the dependency. Word and mask destinations may exactly alias an
input; shifted overlap is unsupported. Compression truncates to destination
capacity and does not support overlap.

## Backends And Dispatch

`simd.go` builds without `purego` and forwards each operation to the pinned
v1.21 whole-slice API. Its runtime dispatcher and measured guards choose scalar
or generated assembly. NornRune does not add a second guessed crossover.

`purego.go` builds with `purego` and provides direct allocation-free loops with
the same contracts. `ops.go` holds shared types and scalar references.

`info.go` exposes cold-path diagnostics: selected tier, the dependency's
one-line description, whether this is a pure-Go build, and thresholds expressed
in each wrapper's input units. Byte-kernel thresholds are converted to uint64
word counts. Normal slice acceleration does not require `GOEXPERIMENT=simd`;
the pinned release's experiment-gated vector API uses obsolete Go 1.26
`archsimd` names and remains outside the matrix until a Go 1.27-compatible
release exists.

## Verification

One differential acceptance test compares every wrapper with local scalar
references over empty inputs, kernel boundaries, unaligned subslices, partial
tails, exact aliases, and short compression destinations. The same test checks
runtime metadata and runs under normal, `purego`, and 386 scalar builds.
`docs/performance.md` records operations, units, aliases, thresholds, and
fallback behavior; performance claims wait for Task 19 benchmarks.

## Rejected Alternatives

- Raw `uint32` storage would spread representation details through the typed
  schema and evaluator.
- Direct dependency calls cannot accept defined policy ID types.
- Hand-written `archsimd` loops are amd64/experiment-only and lose the pinned
  library's generated whole-slice kernels and portable runtime dispatch.
