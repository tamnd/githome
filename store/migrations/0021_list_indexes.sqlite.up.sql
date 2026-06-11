-- 0021_list_indexes (sqlite, up): indexes for the list orders that still
-- scanned and sorted.
--
-- ?sort=updated and ?sort=comments order the issue list by a column only the
-- (repo_pk, created_at, number) keyset index does not cover, so the planner
-- filtered by repo_pk and sorted the whole repository per page. One index per
-- sort column, in the same shape as the created_at one, makes each a forward
-- index read. Both are partial on the soft-delete predicate the list always
-- carries.
CREATE INDEX issues_repo_updated_number_idx
    ON issues (repo_pk, updated_at DESC, number DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX issues_repo_comments_number_idx
    ON issues (repo_pk, comments_count DESC, number DESC)
    WHERE deleted_at IS NULL;

-- Reverse "issues carrying label X" lookups could only probe the
-- (issue_pk, label_pk) primary key, a full scan per label.
CREATE INDEX issue_labels_label_idx ON issue_labels (label_pk);

-- ListPublicGists orders by updated_at over a public=1 scan.
CREATE INDEX gists_public_updated_idx ON gists (public, updated_at DESC);

-- The comment list orders by (created_at, pk) but the index stopped at
-- created_at, leaving a residual sort on every page of a long thread. Widen
-- the index by the tie-breaker so both the page list and the keyset seek
-- read straight off it; the two-column form is a strict prefix, so it goes.
DROP INDEX issue_comments_issue_idx;
CREATE INDEX issue_comments_issue_created_pk_idx
    ON issue_comments (issue_pk, created_at, pk)
    WHERE deleted_at IS NULL;
