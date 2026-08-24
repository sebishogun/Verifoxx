package graphview

import "testing"

func FuzzGraphValidationAndRendering(f *testing.F) {
	f.Add([]byte("semantic"), uint8(8), uint8(0))
	f.Add([]byte{0xff, 0xfe}, uint8(2), uint8(1))
	f.Fuzz(func(t *testing.T, payload []byte, size, mutation uint8) {
		nodes := int(size%64) + 1
		graph := wideTestGraph(nodes)
		switch mutation % 6 {
		case 0:
			if len(payload) != 0 {
				graph.Labels[int(mutation)%nodes] = string(payload)
			}
		case 1:
			if len(graph.Edges) != 0 && len(payload) != 0 {
				graph.Edges[0] = uint32(payload[0])
			}
		case 2:
			graph.EdgeStarts[int(mutation)%nodes]++
		case 3:
			graph.Roots = append(graph.Roots, graph.Roots[0])
		case 4:
			graph.SourceEnds[int(mutation)%nodes] = graph.SourceLength + 1
		case 5:
			graph.EdgeLabels = graph.EdgeLabels[:len(graph.EdgeLabels)/2]
		}

		if err := Validate(&graph, DefaultLimits()); err != nil {
			return
		}
		var renderer Renderer
		if _, err := renderer.AppendDOT(nil, &graph); err != nil {
			t.Fatalf("AppendDOT(valid graph) error = %v", err)
		}
		if _, err := renderer.AppendSVG(nil, &graph); err != nil {
			t.Fatalf("AppendSVG(valid graph) error = %v", err)
		}
		if _, err := renderer.AppendHTML(nil, &graph, &graph); err != nil {
			t.Fatalf("AppendHTML(valid graph) error = %v", err)
		}
	})
}
