package postgres

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDiscoverMigrationsOrdersAndChecksums(t *testing.T) {
	source := fstest.MapFS{
		"README.md":                   {Data: []byte("ignored")},
		"000002_second.down.sql":      {Data: []byte("drop table second;")},
		"000001_initial.up.sql":       {Data: []byte("select 1;")},
		"000002_second.up.sql":        {Data: []byte("create table second();")},
		"000001_initial.down.sql":     {Data: []byte("select 2;")},
		"notes_without_sql_extension": {Data: []byte("ignored")},
	}

	got, err := discoverMigrations(source)
	if err != nil {
		t.Fatalf("discover migrations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("migration count = %d, want 2", len(got))
	}
	if got[0].version != 1 || got[0].name != "initial" ||
		!slices.Equal(got[0].up, []byte("select 1;")) ||
		!slices.Equal(got[0].down, []byte("select 2;")) {
		t.Fatalf("first migration = %+v", got[0])
	}
	if got[1].version != 2 || got[1].name != "second" {
		t.Fatalf("second migration = %+v", got[1])
	}

	wantBytes, err := hex.DecodeString("13460d8b279152494f7f03afeb15fa1bbc014c36b318d0720483501ef1bd4342")
	if err != nil {
		t.Fatalf("decode checksum golden: %v", err)
	}
	if !slices.Equal(got[0].checksum[:], wantBytes) {
		t.Fatalf("checksum = %x, want %x", got[0].checksum, wantBytes)
	}
}

func TestDiscoverMigrationsRejectsInvalidSources(t *testing.T) {
	tests := []struct {
		name   string
		source fstest.MapFS
	}{
		{
			name: "zero version",
			source: migrationSource(
				"000000_zero.up.sql", "000000_zero.down.sql",
			),
		},
		{
			name: "short version",
			source: migrationSource(
				"00001_short.up.sql", "00001_short.down.sql",
			),
		},
		{
			name: "uppercase name",
			source: migrationSource(
				"000001_Initial.up.sql", "000001_Initial.down.sql",
			),
		},
		{
			name: "leading underscore",
			source: migrationSource(
				"000001__initial.up.sql", "000001__initial.down.sql",
			),
		},
		{
			name: "trailing underscore",
			source: migrationSource(
				"000001_initial_.up.sql", "000001_initial_.down.sql",
			),
		},
		{
			name: "hyphenated name",
			source: migrationSource(
				"000001_bad-name.up.sql", "000001_bad-name.down.sql",
			),
		},
		{
			name: "unknown direction",
			source: fstest.MapFS{
				"000001_initial.forward.sql": {Data: []byte("select 1;")},
			},
		},
		{
			name: "missing pair",
			source: fstest.MapFS{
				"000001_initial.up.sql": {Data: []byte("select 1;")},
			},
		},
		{
			name: "mismatched pair names",
			source: fstest.MapFS{
				"000001_first.up.sql":    {Data: []byte("select 1;")},
				"000001_second.down.sql": {Data: []byte("select 2;")},
			},
		},
		{
			name: "duplicate version",
			source: fstest.MapFS{
				"000001_first.up.sql":    {Data: []byte("select 1;")},
				"000001_first.down.sql":  {Data: []byte("select 2;")},
				"000001_second.up.sql":   {Data: []byte("select 3;")},
				"000001_second.down.sql": {Data: []byte("select 4;")},
			},
		},
		{
			name: "version gap",
			source: fstest.MapFS{
				"000001_first.up.sql":   {Data: []byte("select 1;")},
				"000001_first.down.sql": {Data: []byte("select 2;")},
				"000003_third.up.sql":   {Data: []byte("select 3;")},
				"000003_third.down.sql": {Data: []byte("select 4;")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := discoverMigrations(tt.source)
			if !errors.Is(err, ErrInvalidMigrationSource) {
				t.Fatalf("error = %v, want ErrInvalidMigrationSource", err)
			}
		})
	}
}

func TestDiscoverMigrationsRejectsOversizedFile(t *testing.T) {
	source := fstest.MapFS{
		"000001_large.up.sql":   {Data: make([]byte, maxMigrationBytes+1)},
		"000001_large.down.sql": {Data: []byte("select 1;")},
	}

	_, err := discoverMigrations(source)
	if !errors.Is(err, ErrInvalidMigrationSource) {
		t.Fatalf("error = %v, want ErrInvalidMigrationSource", err)
	}
}

func TestDiscoverMigrationsWrapsFilesystemErrors(t *testing.T) {
	cause := errors.New("filesystem unavailable")
	_, err := discoverMigrations(errorFS{err: cause})
	if !errors.Is(err, ErrInvalidMigrationSource) {
		t.Fatalf("error = %v, want ErrInvalidMigrationSource", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped filesystem cause", err)
	}
}

func TestNewMigratorRejectsNilDependencies(t *testing.T) {
	if _, err := NewMigrator(nil, fstest.MapFS{}); !errors.Is(err, ErrInvalidMigrationSource) {
		t.Fatalf("nil pool error = %v, want ErrInvalidMigrationSource", err)
	}

	var source fs.FS
	if _, err := NewMigrator(new(pgxpool.Pool), source); !errors.Is(err, ErrInvalidMigrationSource) {
		t.Fatalf("nil source error = %v, want ErrInvalidMigrationSource", err)
	}
}

func TestMigratorDownRejectsZeroStepsBeforePoolUse(t *testing.T) {
	var migrator Migrator
	if _, err := migrator.Down(context.Background(), 0); !errors.Is(err, ErrInvalidDownCount) {
		t.Fatalf("error = %v, want ErrInvalidDownCount", err)
	}
}

func migrationSource(names ...string) fstest.MapFS {
	source := make(fstest.MapFS, len(names))
	for _, name := range names {
		source[name] = &fstest.MapFile{Data: []byte("select 1;")}
	}
	return source
}

type errorFS struct {
	err error
}

func (source errorFS) Open(string) (fs.File, error) {
	return nil, source.err
}
