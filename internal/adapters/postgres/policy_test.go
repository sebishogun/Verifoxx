//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/persistence"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

const integrationCompilerVersion = "integration-compiler"

type policyPublishResult struct {
	compiled *program.Program
	version  persistence.PolicyVersion
	err      error
}

func compileIntegrationPolicy(source []byte) (*program.Program, error) {
	fields, symbols, err := verifoxx.NewSchema()
	if err != nil {
		return nil, fmt.Errorf("construct Verifoxx schema: %w", err)
	}
	builder := ast.NewBuilder(ast.Hints{SourceBytes: len(source)})
	var decoder jsonpolicy.Decoder
	if err := decoder.Decode(builder, source, fields, symbols, jsonpolicy.Limits{}); err != nil {
		return nil, fmt.Errorf("decode policy: %w", err)
	}
	compiled, err := compile.Lower(builder.Document(), fields, symbols)
	if err != nil {
		return nil, fmt.Errorf("lower policy: %w", err)
	}
	return compiled, nil
}

func policyCandidateFromProgram(t *testing.T, compiled *program.Program) persistence.Candidate {
	t.Helper()
	name, nameOK := compiled.Symbol(compiled.PolicyName)
	semanticVersion, versionOK := compiled.Symbol(compiled.PolicyVersion)
	if !nameOK || !versionOK {
		t.Fatal("compiled Program has invalid policy identity symbols")
	}
	return persistence.Candidate{
		Program:         compiled,
		Source:          compiled.InputBytes,
		Name:            string(name),
		SemanticVersion: string(semanticVersion),
		CompilerVersion: integrationCompilerVersion,
		ContentHash:     compiled.ContentHash,
	}
}

func verifoxxSourceVersion(t *testing.T, version string) []byte {
	t.Helper()
	source := []byte(verifoxx.Source())
	updated := bytes.Replace(source, []byte(`"version": "1.0.0"`), []byte(`"version": "`+version+`"`), 1)
	if bytes.Equal(updated, source) {
		t.Fatalf("embedded policy version marker was not replaced with %q", version)
	}
	return updated
}

func receivePolicyPublish(t *testing.T, ctx context.Context, results <-chan policyPublishResult) policyPublishResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-ctx.Done():
		t.Fatalf("policy publication timed out: %v", ctx.Err())
		return policyPublishResult{}
	}
}

func policyCandidate(name, semanticVersion, compilerVersion string, source []byte) persistence.Candidate {
	owned := append([]byte(nil), source...)
	compiled := minimalGraphProgram(owned, name, semanticVersion)
	return persistence.Candidate{
		Program:         compiled,
		Source:          owned,
		Name:            name,
		SemanticVersion: semanticVersion,
		CompilerVersion: compilerVersion,
		ContentHash:     compiled.ContentHash,
	}
}

