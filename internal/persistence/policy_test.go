package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

type fakePolicyStore struct {
	publishActive func(context.Context, Candidate) (PolicyVersion, error)
	loadActive    func(context.Context, string) (PolicyVersion, error)
	loadByHash    func(context.Context, [sha256.Size]byte) (PolicyVersion, error)
}

func (store *fakePolicyStore) PublishActive(ctx context.Context, candidate Candidate) (PolicyVersion, error) {
	return store.publishActive(ctx, candidate)
}

func (store *fakePolicyStore) LoadActive(ctx context.Context, name string) (PolicyVersion, error) {
	return store.loadActive(ctx, name)
}

func (store *fakePolicyStore) LoadByHash(ctx context.Context, hash [sha256.Size]byte) (PolicyVersion, error) {
	return store.loadByHash(ctx, hash)
}

func testCandidate() Candidate {
	source := []byte(`{"name":"policy"}`)
	compiled := testProgram(source, "policy", "1.0.0")
	return Candidate{
		Program:         compiled,
		Source:          compiled.InputBytes,
		Name:            "policy",
		SemanticVersion: "1.0.0",
		CompilerVersion: "test-compiler",
		ContentHash:     compiled.ContentHash,
	}
}

func testPolicyVersion() PolicyVersion {
	candidate := testCandidate()
	return PolicyVersion{
		Source:          candidate.Source,
		Name:            candidate.Name,
		SemanticVersion: candidate.SemanticVersion,
		CompilerVersion: candidate.CompilerVersion,
		PublishedAt:     time.Unix(1_777_777_777, 0).UTC(),
		ContentHash:     candidate.ContentHash,
		PolicyID:        1,
		ID:              2,
	}
}

func testProgram(source []byte, name, semanticVersion string) *program.Program {
	input := append([]byte(nil), source...)
	symbols := make([]byte, 0, len(name)+len(semanticVersion))
	symbols = append(symbols, name...)
	versionStart := len(symbols)
	symbols = append(symbols, semanticVersion...)
	return &program.Program{
		InputBytes:    input,
		SymbolBytes:   symbols,
		SymbolStarts:  []uint32{0, uint32(versionStart)},
		SymbolLengths: []uint32{uint32(len(name)), uint32(len(semanticVersion))},
		ContentHash:   sha256.Sum256(input),
		PolicyName:    schema.SymbolID(1),
		PolicyVersion: schema.SymbolID(2),
	}
}

func testStoredCandidate(candidate Candidate) PolicyVersion {
	return PolicyVersion{
		Source:          append([]byte(nil), candidate.Source...),
		Name:            candidate.Name,
		SemanticVersion: candidate.SemanticVersion,
		CompilerVersion: candidate.CompilerVersion,
		PublishedAt:     time.Unix(1_777_777_777, 0).UTC(),
		ContentHash:     candidate.ContentHash,
		PolicyID:        1,
		ID:              2,
	}
}

func isZeroPolicyVersion(version PolicyVersion) bool {
	return len(version.Source) == 0 && version.Name == "" && version.SemanticVersion == "" &&
		version.CompilerVersion == "" && version.PublishedAt.IsZero() &&
		version.ContentHash == [sha256.Size]byte{} && version.PolicyID == 0 && version.ID == 0
}

type publishTestResult struct {
	compiled *program.Program
	version  PolicyVersion
	err      error
}

func waitTestSignal(t *testing.T, signal <-chan struct{}, label string) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-signal:
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", label)
	}
}

func receivePublishResult(t *testing.T, result <-chan publishTestResult, label string) publishTestResult {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case got := <-result:
		return got
	case <-timer.C:
		t.Fatalf("timed out waiting for %s", label)
		return publishTestResult{}
	}
}

func TestValidateCandidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := ValidateCandidate(testCandidate()); err != nil {
			t.Fatalf("ValidateCandidate() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*Candidate)
	}{
		{name: "nil_source", mutate: func(candidate *Candidate) { candidate.Source = nil }},
		{name: "empty_source", mutate: func(candidate *Candidate) { candidate.Source = []byte{} }},
		{name: "empty_name", mutate: func(candidate *Candidate) { candidate.Name = "" }},
		{name: "blank_name", mutate: func(candidate *Candidate) { candidate.Name = " \t\n" }},
		{name: "padded_name", mutate: func(candidate *Candidate) { candidate.Name = " policy " }},
		{name: "empty_semantic_version", mutate: func(candidate *Candidate) { candidate.SemanticVersion = "" }},
		{name: "blank_semantic_version", mutate: func(candidate *Candidate) { candidate.SemanticVersion = " \t" }},
		{name: "padded_semantic_version", mutate: func(candidate *Candidate) { candidate.SemanticVersion = " 1.0.0" }},
		{name: "empty_compiler_version", mutate: func(candidate *Candidate) { candidate.CompilerVersion = "" }},
		{name: "blank_compiler_version", mutate: func(candidate *Candidate) { candidate.CompilerVersion = "\n" }},
		{name: "padded_compiler_version", mutate: func(candidate *Candidate) { candidate.CompilerVersion = "compiler " }},
		{name: "zero_hash", mutate: func(candidate *Candidate) { candidate.ContentHash = [sha256.Size]byte{} }},
		{name: "mismatched_hash", mutate: func(candidate *Candidate) { candidate.ContentHash[0] ^= 0xff }},
		{name: "nil_program", mutate: func(candidate *Candidate) { candidate.Program = nil }},
		{name: "program_source", mutate: func(candidate *Candidate) {
			candidate.Program = testProgram([]byte(`{"name":"other"}`), candidate.Name, candidate.SemanticVersion)
		}},
		{name: "program_name", mutate: func(candidate *Candidate) {
			candidate.Program = testProgram(candidate.Source, "other", candidate.SemanticVersion)
		}},
		{name: "program_version", mutate: func(candidate *Candidate) {
			candidate.Program = testProgram(candidate.Source, candidate.Name, "2.0.0")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := testCandidate()
			test.mutate(&candidate)
			if err := ValidateCandidate(candidate); !errors.Is(err, ErrInvalidPolicyPersistence) {
				t.Fatalf("ValidateCandidate() error = %v, want %v", err, ErrInvalidPolicyPersistence)
			}
		})
	}
}

func TestValidatePolicyVersion(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		if err := ValidatePolicyVersion(testPolicyVersion()); err != nil {
			t.Fatalf("ValidatePolicyVersion() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*PolicyVersion)
	}{
		{name: "zero_policy_id", mutate: func(version *PolicyVersion) { version.PolicyID = 0 }},
		{name: "negative_policy_id", mutate: func(version *PolicyVersion) { version.PolicyID = PolicyID(-1) }},
		{name: "zero_version_id", mutate: func(version *PolicyVersion) { version.ID = 0 }},
		{name: "negative_version_id", mutate: func(version *PolicyVersion) { version.ID = PolicyVersionID(-1) }},
		{name: "zero_published_at", mutate: func(version *PolicyVersion) { version.PublishedAt = time.Time{} }},
		{name: "nil_source", mutate: func(version *PolicyVersion) { version.Source = nil }},
		{name: "empty_name", mutate: func(version *PolicyVersion) { version.Name = "" }},
		{name: "padded_name", mutate: func(version *PolicyVersion) { version.Name = " policy" }},
		{name: "blank_semantic_version", mutate: func(version *PolicyVersion) { version.SemanticVersion = " " }},
		{name: "padded_semantic_version", mutate: func(version *PolicyVersion) { version.SemanticVersion = "1.0.0 " }},
		{name: "blank_compiler_version", mutate: func(version *PolicyVersion) { version.CompilerVersion = "\t" }},
		{name: "padded_compiler_version", mutate: func(version *PolicyVersion) { version.CompilerVersion = " compiler" }},
		{name: "zero_hash", mutate: func(version *PolicyVersion) { version.ContentHash = [sha256.Size]byte{} }},
		{name: "mismatched_hash", mutate: func(version *PolicyVersion) { version.ContentHash[0] ^= 0xff }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version := testPolicyVersion()
			test.mutate(&version)
			if err := ValidatePolicyVersion(version); !errors.Is(err, ErrStoredPolicyCorrupt) {
				t.Fatalf("ValidatePolicyVersion() error = %v, want %v", err, ErrStoredPolicyCorrupt)
			}
		})
	}
}

