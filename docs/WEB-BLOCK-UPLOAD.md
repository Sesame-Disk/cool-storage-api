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
  `/blocks/upload`; a per-block physical admission check guards storage; the
  **logical** repo/user storage quota is decided once at commit from the file
  delta (`currentUploadStorageDelta`), never per block. No double counting.
- **R6 — Manifest validation.** Fixed 8 MB blocks except the last (`0 < last ≤ 8 MB`),
  `sum(sizes) == size`, 64-hex hashes, bounded block count/total size, repeated
  blocks allowed.
- **R7 — Server-issued, idempotent sessions.** `session_id` is a random 256-bit
  server-minted token bound to `(org, user, repo)` with a TTL.
  `file-from-blocks` is idempotent per `session + manifest_digest`: a retried
  commit returns the original result instead of auto-renaming a duplicate.
  **Concurrency-safe:** the commit is claimed via a Cassandra LWT
  (`ClaimBlockUploadSessionForCommit`), so exactly one of N concurrent commits
  runs finalize and the rest wait for and return the same result. A failed
  finalize releases the claim so the client can retry.
- **R8 — Commit accepts permanent-reusable OR session-owned blocks.** Ownership
  is encoded in the provisional referrer `up:<session_id>`; the commit also
  accepts blocks kept alive by a permanent `fs:`/`pub:` reference (legitimate
  cross-file dedup hits the client skipped). The publish-attempt staging pins
  every block under the commit *before* the HEAD CAS, so a concurrent rollback
  of another session's provisional ref cannot drop liveness for this file.
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

---

## Validation status

**Backend: validated live** (`internal/integration/web_block_upload_test.go`,
9/9 passing against real Cassandra + MinIO via `docker compose`):

- round-trip upload + download; dedup (re-check reports existing)
- multi-block ordering + download (8 MB block + tail)
- R1: manifest block never uploaded → `needs_upload`
- R1/R3: S3-only block (legacy upload, no metadata) → reported missing + commit refuses
- R6: manifest sum/size mismatch → 400
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
orchestrator, 17 tests) + eslint + production build.

**v1 limitations (behind flag, documented):**
- The "replace existing file" dialog is bypassed for block-flow files; they
  auto-rename on conflict (`replace:false`).
- Upload bitrate remains approximate for block-flow files because they do not
  contribute bytes to `this.resumable.files`; total progress/cancel/retry are
  now wired through the existing dialog.

These are acceptable for a flag-gated rollout and are the polish items for a
follow-up. **Still pending: browser end-to-end validation** (`docker compose up`
+ `npm start`, enable `enableBlockUpload`).

## Remaining work

- Browser E2E of the wired flow (above).
- **"metadata present but S3 object deleted"** is handled (commit verifies physical
  presence via `CheckBlocksParallel` → `needs_upload`); an automated test for it
  needs direct MinIO object deletion and is not yet added.


