package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"

	"github.com/sebishogun/nornrune/internal/adapters/grpcapi"
	"github.com/sebishogun/nornrune/internal/adapters/httpapi"
	postgresadapter "github.com/sebishogun/nornrune/internal/adapters/postgres"
	"github.com/sebishogun/nornrune/internal/buildinfo"
	"github.com/sebishogun/nornrune/internal/config"
	"github.com/sebishogun/nornrune/internal/observability"
	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/security"
	"github.com/sebishogun/nornrune/internal/service"
	"github.com/sebishogun/nornrune/internal/simdops"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
)

type serveFailure struct {
	err  error
	name string
}

// Serve starts the database-backed HTTP and gRPC adapters and drains them on
// cancellation.
func Serve(ctx context.Context, cfg config.Config) error {
	if ctx == nil || cfg.DatabaseURL.Empty() {
		return ErrInvalidRuntime
	}
	limits, err := runtimeSecurityLimits(cfg)
	if err != nil {
		return err
	}
	connectContext, cancelConnect := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL.Reveal())
	if err != nil {
		cancelConnect()
		return fmt.Errorf("server: parse runtime database URL: %w", err)
	}
	poolConfig.MinConns = int32(cfg.DatabaseMinConnections)
	poolConfig.MaxConns = int32(cfg.DatabaseMaxConnections)
	pool, err := pgxpool.NewWithConfig(connectContext, poolConfig)
	if err != nil {
		cancelConnect()
		return fmt.Errorf("server: open runtime database: %w", err)
	}
	if err := pool.Ping(connectContext); err != nil {
		cancelConnect()
		pool.Close()
		return fmt.Errorf("server: ping runtime database: %w", err)
	}
	cancelConnect()

	registry := &program.Registry{}
	policyStore, err := postgresadapter.NewPolicyStore(pool)
	if err != nil {
		pool.Close()
		return err
	}
	publisher, err := persistence.NewPublisher(policyStore, registry, func(source []byte) (*program.Program, error) {
		return compilePolicySourceWithLimits(source, limits)
	}, buildinfo.Version())
	if err != nil {
		pool.Close()
		return err
	}
	var auditStore persistence.AuditStore
	if cfg.AuditMode != persistence.AuditOff {
		auditStore, err = postgresadapter.NewAuditStore(pool)
		if err != nil {
			pool.Close()
			return err
		}
	}
	auditCapacity, err := runtimeAuditCapacity(cfg, limits)
	if err != nil {
		pool.Close()
		return err
	}
	var engine *Engine
	journal, err := postgresadapter.NewJournal(auditStore, postgresadapter.JournalConfig{
		Capacity: auditCapacity, WriteTimeout: cfg.AuditWriteTimeout, Mode: cfg.AuditMode,
		Writers: cfg.AuditWriters, QueueDepth: cfg.AuditQueueDepth,
		BestEffortComplete: func(persisted bool) {
			if engine == nil {
				return
			}
			if persisted {
				engine.recordAudit(publictelemetry.AuditPersisted)
			} else {
				engine.recordAudit(publictelemetry.AuditOptionalDrop)
			}
		},
	})
	if err != nil {
		pool.Close()
		return err
	}
	admission, err := service.New(cfg.QueueDepth)
	if err != nil {
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}
	// The server always maintains the allocation-free counters behind
	// /metrics; the telemetry flag gates only the OTLP pipeline and tracing.
	otelEndpoint := ""
	if cfg.TelemetryEnabled {
		otelEndpoint = cfg.OTelEndpoint
	}
	runtime, err := publictelemetry.New(ctx, publictelemetry.Config{
		Enabled:          true,
		Endpoint:         otelEndpoint,
		ServiceVersion:   buildinfo.Version(),
		BuildVersion:     buildinfo.Version(),
		ExportInterval:   cfg.TelemetryExportInterval,
		TraceSampleRatio: cfg.TraceSampleRatio,
		ExportQueueSize:  uint32(cfg.TelemetryQueueSize),
		QueueDepth:       func() uint64 { return uint64(admission.Stats().Queued) },
	})
	if err != nil {
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		Runtime:         runtime,
		QueueDepth:      func() uint64 { return uint64(admission.Stats().Queued) },
		JournalFailures: func() uint64 { return journal.Stats().Failed },
		SIMDTier:        simdops.Runtime().Tier, Workers: uint32(cfg.Workers),
	})
	if err != nil {
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}
	engine, err = NewEngine(EngineConfig{
		Registry: registry, Publisher: publisher, Journal: journal, Metrics: metrics, Telemetry: runtime, Health: pool.Ping,
		EngineVersion: buildinfo.Version(), AuditCapacity: auditCapacity, AuditMode: cfg.AuditMode, Workers: cfg.Workers,
		QueueDepth: cfg.QueueDepth, Limits: limits,
	})
	if err != nil {
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}
	defer func() { _ = engine.Close(context.Background()) }()
	if _, err := engine.CompilePolicy(ctx, []byte(nornrune.Source())); err != nil {
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return fmt.Errorf("server: publish embedded policy: %w", err)
	}

	httpHandler, err := httpapi.New(engine, admission, metrics, httpapi.Config{
		MaxBodyBytes: cfg.MaxBodyBytes, RequestTimeout: cfg.RequestTimeout, Telemetry: runtime,
	})
	if err != nil {
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}
	grpcServer, err := grpcapi.New(engine, admission, grpcapi.Config{
		MaxMessageBytes: int(cfg.MaxBodyBytes), RequestTimeout: cfg.RequestTimeout, Telemetry: runtime,
	})
	if err != nil {
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}
	httpListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.HTTPAddress)
	if err != nil {
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return fmt.Errorf("server: listen HTTP: %w", err)
	}
	grpcListener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.GRPCAddress)
	if err != nil {
		_ = httpListener.Close()
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return fmt.Errorf("server: listen gRPC: %w", err)
	}
	httpServer := &http.Server{
		Addr: cfg.HTTPAddress, Handler: httpHandler, ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout: cfg.RequestTimeout, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20,
	}
	shutdownHooks := service.ShutdownHooks{
		JoinWorkers: engine.Close,
		StopSessions: func(shutdownCtx context.Context) error {
			return errors.Join(httpServer.Shutdown(shutdownCtx), stopGRPC(shutdownCtx, grpcServer))
		},
		CloseDatabase: func(shutdownCtx context.Context) error {
			var closeErr error
			if cfg.AuditMode == persistence.AuditOff {
				closeErr = journal.Close(shutdownCtx)
			}
			pool.Close()
			return closeErr
		},
		FlushTelemetry: runtime.Shutdown,
	}
	lifecycleConfig := service.LifecycleConfig{ShutdownTimeout: cfg.ShutdownTimeout}
	if cfg.AuditMode != persistence.AuditOff {
		shutdownHooks.FlushJournal = journal.Close
		lifecycleConfig.JournalFlushTimeout = cfg.AuditWriteTimeout
	}
	lifecycle, err := service.NewLifecycle(admission, lifecycleConfig, shutdownHooks)
	if err != nil {
		_ = grpcListener.Close()
		_ = httpListener.Close()
		shutdownTelemetry(runtime)
		_ = journal.Close(context.Background())
		pool.Close()
		return err
	}

	failures := make(chan serveFailure, 2)
	go func() {
		if err := httpServer.Serve(httpListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failures <- serveFailure{name: "HTTP", err: err}
			return
		}
		failures <- serveFailure{name: "HTTP"}
	}()
	go func() {
		if err := grpcServer.Serve(grpcListener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			failures <- serveFailure{name: "gRPC", err: err}
			return
		}
		failures <- serveFailure{name: "gRPC"}
	}()

	lifecycleDone := make(chan error, 1)
	go func() { lifecycleDone <- lifecycle.Run(ctx) }()
	select {
	case lifecycleErr := <-lifecycleDone:
		return lifecycleErr
	case failure := <-failures:
		if ctx.Err() != nil {
			return <-lifecycleDone
		}
		var runErr error
		if failure.err != nil {
			runErr = fmt.Errorf("server: %s serve: %w", failure.name, failure.err)
		} else {
			runErr = fmt.Errorf("server: %s stopped unexpectedly", failure.name)
		}
		shutdownErr := lifecycle.Shutdown(context.Background())
		<-lifecycleDone
		return errors.Join(runErr, shutdownErr)
	}
}

