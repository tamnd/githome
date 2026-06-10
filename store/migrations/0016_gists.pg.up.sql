-- 0016_gists (postgres, up)

CREATE TABLE gists (
    pk          BIGSERIAL PRIMARY KEY,
    gist_id     TEXT        NOT NULL UNIQUE,
    owner_pk    BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    description TEXT        NOT NULL DEFAULT '',
    public      BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX gists_owner ON gists (owner_pk);

CREATE TABLE gist_files (
    pk        BIGSERIAL PRIMARY KEY,
    gist_pk   BIGINT NOT NULL REFERENCES gists(pk) ON DELETE CASCADE,
    filename  TEXT   NOT NULL,
    content   TEXT   NOT NULL DEFAULT '',
    UNIQUE(gist_pk, filename)
);

CREATE TABLE gist_stars (
    gist_pk BIGINT NOT NULL REFERENCES gists(pk) ON DELETE CASCADE,
    user_pk BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    PRIMARY KEY(gist_pk, user_pk)
);

CREATE TABLE gist_comments (
    pk         BIGSERIAL   PRIMARY KEY,
    gist_pk    BIGINT      NOT NULL REFERENCES gists(pk) ON DELETE CASCADE,
    user_pk    BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    body       TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
