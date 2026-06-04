# Schema Bottleneck Audit

**Updated**: 2026-05-26

This document is the rolling backlog of Cassandra schema shapes that look like
operational bottlenecks (hot partitions, `ALLOW FILTERING`, full-table scans,
LWT contention) but are **not** addressed in the current branch that resolved
[ISSUE-BLOCKS-HOT-PARTITION-01](KNOWN_ISSUES.md#issue-blocks-hot-partition-01).

Each entry is a candidate for a future schema-shaping branch. None of these are
currently breaking production; they are tracked so the next audit pass can pick
them up without re-discovering the analysis.

For every item: status, table(s), the observed shape, why it could become a
problem, and the rough fix direction. Add new items at the end of the list with
their date.

---

## A. `users_by_email` ALLOW FILTERING fallback

**Status**: open, low-priority

**Tables**: `users_by_email` (canonical), `users` (fallback)

**Shape**: when the `users_by_email` lookup misses (legacy rows, replica
divergence), the code falls back to
`SELECT user_id, org_id FROM users WHERE email = ? ALLOW FILTERING`
([internal/auth/oidc.go](../internal/auth/oidc.go),
[internal/api/v2/admin.go](../internal/api/v2/admin.go)). That is an unbounded
scan.

**Risk**: gradual degradation as `users` grows. Today it is rare because the
dual-write keeps `users_by_email` populated.

**Direction**: tighten the dual-write contract, audit any place that creates a
`users` row to make sure `users_by_email` is also written, then promote the
fallback to a hard failure instead of an ALLOW FILTERING scan.

---

## B. `file_tags` queries by `tag_id` need ALLOW FILTERING

**Status**: open, medium-priority

**Tables**: `file_tags`

**Shape**:
```sql
SELECT file_path, file_tag_id FROM file_tags
WHERE repo_id = ? AND tag_id = ?
ALLOW FILTERING
```
([internal/api/v2/write_helpers.go](../internal/api/v2/write_helpers.go),
[internal/api/v2/tags.go](../internal/api/v2/tags.go)).

The current PK is `((repo_id), file_path, tag_id)`. Filtering by `tag_id`
without `file_path` is a partition-scan with a filter.

**Risk**: a repo with many tagged files makes "list files for this tag" slow.

**Direction**: add a `file_tags_by_tag (PRIMARY KEY ((repo_id, tag_id), file_path, file_tag_id))`
projection, dual-write on tag changes, and read from the new projection. The
canonical `file_tags` stays for the "list tags on this file" query shape.

---

## C. COUNTER tables without bucketing

**Status**: open, medium-priority

**Tables**: `admin_link_counts_by_org`, `repo_tag_file_counts`, `traffic_counters`,
`traffic_monthly`, `traffic_period_usage`, `storage_counters`.

**Shape**: each of these is a Cassandra `COUNTER` table partitioned by a key
that is shared across a high-throughput workload (e.g. `(org_id, link_type)`).
Counter updates require a coordinator and a read-before-write inside Cassandra
even though they look like ordinary writes from the client side.

**Risk**: under heavy concurrent counter updates on the same partition, latency
climbs and counters can drift if rare counter-replica issues happen. Recovery
relies on the existing recount workflow.

**Direction**: shard hot counter partitions, e.g.
`((org_id, link_type, shard), ...)` with a small `shard = rand() % N`, and
sum across shards at read time. The platform-aggregate row is the most
visible candidate (see also `docs/TECHNICAL-DEBT.md` §12b).

---

## D. `gc_queue` with `default_time_to_live = 0`

**Status**: open, medium-priority

**Tables**: `gc_queue`

**Shape**: durable queue with no TTL. Worker is supposed to remove items via
`CompleteItem`, but worker stalls or persistent failures leave items in place
forever.

**Risk**: cluster-wide growth if the worker stops draining. Failed items move
to `gc_failed_items` and are expired explicitly through
`gc_failed_items_by_expiry`, but successful-looking-but-stale items can linger.

**Direction**: introduce a long but bounded TTL (e.g. 90–180 days) so abandoned
queue items eventually fall off and orphan recovery picks them up via the
`gc_block_candidates_by_day` projection on the next pass. The decision needs
to be paired with explicit dead-item alerts so a stuck worker is detected
before the TTL helps mask the symptom.

---

## E. `storage_counters` partitioned by `scope`

**Status**: open, low-priority

**Tables**: `storage_counters`

**Shape**: partition key is `(scope)` where `scope` is a low-cardinality string
like `org:<uuid>`, `user:<uuid>`, `library:<uuid>`, or `platform`. The
platform-aggregate row in particular is a single partition that absorbs all
global writes.

**Risk**: heavy reconcile passes can hit this row repeatedly under load. Today
the magnitude is fine, but it is the same anti-pattern that
`ISSUE-BLOCKS-HOT-PARTITION-01` had: low-cardinality partition key that
concentrates a high-throughput workload.

**Direction**: same playbook as the COUNTER tables — shard the platform scope
into `((scope, shard), ...)` and sum on read. Org / user / library scopes are
already naturally distributed and probably do not need sharding.

---

## F. `gc_block_candidates_by_day` and `gc_s3_orphans_by_day` growth observability

**Status**: open, low-priority (introduced 2026-05-26)

**Tables**: `gc_block_candidates_by_day`, `gc_s3_orphans_by_day`

**Shape**: discovery projections added when `blocks` moved to per-block
partitioning. They are bounded by the canonical row lifetime, but the scanner
relies on the per-day cursor to bound how far back it walks each pass.

**Risk**: if the scanner is paused for longer than `gcInitialScanLookbackDays`
(default 7 days) before the cursor is initialized, we miss candidates older
than that lookback. The cursor itself is the safety, but it depends on the
scanner running at least once per lookback window.

**Direction**: emit a Prometheus gauge for the current cursor value and an
alert when the cursor lags more than N days behind `today`. The metric
infrastructure for this already exists alongside
`gc_scanner_last_phase_run_timestamp_seconds`.

---

## G. Per-day discovery partitions can become hot on bursty workloads

**Status**: open, low-priority (introduced 2026-05-26)

**Tables**: any `gc_*_by_day` projection (newly: block candidates and S3
orphans; previously: share links and shares).

**Shape**: the partition key is `(day, bucket)` with `db.GCDiscoveryBucketCount
= 32`. On a day with a huge spike of refcount=0 events, one bucket can grow
beyond Cassandra's recommended partition size (100 MB / 100k rows).

**Risk**: today the workload is well below that threshold. Once a single org
or migration starts decrementing millions of refcounts on the same day, the
discovery partition can become slow to read.

**Direction**: keep `GCDiscoveryBucketCount` tunable per table, and consider
moving to `(day, hour, bucket)` granularity for the hottest projections if we
ever measure single-day partitions approaching the soft limit.

---

## H. Org-scoped `libraries` scans still walk tombstone-heavy partitions

**Status**: open, medium-priority (introduced 2026-05-27)

**Tables**: `libraries`, `libraries_by_owner`, `libraries_deleted_by_org`

**Shape**: several code paths still read the canonical `libraries` partition by
`org_id` and then filter in application code on `owner_id` or `deleted_at`.
This is not `ALLOW FILTERING` in the CQL text, but on Cassandra 5 the resulting
read path can still surface warnings that look like:

```sql
SELECT deleted_at, owner_id, storage_class
FROM sesamefs.libraries
WHERE org_id = <org>
LIMIT 5000 ALLOW FILTERING
```

Representative callers:

- `internal/gc/store_cassandra.go` — `ListLibrariesByOwner`, `ListLibrariesForOrg`
- `internal/api/v2/enforcement.go` — `CountActiveLibraries`
- `internal/middleware/permissions.go` — `GetUserLibraries`
- `internal/api/v2/libraries.go` — `ownerHasActiveLibraryNamed`
- `internal/api/v2/search.go` — org-scoped library search prefilter scans

As of 2026-05-27, the deleted-library trash list/clean flows were moved to the
`libraries_deleted_by_org` projection, so those routes no longer contribute to
this canonical-partition scan pattern.

**Risk**: orgs with heavy library churn (soft delete, restore, hard delete)
accumulate `deleted_at` tombstones and row tombstones in the canonical
partition. Repeated org-wide reads then produce noisy Cassandra warnings and
more expensive partition walks in GC and quota/permission-adjacent paths.

**Direction**:

1. Keep deleted-library flows on `libraries_deleted_by_org` and do not regress
	them back to canonical `libraries` scans.
2. Move owner-centric enumeration to `libraries_by_owner` where the caller only
	needs owned libraries.
3. Introduce an explicit active-library count / projection for enforcement
	instead of counting `deleted_at IS NULL` via org-wide scans.
4. Audit any remaining org-wide canonical library scan and either replace it
	with a read model that matches the access pattern or document why the scan is
	still acceptable.

---

## Closing The Loop

When working any of the items above, add the associated `ISSUE-...-01` ID to
`docs/KNOWN_ISSUES.md` first so the link from this audit to the active work is
traceable. The current upload incident class is closed by the per-block schema
move; the next branch should pick from the table above based on observed
operational pain, not eagerness.
