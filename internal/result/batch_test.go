package result

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

func TestBatchResetShapesAndReuses(t *testing.T) {
	var batch Batch
	if err := batch.Reset(65); err != nil {
		t.Fatalf("Reset(65): %v", err)
	}
	assertBatchShape(t, &batch, 65)

	for i := range batch.OutcomeIDs {
		batch.OutcomeIDs[i] = schema.OutcomeID(i + 1)
	}
	poisonOffsets(&batch)
	batch.RequirementIDs = append(batch.RequirementIDs, 1, 2)
	batch.DriverRequirements = append(batch.DriverRequirements, 1)
	batch.DriverClauses = append(batch.DriverClauses, 1)
	batch.DriverNodes = append(batch.DriverNodes, 1)
	batch.DriverReasons = append(batch.DriverReasons, 1)
	batch.DriverExplanations = append(batch.DriverExplanations, 1)
	batch.EvidenceIDs = append(batch.EvidenceIDs, 1)
	batch.ReasonIDs = append(batch.ReasonIDs, 1)
	batch.ReasonNodes = append(batch.ReasonNodes, 1)
	batch.ReasonEvidenceIDs = append(batch.ReasonEvidenceIDs, 1)
	batch.ReasonEvidenceStates = append(batch.ReasonEvidenceStates, 1)
	batch.RemediationIDs = append(batch.RemediationIDs, 1)
	edgeCaps := batchEdgeCaps(&batch)

	if err := batch.Reset(3); err != nil {
		t.Fatalf("Reset(3): %v", err)
	}
	assertBatchShape(t, &batch, 3)
	assertBatchZero(t, &batch)
	if got := batchEdgeCaps(&batch); !reflect.DeepEqual(got, edgeCaps) {
		t.Fatalf("edge capacities after shrink = %v, want %v", got, edgeCaps)
	}

	for i := range batch.OutcomeIDs {
		batch.OutcomeIDs[i] = math.MaxUint32
	}
	poisonOffsets(&batch)
	if err := batch.Reset(65); err != nil {
		t.Fatalf("second Reset(65): %v", err)
	}
	assertBatchShape(t, &batch, 65)
	assertBatchZero(t, &batch)
}

func TestBatchResetRejectsInvalidReceiverAndWidth(t *testing.T) {
	var nilBatch *Batch
	if err := nilBatch.Reset(0); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("nil Reset error = %v, want %v", err, ErrBatchTooLarge)
	}

	if strconv.IntSize != 32 {
		return
	}
	batch := Batch{Rows: 7, OutcomeIDs: []schema.OutcomeID{1}}
	want := batch
	if err := batch.Reset(math.MaxUint32); !errors.Is(err, ErrBatchTooLarge) {
		t.Fatalf("oversized Reset error = %v, want %v", err, ErrBatchTooLarge)
	}
	if !reflect.DeepEqual(batch, want) {
		t.Fatalf("failed Reset changed batch: got %+v want %+v", batch, want)
	}
}

func poisonOffsets(batch *Batch) {
	for _, offsets := range [][]uint32{
		batch.RequirementOffsets,
		batch.DriverOffsets,
		batch.EvidenceOffsets,
		batch.ReasonOffsets,
		batch.RemediationOffsets,
	} {
		for i := range offsets {
			offsets[i] = math.MaxUint32
		}
	}
}

func assertBatchShape(t *testing.T, batch *Batch, rows uint32) {
	t.Helper()
	if batch.Rows != rows || len(batch.OutcomeIDs) != int(rows) {
		t.Fatalf("fixed shape = rows %d outcomes %d, want %d/%d", batch.Rows, len(batch.OutcomeIDs), rows, rows)
	}
	wantOffsets := int(rows) + 1
	for name, offsets := range map[string][]uint32{
		"requirements": batch.RequirementOffsets,
		"drivers":      batch.DriverOffsets,
		"evidence":     batch.EvidenceOffsets,
		"reasons":      batch.ReasonOffsets,
		"remediations": batch.RemediationOffsets,
	} {
		if len(offsets) != wantOffsets {
			t.Fatalf("%s offsets = %d, want %d", name, len(offsets), wantOffsets)
		}
	}
	for name, length := range map[string]int{
		"requirements":        len(batch.RequirementIDs),
		"driver requirements": len(batch.DriverRequirements),
		"driver clauses":      len(batch.DriverClauses),
		"driver nodes":        len(batch.DriverNodes),
		"driver reasons":      len(batch.DriverReasons),
		"driver explanations": len(batch.DriverExplanations),
		"evidence":            len(batch.EvidenceIDs),
		"reasons":             len(batch.ReasonIDs),
		"reason nodes":        len(batch.ReasonNodes),
		"reason evidence":     len(batch.ReasonEvidenceIDs),
		"reason states":       len(batch.ReasonEvidenceStates),
		"remediations":        len(batch.RemediationIDs),
	} {
		if length != 0 {
			t.Fatalf("%s edges = %d, want 0", name, length)
		}
	}
}

func assertBatchZero(t *testing.T, batch *Batch) {
	t.Helper()
	for row, outcome := range batch.OutcomeIDs {
		if outcome != 0 {
			t.Fatalf("outcome[%d] = %d, want 0", row, outcome)
		}
	}
	for _, offsets := range [][]uint32{
		batch.RequirementOffsets,
		batch.DriverOffsets,
		batch.EvidenceOffsets,
		batch.ReasonOffsets,
		batch.RemediationOffsets,
	} {
		for row, offset := range offsets {
			if offset != 0 {
				t.Fatalf("offset[%d] = %d, want 0", row, offset)
			}
		}
	}
}

func batchEdgeCaps(batch *Batch) [12]int {
	return [12]int{
		cap(batch.RequirementIDs),
		cap(batch.DriverRequirements),
		cap(batch.DriverClauses),
		cap(batch.DriverNodes),
		cap(batch.DriverReasons),
		cap(batch.DriverExplanations),
		cap(batch.EvidenceIDs),
		cap(batch.ReasonIDs),
		cap(batch.ReasonNodes),
		cap(batch.ReasonEvidenceIDs),
		cap(batch.ReasonEvidenceStates),
		cap(batch.RemediationIDs),
	}
}
