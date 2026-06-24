# Resumable uploads (`file-uploaded-bytes`) — analysis & limitations

**Date:** 2026-06-19
**Area:** `internal/api/v2/files.go` (`GetFileUploadedBytes`), `internal/api/seafhttp.go` (`ChunkManager` / `HandleUpload`), `frontend/src/components/file-uploader/file-uploader.js`
**Status:** The endpoint is a safe stub today (`uploadedBytes: 0`). Real resume is **not** wired up. This doc records why, what the safe path is, and what the prerequisites are — so the next person does not "just return a number" and ship silent corruption.

> **UPDATE (2026-06-22):** Option B (the block-check API) is now implemented for
> the authenticated web uploader (phase 1, non-encrypted libraries), feature-
> flagged off by default. Resume and network dedup are provided by
> `/blocks/check` + commit-from-manifest, not by `file-uploaded-bytes` (which
> stays a safe `0`). See [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md).
>
> **UPDATE (2026-06-24):** A follow-up hotfix made the web block flow's committed
> files compatible with the Seafile desktop/mobile sync client. The file object
> now stores 40-hex **SHA-1** block IDs (with a server-verified `sha1 → sha256`
> forward mapping), instead of the 64-hex SHA-256 IDs the first cut wrote — which
> the desktop parser rejected with `File <fs_id> does not exist`. Internal storage,
> refs, GC and dedup stay on SHA-256; legacy/seafhttp mapping is untouched. Verified
> end-to-end (desktop sync + web download, incl. the ZIP/MOV cases that originally
> failed). See [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md).

---

## TL;DR

`GET /api/v2.1/repos/:repo_id/file-uploaded-bytes/` always returns `{"uploadedBytes": 0}`
([`files.go` `GetFileUploadedBytes`](../internal/api/v2/files.go)). That is **not laziness — it is the only safe value** given how the rest of the upload path is currently built. Returning a wrong non-zero value causes **silent file corruption** (truncated/garbled files with no error), because the client trusts it to *skip* chunks it believes are already persisted.

Real resume is a worthwhile improvement, but it is **not a small flip**. It needs architectural prerequisites (below). The genuinely valuable long-term shape is the **block-check API**, which subsumes resume *and* dedup *and* upload-bandwidth savings.

---

## How the flow works today

### Frontend (web uploader, `resumable.js`)

`frontend/src/components/file-uploader/file-uploader.js`:

- Before starting an upload (and on retry) it calls
  `seafileAPI.getFileUploadedBytes(repoID, path, fileName)`.
- It then computes `offset = Math.floor(uploadedBytes / blockSize)` and calls
  `resumableFile.markChunksCompleted(offset)` — i.e. it **skips** that many leading
  chunks and only uploads the rest.
- With the current backend returning `0`, `offset` is always `0`, so every upload
  starts fresh. Correct, just not resumable.

The **danger**: if the backend ever returns a non-zero `uploadedBytes` that does not
correspond to bytes actually persisted *for this exact file*, `markChunksCompleted`
skips real data and the finalized file is corrupt. The share-link / upload-link
uploaders already hard-code `markChunksCompleted(0)` precisely because they have no
auth token to call the endpoint
(`frontend/src/components/shared-link-file-uploader/file-uploader.js`,
`frontend/src/pages/upload-link/file-uploader.js`).

### Backend chunk tracking (the part that already works)

`internal/api/seafhttp.go` `HandleUpload` + `ChunkManager`:

- Chunked uploads arrive as `Content-Range: bytes start-end/total` requests.
- Each in-flight upload has a `ChunkUpload` tracker holding a temp file and a set of
  **merged received byte ranges** (`markRangeReceivedLocked`). The contiguous prefix
  from byte 0 is therefore cheaply computable: if `Ranges[0].Start == 0`, the resumable
  offset is `Ranges[0].End + 1`.
- So the **hard part (knowing how many contiguous bytes landed) already exists.** What
  is missing is *addressing* — being able to find the right tracker from the
  `file-uploaded-bytes` request.

---

## Why it cannot be turned on as-is — the blockers

### 1. Trackers are in-memory, and the deployment is multi-node (highest)

`ChunkManager` is a single in-process map (`var chunkManager = NewChunkManager()`),
and the temp file lives on the **local disk of one node**. The compose topology runs
multiple backends (`sesamefs`, `sesamefs-node-2`, `sesamefs-node-3`). A
`file-uploaded-bytes` probe (or a resumed chunk) can land on a different node than the
one holding the partial bytes. There is no shared progress state and no sticky-by-token
routing in nginx. Result: the probe sees `0` (or worse, a different node's unrelated
state).

