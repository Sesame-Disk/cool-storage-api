-- Migration 003: gc_queue / DLQ / marker tables -> LeveledCompactionStrategy
--
-- See docs/SCHEMA-BOTTLENECK-AUDIT.md item I and
-- docs/KNOWN_ISSUES.md ISSUE-GC-QUEUE-RECOUNT-01.
--
-- These five tables are the only GC tables that combine (a) a long-lived hot
-- partition with (b) continuous range/partition scans at the exact coordinates
-- where insert+delete churn leaves tombstones:
--
--   gc_queue          (org_id, bucket)  -- DequeueBatch scans the head where
--                                          completed-item tombstones accumulate
--   gc_pending_items  (org_id, bucket)  -- pending markers, scanned + deleted
--   gc_active_orgs    (bucket)          -- full-partition scan, churned membership
--   gc_dirty_orgs     (bucket)          -- full-partition scan, churned membership
--   gc_failed_items   (org_id)          -- DLQ, scanned + deleted by the expiry sweep
--
-- LCS reduces read amplification at those coordinates immediately: ~90% of
-- reads hit a single SSTable per level and the tombstone "chases" the row it
-- shadows down the levels instead of sitting in a separate STCS SSTable that is
-- re-scanned on every worker tick.
--
-- The tombstone_threshold/tombstone_compaction_interval knobs are anticipatory,
-- not immediately load-bearing: tombstone_threshold only counts tombstones
-- already past gc_grace_seconds (kept at the 10-day default here), so they only
-- accelerate purge AFTER grace, not the sub-grace churn that produces most of
-- the read-path warnings. Lowering gc_grace_seconds on these idempotent,
-- leader-only-drained tables is the deferred follow-up, gated on re-measuring
-- the warnings under multi-node load.
--
-- ALTER is safe to run during clean boot: the tables are freshly created by
-- migration 001 and empty, so switching compaction strategy is instant and
-- triggers no recompaction of existing data. The discovery projections
-- (gc_*_by_day, gc_failed_items_by_expiry) are intentionally left on the
-- default strategy: they are date-bucketed, so an aged partition falls out of
-- the read window and its tombstones are never re-scanned.

ALTER TABLE gc_queue WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
};

ALTER TABLE gc_pending_items WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
};

ALTER TABLE gc_active_orgs WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
};

ALTER TABLE gc_dirty_orgs WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
};

ALTER TABLE gc_failed_items WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
};
