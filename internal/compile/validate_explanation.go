package compile

import (
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func templateContextAt(doc *ast.Document, id schema.TemplateID) (ast.TemplateContext, bool) {
	if id == 0 || uint64(id) > uint64(len(doc.TemplateContexts)) {
		return ast.TemplateContextInvalid, false
	}
	return doc.TemplateContexts[id-1], true
}

func explanationContextAt(doc *ast.Document, id schema.ExplanationID) (ast.TemplateContext, bool) {
	if id == 0 || uint64(id) > uint64(len(doc.ExplanationRationaleIDs)) {
		return ast.TemplateContextInvalid, false
	}
	return templateContextAt(doc, doc.ExplanationRationaleIDs[id-1])
}

func (v *Validator) checkTemplateRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	base := len(doc.TemplateOpStarts)
	if len(doc.TemplateOpCounts) != base || len(doc.TemplateLiteralStarts) != base ||
		len(doc.TemplateMaxBytes) != base || len(doc.TemplateContexts) != base ||
		len(doc.TemplateOps) != len(doc.TemplateArgs) {
		return dst
	}
	rows := safeMin(
		len(doc.TemplateOpStarts), len(doc.TemplateOpCounts), len(doc.TemplateLiteralStarts),
		len(doc.TemplateMaxBytes), len(doc.TemplateContexts),
	)
	opRows := minInt(len(doc.TemplateOps), len(doc.TemplateArgs))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		context := doc.TemplateContexts[i]
		contextValid := context.Valid()
		if !contextValid {
			dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberContext, Row: row})
		}
		start := doc.TemplateOpStarts[i]
		count := uint32(doc.TemplateOpCounts[i])
		if count > ast.MaxTemplateOps || !validRange(start, count, opRows) {
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableTemplate, Member: MemberOperation, Row: row})
			continue
		}
		var literalBytes uint64
		operationsValid := true
		for j := uint32(0); j < count; j++ {
			index := int(start + j)
			op := doc.TemplateOps[index]
			arg := doc.TemplateArgs[index]
			if !op.Valid() {
				dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberOperation, Row: row})
				operationsValid = false
				continue
			}
			if contextValid && !op.AllowedIn(context) {
				dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberContext, Row: row})
			}
			if op == ast.TemplateOpLiteral {
				literalBytes += uint64(arg)
			} else if arg != 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberValue, Row: row})
			}
		}
		if !operationsValid {
			continue
		}
		literalStart := doc.TemplateLiteralStarts[i]
		if literalBytes > uint64(^uint32(0)) || !validRange(literalStart, uint32(literalBytes), len(doc.TemplateBytes)) {
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableTemplate, Member: MemberPayload, Row: row})
			continue
		}
		if literalBytes > ast.MaxTemplateBytes || uint64(doc.TemplateMaxBytes[i]) != literalBytes {
			dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableTemplate, Member: MemberPayload, Row: row})
		}
	}
	return dst
}

func (v *Validator) checkExplanationRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	templateMax := uint64(len(doc.TemplateOpStarts))
	for i, id := range doc.AssumptionTemplateIDs {
		if id == 0 || uint64(id) > templateMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableDocument, Member: MemberAssumptions, Row: uint32(i + 1)})
		}
	}
	rows := safeMin(
		len(doc.ExplanationRationaleIDs),
		len(doc.ExplanationUncertaintyStarts),
		len(doc.ExplanationUncertaintyCounts),
	)
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		if rationale := doc.ExplanationRationaleIDs[i]; rationale != 0 && uint64(rationale) > templateMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidExplanation, Table: TableExplanation, Member: MemberRationale, Row: row})
		}
		start := doc.ExplanationUncertaintyStarts[i]
		count := uint32(doc.ExplanationUncertaintyCounts[i])
		if count > ast.MaxUncertainty || !validRange(start, count, len(doc.ExplanationUncertaintyIDs)) {
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableExplanation, Member: MemberUncertainty, Row: row})
			continue
		}
		for j := uint32(0); j < count; j++ {
			id := doc.ExplanationUncertaintyIDs[int(start+j)]
			if id != 0 && uint64(id) > templateMax {
				dst = append(dst, Diagnostic{Code: CodeInvalidExplanation, Table: TableExplanation, Member: MemberUncertainty, Row: row})
			}
		}
	}
	return dst
}

