-- 0034_social_graph (sqlite, up): the social graph GitHub exposes through the
-- stars, watchers, and follows families: who starred which repository, who is
-- watching it, and who follows whom. Each is a join table keyed by the pair it
-- relates, so a star or a follow is present or absent rather than counted in a
-- column that could drift.

CREATE TABLE stars (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_pk    INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_pk, repo_pk)
);

CREATE TABLE repo_subscriptions (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_pk    INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    subscribed INTEGER NOT NULL DEFAULT 1,
    ignored    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (user_pk, repo_pk)
);

CREATE TABLE follows (
    pk          INTEGER PRIMARY KEY AUTOINCREMENT,
    follower_pk INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    target_pk   INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (follower_pk, target_pk)
);
