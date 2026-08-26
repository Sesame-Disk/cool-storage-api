-- Migration 017: bind every block-delete candidate to the exact physical incarnation.
--
-- A candidate enqueued for P1 = (storage_class, storage_key) must never be able to
-- claim, finalize or clear P2 after P1 died and P2 was installed on the same logical
-- block (R14). storage_class alone does not identify an incarnation -- a re-minted key
-- keeps the class -- so the candidate has to carry the key it was created for.
--
-- The claim CAS names both columns, so a candidate that cannot produce an exact key
-- fails closed instead of guessing: nothing may derive a replacement key from block_id.
--
-- The discovery projection carries the key as a payload column only. Putting P into the
-- projection's PRIMARY KEY is R26 and is deliberately not done here.
ALTER TABLE gc_block_candidates
    ADD IF NOT EXISTS storage_key text;

ALTER TABLE gc_block_candidates_by_day
    ADD IF NOT EXISTS storage_key text;