func (v *Validator) checkExplanationSemantics(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	policyPresent := doc.Metadata.Name != 0 || doc.Metadata.Version != 0 ||
		len(doc.TemplateOpStarts) != 0 || len(doc.ExplanationRationaleIDs) != 0 ||
		len(doc.RequirementIDs) != 0
	if policyPresent && (len(doc.AssumptionsSet) != 1 || doc.AssumptionsSet[0] != 1) {
		dst = append(dst, Diagnostic{Code: CodeMissingExplanation, Table: TableDocument, Member: MemberAssumptions})
	} else if len(doc.AssumptionsSet) == 1 && doc.AssumptionsSet[0] == 1 {
		for i, id := range doc.AssumptionTemplateIDs {
			context, ok := templateContextAt(doc, id)
			if ok && context != ast.TemplateContextAssumption {
				dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableDocument, Member: MemberAssumptions, Row: uint32(i + 1)})
			}
		}
	}

	rows := safeMin(
		len(doc.ExplanationRationaleIDs),
		len(doc.ExplanationUncertaintyStarts),
		len(doc.ExplanationUncertaintyCounts),
	)
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		rationale := doc.ExplanationRationaleIDs[i]
		context, rationaleOK := templateContextAt(doc, rationale)
		if rationale == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingExplanation, Table: TableExplanation, Member: MemberRationale, Row: row})
		} else if rationaleOK && context != ast.TemplateContextDecision && context != ast.TemplateContextUnresolved {
			dst = append(dst, Diagnostic{Code: CodeInvalidExplanation, Table: TableExplanation, Member: MemberRationale, Row: row})
		}
		start := doc.ExplanationUncertaintyStarts[i]
		count := uint32(doc.ExplanationUncertaintyCounts[i])
		if !validRange(start, count, len(doc.ExplanationUncertaintyIDs)) {
			continue
		}
		for j := uint32(0); j < count; j++ {
			id := doc.ExplanationUncertaintyIDs[int(start+j)]
			uncertaintyContext, ok := templateContextAt(doc, id)
			if id == 0 {
				dst = append(dst, Diagnostic{Code: CodeMissingExplanation, Table: TableExplanation, Member: MemberUncertainty, Row: row})
			} else if rationaleOK && ok && uncertaintyContext != context {
				dst = append(dst, Diagnostic{Code: CodeInvalidExplanation, Table: TableExplanation, Member: MemberUncertainty, Row: row})
			}
		}
	}

	for i := range doc.EvidenceKinds {
		start := uint64(i) * uint64(ast.EvidenceIssueReasonCount)
		end := start + uint64(ast.EvidenceIssueReasonCount)
		if end > uint64(len(doc.EvidenceIssueTemplateIDs)) {
			continue
		}
		row := doc.EvidenceIssueTemplateIDs[int(start):int(end)]
		for reason, id := range row {
			context, ok := templateContextAt(doc, id)
			if id == 0 {
				dst = append(dst, Diagnostic{Code: CodeMissingExplanation, Table: TableEvidenceNode, Member: MemberTemplate, Row: uint32(i + 1)})
				break
			}
			if !ok {
				continue
			}
			valid := context == ast.TemplateContextEvidenceMissing
			if reason != int(ast.EvidenceIssueMissing) {
				valid = valid || context == ast.TemplateContextEvidencePresent
			}
			if !valid {
				dst = append(dst, Diagnostic{Code: CodeInvalidTemplate, Table: TableEvidenceNode, Member: MemberTemplate, Row: uint32(i + 1)})
				break
			}
		}
	}

	explanationMembers := [...]MemberKind{
		MemberExplanationSatisfied,
		MemberExplanationFalse,
		MemberExplanationMissing,
		MemberExplanationStale,
		MemberExplanationUnclear,
		MemberExplanationUnverifiable,
		MemberExplanationConflict,
	}
	for i := range doc.ClauseAssertionRoots {
		start := uint64(i) * uint64(ast.ResolutionBranchCount)
		end := start + uint64(ast.ResolutionBranchCount)
		if end > uint64(len(doc.ClauseExplanationIDs)) {
			continue
		}
		for branch, id := range doc.ClauseExplanationIDs[int(start):int(end)] {
			member := explanationMembers[branch]
			if id == 0 {
				dst = append(dst, Diagnostic{Code: CodeMissingExplanation, Table: TableClause, Member: member, Row: uint32(i + 1), Clause: schema.ClauseID(i + 1)})
				continue
			}
			context, ok := explanationContextAt(doc, id)
			if !ok {
				continue
			}
			want := ast.TemplateContextUnresolved
			if branch < 2 {
				want = ast.TemplateContextDecision
			}
			if context != want {
				dst = append(dst, Diagnostic{Code: CodeInvalidExplanation, Table: TableClause, Member: member, Row: uint32(i + 1), Clause: schema.ClauseID(i + 1)})
			}
		}
	}
	return dst
}
