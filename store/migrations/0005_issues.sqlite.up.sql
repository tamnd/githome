-- 0005_issues (sqlite, up): mirrors the postgres issue subsystem with
-- SQLite-native types. Booleans are INTEGER (0/1), timestamps are TEXT, and the
-- shared db_id comes from the id_allocator high-water table rather than a
-- sequence default, so each INSERT supplies db_id explicitly from AllocDBID.

ALTER TABLE repositories ADD COLUMN next_milestone_number INTEGER NOT NULL DEFAULT 1;

CREATE TABLE labels (
    pk          INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id       INTEGER NOT NULL UNIQUE,
    repo_pk     INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    color       TEXT    NOT NULL DEFAULT 'ededed',
    description TEXT,
    is_default  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX labels_repo_name_uq ON labels (repo_pk, lower(name));

CREATE TABLE milestones (
    pk          INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id       INTEGER NOT NULL UNIQUE,
    repo_pk     INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    number      INTEGER NOT NULL,
    title       TEXT    NOT NULL,
    description TEXT,
    state       TEXT    NOT NULL DEFAULT 'open',
    due_on      TEXT,
    creator_pk  INTEGER REFERENCES users(pk),
    closed_at   TEXT,
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX milestones_repo_number_uq ON milestones (repo_pk, number);

CREATE TABLE issues (
    pk                 INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id              INTEGER NOT NULL UNIQUE,
    repo_pk            INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    number             INTEGER NOT NULL,
    is_pull            INTEGER NOT NULL DEFAULT 0,
    title              TEXT    NOT NULL,
    body               TEXT,
    user_pk            INTEGER NOT NULL REFERENCES users(pk),
    state              TEXT    NOT NULL DEFAULT 'open',
    state_reason       TEXT,
    milestone_pk       INTEGER REFERENCES milestones(pk) ON DELETE SET NULL,
    locked             INTEGER NOT NULL DEFAULT 0,
    active_lock_reason TEXT,
    comments_count     INTEGER NOT NULL DEFAULT 0,
    closed_at          TEXT,
    closed_by_pk       INTEGER REFERENCES users(pk),
    lock_version       INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at         TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at         TEXT
);
CREATE UNIQUE INDEX issues_repo_number_uq ON issues (repo_pk, number);
CREATE INDEX issues_repo_state_idx ON issues (repo_pk, state) WHERE deleted_at IS NULL;
CREATE INDEX issues_repo_pull_idx ON issues (repo_pk, is_pull) WHERE deleted_at IS NULL;

CREATE TABLE issue_comments (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id      INTEGER NOT NULL UNIQUE,
    issue_pk   INTEGER NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    user_pk    INTEGER NOT NULL REFERENCES users(pk),
    body       TEXT    NOT NULL,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at TEXT
);
CREATE INDEX issue_comments_issue_idx ON issue_comments (issue_pk, created_at) WHERE deleted_at IS NULL;

CREATE TABLE issue_labels (
    issue_pk INTEGER NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    label_pk INTEGER NOT NULL REFERENCES labels(pk) ON DELETE CASCADE,
    PRIMARY KEY (issue_pk, label_pk)
);

CREATE TABLE assignees (
    issue_pk INTEGER NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    user_pk  INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (issue_pk, user_pk)
);

CREATE TABLE reactions (
    pk           INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id        INTEGER NOT NULL UNIQUE,
    subject_type TEXT    NOT NULL,
    subject_pk   INTEGER NOT NULL,
    user_pk      INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    content      TEXT    NOT NULL,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX reactions_subject_user_content_uq
    ON reactions (subject_type, subject_pk, user_pk, content);
CREATE INDEX reactions_subject_idx ON reactions (subject_type, subject_pk);

CREATE TABLE issue_events (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id      INTEGER NOT NULL UNIQUE,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    issue_pk   INTEGER NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    actor_pk   INTEGER REFERENCES users(pk),
    event      TEXT    NOT NULL,
    payload    TEXT    NOT NULL DEFAULT '{}',
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX issue_events_issue_idx ON issue_events (issue_pk, created_at);
CREATE INDEX issue_events_repo_idx ON issue_events (repo_pk, created_at);
