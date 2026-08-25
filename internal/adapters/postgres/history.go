package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidDecisionHistory = errors.New("postgres: invalid decision history")

const (
	MaxDecisionHistoryEntries = 64
	maxHistoryRequestKeyBytes = 128
	maxHistoryPolicyBytes     = 128
	maxHistoryVersionBytes    = 64
	maxHistoryDecisionBytes   = 64
)

// DecisionHistoryEntry is one bounded persisted finding for terminal display.
type DecisionHistoryEntry struct {
	CompletedAt time.Time
	Policy      string
	Version     string
	Decision    string
}

type decisionHistoryRows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close()
}

type decisionHistoryQueryer interface {
	Query(context.Context, string, ...any) (decisionHistoryRows, error)
}

type poolDecisionHistoryQueryer struct {
	pool *pgxpool.Pool
}

func (queryer poolDecisionHistoryQueryer) Query(
	ctx context.Context,
	query string,
	arguments ...any,
) (decisionHistoryRows, error) {
	return queryer.pool.Query(ctx, query, arguments...)
}

// DecisionHistoryStore reads bounded audit findings without entering evaluation paths.
type DecisionHistoryStore struct {
	queryer decisionHistoryQueryer
}

// NewDecisionHistoryStore validates the PostgreSQL history dependency.
func NewDecisionHistoryStore(pool *pgxpool.Pool) (*DecisionHistoryStore, error) {
	if pool == nil {
		return nil, ErrInvalidDecisionHistory
	}
	return newDecisionHistoryStore(poolDecisionHistoryQueryer{pool: pool}), nil
}

func newDecisionHistoryStore(queryer decisionHistoryQueryer) *DecisionHistoryStore {
	return &DecisionHistoryStore{queryer: queryer}
}

// Load appends the newest persisted decisions for one exact request key to dst.
func (store *DecisionHistoryStore) Load(
	ctx context.Context,
	requestKey string,
	dst []DecisionHistoryEntry,
) ([]DecisionHistoryEntry, error) {
	start := len(dst)
	if store == nil || store.queryer == nil || ctx == nil ||
		!validHistoryText(requestKey, maxHistoryRequestKeyBytes) || strings.TrimSpace(requestKey) != requestKey {
		return dst, ErrInvalidDecisionHistory
	}
	rows, err := store.queryer.Query(ctx, `
		SELECT run.completed_at, policy.name, version.semantic_version, finding.decision
		FROM verifoxx.evaluation_findings AS finding
		JOIN verifoxx.evaluation_runs AS run ON run.id = finding.run_id
		JOIN verifoxx.requests AS request ON request.id = finding.request_id
		JOIN verifoxx.policy_versions AS version ON version.id = run.policy_version_id
		JOIN verifoxx.policies AS policy ON policy.id = version.policy_id
		WHERE request.request_key = $1
		ORDER BY run.completed_at DESC, run.id DESC, finding.row_index DESC
		LIMIT $2
	`, requestKey, MaxDecisionHistoryEntries)
	if err != nil {
		return dst, fmt.Errorf("postgres: query decision history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry DecisionHistoryEntry
		if err := rows.Scan(&entry.CompletedAt, &entry.Policy, &entry.Version, &entry.Decision); err != nil {
			return dst[:start], fmt.Errorf("postgres: scan decision history: %w", err)
		}
		if entry.CompletedAt.IsZero() || !validHistoryText(entry.Policy, maxHistoryPolicyBytes) ||
			!validHistoryText(entry.Version, maxHistoryVersionBytes) ||
			!validHistoryDecision(entry.Decision) {
			return dst[:start], ErrInvalidDecisionHistory
		}
		dst = append(dst, entry)
		if len(dst)-start == MaxDecisionHistoryEntries {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return dst[:start], fmt.Errorf("postgres: read decision history: %w", err)
	}
	return dst, nil
}

func validHistoryText(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validHistoryDecision(value string) bool {
	if len(value) > maxHistoryDecisionBytes {
		return false
	}
	switch value {
	case "Approve", "Reject", "Revise", "Escalate":
		return true
	default:
		return false
	}
}
