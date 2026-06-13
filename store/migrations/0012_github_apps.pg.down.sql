-- 0012_github_apps (postgres, down): drop the token linkage columns first so the
-- foreign keys they carry stop pinning installations and github_apps, then drop
-- the tables in reverse dependency order.
ALTER TABLE tokens DROP COLUMN IF EXISTS installation_pk;
ALTER TABLE tokens DROP COLUMN IF EXISTS github_app_pk;
ALTER TABLE tokens DROP COLUMN IF EXISTS grant_json;
DROP TABLE IF EXISTS installation_repositories;
DROP TABLE IF EXISTS installations;
DROP TABLE IF EXISTS github_apps;
