package frontend

import (
	"math"
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/program"
)

func TestCompileRejectsMalformedSemanticPolicies(t *testing.T) {
	tests := []struct {
		name   string
		policy func(*testing.T) *public.Policy
		mutate func(*public.Policy)
		code   public.DiagnosticCode
	}{
		{
			name:   "nil policy",
			policy: func(*testing.T) *public.Policy { return nil },
			mutate: func(*public.Policy) {},
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "unequal node columns", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.NodeOps = policy.NodeOps[:len(policy.NodeOps)-1] },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "invalid metadata UTF-8", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.Name = []byte{0xff} },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "invalid source span", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.NodeSourceStarts[0] = policy.NodeSourceEnds[0] + 1 },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "invalid root", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.Root = 0 },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "unknown field", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.NodeFields[0] = public.FieldID(len(policy.FieldKinds) + 1) },
			code:   public.CodeUnknownField,
		},
		{
			name: "duplicate field target", policy: escalatePolicy,
			mutate: func(policy *public.Policy) {
				policy.FieldTargetStarts[1] = policy.FieldTargetStarts[0]
				policy.FieldTargetLengths[1] = policy.FieldTargetLengths[0]
			},
			code: public.CodeDuplicate,
		},
		{
			name: "duplicate source field", policy: escalatePolicy,
			mutate: func(policy *public.Policy) {
				policy.FieldNameStarts[1] = policy.FieldNameStarts[0]
				policy.FieldNameLengths[1] = policy.FieldNameLengths[0]
			},
			code: public.CodeDuplicate,
		},
		{
			name: "invalid field UTF-8", policy: escalatePolicy,
			mutate: func(policy *public.Policy) {
				policy.FieldBytes[policy.FieldTargetStarts[0]] = 0xff
			},
			code: public.CodeInvalidBinding,
		},
		{
			name: "invalid literal ID", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.NodeLiterals[0] = public.LiteralID(len(policy.LiteralKinds) + 1) },
			code:   public.CodeType,
		},
		{
			name: "incompatible scalar type", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.NodeLiterals[0] = 2 },
			code:   public.CodeType,
		},
		{
			name: "ordered Boolean", policy: booleanPolicy,
			mutate: func(policy *public.Policy) { policy.NodeOps[0] = public.CompareOpGreater },
			code:   public.CodeType,
		},
		{
			name: "defined without field", policy: definedPolicy,
			mutate: func(policy *public.Policy) { policy.NodeFields[0] = 0 },
			code:   public.CodeUnknownField,
		},
		{
			name: "defined with operation", policy: definedPolicy,
			mutate: func(policy *public.Policy) { policy.NodeOps[0] = public.CompareOpEqual },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "defined with literal", policy: definedPolicy,
			mutate: func(policy *public.Policy) { policy.NodeLiterals[0] = 1 },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "defined with child", policy: definedPolicy,
			mutate: func(policy *public.Policy) {
				policy.NodeChildCounts[0] = 1
				policy.ChildNodeIDs = []public.NodeID{1}
			},
			code: public.CodeInvalidPolicy,
		},
		{
			name: "defined with list", policy: definedPolicy,
			mutate: func(policy *public.Policy) {
				policy.NodeListCounts[0] = 1
				policy.ListLiteralIDs = []public.LiteralID{1}
			},
			code: public.CodeInvalidPolicy,
		},
		{
			name: "mixed in list", policy: escalatePolicy,
			mutate: func(policy *public.Policy) {
				policy.NodeOps[0] = public.CompareOpIn
				policy.NodeLiterals[0] = 0
				policy.NodeListStarts[0] = 0
				policy.NodeListCounts[0] = 2
				policy.ListLiteralIDs = []public.LiteralID{1, 2}
			},
			code: public.CodeType,
		},
		{
			name: "forward self reference", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.ChildNodeIDs[0] = policy.Root },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "invalid child range", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.NodeChildStarts[policy.Root-1] = math.MaxUint32 },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "unowned child edge", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.ChildNodeIDs = append(policy.ChildNodeIDs, 0) },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "unowned list edge", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.ListLiteralIDs = append(policy.ListLiteralIDs, 0) },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "unreachable nodes", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.Root = 1 },
			code:   public.CodeInvalidPolicy,
		},
		{
			name: "source limit", policy: escalatePolicy,
			mutate: func(policy *public.Policy) { policy.Source = make([]byte, public.DefaultLimits().MaxSourceBytes+1) },
			code:   public.CodeLimit,
		},
		{
			name: "oversized peer column", policy: escalatePolicy,
			mutate: func(policy *public.Policy) {
				policy.FieldNameStarts = make([]uint32, public.DefaultLimits().MaxFields+1)
			},
			code: public.CodeLimit,
		},
		{
			name:   "depth limit",
			policy: deepPolicy,
			mutate: func(*public.Policy) {},
			code:   public.CodeLimit,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := test.policy(t)
			if policy != nil {
				test.mutate(policy)
			}
			compiled, diagnostics, err := Compile(policy)
			if err != nil {
				t.Fatalf("Compile error = %v", err)
			}
			if compiled != nil {
				t.Fatal("Compile returned a Program for malformed input")
			}
			if !hasDiagnosticCode(diagnostics, test.code) {
				t.Fatalf("diagnostics = %+v, want code %s", diagnostics, test.code)
			}
		})
	}
}

