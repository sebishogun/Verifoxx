package diff

import (
	"context"
	"testing"

	resultbatch "github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestExhaustiveDecisionOracleCoversAllTransitions(t *testing.T) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatal(err)
	}
	var matrix RiskMatrix
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			class := Equivalent
			if old < next {
				class = Widened
			} else if old > next {
				class = Narrowed
			}
			if err := matrix.Set(old, next, Transition{Class: class, Allowed: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	oldResults := resultbatch.Batch{Rows: 16, OutcomeIDs: make([]schema.OutcomeID, 16)}
	newResults := resultbatch.Batch{Rows: 16, OutcomeIDs: make([]schema.OutcomeID, 16)}
	for _, offsets := range []*[]uint32{
		&oldResults.RequirementOffsets, &oldResults.DriverOffsets, &oldResults.EvidenceOffsets, &oldResults.ReasonOffsets, &oldResults.RemediationOffsets,
		&newResults.RequirementOffsets, &newResults.DriverOffsets, &newResults.EvidenceOffsets, &newResults.ReasonOffsets, &newResults.RemediationOffsets,
	} {
		*offsets = make([]uint32, 17)
	}
	row := 0
	for old := Approve; old <= Escalate; old++ {
		for next := Approve; next <= Escalate; next++ {
			oldResults.OutcomeIDs[row] = schema.OutcomeID(old)
			newResults.OutcomeIDs[row] = schema.OutcomeID(next)
			row++
		}
	}
	result := Result{Outcome: Equivalent, HasCounterexample: true}
	if err := compareResultBatch(
		&result, oldProgram, newProgram, &oldResults, &newResults,
		&searchPlan{}, Domain{}, matrix, 0,
	); err != nil {
		t.Fatalf("compare oracle rows: %v", err)
	}
	if result.Outcome != Changed {
		t.Fatalf("mixed transition outcome: %s", result.Outcome)
	}
	for index, count := range result.Transitions {
		if count != 1 {
			t.Fatalf("transition %d count = %d, want 1", index, count)
		}
	}
}

func TestExhaustiveComparisonWitnessStableAcrossBatchTails(t *testing.T) {
	changed := changedPolicySource()
	var want Result
	for _, batchRows := range []uint32{1, 63, 64, 65} {
		domain := comparisonDomain()
		domain.BatchRows = batchRows
		var analyzer Analyzer
		var result Result
		if err := analyzer.Compare(
			context.Background(), &result, []byte(nornrune.Source()), changed,
			nativeFieldSchema(), domain, uniformRiskMatrix(Changed, true), nil,
		); err != nil {
			t.Fatalf("batch rows %d: %v", batchRows, err)
		}
		if batchRows == 1 {
			want = result
			continue
		}
		if result.Outcome != want.Outcome || result.Counterexample.Index != want.Counterexample.Index ||
			result.Counterexample.Old.Decision != want.Counterexample.Old.Decision || result.Counterexample.New.Decision != want.Counterexample.New.Decision {
			t.Fatalf("batch rows %d changed result: got %+v want %+v", batchRows, result, want)
		}
	}
}
