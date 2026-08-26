# Debugger Graph Visualization Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Draw labeled semantic node-edge graphs in the Bubble Tea debugger and expose the same AST/Program graphs as DOT, SVG, static HTML, and a synchronized loopback browser view.

**Architecture:** A new `internal/graphview` package owns an immutable edge-aware CSR graph, deterministic layered layout, and portable exporters. Existing CLI graph construction expands from expression-only child lists into complete semantic topology, while the Bubble Tea adapter renders a clipped styled canvas and publishes fixed-size live state to an optional local browser server.

**Tech Stack:** Go 1.27, Bubble Tea, Lip Gloss, `net/http`, SVG, DOT, dependency-free browser JavaScript.

---

### Task 1: Add The Edge-Aware Graph Model

**Files:**
- Create: `internal/graphview/model.go`
- Create: `internal/graphview/model_test.go`
- Create: `internal/graphview/layout.go`
- Create: `internal/graphview/layout_test.go`
- Create: `internal/graphview/layout_bench_test.go`

**Step 1: Write failing model tests**

Cover exact CSR coverage, node/edge metadata lengths, one-based destinations,
duplicate roots, invalid UTF-8/control text, source ranges, configured limits,
and a valid shared DAG with typed edge labels.

**Step 2: Run the RED gate**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/graphview
```

Expected: fail because `internal/graphview` does not exist.

**Step 3: Implement bounded graph types and validation**

Define `NodeKind`, `EdgeKind`, `Graph`, `Limits`, and `Validate`. Store node
labels/details/kinds/source ranges and edge destinations/kinds/labels in exact
parallel slices. Keep graph IDs one-based and reject duplicate roots.

**Step 4: Write failing deterministic-layout tests**

Lock root-to-leaf layers, stable IDs as tie-breakers, shared-node convergence,
edge-label routes, cycle rejection, current-node viewport centering, and output
independence from scratch capacity.

**Step 5: Implement layout into reusable scratch**

Use longest-root-distance layering, bounded barycenter ordering, fixed node
cells, orthogonal labeled routes, and viewport clipping. Keep dynamic layout
scratch in a reusable `Layouter`; do not store maps per node.

**Step 6: Run GREEN and benchmark allocation shape**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/graphview
timeout 120s go test -run='^$' -bench='^BenchmarkLayout$' -benchmem -benchtime=200ms -count=6 -timeout=90s ./internal/graphview
```

Expected: tests pass; the primed layout kernel reports `0 B/op` and
`0 allocs/op`, with no per-node or per-edge heap object.

### Task 2: Build Full AST And Program Semantic Graphs

**Files:**
- Create: `internal/adapters/cli/graph_data.go`
- Create: `internal/adapters/cli/graph_data_test.go`
- Modify: `internal/adapters/cli/tui.go`
- Modify: `internal/adapters/tui/model.go`

**Step 1: Write failing graph-construction tests**

Require policy, requirement, clause, expression/instruction, evidence,
remediation, and outcome nodes. Assert exact edge labels and kinds for
`contains`, `applies`, `clause`, `assert`, `requires evidence`, all seven
resolution branches, `remediation`, `arg N`, and `operand N`. Assert field and
literal labels plus source spans contain no request/evidence payload values.

**Step 2: Run the RED gate**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestBuild.*Graph' ./internal/adapters/cli
```

Expected: fail because the current graph has anonymous expression children only.

**Step 3: Implement graph construction**

Move graph construction out of `tui.go`. Preserve AST NodeIDs and Program
InstructionIDs as the first graph rows, then append presentation-only semantic
nodes. Build edge columns with exact capacity hints and resolve safe field,
literal, catalog, outcome, and remediation labels from immutable documents.

**Step 4: Alias the TUI graph to `graphview.Graph`**

Remove duplicate validation from the TUI model and call `graphview.Validate`
from `NewModel`. Keep existing request validation unchanged.

**Step 5: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/graphview ./internal/adapters/cli ./internal/adapters/tui
```

Expected: all graph topology and existing debugger tests pass.

### Task 3: Draw The Colored Bubble Tea Graph

**Files:**
- Replace: `internal/adapters/tui/tree.go`
- Create: `internal/adapters/tui/graph.go`
- Modify: `internal/adapters/tui/styles.go`
- Modify: `internal/adapters/tui/view.go`
- Modify: `internal/adapters/tui/update.go`
- Modify: `internal/adapters/tui/model.go`
- Modify: `internal/adapters/tui/model_test.go`
- Modify: `testdata/golden/tui/semantic-stop.txt`
- Modify: `testdata/golden/tui/disconnected.txt`

**Step 1: Write failing terminal-renderer tests**

Require Unicode boxes, arrowed orthogonal edges, visible edge labels, one box
for shared nodes, current-node centering, active-path emphasis, kind colors,
truth colors, breakpoint/watch markers, narrow fallback, and ANSI-free output
when the renderer profile is monochrome.

**Step 2: Run the RED gate**

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test.*(Graph|Resize|Golden)' ./internal/adapters/tui
```

Expected: fail because the current renderer emits indented `- #ID` lines.

**Step 3: Implement the rune-cell canvas**

Draw edges first and boxes second. Store semantic style IDs per cell and render
contiguous runs through Lip Gloss. Add a compact legend and preserve exact pane
width/height after ANSI styling.

**Step 4: Make mode keys observable**

`a` and `p` set mode, recenter the active ID, and set `AST graph active` or
`Program graph active` even when repeated. Keep `j`/`k`, stepping, breakpoint,
watch, replay, and cancellation behavior unchanged.

