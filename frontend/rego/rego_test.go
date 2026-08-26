package rego

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"

	opaast "github.com/open-policy-agent/opa/v1/ast"
	oparego "github.com/open-policy-agent/opa/v1/rego"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/eval"
	internalfrontend "github.com/sebishogun/nornrune/internal/frontend"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func regoBindings() public.BindingSet {
	return public.BindingSet{
		Name: "rego-policy", Version: "v1", Decision: "allow",
		Fields: []public.Binding{
			{Source: "input.team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "input.count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: "input.enabled", Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
		},
	}
}

func TestCompileLowersCompleteBooleanDecisionRules(t *testing.T) {
	source := []byte(`package nornrune

allow if {
	input.team == "blue"
	input.count >= 2
	input.enabled
}

allow if {
	input.team in {"green", "teal"}
}`)
	policy := requirePolicy(t, source, regoBindings(), public.DefaultLimits())
	if !bytes.Equal(policy.Source, source) || string(policy.Name) != "rego-policy" || string(policy.Version) != "v1" {
		t.Fatalf("policy identity = (%q,%q,%q)", policy.Source, policy.Name, policy.Version)
	}
	if policy.NodeKinds[policy.Root-1] != public.NodeKindAny {
		t.Fatalf("root kind = %v, want any", policy.NodeKinds[policy.Root-1])
	}
	if !hasComparison(policy, 1, public.CompareOpEqual) || !hasComparison(policy, 2, public.CompareOpGreaterEqual) ||
		!hasComparison(policy, 3, public.CompareOpEqual) || !hasComparison(policy, 1, public.CompareOpIn) {
		t.Fatalf("missing lowered atoms: kinds=%v fields=%v ops=%v", policy.NodeKinds, policy.NodeFields, policy.NodeOps)
	}
	if got := evaluatePolicy(t, policy, map[string]any{"team": "blue", "count": int64(3), "enabled": true}); got != 1 {
		t.Fatalf("first rule outcome = %d, want Approve", got)
	}
	if got := evaluatePolicy(t, policy, map[string]any{"team": "green", "count": int64(0), "enabled": false}); got != 1 {
		t.Fatalf("second rule outcome = %d, want Approve", got)
	}
	if got := evaluatePolicy(t, policy, map[string]any{"team": "red", "count": int64(3), "enabled": true}); got != 2 {
		t.Fatalf("false outcome = %d, want Reject", got)
	}
}

func TestCompileHandlesBooleanAtomsComparisonsAndMembership(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		kind      public.NodeKind
		field     public.FieldID
		operation public.CompareOp
	}{
		{name: "true", body: "true", kind: public.NodeKindBoolean},
		{name: "false", body: "false", kind: public.NodeKindBoolean},
		{name: "boolean shorthand", body: "input.enabled", kind: public.NodeKindCompare, field: 3, operation: public.CompareOpEqual},
		{name: "reversed less", body: "2 < input.count", kind: public.NodeKindCompare, field: 2, operation: public.CompareOpGreater},
		{name: "reversed less equal", body: "2 <= input.count", kind: public.NodeKindCompare, field: 2, operation: public.CompareOpGreaterEqual},
		{name: "not equal", body: `input.team != "red"`, kind: public.NodeKindCompare, field: 1, operation: public.CompareOpNotEqual},
		{name: "array membership", body: `input.team in ["blue", "green"]`, kind: public.NodeKindCompare, field: 1, operation: public.CompareOpIn},
		{name: "set membership", body: `input.team in {"blue", "green"}`, kind: public.NodeKindCompare, field: 1, operation: public.CompareOpIn},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("package nornrune\n\nallow if { " + test.body + " }")
			policy := requirePolicy(t, source, regoBindings(), public.DefaultLimits())
			row := policy.Root - 1
			if policy.NodeKinds[row] != test.kind {
				t.Fatalf("root kind = %v, want %v", policy.NodeKinds[row], test.kind)
			}
			if test.field != 0 && (policy.NodeFields[row] != test.field || policy.NodeOps[row] != test.operation) {
				t.Fatalf("root atom = field %d op %v, want field %d op %v", policy.NodeFields[row], policy.NodeOps[row], test.field, test.operation)
			}
		})
	}
}

