-- Migration 001: Initial schema
-- Canonical baseline for all SesameFS tables.
-- Includes every column that exists in production (including columns that were
-- historically added via ALTER TABLE — they are baked in here for clean installs).
--
-- Existing deployments that intentionally reuse this baseline will re-run the
-- CREATE TABLE / CREATE INDEX statements safely where the schema already exists.
-- Fresh installs: this file creates the complete schema in one pass.

-- ─── Core / Identity ─────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS organizations (
    org_id                    UUID PRIMARY KEY,
    name                      TEXT,
    settings                  MAP<TEXT, TEXT>,
    storage_quota             BIGINT,
    storage_used              BIGINT,
    chunking_polynomial       BIGINT,
    storage_config            MAP<TEXT, TEXT>,
    created_at                TIMESTAMP,
    deleted_at                TIMESTAMP,
    status                    TEXT,
    traffic_quota             BIGINT,
    traffic_upload_quota      BIGINT,
    traffic_download_quota    BIGINT,
    max_users                 INT,
    plan                      TEXT,
    billing_cycle             TEXT,
    quota_policy              TEXT,
    current_period_started_at TIMESTAMP,
    current_period_ends_at    TIMESTAMP
);

CREATE TABLE IF NOT EXISTS users (
    org_id                 UUID,
    user_id                UUID,
    email                  TEXT,
    name                   TEXT,
    role                   TEXT,
    oidc_sub               TEXT,
    quota_bytes            BIGINT,
    used_bytes             BIGINT,
    created_at             TIMESTAMP,
    deleted_at             TIMESTAMP,
    status                 TEXT,
    traffic_upload_quota   BIGINT,
    traffic_download_quota BIGINT,
    last_login_at          TIMESTAMP,
    PRIMARY KEY ((org_id), user_id)
);

CREATE TABLE IF NOT EXISTS users_by_email (
    email   TEXT PRIMARY KEY,
    user_id UUID,
    org_id  UUID
);

CREATE TABLE IF NOT EXISTS users_by_oidc (
    oidc_issuer TEXT,
    oidc_sub    TEXT,
    user_id     UUID,
    org_id      UUID,
    PRIMARY KEY ((oidc_issuer), oidc_sub)
);

-- ─── Libraries ───────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS libraries (
    org_id            UUID,
    library_id        UUID,
    owner_id          UUID,
    name              TEXT,
    description       TEXT,
    encrypted         BOOLEAN,
    enc_version       INT,
    magic             TEXT,
    random_key        TEXT,
    salt              TEXT,
    magic_strong      TEXT,
    random_key_strong TEXT,
    root_commit_id    TEXT,
    head_commit_id    TEXT,
    storage_class     TEXT,
    size_bytes        BIGINT,
    file_count        BIGINT,
    version_ttl_days  INT,
    auto_delete_days  INT,
    deleted_at        TIMESTAMP,
    deleted_by        UUID,
    created_at        TIMESTAMP,
    updated_at        TIMESTAMP,
    PRIMARY KEY ((org_id), library_id)
);

-- Lookup table: find library by library_id without knowing org_id.
-- Dual-write: keep in sync with libraries on every write.
CREATE TABLE IF NOT EXISTS libraries_by_id (
    library_id        UUID PRIMARY KEY,
    org_id            UUID,
    owner_id          UUID,
    name              TEXT,
    head_commit_id    TEXT,
    encrypted         BOOLEAN,
    enc_version       INT,
    magic             TEXT,
    random_key        TEXT,
    salt              TEXT,
    magic_strong      TEXT,
    random_key_strong TEXT
);

-- 90-day TTL: ensures GC scanner can resolve org_id for orphaned blocks
-- before the record expires.
CREATE TABLE IF NOT EXISTS deleted_libraries (
    library_id UUID PRIMARY KEY,
    org_id     UUID,
    deleted_at TIMESTAMP
) WITH default_time_to_live = 7776000;

