-- 0018_notifications (postgres, up)

CREATE TABLE IF NOT EXISTS notification_threads (
    pk           BIGSERIAL PRIMARY KEY,
    user_pk      BIGINT  NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repo_pk      BIGINT  NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    issue_pk     BIGINT  NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    reason       TEXT    NOT NULL DEFAULT 'subscribed',
    unread       BOOLEAN NOT NULL DEFAULT TRUE,
    subscribed   BOOLEAN NOT NULL DEFAULT TRUE,
    ignored      BOOLEAN NOT NULL DEFAULT FALSE,
    last_read_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS notification_threads_user_issue_uq ON notification_threads (user_pk, issue_pk);
CREATE INDEX IF NOT EXISTS notification_threads_user_updated ON notification_threads (user_pk, updated_at);
