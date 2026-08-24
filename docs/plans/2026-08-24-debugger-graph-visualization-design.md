# Debugger Graph Visualization Design

**Status:** Approved

**Date:** 2026-08-24

## Goal

Replace the semantic debugger's indented traversal with an actual node-edge
graph and expose the same graph as deterministic DOT, SVG, and interactive HTML.
The graph must remain useful while stepping: the current node, its dependency
path, truth state, breakpoint/watch state, and source span are visually distinct.

## User Surfaces

### Terminal debugger

The graph pane draws boxed nodes on deterministic layers with Unicode edges.
Roots are above their dependencies. The current node is centered when possible;
its ancestors and direct dependencies are retained before unrelated nodes when
the pane cannot fit the whole graph. Shared DAG references converge on one box
rather than expanding into duplicate text.

Color carries bounded meaning:

| Visual | Meaning |
|---|---|
| cyan | comparison or scalar instruction |
| blue | `all`/`any` composition |
| magenta | negation or conflict |
| amber | evidence requirement |
| green | current row is true |
| red | current row is false or node has a breakpoint |
| yellow | current row is unknown or current node |
| dim | outside the active dependency path |

Edges carry semantic labels rather than acting as anonymous child links:

| Edge | Color/style |
|---|---|
| `applies` | violet |
| `assert` | cyan |
| `requires evidence` | amber dashed |
| `on satisfied` | green |
| `on false` | red |
| `on missing/stale/unclear/unverifiable` | yellow |
| `on conflict` | magenta |
| `remediation` | blue dashed |
| `arg N` / `operand N` | muted solid, bright on the active path |

Edge labels are placed on routed horizontal segments and remain present in
monochrome output. When space is constrained, labels are shortened
deterministically but never replaced with an unlabeled edge.

Every state also has a glyph or label, so monochrome terminals remain usable.
`a` selects and recenters the AST graph; `p` selects and recenters the Program
graph. Repeating either key displays a status message instead of appearing to do
nothing. `x` still controls shared-reference expansion for the fallback compact
view.

### Static export

A new command supports:

```text
verifoxx graph --view ast|program --format dot|svg|html --output PATH
```

It accepts the existing policy/request/evidence source flags. DOT is suitable
for Graphviz, SVG is directly viewable, and HTML embeds an interactive SVG with
pan, zoom, fit, AST/Program switching, node selection, labels, details, and
source spans. Output is deterministic for identical inputs.

### Live browser

`verifoxx tui --browser` starts an ephemeral IPv4-loopback HTTP server and opens
the interactive HTML viewer. The static graph payload is served once. A bounded
state endpoint reports only graph mode, current node/instruction, selected row,
truth state, and breakpoint/watch IDs. The page polls that endpoint and updates
highlighting without rebuilding the graph.

The server never binds a non-loopback address, never serves policy/request/
evidence source bytes, and stops with the TUI context. Browser launch uses a
direct platform command without a shell. Failure to open a browser leaves the
printed loopback URL usable and does not stop semantic debugging.

## Shared Graph Model

`internal/graphview` owns a bounded immutable CSR graph:

```text
labels, details, kinds, source starts/ends
edge starts/counts -> destination IDs, edge kinds, edge labels
roots
```

AST and Program adapters populate it once. Labels include meaningful field and
literal data where available rather than only opcode names. Details and source
spans are presentation metadata; protected request and evidence values are not
included.

The AST graph is a semantic graph, not just an expression tree. It includes
policy, requirement, clause, expression, evidence, remediation, and outcome
nodes. Typed edges connect requirements to applicability roots and clauses,
clauses to assertions/evidence/remediations, and every resolution branch to its
outcome. Group expressions label children as `arg N`; negation labels its child
as `operand`.

The Program graph includes instruction nodes plus semantic requirement, clause,
and outcome nodes. Instruction dependencies are labeled `operand N`; semantic
edges retain the same applicability, assertion, evidence, remediation, and
resolution labels as the AST. Virtual presentation nodes use a separate kind
so debugger InstructionIDs and AST NodeIDs still highlight exact runtime nodes.

The package validates one-based IDs, exact CSR coverage, UTF-8 display text,
source ranges, and configured node/edge/text limits. It computes deterministic
layers and positions into caller-supplied scratch. Layout is cycle-safe even
though validated production graphs are acyclic.

## Layout

The layout algorithm is bounded and deterministic:

1. Traverse roots in stable order and assign each reachable node its longest
   root distance.
2. Order each layer by stable barycenter passes, using node ID as the tie-break.
3. Assign fixed-size cells, then compact adjacent empty columns.
4. Route orthogonal edges between layers, preserving shared destinations and a
   stable label segment for every typed edge.
5. For a terminal viewport, score current node, ancestors, children, roots, then
   stable remaining nodes and retain only complete boxes that fit.

The implementation is linear in nodes plus edges apart from bounded layer
ordering. It allocates during cold graph construction/export, never in evaluator
kernels. Live stepping updates packed state atomically and does not recompute
layout.

## Rendering

- The terminal renderer writes a styled rune-cell canvas and emits contiguous
  style runs so ANSI sequences do not corrupt width calculations.
- The SVG renderer writes escaped XML directly into caller-owned bytes and
  includes arrowheads plus typed edge labels.
- The DOT renderer quotes every node and edge label and uses stable ordering.
- The HTML renderer embeds the two SVG graphs and a small dependency-free
  script for pan, zoom, fit, selection, mode switching, and optional live state.

No renderer invokes Graphviz or downloads browser assets.

## Error Handling

Invalid graphs fail before a TUI, export file, or HTTP listener is created.
Export writes to a temporary file in the destination directory, syncs and
renames it, so a failed render cannot leave a partial requested output. Existing
files require `--force`. Browser server and opener errors are bounded operational
errors; semantic target errors keep their existing behavior.

## Testing

- Graph validation covers malformed CSR, missing or misaligned edge metadata,
  invalid IDs, cycles, duplicate roots, invalid UTF-8, limits, and source
  ranges.
- Layout tests lock node layers, stable ordering, shared DAG convergence,
  current-node prioritization, clipping, and deterministic output.
- Terminal golden tests cover color-disabled and ANSI-color renderings, labeled
  semantic edges, active true/false/unknown states, narrow panes, shared nodes,
  and mode recentering.
- Export tests parse SVG/XML, inspect DOT escaping, execute HTML behavior with
  DOM-independent payload checks, and prove byte-for-byte determinism.
- Browser tests bind loopback, reject non-loopback configuration internally,
  verify headers and bounded state JSON, and close on cancellation.
- CLI tests cover formats, `--force`, source flags, browser launch failure, and
  absence of partial files.

## Performance

Graph construction is a cold adapter path. After exact capacities are
established, layout, active-path calculation, terminal canvas rendering into a
caller-owned destination, and live-state publication must each report `0 B/op`
and `0 allocs/op`. DOT, SVG, and HTML exporters accept caller-owned destinations
and must likewise allocate zero on warmed repeated renders with sufficient
capacity; file creation and browser/network boundaries remain cold adapter work.

Benchmarks report nodes, edges, bytes, and allocations for a tree, shared DAG,
and maximum bounded viewport. The Bubble Tea model caches its completed frame so
repeated `View` calls without a state change allocate zero. Stepping publishes
fixed-size atomic state and reuses double-buffered frame storage. The evaluator
binary path and its zero-allocation kernels remain unchanged; linked evaluator
benchmarks must remain at parity before this feature is accepted.
