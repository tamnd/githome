-- 0013_keys_protection (postgres, up): SSH/deploy keys and branch protection rules.

CREATE TABLE ssh_keys (
    pk           BIGSERIAL PRIMARY KEY,
    db_id        BIGINT    NOT NULL UNIQUE DEFAULT nextval('global_id_seq'),
    user_pk      BIGINT    NOT NULL REFERENCES users(pk) ON DELETE CASCADE,
    title        TEXT,
    key_type     TEXT      NOT NULL,
    public_key   TEXT      NOT NULL,
    fingerprint  TEXT      NOT NULL,
    read_only    BOOLEAN   NOT NULL DEFAULT FALSE,
    repo_pk      BIGINT    REFERENCES repositories(pk) ON DELETE CASCADE,
    last_used_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ssh_keys_fp_uq ON ssh_keys (fingerprint);

CREATE TABLE branch_protections (
    pk                          BIGSERIAL PRIMARY KEY,
    repo_pk                     BIGINT    NOT NULL REFERENCES repositories(pk) ON DELETE CASCADE,
    branch_pattern              TEXT      NOT NULL,
    require_pr_reviews          BOOLEAN   NOT NULL DEFAULT FALSE,
    required_approving_count    INT       NOT NULL DEFAULT 0,
    dismiss_stale_reviews       BOOLEAN   NOT NULL DEFAULT FALSE,
    require_code_owner_reviews  BOOLEAN   NOT NULL DEFAULT FALSE,
    require_status_checks       BOOLEAN   NOT NULL DEFAULT FALSE,
    require_branches_up_to_date BOOLEAN   NOT NULL DEFAULT FALSE,
    status_check_contexts       TEXT[]    NOT NULL DEFAULT '{}',
    enforce_admins              BOOLEAN   NOT NULL DEFAULT FALSE,
    restrictions_users          TEXT[]    NOT NULL DEFAULT '{}',
    restrictions_teams          TEXT[]    NOT NULL DEFAULT '{}',
    allow_force_pushes          BOOLEAN   NOT NULL DEFAULT FALSE,
    allow_deletions             BOOLEAN   NOT NULL DEFAULT FALSE,
    created_at                  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at                  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX branch_protections_repo_pattern_uq ON branch_protections (repo_pk, branch_pattern);
