package ast

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

func mustTemplate(t *testing.T, b *Builder, text string, context TemplateContext) schema.TemplateID {
	t.Helper()
	id, err := b.AddTemplate([]byte(text), context)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAssumptionsSetExactlyOnceAndCopyCallerIDs(t *testing.T) {
	b := NewBuilder(Hints{Templates: 2, Assumptions: 2})
	first := mustTemplate(t, b, "Policy {policy_name}", TemplateContextAssumption)
	second := mustTemplate(t, b, "Request {request_id}", TemplateContextAssumption)
	ids := []schema.TemplateID{first, second}
	if err := b.SetAssumptions(ids); err != nil {
		t.Fatal(err)
	}
	ids[0] = 99
	got, ok := b.Document().Assumptions()
	if !ok || !reflect.DeepEqual(got, []schema.TemplateID{first, second}) {
		t.Fatalf("Assumptions = (%v, %v)", got, ok)
	}
	if err := b.SetAssumptions(nil); !errors.Is(err, ErrAssumptionsAlreadySet) {
		t.Fatalf("second SetAssumptions error = %v, want ErrAssumptionsAlreadySet", err)
	}
}

func TestAssumptionsAllowExplicitEmptyList(t *testing.T) {
	b := NewBuilder(Hints{})
	if _, ok := b.Document().Assumptions(); ok {
		t.Fatal("unset assumptions reported present")
	}
	if err := b.SetAssumptions(nil); err != nil {
		t.Fatal(err)
	}
	got, ok := b.Document().Assumptions()
	if !ok || len(got) != 0 {
		t.Fatalf("empty Assumptions = (%v, %v)", got, ok)
	}
}

func TestAssumptionsRejectInvalidShapeWithoutMutation(t *testing.T) {
	b := NewBuilder(Hints{})
	assumption := mustTemplate(t, b, "{request_id}", TemplateContextAssumption)
	decision := mustTemplate(t, b, "{outcome}", TemplateContextDecision)
	tests := []struct {
		name string
		ids  []schema.TemplateID
		err  error
	}{
		{"zero", []schema.TemplateID{0}, ErrInvalidTemplate},
		{"out of range", []schema.TemplateID{99}, ErrInvalidTemplate},
		{"wrong context", []schema.TemplateID{decision}, ErrInvalidTemplateContext},
		{"too many", []schema.TemplateID{assumption, assumption, assumption, assumption, assumption, assumption, assumption, assumption, assumption}, ErrTooManyAssumptions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(b.Document().AssumptionTemplateIDs)
			if err := b.SetAssumptions(tt.ids); !errors.Is(err, tt.err) {
				t.Fatalf("SetAssumptions error = %v, want %v", err, tt.err)
			}
			if len(b.Document().AssumptionTemplateIDs) != before || len(b.Document().AssumptionsSet) != 0 {
				t.Fatal("rejected assumptions mutated document")
			}
		})
	}
}

func TestExplanationStoresRationaleAndUncertaintyCSR(t *testing.T) {
	b := NewBuilder(Hints{Templates: 3, Explanations: 1, ExplanationUncertainty: 2})
	rationale := mustTemplate(t, b, "Outcome {outcome}", TemplateContextUnresolved)
	first := mustTemplate(t, b, "Reason {reason}", TemplateContextUnresolved)
	second := mustTemplate(t, b, "Node {node_id}", TemplateContextUnresolved)
	uncertainty := []schema.TemplateID{first, second}
	id, err := b.AddExplanation(rationale, uncertainty)
	if err != nil {
		t.Fatal(err)
	}
	uncertainty[0] = 99
	gotRationale, gotUncertainty, ok := b.Document().Explanation(id)
	if !ok || gotRationale != rationale || !reflect.DeepEqual(gotUncertainty, []schema.TemplateID{first, second}) {
		t.Fatalf("Explanation = (%d, %v, %v)", gotRationale, gotUncertainty, ok)
	}
	if id != 1 || !reflect.DeepEqual(b.Document().ExplanationUncertaintyStarts, []uint32{0}) || !reflect.DeepEqual(b.Document().ExplanationUncertaintyCounts, []uint16{2}) {
		t.Fatalf("explanation columns = id %d starts %v counts %v", id, b.Document().ExplanationUncertaintyStarts, b.Document().ExplanationUncertaintyCounts)
	}
}

