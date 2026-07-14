# GC Library-Delete Cleanup — Investigation & Follow-up Debt

**Date:** 2026-07-09 (original) · **Audit verified and corrected:** 2026-07-10
**Code audited at:** `4333be1b3` (merge of PR #123 into `main`)
**Branch where found:** `fix/gc-block-representation-durability` (PR #123)
**Status:** partially fixed on PR #123 (representation stamping + Phase 13 recovery);
the remaining items below are **out of scope for #123** and are being closed on a
series of small, auditable follow-up branches. This document is the **canonical audit
record**; the corrected verdict, the confirmed production-gap table (P1–P8), the
architectural invariants, and the branch roadmap live in the
[**"Cross-agent audit — verified verdict"**](#cross-agent-audit--verified-verdict-2026-07-10)
section at the end. The `F1`–`F4` findings below are the original notes, now annotated
with their verified status.

> **TL;DR of the corrected audit:** the physical block-delete protocol in isolation is
> conservative. The end-to-end GC had a **High fail-open existence check** — a transient
> Cassandra error could be read as "library missing", allowing destructive work for a live
> library without the delete guard — **now fixed** (P6a / branch 1D, 2026-07-10: existence
> reads propagate non-`ErrNotFound` errors and Phases 3/4/9 fail closed, with regression tests).
> The narrower **P6b (Medium)** execution-time gap is now **fixed**: Phase 3/4 orphan work
> carries durable `canonical_absent` semantics, and the worker point-reads canonical
> `libraries[(org_id, library_id)]` under the existing library lock before destructive work.
> Canonical presence cancels deletion; read/fence errors fail closed. Tracked as resolved in
> `ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01`.
> Test residue is also affected by global GC test activity. There are additional real
> reclamation gaps
> (org-admin trash deletes defer cleanup up to the retention window; non-durable goroutine
> handoff; stale policy index; `pub:` refs lack a discoverable zero-ref transition; Phase 13
> logs delivery failures but does not propagate them to the phase result/metrics) — tracked
> as `ISSUE-GC-*` in `docs/KNOWN_ISSUES.md`. P1–P5/P7 concern delayed or missing
> reclamation; the safety-classification findings P6a and P6b are fixed.

### Audit scope and evidence

- Static review of delete writers, scanner phases, queue/pending/DLQ lifecycle, worker block
  deletion, S3 recovery, representation validation, and integration-test cleanup.
- Direct Cassandra/MinIO inspection recorded in the residue table below.
- Unit suite: `go test ./...` passed at the audited commit. Integration tests require the
  `integration` build tag and an external backend, so they are not part of that command.
- The dev environment used by the suite had `GC_ENABLED=true`. The harness does not start or
  own the backend service, but tests explicitly call `/api/v2.1/admin/gc/run`, and one normal
  integration test constructs a global `Worker` directly. Reproduction must record GC enabled,
  grace-period, retention, and backend lifetime; otherwise post-suite residue is not comparable.

## Context

While validating the PR #123 fix for "soft-deleted libraries stuck permanently in
trash", we ran the full test suite against a clean dev cluster and inspected
Cassandra + MinIO directly. The admin dashboard correctly shows **0 libraries /
0 bytes**, and that is faithful: `libraries` and every admin projection
(`libraries_by_id`, `libraries_by_owner`, `libraries_by_org_updated`,
`libraries_admin_global_by_updated`, `libraries_deleted_by_org`) are all `0`, and
every `storage_counters` scope reads `0` bytes.

**But the physical storage is not empty**, and that revealed cleanup gaps that are
broader than the representation-stamping bug PR #123 fixes.

### Dev cluster access (for reproducing)

- Cassandra: `docker exec sesamefs-cassandra-1 cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD"` (keyspace `sesamefs`). Creds in `.env` (`CASSANDRA_SUPERUSER_PASSWORD`, app user `sesamefs_app`).
- MinIO: `docker exec sesamefs-minio-1 mc ...` (root creds in `$MINIO_ROOT_USER`/`$MINIO_ROOT_PASSWORD`). Buckets: `sesamefs-blocks/eu/usa/china/archive`.

### Observed residue after a clean-DB full test run (2026-07-09 ~22:06)

| Table | Count | Notes |
| --- | --- | --- |
| `libraries` (+ all projections) | 0 | dashboard correct |
| `storage_counters` | all 0 bytes | accounting correct |
| `deleted_libraries` | 0 | GC markers fully drained (the #123 fix works) |
| `gc_queue` / `gc_failed_items` / `gc_block_candidates` | 0 | GC believes it is done |
| `blocks` | 13 | physical blocks still present |
| `block_references` | 7 | 3 provisional + 4 permanent |
| `block_id_mappings` | 9 | |
| `commits` | 4 | |
| `gc_provisional_block_refs` | 12 | expire 2026-07-11 (2-day TTL) |
| `gc_libraries_by_policy` | 2 | stale, libraries gone |
| MinIO objects | 17 | not empty |

## Findings

### F1 — [High] Permanent block references to deleted libraries leak blocks + S3 objects

> **VERIFIED STATUS (2026-07-10): reframed — partly test artifact, partly delayed-not-eternal.**
> Of the 4 "permanent references to gone libraries", the two `pub:foreign-*` rows are a
> **pure test artifact** — `TestWebBlockUploadForeignPubRefNotPermanent`
> ([web_block_upload_test.go:949](../internal/integration/web_block_upload_test.go#L949))
> injects them with **no TTL and no `t.Cleanup`**. Production `pub:` refs carry a 35-day
> TTL. The two `fs:` rows in this specific snapshot are **not Phase-13 recoverable** because
> the same snapshot has `deleted_libraries = 0`. They are pre-#123/test-created residue or
> work whose durable identity was already removed. More generally, marker-present direct
> deletes are recoverable-but-delayed because `GetLibraryDeletedAt` reads
> `deleted_libraries` ([store_cassandra.go:1165](../internal/gc/store_cassandra.go#L1165)).
> Markerless artifacts are not discovered by current Phase 3/4 (P7) and require a durable
> discovery projection or reconcile/backfill.

Of the 7 `block_references`, 3 are provisional (`up:` / `up:sync:` referrers, 2-day
TTL → self-clean, **not** a leak) and **4 are permanent references to libraries
that no longer exist**:

```
fs:24236472-…:97dbb4a…            library 24236472 (gone)
fs:cc63584e-…:700301c9…           library cc63584e (gone)
pub:foreign-1783634787505051209   library e6afcaf3 (gone)
pub:foreign-1783633981107965697   library ce4beeb1 (gone)
```

A block keeps a `block_references` row per referrer; a block only becomes a GC
candidate at **zero references**. These 4 blocks keep a permanent reference whose
library is already hard-deleted, so they never reach zero-ref, never enter
`gc_block_candidates`, and their `blocks` row + MinIO object leak forever.

Root cause: the direct library hard-delete paths delete the `libraries` row but do
**not** remove the library's permanent block references (`fs:` refs from committed
fs_objects, `pub:foreign:` refs from the published/CAS foreign-block path). Those
refs are normally removed by the GC cascade
(`removeFSObjectBlockReferences` per fs_object). If the cascade never ran for the
library — because the content was never enqueued (the permanent-delete
content-leak, see F4) — the refs survive the library.

`pub:foreign:` refs specifically come from the CAS / web-block-upload publish flow
and may have their own cleanup path that is not wired to library deletion.

### F2 — [Med] `gc_libraries_by_policy` stale entries after direct delete

> **VERIFIED STATUS (2026-07-10): confirmed, benign.** The policy-index rows are
> maintained by the policy-setting endpoints (`library_settings.go` disabling
> `version_ttl`/`auto_delete`, `admin_libraries.go` history edit), by `rollbackNewLibrary`
> ([write_helpers.go:872-873](../internal/api/v2/write_helpers.go#L872)), and by the GC
> cascade's `HardDeleteLibrary`
> ([store_cassandra.go:3816-3817](../internal/gc/store_cassandra.go#L3816)). The confirmed
> gap is that the shared **direct hard-delete helper** `hardDeleteLibraryRowsFn`
> ([library_delete_helpers.go:29-41](../internal/api/v2/library_delete_helpers.go#L29))
> does **not** include the `AddDeleteLibraryPolicyQuery` deletes, so direct-delete paths can
> leave stale index rows. Scanner re-validates the `libraries` row and skips on
> `ErrNotFound`, so no mis-processing — just accumulation. Tracked as
> `ISSUE-GC-POLICY-INDEX-STALE-01` (branch 2).

Two `gc_libraries_by_policy` rows (`version_ttl=90`) reference libraries that no
longer exist (`80d85d6f…`, `4dbca9d4…`).

Root cause confirmed in code: `db.AddDeleteLibraryPolicyQuery` (which removes the
policy index row) is called from the GC cascade
(`store_cassandra.go` `HardDeleteLibrary`), from settings/TTL edit endpoints, and
from `write_helpers.go` hard-delete helper — but **not** from the direct delete
paths `deleted_libraries.go` (`PermanentDeleteRepo`), the `admin_libraries.go`
clean-trash batch, or `org_admin_repos.go`. A policy-bearing library deleted via
those paths (without going through the cascade) leaks its policy index row.

Impact: **benign** — the scanner re-reads the `libraries` row for each policy
entry and `continue`s on `ErrNotFound`, so stale entries are skipped, not
mis-processed. But they accumulate and add scan work. Same *class* of gap as the
`deleted_libraries.block_representation_id` stamping gap fixed on #123, in the same
delete paths.

### F3 — [Med] Tests may bypass canonical delete paths and manufacture drift

> **VERIFIED STATUS (2026-07-10): mixed.**
> `gc_block_representation_resolve_test.go` intentionally constructs unstamped legacy
> markers and removes them through fixture cleanup; that raw CQL is a legitimate part of the
> scenario. `library_projection_regression_test.go`, however, still has a **failure-only**
> restore path (`if !t.Failed() { return }`) that re-inserts `deleted_libraries` **without**
> `block_representation_id`
> ([library_projection_regression_test.go:2090-2094](../internal/integration/library_projection_regression_test.go#L2090)) —
> so a failing run can recreate an unstamped marker in the shared keyspace. Separately, the
> **dominant physical residue** comes from (a)
> `TestWebBlockUploadForeignPubRefNotPermanent` injecting a permanent fake `pub:` ref with no
> cleanup ([web_block_upload_test.go:949](../internal/integration/web_block_upload_test.go#L949))
> and (b) E2E tests that upload **real** blocks to MinIO with no exact S3 teardown. The harness
> does not start/control the external GC service, but it triggers global worker/scanner runs,
> and `TestAdminIdentityProjectionRegression_HardDeleteOrganization` calls global
> `Worker.ProcessOnce()` with `storage=nil`. That can drain unrelated work and remove
> Cassandra block metadata without deleting its S3 object. Root cause is a **shared keyspace
> + shared bucket plus non-isolated/global cleanup**, not "the daemon never runs". Tracked as
> `ISSUE-GC-TEST-RESIDUE-01` (branches 1A/1B/1C).

Several tests write `deleted_libraries` (and delete `libraries`) with **raw CQL**
instead of the canonical helpers, e.g.:

- `internal/integration/library_projection_regression_test.go:2039` — `INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class)` (no `block_representation_id`)
- `internal/integration/gc_block_representation_resolve_test.go:76` — same, unstamped

Because these bypass `softDeleteLibrary` / the permanent-delete handlers, they
create exactly the unstamped markers and orphaned references we are trying to
eliminate, and they run against the shared dev keyspace. This both (a) pollutes
the dev DB with drift and (b) means the tests are not exercising the real
production write paths. Any test that creates or deletes a library for a
non-representation reason should route through the canonical write helpers (or a
shared test helper that mirrors them) rather than hand-rolled CQL.

**Action for the follow-up branch:** audit every raw `INSERT INTO
deleted_libraries` / `DELETE FROM libraries` / raw `block_references` write under
`internal/**/*_test.go` and either route through canonical helpers or justify +
stamp them explicitly.

### F4 — [High, FIXED on #123] Permanent-delete content leak

> **VERIFIED STATUS (2026-07-10): still FIXED.** The representation-stamping fix stands.
> The residual concern F4 hinted at is split into the durability gaps P1/P2 below.

`PermanentDeleteRepo` deletes the `libraries` row and then (in a goroutine) calls
`EnqueueLibraryDeletion` → `EnqueueLibraryContents` → resolves the representation
against the now-missing row → fails → contents never enqueued → blocks/fs_objects/
commits leak. PR #123 mitigates this by stamping the marker before the delete so
resolution can fall back to it. F1 shows there is still residual leaked content
from *before* the fix / from other paths, and that the block-ref removal itself is
not part of the delete path.

## What PR #123 fixed (keep)

- `deleted_libraries.block_representation_id` is now stamped at all production
  delete writers (`write_helpers.softDeleteLibrary`, `deleted_libraries.go`,
  `admin_libraries.go`, `org_admin_repos.go` ×2).
- New `db.ResolveBlockRepresentationIDForDelete` reads the row even when
  `deleted_at` is already set (permanent delete acts on an already-trashed lib).
- GC Phase 13 **recovers** an unstamped marker from the surviving library row
  instead of fail-closed-skipping it, and emits `gc_library_representation_defaulted`.
- Phase 13 recovery must be **kept** even after all writers stamp correctly, to
  repair pre-deploy / legacy / partial-failure markers.

## Original proposed work (historical; superseded where noted below)

> This section preserves the original investigation. Its suggestion that either the library
> cascade or publish subsystem might own `pub:` cleanup is superseded by verified invariant #2:
> publish/expiry is the sole owner; the library cascade must never remove `pub:` refs.

1. **Centralize the library-delete entry point**, but do NOT model the whole
   cleanup as one atomic Cassandra batch. Releasing refs, moving blocks to
   candidates, deleting policy indexes and enqueuing content is variable-size,
   multi-partition, includes queue/S3 work, and overlaps the existing cascade —
   a single "atomic" batch is neither possible nor safe, and doing ref removal in
   the delete path AND again in `removeFSObjectBlockReferences` risks a
   double-decrement / double zero-ref transition unless idempotency is exact.
   Instead, the delete entry point should do only the small, synchronous,
   idempotent-safe part (resolve+validate representation, stamp marker, delete the
   library row + `gc_libraries_by_policy` rows) and then hand off to a **durable,
   idempotent cascade** that owns all content release:
   ```
   hard-delete marker stamped (+ policy index removed)
     → durable library_cascade queued
       → cascade enumerates contents
       → releases each `fs:` ref exactly once
       → zero-ref blocks become candidates
       → physical block + S3 deletion
       → final cleanup
   ```
   Prefer a durable outbox/queue over fire-and-forget goroutines for the enqueue.
2. **`pub:` ownership decision (resolved by the verified audit):** publish/expiry owns
   `pub:` cleanup. The library cascade must not release it.
3. **Audit tests** (F3): route library create/delete through canonical helpers.
4. **Reconcile job / backfill** for already-leaked blocks + S3 orphans and stale
   policy rows on existing clusters.
5. **N+1 in bulk cleaners** (see PR #123 review [Med/Low]): fold `encrypted` +
   `block_representation_id` into the trash-listing query instead of one extra
   read per library.

## One-off dev cleanup commands (test cruft only)

```bash
docker exec sesamefs-cassandra-1 cqlsh -u cassandra -p "$CASSANDRA_SUPERUSER_PASSWORD" \
  -e "TRUNCATE sesamefs.gc_libraries_by_policy;"
# Leaked blocks/refs/objects need a proper reconcile; TRUNCATE-ing block_references
# without deleting the S3 objects would orphan MinIO further — do a real GC/reconcile
# pass on the follow-up branch instead of blind truncation.
```

---

## Cross-agent audit — verified verdict (2026-07-10)

This section consolidates independent code-path reviews and direct Cassandra/MinIO
inspection. Every confirmed claim below points to reproducible code or storage evidence at
the cited `file:line`. The originating investigation asked two questions — *is the GC safe?*
and *is it optimal?* — and the corrected answer is: **the physical block-delete protocol is
conservative. The transient-error fail-open in existence reads (P6a) that could misclassify a
live library is fixed (P6a). Already-enqueued orphan work is now revalidated against the
canonical `libraries` row at execution time (P6b fixed), with fail-closed reads and synchronous
fencing before destructive mutations. Durability/discovery gaps (P1–P5/P7) remain.**

### Verdict 1 — physical block deletion is conservative; P6a and P6b classification guards are fixed

The protocol that deletes a physical block is conservative and crash-safe (no path in the
reviewed sequence deletes a live block, though a code audit cannot prove a universal
"never") — [worker.go:386-557](../internal/gc/worker.go#L386), `store_cassandra.go`:

1. Pre-check refs; skip if any exist.
2. Claim via LWT (`gc_state='deleting'`, stable `claimID`).
3. **Re-check refs after the claim** (claim-then-verify); release + abort if a ref appeared.
4. Register recovery in `gc_s3_orphans` **before** deleting the canonical `blocks` row
   (closes the crash window between Cassandra and S3).
5. Delete the DB row (LWT-guarded) → delete S3 with exponential retry → clean the mapping.
6. Resurrection guard: if the same `block_id` was re-uploaded, the live row owns the mapping
   and the recovery is discarded instead of deleting.

Representation is validated fail-closed (`CanonicalBlockRepresentationIDForLibrary` derives the
one legal representation from identity + `encrypted` and rejects any stored value that would
cross the plain↔encrypted mapping domain). Provisional `up:` refs auto-expire (48h TTL) and are
re-discovered by scanner Phase 0.

> **Scope boundary:** this sequence still depends on correct discovery. P6a now fails closed on
> transient existence-read errors, and P6b revalidates queued orphan work against the canonical
> library under a fenced library lock. P7 remains separate: markerless artifact partitions that are
> never discovered cannot benefit from the worker guard.

### Verdict 2 — test residue is dominated by non-isolated fixture/GC behavior

- `TestMain` does not start or own the external `Service`, but the dev backend is GC-enabled and
  integration tests explicitly trigger the global worker/scanner via `/admin/gc/run`.
- `TestAdminIdentityProjectionRegression_HardDeleteOrganization` uses global `ProcessOnce()`
  with `storage=nil`
  ([admin_identity_projection_regression_test.go:333](../internal/integration/admin_identity_projection_regression_test.go#L333)).
  Unlike scoped `ProcessOrgOnce`, it may process unrelated active orgs; a nil-storage worker
  removes DB metadata without deleting S3. This plausibly contributes to MinIO objects without
  matching Cassandra rows.
- **`TestWebBlockUploadForeignPubRefNotPermanent`** inserts a `pub:foreign-<ts>` ref with **no
  TTL and no `t.Cleanup`** and deletes the session's `up:` ref
  ([web_block_upload_test.go:945-950](../internal/integration/web_block_upload_test.go#L945)) —
  these are exactly the two "permanent" `pub:foreign-*` rows in the residue table. They do **not**
  come from the production writer (which stamps a 35-day TTL,
  [block_references.go:38](../internal/db/block_references.go#L38)).
- No global `TRUNCATE`/teardown of keyspace or MinIO; cleanup is only by ephemeral library name.
  Blocks uploaded under synthetic `org_id`/`library_id` with no `t.Cleanup` persist forever.

### Verdict 3 — confirmed production gaps (P1–P8)

| # | Verified finding | Sev | Evidence | Issue |
|---|---|---|---|---|
| P1 ✅ **FIXED (6A/6B + edge review + org-admin parity)** | Permanent delete hard-deleted the library **without** promptly reclaiming content/counter/tags. **Fixed 2026-07-13/14:** a single shared writer stamps `deleted_libraries.purge_requested_at` (migration 012), so Phase 13 is eligible on its next scan; all wired permanent-delete paths (v2.1 owner + platform + org-admin single/bulk) additionally enqueue the durable `ItemLibraryCascade` immediately (deduplicated against Phase 13) so reclamation starts on the next worker tick rather than after a full `ScanInterval`. The cascade owns content/counter/tags/policy/marker. | High (fixed) | [library_delete_helpers.go](../internal/api/v2/library_delete_helpers.go), [gc.go `EnqueueLibraryCascade`](../internal/gc/gc.go) | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` |
| P1b ✅ **FIXED (6A/6B + edge review)** | The marker used to be re-inserted with `deleted_at = time.Now()`, resetting the retention clock. **Fixed:** eligibility comes from `purge_requested_at` (independent of `deleted_at`), and the writer now **preserves the original `deleted_at`** so the `library_cascade` dedup identity is stable (no double-enqueue). The grace gate is preserved and measured from the original trash time. | High (fixed) | [library_delete_helpers.go](../internal/api/v2/library_delete_helpers.go) | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` |
| P2 ✅ **FIXED (6A/6B + edge review + org-admin parity)** | Was: non-durable, content-only handoff (`EnqueueLibraryDeletion` via `go fn()`, contents not `ItemLibraryCascade`). **Fixed 2026-07-13/14:** correctness is durable via `purge_requested_at` + Phase 13; all wired permanent-delete paths (v2.1 owner + platform + org-admin single/bulk) call `Service.EnqueueLibraryCascade` immediately, which is byte-for-byte identical to Phase 13's enqueue (a dedup no-op, not a second producer) and best-effort — a lost goroutine costs only latency, never cleanup. The legacy `/api2/repos/deleted/:repo_id` route (`libHandler=nil`) relies on marker + Phase 13. | High/Med (fixed) | [library_delete_helpers.go](../internal/api/v2/library_delete_helpers.go), [gc.go `EnqueueLibraryCascade`](../internal/gc/gc.go) | `ISSUE-GC-DELETE-HANDOFF-DURABILITY-01` |
| P3 | `gc_libraries_by_policy` is not cleared on direct delete. Policy-setting endpoints, `rollbackNewLibrary`, and the cascade's `HardDeleteLibrary` all maintain their policy rows, but the shared direct hard-delete helper does not include the `AddDeleteLibraryPolicyQuery` deletes. Benign (scanner re-validates and skips) but accumulates. | Med | [library_delete_helpers.go:29-41](../internal/api/v2/library_delete_helpers.go#L29) vs [store_cassandra.go:3816](../internal/gc/store_cassandra.go#L3816) | `ISSUE-GC-POLICY-INDEX-STALE-01` |
| P4 | `pub:` refs have no discoverable expiry projection (`up:` has one via `gc_provisional_block_refs` + Phase 0). When the last `pub:` expires by Cassandra TTL (35d), nothing runs the zero-ref → `EnsureBlockGCCandidate` transition; `scanOrphanedBlocks` only walks already-created candidates. Block + mapping + S3 can be retained. Confirmed by protocol analysis; no production incident demonstrated. | Med | [block_references.go:339](../internal/db/block_references.go#L339); [scanner.go:335](../internal/gc/scanner.go#L335) | `ISSUE-GC-PUB-REF-ZERO-REF-01` |
| P5 | Phase 13 **logs** delivery failures but does not surface them: on `EnqueueBatch` failure it logs and returns `nil`; on per-library `PendingItemExists` failure it logs and `continue`s. The failure is invisible to the phase result, health, and dedicated metrics, so the overall scan cycle can appear successful. | Med | [scanner.go:1194-1197,1231-1242](../internal/gc/scanner.go#L1194) | `ISSUE-GC-PHASE13-ERROR-VISIBILITY-01` |
| P6a ✅ **FIXED (1D)** | **Fail-open live-library classification (transient errors).** `LibraryExists`/`GroupExists` mapped every read error to `(false,nil)`, so a transient Cassandra error could make Phases 3/4 enqueue a live library's commits/fs_objects and Phase 9 delete a valid group share. **Fixed 2026-07-10:** existence reads return `(false,nil)` only for `gocql.ErrNotFound` and propagate all other errors; Phases 3/4/9 fail closed and surface the error. Phase 9 scans `shares_by_group` directly, uses each projection row's `OrgID`, and has unit plus real-Cassandra regression coverage. | **High** | [store_cassandra.go:2270-2283](../internal/gc/store_cassandra.go#L2270), [scanner.go:531-546](../internal/gc/scanner.go#L531), [scanner.go:607-622](../internal/gc/scanner.go#L607), [scanner.go:1030-1072](../internal/gc/scanner.go#L1030), [store_cassandra.go:3182-3222](../internal/gc/store_cassandra.go#L3182) | `ISSUE-GC-EXISTENCE-CHECK-FAILOPEN-01` |
| P6b ✅ **FIXED (1E)** | **Execution-time canonical revalidation of orphan AND cascade work.** Phase 3/4 persist `canonical_absent`; cascade children persist `deleted_at_identity`; retry/DLQ preserve the mode and legacy booleans remain compatible. The worker takes the existing library lock, point-reads `libraries[(org_id, library_id)]`, cancels on presence, fails closed on errors/unknown modes, and synchronously renews the token before destructive commit/fs_object/reference mutations. Crucially, a `deleted_at_identity` child also requires the canonical row to be **absent**: a matching delete marker does not prove the parent finished `HardDeleteLibrary`, so if the parent crashed mid-cascade (marker + canonical both survive, lease stolen while stale) the child **postpones** (no retry burn, no DLQ) instead of purging a still-restorable library. Library cascade, org cascade, and restore all take the same library lock. Restore reuses the same stale-aware lease, re-reads the canonical row under the lease (present+soft-deleted ⇒ restore; absent ⇒ reject; active ⇒ reject), and fences before its batch — so restore can never revive a partially-purged library. Mixed-version rollout note: pause GC across the fleet, deploy the migration and new binaries everywhere, verify no old workers remain, then re-enable GC. P7 discovery remains separate. | Med (fixed) | [scanner.go:576-586](../internal/gc/scanner.go#L576), [worker.go:1599-1700](../internal/gc/worker.go#L1599), [write_helpers.go:977-994](../internal/api/v2/write_helpers.go#L977) | `ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01` |
| P7 | **Orphan phases cannot discover markerless artifact libraries.** `ListDistinctCommitLibraries` / `ListDistinctFSObjectLibraries` do not enumerate commits/fs_objects; both return the union of `libraries_by_id` + `deleted_libraries`. Once both library rows are gone, surviving commits/fs_objects are invisible to Phases 3/4. This matches the snapshot's `libraries=0`, `deleted_libraries=0`, `commits=4`. | High/Med | [store_cassandra.go:2235-2268](../internal/gc/store_cassandra.go#L2235) | `ISSUE-GC-ORPHAN-ARTIFACT-DISCOVERY-01` |
| P8 | **Phase 9 group-share discovery still performs a global table scan.** The immediate fix streams `shares_by_group` with context and 256-row driver pages, so process memory is bounded and cancellation works, but Cassandra still reads every partition each cycle. Replace it with a bucketed active-partition projection plus reconcile/backfill. | Med (performance/operability) | [store_cassandra.go:3182](../internal/gc/store_cassandra.go#L3182) | `ISSUE-GC-GROUP-SHARE-DISCOVERY-SCAN-01` |

### Precisions (they confirm, they refine)

1. All permanent-delete entry points now share the durable `purge_requested_at` writer. The
   immediate best-effort `library_cascade` enqueue is wired on the v2.1 owner path plus the
   platform/org-admin delete paths; the legacy `/api2/repos/deleted/:repo_id` registration still
   mounts the shared handler with `libHandler=nil`, so it relies on the durable marker + Phase 13
   recovery instead. Where the enqueue is wired, it mirrors Phase 13 exactly (`QueuedAt =
   IdentityAt = deleted_at`, nil library id, same representation), so a later scan is a dedup
   no-op and a lost goroutine costs only latency.
2. The retention clock is no longer reset. `deleted_at` is preserved as the original trash time,
   while `purge_requested_at` is the separate Phase 13 eligibility signal.
3. A cascade can run after `libraries` is gone **only while its deleted marker/queued identity
   remains discoverable**, because `GetLibraryDeletedAt` reads `deleted_libraries`
   ([store_cassandra.go:1165](../internal/gc/store_cassandra.go#L1165)). The snapshot-specific
   `fs:`/commit residue had no marker and is not discoverable by current Phases 3/4 (P7).

### What PR #123 fixed and MUST be kept

Stamping `block_representation_id` before hard-delete at every production writer;
`ResolveBlockRepresentationIDForDelete` (reads the row even with `deleted_at` set); Phase 13
recovery from the live row. **Phase 13 recovery stays permanently** even once all writers stamp
correctly — it repairs legacy / partial-failure markers.

### Architectural invariants that must NOT be broken

1. The API **never** removes `fs:` refs. Sole owner: the `fs_object` cascade (`removeFSObjectBlockReferences`).
2. The library cascade **never** removes `pub:` refs. Owner: publish / expiry scanner.
3. Zero-ref does **not** mean delete directly → it means `EnsureBlockGCCandidate`.
4. The worker keeps the LWT claim + final re-validation before any S3 delete.
5. Never truncate `block_references`/mappings to clean a cluster (it orphans MinIO).
6. Tests **must not** run a global GC over the shared keyspace; current violations are tracked
   by `ISSUE-GC-TEST-RESIDUE-01`. Use fixture-scoped cleanup/`ProcessOrgOnce` only.
7. PR #123 representation recovery is kept always.

### Engine-level review — worker/scanner robustness (2026-07-10)

A second pass reviewed the worker/scanner engine (not the delete *paths*) for races,
liveness, and crash recovery. Physical block crash recovery is **solid** (write-ahead
`gc_s3_orphans` before finalize, persisted scanner cursors, grace period, LWT block claims,
independent lease renewal, claim-then-verify, double stale-check for cascades). Most items
below are fragility/observability; former E3 was escalated to P6 because the Cassandra store
swallows existence-check errors and the resulting destructive work is unguarded.

| # | Confirmed finding | Sev | Evidence |
|---|---|---|---|
| E1 | `postponeItem` re-queues a lock-contended (`hard_delete_in_progress`) item with `RetryCount` **unchanged** — intentional (lock contention should not push toward the DLQ), but there is no postpone bound and no dedicated metric, so a permanently stuck hard-delete lock ⇒ infinite postpone with no DLQ/alert. | Low (liveness/obs) | [worker.go:345-358](../internal/gc/worker.go#L345) |
| E2 | `dryRun` is accessed concurrently without synchronization, which is a Go data race. `atomic.Bool` fixes visibility/race-detector correctness, but does **not** provide hard cutover for work already past its dry-run check; that requires worker drain/serialization or destructive-step rechecks. | Low/Med | [gc.go:1141-1146](../internal/gc/gc.go#L1141), [worker.go:386-390](../internal/gc/worker.go#L386) |
| E3 | **Escalated to P6a → ✅ fixed (1D).** Scanner code appeared to skip on `LibraryExists` errors, but the Cassandra implementation never returned them: it mapped every failure to "missing" (fail-open, not merely invisible accumulation). Closed by the P6a fix; the separate execution-time revalidation gap was subsequently closed by P6b/1E. | **High** | [store_cassandra.go:2270-2283](../internal/gc/store_cassandra.go#L2270) |
| E4 | `gc_pending_items` has no TTL, but normal queue completion, DLQ deletion, and DLQ expiry explicitly remove the pending row in logged batches. The unconfirmed risk is projection drift after historical/manual/ambiguous mutations; there is no independent reconcile/backstop. Do **not** add a blind TTL, which could expire valid dedup protection while queue/DLQ work remains live. | Low (audit/reconcile) | [store_cassandra.go:499-525](../internal/gc/store_cassandra.go#L499), [store_cassandra.go:555-583](../internal/gc/store_cassandra.go#L555), [001_initial_schema.cql:1199](../internal/db/migrations/001_initial_schema.cql#L1199) |
| E5 | `RecoverS3Orphans` has no per-row LWT; mutual exclusion relies entirely on the leader lease. Real double-processing risk only under a lease split-brain (single-instance/dev: none). | Low (defense-in-depth) | [worker.go:599-772](../internal/gc/worker.go#L599) |

**Reviewed and found NOT to be bugs** (recorded so they are not re-raised):

- **"Infinite DLQ↔queue loop when the library marker is deleted mid-cascade" — not a bug.**
  `processLibraryCascade` returns `nil` on the `deleted_at == nil` skip, and a `nil` result
  makes `processOrg` call `Complete()`, which **removes** the item from the queue
  ([worker.go:1199-1202](../internal/gc/worker.go#L1199) → [worker.go:311](../internal/gc/worker.go#L311)).
  Phase 13 lists from the `deleted_libraries` marker, so a gone marker is not re-listed. No loop.
- **"Resurrection guard missing for the `pending_s3` phase" — not a bug.** The `pending_s3`
  branch checks `BlockExists` **before** the S3 delete and defers if the canonical row exists
  (resurrected block) — [worker.go:695-708](../internal/gc/worker.go#L695). The guard covers
  both recovery phases.
- **"Race between child enqueue and parent delete in `processCommit`/`processFSObject`" —
  mitigated.** The child items carry `RequiresLibraryDeletedCheck`; their own
  `acquireLibraryDeleteGuard` catches restore (marker gone → `LibraryExists` → stale skip) and
  re-delete under a different `identityAt` (`deleted_at != identityAt` → stale skip) —
  [worker.go:1586-1616](../internal/gc/worker.go#L1586). The parent delete runs under a held
  hard-delete lease on a soft-deleted library.

E1/E2/E4/E5 remain in `ISSUE-GC-ENGINE-ROBUSTNESS-01`; E3 is superseded by P6. The old
single branch 10 is split into 10A–10E so each semantic decision remains auditable.

### Branch roadmap (small, auditable PRs)

This document (branch `docs/gc-delete-cleanup-audit`) is **branch 0** — documentation only.
Subsequent branches, in recommended merge order:

| Branch | Objective | Issue | Risk |
|---|---|---|---|
| 1D ✅ **DONE (P6a)** | Fail closed on existence reads and make Phase 9 scan `shares_by_group` directly using each projection row's `OrgID`, including stable orphan discovery without the groups N+1. Scanner tests plus a real-Cassandra store regression prove the behavior. Merged first (2026-07-10, branch `fix/gc-existence-check-failopen`). | `ISSUE-GC-EXISTENCE-CHECK-FAILOPEN-01` | **High** |
| 1E ✅ **DONE (P6b)** | Durable guard modes plus execution-time canonical point read under the existing library lock; fail closed on presence/read/fence/unknown mode, preserve queue/retry/DLQ semantics, coordinate restore with the same lock, and cover scanner→worker plus historical-marker regressions. P7 discovery stays open. | `ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01` | Med |
| 1F (P8) | Replace Phase 9's provisional global `shares_by_group` stream with a bucketed active-partition discovery projection. Register on share creation, remove when empty, paginate buckets/partitions, and provide reconcile/backfill for drift. | `ISSUE-GC-GROUP-SHARE-DISCOVERY-SCAN-01` | Med |
| 1A | `pub:foreign` test hygiene: capture the referrer, `t.Cleanup` **all** rows/objects identified by the fixture's exact org/library/session/operation/block IDs (ref + block + mapping + S3 object **+ the provisional expiry projection** from the real upload session), or add a TTL; assert the fake ref is gone. Never broad cleanup. No prod change. | `ISSUE-GC-TEST-RESIDUE-01` | Minimal |
| 1B | Inventory upload fixtures that leave `blocks`/MinIO objects and add fixture-scoped teardown; also repair the `library_projection_regression_test.go` failure-restore so it does not re-insert an unstamped `deleted_libraries`. Measure **no-net-growth** vs the pre-suite baseline. | `ISSUE-GC-TEST-RESIDUE-01` | Low |
| 1C | Remove global-GC cross-test interference: replace direct `ProcessOnce(storage=nil)` with `ProcessOrgOnce`; inventory `/admin/gc/run` callers and isolate or baseline their effects. | `ISSUE-GC-TEST-RESIDUE-01` | Med |
| 2 | Fold `AddDeleteLibraryPolicyQuery` (version_ttl + auto_delete) into the `hardDeleteLibraryRowsFn` batch. Policy index only. | `ISSUE-GC-POLICY-INDEX-STALE-01` | Low |
| 3 ✅ **DONE** | Org-admin single-path parity (`DeleteOrgTrashLibrary`): after hard-delete, immediately enqueue the same durable, Phase-13-deduplicated `ItemLibraryCascade` used by the other permanent-delete paths. | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` | Low/Med |
| 4 ✅ **DONE** | Org-admin bulk-path parity (`CleanOrgTrashLibraries`): after each successful hard-delete, immediately enqueue the same durable, Phase-13-deduplicated `ItemLibraryCascade`. No content-only accelerator remains. | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` | Med |
| 5 | Phase 13 error visibility: accumulate (`errors.Join`), return the error, dedicated metric, don't flag global success on delivery failure; separate enqueued/skipped/failed counters. | `ISSUE-GC-PHASE13-ERROR-VISIBILITY-01` | Low |
| 6A ✅ **DONE** | Durable purge marker: `deleted_libraries.purge_requested_at` (migration 012); Phase 13 eligible when `purge_requested_at != null OR deleted_at < cutoff`. Eligibility only — the grace gate before worker processing is preserved. | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` | Med |
| 6B ✅ **DONE** | Stamp the durable marker at all permanent-delete marker writers (`hardDeleteLibraryRowsFn`, `CleanOrgTrashLibraries`). The existing cascade owns final cleanup; immediate enqueue stays a best-effort accelerator. | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` | Med |
| 7 | Discoverable expiry projection for `pub:` (mirror of `up:`): register a projection when the ref is created, roll back if it fails; scanner deletes the ref on expiry → `BlockHasReferences` → candidate. Owner stays publish. | `ISSUE-GC-PUB-REF-ZERO-REF-01` | Med |
| 8A | Read-only reconcile (`--dry-run` default, per-org scope, paginated, JSON, no S3 delete): report stale policy rows, `fs:` refs with no lib, blocks with no ref and no candidate, candidates with no queue item, markers with no cascade, S3 orphans. | `ISSUE-GC-RECONCILE-BACKFILL-01` | Low |
| 8B | Low-risk repairs: delete stale policy rows; re-enqueue existing candidates; block with no refs → `EnsureBlockGCCandidate` (never delete directly). | `ISSUE-GC-RECONCILE-BACKFILL-01` | Med |
| 8C | Conservative `fs:` orphan repair (fs_object exists → enqueue; missing + lib gone + marker → delete only that ref → zero-ref → candidate). Don't touch `pub:` or S3 directly. | `ISSUE-GC-RECONCILE-BACKFILL-01` | Med |
| 8D | Add durable discovery for markerless commit/fs_object partitions, or retain a durable library cleanup identity until every child is completed/expired. Backfill the snapshot class where both library indexes are gone. | `ISSUE-GC-ORPHAN-ARTIFACT-DISCOVERY-01` | Med/High |
| 9 | Remove the N+1 in bulk cleaners: fold `encrypted` + `block_representation_id` into the trash-listing query; extra resolver only as legacy fallback. | (opt) | Low |
| 10A | Synchronize `dryRun` with `atomic.Bool`; separately decide whether hard cutover requires worker drain/serialization or pre-destructive rechecks. | `ISSUE-GC-ENGINE-ROBUSTNESS-01` | Low |
| 10B | Make remaining scanner phase errors consistently visible; P5 covers Phase 13 and P6 covers fail-open existence checks. | `ISSUE-GC-ENGINE-ROBUSTNESS-01` | Low |
| 10C | Meter/bound repeated `postponeItem` contention without turning normal lease contention into premature DLQ. | `ISSUE-GC-ENGINE-ROBUSTNESS-01` | Low |
| 10D | Audit/reconcile `gc_pending_items` against queue + retained DLQ. Add no standalone TTL unless its lifetime is coordinated with both canonical work stores. | `ISSUE-GC-ENGINE-ROBUSTNESS-01` | Low |
| 10E | Decide E5 explicitly: accept leader-lease exclusion as design (S3 delete/cleanup are largely idempotent) or add a per-orphan claim/LWT if split-brain tolerance is required. | `ISSUE-GC-ENGINE-ROBUSTNESS-01` | Low |

**Crash-matrix gates (after 6B):** exercise a crash at {before the batch, after the marker,
after deleting `libraries`, before/after the enqueue, during the cascade, during fs_object
cleanup, during the S3 delete}. Allowed outcome always: live content intact **or** recoverable
garbage — never a deleted live block, never a library with no recoverable marker.

**Global verification (after branches 1A–4):** the suite does not own the external GC service
but currently triggers global GC work. Absolute zero is not a valid gate on the shared cluster;
first remove/isolate global cross-test processing (1C), then use two gates:

- **Shared cluster:** *no net growth* in fixture-owned Cassandra rows and MinIO objects
  relative to the pre-suite baseline.
- **Ephemeral, empty cluster:** absolute zero (`blocks`, `block_references`,
  `block_id_mappings`, `commits`, `gc_libraries_by_policy` at 0 and an empty bucket) is
  required only after all upload fixtures have exact teardown *or* the harness runs an
  isolated, deterministic GC drain. Do **not** run a global GC or `TRUNCATE` over the shared
  keyspace (invariants #5/#6).

---

## Live-path verification and confirmed `gc_pending_items` leak (2026-07-13)

After the P6b guard-mode work merged (PR #126), we re-ran the full suite on a wiped dev
cluster (`docker compose down -v` → clean deploy) and, separately, exercised the **real**
production delete path (uploaded ~50 GB across three folders through the normal web/desktop
flow, then deleted the libraries and let GC drain). Direct Cassandra + MinIO inspection
separated **test-only residue** from **live-path behavior** for the first time.

### Verdict A — the physical/content delete is clean on the live path

The 50 GB delete left **zero** content residue: `blocks`, `block_references`, `commits`, and
the MinIO block buckets returned to exactly their pre-upload counts (MinIO total ≈ 880 bytes
of pre-existing test residue; the 50 GB of real blocks + S3 objects were fully reclaimed).
The library cascade → `fs:` ref release → zero-ref → candidate → LWT-claimed block + S3 delete
ran end to end. This directly confirms that the **eternal content residue seen in the
suite** — the `pub:foreign` block (F1/1A), the markerless `fs:`/commit/fs_object orphans (P7),
and the S3-only orphans from `ProcessOnce(storage=nil)` (Verdict 2 / 1C) — is **test-only**:
the real delete path reproduced **none** of it.

### Verdict B — CONFIRMED live-path leak: `gc_pending_items` block rows are never removed

`gc_pending_items` grew monotonically with deleted volume and **is not test-only** — the 50 GB
real delete alone drove it from ~575 to **9,633 rows** (9,629 `block`, sampled rows point at
blocks that are **already physically deleted**). With `gc_queue = 0`, `gc_block_candidates = 0`,
and `gc_failed_items = 0`, no live work backs them: they are orphaned dedup rows with no TTL
(`default_time_to_live = 0`, [001_initial_schema.cql:1207](../internal/db/migrations/001_initial_schema.cql#L1207)).
This **upgrades E4 from "drift risk, not a proven leak" to a confirmed, unbounded live-path
leak**, proportional to deleted block volume.

**Root cause — `ItemBlock` is enqueued with an inconsistent `library_id`, and the pending key
is library-scoped.**

- `gc_pending_items` is keyed by `library_id` twice: the partition `bucket` hashes `library_id`
  ([store_cassandra.go:251-259](../internal/gc/store_cassandra.go#L251)) **and** `library_id` is
  a clustering column (`PRIMARY KEY ((org_id, bucket), item_type, library_id, item_id,
  identity_at)`, [001_initial_schema.cql:1206](../internal/db/migrations/001_initial_schema.cql#L1206)).
- `gc_queue` does **not** carry `library_id` in its key (`PRIMARY KEY ((org_id, bucket),
  queued_at, item_type, item_id)`, [001_initial_schema.cql:1124](../internal/db/migrations/001_initial_schema.cql#L1124));
  its bucket is `gcQueueBucket(orgID, itemType, itemID)`, library-independent.
- Blocks are content-addressed and **library-independent** — `processBlock` uses only
  `OrgID` + `ItemID`, never `item.LibraryID` ([worker.go:403-468](../internal/gc/worker.go#L403)).
- There are three block-enqueue sites. Their pending **dedup checks all standardize on
  `uuid.Nil`** ([worker.go:1602](../internal/gc/worker.go#L1602),
  [scanner.go:370](../internal/gc/scanner.go#L370), [gc.go:411](../internal/gc/gc.go#L411)),
  but two of them **write** the pending row under the **real** `libraryID`:
  - `worker.enqueueZeroRefBlocks` enqueues `LibraryID: libraryID`
    ([worker.go:1619](../internal/gc/worker.go#L1619)) — **the live leaker** (fired by every
    cascade/version/auto-delete block release).
  - `Service.EnqueueBlock` passes `libraryID` to `EnqueueItem`
    ([gc.go:422](../internal/gc/gc.go#L422)) — latent only; its sole non-test caller already
    passes `uuid.Nil` ([gc_adapter.go:32](../internal/api/gc_adapter.go#L32)).
  - `scanner.scanOrphanedBlocks` enqueues `LibraryID: uuid.Nil`
    ([scanner.go:386](../internal/gc/scanner.go#L386)) — **the correct reference**.

A **single** producer is self-consistent even with a real `library_id`: `CompleteItem` re-reads
`library_id` from the **same** queue row it is completing and deletes the pending row under that
exact key ([store_cassandra.go:556-580](../internal/gc/store_cassandra.go#L556)). The leak needs
**two** producers enqueuing the same block/candidate under **different** `library_id`s — worker
cascade (`realLib`) plus scanner (`Nil`), or two libraries sharing a block. They enqueue with the
same `candidate_at` as `queued_at`, so they **collapse into one `gc_queue` row** (last writer
wins on the non-key `library_id` column) but each already wrote its **own** `gc_pending_items`
row — `bucket(realLib)` **and** `bucket(Nil)`. `CompleteItem` then deletes only the pending row
matching the **surviving** queue row's `library_id` and **orphans the other forever**. The
`uuid.Nil` dedup check also could not see the worker's own `realLib` write, so it failed to
suppress the double-enqueue.

**Bug, not intentional — git history:** `worker.go` was written with `LibraryID: libraryID` on
2026-04-09 (`e9a9b369b`, "Track block GC candidates"). Its dedup check was migrated to
`uuid.Nil` on 2026-04-30 (`5dee7eee2`) **without** updating the paired enqueue — an incomplete
migration that left the function checking one key and writing another. The scanner phase added
2026-05-26 (`f3597a935`) does both sides with `uuid.Nil`, codifying the intended convention:
**blocks are `Nil`-keyed in `gc_pending_items`.**

**Fix (branch `fix/gc-pending-items-block-library-scope`, this PR):** two layers.
1. **Producers** — every `ItemBlock` enqueue keys `uuid.Nil` (`worker.enqueueZeroRefBlocks`,
   `Service.EnqueueBlock`), matching the dedup checks and the scanner.
2. **Central backstop** — the store-level pending helpers (`addPendingItemBatchQuery`,
   `addPendingItemDeleteBatchQuery`, `PendingItemExists`) coerce `ItemBlock` to `uuid.Nil` via
   `pendingItemLibraryID`, so **every** pending write, delete, and dedup read for a block lands on
   the single `uuid.Nil` key regardless of what any current or future producer passes. This is the
   durable guard against the same class of partial-migration bug recurring.

Both paths write one identical pending row and `CompleteItem` removes it. No data-safety impact
(blocks never used `library_id`); the change is content-only. Pre-existing orphaned rows are not
self-healed by the fix — they need the reconcile sweep (branch 8) or a coordinated one-off
cleanup, tracked in `ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01`.

