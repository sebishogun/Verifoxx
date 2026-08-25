package tui

import (
	"strconv"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"

	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/graphview"
)

const (
	graphNarrowWidth        = 20
	graphOverviewDetailRows = 2
	graphLegendRows         = 2
	graphOverviewMinHeight  = graphOverviewDetailRows + graphLegendRows + 3
)

type graphRenderOptions struct {
	Breakpoints  []breakpointBinding
	Watches      []watchBinding
	Width        int
	Height       int
	Current      uint32
	Truth        debug.TruthState
	Program      bool
	ExpandShared bool
	Color        bool
}

type graphCellStyle uint8

const (
	graphStyleNone graphCellStyle = iota
	graphStyleDim
	graphStyleCyan
	graphStyleBlue
	graphStyleMagenta
	graphStyleYellow
	graphStyleGreen
	graphStyleRed
	graphStyleWhite
)

type graphCell struct {
	rune  rune
	style graphCellStyle
}

// graphRenderer retains all layout, topology, canvas, and text scratch.
type graphRenderer struct {
	validator graphview.Validator
	layouter  graphview.Layouter
	cells     []graphCell
	active    []bool
	breaks    []bool
	watches   []bool

	reverseStarts  []uint32
	reverseCounts  []uint32
	reverseFill    []uint32
	reverseEdges   []uint32
	reverseIndexes []uint32
	queue          []uint32
	seen           []bool
	compactNext    []uint32
	compactEnds    []uint32
	overviewX      []int
	overviewY      []int
	overviewCounts []uint16

	text   []rune
	number []byte
	line   []byte
}

// Append renders graph into dst. After scratch and dst capacity are primed,
// the method performs no heap allocation.
func (renderer *graphRenderer) Append(dst []byte, graph *Graph, options graphRenderOptions) ([]byte, error) {
	if renderer == nil || graph == nil || options.Width <= 0 || options.Height <= 0 {
		return dst, ErrInvalidModel
	}
	if options.Width < graphNarrowWidth || options.Height < 5 {
		if err := renderer.validator.Validate(graph, graphview.DefaultLimits()); err != nil {
			return dst, err
		}
		return renderer.appendCompact(dst, graph, options), nil
	}

	layout, err := renderer.layouter.Layout(graph, graphview.DefaultLayoutOptions())
	if err != nil {
		return dst, err
	}
	graphHeight := options.Height - 2
	cellCount := options.Width * options.Height
	if cellCount/options.Width != options.Height {
		return dst, ErrInvalidModel
	}
	if options.Height >= graphOverviewMinHeight && (layout.Width > options.Width || layout.Height > graphHeight+2) {
		return renderer.appendOverview(dst, graph, layout, options, cellCount), nil
	}
	renderer.resetCanvas(cellCount)
	renderer.prepareState(graph, options)
	originX, originY := layout.Viewport(options.Width, graphHeight, options.Current)
	for _, edge := range layout.Edges {
		renderer.drawEdge(edge, originX, originY, options.Width, graphHeight)
	}
	for _, node := range layout.Nodes {
		renderer.drawNode(graph, node, options, originX, originY, options.Width, graphHeight)
	}
	renderer.drawText(0, options.Height-2, "▶ current  ✓ true  ✗ false  ! both  ? unknown",
		graphStyleWhite, options.Width, options.Height)
	renderer.drawText(0, options.Height-1, "• path  · dim  B break  W watch",
		graphStyleWhite, options.Width, options.Height)
	return renderer.appendCanvas(dst, options.Width, options.Height, options.Color), nil
}

func (renderer *graphRenderer) resetCanvas(cellCount int) {
	renderer.cells = resizeGraphSlice(renderer.cells, cellCount)
	for row := range renderer.cells {
		renderer.cells[row].rune = ' '
		renderer.cells[row].style = graphStyleNone
	}
}

