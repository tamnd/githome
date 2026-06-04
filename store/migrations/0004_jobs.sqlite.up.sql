-- 0004_jobs (sqlite, up): mirrors the postgres job queue with SQLite-native
-- types. The partial unique index on the dedupe key and the partial claim index
-- both work on SQLite, which supports indexes with a WHERE clause.

CREATE TABLE jobs (
    pk           INTEGER PRIMARY KEY AUTOINCREMENT,
    kind         TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '{}',
    dedupe_key   TEXT,
    state        TEXT    NOT NULL DEFAULT 'queued',
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 5,
    run_after    TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error   TEXT,
    locked_at    TEXT,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX jobs_dedupe_active_uq ON jobs (dedupe_key)
    WHERE dedupe_key IS NOT NULL AND state IN ('queued', 'running');

CREATE INDEX jobs_claim_idx ON jobs (run_after) WHERE state = 'queued';
