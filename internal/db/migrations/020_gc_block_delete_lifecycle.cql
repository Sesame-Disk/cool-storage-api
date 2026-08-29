-- Migration 020: durable block-delete lifecycle tombstone (P4b-2 audit follow-up).
--
-- gc_orphan_handoff on blocks is irreversible for a live row, but Finalize
-- deletes that row and recovery then clears gc_s3_orphans. A stale
-- CommittedOwner that still holds D in memory could INSERT the orphan again
-- (PK is still (org_id, block_id)) and treat AlreadyFinalized as license to
-- DELETE P. This table is the never-deleted tombstone of D: once phase is
-- terminal, the same claim_id cannot publish, finalize, or physically delete.
--
-- PRIMARY KEY includes claim_id so a later life D2 of the same logical block
-- occupies a different row. Do not DELETE these rows.
--
-- Do not fold this table into 001_initial_schema.cql here. The full schema
-- fold waits until X1 closes.

CREATE TABLE IF NOT EXISTS gc_block_delete_lifecycles (
    org_id        UUID,
    block_id      TEXT,
    claim_id      TEXT,
    claimed_at    TIMESTAMP,
    storage_class TEXT,
    storage_key   TEXT,
    phase         TEXT,
    PRIMARY KEY ((org_id, block_id), claim_id)
);
