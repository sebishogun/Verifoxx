package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/truth"
)

const (
	defaultWidth  = 100
	defaultHeight = 24
	wideWidth     = 90
	footerHeight  = 3
)

const (
	footerActions = "[s] step  [n] node  [o] over  [c] continue  [space] pause  [r] restart  [u] replay"
	footerTools   = "[b] break  [w] watch  [h] history  [a] ast  [p] program  [x] refs  [q] quit"
)

// View implements tea.Model.
func (model *Model) View() string {
	if model == nil || len(model.data.Requests) == 0 {
		return ""
	}
	if !model.viewDirty {
		return model.viewCache
	}
	width, height := model.width, model.height
	if width <= 0 {
		width = defaultWidth
	}
	if height <= 0 {
		height = defaultHeight
	}
	if width < 1 || height < 1 {
		return model.cacheView("")
	}
	footerRows := footerHeight
	if model.browserStatus != "" {
		footerRows++
	}
	footerRows = min(footerRows, height)
	if height <= footerRows {
		return model.cacheView(model.renderFooter(width, footerRows))
	}
	mainHeight := height - footerRows
	requests := model.requestsView()
	graphTitle, graph, current := model.graphView()
	runtime := model.runtimeView()
	var main string
	if mainHeight < 3 {
		main = renderCompact(width, mainHeight)
	} else if width >= wideWidth {
		available := width - 2
		requestWidth := max(20, available/5)
		graphWidth := max(30, available/2)
		if requestWidth+graphWidth >= available {
			requestWidth = available / 4
			graphWidth = available / 2
		}
		stateWidth := available - requestWidth - graphWidth
		main = lipgloss.JoinHorizontal(lipgloss.Top,
			renderPane("REQUESTS", requests, requestWidth, mainHeight),
			" ",
			renderPane(graphTitle, model.renderGraph(graph, current, graphWidth-4, mainHeight-3), graphWidth, mainHeight),
			" ",
			renderPane("RUNTIME STATE", runtime, stateWidth, mainHeight),
		)
	} else {
		requestHeight := max(1, mainHeight/4)
		remaining := mainHeight - requestHeight
		graphHeight := max(1, remaining/2)
		stateHeight := remaining - graphHeight
		main = lipgloss.JoinVertical(lipgloss.Left,
			renderPane("REQUESTS", requests, width, requestHeight),
			renderPane(graphTitle, model.renderGraph(graph, current, width-4, graphHeight-3), width, graphHeight),
			renderPane("RUNTIME STATE", runtime, width, stateHeight),
		)
	}
	return model.cacheView(main + "\n" + model.renderFooter(width, footerRows))
}

func (model *Model) cacheView(view string) string {
	model.viewCache = view
	model.viewDirty = false
	return view
}

