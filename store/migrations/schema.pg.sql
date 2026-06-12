-- schema.pg.sql: the complete Postgres schema from scratch, equivalent to
-- running every up migration (0001..0010) in order. A fresh install can apply
-- this single file instead of the incremental runner; Store.Install does exactly
-- that and then stamps schema_migrations so a later Migrate is a no-op.
--
-- This mirrors schema.sqlite.sql one table at a time with Postgres-native types
-- (BIGSERIAL/BIGINT, BOOLEAN, TIMESTAMPTZ, BYTEA, tsvector). The ALTER TABLE ADD
-- COLUMN statements the migrations use are folded into the CREATE statements,
-- with the added columns kept in migration order at the end of each table.
--
-- The SQLite half of this pair is guarded by TestSchemaFileMatchesMigrations;
-- this file is exercised live wherever GITHOME_TEST_POSTGRES_DSN is set. Keep it
-- in lockstep with the migrations: when you add one, fold its up body into the
-- matching CREATE here.

CREATE SEQUENCE IF NOT EXISTS global_id_seq START 1;

CREATE TABLE users (
    pk            BIGSERIAL PRIMARY KEY,
    db_id         BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    login         TEXT        NOT NULL,
    type          TEXT        NOT NULL DEFAULT 'User',
    name          TEXT,
    email         TEXT,
    site_admin    BOOLEAN     NOT NULL DEFAULT FALSE,
    password_hash TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ,
    company          TEXT,
    blog             TEXT    NOT NULL DEFAULT '',
    location         TEXT,
    bio              TEXT,
    hireable         BOOLEAN,
    twitter_username TEXT,
    public_repos     INTEGER NOT NULL DEFAULT 0,
    public_gists     INTEGER NOT NULL DEFAULT 0,
    followers        INTEGER NOT NULL DEFAULT 0,
    following        INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX users_login_lower_uq ON users (lower(login)) WHERE deleted_at IS NULL;

CREATE TABLE repositories (
    pk                BIGSERIAL PRIMARY KEY,
    db_id             BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    owner_pk          BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    name              TEXT        NOT NULL,
    description       TEXT,
    private           BOOLEAN     NOT NULL DEFAULT FALSE,
    fork              BOOLEAN     NOT NULL DEFAULT FALSE,
    default_branch    TEXT        NOT NULL DEFAULT 'main',
    next_issue_number BIGINT      NOT NULL DEFAULT 1,
    open_issues_count INTEGER     NOT NULL DEFAULT 0,
    pushed_at         TIMESTAMPTZ,
    lock_version      BIGINT      NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ,
    homepage      TEXT,
    has_issues    BOOLEAN NOT NULL DEFAULT TRUE,
    has_projects  BOOLEAN NOT NULL DEFAULT TRUE,
    has_wiki      BOOLEAN NOT NULL DEFAULT TRUE,
    has_downloads BOOLEAN NOT NULL DEFAULT TRUE,
    archived      BOOLEAN NOT NULL DEFAULT FALSE,
    disabled      BOOLEAN NOT NULL DEFAULT FALSE,
    is_template   BOOLEAN NOT NULL DEFAULT FALSE,
    next_milestone_number BIGINT NOT NULL DEFAULT 1,
    search_vector tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', name || ' ' || COALESCE(description, ''))
    ) STORED,
    allow_squash_merge          BOOLEAN NOT NULL DEFAULT TRUE,   -- 0023
    allow_merge_commit          BOOLEAN NOT NULL DEFAULT TRUE,   -- 0023
    allow_rebase_merge          BOOLEAN NOT NULL DEFAULT TRUE,   -- 0023
    allow_auto_merge            BOOLEAN NOT NULL DEFAULT FALSE,  -- 0023
    delete_branch_on_merge      BOOLEAN NOT NULL DEFAULT FALSE,  -- 0023
    allow_update_branch         BOOLEAN NOT NULL DEFAULT FALSE,  -- 0023
    web_commit_signoff_required BOOLEAN NOT NULL DEFAULT FALSE,  -- 0023
    fork_of_pk                  BIGINT REFERENCES repositories(pk) ON DELETE SET NULL  -- 0023
);
CREATE UNIQUE INDEX repos_owner_name_uq ON repositories (owner_pk, lower(name)) WHERE deleted_at IS NULL;
CREATE INDEX repos_search_vector_gin ON repositories USING GIN (search_vector);
CREATE INDEX repos_fork_of_idx ON repositories (fork_of_pk) WHERE fork_of_pk IS NOT NULL;  -- 0023

