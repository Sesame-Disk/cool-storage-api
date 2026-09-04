-- Migration 001: Initial schema
-- Canonical baseline for all SesameFS tables.
-- In design mode this file is the single source of truth for the full schema:
-- all known columns, reverse-lookup projections, and read models required for a
-- clean boot are baked in here directly instead of being layered through
-- historical ALTERs. Reverse lookups use purpose-built projection tables rather
-- than secondary indexes (which force scatter-gather reads and degrade in
-- multi-region clusters).
--
-- Existing deployments that intentionally reuse this baseline will re-run the
-- CREATE TABLE statements safely where the schema already exists.
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

-- Mirror of soft-deleted orgs used by GC so the scanner only touches deleted
-- rows instead of scanning the full organizations table.
CREATE TABLE IF NOT EXISTS deleted_organizations (
    org_id     UUID PRIMARY KEY,
    name       TEXT,
    deleted_at TIMESTAMP
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

-- GC discovery index for soft-deleted users. `deleted_day` is derived from the
-- canonical users.deleted_at UTC date and remains stable even if the runtime
-- user_grace_days setting changes later. Phase 10 must still revalidate the
-- canonical users row and the current grace-period config before enqueueing a
-- destructive purge.
CREATE TABLE IF NOT EXISTS gc_deleted_users_by_deleted_day (
    deleted_day DATE,
    bucket      INT,
    deleted_at  TIMESTAMP,
    org_id      UUID,
    user_id     UUID,
    PRIMARY KEY ((deleted_day, bucket), deleted_at, org_id, user_id)
) WITH CLUSTERING ORDER BY (deleted_at ASC, org_id ASC, user_id ASC);

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
    library_id    UUID PRIMARY KEY,
    org_id        UUID,
    deleted_at    TIMESTAMP,
    storage_class TEXT
) WITH default_time_to_live = 7776000;

-- GC discovery index for libraries with an active retention policy. This is a
-- rebuildable projection only: the canonical libraries row remains the source
-- of truth and the scanner must revalidate canonical state before deleting
-- commits or fs_objects. `policy_type` is currently `version_ttl` or
-- `auto_delete`; `bucket` is a fixed hash of library_id so long-lived policy
-- rows do not accumulate in a single partition.
CREATE TABLE IF NOT EXISTS gc_libraries_by_policy (
    policy_type           TEXT,
    bucket                INT,
    org_id                UUID,
    library_id            UUID,
    days                  INT,
    cached_head_commit_id TEXT,
    policy_updated_at     TIMESTAMP,
    PRIMARY KEY ((policy_type, bucket), org_id, library_id)
) WITH CLUSTERING ORDER BY (org_id ASC, library_id ASC);

-- Admin/org-admin library read models.
CREATE TABLE IF NOT EXISTS libraries_by_owner (
    org_id         UUID,
    owner_id       UUID,
    library_id     UUID,
    name           TEXT,
    encrypted      BOOLEAN,
    storage_class  TEXT,
    size_bytes     BIGINT,
    file_count     BIGINT,
    created_at     TIMESTAMP,
    updated_at     TIMESTAMP,
    deleted_at     TIMESTAMP,
    PRIMARY KEY ((org_id, owner_id), library_id)
) WITH CLUSTERING ORDER BY (library_id ASC);

CREATE TABLE IF NOT EXISTS libraries_by_org_updated (
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
    created_at     TIMESTAMP,
    updated_at     TIMESTAMP,
    deleted_at     TIMESTAMP,
    PRIMARY KEY ((org_id), library_id)
) WITH CLUSTERING ORDER BY (library_id ASC);

CREATE TABLE IF NOT EXISTS libraries_admin_global_by_updated (
    bucket_day     TEXT,
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
    created_at     TIMESTAMP,
    updated_at     TIMESTAMP,
    deleted_at     TIMESTAMP,
    PRIMARY KEY ((bucket_day), org_id, library_id)
) WITH CLUSTERING ORDER BY (org_id ASC, library_id ASC);

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

