package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

func makeSymbolValueEmpty(t *testing.T, doc *ast.Document, id schema.ValueID) {
	t.Helper()
	if id == 0 || uint64(id) > uint64(len(doc.ValueRefs)) || doc.ValueKinds[id-1] != schema.ValueKindSymbol {
		t.Fatalf("value %d is not a symbol", id)
	}
	ref := doc.ValueRefs[id-1]
	if uint64(ref) >= uint64(len(doc.SymbolLengths)) {
		t.Fatalf("symbol ref %d is out of range", ref)
	}
	doc.SymbolLengths[ref] = 0
}

func TestValidateSemanticNamesRejectEmptySymbols(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document) Diagnostic
	}{
		{"policy name", func(doc *ast.Document) Diagnostic {
			id := doc.Metadata.Name
			makeSymbolValueEmpty(t, doc, id)
			return Diagnostic{Code: CodeInvalidValue, Table: TableDocument, Member: MemberMetadataName, Value: id}
		}},
		{"policy version", func(doc *ast.Document) Diagnostic {
			id := doc.Metadata.Version
			makeSymbolValueEmpty(t, doc, id)
			return Diagnostic{Code: CodeInvalidValue, Table: TableDocument, Member: MemberMetadataVersion, Value: id}
		}},
		{"evidence kind", func(doc *ast.Document) Diagnostic {
			id := doc.EvidenceKindNames[0]
			makeSymbolValueEmpty(t, doc, id)
			return Diagnostic{Code: CodeInvalidValue, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: ast.SourceSpan{Start: 0, End: 2}, Value: id, EvidenceKind: 1}
		}},
		{"evidence state", func(doc *ast.Document) Diagnostic {
			id := doc.EvidenceStateNames[0]
			makeSymbolValueEmpty(t, doc, id)
			return Diagnostic{Code: CodeInvalidValue, Table: TableEvidenceState, Member: MemberName, Row: 1, Span: ast.SourceSpan{Start: 0, End: 2}, Value: id, EvidenceState: 1}
		}},
		{"outcome", func(doc *ast.Document) Diagnostic {
			id := doc.OutcomeNames[0]
			makeSymbolValueEmpty(t, doc, id)
			return Diagnostic{Code: CodeInvalidValue, Table: TableOutcome, Member: MemberName, Row: 1, Span: ast.SourceSpan{Start: 0, End: 2}, Value: id, Outcome: 1}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := buildExplanationCSEFixture(t)
			wantDiagnostic := test.mutate(fixture.doc)
			want(t, Validate(nil, fixture.doc, fixture.fields), []Diagnostic{wantDiagnostic})
		})
	}
}

// appendLiteral appends one structurally valid literal value row of kind to doc
// and returns its ValueID.
func appendLiteral(t *testing.T, doc *ast.Document, kind schema.ValueKind) schema.ValueID {
	t.Helper()
	id := schema.ValueID(len(doc.ValueKinds) + 1)
	doc.ValueKinds = append(doc.ValueKinds, kind)
	switch kind {
	case schema.ValueKindInteger:
		doc.ValueRefs = append(doc.ValueRefs, uint32(len(doc.IntegerValues)))
		doc.IntegerValues = append(doc.IntegerValues, 7)
	case schema.ValueKindBoolean:
		doc.ValueRefs = append(doc.ValueRefs, uint32(len(doc.BooleanValues)))
		doc.BooleanValues = append(doc.BooleanValues, 1)
	case schema.ValueKindTimestamp:
		doc.ValueRefs = append(doc.ValueRefs, uint32(len(doc.TimestampValues)))
		doc.TimestampValues = append(doc.TimestampValues, 12345)
	default:
		t.Fatalf("unsupported literal kind %v", kind)
	}
	return id
}

