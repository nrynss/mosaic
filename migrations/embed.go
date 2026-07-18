// Package migrations embeds the ordered SQLite schema migrations used by the
// local Mosaic store. Migration files remain visible and reviewable SQL rather
// than being assembled at runtime.
package migrations

import "embed"

// Files contains the ordered migration files.
//
//go:embed *.sql
var Files embed.FS
