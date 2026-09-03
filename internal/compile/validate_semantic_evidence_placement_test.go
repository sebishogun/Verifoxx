package compile

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestValidateSemanticEvidencePlacement(t *testing.T) {
	t.Run("requirement applicability", func(t *testing.T) {
		fixture := buildExplanationCSEFixture(t)
		fixture.doc.RequirementApplicabilityRoots[0] = fixture.evidenceA
		want(t, Validate(nil, fixture.doc, fixture.fields), []Diagnostic{{
			Code: CodeInvalidEvidence, Table: TableRequirement, Member: MemberApplicability,
			Row: 1, Requirement: 1, Node: fixture.evidenceA, Span: ast.SourceSpan{Start: 0, End: 2},
		}})
	})

	t.Run("clause assertion", func(t *testing.T) {
		fixture := buildExplanationCSEFixture(t)
		fixture.doc.ClauseAssertionRoots[0] = fixture.evidenceA
		want(t, Validate(nil, fixture.doc, fixture.fields), []Diagnostic{{
			Code: CodeInvalidEvidence, Table: TableClause, Member: MemberAssertion,
			Row: 1, Clause: 1, Node: fixture.evidenceA, Span: ast.SourceSpan{Start: 0, End: 2},
		}})
	})

	t.Run("boolean child", func(t *testing.T) {
		fixture := buildExplanationCSEFixture(t)
		assertion := fixture.doc.RequirementApplicabilityRoots[0]
		row := int(assertion - 1)
		fixture.doc.NodeKinds[row] = ast.NodeKindAll
		fixture.doc.NodeRefs[row] = uint32(len(fixture.doc.GroupChildStarts))
		fixture.doc.GroupChildStarts = append(fixture.doc.GroupChildStarts, uint32(len(fixture.doc.ChildNodeIDs)))
		fixture.doc.GroupChildCounts = append(fixture.doc.GroupChildCounts, 1)
		fixture.doc.ChildNodeIDs = append(fixture.doc.ChildNodeIDs, fixture.evidenceA)
		want(t, Validate(nil, fixture.doc, fixture.fields), []Diagnostic{{
			Code: CodeInvalidEvidence, Table: TableNode, Member: MemberChild,
			Row: uint32(assertion), Node: fixture.evidenceA, Span: ast.SourceSpan{Start: 0, End: 2},
		}})
	})

	t.Run("not child", func(t *testing.T) {
		fixture := buildExplanationCSEFixture(t)
		assertion := fixture.doc.RequirementApplicabilityRoots[0]
		row := int(assertion - 1)
		fixture.doc.NodeKinds[row] = ast.NodeKindNot
		fixture.doc.NodeRefs[row] = uint32(len(fixture.doc.NotChildren))
		fixture.doc.NotChildren = append(fixture.doc.NotChildren, fixture.evidenceA)
		want(t, Validate(nil, fixture.doc, fixture.fields), []Diagnostic{{
			Code: CodeInvalidEvidence, Table: TableNode, Member: MemberChild,
			Row: uint32(assertion), Node: fixture.evidenceA, Span: ast.SourceSpan{Start: 0, End: 2},
		}})
	})
}

func TestValidateSemanticClauseEvidenceRootRemainsValid(t *testing.T) {
	fixture := buildExplanationCSEFixture(t)
	if diagnostics := Validate(nil, fixture.doc, fixture.fields); len(diagnostics) != 0 {
		t.Fatalf("valid clause evidence diagnostics: %+v", diagnostics)
	}
	if fixture.doc.ClauseEvidenceNodeIDs[0] != schema.NodeID(fixture.evidenceA) {
		t.Fatal("fixture does not cover a direct clause evidence root")
	}
}