// TestValidateSemanticEvidenceKindNameType covers the Task 7.3 evidence-kind
// name typing rule: a structurally valid non-symbol literal name emits exactly
// one CodeTypeMismatch on MemberName with the owning table, row, valid owner
// span, the offending Value, and the EvidenceKind strong ID.
func TestValidateSemanticEvidenceKindNameType(t *testing.T) {
	kinds := []struct {
		name string
		kind schema.ValueKind
	}{
		{"integer", schema.ValueKindInteger},
		{"boolean", schema.ValueKindBoolean},
		{"timestamp", schema.ValueKindTimestamp},
	}
	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildCatalogDoc(t)
			id := appendLiteral(t, doc, tc.kind)
			doc.EvidenceKindNames[0] = id
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeTypeMismatch, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: ast.SourceSpan{Start: 0, End: 4}, EvidenceKind: 1, Value: id},
			})
		})
	}
}

// TestValidateSemanticEvidenceKindNameSymbolClean proves a structurally valid
// symbol name is accepted with no diagnostics.
func TestValidateSemanticEvidenceKindNameSymbolClean(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateSemanticEvidenceKindNameInvalidSpanZeroAttachment proves an
// invalid evidence-kind owner span does not suppress an independent type
// mismatch: the structural CodeInvalidSourceSpan is emitted, followed by the
// type mismatch with a zero (unattached) span.
func TestValidateSemanticEvidenceKindNameInvalidSpanZeroAttachment(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	id := appendLiteral(t, doc, schema.ValueKindInteger)
	doc.EvidenceKindNames[0] = id
	doc.EvidenceKindSourceStarts[0] = 5
	doc.EvidenceKindSourceEnds[0] = 2
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableEvidenceKind, Member: MemberSpan, Row: 1, EvidenceKind: 1},
		{Code: CodeTypeMismatch, Table: TableEvidenceKind, Member: MemberName, Row: 1, EvidenceKind: 1, Value: id},
	})
}

// TestValidateSemanticEvidenceKindNameSuppression proves every structural
// defect stays structural-only: the public Validate result equals the exact
// structural diagnostics and never adds a semantic type mismatch.
func TestValidateSemanticEvidenceKindNameSuppression(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("zero id", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.EvidenceKindNames[0] = 0
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: span, EvidenceKind: 1, Value: 0},
		})
	})
	t.Run("high id", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.ValueID(len(doc.ValueKinds) + 1)
		doc.EvidenceKindNames[0] = high
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: span, EvidenceKind: 1, Value: high},
		})
	})
	t.Run("shortened value kinds", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		id := appendLiteral(t, doc, schema.ValueKindInteger)
		doc.EvidenceKindNames[0] = id
		doc.ValueKinds = doc.ValueKinds[:len(doc.ValueKinds)-1]
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableValue},
			{Code: CodeInvalidValue, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: span, EvidenceKind: 1, Value: id},
		})
	})
	t.Run("shortened value refs", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		id := appendLiteral(t, doc, schema.ValueKindInteger)
		doc.EvidenceKindNames[0] = id
		doc.ValueRefs = doc.ValueRefs[:0]
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableValue},
		})
	})
	t.Run("invalid stored kind", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		id := appendLiteral(t, doc, schema.ValueKindInteger)
		doc.EvidenceKindNames[0] = id
		doc.ValueKinds[id-1] = schema.ValueKindPresence
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableValue, Row: uint32(id), Value: id},
		})
	})
	t.Run("invalid payload ref", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		id := appendLiteral(t, doc, schema.ValueKindInteger)
		doc.EvidenceKindNames[0] = id
		doc.ValueRefs[id-1] = 99
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableValue, Row: uint32(id), Value: id},
		})
	})
	t.Run("invalid symbol payload range", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.SymbolLengths[0] = math.MaxUint32
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableValue, Row: 1, Value: 1},
		})
	})
}

