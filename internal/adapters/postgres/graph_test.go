//go:build integration

package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sebishogun/nornrune/internal/persistence"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func testPolicyGraphSchema(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	for _, relation := range []string{
		"nornrune.policy_nodes",
		"nornrune.policy_edges",
		"nornrune.policy_version_vertices",
		"nornrune.requirement_vertices",
		"nornrune.clause_vertices",
		"nornrune.expression_vertices",
		"nornrune.evidence_requirement_vertices",
		"nornrune.outcome_vertices",
		"nornrune.remediation_vertices",
	} {
		assertRelationExists(t, ctx, environment.migrator, relation, true)
	}

	var vertices int
	if err := environment.runtime.QueryRow(ctx, `
		SELECT count(*)
		FROM GRAPH_TABLE (
			nornrune.policy_graph
			MATCH (vertex)
			COLUMNS (vertex.policy_version_id AS policy_version_id)
		)
	`).Scan(&vertices); err != nil {
		t.Fatalf("query empty policy graph: %v", err)
	}
	if vertices != 0 {
		t.Fatalf("empty policy graph vertices = %d, want 0", vertices)
	}
}

func testPolicyGraphProjection(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	source := []byte(nornrune.Source())
	compiled, err := compileIntegrationPolicy(source)
	if err != nil {
		t.Fatalf("compile graph policy: %v", err)
	}
	candidate := policyCandidateFromProgram(t, compiled)
	version, err := store.PublishActive(ctx, candidate)
	if err != nil {
		t.Fatalf("publish graph policy: %v", err)
	}

	evidenceNodes := 0
	for _, opcode := range compiled.Opcodes {
		if opcode == program.OpcodeEvidence {
			evidenceNodes++
		}
	}
	nodeCounts := []struct {
		kind string
		want int
	}{
		{kind: "policy_version", want: 1},
		{kind: "requirement", want: len(compiled.RequirementIDs)},
		{kind: "clause", want: len(compiled.ClauseAssertionRoots)},
		{kind: "expression", want: len(compiled.Opcodes) - evidenceNodes},
		{kind: "evidence_requirement", want: evidenceNodes},
		{kind: "outcome", want: len(compiled.Outcomes.Names)},
		{kind: "remediation", want: len(compiled.Remediations.Kinds)},
	}
	for _, count := range nodeCounts {
		if got := queryCount(t, ctx, environment.runtime, `
			SELECT count(*) FROM nornrune.policy_nodes
			WHERE policy_version_id = $1 AND node_kind = $2
		`, version.ID, count.kind); got != count.want {
			t.Fatalf("%s node count = %d, want %d", count.kind, got, count.want)
		}
	}

	edgeCounts := []struct {
		kind string
		want int
	}{
		{kind: "CONTAINS", want: len(compiled.RequirementIDs) + len(compiled.RequirementClauseIDs) + len(compiled.ClauseAssertionRoots)},
		{kind: "CHILD", want: len(compiled.Operands)},
		{kind: "APPLIES_WHEN", want: len(compiled.RequirementIDs)},
		{kind: "REQUIRES", want: len(compiled.ClauseEvidenceIDs)},
		{kind: "RESOLVES_TO", want: 7 * len(compiled.ClauseAssertionRoots)},
		{kind: "REMEDIATES_WITH", want: len(compiled.ClauseRemediationIDs)},
	}
	totalEdges := 0
	for _, count := range edgeCounts {
		totalEdges += count.want
		if got := queryCount(t, ctx, environment.runtime, `
			SELECT count(*) FROM nornrune.policy_edges
			WHERE policy_version_id = $1 AND edge_kind = $2
		`, version.ID, count.kind); got != count.want {
			t.Fatalf("%s edge count = %d, want %d", count.kind, got, count.want)
		}
	}

	var (
		storedName         string
		storedVersion      string
		storedHash         []byte
		projectedNodeCount int64
		projectedEdgeCount int64
	)
	if err := environment.runtime.QueryRow(ctx, `
		SELECT name, detail, content_hash, projected_node_count, projected_edge_count
		FROM nornrune.policy_nodes
		WHERE policy_version_id = $1 AND node_kind = 'policy_version' AND local_id = 1
	`, version.ID).Scan(
		&storedName, &storedVersion, &storedHash, &projectedNodeCount, &projectedEdgeCount,
	); err != nil {
		t.Fatalf("query policy-version vertex: %v", err)
	}
	wantProjectedNodes := 1 + len(compiled.RequirementIDs) + len(compiled.ClauseAssertionRoots) +
		len(compiled.Opcodes) + len(compiled.Outcomes.Names) + len(compiled.Remediations.Kinds)
	if storedName != candidate.Name || storedVersion != candidate.SemanticVersion ||
		!bytes.Equal(storedHash, candidate.ContentHash[:]) || projectedNodeCount != int64(wantProjectedNodes) ||
		projectedEdgeCount != int64(totalEdges) {
		t.Fatalf("policy-version vertex = (%q, %q, %x, %d, %d), want (%q, %q, %x, %d, %d)",
			storedName, storedVersion, storedHash, projectedNodeCount, projectedEdgeCount,
			candidate.Name, candidate.SemanticVersion, candidate.ContentHash, wantProjectedNodes, totalEdges)
	}

	outcomes := queryStrings(t, ctx, environment.runtime, `
		SELECT name
		FROM nornrune.policy_nodes
		WHERE policy_version_id = $1 AND node_kind = 'outcome'
		ORDER BY local_id
	`, version.ID)
	if want := []string{"Approve", "Reject", "Revise", "Escalate"}; !slices.Equal(outcomes, want) {
		t.Fatalf("outcome vertices = %v, want %v", outcomes, want)
	}
	if got := queryCount(t, ctx, environment.runtime, `
		SELECT count(*)
		FROM nornrune.policy_nodes
		WHERE policy_version_id = $1
		  AND node_kind = 'outcome'
		  AND (name, precedence, terminal) IN (
		      ('Approve', 1, true),
		      ('Reject', 4, true),
		      ('Revise', 2, false),
		      ('Escalate', 3, true)
		  )
	`, version.ID); got != 4 {
		t.Fatalf("outcome vertices with exact properties = %d, want 4", got)
	}

	lastClauseID := len(compiled.ClauseAssertionRoots)
	resolutionPairs := queryStrings(t, ctx, environment.runtime, `
		SELECT edge.branch || ':' || outcome.name
		FROM nornrune.policy_edges AS edge
		JOIN nornrune.policy_nodes AS outcome
		  ON outcome.policy_version_id = edge.policy_version_id
		 AND outcome.node_kind = edge.target_kind
		 AND outcome.local_id = edge.target_id
		WHERE edge.policy_version_id = $1
		  AND edge.edge_kind = 'RESOLVES_TO'
		  AND edge.source_id = $2
		ORDER BY edge.ordinal
	`, version.ID, lastClauseID)
	wantResolutionPairs := []string{
		"satisfied:Approve",
		"false:Revise",
		"missing:Revise",
		"stale:Escalate",
		"unclear:Escalate",
		"unverifiable:Escalate",
		"conflict:Escalate",
	}
	if !slices.Equal(resolutionPairs, wantResolutionPairs) {
		t.Fatalf("last-clause resolution paths = %v, want %v", resolutionPairs, wantResolutionPairs)
	}
	remediations := queryStrings(t, ctx, environment.runtime, `
		SELECT remediation.name || ':' || remediation.detail
		FROM nornrune.policy_edges AS edge
		JOIN nornrune.policy_nodes AS remediation
		  ON remediation.policy_version_id = edge.policy_version_id
		 AND remediation.node_kind = edge.target_kind
		 AND remediation.local_id = edge.target_id
		WHERE edge.policy_version_id = $1
		  AND edge.edge_kind = 'REMEDIATES_WITH'
		  AND edge.source_id = $2
		ORDER BY edge.ordinal
	`, version.ID, lastClauseID)
	if want := []string{"add_evidence:usage_limit_adjustment"}; !slices.Equal(remediations, want) {
		t.Fatalf("last-clause remediations = %v, want %v", remediations, want)
	}

	var requirementStart, requirementEnd int64
	if err := environment.runtime.QueryRow(ctx, `
		SELECT source_start, source_end
		FROM nornrune.policy_nodes
		WHERE policy_version_id = $1 AND node_kind = 'requirement' AND local_id = $2
	`, version.ID, compiled.RequirementIDs[0]).Scan(&requirementStart, &requirementEnd); err != nil {
		t.Fatalf("query requirement source span: %v", err)
	}
	if requirementStart != int64(compiled.RequirementSourceStarts[0]) ||
		requirementEnd != int64(compiled.RequirementSourceEnds[0]) {
		t.Fatalf("requirement source span = [%d,%d), want [%d,%d)",
			requirementStart, requirementEnd,
			compiled.RequirementSourceStarts[0], compiled.RequirementSourceEnds[0])
	}
	if got := queryCount(t, ctx, environment.runtime, `
		SELECT count(*)
		FROM nornrune.policy_edges AS edge
		LEFT JOIN nornrune.policy_nodes AS source
		  ON source.policy_version_id = edge.policy_version_id
		 AND source.node_kind = edge.source_kind
		 AND source.local_id = edge.source_id
		LEFT JOIN nornrune.policy_nodes AS target
		  ON target.policy_version_id = edge.policy_version_id
		 AND target.node_kind = edge.target_kind
		 AND target.local_id = edge.target_id
		WHERE edge.policy_version_id = $1
		  AND (source.local_id IS NULL OR target.local_id IS NULL)
	`, version.ID); got != 0 {
		t.Fatalf("orphan graph edge count = %d, want 0", got)
	}

	duplicate, err := store.PublishActive(ctx, candidate)
	if err != nil {
		t.Fatalf("republish graph policy: %v", err)
	}
	assertPolicyVersionEqual(t, duplicate, version)
	if got := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policy_nodes WHERE policy_version_id = $1", version.ID,
	); got != len(compiled.Opcodes)+len(compiled.RequirementIDs)+len(compiled.ClauseAssertionRoots)+
		len(compiled.Outcomes.Names)+len(compiled.Remediations.Kinds)+1 {
		t.Fatalf("node count after duplicate publication = %d", got)
	}
	if got := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policy_edges WHERE policy_version_id = $1", version.ID,
	); got != totalEdges {
		t.Fatalf("edge count after duplicate publication = %d, want %d", got, totalEdges)
	}
	t.Run("accepts_recompiled_shape", func(t *testing.T) {
		testPolicyGraphAcceptsRecompiledShape(t, ctx, environment, store, source, version)
	})
	t.Run("rejects_malformed_republish", func(t *testing.T) {
		testPolicyGraphRejectsMalformedRepublish(t, ctx, store, source)
	})
	t.Run("rejects_reason_alias_divergence", func(t *testing.T) {
		testPolicyGraphRejectsReasonAliasDivergence(t, ctx, environment, store)
	})
	t.Run("rejects_post_publication_insert", func(t *testing.T) {
		testPolicyGraphRejectsPostPublicationInsert(t, ctx, environment, version, compiled.RequirementIDs[0])
	})

	for _, statement := range []string{
		"UPDATE nornrune.policy_nodes SET name = name",
		"DELETE FROM nornrune.policy_nodes",
		"UPDATE nornrune.policy_edges SET ordinal = ordinal",
		"DELETE FROM nornrune.policy_edges",
	} {
		assertSQLState(t, execError(ctx, environment.runtime, statement), "42501")
		assertSQLState(t, execError(ctx, environment.migrator, statement), "55000")
	}

	invalidSource := nornruneSourceVersion(t, "2.0.0")
	invalidProgram, err := compileIntegrationPolicy(invalidSource)
	if err != nil {
		t.Fatalf("compile invalid graph fixture: %v", err)
	}
	invalidProgram.RequirementRoots[0] = 0
	invalidCandidate := policyCandidateFromProgram(t, invalidProgram)
	if _, err := store.PublishActive(ctx, invalidCandidate); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("publish invalid graph error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
	if got := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policy_versions WHERE content_hash = $1", invalidCandidate.ContentHash[:],
	); got != 0 {
		t.Fatalf("invalid graph durable version count = %d, want 0", got)
	}
	if got := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policy_nodes WHERE policy_version_id <> $1", version.ID,
	); got != 0 {
		t.Fatalf("invalid graph durable node count = %d, want 0", got)
	}
}

