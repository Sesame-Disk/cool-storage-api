# SHA-256 canonical block IDs (and removing SHA-1 from the web client)

**Date:** 2026-06-29
**Status:** Design + PR breakdown. PR1 and PR2 are implemented in the workspace and pending
review/commit; PR3+ remain pending.
**Supersedes:** the out-of-tree `implementation_plan.md` draft (backend/read-side only),
which is removed in favour of this document.
**Related:** [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md) (R10 dual-hash, the current state
this evolves), [CHUNKING-ANALYSIS.md](./CHUNKING-ANALYSIS.md).

## Progress

- `PR1` — implemented in workspace: additive migration `005_sha256_canonical_block_ids.cql`,
  `internal/models` fields for `seafile_block_ids_sha1` / `blocks.sha1`, and regression coverage
  for the new migration + JSON shape. No behavior has been flipped yet.
- `PR2` — implemented in workspace: `blocks.sha1` is now written from verified bytes on the web
  block-upload materialization path and the legacy seafhttp upload/finalize path, and
  `ProbeBlockReuse` now reads/exposes `sha1` for later commit-time use.
- `PR3` — in progress (branch `feat/sha256-canonical-block-ids-pr3`):
  - The read hot path was **already tolerant**: `BatchResolveBlockIDs` / `resolveBlockIDs`
    already pass 64-hex (SHA-256) IDs through untouched and only resolve 40-hex
    ([streaming.go:48,88](../internal/streaming/streaming.go#L48)), so download / zip / file view
    / share-link drop to **O(0) mapping reads** automatically once PR4 stores SHA-256 in
    `block_ids` (other per-request reads — fs_object, storage, permissions — still happen). No
    change needed there.
  - Implemented the **Seafile serve** path: `GetFSObject` and `PackFS` now serialize
    `seafile_block_ids_sha1` (falling back to `block_ids` when empty) via the shared
    `seafileServeBlockIDs` helper. No-op until PR4 (the column is empty, so it falls back to the
    still-SHA-1 `block_ids`), correct afterwards. Unit test `TestSeafileServeBlockIDs`.
  - **Open question RESOLVED (hash-space trace done).** `block_references` is SHA-256-keyed, and
    every sync path that touches it resolves SHA-1 → SHA-256 *first*:
    `stageSyncCommitBlockDelta` / `finalizeSyncCommitBlockDelta` go through `resolveSyncBlockIDs`,
    and `RegisterFSObjectBlockReferences` ([fs_helpers.go:1088](../internal/api/v2/fs_helpers.go#L1088))
    resolves before writing refs (asserted by `TestRegisterFSObjectBlockReferences_AddsReferencesForPersistedFSObject`).
    → **Reference-accounting readers MUST keep reading `block_ids`** (not `seafile_block_ids_sha1`):
    after PR4 `block_ids` is SHA-256, the resolve becomes a passthrough, and refs stay SHA-256.
    No PR4 change needed for them; switching them to SHA-1 would be a regression.
  - Implemented the **fs_id-recompute** boundary: `computeCorrectedObject` (used by
    `buildFSIDMapping` / `CheckFS`) now builds the file JSON from `seafile_block_ids_sha1`
    (fallback `block_ids`) via the same `seafileServeBlockIDs` helper, so the computed→stored
    fs_id mapping keeps matching the client after PR4. No-op until PR4.
  - **PR3 is functionally complete.** The only remaining PR3-adjacent item is a post-flip
    integration test, tracked as a PR4 blocker (it needs PR4 data: SHA-256 `block_ids` + SHA-1
    `seafile_block_ids_sha1`).
- `PR4`–`PR7` — pending.

## Notes / Debt

- The current additive/no-backfill approach still assumes the current pre-deploy / empty-DB
  rollout. If this branch were ever applied to a non-empty environment, older `blocks` rows would
  retain empty `sha1` until a dedicated backfill or rewrite path is added.
- **TODO (before PR4/PR5):** the template-block path in `CreateFile`
  ([files.go:1355](../internal/api/v2/files.go#L1355)) currently registers its block with an
  empty `sha1` (the SHA-1 is not threaded there yet). That is harmless while nothing reads
  `blocks.sha1`, but once PR4/PR5 derive `seafile_block_ids_sha1` / the `fs_id` from
  `blocks.sha1`, this path must supply the block's SHA-1 (or compute it locally) or
  template-created files would get an empty Seafile block id and break desktop `fs_id` matching.

### Gating rule for the next branch (PR4/PR5)

Do **not** start PR4/PR5 until both are closed:

1. **Close the template-block TODO above** — `CreateFile` must persist a real `blocks.sha1`.
2. **Add fail-closed validation of `blocks.sha1` at the point of consumption.** `ProbeBlockReuse`
   exposes `Sha1` raw (only `TrimSpace`d); the commit that uses it as the source for
   `seafile_block_ids_sha1` / `fs_id` (PR5) MUST reject empty or non-40-hex values by treating
   the block as `needs_upload` (the re-upload recomputes and rewrites a verified `sha1`) — never
   write an unvalidated SHA-1 into an fs_object. Reuse `isHex40`
   ([file_from_blocks.go:134](../internal/api/v2/file_from_blocks.go#L134)); mirror the existing
   R1/R10 "forward mapping missing → needs_upload" pattern. Fail closed, never silently.

Also add, when the value first drives `fs_id` (PR5): a test with a **real 40-hex SHA-1** plus the
integration round-trip asserting the desktop-expected `fs_id` (the current plumbing tests use
`"sha1-1"`/`"ext-1"`, which only prove the argument travels).

### Blocker checklist (single source of truth)

Carried forward from review. Status as of the PR3 partial branch:

| # | Item | Severity | Gates |
|---|---|---|---|
| 1 | Desktop breaks if any Seafile endpoint serializes SHA-256 into `"block_ids"`. Serve path (`GetFSObject`/`PackFS`) is fixed; the remaining serializers must be audited. | Critical | PR4 |
| 2 | ~~`CheckFS` / `buildFSIDMapping` must use `seafile_block_ids_sha1` (fallback `block_ids`), never hash SHA-256 into the file JSON.~~ **DONE (PR3)** in `computeCorrectedObject`. | Blocker | resolved |
| 3 | `CreateFile` template block must register a real SHA-1, not empty. | Blocker | PR4 |
| 4 | Validate `blocks.sha1` (40-hex, non-empty) before using it for `seafile_block_ids_sha1` / `fs_id`; else `needs_upload`. Reuse `isHex40`, fail closed. | Blocker | PR4/PR5 |
| 5 | After the writer flip, **no row may have SHA-256 `block_ids` with empty `seafile_block_ids_sha1`**. Add a guard (strong log / fail-closed / repair) in `seafileServeBlockIDs` and at write time. | Blocker | PR4 |
| 6 | Add an integration test for a post-flip file object (`block_ids`=64-hex, `seafile_block_ids_sha1`=40-hex): client receives the 40-hex list and the served JSON re-hashes to the requested `fs_id`. | Med | PR4 |
| 7 | ~~Confirm sync reference-accounting feeds `block_references` in SHA-256.~~ **DONE** — it resolves before writing refs; keep reading `block_ids`. | — | resolved |
| 8 | Do not drop the reverse mapping (`block_id_mappings_by_internal`) until the alias / encrypted / GC enumeration check passes. | Med | PR7 |

---

## a. Context and goal

Today `fs_objects.block_ids` stores **SHA-1** for *every* file (web and desktop). Every
download, preview, zip and GC sweep reads that SHA-1 list and resolves it to the internal
SHA-256 storage identity through `streaming.BatchResolveBlockIDs`
([streaming.go:59](../internal/streaming/streaming.go#L59)) — **N point-reads against
`block_id_mappings` per read operation**. A 1,500-block file pays ~1,500 Cassandra lookups
on *every* download.

Separately, the web block-upload flow makes the browser compute **two** hashes per block
(SHA-256 **and** SHA-1, [block-hash.js:23](../frontend/src/components/file-uploader/block-hash.js#L23)),
even though the client's SHA-1 is **redundant**: the server already recomputes it from the
real bytes in `UploadBlock` ([blocks.go:367](../internal/api/v2/blocks.go#L367)) and only uses
the client value as a commit-time cross-check.

**North star: SHA-256 is the canonical internal identity; SHA-1 exists only at the legacy
Seafile boundary.** Two halves of one change:

1. **Read/GC (storage layout).** `fs_objects.block_ids` becomes SHA-256; a new column
   `seafile_block_ids_sha1` holds the SHA-1 the desktop client needs. Download / zip / preview
   / GC drop to **O(0) mapping reads**. The SHA-1 → SHA-256 resolution moves from the hot read
   path to the cold write path (commit / `RecvFS`).
2. **Upload/frontend.** The browser stops computing and sending SHA-1 (~40–50% less hashing
   CPU; hashing is the first half of the progress bar and is 100% upfront before the first
   byte is sent). The server derives SHA-1 from the bytes it already hashes.

### Critical invariant — DO NOT BREAK

**A Seafile `fs_id` is the SHA-1 of the fs_object JSON, which embeds the block-id list.** So
the fs_object serialized to the desktop client **and its `fs_id`** MUST keep being derived
from the **SHA-1** list (`seafile_block_ids_sha1`), exactly as today. The `block_ids` column
(SHA-256) is purely an internal storage convenience for fast reads; it does **not** change the
`fs_id` or the Seafile serialization. This preserves desktop/mobile compatibility (see
[WEB-BLOCK-UPLOAD.md R10](./WEB-BLOCK-UPLOAD.md) / the merge #91 regression) while making
internal reads O(0).

That rule applies not only to direct sync serving (`GetFSObject`, `PackFS`) but to **any code
path that rebuilds or re-hashes a Seafile file object**. In particular, `CheckFS` /
`buildFSIDMapping` / corrected-fs-id computation must build file JSON from
`seafile_block_ids_sha1`, never from internal SHA-256 `block_ids`, or the computed fs_ids will
stop matching what the desktop client expects.

---

## b. Current-state audit

### Tables (today)
| Table / column | Today |
|---|---|
| `fs_objects.block_ids` | **SHA-1** (40-hex), for both web and desktop files |
| `blocks.block_id` / `block_references.block_id` / S3 key | SHA-256 (64-hex) |
| `block_id_mappings` (forward) | SHA-1 → SHA-256, **authoritative** ([block_references.go:200](../internal/db/block_references.go#L200)) |
| `block_id_mappings_by_internal` (reverse) | SHA-256 → SHA-1, **best-effort** GC/repair projection, allows multiple aliases |

### Read hot path (9 call sites of `BatchResolveBlockIDs`)
- download — [seafhttp.go:3575](../internal/api/seafhttp.go#L3575)
- zip — [seafhttp.go:3976](../internal/api/seafhttp.go#L3976)
- file view ×4 — [fileview.go:586,649,974,1145](../internal/api/v2/fileview.go)
- share-link view ×2 — [sharelink_view.go:907,1010](../internal/api/v2/sharelink_view.go)
- helper — [fs_helpers.go:959](../internal/api/v2/fs_helpers.go#L959)

`GetFSObject` ([sync.go:1365](../internal/api/sync.go#L1365)) and `PackFS`
([sync.go:1446](../internal/api/sync.go#L1446)) serve the fs_object JSON to the desktop client
using today's SHA-1 `block_ids`.

### Upload paths (today)
- **Web block flow:** worker dual-hashes → manifest `{sha1, sha256, size}` → commit
  (`CreateFileFromBlocks`) validates the client SHA-1 against the forward mapping and writes
  SHA-1 into `fs_objects.block_ids`.
- **Legacy seafhttp / desktop:** the server computes both SHA-1 (block + cumulative file id)
  and SHA-256 from the real bytes and writes the `block_id_mappings` rows.

---

## c. Target end-state — where each hash lives

| Field | Target value |
|---|---|
| `fs_objects.block_ids` | **SHA-256** (canonical internal) |
| `fs_objects.seafile_block_ids_sha1` (**new**) | **SHA-1** — Seafile serialization + `fs_id` |
| `blocks.block_id` / `block_references` / S3 key | SHA-256 (unchanged) |
| `blocks.sha1` (**new**) | **SHA-1**, written from real bytes by `UploadBlock` / seafhttp |
| `block_id_mappings` (forward) | **kept** — still resolves the desktop **block download** (GET block by SHA-1 over seafhttp) |
| `block_id_mappings_by_internal` (reverse) | unchanged (GC/repair only) |
| frontend manifest | `{sha256, size}` only |

The forward mapping stays because the desktop client downloads blocks *by SHA-1*; that GET
still needs SHA-1 → SHA-256 to locate the S3 object. The optimization removes mapping reads
from **web/internal** downloads and GC, not from the desktop block-fetch boundary (which is
inherently SHA-1-keyed).

### Where SHA-1 legitimately survives (the Seafile boundary only)

After this change SHA-1 exists in exactly four places, all touched by the desktop/mobile client:

| Where | Why SHA-1 |
|---|---|
| `fs_objects.fs_id`, `commits.commit_id` / `root_fs_id` / `parent_id` | Seafile **object content-addressing**: the client computes/expects the id as SHA-1 of the object JSON ([seafhttp.go:2847,2864](../internal/api/seafhttp.go#L2847)). Changing it breaks sync. A protocol constraint, not an internal choice. |
| `fs_objects.seafile_block_ids_sha1` | the block list serialized to the desktop client |
| `block_id_mappings` (forward) | resolves the desktop bare-SHA-1 block GET → SHA-256 (a global index) |
| `blocks.sha1` | server-side source to produce the three above without a client SHA-1 |

Everything else — `blocks`, `block_references`, `gc_block_candidates*`,
`gc_provisional_block_refs*`, `gc_s3_orphans*`, `fs_objects.block_ids`, and the publish/repair
block lists (`published_block_reference_repairs.staged_block_ids`,
`pending_published_fs_objects.block_ids`) — is SHA-256.

### Related-table inventory and cleanup

| Table | Key / content | Today | Target |
|---|---|---|---|
| `blocks` | `(org,block_id)` + metadata | SHA-256 | SHA-256 **+ new `sha1` col** |
| `block_references` | `(org,block_id),referrer` | SHA-256 | unchanged |
| `gc_block_candidates` / `_by_day` | `block_id` | SHA-256 | unchanged |
| `gc_provisional_block_refs` / `_by_day` | `block_id` | SHA-256 | unchanged |
| `gc_s3_orphans` / `_by_day` | `block_id` | SHA-256 | unchanged |
| `block_id_mappings` (forward) | SHA-1 → SHA-256 | SHA-1 key | **kept** (desktop boundary only) |
| `block_id_mappings_by_internal` (reverse) | SHA-256 → SHA-1 | mixed | **DROP** (PR7) — redundant with `blocks.sha1` |
| `fs_objects.block_ids` | list | SHA-1 | **SHA-256** |
| `onlyoffice_pending_blocks` | `internal_block_id` + `external_block_id` | both | external derivable from `blocks.sha1`; TTL 7d, leave as is |

---

## d. PR breakdown

Each PR must leave `go test` / Jest + lint + build green, keep the web block-upload flag
**OFF**, and introduce no regression. Additive-first, then a single isolated behavior flip.

### PR1 — Schema + models (additive, no behavior change)
- New migration `internal/db/migrations/005_sha256_canonical_block_ids.cql`:
  - `ALTER TABLE fs_objects ADD seafile_block_ids_sha1 LIST<TEXT>;`
  - `ALTER TABLE blocks ADD sha1 TEXT;`
- `FSObject` gains `SeafileBlockIDsSHA1 []string` ([models.go](../internal/models/models.go));
  block metadata gains `Sha1`.
- Nobody reads/writes the new columns yet.
- **Migration policy:** create an incremental `005_*.cql`; **do NOT edit
  `001_initial_schema.cql`** (the removed draft proposed editing 001 — corrected here).
  Server is pre-deploy/empty → no backfill needed.

### PR2 — Populate `blocks.sha1` on write (the server already computes SHA-1)
- `UploadBlock` → `RegisterWebUploadedBlockAndMapping` / `UpsertBlockMetadata`
  ([block_references.go:407](../internal/db/block_references.go#L407)) and the seafhttp finalize
  write `sha1`.
- `ProbeBlockReuse` ([block_references.go:451](../internal/db/block_references.go#L451)) now
  `SELECT`s `sha1` and exposes it on `BlockReuseProbe`.
- Still unconsumed by fs_object writers.

### PR3 — Tolerant reads (read-side prep, centralized)
- `BatchResolveBlockIDs` already pass-throughs 64-hex SHA-256 IDs today; keep that behavior as
  the compatibility base for mixed old/new rows. No new behavior is needed there beyond
  preserving the "resolve only 40-hex" contract.
- **DONE** — Seafile serve (`GetFSObject`, `PackFS`): serialize `seafile_block_ids_sha1` as the
  JSON `block_ids`, falling back to `block_ids` when the column is empty, via the shared
  `seafileServeBlockIDs` helper ([sync.go](../internal/api/sync.go)). Unit test
  `TestSeafileServeBlockIDs`.
- **DONE** — `CheckFS` / `buildFSIDMapping` / corrected-fs-id computation: `computeCorrectedObject`
  ([sync.go](../internal/api/sync.go)) builds the file JSON from `seafile_block_ids_sha1` (fallback
  `block_ids`) via `seafileServeBlockIDs`, so the recomputed `fs_id` matches the client. Never
  hashes the internal SHA-256 list.
- **RESOLVED — reference-accounting is SHA-256; keep reading `block_ids`.** The sync readers
  `collectSyncReachableFiles` / `loadSyncFileBlockIDs` → `buildSyncCommitBlockDelta` →
  `block_references` ([sync.go:2561,2620](../internal/api/sync.go#L2561)) feed refs only after a
  SHA-1 → SHA-256 resolution: `stageSyncCommitBlockDelta` / `finalizeSyncCommitBlockDelta` via
  `resolveSyncBlockIDs`, and `RegisterFSObjectBlockReferences`
  ([fs_helpers.go:1088](../internal/api/v2/fs_helpers.go#L1088)) resolves before writing. So
  `block_references` is SHA-256-keyed. These readers must **keep reading `block_ids`** — after PR4
  it is SHA-256 and the resolve becomes a passthrough; switching them to `seafile_block_ids_sha1`
  would be a regression. **No PR4 change needed here.**
- GC reads the list directly when it is SHA-256 (the resolver passthrough already covers the
  transition).
- With writers still emitting SHA-1, everything falls back to the old behavior → no observable
  change.

### PR4 — Flip writers to the canonical layout (the isolated behavior switch)
All fs_object writers emit `block_ids` = SHA-256 + `seafile_block_ids_sha1` = SHA-1, keeping
`fs_id` derived from the SHA-1 list (the invariant):
- `newPendingPublishedFile` / `stagePendingPublishedFiles` / `createFileFSObjectRow` /
  `copyFSObjectToLibraryForPublish`
  ([fs_helpers.go:1237,1314,1386](../internal/api/v2/fs_helpers.go)) — `copy` falls back to
  `block_ids` when `seafile_block_ids_sha1` is empty.
- `finalizeStoredUploadMetadata[Once]` ([files.go](../internal/api/v2/files.go)).
- `CreateFileFromBlocks` ([file_from_blocks.go](../internal/api/v2/file_from_blocks.go)).
- `createPendingSeafHTTPFileFSObject` ([seafhttp.go](../internal/api/seafhttp.go)).
- `RecvFS` ([sync.go:1555](../internal/api/sync.go#L1555)) — resolves incoming SHA-1 → SHA-256
  via `BatchResolveBlockIDs` on the cold path; stores SHA-256 in `block_ids`, SHA-1 in the new
  column.

New files then carry SHA-256 in `block_ids`, so the PR3 readers serve them O(0).
**This touches live desktop/seafhttp traffic → requires desktop E2E before merge.**

### PR5 — Frontend drops SHA-1; server derives it from `blocks.sha1`
- Worker / `block-hash.js`: SHA-256 only; manifest `{sha256, size}` only; drop
  `X-Block-Hash-SHA1` (not even sent today).
- The commit reads each block's SHA-1 from `blocks.sha1` via the `ProbeBlockReuse` it already
  runs per block for the R11 size check (**0 extra queries**), to populate
  `seafile_block_ids_sha1` and the `fs_id`.
- `manifestDigest` becomes `{sha256, size}` (the true content identity).
- Depends on PR2 + PR4.

### PR6 — Cleanup + regression tests + docs close-out
- Remove the dead manifest-SHA-1 validation and the `X-Block-Hash-SHA1` reader (this also
  removes the "crafted client SHA-1 collision" surface that motivated the
  `WriteVerifiedWebBlockMapping` guard ([block_references.go:266](../internal/db/block_references.go#L266))).
- Update `TestWebBlockUploadFSObjectUsesSHA1ForDesktopCompat` and neighbours to assert
  `block_ids` = SHA-256 + `seafile_block_ids_sha1` = SHA-1.
- Final pass on this doc + [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md).

### PR7 — Drop the reverse mapping table `block_id_mappings_by_internal`
Once `blocks.sha1` exists (PR2), the reverse table is redundant: its only consumer is GC, which
uses it to find a block's SHA-1 alias(es) when deleting the block by SHA-256.
- GC cleanup reads `blocks.sha1` from the block row it is already deleting and removes the single
  forward `block_id_mappings` row directly — no alias enumeration.
- Stop writing the reverse row in `WriteBlockIDMapping` ([block_references.go:200](../internal/db/block_references.go#L200))
  and `WriteVerifiedWebBlockMapping` ([block_references.go:266](../internal/db/block_references.go#L266))
  → removes a dual-write on the upload hot path.
- Drop `ListBlockMappingsByInternalID` ([store_cassandra.go:1712](../internal/gc/store_cassandra.go#L1712))
  and the reverse delete in `DeleteBlockMappingResolved` ([store_cassandra.go:1747](../internal/gc/store_cassandra.go#L1747));
  rewrite GC mapping cleanup to resolve the SHA-1 from `blocks.sha1`.
- Migration `006_drop_block_id_mappings_by_internal.cql`: `DROP TABLE block_id_mappings_by_internal;`
- ⚠️ **Verify the encrypted-library case first.** For encrypted repos SHA-1 is over plaintext and
  SHA-256 over ciphertext, so one SHA-1 can map to several SHA-256. The SHA-256 → SHA-1 direction
  used here stays 1:1 (each block row has its own SHA-1), but confirm no encrypted-GC path relies
  on enumerating *all* SHA-1 aliases for an internal id before dropping the table. If such a path
  exists, keep the table or replace it with a narrower index.

---

## e. Decisions and invariants

- **SHA-256 → SHA-1 source at commit: the `blocks.sha1` column** — not the reverse table
  (`block_id_mappings_by_internal`), which is best-effort and multi-alias. The column is
  authoritative, single-valued, and free via the `ProbeBlockReuse` the commit already runs.
- **`fs_id` stays SHA-1-derived** — the desktop-compat invariant.
- **Any code that recomputes or serializes Seafile file objects (`GetFSObject`, `PackFS`,
  `CheckFS`, `buildFSIDMapping`, corrected-fs-id helpers) must source the file block list from
  `seafile_block_ids_sha1`, falling back to `block_ids` only for pre-flip rows.**
- **`block_id_mappings` (forward) is kept** — the desktop block download resolves by SHA-1;
  it is now read only at the Seafile boundary, not on web/internal reads.
- **`block_id_mappings_by_internal` (reverse) is dropped (PR7)** — `blocks.sha1` is the
  authoritative SHA-256 → SHA-1 source; the reverse table's only consumer (GC) switches to it.
  Verify the encrypted-library aliasing case first (see PR7).
- **`fs_id` / `commit_id` stay SHA-1 by Seafile-protocol constraint**, not as an internal
  choice — re-addressing the whole fs/commit object tree by SHA-256 would break desktop sync for
  no benefit.
- **Migration policy** — incremental `005_*.cql`; never edit `001_initial_schema.cql`. Empty
  pre-deploy DB → no backfill.
- **The legacy seafhttp / desktop *client* flow is untouched** — it keeps minting SHA-1 as its
  native block id. "SHA-1 only at the legacy boundary" means SHA-1 leaves the *web frontend*
  and the *internal hot path*, not that web files stop carrying a SHA-1 id (they must, for
  desktop reads).

---

## f. Verification

- `go test ./internal/...` — especially the integration suites
  `web_block_upload_test.go`, `sync_protocol_regression_test.go`, `zip_download_test.go`,
  `publish_repair_integration_test.go`.
- Frontend Jest + eslint + production build.
- **Real desktop E2E** (the same protocol as the dual-hash validation in
  [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md)): upload via the web block flow, sync from a real
  Seafile desktop client, confirm checkout succeeds, compare local-vs-source file hash.
- Explicit `CheckFS` / sync-mapping regression coverage: mixed old/new fs_object rows must still
  compute the same Seafile-compatible file ids the desktop client sends.
- Confirm **0 SELECTs against `block_id_mappings`** when downloading SHA-256-canonical files.
