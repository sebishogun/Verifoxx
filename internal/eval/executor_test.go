package eval

import (
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func appendExecutorInstruction(
	p *program.Program,
	opcode program.Opcode,
	field schema.FieldID,
	value schema.ValueID,
	operands []schema.InstructionID,
	evidenceKind schema.EvidenceKindID,
	evidenceState schema.EvidenceStateID,
) schema.InstructionID {
	operandStart := uint32(len(p.Operands))
	p.Operands = append(p.Operands, operands...)
	p.Opcodes = append(p.Opcodes, opcode)
	p.Fields = append(p.Fields, field)
	p.Values = append(p.Values, value)
	p.ListStarts = append(p.ListStarts, 0)
	p.ListCounts = append(p.ListCounts, 0)
	p.OperandStarts = append(p.OperandStarts, operandStart)
	p.OperandCounts = append(p.OperandCounts, uint16(len(operands)))
	p.EvidenceKinds = append(p.EvidenceKinds, evidenceKind)
	p.EvidenceStates = append(p.EvidenceStates, evidenceState)
	p.RootFlags = append(p.RootFlags, 0)
	id := schema.InstructionID(len(p.Opcodes))
	p.InstructionNodes = append(p.InstructionNodes, schema.NodeID(id))
	p.InstructionSourceStarts = append(p.InstructionSourceStarts, uint32(id-1))
	p.InstructionSourceEnds = append(p.InstructionSourceEnds, uint32(id))
	return id
}

func executorPredicateBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
		switch row & 3 {
		case 0:
			if err := builder.SetSymbol(row, 1, 1); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetInteger(row, 2, 7); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetBoolean(row, 3, true); err != nil {
				t.Fatal(err)
			}
		case 1:
			if err := builder.SetSymbol(row, 1, 2); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetInteger(row, 2, 7); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetBoolean(row, 3, false); err != nil {
				t.Fatal(err)
			}
		case 2:
			if err := builder.SetInteger(row, 2, 8); err != nil {
				t.Fatal(err)
			}
		case 3:
			if err := builder.SetSymbol(row, 1, 1); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetBoolean(row, 3, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func executorGroupProgram(t testing.TB, opcode program.Opcode, truthDst, reasonDst schema.SlotID) (*program.Program, []schema.InstructionID) {
	t.Helper()
	p := predicateTestProgram(t)
	operands := []schema.InstructionID{
		appendExecutorInstruction(p, program.OpcodeEqual, 1, 1, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 2, 2, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 3, 3, nil, 0, 0),
	}
	root := appendExecutorInstruction(p, opcode, 0, 0, operands, 0, 0)
	p.RootFlags[root-1] = program.RootAssertion
	p.TruthSlots = []schema.SlotID{1, 2, 3, truthDst}
	p.ReasonSlots = []schema.SlotID{1, 2, 3, reasonDst}
	p.TruthSlotCount = uint32(max(3, truthDst))
	p.ReasonSlotCount = uint32(max(3, reasonDst))
	return p, operands
}

func expectedGroup(t testing.TB, p *program.Program, batch Batch, opcode program.Opcode, operands []schema.InstructionID) (truth.Planes, ReasonPlanes) {
	t.Helper()
	leafTruth := make([]truth.Planes, len(operands))
	leafReasons := make([]ReasonPlanes, len(operands))
	for i, id := range operands {
		leafTruth[i], leafReasons[i] = makeLeafOutputs(batch.Rows)
		evalPredicate(leafTruth[i], leafReasons[i], batch, p, id)
	}
	dst, reasons := makeLeafOutputs(batch.Rows)
	copy(dst.Positive, leafTruth[0].Positive)
	copy(dst.Negative, leafTruth[0].Negative)
	copy(reasons.Words, leafReasons[0].Words)
	for i := 1; i < len(operands); i++ {
		if opcode == program.OpcodeAll {
			truth.And(dst, dst, leafTruth[i], batch.Rows)
		} else {
			truth.Or(dst, dst, leafTruth[i], batch.Rows)
		}
		for word := range reasons.Words {
			reasons.Words[word] |= leafReasons[i].Words[word]
		}
	}
	return dst, reasons
}

func TestExecuteScheduleGroupSlotAliases(t *testing.T) {
	const rows = 65
	tests := []struct {
		name                string
		opcode              program.Opcode
		truthDst, reasonDst schema.SlotID
	}{
		{"all first truth last reason", program.OpcodeAll, 1, 3},
		{"all middle truth fresh reason", program.OpcodeAll, 2, 4},
		{"any last truth first reason", program.OpcodeAny, 3, 1},
		{"any fresh truth middle reason", program.OpcodeAny, 4, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, operands := executorGroupProgram(t, tt.opcode, tt.truthDst, tt.reasonDst)
			batch := executorPredicateBatch(t, p, rows)
			wantTruth, wantReasons := expectedGroup(t, p, batch, tt.opcode, operands)

			var executor Executor
			if err := executor.prepare(p, batch); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			executor.executeSchedule(p, batch)
			gotTruth := executor.truthSlot(tt.truthDst, rows)
			gotReasons := executor.reasonSlot(tt.reasonDst, rows)
			if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
				!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
				!slices.Equal(gotReasons.Words, wantReasons.Words) {
				t.Fatalf("group result = %#x/%#x %#x, want %#x/%#x %#x",
					gotTruth.Positive, gotTruth.Negative, gotReasons.Words,
					wantTruth.Positive, wantTruth.Negative, wantReasons.Words)
			}
		})
	}
}

func TestExecuteScheduleNotAliases(t *testing.T) {
	for _, dstSlot := range []schema.SlotID{1, 2} {
		t.Run(string(rune('0'+dstSlot)), func(t *testing.T) {
			p := predicateTestProgram(t)
			leaf := appendExecutorInstruction(p, program.OpcodeEqual, 1, 1, nil, 0, 0)
			root := appendExecutorInstruction(p, program.OpcodeNot, 0, 0, []schema.InstructionID{leaf}, 0, 0)
			p.RootFlags[root-1] = program.RootAssertion
			p.TruthSlots = []schema.SlotID{1, dstSlot}
			p.ReasonSlots = []schema.SlotID{1, dstSlot}
			p.TruthSlotCount = uint32(max(1, dstSlot))
			p.ReasonSlotCount = uint32(max(1, dstSlot))
			batch := executorPredicateBatch(t, p, 65)
			wantTruth, wantReasons := makeLeafOutputs(batch.Rows)
			evalPredicate(wantTruth, wantReasons, batch, p, leaf)
			truth.Not(wantTruth, wantTruth, batch.Rows)

			var executor Executor
			if err := executor.prepare(p, batch); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			executor.executeSchedule(p, batch)
			gotTruth := executor.truthSlot(dstSlot, batch.Rows)
			gotReasons := executor.reasonSlot(dstSlot, batch.Rows)
			if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
				!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
				!slices.Equal(gotReasons.Words, wantReasons.Words) {
				t.Fatal("Not result differs from scalar reference")
			}
		})
	}
}

func TestExecuteScheduleEvidenceLeaf(t *testing.T) {
	p := evidenceEvalTestProgram()
	root := appendExecutorInstruction(
		p, program.OpcodeEvidence, 0, 0, nil,
		testEvidenceKindApproval, testEvidenceStateValid,
	)
	p.RootFlags[root-1] = program.RootEvidence
	p.TruthSlots = []schema.SlotID{1}
	p.ReasonSlots = []schema.SlotID{1}
	p.TruthSlotCount = 1
	p.ReasonSlotCount = 1
	batch := scalarEvidenceBatch(t, p, 65)
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind reference states: %v", err)
	}
	wantTruth, wantReasons := makeLeafOutputs(batch.Rows)
	evalEvidence(wantTruth, wantReasons, batch, p, &states, EvidencePredicate{
		Kind: testEvidenceKindApproval, State: testEvidenceStateValid,
	})

	var executor Executor
	if err := executor.prepare(p, batch); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	executor.executeSchedule(p, batch)
	gotTruth := executor.truthSlot(1, batch.Rows)
	gotReasons := executor.reasonSlot(1, batch.Rows)
	if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
		!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
		!slices.Equal(gotReasons.Words, wantReasons.Words) {
		t.Fatal("Evidence result differs from scalar reference")
	}
}
