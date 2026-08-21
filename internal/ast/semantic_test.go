package ast

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestOutcomesClausesRequirementsAndRemediation(t *testing.T) {
	b := NewBuilder(Hints{
		Nodes:                  3,
		CompareNodes:           2,
		EvidenceNodes:          1,
		Values:                 5,
		SymbolValues:           5,
		SymbolBytes:            40,
		Outcomes:               2,
		Remediations:           2,
		Clauses:                1,
		ClauseEvidenceEdges:    1,
		ClauseRemediationEdges: 2,
		Requirements:           1,
		RequirementClauseEdges: 1,
		SourceBytes:            18,
	})
	if err := b.SetSource([]byte("out rem clause req")); err != nil {
		t.Fatal(err)
	}
	approveName := mustSymbolValue(t, b, "Approve")
	reviseName := mustSymbolValue(t, b, "Revise")
	standard := mustSymbolValue(t, b, "standard")
	fieldValue := mustSymbolValue(t, b, "aggregate")
	applicableValue := mustSymbolValue(t, b, "trusted")
	approve, err := b.AddOutcome(approveName, 1, true, SourceSpan{Start: 0, End: 3})
	if err != nil {
		t.Fatal(err)
	}
	revise, err := b.AddOutcome(reviseName, 2, false, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	setUsage, err := b.AddSetFieldRemediation(4, standard, SourceSpan{Start: 4, End: 7})
	if err != nil {
		t.Fatal(err)
	}
	addApproval, err := b.AddEvidenceRemediation(7, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	applicability, err := b.AddCompare(1, CompareOpEqual, applicableValue, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := b.AddCompare(2, CompareOpEqual, fieldValue, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := b.AddEvidence(7, 2, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	resolution := Resolution{
		OnSatisfied:    approve,
		OnFalse:        revise,
		OnMissing:      revise,
		OnStale:        approve,
		OnUnclear:      approve,
		OnUnverifiable: approve,
		OnConflict:     approve,
	}
	clause, err := b.AddClause(
		assertion,
		[]schema.NodeID{evidence},
		resolution,
		[]schema.RemediationID{setUsage, addApproval},
		SourceSpan{Start: 8, End: 14},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.AddRequirement(1, applicability, []schema.ClauseID{clause}, SourceSpan{Start: 15, End: 18}); err != nil {
		t.Fatal(err)
	}

	d := b.Document()
	name, precedence, terminal, ok := d.Outcome(revise)
	if !ok || name != reviseName || precedence != 2 || terminal {
		t.Fatalf("Outcome(revise) = (%d, %d, %v, %v)", name, precedence, terminal, ok)
	}
	kind, field, value, evidenceKind, ok := d.Remediation(setUsage)
	if !ok || kind != RemediationKindSetField || field != 4 || value != standard || evidenceKind != 0 {
		t.Fatalf("set-field remediation = (%d, %d, %d, %d, %v)", kind, field, value, evidenceKind, ok)
	}
	kind, field, value, evidenceKind, ok = d.Remediation(addApproval)
	if !ok || kind != RemediationKindAddEvidence || field != 0 || value != 0 || evidenceKind != 7 {
		t.Fatalf("add-evidence remediation = (%d, %d, %d, %d, %v)", kind, field, value, evidenceKind, ok)
	}
	gotAssertion, gotResolution, ok := d.Clause(clause)
	if !ok || gotAssertion != assertion || gotResolution != resolution {
		t.Fatalf("Clause = (%d, %+v, %v), want (%d, %+v, true)", gotAssertion, gotResolution, ok, assertion, resolution)
	}
	if got, ok := d.ClauseEvidence(clause); !ok || !reflect.DeepEqual(got, []schema.NodeID{evidence}) {
		t.Fatalf("ClauseEvidence = (%v, %v)", got, ok)
	}
	if got, ok := d.ClauseRemediations(clause); !ok || !reflect.DeepEqual(got, []schema.RemediationID{setUsage, addApproval}) {
		t.Fatalf("ClauseRemediations = (%v, %v)", got, ok)
	}
	if root, ok := d.RequirementRoot(1); !ok || root != applicability {
		t.Fatalf("RequirementRoot = (%d, %v), want (%d, true)", root, ok, applicability)
	}
	if got, ok := d.RequirementClauses(1); !ok || !reflect.DeepEqual(got, []schema.ClauseID{clause}) {
		t.Fatalf("RequirementClauses = (%v, %v)", got, ok)
	}
	if span, ok := d.OutcomeSpan(approve); !ok || span != (SourceSpan{Start: 0, End: 3}) {
		t.Fatalf("OutcomeSpan = (%+v, %v)", span, ok)
	}
	if span, ok := d.RemediationSpan(setUsage); !ok || span != (SourceSpan{Start: 4, End: 7}) {
		t.Fatalf("RemediationSpan = (%+v, %v)", span, ok)
	}
	if span, ok := d.ClauseSpan(clause); !ok || span != (SourceSpan{Start: 8, End: 14}) {
		t.Fatalf("ClauseSpan = (%+v, %v)", span, ok)
	}
	if span, ok := d.RequirementSpan(1); !ok || span != (SourceSpan{Start: 15, End: 18}) {
		t.Fatalf("RequirementSpan = (%+v, %v)", span, ok)
	}
}

func TestPolicyMetadataAndEvidenceCatalogs(t *testing.T) {
	source := []byte("policy metadata")
	b := NewBuilder(Hints{Values: 4, SymbolValues: 4, SymbolBytes: 32, EvidenceKinds: 1, EvidenceStates: 1, SourceBytes: len(source)})
	if err := b.SetSource(source); err != nil {
		t.Fatal(err)
	}
	pack := mustSymbolValue(t, b, "verifoxx")
	version := mustSymbolValue(t, b, "1.0.0")
	kindName := mustSymbolValue(t, b, "approval_record")
	stateName := mustSymbolValue(t, b, "current")
	if err := b.SetMetadata(pack, version); err != nil {
		t.Fatal(err)
	}
	kind, err := b.AddEvidenceKind(kindName, SourceSpan{Start: 0, End: 6})
	if err != nil {
		t.Fatal(err)
	}
	state, err := b.AddEvidenceState(stateName, SourceSpan{Start: 7, End: 15})
	if err != nil {
		t.Fatal(err)
	}
	metadata, ok := b.Document().PolicyMetadata()
	if !ok || metadata.Name != pack || metadata.Version != version || metadata.ContentHash != sha256.Sum256(source) {
		t.Fatalf("PolicyMetadata = (%+v, %v)", metadata, ok)
	}
	if got, ok := b.Document().EvidenceKindName(kind); !ok || got != kindName {
		t.Fatalf("EvidenceKindName = (%d, %v), want (%d, true)", got, ok, kindName)
	}
	if got, ok := b.Document().EvidenceStateName(state); !ok || got != stateName {
		t.Fatalf("EvidenceStateName = (%d, %v), want (%d, true)", got, ok, stateName)
	}
	if _, ok := b.Document().EvidenceKindName(0); ok {
		t.Fatal("EvidenceKindName(0) must fail")
	}
}

func TestMetadataRejectsNonSymbolAndDuplicate(t *testing.T) {
	b := NewBuilder(Hints{Values: 3, SymbolValues: 2, SymbolBytes: 8, IntegerValues: 1})
	pack := mustSymbolValue(t, b, "pack")
	version := mustSymbolValue(t, b, "v1")
	integer, err := b.AddIntegerValue(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetMetadata(integer, version); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("non-symbol metadata err = %v, want ErrInvalidValue", err)
	}
	if err := b.SetMetadata(pack, version); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Document().PolicyMetadata(); ok {
		t.Fatal("metadata without a bound source must be incomplete")
	}
	if err := b.SetMetadata(pack, version); !errors.Is(err, ErrMetadataAlreadySet) {
		t.Fatalf("duplicate metadata err = %v, want ErrMetadataAlreadySet", err)
	}
}

func mustSymbolValue(t *testing.T, b *Builder, value string) schema.ValueID {
	t.Helper()
	id, err := b.AddSymbolValue([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSemanticRelationshipsCopyCallerSlices(t *testing.T) {
	b := NewBuilder(Hints{Clauses: 1, ClauseEvidenceEdges: 1, ClauseRemediationEdges: 1, Requirements: 1, RequirementClauseEdges: 1})
	evidence := []schema.NodeID{2}
	remediations := []schema.RemediationID{3}
	clause, err := b.AddClause(1, evidence, Resolution{}, remediations, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	clauses := []schema.ClauseID{clause}
	if err := b.AddRequirement(1, 1, clauses, SourceSpan{}); err != nil {
		t.Fatal(err)
	}
	evidence[0], remediations[0], clauses[0] = 9, 9, 9
	if got, _ := b.Document().ClauseEvidence(clause); !reflect.DeepEqual(got, []schema.NodeID{2}) {
		t.Fatalf("clause evidence aliases caller: %v", got)
	}
	if got, _ := b.Document().ClauseRemediations(clause); !reflect.DeepEqual(got, []schema.RemediationID{3}) {
		t.Fatalf("clause remediations alias caller: %v", got)
	}
	if got, _ := b.Document().RequirementClauses(1); !reflect.DeepEqual(got, []schema.ClauseID{clause}) {
		t.Fatalf("requirement clauses alias caller: %v", got)
	}
}

func TestRemediationKindsAreBounded(t *testing.T) {
	if !RemediationKindSetField.Valid() || !RemediationKindAddEvidence.Valid() {
		t.Fatal("supported remediation kind reported invalid")
	}
	if RemediationKindInvalid.Valid() || RemediationKind(3).Valid() || RemediationKind(255).Valid() {
		t.Fatal("invalid or out-of-range remediation kind reported valid")
	}
}

func TestRejectedSemanticAddsDoNotMutateDocument(t *testing.T) {
	b := NewBuilder(Hints{})
	if err := b.SetSource([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddOutcome(0, 1, true, SourceSpan{}); err == nil {
		t.Fatal("zero outcome name must fail")
	}
	if _, err := b.AddOutcome(1, 1, true, SourceSpan{End: 2}); err == nil {
		t.Fatal("out-of-range outcome span must fail")
	}
	if _, err := b.AddSetFieldRemediation(0, 1, SourceSpan{}); err == nil {
		t.Fatal("zero remediation field must fail")
	}
	if _, err := b.AddSetFieldRemediation(1, 0, SourceSpan{}); err == nil {
		t.Fatal("zero remediation value must fail")
	}
	if _, err := b.AddEvidenceRemediation(0, SourceSpan{}); err == nil {
		t.Fatal("zero remediation evidence kind must fail")
	}
	if _, err := b.AddClause(0, nil, Resolution{}, nil, SourceSpan{}); err == nil {
		t.Fatal("zero clause assertion must fail")
	}
	if _, err := b.AddClause(1, []schema.NodeID{0}, Resolution{}, nil, SourceSpan{}); err == nil {
		t.Fatal("zero clause evidence node must fail")
	}
	if _, err := b.AddClause(1, nil, Resolution{}, []schema.RemediationID{0}, SourceSpan{}); err == nil {
		t.Fatal("zero clause remediation must fail")
	}
	if err := b.AddRequirement(1, 1, []schema.ClauseID{0}, SourceSpan{}); err == nil {
		t.Fatal("zero requirement clause must fail")
	}
	d := b.Document()
	if len(d.OutcomeNames) != 0 || len(d.RemediationKinds) != 0 || len(d.ClauseAssertionRoots) != 0 ||
		len(d.ClauseEvidenceNodeIDs) != 0 || len(d.ClauseRemediationIDs) != 0 || len(d.RequirementIDs) != 0 || len(d.RequirementClauseIDs) != 0 {
		t.Fatalf("rejected semantic add mutated document: %+v", d)
	}
}
