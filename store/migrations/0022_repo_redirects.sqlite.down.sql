-- 0022_repo_redirects (sqlite, down): drop the rename redirect table; the
-- unique index goes with it.
DROP TABLE repo_redirects;
