package graphview

import (
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRenderDOTIsStableEscapedAndSemantic(t *testing.T) {
	graph := sharedTestGraph()
	graph.Labels[1] = `compare "quoted" & <bounded>`
	graph.Details[1] = "line one\nline two"
	graph.EdgeKinds[0] = EdgeEvidence
	graph.EdgeLabels[0] = "requires evidence"
	var renderer Renderer

	first, err := renderer.AppendDOT(nil, &graph)
	if err != nil {
		t.Fatalf("AppendDOT() error = %v", err)
	}
	second, err := renderer.AppendDOT(make([]byte, 0, len(first)), &graph)
	if err != nil {
		t.Fatalf("AppendDOT() repeat error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("DOT output changed between identical renders")
	}
	output := string(first)
	for _, required := range []string{
		"digraph nornrune", `n1 -> n2 [label="requires evidence"`,
		`compare \"quoted\" & <bounded>`, `tooltip="line one\nline two"`,
		`color="#f59e0b"`, `style="dashed"`, `source_start="1"`, `source_end="10"`,
	} {
		if !strings.Contains(output, required) {
			t.Errorf("DOT output lacks %q:\n%s", required, output)
		}
	}
}

func TestRenderSVGProducesParseableLabeledGraph(t *testing.T) {
	graph := sharedTestGraph()
	graph.EdgeKinds[0] = EdgeEvidence
	graph.EdgeLabels[0] = "requires evidence"
	var renderer Renderer
	output, err := renderer.AppendSVG(nil, &graph)
	if err != nil {
		t.Fatalf("AppendSVG() error = %v", err)
	}
	if err := parseXML(output); err != nil {
		t.Fatalf("SVG is not parseable XML: %v\n%s", err, output)
	}
	text := string(output)
	for _, required := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"`, `id="graph-arrow"`,
		`marker-end="url(#graph-arrow)"`, `class="node kind-policy"`,
		`class="edge kind-evidence"`, `stroke="#f59e0b"`,
		`stroke-dasharray="8 6"`, `>requires evidence</text>`,
		`data-source-start="0"`, `data-source-end="0"`, `data-detail="policy detail"`,
		`data-kind="evidence"`, `data-label="requires evidence"`,
		`<g class="edge-label" aria-hidden="true"><rect`, `class="edge-path"`,
		`role="button"`, `tabindex="0"`, `aria-selected="false"`, `data-layer="0"`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("SVG output lacks %q:\n%s", required, text)
		}
	}

	type group struct {
		Class    string `xml:"class,attr"`
		TabIndex string `xml:"tabindex,attr"`
		Label    struct {
			Rect struct {
				Width  int `xml:"width,attr"`
				Height int `xml:"height,attr"`
			} `xml:"rect"`
		} `xml:"g"`
	}
	var document struct {
		Viewport struct {
			Groups []group `xml:"g"`
		} `xml:"g"`
	}
	if err := xml.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode SVG structure: %v", err)
	}
	tabStops := 0
	edgeLabels := 0
	for _, item := range document.Viewport.Groups {
		switch {
		case strings.HasPrefix(item.Class, "node "):
			if item.TabIndex == "0" {
				tabStops++
			} else if item.TabIndex != "-1" {
				t.Errorf("node tabindex = %q, want 0 or -1", item.TabIndex)
			}
		case strings.HasPrefix(item.Class, "edge "):
			edgeLabels++
			if item.Label.Rect.Width <= 0 || item.Label.Rect.Height <= 0 {
				t.Errorf("edge label backplate = %dx%d, want positive size", item.Label.Rect.Width, item.Label.Rect.Height)
			}
		}
	}
	if tabStops != 1 {
		t.Errorf("SVG tab stops = %d, want 1", tabStops)
	}
	if edgeLabels != len(graph.Edges) {
		t.Errorf("SVG edge labels = %d, want %d", edgeLabels, len(graph.Edges))
	}
}

func TestRenderSVGWrapsNodeLabelWithinItsBounds(t *testing.T) {
	graph := sharedTestGraph()
	graph.Labels[0] = strings.Repeat("bounded semantic label ", 8)
	var renderer Renderer

	output, err := renderer.AppendSVG(nil, &graph)
	if err != nil {
		t.Fatalf("AppendSVG() error = %v", err)
	}
	type span struct {
		Text string `xml:",chardata"`
		Y    int    `xml:"y,attr"`
	}
	type group struct {
		ID   string `xml:"id,attr"`
		Rect struct {
			Y      int `xml:"y,attr"`
			Height int `xml:"height,attr"`
		} `xml:"rect"`
		Text struct {
			Spans []span `xml:"tspan"`
		} `xml:"text"`
	}
	var document struct {
		Viewport struct {
			Groups []group `xml:"g"`
		} `xml:"g"`
	}
	if err := xml.Unmarshal(output, &document); err != nil {
		t.Fatalf("decode SVG: %v", err)
	}

	var node *group
	for row := range document.Viewport.Groups {
		if document.Viewport.Groups[row].ID == "graph-node-1" {
			node = &document.Viewport.Groups[row]
			break
		}
	}
	if node == nil {
		t.Fatal("SVG has no graph-node-1")
	}
	if len(node.Text.Spans) < 2 {
		t.Fatalf("node label has %d lines, want wrapped text", len(node.Text.Spans))
	}
	var joined strings.Builder
	for row, line := range node.Text.Spans {
		joined.WriteString(line.Text)
		if len(line.Text) > DefaultLayoutOptions().NodeWidth-2 {
			t.Errorf("line %d width = %d cells, node interior is %d", row+1, len(line.Text), DefaultLayoutOptions().NodeWidth-2)
		}
		if line.Y <= node.Rect.Y || line.Y >= node.Rect.Y+node.Rect.Height {
			t.Errorf("line %d baseline y=%d outside node [%d,%d]", row+1, line.Y, node.Rect.Y, node.Rect.Y+node.Rect.Height)
		}
	}
	if want := "#1 " + graph.Labels[0]; joined.String() != want {
		t.Errorf("wrapped text = %q, want %q", joined.String(), want)
	}
	if node.Rect.Height <= DefaultLayoutOptions().NodeHeight*svgCellHeight {
		t.Errorf("wrapped node height = %d, want more than %d", node.Rect.Height, DefaultLayoutOptions().NodeHeight*svgCellHeight)
	}
}