-- Per-block partition: each (org_id, block_id) is its own Paxos partition.
-- `blocks` holds immutable block metadata (size, storage class/key) plus GC
-- state. Liveness is NOT a mutable counter anymore — it lives in
-- `block_references` (a block is alive iff it has ≥1 reference row).
-- Metadata is written with INSERT ... IF NOT EXISTS (a per-block LWT): the
-- physical location (storage_class/storage_key) is chosen per library and per
-- routing region, so first-writer-wins pins ONE canonical location and reads/GC
-- resolve the backend where the block actually landed. Because the partition is
-- per (org, block), these LWTs never contend across different blocks — they are
-- NOT globally serialized. `gc_state` is set to 'deleting' by the GC worker's
-- claim LWT just before the irreversible S3 delete; `gc_claim_id` fences
-- release/finalize so only the owning delete attempt can complete that claim.
-- Writers back off when they observe `gc_state='deleting'`. See block_references
-- below.
CREATE TABLE IF NOT EXISTS blocks (
    org_id        UUID,
    block_id      TEXT,
    size_bytes    INT,
    storage_class TEXT,
    storage_key   TEXT,
    gc_state      TEXT,
    gc_claim_id   TEXT,
    gc_claimed_at TIMESTAMP,
    created_at    TIMESTAMP,
    last_accessed TIMESTAMP,
    PRIMARY KEY ((org_id, block_id))
);

-- Row-per-reference liveness model (replaces blocks.ref_count). One row per
-- (block, referrer). A block is alive iff at least one reference row exists.
-- INSERT/DELETE are idempotent and need no LWT in steady state — expensive Paxos
-- is reserved for the GC worker's final claim before deleting from S3.
--   referrer = "fs:<library_id>:<fs_id>"   permanent: an fs_object containing the block
--   referrer = "up:<operation_id>"         provisional: an in-flight upload. The
--                                          live ref remains until its TTL, including
--                                          after success; abandoned uploads are bounded
--                                          and discovered via gc_provisional_block_refs*
--                                          expiry tables.
-- Per-(org, block) partition mirrors `blocks`, so a block's reference rows and its
-- metadata never contend with another block's traffic.
CREATE TABLE IF NOT EXISTS block_references (
    org_id     UUID,
    block_id   TEXT,
    referrer   TEXT,
    library_id UUID,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, block_id), referrer)
);

-- Canonical GC candidates table. Per-block partition like `blocks` so reference
-- decrement to zero never contends with another org's GC traffic.
CREATE TABLE IF NOT EXISTS gc_block_candidates (
    org_id        UUID,
    block_id      TEXT,
    storage_class TEXT,
    candidate_at  TIMESTAMP,
    PRIMARY KEY ((org_id, block_id))
);

-- Discovery projection for `gc_block_candidates`: partition by (candidate_day,
-- bucket) so the GC scanner can enumerate today's candidates across many
-- partitions in parallel. `bucket = GCDiscoveryBucket(org_id, block_id)`.
-- Pattern mirrors `gc_share_links_by_expiry`.
CREATE TABLE IF NOT EXISTS gc_block_candidates_by_day (
    candidate_day DATE,
    bucket        INT,
    candidate_at  TIMESTAMP,
    org_id        UUID,
    block_id      TEXT,
    storage_class TEXT,
    PRIMARY KEY ((candidate_day, bucket), candidate_at, org_id, block_id)
) WITH CLUSTERING ORDER BY (candidate_at ASC, org_id ASC, block_id ASC);

-- Tracks one cleanup deadline per provisional upload referrer so abandoned
-- uploads can be recovered one `up:` row at a time without injecting still-live
-- blocks into `gc_block_candidates`.
CREATE TABLE IF NOT EXISTS gc_provisional_block_refs (
    org_id        TEXT,
    block_id      TEXT,
    referrer      TEXT,
    storage_class TEXT,
    expires_at    TIMESTAMP,
    PRIMARY KEY ((org_id, block_id), referrer)
);

