# GC Queue-Depth Model & Hardening Findings

**Date**: 2026-06-05
**Branch**: `feat/gc-schema-prod-hardening` (after merging `feat/gc-remove-counters`)
**Scope**: how GC tracks queue/DLQ depth, why the design changed, what was
hardened, and the remaining follow-ups. Captures the analysis so the knowledge
is not lost.

---

## The model (current)

GC depth tracking went through three designs:

1. **`gc_queue_stats` counter** (retired earlier): a single Cassandra `COUNTER`
   table. Removed because drift was structural (non-idempotent counter retries,
   TTL-expired queue rows that never fired a DELETE).
2. **Per-(org,bucket) counters + reconciliation queue** (`gc_org_queue_counters`,
   `gc_queue_counter_reconciliation`): cheap hot-path reads, but reintroduced
   counter operational debt (drift, repair machinery, a non-atomic
   counter-update outside the logged batch). **Also retired.**
3. **Dirty snapshot + throttled exact recalc** (current): the model below.

### How it works now

- **Write path** (`EnqueueItem`, `CompleteItem`, `FailItem`, `RequeueItem`, …)
  only mutates canonical rows and writes `gc_active_orgs` + `gc_dirty_orgs`
  markers in the same logged batch. No counters.
- **`gc_org_stats`** holds the per-org snapshot: `queue_depth`, `failed_depth`,
  `oldest_queued_at`, `updated_at`, `recalculated_at`. **Single-writer** — only
  the leader reconciler writes it, which removes the multi-writer counter-drift
  class entirely.
- **`RecalculateOrgQueueStats`** (`scanOrgQueueStats`) computes depth by
  *iterating* rows (`SELECT queued_at FROM gc_queue WHERE org_id=? AND bucket=?`
  over the 32 buckets, `SELECT failed_at FROM gc_failed_items WHERE org_id=?`).
  No `COUNT(*)`.
- **Throttle**: `reconcileSnapshotCandidatesLocked` skips an org whose
  `recalculated_at` is within `minInterval` (`max(2×WorkerInterval, 1m)` ≈ 60s)
  and serves the existing snapshot. The hot path therefore reads the cheap
  snapshot; the tombstone-touching exact recalc is bounded to ≤1×/minInterval
  per org.
- **API split**: `GetOrgQueueStats` (snapshot read) vs
  `RecalculateOrgQueueStats` (exact). Admin paths use
  `refreshOrgQueueStatsNow` (force, single org) so a status/admin call cannot
  accidentally trigger a full exact scan.
- **Drain**: the worker removes drained orgs from `gc_active_orgs` on its
  short-batch path (`len(items) < batchSize`); the reconciler re-`MarkOrgActive`
  on recalc when depth > 0, and a defensive `GetOldestQueuedAt` (LIMIT 1) probe
  prevents a stale snapshot from draining an org with live rows.

### Queue lifecycle arbitration follow-up (2026-08-27)

`DequeueBatch` is deliberately a read and does not lease a row. Multiple workers can
therefore hold the same queue item. `RequeueItem` currently uses the ordinary logged
`DELETE(old)` + `INSERT(new)` batch, while `CompleteItem` and `FailItem` use separate
ordinary batches. A stale requeue can consequently insert a new `queued_at` row after
another lifecycle advanced or removed the old row. This is a known queue-protocol race,
not a guarantee supplied by the depth markers.

