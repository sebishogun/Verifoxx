package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// buildCatalogDoc returns a document whose only records are structurally valid
// catalog rows: two evidence kinds, two evidence states, two outcomes, and two
// remediations (one set-field, one add-evidence), plus seven distinct symbol
// values. Every catalog name is unique so the Task 7.3 duplicate-name check
// stays silent. No nodes, clauses, or requirements are present, so only the
// catalog row checks can fire.
func buildCatalogDoc(t *testing.T) (*ast.Document, *schema.Schema) {
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

	source := []byte("policy source bytes for catalog spans")
	ab := ast.NewBuilder(ast.Hints{
		Values: 16, SymbolValues: 16, SymbolBytes: 192,
		EvidenceKinds: 2, EvidenceStates: 2, Outcomes: 2, Remediations: 2,
		SourceBytes: len(source),
	})
	if err := ab.SetSource(source); err != nil {
		t.Fatal(err)
	}
	span := ast.SourceSpan{Start: 0, End: 4}

	ekName, err := ab.AddSymbolValue([]byte("approval_record"))
	if err != nil {
		t.Fatal(err)
	}
	ekName2, err := ab.AddSymbolValue([]byte("disclosure_letter"))
	if err != nil {
		t.Fatal(err)
	}
	esName, err := ab.AddSymbolValue([]byte("current"))
	if err != nil {
		t.Fatal(err)
	}
	esName2, err := ab.AddSymbolValue([]byte("missing"))
	if err != nil {
		t.Fatal(err)
	}
	outName, err := ab.AddSymbolValue([]byte("Approve"))
	if err != nil {
		t.Fatal(err)
	}
	outName2, err := ab.AddSymbolValue([]byte("Revise"))
	if err != nil {
		t.Fatal(err)
	}
	valName, err := ab.AddSymbolValue([]byte("full"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := ab.AddEvidenceKind(ekName, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddEvidenceKind(ekName2, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddEvidenceState(esName, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddEvidenceState(esName2, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddOutcome(outName, 1, true, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddOutcome(outName2, 2, false, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddSetFieldRemediation(schema.FieldID(1), valName, span); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddEvidenceRemediation(schema.EvidenceKindID(1), span); err != nil {
		t.Fatal(err)
	}
	return ab.Document(), fields
}

func TestValidateCatalogCanonical(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), nil)
}

// TestValidateCatalogColumnLengths locks exactly one CodeColumnLength per
// table when any single peer column diverges, and when all peers diverge
// together.
func TestValidateCatalogColumnLengths(t *testing.T) {
	peerCol := func(t *testing.T, mutate func(*ast.Document), wantCol Diagnostic) {
		t.Helper()
		doc, fields := buildCatalogDoc(t)
		mutate(doc)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{wantCol})
	}
	t.Run("EvidenceKind", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableEvidenceKind}
		peerCol(t, func(d *ast.Document) { d.EvidenceKindNames = append(d.EvidenceKindNames, 1) }, col)
		peerCol(t, func(d *ast.Document) { d.EvidenceKindSourceStarts = append(d.EvidenceKindSourceStarts, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.EvidenceKindSourceEnds = append(d.EvidenceKindSourceEnds, 0) }, col)
		peerCol(t, func(d *ast.Document) {
			d.EvidenceKindSourceStarts = d.EvidenceKindSourceStarts[:0]
			d.EvidenceKindSourceEnds = d.EvidenceKindSourceEnds[:0]
		}, col)
	})
	t.Run("EvidenceState", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableEvidenceState}
		peerCol(t, func(d *ast.Document) { d.EvidenceStateNames = append(d.EvidenceStateNames, 1) }, col)
		peerCol(t, func(d *ast.Document) { d.EvidenceStateSourceStarts = append(d.EvidenceStateSourceStarts, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.EvidenceStateSourceEnds = append(d.EvidenceStateSourceEnds, 0) }, col)
		peerCol(t, func(d *ast.Document) {
			d.EvidenceStateSourceStarts = d.EvidenceStateSourceStarts[:0]
			d.EvidenceStateSourceEnds = d.EvidenceStateSourceEnds[:0]
		}, col)
	})
	t.Run("Outcome", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableOutcome}
		peerCol(t, func(d *ast.Document) { d.OutcomeNames = append(d.OutcomeNames, 1) }, col)
		peerCol(t, func(d *ast.Document) { d.OutcomePrecedence = append(d.OutcomePrecedence, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.OutcomeTerminal = append(d.OutcomeTerminal, false) }, col)
		peerCol(t, func(d *ast.Document) { d.OutcomeSourceStarts = append(d.OutcomeSourceStarts, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.OutcomeSourceEnds = append(d.OutcomeSourceEnds, 0) }, col)
		peerCol(t, func(d *ast.Document) {
			d.OutcomePrecedence = d.OutcomePrecedence[:0]
			d.OutcomeTerminal = d.OutcomeTerminal[:0]
			d.OutcomeSourceStarts = d.OutcomeSourceStarts[:0]
			d.OutcomeSourceEnds = d.OutcomeSourceEnds[:0]
		}, col)
	})
	t.Run("Remediation", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableRemediation}
		peerCol(t, func(d *ast.Document) { d.RemediationKinds = append(d.RemediationKinds, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.RemediationFields = append(d.RemediationFields, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.RemediationValues = append(d.RemediationValues, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.RemediationEvidenceKinds = append(d.RemediationEvidenceKinds, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.RemediationSourceStarts = append(d.RemediationSourceStarts, 0) }, col)
		peerCol(t, func(d *ast.Document) { d.RemediationSourceEnds = append(d.RemediationSourceEnds, 0) }, col)
		peerCol(t, func(d *ast.Document) {
			d.RemediationFields = d.RemediationFields[:0]
			d.RemediationValues = d.RemediationValues[:0]
			d.RemediationEvidenceKinds = d.RemediationEvidenceKinds[:0]
			d.RemediationSourceStarts = d.RemediationSourceStarts[:0]
			d.RemediationSourceEnds = d.RemediationSourceEnds[:0]
		}, col)
	})
}

// TestValidateCatalogColumnOrder locks the fixed column-order diagnostics:
// EvidenceKind, EvidenceState, Outcome, then Remediation.
func TestValidateCatalogColumnOrder(t *testing.T) {
	doc, fields := buildCatalogDoc(t)
	doc.EvidenceKindSourceStarts = doc.EvidenceKindSourceStarts[:0]
	doc.EvidenceKindSourceEnds = doc.EvidenceKindSourceEnds[:0]
	doc.EvidenceStateSourceStarts = doc.EvidenceStateSourceStarts[:0]
	doc.EvidenceStateSourceEnds = doc.EvidenceStateSourceEnds[:0]
	doc.OutcomePrecedence = doc.OutcomePrecedence[:0]
	doc.OutcomeTerminal = doc.OutcomeTerminal[:0]
	doc.OutcomeSourceStarts = doc.OutcomeSourceStarts[:0]
	doc.OutcomeSourceEnds = doc.OutcomeSourceEnds[:0]
	doc.RemediationFields = doc.RemediationFields[:0]
	doc.RemediationValues = doc.RemediationValues[:0]
	doc.RemediationEvidenceKinds = doc.RemediationEvidenceKinds[:0]
	doc.RemediationSourceStarts = doc.RemediationSourceStarts[:0]
	doc.RemediationSourceEnds = doc.RemediationSourceEnds[:0]
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableEvidenceKind},
		{Code: CodeColumnLength, Table: TableEvidenceState},
		{Code: CodeColumnLength, Table: TableOutcome},
		{Code: CodeColumnLength, Table: TableRemediation},
	})
}

// runCatalogNameTest covers the shared name rule for a symbol-named catalog or
// outcome table: zero and out-of-value-table names are CodeInvalidValue on
// MemberName with the valid owner span attached.
func runCatalogNameTest(t *testing.T, table TableKind, set func(*ast.Document, int, schema.ValueID), owner func(Diagnostic) Diagnostic) {
	t.Helper()
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("zero", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		set(doc, 0, 0)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			owner(Diagnostic{Code: CodeInvalidValue, Table: table, Member: MemberName, Row: 1, Value: 0, Span: span}),
		})
	})
	t.Run("high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		high := schema.ValueID(len(doc.ValueKinds) + 1)
		set(doc, 0, high)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			owner(Diagnostic{Code: CodeInvalidValue, Table: table, Member: MemberName, Row: 1, Value: high, Span: span}),
		})
	})
}

