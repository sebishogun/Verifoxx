package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

func TestDefaultIsValid(t *testing.T) {
	t.Parallel()

	got := Default()
	if err := got.Validate(); err != nil {
		t.Fatalf("Default().Validate() error = %v", err)
	}
	wantWorkers := min(runtime.GOMAXPROCS(0), MaxWorkers)
	if got.Workers != wantWorkers || got.AuditMode != persistence.AuditOff || got.MaxBatchRows == 0 {
		t.Fatalf("Default() = %+v", got)
	}
}

func TestLoadPrecedence(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	environmentFile := filepath.Join(directory, "environment.json")
	flagFile := filepath.Join(directory, "flag.json")
	writeConfigFile(t, environmentFile, `{"workers":2,"queue_depth":2,"request_timeout":"2s"}`)
	writeConfigFile(t, flagFile, `{"workers":3,"queue_depth":3,"request_timeout":"3s"}`)
	environment := map[string]string{
		EnvConfig:            environmentFile,
		EnvWorkers:           "4",
		EnvQueueDepth:        "4",
		EnvMaxBatchRows:      "400",
		EnvDatabaseURL:       "postgres://runtime:secret@database/verifoxx",
		EnvShutdownTimeout:   "4s",
		EnvAuditWriteTimeout: "1s",
	}

	got, err := Load([]string{
		"--config", flagFile,
		"--workers", "5",
		"--max-batch-rows", "500",
	}, mapLookup(environment))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Workers != 5 {
		t.Fatalf("Workers = %d, want flag value 5", got.Workers)
	}
	if got.QueueDepth != 4 {
		t.Fatalf("QueueDepth = %d, want environment value 4", got.QueueDepth)
	}
	if got.RequestTimeout != 3*time.Second {
		t.Fatalf("RequestTimeout = %v, want flag-selected file value 3s", got.RequestTimeout)
	}
	if got.MaxBatchRows != 500 {
		t.Fatalf("MaxBatchRows = %d, want flag value 500", got.MaxBatchRows)
	}
	if got.ShutdownTimeout != 4*time.Second {
		t.Fatalf("ShutdownTimeout = %v, want environment value 4s", got.ShutdownTimeout)
	}
	if got.GRPCAddress != Default().GRPCAddress {
		t.Fatalf("GRPCAddress = %q, want default %q", got.GRPCAddress, Default().GRPCAddress)
	}
}

