-- 0002_auth (postgres, up): the credential tables M1 needs and the user profile
-- columns the full User wire model renders. Scopes are stored as a single
-- comma-space TEXT column (the X-OAuth-Scopes header form) rather than TEXT[] so
-- one query path serves both dialects.

ALTER TABLE users
    ADD COLUMN company          TEXT,
    ADD COLUMN blog             TEXT    NOT NULL DEFAULT '',
    ADD COLUMN location         TEXT,
    ADD COLUMN bio              TEXT,
    ADD COLUMN hireable         BOOLEAN,
    ADD COLUMN twitter_username TEXT,
    ADD COLUMN public_repos     INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN public_gists     INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN followers        INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN following        INTEGER NOT NULL DEFAULT 0;

CREATE TABLE oauth_apps (
    pk                  BIGSERIAL PRIMARY KEY,
    client_id           TEXT        NOT NULL UNIQUE,
    client_secret_hash  BYTEA,
    name                TEXT        NOT NULL,
    owner_pk            BIGINT      REFERENCES users(pk) ON DELETE CASCADE,
    device_flow_enabled BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tokens (
    pk           BIGSERIAL   PRIMARY KEY,
    user_pk      BIGINT      REFERENCES users(pk) ON DELETE CASCADE,
    oauth_app_pk BIGINT      REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    token_hash   BYTEA       NOT NULL UNIQUE,
    token_prefix TEXT        NOT NULL,
    last_eight   TEXT        NOT NULL,
    kind         TEXT        NOT NULL,          -- pat | oauth
    scopes       TEXT        NOT NULL DEFAULT '',
    note         TEXT        NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE oauth_device_codes (
    pk               BIGSERIAL   PRIMARY KEY,
    device_code_hash BYTEA       NOT NULL UNIQUE,
    user_code        TEXT        NOT NULL UNIQUE,
    oauth_app_pk     BIGINT      REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    scopes           TEXT        NOT NULL DEFAULT '',
    state            TEXT        NOT NULL DEFAULT 'pending', -- pending | approved | denied
    user_pk          BIGINT      REFERENCES users(pk) ON DELETE SET NULL,
    interval_sec     INTEGER     NOT NULL DEFAULT 5,
    last_polled_at   TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