func testPolicyGraphAcceptsRecompiledShape(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
	store *PolicyStore,
	source []byte,
	wantVersion persistence.PolicyVersion,
) {
	t.Helper()

	nodeCountBefore := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policy_nodes WHERE policy_version_id = $1", wantVersion.ID,
	)
	compiled, err := compileIntegrationPolicy(source)
	if err != nil {
		t.Fatalf("compile changed-shape republish fixture: %v", err)
	}
	compiled.Opcodes = append(compiled.Opcodes, program.OpcodeExists)
	compiled.Fields = append(compiled.Fields, compiled.Fields[0])
	compiled.Values = append(compiled.Values, 0)
	compiled.ListStarts = append(compiled.ListStarts, uint32(len(compiled.ListValues)))
	compiled.ListCounts = append(compiled.ListCounts, 0)
	compiled.OperandStarts = append(compiled.OperandStarts, uint32(len(compiled.Operands)))
	compiled.OperandCounts = append(compiled.OperandCounts, 0)
	compiled.EvidenceKinds = append(compiled.EvidenceKinds, 0)
	compiled.EvidenceStates = append(compiled.EvidenceStates, 0)
	compiled.EvidenceSubjects = append(compiled.EvidenceSubjects, 0)
	compiled.EvidenceScopes = append(compiled.EvidenceScopes, 0)
	compiled.EvidenceTimings = append(compiled.EvidenceTimings, 0)
	compiled.RootFlags = append(compiled.RootFlags, 0)
	compiled.InstructionSourceStarts = append(compiled.InstructionSourceStarts, 0)
	compiled.InstructionSourceEnds = append(compiled.InstructionSourceEnds, 0)
	candidate := policyCandidateFromProgram(t, compiled)
	candidate.CompilerVersion = integrationCompilerVersion + "-v2"

	gotVersion, err := store.PublishActive(ctx, candidate)
	if err != nil {
		t.Fatalf("republish changed Program shape: %v", err)
	}
	assertPolicyVersionEqual(t, gotVersion, wantVersion)
	if got := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policy_nodes WHERE policy_version_id = $1", wantVersion.ID,
	); got != nodeCountBefore {
		t.Fatalf("node count after changed-shape republish = %d, want %d", got, nodeCountBefore)
	}
}

