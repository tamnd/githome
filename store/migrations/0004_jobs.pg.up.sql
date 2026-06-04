-- 0004_jobs (postgres, up): the background job queue. The post-receive sink
-- enqueues work after a push (a push event for webhook delivery, a mergeability
-- recompute per affected pull request, a search reindex of the default branch);
-- later milestones own the workers that claim and run each kind. M3 only writes
-- rows. A partial unique index on the dedupe key collapses a burst of identical
-- work, for example a flurry of pushes to one pull request, into a single
-- queued job. The claim index covers the worker's "next runnable job" scan.

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
