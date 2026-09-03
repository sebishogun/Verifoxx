# Policy Persistence And Reload Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Persist validated policy versions transactionally, make each publication active in PostgreSQL and the immutable Program registry, and reload canonical source by active name or content hash.

**Architecture:** A database-independent `persistence.Publisher` composes a narrow store interface, compiler callback, and `program.Registry`; compilation precedes a cold publication mutex, PostgreSQL commits first, and registry publication/activation follows synchronously. A pgxpool adapter implements only the store transaction and exact source loading.

**Tech Stack:** Go 1.27, pgx/v5 v5.10.0, PostgreSQL 19 Beta 3, Testcontainers for Go v0.44.0, immutable Program registry, SHA-256 source identity.

**Design:** `docs/plans/2026-08-23-policy-persistence-design.md`

**Repository Rule:** Do not create commits unless the user explicitly requests them. The commit checkpoint at the end is optional only.

---

### Task 1: Define Policy Persistence Values And Validation

**Files:**
- Create: `internal/persistence/policy.go`
- Create: `internal/persistence/policy_test.go`

**Step 1: Write failing value-validation tests**

Create package `persistence` tests for:

- zero and negative `PolicyID`/`PolicyVersionID`;
- empty/whitespace name, semantic version, and compiler version;
- empty source, zero hash, and source/hash mismatch;
- zero publication timestamp in a loaded version; and
- one fully valid Candidate and PolicyVersion.

Use one source and exact digest:

```go
source := []byte(`{"name":"policy"}`)
hash := sha256.Sum256(source)
```

Require `errors.Is(err, ErrInvalidPolicyPersistence)` for invalid candidates and
`errors.Is(err, ErrStoredPolicyCorrupt)` for invalid loaded versions.

**Step 2: Run the unit test and verify failure**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/persistence
```

Expected: FAIL because the package and types do not exist.

**Step 3: Implement values, store contract, and validators**

Add:

```go
var (
    ErrInvalidPolicyPersistence = errors.New("persistence: invalid policy operation")
    ErrStoredPolicyNotFound     = errors.New("persistence: stored policy not found")
    ErrPolicyVersionConflict    = errors.New("persistence: policy version conflicts with stored source")
    ErrStoredPolicyCorrupt      = errors.New("persistence: stored policy is corrupt")
    ErrPolicyActivation         = errors.New("persistence: durable policy was not activated")
)

type PolicyID int64
type PolicyVersionID int64

type Candidate struct {
    Source          []byte
    Name            string
    SemanticVersion string
    CompilerVersion string
    ContentHash     [sha256.Size]byte
}

type PolicyVersion struct {
    Source          []byte
    Name            string
    SemanticVersion string
    CompilerVersion string
    PublishedAt     time.Time
    ContentHash     [sha256.Size]byte
    PolicyID        PolicyID
    ID              PolicyVersionID
}

type PolicyStore interface {
    PublishActive(context.Context, Candidate) (PolicyVersion, error)
    LoadActive(context.Context, string) (PolicyVersion, error)
    LoadByHash(context.Context, [sha256.Size]byte) (PolicyVersion, error)
}

type CompileFunc func([]byte) (*program.Program, error)
```

Provide package functions `ValidateCandidate(Candidate) error` and
`ValidatePolicyVersion(PolicyVersion) error`. Require trimmed nonempty text,
exact source digest equality, positive IDs, and nonzero timestamp. Use
`subtle.ConstantTimeCompare` for digests. Validation is cold-path work and must
not retain or clone source.

Order fields after implementation with the pinned field-alignment analyzer.

**Step 4: Run the value tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/persistence
```

Expected: PASS.

### Task 2: Coordinate Compile, Commit, Publish, And Activation

**Files:**
- Modify: `internal/persistence/policy.go`
- Modify: `internal/persistence/policy_test.go`

**Step 1: Write failing publisher tests**

Add a small fake store with function fields and a Program fixture whose symbol
slab resolves policy name/version and whose `InputBytes`/`ContentHash` match.
Test:

- `NewPublisher` rejects nil store, registry, compiler, and blank compiler
  version without invoking dependencies;
- compile failure preserves the exact error and never calls the store;
- store failure occurs after compilation and leaves registry lookup/active
  empty;
