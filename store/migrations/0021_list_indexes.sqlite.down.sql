-- 0021_list_indexes (sqlite, down): drop the list-order indexes and restore
-- the two-column comment index.
DROP INDEX issue_comments_issue_created_pk_idx;
CREATE INDEX issue_comments_issue_idx ON issue_comments (issue_pk, created_at) WHERE deleted_at IS NULL;
DROP INDEX gists_public_updated_idx;
DROP INDEX issue_labels_label_idx;
DROP INDEX issues_repo_comments_number_idx;
DROP INDEX issues_repo_updated_number_idx;
