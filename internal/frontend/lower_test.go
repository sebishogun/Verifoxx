package frontend

import (
	"bytes"
	"crypto/sha256"
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func testPolicy(t *testing.T, decision public.DefaultDecision) *public.Policy {
	t.Helper()
	source := []byte(`team == "blue" && count > 2`)
	bindings := public.BindingSet{
		Name: "compatibility-policy", Version: "v1",
		Fields: []public.Binding{
			{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
		},
	}
	builder, err := public.NewBuilder(source, bindings, public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	team, err := builder.AddCompare(1, public.CompareOpEqual, public.StringLiteral([]byte("blue")), public.Span{Start: 0, End: 14})
	if err != nil {
		t.Fatal(err)
	}
	count, err := builder.AddCompare(2, public.CompareOpGreater, public.IntegerLiteral(2), public.Span{Start: 18, End: uint32(len(source))})
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddAll([]public.NodeID{team, count}, public.Span{End: uint32(len(source))})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := builder.Finish(root, decision)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestCompileLowersOwnedPolicyAndFixedSemantics(t *testing.T) {
	policy := testPolicy(t, public.DefaultEscalate)
	wantSource := append([]byte(nil), policy.Source...)

	compiled, diagnostics, err := Compile(policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	if compiled == nil {
		t.Fatal("Compile returned a nil Program")
	}
	if !bytes.Equal(compiled.InputBytes, wantSource) || compiled.ContentHash != sha256.Sum256(wantSource) {
		t.Fatalf("source/hash were not retained exactly: source=%q hash=%x", compiled.InputBytes, compiled.ContentHash)
	}
	if got := programSymbol(t, compiled, compiled.PolicyName); got != "compatibility-policy" {
		t.Fatalf("policy name = %q", got)
	}
	if got := programSymbol(t, compiled, compiled.PolicyVersion); got != "v1" {
		t.Fatalf("policy version = %q", got)
	}

	wantFields := []struct {
		name  string
		kind  schema.ValueKind
		group schema.FieldGroup
	}{
		{"subject.team", schema.ValueKindSymbol, schema.FieldGroupSubject},
		{"context.count", schema.ValueKindInteger, schema.FieldGroupContext},
	}
	if len(compiled.FieldNames) != len(wantFields) {
		t.Fatalf("field count = %d, want %d", len(compiled.FieldNames), len(wantFields))
	}
	for row, want := range wantFields {
		if got := programSymbol(t, compiled, compiled.FieldNames[row]); got != want.name ||
			compiled.FieldKinds[row] != want.kind || compiled.FieldGroups[row] != want.group {
			t.Fatalf("field[%d] = (%q,%v,%v), want (%q,%v,%v)", row, got, compiled.FieldKinds[row], compiled.FieldGroups[row], want.name, want.kind, want.group)
		}
	}
	if !containsSymbolValue(compiled, []byte("blue")) || !containsIntegerValue(compiled, 2) {
		t.Fatalf("source literals missing: kinds=%v symbols=%q integers=%v", compiled.ValueKinds, compiled.SymbolBytes, compiled.IntegerValues)
	}

	wantOutcomes := []struct {
		name       string
		precedence uint8
		terminal   bool
	}{
		{"Approve", 1, true},
		{"Reject", 4, true},
		{"Revise", 2, false},
		{"Escalate", 3, true},
	}
	if len(compiled.Outcomes.Names) != len(wantOutcomes) {
		t.Fatalf("outcomes = %d, want %d", len(compiled.Outcomes.Names), len(wantOutcomes))
	}
	for row, want := range wantOutcomes {
		if got := programSymbol(t, compiled, compiled.Outcomes.Names[row]); got != want.name ||
			compiled.Outcomes.Precedence[row] != want.precedence || compiled.Outcomes.Terminal[row] != want.terminal {
			t.Fatalf("outcome[%d] = (%q,%d,%t), want (%q,%d,%t)", row, got, compiled.Outcomes.Precedence[row], compiled.Outcomes.Terminal[row], want.name, want.precedence, want.terminal)
		}
	}
	if !reflect.DeepEqual(compiled.RequirementIDs, []schema.RequirementID{1}) ||
		!reflect.DeepEqual(compiled.RequirementClauseIDs, []schema.ClauseID{1}) ||
		len(compiled.ClauseAssertionRoots) != 1 {
		t.Fatalf("semantic shell = requirements %v clauses %v assertions %v", compiled.RequirementIDs, compiled.RequirementClauseIDs, compiled.ClauseAssertionRoots)
	}
	if got := compiled.Opcodes[compiled.RequirementRoots[0]-1]; got != program.OpcodeAny {
		t.Fatalf("applicability opcode = %v, want Any", got)
	}
	if got := compiled.Opcodes[compiled.ClauseAssertionRoots[0]-1]; got != program.OpcodeAll {
		t.Fatalf("assertion opcode = %v, want All", got)
	}
	if compiled.ClauseOnSatisfied[0] != 1 || compiled.ClauseOnFalse[0] != 2 {
		t.Fatalf("known resolution = (%d,%d), want (Approve,Reject)", compiled.ClauseOnSatisfied[0], compiled.ClauseOnFalse[0])
	}
	if len(compiled.Resolutions.OutcomeIDs) != truth.ReasonCount {
		t.Fatalf("unresolved rows = %d, want %d", len(compiled.Resolutions.OutcomeIDs), truth.ReasonCount)
	}
	for row, outcome := range compiled.Resolutions.OutcomeIDs {
		if outcome != 4 {
			t.Fatalf("unresolved outcome[%d] = %d, want Escalate", row, outcome)
		}
	}
	if len(compiled.ClauseExplanationIDs) != 7 || len(compiled.ExplanationRationaleTemplateIDs) != 2 {
		t.Fatalf("explanation columns are incomplete: clause=%v rationale=%v", compiled.ClauseExplanationIDs, compiled.ExplanationRationaleTemplateIDs)
	}

	policy.Source[0] = 'X'
	policy.Name[0] = 'X'
	policy.Version[0] = 'X'
	policy.FieldBytes[policy.FieldTargetStarts[0]] = 'X'
	policy.SymbolBytes[0] = 'X'
	if !bytes.Equal(compiled.InputBytes, wantSource) || programSymbol(t, compiled, compiled.PolicyName) != "compatibility-policy" ||
		programSymbol(t, compiled, compiled.FieldNames[0]) != "subject.team" || !containsSymbolValue(compiled, []byte("blue")) {
		t.Fatal("compiled Program aliases source Policy storage")
	}
}

func TestCompilerIsReusableAndFailuresLeaveDestinationUnchanged(t *testing.T) {
	var compiler Compiler
	var destination program.Program
	firstPolicy := testPolicy(t, public.DefaultEscalate)
	diagnostics, err := compiler.Compile(&destination, firstPolicy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("first Compile = diagnostics %+v, error %v", diagnostics, err)
	}
	first, err := program.Freeze(&destination)
	if err != nil {
		t.Fatal(err)
	}

	invalid := testPolicy(t, public.DefaultReject)
	invalid.Root = 0
	diagnostics, err = compiler.Compile(&destination, invalid)
	if err != nil {
		t.Fatalf("semantic failure returned infrastructure error: %v", err)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != public.CodeInvalidPolicy {
		t.Fatalf("invalid diagnostics = %+v", diagnostics)
	}
	if !reflect.DeepEqual(destination, first) {
		t.Fatal("failed Compile mutated destination")
	}

	secondPolicy := testPolicy(t, public.DefaultReject)
	diagnostics, err = compiler.Compile(&destination, secondPolicy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("second Compile = diagnostics %+v, error %v", diagnostics, err)
	}
	for row, outcome := range destination.Resolutions.OutcomeIDs {
		if outcome != 2 {
			t.Fatalf("second unresolved outcome[%d] = %d, want Reject", row, outcome)
		}
	}
	if !bytes.Equal(first.InputBytes, []byte(`team == "blue" && count > 2`)) || programSymbol(t, &first, first.PolicyName) != "compatibility-policy" {
		t.Fatal("reusing Compiler mutated the previously published Program")
	}
}

func TestCompiledPolicyResolvesTrueFalseAndMissing(t *testing.T) {
	tests := []struct {
		name     string
		decision public.DefaultDecision
		team     []byte
		present  bool
		want     schema.OutcomeID
	}{
		{"true", public.DefaultEscalate, []byte("blue"), true, 1},
		{"false", public.DefaultEscalate, []byte("red"), true, 2},
		{"missing escalates", public.DefaultEscalate, nil, false, 4},
		{"missing rejects", public.DefaultReject, nil, false, 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled, diagnostics, err := Compile(testPolicy(t, test.decision))
			if err != nil || len(diagnostics) != 0 {
				t.Fatalf("Compile = diagnostics %+v, error %v", diagnostics, err)
			}
			var batchBuilder eval.Builder
			if err := batchBuilder.Begin(compiled, 1, 0, 0); err != nil {
				t.Fatal(err)
			}
			if err := batchBuilder.SetRequestID(0, 1); err != nil {
				t.Fatal(err)
			}
			if test.present {
				team, err := batchBuilder.InternSymbol(test.team)
				if err != nil {
					t.Fatal(err)
				}
				if err := batchBuilder.SetSymbol(0, 1, team); err != nil {
					t.Fatal(err)
				}
			}
			if err := batchBuilder.SetInteger(0, 2, 3); err != nil {
				t.Fatal(err)
			}
			batch, err := batchBuilder.Finish()
			if err != nil {
				t.Fatal(err)
			}
			var executor eval.Executor
			var got result.Batch
			if err := executor.Execute(&got, compiled, batch); err != nil {
				t.Fatal(err)
			}
			if len(got.OutcomeIDs) != 1 || got.OutcomeIDs[0] != test.want {
				t.Fatalf("outcomes = %v, want [%d]", got.OutcomeIDs, test.want)
			}
		})
	}
}

func TestCompileLowersDefinedAndEvaluatesPresence(t *testing.T) {
	compiled, diagnostics, err := Compile(definedPolicy(t))
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("Compile = diagnostics %+v, error %v", diagnostics, err)
	}
	root := compiled.ClauseAssertionRoots[0]
	if compiled.Opcodes[root-1] != program.OpcodeDefined || compiled.Fields[root-1] != 1 || compiled.Values[root-1] != 0 {
		t.Fatalf("defined instruction = opcode %v field %d value %d", compiled.Opcodes[root-1], compiled.Fields[root-1], compiled.Values[root-1])
	}

	for _, test := range []struct {
		name    string
		present bool
		want    schema.OutcomeID
	}{
		{name: "present false value", present: true, want: 1},
		{name: "missing", want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var batchBuilder eval.Builder
			if err := batchBuilder.Begin(compiled, 1, 0, 0); err != nil {
				t.Fatal(err)
			}
			if err := batchBuilder.SetRequestID(0, 1); err != nil {
				t.Fatal(err)
			}
			if test.present {
				if err := batchBuilder.SetBoolean(0, 1, false); err != nil {
					t.Fatal(err)
				}
			}
			batch, err := batchBuilder.Finish()
			if err != nil {
				t.Fatal(err)
			}
			var executor eval.Executor
			var got result.Batch
			if err := executor.Execute(&got, compiled, batch); err != nil {
				t.Fatal(err)
			}
			if len(got.OutcomeIDs) != 1 || got.OutcomeIDs[0] != test.want {
				t.Fatalf("outcomes = %v, want [%d]", got.OutcomeIDs, test.want)
			}
		})
	}
}

func programSymbol(t *testing.T, compiled *program.Program, id schema.SymbolID) string {
	t.Helper()
	value, ok := compiled.Symbol(id)
	if !ok {
		t.Fatalf("symbol %d is invalid", id)
	}
	return string(value)
}

func containsSymbolValue(compiled *program.Program, want []byte) bool {
	for row, kind := range compiled.ValueKinds {
		if kind != schema.ValueKindSymbol {
			continue
		}
		ref := schema.SymbolID(compiled.ValueRefs[row])
		value, ok := compiled.Symbol(ref)
		if ok && bytes.Equal(value, want) {
			return true
		}
	}
	return false
}

func containsIntegerValue(compiled *program.Program, want int64) bool {
	for row, kind := range compiled.ValueKinds {
		if kind != schema.ValueKindInteger {
			continue
		}
		ref := compiled.ValueRefs[row]
		if ref != 0 && int(ref) <= len(compiled.IntegerValues) && compiled.IntegerValues[ref-1] == want {
			return true
		}
	}
	return false
}
