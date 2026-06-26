# Web content-addressed (block) upload flow

**Date:** 2026-06-22
**Status:** Phase 1 implemented (backend + frontend) + post-review hardening.
Gated OFF by default by a **server-side** flag (`web_uploads.enable_web_block_upload`
/ `WEB_UPLOADS_ENABLE_WEB_BLOCK_UPLOAD`); the frontend flag (`enableBlockUpload`) is
driven from it via bootstrap. Encrypted libraries and public share/upload links
are out of scope for phase 1.

### Feature flag (server-authoritative)

- Config: `web_uploads.enable_web_block_upload` (default `false`) in all
  `configs/*.yaml`; env override `WEB_UPLOADS_ENABLE_WEB_BLOCK_UPLOAD=true`.
- When off, `block-upload-session` and `file-from-blocks` return 404 and the
  `?session=` mode of `/blocks/check` + `/blocks/upload` is rejected — the routes
  are always registered, so this flag is the real gate (defense in depth: the UI
  flag alone would not stop a direct API call).
- `bootstrap` emits `enableBlockUpload` (real boolean) so the UI matches the server.

### Post-review hardening (2026-06-22)

- **Idempotency vs counters:** the idempotent result is persisted immediately
  after the file is published, *before* the (best-effort) storage-counter update —
  a counter failure no longer leaves a `committed` session without a result, and
  never returns 500 after the file already exists.
- **Storage-class correctness:** `/blocks/upload?session=` resolves the block
  store by the session repo's storage class (not generic hostname/"hot"), so a
  block lands in the same backend `file-from-blocks` later verifies.
- **Parallel verification:** session-mode `/blocks/check` and the commit's
  per-block checks run `ProbeBlockReuse` with bounded concurrency (20), not
  serially — large manifests no longer mean thousands of serial round-trips.
- **`pub:` is not permanent:** a block alive only via a foreign publish-attempt
  ref (`pub:`) is treated as `needs_upload`; only a committed-file ref (`fs:`) or
  this session's own `up:` ref qualifies (covered by an integration test).
- **parent_dir is part of the intent:** commit requires
  `session.parent_dir == req.parent_dir`.
- **Folder uploads** stay on resumable.js in phase 1 (block flow is single-file
  only) to avoid flattening directory structure.
- **Batched check:** the client batches `/blocks/check` (≤5000 hashes/request) to
  stay under the server's 10000 cap for very large files.
- **Staging does not pay logical storage quota (R5):** `/blocks/upload?session=`
  no longer applies the user's *logical* storage quota per block. That quota is a
  property of the final file delta and is decided once at `file-from-blocks`;
  charging it per staged block wrongly rejected valid cases like a same-size
  overwrite (delta ≈ 0). Traffic is still charged per block, and the legacy
  no-session path keeps its per-block admission check (covered by an integration
  test).

This is the implementation of **Option B** from
[UPLOAD-RESUME-ANALYSIS-20260619.md](./UPLOAD-RESUME-ANALYSIS-20260619.md): the web
uploader becomes content-addressed, which subsumes **resumable uploads**,
**network-bandwidth dedup**, and a **lighter commit** — and is naturally
multi-node because the resume state lives in shared storage, not an in-memory
tracker.

---

## Flow

```
1. POST /api/v2/repos/:repo_id/block-upload-session/   → server-issued session_id
2. client splits file into fixed 8 MB blocks, SHA-256 + SHA-1 each (Web Worker)
3. POST /api/v2/blocks/check?session=...               → { existing, missing } (by SHA-256)
4. POST /api/v2/blocks/upload?session=... (per missing) → store + materialize (SHA-256)
5. POST /api/v2/repos/:repo_id/file-from-blocks/        → commit from dual-hash manifest
```

The resume state is the `missing` set recomputed by step 3 from shared storage —
no offsets, no in-memory trackers, no sticky routing required.

### Key files

- Backend
  - `internal/db/migrations/004_block_upload_sessions.cql` — `block_upload_sessions` table (org/user/repo as UUID, like the rest of the schema)
  - `internal/db/block_upload_sessions.go` — session CRUD, `ClaimBlockUploadSessionForCommit` (LWT) / `ReleaseBlockUploadSessionCommit`, `BlockHasReferrer` / `ListBlockReferrers`
  - `internal/api/v2/block_upload_session.go` — `CreateBlockUploadSession`, `libraryEncrypted`
  - `internal/api/v2/blocks.go` — session-aware `CheckBlocks` + `UploadBlock` materialization
  - `internal/api/v2/file_from_blocks.go` — `CreateFileFromBlocks` (commit from manifest)
  - `internal/api/v2/files.go` — `finalizeStoredUploadMetadata[Once]` refactored to take `blockIDs []string`
- Frontend
  - `frontend/src/components/file-uploader/block-hasher.worker.js` — SHA-256 per block
  - `frontend/src/components/file-uploader/block-hasher-worker-factory.js` — webpack worker bundling (isolates `import.meta`)
  - `frontend/src/components/file-uploader/block-upload-orchestrator.js` — full flow + eligibility helpers
  - `frontend/src/utils/seafile-api.js` — `createBlockUploadSession`, `checkBlocks`, `uploadBlock`, `createFileFromBlocks`
  - `frontend/src/utils/constants.js` — `enableBlockUpload`, `blockUploadThresholdMB`

---

## Design rules (R1–R11) and how they are enforced

These rules came out of a careful review of the existing block infrastructure.
They are the reason the implementation is safe; do not relax them without
re-reading this section.

- **R1 — Never publish a block just because it exists in S3.** `/blocks/check`
  (and `CheckBlocksParallel`) is only a *physical existence oracle*. The commit
  runs `db.ProbeBlockReuse` per block and only accepts `Reusable` (metadata +
  live reference + no GC fence). `NeedsPut`/`BlockedByGC` → returned in
  `needs_upload`.
- **R2 — Anti-orphan is P0.** A block uploaded via the legacy `/blocks/upload`
  (no session) is an S3 object with **no Cassandra metadata/ref** and can leak.
  The session flow materializes a provisional reference with TTL
  (`gc_provisional_block_refs`), so an abandoned upload self-expires and the GC
  sweeper reclaims it.
- **R3 — Session-aware check.** `/blocks/check?session=` reports a block as
  `existing` only when `ProbeBlockReuse == Reusable`, not merely present in S3 —
  avoiding the "exists in S3 but unmaterialized → commit says NeedsPut" trap.
- **R5 — Quota in three planes.** Traffic is charged per block at
  `/blocks/upload`; the **logical** repo/user storage quota is decided once at
  commit from the file delta (`currentUploadStorageDelta`), never per block — the
  session staging path does NOT apply it (a staged block is transient, governed
  by a provisional ref + TTL, and the final delta may be ≈ 0 for an overwrite).
  The legacy no-session path still runs the same *logical* `CheckStorageQuota`
  per block (it predates this flow; it is not a separate physical/staging cap).
  No double counting. See the staging-cap limitation below.
