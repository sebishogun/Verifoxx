# Development

NornRune targets Go 1.27.0. The repository developer surface is `devx`, a Cobra
command whose no-argument mode opens a fuzzy menu and whose named workflows are
scriptable. The [`Makefile`](../Makefile) is a one-to-one facade over the same
commands; workflow logic stays in Go.

## Bootstrap

From the repository root, inspect prerequisites and per-workflow availability:

```bash
./cli/devx doctor
./cli/devx status
```

`doctor` probes Go 1.27, Docker and Compose, Delve, Buf, protoc, benchstat, and
the PostgreSQL client. It exits nonzero if any probe is missing, including tools
needed only by optional workflows. `status` instead reports `runnable` or the
specific blocker for every workflow.

The repository wrapper uses a matching executable `bin/devx-$GOOS-$GOARCH`
when present and otherwise falls back to `go run ./cmd/devx`. It always runs
from the repository root. An installed dispatcher searches parent directories
for the nearest repository with `cli/devx`.

## Build And Run

Build the product binary at `bin/nornrune`:

```bash
timeout 180s ./cli/devx build
```

Other useful paths are:

```bash
./cli/devx demo
./cli/devx build:purego
./cli/devx clean
```

`build:purego` selects portable scalar wrappers. `build:exp` is intentionally
blocked while the pinned SIMD dependency's experiment-gated vector API is
incompatible with Go 1.27; normal builds still use its runtime-dispatched
whole-slice kernels.

## Test Matrix

Run the default fresh suite:

```bash
timeout 180s ./cli/devx test
```

Additional lanes are:

```bash
timeout 180s ./cli/devx test:unit
timeout 420s ./cli/devx test:integration
timeout 720s ./cli/devx test:e2e
timeout 300s ./cli/devx test:race
```

Every `go test` command has both an outer process timeout and Go's `-timeout`.
Integration and end-to-end lanes require Docker. Do not run tests in a watch or
repeat loop. Use `-count=1` when fresh evidence matters.

Normal, `purego`, and `GOARCH=386` scalar-fallback builds must remain
semantically equivalent. Hot-path changes also require `-benchmem`, escape
analysis where relevant, and review of the pinned `fieldalignment` analyzer's
recommendations rather than blind field reordering.

Debugger dashboard changes require the focused graph, terminal, and PostgreSQL
adapter suites in addition to the default tests:

```bash
timeout 150s go test -count=1 -timeout 120s \
  ./internal/graphview ./internal/adapters/tui \
  ./internal/adapters/cli ./internal/adapters/postgres
timeout 120s go test -timeout 90s -run '^$' \
  -bench 'BenchmarkGraphRenderer|BenchmarkLayout' -benchmem -count=6 \
  ./internal/adapters/tui ./internal/graphview
```

Run the semantic worker and TUI in separate terminals for resize and
alternate-screen checks. Exercise `[AST]`/`[PROGRAM]`, Session/Persisted
history, request focus, stepping, restart, and clean exit. PostgreSQL history is
optional and reads `NORNRUNE_DATABASE_URL`; the Session timeline remains usable
without it.

## Generated And Canonical Files

Check policy, result, and protobuf drift with:

```bash
./cli/devx policy:check
./cli/devx results:check
./cli/devx proto:check
```

Regeneration commands are explicit:

```bash
./cli/devx results:gen
./cli/devx proto:gen
```

Review generated diffs before committing. The policy is authored JSON and is
validated or compiled rather than generated.

## Compatibility Frontend Verification

The compile-time adapters pin `cel.dev/cel-go v0.32.0`,
`github.com/open-policy-agent/opa v1.19.1`,
`github.com/cedar-policy/cedar-go v1.8.0`, and
`google.golang.org/protobuf v1.36.12`. Capability names are stable test and
documentation identifiers; they are not claims of full language support.
The complete matrices, binding schema, generator workflow, limits, and decision
mapping are in the [compatibility frontend guide](frontends.md).

- CEL: `boolean_literals`, `scalar_variables`, `object_selection`,
  `scalar_comparisons`, `logical_operators`, and `constant_list_membership`
  are supported. `function_calls`, `macros_and_comprehensions`, and
  `maps_messages_and_optionals` are rejected.
- Rego: `rego_v1_modules`, `complete_boolean_decisions`, `boolean_defaults`,
  `multiple_rules`, `conjunctive_bodies`, `static_input_references`,
  `scalar_comparisons`, `constant_membership`, and
  `presence_aware_negation` are supported. `imports_and_data`,
  `functions_and_recursion`, `variables_and_comprehensions`, and
  `with_and_unsupported_builtins` are rejected.
- Cedar: `static_permit_forbid`, `equality_scopes`,
  `context_scalar_conditions`, `boolean_composition`, and
  `forbid_precedence` are supported. `entity_hierarchy_and_attributes`,
  `sets_records_and_extensions`, and `templates_and_annotations` are rejected.

Run the bounded conformance and fuzz-seed corpus with:

```bash
timeout 240s go test -count=1 -timeout 210s ./frontend/... ./internal/frontend ./internal/doccheck
timeout 120s go test -count=1 -timeout 90s -run '^Fuzz(Compile|SemanticPolicy)$' ./frontend/cel ./frontend/rego ./frontend/cedar ./internal/frontend
```

## Database Work

Use the workflows documented in [database](database.md):

```bash
./cli/devx db:up
./cli/devx migrate:check
./cli/devx graph:check
```

`migrate:create --name lower_snake_case` creates an exclusive up/down pair with
mode `0600`. Integration migration checks use isolated containers.

## Code Boundaries

- `internal/ast`, `compile`, `program`, `eval`, `truth`, `result`, and `index`
  are the transport-independent core.
- JSON, CLI, TUI, HTTP, gRPC, and PostgreSQL are adapters.
- Per-row, per-node, and per-record paths must not allocate.
- Use struct-of-arrays columns and CSR edges for bulk scans and variable-degree
  relationships.
- Pre-size storage, reuse caller-owned destinations, and keep mutable lifetime
  groups under one explicit owner.
- Do not introduce `map[string]any`, reflection, string conversion, database
  access, or callbacks into evaluator kernels.
- Parallelize on existing row/word boundaries only when measured work exceeds
  coordination cost.

## Documentation And Review

Technical Markdown contracts and local links are tested by
[`internal/doccheck`](../internal/doccheck). Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/doccheck
git diff --check
```

The source-of-truth design and ordered roadmap are under
[`docs/plans`](plans). Performance claims must follow the
[benchmark methodology](performance.md#methodology), and concurrency changes
must preserve the [lock table](concurrency.md#lock-table).