CREATE TABLE oauth_apps (
    pk                  BIGSERIAL PRIMARY KEY,
    client_id           TEXT        NOT NULL UNIQUE,
    client_secret_hash  BYTEA,
    name                TEXT        NOT NULL,
    owner_pk            BIGINT      REFERENCES users(pk) ON DELETE CASCADE,
    device_flow_enabled BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    callback_url        TEXT        NOT NULL DEFAULT ''  -- 0017
);

CREATE TABLE tokens (
    pk           BIGSERIAL   PRIMARY KEY,
    user_pk      BIGINT      REFERENCES users(pk) ON DELETE CASCADE,
    oauth_app_pk BIGINT      REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    token_hash   BYTEA       NOT NULL UNIQUE,
    token_prefix TEXT        NOT NULL,
    last_eight   TEXT        NOT NULL,
    kind         TEXT        NOT NULL,
    scopes       TEXT        NOT NULL DEFAULT '',
    note         TEXT        NOT NULL DEFAULT '',
    expires_at   TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE oauth_device_codes (
    pk               BIGSERIAL   PRIMARY KEY,
    device_code_hash BYTEA       NOT NULL UNIQUE,
    user_code        TEXT        NOT NULL UNIQUE,
    oauth_app_pk     BIGINT      REFERENCES oauth_apps(pk) ON DELETE CASCADE,
    scopes           TEXT        NOT NULL DEFAULT '',
    state            TEXT        NOT NULL DEFAULT 'pending',
    user_pk          BIGINT      REFERENCES users(pk) ON DELETE SET NULL,
    interval_sec     INTEGER     NOT NULL DEFAULT 5,
    last_polled_at   TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE jobs (
    pk           BIGSERIAL    PRIMARY KEY,
    kind         TEXT         NOT NULL,
    payload      TEXT         NOT NULL DEFAULT '{}',
    dedupe_key   TEXT,
    state        TEXT         NOT NULL DEFAULT 'queued',
    attempts     INTEGER      NOT NULL DEFAULT 0,
    max_attempts INTEGER      NOT NULL DEFAULT 5,
    run_after    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_error   TEXT,
    locked_at    TIMESTAMPTZ,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX jobs_dedupe_active_uq ON jobs (dedupe_key)
    WHERE dedupe_key IS NOT NULL AND state IN ('queued', 'running');
CREATE INDEX jobs_claim_idx ON jobs (run_after) WHERE state = 'queued';

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
    deleted_at        TIMESTAMPTZ,
    search_vector tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', title || ' ' || COALESCE(body, ''))
    ) STORED
);
CREATE UNIQUE INDEX issues_repo_number_uq ON issues (repo_pk, number);
CREATE INDEX issues_repo_state_idx ON issues (repo_pk, state) WHERE deleted_at IS NULL;
CREATE INDEX issues_repo_pull_idx ON issues (repo_pk, is_pull) WHERE deleted_at IS NULL;
CREATE INDEX issues_search_vector_gin ON issues USING GIN (search_vector);
CREATE INDEX issues_repo_created_number_idx
    ON issues (repo_pk, created_at DESC, number DESC)
    WHERE deleted_at IS NULL;

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

CREATE TABLE pull_requests (
    pk                      BIGSERIAL   PRIMARY KEY,
    db_id                   BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    issue_pk                BIGINT      NOT NULL UNIQUE REFERENCES issues(pk) ON DELETE CASCADE,
    repo_pk                 BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    base_ref                TEXT        NOT NULL,
    base_sha                TEXT        NOT NULL,
    head_ref                TEXT        NOT NULL,
    head_sha                TEXT        NOT NULL,
    head_repo_pk            BIGINT      REFERENCES repositories(pk) ON DELETE SET NULL,
    draft                   BOOLEAN     NOT NULL DEFAULT FALSE,
    maintainer_can_modify   BOOLEAN     NOT NULL DEFAULT FALSE,
    merged                  BOOLEAN     NOT NULL DEFAULT FALSE,
    merged_at               TIMESTAMPTZ,
    merged_by_pk            BIGINT      REFERENCES users(pk),
    merge_commit_sha        TEXT,
    mergeable               BOOLEAN,
    mergeable_state         TEXT        NOT NULL DEFAULT 'unknown',
    rebaseable              BOOLEAN,
    additions               INTEGER     NOT NULL DEFAULT 0,
    deletions               INTEGER     NOT NULL DEFAULT 0,
    changed_files           INTEGER     NOT NULL DEFAULT 0,
    commits_count           INTEGER     NOT NULL DEFAULT 0,
    mergeability_checked_at TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_requests_repo_idx ON pull_requests (repo_pk);
CREATE INDEX pull_requests_open_head_idx ON pull_requests (repo_pk, head_ref) WHERE merged = FALSE;

CREATE TABLE pull_request_reviews (
    pk                BIGSERIAL   PRIMARY KEY,
    db_id             BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    pull_pk           BIGINT      NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    repo_pk           BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    user_pk           BIGINT      NOT NULL REFERENCES users(pk),
    state             TEXT        NOT NULL,
    body              TEXT        NOT NULL DEFAULT '',
    commit_id         TEXT        NOT NULL DEFAULT '',
    dismissed_message TEXT,
    submitted_at      TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_request_reviews_pull_idx ON pull_request_reviews (pull_pk);
CREATE UNIQUE INDEX pull_request_reviews_pending_idx
    ON pull_request_reviews (pull_pk, user_pk) WHERE state = 'PENDING';

CREATE TABLE pull_request_review_comments (
    pk                  BIGSERIAL   PRIMARY KEY,
    db_id               BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    review_pk           BIGINT      NOT NULL REFERENCES pull_request_reviews(pk) ON DELETE CASCADE,
    pull_pk             BIGINT      NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    repo_pk             BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    user_pk             BIGINT      NOT NULL REFERENCES users(pk),
    path                TEXT        NOT NULL,
    side                TEXT        NOT NULL DEFAULT 'RIGHT',
    line                INTEGER,
    start_line          INTEGER,
    start_side          TEXT,
    original_line       INTEGER,
    original_start_line INTEGER,
    position            INTEGER,
    original_position   INTEGER,
    commit_id           TEXT        NOT NULL DEFAULT '',
    original_commit_id  TEXT        NOT NULL DEFAULT '',
    in_reply_to_pk      BIGINT      REFERENCES pull_request_review_comments(pk) ON DELETE SET NULL,
    diff_hunk           TEXT        NOT NULL DEFAULT '',
    subject_type        TEXT        NOT NULL DEFAULT 'line',
    body                TEXT        NOT NULL,
    resolved            BOOLEAN     NOT NULL DEFAULT FALSE,
    resolved_by_pk      BIGINT      REFERENCES users(pk),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX pull_request_review_comments_pull_idx ON pull_request_review_comments (pull_pk);
CREATE INDEX pull_request_review_comments_review_idx ON pull_request_review_comments (review_pk);

CREATE TABLE commit_statuses (
    pk          BIGSERIAL   PRIMARY KEY,
    db_id       BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk     BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    sha         TEXT        NOT NULL,
    state       TEXT        NOT NULL,
    context     TEXT        NOT NULL DEFAULT 'default',
    target_url  TEXT,
    description TEXT,
    creator_pk  BIGINT      REFERENCES users(pk),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX commit_statuses_sha_idx ON commit_statuses (repo_pk, sha);

CREATE TABLE check_suites (
    pk         BIGSERIAL   PRIMARY KEY,
    db_id      BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk    BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    head_sha   TEXT        NOT NULL,
    app_slug   TEXT        NOT NULL DEFAULT 'githome',
    status     TEXT        NOT NULL DEFAULT 'queued',
    conclusion TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX check_suites_key_idx ON check_suites (repo_pk, head_sha, app_slug);

CREATE TABLE check_runs (
    pk             BIGSERIAL   PRIMARY KEY,
    db_id          BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    suite_pk       BIGINT      NOT NULL REFERENCES check_suites(pk) ON DELETE CASCADE,
    repo_pk        BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    head_sha       TEXT        NOT NULL,
    name           TEXT        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'queued',
    conclusion     TEXT,
    details_url    TEXT,
    external_id    TEXT,
    output_title   TEXT,
    output_summary TEXT,
    output_text    TEXT,
    started_at     TIMESTAMPTZ,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX check_runs_sha_idx ON check_runs (repo_pk, head_sha);
CREATE INDEX check_runs_suite_idx ON check_runs (suite_pk);

CREATE TABLE pull_request_check_state (
    pull_pk         BIGINT      PRIMARY KEY REFERENCES pull_requests(pk) ON DELETE CASCADE,
    review_decision TEXT,
    rollup_state    TEXT        NOT NULL DEFAULT 'EXPECTED',
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

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

-- 0011_releases
ALTER TABLE repositories ADD COLUMN next_release_number BIGINT NOT NULL DEFAULT 1;

CREATE TABLE releases (
    pk               BIGSERIAL   PRIMARY KEY,
    db_id            BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    repo_pk          BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    tag_name         TEXT        NOT NULL,
    target_commitish TEXT        NOT NULL DEFAULT 'main',
    name             TEXT,
    body             TEXT,
    draft            BOOLEAN     NOT NULL DEFAULT FALSE,
    prerelease       BOOLEAN     NOT NULL DEFAULT FALSE,
    author_pk        BIGINT      REFERENCES users(pk),
    lock_version     BIGINT      NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at     TIMESTAMPTZ,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at       TIMESTAMPTZ
);
CREATE UNIQUE INDEX releases_repo_tag_uq ON releases (repo_pk, tag_name) WHERE deleted_at IS NULL;
CREATE INDEX releases_repo_pub_idx ON releases (repo_pk, published_at DESC NULLS LAST) WHERE deleted_at IS NULL;

CREATE TABLE release_assets (
    pk             BIGSERIAL   PRIMARY KEY,
    db_id          BIGINT      NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    release_pk     BIGINT      NOT NULL REFERENCES releases(pk) ON DELETE CASCADE,
    name           TEXT        NOT NULL,
    label          TEXT,
    content_type   TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size           BIGINT      NOT NULL DEFAULT 0,
    download_count BIGINT      NOT NULL DEFAULT 0,
    uploader_pk    BIGINT      REFERENCES users(pk),
    state          TEXT        NOT NULL DEFAULT 'open',
    lock_version   BIGINT      NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX release_assets_release_name_uq ON release_assets (release_pk, name) WHERE deleted_at IS NULL;

-- 0022_repo_redirects
CREATE TABLE repo_redirects (
    pk         BIGSERIAL   PRIMARY KEY,
    old_owner  TEXT        NOT NULL,
    old_name   TEXT        NOT NULL,
    repo_pk    BIGINT      NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX repo_redirects_old_uq ON repo_redirects (old_owner, old_name);

-- 0030_review_requests
CREATE TABLE review_requests (
    pull_pk     BIGINT      NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    reviewer_pk BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    position    INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_pk, reviewer_pk)
);

-- 0031_check_run_details
ALTER TABLE check_runs ADD COLUMN actions TEXT;
ALTER TABLE check_runs ADD COLUMN annotations_count INTEGER NOT NULL DEFAULT 0;
CREATE TABLE check_run_annotations (
    pk               BIGSERIAL   PRIMARY KEY,
    check_run_pk     BIGINT      NOT NULL REFERENCES check_runs(pk) ON DELETE CASCADE,
    path             TEXT        NOT NULL,
    start_line       INTEGER     NOT NULL,
    end_line         INTEGER     NOT NULL,
    start_column     INTEGER,
    end_column       INTEGER,
    annotation_level TEXT        NOT NULL,
    message          TEXT        NOT NULL,
    title            TEXT,
    raw_details      TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX check_run_annotations_run_idx ON check_run_annotations (check_run_pk);
