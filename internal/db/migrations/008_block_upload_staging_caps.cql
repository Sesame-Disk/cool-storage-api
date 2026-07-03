-- Migration 008: web block-upload staging caps (docs/WEB-BLOCK-UPLOAD.md item 1)
--
-- Two drift-free, counter-free caps that bound uncommitted (staged) backend bytes
-- for the web content-addressed upload flow, without breaking legitimate large
-- uploads and without reintroducing the same-size-overwrite rejection (R5):
--
--   (1) Per-user concurrent uncommitted-session cap — claimed atomically via LWT
--       slots at session creation (never per block). block_upload_session_slots_by_user
--       holds one row per claimed slot; rows carry the session TTL so an abandoned
--       session self-expires. The claimed slot number is stored on the session row
--       (see the ALTER below) so commit cleanup is a direct delete.
--
--   (2) Per-session staged-block cap — a BUCKETED ledger of the distinct NEW blocks
--       a session has staged. Bucketing keeps the admission read O(1) rows (one small
--       bucket) instead of an O(N^2) partition re-scan for large files. Normal rows +
--       per-row TTL (no Cassandra COUNTER): the reserve is idempotent by
--       ((session_id, bucket), block_id), so a retried block is never double-counted,
--       and an abandoned session's rows self-expire.
--
-- org_id/user_id are UUID to match the rest of the schema (blocks, libraries,
-- block_upload_sessions). block_upload_sessions is defined in migration 004; the
-- ALTER adds the slot the session claimed and the client-declared expected file size
-- (validated at session creation for fail-fast UX and re-checked against the manifest
-- at commit). Applied exactly once (tracked in schema_migrations); the combined ADD is
-- the final statement so the idempotent CREATEs above it are safe on any retry.

CREATE TABLE IF NOT EXISTS block_upload_session_slots_by_user (
    org_id     UUID,
    user_id    UUID,
    slot       INT,
    session_id TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, user_id), slot)
);

CREATE TABLE IF NOT EXISTS block_upload_session_staged_blocks (
    session_id TEXT,
    bucket     INT,
    block_id   TEXT,
    size_bytes BIGINT,
    created_at TIMESTAMP,
    PRIMARY KEY ((session_id, bucket), block_id)
);

ALTER TABLE block_upload_sessions ADD (slot INT, expected_size BIGINT);
