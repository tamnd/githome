-- 0020_code_search (postgres, up): code documents with a generated tsvector and
-- GIN index, the Postgres half of the FTS5 table SQLite uses. The 'simple'
-- dictionary does no stemming or stop-word removal, matching FTS5's tokenizer
-- closely enough that both dialects answer the same queries. The indexer
-- replaces a repository's rows wholesale on reindex.
--
-- The path is split on its punctuation before tokenizing so a query for one
-- segment ("logo") finds "assets/logo.png". Postgres's text parser otherwise
-- keeps a slash-joined path as a single "file" token, which FTS5's unicode61
-- tokenizer would have split on every non-word character. Replacing those
-- characters with spaces lines the two dialects up.

CREATE TABLE code_documents (
    repo_pk       BIGINT NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    path          TEXT   NOT NULL,
    sha           TEXT   NOT NULL,
    content       TEXT   NOT NULL DEFAULT '',
    search_vector tsvector GENERATED ALWAYS AS (
        to_tsvector('simple', regexp_replace(path, '[^A-Za-z0-9]+', ' ', 'g') || ' ' || content)
    ) STORED,
    PRIMARY KEY (repo_pk, path)
);
CREATE INDEX code_documents_search_vector_gin ON code_documents USING GIN (search_vector);

CREATE TABLE code_index_state (
    repo_pk    BIGINT      PRIMARY KEY REFERENCES repositories(pk) ON DELETE CASCADE,
    head_sha   TEXT        NOT NULL,
    truncated  BOOLEAN     NOT NULL DEFAULT FALSE,
    indexed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