// TestValidateSemanticEvidenceStateNameType covers the Task 7.3 evidence-state
// name typing rule with the same shape as evidence kinds: a structurally valid
// non-symbol literal name emits exactly one CodeTypeMismatch on MemberName with
// the owning table, row, valid owner span, the offending Value, and the
// EvidenceState strong ID.
func TestValidateSemanticEvidenceStateNameType(t *testing.T) {
	kinds := []struct {
		name string
		kind schema.ValueKind
	}{
		{"integer", schema.ValueKindInteger},
		{"boolean", schema.ValueKindBoolean},
		{"timestamp", schema.ValueKindTimestamp},
	}
	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildCatalogDoc(t)
			id := appendLiteral(t, doc, tc.kind)
			doc.EvidenceStateNames[0] = id
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeTypeMismatch, Table: TableEvidenceState, Member: MemberName, Row: 1, Span: ast.SourceSpan{Start: 0, End: 4}, EvidenceState: 1, Value: id},
			})
		})
	}
}

// TestValidateSemanticOutcomeNameType covers the Task 7.3 outcome name typing
// rule with the same shape as evidence kinds: a structurally valid non-symbol
// literal name emits exactly one CodeTypeMismatch on MemberName with the
// owning table, row, valid owner span, the offending Value, and the Outcome
// strong ID.
func TestValidateSemanticOutcomeNameType(t *testing.T) {
	kinds := []struct {
		name string
		kind schema.ValueKind
	}{
		{"integer", schema.ValueKindInteger},
		{"boolean", schema.ValueKindBoolean},
		{"timestamp", schema.ValueKindTimestamp},
	}
	for _, tc := range kinds {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildCatalogDoc(t)
			id := appendLiteral(t, doc, tc.kind)
			doc.OutcomeNames[0] = id
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{
				{Code: CodeTypeMismatch, Table: TableOutcome, Member: MemberName, Row: 1, Span: ast.SourceSpan{Start: 0, End: 4}, Outcome: 1, Value: id},
			})
		})
	}
}

// TestValidateSemanticNameTablesOrdering locks the semantic table order after
// expression diagnostics: EvidenceKind rows, then EvidenceState rows, then
// Outcome rows, each ascending, preserving the seeded prefix.
func TestValidateSemanticNameTablesOrdering(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	id := appendLiteral(t, doc, schema.ValueKindInteger)
	doc.EvidenceKindNames[0] = id
	doc.EvidenceStateNames[0] = id
	doc.OutcomeNames[0] = id
	seed := Diagnostic{Code: CodeCycle}
	span := ast.SourceSpan{Start: 0, End: 4}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeTypeMismatch, Table: TableEvidenceKind, Member: MemberName, Row: 1, Span: span, EvidenceKind: 1, Value: id},
		{Code: CodeTypeMismatch, Table: TableEvidenceState, Member: MemberName, Row: 1, Span: span, EvidenceState: 1, Value: id},
		{Code: CodeTypeMismatch, Table: TableOutcome, Member: MemberName, Row: 1, Span: span, Outcome: 1, Value: id},
	})
}

// appendSymbolRef appends a symbol value row that references the given symbol
// payload ref, returning its ValueID. ref 0 aliases the bytes of ValueID 1, so
// the appended ID is distinct yet byte-equal to it; an out-of-range ref makes
// the row structurally invalid.
func appendSymbolRef(t *testing.T, doc *ast.Document, ref uint32) schema.ValueID {
	t.Helper()
	id := schema.ValueID(len(doc.ValueKinds) + 1)
	doc.ValueKinds = append(doc.ValueKinds, schema.ValueKindSymbol)
	doc.ValueRefs = append(doc.ValueRefs, ref)
	return id
}

// catalogTableOps drives the duplicate-name tests for one symbol-named catalog
// table: name and span setters, a row appender (used for the triple case), and
// the owner-ID stamper. The owner strong ID of catalog row r is always r.
type catalogTableOps struct {
	table     TableKind
	setName   func(*ast.Document, int, schema.ValueID)
	setSpan   func(*ast.Document, int, uint32, uint32)
	appendRow func(*ast.Document)
	owner     func(Diagnostic) Diagnostic
}

