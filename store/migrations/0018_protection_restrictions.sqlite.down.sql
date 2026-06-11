-- 0017_protection_restrictions (sqlite, down)

ALTER TABLE branch_protections DROP COLUMN restrictions_enabled;
