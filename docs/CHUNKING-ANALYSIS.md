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

The dual mapping tables (`block_id_mappings` and `block_id_mappings_by_internal`)
are written on every upload and read on every download. If the mapping is wrong or
missing, downloads return 404 or corrupted data.

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

When GC deletes a block from S3 and the `blocks` table, it also deletes mappings
found via the `block_id_mappings_by_internal` index. If the index is stale or
incomplete, some forward mappings (`block_id_mappings`) may remain as orphans.

**Impact:** Wasted Cassandra storage. No data loss.

**Tested:** No.

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
go test -v -run "TestCheckBlocks\|TestDownloadBlock\|TestUploadBlock" ./internal/api/v2/...

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