func testPolicyGraphRejectsMalformedRepublish(
	t *testing.T,
	ctx context.Context,
	store *PolicyStore,
	source []byte,
) {
	t.Helper()

	compiled, err := compileIntegrationPolicy(source)
	if err != nil {
		t.Fatalf("compile malformed republish fixture: %v", err)
	}
	compiled.RequirementRoots[0] = 0
	if _, err := store.PublishActive(ctx, policyCandidateFromProgram(t, compiled)); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
		t.Fatalf("malformed republish error = %v, want %v", err, persistence.ErrInvalidPolicyPersistence)
	}
}

func testPolicyGraphRejectsReasonAliasDivergence(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
	store *PolicyStore,
) {
	t.Helper()

	for index, reason := range []schema.ReasonID{
		truth.ReasonWrongScope,
		truth.ReasonWrongSubject,
		truth.ReasonWrongTiming,
		truth.ReasonInvalid,
	} {
		t.Run(fmt.Sprintf("reason_%d", reason), func(t *testing.T) {
			source := nornruneSourceVersion(t, fmt.Sprintf("3.0.%d", index))
			compiled, err := compileIntegrationPolicy(source)
			if err != nil {
				t.Fatalf("compile reason-alias fixture: %v", err)
			}
			clause := (index + 1) % len(compiled.ClauseAssertionRoots)
			resolutionBase := clause * truth.ReasonCount
			unverifiable := compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonUnverifiable-1)]
			replacement := schema.OutcomeID(1)
			if replacement == unverifiable {
				replacement++
			}
			if uint64(replacement) > uint64(len(compiled.Outcomes.Names)) {
				t.Fatalf("reason-alias fixture has no outcome distinct from %d", unverifiable)
			}
			compiled.Resolutions.OutcomeIDs[resolutionBase+int(reason-1)] = replacement
			candidate := policyCandidateFromProgram(t, compiled)
			if _, err := store.PublishActive(ctx, candidate); !errors.Is(err, persistence.ErrInvalidPolicyPersistence) {
				t.Fatalf("reason %d divergence error = %v, want %v", reason, err, persistence.ErrInvalidPolicyPersistence)
			}
			if got := queryCount(t, ctx, environment.runtime,
				"SELECT count(*) FROM nornrune.policy_versions WHERE content_hash = $1", candidate.ContentHash[:],
			); got != 0 {
				t.Fatalf("reason %d divergence durable version count = %d, want 0", reason, got)
			}
		})
	}
}