-- Discovery projection for `gc_provisional_block_refs`: partition by
-- (expiry_day, bucket) so the GC scanner can enumerate expired provisional
-- refs without scanning canonical block partitions.
CREATE TABLE IF NOT EXISTS gc_provisional_block_refs_by_day (
    expiry_day    DATE,
    bucket        INT,
    expires_at    TIMESTAMP,
    org_id        TEXT,
    block_id      TEXT,
    referrer      TEXT,
    storage_class TEXT,
    PRIMARY KEY ((expiry_day, bucket), expires_at, org_id, block_id, referrer)
) WITH CLUSTERING ORDER BY (expires_at ASC, org_id ASC, block_id ASC, referrer ASC);

-- SHA-1 (external/Seafile client) → SHA-256 (internal) content-address mapping.
-- Per-(org, external) partition so concurrent uploads of different content
-- never share a Paxos partition.
CREATE TABLE IF NOT EXISTS block_id_mappings (
    org_id      UUID,
    external_id TEXT,
    internal_id TEXT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((org_id, external_id))
);

-- Reverse lookup: internal SHA-256 → external SHA-1. `external_id` is kept as
-- a clustering column because multiple external aliases can point to the same
-- internal hash and we need to iterate them together.
CREATE TABLE IF NOT EXISTS block_id_mappings_by_internal (
    org_id      UUID,
    internal_id TEXT,
    external_id TEXT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((org_id, internal_id), external_id)
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

-- GC discovery index for expired share/upload/internal links. This projection
-- stores the cleanup keys needed to remove secondary tables even if the
-- canonical share_links row is missing or stale during GC. `expiry_day` comes
-- from expires_at in UTC; `bucket` is a fixed hash of link_token.
CREATE TABLE IF NOT EXISTS gc_share_links_by_expiry (
    expiry_day DATE,
    bucket     INT,
    expires_at TIMESTAMP,
    link_token TEXT,
    org_id     UUID,
    library_id UUID,
    created_by UUID,
    created_at TIMESTAMP,
    link_type  TEXT,
    PRIMARY KEY ((expiry_day, bucket), expires_at, link_token)
) WITH CLUSTERING ORDER BY (expires_at ASC, link_token ASC);

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

-- GC discovery index for expired shares. The projection carries enough context
-- to clean up recipient/creator/group read models without depending on a fresh
-- read from the canonical shares row at delete time. `expiry_day` comes from
-- expires_at in UTC; `bucket` is a fixed hash of share_id.
CREATE TABLE IF NOT EXISTS gc_shares_by_expiry (
    expiry_day     DATE,
    bucket         INT,
    expires_at     TIMESTAMP,
    org_id         UUID,
    library_id     UUID,
    share_id       UUID,
    shared_to      UUID,
    shared_to_type TEXT,
    shared_by      UUID,
    created_at     TIMESTAMP,
    PRIMARY KEY ((expiry_day, bucket), expires_at, org_id, library_id, share_id)
) WITH CLUSTERING ORDER BY (expires_at ASC, org_id ASC, library_id ASC, share_id ASC);

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
    token_hash          TEXT PRIMARY KEY,
    user_id             UUID,
    org_id              UUID,
    email               TEXT,
    role                TEXT,
    created_at          TIMESTAMP,
    expires_at          TIMESTAMP,
    source_api_key_hash TEXT,
    api_key_scope       TEXT
);

-- Reverse index for bulk invalidation when a user is deactivated or deleted.
CREATE TABLE IF NOT EXISTS sessions_by_user (
    org_id              UUID,
    user_id             UUID,
    token_hash          TEXT,
    source_api_key_hash TEXT,
    PRIMARY KEY ((org_id, user_id), token_hash)
);

-- Reverse index for org-wide invalidation without scanning every user first.
CREATE TABLE IF NOT EXISTS sessions_by_org (
    org_id              UUID,
    token_hash          TEXT,
    user_id             UUID,
    source_api_key_hash TEXT,
    PRIMARY KEY ((org_id), token_hash)
);

