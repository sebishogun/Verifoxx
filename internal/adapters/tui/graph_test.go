package tui

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/graphview"
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

func TestGraphRendererCentersCurrentNodeInClippedViewport(t *testing.T) {
	graph := rendererChainGraph(8)
	var renderer graphRenderer
	output, err := renderer.Append(nil, &graph, graphRenderOptions{
		Width: 24, Height: 7, Current: 8, Truth: debug.TruthBoth,
	})
	if err != nil {
		t.Fatal(err)
	}
	view := string(output)
	if !strings.Contains(view, "▶! #8 node-8") || strings.Contains(view, "#1 node-1") {
		t.Fatalf("viewport did not center current node:\n%s", view)
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
