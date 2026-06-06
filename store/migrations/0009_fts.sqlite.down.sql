-- 0009_fts (sqlite, down): drop FTS virtual tables and their sync triggers.
DROP TRIGGER IF EXISTS repos_au;
DROP TRIGGER IF EXISTS repos_ad;
DROP TRIGGER IF EXISTS repos_ai;
DROP TABLE  IF EXISTS repos_fts;

DROP TRIGGER IF EXISTS issues_au;
DROP TRIGGER IF EXISTS issues_ad;
DROP TRIGGER IF EXISTS issues_ai;
DROP TABLE  IF EXISTS issues_fts;