func (model *Model) requestsView() string {
	var output strings.Builder
	for row, request := range model.data.Requests {
		if row == model.selectedRequest {
			output.WriteString("> ")
		} else {
			output.WriteString("  ")
		}
		output.WriteString(request.Name)
		output.WriteByte(' ')
		output.WriteString(request.Decision)
		output.WriteByte('\n')
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func (model *Model) graphView() (string, Graph, uint32) {
	if model.graphMode == graphProgram {
		return "PROGRAM GRAPH", model.data.Program, uint32(model.state.Instruction)
	}
	return "AST GRAPH", model.data.AST, uint32(model.state.Node)
}

func (model *Model) renderGraph(graph Graph, current uint32, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	truth := debug.TruthNeither
	row := uint32(model.selectedRequest)
	positive := rowMaskBit(model.state.Positive, row)
	negative := rowMaskBit(model.state.Negative, row)
	switch {
	case positive && negative:
		truth = debug.TruthBoth
	case positive:
		truth = debug.TruthTrue
	case negative:
		truth = debug.TruthFalse
	}
	output, err := model.graphRenderer.Append(model.graphBuffer[:0], &graph, graphRenderOptions{
		Breakpoints:  model.breakpoints,
		Watches:      model.watches,
		Width:        width,
		Height:       height,
		Current:      current,
		Truth:        truth,
		Program:      model.graphMode == graphProgram,
		ExpandShared: model.expandShared,
		Color:        model.graphColor,
	})
	if err != nil {
		return "graph unavailable"
	}
	model.graphBuffer = output
	return string(output)
}

func (model *Model) runtimeView() string {
	request := model.data.Requests[model.selectedRequest]
	var output strings.Builder
	output.Grow(512)
	if model.disconnected {
		output.WriteString("DEBUG TARGET DISCONNECTED\n")
	}
	output.WriteString("Request: ")
	output.WriteString(request.Name)
	output.WriteString(" (#")
	output.WriteString(strconv.FormatUint(uint64(request.ID), 10))
	output.WriteString(")\n")
	output.WriteString(request.Text)
	output.WriteString("\nStatus: ")
	output.WriteString(statusName(model.state.Status))
	output.WriteString(" / ")
	output.WriteString(stopName(model.state.Stop))
	output.WriteString("\nI: ")
	output.WriteString(strconv.FormatUint(uint64(model.state.Instruction), 10))
	output.WriteString(" -> ")
	output.WriteString(strconv.FormatUint(uint64(model.state.NextInstruction), 10))
	output.WriteString("  Cursor: ")
	output.WriteString(strconv.FormatUint(uint64(model.state.Cursor), 10))
	output.WriteString("\nNode: ")
	output.WriteString(strconv.FormatUint(uint64(model.state.Node), 10))
	output.WriteString("  Source: [")
	output.WriteString(strconv.FormatUint(uint64(model.state.SourceStart), 10))
	output.WriteByte(',')
	output.WriteString(strconv.FormatUint(uint64(model.state.SourceEnd), 10))
	output.WriteString(")")
	row := uint32(model.selectedRequest)
	output.WriteString("\nRow: ")
	output.WriteString(strconv.FormatUint(uint64(row+1), 10))
	output.WriteByte('/')
	output.WriteString(strconv.FormatUint(uint64(model.state.Rows), 10))
	output.WriteString("  Truth: ")
	output.WriteString(rowTruthName(&model.state, row))
	output.WriteString("\nOutcome: #")
	if uint64(row) < uint64(len(model.state.OutcomeIDs)) {
		output.WriteString(strconv.FormatUint(uint64(model.state.OutcomeIDs[row]), 10))
	} else {
		output.WriteByte('0')
	}
	output.WriteString("\nReasons: ")
	appendRowReasons(&output, &model.state, row)
	output.WriteString("\nWorker: ")
	output.WriteString(strconv.FormatUint(uint64(model.state.Worker), 10))
	output.WriteString("  Shard: ")
	output.WriteString(strconv.FormatUint(uint64(model.state.Shard), 10))
	output.WriteString("\nSlab: T=")
	output.WriteString(strconv.FormatUint(model.state.TruthWordOffset, 10))
	output.WriteString(" R=")
	output.WriteString(strconv.FormatUint(model.state.ReasonWordOffset, 10))
	output.WriteString("\nBreakpoints: ")
	output.WriteString(strconv.Itoa(len(model.breakpoints)))
	output.WriteString("\nWatches: ")
	output.WriteString(strconv.Itoa(len(model.watches)))
	if model.status != "" {
		output.WriteString("\nMessage: ")
		output.WriteString(model.status)
	}
	if len(model.historyEntries) != 0 {
		output.WriteString("\nHISTORY")
		for _, entry := range model.historyEntries {
			output.WriteString("\nAt: ")
			output.WriteString(entry.At.UTC().Format("2006-01-02 15:04"))
			output.WriteString("\nPolicy: ")
			output.WriteString(entry.Policy)
			output.WriteString("\nDecision: ")
			output.WriteString(entry.Decision)
		}
	}
	return output.String()
}

func statusName(status debug.Status) string {
	switch status {
	case debug.StatusPaused:
		return "Paused"
	case debug.StatusRunning:
		return "Running"
	case debug.StatusComplete:
		return "Complete"
	case debug.StatusClosed:
		return "Closed"
	default:
		return "Invalid"
	}
}

func stopName(stop debug.StopReason) string {
	switch stop {
	case debug.StopInstruction:
		return "Instruction"
	case debug.StopNode:
		return "Node"
	case debug.StopOver:
		return "Over"
	case debug.StopBreakpoint:
		return "Breakpoint"
	case debug.StopPause:
		return "Pause"
	case debug.StopRestart:
		return "Restart"
	case debug.StopReplay:
		return "Replay"
	case debug.StopComplete:
		return "Complete"
	default:
		return "None"
	}
}

func rowTruthName(state *debug.State, row uint32) string {
	positive := rowMaskBit(state.Positive, row)
	negative := rowMaskBit(state.Negative, row)
	switch {
	case positive && negative:
		return "Both"
	case positive:
		return "True"
	case negative:
		return "False"
	default:
		return "Neither"
	}
}

func rowMaskBit(words []uint64, row uint32) bool {
	word := uint64(row) >> 6
	return word < uint64(len(words)) && words[word]&(uint64(1)<<(row&63)) != 0
}

func appendRowReasons(output *strings.Builder, state *debug.State, row uint32) {
	words := (uint64(state.Rows) + 63) >> 6
	if words == 0 {
		output.WriteString("none")
		return
	}
	names := [...]string{"Missing", "Stale", "Unclear", "Unverifiable", "WrongScope", "WrongSubject", "WrongTiming", "Invalid", "Conflict"}
	found := false
	for reason := uint64(0); reason < truth.ReasonCount; reason++ {
		word := reason*words + (uint64(row) >> 6)
		if word >= uint64(len(state.Reasons)) || state.Reasons[word]&(uint64(1)<<(row&63)) == 0 {
			continue
		}
		if found {
			output.WriteByte(',')
		}
		output.WriteString(names[reason])
		found = true
	}
	if !found {
		output.WriteString("none")
	}
}

func renderPane(title, body string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	frameWidth := paneStyle.GetHorizontalFrameSize()
	frameHeight := paneStyle.GetVerticalFrameSize()
	if width <= frameWidth || height <= frameHeight {
		return fitLine(title, width)
	}
	contentWidth := width - frameWidth
	contentHeight := height - frameHeight
	content := title
	if contentHeight > 1 && body != "" {
		content += "\n" + body
	}
	content = fitText(content, contentWidth, contentHeight)
	return paneStyle.Copy().Width(width - 2).Height(height - 2).Render(content)
}

func renderFooter(width, height int) string {
	if height <= 0 {
		return ""
	}
	if height == 1 {
		return fitLine(footerActions, width)
	}
	if height == 2 {
		return strings.Repeat("-", width) + "\n" + fitLine(footerActions, width)
	}
	return strings.Repeat("-", width) + "\n" + fitLine(footerActions, width) + "\n" + fitLine(footerTools, width)
}

func (model *Model) renderFooter(width, height int) string {
	if model.browserStatus == "" {
		return renderFooter(width, height)
	}
	if height <= 1 {
		return fitLine(model.browserStatus, width)
	}
	return fitLine(model.browserStatus, width) + "\n" + renderFooter(width, height-1)
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = ansi.Truncate(value, width, "")
	return value + strings.Repeat(" ", width-ansi.StringWidth(value))
}

func fitText(value string, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	lines := strings.Split(value, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for row, line := range lines {
		lines[row] = ansi.Truncate(line, width, "")
	}
	return strings.Join(lines, "\n")
}

func renderCompact(width, height int) string {
	if height <= 0 {
		return ""
	}
	var output strings.Builder
	output.WriteString(fitLine("VERIFOXX DEBUGGER", width))
	for range height - 1 {
		output.WriteByte('\n')
		output.WriteString(strings.Repeat(" ", width))
	}
	return output.String()
}
