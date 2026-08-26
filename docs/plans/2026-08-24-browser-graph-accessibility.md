# Browser Graph Accessibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Eliminate dense HTML graph label collisions and add topology-aware keyboard navigation with complete relationship inspection.

**Architecture:** The Go renderer emits accessible SVG nodes, edge metadata, and opaque label backplates without changing the graph model. The dependency-free HTML runtime performs adaptive label collision suppression, roving node focus, topology-aware arrow navigation, incident-edge highlighting, and relationship inspection.

**Tech Stack:** Go 1.27, deterministic SVG/HTML, browser DOM APIs, Playwright 1.62, Chromium

---

### Task 1: Emit Accessible Nodes And Edge Label Backplates

**Files:**
- Modify: `internal/graphview/render_test.go`
- Modify: `internal/graphview/svg.go`

**Step 1: Write failing SVG structure tests**

Extend `TestRenderSVGProducesParseableLabeledGraph` to require:

```go
for _, required := range []string{
    `class="edge kind-evidence"`,
    `data-kind="evidence"`,
    `data-label="requires evidence"`,
    `<g class="edge-label" aria-hidden="true"><rect`,
    `class="edge-path"`,
    `role="button"`,
    `tabindex="0"`,
    `aria-selected="false"`,
    `data-layer="0"`,
} {
    if !strings.Contains(text, required) {
        t.Errorf("SVG output lacks %q", required)
    }
}
```

Add an XML-decoding assertion that every edge label contains one positive-size
backplate rectangle and that only the first node has `tabindex="0"`; remaining
nodes must have `tabindex="-1"`.

**Step 2: Run the focused test to verify RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run \
  'TestRenderSVGProducesParseableLabeledGraph' ./internal/graphview
```

Expected: FAIL on missing edge metadata/backplate and node focus attributes.

**Step 3: Emit the minimal SVG metadata and geometry**

In `appendSVGEdge`:

- add `data-kind` and escaped `data-label` to the edge group;
- give the route path `class="edge-path"`;
- wrap the label rectangle and text in
  `<g class="edge-label" aria-hidden="true">`;
- compute the rectangle width from `runewidth.StringWidth(edge.Label)` using the
  fixed 12px monospace assumptions, with horizontal padding;
- render a dark opaque fill and the edge-kind color as a one-pixel border;
- add an edge `<title>` containing direction and full relationship text.

In `appendSVGNode`:

- accept a `tabStop bool` argument from the node loop;
- emit `role="button"`, `tabindex`, `aria-selected="false"`, `data-layer`, and
  an escaped `aria-label` containing node ID and semantic label;
- set only the first node in each SVG to `tabindex="0"`.

Keep all output append-only and allocation-free after renderer priming.

**Step 4: Run focused and allocation tests**

```bash
timeout 90s go test -count=1 -timeout 60s -run \
  'TestRenderSVGProducesParseableLabeledGraph|TestRenderersAllocateZeroAfterPriming' \
  ./internal/graphview
```

Expected: PASS with `0` warmed allocations.

### Task 2: Add Adaptive Labels And Complete Relationship Inspection

**Files:**
- Modify: `internal/graphview/render_test.go`
- Modify: `internal/graphview/html.go`

**Step 1: Write failing HTML behavior markers**

Extend `TestRenderHTMLIncludesBothInteractiveGraphsWithoutExternalAssets` to
require these stable behaviors:

```go
for _, required := range []string{
    `id="relationships"`,
    `class="keyboard-help"`,
    `function boxesOverlap(`,
    `function resolveLabels(`,
    `dataset.colliding`,
    `function selectNode(`,
    `classList.toggle('related'`,
    `replaceChildren()`,
    `edge.dataset.label`,
} {
    if !strings.Contains(text, required) {
        t.Errorf("HTML output lacks %q", required)
    }
}
```

Also require CSS selectors for a suppressed label, related edge, dimmed
unrelated edge, and visible node focus ring.

**Step 2: Run the focused test to verify RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run \
  'TestRenderHTMLIncludesBothInteractiveGraphsWithoutExternalAssets' \
  ./internal/graphview
```

