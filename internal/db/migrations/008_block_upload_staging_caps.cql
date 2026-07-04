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
-- ALTER adds the per-session admission contract frozen at session creation so the
-- rules in effect when the session was minted stay stable for its whole 48h life,
-- even across config changes or restarts, WITHOUT adding coordination to the
-- /blocks/upload hot path:
--   - slot                    : the per-user concurrency slot claimed (freed at commit).
--   - expected_size           : the client-declared file size (fail-fast at creation,
--                               re-checked against the manifest at commit).
--   - expected_size_declared  : distinguishes an explicit size=0 (empty file) from
--                               "size missing" so the commit guard only applies when
--                               a size was actually declared.
--   - block_size_bytes        : the authoritative CAS block size echoed to the client
--                               and reused by /blocks/upload, so a live config change
--                               cannot reject an in-flight upload's body size.
--   - staged_bucket_count /
--     staged_bucket_cap        : the staged-block ledger parameters, frozen so retries
--                               hash into the SAME bucket partitions (a config change
--                               must not move a block to a different bucket and count
--                               it twice).
-- Applied exactly once (tracked in schema_migrations). The columns are added with
-- one single-column ALTER each, matching the repo convention (see migration 005) for
-- the widest CQL compatibility.

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

ALTER TABLE block_upload_sessions ADD slot INT;
ALTER TABLE block_upload_sessions ADD expected_size BIGINT;
ALTER TABLE block_upload_sessions ADD expected_size_declared BOOLEAN;
ALTER TABLE block_upload_sessions ADD block_size_bytes BIGINT;
ALTER TABLE block_upload_sessions ADD staged_bucket_count INT;
ALTER TABLE block_upload_sessions ADD staged_bucket_cap INT;
