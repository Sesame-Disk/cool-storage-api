# Chunking & Block Storage — Preproduction Assessment

**Date:** 2026-04-14
**Diagrams:** [Chunking Flow Diagrams](./diagrams/chunking-flow.md)

---

## Issues found

### HIGH: No end-to-end test for check-blocks + sync upload

The Seafile desktop sync protocol (`POST /check-blocks` → `PUT /block/:id` → commit)
has zero integration test coverage. This is the primary upload path for desktop clients
and the main dedup optimization — yet it's never tested against real Cassandra + S3.

**Risk:** A regression in block existence checking, SHA-1→SHA-256 mapping, or the
sync commit flow would break desktop sync for all users.

**Fix:** Add integration test: upload file via sync protocol → check-blocks → upload
missing → create commit → download and verify content.

---

### HIGH: No test for SHA-1 ↔ SHA-256 mapping correctness

The forward mapping table (`block_id_mappings`) is written on upload and read on
desktop/sync download. PR7 dropped the reverse table
`block_id_mappings_by_internal`; GC now resolves SHA-1 from `blocks.sha1`. If the
forward mapping is wrong or missing, downloads return 404 or corrupted data.

**Currently:** Mapping is tested implicitly (upload via web, download via web works).
But there's no test that verifies: upload via web (creates mapping) → download via
sync protocol (reads mapping) → content matches. Cross-protocol mapping is untested.

**Fix:** Add integration test: upload via multipart form → read block_id_mappings
from Cassandra → verify SHA-1 and SHA-256 both resolve to same block content.

---

### MEDIUM: Cross-method dedup doesn't work

Same file uploaded via web UI (8 MB fixed blocks) and via desktop sync (FastCDC
variable blocks) produces **different block hashes**. Both sets are stored in S3.
A 50 MB file creates ~12 blocks instead of ~5.

**Impact:** Double storage cost for files that exist on both web and desktop.
Not a bug — it's inherent to using different chunking algorithms — but it's a
cost concern at scale.

**Fix:** Consider standardizing on FastCDC for the web upload path, or accepting
the trade-off and documenting it.

**Tested:** No. No test verifies or measures the cross-method dedup gap.

> **Reconfirmed 2026-06-22, corrected 2026-06-24** while implementing — and then
> fixing the desktop/mobile compatibility of — the web content-addressed upload
> flow ([WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md)). The web block flow uses
> fixed 8 MB blocks; desktop uses FastCDC variable blocks. **Both now use SHA-1 as
> the external block ID** (the web flow's dual-hash fix writes a 40-hex SHA-1 into
> the file fs_object and keeps SHA-256 as the internal storage identity via a
> forward `sha1 → sha256` mapping), so the external-ID *hash algorithm* is no
> longer the differentiator it was originally recorded to be. The residual gap is
> purely the **block boundaries**: identical content still chunks differently
> (8 MB fixed vs FastCDC), so the blocks — and therefore both their SHA-1 and
> SHA-256 — don't line up and aren't reusable across methods. Closing this needs
> FastCDC in the browser/worker (matching the desktop boundaries); SHA-1 aliasing
> is no longer the missing piece. Deferred out of phase 1.

---

### Debts recorded from the web block-upload work (2026-06-22)

- **Legacy `/blocks/upload` without a session now materializes Cassandra state.**
  It writes canonical block metadata and a deterministic provisional TTL pin with
  canonical and by-day expiry rows, so abandoned objects remain GC-discoverable.
  The web flow still passes a `session` because its commit requires session ownership;
  a legacy pin does not authorize an unrelated session. See R2/R9 in
  [WEB-BLOCK-UPLOAD.md](./WEB-BLOCK-UPLOAD.md).
- **Encrypted libraries** are rejected by the block flow (SHA-256 over plaintext
  vs server-side block encryption). Deferred.
- **Public share/upload links** cannot mint a session (no authenticated token)
  and keep restarting from zero. Deferred — would need a signed-session variant.

---

### MEDIUM: Block integrity not verified on download

**File:** `internal/storage/blocks.go` (GetBlock)

