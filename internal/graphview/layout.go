package graphview

import (
	"errors"
	"slices"
)

var ErrInvalidLayout = errors.New("graphview: invalid layout configuration")

// LayoutOptions controls fixed-cell semantic graph geometry.
type LayoutOptions struct {
	NodeWidth     int
	NodeHeight    int
	HorizontalGap int
	VerticalGap   int
}

// DefaultLayoutOptions returns geometry shared by terminal and export views.
func DefaultLayoutOptions() LayoutOptions {
	return LayoutOptions{NodeWidth: 20, NodeHeight: 3, HorizontalGap: 6, VerticalGap: 4}
}

func (options LayoutOptions) valid() bool {
	return options.NodeWidth >= 8 && options.NodeWidth <= 256 &&
		options.NodeHeight >= 3 && options.NodeHeight <= 32 &&
		options.HorizontalGap >= 2 && options.HorizontalGap <= 256 &&
		options.VerticalGap >= 2 && options.VerticalGap <= 256
}

// Point is one integer canvas coordinate.
type Point struct {
	X int
	Y int
}

// Node is one laid-out graph node. Nodes remain indexed by Graph ID minus one.
type Node struct {
	X      int
	Y      int
	Width  int
	Height int
	ID     uint32
	Layer  uint16
}

// Edge is one orthogonal route with borrowed immutable label text.
type Edge struct {
	Label      string
	Points     [4]Point
	From       uint32
	To         uint32
	Kind       EdgeKind
	PointCount uint8
}

// Layout is valid until the next call on the Layouter that produced it.
type Layout struct {
	Nodes  []Node
	Edges  []Edge
	Width  int
	Height int
}

// Viewport returns a clamped origin that contains and centers current.
func (layout Layout) Viewport(width, height int, current uint32) (int, int) {
	if width <= 0 || height <= 0 || current == 0 || uint64(current) > uint64(len(layout.Nodes)) {
		return 0, 0
	}
	node := layout.Nodes[current-1]
	x := node.X + node.Width/2 - width/2
	y := node.Y + node.Height/2 - height/2
	maxX := max(0, layout.Width-width)
	maxY := max(0, layout.Height-height)
	return min(max(0, x), maxX), min(max(0, y), maxY)
}

// Layouter retains uniformly typed scratch and output slices across layouts.
type Layouter struct {
	validator     Validator
	nodes         []Node
	edges         []Edge
	layers        []uint16
	indegree      []uint32
	queue         []uint32
	order         []uint32
	positions     []uint32
	barySums      []uint64
	baryCounts    []uint32
	reverseStarts []uint32
	reverseCounts []uint32
	reverseFill   []uint32
	reverseEdges  []uint32
	layerCounts   []int
	layerStarts   []int
	layerFill     []int
}

