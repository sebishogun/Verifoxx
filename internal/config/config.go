// Package config loads and validates service configuration without coupling
// operational settings to transport adapters.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/security"
)

var ErrInvalidConfig = errors.New("config: invalid configuration")

const RedactedSecret = security.RedactedValue

const (
	MaxWorkers           = 256
	MaxQueueDepth        = 4096
	MaxBatchRows  uint32 = security.MaximumBatchRows
	MaxBodyBytes  int64  = security.MaximumRequestBytes

	maxDatabaseConnections = 1024
	maxDatabaseURLBytes    = 4096
	maxConfigFileBytes     = 64 << 10
	maxOperationalTimeout  = security.MaximumRequestTimeout
)

// Config is the validated cold-path configuration shared by service adapters.
type Config struct {
	HTTPAddress            string
	GRPCAddress            string
	PolicyName             string
	DatabaseURL            SecretURL
	OTelEndpoint           string
	RequestTimeout         time.Duration
	ShutdownTimeout        time.Duration
	AuditWriteTimeout      time.Duration
	DatabaseConnectTimeout time.Duration
	MaxBodyBytes           int64
	Workers                int
	QueueDepth             int
	AuditWriters           int
	AuditQueueDepth        int
	DatabaseMinConnections int
	DatabaseMaxConnections int
	MaxBatchRows           uint32
	AuditMode              persistence.AuditMode

	TelemetryEnabled        bool
	TraceSampleRatio        float64
	TelemetryExportInterval time.Duration
	TelemetryQueueSize      int
}

// SecretURL keeps database credentials available only through an explicit
// Reveal call. Formatting and serialization expose a stable redacted marker.
type SecretURL string

// Reveal returns the database URL for a connection constructor.
func (value SecretURL) Reveal() string { return string(value) }

// Empty reports whether no database URL was configured.
func (value SecretURL) Empty() bool { return value == "" }

func (value SecretURL) String() string {
	if value.Empty() {
		return ""
	}
	return RedactedSecret
}

func (value SecretURL) GoString() string { return value.String() }

// LogValue prevents structured slog output from reflecting the underlying string.
func (value SecretURL) LogValue() slog.Value { return slog.StringValue(value.String()) }

// MarshalJSON prevents JSON diagnostics from exposing credentials.
func (value SecretURL) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.String())
}

// MarshalText prevents text-based diagnostics from exposing credentials.
func (value SecretURL) MarshalText() ([]byte, error) {
	return []byte(value.String()), nil
}

// Default returns a complete standalone configuration. Database-backed modes
// remain disabled until a database URL and audit mode are selected explicitly.
func Default() Config {
	workers := min(runtime.GOMAXPROCS(0), MaxWorkers)
	if workers < 1 {
		workers = 1
	}
	return Config{
		HTTPAddress:             "127.0.0.1:8080",
		GRPCAddress:             "127.0.0.1:9090",
		PolicyName:              "nornrune",
		RequestTimeout:          30 * time.Second,
		ShutdownTimeout:         30 * time.Second,
		AuditWriteTimeout:       5 * time.Second,
		DatabaseConnectTimeout:  5 * time.Second,
		MaxBodyBytes:            8 << 20,
		Workers:                 workers,
		QueueDepth:              min(workers*2, MaxQueueDepth),
		AuditWriters:            1,
		AuditQueueDepth:         64,
		DatabaseMinConnections:  0,
		DatabaseMaxConnections:  16,
		MaxBatchRows:            64 << 10,
		AuditMode:               persistence.AuditOff,
		TelemetryEnabled:        false,
		OTelEndpoint:            "",
		TraceSampleRatio:        0.1,
		TelemetryExportInterval: 10 * time.Second,
		TelemetryQueueSize:      2048,
	}
}

