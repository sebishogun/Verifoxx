package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/graphview"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func TestModelAcceptsEdgeAwareSemanticGraphs(t *testing.T) {
	graph := Graph{
		Labels:       []string{"policy", "requirement"},
		Details:      []string{"policy source", "requirement R1"},
		Kinds:        []graphview.NodeKind{graphview.NodePolicy, graphview.NodeRequirement},
		SourceStarts: []uint32{0, 1},
		SourceEnds:   []uint32{2, 2},
		EdgeStarts:   []uint32{0, 1},
		EdgeCounts:   []uint16{1, 0},
		Edges:        []uint32{2},
		EdgeKinds:    []graphview.EdgeKind{graphview.EdgeContains},
		EdgeLabels:   []string{"contains"},
		Roots:        []uint32{1},
		SourceLength: 2,
	}
	data := Data{
		Requests: []RequestItem{{ID: 1, Name: "R1", Decision: "Approve", Text: "request"}},
		AST:      graph,
		Program:  graph,
	}
	if _, err := NewModel(&stubTarget{}, nil, data); err != nil {
		t.Fatalf("NewModel(edge-aware graph) error = %v", err)
	}
}

func TestModelSelectsRequestAndTogglesGraph(t *testing.T) {
	t.Parallel()

	model := newTestModel(t, &stubTarget{}, nil)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.selectedRequest != 1 {
		t.Fatalf("selected request = %d, want 1", model.selectedRequest)
	}
	if got := model.View(); !containsAll(got, "> R2 Reject", "AST GRAPH", "all") {
		t.Fatalf("selected AST view:\n%s", got)
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if model.graphMode != graphProgram {
		t.Fatalf("graph mode = %v, want program", model.graphMode)
	}
	if got := model.View(); !containsAll(got, "PROGRAM GRAPH", "and", "field:eq") {
		t.Fatalf("program view:\n%s", got)
	}
	if model.status != "Program graph active" {
		t.Fatalf("program mode status = %q", model.status)
	}
	model = updateModel(t, model, runeKey('p'))
	if model.status != "Program graph active" {
		t.Fatalf("repeated program mode status = %q", model.status)
	}
	model = updateModel(t, model, runeKey('a'))
	if model.graphMode != graphAST || model.status != "AST graph active" {
		t.Fatalf("AST mode = %v status=%q", model.graphMode, model.status)
	}
}

func TestModelLoadsSnapshotAndIssuesSemanticCommands(t *testing.T) {
	t.Parallel()

	target := &stubTarget{state: debug.State{
		Instruction: 3,
		Node:        2,
		Rows:        2,
		Status:      debug.StatusPaused,
		Stop:        debug.StopInstruction,
	}}
	model := newTestModel(t, target, nil)
	model = runModelCommand(t, model, model.Init())
	if model.state.Instruction != 3 || model.state.Node != 2 {
		t.Fatalf("snapshot state = %+v", model.state)
	}

	tests := []struct {
		name string
		key  tea.KeyMsg
		want string
	}{
		{name: "instruction", key: runeKey('s'), want: "step-instruction"},
		{name: "node", key: runeKey('n'), want: "step-node"},
		{name: "over", key: runeKey('o'), want: "step-over"},
		{name: "continue", key: runeKey('c'), want: "continue"},
		{name: "pause", key: tea.KeyMsg{Type: tea.KeySpace}, want: "pause"},
		{name: "restart", key: runeKey('r'), want: "restart"},
		{name: "replay", key: runeKey('u'), want: "replay"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target.operation = ""
			var command tea.Cmd
			model, command = updateModelCommand(t, model, test.key)
			model = runModelCommand(t, model, command)
			if target.operation != test.want {
				t.Fatalf("operation = %q, want %q", target.operation, test.want)
			}
		})
	}
	if target.replayed != 2 {
		t.Fatalf("Replay instruction = %d, want 2", target.replayed)
	}
}