// Layout validates graph and computes deterministic root-to-dependency layers.
func (layouter *Layouter) Layout(graph *Graph, options LayoutOptions) (Layout, error) {
	if layouter == nil || !options.valid() {
		return Layout{}, ErrInvalidLayout
	}
	if err := layouter.validator.Validate(graph, DefaultLimits()); err != nil {
		return Layout{}, err
	}
	nodeCount := len(graph.Kinds)
	layouter.layers = resizeClear(layouter.layers, nodeCount)
	layouter.indegree = resizeClear(layouter.indegree, nodeCount)
	layouter.queue = resizeClear(layouter.queue, nodeCount)
	layouter.nodes = resizeClear(layouter.nodes, nodeCount)
	layouter.edges = resizeClear(layouter.edges, len(graph.Edges))

	for _, destination := range graph.Edges {
		layouter.indegree[destination-1]++
	}
	queueEnd := 0
	for row, degree := range layouter.indegree {
		if degree == 0 {
			layouter.queue[queueEnd] = uint32(row + 1)
			queueEnd++
		}
	}
	maxLayer := uint16(0)
	for cursor := 0; cursor < queueEnd; cursor++ {
		id := layouter.queue[cursor]
		row := id - 1
		childLayer := layouter.layers[row] + 1
		start := graph.EdgeStarts[row]
		end := start + uint32(graph.EdgeCounts[row])
		for _, destination := range graph.Edges[start:end] {
			child := destination - 1
			if layouter.layers[child] < childLayer {
				layouter.layers[child] = childLayer
				maxLayer = max(maxLayer, childLayer)
			}
			layouter.indegree[child]--
			if layouter.indegree[child] == 0 {
				layouter.queue[queueEnd] = destination
				queueEnd++
			}
		}
	}
	if queueEnd != nodeCount {
		return Layout{}, ErrGraphCycle
	}

	layerCount := int(maxLayer) + 1
	layouter.layerCounts = resizeClear(layouter.layerCounts, layerCount)
	for _, layer := range layouter.layers {
		layouter.layerCounts[layer]++
	}
	maxColumns := 0
	for _, count := range layouter.layerCounts {
		maxColumns = max(maxColumns, count)
	}
	if maxColumns > 1 {
		layouter.prepareLayerOrder(nodeCount, layerCount)
		layouter.buildReverseEdges(graph)
		layouter.orderByBarycenter(graph, layerCount)
	}
	pitchX := options.NodeWidth + options.HorizontalGap
	pitchY := options.NodeHeight + options.VerticalGap
	width := maxColumns*options.NodeWidth + max(0, maxColumns-1)*options.HorizontalGap
	if maxColumns == 1 {
		for row, layer := range layouter.layers {
			layouter.nodes[row] = Node{
				X: 0, Y: int(layer) * pitchY, Width: options.NodeWidth, Height: options.NodeHeight,
				ID: uint32(row + 1), Layer: layer,
			}
		}
	} else {
		for layer := range layerCount {
			layerWidth := layouter.layerCounts[layer]*options.NodeWidth + max(0, layouter.layerCounts[layer]-1)*options.HorizontalGap
			for column, id := range layouter.order[layouter.layerStarts[layer]:layouter.layerStarts[layer+1]] {
				x := (width-layerWidth)/2 + column*pitchX
				y := layer * pitchY
				layouter.nodes[id-1] = Node{
					X: x, Y: y, Width: options.NodeWidth, Height: options.NodeHeight,
					ID: id, Layer: uint16(layer),
				}
			}
		}
	}

	edgeRow := 0
	for source := range nodeCount {
		from := layouter.nodes[source]
		start := graph.EdgeStarts[source]
		end := start + uint32(graph.EdgeCounts[source])
		for index := start; index < end; index++ {
			to := layouter.nodes[graph.Edges[index]-1]
			fromPoint := Point{X: from.X + from.Width/2, Y: from.Y + from.Height - 1}
			toPoint := Point{X: to.X + to.Width/2, Y: to.Y}
			middleY := fromPoint.Y + max(1, (toPoint.Y-fromPoint.Y)/2)
			layouter.edges[edgeRow] = Edge{
				Label: graph.EdgeLabels[index], From: uint32(source + 1), To: graph.Edges[index],
				Kind: graph.EdgeKinds[index], PointCount: 4,
				Points: [4]Point{fromPoint, {X: fromPoint.X, Y: middleY}, {X: toPoint.X, Y: middleY}, toPoint},
			}
			edgeRow++
		}
	}
	return Layout{
		Nodes:  layouter.nodes,
		Edges:  layouter.edges,
		Width:  width,
		Height: int(maxLayer)*pitchY + options.NodeHeight,
	}, nil
}