func (renderer *graphRenderer) appendOverview(
	dst []byte,
	graph *Graph,
	layout graphview.Layout,
	options graphRenderOptions,
	cellCount int,
) []byte {
	renderer.resetCanvas(cellCount)
	renderer.prepareState(graph, options)
	renderer.overviewX = resizeGraphSlice(renderer.overviewX, len(layout.Nodes))
	renderer.overviewY = resizeGraphSlice(renderer.overviewY, len(layout.Nodes))

	maxLayer := uint16(0)
	for _, node := range layout.Nodes {
		maxLayer = max(maxLayer, node.Layer)
	}
	topologyHeight := options.Height - graphOverviewDetailRows - graphLegendRows
	renderer.overviewCounts = resizeGraphSlice(renderer.overviewCounts, options.Width*topologyHeight)
	clear(renderer.overviewCounts)
	for _, node := range layout.Nodes {
		row := node.ID - 1
		center := node.X + node.Width/2
		renderer.overviewX[row] = center * (options.Width - 1) / max(1, layout.Width)
		if maxLayer != 0 {
			renderer.overviewY[row] = int(node.Layer) * (topologyHeight - 1) / int(maxLayer)
		}
		cell := renderer.overviewY[row]*options.Width + renderer.overviewX[row]
		renderer.overviewCounts[cell]++
	}
	for _, edge := range layout.Edges {
		renderer.drawOverviewEdge(edge, options.Width, topologyHeight)
	}
	for _, node := range layout.Nodes {
		row := node.ID - 1
		marker := '·'
		style := renderer.nodeStyle(graph.Kinds[row], node.ID, options)
		switch {
		case renderer.overviewCounts[renderer.overviewY[row]*options.Width+renderer.overviewX[row]] > 1:
			marker = '◆'
			style = graphStyleMagenta
		case node.ID == options.Current:
			marker = '▶'
		case renderer.breaks[row]:
			marker = 'B'
		case renderer.watches[row]:
			marker = 'W'
		case renderer.active[row]:
			marker = '•'
		}
		renderer.setCell(renderer.overviewX[row], renderer.overviewY[row], marker,
			style, options.Width, topologyHeight, true)
	}

	selected := options.Current
	if selected == 0 || uint64(selected) > uint64(len(graph.Kinds)) {
		selected = graph.Roots[0]
	}
	renderer.drawOverviewDetails(graph, selected, options, topologyHeight)
	renderer.drawText(0, options.Height-2, "▶ current  ✓ true  ✗ false  ! both  ? unknown",
		graphStyleWhite, options.Width, options.Height)
	renderer.drawText(0, options.Height-1, "• path  · dim  B break  W watch  ◆ group",
		graphStyleWhite, options.Width, options.Height)
	return renderer.appendCanvas(dst, options.Width, options.Height, options.Color)
}

func (renderer *graphRenderer) drawOverviewEdge(edge graphview.Edge, width, height int) {
	fromX := renderer.overviewX[edge.From-1]
	fromY := renderer.overviewY[edge.From-1]
	toX := renderer.overviewX[edge.To-1]
	toY := renderer.overviewY[edge.To-1]
	style := graphEdgeStyle(edge.Kind)
	vertical, horizontal := '│', '─'
	if edge.Kind == graphview.EdgeEvidence || edge.Kind == graphview.EdgeRemediation {
		vertical, horizontal = '┆', '╌'
	}
	if toY <= fromY+1 {
		renderer.drawHorizontal(fromY, fromX, toX, horizontal, style, width, height)
		return
	}
	startY := fromY + 1
	endY := toY - 1
	middleY := startY + (endY-startY)/2
	renderer.drawVertical(fromX, startY, middleY, vertical, style, width, height)
	renderer.drawHorizontal(middleY, fromX, toX, horizontal, style, width, height)
	renderer.drawVertical(toX, middleY, endY, vertical, style, width, height)
	renderer.setCell(toX, endY, '▼', style, width, height, true)
}