func TestModelTogglesNodeBreakpointAndMaskWatch(t *testing.T) {
	t.Parallel()

	target := &stubTarget{state: debug.State{Instruction: 3, Node: 2, Rows: 2, Status: debug.StatusPaused}}
	model := newTestModel(t, target, nil)
	model.state = target.state
	model.status = "stale error"

	var command tea.Cmd
	model, command = updateModelCommand(t, model, runeKey('b'))
	model = runModelCommand(t, model, command)
	if target.breakpoint.Kind != debug.BreakNode || target.breakpoint.Node != 2 || len(model.breakpoints) != 1 {
		t.Fatalf("breakpoint = %+v, bindings = %+v", target.breakpoint, model.breakpoints)
	}
	if model.status != "" {
		t.Fatalf("status after breakpoint = %q", model.status)
	}
	model, command = updateModelCommand(t, model, runeKey('b'))
	model = runModelCommand(t, model, command)
	if target.removed != 1 || len(model.breakpoints) != 0 {
		t.Fatalf("removed breakpoint = %d, bindings = %+v", target.removed, model.breakpoints)
	}

	model.status = "stale error"
	model, command = updateModelCommand(t, model, runeKey('w'))
	model = runModelCommand(t, model, command)
	if target.watch.Kind != debug.WatchMask || target.watch.Instruction != 3 || target.watch.Row != 0 || len(model.watches) != 1 {
		t.Fatalf("watch = %+v, bindings = %+v", target.watch, model.watches)
	}
	if model.status != "" {
		t.Fatalf("status after watch = %q", model.status)
	}
	model, command = updateModelCommand(t, model, runeKey('w'))
	model = runModelCommand(t, model, command)
	if target.removedWatch != 1 || len(model.watches) != 0 {
		t.Fatalf("removed watch = %d, bindings = %+v", target.removedWatch, model.watches)
	}
}

func TestModelLoadsSelectedRequestHistory(t *testing.T) {
	t.Parallel()

	history := &stubHistory{entries: []HistoryEntry{
		{At: time.Date(2026, time.August, 24, 12, 30, 0, 0, time.UTC), Policy: "policy@2.1.0", Decision: "Revise"},
	}}
	model := newTestModel(t, &stubTarget{}, history)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	updated, command := model.Update(runeKey('h'))
	model = updated.(*Model)
	if command != nil || !model.historyVisible {
		t.Fatalf("opening session history = command %v visible %v", command, model.historyVisible)
	}
	model, command = updateModelCommand(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model = runModelCommand(t, model, command)
	if history.request.ID != 2 || history.request.Name != "R2" || len(model.historyEntries) != 1 {
		t.Fatalf("history request = %+v, entries = %+v", history.request, model.historyEntries)
	}
	if got := model.View(); !containsAll(got, "HISTORY", "[PERSISTED]", "2026-08-24 12:30", "policy@2.1.0", "Revise") {
		t.Fatalf("history view:\n%s", got)
	}
}

func TestModelSessionHistoryTogglesAndRetainsBoundedStops(t *testing.T) {
	model := newTestModel(t, &stubTarget{}, nil)
	updated, command := model.Update(runeKey('h'))
	model = updated.(*Model)
	if command != nil || !model.historyVisible || model.historyTab != historySession {
		t.Fatalf("session history open = visible %v tab %v command %v", model.historyVisible, model.historyTab, command)
	}

	for sequence := uint64(1); sequence <= maxSessionHistoryEntries+1; sequence++ {
		state := debug.State{
			Positive:    []uint64{1},
			OutcomeIDs:  []schema.OutcomeID{2},
			Instruction: schema.InstructionID(sequence),
			Node:        schema.NodeID(sequence),
			Rows:        1,
			Status:      debug.StatusPaused,
			Stop:        debug.StopInstruction,
		}
		updated, command = model.Update(stateMessage{state: state, sequence: sequence, action: actionStepInstruction})
		model = updated.(*Model)
		if command != nil {
			t.Fatalf("state %d returned command", sequence)
		}
	}
	if len(model.sessionHistory) != maxSessionHistoryEntries {
		t.Fatalf("session history entries = %d, want %d", len(model.sessionHistory), maxSessionHistoryEntries)
	}
	if first, last := model.sessionHistory[0], model.sessionHistory[len(model.sessionHistory)-1]; first.instruction != 2 || last.instruction != maxSessionHistoryEntries+1 || last.truth != debug.TruthTrue ||
		last.outcome != 2 || last.atUnixMilli == 0 {
		t.Fatalf("bounded session history first=%+v last=%+v", first, last)
	}
	before := len(model.sessionHistory)
	updated, _ = model.Update(stateMessage{err: errors.New("step failed"), sequence: maxSessionHistoryEntries + 2, action: actionStepInstruction})
	model = updated.(*Model)
	updated, _ = model.Update(stateMessage{
		state:    debug.State{Instruction: 1, Status: debug.StatusPaused, Stop: debug.StopInstruction},
		sequence: 1,
		action:   actionStepInstruction,
	})
	model = updated.(*Model)
	if len(model.sessionHistory) != before {
		t.Fatalf("failed or stale state changed session history length to %d", len(model.sessionHistory))
	}
	if view := model.View(); !containsAll(view, "HISTORY  [SESSION]", "Instruction", "#65") {
		t.Fatalf("session history dock:\n%s", view)
	}
	model.historySelection = len(model.sessionHistory) - 1
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyUp})
	if model.historySelection != len(model.sessionHistory)-2 || model.selectedRequest != 0 {
		t.Fatalf("history up selected history=%d request=%d", model.historySelection, model.selectedRequest)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if model.selectedRequest != 1 {
		t.Fatalf("request focus after escape selected request=%d", model.selectedRequest)
	}

	updated, command = model.Update(runeKey('h'))
	model = updated.(*Model)
	if command != nil || model.historyVisible {
		t.Fatalf("session history close = visible %v command %v", model.historyVisible, command)
	}
}

