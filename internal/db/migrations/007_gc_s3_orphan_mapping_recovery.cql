-- Migration 007: persist forward-mapping cleanup state in gc_s3_orphans
--
-- PR7 dropped block_id_mappings_by_internal and GC started resolving the single
-- forward SHA-1->SHA-256 mapping from blocks.sha1. That is correct, but one
-- restart window remained: after FinalizeBlockDelete removes the canonical
-- blocks row, a crash/redeploy before cleanupBlockMapping finishes leaves no way
-- to reconstruct the external SHA-1 from the deleted block row.
--
-- Fix: persist the server-derived external SHA-1 plus a tiny recovery phase in
-- gc_s3_orphans, the existing crash-safe recovery row already written before the
-- canonical delete. Recovery can now resume in either phase:
--   - pending_s3: delete backing bytes, then advance to mapping cleanup
--   - pending_mapping_cleanup: finish block_id_mappings cleanup only
--
-- The discovery projection mirrors the same fields so recovery can continue to
-- enumerate work without extra canonical reads per orphan.

ALTER TABLE gc_s3_orphans
    ADD external_sha1 TEXT;

ALTER TABLE gc_s3_orphans
    ADD recovery_phase TEXT;

ALTER TABLE gc_s3_orphans_by_day
    ADD external_sha1 TEXT;

ALTER TABLE gc_s3_orphans_by_day
    ADD recovery_phase TEXT;
