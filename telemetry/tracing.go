package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
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

var operationNames = [OperationCount]string{
	"admission", "decode", "policy_lookup", "evaluation", "audit_acknowledgment", "response_encode",
}

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

func (runtime *Runtime) Start(ctx context.Context, operation Operation) (context.Context, trace.Span) {
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
	return runtime.tracerProvider.Tracer("github.com/sebishogun/nornrune/telemetry").Start(
		ctx,
		"nornrune."+name,
		trace.WithAttributes(attribute.String("nornrune.operation", name)),
	)
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
