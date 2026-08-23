//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

func setAuditKey(t *testing.T, batch *persistence.AuditBatch, key string) {
	t.Helper()
	value := batch.IdempotencyKey.Bytes(batch.Bytes)
	if len(value) != len(key) {
		t.Fatalf("replacement audit key length = %d, want %d", len(key), len(value))
	}
	copy(value, key)
}

func multiRowAuditBatch(batch persistence.AuditBatch) persistence.AuditBatch {
	evidencePayload := appendWriterText(&batch, `{"evidence":"E2"}`)
	batch.Evidence.Keys = append(batch.Evidence.Keys, appendWriterText(&batch, "E2"))
	batch.Evidence.Payloads = append(batch.Evidence.Payloads, evidencePayload)
	batch.Evidence.Hashes = append(batch.Evidence.Hashes, sha256.Sum256(evidencePayload.Bytes(batch.Bytes)))
	batch.Evidence.CapturedAt = append(batch.Evidence.CapturedAt, batch.StartedAt)
	batch.Evidence.ExpiresAt = append(batch.Evidence.ExpiresAt, time.Time{})

	first := batch.Findings
	batch.Findings = persistence.AuditFindings{
		Rationales:           []persistence.ByteRange{first.Rationales[0], first.Rationales[0], first.Rationales[0]},
		DriverRequirementIDs: []persistence.ByteRange{first.DriverRequirementIDs[0], first.DriverRequirementIDs[0], first.DriverRequirementIDs[0]},
		DriverClauseIDs:      []persistence.ByteRange{first.DriverClauseIDs[0], first.DriverClauseIDs[0], first.DriverClauseIDs[0]},
		DriverReasons:        []persistence.ByteRange{first.DriverReasons[0], first.DriverReasons[0], first.DriverReasons[0]},
		AppliedRequirements:  []persistence.ByteRange{first.AppliedRequirements[0], first.AppliedRequirements[0], first.AppliedRequirements[0]},
		MissingEvidence:      []persistence.ByteRange{first.MissingEvidence[0], first.MissingEvidence[0], first.MissingEvidence[0]},
		Assumptions:          []persistence.ByteRange{first.Assumptions[0], first.Assumptions[0], first.Assumptions[0]},
		Uncertainty:          []persistence.ByteRange{first.Uncertainty[0], first.Uncertainty[0], first.Uncertainty[0]},
		Remediation:          []persistence.ByteRange{first.Remediation[0], first.Remediation[0], first.Remediation[0]},
		RequestRows:          []uint32{0, 0, 0},
		EvidenceOffsets:      []uint32{0, 2, 2, 3},
		EvidenceRows:         []uint32{0, 1, 1},
		Decisions: []persistence.Decision{
			persistence.DecisionApprove,
			persistence.DecisionRevise,
			persistence.DecisionEscalate,
		},
	}
	batch.Rows = 3
	return batch
}

