# Fixed Worker Scheduler Design

**Date:** 2026-08-22

**Status:** Approved by the Phase 9 roadmap and delegated implementation
authority

## Goal

Evaluate large request batches as 64-row-aligned shards on a fixed worker set
while preserving serial semantics, deterministic CSR output, bounded admission,
caller cancellation, and graceful shutdown. The warm scheduler path must create
no goroutine or heap object per batch or shard.

## Considered Row Models

Copying each shard into a compact worker-owned SoA would leave the evaluator
unchanged, but it duplicates every typed fact column before useful work and adds
one buffer family per value kind. Re-evaluating the full batch and retaining only
one row range multiplies useful work. Exposing arbitrary row strides directly on
the public `eval.Batch` contract would push malformed-view validation into every
adapter.

The selected model adds `Executor.ExecuteRange` and keeps its physical view
metadata private to `eval`. A full batch remains the only adapter-facing shape.
The range entry point creates an internal view over a contiguous row interval:

- scalar value columns retain the full backing slices and use source-row stride
  plus row offset;
- packed Boolean and presence columns use source-word stride plus an aligned
  word offset;
- request IDs become one contiguous row slice;
- global evidence columns remain borrowed and immutable; and
- only request-to-evidence CSR offsets are rebased into caller-owned uint32
  scratch, while the corresponding reference edge range is sliced once.

Shard starts must be divisible by 64. A shard end must also be word-aligned
unless it is the source batch tail. This keeps every packed input word exclusive
to one shard and lets existing SIMD kernels consume contiguous slices. Full
`Execute` remains unchanged and constructs no view.

The fact-index builder receives the same active-row count, source stride, and
row offset. It scans only the shard's contiguous segment in each selected symbol
column. Existing zero-value stride fields retain the compact full-batch API.

## Scheduler API And Ownership

```go
type Config struct {
	Capacity     Capacity
	Workers      int
	QueueDepth   int
	ParallelRows uint32
}

func NewScheduler(config Config) (*Scheduler, error)
func (s *Scheduler) Execute(
	ctx context.Context,
	dst *result.Batch,
	p *program.Program,
	batch eval.Batch,
) error
func (s *Scheduler) Close() error
```

`Workers` is the maximum number of simultaneous evaluator kernels.
`QueueDepth` is the exact number of preallocated admitted batch states.
`ParallelRows == 0` selects the measured package default; a nonzero value is a
test and benchmark override. Invalid counts, capacities, or nil execution
arguments fail before admission.

The scheduler allocates at construction:

- one Task 21 `Arena` containing exactly `Workers` complete contexts;
- `Workers` long-lived worker goroutines and one bounded shard-job channel;
- one channel prefilled with `Workers` global work tokens; and
- `QueueDepth` fixed batch states, each with `Workers` row ranges, private
  `result.Batch` values, error slots, and one reusable `sync.WaitGroup`.

No `sync.Pool`, map, per-call result slice, completion channel, worker goroutine,
or shard goroutine is introduced. A fixed worker or the direct serial caller
borrows one arena context for the complete shard, so decoder/evaluator scratch
has exactly one owner. The work token is returned only after that context is no
longer executing.

## Admission And Dispatch

`Execute` first selects among an available batch state, `ctx.Done()`, and the
scheduler stopping signal. After acquiring a state it rechecks shutdown; this
defines the admission boundary. Calls waiting for a state are queued by the
bounded channel and are canceled without consuming one.

An admitted call reserves one work token with cancellation, then takes any
additional immediately available tokens up to the desired shard count. It
never waits while holding a partial set for a fixed larger count, so concurrent
batches cannot deadlock by each reserving part of the budget. Contention
naturally reduces a large batch to fewer shards and leaves service-level
parallelism available to other calls.

Rows are partitioned by bitset words. The total word count is divided as evenly
as possible among the acquired tokens; ranges are contiguous, nonempty, start
on 64-row boundaries, and cover every row exactly once. Zero-row and one-shard
batches use the direct serial path in the calling goroutine. Multiple shards are
sent by value to the fixed job channel; workers create no child goroutines.

Cancellation is checked before admission, while waiting for the first work
token, while submitting each shard, before a worker begins evaluation, and
after all shard completions. If submission is canceled, unsent shards release
their reserved tokens and complete their pre-added wait-group entries. The
caller still waits for already submitted shards before clearing borrowed input
pointers or returning the batch state. Cancellation therefore cannot create a
use-after-return or leave a work token stranded.

Workers write one private result and error slot selected by shard index. They
never append to a shared destination. After the reusable wait group reaches
zero, cancellation takes precedence; otherwise the lowest failing shard index
defines the returned evaluator error.

## Deterministic Merge

Merge validates every private result shape and computes all edge totals before
mutating `dst`. It then resets the full fixed-width result once, reserves each
CSR edge family once, and consumes shards in ascending row order. Outcome IDs
copy into disjoint row ranges. Requirement, driver, evidence, reason, and
remediation offsets add the destination family's current edge base; parallel
driver edge columns append together.

The merge order is independent of worker completion order. Overflow or an
invalid private shape fails before destination mutation. Repeated equal-size
batches retain destination and per-state result capacity, making execution and
merge allocation-free after all batch states and worker contexts are primed.

## Shutdown

`Close` is synchronous and idempotent. The first caller closes the stopping
signal, which rejects new and queued admissions. Already admitted executions do
not observe shutdown as cancellation; they finish or follow their caller
context. Close then receives every preallocated batch state, proving no admitted
call still owns input, result, or completion state. Only then does it close the
job channel and join every worker. Concurrent Close callers wait for the same
closed signal.

No job channel is closed while a producer can still send, and no worker exits
while an admitted call can still require a shard.

## Verification And Measurement

One scheduler acceptance test covers invalid construction, direct serial
equivalence, 63/64/65 and 127/128/129 row range boundaries, exact shard
coverage, deterministic full-result equality across completion orders, bounded
admission, pre-canceled and queued cancellation, concurrent work-budget use,
poisoned state reuse, zero warm allocations, destination atomicity on failure,
and idempotent graceful close. Range tests differentially compare full and
sharded scalar, SIMD, evidence, and indexed execution. Race/checkptr and 386
gates cover worker ownership and widened range arithmetic.

`BenchmarkScheduler` measures direct and forced-parallel execution at several
row and worker counts after priming. Six interleaved runs determine the first
stable complete-evaluator crossover above the repository noise floor. That row
count becomes the zero-config default and is recorded in `docs/performance.md`;
no guessed threshold is shipped.
