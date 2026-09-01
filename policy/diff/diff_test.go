package diff_test

import (
	"context"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/policy/diff"
)

type publicProver struct{}

func (publicProver) Prove(context.Context, diff.ProofRequest) (diff.Proof, error) {
	return diff.Proof{Claim: diff.ProofClaimEquivalent}, nil
}

func TestPublicContractExposesStableOwnedDomain(t *testing.T) {
	domain := diff.Domain{
		Fields: []diff.FieldDomain{{Name: "context.enabled", Kind: diff.FieldKindBoolean, Closed: true, Values: []diff.Value{
			{Kind: diff.FieldKindBoolean, State: diff.ValueMissing},
			{Kind: diff.FieldKindBoolean, State: diff.ValuePresent, Boolean: true},
		}}},
		MaxCandidates: 2,
		BatchRows:     2,
	}
	cardinality, complete, err := diff.ValidateDomain(domain)
	if err != nil || cardinality != 2 || !complete {
		t.Fatalf("ValidateDomain() = (%d,%v,%v)", cardinality, complete, err)
	}
	clone := diff.CloneDomain(domain)
	domain.Fields[0].Values[1].Boolean = false
	if !clone.Fields[0].Values[1].Boolean {
		t.Fatal("public clone borrowed values")
	}
}

func TestFieldSchemaPublicContract(t *testing.T) {
	fields := diff.FieldSchema{Fields: []diff.FieldSpec{{
		Name: "requester.trust", Kind: diff.FieldKindString, Group: diff.FieldGroupSubject,
	}}}
	if len(fields.Fields) != 1 || !fields.Fields[0].Group.Valid() {
		t.Fatalf("field schema: %+v", fields)
	}
}

func TestComparisonResultPublicContract(t *testing.T) {
	var analyzer diff.Analyzer
	var result diff.Result
	_ = analyzer
	result.Counterexample.Fields = []diff.CandidateField{{Name: "requester.trust"}}
	if len(result.Counterexample.Fields) != 1 {
		t.Fatal("counterexample fields unavailable")
	}
}

func TestProverPublicContract(t *testing.T) {
	var prover diff.Prover = publicProver{}
	if prover == nil || !diff.ProofClaimEquivalent.Valid() {
		t.Fatal("public prover contract unavailable")
	}
}

func TestRegressionPublicContract(t *testing.T) {
	result := diff.Result{Outcome: diff.Equivalent}
	decision := diff.CheckRegression(result, nil, nil, []diff.Exception{}, time.Unix(0, 0))
	if !decision.Allowed {
		t.Fatalf("equivalent regression result rejected: %+v", decision)
	}
}
