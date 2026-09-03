package telemetry

import (
	"context"

	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

type countingMetricExporter struct {
	sdkmetric.Exporter
	counters *internaltelemetry.Counters
}

func newCountingMetricExporter(exporter sdkmetric.Exporter, counters *internaltelemetry.Counters) *countingMetricExporter {
	return &countingMetricExporter{Exporter: exporter, counters: counters}
}

func (exporter *countingMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	err := exporter.Exporter.Export(ctx, metrics)
	if err != nil {
		exporter.counters.AddExportDrop(1)
	}
	return err
}
