-- 0012_github_apps (sqlite, up): GitHub App registration, installations, and
-- the installation_repositories pivot. Also extends the tokens table with
-- installation linkage so installation tokens can be revoked by app.

CREATE TABLE github_apps (
    pk                  INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id               INTEGER NOT NULL UNIQUE,
    owner_pk            INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    slug                TEXT    NOT NULL UNIQUE,
    name                TEXT    NOT NULL,
    client_id           TEXT    NOT NULL UNIQUE,
    client_secret_hash  BLOB,
    webhook_secret_hash BLOB,
    private_key_pem     BLOB    NOT NULL,
    permissions         TEXT    NOT NULL DEFAULT '{}',
    events              TEXT    NOT NULL DEFAULT '[]',
    created_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE installations (
    pk                   INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id                INTEGER NOT NULL UNIQUE,
    app_pk               INTEGER NOT NULL REFERENCES github_apps(pk) ON DELETE CASCADE,
    account_pk           INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repository_selection TEXT    NOT NULL DEFAULT 'all',
    permissions          TEXT    NOT NULL DEFAULT '{}',
    events               TEXT    NOT NULL DEFAULT '[]',
    suspended_at         TEXT,
    created_at           TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX installations_app_account_uq ON installations (app_pk, account_pk);

CREATE TABLE installation_repositories (
    installation_pk INTEGER NOT NULL REFERENCES installations(pk) ON DELETE CASCADE,
    repo_pk         INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    PRIMARY KEY (installation_pk, repo_pk)
);

ALTER TABLE tokens ADD COLUMN installation_pk  INTEGER REFERENCES installations(pk) ON DELETE CASCADE;
ALTER TABLE tokens ADD COLUMN github_app_pk    INTEGER REFERENCES github_apps(pk) ON DELETE CASCADE;
ALTER TABLE tokens ADD COLUMN grant_json       TEXT;
