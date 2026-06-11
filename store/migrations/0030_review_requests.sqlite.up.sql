-- 0030_review_requests (sqlite, up): the reviewers requested on a pull
-- request, one row per (pull, reviewer) pair. position keeps the request
-- order so the rendered list is stable.

CREATE TABLE review_requests (
    pull_pk     INTEGER NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    reviewer_pk INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (pull_pk, reviewer_pk)
);