-- Admin/org-admin library read models.
CREATE TABLE IF NOT EXISTS libraries_by_owner (
    org_id         UUID,
    owner_id       UUID,
    updated_at     TIMESTAMP,
    library_id     UUID,
    name           TEXT,
    encrypted      BOOLEAN,
    storage_class  TEXT,
    size_bytes     BIGINT,
    file_count     BIGINT,
    deleted_at     TIMESTAMP,
    PRIMARY KEY ((org_id, owner_id), updated_at, library_id)
) WITH CLUSTERING ORDER BY (updated_at DESC, library_id ASC);

CREATE TABLE IF NOT EXISTS libraries_by_org_updated (
    org_id         UUID,
    updated_at     TIMESTAMP,
    library_id     UUID,
    owner_id       UUID,
    owner_email    TEXT,
    owner_name     TEXT,
    name           TEXT,
    encrypted      BOOLEAN,
    storage_class  TEXT,
    size_bytes     BIGINT,
    file_count     BIGINT,
    deleted_at     TIMESTAMP,
    PRIMARY KEY ((org_id), updated_at, library_id)
) WITH CLUSTERING ORDER BY (updated_at DESC, library_id ASC);

CREATE TABLE IF NOT EXISTS libraries_admin_global_by_updated (
    bucket_day     TEXT,
    updated_at     TIMESTAMP,
    org_id         UUID,
    library_id     UUID,
    owner_id       UUID,
    owner_email    TEXT,
    owner_name     TEXT,
    name           TEXT,
    encrypted      BOOLEAN,
    storage_class  TEXT,
    size_bytes     BIGINT,
    file_count     BIGINT,
    deleted_at     TIMESTAMP,
    PRIMARY KEY ((bucket_day), updated_at, org_id, library_id)
) WITH CLUSTERING ORDER BY (updated_at DESC, org_id ASC, library_id ASC);

CREATE TABLE IF NOT EXISTS libraries_deleted_by_org (
    org_id         UUID,
    deleted_at     TIMESTAMP,
    library_id     UUID,
    owner_id       UUID,
    owner_email    TEXT,
    owner_name     TEXT,
    name           TEXT,
    encrypted      BOOLEAN,
    size_bytes     BIGINT,
    PRIMARY KEY ((org_id), deleted_at, library_id)
) WITH CLUSTERING ORDER BY (deleted_at DESC, library_id ASC);

CREATE TABLE IF NOT EXISTS library_admin_global_buckets (
    bucket_day TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS library_admin_projection_state (
    library_id UUID PRIMARY KEY,
    org_id     UUID,
    owner_id   UUID,
    updated_at TIMESTAMP,
    deleted_at TIMESTAMP
);

-- ─── File System ─────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS commits (
    library_id  UUID,
    commit_id   TEXT,
    parent_id   TEXT,
    root_fs_id  TEXT,
    creator_id  UUID,
    description TEXT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((library_id), commit_id)
);

CREATE TABLE IF NOT EXISTS fs_objects (
    library_id  UUID,
    fs_id       TEXT,
    obj_type    TEXT,
    obj_name    TEXT,
    full_path   TEXT,
    dir_entries TEXT,
    block_ids   LIST<TEXT>,
    size_bytes  BIGINT,
    mtime       BIGINT,
    PRIMARY KEY ((library_id), fs_id)
);

CREATE TABLE IF NOT EXISTS blocks (
    org_id        UUID,
    block_id      TEXT,
    size_bytes    INT,
    storage_class TEXT,
    storage_key   TEXT,
    ref_count     INT,
    created_at    TIMESTAMP,
    last_accessed TIMESTAMP,
    PRIMARY KEY ((org_id), block_id)
);

-- SHA-1 (external/Seafile client) → SHA-256 (internal) content-address mapping.
CREATE TABLE IF NOT EXISTS block_id_mappings (
    org_id      UUID,
    external_id TEXT,
    internal_id TEXT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((org_id), external_id)
);

-- Reverse lookup: internal SHA-256 → external SHA-1.
-- Avoids full org scan when deleting a block and cleaning up its mappings.
CREATE TABLE IF NOT EXISTS block_id_mappings_by_internal (
    org_id      UUID,
    internal_id TEXT,
    external_id TEXT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((org_id), internal_id, external_id)
);

-- ─── Share Links ─────────────────────────────────────────────────────────────

-- Primary lookup by token. link_type: 'share' | 'upload' | 'internal'.
CREATE TABLE IF NOT EXISTS share_links (
    link_token       TEXT PRIMARY KEY,
    link_type        TEXT,
    org_id           UUID,
    library_id       UUID,
    file_path        TEXT,
    created_by       UUID,
    permission       TEXT,
    password_hash    TEXT,
    expires_at       TIMESTAMP,
    single_use       BOOLEAN,
    active           BOOLEAN,
    view_count       INT,
    download_count   INT,
    upload_count     INT,
    max_downloads    INT,
    last_accessed_at TIMESTAMP,
    created_at       TIMESTAMP
);

-- "My links" view — ordered newest-first per user.
CREATE TABLE IF NOT EXISTS share_links_by_creator (
    org_id           UUID,
    created_by       UUID,
    created_at       TIMESTAMP,
    link_token       TEXT,
    link_type        TEXT,
    library_id       UUID,
    file_path        TEXT,
    permission       TEXT,
    expires_at       TIMESTAMP,
    single_use       BOOLEAN,
    active           BOOLEAN,
    view_count       INT,
    download_count   INT,
    upload_count     INT,
    max_downloads    INT,
    has_password     BOOLEAN,
    last_accessed_at TIMESTAMP,
    PRIMARY KEY ((org_id, created_by), created_at, link_token)
) WITH CLUSTERING ORDER BY (created_at DESC, link_token ASC);

-- Orphan cleanup: enumerate all links for a library on permanent delete.
CREATE TABLE IF NOT EXISTS share_links_by_library (
    org_id     UUID,
    library_id UUID,
    link_token TEXT,
    link_type  TEXT,
    created_by UUID,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, library_id), link_token)
);

