package telemetry

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestCountersSnapshotUsesFixedSchema(t *testing.T) {
	var counters Counters
	delta := BatchDelta{
		Batches: 1, Rows: 10, Duration: 1500 * time.Microsecond, QueueWait: 40 * time.Microsecond, QueueObserved: true,
	}
	delta.Decisions = [DecisionCount]uint64{4, 3, 2, 1}
	delta.Reasons[ReasonMissing] = 1
	delta.Reasons[ReasonStale] = 2
	delta.Audits[AuditPersisted] = 1
	delta.Reloads[ReloadSuccess] = 1
	if err := counters.Add(delta); err != nil {
		t.Fatal(err)
	}
	counters.AddExportDrop(2)
	counters.AddShutdownFailure()
	counters.AdmissionStarted()

	got := counters.Snapshot()
	if got.Batches != 1 || got.Rows != 10 || got.Decisions != delta.Decisions ||
		got.Reasons != delta.Reasons || got.Audits != delta.Audits || got.Reloads != delta.Reloads ||
		got.ExportDrops != 2 || got.ShutdownFailures != 1 || got.ActiveAdmissions != 1 {
		t.Fatalf("Snapshot() = %+v", got)
	}
	if got.LatencyBuckets[durationBucket(delta.Duration)] != 1 || got.QueueBuckets[durationBucket(delta.QueueWait)] != 1 {
		t.Fatalf("duration buckets = latency:%v queue:%v", got.LatencyBuckets, got.QueueBuckets)
	}
	counters.AdmissionFinished()
	if got := counters.Snapshot().ActiveAdmissions; got != 0 {
		t.Fatalf("ActiveAdmissions = %d, want 0", got)
	}
}

func TestCountersNamesAreStableAndBounded(t *testing.T) {
	assertNames(t, DecisionCount, DecisionName, []string{"approve", "reject", "revise", "escalate"})
	assertNames(t, ReasonCount, ReasonName, []string{
		"missing", "stale", "unclear", "unverifiable", "wrong_scope", "wrong_subject", "wrong_timing", "invalid", "conflict",
	})
	assertNames(t, AuditOutcomeCount, AuditOutcomeName, []string{"persisted", "optional_drop", "required_failure"})
	assertNames(t, ReloadOutcomeCount, ReloadOutcomeName, []string{"success", "invalid", "persistence_failure"})
}

func TestCountersRejectInvalidDeltaWithoutMutation(t *testing.T) {
	var counters Counters
	invalid := []BatchDelta{
		{Batches: 1, Rows: 1, Duration: -time.Nanosecond},
		{Batches: 1, Rows: 1, QueueWait: -time.Nanosecond},
		{Batches: 1, Rows: 1, Decisions: [DecisionCount]uint64{2}},
		{Rows: 1, Decisions: [DecisionCount]uint64{1}},
	}
	for _, delta := range invalid {
		if err := counters.Add(delta); err == nil {
			t.Fatalf("Add(%+v) error = nil", delta)
		}
		if got := counters.Snapshot(); got != (Snapshot{}) {
			t.Fatalf("invalid Add mutated counters: %+v", got)
		}
	}
}

func TestCountersSaturateInsteadOfWrapping(t *testing.T) {
	var counters Counters
	if err := counters.Add(BatchDelta{Batches: math.MaxUint64}); err != nil {
		t.Fatal(err)
	}
	if err := counters.Add(BatchDelta{Batches: 1}); err != nil {
		t.Fatal(err)
	}
	if got := counters.Snapshot().Batches; got != math.MaxUint64 {
		t.Fatalf("Batches = %d, want saturation", got)
	}
}

func TestCountersAndSnapshotContainNoDynamicFields(t *testing.T) {
	for _, valueType := range []reflect.Type{reflect.TypeFor[Counters](), reflect.TypeFor[Snapshot]()} {
		for row := range valueType.NumField() {
			assertFixedType(t, valueType.Field(row).Type, valueType.Field(row).Name)
		}
	}
}

func assertNames[T ~uint8](t *testing.T, count T, name func(T) (string, bool), want []string) {
	t.Helper()
	if int(count) != len(want) {
		t.Fatalf("count = %d, want %d", count, len(want))
	}
	for row, expected := range want {
		if got, ok := name(T(row)); !ok || got != expected {
			t.Fatalf("name(%d) = (%q, %t), want %q", row, got, ok, expected)
		}
	}
	if got, ok := name(count); ok || got != "" {
		t.Fatalf("name(count) = (%q, %t), want invalid", got, ok)
	}
}

func assertFixedType(t *testing.T, valueType reflect.Type, path string) {
	t.Helper()
	switch valueType.Kind() {
	case reflect.Map, reflect.Pointer, reflect.Slice, reflect.String, reflect.Interface:
		t.Fatalf("%s has dynamic type %s", path, valueType)
	case reflect.Array:
		assertFixedType(t, valueType.Elem(), path+"[]")
	case reflect.Struct:
		for row := range valueType.NumField() {
			field := valueType.Field(row)
			assertFixedType(t, field.Type, path+"."+field.Name)
		}
	}
}
