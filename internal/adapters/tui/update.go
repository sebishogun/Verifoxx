package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sebishogun/nornrune/internal/debug"
)

const (
	commandTimeout    = 5 * time.Second
	maxHistoryEntries = 64
	maxHistoryPolicy  = 128
	maxHistoryVersion = 64
	maxStatusText     = 256
)

// Update implements tea.Model.
func (model *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	model.viewDirty = true
	defer model.publishBrowserState()
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width = message.Width
		model.height = message.Height
	case stateMessage:
		return model, model.applyState(message)
	case breakpointMessage:
		model.applyBreakpoint(message)
	case watchMessage:
		model.applyWatch(message)
	case historyMessage:
		return model, model.applyHistory(message)
	case tea.KeyMsg:
		switch message.String() {
		case "q", "ctrl+c":
			if model.continueCancel != nil {
				model.continueCancel()
			}
			return model, tea.Quit
		case "up", "k":
			if model.historyVisible && model.historyFocus {
				model.moveHistorySelection(-1)
				break
			}
			if model.selectedRequest > 0 {
				model.selectedRequest--
				model.historyEntries = model.historyEntries[:0]
				model.historyError = ""
				model.historyQueued = false
			}
		case "down", "j":
			if model.historyVisible && model.historyFocus {
				model.moveHistorySelection(1)
				break
			}
			if model.selectedRequest+1 < len(model.data.Requests) {
				model.selectedRequest++
				model.historyEntries = model.historyEntries[:0]
				model.historyError = ""
				model.historyQueued = false
			}
		case "a":
			model.graphMode = graphAST
			model.status = "AST graph active"
		case "p":
			model.graphMode = graphProgram
			model.status = "Program graph active"
		case "h":
			model.historyVisible = !model.historyVisible
			model.historyFocus = model.historyVisible
			model.historyTab = historySession
			model.historySelection = max(0, len(model.sessionHistory)-1)
			if model.historyVisible {
				model.status = "Session history active"
			} else {
				model.status = "History closed"
			}
		case "tab":
			if model.historyVisible {
				model.historyFocus = true
				if model.historyTab == historySession {
					model.historyTab = historyPersisted
					model.historySelection = max(0, len(model.historyEntries)-1)
					model.status = "Persisted history active"
					return model, model.historyCommand()
				}
				model.historyTab = historySession
				model.historySelection = max(0, len(model.sessionHistory)-1)
				model.status = "Session history active"
			}
		case "esc":
			if model.historyVisible {
				model.historyFocus = false
				model.status = "Request focus active"
			}
		case "x":
			model.expandShared = !model.expandShared
		case "s":
			return model, model.stateCommand(actionStepInstruction)
		case "n":
			return model, model.stateCommand(actionStepNode)
		case "o":
			return model, model.stateCommand(actionStepOver)
		case "c":
			return model, model.stateCommand(actionContinue)
		case " ":
			return model, model.stateCommand(actionPause)
		case "r":
			return model, model.stateCommand(actionRestart)
		case "u":
			return model, model.stateCommand(actionReplay)
		case "b":
			return model, model.breakpointCommand()
		case "w":
			return model, model.watchCommand()
		}
	}
	return model, nil
}

func (model *Model) stateCommand(action semanticAction) tea.Cmd {
	bit := uint32(1) << action
	if model == nil || model.disconnected {
		return nil
	}
	if model.commandPending {
		if action == actionPause && model.pending&(uint32(1)<<actionContinue) != 0 {
			model.pauseAfterContinue = true
			if model.continueCancel != nil {
				model.continueCancel()
			}
		}
		return nil
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if action == actionContinue {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), commandTimeout)
	}
	model.commandPending = true
	model.pending |= bit
	model.nextSequence++
	sequence := model.nextSequence
	target := model.target
	if action == actionContinue {
		model.continueCancel = cancel
	}
	replayTo := model.state.Instruction
	if replayTo != 0 {
		replayTo--
	}
	return func() tea.Msg {
		defer cancel()
		var state debug.State
		var err error
		switch action {
		case actionSnapshot:
			state, err = target.Snapshot(ctx)
		case actionStepInstruction:
			state, err = target.StepInstruction(ctx)
		case actionStepNode:
			state, err = target.StepNode(ctx)
		case actionStepOver:
			state, err = target.StepOver(ctx)
		case actionContinue:
			state, err = target.Continue(ctx)
		case actionPause:
			state, err = target.Pause(ctx)
		case actionRestart:
			state, err = target.Restart(ctx)
		case actionReplay:
			state, err = target.Replay(ctx, replayTo)
		default:
			err = ErrInvalidModel
		}
		return stateMessage{state: state, err: err, sequence: sequence, action: action}
	}
}

func (model *Model) applyState(message stateMessage) tea.Cmd {
	model.pending &^= uint32(1) << message.action
	model.commandPending = false
	if message.action == actionContinue {
		model.continueCancel = nil
		if model.pauseAfterContinue {
			model.pauseAfterContinue = false
			if message.err == nil {
				model.appliedSequence = message.sequence
				model.state = message.state
				model.status = ""
				model.appendSessionHistory(message)
				if message.state.Status == debug.StatusRunning {
					return model.stateCommand(actionPause)
				}
				return nil
			}
			if errors.Is(message.err, context.Canceled) {
				return model.stateCommand(actionPause)
			}
			model.setError(message.err)
			return nil
		}
	}
	if message.err != nil {
		model.setError(message.err)
		return nil
	}
	if message.sequence < model.appliedSequence {
		return nil
	}
	model.appliedSequence = message.sequence
	model.state = message.state
	model.status = ""
	model.appendSessionHistory(message)
	return nil
}

func (model *Model) appendSessionHistory(message stateMessage) {
	if message.action != actionSnapshot && message.state.Stop == debug.StopNone {
		return
	}
	row := uint32(model.selectedRequest)
	entry := sessionHistoryEntry{
		atUnixMilli: time.Now().UnixMilli(),
		sequence:    message.sequence,
		instruction: message.state.Instruction,
		node:        message.state.Node,
		row:         row,
		action:      message.action,
		stop:        message.state.Stop,
		truth:       stateRowTruth(&message.state, row),
	}
	if uint64(row) < uint64(len(message.state.OutcomeIDs)) {
		entry.outcome = message.state.OutcomeIDs[row]
	}
	if len(model.sessionHistory) < maxSessionHistoryEntries {
		model.sessionHistory = append(model.sessionHistory, entry)
	} else {
		copy(model.sessionHistory, model.sessionHistory[1:])
		model.sessionHistory[len(model.sessionHistory)-1] = entry
	}
	if model.historyVisible && model.historyFocus && model.historyTab == historySession {
		model.historySelection = len(model.sessionHistory) - 1
	}
}

func stateRowTruth(state *debug.State, row uint32) debug.TruthState {
	positive := rowMaskBit(state.Positive, row)
	negative := rowMaskBit(state.Negative, row)
	switch {
	case positive && negative:
		return debug.TruthBoth
	case positive:
		return debug.TruthTrue
	case negative:
		return debug.TruthFalse
	default:
		return debug.TruthNeither
	}
}

func (model *Model) moveHistorySelection(delta int) {
	entries := len(model.sessionHistory)
	if model.historyTab == historyPersisted {
		entries = len(model.historyEntries)
	}
	if entries == 0 {
		model.historySelection = 0
		return
	}
	model.historySelection = min(entries-1, max(0, model.historySelection+delta))
}