func (renderer *graphRenderer) drawOverviewDetails(graph *Graph, selected uint32, options graphRenderOptions, y int) {
	renderer.text = renderer.text[:0]
	renderer.appendNodeMarker(selected, options)
	renderer.text = append(renderer.text, ' ', '#')
	renderer.number = strconv.AppendUint(renderer.number[:0], uint64(selected), 10)
	for _, character := range renderer.number {
		renderer.text = append(renderer.text, rune(character))
	}
	renderer.text = append(renderer.text, ' ')
	for _, character := range graph.Labels[selected-1] {
		renderer.text = append(renderer.text, character)
	}
	cell := renderer.overviewY[selected-1]*options.Width + renderer.overviewX[selected-1]
	if renderer.overviewCounts[cell] > 1 {
		renderer.text = append(renderer.text, ' ', ' ', 'c', 'l', 'u', 's', 't', 'e', 'r', ' ')
		renderer.number = strconv.AppendUint(renderer.number[:0], uint64(renderer.overviewCounts[cell]), 10)
		for _, character := range renderer.number {
			renderer.text = append(renderer.text, rune(character))
		}
	}
	renderer.text = append(renderer.text, ' ', ' ', '·', ' ')
	renderer.number = strconv.AppendUint(renderer.number[:0], uint64(len(graph.Kinds)), 10)
	for _, character := range renderer.number {
		renderer.text = append(renderer.text, rune(character))
	}
	renderer.text = append(renderer.text, ' ', 'n', 'o', 'd', 'e', 's')
	renderer.drawRunes(0, y, renderer.text, graphStyleWhite, options.Width, options.Width, options.Height)

	renderer.text = append(renderer.text[:0], 'i', 'n', ':', ' ')
	reverseStart := renderer.reverseStarts[selected-1]
	reverseEnd := reverseStart + renderer.reverseCounts[selected-1]
	for position := reverseStart; position < reverseEnd; position++ {
		if position != reverseStart {
			renderer.text = append(renderer.text, ' ', ' ')
		}
		edge := renderer.reverseIndexes[position]
		for _, character := range graph.EdgeLabels[edge] {
			renderer.text = append(renderer.text, character)
		}
		renderer.text = append(renderer.text, '←', '#')
		renderer.number = strconv.AppendUint(renderer.number[:0], uint64(renderer.reverseEdges[position]), 10)
		for _, character := range renderer.number {
			renderer.text = append(renderer.text, rune(character))
		}
	}
	if reverseStart == reverseEnd {
		renderer.text = append(renderer.text, 'n', 'o', 'n', 'e')
	}
	renderer.text = append(renderer.text, ' ', ' ', 'o', 'u', 't', ':', ' ')
	start := graph.EdgeStarts[selected-1]
	end := start + uint32(graph.EdgeCounts[selected-1])
	for edge := start; edge < end; edge++ {
		if edge != start {
			renderer.text = append(renderer.text, ' ', ' ')
		}
		for _, character := range graph.EdgeLabels[edge] {
			renderer.text = append(renderer.text, character)
		}
		renderer.text = append(renderer.text, '→', '#')
		renderer.number = strconv.AppendUint(renderer.number[:0], uint64(graph.Edges[edge]), 10)
		for _, character := range renderer.number {
			renderer.text = append(renderer.text, rune(character))
		}
	}
	if start == end {
		renderer.text = append(renderer.text, 'n', 'o', 'n', 'e')
	}
	renderer.drawRunes(0, y+1, renderer.text, graphStyleDim, options.Width, options.Width, options.Height)
}

