-- 0014_teams_collab_topics (postgres, up)

ALTER TABLE repositories ADD COLUMN IF NOT EXISTS topics TEXT NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS teams (
    pk          BIGSERIAL PRIMARY KEY,
    db_id       BIGINT NOT NULL UNIQUE,
    org_pk      BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    name        TEXT   NOT NULL,
    slug        TEXT   NOT NULL,
    description TEXT,
    privacy     TEXT   NOT NULL DEFAULT 'secret',
    permission  TEXT   NOT NULL DEFAULT 'pull',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS teams_org_slug_uq ON teams (org_pk, slug);

CREATE TABLE IF NOT EXISTS team_members (
    pk      BIGSERIAL PRIMARY KEY,
    team_pk BIGINT NOT NULL REFERENCES teams(pk) ON DELETE CASCADE,
    user_pk BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    role    TEXT   NOT NULL DEFAULT 'member',
    UNIQUE (team_pk, user_pk)
);

CREATE TABLE IF NOT EXISTS team_repos (
    pk         BIGSERIAL PRIMARY KEY,
    team_pk    BIGINT NOT NULL REFERENCES teams(pk) ON DELETE CASCADE,
    repo_pk    BIGINT NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    permission TEXT   NOT NULL DEFAULT 'pull',
    UNIQUE (team_pk, repo_pk)
);

CREATE TABLE IF NOT EXISTS collaborators (
    pk         BIGSERIAL PRIMARY KEY,
    repo_pk    BIGINT NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    user_pk    BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    permission TEXT   NOT NULL DEFAULT 'push',
    UNIQUE (repo_pk, user_pk)
);