The Cassandra 5.0.9 CQL reference documents the relevant conditional-write semantics:
[CQL data manipulation](https://cassandra.apache.org/doc/5.0.9/cassandra/developing/cql/dml.html).

The P4a late-loser path does not enter this generic protocol: a foreign-owner result is
handled at the worker boundary with no queue mutation. A future queue design must choose
one authority across `Requeue`, `Complete`, `Fail`, DLQ and `gc_pending_items`; adding an
LWT only to `RequeueItem` would leave the lifecycle partially ordered.

---

## Hardening applied in this branch

### P1 — `snapshot_age_seconds` / `last_reconcile_run` could lie (FIXED)

**Problem (confirmed against code):** the reconciler persisted
`last_reconcile_run` and reset `gc_snapshot_age_seconds = 0` at the end of
*every* pass, even when all dirty orgs were throttle-skipped and nothing was
recalculated. `Status()` derives snapshot age from `last_reconcile_run`, so a
dashboard could read "0s / fresh" while `gc_org_stats` was actually stale within
the throttle window.

**Fix:** `reconcileSnapshotCandidatesLocked` now tracks `deferred` (candidates
that still needed a refresh but were throttled or errored). Totals and their
gauges still update every pass, but `last_reconcile_run` advances and
`gc_snapshot_age_seconds` resets to 0 **only when `deferred == 0`** (no
known-stale work left). When work was deferred, the gauge is set to the real age
(`now - last_reconcile_run`). This is stricter than "recalculated > 0": an idle
pass with nothing dirty correctly counts as fresh.

### DLQ cleanup stays explicit-only — no Cassandra TTL (decided)

`feat/gc-remove-counters` dropped the `gc_failed_items` table TTL and the
per-reconcile `refreshFailedItemSnapshotLocked` poll, replacing both with the
event-driven `gc_failed_items_by_expiry` projection (scanner deletes at
`failed_at + 30d`, in one batch with the projection + pending marker, and marks
the org dirty so `failed_depth` refreshes).

A 45-day table-TTL "backstop" was briefly added and then **reverted** after
review. Reasons it is the wrong tool here:

- A Cassandra TTL reaps rows **without** marking the org dirty. The drift check
  only *sums* `gc_org_stats` snapshots, so it cannot self-heal a reaped count —
  `failed_depth` would stay stale until the org is dirtied again or an admin
  view forces a refresh.
- It would silently destroy diagnostic `last_error` / `failure_code` history.
- It reintroduces implicit cleanup into a deliberately explicit-expiry design,
  and in healthy operation the explicit path always deletes at 30d first, so the
  45-day TTL never fires anyway (pure dead weight that only "helps" during a
  multi-week scanner outage — exactly when stale counts are the least concern).

**Backstop against unbounded growth during a prolonged scanner outage is
operational**, via the existing `gc_scanner_last_phase_run_timestamp` lag alert,
not an implicit TTL. So `gc_failed_items`, `gc_failed_items_by_expiry`, and the
failed-item pending markers all stay `TTL = 0`.

### `ListOrgsWithFailedItems` top-N now ordered before truncation (FIXED)

The first cut of the [`updated_at` fix] truncated candidates to `limit` ordered
by `(failed_depth, org_id)` *before* reading each org's real last-failure time,
then sorted the page. On `failed_depth` ties at the limit boundary the selection
was biased by `org_id`, not recency — wrong for the admin list and, more
importantly, for `retryAutoRecoverableFailedItems` (`gc.go`), which lists with a
limit, so a recently-failing org could be starved from auto-retry. Fixed by
hydrating last-failure for all candidates (bounded by the small number of orgs
with DLQ items), sorting by `(failed_depth desc, last_failure desc, org_id)`,
then truncating. `MockStore` already ordered-then-limited, so prod now matches
the mock; a service-level contract test
(`TestService_ListFailedItemOrgs_DepthTieBreaksByRecency`) locks the tie-break.
Exact `CassandraStore` parity still needs an integration test (real Cassandra).

### P2 (contract) — `updated_at` in `/admin/gc/failed-items/orgs/` (FIXED)

**Problem:** the endpoint force-refreshes the snapshot before listing, which
rewrites `gc_org_stats.updated_at = now`. The org list then showed that as
"Updated", so the column read ~now for every org and conveyed nothing.

**Fix:** `ListOrgsWithFailedItems` now populates the list's time from the most
recent real failure (`SELECT failed_at FROM gc_failed_items WHERE org_id=?
LIMIT 1`, clustered `failed_at DESC` → cheap single row), bounded to the
returned page. `gc_org_stats.updated_at` keeps its "snapshot updated at" meaning;
the admin list shows "last failure", which is what the UI's "Updated" implies.
The `MockStore` mirrors this.

### Pre-existing flaky test (FIXED)

`TestGCPendingItemBucket_DeterministicAndDistributedByIdentity` asserted a single
random `uuid.New()` library mapped to a different bucket than the original —
a ~1/32 spurious failure. Made deterministic (search up to 64 candidates for a
differing bucket). Unrelated to the depth model; found while verifying.

---

## Remaining follow-ups (documented, not done here)

### P2 — Exact recalc still scans tombstones → compaction branch

Removing `COUNT(*)`/counters did **not** stop the exact recompute (or
`DequeueBatch`) from reading tombstones — it only moved the recompute off the
hot path and throttled it. On a hot org the recompute (~1/min) and the per-tick
`DequeueBatch` can still emit `tombstone_warn_threshold` warnings.

**This is the load-bearing follow-up.** Tracked in
[SCHEMA-BOTTLENECK-AUDIT.md](SCHEMA-BOTTLENECK-AUDIT.md) item I — branch
`gc_queue-lcs-compaction`: `LeveledCompactionStrategy` +
`tombstone_threshold=0.1` + `tombstone_compaction_interval=600` on the
queue/marker tables, leaving `gc_grace_seconds` at default initially.

### P2 — `dirty.marked_at` vs `recalculated_at` is a cross-node wall-clock compare

The reconciler clears a dirty marker when `recalculated_at` is not before
`dirty.marked_at`. Correct with sane clocks, but the two timestamps can be
written by different nodes. Under clock skew it could clear a dirty marker for a
mutation the snapshot didn't capture. **Not data loss** (canonical rows remain
the source of truth; the next mutation re-marks dirty), but it can leave metrics
briefly stale. **Action:** require NTP/clock sync (document as an operational
prerequisite), or revisit if skew is ever observed.

