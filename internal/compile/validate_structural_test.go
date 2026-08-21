package compile

import (
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// buildMinimal returns a canonical minimal document covering every node kind
// (compare/equal, not, all, any, evidence), every literal value kind, an
// outcome catalog, a complete seven-field clause resolution, clause evidence
// reaching the evidence node, and one requirement with a non-empty clause
// range. Every node is reachable from the requirement: its applicability is
// the Any root and the clause assertion is the All root. It must validate to
// zero diagnostics now and stay free of unrelated diagnostics in later
// semantic and graph phases.
func buildMinimal(t *testing.T) (*ast.Document, *schema.Schema) {
	t.Helper()
	syms := schema.NewSymbolInterner(8)
	fieldSym, err := syms.Intern([]byte("subject.trust"))
	if err != nil {
		t.Fatal(err)
	}
	fb := schema.NewBuilder()
	if _, err := fb.AddField(fieldSym, schema.ValueKindSymbol, schema.FieldGroupSubject); err != nil {
		t.Fatal(err)
	}
	fields := fb.Finish()

	source := []byte("policy source bytes for minimal spans")
	ab := ast.NewBuilder(ast.Hints{
		Nodes: 6, CompareNodes: 2, GroupNodes: 2, ChildEdges: 3, NotNodes: 1,
		EvidenceNodes: 1, Values: 12, SymbolValues: 12, SymbolBytes: 96,
		IntegerValues: 4, BooleanValues: 4, TimestampValues: 4,
		EvidenceKinds: 2, EvidenceStates: 2, Outcomes: 4,
		Clauses: 1, ClauseEvidenceEdges: 1, Requirements: 1, RequirementClauseEdges: 1,
		SourceBytes: len(source),
	})
	if err := ab.SetSource(source); err != nil {
		t.Fatal(err)
	}
	span := ast.SourceSpan{Start: 0, End: 1}

	ekName, err := ab.AddSymbolValue([]byte("approval_record"))
	if err != nil {
		t.Fatal(err)
	}
	esName, err := ab.AddSymbolValue([]byte("current"))
	if err != nil {
		t.Fatal(err)
	}
	sym, err := ab.AddSymbolValue([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIntegerValue(42); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddBooleanValue(true); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddTimestampValue(12345); err != nil {
		t.Fatal(err)
	}
	outApprove, err := ab.AddSymbolValue([]byte("Approve"))
	if err != nil {
		t.Fatal(err)
	}
	outReject, err := ab.AddSymbolValue([]byte("Reject"))
	if err != nil {
		t.Fatal(err)
	}
	outRevise, err := ab.AddSymbolValue([]byte("Revise"))
	if err != nil {
		t.Fatal(err)
	}
	outEscalate, err := ab.AddSymbolValue([]byte("Escalate"))
	if err != nil {
		t.Fatal(err)
	}

	kind, err := ab.AddEvidenceKind(ekName, span)
	if err != nil {
		t.Fatal(err)
	}
	state, err := ab.AddEvidenceState(esName, span)
	if err != nil {
		t.Fatal(err)
	}
	approve, err := ab.AddOutcome(outApprove, 1, true, span)
	if err != nil {
		t.Fatal(err)
	}
	reject, err := ab.AddOutcome(outReject, 4, true, span)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddOutcome(outRevise, 2, false, span); err != nil {
		t.Fatal(err)
	}
	escalate, err := ab.AddOutcome(outEscalate, 3, true, span)
	if err != nil {
		t.Fatal(err)
	}

	e1, err := ab.AddExists(schema.FieldID(1), span)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := ab.AddCompare(schema.FieldID(1), ast.CompareOpEqual, sym, span)
	if err != nil {
		t.Fatal(err)
	}
	n1, err := ab.AddNot(e1, span)
	if err != nil {
		t.Fatal(err)
	}
	a1, err := ab.AddGroup(ast.NodeKindAll, []schema.NodeID{e1, c1}, span)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := ab.AddGroup(ast.NodeKindAny, []schema.NodeID{n1}, span)
	if err != nil {
		t.Fatal(err)
	}
	ev1, err := ab.AddEvidence(kind, state, span)
	if err != nil {
		t.Fatal(err)
	}
	clause, err := ab.AddClause(a1, []schema.NodeID{ev1}, ast.Resolution{
		OnSatisfied: approve, OnFalse: reject,
		OnMissing: escalate, OnStale: escalate, OnUnclear: escalate,
		OnUnverifiable: escalate, OnConflict: escalate,
	}, nil, span)
	if err != nil {
		t.Fatal(err)
	}
	if err := ab.AddRequirement(schema.RequirementID(1), a2, []schema.ClauseID{clause}, span); err != nil {
		t.Fatal(err)
	}
	return ab.Document(), fields
}

// buildDoc returns a document with n exists nodes and c clauses, all sharing
// one valid span. Used for validator state-resizing tests.
func buildDoc(t *testing.T, nodes, clauses int) (*ast.Document, *schema.Schema) {
	t.Helper()
	syms := schema.NewSymbolInterner(8)
	fieldSym, err := syms.Intern([]byte("subject.trust"))
	if err != nil {
		t.Fatal(err)
	}
	fb := schema.NewBuilder()
	if _, err := fb.AddField(fieldSym, schema.ValueKindSymbol, schema.FieldGroupSubject); err != nil {
		t.Fatal(err)
	}
	fields := fb.Finish()

	source := []byte("src")
	ab := ast.NewBuilder(ast.Hints{Nodes: nodes, CompareNodes: nodes, Clauses: clauses, SourceBytes: len(source)})
	if err := ab.SetSource(source); err != nil {
		t.Fatal(err)
	}
	span := ast.SourceSpan{Start: 0, End: 1}
	var first schema.NodeID
	for i := 0; i < nodes; i++ {
		id, err := ab.AddExists(schema.FieldID(1), span)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = id
		}
	}
	for i := 0; i < clauses; i++ {
		if _, err := ab.AddClause(first, nil, ast.Resolution{}, nil, span); err != nil {
			t.Fatal(err)
		}
	}
	return ab.Document(), fields
}

// want checks got equals want exactly (codes, tables, rows, IDs, spans).
func want(t *testing.T, got, want []Diagnostic) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostics mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestValidateStructuralCanonicalMinimal(t *testing.T) {
	doc, fields := buildMinimal(t)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), nil)
}

