package ast

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestCompareAndExistsNodes(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 2, CompareNodes: 2, SourceBytes: 12})
	source := []byte("subject.role")
	if err := b.SetSource(source); err != nil {
		t.Fatal(err)
	}
	equal, err := b.AddCompare(1, CompareOpEqual, 2, SourceSpan{Start: 0, End: 7})
	if err != nil {
		t.Fatal(err)
	}
	exists, err := b.AddExists(3, SourceSpan{Start: 8, End: 12})
	if err != nil {
		t.Fatal(err)
	}
	if equal != 1 || exists != 2 {
		t.Fatalf("NodeIDs = (%d, %d), want (1, 2)", equal, exists)
	}

	d := b.Document()
	field, op, value, ok := d.Compare(equal)
	if !ok || field != 1 || op != CompareOpEqual || value != 2 {
		t.Fatalf("Compare(equal) = (%d, %d, %d, %v)", field, op, value, ok)
	}
	field, op, value, ok = d.Compare(exists)
	if !ok || field != 3 || op != CompareOpExists || value != 0 {
		t.Fatalf("Compare(exists) = (%d, %d, %d, %v)", field, op, value, ok)
	}
	if ref, ok := d.NodeRef(equal); !ok || ref != 0 {
		t.Fatalf("NodeRef(equal) = (%d, %v), want (0, true)", ref, ok)
	}
	if ref, ok := d.NodeRef(exists); !ok || ref != 1 {
		t.Fatalf("NodeRef(exists) = (%d, %v), want (1, true)", ref, ok)
	}
	if got, ok := d.Source(equal); !ok || string(got) != "subject" {
		t.Fatalf("Source(equal) = (%q, %v)", got, ok)
	}
	if got, ok := d.Source(exists); !ok || string(got) != "role" {
		t.Fatalf("Source(exists) = (%q, %v)", got, ok)
	}
	source[0] = 'X'
	if got, _ := d.Source(equal); string(got) != "subject" {
		t.Fatalf("document source aliases caller buffer: %q", got)
	}
}

func TestNodeKindsAndCompareOpsAreBounded(t *testing.T) {
	for _, kind := range []NodeKind{NodeKindCompare, NodeKindAll, NodeKindAny, NodeKindNot, NodeKindEvidence} {
		if !kind.Valid() {
			t.Errorf("NodeKind(%d) must be valid", kind)
		}
	}
	if NodeKindInvalid.Valid() || NodeKind(6).Valid() || NodeKind(255).Valid() {
		t.Fatal("invalid or out-of-range NodeKind reported valid")
	}
	for _, op := range []CompareOp{
		CompareOpEqual, CompareOpNotEqual, CompareOpIn, CompareOpExists,
		CompareOpLess, CompareOpLessEqual, CompareOpGreater, CompareOpGreaterEqual,
	} {
		if !op.Valid() {
			t.Errorf("CompareOp(%d) must be valid", op)
		}
	}
	if CompareOpInvalid.Valid() || CompareOp(9).Valid() || CompareOp(255).Valid() {
		t.Fatal("invalid or out-of-range CompareOp reported valid")
	}
}

