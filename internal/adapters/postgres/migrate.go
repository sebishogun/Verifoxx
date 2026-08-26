package postgres

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxMigrationBytes       = 8 << 20
	migrationAdvisoryLock   = int64(0x56455249464f5858)
	migrationRollbackWindow = 5 * time.Second
)

const createMigrationLedgerSQL = `
CREATE TABLE IF NOT EXISTS public.nornrune_schema_migrations (
    version integer PRIMARY KEY CHECK (version > 0),
    name text NOT NULL CHECK (btrim(name) <> ''),
    checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON TABLE public.nornrune_schema_migrations FROM PUBLIC;
DO $nornrune$
BEGIN
    IF EXISTS (SELECT FROM pg_roles WHERE rolname = 'nornrune_runtime') THEN
        EXECUTE 'REVOKE ALL ON TABLE public.nornrune_schema_migrations FROM nornrune_runtime';
    END IF;
END;
$nornrune$;
`

var (
	ErrInvalidMigrationSource = errors.New("postgres: invalid migration source")
	ErrInvalidDownCount       = errors.New("postgres: down count must be positive")
	ErrMigrationDrift         = errors.New("postgres: applied migration differs from source")
)

type Migrator struct {
	pool       *pgxpool.Pool
	migrations []migration
}

type migration struct {
	name     string
	up       []byte
	down     []byte
	checksum [sha256.Size]byte
	version  uint32
}

type migrationPair struct {
	name string
	up   []byte
	down []byte
}

func NewMigrator(pool *pgxpool.Pool, source fs.FS) (*Migrator, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: nil pool", ErrInvalidMigrationSource)
	}
	if source == nil {
		return nil, fmt.Errorf("%w: nil filesystem", ErrInvalidMigrationSource)
	}

	migrations, err := discoverMigrations(source)
	if err != nil {
		return nil, err
	}
	return &Migrator{migrations: migrations, pool: pool}, nil
}

