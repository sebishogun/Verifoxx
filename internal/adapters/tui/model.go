// Package tui provides the Bubble Tea semantic debugger adapter.
package tui

import (
	"context"
	"errors"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sebishogun/nornrune/internal/debug"
	"github.com/sebishogun/nornrune/internal/graphview"
	"github.com/sebishogun/nornrune/internal/schema"
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

	maxSessionHistoryEntries = 64
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
	LoadHistory(context.Context, RequestItem) ([]HistoryEntry, error)
}

// HistoryEntry is one display-ready historical decision.
type HistoryEntry struct {
	At       time.Time
	Policy   string
	Version  string
	Decision string
}

// RequestItem is one selectable request row.
type RequestItem struct {
	Name     string
	Decision string
	Text     string
	ID       schema.RequestID
}

// Graph is the shared immutable semantic graph consumed by every renderer.
type Graph = graphview.Graph

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

type historyKind uint8

const (
	historySession historyKind = iota
	historyPersisted
)

type sessionHistoryEntry struct {
	atUnixMilli int64
	sequence    uint64
	instruction schema.InstructionID
	node        schema.NodeID
	outcome     schema.OutcomeID
	row         uint32
	action      semanticAction
	stop        debug.StopReason
	truth       debug.TruthState
}

// Model owns serialized terminal interaction state. Target calls are issued as
// Bubble Tea commands and feed immutable snapshots back into Update.
type Model struct {
	target             Target
	history            HistoryLoader
	browser            *Browser
	continueCancel     context.CancelFunc
	browserStatus      string
	status             string
	historyError       string
	viewCache          string
	data               Data
	graphRenderer      graphRenderer
	graphBuffer        []byte
	breakpoints        []breakpointBinding
	watches            []watchBinding
	sessionHistory     []sessionHistoryEntry
	historyEntries     []HistoryEntry
	state              debug.State
	height             int
	width              int
	selectedRequest    int
	historySelection   int
	nextSequence       uint64
	appliedSequence    uint64
	pending            uint32
	graphMode          graphKind
	historyTab         historyKind
	expandShared       bool
	disconnected       bool
	historyVisible     bool
	historyFocus       bool
	historyPending     bool
	historyQueued      bool
	commandPending     bool
	pauseAfterContinue bool
	graphColor         bool
	viewDirty          bool
}

// AttachBrowser connects the model to one pre-rendered loopback viewer.
func (model *Model) AttachBrowser(browser *Browser, status string) error {
	if model == nil || browser == nil || status == "" || !validDisplayText(status, false) {
		return ErrInvalidModel
	}
	model.browser = browser
	model.browserStatus = boundedText(status, maxStatusText)
	model.viewDirty = true
	model.publishBrowserState()
	return nil
}

func (model *Model) publishBrowserState() {
	if model != nil && model.browser != nil {
		model.browser.publish(model)
	}
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
	return &Model{
		target: target, history: history, data: data,
		sessionHistory: make([]sessionHistoryEntry, 0, maxSessionHistoryEntries),
		graphColor:     lipgloss.ColorProfile() != termenv.Ascii,
		viewDirty:      true,
	}, nil
}

func validGraph(graph Graph) bool {
	return graphview.Validate(&graph, graphview.DefaultLimits()) == nil
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