Expected: FAIL on the missing inspector and adaptive-label runtime.

**Step 3: Add inspector markup and styles**

Extend the inspector with:

```html
<h2>relationships</h2>
<div id="relationships">Select a node.</div>
<p class="keyboard-help">Tab to the graph. Use arrow keys to navigate nodes; Enter or Space selects.</p>
```

Add styles that:

- hide `.edge-label[data-colliding="true"]`;
- widen and brighten `.edge.related .edge-path`;
- dim unrelated edges only while the SVG has `.has-selection`;
- preserve opaque label backplates;
- show a distinct `.node:focus-visible rect` ring.

**Step 4: Add deterministic collision suppression**

Implement `boxesOverlap(left, right, padding)` and `resolveLabels(svg)` in the
embedded script. `resolveLabels` must:

1. clear prior collision flags;
2. collect node rectangle bounds as occupied regions;
3. inspect labels in stable DOM order;
4. suppress a label when its backplate intersects any occupied region;
5. otherwise retain it and append its bounds to the occupied list;
6. catch measurement failures and clear collision flags so information remains
   visible.

Run it for both graphs after initialization. SVG `getBBox()` keeps all
comparisons in one coordinate system and independent of viewport size.

**Step 5: Centralize node selection and relationship rendering**

Implement `selectNode(view, node, focus)` to:

- clear the prior selected node and its `aria-selected` state;
- maintain one roving `tabindex="0"` in that graph;
- add `.selected` and `aria-selected="true"` to the new node;
- toggle `.related` on every incident edge and `.has-selection` on the SVG;
- update node details and source span using `textContent`;
- rebuild `#relationships` with DOM-created `ul`/`li` elements, listing
  `out <kind> -> #<id>: <label>` or
  `in <kind> <- #<id>: <label>` for every incident edge;
- focus the node only when requested.

Replace the click-only selection body with calls to `selectNode`.

**Step 6: Run graphview tests**

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/graphview
```

Expected: PASS, including deterministic rendering and zero allocation.

### Task 3: Add Topology-Aware Arrow Navigation And Responsive Layout

**Files:**
- Modify: `internal/graphview/render_test.go`
- Modify: `internal/graphview/html.go`

**Step 1: Write failing keyboard and responsive markers**

Require the generated HTML to contain:

```go
for _, required := range []string{
    `function moveNode(`,
    `case 'ArrowLeft':`,
    `case 'ArrowRight':`,
    `case 'ArrowUp':`,
    `case 'ArrowDown':`,
    `e.key==='Enter'||e.key===' '`,
    `@media(max-width:800px)`,
    `grid-template-rows:minmax(0,1fr)`,
} {
    if !strings.Contains(text, required) {
        t.Errorf("HTML output lacks %q", required)
    }
}
```

**Step 2: Run the focused test to verify RED**

```bash
timeout 90s go test -count=1 -timeout 60s -run \
  'TestRenderHTMLIncludesBothInteractiveGraphsWithoutExternalAssets' \
  ./internal/graphview
```

Expected: FAIL on missing keyboard movement and responsive layout.

**Step 3: Implement directional candidate selection**

Implement `moveNode(svg, node, key)` using existing node/edge metadata:

- left/right: candidates in the same `data-layer` on the requested side;
- up: sources of edges whose `data-to` is the current node;
- down: destinations of edges whose `data-from` is the current node;
- root/leaf fallback: nearest node in the closest layer in that direction;
- candidate ranking: smallest horizontal center distance, then smallest node ID
  for deterministic ties.

Return the current node when no destination exists.

**Step 4: Wire focus and key events**

For each node:

- `focus` calls `selectNode(view, node, false)`;
- arrow keydown prevents browser scrolling, calls `moveNode`, and selects/focuses
  the destination;
- Enter or Space prevents default behavior and selects the current node;
- click selects and focuses the node.

Do not move focus from `applyLive`; live debugger polling may only update visual
state.

**Step 5: Add narrow-screen CSS**

At `max-width:800px`:

- use `100dvh` where supported;
- wrap toolbar controls and move live state to its own row;
- change the workspace to one column with graph above inspector;
- cap the inspector at a useful viewport fraction;
- replace its left border with a top border.

**Step 6: Run graphview and browser adapter tests**

```bash
timeout 120s go test -count=1 -timeout 90s \
  ./internal/graphview ./internal/adapters/cli ./internal/adapters/tui
