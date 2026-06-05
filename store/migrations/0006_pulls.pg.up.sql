-- 0006_pulls (postgres, up): the pull request extension table. A pull request is
-- an issue row (issues.is_pull = TRUE) that shares the per-repo number sequence,
-- plus the row here that carries the git coordinates and the merge state the
-- issue table has no place for. issue_pk is UNIQUE so the two tables map one to
-- one. mergeable is nullable on purpose: it is NULL until the
-- recompute_mergeability worker computes it, which is the null-then-value
-- contract the API and its acceptance gate poll against.

CREATE TABLE pull_requests (
    pk                      BIGSERIAL   PRIMARY KEY,
    db_id                   BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    issue_pk                BIGINT      NOT NULL UNIQUE REFERENCES issues(pk) ON DELETE CASCADE,
    repo_pk                 BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    base_ref                TEXT        NOT NULL,
    base_sha                TEXT        NOT NULL,
    head_ref                TEXT        NOT NULL,
    head_sha                TEXT        NOT NULL,
    head_repo_pk            BIGINT      REFERENCES repositories(pk) ON DELETE SET NULL,
    draft                   BOOLEAN     NOT NULL DEFAULT FALSE,
    maintainer_can_modify   BOOLEAN     NOT NULL DEFAULT FALSE,
    merged                  BOOLEAN     NOT NULL DEFAULT FALSE,
    merged_at               TIMESTAMPTZ,
    merged_by_pk            BIGINT      REFERENCES users(pk),
    merge_commit_sha        TEXT,
    mergeable               BOOLEAN,
    mergeable_state         TEXT        NOT NULL DEFAULT 'unknown',
    rebaseable              BOOLEAN,
    additions               INTEGER     NOT NULL DEFAULT 0,
    deletions               INTEGER     NOT NULL DEFAULT 0,
    changed_files           INTEGER     NOT NULL DEFAULT 0,
    commits_count           INTEGER     NOT NULL DEFAULT 0,
    mergeability_checked_at TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_requests_repo_idx ON pull_requests (repo_pk);
CREATE INDEX pull_requests_open_head_idx ON pull_requests (repo_pk, head_ref) WHERE merged = FALSE;
