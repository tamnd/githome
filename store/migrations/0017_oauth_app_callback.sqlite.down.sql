-- 0017_oauth_app_callback (sqlite, down)
ALTER TABLE oauth_apps DROP COLUMN callback_url;