// shutdownTelemetry bounds early-error-path telemetry flushes so a hung
// exporter cannot stall startup failure handling.
func shutdownTelemetry(runtime *publictelemetry.Runtime) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = runtime.Shutdown(ctx)
}

func runtimeSecurityLimits(cfg config.Config) (security.Limits, error) {
	if cfg.MaxBodyBytes <= 0 || cfg.MaxBodyBytes > int64(security.MaximumRequestBytes) {
		return security.Limits{}, ErrInvalidRuntime
	}
	limits := security.DefaultLimits()
	limits.RequestTimeout = cfg.RequestTimeout
	limits.MaxRequestBytes = int(cfg.MaxBodyBytes)
	limits.MaxOutputBytes = int(cfg.MaxBodyBytes)
	limits.MaxBatchRows = cfg.MaxBatchRows
	if err := limits.Validate(); err != nil {
		return security.Limits{}, ErrInvalidRuntime
	}
	return limits, nil
}

func runtimeAuditCapacity(cfg config.Config, limits security.Limits) (persistence.AuditCapacity, error) {
	if cfg.MaxBatchRows == 0 || cfg.MaxBodyBytes <= 0 || cfg.MaxBodyBytes > int64(^uint32(0))/3 {
		return persistence.AuditCapacity{}, ErrInvalidRuntime
	}
	rows := uint64(cfg.MaxBatchRows)
	evidence := min(rows*4, uint64(limits.MaxEvidenceRecords))
	links := min(rows*16, uint64(1<<20))
	return persistence.AuditCapacity{
		Bytes: int(cfg.MaxBodyBytes * 3), Requests: int(rows), Evidence: int(evidence),
		Rows: int(rows), EvidenceLinks: int(links),
	}, nil
}

func stopGRPC(ctx context.Context, server *grpc.Server) error {
	stopped := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		return nil
	case <-ctx.Done():
		server.Stop()
		<-stopped
		return ctx.Err()
	}
}

// Healthcheck probes the configured HTTP readiness endpoint.
func Healthcheck(ctx context.Context, cfg config.Config) error {
	if ctx == nil {
		return ErrInvalidRuntime
	}
	host, port, err := net.SplitHostPort(cfg.HTTPAddress)
	if err != nil {
		return ErrInvalidRuntime
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+net.JoinHostPort(host, port)+"/readyz", nil)
	if err != nil {
		return err
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("server: readiness returned HTTP %s", strconv.Itoa(response.StatusCode))
	}
	return errors.Join(readErr, closeErr)
}
