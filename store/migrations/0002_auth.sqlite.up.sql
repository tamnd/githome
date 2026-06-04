-- 0002_auth (sqlite, up): mirrors the postgres migration with SQLite-native
-- types. SQLite ALTER TABLE adds one column per statement. Scopes are a single
-- comma-space TEXT column so one query path serves both dialects.

ALTER TABLE users ADD COLUMN company          TEXT;
ALTER TABLE users ADD COLUMN blog             TEXT    NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN location         TEXT;
ALTER TABLE users ADD COLUMN bio              TEXT;
ALTER TABLE users ADD COLUMN hireable         INTEGER;
ALTER TABLE users ADD COLUMN twitter_username TEXT;
ALTER TABLE users ADD COLUMN public_repos     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN public_gists     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN followers        INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN following        INTEGER NOT NULL DEFAULT 0;

CREATE TABLE oauth_apps (
    pk                  INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id           TEXT    NOT NULL UNIQUE,
    client_secret_hash  BLOB,
    name                TEXT    NOT NULL,
    owner_pk            INTEGER REFERENCES users(pk) ON DELETE CASCADE,
    device_flow_enabled INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE tokens (
    pk           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_pk      INTEGER REFERENCES users(pk) ON DELETE CASCADE,
    oauth_app_pk INTEGER REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    token_hash   BLOB    NOT NULL UNIQUE,
    token_prefix TEXT    NOT NULL,
    last_eight   TEXT    NOT NULL,
    kind         TEXT    NOT NULL,
    scopes       TEXT    NOT NULL DEFAULT '',
    note         TEXT    NOT NULL DEFAULT '',
    expires_at   TEXT,
    revoked_at   TEXT,
    last_used_at TEXT,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE oauth_device_codes (
    pk               INTEGER PRIMARY KEY AUTOINCREMENT,
    device_code_hash BLOB    NOT NULL UNIQUE,
    user_code        TEXT    NOT NULL UNIQUE,
    oauth_app_pk     INTEGER REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    scopes           TEXT    NOT NULL DEFAULT '',
    state            TEXT    NOT NULL DEFAULT 'pending',
    user_pk          INTEGER REFERENCES users(pk) ON DELETE SET NULL,
    interval_sec     INTEGER NOT NULL DEFAULT 5,
    last_polled_at   TEXT,
    expires_at       TEXT    NOT NULL,
    created_at       TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