func TestModelPersistedHistoryFailureIsVisibleAndNonFatal(t *testing.T) {
	model := newTestModel(t, &stubTarget{}, &stubHistory{err: errors.New("persisted history unavailable")})
	model = updateModel(t, model, runeKey('h'))
	var command tea.Cmd
	model, command = updateModelCommand(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model = runModelCommand(t, model, command)
	if model.disconnected || model.historyPending {
		t.Fatalf("history failure disconnected=%v pending=%v", model.disconnected, model.historyPending)
	}
	if view := model.View(); !containsAll(view, "[PERSISTED]", "Persisted history error: persisted history unavailable") {
		t.Fatalf("persisted history failure is not visible in its pane:\n%s", view)
	}
	model = updateModel(t, model, runeKey('p'))
	if model.graphMode != graphProgram {
		t.Fatal("persisted history failure prevented graph interaction")
	}
}

func TestModelPersistedHistoryReportsUnavailableConfiguration(t *testing.T) {
	model := newTestModel(t, &stubTarget{}, nil)
	model = updateModel(t, model, runeKey('h'))
	updated, command := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*Model)
	if command != nil {
		t.Fatal("unconfigured persisted history returned a command")
	}
	if view := model.View(); !strings.Contains(view, "Persisted history is not configured.") {
		t.Fatalf("unconfigured persisted history pane:\n%s", view)
	}
}

func TestModelSessionHistoryRecordsSuccessfulStopActions(t *testing.T) {
	model := newTestModel(t, &stubTarget{}, nil)
	tests := []struct {
		action semanticAction
		stop   debug.StopReason
	}{
		{actionSnapshot, debug.StopNone},
		{actionStepInstruction, debug.StopInstruction},
		{actionStepNode, debug.StopNode},
		{actionStepOver, debug.StopOver},
		{actionContinue, debug.StopBreakpoint},
		{actionPause, debug.StopPause},
		{actionRestart, debug.StopRestart},
		{actionReplay, debug.StopReplay},
		{actionContinue, debug.StopComplete},
	}
	for index, test := range tests {
		sequence := uint64(index + 1)
		updated, command := model.Update(stateMessage{
			state: debug.State{
				Instruction: schema.InstructionID(sequence),
				Status:      debug.StatusPaused,
				Stop:        test.stop,
			},
			sequence: sequence,
			action:   test.action,
		})
		model = updated.(*Model)
		if command != nil {
			t.Fatalf("history action %v returned follow-up command", test.action)
		}
	}
	if len(model.sessionHistory) != len(tests) {
		t.Fatalf("session history entries = %d, want %d", len(model.sessionHistory), len(tests))
	}
	for index, test := range tests {
		if entry := model.sessionHistory[index]; entry.action != test.action || entry.stop != test.stop {
			t.Fatalf("session history[%d] = %+v, want action=%v stop=%v", index, entry, test.action, test.stop)
		}
	}
}

