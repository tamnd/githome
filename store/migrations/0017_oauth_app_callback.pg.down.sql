-- 0017_oauth_app_callback (postgres, down)
ALTER TABLE oauth_apps DROP COLUMN IF EXISTS callback_url;
