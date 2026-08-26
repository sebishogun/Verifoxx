package compile

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

// semFields is the fixed five-kind field schema used by the semantic tests:
// symbol, integer, boolean, timestamp, and presence, in that registration
// order.
type semFields struct {
	schema                                        *schema.Schema
	symbol, integer, boolean, timestamp, presence schema.FieldID
}

func semSchema(t *testing.T) semFields {
	t.Helper()
	syms := schema.NewSymbolInterner(16)
	fb := schema.NewBuilder()
	add := func(name string, kind schema.ValueKind, group schema.FieldGroup) schema.FieldID {
		id, err := syms.Intern([]byte(name))
		if err != nil {
			t.Fatal(err)
		}
		fid, err := fb.AddField(id, kind, group)
		if err != nil {
			t.Fatal(err)
		}
		return fid
	}
	sf := semFields{}
	sf.symbol = add("subject.trust", schema.ValueKindSymbol, schema.FieldGroupSubject)
	sf.integer = add("subject.count", schema.ValueKindInteger, schema.FieldGroupSubject)
	sf.boolean = add("subject.flag", schema.ValueKindBoolean, schema.FieldGroupSubject)
	sf.timestamp = add("subject.when", schema.ValueKindTimestamp, schema.FieldGroupSubject)
	sf.presence = add("context.env", schema.ValueKindPresence, schema.FieldGroupContext)
	sf.schema = fb.Finish()
	return sf
}

// semLit holds one literal ValueID of each kind from a fresh semantic doc.
type semLit struct {
	symbol, integer, boolean, timestamp schema.ValueID
}

// newSemDoc returns a source-bound builder with one literal of each kind and
// a fresh five-field schema. Tests add nodes, mutate the returned document,
// then validate through the public path.
func newSemDoc(t *testing.T) (*ast.Builder, semFields, semLit, ast.SourceSpan) {
	t.Helper()
	sf := semSchema(t)
	source := []byte("semantic source bytes")
	ab := ast.NewBuilder(ast.Hints{
		Nodes: 8, CompareNodes: 8, CompareListValues: 8, Values: 8,
		SymbolValues: 8, SymbolBytes: 128, IntegerValues: 4,
		BooleanValues: 4, TimestampValues: 4, SourceBytes: len(source),
	})
	if err := ab.SetSource(source); err != nil {
		t.Fatal(err)
	}
	var lit semLit
	var err error
	if lit.symbol, err = ab.AddSymbolValue([]byte("full")); err != nil {
		t.Fatal(err)
	}
	if lit.integer, err = ab.AddIntegerValue(7); err != nil {
		t.Fatal(err)
	}
	if lit.boolean, err = ab.AddBooleanValue(true); err != nil {
		t.Fatal(err)
	}
	if lit.timestamp, err = ab.AddTimestampValue(12345); err != nil {
		t.Fatal(err)
	}
	return ab, sf, lit, ast.SourceSpan{Start: 0, End: 1}
}

// TestValidateSemanticAllArity covers the Task 7 milestone-3 All rule: a
// structurally safe All node with a valid empty group CSR emits exactly one
// arity diagnostic on the group payload row owned by the node, while a
// nonempty group is clean and a bad payload ref or CSR range stays
// structural-only.
func TestValidateSemanticAllArity(t *testing.T) {
	t.Run("empty-all-invalid", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddGroup(ast.NodeKindAll, nil, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 1, Node: 1, Span: span},
		})
	})
}

// TestValidateSemanticAnyArity covers the Any rule with the same diagnostic
// shape as All: an empty Any emits one arity diagnostic, a nonempty Any is
// clean.
func TestValidateSemanticAnyArity(t *testing.T) {
	t.Run("empty-any-invalid", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddGroup(ast.NodeKindAny, nil, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 1, Node: 1, Span: span},
		})
	})
	t.Run("nonempty-any-clean", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		c, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ab.AddGroup(ast.NodeKindAny, []schema.NodeID{c}, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
	})
}

func TestValidateSemanticGroupNonemptyClean(t *testing.T) {
	t.Run("all", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		c, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ab.AddGroup(ast.NodeKindAll, []schema.NodeID{c}, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
	})
	t.Run("any", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		c, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ab.AddGroup(ast.NodeKindAny, []schema.NodeID{c}, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
	})
}

