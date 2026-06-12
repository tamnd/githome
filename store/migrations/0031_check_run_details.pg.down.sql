-- 0031_check_run_details (postgres, down): drop the annotations table and
-- the two columns the details added to check_runs.

DROP TABLE check_run_annotations;
ALTER TABLE check_runs DROP COLUMN annotations_count;
ALTER TABLE check_runs DROP COLUMN actions;
