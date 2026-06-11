-- 0022_repo_redirects (pg, up): the rename redirect table. Same shape as the
-- sqlite pair; see that file for the why.
CREATE TABLE repo_redirects (
    pk         BIGSERIAL   PRIMARY KEY,
    old_owner  TEXT        NOT NULL,
    old_name   TEXT        NOT NULL,
    repo_pk    BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX repo_redirects_old_uq ON repo_redirects (old_owner, old_name);