Blocks are stored by SHA-256 hash but the hash is never re-verified when reading.
Corrupted S3 data would be silently served to clients.

**Fix:** Optional: re-hash on read and compare with the block ID. Or add the hash
as a response header (`X-Block-Hash`) so clients can verify.

**Tested:** No.

---

### MEDIUM: check-blocks has no rate limiting

**File:** `internal/api/v2/blocks.go` (CheckBlocks)

The v2 API limits to 10,000 hashes per request, but there's no per-IP or per-user
rate limit on the endpoint itself. An attacker could enumerate all blocks in an org
by sending repeated check-blocks requests with random hashes.

**Risk:** Block existence oracle — attacker can discover which SHA-256 hashes exist,
which leaks information about stored content.

**Fix:** Add rate limiting on the check-blocks endpoint.

**Tested:** The 10k limit is tested. Rate limiting is not.

---

### LOW: Adaptive chunking speed probe not tested in real network

The adaptive chunker adjusts block sizes based on a 1 MB speed probe. If the probe
gives a wrong measurement (e.g., during a network spike), subsequent chunks may be
too large (causing timeouts) or too small (wasting round-trips).

**Tested:** Speed probe tested with a mock throttled writer but never against a real
network endpoint.

---

### LOW: Orphaned block mappings after block deletion

When GC deletes a block from S3 and the `blocks` table, PR7 deletes the forward
mapping using `blocks.sha1`. If the worker crashes after deleting the canonical
row but before mapping cleanup, some forward mappings (`block_id_mappings`) may
remain as harmless dangling pointers because the reverse table no longer exists.

**Impact:** Wasted Cassandra storage. No data loss.

**Tested:** Covered by unit/integration tests for the PR7 fail-safe; monitor
`gc_block_mapping_sha1_missing` in production.

---

## Test coverage

### Existing tests: 31

| What's tested | Count | Type |
|---------------|-------|------|
| FastCDC (small data, large data, determinism, boundaries) | 5 | Unit |
| Adaptive sizing (speed-based, min/max bounds, timeout/success) | 12 | Unit |
| Speed probe (measurement, timeout, categorization) | 4 | Unit |
| Large file chunking (100-256 MB) | 1 | Integration |
| Dedup potential (80% overlap measurement) | 1 | Integration |
| Performance benchmark (50 MB throughput) | 1 | Benchmark |
| Block API validation (hash format, limits, JSON) | 9 | Unit |
| Upload ref_count increment (dedup) | 1 | Integration |
| Cross-library copy ref_count | 1 | Integration |

### Critical gaps: 7

| Gap | Risk | What to add |
|-----|------|-------------|
| check-blocks → sync upload end-to-end | High | POST check-blocks → upload missing → commit → download → verify |
| SHA-1 ↔ SHA-256 mapping correctness | High | Upload → query both mapping tables → verify bidirectional |
| Cross-protocol download (web upload → sync download) | High | Upload via web → download via sync block API → verify |
| Cross-method dedup measurement | Medium | Upload same file via web + sync → count total blocks in S3 |
| Block download via sync protocol | Medium | GET /seafhttp/repo/:id/block/:sha1 → verify content |
| Encrypted block mapping (plaintext SHA-1 → ciphertext SHA-256) | Medium | Upload to encrypted lib → verify mapping correctness |
| Block integrity on download | Low | GET block → re-hash → compare with block ID |

### How to run

```bash
# Chunking unit + integration tests
go test -v ./internal/chunker/...

# Block API unit tests
go test -v -run "TestCheckBlocks\|TestDirectBlockReadRoutesAreNotRegistered\|TestUploadBlock" ./internal/api/v2/...

# Integration tests involving blocks
go test -tags integration -v -run "TestUpload\|TestDuplicate\|TestCrossLibrary\|TestBatchDelete" \
    -timeout 5m ./internal/integration/...

# Performance benchmark
go test -bench=. -benchtime=10s ./internal/chunker/...
```

---

## Best practices check