func TestValidateStructuralNodeColumnLengths(t *testing.T) {
	trunc := func(t *testing.T, mutate func(*ast.Document)) {
		t.Helper()
		doc, fields := fixture(t)
		mutate(doc)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableNode},
		})
	}
	t.Run("NodeRefs", func(t *testing.T) {
		trunc(t, func(d *ast.Document) { d.NodeRefs = d.NodeRefs[:len(d.NodeRefs)-1] })
	})
	t.Run("SourceStarts", func(t *testing.T) {
		trunc(t, func(d *ast.Document) { d.SourceStarts = d.SourceStarts[:len(d.SourceStarts)-1] })
	})
	t.Run("SourceEnds", func(t *testing.T) {
		trunc(t, func(d *ast.Document) { d.SourceEnds = d.SourceEnds[:len(d.SourceEnds)-1] })
	})
	t.Run("all three peers", func(t *testing.T) {
		trunc(t, func(d *ast.Document) {
			d.NodeRefs = d.NodeRefs[:len(d.NodeRefs)-1]
			d.SourceStarts = d.SourceStarts[:len(d.SourceStarts)-1]
			d.SourceEnds = d.SourceEnds[:len(d.SourceEnds)-1]
		})
	})
}

func TestValidateStructuralValueColumnLengths(t *testing.T) {
	doc, fields := fixture(t)
	doc.ValueRefs = append(doc.ValueRefs, 0)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableValue},
	})

	doc, fields = fixture(t)
	doc.SymbolLengths = append(doc.SymbolLengths, 0)
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableValue},
	})

	doc, fields = fixture(t)
	doc.ValueRefs = append(doc.ValueRefs, 0)
	doc.SymbolStarts = append(doc.SymbolStarts, 0)
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableValue},
	})
}

func TestValidateStructuralValueKinds(t *testing.T) {
	tests := []struct {
		name  string
		index int
		kind  schema.ValueKind
	}{
		{"presence", 0, schema.ValueKindPresence},
		{"invalid zero", 0, schema.ValueKindInvalid},
		{"out of range", 3, schema.ValueKind(255)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ValueKinds[tc.index] = tc.kind
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidValue, Table: TableValue, Row: uint32(tc.index + 1), Value: schema.ValueID(tc.index + 1)},
			})
		})
	}
}

