package debug

import (
	"context"
	"errors"
	"math"
	"runtime"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

var (
	ErrInvalidSession    = errors.New("debug: invalid session")
	ErrSessionClosed     = errors.New("debug: session closed")
	ErrSessionRunning    = errors.New("debug: session running")
	ErrSessionComplete   = errors.New("debug: session complete")
	ErrBreakpointLimit   = errors.New("debug: breakpoint limit reached")
	ErrInvalidBreakpoint = errors.New("debug: invalid breakpoint")
	ErrWatchLimit        = errors.New("debug: watch limit reached")
	ErrInvalidWatch      = errors.New("debug: invalid watch")
	ErrResultUnavailable = errors.New("debug: result unavailable")
)

const (
	maxCommandDepth = 1024
	maxSessionItems = 4096
)

// Config bounds all session channels and retained semantic controls.
type Config struct {
	CommandDepth       int
	MaxBreakpoints     int
	MaxWatches         int
	CheckpointInterval uint32
	MaxCheckpoints     int
}

func (config Config) valid() bool {
	return config.CommandDepth > 0 && config.CommandDepth <= maxCommandDepth &&
		config.MaxBreakpoints > 0 && config.MaxBreakpoints <= maxSessionItems &&
		config.MaxWatches > 0 && config.MaxWatches <= maxSessionItems &&
		config.CheckpointInterval > 0 && config.MaxCheckpoints > 0 && config.MaxCheckpoints <= maxSessionItems
}

// Session serializes every mutable debugger operation through one actor.
type Session struct {
	commands chan command
	done     chan struct{}
}

type commandKind uint8

const (
	commandSnapshot commandKind = iota
	commandStepInstruction
	commandStepNode
	commandStepOver
	commandContinue
	commandPause
	commandRestart
	commandReplay
	commandAddBreakpoint
	commandRemoveBreakpoint
	commandAddWatch
	commandRemoveWatch
	commandResult
	commandClose
)

type command struct {
	ctx          context.Context
	reply        chan response
	breakpoint   Breakpoint
	watch        Watch
	target       schema.InstructionID
	breakpointID BreakpointID
	watchID      WatchID
	kind         commandKind
}

type response struct {
	err          error
	result       result.Batch
	state        State
	breakpointID BreakpointID
	watchID      WatchID
}

type watchEntry struct {
	watch Watch
	id    WatchID
}

type sessionState struct {
	program          *program.Program
	breakpoints      []breakpointEntry
	watches          []watchEntry
	batch            eval.Batch
	checkpoints      checkpointSet
	execution        eval.RetainedExecutor
	maxBreakpoints   int
	maxWatches       int
	nextBreakpointID BreakpointID
	nextWatchID      WatchID
}

// NewSession validates borrowed immutable inputs before starting the actor.
func NewSession(p *program.Program, batch eval.Batch, config Config) (*Session, error) {
	if p == nil || !config.valid() {
		return nil, ErrInvalidSession
	}
	state := &sessionState{
		program:        p,
		batch:          batch,
		breakpoints:    make([]breakpointEntry, 0, config.MaxBreakpoints),
		watches:        make([]watchEntry, 0, config.MaxWatches),
		checkpoints:    newCheckpointSet(config.CheckpointInterval, config.MaxCheckpoints),
		maxBreakpoints: config.MaxBreakpoints,
		maxWatches:     config.MaxWatches,
	}
	if err := state.execution.Begin(p, batch); err != nil {
		return nil, err
	}
	session := &Session{commands: make(chan command, config.CommandDepth), done: make(chan struct{})}
	go session.run(state)
	return session, nil
}

// Snapshot copies current semantic state without changing execution.
func (session *Session) Snapshot(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandSnapshot})
	return response.state, err
}

// StepInstruction executes one compiled instruction.
func (session *Session) StepInstruction(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandStepInstruction})
	return response.state, err
}

// StepNode executes the contiguous instruction run owned by the next node.
func (session *Session) StepNode(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandStepNode})
	return response.state, err
}

// StepOver executes through the final instruction mapped to the next node.
func (session *Session) StepOver(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandStepOver})
	return response.state, err
}

// Continue runs until a breakpoint, pause, cancellation, or completion.
func (session *Session) Continue(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandContinue})
	return response.state, err
}

// Pause interrupts an active Continue after its current instruction.
func (session *Session) Pause(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandPause})
	return response.state, err
}

// Restart returns to the boundary before instruction one and drops checkpoints.
func (session *Session) Restart(ctx context.Context) (State, error) {
	response, err := session.request(ctx, command{kind: commandRestart})
	return response.state, err
}

