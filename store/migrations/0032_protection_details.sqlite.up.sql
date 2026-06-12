-- 0032_protection_details (sqlite, up): the branch-protection toggles the
-- GitHub protection object and its sub-endpoints round-trip beyond the
-- original rule set.

ALTER TABLE branch_protections ADD COLUMN required_linear_history INTEGER NOT NULL DEFAULT 0;
ALTER TABLE branch_protections ADD COLUMN block_creations INTEGER NOT NULL DEFAULT 0;
ALTER TABLE branch_protections ADD COLUMN required_conversation_resolution INTEGER NOT NULL DEFAULT 0;
ALTER TABLE branch_protections ADD COLUMN lock_branch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE branch_protections ADD COLUMN allow_fork_syncing INTEGER NOT NULL DEFAULT 0;
ALTER TABLE branch_protections ADD COLUMN required_signatures INTEGER NOT NULL DEFAULT 0;