func TestValidateStructuralValueRefs(t *testing.T) {
	tests := []struct {
		name     string
		index    int
		ref      uint32
		corrupt  func(*ast.Document)
		row, val uint32
	}{
		{"symbol ref out of range", 0, 0, func(d *ast.Document) {
			d.ValueRefs[0] = uint32(len(d.SymbolStarts))
		}, 1, 1},
		{"integer ref out of range", 3, 1, nil, 4, 4},
		{"boolean ref out of range", 4, 1, nil, 5, 5},
		{"timestamp ref out of range", 5, 1, nil, 6, 6},
		{"symbol range overflow", 0, 0, func(d *ast.Document) {
			d.SymbolStarts[0] = 0
			d.SymbolLengths[0] = uint32(len(d.SymbolBytes)) + 1
		}, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			if tc.corrupt != nil {
				tc.corrupt(doc)
			} else {
				doc.ValueRefs[tc.index] = tc.ref
			}
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidPayloadRef, Table: TableValue, Row: tc.row, Value: schema.ValueID(tc.val)},
			})
		})
	}
}

func TestValidateStructuralNodeKinds(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NodeKinds[0] = ast.NodeKindInvalid
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeKind, Table: TableNode, Row: 1, Node: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
	})

	doc, fields = buildMinimal(t)
	doc.NodeKinds[1] = ast.NodeKind(255)
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeKind, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateStructuralNodeRefs(t *testing.T) {
	tests := []struct {
		name  string
		index int
		ref   uint32
		row   uint32
		node  schema.NodeID
	}{
		{"compare", 0, 2, 1, 1},
		{"not", 2, 1, 3, 3},
		{"group", 3, 2, 4, 4},
		{"evidence", 5, 1, 6, 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.NodeRefs[tc.index] = tc.ref
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: tc.row, Node: tc.node, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralSourceSpans(t *testing.T) {
	t.Run("reversed", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.SourceStarts[0] = 5
		doc.SourceEnds[0] = 2
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableNode, Row: 1, Node: 1},
		})
	})
	t.Run("end beyond input", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.SourceEnds[0] = uint32(len(doc.InputBytes)) + 10
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableNode, Row: 1, Node: 1},
		})
	})
}

func TestValidateStructuralNodeRowOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.SourceStarts[0] = 5
	doc.SourceEnds[0] = 2
	doc.NodeRefs[0] = 2
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableNode, Row: 1, Node: 1},
		{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 1, Node: 1},
	})
}

func TestValidateStructuralMultiCorruptionOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NodeRefs = doc.NodeRefs[:len(doc.NodeRefs)-1]
	doc.SymbolLengths = append(doc.SymbolLengths, 0)
	doc.ValueKinds[0] = schema.ValueKindPresence
	doc.NodeKinds[1] = ast.NodeKindInvalid
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableNode},
		{Code: CodeColumnLength, Table: TableValue},
		{Code: CodeInvalidValue, Table: TableValue, Row: 1, Value: 1},
		{Code: CodeInvalidNodeKind, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateStructuralTruncationNoPanic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document)
		want   []Diagnostic
	}{
		{"NodeRefs empty", func(d *ast.Document) { d.NodeRefs = d.NodeRefs[:0] },
			[]Diagnostic{{Code: CodeColumnLength, Table: TableNode}}},
		{"NodeRefs partial", func(d *ast.Document) { d.NodeRefs = d.NodeRefs[:2] },
			[]Diagnostic{{Code: CodeColumnLength, Table: TableNode}}},
		{"SourceStarts empty", func(d *ast.Document) { d.SourceStarts = d.SourceStarts[:0] },
			[]Diagnostic{{Code: CodeColumnLength, Table: TableNode}}},
		{"SourceEnds empty", func(d *ast.Document) { d.SourceEnds = d.SourceEnds[:0] },
			[]Diagnostic{{Code: CodeColumnLength, Table: TableNode}}},
		{"ValueRefs empty", func(d *ast.Document) { d.ValueRefs = d.ValueRefs[:0] },
			[]Diagnostic{{Code: CodeColumnLength, Table: TableValue}}},
		{"all top columns", func(d *ast.Document) {
			d.NodeRefs = d.NodeRefs[:0]
			d.SourceStarts = d.SourceStarts[:0]
			d.SourceEnds = d.SourceEnds[:0]
			d.ValueRefs = d.ValueRefs[:0]
		}, []Diagnostic{
			{Code: CodeColumnLength, Table: TableNode},
			{Code: CodeColumnLength, Table: TableValue},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := fixture(t)
			tc.mutate(doc)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), tc.want)
		})
	}

	var v Validator
	want(t, v.validateStructure(nil, &ast.Document{}, schema.NewBuilder().Finish()), nil)
}

