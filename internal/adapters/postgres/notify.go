package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sebishogun/verifoxx/internal/adapters/wire"
	"github.com/sebishogun/verifoxx/internal/persistence"
	"github.com/sebishogun/verifoxx/internal/program"
)

const (
	policyNotificationChannelPrefix = "verifoxx_policy_"
	policyListenerCloseTimeout      = time.Second
)

// PolicyReloader reloads durable policy state into one local registry.
type PolicyReloader interface {
	ReloadActive(context.Context, string) (*program.Program, persistence.PolicyVersion, error)
}

// PolicyListenerStats is one lock-free listener health snapshot.
type PolicyListenerStats struct {
	Connections   uint64
	Reconnects    uint64
	Notifications uint64
	Reloaded      uint64
	Ignored       uint64
	Failures      uint64
	BackendPID    uint32
	Connected     bool
}

// PolicyListener owns one dedicated PostgreSQL LISTEN connection. Run is
// synchronous so the caller owns its goroutine and shutdown context.
type PolicyListener struct {
	reloader       PolicyReloader
	pool           *pgxpool.Pool
	ready          chan struct{}
	policyName     string
	channel        string
	reconnects     atomic.Uint64
	connections    atomic.Uint64
	reconnectDelay time.Duration
	notifications  atomic.Uint64
	reloaded       atomic.Uint64
	ignored        atomic.Uint64
	failures       atomic.Uint64
	readyOnce      sync.Once
	backendPID     atomic.Uint32
	started        atomic.Bool
	connected      atomic.Bool
}

// NewPolicyListener validates the dedicated notification-loop dependencies.
func NewPolicyListener(
	pool *pgxpool.Pool,
	reloader PolicyReloader,
	policyName string,
	reconnectDelay time.Duration,
) (*PolicyListener, error) {
	if pool == nil || reloader == nil || policyName == "" || policyName != strings.TrimSpace(policyName) ||
		reconnectDelay <= 0 {
		return nil, fmt.Errorf("%w: policy listener dependencies", persistence.ErrInvalidPolicyPersistence)
	}
	return &PolicyListener{
		reloader:       reloader,
		policyName:     policyName,
		channel:        policyNotificationChannel(policyName),
		pool:           pool,
		ready:          make(chan struct{}),
		reconnectDelay: reconnectDelay,
	}, nil
}

// Ready closes after the first successful LISTEN and active-policy catch-up.
func (listener *PolicyListener) Ready() <-chan struct{} {
	if listener == nil {
		return nil
	}
	return listener.ready
}

// Stats returns listener counters and current connection state.
func (listener *PolicyListener) Stats() PolicyListenerStats {
	if listener == nil {
		return PolicyListenerStats{}
	}
	return PolicyListenerStats{
		Connections:   listener.connections.Load(),
		Reconnects:    listener.reconnects.Load(),
		Notifications: listener.notifications.Load(),
		Reloaded:      listener.reloaded.Load(),
		Ignored:       listener.ignored.Load(),
		Failures:      listener.failures.Load(),
		BackendPID:    listener.backendPID.Load(),
		Connected:     listener.connected.Load(),
	}
}

// Run listens until ctx is canceled, reconnecting and catching up the durable
// active policy after connection or reload failures.
func (listener *PolicyListener) Run(ctx context.Context) error {
	if listener == nil || ctx == nil {
		return fmt.Errorf("%w: policy listener run", persistence.ErrInvalidPolicyPersistence)
	}
	if !listener.started.CompareAndSwap(false, true) {
		return fmt.Errorf("%w: policy listener already started", persistence.ErrInvalidPolicyPersistence)
	}
	if ctx.Err() != nil {
		return nil
	}

	for {
		err := listener.listen(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			listener.failures.Add(1)
		}
		listener.reconnects.Add(1)
		timer := time.NewTimer(listener.reconnectDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		}
	}
}

func (listener *PolicyListener) listen(ctx context.Context) error {
	pooled, err := listener.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("postgres: acquire policy listener: %w", err)
	}
	connection := pooled.Hijack()
	defer func() {
		listener.connected.Store(false)
		listener.backendPID.Store(0)
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), policyListenerCloseTimeout)
		_ = connection.Close(closeCtx)
		cancel()
	}()
	if _, err := connection.Exec(ctx, "LISTEN "+listener.channel); err != nil {
		return fmt.Errorf("postgres: listen for policy publication: %w", err)
	}

	listener.backendPID.Store(connection.PgConn().PID())
	listener.connected.Store(true)
	listener.connections.Add(1)

	if _, _, err := listener.reloader.ReloadActive(ctx, listener.policyName); err != nil &&
		!errors.Is(err, persistence.ErrStoredPolicyNotFound) {
		return fmt.Errorf("reload active policy after LISTEN: %w", err)
	} else if err == nil {
		listener.reloaded.Add(1)
	}
	listener.readyOnce.Do(func() { close(listener.ready) })

	for {
		notification, err := connection.WaitForNotification(ctx)
		if err != nil {
			return fmt.Errorf("postgres: wait for policy notification: %w", err)
		}
		listener.notifications.Add(1)
		hash, ok := decodePolicyNotificationHash(notification.Payload)
		if notification.Channel != listener.channel || !ok {
			listener.ignored.Add(1)
			continue
		}
		_, version, err := listener.reloader.ReloadActive(ctx, listener.policyName)
		if err != nil {
			if errors.Is(err, persistence.ErrStoredPolicyNotFound) {
				listener.ignored.Add(1)
				continue
			}
			return fmt.Errorf("reload notified policy: %w", err)
		}
		if version.ContentHash != hash {
			listener.ignored.Add(1)
			continue
		}
		listener.reloaded.Add(1)
	}
}

func policyNotificationChannel(policyName string) string {
	digest := sha256.Sum256([]byte(policyName))
	var channel [len(policyNotificationChannelPrefix) + 46]byte
	copy(channel[:], policyNotificationChannelPrefix)
	hex.Encode(channel[len(policyNotificationChannelPrefix):], digest[:23])
	return string(channel[:])
}

func decodePolicyNotificationHash(payload string) ([sha256.Size]byte, bool) {
	return wire.DecodeSHA256(payload)
}
