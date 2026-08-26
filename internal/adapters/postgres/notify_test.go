//go:build integration

package postgres

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/program"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func testPolicyNotifications(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct notification policy store: %v", err)
	}
	publishingRegistry := &program.Registry{}
	publisher, err := persistence.NewPublisher(
		store,
		publishingRegistry,
		compileIntegrationPolicy,
		integrationCompilerVersion,
	)
	if err != nil {
		t.Fatalf("construct notification publisher: %v", err)
	}
	reloadingRegistry := &program.Registry{}
	reloader, err := persistence.NewPublisher(
		store,
		reloadingRegistry,
		compileIntegrationPolicy,
		integrationCompilerVersion,
	)
	if err != nil {
		t.Fatalf("construct notification reloader: %v", err)
	}
	listener, err := NewPolicyListener(environment.runtime, reloader, "nornrune", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("construct policy listener: %v", err)
	}

	opCtx, opCancel := context.WithTimeout(ctx, 20*time.Second)
	defer opCancel()
	runCtx, stop := context.WithCancel(opCtx)
	runDone := make(chan error, 1)
	go func() {
		runDone <- listener.Run(runCtx)
	}()
	stopped := false
	t.Cleanup(func() {
		stop()
		if stopped {
			return
		}
		select {
		case <-runDone:
		case <-opCtx.Done():
		}
	})
	select {
	case <-listener.Ready():
	case err := <-runDone:
		t.Fatalf("policy listener stopped before ready: %v", err)
	case <-opCtx.Done():
		t.Fatalf("wait for policy listener readiness: %v", opCtx.Err())
	}

	compiledV1, versionV1, err := publisher.Publish(opCtx, []byte(nornrune.Source()))
	if err != nil {
		t.Fatalf("publish notification policy v1: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool {
		active := reloadingRegistry.Active()
		return active != nil && active.ContentHash == versionV1.ContentHash
	})
	if active := reloadingRegistry.Active(); active == compiledV1 {
		t.Fatal("notification reloader reused the publishing process Program pointer")
	}
	stats := listener.Stats()
	if stats.Connections != 1 || !stats.Connected || stats.BackendPID == 0 ||
		stats.Notifications != 1 || stats.Reloaded != 1 || stats.Ignored != 0 {
		t.Fatalf("listener stats after publication = %+v", stats)
	}

	activeV1 := reloadingRegistry.Active()
	otherSource := bytes.Replace(
		[]byte(nornrune.Source()),
		[]byte(`"name": "nornrune"`),
		[]byte(`"name": "other-policy"`),
		1,
	)
	if bytes.Equal(otherSource, []byte(nornrune.Source())) {
		t.Fatal("other-policy fixture did not replace the policy name")
	}
	otherProgram, err := compileIntegrationPolicy(otherSource)
	if err != nil {
		t.Fatalf("compile unrelated policy: %v", err)
	}
	otherCandidate := policyCandidateFromProgram(t, otherProgram)
	if _, err := store.PublishActive(opCtx, otherCandidate); err != nil {
		t.Fatalf("publish unrelated policy notification: %v", err)
	}
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, hex.EncodeToString(otherCandidate.ContentHash[:]),
	); err != nil {
		t.Fatalf("send foreign policy hash on listener channel: %v", err)
	}
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, "not-a-policy-hash",
	); err != nil {
		t.Fatalf("send policy-isolation barrier notification: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool { return listener.Stats().Ignored >= stats.Ignored+2 })
	isolationStats := listener.Stats()
	if isolationStats.Notifications != stats.Notifications+2 || isolationStats.Reloaded != stats.Reloaded ||
		isolationStats.Ignored != stats.Ignored+2 {
		t.Fatalf("listener processed unrelated policy notification: before=%+v after=%+v", stats, isolationStats)
	}
	if active := reloadingRegistry.Active(); active != activeV1 {
		t.Fatalf("unrelated policy notification changed active Program: got %p want %p", active, activeV1)
	}

	stats = isolationStats
	payload := hex.EncodeToString(versionV1.ContentHash[:])
	for range 2 {
		if _, err := environment.runtime.Exec(opCtx,
			"SELECT pg_notify($1, $2)", listener.channel, payload,
		); err != nil {
			t.Fatalf("send duplicate policy notification: %v", err)
		}
	}
	waitPolicyListener(t, opCtx, func() bool {
		return listener.Stats().Reloaded >= stats.Reloaded+2
	})
	if active := reloadingRegistry.Active(); active != activeV1 {
		t.Fatalf("duplicate notification changed canonical Program: got %p want %p", active, activeV1)
	}
	duplicateStats := listener.Stats()
	if duplicateStats.Reloaded != stats.Reloaded+2 {
		t.Fatalf("successful reload count after duplicates = %d, want %d", duplicateStats.Reloaded, stats.Reloaded+2)
	}
	stats = duplicateStats
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, "not-a-policy-hash",
	); err != nil {
		t.Fatalf("send malformed policy notification: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool {
		return listener.Stats().Ignored >= stats.Ignored+1
	})
	stats = listener.Stats()
	transaction, err := environment.runtime.Begin(opCtx)
	if err != nil {
		t.Fatalf("begin rolled-back notification: %v", err)
	}
	if _, err := transaction.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, payload,
	); err != nil {
		_ = transaction.Rollback(opCtx)
		t.Fatalf("send rolled-back policy notification: %v", err)
	}
	if err := transaction.Rollback(opCtx); err != nil {
		t.Fatalf("rollback policy notification: %v", err)
	}
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, "rollback-barrier",
	); err != nil {
		t.Fatalf("send rollback barrier notification: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool { return listener.Stats().Ignored >= stats.Ignored+1 })
	rollbackStats := listener.Stats()
	if rollbackStats.Notifications != stats.Notifications+1 || rollbackStats.Reloaded != stats.Reloaded {
		t.Fatalf("rolled-back notification was delivered: before=%+v after=%+v", stats, rollbackStats)
	}

	oldPID := listener.Stats().BackendPID
	var terminated bool
	if err := environment.admin.QueryRow(opCtx, "SELECT pg_terminate_backend($1)", oldPID).Scan(&terminated); err != nil {
		t.Fatalf("terminate notification backend %d: %v", oldPID, err)
	}
	if !terminated {
		t.Fatalf("notification backend %d was not terminated", oldPID)
	}
	compiledV2, versionV2, err := publisher.Publish(opCtx, nornruneSourceVersion(t, "2.0.0"))
	if err != nil {
		t.Fatalf("publish notification policy v2 during reconnect: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool {
		listenerStats := listener.Stats()
		active := reloadingRegistry.Active()
		return listenerStats.Connections >= 2 && listenerStats.Reconnects >= 1 &&
			listenerStats.BackendPID != 0 && listenerStats.BackendPID != oldPID &&
			active != nil && active.ContentHash == versionV2.ContentHash
	})
	if active := reloadingRegistry.Active(); active == compiledV2 {
		t.Fatal("reconnect catch-up reused the publishing process Program pointer")
	}
	reconnectStats := listener.Stats()
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, "reconnect-settle-barrier",
	); err != nil {
		t.Fatalf("send reconnect settle notification: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool { return listener.Stats().Ignored >= reconnectStats.Ignored+1 })

	activeV2 := reloadingRegistry.Active()
	stats = listener.Stats()
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, hex.EncodeToString(versionV1.ContentHash[:]),
	); err != nil {
		t.Fatalf("send stale policy notification: %v", err)
	}
	if _, err := environment.runtime.Exec(opCtx,
		"SELECT pg_notify($1, $2)", listener.channel, "stale-policy-barrier",
	); err != nil {
		t.Fatalf("send stale-policy barrier notification: %v", err)
	}
	waitPolicyListener(t, opCtx, func() bool { return listener.Stats().Ignored >= stats.Ignored+2 })
	staleStats := listener.Stats()
	if staleStats.Reloaded != stats.Reloaded || staleStats.Ignored != stats.Ignored+2 {
		t.Fatalf("listener processed stale policy notification: before=%+v after=%+v", stats, staleStats)
	}
	if active := reloadingRegistry.Active(); active != activeV2 {
		t.Fatalf("stale policy notification changed active Program: got %p want %p", active, activeV2)
	}

	stop()
	select {
	case err := <-runDone:
		stopped = true
		if err != nil {
			t.Fatalf("stop policy listener: %v", err)
		}
	case <-opCtx.Done():
		t.Fatalf("wait for policy listener shutdown: %v", opCtx.Err())
	}
	stats = listener.Stats()
	if stats.Connected || stats.BackendPID != 0 {
		t.Fatalf("listener stats after shutdown = %+v", stats)
	}
	if err := listener.Run(context.Background()); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("second policy listener Run error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
}

func testPolicyNotificationStartupCatchup(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct startup notification store: %v", err)
	}
	publisher, err := persistence.NewPublisher(
		store,
		&program.Registry{},
		compileIntegrationPolicy,
		integrationCompilerVersion,
	)
	if err != nil {
		t.Fatalf("construct startup notification publisher: %v", err)
	}
	_, version, err := publisher.Publish(ctx, []byte(nornrune.Source()))
	if err != nil {
		t.Fatalf("publish policy before listener startup: %v", err)
	}

	registry := &program.Registry{}
	reloader, err := persistence.NewPublisher(store, registry, compileIntegrationPolicy, integrationCompilerVersion)
	if err != nil {
		t.Fatalf("construct startup notification reloader: %v", err)
	}
	listener, err := NewPolicyListener(environment.runtime, reloader, version.Name, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("construct startup policy listener: %v", err)
	}
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runCtx, stop := context.WithCancel(opCtx)
	done := make(chan error, 1)
	go func() { done <- listener.Run(runCtx) }()
	select {
	case <-listener.Ready():
	case err := <-done:
		t.Fatalf("startup policy listener stopped before ready: %v", err)
	case <-opCtx.Done():
		t.Fatalf("wait for startup policy listener: %v", opCtx.Err())
	}
	active := registry.Active()
	if active == nil || active.ContentHash != version.ContentHash {
		t.Fatalf("startup catch-up active Program = %p, want hash %x", active, version.ContentHash)
	}
	stats := listener.Stats()
	if stats.Connections != 1 || stats.Notifications != 0 || stats.Reloaded != 1 || !stats.Connected {
		t.Fatalf("startup catch-up listener stats = %+v", stats)
	}
	backendPID := stats.BackendPID
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stop startup policy listener: %v", err)
		}
	case <-opCtx.Done():
		t.Fatalf("wait for startup listener shutdown: %v", opCtx.Err())
	}
	waitPolicyListener(t, opCtx, func() bool {
		return queryCount(t, opCtx, environment.admin,
			"SELECT count(*) FROM pg_stat_activity WHERE pid = $1", backendPID,
		) == 0
	})
}

func waitPolicyListener(t *testing.T, ctx context.Context, condition func() bool) {
	t.Helper()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatalf("wait for policy listener condition: %v", ctx.Err())
		}
	}
}
