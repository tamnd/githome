-- 0021_list_indexes (pg, up): indexes for the list orders that still scanned
-- and sorted. Same shapes as the sqlite pair; see that file for the why per
-- index.
CREATE INDEX issues_repo_updated_number_idx
    ON issues (repo_pk, updated_at DESC, number DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX issues_repo_comments_number_idx
    ON issues (repo_pk, comments_count DESC, number DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX issue_labels_label_idx ON issue_labels (label_pk);
CREATE INDEX gists_public_updated_idx ON gists (public, updated_at DESC);
DROP INDEX issue_comments_issue_idx;
CREATE INDEX issue_comments_issue_created_pk_idx
    ON issue_comments (issue_pk, created_at, pk)
    WHERE deleted_at IS NULL;
