-- 0017_protection_restrictions (postgres, up)

ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS restrictions_enabled BOOLEAN NOT NULL DEFAULT FALSE;
