package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/sebishogun/nornrune/internal/debug"
	"github.com/sebishogun/nornrune/internal/truth"
)

const (
	defaultWidth  = 100
	defaultHeight = 24
	wideWidth     = 90
	headerHeight  = 1
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
	footerRows = min(footerRows, height)
	if height <= footerRows {
		return model.cacheView(model.renderFooter(width, footerRows))
	}
	availableHeight := height - footerRows - headerHeight
	historyHeight := 0
	if model.historyVisible && availableHeight >= 9 {
		historyHeight = min(12, max(6, availableHeight/3))
	}
	mainHeight := availableHeight - historyHeight
	if mainHeight <= 0 {
		return model.cacheView(model.renderFooter(width, height))
	}
	requests := model.requestsView()
	graphTitle, graph, current := model.graphView()
	runtime := model.runtimeView()
	var main string
	if mainHeight < 3 {
		main = renderCompact(width, mainHeight)
	} else if width >= wideWidth {
		available := width - 2
		requestWidth := min(24, max(18, width/6))
		stateWidth := min(38, max(32, width/4))
		graphWidth := available - requestWidth - stateWidth
		if graphWidth < 30 {
			requestWidth = max(16, available/5)
			stateWidth = max(24, available/3)
			graphWidth = available - requestWidth - stateWidth
		}
		main = lipgloss.JoinHorizontal(lipgloss.Top,
			renderPane("REQUESTS", requests, requestWidth, mainHeight),
			" ",
			renderPane(graphTitle, model.renderGraph(graph, current, graphWidth-4, mainHeight-3), graphWidth, mainHeight),
			" ",
			model.renderStateColumn(runtime, stateWidth, mainHeight),
		)
	} else {
		requestHeight := max(1, mainHeight/4)
		remaining := mainHeight - requestHeight
		graphHeight := max(1, remaining/2)
		stateHeight := remaining - graphHeight
		main = lipgloss.JoinVertical(lipgloss.Left,
			renderPane("REQUESTS", requests, width, requestHeight),
			renderPane(graphTitle, model.renderGraph(graph, current, width-4, graphHeight-3), width, graphHeight),
			renderPane("RUNTIME STATE / BREAKPOINTS / WATCHES", runtime+"\n"+model.bindingsView(), width, stateHeight),
		)
	}
	view := model.renderHeader(width) + "\n" + main
	if historyHeight != 0 {
		view += "\n" + model.renderHistory(width, historyHeight)
	}
	return model.cacheView(view + "\n" + model.renderFooter(width, footerRows))
}

func (model *Model) renderHeader(width int) string {
	mode := "AST"
	if model.graphMode == graphProgram {
		mode = "PROGRAM"
	}
	request := model.data.Requests[model.selectedRequest]
	header := "NORNRUNE SEMANTIC DEBUGGER  |  " + mode + "  |  " + request.Name + " " + request.Decision +
		"  |  " + statusName(model.state.Status) + " / " + stopName(model.state.Stop)
	return fitLine(header, width)
}

func (model *Model) renderStateColumn(runtime string, width, height int) string {
	bindingsHeight := min(7, max(5, height/4))
	runtimeHeight := height - bindingsHeight
	if runtimeHeight < 3 {
		return renderPane("RUNTIME STATE / BREAKPOINTS / WATCHES", runtime+"\n"+model.bindingsView(), width, height)
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		renderPane("RUNTIME STATE", runtime, width, runtimeHeight),
		renderPane("BREAKPOINTS / WATCHES", model.bindingsView(), width, bindingsHeight),
	)
}

func (model *Model) bindingsView() string {
	var output strings.Builder
	output.WriteString("Breakpoints: ")
	output.WriteString(strconv.Itoa(len(model.breakpoints)))
	for _, binding := range model.breakpoints {
		output.WriteString("  #")
		output.WriteString(strconv.FormatUint(uint64(binding.node), 10))
	}
	output.WriteString("\nWatches: ")
	output.WriteString(strconv.Itoa(len(model.watches)))
	for _, binding := range model.watches {
		output.WriteString("  #")
		output.WriteString(strconv.FormatUint(uint64(binding.instruction), 10))
		output.WriteString(" row ")
		output.WriteString(strconv.FormatUint(uint64(binding.row+1), 10))
	}
	return output.String()
}