-- Admin/global read models for share and upload links.
CREATE TABLE IF NOT EXISTS admin_links_by_created (
    link_type      TEXT,
    bucket_day     TEXT,
    created_at     TIMESTAMP,
    org_id         UUID,
    link_token     TEXT,
    library_id     UUID,
    repo_name      TEXT,
    file_path      TEXT,
    obj_name       TEXT,
    created_by     UUID,
    creator_email  TEXT,
    creator_name   TEXT,
    permission     TEXT,
    expires_at     TIMESTAMP,
    has_password   BOOLEAN,
    active         BOOLEAN,
    view_count     INT,
    upload_count   INT,
    PRIMARY KEY ((link_type, bucket_day), created_at, org_id, link_token)
) WITH CLUSTERING ORDER BY (created_at DESC, org_id ASC, link_token ASC);

CREATE TABLE IF NOT EXISTS admin_link_buckets (
    link_type   TEXT,
    bucket_day  TEXT,
    PRIMARY KEY (link_type, bucket_day)
) WITH CLUSTERING ORDER BY (bucket_day DESC);

CREATE TABLE IF NOT EXISTS admin_links_by_org_created (
    org_id         UUID,
    link_type      TEXT,
    bucket_day     TEXT,
    created_at     TIMESTAMP,
    link_token     TEXT,
    library_id     UUID,
    repo_name      TEXT,
    file_path      TEXT,
    obj_name       TEXT,
    created_by     UUID,
    creator_email  TEXT,
    creator_name   TEXT,
    permission     TEXT,
    expires_at     TIMESTAMP,
    has_password   BOOLEAN,
    active         BOOLEAN,
    view_count     INT,
    upload_count   INT,
    PRIMARY KEY ((org_id, link_type, bucket_day), created_at, link_token)
) WITH CLUSTERING ORDER BY (created_at DESC, link_token ASC);

CREATE TABLE IF NOT EXISTS admin_link_buckets_by_org (
    org_id       UUID,
    link_type    TEXT,
    bucket_day   TEXT,
    PRIMARY KEY ((org_id, link_type), bucket_day)
) WITH CLUSTERING ORDER BY (bucket_day DESC);

-- Org/type counters for share/upload-link enforcement.
CREATE TABLE IF NOT EXISTS admin_link_counts_by_org (
    org_id     UUID,
    link_type  TEXT,
    link_count COUNTER,
    PRIMARY KEY (org_id, link_type)
);

