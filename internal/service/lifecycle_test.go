package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestServiceStopsAdmissionCancelsQueueAndDrainsActive(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	active, err := gate.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit(active) error = %v", err)
	}
	queued := make(chan error, 1)
	go func() {
		_, queuedErr := gate.Admit(context.Background())
		queued <- queuedErr
	}()
	waitService(t, func() bool { return gate.Stats().Queued == 1 })

	journalStarted := make(chan struct{})
	journalRelease := make(chan struct{})
	recorder := &lifecycleRecorder{}
	lifecycle, err := NewLifecycle(gate, testLifecycleConfig(), ShutdownHooks{
		FlushJournal: func(context.Context) error {
			recorder.append("journal")
			close(journalStarted)
			<-journalRelease
			return nil
		},
		StopSessions: func(context.Context) error {
			recorder.append("sessions")
			return nil
		},
		CloseDatabase: func(context.Context) error {
			recorder.append("database")
			return nil
		},
		JoinWorkers: func(context.Context) error {
			recorder.append("workers")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	shutdown := make(chan error, 1)
	go func() { shutdown <- lifecycle.Shutdown(context.Background()) }()

	if queuedErr := receiveService(t, queued); !errors.Is(queuedErr, ErrServiceStopping) {
		t.Fatalf("queued Admit() error = %v, want %v", queuedErr, ErrServiceStopping)
	}
	if _, err := gate.Admit(context.Background()); !errors.Is(err, ErrServiceStopping) {
		t.Fatalf("Admit(after stop) error = %v, want %v", err, ErrServiceStopping)
	}
	select {
	case <-journalStarted:
		t.Fatal("journal flush started before active evaluation drained")
	case <-time.After(20 * time.Millisecond):
	}
	if err := gate.Release(&active); err != nil {
		t.Fatalf("Release(active) error = %v", err)
	}
	select {
	case <-journalStarted:
	case <-time.After(time.Second):
		t.Fatal("journal flush did not start after evaluation drain")
	}
	if got := recorder.snapshot(); len(got) != 1 || got[0] != "journal" {
		t.Fatalf("shutdown order before journal flush = %v", got)
	}
	close(journalRelease)
	if err := receiveService(t, shutdown); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := recorder.snapshot(); !equalStrings(got, []string{"journal", "sessions", "database", "workers"}) {
		t.Fatalf("shutdown order = %v", got)
	}
	stats := gate.Stats()
	if stats.Accepting || stats.Active != 0 || stats.Queued != 0 {
		t.Fatalf("service stats after shutdown = %+v", stats)
	}
}

func TestServiceAdmissionCancellationAndLeaseValidation(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	active, err := gate.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit(active) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gate.Admit(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Admit(canceled) error = %v, want context cancellation", err)
	}
	copyOfActive := active
	if err := gate.Release(&active); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	reused, err := gate.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit(reused) error = %v", err)
	}
	if err := gate.Release(&copyOfActive); !errors.Is(err, ErrAdmissionReleased) {
		t.Fatalf("Release(copy) error = %v, want %v", err, ErrAdmissionReleased)
	}
	if stats := gate.Stats(); stats.Active != 1 {
		t.Fatalf("stale release changed active admissions: %+v", stats)
	}
	if err := gate.Release(&reused); err != nil {
		t.Fatalf("Release(reused) error = %v", err)
	}
	if err := gate.Release(&Admission{}); !errors.Is(err, ErrInvalidAdmission) {
		t.Fatalf("Release(empty) error = %v, want %v", err, ErrInvalidAdmission)
	}
}

func TestServiceBoundsQueuedAdmissions(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	active, err := gate.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit(active) error = %v", err)
	}
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, queuedErr := gate.Admit(queuedCtx)
		queued <- queuedErr
	}()
	waitService(t, func() bool { return gate.Stats().Queued == 1 })

	busyCtx, cancelBusy := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelBusy()
	if _, err := gate.Admit(busyCtx); !errors.Is(err, ErrServiceBusy) {
		t.Fatalf("Admit(over queue limit) error = %v, want %v", err, ErrServiceBusy)
	}
	cancelQueued()
	if err := receiveService(t, queued); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued Admit() error = %v, want context cancellation", err)
	}
	if err := gate.Release(&active); err != nil {
		t.Fatalf("Release(active) error = %v", err)
	}
}

