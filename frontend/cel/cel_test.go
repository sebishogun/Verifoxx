package cel

import (
	"bytes"
	"reflect"
	"testing"

	celgo "cel.dev/cel-go/cel"
	celtypes "cel.dev/cel-go/common/types"

	public "github.com/sebishogun/verifoxx/frontend"
	"github.com/sebishogun/verifoxx/internal/eval"
	internalfrontend "github.com/sebishogun/verifoxx/internal/frontend"
	coreprogram "github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func scalarBindings() public.BindingSet {
	return public.BindingSet{
		Name: "cel-policy", Version: "v1",
		Fields: []public.Binding{
			{Source: "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: "enabled", Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
			{Source: "request.team", Target: "subject.selected_team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
		},
	}
}

func TestCompileLowersSupportedCELSubset(t *testing.T) {
	source := []byte(`team == "blue" && count >= 2 && enabled && request.team in ["blue", "green"]`)
	policy := requirePolicy(t, source, scalarBindings(), public.DefaultLimits())
	if !bytes.Equal(policy.Source, source) || string(policy.Name) != "cel-policy" || string(policy.Version) != "v1" {
		t.Fatalf("policy identity = (%q,%q,%q)", policy.Source, policy.Name, policy.Version)
	}
	if policy.Root == 0 || policy.NodeKinds[policy.Root-1] != public.NodeKindAll {
		t.Fatalf("root = %d kind %v, want all", policy.Root, policy.NodeKinds[policy.Root-1])
	}
	wantOperations := []struct {
		field public.FieldID
		op    public.CompareOp
	}{
		{1, public.CompareOpEqual},
		{2, public.CompareOpGreaterEqual},
		{3, public.CompareOpEqual},
		{4, public.CompareOpIn},
	}
	for _, want := range wantOperations {
		if !hasComparison(policy, want.field, want.op) {
			t.Errorf("missing field %d operation %v: fields=%v ops=%v", want.field, want.op, policy.NodeFields, policy.NodeOps)
		}
	}
	if policy.Default != public.DefaultEscalate {
		t.Fatalf("default = %v, want escalate", policy.Default)
	}
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(diagnostics) != 0 || compiled == nil {
		t.Fatalf("shared Compile = (%v, %+v, %v)", compiled, diagnostics, err)
	}
}

func TestCompileHandlesBooleanConstantsAndReversedComparisons(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		rootKind  public.NodeKind
		field     public.FieldID
		operation public.CompareOp
	}{
		{name: "true", source: "true", rootKind: public.NodeKindBoolean},
		{name: "false", source: "false", rootKind: public.NodeKindBoolean},
		{name: "boolean shorthand", source: "enabled", rootKind: public.NodeKindCompare, field: 3, operation: public.CompareOpEqual},
		{name: "not", source: "!enabled", rootKind: public.NodeKindNot},
		{name: "reversed less", source: "2 < count", rootKind: public.NodeKindCompare, field: 2, operation: public.CompareOpGreater},
		{name: "reversed less equal", source: "2 <= count", rootKind: public.NodeKindCompare, field: 2, operation: public.CompareOpGreaterEqual},
		{name: "reversed greater", source: "2 > count", rootKind: public.NodeKindCompare, field: 2, operation: public.CompareOpLess},
		{name: "reversed greater equal", source: "2 >= count", rootKind: public.NodeKindCompare, field: 2, operation: public.CompareOpLessEqual},
		{name: "not equal", source: `team != "red"`, rootKind: public.NodeKindCompare, field: 1, operation: public.CompareOpNotEqual},
		{name: "membership", source: `team in ["blue", "red"]`, rootKind: public.NodeKindCompare, field: 1, operation: public.CompareOpIn},
		{name: "all", source: "enabled && true", rootKind: public.NodeKindAll},
		{name: "any", source: "enabled || false", rootKind: public.NodeKindAny},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := requirePolicy(t, []byte(test.source), scalarBindings(), public.DefaultLimits())
			row := policy.Root - 1
			if got := policy.NodeKinds[row]; got != test.rootKind {
				t.Fatalf("root kind = %v, want %v", got, test.rootKind)
			}
			if test.field != 0 && (policy.NodeFields[row] != test.field || policy.NodeOps[row] != test.operation) {
				t.Fatalf("comparison = field %d op %v, want field %d op %v", policy.NodeFields[row], policy.NodeOps[row], test.field, test.operation)
			}
		})
	}
}