func (layouter *Layouter) prepareLayerOrder(nodeCount, layerCount int) {
	layouter.order = resizeClear(layouter.order, nodeCount)
	layouter.positions = resizeClear(layouter.positions, nodeCount)
	layouter.barySums = resizeClear(layouter.barySums, nodeCount)
	layouter.baryCounts = resizeClear(layouter.baryCounts, nodeCount)
	layouter.layerFill = resizeClear(layouter.layerFill, layerCount)
	layouter.layerStarts = resizeClear(layouter.layerStarts, layerCount+1)
	for layer, count := range layouter.layerCounts {
		layouter.layerStarts[layer+1] = layouter.layerStarts[layer] + count
		layouter.layerFill[layer] = layouter.layerStarts[layer]
	}
	for row, layer := range layouter.layers {
		position := layouter.layerFill[layer]
		layouter.order[position] = uint32(row + 1)
		layouter.positions[row] = uint32(position - layouter.layerStarts[layer])
		layouter.layerFill[layer]++
	}
}

func (layouter *Layouter) buildReverseEdges(graph *Graph) {
	nodes := len(graph.Kinds)
	layouter.reverseCounts = resizeClear(layouter.reverseCounts, nodes)
	for _, destination := range graph.Edges {
		layouter.reverseCounts[destination-1]++
	}
	layouter.reverseStarts = resizeClear(layouter.reverseStarts, nodes+1)
	var edge uint32
	for row, count := range layouter.reverseCounts {
		layouter.reverseStarts[row] = edge
		edge += count
	}
	layouter.reverseStarts[nodes] = edge
	layouter.reverseFill = resizeClear(layouter.reverseFill, nodes)
	copy(layouter.reverseFill, layouter.reverseStarts[:nodes])
	layouter.reverseEdges = resizeClear(layouter.reverseEdges, len(graph.Edges))
	for source := range graph.Kinds {
		start := graph.EdgeStarts[source]
		end := start + uint32(graph.EdgeCounts[source])
		for _, destination := range graph.Edges[start:end] {
			position := layouter.reverseFill[destination-1]
			layouter.reverseEdges[position] = uint32(source + 1)
			layouter.reverseFill[destination-1]++
		}
	}
}

func (layouter *Layouter) orderByBarycenter(graph *Graph, layerCount int) {
	const sweeps = 2
	for range sweeps {
		for layer := 1; layer < layerCount; layer++ {
			start := layouter.layerStarts[layer]
			end := layouter.layerStarts[layer+1]
			for _, id := range layouter.order[start:end] {
				row := id - 1
				first := layouter.reverseStarts[row]
				last := first + layouter.reverseCounts[row]
				var sum uint64
				for _, source := range layouter.reverseEdges[first:last] {
					sum += uint64(layouter.positions[source-1])
				}
				layouter.setBarycenter(id, sum, last-first)
			}
			layouter.sortLayer(start, end)
		}
		for layer := layerCount - 2; layer >= 0; layer-- {
			start := layouter.layerStarts[layer]
			end := layouter.layerStarts[layer+1]
			for _, id := range layouter.order[start:end] {
				row := id - 1
				first := graph.EdgeStarts[row]
				last := first + uint32(graph.EdgeCounts[row])
				var sum uint64
				for _, destination := range graph.Edges[first:last] {
					sum += uint64(layouter.positions[destination-1])
				}
				layouter.setBarycenter(id, sum, last-first)
			}
			layouter.sortLayer(start, end)
		}
	}
}

func (layouter *Layouter) setBarycenter(id uint32, sum uint64, count uint32) {
	row := id - 1
	if count == 0 {
		sum = uint64(layouter.positions[row])
		count = 1
	}
	layouter.barySums[row] = sum
	layouter.baryCounts[row] = count
}

func (layouter *Layouter) sortLayer(start, end int) {
	slices.SortFunc(layouter.order[start:end], func(left, right uint32) int {
		leftRow := left - 1
		rightRow := right - 1
		leftValue := layouter.barySums[leftRow] * uint64(layouter.baryCounts[rightRow])
		rightValue := layouter.barySums[rightRow] * uint64(layouter.baryCounts[leftRow])
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
		return int(left) - int(right)
	})
	for column, id := range layouter.order[start:end] {
		layouter.positions[id-1] = uint32(column)
	}
}

func resizeClear[T any](destination []T, length int) []T {
	if cap(destination) < length {
		return make([]T, length)
	}
	destination = destination[:length]
	clear(destination)
	return destination
}
