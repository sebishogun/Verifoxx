# TUI Debugger Dashboard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Turn the semantic debugger into a full-screen responsive DAP-style dashboard with a complete fit-to-pane graph, observable AST/Program controls, and bounded session plus optional PostgreSQL decision history.

**Architecture:** Preserve the immutable `graphview.Graph`, rich renderer for graphs that fit, and all evaluator/debug protocols. Add a terminal-only scaled overview for oversized graphs, expose the real terminal descriptor through Cobra's tracking writer, split the Bubble Tea view into explicit bounded panes, retain session stops in model-owned fixed storage, and keep PostgreSQL history behind an asynchronous optional loader.

**Tech Stack:** Go 1.27, Bubble Tea, Lip Gloss, `github.com/charmbracelet/x/term`, pgx v5, existing SoA/CSR graph layout and Unix semantic-debug protocol.

---

### Task 1: Lock The HTML Label-Containment Fix

**Files:**
- Modify: `internal/graphview/dot.go`
- Modify: `internal/graphview/layout.go`
- Modify: `internal/graphview/svg.go`
- Modify: `internal/graphview/render_test.go`

**Step 1: Preserve the failing regression evidence**

Keep `TestRenderSVGWrapsNodeLabelWithinItsBounds`, which renders a valid 184-byte label and requires multiple complete `<tspan>` lines inside an enlarged node rectangle.

**Step 2: Run the focused RED/GREEN history check**

The test already failed against the fixed-width renderer with:

```text
node label has 0 lines, want wrapped text
```

Run the current implementation:

```bash
timeout 45s go test -count=1 -timeout 30s -run '^TestRenderSVGWrapsNodeLabelWithinItsBounds$' ./internal/graphview
```

Expected: PASS.

**Step 3: Verify bounded rendering**

Run:

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/graphview
timeout 90s go test -count=1 -timeout 60s -run 'TestLayouterWarmPathDoesNotAllocate|TestRenderersAllocateZeroAfterPriming' ./internal/graphview
```

Expected: PASS with zero warmed allocations.

### Task 2: Restore Real Terminal Ownership

**Files:**
- Modify: `internal/adapters/cli/root.go`
- Modify: `internal/adapters/cli/tui.go`
- Modify: `internal/adapters/cli/tui_test.go`

**Step 1: Write failing terminal-adapter tests**

Add tests proving that when Cobra's `trackingWriter` wraps a descriptor-bearing terminal writer, the writer passed to Bubble Tea still implements the terminal file contract and returns the same descriptor. A non-terminal `bytes.Buffer` must remain a normal writer. Also require TUI program options to request the alternate screen.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run 'Test.*(TerminalOutput|AlternateScreen)' ./internal/adapters/cli
```

Expected: FAIL because `trackingWriter` hides the descriptor and `runSemanticTUI` does not use `tea.WithAltScreen()`.

**Step 3: Implement the minimal terminal bridge**

Give `trackingWriter` an internal unwrapping method. In `tui.go`, construct a small writer that continues forwarding `Write` through `trackingWriter` while forwarding `Fd` from the underlying terminal and implementing the no-op `ReadWriteCloser` surface Bubble Tea requires for output detection. Pass it to `tea.WithOutput`, add `tea.WithAltScreen`, and leave non-terminal test writers unchanged.

Do not bypass `trackingWriter`; renderer write failures must still set its error.

**Step 4: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/adapters/cli
```

Expected: PASS. Interactive runs receive initial and resize `tea.WindowSizeMsg` values and restore the prior screen on exit.

### Task 3: Add A Complete Fit-To-Pane Graph Overview

**Files:**
- Modify: `internal/adapters/tui/graph.go`
- Modify: `internal/adapters/tui/graph_test.go`

**Step 1: Replace the clipped-viewport contract with failing overview tests**

Replace `TestGraphRendererCentersCurrentNodeInClippedViewport` with tests that build an oversized, wide layered DAG and require:

- every node contributes a marker to the output;
- roots and the current node remain visible when `Current` is zero and nonzero;
- all output lines fit the requested width and height;
- edge labels appear only in reserved detail rows, never in route cells;
- monochrome and color output strip to identical text;
- warmed overview rendering allocates zero times.

Add one generated fixture with at least 19 nodes in one layer to match the supplied AST's density.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run 'TestGraphRenderer.*(Overview|Oversized|Warm)' ./internal/adapters/tui
```

