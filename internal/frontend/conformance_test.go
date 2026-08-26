package frontend

import (
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/frontend/cedar"
	"github.com/sebishogun/nornrune/frontend/cel"
	"github.com/sebishogun/nornrune/frontend/rego"
	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

type conformanceInput struct {
	team         string
	count        int64
	enabled      bool
	teamPresent  bool
	countPresent bool
	boolPresent  bool
	want         schema.OutcomeID
}

type conformanceSources struct {
	cel   string
	rego  string
	cedar string
}

func TestCompatibilityFrontendsProduceEquivalentDecisions(t *testing.T) {
	tests := []struct {
		name    string
		sources conformanceSources
		inputs  []conformanceInput
	}{
		{name: "boolean", sources: expressions("enabled", "input.enabled", "context.enabled"), inputs: []conformanceInput{
			{enabled: true, boolPresent: true, want: 1}, {boolPresent: true, want: 2},
		}},
		{name: "integer", sources: expressions("count >= 2", "input.count >= 2", "context.count >= 2"), inputs: []conformanceInput{
			{count: 3, countPresent: true, want: 1}, {count: 1, countPresent: true, want: 2},
		}},
		{name: "string", sources: expressions(`team == "blue"`, `input.team == "blue"`, `context.team == "blue"`), inputs: []conformanceInput{
			{team: "blue", teamPresent: true, want: 1}, {team: "red", teamPresent: true, want: 2},
		}},
		{name: "conjunction", sources: expressions(`team == "blue" && count >= 2`, `input.team == "blue"; input.count >= 2`, `context.team == "blue" && context.count >= 2`), inputs: []conformanceInput{
			{team: "blue", teamPresent: true, count: 3, countPresent: true, want: 1},
			{team: "blue", teamPresent: true, count: 1, countPresent: true, want: 2},
		}},
		{name: "disjunction", sources: conformanceSources{
			cel:   `team == "blue" || count >= 2`,
			rego:  "package nornrune\nallow if { input.team == \"blue\" }\nallow if { input.count >= 2 }",
			cedar: `permit(principal, action, resource) when { context.team == "blue" || context.count >= 2 };`,
		}, inputs: []conformanceInput{
			{team: "blue", teamPresent: true, count: 1, countPresent: true, want: 1},
			{team: "red", teamPresent: true, count: 3, countPresent: true, want: 1},
			{team: "red", teamPresent: true, count: 1, countPresent: true, want: 2},
		}},
		{name: "negation", sources: expressions("!enabled", "not input.enabled", "!context.enabled"), inputs: []conformanceInput{
			{boolPresent: true, want: 1}, {enabled: true, boolPresent: true, want: 2},
		}},
		{name: "missing", sources: expressions(`team == "blue"`, `input.team == "blue"`, `context.team == "blue"`), inputs: []conformanceInput{
			{want: 4},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var reference []schema.OutcomeID
			for _, language := range []public.Language{public.LanguageCEL, public.LanguageRego, public.LanguageCedar} {
				compiled := compileConformancePolicy(t, language, test.sources)
				got := evaluateConformancePolicy(t, compiled, test.inputs)
				if reference == nil {
					reference = append(reference, got...)
				} else if !reflect.DeepEqual(got, reference) {
					t.Errorf("%s outcomes = %v, want %v", language, got, reference)
				}
				for row := range got {
					if got[row] != test.inputs[row].want {
						t.Errorf("%s outcome[%d] = %d, want %d", language, row, got[row], test.inputs[row].want)
					}
				}
			}
		})
	}
}

func TestCompatibilityFrontendDiagnosticsAreStable(t *testing.T) {
	tests := []struct {
		language public.Language
		sources  conformanceSources
	}{
		{language: public.LanguageCEL, sources: conformanceSources{cel: `request.unknown == "x"`}},
		{language: public.LanguageRego, sources: conformanceSources{rego: "package nornrune\nallow if { input.unknown }"}},
		{language: public.LanguageCedar, sources: conformanceSources{cedar: "permit(principal, action, resource) when { context.unknown };"}},
	}
	for _, test := range tests {
		t.Run(test.language.String(), func(t *testing.T) {
			first := conformanceDiagnostics(test.language, test.sources)
			second := conformanceDiagnostics(test.language, test.sources)
			if len(first) == 0 || !reflect.DeepEqual(first, second) {
				t.Fatalf("diagnostics are not stable: first=%+v second=%+v", first, second)
			}
			if first[0].Language != test.language || !first[0].Code.Valid() {
				t.Fatalf("diagnostic = %+v, want a valid %s diagnostic", first[0], test.language)
			}
		})
	}
}

func expressions(celExpression, regoExpression, cedarExpression string) conformanceSources {
	return conformanceSources{
		cel:   celExpression,
		rego:  "package nornrune\nallow if { " + regoExpression + " }",
		cedar: "permit(principal, action, resource) when { " + cedarExpression + " };",
	}
}

func conformanceBindings(language public.Language) public.BindingSet {
	prefix := ""
	if language == public.LanguageRego {
		prefix = "input."
	} else if language == public.LanguageCedar {
		prefix = "context."
	}
	bindings := public.BindingSet{Name: "compatibility", Version: "v1"}
	if language == public.LanguageRego {
		bindings.Decision = "allow"
	}
	bindings.Fields = []public.Binding{
		{Source: prefix + "team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
		{Source: prefix + "count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
		{Source: prefix + "enabled", Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
	}
	return bindings
}

func compileConformancePolicy(t testing.TB, language public.Language, sources conformanceSources) *program.Program {
	t.Helper()
	policy, diagnostics := conformancePolicy(language, sources)
	if len(diagnostics) != 0 || policy == nil {
		t.Fatalf("%s frontend diagnostics = %+v", language, diagnostics)
	}
	compiled, diagnostics, err := Compile(policy)
	if err != nil || len(diagnostics) != 0 || compiled == nil {
		t.Fatalf("%s shared Compile = (%v, %+v, %v)", language, compiled, diagnostics, err)
	}
	return compiled
}

func conformanceDiagnostics(language public.Language, sources conformanceSources) []public.Diagnostic {
	_, diagnostics := conformancePolicy(language, sources)
	return diagnostics
}

func conformancePolicy(language public.Language, sources conformanceSources) (*public.Policy, []public.Diagnostic) {
	bindings := conformanceBindings(language)
	limits := public.DefaultLimits()
	switch language {
	case public.LanguageCEL:
		return cel.Compile([]byte(sources.cel), bindings, limits)
	case public.LanguageRego:
		return rego.Compile([]byte(sources.rego), bindings, limits)
	case public.LanguageCedar:
		return cedar.Compile([]byte(sources.cedar), bindings, limits)
	default:
		return nil, []public.Diagnostic{{Language: language, Code: public.CodeInvalidPolicy}}
	}
}

func evaluateConformancePolicy(t *testing.T, compiled *program.Program, inputs []conformanceInput) []schema.OutcomeID {
	t.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, uint32(len(inputs)), 0, 0); err != nil {
		t.Fatal(err)
	}
	for row := range inputs {
		input := &inputs[row]
		if err := builder.SetRequestID(uint32(row), schema.RequestID(row+1)); err != nil {
			t.Fatal(err)
		}
		if input.teamPresent {
			value, err := builder.InternSymbol([]byte(input.team))
			if err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(uint32(row), 1, value); err != nil {
				t.Fatal(err)
			}
		}
		if input.countPresent {
			if err := builder.SetInteger(uint32(row), 2, input.count); err != nil {
				t.Fatal(err)
			}
		}
		if input.boolPresent {
			if err := builder.SetBoolean(uint32(row), 3, input.enabled); err != nil {
				t.Fatal(err)
			}
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var results result.Batch
	if err := executor.Execute(&results, compiled, batch); err != nil {
		t.Fatal(err)
	}
	return results.OutcomeIDs
}