- Candidate source/name/version/hash come from the compiled Program, not the
  caller slice after the compiler mutates or copies it;
- successful store return is validated before registry mutation;
- success publishes and activates the Program only after the fake store marks
  its commit complete; and
- duplicate hash publication returns and activates the first canonical
  registry pointer.

The fake store must inspect the registry during `PublishActive` and fail the
test if the hash is visible before it returns.

**Step 2: Run focused publisher tests and verify failure**

```bash
timeout 120s go test -count=1 -timeout 60s -run '^TestPublisher/(constructor|publish)' ./internal/persistence
```

Expected: FAIL because Publisher does not exist.

**Step 3: Implement Publisher construction and Publish**

Add:

```go
type Publisher struct {
    store           PolicyStore
    compile         CompileFunc
    registry        *program.Registry
    compilerVersion string
    publish         sync.Mutex
}

func NewPublisher(
    store PolicyStore,
    registry *program.Registry,
    compile CompileFunc,
    compilerVersion string,
) (*Publisher, error)

func (publisher *Publisher) Publish(
    ctx context.Context,
    source []byte,
) (*program.Program, PolicyVersion, error)
```

`Publish` must:

1. Reject nil receiver/context, empty source, and canceled context.
2. Call the compiler before locking.
3. Require a nonnil Program, exact `Program.InputBytes == source`, matching
   SHA-256 hash, and nonempty resolvable `PolicyName`/`PolicyVersion` symbols.
4. Build Candidate from Program-owned bytes and metadata.
5. Lock `publish`, recheck context, and call `store.PublishActive`.
6. Validate the returned row and require it to match candidate name, version,
   hash, and source. Permit an older stored compiler version on idempotent
   duplicate publication.
7. Publish the Program, validate any existing canonical pointer against the
   stored row, activate by hash, and return it.

Wrap any post-store registry invariant failure with `ErrPolicyActivation`; do
not pretend the durable transaction rolled back. Registry operations do not
use the caller context after a successful store return.

**Step 4: Run publisher tests**

```bash
timeout 120s go test -count=1 -timeout 60s -run '^TestPublisher/(constructor|publish)' ./internal/persistence
```

Expected: PASS.

### Task 3: Reload Durable Source Into The Registry

**Files:**
- Modify: `internal/persistence/policy.go`
- Modify: `internal/persistence/policy_test.go`

**Step 1: Write failing reload and concurrency tests**

Cover:

- `ReloadActive` requests the exact name and `ReloadHash` requests the exact
  hash;
- store not-found errors retain `ErrStoredPolicyNotFound`;
- corrupt source/hash and mismatched compiled name/version/hash never activate;
- an empty Registry compiles, publishes, and activates loaded source;
- an already-cached hash does not invoke the compiler again;
- a failed reload leaves an existing active Program unchanged;
- compiler callbacks run outside Registry locks; and
- two concurrent Publish calls compile before the Publisher mutex, enter the
  fake store one at a time, and leave Registry.Active equal to the second
  durable commit.

Use channels for concurrency ordering; do not use sleeps or unbounded receives.

**Step 2: Run focused reload tests and verify failure**

```bash
timeout 120s go test -count=1 -timeout 60s -run '^TestPublisher/(reload|concurrent)' ./internal/persistence
```

Expected: FAIL because reload methods do not exist.

**Step 3: Implement reload methods**

```go
func (publisher *Publisher) ReloadActive(
    ctx context.Context,
    name string,
) (*program.Program, PolicyVersion, error)

func (publisher *Publisher) ReloadHash(
    ctx context.Context,
    hash [sha256.Size]byte,
) (*program.Program, PolicyVersion, error)
```

Both methods validate input, lock `publish`, load and validate the stored row,
then call `Registry.LoadOrCompile`. The callback compiles `version.Source` and
requires exact retained source/hash/name/version. Activate only after all
checks succeed. An already cached Program must also be checked against the row
before activation.

Keep one internal `activateLoaded` helper shared by both methods. Do not add a
cache, goroutine, notification, or retry loop.

