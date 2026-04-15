# Upload & Download — Preproduction Assessment

**Date:** 2026-04-14
**Diagrams:** [Upload/Download Flow Diagrams](./diagrams/upload-download-flow.md)

---

## Issues found

### HIGH: Zombie temp files from abandoned chunked uploads

**File:** `internal/api/seafhttp.go` (HandleUpload, chunked path)

When a client starts a chunked upload (Content-Range) and disconnects, the temp file
(`/tmp/sesamefs_upload_{token}_{filename}`) is left on disk. There is no cleanup job.
The upload token expires in 1 hour, but the temp file persists indefinitely.

**Impact:** Disk exhaustion on servers with high upload traffic. A malicious client could
intentionally start many chunked uploads without completing them.

**Fix:** Add a periodic sweeper that deletes temp files older than the token TTL (1h).
Alternatively, use `os.CreateTemp` with a prefix and sweep by `mtime`.

**Tested:** No. No test verifies temp file cleanup on failure or abandonment.

---

### HIGH: No integration test for encrypted upload/download

There is no test that uploads a file to an encrypted library and downloads it back with
content verification. The encryption path (`EncryptBlockSeafile` on upload,
`DecryptBlock` on download) is only tested in unit tests with mock data.

**Risk:** A regression in the encrypt→store→fetch→decrypt pipeline would not be caught
until a customer reports corrupted files.

**Fix:** Add integration test: create encrypted library → set password → upload file →
download → verify SHA-256 matches original content.

---

### MEDIUM: Permission not rechecked during chunked upload streaming

**File:** `internal/api/seafhttp.go:626`

The upload token is validated once at the start. If a user's permission is revoked
while a large chunked upload is in progress (which can take minutes for multi-GB files),
the upload completes successfully. The token has a 1-hour TTL.

**Risk:** Low in practice (permission revocation during active upload is rare), but
violates the principle that access checks happen at the time of action.

**Tested:** No.

---

### MEDIUM: No test for resumable/chunked upload

No integration test sends Content-Range headers to simulate a multi-chunk upload.
The entire chunked upload path (temp file creation, chunk assembly, finalization)
has zero end-to-end coverage.

**Fix:** Add integration test: upload a 1MB file in 4 chunks with Content-Range headers,
verify the assembled file matches.

---

### MEDIUM: Dedup doesn't save network bandwidth (web UI)

**File:** `internal/api/seafhttp.go` (HandleUpload, single-shot path)

When a user uploads a file via the web UI, the full file content is transferred over
the network even if every block already exists in S3. The dedup check only happens
at the S3 storage level (skip write if exists). The Seafile sync protocol avoids this
with `check-blocks`, but the web UI doesn't.

**Impact:** Wasted bandwidth for re-uploads of existing files. Not a bug, but a
performance gap that affects users with large files.

**Fix:** Consider a client-side hash check before upload, or a server-side
`check-blocks` step in the web upload flow.

---

### MEDIUM: Encrypted files don't send Content-Length

**File:** `internal/api/seafhttp.go:1648` (streamFileFromBlocks)

For encrypted files, `Content-Length` is omitted because the decrypted size may
differ from the stored (encrypted) size. Clients cannot show download progress bars.

**Impact:** Poor UX for large encrypted file downloads.

**Fix:** Pre-compute decrypted size by summing original file sizes from `fs_objects`
(which stores plaintext size) and set Content-Length from that.

---

### LOW: Block integrity not verified on download

**File:** `internal/storage/blocks.go` (GetBlock)

Blocks are hashed (SHA-256) on upload but the hash is not re-verified on download.
If S3 returns corrupted data, it is streamed to the client as-is.

**Impact:** Silent data corruption. Mitigated by S3's own integrity guarantees,
but no defense-in-depth at the application layer.

**Fix:** Optional hash verification on download, or add an `X-Block-Hash` response
header so clients can verify.

---

## Test coverage

### Existing tests: 32

| What's tested | Count | Type |
|---------------|-------|------|
| Upload/download round-trip (content verified) | 1 | Integration |
| Upload overwrite, URL format, region pinning | 4 | Integration |
| Permission enforcement (readonly, guest blocked) | 2 | Integration |
| v2 direct upload | 1 | Integration |
| File CRUD + batch operations | 7 | Integration |
| History download (round-trip + errors) | 5 | Integration |
| ZIP download (region-pinned) | 1 | Integration |
| ZIP traversal limits (depth/count/bytes) | 3 | Unit |
| Token lifecycle (create/get/expire/delete) | 8 | Unit |

### Critical gaps: 8

| Gap | Risk | What to add |
|-----|------|-------------|
| Encrypted file upload/download | High | Create encrypted lib → upload → download → verify content |
| Chunked/resumable upload | High | Upload 1MB in 4 chunks via Content-Range → verify |
| Large file (>100 MB) | Medium | Upload 100MB+ file, verify block count and download |
| Concurrent upload to same path | Medium | Two clients upload simultaneously → verify no corruption |
| Quota exhaustion behavior | Medium | Fill quota → verify upload rejected with correct error |
| Expired token during download | Medium | Start download, expire token, verify behavior |
| Range request (video seek) | Medium | Upload video → Range request → verify partial content |
| Temp file cleanup on failure | Low | Start chunked upload, abandon, verify temp file cleaned |

### How to run

```bash
# All upload/download integration tests
go test -tags integration -v -run "TestUpload\|TestDownload\|TestFile\|TestHistory\|TestZip" \
    -timeout 5m ./internal/integration/...

# Unit tests only
go test -v -run "TestToken\|TestZipTraversal" ./internal/api/...
```

---

## Best practices check

| Practice | Status |
|----------|--------|
| Auth checked before any I/O | Yes (token validated first) |
| Quota checked before reading body | Yes (fail-fast) |
| File size limited | Only by quota (no hard max) |
| Temp files in secure location | Yes (/tmp, 0600 perms) |
| Temp files cleaned on success | Yes |
| Temp files cleaned on failure | **No** |
| Traffic recorded accurately | Yes (fire-and-forget) |
| Streaming uses bounded memory | Yes (4MB buffer pool) |
| Encrypted blocks never written to disk | Yes (memory only) |
| Download tokens are single-use | No (reusable within TTL — acceptable) |
