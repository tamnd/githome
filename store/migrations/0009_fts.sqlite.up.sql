-- 0009_fts (sqlite, up): FTS5 virtual tables for issue and repository full-text
-- search. The content= option points at the source table so the FTS index stores
-- only the posting lists; triggers keep it in sync with INSERT/UPDATE/DELETE.
-- Soft-deleted rows (deleted_at IS NOT NULL) are not purged from the index; the
-- main queries' deleted_at IS NULL predicate excludes them after the rowid join.

CREATE VIRTUAL TABLE issues_fts USING fts5(
    title, body,
    content='issues', content_rowid='pk'
);

CREATE TRIGGER issues_ai AFTER INSERT ON issues BEGIN
    INSERT INTO issues_fts(rowid, title, body)
        VALUES (new.pk, new.title, COALESCE(new.body, ''));
END;

CREATE TRIGGER issues_ad AFTER DELETE ON issues BEGIN
    INSERT INTO issues_fts(issues_fts, rowid, title, body)
        VALUES ('delete', old.pk, old.title, COALESCE(old.body, ''));
END;

-- Only title and body edits need reindexing; deleted_at updates do not.
CREATE TRIGGER issues_au AFTER UPDATE OF title, body ON issues BEGIN
    INSERT INTO issues_fts(issues_fts, rowid, title, body)
        VALUES ('delete', old.pk, old.title, COALESCE(old.body, ''));
    INSERT INTO issues_fts(rowid, title, body)
        VALUES (new.pk, new.title, COALESCE(new.body, ''));
END;

-- Backfill rows already in the table (excluding soft-deleted ones to keep the
-- index compact; they would be filtered out by the main query anyway).
INSERT INTO issues_fts(rowid, title, body)
    SELECT pk, title, COALESCE(body, '') FROM issues WHERE deleted_at IS NULL;

-- Repository FTS: name and description.
CREATE VIRTUAL TABLE repos_fts USING fts5(
    name, description,
    content='repositories', content_rowid='pk'
);

CREATE TRIGGER repos_ai AFTER INSERT ON repositories BEGIN
    INSERT INTO repos_fts(rowid, name, description)
        VALUES (new.pk, new.name, COALESCE(new.description, ''));
END;

CREATE TRIGGER repos_ad AFTER DELETE ON repositories BEGIN
    INSERT INTO repos_fts(repos_fts, rowid, name, description)
        VALUES ('delete', old.pk, old.name, COALESCE(old.description, ''));
END;

CREATE TRIGGER repos_au AFTER UPDATE OF name, description ON repositories BEGIN
    INSERT INTO repos_fts(repos_fts, rowid, name, description)
        VALUES ('delete', old.pk, old.name, COALESCE(old.description, ''));
    INSERT INTO repos_fts(rowid, name, description)
        VALUES (new.pk, new.name, COALESCE(new.description, ''));
END;

INSERT INTO repos_fts(rowid, name, description)
    SELECT pk, name, COALESCE(description, '') FROM repositories WHERE deleted_at IS NULL;
