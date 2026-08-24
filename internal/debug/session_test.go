package debug

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

func TestSessionStepsRestartsAndReplaysDeterministically(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initial, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if initial.Status != StatusPaused || initial.Cursor != 0 || initial.NextInstruction != 1 {
		t.Fatalf("initial state = %+v", initial)
	}
	stepped, err := session.StepInstruction(ctx)
	if err != nil {
		t.Fatalf("StepInstruction() error = %v", err)
	}
	if stepped.Stop != StopInstruction || stepped.Cursor != 1 || stepped.Instruction != 1 || len(stepped.Active) == 0 {
		t.Fatalf("instruction state = %+v", stepped)
	}
	node, err := session.StepNode(ctx)
	if err != nil {
		t.Fatalf("StepNode() error = %v", err)
	}
	if node.Stop != StopNode || node.Cursor <= stepped.Cursor {
		t.Fatalf("node state = %+v after %+v", node, stepped)
	}
	over, err := session.StepOver(ctx)
	if err != nil {
		t.Fatalf("StepOver() error = %v", err)
	}
	if over.Stop != StopOver || over.Cursor <= node.Cursor {
		t.Fatalf("step-over state = %+v after %+v", over, node)
	}
	complete, err := session.Continue(ctx)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if complete.Status != StatusComplete || complete.Stop != StopComplete || complete.Cursor != uint32(len(p.Opcodes)) {
		t.Fatalf("complete state = %+v", complete)
	}
	got, err := session.Result(ctx)
	if err != nil {
		t.Fatalf("Result() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("debug result differs\ngot:  %+v\nwant: %+v", got, want)
	}
	complete.OutcomeIDs[0] = 0
	got.OutcomeIDs[0] = 0
	freshState, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() after result mutation error = %v", err)
	}
	freshResult, err := session.Result(ctx)
	if err != nil || freshState.OutcomeIDs[0] != want.OutcomeIDs[0] || freshResult.OutcomeIDs[0] != want.OutcomeIDs[0] {
		t.Fatal("state or result aliases caller-mutated outcome storage")
	}

	target := schema.InstructionID(len(p.Opcodes) / 2)
	replayed, err := session.Replay(ctx, target)
	if err != nil {
		t.Fatalf("Replay(%d) error = %v", target, err)
	}
	if replayed.Status != StatusPaused || replayed.Stop != StopReplay || replayed.Cursor != uint32(target) ||
		replayed.ReplayFrom > target || replayed.CheckpointCount > uint32(debugConfig().MaxCheckpoints) {
		t.Fatalf("replayed state = %+v", replayed)
	}
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() after replay error = %v", err)
	}
	got, err = session.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed result = (%+v, %v), want %+v", got, err, want)
	}

	restarted, err := session.Restart(ctx)
	if err != nil {
		t.Fatalf("Restart() error = %v", err)
	}
	if restarted.Stop != StopRestart || restarted.Cursor != 0 || restarted.Status != StatusPaused {
		t.Fatalf("restarted state = %+v", restarted)
	}
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() after restart error = %v", err)
	}
	got, err = session.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("restarted result = (%+v, %v), want %+v", got, err, want)
	}
}

