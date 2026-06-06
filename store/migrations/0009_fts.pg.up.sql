-- 0009_fts (postgres, up): tsvector generated columns + GIN indexes for
-- full-text search on issues and repositories. The 'simple' dictionary does
-- no stemming or stop-word removal, matching the LIKE-based behavior this
-- replaces: a term that appears in the text matches regardless of language.
-- Generated columns are maintained by Postgres automatically; no triggers needed.

ALTER TABLE issues ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', title || ' ' || COALESCE(body, ''))
) STORED;
CREATE INDEX issues_search_vector_gin ON issues USING GIN (search_vector);

ALTER TABLE repositories ADD COLUMN search_vector tsvector GENERATED ALWAYS AS (
    to_tsvector('simple', name || ' ' || COALESCE(description, ''))
) STORED;
CREATE INDEX repos_search_vector_gin ON repositories USING GIN (search_vector);