func testPolicyGraphRejectsPostPublicationInsert(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
	version persistence.PolicyVersion,
	requirementID schema.RequirementID,
) {
	t.Helper()

	connections := []struct {
		name string
		pool *pgxpool.Pool
	}{
		{name: "runtime", pool: environment.runtime},
		{name: "migrator", pool: environment.migrator},
	}
	for _, connection := range connections {
		t.Run(connection.name+"_claim", func(t *testing.T) {
			assertPolicyGraphInsertRejected(t, ctx, connection.pool, `
				INSERT INTO nornrune.policy_nodes
				    (policy_version_id, node_kind, local_id, name, detail,
				     source_start, source_end, content_hash,
				     projected_node_count, projected_edge_count, projection_xid)
				VALUES ($1, 'policy_version', 1, 'forged', '1.0.0', 0, 0,
				        decode(repeat('00', 32), 'hex'), 1, 0, '0'::xid8)
			`, version.ID)
		})
		t.Run(connection.name+"_node", func(t *testing.T) {
			assertPolicyGraphInsertRejected(t, ctx, connection.pool, `
				INSERT INTO nornrune.policy_nodes
				    (policy_version_id, node_kind, local_id, source_start, source_end)
				VALUES ($1, 'requirement', 9223372036854775807, 0, 0)
			`, version.ID)
		})
		t.Run(connection.name+"_edge", func(t *testing.T) {
			assertPolicyGraphInsertRejected(t, ctx, connection.pool, `
				INSERT INTO nornrune.policy_edges
				    (policy_version_id, edge_id, edge_kind, source_kind, source_id,
				     target_kind, target_id, ordinal)
				VALUES ($1, 9223372036854775807, 'CONTAINS', 'policy_version', 1,
				        'requirement', $2, 9223372036854775807)
			`, version.ID, requirementID)
		})
	}
}

