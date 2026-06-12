-- 0032_protection_details (sqlite, down)

ALTER TABLE branch_protections DROP COLUMN required_linear_history;
ALTER TABLE branch_protections DROP COLUMN block_creations;
ALTER TABLE branch_protections DROP COLUMN required_conversation_resolution;
ALTER TABLE branch_protections DROP COLUMN lock_branch;
ALTER TABLE branch_protections DROP COLUMN allow_fork_syncing;
ALTER TABLE branch_protections DROP COLUMN required_signatures;