// Replay deterministically reconstructs state at the requested instruction boundary.
func (session *Session) Replay(ctx context.Context, target schema.InstructionID) (State, error) {
	response, err := session.request(ctx, command{kind: commandReplay, target: target})
	return response.state, err
}

// AddBreakpoint installs one bounded semantic stop condition.
func (session *Session) AddBreakpoint(ctx context.Context, breakpoint Breakpoint) (BreakpointID, error) {
	response, err := session.request(ctx, command{kind: commandAddBreakpoint, breakpoint: breakpoint})
	return response.breakpointID, err
}

// RemoveBreakpoint removes one session-local breakpoint.
func (session *Session) RemoveBreakpoint(ctx context.Context, id BreakpointID) error {
	_, err := session.request(ctx, command{kind: commandRemoveBreakpoint, breakpointID: id})
	return err
}

// AddWatch installs one bounded state projection.
func (session *Session) AddWatch(ctx context.Context, watch Watch) (WatchID, error) {
	response, err := session.request(ctx, command{kind: commandAddWatch, watch: watch})
	return response.watchID, err
}

// RemoveWatch removes one session-local watch.
func (session *Session) RemoveWatch(ctx context.Context, id WatchID) error {
	_, err := session.request(ctx, command{kind: commandRemoveWatch, watchID: id})
	return err
}

// Result returns a deep copy only after result reduction completes.
func (session *Session) Result(ctx context.Context) (result.Batch, error) {
	response, err := session.request(ctx, command{kind: commandResult})
	return response.result, err
}

// Close stops the actor. Repeated calls after closure succeed.
func (session *Session) Close(ctx context.Context) error {
	if session == nil {
		return ErrInvalidSession
	}
	select {
	case <-session.done:
		return nil
	default:
	}
	_, err := session.request(ctx, command{kind: commandClose})
	if errors.Is(err, ErrSessionClosed) {
		return nil
	}
	return err
}

func (session *Session) request(ctx context.Context, request command) (response, error) {
	reply, err := session.enqueue(ctx, request)
	if err != nil {
		return response{}, err
	}
	select {
	case response := <-reply:
		return response, response.err
	case <-session.done:
		select {
		case response := <-reply:
			return response, response.err
		default:
			return response{}, ErrSessionClosed
		}
	case <-ctx.Done():
		return response{}, ctx.Err()
	}
}