func (renderer *graphRenderer) prepareState(graph *Graph, options graphRenderOptions) {
	nodes := len(graph.Kinds)
	renderer.active = resizeGraphSlice(renderer.active, nodes)
	renderer.breaks = resizeGraphSlice(renderer.breaks, nodes)
	renderer.watches = resizeGraphSlice(renderer.watches, nodes)
	clear(renderer.active)
	clear(renderer.breaks)
	clear(renderer.watches)
	if !options.Program {
		for _, binding := range options.Breakpoints {
			if binding.node != 0 && uint64(binding.node) <= uint64(nodes) {
				renderer.breaks[binding.node-1] = true
			}
		}
	} else {
		for _, binding := range options.Watches {
			if binding.instruction != 0 && uint64(binding.instruction) <= uint64(nodes) {
				renderer.watches[binding.instruction-1] = true
			}
		}
	}
	renderer.reverseCounts = resizeGraphSlice(renderer.reverseCounts, nodes)
	clear(renderer.reverseCounts)
	for _, destination := range graph.Edges {
		renderer.reverseCounts[destination-1]++
	}
	renderer.reverseStarts = resizeGraphSlice(renderer.reverseStarts, nodes+1)
	var edge uint32
	for row, count := range renderer.reverseCounts {
		renderer.reverseStarts[row] = edge
		edge += count
	}
	renderer.reverseStarts[nodes] = edge
	renderer.reverseFill = resizeGraphSlice(renderer.reverseFill, nodes)
	copy(renderer.reverseFill, renderer.reverseStarts[:nodes])
	renderer.reverseEdges = resizeGraphSlice(renderer.reverseEdges, len(graph.Edges))
	renderer.reverseIndexes = resizeGraphSlice(renderer.reverseIndexes, len(graph.Edges))
	for source := range graph.Kinds {
		start := graph.EdgeStarts[source]
		end := start + uint32(graph.EdgeCounts[source])
		for edge := start; edge < end; edge++ {
			destination := graph.Edges[edge]
			position := renderer.reverseFill[destination-1]
			renderer.reverseEdges[position] = uint32(source + 1)
			renderer.reverseIndexes[position] = edge
			renderer.reverseFill[destination-1]++
		}
	}
	if options.Current == 0 || uint64(options.Current) > uint64(nodes) {
		for row := range renderer.active {
			renderer.active[row] = true
		}
		return
	}
	renderer.queue = resizeGraphSlice(renderer.queue, nodes)
	renderer.markDescendants(graph, options.Current)
	renderer.markAncestors(options.Current)
}

func (renderer *graphRenderer) markDescendants(graph *Graph, current uint32) {
	start, end := 0, 1
	renderer.queue[0] = current
	renderer.active[current-1] = true
	for start < end {
		node := renderer.queue[start]
		start++
		first := graph.EdgeStarts[node-1]
		last := first + uint32(graph.EdgeCounts[node-1])
		for _, destination := range graph.Edges[first:last] {
			if renderer.active[destination-1] {
				continue
			}
			renderer.active[destination-1] = true
			renderer.queue[end] = destination
			end++
		}
	}
}

func (renderer *graphRenderer) markAncestors(current uint32) {
	start, end := 0, 1
	renderer.queue[0] = current
	for start < end {
		node := renderer.queue[start]
		start++
		first := renderer.reverseStarts[node-1]
		last := first + renderer.reverseCounts[node-1]
		for _, source := range renderer.reverseEdges[first:last] {
			if renderer.active[source-1] {
				continue
			}
			renderer.active[source-1] = true
			renderer.queue[end] = source
			end++
		}
	}
}

