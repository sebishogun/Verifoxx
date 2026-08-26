package graphview

import (
	"strconv"
	"unicode/utf8"
)

// Renderer retains validation and layout scratch across deterministic exports.
type Renderer struct {
	validator Validator
	layouter  Layouter
	nodeText  []byte
}

// AppendDOT appends a deterministic Graphviz representation to dst.
func (renderer *Renderer) AppendDOT(dst []byte, graph *Graph) ([]byte, error) {
	if renderer == nil {
		return dst, ErrInvalidGraph
	}
	if err := renderer.validator.Validate(graph, DefaultLimits()); err != nil {
		return dst, err
	}
	dst = append(dst, "digraph nornrune {\n"...)
	dst = append(dst, "  graph [rankdir=TB, bgcolor=\"transparent\"];\n"...)
	dst = append(dst, "  node [shape=box, style=\"rounded,filled\", fontname=\"monospace\"];\n"...)
	for row, kind := range graph.Kinds {
		dst = append(dst, "  n"...)
		dst = strconv.AppendUint(dst, uint64(row+1), 10)
		dst = append(dst, " [label=\""...)
		dst = appendDOTText(dst, graph.Labels[row])
		dst = append(dst, "\", tooltip=\""...)
		dst = appendDOTText(dst, graph.Details[row])
		dst = append(dst, "\", kind=\""...)
		dst = append(dst, kind.String()...)
		dst = append(dst, "\", fillcolor=\""...)
		dst = append(dst, graphNodeColor(kind)...)
		dst = append(dst, "\", source_start=\""...)
		dst = strconv.AppendUint(dst, uint64(graph.SourceStarts[row]), 10)
		dst = append(dst, "\", source_end=\""...)
		dst = strconv.AppendUint(dst, uint64(graph.SourceEnds[row]), 10)
		dst = append(dst, "\"];\n"...)
	}
	for source := range graph.Kinds {
		start := graph.EdgeStarts[source]
		end := start + uint32(graph.EdgeCounts[source])
		for edge := start; edge < end; edge++ {
			kind := graph.EdgeKinds[edge]
			dst = append(dst, "  n"...)
			dst = strconv.AppendUint(dst, uint64(source+1), 10)
			dst = append(dst, " -> n"...)
			dst = strconv.AppendUint(dst, uint64(graph.Edges[edge]), 10)
			dst = append(dst, " [label=\""...)
			dst = appendDOTText(dst, graph.EdgeLabels[edge])
			dst = append(dst, "\", color=\""...)
			dst = append(dst, graphEdgeColor(kind)...)
			dst = append(dst, "\", style=\""...)
			dst = append(dst, graphEdgeLineStyle(kind)...)
			dst = append(dst, "\", kind=\""...)
			dst = append(dst, kind.String()...)
			dst = append(dst, "\"];\n"...)
		}
	}
	dst = append(dst, "}\n"...)
	return dst, nil
}

func appendDOTText(dst []byte, value string) []byte {
	for _, character := range value {
		switch character {
		case '\\':
			dst = append(dst, "\\\\"...)
		case '"':
			dst = append(dst, "\\\""...)
		case '\n':
			dst = append(dst, "\\n"...)
		default:
			dst = utf8.AppendRune(dst, character)
		}
	}
	return dst
}

func graphNodeColor(kind NodeKind) string {
	switch kind {
	case NodePolicy:
		return "#64748b"
	case NodeRequirement:
		return "#8b5cf6"
	case NodeClause:
		return "#0ea5e9"
	case NodeCompare, NodeInstruction:
		return "#06b6d4"
	case NodeAll, NodeAny:
		return "#3b82f6"
	case NodeNot:
		return "#d946ef"
	case NodeEvidence:
		return "#f59e0b"
	case NodeRemediation:
		return "#2563eb"
	case NodeOutcome:
		return "#22c55e"
	default:
		return "#94a3b8"
	}
}

func graphEdgeColor(kind EdgeKind) string {
	switch kind {
	case EdgeApplies:
		return "#8b5cf6"
	case EdgeAssertion:
		return "#06b6d4"
	case EdgeEvidence, EdgeMissing, EdgeStale, EdgeUnclear, EdgeUnverifiable:
		return "#f59e0b"
	case EdgeSatisfied:
		return "#22c55e"
	case EdgeFalse:
		return "#ef4444"
	case EdgeConflict:
		return "#d946ef"
	case EdgeRemediation:
		return "#3b82f6"
	default:
		return "#64748b"
	}
}

func graphEdgeLineStyle(kind EdgeKind) string {
	if kind == EdgeEvidence || kind == EdgeRemediation {
		return "dashed"
	}
	return "solid"
}
