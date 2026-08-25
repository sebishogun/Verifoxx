# Browser Graph Accessibility Design

## Goal

Make exported and live HTML graphs readable in dense regions and fully
navigable without a pointer while preserving every policy relationship.

## Visual Model

SVG rendering adds a dark, color-keyed backplate behind each edge label so an
edge path cannot run through its text. In HTML, a deterministic browser-side
pass compares label, node, and previously accepted label bounds. It leaves
non-colliding labels visible and suppresses only labels that would overlap.
Sparse graphs therefore retain their at-a-glance relationship text without
forcing dense graphs into an unbounded canvas.

Selecting a node highlights its incident edges and populates a relationship
section in the inspector. The section lists every typed incoming and outgoing
edge, endpoint ID, and full label, including labels suppressed on the canvas.
Relationship metadata comes from bounded `data-*` attributes already emitted
with each edge; no new graph representation or runtime fetch is introduced.

## Keyboard And Accessibility

Each visible graph exposes one roving tab stop. SVG node groups carry a button
role, label, selection state, layer, and focusability metadata. ArrowLeft and
ArrowRight move to the nearest node in the same layer. ArrowUp follows the
nearest incoming relationship and ArrowDown follows the nearest outgoing
relationship; roots and leaves fall back to the nearest node in the adjacent
geometric layer. Focus selects the destination and updates the inspector, while
Enter and Space activate the focused node explicitly.

Focus receives a visible ring and selection is reflected through
`aria-selected`. Switching AST and Program views retains independent focus and
selection state. Live polling updates semantic highlights without moving focus.
Toolbar controls remain native buttons. On narrow screens, the inspector moves
below the graph and the toolbar wraps rather than shrinking the graph into an
unusable column.

## Rendering And Performance

The Go renderer continues to append into caller-owned buffers. Label backplate
geometry uses the existing fixed-width font assumptions and rune display width;
it does not allocate per edge. Collision resolution, focus navigation, and
relationship-list construction run only in the browser and stay outside the
evaluator and graph rendering kernels. Standalone SVG receives the label
backplates and node accessibility metadata; adaptive suppression and keyboard
behavior are HTML enhancements.

## Failure Behavior

Missing relationship targets are ignored rather than throwing during keyboard
navigation. A graph with no nodes remains non-focusable. Browser collision
measurement failures leave labels visible, preserving information. Existing
live-state polling remains non-fatal and never replaces the terminal debugger
as the controller.

## Verification

Go tests lock deterministic SVG/HTML output markers, focus semantics,
relationship metadata, label backplates, adaptive collision logic, arrow-key
handlers, and warmed zero-allocation rendering. A bounded Playwright run opens a
real exported policy graph at desktop and narrow widths, verifies visible label
and node bounds do not overlap, drives all four arrows, checks inspector and
selection changes, and rejects browser console errors. Graph, browser, full Go,
vet, field-alignment, and diff checks remain required. No commit is created
unless requested.
