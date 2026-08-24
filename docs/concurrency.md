# Concurrency

Verifoxx uses fixed ownership budgets and immutable policy snapshots. Channels
transfer exclusive mutable state; locks protect cold metadata and lifecycle
state. No mutex is acquired by an `eval.Executor` instruction kernel.

## Ownership Diagram

```text
HTTP/gRPC goroutines
       |
       v
service admission slots (fixed QueueDepth)
       |
       v
engine workers (fixed Workers)
  decoder | batch builder | evaluator | result | encoder | audit scratch
       |
       +---------------------> response
       |
       v
journal slots (fixed AuditQueueDepth) -> writer goroutines -> PostgreSQL
```

An admitted call owns one admission generation until the response has been
encoded and any required audit commit is acknowledged. It borrows exactly one
engine worker through a bounded channel. The worker's mutable slices never
become shared state and are returned as one lifetime group.

## Evaluation Workers

`server.Engine` creates `Workers` complete workspaces before serving. A request
waits on the worker channel under its request deadline, executes serially in
that workspace, then returns it. This provides service-level parallelism
without a decoder, result, or audit allocation per concurrent request.

The reusable [`scheduler.Scheduler`](../internal/scheduler/scheduler.go) is a
separate bulk execution component. It owns:

- a fixed worker goroutine set;
- one preallocated batch-state slab per queue position;
- a token budget that prevents nested requests from claiming all workers;
- private result storage per shard; and
- one deterministic merge in ascending row order.

Automatic row sharding starts at 256 rows. Non-final shards begin and end at
64-row bitmap boundaries. One-worker and smaller-batch execution stay on the
direct path. The database-backed server currently uses fixed engine workers and
does not nest the row scheduler inside each request.

## Lock Table

| Component | Synchronization | Protected state | Hot-path effect |
|---|---|---|---|
| active policy | `atomic.Pointer[Program]` | default immutable program | one lock-free load per evaluation |
| registry snapshot | `atomic.Pointer` | immutable hash-to-program map | lock-free lookup |
| `Registry.publish` | `sync.Mutex` | copy-on-write snapshot publication | cold publication only |
| `Registry.flight` | `sync.Mutex` | duplicate compilation flights by hash | reload/compile only |
| `Publisher.publish` | `sync.Mutex` | commit-to-local-activation interval | compile occurs before lock; no evaluation use |
| `Engine.versionMu` | `sync.RWMutex` | policy hash to durable version ID | short read before worker acquisition |
| admission `Service.mu` | `sync.Mutex` | accepting, queued, and active counters | request boundary only |
| admission slots | atomics plus bounded channel | generation-stamped capacity ownership | no allocation while waiting |
| scheduler context | atomics plus bounded channel | lease generation and output claim | ownership checks only |
| journal | channels, atomics, `sync.Once` | slot transfer, counters, one shutdown | one batch submission |
| lifecycle | `sync.Once` and atomic started flag | one shutdown sequence | shutdown only |
| debug session | actor command channel | all retained semantic state | debug build only |
| debug transport | `requestMu` / `pendingMu` | cancellation and response correlation maps | semantic debugger only |
| policy listener | atomics and `sync.Once` | counters and first-ready signal | background PostgreSQL connection |
| migrations | PostgreSQL transaction advisory lock | schema migration sequence | migration process only |

The registry holds immutable `Program` pointers, so a snapshot copy does not
copy program payload. The maps in the registry and debug transport are outside
per-row and per-node paths.

## Lock Order

Policy publication has one explicit nesting direction:

```text
compile with no publication lock
    -> Publisher.publish
        -> PostgreSQL transaction
        -> Registry.publish
```

No registry operation calls back into the publisher, and evaluation never
acquires either publication mutex. `Engine.versionMu` is not held while waiting
for an engine worker, evaluating, encoding, or writing an audit record.

Admission accounting is independent of engine-worker and journal channels. A
request may wait in that order, but no return path acquires them in reverse:
the journal copies a complete audit batch before submission returns, the engine
worker is returned, and the adapter finally releases admission.

## Backpressure

Three independent bounds prevent goroutine-driven memory growth:

1. `QueueDepth` creates that many active admission slots and permits at most the
   same number of additional queued callers; a full waiting budget returns
   `service busy`.
2. `Workers` bounds complete mutable evaluation workspaces.
3. `AuditQueueDepth` bounds admitted PostgreSQL audit slots, including active
   writes.

Required audit mode waits for a journal slot and commit under the request
context. Best-effort mode never waits for a full slot: it increments `dropped`
and returns the evaluation result. Off mode starts no journal goroutines.

## Cancellation

HTTP and gRPC create a per-call deadline before admission. Cancellation wakes
admission and engine-worker waits. Once an engine worker begins decoding, the
current evaluator runs that bounded batch to completion rather than checking
the context per row or instruction. The deadline still governs required audit
submission. Once a PostgreSQL transaction has begun, commit and rollback use
short contexts derived with `context.WithoutCancel` so the storage boundary
reaches a known state after client cancellation.

The semantic debugger serializes mutable execution through one actor. A
`Continue` checks its context between instructions; `Pause` takes effect after
the current instruction. The Unix transport may correlate concurrent commands,
but all of them ultimately enter the same actor queue.

## Shutdown

The lifecycle performs one bounded sequence:

1. stop accepting and wake queued admissions;
2. drain active evaluations while reserving the journal flush budget;
3. close and drain the audit journal;
4. stop HTTP and gRPC sessions;
5. close the PostgreSQL pool; and
6. join any configured workers.

A caller that stops waiting does not cancel the process-owned cleanup goroutine.
See [operations](operations.md) for timeout configuration.

## Race Verification

Run the complete race lane through its bounded developer workflow:

```bash
timeout 300s ./cli/devx test:race
```

Targeted ownership and failure tests live in
[`internal/scheduler`](../internal/scheduler),
[`internal/service`](../internal/service),
[`internal/program`](../internal/program), and
[`internal/adapters/postgres`](../internal/adapters/postgres).