// runCatalogSpanTest covers the shared span rule: a valid zero-length span is
// accepted; reversed, end-beyond-input, and MaxUint32 ends are rejected as
// CodeInvalidSourceSpan on MemberSpan.
func runCatalogSpanTest(t *testing.T, table TableKind, set func(*ast.Document, int, uint32, uint32), owner func(Diagnostic) Diagnostic) {
	t.Helper()
	t.Run("valid zero-length", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		set(doc, 0, 3, 3)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), nil)
	})
	t.Run("reversed", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		set(doc, 0, 5, 2)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			owner(Diagnostic{Code: CodeInvalidSourceSpan, Table: table, Member: MemberSpan, Row: 1}),
		})
	})
	t.Run("end beyond input", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		set(doc, 0, 0, uint32(len(doc.InputBytes))+1)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			owner(Diagnostic{Code: CodeInvalidSourceSpan, Table: table, Member: MemberSpan, Row: 1}),
		})
	})
	t.Run("end near MaxUint32", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		set(doc, 0, 0, math.MaxUint32)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			owner(Diagnostic{Code: CodeInvalidSourceSpan, Table: table, Member: MemberSpan, Row: 1}),
		})
	})
}

func TestValidateCatalogEvidenceKindRows(t *testing.T) {
	runCatalogNameTest(t, TableEvidenceKind,
		func(d *ast.Document, i int, v schema.ValueID) { d.EvidenceKindNames[i] = v },
		func(d Diagnostic) Diagnostic { d.EvidenceKind = 1; return d })
	runCatalogSpanTest(t, TableEvidenceKind,
		func(d *ast.Document, i int, s, e uint32) {
			d.EvidenceKindSourceStarts[i] = s
			d.EvidenceKindSourceEnds[i] = e
		},
		func(d Diagnostic) Diagnostic { d.EvidenceKind = 1; return d })
}