// Validate rejects values that could create unbounded service storage or an
// unverifiable runtime dependency.
func (config Config) Validate() error {
	if !validAddress(config.HTTPAddress) || !validAddress(config.GRPCAddress) {
		return fmt.Errorf("%w: listen address", ErrInvalidConfig)
	}
	if trimmed := strings.TrimSpace(config.PolicyName); trimmed == "" || trimmed != config.PolicyName || len(trimmed) > 128 {
		return fmt.Errorf("%w: policy name", ErrInvalidConfig)
	}
	if !config.DatabaseURL.Empty() &&
		(len(config.DatabaseURL.Reveal()) > maxDatabaseURLBytes || !validDatabaseURL(config.DatabaseURL.Reveal())) {
		return fmt.Errorf("%w: database URL", ErrInvalidConfig)
	}
	if config.Workers < 1 || config.Workers > MaxWorkers ||
		config.QueueDepth < 1 || config.QueueDepth > MaxQueueDepth {
		return fmt.Errorf("%w: worker limits", ErrInvalidConfig)
	}
	if config.MaxBatchRows < 1 || config.MaxBatchRows > MaxBatchRows ||
		config.MaxBodyBytes < 1 || config.MaxBodyBytes > MaxBodyBytes {
		return fmt.Errorf("%w: request limits", ErrInvalidConfig)
	}
	if !validTimeout(config.RequestTimeout) || !validTimeout(config.ShutdownTimeout) ||
		!validTimeout(config.AuditWriteTimeout) || !validTimeout(config.DatabaseConnectTimeout) {
		return fmt.Errorf("%w: timeout", ErrInvalidConfig)
	}
	if config.AuditMode != persistence.AuditOff && config.AuditWriteTimeout >= config.ShutdownTimeout {
		return fmt.Errorf("%w: shutdown flush budget", ErrInvalidConfig)
	}
	if !config.AuditMode.Valid() || config.AuditWriters < 1 || config.AuditWriters > MaxWorkers ||
		config.AuditQueueDepth < 1 || config.AuditQueueDepth > MaxQueueDepth ||
		config.AuditWriters > config.AuditQueueDepth {
		return fmt.Errorf("%w: audit settings", ErrInvalidConfig)
	}
	if config.AuditMode != persistence.AuditOff && config.DatabaseURL.Empty() {
		return fmt.Errorf("%w: audit database", ErrInvalidConfig)
	}
	if config.DatabaseMinConnections < 0 || config.DatabaseMaxConnections < 1 ||
		config.DatabaseMaxConnections > maxDatabaseConnections ||
		config.DatabaseMinConnections > config.DatabaseMaxConnections {
		return fmt.Errorf("%w: database connections", ErrInvalidConfig)
	}
	if !validTelemetry(config) {
		return fmt.Errorf("%w: telemetry settings", ErrInvalidConfig)
	}
	return nil
}

func validTelemetry(config Config) bool {
	if config.OTelEndpoint != "" && !validOTelEndpoint(config.OTelEndpoint) {
		return false
	}
	if !config.TelemetryEnabled {
		return true
	}
	return config.TraceSampleRatio >= 0 && config.TraceSampleRatio <= 1 &&
		config.TelemetryExportInterval >= 100*time.Millisecond && config.TelemetryExportInterval <= time.Hour &&
		config.TelemetryQueueSize >= 1 && config.TelemetryQueueSize <= 1<<16
}

