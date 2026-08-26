package cedar

import (
	"bytes"
	"os"
	"reflect"
	"testing"

	cedargo "github.com/cedar-policy/cedar-go"
	cedartypes "github.com/cedar-policy/cedar-go/types"

	public "github.com/sebishogun/verifoxx/frontend"
	"github.com/sebishogun/verifoxx/internal/eval"
	internalfrontend "github.com/sebishogun/verifoxx/internal/frontend"
	coreprogram "github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func cedarBindings() public.BindingSet {
	return public.BindingSet{
		Name: "cedar-policy", Version: "v1",
		Fields: []public.Binding{
			{Source: "principal", Target: "subject.principal", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: "action", Target: "action.name", Kind: public.ValueKindString, Group: public.FieldGroupAction},
			{Source: "resource", Target: "resource.id", Kind: public.ValueKindString, Group: public.FieldGroupResource},
			{Source: "context.team", Target: "context.team", Kind: public.ValueKindString, Group: public.FieldGroupContext},
			{Source: "context.count", Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: "context.enabled", Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
		},
	}
}

func TestCompileLowersCedarAuthorizationSemantics(t *testing.T) {
	source := []byte(`permit(
  principal == User::"alice",
  action == Action::"read",
  resource == Document::"report"
) when { context.team == "blue" && context.count >= 2 };
forbid(principal, action, resource)
unless { context.enabled };`)
	policy := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	if !bytes.Equal(policy.Source, source) || string(policy.Name) != "cedar-policy" || string(policy.Version) != "v1" {
		t.Fatalf("policy identity = (%q,%q,%q)", policy.Source, policy.Name, policy.Version)
	}
	if policy.Default != public.DefaultEscalate || policy.NodeKinds[policy.Root-1] != public.NodeKindAll {
		t.Fatalf("root = kind %v default %v, want all/escalate", policy.NodeKinds[policy.Root-1], policy.Default)
	}
	for _, want := range []struct {
		field public.FieldID
		op    public.CompareOp
	}{{1, public.CompareOpEqual}, {2, public.CompareOpEqual}, {3, public.CompareOpEqual}, {4, public.CompareOpEqual}, {5, public.CompareOpGreaterEqual}, {6, public.CompareOpEqual}} {
		if !hasCedarComparison(policy, want.field, want.op) {
			t.Errorf("missing field %d operation %v", want.field, want.op)
		}
	}
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(diagnostics) != 0 || compiled == nil {
		t.Fatalf("shared Compile = (%v, %+v, %v)", compiled, diagnostics, err)
	}
}

func TestCompileCombinesMultiplePoliciesWithForbidPrecedence(t *testing.T) {
	source := []byte(`permit(principal, action, resource) when { context.team == "blue" };
permit(principal, action, resource) when { context.count >= 2 };
forbid(principal, action, resource) when { !context.enabled };`)
	policy := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	root := policy.Root - 1
	if policy.NodeKinds[root] != public.NodeKindAll {
		t.Fatalf("root kind = %v, want all", policy.NodeKinds[root])
	}
	start := policy.NodeChildStarts[root]
	children := policy.ChildNodeIDs[start : start+uint32(policy.NodeChildCounts[root])]
	if len(children) != 2 || policy.NodeKinds[children[0]-1] != public.NodeKindAny || policy.NodeKinds[children[1]-1] != public.NodeKindNot {
		t.Fatalf("root children = %v kinds=%v", children, policy.NodeKinds)
	}
}

func TestCompileNoPermitIsStaticFalse(t *testing.T) {
	policy := requireCedarPolicy(t, []byte(`forbid(principal, action, resource);`), cedarBindings(), public.DefaultLimits())
	root := policy.Root - 1
	if policy.NodeKinds[root] != public.NodeKindAll && policy.NodeKinds[root] != public.NodeKindBoolean {
		t.Fatalf("root kind = %v, want a static rejecting expression", policy.NodeKinds[root])
	}
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("shared Compile = (%v, %+v)", err, diagnostics)
	}
	if got := evaluateCedarPolicy(t, compiled, nil); got != 2 {
		t.Fatalf("outcome = %d, want Reject", got)
	}
}