var nameTableOps = []struct {
	name string
	ops  catalogTableOps
}{
	{"evidence kind", catalogTableOps{
		table:   TableEvidenceKind,
		setName: func(d *ast.Document, i int, v schema.ValueID) { d.EvidenceKindNames[i] = v },
		setSpan: func(d *ast.Document, i int, s, e uint32) {
			d.EvidenceKindSourceStarts[i] = s
			d.EvidenceKindSourceEnds[i] = e
		},
		appendRow: func(d *ast.Document) {
			d.EvidenceKindNames = append(d.EvidenceKindNames, 1)
			d.EvidenceKindSourceStarts = append(d.EvidenceKindSourceStarts, 0)
			d.EvidenceKindSourceEnds = append(d.EvidenceKindSourceEnds, 4)
		},
		owner: func(d Diagnostic) Diagnostic { d.EvidenceKind = schema.EvidenceKindID(d.Row); return d },
	}},
	{"evidence state", catalogTableOps{
		table:   TableEvidenceState,
		setName: func(d *ast.Document, i int, v schema.ValueID) { d.EvidenceStateNames[i] = v },
		setSpan: func(d *ast.Document, i int, s, e uint32) {
			d.EvidenceStateSourceStarts[i] = s
			d.EvidenceStateSourceEnds[i] = e
		},
		appendRow: func(d *ast.Document) {
			d.EvidenceStateNames = append(d.EvidenceStateNames, 1)
			d.EvidenceStateSourceStarts = append(d.EvidenceStateSourceStarts, 0)
			d.EvidenceStateSourceEnds = append(d.EvidenceStateSourceEnds, 4)
		},
		owner: func(d Diagnostic) Diagnostic { d.EvidenceState = schema.EvidenceStateID(d.Row); return d },
	}},
	{"outcome", catalogTableOps{
		table:   TableOutcome,
		setName: func(d *ast.Document, i int, v schema.ValueID) { d.OutcomeNames[i] = v },
		setSpan: func(d *ast.Document, i int, s, e uint32) {
			d.OutcomeSourceStarts[i] = s
			d.OutcomeSourceEnds[i] = e
		},
		appendRow: func(d *ast.Document) {
			d.OutcomeNames = append(d.OutcomeNames, 1)
			d.OutcomePrecedence = append(d.OutcomePrecedence, 1)
			d.OutcomeTerminal = append(d.OutcomeTerminal, false)
			d.OutcomeSourceStarts = append(d.OutcomeSourceStarts, 0)
			d.OutcomeSourceEnds = append(d.OutcomeSourceEnds, 4)
		},
		owner: func(d Diagnostic) Diagnostic { d.Outcome = schema.OutcomeID(d.Row); return d },
	}},
}

