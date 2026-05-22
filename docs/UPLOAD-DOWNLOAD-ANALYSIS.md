# Upload & Download — Preproduction Assessment

**Date:** 2026-05-22
**Diagrams:** [Upload/Download Flow Diagrams](./diagrams/upload-download-flow.md)

---

## Current findings

### HIGH: `upload-link` vs `update-link` still collapses replace semantics

**Files:** `internal/api/v2/files.go`, `internal/api/seafhttp.go`

The desktop client and the web UI both distinguish “replace” vs “don't replace” at
the UX layer, but both flows still collapse into the same upload-token semantics on
the backend. `upload-link` and `update-link` still produce equivalent tokens, and the
upload handler defaults to overwrite behavior.

**Impact:** A user can explicitly choose not to replace an existing file and still end up
overwriting it. This is a correctness bug, not just missing UX polish.

**Fix:** Split token semantics so `update-link` carries `Replace=true` and `upload-link`
defaults to auto-rename / no-replace, then plumb that flag through `HandleUpload`.

---

### MEDIUM: Permission not rechecked during chunked upload streaming

**File:** `internal/api/seafhttp.go` (`HandleUpload`)

The upload token and current permission are validated before the upload starts, but a
long-running chunked upload is not rechecked chunk-by-chunk. If a user's write access is
revoked mid-upload, the in-flight upload can still finish within the token TTL window.

**Risk:** Low in practice, but the action is effectively authorized at chunk-session start
instead of at every write/finalize boundary.

**Tested:** No focused revoke-during-upload integration test exists yet.

---

### MEDIUM: Dedup still does not save upload bandwidth in the web UI

**File:** `internal/api/seafhttp.go` (web upload path)

The web uploader still sends the full file bytes even when every block already exists in
storage. Dedup saves storage writes, but it does not avoid network transfer the way the
desktop sync protocol can.

**Impact:** Re-uploads of large files waste user bandwidth and time.

**Fix:** A future browser-side block API flow (`check-blocks` + upload missing blocks +
commit manifest) is the real solution. This is larger than a small backend patch.

---

### MEDIUM: Encrypted downloads still omit `Content-Length`

**Files:** `internal/api/seafhttp.go`, `internal/api/v2/fileview.go`, `internal/api/v2/sharelink_view.go`

Encrypted download paths stream the decrypted payload correctly now, but they still omit
`Content-Length` to avoid lying about ciphertext size.

**Impact:** Browsers and clients cannot show accurate progress bars for large encrypted
downloads.

**Fix:** Use the plaintext size already stored in `fs_objects.size_bytes` where the route
can safely set a decrypted content length.

---

### MEDIUM: Chunked upload traffic accounting is completion-based

**File:** `internal/api/seafhttp.go` (`HandleUpload`)

Chunked uploads now pre-check traffic against the declared `Content-Range` total, but
traffic is still recorded only after `finalizeUploadStreaming()` succeeds.

**Impact:** Abandoned uploads and finalize failures can consume real bandwidth without
advancing `traffic_period_usage`.

**Status:** Accepted debt for now; paid upload headroom is generous and the earlier
fail-open pre-check bug is already closed.

---

### LOW: Block integrity is still not re-verified on download

**File:** `internal/storage/blocks.go` (`GetBlock`)

Blocks are hashed on upload, but downloads still trust the backing store and stream the
returned bytes without re-hashing.

**Impact:** Silent corruption is unlikely because S3/MinIO already provide their own
integrity guarantees, but the application layer has no defense-in-depth check.

**Fix:** Optional hash verification on read, or an explicit integrity mode for high-value
download paths.

---

## Recently closed findings

### CLOSED: Abandoned chunk temp files are already cleaned by the janitor

The original “zombie temp files” finding was stale. The current chunk manager already has
both an in-memory tracker sweep and an orphaned-temp-file disk sweep, and that behavior is
covered by janitor tests in `internal/api/seafhttp_test.go`.

**Outcome:** This is no longer a live backend issue and should not be treated as an open
upload risk.

---

### CLOSED: Encrypted upload/download round-trip now has end-to-end coverage, and it caught a real bug

A new integration test now creates an encrypted library, unlocks it, uploads a file, and
downloads it back with content verification.

That test immediately exposed a real mismatch: encrypted uploads used
`EncryptBlockSeafile`, while several read paths still used the legacy `DecryptBlock`
format. The fix now propagates the file IV through the shared readers and decrypts via the
Seafile-compatible path.

**Covered surfaces fixed together:**
- `seafhttp` download and ZIP streaming
- shared streaming helpers (`StreamBlocks`, `BlockReadSeeker`)
- raw file / historic file / share-link readers that reuse those helpers

---

### CLOSED: Chunked/resumable upload now has end-to-end coverage

There is now an integration test that sends a file in multiple `Content-Range` requests,
verifies assembly, and downloads the result back.

The handler was also tightened so repeated chunk requests stop re-running the same visible
tree storage-quota precheck on every chunk. The initial precheck is cached per upload
tracker, while finalization still revalidates against the current HEAD before publish.

---

## Test coverage

### Existing tests: 35

| What's tested | Count | Type |
|---------------|-------|------|
| Upload/download round-trip (content verified) | 3 | Integration |
| Upload overwrite, URL format, region pinning | 4 | Integration |
| Permission enforcement (readonly, guest blocked) | 2 | Integration |
| v2 direct upload | 1 | Integration |
| File CRUD + batch operations | 7 | Integration |
| History download (round-trip + errors) | 5 | Integration |
| ZIP download (region-pinned) | 1 | Integration |
| ZIP traversal limits (depth/count/bytes) | 3 | Unit |
| Chunked upload tracker regression coverage | 1 | Unit |
| Token lifecycle (create/get/expire/delete) | 8 | Unit |

### Critical gaps: 5

| Gap | Risk | What to add |
|-----|------|-------------|
| Large file (>100 MB) | Medium | Upload 100MB+ file, verify block count and download |
| Concurrent upload to same path | Medium | Two clients upload simultaneously → verify no corruption |
| Quota exhaustion behavior | Medium | Fill quota → verify upload rejected with correct error |
| Expired token during download | Medium | Start download, expire token, verify behavior |
| Range request (video seek) | Medium | Upload video → Range request → verify partial content |

### How to run

```bash
# All upload/download integration tests
go test -count=1 -tags integration -v -run "TestUpload\|TestDownload\|TestFile\|TestHistory\|TestZip" \
    -timeout 5m ./internal/integration/...

# Unit tests only
go test -v -run "TestToken\|TestZipTraversal" ./internal/api/...
```

---

## Best practices check

| Practice | Status |
|----------|--------|
| Auth checked before any I/O | Yes (token validated first) |
| Quota checked before reading body | Yes (traffic fail-fast; chunked storage precheck cached per upload tracker, then revalidated at finalize) |
| File size limited | Only by quota (no hard max) |
| Temp files in secure location | Yes (/tmp, 0600 perms) |
| Temp files cleaned on success | Yes |
| Temp files cleaned on failure | Yes (tracker janitor + orphan disk sweep) |
| Traffic recorded accurately | Yes (fire-and-forget) |
| Streaming uses bounded memory | Yes (4MB buffer pool) |
| Encrypted blocks never written to disk | Yes (memory only) |
| Final publish revalidates current HEAD | Yes |
| Download tokens are single-use | No (reusable within TTL — acceptable) |
