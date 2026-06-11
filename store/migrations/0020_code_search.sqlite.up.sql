-- 0020_code_search (sqlite, up): FTS5 index over repository file contents so
-- code search queries an index instead of walking the head tree and grepping
-- every blob per request. Unlike issues_fts this is not an external-content
-- table: the documents come from git, not from another SQL table, so the index
-- stores the text itself and the indexer replaces a repository's rows wholesale.
-- repo_pk scopes queries and sha addresses the blob; neither needs a posting
-- list, so both are UNINDEXED payload columns.

CREATE VIRTUAL TABLE code_fts USING fts5(
    path, content,
    repo_pk UNINDEXED, sha UNINDEXED
);

-- code_index_state records which head commit a repository's documents were
-- built from, so the indexer skips repositories that have not moved and search
-- can detect a stale index. truncated mirrors the walk hitting the file or
-- size ceiling, surfacing as incomplete_results in the search envelope.
CREATE TABLE code_index_state (
    repo_pk    INTEGER PRIMARY KEY REFERENCES repositories(pk) ON DELETE CASCADE,
    head_sha   TEXT    NOT NULL,
    truncated  INTEGER NOT NULL DEFAULT 0,
    indexed_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
