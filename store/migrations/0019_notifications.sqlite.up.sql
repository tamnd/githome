-- 0018_notifications (sqlite, up)

CREATE TABLE notification_threads (
    pk           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_pk      INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    repo_pk      INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    issue_pk     INTEGER NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    reason       TEXT    NOT NULL DEFAULT 'subscribed',
    unread       INTEGER NOT NULL DEFAULT 1,
    subscribed   INTEGER NOT NULL DEFAULT 1,
    ignored      INTEGER NOT NULL DEFAULT 0,
    last_read_at TEXT,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX notification_threads_user_issue_uq ON notification_threads (user_pk, issue_pk);
CREATE INDEX notification_threads_user_updated ON notification_threads (user_pk, updated_at);
