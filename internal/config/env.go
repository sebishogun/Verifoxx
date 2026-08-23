package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

const (
	EnvConfig                 = "VERIFOXX_CONFIG"
	EnvHTTPAddress            = "VERIFOXX_HTTP_ADDRESS"
	EnvGRPCAddress            = "VERIFOXX_GRPC_ADDRESS"
	EnvPolicyName             = "VERIFOXX_POLICY_NAME"
	EnvDatabaseURL            = "VERIFOXX_DATABASE_URL"
	EnvWorkers                = "VERIFOXX_WORKERS"
	EnvQueueDepth             = "VERIFOXX_QUEUE_DEPTH"
	EnvMaxBatchRows           = "VERIFOXX_MAX_BATCH_ROWS"
	EnvMaxBodyBytes           = "VERIFOXX_MAX_BODY_BYTES"
	EnvRequestTimeout         = "VERIFOXX_REQUEST_TIMEOUT"
	EnvShutdownTimeout        = "VERIFOXX_SHUTDOWN_TIMEOUT"
	EnvAuditMode              = "VERIFOXX_AUDIT_MODE"
	EnvAuditWriters           = "VERIFOXX_AUDIT_WRITERS"
	EnvAuditQueueDepth        = "VERIFOXX_AUDIT_QUEUE_DEPTH"
	EnvAuditWriteTimeout      = "VERIFOXX_AUDIT_WRITE_TIMEOUT"
	EnvDatabaseMinConnections = "VERIFOXX_DATABASE_MIN_CONNECTIONS"
	EnvDatabaseMaxConnections = "VERIFOXX_DATABASE_MAX_CONNECTIONS"
	EnvDatabaseConnectTimeout = "VERIFOXX_DATABASE_CONNECT_TIMEOUT"
)

// LookupEnv supplies environment values without coupling tests to process state.
type LookupEnv func(string) (string, bool)

// LoadOS loads configuration from process arguments, environment, and any
// selected configuration file.
func LoadOS(arguments []string) (Config, error) {
	return Load(arguments, os.LookupEnv)
}

func applyEnvironment(config *Config, lookup LookupEnv) error {
	if lookup == nil {
		return nil
	}
	applyEnvironmentString(&config.HTTPAddress, lookup, EnvHTTPAddress)
	applyEnvironmentString(&config.GRPCAddress, lookup, EnvGRPCAddress)
	applyEnvironmentString(&config.PolicyName, lookup, EnvPolicyName)
	if value, ok := lookup(EnvDatabaseURL); ok {
		config.DatabaseURL = SecretURL(value)
	}
	var err error
	if config.Workers, err = environmentInt(config.Workers, lookup, EnvWorkers); err != nil {
		return err
	}
	if config.QueueDepth, err = environmentInt(config.QueueDepth, lookup, EnvQueueDepth); err != nil {
		return err
	}
	if config.AuditWriters, err = environmentInt(config.AuditWriters, lookup, EnvAuditWriters); err != nil {
		return err
	}
	if config.AuditQueueDepth, err = environmentInt(config.AuditQueueDepth, lookup, EnvAuditQueueDepth); err != nil {
		return err
	}
	if config.DatabaseMinConnections, err = environmentInt(config.DatabaseMinConnections, lookup, EnvDatabaseMinConnections); err != nil {
		return err
	}
	if config.DatabaseMaxConnections, err = environmentInt(config.DatabaseMaxConnections, lookup, EnvDatabaseMaxConnections); err != nil {
		return err
	}
	if config.MaxBodyBytes, err = environmentInt64(config.MaxBodyBytes, lookup, EnvMaxBodyBytes); err != nil {
		return err
	}
	if config.MaxBatchRows, err = environmentUint32(config.MaxBatchRows, lookup, EnvMaxBatchRows); err != nil {
		return err
	}
	if config.RequestTimeout, err = environmentDuration(config.RequestTimeout, lookup, EnvRequestTimeout); err != nil {
		return err
	}
	if config.ShutdownTimeout, err = environmentDuration(config.ShutdownTimeout, lookup, EnvShutdownTimeout); err != nil {
		return err
	}
	if config.AuditWriteTimeout, err = environmentDuration(config.AuditWriteTimeout, lookup, EnvAuditWriteTimeout); err != nil {
		return err
	}
	if config.DatabaseConnectTimeout, err = environmentDuration(config.DatabaseConnectTimeout, lookup, EnvDatabaseConnectTimeout); err != nil {
		return err
	}
	if value, ok := lookup(EnvAuditMode); ok {
		config.AuditMode, err = parseAuditMode(value)
		if err != nil {
			return fmt.Errorf("%w: environment %s", ErrInvalidConfig, EnvAuditMode)
		}
	}
	return nil
}

func applyEnvironmentString(destination *string, lookup LookupEnv, name string) {
	if value, ok := lookup(name); ok {
		*destination = value
	}
}

func environmentInt(current int, lookup LookupEnv, name string) (int, error) {
	value, ok := lookup(name)
	if !ok {
		return current, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: environment %s", ErrInvalidConfig, name)
	}
	return parsed, nil
}

func environmentInt64(current int64, lookup LookupEnv, name string) (int64, error) {
	value, ok := lookup(name)
	if !ok {
		return current, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: environment %s", ErrInvalidConfig, name)
	}
	return parsed, nil
}

func environmentUint32(current uint32, lookup LookupEnv, name string) (uint32, error) {
	value, ok := lookup(name)
	if !ok {
		return current, nil
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: environment %s", ErrInvalidConfig, name)
	}
	return uint32(parsed), nil
}

func environmentDuration(current time.Duration, lookup LookupEnv, name string) (time.Duration, error) {
	value, ok := lookup(name)
	if !ok {
		return current, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%w: environment %s", ErrInvalidConfig, name)
	}
	return parsed, nil
}