Expected: FAIL because `Layout.Viewport` clips the 384/488-cell canvas around one node or leaves an origin viewport blank.

**Step 3: Implement reusable overview scratch**

Extend `graphRenderer` with reusable node-coordinate, layer-count, and collision-count slices. After normal layout:

```go
if layout.Width <= options.Width && layout.Height <= graphHeight {
    return renderer.appendRichCanvas(...)
}
return renderer.appendOverview(...)
```

The overview must:

1. reserve two detail rows and one legend row;
2. scale node centers by deterministic layout X and layer into the remaining rectangle;
3. draw all orthogonal edges first without labels;
4. draw one marker per node second, using current/break/watch/path/dim glyphs;
5. merge same-cell nodes into a collision glyph with a bounded count in details;
6. render `#ID label` and typed incoming/outgoing edges only in reserved rows.

Keep the existing compact traversal only for rectangles too small to hold an overview and start that traversal at the graph root, not the current node.

**Step 4: Run GREEN and benchmarks**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/adapters/tui
timeout 120s go test -timeout 90s -run '^$' -bench '^BenchmarkGraphRenderer$' -benchmem -count=6 ./internal/adapters/tui
```

Expected: PASS; warmed overview remains `0 B/op`, `0 allocs/op`.

### Task 4: Build The Responsive DAP Dashboard

**Files:**
- Modify: `internal/adapters/tui/model.go`
- Modify: `internal/adapters/tui/update.go`
- Modify: `internal/adapters/tui/view.go`
- Modify: `internal/adapters/tui/styles.go`
- Modify: `internal/adapters/tui/model_test.go`

**Step 1: Write failing screen and key tests**

Require at 120x40 and 160x50:

- exact terminal width and height;
- Requests, `[AST]`, `[Program]`, Graph, Runtime, Breakpoints, and Watches sections;
- a graph pane larger than the current half-width fallback;
- a dedicated one-line status bar and two-line key bar;
- `p` visibly selects Program and `a` visibly returns to AST, including repeated presses;
- every key invalidates the cached frame and updates the selected tab immediately;
- widths below 100 columns stack bounded panes without border loss.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run 'TestModel.*(Dashboard|GraphTabs|Resize)' ./internal/adapters/tui
```

Expected: FAIL against the current implicit title and three equal-ish panes.

**Step 3: Implement pane rectangles before text**

Compute all pane widths and heights first. Wide mode gives Requests a bounded 18-24 columns, Runtime 28-36 columns, and all remaining width to Graph. Narrow mode stacks panes. Render active graph tabs in the graph title and move `model.status` into its own footer row.

Keep `renderPane` and `fitText` as final containment boundaries. Do not let Runtime request text consume Breakpoints/Watches rows; truncate it within a dedicated request subsection.

**Step 4: Run GREEN and frame-cache tests**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/adapters/tui
```

Expected: PASS, including zero allocations for unchanged cached `View()` calls.

### Task 5: Add Bounded Session Stop History

**Files:**
- Modify: `internal/adapters/tui/model.go`
- Modify: `internal/adapters/tui/update.go`
- Modify: `internal/adapters/tui/view.go`
- Modify: `internal/adapters/tui/model_test.go`

**Step 1: Write failing session-history tests**

Test that:

- `h` opens and closes a visible history dock;
- successful initial snapshot, step, over, restart, replay, breakpoint, continue stop, and completion states append scalar timeline entries;
- errors and stale sequence responses append nothing;
- the oldest entry is compacted away at 65 records;
- `j`/`k` select history rows while focused and `esc` restores request focus;
- no truth/reason slabs are retained in an entry.

**Step 2: Run RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run 'TestModel.*SessionHistory' ./internal/adapters/tui
```

Expected: FAIL because the current model has no stop timeline and `h` immediately calls the persisted loader.

**Step 3: Implement fixed-capacity history**

Add a pointer-free `sessionHistoryEntry` with Unix milliseconds, sequence, action, stop reason, instruction, node, row, truth, and outcome. Preallocate 64 entries in `NewModel`; append after a successful non-stale `applyState`, compact in place when full, and never clone masks.

Add `historyVisible`, `historyTab`, `historyFocus`, and selection indices. `h` toggles the dock, `tab` changes tabs, and only entering the Persisted tab starts a loader command.

**Step 4: Run GREEN**

```bash
timeout 120s go test -count=1 -timeout 90s ./internal/adapters/tui
timeout 300s ./scripts/check-fieldalignment.sh
```

