-- 0010_issue_keyset (pg, up): the covering index for the issue-list keyset seek.
-- ListIssues pages a repository's issues newest-first with a seek predicate on
-- (created_at, number) under a fixed repo_pk; without an index in that exact
-- order Postgres filters by repo_pk and sorts, so a deep page of a
-- several-hundred-thousand-issue repo scans and sorts the whole repo. This index
-- lets the seek land on the page boundary and read forward, so deep pages cost
-- the page size, not the repo size. It is partial on the soft-delete predicate
-- the list always carries, so deleted rows never bloat it.
CREATE INDEX issues_repo_created_number_idx
    ON issues (repo_pk, created_at DESC, number DESC)
    WHERE deleted_at IS NULL;
