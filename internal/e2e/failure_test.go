package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/observability"
	"github.com/sebishogun/verifoxx/internal/persistence"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/security"
	"github.com/sebishogun/verifoxx/internal/server"
	"github.com/sebishogun/verifoxx/internal/service"
	"github.com/sebishogun/verifoxx/internal/simdops"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

func TestPolicyReloadDuringEvaluationKeepsCapturedProgram(t *testing.T) {
	journal := &failureJournal{entered: make(chan struct{}), release: make(chan struct{})}
	released := false
	t.Cleanup(func() {
		if !released {
			close(journal.release)
		}
	})
	engine := newFailureEngine(t, persistence.AuditRequired, journal)

	firstDone := make(chan evaluationResult, 1)
	go func() {
		output, err := engine.EvaluateBatch(context.Background(), failureEvaluationRequest(), nil)
		firstDone <- evaluationResult{output: output, err: err}
	}()
	waitFailureSignal(t, journal.entered, "first audit submission")

	secondContext := &observedDoneContext{Context: context.Background(), observed: make(chan struct{})}
	secondDone := make(chan evaluationResult, 1)
	go func() {
		output, err := engine.EvaluateBatch(secondContext, failureEvaluationRequest(), nil)
		secondDone <- evaluationResult{output: output, err: err}
	}()
	waitFailureSignal(t, secondContext.observed, "second evaluation worker wait")

	sourceV2 := bytes.Replace(
		[]byte(verifoxx.Source()),
		[]byte(`"version": "1.0.0"`),
		[]byte(`"version": "2.0.0"`),
		1,
	)
	if bytes.Equal(sourceV2, []byte(verifoxx.Source())) {
		t.Fatal("policy version fixture was not replaced")
	}
	if _, err := engine.CompilePolicy(context.Background(), sourceV2); err != nil {
		t.Fatalf("CompilePolicy(v2) error = %v", err)
	}
	close(journal.release)
	released = true

	first := receiveEvaluation(t, firstDone, "first evaluation")
	second := receiveEvaluation(t, secondDone, "second evaluation")
	if first.err != nil || second.err != nil {
		t.Fatalf("captured evaluations errors = (%v, %v)", first.err, second.err)
	}
	if version := resultPolicyVersion(t, first.output); version != "1.0.0" {
		t.Fatalf("first evaluation policy version = %q, want 1.0.0", version)
	}
	if version := resultPolicyVersion(t, second.output); version != "1.0.0" {
		t.Fatalf("queued evaluation policy version = %q, want captured 1.0.0", version)
	}

	thirdOutput, err := engine.EvaluateBatch(context.Background(), failureEvaluationRequest(), nil)
	if err != nil {
		t.Fatalf("EvaluateBatch(after reload) error = %v", err)
	}
	if version := resultPolicyVersion(t, thirdOutput); version != "2.0.0" {
		t.Fatalf("post-reload evaluation policy version = %q, want 2.0.0", version)
	}
	if calls := journal.calls.Load(); calls != 3 {
		t.Fatalf("audit submissions = %d, want 3", calls)
	}
}

func TestAuditFailureModeControlsEvaluationAvailability(t *testing.T) {
	storeFailure := errors.New("audit store unavailable")
	for _, test := range []struct {
		name string
		mode persistence.AuditMode
	}{
		{name: "required", mode: persistence.AuditRequired},
		{name: "best_effort", mode: persistence.AuditBestEffort},
	} {
		t.Run(test.name, func(t *testing.T) {
			journal := &failureJournal{err: storeFailure}
			engine := newFailureEngine(t, test.mode, journal)
			output, err := engine.EvaluateBatch(context.Background(), failureEvaluationRequest(), nil)
			if test.mode == persistence.AuditRequired {
				if output != nil || !errors.Is(err, service.ErrAuditUnavailable) {
					t.Fatalf("required evaluation = (%d bytes, %v), want nil and %v", len(output), err, service.ErrAuditUnavailable)
				}
				return
			}
			if err != nil {
				t.Fatalf("best-effort evaluation error = %v", err)
			}
			if version := resultPolicyVersion(t, output); version != "1.0.0" {
				t.Fatalf("best-effort policy version = %q, want 1.0.0", version)
			}
		})
	}
}