func TestPublisher(t *testing.T) {
	const compilerVersion = "test-compiler"
	source := []byte(`{"name":"policy"}`)

	t.Run("constructor_rejects_invalid_dependencies", func(t *testing.T) {
		registry := &program.Registry{}
		store := &fakePolicyStore{}
		compile := func([]byte) (*program.Program, error) {
			t.Fatal("compiler called by constructor")
			return nil, nil
		}

		tests := []struct {
			name            string
			store           PolicyStore
			registry        *program.Registry
			compile         CompileFunc
			compilerVersion string
		}{
			{name: "nil_store", registry: registry, compile: compile, compilerVersion: compilerVersion},
			{name: "nil_registry", store: store, compile: compile, compilerVersion: compilerVersion},
			{name: "nil_compiler", store: store, registry: registry, compilerVersion: compilerVersion},
			{name: "empty_compiler_version", store: store, registry: registry, compile: compile},
			{name: "blank_compiler_version", store: store, registry: registry, compile: compile, compilerVersion: " \t"},
			{name: "padded_compiler_version", store: store, registry: registry, compile: compile, compilerVersion: " compiler"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				publisher, err := NewPublisher(test.store, test.registry, test.compile, test.compilerVersion)
				if publisher != nil || !errors.Is(err, ErrInvalidPolicyPersistence) {
					t.Fatalf("NewPublisher() = (%p, %v), want (nil, %v)", publisher, err, ErrInvalidPolicyPersistence)
				}
			})
		}
	})

	t.Run("publish_rejects_invalid_inputs_before_compile", func(t *testing.T) {
		compileCalls := 0
		publisher, err := NewPublisher(
			&fakePolicyStore{},
			&program.Registry{},
			func([]byte) (*program.Program, error) {
				compileCalls++
				return nil, nil
			},
			compilerVersion,
		)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		tests := []struct {
			name      string
			publisher *Publisher
			ctx       context.Context
			source    []byte
		}{
			{name: "nil_receiver", ctx: context.Background(), source: source},
			{name: "nil_context", publisher: publisher, source: source},
			{name: "empty_source", publisher: publisher, ctx: context.Background()},
			{name: "canceled_context", publisher: publisher, ctx: canceled, source: source},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				compiled, version, err := test.publisher.Publish(test.ctx, test.source)
				if compiled != nil || !isZeroPolicyVersion(version) || !errors.Is(err, ErrInvalidPolicyPersistence) {
					t.Fatalf("Publish() = (%p, %+v, %v), want zero results and %v", compiled, version, err, ErrInvalidPolicyPersistence)
				}
			})
		}
		if compileCalls != 0 {
			t.Fatalf("compile calls = %d, want 0", compileCalls)
		}
	})

	t.Run("publish_preserves_compile_failure", func(t *testing.T) {
		compileErr := errors.New("compile failed")
		storeCalled := false
		store := &fakePolicyStore{publishActive: func(context.Context, Candidate) (PolicyVersion, error) {
			storeCalled = true
			return PolicyVersion{}, nil
		}}
		registry := &program.Registry{}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return nil, compileErr
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		compiled, version, err := publisher.Publish(context.Background(), source)
		if compiled != nil || !isZeroPolicyVersion(version) || !errors.Is(err, compileErr) {
			t.Fatalf("Publish() = (%p, %+v, %v), want compile error %v", compiled, version, err, compileErr)
		}
		if storeCalled || registry.Active() != nil {
			t.Fatalf("compile failure mutated dependencies: store=%v active=%p", storeCalled, registry.Active())
		}
	})

	t.Run("publish_rejects_invalid_programs", func(t *testing.T) {
		tests := []struct {
			name    string
			program func() *program.Program
		}{
			{name: "nil", program: func() *program.Program { return nil }},
			{name: "zero_hash", program: func() *program.Program {
				compiled := testProgram(source, "policy", "1.0.0")
				compiled.ContentHash = [sha256.Size]byte{}
				return compiled
			}},
			{name: "different_source", program: func() *program.Program {
				return testProgram([]byte(`{"name":"other"}`), "policy", "1.0.0")
			}},
			{name: "invalid_name_symbol", program: func() *program.Program {
				compiled := testProgram(source, "policy", "1.0.0")
				compiled.PolicyName = 0
				return compiled
			}},
			{name: "blank_name", program: func() *program.Program { return testProgram(source, " ", "1.0.0") }},
			{name: "invalid_version_symbol", program: func() *program.Program {
				compiled := testProgram(source, "policy", "1.0.0")
				compiled.PolicyVersion = 3
				return compiled
			}},
			{name: "blank_version", program: func() *program.Program { return testProgram(source, "policy", "\t") }},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				storeCalled := false
				store := &fakePolicyStore{publishActive: func(context.Context, Candidate) (PolicyVersion, error) {
					storeCalled = true
					return PolicyVersion{}, nil
				}}
				registry := &program.Registry{}
				publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
					return test.program(), nil
				}, compilerVersion)
				if err != nil {
					t.Fatalf("NewPublisher() error = %v", err)
				}

				compiled, version, err := publisher.Publish(context.Background(), source)
				if compiled != nil || !isZeroPolicyVersion(version) || !errors.Is(err, ErrInvalidPolicyPersistence) {
					t.Fatalf("Publish() = (%p, %+v, %v), want invalid Program", compiled, version, err)
				}
				if storeCalled || registry.Active() != nil {
					t.Fatalf("invalid Program mutated dependencies: store=%v active=%p", storeCalled, registry.Active())
				}
			})
		}
	})

	t.Run("publish_uses_frozen_program_identity", func(t *testing.T) {
		callerSource := append([]byte(nil), source...)
		frozen := testProgram(callerSource, "compiled-policy", "2.3.4")
		registry := &program.Registry{}
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			if candidate.Program != frozen {
				t.Fatalf("candidate Program = %p, want frozen %p", candidate.Program, frozen)
			}
			if len(candidate.Source) == 0 || &candidate.Source[0] != &frozen.InputBytes[0] {
				t.Fatal("candidate source does not borrow Program.InputBytes")
			}
			if &candidate.Source[0] == &callerSource[0] {
				t.Fatal("candidate source borrowed caller input")
			}
			if candidate.Name != "compiled-policy" || candidate.SemanticVersion != "2.3.4" ||
				candidate.CompilerVersion != compilerVersion || candidate.ContentHash != frozen.ContentHash {
				t.Fatalf("candidate = %+v, want frozen Program identity", candidate)
			}
			return testStoredCandidate(candidate), nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return frozen, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, _, err := publisher.Publish(context.Background(), callerSource)
		if err != nil || got != frozen {
			t.Fatalf("Publish() = (%p, %v), want (%p, nil)", got, err, frozen)
		}
	})

	t.Run("publish_store_failure_leaves_registry_unchanged", func(t *testing.T) {
		storeErr := errors.New("store failed")
		compiledBeforeStore := false
		registry := &program.Registry{}
		store := &fakePolicyStore{publishActive: func(context.Context, Candidate) (PolicyVersion, error) {
			if !compiledBeforeStore {
				t.Fatal("store called before compiler")
			}
			return PolicyVersion{}, storeErr
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			compiledBeforeStore = true
			return testProgram(source, "policy", "1.0.0"), nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, version, err := publisher.Publish(context.Background(), source)
		if got != nil || !isZeroPolicyVersion(version) || !errors.Is(err, storeErr) {
			t.Fatalf("Publish() = (%p, %+v, %v), want store error %v", got, version, err, storeErr)
		}
		if registry.Active() != nil {
			t.Fatalf("registry active = %p, want nil", registry.Active())
		}
	})

	t.Run("publish_rejects_invalid_stored_row_before_registry_mutation", func(t *testing.T) {
		compiled := testProgram(source, "policy", "1.0.0")
		registry := &program.Registry{}
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			stored := testStoredCandidate(candidate)
			stored.ContentHash[0] ^= 0xff
			return stored, nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return compiled, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, _, err := publisher.Publish(context.Background(), source)
		if got != nil || !errors.Is(err, ErrPolicyActivation) || !errors.Is(err, ErrStoredPolicyCorrupt) {
			t.Fatalf("Publish() = (%p, %v), want activation corruption", got, err)
		}
		if _, found := registry.Lookup(compiled.ContentHash); found || registry.Active() != nil {
			t.Fatal("invalid stored row mutated registry")
		}
	})

	t.Run("publish_commits_before_registry_activation", func(t *testing.T) {
		compiled := testProgram(source, "policy", "1.0.0")
		registry := &program.Registry{}
		committed := false
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			if _, found := registry.Lookup(candidate.ContentHash); found || registry.Active() != nil {
				t.Fatal("registry mutated before store commit")
			}
			committed = true
			return testStoredCandidate(candidate), nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return compiled, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, version, err := publisher.Publish(context.Background(), source)
		if err != nil || !committed || got != compiled || registry.Active() != compiled {
			t.Fatalf("Publish() = (%p, %+v, %v), committed=%v active=%p", got, version, err, committed, registry.Active())
		}
	})

	t.Run("publish_completes_activation_after_commit_cancellation", func(t *testing.T) {
		compiled := testProgram(source, "policy", "1.0.0")
		registry := &program.Registry{}
		ctx, cancel := context.WithCancel(context.Background())
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			cancel()
			return testStoredCandidate(candidate), nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return compiled, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, _, err := publisher.Publish(ctx, source)
		if err != nil || got != compiled || registry.Active() != compiled {
			t.Fatalf("Publish() = (%p, %v), active=%p, want committed activation", got, err, registry.Active())
		}
	})

	t.Run("publish_returns_first_canonical_program", func(t *testing.T) {
		first := testProgram(source, "policy", "1.0.0")
		second := testProgram(source, "policy", "1.0.0")
		registry := &program.Registry{}
		if _, err := registry.Publish(first); err != nil {
			t.Fatalf("Publish(first) error = %v", err)
		}
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			return testStoredCandidate(candidate), nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return second, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, _, err := publisher.Publish(context.Background(), source)
		if err != nil || got != first || registry.Active() != first {
			t.Fatalf("Publish() = (%p, %v), active=%p, want canonical %p", got, err, registry.Active(), first)
		}
	})

	t.Run("publish_preserves_original_stored_compiler_version", func(t *testing.T) {
		compiled := testProgram(source, "policy", "1.0.0")
		registry := &program.Registry{}
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			stored := testStoredCandidate(candidate)
			stored.CompilerVersion = "original-compiler"
			return stored, nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return compiled, nil
		}, "newer-compiler")
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, version, err := publisher.Publish(context.Background(), source)
		if err != nil || got != compiled || version.CompilerVersion != "original-compiler" || registry.Active() != compiled {
			t.Fatalf("Publish() = (%p, %+v, %v), active=%p", got, version, err, registry.Active())
		}
	})

	t.Run("publish_reports_post_commit_registry_invariant", func(t *testing.T) {
		canonical := testProgram(source, "policy", "1.0.0")
		canonical.InputBytes = []byte("corrupt")
		candidate := testProgram(source, "policy", "1.0.0")
		registry := &program.Registry{}
		if _, err := registry.Publish(canonical); err != nil {
			t.Fatalf("Publish(canonical) error = %v", err)
		}
		store := &fakePolicyStore{publishActive: func(_ context.Context, stored Candidate) (PolicyVersion, error) {
			return testStoredCandidate(stored), nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return candidate, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		got, version, err := publisher.Publish(context.Background(), source)
		if got != nil || version.ID == 0 || !errors.Is(err, ErrPolicyActivation) {
			t.Fatalf("Publish() = (%p, %+v, %v), want durable version and %v", got, version, err, ErrPolicyActivation)
		}
		if registry.Active() != nil {
			t.Fatalf("registry active = %p, want nil", registry.Active())
		}
	})

	t.Run("reload_rejects_invalid_inputs_before_store", func(t *testing.T) {
		storeCalls := 0
		store := &fakePolicyStore{
			loadActive: func(context.Context, string) (PolicyVersion, error) {
				storeCalls++
				return PolicyVersion{}, nil
			},
			loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
				storeCalls++
				return PolicyVersion{}, nil
			},
		}
		publisher, err := NewPublisher(store, &program.Registry{}, func([]byte) (*program.Program, error) {
			t.Fatal("compiler called for invalid reload")
			return nil, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()

		activeTests := []struct {
			name      string
			publisher *Publisher
			ctx       context.Context
			policy    string
		}{
			{name: "nil_receiver", ctx: context.Background(), policy: "policy"},
			{name: "nil_context", publisher: publisher, policy: "policy"},
			{name: "empty_name", publisher: publisher, ctx: context.Background()},
			{name: "blank_name", publisher: publisher, ctx: context.Background(), policy: " \t"},
			{name: "padded_name", publisher: publisher, ctx: context.Background(), policy: " policy "},
			{name: "canceled_context", publisher: publisher, ctx: canceled, policy: "policy"},
		}
		for _, test := range activeTests {
			t.Run("active_"+test.name, func(t *testing.T) {
				compiled, version, err := test.publisher.ReloadActive(test.ctx, test.policy)
				if compiled != nil || !isZeroPolicyVersion(version) || !errors.Is(err, ErrInvalidPolicyPersistence) {
					t.Fatalf("ReloadActive() = (%p, %+v, %v), want invalid input", compiled, version, err)
				}
			})
		}

		hashTests := []struct {
			name      string
			publisher *Publisher
			ctx       context.Context
			hash      [sha256.Size]byte
		}{
			{name: "nil_receiver", ctx: context.Background(), hash: sha256.Sum256(source)},
			{name: "nil_context", publisher: publisher, hash: sha256.Sum256(source)},
			{name: "zero_hash", publisher: publisher, ctx: context.Background()},
			{name: "canceled_context", publisher: publisher, ctx: canceled, hash: sha256.Sum256(source)},
		}
		for _, test := range hashTests {
			t.Run("hash_"+test.name, func(t *testing.T) {
				compiled, version, err := test.publisher.ReloadHash(test.ctx, test.hash)
				if compiled != nil || !isZeroPolicyVersion(version) || !errors.Is(err, ErrInvalidPolicyPersistence) {
					t.Fatalf("ReloadHash() = (%p, %+v, %v), want invalid input", compiled, version, err)
				}
			})
		}
		if storeCalls != 0 {
			t.Fatalf("store calls = %d, want 0", storeCalls)
		}
	})

	t.Run("reload_routes_exact_lookup_and_activates", func(t *testing.T) {
		stored := testPolicyVersion()
		registry := &program.Registry{}
		activeCalls := 0
		hashCalls := 0
		compileCalls := 0
		store := &fakePolicyStore{
			loadActive: func(_ context.Context, name string) (PolicyVersion, error) {
				activeCalls++
				if name != stored.Name {
					t.Fatalf("LoadActive name = %q, want %q", name, stored.Name)
				}
				return stored, nil
			},
			loadByHash: func(_ context.Context, hash [sha256.Size]byte) (PolicyVersion, error) {
				hashCalls++
				if hash != stored.ContentHash {
					t.Fatalf("LoadByHash hash = %x, want %x", hash, stored.ContentHash)
				}
				return stored, nil
			},
		}
		publisher, err := NewPublisher(store, registry, func(gotSource []byte) (*program.Program, error) {
			compileCalls++
			if !bytes.Equal(gotSource, stored.Source) {
				t.Fatalf("compiler source = %q, want %q", gotSource, stored.Source)
			}
			return testProgram(gotSource, stored.Name, stored.SemanticVersion), nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		byName, versionByName, err := publisher.ReloadActive(context.Background(), stored.Name)
		if err != nil || byName == nil || versionByName.ID != stored.ID || registry.Active() != byName {
			t.Fatalf("ReloadActive() = (%p, %+v, %v), active=%p", byName, versionByName, err, registry.Active())
		}
		byHash, versionByHash, err := publisher.ReloadHash(context.Background(), stored.ContentHash)
		if err != nil || byHash != byName || versionByHash.ID != stored.ID || registry.Active() != byName {
			t.Fatalf("ReloadHash() = (%p, %+v, %v), active=%p", byHash, versionByHash, err, registry.Active())
		}
		if activeCalls != 1 || hashCalls != 1 || compileCalls != 1 {
			t.Fatalf("calls active/hash/compile = %d/%d/%d, want 1/1/1", activeCalls, hashCalls, compileCalls)
		}
	})

	t.Run("reload_preserves_not_found_error", func(t *testing.T) {
		store := &fakePolicyStore{loadActive: func(context.Context, string) (PolicyVersion, error) {
			return PolicyVersion{}, ErrStoredPolicyNotFound
		}}
		registry := &program.Registry{}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			t.Fatal("compiler called for absent policy")
			return nil, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		compiled, version, err := publisher.ReloadActive(context.Background(), "missing")
		if compiled != nil || !isZeroPolicyVersion(version) || !errors.Is(err, ErrStoredPolicyNotFound) {
			t.Fatalf("ReloadActive() = (%p, %+v, %v), want %v", compiled, version, err, ErrStoredPolicyNotFound)
		}
		if registry.Active() != nil {
			t.Fatalf("active = %p, want nil", registry.Active())
		}
	})

	t.Run("reload_rejects_store_lookup_identity_mismatch", func(t *testing.T) {
		stored := testPolicyVersion()
		store := &fakePolicyStore{
			loadActive: func(context.Context, string) (PolicyVersion, error) {
				mismatched := stored
				mismatched.Name = "different"
				return mismatched, nil
			},
			loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
				mismatched := stored
				mismatched.Source = []byte("different")
				return mismatched, nil
			},
		}
		registry := &program.Registry{}
		compileCalls := 0
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			compileCalls++
			return nil, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		if compiled, _, err := publisher.ReloadActive(context.Background(), stored.Name); compiled != nil || !errors.Is(err, ErrStoredPolicyCorrupt) {
			t.Fatalf("ReloadActive() = (%p, %v), want %v", compiled, err, ErrStoredPolicyCorrupt)
		}
		if compiled, _, err := publisher.ReloadHash(context.Background(), stored.ContentHash); compiled != nil || !errors.Is(err, ErrStoredPolicyCorrupt) {
			t.Fatalf("ReloadHash() = (%p, %v), want %v", compiled, err, ErrStoredPolicyCorrupt)
		}
		if compileCalls != 0 || registry.Active() != nil {
			t.Fatalf("corrupt rows compiled or activated: compile=%d active=%p", compileCalls, registry.Active())
		}
	})

	t.Run("reload_rejects_compiled_identity_mismatch", func(t *testing.T) {
		stored := testPolicyVersion()
		tests := []struct {
			name    string
			compile func([]byte) *program.Program
		}{
			{name: "source", compile: func([]byte) *program.Program {
				return testProgram([]byte("different"), stored.Name, stored.SemanticVersion)
			}},
			{name: "hash", compile: func(source []byte) *program.Program {
				compiled := testProgram(source, stored.Name, stored.SemanticVersion)
				compiled.ContentHash[0] ^= 0xff
				return compiled
			}},
			{name: "name", compile: func(source []byte) *program.Program {
				return testProgram(source, "different", stored.SemanticVersion)
			}},
			{name: "version", compile: func(source []byte) *program.Program {
				return testProgram(source, stored.Name, "9.9.9")
			}},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				store := &fakePolicyStore{loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
					return stored, nil
				}}
				registry := &program.Registry{}
				publisher, err := NewPublisher(store, registry, func(source []byte) (*program.Program, error) {
					return test.compile(source), nil
				}, compilerVersion)
				if err != nil {
					t.Fatalf("NewPublisher() error = %v", err)
				}

				compiled, version, err := publisher.ReloadHash(context.Background(), stored.ContentHash)
				if compiled != nil || version.ID != stored.ID || !errors.Is(err, ErrPolicyActivation) || !errors.Is(err, ErrStoredPolicyCorrupt) {
					t.Fatalf("ReloadHash() = (%p, %+v, %v), want activation corruption", compiled, version, err)
				}
				if registry.Active() != nil {
					t.Fatalf("active = %p, want nil", registry.Active())
				}
			})
		}
	})

	t.Run("reload_preserves_compiler_failure", func(t *testing.T) {
		stored := testPolicyVersion()
		compileErr := errors.New("reload compile failed")
		store := &fakePolicyStore{loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
			return stored, nil
		}}
		registry := &program.Registry{}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			return nil, compileErr
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		compiled, version, err := publisher.ReloadHash(context.Background(), stored.ContentHash)
		if compiled != nil || version.ID != stored.ID || !errors.Is(err, ErrPolicyActivation) || !errors.Is(err, compileErr) {
			t.Fatalf("ReloadHash() = (%p, %+v, %v), want activation and compiler errors", compiled, version, err)
		}
	})

	t.Run("reload_uses_cached_canonical_without_compile", func(t *testing.T) {
		stored := testPolicyVersion()
		canonical := testProgram(stored.Source, stored.Name, stored.SemanticVersion)
		registry := &program.Registry{}
		if _, err := registry.Publish(canonical); err != nil {
			t.Fatalf("Publish(canonical) error = %v", err)
		}
		store := &fakePolicyStore{loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
			return stored, nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			t.Fatal("compiler called for cached hash")
			return nil, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		compiled, _, err := publisher.ReloadHash(context.Background(), stored.ContentHash)
		if err != nil || compiled != canonical || registry.Active() != canonical {
			t.Fatalf("ReloadHash() = (%p, %v), active=%p, want %p", compiled, err, registry.Active(), canonical)
		}
	})

	t.Run("reload_rejects_cached_identity_mismatch_and_preserves_active", func(t *testing.T) {
		active := testProgram([]byte("current"), "current", "1.0.0")
		stored := testPolicyVersion()
		cached := testProgram(stored.Source, stored.Name, stored.SemanticVersion)
		cached.InputBytes = []byte("corrupt")
		registry := &program.Registry{}
		if _, err := registry.Publish(active); err != nil {
			t.Fatalf("Publish(active) error = %v", err)
		}
		if err := registry.Activate(active.ContentHash); err != nil {
			t.Fatalf("Activate(active) error = %v", err)
		}
		if _, err := registry.Publish(cached); err != nil {
			t.Fatalf("Publish(cached) error = %v", err)
		}
		store := &fakePolicyStore{loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
			return stored, nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			t.Fatal("compiler called for cached hash")
			return nil, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		compiled, version, err := publisher.ReloadHash(context.Background(), stored.ContentHash)
		if compiled != nil || version.ID != stored.ID || !errors.Is(err, ErrPolicyActivation) ||
			!errors.Is(err, ErrStoredPolicyCorrupt) || registry.Active() != active {
			t.Fatalf("ReloadHash() = (%p, %+v, %v), active=%p, want preserved %p", compiled, version, err, registry.Active(), active)
		}
	})

	t.Run("reload_failure_preserves_existing_active", func(t *testing.T) {
		active := testProgram([]byte("current"), "current", "1.0.0")
		stored := testPolicyVersion()
		stored.ContentHash[0] ^= 0xff
		registry := &program.Registry{}
		if _, err := registry.Publish(active); err != nil {
			t.Fatalf("Publish(active) error = %v", err)
		}
		if err := registry.Activate(active.ContentHash); err != nil {
			t.Fatalf("Activate(active) error = %v", err)
		}
		store := &fakePolicyStore{loadActive: func(context.Context, string) (PolicyVersion, error) {
			return stored, nil
		}}
		publisher, err := NewPublisher(store, registry, func([]byte) (*program.Program, error) {
			t.Fatal("compiler called for corrupt row")
			return nil, nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		compiled, _, err := publisher.ReloadActive(context.Background(), stored.Name)
		if compiled != nil || !errors.Is(err, ErrStoredPolicyCorrupt) || registry.Active() != active {
			t.Fatalf("ReloadActive() = (%p, %v), active=%p, want preserved %p", compiled, err, registry.Active(), active)
		}
	})

	t.Run("reload_compiler_runs_outside_registry_locks", func(t *testing.T) {
		stored := testPolicyVersion()
		unrelated := testProgram([]byte("unrelated"), "unrelated", "1.0.0")
		registry := &program.Registry{}
		store := &fakePolicyStore{loadByHash: func(context.Context, [sha256.Size]byte) (PolicyVersion, error) {
			return stored, nil
		}}
		publisher, err := NewPublisher(store, registry, func(source []byte) (*program.Program, error) {
			if _, err := registry.Publish(unrelated); err != nil {
				return nil, err
			}
			return testProgram(source, stored.Name, stored.SemanticVersion), nil
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}
		result := make(chan publishTestResult, 1)
		go func() {
			compiled, version, err := publisher.ReloadHash(context.Background(), stored.ContentHash)
			result <- publishTestResult{compiled: compiled, version: version, err: err}
		}()

		got := receivePublishResult(t, result, "reload outside registry locks")
		if got.err != nil || got.compiled == nil || registry.Active() != got.compiled {
			t.Fatalf("ReloadHash() = (%p, %v), active=%p", got.compiled, got.err, registry.Active())
		}
		if found, ok := registry.Lookup(unrelated.ContentHash); !ok || found != unrelated {
			t.Fatalf("unrelated Program = (%p, %v), want (%p, true)", found, ok, unrelated)
		}
	})

	t.Run("concurrent_publish_compiles_in_parallel_and_activates_in_commit_order", func(t *testing.T) {
		sourceA := []byte("source-a")
		sourceB := []byte("source-b")
		programA := testProgram(sourceA, "policy", "1.0.0")
		programB := testProgram(sourceB, "policy", "2.0.0")
		compiledA := make(chan struct{})
		compiledB := make(chan struct{})
		firstStoreEntered := make(chan struct{})
		secondStoreEntered := make(chan struct{})
		releaseFirstStore := make(chan struct{})
		storeCall := 0
		store := &fakePolicyStore{publishActive: func(_ context.Context, candidate Candidate) (PolicyVersion, error) {
			storeCall++
			stored := testStoredCandidate(candidate)
			stored.ID = PolicyVersionID(storeCall)
			if storeCall == 1 {
				close(firstStoreEntered)
				<-releaseFirstStore
			} else {
				close(secondStoreEntered)
			}
			return stored, nil
		}}
		registry := &program.Registry{}
		publisher, err := NewPublisher(store, registry, func(source []byte) (*program.Program, error) {
			switch {
			case bytes.Equal(source, sourceA):
				close(compiledA)
				return programA, nil
			case bytes.Equal(source, sourceB):
				close(compiledB)
				return programB, nil
			default:
				return nil, errors.New("unexpected source")
			}
		}, compilerVersion)
		if err != nil {
			t.Fatalf("NewPublisher() error = %v", err)
		}

		resultA := make(chan publishTestResult, 1)
		resultB := make(chan publishTestResult, 1)
		go func() {
			compiled, version, err := publisher.Publish(context.Background(), sourceA)
			resultA <- publishTestResult{compiled: compiled, version: version, err: err}
		}()
		waitTestSignal(t, compiledA, "first compilation")
		waitTestSignal(t, firstStoreEntered, "first store call")
		go func() {
			compiled, version, err := publisher.Publish(context.Background(), sourceB)
			resultB <- publishTestResult{compiled: compiled, version: version, err: err}
		}()
		waitTestSignal(t, compiledB, "second compilation")
		select {
		case <-secondStoreEntered:
			t.Fatal("second store call entered before first completed")
		default:
		}
		close(releaseFirstStore)

		gotA := receivePublishResult(t, resultA, "first publication")
		gotB := receivePublishResult(t, resultB, "second publication")
		if gotA.err != nil || gotA.compiled != programA || gotA.version.ID != 1 {
			t.Fatalf("first Publish() = (%p, %+v, %v)", gotA.compiled, gotA.version, gotA.err)
		}
		if gotB.err != nil || gotB.compiled != programB || gotB.version.ID != 2 {
			t.Fatalf("second Publish() = (%p, %+v, %v)", gotB.compiled, gotB.version, gotB.err)
		}
		if registry.Active() != programB {
			t.Fatalf("active = %p, want second committed Program %p", registry.Active(), programB)
		}
	})

	if !bytes.Equal(source, []byte(`{"name":"policy"}`)) {
		t.Fatalf("caller source mutated: %q", source)
	}
}
