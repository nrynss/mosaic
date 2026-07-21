package pgstore

import "embed"

// migrationFiles contains the ordered PostgreSQL schema migrations for the
// pgstore backend. They live inside this package (rather than the top-level
// migrations package, which embeds the untouched SQLite DDL) so the two
// backends never share a migration set. Files remain reviewable SQL rather than
// being assembled at runtime.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS
