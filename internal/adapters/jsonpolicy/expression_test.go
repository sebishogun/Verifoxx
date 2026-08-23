package jsonpolicy

import (
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

const (
	fieldSubjectTrust     schema.FieldID = 1
	fieldContextCount     schema.FieldID = 2
	fieldContextEnabled   schema.FieldID = 3
	fieldContextRequested schema.FieldID = 4
	fieldContextEnv       schema.FieldID = 5
	fieldContextUsage     schema.FieldID = 6
)

// testSchema builds the field schema used by every expression test. Field
// registration order fixes the FieldIDs asserted throughout this file.
func testSchema(t testing.TB) *schema.Schema {
	t.Helper()
	syms := schema.NewSymbolInterner(16)
	b := schema.NewBuilder()
	add := func(name string, kind schema.ValueKind, group schema.FieldGroup) {
		id, err := syms.Intern([]byte(name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.AddField(id, kind, group); err != nil {
			t.Fatal(err)
		}
	}
	add("subject.trust", schema.ValueKindSymbol, schema.FieldGroupSubject)
	add("context.count", schema.ValueKindInteger, schema.FieldGroupContext)
	add("context.enabled", schema.ValueKindBoolean, schema.FieldGroupContext)
	add("context.requested_at", schema.ValueKindTimestamp, schema.FieldGroupContext)
	add("context.environment", schema.ValueKindPresence, schema.FieldGroupContext)
	add("context.usage", schema.ValueKindSymbol, schema.FieldGroupContext)
	return b.Finish()
}

// testInterner returns an interner containing exactly the field name symbols.
func testInterner(t testing.TB) *schema.Interner {
	t.Helper()
	syms := schema.NewSymbolInterner(16)
	for _, name := range []string{
		"subject.trust", "context.count", "context.enabled",
		"context.requested_at", "context.environment", "context.usage",
	} {
		if _, err := syms.Intern([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	return syms
}

// catalogBuilder returns a builder bound to src with the given evidence
// catalogs already populated.
func catalogBuilder(t *testing.T, src string, kinds, states []string) *ast.Builder {
	t.Helper()
	b := ast.NewBuilder(ast.Hints{
		Nodes: 16, Values: 16, SymbolValues: 16, SymbolBytes: 512,
		EvidenceKinds: 4, EvidenceStates: 4,
		SourceBytes: len(src),
	})
	if err := b.SetSource([]byte(src)); err != nil {
		t.Fatal(err)
	}
	for _, k := range kinds {
		id, err := b.AddSymbolValue([]byte(k))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.AddEvidenceKind(id, ast.SourceSpan{}); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range states {
		id, err := b.AddSymbolValue([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.AddEvidenceState(id, ast.SourceSpan{}); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// decodeExprInto decodes one expression object from src into b.
func decodeExprInto(t *testing.T, b *ast.Builder, src string, syms *schema.Interner, limits Limits) schema.NodeID {
	t.Helper()
	d := decoder{src: []byte(src), fields: testSchema(t), symbols: syms, limits: limits}
	d.skipWS()
	id, err := d.decodeExpression(b, 1)
	if err != nil {
		t.Fatalf("decodeExpression(%q) error: %v", src, err)
	}
	if d.pos != len(src) {
		t.Fatalf("decodeExpression(%q) consumed %d of %d bytes", src, d.pos, len(src))
	}
	return id
}

// decodeExpr decodes one expression object from src into a fresh builder.
func decodeExpr(t *testing.T, src string, syms *schema.Interner, limits Limits) (*ast.Builder, schema.NodeID) {
	t.Helper()
	b := ast.NewBuilder(ast.Hints{
		Nodes: 32, CompareNodes: 16, CompareListValues: 8, GroupNodes: 8,
		ChildEdges: 16, NotNodes: 4, EvidenceNodes: 4,
		Values: 16, SymbolValues: 16, SymbolBytes: 512,
		SourceBytes: len(src),
	})
	if err := b.SetSource([]byte(src)); err != nil {
		t.Fatal(err)
	}
	return b, decodeExprInto(t, b, src, syms, limits)
}

// rejectExprInto decodes src expecting a *Error with the given code.
func rejectExprInto(t *testing.T, b *ast.Builder, src string, syms *schema.Interner, limits Limits, want ErrorCode, wantOffset int) *Error {
	t.Helper()
	d := decoder{src: []byte(src), fields: testSchema(t), symbols: syms, limits: limits}
	d.skipWS()
	_, err := d.decodeExpression(b, 1)
	var je *Error
	if !errors.As(err, &je) {
		t.Fatalf("source %q: error = %v, want *Error with code %s", src, err, want)
	}
	if je.Code != want {
		t.Fatalf("source %q: code = %s (%d), want %s (%d)", src, je.Code, je.Code, want, want)
	}
	if je.Offset < 0 || je.Offset > len(src) {
		t.Fatalf("source %q: offset %d outside [0, %d]", src, je.Offset, len(src))
	}
	if wantOffset >= 0 && je.Offset != wantOffset {
		t.Fatalf("source %q: offset = %d, want %d", src, je.Offset, wantOffset)
	}
	return je
}

func rejectExpr(t *testing.T, src string, syms *schema.Interner, limits Limits, want ErrorCode, wantOffset int) *Error {
	t.Helper()
	b := ast.NewBuilder(ast.Hints{Nodes: 32, Values: 16, SymbolBytes: 512, SourceBytes: len(src)})
	if err := b.SetSource([]byte(src)); err != nil {
		t.Fatal(err)
	}
	return rejectExprInto(t, b, src, syms, limits, want, wantOffset)
}

// wantSpan returns the span of an exact object substring of src.
func wantSpan(t *testing.T, src, obj string) ast.SourceSpan {
	t.Helper()
	i := offsetOf(t, src, obj)
	return ast.SourceSpan{Start: uint32(i), End: uint32(i + len(obj))}
}

func TestDecodeExpressionEqual(t *testing.T) {
	src := `{"op":"equal","field":"subject.trust","value":"external"}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	d := b.Document()
	if k, _ := d.Kind(id); k != ast.NodeKindCompare {
		t.Fatalf("Kind(%d) = %v, want compare", id, k)
	}
	field, op, valueID, ok := d.Compare(id)
	if !ok || field != fieldSubjectTrust || op != ast.CompareOpEqual {
		t.Fatalf("Compare(%d) = (%d, %v, %d, %v)", id, field, op, valueID, ok)
	}
	kind, ok := d.ValueKind(valueID)
	if !ok || kind != schema.ValueKindSymbol {
		t.Fatalf("ValueKind(%d) = %v, %v", valueID, kind, ok)
	}
	if got, ok := d.SymbolValue(valueID); !ok || string(got) != "external" {
		t.Fatalf("SymbolValue(%d) = %q, %v", valueID, got, ok)
	}
	want := wantSpan(t, src, src)
	if span, ok := d.Span(id); !ok || span != want {
		t.Fatalf("Span(%d) = %+v, %v; want %+v", id, span, ok, want)
	}
}

func TestDecodeExpressionCompareOps(t *testing.T) {
	tests := []struct {
		op    string
		want  ast.CompareOp
		value int64
	}{
		{"not_equal", ast.CompareOpNotEqual, -7},
		{"less", ast.CompareOpLess, 2},
		{"less_equal", ast.CompareOpLessEqual, 3},
		{"greater", ast.CompareOpGreater, 4},
		{"greater_equal", ast.CompareOpGreaterEqual, 5},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			src := fmt.Sprintf(`{"op":"%s","field":"context.count","value":%d}`, tc.op, tc.value)
			b, id := decodeExpr(t, src, testInterner(t), Limits{})
			field, op, valueID, ok := b.Document().Compare(id)
			if !ok || field != fieldContextCount || op != tc.want {
				t.Fatalf("Compare(%d) = (%d, %v, %d, %v)", id, field, op, valueID, ok)
			}
			if v, ok := b.Document().IntegerValue(valueID); !ok || v != tc.value {
				t.Fatalf("IntegerValue(%d) = (%d, %v)", valueID, v, ok)
			}
		})
	}
}

func TestDecodeExpressionExists(t *testing.T) {
	for _, src := range []string{
		`{"op":"exists","field":"context.environment"}`,
		`{"op":"exists","field":"subject.trust"}`,
	} {
		b, id := decodeExpr(t, src, testInterner(t), Limits{})
		field, op, valueID, ok := b.Document().Compare(id)
		if !ok || op != ast.CompareOpExists || valueID != 0 {
			t.Fatalf("Compare(%d) = (%d, %v, %d, %v), want exists with zero value", id, field, op, valueID, ok)
		}
	}
}

func TestDecodeExpressionIn(t *testing.T) {
	src := `{"op":"in","field":"context.usage","values":["standard","limited"]}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	d := b.Document()
	field, op, _, ok := d.Compare(id)
	if !ok || field != fieldContextUsage || op != ast.CompareOpIn {
		t.Fatalf("Compare(%d) = (%d, %v, _, %v)", id, field, op, ok)
	}
	start, count, ok := d.CompareListRange(id)
	if !ok || start != 0 || count != 2 {
		t.Fatalf("CompareListRange(%d) = (%d, %d, %v)", id, start, count, ok)
	}
	values, ok := d.InValues(id)
	if !ok || len(values) != 2 {
		t.Fatalf("InValues(%d) = %v, %v", id, values, ok)
	}
	for i, want := range []string{"standard", "limited"} {
		got, ok := d.SymbolValue(values[i])
		if !ok || string(got) != want {
			t.Fatalf("InValues(%d)[%d] = %q, %v; want %q", id, i, got, ok, want)
		}
	}
	if span, ok := d.Span(id); !ok || span != wantSpan(t, src, src) {
		t.Fatalf("Span(%d) = %+v, %v", id, span, ok)
	}
}

func TestDecodeExpressionAllAny(t *testing.T) {
	for _, tc := range []struct {
		op   string
		want ast.NodeKind
	}{
		{"all", ast.NodeKindAll},
		{"any", ast.NodeKindAny},
	} {
		t.Run(tc.op, func(t *testing.T) {
			src := `{"op":"` + tc.op + `","args":[{"op":"exists","field":"context.environment"},{"op":"in","field":"context.usage","values":["standard"]}]}`
			b, id := decodeExpr(t, src, testInterner(t), Limits{})
			d := b.Document()
			if k, _ := d.Kind(id); k != tc.want {
				t.Fatalf("Kind(%d) = %v, want %v", id, k, tc.want)
			}
			start, count, ok := d.GroupRange(id)
			if !ok || start != 0 || count != 2 {
				t.Fatalf("GroupRange(%d) = (%d, %d, %v)", id, start, count, ok)
			}
			children, ok := d.GroupChildren(id)
			if !ok || len(children) != 2 {
				t.Fatalf("GroupChildren(%d) = %v, %v", id, children, ok)
			}
			field, op, _, ok := d.Compare(children[0])
			if !ok || field != fieldContextEnv || op != ast.CompareOpExists {
				t.Fatalf("child 1 Compare = (%d, %v, _, %v)", field, op, ok)
			}
			field, op, _, ok = d.Compare(children[1])
			if !ok || field != fieldContextUsage || op != ast.CompareOpIn {
				t.Fatalf("child 2 Compare = (%d, %v, _, %v)", field, op, ok)
			}
			if span, ok := d.Span(id); !ok || span != wantSpan(t, src, src) {
				t.Fatalf("Span(%d) = %+v, %v", id, span, ok)
			}
			child1 := `{"op":"exists","field":"context.environment"}`
			if span, ok := d.Span(children[0]); !ok || span != wantSpan(t, src, child1) {
				t.Fatalf("child 1 span = %+v, %v; want %+v", span, ok, wantSpan(t, src, child1))
			}
		})
	}
}

func TestDecodeExpressionNot(t *testing.T) {
	src := `{"op":"not","arg":{"op":"equal","field":"context.enabled","value":true}}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	d := b.Document()
	if k, _ := d.Kind(id); k != ast.NodeKindNot {
		t.Fatalf("Kind(%d) = %v, want not", id, k)
	}
	child, ok := d.NotChild(id)
	if !ok || child != 1 {
		t.Fatalf("NotChild(%d) = (%d, %v), want (1, true)", id, child, ok)
	}
	field, op, valueID, ok := d.Compare(child)
	if !ok || field != fieldContextEnabled || op != ast.CompareOpEqual {
		t.Fatalf("child Compare = (%d, %v, %d, %v)", field, op, valueID, ok)
	}
	if v, ok := d.BooleanValue(valueID); !ok || !v {
		t.Fatalf("BooleanValue(%d) = (%v, %v), want true", valueID, v, ok)
	}
}

func TestDecodeExpressionNestedGroups(t *testing.T) {
	src := `{"op":"all","args":[{"op":"any","args":[{"op":"equal","field":"context.count","value":1},{"op":"not","arg":{"op":"equal","field":"context.count","value":2}}]},{"op":"any","args":[{"op":"in","field":"context.usage","values":["a"]},{"op":"exists","field":"context.environment"}]}]}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	d := b.Document()
	if d.Len() != 8 {
		t.Fatalf("Len() = %d, want 8", d.Len())
	}
	if k, _ := d.Kind(id); k != ast.NodeKindAll {
		t.Fatalf("root kind = %v", k)
	}
	start, count, ok := d.GroupRange(id)
	if !ok || start != 4 || count != 2 {
		t.Fatalf("root GroupRange = (%d, %d, %v)", start, count, ok)
	}
	children, _ := d.GroupChildren(id)
	firstAny, secondAny := children[0], children[1]
	if k, _ := d.Kind(firstAny); k != ast.NodeKindAny {
		t.Fatalf("first child kind = %v", k)
	}
	start, count, _ = d.GroupRange(firstAny)
	if start != 0 || count != 2 {
		t.Fatalf("first any GroupRange = (%d, %d)", start, count)
	}
	firstChildren, _ := d.GroupChildren(firstAny)
	eq1, notNode := firstChildren[0], firstChildren[1]
	if field, op, _, ok := d.Compare(eq1); !ok || field != fieldContextCount || op != ast.CompareOpEqual {
		t.Fatalf("eq1 = (%d, %v, _, %v)", field, op, ok)
	}
	if k, _ := d.Kind(notNode); k != ast.NodeKindNot {
		t.Fatalf("notNode kind = %v", k)
	}
	eq2, _ := d.NotChild(notNode)
	if field, op, _, ok := d.Compare(eq2); !ok || field != fieldContextCount || op != ast.CompareOpEqual {
		t.Fatalf("eq2 = (%d, %v, _, %v)", field, op, ok)
	}
	start, count, _ = d.GroupRange(secondAny)
	if start != 2 || count != 2 {
		t.Fatalf("second any GroupRange = (%d, %d)", start, count)
	}
	secondChildren, _ := d.GroupChildren(secondAny)
	inNode, existsNode := secondChildren[0], secondChildren[1]
	if field, op, _, ok := d.Compare(inNode); !ok || field != fieldContextUsage || op != ast.CompareOpIn {
		t.Fatalf("inNode = (%d, %v, _, %v)", field, op, ok)
	}
	if field, op, _, ok := d.Compare(existsNode); !ok || field != fieldContextEnv || op != ast.CompareOpExists {
		t.Fatalf("existsNode = (%d, %v, _, %v)", field, op, ok)
	}
	innerAny := `{"op":"any","args":[{"op":"in","field":"context.usage","values":["a"]},{"op":"exists","field":"context.environment"}]}`
	if span, ok := d.Span(secondAny); !ok || span != wantSpan(t, src, innerAny) {
		t.Fatalf("second any span = %+v, %v; want %+v", span, ok, wantSpan(t, src, innerAny))
	}
}

func TestDecodeExpressionScratchRangesNestedSiblings(t *testing.T) {
	src := `{"op":"all","args":[{"op":"in","field":"context.usage","values":["standard","limited"]},{"op":"in","field":"context.usage","values":["other"]}]}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	d := b.Document()
	children, ok := d.GroupChildren(id)
	if !ok || len(children) != 2 {
		t.Fatalf("GroupChildren(%d) = %v, %v", id, children, ok)
	}
	values, _ := d.InValues(children[0])
	if len(values) != 2 {
		t.Fatalf("first InValues = %v", values)
	}
	got, _ := d.SymbolValue(values[0])
	if string(got) != "standard" {
		t.Fatalf("first value = %q", got)
	}
	got, _ = d.SymbolValue(values[1])
	if string(got) != "limited" {
		t.Fatalf("second value = %q", got)
	}
	values, _ = d.InValues(children[1])
	if len(values) != 1 {
		t.Fatalf("second InValues = %v", values)
	}
	got, _ = d.SymbolValue(values[0])
	if string(got) != "other" {
		t.Fatalf("third value = %q", got)
	}
	start, count, _ := d.CompareListRange(children[0])
	if start != 0 || count != 2 {
		t.Fatalf("first list range = (%d, %d)", start, count)
	}
	start, count, _ = d.CompareListRange(children[1])
	if start != 2 || count != 1 {
		t.Fatalf("second list range = (%d, %d)", start, count)
	}
}

func TestDecodeExpressionEvidenceMatches(t *testing.T) {
	for _, tc := range []struct {
		name      string
		src       string
		wantKind  schema.EvidenceKindID
		wantState schema.EvidenceStateID
	}{
		{"canonical order", `{"op":"evidence_matches","kind":"approval_record","state":"current","explanation":{"issue":"{evidence_kind}"}}`, 1, 1},
		{"state before kind", `{"op":"evidence_matches","explanation":{"issue":"{evidence_kind}"},"state":"stale","kind":"usage_adjustment"}`, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := catalogBuilder(t, tc.src, []string{"approval_record", "usage_adjustment"}, []string{"current", "stale"})
			id := decodeExprInto(t, b, tc.src, testInterner(t), Limits{})
			d := b.Document()
			if k, _ := d.Kind(id); k != ast.NodeKindEvidence {
				t.Fatalf("Kind(%d) = %v", id, k)
			}
			kind, state, ok := d.Evidence(id)
			if !ok || kind != tc.wantKind || state != tc.wantState {
				t.Fatalf("Evidence(%d) = (%d, %d, %v), want (%d, %d)", id, kind, state, ok, tc.wantKind, tc.wantState)
			}
			if span, ok := d.Span(id); !ok || span != wantSpan(t, tc.src, tc.src) {
				t.Fatalf("Span(%d) = %+v, %v", id, span, ok)
			}
		})
	}
}

func TestDecodeEvidenceMatchQualifiers(t *testing.T) {
	src := `{"op":"evidence_matches","kind":"approval_record","state":"current","subject":"local_approved_env","scope":"trusted_internal_only","timing":"before_execution","explanation":{"issue":"{evidence_kind}"}}`
	b := catalogBuilder(t, src, []string{"approval_record"}, []string{"current"})
	id := decodeExprInto(t, b, src, testInterner(t), Limits{})
	kind, state, subject, scope, timing, ok := b.Document().EvidenceMatch(id)
	if !ok || kind != 1 || state != 1 {
		t.Fatalf("EvidenceMatch(%d) = (%d, %d, %d, %d, %d, %v)", id, kind, state, subject, scope, timing, ok)
	}
	for value, want := range map[schema.ValueID]string{
		subject: "local_approved_env",
		scope:   "trusted_internal_only",
		timing:  "before_execution",
	} {
		got, ok := b.Document().SymbolValue(value)
		if !ok || string(got) != want {
			t.Fatalf("qualifier %d = %q, %v; want %q", value, got, ok, want)
		}
	}
}

func TestDecodeExpressionKeyPermutations(t *testing.T) {
	canonical := `{"op":"equal","field":"context.count","value":7}`
	b, id := decodeExpr(t, canonical, testInterner(t), Limits{})
	wantField, wantOp, wantValue, _ := b.Document().Compare(id)

	for _, src := range []string{
		`{"op":"equal","value":7,"field":"context.count"}`,
		`{"value":7,"field":"context.count","op":"equal"}`,
		`{"value":7,"op":"equal","field":"context.count"}`,
	} {
		b, id := decodeExpr(t, src, testInterner(t), Limits{})
		field, op, valueID, ok := b.Document().Compare(id)
		if !ok || field != wantField || op != wantOp || valueID != wantValue {
			t.Fatalf("%s: Compare = (%d, %v, %d, %v)", src, field, op, valueID, ok)
		}
	}

	src := `{"values":["standard"],"field":"context.usage","op":"in"}`
	b, id = decodeExpr(t, src, testInterner(t), Limits{})
	if field, op, _, ok := b.Document().Compare(id); !ok || field != fieldContextUsage || op != ast.CompareOpIn {
		t.Fatalf("in permutation = (%d, %v, _, %v)", field, op, ok)
	}

	src = `{"args":[{"op":"exists","field":"context.environment"}],"op":"all"}`
	b, id = decodeExpr(t, src, testInterner(t), Limits{})
	if k, _ := b.Document().Kind(id); k != ast.NodeKindAll {
		t.Fatalf("all permutation kind = %v", k)
	}
	if _, count, _ := b.Document().GroupRange(id); count != 1 {
		t.Fatalf("all permutation children = %d", count)
	}
}

func TestDecodeExpressionStringEscapes(t *testing.T) {
	src := `{"op":"equal","field":"subject.trust","value":"caf\u00e9 \u4e2d\u6587 \ud83d\ude00 \t\n\"\\\/\b\f\r"}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	_, _, valueID, ok := b.Document().Compare(id)
	if !ok {
		t.Fatal("Compare missing")
	}
	want := "café 中文 😀 \t\n\"\\/\b\f\r"
	if got, ok := b.Document().SymbolValue(valueID); !ok || string(got) != want {
		t.Fatalf("SymbolValue = %q, %v; want %q", got, ok, want)
	}
}

func TestDecodeExpressionIntegerLimits(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int64
	}{
		{"-9223372036854775808", math.MinInt64},
		{"9223372036854775807", math.MaxInt64},
		{"0", 0},
	} {
		src := fmt.Sprintf(`{"op":"equal","field":"context.count","value":%s}`, tc.value)
		b, id := decodeExpr(t, src, testInterner(t), Limits{})
		_, _, valueID, ok := b.Document().Compare(id)
		if !ok {
			t.Fatal("Compare missing")
		}
		if v, ok := b.Document().IntegerValue(valueID); !ok || v != tc.want {
			t.Fatalf("IntegerValue(%d) = (%d, %v), want %d", valueID, v, ok, tc.want)
		}
	}
}

func TestDecodeExpressionBoolean(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"false", false},
	} {
		src := fmt.Sprintf(`{"op":"equal","field":"context.enabled","value":%s}`, tc.value)
		b, id := decodeExpr(t, src, testInterner(t), Limits{})
		_, _, valueID, ok := b.Document().Compare(id)
		if !ok {
			t.Fatal("Compare missing")
		}
		if v, ok := b.Document().BooleanValue(valueID); !ok || v != tc.want {
			t.Fatalf("BooleanValue(%d) = (%v, %v), want %v", valueID, v, ok, tc.want)
		}
	}
	src := `{"op":"in","field":"context.enabled","values":[true,false]}`
	b, id := decodeExpr(t, src, testInterner(t), Limits{})
	values, ok := b.Document().InValues(id)
	if !ok || len(values) != 2 {
		t.Fatalf("InValues(%d) = %v, %v", id, values, ok)
	}
	if v, ok := b.Document().BooleanValue(values[0]); !ok || !v {
		t.Fatalf("BooleanValue(values[0]) = (%v, %v)", v, ok)
	}
	if v, ok := b.Document().BooleanValue(values[1]); !ok || v {
		t.Fatalf("BooleanValue(values[1]) = (%v, %v)", v, ok)
	}
}

func TestDecodeExpressionTimestamps(t *testing.T) {
	tests := []struct {
		ts   string
		want time.Time
	}{
		{"2024-01-15T10:30:00Z", time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)},
		{"2024-01-15T10:30:00.123456789Z", time.Date(2024, 1, 15, 10, 30, 0, 123456789, time.UTC)},
		{"2024-01-15T10:30:00.5Z", time.Date(2024, 1, 15, 10, 30, 0, 500000000, time.UTC)},
		{"2024-01-15T10:30:00+02:00", time.Date(2024, 1, 15, 10, 30, 0, 0, time.FixedZone("", 2*3600))},
		{"2024-01-15T10:30:00-05:30", time.Date(2024, 1, 15, 10, 30, 0, 0, time.FixedZone("", -5*3600-30*60))},
		{"2024-02-29T00:00:00Z", time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)},
		{"2000-02-29T23:59:59Z", time.Date(2000, 2, 29, 23, 59, 59, 0, time.UTC)},
		{"1900-02-28T00:00:00Z", time.Date(1900, 2, 28, 0, 0, 0, 0, time.UTC)},
		{"2262-04-11T23:47:16.854775807Z", time.Unix(0, math.MaxInt64)},
		{"1677-09-21T00:12:43.145224192Z", time.Unix(0, math.MinInt64)},
	}
	for _, tc := range tests {
		src := fmt.Sprintf(`{"op":"equal","field":"context.requested_at","value":"%s"}`, tc.ts)
		b, id := decodeExpr(t, src, testInterner(t), Limits{})
		_, _, valueID, ok := b.Document().Compare(id)
		if !ok {
			t.Fatal("Compare missing")
		}
		want := tc.want.UnixNano()
		if v, ok := b.Document().TimestampValue(valueID); !ok || v != want {
			t.Fatalf("TimestampValue(%q) = (%d, %v), want %d", tc.ts, v, ok, want)
		}
	}
}

func TestDecodeExpressionKeyErrorOffsets(t *testing.T) {
	syms := testInterner(t)
	src := `{"op":"equal","field":"context.count","value":1,"bogus":2}`
	rejectExpr(t, src, syms, Limits{}, CodeUnknownKey, offsetOf(t, src, `"bogus"`))

	src = `{"op":"equal","field":"context.count","field":"context.count","value":1}`
	first := offsetOf(t, src, `,"field"`)
	second := offsetOf(t, src[first+1:], `,"field"`)
	rejectExpr(t, src, syms, Limits{}, CodeDuplicateKey, first+1+second+1)

	src = `{"field":"context.count","value":1}`
	rejectExpr(t, src, syms, Limits{}, CodeMissingKey, 0)
}

func TestDecodeExpressionInvalidReferenceOffsets(t *testing.T) {
	syms := testInterner(t)
	src := `{"op":"equal","field":"unknown.field","value":"x"}`
	rejectExpr(t, src, syms, Limits{}, CodeInvalidReference, offsetOf(t, src, `"unknown.field"`))

	src = `{"op":"evidence_matches","kind":"nope","state":"current","explanation":{"issue":"{evidence_kind}"}}`
	rejectExpr(t, src, syms, Limits{}, CodeInvalidReference, offsetOf(t, src, `"nope"`))

	src = `{"op":"evidence_matches","kind":"approval_record","state":"nope","explanation":{"issue":"{evidence_kind}"}}`
	b := catalogBuilder(t, src, []string{"approval_record"}, []string{"current"})
	rejectExprInto(t, b, src, syms, Limits{}, CodeInvalidReference, offsetOf(t, src, `"nope"`))

	src = `{"op":"equal","field":"unknown\u002efield","value":"x"}`
	rejectExpr(t, src, syms, Limits{}, CodeInvalidReference, offsetOf(t, src, `"unknown\u002efield"`))
}

func TestDecodeExpressionRejects(t *testing.T) {
	syms := testInterner(t)
	tests := []struct {
		name    string
		src     string
		code    ErrorCode
		catalog bool
	}{
		{"duplicate op", `{"op":"equal","op":"equal","field":"context.count","value":1}`, CodeDuplicateKey, false},
		{"duplicate field", `{"op":"equal","field":"context.count","field":"context.count","value":1}`, CodeDuplicateKey, false},
		{"duplicate value", `{"op":"equal","field":"context.count","value":1,"value":2}`, CodeDuplicateKey, false},
		{"duplicate values", `{"op":"in","field":"context.usage","values":["a"],"values":["b"]}`, CodeDuplicateKey, false},
		{"duplicate args", `{"op":"all","args":[{"op":"exists","field":"context.environment"}],"args":[{"op":"exists","field":"context.usage"}]}`, CodeDuplicateKey, false},
		{"duplicate kind", `{"op":"evidence_matches","kind":"approval_record","kind":"approval_record","state":"current"}`, CodeDuplicateKey, true},
		{"unknown key", `{"op":"equal","field":"context.count","value":1,"bogus":2}`, CodeUnknownKey, false},
		{"missing op", `{"field":"context.count","value":1}`, CodeMissingKey, false},
		{"empty object", `{}`, CodeMissingKey, false},
		{"missing field", `{"op":"equal","value":1}`, CodeInvalidArity, false},
		{"missing value", `{"op":"equal","field":"context.count"}`, CodeInvalidArity, false},
		{"missing field on exists", `{"op":"exists"}`, CodeInvalidArity, false},
		{"missing arg on not", `{"op":"not"}`, CodeInvalidArity, false},
		{"missing args on all", `{"op":"all"}`, CodeInvalidArity, false},
		{"missing values on in", `{"op":"in","field":"context.usage"}`, CodeInvalidArity, false},
		{"missing kind on evidence", `{"op":"evidence_matches","state":"current"}`, CodeInvalidArity, true},
		{"missing state on evidence", `{"op":"evidence_matches","kind":"approval_record"}`, CodeInvalidArity, true},
		{"wrong key args on not", `{"op":"not","args":[{"op":"exists","field":"context.environment"}]}`, CodeInvalidArity, false},
		{"wrong key arg on all", `{"op":"all","arg":{"op":"exists","field":"context.environment"}}`, CodeInvalidArity, false},
		{"wrong key values on equal", `{"op":"equal","field":"context.count","values":[1]}`, CodeInvalidArity, false},
		{"wrong key value on exists", `{"op":"exists","field":"context.environment","value":1}`, CodeInvalidArity, false},
		{"wrong key kind on equal", `{"op":"equal","field":"context.count","value":1,"kind":"x"}`, CodeInvalidArity, false},
		{"wrong key field on evidence", `{"op":"evidence_matches","field":"subject.trust","kind":"approval_record","state":"current"}`, CodeInvalidArity, true},
		{"empty all args", `{"op":"all","args":[]}`, CodeInvalidArity, false},
		{"empty any args", `{"op":"any","args":[]}`, CodeInvalidArity, false},
		{"empty in values", `{"op":"in","field":"context.usage","values":[]}`, CodeInvalidArity, false},
		{"unknown op", `{"op":"xor","field":"context.count","value":1}`, CodeMalformed, false},
		{"unknown field", `{"op":"equal","field":"unknown.field","value":"x"}`, CodeInvalidReference, false},
		{"unknown evidence kind", `{"op":"evidence_matches","kind":"nope","state":"current","explanation":{"issue":"{evidence_kind}"}}`, CodeInvalidReference, false},
		{"unknown evidence state", `{"op":"evidence_matches","kind":"approval_record","state":"nope","explanation":{"issue":"{evidence_kind}"}}`, CodeInvalidReference, true},
		{"string value for integer field", `{"op":"equal","field":"context.count","value":"1"}`, CodeInvalidType, false},
		{"integer value for symbol field", `{"op":"equal","field":"subject.trust","value":5}`, CodeInvalidType, false},
		{"bool value for integer field", `{"op":"equal","field":"context.count","value":true}`, CodeInvalidType, false},
		{"string value for boolean field", `{"op":"equal","field":"context.enabled","value":"true"}`, CodeInvalidType, false},
		{"integer value for boolean field", `{"op":"equal","field":"context.enabled","value":1}`, CodeInvalidType, false},
		{"presence field with value", `{"op":"equal","field":"context.environment","value":1}`, CodeInvalidType, false},
		{"presence field in values", `{"op":"in","field":"context.environment","values":["x"]}`, CodeInvalidType, false},
		{"null value", `{"op":"equal","field":"subject.trust","value":null}`, CodeInvalidType, false},
		{"null in values", `{"op":"in","field":"context.usage","values":[null]}`, CodeInvalidType, false},
		{"array value", `{"op":"equal","field":"subject.trust","value":[1]}`, CodeInvalidType, false},
		{"object value", `{"op":"equal","field":"subject.trust","value":{}}`, CodeInvalidType, false},
		{"mixed in types", `{"op":"in","field":"context.count","values":[1,"two"]}`, CodeInvalidType, false},
		{"bool in integer values", `{"op":"in","field":"context.count","values":[1,true]}`, CodeInvalidType, false},
		{"non-object arg", `{"op":"not","arg":5}`, CodeInvalidType, false},
		{"non-object args element", `{"op":"all","args":[5]}`, CodeInvalidType, false},
		{"integer fraction", `{"op":"equal","field":"context.count","value":1.5}`, CodeMalformed, false},
		{"integer exponent", `{"op":"equal","field":"context.count","value":1e3}`, CodeMalformed, false},
		{"integer overflow", `{"op":"equal","field":"context.count","value":9223372036854775808}`, CodeLimit, false},
		{"integer underflow", `{"op":"equal","field":"context.count","value":-9223372036854775809}`, CodeLimit, false},
		{"in integer fraction", `{"op":"in","field":"context.count","values":[1,2.5]}`, CodeMalformed, false},
		{"trailing comma", `{"op":"equal","field":"context.count","value":1,}`, CodeMalformed, false},
		{"missing colon", `{"op" "equal","field":"context.count","value":1}`, CodeMalformed, false},
		{"bad month", `{"op":"equal","field":"context.requested_at","value":"2024-13-01T10:00:00Z"}`, CodeMalformed, false},
		{"zero month", `{"op":"equal","field":"context.requested_at","value":"2024-00-10T10:00:00Z"}`, CodeMalformed, false},
		{"zero day", `{"op":"equal","field":"context.requested_at","value":"2024-01-00T10:00:00Z"}`, CodeMalformed, false},
		{"bad day for month", `{"op":"equal","field":"context.requested_at","value":"2024-04-31T10:00:00Z"}`, CodeMalformed, false},
		{"feb 29 non-leap", `{"op":"equal","field":"context.requested_at","value":"2023-02-29T10:00:00Z"}`, CodeMalformed, false},
		{"feb 29 non-leap century", `{"op":"equal","field":"context.requested_at","value":"1900-02-29T10:00:00Z"}`, CodeMalformed, false},
		{"hour 24", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T24:00:00Z"}`, CodeMalformed, false},
		{"minute 60", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:60:00Z"}`, CodeMalformed, false},
		{"second 60", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:60Z"}`, CodeMalformed, false},
		{"zone hour 24", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:00+24:00"}`, CodeMalformed, false},
		{"zone minute 60", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:00+02:60"}`, CodeMalformed, false},
		{"fraction 10 digits", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:00.1234567890Z"}`, CodeMalformed, false},
		{"empty fraction", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:00.Z"}`, CodeMalformed, false},
		{"missing zone", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:00"}`, CodeMalformed, false},
		{"space separator", `{"op":"equal","field":"context.requested_at","value":"2024-01-15 10:30:00Z"}`, CodeMalformed, false},
		{"not a timestamp", `{"op":"equal","field":"context.requested_at","value":"hello"}`, CodeMalformed, false},
		{"timestamp trailing junk", `{"op":"equal","field":"context.requested_at","value":"2024-01-15T10:30:00Zjunk"}`, CodeMalformed, false},
		{"timestamp ns overflow", `{"op":"equal","field":"context.requested_at","value":"2262-04-11T23:47:16.854775808Z"}`, CodeLimit, false},
		{"timestamp year 9999 overflow", `{"op":"equal","field":"context.requested_at","value":"9999-12-31T23:59:59Z"}`, CodeLimit, false},
		{"timestamp year 0000 overflow", `{"op":"equal","field":"context.requested_at","value":"0000-01-01T00:00:00Z"}`, CodeLimit, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.catalog {
				b := catalogBuilder(t, tc.src, []string{"approval_record"}, []string{"current"})
				rejectExprInto(t, b, tc.src, syms, Limits{}, tc.code, -1)
				return
			}
			rejectExpr(t, tc.src, syms, Limits{}, tc.code, -1)
		})
	}
}

func TestDecodeExpressionLimits(t *testing.T) {
	syms := testInterner(t)
	tests := []struct {
		name   string
		src    string
		limits Limits
	}{
		{"depth", `{"op":"all","args":[{"op":"all","args":[{"op":"exists","field":"context.environment"}]}]}`, Limits{MaxDepth: 2}},
		{"nodes", `{"op":"all","args":[{"op":"exists","field":"context.environment"},{"op":"exists","field":"context.usage"}]}`, Limits{MaxNodes: 1}},
		{"nodes all parent", `{"op":"all","args":[{"op":"exists","field":"context.environment"}]}`, Limits{MaxNodes: 1}},
		{"nodes not parent", `{"op":"not","arg":{"op":"exists","field":"context.environment"}}`, Limits{MaxNodes: 1}},
		{"values in", `{"op":"in","field":"context.usage","values":["a","b"]}`, Limits{MaxValues: 1}},
		{"values compare", `{"op":"all","args":[{"op":"equal","field":"context.count","value":1},{"op":"equal","field":"context.count","value":2}]}`, Limits{MaxValues: 1}},
		{"array items args", `{"op":"all","args":[{"op":"exists","field":"context.environment"},{"op":"exists","field":"context.usage"}]}`, Limits{MaxArrayItems: 1}},
		{"array items values", `{"op":"in","field":"context.usage","values":["a","b"]}`, Limits{MaxArrayItems: 1}},
		{"string bytes", `{"op":"equal","field":"subject.trust","value":"external-extra-long"}`, Limits{MaxStringBytes: 13}},
		{"string bytes unicode escape", `{"op":"equal","field":"subject.trust","value":"a\u00e9b"}`, Limits{MaxStringBytes: 3}},
		{"string bytes raw utf8", `{"op":"equal","field":"subject.trust","value":"café"}`, Limits{MaxStringBytes: 4}},
		{"symbol bytes", `{"op":"equal","field":"subject.trust","value":"external"}`, Limits{MaxSymbolBytes: 4}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rejectExpr(t, tc.src, syms, tc.limits, CodeLimit, -1)
		})
	}
}

func TestDecodeExpressionDeterministic(t *testing.T) {
	src := `{"op":"all","args":[{"op":"any","args":[{"op":"equal","field":"context.count","value":1},{"op":"not","arg":{"op":"in","field":"context.usage","values":["a","b"]}}]},{"op":"equal","field":"subject.trust","value":"external"}]}`
	syms := testInterner(t)
	b1, _ := decodeExpr(t, src, syms, Limits{})
	b2, _ := decodeExpr(t, src, syms, Limits{})
	d1, d2 := b1.Document(), b2.Document()
	if !sliceEqual(d1.NodeKinds, d2.NodeKinds) {
		t.Fatalf("NodeKinds differ: %v vs %v", d1.NodeKinds, d2.NodeKinds)
	}
	if !sliceEqual(d1.NodeRefs, d2.NodeRefs) {
		t.Fatalf("NodeRefs differ: %v vs %v", d1.NodeRefs, d2.NodeRefs)
	}
	if !sliceEqual(d1.CompareFields, d2.CompareFields) || !sliceEqual(d1.CompareOps, d2.CompareOps) || !sliceEqual(d1.CompareValues, d2.CompareValues) {
		t.Fatalf("compare payloads differ")
	}
	if !sliceEqual(d1.CompareListStarts, d2.CompareListStarts) || !sliceEqual(d1.CompareListCounts, d2.CompareListCounts) || !sliceEqual(d1.ListValueIDs, d2.ListValueIDs) {
		t.Fatalf("in CSR differs")
	}
	if !sliceEqual(d1.GroupChildStarts, d2.GroupChildStarts) || !sliceEqual(d1.GroupChildCounts, d2.GroupChildCounts) || !sliceEqual(d1.ChildNodeIDs, d2.ChildNodeIDs) {
		t.Fatalf("group CSR differs")
	}
	if !sliceEqual(d1.NotChildren, d2.NotChildren) {
		t.Fatalf("NotChildren differ")
	}
	if !sliceEqual(d1.SourceStarts, d2.SourceStarts) || !sliceEqual(d1.SourceEnds, d2.SourceEnds) {
		t.Fatalf("spans differ")
	}
	if !sliceEqual(d1.ValueKinds, d2.ValueKinds) || !sliceEqual(d1.ValueRefs, d2.ValueRefs) {
		t.Fatalf("value payloads differ")
	}
	if !sliceEqual(d1.SymbolStarts, d2.SymbolStarts) || !sliceEqual(d1.SymbolLengths, d2.SymbolLengths) || !bytesEqual(d1.SymbolBytes, d2.SymbolBytes) {
		t.Fatalf("symbol slabs differ")
	}
	if !sliceEqual(d1.IntegerValues, d2.IntegerValues) || !sliceEqual(d1.BooleanValues, d2.BooleanValues) || !sliceEqual(d1.TimestampValues, d2.TimestampValues) {
		t.Fatalf("scalar values differ")
	}
}

func TestDecodeExpressionInternerUnchanged(t *testing.T) {
	syms := testInterner(t)
	before := syms.Len()
	beforeBytes := syms.ByteLen()
	decodeExpr(t, `{"op":"equal","field":"subject.trust","value":"external"}`, syms, Limits{})
	if syms.Len() != before || syms.ByteLen() != beforeBytes {
		t.Fatalf("successful decode mutated interner: len %d->%d, bytes %d->%d", before, syms.Len(), beforeBytes, syms.ByteLen())
	}
	if _, ok := syms.Lookup([]byte("external")); ok {
		t.Fatal("literal external was interned")
	}
	rejectExpr(t, `{"op":"equal","field":"unknown.field","value":"x"}`, syms, Limits{}, CodeInvalidReference, -1)
	if syms.Len() != before || syms.ByteLen() != beforeBytes {
		t.Fatalf("failed decode mutated interner: len %d->%d, bytes %d->%d", before, syms.Len(), beforeBytes, syms.ByteLen())
	}
}

func sliceEqual[E comparable](a, b []E) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bytesEqual(a, b []byte) bool {
	return sliceEqual(a, b)
}