func TestSessionBreakpointTypes(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	evidenceInstruction, evidenceState := debugEvidenceBreakpoint(t, p, batch)
	breakpoints := []struct {
		name       string
		breakpoint Breakpoint
		check      func(State) bool
	}{
		{name: "instruction", breakpoint: Breakpoint{Kind: BreakInstruction, Instruction: 2},
			check: func(state State) bool { return state.Instruction == 2 }},
		{name: "node", breakpoint: Breakpoint{Kind: BreakNode, Node: p.InstructionNodes[1]},
			check: func(state State) bool { return state.Node == p.InstructionNodes[1] }},
		{name: "truth", breakpoint: Breakpoint{Kind: BreakTruth, Truth: TruthTrue, Row: AnyRow},
			check: func(state State) bool { return state.Instruction != 0 }},
		{name: "evidence", breakpoint: Breakpoint{Kind: BreakEvidenceState, EvidenceState: evidenceState, Row: AnyRow},
			check: func(state State) bool { return state.Instruction == evidenceInstruction }},
		{name: "outcome", breakpoint: Breakpoint{Kind: BreakOutcome, Outcome: want.OutcomeIDs[0], Row: AnyRow},
			check: func(state State) bool { return state.Status == StatusComplete }},
	}
	for _, test := range breakpoints {
		t.Run(test.name, func(t *testing.T) {
			session := newDebugSession(t, p, batch, debugConfig())
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			id, err := session.AddBreakpoint(ctx, test.breakpoint)
			if err != nil || id == 0 {
				t.Fatalf("AddBreakpoint() = (%d, %v)", id, err)
			}
			state, err := session.Continue(ctx)
			if err != nil {
				t.Fatalf("Continue() error = %v", err)
			}
			if state.Stop != StopBreakpoint || state.Breakpoint != id || !test.check(state) {
				t.Fatalf("breakpoint state = %+v", state)
			}
			if err := session.RemoveBreakpoint(ctx, id); err != nil {
				t.Fatalf("RemoveBreakpoint() error = %v", err)
			}
		})
	}
}

func TestSessionBreakpointOnSharedDAGNode(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	node, instruction := debugSharedNode(t, p)
	session := newDebugSession(t, p, batch, debugConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := session.AddBreakpoint(ctx, Breakpoint{Kind: BreakNode, Node: node})
	if err != nil {
		t.Fatalf("AddBreakpoint(shared node %d) error = %v", node, err)
	}
	state, err := session.Continue(ctx)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if state.Stop != StopBreakpoint || state.Breakpoint != id || state.Instruction != instruction {
		t.Fatalf("shared-node breakpoint state = %+v, want instruction %d", state, instruction)
	}
}

func TestSessionWatchesAreBoundedAndSnapshotsDoNotAlias(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	config := debugConfig()
	config.MaxWatches = 4
	session := newDebugSession(t, p, batch, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	watches := []Watch{
		{Kind: WatchField, Field: 1, Row: 0},
		{Kind: WatchMask, Instruction: 1, Row: 0},
		{Kind: WatchEvidence, Evidence: batch.Evidence.IDs[0]},
		{Kind: WatchOutcome, Row: 0},
	}
	ids := make([]WatchID, len(watches))
	for index, watch := range watches {
		id, err := session.AddWatch(ctx, watch)
		if err != nil {
			t.Fatalf("AddWatch(%d) error = %v", index, err)
		}
		ids[index] = id
	}
	if _, err := session.AddWatch(ctx, Watch{Kind: WatchMask, Instruction: 2, Row: 0}); err == nil {
		t.Fatal("AddWatch() beyond bound error = nil")
	}
	initial, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(initial.Watches) != 4 || !initial.Watches[0].Ready || initial.Watches[1].Ready ||
		!initial.Watches[2].Ready || initial.Watches[3].Ready {
		t.Fatalf("initial watches = %+v", initial.Watches)
	}
	stepped, err := session.StepInstruction(ctx)
	if err != nil || !stepped.Watches[1].Ready {
		t.Fatalf("stepped watches = (%+v, %v)", stepped.Watches, err)
	}
	stepped.Positive[0] = ^uint64(0)
	stepped.Watches[0].Present = !stepped.Watches[0].Present
	fresh, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() after mutation error = %v", err)
	}
	if fresh.Positive[0] == ^uint64(0) || fresh.Watches[0].Present == stepped.Watches[0].Present {
		t.Fatal("snapshot aliases caller-mutated storage")
	}
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	complete, err := session.Snapshot(ctx)
	if err != nil || !complete.Watches[3].Ready || complete.Watches[3].Outcome == 0 {
		t.Fatalf("complete outcome watch = (%+v, %v)", complete.Watches[3], err)
	}
	if err := session.RemoveWatch(ctx, ids[0]); err != nil {
		t.Fatalf("RemoveWatch() error = %v", err)
	}
	afterRemove, err := session.Snapshot(ctx)
	if err != nil || len(afterRemove.Watches) != 3 {
		t.Fatalf("watches after remove = (%+v, %v)", afterRemove.Watches, err)
	}
}

func TestSessionPauseInterruptsContinue(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	extendDebugSchedule(p, 1<<16)
	config := debugConfig()
	config.CheckpointInterval = 1
	session := newDebugSession(t, p, batch, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	continued := make(chan State, 1)
	continueErr := make(chan error, 1)
	go func() {
		state, err := session.Continue(ctx)
		continued <- state
		continueErr <- err
	}()

	var running State
	for attempt := 0; attempt < 100; attempt++ {
		state, err := session.Snapshot(ctx)
		if err != nil {
			t.Fatalf("Snapshot() while running error = %v", err)
		}
		if state.Status == StatusRunning {
			running = state
			break
		}
		if state.Status == StatusComplete {
			t.Fatal("Continue() completed before pause could be observed")
		}
	}
	if running.Status != StatusRunning {
		t.Fatal("session did not report running state")
	}
	paused, err := session.Pause(ctx)
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	continuedState := <-continued
	if err := <-continueErr; err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if paused.Status != StatusPaused || paused.Stop != StopPause || continuedState.Stop != StopPause ||
		paused.Cursor != continuedState.Cursor {
		t.Fatalf("pause states = pause:%+v continue:%+v", paused, continuedState)
	}
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() after pause error = %v", err)
	}
	got, err := session.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("result after pause = (%+v, %v), want %+v", got, err, want)
	}
}

