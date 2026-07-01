# SHA-256 canonical block IDs (and removing SHA-1 from the web client)

**Date:** 2026-06-29 (last updated 2026-07-01)
**Status:** Design + implementation tracker. PR1-PR8 are **merged to `main`**. The SHA-256-canonical
block-id effort is implemented end-to-end, including the GC recovery hardening that followed the PR7
reverse-mapping drop.
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
- `PR4` — in progress (branch `feat/sha256-canonical-block-ids-pr4`):
  - **DONE — `CreateFile` template SHA-1 fix** (blocker #3 + a latent desktop-incompat bug):
    Office-created files used SHA-256 as the block id and derived a SHA-256-based `fs_id`. Now
    `CreateFile` computes the template's SHA-1, uses it as the external (Seafile) block id, and
    writes the SHA-1→SHA-256 mapping + `blocks.sha1` via `RegisterUploadedBlockAndMapping`. Still
    pre-flip (block_ids = SHA-1), consistent with the other v2 writers.
  - **DONE — writer flip.** Because PR3 made all readers tolerant (64-hex passthrough +
    `seafile…` fallback), writers were flipped incrementally, each commit safe with mixed
    old/new rows. Production file-fs_object writers and their status:
    - **v2 publish path — flipped.** `createFileFSObjectRow` writes `block_ids` = SHA-256 +
      `seafile_block_ids_sha1` = SHA-1; covers direct upload (`finalizeStoredUploadMetadata`),
      web commit (`CreateFileFromBlocks`), `CreateFile`, OnlyOffice and copy (all reach it via
      `stagePendingPublishedFiles` → `createPendingPublishedFileRow`). `copyFSObjectToLibraryForPublish`
      reads `seafile_block_ids_sha1` so the copied `fs_id` stays SHA-1-derived (helper
      `seafileFSObjectBlockIDs`).
    - **seafhttp — flipped.** `createPendingSeafHTTPFileFSObject` resolves the POSITIONAL SHA-1
      list to SHA-256 via `streaming.BatchResolveBlockIDs` (not `stagedBlockIDs`, which is deduped
      for refs) and writes both columns. Mappings exist by commit time (blocks uploaded first).
    - **RecvFS — intentionally NOT flipped.** Desktop sync can send recv-fs *before* put-block
      (`TestSyncRecvFSBeforePutBlockPublishesDownloadableFile`), so the SHA-1→SHA-256 mapping may
      not exist yet and a strict resolve would fail. Desktop-sync files therefore keep
      `block_ids` = SHA-1 (empty `seafile_block_ids_sha1`) — the legacy layout readers already
      tolerate (serve falls back to `block_ids`; web download resolves via the forward mapping).
      Not a blocker-#5 violation (that is SHA-256 `block_ids` + empty SHA-1 column).
  - `fs_id` stays SHA-1-derived everywhere (unchanged). All dir-object writers are untouched
    (no `block_ids`).
  - **DONE — blocker #5 (fail-closed guard):** the boundary helpers now return `(list, ok)` and
    refuse a 64-hex `block_ids` with an empty SHA-1 column; `GetFSObject`, `PackFS`, `CheckFS`
    fs-id recomputation, and copy now all fail closed instead of serving / hashing a non-Seafile
    block-id list or silently degrading to "missing". The guard also validates that
    `seafile_block_ids_sha1` stays positional and well-formed when present: same length as
    `block_ids`, every entry 40-hex SHA-1, never a leaked 64-hex SHA-256.
  - **DONE — writer invariant hardening:** the canonical writers now fail fast if asked to persist
    mismatched or malformed block-id lists (`block_ids` must be 64-hex SHA-256,
    `seafile_block_ids_sha1` must be positional 40-hex SHA-1).
  - **DONE — blocker #6 (post-flip integration tests):** serve returns the SHA-1 list and the
    served JSON re-hashes to the `fs_id`; `PackFS` also serves the SHA-1 list, and the guard
    returns 500 for broken `GetFSObject`, `PackFS`, and `CheckFS` states.
  - **PR4 is functionally complete** (writer flip + guard + tests). `RecvFS` intentionally stays on
    the legacy SHA-1 layout (see above).
- `PR5` — **merged** (PR #107, branch `feat/sha256-canonical-block-ids-pr5`): the frontend stops
  computing/sending SHA-1 and the server derives it.
  - **Backend:** `file-from-blocks` manifest is `{sha256, size}` only; `manifestDigest` is over
    sha256+size. The external SHA-1 is read from `blocks.sha1` (via `ProbeBlockReuse`, surfaced
    through `verifyManifestBlocks`) and validated 40-hex — a ready block missing a well-formed
    `blocks.sha1` is sent to `needs_upload` (blocker #4, fail-closed). Re-uploading verified
    bytes now also repairs an existing `blocks` row whose `sha1` was empty: the writer keeps
    `INSERT ... IF NOT EXISTS` for immutable storage metadata but backfills `sha1` when blank,
    and rejects conflicting non-empty values. Removed the client-SHA-1 forward-mapping
    validation (`resolveManifestForwardMappings` / `getBlockIDMappingFn`).
  - **Replay/idempotency hardening:** successful `file-from-blocks` commits now persist the
    published file `fs_id` in the session result row, so replays / lost-race retries return the
    exact committed id instead of a best-effort re-derivation from fresh `ProbeBlockReuse` reads.
  - **Frontend:** the worker (`block-hash.js` / `block-hasher.worker.js`) computes a single
    SHA-256 digest per block (single digest per block; expected lower hashing CPU); `buildManifest` emits `{sha256, size}`.
  - Integration guards rewritten to the inverse semantics (forged client SHA-1 ignored;
    replay with a different client SHA-1 is the same file).
- `PR6` — **in progress** (branch `feat/sha256-canonical-block-ids-pr6`): cleanup + docs close-out.
  - **DONE — dead `X-Block-Hash-SHA1` reader removed.** `UploadBlock` no longer reads/cross-checks
    a client-supplied SHA-1 header (the frontend stopped sending it in PR5). The server still
    computes the SHA-1 from the real bytes and stores it in `blocks.sha1` — that is the canonical
    source — so the removed header was pure dead code.
  - **N/A — dead manifest-SHA-1 validation** (`resolveManifestForwardMappings` / `getBlockIDMappingFn`)
    was already removed in PR5.
  - **DONE — docs close-out.** This tracker + [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md) updated to
    the canonical end-state (`block_ids` = SHA-256, `seafile_block_ids_sha1` = SHA-1, client sends
    only `{sha256, size}`); stale regression-guard test names corrected.
  - **Deferred to PR7 — the `WriteVerifiedWebBlockMapping` collision guard.** Removing the client
    SHA-1 surface eliminates the *crafted-collision* threat that motivated the guard, but the guard
    is still the active web mapping writer; it is now harmless defense-in-depth and is removed as
    part of the PR7 mapping rework, not here.
- `PR7` — **merged to `main`**: drop the reverse mapping table
  `block_id_mappings_by_internal`.
  - **Migration `006_drop_block_id_mappings_by_internal.cql`** (`DROP TABLE IF EXISTS`).
  - **Stopped the reverse dual-write** in `WriteBlockIDMapping` and `WriteVerifiedWebBlockMapping`
    (both are now single forward INSERTs); removed the now-dead `WriteBlockIDMappingDualWrite`.
  - **GC cleanup sources the SHA-1 from `blocks.sha1`**: `GetBlockInfo` now also returns `sha1`
    (same single-partition point read, no extra query); `cleanupBlockMapping(orgID, internalID,
    externalSHA1)` deletes the single forward row by its `(org_id, external_id)` key. Removed
    `ListBlockMappingsByInternalID` and `DeleteBlockMappingResolved`; `DeleteBlockMapping` is a
    plain single-partition delete (no read-before-delete, no reverse cleanup).
  - **Fail-safe**: when `blocks.sha1` is empty (legacy/pre-PR2 row, or the canonical row is already
    gone), GC does NOT blind-delete a mapping — it records `gc_block_mapping_sha1_missing` and
    leaves the forward row as a harmless dangling pointer (a desktop SHA-1 GET 404s; it self-heals
    on re-upload).
  - **Tests**: unit `TestWorker_ProcessBlock_EmptyBlockSHA1LeavesForwardMappingObservable` (fail-safe);
    rewrote the GC mapping-cleanup unit/integration assertions to the forward-only model; added the
    encrypted-equivalent integration guard `TestGC_WorkerCleansForwardMappingViaBlockSHA1` (deletes a
    block whose external SHA-1 != internal block_id and asserts the forward row is resolved/cleaned
    from `blocks.sha1`). See the safety + performance section below.
- `PR8` — **merged to `main`**: GC recovery hardening for the forward-only mapping-cleanup model.
  - **Migration `007_gc_s3_orphan_mapping_recovery.cql`** extends `gc_s3_orphans` and
    `gc_s3_orphans_by_day` with `external_sha1` and `recovery_phase`, so recovery can still clean the
    forward SHA-1 mapping after the canonical `blocks` row is gone.
  - **Resurrection-safe recovery** re-checks `BlockExists` before mapping cleanup, so a re-uploaded
    live block does not lose its freshly re-created forward mapping.
  - **Stale-phase reset on new delete** ensures a fresh block lifecycle cannot inherit an old
    `pending_mapping_cleanup` phase and skip the physical S3 delete.
  - **Tests** pin both behaviors: `TestWorker_RecoverS3Orphans_PendingMappingCleanupKeepsResurrectedBlockMapping`
    and `TestWorker_RecoverS3Orphans_NewDeleteResetsStalePhaseAndStillDeletesS3`.

## Notes / Debt

- The current additive/no-backfill approach still assumes the current pre-deploy / empty-DB
  rollout. On a non-empty environment, untouched older `blocks` rows could still retain empty
  `sha1` until a dedicated backfill runs; the PR5 repair path now self-heals any such row that is
  re-uploaded with verified bytes, but it is not a bulk migration.
### Gating rule for PR4/PR5 — RESOLVED

The two preconditions that gated PR4/PR5 are both closed:

1. ~~`CreateFile` template block must persist a real `blocks.sha1`.~~ **Done (PR4):** `CreateFile`
   computes the template SHA-1, uses it as the external block id, and writes the mapping +
   `blocks.sha1` via `RegisterUploadedBlockAndMapping`.
2. ~~Fail-closed validation of `blocks.sha1` at the point of consumption.~~ **Done (PR5):**
   `file-from-blocks` reads `blocks.sha1` via `ProbeBlockReuse` and `needs_upload`s any ready
   block whose `blocks.sha1` is empty/non-40-hex (`isHex40`); the re-upload repairs the row (see
   `ensureBlockSHA1`, `IF EXISTS` backfill). An unvalidated SHA-1 never reaches an fs_object.

### Blocker checklist (single source of truth)

Carried forward from review. Status as of the PR3 partial branch:

| # | Item | Severity | Gates |
|---|---|---|---|
| 1 | ~~Desktop breaks if any Seafile endpoint serializes SHA-256 into `"block_ids"`.~~ **DONE (PR4)** — every Seafile-boundary serializer/​re-hasher (`GetFSObject`, `PackFS`, the serve/recompute path, and copy/publish) routes through the guarded `seafileServeBlockIDs` / `seafileFSObjectBlockIDs` helpers, which return `(list, ok)` and **fail closed (500)** on a 64-hex `block_ids` with an empty SHA-1 column — so a missed serializer cannot leak SHA-256 to the desktop client. | Critical | resolved |
| 2 | ~~`CheckFS` / `buildFSIDMapping` must use `seafile_block_ids_sha1` (fallback `block_ids`), never hash SHA-256 into the file JSON.~~ **DONE (PR3)** in `computeCorrectedObject`. | Blocker | resolved |
| 3 | ~~`CreateFile` template block must register a real SHA-1, not empty.~~ **DONE (PR4)** — `CreateFile` now computes the template SHA-1, uses it as the external block id, and writes the mapping + `blocks.sha1` via `RegisterUploadedBlockAndMapping` (mirrors `UploadFile`). Also fixes a pre-existing desktop-incompat bug: Office-created files used SHA-256 block ids / fs_id. | Blocker | resolved |
| 4 | ~~Validate `blocks.sha1` (40-hex, non-empty) before using it for `seafile_block_ids_sha1` / `fs_id`; else `needs_upload`.~~ **DONE (PR5)** — `file-from-blocks` reads `blocks.sha1` via `ProbeBlockReuse` and `needs_upload`s any ready block whose `blocks.sha1` is missing/non-40-hex (`isHex40`, fail-closed). | Blocker | resolved |
| 5 | ~~After the writer flip, no row may have SHA-256 `block_ids` with empty `seafile_block_ids_sha1`.~~ **DONE (PR4)** — `seafileServeBlockIDs` / `seafileFSObjectBlockIDs` return `(list, ok)` and fail closed when the SHA-1 column is empty and `block_ids` is 64-hex; `GetFSObject`, `PackFS`, `CheckFS`/`buildFSIDMapping`, and copy all refuse to serve/hash corrupted rows. (Writers already set both columns, so this is defense-in-depth.) | Blocker | resolved |
| 6 | ~~Add an integration test for a post-flip file object.~~ **DONE (PR4)** — `TestSyncServesSHA1BlockIDsForCanonicalFSObject`, `TestSyncPackFSServesSHA1BlockIDsForCanonicalFSObject`, `TestSyncRefusesToServeSHA256BlockIDsWithoutSHA1Column`, `TestSyncPackFSRefusesBrokenCanonicalObject`, and `TestSyncCheckFSRefusesBrokenCanonicalTree`. | Med | resolved |
| 7 | ~~Confirm sync reference-accounting feeds `block_references` in SHA-256.~~ **DONE** — it resolves before writing refs; keep reading `block_ids`. | — | resolved |
| 8 | ~~Do not drop the reverse mapping (`block_id_mappings_by_internal`) until the alias / encrypted / GC enumeration check passes.~~ **DONE (PR7)** — block encryption is deterministic (AES-CBC, derived fixed IV), so SHA-256 -> SHA-1 is 1:1 and `blocks.sha1` is the complete single-valued source; no honest internal id has multiple aliases. GC no longer enumerates; it resolves the single forward row from `blocks.sha1`. See the safety section. | Med | resolved |

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
stop matching what the desktop client expects. If a post-flip row is missing
`seafile_block_ids_sha1`, these paths must now **fail closed with 500**, not silently report the
object as missing.

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
| `block_id_mappings_by_internal` (reverse) | SHA-256 → SHA-1 | mixed | **DROPPED** (PR7, migration 006) — redundant with `blocks.sha1` |
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

### PR7 / PR8 — why it is safe and clean (verification + performance)

**Safety — the encrypted aliasing concern is cleared by deterministic encryption.**

1. **Block encryption is deterministic.** `EncryptBlockSeafile`
   ([crypto.go](../internal/crypto/crypto.go)) is AES-256-CBC with a **derived, fixed IV** (PBKDF2,
   "NO prepended IV"), not a random per-upload IV; the file key/IV are stable per repo. So the same
   plaintext block always produces the same ciphertext → the same SHA-256. SHA-1(plaintext) ↔
   SHA-256(ciphertext) is therefore **1:1 in practice**, encrypted or not. The "one SHA-1 → many
   SHA-256" hazard the table guarded against is not realized by this code. This is now pinned by the
   unit test `TestEncryptBlockSeafile_DeterministicForSameKeyIVAndPlaintext`.
2. **GC needs only the SHA-256 → SHA-1 direction, which is single-valued.** Each `blocks` row (one
   SHA-256) carries exactly one `sha1`. The reverse table's multi-alias capability was only ever
   populated by stale/orphan ("reverse-only") rows for defensive cleanup, never by honest content —
   and those vanish with the table.
3. **`blocks.sha1` is complete.** Every block-materialization path co-writes `blocks.sha1` with the
   forward mapping via `UpsertBlockMetadataWithSHA1`: web block flow, seafhttp upload/finalize,
   desktop `PutBlock`, `CreateFile`, OnlyOffice, copy. On the pre-deploy/empty DB there are no
   pre-PR2 rows with an empty `sha1`; PR5 self-heals any such row on re-upload.
4. **No blind deletes, fail-closed and observable.** When `blocks.sha1` is empty (or the canonical
   row is already gone, so it cannot be read), GC does NOT delete a forward row by guessing — it
   increments `gc_block_mapping_sha1_missing` and leaves the row as a harmless dangling pointer (a
   desktop bare-SHA-1 GET 404s; it self-heals on re-upload). Pinned by
   `TestWorker_ProcessBlock_EmptyBlockSHA1LeavesForwardMappingObservable` and the rewritten
   missing-canonical-row guard.
5. **No regression vs. today.** The previous code already deleted the forward row unconditionally
   (`DeleteBlockMappingResolved`); PR7 keeps identical delete semantics and only changes the SHA-1
   *source* (from a reverse-table enumeration to the single `blocks.sha1`). The encrypted-equivalent
   path (external SHA-1 ≠ internal block_id) is covered by
   `TestGC_WorkerCleansForwardMappingViaBlockSHA1`.
6. **Crash recovery now covers mapping cleanup too.** PR8 extends the existing
   `gc_s3_orphans` recovery row with `external_sha1` and `recovery_phase`, written before
   `FinalizeBlockDelete`. If a worker crashes/redeploys after the canonical `blocks` row is gone,
   recovery can still either (a) retry S3 deletion in `pending_s3`, or (b) skip straight to forward
   mapping cleanup in `pending_mapping_cleanup`. The fail-safe metric
   `gc_block_mapping_sha1_missing` remains only for genuinely legacy / metadata-free rows where no
   `blocks.sha1` was ever available.
   - **Resurrection guard (both phases).** Block content is deterministic, so a re-uploaded block
     reuses the same `block_id` + SHA-1 and re-creates a *live* forward mapping. Recovery must not
     delete that live mapping. Both recovery phases therefore re-check `BlockExists` before touching
     the mapping: `pending_s3` defers while the canonical row is present, and `pending_mapping_cleanup`
     discards the stale recovery row (incrementing `gc_s3_orphan_resurrected_discarded`) instead of
     cleaning the mapping. Pinned by
     `TestWorker_RecoverS3Orphans_PendingMappingCleanupKeepsResurrectedBlockMapping`.
   - **Stale-phase reset on a new delete.** A NEW block delete writes its recovery row via
     `StartBlockDeleteOrphan`, which always resets the phase to `pending_s3` (and `retry_count`,
     `last_error`) — even if a stale row from an older delete of the same `block_id` was left at
     `pending_mapping_cleanup`. Otherwise recovery would inherit the stale phase and SKIP the
     physical S3 delete for the new lifecycle, leaking the new object. `first_seen_at` is preserved
     so the discovery projection stays a single in-place row; the reset is fail-closed (`IF EXISTS`).
     Pinned by `TestStore_StartBlockDeleteOrphan_ResetsStalePendingMappingCleanup` and
     `TestWorker_RecoverS3Orphans_NewDeleteResetsStalePhaseAndStillDeletesS3`.

**No tombstone / hot-partition risk (Cassandra access pattern).** Both queries hit a full partition
key, so there is no `ALLOW FILTERING`, no clustering-row scan, and no tombstone accumulation to read
through:
- `GetBlockInfo` reads `blocks` by `((org_id), block_id)` — a single-row point read; `sha1` is the
  same row, so it adds no query.
- `DeleteBlockMapping` deletes `block_id_mappings` by `((org_id, external_id))` — a single-partition
  delete with no read-before-delete.
- The dropped table was the only one that required reading a clustering range
  (`block_id_mappings_by_internal WHERE org_id=? AND internal_id=?`); removing it removes that
  pattern entirely. Critically, GC never queries `block_id_mappings WHERE internal_id=?` (that would
  be `ALLOW FILTERING` over the whole table) — `blocks.sha1` makes the SHA-256 → SHA-1 lookup a keyed
  single-row read.

**Performance / cost wins.**
- **Upload hot path: one fewer write per block.** `WriteBlockIDMapping` /
  `WriteVerifiedWebBlockMapping` drop the second INSERT (the reverse row), halving the mapping-write
  cost on every block upload (web + seafhttp/desktop). `WriteVerifiedWebBlockMapping` also no longer
  needs the no-op reverse rewrite on the already-mapped path.
- **GC block delete: fewer round-trips and no LWT batch.** Per deleted block GC now does one keyed
  point read (folded into the `GetBlockInfo` it already issues — **zero extra reads**) plus one
  single-partition delete, instead of a clustering-range SELECT (`ListBlockMappingsByInternalID`)
  followed by a per-alias `LoggedBatch` deleting two tables. No `LoggedBatch`, no reverse partition
  writes.
- **Less storage + compaction.** One fewer table to store, replicate, repair, and compact; the
  upload path stops generating reverse-partition tombstones on GC.

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
