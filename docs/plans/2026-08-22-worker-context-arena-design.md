# Worker Context Arena Design

**Date:** 2026-08-22

**Status:** Approved by the Phase 9 roadmap and delegated implementation
authority

## Goal

Give every service worker one complete, reusable set of decoder, evaluator,
result, and byte buffers. Ownership must be bounded and exclusive, warm
borrow/return must allocate zero bytes, and no response output may still be
claimed when its context returns for reuse.

Task 21 defines ownership only. Task 22 will add long-lived workers, row
shards, admission, cancellation, and deterministic result merging.

## Considered Models

A channel of raw `*Context` values is small, but copied pointers make stale and
double returns difficult to diagnose. `sync.Pool` has no fixed bound, does not
transfer ownership explicitly, and is excluded by the roadmap. Creating a new
context or goroutine for each request violates the zero-allocation warm path.

The selected model stores a fixed context array behind a bounded channel and
returns a small generation-stamped `Lease` value. The channel supplies
backpressure; per-context atomic state and generation values reject copied,
stale, and double returns without a mutex.

## Owned Context

One `scheduler.Context` owns these same-lifetime components by value:

- one reusable `jsonbatch.Decoder`;
- one reusable `eval.Builder` and its typed batch columns;
- one reusable `eval.Executor` and all evaluator/index scratch;
- one caller-owned `result.Batch`;
- input and encoded-output byte buffers; and
- reusable uint32 row-offset scratch for Task 22 evidence-CSR rebasing.

Decoder, builder, and executor bindings survive return. Their public operations
already validate a new Program and overwrite or clear every active range.
Return only resets service-level active storage: input/output lengths, active
offset scratch, and result rows/CSR lengths. This retains all capacities and
avoids redoing cold Program binding work.

`Capacity` carries nonnegative input/output byte hints and a uint32 row hint.
Construction pre-sizes those three directly owned buffers. `Lease.Grow`
increases capacity without shrinking and preserves active input/output bytes.
Rows are widened and checked before conversion to a host `int`.

## Ownership State Machine

`NewArena(count, capacity)` allocates one contiguous `[]Context`, a channel of
exactly `count`, and the requested initial buffers. Count must be positive. The
channel is prefilled once; no context is created or destroyed during requests.

`Arena.Borrow(ctx)` waits for an available context or returns `ctx.Err()` while
queued. On receipt it transitions `available -> borrowed`, increments a
nonzero generation, and returns this value without allocation:

```go
type Lease struct {
	arena      *Arena
	context    *Context
	generation uint64
}
```

Every lease accessor checks arena identity, generation, and borrowed state.
`Arena.Return(lease)` performs `borrowed -> returning`, resets the context,
publishes `available`, and sends it back to the channel. A copied lease can win
that transition only once; later returns receive a bounded double-return error
instead of blocking or duplicating a channel entry.

The Task 22 work/admission channel is separate from this arena channel. The
arena transfers scratch ownership; the scheduler transfers jobs.

## Output Lifetime

Encoded output is handed to a response writer through a second value handle:

```go
type OutputLease struct {
	context    *Context
	generation uint64
	claim      uint32
}
```

Only one output claim may be active. `Lease.ClaimOutput` advances an odd/even
claim token; `OutputLease.Bytes` validates both the context generation and that
specific active token before exposing the current read-only byte view.
`OutputLease.Release` advances its token exactly once. Returning a context with
an active claim returns `ErrOutputEscaped` and leaves the context borrowed, so
the caller can complete and release the response before retrying return. Stale
output handles reject access after a later same-generation claim as well as
after context reuse. Output-changing `Lease` methods reject an active claim;
the worker must not mutate output concurrently after handing the claim to a
response writer.

The uint32 claim sequence cannot wrap within one context generation. At its
limit, further claims are rejected until return resets the inactive sequence
for the next generation. If a `ClaimOutput` CAS races after `Return` has decided
that no claim is active, the claim cannot have produced a successful handle;
the next borrower retires that transient odd token before activating the new
generation.

As with every borrowed Go slice, callers must not retain the raw byte view after
release. The generation checks enforce the package API boundary; Go cannot
revoke a slice already copied by hostile code.

## Errors And Reset

Sentinel errors distinguish invalid arena/capacity, stale lease, double return,
an already claimed output, and escaped output. Validation occurs before state
or active storage mutation. If output is still claimed, return does not reset
or publish the context.

Reset clears the inactive claim sequence, clears active offset words before
reducing their length, sets input and output lengths to zero, and calls
`result.Batch.Reset(0)`. Existing builder and executor entry points remain
responsible for typed bitmap tails; they already clear or overwrite every
active word. No map, reflection, callback registry, `sync.Pool`, or per-borrow
allocation is introduced.

## Verification

One top-level arena test covers invalid construction, borrow, exclusive
ownership, canceled waiting, growth with preserved bytes, return and reborrow,
stale lease access, copied-lease double return, output claim exclusivity,
escaped-output rejection, poisoned result/offset/byte reuse, retained capacity,
bounded concurrent copied handles, and zero warm borrow/return allocations.

Package gates run normally, on 386, under race/checkptr, through vet and the
pinned production-only field-alignment analyzer, then through the one full
repository test. Task 46 will extend poisoning into scheduler saturation and
cancellation stress; Task 21 keeps the unit contract bounded to ownership and
reset.