func TestCompileLowersSelectedBooleanShorthand(t *testing.T) {
	bindings := public.BindingSet{
		Name: "selection", Version: "v1",
		Fields: []public.Binding{{
			Source: "request.enabled", Target: "context.enabled",
			Kind: public.ValueKindBoolean, Group: public.FieldGroupContext,
		}},
	}
	policy := requirePolicy(t, []byte("request.enabled"), bindings, public.DefaultLimits())
	row := policy.Root - 1
	if policy.NodeKinds[row] != public.NodeKindCompare || policy.NodeFields[row] != 1 || policy.NodeOps[row] != public.CompareOpEqual {
		t.Fatalf("selected shorthand = kind %v field %d op %v", policy.NodeKinds[row], policy.NodeFields[row], policy.NodeOps[row])
	}
}

func TestSelectionSpanSkipsCommentsAfterDot(t *testing.T) {
	bindings := public.BindingSet{
		Name: "selection", Version: "v1",
		Fields: []public.Binding{{
			Source: "request.enabled", Target: "context.enabled",
			Kind: public.ValueKindBoolean, Group: public.FieldGroupContext,
		}},
	}
	source := []byte("request. // field follows\n enabled")
	policy := requirePolicy(t, source, bindings, public.DefaultLimits())
	row := policy.Root - 1
	if policy.NodeSourceStarts[row] != 0 || policy.NodeSourceEnds[row] != uint32(len(source)) {
		t.Fatalf("selection span = [%d,%d), want [0,%d)", policy.NodeSourceStarts[row], policy.NodeSourceEnds[row], len(source))
	}
}

func TestCompilePreservesExactUnicodeByteSpans(t *testing.T) {
	bindings := public.BindingSet{
		Name: "unicode", Version: "v1",
		Fields: []public.Binding{{Source: "name", Target: "subject.name", Kind: public.ValueKindString, Group: public.FieldGroupSubject}},
	}
	source := []byte("// café\nname == \"café\"")
	policy := requirePolicy(t, source, bindings, public.DefaultLimits())
	row := policy.Root - 1
	wantStart := uint32(len("// café\n"))
	if policy.NodeSourceStarts[row] != wantStart || policy.NodeSourceEnds[row] != uint32(len(source)) {
		t.Fatalf("root span = [%d,%d), want [%d,%d)", policy.NodeSourceStarts[row], policy.NodeSourceEnds[row], wantStart, len(source))
	}
}

func TestMembershipSpanSkipsDelimitersInsideComments(t *testing.T) {
	source := []byte("team in [\"blue\" // ] is not the list end\n]")
	policy := requirePolicy(t, source, scalarBindings(), public.DefaultLimits())
	row := policy.Root - 1
	if policy.NodeSourceStarts[row] != 0 || policy.NodeSourceEnds[row] != uint32(len(source)) {
		t.Fatalf("membership span = [%d,%d), want [0,%d)", policy.NodeSourceStarts[row], policy.NodeSourceEnds[row], len(source))
	}
}

func TestUnsupportedCallDiagnosticUsesExactUnicodeByteSpan(t *testing.T) {
	source := []byte("// café\nteam.startsWith(\"b\")")
	policy, diagnostics := Compile(source, scalarBindings(), public.DefaultLimits())
	if policy != nil || len(diagnostics) != 1 {
		t.Fatalf("Compile = (%v, %+v), want one diagnostic", policy, diagnostics)
	}
	span := diagnostics[0].Span
	if span.End > uint32(len(source)) || string(source[span.Start:span.End]) != "startsWith" {
		t.Fatalf("diagnostic span = [%d,%d) %q, want startsWith", span.Start, span.End, source[span.Start:span.End])
	}
}

