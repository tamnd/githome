-- 0022_repo_merge_fork (postgres, up): the merge-policy settings a repository
-- carries on GitHub (which merge methods PRs may use, auto-merge, branch
-- cleanup, the update-branch button, and commit signoff) plus the fork
-- linkage. fork_of_pk points a fork at its parent repository; it survives a
-- parent's hard delete as NULL rather than cascading the fork away. The
-- partial index serves the parent's fork listing.

ALTER TABLE repositories ADD COLUMN allow_squash_merge          BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN allow_merge_commit          BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN allow_rebase_merge          BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE repositories ADD COLUMN allow_auto_merge            BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repositories ADD COLUMN delete_branch_on_merge      BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repositories ADD COLUMN allow_update_branch         BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repositories ADD COLUMN web_commit_signoff_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE repositories ADD COLUMN fork_of_pk                  BIGINT REFERENCES repositories(pk) ON DELETE SET NULL;

CREATE INDEX repos_fork_of_idx ON repositories (fork_of_pk) WHERE fork_of_pk IS NOT NULL;
