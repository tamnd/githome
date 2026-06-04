-- 0001_init (sqlite, up): SQLite has no sequences, so a single-row high-water
-- table hands out the global db_id; otherwise the schema mirrors the postgres
-- migration with SQLite-native types.

CREATE TABLE id_allocator (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    high_water INTEGER NOT NULL DEFAULT 0
);
INSERT INTO id_allocator (id, high_water) VALUES (1, 0);

CREATE TABLE users (
    pk            INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id         INTEGER NOT NULL UNIQUE,
    login         TEXT    NOT NULL,
    type          TEXT    NOT NULL DEFAULT 'User',
    name          TEXT,
    email         TEXT,
    site_admin    INTEGER NOT NULL DEFAULT 0,
    password_hash TEXT,
    created_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at    TEXT
);
CREATE UNIQUE INDEX users_login_lower_uq ON users (lower(login)) WHERE deleted_at IS NULL;

CREATE TABLE repositories (
    pk                INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id             INTEGER NOT NULL UNIQUE,
    owner_pk          INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    name              TEXT    NOT NULL,
    description       TEXT,
    private           INTEGER NOT NULL DEFAULT 0,
    fork              INTEGER NOT NULL DEFAULT 0,
    default_branch    TEXT    NOT NULL DEFAULT 'main',
    next_issue_number INTEGER NOT NULL DEFAULT 1,
    open_issues_count INTEGER NOT NULL DEFAULT 0,
    pushed_at         TEXT,
    lock_version      INTEGER NOT NULL DEFAULT 0,
    created_at        TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at        TEXT
);
CREATE UNIQUE INDEX repos_owner_name_uq ON repositories (owner_pk, lower(name)) WHERE deleted_at IS NULL;