-- API Keys: unified authentication for user API access, Accounts M2M, and
-- Seafile legacy client compatibility. Raw tokens are shown once on creation
-- and only their SHA-256 hashes are persisted.
CREATE TABLE IF NOT EXISTS api_keys (
    key_hash     TEXT PRIMARY KEY,
    key_prefix   TEXT,
    user_id      UUID,
    org_id       UUID,
    label        TEXT,
    scope        TEXT,
    created_at   TIMESTAMP,
    last_used_at TIMESTAMP,
    expires_at   TIMESTAMP
);

CREATE TABLE IF NOT EXISTS api_keys_by_user (
    org_id       UUID,
    user_id      UUID,
    key_hash     TEXT,
    key_prefix   TEXT,
    label        TEXT,
    scope        TEXT,
    created_at   TIMESTAMP,
    last_used_at TIMESTAMP,
    expires_at   TIMESTAMP,
    PRIMARY KEY ((org_id, user_id), created_at)
) WITH CLUSTERING ORDER BY (created_at DESC);

CREATE TABLE IF NOT EXISTS sessions_by_api_key (
    api_key_hash TEXT,
    token_hash   TEXT,
    org_id       UUID,
    user_id      UUID,
    PRIMARY KEY ((api_key_hash), token_hash)
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
    source     TEXT,
    replace_existing BOOLEAN
);

-- Durable pending-cleanup rows for OnlyOffice save callbacks.
-- This tracks materialized blocks between block ref registration and
-- successful library-head publication so later processes can reconcile stale
-- pending saves conservatively.
--
-- The 7-day default_time_to_live is a safety net only. Normal cleanup happens
-- on the order of minutes via reconcileStaleOnlyOfficePendingBlocks
-- (onlyOfficePendingBlockStaleAfter = 5 minutes). The TTL exists so that if
-- both the inline save trigger and the GC scanner phase fail for an extended
-- period, rows do not accumulate without bound.
CREATE TABLE IF NOT EXISTS onlyoffice_pending_blocks (
    org_id            UUID,
    operation_id      TEXT,
    repo_id           UUID,
    file_path         TEXT,
    internal_block_id TEXT,
    external_block_id TEXT,
    storage_class     TEXT,
    publish_commit_id TEXT,
    created_at        TIMESTAMP,
    updated_at        TIMESTAMP,
    PRIMARY KEY ((org_id), operation_id)
) WITH default_time_to_live = 604800;

-- Durable queue for post-HEAD block-reference promotion/cleanup repairs.
-- Rows are inserted before the library HEAD CAS so later processes can resume
-- recovery after crashes, restarts, or redeploys. `staged_block_ids` stores
-- the exact IDs originally used in the pub:<commit> rows; this must not be
-- reconstructed from fs_objects because different publish surfaces do not all
-- stage the same identifier shape.
CREATE TABLE IF NOT EXISTS published_block_reference_repairs (
    bucket           INT,
    org_id           UUID,
    repo_id          UUID,
    commit_id        TEXT,
    fs_id            TEXT,
    staged_block_ids LIST<TEXT>,
    created_at       TIMESTAMP,
    lease_expires_at TIMESTAMP,
    PRIMARY KEY ((bucket), org_id, repo_id, commit_id, fs_id)
) WITH default_time_to_live = 0;

-- Tracks one or more in-flight publish owners per materialized fs_object row.
-- Rows exist from pre-commit fs_object persistence until the attempt is cleaned
-- up or the published commit is visible and the owner is cleared. Retain these
-- rows for 35 days so recovery still works after extended repair/sweeper
-- downtime.
CREATE TABLE IF NOT EXISTS pending_published_fs_objects (
    repo_id    UUID,
    fs_id      TEXT,
    owner_id   TEXT,
    created_at TIMESTAMP,
    org_id     UUID,
    attempt_id TEXT,
    block_ids  LIST<TEXT>,
    PRIMARY KEY ((repo_id, fs_id), owner_id)
) WITH default_time_to_live = 3024000;

