package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/sebishogun/nornrune/internal/debug"
	"github.com/sebishogun/nornrune/internal/graphview"
)

func TestGraphRendererDrawsLabeledSharedDAG(t *testing.T) {
	graph := rendererSharedGraph()
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 72, Height: 18, Current: 4, Truth: debug.TruthTrue,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	view := string(output)
	if !containsAll(view, "┌", "┐", "└", "┘", "│", "─", "▼", "applies", "assert", "arg 1", "arg 2", "▶✓ #4 shared") {
		t.Fatalf("semantic graph render is incomplete:\n%s", view)
	}
	if strings.Count(view, "#4 shared") != 1 {
		t.Fatalf("shared node rendered %d times, want one box:\n%s", strings.Count(view, "#4 shared"), view)
	}
}

func TestGraphRendererShowsActivePathProgramWatchesAndLegend(t *testing.T) {
	graph := rendererSharedGraph()
	graph.Labels = append(graph.Labels, "detached")
	graph.Details = append(graph.Details, "detached node")
	graph.Kinds = append(graph.Kinds, graphview.NodeEvidence)
	graph.SourceStarts = append(graph.SourceStarts, 0)
	graph.SourceEnds = append(graph.SourceEnds, 0)
	graph.EdgeStarts = append(graph.EdgeStarts, uint32(len(graph.Edges)))
	graph.EdgeCounts = append(graph.EdgeCounts, 0)
	graph.Roots = append(graph.Roots, 5)

	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 96, Height: 18, Current: 4, Truth: debug.TruthFalse,
		Breakpoints: []breakpointBinding{{node: 2, id: 7}},
		Watches:     []watchBinding{{instruction: 3, id: 9}},
		Program:     true,
	})
	if err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	view := string(output)
	if !containsAll(view, "▶✗ #4 shared", "W #3 right", "· #5 detached", "▶ current", "! both", "• path", "· dim", "B break", "W watch") || strings.Contains(view, "B #2 left") {
		t.Fatalf("graph state markers are incomplete:\n%s", view)
	}
}

func TestGraphRendererUsesViewSpecificBindingNamespace(t *testing.T) {
	graph := rendererSharedGraph()
	options := graphRenderOptions{
		Width: 96, Height: 18, Current: 4,
		Breakpoints: []breakpointBinding{{node: 2, id: 7}},
		Watches:     []watchBinding{{instruction: 3, id: 9}},
	}
	var renderer graphRenderer
	ast, err := renderer.Append(nil, &graph, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ast), "B #2 left") || strings.Contains(string(ast), "W #3 right") {
		t.Fatalf("AST graph mixed binding namespaces:\n%s", ast)
	}

	options.Program = true
	program, err := renderer.Append(ast[:0], &graph, options)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(program), "W #3 right") || strings.Contains(string(program), "B #2 left") {
		t.Fatalf("Program graph mixed binding namespaces:\n%s", program)
	}
}

func TestGraphRendererColorAndMonochromeProfilesCarrySameLabels(t *testing.T) {
	graph := rendererSharedGraph()
	graph.Kinds[1] = graphview.NodeCompare
	graph.Kinds[2] = graphview.NodeEvidence
	var renderer graphRenderer

	plain, err := renderer.Append(nil, &graph, graphRenderOptions{Width: 72, Height: 18, Current: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "\x1b[") {
		t.Fatalf("monochrome render contains ANSI: %q", plain)
	}
	colored, err := renderer.Append(nil, &graph, graphRenderOptions{Width: 72, Height: 18, Current: 1, Color: true})
	if err != nil {
		t.Fatal(err)
	}
	colorView := string(colored)
	if !containsAll(colorView, "\x1b[", "\x1b[36m", "\x1b[33m", "applies", "assert", "arg 1", "arg 2") {
		t.Fatalf("colored render lacks semantic styles:\n%q", colorView)
	}
	if ansi.Strip(colorView) != string(plain) {
		t.Fatalf("color changed graph text:\nplain=%q\ncolor=%q", plain, ansi.Strip(colorView))
	}
}

func TestGraphRendererMonochromeLegendFitsTypicalPane(t *testing.T) {
	graph := rendererSharedGraph()
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{Width: 57, Height: 18, Current: 4})
	if err != nil {
		t.Fatal(err)
	}
	view := string(output)
	if !containsAll(view, "▶ current", "✓ true", "✗ false", "! both", "? unknown", "• path", "· dim", "B break", "W watch") {
		t.Fatalf("monochrome legend was clipped:\n%s", view)
	}
}

func TestGraphRendererUsesBoundedLabeledFallbackForNarrowView(t *testing.T) {
	graph := rendererSharedGraph()
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 18, Height: 7, Current: 4, Truth: debug.TruthNeither,
	})
	if err != nil {
		t.Fatal(err)
	}
	view := string(output)
	if !containsAll(view, "#4 shared", "arg 1") {
		t.Fatalf("narrow fallback lost current node or edge label:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > 18 {
			t.Fatalf("narrow line width = %d, want <=18: %q", ansi.StringWidth(line), line)
		}
		if !utf8.ValidString(line) {
			t.Fatalf("narrow line is invalid UTF-8: %q", line)
		}
	}
}

