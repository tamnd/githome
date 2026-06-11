-- 0022_repo_redirects (pg, down): drop the rename redirect table; the unique
-- index goes with it.
DROP TABLE repo_redirects;
