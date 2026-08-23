package migrations

import "embed"

// Files contains the ordered SQLite migrations shipped in the TAdmin binary.
//
//go:embed *.sql
var Files embed.FS