// TestValidateSemanticGroupOrdering locks the seeded-prefix and ascending
// NodeID ordering of the single expression scan across interleaved compare,
// empty All, and empty Any defects.
func TestValidateSemanticGroupOrdering(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddGroup(ast.NodeKindAll, nil, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddGroup(ast.NodeKindAny, nil, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.CompareValues[0] = 0
	doc.CompareValues[1] = 0
	seed := Diagnostic{Code: CodeCycle}
	var v Validator
	want(t, v.validateNoGraph([]Diagnostic{seed}, doc, sf.schema), []Diagnostic{
		seed,
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span},
		{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 1, Node: 2, Span: span},
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 2, Node: 3, Span: span},
		{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 2, Node: 4, Span: span},
	})
}

// TestValidateSemanticGroupAliasedPayload makes two structurally safe owner
// nodes alias one empty group payload row through public mutation: each owner
// emits its own arity diagnostic in ascending NodeID with the same payload
// Row but a distinct Node, while the second group row becomes unreferenced and
// stays inert.
func TestValidateSemanticGroupAliasedPayload(t *testing.T) {
	ab, sf, _, span := newSemDoc(t)
	if _, err := ab.AddGroup(ast.NodeKindAll, nil, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddGroup(ast.NodeKindAny, nil, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.NodeRefs[1] = 0
	var v Validator
	want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
		{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 1, Node: 1, Span: span},
		{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 1, Node: 2, Span: span},
	})
}

// TestValidateSemanticGroupUnreferencedInert proves a group payload row no
// node references emits no semantic diagnostic: only the referenced empty row
// is diagnosed.
func TestValidateSemanticGroupUnreferencedInert(t *testing.T) {
	ab, sf, _, span := newSemDoc(t)
	if _, err := ab.AddGroup(ast.NodeKindAll, nil, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.GroupChildStarts = append(doc.GroupChildStarts, 0)
	doc.GroupChildCounts = append(doc.GroupChildCounts, 0)
	var v Validator
	want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
		{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: 1, Node: 1, Span: span},
	})
}

// TestValidateSemanticGroupStructuralOnly proves group defects stay
// structural-only: a truncated node peer, an out-of-range payload ref, an
// invalid CSR range, and an invalid child inside a valid nonempty range each
// produce exactly their structural diagnostic with no semantic arity
// diagnostic.
func TestValidateSemanticGroupStructuralOnly(t *testing.T) {
	check := func(t *testing.T, doc *ast.Document, fields *schema.Schema, wantDiags []Diagnostic) {
		t.Helper()
		var v Validator
		want(t, v.validateNoGraph(nil, doc, fields), wantDiags)
	}
	t.Run("node peer truncated", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddGroup(ast.NodeKindAll, nil, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.NodeRefs = doc.NodeRefs[:0]
		check(t, doc, sf.schema, []Diagnostic{
			{Code: CodeColumnLength, Table: TableNode},
		})
	})
	t.Run("bad payload ref", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddGroup(ast.NodeKindAll, nil, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.NodeRefs[0] = 99
		check(t, doc, sf.schema, []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 1, Node: 1, Span: span},
		})
	})
	t.Run("invalid group CSR", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddGroup(ast.NodeKindAny, nil, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.GroupChildStarts[0] = math.MaxUint32
		check(t, doc, sf.schema, []Diagnostic{
			{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 1},
		})
	})
	t.Run("invalid child in valid range", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddGroup(ast.NodeKindAll, []schema.NodeID{1}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ChildNodeIDs[0] = schema.NodeID(len(doc.NodeKinds) + 1)
		check(t, doc, sf.schema, []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 1, Node: schema.NodeID(len(doc.NodeKinds) + 1)},
		})
	})
}

func TestValidateSemanticInvalidCompareOp(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.CompareOps[0] = ast.CompareOp(99)
	var v Validator
	want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberOperation, Row: 1, Node: 1, Span: span},
	})
}

