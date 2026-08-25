package frontend

import (
	"reflect"
	"testing"

	public "github.com/sebishogun/verifoxx/frontend"
)

func TestCompiledProgramUsesExactCapacityAndSourceSpans(t *testing.T) {
	policy := testPolicy(t, public.DefaultEscalate)
	compiled, diagnostics, err := Compile(policy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("Compile = diagnostics %+v, error %v", diagnostics, err)
	}
	value := reflect.ValueOf(compiled).Elem()
	typeOfProgram := value.Type()
	for row := 0; row < value.NumField(); row++ {
		field := value.Field(row)
		if field.Kind() == reflect.Slice && field.Len() != field.Cap() {
			t.Errorf("%s len/cap = %d/%d", typeOfProgram.Field(row).Name, field.Len(), field.Cap())
		}
	}
	for row := range policy.NodeKinds {
		start := compiled.NodeInstructionStarts[row]
		count := compiled.NodeInstructionCounts[row]
		if count != 1 || int(start) >= len(compiled.NodeInstructionIDs) {
			t.Fatalf("node %d instruction range = (%d,%d)", row+1, start, count)
		}
		instruction := compiled.NodeInstructionIDs[start]
		instructionRow := instruction - 1
		if compiled.InstructionSourceStarts[instructionRow] != policy.NodeSourceStarts[row] ||
			compiled.InstructionSourceEnds[instructionRow] != policy.NodeSourceEnds[row] {
			t.Fatalf("node %d span = [%d,%d), want [%d,%d)", row+1,
				compiled.InstructionSourceStarts[instructionRow], compiled.InstructionSourceEnds[instructionRow],
				policy.NodeSourceStarts[row], policy.NodeSourceEnds[row])
		}
	}
}
