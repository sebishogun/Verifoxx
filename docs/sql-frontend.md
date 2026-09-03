# SQL Frontend

NornRune provides three bounded SQL compatibility profiles: PostgreSQL 19,
Snowflake, and Databricks. They are not drop-in replacements for those engines.
Accepted text is compiled into NornRune's shared semantic policy and immutable
Program; the frontend does not execute SQL, issue database calls, inspect a
catalog, or enter the per-row evaluator path. The current entry points are Go
APIs, not CLI `--format` values.

## Scalar Expressions

All three profiles accept the same deliberately small expression shape:

- Boolean literals and declared Boolean fields, with `NOT`, `AND`, `OR`, and
  parentheses around complete Boolean expressions.
- Typed string, signed integer, and Boolean field-to-literal comparisons using
  `=`, `<>`, `!=`, `<`, `<=`, `>`, and `>=`; ordered comparisons require
  integers. A literal may appear on either side.
- Non-empty constant `IN` lists whose values match the field type.
- Field `IS NULL` and `IS NOT NULL` tests.
- Declared compile-time parameters: `$n` for PostgreSQL and `:name` or `?` for
  Snowflake and Databricks. Parameter values are typed schema declarations, not
  values read during evaluation. Named markers may be reused; each `?`
  occurrence consumes the next `?` declaration in schema order.

SQL NULL becomes NornRune Missing. A standalone unresolved expression uses
`DefaultEscalate`; true maps to `Approve`, false to `Reject`, and NULL therefore
maps to `Escalate` with its missing reason. NornRune preserves three-valued
Boolean behavior inside the supported expression tree.

Unquoted identifiers follow each profile's folding rule: PostgreSQL and
Databricks use lowercase declarations, while Snowflake uses uppercase
declarations. Quoted identifiers match exactly. Diagnostics are bounded,
pointerless rows with exact half-open source spans measured as UTF-8 byte offsets.
PostgreSQL zero-length quoted identifiers are rejected as syntax errors.

The parser rejects unsupported syntax atomically. Rejected forms include field
comparisons, `NOT IN`, comparison with a NULL literal, functions, casts, joins,
subqueries, statements, queries, DDL outside the PostgreSQL policy grammar, and
catalog access. Parentheses around a scalar field or literal operand are also
outside this bounded grammar; parentheses group complete Boolean expressions.
A scalar input is one expression without a statement terminator. Unknown fields
and type errors have distinct diagnostics.

## Capability Matrix

The public `frontend/sql.Capabilities` API returns an owned matrix. `Supported`
means the documented subset is implemented, `Restricted` means only the stated
shape is accepted, and `Rejected` means the profile fails rather than
approximating the feature.

| Capability | PostgreSQL | Snowflake | Databricks | Boundary |
|---|---|---|---|---|
| `scalar_expressions` | Restricted | Restricted | Restricted | Operators and operands listed above |
| `three_valued_logic` | Restricted | Restricted | Restricted | NULL is Missing within the bounded tree |
| `compile_time_parameters` | Restricted | Restricted | Restricted | Values must be declared before parsing |
| `row_level_security` | Supported | Rejected | Rejected | PostgreSQL `CREATE POLICY` subset only |
| `permissive_and_restrictive_policies` | Supported | absent | absent | PostgreSQL role and command composition |
| `casts_and_functions` | Rejected | Rejected | Rejected | No calls or casts |
| `queries_and_catalog_access` | Rejected | Rejected | Rejected | No query execution or metadata lookup |

## PostgreSQL RLS

`frontend/sql/postgres.CompileRLS` accepts one or more `CREATE POLICY` statements
for one unqualified table. It supports `AS PERMISSIVE` and `AS RESTRICTIVE`,
`FOR ALL`, `SELECT`, `INSERT`, `UPDATE`, and `DELETE`, named role lists and
`PUBLIC`, plus parenthesized `USING` and `WITH CHECK` expressions from the scalar
subset. Policy names must be unique. Qualified tables and clauses PostgreSQL
does not allow for a command are rejected. Policy, table, and role names that
collide with this subset's grammar keywords must use PostgreSQL double quotes.
Unquoted `CURRENT_ROLE`, `CURRENT_USER`, and `SESSION_USER` are rejected because
the offline frontend cannot resolve session state. Their double-quoted forms
remain literal named roles.

The caller declares two string fields: the runtime command and role. Command
values are `select`, `insert`, `update_using`, `update_check`, and `delete`.
These correspond to SELECT, INSERT, UPDATE existing row, UPDATE proposed row,
and DELETE. For each phase, applicable permissive policies OR and applicable
restrictive policies AND:

```text
permissive = OR(role_matches AND predicate)
restrictive = AND(NOT role_matches OR predicate)
effective = permissive AND restrictive
```

No applicable permissive policy denies. A restrictive policy only constrains a
matching role. Unquoted `PUBLIC` matches every role; quoted `"public"` is a
literal named role. `ALL` participates in every command phase. Omitted `USING`
is true; omitted `WITH CHECK` reuses `USING`. INSERT only uses `WITH CHECK`;
SELECT and DELETE only use `USING`; UPDATE evaluates each phase separately. RLS
uses `DefaultReject`, so SQL NULL, a missing runtime command or role, and any
other unresolved policy result fail closed while retaining the original
Missing reason.

This role model compares one declared runtime role. It does not resolve
PostgreSQL role membership, ownership, `BYPASSRLS`, leakproof functions,
security-definer behavior, table inheritance, catalog state, or session state.

## Verification

The PostgreSQL 19 differential integration test starts the pinned
`postgres:19beta3` container. It compares fixed, repository-owned expression
fixtures using parameterized row values and runs isolated temporary RLS
roles, rows, and write operations. Database calls exist only in the
integration-tagged test package.

Snowflake and Databricks use fixture-only parser and native-lowering corpora.
There has been no official-engine differential run for either profile, so their
matrices claim syntax support only, not engine equivalence.

Parsing and compilation are a cold path. Reproduce their allocation and timing
measurements separately from warmed evaluation:

```bash
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkSQLParse|BenchmarkSQLLower|BenchmarkRLSCompile' -benchmem -count=6 ./internal/frontend/sql
timeout 420s go test -count=1 -tags=integration -timeout 360s ./internal/frontend/sql
timeout 180s go test -timeout 150s -run '^$' -bench 'BenchmarkExecutor' -benchmem -count=6 ./internal/eval
```

`BenchmarkSQLParse`, `BenchmarkSQLLower`, and `BenchmarkRLSCompile` include their
documented cold-stage ownership costs. Existing warmed evaluator benchmarks,
not the parser benchmarks, enforce `0 B/op` and `0 allocs/op`. No parser result
is used as an evaluator-throughput or cross-engine performance claim.