- **R6 — Manifest validation.** Fixed 8 MB blocks except the last (`0 < last ≤ 8 MB`),
  `sum(sizes) == size`, 64-hex hashes, bounded block count/total size. Repeated
  blocks are allowed **only when every occurrence of a SHA-256 declares the same
  size** — the same content cannot have two sizes, and rejecting it stops a lie
  from surviving the last-wins size dedup and corrupting the file's size/offsets.
- **R7 — Server-issued, idempotent sessions.** `session_id` is a random 256-bit
  server-minted token bound to `(org, user, repo)` with a TTL.
  `file-from-blocks` is idempotent per `session + manifest_digest`: a retried
  commit returns the original result instead of auto-renaming a duplicate.
  **Concurrency-safe:** the commit is claimed via a Cassandra LWT
  (`ClaimBlockUploadSessionForCommit`), so exactly one of N concurrent commits
  runs finalize and the rest wait briefly for the winner's persisted result. If
  the winner takes longer than the bounded poll window (~10s), losers return
  `409 "commit still in progress; retry"` and a later retry returns the same
  result. A failed finalize releases the claim so the client can retry.
- **R8 — Commit accepts permanent-reusable OR session-owned blocks.** A block is
  committable only if it is kept alive by **(a)** this session's own provisional
  referrer `up:<session_id>`, or **(b)** a permanent committed-file referrer
  `fs:*` (a legitimate cross-file dedup hit the client skipped uploading). A
  `pub:*` referrer does **NOT** count as permanent: it is a transient
  publish-attempt pin that vanishes if that attempt loses its HEAD CAS, so
  trusting a *foreign* `pub:` could leave this file pointing at a GC-able block
  (`blockHasPermanentReference` only matches `fs:`; covered by
  `TestWebBlockUploadForeignPubRefNotPermanent`). This commit's own
  publish-attempt staging still pins every block under the commit *before* its
  HEAD CAS, so a concurrent rollback of another session's provisional ref cannot
  drop liveness for this file.
- **R9 — `/blocks/upload?session=` always materializes**, even when
  `PutBlockData` was a no-op because the object already existed in S3. The point
  is to *govern* the block, not just store bytes.
