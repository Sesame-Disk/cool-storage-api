-- Migration 016: persist the exact canonical physical locator for orphan recovery.
--
-- The discovery projection remains identity-only. Recovery reloads this value from
-- gc_s3_orphans at EACH_QUORUM and must never derive a replacement key from block_id.
ALTER TABLE gc_s3_orphans
    ADD IF NOT EXISTS storage_key text;
