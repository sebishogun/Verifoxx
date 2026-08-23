# Immutable Policy Registry Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Publish frozen Programs by content hash through lock-free immutable snapshots, explicit active-policy selection, and duplicate compilation suppression.

**Architecture:** `Registry` atomically points to a map that is never mutated after publication and separately stores the active Program pointer. A short mutex serializes copy-on-write map replacement; another mutex protects only a cold in-flight compilation table, while compiler callbacks run without locks.

**Tech Stack:** Go 1.27 generics, `sync.Mutex`, `sync/atomic.Pointer`, immutable maps, channels for flight completion, race/checkptr, 386, fieldalignment.

**Design:** `docs/plans/2026-08-22-immutable-policy-registry-design.md`

**Constraint:** Keep all work uncommitted unless the user explicitly requests a commit.

---

### Task 1: Define Lock-Free Empty Reads And Registry Errors

**Files:**
- Create: `internal/program/registry.go`
- Create: `internal/program/registry_test.go`

**Step 1: Write the failing basic registry test**

Add `TestRegistry` with an `empty reads and invalid operations` subtest. Use a
small hash helper and a Program containing only identity fields needed by the
registry:

```go
func registryHash(seed byte) [32]byte {
    var hash [32]byte
    hash[0] = seed
    return hash
}

func registryProgram(seed byte) *Program {
    return &Program{ContentHash: registryHash(seed), InputBytes: []byte{seed}}
}
```

Require zero-value and nil `Lookup` to return `(nil, false)`, zero-value and nil
`Active` to return nil, and nil receivers/candidates/callbacks to return bounded
sentinels without invoking callbacks or mutating state.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry$'
```

Expected: FAIL because `Registry`, `ErrInvalidRegistry`, and
`ErrPolicyNotFound` do not exist.

**Step 3: Add the storage types and read APIs**

Implement:

```go
var (
    ErrInvalidRegistry = errors.New("program: invalid registry operation")
    ErrPolicyNotFound  = errors.New("program: policy not found")
)

type CompileFunc func() (*Program, error)

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
    flights  map[[32]byte]*compileFlight
    publishMu sync.Mutex
    flightMu  sync.Mutex
}

func (r *Registry) Lookup(hash [32]byte) (*Program, bool) {
    if r == nil {
        return nil, false
    }
    snapshot := r.snapshot.Load()
    if snapshot == nil {
        return nil, false
    }
    program, found := snapshot.programs[hash]
    return program, found
}

func (r *Registry) Active() *Program {
    if r == nil {
        return nil
    }
    return r.active.Load()
}
```

Order fields deliberately, then let the pinned analyzer confirm rather than
blindly applying its suggestion.

**Step 4: Run the focused GREEN test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry$'
```

Expected: PASS for the implemented empty-read cases. Do not commit.

### Task 2: Add Copy-On-Write Publication And Explicit Activation

**Files:**
- Modify: `internal/program/registry.go`
- Modify: `internal/program/registry_test.go`

**Step 1: Extend the test with publication invariants**

Add subtests covering:

- first publication and exact pointer lookup;
- duplicate-hash first-writer identity;
- old snapshot map contents unchanged after a second publication;
- explicit activation only after publication;
- replacing active while a retained old pointer and its bytes remain valid;
- nil candidate and zero hash leaving snapshot and active pointers unchanged;
- activating zero or absent hashes returning `ErrPolicyNotFound`.

Capture `oldSnapshot := registry.snapshot.Load()` before the second publication
and inspect it after publication from the package-internal test. Never mutate a
published Program or snapshot in the test.

**Step 2: Run the focused RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry/(publication|activation)'
```

Expected: FAIL because `Publish` and `Activate` do not exist.

**Step 3: Implement publication and activation**

Use a comparable zero hash helper and explicit unlocks:

```go
func validRegistryHash(hash [32]byte) bool {
    return hash != [32]byte{}
}

func (r *Registry) Publish(candidate *Program) (*Program, error) {
    if r == nil || candidate == nil || !validRegistryHash(candidate.ContentHash) {
        return nil, ErrInvalidRegistry
    }
    hash := candidate.ContentHash
    r.publishMu.Lock()
    current := r.snapshot.Load()
    if current != nil {
        if existing, found := current.programs[hash]; found {
            r.publishMu.Unlock()
            return existing, nil
        }
    }
    size := 1
    if current != nil {
        size += len(current.programs)
    }
    programs := make(map[[32]byte]*Program, size)
    if current != nil {
        for key, program := range current.programs {
            programs[key] = program
        }
    }
    programs[hash] = candidate
    r.snapshot.Store(&registrySnapshot{programs: programs})
    r.publishMu.Unlock()
    return candidate, nil
}

func (r *Registry) Activate(hash [32]byte) error {
    if r == nil {
        return ErrInvalidRegistry
    }
    program, found := r.Lookup(hash)
    if !validRegistryHash(hash) || !found {
        return ErrPolicyNotFound
    }
    r.active.Store(program)
    return nil
}
```

Do not alter `active` inside `Publish`.

**Step 4: Run publication GREEN**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry$'
```

Expected: PASS. Do not commit.

### Task 3: Suppress Duplicate Compilation Without Serializing Different Hashes

**Files:**
- Modify: `internal/program/registry.go`
- Modify: `internal/program/registry_test.go`

