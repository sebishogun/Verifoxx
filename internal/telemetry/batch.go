package telemetry

import (
	"time"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

type OutcomeIDs struct {
	Approve  schema.OutcomeID
	Reject   schema.OutcomeID
	Revise   schema.OutcomeID
	Escalate schema.OutcomeID
}

// ObserveBatch validates and folds one completed result batch in a single
// pass. Validation and counting are fused: the reason-offset walk happens
// once, and no counter mutates unless the whole batch validates, because the
// aggregate is applied only by the final Add.
func ObserveBatch(
	counters *Counters,
	batch *result.Batch,
	outcomes OutcomeIDs,
	duration time.Duration,
) error {
	if counters == nil || duration < 0 || !validOutcomeIDs(outcomes) || batch == nil || batch.Rows == 0 ||
		uint64(batch.Rows) > uint64(^uint(0)>>1) {
		return ErrInvalidDelta
	}
	rows := int(batch.Rows)
	offsets := batch.ReasonOffsets
	reasons := batch.ReasonIDs
	if len(batch.OutcomeIDs) != rows || len(offsets) != rows+1 || offsets[0] != 0 ||
		uint64(offsets[rows]) != uint64(len(reasons)) {
		return ErrInvalidDelta
	}
	delta := BatchDelta{Batches: 1, Rows: uint64(batch.Rows), Duration: duration}
	previous := uint32(0)
	for row, outcomeID := range batch.OutcomeIDs {
		end := offsets[row+1]
		if end < previous || uint64(end) > uint64(len(reasons)) {
			return ErrInvalidDelta
		}
		decision, ok := decisionForOutcome(outcomes, outcomeID)
		if !ok {
			return ErrInvalidDelta
		}
		delta.Decisions[decision]++
		if end > previous {
			for _, reasonID := range reasons[previous:end] {
				if reasonID < truth.ReasonMissing || reasonID > truth.ReasonConflict {
					return ErrInvalidDelta
				}
				if decision == DecisionEscalate {
					delta.Reasons[Reason(reasonID-truth.ReasonMissing)]++
				}
			}
		}
		previous = end
	}
	return counters.Add(delta)
}

// validOutcomeIDs accepts absent outcomes as the zero ID but rejects
// duplicates among the IDs that are present.
func validOutcomeIDs(outcomes OutcomeIDs) bool {
	values := [...]schema.OutcomeID{outcomes.Approve, outcomes.Reject, outcomes.Revise, outcomes.Escalate}
	for row, value := range values {
		for previous := range row {
			if value != 0 && value == values[previous] {
				return false
			}
		}
	}
	return true
}

func decisionForOutcome(outcomes OutcomeIDs, id schema.OutcomeID) (Decision, bool) {
	if id == 0 {
		return 0, false
	}
	switch id {
	case outcomes.Approve:
		return DecisionApprove, true
	case outcomes.Reject:
		return DecisionReject, true
	case outcomes.Revise:
		return DecisionRevise, true
	case outcomes.Escalate:
		return DecisionEscalate, true
	default:
		return 0, false
	}
}
