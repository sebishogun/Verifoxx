package frontend

import (
	"reflect"
	"testing"
)

func TestBuilderCreatesOwnedSoAAndCSRPolicy(t *testing.T) {
	source := []byte("é && count > 3")
	bindings := BindingSet{
		Name: "example", Version: "v1",
		Fields: []Binding{
			{Source: "team", Target: "requester.team", Kind: ValueKindString, Group: FieldGroupSubject},
			{Source: "count", Target: "environment.count", Kind: ValueKindInteger, Group: FieldGroupContext},
		},
	}
	builder, err := NewBuilder(source, bindings, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}

	source[0] = 'x'
	bindings.Name = "changed"
	bindings.Fields[0].Source = "changed"

	constant, err := builder.AddBoolean(true, Span{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := builder.AddCompare(2, CompareOpGreater, IntegerLiteral(3), Span{Start: 6, End: 15})
	if err != nil {
		t.Fatal(err)
	}
	values := [][]byte{[]byte("red"), []byte("blue")}
	membership, err := builder.AddIn(1, []Literal{StringLiteral(values[0]), StringLiteral(values[1])}, Span{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	values[0][0] = 'X'

	all, err := builder.AddAll([]NodeID{constant, comparison}, Span{Start: 0, End: 15})
	if err != nil {
		t.Fatal(err)
	}
	not, err := builder.AddNot(membership, Span{Start: 0, End: 2})
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddAny([]NodeID{all, not}, Span{Start: 0, End: 15})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := builder.Finish(root, DefaultEscalate)
	if err != nil {
		t.Fatal(err)
	}

	if string(policy.Source) != "é && count > 3" || string(policy.Name) != "example" || string(policy.Version) != "v1" {
		t.Fatalf("builder retained caller storage: source=%q name=%q version=%q", policy.Source, policy.Name, policy.Version)
	}
	if policy.Root != root || policy.Default != DefaultEscalate {
		t.Fatalf("root/default = (%d, %s)", policy.Root, policy.Default)
	}
	wantKinds := []NodeKind{NodeKindBoolean, NodeKindCompare, NodeKindCompare, NodeKindAll, NodeKindNot, NodeKindAny}
	if !reflect.DeepEqual(policy.NodeKinds, wantKinds) {
		t.Fatalf("NodeKinds = %v, want %v", policy.NodeKinds, wantKinds)
	}
	for _, columnLength := range []int{
		len(policy.NodeOps), len(policy.NodeFields), len(policy.NodeLiterals),
		len(policy.NodeChildStarts), len(policy.NodeChildCounts),
		len(policy.NodeListStarts), len(policy.NodeListCounts),
		len(policy.NodeSourceStarts), len(policy.NodeSourceEnds),
	} {
		if columnLength != len(policy.NodeKinds) {
			t.Fatalf("node column length = %d, want %d", columnLength, len(policy.NodeKinds))
		}
	}
	if !reflect.DeepEqual(policy.ChildNodeIDs, []NodeID{constant, comparison, membership, all, not}) {
		t.Fatalf("ChildNodeIDs = %v", policy.ChildNodeIDs)
	}
	if policy.NodeChildStarts[3] != 0 || policy.NodeChildCounts[3] != 2 ||
		policy.NodeChildStarts[4] != 2 || policy.NodeChildCounts[4] != 1 ||
		policy.NodeChildStarts[5] != 3 || policy.NodeChildCounts[5] != 2 {
		t.Fatalf("unexpected child CSR: starts=%v counts=%v", policy.NodeChildStarts, policy.NodeChildCounts)
	}
	if len(policy.ListLiteralIDs) != 2 || policy.NodeListStarts[2] != 0 || policy.NodeListCounts[2] != 2 {
		t.Fatalf("unexpected list CSR: ids=%v starts=%v counts=%v", policy.ListLiteralIDs, policy.NodeListStarts, policy.NodeListCounts)
	}
	if got := policySymbol(policy, policy.ListLiteralIDs[0]); got != "red" {
		t.Fatalf("first list literal = %q, want red", got)
	}
	if policy.NodeSourceStarts[0] != 0 || policy.NodeSourceEnds[0] != 2 {
		t.Fatalf("Unicode byte span = [%d,%d), want [0,2)", policy.NodeSourceStarts[0], policy.NodeSourceEnds[0])
	}
	assertPolicySliceElementsPointerless(t, policy)
	assertExactCapacities(t, policy)
}

func TestBuilderAddsDefinedWithoutPayload(t *testing.T) {
	source := []byte("input.enabled")
	bindings := BindingSet{
		Name: "presence", Version: "v1",
		Fields: []Binding{{
			Source: "input.enabled", Target: "context.enabled",
			Kind: ValueKindBoolean, Group: FieldGroupContext,
		}},
	}
	builder, err := NewBuilder(source, bindings, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddDefined(1, Span{End: uint32(len(source))})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := builder.Finish(root, DefaultEscalate)
	if err != nil {
		t.Fatal(err)
	}
	row := root - 1
	if policy.NodeKinds[row] != NodeKindDefined || policy.NodeFields[row] != 1 ||
		policy.NodeOps[row] != CompareOpInvalid || policy.NodeLiterals[row] != 0 ||
		policy.NodeChildCounts[row] != 0 || policy.NodeListCounts[row] != 0 ||
		policy.NodeSourceStarts[row] != 0 || policy.NodeSourceEnds[row] != uint32(len(source)) {
		t.Fatalf("defined row = kind %v field %d op %v literal %d children %d list %d span [%d,%d)",
			policy.NodeKinds[row], policy.NodeFields[row], policy.NodeOps[row], policy.NodeLiterals[row],
			policy.NodeChildCounts[row], policy.NodeListCounts[row], policy.NodeSourceStarts[row], policy.NodeSourceEnds[row])
	}
	if len(policy.LiteralKinds) != 0 || len(policy.ChildNodeIDs) != 0 || len(policy.ListLiteralIDs) != 0 {
		t.Fatalf("defined appended payload: literals=%v children=%v list=%v", policy.LiteralKinds, policy.ChildNodeIDs, policy.ListLiteralIDs)
	}
}

func TestBuilderFailuresAreAtomic(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxNodes = 1
	bindings := BindingSet{
		Name: "atomic", Version: "v1",
		Fields: []Binding{{Source: "count", Target: "context.count", Kind: ValueKindInteger, Group: FieldGroupContext}},
	}
	builder, err := NewBuilder([]byte("count"), bindings, limits)
	if err != nil {
		t.Fatal(err)
	}
	root, err := builder.AddCompare(1, CompareOpEqual, IntegerLiteral(1), Span{End: 5})
	if err != nil {
		t.Fatal(err)
	}
	before, err := builder.Finish(root, DefaultReject)
	if err != nil {
		t.Fatal(err)
	}

	failures := []func() error{
		func() error { _, err := builder.AddBoolean(true, Span{End: 5}); return err },
		func() error { _, err := builder.AddDefined(1, Span{End: 5}); return err },
		func() error { _, err := builder.AddDefined(0, Span{End: 5}); return err },
		func() error {
			_, err := builder.AddCompare(1, CompareOpIn, IntegerLiteral(2), Span{End: 5})
			return err
		},
		func() error {
			_, err := builder.AddCompare(1, CompareOpEqual, IntegerLiteral(2), Span{End: 6})
			return err
		},
		func() error {
			_, err := builder.AddIn(1, []Literal{IntegerLiteral(1), StringLiteral([]byte("x"))}, Span{End: 5})
			return err
		},
		func() error { _, err := builder.AddAll([]NodeID{root}, Span{End: 5}); return err },
		func() error { _, err := builder.AddNot(0, Span{End: 5}); return err },
	}
	for i, failure := range failures {
		if err := failure(); err == nil {
			t.Fatalf("failure %d returned nil", i)
		}
	}
	after, err := builder.Finish(root, DefaultReject)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected append mutated policy:\n before=%+v\n after=%+v", before, after)
	}
	if policy, err := builder.Finish(0, DefaultReject); err == nil || policy != nil {
		t.Fatalf("Finish(0) = (%+v, %v), want nil policy and error", policy, err)
	}
	if policy, err := builder.Finish(root+1, DefaultReject); err == nil || policy != nil {
		t.Fatalf("Finish(out of range) = (%+v, %v), want nil policy and error", policy, err)
	}
	if policy, err := builder.Finish(root, DefaultInvalid); err == nil || policy != nil {
		t.Fatalf("Finish(invalid default) = (%+v, %v), want nil policy and error", policy, err)
	}
}

func TestFinishedPolicyDoesNotAliasBuilder(t *testing.T) {
	bindings := BindingSet{Name: "owned", Version: "v1"}
	builder, err := NewBuilder([]byte("true"), bindings, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	first, err := builder.AddBoolean(true, Span{End: 4})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := builder.Finish(first, DefaultEscalate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.AddBoolean(false, Span{End: 4}); err != nil {
		t.Fatal(err)
	}
	if len(policy.NodeKinds) != 1 || len(policy.LiteralKinds) != 1 || policy.BooleanValues[0] != 1 {
		t.Fatalf("finished policy changed after builder mutation: %+v", policy)
	}
}

func policySymbol(policy *Policy, id LiteralID) string {
	if id == 0 || int(id) > len(policy.LiteralKinds) {
		return ""
	}
	row := int(id - 1)
	if policy.LiteralKinds[row] != ValueKindString {
		return ""
	}
	ref := policy.LiteralRefs[row]
	if int(ref) >= len(policy.SymbolStarts) || int(ref) >= len(policy.SymbolLengths) {
		return ""
	}
	start := policy.SymbolStarts[ref]
	end := start + policy.SymbolLengths[ref]
	if int(end) > len(policy.SymbolBytes) {
		return ""
	}
	return string(policy.SymbolBytes[start:end])
}

func assertPolicySliceElementsPointerless(t *testing.T, policy *Policy) {
	t.Helper()
	typ := reflect.TypeOf(*policy)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type.Kind() == reflect.Slice {
			assertPointerless(t, field.Type.Elem())
		}
	}
}

func assertExactCapacities(t *testing.T, policy *Policy) {
	t.Helper()
	value := reflect.ValueOf(policy).Elem()
	typ := value.Type()
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).Kind() != reflect.Slice {
			continue
		}
		if value.Field(i).Len() != value.Field(i).Cap() {
			t.Errorf("%s len/cap = %d/%d", typ.Field(i).Name, value.Field(i).Len(), value.Field(i).Cap())
		}
	}
}
