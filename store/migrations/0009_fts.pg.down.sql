-- 0009_fts (postgres, down): drop generated tsvector columns and their indexes.
DROP INDEX IF EXISTS repos_search_vector_gin;
ALTER TABLE repositories DROP COLUMN IF EXISTS search_vector;

DROP INDEX IF EXISTS issues_search_vector_gin;
ALTER TABLE issues DROP COLUMN IF EXISTS search_vector;