func testDecisionAuditJournal(t *testing.T, ctx context.Context, environment *postgresTestEnvironment) {
	t.Helper()

	policyStore, err := NewPolicyStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct policy store: %v", err)
	}
	version, err := policyStore.PublishActive(ctx, policyCandidate(
		"audit-policy",
		"1.0.0",
		"audit-compiler",
		[]byte(`{"name":"audit-policy","version":"1.0.0"}`),
	))
	if err != nil {
		t.Fatalf("publish audit policy: %v", err)
	}
	store, err := NewAuditStore(environment.runtime)
	if err != nil {
		t.Fatalf("construct audit store: %v", err)
	}
	batch := testWriterBatch()
	batch.PolicyVersionID = version.ID
	futureCapture := time.Date(2500, time.January, 2, 3, 4, 5, 123_457_000, time.UTC)
	batch.Requests.CapturedAt[0] = futureCapture
	batch.Evidence.CapturedAt[0] = futureCapture
	batch.Evidence.ExpiresAt[0] = futureCapture.Add(time.Hour + time.Microsecond)

	off, err := NewJournal(nil, JournalConfig{Mode: persistence.AuditOff})
	if err != nil {
		t.Fatalf("construct off journal: %v", err)
	}
	if err := off.Submit(ctx, &batch); err != nil {
		t.Fatalf("submit off audit: %v", err)
	}
	if err := off.Close(ctx); err != nil {
		t.Fatalf("close off audit: %v", err)
	}
	if got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx.evaluation_runs"); got != 0 {
		t.Fatalf("run count after off mode = %d, want 0", got)
	}

	required, err := NewJournal(store, testJournalConfig(persistence.AuditRequired))
	if err != nil {
		t.Fatalf("construct required journal: %v", err)
	}
	if err := required.Submit(ctx, &batch); err != nil {
		t.Fatalf("submit required audit: %v", err)
	}
	if err := required.Close(ctx); err != nil {
		t.Fatalf("close required audit: %v", err)
	}

	setAuditKey(t, &batch, "audit-2")
	bestEffort, err := NewJournal(store, testJournalConfig(persistence.AuditBestEffort))
	if err != nil {
		t.Fatalf("construct best-effort journal: %v", err)
	}
	if err := bestEffort.Submit(ctx, &batch); err != nil {
		t.Fatalf("submit best-effort audit: %v", err)
	}
	if err := bestEffort.Close(ctx); err != nil {
		t.Fatalf("close best-effort audit: %v", err)
	}
	if stats := bestEffort.Stats(); stats.Accepted != 1 || stats.Succeeded != 1 || stats.Failed != 0 {
		t.Fatalf("best-effort Stats() = %+v", stats)
	}

	if err := store.Append(ctx, &batch); err != nil {
		t.Fatalf("idempotent audit replay: %v", err)
	}
	setAuditKey(t, &batch, "audit-3")
	if err := store.Append(ctx, &batch); err != nil {
		t.Fatalf("snapshot-reusing audit: %v", err)
	}

	wantCounts := map[string]int{
		"evaluation_runs":     3,
		"requests":            1,
		"evidence_snapshots":  1,
		"evaluation_findings": 3,
		"evaluation_evidence": 3,
	}
	for table, want := range wantCounts {
		got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx."+table)
		if got != want {
			t.Fatalf("%s count = %d, want %d", table, got, want)
		}
	}

	var (
		idempotencyKey string
		engineVersion  string
		requestKey     string
		decision       string
		evidenceKey    string
		policyID       int64
		rowIndex       int64
		ordinal        int64
		requestTime    time.Time
		evidenceTime   time.Time
		expiresTime    time.Time
	)
	if err := environment.runtime.QueryRow(ctx, `
		SELECT run.idempotency_key, run.policy_version_id, run.engine_version,
		       finding.row_index, request.request_key, request.captured_at,
		       finding.decision, link.evidence_ordinal, evidence.evidence_key,
		       evidence.captured_at, evidence.expires_at
		FROM verifoxx.evaluation_runs AS run
		JOIN verifoxx.evaluation_findings AS finding ON finding.run_id = run.id
		JOIN verifoxx.requests AS request ON request.id = finding.request_id
		JOIN verifoxx.evaluation_evidence AS link
		  ON link.run_id = finding.run_id AND link.row_index = finding.row_index
		JOIN verifoxx.evidence_snapshots AS evidence ON evidence.id = link.evidence_snapshot_id
		WHERE run.idempotency_key = 'audit-3'
	`).Scan(
		&idempotencyKey,
		&policyID,
		&engineVersion,
		&rowIndex,
		&requestKey,
		&requestTime,
		&decision,
		&ordinal,
		&evidenceKey,
		&evidenceTime,
		&expiresTime,
	); err != nil {
		t.Fatalf("query exact audit replay references: %v", err)
	}
	if idempotencyKey != "audit-3" || policyID != int64(version.ID) || engineVersion != "engine-1" ||
		rowIndex != 0 || requestKey != "R1" || decision != "Approve" || ordinal != 0 || evidenceKey != "E1" ||
		!requestTime.Equal(batch.Requests.CapturedAt[0]) || !evidenceTime.Equal(batch.Evidence.CapturedAt[0]) ||
		!expiresTime.Equal(batch.Evidence.ExpiresAt[0]) {
		t.Fatalf("audit replay = key:%q policy:%d engine:%q row:%d request:%q decision:%q ordinal:%d evidence:%q",
			idempotencyKey, policyID, engineVersion, rowIndex, requestKey, decision, ordinal, evidenceKey,
		)
	}

	multi := multiRowAuditBatch(batch)
	setAuditKey(t, &multi, "audit-6")
	if err := store.Append(ctx, &multi); err != nil {
		t.Fatalf("multi-row CSR audit: %v", err)
	}
	wantCounts["evaluation_runs"] = 4
	wantCounts["evidence_snapshots"] = 2
	wantCounts["evaluation_findings"] = 6
	wantCounts["evaluation_evidence"] = 6
	for table, want := range wantCounts {
		got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx."+table)
		if got != want {
			t.Fatalf("%s count after multi-row audit = %d, want %d", table, got, want)
		}
	}
	links, err := environment.runtime.Query(ctx, `
		SELECT finding.row_index, link.evidence_ordinal, evidence.evidence_key
		FROM verifoxx.evaluation_runs AS run
		JOIN verifoxx.evaluation_findings AS finding ON finding.run_id = run.id
		JOIN verifoxx.evaluation_evidence AS link
		  ON link.run_id = finding.run_id AND link.row_index = finding.row_index
		JOIN verifoxx.evidence_snapshots AS evidence ON evidence.id = link.evidence_snapshot_id
		WHERE run.idempotency_key = 'audit-6'
		ORDER BY finding.row_index, link.evidence_ordinal
	`)
	if err != nil {
		t.Fatalf("query multi-row evidence links: %v", err)
	}
	defer links.Close()
	wantRows := [...]int64{0, 0, 2}
	wantOrdinals := [...]int64{0, 1, 0}
	wantEvidence := [...]string{"E1", "E2", "E2"}
	linkIndex := 0
	for links.Next() {
		if linkIndex >= len(wantRows) {
			t.Fatal("query returned extra multi-row evidence link")
		}
		var gotRow, gotOrdinal int64
		var gotEvidence string
		if err := links.Scan(&gotRow, &gotOrdinal, &gotEvidence); err != nil {
			t.Fatalf("scan multi-row evidence link: %v", err)
		}
		if gotRow != wantRows[linkIndex] || gotOrdinal != wantOrdinals[linkIndex] || gotEvidence != wantEvidence[linkIndex] {
			t.Fatalf("link %d = (%d, %d, %q), want (%d, %d, %q)", linkIndex,
				gotRow, gotOrdinal, gotEvidence,
				wantRows[linkIndex], wantOrdinals[linkIndex], wantEvidence[linkIndex],
			)
		}
		linkIndex++
	}
	if err := links.Err(); err != nil {
		t.Fatalf("iterate multi-row evidence links: %v", err)
	}
	if linkIndex != len(wantRows) {
		t.Fatalf("multi-row evidence link count = %d, want %d", linkIndex, len(wantRows))
	}

	if _, err := environment.migrator.Exec(ctx, `
		CREATE FUNCTION verifoxx.reject_test_finding() RETURNS trigger
		LANGUAGE plpgsql SET search_path = pg_catalog AS $$
		BEGIN
			RAISE EXCEPTION 'forced finding failure' USING ERRCODE = 'P0001';
		END;
		$$;
		CREATE TRIGGER reject_test_finding
		BEFORE INSERT ON verifoxx.evaluation_findings
		FOR EACH STATEMENT EXECUTE FUNCTION verifoxx.reject_test_finding();
	`); err != nil {
		t.Fatalf("install finding failure trigger: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = environment.migrator.Exec(cleanupCtx, `
			DROP TRIGGER IF EXISTS reject_test_finding ON verifoxx.evaluation_findings;
			DROP FUNCTION IF EXISTS verifoxx.reject_test_finding();
		`)
	})
	setAuditKey(t, &batch, "audit-4")
	if err := store.Append(ctx, &batch); err == nil {
		t.Fatal("forced late audit failure returned nil")
	}
	for table, want := range wantCounts {
		got := queryCount(t, ctx, environment.runtime, "SELECT count(*) FROM verifoxx."+table)
		if got != want {
			t.Fatalf("%s count after rollback = %d, want %d", table, got, want)
		}
	}

	closedPool := openTestPool(t, ctx, environment.adminURL, "verifoxx_runtime", runtimePassword)
	closedStore, err := NewAuditStore(closedPool)
	if err != nil {
		t.Fatalf("construct closed-pool store: %v", err)
	}
	closedPool.Close()
	setAuditKey(t, &batch, "audit-5")
	if err := closedStore.Append(ctx, &batch); err == nil {
		t.Fatal("audit append through closed pool returned nil")
	}

	if auditStore, err := NewAuditStore(nil); auditStore != nil || !errors.Is(err, persistence.ErrInvalidJournal) {
		t.Fatalf("NewAuditStore(nil) = (%p, %v), want invalid journal", auditStore, err)
	}
}