func minimalGraphProgram(source []byte, name, semanticVersion string) *program.Program {
	bytes := make([]byte, 0, len(name)+len(semanticVersion)+len("fieldApprove"))
	starts := make([]uint32, 0, 4)
	lengths := make([]uint32, 0, 4)
	appendSymbol := func(value string) schema.SymbolID {
		starts = append(starts, uint32(len(bytes)))
		bytes = append(bytes, value...)
		lengths = append(lengths, uint32(len(value)))
		return schema.SymbolID(len(starts))
	}
	policyName := appendSymbol(name)
	policyVersion := appendSymbol(semanticVersion)
	fieldName := appendSymbol("field")
	outcomeName := appendSymbol("Approve")
	spanEnd := uint32(len(source))
	resolutionOutcomes := make([]schema.OutcomeID, truth.ReasonCount)
	for row := range resolutionOutcomes {
		resolutionOutcomes[row] = 1
	}

	return &program.Program{
		Opcodes:                      []program.Opcode{program.OpcodeExists},
		Fields:                       []schema.FieldID{1},
		Values:                       []schema.ValueID{0},
		ListStarts:                   []uint32{0},
		ListCounts:                   []uint16{0},
		OperandStarts:                []uint32{0},
		OperandCounts:                []uint16{0},
		EvidenceKinds:                []schema.EvidenceKindID{0},
		EvidenceStates:               []schema.EvidenceStateID{0},
		EvidenceSubjects:             []schema.SymbolID{0},
		EvidenceScopes:               []schema.SymbolID{0},
		EvidenceTimings:              []schema.SymbolID{0},
		RootFlags:                    []program.RootFlags{program.RootApplicability | program.RootAssertion},
		InstructionNodes:             []schema.NodeID{1},
		InstructionSourceStarts:      []uint32{0},
		InstructionSourceEnds:        []uint32{spanEnd},
		SymbolBytes:                  bytes,
		SymbolStarts:                 starts,
		SymbolLengths:                lengths,
		FieldNames:                   []schema.SymbolID{fieldName},
		RequirementIDs:               []schema.RequirementID{1},
		RequirementRoots:             []schema.InstructionID{1},
		RequirementSourceNodeIDs:     []schema.NodeID{1},
		RequirementClauseStarts:      []uint32{0},
		RequirementClauseCounts:      []uint16{1},
		RequirementClauseIDs:         []schema.ClauseID{1},
		RequirementSourceStarts:      []uint32{0},
		RequirementSourceEnds:        []uint32{spanEnd},
		ClauseAssertionRoots:         []schema.InstructionID{1},
		ClauseAssertionSourceNodeIDs: []schema.NodeID{1},
		ClauseEvidenceStarts:         []uint32{0},
		ClauseEvidenceCounts:         []uint16{0},
		ClauseOnSatisfied:            []schema.OutcomeID{1},
		ClauseOnFalse:                []schema.OutcomeID{1},
		ClauseRemediationStarts:      []uint32{0},
		ClauseRemediationCounts:      []uint16{0},
		ClauseSourceStarts:           []uint32{0},
		ClauseSourceEnds:             []uint32{spanEnd},
		OutcomeSourceStarts:          []uint32{0},
		OutcomeSourceEnds:            []uint32{spanEnd},
		Outcomes: result.OutcomeTable{
			Names:      []schema.SymbolID{outcomeName},
			Precedence: []uint8{1},
			Terminal:   []bool{true},
		},
		Resolutions:        result.ResolutionTable{OutcomeIDs: resolutionOutcomes},
		InputBytes:         source,
		ContentHash:        sha256.Sum256(source),
		PolicyName:         policyName,
		PolicyVersion:      policyVersion,
		ProgramSymbolCount: uint32(len(starts)),
	}
}

func assertPolicyVersionEqual(t *testing.T, got, want persistence.PolicyVersion) {
	t.Helper()
	if got.PolicyID != want.PolicyID || got.ID != want.ID || got.Name != want.Name ||
		got.SemanticVersion != want.SemanticVersion || got.CompilerVersion != want.CompilerVersion ||
		got.ContentHash != want.ContentHash || !got.PublishedAt.Equal(want.PublishedAt) ||
		!bytes.Equal(got.Source, want.Source) {
		t.Fatalf("PolicyVersion = %+v, want %+v", got, want)
	}
}

