-- 0017_oauth_app_callback (postgres, up): the registered authorization
-- callback URL for an OAuth app. The web-flow token exchange validates
-- redirect_uri by prefix against this, like GitHub does.

ALTER TABLE oauth_apps ADD COLUMN IF NOT EXISTS callback_url TEXT NOT NULL DEFAULT '';
