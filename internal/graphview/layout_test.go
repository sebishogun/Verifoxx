package graphview

import (
	"reflect"
	"testing"
)

func TestLayouterBuildsStableSharedDAG(t *testing.T) {
	t.Parallel()

	graph := sharedTestGraph()
	var layouter Layouter
	got, err := layouter.Layout(&graph, DefaultLayoutOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 4 || len(got.Edges) != 4 {
		t.Fatalf("layout sizes = nodes %d edges %d, want 4/4", len(got.Nodes), len(got.Edges))
	}
	wantLayers := []uint16{0, 1, 1, 2}
	for row, want := range wantLayers {
		if got.Nodes[row].Layer != want {
			t.Errorf("node %d layer = %d, want %d", row+1, got.Nodes[row].Layer, want)
		}
	}
	if got.Nodes[1].X >= got.Nodes[2].X {
		t.Fatalf("stable layer order = node2 x=%d node3 x=%d", got.Nodes[1].X, got.Nodes[2].X)
	}
	if got.Edges[0].Label != "applies" || got.Edges[0].Kind != EdgeApplies || got.Edges[0].PointCount < 2 {
		t.Fatalf("first edge = %+v", got.Edges[0])
	}
	if got.Edges[2].To != 4 || got.Edges[3].To != 4 || got.Nodes[3].Layer != 2 {
		t.Fatalf("shared destination was duplicated: nodes=%+v edges=%+v", got.Nodes, got.Edges)
	}
}

func TestLayouterIsIndependentOfRetainedScratch(t *testing.T) {
	t.Parallel()

	graph := sharedTestGraph()
	var layouter Layouter
	first, err := layouter.Layout(&graph, DefaultLayoutOptions())
	if err != nil {
		t.Fatal(err)
	}
	first = cloneLayout(first)

	large := wideTestGraph(64)
	if _, err := layouter.Layout(&large, DefaultLayoutOptions()); err != nil {
		t.Fatal(err)
	}
	second, err := layouter.Layout(&graph, DefaultLayoutOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("layout changed after scratch growth:\nfirst=%+v\nsecond=%+v", first, second)
	}
}

func TestLayouterUsesStableBarycentersToReduceCrossings(t *testing.T) {
	graph := Graph{
		Labels:       []string{"left parent", "right parent", "right child", "left child"},
		Details:      []string{"parent", "parent", "child", "child"},
		Kinds:        []NodeKind{NodeInstruction, NodeInstruction, NodeInstruction, NodeInstruction},
		SourceStarts: []uint32{0, 0, 0, 0},
		SourceEnds:   []uint32{0, 0, 0, 0},
		EdgeStarts:   []uint32{0, 1, 2, 2},
		EdgeCounts:   []uint16{1, 1, 0, 0},
		Edges:        []uint32{4, 3},
		EdgeKinds:    []EdgeKind{EdgeOperand, EdgeOperand},
		EdgeLabels:   []string{"operand 1", "operand 1"},
		Roots:        []uint32{1, 2},
	}
	var layouter Layouter
	layout, err := layouter.Layout(&graph, DefaultLayoutOptions())
	if err != nil {
		t.Fatal(err)
	}
	if layout.Nodes[3].X >= layout.Nodes[2].X {
		t.Fatalf("barycenter order crosses edges: left child x=%d right child x=%d", layout.Nodes[3].X, layout.Nodes[2].X)
	}
	leftX, rightX := layout.Nodes[3].X, layout.Nodes[2].X
	repeated, err := layouter.Layout(&graph, DefaultLayoutOptions())
	if err != nil || leftX != repeated.Nodes[3].X || rightX != repeated.Nodes[2].X {
		t.Fatalf("barycenter order is unstable: first=%d,%d repeated=%d,%d error=%v", leftX, rightX, repeated.Nodes[3].X, repeated.Nodes[2].X, err)
	}
}

func TestLayoutViewportCentersCurrentNodeAndClamps(t *testing.T) {
	t.Parallel()

	graph := wideTestGraph(8)
	var layouter Layouter
	layout, err := layouter.Layout(&graph, DefaultLayoutOptions())
	if err != nil {
		t.Fatal(err)
	}
	x, y := layout.Viewport(30, 8, 8)
	current := layout.Nodes[7]
	if current.X < x || current.X+current.Width > x+30 || current.Y < y || current.Y+current.Height > y+8 {
		t.Fatalf("current node %+v outside viewport [%d,%d 30x8]", current, x, y)
	}
	if x < 0 || y < 0 || x+30 > max(30, layout.Width) || y+8 > max(8, layout.Height) {
		t.Fatalf("viewport = %d,%d outside layout %dx%d", x, y, layout.Width, layout.Height)
	}

	x, y = layout.Viewport(layout.Width+100, layout.Height+100, 0)
	if x != 0 || y != 0 {
		t.Fatalf("oversized viewport origin = %d,%d, want 0,0", x, y)
	}
}

func TestLayouterRejectsInvalidOptionsAndGraph(t *testing.T) {
	t.Parallel()

	graph := sharedTestGraph()
	var layouter Layouter
	options := DefaultLayoutOptions()
	options.NodeWidth = 0
	if _, err := layouter.Layout(&graph, options); err == nil {
		t.Fatal("Layout() accepted zero node width")
	}
	graph.Edges[3] = 1
	if _, err := layouter.Layout(&graph, DefaultLayoutOptions()); err == nil {
		t.Fatal("Layout() accepted a cycle")
	}
}

func cloneLayout(source Layout) Layout {
	destination := source
	destination.Nodes = append([]Node(nil), source.Nodes...)
	destination.Edges = append([]Edge(nil), source.Edges...)
	return destination
}

func wideTestGraph(nodes int) Graph {
	graph := Graph{
		Labels:       make([]string, nodes),
		Details:      make([]string, nodes),
		Kinds:        make([]NodeKind, nodes),
		SourceStarts: make([]uint32, nodes),
		SourceEnds:   make([]uint32, nodes),
		EdgeStarts:   make([]uint32, nodes),
		EdgeCounts:   make([]uint16, nodes),
		Roots:        []uint32{1},
		SourceLength: uint32(nodes * 2),
	}
	for row := range nodes {
		graph.Labels[row] = "node"
		graph.Details[row] = "detail"
		graph.Kinds[row] = NodeInstruction
		graph.SourceStarts[row] = uint32(row)
		graph.SourceEnds[row] = uint32(row + 1)
		graph.EdgeStarts[row] = uint32(len(graph.Edges))
		if row+1 < nodes {
			graph.EdgeCounts[row] = 1
			graph.Edges = append(graph.Edges, uint32(row+2))
			graph.EdgeKinds = append(graph.EdgeKinds, EdgeOperand)
			graph.EdgeLabels = append(graph.EdgeLabels, "operand 1")
		}
	}
	return graph
}