func TestModelShowsDisconnectedTargetAndSuppressesCommands(t *testing.T) {
	t.Parallel()

	target := &stubTarget{err: fmt.Errorf("peer closed: %w", debug.ErrTransportClosed)}
	model := newTestModel(t, target, nil)
	model = runModelCommand(t, model, model.Init())
	if !model.disconnected {
		t.Fatal("model is connected after transport closure")
	}
	if got := model.View(); !containsAll(got, "DEBUG TARGET DISCONNECTED", "peer closed") {
		t.Fatalf("disconnected view:\n%s", got)
	}
	_, command := model.Update(runeKey('s'))
	if command != nil || target.steps != 0 {
		t.Fatalf("disconnected step command = %v, calls = %d", command, target.steps)
	}
}

func TestModelRendersOneSharedDAGNodeWithConvergingEdges(t *testing.T) {
	t.Parallel()

	model := newTestModel(t, &stubTarget{}, nil)
	model.data.AST = testGraph(
		[]string{"root", "left", "right", "shared"},
		[]uint32{0, 2, 3, 4}, []uint16{2, 1, 1, 0}, []uint32{2, 3, 4, 4},
		[]graphview.NodeKind{graphview.NodeAll, graphview.NodeAll, graphview.NodeAll, graphview.NodeCompare},
		graphview.EdgeArgument,
	)
	model.state.Node = 4
	view := model.View()
	if !containsAll(view, "▶? #4 shared", "arg 1", "arg 2") || strings.Count(view, "#4 shared") != 1 || strings.Contains(view, "[ref]") {
		t.Fatalf("shared DAG view:\n%s", view)
	}

	model = updateModel(t, model, runeKey('x'))
	view = model.View()
	if strings.Contains(view, "[ref]") || strings.Count(view, "#4 shared") != 1 {
		t.Fatalf("shared DAG changed after fallback toggle:\n%s", view)
	}
}

func TestModelResizeRendersBoundedResponsivePanes(t *testing.T) {
	t.Parallel()

	model := newTestModel(t, &stubTarget{}, nil)
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 120, Height: 24})
	wide := model.View()
	if got := lipgloss.Width(wide); got != 120 {
		t.Fatalf("wide view width = %d, want 120:\n%s", got, wide)
	}
	if got := lipgloss.Height(wide); got != 24 {
		t.Fatalf("wide view height = %d, want 24:\n%s", got, wide)
	}
	if !containsAll(wide, "REQUESTS", "AST GRAPH", "RUNTIME STATE", "[s] step") {
		t.Fatalf("wide panes:\n%s", wide)
	}
	if strings.Contains(wide, "\x1b[") {
		t.Fatalf("wide view contains terminal color escapes: %q", wide)
	}

	model = updateModel(t, model, tea.WindowSizeMsg{Width: 64, Height: 30})
	narrow := model.View()
	if got := lipgloss.Width(narrow); got != 64 {
		t.Fatalf("narrow view width = %d, want 64:\n%s", got, narrow)
	}
	if got := lipgloss.Height(narrow); got != 30 {
		t.Fatalf("narrow view height = %d, want 30:\n%s", got, narrow)
	}
	if !containsAll(narrow, "REQUESTS", "AST GRAPH", "RUNTIME STATE") {
		t.Fatalf("narrow panes:\n%s", narrow)
	}
}

