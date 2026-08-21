package compile

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// appendReachTestNode appends one node row with all four peer columns kept in
// sync and returns its new NodeID. ref must index a valid payload row in the
// payload table named by kind so the appended node stays structurally safe.
func appendReachTestNode(doc *ast.Document, kind ast.NodeKind, ref uint32, span ast.SourceSpan) schema.NodeID {
	doc.NodeKinds = append(doc.NodeKinds, kind)
	doc.NodeRefs = append(doc.NodeRefs, ref)
	doc.SourceStarts = append(doc.SourceStarts, span.Start)
	doc.SourceEnds = append(doc.SourceEnds, span.End)
	return schema.NodeID(len(doc.NodeKinds))
}

// appendReachTestCompare appends an Exists compare node (a graph leaf) with a
// fresh compare payload row and returns its NodeID.
func appendReachTestCompare(doc *ast.Document, field schema.FieldID, span ast.SourceSpan) schema.NodeID {
	ref := uint32(len(doc.CompareFields))
	doc.CompareFields = append(doc.CompareFields, field)
	doc.CompareOps = append(doc.CompareOps, ast.CompareOpExists)
	doc.CompareValues = append(doc.CompareValues, 0)
	doc.CompareListStarts = append(doc.CompareListStarts, uint32(len(doc.ListValueIDs)))
	doc.CompareListCounts = append(doc.CompareListCounts, 0)
	return appendReachTestNode(doc, ast.NodeKindCompare, ref, span)
}

// appendReachTestNot appends a negation node whose one child is child and
// returns its NodeID.
func appendReachTestNot(doc *ast.Document, child schema.NodeID, span ast.SourceSpan) schema.NodeID {
	ref := uint32(len(doc.NotChildren))
	doc.NotChildren = append(doc.NotChildren, child)
	return appendReachTestNode(doc, ast.NodeKindNot, ref, span)
}

// appendReachTestGroup appends a nonempty All/Any group node whose children are
// appended to the shared CSR edge column at the tail, returning the NodeID.
func appendReachTestGroup(doc *ast.Document, kind ast.NodeKind, children []schema.NodeID, span ast.SourceSpan) schema.NodeID {
	start := uint32(len(doc.ChildNodeIDs))
	doc.ChildNodeIDs = append(doc.ChildNodeIDs, children...)
	ref := uint32(len(doc.GroupChildStarts))
	doc.GroupChildStarts = append(doc.GroupChildStarts, start)
	doc.GroupChildCounts = append(doc.GroupChildCounts, uint16(len(children)))
	return appendReachTestNode(doc, kind, ref, span)
}

// appendReachTestEvidence appends an evidence node referencing the existing
// evidence-kind and evidence-state catalog rows and returns its NodeID.
func appendReachTestEvidence(doc *ast.Document, kind schema.EvidenceKindID, state schema.EvidenceStateID, span ast.SourceSpan) schema.NodeID {
	ref := uint32(len(doc.EvidenceKinds))
	doc.EvidenceKinds = append(doc.EvidenceKinds, kind)
	doc.EvidenceStates = append(doc.EvidenceStates, state)
	return appendReachTestNode(doc, ast.NodeKindEvidence, ref, span)
}

// appendReachTestClause appends one clause row with a valid assertion root, an
// evidence CSR over the given edges, an empty remediation range, a complete
// seven-slot resolution, and span, returning the new ClauseID. Every peer
// column grows so the structural and semantic safe minimums keep scanning the
// new row.
func appendReachTestClause(doc *ast.Document, assertion schema.NodeID, evidence []schema.NodeID, resolution ast.Resolution, span ast.SourceSpan) schema.ClauseID {
	estart := uint32(len(doc.ClauseEvidenceNodeIDs))
	doc.ClauseEvidenceNodeIDs = append(doc.ClauseEvidenceNodeIDs, evidence...)
	rstart := uint32(len(doc.ClauseRemediationIDs))
	id := schema.ClauseID(len(doc.ClauseAssertionRoots) + 1)
	doc.ClauseAssertionRoots = append(doc.ClauseAssertionRoots, assertion)
	doc.ClauseEvidenceStarts = append(doc.ClauseEvidenceStarts, estart)
	doc.ClauseEvidenceCounts = append(doc.ClauseEvidenceCounts, uint16(len(evidence)))
	doc.ClauseRemediationStarts = append(doc.ClauseRemediationStarts, rstart)
	doc.ClauseRemediationCounts = append(doc.ClauseRemediationCounts, 0)
	doc.ClauseOnSatisfied = append(doc.ClauseOnSatisfied, resolution.OnSatisfied)
	doc.ClauseOnFalse = append(doc.ClauseOnFalse, resolution.OnFalse)
	doc.ClauseOnMissing = append(doc.ClauseOnMissing, resolution.OnMissing)
	doc.ClauseOnStale = append(doc.ClauseOnStale, resolution.OnStale)
	doc.ClauseOnUnclear = append(doc.ClauseOnUnclear, resolution.OnUnclear)
	doc.ClauseOnUnverifiable = append(doc.ClauseOnUnverifiable, resolution.OnUnverifiable)
	doc.ClauseOnConflict = append(doc.ClauseOnConflict, resolution.OnConflict)
	doc.ClauseSourceStarts = append(doc.ClauseSourceStarts, span.Start)
	doc.ClauseSourceEnds = append(doc.ClauseSourceEnds, span.End)
	return id
}

