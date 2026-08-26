package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	coreservice "github.com/sebishogun/nornrune/internal/service"
)

func TestHealthSeparatesLivenessAndReadiness(t *testing.T) {
	t.Parallel()

	dependencyErr := errors.New("dependency unavailable")
	checker := &healthChecker{err: dependencyErr}
	admission, err := coreservice.New(1)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	health, err := NewHealth(checker, admission)
	if err != nil {
		t.Fatalf("NewHealth() error = %v", err)
	}
	if err := health.Liveness(context.Background()); err != nil {
		t.Fatalf("Liveness() error = %v", err)
	}
	if checker.calls.Load() != 0 {
		t.Fatalf("liveness dependency calls = %d, want 0", checker.calls.Load())
	}
	if err := health.Readiness(context.Background()); !errors.Is(err, dependencyErr) {
		t.Fatalf("Readiness() error = %v, want %v", err, dependencyErr)
	}
	checker.err = nil
	if err := health.Readiness(context.Background()); err != nil {
		t.Fatalf("Readiness() healthy error = %v", err)
	}
	if err := admission.StopAdmission(); err != nil {
		t.Fatalf("StopAdmission() error = %v", err)
	}
	if err := health.Readiness(context.Background()); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Readiness() after stop = %v, want %v", err, ErrNotReady)
	}
	if err := health.Liveness(context.Background()); err != nil {
		t.Fatalf("Liveness() after stop error = %v", err)
	}
}

func TestHealthRejectsInvalidDependenciesAndContexts(t *testing.T) {
	t.Parallel()

	admission, err := coreservice.New(1)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if health, err := NewHealth(nil, admission); err == nil || health != nil {
		t.Fatalf("NewHealth(nil checker) = (%p, %v)", health, err)
	}
	if health, err := NewHealth(&healthChecker{}, nil); err == nil || health != nil {
		t.Fatalf("NewHealth(nil admission) = (%p, %v)", health, err)
	}
	if health, err := NewHealth(&healthChecker{}, &coreservice.Service{}); err == nil || health != nil {
		t.Fatalf("NewHealth(zero admission) = (%p, %v)", health, err)
	}
	health, err := NewHealth(&healthChecker{}, admission)
	if err != nil {
		t.Fatalf("NewHealth() error = %v", err)
	}
	if err := health.Liveness(nil); !errors.Is(err, ErrInvalidHealth) {
		t.Fatalf("Liveness(nil) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := health.Readiness(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Readiness(canceled) error = %v", err)
	}
}

func TestProfileServerIsOptionalAndLoopbackOnly(t *testing.T) {
	t.Parallel()

	server, err := NewProfileServer("")
	if err != nil || server != nil {
		t.Fatalf("NewProfileServer(empty) = (%p, %v), want nil nil", server, err)
	}
	for _, address := range []string{"0.0.0.0:6060", "[::]:6060", "192.0.2.1:6060", "example.com:6060", "localhost", "localhost:0"} {
		t.Run(address, func(t *testing.T) {
			server, err := NewProfileServer(address)
			if err == nil || server != nil {
				t.Fatalf("NewProfileServer(%q) = (%p, %v), want nil error", address, server, err)
			}
		})
	}
	for _, address := range []string{"127.0.0.1:6060", "[::1]:6060", "localhost:6060"} {
		t.Run(address, func(t *testing.T) {
			server, err := NewProfileServer(address)
			if err != nil {
				t.Fatalf("NewProfileServer(%q) error = %v", address, err)
			}
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("pprof status = %d, want %d", response.Code, http.StatusOK)
			}
			if server.ReadHeaderTimeout <= 0 || server.MaxHeaderBytes <= 0 {
				t.Fatalf("pprof server limits = %+v", server)
			}
		})
	}
}

type healthChecker struct {
	err   error
	calls atomic.Uint64
}

func (checker *healthChecker) Health(context.Context) error {
	checker.calls.Add(1)
	return checker.err
}
