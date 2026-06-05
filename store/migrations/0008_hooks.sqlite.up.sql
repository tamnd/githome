-- 0008_hooks (sqlite, up): the activity and webhook surface. An event is one
-- append-only record of something a user did to a repository (opened an issue,
-- pushed, reviewed a pull request); it feeds both the pull-based Events API and
-- the push-based webhook fan-out, so the two never re-derive from each other. A
-- webhook is a repository's registration of a URL to POST those events to, with
-- the event types it wants and an optional shared secret for signing. A delivery
-- is the recorded result of one POST: the request, the response, and the timing,
-- kept so a maintainer can inspect or redeliver it. db_id is allocated in the
-- insert tx so node ids stay unique across every kind.

CREATE TABLE events (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id      INTEGER NOT NULL UNIQUE,
    event      TEXT    NOT NULL,
    action     TEXT    NOT NULL DEFAULT '',
    actor_pk   INTEGER NOT NULL REFERENCES users(pk),
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    issue_pk   INTEGER REFERENCES issues(pk) ON DELETE CASCADE,
    payload    TEXT    NOT NULL DEFAULT '{}',
    public     INTEGER NOT NULL DEFAULT 1,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX events_repo_idx ON events (repo_pk, pk DESC);
CREATE INDEX events_actor_idx ON events (actor_pk, pk DESC);
CREATE INDEX events_issue_idx ON events (issue_pk, pk);

CREATE TABLE webhooks (
    pk            INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id         INTEGER NOT NULL UNIQUE,
    repo_pk       INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    name          TEXT    NOT NULL DEFAULT 'web',
    url           TEXT    NOT NULL,
    content_type  TEXT    NOT NULL DEFAULT 'json',
    secret        TEXT,
    insecure_ssl  INTEGER NOT NULL DEFAULT 0,
    active        INTEGER NOT NULL DEFAULT 1,
    events        TEXT    NOT NULL DEFAULT '["push"]',
    last_response TEXT,
    created_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX webhooks_repo_idx ON webhooks (repo_pk);

CREATE TABLE webhook_deliveries (
    pk               INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id            INTEGER NOT NULL UNIQUE,
    webhook_pk       INTEGER NOT NULL REFERENCES webhooks(pk) ON DELETE CASCADE,
    guid             TEXT    NOT NULL,
    event            TEXT    NOT NULL,
    action           TEXT    NOT NULL DEFAULT '',
    status_code      INTEGER,
    request_url      TEXT    NOT NULL DEFAULT '',
    request_headers  TEXT    NOT NULL DEFAULT '{}',
    request_body     TEXT    NOT NULL DEFAULT '',
    response_headers TEXT    NOT NULL DEFAULT '{}',
    response_body    TEXT    NOT NULL DEFAULT '',
    duration_ms      INTEGER NOT NULL DEFAULT 0,
    redelivery       INTEGER NOT NULL DEFAULT 0,
    success          INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX webhook_deliveries_hook_idx ON webhook_deliveries (webhook_pk, pk DESC);
CREATE INDEX webhook_deliveries_guid_idx ON webhook_deliveries (guid);