func TestServiceAdmitReleaseAllocations(t *testing.T) {
	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	allocations := testing.AllocsPerRun(1000, func() {
		admission, admitErr := gate.Admit(ctx)
		if admitErr != nil {
			t.Fatalf("Admit() error = %v", admitErr)
		}
		if releaseErr := gate.Release(&admission); releaseErr != nil {
			t.Fatalf("Release() error = %v", releaseErr)
		}
	})
	if allocations != 0 {
		t.Fatalf("Admit/Release allocations = %v, want 0", allocations)
	}
}

func TestLifecycleFlushesTelemetryAfterDatabaseAndBeforeWorkers(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := &lifecycleRecorder{}
	telemetryErr := errors.New("collector unavailable")
	lifecycle, err := NewLifecycle(gate, testLifecycleConfig(), ShutdownHooks{
		CloseDatabase: func(context.Context) error {
			recorder.append("database")
			return nil
		},
		FlushTelemetry: func(context.Context) error {
			recorder.append("telemetry")
			return telemetryErr
		},
		JoinWorkers: func(context.Context) error {
			recorder.append("workers")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	shutdownErr := lifecycle.Shutdown(context.Background())
	if !errors.Is(shutdownErr, telemetryErr) {
		t.Fatalf("Shutdown() error = %v, want telemetry flush error", shutdownErr)
	}
	if got := recorder.snapshot(); !equalStrings(got, []string{"database", "telemetry", "workers"}) {
		t.Fatalf("shutdown order = %v", got)
	}
}

func TestLifecycleContinuesCleanupAndJoinsWorkersLast(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	recorder := &lifecycleRecorder{}
	journalErr := errors.New("journal unavailable")
	sessionErr := errors.New("session stop failed")
	lifecycle, err := NewLifecycle(gate, testLifecycleConfig(), ShutdownHooks{
		FlushJournal: func(context.Context) error {
			recorder.append("journal")
			return journalErr
		},
		StopSessions: func(context.Context) error {
			recorder.append("sessions")
			return sessionErr
		},
		CloseDatabase: func(context.Context) error {
			recorder.append("database")
			return nil
		},
		JoinWorkers: func(context.Context) error {
			recorder.append("workers")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	shutdownErr := lifecycle.Shutdown(context.Background())
	if !errors.Is(shutdownErr, journalErr) || !errors.Is(shutdownErr, sessionErr) {
		t.Fatalf("Shutdown() error = %v, want joined hook errors", shutdownErr)
	}
	if got := recorder.snapshot(); !equalStrings(got, []string{"journal", "sessions", "database", "workers"}) {
		t.Fatalf("shutdown order = %v", got)
	}
	if err := lifecycle.Shutdown(context.Background()); !errors.Is(err, journalErr) {
		t.Fatalf("second Shutdown() error = %v, want stored result", err)
	}
}

func TestLifecycleRunUsesContextAndInternalShutdownDeadline(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	deadlineObserved := make(chan time.Time, 1)
	lifecycle, err := NewLifecycle(gate, testLifecycleConfig(), ShutdownHooks{
		FlushJournal: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				return errors.New("missing shutdown deadline")
			}
			deadlineObserved <- deadline
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- lifecycle.Run(runCtx) }()
	select {
	case <-lifecycle.Ready():
	case <-time.After(time.Second):
		t.Fatal("lifecycle did not become ready")
	}
	stop()
	if err := receiveService(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case deadline := <-deadlineObserved:
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > time.Second {
			t.Fatalf("shutdown deadline remaining = %v", remaining)
		}
	case <-time.After(time.Second):
		t.Fatal("journal did not observe shutdown context")
	}
	if err := lifecycle.Run(context.Background()); !errors.Is(err, ErrLifecycleStarted) {
		t.Fatalf("second Run() error = %v, want %v", err, ErrLifecycleStarted)
	}
}

func TestLifecycleReservesJournalFlushBudget(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	active, err := gate.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	flushContext := make(chan error, 1)
	lifecycle, err := NewLifecycle(gate, LifecycleConfig{
		ShutdownTimeout:     200 * time.Millisecond,
		JournalFlushTimeout: 75 * time.Millisecond,
	}, ShutdownHooks{
		FlushJournal: func(ctx context.Context) error {
			flushContext <- ctx.Err()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	shutdownErr := lifecycle.Shutdown(context.Background())
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want drain deadline", shutdownErr)
	}
	if err := receiveService(t, flushContext); err != nil {
		t.Fatalf("journal flush started with expired context: %v", err)
	}
	if err := gate.Release(&active); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestLifecycleRunReturnsAtDeadlineWhenHookBlocks(t *testing.T) {
	t.Parallel()

	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	workerRelease := make(chan struct{})
	lifecycle, err := NewLifecycle(gate, LifecycleConfig{
		ShutdownTimeout:     50 * time.Millisecond,
		JournalFlushTimeout: 10 * time.Millisecond,
	}, ShutdownHooks{
		JoinWorkers: func(context.Context) error {
			<-workerRelease
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewLifecycle() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- lifecycle.Run(context.Background()) }()
	select {
	case <-lifecycle.Ready():
	case <-time.After(time.Second):
		t.Fatal("lifecycle did not become ready")
	}
	started := time.Now()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- lifecycle.Shutdown(context.Background()) }()
	if err := receiveService(t, runDone); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want shutdown deadline", err)
	}
	if err := receiveService(t, shutdownDone); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want shutdown deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("Run() exceeded bounded shutdown: %v", elapsed)
	}
	close(workerRelease)
	if err := lifecycle.Shutdown(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("completed Shutdown() error = %v, want stored timeout", err)
	}
}

func TestServiceAndLifecycleRejectInvalidCalls(t *testing.T) {
	t.Parallel()

	if _, err := New(0); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("New(0) error = %v, want %v", err, ErrInvalidService)
	}
	if _, err := New(MaxAdmissions + 1); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("New(MaxAdmissions+1) error = %v, want %v", err, ErrInvalidService)
	}
	var nilService *Service
	if _, err := nilService.Admit(context.Background()); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("nil Admit() error = %v, want %v", err, ErrInvalidService)
	}
	if err := nilService.Release(nil); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("nil Release() error = %v, want %v", err, ErrInvalidService)
	}
	gate, err := New(1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := gate.Admit(nil); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("Admit(nil) error = %v, want %v", err, ErrInvalidContext)
	}
	if _, err := NewLifecycle(gate, LifecycleConfig{}, ShutdownHooks{}); !errors.Is(err, ErrInvalidLifecycle) {
		t.Fatalf("NewLifecycle(timeout=0) error = %v, want %v", err, ErrInvalidLifecycle)
	}
}

func testLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		ShutdownTimeout:     time.Second,
		JournalFlushTimeout: 100 * time.Millisecond,
	}
}

type lifecycleRecorder struct {
	mu     sync.Mutex
	events []string
}

func (recorder *lifecycleRecorder) append(event string) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

func (recorder *lifecycleRecorder) snapshot() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.events...)
}

func waitService(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for service condition")
		}
	}
}

func receiveService[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for service result")
		var zero T
		return zero
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func BenchmarkServiceAdmitRelease(b *testing.B) {
	gate, err := New(1)
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		admission, admitErr := gate.Admit(ctx)
		if admitErr != nil {
			b.Fatalf("Admit() error = %v", admitErr)
		}
		if releaseErr := gate.Release(&admission); releaseErr != nil {
			b.Fatalf("Release() error = %v", releaseErr)
		}
	}
}