func TestValidateSemanticExistsArity(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddExists(sf.symbol, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
	})
	t.Run("scalar extra", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddExists(sf.symbol, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareValues[0] = lit.symbol
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit.symbol},
		})
	})
	t.Run("list extra", func(t *testing.T) {
		ab, sf, _, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{1}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareOps[0] = ast.CompareOpExists
		doc.CompareValues[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
		})
	})
	t.Run("scalar and list extra", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{1}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareOps[0] = ast.CompareOpExists
		doc.CompareValues[0] = lit.symbol
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit.symbol},
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
		})
	})
}

func TestValidateSemanticInArity(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
	})
	t.Run("scalar extra", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareValues[0] = lit.symbol
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit.symbol},
		})
	})
	t.Run("empty list", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareListCounts[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
		})
	})
	t.Run("scalar extra and empty list", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareValues[0] = lit.symbol
		doc.CompareListCounts[0] = 0
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit.symbol},
			{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
		})
	})
}

// scalarOps is every compare operation that requires exactly one nonzero
// scalar value and an empty list.
var scalarOps = [...]ast.CompareOp{
	ast.CompareOpEqual, ast.CompareOpNotEqual,
	ast.CompareOpLess, ast.CompareOpLessEqual,
	ast.CompareOpGreater, ast.CompareOpGreaterEqual,
}

func TestValidateSemanticScalarCompareArity(t *testing.T) {
	for _, op := range scalarOps {
		t.Run(fmt.Sprintf("op-%d", op), func(t *testing.T) {
			t.Run("valid", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				field, value := sf.symbol, lit.symbol
				switch op {
				case ast.CompareOpLess, ast.CompareOpLessEqual, ast.CompareOpGreater, ast.CompareOpGreaterEqual:
					field, value = sf.integer, lit.integer
				}
				if _, err := ab.AddCompare(field, op, value, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
			})
			t.Run("missing scalar", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.symbol, op, lit.symbol, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareValues[0] = 0
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span},
				})
			})
			t.Run("list extra", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareOps[0] = op
				doc.CompareValues[0] = lit.symbol
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
				})
			})
			t.Run("missing scalar and list extra", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareOps[0] = op
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span},
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
				})
			})
		})
	}
}

func TestValidateSemanticMultipleDefectsOrder(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpExists, 0, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.CompareValues[0] = lit.symbol
	doc.CompareValues[1] = 0
	doc.CompareListCounts[2] = 0
	doc.CompareOps[3] = ast.CompareOpLess
	doc.CompareValues[3] = lit.integer

	seed := Diagnostic{Code: CodeCycle}
	var v Validator
	got := v.validateNoGraph([]Diagnostic{seed}, doc, sf.schema)
	want(t, got, []Diagnostic{
		seed,
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit.symbol},
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 2, Node: 2, Span: span},
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 3, Node: 3, Span: span},
		{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 4, Node: 4, Span: span},
	})
}

func TestValidateSemanticNoStructuralCascade(t *testing.T) {
	check := func(t *testing.T, doc *ast.Document, fields *schema.Schema) {
		t.Helper()
		var pub Validator
		pubGot := pub.validateNoGraph(nil, doc, fields)
		var phase1 Validator
		structGot := phase1.validateStructure(nil, doc, fields)
		if len(structGot) == 0 {
			t.Fatal("mutation produced no structural diagnostic")
		}
		if !reflect.DeepEqual(pubGot, structGot) {
			t.Fatalf("semantic cascade: public %+v != structural %+v", pubGot, structGot)
		}
	}
	t.Run("field invalid", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareFields[0] = 0
		check(t, doc, sf.schema)
	})
	t.Run("value out of range", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareValues[0] = schema.ValueID(len(doc.ValueKinds) + 1)
		check(t, doc, sf.schema)
	})
	t.Run("list CSR invalid", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.CompareListStarts[0] = math.MaxUint32
		check(t, doc, sf.schema)
	})
	t.Run("literal kind invalid", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ValueKinds[0] = schema.ValueKindPresence
		check(t, doc, sf.schema)
	})
	t.Run("node kind invalid", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.NodeKinds[0] = ast.NodeKindInvalid
		check(t, doc, sf.schema)
	})
	t.Run("node ref invalid", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.NodeRefs[0] = 99
		check(t, doc, sf.schema)
	})
}

