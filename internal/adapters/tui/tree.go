package tui

import (
	"strconv"
	"strings"
)

func renderGraph(graph Graph, expandShared bool, current uint32, maxLines int) string {
	renderer := treeRenderer{
		graph:        graph,
		current:      current,
		expandShared: expandShared,
		seen:         make([]bool, len(graph.Labels)),
		path:         make([]bool, len(graph.Labels)),
		maxLines:     max(1, maxLines),
	}
	renderer.output.Grow(min(len(graph.Labels), renderer.maxLines) * 16)
	for _, root := range graph.Roots {
		renderer.render(root, 0)
	}
	return strings.TrimSuffix(renderer.output.String(), "\n")
}

type treeRenderer struct {
	graph        Graph
	output       strings.Builder
	seen         []bool
	path         []bool
	maxLines     int
	lines        int
	current      uint32
	expandShared bool
}

func (renderer *treeRenderer) render(id uint32, depth int) {
	if renderer.lines >= renderer.maxLines {
		return
	}
	row := int(id - 1)
	renderer.output.WriteString(strings.Repeat("  ", depth))
	renderer.lines++
	if renderer.path[row] {
		renderer.output.WriteString("-> #")
		renderer.output.WriteString(strconv.FormatUint(uint64(id), 10))
		renderer.output.WriteString(" [cycle]\n")
		return
	}
	if renderer.seen[row] && !renderer.expandShared {
		renderer.output.WriteString("-> #")
		renderer.output.WriteString(strconv.FormatUint(uint64(id), 10))
		renderer.output.WriteString(" [ref]\n")
		return
	}
	renderer.seen[row] = true
	renderer.path[row] = true
	if id == renderer.current {
		renderer.output.WriteString("* #")
	} else {
		renderer.output.WriteString("- #")
	}
	renderer.output.WriteString(strconv.FormatUint(uint64(id), 10))
	renderer.output.WriteByte(' ')
	renderer.output.WriteString(renderer.graph.Labels[row])
	renderer.output.WriteByte('\n')
	start := int(renderer.graph.ChildStarts[row])
	end := start + int(renderer.graph.ChildCounts[row])
	for _, child := range renderer.graph.Children[start:end] {
		renderer.render(child, depth+1)
	}
	renderer.path[row] = false
}