// appendReachTestRequirement appends one requirement row with a valid
// applicability root and a nonempty clause CSR over the given edges.
func appendReachTestRequirement(doc *ast.Document, id schema.RequirementID, applicability schema.NodeID, clauses []schema.ClauseID, span ast.SourceSpan) {
	start := uint32(len(doc.RequirementClauseIDs))
	doc.RequirementClauseIDs = append(doc.RequirementClauseIDs, clauses...)
	doc.RequirementIDs = append(doc.RequirementIDs, id)
	doc.RequirementApplicabilityRoots = append(doc.RequirementApplicabilityRoots, applicability)
	doc.RequirementClauseStarts = append(doc.RequirementClauseStarts, start)
	doc.RequirementClauseCounts = append(doc.RequirementClauseCounts, uint16(len(clauses)))
	doc.RequirementSourceStarts = append(doc.RequirementSourceStarts, span.Start)
	doc.RequirementSourceEnds = append(doc.RequirementSourceEnds, span.End)
}

// appendReachTestResolution returns the complete seven-outcome resolution used
// by every appended clause: approve, reject, and escalate everywhere else.
func appendReachTestResolution() ast.Resolution {
	return ast.Resolution{
		OnSatisfied:    1,
		OnFalse:        2,
		OnMissing:      4,
		OnStale:        4,
		OnUnclear:      4,
		OnUnverifiable: 4,
		OnConflict:     4,
	}
}

// TestValidateGraphReachabilityNoRequirements proves a document with no
// requirement rows leaves every safe node an orphan: one valid Exists leaf
// without any requirement or clause roots emits exactly one
// CodeUnreachableNode. No-requirements is not special-cased as clean.
func TestValidateGraphReachabilityNoRequirements(t *testing.T) {
	ab, sf, _, span := newSemDoc(t)
	if _, err := ab.AddExists(sf.symbol, span); err != nil {
		t.Fatal(err)
	}
	var v Validator
	want(t, v.Validate(nil, ab.Document(), sf.schema), []Diagnostic{
		{Code: CodeUnreachableNode, Table: TableNode, Row: 1, Node: 1, Span: span},
	})
}

// TestValidateGraphReachabilityCanonicalClean proves the graph phase adds no
// diagnostics to the canonical document: every node is reachable from the
// requirement applicability root, the clause assertion root, and the clause
// evidence edge.
func TestValidateGraphReachabilityCanonicalClean(t *testing.T) {
	doc, fields := buildMinimal(t)
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateGraphReachabilityOrphanCompareLeaf appends one orphan Exists
// leaf and expects exactly one CodeUnreachableNode on its node row.
func TestValidateGraphReachabilityOrphanCompareLeaf(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	id := appendReachTestCompare(doc, schema.FieldID(1), span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(id), Node: id, Span: span},
	})
}

func TestValidateGraphReachabilityOrphanInvalidSpan(t *testing.T) {
	doc, fields := buildMinimal(t)
	id := appendReachTestCompare(doc, schema.FieldID(1), ast.SourceSpan{Start: 5, End: 2})
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidSourceSpan, Table: TableNode, Row: uint32(id), Node: id},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(id), Node: id},
	})
}

// TestValidateGraphReachabilityOrphanSubtree appends an orphan subtree
// Compare leaf -> Not -> nonempty Any group and expects all three nodes
// unreachable in ascending NodeID order.
func TestValidateGraphReachabilityOrphanSubtree(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	c := appendReachTestCompare(doc, schema.FieldID(1), span)
	n := appendReachTestNot(doc, c, span)
	g := appendReachTestGroup(doc, ast.NodeKindAny, []schema.NodeID{n}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(c), Node: c, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(n), Node: n, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(g), Node: g, Span: span},
	})
}

