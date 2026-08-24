// Package tui provides the Bubble Tea semantic debugger adapter.
package tui

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	ErrInvalidModel   = errors.New("tui: invalid model configuration")
	ErrInvalidHistory = errors.New("tui: invalid history response")
)

const (
	MaxRequests     = 4096
	MaxGraphNodes   = 16384
	MaxGraphEdges   = 65536
	MaxRequestName  = 128
	MaxDecisionText = 64
	MaxRequestText  = 4096
	MaxGraphLabel   = 256
)

// Target is the semantic command subset used by the debugger model.
type Target interface {
	Snapshot(context.Context) (debug.State, error)
	StepInstruction(context.Context) (debug.State, error)
	StepNode(context.Context) (debug.State, error)
	StepOver(context.Context) (debug.State, error)
	Continue(context.Context) (debug.State, error)
	Pause(context.Context) (debug.State, error)
	Restart(context.Context) (debug.State, error)
	Replay(context.Context, schema.InstructionID) (debug.State, error)
	AddBreakpoint(context.Context, debug.Breakpoint) (debug.BreakpointID, error)
	RemoveBreakpoint(context.Context, debug.BreakpointID) error
	AddWatch(context.Context, debug.Watch) (debug.WatchID, error)
	RemoveWatch(context.Context, debug.WatchID) error
}

// HistoryLoader loads bounded historical decisions for the selected request.
type HistoryLoader interface {
	LoadHistory(context.Context, schema.RequestID) ([]HistoryEntry, error)
}

// HistoryEntry is one display-ready historical decision.
type HistoryEntry struct {
	At       time.Time
	Policy   string
	Decision string
}

// RequestItem is one selectable request row.
type RequestItem struct {
	Name     string
	Decision string
	Text     string
	ID       schema.RequestID
}

// Graph stores one display-ready DAG in one-based CSR form.
type Graph struct {
	Labels      []string
	ChildStarts []uint32
	ChildCounts []uint16
	Children    []uint32
	Roots       []uint32
}

// Data is immutable presentation data borrowed for the model lifetime.
type Data struct {
	Requests []RequestItem
	AST      Graph
	Program  Graph
}

type graphKind uint8

const (
	graphAST graphKind = iota
	graphProgram
)

// Model owns serialized terminal interaction state. Target calls are issued as
// Bubble Tea commands and feed immutable snapshots back into Update.
type Model struct {
	target             Target
	history            HistoryLoader
	continueCancel     context.CancelFunc
	status             string
	data               Data
	breakpoints        []breakpointBinding
	watches            []watchBinding
	historyEntries     []HistoryEntry
	state              debug.State
	height             int
	width              int
	selectedRequest    int
	nextSequence       uint64
	appliedSequence    uint64
	pending            uint32
	graphMode          graphKind
	expandShared       bool
	disconnected       bool
	historyPending     bool
	historyQueued      bool
	commandPending     bool
	pauseAfterContinue bool
}

var (
	_ tea.Model = (*Model)(nil)
	_ Target    = (*debug.Client)(nil)
)

// NewModel validates immutable presentation data and constructs a debugger.
func NewModel(target Target, history HistoryLoader, data Data) (*Model, error) {
	if target == nil || len(data.Requests) == 0 || len(data.Requests) > MaxRequests ||
		!validGraph(data.AST) || !validGraph(data.Program) {
		return nil, ErrInvalidModel
	}
	requestIDs := make(map[schema.RequestID]struct{}, len(data.Requests))
	for _, request := range data.Requests {
		if request.ID == 0 || request.Name == "" || len(request.Name) > MaxRequestName ||
			len(request.Decision) > MaxDecisionText || len(request.Text) > MaxRequestText ||
			!validDisplayText(request.Name, false) || !validDisplayText(request.Decision, false) ||
			!validDisplayText(request.Text, true) {
			return nil, ErrInvalidModel
		}
		if _, exists := requestIDs[request.ID]; exists {
			return nil, ErrInvalidModel
		}
		requestIDs[request.ID] = struct{}{}
	}
	return &Model{target: target, history: history, data: data}, nil
}

func validGraph(graph Graph) bool {
	if len(graph.Labels) == 0 || len(graph.ChildStarts) != len(graph.Labels) ||
		len(graph.ChildCounts) != len(graph.Labels) || len(graph.Roots) == 0 ||
		len(graph.Labels) > MaxGraphNodes || len(graph.Children) > MaxGraphEdges || len(graph.Roots) > MaxGraphNodes {
		return false
	}
	nodes := uint64(len(graph.Labels))
	var edge uint64
	for row := range graph.Labels {
		start := uint64(graph.ChildStarts[row])
		end := start + uint64(graph.ChildCounts[row])
		if graph.Labels[row] == "" || len(graph.Labels[row]) > MaxGraphLabel ||
			!validDisplayText(graph.Labels[row], false) || start != edge || end > uint64(len(graph.Children)) {
			return false
		}
		edge = end
	}
	if edge != uint64(len(graph.Children)) {
		return false
	}
	for _, id := range graph.Roots {
		if id == 0 || uint64(id) > nodes {
			return false
		}
	}
	for _, id := range graph.Children {
		if id == 0 || uint64(id) > nodes {
			return false
		}
	}
	return true
}

func validDisplayText(value string, multiline bool) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && !(multiline && character == '\n') {
			return false
		}
	}
	return true
}

// Init implements tea.Model.
func (model *Model) Init() tea.Cmd {
	return model.stateCommand(actionSnapshot)
}

type semanticAction uint8

const (
	actionSnapshot semanticAction = iota
	actionStepInstruction
	actionStepNode
	actionStepOver
	actionContinue
	actionPause
	actionRestart
	actionReplay
)

type stateMessage struct {
	err      error
	state    debug.State
	sequence uint64
	action   semanticAction
}

type breakpointBinding struct {
	node schema.NodeID
	id   debug.BreakpointID
}

type breakpointMessage struct {
	err    error
	node   schema.NodeID
	id     debug.BreakpointID
	remove bool
}

type watchBinding struct {
	instruction schema.InstructionID
	id          debug.WatchID
	row         uint32
}

type watchMessage struct {
	err         error
	instruction schema.InstructionID
	id          debug.WatchID
	row         uint32
	remove      bool
}

type historyMessage struct {
	err     error
	entries []HistoryEntry
	request schema.RequestID
}