func TestUnknownSelectionDiagnosticUsesExactExpressionSpan(t *testing.T) {
	source := []byte("// café\nrequest.unknown == \"blue\"")
	policy, diagnostics := Compile(source, scalarBindings(), public.DefaultLimits())
	if policy != nil || len(diagnostics) != 1 {
		t.Fatalf("Compile = (%v, %+v), want one diagnostic", policy, diagnostics)
	}
	span := diagnostics[0].Span
	if span.End > uint32(len(source)) || string(source[span.Start:span.End]) != "request.unknown" {
		t.Fatalf("diagnostic span = [%d,%d) %q, want request.unknown", span.Start, span.End, source[span.Start:span.End])
	}
}

func TestParseAndLowerAreSeparateAndOwned(t *testing.T) {
	source := []byte(`team == "blue"`)
	bindings := scalarBindings()
	parsed, diagnostics := Parse(source, bindings, public.DefaultLimits())
	if parsed == nil || len(diagnostics) != 0 {
		t.Fatalf("Parse = (%v, %+v)", parsed, diagnostics)
	}
	policy, diagnostics := Lower(source, parsed, bindings, public.DefaultLimits())
	if policy == nil || len(diagnostics) != 0 {
		t.Fatalf("Lower = (%v, %+v)", policy, diagnostics)
	}
	mutated := append([]byte(nil), source...)
	mutated[0] = 'X'
	bindings.Fields[0].Source = "changed"
	if !bytes.Equal(policy.Source, source) || fieldName(policy, 0) != "team" {
		t.Fatal("published policy aliases caller source or bindings")
	}
	if got, diagnostics := Lower(mutated, parsed, scalarBindings(), public.DefaultLimits()); got != nil || !hasDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("mismatched Lower = (%v, %+v), want invalid policy", got, diagnostics)
	}
	if got, diagnostics := Lower(source, nil, scalarBindings(), public.DefaultLimits()); got != nil || !hasDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("nil Lower = (%v, %+v), want invalid policy", got, diagnostics)
	}
}

func TestCompileRejectsInvalidAndUnsupportedCEL(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   public.DiagnosticCode
	}{
		{name: "syntax", source: "team ==", code: public.CodeSyntax},
		{name: "type", source: "team == 1", code: public.CodeType},
		{name: "identifier named limit", source: "limit == 1", code: public.CodeType},
		{name: "non boolean result", source: "count", code: public.CodeType},
		{name: "unknown selection", source: `request.unknown == "blue"`, code: public.CodeUnknownField},
		{name: "member call", source: `team.startsWith("b")`, code: public.CodeUnsupported},
		{name: "global call", source: "size(team) > 0", code: public.CodeUnsupported},
		{name: "field to field", source: "team == request.team", code: public.CodeUnsupported},
		{name: "map", source: `{"a": 1} == {"a": 1}`, code: public.CodeUnsupported},
		{name: "comprehension macro", source: "[1].exists(x, x > 0)", code: public.CodeUnsupported},
		{name: "double", source: "1.0 == 1.0", code: public.CodeUnsupported},
		{name: "mixed membership", source: `team in ["blue", 1]`, code: public.CodeType},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := Compile([]byte(test.source), scalarBindings(), public.DefaultLimits())
			if policy != nil || !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("Compile = (%v, %+v), want %v", policy, diagnostics, test.code)
			}
			if !diagnosticsSorted(diagnostics) {
				t.Fatalf("diagnostics are not sorted: %+v", diagnostics)
			}
		})
	}
}

func TestParseRejectsReservedCELBindingName(t *testing.T) {
	bindings := public.BindingSet{
		Name: "reserved", Version: "v1",
		Fields: []public.Binding{{
			Source: "true", Target: "context.value",
			Kind: public.ValueKindBoolean, Group: public.FieldGroupContext,
		}},
	}
	parsed, diagnostics := Parse([]byte("true"), bindings, public.DefaultLimits())
	if parsed != nil || !hasDiagnostic(diagnostics, public.CodeInvalidBinding) {
		t.Fatalf("Parse = (%v, %+v), want invalid binding", parsed, diagnostics)
	}
}

