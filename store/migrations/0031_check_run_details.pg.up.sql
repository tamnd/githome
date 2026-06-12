-- 0031_check_run_details (postgres, up): the check-run details a reporter
-- writes and reads back. The requested actions ride the run as a JSON
-- blob, the annotations live in their own table, and the run keeps a
-- denormalized annotation count so list views avoid a count per row.

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