func TestGraphRendererCompactSharedReferenceToggle(t *testing.T) {
	graph := rendererSharedGraph()
	model := Model{}
	collapsed := model.renderGraph(graph, 1, 18, 16)
	model.expandShared = true
	expanded := model.renderGraph(graph, 1, 18, 16)
	if !strings.Contains(collapsed, "#4 [ref]") {
		t.Fatalf("collapsed compact graph lacks shared reference:\n%s", collapsed)
	}
	if strings.Contains(expanded, "[ref]") || strings.Count(expanded, "#4 shared") != 2 {
		t.Fatalf("expanded compact graph did not repeat the shared node:\n%s", expanded)
	}
}

func TestGraphRendererFitsCompleteOverviewForOversizedGraph(t *testing.T) {
	graph := rendererWideLayerGraph(20)
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 48, Height: 14, Current: 10, Truth: debug.TruthBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(string(output))
	lines := strings.Split(view, "\n")
	if len(lines) != 14 {
		t.Fatalf("overview height = %d, want 14:\n%s", len(lines), view)
	}
	topology := strings.Join(lines[:10], "\n")
	if markers := graphOverviewMarkerCount(topology); markers != len(graph.Kinds) {
		t.Fatalf("overview markers = %d, want %d nodes:\n%s", markers, len(graph.Kinds), view)
	}
	if strings.Contains(topology, "branch") {
		t.Fatalf("edge label was drawn over overview routes:\n%s", view)
	}
	if !strings.Contains(view, "▶! #10 node-10") {
		t.Fatalf("overview lacks current-node detail:\n%s", view)
	}
	for row, line := range lines {
		if width := ansi.StringWidth(line); width > 48 {
			t.Fatalf("overview line %d width = %d, want <=48: %q", row+1, width, line)
		}
	}

	output, err = renderer.Append(output[:0], &graph, graphRenderOptions{Width: 48, Height: 14})
	if err != nil {
		t.Fatal(err)
	}
	view = ansi.Strip(string(output))
	lines = strings.Split(view, "\n")
	if markers := graphOverviewMarkerCount(strings.Join(lines[:10], "\n")); markers != len(graph.Kinds) ||
		!strings.Contains(view, "#1 root") {
		t.Fatalf("pre-step overview does not show the complete graph and root detail:\n%s", view)
	}

	plain := view
	output, err = renderer.Append(output[:0], &graph, graphRenderOptions{
		Width: 48, Height: 14, Color: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stripped := ansi.Strip(string(output)); stripped != plain {
		t.Fatalf("overview color changed topology text:\nplain=%q\ncolor=%q", plain, stripped)
	}
}

func TestGraphRendererOverviewGroupsCrowdedCellsAndShowsTypedRelationships(t *testing.T) {
	graph := rendererWideLayerGraph(82)
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 48, Height: 14, Current: 10, Truth: debug.TruthTrue,
	})
	if err != nil {
		t.Fatal(err)
	}
	view := ansi.Strip(string(output))
	if !containsAll(view, "◆", "cluster ", "82 nodes", "branch 9←#1", "out: none", "◆ group") {
		t.Fatalf("crowded overview lacks collision or relationship details:\n%s", view)
	}
	total := 0
	for _, count := range renderer.overviewCounts {
		total += int(count)
	}
	if total != len(graph.Kinds) {
		t.Fatalf("overview cell population = %d, want %d", total, len(graph.Kinds))
	}

	graph = rendererSharedGraph()
	output, err = renderer.Append(output[:0], &graph, graphRenderOptions{
		Width: 48, Height: 9, Current: 4, Truth: debug.TruthBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	view = ansi.Strip(string(output))
	if !containsAll(view, "arg 1←#2", "arg 2←#3", "out: none") {
		t.Fatalf("overview lacks typed incoming relationships:\n%s", view)
	}
}

func TestGraphRendererCompactTraversalStartsAtRoot(t *testing.T) {
	graph := rendererSharedGraph()
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 18, Height: 4, Current: 4, Truth: debug.TruthTrue,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(output), "\n")
	if !strings.Contains(first, "#1 root") {
		t.Fatalf("compact traversal starts at %q, want graph root:\n%s", first, output)
	}
}

func TestGraphRendererWarmPathDoesNotAllocate(t *testing.T) {
	graph := rendererSharedGraph()
	options := graphRenderOptions{
		Width: 96, Height: 24, Current: 4, Truth: debug.TruthBoth, Color: true,
		Breakpoints: []breakpointBinding{{node: 2, id: 7}},
		Watches:     []watchBinding{{instruction: 3, id: 9}},
		Program:     true,
	}
	var renderer graphRenderer
	destination := make([]byte, 0, 64<<10)
	if _, err := renderer.Append(destination[:0], &graph, options); err != nil {
		t.Fatal(err)
	}
	var renderErr error
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.Append(destination[:0], &graph, options)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed terminal render = %.2f allocs/run, error=%v", allocations, renderErr)
	}

	options.Width = 18
	options.Height = 16
	options.Current = 1
	options.ExpandShared = true
	if _, err := renderer.Append(destination[:0], &graph, options); err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.Append(destination[:0], &graph, options)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed compact terminal render = %.2f allocs/run, error=%v", allocations, renderErr)
	}

	overview := rendererWideLayerGraph(20)
	options.Width = 48
	options.Height = 14
	options.Current = 10
	options.ExpandShared = false
	if _, err := renderer.Append(destination[:0], &overview, options); err != nil {
		t.Fatal(err)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.Append(destination[:0], &overview, options)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed overview terminal render = %.2f allocs/run, error=%v", allocations, renderErr)
	}
}

