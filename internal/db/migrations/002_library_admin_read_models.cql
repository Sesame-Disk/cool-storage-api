-- Migration 003: Library admin/org-admin read models
--
-- Purpose:
--   Remove ALLOW FILTERING and full-partition scans from the hottest admin/org-admin
--   library listings: owner repos, org repo listings, deleted repo listings, and
--   global admin repo dashboards.
--
-- Write model:
--   Canonical source remains libraries. These tables are read projections updated
--   alongside library create/update/delete/restore flows.

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