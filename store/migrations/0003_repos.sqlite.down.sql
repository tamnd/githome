-- 0003_repos (sqlite, down): drop the columns M2 added, newest first.

ALTER TABLE repositories DROP COLUMN is_template;
ALTER TABLE repositories DROP COLUMN disabled;
ALTER TABLE repositories DROP COLUMN archived;
ALTER TABLE repositories DROP COLUMN has_downloads;
ALTER TABLE repositories DROP COLUMN has_wiki;
ALTER TABLE repositories DROP COLUMN has_projects;
ALTER TABLE repositories DROP COLUMN has_issues;
ALTER TABLE repositories DROP COLUMN homepage;
