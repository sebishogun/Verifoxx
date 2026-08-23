package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

const (
	auditCommitWindow   = 5 * time.Second
	auditRollbackWindow = 5 * time.Second
)

// AuditStore appends complete immutable audit batches through a runtime-role pool.
type AuditStore struct {
	pool *pgxpool.Pool
}

// NewAuditStore validates the PostgreSQL audit-store dependency.
func NewAuditStore(pool *pgxpool.Pool) (*AuditStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: nil PostgreSQL audit pool", persistence.ErrInvalidJournal)
	}
	return &AuditStore{pool: pool}, nil
}

// Append writes a complete evaluation run and all replay references atomically.
func (store *AuditStore) Append(ctx context.Context, batch *persistence.AuditBatch) error {
	if store == nil || store.pool == nil || ctx == nil {
		return fmt.Errorf("%w: PostgreSQL audit store", persistence.ErrInvalidJournal)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := persistence.ValidateAuditBatch(batch); err != nil {
		return err
	}
	prepareAuditResolution(batch)

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("postgres: begin audit transaction: %w", err)
	}
	defer rollbackAudit(tx, ctx)

	runID, inserted, err := insertAuditRun(ctx, tx, batch)
	if err != nil {
		return err
	}
	if !inserted {
		return nil
	}
	if err := insertAuditSnapshots(ctx, tx, batch); err != nil {
		return err
	}
	if err := resolveAuditSnapshotIDs(ctx, tx, batch); err != nil {
		return err
	}
	if err := copyAuditFindings(ctx, tx, runID, batch); err != nil {
		return err
	}
	if err := copyAuditEvidence(ctx, tx, runID, batch); err != nil {
		return err
	}
	if err := commitAudit(tx, ctx); err != nil {
		return fmt.Errorf("postgres: commit audit transaction: %w", err)
	}
	return nil
}

func prepareAuditResolution(batch *persistence.AuditBatch) {
	requestCount := len(batch.Requests.Keys)
	if cap(batch.Requests.ResolvedIDs) < requestCount {
		batch.Requests.ResolvedIDs = make([]int64, requestCount)
	} else {
		batch.Requests.ResolvedIDs = batch.Requests.ResolvedIDs[:requestCount]
		clear(batch.Requests.ResolvedIDs)
	}
	evidenceCount := len(batch.Evidence.Keys)
	if cap(batch.Evidence.ResolvedIDs) < evidenceCount {
		batch.Evidence.ResolvedIDs = make([]int64, evidenceCount)
	} else {
		batch.Evidence.ResolvedIDs = batch.Evidence.ResolvedIDs[:evidenceCount]
		clear(batch.Evidence.ResolvedIDs)
	}
}

func insertAuditRun(
	ctx context.Context,
	tx pgx.Tx,
	batch *persistence.AuditBatch,
) (int64, bool, error) {
	var runID int64
	err := tx.QueryRow(ctx, `
		INSERT INTO verifoxx.evaluation_runs
		    (idempotency_key, policy_version_id, engine_version,
		     started_at, completed_at, row_count, execution_metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id
	`,
		batch.IdempotencyKey.Bytes(batch.Bytes),
		batch.PolicyVersionID,
		batch.EngineVersion.Bytes(batch.Bytes),
		batch.StartedAt,
		batch.CompletedAt,
		int64(batch.Rows),
		batch.ExecutionMetadata.Bytes(batch.Bytes),
	).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("postgres: insert audit run: %w", err)
	}
	if runID <= 0 {
		return 0, false, fmt.Errorf("%w: invalid audit run identity", persistence.ErrInvalidJournal)
	}
	return runID, true, nil
}

