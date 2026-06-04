-- 0003_repos (postgres, up): the repository settings columns the full
-- Repository wire model renders. 0001 shipped the repository skeleton
-- (owner, name, description, private, fork, default_branch, the issue
-- counters, and pushed_at); M2 adds the homepage and the feature and state
-- flags GitHub persists per repository so a stored repo renders real values
-- rather than hardcoded constants. The owner/name lookup index already lives
-- in 0001 (repos_owner_name_uq), so this migration only widens the row.

ALTER TABLE repositories ADD COLUMN homepage      TEXT;
ALTER TABLE repositories ADD COLUMN has_issues    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN has_projects  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN has_wiki      BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN has_downloads BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN archived      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repositories ADD COLUMN disabled      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repositories ADD COLUMN is_template   BOOLEAN NOT NULL DEFAULT FALSE;