func TestValidateSemanticCanonicalDocsPublicZero(t *testing.T) {
	docs := []func(t *testing.T) (*ast.Document, *schema.Schema){
		func(t *testing.T) (*ast.Document, *schema.Schema) { return fixture(t) },
		buildMinimal,
		buildInDoc,
		buildCatalogDoc,
	}
	for i, build := range docs {
		t.Run(acceptedDocName(i), func(t *testing.T) {
			doc, fields := build(t)
			var v Validator
			want(t, v.Validate(nil, doc, fields), nil)
		})
	}
}

func TestValidateSemanticValidatorReuse(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.CompareValues[0] = 0
	var v Validator
	if got := v.validateNoGraph(nil, doc, sf.schema); len(got) != 1 {
		t.Fatalf("defect doc produced %d diagnostics, want 1: %+v", len(got), got)
	}
	clean, cleanFields := buildMinimal(t)
	if got := v.validateNoGraph(nil, clean, cleanFields); len(got) != 0 {
		t.Fatalf("reused validator produced %d diagnostics on clean doc: %+v", len(got), got)
	}
}

func TestValidateSemanticDoesNotModifyInputs(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	doc := ab.Document()
	doc.CompareValues[0] = 0

	ab2, _, lit2, _ := newSemDoc(t)
	if _, err := ab2.AddCompare(sf.symbol, ast.CompareOpEqual, lit2.symbol, ast.SourceSpan{Start: 0, End: 1}); err != nil {
		t.Fatal(err)
	}
	wantDoc := ab2.Document()
	wantDoc.CompareValues[0] = 0

	var v Validator
	v.Validate(nil, doc, sf.schema)
	if !reflect.DeepEqual(*doc, *wantDoc) {
		t.Fatal("Validate mutated ast.Document")
	}
}

func TestValidateSemanticEqualNotEqualScalarType(t *testing.T) {
	for _, op := range [...]ast.CompareOp{ast.CompareOpEqual, ast.CompareOpNotEqual} {
		t.Run(fmt.Sprintf("op-%d", op), func(t *testing.T) {
			t.Run("all-kinds-match", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				adds := [...]struct {
					field schema.FieldID
					lit   schema.ValueID
				}{
					{sf.symbol, lit.symbol},
					{sf.integer, lit.integer},
					{sf.boolean, lit.boolean},
					{sf.timestamp, lit.timestamp},
				}
				for _, a := range adds {
					if _, err := ab.AddCompare(a.field, op, a.lit, span); err != nil {
						t.Fatal(err)
					}
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
			})
			t.Run("kind-mismatch-ordering", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				cases := []struct {
					field schema.FieldID
					value schema.ValueID
				}{
					{sf.symbol, lit.integer},
					{sf.integer, lit.symbol},
					{sf.boolean, lit.timestamp},
					{sf.timestamp, lit.boolean},
				}
				var wantDiags []Diagnostic
				for i, c := range cases {
					if _, err := ab.AddCompare(c.field, op, c.value, span); err != nil {
						t.Fatal(err)
					}
					wantDiags = append(wantDiags, Diagnostic{
						Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue,
						Row: uint32(i + 1), Node: schema.NodeID(i + 1), Span: span,
						Field: c.field, Value: c.value,
					})
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), wantDiags)
			})
			t.Run("presence-field-mismatch", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.presence, op, lit.symbol, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
					{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Field: sf.presence, Value: lit.symbol},
				})
			})
			t.Run("missing-scalar-no-cascade", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.symbol, op, lit.integer, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareValues[0] = 0
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span},
				})
			})
			t.Run("list-extra-no-cascade", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareOps[0] = op
				doc.CompareValues[0] = lit.integer
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
				})
			})
			t.Run("no-structural-cascade", func(t *testing.T) {
				check := func(t *testing.T, doc *ast.Document, fields *schema.Schema) {
					t.Helper()
					var pub Validator
					pubGot := pub.validateNoGraph(nil, doc, fields)
					var phase1 Validator
					structGot := phase1.validateStructure(nil, doc, fields)
					if len(structGot) == 0 {
						t.Fatal("mutation produced no structural diagnostic")
					}
					if !reflect.DeepEqual(pubGot, structGot) {
						t.Fatalf("semantic cascade: public %+v != structural %+v", pubGot, structGot)
					}
				}
				base := func(t *testing.T) (*ast.Document, semFields, semLit, ast.SourceSpan) {
					ab, sf, lit, span := newSemDoc(t)
					if _, err := ab.AddCompare(sf.symbol, op, lit.integer, span); err != nil {
						t.Fatal(err)
					}
					return ab.Document(), sf, lit, span
				}
				t.Run("field invalid", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.CompareFields[0] = 0
					check(t, doc, sf.schema)
				})
				t.Run("field out of range", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.CompareFields[0] = schema.FieldID(sf.schema.Len() + 1)
					check(t, doc, sf.schema)
				})
				t.Run("value out of range", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.CompareValues[0] = schema.ValueID(len(doc.ValueKinds) + 1)
					check(t, doc, sf.schema)
				})
				t.Run("literal kind invalid", func(t *testing.T) {
					doc, sf, lit, _ := base(t)
					doc.ValueKinds[lit.integer-1] = schema.ValueKindPresence
					check(t, doc, sf.schema)
				})
				t.Run("value kinds shortened", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.ValueKinds = doc.ValueKinds[:0]
					check(t, doc, sf.schema)
				})
				t.Run("value refs shortened", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.ValueRefs = doc.ValueRefs[:0]
					check(t, doc, sf.schema)
				})
				t.Run("list CSR invalid", func(t *testing.T) {
					ab, sf, lit, span := newSemDoc(t)
					if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol}, span); err != nil {
						t.Fatal(err)
					}
					doc := ab.Document()
					doc.CompareOps[0] = op
					doc.CompareValues[0] = lit.integer
					doc.CompareListStarts[0] = math.MaxUint32
					check(t, doc, sf.schema)
				})
			})
		})
	}
}

