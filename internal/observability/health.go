package observability

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"time"

	coreservice "github.com/sebishogun/nornrune/internal/service"
)

var (
	ErrInvalidHealth         = errors.New("observability: invalid health probe")
	ErrNotReady              = errors.New("observability: service not ready")
	ErrInvalidProfileAddress = errors.New("observability: pprof address must be a loopback host and nonzero port")
)

// Checker reports whether runtime dependencies can serve new work.
type Checker interface {
	Health(context.Context) error
}

// Health separates process liveness from dependency and admission readiness.
type Health struct {
	checker   Checker
	admission *coreservice.Service
}

// NewHealth binds readiness to one dependency checker and admission service.
func NewHealth(checker Checker, admission *coreservice.Service) (*Health, error) {
	if checker == nil || admission == nil || admission.Stats().Limit == 0 {
		return nil, ErrInvalidHealth
	}
	return &Health{checker: checker, admission: admission}, nil
}

// Liveness reports whether the probe object can serve requests. Dependency
// failures and graceful shutdown do not make the process dead.
func (health *Health) Liveness(ctx context.Context) error {
	if health == nil || health.checker == nil || health.admission == nil || ctx == nil {
		return ErrInvalidHealth
	}
	return ctx.Err()
}

// Readiness requires open admission and a healthy runtime dependency set.
func (health *Health) Readiness(ctx context.Context) error {
	if health == nil || health.checker == nil || health.admission == nil || ctx == nil {
		return ErrInvalidHealth
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	stats := health.admission.Stats()
	if stats.Limit == 0 || !stats.Accepting {
		return ErrNotReady
	}
	return health.checker.Health(ctx)
}

// NewProfileServer creates a disabled server for an empty address or a
// dedicated pprof server that can bind only to a loopback host.
func NewProfileServer(address string) (*http.Server, error) {
	if address == "" {
		return nil, nil
	}
	if !loopbackAddress(address) {
		return nil, ErrInvalidProfileAddress
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return &http.Server{
		Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 1 << 20,
	}, nil
}

func loopbackAddress(address string) bool {
	host, encodedPort, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	port, err := strconv.ParseUint(encodedPort, 10, 16)
	if err != nil || port == 0 {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