-- Discovery projection for stale pending fs_object owners. Successful publishes
-- delete these rows immediately; the remaining rows are abandoned/in-flight
-- attempts that need conservative cleanup. Lean key-only projection: owner
-- metadata (org_id/attempt_id/block_ids) lives on the canonical
-- pending_published_fs_objects row and is hydrated from there during cleanup.
CREATE TABLE IF NOT EXISTS pending_published_fs_objects_by_day (
    created_day DATE,
    bucket      INT,
    created_at  TIMESTAMP,
    repo_id     UUID,
    fs_id       TEXT,
    owner_id    TEXT,
    PRIMARY KEY ((created_day, bucket), created_at, repo_id, fs_id, owner_id)
) WITH CLUSTERING ORDER BY (created_at ASC, repo_id ASC, fs_id ASC, owner_id ASC)
    AND default_time_to_live = 3024000;

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

-- Reverse-lookup projection: stars for a given repo, used by GC library-deletion
-- cleanup (store_cassandra.go DeleteStarredFilesByLibrary). Replaces a secondary
-- index on starred_files(repo_id): secondary indexes are node-local and force a
-- scatter-gather across the whole cluster, which degrades at scale and is an
-- anti-pattern in multi-region. A purpose-built projection partitioned by repo_id
-- serves the same query as a single-partition read. Kept in sync by dual-write on
-- star/unstar (write_helpers.go), torn down per-row on user cascade, and removed
-- after canonical deletes succeed on library cascade.
CREATE TABLE IF NOT EXISTS starred_files_by_repo (
    repo_id    UUID,
    user_id    UUID,
    path       TEXT,
    starred_at TIMESTAMP,
    PRIMARY KEY ((repo_id), user_id, path)
);

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

