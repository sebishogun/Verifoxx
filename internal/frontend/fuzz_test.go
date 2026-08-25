package frontend

import (
	"testing"

	public "github.com/sebishogun/verifoxx/frontend"
	"github.com/sebishogun/verifoxx/internal/program"
)

func FuzzSemanticPolicy(f *testing.F) {
	for _, seed := range [][]byte{
		nil,
		{1, 0},
		{2, 255},
		{3, 255},
		{4, 255},
		{5, 255},
		{6, 255},
		{7, 255},
		{8, 255},
		{9, 255},
		{10, 255},
		{11, 255},
		{12, 255},
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			return
		}
		policy := testPolicy(t, public.DefaultEscalate)
		mutateFuzzPolicy(policy, data)
		var compiler Compiler
		destination := program.Program{InputBytes: []byte("unchanged"), ProgramSymbolCount: 37}
		diagnostics, err := compiler.Compile(&destination, policy)
		if err != nil {
			t.Fatalf("validated semantic policy reached an invalid lowerer state: %v", err)
		}
		if len(diagnostics) != 0 {
			if string(destination.InputBytes) != "unchanged" || destination.ProgramSymbolCount != 37 {
				t.Fatal("malformed semantic policy partially mutated destination")
			}
			return
		}
		if string(destination.InputBytes) != string(policy.Source) || destination.InstructionCount() == 0 {
			t.Fatalf("valid semantic policy did not produce an owned Program: source=%q instructions=%d", destination.InputBytes, destination.InstructionCount())
		}
	})
}

func mutateFuzzPolicy(policy *public.Policy, data []byte) {
	if len(data) == 0 {
		return
	}
	value := byte(0)
	if len(data) > 1 {
		value = data[1]
	}
	row := int(value) % len(policy.NodeKinds)
	switch data[0] % 13 {
	case 0:
		policy.Root = public.NodeID(value)
	case 1:
		policy.NodeKinds[row] = public.NodeKind(value)
	case 2:
		policy.NodeOps[row] = public.CompareOp(value)
	case 3:
		policy.NodeFields[row] = public.FieldID(value)
	case 4:
		policy.NodeLiterals[row] = public.LiteralID(value)
	case 5:
		policy.NodeChildStarts[row] = uint32(value)
		policy.NodeChildCounts[row] = uint16(value)
	case 6:
		policy.NodeListStarts[row] = uint32(value)
		policy.NodeListCounts[row] = uint16(value)
	case 7:
		policy.NodeSourceStarts[row] = uint32(value)
		policy.NodeSourceEnds[row] = uint32(value >> 1)
	case 8:
		policy.LiteralKinds[0] = public.ValueKind(value)
		policy.LiteralRefs[0] = uint32(value)
	case 9:
		policy.BooleanValues = append(policy.BooleanValues, value)
	case 10:
		policy.Default = public.DefaultDecision(value)
	case 11:
		policy.Name = append(policy.Name[:0], data[1:]...)
	case 12:
		policy.NodeOps = policy.NodeOps[:row]
	}
}
