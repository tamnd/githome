-- 0003_repos (sqlite, up): mirrors the postgres migration with SQLite-native
-- types. Booleans are INTEGER (0/1); SQLite supports ALTER TABLE ADD COLUMN
-- with a constant DEFAULT, which backfills existing rows.

ALTER TABLE repositories ADD COLUMN homepage      TEXT;
ALTER TABLE repositories ADD COLUMN has_issues    INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN has_projects  INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN has_wiki      INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN has_downloads INTEGER NOT NULL DEFAULT 1;
ALTER TABLE repositories ADD COLUMN archived      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN disabled      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE repositories ADD COLUMN is_template   INTEGER NOT NULL DEFAULT 0;
