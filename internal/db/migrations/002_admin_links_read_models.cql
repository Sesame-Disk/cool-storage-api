-- Migration 002: Admin/global link read models
--
-- Purpose:
--   Fast sys-admin listings for share/upload links without walking every org
--   partition and without N+1 lookups for repo/creator display data.
--
-- Write model:
--   Canonical source remains share_links + share_links_by_creator/_by_org/_by_library.
--   This table is a global read projection maintained by dual-write helpers.

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