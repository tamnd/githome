-- 0005_issues (postgres, up): the issue subsystem. Issues share the per-repo
-- number sequence with pull requests (repositories.next_issue_number, already in
-- 0001), so an issue and a PR never collide on a number; milestones get their
-- own per-repo counter added here. The tables are issues, their comments,
-- per-repo labels and the issue/label join, milestones, the issue/assignee join,
-- polymorphic reactions, and the issue timeline event log. The webhook fan-out
-- that turns a create into a delivered `issues` event lands in M7; M4 only
-- enqueues the job and records the timeline rows.

ALTER TABLE repositories ADD COLUMN next_milestone_number BIGINT NOT NULL DEFAULT 1;

CREATE TABLE labels (
    pk          BIGSERIAL   PRIMARY KEY,
    db_id       BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk     BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    name        TEXT        NOT NULL,
    color       TEXT        NOT NULL DEFAULT 'ededed',
    description TEXT,
    is_default  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX labels_repo_name_uq ON labels (repo_pk, lower(name));

CREATE TABLE milestones (
    pk           BIGSERIAL   PRIMARY KEY,
    db_id        BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk      BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    number       BIGINT      NOT NULL,
    title        TEXT        NOT NULL,
    description  TEXT,
    state        TEXT        NOT NULL DEFAULT 'open',
    due_on       TIMESTAMPTZ,
    creator_pk   BIGINT      REFERENCES users(pk),
    closed_at    TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX milestones_repo_number_uq ON milestones (repo_pk, number);

CREATE TABLE issues (
    pk                BIGSERIAL   PRIMARY KEY,
    db_id             BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk           BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    number            BIGINT      NOT NULL,
    is_pull           BOOLEAN     NOT NULL DEFAULT FALSE,
    title             TEXT        NOT NULL,
    body              TEXT,
    user_pk           BIGINT      NOT NULL REFERENCES users(pk),
    state             TEXT        NOT NULL DEFAULT 'open',
    state_reason      TEXT,
    milestone_pk      BIGINT      REFERENCES milestones(pk) ON DELETE SET NULL,
    locked            BOOLEAN     NOT NULL DEFAULT FALSE,
    active_lock_reason TEXT,
    comments_count    INTEGER     NOT NULL DEFAULT 0,
    closed_at         TIMESTAMPTZ,
    closed_by_pk      BIGINT      REFERENCES users(pk),
    lock_version      BIGINT      NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);
CREATE UNIQUE INDEX issues_repo_number_uq ON issues (repo_pk, number);
CREATE INDEX issues_repo_state_idx ON issues (repo_pk, state) WHERE deleted_at IS NULL;
CREATE INDEX issues_repo_pull_idx ON issues (repo_pk, is_pull) WHERE deleted_at IS NULL;

CREATE TABLE issue_comments (
    pk           BIGSERIAL   PRIMARY KEY,
    db_id        BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    issue_pk     BIGINT      NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    user_pk      BIGINT      NOT NULL REFERENCES users(pk),
    body         TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at   TIMESTAMPTZ
);
CREATE INDEX issue_comments_issue_idx ON issue_comments (issue_pk, created_at) WHERE deleted_at IS NULL;

CREATE TABLE issue_labels (
    issue_pk BIGINT NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    label_pk BIGINT NOT NULL REFERENCES labels(pk) ON DELETE CASCADE,
    PRIMARY KEY (issue_pk, label_pk)
);

CREATE TABLE assignees (
    issue_pk BIGINT  NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    user_pk  BIGINT  NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (issue_pk, user_pk)
);

CREATE TABLE reactions (
    pk           BIGSERIAL   PRIMARY KEY,
    db_id        BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    subject_type TEXT        NOT NULL,
    subject_pk   BIGINT      NOT NULL,
    user_pk      BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    content      TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX reactions_subject_user_content_uq
    ON reactions (subject_type, subject_pk, user_pk, content);
CREATE INDEX reactions_subject_idx ON reactions (subject_type, subject_pk);

CREATE TABLE issue_events (
    pk         BIGSERIAL   PRIMARY KEY,
    db_id      BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk    BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    issue_pk   BIGINT      NOT NULL REFERENCES issues(pk) ON DELETE CASCADE,
    actor_pk   BIGINT      REFERENCES users(pk),
    event      TEXT        NOT NULL,
    payload    TEXT        NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX issue_events_issue_idx ON issue_events (issue_pk, created_at);
CREATE INDEX issue_events_repo_idx ON issue_events (repo_pk, created_at);
