# Reused Fact Bitmap Indexes Design

**Date:** 2026-08-22

**Status:** Approved by the Phase 8 roadmap and delegated implementation
authority

## Goal

Avoid rescanning one categorical batch column for every distinct policy
predicate. The compiler identifies symbol fields used by multiple exact
predicates. The executor then builds bounded per-value row bitmaps once per
batch and reuses them for `Equal`, `NotEqual`, and `In` leaves when paired
benchmarks show that build-plus-query beats direct execution.

The scalar evaluator remains the semantic reference. Missing facts retain the
missing reason, unsupported shapes retain their existing scalar or SIMD path,
and automatic indexing must allocate zero bytes after executor priming.

## Considered Layouts

### Index every observed batch value

A sorted or open-address table could map every distinct observed value to a
bitmap. This supports more value kinds, but memory grows as distinct batch
values times bitmap words. High-cardinality columns can therefore approach a
quadratic byte count, even when the policy queries only a few constants.

### Discover reuse in each executor

An executor could rescan the instruction columns when binding a Program and
derive its own index plan. This avoids new Program columns, but repeats compiler
work, moves policy decisions into the runtime, and contradicts the roadmap's
compiler-selected boundary.

### Index only queried categorical constants

The selected design stores only symbol values referenced by reusable exact
predicates. Runtime memory is bounded by policy constants, not observed batch
cardinality. Program symbol IDs are dense, so construction uses a reusable
direct lookup table rather than hashing or per-row binary search.

## Compiled Specification

`index.FactSpec` is immutable SoA metadata embedded in `program.Program`:

- sorted `FieldIDs`;
- zero-based symbol `Columns`;
- per-field `ValueStarts` and `ValueCounts`;
- sorted unique queried `Values`; and
- per-field `UseCounts`, measured in avoided full-column equality scans.

One `Equal` or `NotEqual` contributes one use. An `In` contributes its list
length because the existing scalar implementation scans the full column once
per list value. Only symbol fields and those three opcodes participate.
Booleans are already bit-packed; ordered integer and timestamp predicates keep
the Task 19 SIMD path. The compiler emits a candidate field only when its use
count reaches the benchmark-selected reuse threshold.

The specification is built after instruction normalization and CSE, so counts
describe the final executable schedule. It is cloned by `Program.Freeze`, reset
with the other generated indexes, and validated as sorted, unique, in-range,
and consistent with `FieldIndex`.

## Runtime Index

`index.FactBuilder` owns a reusable `[]uint32` value-to-mask-row table sized to
`ProgramSymbolCount+1`. `index.FactIndex` owns a reusable flat `ValueMasks` slab;
mask row `n` occupies `WordCount` contiguous uint64 words and corresponds to
`FactSpec.Values[n]`.

For each selected field, construction performs these bulk steps:

1. Install that field's queried symbol IDs in the direct lookup table.
2. Scan the field's contiguous symbol column once.
3. Ignore symbol zero and batch-extension symbols above `ProgramSymbolCount`.
4. Set the request bit in the matching value mask when the lookup is nonzero.
5. Clear only the lookup entries installed for that field before continuing.

All active mask words are cleared before construction. Since only valid row
bits are set, final tails remain zero. Missing rows normally contain symbol
zero; evaluator presence masks remain authoritative and are applied when a
leaf turns a value bitmap into four-valued truth and missing-reason output.

The builder validates every shape and widened product before mutating the
published index. It returns bounded invalid-spec or too-large errors rather
than allowing an `int` conversion to overflow on 386.

## Evaluator Integration

Execution modes remain independently measurable:

- automatic: build the batch index only at measured row and reuse crossovers,
  then use SIMD or scalar fallback for other leaves;
- scalar: disable both the index and SIMD;
- SIMD: disable the index and force whole-slice SIMD where supported; and
- indexed: force the compiled batch index and use scalar fallback elsewhere.

The public `Executor.Execute` uses automatic mode. The index is built once
after Program and Batch validation but before result mutation. For an indexed
symbol leaf:

- `Equal` copies the value mask into positive truth;
- `NotEqual` uses present-and-not-mask for positive truth;
- `In` ORs the masks for its values;
- negative truth is presence minus positive truth; and
- missing reasons are the valid row mask minus presence, exactly as in the
  scalar reference.

An unknown batch-extension symbol never matches a compiled policy value. An
unsupported opcode, kind, unselected field, or disabled crossover falls through
without mutation to the existing SIMD/scalar evaluator.

## Measurement And Selection

Paired benchmarks compare complete direct execution with index build plus
reuse from one through 128 value uses, with concentrated cases around the
selected 96-use boundary. They cover row tails, the 64-row automatic boundary,
sparse matches, dense matches, and multiple symbol cardinalities. Each
benchmark primes reusable storage, reports allocations, and uses repeated runs
on the selected runtime tier.

The compiler reuse threshold and runtime row threshold are set only where the
indexed path remains outside measured noise at that shape and larger shapes.
If no robust crossover exists, automatic mode remains direct while forced mode
retains differential coverage and the benchmark evidence is recorded.

The measured conservative thresholds are 96 final-schedule value uses and 64
batch rows. Exact results, commands, and the memory formula are recorded in
`docs/performance.md`.

## Verification

One top-level acceptance test covers exact masks, missing values, sparse and
dense matches, irrelevant fields, unknown extension symbols, `Equal`,
`NotEqual`, `In`, dirty-tail reuse, large-small-large reuse, malformed specs,
386 overflow guards, direct/index/SIMD outcome equivalence, and zero warm
allocations. Package gates run under normal, `purego`, 386, race/checkptr, vet,
and the pinned production-only field-alignment analyzer before the one full
repository test.
