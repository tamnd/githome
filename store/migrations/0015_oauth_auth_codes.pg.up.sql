-- 0015_oauth_auth_codes (postgres, up): authorization-code grant support for
-- the OAuth web flow (RFC 6749 §4.1).

CREATE TABLE IF NOT EXISTS oauth_auth_codes (
    pk           BIGSERIAL   PRIMARY KEY,
    code_hash    BYTEA       NOT NULL UNIQUE,
    oauth_app_pk BIGINT      NOT NULL REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    user_pk      BIGINT      NOT NULL REFERENCES users(pk)      ON DELETE CASCADE,
    redirect_uri TEXT        NOT NULL,
    scopes       TEXT        NOT NULL DEFAULT '',
    used         BOOLEAN     NOT NULL DEFAULT false,
    expires_at   TIMESTAMPTZ NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