**Step 5: Update goldens and run GREEN**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/tui ./internal/adapters/cli
```

Expected: all TUI and CLI tests pass with labeled graph goldens.

### Task 4: Add DOT, SVG, And Interactive HTML Renderers

**Files:**
- Create: `internal/graphview/dot.go`
- Create: `internal/graphview/svg.go`
- Create: `internal/graphview/html.go`
- Create: `internal/graphview/render_test.go`

**Step 1: Write failing exporter tests**

Require stable node and edge ordering, escaped labels, semantic colors/styles,
arrowheads, source-span/detail metadata, AST/Program tabs in HTML, pan/zoom/fit
controls, and byte-identical repeated renders.

**Step 2: Run the RED gate**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestRender' ./internal/graphview
```

Expected: fail because exporters do not exist.

**Step 3: Implement append-based renderers**

Append DOT, XML, and HTML directly into caller-provided byte slices. Generate
SVG geometry from the shared layout. Embed dependency-free JavaScript and both
graph payloads in HTML without external assets or protected source bytes. Warm
repeated renders with sufficient destination capacity must allocate zero.

**Step 4: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/graphview
```

Expected: exporter and model tests pass.

### Task 5: Add The Static `nornrune graph` Command

**Files:**
- Create: `internal/adapters/cli/graph.go`
- Create: `internal/adapters/cli/graph_test.go`
- Modify: `internal/adapters/cli/root.go`
- Modify: `README.md`
- Modify: `docs/debugging.md`

**Step 1: Write failing command tests**

Cover required output path, valid views/formats, source flags, deterministic
files, existing-file rejection, `--force`, invalid data, write failure, and no
partial destination after render failure.

**Step 2: Run the RED gate**

```bash
timeout 120s go test -count=1 -timeout 60s -run='TestGraphCommand' ./internal/adapters/cli
```

Expected: fail because `graph` is not registered.

**Step 3: Implement atomic export**

Compile sources through the existing pipeline, build both graphs once, render
the requested view/format, write a mode-`0600` temporary sibling, sync, close,
and rename. Reject overwrite unless `--force`.

**Step 4: Run GREEN and CLI smoke tests**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/cli ./cmd/nornrune
timeout 120s go run ./cmd/nornrune graph --view ast --format svg --output /tmp/nornrune-ast.svg --force
timeout 120s go run ./cmd/nornrune graph --view program --format html --output /tmp/nornrune-program.html --force
```

Expected: tests pass and both output files are nonempty.

### Task 6: Add The Live Loopback Browser Viewer

**Files:**
- Create: `internal/adapters/tui/browser.go`
- Create: `internal/adapters/tui/browser_test.go`
- Modify: `internal/adapters/tui/model.go`
- Modify: `internal/adapters/tui/update.go`
- Modify: `internal/adapters/cli/tui.go`
- Modify: `internal/adapters/cli/tui_test.go`
- Modify: `docs/debugging.md`

**Step 1: Write failing server and publication tests**

Require IPv4 loopback binding, secure headers, static HTML, bounded state JSON,
mode/current/request/truth/breakpoint/watch updates, cancellation shutdown, and
rejection of non-loopback configuration. Prove publishing state does not
recompute layout.

**Step 2: Run the RED gate**

```bash
timeout 120s go test -count=1 -timeout 60s -run='Test.*Browser' ./internal/adapters/tui ./internal/adapters/cli
```

Expected: fail because live browser support does not exist.

**Step 3: Implement packed live state and server**

Pre-render HTML once, store changing scalar state atomically, and serve only
`/` and `/state`. Add cache and content-security headers. Make the model publish
after snapshots, steps, mode changes, row selection, breakpoint changes, and
watch changes. State publication must allocate zero.

**Step 4: Add `tui --browser`**

Start the server before Bubble Tea, launch `xdg-open` or `open` directly, and
show the URL in model status. An opener failure leaves the server and terminal
debugger running and reports the URL.

**Step 5: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 60s ./internal/adapters/tui ./internal/adapters/cli
```

Expected: all browser and debugger tests pass.

### Task 7: Verify Semantics, Layout, And Release Gates

**Files:**
- Modify: `docs/performance.md`
- Modify: `docs/debugging.md`
- Modify: `README.md`

**Step 1: Run focused layout and linked evaluator benchmarks**

```bash
timeout 180s go test -run='^$' -bench='^BenchmarkLayout$' -benchmem -benchtime=300ms -count=6 -timeout=120s ./internal/graphview
timeout 180s go test -run='^$' -bench='^BenchmarkEvaluate$' -benchmem -benchtime=300ms -count=6 -timeout=120s ./internal/eval
```

Expected: graph layout remains bounded; evaluator stays `0 B/op`, `0 allocs/op`.

**Step 2: Run static analysis and architecture gates**

```bash
timeout 180s go vet ./...
timeout 180s go run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@v0.47.1-0.20260707181000-a299dadba899 -test=false ./internal/graphview ./internal/adapters/tui ./internal/adapters/cli
```

Expected: no output.

**Step 3: Run the release matrix**

```bash
timeout 240s go test -count=1 -timeout 180s ./...
timeout 300s go test -count=1 -timeout 240s -race ./internal/... ./cmd/...
timeout 300s env GOARCH=386 go test -count=1 -timeout 240s ./...
timeout 300s go test -count=1 -timeout 240s -tags=purego ./...
timeout 360s go test -count=1 -timeout 300s -tags=integration ./...
timeout 300s go run ./cmd/devx policy:check
timeout 300s go run ./cmd/devx results:check
timeout 300s env PATH="/tmp/opencode/nornrune-tools:$PATH" go run ./cmd/devx proto:check
timeout 300s go run ./cmd/devx build
git diff --check
```

Expected: every command exits zero.

**Step 4: Commit and push**

Stage only debugger graph files; keep the paused Task 52 design out of this
commit.

```bash
git commit -m "feat: draw semantic debugger graphs"
git push origin main
```
