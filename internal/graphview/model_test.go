package graphview

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateAcceptsTypedSharedDAG(t *testing.T) {
	t.Parallel()

	graph := sharedTestGraph()
	if err := Validate(&graph, DefaultLimits()); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsMalformedGraph(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Graph)
		want   error
	}{
		{name: "missing labels", mutate: func(graph *Graph) { graph.Labels = graph.Labels[:3] }, want: ErrInvalidGraph},
		{name: "missing details", mutate: func(graph *Graph) { graph.Details = graph.Details[:3] }, want: ErrInvalidGraph},
		{name: "missing node kinds", mutate: func(graph *Graph) { graph.Kinds = graph.Kinds[:3] }, want: ErrInvalidGraph},
		{name: "missing source start", mutate: func(graph *Graph) { graph.SourceStarts = graph.SourceStarts[:3] }, want: ErrInvalidGraph},
		{name: "missing source end", mutate: func(graph *Graph) { graph.SourceEnds = graph.SourceEnds[:3] }, want: ErrInvalidGraph},
		{name: "missing edge start", mutate: func(graph *Graph) { graph.EdgeStarts = graph.EdgeStarts[:3] }, want: ErrInvalidGraph},
		{name: "missing edge count", mutate: func(graph *Graph) { graph.EdgeCounts = graph.EdgeCounts[:3] }, want: ErrInvalidGraph},
		{name: "edge metadata mismatch", mutate: func(graph *Graph) { graph.EdgeLabels = graph.EdgeLabels[:3] }, want: ErrInvalidGraph},
		{name: "noncontiguous csr", mutate: func(graph *Graph) { graph.EdgeStarts[1]++ }, want: ErrInvalidGraph},
		{name: "zero destination", mutate: func(graph *Graph) { graph.Edges[0] = 0 }, want: ErrInvalidGraph},
		{name: "large destination", mutate: func(graph *Graph) { graph.Edges[0] = 5 }, want: ErrInvalidGraph},
		{name: "invalid node kind", mutate: func(graph *Graph) { graph.Kinds[0] = NodeInvalid }, want: ErrInvalidGraph},
		{name: "invalid edge kind", mutate: func(graph *Graph) { graph.EdgeKinds[0] = EdgeInvalid }, want: ErrInvalidGraph},
		{name: "empty node label", mutate: func(graph *Graph) { graph.Labels[0] = "" }, want: ErrInvalidGraph},
		{name: "empty edge label", mutate: func(graph *Graph) { graph.EdgeLabels[0] = "" }, want: ErrInvalidGraph},
		{name: "control node label", mutate: func(graph *Graph) { graph.Labels[0] = "bad\nlabel" }, want: ErrInvalidGraph},
		{name: "control edge label", mutate: func(graph *Graph) { graph.EdgeLabels[0] = "bad\tedge" }, want: ErrInvalidGraph},
		{name: "invalid utf8", mutate: func(graph *Graph) { graph.Details[0] = string([]byte{0xff}) }, want: ErrInvalidGraph},
		{name: "source reversed", mutate: func(graph *Graph) { graph.SourceStarts[1], graph.SourceEnds[1] = 9, 8 }, want: ErrInvalidGraph},
		{name: "source outside document", mutate: func(graph *Graph) { graph.SourceEnds[1] = 65 }, want: ErrInvalidGraph},
		{name: "zero root", mutate: func(graph *Graph) { graph.Roots[0] = 0 }, want: ErrInvalidGraph},
		{name: "duplicate root", mutate: func(graph *Graph) { graph.Roots = append(graph.Roots, graph.Roots[0]) }, want: ErrInvalidGraph},
		{name: "cycle", mutate: func(graph *Graph) {
			graph.EdgeStarts[3] = uint32(len(graph.Edges))
			graph.EdgeCounts[3] = 1
			graph.Edges = append(graph.Edges, 1)
			graph.EdgeKinds = append(graph.EdgeKinds, EdgeOperand)
			graph.EdgeLabels = append(graph.EdgeLabels, "cycle")
		}, want: ErrGraphCycle},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			graph := sharedTestGraph()
			test.mutate(&graph)
			if err := Validate(&graph, DefaultLimits()); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestValidateEnforcesLimitsBeforeContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Graph, *Limits)
	}{
		{name: "nodes", mutate: func(_ *Graph, limits *Limits) { limits.MaxNodes = 3 }},
		{name: "edges", mutate: func(_ *Graph, limits *Limits) { limits.MaxEdges = 3 }},
		{name: "roots", mutate: func(_ *Graph, limits *Limits) { limits.MaxRoots = 0 }},
		{name: "label bytes", mutate: func(graph *Graph, limits *Limits) {
			limits.MaxLabelBytes = len(graph.Labels[0]) - 1
		}},
		{name: "detail bytes", mutate: func(graph *Graph, limits *Limits) {
			limits.MaxDetailBytes = len(graph.Details[0]) - 1
		}},
		{name: "edge label bytes", mutate: func(graph *Graph, limits *Limits) {
			limits.MaxEdgeLabelBytes = len(graph.EdgeLabels[0]) - 1
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			graph := sharedTestGraph()
			limits := DefaultLimits()
			test.mutate(&graph, &limits)
			if err := Validate(&graph, limits); !errors.Is(err, ErrGraphLimit) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrGraphLimit)
			}
		})
	}
}

func TestKindNamesRemainBoundedAndReadable(t *testing.T) {
	t.Parallel()

	for kind := NodePolicy; kind <= NodeInstruction; kind++ {
		if name := kind.String(); name == "invalid" || strings.ContainsAny(name, "\n\r\t") {
			t.Fatalf("NodeKind(%d).String() = %q", kind, name)
		}
	}
	for kind := EdgeContains; kind <= EdgeOperand; kind++ {
		if name := kind.String(); name == "invalid" || strings.ContainsAny(name, "\n\r\t") {
			t.Fatalf("EdgeKind(%d).String() = %q", kind, name)
		}
	}
}

func sharedTestGraph() Graph {
	return Graph{
		Labels:       []string{"policy", "left", "right", "shared"},
		Details:      []string{"policy detail", "left detail", "right detail", "shared detail"},
		Kinds:        []NodeKind{NodePolicy, NodeAll, NodeAny, NodeCompare},
		SourceStarts: []uint32{0, 1, 12, 24},
		SourceEnds:   []uint32{0, 10, 20, 32},
		EdgeStarts:   []uint32{0, 2, 3, 4},
		EdgeCounts:   []uint16{2, 1, 1, 0},
		Edges:        []uint32{2, 3, 4, 4},
		EdgeKinds:    []EdgeKind{EdgeApplies, EdgeClause, EdgeArgument, EdgeArgument},
		EdgeLabels:   []string{"applies", "clause 1", "arg 1", "arg 1"},
		Roots:        []uint32{1},
		SourceLength: 64,
	}
}