func TestRenderHTMLIncludesBothInteractiveGraphsWithoutExternalAssets(t *testing.T) {
	astGraph := sharedTestGraph()
	programGraph := sharedTestGraph()
	astGraph.Labels[0] = "AST sentinel"
	programGraph.Labels[0] = "Program sentinel"
	var renderer Renderer

	first, err := renderer.AppendHTML(nil, &astGraph, &programGraph)
	if err != nil {
		t.Fatalf("AppendHTML() error = %v", err)
	}
	second, err := renderer.AppendHTML(make([]byte, 0, len(first)), &astGraph, &programGraph)
	if err != nil {
		t.Fatalf("AppendHTML() repeat error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("HTML output changed between identical renders")
	}
	text := string(first)
	for _, required := range []string{
		"<!doctype html>", `data-view="ast"`, `data-view="program"`,
		`id="ast-graph"`, `id="program-graph"`, "AST sentinel", "Program sentinel",
		`data-action="zoom-in"`, `data-action="zoom-out"`, `data-action="fit"`,
		"wheel", "pointerdown", "pointermove", "source span", "node details",
		`id="relationships"`, `class="keyboard-help"`, `function boxesOverlap(`,
		`function resolveLabels(`, `dataset.colliding`, `function selectNode(`,
		`classList.toggle('related'`, `replaceChildren()`, `edge.dataset.label`,
		`function clearInspector(`, `const changed=active!==next`,
		`if(changed)requestAnimationFrame(()=>resolveLabels(svg()))`,
		`.edge-label[data-colliding=true]{display:none}`,
		`.edge.related .edge-path{stroke-width:4}`,
		`.has-selection .edge:not(.related){opacity:.28}`,
		`.node:focus-visible rect`,
		`function moveNode(`, `case 'ArrowLeft':`, `case 'ArrowRight':`,
		`case 'ArrowUp':`, `case 'ArrowDown':`, `e.key==='Enter'||e.key===' '`,
		`@media(max-width:800px)`, `grid-template-rows:minmax(0,1fr)`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("HTML output lacks %q", required)
		}
	}
	if strings.Contains(text, "<script src=") || strings.Contains(text, "<link ") {
		t.Fatal("HTML output references an external asset")
	}
}

func TestRenderLiveHTMLPollsBoundedStateEndpoint(t *testing.T) {
	graph := sharedTestGraph()
	var renderer Renderer
	output, err := renderer.AppendLiveHTML(nil, &graph, &graph)
	if err != nil {
		t.Fatalf("AppendLiveHTML() error = %v", err)
	}
	text := string(output)
	for _, required := range []string{
		`data-live-state="true"`, `fetch('/state'`, `current-live`,
		`breakpoint-live`, `watch-live`, `id="live-state-label"`,
		`dataset.selectedRow`, `body[data-truth=true]`, `setTimeout(poll,200)`,
	} {
		if !strings.Contains(text, required) {
			t.Errorf("live HTML lacks %q", required)
		}
	}
}

func TestRenderersAllocateZeroAfterPriming(t *testing.T) {
	graph := sharedTestGraph()
	var renderer Renderer
	destination := make([]byte, 0, 1<<20)
	if _, err := renderer.AppendDOT(destination[:0], &graph); err != nil {
		t.Fatal(err)
	}
	if _, err := renderer.AppendSVG(destination[:0], &graph); err != nil {
		t.Fatal(err)
	}
	if _, err := renderer.AppendHTML(destination[:0], &graph, &graph); err != nil {
		t.Fatal(err)
	}
	if _, err := renderer.AppendLiveHTML(destination[:0], &graph, &graph); err != nil {
		t.Fatal(err)
	}

	var renderErr error
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.AppendDOT(destination[:0], &graph)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed DOT = %.2f allocs/run, error=%v", allocations, renderErr)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.AppendSVG(destination[:0], &graph)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed SVG = %.2f allocs/run, error=%v", allocations, renderErr)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.AppendHTML(destination[:0], &graph, &graph)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed HTML = %.2f allocs/run, error=%v", allocations, renderErr)
	}
	if allocations := testing.AllocsPerRun(100, func() {
		_, renderErr = renderer.AppendLiveHTML(destination[:0], &graph, &graph)
	}); allocations != 0 || renderErr != nil {
		t.Fatalf("warmed live HTML = %.2f allocs/run, error=%v", allocations, renderErr)
	}
}

func parseXML(value []byte) error {
	decoder := xml.NewDecoder(strings.NewReader(string(value)))
	for {
		if _, err := decoder.Token(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
