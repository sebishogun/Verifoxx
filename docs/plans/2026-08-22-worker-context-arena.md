# Worker Context Arena Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add bounded, generation-checked ownership of reusable service worker
contexts without `sync.Pool` or warm borrow/return allocations.

**Architecture:** A fixed `[]Context` is prefilled into a bounded channel.
`Borrow` returns a small generation-stamped lease value; atomic context state
rejects stale and double returns. Each context owns the existing decoder,
builder, executor, result storage, byte buffers, and future shard-offset
scratch. A separate output claim prevents return before response bytes are no
longer in use.

**Tech Stack:** Go 1.27, typed reusable workers, bounded channels,
`sync/atomic`, `context.Context`, capacity hints, `testing.AllocsPerRun`, 386,
race/checkptr, vet, and the pinned `fieldalignment` analyzer.

---

### Task 1: Write The Ownership Contract Test

**Files:**
- Create: `internal/scheduler/arena_test.go`

**Step 1: Add one top-level failing test**

Create `TestArenaOwnership` with subtests covering:

- zero or negative construction arguments;
- borrow from a one-context arena;
- a canceled second borrow while the first lease owns the context;
- decoder, builder, executor, and result access through an active lease;
- input/output growth with active bytes preserved;
- return and borrow of the same retained-capacity context;
- stale access after return and after a later generation borrows the context;
- copied-lease double return;
- one active output claim, rejection of a second claim, and
  `ErrOutputEscaped` when context return races ahead of output release;
- bounded concurrent copied returns and claim/return crossings;
- poisoned input/output, offset scratch, and result storage clearing on reuse;
  and
- zero allocations for warm `Borrow` plus `Return`.

Use `package scheduler`, not `scheduler_test`, so the poison subtest can inspect
private active lengths and retained capacities. Keep one top-level acceptance
test and table/subtest helpers rather than many public-contract tests.

The intended API shape is:

```go
capacity := Capacity{InputBytes: 128, OutputBytes: 256, Rows: 65}
arena, err := NewArena(1, capacity)
lease, err := arena.Borrow(context.Background())
executor, err := lease.Executor()
output, err := lease.ClaimOutput()
bytes, err := output.Bytes()
err = output.Release()
err = arena.Return(lease)
```

The zero-allocation subtest must prime once before:

```go
allocs := testing.AllocsPerRun(1000, func() {
	lease, err := arena.Borrow(context.Background())
	if err != nil {
		panic(err)
	}
	if err := arena.Return(lease); err != nil {
		panic(err)
	}
})
if allocs != 0 {
	t.Fatalf("warm borrow/return allocations = %g, want 0", allocs)
}
```

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run '^TestArenaOwnership$'
```

Expected: FAIL because `internal/scheduler`, `Capacity`, `Arena`, and lease
types do not exist.

### Task 2: Implement Capacity-Sized Worker Contexts

**Files:**
- Create: `internal/scheduler/context.go`
- Continue testing: `internal/scheduler/arena_test.go`

**Step 1: Define context-owned data**

Add these types, ordering pointer-bearing fields before atomic/scalar state and
reviewing the final order with `fieldalignment`:

```go
type Capacity struct {
	InputBytes  int
	OutputBytes int
	Rows        uint32
}

type Context struct {
	decoder jsonbatch.Decoder
	builder eval.Builder
	executor eval.Executor
	result result.Batch
	input []byte
	output []byte
	evidenceOffsets []uint32
	arena *Arena
	generation atomic.Uint64
	state atomic.Uint32
	outputClaim atomic.Uint32
}

type Lease struct {
	arena *Arena
	context *Context
	generation uint64
}

