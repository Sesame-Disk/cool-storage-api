-- Migration 004: GC queue redesign primitives.
--
-- This migration introduces the durable queue support tables used by the
-- redesigned GC reconciler. Runtime switchover happens in code; the schema is
-- added here first so the new model can be enabled on a clean database.

DROP TABLE IF EXISTS gc_queue_stats;

ALTER TABLE gc_queue WITH default_time_to_live = 0;

CREATE TABLE IF NOT EXISTS gc_active_orgs (
    bucket           INT,
    org_id           UUID,
    last_enqueued_at TIMESTAMP,
    PRIMARY KEY ((bucket), org_id)
);

CREATE TABLE IF NOT EXISTS gc_dirty_orgs (
    bucket    INT,
    org_id    UUID,
    marked_at TIMESTAMP,
    PRIMARY KEY ((bucket), org_id)
);

CREATE TABLE IF NOT EXISTS gc_org_stats (
    org_id           UUID PRIMARY KEY,
    queue_depth      INT,
    failed_depth     INT,
    oldest_queued_at TIMESTAMP,
    updated_at       TIMESTAMP
);

CREATE TABLE IF NOT EXISTS gc_failed_items (
    org_id            UUID,
    failed_at         TIMESTAMP,
    queued_at         TIMESTAMP,
    item_type         TEXT,
    item_id           TEXT,
    library_id        UUID,
    storage_class     TEXT,
    retry_count       INT,
    last_error        TEXT,
    resolution_status TEXT,
    resolved_at       TIMESTAMP,
    PRIMARY KEY ((org_id), failed_at, item_type, item_id)
) WITH CLUSTERING ORDER BY (failed_at DESC, item_type ASC, item_id ASC)
  AND default_time_to_live = 2592000;

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('total_queue_depth', '0', toTimestamp(now()));

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('total_failed_items', '0', toTimestamp(now()));

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('dirty_orgs_total', '0', toTimestamp(now()));

INSERT INTO gc_stats (stat_key, stat_value, updated_at)
VALUES ('last_reconcile_run', '', toTimestamp(now()));