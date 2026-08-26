package compile

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func slotTestProgram(opcodes []program.Opcode, operands [][]schema.InstructionID, roots []program.RootFlags) program.Program {
	n := len(opcodes)
	p := program.Program{
		Opcodes:                 append([]program.Opcode(nil), opcodes...),
		Fields:                  make([]schema.FieldID, n),
		Values:                  make([]schema.ValueID, n),
		ListStarts:              make([]uint32, n),
		ListCounts:              make([]uint16, n),
		OperandStarts:           make([]uint32, n),
		OperandCounts:           make([]uint16, n),
		EvidenceKinds:           make([]schema.EvidenceKindID, n),
		EvidenceStates:          make([]schema.EvidenceStateID, n),
		RootFlags:               make([]program.RootFlags, n),
		InstructionNodes:        make([]schema.NodeID, n),
		InstructionSourceStarts: make([]uint32, n),
		InstructionSourceEnds:   make([]uint32, n),
	}
	copy(p.RootFlags, roots)
	for row := range n {
		p.OperandStarts[row] = uint32(len(p.Operands))
		p.OperandCounts[row] = uint16(len(operands[row]))
		p.Operands = append(p.Operands, operands[row]...)
	}
	return p
}

func TestSlotLastUseExact(t *testing.T) {
	allRoots := program.RootApplicability | program.RootAssertion | program.RootEvidence
	tests := []struct {
		name     string
		program  program.Program
		expected []uint32
	}{
		{
			name: "chain",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeNot, program.OpcodeNot},
				[][]schema.InstructionID{nil, {1}, {2}},
				[]program.RootFlags{0, 0, program.RootAssertion},
			),
			expected: []uint32{1, 2, 3},
		},
		{
			name: "shared dependency",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeNot, program.OpcodeNot},
				[][]schema.InstructionID{nil, {1}, {1}},
				[]program.RootFlags{0, 0, program.RootAssertion},
			),
			expected: []uint32{2, 1, 3},
		},
		{
			name: "independent roots",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeExists},
				[][]schema.InstructionID{nil, nil},
				[]program.RootFlags{program.RootApplicability, program.RootAssertion},
			),
			expected: []uint32{2, 2},
		},
		{
			name: "one row with several root roles",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists},
				[][]schema.InstructionID{nil},
				[]program.RootFlags{allRoots},
			),
			expected: []uint32{1},
		},
	}

	var lowerer Lowerer
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lowerer.computeLastUses(&tt.program)
			if err != nil {
				t.Fatalf("computeLastUses: %v", err)
			}
			if !reflect.DeepEqual(got, tt.expected) {
				t.Fatalf("last uses = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSlotLastUseRejectsMalformedProgram(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*program.Program)
	}{
		{"nil program", func(p *program.Program) {}},
		{"misaligned columns", func(p *program.Program) { p.Fields = p.Fields[:1] }},
		{"invalid opcode", func(p *program.Program) { p.Opcodes[0] = program.OpcodeInvalid }},
		{"bad CSR range", func(p *program.Program) { p.OperandStarts[1] = math.MaxUint32 }},
		{"zero operand", func(p *program.Program) { p.Operands[0] = 0 }},
		{"self operand", func(p *program.Program) { p.Operands[0] = 2 }},
		{"forward operand", func(p *program.Program) { p.Operands[0] = 3 }},
		{"unknown root bit", func(p *program.Program) { p.RootFlags[0] = 1 << 7 }},
	}

	var lowerer Lowerer
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "nil program" {
				if _, err := lowerer.computeLastUses(nil); !errors.Is(err, ErrInvalidGeneratedProgram) {
					t.Fatalf("error = %v, want %v", err, ErrInvalidGeneratedProgram)
				}
				return
			}
			p := slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeNot, program.OpcodeNot},
				[][]schema.InstructionID{nil, {1}, {2}},
				[]program.RootFlags{0, 0, program.RootAssertion},
			)
			tt.mutate(&p)
			if _, err := lowerer.computeLastUses(&p); !errors.Is(err, ErrInvalidGeneratedProgram) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidGeneratedProgram)
			}
		})
	}
}

func assertSafeSlotIntervals(t *testing.T, slots []schema.SlotID, lastUses []uint32, live []uint8) {
	t.Helper()
	for row, slot := range slots {
		if live != nil && live[row] == 0 {
			if slot != 0 {
				t.Fatalf("ineligible row %d has slot %d", row, slot)
			}
			continue
		}
		if slot == 0 {
			t.Fatalf("eligible row %d has zero slot", row)
		}
		for prior := range row {
			if slots[prior] == slot && lastUses[prior] > uint32(row) {
				t.Fatalf("slot %d reused at row %d before row %d last use %d",
					slot, row, prior, lastUses[prior])
			}
		}
	}
}