func (session *Session) enqueue(ctx context.Context, request command) (<-chan response, error) {
	if session == nil || session.commands == nil || session.done == nil || ctx == nil {
		return nil, ErrInvalidSession
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	request.ctx = ctx
	request.reply = make(chan response, 1)
	select {
	case session.commands <- request:
		return request.reply, nil
	case <-session.done:
		return nil, ErrSessionClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (session *Session) run(state *sessionState) {
	defer close(session.done)
	var pending command
	running := false
	for {
		if !running {
			request := <-session.commands
			if session.handlePaused(state, request, &pending, &running) {
				return
			}
			continue
		}

		select {
		case <-pending.ctx.Done():
			pending.reply <- response{state: state.snapshot(StatusPaused, StopPause, 0, 0), err: pending.ctx.Err()}
			running = false
			continue
		default:
		}
		select {
		case request := <-session.commands:
			if request.ctx == nil || request.ctx.Err() != nil {
				err := ErrInvalidSession
				if request.ctx != nil {
					err = request.ctx.Err()
				}
				request.reply <- response{err: err}
				continue
			}
			switch request.kind {
			case commandPause:
				pending.reply <- response{state: state.snapshot(StatusPaused, StopPause, 0, 0)}
				request.reply <- response{state: state.snapshot(StatusPaused, StopPause, 0, 0)}
				running = false
			case commandSnapshot:
				request.reply <- response{state: state.snapshot(StatusRunning, StopNone, 0, 0)}
			case commandClose:
				pending.reply <- response{err: ErrSessionClosed}
				request.reply <- response{state: state.snapshot(StatusClosed, StopNone, 0, 0)}
				return
			default:
				request.reply <- response{err: ErrSessionRunning}
			}
		case <-pending.ctx.Done():
			pending.reply <- response{state: state.snapshot(StatusPaused, StopPause, 0, 0), err: pending.ctx.Err()}
			running = false
		default:
			stop, breakpoint, err := state.advanceOne()
			if err != nil {
				pending.reply <- response{err: err}
				running = false
			} else if stop != StopNone {
				status := StatusPaused
				if state.execution.Complete() {
					status = StatusComplete
				}
				pending.reply <- response{state: state.snapshot(status, stop, breakpoint, 0)}
				running = false
			}
			runtime.Gosched()
		}
	}
}

func (session *Session) handlePaused(
	state *sessionState,
	request command,
	pending *command,
	running *bool,
) bool {
	if request.ctx == nil || request.ctx.Err() != nil {
		err := ErrInvalidSession
		if request.ctx != nil {
			err = request.ctx.Err()
		}
		request.reply <- response{err: err}
		return false
	}
	switch request.kind {
	case commandSnapshot:
		request.reply <- response{state: state.snapshot(state.status(), StopNone, 0, 0)}
	case commandStepInstruction:
		if state.execution.Complete() {
			request.reply <- response{state: state.snapshot(StatusComplete, StopComplete, 0, 0), err: ErrSessionComplete}
			break
		}
		stop, breakpoint, err := state.advanceOne()
		if err == nil && stop == StopNone {
			stop = StopInstruction
		}
		request.reply <- response{state: state.stopSnapshot(stop, breakpoint), err: err}
	case commandStepNode:
		stop, breakpoint, err := state.stepNode()
		request.reply <- response{state: state.stopSnapshot(stop, breakpoint), err: err}
	case commandStepOver:
		stop, breakpoint, err := state.stepOver()
		request.reply <- response{state: state.stopSnapshot(stop, breakpoint), err: err}
	case commandContinue:
		if state.execution.Complete() {
			request.reply <- response{state: state.snapshot(StatusComplete, StopComplete, 0, 0)}
			break
		}
		*pending = request
		*running = true
	case commandPause:
		if state.execution.Complete() {
			request.reply <- response{state: state.snapshot(StatusComplete, StopComplete, 0, 0)}
			break
		}
		request.reply <- response{state: state.snapshot(StatusPaused, StopPause, 0, 0)}
	case commandRestart:
		err := state.execution.Restart()
		if err == nil {
			state.checkpoints.reset()
		}
		request.reply <- response{state: state.snapshot(StatusPaused, StopRestart, 0, 0), err: err}
	case commandReplay:
		origin, err := state.replay(uint32(request.target))
		request.reply <- response{
			state: state.snapshot(state.status(), StopReplay, 0, schema.InstructionID(origin)), err: err,
		}
	case commandAddBreakpoint:
		id, err := state.addBreakpoint(request.breakpoint)
		request.reply <- response{breakpointID: id, err: err}
	case commandRemoveBreakpoint:
		request.reply <- response{err: state.removeBreakpoint(request.breakpointID)}
	case commandAddWatch:
		id, err := state.addWatch(request.watch)
		request.reply <- response{watchID: id, err: err}
	case commandRemoveWatch:
		request.reply <- response{err: state.removeWatch(request.watchID)}
	case commandResult:
		resultBatch, ok := state.execution.Result()
		if !ok {
			request.reply <- response{err: ErrResultUnavailable}
			break
		}
		request.reply <- response{result: cloneResultBatch(resultBatch)}
	case commandClose:
		request.reply <- response{state: state.snapshot(StatusClosed, StopNone, 0, 0)}
		return true
	default:
		request.reply <- response{err: ErrInvalidSession}
	}
	return false
}

func (state *sessionState) advanceOne() (StopReason, BreakpointID, error) {
	instruction, done, err := state.execution.Step()
	if err != nil {
		return StopNone, 0, err
	}
	state.checkpoints.record(state.execution.Cursor(), uint32(state.execution.InstructionCount()))
	if breakpoint := state.matchingBreakpoint(instruction); breakpoint != 0 {
		return StopBreakpoint, breakpoint, nil
	}
	if done {
		return StopComplete, 0, nil
	}
	return StopNone, 0, nil
}

func (state *sessionState) stepNode() (StopReason, BreakpointID, error) {
	cursor := state.execution.Cursor()
	if state.execution.Complete() {
		return StopComplete, 0, ErrSessionComplete
	}
	if uint64(cursor) >= uint64(len(state.program.Opcodes)) {
		return StopNone, 0, ErrInvalidSession
	}
	node := state.program.InstructionNodes[cursor]
	for uint64(state.execution.Cursor()) < uint64(len(state.program.Opcodes)) &&
		state.program.InstructionNodes[state.execution.Cursor()] == node {
		stop, breakpoint, err := state.advanceOne()
		if err != nil || stop != StopNone {
			return stop, breakpoint, err
		}
	}
	return StopNode, 0, nil
}

func (state *sessionState) stepOver() (StopReason, BreakpointID, error) {
	cursor := state.execution.Cursor()
	if state.execution.Complete() {
		return StopComplete, 0, ErrSessionComplete
	}
	if uint64(cursor) >= uint64(len(state.program.Opcodes)) {
		return StopNone, 0, ErrInvalidSession
	}
	target := cursor + 1
	node := state.program.InstructionNodes[cursor]
	nodeRow := uint64(node - 1)
	if node != 0 && nodeRow < uint64(len(state.program.NodeInstructionStarts)) &&
		nodeRow < uint64(len(state.program.NodeInstructionCounts)) {
		start := uint64(state.program.NodeInstructionStarts[nodeRow])
		count := uint64(state.program.NodeInstructionCounts[nodeRow])
		if start+count >= start && start+count <= uint64(len(state.program.NodeInstructionIDs)) {
			for _, instruction := range state.program.NodeInstructionIDs[int(start):int(start+count)] {
				if uint32(instruction) > target {
					target = uint32(instruction)
				}
			}
		}
	}
	for state.execution.Cursor() < target {
		stop, breakpoint, err := state.advanceOne()
		if err != nil || stop != StopNone {
			return stop, breakpoint, err
		}
	}
	return StopOver, 0, nil
}

func (state *sessionState) replay(target uint32) (uint32, error) {
	count := uint32(state.execution.InstructionCount())
	if target > count {
		return 0, ErrInvalidSession
	}
	cursor := state.execution.Cursor()
	origin := cursor
	if target < cursor {
		origin = state.checkpoints.nearest(target)
		if err := state.execution.Rewind(origin); err != nil {
			return 0, err
		}
		state.checkpoints.truncate(target)
	}
	for state.execution.Cursor() < target {
		if _, _, err := state.execution.Step(); err != nil {
			return 0, err
		}
		state.checkpoints.record(state.execution.Cursor(), count)
	}
	return origin, nil
}

func (state *sessionState) status() Status {
	if state.execution.Complete() {
		return StatusComplete
	}
	return StatusPaused
}

func (state *sessionState) stopSnapshot(stop StopReason, breakpoint BreakpointID) State {
	return state.snapshot(state.status(), stop, breakpoint, 0)
}

func (state *sessionState) snapshot(
	status Status,
	stop StopReason,
	breakpoint BreakpointID,
	replayFrom schema.InstructionID,
) State {
	cursor := state.execution.Cursor()
	rows := state.batch.Rows
	snapshot := State{
		Active: activeMask(rows), Status: status, Stop: stop, Breakpoint: breakpoint,
		ReplayFrom: replayFrom, Cursor: cursor, Rows: rows,
		CheckpointCount: state.checkpoints.count(),
	}
	if uint64(cursor) < uint64(len(state.program.Opcodes)) {
		snapshot.NextInstruction = schema.InstructionID(cursor + 1)
	}
	if cursor != 0 {
		instruction := schema.InstructionID(cursor)
		row := cursor - 1
		words := truth.WordCount(rows)
		snapshot.Instruction = instruction
		snapshot.Node = state.program.InstructionNodes[row]
		snapshot.SourceStart = state.program.InstructionSourceStarts[row]
		snapshot.SourceEnd = state.program.InstructionSourceEnds[row]
		snapshot.TruthSlot = schema.SlotID(instruction)
		snapshot.ReasonSlot = schema.SlotID(instruction)
		snapshot.TruthWordOffset, snapshot.ReasonWordOffset = slotWordOffsets(instruction, rows)
		snapshot.Positive = make([]uint64, words)
		snapshot.Negative = make([]uint64, words)
		snapshot.Reasons = make([]uint64, truth.ReasonCount*words)
		if err := state.execution.CopyInstruction(
			instruction, snapshot.Positive, snapshot.Negative, snapshot.Reasons,
		); err != nil {
			panic("debug: invalid retained snapshot")
		}
	}
	if resultBatch, ok := state.execution.Result(); ok {
		snapshot.OutcomeIDs = append(snapshot.OutcomeIDs, resultBatch.OutcomeIDs...)
		snapshot.RemediationOffsets = append(snapshot.RemediationOffsets, resultBatch.RemediationOffsets...)
		snapshot.RemediationIDs = append(snapshot.RemediationIDs, resultBatch.RemediationIDs...)
	}
	snapshot.Watches = state.watchValues()
	return snapshot
}

func (state *sessionState) addBreakpoint(breakpoint Breakpoint) (BreakpointID, error) {
	if !validBreakpoint(state, breakpoint) {
		return 0, ErrInvalidBreakpoint
	}
	if len(state.breakpoints) == state.maxBreakpoints || state.nextBreakpointID == math.MaxUint32 {
		return 0, ErrBreakpointLimit
	}
	state.nextBreakpointID++
	entry := breakpointEntry{id: state.nextBreakpointID, breakpoint: breakpoint}
	state.breakpoints = append(state.breakpoints, entry)
	return entry.id, nil
}

func (state *sessionState) removeBreakpoint(id BreakpointID) error {
	for index := range state.breakpoints {
		if state.breakpoints[index].id == id {
			copy(state.breakpoints[index:], state.breakpoints[index+1:])
			state.breakpoints = state.breakpoints[:len(state.breakpoints)-1]
			return nil
		}
	}
	return ErrInvalidBreakpoint
}

func (state *sessionState) addWatch(watch Watch) (WatchID, error) {
	if !state.validWatch(watch) {
		return 0, ErrInvalidWatch
	}
	if len(state.watches) == state.maxWatches || state.nextWatchID == math.MaxUint32 {
		return 0, ErrWatchLimit
	}
	state.nextWatchID++
	entry := watchEntry{id: state.nextWatchID, watch: watch}
	state.watches = append(state.watches, entry)
	return entry.id, nil
}

func (state *sessionState) removeWatch(id WatchID) error {
	for index := range state.watches {
		if state.watches[index].id == id {
			copy(state.watches[index:], state.watches[index+1:])
			state.watches = state.watches[:len(state.watches)-1]
			return nil
		}
	}
	return ErrInvalidWatch
}

func (state *sessionState) validWatch(watch Watch) bool {
	switch watch.Kind {
	case WatchField:
		_, _, ok := state.program.FieldIndex.Lookup(watch.Field)
		return ok && watch.Row < state.batch.Rows
	case WatchMask:
		return watch.Instruction != 0 && uint64(watch.Instruction) <= uint64(len(state.program.Opcodes)) &&
			watch.Row < state.batch.Rows
	case WatchEvidence:
		if watch.Evidence == 0 {
			return false
		}
		for _, id := range state.batch.Evidence.IDs {
			if id == watch.Evidence {
				return true
			}
		}
		return false
	case WatchOutcome:
		return watch.Row < state.batch.Rows
	default:
		return false
	}
}

func (state *sessionState) watchValues() []WatchValue {
	values := make([]WatchValue, len(state.watches))
	for index, entry := range state.watches {
		value := WatchValue{Watch: entry.watch, ID: entry.id}
		switch entry.watch.Kind {
		case WatchField:
			state.fieldWatch(&value)
		case WatchMask:
			positive, negative, ok := state.execution.InstructionTruth(entry.watch.Instruction, entry.watch.Row)
			if ok {
				value.Ready = true
				value.Truth = classifyTruth(positive, negative)
				value.Reasons, _ = state.execution.InstructionReasons(entry.watch.Instruction, entry.watch.Row)
			}
		case WatchEvidence:
			state.evidenceWatch(&value)
		case WatchOutcome:
			if resultBatch, ok := state.execution.Result(); ok {
				value.Ready = true
				value.Outcome = resultBatch.OutcomeIDs[entry.watch.Row]
			}
		}
		values[index] = value
	}
	return values
}

func (state *sessionState) fieldWatch(value *WatchValue) {
	kind, column, ok := state.program.FieldIndex.Lookup(value.Field)
	if !ok {
		return
	}
	value.Ready = true
	value.ValueKind = kind
	value.Present = state.batch.Present(value.Field, value.Row)
	if !value.Present {
		return
	}
	switch kind {
	case schema.ValueKindSymbol:
		value.Symbol, _ = state.batch.Symbol(column, value.Row)
	case schema.ValueKindInteger:
		value.Integer, _ = state.batch.Integer(column, value.Row)
	case schema.ValueKindTimestamp:
		value.Timestamp, _ = state.batch.Timestamp(column, value.Row)
	case schema.ValueKindBoolean:
		value.Boolean = state.batch.Boolean(column, value.Row)
	}
}

func (state *sessionState) evidenceWatch(value *WatchValue) {
	for row, id := range state.batch.Evidence.IDs {
		if id != value.Evidence {
			continue
		}
		value.Ready = true
		value.EvidenceKind = state.batch.Evidence.Kinds[row]
		value.EvidenceState = state.batch.Evidence.States[row]
		return
	}
}