func TestLoadAllSources(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "verifoxx.json")
	writeConfigFile(t, path, `{
		"http_address":"127.0.0.1:18080",
		"grpc_address":"127.0.0.1:19090",
		"policy_name":"policy-a",
		"database_url":"postgres://runtime:secret@database/verifoxx",
		"workers":2,
		"queue_depth":8,
		"max_batch_rows":1024,
		"max_body_bytes":1048576,
		"request_timeout":"11s",
		"shutdown_timeout":"12s",
		"audit_mode":"required",
		"audit_writers":2,
		"audit_queue_depth":8,
		"audit_write_timeout":"3s",
		"database_min_connections":1,
		"database_max_connections":8,
		"database_connect_timeout":"14s"
	}`)

	got, err := Load([]string{"--config=" + path}, mapLookup(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.HTTPAddress != "127.0.0.1:18080" || got.GRPCAddress != "127.0.0.1:19090" ||
		got.PolicyName != "policy-a" || got.Workers != 2 || got.QueueDepth != 8 ||
		got.MaxBatchRows != 1024 || got.MaxBodyBytes != 1048576 ||
		got.RequestTimeout != 11*time.Second || got.ShutdownTimeout != 12*time.Second ||
		got.AuditMode != persistence.AuditRequired || got.AuditWriters != 2 || got.AuditQueueDepth != 8 ||
		got.AuditWriteTimeout != 3*time.Second || got.DatabaseMinConnections != 1 ||
		got.DatabaseMaxConnections != 8 || got.DatabaseConnectTimeout != 14*time.Second {
		t.Fatalf("Load() = %+v", got)
	}
	if got.DatabaseURL.Reveal() != "postgres://runtime:secret@database/verifoxx" {
		t.Fatal("Load() did not preserve the database URL for runtime use")
	}
}

func TestConfigRedactsDatabaseURL(t *testing.T) {
	t.Parallel()

	value, err := Load(nil, mapLookup(map[string]string{
		EnvDatabaseURL: "postgres://runtime:super-secret@database/verifoxx",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	formatted := fmt.Sprintf("%+v %#v %s", value, value, value.DatabaseURL)
	if strings.Contains(formatted, "super-secret") || strings.Contains(string(encoded), "super-secret") {
		t.Fatalf("database credential leaked: formatted=%q json=%s", formatted, encoded)
	}
	if !strings.Contains(formatted, RedactedSecret) || !strings.Contains(string(encoded), RedactedSecret) {
		t.Fatalf("database URL redaction missing: formatted=%q json=%s", formatted, encoded)
	}
}

func TestLoadRejectsMalformedSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contents    string
		environment map[string]string
		arguments   []string
	}{
		{name: "unknown file field", contents: `{"unknown":1}`},
		{name: "trailing file value", contents: `{}` + "\n{}"},
		{name: "oversized file", contents: strings.Repeat(" ", maxConfigFileBytes+1)},
		{name: "invalid environment integer", environment: map[string]string{EnvWorkers: "many"}},
		{name: "invalid environment duration", environment: map[string]string{EnvRequestTimeout: "soon"}},
		{name: "invalid environment audit mode", environment: map[string]string{EnvAuditMode: "sometimes"}},
		{name: "unknown flag", arguments: []string{"--missing=value"}},
		{name: "missing config value", arguments: []string{"--config"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			environment := test.environment
			arguments := test.arguments
			if test.contents != "" {
				path := filepath.Join(t.TempDir(), "invalid.json")
				writeConfigFile(t, path, test.contents)
				arguments = append([]string{"--config", path}, arguments...)
			}
			if _, err := Load(arguments, mapLookup(environment)); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Load() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "http address", mutate: func(value *Config) { value.HTTPAddress = "invalid" }},
		{name: "policy name", mutate: func(value *Config) { value.PolicyName = " " }},
		{name: "workers", mutate: func(value *Config) { value.Workers = MaxWorkers + 1 }},
		{name: "queue depth", mutate: func(value *Config) { value.QueueDepth = 0 }},
		{name: "batch rows", mutate: func(value *Config) { value.MaxBatchRows = MaxBatchRows + 1 }},
		{name: "body bytes", mutate: func(value *Config) { value.MaxBodyBytes = MaxBodyBytes + 1 }},
		{name: "request timeout", mutate: func(value *Config) { value.RequestTimeout = 0 }},
		{name: "shutdown timeout", mutate: func(value *Config) { value.ShutdownTimeout = -time.Second }},
		{name: "flush budget", mutate: func(value *Config) {
			value.AuditMode = persistence.AuditRequired
			value.DatabaseURL = SecretURL("postgres://runtime:secret@database/verifoxx")
			value.ShutdownTimeout = value.AuditWriteTimeout
		}},
		{name: "audit mode", mutate: func(value *Config) { value.AuditMode = persistence.AuditMode(255) }},
		{name: "audit database", mutate: func(value *Config) { value.AuditMode = persistence.AuditRequired }},
		{name: "audit writers", mutate: func(value *Config) { value.AuditWriters = value.AuditQueueDepth + 1 }},
		{name: "database url", mutate: func(value *Config) { value.DatabaseURL = SecretURL("https://database/verifoxx") }},
		{name: "database connections", mutate: func(value *Config) {
			value.DatabaseMinConnections = value.DatabaseMaxConnections + 1
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := Default()
			test.mutate(&value)
			if err := value.Validate(); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("Validate() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func writeConfigFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

func mapLookup(values map[string]string) LookupEnv {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}