func TestCompileNegatesConstantsWithoutDefinednessExpansion(t *testing.T) {
	policy := requirePolicy(t, []byte("package nornrune\nallow if { not false }"), regoBindings(), public.DefaultLimits())
	if policy.NodeKinds[policy.Root-1] != public.NodeKindNot {
		t.Fatalf("root kind = %v, want not", policy.NodeKinds[policy.Root-1])
	}
	for _, kind := range policy.NodeKinds {
		if kind == public.NodeKindDefined {
			t.Fatalf("constant negation contains Defined: %v", policy.NodeKinds)
		}
	}
}

func TestCompileMapsRegoDefaults(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantDefault public.DefaultDecision
		wantBoolean *bool
	}{
		{name: "no default", source: "package nornrune\nallow if { input.enabled }", wantDefault: public.DefaultEscalate},
		{name: "false default", source: "package nornrune\ndefault allow := false\nallow if { input.enabled }", wantDefault: public.DefaultReject},
		{name: "true default", source: "package nornrune\ndefault allow := true\nallow if { input.enabled }", wantDefault: public.DefaultEscalate, wantBoolean: boolPointer(true)},
		{name: "default only false", source: "package nornrune\ndefault allow := false", wantDefault: public.DefaultReject, wantBoolean: boolPointer(false)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := requirePolicy(t, []byte(test.source), regoBindings(), public.DefaultLimits())
			if policy.Default != test.wantDefault {
				t.Fatalf("default = %v, want %v", policy.Default, test.wantDefault)
			}
			if test.wantBoolean != nil {
				row := policy.Root - 1
				if policy.NodeKinds[row] != public.NodeKindBoolean || policy.BooleanValues[policy.LiteralRefs[policy.NodeLiterals[row]-1]] != boolByte(*test.wantBoolean) {
					t.Fatalf("root is not Boolean %v", *test.wantBoolean)
				}
			}
		})
	}
	policy, diagnostics := Compile([]byte("package nornrune"), regoBindings(), public.DefaultLimits())
	if policy != nil || !hasDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("empty decision = (%v,%+v), want invalid policy", policy, diagnostics)
	}
}

func TestNegationMatchesOPAForPresentAndMissingFields(t *testing.T) {
	source := []byte("package nornrune\n\nallow if { not input.enabled }")
	policy := requirePolicy(t, source, regoBindings(), public.DefaultLimits())
	foundDefined := false
	for _, kind := range policy.NodeKinds {
		foundDefined = foundDefined || kind == public.NodeKindDefined
	}
	if !foundDefined {
		t.Fatalf("negation tree lacks Defined: %v", policy.NodeKinds)
	}
	tests := []struct {
		name      string
		input     map[string]any
		wantFound bool
		wantOPA   bool
		want      schema.OutcomeID
	}{
		{name: "true", input: map[string]any{"enabled": true}, wantFound: false, want: 2},
		{name: "false", input: map[string]any{"enabled": false}, wantFound: true, wantOPA: true, want: 1},
		{name: "missing", input: map[string]any{}, wantFound: true, wantOPA: true, want: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, found := evaluateOPA(t, source, test.input)
			if found != test.wantFound || value != test.wantOPA {
				t.Fatalf("OPA = (%v,%v), want (%v,%v)", value, found, test.wantOPA, test.wantFound)
			}
			if got := evaluatePolicy(t, policy, test.input); got != test.want {
				t.Fatalf("NornRune outcome = %d, want %d", got, test.want)
			}
		})
	}
}

