-- Migration 018: make the exact physical candidate identity part of every GC key.
--
-- A block candidate is identified by P = (storage_class, storage_key) and the
-- candidate_at lifecycle instant. Two lives of one logical block must occupy two
-- rows, everywhere GC work is durable: the canonical candidate, its discovery
-- projection, the queue, the pending markers and the DLQ. Cassandra cannot add
-- clustering columns to an existing table, so these are recreated.
--
-- THIS MIGRATION IS DESTRUCTIVE AND IS NOT AN UPGRADE. It drops the tables it
-- recreates. It is correct only for the contract this release ships under: a
-- clean keyspace, migrated before any node starts producing GC work.
--
-- That contract is ENFORCED, not merely documented. Migrator.preflightDestructive
-- refuses to apply any table-dropping migration whose targets still hold rows, so
-- running this release against a keyspace that already accumulated GC work fails
-- at startup with the offending table named, instead of silently erasing queue,
-- pending and DLQ state — including the non-block item types that have nothing to
-- do with this change. See internal/db/migrator.go.
--
-- Dropping rather than altering also makes an accidental rolling upgrade fail
-- visibly: a binary from before this release inserts into gc_queue without the
-- clustering columns and is rejected by the engine, rather than collapsing P1 and
-- P2 back into one row.
--
-- Migration 017 added storage_key to gc_block_candidates as a payload column and
-- recorded that folding P into the projection's PRIMARY KEY was deliberately out
-- of scope. This migration is that follow-up; 017 still applies first on a fresh
-- keyspace and is then superseded here. It is left untouched because editing an
-- applied migration breaks its recorded checksum.
--
-- Non-block GC rows carry empty P values, and are identified by identity_at.

DROP TABLE IF EXISTS gc_failed_items_by_expiry;
DROP TABLE IF EXISTS gc_failed_items;
DROP TABLE IF EXISTS gc_pending_items;
DROP TABLE IF EXISTS gc_queue;
DROP TABLE IF EXISTS gc_block_candidates_by_day;
DROP TABLE IF EXISTS gc_block_candidates;

CREATE TABLE IF NOT EXISTS gc_block_candidates (
    org_id        UUID,
    block_id      TEXT,
    storage_class TEXT,
    storage_key   TEXT,
    candidate_at  TIMESTAMP,
    PRIMARY KEY ((org_id, block_id), storage_class, storage_key)
);

CREATE TABLE IF NOT EXISTS gc_block_candidates_by_day (
    candidate_day DATE,
    bucket        INT,
    candidate_at  TIMESTAMP,
    org_id        UUID,
    block_id      TEXT,
    storage_class TEXT,
    storage_key   TEXT,
    PRIMARY KEY ((candidate_day, bucket), candidate_at, org_id, block_id, storage_class, storage_key)
) WITH CLUSTERING ORDER BY (candidate_at ASC, org_id ASC, block_id ASC, storage_class ASC, storage_key ASC);

CREATE TABLE IF NOT EXISTS gc_queue (
    org_id                         UUID,
    bucket                         INT,
    queued_at                      TIMESTAMP,
    identity_at                    TIMESTAMP,
    requires_library_deleted_check BOOLEAN,
    library_guard_mode             TEXT,
    item_type                      TEXT,
    item_id                        TEXT,
    library_id                     UUID,
    block_representation_id        TEXT,
    storage_class                  TEXT,
    candidate_storage_class        TEXT,
    candidate_storage_key          TEXT,
    retry_count                    INT,
    PRIMARY KEY ((org_id, bucket), queued_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at)
) WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
}
  AND default_time_to_live = 0
  AND CLUSTERING ORDER BY (queued_at ASC);

CREATE TABLE IF NOT EXISTS gc_pending_items (
    org_id                  UUID,
    bucket                  INT,
    item_type               TEXT,
    library_id              UUID,
    item_id                 TEXT,
    candidate_storage_class TEXT,
    candidate_storage_key   TEXT,
    identity_at             TIMESTAMP,
    PRIMARY KEY ((org_id, bucket), item_type, library_id, item_id, candidate_storage_class, candidate_storage_key, identity_at)
) WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
}
  AND default_time_to_live = 0;

CREATE TABLE IF NOT EXISTS gc_failed_items (
    org_id                         UUID,
    failed_at                      TIMESTAMP,
    expires_at                     TIMESTAMP,
    queued_at                      TIMESTAMP,
    identity_at                    TIMESTAMP,
    requires_library_deleted_check BOOLEAN,
    library_guard_mode             TEXT,
    item_type                      TEXT,
    item_id                        TEXT,
    library_id                     UUID,
    block_representation_id        TEXT,
    storage_class                  TEXT,
    candidate_storage_class        TEXT,
    candidate_storage_key          TEXT,
    retry_count                    INT,
    last_error                     TEXT,
    failure_code                   TEXT,
    resolution_status              TEXT,
    resolved_at                    TIMESTAMP,
    PRIMARY KEY ((org_id), failed_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at)
) WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
}
  AND CLUSTERING ORDER BY (failed_at DESC, item_type ASC, item_id ASC, candidate_storage_class ASC, candidate_storage_key ASC, identity_at ASC)
  AND default_time_to_live = 0;

CREATE TABLE IF NOT EXISTS gc_failed_items_by_expiry (
    expiry_day              DATE,
    bucket                  INT,
    expires_at              TIMESTAMP,
    org_id                  UUID,
    failed_at               TIMESTAMP,
    item_type               TEXT,
    item_id                 TEXT,
    candidate_storage_class TEXT,
    candidate_storage_key   TEXT,
    identity_at             TIMESTAMP,
    PRIMARY KEY ((expiry_day, bucket), expires_at, org_id, failed_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at)
) WITH CLUSTERING ORDER BY (expires_at ASC, org_id ASC, failed_at ASC, item_type ASC, item_id ASC, candidate_storage_class ASC, candidate_storage_key ASC, identity_at ASC)
  AND default_time_to_live = 0;
