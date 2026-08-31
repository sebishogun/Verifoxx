# SQL And PostgreSQL RLS Frontends Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add bounded PostgreSQL expression and RLS compilation plus separate Snowflake and Databricks expression profiles that lower through NornRune's existing semantic Policy and immutable Program.

**Architecture:** A shared allocation-conscious SQL lexer and precedence parser emit directly through `frontend.Builder`; no general object AST or database access is introduced. PostgreSQL RLS rows use SoA columns and CSR role edges, then compose command/role guards, permissive OR, restrictive AND, `USING`, and `WITH CHECK` into one semantic root. Snowflake and Databricks reuse the common scalar grammar behind independent dialect profiles and capability matrices.

**Tech Stack:** Go 1.27, existing `frontend.Policy`/Builder and `internal/frontend.Compiler`, pgx/PostgreSQL 19 integration harness, Go fuzzing, pinned field-alignment analyzer.

---

## Fixed Constraints

- Follow `docs/plans/2026-08-31-strategic-platform-extensions-design.md`.
- Use TDD and observe every intended RED failure before production edits.
- Bound every test, build, vet, fuzz, analyzer, benchmark, and integration command with an outer timeout; every `go test` also gets `-timeout`.
- Never execute source SQL or query a database during parse, lower, compile, or evaluation.
- Keep database access confined to integration tests.
- Keep maps, reflection, parser objects, string conversion, catalog access, and allocation outside evaluator kernels.
- Preserve SQL NULL as NornRune missing/unknown. RLS must use `DefaultReject`; standalone expressions use `DefaultEscalate`.
- Do not claim complete PostgreSQL, Snowflake, or Databricks compatibility.
- Commit and merge only because the user explicitly required Task 54 to be merged before later product-framing work.

### Task 1: Define SQL Profiles, Schema, Parameters, And Diagnostics

**Files:**
- Modify: `frontend/frontend.go`
- Modify: `frontend/frontend_test.go`
- Create: `frontend/sql/frontend.go`
- Create: `frontend/sql/schema.go`
- Create: `frontend/sql/diagnostic.go`
- Create: `frontend/sql/capabilities.go`
- Test: `frontend/sql/frontend_test.go`

**Step 1: Write failing public-contract tests**

Require:

```go
type Dialect uint8
const (
    DialectPostgreSQL Dialect = iota + 1
    DialectSnowflake
    DialectDatabricks
)

type Command uint8
const (
    CommandSelect Command = iota + 1
    CommandInsert
    CommandUpdateUsing
    CommandUpdateCheck
    CommandDelete
)

type Parameter struct {
    Name  string
    Value frontend.Literal
}

type Schema struct {
    Bindings     frontend.BindingSet
    Parameters   []Parameter
    CommandField string
    RoleField    string
}
```

Test stable enum strings, `frontend.LanguageSQL`, profile capability rows,
duplicate/invalid parameters, invalid command/role targets, binding collisions,
UTF-8 metadata, total string limits, and defensive ownership.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend ./frontend/sql
```

Expected: FAIL because `LanguageSQL` and `frontend/sql` do not exist.

**Step 3: Implement the minimal contracts**

Add `LanguageSQL` as the final append-only shared language enum. Define SQL
profile enums, schema validation, a bounded linear parameter lookup helper, and
pointerless SQL diagnostics wrapping exact byte spans, dialect, command, row,
field, and shared diagnostic code. Capability tables must be immutable package
data and identify each feature as Supported, Restricted, or Rejected.

**Step 4: Run GREEN and layout checks**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend ./frontend/sql
timeout 300s ./scripts/check-fieldalignment.sh
timeout 30s git diff --check
```

Expected: PASS with no field-alignment findings.

### Task 2: Implement The Bounded Shared SQL Lexer

**Files:**
- Create: `frontend/sql/token.go`
- Create: `frontend/sql/lexer.go`
- Test: `frontend/sql/lexer_test.go`
- Fuzz: `frontend/sql/fuzz_test.go`

**Step 1: Write failing lexer tests**

Cover identifiers, quoted identifiers, PostgreSQL double quotes and `$1`,
Snowflake `?`/`:name`, Databricks backticks, ASCII case folding, Unicode byte
spans, integer boundaries, doubled string quotes, whitespace, `--` comments,
bounded `/* */` comments, punctuation, comparison operators, keywords, invalid
UTF-8, unterminated input, unknown bytes, source limit, token limit, and
cancellation-free deterministic output.

Require a SoA token stream:

```go
type Tokens struct {
    Kinds  []TokenKind
    Starts []uint32
    Ends   []uint32
    Integers []int64
    IntegerTokenRows []uint32
}
```

Variable token text remains in the one owned source slab; no token owns a
string.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s -run 'TestLex|TestToken' ./frontend/sql
```

Expected: FAIL because the lexer is missing.

**Step 3: Implement one byte-scan lexer**

Pre-size all columns from a bounded source-length hint. Scan source once,
decode UTF-8 only when an identifier requires it, use widened arithmetic before
append, and return at most `MaxDiagnostics` exact-span failures. Dialect profiles
control quote and parameter forms. Reject nested block comments and unsupported
dialect tokens explicitly.

**Step 4: Run GREEN and bounded fuzzing**

```bash
timeout 120s go test -count=1 -timeout 90s ./frontend/sql
timeout 60s go test -count=1 -timeout 50s -run '^$' -fuzz '^FuzzLex$' -fuzztime=10s ./frontend/sql
```

Expected: PASS without panic, timeout, or unbounded allocation.

### Task 3: Parse And Lower Scalar Expressions

**Files:**
- Create: `frontend/sql/parser.go`
- Create: `frontend/sql/expression.go`
- Create: `frontend/sql/postgres/expression.go`
- Create: `frontend/sql/snowflake/expression.go`
- Create: `frontend/sql/databricks/expression.go`
- Test: `frontend/sql/expression_test.go`
- Test: `frontend/sql/postgres/expression_test.go`
- Test: `frontend/sql/snowflake/expression_test.go`
- Test: `frontend/sql/databricks/expression_test.go`
- Create: `testdata/frontends/sql/postgres/expressions.json`
- Create: `testdata/frontends/sql/snowflake/expressions.json`
- Create: `testdata/frontends/sql/databricks/expressions.json`

**Step 1: Write failing precedence and binding tests**

Cover Boolean fields/literals, parentheses, `NOT`, `AND`, `OR`, scalar
comparisons, reversed literal comparisons, `IN`, `IS NULL`, `IS NOT NULL`,
negative integers, bound compile-time parameters, exact identifier binding,
profile case folding, NULL propagation, unknown fields, type errors, empty IN,
field-to-field comparison, `NOT IN`, `= NULL`, casts, functions, subqueries,
arrays, dialect mismatch, depth, nodes, literals, children, and exact source
spans.

Require APIs:

```go
func CompileExpression(
    source []byte,
    dialect sql.Dialect,
    schema sql.Schema,
    limits frontend.Limits,
) (*frontend.Policy, []sql.Diagnostic)
```

and thin profile wrappers in `postgres`, `snowflake`, and `databricks`.

**Step 2: Run RED**

```bash
timeout 150s go test -count=1 -timeout 120s ./frontend/sql/...
```

Expected: FAIL because expression parsing is missing.

**Step 3: Implement precedence parsing directly into the shared Builder**

Use iterative or explicitly depth-bounded recursive descent with this order:
OR, AND, unary NOT, comparison/IS/IN, primary. Resolve fields and parameters by
bounded linear scans over declared slices. Emit:

- `IS NULL` as `NOT Defined(field)`;
- `IS NOT NULL` as `Defined(field)`;
- bare Boolean field as `field = true`;
- SQL NULL only through definedness operations;
- RLS-independent scalar roots with `DefaultEscalate`.

Use small reusable child/literal slices and do not build a second AST.

**Step 4: Run GREEN and semantic compilation tests**

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/sql/... ./internal/frontend
timeout 180s env GOARCH=386 go test -count=1 -timeout 150s ./frontend/sql/...
timeout 180s go test -count=1 -tags=purego -timeout 150s ./frontend/sql/...
```

Expected: PASS with identical shared semantic Policies for equivalent supported
expressions across profiles.

### Task 4: Parse PostgreSQL RLS Definitions Into SoA/CSR Rows

**Files:**
- Create: `frontend/sql/postgres/parser.go`
- Create: `frontend/sql/postgres/rls.go`
- Test: `frontend/sql/postgres/rls_test.go`
- Create: `testdata/frontends/sql/postgres/rls.sql`
- Create: `testdata/frontends/sql/postgres/rls-malformed.sql`

**Step 1: Write failing RLS syntax and ownership tests**