func TestValidatorStateResize(t *testing.T) {
	var v Validator

	big1, fields := buildDoc(t, 6, 2)
	big1.NodeKinds[5] = ast.NodeKindInvalid
	v.validateStructure(nil, big1, fields)
	if len(v.nodeState) != 6 {
		t.Fatalf("nodeState len = %d, want 6", len(v.nodeState))
	}
	if len(v.clauseState) != 2 {
		t.Fatalf("clauseState len = %d, want 2", len(v.clauseState))
	}
	if v.nodeState[5]&nodeStateUnsafe == 0 {
		t.Fatal("node 6 not marked unsafe after kind corruption")
	}
	for i := 0; i < 6; i++ {
		if i != 5 && v.nodeState[i]&nodeStateUnsafe != 0 {
			t.Fatalf("nodeState[%d] unsafe set on a valid node", i)
		}
	}
	if len(v.stack) != 0 {
		t.Fatalf("stack len = %d, want 0", len(v.stack))
	}

	// Seed stale clause bytes and a stack frame, then validate a smaller doc
	// to prove the next call clears active state and empties the stack while
	// retaining capacity.
	v.clauseState[0] = 0xAA
	v.clauseState[1] = 0x55
	v.stack = append(v.stack, visitFrame{node: 1, next: 0})

	small, _ := buildDoc(t, 1, 0)
	v.validateStructure(nil, small, fields)
	if len(v.nodeState) != 1 {
		t.Fatalf("nodeState len = %d, want 1 after small doc", len(v.nodeState))
	}
	if cap(v.nodeState) < 6 {
		t.Fatalf("nodeState cap = %d, want >= 6 (capacity retained)", cap(v.nodeState))
	}
	if len(v.clauseState) != 0 {
		t.Fatalf("clauseState len = %d, want 0 after small doc", len(v.clauseState))
	}
	if cap(v.clauseState) < 2 {
		t.Fatalf("clauseState cap = %d, want >= 2 (capacity retained)", cap(v.clauseState))
	}
	if v.nodeState[0]&nodeStateUnsafe != 0 {
		t.Fatal("nodeState[0] unsafe not cleared after valid small doc")
	}
	if len(v.stack) != 0 {
		t.Fatalf("stack len = %d, want 0 after small doc", len(v.stack))
	}

	big2, _ := buildDoc(t, 6, 1)
	v.validateStructure(nil, big2, fields)
	if len(v.nodeState) != 6 {
		t.Fatalf("nodeState len = %d, want 6 after big2", len(v.nodeState))
	}
	if len(v.clauseState) != 1 {
		t.Fatalf("clauseState len = %d, want 1 after big2", len(v.clauseState))
	}
	if cap(v.clauseState) < 2 {
		t.Fatalf("clauseState cap = %d, want >= 2 (capacity retained)", cap(v.clauseState))
	}
	if v.clauseState[0] != 0 {
		t.Fatalf("clauseState[0] = %#x, want seeded byte cleared", v.clauseState[0])
	}
	for i := 0; i < 6; i++ {
		if v.nodeState[i]&nodeStateUnsafe != 0 {
			t.Fatalf("nodeState[%d] unsafe set after valid big2, want old byte cleared", i)
		}
	}
	if len(v.stack) != 0 {
		t.Fatalf("stack len = %d, want 0 after big2", len(v.stack))
	}

	bigger, _ := buildDoc(t, 8, 0)
	v.validateStructure(nil, bigger, fields)
	if len(v.nodeState) != 8 {
		t.Fatalf("nodeState len = %d, want 8 after larger doc", len(v.nodeState))
	}
	if cap(v.nodeState) < 8 {
		t.Fatalf("nodeState cap = %d, want >= 8 after growth", cap(v.nodeState))
	}
}

