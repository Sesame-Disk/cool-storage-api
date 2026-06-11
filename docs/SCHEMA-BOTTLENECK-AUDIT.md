# Schema Bottleneck Audit

**Updated**: 2026-06-11

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

**Tracked issue**: [ISSUE-USERS-BY-EMAIL-FALLBACK-01](KNOWN_ISSUES.md#issue-users-by-email-fallback-01)

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

**Status**: resolved in branch `feat/file-tags-by-tag-projection` (2026-06-10)

**Tables**: `file_tags`, new `file_tags_by_tag`

**Shape (was)**:
```sql
SELECT file_path, file_tag_id FROM file_tags
WHERE repo_id = ? AND tag_id = ?
ALLOW FILTERING
```
The canonical PK is `((repo_id), file_path, tag_id)`, so filtering by `tag_id`
without `file_path` was a partition-scan with a filter, in `deleteRepoTag`
([write_helpers.go](../internal/api/v2/write_helpers.go)) and the
list-files-by-tag flow ([tags.go](../internal/api/v2/tags.go)).

**Resolved**: added projection
`file_tags_by_tag (PRIMARY KEY ((repo_id, tag_id), file_path))` — `file_path`
alone is unique within `(repo_id, tag_id)`, so `file_tag_id`/`created_at` stay
payload columns and deletes only need `repo_id+tag_id+file_path` for the
projection row. The user-facing list-files-by-tag path now hits the projection
as a single-partition lookup (no `ALLOW FILTERING`). The projection is kept in
sync by dual-write on every `file_tags` mutation: `addFileTag`/`removeFileTag`
and the projection partition delete in `deleteRepoTag` (write_helpers.go),
`CleanupFileTagsByPath`/`MoveFileTagsByPath`/`CleanupAllLibraryTags` (tags.go),
the prefix cleanup in files.go, and the GC library-cascade `DeleteFileTag`
(store_cassandra.go). For safety, `deleteRepoTag` still derives the exact row
set to delete from canonical `file_tags`; only the reverse-lookup partition
drop uses `file_tags_by_tag` directly. That keeps the merge safe while avoiding
the prior `ALLOW FILTERING` read in the main list-files-by-tag flow.

---

## C. COUNTER tables without bucketing

**Status**: resolved in branch `feat/shard-global-counters-safe` (2026-06-11)

**Tracked issue**: [ISSUE-COUNTER-HOT-PARTITION-01](KNOWN_ISSUES.md#issue-counter-hot-partition-01)

**Tables**: `admin_link_counts_by_org`, `repo_tag_file_counts`, `traffic_counters`,
`traffic_monthly`, `traffic_period_usage`, `storage_counters`.

**Shape**: each of these is a Cassandra `COUNTER` table partitioned by a key
that is shared across a high-throughput workload (e.g. `(org_id, link_type)`).
Counter updates require a coordinator and a read-before-write inside Cassandra
even though they look like ordinary writes from the client side.

**Risk**: under heavy concurrent counter updates on the same partition, latency
climbs and counters can drift if rare counter-replica issues happen. Recovery
relies on the existing recount workflow.

**Resolved**: only the genuinely shared/global hot counter partitions were
sharded in the clean init schema. `traffic_counters` now uses
`PRIMARY KEY ((org_id, month, shard), day, user_id, traffic_type)` and the
platform aggregate rows are routed by deterministic `CounterShard(org_id)`.
Tenant/org readers stay on `shard = 0`, so the hot quota paths keep
single-partition reads while only the cold global admin reads fan out.

---

## D. `gc_queue` with `default_time_to_live = 0`

**Status**: open, medium-priority

**Tracked issue**: [ISSUE-GC-QUEUE-TTL-01](KNOWN_ISSUES.md#issue-gc-queue-ttl-01)

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

**Status**: resolved in branch `feat/shard-global-counters-safe` (2026-06-11)

**Tracked issue**: [ISSUE-COUNTER-HOT-PARTITION-01](KNOWN_ISSUES.md#issue-counter-hot-partition-01)

**Tables**: `storage_counters`

**Shape**: partition key is `(scope)` where `scope` is a low-cardinality string
like `org:<uuid>`, `user:<uuid>`, `library:<uuid>`, or `platform`. The
platform-aggregate row in particular is a single partition that absorbs all
global writes.

**Risk**: heavy reconcile passes can hit this row repeatedly under load. Today
the magnitude is fine, but it is the same anti-pattern that
`ISSUE-BLOCKS-HOT-PARTITION-01` had: low-cardinality partition key that
concentrates a high-throughput workload.

**Resolved**: `storage_counters` now uses `PRIMARY KEY ((scope, shard), day)`,
but only the platform scope uses a hashed shard. Org/user/library scopes stay
pinned to `shard = 0`, so quota reads remain single-partition and only the
global admin readers fan out across shards.

---

## F. `gc_block_candidates_by_day` and `gc_s3_orphans_by_day` growth observability

**Status**: open, low-priority (introduced 2026-05-26)

**Tracked issue**: [ISSUE-GC-DISCOVERY-CURSOR-OBS-01](KNOWN_ISSUES.md#issue-gc-discovery-cursor-obs-01)

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

**Tracked issue**: [ISSUE-GC-DISCOVERY-HOTSPOT-01](KNOWN_ISSUES.md#issue-gc-discovery-hotspot-01)

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

**Status**: partially resolved (2026-06-10, branch
`feat/libraries-org-readers-projection`); owner/enforcement reads moved, list + GC scans still open

**Tracked issue**: [ISSUE-LIBRARIES-ORG-SCAN-01](KNOWN_ISSUES.md#issue-libraries-org-scan-01)

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

**Done so far** (`feat/libraries-org-readers-projection`): the owner-centric and
enforcement reads were moved off the canonical org-partition scan to the
existing projections (no schema change; projection completeness covered by the
existing projection regression suite plus the owner-reader integration tests
across create / soft-delete / restore / owner-transfer / hard-delete /
head-update):

- `enforcement.go CountActiveLibraries` → `libraries_by_org_updated`
- `libraries.go ownerHasActiveLibraryNamed` → `libraries_by_owner`
- `org_admin_users.go GetOrgUserOwnedRepos` → `libraries_by_owner` (also drops an
  `ALLOW FILTERING`)
- `middleware GetUserLibraries` owned-library discovery → `libraries_by_owner`

**Still open** (need a different shape, deferred to a follow-up branch):

- `libraries.go ListLibraries` (api2) and the v2.1 list still scan the org
  partition to fetch full canonical rows (incl. `description`, encryption fields,
  `head_commit_id` not carried by the projections). Right fix: point-read each
  accessible library by id (the handlers already compute the accessible set) so
  the read is bounded to the caller's libraries instead of the whole org.
- `internal/gc/store_cassandra.go` storage-reconcile reads `FROM libraries` with
  no `WHERE` (full table scan) — separate maintenance-path concern.
- `internal/api/v2/search.go` org-scoped library search prefilter.

---

## I. `gc_queue` / DLQ tombstone purge needs compaction tuning

**Status**: mitigated structurally in the current branch (2026-06-08);
re-measurement pending

**Tables**: `gc_queue`, `gc_pending_items`, `gc_active_orgs`, `gc_dirty_orgs`,
`gc_failed_items`, `gc_queue_counter_reconciliation` is gone — but the
queue/marker tables remain insert+delete churn tables on the default STCS +
`gc_grace_seconds` (10 days).

**Shape**: these are queue-like tables (rows inserted then deleted on
completion/clear). The GC moved off `COUNT(*)` recounts to a throttled per-org
exact recompute (`scanOrgQueueStats`, see
[GC-QUEUE-DEPTH-MODEL.md](GC-QUEUE-DEPTH-MODEL.md)), but that recompute still
**reads** the partition, so it walks tombstones. `DequeueBatch` reads
`queued_at < cutoff` from the front of the queue, exactly where completed-item
tombstones accumulate.

**Risk**: on a hot org (e.g. the platform org `00000000-…-0001`, bucket 0) the
recompute and dequeue paths emit `tombstone_warn_threshold` warnings
(`Read N live rows and M tombstone cells …`). The snapshot throttle
(`minInterval ≈ 60s`) reduces the recompute frequency but does not eliminate the
scan; the dequeue scan runs every worker tick regardless.

**Implemented in this branch**: migration
`003_gc_queue_lcs_compaction.cql` `ALTER`s the queue/marker/DLQ tables to
`LeveledCompactionStrategy`. The baseline schema (`001`) is left untouched and
identical to `main`; the `ALTER` runs against the empty, freshly created tables
during clean boot, so the strategy switch is instant and recompacts nothing:

```cql
WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'tombstone_threshold': '0.1',
    'tombstone_compaction_interval': '600'
}
```

What this actually buys, and what it does not:

- **LCS does the load-bearing work.** These are long-lived hot partitions
  (`gc_queue`/`gc_pending_items` keyed by `(org_id, bucket)`, `gc_active_orgs`/
  `gc_dirty_orgs` keyed by `(bucket)`) that are re-scanned at the same
  coordinates where completed-item tombstones accumulate. LCS reduces read
  amplification there immediately and consolidates the tombstone with the row it
  shadows as it chases the data down the levels.
- **The tombstone knobs are anticipatory, not immediately load-bearing.**
  `tombstone_threshold` only counts *droppable* tombstones — those already past
  `gc_grace_seconds`. With `gc_grace_seconds` kept at the 10-day default, a queue
  item that completes within seconds leaves a tombstone that is non-droppable for
  10 days, so `0.1`/`600s` cannot purge it during that window and a hot org may
  still emit read-path tombstone warnings. The knobs only accelerate purge once
  the tombstone ages past grace (from up to a day down to ~10 minutes).

`gc_grace_seconds` intentionally stays at the default for now to avoid the
multi-node resurrection trade-offs of a low grace. The real lever for silencing
the residual warnings is a lower `gc_grace_seconds` on these idempotent,
leader-only-drained queue tables — that is the deferred operational follow-up,
gated on re-measuring the warnings under multi-node load before changing it.

---

## J. `starred_files` secondary index → reverse-lookup projection

**Status**: resolved in branch `feat/schema-multiregion-hotpath-hardening`
(2026-06-09)

**Tables**: `starred_files`, new `starred_files_by_repo`

**Shape**: the baseline had the schema's only secondary index,
`CREATE INDEX starred_files_by_repo ON starred_files (repo_id)`, used by exactly
one query — GC library-deletion cleanup (`DeleteStarredFilesByLibrary`,
`SELECT ... WHERE repo_id = ?`). Secondary indexes are node-local, so that read
fans out to every node (scatter-gather) and degrades as the cluster grows; it is
a recognised anti-pattern in multi-region.

**Resolved**: replaced with a purpose-built projection table
`starred_files_by_repo ((repo_id), user_id, path)` that serves the same query as
a single-partition read. Kept in sync by dual-write on star/unstar
(`write_helpers.go`) inside a `LoggedBatch`, torn down per-repo on library
cascade and per-row on user cascade (`store_cassandra.go`). No secondary indexes
remain in the baseline schema (asserted by `migrator_test.go`).

---

## K. Group-library creation paths skipped the admin library projections (latent bug)

**Status**: resolved in branch `feat/schema-multiregion-hotpath-hardening`
(2026-06-09)

**Tables**: `libraries_by_org_updated`, `libraries_by_owner`,
`libraries_admin_global_by_updated`

**Shape**: three group-library creation paths
(`groups.go`, `org_admin_groups.go`, `admin_extra.go`) wrote the canonical
`libraries` row and `libraries_by_id` but **not** the admin read-model
projections, unlike the primary create path (`libraries.go`) and the admin
create path (`admin_libraries.go`). Group-created libraries were therefore
silently absent from org-wide library listings and from any projection-based
enforcement (e.g. active-library quota counts), i.e. a quota-undercount /
missing-row bug independent of any partition-key change.

**Resolved**: all three paths now call the shared
`addNewLibraryProjectionQueries` helper (resolves owner display fields and
enqueues the projection upserts in the same `LoggedBatch` as the canonical
write). This also unblocks any future move of org-wide canonical `libraries`
scans (item H) onto the projections, since the projections are now complete.

---

## L. (Evaluated, not pursued) `libraries` canonical PK → per-library partition

**Status**: evaluated and deferred (2026-06-09)

**Idea**: change `libraries` PK from `((org_id), library_id)` to
`((org_id, library_id))` so the head-commit CAS
(`UPDATE libraries ... IF head_commit_id = ?`) contends per-library instead of
per-org Paxos partition, and to push org-wide reads onto projections.

**Why deferred**: the deployment runs `serial_consistency = SERIAL` for global
linearizability. Under SERIAL every head CAS already pays a global cross-DC
Paxos round-trip, which **dominates** latency; per-org ballot contention is only
a secondary effect that bites under concurrent commits to different libraries of
the *same* org. The marginal win does not justify restructuring the most
correctness-critical table (the commit-publish path) and re-pointing six
org-wide readers. If same-org high-concurrency commit bursts ever become a
measured problem, the lower-risk option is a dedicated `library_heads
((org_id, library_id))` CAS table rather than changing the canonical PK. Item H
(org-wide canonical scans / tombstones) remains the tracked follow-up.

---

## M. (Evaluated, not pursued) Shard the per-day admin global projections

**Status**: evaluated and deferred (2026-06-09)

**Tables**: `organizations_admin_by_created`, `users_admin_global_by_created`,
`groups_admin_global_by_created`, `libraries_admin_global_by_updated` and the
`_by_status_created` variants (all keyed by `(bucket_day)` or
`(status, bucket_day)`).

**Idea**: add a `shard` to the partition key so platform-wide creates on a given
day do not all land on today's partition.

**Why deferred**: these projections are written on org/user/group/library
*lifecycle* events, not on a data-path workload. Even at high signup rates a
single per-day partition stays well under Cassandra's soft limits (100 MB /
100k rows). The hotspot only materialises during an extreme one-off (mass import
of millions of accounts), and sharding imposes a permanent read-side
scatter-gather + merge cost. Preferred direction if ever needed: keep the bucket
granularity tunable and shard only when a per-day partition is measured
approaching the soft limit (same playbook as item G).

---

## N. Soft-deleted libraries still accept star/unstar mutations

**Status**: resolved in branch `feat/library-live-write-fencing` (PR #73, 2026-06-09)

**Tables**: `libraries`, `deleted_libraries`, `starred_files`,
`starred_files_by_repo`

**Shape**: library soft-delete keeps the canonical `libraries` row alive with
`deleted_at != null` until GC permanently deletes it. The delete handlers
correctly fence on `deleted_at`, but `StarFile` still only checks that the
library row exists and then dual-writes `starred_files` /
`starred_files_by_repo`. That leaves a real lifecycle window where a client that
still knows the `repo_id` can star a soft-deleted library after the GC cleanup
scan starts.

**Why it matters**: the recent starred-files hardening made GC fail safe on
partial cleanup failure, but it does not close a concurrent post-scan write. A
new star inserted after `DeleteStarredFilesByLibrary` scans
`starred_files_by_repo` can survive long enough to strand a canonical
`starred_files` row without its reverse-lookup projection once the library
cascade finishes. More broadly, repo-scoped mutating endpoints should treat
soft-deleted libraries as non-writable.

**Resolved**: a shared "library is live" guard (`ReadLiveLibraryState` /
`ErrLibraryDeleted`) now fences repo-scoped *create/add* mutations (StarFile,
MonitorRepo, share/upload link create+update, file-share create) and the
permission resolvers, returning 404/PermissionNone for soft-deleted libraries.
Pure *removal* paths (UnstarFile, UnmonitorRepo) intentionally stay unfenced so
clients can still clean up entries pointing at a soft-deleted library.

---

## O. `MoveFileTagsByPath` / `MoveFileTagsByPrefix` are best-effort (no error propagation)

**Status**: open, low-priority (introduced 2026-06-10)

**Tracked issue**: [ISSUE-FILE-TAG-MOVE-BESTEFFORT-01](KNOWN_ISSUES.md#issue-file-tag-move-besteffort-01)

**Tables**: `file_tags`, `file_tags_by_id`, `file_tags_by_tag`

**Shape**: both functions are `void`. On a per-tag batch failure they log and
continue; the file/directory rename that triggered them has already committed in
the FS, so a failed tag move leaves the tag stranded at the old path. Each tag's
move is a single atomic `LoggedBatch`, so the inconsistency is bounded to "some
tags not moved", never a half-moved tag. Stale old-path tags are filtered by
`ListTaggedFiles` (HEAD existence check) and cleaned by the `deleteRepoTag`
canonical fallback / library cascade.

**Direction**: have `MoveFileTagsByPath` return `error`. The caller must NOT fail
the rename (the FS mutation is already durable) — instead log at request level
and/or enqueue a reconciliation so the move is retried out of band. This is an
observability/reconcile hook, not a request-failure path.

---

## P. `DeleteRepoTag` fast path cannot prove projection completeness from cardinality alone

**Status**: open, low-priority (2026-06-10)

**Tracked issue**: [ISSUE-DELETE-REPO-TAG-PROOF-01](KNOWN_ISSUES.md#issue-delete-repo-tag-proof-01)

**Tables**: `file_tags`, `file_tags_by_tag`, `repo_tag_file_counts`

**Shape**: a per-tag counter matching the number of `file_tags_by_tag` rows is
not enough to prove the reverse lookup is complete. A best-effort file rename
can leave a stale old-path projection row while missing the new-path row, so
the count still matches even though the exact set drifted.

**Risk**: using that equality as a fast-path proof can strand canonical
`file_tags` / `file_tags_by_id` rows when deleting a repo tag.

**Direction**: keep `deleteRepoTag` on the canonical exact-set scan until there
is a stronger proof source for projection completeness (e.g. deterministic
reconciliation on failed moves, or a separate exact-set checksum/versioning
scheme). Do not reintroduce the counter-only shortcut.

---

## Q. `MoveFileTagsByPrefix` scans the whole `file_tags` repo partition

**Status**: open, low-priority (introduced 2026-06-10)

**Tracked issue**: [ISSUE-FILE-TAG-PREFIX-SCAN-01](KNOWN_ISSUES.md#issue-file-tag-prefix-scan-01)

**Tables**: `file_tags`

**Shape**: directory rename lists every path in the repo's `file_tags`
partition (`SELECT file_path FROM file_tags WHERE repo_id = ?`) and filters by
prefix in memory. Cost scales with the repo's total tagged files, not the moved
subtree. It is a single-partition read on an infrequent operation (directory
rename), not a hot path.

**Direction**: no new table needed — `file_tags` is clustered by
`file_path` first (`PRIMARY KEY ((repo_id), file_path, tag_id)`), so a clustering
range slice (`WHERE repo_id = ? AND file_path >= ? AND file_path < ?`, upper
bound = prefix + a high sentinel) reads only the subtree. Apply when a repo with
very many tagged files makes directory renames measurably slow.

---

## Closing The Loop

When working any of the items above, add the associated `ISSUE-...-01` ID to
`docs/KNOWN_ISSUES.md` first so the link from this audit to the active work is
traceable. The current upload incident class is closed by the per-block schema
move; the next branch should pick from the table above based on observed
operational pain, not eagerness.