func TestSessionCompleteCommandSemantics(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}

	paused, err := session.Pause(ctx)
	if err != nil {
		t.Fatalf("Pause() after completion error = %v", err)
	}
	if paused.Status != StatusComplete || paused.Stop != StopComplete {
		t.Fatalf("Pause() after completion = %+v", paused)
	}
	for _, test := range []struct {
		name string
		step func(context.Context) (State, error)
	}{
		{name: "instruction", step: session.StepInstruction},
		{name: "node", step: session.StepNode},
		{name: "over", step: session.StepOver},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.step(ctx); !errors.Is(err, ErrSessionComplete) {
				t.Fatalf("step error = %v, want ErrSessionComplete", err)
			}
		})
	}

	restarted, err := session.Replay(ctx, 0)
	if err != nil || restarted.Cursor != 0 || restarted.ReplayFrom != 0 || restarted.Status != StatusPaused {
		t.Fatalf("Replay(0) = (%+v, %v)", restarted, err)
	}
	complete, err := session.Replay(ctx, schema.InstructionID(len(p.Opcodes)))
	if err != nil || complete.Cursor != uint32(len(p.Opcodes)) || complete.Status != StatusComplete {
		t.Fatalf("Replay(end) = (%+v, %v)", complete, err)
	}
	got, err := session.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed result = (%+v, %v), want %+v", got, err, want)
	}
}

func TestSessionBoundsBreakpointsAndCheckpoints(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	config := debugConfig()
	config.MaxBreakpoints = 1
	config.CheckpointInterval = 1
	config.MaxCheckpoints = 2
	session := newDebugSession(t, p, batch, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	id, err := session.AddBreakpoint(ctx, Breakpoint{Kind: BreakInstruction, Instruction: 1})
	if err != nil {
		t.Fatalf("AddBreakpoint() error = %v", err)
	}
	if _, err := session.AddBreakpoint(ctx, Breakpoint{Kind: BreakInstruction, Instruction: 2}); !errors.Is(err, ErrBreakpointLimit) {
		t.Fatalf("AddBreakpoint() beyond bound error = %v, want ErrBreakpointLimit", err)
	}
	if err := session.RemoveBreakpoint(ctx, id); err != nil {
		t.Fatalf("RemoveBreakpoint() error = %v", err)
	}
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	complete, err := session.Snapshot(ctx)
	if err != nil || complete.CheckpointCount != uint32(config.MaxCheckpoints) {
		t.Fatalf("complete checkpoints = (%d, %v), want %d", complete.CheckpointCount, err, config.MaxCheckpoints)
	}
	target := schema.InstructionID(len(p.Opcodes) - 3)
	replayed, err := session.Replay(ctx, target)
	if err != nil {
		t.Fatalf("Replay(%d) error = %v", target, err)
	}
	if replayed.ReplayFrom != 0 || replayed.Cursor != uint32(target) ||
		replayed.CheckpointCount > uint32(config.MaxCheckpoints) {
		t.Fatalf("replayed state = %+v", replayed)
	}
	if _, err := session.Continue(ctx); err != nil {
		t.Fatalf("Continue() after replay error = %v", err)
	}
	got, err := session.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("result after checkpoint replay = (%+v, %v), want %+v", got, err, want)
	}
}