func TestCompilePreservesExactUnicodeByteSpans(t *testing.T) {
	source := []byte("// café\npermit(principal, action, resource) when { context.team == \"blå\" };")
	policy := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	found := false
	for row, field := range policy.NodeFields {
		if field != 4 {
			continue
		}
		span := public.Span{Start: policy.NodeSourceStarts[row], End: policy.NodeSourceEnds[row]}
		if got := string(source[span.Start:span.End]); got != `context.team == "blå"` {
			t.Fatalf("comparison span = [%d,%d) %q", span.Start, span.End, got)
		}
		found = true
	}
	if !found {
		t.Fatal("context.team comparison not found")
	}
}

func TestCompilePreservesExactScopeSpan(t *testing.T) {
	source := []byte(`permit(
  principal == User::"alice",
  action,
  resource
);`)
	policy := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	for row, field := range policy.NodeFields {
		if field != 1 {
			continue
		}
		span := public.Span{Start: policy.NodeSourceStarts[row], End: policy.NodeSourceEnds[row]}
		if got := string(source[span.Start:span.End]); got != `principal == User::"alice"` {
			t.Fatalf("scope span = [%d,%d) %q", span.Start, span.End, got)
		}
		return
	}
	t.Fatal("principal scope comparison not found")
}

func TestCompileAcceptsTrailingScopeComma(t *testing.T) {
	source := []byte(`permit(principal, action, resource,);`)
	requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
}

func TestCompileHandlesAllComparisonsAndReversedOperands(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		operation  public.CompareOp
	}{
		{name: "equal", expression: `context.count == 2`, operation: public.CompareOpEqual},
		{name: "not equal", expression: `context.count != 2`, operation: public.CompareOpNotEqual},
		{name: "less", expression: `context.count < 2`, operation: public.CompareOpLess},
		{name: "less equal", expression: `context.count <= 2`, operation: public.CompareOpLessEqual},
		{name: "greater", expression: `context.count > 2`, operation: public.CompareOpGreater},
		{name: "greater equal", expression: `context.count >= 2`, operation: public.CompareOpGreaterEqual},
		{name: "reversed less", expression: `2 < context.count`, operation: public.CompareOpGreater},
		{name: "reversed less equal", expression: `2 <= context.count`, operation: public.CompareOpGreaterEqual},
		{name: "reversed greater", expression: `2 > context.count`, operation: public.CompareOpLess},
		{name: "reversed greater equal", expression: `2 >= context.count`, operation: public.CompareOpLessEqual},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := []byte(`permit(principal, action, resource) when { ` + test.expression + ` };`)
			policy := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
			if !hasCedarComparison(policy, 5, test.operation) {
				t.Fatalf("missing operation %v: fields=%v ops=%v", test.operation, policy.NodeFields, policy.NodeOps)
			}
		})
	}
}

func TestUnlessNegatesCondition(t *testing.T) {
	source := []byte(`permit(principal, action, resource) unless { context.enabled };`)
	policy := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("shared Compile = (%v, %+v)", err, diagnostics)
	}
	if got := evaluateCedarPolicy(t, compiled, map[public.FieldID]any{6: false}); got != 1 {
		t.Fatalf("false condition outcome = %d, want Approve", got)
	}
	if got := evaluateCedarPolicy(t, compiled, map[public.FieldID]any{6: true}); got != 2 {
		t.Fatalf("true condition outcome = %d, want Reject", got)
	}
	if got := evaluateCedarPolicy(t, compiled, nil); got != 4 {
		t.Fatalf("missing condition outcome = %d, want Escalate", got)
	}
}

