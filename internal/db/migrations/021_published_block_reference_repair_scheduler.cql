-- Migration 021: bounded due-time discovery for post-HEAD publish repairs.
--
-- The durable repair row is intentionally retained until positive publication
-- reachability is confirmed. It therefore cannot also be the worker's minute-
-- by-minute due queue: scanning its 32 long-lived partitions makes every node
-- reread every UNKNOWN row, and an IF EXISTS retry update would add Paxos to the
-- retry path. These tables split scheduler state from repair authority.
--
-- retry_slot is a 2048-slot, five-minute time wheel. The worker reads only the
-- current and two immediately previous slots in each bucket, with a clustering
-- bound on next_retry_at. The ring is longer than the six-hour retry cap; a
-- startup bootstrap also re-seeds overdue rows after a long outage.
CREATE TABLE IF NOT EXISTS published_block_reference_repair_retry_slots (
    retry_slot    INT,
    bucket        INT,
    next_retry_at TIMESTAMP,
    org_id        UUID,
    repo_id       UUID,
    commit_id     TEXT,
    fs_id         TEXT,
    PRIMARY KEY ((retry_slot, bucket), next_retry_at, org_id, repo_id, commit_id, fs_id)
) WITH CLUSTERING ORDER BY (next_retry_at ASC, org_id ASC, repo_id ASC, commit_id ASC, fs_id ASC)
  AND default_time_to_live = 0;

-- One ordinary exact-key row records the current scheduler identity. It lets
-- the worker discard stale due entries left by insert-before-delete recovery
-- without touching the publication authority path.
CREATE TABLE IF NOT EXISTS published_block_reference_repair_schedule_state (
    bucket        INT,
    org_id        UUID,
    repo_id       UUID,
    commit_id     TEXT,
    fs_id         TEXT,
    next_retry_at TIMESTAMP,
    PRIMARY KEY ((bucket), org_id, repo_id, commit_id, fs_id)
) WITH default_time_to_live = 0;