func TestModelRendersFullScreenDAPDashboardAndGraphTabs(t *testing.T) {
	model := newTestModel(t, &stubTarget{}, nil)
	model.state = debug.State{
		Instruction: 2,
		Node:        2,
		Rows:        2,
		Status:      debug.StatusPaused,
		Stop:        debug.StopInstruction,
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 40}, {Width: 160, Height: 50}} {
		model = updateModel(t, model, size)
		view := model.View()
		if width, height := lipgloss.Width(view), lipgloss.Height(view); width != size.Width || height != size.Height {
			t.Fatalf("dashboard = %dx%d, want %dx%d:\n%s", width, height, size.Width, size.Height, view)
		}
		lines := strings.Split(view, "\n")
		if !strings.HasPrefix(lines[len(lines)-3], "STATUS  ") ||
			!strings.Contains(lines[len(lines)-2], "[s] step") ||
			!strings.Contains(lines[len(lines)-1], "[h] history") {
			t.Fatalf("dashboard lacks one status and two key rows:\n%s", view)
		}
	}
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 140, Height: 42})

	view := model.View()
	if width, height := lipgloss.Width(view), lipgloss.Height(view); width != 140 || height != 42 {
		t.Fatalf("dashboard = %dx%d, want 140x42:\n%s", width, height, view)
	}
	if !containsAll(view,
		"VERIFOXX SEMANTIC DEBUGGER", "REQUESTS", "AST GRAPH", "[AST]", "PROGRAM",
		"RUNTIME STATE", "BREAKPOINTS / WATCHES", "Paused / Instruction",
	) {
		t.Fatalf("dashboard lacks DAP panes or active AST tab:\n%s", view)
	}

	model = updateModel(t, model, runeKey('p'))
	view = model.View()
	if !containsAll(view, "PROGRAM GRAPH", "AST", "[PROGRAM]", "Program graph active") {
		t.Fatalf("program tab did not become visibly active:\n%s", view)
	}
	model = updateModel(t, model, runeKey('p'))
	view = model.View()
	lines := strings.Split(view, "\n")
	if !strings.HasPrefix(lines[len(lines)-3], "STATUS  Program graph active") {
		t.Fatalf("repeated program key was not observable:\n%s", view)
	}
	model = updateModel(t, model, runeKey('a'))
	if view = model.View(); !containsAll(view, "AST GRAPH", "[AST]", "PROGRAM", "AST graph active") {
		t.Fatalf("AST tab did not become visibly active:\n%s", view)
	}
}

func TestModelCachesUnchangedViewWithoutAllocating(t *testing.T) {
	model := newTestModel(t, &stubTarget{}, nil)
	first := model.View()
	var repeated string
	if allocations := testing.AllocsPerRun(100, func() { repeated = model.View() }); allocations != 0 {
		t.Fatalf("unchanged View = %.2f allocs/run, want 0", allocations)
	}
	if repeated != first {
		t.Fatal("unchanged View returned a different frame")
	}

	model = updateModel(t, model, runeKey('p'))
	programView := model.View()
	if programView == first || !strings.Contains(programView, "PROGRAM GRAPH") {
		t.Fatal("graph mode change did not invalidate the cached frame")
	}
	if allocations := testing.AllocsPerRun(100, func() { repeated = model.View() }); allocations != 0 {
		t.Fatalf("unchanged Program View = %.2f allocs/run, want 0", allocations)
	}
}

func TestModelRendersSelectedRowSemanticState(t *testing.T) {
	t.Parallel()

	model := newTestModel(t, &stubTarget{}, nil)
	model.selectedRequest = 1
	model.state = debug.State{
		Active:           []uint64{0b11},
		Positive:         []uint64{0b01},
		Negative:         []uint64{0b10},
		Reasons:          make([]uint64, truth.ReasonCount),
		OutcomeIDs:       []schema.OutcomeID{1, 2},
		Instruction:      3,
		NextInstruction:  4,
		Breakpoint:       7,
		Node:             2,
		SourceStart:      10,
		SourceEnd:        24,
		TruthWordOffset:  64,
		ReasonWordOffset: 128,
		Cursor:           3,
		Rows:             2,
		Worker:           1,
		Shard:            3,
		Status:           debug.StatusPaused,
		Stop:             debug.StopBreakpoint,
	}
	model.state.Reasons[truth.ReasonMissing-1] = 0b10
	view := model.View()
	if !containsAll(view,
		"Status: Paused / Breakpoint",
		"I: 3 -> 4",
		"Node: 2  Source: [10,24)",
		"Row: 2/2  Truth: False",
		"Outcome: #2",
		"Worker: 1  Shard: 3",
		"Slab: T=64 R=128",
	) {
		t.Fatalf("semantic runtime view:\n%s", view)
	}
}

func TestModelPauseCancelsPendingContinueBeforeIssuingPause(t *testing.T) {
	t.Parallel()

	target := &blockingTarget{stubTarget: stubTarget{state: debug.State{Status: debug.StatusPaused, Stop: debug.StopPause}}}
	model := newTestModel(t, target, nil)
	var continueCommand tea.Cmd
	model, continueCommand = updateModelCommand(t, model, runeKey('c'))
	updated, immediatePause := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)
	if immediatePause != nil {
		t.Fatal("Pause command issued before Continue cancellation completed")
	}
	continueResult := continueCommand()
	model, pauseCommand := updateModelCommand(t, model, continueResult)
	model = runModelCommand(t, model, pauseCommand)
	if target.operation != "pause" || model.state.Stop != debug.StopPause || model.status != "" {
		t.Fatalf("pause operation = %q, state = %+v, status = %q", target.operation, model.state, model.status)
	}
}

