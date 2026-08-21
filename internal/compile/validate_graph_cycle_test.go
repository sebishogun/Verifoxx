package compile

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// appendCycleTestNot appends structurally and semantically valid Not nodes to
// doc, one per target in order, each with source span {0,1} and a payload ref
// at the tail of NotChildren, and returns the NodeID of the first appended
// node. Every parallel node column is preserved.
func appendCycleTestNot(t *testing.T, doc *ast.Document, targets ...schema.NodeID) schema.NodeID {
	t.Helper()
	first := schema.NodeID(len(doc.NodeKinds) + 1)
	for _, target := range targets {
		doc.NodeKinds = append(doc.NodeKinds, ast.NodeKindNot)
		doc.NodeRefs = append(doc.NodeRefs, uint32(len(doc.NotChildren)))
		doc.SourceStarts = append(doc.SourceStarts, 0)
		doc.SourceEnds = append(doc.SourceEnds, 1)
		doc.NotChildren = append(doc.NotChildren, target)
	}
	return first
}

// appendCycleTestExists appends one structurally and semantically valid Exists
// compare leaf node with source span {0,1} and a payload row at the tail of
// the five compare peer columns, and returns its NodeID.
func appendCycleTestExists(t *testing.T, doc *ast.Document, field schema.FieldID) schema.NodeID {
	t.Helper()
	id := schema.NodeID(len(doc.NodeKinds) + 1)
	ref := uint32(len(doc.CompareFields))
	doc.NodeKinds = append(doc.NodeKinds, ast.NodeKindCompare)
	doc.NodeRefs = append(doc.NodeRefs, ref)
	doc.SourceStarts = append(doc.SourceStarts, 0)
	doc.SourceEnds = append(doc.SourceEnds, 1)
	doc.CompareFields = append(doc.CompareFields, field)
	doc.CompareOps = append(doc.CompareOps, ast.CompareOpExists)
	doc.CompareValues = append(doc.CompareValues, 0)
	doc.CompareListStarts = append(doc.CompareListStarts, 0)
	doc.CompareListCounts = append(doc.CompareListCounts, 0)
	return id
}

// appendCycleTestGroupChildren appends children to the tail of the shared CSR
// edge column and extends the group payload row's count so the row owns the
// appended edges after its existing range.
func appendCycleTestGroupChildren(doc *ast.Document, ref uint32, children ...schema.NodeID) {
	doc.ChildNodeIDs = append(doc.ChildNodeIDs, children...)
	doc.GroupChildCounts[ref] += uint16(len(children))
}

// appendCycleTestGroup appends one structurally and semantically valid group
// node whose nonempty child range occupies the tail of the shared edge column.
func appendCycleTestGroup(doc *ast.Document, kind ast.NodeKind, children ...schema.NodeID) schema.NodeID {
	start := uint32(len(doc.ChildNodeIDs))
	doc.ChildNodeIDs = append(doc.ChildNodeIDs, children...)
	ref := uint32(len(doc.GroupChildStarts))
	doc.GroupChildStarts = append(doc.GroupChildStarts, start)
	doc.GroupChildCounts = append(doc.GroupChildCounts, uint16(len(children)))
	doc.NodeKinds = append(doc.NodeKinds, kind)
	doc.NodeRefs = append(doc.NodeRefs, ref)
	doc.SourceStarts = append(doc.SourceStarts, 0)
	doc.SourceEnds = append(doc.SourceEnds, 1)
	return schema.NodeID(len(doc.NodeKinds))
}

func TestValidateGraphCycleSelfNot(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NotChildren[0] = 3
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 3, Node: 3, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateGraphCycleTwoNodeGroupNot(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NotChildren[0] = 5
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 3, Node: 5, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateGraphCycleOrphanPair(t *testing.T) {
	doc, fields := buildMinimal(t)
	appendCycleTestNot(t, doc, schema.NodeID(8), schema.NodeID(7))
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 8, Node: 7, Span: ast.SourceSpan{Start: 0, End: 1}},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 7, Node: 7, Span: ast.SourceSpan{Start: 0, End: 1}},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 8, Node: 8, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateGraphCycleReachableBeforeOrphan(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NotChildren[0] = 3
	appendCycleTestNot(t, doc, schema.NodeID(8), schema.NodeID(7))
	seed := []Diagnostic{{Code: CodeCycle, Row: 77}}
	var v Validator
	want(t, v.Validate(seed, doc, fields), []Diagnostic{
		{Code: CodeCycle, Row: 77},
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 3, Node: 3, Span: ast.SourceSpan{Start: 0, End: 1}},
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 8, Node: 7, Span: ast.SourceSpan{Start: 0, End: 1}},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 7, Node: 7, Span: ast.SourceSpan{Start: 0, End: 1}},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 8, Node: 8, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateGraphCyclePhaseOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementIDs[0] = 0
	doc.NotChildren[0] = 3
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 3, Node: 3, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateGraphCycleInvalidSourceSpan(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.SourceStarts[2] = 5
	doc.SourceEnds[2] = 2
	doc.NotChildren[0] = 3
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableNode, Row: 3, Node: 3},
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 3, Node: 3},
	})
}

func TestValidateGraphCycleBlackEdgeClean(t *testing.T) {
	doc, fields := buildMinimal(t)
	leaf := appendCycleTestExists(t, doc, schema.FieldID(1))
	first := appendCycleTestNot(t, doc, leaf)
	appendCycleTestNot(t, doc, leaf)
	appendCycleTestGroupChildren(doc, doc.NodeRefs[4], first, first+1)
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

func TestValidateGraphCycleClauseAssertionRoot(t *testing.T) {
	doc, fields := buildMinimal(t)
	first := appendCycleTestNot(t, doc, schema.NodeID(8), schema.NodeID(7))
	root := appendCycleTestGroup(doc, ast.NodeKindAll, 4, first)
	doc.ClauseAssertionRoots[0] = root
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 8, Node: 7, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}

func TestValidateGraphCycleRequirementRootOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	first := appendCycleTestNot(t, doc, schema.NodeID(7), schema.NodeID(8))
	span := ast.SourceSpan{Start: 0, End: 1}
	appendReachTestRequirement(doc, schema.RequirementID(2), first, []schema.ClauseID{1}, span)
	appendReachTestRequirement(doc, schema.RequirementID(3), first+1, []schema.ClauseID{1}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 7, Node: 7, Span: span},
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 8, Node: 8, Span: span},
	})
}

func TestValidateGraphCycleGroupSecondEdge(t *testing.T) {
	doc, fields := buildMinimal(t)
	group := appendCycleTestGroup(doc, ast.NodeKindAny, 5, 7)
	span := ast.SourceSpan{Start: 0, End: 1}
	appendReachTestRequirement(doc, schema.RequirementID(2), group, []schema.ClauseID{1}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: 7, Node: 7, Span: span},
	})
}
