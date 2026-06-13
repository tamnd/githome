-- 0033_org_members (sqlite, up): persisted org membership so the org member
-- check and listings stop fabricating data.

CREATE TABLE org_members (
    pk       INTEGER PRIMARY KEY AUTOINCREMENT,
    org_pk   INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    user_pk  INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    role     TEXT    NOT NULL DEFAULT 'member',
    UNIQUE (org_pk, user_pk)
);