func (renderer *graphRenderer) drawEdge(edge graphview.Edge, originX, originY, width, height int) {
	style := graphEdgeStyle(edge.Kind)
	dashed := edge.Kind == graphview.EdgeEvidence || edge.Kind == graphview.EdgeRemediation
	vertical, horizontal := '│', '─'
	if dashed {
		vertical, horizontal = '┆', '╌'
	}
	points := edge.Points[:edge.PointCount]
	for row := 1; row < len(points); row++ {
		from, to := points[row-1], points[row]
		from.X, from.Y = from.X-originX, from.Y-originY
		to.X, to.Y = to.X-originX, to.Y-originY
		if from.X == to.X {
			renderer.drawVertical(from.X, from.Y, to.Y, vertical, style, width, height)
		} else if from.Y == to.Y {
			renderer.drawHorizontal(from.Y, from.X, to.X, horizontal, style, width, height)
		}
	}
	if len(points) >= 3 {
		left := min(points[1].X, points[2].X) - originX
		right := max(points[1].X, points[2].X) - originX
		labelWidth := graphTextWidth(edge.Label)
		x := left + (right-left-labelWidth)/2
		if left == right {
			x = left - labelWidth/2
		}
		renderer.drawText(x, points[1].Y-originY, edge.Label, style, width, height)
	}
	last := points[len(points)-1]
	renderer.setCell(last.X-originX, last.Y-originY-1, '▼', style, width, height, true)
}

func (renderer *graphRenderer) drawVertical(x, y1, y2 int, character rune, style graphCellStyle, width, height int) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		renderer.setCell(x, y, character, style, width, height, false)
	}
}

func (renderer *graphRenderer) drawHorizontal(y, x1, x2 int, character rune, style graphCellStyle, width, height int) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		renderer.setCell(x, y, character, style, width, height, false)
	}
}

func (renderer *graphRenderer) setCell(x, y int, character rune, style graphCellStyle, width, height int, force bool) {
	if x < 0 || y < 0 || x >= width || y >= height {
		return
	}
	cell := &renderer.cells[y*width+x]
	if !force && cell.rune != ' ' && cell.rune != character {
		if (cell.rune == '│' || cell.rune == '┆') && (character == '─' || character == '╌') ||
			(cell.rune == '─' || cell.rune == '╌') && (character == '│' || character == '┆') {
			cell.rune = '┼'
			cell.style = style
		}
		return
	}
	cell.rune = character
	cell.style = style
}

func (renderer *graphRenderer) drawNode(graph *Graph, node graphview.Node, options graphRenderOptions, originX, originY, width, height int) {
	x, y := node.X-originX, node.Y-originY
	style := renderer.nodeStyle(graph.Kinds[node.ID-1], node.ID, options)
	renderer.drawBox(x, y, node.Width, node.Height, style, width, height)
	renderer.text = renderer.text[:0]
	renderer.appendNodeMarker(node.ID, options)
	renderer.text = append(renderer.text, ' ', '#')
	renderer.number = strconv.AppendUint(renderer.number[:0], uint64(node.ID), 10)
	for _, character := range renderer.number {
		renderer.text = append(renderer.text, rune(character))
	}
	renderer.text = append(renderer.text, ' ')
	for _, character := range graph.Labels[node.ID-1] {
		renderer.text = append(renderer.text, character)
	}
	renderer.drawRunes(x+1, y+node.Height/2, renderer.text, style, min(node.Width-2, width), width, height)
}

func (renderer *graphRenderer) appendNodeMarker(id uint32, options graphRenderOptions) {
	row := id - 1
	if id == options.Current {
		renderer.text = append(renderer.text, '▶', truthGlyph(options.Truth))
		if renderer.breaks[row] {
			renderer.text = append(renderer.text, 'B')
		}
		if renderer.watches[row] {
			renderer.text = append(renderer.text, 'W')
		}
		return
	}
	if renderer.breaks[row] {
		renderer.text = append(renderer.text, 'B')
		return
	}
	if renderer.watches[row] {
		renderer.text = append(renderer.text, 'W')
		return
	}
	if renderer.active[row] {
		renderer.text = append(renderer.text, '•')
	} else {
		renderer.text = append(renderer.text, '·')
	}
}