func TestAssignTruthSlotsReusesDeterministically(t *testing.T) {
	tests := []struct {
		name          string
		program       program.Program
		expectedSlots []schema.SlotID
		expectedPeak  uint32
	}{
		{
			name: "linear chain",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeNot, program.OpcodeNot},
				[][]schema.InstructionID{nil, {1}, {2}},
				[]program.RootFlags{0, 0, program.RootAssertion},
			),
			expectedSlots: []schema.SlotID{1, 1, 1},
			expectedPeak:  1,
		},
		{
			name: "simultaneously live branches",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeExists, program.OpcodeAll},
				[][]schema.InstructionID{nil, nil, {1, 2}},
				[]program.RootFlags{0, 0, program.RootAssertion},
			),
			expectedSlots: []schema.SlotID{1, 2, 1},
			expectedPeak:  2,
		},
		{
			name: "shared dependency",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeNot, program.OpcodeNot, program.OpcodeAll},
				[][]schema.InstructionID{nil, {1}, {1}, {2, 3}},
				[]program.RootFlags{0, 0, 0, program.RootAssertion},
			),
			expectedSlots: []schema.SlotID{1, 2, 1, 1},
			expectedPeak:  2,
		},
		{
			name: "independent retained roots",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeExists},
				[][]schema.InstructionID{nil, nil},
				[]program.RootFlags{program.RootApplicability, program.RootAssertion},
			),
			expectedSlots: []schema.SlotID{1, 2},
			expectedPeak:  2,
		},
		{
			name: "lowest released ID",
			program: slotTestProgram(
				[]program.Opcode{program.OpcodeExists, program.OpcodeExists, program.OpcodeAll, program.OpcodeExists},
				[][]schema.InstructionID{nil, nil, {1, 2}, nil},
				[]program.RootFlags{0, 0, 0, program.RootAssertion},
			),
			expectedSlots: []schema.SlotID{1, 2, 1, 1},
			expectedPeak:  2,
		},
	}

	var lowerer Lowerer
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peak, err := lowerer.assignTruthSlots(&tt.program, slotReuse)
			if err != nil {
				t.Fatalf("assignTruthSlots: %v", err)
			}
			if !reflect.DeepEqual(lowerer.slotTruth, tt.expectedSlots) || peak != tt.expectedPeak {
				t.Fatalf("slots/peak = %v/%d, want %v/%d",
					lowerer.slotTruth, peak, tt.expectedSlots, tt.expectedPeak)
			}
			for row, slot := range lowerer.slotTruth {
				if slot == 0 || uint32(slot) > peak {
					t.Fatalf("slot[%d] = %d outside 1..%d", row, slot, peak)
				}
			}
			assertSafeSlotIntervals(t, lowerer.slotTruth, lowerer.slotLastUses, nil)
		})
	}
}

func reasonSlotTestProgram() program.Program {
	return slotTestProgram(
		[]program.Opcode{
			program.OpcodeExists,
			program.OpcodeExists,
			program.OpcodeExists,
			program.OpcodeAll,
			program.OpcodeNot,
		},
		[][]schema.InstructionID{nil, nil, nil, {2, 3}, {4}},
		[]program.RootFlags{0, 0, 0, 0, program.RootAssertion},
	)
}

func TestAssignReasonSlotsUsesRootClosure(t *testing.T) {
	p := reasonSlotTestProgram()
	var lowerer Lowerer
	if err := lowerer.assignSlots(&p, slotReuse); err != nil {
		t.Fatalf("assignSlots: %v", err)
	}
	wantLive := []uint8{0, 1, 1, 1, 1}
	wantReasons := []schema.SlotID{0, 1, 2, 1, 1}
	if !reflect.DeepEqual(lowerer.slotReasonLive, wantLive) {
		t.Fatalf("reason live = %v, want %v", lowerer.slotReasonLive, wantLive)
	}
	if !reflect.DeepEqual(p.ReasonSlots, wantReasons) || p.ReasonSlotCount != 2 {
		t.Fatalf("reason slots/peak = %v/%d, want %v/2", p.ReasonSlots, p.ReasonSlotCount, wantReasons)
	}
	if !reflect.DeepEqual(p.TruthSlots, []schema.SlotID{1, 1, 2, 1, 1}) || p.TruthSlotCount != 2 {
		t.Fatalf("truth slots/peak = %v/%d", p.TruthSlots, p.TruthSlotCount)
	}
	assertSafeSlotIntervals(t, p.TruthSlots, lowerer.slotLastUses, nil)
	assertSafeSlotIntervals(t, p.ReasonSlots, lowerer.slotLastUses, lowerer.slotReasonLive)
}

