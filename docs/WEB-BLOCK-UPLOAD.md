# Web content-addressed (block) upload flow

**Date:** 2026-06-22
**Status:** Phase 1 implemented (backend + frontend) + post-review hardening.
Gated OFF by default by a **server-side** flag (`web_uploads.enable_web_block_upload`
/ `WEB_UPLOADS_ENABLE_BLOCK_UPLOAD`); the frontend flag (`enableBlockUpload`) is
driven from it via bootstrap. Encrypted libraries and public share/upload links
are out of scope for phase 1.

### Feature flag (server-authoritative)

- Config: `web_uploads.enable_web_block_upload` (default `false`) in all
  `configs/*.yaml`; env override `WEB_UPLOADS_ENABLE_BLOCK_UPLOAD=true`.
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
2. client splits file into fixed 8 MB blocks, SHA-256 each (Web Worker)
3. POST /api/v2/blocks/check?session=...               → { existing, missing }
4. POST /api/v2/blocks/upload?session=... (per missing) → store + materialize
5. POST /api/v2/repos/:repo_id/file-from-blocks/        → commit from manifest
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
  runs finalize and the rest wait for and return the same result. A failed
  finalize releases the claim so the client can retry.
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
- **R10 — No SHA-1 mapping for pure SHA-256.** The web flow uses SHA-256 as the
  external block ID (`external == internal`), so `RegisterUploadedBlockAndMapping`
  is called with `externalBlockID == ""` and writes **no** `block_id_mappings`
  row. `streaming.BatchResolveBlockIDs` leaves 64-char IDs untouched, so download
  resolves them directly. SHA-1 stays only at the Seafile (desktop) edge.
- **R11 — Sizes validated against real metadata.** The commit checks
  `manifest.size == ProbeBlockReuse.SizeBytes` per block; a manifest cannot
  declare a size that disagrees with the stored block.

---

## Out-of-branch debts and limitations (do not lose this knowledge)

- **Legacy `/blocks/upload` without a session does not materialize metadata.** It
  is used by desktop/mobile via their own commit paths; for any *new* caller,
  uploading without a session leaves an ungoverned S3 object. The web flow always
  passes a session. If another non-sync caller is added, it must either use a
  session or its own materialization + cleanup.
- **No cross-method dedup (web ↔ desktop).** Web uses fixed 8 MB blocks
  (SHA-256 external IDs); desktop uses FastCDC variable blocks (SHA-1 external
  IDs). Same file → different boundaries → different hashes → stored twice. See
  [CHUNKING-ANALYSIS.md](./CHUNKING-ANALYSIS.md). Closing this would require
  FastCDC in the browser/worker plus SHA-1 aliasing — a separate phase.
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
- R4: rename + re-download + history over a SHA-256-`block_ids` file

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
- **Aggregated concurrency of multiple large files is unbounded across files.**
  Each large file spawns its own orchestrator with internal block concurrency, so
  `N` large files in one batch run `N × M` concurrent block operations
  (hashing/network/backend). There is no cross-file ceiling yet.
- **Cancel during `Saving…`/commit is undefined.** Cancel aborts the in-flight
  client requests, but its effect once the commit has started is not specified
  (whether the commit is interrupted, whether provisional refs linger until TTL,
  whether the same session can be retried). With a long `Saving…` phase this is no
  longer a rare case.

These are acceptable for a flag-gated rollout and are the polish items for a
follow-up. **Still pending: browser end-to-end validation** (`docker compose up`
+ `npm start`, enable `enableBlockUpload`).

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
4. **[Med] No real browser E2E yet.** Backend is covered live; still missing
   in-browser validation of: large files, real retry, cancellation, resume,
   progress accuracy, fallback, legacy folder upload, and local-vs-downloaded hash
   comparison. Run with `docker compose up` + `npm start`, `enableBlockUpload` on.
5. **[Med] No cross-method dedup (web ↔ desktop).** Web uses fixed 8 MB SHA-256
   blocks; desktop/sync uses FastCDC variable blocks (SHA-1 external IDs) — same
   file hashes differently, so it is stored twice. Out of phase 1; closing it needs
   FastCDC + SHA-1 aliasing in the browser. See
   [CHUNKING-ANALYSIS.md](./CHUNKING-ANALYSIS.md) and the limitations section above.
6. **[Med] Frontend phase/progress/throughput UX.** The block flow is mapped onto
   the legacy resumable.js dialog, which loses per-phase state, shows `0.00 B/s`,
   surfaces `Saving…` early and without its own progress, has no dedup
   observability, and is not fully integrated with the global aggregator. Full
   detail in *Frontend UX gaps* above — the main polish cluster before flag-on.
7. **[Low/med] `session` travels in the query string.** `/blocks/check?session=`
   and `/blocks/upload?session=` carry the session id as a query parameter, which
   can land in access logs. It is not a strong secret leak (the request is already
   authenticated and scoped to the caller), but a request header
   (`X-Block-Upload-Session: <id>`) would keep it out of logs. Cheap follow-up.
8. **[Low/med] No local manifest persistence (resume survives the server, not a
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