func (renderer *graphRenderer) drawBox(x, y, boxWidth, boxHeight int, style graphCellStyle, width, height int) {
	renderer.setCell(x, y, '┌', style, width, height, true)
	renderer.setCell(x+boxWidth-1, y, '┐', style, width, height, true)
	renderer.setCell(x, y+boxHeight-1, '└', style, width, height, true)
	renderer.setCell(x+boxWidth-1, y+boxHeight-1, '┘', style, width, height, true)
	for column := 1; column < boxWidth-1; column++ {
		renderer.setCell(x+column, y, '─', style, width, height, true)
		renderer.setCell(x+column, y+boxHeight-1, '─', style, width, height, true)
	}
	for row := 1; row < boxHeight-1; row++ {
		renderer.setCell(x, y+row, '│', style, width, height, true)
		renderer.setCell(x+boxWidth-1, y+row, '│', style, width, height, true)
	}
}

func (renderer *graphRenderer) drawText(x, y int, text string, style graphCellStyle, width, height int) {
	for _, character := range text {
		characterWidth := max(1, runewidth.RuneWidth(character))
		if x+characterWidth > width {
			return
		}
		if x >= 0 {
			renderer.setCell(x, y, character, style, width, height, true)
			for continuation := 1; continuation < characterWidth; continuation++ {
				renderer.setCell(x+continuation, y, 0, style, width, height, true)
			}
		}
		x += characterWidth
	}
}

func (renderer *graphRenderer) drawRunes(x, y int, text []rune, style graphCellStyle, limit, width, height int) {
	written := 0
	for _, character := range text {
		characterWidth := max(1, runewidth.RuneWidth(character))
		if written+characterWidth > limit {
			return
		}
		renderer.setCell(x+written, y, character, style, width, height, true)
		for continuation := 1; continuation < characterWidth; continuation++ {
			renderer.setCell(x+written+continuation, y, 0, style, width, height, true)
		}
		written += characterWidth
	}
}

func (renderer *graphRenderer) nodeStyle(kind graphview.NodeKind, id uint32, options graphRenderOptions) graphCellStyle {
	row := id - 1
	if renderer.breaks[row] {
		return graphStyleRed
	}
	if id == options.Current {
		switch options.Truth {
		case debug.TruthTrue:
			return graphStyleGreen
		case debug.TruthFalse:
			return graphStyleRed
		case debug.TruthBoth:
			return graphStyleMagenta
		default:
			return graphStyleYellow
		}
	}
	if !renderer.active[row] {
		return graphStyleDim
	}
	switch kind {
	case graphview.NodeCompare, graphview.NodeInstruction:
		return graphStyleCyan
	case graphview.NodeAll, graphview.NodeAny:
		return graphStyleBlue
	case graphview.NodeNot:
		return graphStyleMagenta
	case graphview.NodeEvidence:
		return graphStyleYellow
	case graphview.NodeOutcome:
		return graphStyleGreen
	case graphview.NodeRemediation:
		return graphStyleBlue
	default:
		return graphStyleWhite
	}
}

func graphEdgeStyle(kind graphview.EdgeKind) graphCellStyle {
	switch kind {
	case graphview.EdgeApplies:
		return graphStyleMagenta
	case graphview.EdgeAssertion:
		return graphStyleCyan
	case graphview.EdgeEvidence, graphview.EdgeMissing, graphview.EdgeStale,
		graphview.EdgeUnclear, graphview.EdgeUnverifiable:
		return graphStyleYellow
	case graphview.EdgeSatisfied:
		return graphStyleGreen
	case graphview.EdgeFalse:
		return graphStyleRed
	case graphview.EdgeConflict:
		return graphStyleMagenta
	case graphview.EdgeRemediation:
		return graphStyleBlue
	default:
		return graphStyleDim
	}
}

func truthGlyph(truth debug.TruthState) rune {
	switch truth {
	case debug.TruthTrue:
		return '✓'
	case debug.TruthFalse:
		return '✗'
	case debug.TruthBoth:
		return '!'
	default:
		return '?'
	}
}

