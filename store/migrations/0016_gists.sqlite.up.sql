-- 0016_gists (sqlite, up): Gist API — gists, their files, stars, and comments.
-- Gist IDs are random 20-byte hex strings matching GitHub's format. Files are
-- stored as content text; the git transport backed by a bare repo is a later
-- addition. Stars are a junction table; comments are simple text rows.

CREATE TABLE gists (
    pk          INTEGER PRIMARY KEY AUTOINCREMENT,
    gist_id     TEXT    NOT NULL UNIQUE,
    owner_pk    INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    description TEXT    NOT NULL DEFAULT '',
    public      INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX gists_owner ON gists (owner_pk);

CREATE TABLE gist_files (
    pk        INTEGER PRIMARY KEY AUTOINCREMENT,
    gist_pk   INTEGER NOT NULL REFERENCES gists(pk) ON DELETE CASCADE,
    filename  TEXT    NOT NULL,
    content   TEXT    NOT NULL DEFAULT '',
    UNIQUE(gist_pk, filename)
);

CREATE TABLE gist_stars (
    gist_pk INTEGER NOT NULL REFERENCES gists(pk) ON DELETE CASCADE,
    user_pk INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    PRIMARY KEY(gist_pk, user_pk)
);

CREATE TABLE gist_comments (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    gist_pk    INTEGER NOT NULL REFERENCES gists(pk) ON DELETE CASCADE,
    user_pk    INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    body       TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