func TestAssignSlotsRetainAll(t *testing.T) {
	p := reasonSlotTestProgram()
	var lowerer Lowerer
	if err := lowerer.assignSlots(&p, slotRetainAll); err != nil {
		t.Fatalf("assignSlots: %v", err)
	}
	wantTruth := []schema.SlotID{1, 2, 3, 4, 5}
	wantReasons := []schema.SlotID{0, 1, 2, 3, 4}
	if !reflect.DeepEqual(p.TruthSlots, wantTruth) || p.TruthSlotCount != 5 {
		t.Fatalf("truth slots/peak = %v/%d, want %v/5", p.TruthSlots, p.TruthSlotCount, wantTruth)
	}
	if !reflect.DeepEqual(p.ReasonSlots, wantReasons) || p.ReasonSlotCount != 4 {
		t.Fatalf("reason slots/peak = %v/%d, want %v/4", p.ReasonSlots, p.ReasonSlotCount, wantReasons)
	}
	assertSafeSlotIntervals(t, p.TruthSlots, lowerer.slotLastUses, nil)
	assertSafeSlotIntervals(t, p.ReasonSlots, lowerer.slotLastUses, lowerer.slotReasonLive)
}

func branchingSlotProgram(rows int) program.Program {
	if rows < 4 || rows&1 != 0 {
		panic("branchingSlotProgram requires an even row count of at least four")
	}
	opcodes := make([]program.Opcode, rows)
	roots := make([]program.RootFlags, rows)
	edges := make([][]schema.InstructionID, rows)
	edgeIDs := make([]schema.InstructionID, rows-1)
	edgeAt := 0
	opcodes[0] = program.OpcodeExists
	for row := 1; row < rows-1; row++ {
		if row&1 != 0 {
			opcodes[row] = program.OpcodeExists
			continue
		}
		opcodes[row] = program.OpcodeAll
		edges[row] = edgeIDs[edgeAt : edgeAt+2]
		edges[row][0] = schema.InstructionID(row - 1)
		edges[row][1] = schema.InstructionID(row)
		edgeAt += 2
	}
	opcodes[rows-1] = program.OpcodeNot
	edges[rows-1] = edgeIDs[edgeAt : edgeAt+1]
	edges[rows-1][0] = schema.InstructionID(rows - 1)
	roots[rows-1] = program.RootAssertion
	return slotTestProgram(opcodes, edges, roots)
}

func slotPeakBytes(p *program.Program, rows uint32) uint64 {
	words := uint64(truth.WordCount(rows))
	truthBytes := words * 8 * 2 * uint64(p.TruthSlotCount)
	reasonBytes := words * 8 * uint64(truth.ReasonCount) * uint64(p.ReasonSlotCount)
	return truthBytes + reasonBytes
}

func TestAssignSlotsReuse(t *testing.T) {
	const rows = 8192
	freshProgram := branchingSlotProgram(rows)
	var fresh Lowerer
	if err := fresh.assignSlots(&freshProgram, slotReuse); err != nil {
		t.Fatalf("fresh assignSlots: %v", err)
	}
	wantTruth := append([]schema.SlotID(nil), freshProgram.TruthSlots...)
	wantReasons := append([]schema.SlotID(nil), freshProgram.ReasonSlots...)
	wantTruthPeak := freshProgram.TruthSlotCount
	wantReasonPeak := freshProgram.ReasonSlotCount

	warmProgram := branchingSlotProgram(rows)
	var warm Lowerer
	if err := warm.assignSlots(&warmProgram, slotReuse); err != nil {
		t.Fatalf("prime assignSlots: %v", err)
	}
	var allocErr error
	allocs := testing.AllocsPerRun(100, func() {
		allocErr = warm.assignSlots(&warmProgram, slotReuse)
	})
	if allocErr != nil {
		t.Fatalf("warm assignSlots: %v", allocErr)
	}
	if allocs != 0 {
		t.Fatalf("warm assignSlots allocations = %g, want 0", allocs)
	}

	small := branchingSlotProgram(4)
	if err := warm.assignSlots(&small, slotReuse); err != nil {
		t.Fatalf("small assignSlots: %v", err)
	}
	if err := warm.assignSlots(&warmProgram, slotReuse); err != nil {
		t.Fatalf("re-plan after small Program: %v", err)
	}
	if !reflect.DeepEqual(warmProgram.TruthSlots, wantTruth) ||
		!reflect.DeepEqual(warmProgram.ReasonSlots, wantReasons) ||
		warmProgram.TruthSlotCount != wantTruthPeak || warmProgram.ReasonSlotCount != wantReasonPeak {
		t.Fatalf("warm re-plan differs: peaks %d/%d, want %d/%d",
			warmProgram.TruthSlotCount, warmProgram.ReasonSlotCount, wantTruthPeak, wantReasonPeak)
	}
	assertProgramSlots(t, &warmProgram)

	retained := branchingSlotProgram(rows)
	var debug Lowerer
	if err := debug.assignSlots(&retained, slotRetainAll); err != nil {
		t.Fatalf("retain-all assignSlots: %v", err)
	}
	if reuseBytes, retainBytes := slotPeakBytes(&warmProgram, rows), slotPeakBytes(&retained, rows); reuseBytes >= retainBytes {
		t.Fatalf("reuse peak = %d B, retain-all peak = %d B", reuseBytes, retainBytes)
	}
}