- **R10 — Dual-hash: SHA-1 is the external Seafile block ID, SHA-256 is the
  storage identity.** A file's `fs_object.block_ids` MUST be SHA-1 (40-hex): the
  Seafile desktop/mobile sync client parses block IDs as 20-byte SHA-1 and cannot
  read a file whose block IDs are 64-hex SHA-256 (it fails the fs-object load with
  "File <id> does not exist" at checkout — see
  [the desktop-compat regression note](#desktop-and-mobile-sync-compatibility-dual-hash)).
  So the client hashes each 8 MB block with **both** SHA-256 (storage/internal ID:
  S3 key, `blocks`, `block_references`, GC, dedup, `/blocks/check`+`/blocks/upload`)
  and SHA-1 (external ID), and the commit manifest carries both
  (`{sha1, sha256, size}`).

  **The mapping is server-authoritative, never client-asserted, and the FORWARD
  table is the only source of truth.** `UploadBlock` computes the SHA-1 from the
  block's REAL bytes (alongside the SHA-256 it already verifies) and writes the
  `block_id_mappings` row (SHA-1 → SHA-256). `file-from-blocks` **never mints a
  mapping**: it validates the manifest's SHA-1 against the forward mapping
  (`GetBlockIDMapping(sha1)`):
  - forward **missing** → `needs_upload` (the client re-uploads, which writes the
    verified row);
  - forward **resolves to a different SHA-256** → 400 (`manifest sha1 does not
    match the block content`);
  - forward **resolves to the declared SHA-256** → the manifest SHA-1 is accepted
    and written into the fs_object.

  The forward table is used (not the reverse `block_id_mappings_by_internal`)
  because the reverse schema allows several SHA-1 aliases per SHA-256 and is a
  best-effort GC/repair projection that can lag the forward row — depending on it
  would risk picking a stale alias or rejecting a valid commit. Download resolution
  (`streaming.BatchResolveBlockIDs`) also uses the forward table, so both the web/v2
  path and the desktop seafhttp path serve the same bytes. SHA-1 is folded into
  `manifestDigest`, so an idempotent replay with the same SHA-256s but a different
  SHA-1 is a different file (409), not a silent id/object mismatch.

  **No Paxos on the hot path, and legacy is untouched.** The forward mapping for
  the web flow is written by a **web-only** helper, `WriteVerifiedWebBlockMapping`
  (via `RegisterWebUploadedBlockAndMapping`), which guards against remapping an
  existing SHA-1 to different content (a crafted SHA-1 collision could otherwise
  corrupt downloads of committed files) with a plain read-before-write, **not** a
  Cassandra LWT. Per-block Paxos would cause latency/contention/timeouts in
  multi-DC/multi-node deployments; the only residual gap is two *colliding* blocks
  written concurrently in the tiny read→write window (astronomically unlikely), and
  the commit-side forward check is the integrity authority regardless.

  The shared legacy/seafhttp path keeps using the original `WriteBlockIDMapping`
  (plain idempotent dual-write) unchanged — **it pays no extra read and sees no
  behavior change**. The two writers diverge only in the conflict guard, which is
  needed solely on the new web flow where the server, not a trusted desktop client,
  mints the SHA-1.

Although the reverse row is non-authoritative, the web-only writer still fails
the upload if rewriting `block_id_mappings_by_internal` fails after the forward
row is in place. That is deliberate fail-closed behavior: a client retry can
heal the auxiliary GC/repair projection instead of silently leaving it behind.
- **R11 — Sizes validated against real metadata.** The commit checks
  `manifest.size == ProbeBlockReuse.SizeBytes` per block; a manifest cannot
  declare a size that disagrees with the stored block.

---

## Desktop and mobile sync compatibility (dual-hash)

**Symptom (the bug this design fixes).** A file uploaded via the web block flow
synced through the `check → commit → fs → data` phases on the desktop client but
then failed checkout with `File <40-hex> does not exist` / `Failed to checkout
file ...`, for every web-block file, in fresh libraries. The fs_object existed,
hashed correctly, and pack-fs served it fine; the dir entry was correct. The only
anomaly was the **block IDs inside the file fs_object were 64-hex SHA-256**.

**Root cause.** The stock Seafile sync client parses a file (seafile) object's
`block_ids` as 20-byte (40-hex) SHA-1. A 64-hex ID makes the fs-object parse fail,
so `seaf_fs_manager_get_seafile` returns NULL and checkout reports the file as
non-existent — *before* any block is even requested. The limitation is in the
**client parser**, not in the server's block resolution, which is why the web/v2
download path (which resolves 64-hex IDs directly) worked while desktop sync did
not. The original R10 ("no SHA-1 mapping for pure SHA-256; SHA-1 stays only at the
Seafile edge") was the wrong assumption — there was no SHA-1 edge for these files.

**Fix.** Dual-hash (see R10 above): the client computes SHA-1 alongside SHA-256
per block and the manifest carries both. The server computes SHA-1 from the real
bytes at upload and owns the `block_id_mappings` (forward) row; the commit
validates the manifest's SHA-1 against that forward mapping and writes the
verified SHA-1 into the fs_object (never trusting the client's SHA-1, never minting
a mapping, never using the reverse table as truth). End state per the design
decision:

| Field | Value |
|---|---|
| `fs_objects.block_ids` | **SHA-1** (desktop/mobile Seafile compat) |
| `blocks.block_id` | SHA-256 |
| `block_references.block_id` | SHA-256 |
| S3 object key | SHA-256 |
| `block_id_mappings` (forward, source of truth) | SHA-1 → SHA-256 (written at upload, from verified bytes, non-Paxos) |
| `block_id_mappings_by_internal` (reverse) | SHA-256 → SHA-1 (best-effort GC/repair projection only) |


**Validated end-to-end (2026-06-24) against a real Seafile desktop client** on
this implementation, including the file types that originally failed:

1. Uploaded files through the web block flow with the feature flag enabled in dev
   (a ZIP and a MOV — the cases that previously broke — plus a large binary).
2. Confirmed in the database that every committed `fs_objects.block_ids` entry is
   a 40-hex SHA-1, and that its `sha1 → sha256` forward mapping resolves to the
   real SHA-256 storage identity (`blocks` / `block_references` stay 64-hex).
3. Synced the library from the desktop client: checkout **succeeds**, with no
   `File <fs_id> does not exist` / `Failed to checkout file ...` errors.
4. Compared the local/downloaded file hash against the source bytes — identical.
5. Re-verified the web download path (server-side SHA-1 → SHA-256 resolution)
   serves the same bytes.

**Result: desktop/mobile sync and web download both work** for files uploaded via
the web block flow. The regression introduced by merge #91 (SHA-256 block IDs in
the file object) is fixed and verified, with the internal SHA-256 model unchanged.


This branch prevents new SHA-256 `fs_objects.block_ids`, but it does not repair
files already committed by the broken merge that stored SHA-256 block IDs in the
file object. Repairing those files is a separate migration/rewrite problem
because changing the block IDs changes the file `fs_id`; in dev/local the safe
answer is delete and re-upload.
Regression guards (`internal/integration/web_block_upload_test.go`):
`TestWebBlockUploadFSObjectUsesSHA1ForDesktopCompat`,
`TestWebBlockUploadForwardMappingIsSourceOfTruth`,
`TestWebBlockUploadCommitIndependentOfReverseMapping`,
`TestWebBlockUploadReplayDifferentSHA1IsDifferentFile`.

---

## Out-of-branch debts and limitations (do not lose this knowledge)

- **Legacy `/blocks/upload` without a session does not materialize metadata.** It
  is used by desktop/mobile via their own commit paths; for any *new* caller,
  uploading without a session leaves an ungoverned S3 object. The web flow always
  passes a session. If another non-sync caller is added, it must either use a
  session or its own materialization + cleanup.
- **No cross-method dedup (web ↔ desktop) — upload side only.** Download compat is
  solved (web files now carry SHA-1 block IDs, see the dual-hash section above).
  What remains unsolved is *dedup at upload*: web uses fixed 8 MB blocks while
  desktop uses FastCDC variable blocks, so the same file uploaded by each method
  has different block boundaries → different hashes → stored twice. See
  [CHUNKING-ANALYSIS.md](./CHUNKING-ANALYSIS.md). Closing this would require
  FastCDC in the browser/worker — a separate phase.
- **Encrypted libraries are rejected** by the block flow: SHA-256 is computed
  over plaintext on the client, incompatible with server-side Seafile block
  encryption. Encrypted repos keep using the legacy uploader. A future phase
  would need client-side encryption before hashing.
- **Public share/upload links are out of scope**: they have no authenticated
  session token, so they cannot mint a block-upload session. They keep restarting
  from zero. A signed-session variant would be a separate design.
- **`GetFileUploadedBytes` remains a safe stub returning 0.** Real resume is now
  provided by `/blocks/check`; the old endpoint is kept only for the legacy
  frontend path.
- **No staging-bytes cap (uncommitted backend consumption).** Because session
  staging skips the logical storage quota (R5), and there is no separate cap on
  *uncommitted* staged bytes, an authenticated user can `PutBlockData` many new
  blocks under sessions and never commit. This is bounded — not unbounded — by:
  (a) the upload **traffic quota**, charged per block at `/blocks/upload`, which
  hard-blocks free/hard-tier users once their monthly upload allowance is spent;
  and (b) the **48h provisional-ref TTL** + GC sweeper, which reclaims abandoned
  blocks. The gap is real for **soft-tier or disabled traffic quota**, where the
  brake is only (b): up to 48h of transient backend bytes. The proper fix is a
  dedicated staging-bytes cap (a per-user/org counter of materialized-but-
  uncommitted bytes, decremented at commit/expiry, checked at `/blocks/upload`) —
  a separate limit from the final logical quota, so it would NOT reintroduce the
  same-size-overwrite rejection. It is a follow-up feature, not yet implemented.
- **Session TTL is fixed from mint time (not sliding).** `block_upload_sessions`
  is created with a 48h TTL when the session is minted, and each provisional
  `up:<session_id>` block ref gets its own 48h TTL when that block is uploaded.
  Uploading more blocks does **not** refresh the session row, so a very long-
  lived tab can reach a state where some later blocks still have live provisional
  refs but the original `session_id` has expired. Resume beyond ~48h should be
  treated as "new session + re-check/re-upload", not as a guaranteed seamless
  continuation of the old session.

---

## Validation status

**Backend: validated live** (`internal/integration/web_block_upload_test.go`, all
passing against real Cassandra + MinIO via `docker compose`):

- round-trip upload + download; dedup (re-check reports existing)
- multi-block ordering + download (8 MB block + tail)
- R1: manifest block never uploaded → `needs_upload`
- R1/R3: S3-only block (legacy upload, no metadata) → reported missing + commit refuses
- R6: manifest sum/size mismatch → 400
- R6: same SHA-256 declared with conflicting sizes → 400
- R11: block size disagrees with stored metadata → 422
- GC fence (`blocks.gc_state='deleting'`) → commit refuses
- R7: idempotent sequential replay → no duplicate file
- R7: **concurrent double commit → single file** (this caught a real read-then-act
  race, fixed with the LWT claim)
- R4: rename + re-download + history over a web-block-upload file (SHA-1 `block_ids`
  fs_object resolving to SHA-256 storage)

## Frontend wiring (implemented, behind flag)

`file-uploader.js` diverts eligible files (`shouldUseBlockUpload`: flag on, size ≥
`blockUploadThresholdMB`, non-encrypted, Worker+SubtleCrypto) out of resumable.js
in `onFileAdded` → `maybeBlockUpload` → `runBlockUpload(uploadFileViaBlocks)`,
rendering a resumable-file-shaped adapter in the existing progress dialog
(hashing = first half of the bar, uploading = second half). Anything ineligible
falls through to the unchanged resumable.js path. Validated via Jest (component +
orchestrator) + eslint + production build.

**v1 limitations (behind flag, documented):**
- The "replace existing file" dialog is bypassed for block-flow files; they
  auto-rename on conflict (`replace:false`).
- Upload bitrate remains approximate for block-flow files because they do not
  contribute bytes to `this.resumable.files`; total progress/cancel/retry are
  now wired through the existing dialog.

**Frontend UX gaps (verified, deferred — polish before flag-on):** the block flow
is mapped onto the *legacy* resumable.js progress UI, which only understands
"uploading bytes" and "saving". The mapping is lossy:
- **No per-phase state.** The four real phases (hashing → checking existing blocks
  → uploading missing blocks → committing/finalizing) collapse into one bar:
  hashing is `0→50%`, upload is `50→100%`
  ([file-uploader.js](../frontend/src/components/file-uploader/file-uploader.js)
  `onHashProgress`/`onUploadProgress`). `check` is not represented (bar sits at
  50%) and `commit` is not represented (bar sits at ~100%), so both read as a
  stall. The UI should show explicit `Hashing… / Checking… / Uploading… /
  Committing… / Finalizing… / Uploaded` states.
- **Progress ≠ bytes uploaded.** Because the first half of the bar is local
  hashing, the percentage does not represent bytes actually sent; for a heavily
  deduplicated file the "upload" half completes almost instantly while the bar
  already spent half on hashing. Progress weighting needs an explicit per-phase
  definition.
- **Header/row mismatch after a successful commit is still possible.** The
  dialog header is driven by the global `isUploading()` check, while each row
  renders its own legacy resumable-style state from `isUploading()`, `isSaved`,
  `progress()`, and `isFileSaving()`. A block-flow entry that has stopped
  reporting `isUploading()` but has not yet been marked `isSaved` can therefore
  transiently render as `Waiting...` or `Saving...` while the header already
  says `All files uploaded`. If a page refresh shows the file in the listing,
  that confirms the backend commit already succeeded; the mismatch is local UI
  state, not data loss. This branch treats it as a frontend polish gap, not a
  backend integrity issue.
- **Speed shows `0.00 B/s`.** A block entry sets `remainingTime = 0` and feeds no
  bytes into `this.resumable.files`, so the legacy `getBitrate()` sees no activity
  and reports `0.00 B/s` even while blocks are uploading. No real throughput/ETA
  is surfaced for block-flow files.
- **`Saving…` appears early and can last a while.** Once the bar reaches ~100% the
  file reads as "saving" while the commit is still running. The commit is **not**
  zero-cost: for a multi-GB file (e.g. 12.3 GB ≈ 1,500+ blocks) it verifies every
  block (presence + metadata + refs + size), takes the file lock, checks logical
  quota, does the HEAD CAS, promotes provisional→permanent refs and cleans up — so
  on large files the `Saving…` phase is genuinely visible and currently has no
  progress of its own.
- **Global vs per-file inconsistency.** The aggregate row can read
  `File Uploading… 58% (0.00 B/s)` while one file shows `Uploaded` and another
  shows `Saving…` — the block flow is not fully integrated with the legacy global
  progress/bitrate aggregator.
- **No dedup/throughput observability.** The UI does not surface bytes already
  existing vs actually uploaded vs pending, nor the deduplicated percentage, so a
  fast repeat upload or an oddly-moving bar is unexplained to the user.
- **~~Aggregated concurrency of multiple large files is unbounded across files.~~ —
  FIXED (PR2, global block-upload limiter).** Previously each large file spawned its
  own orchestrator with its own pool, so `N` files ran `N × M` concurrent block
  operations with no cross-file ceiling. A single shared `createBlockLimiter` now caps
  the total blocks on the wire to the configured `simultaneous_uploads` ceiling; see
  the "Frontend re-implementation from main → PR2" section below. (Hashing is still
  per-file off-thread; the limiter bounds the network/backend block operations.)
- **Cancel during `Saving…`/commit is undefined.** Cancel aborts the in-flight
  client requests, but its effect once the commit has started is not specified
  (whether the commit is interrupted, whether provisional refs linger until TTL,
  whether the same session can be retried). With a long `Saving…` phase this is no
  longer a rare case.

These are acceptable for a flag-gated rollout and are the polish items for a
follow-up. **Still pending: browser end-to-end validation** (`docker compose up`
+ `npm start`, enable `enableBlockUpload`).

## Frontend re-implementation from main (small auditable PRs)

An earlier branch `fix/web-block-upload-frontend-state-and-stall` fixed several of the
gaps above but over-refactored `file-uploader.js` and introduced regressions (per-file
block concurrency, duplicate/stale dialog rows, mixed sources of truth). It is kept only
as **reference**; the fixes are being re-applied cleanly from `main` in small PRs, each
leaving `npm test` + lint + build green and the legacy resumable path untouched.

**Finding — "legacy retry can start without a shared upload target" does NOT exist on
`main`; it was self-inflicted by the reference branch.** On `main`,
`this.resumable.opts.target` is only ever *written* (unconditionally on a new batch when
`isUploadLinkLoaded` is false, in `onFileAdded`) or read — it is **never cleared to
`''`**. So a legacy retry always reuses the last valid token. The reference branch added
`resetSharedUploadTarget({ clearTarget: true })` (clearing the target in
`onError`/`onComplete`/etc.), which *created* the empty-target window that
`withSharedTargetForLegacyRetry` then had to guard. The genuine fix from those commits
(single-fetch session-link reuse; never re-mint a token under an in-flight upload) is
**already in `main`** via the `isUploadLinkLoaded` guard in `onFileAdded`. So the
retry-target machinery is **not** ported, and the upcoming duplicate/single-source work
must **not** reintroduce any target clearing.

**PR1 — block phase / bitrate / watchdog (done; behind flag).**
- Closed: false `Saving…` at 3% / missing per-row Cancel — block entries now carry an
  explicit `_phase` (`hashing→uploading→saving→done`); `isFileSaving` is true only in
  `saving`. Tests: `upload-finalization.test.js` "block-upload entry state (isFileSaving)".
- Closed: silent infinite hang on a dropped connection — two-phase watchdog in the
  orchestrator (`BLOCK_STALL_TIMEOUT_MS` 30s / `BLOCK_RESPONSE_TIMEOUT_MS` 120s /
  `CONTROL_PLANE_TIMEOUT_MS` / `COMMIT_TIMEOUT_MS`, retryable `StallTimeoutError`).
  Tests: `block-upload-orchestrator.test.js` "stall watchdog" + "phase reporting".
- Closed: `0.00 B/s` for block files — real wire bytes via `onTransferProgress` →
  `sampleBlockUploadBitrate` / `aggregateBlockUploadBitrate`, summed with the isolated
  `legacyUploadBitrate` in `calculateUploadBitrate`. Tests: `upload-finalization.test.js`
  "block-upload throughput" + `file-uploader.test.js` "does not fake block bitrate…".
- Closed: `setUploadFileList` dropped in-flight block entries — `mergeUploadFileList`
  unions resumable files with block entries (deduped by `uniqueIdentifier`). Test:
  "preserves block entries when a legacy upload is added later".
- Cancel-All now cancels every `!isSaved` entry (incl. a block at 100% in commit). Test:
  "cancel all still aborts a block upload that is already in saving at 100%".

**PR2 — global STATIC block concurrency limiter (done; behind flag).**
- Closed: CAS ignored global concurrency — each file ran its own pool of `3`, so N
  files opened N×3 requests. New `frontend/src/utils/block-upload-limiter.js`
  (`createBlockLimiter`) is a SINGLE semaphore shared by every block upload; the
  ceiling is the config value `simultaneous_uploads` (via
  `getBaselineSimultaneousUploads(props.simultaneousUploads || resumableSimultaneousUploads)`,
  built once in `componentDidMount`) — the hardcoded `concurrency = 3` is removed from
  the orchestrator (now `concurrency` is only a no-limiter test fallback, default 1).
  Every block `acquire`s a slot before its network upload and releases after, so the
  TOTAL blocks on the wire across ALL files never exceed the ceiling. Test:
  `block-upload-orchestrator.test.js` "never exceeds the shared ceiling across multiple
  files (no N×max)".
- `acquire({ signal })` is signal-aware: an already-aborted signal rejects without
  taking a slot; a waiter whose signal aborts is removed from the FIFO and never
  uploads (no "ghost" upload after cancel). Tests: `block-upload-limiter.test.js`
  "a waiter whose signal aborts…" + orchestrator "a block waiting for a slot is NOT
  uploaded after its file is cancelled".
- Anti-starvation: a file spawns at most `maxConcurrency` workers (NOT one waiter per
  block) and each worker re-acquires at the back of the FIFO after every block, so
  files interleave instead of the first one monopolising the queue. Test: orchestrator
  "does not starve later files…".
- STATIC ceiling here (`effective === max`); the adaptive ramp lands in PR3.
  The limiter is `reset()` on dialog close / cancel-all. Legacy resumable concurrency
  (`upload-finalization.js` adaptive engine) is untouched — block-only.

**PR3 — adaptive ramp for the block limiter (done; behind flag).**
- The limiter's live ceiling `effective` now starts at **1** and climbs one step per
  run of healthy throughput samples up to `max`, and drops back to **1** on a
  sustained bitrate collapse or a block failure/retry — matching the config intent
  ("the adaptive client starts at 1 and can ramp up to this ceiling"). Sample-based
  and deterministic (a "sample" is one `noteBitrate` call, throttled upstream to
  ~one/500 ms): `RAMP_MIN_SAMPLES=3` healthy samples per +1 step, `DEGRADE_MIN_SAMPLES=2`
  low samples (`< 0.6 × smoothed`) before dropping to 1; idle/zero samples (pure
  hashing) are ignored.
- Signals: `noteBitrate(aggregateBlockUploadBitrate)` fed from `file-uploader`'s
  `updateBlockUploadTransferredBytes` (real wire bytes, block-only — not the combined
  legacy+block figure). It is fed **only on a fresh throttled sample**, not on every
  progress tick: `updateBlockUploadTransferredBytes` checks whether
  `sampleBlockUploadBitrate` actually advanced `entry._bitrateTs` (it produces at most
  one reading per `BLOCK_BITRATE_SAMPLE_MS` = 500 ms) before calling `noteBitrate`, so a
  burst of rapid progress events cannot count as many healthy samples and ramp the
  ceiling up almost instantly. `noteFailure()` called by the orchestrator when a block upload
  attempt errors (stall/timeout/transport, not a user abort) — a strong "link
  unhealthy" signal that drops to 1. `reset()` returns to the conservative start (1).
- Lowering the ceiling never aborts in-flight blocks (we cannot un-send a block); it
  just stops NEW acquires until `inFlight` falls below the new ceiling. Legacy
  resumable adaptive concurrency remains untouched.
- Tests: `block-upload-limiter.test.js` adaptive-ramp suite (climb 1→max, drop on
  sustained collapse, `noteFailure`/`noteRetry` drop to 1, ignore idle); orchestrator
  "a failed block upload tells the limiter to back off"; `file-uploader.test.js`
  "feeds the global block aggregate into the adaptive limiter on transfer progress".
- Pending (next PRs): PR3.5 file-level serialization; PR4 single-source upload list +
  duplicate-name prompt for batch/large; broader browser E2E with the flag on (verify
  the ceiling ramps 1→`max` on a healthy link and backs off on a throttled one).

**PR3.5 — serialize block uploads to one active file at a time (done; behind flag).**
- The block (CAS) flow now runs **one file at a time**: a file-level FIFO
  (`this.blockUploadQueue` + `this.activeBlockUpload`) sits on top of the block-level
  limiter. A single large file gets the full adaptive block-concurrency ceiling while
  the others wait ("Waiting...", they render at progress 0), instead of several large
  files crawling in parallel. Rationale: clearer UX, fewer simultaneous CAS sessions,
  and fewer odd list states while the single-source `uploadEntries` map (PR4) is not in
  yet. The block-level limiter (PR2/PR3) is unchanged — it still bounds concurrency
  WITHIN the active file. Legacy resumable uploads are NOT serialized (untouched).
- `maybeBlockUpload` and the block retry paths (`retryBlockUpload`, `onUploadRetryAll`)
  now `enqueueBlockUpload(entry, file)` instead of calling `runBlockUpload` directly;
  `drainBlockUploadQueue` starts the next file only when none is active. `runBlockUpload`
  returns its settled promise so the queue advances on success, handled error, or cancel
  (it never rejects); `drainBlockUploadQueue` also guards a synchronous throw so a future
  bug cannot strand `activeBlockUpload` and wedge the queue.
- `enqueueBlockUpload` is **idempotent per entry (object identity)** — a double Retry /
  Retry-All cannot enqueue the same file twice and open two CAS sessions.
  `removeQueuedBlockUpload` also matches by identity (not `uniqueIdentifier`), so
  re-uploading the same filename in a later batch is never removed by accident.
- Cancellation: cancelling a queued file removes it (it never started); cancelling the
  active file aborts it via `entry.cancel()` and the queue advances when its promise
  settles. **Dialog close / cancel-all `clearBlockUploadQueue()` drops only the PENDING
  queue — it does NOT null `activeBlockUpload`.** The active file is being torn down
  (aborted) but its promise may not have reached the `finally` yet; nulling the marker
  early would let a freshly-added file start mid-teardown and break the
  one-active-at-a-time guarantee. The marker is released solely by the active job's own
  `finally`.
- Tests: `file-uploader.test.js` "block upload file-level queue (serialization)" — one
  active at a time + advance on settle; cancelling a queued file removes it; close keeps
  the active marker until the cancelled job settles; a new file added during the abort
  window waits; idempotent enqueue.
- Pending (next PR): PR4 duplicate-name prompt for batch/large; broader browser E2E
  with the flag on.

**PR4 — duplicate-name prompt for batch/large + apply-to-all (done; touches legacy).**
- The "Replace?" dialog now fires for **every** file (single / batch / large), BEFORE
  the block diversion, instead of only a lone legacy file. A same-named file inside a
  multi-file batch — or a large file routed to the block flow — used to silently land as
  `name (1).ext`; now it is offered Replace / Don't replace / Cancel.
- **Held duplicates are kept OUT of the rendered list** (`pendingDuplicates`), not just
  out of the resumable queue: `handleDuplicateFile` `removeFile`s the match and does NOT
  add it to `uploadFileList` until the user decides. So a held/undecided duplicate never
  shows as a stale "Waiting..." row, and a link-fetch failure that re-queues it leaves no
  orphan row (closes the duplicate-row / stale-Waiting bug and the resumable.files ↔
  block-entry mixing — held files touch neither representation until resolved).
- Decision routing (`applyDuplicateDecision`): a block-eligible file → a block entry with
  `_replace` persisted, run through the PR3.5 file-level queue (`enqueueBlockUpload`); a
  legacy file → `startLegacyDuplicateUpload` (replace = per-file update-link target +
  `target_file`; keep = the shared session target). `target_file` is built from a
  slash-terminated parent dir so a subfolder replace resolves to `/folder/name`.
- Multiple duplicates prompt **sequentially** with an **"Apply to all duplicate files"**
  checkbox (`duplicateBatchActive`) that drains the rest with one choice; the bulk choice
  is scoped to the current add batch (keyed on resumable's per-batch `files` array) so it
  never auto-resolves a duplicate added in a later batch. The dialog is keyed by file so a
  checked "apply to all" never leaks into the next prompt, and `getApplyToAll` only counts
  when the box is both shown and checked.
- **No target clearing reintroduced (Bug #4 stays fixed).** Both the normal add flow and
  the keep flow await one cached `ensureSharedUploadTarget()` (fetched once per batch,
  reset on idle) before `resumable.upload()` — so a resolved duplicate cannot kick the
  queue before the shared target is set (the `POST <page-url>` → 405 race). The target is
  **overwritten** with a fresh token per batch but **never cleared**, so a legacy retry
  still reuses the last token (the self-inflicted retry-405 is not reintroduced).
- Tests: `file-uploader.test.js` "duplicate-name prompting" — single/batch/large
  prompt, held-out-of-list, block-vs-legacy routing with `_replace`, keep via shared
  target, apply-to-all + batch scoping + reset, cancel drops the held file, subfolder
  `target_file`, the 405 "wait for shared target" regression, link-failure re-queue with
  no stale row, and a **flag-OFF** legacy path check.

**PR4 review hotfix (same branch).** Five issues found in review/testing:
- **Re-dropping a same-named file already uploaded/uploading in THIS session was not
  detected** (the server `direntList` prop had not refreshed), so it produced a second
  silent row ("Waiting..." next to "Uploaded") — the screenshot bug. `fileNameExistsInDir`
  now also matches a non-error `uploadFileList` entry (saved or uploading), so the re-drop
  is offered the Replace? prompt instead of silently duplicating. Test: "re-dropping a
  file already uploaded in THIS session prompts instead of silently duplicating".
- **[P1] "Don't replace" could inherit a stale update-link** from a previous Replace
  attempt on the same re-queued object and overwrite the file against the user's choice.
  `startLegacyDuplicateUpload` now `delete`s `opts.target` + `formData.target_file` at the
  start, before applying the new decision. Test: "a re-queued replace attempt does not leak
  its update-link into a later keep decision".
- **Replace no longer depends on the shared-target fetch.** A Replace carries its own
  per-file update-link target; gating it on `ensureSharedUploadTarget` meant a shared-link
  failure blocked a valid replace. Replace now starts directly after `getUpdateLink`; only
  "keep" awaits the shared target. Test: "a replace decision proceeds even when the
  shared-target fetch fails".
- **Cancelling the only held duplicate left an empty progress panel open.**
  `showNextDuplicatePrompt` now closes `isUploadProgressDialogShow` when the queue drains
  and nothing is uploading/uploaded. Test: "cancelling the only held duplicate closes the
  otherwise-empty progress dialog".
- **`showApplyToAll` wiring locked with tests:** the parent passes
  `showApplyToAll={duplicateBatchActive}` to the dialog, and the dialog renders the
  checkbox / `getApplyToAll` only when offered. Tests: `file-uploader.test.js` "the parent
  passes showApplyToAll…" + new `upload-remind-dialog.test.js`.

**PR4 review hotfix #2 (same branch).** Two more issues from a follow-up review:
- **[P1] Replace in a MIXED batch could re-open the 405.** Skipping the shared-target
  wait is safe for the replace file (it has its own update link) but `resumable.upload()`
  starts the WHOLE queue — so a normal/keep sibling still awaiting the shared target would
  POST to the empty target → 405. Replace now only skips the wait when
  `hasSharedTargetDependentLegacyFiles` is false (no other queued file lacks a per-file
  target); otherwise it awaits `ensureSharedUploadTarget` before starting. Tests: "replace
  in a mixed batch waits for the shared target…" + "replace with no shared-target-dependent
  siblings starts immediately".
- **[P2] Apply-to-all + Cancel could leave an empty progress panel** (the bulk branch of
  `resolveDuplicate` bypassed `showNextDuplicatePrompt`). It now closes
  `isUploadProgressDialogShow` after a bulk cancel when nothing is visible (replace/keep
  keep it open). Test: "apply-to-all Cancel closes both dialogs and leaves no empty
  progress panel".
- **Manual verification with the flag OFF still required before merge** (legacy:
  small files, folders, replace/keep/cancel, cancel-all/retry-all → identical behavior,
  no duplicate/stale rows, no 405), then flag-ON browser E2E.

**PR5 — target-mode scheduler for legacy uploads + synchronous dedup guard.**

> **Critical finding — `@seafile/resumablejs@1.1.16` ignores per-file `opts.target`.**
> Its `$h.getTarget` reads `$.getOpt('target')` where `$` is bound to the Resumable
> INSTANCE (not the chunk/file), and the chunk's real POST (`getTarget('upload', [])`)
> passes empty params — so even a function target gets no per-file context. Every chunk
> of every queued file therefore POSTs to the single instance-level
> `resumable.opts.target`. This invalidated the per-file-target assumption baked into
> every prior PR's replace flow ("file.opts wins in getOpt"): replace files setting
> `resumableFile.opts.target = updateLink` had NO effect — their chunks went to the
> shared upload-link. Replace was effectively broken for the real (un-mocked) path.

- **Target-mode scheduler.** Because only ONE instance target can be active, files
  needing different endpoints — `'upload'` (new + keep → upload-link) vs `'update'`
  (replace → update-link) — cannot run concurrently. `enqueueLegacyUpload(file, mode)`
  routes a prepared legacy file: if its mode matches the in-flight mode (or the queue is
  idle) it starts (`startLegacyFiles` sets the instance target for the mode, then
  `resumable.upload()`); if it conflicts, the file is **held** (`holdLegacyFile` pulls it
  out of `resumable.files` and renders it "Waiting…"). When the queue goes idle
  (`onComplete` / idle `onError` / `onCancel` → `onLegacyQueueIdle`) the next held
  same-mode group is started, switching the instance target. No-conflict batches
  (all-upload or all-update) never hit the hold path, so the common flow is unchanged.
  `startLegacyDuplicateUpload` is now just "prepare formData + `enqueueLegacyUpload`";
  replace uses a per-replace-group cached update-link (`ensureReplaceUpdateLink`).
  `mergeUploadFileList` unions the held files so they render. Replace now actually routes
  to the update endpoint (instance target), fixing the broken/`405` replace.
- **Synchronous dedup guard (`activeUploadNameKeys`).** `fileNameExistsInDir` reads the
  async `uploadFileList` and (for legacy) requires `isUploading()`, so a rapid SECOND
  `fileAdded` for the same destination slips past it before the first is visible —
  producing a duplicate row (the user's screenshot: "Waiting…" next to "Uploaded"). A
  `Set` of `repoID:path:relativePath` keys is updated SYNCHRONOUSLY the moment a file is
  committed (queued or held for a prompt); a second add is dropped at once ("This file is
  already queued"). Released on success (`markUploadSaved`), per-file cancel, bulk
  cancel/close, and a cancelled held duplicate — so a re-drop AFTER completion still gets
  the Replace? prompt. No `_ownsDestinationKey` flag needed: Option-A drop means only the
  original ever holds a key.
- Tests: `file-uploader.test.js` "target-mode scheduler + sync dedup guard" (rapid
  double-drop dropped; key released on save; apply-to-all Replace runs all via update-link
  with no hold; a normal file added during a replace is held "Waiting" then runs
  upload-link when idle) + the replace tests rewritten for the instance-target model (lone
  replace → update-link immediately; replace held behind an in-flight upload-link queue).
- Pending: manual flag-OFF verification (incl. a real replace POSTing to the update
  endpoint) + flag-ON browser E2E.

**PR5 review hotfix (same branch).** One screenshot bug + three review findings:
- **Large (block-flow) files that already exist in the folder were dropped from the list
  entirely** (screenshot: small files added, large ones vanished). Root cause: block
  entries are added with a FUNCTIONAL `setState(prev => …)`, but `renderLegacyList` /
  `setUploadFileList` rebuilt the list with a PLAIN `setState({ uploadFileList })` whose
  value was computed against stale `this.state`. Under React batching, a legacy render
  fired right after a block entry was added would OVERWRITE the list without the
  not-yet-committed block entry → the large file never appeared. Both now use a functional
  `setState` reading `prev.uploadFileList`, so block entries survive. (Sync-setState tests
  did not reproduce it; a new batched-setState test does.)
- **[P1] A target-fetch failure could wedge the scheduler / mix modes.**
  `startLegacyFiles`' catch only toasted: it left `activeLegacyMode` set, the failed files
  in `resumable.files`, and never drained held work — so a held replace could wait forever,
  and a later different-mode group would run with the failed files still mixed in (wrong
  endpoint). The catch now removes the failed files from `resumable.files`, frees the
  active mode, re-offers duplicate-prompt files (releases the key for plain files), and
  calls `onLegacyQueueIdle` to drain the next mode group.
- **[P2] `_replaceUpdateLinkPromise` was not cleared in `onComplete`** — a second replace
  after an idle batch (dialog left open) could reuse a stale update-link. Now cleared in
  `onComplete` (and still on close / cancel-all).
- **Cancel-all / close could start held work mid-teardown** (a resumable `cancel` event →
  `onCancel` → `onLegacyQueueIdle`). `onLegacyQueueIdle` is now a no-op while
  `this.resumable.isUploading()` OR a `_resettingUploads` flag is set; cancel-all/close set
  that flag and clear `legacyHold`/`activeLegacyMode` FIRST.
- Tests: "a legacy render preserves an in-flight block entry…", "a target-fetch failure
  frees the active mode and drains the next held group", "onComplete clears the cached
  update-link…", "onLegacyQueueIdle does not start held work while a reset is in progress".

**Deferred frontend cleanups (reviewed, non-blocking).**
- **Abandoned normal file on target-fetch failure is not a retryable row.** When
  `ensureSharedUploadTarget` fails for a plain (non-duplicate) file, `startLegacyFiles`'
  catch removes it and surfaces a toast — clear feedback, and strictly better than the old
  `main` behavior (a file stuck on "Preparing…" forever with no retry). A nicer UX would
  move it to the retry list, but the retry path (`retryUploadWithFreshLink`) assumes a file
  that already started and errored, so wiring a pre-start failure into it needs care. Rare
  (a network blip on the session-link fetch); deferred, not a blocker.
- **A manual retry of a held replace after a mode switch can route to the wrong instance
  target.** `retryUploadWithFreshLink` (and `retryUploadFile`) re-drive a file via
  `resumableObj.upload()` reusing whatever `resumable.opts.target` is CURRENTLY set — it
  does NOT re-enter the target-mode scheduler. So in the narrow case where a replace
  (`'update'`) file errors and lands in the retry list, the queue then goes idle and
  `onLegacyQueueIdle` switches the instance target to a held `'upload'` group, a later
  **manual** retry of that replace would POST to the upload-link → it would auto-rename
  ("name (1).ext") instead of overwriting, and momentarily mix modes against the running
  group. Requires the exact combination: mixed replace+normal in one session **+** the
  replace errors **+** the user clicks retry **after** the mode switched. The common
  auto-retry path (409 conflict) is unaffected: it fires from `onFileError` before any
  mode switch, while the instance target is still the update-link. Strictly better than
  `main` (where replace via per-file target never worked at all), same class as the items
  above; the clean fix is to route retries back through `enqueueLegacyUpload(file, mode)`
  using the file's `_uploadMode`, so the scheduler re-establishes the correct target.
  Deferred, not a blocker.
- **`mergeUploadFileList` is still a bridge, not a single-source `uploadEntries` Map.** The
  functional duplicate-row / disappearing-block-entry bugs are covered by the synchronous
  `activeUploadNameKeys` guard + the functional-`setState` batching fixes; a full Map keyed
  by `uniqueIdentifier` (the originally-sketched single-source list) remains an architecture
  cleanup, not a bug fix. Deferred.
- **[RESOLVED — PR6] Finer per-phase labels for block uploads + commit-phase pipelining.**
  The orchestrator now emits a distinct **`checking`** phase before `/blocks/check`
  (`emitPhase('checking')`), so `_phase` is `hashing → checking → uploading → saving → done`.
  `upload-list-item` renders each step from `_phase` (`blockProgressText`): `Hashing… /
  Checking… / Uploading… X% / Saving…` (percent is the overall bar, hashing being its first
  half). **Pipelining:** the file-level FIFO no longer holds the slot through the commit —
  `setBlockUploadPhase` calls `releaseActiveBlockSlotForCommit` the moment a file enters
  `saving`, handing the slot to the next queued block file so it starts uploading while the
  first commits (the block limiter still caps total blocks on the wire). Idempotent per job
  (`_committing`) so the `needs_upload` re-entry into `saving` cannot double-release or
  strand the queue. Mirrors the legacy finalize-slot handoff. Tests: orchestrator phase
  order + `file-uploader.test.js` "releases the slot at saving" / "needs_upload re-entry"
  + `upload-list-item.test.js` phase labels.
- **[RESOLVED — PR6] Deduplicated bytes surfaced.** The orchestrator reports a dedup plan
  from the AUTHORITATIVE missing set after `/blocks/check` (`onPlan({ totalBytes, uploadBytes,
  dedupedBytes })`), computed from each unique missing block's real size (not wire bytes,
  which include retries). The row shows `N M deduplicated` (`dedupNote`) during the upload
  and on the completed row, so a fast repeat upload is explained. "Deduplicated" (not
  "already on server") because the saving covers BOTH blocks already on the server AND
  blocks repeated within the same file. Tests: orchestrator `onPlan` (mixed + all-missing)
  + `upload-list-item.test.js` dedup note.
- **[RESOLVED — PR6] Queued block files render "Waiting…", not "Hashing…".** The block
  FIFO runs one file at a time; the rest wait. The entry phase now starts `'queued'`
  (createBlockUploadEntry / prepareBlockUploadRetry) and `runBlockUpload` flips it to
  `'hashing'` only when the file actually starts, so a queued row no longer shows the
  active file's "Hashing…". Test: `upload-list-item.test.js` "queued → Waiting" +
  `file-uploader.test.js` "freshly created block entry starts queued".

## Known issues / deferred debts (tracked — gated by the flag being OFF in prod)

None of these block merging the branch: the flow ships **disabled** in every prod
config (`enable_web_block_upload: false`); they are the checklist to clear before
flipping the flag on in production. Severity is the operator-facing risk *if the
flag were on*.

1. **[Med/high] No staging-bytes cap.** Session staging skips the logical storage
   quota (R5) and nothing caps *uncommitted* staged bytes, so an authenticated
   user can `PutBlockData` many new blocks and never commit. Bounded by the upload
   **traffic quota** (per-block, hard-blocks free/hard tier) and the **48h
   provisional-ref TTL + GC**; the gap is real only for **soft-tier or disabled
   traffic quota** (≤48h of transient backend bytes). Fix before prod: a dedicated
   per-user/org/session counter of materialized-but-uncommitted bytes, decremented
   at commit/expiry, checked at `/blocks/upload` — a *separate* limit from the
   final logical quota so it cannot reintroduce the same-size-overwrite rejection.
   (Subsumes the "abandoned staged blocks live until TTL" observation.)
2. **[Med, mitigated] Idempotency result can still be lost on a sustained
   Cassandra outage.** After publish, `MarkBlockUploadSessionCommitted` is now
   retried (3× bounded backoff). If *all* retries fail the session is left
   `committed=true` without a result, so retries get `409 "commit still in
   progress"` until the TTL — the file is published, **no duplicate, no
   corruption** (the LWT prevents re-finalize). The residual is a pathological
   outage window. Full fix (deferred): reconstruct the result by re-reading the
   published path on a committed-but-resultless session, instead of waiting for TTL.
3. **[Low/med] `/blocks/check?session=` is optimistic vs the commit.** Check marks
   a block `existing` from `ProbeBlockReuse` (DB liveness) only; the commit
   additionally verifies physical S3 presence (`CheckBlocksParallel`) and
   ownership/permanence (`classifyBlockForCommit`). So check can say `existing`
   while the commit returns `needs_upload` (e.g. metadata present but the S3 object
   was deleted). **Not a correctness bug** — the commit is the authority and the
   client re-uploads — just a possible extra round-trip. Optional follow-up: align
   `check?session=` with the commit's classifier.
4. **[Med] No broader real browser E2E yet.** Backend is covered
   live; still missing in-browser validation of: large files, real retry,
   cancellation, resume, progress accuracy, fallback, legacy folder upload, and
   local-vs-downloaded hash comparison. The Seafile desktop-client regression is
   already validated end-to-end on this branch; what remains here is broader
   browser-side coverage and UX verification.
5. **[Med] No cross-method dedup (web ↔ desktop).** Web uses fixed 8 MB SHA-256
   blocks; desktop/sync uses FastCDC variable blocks (SHA-1 external IDs) — same
   file hashes differently, so it is stored twice. Out of phase 1; closing it needs
   FastCDC + SHA-1 aliasing in the browser. See
   [CHUNKING-ANALYSIS.md](./CHUNKING-ANALYSIS.md) and the limitations section above.
6. **[Med, mostly addressed] Frontend phase/progress/throughput UX.** The block flow
   is mapped onto the legacy resumable.js dialog. **Done (PR6):** explicit per-phase
   labels (`Hashing… / Checking… / Uploading… X% / Saving…`), commit-phase pipelining
   (the next file uploads while the previous commits), and dedup observability
   (`N M deduplicated`). Real block-upload throughput already replaced the legacy
   `0.00 B/s`. **Remaining:** fuller integration with the global progress aggregator and
   any further polish before flag-on.
7. **[Resolved] Concurrent-loser `409 "commit still in progress"` retry.** The LWT
   guarantees a single winner; losing/retried commits poll only ~10s for the
   winner's `ResultFilename`, after which the server returns
   `409 "commit still in progress; retry"`. The frontend now handles this:
   `commitFromManifest` in
   [block-upload-orchestrator.js](../frontend/src/components/file-uploader/block-upload-orchestrator.js)
   retries `createFileFromBlocks` with bounded exponential backoff (default 6
   attempts) only for the explicit retryable conflict
   (`code: "commit_in_progress"`, with exact-message fallback for older servers),
   until the idempotent result is returned. Permanent 409s (different file,
   parent_dir mismatch, encrypted) are not retried; the backend now emits a
   distinct permanent code for the "different file" case, so the old ambiguous
   `"different file or commit is still in progress"` branch is gone (covered by
   Go + Jest tests). The `needs_upload` re-upload path is unchanged.
8. **[Med] Commit latency/observability at large block counts.** `file-from-blocks`
   is not a cheap metadata flip: on large files it verifies every distinct block,
   checks logical quota, does the HEAD CAS, promotes provisional→permanent refs,
   and cleans up staging. The UX doc already notes the visible `Saving…` phase;
   before prod flag-on we still need operator-facing observability for this path
   (commit latency, retries, and timeout/error rates at high block counts).
9. **[Low/med] `session` travels in the query string.** `/blocks/check?session=`
   and `/blocks/upload?session=` carry the session id as a query parameter, which
   can land in access logs. It is not a strong secret leak (the request is already
   authenticated and scoped to the caller), but a request header
   (`X-Block-Upload-Session: <id>`) would keep it out of logs. Cheap follow-up.
10. **[Low/med] No local manifest persistence (resume survives the server, not a
    reload).** Real resume lives server-side via `/blocks/check`, but the client
    does not cache the computed manifest (e.g. in IndexedDB). After a page reload
    the browser must re-hash the whole file before it can ask `/blocks/check` which
    blocks are still missing. The network resume is preserved; the local hashing
    work is not.

## Remaining work

- Clear the prod-readiness checklist above (items 1, 2, 4 in particular) before
  enabling `enable_web_block_upload` in production.
- **"metadata present but S3 object deleted"** is handled (commit verifies physical
  presence via `CheckBlocksParallel` → `needs_upload`); an automated test for it
  needs direct MinIO object deletion and is not yet added.


