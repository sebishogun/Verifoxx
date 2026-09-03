package config

import (
	"math"
	"testing"
	"time"
)

func TestLoadTelemetryEnvironment(t *testing.T) {
	values := map[string]string{
		EnvTelemetryEnabled:        "true",
		EnvOTelEndpoint:            "https://collector.example:4318",
		EnvTraceSampleRatio:        "0.25",
		EnvTelemetryExportInterval: "2s",
		EnvTelemetryQueueSize:      "512",
	}
	config, err := Load(nil, func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if err != nil {
		t.Fatal(err)
	}
	if !config.TelemetryEnabled || config.OTelEndpoint != values[EnvOTelEndpoint] ||
		config.TraceSampleRatio != 0.25 || config.TelemetryExportInterval != 2*time.Second || config.TelemetryQueueSize != 512 {
		t.Fatalf("telemetry config = %+v", config)
	}
}

func TestTelemetryConfigurationRejectsUnboundedOrCredentialedValues(t *testing.T) {
	valid := Default()
	valid.TelemetryEnabled = true
	valid.OTelEndpoint = "https://collector.example:4318"
	invalid := []Config{
		func() Config {
			value := valid
			value.OTelEndpoint = "https://user:secret@collector.example"
			return value
		}(),
		func() Config { value := valid; value.TraceSampleRatio = -1; return value }(),
		func() Config { value := valid; value.TraceSampleRatio = 2; return value }(),
		func() Config { value := valid; value.TelemetryExportInterval = 0; return value }(),
		func() Config { value := valid; value.TelemetryQueueSize = 0; return value }(),
		func() Config { value := valid; value.TelemetryQueueSize = 1 << 20; return value }(),
	}
	for _, config := range invalid {
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil", config)
		}
	}
}

func TestTelemetryConfigurationValidatesRuntimeSettingsWhenOTLPDisabled(t *testing.T) {
	valid := Default()
	invalid := []Config{
		func() Config { value := valid; value.TraceSampleRatio = -1; return value }(),
		func() Config { value := valid; value.TraceSampleRatio = math.NaN(); return value }(),
		func() Config { value := valid; value.TelemetryExportInterval = 0; return value }(),
		func() Config { value := valid; value.TelemetryQueueSize = 0; return value }(),
	}
	for _, config := range invalid {
		if err := config.Validate(); err == nil {
			t.Fatalf("Validate(%+v) error = nil", config)
		}
	}
}
