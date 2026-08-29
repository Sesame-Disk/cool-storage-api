-- Migration 019: durable block-delete authority handoff (P4b-2 / R14b).
--
-- `blocks` and `gc_s3_orphans` are separate Paxos partitions, so there is no
-- cross-table CAS. After the last liveness authorization, this marker commits
-- the exact delete authority (P, D) on `blocks` so a later worker can only
-- resume that same lifecycle. The canonical orphan then stores the same D.
--
-- gc_orphan_handoff is unset (null) until commit and true afterwards. Never
-- write false: null remains the only "not yet committed" value the LWTs test.
--
-- Do not fold these columns into 001_initial_schema.cql here. Migration 018
-- records that the full schema fold waits until X1 closes. Fresh installs
-- already apply 001..018 and then this ALTER.
--
-- PRIMARY KEY of gc_s3_orphans / gc_s3_orphans_by_day is unchanged (P4c-orphan).

ALTER TABLE blocks
    ADD IF NOT EXISTS gc_orphan_handoff boolean;

ALTER TABLE gc_s3_orphans
    ADD IF NOT EXISTS gc_claim_id text;

ALTER TABLE gc_s3_orphans
    ADD IF NOT EXISTS gc_claimed_at timestamp;