func insertAuditSnapshots(ctx context.Context, tx pgx.Tx, batch *persistence.AuditBatch) error {
	var commands pgx.Batch
	for row := range batch.Requests.Keys {
		commands.Queue(`
			INSERT INTO verifoxx.requests (request_key, content_hash, payload, captured_at)
			VALUES ($1, $2, $3::jsonb,
			        timestamptz 'epoch'
			        + ($4::bigint / 1000000) * interval '1 second'
			        + ($4::bigint % 1000000) * interval '1 microsecond')
			ON CONFLICT (request_key, content_hash) DO NOTHING
		`,
			batch.Requests.Keys[row].Bytes(batch.Bytes),
			batch.Requests.Hashes[row][:],
			batch.Requests.Payloads[row].Bytes(batch.Bytes),
			batch.Requests.CapturedAt[row].UnixMicro(),
		)
	}
	for row := range batch.Evidence.Keys {
		var expires any
		if !batch.Evidence.ExpiresAt[row].IsZero() {
			expires = batch.Evidence.ExpiresAt[row].UnixMicro()
		}
		commands.Queue(`
			INSERT INTO verifoxx.evidence_snapshots
			    (evidence_key, content_hash, payload, captured_at, expires_at)
			VALUES ($1, $2, $3::jsonb,
			        timestamptz 'epoch'
			        + ($4::bigint / 1000000) * interval '1 second'
			        + ($4::bigint % 1000000) * interval '1 microsecond',
			        CASE WHEN $5::bigint IS NULL THEN NULL
			             ELSE timestamptz 'epoch'
			                  + ($5::bigint / 1000000) * interval '1 second'
			                  + ($5::bigint % 1000000) * interval '1 microsecond'
			        END)
			ON CONFLICT (evidence_key, content_hash) DO NOTHING
		`,
			batch.Evidence.Keys[row].Bytes(batch.Bytes),
			batch.Evidence.Hashes[row][:],
			batch.Evidence.Payloads[row].Bytes(batch.Bytes),
			batch.Evidence.CapturedAt[row].UnixMicro(),
			expires,
		)
	}
	if commands.Len() == 0 {
		return nil
	}
	results := tx.SendBatch(ctx, &commands)
	if err := results.Close(); err != nil {
		return fmt.Errorf("postgres: insert audit snapshots: %w", err)
	}
	return nil
}

func resolveAuditSnapshotIDs(ctx context.Context, tx pgx.Tx, batch *persistence.AuditBatch) error {
	var queries pgx.Batch
	for row := range batch.Requests.Keys {
		queries.Queue(`
			SELECT id FROM verifoxx.requests
			WHERE request_key = $1 AND content_hash = $2
		`, batch.Requests.Keys[row].Bytes(batch.Bytes), batch.Requests.Hashes[row][:])
	}
	for row := range batch.Evidence.Keys {
		queries.Queue(`
			SELECT id FROM verifoxx.evidence_snapshots
			WHERE evidence_key = $1 AND content_hash = $2
		`, batch.Evidence.Keys[row].Bytes(batch.Bytes), batch.Evidence.Hashes[row][:])
	}
	if queries.Len() == 0 {
		return nil
	}
	results := tx.SendBatch(ctx, &queries)
	for row := range batch.Requests.Keys {
		if err := results.QueryRow().Scan(&batch.Requests.ResolvedIDs[row]); err != nil {
			_ = results.Close()
			return fmt.Errorf("postgres: resolve request snapshot %d: %w", row, err)
		}
		if batch.Requests.ResolvedIDs[row] <= 0 {
			_ = results.Close()
			return fmt.Errorf("%w: invalid request snapshot identity", persistence.ErrInvalidJournal)
		}
	}
	for row := range batch.Evidence.Keys {
		if err := results.QueryRow().Scan(&batch.Evidence.ResolvedIDs[row]); err != nil {
			_ = results.Close()
			return fmt.Errorf("postgres: resolve evidence snapshot %d: %w", row, err)
		}
		if batch.Evidence.ResolvedIDs[row] <= 0 {
			_ = results.Close()
			return fmt.Errorf("%w: invalid evidence snapshot identity", persistence.ErrInvalidJournal)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("postgres: close audit snapshot lookup: %w", err)
	}
	return nil
}

func copyAuditFindings(ctx context.Context, tx pgx.Tx, runID int64, batch *persistence.AuditBatch) error {
	if batch.Rows == 0 {
		return nil
	}
	source := auditFindingSource{runID: runID, batch: batch}
	copied, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"verifoxx", "evaluation_findings"},
		[]string{
			"run_id",
			"row_index",
			"request_id",
			"decision",
			"rationale",
			"driver_requirement_id",
			"driver_clause_id",
			"driver_reason",
			"applied_requirements",
			"missing_or_conflicting_evidence",
			"assumptions",
			"unresolved_uncertainty",
			"remediation",
		},
		&source,
	)
	if err != nil {
		return fmt.Errorf("postgres: copy audit findings: %w", err)
	}
	if copied != int64(batch.Rows) {
		return fmt.Errorf("%w: copied %d of %d findings", persistence.ErrInvalidJournal, copied, batch.Rows)
	}
	return nil
}

