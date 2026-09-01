package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRuntimeExportsSnapshotToConfiguredOTLPEndpoint(t *testing.T) {
	var metricRequests atomic.Uint64
	collector := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/metrics" {
			metricRequests.Add(1)
		}
		response.Header().Set("Content-Type", "application/x-protobuf")
		response.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	config := validConfig()
	config.Endpoint = collector.URL
	config.ExportInterval = 100 * time.Millisecond
	runtime, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	delta := BatchDelta{Batches: 1, Rows: 1}
	delta.Decisions[DecisionApprove] = 1
	if err := runtime.Record(delta); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.ForceFlush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := metricRequests.Load(); got == 0 {
		t.Fatal("configured OTLP collector received no metric export")
	}
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDisabledRuntimeDoesNotRecord(t *testing.T) {
	runtime, err := New(context.Background(), Config{})
	if err != nil {
		t.Fatal(err)
	}
	delta := BatchDelta{Batches: 1, Rows: 1}
	delta.Decisions[DecisionApprove] = 1
	if err := runtime.Record(delta); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Snapshot(); got != (Snapshot{}) {
		t.Fatalf("Snapshot() = %+v, want zero", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRecordsAdmissionLifetimeAndQueueWait(t *testing.T) {
	runtime, err := New(context.Background(), validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AdmissionStarted(250 * time.Microsecond); err != nil {
		t.Fatal(err)
	}
	got := runtime.Snapshot()
	if got.ActiveAdmissions != 1 || got.QueueObservations != 1 || got.QueueWaitNanoseconds != uint64(250*time.Microsecond) {
		t.Fatalf("admission snapshot = %+v", got)
	}
	runtime.AdmissionFinished()
	if got := runtime.Snapshot().ActiveAdmissions; got != 0 {
		t.Fatalf("active admissions = %d, want 0", got)
	}
}

func TestRuntimeSnapshotAndOTelMetricUseSameCounters(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	var queueDepth atomic.Uint64
	queueDepth.Store(3)
	config := validConfig()
	config.QueueDepth = queueDepth.Load
	runtime, err := New(context.Background(), config, WithMetricReader(reader))
	if err != nil {
		t.Fatal(err)
	}
	delta := BatchDelta{Batches: 1, Rows: 10, Duration: time.Millisecond, QueueWait: 40 * time.Microsecond, QueueObserved: true}
	delta.Decisions = [DecisionCount]uint64{4, 3, 2, 1}
	delta.Reasons[ReasonMissing] = 1
	delta.Audits[AuditPersisted] = 1
	delta.Reloads[ReloadSuccess] = 1
	if err := runtime.Record(delta); err != nil {
		t.Fatal(err)
	}
	if got := runtime.Snapshot(); got.Rows != 10 || got.Decisions != delta.Decisions {
		t.Fatalf("Snapshot() = %+v", got)
	}

	var exported metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &exported); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		name  string
		value int64
		attrs [][2]string
	}{
		{name: "nornrune.evaluation.rows", value: 10},
		{name: "nornrune.evaluation.batches", value: 1},
		{name: "nornrune.evaluation.outcomes", value: 4, attrs: [][2]string{{"outcome", "approve"}}},
		{name: "nornrune.evaluation.escalations", value: 1, attrs: [][2]string{{"reason", "missing"}}},
		{name: "nornrune.audit.outcomes", value: 1, attrs: [][2]string{{"outcome", "persisted"}}},
		{name: "nornrune.policy.reloads", value: 1, attrs: [][2]string{{"outcome", "success"}}},
		{name: "nornrune.evaluation.duration_ns", value: int64(time.Millisecond)},
		{name: "nornrune.service.queue_wait_ns", value: int64(40 * time.Microsecond)},
		{name: "nornrune.evaluation.duration_bucket", value: 1, attrs: [][2]string{{"le", "0.001"}}},
		{name: "nornrune.evaluation.duration_bucket", value: 1, attrs: [][2]string{{"le", "+Inf"}}},
		{name: "nornrune.service.queue_wait_bucket", value: 1, attrs: [][2]string{{"le", "0.0001"}}},
		{name: "nornrune.service.queue_wait_bucket", value: 1, attrs: [][2]string{{"le", "+Inf"}}},
	} {
		if got, ok := metricInt64(exported, expected.name, expected.attrs); !ok || got != expected.value {
			t.Fatalf("OTel %s = (%d, %t), want (%d, true)", expected.name, got, ok, expected.value)
		}
	}
	if got, ok := metricInt64(exported, "nornrune.service.queue_depth", nil); !ok || got != 3 {
		t.Fatalf("OTel queue depth = (%d, %t), want (3, true)", got, ok)
	}
}

func TestRuntimeRejectsInvalidConfiguration(t *testing.T) {
	tests := []Config{
		{Enabled: true},
		{Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second, ExportQueueSize: 1, TraceSampleRatio: -1},
		{Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second, ExportQueueSize: 1, TraceSampleRatio: 1.1},
		{Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportQueueSize: 1},
		{Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second},
		{Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second, ExportQueueSize: 1, Endpoint: "ftp://collector"},
		{Enabled: true, ServiceVersion: "test", BuildVersion: "test", ExportInterval: time.Second, ExportQueueSize: 1, Endpoint: "https://user:secret@collector/v1/traces"},
	}
	for _, config := range tests {
		if runtime, err := New(context.Background(), config); err == nil || runtime != nil {
			t.Fatalf("New(%+v) = (%p, %v), want nil error", config, runtime, err)
		}
	}
}

func validConfig() Config {
	return Config{
		Enabled: true, ServiceVersion: "nornrune-test", BuildVersion: "test-build",
		ExportInterval: time.Second, ExportQueueSize: 128, TraceSampleRatio: 0.1,
	}
}

func attributeValue(set attribute.Set, key string) (string, bool) {
	iterator := set.Iter()
	for iterator.Next() {
		pair := iterator.Attribute()
		if string(pair.Key) == key {
			return pair.Value.AsString(), true
		}
	}
	return "", false
}

func metricInt64(metrics metricdata.ResourceMetrics, name string, attrs [][2]string) (int64, bool) {
	for _, scope := range metrics.ScopeMetrics {
		for _, value := range scope.Metrics {
			if value.Name != name {
				continue
			}
			var points []metricdata.DataPoint[int64]
			switch data := value.Data.(type) {
			case metricdata.Sum[int64]:
				points = data.DataPoints
			case metricdata.Gauge[int64]:
				points = data.DataPoints
			default:
				continue
			}
			for _, point := range points {
				if len(attrs) != point.Attributes.Len() {
					continue
				}
				matched := true
				for _, pair := range attrs {
					if found, ok := attributeValue(point.Attributes, pair[0]); !ok || found != pair[1] {
						matched = false
						break
					}
				}
				if matched {
					return point.Value, true
				}
			}
		}
	}
	return 0, false
}