// buildInDoc returns a fully connected, semantically valid policy whose sole
// node is an In comparison over the first two symbol values (ValueIDs 1 and 2,
// kept as compare-row and list-layout references). It also carries a
// four-outcome catalog, one clause whose assertion is the In node with a
// complete seven-way Resolution and no evidence or remediation, and one
// requirement whose applicability root is the same In node and whose clause
// range contains that clause, so the sole node is reachable from the
// requirement.
func buildInDoc(t *testing.T) (*ast.Document, *schema.Schema) {
	t.Helper()
	syms := schema.NewSymbolInterner(8)
	fieldSym, err := syms.Intern([]byte("context.usage"))
	if err != nil {
		t.Fatal(err)
	}
	fb := schema.NewBuilder()
	if _, err := fb.AddField(fieldSym, schema.ValueKindSymbol, schema.FieldGroupContext); err != nil {
		t.Fatal(err)
	}
	fields := fb.Finish()

	source := []byte("src!")
	ab := ast.NewBuilder(ast.Hints{
		Nodes: 1, CompareNodes: 1, CompareListValues: 2,
		Values: 6, SymbolValues: 6, SymbolBytes: 64,
		Outcomes: 4, Clauses: 1, Requirements: 1, RequirementClauseEdges: 1,
		SourceBytes: len(source),
	})
	if err := ab.SetSource(source); err != nil {
		t.Fatal(err)
	}
	standard, err := ab.AddSymbolValue([]byte("standard"))
	if err != nil {
		t.Fatal(err)
	}
	limited, err := ab.AddSymbolValue([]byte("limited"))
	if err != nil {
		t.Fatal(err)
	}
	in, err := ab.AddIn(schema.FieldID(1), []schema.ValueID{standard, limited}, ast.SourceSpan{Start: 0, End: 1})
	if err != nil {
		t.Fatal(err)
	}
	span := ast.SourceSpan{Start: 0, End: 1}
	outApprove, err := ab.AddSymbolValue([]byte("Approve"))
	if err != nil {
		t.Fatal(err)
	}
	outReject, err := ab.AddSymbolValue([]byte("Reject"))
	if err != nil {
		t.Fatal(err)
	}
	outRevise, err := ab.AddSymbolValue([]byte("Revise"))
	if err != nil {
		t.Fatal(err)
	}
	outEscalate, err := ab.AddSymbolValue([]byte("Escalate"))
	if err != nil {
		t.Fatal(err)
	}
	approve, err := ab.AddOutcome(outApprove, 1, true, span)
	if err != nil {
		t.Fatal(err)
	}
	reject, err := ab.AddOutcome(outReject, 4, true, span)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddOutcome(outRevise, 2, false, span); err != nil {
		t.Fatal(err)
	}
	escalate, err := ab.AddOutcome(outEscalate, 3, true, span)
	if err != nil {
		t.Fatal(err)
	}
	clause, err := ab.AddClause(in, nil, ast.Resolution{
		OnSatisfied: approve, OnFalse: reject,
		OnMissing: escalate, OnStale: escalate, OnUnclear: escalate,
		OnUnverifiable: escalate, OnConflict: escalate,
	}, nil, span)
	if err != nil {
		t.Fatal(err)
	}
	if err := ab.AddRequirement(schema.RequirementID(1), in, []schema.ClauseID{clause}, span); err != nil {
		t.Fatal(err)
	}
	return ab.Document(), fields
}

func TestValidateStructuralCompareColumnLengths(t *testing.T) {
	col := func(t *testing.T, mutate func(*ast.Document)) {
		t.Helper()
		doc, fields := buildMinimal(t)
		mutate(doc)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableCompare},
		})
	}
	t.Run("CompareFields", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.CompareFields = append(d.CompareFields, 0) })
	})
	t.Run("CompareOps", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.CompareOps = append(d.CompareOps, 0) })
	})
	t.Run("CompareValues", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.CompareValues = append(d.CompareValues, 0) })
	})
	t.Run("CompareListStarts", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.CompareListStarts = append(d.CompareListStarts, 0) })
	})
	t.Run("CompareListCounts", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.CompareListCounts = append(d.CompareListCounts, 0) })
	})
	t.Run("all four peers", func(t *testing.T) {
		col(t, func(d *ast.Document) {
			d.CompareOps = append(d.CompareOps, 0)
			d.CompareValues = append(d.CompareValues, 0)
			d.CompareListStarts = append(d.CompareListStarts, 0)
			d.CompareListCounts = append(d.CompareListCounts, 0)
		})
	})
}

