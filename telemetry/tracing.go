package telemetry

import (
	"context"
	"math/bits"

	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Operation uint8

const (
	OperationAdmission Operation = iota
	OperationDecode
	OperationPolicyLookup
	OperationEvaluation
	OperationAuditAcknowledgment
	OperationResponseEncode
	OperationCount
)

type Transport uint8

const (
	TransportHTTP Transport = iota
	TransportGRPC
	TransportService
	TransportCount
)

type SpanStatus uint8

const (
	SpanStatusOK SpanStatus = iota
	SpanStatusInvalidArgument
	SpanStatusNotFound
	SpanStatusResourceExhausted
	SpanStatusCanceled
	SpanStatusDeadlineExceeded
	SpanStatusUnavailable
	SpanStatusInternal
	SpanStatusCount
)

var operationNames = [OperationCount]string{
	"admission", "decode", "policy_lookup", "evaluation", "audit_acknowledgment", "response_encode",
}

var (
	transportNames  = [TransportCount]string{"http", "grpc", "service"}
	spanStatusNames = [SpanStatusCount]string{
		"ok", "invalid_argument", "not_found", "resource_exhausted", "canceled", "deadline_exceeded", "unavailable", "internal",
	}
	operationAttributes  = fixedAttributes("nornrune.operation", operationNames[:])
	transportAttributes  = fixedAttributes("nornrune.transport", transportNames[:])
	spanStatusAttributes = fixedAttributes("nornrune.status", spanStatusNames[:])
	decisionAttributes   = [...]attribute.KeyValue{
		attribute.String("nornrune.decision", "approve"),
		attribute.String("nornrune.decision", "reject"),
		attribute.String("nornrune.decision", "revise"),
		attribute.String("nornrune.decision", "escalate"),
		attribute.String("nornrune.decision", "mixed"),
	}
	reasonAttributes = [...]attribute.KeyValue{
		attribute.String("nornrune.reason", "missing"),
		attribute.String("nornrune.reason", "stale"),
		attribute.String("nornrune.reason", "unclear"),
		attribute.String("nornrune.reason", "unverifiable"),
		attribute.String("nornrune.reason", "wrong_scope"),
		attribute.String("nornrune.reason", "wrong_subject"),
		attribute.String("nornrune.reason", "wrong_timing"),
		attribute.String("nornrune.reason", "invalid"),
		attribute.String("nornrune.reason", "conflict"),
		attribute.String("nornrune.reason", "multiple"),
	}
)

// noopSpan is constructed once so disabled and counters-only runtimes pay no
// allocation when spans are requested. Start returns the caller's context
// unwrapped in that mode; there is no span to record or propagate, and any
// extracted remote parent stays in the context.
var noopSpan = trace.SpanFromContext(context.Background())

func OperationName(operation Operation) (string, bool) {
	if operation >= OperationCount {
		return "", false
	}
	return operationNames[operation], true
}

func TransportName(transport Transport) (string, bool) {
	if transport >= TransportCount {
		return "", false
	}
	return transportNames[transport], true
}

func SpanStatusName(status SpanStatus) (string, bool) {
	if status >= SpanStatusCount {
		return "", false
	}
	return spanStatusNames[status], true
}

func (runtime *Runtime) Start(ctx context.Context, operation Operation, transport Transport) (context.Context, trace.Span) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime == nil || runtime.tracerProvider == nil {
		return ctx, noopSpan
	}
	name, ok := OperationName(operation)
	if !ok {
		return ctx, noopSpan
	}
	if _, ok := TransportName(transport); !ok {
		return ctx, noopSpan
	}
	return runtime.tracerProvider.Tracer("github.com/sebishogun/nornrune/telemetry").Start(
		ctx,
		"nornrune."+name,
		trace.WithAttributes(operationAttributes[operation], transportAttributes[transport]),
	)
}

func (runtime *Runtime) Finish(span trace.Span, status SpanStatus) {
	if runtime == nil || runtime.tracerProvider == nil || span == nil {
		return
	}
	if status >= SpanStatusCount {
		status = SpanStatusInternal
	}
	span.SetAttributes(spanStatusAttributes[status])
	if status == SpanStatusOK {
		span.SetStatus(codes.Ok, "")
	} else {
		span.SetStatus(codes.Error, spanStatusNames[status])
	}
	span.End()
}

func (runtime *Runtime) annotateEvaluation(ctx context.Context, summary internaltelemetry.BatchSummary) {
	if runtime == nil || runtime.tracerProvider == nil || ctx == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}
	if mask := summary.DecisionMask; mask != 0 {
		index := bits.TrailingZeros8(mask)
		if mask&(mask-1) != 0 {
			index = int(internaltelemetry.DecisionCount)
		}
		span.SetAttributes(decisionAttributes[index])
	}
	if mask := summary.ReasonMask; mask != 0 {
		index := bits.TrailingZeros16(mask)
		if mask&(mask-1) != 0 {
			index = int(internaltelemetry.ReasonCount)
		}
		span.SetAttributes(reasonAttributes[index])
	}
}

func fixedAttributes(key string, values []string) []attribute.KeyValue {
	attributes := make([]attribute.KeyValue, len(values))
	for index, value := range values {
		attributes[index] = attribute.String(key, value)
	}
	return attributes
}

func (runtime *Runtime) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime == nil || runtime.propagator == nil || carrier == nil {
		return ctx
	}
	return runtime.propagator.Extract(ctx, carrier)
}

func (runtime *Runtime) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	if runtime != nil && runtime.propagator != nil && ctx != nil && carrier != nil {
		runtime.propagator.Inject(ctx, carrier)
	}
}

func (runtime *Runtime) TracerProvider() trace.TracerProvider {
	if runtime == nil || runtime.tracerProvider == nil {
		return trace.NewNoopTracerProvider()
	}
	return runtime.tracerProvider
}

func (runtime *Runtime) Propagator() propagation.TextMapPropagator {
	if runtime == nil || runtime.propagator == nil {
		return propagation.TraceContext{}
	}
	return runtime.propagator
}
