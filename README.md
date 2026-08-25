# Verifoxx

Verifoxx is a deterministic evidence-aware policy engine for the Verifoxx AI
Engineer semantic decision representation exercise. It compiles the three
natural-language requirements into a reusable semantic policy, evaluates the
five supplied requests, and returns exactly one of `Approve`, `Reject`,
`Revise`, or `Escalate` with bounded provenance and uncertainty.

The default command embeds the policy, requests, and evidence, so the required
submission runs without a database or network service.

## Dependencies

- Go 1.27 for source builds and tests.
- Linux or macOS for the semantic TUI, which uses a local Unix socket.
- Docker Engine with the Compose v2 plugin for container modes.
- PostgreSQL is not required for embedded evaluation. Full Compose mode starts
  PostgreSQL 19 beta and applies the embedded migrations.

## Quick Start

### Embedded CLI

Run the complete demonstration from the repository root:

```bash
timeout 120s go run ./cmd/verifoxx demo
```

Produce only the machine-readable R1-R5 results:

```bash
timeout 120s go run ./cmd/verifoxx evaluate
```

The checked-in result is [`results/requests.json`](results/requests.json). To
verify it against a fresh embedded evaluation:

```bash
timeout 120s go run ./cmd/verifoxx evaluate | cmp - results/requests.json
```

Useful commands are:

```text
validate                 validate a policy document
compile                  compile and summarize a policy
evaluate                 evaluate a request batch as JSON
bench                    benchmark deterministic offline evaluation
explain R1               explain one request
simulate R1 --set K=V    evaluate one bounded field override
demo                     run evaluation and revision scenarios
graph --output PATH      export the AST or Program semantic graph
```

### Offline Benchmark

Measure warmed scheduler execution with a deterministic typed batch generated
from the embedded fixtures:

```bash
timeout 120s go run ./cmd/verifoxx bench --rows 4096 --iterations 100 --workers 4
```

`bench` accepts no policy, request, evidence, stdin, or network payload. Bounds
are 1-65,536 rows, 1-100,000 iterations, and 1-256 workers. Its single JSON
report includes workload shape, serial or parallel execution mode, SIMD tier,
elapsed nanoseconds, rows per second, allocated bytes, and allocations. Fixture
setup, direct-result verification, scheduler construction, and enough priming
runs to warm every fixed context and admission state occur before measurement.

### Semantic Graph Export

Export a deterministic standalone SVG:

```bash
timeout 120s go run ./cmd/verifoxx graph \
  --view ast --format svg --output /tmp/verifoxx-ast.svg --force
```

`--view` accepts `ast` or `program`; `--format` accepts `dot`, `svg`, or `html`.
DOT is suitable for Graphviz, SVG is directly viewable, and HTML embeds both
graphs with pan, zoom, fit, graph switching, and node details. Files are created
with mode `0600`; an existing destination is rejected unless `--force` is set.

### Semantic TUI

The TUI is a client for a retained semantic debug session. With the installed
Neovim configuration, open the repository, run `:DapContinue`, and select
`Debug Verifoxx`. Neovim imports `.vscode/launch.json`, starts an ephemeral
Delve DAP server, and launches `debug-worker`. Once `.verifoxx/debug.sock`
exists, connect from a terminal:

```bash
./cli/devx debug:tui
```

`./cli/devx debug` and `./cli/devx debug:dap` are aliases for a fixed
`127.0.0.1:38697` Delve DAP server when an editor-managed ephemeral server is
not desired. Both wait for a DAP launch request; neither starts the worker by
itself.

Without an editor, start the worker directly in one terminal:

```bash
mkdir -p .verifoxx && chmod 700 .verifoxx
timeout 30m go run ./cmd/verifoxx debug-worker --socket "$PWD/.verifoxx/debug.sock"
```

Connect from a second terminal:

```bash
timeout 30m go run ./cmd/verifoxx tui --socket "$PWD/.verifoxx/debug.sock"
```

Add `--browser` to start a synchronized viewer on an ephemeral
`127.0.0.1` port. The browser shows the same AST and Program graphs while the
terminal remains the debugger controller. If the desktop opener fails, the TUI
prints the loopback URL and continues running.

In the browser, use Tab to enter the graph, left/right to move between nodes at
the same depth, and up/down to follow incoming/outgoing relationships. Enter or
Space selects the focused node. Dense edge labels are suppressed only when they
would overlap; the inspector always lists every typed relationship for the
selected node. Pointer pan, wheel zoom, Fit, AST, and Program controls remain
available.

The alternate-screen dashboard follows terminal resizes and keeps Requests,
Graph, Runtime, and Breakpoints/Watches in bounded panes. Graphs that fit use
labeled boxes; larger graphs use a complete fit-to-pane topology with current
node and typed relationship details below it. The browser remains the richer
pan-and-zoom labeled view.

