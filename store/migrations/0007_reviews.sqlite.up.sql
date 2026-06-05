-- 0007_reviews (sqlite, up): the code review surface. A review is one act of
-- reviewing a pull request (approve, request changes, comment, or a pending
-- draft); a review comment is anchored to a diff line and belongs to a review and
-- a thread. Commit statuses and check runs are the two independent pass/fail
-- signals external systems report against a head sha; their combination is the
-- status check rollup. The cache table holds the derived reviewDecision and
-- rollup state a recompute worker refreshes, so list views and webhook payloads
-- read one row instead of re-aggregating. db_id is allocated in the insert tx so
-- node ids stay unique across every kind.

CREATE TABLE pull_request_reviews (
    pk                INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id             INTEGER NOT NULL UNIQUE,
    pull_pk           INTEGER NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    repo_pk           INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    user_pk           INTEGER NOT NULL REFERENCES users(pk),
    state             TEXT    NOT NULL,
    body              TEXT    NOT NULL DEFAULT '',
    commit_id         TEXT    NOT NULL DEFAULT '',
    dismissed_message TEXT,
    submitted_at      TEXT,
    created_at        TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at        TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX pull_request_reviews_pull_idx ON pull_request_reviews (pull_pk);
CREATE UNIQUE INDEX pull_request_reviews_pending_idx
    ON pull_request_reviews (pull_pk, user_pk) WHERE state = 'PENDING';

CREATE TABLE pull_request_review_comments (
    pk                  INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id               INTEGER NOT NULL UNIQUE,
    review_pk           INTEGER NOT NULL REFERENCES pull_request_reviews(pk) ON DELETE CASCADE,
    pull_pk             INTEGER NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    repo_pk             INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    user_pk             INTEGER NOT NULL REFERENCES users(pk),
    path                TEXT    NOT NULL,
    side                TEXT    NOT NULL DEFAULT 'RIGHT',
    line                INTEGER,
    start_line          INTEGER,
    start_side          TEXT,
    original_line       INTEGER,
    original_start_line INTEGER,
    position            INTEGER,
    original_position   INTEGER,
    commit_id           TEXT    NOT NULL DEFAULT '',
    original_commit_id  TEXT    NOT NULL DEFAULT '',
    in_reply_to_pk      INTEGER REFERENCES pull_request_review_comments(pk) ON DELETE SET NULL,
    diff_hunk           TEXT    NOT NULL DEFAULT '',
    subject_type        TEXT    NOT NULL DEFAULT 'line',
    body                TEXT    NOT NULL,
    resolved            INTEGER NOT NULL DEFAULT 0,
    resolved_by_pk      INTEGER REFERENCES users(pk),
    created_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at          TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX pull_request_review_comments_pull_idx ON pull_request_review_comments (pull_pk);
CREATE INDEX pull_request_review_comments_review_idx ON pull_request_review_comments (review_pk);

CREATE TABLE commit_statuses (
    pk          INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id       INTEGER NOT NULL UNIQUE,
    repo_pk     INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    sha         TEXT    NOT NULL,
    state       TEXT    NOT NULL,
    context     TEXT    NOT NULL DEFAULT 'default',
    target_url  TEXT,
    description TEXT,
    creator_pk  INTEGER REFERENCES users(pk),
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX commit_statuses_sha_idx ON commit_statuses (repo_pk, sha);

CREATE TABLE check_suites (
    pk         INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id      INTEGER NOT NULL UNIQUE,
    repo_pk    INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    head_sha   TEXT    NOT NULL,
    app_slug   TEXT    NOT NULL DEFAULT 'githome',
    status     TEXT    NOT NULL DEFAULT 'queued',
    conclusion TEXT,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX check_suites_key_idx ON check_suites (repo_pk, head_sha, app_slug);

CREATE TABLE check_runs (
    pk             INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id          INTEGER NOT NULL UNIQUE,
    suite_pk       INTEGER NOT NULL REFERENCES check_suites(pk) ON DELETE CASCADE,
    repo_pk        INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    head_sha       TEXT    NOT NULL,
    name           TEXT    NOT NULL,
    status         TEXT    NOT NULL DEFAULT 'queued',
    conclusion     TEXT,
    details_url    TEXT,
    external_id    TEXT,
    output_title   TEXT,
    output_summary TEXT,
    output_text    TEXT,
    started_at     TEXT,
    completed_at   TEXT,
    created_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX check_runs_sha_idx ON check_runs (repo_pk, head_sha);
CREATE INDEX check_runs_suite_idx ON check_runs (suite_pk);

CREATE TABLE pull_request_check_state (
    pull_pk         INTEGER PRIMARY KEY REFERENCES pull_requests(pk) ON DELETE CASCADE,
    review_decision TEXT,
    rollup_state    TEXT    NOT NULL DEFAULT 'EXPECTED',
    updated_at      TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