**Step 4: Run all persistence unit tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/persistence
timeout 180s go test -count=1 -timeout 120s -race -gcflags=all=-d=checkptr=2 ./internal/persistence
```

Expected: PASS.

### Task 4: Persist Active Versions Through pgx

**Files:**
- Create: `internal/adapters/postgres/policy.go`
- Create: `internal/adapters/postgres/policy_test.go`
- Modify: `internal/adapters/postgres/migrations_integration_test.go`

**Step 1: Add tagged PostgreSQL store tests**

Start `policy_test.go` with `//go:build integration` and package `postgres`.
Add calls in `TestPostgreSQLMigrations`, before `standalone_migrator`, for
sequential subtests implemented in the new file:

```go
t.Run("policy_store_publish", ...)
t.Run("policy_store_load", ...)
```

Each subtest resets the database and applies `000001_initial`. Initially call
the wished-for `NewPolicyStore(environment.runtime)` API.

The publish subtest must assert:

- one policy and one version row are inserted;
- source bytes and SHA-256 hash round trip exactly;
- compiler version and publication timestamp are retained;
- `active_version_id` is the returned version ID;
- republishing the same Candidate returns the same IDs/timestamp and leaves one
  version row;
- the same policy/semantic version with changed source returns
  `ErrPolicyVersionConflict` and leaves the active pointer unchanged; and
- direct runtime update/delete of the immutable version remain denied.

The load subtest verifies active name, hash lookup, absent name/hash errors, and
quote-containing policy names through bound parameters.

**Step 2: Run store subtests and verify failure**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/policy_store_(publish|load)$' ./internal/adapters/postgres
```

Expected: FAIL because PolicyStore does not exist.

**Step 3: Implement the pgx PolicyStore**

Add:

```go
type PolicyStore struct {
    pool *pgxpool.Pool
}

func NewPolicyStore(pool *pgxpool.Pool) (*PolicyStore, error)
func (store *PolicyStore) PublishActive(ctx context.Context, candidate persistence.Candidate) (persistence.PolicyVersion, error)
func (store *PolicyStore) LoadActive(ctx context.Context, name string) (persistence.PolicyVersion, error)
func (store *PolicyStore) LoadByHash(ctx context.Context, hash [sha256.Size]byte) (persistence.PolicyVersion, error)
```

Use one transaction for PublishActive:

```sql
INSERT INTO nornrune.policies (name)
VALUES ($1)
ON CONFLICT (name) DO NOTHING
RETURNING id;

INSERT INTO nornrune.policy_versions
    (policy_id, semantic_version, source, content_hash, compiler_version)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (content_hash) DO NOTHING
RETURNING id, published_at;

UPDATE nornrune.policies
SET active_version_id = $1
WHERE id = $2;
```

On `pgx.ErrNoRows`, select the existing policy/version inside the same
transaction. For duplicate hashes, compare policy ID, name, semantic version,
source, and digest exactly; preserve the stored compiler version/timestamp.
Map only constraint `policy_versions_policy_semantic_version_key` to
`ErrPolicyVersionConflict` using `errors.As(*pgconn.PgError)`. Wrap all other
errors without SQL text, source, DSN, or credentials.

Factor one row scanner for both load queries. Scan `bytea` hash into `[]byte`,
require 32 bytes, and copy into the fixed array. Rollback with a five-second
context derived from `context.WithoutCancel(ctx)`.

**Step 4: Run store integration tests**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/policy_store_(publish|load)$' ./internal/adapters/postgres
```

Expected: PASS.

### Task 5: Integrate Real Compilation, Reload, And Concurrency

**Files:**
- Modify: `internal/adapters/postgres/policy_test.go`
- Modify if tests expose defects: `internal/persistence/policy.go`
- Modify if tests expose defects: `internal/adapters/postgres/policy.go`

**Step 1: Write failing end-to-end publication tests**

Add a test-only real compiler using `nornrune.NewSchema`, `jsonpolicy.Decoder`,
`ast.Builder`, and `compile.Lowerer`. Do not import unexported CLI engine code.

Add parent subtests before `standalone_migrator`:

```text
policy_publish_reload
policy_concurrent_publish
```

`policy_publish_reload` must:

1. Construct a PostgreSQL PolicyStore, empty Registry, and Publisher.
2. Publish the embedded source and require database plus registry activation.
3. Publish it again and require the same database IDs and canonical Program
   pointer.
