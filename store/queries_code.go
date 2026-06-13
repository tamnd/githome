package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Code search reads the index the migrations name code_fts (SQLite FTS5) or
// code_documents (Postgres tsvector + GIN). The domain indexer extracts the
// documents from git and replaces a repository's rows wholesale through
// ReplaceCodeDocs; the search path filters by repository scope and FTS terms.

// CodeDoc is one indexed file: its repository-relative path, the blob sha the
// content was read from, and the content itself. Content is empty for binary
// or oversized files, which are indexed by path only so an empty-term scoped
// query still lists them.
type CodeDoc struct {
	Path    string
	SHA     string
	Content string
}

// CodeHit is one code search match, addressed by repository and path.
type CodeHit struct {
	RepoPK int64
	Path   string
	SHA    string
}

// CodeSearch is a resolved code query: the repositories in scope (already
// visibility-checked by the domain) and the free-text terms. Empty Terms lists
// every indexed file in scope, the way an empty code query lists the tree.
type CodeSearch struct {
	RepoPKs []int64
	Terms   []string
	Limit   int
	Offset  int
}

// codeTermClause returns the dialect's FTS predicate for the query terms, or
// "" when there are none.
func (s *Store) codeTermClause(q CodeSearch) (string, []any) {
	if len(q.Terms) == 0 {
		return "", nil
	}
	switch s.dialect {
	case DialectPostgres:
		return ` AND search_vector @@ plainto_tsquery('simple', ?)`,
			[]any{strings.Join(q.Terms, " ")}
	default:
		return ` AND code_fts MATCH ?`, []any{buildFTSMatch(q.Terms)}
	}
}

// codeTable names the dialect's document table.
func (s *Store) codeTable() string {
	if s.dialect == DialectPostgres {
		return "code_documents"
	}
	return "code_fts"
}

// codePathOrder returns the ORDER BY expression for path. SQLite sorts text
// bytewise (BINARY); Postgres sorts by the cluster's collation, which orders
// mixed-case paths differently. Pinning the Postgres sort to the "C" collation
// makes both dialects page in the same byte order.
func (s *Store) codePathOrder() string {
	if s.dialect == DialectPostgres {
		return `path COLLATE "C"`
	}
	return "path"
}

// SearchCode returns the page of indexed files matching the query, ordered by
// repository then path so pagination is deterministic.
func (s *Store) SearchCode(ctx context.Context, q CodeSearch) ([]CodeHit, error) {
	if len(q.RepoPKs) == 0 {
		return nil, nil
	}
	where, args := codeScopeClause(q.RepoPKs)
	termSQL, termArgs := s.codeTermClause(q)
	where += termSQL
	args = append(args, termArgs...)
	limit := q.Limit
	if limit <= 0 {
		limit = 30
	}
	args = append(args, limit, q.Offset)
	sql := s.rebind(`SELECT repo_pk, path, sha FROM ` + s.codeTable() + where +
		` ORDER BY repo_pk, ` + s.codePathOrder() + ` LIMIT ? OFFSET ?`)
	rows, err := s.rdb.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CodeHit
	for rows.Next() {
		var h CodeHit
		if err := rows.Scan(&h.RepoPK, &h.Path, &h.SHA); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// CountSearchCode counts every indexed file matching the query, ignoring its
// page window, for the search envelope's total_count.
func (s *Store) CountSearchCode(ctx context.Context, q CodeSearch) (int, error) {
	if len(q.RepoPKs) == 0 {
		return 0, nil
	}
	where, args := codeScopeClause(q.RepoPKs)
	termSQL, termArgs := s.codeTermClause(q)
	where += termSQL
	args = append(args, termArgs...)
	sql := s.rebind(`SELECT COUNT(*) FROM ` + s.codeTable() + where)
	var n int
	if err := s.rdb.QueryRowContext(ctx, sql, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// codeScopeClause builds the WHERE prefix that scopes a code query to its
// repositories. Callers guarantee pks is non-empty.
func codeScopeClause(pks []int64) (string, []any) {
	frag, args := inClause("repo_pk", pks)
	// inClause emits " AND col IN (...)"; lead with a tautology so it composes.
	return ` WHERE 1=1` + frag, args
}

// CodeIndexHead returns the head commit sha the repository's code documents
// were last built from, or ErrNotFound when the repository was never indexed.
func (s *Store) CodeIndexHead(ctx context.Context, repoPK int64) (string, error) {
	var sha string
	err := s.rdb.QueryRowContext(ctx,
		s.rebind(`SELECT head_sha FROM code_index_state WHERE repo_pk = ?`), repoPK).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return sha, nil
}

// CodeIndexTruncated reports whether any of the repositories' indexes stopped
// at the file ceiling before covering the whole tree, which search surfaces as
// incomplete_results.
func (s *Store) CodeIndexTruncated(ctx context.Context, repoPKs []int64) (bool, error) {
	if len(repoPKs) == 0 {
		return false, nil
	}
	frag, args := inClause("repo_pk", repoPKs)
	args = append([]any{true}, args...)
	var n int
	err := s.rdb.QueryRowContext(ctx,
		s.rebind(`SELECT COUNT(*) FROM code_index_state WHERE truncated = ?`+frag), args...).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ReplaceCodeDocs atomically replaces the repository's indexed documents and
// records the head sha they were built from. The whole swap is one transaction
// so a concurrent search never sees a half-replaced index.
func (s *Store) ReplaceCodeDocs(ctx context.Context, repoPK int64, headSHA string, truncated bool, docs []CodeDoc) error {
	return s.WithTx(ctx, func(tx *Tx) error {
		del := `DELETE FROM ` + s.codeTable() + ` WHERE repo_pk = ?`
		if _, err := tx.tx.ExecContext(ctx, s.rebind(del), repoPK); err != nil {
			return err
		}
		if len(docs) > 0 {
			ins := `INSERT INTO ` + s.codeTable() + ` (repo_pk, path, sha, content) VALUES (?, ?, ?, ?)`
			stmt, err := tx.tx.PrepareContext(ctx, s.rebind(ins))
			if err != nil {
				return err
			}
			defer func() { _ = stmt.Close() }()
			for _, d := range docs {
				if _, err := stmt.ExecContext(ctx, repoPK, d.Path, d.SHA, d.Content); err != nil {
					return err
				}
			}
		}
		state := `INSERT INTO code_index_state (repo_pk, head_sha, truncated, indexed_at)
			VALUES (?, ?, ?, CURRENT_TIMESTAMP)
			ON CONFLICT (repo_pk) DO UPDATE SET
				head_sha = excluded.head_sha,
				truncated = excluded.truncated,
				indexed_at = CURRENT_TIMESTAMP`
		_, err := tx.tx.ExecContext(ctx, s.rebind(state), repoPK, headSHA, truncated)
		return err
	})
}
