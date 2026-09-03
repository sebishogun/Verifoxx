# Policy Persistence And Reload Design

**Status:** Approved

**Date:** 2026-08-23

## Goal

Persist validated policy source and immutable publication metadata in
PostgreSQL, make each successful publication the active policy version, and
publish the corresponding frozen Program into the existing lock-free registry.
Reload must rebuild the active Program from canonical source without placing a
database call on any evaluation path.

Task 28 owns policy publication and reload only. Policy graph projection,
LISTEN/NOTIFY, service startup wiring, HTTP endpoints, and audit records remain
in Tasks 30-33.

## Component Boundary

`internal/persistence/policy.go` defines a database-independent coordinator and
store interface. It imports `internal/program` but no adapter or JSON package.
`internal/adapters/postgres/policy.go` implements the store with pgxpool and
imports no decoder, compiler, or registry implementation.

```go
type PolicyID int64
type PolicyVersionID int64

type Candidate struct {
    Source          []byte
    Name            string
    SemanticVersion string
    CompilerVersion string
    ContentHash     [32]byte
}

type PolicyVersion struct {
    Source          []byte
    Name            string
    SemanticVersion string
    CompilerVersion string
    PublishedAt     time.Time
    ContentHash     [32]byte
    PolicyID        PolicyID
    ID              PolicyVersionID
}

type PolicyStore interface {
    PublishActive(context.Context, Candidate) (PolicyVersion, error)
    LoadActive(context.Context, string) (PolicyVersion, error)
    LoadByHash(context.Context, [32]byte) (PolicyVersion, error)
}

type CompileFunc func([]byte) (*program.Program, error)

func NewPublisher(
    store PolicyStore,
    registry *program.Registry,
    compile CompileFunc,
    compilerVersion string,
) (*Publisher, error)

func (p *Publisher) Publish(
    context.Context,
    []byte,
) (*program.Program, PolicyVersion, error)

func (p *Publisher) ReloadActive(
    context.Context,
    string,
) (*program.Program, PolicyVersion, error)

func (p *Publisher) ReloadHash(
    context.Context,
    [32]byte,
) (*program.Program, PolicyVersion, error)
```

The PostgreSQL adapter exposes a constructor over `*pgxpool.Pool` and satisfies
`PolicyStore`. Later service wiring supplies the real compiler and runtime-role
pool.

## Data And Ownership

Compilation is the authority for candidate identity. `Publisher.Publish`
resolves the policy name and semantic version through the frozen Program's
symbol table and uses `Program.InputBytes` plus `Program.ContentHash` for the
candidate. It never accepts caller metadata separately from source.

Candidate slices are borrowed only for one store call. pgx-loaded
`PolicyVersion.Source` owns its returned bytes. A Program owns an independent
frozen source copy, so row buffers, compiler scratch, and caller input may be
reused after publication.

The original compiler version and publication time remain immutable on a
duplicate hash. Republishing identical source under a newer binary returns the
original stored row and makes it active again; it does not rewrite historical
metadata.

## Publish Flow

Publication follows this order:

1. Validate dependencies, source, and caller context.
2. Compile outside all publication and registry locks.
3. Validate the Program's nonzero hash, exact retained source, and resolvable
   nonempty name/version.
4. Build a Candidate from the Program and current compiler version.
5. Acquire the Publisher's cold-path mutex.
6. Call `PolicyStore.PublishActive`.
7. After a successful database commit, call `Registry.Publish` and retain its
   canonical Program pointer.
8. Activate that canonical pointer by content hash.
9. Release the Publisher mutex and return the stored row plus Program.

The mutex serializes database commit and in-memory activation within one
process. Compilation remains concurrent and occurs before the mutex. The
existing registry publication lock remains short and internal to Registry; no
database operation runs while that lock is held.

If compilation or the transaction fails, the registry remains unchanged. A
process crash after commit but before activation leaves PostgreSQL canonical;
startup reload repairs local memory. An unexpected registry failure after
commit is reported distinctly with the stored version retained in the error
context, without attempting to roll back durable state.

## PostgreSQL Transaction

`PublishActive` uses one read-committed transaction through the runtime role:

