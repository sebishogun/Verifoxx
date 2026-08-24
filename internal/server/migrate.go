// Package server wires the product service to its transport and persistence
// adapters.
package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sebishogun/verifoxx/internal/adapters/postgres"
	"github.com/sebishogun/verifoxx/internal/config"
	"github.com/sebishogun/verifoxx/migrations"
)

var ErrInvalidRuntime = errors.New("server: invalid runtime configuration")

const migrationTimeout = 5 * time.Minute

// Migrate applies all embedded migrations with the configured database role.
func Migrate(ctx context.Context, cfg config.Config) error {
	if ctx == nil || cfg.DatabaseURL.Empty() {
		return ErrInvalidRuntime
	}
	operationContext, cancelOperation := context.WithTimeout(ctx, migrationTimeout)
	defer cancelOperation()
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL.Reveal())
	if err != nil {
		return fmt.Errorf("server: parse migration database URL: %w", err)
	}
	poolConfig.MinConns = 0
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(operationContext, poolConfig)
	if err != nil {
		return fmt.Errorf("server: open migration database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(operationContext); err != nil {
		return fmt.Errorf("server: ping migration database: %w", err)
	}
	migrator, err := postgres.NewMigrator(pool, migrations.Files)
	if err != nil {
		return fmt.Errorf("server: prepare migrations: %w", err)
	}
	if _, err := migrator.Up(operationContext); err != nil {
		return fmt.Errorf("server: migrate database: %w", err)
	}
	return nil
}

// MigrationHealth verifies that the embedded migration set is fully recorded.
func MigrationHealth(ctx context.Context, cfg config.Config) error {
	if ctx == nil || cfg.DatabaseURL.Empty() {
		return ErrInvalidRuntime
	}
	expected, err := fs.Glob(migrations.Files, "*.up.sql")
	if err != nil {
		return fmt.Errorf("server: enumerate embedded migrations: %w", err)
	}
	if len(expected) == 0 {
		return errors.New("server: no embedded migrations")
	}
	probeContext, cancelProbe := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	defer cancelProbe()
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL.Reveal())
	if err != nil {
		return fmt.Errorf("server: parse migration database URL: %w", err)
	}
	poolConfig.MinConns = 0
	poolConfig.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(probeContext, poolConfig)
	if err != nil {
		return fmt.Errorf("server: open migration health database: %w", err)
	}
	defer pool.Close()
	var applied int
	if err := pool.QueryRow(probeContext, "SELECT count(*) FROM public.verifoxx_schema_migrations").Scan(&applied); err != nil {
		return fmt.Errorf("server: read migration health: %w", err)
	}
	if applied != len(expected) {
		return fmt.Errorf("server: migration health has %d of %d versions", applied, len(expected))
	}
	return nil
}