func (model *Model) breakpointCommand() tea.Cmd {
	if model == nil || model.disconnected || model.commandPending || model.state.Node == 0 {
		return nil
	}
	model.commandPending = true
	node := model.state.Node
	target := model.target
	for _, binding := range model.breakpoints {
		if binding.node == node {
			id := binding.id
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
				defer cancel()
				return breakpointMessage{node: node, id: id, remove: true, err: target.RemoveBreakpoint(ctx, id)}
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		id, err := target.AddBreakpoint(ctx, debug.Breakpoint{Kind: debug.BreakNode, Node: node})
		return breakpointMessage{node: node, id: id, err: err}
	}
}

func (model *Model) applyBreakpoint(message breakpointMessage) {
	model.commandPending = false
	if message.err != nil {
		model.setError(message.err)
		return
	}
	model.status = ""
	if message.remove {
		for row := range model.breakpoints {
			if model.breakpoints[row].id == message.id {
				model.breakpoints = slices.Delete(model.breakpoints, row, row+1)
				break
			}
		}
		return
	}
	model.breakpoints = append(model.breakpoints, breakpointBinding{node: message.node, id: message.id})
}

func (model *Model) watchCommand() tea.Cmd {
	if model == nil || model.disconnected || model.commandPending || model.state.Instruction == 0 {
		return nil
	}
	model.commandPending = true
	instruction := model.state.Instruction
	row := uint32(model.selectedRequest)
	target := model.target
	for _, binding := range model.watches {
		if binding.instruction == instruction && binding.row == row {
			id := binding.id
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
				defer cancel()
				return watchMessage{instruction: instruction, row: row, id: id, remove: true, err: target.RemoveWatch(ctx, id)}
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		id, err := target.AddWatch(ctx, debug.Watch{
			Kind: debug.WatchMask, Instruction: instruction, Row: row,
		})
		return watchMessage{instruction: instruction, row: row, id: id, err: err}
	}
}

func (model *Model) applyWatch(message watchMessage) {
	model.commandPending = false
	if message.err != nil {
		model.setError(message.err)
		return
	}
	model.status = ""
	if message.remove {
		for row := range model.watches {
			if model.watches[row].id == message.id {
				model.watches = slices.Delete(model.watches, row, row+1)
				break
			}
		}
		return
	}
	model.watches = append(model.watches, watchBinding{
		instruction: message.instruction,
		row:         message.row,
		id:          message.id,
	})
}

func (model *Model) setError(err error) {
	message := err.Error()
	if errors.Is(err, debug.ErrTransportClosed) {
		model.disconnected = true
		transport := debug.ErrTransportClosed.Error()
		if message == transport {
			message = "transport closed"
		} else {
			message = strings.TrimSuffix(message, ": "+transport)
		}
	}
	model.status = boundedText(message, maxStatusText)
}

func (model *Model) historyCommand() tea.Cmd {
	if model == nil || model.disconnected {
		return nil
	}
	if model.historyPending {
		model.historyQueued = true
		model.status = "history queued"
		return nil
	}
	if model.history == nil {
		model.status = "history unavailable"
		return nil
	}
	model.historyPending = true
	model.historyError = ""
	request := model.data.Requests[model.selectedRequest]
	history := model.history
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		entries, err := history.LoadHistory(ctx, request)
		return historyMessage{entries: entries, request: request.ID, err: err}
	}
}

func (model *Model) applyHistory(message historyMessage) tea.Cmd {
	model.historyPending = false
	if model.data.Requests[model.selectedRequest].ID == message.request {
		switch {
		case message.err != nil:
			model.historyError = boundedText(message.err.Error(), maxStatusText)
			model.setError(message.err)
		case len(message.entries) > maxHistoryEntries || !validHistory(message.entries):
			model.historyError = ErrInvalidHistory.Error()
			model.setError(ErrInvalidHistory)
		default:
			model.historyEntries = slices.Clone(message.entries)
			model.historyError = ""
			if model.historyTab == historyPersisted {
				model.historySelection = max(0, len(model.historyEntries)-1)
			}
			model.status = ""
		}
	}
	if model.historyQueued {
		model.historyQueued = false
		return model.historyCommand()
	}
	return nil
}

func validHistory(entries []HistoryEntry) bool {
	for _, entry := range entries {
		if entry.At.IsZero() || entry.Policy == "" || len(entry.Policy) > maxHistoryPolicy ||
			len(entry.Version) > maxHistoryVersion ||
			entry.Decision == "" || len(entry.Decision) > MaxDecisionText ||
			!validDisplayText(entry.Policy, false) || !validDisplayText(entry.Version, false) ||
			!validDisplayText(entry.Decision, false) {
			return false
		}
	}
	return true
}

func boundedText(value string, limit int) string {
	if !validDisplayText(value, false) {
		var sanitized strings.Builder
		sanitized.Grow(min(len(value), limit))
		for _, character := range value {
			if unicode.IsControl(character) {
				sanitized.WriteByte(' ')
			} else {
				sanitized.WriteRune(character)
			}
		}
		value = sanitized.String()
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end]
}
