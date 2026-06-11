-- 0022_repo_merge_fork (sqlite, up): mirrors the postgres migration with
-- SQLite-native types. Booleans are INTEGER (0/1). The fork_of_pk foreign key
-- defaults to NULL, which is what SQLite's ALTER TABLE ADD COLUMN requires of
-- a referencing column.

ALTER TABLE repositories ADD COLUMN allow_squash_merge          INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN allow_merge_commit          INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN allow_rebase_merge          INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN allow_auto_merge            INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN delete_branch_on_merge      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN allow_update_branch         INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN web_commit_signoff_required INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN fork_of_pk                  INTEGER REFERENCES repositories(pk) ON DELETE SET NULL;

CREATE INDEX repos_fork_of_idx ON repositories (fork_of_pk) WHERE fork_of_pk IS NOT NULL;
