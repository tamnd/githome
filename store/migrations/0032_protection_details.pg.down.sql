-- 0032_protection_details (pg, down)

ALTER TABLE branch_protections DROP COLUMN IF EXISTS required_linear_history;
ALTER TABLE branch_protections DROP COLUMN IF EXISTS block_creations;
ALTER TABLE branch_protections DROP COLUMN IF EXISTS required_conversation_resolution;
ALTER TABLE branch_protections DROP COLUMN IF EXISTS lock_branch;
ALTER TABLE branch_protections DROP COLUMN IF EXISTS allow_fork_syncing;
ALTER TABLE branch_protections DROP COLUMN IF EXISTS required_signatures;