CREATE TABLE IF NOT EXISTS monitored_repos_by_repo (
    repo_id       UUID,
    user_id       UUID,
    monitored_at  TIMESTAMP,
    PRIMARY KEY ((repo_id), user_id)
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

-- Reverse-lookup projection: files carrying a given tag. The canonical file_tags
-- is partitioned by (repo_id) and clustered by (file_path, tag_id), so "list all
-- files with tag T" previously required `WHERE repo_id = ? AND tag_id = ?
-- ALLOW FILTERING` (a partition scan + filter). This projection answers that as a
-- single-partition read. Kept in sync by dual-write on every file_tags mutation
-- (add/remove tag, file rename/move, path/prefix cleanup, repo/library teardown).
-- file_path alone is unique within (repo_id, tag_id), so file_tag_id stays a
-- payload column (deletes only need repo_id+tag_id+file_path).
CREATE TABLE IF NOT EXISTS file_tags_by_tag (
    repo_id     UUID,
    tag_id      INT,
    file_path   TEXT,
    file_tag_id INT,
    created_at  TIMESTAMP,
    PRIMARY KEY ((repo_id, tag_id), file_path)
);

-- ─── GC ──────────────────────────────────────────────────────────────────────

-- Durable queue: items are removed only by explicit completion, requeue, or
-- DLQ transfer. Ordered ASC for worker FIFO processing.
CREATE TABLE IF NOT EXISTS gc_queue (
    org_id        UUID,
    bucket        INT,
    queued_at     TIMESTAMP,
    identity_at   TIMESTAMP,
    requires_library_deleted_check BOOLEAN,
    item_type     TEXT,
    item_id       TEXT,
    library_id    UUID,
    storage_class TEXT,
    retry_count   INT,
    PRIMARY KEY ((org_id, bucket), queued_at, item_type, item_id)
) WITH default_time_to_live = 0
  AND CLUSTERING ORDER BY (queued_at ASC);

CREATE TABLE IF NOT EXISTS gc_active_orgs (
    bucket           INT,
    org_id           UUID,
    last_enqueued_at TIMESTAMP,
    PRIMARY KEY ((bucket), org_id)
);

CREATE TABLE IF NOT EXISTS gc_dirty_orgs (
    bucket    INT,
    org_id    UUID,
    marked_at TIMESTAMP,
    PRIMARY KEY ((bucket), org_id)
);

CREATE TABLE IF NOT EXISTS gc_org_stats (
    org_id           UUID PRIMARY KEY,
    queue_depth      INT,
    failed_depth     INT,
    oldest_queued_at TIMESTAMP,
    updated_at       TIMESTAMP,
    recalculated_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS gc_failed_items (
    org_id            UUID,
    failed_at         TIMESTAMP,
    expires_at        TIMESTAMP,
    queued_at         TIMESTAMP,
    identity_at       TIMESTAMP,
    requires_library_deleted_check BOOLEAN,
    item_type         TEXT,
    item_id           TEXT,
    library_id        UUID,
    storage_class     TEXT,
    retry_count       INT,
    last_error        TEXT,
    failure_code      TEXT,
    resolution_status TEXT,
    resolved_at       TIMESTAMP,
    PRIMARY KEY ((org_id), failed_at, item_type, item_id)
) WITH CLUSTERING ORDER BY (failed_at DESC, item_type ASC, item_id ASC)
  AND default_time_to_live = 0;
-- ^ No table TTL on purpose. DLQ cleanup is the scanner-driven explicit expiry
-- below: it deletes at failed_at + 30d, removes the projection and pending
-- marker in the same batch, and marks the org dirty so the snapshot reconciler
-- refreshes failed_depth. A silent Cassandra TTL would reap rows WITHOUT marking
-- dirty (leaving failed_depth stale, since the drift check only sums snapshots)
-- and would destroy diagnostic last_error/failure_code history. Unbounded growth
-- during a prolonged scanner outage is handled operationally via scanner-health
-- alerting (gc_scanner_last_phase_run_timestamp), not by an implicit TTL.

-- Explicit expiry projection for DLQ rows. With the per-org snapshot model
-- (gc_org_stats refreshed from canonical rows, no counters), the scanner walks
-- this projection, revalidates the primary row, then deletes through the GC
-- store so failed_depth and pending markers move together with the DLQ row and
-- the org is marked dirty for an exact refresh.
CREATE TABLE IF NOT EXISTS gc_failed_items_by_expiry (
    expiry_day DATE,
    bucket     INT,
    expires_at TIMESTAMP,
    org_id     UUID,
    failed_at  TIMESTAMP,
    item_type  TEXT,
    item_id    TEXT,
    PRIMARY KEY ((expiry_day, bucket), expires_at, org_id, failed_at, item_type, item_id)
) WITH CLUSTERING ORDER BY (expires_at ASC, org_id ASC, failed_at ASC, item_type ASC, item_id ASC)
  AND default_time_to_live = 0;

-- Live queue and DLQ markers persist until the queue/DLQ row is resolved.
-- Failed-item retention is enforced explicitly by gc_failed_items_by_expiry so
-- pending markers cannot expire before the failed row that suppresses retries.
CREATE TABLE IF NOT EXISTS gc_pending_items (
    org_id      UUID,
    bucket      INT,
    item_type   TEXT,
    library_id  UUID,
    item_id     TEXT,
    identity_at TIMESTAMP,
    PRIMARY KEY ((org_id, bucket), item_type, library_id, item_id, identity_at)
) WITH default_time_to_live = 0;

-- Metadata/checkpoint table for GC snapshots, scanner cursors, and projection
-- rebuild state. The runtime may persist last successful scan days and the
-- currently applied projection/config versions here.
CREATE TABLE IF NOT EXISTS gc_stats (
    stat_key   TEXT PRIMARY KEY,
    stat_value TEXT,
    updated_at TIMESTAMP
);

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('total_queue_depth', '0', toTimestamp(now()))
IF NOT EXISTS;

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('total_failed_items', '0', toTimestamp(now()))
IF NOT EXISTS;

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('dirty_orgs_total', '0', toTimestamp(now()))
IF NOT EXISTS;

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('last_reconcile_run', '', toTimestamp(now()))
IF NOT EXISTS;

-- When the GC worker deletes the canonical `blocks` row before the backing S3
-- object, any S3 failure would otherwise leak storage forever. This recovery
-- table lets the scanner retry deletes later. The 90-day TTL bounds growth.
-- Per-block partition like `blocks` itself: a hot platform org no longer
-- concentrates all S3-orphan rows in one Cassandra partition.
CREATE TABLE IF NOT EXISTS gc_s3_orphans (
    org_id          UUID,
    block_id        TEXT,
    storage_class   TEXT,
    first_seen_at   TIMESTAMP,
    last_attempt_at TIMESTAMP,
    retry_count     INT,
    last_error      TEXT,
    PRIMARY KEY ((org_id, block_id))
) WITH default_time_to_live = 7776000;

-- Discovery projection for `gc_s3_orphans`: partition by (first_seen_day, bucket)
-- so the scanner can walk recent orphans in parallel without enumerating
-- partitions by org. Same TTL as the canonical row so the projection cannot
-- outlive the source. Pattern mirrors `gc_block_candidates_by_day`.
CREATE TABLE IF NOT EXISTS gc_s3_orphans_by_day (
    first_seen_day DATE,
    bucket         INT,
    first_seen_at  TIMESTAMP,
    org_id         UUID,
    block_id       TEXT,
    storage_class  TEXT,
    PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id)
) WITH CLUSTERING ORDER BY (first_seen_at ASC, org_id ASC, block_id ASC)
  AND default_time_to_live = 7776000;

-- Leadership lease so only one enabled replica runs worker/scanner/rollover
-- work at a time.
CREATE TABLE IF NOT EXISTS gc_leases (
    role        TEXT PRIMARY KEY,
    instance_id TEXT,
    heartbeat   TIMESTAMP
);

-- Pending scope-level storage counter reconciliation requests. The scanner
-- recomputes the expected aggregate totals for these scopes and corrects any
-- drift left behind by partial soft-delete/restore flows.
CREATE TABLE IF NOT EXISTS gc_storage_counter_reconciliation (
    scope        TEXT PRIMARY KEY,
    org_id       UUID,
    owner_id     UUID,
    requested_at TIMESTAMP
);

-- Short-lived lock acquired while an org is being hard-deleted. This blocks
-- concurrent reactivation attempts while GC verifies child tables and removes
-- the canonical org rows.
CREATE TABLE IF NOT EXISTS gc_org_hard_delete_locks (
    org_id      UUID PRIMARY KEY,
    started_at  TIMESTAMP,
    heartbeat   TIMESTAMP,
    lease_token UUID
) WITH default_time_to_live = 21600;

-- Short-lived lock acquired while a user is being hard-deleted. This blocks
-- a concurrent restore attempt from racing the cascade worker between the
-- stale-check and the final HardDeleteUser write.
CREATE TABLE IF NOT EXISTS gc_user_hard_delete_locks (
    user_id     UUID PRIMARY KEY,
    started_at  TIMESTAMP,
    heartbeat   TIMESTAMP,
    lease_token UUID
) WITH default_time_to_live = 21600;

-- Short-lived library lifecycle fence for soft-delete, restore, and hard-delete.
-- This blocks concurrent lifecycle writers from observing a cross-table
-- intermediate state between the stale-check and their final mutation.
CREATE TABLE IF NOT EXISTS gc_library_hard_delete_locks (
    library_id  UUID PRIMARY KEY,
    started_at  TIMESTAMP,
    heartbeat   TIMESTAMP,
    lease_token UUID
) WITH default_time_to_live = 21600;

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
    shard             INT,
    day               DATE,
    user_id           UUID,
    traffic_type      TEXT,
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month, shard), day, user_id, traffic_type)
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
    shard      INT,
    day        DATE,
    bytes_used COUNTER,
    file_count COUNTER,
    PRIMARY KEY ((scope, shard), day)
) WITH CLUSTERING ORDER BY (day DESC);