func assertPolicyGraphInsertRejected(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	statement string,
	args ...any,
) {
	t.Helper()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin graph insert probe: %v", err)
	}
	_, execErr := tx.Exec(ctx, statement, args...)
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback graph insert probe: %v", err)
	}
	assertSQLState(t, execErr, "55000")
}

func testPolicyGraphRejectsCorruptExistingClaim(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct corrupt-claim policy store: %v", err)
	}
	for index, test := range []struct {
		name         string
		mismatchHash bool
	}{
		{name: "mismatched_hash", mismatchHash: true},
		{name: "incomplete_projection"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := nornruneSourceVersion(t, fmt.Sprintf("4.0.%d", index))
			compiled, err := compileIntegrationPolicy(source)
			if err != nil {
				t.Fatalf("compile corrupt-claim fixture: %v", err)
			}
			candidate := policyCandidateFromProgram(t, compiled)
			claimHash := candidate.ContentHash
			if test.mismatchHash {
				claimHash[0] ^= 0xff
			}
			seedPolicyGraphClaim(t, ctx, environment, candidate, claimHash)

			if _, err := store.PublishActive(ctx, candidate); !errors.Is(err, persistence.ErrStoredPolicyCorrupt) {
				t.Fatalf("publish over %s claim error = %v, want %v", test.name, err, persistence.ErrStoredPolicyCorrupt)
			}
		})
	}
	if got := queryCount(t, ctx, environment.runtime,
		"SELECT count(*) FROM nornrune.policies WHERE active_version_id IS NOT NULL",
	); got != 0 {
		t.Fatalf("active policies after corrupt claims = %d, want 0", got)
	}
}