func (model *Model) renderHistory(width, height int) string {
	title := "HISTORY  [SESSION]  PERSISTED"
	if model.historyTab == historyPersisted {
		title = "HISTORY  SESSION  [PERSISTED]"
	}
	var output strings.Builder
	rows := max(1, height-3)
	if model.historyTab == historySession {
		if len(model.sessionHistory) == 0 {
			output.WriteString("No debugger stops recorded.")
		} else {
			start := max(0, len(model.sessionHistory)-rows)
			for index := start; index < len(model.sessionHistory); index++ {
				if index != start {
					output.WriteByte('\n')
				}
				entry := model.sessionHistory[index]
				if model.historyFocus && index == model.historySelection {
					output.WriteString("> ")
				} else {
					output.WriteString("  ")
				}
				output.WriteString(time.UnixMilli(entry.atUnixMilli).UTC().Format("15:04:05"))
				output.WriteByte(' ')
				output.WriteString(sessionActionName(entry.action))
				output.WriteString(" / ")
				output.WriteString(stopName(entry.stop))
				output.WriteString("  I#")
				output.WriteString(strconv.FormatUint(uint64(entry.instruction), 10))
				output.WriteString(" N#")
				output.WriteString(strconv.FormatUint(uint64(entry.node), 10))
				output.WriteString(" R")
				output.WriteString(strconv.FormatUint(uint64(entry.row+1), 10))
				output.WriteByte(' ')
				output.WriteString(truthStateName(entry.truth))
				output.WriteString(" O#")
				output.WriteString(strconv.FormatUint(uint64(entry.outcome), 10))
			}
		}
	} else {
		switch {
		case model.historyPending:
			output.WriteString("Loading persisted history...")
		case model.historyError != "":
			output.WriteString("Persisted history error: ")
			output.WriteString(model.historyError)
		case model.history == nil:
			output.WriteString("Persisted history is not configured.")
		case len(model.historyEntries) == 0:
			output.WriteString("No persisted decisions.")
		default:
			start := max(0, len(model.historyEntries)-rows)
			for index := start; index < len(model.historyEntries); index++ {
				if index != start {
					output.WriteByte('\n')
				}
				entry := model.historyEntries[index]
				if model.historyFocus && index == model.historySelection {
					output.WriteString("> ")
				} else {
					output.WriteString("  ")
				}
				output.WriteString(entry.At.UTC().Format("2006-01-02 15:04"))
				output.WriteByte(' ')
				output.WriteString(entry.Policy)
				if entry.Version != "" {
					output.WriteByte('@')
					output.WriteString(entry.Version)
				}
				output.WriteByte(' ')
				output.WriteString(entry.Decision)
			}
		}
	}
	return renderPane(title, output.String(), width, height)
}

func sessionActionName(action semanticAction) string {
	switch action {
	case actionSnapshot:
		return "Snapshot"
	case actionStepInstruction:
		return "Step"
	case actionStepNode:
		return "Node"
	case actionStepOver:
		return "Over"
	case actionContinue:
		return "Continue"
	case actionPause:
		return "Pause"
	case actionRestart:
		return "Restart"
	case actionReplay:
		return "Replay"
	default:
		return "Unknown"
	}
}

func truthStateName(state debug.TruthState) string {
	switch state {
	case debug.TruthTrue:
		return "True"
	case debug.TruthFalse:
		return "False"
	case debug.TruthBoth:
		return "Both"
	default:
		return "Neither"
	}
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
		return "PROGRAM GRAPH  AST  [PROGRAM]", model.data.Program, uint32(model.state.Instruction)
	}
	return "AST GRAPH  [AST]  PROGRAM", model.data.AST, uint32(model.state.Node)
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
	return truthStateName(stateRowTruth(state, row))
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

func (model *Model) renderFooter(width, height int) string {
	if height <= 0 {
		return ""
	}
	status := model.status
	if status == "" {
		status = statusName(model.state.Status) + " / " + stopName(model.state.Stop)
	}
	if model.browserStatus != "" {
		status += "  |  " + model.browserStatus
	}
	footer := fitLine("STATUS  "+status, width)
	if height > 1 {
		footer += "\n" + fitLine(footerActions, width)
	}
	if height > 2 {
		footer += "\n" + fitLine(footerTools, width)
	}
	return footer
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
	output.WriteString(fitLine("NORNRUNE DEBUGGER", width))
	for range height - 1 {
		output.WriteByte('\n')
		output.WriteString(strings.Repeat(" ", width))
	}
	return output.String()
}