func TestCompileEnforcesAllBounds(t *testing.T) {
	base := public.DefaultLimits()
	tests := []struct {
		name   string
		source string
		limits func(public.Limits) public.Limits
	}{
		{name: "source", source: "enabled", limits: func(l public.Limits) public.Limits { l.MaxSourceBytes = 3; return l }},
		{name: "fields", source: "enabled", limits: func(l public.Limits) public.Limits { l.MaxFields = 3; return l }},
		{name: "binding strings", source: "enabled", limits: func(l public.Limits) public.Limits { l.MaxStringBytes = 100; return l }},
		{name: "nodes", source: "enabled && true", limits: func(l public.Limits) public.Limits { l.MaxNodes = 1; return l }},
		{name: "depth", source: "enabled && true", limits: func(l public.Limits) public.Limits { l.MaxDepth = 1; return l }},
		{name: "literals", source: "enabled == true && enabled == false", limits: func(l public.Limits) public.Limits { l.MaxLiterals = 1; return l }},
		{name: "children", source: "enabled && true", limits: func(l public.Limits) public.Limits { l.MaxChildren = 1; return l }},
		{name: "strings", source: `team == "blue"`, limits: func(l public.Limits) public.Limits { l.MaxStringBytes = 104; return l }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := test.limits(base)
			policy, diagnostics := Compile([]byte(test.source), scalarBindings(), limits)
			if policy != nil || !hasDiagnostic(diagnostics, public.CodeLimit) {
				t.Fatalf("Compile = (%v, %+v), want limit diagnostic", policy, diagnostics)
			}
		})
	}

	limits := base
	limits.MaxDiagnostics = 1
	policy, diagnostics := Compile([]byte(`team.startsWith("b") && team.endsWith("e")`), scalarBindings(), limits)
	if policy != nil || len(diagnostics) != 1 {
		t.Fatalf("diagnostic bound = (%v, %+v), want one diagnostic", policy, diagnostics)
	}
}

func TestCapabilitiesAreStableAndCallerOwned(t *testing.T) {
	want := []public.Capability{
		{Name: "boolean_literals", Support: public.SupportSupported},
		{Name: "scalar_variables", Support: public.SupportSupported},
		{Name: "object_selection", Support: public.SupportRestricted},
		{Name: "scalar_comparisons", Support: public.SupportRestricted},
		{Name: "logical_operators", Support: public.SupportSupported},
		{Name: "constant_list_membership", Support: public.SupportRestricted},
		{Name: "function_calls", Support: public.SupportRejected},
		{Name: "macros_and_comprehensions", Support: public.SupportRejected},
		{Name: "maps_messages_and_optionals", Support: public.SupportRejected},
	}
	got := Capabilities()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Capabilities = %+v, want %+v", got, want)
	}
	got[0].Name = "mutated"
	if reflect.DeepEqual(Capabilities(), got) {
		t.Fatal("Capabilities returned mutable package storage")
	}
}

