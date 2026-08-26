package scheduler

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
)

func TestSchedulerExplanationProvenanceEndToEnd(t *testing.T) {
	p, batch := schedulerFixture(t)
	var want result.Batch
	var executor eval.Executor
	if err := executor.Execute(&want, p, batch); err != nil {
		t.Fatalf("direct Execute: %v", err)
	}
	if want.Rows != 3 || len(want.DriverExplanations) != 3 || len(want.ReasonNodes) != 2 ||
		len(want.ReasonEvidenceIDs) != 2 || len(want.ReasonEvidenceStates) != 2 {
		t.Fatalf("fixture omits nonempty explanation provenance: %+v", want)
	}
	var foundEvidence, foundMissing bool
	for reason := range want.ReasonEvidenceIDs {
		evidenceID := want.ReasonEvidenceIDs[reason]
		state := want.ReasonEvidenceStates[reason]
		foundEvidence = foundEvidence || evidenceID != 0 && state != 0
		foundMissing = foundMissing || evidenceID == 0 && state == 0
	}
	if !foundEvidence || !foundMissing {
		t.Fatalf("fixture evidence provenance = IDs %v states %v, want recorded and missing causes", want.ReasonEvidenceIDs, want.ReasonEvidenceStates)
	}
	var explainer result.Explainer
	if err := explainer.Bind(p.ExplanationCatalog()); err != nil {
		t.Fatalf("Bind compiled explanation catalog: %v", err)
	}
	var materialized result.Materialized
	for row, requestID := range batch.RequestIDs {
		if err := explainer.Materialize(&materialized, &want, uint32(row), requestID); err != nil {
			t.Fatalf("Materialize row %d: %v", row, err)
		}
	}

	scheduler := newTestScheduler(t, 4, 1, 1)
	defer closeTestScheduler(t, scheduler)
	var got result.Batch
	if err := scheduler.Execute(context.Background(), &got, p, batch); err != nil {
		t.Fatalf("scheduled Execute: %v", err)
	}
	assertSchedulerResult(t, got, want)
}

func TestSchedulerExplanationProvenanceMerge(t *testing.T) {
	for _, rows := range [...]uint32{0, 1, 63, 64, 65, 256, 257} {
		ranges := partitionRows(make([]rowRange, 4), rows, 4)
		shards := make([]result.Batch, len(ranges))
		for shard := range ranges {
			shards[shard] = schedulerTestResult(t, ranges[shard].start, ranges[shard].end)
		}

		var dst result.Batch
		if rows != 0 {
			dst = schedulerTestResult(t, rows+17, rows+20)
		}
		if err := mergeResults(&dst, shards, ranges, rows); err != nil {
			t.Fatalf("rows=%d mergeResults: %v", rows, err)
		}
		want := schedulerTestResult(t, 0, rows)
		assertSchedulerResult(t, dst, want)
	}
}

func TestSchedulerProvenanceMergeRejectsMalformedColumnsAtomically(t *testing.T) {
	ranges := []rowRange{{0, 64}, {64, 65}}
	shards := []result.Batch{
		schedulerTestResult(t, 0, 64),
		schedulerTestResult(t, 64, 65),
	}
	tests := []struct {
		name   string
		mutate func(*result.Batch)
	}{
		{"driver explanations", func(batch *result.Batch) {
			batch.DriverExplanations = batch.DriverExplanations[:len(batch.DriverExplanations)-1]
		}},
		{"reason nodes", func(batch *result.Batch) {
			batch.ReasonNodes = batch.ReasonNodes[:len(batch.ReasonNodes)-1]
		}},
		{"reason evidence IDs", func(batch *result.Batch) {
			batch.ReasonEvidenceIDs = batch.ReasonEvidenceIDs[:len(batch.ReasonEvidenceIDs)-1]
		}},
		{"reason evidence states", func(batch *result.Batch) {
			batch.ReasonEvidenceStates = batch.ReasonEvidenceStates[:len(batch.ReasonEvidenceStates)-1]
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			badShards := []result.Batch{cloneSchedulerResult(shards[0]), cloneSchedulerResult(shards[1])}
			badRanges := slices.Clone(ranges)
			tc.mutate(&badShards[0])
			dst := schedulerTestResult(t, 20, 27)
			want := cloneSchedulerResult(dst)
			if err := mergeResults(&dst, badShards, badRanges, 65); err == nil {
				t.Fatal("mergeResults error = nil")
			}
			if !reflect.DeepEqual(dst, want) {
				t.Fatalf("invalid merge mutated destination\ngot:  %+v\nwant: %+v", dst, want)
			}
		})
	}
}
