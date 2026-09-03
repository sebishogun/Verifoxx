package telemetry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	"github.com/sebishogun/nornrune/internal/truth"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type blockingSpanExporter struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (exporter *blockingSpanExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	exporter.once.Do(func() { close(exporter.started) })
	select {
	case <-exporter.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (*blockingSpanExporter) Shutdown(context.Context) error { return nil }

func TestRuntimeStartsFixedRedactedChildSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	runtime, err := New(context.Background(), validConfig(), WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	carrier := propagation.MapCarrier{
		"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	}
	parent := runtime.Extract(context.Background(), carrier)
	ctx, span := runtime.Start(parent, OperationEvaluation, TransportHTTP)
	if !span.SpanContext().IsValid() {
		t.Fatal("Start returned invalid span context")
	}
	batch := result.Batch{
		Rows: 2, OutcomeIDs: []schema.OutcomeID{1, 4}, ReasonOffsets: []uint32{0, 0, 2},
		ReasonIDs: []schema.ReasonID{truth.ReasonMissing, truth.ReasonConflict},
	}
	if err := runtime.ObserveEvaluation(ctx, &batch, internaltelemetry.OutcomeIDs{Approve: 1, Reject: 2, Revise: 3, Escalate: 4}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	runtime.Finish(span, SpanStatusUnavailable)
	if err := runtime.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("exported spans = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Name != "nornrune.evaluation" || got.Parent.SpanID().String() != "00f067aa0ba902b7" {
		t.Fatalf("span = name:%q parent:%s", got.Name, got.Parent.SpanID())
	}
	wantAttributes := map[string]string{
		"nornrune.operation": "evaluation", "nornrune.transport": "http", "nornrune.status": "unavailable",
		"nornrune.decision": "mixed", "nornrune.reason": "multiple",
	}
	if len(got.Attributes) != len(wantAttributes) {
		t.Fatalf("attributes = %v", got.Attributes)
	}
	for _, value := range got.Attributes {
		if wantAttributes[string(value.Key)] != value.Value.AsString() {
			t.Fatalf("attribute %s = %q, want %q", value.Key, value.Value.AsString(), wantAttributes[string(value.Key)])
		}
	}
	if got.Status.Code != codes.Error || got.Status.Description != "unavailable" || got.EndTime.Before(got.StartTime) {
		t.Fatalf("span status/duration = %+v %s", got.Status, got.EndTime.Sub(got.StartTime))
	}
	if traceID := span.SpanContext().TraceID(); traceID != got.SpanContext.TraceID() || ctx == nil {
		t.Fatalf("span context mismatch: %s vs %s", traceID, got.SpanContext.TraceID())
	}
}

func TestOperationNamesAreFixed(t *testing.T) {
	want := []string{"admission", "decode", "policy_lookup", "evaluation", "audit_acknowledgment", "response_encode"}
	if int(OperationCount) != len(want) {
		t.Fatalf("OperationCount = %d, want %d", OperationCount, len(want))
	}
	for row, expected := range want {
		if got, ok := OperationName(Operation(row)); !ok || got != expected {
			t.Fatalf("OperationName(%d) = (%q, %t), want %q", row, got, ok, expected)
		}
	}
	for row, expected := range []string{"http", "grpc", "service"} {
		if got, ok := TransportName(Transport(row)); !ok || got != expected {
			t.Fatalf("TransportName(%d) = (%q, %t), want %q", row, got, ok, expected)
		}
	}
	for row, expected := range []string{"ok", "invalid_argument", "not_found", "resource_exhausted", "canceled", "deadline_exceeded", "unavailable", "internal"} {
		if got, ok := SpanStatusName(SpanStatus(row)); !ok || got != expected {
			t.Fatalf("SpanStatusName(%d) = (%q, %t), want %q", row, got, ok, expected)
		}
	}
}

func TestTraceQueueOverflowIncrementsExportDrops(t *testing.T) {
	exporter := &blockingSpanExporter{started: make(chan struct{}), release: make(chan struct{})}
	config := validConfig()
	config.ExportInterval = 100 * time.Millisecond
	config.ExportQueueSize = 1
	config.TraceSampleRatio = 1
	runtime, err := New(context.Background(), config, WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	endSpan := func() {
		_, span := runtime.Start(context.Background(), OperationEvaluation, TransportService)
		runtime.Finish(span, SpanStatusOK)
	}
	endSpan()
	select {
	case <-exporter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("trace export did not start")
	}
	endSpan()
	endSpan()
	if got := runtime.Snapshot().ExportDrops; got != 1 {
		t.Fatalf("export drops = %d, want 1", got)
	}
	close(exporter.release)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

var errTerminalSpanShutdown = errors.New("terminal span shutdown")

type lifecycleSpanExporter struct {
	exportStarted   chan struct{}
	releaseExport   chan struct{}
	shutdownStarted chan struct{}
	releaseShutdown chan struct{}
	shutdownErr     error
	exportOnce      sync.Once
	shutdownOnce    sync.Once
	exports         atomic.Uint64
	shutdowns       atomic.Uint64
}

func (exporter *lifecycleSpanExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	exporter.exports.Add(1)
	if exporter.exportStarted != nil {
		exporter.exportOnce.Do(func() { close(exporter.exportStarted) })
	}
	if exporter.releaseExport == nil {
		return nil
	}
	select {
	case <-exporter.releaseExport:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (exporter *lifecycleSpanExporter) Shutdown(ctx context.Context) error {
	exporter.shutdowns.Add(1)
	if exporter.shutdownStarted != nil {
		exporter.shutdownOnce.Do(func() { close(exporter.shutdownStarted) })
	}
	if exporter.releaseShutdown != nil {
		select {
		case <-exporter.releaseShutdown:
		case <-ctx.Done():
			return errors.Join(exporter.shutdownErr, ctx.Err())
		}
	}
	return exporter.shutdownErr
}

func TestSpanProcessorShutdownContinuesAfterCallerCancellation(t *testing.T) {
	exporter := &lifecycleSpanExporter{
		exportStarted: make(chan struct{}),
		releaseExport: make(chan struct{}),
	}
	counters := &internaltelemetry.Counters{}
	processor := newBoundedSpanProcessor(exporter, counters, 1, time.Hour)
	processor.OnEnd(sampledReadOnlySpan())
	requireSignal(t, exporter.exportStarted, "span export did not start")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := processor.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v, want cancellation", err)
	}
	close(exporter.releaseExport)
	requireSignal(t, processor.done, "processor cleanup did not continue")
	if exporter.shutdowns.Load() != 1 {
		t.Fatalf("exporter Shutdown calls = %d, want 1", exporter.shutdowns.Load())
	}
	if err := processor.Shutdown(context.Background()); err != nil {
		t.Fatalf("terminal Shutdown() error = %v", err)
	}
}

func TestRuntimeShutdownContinuesAfterCallerCancellation(t *testing.T) {
	exporter := &lifecycleSpanExporter{
		exportStarted: make(chan struct{}),
		releaseExport: make(chan struct{}),
	}
	config := validConfig()
	config.ExportQueueSize = 1
	config.TraceSampleRatio = 1
	runtime, err := New(context.Background(), config, WithSpanExporter(exporter))
	if err != nil {
		t.Fatal(err)
	}
	_, span := runtime.Start(context.Background(), OperationEvaluation, TransportService)
	runtime.Finish(span, SpanStatusOK)
	requireSignal(t, exporter.exportStarted, "span export did not start")

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runtime.Shutdown(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Shutdown() error = %v, want cancellation", err)
	}
	close(exporter.releaseExport)
	wait, stopWaiting := context.WithTimeout(context.Background(), 2*time.Second)
	defer stopWaiting()
	if err := runtime.Shutdown(wait); err != nil {
		t.Fatalf("later Shutdown() error = %v", err)
	}
	if exporter.shutdowns.Load() != 1 {
		t.Fatalf("exporter Shutdown calls = %d, want 1", exporter.shutdowns.Load())
	}
}

func TestSpanProcessorShutdownCallersWaitIndependently(t *testing.T) {
	exporter := &lifecycleSpanExporter{
		shutdownStarted: make(chan struct{}),
		releaseShutdown: make(chan struct{}),
		shutdownErr:     errTerminalSpanShutdown,
	}
	processor := newBoundedSpanProcessor(exporter, &internaltelemetry.Counters{}, 1, time.Hour)
	first := make(chan error, 1)
	go func() { first <- processor.Shutdown(context.Background()) }()
	requireSignal(t, exporter.shutdownStarted, "exporter shutdown did not start")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	second := make(chan error, 1)
	go func() { second <- processor.Shutdown(ctx) }()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("second Shutdown() error = %v, want cancellation", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(exporter.releaseShutdown)
		<-first
		t.Fatal("second Shutdown blocked behind first caller")
	}

	close(exporter.releaseShutdown)
	if err := <-first; !errors.Is(err, errTerminalSpanShutdown) {
		t.Fatalf("first Shutdown() error = %v, want terminal exporter error", err)
	}
	if err := processor.Shutdown(context.Background()); !errors.Is(err, errTerminalSpanShutdown) {
		t.Fatalf("later Shutdown() error = %v, want terminal exporter error", err)
	}
	if exporter.shutdowns.Load() != 1 {
		t.Fatalf("exporter Shutdown calls = %d, want 1", exporter.shutdowns.Load())
	}
}

type blockingSpanContext struct {
	sdktrace.ReadOnlySpan
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (span *blockingSpanContext) SpanContext() trace.SpanContext {
	span.once.Do(func() { close(span.entered) })
	<-span.release
	return span.ReadOnlySpan.SpanContext()
}

func TestSpanProcessorRejectsOnEndRacingFinalDrain(t *testing.T) {
	exporter := &lifecycleSpanExporter{}
	counters := &internaltelemetry.Counters{}
	processor := newBoundedSpanProcessor(exporter, counters, 1, time.Hour)
	span := &blockingSpanContext{
		ReadOnlySpan: sampledReadOnlySpan(),
		entered:      make(chan struct{}),
		release:      make(chan struct{}),
	}
	ended := make(chan struct{})
	go func() {
		processor.OnEnd(span)
		close(ended)
	}()
	requireSignal(t, span.entered, "OnEnd did not reach SpanContext")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := processor.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	close(span.release)
	requireSignal(t, ended, "OnEnd did not return")
	if len(processor.queue) != 0 || exporter.exports.Load() != 0 || counters.Snapshot().ExportDrops != 1 {
		t.Fatalf("late span state = queue %d, exports %d, drops %d",
			len(processor.queue), exporter.exports.Load(), counters.Snapshot().ExportDrops)
	}
}

func TestSpanProcessorOnEndDoesNotAllocate(t *testing.T) {
	processor := &boundedSpanProcessor{
		counters: &internaltelemetry.Counters{},
		queue:    make(chan sdktrace.ReadOnlySpan, 1),
	}
	span := sampledReadOnlySpan()
	if allocations := testing.AllocsPerRun(100, func() {
		select {
		case <-processor.queue:
		default:
		}
		processor.OnEnd(span)
	}); allocations != 0 {
		t.Fatalf("OnEnd allocations = %.2f, want 0", allocations)
	}
}

func sampledReadOnlySpan() sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
	})}.Snapshot()
}

func requireSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatal(message)
	}
}