// TestValidateGraphReachabilityNewApplicabilityClean appends a subtree
// reachable only through a new requirement's applicability root while the old
// requirement keeps its own root, and expects zero diagnostics.
func TestValidateGraphReachabilityNewApplicabilityClean(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	c := appendReachTestCompare(doc, schema.FieldID(1), span)
	n := appendReachTestNot(doc, c, span)
	g := appendReachTestGroup(doc, ast.NodeKindAny, []schema.NodeID{n}, span)
	appendReachTestRequirement(doc, schema.RequirementID(2), g, []schema.ClauseID{1}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateGraphReachabilitySecondClauseClean adds an assertion leaf and an
// evidence node reachable only through a second clause appended to requirement
// one's valid nonempty clause CSR, and expects zero diagnostics with all old
// roots retained.
func TestValidateGraphReachabilitySecondClauseClean(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	a := appendReachTestCompare(doc, schema.FieldID(1), span)
	e := appendReachTestEvidence(doc, schema.EvidenceKindID(1), schema.EvidenceStateID(1), span)
	c2 := appendReachTestClause(doc, a, []schema.NodeID{e}, appendReachTestResolution(), span)
	doc.RequirementClauseIDs = append(doc.RequirementClauseIDs, c2)
	doc.RequirementClauseCounts[0] = uint16(len(doc.RequirementClauseIDs))
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
}

// TestValidateGraphReachabilityUnreferencedClause appends a valid second clause
// that no requirement references and expects its assertion and evidence nodes
// unreachable in ascending NodeID order: clauses are never seeded globally.
func TestValidateGraphReachabilityUnreferencedClause(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	a := appendReachTestCompare(doc, schema.FieldID(1), span)
	e := appendReachTestEvidence(doc, schema.EvidenceKindID(1), schema.EvidenceStateID(1), span)
	appendReachTestClause(doc, a, []schema.NodeID{e}, appendReachTestResolution(), span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(a), Node: a, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(e), Node: e, Span: span},
	})
}

// TestValidateGraphReachabilityRequirementClauseZeroSibling appends a zero
// clause edge next to a valid one in requirement one's CSR and expects only the
// structural MemberClause diagnostic: the valid sibling still marks its clause
// and no graph diagnostic duplicates the invalid edge.
func TestValidateGraphReachabilityRequirementClauseZeroSibling(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	doc.RequirementClauseIDs = append(doc.RequirementClauseIDs, 0)
	doc.RequirementClauseCounts[0] = 2
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidPayloadRef, Table: TableRequirement, Member: MemberClause, Row: 1, Requirement: 1, Span: span},
	})
}

// TestValidateGraphReachabilityClauseEvidenceZeroSibling appends a zero
// evidence edge next to a valid evidence node in a referenced clause's CSR and
// expects only the structural MemberEvidence diagnostic: the valid sibling
// still roots its evidence node and no graph diagnostic duplicates the edge.
func TestValidateGraphReachabilityClauseEvidenceZeroSibling(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	e := appendReachTestEvidence(doc, schema.EvidenceKindID(1), schema.EvidenceStateID(1), span)
	c2 := appendReachTestClause(doc, schema.NodeID(4), []schema.NodeID{e, 0}, appendReachTestResolution(), span)
	doc.RequirementClauseIDs = append(doc.RequirementClauseIDs, c2)
	doc.RequirementClauseCounts[0] = uint16(len(doc.RequirementClauseIDs))
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberEvidence, Row: 2, Clause: 2, Span: span},
	})
}

// TestValidateGraphReachabilityGroupInvalidChildEdges appends zero and high
// child edges to a reachable group's CSR and expects only the two structural
// group edge diagnostics: the invalid edges are skipped, the valid child stays
// reachable, and no graph diagnostic duplicates the edges.
func TestValidateGraphReachabilityGroupInvalidChildEdges(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	c := appendReachTestCompare(doc, schema.FieldID(1), span)
	g := appendReachTestGroup(doc, ast.NodeKindAny, []schema.NodeID{c}, span)
	doc.ChildNodeIDs = append(doc.ChildNodeIDs, 0, schema.NodeID(99))
	doc.GroupChildCounts[len(doc.GroupChildStarts)-1] = 3
	appendReachTestRequirement(doc, schema.RequirementID(2), g, []schema.ClauseID{1}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 3, Node: 0},
		{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 3, Node: 99},
	})
}

// TestValidateGraphReachabilityUnsafeGroupChild points a reachable group at an
// in-range node made structurally unsafe by an invalid payload reference and
// expects only the structural node diagnostic: the graph skips the unsafe
// target and never appends an unreachable diagnostic for it.
func TestValidateGraphReachabilityUnsafeGroupChild(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	u := appendReachTestNode(doc, ast.NodeKindCompare, 99, span)
	g := appendReachTestGroup(doc, ast.NodeKindAny, []schema.NodeID{u}, span)
	appendReachTestRequirement(doc, schema.RequirementID(2), g, []schema.ClauseID{1}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidPayloadRef, Table: TableNode, Row: uint32(u), Node: u, Span: span},
	})
}

