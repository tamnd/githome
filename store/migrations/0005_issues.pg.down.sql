-- 0005_issues (postgres, down): drop the issue tables in reverse dependency
-- order, then the milestone-number column. Indexes drop with their tables.

DROP TABLE IF EXISTS issue_events;
DROP TABLE IF EXISTS reactions;
DROP TABLE IF EXISTS assignees;
DROP TABLE IF EXISTS issue_labels;
DROP TABLE IF EXISTS issue_comments;
DROP TABLE IF EXISTS issues;
DROP TABLE IF EXISTS milestones;
DROP TABLE IF EXISTS labels;

ALTER TABLE repositories DROP COLUMN next_milestone_number;
