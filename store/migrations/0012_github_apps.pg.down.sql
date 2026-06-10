-- 0012_github_apps (postgres, down)
DROP TABLE IF EXISTS installation_repositories;
DROP TABLE IF EXISTS installations;
DROP TABLE IF EXISTS github_apps;
ALTER TABLE tokens DROP COLUMN IF EXISTS installation_pk;
ALTER TABLE tokens DROP COLUMN IF EXISTS github_app_pk;
ALTER TABLE tokens DROP COLUMN IF EXISTS grant_json;