func copyAuditEvidence(ctx context.Context, tx pgx.Tx, runID int64, batch *persistence.AuditBatch) error {
	if len(batch.Findings.EvidenceRows) == 0 {
		return nil
	}
	source := auditEvidenceSource{runID: runID, batch: batch}
	copied, err := tx.CopyFrom(
		ctx,
		pgx.Identifier{"verifoxx", "evaluation_evidence"},
		[]string{"run_id", "row_index", "evidence_ordinal", "evidence_snapshot_id"},
		&source,
	)
	if err != nil {
		return fmt.Errorf("postgres: copy audit evidence links: %w", err)
	}
	if copied != int64(len(batch.Findings.EvidenceRows)) {
		return fmt.Errorf("%w: copied %d of %d evidence links",
			persistence.ErrInvalidJournal, copied, len(batch.Findings.EvidenceRows),
		)
	}
	return nil
}

type auditFindingSource struct {
	batch  *persistence.AuditBatch
	values [13]any
	runID  int64
	row    int
}

func (source *auditFindingSource) Next() bool {
	if source.row >= int(source.batch.Rows) {
		return false
	}
	source.row++
	return true
}

func (source *auditFindingSource) Values() ([]any, error) {
	row := source.row - 1
	findings := &source.batch.Findings
	source.values[0] = source.runID
	source.values[1] = int64(row)
	source.values[2] = source.batch.Requests.ResolvedIDs[findings.RequestRows[row]]
	source.values[3] = findings.Decisions[row].String()
	source.values[4] = findings.Rationales[row].Bytes(source.batch.Bytes)
	source.values[5] = nullableAuditText(source.batch.Bytes, findings.DriverRequirementIDs[row])
	source.values[6] = nullableAuditText(source.batch.Bytes, findings.DriverClauseIDs[row])
	source.values[7] = nullableAuditText(source.batch.Bytes, findings.DriverReasons[row])
	source.values[8] = findings.AppliedRequirements[row].Bytes(source.batch.Bytes)
	source.values[9] = findings.MissingEvidence[row].Bytes(source.batch.Bytes)
	source.values[10] = findings.Assumptions[row].Bytes(source.batch.Bytes)
	source.values[11] = findings.Uncertainty[row].Bytes(source.batch.Bytes)
	source.values[12] = findings.Remediation[row].Bytes(source.batch.Bytes)
	return source.values[:], nil
}

func (*auditFindingSource) Err() error {
	return nil
}

type auditEvidenceSource struct {
	batch   *persistence.AuditBatch
	values  [4]any
	runID   int64
	edge    int
	row     int
	current int
}

func (source *auditEvidenceSource) Next() bool {
	if source.edge >= len(source.batch.Findings.EvidenceRows) {
		return false
	}
	for source.row+1 < len(source.batch.Findings.EvidenceOffsets) &&
		uint64(source.edge) >= uint64(source.batch.Findings.EvidenceOffsets[source.row+1]) {
		source.row++
	}
	source.current = source.edge
	source.edge++
	return true
}

func (source *auditEvidenceSource) Values() ([]any, error) {
	evidenceRow := source.batch.Findings.EvidenceRows[source.current]
	source.values[0] = source.runID
	source.values[1] = int64(source.row)
	source.values[2] = int64(source.current) - int64(source.batch.Findings.EvidenceOffsets[source.row])
	source.values[3] = source.batch.Evidence.ResolvedIDs[evidenceRow]
	return source.values[:], nil
}

func (*auditEvidenceSource) Err() error {
	return nil
}

func nullableAuditText(data []byte, value persistence.ByteRange) any {
	if value.Start == value.End {
		return nil
	}
	return value.Bytes(data)
}

func rollbackAudit(tx pgx.Tx, parent context.Context) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), auditRollbackWindow)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func commitAudit(tx pgx.Tx, parent context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), auditCommitWindow)
	defer cancel()
	return tx.Commit(ctx)
}