### P2 — `minInterval = 60s` may be low for dev/CI

`gcMinSnapshotRecalcInterval = 1m`, effective `max(2×WorkerInterval, 1m)`. With
frequent test activity a hot org can still be scanned ~1×/min. **Action:**
consider making it configurable (`GC_SNAPSHOT_RECALC_MIN_INTERVAL`) and raising
the default in dev/CI. Not a production blocker.

### P3 — `gc_failed_items_by_expiry` may be simplifiable

With counters gone, explicit DLQ expiry is no longer *required* to decrement a
counter; it is kept because it provides event-driven dirty-marking (keeps
`failed_depth` fresh without polling) and same-batch deletion of the failed row +
pending marker. Reverting to pure TTL would reintroduce a per-reconcile
failed-depth poll. **Decision: keep as-is.** Re-evaluate only if the projection
proves to be operational overhead.

### P3 — Recompute iterates all 32 buckets even when few hold rows

`scanOrgQueueStats` reads all `gcDefaultQueueBucketCount` buckets per org → up to
~30 empty reads for a small org. Noise, not a data problem. Leave unless metrics
justify a bucket bitmap/hint.

### P3 — Multi-table enqueue batch unchanged

`EnqueueBatch` still groups `gc_queue` + `gc_pending_items` + `gc_active_orgs` +
`gc_dirty_orgs` into one logged batch and can trip Cassandra's batch-size warning
for large chunks. Pre-existing, out of scope here. Future: coalesce markers and
cap real batch size.

---

## Multi-node / multi-instance safety summary

- `gc_org_stats` is **single-writer** (leader reconciler) → no counter-style
  multi-writer drift.
- Depth correctness always derives from canonical rows (`gc_queue`,
  `gc_failed_items`); snapshots are an optimization, not a source of truth.
- DLQ cleanup is event-driven (leader scanner) with a TTL backstop; a TTL reap
  self-heals in the snapshot.
- The one cross-node assumption is wall-clock comparison for dirty-marker
  clearing (see P2 clock-skew item) — bounded, non-destructive.