Cover multiple semicolon-delimited `CREATE POLICY` statements, quoted policy and
table identifiers, `AS PERMISSIVE|RESTRICTIVE`, `FOR ALL|SELECT|INSERT|UPDATE|DELETE`,
`TO PUBLIC|role,...`, `USING`, `WITH CHECK`, omitted clauses, comments, duplicate
names, unsupported ALTER/DROP, schema-qualified tables, command/clause mismatch,
unbalanced expressions, role limits, policy limits, CSR ownership, exact spans,
and atomic failure.

Require pointerless tables:

```go
type RLS struct {
    Source []byte
    Modes []PolicyMode
    Commands []PolicyCommand
    UsingRoots []frontend.NodeID
    CheckRoots []frontend.NodeID
    RoleStarts []uint32
    RoleCounts []uint16
    RoleNameStarts []uint32
    RoleNameLengths []uint32
    RoleBytes []byte
    PolicySpans []frontend.Span
}
```

**Step 2: Run RED**

```bash
timeout 150s go test -count=1 -timeout 120s -run 'TestRLS|TestCreatePolicy' ./frontend/sql/postgres
```

Expected: FAIL because RLS parsing is missing.

**Step 3: Implement bounded statement parsing**

Reuse the common token stream and expression parser. Keep all expression roots
in one shared Builder. Validate role and row ownership before publishing the RLS
snapshot. Omitted `USING` is true; omitted `WITH CHECK` inherits `USING` where
PostgreSQL specifies and otherwise is true. Reject unsupported commands rather
than skipping them.

**Step 4: Run GREEN and fuzz statement parsing**

```bash
timeout 150s go test -count=1 -timeout 120s ./frontend/sql/postgres
timeout 60s go test -count=1 -timeout 50s -run '^$' -fuzz '^FuzzRLS$' -fuzztime=10s ./frontend/sql/postgres
```

Expected: PASS with deterministic rows and diagnostics.

### Task 5: Compose RLS Semantics And Compile Through The Native Program

**Files:**
- Create: `internal/frontend/sql/types.go`
- Create: `internal/frontend/sql/lower.go`
- Test: `internal/frontend/sql/conformance_test.go`
- Test: `internal/frontend/sql/lower_test.go`

**Step 1: Write failing RLS composition tests**

For SELECT, INSERT, UPDATE-existing, UPDATE-proposed, and DELETE, cover command
selection, PUBLIC and named roles, no applicable permissive policy, one/many
permissive policies, one/many restrictive policies, ALL policies, omitted
clauses, NULL/missing values, conflicting facts, and malformed semantic tables.
Assert:

- permissive policies OR;
- restrictive policies AND;
- no applicable permissive policy rejects;
- role/command mismatches do not participate;
- RLS unresolved/missing rejects with the original reason;
- destination Program remains unchanged on error or diagnostic.

Require:

```go
type Compiler struct {
    semantic frontend.Compiler
    // reusable SQL composition scratch
}

func (compiler *Compiler) CompileRLS(
    dst *program.Program,
    source []byte,
    schema sql.Schema,
    limits frontend.Limits,
) ([]sql.Diagnostic, error)
```

**Step 2: Run RED**

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/frontend/sql
```

Expected: FAIL because RLS composition/compiler is missing.

**Step 3: Implement formula composition**

For each runtime command value, build:

```text
permissive = OR(role_matches_i AND predicate_i)
restrictive = AND(NOT role_matches_j OR predicate_j)
effective = permissive AND restrictive
root = OR(command_is_k AND effective_k ...)
```

Use Boolean identity nodes when a group has zero or one child because the shared
Builder requires at least two group children. Represent runtime command values
as `select`, `insert`, `update_using`, `update_check`, and `delete`. Finish with
`DefaultReject`, then call the existing atomic `internal/frontend.Compiler`.

**Step 4: Run GREEN and differential native checks**

```bash
timeout 180s go test -count=1 -timeout 150s ./frontend/sql/... ./internal/frontend/sql ./internal/conformance ./internal/e2e
timeout 240s go test -count=1 -timeout 210s -race ./frontend/sql/... ./internal/frontend/sql
```

Expected: PASS; existing assignment baseline R1-R5 remains unchanged.

### Task 6: Add PostgreSQL 19 Differential Tests And Profile Corpora

**Files:**
- Create: `internal/frontend/sql/postgres_integration_test.go`
- Create: `internal/frontend/sql/profile_test.go`
- Modify: `compose.yaml` only if an isolated integration schema is required
- Create: `testdata/frontends/sql/postgres/differential.json`

**Step 1: Write integration tests behind the existing integration tag**

Generate bounded supported expression rows containing NULL-heavy values, integer
boundaries, strings, booleans, and role/command combinations. Evaluate each
expression with PostgreSQL 19 using parameterized test SQL and compare truth;
create temporary tables and policies for RLS combinations and compare row
visibility/check acceptance with NornRune decisions. Database calls remain in
the test package.

**Step 2: Run RED**

```bash
timeout 420s go test -count=1 -tags=integration -timeout 360s ./internal/frontend/sql
```

Expected: at least one differential fixture fails before mapping corrections.

**Step 3: Fix only confirmed semantic mismatches**

Adjust parser/lowering semantics or narrow the capability matrix. Never coerce
unsupported PostgreSQL behavior into a false compatibility claim. Add exact
regression rows for every mismatch.

**Step 4: Run GREEN plus profile corpora**

```bash
timeout 420s go test -count=1 -tags=integration -timeout 360s ./internal/frontend/sql
timeout 180s go test -count=1 -timeout 150s ./frontend/sql/... ./internal/frontend/sql
```

Expected: PASS. Snowflake and Databricks tests are fixture conformance only and
state that no official-engine differential run occurred.

### Task 7: Benchmark And Document Exact Capabilities

**Files:**
- Create: `internal/frontend/sql/benchmark_test.go`
- Create: `docs/sql-frontend.md`
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/frontends.md`
- Modify: `docs/performance.md`
- Modify: `internal/doccheck/frontends_test.go`