func TestSessionQueuedCommandsPreserveSubmissionOrder(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	breakpointReply, err := session.enqueue(ctx, command{
		kind:       commandAddBreakpoint,
		breakpoint: Breakpoint{Kind: BreakInstruction, Instruction: 1},
	})
	if err != nil {
		t.Fatalf("enqueue(AddBreakpoint) error = %v", err)
	}
	continueReply, err := session.enqueue(ctx, command{kind: commandContinue})
	if err != nil {
		t.Fatalf("enqueue(Continue) error = %v", err)
	}
	added := <-breakpointReply
	continued := <-continueReply
	if added.err != nil || added.breakpointID == 0 {
		t.Fatalf("AddBreakpoint response = %+v", added)
	}
	if continued.err != nil || continued.state.Stop != StopBreakpoint ||
		continued.state.Breakpoint != added.breakpointID || continued.state.Instruction != 1 {
		t.Fatalf("Continue response = %+v after AddBreakpoint %+v", continued, added)
	}
}

func TestSessionCanceledPausedCommandDoesNotMutateState(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	state := &sessionState{
		program: p, batch: batch,
		checkpoints: newCheckpointSet(1, 1),
	}
	if err := state.execution.Begin(p, batch); err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reply := make(chan response, 1)
	request := command{ctx: ctx, reply: reply, kind: commandStepInstruction}
	var pending command
	running := false
	if closed := (&Session{}).handlePaused(state, request, &pending, &running); closed {
		t.Fatal("canceled StepInstruction closed the session")
	}
	got := <-reply
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("canceled StepInstruction error = %v, want context.Canceled", got.err)
	}
	if cursor := state.execution.Cursor(); cursor != 0 {
		t.Fatalf("canceled StepInstruction cursor = %d, want 0", cursor)
	}
}

func debugFixture(t testing.TB) (*program.Program, eval.Batch, result.Batch) {
	t.Helper()
	fields, symbols, err := verifoxx.NewSchema()
	if err != nil {
		t.Fatalf("build policy schema: %v", err)
	}
	policySource := []byte(verifoxx.Source())
	builder := ast.NewBuilder(ast.Hints{
		Nodes: 48, CompareNodes: 32, GroupNodes: 12, ChildEdges: 48, EvidenceNodes: 8,
		Values: 96, SymbolValues: 96, SymbolBytes: 4096, EvidenceKinds: 8, EvidenceStates: 16,
		Outcomes: 8, Remediations: 4, Clauses: 8, ClauseEvidenceEdges: 8,
		ClauseRemediationEdges: 4, Requirements: 4, RequirementClauseEdges: 8, SourceBytes: len(policySource),
	})
	if err := jsonpolicy.Decode(builder, policySource, fields, symbols, jsonpolicy.Limits{}); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	p, err := compile.Lower(builder.Document(), fields, symbols)
	if err != nil {
		t.Fatalf("compile policy: %v", err)
	}
	var batchBuilder eval.Builder
	batch, err := jsonbatch.Decode(
		&batchBuilder, p, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()), jsonbatch.Limits{},
	)
	if err != nil {
		t.Fatalf("decode fixtures: %v", err)
	}
	var want result.Batch
	var executor eval.Executor
	if err := executor.Execute(&want, p, batch); err != nil {
		t.Fatalf("execute policy: %v", err)
	}
	return p, batch, want
}