type OutputLease struct {
	context *Context
	generation uint64
	claim uint32
}
```

Do not copy a `Context` after its atomic fields are first used. `NewArena`
creates the complete context slice before publishing pointers or setting state.

**Step 2: Validate and grow capacity without truncation**

Implement one widened capacity validator. Reject negative byte hints and any
`Rows+1` count that cannot be represented as a host `[]uint32`. Pre-size input
and output as zero-length slices and evidence offsets as zero-length scratch.

Implement `Lease.Grow(Capacity) error`, `SetInput`, `InputBytes`,
`AppendOutput`, `ResetOutput`, and active-lease accessors for decoder, builder,
executor, and result. Growth only increases capacity and preserves active byte
contents. Accessors return `ErrLeaseExpired` without exposing storage when the
generation or state is stale.

Do not add a generic allocator, map, reflection, callback registry, or
`sync.Pool`.

**Step 3: Implement context reset**

Add a private reset that:

```go
clear(c.evidenceOffsets)
c.evidenceOffsets = c.evidenceOffsets[:0]
c.input = c.input[:0]
c.output = c.output[:0]
_ = c.result.Reset(0)
```

`result.Reset(0)` cannot fail for a nonnil context; keep the impossible error
as an internal panic rather than silently publishing dirty state. Preserve
decoder, builder, and executor bindings and capacities.

### Task 3: Implement Bounded Lease Transfer

**Files:**
- Create: `internal/scheduler/arena.go`
- Modify: `internal/scheduler/context.go`
- Test: `internal/scheduler/arena_test.go`

**Step 1: Define bounded errors and states**

Add sentinel errors for invalid arena/capacity/lease, expired lease, double
return, output already claimed, output not claimed, and escaped output.

Use these states:

```go
const (
	contextAvailable uint32 = iota
	contextBorrowed
	contextReturning
)
```

**Step 2: Construct the fixed arena**

`NewArena(count int, capacity Capacity) (*Arena, error)` must:

1. validate count and capacity before allocation;
2. allocate one `[]Context` and `chan *Context` with exact count;
3. initialize each context's capacity and arena pointer;
4. store `contextAvailable`; and
5. prefill the channel once.

The arena channel transfers scratch ownership only. Do not add scheduler jobs,
workers, close, sharding, or result merging in Task 21.

**Step 3: Borrow with bounded cancellation**

`Borrow(ctx context.Context) (Lease, error)` checks a nil arena/context and a
pre-canceled context before selecting between `available` and `ctx.Done()`.
After receive, CAS `available -> borrowed`, increment a nonzero generation,
and return the lease by value. No goroutine is created.

**Step 4: Return atomically**

`Return(lease Lease) error` validates arena identity and generation, then CASes
`borrowed -> returning`. If an output claim is active, restore `borrowed` and
return `ErrOutputEscaped` without resetting. Otherwise reset, store available,
and return the context to the bounded channel.

A second copied return must receive `ErrDoubleReturn`, never block and never
insert a duplicate pointer. A lease from an earlier generation receives
`ErrLeaseExpired`.

**Step 5: Add output claim lifetime**

`Lease.ClaimOutput` advances the per-context odd/even claim token to an active
odd value and revalidates the generation after the CAS. `OutputLease.Bytes`
exposes a read-only view only while the same generation and exact claim token
remain active. `OutputLease.Release` advances that token exactly once. A
context return cannot succeed while any claim token is active. Output-changing
`Lease` methods reject an active claim. Reject token exhaustion within one
generation and reset the inactive sequence only during return. Before a newly
received context becomes borrowed, retire any transient odd token left by a
failed `ClaimOutput` CAS that raced with the prior return.

**Step 6: Run GREEN and portability checks**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/scheduler -run '^TestArenaOwnership$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/scheduler -run '^TestArenaOwnership$'
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/scheduler -run '^TestArenaOwnership$'
```

Expected: PASS and warm borrow/return reports zero allocations.

### Task 4: Verify Architecture And Repository Gates

**Files:**
- Verify: `internal/scheduler/context.go`
- Verify: `internal/scheduler/arena.go`
- Verify: `internal/scheduler/arena_test.go`
- Verify: `docs/plans/2026-08-22-worker-context-arena-design.md`

**Step 1: Run static gates**

Run independently:

```bash
timeout 60s go vet ./internal/scheduler
timeout 60s env GOARCH=386 go vet ./internal/scheduler
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/scheduler
```

Expected: vet and field alignment print nothing.

**Step 2: Check architecture constraints**

Search production scheduler files for `sync.Pool`, maps, reflection, and
per-borrow goroutine creation. Expect no matches. Confirm the context channel
capacity equals context count and every successful borrow has exactly one
successful return.

**Step 3: Run the one full repository gate**

Run:

```bash
go test -count=1 -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Expected: all tests pass; formatting and whitespace checks print nothing.

**Step 4: Review and checkpoint**

Review the complete uncommitted Task 21 diff against the roadmap, design, and
this plan. Fix every Critical and Important finding and rerun affected bounded
gates. Keep Tasks 18-21 separable in the worktree. Do not commit unless the user
explicitly requests it; the roadmap commit message would be
`feat: add evaluator arena ownership`.