func seedPolicyGraphClaim(
	t *testing.T,
	ctx context.Context,
	environment *postgresTestEnvironment,
	candidate persistence.Candidate,
	claimHash [32]byte,
) {
	t.Helper()

	tx, err := environment.runtime.Begin(ctx)
	if err != nil {
		t.Fatalf("begin corrupt graph claim: %v", err)
	}
	defer rollbackPolicy(tx, ctx)
	policyID, err := ensurePolicy(ctx, tx, candidate.Name)
	if err != nil {
		t.Fatalf("ensure corrupt-claim policy: %v", err)
	}
	if err := insertPolicyVersion(ctx, tx, policyID, candidate); err != nil {
		t.Fatalf("insert corrupt-claim version: %v", err)
	}
	version, err := loadPolicyVersionByHash(ctx, tx, candidate.ContentHash)
	if err != nil {
		t.Fatalf("load corrupt-claim version: %v", err)
	}
	nodes, err := newPolicyNodeSource(version.ID, candidate.Program)
	if err != nil {
		t.Fatalf("count corrupt-claim nodes: %v", err)
	}
	edges, err := newPolicyEdgeSource(version.ID, candidate.Program)
	if err != nil {
		t.Fatalf("count corrupt-claim edges: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO nornrune.policy_nodes
		    (policy_version_id, node_kind, local_id, name, detail,
		     source_start, source_end, content_hash,
		     projected_node_count, projected_edge_count, projection_xid)
		VALUES ($1, 'policy_version', 1, $2, $3, 0, $4, $5, $6, $7, pg_current_xact_id())
	`, version.ID, candidate.Name, candidate.SemanticVersion, len(candidate.Source), claimHash[:],
		nodes.total+1, edges.total); err != nil {
		t.Fatalf("insert corrupt graph claim: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit corrupt graph claim: %v", err)
	}
}

func testPolicyGraphMigrationDown(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	migrator, err := NewMigrator(environment.migrator, os.DirFS(filepath.Join(environment.root, "migrations")))
	if err != nil {
		t.Fatalf("construct graph migrator: %v", err)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 2 {
		t.Fatalf("migrate graph schema up = (%d, %v), want (2, nil)", changed, err)
	}
	if changed, err := migrator.Down(ctx, 1); err != nil || changed != 1 {
		t.Fatalf("migrate graph schema down = (%d, %v), want (1, nil)", changed, err)
	}
	assertRelationExists(t, ctx, environment.migrator, "nornrune.policy_nodes", false)
	assertRelationExists(t, ctx, environment.migrator, "nornrune.policy_edges", false)
	assertRelationExists(t, ctx, environment.migrator, "nornrune.policy_versions", true)
	if got := queryCount(t, ctx, environment.migrator,
		"SELECT count(*) FROM public.nornrune_schema_migrations",
	); got != 1 {
		t.Fatalf("migration rows after graph down = %d, want 1", got)
	}
	if changed, err := migrator.Up(ctx); err != nil || changed != 1 {
		t.Fatalf("reapply graph migration = (%d, %v), want (1, nil)", changed, err)
	}
	testPolicyGraphSchema(t, ctx, environment)
}

var benchmarkGraphRows int64

func BenchmarkPolicyGraphSources(b *testing.B) {
	compiled, err := compileIntegrationPolicy([]byte(nornrune.Source()))
	if err != nil {
		b.Fatalf("compile benchmark policy: %v", err)
	}
	b.Run("nodes", func(b *testing.B) {
		b.ReportAllocs()
		var rows int64
		for b.Loop() {
			source, err := newPolicyNodeSource(1, compiled)
			if err != nil {
				b.Fatal(err)
			}
			for source.Next() {
				if _, err := source.Values(); err != nil {
					b.Fatal(err)
				}
				rows++
			}
			if err := source.Err(); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkGraphRows = rows
	})
	b.Run("edges", func(b *testing.B) {
		b.ReportAllocs()
		var rows int64
		for b.Loop() {
			source, err := newPolicyEdgeSource(1, compiled)
			if err != nil {
				b.Fatal(err)
			}
			for source.Next() {
				if _, err := source.Values(); err != nil {
					b.Fatal(err)
				}
				rows++
			}
			if err := source.Err(); err != nil {
				b.Fatal(err)
			}
		}
		benchmarkGraphRows = rows
	})
}

type graphPath struct {
	sourceVersion int64
	targetVersion int64
	requirementID int64
	name          string
	branch        string
}

func queryGraphPaths(t *testing.T, ctx context.Context, environment *postgresTestEnvironment, query string) []graphPath {
	t.Helper()

	rows, err := environment.runtime.Query(ctx, query)
	if err != nil {
		t.Fatalf("query policy graph paths: %v", err)
	}
	defer rows.Close()
	paths := make([]graphPath, 0, 8)
	for rows.Next() {
		var path graphPath
		if err := rows.Scan(
			&path.sourceVersion,
			&path.targetVersion,
			&path.requirementID,
			&path.name,
			&path.branch,
		); err != nil {
			t.Fatalf("scan policy graph path: %v", err)
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate policy graph paths: %v", err)
	}
	return paths
}

func testPolicyGraphPGQ(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	store, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	versions := make([]persistence.PolicyVersion, 0, 2)
	for _, source := range [][]byte{[]byte(nornrune.Source()), nornruneSourceVersion(t, "2.0.0")} {
		compiled, err := compileIntegrationPolicy(source)
		if err != nil {
			t.Fatalf("compile graph-query policy: %v", err)
		}
		version, err := store.PublishActive(ctx, policyCandidateFromProgram(t, compiled))
		if err != nil {
			t.Fatalf("publish graph-query policy: %v", err)
		}
		versions = append(versions, version)
	}

	rejectPaths := queryGraphPaths(t, ctx, environment, `
		SELECT source_version, target_version, requirement_id, outcome_name, branch
		FROM GRAPH_TABLE (
			nornrune.policy_graph
			MATCH
				(requirement IS requirement)
				-[IS "CONTAINS"]->(clause IS clause)
				-[resolution IS "RESOLVES_TO"]->(outcome IS outcome WHERE outcome.name = 'Reject')
			COLUMNS (
				requirement.policy_version_id AS source_version,
				outcome.policy_version_id AS target_version,
				requirement.local_id AS requirement_id,
				outcome.name AS outcome_name,
				resolution.branch AS branch
			)
		)
		WHERE branch = 'false'
		ORDER BY source_version, requirement_id
	`)
	if len(rejectPaths) != 4 {
		t.Fatalf("requirement-to-Reject paths = %+v, want four", rejectPaths)
	}
	for index, path := range rejectPaths {
		version := versions[index/2]
		wantRequirement := int64(1 + 2*(index%2))
		if path.sourceVersion != int64(version.ID) || path.targetVersion != int64(version.ID) ||
			path.requirementID != wantRequirement || path.name != "Reject" || path.branch != "false" {
			t.Fatalf("Reject path %d = %+v, want version %d requirement %d", index, path, version.ID, wantRequirement)
		}
	}

	evidencePaths := queryGraphPaths(t, ctx, environment, `
		SELECT source_version, target_version, requirement_id, evidence_kind, edge_kind
		FROM GRAPH_TABLE (
			nornrune.policy_graph
			MATCH
				(requirement IS requirement)
				-[IS "CONTAINS"]->(clause IS clause)
				-[dependency IS "REQUIRES"]->(
					evidence IS evidence_requirement
					WHERE evidence.name = 'execution_environment_attestation'
				)
			COLUMNS (
				requirement.policy_version_id AS source_version,
				evidence.policy_version_id AS target_version,
				requirement.local_id AS requirement_id,
				evidence.name AS evidence_kind,
				dependency.edge_kind AS edge_kind
			)
		)
		ORDER BY source_version
	`)
	if len(evidencePaths) != 2 {
		t.Fatalf("environment evidence paths = %+v, want two", evidencePaths)
	}
	for index, path := range evidencePaths {
		if path.sourceVersion != int64(versions[index].ID) || path.targetVersion != int64(versions[index].ID) ||
			path.requirementID != 2 || path.name != "execution_environment_attestation" || path.branch != "REQUIRES" {
			t.Fatalf("environment evidence path %d = %+v, want version %d requirement 2", index, path, versions[index].ID)
		}
	}

	rows, err := environment.runtime.Query(ctx, `
		SELECT policy_version_id, evidence_id, count(*) AS dependency_count
		FROM GRAPH_TABLE (
			nornrune.policy_graph
			MATCH
				(IS clause)-[IS "REQUIRES"]->(
					evidence IS evidence_requirement
					WHERE evidence.name = 'approval_record'
				)
			COLUMNS (
				evidence.policy_version_id AS policy_version_id,
				evidence.local_id AS evidence_id
			)
		)
		GROUP BY policy_version_id, evidence_id
		HAVING count(*) = 2
		ORDER BY policy_version_id
	`)
	if err != nil {
		t.Fatalf("query shared evidence requirements: %v", err)
	}
	defer rows.Close()
	shared := 0
	for rows.Next() {
		var versionID, evidenceID, dependencies int64
		if err := rows.Scan(&versionID, &evidenceID, &dependencies); err != nil {
			t.Fatalf("scan shared evidence requirement: %v", err)
		}
		if shared >= len(versions) || versionID != int64(versions[shared].ID) || evidenceID <= 0 || dependencies != 2 {
			t.Fatalf("shared evidence row %d = version:%d evidence:%d dependencies:%d", shared, versionID, evidenceID, dependencies)
		}
		shared++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate shared evidence requirements: %v", err)
	}
	if shared != len(versions) {
		t.Fatalf("shared evidence rows = %d, want %d", shared, len(versions))
	}
}
