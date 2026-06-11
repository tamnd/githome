-- 0030_review_requests (postgres, up): the reviewers requested on a pull
-- request, one row per (pull, reviewer) pair. position keeps the request
-- order so the rendered list is stable.

CREATE TABLE review_requests (
    pull_pk     BIGINT      NOT NULL REFERENCES pull_requests(pk) ON DELETE CASCADE,
    reviewer_pk BIGINT      NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    position    INTEGER     NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (pull_pk, reviewer_pk)
);
