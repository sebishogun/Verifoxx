package telemetry

import (
	"errors"
	"net/url"
	"strings"
	"time"

	internaltelemetry "github.com/sebishogun/nornrune/internal/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var ErrInvalidConfig = errors.New("telemetry: invalid configuration")

const (
	DecisionApprove  = internaltelemetry.DecisionApprove
	DecisionReject   = internaltelemetry.DecisionReject
	DecisionRevise   = internaltelemetry.DecisionRevise
	DecisionEscalate = internaltelemetry.DecisionEscalate
	DecisionCount    = internaltelemetry.DecisionCount

	ReasonMissing      = internaltelemetry.ReasonMissing
	ReasonStale        = internaltelemetry.ReasonStale
	ReasonUnclear      = internaltelemetry.ReasonUnclear
	ReasonUnverifiable = internaltelemetry.ReasonUnverifiable
	ReasonWrongScope   = internaltelemetry.ReasonWrongScope
	ReasonWrongSubject = internaltelemetry.ReasonWrongSubject
	ReasonWrongTiming  = internaltelemetry.ReasonWrongTiming
	ReasonInvalid      = internaltelemetry.ReasonInvalid
	ReasonConflict     = internaltelemetry.ReasonConflict
	ReasonCount        = internaltelemetry.ReasonCount

	AuditPersisted       = internaltelemetry.AuditPersisted
	AuditOptionalDrop    = internaltelemetry.AuditOptionalDrop
	AuditRequiredFailure = internaltelemetry.AuditRequiredFailure
	AuditOutcomeCount    = internaltelemetry.AuditOutcomeCount

	ReloadSuccess            = internaltelemetry.ReloadSuccess
	ReloadInvalid            = internaltelemetry.ReloadInvalid
	ReloadPersistenceFailure = internaltelemetry.ReloadPersistenceFailure
	ReloadOutcomeCount       = internaltelemetry.ReloadOutcomeCount
	LatencyBucketCount       = internaltelemetry.LatencyBucketCount
	QueueBucketCount         = internaltelemetry.QueueBucketCount
)

func DecisionName(value Decision) (string, bool) { return internaltelemetry.DecisionName(value) }
func ReasonName(value Reason) (string, bool)     { return internaltelemetry.ReasonName(value) }
func AuditOutcomeName(value AuditOutcome) (string, bool) {
	return internaltelemetry.AuditOutcomeName(value)
}
func ReloadOutcomeName(value ReloadOutcome) (string, bool) {
	return internaltelemetry.ReloadOutcomeName(value)
}

type (
	Decision      = internaltelemetry.Decision
	Reason        = internaltelemetry.Reason
	AuditOutcome  = internaltelemetry.AuditOutcome
	ReloadOutcome = internaltelemetry.ReloadOutcome
	BatchDelta    = internaltelemetry.BatchDelta
	Snapshot      = internaltelemetry.Snapshot
)

type Config struct {
	Endpoint         string
	ServiceVersion   string
	BuildVersion     string
	ExportInterval   time.Duration
	TraceSampleRatio float64
	ExportQueueSize  uint32
	QueueDepth       func() uint64
	Enabled          bool
}

type options struct {
	metricReader sdkmetric.Reader
	spanExporter sdktrace.SpanExporter
}

type Option func(*options) error

func WithMetricReader(reader sdkmetric.Reader) Option {
	return func(options *options) error {
		if options == nil || reader == nil || options.metricReader != nil {
			return ErrInvalidConfig
		}
		options.metricReader = reader
		return nil
	}
}

func WithSpanExporter(exporter sdktrace.SpanExporter) Option {
	return func(options *options) error {
		if options == nil || exporter == nil || options.spanExporter != nil {
			return ErrInvalidConfig
		}
		options.spanExporter = exporter
		return nil
	}
}

func (config Config) valid() bool {
	if !config.Enabled {
		return config.Endpoint == "" && config.ServiceVersion == "" && config.BuildVersion == "" &&
			config.ExportInterval == 0 && config.TraceSampleRatio == 0 && config.ExportQueueSize == 0 &&
			config.QueueDepth == nil
	}
	if !validToken(config.ServiceVersion) || !validToken(config.BuildVersion) ||
		config.ExportInterval < 100*time.Millisecond || config.ExportInterval > time.Hour ||
		config.ExportQueueSize == 0 || config.ExportQueueSize > 1<<16 ||
		config.TraceSampleRatio < 0 || config.TraceSampleRatio > 1 {
		return false
	}
	if config.Endpoint == "" {
		return true
	}
	if len(config.Endpoint) > 2048 {
		return false
	}
	parsed, err := url.Parse(config.Endpoint)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func validToken(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && strings.IndexByte(value, 0) < 0
}

func DurationBucketBounds() [LatencyBucketCount - 1]time.Duration {
	return internaltelemetry.DurationBucketBounds()
}

func signalEndpoint(endpoint, signal string) string {
	parsed, _ := url.Parse(endpoint)
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/v1/" + signal
	return parsed.String()
}
