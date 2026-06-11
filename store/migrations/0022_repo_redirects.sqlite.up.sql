-- 0022_repo_redirects (sqlite, up): the rename redirect table. When a
-- repository is renamed the old owner/name pair is recorded here pointing at
-- the repository row itself, not at the new name, so a chain of renames
-- collapses to wherever the repo currently lives and a transfer would resolve
-- the same way. Keys are stored lowercased because the web lookup is
-- case-insensitive; one old name maps to at most one repository, and a later
-- rename through the same old name repoints it (upsert).
CREATE TABLE repo_redirects (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    old_owner  TEXT    NOT NULL,
    old_name   TEXT    NOT NULL,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX repo_redirects_old_uq ON repo_redirects (old_owner, old_name);
