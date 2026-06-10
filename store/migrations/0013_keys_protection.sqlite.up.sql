-- 0013_keys_protection (sqlite, up): SSH/deploy keys and branch protection rules.

CREATE TABLE ssh_keys (
    pk           INTEGER PRIMARY KEY AUTOINCREMENT,
    db_id        INTEGER NOT NULL UNIQUE,
    user_pk      INTEGER NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    title        TEXT,
    key_type     TEXT    NOT NULL,
    public_key   TEXT    NOT NULL,
    fingerprint  TEXT    NOT NULL,
    read_only    INTEGER NOT NULL DEFAULT 0,
    repo_pk      INTEGER REFERENCES repositories(pk) ON DELETE CASCADE,
    last_used_at TEXT,
    created_at   TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX ssh_keys_fp_uq ON ssh_keys (fingerprint);

CREATE TABLE branch_protections (
    pk                          INTEGER PRIMARY KEY AUTOINCREMENT,
    repo_pk                     INTEGER NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    branch_pattern              TEXT    NOT NULL,
    require_pr_reviews          INTEGER NOT NULL DEFAULT 0,
    required_approving_count    INTEGER NOT NULL DEFAULT 0,
    dismiss_stale_reviews       INTEGER NOT NULL DEFAULT 0,
    require_code_owner_reviews  INTEGER NOT NULL DEFAULT 0,
    require_status_checks       INTEGER NOT NULL DEFAULT 0,
    require_branches_up_to_date INTEGER NOT NULL DEFAULT 0,
    status_check_contexts       TEXT    NOT NULL DEFAULT '[]',
    enforce_admins              INTEGER NOT NULL DEFAULT 0,
    restrictions_users          TEXT    NOT NULL DEFAULT '[]',
    restrictions_teams          TEXT    NOT NULL DEFAULT '[]',
    allow_force_pushes          INTEGER NOT NULL DEFAULT 0,
    allow_deletions             INTEGER NOT NULL DEFAULT 0,
    created_at                  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at                  TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX branch_protections_repo_pattern_uq ON branch_protections (repo_pk, branch_pattern);
