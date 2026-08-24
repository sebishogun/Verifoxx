package graphview

import "testing"

func TestLayouterWarmPathDoesNotAllocate(t *testing.T) {
	graph := wideTestGraph(256)
	var layouter Layouter
	if _, err := layouter.Layout(&graph, DefaultLayoutOptions()); err != nil {
		t.Fatal(err)
	}
	var layout Layout
	var layoutErr error
	if allocations := testing.AllocsPerRun(100, func() {
		layout, layoutErr = layouter.Layout(&graph, DefaultLayoutOptions())
	}); allocations != 0 {
		t.Fatalf("warm Layout allocations = %f, want 0", allocations)
	}
	if layoutErr != nil || len(layout.Nodes) != len(graph.Labels) {
		t.Fatalf("Layout() = nodes %d, error %v", len(layout.Nodes), layoutErr)
	}
}

func BenchmarkLayout(b *testing.B) {
	for _, nodes := range []int{64, 256, 1024} {
		graph := wideTestGraph(nodes)
		var layouter Layouter
		if _, err := layouter.Layout(&graph, DefaultLayoutOptions()); err != nil {
			b.Fatal(err)
		}
		b.Run(layoutBenchmarkName(nodes), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, err := layouter.Layout(&graph, DefaultLayoutOptions()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func layoutBenchmarkName(nodes int) string {
	switch nodes {
	case 64:
		return "nodes=64"
	case 256:
		return "nodes=256"
	case 1024:
		return "nodes=1024"
	default:
		return "nodes=other"
	}
}