func TestValidateCatalogEvidenceStateRows(t *testing.T) {
	runCatalogNameTest(t, TableEvidenceState,
		func(d *ast.Document, i int, v schema.ValueID) { d.EvidenceStateNames[i] = v },
		func(d Diagnostic) Diagnostic { d.EvidenceState = 1; return d })
	runCatalogSpanTest(t, TableEvidenceState,
		func(d *ast.Document, i int, s, e uint32) {
			d.EvidenceStateSourceStarts[i] = s
			d.EvidenceStateSourceEnds[i] = e
		},
		func(d Diagnostic) Diagnostic { d.EvidenceState = 1; return d })
}

func TestValidateCatalogOutcomeRows(t *testing.T) {
	runCatalogNameTest(t, TableOutcome,
		func(d *ast.Document, i int, v schema.ValueID) { d.OutcomeNames[i] = v },
		func(d Diagnostic) Diagnostic { d.Outcome = 1; return d })
	runCatalogSpanTest(t, TableOutcome,
		func(d *ast.Document, i int, s, e uint32) { d.OutcomeSourceStarts[i] = s; d.OutcomeSourceEnds[i] = e },
		func(d Diagnostic) Diagnostic { d.Outcome = 1; return d })
}

func TestValidateCatalogRemediationRefs(t *testing.T) {
	span := ast.SourceSpan{Start: 0, End: 4}
	t.Run("field high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationFields[0] = schema.FieldID(fields.Len() + 1)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidField, Table: TableRemediation, Member: MemberField, Row: 1, Field: schema.FieldID(fields.Len() + 1), Remediation: 1, Span: span},
		})
	})
	t.Run("value high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationValues[0] = schema.ValueID(len(doc.ValueKinds) + 1)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: 1, Value: schema.ValueID(len(doc.ValueKinds) + 1), Remediation: 1, Span: span},
		})
	})
	t.Run("evidence high", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationEvidenceKinds[1] = schema.EvidenceKindID(len(doc.EvidenceKindNames) + 1)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidEvidence, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, EvidenceKind: schema.EvidenceKindID(len(doc.EvidenceKindNames) + 1), Remediation: 2, Span: span},
		})
	})
	t.Run("zero accepted structurally", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationFields[0] = 0
		doc.RemediationValues[0] = 0
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), nil)
	})
}

func TestValidateCatalogRemediationSpan(t *testing.T) {
	runCatalogSpanTest(t, TableRemediation,
		func(d *ast.Document, i int, s, e uint32) {
			d.RemediationSourceStarts[i] = s
			d.RemediationSourceEnds[i] = e
		},
		func(d Diagnostic) Diagnostic { d.Remediation = 1; return d })
}