func TestModelContinueContextHasNoDeadline(t *testing.T) {
	t.Parallel()

	target := &deadlineTarget{}
	model := newTestModel(t, target, nil)
	var command tea.Cmd
	model, command = updateModelCommand(t, model, runeKey('c'))
	model = runModelCommand(t, model, command)
	if target.hadDeadline {
		t.Fatal("Continue context has a deadline")
	}
}

func TestModelPausePreservesCompletedContinueStop(t *testing.T) {
	t.Parallel()

	target := &stubTarget{state: debug.State{
		Instruction: 4,
		Node:        2,
		Status:      debug.StatusPaused,
		Stop:        debug.StopBreakpoint,
		Breakpoint:  9,
	}}
	model := newTestModel(t, target, nil)
	var continueCommand tea.Cmd
	model, continueCommand = updateModelCommand(t, model, runeKey('c'))
	updated, immediatePause := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)
	if immediatePause != nil {
		t.Fatal("Pause command issued before Continue completed")
	}
	updated, followUp := model.Update(continueCommand())
	model = updated.(*Model)
	if followUp != nil {
		t.Fatal("Pause command replaced a completed Continue stop")
	}
	if model.state.Stop != debug.StopBreakpoint || model.state.Breakpoint != 9 || model.state.Instruction != 4 {
		t.Fatalf("completed Continue state = %+v", model.state)
	}
}

func TestModelPauseDoesNotMaskContinueFailure(t *testing.T) {
	t.Parallel()

	target := &failedContinueTarget{err: errors.New("continue failed")}
	model := newTestModel(t, target, nil)
	var continueCommand tea.Cmd
	model, continueCommand = updateModelCommand(t, model, runeKey('c'))
	updated, immediatePause := model.Update(tea.KeyMsg{Type: tea.KeySpace})
	model = updated.(*Model)
	if immediatePause != nil {
		t.Fatal("Pause command issued before Continue failed")
	}
	updated, followUp := model.Update(continueCommand())
	model = updated.(*Model)
	if followUp != nil || model.status != "continue failed" {
		t.Fatalf("follow-up = %v, status = %q", followUp, model.status)
	}
}

func TestModelQueuesHistoryForNewSelection(t *testing.T) {
	t.Parallel()

	history := &stubHistory{entries: []HistoryEntry{
		{At: time.Date(2026, time.August, 24, 12, 30, 0, 0, time.UTC), Policy: "policy@2.1.0", Decision: "Revise"},
	}}
	model := newTestModel(t, &stubTarget{}, history)
	var first tea.Cmd
	model = updateModel(t, model, runeKey('h'))
	model, first = updateModelCommand(t, model, tea.KeyMsg{Type: tea.KeyTab})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyTab})
	updated, immediate := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(*Model)
	if immediate != nil {
		t.Fatal("second history load started concurrently")
	}
	model, queued := updateModelCommand(t, model, first())
	model = runModelCommand(t, model, queued)
	if history.request.ID != 2 || history.request.Name != "R2" || len(model.historyEntries) != 1 {
		t.Fatalf("history request = %+v, entries = %+v", history.request, model.historyEntries)
	}
}

func TestNewModelRejectsDuplicateRequestIdentity(t *testing.T) {
	t.Parallel()

	data := testData()
	data.Requests[1].ID = data.Requests[0].ID
	if _, err := NewModel(&stubTarget{}, nil, data); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("NewModel() error = %v, want ErrInvalidModel", err)
	}
}

