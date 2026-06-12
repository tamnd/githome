-- 0032_protection_details (pg, up): the branch-protection toggles the
-- GitHub protection object and its sub-endpoints round-trip beyond the
-- original rule set.

ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS required_linear_history BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS block_creations BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS required_conversation_resolution BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS lock_branch BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS allow_fork_syncing BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE branch_protections ADD COLUMN IF NOT EXISTS required_signatures BOOLEAN NOT NULL DEFAULT FALSE;
