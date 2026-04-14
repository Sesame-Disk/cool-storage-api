-- Migration 002: gc_s3_orphans
--
-- When the GC worker deletes a block, it executes the LWT delete on the
-- canonical `blocks` row first and then deletes the object from S3. If the
-- S3 DeleteBlock call fails (transient network error, S3 outage) AFTER the
-- DB row is gone, the object in S3 becomes unreachable — the scanner cannot
-- rediscover it because there is no row in `blocks` with ref_count <= 0 to
-- scan. Without a recovery record, the orphan leaks storage forever.
--
-- This table records those failures so a scanner phase can retry the S3
-- delete later. Rows are removed as soon as the S3 delete succeeds. The
-- 90-day TTL is a safety net — if a block has been unretryable for 90 days,
-- it is almost certainly due to a credential/bucket change and manual
-- intervention is needed anyway.

CREATE TABLE IF NOT EXISTS gc_s3_orphans (
    org_id          UUID,
    block_id        TEXT,
    storage_class   TEXT,
    first_seen_at   TIMESTAMP,
    last_attempt_at TIMESTAMP,
    retry_count     INT,
    last_error      TEXT,
    PRIMARY KEY ((org_id), block_id)
) WITH default_time_to_live = 7776000;

-- GC remains operator-gated via GC_ENABLED, but once enabled multiple replicas
-- should not process the queue concurrently. This lease row provides a short-
-- lived Cassandra-backed leadership heartbeat so worker/scanner/rollover work
-- runs on at most one enabled replica at a time and recovers automatically on
-- container restart or node loss.

CREATE TABLE IF NOT EXISTS gc_leases (
    role        TEXT PRIMARY KEY,
    instance_id TEXT,
    heartbeat   TIMESTAMP
);
