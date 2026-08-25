package cli

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/sebishogun/verifoxx/internal/adapters/postgres"
	tuiadapter "github.com/sebishogun/verifoxx/internal/adapters/tui"
	"github.com/sebishogun/verifoxx/internal/config"
)

var errPersistedHistoryUnavailable = errors.New("persisted history unavailable")

type tuiPersistedHistoryStore interface {
	Load(
		context.Context,
		string,
		[]postgresadapter.DecisionHistoryEntry,
	) ([]postgresadapter.DecisionHistoryEntry, error)
}

type tuiHistoryPool interface {
	Close()
}

type tuiHistoryLoader struct {
	store       tuiPersistedHistoryStore
	pool        tuiHistoryPool
	databaseURL config.SecretURL
	rows        []postgresadapter.DecisionHistoryEntry
	entries     []tuiadapter.HistoryEntry
}

func newTUIHistoryLoader(databaseURL config.SecretURL) *tuiHistoryLoader {
	if databaseURL.Empty() {
		return nil
	}
	return &tuiHistoryLoader{databaseURL: databaseURL}
}

func (loader *tuiHistoryLoader) LoadHistory(
	ctx context.Context,
	request tuiadapter.RequestItem,
) ([]tuiadapter.HistoryEntry, error) {
	if loader == nil || ctx == nil || request.ID == 0 || request.Name == "" ||
		len(request.Name) > tuiadapter.MaxRequestName {
		return nil, errPersistedHistoryUnavailable
	}
	if loader.store == nil {
		if err := loader.connect(ctx); err != nil {
			return nil, errPersistedHistoryUnavailable
		}
	}
	rows, err := loader.store.Load(ctx, request.Name, loader.rows[:0])
	if err != nil || len(rows) > postgresadapter.MaxDecisionHistoryEntries {
		return nil, errPersistedHistoryUnavailable
	}
	loader.rows = rows
	if cap(loader.entries) < len(rows) {
		loader.entries = make([]tuiadapter.HistoryEntry, len(rows))
	} else {
		loader.entries = loader.entries[:len(rows)]
	}
	for index := range rows {
		row := rows[len(rows)-1-index]
		loader.entries[index] = tuiadapter.HistoryEntry{
			At:       row.CompletedAt,
			Policy:   row.Policy,
			Version:  row.Version,
			Decision: row.Decision,
		}
	}
	return loader.entries, nil
}

func (loader *tuiHistoryLoader) connect(ctx context.Context) error {
	poolConfig, err := pgxpool.ParseConfig(loader.databaseURL.Reveal())
	if err != nil {
		return err
	}
	poolConfig.MinConns = 0
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	store, err := postgresadapter.NewDecisionHistoryStore(pool)
	if err != nil {
		pool.Close()
		return err
	}
	loader.pool = pool
	loader.store = store
	return nil
}

func (loader *tuiHistoryLoader) Close() {
	if loader != nil && loader.pool != nil {
		loader.pool.Close()
		loader.pool = nil
		loader.store = nil
	}
}
