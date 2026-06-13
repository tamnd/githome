-- 0034_social_graph (pg, up): the social graph GitHub exposes through the
-- stars, watchers, and follows families: who starred which repository, who is
-- watching it, and who follows whom. Each is a join table keyed by the pair it
-- relates, so a star or a follow is present or absent rather than counted in a
-- column that could drift.

CREATE TABLE IF NOT EXISTS stars (
    pk         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_pk    BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repo_pk    BIGINT NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_pk, repo_pk)
);

CREATE TABLE IF NOT EXISTS repo_subscriptions (
    pk         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_pk    BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repo_pk    BIGINT NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    subscribed BOOLEAN NOT NULL DEFAULT true,
    ignored    BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_pk, repo_pk)
);

CREATE TABLE IF NOT EXISTS follows (
    pk          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    follower_pk BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    target_pk   BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (follower_pk, target_pk)
);