func TestModelGoldenViews(t *testing.T) {
	t.Parallel()

	t.Run("semantic-stop", func(t *testing.T) {
		model := newTestModel(t, &stubTarget{}, nil)
		model.state = debug.State{
			Active:           []uint64{0b11},
			Positive:         []uint64{0b11},
			Negative:         []uint64{0},
			Reasons:          make([]uint64, truth.ReasonCount),
			OutcomeIDs:       []schema.OutcomeID{1, 2},
			Instruction:      2,
			NextInstruction:  3,
			Node:             2,
			SourceStart:      120,
			SourceEnd:        164,
			TruthWordOffset:  64,
			ReasonWordOffset: 320,
			Cursor:           2,
			Rows:             2,
			Status:           debug.StatusPaused,
			Stop:             debug.StopInstruction,
		}
		assertGoldenView(t, "semantic-stop.txt", model.View())
	})

	t.Run("disconnected", func(t *testing.T) {
		model := newTestModel(t, &stubTarget{err: fmt.Errorf("peer closed: %w", debug.ErrTransportClosed)}, nil)
		model = runModelCommand(t, model, model.Init())
		assertGoldenView(t, "disconnected.txt", model.View())
	})
}

func TestModelBoundsTinyTerminal(t *testing.T) {
	t.Parallel()

	model := newTestModel(t, &stubTarget{}, nil)
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 20, Height: 2})
	view := model.View()
	if got := lipgloss.Width(view); got != 20 {
		t.Fatalf("tiny view width = %d, want 20:\n%s", got, view)
	}
	if got := lipgloss.Height(view); got != 2 {
		t.Fatalf("tiny view height = %d, want 2:\n%s", got, view)
	}
}

func TestNewModelRejectsTerminalControlCharacters(t *testing.T) {
	t.Parallel()

	data := testData()
	data.AST.Labels[0] = "\x1b[31mall"
	if _, err := NewModel(&stubTarget{}, nil, data); !errors.Is(err, ErrInvalidModel) {
		t.Fatalf("NewModel() error = %v, want ErrInvalidModel", err)
	}
}

func newTestModel(t *testing.T, target Target, history HistoryLoader) *Model {
	t.Helper()
	model, err := NewModel(target, history, testData())
	if err != nil {
		t.Fatalf("NewModel() error = %v", err)
	}
	model.width = 120
	model.height = 24
	model.graphColor = false
	return model
}

func testData() Data {
	return Data{
		Requests: []RequestItem{
			{ID: 1, Name: "R1", Decision: "Approve", Text: "local review"},
			{ID: 2, Name: "R2", Decision: "Reject", Text: "remote disclosure"},
		},
		AST: testGraph(
			[]string{"all", "protected", "local"},
			[]uint32{0, 2, 2}, []uint16{2, 0, 0}, []uint32{2, 3},
			[]graphview.NodeKind{graphview.NodeAll, graphview.NodeCompare, graphview.NodeCompare},
			graphview.EdgeArgument,
		),
		Program: testGraph(
			[]string{"and", "field:eq", "field:eq"},
			[]uint32{0, 2, 2}, []uint16{2, 0, 0}, []uint32{2, 3},
			[]graphview.NodeKind{graphview.NodeInstruction, graphview.NodeInstruction, graphview.NodeInstruction},
			graphview.EdgeOperand,
		),
	}
}

func testGraph(labels []string, starts []uint32, counts []uint16, edges []uint32, kinds []graphview.NodeKind, edgeKind graphview.EdgeKind) Graph {
	details := make([]string, len(labels))
	sourceStarts := make([]uint32, len(labels))
	sourceEnds := make([]uint32, len(labels))
	edgeKinds := make([]graphview.EdgeKind, len(edges))
	edgeLabels := make([]string, len(edges))
	for source := range starts {
		start := starts[source]
		end := start + uint32(counts[source])
		for edge := start; edge < end; edge++ {
			edgeKinds[edge] = edgeKind
			position := edge - start + 1
			if edgeKind == graphview.EdgeOperand {
				edgeLabels[edge] = "operand " + strconv.FormatUint(uint64(position), 10)
			} else {
				edgeLabels[edge] = "arg " + strconv.FormatUint(uint64(position), 10)
			}
		}
	}
	return Graph{
		Labels: labels, Details: details, Kinds: kinds,
		SourceStarts: sourceStarts, SourceEnds: sourceEnds,
		EdgeStarts: starts, EdgeCounts: counts, Edges: edges,
		EdgeKinds: edgeKinds, EdgeLabels: edgeLabels, Roots: []uint32{1},
	}
}

func updateModel(t *testing.T, model *Model, message tea.Msg) *Model {
	t.Helper()
	updated, _ := model.Update(message)
	result, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update() model type = %T", updated)
	}
	return result
}

