package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestValidateStructuralGroupColumnLengths(t *testing.T) {
	col := func(t *testing.T, mutate func(*ast.Document)) {
		t.Helper()
		doc, fields := buildMinimal(t)
		mutate(doc)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableGroup},
		})
	}
	t.Run("GroupChildStarts", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.GroupChildStarts = append(d.GroupChildStarts, 0) })
	})
	t.Run("GroupChildCounts", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.GroupChildCounts = append(d.GroupChildCounts, 0) })
	})
	t.Run("both peers differ", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.GroupChildStarts = append(doc.GroupChildStarts, 0)
		doc.GroupChildCounts = doc.GroupChildCounts[:1]
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableGroup},
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 5, Node: 5, Span: ast.SourceSpan{Start: 0, End: 1}},
		})
	})
}

func TestValidateStructuralEvidenceColumnLengths(t *testing.T) {
	col := func(t *testing.T, mutate func(*ast.Document)) {
		t.Helper()
		doc, fields := buildMinimal(t)
		mutate(doc)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableEvidenceNode},
		})
	}
	t.Run("EvidenceKinds", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.EvidenceKinds = append(d.EvidenceKinds, 0) })
	})
	t.Run("EvidenceStates", func(t *testing.T) {
		col(t, func(d *ast.Document) { d.EvidenceStates = append(d.EvidenceStates, 0) })
	})
	t.Run("both peers differ", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.EvidenceKinds = append(doc.EvidenceKinds, 0)
		doc.EvidenceStates = doc.EvidenceStates[:0]
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableEvidenceNode},
			{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 6, Node: 6, Span: ast.SourceSpan{Start: 0, End: 1}},
		})
	})
}

func TestValidateStructuralColumnLengthOrder(t *testing.T) {
	doc, fields := fixture(t)
	doc.NodeRefs = append(doc.NodeRefs, 0)
	doc.CompareOps = append(doc.CompareOps, 0)
	doc.GroupChildCounts = append(doc.GroupChildCounts, 0)
	doc.EvidenceStates = append(doc.EvidenceStates, 0)
	doc.SymbolLengths = append(doc.SymbolLengths, 0)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableNode},
		{Code: CodeColumnLength, Table: TableCompare},
		{Code: CodeColumnLength, Table: TableGroup},
		{Code: CodeColumnLength, Table: TableEvidenceNode},
		{Code: CodeColumnLength, Table: TableValue},
	})
}

func TestValidateStructuralGroupCSRRange(t *testing.T) {
	tests := []struct {
		name  string
		start uint32
		count uint16
	}{
		{"start beyond total", 4, 0},
		{"count beyond total", 0, math.MaxUint16},
		{"start near MaxUint32", math.MaxUint32, 0},
		{"start near MaxUint32 with count", math.MaxUint32, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.GroupChildStarts[0] = tc.start
			doc.GroupChildCounts[0] = tc.count
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 1},
			})
		})
	}
}

func TestValidateStructuralGroupChildReferences(t *testing.T) {
	t.Run("zero child", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ChildNodeIDs[0] = 0
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 1, Node: 0},
		})
	})
	t.Run("high child", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ChildNodeIDs[1] = 7
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 1, Node: 7},
		})
	})
	t.Run("both children of one row", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ChildNodeIDs[0] = 0
		doc.ChildNodeIDs[1] = 7
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 1, Node: 0},
			{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 1, Node: 7},
		})
	})
}

func TestValidateStructuralNotChildReferences(t *testing.T) {
	t.Run("zero child", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.NotChildren[0] = 0
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableNot, Row: 1, Node: 0},
		})
	})
	t.Run("high child", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.NotChildren[0] = 7
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableNot, Row: 1, Node: 7},
		})
	})
	t.Run("multiple rows ascending", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.NotChildren[0] = 7
		doc.NotChildren = append(doc.NotChildren, 0)
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidNodeReference, Table: TableNot, Row: 1, Node: 7},
			{Code: CodeInvalidNodeReference, Table: TableNot, Row: 2, Node: 0},
		})
	})
}

func TestValidateStructuralEvidenceRows(t *testing.T) {
	tests := []struct {
		name  string
		kind  schema.EvidenceKindID
		state schema.EvidenceStateID
		want  []Diagnostic
	}{
		{"kind zero", 0, 1, []Diagnostic{{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceKind, Row: 1, EvidenceKind: 0}}},
		{"kind high", 2, 1, []Diagnostic{{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceKind, Row: 1, EvidenceKind: 2}}},
		{"state zero", 1, 0, []Diagnostic{{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceState, Row: 1, EvidenceState: 0}}},
		{"state high", 1, 2, []Diagnostic{{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceState, Row: 1, EvidenceState: 2}}},
		{"both kind then state", 0, 0, []Diagnostic{
			{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceKind, Row: 1, EvidenceKind: 0},
			{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceState, Row: 1, EvidenceState: 0},
		}},
		{"both high kind then high state", 2, 2, []Diagnostic{
			{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceKind, Row: 1, EvidenceKind: 2},
			{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceState, Row: 1, EvidenceState: 2},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.EvidenceKinds[0] = tc.kind
			doc.EvidenceStates[0] = tc.state
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), tc.want)
		})
	}
}