func TestParseAndLowerAreSeparateAndOwned(t *testing.T) {
	source := []byte(`permit(principal, action, resource) when { context.team == "blue" };`)
	bindings := cedarBindings()
	parsed, diagnostics := Parse(source, bindings, public.DefaultLimits())
	if parsed == nil || len(diagnostics) != 0 {
		t.Fatalf("Parse = (%v, %+v)", parsed, diagnostics)
	}
	policy, diagnostics := Lower(source, parsed, bindings, public.DefaultLimits())
	if policy == nil || len(diagnostics) != 0 {
		t.Fatalf("Lower = (%v, %+v)", policy, diagnostics)
	}
	mutated := bytes.Clone(source)
	mutated[0] = 'X'
	bindings.Fields[0].Source = "changed"
	if !bytes.Equal(policy.Source, source) || cedarFieldName(policy, 0) != "principal" {
		t.Fatal("published policy aliases caller source or bindings")
	}
	if got, diagnostics := Lower(mutated, parsed, cedarBindings(), public.DefaultLimits()); got != nil || !hasCedarDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("mismatched Lower = (%v, %+v), want invalid policy", got, diagnostics)
	}
	if got, diagnostics := Lower(source, nil, cedarBindings(), public.DefaultLimits()); got != nil || !hasCedarDiagnostic(diagnostics, public.CodeInvalidPolicy) {
		t.Fatalf("nil Lower = (%v, %+v), want invalid policy", got, diagnostics)
	}
}

func TestCompileRejectsUnsupportedCedar(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   public.DiagnosticCode
	}{
		{name: "syntax", source: `permit(principal, action, resource`, code: public.CodeSyntax},
		{name: "hierarchy scope", source: `permit(principal in Group::"ops", action, resource);`, code: public.CodeUnsupported},
		{name: "action set scope", source: `permit(principal, action in [Action::"read"], resource);`, code: public.CodeUnsupported},
		{name: "is scope", source: `permit(principal is User, action, resource);`, code: public.CodeUnsupported},
		{name: "entity attribute", source: `permit(principal, action, resource) when { principal.team == "blue" };`, code: public.CodeUnsupported},
		{name: "set", source: `permit(principal, action, resource) when { context.team in ["blue"] };`, code: public.CodeUnsupported},
		{name: "record", source: `permit(principal, action, resource) when { context.team == {"x": "blue"} };`, code: public.CodeUnsupported},
		{name: "extension", source: `permit(principal, action, resource) when { ip("127.0.0.1").isIpv4() };`, code: public.CodeUnsupported},
		{name: "annotation", source: `@id("one") permit(principal, action, resource);`, code: public.CodeUnsupported},
		{name: "arithmetic", source: `permit(principal, action, resource) when { context.count + 1 > 2 };`, code: public.CodeUnsupported},
		{name: "like", source: `permit(principal, action, resource) when { context.team like "b*" };`, code: public.CodeUnsupported},
		{name: "has", source: `permit(principal, action, resource) when { context has team };`, code: public.CodeUnsupported},
		{name: "tag", source: `permit(principal, action, resource) when { principal.hasTag("x") };`, code: public.CodeUnsupported},
		{name: "conditional", source: `permit(principal, action, resource) when { if context.enabled then true else false };`, code: public.CodeUnsupported},
		{name: "unknown context", source: `permit(principal, action, resource) when { context.unknown == "x" };`, code: public.CodeUnknownField},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := Compile([]byte(test.source), cedarBindings(), public.DefaultLimits())
			if policy != nil || !hasCedarDiagnostic(diagnostics, test.code) {
				t.Fatalf("Compile = (%v, %+v), want %v", policy, diagnostics, test.code)
			}
			if !cedarDiagnosticsSorted(diagnostics) {
				t.Fatalf("diagnostics are not sorted: %+v", diagnostics)
			}
		})
	}
}

func TestCompileRejectsMalformedUTF8AndInvalidBindings(t *testing.T) {
	policy, diagnostics := Compile([]byte{0xff}, cedarBindings(), public.DefaultLimits())
	if policy != nil || !hasCedarDiagnostic(diagnostics, public.CodeSyntax) || diagnostics[0].Span != (public.Span{End: 1}) {
		t.Fatalf("invalid UTF-8 Compile = (%v, %+v)", policy, diagnostics)
	}
	bindings := cedarBindings()
	bindings.Fields[1].Source = "principal"
	policy, diagnostics = Compile([]byte(`permit(principal, action, resource);`), bindings, public.DefaultLimits())
	if policy != nil || !hasCedarDiagnostic(diagnostics, public.CodeInvalidBinding) {
		t.Fatalf("invalid binding Compile = (%v, %+v)", policy, diagnostics)
	}
}