func TestSetSourceRejectsReplacementAfterNodes(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, CompareNodes: 1, SourceBytes: 5})
	if err := b.SetSource([]byte("first")); err != nil {
		t.Fatal(err)
	}
	id, err := b.AddCompare(1, CompareOpEqual, 1, SourceSpan{End: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetSource([]byte("x")); !errors.Is(err, ErrSourceAfterRecords) {
		t.Fatalf("SetSource after nodes err = %v, want ErrSourceAfterRecords", err)
	}
	if got, ok := b.Document().Source(id); !ok || string(got) != "first" {
		t.Fatalf("source changed after rejected replacement: (%q, %v)", got, ok)
	}
}

func TestSetSourceRejectsReplacementAfterSemanticRows(t *testing.T) {
	b := NewBuilder(Hints{Values: 1, SymbolValues: 1, SymbolBytes: 7, Outcomes: 1})
	name, err := b.AddSymbolValue([]byte("Approve"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddOutcome(name, 1, true, SourceSpan{}); err != nil {
		t.Fatal(err)
	}
	if err := b.SetSource([]byte("new")); !errors.Is(err, ErrSourceAfterRecords) {
		t.Fatalf("SetSource after outcome err = %v, want ErrSourceAfterRecords", err)
	}
}

func TestGroupCSRAndNegation(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 5, CompareNodes: 2, GroupNodes: 2, ChildEdges: 4, NotNodes: 1})
	left, err := b.AddCompare(1, CompareOpEqual, 1, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	right, err := b.AddCompare(2, CompareOpNotEqual, 2, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	all, err := b.AddGroup(NodeKindAll, []schema.NodeID{left, right}, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	not, err := b.AddNot(right, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	any, err := b.AddGroup(NodeKindAny, []schema.NodeID{all, not}, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}

	d := b.Document()
	if start, count, ok := d.GroupRange(all); !ok || start != 0 || count != 2 {
		t.Fatalf("GroupRange(all) = (%d, %d, %v), want (0, 2, true)", start, count, ok)
	}
	if start, count, ok := d.GroupRange(any); !ok || start != 2 || count != 2 {
		t.Fatalf("GroupRange(any) = (%d, %d, %v), want (2, 2, true)", start, count, ok)
	}
	wantEdges := []schema.NodeID{left, right, all, not}
	if !reflect.DeepEqual(d.ChildNodeIDs, wantEdges) {
		t.Fatalf("ChildNodeIDs = %v, want %v", d.ChildNodeIDs, wantEdges)
	}
	if child, ok := d.NotChild(not); !ok || child != right {
		t.Fatalf("NotChild = (%d, %v), want (%d, true)", child, ok, right)
	}
	if children, ok := d.GroupChildren(any); !ok || !reflect.DeepEqual(children, wantEdges[2:]) {
		t.Fatalf("GroupChildren(any) = (%v, %v)", children, ok)
	}
}

func TestGroupCopiesCallerChildren(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, GroupNodes: 1, ChildEdges: 2})
	children := []schema.NodeID{7, 8}
	group, err := b.AddGroup(NodeKindAll, children, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	children[0] = 99
	got, ok := b.Document().GroupChildren(group)
	if !ok || !reflect.DeepEqual(got, []schema.NodeID{7, 8}) {
		t.Fatalf("stored children alias caller: (%v, %v)", got, ok)
	}
}

func TestEmptyGroupRetainsZeroLengthCSRRange(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, GroupNodes: 1})
	group, err := b.AddGroup(NodeKindAll, nil, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	start, count, ok := b.Document().GroupRange(group)
	if !ok || start != 0 || count != 0 {
		t.Fatalf("GroupRange(empty) = (%d, %d, %v), want (0, 0, true)", start, count, ok)
	}
}

func TestEvidenceAndRequirementRoots(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, EvidenceNodes: 1, Requirements: 1})
	evidence, err := b.AddEvidence(2, 3, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AddRequirement(1, evidence, nil, SourceSpan{}); err != nil {
		t.Fatal(err)
	}
	kind, state, ok := b.Document().Evidence(evidence)
	if !ok || kind != 2 || state != 3 {
		t.Fatalf("Evidence = (%d, %d, %v), want (2, 3, true)", kind, state, ok)
	}
	if root, ok := b.Document().RequirementRoot(1); !ok || root != evidence {
		t.Fatalf("RequirementRoot = (%d, %v), want (%d, true)", root, ok, evidence)
	}
}

func TestRejectedAddsDoNotMutateDocument(t *testing.T) {
	b := NewBuilder(Hints{})
	if err := b.SetSource([]byte("x")); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		err  error
	}{
		{"zero field", addCompareError(b, 0, CompareOpEqual, 1, SourceSpan{})},
		{"invalid op", addCompareError(b, 1, CompareOpInvalid, 1, SourceSpan{})},
		{"missing value", addCompareError(b, 1, CompareOpEqual, 0, SourceSpan{})},
		{"exists value", addCompareError(b, 1, CompareOpExists, 1, SourceSpan{})},
		{"bad span", addCompareError(b, 1, CompareOpEqual, 1, SourceSpan{End: 2})},
		{"bad group kind", addGroupError(b, NodeKindCompare, []schema.NodeID{1})},
		{"zero child", addGroupError(b, NodeKindAll, []schema.NodeID{1, 0})},
		{"zero not child", addNotError(b, 0)},
		{"zero evidence kind", addEvidenceError(b, 0, 1)},
		{"zero evidence state", addEvidenceError(b, 1, 0)},
	}
	for _, tt := range tests {
		if tt.err == nil {
			t.Errorf("%s: expected error", tt.name)
		}
	}
	if err := b.AddRequirement(0, 1, nil, SourceSpan{}); !errors.Is(err, ErrInvalidRequirement) {
		t.Fatalf("zero requirement err = %v", err)
	}
	if err := b.AddRequirement(1, 0, nil, SourceSpan{}); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("zero root err = %v", err)
	}
	d := b.Document()
	if d.Len() != 0 || len(d.CompareFields) != 0 || len(d.GroupChildStarts) != 0 || len(d.ChildNodeIDs) != 0 || len(d.NotChildren) != 0 || len(d.EvidenceKinds) != 0 || len(d.RequirementIDs) != 0 {
		t.Fatalf("rejected add mutated document: %+v", d)
	}
}

