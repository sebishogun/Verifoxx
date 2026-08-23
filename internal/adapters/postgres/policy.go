package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

const (
	policyVersionSemanticConstraint = "policy_versions_policy_semantic_version_key"
	policyCommitWindow              = 5 * time.Second
	policyRollbackWindow            = 5 * time.Second
)

const selectPolicyVersionByHashSQL = `
SELECT p.id, v.id, p.name, v.semantic_version, v.source,
       v.content_hash, v.compiler_version, v.published_at
FROM verifoxx.policy_versions AS v
JOIN verifoxx.policies AS p ON p.id = v.policy_id
WHERE v.content_hash = $1
`

const selectActivePolicyVersionSQL = `
SELECT p.id, v.id, p.name, v.semantic_version, v.source,
       v.content_hash, v.compiler_version, v.published_at
FROM verifoxx.policies AS p
JOIN verifoxx.policy_versions AS v ON v.id = p.active_version_id
WHERE p.name = $1
`

// PolicyStore persists canonical source and active-version selection through a
// runtime-role pgx pool.
type PolicyStore struct {
	pool *pgxpool.Pool
}

// NewPolicyStore validates the PostgreSQL policy-store dependency.
func NewPolicyStore(pool *pgxpool.Pool) (*PolicyStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: nil PostgreSQL policy pool", persistence.ErrInvalidPolicyPersistence)
	}
	return &PolicyStore{pool: pool}, nil
}

// PublishActive inserts immutable source metadata idempotently and selects it
// as the policy's active version in the same transaction.
func (store *PolicyStore) PublishActive(
	ctx context.Context,
	candidate persistence.Candidate,
) (persistence.PolicyVersion, error) {
	if err := store.validContext(ctx); err != nil {
		return persistence.PolicyVersion{}, err
	}
	if err := persistence.ValidateCandidate(candidate); err != nil {
		return persistence.PolicyVersion{}, err
	}

	tx, err := store.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return persistence.PolicyVersion{}, fmt.Errorf("postgres: begin policy publication: %w", err)
	}
	defer rollbackPolicy(tx, ctx)

	policyID, err := ensurePolicy(ctx, tx, candidate.Name)
	if err != nil {
		return persistence.PolicyVersion{}, err
	}
	if err := insertPolicyVersion(ctx, tx, policyID, candidate); err != nil {
		return persistence.PolicyVersion{}, err
	}
	version, err := loadPolicyVersionByHash(ctx, tx, candidate.ContentHash)
	if err != nil {
		if errors.Is(err, persistence.ErrStoredPolicyNotFound) {
			return persistence.PolicyVersion{}, fmt.Errorf("%w: published version disappeared", persistence.ErrStoredPolicyCorrupt)
		}
		return persistence.PolicyVersion{}, err
	}
	if err := validatePublishedVersion(version, policyID, candidate); err != nil {
		return persistence.PolicyVersion{}, err
	}
	if err := writePolicyGraph(ctx, tx, version.ID, candidate); err != nil {
		return persistence.PolicyVersion{}, err
	}

	tag, err := tx.Exec(ctx, `
		UPDATE verifoxx.policies
		SET active_version_id = $1
		WHERE id = $2
	`, version.ID, policyID)
	if err != nil {
		return persistence.PolicyVersion{}, fmt.Errorf("postgres: activate policy version: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: policy identity disappeared", persistence.ErrStoredPolicyCorrupt)
	}
	if _, err := tx.Exec(ctx,
		"SELECT pg_notify($1, encode($2::bytea, 'hex'))",
		policyNotificationChannel(candidate.Name),
		version.ContentHash[:],
	); err != nil {
		return persistence.PolicyVersion{}, fmt.Errorf("postgres: notify policy publication: %w", err)
	}
	if err := commitPolicy(tx, ctx); err != nil {
		return persistence.PolicyVersion{}, fmt.Errorf("postgres: commit policy publication: %w", err)
	}
	return version, nil
}