func TestParseAndLowerAreSeparateAndOwned(t *testing.T) {
	original := []byte("package nornrune\nallow if { input.team == \"blue\" }")
	source := bytes.Clone(original)
	bindings := regoBindings()
	parsed, diagnostics := Parse(source, bindings, public.DefaultLimits())
	if parsed == nil || len(diagnostics) != 0 {
		t.Fatalf("Parse = (%v,%+v)", parsed, diagnostics)
	}
	source[0] = 'X'
	bindings.Fields[0].Source = "input.changed"
	policy, diagnostics := Lower(original, parsed, regoBindings(), public.DefaultLimits())
	if policy == nil || len(diagnostics) != 0 || !bytes.Equal(policy.Source, original) {
		t.Fatalf("owned Lower = (%v,%+v)", policy, diagnostics)
	}
	if got, diagnostics := Lower(source, parsed, regoBindings(), public.DefaultLimits()); got != nil || !hasDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("mismatched Lower = (%v,%+v)", got, diagnostics)
	}
	if got, diagnostics := Lower(original, nil, regoBindings(), public.DefaultLimits()); got != nil || !hasDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("nil Lower = (%v,%+v)", got, diagnostics)
	}
}

func TestCompilePreservesOPAUnicodeByteSpans(t *testing.T) {
	source := []byte("package nornrune\n\nallow if { # café\n\tinput.team == \"café\"\n}")
	policy := requirePolicy(t, source, regoBindings(), public.DefaultLimits())
	found := false
	for row, kind := range policy.NodeKinds {
		if kind != public.NodeKindCompare {
			continue
		}
		start, end := policy.NodeSourceStarts[row], policy.NodeSourceEnds[row]
		if end <= uint32(len(source)) && string(source[start:end]) == `input.team == "café"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("exact comparison span absent: starts=%v ends=%v", policy.NodeSourceStarts, policy.NodeSourceEnds)
	}
}

func TestUnknownComparisonFieldDiagnosticUsesExactReferenceSpan(t *testing.T) {
	source := []byte("package nornrune\n\n# café\nallow if { input.unknown == \"blue\" }")
	policy, diagnostics := Compile(source, regoBindings(), public.DefaultLimits())
	if policy != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeUnknownField {
		t.Fatalf("Compile = (%v,%+v), want one unknown-field diagnostic", policy, diagnostics)
	}
	span := diagnostics[0].Span
	if span.End > uint32(len(source)) || string(source[span.Start:span.End]) != "input.unknown" {
		t.Fatalf("diagnostic span = [%d,%d) %q, want input.unknown", span.Start, span.End, source[span.Start:span.End])
	}
}

func TestCompileRejectsUnsupportedRego(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   public.DiagnosticCode
	}{
		{name: "syntax", source: "package nornrune\nallow if {", code: public.CodeSyntax},
		{name: "import", source: "package nornrune\nimport data.foo\nallow if { true }", code: public.CodeUnsupported},
		{name: "data", source: "package nornrune\nallow if { data.foo }", code: public.CodeUnsupported},
		{name: "function", source: "package nornrune\nallow(x) if { true }", code: public.CodeUnsupported},
		{name: "else", source: "package nornrune\nallow := true if { true } else := true if { true }", code: public.CodeUnsupported},
		{name: "recursion", source: "package nornrune\nallow if { allow }", code: public.CodeUnsupported},
		{name: "comprehension", source: "package nornrune\nallow if { [x | x := 1] }", code: public.CodeUnsupported},
		{name: "variable assignment", source: "package nornrune\nallow if { x := input.count; x > 1 }", code: public.CodeUnsupported},
		{name: "unification", source: "package nornrune\nallow if { input.count = 1 }", code: public.CodeUnsupported},
		{name: "partial document", source: "package nornrune\nallow contains input.team if { true }", code: public.CodeUnsupported},
		{name: "non boolean head", source: "package nornrune\nallow := \"yes\"", code: public.CodeType},
		{name: "with", source: "package nornrune\nallow if { input.enabled with input.enabled as true }", code: public.CodeUnsupported},
		{name: "builtin", source: "package nornrune\nallow if { startswith(input.team, \"b\") }", code: public.CodeUnsupported},
		{name: "field to field", source: "package nornrune\nallow if { input.team == input.team }", code: public.CodeUnsupported},
		{name: "unrelated rule", source: "package nornrune\ndeny if { true }\nallow if { true }", code: public.CodeUnsupported},
		{name: "duplicate default", source: "package nornrune\ndefault allow := false\ndefault allow := false", code: public.CodeDuplicate},
		{name: "unknown input", source: "package nornrune\nallow if { input.unknown }", code: public.CodeUnknownField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := Compile([]byte(test.source), regoBindings(), public.DefaultLimits())
			if policy != nil || !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("Compile = (%v,%+v), want %v", policy, diagnostics, test.code)
			}
			if !diagnosticsSorted(diagnostics) {
				t.Fatalf("diagnostics are not sorted: %+v", diagnostics)
			}
		})
	}
}

func TestParseRejectsInvalidBindingsAndMalformedSource(t *testing.T) {
	tests := []struct {
		name     string
		source   []byte
		bindings public.BindingSet
		code     public.DiagnosticCode
	}{
		{name: "invalid utf8", source: []byte{0xff}, bindings: regoBindings(), code: public.CodeSyntax},
		{name: "missing decision", source: []byte("package nornrune"), bindings: func() public.BindingSet { b := regoBindings(); b.Decision = ""; return b }(), code: public.CodeInvalidBinding},
		{name: "non input binding", source: []byte("package nornrune"), bindings: func() public.BindingSet { b := regoBindings(); b.Fields[0].Source = "team"; return b }(), code: public.CodeInvalidBinding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, diagnostics := Parse(test.source, test.bindings, public.DefaultLimits())
			if parsed != nil || !hasDiagnostic(diagnostics, test.code) {
				t.Fatalf("Parse = (%v,%+v), want %v", parsed, diagnostics, test.code)
			}
		})
	}
}

func TestCompileEnforcesSharedLimits(t *testing.T) {
	base := public.DefaultLimits()
	tests := []struct {
		name   string
		source string
		limits func(public.Limits) public.Limits
	}{
		{name: "source", source: "package nornrune\nallow if { true }", limits: func(l public.Limits) public.Limits { l.MaxSourceBytes = 5; return l }},
		{name: "fields", source: "package nornrune\nallow if { true }", limits: func(l public.Limits) public.Limits { l.MaxFields = 2; return l }},
		{name: "binding strings", source: "package nornrune\nallow if { true }", limits: func(l public.Limits) public.Limits { l.MaxStringBytes = 1; return l }},
		{name: "nodes", source: "package nornrune\nallow if { input.enabled; true }", limits: func(l public.Limits) public.Limits { l.MaxNodes = 1; return l }},
		{name: "depth", source: "package nornrune\nallow if { not input.enabled }", limits: func(l public.Limits) public.Limits { l.MaxDepth = 2; return l }},
		{name: "literals", source: "package nornrune\nallow if { input.count == 1; input.count == 2 }", limits: func(l public.Limits) public.Limits { l.MaxLiterals = 1; return l }},
		{name: "children", source: "package nornrune\nallow if { input.enabled; true }", limits: func(l public.Limits) public.Limits { l.MaxChildren = 1; return l }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := Compile([]byte(test.source), regoBindings(), test.limits(base))
			if policy != nil || !hasDiagnostic(diagnostics, public.CodeLimit) {
				t.Fatalf("Compile = (%v,%+v), want limit", policy, diagnostics)
			}
		})
	}
}

func TestCapabilitiesAreStableAndCallerOwned(t *testing.T) {
	want := []public.Capability{
		{Name: "rego_v1_modules", Support: public.SupportSupported},
		{Name: "complete_boolean_decisions", Support: public.SupportSupported},
		{Name: "boolean_defaults", Support: public.SupportSupported},
		{Name: "multiple_rules", Support: public.SupportSupported},
		{Name: "conjunctive_bodies", Support: public.SupportSupported},
		{Name: "static_input_references", Support: public.SupportRestricted},
		{Name: "scalar_comparisons", Support: public.SupportRestricted},
		{Name: "constant_membership", Support: public.SupportRestricted},
		{Name: "presence_aware_negation", Support: public.SupportRestricted},
		{Name: "imports_and_data", Support: public.SupportRejected},
		{Name: "functions_and_recursion", Support: public.SupportRejected},
		{Name: "variables_and_comprehensions", Support: public.SupportRejected},
		{Name: "with_and_unsupported_builtins", Support: public.SupportRejected},
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

func TestDifferentialEvaluationMatchesOfficialRego(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		input     map[string]any
		wantOPA   bool
		wantFound bool
		want      schema.OutcomeID
	}{
		{name: "true comparison", source: `allow if { input.team == "blue" }`, input: map[string]any{"team": "blue"}, wantOPA: true, wantFound: true, want: 1},
		{name: "false comparison", source: `allow if { input.team == "blue" }`, input: map[string]any{"team": "red"}, want: 2},
		{name: "missing comparison", source: `allow if { input.team == "blue" }`, input: map[string]any{}, want: 4},
		{name: "false default", source: "default allow := false\nallow if { input.enabled }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "true default", source: "default allow := true\nallow if { input.enabled }", input: map[string]any{"enabled": false}, wantOPA: true, wantFound: true, want: 1},
		{name: "multiple rules", source: `allow if { input.team == "blue" }
allow if { input.team == "green" }`, input: map[string]any{"team": "green"}, wantOPA: true, wantFound: true, want: 1},
		{name: "array membership", source: `allow if { input.team in ["blue", "green"] }`, input: map[string]any{"team": "green"}, wantOPA: true, wantFound: true, want: 1},
		{name: "set membership false", source: `allow if { input.team in {"blue", "green"} }`, input: map[string]any{"team": "red"}, want: 2},
		{name: "missing negation", source: "allow if { not input.enabled }", input: map[string]any{}, wantOPA: true, wantFound: true, want: 1},
		{name: "missing negated equality", source: `allow if { not input.team == "blue" }`, input: map[string]any{}, wantOPA: true, wantFound: true, want: 1},
		{name: "present unequal negated equality", source: `allow if { not input.team == "blue" }`, input: map[string]any{"team": "red"}, wantOPA: true, wantFound: true, want: 1},
		{name: "present equal negated equality", source: `allow if { not input.team == "blue" }`, input: map[string]any{"team": "blue"}, want: 2},
		{name: "reversed present unequal negated equality", source: `allow if { not "blue" == input.team }`, input: map[string]any{"team": "red"}, wantOPA: true, wantFound: true, want: 1},
		{name: "missing negated inequality", source: `allow if { not input.team != "red" }`, input: map[string]any{}, want: 4},
		{name: "missing negated ordered", source: "allow if { not input.count >= 2 }", input: map[string]any{}, want: 4},
		{name: "missing negated membership", source: `allow if { not input.team in {"blue"} }`, input: map[string]any{}, want: 4},
		{name: "missing negated shorthand false default", source: "default allow := false\nallow if { not input.enabled }", input: map[string]any{}, wantOPA: true, wantFound: true, want: 1},
		{name: "missing negated equality false default", source: "default allow := false\nallow if { not input.team == \"blue\" }", input: map[string]any{}, wantOPA: true, wantFound: true, want: 1},
		{name: "missing negated inequality false default", source: "default allow := false\nallow if { not input.team != \"red\" }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "missing negated less false default", source: "default allow := false\nallow if { not input.count < 2 }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "missing negated less equal false default", source: "default allow := false\nallow if { not input.count <= 2 }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "missing negated greater false default", source: "default allow := false\nallow if { not input.count > 2 }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "missing negated greater equal false default", source: "default allow := false\nallow if { not 2 <= input.count }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "missing negated array membership false default", source: "default allow := false\nallow if { not input.team in [\"blue\"] }", input: map[string]any{}, wantFound: true, want: 2},
		{name: "missing negated set membership false default", source: "default allow := false\nallow if { not input.team in {\"blue\"} }", input: map[string]any{}, wantFound: true, want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte("package nornrune\n" + test.source)
			value, found := evaluateOPA(t, source, test.input)
			if value != test.wantOPA || found != test.wantFound {
				t.Fatalf("OPA = (%v,%v), want (%v,%v)", value, found, test.wantOPA, test.wantFound)
			}
			policy := requirePolicy(t, source, regoBindings(), public.DefaultLimits())
			if got := evaluatePolicy(t, policy, test.input); got != test.want {
				t.Fatalf("NornRune outcome = %d, want %d", got, test.want)
			}
		})
	}
}

func TestFrontendFixtures(t *testing.T) {
	for _, name := range []string{"allow.rego", "default.rego"} {
		source, err := os.ReadFile("../../testdata/frontends/rego/" + name)
		if err != nil {
			t.Fatal(err)
		}
		requirePolicy(t, source, regoBindings(), public.DefaultLimits())
	}
	source, err := os.ReadFile("../../testdata/frontends/rego/unsupported.rego")
	if err != nil {
		t.Fatal(err)
	}
	if policy, diagnostics := Compile(source, regoBindings(), public.DefaultLimits()); policy != nil || !hasDiagnostic(diagnostics, public.CodeUnsupported) {
		t.Fatalf("unsupported fixture = (%v,%+v)", policy, diagnostics)
	}
}

func requirePolicy(t *testing.T, source []byte, bindings public.BindingSet, limits public.Limits) *public.Policy {
	t.Helper()
	policy, diagnostics := Compile(source, bindings, limits)
	if policy == nil || len(diagnostics) != 0 {
		t.Fatalf("Compile(%q) = (%v,%+v)", source, policy, diagnostics)
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
		if diagnostic.Code == code && diagnostic.Language == public.LanguageRego {
			return true
		}
	}
	return false
}

func diagnosticsSorted(diagnostics []public.Diagnostic) bool {
	for row := 1; row < len(diagnostics); row++ {
		left, right := diagnostics[row-1], diagnostics[row]
		if left.Span.Start > right.Span.Start || (left.Span.Start == right.Span.Start && left.Span.End > right.Span.End) ||
			(left.Span == right.Span && left.Code > right.Code) {
			return false
		}
	}
	return true
}

func evaluatePolicy(t *testing.T, policy *public.Policy, input map[string]any) schema.OutcomeID {
	t.Helper()
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("shared Compile = (%v,%+v)", err, diagnostics)
	}
	var builder eval.Builder
	if err := builder.Begin(compiled, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if value, ok := input["team"]; ok {
		symbol, err := builder.InternSymbol([]byte(value.(string)))
		if err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, 1, symbol); err != nil {
			t.Fatal(err)
		}
	}
	if value, ok := input["count"]; ok {
		if err := builder.SetInteger(0, 2, value.(int64)); err != nil {
			t.Fatal(err)
		}
	}
	if value, ok := input["enabled"]; ok {
		if err := builder.SetBoolean(0, 3, value.(bool)); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var got result.Batch
	if err := executor.Execute(&got, compiled, batch); err != nil {
		t.Fatal(err)
	}
	return got.OutcomeIDs[0]
}

func evaluateOPA(t *testing.T, source []byte, input map[string]any) (bool, bool) {
	t.Helper()
	results, err := oparego.New(
		oparego.Query("data.nornrune.allow"),
		oparego.Module("policy.rego", string(source)),
		oparego.Input(input),
		oparego.SetRegoVersion(opaast.RegoV1),
	).Eval(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		return false, false
	}
	value, ok := results[0].Expressions[0].Value.(bool)
	if !ok {
		t.Fatalf("OPA result = %#v, want bool", results)
	}
	return value, true
}

func boolPointer(value bool) *bool { return &value }

func boolByte(value bool) uint8 {
	if value {
		return 1
	}
	return 0
}