**Step 1: Write failing documentation-contract tests**

Require exact PostgreSQL, Snowflake, and Databricks capability matrices; NULL
mapping; parameter restriction; RLS command/role/mode behavior; unsupported
syntax; no catalog/runtime SQL; source-span diagnostics; profile-specific claims;
benchmark methodology; and no `120+ GB/s` claim.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/doccheck
```

Expected: FAIL because the SQL guide is absent.

**Step 3: Write docs and cold-path benchmarks**

Add `BenchmarkSQLParse`, `BenchmarkSQLLower`, and `BenchmarkRLSCompile` with
source size/policy shape reporting. Reuse existing evaluator benchmarks for warm
scalar/SIMD/parallel behavior; do not attribute evaluator throughput to parsing.

**Step 4: Run benchmarks and focused checks**

```bash
timeout 180s go test -count=1 -timeout 150s ./internal/doccheck ./frontend/sql/... ./internal/frontend/sql
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkSQL|BenchmarkRLS' -benchmem -count=6 ./internal/frontend/sql
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkExecutor' -benchmem -count=6 ./internal/eval
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 180s go mod tidy -diff
timeout 30s git diff --check
```

Expected: docs/tests/checks pass; existing executor cases remain `0 B/op`,
`0 allocs/op`.

### Task 8: Review, Release, Commit, And Merge Task 54

**Files:**
- Modify: `docs/plans/2026-08-20-nornrune-policy-engine.md`
- Review: all Task 53 and Task 54 uncommitted files

**Step 1: Run a read-only security/correctness review**

Review malformed-input safety, SQL injection-shaped source, exact spans, RLS
formula semantics, NULL mapping, ownership, atomic failure, allocation boundaries,
dialect claims, integration isolation, and unchanged evaluator kernels. Fix every
confirmed Critical, High, or Medium issue with RED/GREEN tests.

**Step 2: Run the complete bounded matrix**

```bash
timeout 300s go test -count=1 -timeout 240s ./...
timeout 360s go test -count=1 -timeout 300s -race -gcflags=all=-d=checkptr=2 ./...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -tags=purego -timeout 240s ./...
timeout 420s go test -count=1 -tags=integration -timeout 360s ./...
timeout 180s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
timeout 300s go run github.com/goreleaser/goreleaser/v2@v2.12.3 check
timeout 30s git diff --check
```

Expected: PASS with no generated or build artifacts left untracked.

**Step 3: Mark Task 54 complete**

Add `**Status:** Complete (2026-08-31)` beneath the Task 54 heading only after
the matrix passes.

**Step 4: Commit the already-complete Task 53 and Task 54 separately**

Inspect `git status`, `git diff`, and recent log. Stage exact task files only:

```bash
git commit -m "feat: add reviewed natural language frontend"
git commit -m "feat: add sql and postgres rls frontends"
```

Do not amend, skip hooks, or include build output.

**Step 5: Verify from a fresh clone and merge**

Push only after the exact CI matrix passes from a fresh clone of the branch.
Create and inspect a PR, ensure all checks pass, then merge without force. Return
to an updated `main` worktree before beginning later roadmap implementation.
