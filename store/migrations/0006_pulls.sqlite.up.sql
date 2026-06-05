-- 0006_pulls (sqlite, up): the pull request extension table. A pull request is
-- an issue row (issues.is_pull = 1) that shares the per-repo number sequence,
-- plus the row here that carries the git coordinates and the merge state the
-- issue table has no place for. issue_pk is unique so the two tables map one to
-- one. mergeable is nullable on purpose: it is NULL until the
-- recompute_mergeability worker computes it, which is the null-then-value
-- contract the API and its acceptance gate poll against.

CREATE TABLE pull_requests (
    pk                      INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id                   INTEGER NOT NULL UNIQUE,
    issue_pk                INTEGER NOT NULL UNIQUE REFERENCES issues(pk) ON DELETE CASCADE,
    repo_pk                 INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    base_ref                TEXT    NOT NULL,
    base_sha                TEXT    NOT NULL,
    head_ref                TEXT    NOT NULL,
    head_sha                TEXT    NOT NULL,
    head_repo_pk            INTEGER REFERENCES repositories(pk) ON DELETE SET NULL,
    draft                   INTEGER NOT NULL DEFAULT 0,
    maintainer_can_modify   INTEGER NOT NULL DEFAULT 0,
    merged                  INTEGER NOT NULL DEFAULT 0,
    merged_at               TEXT,
    merged_by_pk            INTEGER REFERENCES users(pk),
    merge_commit_sha        TEXT,
    mergeable               INTEGER,
    mergeable_state         TEXT    NOT NULL DEFAULT 'unknown',
    rebaseable              INTEGER,
    additions               INTEGER NOT NULL DEFAULT 0,
    deletions               INTEGER NOT NULL DEFAULT 0,
    changed_files           INTEGER NOT NULL DEFAULT 0,
    commits_count           INTEGER NOT NULL DEFAULT 0,
    mergeability_checked_at TEXT,
    created_at              TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at              TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX pull_requests_repo_idx ON pull_requests (repo_pk);
CREATE INDEX pull_requests_open_head_idx ON pull_requests (repo_pk, head_ref) WHERE merged = 0;