1. Insert the policy name with `ON CONFLICT DO NOTHING RETURNING id`.
2. If another transaction already inserted it, select its stable ID.
3. Insert the immutable policy version with content hash as the idempotency
   conflict target.
4. If the hash already exists, load that exact row and verify policy identity,
   semantic version, source bytes, and hash.
5. Map a unique `(policy_id, semantic_version)` conflict with different content
   to `ErrPolicyVersionConflict`.
6. Update only `policies.active_version_id` to the stored version ID.
7. Commit and return the complete row.

The insert path never uses a no-op policy-name update, because the runtime role
has column-scoped update permission only for `active_version_id`. All values
are bound pgx parameters. Exact source bytes remain `bytea`; no JSON
re-encoding or source canonicalization occurs.

Concurrent identical publications converge on one version row. Concurrent
distinct semantic versions retain every immutable row; the transaction that
updates the policy row last determines the database active pointer. One
Publisher serializes local commit and registry activation in that same order.
Task 31 notifications converge other processes.

## Reload Flow

`ReloadActive(name)` and `ReloadHash(hash)` run under the Publisher mutex:

1. Load the complete stored row.
2. Require positive IDs, nonzero publication time, nonempty metadata, a
   nonzero 32-byte hash, and exact `SHA-256(source) == content_hash`.
3. Call `Registry.LoadOrCompile` for the stored hash. The callback compiles the
   stored source with no registry lock held.
4. Verify the resulting Program's retained source, hash, name, and semantic
   version against the row.
5. Activate the canonical registry pointer.

`ReloadActive` is the startup path. Task 31 also re-runs `ReloadActive` for a
notification and compares its durable active hash with the payload hash. The
payload is only a hint: a stale or foreign hash never activates its row.
`ReloadHash` remains available for an explicit trusted hash lookup and is
idempotent when the Program is already present. Compiler version changes do
not alter the content-hash registry key; one process runs one compiler build,
while the database retains the compiler version used for the original
publication.

## Errors

The persistence package defines distinct sentinels for:

- invalid publisher, store, source, compiler version, or returned Program;
- stored policy not found;
- semantic-version conflict;
- corrupt or inconsistent stored policy; and
- database-committed but registry publication/activation failure.

Compiler errors preserve identity. The PostgreSQL adapter maps `pgx.ErrNoRows`
and the named semantic-version unique constraint to domain errors, wraps all
other pgx failures with operation context, and never includes source bytes,
connection strings, passwords, or complete environments.

Cancellation before commit leaves no database or registry mutation. After a
successful commit, registry publication completes synchronously without using
the canceled context so the local process does not intentionally remain stale.

## Tests

Untagged unit tests use a fake store and compiler to cover:

- constructor and input validation;
- compile-before-store ordering;
- compile failure causing no store or registry mutation;
- store failure leaving the registry unchanged;
- candidate metadata derived only from the frozen Program;
- duplicate publication returning the canonical registry pointer;
- stored hash/source/name/version corruption rejected before activation;
- active and hash reload into an empty registry;
- post-commit registry invariant errors; and
- concurrent local publication preserving commit/activation order.

Tagged PostgreSQL 19 tests reuse Task 27's one-container test and run before its
destructive no-runtime-role subtest. They cover:

- first publication and active-pointer update;
- exact source, hash, semantic version, compiler version, and timestamp round
  trips;
- duplicate-hash idempotency and one immutable version row;
- same policy/version with changed source returning a conflict;
- direct immutable-row update/delete rejection;
- load by active name and content hash;
- reload into a fresh Registry with real policy compilation;
- quote-containing metadata through bound parameters;
- concurrent identical and distinct-version publication without lost rows;
- database and registry active-hash agreement; and
- transaction rollback and registry atomicity on database errors.

Default, purego, 386, and non-integration race suites remain Docker-free. Task
completion also requires PostgreSQL integration and integration-race runs,
native/386/integration vet, field alignment, formatting, Compose validation,
the full build, and byte-identical default evaluation output.

## Exclusions

Task 28 does not persist compiled Program slabs, policy graph rows, requests,
evidence, or decisions. It does not send notifications, watch PostgreSQL,
configure service startup, expose policy endpoints, or add database calls to
CLI demonstration and evaluator kernels.
