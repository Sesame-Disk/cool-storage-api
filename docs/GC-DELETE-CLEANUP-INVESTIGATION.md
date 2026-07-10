# GC Library-Delete Cleanup — Investigation & Follow-up Debt

**Date:** 2026-07-09 (original) · **Audit verified:** 2026-07-10
**Branch where found:** `fix/gc-block-representation-durability` (PR #123)
**Status:** partially fixed on PR #123 (representation stamping + Phase 13 recovery);
the remaining items below are **out of scope for #123** and are being closed on a
series of small, auditable follow-up branches. This document is the **canonical audit
record**; the verified verdict, the confirmed production-gap table (P1–P5), the
architectural invariants, and the branch roadmap live in the
[**"Cross-agent audit — verified verdict"**](#cross-agent-audit--verified-verdict-2026-07-10)
section at the end. The `F1`–`F4` findings below are the original notes, now annotated
with their verified status.

> **TL;DR of the verified audit:** the block-delete **engine is safe** (crash-safe
> claim→recovery→delete ordering, fail-closed representation validation). The "garbage
> after tests" is **mostly test hygiene** (the GC daemon does not run in the integration
> suite; one test injects a permanent fake `pub:` ref with no cleanup). There are still
> a handful of **real production gaps** (org-admin trash deletes defer cleanup up to the
> retention window; non-durable goroutine handoff; stale policy index; `pub:` refs lack a
> discoverable zero-ref transition; Phase 13 swallows enqueue errors) — tracked as
> `ISSUE-GC-*` in `docs/KNOWN_ISSUES.md`.

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
> TTL. The two `fs:` rows are either **recoverable-but-delayed** (Phase 13 can still run
> because `GetLibraryDeletedAt` reads the `deleted_libraries` marker, not the deleted
> `libraries` row — [store_cassandra.go:1165](../internal/gc/store_cassandra.go#L1165)) or
> **pre-#123 residue**. So the "leak forever" claim only holds when the marker is missing/
> irrecoverable or the enqueue never lands durably. See P1/P2 and `ISSUE-GC-TEST-RESIDUE-01`.

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

> **VERIFIED STATUS (2026-07-10): confirmed, benign.** `hardDeleteLibraryRowsFn`
> ([library_delete_helpers.go:29-41](../internal/api/v2/library_delete_helpers.go#L29))
> does not call `AddDeleteLibraryPolicyQuery`; only the cascade's `HardDeleteLibrary`
> ([store_cassandra.go:3809](../internal/gc/store_cassandra.go#L3809)) and
> `rollbackNewLibrary` do. Scanner re-validates the `libraries` row and skips on
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

> **VERIFIED STATUS (2026-07-10): corrected.** The two files named below are, on
> re-inspection, **well-sanitized** — both stamp/register a `t.Cleanup` (or snapshot+restore
> guarded by `t.Failed()`) for every seeded row. The **real** residue lives elsewhere:
> (a) `TestWebBlockUploadForeignPubRefNotPermanent` injecting a permanent fake `pub:` ref
> with no cleanup ([web_block_upload_test.go:949](../internal/integration/web_block_upload_test.go#L949));
> (b) the many E2E tests that upload **real** blocks to MinIO with **no S3 teardown** (only
> `gc_s3_deletion_test.go` ever deletes an object), because the GC daemon never runs in the
> suite to reclaim them. Root cause is a **shared keyspace + shared bucket with no global
> truncate/teardown** — cleanup is only by ephemeral library name. Tracked as
> `ISSUE-GC-TEST-RESIDUE-01` (branch 1 + follow-ups).

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

## Proposed work for the follow-up branch

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
       → releases each ref exactly once (fs: and, once owned, pub:foreign:)
       → zero-ref blocks become candidates
       → physical block + S3 deletion
       → final cleanup
   ```
   Prefer a durable outbox/queue over fire-and-forget goroutines for the enqueue.
2. **Determine the single owner of `pub:foreign:` ref cleanup FIRST** (CAS/publish
   path) before wiring any release. Until that owner is defined, do not have two
   subsystems remove the same ref — decide whether the cascade or the publish
   subsystem releases `pub:foreign:` refs, and release it in exactly one place.
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

Four agents investigated the GC delete path (three parallel explorations + one that
cross-checked the results). Every claim below was **verified first-hand in the code** at
the cited `file:line`. The originating investigation asked two questions — *is the GC safe?*
and *is it optimal?* — and the answer is: **safe yes, optimal not yet.**

### Verdict 1 — the block-delete engine is SAFE

The protocol that deletes a physical block is conservative and crash-safe
([worker.go:386-557](../internal/gc/worker.go#L386), `store_cassandra.go`):

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

> **Net risk: low chance of deleting a live block; the real risk is *not* deleting garbage
> (leaks / long delays).**

### Verdict 2 — the residue after tests is MOSTLY test hygiene

- **The GC daemon does not run in the integration suite.** `TestMain` never starts the
  `Service`; only a few GC tests build a `Worker` by hand
  ([gc_integration_test.go:974](../internal/integration/gc_integration_test.go#L974)). Every
  other test uploads real blocks to MinIO and deletes libraries via the API (which only enqueues
  async cleanup that **nothing drains**), so blocks + S3 objects + refs pile up in the **shared**
  keyspace and bucket.
- **`TestWebBlockUploadForeignPubRefNotPermanent`** inserts a `pub:foreign-<ts>` ref with **no
  TTL and no `t.Cleanup`** and deletes the session's `up:` ref
  ([web_block_upload_test.go:945-950](../internal/integration/web_block_upload_test.go#L945)) —
  these are exactly the two "permanent" `pub:foreign-*` rows in the residue table. They do **not**
  come from the production writer (which stamps a 35-day TTL,
  [block_references.go:38](../internal/db/block_references.go#L38)).
- No global `TRUNCATE`/teardown of keyspace or MinIO; cleanup is only by ephemeral library name.
  Blocks uploaded under synthetic `org_id`/`library_id` with no `t.Cleanup` persist forever.

### Verdict 3 — confirmed production gaps (P1–P5)

| # | Verified finding | Sev | Evidence | Issue |
|---|---|---|---|---|
| P1 | Org-admin trash deletes hard-delete the library **without** enqueuing a cascade, deleting the storage counter, or cleaning tags. Individual `deleteResolvedTrashLibrary` and bulk `CleanOrgTrashLibraries` rely on Phase 13, which only fires after `TrashRetentionDays` (~30d). | High | [library_delete_helpers.go:98-116](../internal/api/v2/library_delete_helpers.go#L98), [org_admin_repos.go:390-448](../internal/api/v2/org_admin_repos.go#L390) | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` |
| P1b | The marker is re-inserted with `deleted_at = time.Now()`, **resetting the retention clock** → Phase 13 picks it up 30d after the permanent-delete action, not after the original trashing. | High | [org_admin_repos.go:432](../internal/api/v2/org_admin_repos.go#L432), [library_delete_helpers.go:36](../internal/api/v2/library_delete_helpers.go#L36) | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` |
| P2 | Non-durable handoff on the superadmin paths: `PermanentDeleteRepo` and the global clean-trash enqueue via `go fn()` fire-and-forget after responding 200. A process exit / suite end in that window loses the work (recoverable only via marker + Phase 13). | High/Med | [library_delete_helpers.go:44,82-86,132-136](../internal/api/v2/library_delete_helpers.go#L44) | `ISSUE-GC-DELETE-HANDOFF-DURABILITY-01` |
| P3 | `gc_libraries_by_policy` is not cleared on direct delete; only the cascade's `HardDeleteLibrary` and `rollbackNewLibrary` call `AddDeleteLibraryPolicyQuery`. Benign (scanner re-validates and skips) but accumulates. | Med | [library_delete_helpers.go:29-41](../internal/api/v2/library_delete_helpers.go#L29) vs [store_cassandra.go:3809](../internal/gc/store_cassandra.go#L3809) | `ISSUE-GC-POLICY-INDEX-STALE-01` |
| P4 | `pub:` refs have no discoverable expiry projection (`up:` has one via `gc_provisional_block_refs` + Phase 0). When the last `pub:` expires by Cassandra TTL (35d), nothing runs the zero-ref → `EnsureBlockGCCandidate` transition; `scanOrphanedBlocks` only walks already-created candidates. Block + mapping + S3 can be retained. | Med | [block_references.go:339](../internal/db/block_references.go#L339); [scanner.go:335](../internal/gc/scanner.go#L335) | `ISSUE-GC-PUB-REF-ZERO-REF-01` |
| P5 | Phase 13 hides delivery failures: on `EnqueueBatch` failure it logs and returns `nil`; on per-library `PendingItemExists` failure it `continue`s silently. Health/metrics can report success without delivering the work. | Med | [scanner.go:1194-1197,1231-1242](../internal/gc/scanner.go#L1194) | `ISSUE-GC-PHASE13-ERROR-VISIBILITY-01` |

### Precisions (they confirm, they refine)

1. The "up to 30 days" wait applies **only** to the two org-admin paths (P1). The superadmin
   paths **do** enqueue immediately — their weakness is the non-durable goroutine (P2), not the
   retention wait.
2. The clock reset (P1b) makes P1 worse: even the Phase 13 rescue is delayed 30d **from the action**.
3. The cascade can run even after the `libraries` row is gone, because `GetLibraryDeletedAt` reads
   the `deleted_libraries` marker ([store_cassandra.go:1165](../internal/gc/store_cassandra.go#L1165)),
   not the live row. That is why the `fs:` residue is "recoverable-but-delayed" or pre-#123 —
   **not** an eternal leak in the general marker-present case.

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
6. Tests never run a global GC over the shared keyspace; they only clean fixtures they created.
7. PR #123 representation recovery is kept always.

### Branch roadmap (small, auditable PRs)

This document (branch `docs/gc-delete-cleanup-audit`) is **branch 0** — documentation only.
Subsequent branches, in recommended merge order:

| Branch | Objective | Issue | Risk |
|---|---|---|---|
| 1 | `pub:foreign` test hygiene: capture the referrer, `t.Cleanup` the ref+block+mapping+object (or add a TTL), assert the ref is gone. No prod change. | `ISSUE-GC-TEST-RESIDUE-01` | Minimal |
| 2 | Fold `AddDeleteLibraryPolicyQuery` (version_ttl + auto_delete) into the `hardDeleteLibraryRowsFn` batch. Policy index only. | `ISSUE-GC-POLICY-INDEX-STALE-01` | Low |
| 3 | Org-admin single parity (`DeleteOrgTrashLibrary`): after hard-delete, enqueue cascade + delete counter + clean tags (match `PermanentDeleteRepo`). | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` | Low/Med |
| 4 | Org-admin bulk parity + centralization (`CleanOrgTrashLibraries`): use the per-candidate helper instead of the hand-rolled hard-delete; inherit cascade/counter/tags/policy. No N+1 yet. | `ISSUE-GC-ORG-TRASH-NO-CASCADE-01` | Med |
| 5 | Phase 13 error visibility: accumulate (`errors.Join`), return the error, dedicated metric, don't flag global success on delivery failure; separate enqueued/skipped/failed counters. | `ISSUE-GC-PHASE13-ERROR-VISIBILITY-01` | Low |
| 6A | Durable purge marker: `deleted_libraries.purge_requested_at`; Phase 13 eligible when `purge_requested_at != null OR deleted_at < cutoff`. Incremental `NNN_*.cql` migration. | `ISSUE-GC-DELETE-HANDOFF-DURABILITY-01` | Med |
| 6B | Stamp the durable marker at all permanent-delete writers; keep immediate enqueue only as a best-effort accelerator. Never return 500 after a confirmed hard-delete. | `ISSUE-GC-DELETE-HANDOFF-DURABILITY-01` | Med |
| 7 | Discoverable expiry projection for `pub:` (mirror of `up:`): register a projection when the ref is created, roll back if it fails; scanner deletes the ref on expiry → `BlockHasReferences` → candidate. Owner stays publish. | `ISSUE-GC-PUB-REF-ZERO-REF-01` | Med |
| 8A | Read-only reconcile (`--dry-run` default, per-org scope, paginated, JSON, no S3 delete): report stale policy rows, `fs:` refs with no lib, blocks with no ref and no candidate, candidates with no queue item, markers with no cascade, S3 orphans. | `ISSUE-GC-RECONCILE-BACKFILL-01` | Low |
| 8B | Low-risk repairs: delete stale policy rows; re-enqueue existing candidates; block with no refs → `EnsureBlockGCCandidate` (never delete directly). | `ISSUE-GC-RECONCILE-BACKFILL-01` | Med |
| 8C | Conservative `fs:` orphan repair (fs_object exists → enqueue; missing + lib gone + marker → delete only that ref → zero-ref → candidate). Don't touch `pub:` or S3 directly. | `ISSUE-GC-RECONCILE-BACKFILL-01` | Med |
| 9 | Remove the N+1 in bulk cleaners: fold `encrypted` + `block_representation_id` into the trash-listing query; extra resolver only as legacy fallback. | (opt) | Low |

**Crash-matrix gates (after 6B):** exercise a crash at {before the batch, after the marker,
after deleting `libraries`, before/after the enqueue, during the cascade, during fs_object
cleanup, during the S3 delete}. Allowed outcome always: live content intact **or** recoverable
garbage — never a deleted live block, never a library with no recoverable marker.

**Global verification (after branches 1–4):** re-run the full suite on a clean cluster and
re-inspect Cassandra + MinIO with the access commands above. Target: `blocks`,
`block_references`, `block_id_mappings`, `commits`, `gc_libraries_by_policy` at 0 and an empty
bucket after a clean run (once the tests that manufacture their own residue are sanitized).