func TestValidateStructuralGroupCSRUnsafeSourceNodes(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.GroupChildStarts[1] = 7
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 2},
	})
	if v.nodeState[4]&nodeStateUnsafe == 0 {
		t.Fatal("node 5 (Any over group row 2) not marked unsafe for invalid CSR")
	}
	for i := 0; i < 6; i++ {
		if i != 4 && v.nodeState[i]&nodeStateUnsafe != 0 {
			t.Fatalf("nodeState[%d] unsafe set on a node over a valid group row", i)
		}
	}
}

func TestValidateStructuralGroupValidSourceNotUnsafe(t *testing.T) {
	doc, fields := buildMinimal(t)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), nil)
	for i := range v.nodeState {
		if v.nodeState[i]&nodeStateUnsafe != 0 {
			t.Fatalf("nodeState[%d] unsafe on a clean document", i)
		}
	}

	doc, fields = buildMinimal(t)
	doc.ChildNodeIDs[0] = 0
	v = Validator{}
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableGroup, Row: 1, Node: 0},
	})
	if v.nodeState[3]&nodeStateUnsafe != 0 {
		t.Fatal("node 4 marked unsafe though its group CSR range is valid")
	}
}

func TestValidateStructuralNotBadTargetSourceNotUnsafe(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NotChildren[0] = 0
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableNot, Row: 1, Node: 0},
	})
	if v.nodeState[2]&nodeStateUnsafe != 0 {
		t.Fatal("node 3 (Not) marked unsafe for a bad target; graph phase skips the edge")
	}
}

func TestValidateStructuralPayloadTruncationNoPanic(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ast.Document)
		want   []Diagnostic
	}{
		{"GroupChildStarts partial", func(d *ast.Document) { d.GroupChildStarts = d.GroupChildStarts[:1] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableGroup},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 5, Node: 5, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"GroupChildCounts partial", func(d *ast.Document) { d.GroupChildCounts = d.GroupChildCounts[:1] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableGroup},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 5, Node: 5, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"both group peers partial", func(d *ast.Document) {
			d.GroupChildStarts = d.GroupChildStarts[:1]
			d.GroupChildCounts = d.GroupChildCounts[:1]
		},
			[]Diagnostic{
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 5, Node: 5, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"EvidenceKinds empty", func(d *ast.Document) { d.EvidenceKinds = d.EvidenceKinds[:0] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableEvidenceNode},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 6, Node: 6, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"EvidenceStates empty", func(d *ast.Document) { d.EvidenceStates = d.EvidenceStates[:0] },
			[]Diagnostic{
				{Code: CodeColumnLength, Table: TableEvidenceNode},
				{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 6, Node: 6, Span: ast.SourceSpan{Start: 0, End: 1}},
			}},
		{"child column empty never inspected", func(d *ast.Document) { d.ChildNodeIDs = d.ChildNodeIDs[:0] },
			[]Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 1},
				{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 2},
			}},
		{"poisoned child behind invalid CSR", func(d *ast.Document) { d.ChildNodeIDs = d.ChildNodeIDs[:1] },
			[]Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 1},
				{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 2},
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

func TestValidateStructuralPayloadMixedOrdering(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.GroupChildCounts = append(doc.GroupChildCounts, 0)
	doc.EvidenceStates = append(doc.EvidenceStates, 0)
	doc.ValueKinds[0] = schema.ValueKindPresence
	doc.GroupChildStarts[0] = 7
	doc.NotChildren[0] = 0
	doc.EvidenceKinds[0] = 0
	doc.EvidenceStates[0] = 0
	doc.NodeRefs[2] = 1
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableGroup},
		{Code: CodeColumnLength, Table: TableEvidenceNode},
		{Code: CodeInvalidValue, Table: TableValue, Row: 1, Value: 1},
		{Code: CodeInvalidCSRRange, Table: TableGroup, Row: 1},
		{Code: CodeInvalidNodeReference, Table: TableNot, Row: 1, Node: 0},
		{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceKind, Row: 1, EvidenceKind: 0},
		{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceState, Row: 1, EvidenceState: 0},
		{Code: CodeInvalidPayloadRef, Table: TableNode, Row: 3, Node: 3, Span: ast.SourceSpan{Start: 0, End: 1}},
	})
}
