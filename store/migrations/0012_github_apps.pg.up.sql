-- 0012_github_apps (postgres, up): GitHub App registration, installations, and
-- the installation_repositories pivot. Also extends the tokens table with
-- installation linkage so installation tokens can be revoked by app.

CREATE TABLE github_apps (
    pk                  BIGSERIAL PRIMARY KEY,
    db_id               BIGINT    NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    owner_pk            BIGINT    NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    slug                TEXT      NOT NULL UNIQUE,
    name                TEXT      NOT NULL,
    client_id           TEXT      NOT NULL UNIQUE,
    client_secret_hash  BYTEA,
    webhook_secret_hash BYTEA,
    private_key_pem     BYTEA     NOT NULL,
    permissions         JSONB     NOT NULL DEFAULT '{}',
    events              TEXT[]    NOT NULL DEFAULT '{}',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE installations (
    pk                   BIGSERIAL PRIMARY KEY,
    db_id                BIGINT    NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    app_pk               BIGINT    NOT NULL REFERENCES github_apps(pk) ON DELETE CASCADE,
    account_pk           BIGINT    NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repository_selection TEXT      NOT NULL DEFAULT 'all',
    permissions          JSONB     NOT NULL DEFAULT '{}',
    events               TEXT[]    NOT NULL DEFAULT '{}',
    suspended_at         TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX installations_app_account_uq ON installations (app_pk, account_pk);

CREATE TABLE installation_repositories (
    installation_pk BIGINT NOT NULL REFERENCES installations(pk) ON DELETE CASCADE,
    repo_pk         BIGINT NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    PRIMARY KEY (installation_pk, repo_pk)
);

ALTER TABLE tokens ADD COLUMN installation_pk BIGINT REFERENCES installations(pk) ON DELETE CASCADE;
ALTER TABLE tokens ADD COLUMN github_app_pk   BIGINT REFERENCES github_apps(pk) ON DELETE CASCADE;
ALTER TABLE tokens ADD COLUMN grant_json      TEXT;