func TestCompileRejectsExcessiveNestingAsLimitBeforeOfficialParser(t *testing.T) {
	source := []byte(`permit(principal, action, resource) when { (((((true))))) };`)
	limits := public.DefaultLimits()
	limits.MaxDepth = 4
	policy, diagnostics := Compile(source, cedarBindings(), limits)
	if policy != nil || !hasCedarDiagnostic(diagnostics, public.CodeLimit) || hasCedarDiagnostic(diagnostics, public.CodeSyntax) {
		t.Fatalf("Compile = (%v, %+v), want limit", policy, diagnostics)
	}
}

func TestCompileRetainsSafeParserDepthWhenCallerRaisesSemanticLimit(t *testing.T) {
	const nesting = maxSafeParserDepth + 1
	source := make([]byte, 0, nesting*2+64)
	source = append(source, `permit(principal, action, resource) when { `...)
	source = append(source, bytes.Repeat([]byte{'('}, nesting)...)
	source = append(source, "true"...)
	source = append(source, bytes.Repeat([]byte{')'}, nesting)...)
	source = append(source, ` };`...)
	limits := public.DefaultLimits()
	limits.MaxDepth = nesting * 2
	policy, diagnostics := Compile(source, cedarBindings(), limits)
	if policy != nil || !hasCedarDiagnostic(diagnostics, public.CodeLimit) {
		t.Fatalf("Compile = (%v, %+v), want safe parser limit", policy, diagnostics)
	}
}

func TestLexerFailureUsesExactSourceSpan(t *testing.T) {
	for _, source := range [][]byte{
		[]byte(`permit(principal, action, resource); /*`),
		[]byte(`permit(principal, action, resource) when { context.team == "unterminated };`),
	} {
		policy, diagnostics := Compile(source, cedarBindings(), public.DefaultLimits())
		if policy != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeSyntax {
			t.Fatalf("Compile(%q) = (%v, %+v), want syntax", source, policy, diagnostics)
		}
		span := diagnostics[0].Span
		if span.Start == 0 || span.End != uint32(len(source)) {
			t.Fatalf("Compile(%q) span = [%d,%d), want exact suffix", source, span.Start, span.End)
		}
	}
}

func TestOrderedStringComparisonIsUnsupportedRatherThanIllTyped(t *testing.T) {
	source := []byte(`permit(principal, action, resource) when { context.team < "m" };`)
	policy, diagnostics := Compile(source, cedarBindings(), public.DefaultLimits())
	if policy != nil || !hasCedarDiagnostic(diagnostics, public.CodeUnsupported) || hasCedarDiagnostic(diagnostics, public.CodeType) {
		t.Fatalf("Compile = (%v, %+v), want unsupported", policy, diagnostics)
	}
}

func TestDiagnosticsAreBoundedAndDeterministic(t *testing.T) {
	source := []byte(`permit(principal, action, resource)
when { context.team like "b*" }
unless { context has enabled };`)
	limits := public.DefaultLimits()
	limits.MaxDiagnostics = 1
	firstPolicy, first := Compile(source, cedarBindings(), limits)
	secondPolicy, second := Compile(source, cedarBindings(), limits)
	if firstPolicy != nil || secondPolicy != nil || len(first) != 1 || !reflect.DeepEqual(first, second) {
		t.Fatalf("Compile results = (%v,%+v) and (%v,%+v)", firstPolicy, first, secondPolicy, second)
	}
}

func TestOneLimitDiagnosticPerExpression(t *testing.T) {
	limits := public.DefaultLimits()
	limits.MaxDepth = 4
	limits.MaxNodes = 4
	policy, diagnostics := Compile(
		[]byte(`permit(principal, action, resource) when { !!!!!context.enabled };`),
		cedarBindings(), limits,
	)
	if policy != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeLimit {
		t.Fatalf("Compile = (%v, %+v), want one limit diagnostic", policy, diagnostics)
	}
}

func TestMissingForbidContextEscalatesByFrontendContract(t *testing.T) {
	source := []byte(`permit(principal, action, resource);
forbid(principal, action, resource) when { context.team == "blocked" };`)
	semantic := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	compiled, diagnostics, err := internalfrontend.Compile(semantic)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("shared Compile = (%v, %+v)", err, diagnostics)
	}
	if got := evaluateCedarPolicy(t, compiled, nil); got != 4 {
		t.Fatalf("missing forbid context outcome = %d, want Escalate", got)
	}

	official, err := cedargo.NewPolicySetFromBytes("policy.cedar", source)
	if err != nil {
		t.Fatal(err)
	}
	decision, diagnostic := cedargo.Authorize(official, nil, cedargo.Request{})
	if decision != cedargo.Allow || len(diagnostic.Errors) != 1 {
		t.Fatalf("official Cedar = (%v, %+v), want allow plus one policy error", decision, diagnostic)
	}
}

