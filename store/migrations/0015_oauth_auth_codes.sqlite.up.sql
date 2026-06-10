-- 0015_oauth_auth_codes (sqlite, up): authorization-code grant support for the
-- OAuth web flow (RFC 6749 §4.1). Each row is a single-use code valid for 10
-- minutes. The code itself is stored as a SHA-256 hash so a DB dump does not
-- leak live codes.

CREATE TABLE oauth_auth_codes (
    pk           INTEGER PRIMARY KEY AUTOINCREMENT,
    code_hash    BLOB    NOT NULL UNIQUE,
    oauth_app_pk INTEGER NOT NULL REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    user_pk      INTEGER NOT NULL REFERENCES users(pk)      ON DELETE CASCADE,
    redirect_uri TEXT    NOT NULL,
    scopes       TEXT    NOT NULL DEFAULT '',
    used         INTEGER NOT NULL DEFAULT 0,
    expires_at   TEXT    NOT NULL,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