-- ─── Shares (user-to-user) ───────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS shares (
    library_id     UUID,
    share_id       UUID,
    org_id         UUID,
    shared_by      UUID,
    shared_by_email TEXT,
    shared_by_name  TEXT,
    shared_to      UUID,
    shared_to_type TEXT,
    repo_name      TEXT,
    encrypted      BOOLEAN,
    size_bytes     BIGINT,
    permission     TEXT,
    created_at     TIMESTAMP,
    expires_at     TIMESTAMP,
    PRIMARY KEY ((library_id), share_id)
);

CREATE TABLE IF NOT EXISTS shares_by_user (
    shared_to      UUID,
    library_id     UUID,
    shared_to_type TEXT,
    permission     TEXT,
    shared_by      UUID,
    created_at     TIMESTAMP,
    PRIMARY KEY ((shared_to), library_id)
);

-- Admin/org-admin share read models.
CREATE TABLE IF NOT EXISTS shares_by_group (
    org_id            UUID,
    group_id          UUID,
    created_at        TIMESTAMP,
    library_id        UUID,
    share_id          UUID,
    permission        TEXT,
    shared_by         UUID,
    shared_by_email   TEXT,
    shared_by_name    TEXT,
    repo_name         TEXT,
    encrypted         BOOLEAN,
    size_bytes        BIGINT,
    PRIMARY KEY ((org_id, group_id), created_at, library_id, share_id)
) WITH CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC);

CREATE TABLE IF NOT EXISTS shares_by_user_org (
    org_id            UUID,
    user_id           UUID,
    created_at        TIMESTAMP,
    library_id        UUID,
    share_id          UUID,
    permission        TEXT,
    shared_by         UUID,
    shared_by_email   TEXT,
    shared_by_name    TEXT,
    repo_name         TEXT,
    encrypted         BOOLEAN,
    size_bytes        BIGINT,
    PRIMARY KEY ((org_id, user_id), created_at, library_id, share_id)
) WITH CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC);

-- User-facing share lookup tables.
CREATE TABLE IF NOT EXISTS shares_by_creator (
    org_id         UUID,
    shared_by      UUID,
    created_at     TIMESTAMP,
    library_id     UUID,
    share_id       UUID,
    shared_to      UUID,
    shared_to_type TEXT,
    permission     TEXT,
    expires_at     TIMESTAMP,
    PRIMARY KEY ((org_id, shared_by), created_at, library_id, share_id)
) WITH CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC);

CREATE TABLE IF NOT EXISTS shares_by_recipient (
    org_id         UUID,
    shared_to_type TEXT,
    shared_to      UUID,
    created_at     TIMESTAMP,
    library_id     UUID,
    share_id       UUID,
    shared_by      UUID,
    permission     TEXT,
    expires_at     TIMESTAMP,
    PRIMARY KEY ((org_id, shared_to_type, shared_to), created_at, library_id, share_id)
) WITH CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC);

-- Reusable per-user permission presets for share dialogs.
CREATE TABLE IF NOT EXISTS custom_share_permissions (
    permission_id   UUID PRIMARY KEY,
    creator_id      UUID,
    name            TEXT,
    description     TEXT,
    permission_json TEXT,
    created_at      TIMESTAMP
);

CREATE TABLE IF NOT EXISTS custom_share_permissions_by_user (
    creator_id      UUID,
    permission_id   UUID,
    name            TEXT,
    description     TEXT,
    permission_json TEXT,
    created_at      TIMESTAMP,
    PRIMARY KEY ((creator_id), permission_id)
);

-- ─── Sessions & Auth ─────────────────────────────────────────────────────────

-- token_hash is the SHA-256 of the raw session token (raw tokens are never stored).
CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    UUID,
    org_id     UUID,
    email      TEXT,
    role       TEXT,
    created_at TIMESTAMP,
    expires_at TIMESTAMP
);

-- Reverse index for bulk invalidation when a user is deactivated or deleted.
CREATE TABLE IF NOT EXISTS sessions_by_user (
    org_id     UUID,
    user_id    UUID,
    token_hash TEXT,
    PRIMARY KEY ((org_id, user_id), token_hash)
);