```

Expected: PASS.

### Task 4: Verify Real Browser Geometry And Keyboard Behavior

**Files:**
- Temporary only: `/tmp/opencode/nornrune-browser-graph-e2e.mjs`
- Temporary output: `/tmp/opencode/nornrune-browser-graph.html`
- Temporary screenshots: `/tmp/opencode/nornrune-browser-{desktop,mobile}.png`

**Step 1: Build a fresh bounded CLI**

```bash
timeout 150s go build -tags=debug -gcflags=all='-N -l' \
  -o /tmp/opencode/nornrune-browser-e2e ./cmd/nornrune
```

**Step 2: Export the real policy graph**

```bash
timeout 60s /tmp/opencode/nornrune-browser-e2e graph \
  --view ast --format html \
  --output /tmp/opencode/nornrune-browser-graph.html --force
```

**Step 3: Write the bounded Playwright check**

Use Playwright from
`/home/sebishogun/.local/share/mise/installs/npm-playwright/1.62.1/node_modules`.
The script must:

- launch `/usr/bin/chromium` headlessly;
- fail on `pageerror` or console error;
- load the exported `file://` document;
- assert every visible `.edge-label rect` is disjoint from every other visible
  label and every `.node rect` in SVG coordinates;
- focus the roving node, exercise ArrowDown and ArrowUp, and assert focus and
  `aria-selected` move then return;
- locate a layer with siblings, exercise ArrowRight and ArrowLeft, and assert
  deterministic focus movement;
- assert the relationship inspector lists all incident edges of the selected
  node and those edges have `.related`;
- capture a desktop screenshot;
- resize to a narrow viewport, assert inspector top is below graph top and no
  horizontal document overflow exists, then capture a mobile screenshot;
- close Chromium in `finally`.

**Step 4: Run Playwright under an outer timeout**

```bash
timeout 90s env \
  NODE_PATH=/home/sebishogun/.local/share/mise/installs/npm-playwright/1.62.1/node_modules \
  node /tmp/opencode/nornrune-browser-graph-e2e.mjs
```

Expected: PASS with no overlaps, browser errors, or leaked Chromium process.

**Step 5: Inspect both screenshots**

Use the image reader to confirm labels, focus, edge highlights, inspector, and
narrow layout are legible. If visual inspection disagrees with geometry checks,
return to Task 2 rather than weakening assertions.

### Task 5: Document Browser Keyboard Controls

**Files:**
- Modify: `README.md:78-124`
- Modify: `docs/debugging.md:164-205`

**Step 1: Update user-facing controls**

Document:

- Tab enters the graph;
- left/right select same-layer siblings;
- up/down follow incoming/outgoing relationships;
- Enter/Space select;
- the inspector always lists complete relationships when dense labels are
  suppressed;
- pointer pan, wheel zoom, Fit, AST, and Program controls remain available.

**Step 2: Run documentation and diff checks**

```bash
timeout 90s go test -count=1 -timeout 60s ./internal/doccheck
timeout 30s git diff --check
```

Expected: PASS.

### Task 6: Run Final Regression Gates

**Files:**
- Verify only.

**Step 1: Run all tests**

```bash
timeout 240s go test -count=1 -timeout 180s ./...
```

**Step 2: Run vet and field-layout checks**

```bash
timeout 240s go vet ./...
timeout 300s ./scripts/check-fieldalignment.sh
```

**Step 3: Confirm renderer allocations and diff hygiene**

```bash
timeout 90s go test -count=1 -timeout 60s -run \
  '^TestRenderersAllocateZeroAfterPriming$' ./internal/graphview
timeout 30s git diff --check
```

Expected: every command passes. Do not create a commit unless explicitly
requested.
