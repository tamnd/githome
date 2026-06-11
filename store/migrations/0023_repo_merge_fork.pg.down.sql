-- 0022_repo_merge_fork (postgres, down): drop the fork linkage and the
-- merge-policy columns, newest first.

DROP INDEX repos_fork_of_idx;
ALTER TABLE repositories DROP COLUMN fork_of_pk;
ALTER TABLE repositories DROP COLUMN web_commit_signoff_required;
ALTER TABLE repositories DROP COLUMN allow_update_branch;
ALTER TABLE repositories DROP COLUMN delete_branch_on_merge;
ALTER TABLE repositories DROP COLUMN allow_auto_merge;
ALTER TABLE repositories DROP COLUMN allow_rebase_merge;
ALTER TABLE repositories DROP COLUMN allow_merge_commit;
ALTER TABLE repositories DROP COLUMN allow_squash_merge;
