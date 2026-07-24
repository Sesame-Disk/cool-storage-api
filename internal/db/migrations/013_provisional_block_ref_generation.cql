-- Migration 013: shared generation identity for provisional block references.
--
-- A provisional upload reference and its GC expiry tracker are written together
-- (AddProvisionalBlockReferenceWithExpiry). A release has to retire exactly the
-- pair it observed, or it deletes a reference an upload just renewed — unpinning
-- a live upload — or retires the tracking of a live reference, which makes that
-- reference undiscoverable to GC forever.
--
-- Deriving that identity from the two rows' own timestamps does not work:
--   * created_at (reference) and expires_at (tracker) are read separately, so a
--     renewal landing between the reads yields a MIXED observation — one row's
--     old identity with the other's new one.
--   * both are wall-clock values at millisecond resolution, so two renewals
--     inside the same millisecond are indistinguishable.
--
-- generation_id is one opaque UUID minted per write and stamped on BOTH rows, so
-- a single read yields a coherent identity and no two writes ever collide.
ALTER TABLE block_references ADD IF NOT EXISTS generation_id UUID;

ALTER TABLE gc_provisional_block_refs ADD IF NOT EXISTS generation_id UUID;