// runCatalogDuplicateTests covers the duplicate-name rule for one table: names
// are unique by exact symbol bytes within the table, the first row is never
// diagnosed, and structurally invalid current or predecessor rows do not
// participate.
func runCatalogDuplicateTests(t *testing.T, ops catalogTableOps) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("same value id", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		ops.setName(doc, 0, 1)
		ops.setName(doc, 1, 1)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			ops.owner(Diagnostic{Code: CodeDuplicateName, Table: ops.table, Member: MemberName, Row: 2, Span: span, Value: 1}),
		})
	})
	t.Run("distinct value ids equal bytes", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		id := appendSymbolRef(t, doc, 0)
		ops.setName(doc, 0, 1)
		ops.setName(doc, 1, id)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			ops.owner(Diagnostic{Code: CodeDuplicateName, Table: ops.table, Member: MemberName, Row: 2, Span: span, Value: id}),
		})
	})
	t.Run("triple duplicate later rows", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		ops.setName(doc, 0, 1)
		ops.setName(doc, 1, 1)
		ops.appendRow(doc)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			ops.owner(Diagnostic{Code: CodeDuplicateName, Table: ops.table, Member: MemberName, Row: 2, Span: span, Value: 1}),
			ops.owner(Diagnostic{Code: CodeDuplicateName, Table: ops.table, Member: MemberName, Row: 3, Span: span, Value: 1}),
		})
	})
	t.Run("invalid current suppressed", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		bad := appendSymbolRef(t, doc, 99)
		ops.setName(doc, 0, 1)
		ops.setName(doc, 1, bad)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidPayloadRef, Table: TableValue, Row: uint32(bad), Value: bad},
		})
	})
	t.Run("invalid predecessor type suppressed", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		intID := appendLiteral(t, doc, schema.ValueKindInteger)
		ops.setName(doc, 0, intID)
		ops.setName(doc, 1, 1)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			ops.owner(Diagnostic{Code: CodeTypeMismatch, Table: ops.table, Member: MemberName, Row: 1, Span: span, Value: intID}),
		})
	})
	t.Run("invalid predecessor structural suppressed", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		ops.setName(doc, 0, 0)
		ops.setName(doc, 1, 1)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			ops.owner(Diagnostic{Code: CodeInvalidValue, Table: ops.table, Member: MemberName, Row: 1, Span: span, Value: 0}),
		})
	})
	t.Run("invalid current span zero attachment", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		ops.setName(doc, 0, 1)
		ops.setName(doc, 1, 1)
		ops.setSpan(doc, 1, 5, 2)
		var v Validator
		want(t, v.Validate(nil, doc, fields), []Diagnostic{
			ops.owner(Diagnostic{Code: CodeInvalidSourceSpan, Table: ops.table, Member: MemberSpan, Row: 2}),
			ops.owner(Diagnostic{Code: CodeDuplicateName, Table: ops.table, Member: MemberName, Row: 2, Value: 1}),
		})
	})
}

func TestValidateSemanticDuplicateNames(t *testing.T) {
	for _, tc := range nameTableOps {
		t.Run(tc.name, func(t *testing.T) {
			runCatalogDuplicateTests(t, tc.ops)
		})
	}
}

// TestValidateSemanticNameCrossTableClean proves the three tables are
// independent namespaces: identical symbol bytes across different tables are
// accepted, so only within-table duplicates diagnose.
func TestValidateSemanticNameCrossTableClean(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	doc.EvidenceStateNames[0] = 1
	doc.OutcomeNames[0] = 1
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateSemanticDuplicateOrdering locks the full semantic order: the
// seeded prefix, then EvidenceKind rows ascending (a duplicate and a type
// mismatch each at its current row), then EvidenceState rows, then Outcome
// rows.
func TestValidateSemanticDuplicateOrdering(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	intID := appendLiteral(t, doc, schema.ValueKindInteger)
	doc.EvidenceKindNames[0] = 1
	doc.EvidenceKindNames[1] = 1
	doc.EvidenceKindNames = append(doc.EvidenceKindNames, intID)
	doc.EvidenceKindSourceStarts = append(doc.EvidenceKindSourceStarts, 0)
	doc.EvidenceKindSourceEnds = append(doc.EvidenceKindSourceEnds, 4)
	doc.EvidenceStateNames[0] = 1
	doc.EvidenceStateNames[1] = 1
	doc.OutcomeNames[0] = 1
	doc.OutcomeNames[1] = 1
	seed := Diagnostic{Code: CodeCycle}
	span := ast.SourceSpan{Start: 0, End: 4}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeDuplicateName, Table: TableEvidenceKind, Member: MemberName, Row: 2, Span: span, EvidenceKind: 2, Value: 1},
		{Code: CodeTypeMismatch, Table: TableEvidenceKind, Member: MemberName, Row: 3, Span: span, EvidenceKind: 3, Value: intID},
		{Code: CodeDuplicateName, Table: TableEvidenceState, Member: MemberName, Row: 2, Span: span, EvidenceState: 2, Value: 1},
		{Code: CodeDuplicateName, Table: TableOutcome, Member: MemberName, Row: 2, Span: span, Outcome: 2, Value: 1},
	})
}