func TestValidateStructuralCompareFields(t *testing.T) {
	tests := []struct {
		name  string
		field schema.FieldID
	}{
		{"zero", 0},
		{"high", schema.FieldID(4)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			if tc.field > 0 && uint64(tc.field) <= uint64(fields.Len()) {
				t.Fatalf("test field %d is valid, want an invalid bound", tc.field)
			}
			doc.CompareFields[0] = tc.field
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidField, Table: TableCompare, Row: 1, Field: tc.field},
			})
		})
	}
}

func TestValidateStructuralCompareScalarValue(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.CompareValues[1] = schema.ValueID(len(doc.ValueKinds) + 1)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidValue, Table: TableCompare, Row: 2, Value: schema.ValueID(len(doc.ValueKinds) + 1)},
	})
}

func TestValidateStructuralCompareCSRRange(t *testing.T) {
	tests := []struct {
		name  string
		start uint32
		count uint16
	}{
		{"start beyond total", 1, 0},
		{"count beyond total", 0, math.MaxUint16},
		{"start near MaxUint32", math.MaxUint32, 0},
		{"start near MaxUint32 with count", math.MaxUint32, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.CompareListStarts[0] = tc.start
			doc.CompareListCounts[0] = tc.count
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableCompare, Row: 1},
			})
		})
	}
}

func TestValidateStructuralCompareListValues(t *testing.T) {
	doc, fields := buildInDoc(t)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), nil)

	tests := []struct {
		name  string
		index int
		value schema.ValueID
	}{
		{"zero", 0, 0},
		{"high", 0, schema.ValueID(len(doc.ValueKinds) + 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildInDoc(t)
			doc.ListValueIDs[tc.index] = tc.value
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidValue, Table: TableCompare, Row: 1, Value: tc.value},
			})
		})
	}
}

func TestValidateStructuralCompareTruncatedNoPanic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document)
		want   []Diagnostic
	}{
		{"CompareOps partial", func(d *ast.Document) { d.CompareOps = d.CompareOps[:1] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableCompare},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"CompareValues partial", func(d *ast.Document) { d.CompareValues = d.CompareValues[:1] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableCompare},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"CompareListStarts partial", func(d *ast.Document) { d.CompareListStarts = d.CompareListStarts[:1] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableCompare},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"CompareListCounts partial", func(d *ast.Document) { d.CompareListCounts = d.CompareListCounts[:1] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableCompare},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"all five empty", func(d *ast.Document) {
			d.CompareFields = d.CompareFields[:0]
			d.CompareOps = d.CompareOps[:0]
			d.CompareValues = d.CompareValues[:0]
			d.CompareListStarts = d.CompareListStarts[:0]
			d.CompareListCounts = d.CompareListCounts[:0]
		}, []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 1, Node: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			tc.mutate(doc)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), tc.want)
		})
	}
}

func TestValidateStructuralCompareMixedOrdering(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NodeRefs = doc.NodeRefs[:len(doc.NodeRefs)-1]
	doc.CompareOps = doc.CompareOps[:1]
	doc.SymbolLengths = append(doc.SymbolLengths, 0)
	doc.ValueKinds[0] = schema.ValueKindPresence
	doc.CompareFields[0] = 0
	doc.NodeKinds[1] = ast.NodeKindInvalid
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableNode},
		{Code: CodeColumnLength, Table: TableCompare},
		{Code: CodeColumnLength, Table: TableValue},
		{Code: CodeInvalidValue, Table: TableValue, Row: 1, Value: 1},
		{Code: CodeInvalidField, Table: TableCompare, Row: 1, Field: 0},
		{Code: CodeInvalidNodeKind, Table: TableNode, Row: 2, Node: 2, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}
