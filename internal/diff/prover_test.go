package diff

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

type proverFunc func(context.Context, ProofRequest) (Proof, error)

func (function proverFunc) Prove(ctx context.Context, request ProofRequest) (Proof, error) {
	return function(ctx, request)
}

func changedPolicySource() []byte {
	return []byte(strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`))
}

func compareWithProver(t *testing.T, prover Prover) Result {
	t.Helper()
	var analyzer Analyzer
	var result Result
	if err := analyzer.Compare(
		context.Background(), &result, []byte(nornrune.Source()), changedPolicySource(),
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Widened, true), prover,
	); err != nil {
		t.Fatalf("compare: %v", err)
	}
	return result
}

func TestProverAdvisoryEquivalenceStillRunsConcreteComparison(t *testing.T) {
	called := 0
	result := compareWithProver(t, proverFunc(func(_ context.Context, request ProofRequest) (Proof, error) {
		called++
		if len(request.OldSource) == 0 || len(request.NewSource) == 0 || len(request.Domain.Fields) == 0 {
			t.Fatal("provider request is incomplete")
		}
		request.OldSource[0] = 'x'
		request.Domain.Fields[0].Name = "mutated"
		return Proof{Claim: ProofClaimEquivalent}, nil
	}))
	if called != 1 || result.Outcome != Widened || !result.HasCounterexample {
		t.Fatalf("advisory proof result: called=%d result=%+v", called, result)
	}
}

func TestProverChangedWitnessIsReplayed(t *testing.T) {
	baseline := compareWithProver(t, nil)
	witness := Candidate{
		Fields:      baseline.Counterexample.Fields,
		Evidence:    baseline.Counterexample.Evidence,
		OldDecision: baseline.Counterexample.Old.Decision,
		NewDecision: baseline.Counterexample.New.Decision,
	}
	result := compareWithProver(t, proverFunc(func(context.Context, ProofRequest) (Proof, error) {
		return Proof{Claim: ProofClaimChanged, Witness: witness}, nil
	}))
	if result.Outcome != baseline.Outcome || result.Counterexample.Index != baseline.Counterexample.Index {
		t.Fatalf("replayed result: got %+v, baseline %+v", result, baseline)
	}

	witness.NewDecision = witness.OldDecision
	result = compareWithProver(t, proverFunc(func(context.Context, ProofRequest) (Proof, error) {
		return Proof{Claim: ProofClaimChanged, Witness: witness}, nil
	}))
	if result.Outcome != Inconclusive {
		t.Fatalf("mismatched decisions: %+v", result)
	}
}

func TestProverInvalidClaimsErrorsAndPanicsAreInconclusive(t *testing.T) {
	tests := []struct {
		name   string
		prover Prover
	}{
		{name: "unsupported claim", prover: proverFunc(func(context.Context, ProofRequest) (Proof, error) {
			return Proof{Claim: ProofClaimInvalid}, nil
		})},
		{name: "fabricated witness", prover: proverFunc(func(context.Context, ProofRequest) (Proof, error) {
			return Proof{Claim: ProofClaimChanged, Witness: Candidate{OldDecision: Approve, NewDecision: Reject}}, nil
		})},
		{name: "provider error", prover: proverFunc(func(context.Context, ProofRequest) (Proof, error) {
			return Proof{}, errors.New("provider failed")
		})},
		{name: "provider panic", prover: proverFunc(func(context.Context, ProofRequest) (Proof, error) {
			panic("provider panic")
		})},
		{name: "provider inconclusive", prover: proverFunc(func(context.Context, ProofRequest) (Proof, error) {
			return Proof{Claim: ProofClaimInconclusive}, nil
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := compareWithProver(t, test.prover)
			if result.Outcome != Inconclusive || result.Uncertainty == "" {
				t.Fatalf("provider boundary: %+v", result)
			}
		})
	}
}

func TestProverReceivesCallerCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	provider := proverFunc(func(ctx context.Context, _ ProofRequest) (Proof, error) {
		called = true
		return Proof{}, ctx.Err()
	})
	var analyzer Analyzer
	var result Result
	if err := analyzer.Compare(
		cancelled, &result, []byte(nornrune.Source()), changedPolicySource(),
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Widened, true), provider,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("compare cancelled provider: got %v, want %v", err, context.Canceled)
	}
	if called || !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("pre-canceled comparison invoked provider or changed result: called=%v result=%+v", called, result)
	}
}

func TestProverCooperatesWithCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	provider := proverFunc(func(providerCtx context.Context, _ ProofRequest) (Proof, error) {
		called = true
		cancel()
		<-providerCtx.Done()
		return Proof{}, providerCtx.Err()
	})
	var analyzer Analyzer
	var result Result
	if err := analyzer.Compare(
		ctx, &result, []byte(nornrune.Source()), changedPolicySource(),
		nativeFieldSchema(), comparisonDomain(), uniformRiskMatrix(Widened, true), provider,
	); err != nil {
		t.Fatalf("compare: %v", err)
	}
	if !called || result.Outcome != Inconclusive || result.Uncertainty != "proof provider was inconclusive" {
		t.Fatalf("cooperative provider: called=%v result=%+v", called, result)
	}
}
