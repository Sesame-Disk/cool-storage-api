-- Durable pending-cleanup rows for OnlyOffice save callbacks.
-- This tracks materialized blocks between block ref registration and
-- successful library-head publication so later processes can reconcile stale
-- pending saves conservatively.
CREATE TABLE IF NOT EXISTS onlyoffice_pending_blocks (
    org_id            TEXT,
    operation_id      TEXT,
    repo_id           TEXT,
    file_path         TEXT,
    internal_block_id TEXT,
    external_block_id TEXT,
    storage_class     TEXT,
    publish_commit_id TEXT,
    created_at        TIMESTAMP,
    updated_at        TIMESTAMP,
    PRIMARY KEY ((org_id), operation_id)
);