func TestCedarFixtures(t *testing.T) {
	tests := []struct {
		name string
		code public.DiagnosticCode
	}{
		{name: "permit.cedar"},
		{name: "forbid.cedar"},
		{name: "unsupported.cedar", code: public.CodeUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := os.ReadFile("../../testdata/frontends/cedar/" + test.name)
			if err != nil {
				t.Fatal(err)
			}
			policy, diagnostics := Compile(source, cedarBindings(), public.DefaultLimits())
			if test.code.Valid() {
				if policy != nil || !hasCedarDiagnostic(diagnostics, test.code) {
					t.Fatalf("Compile = (%v, %+v), want %v", policy, diagnostics, test.code)
				}
				return
			}
			if policy == nil || len(diagnostics) != 0 {
				t.Fatalf("Compile = (%v, %+v), want policy", policy, diagnostics)
			}
		})
	}
}

func TestCompileEnforcesCedarBounds(t *testing.T) {
	base := public.DefaultLimits()
	tests := []struct {
		name   string
		source string
		limits func(public.Limits) public.Limits
	}{
		{name: "source", source: `permit(principal, action, resource);`, limits: func(l public.Limits) public.Limits { l.MaxSourceBytes = 3; return l }},
		{name: "fields", source: `permit(principal, action, resource);`, limits: func(l public.Limits) public.Limits { l.MaxFields = 5; return l }},
		{name: "nodes", source: `permit(principal, action, resource) when { context.enabled && true };`, limits: func(l public.Limits) public.Limits { l.MaxNodes = 1; return l }},
		{name: "depth", source: `permit(principal, action, resource) when { context.enabled && true };`, limits: func(l public.Limits) public.Limits { l.MaxDepth = 1; return l }},
		{name: "literals", source: `permit(principal, action, resource) when { context.enabled && true };`, limits: func(l public.Limits) public.Limits { l.MaxLiterals = 1; return l }},
		{name: "children", source: `permit(principal, action, resource) when { context.enabled && true };`, limits: func(l public.Limits) public.Limits { l.MaxChildren = 1; return l }},
		{name: "strings", source: `permit(principal, action, resource) when { context.team == "blue" };`, limits: func(l public.Limits) public.Limits {
			l.MaxStringBytes = uint32(bindingStringBytes(cedarBindings()) + uint64(len("blu")))
			return l
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := Compile([]byte(test.source), cedarBindings(), test.limits(base))
			if policy != nil || !hasCedarDiagnostic(diagnostics, public.CodeLimit) {
				t.Fatalf("Compile = (%v, %+v), want limit diagnostic", policy, diagnostics)
			}
		})
	}
}