func TestDiagnosticsAreSortedDeterministicAndCapped(t *testing.T) {
	limit := public.DefaultLimits().MaxDiagnostics
	nodes := int(limit + 5)
	policy := &public.Policy{
		Source: []byte{}, Name: []byte("invalid-many"), Version: []byte("v1"),
		NodeKinds:        make([]public.NodeKind, nodes),
		NodeOps:          make([]public.CompareOp, nodes),
		NodeFields:       make([]public.FieldID, nodes),
		NodeLiterals:     make([]public.LiteralID, nodes),
		NodeChildStarts:  make([]uint32, nodes),
		NodeChildCounts:  make([]uint16, nodes),
		NodeListStarts:   make([]uint32, nodes),
		NodeListCounts:   make([]uint16, nodes),
		NodeSourceStarts: make([]uint32, nodes),
		NodeSourceEnds:   make([]uint32, nodes),
		Root:             public.NodeID(nodes),
		Default:          public.DefaultEscalate,
	}
	var compiler Compiler
	destination := program.Program{InputBytes: []byte("unchanged"), ProgramSymbolCount: 17}
	diagnostics, err := compiler.Compile(&destination, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != int(limit) {
		t.Fatalf("diagnostic count = %d, want cap %d", len(diagnostics), limit)
	}
	if string(destination.InputBytes) != "unchanged" || destination.ProgramSymbolCount != 17 {
		t.Fatal("diagnostic failure mutated destination")
	}
	first := append([]public.Diagnostic(nil), diagnostics...)
	diagnostics, err = compiler.Compile(&destination, policy)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(diagnostics, first) {
		t.Fatalf("diagnostics changed across runs:\nfirst=%+v\nsecond=%+v", first, diagnostics)
	}
	for row := 1; row < len(diagnostics); row++ {
		left, right := diagnostics[row-1], diagnostics[row]
		if left.Span.Start > right.Span.Start ||
			left.Span.Start == right.Span.Start && left.Span.End > right.Span.End ||
			left.Span == right.Span && left.Code > right.Code ||
			left.Span == right.Span && left.Code == right.Code && left.Row > right.Row {
			t.Fatalf("diagnostics not sorted at %d: %+v then %+v", row, left, right)
		}
	}
}

func escalatePolicy(t *testing.T) *public.Policy {
	return testPolicy(t, public.DefaultEscalate)
}

func booleanPolicy(t *testing.T) *public.Policy {
	t.Helper()
	bindings := public.BindingSet{
		Name: "boolean", Version: "v1",
		Fields: []public.Binding{{Source: "trusted", Target: "subject.trusted", Kind: public.ValueKindBoolean, Group: public.FieldGroupSubject}},
	}
	builder, err := public.NewBuilder([]byte("trusted == true"), bindings, public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddCompare(1, public.CompareOpEqual, public.BooleanLiteral(true), public.Span{End: 15})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := builder.Finish(root, public.DefaultEscalate)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func definedPolicy(t *testing.T) *public.Policy {
	t.Helper()
	bindings := public.BindingSet{
		Name: "presence", Version: "v1",
		Fields: []public.Binding{{
			Source: "input.enabled", Target: "context.enabled",
			Kind: public.ValueKindBoolean, Group: public.FieldGroupContext,
		}},
	}
	builder, err := public.NewBuilder([]byte("input.enabled"), bindings, public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddDefined(1, public.Span{End: 13})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := builder.Finish(root, public.DefaultEscalate)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func deepPolicy(t *testing.T) *public.Policy {
	t.Helper()
	limits := public.DefaultLimits()
	limits.MaxDepth++
	builder, err := public.NewBuilder(
		[]byte("true"),
		public.BindingSet{Name: "deep", Version: "v1"},
		limits,
	)
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddBoolean(true, public.Span{End: 4})
	if err != nil {
		t.Fatal(err)
	}
	for range public.DefaultLimits().MaxDepth {
		root, err = builder.AddNot(root, public.Span{End: 4})
		if err != nil {
			t.Fatal(err)
		}
	}
	policy, err := builder.Finish(root, public.DefaultEscalate)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func hasDiagnosticCode(diagnostics []public.Diagnostic, code public.DiagnosticCode) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