func testPolicyStorePublish(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	if store, err := NewPolicyStore(nil); store != nil || !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("NewPolicyStore(nil) = (%p, %v), want invalid dependency", store, err)
	}
	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	if _, err := store.PublishActive(ctx, persistence.Candidate{}); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("publish invalid Candidate error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
	if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx.policies"); got != 0 {
		t.Fatalf("policy count after invalid Candidate = %d, want 0", got)
	}

	candidate := policyCandidate("policy", "1.0.0", "compiler-v1", []byte(`{"name":"policy","version":"1.0.0"}`))
	stored, err := store.PublishActive(ctx, candidate)
	if err != nil {
		t.Fatalf("publish first policy: %v", err)
	}
	if err := persistence.ValidatePolicyVersion(stored); err != nil {
		t.Fatalf("stored PolicyVersion invalid: %v", err)
	}
	if stored.Name != candidate.Name || stored.SemanticVersion != candidate.SemanticVersion ||
		stored.CompilerVersion != candidate.CompilerVersion || stored.ContentHash != candidate.ContentHash ||
		!bytes.Equal(stored.Source, candidate.Source) {
		t.Fatalf("stored PolicyVersion = %+v, want Candidate %+v", stored, candidate)
	}
	if &stored.Source[0] == &candidate.Source[0] {
		t.Fatal("stored source aliases borrowed Candidate source")
	}
	originalFirstByte := candidate.Source[0]
	candidate.Source[0] ^= 0xff
	if stored.Source[0] != originalFirstByte {
		t.Fatal("stored source changed after Candidate reuse")
	}
	candidate.Source[0] = originalFirstByte

	var (
		databaseSource          []byte
		databaseHash            []byte
		databaseName            string
		databaseSemanticVersion string
		databaseCompilerVersion string
		databasePolicyID        int64
		databaseVersionID       int64
		databaseActiveID        int64
	)
	if err := environment.runtime.QueryRow(ctx, `
		SELECT p.id, p.name, p.active_version_id,
		       v.id, v.semantic_version, v.source, v.content_hash, v.compiler_version
		FROM verifoxx.policies AS p
		JOIN verifoxx.policy_versions AS v ON v.id = p.active_version_id
		WHERE p.name = $1
	`, candidate.Name).Scan(
		&databasePolicyID,
		&databaseName,
		&databaseActiveID,
		&databaseVersionID,
		&databaseSemanticVersion,
		&databaseSource,
		&databaseHash,
		&databaseCompilerVersion,
	); err != nil {
		t.Fatalf("query stored active policy: %v", err)
	}
	if databasePolicyID != int64(stored.PolicyID) || databaseVersionID != int64(stored.ID) ||
		databaseActiveID != int64(stored.ID) || databaseName != candidate.Name ||
		databaseSemanticVersion != candidate.SemanticVersion || databaseCompilerVersion != candidate.CompilerVersion ||
		!bytes.Equal(databaseSource, candidate.Source) || !bytes.Equal(databaseHash, candidate.ContentHash[:]) {
		t.Fatalf("database row = policy:%d name:%q active:%d version:%d semantic:%q source:%q hash:%x compiler:%q",
			databasePolicyID, databaseName, databaseActiveID, databaseVersionID, databaseSemanticVersion,
			databaseSource, databaseHash, databaseCompilerVersion,
		)
	}

	duplicate := candidate
	duplicate.CompilerVersion = "compiler-v2"
	storedAgain, err := store.PublishActive(ctx, duplicate)
	if err != nil {
		t.Fatalf("republish identical source: %v", err)
	}
	assertPolicyVersionEqual(t, storedAgain, stored)
	if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx.policy_versions"); got != 1 {
		t.Fatalf("version count after duplicate = %d, want 1", got)
	}

	conflict := policyCandidate(candidate.Name, candidate.SemanticVersion, "compiler-v3", []byte("changed-source"))
	if _, err := store.PublishActive(ctx, conflict); !errors.Is(err, persistence.ErrPolicyVersionConflict) {
		t.Fatalf("conflicting semantic version error = %v, want %v", err, persistence.ErrPolicyVersionConflict)
	}
	if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx.policies"); got != 1 {
		t.Fatalf("policy count after conflict = %d, want 1", got)
	}
	if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx.policy_versions"); got != 1 {
		t.Fatalf("version count after conflict = %d, want 1", got)
	}
	var activeID int64
	if err := environment.runtime.QueryRow(ctx,
		"SELECT active_version_id FROM verifoxx.policies WHERE id = $1",
		stored.PolicyID,
	).Scan(&activeID); err != nil {
		t.Fatalf("query active version after conflict: %v", err)
	}
	if activeID != int64(stored.ID) {
		t.Fatalf("active version after conflict = %d, want %d", activeID, stored.ID)
	}

	assertSQLState(t, execError(ctx, environment.runtime,
		"UPDATE verifoxx.policy_versions SET compiler_version = compiler_version WHERE id = $1", stored.ID,
	), "42501")
	assertSQLState(t, execError(ctx, environment.runtime,
		"DELETE FROM verifoxx.policy_versions WHERE id = $1", stored.ID,
	), "42501")
}