// LoadActive returns the complete active version for one exact policy name.
func (store *PolicyStore) LoadActive(ctx context.Context, name string) (persistence.PolicyVersion, error) {
	if err := store.validContext(ctx); err != nil {
		return persistence.PolicyVersion{}, err
	}
	if name == "" || name != strings.TrimSpace(name) {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: empty policy name", persistence.ErrInvalidPolicyPersistence)
	}
	version, err := scanPolicyVersion(store.pool.QueryRow(ctx, selectActivePolicyVersionSQL, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: active policy", persistence.ErrStoredPolicyNotFound)
	}
	if err != nil {
		return persistence.PolicyVersion{}, fmt.Errorf("postgres: load active policy: %w", err)
	}
	if version.Name != name {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: active policy identity", persistence.ErrStoredPolicyCorrupt)
	}
	return version, nil
}

// LoadByHash returns the complete immutable version for one exact digest.
func (store *PolicyStore) LoadByHash(
	ctx context.Context,
	hash [sha256.Size]byte,
) (persistence.PolicyVersion, error) {
	if err := store.validContext(ctx); err != nil {
		return persistence.PolicyVersion{}, err
	}
	if hash == [sha256.Size]byte{} {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: zero policy hash", persistence.ErrInvalidPolicyPersistence)
	}
	return loadPolicyVersionByHash(ctx, store.pool, hash)
}

func (store *PolicyStore) validContext(ctx context.Context) error {
	if store == nil || store.pool == nil || ctx == nil {
		return fmt.Errorf("%w: PostgreSQL policy store", persistence.ErrInvalidPolicyPersistence)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", persistence.ErrInvalidPolicyPersistence, err)
	}
	return nil
}

func ensurePolicy(ctx context.Context, tx pgx.Tx, name string) (persistence.PolicyID, error) {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO verifoxx.policies (name)
		VALUES ($1)
		ON CONFLICT (name) DO NOTHING
		RETURNING id
	`, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			"SELECT id FROM verifoxx.policies WHERE name = $1",
			name,
		).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: ensure policy identity: %w", err)
	}
	if id <= 0 {
		return 0, fmt.Errorf("%w: invalid policy identity", persistence.ErrStoredPolicyCorrupt)
	}
	return persistence.PolicyID(id), nil
}

func insertPolicyVersion(
	ctx context.Context,
	tx pgx.Tx,
	policyID persistence.PolicyID,
	candidate persistence.Candidate,
) error {
	var id int64
	err := tx.QueryRow(ctx, `
		INSERT INTO verifoxx.policy_versions
		    (policy_id, semantic_version, source, content_hash, compiler_version)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (content_hash) DO NOTHING
		RETURNING id
	`,
		policyID,
		candidate.SemanticVersion,
		candidate.Source,
		candidate.ContentHash[:],
		candidate.CompilerVersion,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.ConstraintName == policyVersionSemanticConstraint {
			return fmt.Errorf("%w: policy %d semantic version", persistence.ErrPolicyVersionConflict, policyID)
		}
		return fmt.Errorf("postgres: insert policy version: %w", err)
	}
	if id <= 0 {
		return fmt.Errorf("%w: invalid policy version identity", persistence.ErrStoredPolicyCorrupt)
	}
	return nil
}

type policyVersionQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadPolicyVersionByHash(
	ctx context.Context,
	queryer policyVersionQueryer,
	hash [sha256.Size]byte,
) (persistence.PolicyVersion, error) {
	version, err := scanPolicyVersion(queryer.QueryRow(ctx, selectPolicyVersionByHashSQL, hash[:]))
	if errors.Is(err, pgx.ErrNoRows) {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: policy hash", persistence.ErrStoredPolicyNotFound)
	}
	if err != nil {
		return persistence.PolicyVersion{}, fmt.Errorf("postgres: load policy hash: %w", err)
	}
	if version.ContentHash != hash {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: selected policy hash", persistence.ErrStoredPolicyCorrupt)
	}
	return version, nil
}

func scanPolicyVersion(row pgx.Row) (persistence.PolicyVersion, error) {
	var (
		version         persistence.PolicyVersion
		source          []byte
		hash            []byte
		policyID        int64
		policyVersionID int64
	)
	if err := row.Scan(
		&policyID,
		&policyVersionID,
		&version.Name,
		&version.SemanticVersion,
		&source,
		&hash,
		&version.CompilerVersion,
		&version.PublishedAt,
	); err != nil {
		return persistence.PolicyVersion{}, err
	}
	if len(hash) != sha256.Size {
		return persistence.PolicyVersion{}, fmt.Errorf("%w: content hash size", persistence.ErrStoredPolicyCorrupt)
	}
	version.Source = append([]byte(nil), source...)
	copy(version.ContentHash[:], hash)
	version.PolicyID = persistence.PolicyID(policyID)
	version.ID = persistence.PolicyVersionID(policyVersionID)
	if err := persistence.ValidatePolicyVersion(version); err != nil {
		return persistence.PolicyVersion{}, err
	}
	return version, nil
}

func validatePublishedVersion(
	version persistence.PolicyVersion,
	policyID persistence.PolicyID,
	candidate persistence.Candidate,
) error {
	if version.PolicyID != policyID || version.Name != candidate.Name ||
		version.SemanticVersion != candidate.SemanticVersion ||
		version.ContentHash != candidate.ContentHash ||
		!bytes.Equal(version.Source, candidate.Source) {
		return fmt.Errorf("%w: published policy identity", persistence.ErrStoredPolicyCorrupt)
	}
	return nil
}

func rollbackPolicy(tx pgx.Tx, parent context.Context) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), policyRollbackWindow)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func commitPolicy(tx pgx.Tx, parent context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), policyCommitWindow)
	defer cancel()
	return tx.Commit(ctx)
}
