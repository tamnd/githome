-- 0012_github_apps (sqlite, down)
-- Drop the added FK columns from tokens first so the referenced tables can be dropped.
ALTER TABLE tokens DROP COLUMN grant_json;
ALTER TABLE tokens DROP COLUMN github_app_pk;
ALTER TABLE tokens DROP COLUMN installation_pk;
DROP TABLE IF EXISTS installation_repositories;
DROP TABLE IF EXISTS installations;
DROP TABLE IF EXISTS github_apps;
