-- 0017_oauth_app_callback (sqlite, up): the registered authorization callback
-- URL for an OAuth app. The web-flow token exchange validates redirect_uri by
-- prefix against this, like GitHub does. Empty means no registered callback,
-- which existing rows keep so their flows do not break.

ALTER TABLE oauth_apps ADD COLUMN callback_url TEXT NOT NULL DEFAULT '';
