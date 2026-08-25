package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecisionHistoryStoreLoadsNewestBoundedRows(t *testing.T) {
	now := time.Date(2026, time.August, 24, 14, 0, 0, 0, time.UTC)
	queryer := &historyTestQueryer{rows: &historyTestRows{values: []DecisionHistoryEntry{
		{CompletedAt: now, Policy: "verifoxx", Version: "2.0.0", Decision: "Reject"},
		{CompletedAt: now.Add(-time.Hour), Policy: "verifoxx", Version: "1.0.0", Decision: "Approve"},
	}}}
	store := newDecisionHistoryStore(queryer)

	destination := make([]DecisionHistoryEntry, 0, MaxDecisionHistoryEntries)
	got, err := store.Load(context.Background(), "R2", destination)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got) != 2 || got[0].Decision != "Reject" || got[1].Version != "1.0.0" {
		t.Fatalf("Load() = %+v", got)
	}
	if !strings.Contains(queryer.query, "JOIN verifoxx.evaluation_runs") ||
		!strings.Contains(queryer.query, "ORDER BY run.completed_at DESC, run.id DESC, finding.row_index DESC") {
		t.Fatalf("history query lacks stable newest-first joins/order: %s", queryer.query)
	}
	if len(queryer.args) != 2 || queryer.args[0] != "R2" || queryer.args[1] != MaxDecisionHistoryEntries {
		t.Fatalf("history query args = %#v", queryer.args)
	}
	if !queryer.rows.(*historyTestRows).closed {
		t.Fatal("history rows were not closed")
	}
}

func TestDecisionHistoryStoreRejectsInvalidInputAndRows(t *testing.T) {
	store := newDecisionHistoryStore(&historyTestQueryer{rows: &historyTestRows{values: []DecisionHistoryEntry{{
		CompletedAt: time.Now(), Policy: "verifoxx", Version: "1.0.0", Decision: "Maybe",
	}}}})
	if _, err := store.Load(context.Background(), "R1", nil); !errors.Is(err, ErrInvalidDecisionHistory) {
		t.Fatalf("invalid decision error = %v", err)
	}
	if _, err := store.Load(context.Background(), "", nil); !errors.Is(err, ErrInvalidDecisionHistory) {
		t.Fatalf("empty request key error = %v", err)
	}
	if _, err := (*DecisionHistoryStore)(nil).Load(context.Background(), "R1", nil); !errors.Is(err, ErrInvalidDecisionHistory) {
		t.Fatalf("nil store error = %v", err)
	}
}

func TestDecisionHistoryStorePropagatesCancellationAndQueryFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := newDecisionHistoryStore(&historyTestQueryer{respectContext: true})
	if _, err := store.Load(ctx, "R1", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Load() error = %v", err)
	}

	want := errors.New("database unavailable")
	store = newDecisionHistoryStore(&historyTestQueryer{err: want})
	if _, err := store.Load(context.Background(), "R1", nil); !errors.Is(err, want) {
		t.Fatalf("failed query error = %v", err)
	}
}

type historyTestQueryer struct {
	rows           decisionHistoryRows
	err            error
	query          string
	args           []any
	respectContext bool
}

func (queryer *historyTestQueryer) Query(ctx context.Context, query string, args ...any) (decisionHistoryRows, error) {
	queryer.query = query
	queryer.args = append(queryer.args[:0], args...)
	if queryer.respectContext && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return queryer.rows, queryer.err
}

type historyTestRows struct {
	values []DecisionHistoryEntry
	err    error
	row    int
	closed bool
}

func (rows *historyTestRows) Next() bool {
	return rows.row < len(rows.values)
}

func (rows *historyTestRows) Scan(destinations ...any) error {
	if rows.row >= len(rows.values) {
		return errors.New("scan past history rows")
	}
	value := rows.values[rows.row]
	rows.row++
	*destinations[0].(*time.Time) = value.CompletedAt
	*destinations[1].(*string) = value.Policy
	*destinations[2].(*string) = value.Version
	*destinations[3].(*string) = value.Decision
	return nil
}

func (rows *historyTestRows) Err() error { return rows.err }
func (rows *historyTestRows) Close()     { rows.closed = true }
