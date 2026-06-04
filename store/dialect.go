package store

// Dialect is the database backend in use. It is resolved once at Open from the
// DSN scheme and never changes for the life of a Store.
type Dialect int

const (
	// DialectPostgres is PostgreSQL via the pgx stdlib driver.
	DialectPostgres Dialect = iota
	// DialectSQLite is SQLite via the pure-Go modernc.org/sqlite driver.
	DialectSQLite
)

func (d Dialect) String() string {
	switch d {
	case DialectPostgres:
		return "postgres"
	case DialectSQLite:
		return "sqlite"
	default:
		return "unknown"
	}
}
