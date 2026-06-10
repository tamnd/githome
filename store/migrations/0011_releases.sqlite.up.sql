-- 0011_releases (sqlite, up): releases and release assets. Mirrors GitHub's
-- releases API: a release ties a git tag to a set of downloadable assets plus
-- optional release notes. Assets are stored as rows; the binary content lives
-- on disk under DataDir/assets/{asset_pk}.

ALTER TABLE repositories ADD COLUMN next_release_number INTEGER NOT NULL DEFAULT 1;

CREATE TABLE releases (
    pk               INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id            INTEGER NOT NULL UNIQUE,
    repo_pk          INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    tag_name         TEXT    NOT NULL,
    target_commitish TEXT    NOT NULL DEFAULT 'main',
    name             TEXT,
    body             TEXT,
    draft            INTEGER NOT NULL DEFAULT 0,
    prerelease       INTEGER NOT NULL DEFAULT 0,
    author_pk        INTEGER REFERENCES users(pk),
    lock_version     INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at     TEXT,
    updated_at       TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at       TEXT
);
CREATE UNIQUE INDEX releases_repo_tag_uq ON releases (repo_pk, tag_name) WHERE deleted_at IS NULL;
CREATE INDEX releases_repo_pub_idx ON releases (repo_pk, published_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE release_assets (
    pk             INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id          INTEGER NOT NULL UNIQUE,
    release_pk     INTEGER NOT NULL REFERENCES releases(pk) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    label          TEXT,
    content_type   TEXT    NOT NULL DEFAULT 'application/octet-stream',
    size           INTEGER NOT NULL DEFAULT 0,
    download_count INTEGER NOT NULL DEFAULT 0,
    uploader_pk    INTEGER REFERENCES users(pk),
    state          TEXT    NOT NULL DEFAULT 'open',
    lock_version   INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at     TEXT
);
CREATE UNIQUE INDEX release_assets_release_name_uq ON release_assets (release_pk, name) WHERE deleted_at IS NULL;
