-- Migration 004: Group/user share read models for admin panels
--
-- Purpose:
--   Avoid scanning libraries + filtering shares in memory for org-admin and admin
--   screens that need "what repos are shared to this group/user".
--
-- Write model:
--   Canonical source remains shares + shares_by_user. These tables are richer
--   projections for admin/org-admin read paths and dashboards.

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