func addCompareError(b *Builder, field schema.FieldID, op CompareOp, value schema.ValueID, span SourceSpan) error {
	_, err := b.AddCompare(field, op, value, span)
	return err
}

func addGroupError(b *Builder, kind NodeKind, children []schema.NodeID) error {
	_, err := b.AddGroup(kind, children, SourceSpan{})
	return err
}

func addNotError(b *Builder, child schema.NodeID) error {
	_, err := b.AddNot(child, SourceSpan{})
	return err
}

func addEvidenceError(b *Builder, kind schema.EvidenceKindID, state schema.EvidenceStateID) error {
	_, err := b.AddEvidence(kind, state, SourceSpan{})
	return err
}

func TestDocumentRepresentationHasNoPerNodePointersOrSlices(t *testing.T) {
	typ := reflect.TypeOf(Document{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "Metadata" {
			if field.Type.Kind() != reflect.Struct {
				t.Fatalf("Document.Metadata kind = %v, want struct", field.Type.Kind())
			}
			continue
		}
		if field.Type.Kind() != reflect.Slice {
			t.Fatalf("Document.%s kind = %v, want flat slice column", field.Name, field.Type.Kind())
		}
		elem := field.Type.Elem().Kind()
		if elem == reflect.Slice || elem == reflect.Pointer || elem == reflect.Map || elem == reflect.Interface || elem == reflect.Func {
			t.Fatalf("Document.%s has indirect per-row element kind %v", field.Name, elem)
		}
	}
	childColumn, ok := typ.FieldByName("ChildNodeIDs")
	if !ok || childColumn.Type.Elem() != reflect.TypeOf(schema.NodeID(0)) {
		t.Fatalf("ChildNodeIDs must be one flat []schema.NodeID column")
	}
	spanType := reflect.TypeOf(SourceSpan{})
	for i := 0; i < spanType.NumField(); i++ {
		if spanType.Field(i).Type.Kind() != reflect.Uint32 {
			t.Fatalf("SourceSpan.%s is not uint32", spanType.Field(i).Name)
		}
	}
	metadataType := reflect.TypeOf(PolicyMetadata{})
	for i := 0; i < metadataType.NumField(); i++ {
		kind := metadataType.Field(i).Type.Kind()
		if kind == reflect.Slice || kind == reflect.Pointer || kind == reflect.Map || kind == reflect.Interface || kind == reflect.Func {
			t.Fatalf("PolicyMetadata.%s has indirect kind %v", metadataType.Field(i).Name, kind)
		}
	}
}

func TestAccessorsRejectInvalidIDsAndKinds(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, CompareNodes: 1})
	id, err := b.AddCompare(1, CompareOpEqual, 1, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	d := b.Document()
	if _, ok := d.Kind(0); ok {
		t.Fatal("Kind(0) must fail")
	}
	if _, ok := d.Kind(2); ok {
		t.Fatal("Kind(out of range) must fail")
	}
	if _, _, ok := d.GroupRange(id); ok {
		t.Fatal("GroupRange(compare) must fail")
	}
	if _, ok := d.NotChild(id); ok {
		t.Fatal("NotChild(compare) must fail")
	}
	if _, _, ok := d.Evidence(id); ok {
		t.Fatal("Evidence(compare) must fail")
	}
}

