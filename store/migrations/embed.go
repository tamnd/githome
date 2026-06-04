// Package migrations holds Githome's embedded SQL schema migrations and exposes
// them as a read-only filesystem for the store's migration runner.
//
// File naming is "<version>_<name>.<dialect>.<direction>.sql", for example
// "0001_init.pg.up.sql" or "0001_init.sqlite.down.sql". The dialect token is
// "pg" or "sqlite"; the direction is "up" or "down".
package migrations

import "embed"

// FS is the read-only filesystem of embedded migration SQL files.
//
//go:embed *.sql
var FS embed.FS
