// Package telemetry exposes optional bounded production telemetry.
package telemetry

import (
	"context"
	"errors"
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
	counters       *internaltelemetry.Counters
	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider
	propagator     propagation.TextMapPropagator
	shutdownOnce   sync.Once
	shutdownErr    error
}

func New(ctx context.Context, config Config, runtimeOptions ...Option) (*Runtime, error) {
	if ctx == nil || !config.valid() {
		return nil, ErrInvalidConfig
	}
	runtime := &Runtime{propagator: propagation.TraceContext{}}
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
			exporter, sdkmetric.WithInterval(config.ExportInterval),
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
		traceOptions = append(traceOptions, sdktrace.WithBatcher(spanExporter,
			sdktrace.WithMaxQueueSize(int(config.ExportQueueSize)),
			sdktrace.WithBatchTimeout(config.ExportInterval),
		))
		runtime.tracerProvider = sdktrace.NewTracerProvider(traceOptions...)
	}
	return runtime, nil
}

func (runtime *Runtime) ForceFlush(ctx context.Context) error {
	if runtime == nil || ctx == nil {
		return ErrInvalidConfig
	}
	var meterErr, traceErr error
	if runtime.meterProvider != nil {
		meterErr = runtime.meterProvider.ForceFlush(ctx)
	}
	if runtime.tracerProvider != nil {
		traceErr = runtime.tracerProvider.ForceFlush(ctx)
	}
	if err := errors.Join(meterErr, traceErr); err != nil {
		if runtime.counters != nil {
			runtime.counters.AddExportDrop(1)
		}
		return err
	}
	return nil
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
		var meterErr, traceErr error
		if runtime.meterProvider != nil {
			meterErr = runtime.meterProvider.Shutdown(ctx)
		}
		if runtime.tracerProvider != nil {
			traceErr = runtime.tracerProvider.Shutdown(ctx)
		}
		runtime.shutdownErr = errors.Join(meterErr, traceErr)
		if runtime.shutdownErr != nil && runtime.counters != nil {
			runtime.counters.AddShutdownFailure()
		}
	})
	return runtime.shutdownErr
}

func clampMetric(value uint64) int64 {
	if value > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1)
	}
	return int64(value)
}