func TestResetRetainsCapacityAndRestartsNodeIDs(t *testing.T) {
	hints := Hints{
		Nodes: 4, CompareNodes: 2, GroupNodes: 1, ChildEdges: 2, NotNodes: 1,
		Values: 2, SymbolValues: 1, SymbolBytes: 7, IntegerValues: 1,
		EvidenceKinds: 1, EvidenceStates: 1, Outcomes: 1, Remediations: 1,
		Clauses: 1, ClauseRemediationEdges: 1,
		Requirements: 1, RequirementClauseEdges: 1, SourceBytes: 8,
	}
	b := NewBuilder(hints)
	source := []byte("abcdefgh")
	outcomeName := []byte("Approve")
	children := make([]schema.NodeID, 2)
	remediations := make([]schema.RemediationID, 1)
	clauses := make([]schema.ClauseID, 1)
	build := func() error {
		if err := b.SetSource(source); err != nil {
			return err
		}
		name, err := b.AddSymbolValue(outcomeName)
		if err != nil {
			return err
		}
		value, err := b.AddIntegerValue(1)
		if err != nil {
			return err
		}
		if err := b.SetMetadata(name, name); err != nil {
			return err
		}
		if _, err := b.AddEvidenceKind(name, SourceSpan{End: 1}); err != nil {
			return err
		}
		if _, err := b.AddEvidenceState(name, SourceSpan{End: 1}); err != nil {
			return err
		}
		outcome, err := b.AddOutcome(name, 1, true, SourceSpan{End: 1})
		if err != nil {
			return err
		}
		remediation, err := b.AddSetFieldRemediation(1, value, SourceSpan{End: 1})
		if err != nil {
			return err
		}
		first, err := b.AddCompare(1, CompareOpEqual, value, SourceSpan{End: 1})
		if err != nil {
			return err
		}
		second, err := b.AddCompare(2, CompareOpEqual, value, SourceSpan{Start: 1, End: 2})
		if err != nil {
			return err
		}
		children[0], children[1] = first, second
		group, err := b.AddGroup(NodeKindAll, children, SourceSpan{End: 2})
		if err != nil {
			return err
		}
		if _, err := b.AddNot(group, SourceSpan{End: 2}); err != nil {
			return err
		}
		remediations[0] = remediation
		resolution := Resolution{
			OnSatisfied: outcome,
			OnFalse:     outcome, OnMissing: outcome, OnStale: outcome,
			OnUnclear: outcome, OnUnverifiable: outcome, OnConflict: outcome,
		}
		clause, err := b.AddClause(group, nil, resolution, remediations, SourceSpan{End: 2})
		if err != nil {
			return err
		}
		clauses[0] = clause
		return b.AddRequirement(1, group, clauses, SourceSpan{End: 2})
	}
	if err := build(); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	if b.Len() != 0 || len(b.Document().InputBytes) != 0 {
		t.Fatalf("Reset left nodes or source: nodes=%d source=%d", b.Len(), len(b.Document().InputBytes))
	}
	if err := b.SetSource(source); err != nil {
		t.Fatal(err)
	}
	restarted, err := b.AddCompare(1, CompareOpEqual, 1, SourceSpan{End: 1})
	if err != nil {
		t.Fatal(err)
	}
	if restarted != 1 {
		t.Fatalf("first NodeID after Reset = %d, want 1", restarted)
	}
	b.Reset()

	var buildErr error
	allocs := testing.AllocsPerRun(100, func() {
		b.Reset()
		if err := build(); err != nil {
			buildErr = err
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if allocs != 0 {
		t.Fatalf("same-shape build after Reset allocates %.2f times, want 0", allocs)
	}
	if b.Len() != 4 {
		t.Fatalf("Len after reusable build = %d, want 4", b.Len())
	}
}