func (m *Migrator) Up(ctx context.Context) (int, error) {
	if err := m.valid(); err != nil {
		return 0, err
	}

	tx, err := m.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer rollbackMigration(tx, ctx)

	applied, err := m.reconcile(ctx, tx)
	if err != nil {
		return 0, err
	}
	for row := applied; row < len(m.migrations); row++ {
		item := &m.migrations[row]
		if _, err := tx.Exec(ctx, string(item.up), pgx.QueryExecModeSimpleProtocol); err != nil {
			return 0, fmt.Errorf("postgres: apply migration %06d_%s: %w", item.version, item.name, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO public.nornrune_schema_migrations (version, name, checksum)
			VALUES ($1, $2, $3)
		`, item.version, item.name, item.checksum[:]); err != nil {
			return 0, fmt.Errorf("postgres: record migration %06d_%s: %w", item.version, item.name, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit up migrations: %w", err)
	}
	return len(m.migrations) - applied, nil
}

func (m *Migrator) Down(ctx context.Context, steps uint32) (int, error) {
	if steps == 0 {
		return 0, ErrInvalidDownCount
	}
	if err := m.valid(); err != nil {
		return 0, err
	}

	tx, err := m.begin(ctx)
	if err != nil {
		return 0, err
	}
	defer rollbackMigration(tx, ctx)

	applied, err := m.reconcile(ctx, tx)
	if err != nil {
		return 0, err
	}
	if uint64(steps) > uint64(applied) {
		return 0, fmt.Errorf("%w: requested %d, applied %d", ErrInvalidDownCount, steps, applied)
	}

	stop := applied - int(steps)
	for row := applied - 1; row >= stop; row-- {
		item := &m.migrations[row]
		if _, err := tx.Exec(ctx, string(item.down), pgx.QueryExecModeSimpleProtocol); err != nil {
			return 0, fmt.Errorf("postgres: revert migration %06d_%s: %w", item.version, item.name, err)
		}
		tag, err := tx.Exec(ctx,
			"DELETE FROM public.nornrune_schema_migrations WHERE version = $1",
			item.version,
		)
		if err != nil {
			return 0, fmt.Errorf("postgres: remove migration %06d_%s: %w", item.version, item.name, err)
		}
		if tag.RowsAffected() != 1 {
			return 0, fmt.Errorf("%w: migration %06d_%s ledger row disappeared", ErrMigrationDrift, item.version, item.name)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("postgres: commit down migrations: %w", err)
	}
	return int(steps), nil
}

func (m *Migrator) valid() error {
	if m == nil || m.pool == nil {
		return fmt.Errorf("%w: nil migrator pool", ErrInvalidMigrationSource)
	}
	return nil
}

func (m *Migrator) begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("postgres: begin migration transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationAdvisoryLock); err != nil {
		rollbackMigration(tx, ctx)
		return nil, fmt.Errorf("postgres: acquire migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, createMigrationLedgerSQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		rollbackMigration(tx, ctx)
		return nil, fmt.Errorf("postgres: create migration ledger: %w", err)
	}
	return tx, nil
}

func (m *Migrator) reconcile(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `
		SELECT version, name, checksum
		FROM public.nornrune_schema_migrations
		ORDER BY version
	`)
	if err != nil {
		return 0, fmt.Errorf("postgres: read migration ledger: %w", err)
	}

	applied := 0
	for rows.Next() {
		var (
			checksum []byte
			name     string
			version  int64
		)
		if err := rows.Scan(&version, &name, &checksum); err != nil {
			rows.Close()
			return 0, fmt.Errorf("postgres: scan migration ledger: %w", err)
		}
		if applied >= len(m.migrations) {
			rows.Close()
			return 0, fmt.Errorf("%w: database contains unknown version %06d", ErrMigrationDrift, version)
		}

		item := &m.migrations[applied]
		if version != int64(item.version) || name != item.name ||
			len(checksum) != len(item.checksum) || subtle.ConstantTimeCompare(checksum, item.checksum[:]) != 1 {
			rows.Close()
			return 0, fmt.Errorf("%w: applied version %06d", ErrMigrationDrift, version)
		}
		applied++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("postgres: iterate migration ledger: %w", err)
	}
	rows.Close()
	return applied, nil
}

func rollbackMigration(tx pgx.Tx, parent context.Context) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), migrationRollbackWindow)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func discoverMigrations(source fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(source, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: read directory: %w", ErrInvalidMigrationSource, err)
	}

	pairs := make(map[uint32]*migrationPair, len(entries)/2)
	for _, entry := range entries {
		filename := entry.Name()
		version, name, direction, sqlFile, err := parseMigrationFilename(filename)
		if err != nil {
			return nil, err
		}
		if !sqlFile {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("%w: stat %q: %w", ErrInvalidMigrationSource, filename, err)
		}
		if !info.Mode().IsRegular() || info.Size() > maxMigrationBytes {
			return nil, fmt.Errorf("%w: invalid file %q", ErrInvalidMigrationSource, filename)
		}

		contents, err := fs.ReadFile(source, filename)
		if err != nil {
			return nil, fmt.Errorf("%w: read %q: %w", ErrInvalidMigrationSource, filename, err)
		}

		pair := pairs[version]
		if pair == nil {
			pair = &migrationPair{name: name}
			pairs[version] = pair
		} else if pair.name != name {
			return nil, fmt.Errorf("%w: version %06d has names %q and %q", ErrInvalidMigrationSource, version, pair.name, name)
		}

		switch direction {
		case migrationUp:
			if pair.up != nil {
				return nil, fmt.Errorf("%w: duplicate up migration %06d", ErrInvalidMigrationSource, version)
			}
			pair.up = contents
		case migrationDown:
			if pair.down != nil {
				return nil, fmt.Errorf("%w: duplicate down migration %06d", ErrInvalidMigrationSource, version)
			}
			pair.down = contents
		}
	}

	migrations := make([]migration, 0, len(pairs))
	for version, pair := range pairs {
		if pair.up == nil || pair.down == nil {
			return nil, fmt.Errorf("%w: migration %06d_%s is not paired", ErrInvalidMigrationSource, version, pair.name)
		}

		item := migration{
			up:      pair.up,
			down:    pair.down,
			name:    pair.name,
			version: version,
		}
		item.checksum = migrationChecksum(item.up, item.down)
		migrations = append(migrations, item)
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	for row := range migrations {
		want := uint32(row + 1)
		if migrations[row].version != want {
			return nil, fmt.Errorf("%w: version gap at %06d", ErrInvalidMigrationSource, want)
		}
	}
	return migrations, nil
}

const (
	migrationUp uint8 = iota + 1
	migrationDown
)

func parseMigrationFilename(filename string) (version uint32, name string, direction uint8, sqlFile bool, err error) {
	var base string
	switch {
	case strings.HasSuffix(filename, ".up.sql"):
		base = filename[:len(filename)-len(".up.sql")]
		direction = migrationUp
	case strings.HasSuffix(filename, ".down.sql"):
		base = filename[:len(filename)-len(".down.sql")]
		direction = migrationDown
	case strings.HasSuffix(filename, ".sql"):
		return 0, "", 0, true, fmt.Errorf("%w: malformed filename %q", ErrInvalidMigrationSource, filename)
	default:
		return 0, "", 0, false, nil
	}

	if len(base) < 8 || base[6] != '_' {
		return 0, "", 0, true, fmt.Errorf("%w: malformed filename %q", ErrInvalidMigrationSource, filename)
	}
	for index := 0; index < 6; index++ {
		digit := base[index]
		if digit < '0' || digit > '9' {
			return 0, "", 0, true, fmt.Errorf("%w: malformed filename %q", ErrInvalidMigrationSource, filename)
		}
		version = version*10 + uint32(digit-'0')
	}
	if version == 0 {
		return 0, "", 0, true, fmt.Errorf("%w: version zero in %q", ErrInvalidMigrationSource, filename)
	}

	name = base[7:]
	if !validMigrationName(name) {
		return 0, "", 0, true, fmt.Errorf("%w: invalid migration name in %q", ErrInvalidMigrationSource, filename)
	}
	return version, name, direction, true, nil
}

func validMigrationName(name string) bool {
	if len(name) == 0 || name[0] == '_' || name[len(name)-1] == '_' {
		return false
	}
	for index := range len(name) {
		char := name[index]
		if char != '_' && (char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func migrationChecksum(up, down []byte) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write(up)
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(down)

	var checksum [sha256.Size]byte
	hash.Sum(checksum[:0])
	return checksum
}