func TestValidateGraphReachabilityOrphanUnsafeNode(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	id := appendReachTestNode(doc, ast.NodeKindCompare, 99, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidPayloadRef, Table: TableNode, Row: uint32(id), Node: id, Span: span},
	})
}

// TestValidateGraphReachabilityOrphanOrderAscending appends several independent
// orphan leaves whose source spans are arranged out of NodeID order and expects
// the unreachable diagnostics strictly ascending by NodeID, not by span or
// payload-row order, with a seeded destination prefix preserved.
func TestValidateGraphReachabilityOrphanOrderAscending(t *testing.T) {
	doc, fields := buildMinimal(t)
	spanA := ast.SourceSpan{Start: 30, End: 31}
	spanB := ast.SourceSpan{Start: 10, End: 11}
	spanC := ast.SourceSpan{Start: 20, End: 21}
	a := appendReachTestCompare(doc, schema.FieldID(1), spanA)
	b := appendReachTestCompare(doc, schema.FieldID(1), spanB)
	c := appendReachTestCompare(doc, schema.FieldID(1), spanC)
	seed := Diagnostic{Code: CodeInvalidField, Field: schema.FieldID(7)}
	var v Validator
	want(t, v.Validate([]Diagnostic{seed}, doc, fields), []Diagnostic{
		seed,
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(a), Node: a, Span: spanA},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(b), Node: b, Span: spanB},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(c), Node: c, Span: spanC},
	})
}

func TestValidateGraphReachabilitySemanticBeforeUnreachable(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	id := appendReachTestCompare(doc, schema.FieldID(1), span)
	doc.RequirementIDs[0] = 0
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: 1, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(id), Node: id, Span: span},
	})
}

func TestValidateGraphReachabilityInvalidApplicabilityStillMarksClause(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	doc.RequirementApplicabilityRoots[0] = 0
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableRequirement, Member: MemberApplicability, Row: 1, Requirement: 1, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 3, Node: 3, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 5, Node: 5, Span: span},
	})
}

func TestValidateGraphReachabilityReferencedUnsafeClauseSkipped(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	doc.ClauseAssertionRoots[0] = 0
	var v Validator
	want(t, v.Validate(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberAssertion, Row: 1, Clause: 1, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 2, Node: 2, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 4, Node: 4, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: 6, Node: 6, Span: span},
	})
}

func buildReachTestClauseReuseDocument(t *testing.T, referenced bool) (*ast.Document, *schema.Schema, schema.NodeID, schema.NodeID) {
	t.Helper()
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	assertion := appendReachTestCompare(doc, schema.FieldID(1), span)
	evidence := appendReachTestEvidence(doc, schema.EvidenceKindID(1), schema.EvidenceStateID(1), span)
	clause := appendReachTestClause(doc, assertion, []schema.NodeID{evidence}, appendReachTestResolution(), span)
	if referenced {
		doc.RequirementClauseIDs = append(doc.RequirementClauseIDs, clause)
		doc.RequirementClauseCounts[0] = 2
	}
	return doc, fields, assertion, evidence
}

func TestValidateGraphReachabilityValidatorReuseClearsState(t *testing.T) {
	referenced, referencedFields, _, _ := buildReachTestClauseReuseDocument(t, true)
	orphan, orphanFields, assertion, evidence := buildReachTestClauseReuseDocument(t, false)
	span := ast.SourceSpan{Start: 0, End: 1}
	var v Validator
	want(t, v.Validate(nil, referenced, referencedFields), nil)
	want(t, v.Validate(nil, orphan, orphanFields), []Diagnostic{
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(assertion), Node: assertion, Span: span},
		{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(evidence), Node: evidence, Span: span},
	})
	want(t, v.Validate(nil, referenced, referencedFields), nil)
	if len(v.stack) != 0 {
		t.Fatalf("graph traversal stack retained %d frames", len(v.stack))
	}
}

func TestValidateGraphReachabilityDeepIterative(t *testing.T) {
	doc, fields := buildMinimal(t)
	span := ast.SourceSpan{Start: 0, End: 1}
	const depth = 8192
	root := schema.NodeID(5)
	for range depth {
		root = appendReachTestNot(doc, root, span)
	}
	appendReachTestRequirement(doc, schema.RequirementID(2), root, []schema.ClauseID{1}, span)
	var v Validator
	want(t, v.Validate(nil, doc, fields), nil)
	if len(v.stack) != 0 {
		t.Fatalf("graph traversal stack retained %d frames", len(v.stack))
	}
	if cap(v.stack) < depth {
		t.Fatalf("graph traversal stack capacity = %d, want at least %d", cap(v.stack), depth)
	}
}