### 2. The endpoint contract is too thin to locate a tracker

Trackers are keyed by `(token, resumableIdentifier, parentDir, filename, totalSize)`
(`chunkUploadTrackerKey`). But `GetFileUploadedBytes` only receives `repo_id`,
`parent_dir`, `file_name`. It has **no token, no identifier, no totalSize** — it
literally cannot compute the key, so it cannot find the tracker even on the same node.

### 3. The resumable identifier is not stable across sessions

`generateUniqueIdentifier` uses `MD5(relativePath + new Date())`
(`file-uploader.js`). It changes on every page load. The real resume case — you closed
the tab / lost the network and come back — can therefore **never match** a prior
tracker by identifier. And the temp file path is derived from a hash of the tracker key
(token+identifier), so there is no stable `(repo, dir, filename)` lookup on disk either.

### 4. Half-correct is worse than off

Because of (1)–(3), any quick attempt to return a non-zero value risks returning bytes
that belong to a *different* upload (same name/dir, different content) or to a
stale/partial tracker → `markChunksCompleted` skips real chunks → **silent
truncation/corruption with no error surfaced to the user.** The current `0` is the safe
degenerate.

### 5. Only the authenticated web uploader is even in scope

Public share/upload-link uploads can't call this endpoint (no auth token), so they
already restart from `0`. Any resume work only benefits the logged-in web uploader
unless a separate signed offset-probe is designed for links (tracked in
`docs/TECHNICAL-DEBT.md` §15).

---

## Options (smallest-safe → strategic)

### A. In-session retry resume (smallest correct scope)

Make a *failed file within the same page session* resume instead of restarting:

- Plumb `token`, `resumableIdentifier`, and `totalSize` into the `file-uploaded-bytes`
  request so the endpoint can compute the tracker key and return the contiguous prefix
  (`Ranges[0].End + 1`, guarded to `0` unless `Ranges[0].Start == 0`).
- Make the resumable identifier **content-based and stable** (e.g. size + relative path,
  no `new Date()`), so it survives a retry/reload.
- Pin all chunks + the probe for one upload token to one node (sticky-by-token at nginx)
  so the in-memory tracker is always reachable.

Win: retries of a large in-flight file don't re-upload from zero. Limit: does **not**
cover cross-node / cross-restart resume.

### B. Block-check API (the real destination)

Client hashes blocks → `POST /api/v2/blocks/check` returns which blocks are missing →
client uploads only missing blocks → a final commit assembles the file from the block
manifest. This is the Seafile-style content-addressed flow and is already flagged in
`docs/TECHNICAL-DEBT.md` (client-side hashing / `blocks/check` / commit-from-manifest)
and `docs/UPLOAD-DOWNLOAD-ANALYSIS.md` (the "dedup doesn't save upload bandwidth"
finding). It **subsumes** resume (missing-block set is the resume state), dedup, and
upload-bandwidth savings, and it is naturally multi-node because block existence is
queried from shared storage/metadata, not an in-process map. Cost: a real
multi-jornada build (client hashing, the check endpoint, the manifest-commit path, and
UI wiring).

### Recommendation

If we invest in uploads, **B** is the correct target because it collapses three open
items into one coherent design. If a quick, shippable win is wanted first, **A** (or
the unrelated quick wins below) is fine, but A's value is bounded by the sticky-routing
requirement.

---

## Related open upload items (for ordering)

- **MEDIUM** — dedup does not save *upload bandwidth* in the web UI (the block-check API
  is the fix; converges with Option B). `docs/UPLOAD-DOWNLOAD-ANALYSIS.md`.
- **MEDIUM** — permission not re-checked during long chunked uploads (authorized at
  chunk-session start, not per write/finalize). `docs/UPLOAD-DOWNLOAD-ANALYSIS.md`.
- **MEDIUM** — encrypted downloads omit `Content-Length` (no accurate progress bars).
- **MEDIUM** — chunked upload traffic accounting is completion-based (abandoned uploads
  can consume bandwidth without advancing usage). `docs/TECHNICAL-DEBT.md`.
- **§15** — SeafHTTP upload-token contract & resumability for public links is still
  undefined (TTL, retries, cleanup, resume). `docs/TECHNICAL-DEBT.md`.

## Already closed (verified 2026-06-19)

- `upload-link` vs `update-link` replace semantics are split end-to-end and covered by
  four integration tests. See the CLOSED entry in
  `docs/UPLOAD-DOWNLOAD-ANALYSIS.md`. (The earlier HIGH finding was stale.)
