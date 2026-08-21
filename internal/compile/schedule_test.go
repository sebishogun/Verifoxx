package compile

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func groupedScheduleFixture() program.Program {
	return program.Program{
		Opcodes:                 []program.Opcode{program.OpcodeEqual, program.OpcodeNot, program.OpcodeAll, program.OpcodeNot, program.OpcodeAll, program.OpcodeAny},
		Fields:                  []schema.FieldID{1, 0, 0, 0, 0, 0},
		Values:                  make([]schema.ValueID, 6),
		ListStarts:              make([]uint32, 6),
		ListCounts:              make([]uint16, 6),
		OperandStarts:           []uint32{0, 0, 1, 2, 3, 4},
		OperandCounts:           []uint16{0, 1, 1, 1, 1, 1},
		EvidenceKinds:           make([]schema.EvidenceKindID, 6),
		EvidenceStates:          make([]schema.EvidenceStateID, 6),
		RootFlags:               make([]program.RootFlags, 6),
		InstructionNodes:        []schema.NodeID{1, 2, 3, 4, 5, 6},
		InstructionSourceStarts: []uint32{10, 20, 30, 40, 50, 60},
		InstructionSourceEnds:   []uint32{11, 21, 31, 41, 51, 61},
		Operands:                []schema.InstructionID{1, 1, 3, 2, 1},
		NodeInstructionStarts:   []uint32{0, 1, 2, 3, 4, 5},
		NodeInstructionCounts:   []uint16{1, 1, 1, 1, 1, 1},
		NodeInstructionIDs:      []schema.InstructionID{1, 2, 3, 4, 5, 6},
	}
}

func TestGroupedScheduleRejectsCycle(t *testing.T) {
	p := program.Program{
		Opcodes:                 []program.Opcode{program.OpcodeNot, program.OpcodeNot},
		Fields:                  make([]schema.FieldID, 2),
		Values:                  make([]schema.ValueID, 2),
		ListStarts:              make([]uint32, 2),
		ListCounts:              make([]uint16, 2),
		OperandStarts:           []uint32{0, 1},
		OperandCounts:           []uint16{1, 1},
		EvidenceKinds:           make([]schema.EvidenceKindID, 2),
		EvidenceStates:          make([]schema.EvidenceStateID, 2),
		RootFlags:               make([]program.RootFlags, 2),
		InstructionNodes:        []schema.NodeID{1, 2},
		InstructionSourceStarts: make([]uint32, 2),
		InstructionSourceEnds:   make([]uint32, 2),
		Operands:                []schema.InstructionID{2, 1},
	}
	var lowerer Lowerer
	if err := lowerer.scheduleInstructions(&p); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("cycle error = %v, want ErrInvalidDocument", err)
	}
}

func TestGroupedScheduleDeterministicRunsAndRemaps(t *testing.T) {
	got := groupedScheduleFixture()
	var lowerer Lowerer
	if err := lowerer.scheduleInstructions(&got); err != nil {
		t.Fatalf("scheduleInstructions: %v", err)
	}

	wantOpcodes := []program.Opcode{
		program.OpcodeEqual,
		program.OpcodeAll,
		program.OpcodeAny,
		program.OpcodeNot,
		program.OpcodeNot,
		program.OpcodeAll,
	}
	if !reflect.DeepEqual(got.Opcodes, wantOpcodes) {
		t.Fatalf("scheduled opcodes = %v, want %v", got.Opcodes, wantOpcodes)
	}
	if !reflect.DeepEqual(got.InstructionNodes, []schema.NodeID{1, 3, 6, 2, 4, 5}) {
		t.Fatalf("scheduled old-ID order = %v, want [1 3 6 2 4 5]", got.InstructionNodes)
	}
	if !reflect.DeepEqual(got.OpcodeRunOpcodes, []program.Opcode{
		program.OpcodeEqual, program.OpcodeAll, program.OpcodeAny,
		program.OpcodeNot, program.OpcodeAll,
	}) {
		t.Fatalf("run opcodes = %v", got.OpcodeRunOpcodes)
	}
	if !reflect.DeepEqual(got.OpcodeRunStarts, []uint32{0, 1, 2, 3, 5}) ||
		!reflect.DeepEqual(got.OpcodeRunCounts, []uint32{1, 1, 1, 2, 1}) {
		t.Fatalf("runs = starts %v counts %v", got.OpcodeRunStarts, got.OpcodeRunCounts)
	}

	wantOperands := [][]schema.InstructionID{{}, {1}, {1}, {1}, {2}, {4}}
	for row, want := range wantOperands {
		start := got.OperandStarts[row]
		count := got.OperandCounts[row]
		actual := got.Operands[start : start+uint32(count)]
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("instruction %d operands = %v, want %v", row+1, actual, want)
		}
		for _, operand := range actual {
			if operand >= schema.InstructionID(row+1) {
				t.Fatalf("instruction %d has non-topological operand %d", row+1, operand)
			}
		}
	}
	if !reflect.DeepEqual(got.NodeInstructionIDs, []schema.InstructionID{1, 4, 2, 5, 6, 3}) {
		t.Fatalf("source-map IDs = %v, want [1 4 2 5 6 3]", got.NodeInstructionIDs)
	}

	want := got
	if err := lowerer.scheduleInstructions(&got); err != nil {
		t.Fatalf("repeat scheduleInstructions: %v", err)
	}
	if !reflect.DeepEqual(got.Opcodes, want.Opcodes) ||
		!reflect.DeepEqual(got.Operands, want.Operands) ||
		!reflect.DeepEqual(got.InstructionNodes, want.InstructionNodes) ||
		!reflect.DeepEqual(got.NodeInstructionIDs, want.NodeInstructionIDs) ||
		!reflect.DeepEqual(got.OpcodeRunOpcodes, want.OpcodeRunOpcodes) ||
		!reflect.DeepEqual(got.OpcodeRunStarts, want.OpcodeRunStarts) ||
		!reflect.DeepEqual(got.OpcodeRunCounts, want.OpcodeRunCounts) {
		t.Fatal("repeated scheduling changed deterministic output")
	}
}