type evaluationResult struct {
	output []byte
	err    error
}

type observedDoneContext struct {
	context.Context
	once     sync.Once
	observed chan struct{}
}

func (ctx *observedDoneContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}

type failureJournal struct {
	err     error
	entered chan struct{}
	release chan struct{}
	calls   atomic.Uint32
}

func (journal *failureJournal) Submit(ctx context.Context, _ *persistence.AuditBatch) error {
	call := journal.calls.Add(1)
	if call == 1 && journal.entered != nil {
		close(journal.entered)
		select {
		case <-journal.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return journal.err
}

type failurePolicyStore struct {
	next atomic.Int64
}

func (store *failurePolicyStore) PublishActive(
	_ context.Context,
	candidate persistence.Candidate,
) (persistence.PolicyVersion, error) {
	id := store.next.Add(1)
	return persistence.PolicyVersion{
		Name:            candidate.Name,
		SemanticVersion: candidate.SemanticVersion,
		CompilerVersion: candidate.CompilerVersion,
		PublishedAt:     time.Unix(id, 0).UTC(),
		Source:          bytes.Clone(candidate.Source),
		ContentHash:     candidate.ContentHash,
		PolicyID:        1,
		ID:              persistence.PolicyVersionID(id),
	}, nil
}

func (*failurePolicyStore) LoadActive(context.Context, string) (persistence.PolicyVersion, error) {
	return persistence.PolicyVersion{}, persistence.ErrStoredPolicyNotFound
}

func (*failurePolicyStore) LoadByHash(context.Context, [sha256.Size]byte) (persistence.PolicyVersion, error) {
	return persistence.PolicyVersion{}, persistence.ErrStoredPolicyNotFound
}

func newFailureEngine(t *testing.T, mode persistence.AuditMode, journal *failureJournal) *server.Engine {
	t.Helper()
	registry := &program.Registry{}
	publisher, err := persistence.NewPublisher(&failurePolicyStore{}, registry, compileFailurePolicy, "failure-test")
	if err != nil {
		t.Fatalf("NewPublisher() error = %v", err)
	}
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		QueueDepth:      func() uint64 { return 0 },
		JournalFailures: func() uint64 { return 0 },
		SIMDTier:        simdops.Runtime().Tier,
		Workers:         1,
	})
	if err != nil {
		t.Fatalf("NewMetrics() error = %v", err)
	}
	engine, err := server.NewEngine(server.EngineConfig{
		Registry: registry, Publisher: publisher, Journal: journal, Metrics: metrics,
		Health: func(context.Context) error { return nil }, EngineVersion: "failure-test",
		AuditMode: mode,
		AuditCapacity: persistence.AuditCapacity{
			Bytes: 64 << 10, Requests: 16, Evidence: 32, Rows: 16, EvidenceLinks: 64,
		},
		Limits: security.DefaultLimits(), Workers: 1,
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	if _, err := engine.CompilePolicy(context.Background(), []byte(verifoxx.Source())); err != nil {
		t.Fatalf("CompilePolicy(v1) error = %v", err)
	}
	return engine
}

func compileFailurePolicy(source []byte) (*program.Program, error) {
	fields, symbols, err := verifoxx.NewSchema()
	if err != nil {
		return nil, err
	}
	builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	if err := jsonpolicy.Decode(builder, source, fields, symbols, jsonpolicy.Limits{}); err != nil {
		return nil, err
	}
	return compile.Lower(builder.Document(), fields, symbols)
}

func failureEvaluationRequest() service.EvaluationRequest {
	return service.EvaluationRequest{
		Requests: []byte(fixtures.RequestsJSON()),
		Evidence: []byte(fixtures.EvidenceJSON()),
	}
}

func resultPolicyVersion(t *testing.T, output []byte) string {
	t.Helper()
	var envelope struct {
		Policy struct {
			Version string `json:"version"`
		} `json:"policy"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("decode result envelope: %v\n%s", err, output)
	}
	return envelope.Policy.Version
}

func waitFailureSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func receiveEvaluation(t *testing.T, result <-chan evaluationResult, label string) evaluationResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return evaluationResult{}
	}
}
