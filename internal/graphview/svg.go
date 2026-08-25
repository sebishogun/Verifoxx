package graphview

import (
	"strconv"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const (
	svgCellWidth      = 10
	svgCellHeight     = 20
	svgTextLineHeight = 18
	svgMargin         = 20
	svgLabelCharWidth = 7
	svgLabelPadding   = 6
	svgLabelHeight    = 18
)

// AppendSVG appends a standalone deterministic SVG representation to dst.
func (renderer *Renderer) AppendSVG(dst []byte, graph *Graph) ([]byte, error) {
	return renderer.appendSVG(dst, graph, "graph")
}

func (renderer *Renderer) appendSVG(dst []byte, graph *Graph, prefix string) ([]byte, error) {
	if renderer == nil || graph == nil {
		return dst, ErrInvalidGraph
	}
	if err := renderer.validator.Validate(graph, DefaultLimits()); err != nil {
		return dst, err
	}
	options := DefaultLayoutOptions()
	maxLines := 1
	for row, label := range graph.Labels {
		renderer.nodeText = appendSVGNodeText(renderer.nodeText[:0], uint32(row+1), label)
		maxLines = max(maxLines, svgWrappedLineCount(renderer.nodeText, options.NodeWidth-2))
	}
	options.NodeHeight = max(options.NodeHeight, maxLines+1)
	layout, err := renderer.layouter.layoutValidated(graph, options)
	if err != nil {
		return dst, err
	}
	width := layout.Width*svgCellWidth + svgMargin*2
	height := layout.Height*svgCellHeight + svgMargin*2
	dst = append(dst, `<svg xmlns="http://www.w3.org/2000/svg" id="`...)
	dst = appendXMLText(dst, prefix)
	dst = append(dst, `-graph" role="group" aria-label="`...)
	dst = appendXMLText(dst, prefix)
	dst = append(dst, ` semantic graph" viewBox="0 0 `...)
	dst = strconv.AppendInt(dst, int64(width), 10)
	dst = append(dst, ' ')
	dst = strconv.AppendInt(dst, int64(height), 10)
	dst = append(dst, `" width="`...)
	dst = strconv.AppendInt(dst, int64(width), 10)
	dst = append(dst, `" height="`...)
	dst = strconv.AppendInt(dst, int64(height), 10)
	dst = append(dst, `">`...)
	dst = append(dst, `<defs><marker id="`...)
	dst = appendXMLText(dst, prefix)
	dst = append(dst, `-arrow" markerWidth="10" markerHeight="8" refX="9" refY="4" orient="auto" markerUnits="strokeWidth"><path d="M0,0 L10,4 L0,8 z" fill="context-stroke"/></marker></defs>`...)
	dst = append(dst, `<rect width="100%" height="100%" fill="#0f172a"/><g class="viewport">`...)
	for _, edge := range layout.Edges {
		dst = appendSVGEdge(dst, edge, prefix)
	}
	for row, node := range layout.Nodes {
		dst = renderer.appendSVGNode(dst, graph, node, prefix, row == 0)
	}
	dst = append(dst, `</g></svg>`...)
	return dst, nil
}

func appendSVGEdge(dst []byte, edge Edge, prefix string) []byte {
	dst = append(dst, `<g class="edge kind-`...)
	dst = append(dst, edge.Kind.String()...)
	dst = append(dst, `" data-from="`...)
	dst = strconv.AppendUint(dst, uint64(edge.From), 10)
	dst = append(dst, `" data-to="`...)
	dst = strconv.AppendUint(dst, uint64(edge.To), 10)
	dst = append(dst, `" data-kind="`...)
	dst = append(dst, edge.Kind.String()...)
	dst = append(dst, `" data-label="`...)
	dst = appendXMLText(dst, edge.Label)
	dst = append(dst, `"><title>#`...)
	dst = strconv.AppendUint(dst, uint64(edge.From), 10)
	dst = append(dst, ' ')
	dst = append(dst, edge.Kind.String()...)
	dst = append(dst, ` to #`...)
	dst = strconv.AppendUint(dst, uint64(edge.To), 10)
	dst = append(dst, ": "...)
	dst = appendXMLText(dst, edge.Label)
	dst = append(dst, `</title><path class="edge-path" d="`...)
	for row := uint8(0); row < edge.PointCount; row++ {
		if row == 0 {
			dst = append(dst, 'M')
		} else {
			dst = append(dst, " L"...)
		}
		point := edge.Points[row]
		dst = strconv.AppendInt(dst, int64(point.X*svgCellWidth+svgMargin), 10)
		dst = append(dst, ' ')
		dst = strconv.AppendInt(dst, int64(point.Y*svgCellHeight+svgMargin), 10)
	}
	dst = append(dst, `" fill="none" stroke="`...)
	dst = append(dst, graphEdgeColor(edge.Kind)...)
	dst = append(dst, `" stroke-width="2"`...)
	if graphEdgeLineStyle(edge.Kind) == "dashed" {
		dst = append(dst, ` stroke-dasharray="8 6"`...)
	}
	dst = append(dst, ` marker-end="url(#`...)
	dst = appendXMLText(dst, prefix)
	dst = append(dst, `-arrow)"/>`...)
	labelX := edge.Points[1].X + (edge.Points[2].X-edge.Points[1].X)/2
	labelY := edge.Points[1].Y
	labelPixelX := labelX*svgCellWidth + svgMargin
	labelBaselineY := labelY*svgCellHeight + svgMargin - 5
	labelWidth := runewidth.StringWidth(edge.Label)*svgLabelCharWidth + svgLabelPadding*2
	dst = append(dst, `<g class="edge-label" aria-hidden="true"><rect x="`...)
	dst = strconv.AppendInt(dst, int64(labelPixelX-labelWidth/2), 10)
	dst = append(dst, `" y="`...)
	dst = strconv.AppendInt(dst, int64(labelBaselineY-svgLabelHeight+4), 10)
	dst = append(dst, `" width="`...)
	dst = strconv.AppendInt(dst, int64(labelWidth), 10)
	dst = append(dst, `" height="`...)
	dst = strconv.AppendInt(dst, svgLabelHeight, 10)
	dst = append(dst, `" rx="3" fill="#0f172a" fill-opacity="0.96" stroke="`...)
	dst = append(dst, graphEdgeColor(edge.Kind)...)
	dst = append(dst, `" stroke-width="1"/><text x="`...)
	dst = strconv.AppendInt(dst, int64(labelPixelX), 10)
	dst = append(dst, `" y="`...)
	dst = strconv.AppendInt(dst, int64(labelBaselineY), 10)
	dst = append(dst, `" text-anchor="middle" fill="`...)
	dst = append(dst, graphEdgeColor(edge.Kind)...)
	dst = append(dst, `" font-family="ui-monospace,monospace" font-size="12">`...)
	dst = appendXMLText(dst, edge.Label)
	dst = append(dst, `</text></g></g>`...)
	return dst
}

func (renderer *Renderer) appendSVGNode(dst []byte, graph *Graph, node Node, prefix string, tabStop bool) []byte {
	row := node.ID - 1
	kind := graph.Kinds[row]
	x := node.X*svgCellWidth + svgMargin
	y := node.Y*svgCellHeight + svgMargin
	width := node.Width * svgCellWidth
	height := node.Height * svgCellHeight
	renderer.nodeText = appendSVGNodeText(renderer.nodeText[:0], node.ID, graph.Labels[row])
	lineWidth := node.Width - 2
	lineCount := svgWrappedLineCount(renderer.nodeText, lineWidth)
	dst = append(dst, `<g id="`...)
	dst = appendXMLText(dst, prefix)
	dst = append(dst, `-node-`...)
	dst = strconv.AppendUint(dst, uint64(node.ID), 10)
	dst = append(dst, `" class="node kind-`...)
	dst = append(dst, kind.String()...)
	dst = append(dst, `" role="button" tabindex="`...)
	if tabStop {
		dst = append(dst, '0')
	} else {
		dst = append(dst, "-1"...)
	}
	dst = append(dst, `" aria-selected="false" aria-label="Node #`...)
	dst = strconv.AppendUint(dst, uint64(node.ID), 10)
	dst = append(dst, ": "...)
	dst = appendXMLText(dst, graph.Labels[row])
	dst = append(dst, `" data-node-id="`...)
	dst = strconv.AppendUint(dst, uint64(node.ID), 10)
	dst = append(dst, `" data-layer="`...)
	dst = strconv.AppendUint(dst, uint64(node.Layer), 10)
	dst = append(dst, `" data-source-start="`...)
	dst = strconv.AppendUint(dst, uint64(graph.SourceStarts[row]), 10)
	dst = append(dst, `" data-source-end="`...)
	dst = strconv.AppendUint(dst, uint64(graph.SourceEnds[row]), 10)
	dst = append(dst, `" data-detail="`...)
	dst = appendXMLText(dst, graph.Details[row])
	dst = append(dst, `"><title>`...)
	dst = appendXMLText(dst, graph.Labels[row])
	if graph.Details[row] != "" {
		dst = append(dst, " - "...)
		dst = appendXMLText(dst, graph.Details[row])
	}
	dst = append(dst, ` [source `...)
	dst = strconv.AppendUint(dst, uint64(graph.SourceStarts[row]), 10)
	dst = append(dst, ',')
	dst = strconv.AppendUint(dst, uint64(graph.SourceEnds[row]), 10)
	dst = append(dst, `)</title><rect x="`...)
	dst = strconv.AppendInt(dst, int64(x), 10)
	dst = append(dst, `" y="`...)
	dst = strconv.AppendInt(dst, int64(y), 10)
	dst = append(dst, `" width="`...)
	dst = strconv.AppendInt(dst, int64(width), 10)
	dst = append(dst, `" height="`...)
	dst = strconv.AppendInt(dst, int64(height), 10)
	dst = append(dst, `" rx="8" fill="`...)
	dst = append(dst, graphNodeColor(kind)...)
	dst = append(dst, `" fill-opacity="0.22" stroke="`...)
	dst = append(dst, graphNodeColor(kind)...)
	dst = append(dst, `" stroke-width="2"/><text text-anchor="middle" fill="#f8fafc" font-family="ui-monospace,monospace" font-size="14">`...)
	lineY := y + height/2 - (lineCount-1)*svgTextLineHeight/2 + 5
	for start := 0; start < len(renderer.nodeText); {
		end := svgLineEnd(renderer.nodeText, start, lineWidth)
		dst = append(dst, `<tspan x="`...)
		dst = strconv.AppendInt(dst, int64(x+width/2), 10)
		dst = append(dst, `" y="`...)
		dst = strconv.AppendInt(dst, int64(lineY), 10)
		dst = append(dst, `">`...)
		dst = appendXMLBytes(dst, renderer.nodeText[start:end])
		dst = append(dst, `</tspan>`...)
		start = end
		lineY += svgTextLineHeight
	}
	dst = append(dst, `</text></g>`...)
	return dst
}

func appendSVGNodeText(dst []byte, id uint32, label string) []byte {
	dst = append(dst, '#')
	dst = strconv.AppendUint(dst, uint64(id), 10)
	dst = append(dst, ' ')
	dst = append(dst, label...)
	return dst
}

func svgWrappedLineCount(text []byte, width int) int {
	lines := 0
	for start := 0; start < len(text); lines++ {
		start = svgLineEnd(text, start, width)
	}
	return lines
}

func svgLineEnd(text []byte, start, width int) int {
	used := 0
	lastSpace := start
	for index := start; index < len(text); {
		character, size := utf8.DecodeRune(text[index:])
		characterWidth := max(1, runewidth.RuneWidth(character))
		if used+characterWidth > width {
			if lastSpace > start {
				return lastSpace
			}
			if index == start {
				return index + size
			}
			return index
		}
		used += characterWidth
		index += size
		if unicode.IsSpace(character) {
			lastSpace = index
		}
	}
	return len(text)
}

func appendXMLText(dst []byte, value string) []byte {
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '&':
			dst = append(dst, "&amp;"...)
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		case '"':
			dst = append(dst, "&quot;"...)
		case '\'':
			dst = append(dst, "&apos;"...)
		default:
			dst = append(dst, value[index])
		}
	}
	return dst
}

func appendXMLBytes(dst, value []byte) []byte {
	for _, character := range value {
		switch character {
		case '&':
			dst = append(dst, "&amp;"...)
		case '<':
			dst = append(dst, "&lt;"...)
		case '>':
			dst = append(dst, "&gt;"...)
		case '"':
			dst = append(dst, "&quot;"...)
		case '\'':
			dst = append(dst, "&apos;"...)
		default:
			dst = append(dst, character)
		}
	}
	return dst
}
