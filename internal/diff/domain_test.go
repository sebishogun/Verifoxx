package diff

import (
	"errors"
	"math"
	"testing"
)

func TestOutcomeAndDecisionNamesAreStable(t *testing.T) {
	outcomes := []Outcome{Equivalent, Widened, Narrowed, Changed, Inconclusive}
	for row, outcome := range outcomes {
		if got, want := uint8(outcome), uint8(row+1); got != want {
			t.Fatalf("outcome %d = %d, want %d", row, got, want)
		}
		text, err := outcome.MarshalText()
		if err != nil || string(text) != outcome.String() {
			t.Fatalf("MarshalText(%v) = %q, %v", outcome, text, err)
		}
		var decoded Outcome
		if err := decoded.UnmarshalText(text); err != nil || decoded != outcome {
			t.Fatalf("UnmarshalText(%q) = %v, %v", text, decoded, err)
		}
	}
	decisions := []Decision{Approve, Reject, Revise, Escalate}
	for row, decision := range decisions {
		if got, want := uint8(decision), uint8(row+1); got != want {
			t.Fatalf("decision %d = %d, want %d", row, got, want)
		}
		if !decision.Valid() || decision.String() == "invalid" {
			t.Fatalf("decision %d is invalid", decision)
		}
	}
	if OutcomeInvalid.Valid() || DecisionInvalid.Valid() {
		t.Fatal("zero enum is valid")
	}
}

func TestRiskMatrixRequiresEveryTransitionAndIndexesRows(t *testing.T) {
	var matrix RiskMatrix
	if err := matrix.Validate(); !errors.Is(err, ErrInvalidRiskMatrix) {
		t.Fatalf("zero matrix error = %v", err)
	}
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			class := Changed
			if old == next {
				class = Equivalent
			}
			if err := matrix.Set(old, next, Transition{Class: class, Allowed: old == next}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := matrix.Validate(); err != nil {
		t.Fatal(err)
	}
	transition, ok := matrix.Lookup(Approve, Reject)
	if !ok || transition.Class != Changed || transition.Allowed {
		t.Fatalf("Approve->Reject = %#v, %v", transition, ok)
	}
	if _, ok := matrix.Lookup(DecisionInvalid, Approve); ok {
		t.Fatal("invalid transition lookup succeeded")
	}
}

func TestDomainValidationCardinalityAndCompleteness(t *testing.T) {
	domain := Domain{
		Fields: []FieldDomain{
			{Name: "subject.team", Kind: FieldKindString, Closed: true, Values: []Value{
				{Kind: FieldKindString, State: ValueMissing},
				{Kind: FieldKindString, State: ValuePresent, String: "blue"},
			}},
			{Name: "context.count", Kind: FieldKindInteger, Closed: true, Values: []Value{
				{Kind: FieldKindInteger, State: ValueMissing},
				{Kind: FieldKindInteger, State: ValuePresent, Integer: math.MinInt64},
				{Kind: FieldKindInteger, State: ValuePresent, Integer: math.MaxInt64},
			}},
		},
		EvidenceSets: []EvidenceSet{
			{},
			{Records: []Evidence{{Kind: "approval", State: "current", Subject: "request", Scope: "aggregate", Timing: "pre_execution"}}},
		},
		MaxCandidates: 12,
		BatchRows:     4,
	}
	cardinality, complete, err := domain.Validate()
	if err != nil || cardinality != 12 || !complete {
		t.Fatalf("Validate() = (%d,%v,%v), want (12,true,nil)", cardinality, complete, err)
	}
	domain.Fields[0].Closed = false
	cardinality, complete, err = domain.Validate()
	if err != nil || cardinality != 12 || complete {
		t.Fatalf("open Validate() = (%d,%v,%v), want (12,false,nil)", cardinality, complete, err)
	}
}

func TestDomainRejectsMalformedShapes(t *testing.T) {
	valid := Domain{
		Fields: []FieldDomain{{Name: "context.enabled", Kind: FieldKindBoolean, Closed: true, Values: []Value{
			{Kind: FieldKindBoolean, State: ValueMissing},
			{Kind: FieldKindBoolean, State: ValuePresent, Boolean: true},
		}}},
		MaxCandidates: 2,
		BatchRows:     1,
	}
	tests := []struct {
		name   string
		mutate func(*Domain)
	}{
		{name: "zero budget", mutate: func(domain *Domain) { domain.MaxCandidates = 0 }},
		{name: "zero rows", mutate: func(domain *Domain) { domain.BatchRows = 0 }},
		{name: "too many rows", mutate: func(domain *Domain) { domain.BatchRows = MaxBatchRows + 1 }},
		{name: "empty name", mutate: func(domain *Domain) { domain.Fields[0].Name = "" }},
		{name: "invalid kind", mutate: func(domain *Domain) { domain.Fields[0].Kind = FieldKindInvalid }},
		{name: "empty values", mutate: func(domain *Domain) { domain.Fields[0].Values = nil }},
		{name: "missing absent", mutate: func(domain *Domain) { domain.Fields[0].Values = domain.Fields[0].Values[1:] }},
		{name: "value kind mismatch", mutate: func(domain *Domain) { domain.Fields[0].Values[1].Kind = FieldKindInteger }},
		{name: "duplicate", mutate: func(domain *Domain) { domain.Fields = append(domain.Fields, domain.Fields[0]) }},
		{name: "invalid evidence", mutate: func(domain *Domain) { domain.EvidenceSets = []EvidenceSet{{Records: []Evidence{{Kind: "approval"}}}} }},
		{name: "budget below product", mutate: func(domain *Domain) { domain.MaxCandidates = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			domain := CloneDomain(valid)
			test.mutate(&domain)
			if _, _, err := domain.Validate(); !errors.Is(err, ErrInvalidDomain) && !errors.Is(err, ErrCandidateBudget) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

func TestCloneDomainOwnsNestedStorage(t *testing.T) {
	source := Domain{
		Fields: []FieldDomain{{Name: "subject.team", Kind: FieldKindString, Closed: true, Values: []Value{
			{Kind: FieldKindString, State: ValueMissing},
			{Kind: FieldKindString, State: ValuePresent, String: "blue"},
		}}},
		EvidenceSets:  []EvidenceSet{{Records: []Evidence{{Kind: "approval", State: "current"}}}},
		MaxCandidates: 2,
		BatchRows:     2,
	}
	cloned := CloneDomain(source)
	source.Fields[0].Name = "changed"
	source.Fields[0].Values[1].String = "red"
	source.EvidenceSets[0].Records[0].Kind = "changed"
	if cloned.Fields[0].Name != "subject.team" || cloned.Fields[0].Values[1].String != "blue" || cloned.EvidenceSets[0].Records[0].Kind != "approval" {
		t.Fatalf("clone borrowed storage: %#v", cloned)
	}
}
