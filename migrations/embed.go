// Package migrations embeds the ordered PostgreSQL migration source used by
// the standalone migrator and container environment.
package migrations

import "embed"

// Files contains every paired migration in this directory.
//
//go:embed *.sql
var Files embed.FS
