package telemetry

import (
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func TestObserveBatchAggregatesDecisionsAndEscalationReasons(t *testing.T) {
	ids := testOutcomeIDs()
	batch := result.Batch{
		Rows:          5,
		OutcomeIDs:    []schema.OutcomeID{ids.Approve, ids.Reject, ids.Revise, ids.Escalate, ids.Escalate},
		ReasonOffsets: []uint32{0, 0, 0, 0, 2, 3},
		ReasonIDs:     []schema.ReasonID{truth.ReasonMissing, truth.ReasonStale, truth.ReasonConflict},
		ReasonNodes:   make([]schema.NodeID, 3), ReasonEvidenceIDs: make([]schema.EvidenceID, 3),
		ReasonEvidenceStates: make([]schema.EvidenceStateID, 3),
	}
	var counters Counters
	if err := ObserveBatch(&counters, &batch, ids, 2*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got := counters.Snapshot()
	if got.Batches != 1 || got.Rows != 5 || got.Decisions != [DecisionCount]uint64{1, 1, 1, 2} {
		t.Fatalf("batch snapshot = %+v", got)
	}
	wantReasons := [ReasonCount]uint64{}
	wantReasons[ReasonMissing] = 1
	wantReasons[ReasonStale] = 1
	wantReasons[ReasonConflict] = 1
	if got.Reasons != wantReasons {
		t.Fatalf("reasons = %v, want %v", got.Reasons, wantReasons)
	}
}

func TestObserveBatchRejectsMalformedInputWithoutMutation(t *testing.T) {
	ids := testOutcomeIDs()
	valid := result.Batch{Rows: 1, OutcomeIDs: []schema.OutcomeID{ids.Approve}, ReasonOffsets: []uint32{0, 0}}
	tests := []result.Batch{
		{},
		{Rows: 1},
		{Rows: 1, OutcomeIDs: []schema.OutcomeID{99}, ReasonOffsets: []uint32{0, 0}},
		{Rows: 1, OutcomeIDs: []schema.OutcomeID{ids.Approve}, ReasonOffsets: []uint32{0}},
		{Rows: 1, OutcomeIDs: []schema.OutcomeID{ids.Escalate}, ReasonOffsets: []uint32{0, 1}, ReasonIDs: []schema.ReasonID{truth.ReasonConflict + 1}},
	}
	for _, batch := range tests {
		var counters Counters
		if err := ObserveBatch(&counters, &batch, ids, time.Millisecond); err == nil {
			t.Fatalf("ObserveBatch(%+v) error = nil", batch)
		}
		if got := counters.Snapshot(); got != (Snapshot{}) {
			t.Fatalf("invalid batch mutated counters: %+v", got)
		}
	}
	var counters Counters
	invalidIDs := ids
	invalidIDs.Escalate = invalidIDs.Approve
	if err := ObserveBatch(&counters, &valid, invalidIDs, time.Millisecond); err == nil {
		t.Fatal("ObserveBatch duplicate outcome IDs error = nil")
	}
}

func TestObserveBatchHandlesMaximumConfiguredRows(t *testing.T) {
	const rows = 64 << 10
	ids := testOutcomeIDs()
	batch := result.Batch{Rows: rows, OutcomeIDs: make([]schema.OutcomeID, rows), ReasonOffsets: make([]uint32, rows+1)}
	for row := range batch.OutcomeIDs {
		batch.OutcomeIDs[row] = ids.Approve
	}
	var counters Counters
	if err := ObserveBatch(&counters, &batch, ids, time.Second); err != nil {
		t.Fatal(err)
	}
	got := counters.Snapshot()
	if got.Rows != rows || got.Decisions[DecisionApprove] != rows {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestObserveBatchAcceptsPoliciesWithAbsentOutcomes(t *testing.T) {
	ids := testOutcomeIDs()
	ids.Escalate = 0
	batch := result.Batch{Rows: 2, OutcomeIDs: []schema.OutcomeID{ids.Approve, ids.Reject}, ReasonOffsets: []uint32{0, 0, 0}}
	var counters Counters
	if err := ObserveBatch(&counters, &batch, ids, time.Millisecond); err != nil {
		t.Fatalf("ObserveBatch() with absent escalate outcome error = %v", err)
	}
	got := counters.Snapshot()
	if got.Decisions[DecisionApprove] != 1 || got.Decisions[DecisionReject] != 1 || got.Decisions[DecisionEscalate] != 0 {
		t.Fatalf("decisions = %+v", got.Decisions)
	}
}

func testOutcomeIDs() OutcomeIDs {
	return OutcomeIDs{Approve: 1, Reject: 2, Revise: 3, Escalate: 4}
}