func TestValidateSemanticInListKind(t *testing.T) {
	t.Run("all-kinds-match", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		adds := [...]struct {
			field schema.FieldID
			lit   schema.ValueID
		}{
			{sf.symbol, lit.symbol},
			{sf.integer, lit.integer},
			{sf.boolean, lit.boolean},
			{sf.timestamp, lit.timestamp},
		}
		for _, a := range adds {
			if _, err := ab.AddIn(a.field, []schema.ValueID{a.lit}, span); err != nil {
				t.Fatal(err)
			}
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
	})
	t.Run("presence-field", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.presence, []schema.ValueID{lit.symbol, lit.integer}, span); err != nil {
			t.Fatal(err)
		}
		var v Validator
		want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
			{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 1, Node: 1, Span: span, Field: sf.presence},
		})
	})
	t.Run("seeded-multi-node-csr-order", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.integer}, span); err != nil {
			t.Fatal(err)
		}
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol, lit.integer, lit.boolean}, span); err != nil {
			t.Fatal(err)
		}
		if _, err := ab.AddIn(sf.integer, []schema.ValueID{lit.integer}, span); err != nil {
			t.Fatal(err)
		}
		seed := Diagnostic{Code: CodeCycle}
		var v Validator
		want(t, v.validateNoGraph([]Diagnostic{seed}, ab.Document(), sf.schema), []Diagnostic{
			seed,
			{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span, Field: sf.symbol, Value: lit.integer},
			{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValues, Row: 2, Node: 2, Span: span, Field: sf.symbol, Value: lit.integer},
			{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValues, Row: 2, Node: 2, Span: span, Field: sf.symbol, Value: lit.boolean},
		})
	})
	t.Run("mixed-entries-no-cascade", func(t *testing.T) {
		ab, sf, lit, span := newSemDoc(t)
		if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.symbol, lit.integer, lit.boolean}, span); err != nil {
			t.Fatal(err)
		}
		doc := ab.Document()
		doc.ListValueIDs = []schema.ValueID{
			lit.symbol, lit.integer, 0,
			schema.ValueID(len(doc.ValueKinds) + 1), lit.boolean,
		}
		doc.CompareListCounts[0] = 5
		doc.ValueKinds[lit.boolean-1] = schema.ValueKindPresence
		var v Validator
		want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableValue, Row: 3, Value: lit.boolean},
			{Code: CodeInvalidValue, Table: TableCompare, Row: 1, Value: 0},
			{Code: CodeInvalidValue, Table: TableCompare, Row: 1, Value: schema.ValueID(len(doc.ValueKinds) + 1)},
			{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span, Field: sf.symbol, Value: lit.integer},
		})
	})
	t.Run("suppression", func(t *testing.T) {
		check := func(t *testing.T, doc *ast.Document, fields *schema.Schema) {
			t.Helper()
			var pub Validator
			pubGot := pub.validateNoGraph(nil, doc, fields)
			var phase1 Validator
			structGot := phase1.validateStructure(nil, doc, fields)
			if len(structGot) == 0 {
				t.Fatal("mutation produced no structural diagnostic")
			}
			if !reflect.DeepEqual(pubGot, structGot) {
				t.Fatalf("semantic cascade: public %+v != structural %+v", pubGot, structGot)
			}
		}
		base := func(t *testing.T) (*ast.Document, semFields, semLit, ast.SourceSpan) {
			ab, sf, lit, span := newSemDoc(t)
			if _, err := ab.AddIn(sf.symbol, []schema.ValueID{lit.integer}, span); err != nil {
				t.Fatal(err)
			}
			return ab.Document(), sf, lit, span
		}
		t.Run("field invalid", func(t *testing.T) {
			doc, sf, _, _ := base(t)
			doc.CompareFields[0] = 0
			check(t, doc, sf.schema)
		})
		t.Run("field out of range", func(t *testing.T) {
			doc, sf, _, _ := base(t)
			doc.CompareFields[0] = schema.FieldID(sf.schema.Len() + 1)
			check(t, doc, sf.schema)
		})
		t.Run("list CSR invalid", func(t *testing.T) {
			doc, sf, _, _ := base(t)
			doc.CompareListStarts[0] = math.MaxUint32
			check(t, doc, sf.schema)
		})
		t.Run("empty list", func(t *testing.T) {
			doc, sf, _, span := base(t)
			doc.CompareListCounts[0] = 0
			var v Validator
			want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
				{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
			})
		})
		t.Run("scalar extra", func(t *testing.T) {
			doc, sf, lit, span := base(t)
			doc.CompareValues[0] = lit.symbol
			var v Validator
			want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
				{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Value: lit.symbol},
			})
		})
	})
}