-- Per-library API tokens for programmatic access.
CREATE TABLE IF NOT EXISTS repo_api_tokens (
    repo_id      UUID,
    app_name     TEXT,
    api_token    TEXT,
    permission   TEXT,
    generated_by UUID,
    created_at   TIMESTAMP,
    PRIMARY KEY ((repo_id), app_name)
);

-- Reverse lookup: O(1) authentication given a raw token string.
CREATE TABLE IF NOT EXISTS repo_api_tokens_by_token (
    api_token    TEXT PRIMARY KEY,
    repo_id      UUID,
    app_name     TEXT,
    permission   TEXT,
    generated_by UUID
);

-- Stateless file-operation tokens (Cassandra TTL handles expiration).
-- "token" is quoted because it is a reserved CQL keyword.
CREATE TABLE IF NOT EXISTS access_tokens (
    "token"    TEXT PRIMARY KEY,
    token_type TEXT,
    org_id     UUID,
    repo_id    UUID,
    file_path  TEXT,
    user_id    UUID,
    created_at TIMESTAMP,
    source     TEXT
);

-- ─── Groups ──────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS groups (
    org_id          UUID,
    group_id        UUID,
    name            TEXT,
    creator_id      UUID,
    parent_group_id UUID,
    is_department   BOOLEAN,
    created_at      TIMESTAMP,
    updated_at      TIMESTAMP,
    PRIMARY KEY ((org_id), group_id)
);

CREATE TABLE IF NOT EXISTS group_members (
    group_id UUID,
    user_id  UUID,
    role     TEXT,
    added_at TIMESTAMP,
    PRIMARY KEY ((group_id), user_id)
);

-- "My groups" lookup — efficient per-user query without ALLOW FILTERING.
CREATE TABLE IF NOT EXISTS groups_by_member (
    org_id     UUID,
    user_id    UUID,
    group_id   UUID,
    group_name TEXT,
    role       TEXT,
    added_at   TIMESTAMP,
    PRIMARY KEY ((org_id, user_id), group_id)
);

CREATE TABLE IF NOT EXISTS groups_by_id (
    group_id UUID PRIMARY KEY,
    org_id   UUID,
    name     TEXT
);