func debugEvidenceBreakpoint(t testing.TB, p *program.Program, batch eval.Batch) (schema.InstructionID, schema.EvidenceStateID) {
	t.Helper()
	for row, opcode := range p.Opcodes {
		if opcode != program.OpcodeEvidence {
			continue
		}
		kind := p.EvidenceKinds[row]
		for evidenceRow, evidenceKind := range batch.Evidence.Kinds {
			if evidenceKind == kind {
				return schema.InstructionID(row + 1), batch.Evidence.States[evidenceRow]
			}
		}
	}
	t.Fatal("fixture has no matching evidence breakpoint")
	return 0, 0
}

func debugSharedNode(t testing.TB, p *program.Program) (schema.NodeID, schema.InstructionID) {
	t.Helper()
	for row, start := range p.NodeInstructionStarts {
		node := schema.NodeID(row + 1)
		owned := false
		for _, owner := range p.InstructionNodes {
			if owner == node {
				owned = true
				break
			}
		}
		if owned || row >= len(p.NodeInstructionCounts) {
			continue
		}
		end := uint64(start) + uint64(p.NodeInstructionCounts[row])
		if end > uint64(len(p.NodeInstructionIDs)) || start == uint32(end) {
			continue
		}
		return node, p.NodeInstructionIDs[start]
	}
	t.Fatal("fixture has no non-owner shared DAG node")
	return 0, 0
}

func extendDebugSchedule(p *program.Program, count int) {
	baseTruthSlot := p.TruthSlotCount
	baseReasonSlot := p.ReasonSlotCount
	for index := range count {
		p.Opcodes = append(p.Opcodes, program.OpcodeExists)
		p.Fields = append(p.Fields, 1)
		p.Values = append(p.Values, 0)
		p.ListStarts = append(p.ListStarts, 0)
		p.ListCounts = append(p.ListCounts, 0)
		p.OperandStarts = append(p.OperandStarts, uint32(len(p.Operands)))
		p.OperandCounts = append(p.OperandCounts, 0)
		p.EvidenceKinds = append(p.EvidenceKinds, 0)
		p.EvidenceStates = append(p.EvidenceStates, 0)
		if len(p.EvidenceSubjects) != 0 {
			p.EvidenceSubjects = append(p.EvidenceSubjects, 0)
		}
		if len(p.EvidenceScopes) != 0 {
			p.EvidenceScopes = append(p.EvidenceScopes, 0)
		}
		if len(p.EvidenceTimings) != 0 {
			p.EvidenceTimings = append(p.EvidenceTimings, 0)
		}
		p.RootFlags = append(p.RootFlags, 0)
		p.TruthSlots = append(p.TruthSlots, schema.SlotID(baseTruthSlot+uint32(index)+1))
		p.ReasonSlots = append(p.ReasonSlots, schema.SlotID(baseReasonSlot+uint32(index)+1))
		p.InstructionNodes = append(p.InstructionNodes, p.InstructionNodes[0])
		p.InstructionSourceStarts = append(p.InstructionSourceStarts, p.InstructionSourceStarts[0])
		p.InstructionSourceEnds = append(p.InstructionSourceEnds, p.InstructionSourceEnds[0])
	}
	p.TruthSlotCount = baseTruthSlot + uint32(count)
	p.ReasonSlotCount = baseReasonSlot + uint32(count)
}

func debugConfig() Config {
	return Config{
		CommandDepth: 16, MaxBreakpoints: 16, MaxWatches: 16,
		CheckpointInterval: 2, MaxCheckpoints: 4,
	}
}

func newDebugSession(t testing.TB, p *program.Program, batch eval.Batch, config Config) *Session {
	t.Helper()
	session, err := NewSession(p, batch, config)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := session.Close(ctx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return session
}