4. Construct a fresh Registry/Publisher, call ReloadActive and ReloadHash, and
   require exact source/hash/name/version and active pointer.
5. Change source without changing semantic version and require conflict with no
   registry change.

`policy_concurrent_publish` must use bounded goroutines to cover:

- identical source converging on one version and canonical Program;
- two valid source documents with distinct semantic versions retaining two
  rows; and
- final `policies.active_version_id`, Registry.Active content hash, and the
  Publisher's last durable completion agreeing.

Also call two PolicyStore instances directly for identical Candidates so real
PostgreSQL unique-index serialization is tested independently of the
Publisher mutex.

**Step 2: Run end-to-end tests and verify failure**

```bash
timeout 360s go test -count=1 -timeout 300s -tags=integration -run '^TestPostgreSQLMigrations$/policy_(publish_reload|concurrent_publish)$' ./internal/adapters/postgres
```

Expected: FAIL on any incomplete orchestration or concurrency behavior.

**Step 3: Make minimal coordinator/store corrections**

Keep compilation outside the Publisher mutex, database commit before Registry
mutation, and Registry reads unchanged. Do not add advisory locks, session
locks, global maps, persistence goroutines, or evaluator integration.

**Step 4: Run all Task 28 tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/persistence
timeout 360s go test -count=1 -timeout 300s -tags=integration ./internal/adapters/postgres
timeout 480s go test -count=1 -timeout 420s -race -tags=integration ./internal/adapters/postgres
```

Expected: PASS.

### Task 6: Complete Task 28 Verification Gates

**Files:**
- Modify only files identified by gate failures.

**Step 1: Normalize modules and formatting**

```bash
timeout 120s go mod tidy
timeout 120s go mod verify
test -z "$(gofmt -l .)"
git diff --check
```

Expected: modules verify and no formatting/whitespace diagnostics are printed.

**Step 2: Run default portability and race gates**

```bash
timeout 180s go test -count=1 -timeout 120s ./...
timeout 180s go test -count=1 -timeout 120s -tags=purego ./...
timeout 180s env GOARCH=386 go test -count=1 -timeout 120s ./...
timeout 240s go test -count=1 -timeout 180s -race -gcflags=all=-d=checkptr=2 ./...
```

Expected: PASS without starting Docker.

**Step 3: Run fresh PostgreSQL integration gates**

```bash
timeout 120s docker compose --env-file .env.example config --quiet
timeout 360s go test -count=1 -timeout 300s -tags=integration ./internal/adapters/postgres
timeout 480s go test -count=1 -timeout 420s -race -tags=integration ./internal/adapters/postgres
```

Expected: PASS against PostgreSQL major version 19 with every container cleaned
up.

**Step 4: Run static and layout gates**

```bash
timeout 120s go vet ./...
timeout 120s env GOARCH=386 go vet ./...
timeout 180s go vet -tags=integration ./internal/adapters/postgres
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/persistence ./internal/adapters/postgres
```

Expected: no diagnostics. Review each field-order suggestion rather than
applying analyzer fixes blindly.

**Step 5: Recheck build and immutable CLI output**

```bash
timeout 120s go build -trimpath -o /tmp/opencode/nornrune-task28 ./cmd/nornrune
timeout 120s go run ./cmd/nornrune evaluate > /tmp/opencode/task28-results.json
cmp /tmp/opencode/task28-results.json results/requests.json
```

Expected: build success and byte-identical machine-readable output.

**Step 6: Request a read-only code review**

Review Task 28 against this plan and the approved design, focusing on commit
ordering, source/hash integrity, duplicate races, runtime privileges, error
leaks, registry stale-state windows, and integration-test false positives. Fix
all Critical and Important findings through a failing test first, then rerun
the affected and full gates.

**Step 7: Optional commit checkpoint**

If the user explicitly requests a commit:

```bash
git add internal/persistence/policy.go internal/persistence/policy_test.go internal/adapters/postgres/policy.go internal/adapters/postgres/policy_test.go internal/adapters/postgres/migrations_integration_test.go docs/plans/2026-08-23-policy-persistence-design.md docs/plans/2026-08-23-policy-persistence.md
git commit -m "feat: persist and reload policy versions"
```
