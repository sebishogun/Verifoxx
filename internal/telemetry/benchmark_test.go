package telemetry

import (
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func BenchmarkObserveBatch(b *testing.B) {
	ids := testOutcomeIDs()
	batch := result.Batch{Rows: 256, OutcomeIDs: make([]schema.OutcomeID, 256), ReasonOffsets: make([]uint32, 257)}
	for row := range batch.OutcomeIDs {
		batch.OutcomeIDs[row] = ids.Approve
	}
	var counters Counters
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := ObserveBatch(&counters, &batch, ids, time.Millisecond); err != nil {
			b.Fatal(err)
		}
	}
}