func graphTextWidth(text string) int {
	width := 0
	for _, character := range text {
		width += max(1, runewidth.RuneWidth(character))
	}
	return width
}

func (renderer *graphRenderer) appendCanvas(dst []byte, width, height int, color bool) []byte {
	for y := 0; y < height; y++ {
		last := width - 1
		for last >= 0 {
			cell := renderer.cells[y*width+last]
			if cell.rune != ' ' && cell.rune != 0 {
				break
			}
			last--
		}
		activeStyle := graphStyleNone
		for x := 0; x <= last; x++ {
			cell := renderer.cells[y*width+x]
			if cell.rune == 0 {
				continue
			}
			if color && cell.style != activeStyle {
				if activeStyle != graphStyleNone {
					dst = append(dst, "\x1b[0m"...)
				}
				activeStyle = cell.style
				if activeStyle != graphStyleNone {
					dst = appendGraphStyle(dst, activeStyle)
				}
			}
			dst = utf8.AppendRune(dst, cell.rune)
		}
		if color && activeStyle != graphStyleNone {
			dst = append(dst, "\x1b[0m"...)
		}
		if y+1 < height {
			dst = append(dst, '\n')
		}
	}
	return dst
}

func appendGraphStyle(dst []byte, style graphCellStyle) []byte {
	switch style {
	case graphStyleDim:
		return append(dst, "\x1b[2m"...)
	case graphStyleCyan:
		return append(dst, "\x1b[36m"...)
	case graphStyleBlue:
		return append(dst, "\x1b[34m"...)
	case graphStyleMagenta:
		return append(dst, "\x1b[35m"...)
	case graphStyleYellow:
		return append(dst, "\x1b[33m"...)
	case graphStyleGreen:
		return append(dst, "\x1b[32m"...)
	case graphStyleRed:
		return append(dst, "\x1b[31m"...)
	case graphStyleWhite:
		return append(dst, "\x1b[37m"...)
	default:
		return dst
	}
}

func (renderer *graphRenderer) appendCompact(dst []byte, graph *Graph, options graphRenderOptions) []byte {
	renderer.prepareState(graph, options)
	current := graph.Roots[0]
	lines := 0
	dst, lines = renderer.appendCompactNode(dst, graph, options, current, lines)
	contentLines := max(0, options.Height-1)
	for source := uint32(1); source <= uint32(len(graph.Kinds)) && lines < options.Height-1; source++ {
		start := graph.EdgeStarts[source-1]
		end := start + uint32(graph.EdgeCounts[source-1])
		for edge := start; edge < end && lines < options.Height-1; edge++ {
			if graph.Edges[edge] == current {
				dst, lines = renderer.appendCompactEdge(dst, graph, source, current, edge, options.Width, lines)
			}
		}
	}

	nodes := len(graph.Kinds)
	renderer.seen = resizeGraphSlice(renderer.seen, nodes)
	clear(renderer.seen)
	renderer.seen[current-1] = true
	renderer.queue = resizeGraphSlice(renderer.queue, nodes)
	renderer.compactNext = resizeGraphSlice(renderer.compactNext, nodes)
	renderer.compactEnds = resizeGraphSlice(renderer.compactEnds, nodes)
	renderer.queue[0] = current
	renderer.compactNext[0] = graph.EdgeStarts[current-1]
	renderer.compactEnds[0] = renderer.compactNext[0] + uint32(graph.EdgeCounts[current-1])
	depth := 1
	for depth != 0 && lines < contentLines {
		stackRow := depth - 1
		edge := renderer.compactNext[stackRow]
		if edge == renderer.compactEnds[stackRow] {
			depth--
			continue
		}
		renderer.compactNext[stackRow]++
		from := renderer.queue[stackRow]
		to := graph.Edges[edge]
		dst, lines = renderer.appendCompactEdge(dst, graph, from, to, edge, options.Width, lines)
		if lines >= contentLines {
			break
		}
		if renderer.seen[to-1] && !options.ExpandShared {
			dst, lines = renderer.appendCompactReference(dst, to, options.Width, lines)
			continue
		}
		renderer.seen[to-1] = true
		dst, lines = renderer.appendCompactNode(dst, graph, options, to, lines)
		if lines >= contentLines {
			break
		}
		renderer.queue[depth] = to
		renderer.compactNext[depth] = graph.EdgeStarts[to-1]
		renderer.compactEnds[depth] = renderer.compactNext[depth] + uint32(graph.EdgeCounts[to-1])
		depth++
	}
	if lines < options.Height {
		renderer.line = append(renderer.line[:0], "▶ current B break"...)
		dst, _ = appendGraphLine(dst, renderer.line, options.Width, lines != 0)
	}
	return dst
}