func TestCapabilitiesAreStableAndCallerOwned(t *testing.T) {
	want := []public.Capability{
		{Name: "static_permit_forbid", Support: public.SupportSupported},
		{Name: "equality_scopes", Support: public.SupportRestricted},
		{Name: "context_scalar_conditions", Support: public.SupportRestricted},
		{Name: "boolean_composition", Support: public.SupportSupported},
		{Name: "forbid_precedence", Support: public.SupportSupported},
		{Name: "entity_hierarchy_and_attributes", Support: public.SupportRejected},
		{Name: "sets_records_and_extensions", Support: public.SupportRejected},
		{Name: "templates_and_annotations", Support: public.SupportRejected},
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

func TestDifferentialEvaluationMatchesOfficialCedar(t *testing.T) {
	source := []byte(`permit(
  principal == User::"alice",
  action == Action::"read",
  resource == Document::"report"
) when { context.team == "blue" && context.count >= 2 && context.enabled };
forbid(principal, action, resource) when { context.team == "blocked" };`)
	semantic := requireCedarPolicy(t, source, cedarBindings(), public.DefaultLimits())
	compiled, diagnostics, err := internalfrontend.Compile(semantic)
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("shared Compile = (%v, %+v)", err, diagnostics)
	}
	official, err := cedargo.NewPolicySetFromBytes("policy.cedar", source)
	if err != nil {
		t.Fatal(err)
	}
	principal := cedartypes.NewEntityUID("User", "alice")
	action := cedartypes.NewEntityUID("Action", "read")
	resource := cedartypes.NewEntityUID("Document", "report")
	tests := []struct {
		name    string
		team    string
		count   int64
		enabled bool
		missing bool
		want    schema.OutcomeID
	}{
		{name: "permit", team: "blue", count: 3, enabled: true, want: 1},
		{name: "no permit", team: "red", count: 3, enabled: true, want: 2},
		{name: "forbid wins", team: "blocked", count: 3, enabled: true, want: 2},
		{name: "missing", count: 3, enabled: true, missing: true, want: 4},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contextValues := cedartypes.RecordMap{
				"count": cedartypes.Long(test.count), "enabled": cedartypes.Boolean(test.enabled),
			}
			if !test.missing {
				contextValues["team"] = cedartypes.String(test.team)
			}
			decision, diagnostic := cedargo.Authorize(official, nil, cedargo.Request{
				Principal: principal, Action: action, Resource: resource, Context: cedartypes.NewRecord(contextValues),
			})
			switch test.want {
			case 1:
				if decision != cedargo.Allow || len(diagnostic.Errors) != 0 {
					t.Fatalf("official Cedar = (%v,%+v), want allow", decision, diagnostic)
				}
			case 2:
				if decision != cedargo.Deny || len(diagnostic.Errors) != 0 {
					t.Fatalf("official Cedar = (%v,%+v), want deny", decision, diagnostic)
				}
			case 4:
				if decision != cedargo.Deny || len(diagnostic.Errors) == 0 {
					t.Fatalf("official Cedar = (%v,%+v), want evaluation error", decision, diagnostic)
				}
			}
			fields := map[public.FieldID]any{
				1: principal.String(), 2: action.String(), 3: resource.String(),
				5: test.count, 6: test.enabled,
			}
			if !test.missing {
				fields[4] = test.team
			}
			if got := evaluateCedarPolicy(t, compiled, fields); got != test.want {
				t.Fatalf("Verifoxx outcome = %d, want %d", got, test.want)
			}
		})
	}
}

func requireCedarPolicy(t *testing.T, source []byte, bindings public.BindingSet, limits public.Limits) *public.Policy {
	t.Helper()
	policy, diagnostics := Compile(source, bindings, limits)
	if policy == nil || len(diagnostics) != 0 {
		t.Fatalf("Compile(%q) = (%v, %+v)", source, policy, diagnostics)
	}
	return policy
}

func hasCedarComparison(policy *public.Policy, field public.FieldID, operation public.CompareOp) bool {
	for row, kind := range policy.NodeKinds {
		if kind == public.NodeKindCompare && policy.NodeFields[row] == field && policy.NodeOps[row] == operation {
			return true
		}
	}
	return false
}

func hasCedarDiagnostic(diagnostics []public.Diagnostic, code public.DiagnosticCode) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Language == public.LanguageCedar {
			return true
		}
	}
	return false
}

func cedarDiagnosticsSorted(diagnostics []public.Diagnostic) bool {
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

func cedarFieldName(policy *public.Policy, row int) string {
	start := policy.FieldNameStarts[row]
	return string(policy.FieldBytes[start : start+policy.FieldNameLengths[row]])
}

func evaluateCedarPolicy(t *testing.T, compiled *coreprogram.Program, fields map[public.FieldID]any) schema.OutcomeID {
	t.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	for field, value := range fields {
		switch value := value.(type) {
		case string:
			symbol, err := builder.InternSymbol([]byte(value))
			if err != nil {
				t.Fatal(err)
			}
			if err := builder.SetSymbol(0, schema.FieldID(field), symbol); err != nil {
				t.Fatal(err)
			}
		case int64:
			if err := builder.SetInteger(0, schema.FieldID(field), value); err != nil {
				t.Fatal(err)
			}
		case bool:
			if err := builder.SetBoolean(0, schema.FieldID(field), value); err != nil {
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
	return outcomes.OutcomeIDs[0]
}
