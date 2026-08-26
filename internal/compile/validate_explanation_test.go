package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestValidateTemplateStructuralDefects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document)
		want   Diagnostic
	}{
		{
			name: "operation range",
			mutate: func(d *ast.Document) {
				d.TemplateOpStarts[0] = math.MaxUint32
			},
			want: Diagnostic{Code: CodeInvalidCSRRange, Table: TableTemplate, Member: MemberOperation, Row: 1},
		},
		{
			name: "context",
			mutate: func(d *ast.Document) {
				d.TemplateContexts[0] = ast.TemplateContextInvalid
			},
			want: Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberContext, Row: 1},
		},
		{
			name: "operation",
			mutate: func(d *ast.Document) {
				d.TemplateOps[d.TemplateOpStarts[0]] = ast.TemplateOpInvalid
			},
			want: Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberOperation, Row: 1},
		},
		{
			name: "literal range",
			mutate: func(d *ast.Document) {
				d.TemplateLiteralStarts[0] = math.MaxUint32
			},
			want: Diagnostic{Code: CodeInvalidCSRRange, Table: TableTemplate, Member: MemberPayload, Row: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, fields := fixture(t)
			tt.mutate(doc)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{tt.want})
		})
	}
}

func TestValidateExplanationStructuralDefects(t *testing.T) {
	t.Run("rationale ID", func(t *testing.T) {
		doc, fields := fixture(t)
		doc.ExplanationRationaleIDs[0] = schema.TemplateID(len(doc.TemplateOpStarts) + 1)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidExplanation, Table: TableExplanation, Member: MemberRationale, Row: 1},
		})
	})
	t.Run("uncertainty range", func(t *testing.T) {
		doc, fields := fixture(t)
		row := -1
		for i, count := range doc.ExplanationUncertaintyCounts {
			if count != 0 {
				row = i
				break
			}
		}
		if row < 0 {
			t.Fatal("fixture has no uncertainty row")
		}
		doc.ExplanationUncertaintyStarts[row] = math.MaxUint32
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidCSRRange, Table: TableExplanation, Member: MemberUncertainty, Row: uint32(row + 1)},
		})
	})
	t.Run("assumption ID", func(t *testing.T) {
		doc, fields := fixture(t)
		doc.AssumptionTemplateIDs[0] = schema.TemplateID(len(doc.TemplateOpStarts) + 1)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidTemplate, Table: TableDocument, Member: MemberAssumptions, Row: 1},
		})
	})
}

func TestValidateExplanationSemanticDefects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document)
		want   Diagnostic
	}{
		{
			name: "missing assumptions",
			mutate: func(d *ast.Document) {
				d.AssumptionsSet = d.AssumptionsSet[:0]
			},
			want: Diagnostic{Code: CodeMissingExplanation, Table: TableDocument, Member: MemberAssumptions},
		},
		{
			name: "assumption context",
			mutate: func(d *ast.Document) {
				d.AssumptionTemplateIDs[0] = d.ExplanationRationaleIDs[0]
			},
			want: Diagnostic{Code: CodeInvalidTemplate, Table: TableDocument, Member: MemberAssumptions, Row: 1},
		},
		{
			name: "missing evidence issue",
			mutate: func(d *ast.Document) {
				d.EvidenceIssueTemplateIDs[ast.EvidenceIssueConflict] = 0
			},
			want: Diagnostic{Code: CodeMissingExplanation, Table: TableEvidenceNode, Member: MemberTemplate, Row: 1},
		},
		{
			name: "missing clause explanation",
			mutate: func(d *ast.Document) {
				d.ClauseExplanationIDs[0] = 0
			},
			want: Diagnostic{Code: CodeMissingExplanation, Table: TableClause, Member: MemberExplanationSatisfied, Row: 1, Clause: 1},
		},
		{
			name: "clause explanation context",
			mutate: func(d *ast.Document) {
				d.ClauseExplanationIDs[0] = d.ClauseExplanationIDs[2]
			},
			want: Diagnostic{Code: CodeInvalidExplanation, Table: TableClause, Member: MemberExplanationSatisfied, Row: 1, Clause: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, fields := fixture(t)
			tt.mutate(doc)
			var v Validator
			want(t, v.Validate(nil, doc, fields), []Diagnostic{tt.want})
		})
	}
}

func TestValidateExplanationParallelColumnLengths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document)
		table  TableKind
	}{
		{"template headers", func(d *ast.Document) { d.TemplateOpCounts = d.TemplateOpCounts[:len(d.TemplateOpCounts)-1] }, TableTemplate},
		{"template operations", func(d *ast.Document) { d.TemplateArgs = d.TemplateArgs[:len(d.TemplateArgs)-1] }, TableTemplate},
		{"explanation headers", func(d *ast.Document) {
			d.ExplanationUncertaintyCounts = d.ExplanationUncertaintyCounts[:len(d.ExplanationUncertaintyCounts)-1]
		}, TableExplanation},
		{"evidence issues", func(d *ast.Document) {
			d.EvidenceIssueTemplateIDs = d.EvidenceIssueTemplateIDs[:len(d.EvidenceIssueTemplateIDs)-1]
		}, TableEvidenceNode},
		{"clause explanations", func(d *ast.Document) { d.ClauseExplanationIDs = d.ClauseExplanationIDs[:len(d.ClauseExplanationIDs)-1] }, TableClause},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, fields := fixture(t)
			tt.mutate(doc)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{{Code: CodeColumnLength, Table: tt.table}})
		})
	}
}

func TestValidateExplanationCorruptRangesDoNotPanic(t *testing.T) {
	doc, fields := fixture(t)
	doc.TemplateOpStarts[0] = math.MaxUint32
	doc.TemplateLiteralStarts[0] = math.MaxUint32
	doc.ExplanationUncertaintyStarts[0] = math.MaxUint32
	var v Validator
	_ = v.Validate(nil, doc, fields)
}
