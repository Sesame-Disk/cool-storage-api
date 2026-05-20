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
