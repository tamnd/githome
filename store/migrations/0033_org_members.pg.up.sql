-- 0033_org_members (pg, up): persisted org membership so the org member
-- check and listings stop fabricating data.

CREATE TABLE IF NOT EXISTS org_members (
    pk       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    org_pk   BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    user_pk  BIGINT NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    role     TEXT   NOT NULL DEFAULT 'member',
    UNIQUE (org_pk, user_pk)
);
