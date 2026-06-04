-- 0002_auth (postgres, down): drop the credential tables and the profile columns
-- in reverse dependency order.

DROP TABLE IF EXISTS oauth_device_codes;
DROP TABLE IF EXISTS tokens;
DROP TABLE IF EXISTS oauth_apps;

ALTER TABLE users
    DROP COLUMN IF EXISTS following,
    DROP COLUMN IF EXISTS followers,
    DROP COLUMN IF EXISTS public_gists,
    DROP COLUMN IF EXISTS public_repos,
    DROP COLUMN IF EXISTS twitter_username,
    DROP COLUMN IF EXISTS hireable,
    DROP COLUMN IF EXISTS bio,
    DROP COLUMN IF EXISTS location,
    DROP COLUMN IF EXISTS blog,
    DROP COLUMN IF EXISTS company;