Expected: PASS with reviewed model/entry field order.

### Task 6: Wire Optional PostgreSQL Decision History

**Files:**
- Create: `internal/adapters/postgres/history.go`
- Create: `internal/adapters/postgres/history_test.go`
- Modify: `internal/adapters/tui/model.go`
- Modify: `internal/adapters/tui/update.go`
- Modify: `internal/adapters/tui/view.go`
- Modify: `internal/adapters/cli/root.go`
- Modify: `internal/adapters/cli/tui.go`
- Modify: `internal/adapters/cli/tui_test.go`

**Step 1: Write failing store and optional-loader tests**

Use a narrow query interface in unit tests. Require a parameterized query by exact request key, descending completion/run/finding order, and limit clamped to 64. Rows contain completion time, policy name, semantic version, and decision. Test cancellation, malformed rows, unavailable configuration, and database failure without exposing the URL.

CLI tests require:

- unset `VERIFOXX_DATABASE_URL` starts TUI with Persisted marked “not configured”;
- a configured URL constructs the loader lazily;
- parse/connect/query failure appears in the history pane and does not end the Bubble Tea program;
- loader resources close when TUI exits.

**Step 2: Run RED**

```bash
timeout 120s go test -count=1 -timeout 90s -run 'Test.*History' ./internal/adapters/postgres ./internal/adapters/cli ./internal/adapters/tui
```

Expected: FAIL because PostgreSQL currently supports audit writes but no bounded history read.

**Step 3: Implement the read adapter**

Query:

```sql
SELECT run.completed_at, policy.name, version.semantic_version, finding.decision
FROM verifoxx.evaluation_findings AS finding
JOIN verifoxx.evaluation_runs AS run ON run.id = finding.run_id
JOIN verifoxx.requests AS request ON request.id = finding.request_id
JOIN verifoxx.policy_versions AS version ON version.id = run.policy_version_id
JOIN verifoxx.policies AS policy ON policy.id = version.policy_id
WHERE request.request_key = $1
ORDER BY run.completed_at DESC, run.id DESC, finding.row_index DESC
LIMIT $2
```

Keep returned display strings bounded and validate decisions. The CLI reads the existing environment key through injected dependencies, creates at most one lazy one-connection pool, adapts records to TUI entries, and closes it after `program.Run`.

**Step 4: Run GREEN and integration coverage**

```bash
timeout 150s go test -count=1 -timeout 120s ./internal/adapters/postgres ./internal/adapters/cli ./internal/adapters/tui
timeout 240s go test -count=1 -tags=integration -timeout 210s -run 'Test.*History' ./internal/adapters/postgres
```

Expected: unit tests pass. Integration passes when Docker/PostgreSQL is available; otherwise report the external blocker without weakening the test.

### Task 7: Update Goldens And Verify End To End

**Files:**
- Modify: `testdata/golden/tui/semantic-stop.txt`
- Modify: `testdata/golden/tui/disconnected.txt`
- Modify: `README.md`
- Modify: `docs/development.md`

**Step 1: Update golden contracts**

Regenerate the two fixed terminal frames at their explicit test dimensions. Add concise documentation for full-screen behavior, AST/Program tabs, history tabs, optional `VERIFOXX_DATABASE_URL`, and the browser's richer labeled graph.

**Step 2: Run focused verification**

```bash
timeout 150s go test -count=1 -timeout 120s ./internal/graphview ./internal/adapters/tui ./internal/adapters/cli ./internal/adapters/postgres
timeout 120s go test -timeout 90s -run '^$' -bench 'BenchmarkGraphRenderer|BenchmarkLayout' -benchmem -count=6 ./internal/adapters/tui ./internal/graphview
timeout 300s ./scripts/check-fieldalignment.sh
timeout 30s git diff --check
```

Expected: PASS; warmed graph paths remain allocation-free.

**Step 3: Run the full repository suite**

```bash
timeout 180s go test -count=1 -timeout 120s ./...
timeout 180s go vet ./...
```

Expected: PASS.

**Step 4: Manual pseudo-terminal verification**

Run the worker and TUI in separate terminals, resize the terminal, switch `a`/`p`, open `h`, switch history tabs, step, restart, and quit. Confirm the dashboard occupies/restores the alternate screen and no graph label crosses a route or pane border.

Do not create a commit unless the user explicitly requests one.