func (renderer *graphRenderer) appendCompactReference(dst []byte, id uint32, width, lines int) ([]byte, int) {
	renderer.line = append(renderer.line[:0], '#')
	renderer.line = strconv.AppendUint(renderer.line, uint64(id), 10)
	renderer.line = append(renderer.line, " [ref]"...)
	dst, _ = appendGraphLine(dst, renderer.line, width, lines != 0)
	return dst, lines + 1
}

func (renderer *graphRenderer) appendCompactNode(dst []byte, graph *Graph, options graphRenderOptions, id uint32, lines int) ([]byte, int) {
	renderer.line = renderer.line[:0]
	if id == options.Current {
		renderer.line = append(renderer.line, "▶"...)
		renderer.line = utf8.AppendRune(renderer.line, truthGlyph(options.Truth))
	} else if renderer.breaks[id-1] {
		renderer.line = append(renderer.line, 'B')
	} else if renderer.watches[id-1] {
		renderer.line = append(renderer.line, 'W')
	} else if renderer.active[id-1] {
		renderer.line = append(renderer.line, "•"...)
	} else {
		renderer.line = append(renderer.line, "·"...)
	}
	renderer.line = append(renderer.line, ' ', '#')
	renderer.line = strconv.AppendUint(renderer.line, uint64(id), 10)
	renderer.line = append(renderer.line, ' ')
	renderer.line = append(renderer.line, graph.Labels[id-1]...)
	dst, _ = appendGraphLine(dst, renderer.line, options.Width, lines != 0)
	return dst, lines + 1
}

func (renderer *graphRenderer) appendCompactEdge(dst []byte, graph *Graph, from, to, edge uint32, width, lines int) ([]byte, int) {
	renderer.line = renderer.line[:0]
	renderer.line = append(renderer.line, '#')
	renderer.line = strconv.AppendUint(renderer.line, uint64(from), 10)
	renderer.line = append(renderer.line, ' ', '-')
	renderer.line = append(renderer.line, graph.EdgeLabels[edge]...)
	renderer.line = append(renderer.line, "→ #"...)
	renderer.line = strconv.AppendUint(renderer.line, uint64(to), 10)
	dst, _ = appendGraphLine(dst, renderer.line, width, lines != 0)
	return dst, lines + 1
}

func appendGraphLine(dst, line []byte, width int, newline bool) ([]byte, int) {
	if newline {
		dst = append(dst, '\n')
	}
	used := 0
	for len(line) != 0 && used < width {
		character, size := utf8.DecodeRune(line)
		if character == utf8.RuneError && size == 1 {
			break
		}
		characterWidth := max(1, runewidth.RuneWidth(character))
		if used+characterWidth > width {
			break
		}
		dst = append(dst, line[:size]...)
		line = line[size:]
		used += characterWidth
	}
	return dst, used
}

func resizeGraphSlice[T any](destination []T, length int) []T {
	if cap(destination) < length {
		return make([]T, length)
	}
	return destination[:length]
}
