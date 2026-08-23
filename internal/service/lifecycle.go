package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrInvalidLifecycle = errors.New("service: invalid lifecycle")
	ErrLifecycleStarted = errors.New("service: lifecycle already started")
)

// ShutdownHooks adapt the journal, session group, database pool, and evaluator
// workers without importing transport or PostgreSQL types into this package.
type ShutdownHooks struct {
	FlushJournal  func(context.Context) error
	StopSessions  func(context.Context) error
	CloseDatabase func(context.Context) error
	JoinWorkers   func(context.Context) error
}

// LifecycleConfig divides the total shutdown deadline so evaluation drain
// cannot consume the required journal flush budget.
type LifecycleConfig struct {
	ShutdownTimeout     time.Duration
	JournalFlushTimeout time.Duration
}

// Lifecycle executes the single process shutdown sequence and shares its final
// result with every caller.
type Lifecycle struct {
	service         *Service
	ready           chan struct{}
	done            chan struct{}
	shutdownStarted chan struct{}
	shutdownCtx     context.Context
	hooks           ShutdownHooks
	result          error
	config          LifecycleConfig
	shutdownOnce    sync.Once
	started         atomic.Bool
}

// NewLifecycle binds one admission service to ordered teardown hooks.
func NewLifecycle(service *Service, config LifecycleConfig, hooks ShutdownHooks) (*Lifecycle, error) {
	if !service.valid() || config.ShutdownTimeout <= 0 ||
		(hooks.FlushJournal != nil && (config.JournalFlushTimeout <= 0 ||
			config.JournalFlushTimeout >= config.ShutdownTimeout)) {
		return nil, ErrInvalidLifecycle
	}
	return &Lifecycle{
		service:         service,
		ready:           make(chan struct{}),
		done:            make(chan struct{}),
		shutdownStarted: make(chan struct{}),
		hooks:           hooks,
		config:          config,
	}, nil
}

func (lifecycle *Lifecycle) valid() bool {
	return lifecycle != nil && lifecycle.service.valid() && lifecycle.ready != nil && lifecycle.done != nil &&
		lifecycle.shutdownStarted != nil &&
		lifecycle.config.ShutdownTimeout > 0 &&
		(lifecycle.hooks.FlushJournal == nil || (lifecycle.config.JournalFlushTimeout > 0 &&
			lifecycle.config.JournalFlushTimeout < lifecycle.config.ShutdownTimeout))
}

// Ready closes when Run has taken ownership of process lifecycle.
func (lifecycle *Lifecycle) Ready() <-chan struct{} {
	if !lifecycle.valid() {
		return nil
	}
	return lifecycle.ready
}

// Run waits for caller cancellation and then performs bounded graceful
// shutdown. It may be called exactly once.
func (lifecycle *Lifecycle) Run(ctx context.Context) error {
	if !lifecycle.valid() {
		return ErrInvalidLifecycle
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if !lifecycle.started.CompareAndSwap(false, true) {
		return ErrLifecycleStarted
	}
	close(lifecycle.ready)
	select {
	case <-ctx.Done():
		return lifecycle.Shutdown(context.Background())
	case <-lifecycle.done:
		return lifecycle.result
	case <-lifecycle.shutdownStarted:
		return lifecycle.Shutdown(context.Background())
	}
}

// Shutdown stops admission synchronously, then runs one independent bounded
// cleanup sequence. A caller may stop waiting without canceling cleanup.
func (lifecycle *Lifecycle) Shutdown(ctx context.Context) error {
	if !lifecycle.valid() {
		return ErrInvalidLifecycle
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	lifecycle.shutdownOnce.Do(func() {
		_ = lifecycle.service.StopAdmission()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), lifecycle.config.ShutdownTimeout)
		lifecycle.shutdownCtx = shutdownCtx
		close(lifecycle.shutdownStarted)
		go lifecycle.shutdown(shutdownCtx, cancel)
	})
	select {
	case <-lifecycle.done:
		return lifecycle.result
	default:
	}
	select {
	case <-lifecycle.done:
		return lifecycle.result
	case <-ctx.Done():
		return ctx.Err()
	case <-lifecycle.shutdownCtx.Done():
		select {
		case <-lifecycle.done:
			return lifecycle.result
		default:
			return fmt.Errorf("service: shutdown deadline: %w", lifecycle.shutdownCtx.Err())
		}
	}
}

func (lifecycle *Lifecycle) shutdown(ctx context.Context, cancel context.CancelFunc) {
	var joined error
	drainTimeout := lifecycle.config.ShutdownTimeout
	if lifecycle.hooks.FlushJournal != nil {
		drainTimeout -= lifecycle.config.JournalFlushTimeout
	}
	drainCtx, cancelDrain := context.WithTimeout(ctx, drainTimeout)
	if err := lifecycle.service.Drain(drainCtx); err != nil {
		joined = errors.Join(joined, fmt.Errorf("service: drain evaluations: %w", err))
	}
	cancelDrain()
	if lifecycle.hooks.FlushJournal != nil {
		flushCtx, cancelFlush := context.WithTimeout(ctx, lifecycle.config.JournalFlushTimeout)
		if err := lifecycle.hooks.FlushJournal(flushCtx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("service: flush journal: %w", err))
		}
		cancelFlush()
	}
	if lifecycle.hooks.StopSessions != nil {
		if err := lifecycle.hooks.StopSessions(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("service: stop sessions: %w", err))
		}
	}
	if lifecycle.hooks.CloseDatabase != nil {
		if err := lifecycle.hooks.CloseDatabase(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("service: close database: %w", err))
		}
	}
	if lifecycle.hooks.JoinWorkers != nil {
		if err := lifecycle.hooks.JoinWorkers(ctx); err != nil {
			joined = errors.Join(joined, fmt.Errorf("service: join workers: %w", err))
		}
	}
	if err := ctx.Err(); err != nil {
		joined = errors.Join(joined, fmt.Errorf("service: shutdown deadline: %w", err))
	}
	lifecycle.result = joined
	close(lifecycle.done)
	cancel()
}