func TestDifferentialEvaluationMatchesOfficialCEL(t *testing.T) {
	source := []byte(`team == "blue" && count >= 2 && enabled && request.team == "blue"`)
	bindings := scalarBindings()
	policy := requirePolicy(t, source, bindings, public.DefaultLimits())
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("shared Compile = (%v, %+v)", err, diagnostics)
	}
	officialEnv, err := celgo.NewEnv(
		celgo.ClearMacros(),
		celgo.Variable("team", celgo.StringType),
		celgo.Variable("count", celgo.IntType),
		celgo.Variable("enabled", celgo.BoolType),
		celgo.Variable("request", celgo.MapType(celgo.StringType, celgo.DynType)),
	)
	if err != nil {
		t.Fatal(err)
	}
	officialAST, issues := officialEnv.Compile(string(source))
	if issues != nil && issues.Err() != nil {
		t.Fatal(issues.Err())
	}
	officialProgram, err := officialEnv.Program(officialAST)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		activation map[string]any
		want       schema.OutcomeID
	}{
		{name: "true", activation: map[string]any{"team": "blue", "count": int64(3), "enabled": true, "request": map[string]any{"team": "blue"}}, want: 1},
		{name: "false", activation: map[string]any{"team": "blue", "count": int64(3), "enabled": true, "request": map[string]any{"team": "red"}}, want: 2},
		{name: "missing", activation: map[string]any{"team": "blue", "count": int64(3), "enabled": true, "request": map[string]any{}}, want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, _, officialErr := officialProgram.Eval(test.activation)
			switch test.want {
			case 1:
				if officialErr != nil || value != celtypes.True {
					t.Fatalf("official CEL = (%v,%v), want true", value, officialErr)
				}
			case 2:
				if officialErr != nil || value != celtypes.False {
					t.Fatalf("official CEL = (%v,%v), want false", value, officialErr)
				}
			case 4:
				if officialErr == nil && !celtypes.IsUnknown(value) && !celtypes.IsError(value) {
					t.Fatalf("official CEL = (%v,%v), want missing error or unknown", value, officialErr)
				}
			}
			if got := evaluatePolicy(t, compiled, test.activation); got != test.want {
				t.Fatalf("Verifoxx outcome = %d, want %d", got, test.want)
			}
		})
	}
}

func requirePolicy(t *testing.T, source []byte, bindings public.BindingSet, limits public.Limits) *public.Policy {
	t.Helper()
	policy, diagnostics := Compile(source, bindings, limits)
	if policy == nil || len(diagnostics) != 0 {
		t.Fatalf("Compile(%q) = (%v, %+v)", source, policy, diagnostics)
	}
	return policy
}

func hasComparison(policy *public.Policy, field public.FieldID, operation public.CompareOp) bool {
	for row, kind := range policy.NodeKinds {
		if kind == public.NodeKindCompare && policy.NodeFields[row] == field && policy.NodeOps[row] == operation {
			return true
		}
	}
	return false
}

func hasDiagnostic(diagnostics []public.Diagnostic, code public.DiagnosticCode) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Language == public.LanguageCEL {
			return true
		}
	}
	return false
}

func diagnosticsSorted(diagnostics []public.Diagnostic) bool {
	for row := 1; row < len(diagnostics); row++ {
		left, right := diagnostics[row-1], diagnostics[row]
		if left.Span.Start > right.Span.Start ||
			(left.Span.Start == right.Span.Start && left.Span.End > right.Span.End) ||
			(left.Span == right.Span && left.Code > right.Code) {
			return false
		}
	}
	return true
}

func fieldName(policy *public.Policy, row int) string {
	start := policy.FieldNameStarts[row]
	return string(policy.FieldBytes[start : start+policy.FieldNameLengths[row]])
}

func evaluatePolicy(t *testing.T, compiled *coreprogram.Program, activation map[string]any) schema.OutcomeID {
	t.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if value, present := activation["team"]; present {
		symbol, err := builder.InternSymbol([]byte(value.(string)))
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, 1, symbol); err != nil {
			t.Fatal(err)
		}
	}
	if value, present := activation["count"]; present {
		if err := builder.SetInteger(0, 2, value.(int64)); err != nil {
			t.Fatal(err)
		}
	}
	if value, present := activation["enabled"]; present {
		if err := builder.SetBoolean(0, 3, value.(bool)); err != nil {
			t.Fatal(err)
		}
	}
	if value, present := activation["request"]; present {
		request := value.(map[string]any)
		if team, present := request["team"]; present {
			symbol, err := builder.InternSymbol([]byte(team.(string)))
			if err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(0, 4, symbol); err != nil {
				t.Fatal(err)
			}
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var outcomes result.Batch
	if err := executor.Execute(&outcomes, compiled, batch); err != nil {
		t.Fatal(err)
	}
	if len(outcomes.OutcomeIDs) != 1 {
		t.Fatalf("outcomes = %v", outcomes.OutcomeIDs)
	}
	return outcomes.OutcomeIDs[0]
}
