-- 0008_hooks (postgres, up): the activity and webhook surface. An event is one
-- append-only record of something a user did to a repository (opened an issue,
-- pushed, reviewed a pull request); it feeds both the pull-based Events API and
-- the push-based webhook fan-out, so the two never re-derive from each other. A
-- webhook is a repository's registration of a URL to POST those events to, with
-- the event types it wants and an optional shared secret for signing. A delivery
-- is the recorded result of one POST: the request, the response, and the timing,
-- kept so a maintainer can inspect or redeliver it.

CREATE TABLE events (
    pk         BIGSERIAL   PRIMARY KEY,
    db_id      BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    event      TEXT        NOT NULL,
    action     TEXT        NOT NULL DEFAULT '',
    actor_pk   BIGINT      NOT NULL REFERENCES users(pk),
    repo_pk    BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    issue_pk   BIGINT      REFERENCES issues(pk) ON DELETE CASCADE,
    payload    TEXT        NOT NULL DEFAULT '{}',
    public     BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX events_repo_idx ON events (repo_pk, pk DESC);
CREATE INDEX events_actor_idx ON events (actor_pk, pk DESC);
CREATE INDEX events_issue_idx ON events (issue_pk, pk);

CREATE TABLE webhooks (
    pk            BIGSERIAL   PRIMARY KEY,
    db_id         BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk       BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    name          TEXT        NOT NULL DEFAULT 'web',
    url           TEXT        NOT NULL,
    content_type  TEXT        NOT NULL DEFAULT 'json',
    secret        TEXT,
    insecure_ssl  BOOLEAN     NOT NULL DEFAULT FALSE,
    active        BOOLEAN     NOT NULL DEFAULT TRUE,
    events        TEXT        NOT NULL DEFAULT '["push"]',
    last_response TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhooks_repo_idx ON webhooks (repo_pk);

CREATE TABLE webhook_deliveries (
    pk               BIGSERIAL   PRIMARY KEY,
    db_id            BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    webhook_pk       BIGINT      NOT NULL REFERENCES webhooks(pk) ON DELETE CASCADE,
    guid             TEXT        NOT NULL,
    event            TEXT        NOT NULL,
    action           TEXT        NOT NULL DEFAULT '',
    status_code      INTEGER,
    request_url      TEXT        NOT NULL DEFAULT '',
    request_headers  TEXT        NOT NULL DEFAULT '{}',
    request_body     TEXT        NOT NULL DEFAULT '',
    response_headers TEXT        NOT NULL DEFAULT '{}',
    response_body    TEXT        NOT NULL DEFAULT '',
    duration_ms      INTEGER     NOT NULL DEFAULT 0,
    redelivery       BOOLEAN     NOT NULL DEFAULT FALSE,
    success          BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX webhook_deliveries_hook_idx ON webhook_deliveries (webhook_pk, pk DESC);
CREATE INDEX webhook_deliveries_guid_idx ON webhook_deliveries (guid);
