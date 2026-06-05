package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Tx is a transaction-scoped handle the multi-statement writes run through, so a
// mutation and the timeline and counter updates it implies all commit together
// or not at all. It exposes the same write methods the store does, bound to one
// *sql.Tx.
type Tx struct {
	tx      *sql.Tx
	dialect Dialect
}

// WithTx runs fn inside a single transaction, committing when fn returns nil and
// rolling back on any error or panic. The issue service uses it so allocating a
// number, inserting the issue, attaching labels and assignees, and writing the
// timeline rows are one atomic unit.
func (s *Store) WithTx(ctx context.Context, fn func(*Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = sqlTx.Rollback()
		}
	}()
	if err := fn(&Tx{tx: sqlTx, dialect: s.dialect}); err != nil {
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

// rebind rewrites `?` placeholders to the dialect's form, mirroring the store's
// own rebind so a query string works the same inside or outside a transaction.
func (t *Tx) rebind(query string) string { return rebindFor(t.dialect, query) }

// allocDBID hands out the next global id from within the transaction, matching
// Store.AllocDBID but on the transaction's connection so the id and the row that
// uses it share one atomic unit.
func (t *Tx) allocDBID(ctx context.Context) (int64, error) {
	var q string
	switch t.dialect {
	case DialectPostgres:
		q = `SELECT nextval('global_id_seq')`
	case DialectSQLite:
		q = `UPDATE id_allocator SET high_water = high_water + 1 WHERE id = 1 RETURNING high_water`
	default:
		return 0, errUnknownDialect
	}
	var id int64
	if err := t.tx.QueryRowContext(ctx, q).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// rebindFor rewrites `?` placeholders to $1.. on Postgres; SQLite keeps `?`.
func rebindFor(d Dialect, query string) string {
	if d != DialectPostgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}
