# Immutable Policy Registry Design

**Date:** 2026-08-22
**Status:** Approved by the user
**Roadmap:** Task 23, Phase 9

## Goal

Publish frozen compiled policies by SHA-256 content hash without locking registry
lookups. Concurrent publication must not lose entries, active evaluations must
retain the exact Program pointer they loaded, and concurrent requests for the
same absent hash must compile it once.

## Scope And Constraints

- Create only `internal/program/registry.go` and its tests.
- Use `Program.ContentHash` as the `[32]byte` registry key.
- Treat Programs returned by `compile.Lower` as frozen and immutable under the
  existing Program contract; do not clone the complete Program again.
- Keep lookup and active-policy reads lock-free and allocation-free.
- Compile outside publication and compile-flight locks.
- Keep publication copy-on-write and rare; no lock enters evaluator kernels.
- Do not add deletion, version aliases, persistence, notification, eviction,
  metrics, or context cancellation in this task.

## Alternatives

A mutable map behind `sync.RWMutex` is smaller but adds synchronization to every
lookup and contradicts the approved architecture. Sorted parallel hash and
Program slices avoid maps but require repeated 32-byte comparisons and more
insertion machinery. An atomic pointer to an immutable map snapshot gives one
atomic load and one map lookup on the read path, while keeping all mutation on
the cold publication path.

## API

The zero value of `Registry` is usable.

```go
var (
    ErrInvalidRegistry = errors.New("program: invalid registry operation")
    ErrPolicyNotFound  = errors.New("program: policy not found")
)

type CompileFunc func() (*Program, error)

func (r *Registry) Lookup(hash [32]byte) (*Program, bool)
func (r *Registry) Publish(candidate *Program) (*Program, error)
func (r *Registry) LoadOrCompile(hash [32]byte, compile CompileFunc) (*Program, error)
func (r *Registry) Activate(hash [32]byte) error
func (r *Registry) Active() *Program
```

`Publish` and `LoadOrCompile` return the canonical registry pointer. The first
successful publication for a hash wins; later duplicate candidates are not
installed. Publication does not implicitly change the active default.

## Data And Ownership

```go
type registrySnapshot struct {
    programs map[[32]byte]*Program
}

type compileFlight struct {
    done    chan struct{}
    program *Program
    err     error
}

type Registry struct {
    snapshot atomic.Pointer[registrySnapshot]
    active   atomic.Pointer[Program]

    publishMu sync.Mutex
    flightMu  sync.Mutex
    flights   map[[32]byte]*compileFlight
}
```

Published snapshot maps are never mutated. A publication mutex prevents lost
copy-on-write updates. The current snapshot retains every published Program;
callers may retain returned pointers independently. A later snapshot or active
pointer replacement cannot modify or invalidate an earlier Program.

The flight map is mutable only under `flightMu` and contains absent hashes that
are currently compiling. It is a cold-path coordination table, not evaluator
data. Each unique active compilation allocates one completion channel and one
flight object. Different hashes do not serialize during compilation.

## Lookup, Publication, And Activation

`Lookup` atomically loads the current snapshot and performs a read-only map
lookup. A nil snapshot is an empty registry. `Active` is one atomic pointer
load.

`Publish` rejects a nil candidate or zero content hash before locking. Under
`publishMu` it reloads the current snapshot, returns an existing pointer for a
duplicate hash, allocates a map sized for one additional entry, copies every
existing pair, inserts the candidate, and atomically stores the new snapshot.
No old map is modified.

`Activate` first resolves the hash through `Lookup`. An absent or zero hash
returns `ErrPolicyNotFound`; otherwise it atomically stores that exact Program
pointer. Publication and activation are separate so persistence can commit a
version before making it the default in later roadmap tasks.

## Duplicate Compilation

`LoadOrCompile` first performs lock-free lookup. On a miss it briefly locks
`flightMu`, rechecks lookup, and either joins an existing flight or installs a
new one. Joiners release the mutex before waiting on the flight channel.

The leader calls `CompileFunc` with no registry mutex held. A nil Program, zero
requested hash, nil callback, or returned `ContentHash` mismatch becomes
`ErrInvalidRegistry`. A valid result goes through `Publish`, which resolves a
race with direct publication by returning the already-canonical pointer.

The leader stores the Program and error in the flight, removes the map entry,
and closes `done`. Channel close publishes those fields to every waiter. Failed
flights publish nothing and are removed, so a later call may retry. Compiler
errors are returned unchanged to all callers in that flight.

## Errors

Nil receivers for mutating methods, nil candidates, nil compile callbacks, zero
compile hashes, and returned hash mismatches use `ErrInvalidRegistry`.
Read-only `Lookup` and `Active` treat a nil receiver as an empty registry.
`Activate` uses `ErrPolicyNotFound` for zero or absent hashes. User compiler
errors retain their identity. Failed operations do not alter the snapshot or
active pointer.

## Verification

One concurrent acceptance test covers:

- zero-value lookup, publication, duplicate first-writer identity, and explicit
  activation;
- copy-on-write snapshots remaining unchanged after later publication;
- an evaluation-held old Program pointer surviving active replacement;
- simultaneous different-hash publication without lost entries;
- one callback and one canonical pointer for many same-hash callers;
- different hashes compiling concurrently without a global compile lock;
- compiler-error fan-out, no publication, and a successful retry;
- invalid receiver, candidate, callback, zero hash, and hash mismatch atomicity;
- zero warm allocations for `Lookup` and `Active`; and
- native, race/checkptr, 386, vet, field-alignment, formatting, and full-repo
  gates.