-- Sys-admin group read model.
CREATE TABLE IF NOT EXISTS group_admin_global_buckets (
    bucket_day TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS groups_admin_global_by_created (
    bucket_day       TEXT,
    created_at       TIMESTAMP,
    org_id           UUID,
    group_id         UUID,
    name             TEXT,
    creator_id       UUID,
    owner_email      TEXT,
    owner_name       TEXT,
    parent_group_id  UUID,
    is_department    BOOLEAN,
    PRIMARY KEY ((bucket_day), created_at, org_id, group_id)
) WITH CLUSTERING ORDER BY (created_at DESC, org_id ASC, group_id ASC);

-- Sys-admin organization read models.
CREATE TABLE IF NOT EXISTS organization_admin_buckets (
    bucket_day TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS organization_admin_buckets_by_status (
    status     TEXT,
    bucket_day TEXT,
    PRIMARY KEY ((status), bucket_day)
) WITH CLUSTERING ORDER BY (bucket_day DESC);

CREATE TABLE IF NOT EXISTS organizations_admin_by_created (
    bucket_day     TEXT,
    created_at     TIMESTAMP,
    org_id         UUID,
    name           TEXT,
    owner_email    TEXT,
    owner_name     TEXT,
    status         TEXT,
    plan           TEXT,
    storage_quota  BIGINT,
    deleted_at     TIMESTAMP,
    users_count    INT,
    PRIMARY KEY ((bucket_day), created_at, org_id)
) WITH CLUSTERING ORDER BY (created_at DESC, org_id ASC);

CREATE TABLE IF NOT EXISTS organizations_admin_by_status_created (
    status         TEXT,
    bucket_day     TEXT,
    created_at     TIMESTAMP,
    org_id         UUID,
    name           TEXT,
    owner_email    TEXT,
    owner_name     TEXT,
    plan           TEXT,
    storage_quota  BIGINT,
    deleted_at     TIMESTAMP,
    users_count    INT,
    PRIMARY KEY ((status, bucket_day), created_at, org_id)
) WITH CLUSTERING ORDER BY (created_at DESC, org_id ASC);

CREATE TABLE IF NOT EXISTS organization_admin_projection_state (
    org_id      UUID PRIMARY KEY,
    status      TEXT,
    created_at  TIMESTAMP
);

-- Sys-admin user read models.
CREATE TABLE IF NOT EXISTS user_admin_global_buckets (
    bucket_day TEXT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS user_admin_buckets_by_status (
    status     TEXT,
    bucket_day TEXT,
    PRIMARY KEY ((status), bucket_day)
) WITH CLUSTERING ORDER BY (bucket_day DESC);

CREATE TABLE IF NOT EXISTS users_admin_global_by_created (
    bucket_day              TEXT,
    created_at              TIMESTAMP,
    org_id                  UUID,
    user_id                 UUID,
    email                   TEXT,
    name                    TEXT,
    role                    TEXT,
    status                  TEXT,
    quota_bytes             BIGINT,
    quota_usage             BIGINT,
    last_login_at           TIMESTAMP,
    PRIMARY KEY ((bucket_day), created_at, org_id, user_id)
) WITH CLUSTERING ORDER BY (created_at DESC, org_id ASC, user_id ASC);

CREATE TABLE IF NOT EXISTS users_admin_global_by_status_created (
    status                  TEXT,
    bucket_day              TEXT,
    created_at              TIMESTAMP,
    org_id                  UUID,
    user_id                 UUID,
    email                   TEXT,
    name                    TEXT,
    role                    TEXT,
    quota_bytes             BIGINT,
    quota_usage             BIGINT,
    last_login_at           TIMESTAMP,
    PRIMARY KEY ((status, bucket_day), created_at, org_id, user_id)
) WITH CLUSTERING ORDER BY (created_at DESC, org_id ASC, user_id ASC);

CREATE TABLE IF NOT EXISTS user_admin_projection_state (
    user_id      UUID PRIMARY KEY,
    org_id       UUID,
    status       TEXT,
    created_at   TIMESTAMP
);

-- ─── Features ────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS starred_files (
    user_id    UUID,
    repo_id    UUID,
    path       TEXT,
    starred_at TIMESTAMP,
    PRIMARY KEY ((user_id), repo_id, path)
);

-- Secondary index required by GC cleanup: DELETE FROM starred_files WHERE repo_id = ?
-- (store_cassandra.go DeleteStarredFilesByLibrary). Without this index that query
-- requires ALLOW FILTERING and will fail on any non-trivial dataset.
CREATE INDEX IF NOT EXISTS starred_files_by_repo ON starred_files (repo_id);

CREATE TABLE IF NOT EXISTS locked_files (
    repo_id   UUID,
    path      TEXT,
    locked_by UUID,
    locked_at TIMESTAMP,
    PRIMARY KEY ((repo_id), path)
);

CREATE TABLE IF NOT EXISTS monitored_repos (
    user_id      UUID,
    repo_id      UUID,
    monitored_at TIMESTAMP,
    PRIMARY KEY ((user_id), repo_id)
);

CREATE TABLE IF NOT EXISTS restore_jobs (
    org_id         UUID,
    job_id         UUID,
    library_id     UUID,
    block_ids      LIST<TEXT>,
    glacier_job_id TEXT,
    status         TEXT,
    requested_at   TIMESTAMP,
    completed_at   TIMESTAMP,
    expires_at     TIMESTAMP,
    PRIMARY KEY ((org_id), library_id, job_id)
);

-- OnlyOffice document-key → file mapping for edit callbacks.
CREATE TABLE IF NOT EXISTS onlyoffice_doc_keys (
    doc_key    TEXT PRIMARY KEY,
    user_id    TEXT,
    repo_id    TEXT,
    file_path  TEXT,
    created_at TIMESTAMP
);

-- Hostname → org routing for multi-tenant deployments.
CREATE TABLE IF NOT EXISTS hostname_mappings (
    hostname   TEXT PRIMARY KEY,
    org_id     UUID,
    settings   MAP<TEXT, TEXT>,
    created_at TIMESTAMP,
    updated_at TIMESTAMP
);

-- ─── Tags ────────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS repo_tags (
    repo_id    UUID,
    tag_id     INT,
    name       TEXT,
    color      TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((repo_id), tag_id)
);

CREATE TABLE IF NOT EXISTS file_tags (
    repo_id     UUID,
    file_path   TEXT,
    tag_id      INT,
    file_tag_id INT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((repo_id), file_path, tag_id)
);

-- Lookup by file_tag_id to support DELETE /file-tags/:id without ALLOW FILTERING.
CREATE TABLE IF NOT EXISTS file_tags_by_id (
    repo_id     UUID,
    file_tag_id INT,
    file_path   TEXT,
    tag_id      INT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((repo_id), file_tag_id)
);

CREATE TABLE IF NOT EXISTS repo_tag_counters (
    repo_id     UUID PRIMARY KEY,
    next_tag_id INT
);

CREATE TABLE IF NOT EXISTS file_tag_counters (
    repo_id          UUID PRIMARY KEY,
    next_file_tag_id INT
);

-- COUNTER table: number of files tagged with each tag.
CREATE TABLE IF NOT EXISTS repo_tag_file_counts (
    repo_id    UUID,
    tag_id     INT,
    file_count COUNTER,
    PRIMARY KEY ((repo_id), tag_id)
);

-- ─── GC ──────────────────────────────────────────────────────────────────────

-- 7-day TTL auto-cleans stale items; ordered ASC for worker FIFO processing.
CREATE TABLE IF NOT EXISTS gc_queue (
    org_id        UUID,
    queued_at     TIMESTAMP,
    item_type     TEXT,
    item_id       TEXT,
    library_id    UUID,
    storage_class TEXT,
    retry_count   INT,
    PRIMARY KEY ((org_id), queued_at, item_type, item_id)
) WITH default_time_to_live = 604800
  AND CLUSTERING ORDER BY (queued_at ASC);

CREATE TABLE IF NOT EXISTS gc_stats (
    stat_key   TEXT PRIMARY KEY,
    stat_value TEXT,
    updated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS gc_queue_stats (
    stat_key   TEXT PRIMARY KEY,
    queue_size COUNTER
);

-- 48-hour TTL prevents double-decrement of ref_counts if the queue is slow.
CREATE TABLE IF NOT EXISTS gc_processed_items (
    task_id UUID PRIMARY KEY
) WITH default_time_to_live = 172800;

-- ─── Audit ───────────────────────────────────────────────────────────────────

-- 365-day TTL; partitioned by org for efficient recent-first queries.
CREATE TABLE IF NOT EXISTS audit_log (
    org_id      UUID,
    timestamp   TIMESTAMP,
    action      TEXT,
    target_type TEXT,
    target_id   TEXT,
    actor_id    TEXT,
    details     TEXT,
    PRIMARY KEY ((org_id), timestamp, action, target_id)
) WITH CLUSTERING ORDER BY (timestamp DESC, action ASC, target_id ASC)
  AND default_time_to_live = 31536000;

-- ─── Traffic & Storage Counters ──────────────────────────────────────────────

-- Daily granularity per org/user/type — drives statistics API graphs.
CREATE TABLE IF NOT EXISTS traffic_counters (
    org_id            UUID,
    month             TEXT,
    day               DATE,
    user_id           UUID,
    traffic_type      TEXT,
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month), day, user_id, traffic_type)
);

-- Monthly aggregates — single partition read for fast quota enforcement.
CREATE TABLE IF NOT EXISTS traffic_monthly (
    org_id            UUID,
    month             TEXT,
    scope             TEXT,
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month), scope)
);

-- Per-period aggregates keyed by the org's effective quota period start.
-- Canonical source for billing-cycle quota enforcement.
CREATE TABLE IF NOT EXISTS traffic_period_usage (
    org_id            UUID,
    period_started_at TIMESTAMP,
    scope             TEXT,
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, period_started_at), scope)
);

-- Storage counters: running total (day = 1970-01-01) + daily deltas for graphs.
-- Ordered DESC so the running-total row (epoch date) sorts last, daily deltas first.
CREATE TABLE IF NOT EXISTS storage_counters (
    scope      TEXT,
    day        DATE,
    bytes_used COUNTER,
    file_count COUNTER,
    PRIMARY KEY ((scope), day)
) WITH CLUSTERING ORDER BY (day DESC);