func updateModelCommand(t *testing.T, model *Model, message tea.Msg) (*Model, tea.Cmd) {
	t.Helper()
	updated, command := model.Update(message)
	result, ok := updated.(*Model)
	if !ok {
		t.Fatalf("Update() model type = %T", updated)
	}
	if command == nil {
		t.Fatalf("Update(%v) command is nil", message)
	}
	return result, command
}

func runModelCommand(t *testing.T, model *Model, command tea.Cmd) *Model {
	t.Helper()
	if command == nil {
		t.Fatal("command is nil")
	}
	return updateModel(t, model, command())
}

func runeKey(value rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{value}}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}

func assertGoldenView(t *testing.T, name, view string) {
	t.Helper()
	got := canonicalView(view)
	path := filepath.Join("..", "..", "..", "testdata", "golden", "tui", name)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v\ncandidate:\n%s", name, err, got)
	}
	if string(want) != got+"\n" {
		t.Fatalf("golden %s mismatch\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

func canonicalView(view string) string {
	lines := strings.Split(strings.ReplaceAll(view, "\r\n", "\n"), "\n")
	for row := range lines {
		lines[row] = strings.TrimRight(lines[row], " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

type stubTarget struct {
	state        debug.State
	breakpoint   debug.Breakpoint
	removed      debug.BreakpointID
	watch        debug.Watch
	removedWatch debug.WatchID
	err          error
	steps        int
	operation    string
	replayed     schema.InstructionID
}

func (target *stubTarget) Snapshot(context.Context) (debug.State, error) {
	target.operation = "snapshot"
	return target.state, target.err
}

func (target *stubTarget) StepInstruction(context.Context) (debug.State, error) {
	target.steps++
	target.operation = "step-instruction"
	return target.state, target.err
}

func (target *stubTarget) StepNode(context.Context) (debug.State, error) {
	target.operation = "step-node"
	return target.state, target.err
}

func (target *stubTarget) StepOver(context.Context) (debug.State, error) {
	target.operation = "step-over"
	return target.state, target.err
}

func (target *stubTarget) Continue(context.Context) (debug.State, error) {
	target.operation = "continue"
	return target.state, target.err
}

func (target *stubTarget) Pause(context.Context) (debug.State, error) {
	target.operation = "pause"
	return target.state, target.err
}

func (target *stubTarget) Restart(context.Context) (debug.State, error) {
	target.operation = "restart"
	return target.state, target.err
}

func (target *stubTarget) Replay(_ context.Context, instruction schema.InstructionID) (debug.State, error) {
	target.operation = "replay"
	target.replayed = instruction
	return target.state, target.err
}

func (target *stubTarget) AddBreakpoint(_ context.Context, breakpoint debug.Breakpoint) (debug.BreakpointID, error) {
	target.breakpoint = breakpoint
	return 1, target.err
}

func (target *stubTarget) RemoveBreakpoint(_ context.Context, id debug.BreakpointID) error {
	target.removed = id
	return target.err
}

func (target *stubTarget) AddWatch(_ context.Context, watch debug.Watch) (debug.WatchID, error) {
	target.watch = watch
	return 1, target.err
}

func (target *stubTarget) RemoveWatch(_ context.Context, id debug.WatchID) error {
	target.removedWatch = id
	return target.err
}

type deadlineTarget struct {
	stubTarget
	hadDeadline bool
}

func (target *deadlineTarget) Continue(ctx context.Context) (debug.State, error) {
	_, target.hadDeadline = ctx.Deadline()
	return target.state, nil
}

type failedContinueTarget struct {
	stubTarget
	err error
}

func (target *failedContinueTarget) Continue(context.Context) (debug.State, error) {
	return debug.State{}, target.err
}

type stubHistory struct {
	request RequestItem
	entries []HistoryEntry
	err     error
}

func (history *stubHistory) LoadHistory(_ context.Context, request RequestItem) ([]HistoryEntry, error) {
	history.request = request
	return history.entries, history.err
}

type blockingTarget struct {
	stubTarget
}

func (target *blockingTarget) Continue(ctx context.Context) (debug.State, error) {
	target.operation = "continue"
	<-ctx.Done()
	return debug.State{}, ctx.Err()
}
