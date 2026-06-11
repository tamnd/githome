-- 0017_protection_restrictions (sqlite, up)

ALTER TABLE branch_protections ADD COLUMN restrictions_enabled INTEGER NOT NULL DEFAULT 0;
