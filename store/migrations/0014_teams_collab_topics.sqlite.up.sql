-- 0014_teams_collab_topics (sqlite, up)

ALTER TABLE repositories ADD COLUMN topics TEXT NOT NULL DEFAULT '[]';

CREATE TABLE teams (
    pk          INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id       INTEGER NOT NULL UNIQUE,
    org_pk      INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    slug        TEXT    NOT NULL,
    description TEXT,
    privacy     TEXT    NOT NULL DEFAULT 'secret',
    permission  TEXT    NOT NULL DEFAULT 'pull',
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX teams_org_slug_uq ON teams (org_pk, slug);

CREATE TABLE team_members (
    pk       INTEGER PRIMARY KEY AUTOINCREMENT,
    team_pk  INTEGER NOT NULL REFERENCES teams(pk) ON DELETE CASCADE,
    user_pk  INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    role     TEXT    NOT NULL DEFAULT 'member',
    UNIQUE (team_pk, user_pk)
);

CREATE TABLE team_repos (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    team_pk    INTEGER NOT NULL REFERENCES teams(pk) ON DELETE CASCADE,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    permission TEXT    NOT NULL DEFAULT 'pull',
    UNIQUE (team_pk, repo_pk)
);

CREATE TABLE collaborators (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    user_pk    INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    permission TEXT    NOT NULL DEFAULT 'push',
    UNIQUE (repo_pk, user_pk)
);
