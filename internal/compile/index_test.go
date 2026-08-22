package compile

import (
	"errors"
	"math"
	"slices"
	"testing"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func applicabilityIndexProgram() program.Program {
	p := slotTestProgram(
		[]program.Opcode{program.OpcodeEqual, program.OpcodeIn, program.OpcodeEqual, program.OpcodeAll},
		[][]schema.InstructionID{nil, nil, nil, {1, 2, 3}},
		[]program.RootFlags{0, 0, 0, program.RootApplicability},
	)
	p.Fields = []schema.FieldID{1, 2, 3, 0}
	p.Values = []schema.ValueID{1, 0, 4, 0}
	p.ListStarts[1] = 0
	p.ListCounts[1] = 2
	p.ListValues = []schema.ValueID{2, 3}
	p.FieldKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindSymbol,
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
	}
	p.ValueKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindSymbol,
		schema.ValueKindSymbol,
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
	}
	p.ValueRefs = []uint32{20, 30, 31, 10, 1}
	p.ProgramSymbolCount = 31
	p.RequirementIDs = []schema.RequirementID{1}
	p.RequirementRoots = []schema.InstructionID{4}
	return p
}

func constraintsEqual(a, b policyindex.Constraints) bool {
	return slices.Equal(a.Rows, b.Rows) &&
		slices.Equal(a.Fields, b.Fields) &&
		slices.Equal(a.ValueStarts, b.ValueStarts) &&
		slices.Equal(a.ValueCounts, b.ValueCounts) &&
		slices.Equal(a.Values, b.Values)
}

func TestApplicabilityIndexExtractPositiveAll(t *testing.T) {
	p := applicabilityIndexProgram()
	var lowerer Lowerer
	got, err := lowerer.extractApplicabilityConstraints(&p)
	if err != nil {
		t.Fatalf("extractApplicabilityConstraints: %v", err)
	}
	want := policyindex.Constraints{
		Rows:        []uint32{0, 0, 0},
		Fields:      []schema.FieldID{1, 2, 3},
		ValueStarts: []uint32{0, 1, 3},
		ValueCounts: []uint32{1, 2, 1},
		Values:      []schema.SymbolID{20, 30, 31, 10},
	}
	if !constraintsEqual(got, want) {
		t.Fatalf("constraints = %+v, want %+v", got, want)
	}
}

func TestApplicabilityIndexExtractConservative(t *testing.T) {
	tests := []struct {
		name    string
		program func() program.Program
		want    policyindex.Constraints
	}{
		{
			name: "any remains wildcard",
			program: func() program.Program {
				p := applicabilityIndexProgram()
				p.Opcodes[3] = program.OpcodeAny
				return p
			},
		},
		{
			name: "not remains wildcard",
			program: func() program.Program {
				p := slotTestProgram(
					[]program.Opcode{program.OpcodeEqual, program.OpcodeNot},
					[][]schema.InstructionID{nil, {1}},
					[]program.RootFlags{0, program.RootApplicability},
				)
				p.Fields[0], p.Values[0] = 1, 1
				p.FieldKinds = []schema.ValueKind{schema.ValueKindSymbol}
				p.ValueKinds = []schema.ValueKind{schema.ValueKindSymbol}
				p.ValueRefs = []uint32{20}
				p.ProgramSymbolCount = 20
				p.RequirementIDs = []schema.RequirementID{1}
				p.RequirementRoots = []schema.InstructionID{2}
				return p
			},
		},
		{
			name: "not equal remains wildcard",
			program: func() program.Program {
				p := applicabilityIndexProgram()
				p.Opcodes = p.Opcodes[:1]
				p.Fields = p.Fields[:1]
				p.Values = p.Values[:1]
				p.ListStarts = p.ListStarts[:1]
				p.ListCounts = p.ListCounts[:1]
				p.OperandStarts = p.OperandStarts[:1]
				p.OperandCounts = p.OperandCounts[:1]
				p.EvidenceKinds = p.EvidenceKinds[:1]
				p.EvidenceStates = p.EvidenceStates[:1]
				p.RootFlags = []program.RootFlags{program.RootApplicability}
				p.InstructionNodes = p.InstructionNodes[:1]
				p.InstructionSourceStarts = p.InstructionSourceStarts[:1]
				p.InstructionSourceEnds = p.InstructionSourceEnds[:1]
				p.Opcodes[0] = program.OpcodeNotEqual
				p.RequirementRoots[0] = 1
				return p
			},
		},
		{
			name: "non-symbol field remains wildcard",
			program: func() program.Program {
				p := applicabilityIndexProgram()
				p.Fields[0] = 4
				p.Values[0] = 5
				p.RequirementRoots[0] = 1
				p.RootFlags = []program.RootFlags{program.RootApplicability, 0, 0, 0}
				return p
			},
		},
		{
			name: "duplicate field constraints remain wildcard",
			program: func() program.Program {
				p := applicabilityIndexProgram()
				p.Fields[1] = 1
				p.Fields[2] = 1
				return p
			},
		},
		{
			name: "shared CSE leaf visited once",
			program: func() program.Program {
				p := slotTestProgram(
					[]program.Opcode{program.OpcodeEqual, program.OpcodeAll},
					[][]schema.InstructionID{nil, {1, 1}},
					[]program.RootFlags{0, program.RootApplicability},
				)
				p.Fields[0], p.Values[0] = 1, 1
				p.FieldKinds = []schema.ValueKind{schema.ValueKindSymbol}
				p.ValueKinds = []schema.ValueKind{schema.ValueKindSymbol}
				p.ValueRefs = []uint32{20}
				p.ProgramSymbolCount = 20
				p.RequirementIDs = []schema.RequirementID{1}
				p.RequirementRoots = []schema.InstructionID{2}
				return p
			},
			want: policyindex.Constraints{
				Rows:        []uint32{0},
				Fields:      []schema.FieldID{1},
				ValueStarts: []uint32{0},
				ValueCounts: []uint32{1},
				Values:      []schema.SymbolID{20},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.program()
			var lowerer Lowerer
			got, err := lowerer.extractApplicabilityConstraints(&p)
			if err != nil {
				t.Fatalf("extractApplicabilityConstraints: %v", err)
			}
			if !constraintsEqual(got, tt.want) {
				t.Fatalf("constraints = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestApplicabilityIndexExtractRejectsMalformedProgram(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*program.Program)
	}{
		{"zero requirement root", func(p *program.Program) { p.RequirementRoots[0] = 0 }},
		{"misaligned instruction columns", func(p *program.Program) { p.Fields = p.Fields[:3] }},
		{"bad value ID", func(p *program.Program) { p.Values[0] = 99 }},
		{"bad list range", func(p *program.Program) { p.ListStarts[1] = math.MaxUint32 }},
		{"zero symbol ref", func(p *program.Program) { p.ValueRefs[0] = 0 }},
		{"symbol ref out of range", func(p *program.Program) { p.ValueRefs[0] = 32 }},
		{"field out of range", func(p *program.Program) { p.Fields[0] = 5 }},
		{"misaligned requirements", func(p *program.Program) { p.RequirementIDs = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := applicabilityIndexProgram()
			tt.mutate(&p)
			var lowerer Lowerer
			if _, err := lowerer.extractApplicabilityConstraints(&p); !errors.Is(err, ErrInvalidGeneratedProgram) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidGeneratedProgram)
			}
		})
	}
}