func TestExplanationRejectsInvalidShapeWithoutMutation(t *testing.T) {
	b := NewBuilder(Hints{})
	decision := mustTemplate(t, b, "{outcome}", TemplateContextDecision)
	unresolved := mustTemplate(t, b, "{reason}", TemplateContextUnresolved)
	assumption := mustTemplate(t, b, "{request_id}", TemplateContextAssumption)
	tests := []struct {
		name        string
		rationale   schema.TemplateID
		uncertainty []schema.TemplateID
		err         error
	}{
		{"zero rationale", 0, nil, ErrInvalidExplanation},
		{"unknown rationale", 99, nil, ErrInvalidExplanation},
		{"assumption rationale", assumption, nil, ErrInvalidTemplateContext},
		{"zero uncertainty", decision, []schema.TemplateID{0}, ErrInvalidExplanation},
		{"mixed contexts", decision, []schema.TemplateID{unresolved}, ErrInvalidTemplateContext},
		{"too many uncertainty", unresolved, []schema.TemplateID{unresolved, unresolved, unresolved, unresolved, unresolved, unresolved, unresolved, unresolved, unresolved}, ErrTooManyUncertainty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := explanationColumnLengths(b.Document())
			if _, err := b.AddExplanation(tt.rationale, tt.uncertainty); !errors.Is(err, tt.err) {
				t.Fatalf("AddExplanation error = %v, want %v", err, tt.err)
			}
			if after := explanationColumnLengths(b.Document()); after != before {
				t.Fatalf("rejected explanation mutated columns: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestEvidenceIssueTemplatesUseFixedReasonOrder(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, EvidenceNodes: 1, Templates: 2})
	fallback := mustTemplate(t, b, "{evidence_kind}: {reason}", TemplateContextEvidenceMissing)
	present := mustTemplate(t, b, "{evidence_id}: {evidence_state}", TemplateContextEvidencePresent)
	node, err := b.AddEvidence(1, 1, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := b.Document().EvidenceIssueTemplates(node); ok || got != nil {
		t.Fatalf("unset EvidenceIssueTemplates = (%v, %v)", got, ok)
	}
	var templates [EvidenceIssueReasonCount]schema.TemplateID
	for i := range templates {
		templates[i] = fallback
	}
	templates[EvidenceIssueConflict] = present
	if err := b.SetEvidenceIssueTemplates(node, templates); err != nil {
		t.Fatal(err)
	}
	templates[0] = 99
	got, ok := b.Document().EvidenceIssueTemplates(node)
	if !ok || len(got) != EvidenceIssueReasonCount || got[EvidenceIssueMissing] != fallback || got[EvidenceIssueConflict] != present {
		t.Fatalf("EvidenceIssueTemplates = (%v, %v)", got, ok)
	}
	if err := b.SetEvidenceIssueTemplates(node, templates); !errors.Is(err, ErrEvidenceIssuesAlreadySet) {
		t.Fatalf("duplicate SetEvidenceIssueTemplates error = %v", err)
	}
}

func TestEvidenceIssueTemplatesRejectInvalidRowsWithoutMutation(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 2, EvidenceNodes: 1})
	fallback := mustTemplate(t, b, "{evidence_kind}", TemplateContextEvidenceMissing)
	present := mustTemplate(t, b, "{evidence_id}", TemplateContextEvidencePresent)
	other := mustTemplate(t, b, "{outcome}", TemplateContextDecision)
	compare, err := b.AddCompare(1, CompareOpExists, 0, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := b.AddEvidence(1, 1, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	valid := [EvidenceIssueReasonCount]schema.TemplateID{}
	for i := range valid {
		valid[i] = fallback
	}
	tests := []struct {
		name string
		node schema.NodeID
		ids  [EvidenceIssueReasonCount]schema.TemplateID
		err  error
	}{
		{"non evidence", compare, valid, ErrInvalidEvidence},
		{"unknown node", 99, valid, ErrInvalidEvidence},
		{"zero template", evidence, func() [EvidenceIssueReasonCount]schema.TemplateID { ids := valid; ids[1] = 0; return ids }(), ErrInvalidTemplate},
		{"unknown template", evidence, func() [EvidenceIssueReasonCount]schema.TemplateID { ids := valid; ids[1] = 99; return ids }(), ErrInvalidTemplate},
		{"decision context", evidence, func() [EvidenceIssueReasonCount]schema.TemplateID { ids := valid; ids[1] = other; return ids }(), ErrInvalidTemplateContext},
		{"present missing", evidence, func() [EvidenceIssueReasonCount]schema.TemplateID {
			ids := valid
			ids[EvidenceIssueMissing] = present
			return ids
		}(), ErrInvalidTemplateContext},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := append([]schema.TemplateID(nil), b.Document().EvidenceIssueTemplateIDs...)
			if err := b.SetEvidenceIssueTemplates(tt.node, tt.ids); !errors.Is(err, tt.err) {
				t.Fatalf("SetEvidenceIssueTemplates error = %v, want %v", err, tt.err)
			}
			if !reflect.DeepEqual(b.Document().EvidenceIssueTemplateIDs, before) {
				t.Fatal("rejected evidence issues mutated document")
			}
		})
	}
}

func TestClauseRetainsSevenExplanationIDs(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, CompareNodes: 1, Templates: 2, Explanations: 2, Clauses: 1})
	decisionTemplate := mustTemplate(t, b, "{outcome}", TemplateContextDecision)
	unresolvedTemplate := mustTemplate(t, b, "{reason}", TemplateContextUnresolved)
	decision, err := b.AddExplanation(decisionTemplate, nil)
	if err != nil {
		t.Fatal(err)
	}
	unresolved, err := b.AddExplanation(unresolvedTemplate, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertion, err := b.AddExists(1, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	resolution := Resolution{
		OnSatisfied: 1, OnFalse: 2, OnMissing: 3, OnStale: 4, OnUnclear: 5, OnUnverifiable: 6, OnConflict: 7,
		OnSatisfiedExplanation:    decision,
		OnFalseExplanation:        decision,
		OnMissingExplanation:      unresolved,
		OnStaleExplanation:        unresolved,
		OnUnclearExplanation:      unresolved,
		OnUnverifiableExplanation: unresolved,
		OnConflictExplanation:     unresolved,
	}
	clause, err := b.AddClause(assertion, nil, resolution, nil, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	_, got, ok := b.Document().Clause(clause)
	if !ok || got != resolution {
		t.Fatalf("Clause resolution = (%+v, %v), want %+v", got, ok, resolution)
	}
}

func TestClauseRejectsExplanationContextMismatchWithoutMutation(t *testing.T) {
	b := NewBuilder(Hints{Nodes: 1, CompareNodes: 1})
	decisionTemplate := mustTemplate(t, b, "{outcome}", TemplateContextDecision)
	unresolvedTemplate := mustTemplate(t, b, "{reason}", TemplateContextUnresolved)
	decision, _ := b.AddExplanation(decisionTemplate, nil)
	unresolved, _ := b.AddExplanation(unresolvedTemplate, nil)
	assertion, err := b.AddExists(1, SourceSpan{})
	if err != nil {
		t.Fatal(err)
	}
	resolution := Resolution{OnSatisfiedExplanation: unresolved, OnMissingExplanation: decision}
	if _, err := b.AddClause(assertion, nil, resolution, nil, SourceSpan{}); !errors.Is(err, ErrInvalidTemplateContext) {
		t.Fatalf("AddClause error = %v, want ErrInvalidTemplateContext", err)
	}
	if len(b.Document().ClauseAssertionRoots) != 0 || len(b.Document().ClauseExplanationIDs) != 0 {
		t.Fatal("rejected clause mutated columns")
	}
}

func TestExplanationResetRetainsCapacity(t *testing.T) {
	b := NewBuilder(Hints{Templates: 2, Assumptions: 2, Explanations: 2, ExplanationUncertainty: 2, EvidenceNodes: 1})
	assumption := mustTemplate(t, b, "{request_id}", TemplateContextAssumption)
	unresolved := mustTemplate(t, b, "{reason}", TemplateContextUnresolved)
	if err := b.SetAssumptions([]schema.TemplateID{assumption}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddExplanation(unresolved, []schema.TemplateID{unresolved}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddEvidence(1, 1, SourceSpan{}); err != nil {
		t.Fatal(err)
	}
	d := b.Document()
	assumptionsCap := cap(d.AssumptionTemplateIDs)
	explanationsCap := cap(d.ExplanationRationaleIDs)
	uncertaintyCap := cap(d.ExplanationUncertaintyIDs)
	issuesCap := cap(d.EvidenceIssueTemplateIDs)
	b.Reset()
	if len(d.AssumptionTemplateIDs) != 0 || len(d.AssumptionsSet) != 0 || explanationColumnLengths(d) != (explanationLengths{}) || len(d.EvidenceIssueTemplateIDs) != 0 {
		t.Fatal("Reset left active explanation state")
	}
	if cap(d.AssumptionTemplateIDs) != assumptionsCap || cap(d.ExplanationRationaleIDs) != explanationsCap || cap(d.ExplanationUncertaintyIDs) != uncertaintyCap || cap(d.EvidenceIssueTemplateIDs) != issuesCap {
		t.Fatal("Reset discarded explanation storage capacity")
	}
}

type explanationLengths struct {
	rationales, starts, counts, uncertainty int
}

func explanationColumnLengths(d *Document) explanationLengths {
	return explanationLengths{
		rationales:  len(d.ExplanationRationaleIDs),
		starts:      len(d.ExplanationUncertaintyStarts),
		counts:      len(d.ExplanationUncertaintyCounts),
		uncertainty: len(d.ExplanationUncertaintyIDs),
	}
}
