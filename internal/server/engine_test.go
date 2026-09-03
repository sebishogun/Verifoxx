package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/buildinfo"
	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/observability"
	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/security"
	"github.com/sebishogun/nornrune/internal/service"
	"github.com/sebishogun/nornrune/internal/simdops"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestEngineEvaluatesGoldenAndSubmitsRequiredAudit(t *testing.T) {
	capacity := persistence.AuditCapacity{Bytes: 64 << 10, Requests: 16, Evidence: 16, Rows: 16, EvidenceLinks: 64}
	store := &memoryPolicyStore{}
	registry := &program.Registry{}
	publisher, err := persistence.NewPublisher(store, registry, compilePolicySource, buildinfo.Version())
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	journal := &captureJournal{capacity: capacity}
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		QueueDepth: func() uint64 { return 0 }, JournalFailures: func() uint64 { return 0 },
		SIMDTier: simdops.Runtime().Tier, Workers: 1,
	})
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	engine, err := NewEngine(EngineConfig{
		Registry: registry, Publisher: publisher, Journal: journal, Metrics: metrics,
		Health: func(context.Context) error { return nil }, EngineVersion: buildinfo.Version(),
		AuditMode: persistence.AuditRequired, AuditCapacity: capacity, Limits: security.DefaultLimits(), Workers: 1, QueueDepth: 1,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	t.Cleanup(func() {
		if err := engine.Close(context.Background()); err != nil {
			t.Errorf("Engine.Close() error = %v", err)
		}
	})
	metadata, err := engine.CompilePolicy(context.Background(), []byte(nornrune.Source()))
	if err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	if metadata.ContentHash == [sha256.Size]byte{} || string(metadata.Name) != "nornrune" {
		t.Fatalf("CompilePolicy() metadata = %+v", metadata)
	}

	got, err := engine.EvaluateBatch(context.Background(), service.EvaluationRequest{
		Requests: []byte(fixtures.RequestsJSON()), Evidence: []byte(fixtures.EvidenceJSON()),
	}, nil)
	if err != nil {
		t.Fatalf("EvaluateBatch() error = %v", err)
	}
	want, err := os.ReadFile("../../testdata/golden/requests.json")
	if err != nil {
		t.Fatalf("read golden results: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("EvaluateBatch() differs from golden\n%s", got)
	}
	if journal.calls != 1 || journal.batch.Rows != 5 {
		t.Fatalf("journal = %d calls, %d rows", journal.calls, journal.batch.Rows)
	}
	if err := persistence.ValidateAuditBatch(&journal.batch); err != nil {
		t.Fatalf("ValidateAuditBatch() error = %v", err)
	}
	if err := engine.Health(context.Background()); err != nil {
		t.Fatalf("Health() error = %v", err)
	}
	if stats := engine.scheduler.Stats(); stats.Executions != 1 || stats.Serial != 1 || stats.Parallel != 0 {
		t.Fatalf("scheduler stats = %+v, want one serial execution", stats)
	}
}

func TestEngineUsesParallelSchedulerAndCloses(t *testing.T) {
	engine := newSecurityTestEngineWithWorkers(t, security.DefaultLimits(), 2)
	request := service.EvaluationRequest{
		Requests: schedulerTestRequests(256),
		Evidence: []byte(fixtures.EvidenceJSON()),
	}
	output, err := engine.EvaluateBatch(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("EvaluateBatch() error = %v", err)
	}
	if len(output) == 0 {
		t.Fatal("EvaluateBatch() output is empty")
	}
	if stats := engine.scheduler.Stats(); stats.Executions != 1 || stats.Serial != 0 || stats.Parallel != 1 {
		t.Fatalf("scheduler stats = %+v, want one parallel execution", stats)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close(canceled) error = %v, want context cancellation", err)
	}
	if err := engine.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := engine.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := engine.EvaluateBatch(context.Background(), request, nil); !errors.Is(err, service.ErrUnavailable) {
		t.Fatalf("EvaluateBatch() after close error = %v, want %v", err, service.ErrUnavailable)
	}
}

func schedulerTestRequests(rows int) []byte {
	request := []byte(`{"requester":{"team":"external_partner","trust":"external"},"action":{"type":"aggregate_analysis","output":"aggregate_counts","dataset":"protected_dataset"},"environment":{"execution_env":"local_approved_env","usage":"standard"},"evidence_refs":["E1","E2"]}`)
	dst := make([]byte, 0, rows*(len(request)+16))
	dst = append(dst, `{"schema_version":1,"pack":"nornrune","requests":[`...)
	for row := 1; row <= rows; row++ {
		if row != 1 {
			dst = append(dst, ',')
		}
		dst = append(dst, `{"id":"R`...)
		dst = strconv.AppendInt(dst, int64(row), 10)
		dst = append(dst, `",`...)
		dst = append(dst, request[1:]...)
	}
	return append(dst, "]}"...)
}

type memoryPolicyStore struct {
	version persistence.PolicyVersion
}

func (store *memoryPolicyStore) PublishActive(_ context.Context, candidate persistence.Candidate) (persistence.PolicyVersion, error) {
	store.version = persistence.PolicyVersion{
		Name: candidate.Name, SemanticVersion: candidate.SemanticVersion, CompilerVersion: candidate.CompilerVersion,
		PublishedAt: time.Unix(1, 0), Source: append([]byte(nil), candidate.Source...), ContentHash: candidate.ContentHash,
		PolicyID: 1, ID: 1,
	}
	return store.version, nil
}

func (store *memoryPolicyStore) LoadActive(_ context.Context, name string) (persistence.PolicyVersion, error) {
	if store.version.Name != name {
		return persistence.PolicyVersion{}, persistence.ErrStoredPolicyNotFound
	}
	return store.version, nil
}

func (store *memoryPolicyStore) LoadByHash(_ context.Context, hash [sha256.Size]byte) (persistence.PolicyVersion, error) {
	if store.version.ContentHash != hash {
		return persistence.PolicyVersion{}, persistence.ErrStoredPolicyNotFound
	}
	return store.version, nil
}

type captureJournal struct {
	batch    persistence.AuditBatch
	capacity persistence.AuditCapacity
	onSubmit func()
	calls    int
}

func (journal *captureJournal) Submit(_ context.Context, source *persistence.AuditBatch) error {
	if journal.batch.Bytes == nil {
		batch, err := persistence.NewAuditBatch(journal.capacity)
		if err != nil {
			return err
		}
		journal.batch = batch
	}
	if err := persistence.CopyAuditBatch(&journal.batch, source); err != nil {
		return err
	}
	journal.calls++
	if journal.onSubmit != nil {
		journal.onSubmit()
	}
	return nil
}

func TestEngineRejectsUnavailableDependencies(t *testing.T) {
	if engine, err := NewEngine(EngineConfig{}); engine != nil || !errors.Is(err, ErrInvalidRuntime) {
		t.Fatalf("NewEngine() = (%p, %v), want invalid runtime", engine, err)
	}
}