| Practice | Status |
|----------|--------|
| Content-addressed storage (SHA-256) | Yes |
| Dedup via hash before write | Yes (S3 existence check) |
| Block size bounded (min/max) | Yes (2 MB – 256 MB) |
| Parallel existence checks | Yes (10-20 concurrent) |
| check-blocks limit enforced | Yes (10,000 per request) |
| Block ID validated before storage ops | Yes (64 hex chars only) |
| Path traversal impossible in S3 keys | Yes (two-level sharding from hash) |
| Dual mapping tables (forward + reverse) | Yes |
| Mapping written atomically on upload | Yes (dual-write) |
| Block hash re-verified on download | **No** |
| Rate limiting on check-blocks | **No** |
| Cross-method dedup | **No** (different chunking → different hashes) |
| Adaptive sizing tested in real network | **No** |

---

## RESOLVED (2026-06-17): Chunked upload "tracker split" — file never finalizes

### Symptom

With **multiple files** uploading at once (especially via upload links / share
links), a large file would report `{"success":true}` on its final chunk yet never
appear in the library. The browser surfaced **"Upload could not be confirmed.
Please retry."** A single file uploaded alone always worked. Not a timeout — the
finalize duration is identical when the file uploads alone.

### Root cause

The server keys each in-flight chunk tracker by
`(token, resumableIdentifier, parentDir, filename, totalSize)` — see
`chunkUploadTrackerKey` in [internal/api/seafhttp.go](../internal/api/seafhttp.go).
The browser sends **one upload token for the whole session**, reused across every
chunk of every file.

The bug was entirely on the **frontend**: `this.resumable.opts.target` (which
carries the upload token in its URL) is a **single value shared by all files**.
Several code paths re-fetched a *fresh* upload link mid-session and reassigned the
global target. `seafileAPI.{getFileServerUploadLink,sharedLinkGetFileUploadUrl,
sharedUploadLinkGetFileUploadUrl}` mints a **brand-new token** on each call. So a
file still uploading had its *remaining* chunks routed to a different token →
**different server-side tracker**. The bytes split across two trackers, neither
reached contiguity (`isCompleteLocked`), neither finalized, and the file silently
never committed.

Two trigger paths (both fixed):

1. **`onFileAdded` single-file branch was unguarded.** Adding the big file alone
   first fetched token A but never set `isUploadLinkLoaded`; adding more files
   later then minted token B and overwrote the target under the in-flight big
   file. ("Add big file first, then add the rest" was a 100% repro; "select all at
   once" worked because the first add took the guarded multi-file branch.)
2. **Retry / replace paths** (`retryUploadWithFreshLink`, `onUploadRetryAll`,
   `replaceRepetitionFile`, `uploadFile`) each re-fetched and reassigned the global
   target. The 409 auto-retry (`shouldAutoRetryUploadConflict`, fired when a
   concurrent commit returns "library was modified concurrently") swapped the token
   out from under other in-flight files — which is why the bug needed concurrency.

### Invariant (do not regress)

**The session upload token is fetched ONCE and the shared `resumable.opts.target`
must never be reassigned while any file is in flight.**

- Fetch the upload link once, guarded by `isUploadLinkLoaded`; files added later are
  picked up automatically by the running queue (no re-fetch, no re-`upload()`).
- A 409 / conflict retry reuses the **existing** session token — re-fetching is
  unnecessary (the token is path-scoped and multi-use) and harmful.
- When one file genuinely needs a different endpoint (replace via update-link, or
  "upload anyway"), set the URL on the **file**, not the resumable instance:
  `resumableFile.opts.target = …`. `resumable.getOpt` resolves
  chunk → file → resumable, so a per-file target overrides the global for that file
  only and leaves every other in-flight upload untouched.

### Diagnostic left in place

`HandleUpload` logs `FINAL_CHUNK_BUT_INCOMPLETE … first_gap=A-B ranges=N
received=R/total` (via `ChunkUpload.DebugCompletenessSnapshot`) whenever the
final-offset chunk arrives but the tracker is still non-contiguous. A clean,
chunk-aligned gap at offset 0 with the tail present is the fingerprint of a tracker
split; grep the backend log for two `Created upload tracker … file=<name>` lines
with **different** `op=seafhttp:<token>:…` to confirm.