func validOTelEndpoint(endpoint string) bool {
	if len(endpoint) > 2048 {
		return false
	}
	parsed, err := url.Parse(endpoint)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

// Load applies defaults, an optional strict JSON file, environment values, and
// flags in increasing precedence order.
func Load(arguments []string, lookup LookupEnv) (Config, error) {
	config := Default()
	path, err := resolveConfigPath(arguments, lookup)
	if err != nil {
		return Config{}, err
	}
	if path != "" {
		if err := applyFile(&config, path); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnvironment(&config, lookup); err != nil {
		return Config{}, err
	}
	if err := applyFlags(&config, arguments); err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

type fileConfig struct {
	HTTPAddress             *string  `json:"http_address"`
	GRPCAddress             *string  `json:"grpc_address"`
	PolicyName              *string  `json:"policy_name"`
	DatabaseURL             *string  `json:"database_url"`
	RequestTimeout          *string  `json:"request_timeout"`
	ShutdownTimeout         *string  `json:"shutdown_timeout"`
	AuditMode               *string  `json:"audit_mode"`
	AuditWriteTimeout       *string  `json:"audit_write_timeout"`
	DatabaseConnectTimeout  *string  `json:"database_connect_timeout"`
	MaxBodyBytes            *int64   `json:"max_body_bytes"`
	Workers                 *int     `json:"workers"`
	QueueDepth              *int     `json:"queue_depth"`
	AuditWriters            *int     `json:"audit_writers"`
	AuditQueueDepth         *int     `json:"audit_queue_depth"`
	DatabaseMinConnections  *int     `json:"database_min_connections"`
	DatabaseMaxConnections  *int     `json:"database_max_connections"`
	MaxBatchRows            *uint32  `json:"max_batch_rows"`
	TelemetryEnabled        *bool    `json:"telemetry_enabled"`
	OTelEndpoint            *string  `json:"otel_endpoint"`
	TraceSampleRatio        *float64 `json:"trace_sample_ratio"`
	TelemetryExportInterval *string  `json:"telemetry_export_interval"`
	TelemetryQueueSize      *int     `json:"telemetry_queue_size"`
}

func applyFile(config *Config, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("%w: read configuration file: %v", ErrInvalidConfig, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigFileBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read configuration file: %v", ErrInvalidConfig, err)
	}
	if len(data) > maxConfigFileBytes {
		return fmt.Errorf("%w: configuration file exceeds %d bytes", ErrInvalidConfig, maxConfigFileBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var values fileConfig
	if err := decoder.Decode(&values); err != nil {
		return fmt.Errorf("%w: decode configuration file: %v", ErrInvalidConfig, err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return err
	}
	return values.apply(config)
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing configuration value", ErrInvalidConfig)
		}
		return fmt.Errorf("%w: trailing configuration data: %v", ErrInvalidConfig, err)
	}
	return nil
}

func (values fileConfig) apply(config *Config) error {
	assignString(&config.HTTPAddress, values.HTTPAddress)
	assignString(&config.GRPCAddress, values.GRPCAddress)
	assignString(&config.PolicyName, values.PolicyName)
	if values.DatabaseURL != nil {
		config.DatabaseURL = SecretURL(*values.DatabaseURL)
	}
	assignInt64(&config.MaxBodyBytes, values.MaxBodyBytes)
	assignInt(&config.Workers, values.Workers)
	assignInt(&config.QueueDepth, values.QueueDepth)
	assignInt(&config.AuditWriters, values.AuditWriters)
	assignInt(&config.AuditQueueDepth, values.AuditQueueDepth)
	assignInt(&config.DatabaseMinConnections, values.DatabaseMinConnections)
	assignInt(&config.DatabaseMaxConnections, values.DatabaseMaxConnections)
	if values.MaxBatchRows != nil {
		config.MaxBatchRows = *values.MaxBatchRows
	}
	var err error
	if config.RequestTimeout, err = fileDuration(config.RequestTimeout, values.RequestTimeout, "request timeout"); err != nil {
		return err
	}
	if config.ShutdownTimeout, err = fileDuration(config.ShutdownTimeout, values.ShutdownTimeout, "shutdown timeout"); err != nil {
		return err
	}
	if config.AuditWriteTimeout, err = fileDuration(config.AuditWriteTimeout, values.AuditWriteTimeout, "audit write timeout"); err != nil {
		return err
	}
	if config.DatabaseConnectTimeout, err = fileDuration(config.DatabaseConnectTimeout, values.DatabaseConnectTimeout, "database connect timeout"); err != nil {
		return err
	}
	if values.AuditMode != nil {
		config.AuditMode, err = parseAuditMode(*values.AuditMode)
		if err != nil {
			return fmt.Errorf("%w: file audit mode", ErrInvalidConfig)
		}
	}
	if values.TelemetryEnabled != nil {
		config.TelemetryEnabled = *values.TelemetryEnabled
	}
	assignString(&config.OTelEndpoint, values.OTelEndpoint)
	if values.TraceSampleRatio != nil {
		config.TraceSampleRatio = *values.TraceSampleRatio
	}
	if values.TelemetryQueueSize != nil {
		config.TelemetryQueueSize = *values.TelemetryQueueSize
	}
	if config.TelemetryExportInterval, err = fileDuration(
		config.TelemetryExportInterval, values.TelemetryExportInterval, "telemetry export interval",
	); err != nil {
		return err
	}
	return nil
}

func assignString(destination *string, source *string) {
	if source != nil {
		*destination = *source
	}
}

func assignInt(destination *int, source *int) {
	if source != nil {
		*destination = *source
	}
}

func assignInt64(destination *int64, source *int64) {
	if source != nil {
		*destination = *source
	}
}

func fileDuration(current time.Duration, source *string, name string) (time.Duration, error) {
	if source == nil {
		return current, nil
	}
	value, err := time.ParseDuration(*source)
	if err != nil {
		return 0, fmt.Errorf("%w: file %s", ErrInvalidConfig, name)
	}
	return value, nil
}

func applyFlags(config *Config, arguments []string) error {
	flags := flag.NewFlagSet("nornrune", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var ignoredPath string
	auditMode := auditModeString(config.AuditMode)
	maxBatchRows := uint64(config.MaxBatchRows)
	flags.StringVar(&ignoredPath, "config", "", "configuration file")
	flags.StringVar(&config.HTTPAddress, "http-address", config.HTTPAddress, "HTTP listen address")
	flags.StringVar(&config.GRPCAddress, "grpc-address", config.GRPCAddress, "gRPC listen address")
	flags.StringVar(&config.PolicyName, "policy-name", config.PolicyName, "active policy name")
	databaseURL := config.DatabaseURL.Reveal()
	flags.StringVar(&databaseURL, "database-url", databaseURL, "PostgreSQL URL")
	flags.IntVar(&config.Workers, "workers", config.Workers, "evaluation workers")
	flags.IntVar(&config.QueueDepth, "queue-depth", config.QueueDepth, "admission depth")
	flags.Uint64Var(&maxBatchRows, "max-batch-rows", maxBatchRows, "maximum rows per batch")
	flags.Int64Var(&config.MaxBodyBytes, "max-body-bytes", config.MaxBodyBytes, "maximum request body bytes")
	flags.DurationVar(&config.RequestTimeout, "request-timeout", config.RequestTimeout, "request timeout")
	flags.DurationVar(&config.ShutdownTimeout, "shutdown-timeout", config.ShutdownTimeout, "shutdown timeout")
	flags.StringVar(&auditMode, "audit-mode", auditMode, "audit mode")
	flags.IntVar(&config.AuditWriters, "audit-writers", config.AuditWriters, "audit writers")
	flags.IntVar(&config.AuditQueueDepth, "audit-queue-depth", config.AuditQueueDepth, "audit queue depth")
	flags.DurationVar(&config.AuditWriteTimeout, "audit-write-timeout", config.AuditWriteTimeout, "audit write timeout")
	flags.IntVar(&config.DatabaseMinConnections, "database-min-connections", config.DatabaseMinConnections, "minimum database connections")
	flags.IntVar(&config.DatabaseMaxConnections, "database-max-connections", config.DatabaseMaxConnections, "maximum database connections")
	flags.DurationVar(&config.DatabaseConnectTimeout, "database-connect-timeout", config.DatabaseConnectTimeout, "database connect timeout")
	flags.BoolVar(&config.TelemetryEnabled, "telemetry-enabled", config.TelemetryEnabled, "enable OTLP telemetry export")
	flags.StringVar(&config.OTelEndpoint, "otel-endpoint", config.OTelEndpoint, "OTLP HTTP endpoint base URL")
	flags.Float64Var(&config.TraceSampleRatio, "trace-sample-ratio", config.TraceSampleRatio, "trace sampling ratio")
	flags.DurationVar(&config.TelemetryExportInterval, "telemetry-export-interval", config.TelemetryExportInterval, "telemetry export interval")
	flags.IntVar(&config.TelemetryQueueSize, "telemetry-queue-size", config.TelemetryQueueSize, "telemetry export queue size")
	if err := flags.Parse(arguments); err != nil || flags.NArg() != 0 || maxBatchRows > math.MaxUint32 {
		return fmt.Errorf("%w: command-line flags", ErrInvalidConfig)
	}
	mode, err := parseAuditMode(auditMode)
	if err != nil {
		return fmt.Errorf("%w: flag audit mode", ErrInvalidConfig)
	}
	config.AuditMode = mode
	config.MaxBatchRows = uint32(maxBatchRows)
	config.DatabaseURL = SecretURL(databaseURL)
	return nil
}

func resolveConfigPath(arguments []string, lookup LookupEnv) (string, error) {
	path := ""
	if lookup != nil {
		path, _ = lookup(EnvConfig)
	}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		if argument == "--config" || argument == "-config" {
			index++
			if index >= len(arguments) {
				return "", fmt.Errorf("%w: missing config flag value", ErrInvalidConfig)
			}
			path = arguments[index]
			continue
		}
		if strings.HasPrefix(argument, "--config=") || strings.HasPrefix(argument, "-config=") {
			path = argument[strings.IndexByte(argument, '=')+1:]
		}
	}
	return path, nil
}

func parseAuditMode(value string) (persistence.AuditMode, error) {
	switch value {
	case "off":
		return persistence.AuditOff, nil
	case "best-effort":
		return persistence.AuditBestEffort, nil
	case "required":
		return persistence.AuditRequired, nil
	default:
		return 0, ErrInvalidConfig
	}
}

func auditModeString(mode persistence.AuditMode) string {
	switch mode {
	case persistence.AuditOff:
		return "off"
	case persistence.AuditBestEffort:
		return "best-effort"
	case persistence.AuditRequired:
		return "required"
	default:
		return ""
	}
}

func validTimeout(value time.Duration) bool {
	return value > 0 && value <= maxOperationalTimeout
}

func validAddress(address string) bool {
	host, portText, err := net.SplitHostPort(address)
	if err != nil || strings.ContainsAny(host, "\r\n") {
		return false
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	return err == nil && port != 0
}

func validDatabaseURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		return false
	}
	return strings.Trim(parsed.Path, "/") != ""
}