func BenchmarkGraphRenderer(b *testing.B) {
	graph := rendererChainGraph(256)
	options := graphRenderOptions{Width: 120, Height: 40, Current: 128, Truth: debug.TruthTrue, Color: true}
	var renderer graphRenderer
	destination := make([]byte, 0, 128<<10)
	output, err := renderer.Append(destination[:0], &graph, options)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(output)))
	b.ResetTimer()
	for range b.N {
		if _, err := renderer.Append(destination[:0], &graph, options); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(graph.Kinds)), "nodes")
	b.ReportMetric(float64(len(graph.Edges)), "edges")
}

func rendererSharedGraph() Graph {
	graph := testGraph(
		[]string{"root", "left", "right", "shared"},
		[]uint32{0, 2, 3, 4}, []uint16{2, 1, 1, 0}, []uint32{2, 3, 4, 4},
		[]graphview.NodeKind{graphview.NodeAll, graphview.NodeCompare, graphview.NodeEvidence, graphview.NodeCompare},
		graphview.EdgeArgument,
	)
	graph.EdgeKinds = []graphview.EdgeKind{
		graphview.EdgeApplies, graphview.EdgeAssertion, graphview.EdgeArgument, graphview.EdgeArgument,
	}
	graph.EdgeLabels = []string{"applies", "assert", "arg 1", "arg 2"}
	return graph
}

func rendererChainGraph(nodes int) Graph {
	labels := make([]string, nodes)
	details := make([]string, nodes)
	kinds := make([]graphview.NodeKind, nodes)
	starts := make([]uint32, nodes)
	counts := make([]uint16, nodes)
	edges := make([]uint32, 0, nodes-1)
	edgeKinds := make([]graphview.EdgeKind, 0, nodes-1)
	edgeLabels := make([]string, 0, nodes-1)
	for row := range nodes {
		labels[row] = "node-" + strconv.Itoa(row+1)
		details[row] = "chain node"
		kinds[row] = graphview.NodeInstruction
		starts[row] = uint32(len(edges))
		if row+1 < nodes {
			counts[row] = 1
			edges = append(edges, uint32(row+2))
			edgeKinds = append(edgeKinds, graphview.EdgeOperand)
			edgeLabels = append(edgeLabels, "operand 1")
		}
	}
	return Graph{
		Labels: labels, Details: details, Kinds: kinds,
		SourceStarts: make([]uint32, nodes), SourceEnds: make([]uint32, nodes),
		EdgeStarts: starts, EdgeCounts: counts, Edges: edges,
		EdgeKinds: edgeKinds, EdgeLabels: edgeLabels, Roots: []uint32{1},
	}
}

func rendererWideLayerGraph(nodes int) Graph {
	graph := Graph{
		Labels:       make([]string, nodes),
		Details:      make([]string, nodes),
		Kinds:        make([]graphview.NodeKind, nodes),
		SourceStarts: make([]uint32, nodes),
		SourceEnds:   make([]uint32, nodes),
		EdgeStarts:   make([]uint32, nodes),
		EdgeCounts:   make([]uint16, nodes),
		Edges:        make([]uint32, 0, nodes-1),
		EdgeKinds:    make([]graphview.EdgeKind, 0, nodes-1),
		EdgeLabels:   make([]string, 0, nodes-1),
		Roots:        []uint32{1},
	}
	graph.Labels[0] = "root"
	graph.Details[0] = "wide root"
	graph.Kinds[0] = graphview.NodePolicy
	graph.EdgeCounts[0] = uint16(nodes - 1)
	for row := 1; row < nodes; row++ {
		graph.Labels[row] = "node-" + strconv.Itoa(row+1)
		graph.Details[row] = "wide child"
		graph.Kinds[row] = graphview.NodeInstruction
		graph.EdgeStarts[row] = uint32(nodes - 1)
		graph.Edges = append(graph.Edges, uint32(row+1))
		graph.EdgeKinds = append(graph.EdgeKinds, graphview.EdgeOperand)
		graph.EdgeLabels = append(graph.EdgeLabels, "branch "+strconv.Itoa(row))
	}
	return graph
}

func graphOverviewMarkerCount(view string) int {
	markers := 0
	for _, character := range view {
		switch character {
		case '▶', '•', '·', 'B', 'W':
			markers++
		}
	}
	return markers
}
