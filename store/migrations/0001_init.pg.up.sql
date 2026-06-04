-- 0001_init (postgres, up): the global ID sequence and the user/repository
-- skeleton every later milestone hangs off. Issues, pulls, and the rest arrive
-- in their own milestones.

CREATE SEQUENCE IF NOT EXISTS global_id_seq START 1;

CREATE TABLE users (
    pk            BIGSERIAL PRIMARY KEY,
    db_id         BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    login         TEXT        NOT NULL,
    type          TEXT        NOT NULL DEFAULT 'User',
    name          TEXT,
    email         TEXT,
    site_admin    BOOLEAN     NOT NULL DEFAULT FALSE,
    password_hash TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX users_login_lower_uq ON users (lower(login)) WHERE deleted_at IS NULL;

CREATE TABLE repositories (
    pk                BIGSERIAL PRIMARY KEY,
    db_id             BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    owner_pk          BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    name              TEXT        NOT NULL,
    description       TEXT,
    private           BOOLEAN     NOT NULL DEFAULT FALSE,
    fork              BOOLEAN     NOT NULL DEFAULT FALSE,
    default_branch    TEXT        NOT NULL DEFAULT 'main',
    next_issue_number BIGINT      NOT NULL DEFAULT 1,
    open_issues_count INTEGER     NOT NULL DEFAULT 0,
    pushed_at         TIMESTAMPTZ,
    lock_version      BIGINT      NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX repos_owner_name_uq ON repositories (owner_pk, lower(name)) WHERE deleted_at IS NULL;
