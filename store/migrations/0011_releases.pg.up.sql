-- 0011_releases (postgres, up): releases and release assets.

ALTER TABLE repositories ADD COLUMN next_release_number BIGINT NOT NULL DEFAULT 1;

CREATE TABLE releases (
    pk               BIGSERIAL   PRIMARY KEY,
    db_id            BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk          BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    tag_name         TEXT        NOT NULL,
    target_commitish TEXT        NOT NULL DEFAULT 'main',
    name             TEXT,
    body             TEXT,
    draft            BOOLEAN     NOT NULL DEFAULT FALSE,
    prerelease       BOOLEAN     NOT NULL DEFAULT FALSE,
    author_pk        BIGINT      REFERENCES users(pk),
    lock_version     BIGINT      NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at     TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX releases_repo_tag_uq ON releases (repo_pk, tag_name) WHERE deleted_at IS NULL;
CREATE INDEX releases_repo_pub_idx ON releases (repo_pk, published_at DESC NULLS LAST) WHERE deleted_at IS NULL;

CREATE TABLE release_assets (
    pk             BIGSERIAL   PRIMARY KEY,
    db_id          BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    release_pk     BIGINT      NOT NULL REFERENCES releases(pk) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    label          TEXT,
    content_type   TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size           BIGINT      NOT NULL DEFAULT 0,
    download_count BIGINT      NOT NULL DEFAULT 0,
    uploader_pk    BIGINT      REFERENCES users(pk),
    state          TEXT        NOT NULL DEFAULT 'open',
    lock_version   BIGINT      NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX release_assets_release_name_uq ON release_assets (release_pk, name) WHERE deleted_at IS NULL;