func testPolicyStoreLoad(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	name := "policy'quoted"
	firstCandidate := policyCandidate(name, "1.0.0", "compiler-v1", []byte("quoted-policy-v1"))
	first, err := store.PublishActive(ctx, firstCandidate)
	if err != nil {
		t.Fatalf("publish first quoted policy: %v", err)
	}
	secondCandidate := policyCandidate(name, "2.0.0", "compiler-v2", []byte("quoted-policy-v2"))
	second, err := store.PublishActive(ctx, secondCandidate)
	if err != nil {
		t.Fatalf("publish second quoted policy: %v", err)
	}

	active, err := store.LoadActive(ctx, name)
	if err != nil {
		t.Fatalf("load active quoted policy: %v", err)
	}
	assertPolicyVersionEqual(t, active, second)

	byHash, err := store.LoadByHash(ctx, first.ContentHash)
	if err != nil {
		t.Fatalf("load inactive version by hash: %v", err)
	}
	assertPolicyVersionEqual(t, byHash, first)
	byHash.Source[0] ^= 0xff
	loadedAgain, err := store.LoadByHash(ctx, first.ContentHash)
	if err != nil {
		t.Fatalf("reload source after caller mutation: %v", err)
	}
	assertPolicyVersionEqual(t, loadedAgain, first)

	if _, err := store.LoadActive(ctx, "missing'policy"); !errors.Is(err, persistence.ErrStoredPolicyNotFound) {
		t.Fatalf("missing active policy error = %v, want %v", err, persistence.ErrStoredPolicyNotFound)
	}
	missingHash := sha256.Sum256([]byte("missing-policy"))
	if _, err := store.LoadByHash(ctx, missingHash); !errors.Is(err, persistence.ErrStoredPolicyNotFound) {
		t.Fatalf("missing hash error = %v, want %v", err, persistence.ErrStoredPolicyNotFound)
	}
	if _, err := store.LoadActive(ctx, " \t"); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("blank active name error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
	if _, err := store.LoadActive(ctx, " policy "); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("padded active name error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
	if _, err := store.LoadByHash(ctx, [sha256.Size]byte{}); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("zero hash error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
}

func testPolicyPublishReload(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	registry := &program.Registry{}
	publisher, err := persistence.NewPublisher(store, registry, compileIntegrationPolicy, integrationCompilerVersion)
	if err != nil {
		t.Fatalf("construct Publisher: %v", err)
	}
	source := []byte(verifoxx.Source())

	firstProgram, firstVersion, err := publisher.Publish(ctx, source)
	if err != nil {
		t.Fatalf("publish embedded policy: %v", err)
	}
	if registry.Active() != firstProgram || firstVersion.Name != "verifoxx" ||
		firstVersion.SemanticVersion != "1.0.0" || !bytes.Equal(firstProgram.InputBytes, source) ||
		firstProgram.ContentHash != firstVersion.ContentHash {
		t.Fatalf("first publication = Program:%p Version:%+v Active:%p", firstProgram, firstVersion, registry.Active())
	}
	secondProgram, secondVersion, err := publisher.Publish(ctx, source)
	if err != nil {
		t.Fatalf("republish embedded policy: %v", err)
	}
	if secondProgram != firstProgram {
		t.Fatalf("duplicate Program = %p, want canonical %p", secondProgram, firstProgram)
	}
	assertPolicyVersionEqual(t, secondVersion, firstVersion)

	reloadedRegistry := &program.Registry{}
	reloader, err := persistence.NewPublisher(store, reloadedRegistry, compileIntegrationPolicy, "newer-compiler")
	if err != nil {
		t.Fatalf("construct reload Publisher: %v", err)
	}
	reloadedActive, activeVersion, err := reloader.ReloadActive(ctx, firstVersion.Name)
	if err != nil {
		t.Fatalf("reload active policy: %v", err)
	}
	assertPolicyVersionEqual(t, activeVersion, firstVersion)
	if reloadedRegistry.Active() != reloadedActive || !bytes.Equal(reloadedActive.InputBytes, source) ||
		reloadedActive.ContentHash != firstProgram.ContentHash {
		t.Fatalf("ReloadActive() Program=%p Active=%p Hash=%x", reloadedActive, reloadedRegistry.Active(), reloadedActive.ContentHash)
	}
	reloadedHash, hashVersion, err := reloader.ReloadHash(ctx, firstVersion.ContentHash)
	if err != nil {
		t.Fatalf("reload policy hash: %v", err)
	}
	if reloadedHash != reloadedActive {
		t.Fatalf("ReloadHash Program = %p, want cached %p", reloadedHash, reloadedActive)
	}
	assertPolicyVersionEqual(t, hashVersion, firstVersion)

	conflictingSource := append(append([]byte(nil), source...), '\n')
	if _, conflictVersion, err := reloader.Publish(ctx, conflictingSource); !errors.Is(err, persistence.ErrPolicyVersionConflict) {
		t.Fatalf("conflicting publication = (%+v, %v), want %v", conflictVersion, err, persistence.ErrPolicyVersionConflict)
	}
	if reloadedRegistry.Active() != reloadedActive {
		t.Fatalf("active Program changed after conflict: got %p want %p", reloadedRegistry.Active(), reloadedActive)
	}
	if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx.policy_versions"); got != 1 {
		t.Fatalf("version count after conflict = %d, want 1", got)
	}
	databaseActive, err := store.LoadActive(ctx, firstVersion.Name)
	if err != nil {
		t.Fatalf("load database active after conflict: %v", err)
	}
	assertPolicyVersionEqual(t, databaseActive, firstVersion)
}

func testPolicyConcurrentPublish(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	opCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	firstStore, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct first policy store: %v", err)
	}
	secondStore, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct second policy store: %v", err)
	}
	sourceV1 := []byte(verifoxx.Source())
	compiledV1, err := compileIntegrationPolicy(sourceV1)
	if err != nil {
		t.Fatalf("compile v1 policy: %v", err)
	}
	candidateV1 := policyCandidateFromProgram(t, compiledV1)
	startStores := make(chan struct{})
	storeResults := make(chan policyPublishResult, 2)
	runStore := func(store *PolicyStore) {
		<-startStores
		version, err := store.PublishActive(opCtx, candidateV1)
		storeResults <- policyPublishResult{version: version, err: err}
	}
	go runStore(firstStore)
	go runStore(secondStore)
	close(startStores)
	firstResult := receivePolicyPublish(t, opCtx, storeResults)
	secondResult := receivePolicyPublish(t, opCtx, storeResults)
	if firstResult.err != nil || secondResult.err != nil {
		t.Fatalf("concurrent identical store errors = (%v, %v)", firstResult.err, secondResult.err)
	}
	assertPolicyVersionEqual(t, firstResult.version, secondResult.version)
	if got := queryCount(t, opCtx, environment.runtime, "SELECT count(*) FROM verifoxx.policy_versions"); got != 1 {
		t.Fatalf("version count after identical race = %d, want 1", got)
	}
	wantNodes := 1 + len(compiledV1.RequirementIDs) + len(compiledV1.ClauseAssertionRoots) +
		len(compiledV1.Opcodes) + len(compiledV1.Outcomes.Names) + len(compiledV1.Remediations.Kinds)
	if got := queryCount(t, opCtx, environment.runtime,
		"SELECT count(*) FROM verifoxx.policy_nodes WHERE policy_version_id = $1", firstResult.version.ID,
	); got != wantNodes {
		t.Fatalf("node count after identical race = %d, want %d", got, wantNodes)
	}
	wantEdges := 2*len(compiledV1.RequirementIDs) + len(compiledV1.RequirementClauseIDs) +
		len(compiledV1.ClauseAssertionRoots) + len(compiledV1.ClauseEvidenceIDs) + len(compiledV1.Operands) +
		7*len(compiledV1.ClauseAssertionRoots) + len(compiledV1.ClauseRemediationIDs)
	if got := queryCount(t, opCtx, environment.runtime,
		"SELECT count(*) FROM verifoxx.policy_edges WHERE policy_version_id = $1", firstResult.version.ID,
	); got != wantEdges {
		t.Fatalf("edge count after identical race = %d, want %d", got, wantEdges)
	}

	sourceV2 := verifoxxSourceVersion(t, "2.0.0")
	registry := &program.Registry{}
	compileReady := make(chan struct{}, 2)
	releaseCompile := make(chan struct{})
	concurrentCompiler := func(source []byte) (*program.Program, error) {
		compiled, err := compileIntegrationPolicy(source)
		if err != nil {
			return nil, err
		}
		select {
		case compileReady <- struct{}{}:
		case <-opCtx.Done():
			return nil, opCtx.Err()
		}
		select {
		case <-releaseCompile:
			return compiled, nil
		case <-opCtx.Done():
			return nil, opCtx.Err()
		}
	}
	publisher, err := persistence.NewPublisher(firstStore, registry, concurrentCompiler, integrationCompilerVersion)
	if err != nil {
		t.Fatalf("construct concurrent Publisher: %v", err)
	}
	resultV1 := make(chan policyPublishResult, 1)
	resultV2 := make(chan policyPublishResult, 1)
	go func() {
		compiled, version, err := publisher.Publish(opCtx, sourceV1)
		resultV1 <- policyPublishResult{compiled: compiled, version: version, err: err}
	}()
	go func() {
		compiled, version, err := publisher.Publish(opCtx, sourceV2)
		resultV2 <- policyPublishResult{compiled: compiled, version: version, err: err}
	}()
	for range 2 {
		select {
		case <-compileReady:
		case <-opCtx.Done():
			t.Fatalf("concurrent compilation timed out: %v", opCtx.Err())
		}
	}
	close(releaseCompile)
	publishedV1 := receivePolicyPublish(t, opCtx, resultV1)
	publishedV2 := receivePolicyPublish(t, opCtx, resultV2)
	if publishedV1.err != nil || publishedV2.err != nil {
		t.Fatalf("concurrent Publisher errors = (%v, %v)", publishedV1.err, publishedV2.err)
	}
	if publishedV1.version.ID == publishedV2.version.ID || publishedV1.compiled == nil || publishedV2.compiled == nil {
		t.Fatalf("concurrent versions = (%+v, %+v)", publishedV1.version, publishedV2.version)
	}
	if got := queryCount(t, opCtx, environment.runtime, "SELECT count(*) FROM verifoxx.policy_versions"); got != 2 {
		t.Fatalf("version count after distinct publication = %d, want 2", got)
	}
	active := registry.Active()
	if active == nil {
		t.Fatal("registry has no active Program after concurrent publication")
	}
	databaseActive, err := firstStore.LoadActive(opCtx, "verifoxx")
	if err != nil {
		t.Fatalf("load database active after concurrency: %v", err)
	}
	if databaseActive.ContentHash != active.ContentHash {
		t.Fatalf("database active hash = %x, registry active hash = %x", databaseActive.ContentHash, active.ContentHash)
	}
	if active != publishedV1.compiled && active != publishedV2.compiled {
		t.Fatalf("active Program %p is neither publication (%p, %p)", active, publishedV1.compiled, publishedV2.compiled)
	}
}