func TestValidateSemanticOrderedOps(t *testing.T) {
	ops := [...]ast.CompareOp{
		ast.CompareOpLess, ast.CompareOpLessEqual,
		ast.CompareOpGreater, ast.CompareOpGreaterEqual,
	}
	for _, op := range ops {
		t.Run(fmt.Sprintf("op-%d", op), func(t *testing.T) {
			t.Run("integer-valid", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.integer, op, lit.integer, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
			})
			t.Run("timestamp-valid", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.timestamp, op, lit.timestamp, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), nil)
			})
			t.Run("symbol-field", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.symbol, op, lit.symbol, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
					{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 1, Node: 1, Span: span, Field: sf.symbol},
				})
			})
			t.Run("boolean-field", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.boolean, op, lit.boolean, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
					{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 1, Node: 1, Span: span, Field: sf.boolean},
				})
			})
			t.Run("presence-field-skips-scalar", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.presence, op, lit.symbol, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
					{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 1, Node: 1, Span: span, Field: sf.presence},
				})
			})
			t.Run("integer-field-timestamp-literal", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.integer, op, lit.timestamp, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
					{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Field: sf.integer, Value: lit.timestamp},
				})
			})
			t.Run("timestamp-field-integer-literal", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.timestamp, op, lit.integer, span); err != nil {
					t.Fatal(err)
				}
				var v Validator
				want(t, v.validateNoGraph(nil, ab.Document(), sf.schema), []Diagnostic{
					{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span, Field: sf.timestamp, Value: lit.integer},
				})
			})
			t.Run("missing-scalar-no-cascade", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddCompare(sf.integer, op, lit.symbol, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareValues[0] = 0
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: 1, Node: 1, Span: span},
				})
			})
			t.Run("list-extra-no-cascade", func(t *testing.T) {
				ab, sf, lit, span := newSemDoc(t)
				if _, err := ab.AddIn(sf.integer, []schema.ValueID{lit.integer}, span); err != nil {
					t.Fatal(err)
				}
				doc := ab.Document()
				doc.CompareOps[0] = op
				doc.CompareValues[0] = lit.symbol
				var v Validator
				want(t, v.validateNoGraph(nil, doc, sf.schema), []Diagnostic{
					{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: 1, Node: 1, Span: span},
				})
			})
			t.Run("no-structural-cascade", func(t *testing.T) {
				check := func(t *testing.T, doc *ast.Document, fields *schema.Schema) {
					t.Helper()
					var pub Validator
					pubGot := pub.validateNoGraph(nil, doc, fields)
					var phase1 Validator
					structGot := phase1.validateStructure(nil, doc, fields)
					if len(structGot) == 0 {
						t.Fatal("mutation produced no structural diagnostic")
					}
					if !reflect.DeepEqual(pubGot, structGot) {
						t.Fatalf("semantic cascade: public %+v != structural %+v", pubGot, structGot)
					}
				}
				base := func(t *testing.T) (*ast.Document, semFields, semLit, ast.SourceSpan) {
					ab, sf, lit, span := newSemDoc(t)
					if _, err := ab.AddCompare(sf.integer, op, lit.symbol, span); err != nil {
						t.Fatal(err)
					}
					return ab.Document(), sf, lit, span
				}
				t.Run("field invalid", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.CompareFields[0] = 0
					check(t, doc, sf.schema)
				})
				t.Run("field out of range", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.CompareFields[0] = schema.FieldID(sf.schema.Len() + 1)
					check(t, doc, sf.schema)
				})
				t.Run("value out of range", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.CompareValues[0] = schema.ValueID(len(doc.ValueKinds) + 1)
					check(t, doc, sf.schema)
				})
				t.Run("literal kind invalid", func(t *testing.T) {
					doc, sf, lit, _ := base(t)
					doc.ValueKinds[lit.symbol-1] = schema.ValueKindPresence
					check(t, doc, sf.schema)
				})
				t.Run("value kinds shortened", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.ValueKinds = doc.ValueKinds[:0]
					check(t, doc, sf.schema)
				})
				t.Run("value refs shortened", func(t *testing.T) {
					doc, sf, _, _ := base(t)
					doc.ValueRefs = doc.ValueRefs[:0]
					check(t, doc, sf.schema)
				})
				t.Run("list CSR invalid", func(t *testing.T) {
					ab, sf, lit, span := newSemDoc(t)
					if _, err := ab.AddIn(sf.integer, []schema.ValueID{lit.integer}, span); err != nil {
						t.Fatal(err)
					}
					doc := ab.Document()
					doc.CompareOps[0] = op
					doc.CompareValues[0] = lit.symbol
					doc.CompareListStarts[0] = math.MaxUint32
					check(t, doc, sf.schema)
				})
			})
		})
	}
}

func TestValidateSemanticOrderedOpsOrdering(t *testing.T) {
	ab, sf, lit, span := newSemDoc(t)
	if _, err := ab.AddCompare(sf.symbol, ast.CompareOpLess, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddCompare(sf.integer, ast.CompareOpLess, lit.timestamp, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddCompare(sf.timestamp, ast.CompareOpLess, lit.timestamp, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddCompare(sf.presence, ast.CompareOpLessEqual, lit.symbol, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddCompare(sf.boolean, ast.CompareOpGreater, lit.boolean, span); err != nil {
		t.Fatal(err)
	}
	seed := Diagnostic{Code: CodeCycle}
	var v Validator
	want(t, v.validateNoGraph([]Diagnostic{seed}, ab.Document(), sf.schema), []Diagnostic{
		seed,
		{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 1, Node: 1, Span: span, Field: sf.symbol},
		{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue, Row: 2, Node: 2, Span: span, Field: sf.integer, Value: lit.timestamp},
		{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 4, Node: 4, Span: span, Field: sf.presence},
		{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: 5, Node: 5, Span: span, Field: sf.boolean},
	})
}
