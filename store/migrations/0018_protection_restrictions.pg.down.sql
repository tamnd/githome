-- 0017_protection_restrictions (postgres, down)

ALTER TABLE branch_protections DROP COLUMN IF EXISTS restrictions_enabled;
