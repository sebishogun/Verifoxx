# SIMD Evaluator Design

**Date:** 2026-08-22

**Status:** Approved by the Phase 8 roadmap and delegated implementation authority

## Goal

Accelerate compatible evaluator stages without changing Program, Batch, truth,
reason, result, or evidence representations. The existing scalar evaluator
remains the executable reference and fallback. Warm execution remains
allocation-free.

## Chosen Shape

Keep one `Executor` and one topological schedule. An internal execution mode
selects automatic, scalar-reference, or forced-SIMD behavior; only tests and
benchmarks call the forced modes. Public `Execute` uses automatic selection.

Automatic mode chooses each stage independently:

- contiguous symbol, integer, and timestamp scalar comparisons use
  `simdops` above the measured evaluator crossover;
- packed Boolean equality, truth composition, and reason union use uint64
  whole-slice operations at the measured composite word threshold;
- `Exists` keeps its direct copy/clear path;
- `In` remains scalar until Task 20 measures index reuse; and
- evidence remains the scalar CSR reducer because each request has a
  variable-length edge range and fixed-width qualifiers.

This avoids a second evaluator and retains small-batch scalar behavior. A
separate SIMD executor would duplicate validation, resolution, provenance, and
slot-liveness logic. Changing truth storage to `[]bool` would consume eight
times the mask memory and destroy the word-aligned layout used by pruning and
row sharding.

## Comparison Packing

The pinned comparison API writes one Boolean byte per row, while evaluator truth
is one bit per row. `Executor` therefore owns one reusable `[]bool` comparison
slab sized only when a SIMD comparison is selected.

`internal/simdops` adds `PackMask`, which packs those canonical Boolean values
into caller-owned uint64 words. The accelerated backend privately views the
Boolean slice as bytes and calls the dependency's `MaskBits` primitive. It
zeroes final-word padding and uses a portable packer on big-endian hosts. The
`purego` backend uses the same portable packer. Destination shape is validated
before mutation, and the operation allocates nothing.

Adding this primitive is preferable to packing one row at a time in the
evaluator: scalar packing would make the O(rows) conversion the bottleneck and
erase most comparison-kernel gain. Keeping the unsafe view in `simdops`
preserves the repository's single unsafe boundary.

## Data Flow

For a compatible scalar predicate:

1. Resolve the Program constant and contiguous kind-local column once.
2. Compare the complete column into the reusable Boolean slab.
3. Pack the slab into the destination positive words.
4. Intersect positive words with field presence.
5. Compute negative as presence AND-NOT positive.
6. Compute missing reason words as valid row bits AND-NOT presence.

Packed Boolean predicates skip the temporary slab. Four-valued `All` and `Any`
apply the existing equations through word operations. Reason groups OR their
complete reason-major slabs in one call. `Not` retains copy/swap behavior,
which is already a bulk runtime copy.

All final words mask unused row bits. Exact destination/source slot aliases are
preserved; shifted overlap remains unsupported. Program and Batch validation
still happens before schedule output mutation.

## Selection And Measurement

The dependency continues to own individual kernel guards. The evaluator adds
measured comparison-plus-pack and composite word-stage crossovers because those
costs are not represented by one dependency threshold. Runtime SIMD is disabled
when `simdops.Runtime` reports `PureGo` or the scalar tier. Forced SIMD remains
available internally for differential tests and portable-backend verification.

Paired benchmarks measure scalar and forced SIMD fact leaves, truth/reason
groups, and end-to-end execution over boundary and representative row counts.
The automatic crossover is set only where repeated interleaved measurements
show a gain above the machine's noise floor. Results record CPU, tier, rows,
`ns/op`, bytes, and allocations in `docs/performance.md`.

## Verification

One differential acceptance test compares scalar and forced SIMD schedule
outputs for rows `0, 1, 7, 8, 15, 16, 31, 32, 63, 64, 65`, every facade
threshold plus or minus one, and the measured evaluator crossover. Fixtures
cover all four truth states, every reason plane, dirty partial tails, exact slot
aliases, supported fact kinds, scalar-only shapes, normal dispatch, `purego`,
and 386 fallback. Warm `Execute` and forced schedule calls must allocate zero.
