// Package telemetry exposes optional bounded production telemetry.
package telemetry

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/sebishogun/nornrune/internal/result"
	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type Runtime struct {
	propagator     propagation.TextMapPropagator
	shutdownErr    error
	counters       *internaltelemetry.Counters
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	spanProcessor  *boundedSpanProcessor
	shutdownDone   chan struct{}
	shutdownOnce   sync.Once
}

func New(ctx context.Context, config Config, runtimeOptions ...Option) (*Runtime, error) {
	if ctx == nil || !config.valid() {
		return nil, ErrInvalidConfig
	}
	runtime := &Runtime{propagator: propagation.TraceContext{}, shutdownDone: make(chan struct{})}
	if !config.Enabled {
		return runtime, nil
	}
	var configured options
	for _, option := range runtimeOptions {
		if option == nil || option(&configured) != nil {
			return nil, ErrInvalidConfig
		}
	}
	if config.Endpoint != "" && configured.metricReader != nil {
		return nil, ErrInvalidConfig
	}
	runtime.counters = &internaltelemetry.Counters{}
	resourceValue := resource.NewSchemaless(
		attribute.String("service.name", "nornrune"),
		attribute.String("service.version", config.ServiceVersion),
		attribute.String("nornrune.build.version", config.BuildVersion),
	)
	meterOptions := []sdkmetric.Option{sdkmetric.WithResource(resourceValue)}
	if configured.metricReader != nil {
		meterOptions = append(meterOptions, sdkmetric.WithReader(configured.metricReader))
	}
	if config.Endpoint != "" {
		exporter, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(signalEndpoint(config.Endpoint, "metrics")))
		if err != nil {
			return nil, errors.Join(ErrInvalidConfig, err)
		}
		meterOptions = append(meterOptions, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			newCountingMetricExporter(exporter, runtime.counters), sdkmetric.WithInterval(config.ExportInterval),
		)))
	}
	if len(meterOptions) > 1 {
		runtime.meterProvider = sdkmetric.NewMeterProvider(meterOptions...)
		if err := registerMetrics(runtime.meterProvider, runtime.counters, config.QueueDepth); err != nil {
			_ = runtime.meterProvider.Shutdown(ctx)
			return nil, err
		}
	}
	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(resourceValue),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(config.TraceSampleRatio))),
	}
	spanExporter := configured.spanExporter
	if config.Endpoint != "" {
		if spanExporter != nil {
			if runtime.meterProvider != nil {
				_ = runtime.meterProvider.Shutdown(ctx)
			}
			return nil, ErrInvalidConfig
		}
		exporter, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(signalEndpoint(config.Endpoint, "traces")))
		if err != nil {
			if runtime.meterProvider != nil {
				_ = runtime.meterProvider.Shutdown(ctx)
			}
			return nil, errors.Join(ErrInvalidConfig, err)
		}
		spanExporter = exporter
	}
	if spanExporter != nil {
		runtime.spanProcessor = newBoundedSpanProcessor(
			spanExporter, runtime.counters, config.ExportQueueSize, config.ExportInterval,
		)
		traceOptions = append(traceOptions, sdktrace.WithSpanProcessor(runtime.spanProcessor))
		runtime.tracerProvider = sdktrace.NewTracerProvider(traceOptions...)
	}
	return runtime, nil
}

func (runtime *Runtime) ForceFlush(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return ErrInvalidConfig
	}
	before := uint64(0)
	if runtime.counters != nil {
		before = runtime.counters.Snapshot().ExportDrops
	}
	var meterErr, traceErr error
	if runtime.meterProvider != nil {
		meterErr = runtime.meterProvider.ForceFlush(ctx)
	}
	if runtime.tracerProvider != nil {
		traceErr = runtime.tracerProvider.ForceFlush(ctx)
	}
	err := errors.Join(meterErr, traceErr)
	if err != nil && runtime.counters != nil && runtime.counters.Snapshot().ExportDrops == before {
		runtime.counters.AddExportDrop(1)
	}
	return err
}

func (runtime *Runtime) Record(delta BatchDelta) error {
	if runtime == nil {
		return ErrInvalidConfig
	}
	if runtime.counters == nil {
		return nil
	}
	return runtime.counters.Add(delta)
}

func (runtime *Runtime) ObserveEvaluation(
	batch *result.Batch,
	outcomes internaltelemetry.OutcomeIDs,
	duration time.Duration,
) error {
	if runtime == nil {
		return ErrInvalidConfig
	}
	if runtime.counters == nil {
		return nil
	}
	return internaltelemetry.ObserveBatch(runtime.counters, batch, outcomes, duration)
}

func (runtime *Runtime) AdmissionStarted(queueWait time.Duration) error {
	if runtime == nil {
		return ErrInvalidConfig
	}
	if runtime.counters == nil {
		return nil
	}
	return runtime.counters.ObserveAdmission(queueWait)
}

func (runtime *Runtime) AdmissionFinished() {
	if runtime != nil && runtime.counters != nil {
		runtime.counters.AdmissionFinished()
	}
}

func (runtime *Runtime) Snapshot() Snapshot {
	if runtime == nil || runtime.counters == nil {
		return Snapshot{}
	}
	return runtime.counters.Snapshot()
}

func (runtime *Runtime) Shutdown(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return ErrInvalidConfig
	}
	runtime.shutdownOnce.Do(func() {
		if runtime.spanProcessor != nil {
			runtime.spanProcessor.beginShutdown()
		}
		go runtime.shutdown()
	})
	select {
	case <-runtime.shutdownDone:
		return runtime.shutdownErr
	default:
	}
	select {
	case <-runtime.shutdownDone:
		return runtime.shutdownErr
	case <-ctx.Done():
		select {
		case <-runtime.shutdownDone:
			return runtime.shutdownErr
		default:
			return ctx.Err()
		}
	}
}

func (runtime *Runtime) shutdown() {
	var traceErr error
	if runtime.tracerProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), spanExportTimeout)
		traceErr = runtime.tracerProvider.Shutdown(ctx)
		cancel()
	}
	if traceErr != nil && runtime.counters != nil {
		runtime.counters.AddShutdownFailure()
	}
	var meterErr error
	if runtime.meterProvider != nil {
		ctx, cancel := context.WithTimeout(context.Background(), spanExportTimeout)
		meterErr = runtime.meterProvider.Shutdown(ctx)
		cancel()
	}
	if meterErr != nil && traceErr == nil && runtime.counters != nil {
		runtime.counters.AddShutdownFailure()
	}
	runtime.shutdownErr = errors.Join(meterErr, traceErr)
	close(runtime.shutdownDone)
}

// The OTel SDK sum aggregator converts int64 through float64 when loading it.
// Cap at the largest representable value below 2^63 so that conversion cannot wrap.
const maxOTelInt64Sum = int64(math.MaxInt64 - (1<<10 - 1))

func clampMetric(value uint64) int64 {
	if value > uint64(maxOTelInt64Sum) {
		return maxOTelInt64Sum
	}
	return int64(value)
}
