package compile

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
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