// TestValidateCatalogRowOrder locks per-row ordering: name/reference checks
// first, then the span check, with exact codes, tables, members, rows, strong
// owner and target IDs, and span attachment.
func TestValidateCatalogRowOrder(t *testing.T) {
	t.Run("evidence kind name then span", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.EvidenceKindNames[0] = 0
		doc.EvidenceKindSourceStarts[0] = 5
		doc.EvidenceKindSourceEnds[0] = 2
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableEvidenceKind, Member: MemberName, Row: 1, Value: 0, EvidenceKind: 1},
			{Code: CodeInvalidSourceSpan, Table: TableEvidenceKind, Member: MemberSpan, Row: 1, EvidenceKind: 1},
		})
	})
	t.Run("outcome name then span", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.OutcomeNames[0] = schema.ValueID(len(doc.ValueKinds) + 1)
		doc.OutcomeSourceStarts[0] = 7
		doc.OutcomeSourceEnds[0] = 3
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidValue, Table: TableOutcome, Member: MemberName, Row: 1, Value: schema.ValueID(len(doc.ValueKinds) + 1), Outcome: 1},
			{Code: CodeInvalidSourceSpan, Table: TableOutcome, Member: MemberSpan, Row: 1, Outcome: 1},
		})
	})
	t.Run("remediation refs then span", func(t *testing.T) {
		doc, fields := buildCatalogDoc(t)
		doc.RemediationFields[0] = schema.FieldID(fields.Len() + 1)
		doc.RemediationValues[0] = schema.ValueID(len(doc.ValueKinds) + 1)
		doc.RemediationEvidenceKinds[0] = schema.EvidenceKindID(len(doc.EvidenceKindNames) + 1)
		doc.RemediationSourceStarts[0] = 9
		doc.RemediationSourceEnds[0] = 2
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidField, Table: TableRemediation, Member: MemberField, Row: 1, Field: schema.FieldID(fields.Len() + 1), Remediation: 1},
			{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: 1, Value: schema.ValueID(len(doc.ValueKinds) + 1), Remediation: 1},
			{Code: CodeInvalidEvidence, Table: TableRemediation, Member: MemberEvidenceKind, Row: 1, EvidenceKind: schema.EvidenceKindID(len(doc.EvidenceKindNames) + 1), Remediation: 1},
			{Code: CodeInvalidSourceSpan, Table: TableRemediation, Member: MemberSpan, Row: 1, Remediation: 1},
		})
	})
}

// TestValidateCatalogTruncationNoPanic truncates each peer column in each
// catalog table and asserts the safe minimum row count prevents any panic.
// Truncating the evidence-kind name column legitimately cascades into a
// remediation evidence-kind reference diagnostic because remediation rows
// bound against the evidence-kind catalog length.
func TestValidateCatalogTruncationNoPanic(t *testing.T) {
	trunc := func(t *testing.T, mutate func(*ast.Document), wantDiag ...Diagnostic) {
		t.Helper()
		doc, fields := buildCatalogDoc(t)
		mutate(doc)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), wantDiag)
	}
	t.Run("EvidenceKind", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableEvidenceKind}
		trunc(t, func(d *ast.Document) { d.EvidenceKindNames = d.EvidenceKindNames[:0] }, col,
			Diagnostic{Code: CodeInvalidEvidence, Table: TableRemediation, Member: MemberEvidenceKind, Row: 2, EvidenceKind: 1, Remediation: 2, Span: ast.SourceSpan{Start: 0, End: 4}})
		trunc(t, func(d *ast.Document) { d.EvidenceKindSourceStarts = d.EvidenceKindSourceStarts[:0] }, col)
		trunc(t, func(d *ast.Document) { d.EvidenceKindSourceEnds = d.EvidenceKindSourceEnds[:0] }, col)
	})
	t.Run("EvidenceState", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableEvidenceState}
		trunc(t, func(d *ast.Document) { d.EvidenceStateNames = d.EvidenceStateNames[:0] }, col)
		trunc(t, func(d *ast.Document) { d.EvidenceStateSourceStarts = d.EvidenceStateSourceStarts[:0] }, col)
		trunc(t, func(d *ast.Document) { d.EvidenceStateSourceEnds = d.EvidenceStateSourceEnds[:0] }, col)
	})
	t.Run("Outcome", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableOutcome}
		trunc(t, func(d *ast.Document) { d.OutcomeNames = d.OutcomeNames[:0] }, col)
		trunc(t, func(d *ast.Document) { d.OutcomePrecedence = d.OutcomePrecedence[:0] }, col)
		trunc(t, func(d *ast.Document) { d.OutcomeTerminal = d.OutcomeTerminal[:0] }, col)
		trunc(t, func(d *ast.Document) { d.OutcomeSourceStarts = d.OutcomeSourceStarts[:0] }, col)
		trunc(t, func(d *ast.Document) { d.OutcomeSourceEnds = d.OutcomeSourceEnds[:0] }, col)
	})
	t.Run("Remediation", func(t *testing.T) {
		col := Diagnostic{Code: CodeColumnLength, Table: TableRemediation}
		trunc(t, func(d *ast.Document) { d.RemediationKinds = d.RemediationKinds[:0] }, col)
		trunc(t, func(d *ast.Document) { d.RemediationFields = d.RemediationFields[:0] }, col)
		trunc(t, func(d *ast.Document) { d.RemediationValues = d.RemediationValues[:0] }, col)
		trunc(t, func(d *ast.Document) { d.RemediationEvidenceKinds = d.RemediationEvidenceKinds[:0] }, col)
		trunc(t, func(d *ast.Document) { d.RemediationSourceStarts = d.RemediationSourceStarts[:0] }, col)
		trunc(t, func(d *ast.Document) { d.RemediationSourceEnds = d.RemediationSourceEnds[:0] }, col)
	})
}
