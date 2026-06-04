-- 0002_auth (sqlite, down): drop the credential tables and the profile columns in
-- reverse dependency order. SQLite DROP COLUMN (3.35+) is one column per statement.

DROP TABLE IF EXISTS oauth_device_codes;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS oauth_apps;

ALTER TABLE users DROP COLUMN following;
ALTER TABLE users DROP COLUMN followers;
ALTER TABLE users DROP COLUMN public_gists;
ALTER TABLE users DROP COLUMN public_repos;
ALTER TABLE users DROP COLUMN twitter_username;
ALTER TABLE users DROP COLUMN hireable;
ALTER TABLE users DROP COLUMN bio;
ALTER TABLE users DROP COLUMN location;
ALTER TABLE users DROP COLUMN blog;
ALTER TABLE users DROP COLUMN company;