**Step 1: Add deterministic concurrent tests**

Add subtests for:

- many callers released from one start barrier for one absent hash;
- a blocking callback proving it is invoked exactly once;
- every caller receiving the same canonical Program pointer;
- two different hashes whose callbacks both enter before either is released;
- simultaneous direct publications of different hashes retaining every entry;
- a direct publication racing a compile and winning canonical identity.

Use channels and atomics, not sleeps. Bound any spin used to observe both
different-hash callbacks and always release blocked callbacks before failing.

**Step 2: Run the concurrent RED test**

Run:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry/(duplicate_compilation|different_hashes|concurrent_publication)'
```

Expected: FAIL because `LoadOrCompile` does not exist.

**Step 3: Implement compile flights**

Implement the fast lookup, short flight-table critical sections, and unlocked
callback:

```go
func (r *Registry) LoadOrCompile(hash [32]byte, compile CompileFunc) (*Program, error) {
    if r == nil || compile == nil || !validRegistryHash(hash) {
        return nil, ErrInvalidRegistry
    }
    if program, found := r.Lookup(hash); found {
        return program, nil
    }

    r.flightMu.Lock()
    if program, found := r.Lookup(hash); found {
        r.flightMu.Unlock()
        return program, nil
    }
    if flight := r.flights[hash]; flight != nil {
        r.flightMu.Unlock()
        <-flight.done
        return flight.program, flight.err
    }
    if r.flights == nil {
        r.flights = make(map[[32]byte]*compileFlight)
    }
    flight := &compileFlight{done: make(chan struct{})}
    r.flights[hash] = flight
    r.flightMu.Unlock()

    candidate, err := compile()
    if err == nil {
        if candidate == nil || candidate.ContentHash != hash {
            err = ErrInvalidRegistry
        } else {
            candidate, err = r.Publish(candidate)
        }
    }

    r.flightMu.Lock()
    flight.program = candidate
    flight.err = err
    delete(r.flights, hash)
    close(flight.done)
    r.flightMu.Unlock()
    return candidate, err
}
```

Do not hold `flightMu` while waiting, compiling, or publishing. Do not add
`singleflight`, `sync.Map`, string conversion, or a global compile semaphore.

**Step 4: Run concurrent GREEN and race detection**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry$'
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/program -run '^TestRegistry$'
```

Expected: PASS with one callback for the same hash and overlapping callbacks for
different hashes. Do not commit.

### Task 4: Verify Failure Fan-Out, Retry, And Read-Path Allocations

**Files:**
- Modify: `internal/program/registry_test.go`

**Step 1: Add failure and retry coverage**

Use a shared sentinel compiler error and a blocked first callback. Require all
concurrent callers in that flight to receive `errors.Is(err, sentinel)`, require
the hash to remain absent, then retry with a valid Program and require one
successful publication. Add nil-Program and hash-mismatch callbacks that return
`ErrInvalidRegistry` and leave snapshot and active state unchanged.

**Step 2: Add allocation checks**

After publishing and activating one Program, use `testing.AllocsPerRun(1000, ...)`
for `Lookup` and `Active`. Store returned pointers in outer variables so calls
cannot be eliminated. Require exactly `0` allocations for each read API.

**Step 3: Run focused allocation and 386 gates**

Run independently:

```bash
go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry$'
GOARCH=386 go test -count=1 -timeout 60s ./internal/program -run '^TestRegistry$'
```

Expected: PASS and zero warm read allocations. Do not commit.

### Task 5: Run Static, Architecture, Review, And Repository Gates

**Files:**
- Verify: `internal/program/registry.go`
- Verify: `internal/program/registry_test.go`
- Verify: `docs/plans/2026-08-22-immutable-policy-registry-design.md`
- Verify: `docs/plans/2026-08-22-immutable-policy-registry.md`

**Step 1: Run static and layout checks independently**

```bash
timeout 60s go vet ./internal/program
timeout 60s env GOARCH=386 go vet ./internal/program
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/program
```

Expected: every command exits zero and prints nothing. Review any field order
suggestion against read locality and pointer-scan bytes before editing.

**Step 2: Inspect the architecture**

Confirm production registry lookup contains no mutation, lock, allocation,
string conversion, reflection, callback, or goroutine. Confirm callbacks occur
with neither mutex held; published snapshot maps are never modified; active
replacement is one atomic store; and compiler output identity is verified before
publication. Inspect package-specific `-gcflags=-m=2`; snapshot and flight
construction may escape, while `Lookup` and `Active` must not allocate.

**Step 3: Run final bounded tests**

```bash
go test -count=1 -timeout 60s ./internal/program
GOARCH=386 go test -count=1 -timeout 60s ./internal/program
go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 60s ./internal/program
go test -count=1 -timeout 60s ./...
go test -count=1 -tags=purego -timeout 60s ./...
timeout 30s gofmt -l .
git diff --check
```

Expected: all tests pass; formatting and whitespace checks print nothing.

**Step 4: Request separate specification and code-quality reviews**

Review against roadmap Task 23,
`docs/plans/2026-08-20-verifoxx-policy-engine-design.md`, and the approved design.
Fix every Critical and Important finding, add a RED regression for each behavior
defect, and rerun the affected bounded commands plus the full repository test.
Do not commit; the roadmap commit message, if later requested, is
`feat: publish immutable policy programs`.
