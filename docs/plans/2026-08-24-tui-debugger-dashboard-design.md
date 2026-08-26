# TUI Debugger Dashboard Design

**Status:** Approved

**Date:** 2026-08-24

## Goal

Make the Bubble Tea semantic debugger behave like a full-screen DAP/GDB
dashboard. The complete graph topology must remain visible during stepping,
pane text must never overwrite graph routes or borders, AST/Program switching
must be obvious, and history must expose both current-session stops and optional
persisted decisions.

This design replaces the terminal-viewport behavior in
`2026-08-24-debugger-graph-visualization-design.md`. The shared graph model and
DOT, SVG, HTML, and live-browser surfaces remain unchanged.

## Root Causes

- Cobra passes Bubble Tea a tracking writer that does not expose the underlying
  terminal descriptor. Bubble Tea therefore receives no window-size events and
  renders the 100x24 fallback inside a larger terminal.
- The terminal graph always uses a 20-column node box. The supplied AST is 488
  cells wide and the Program graph is 384 cells wide, so centering the current
  node can only display a fragment. With no current node, the centered root can
  be entirely outside the origin viewport.
- Edge labels are painted directly on shared routes without reserved rows, so
  labels overwrite one another in dense layers.
- `h` delegates to a `HistoryLoader`, but the CLI always passes `nil`; the only
  result is a status message that is easy to miss in the clipped Runtime pane.

## Screen Layout

The interactive command uses Bubble Tea's alternate screen and the real
terminal dimensions.

For terminals at least 100 columns wide, the main area is a three-column grid:

```text
+ Requests -------+ [AST] [Program] Graph ----------------+ Runtime --------+
| > R2 Reject     | complete fit-to-pane topology          | stop / truth    |
|   R3 Revise     |                                        | source / masks  |
|                 | current: #4 action.type = ...          | breaks / watches|
+-----------------+----------------------------------------+-----------------+
+ History: Session | Persisted ---------------------------------------------+
| bounded stop timeline or optional PostgreSQL decision rows                |
+---------------------------------------------------------------------------+
| keys and current status                                                   |
+---------------------------------------------------------------------------+
```

The history dock is hidden initially and toggled with `h`. When visible, it
takes at most one third of the usable height. Narrow terminals stack Requests,
Graph, Runtime, and History vertically. Every pane receives an exact bounded
rectangle before rendering; `fitText` remains the final border guard.

## Graph Overview

Small graphs that fit retain the existing labeled-box canvas. Oversized graphs
use a terminal-specific overview:

1. Reuse deterministic graph layers and barycenter order.
2. Scale every node center into the available columns and every layer into the
   available graph rows.
3. Draw all edges first and one state glyph per node second. Intersections use a
   crossing glyph; no edge label is painted over a route.
4. Reserve rows below the topology for the current node label and its incoming
   and outgoing typed relationships. Long text is clipped or wrapped only in
   these rows, never in the graph canvas.
5. Use IDs, state glyphs, color, and a monochrome legend so the overview remains
   meaningful without color.

If a layer has more nodes than display columns, nodes are grouped into a visible
collision marker and the detail row reports the count. The renderer never pans
away unrelated nodes. The browser remains the rich labeled and zoomable view.

## Interaction

- `a` activates AST and visibly selects the AST tab, even when already active.
- `p` activates Program and visibly selects the Program tab.
- `h` toggles the history dock. `tab` switches Session/Persisted history while
  the dock is open.
- `j`/`k` continue to select requests when history is closed. When history is
  open they select history rows; `esc` returns focus to Requests.
- Existing step, node, over, continue, pause, restart, replay, breakpoint,
  watch, shared-reference, and quit keys remain available.
- Status and errors have a dedicated footer row instead of being discoverable
  only inside Runtime text.

## History

### Session

The model retains a 64-entry bounded ring of successful debugger stops. Each
entry stores scalar stop metadata only: action, stop reason, instruction, node,
selected request row, truth, outcome, and timestamp. It does not clone mask or
reason slabs. Restart, replay, step, continue completion, breakpoint, and final
completion are visible.

### Persisted

When `NORNRUNE_DATABASE_URL` is unset, the Persisted tab states that persistence
is not configured while Session history remains usable. When set, a cold-path
PostgreSQL loader queries at most 64 newest audit findings for the selected
request key, ordered by completion time and stable IDs. Returned rows contain
time, policy name/version, and decision only.

The query runs asynchronously under the existing command timeout. Connection or
query failure appears inside the Persisted tab and never prevents startup,
stepping, mode switching, or Session history. Credentials are never rendered.

## Performance And Bounds

Graph topology, positions, and typed scratch remain reusable SoA/CSR storage.
Overview rendering, state marking, and warmed destination rendering remain zero
allocation. Session history is one fixed-capacity slice compacted in place. The
PostgreSQL connection and returned display rows are cold adapter work and are
bounded to 64 records.

## Testing

- A terminal-output adapter test proves the underlying TTY descriptor remains
  visible through Cobra's error-tracking writer.
- Model tests prove real window dimensions drive a full-frame alternate-screen
  layout and all pane lines remain within their rectangles.
- Oversized AST/Program fixtures prove every node is represented in overview
  mode, no label is drawn on a route, and current details remain visible.
- Key tests prove `a`, `p`, `h`, `tab`, and `esc` produce observable state and
  invalidate the frame cache.
- Session-history tests cover bounded eviction and every successful stop type.
- Persisted-history tests use a query stub for ordering, limits, cancellation,
  unavailable configuration, and non-fatal failures; PostgreSQL integration
  verifies the production query.
- Existing graph, browser, CLI, width/height, UTF-8, ANSI parity, allocation,
  and evaluator tests remain green.