Use `s`, `n`, and `o` to step; `c` to continue; and `a` and `p` to switch the
visible `[AST]` and `[PROGRAM]` tabs. `h` opens a bounded 64-stop Session
history, `tab` switches to Persisted history, `j`/`k` select history rows, and
`esc` returns focus to Requests. Persisted history is optional: when
`VERIFOXX_DATABASE_URL` is set, the TUI lazily loads at most 64 newest audit
findings for the selected request; when unset, that tab reports that persistence
is not configured. Database failures remain inside the history pane and never
stop stepping. Credentials are not displayed.

Supply the same custom `--policy`, `--requests`, and `--evidence` paths to both
commands when not using the embedded pack. Press `q` to restore the prior screen
and quit.

### Docker

Build and run the isolated demonstration:

```bash
timeout 600s docker build -t verifoxx:local .
timeout 120s docker run --rm verifoxx:local demo
```

The image is a non-root `scratch` image. Its default command is `evaluate`.

### Full Compose

Start PostgreSQL, migrations, and the HTTP/gRPC service with health-based
dependencies:

```bash
timeout 600s docker compose --profile full up --build --wait
curl -fsS http://127.0.0.1:8080/readyz
```

HTTP listens on `127.0.0.1:8080`, gRPC on `127.0.0.1:9090`, and PostgreSQL on
`127.0.0.1:5432`. The defaults in `compose.yaml` are local-development
credentials only. Stop the stack and remove its data with:

```bash
timeout 120s docker compose down -v
```

## Input Format

`evaluate`, `demo`, `graph`, `tui`, and `debug-worker` accept three JSON documents:

- `--policy PATH`: semantic policy; default
  [`policies/verifoxx/policy.json`](policies/verifoxx/policy.json).
- `--requests PATH`: request pack; default
  [`internal/fixtures/verifoxx-requests.json`](internal/fixtures/verifoxx-requests.json).
- `--evidence PATH`: evidence pack; default
  [`internal/fixtures/verifoxx-evidence.json`](internal/fixtures/verifoxx-evidence.json).

Omit a flag to use its embedded document. For non-interactive commands, one path
may be `-` to read that document from standard input; more than one stdin source
is rejected. `tui` reserves stdin for terminal input and requires embedded or
file-based sources.

```bash
timeout 120s go run ./cmd/verifoxx evaluate \
  --policy policies/verifoxx/policy.json \
  --requests internal/fixtures/verifoxx-requests.json \
  --evidence internal/fixtures/verifoxx-evidence.json
```

The policy declares versioned evidence and outcome catalogs, applicability
expressions, clauses, evidence obligations, uncertainty-specific resolutions,
and bounded remediations. Requests contain typed requester, action,
environment, usage, and evidence-reference fields. Evidence records contain a
typed kind plus bounded attributes such as status, timing, subject, and scope.
Unknown fields, malformed IDs, invalid references, excessive depth, and
oversized inputs are rejected.

## Output Format

Successful evaluation writes one JSON document to stdout. Diagnostics go to
stderr. Exit code `0` means success, `1` means an operational or semantic
failure, and `2` means invalid command usage.

The top level contains `schema_version`, policy identity and SHA-256,
`engine_version`, and `results`. Every result contains:

- `request_id` and `decision`.
- `rationale` and the driving requirement/clause/reason.
- `requirements_applied` and `evidence_used`.
- `missing_or_conflicting_evidence`.
- `assumptions` and `unresolved_uncertainty`.
- `remediation`, which is empty unless a bounded correction is allowed.

The supplied pack evaluates to R1 `Approve`, R2 `Reject`, R3 `Revise`, and R4
and R5 `Escalate`. These are computed from the policy graph; the evaluator does
not branch on request IDs.

## Development

Run the native test suite with explicit process and test deadlines:

```bash
timeout 120s go test -count=1 -timeout 60s ./...
timeout 300s ./scripts/check-fieldalignment.sh
```

`./cli/devx` and the `Makefile` provide the same bounded build, test, database,
debug, benchmark, and container workflows. Run `./cli/devx status` to see which
optional tools are available.

The field-alignment script is the local and CI production-layout gate. It pins
the reviewed analyzer version and checks `internal`, `cmd`, and `policies`
packages without automatically rewriting structs.

See the one-page [design note](docs/design-note.md) for the semantic model and
[AI usage disclosure](docs/ai-usage.md) for tool assistance.

## Technical Guides

- [Architecture](docs/architecture.md): boundaries, ownership, and data layout.
- [Policy language](docs/policy-language.md): expressions, four-state truth,
  resolution, and remediation.
- [Concurrency](docs/concurrency.md): worker ownership, lock table,
  backpressure, cancellation, and shutdown.
- [Database](docs/database.md): PostgreSQL 19 beta, migrations, publication,
  audit storage, graph projection, and recovery.
- [API](docs/api.md): HTTP and gRPC contracts and examples.
- [Development](docs/development.md): setup, build, test, and generation
  workflows.
- [Operations](docs/operations.md): configuration, health, metrics, capacity,
  and service runbooks.
- [Debugging](docs/debugging.md): Neovim DAP, semantic TUI, and graph viewer setup.
- [Performance](docs/performance.md): SIMD boundaries, benchmarks, measurements,
  and methodology.
