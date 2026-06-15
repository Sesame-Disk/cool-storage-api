# Upload Performance & Security Audit — 2026-06

Audit of the chunked upload path. Each finding includes a verdict verified
against source code, severity, and the recommended fix branch.

## P-1 — Metadata permit serialized the S3 PUT (RESOLVED — fix/upload-permit-unwrap-s3-put)

**Severity: CRITICAL**

`finalizeUploadBlockMetadataConcurrency = 1` creates a single-slot semaphore
(`seafhttp.go`). The previous code acquired that permit *before*
`retrySeafHTTPBlockMaterialization`, which enclosed inside the critical section:

1. `putUploadedBlockAutoFn` → S3 Exists + S3 PUT
2. `registerUploadedBlockAndMappingForUploadFn` → Cassandra LWT
3. `clearSeafHTTPS3OrphanFenceFn` → Cassandra fence check

The code comment correctly described the intent ("Keep block PUTs parallel, but
serialize IncrementOrCreateBlock") but the implementation violated it. The 8
workers of `finalizeUploadConcurrency` had no effect; all contended on the single
slot.

**Fix applied:** permit moved inside the `materialize` callback (the Cassandra
LWT). S3 Exists + S3 PUT now run in parallel across all 8 workers. The fence
check does not need the permit.

**Expected impact:** 5–8× throughput on local MinIO; more significant on real
S3 due to per-block RTT.

**Regression test added:** `TestFinalizeUploadBlockMetadataPermitDoesNotBlockS3Put`
verifies that the S3 PUT callback completes while the permit is externally held.

---

## P-2 — Double S3 round-trip per block (Exists/HEAD + PUT)

**Severity: HIGH while P-1 is active; MEDIUM after**
**Branch: pending**

`PutBlockAuto` in `internal/storage/blocks.go:100–119`:
```go
exists, err := bs.s3.Exists(ctx, key)   // HEAD — round-trip 1
// if not present:
_, err = bs.s3.PutAuto(ctx, key, reader, size)  // PUT — round-trip 2
```

Same pattern in `PutBlock` (L75–96) and `PutBlockData` (L50–71). With P-1 active,
both RTTs were serialized process-wide. With P-1 resolved, they run in parallel
across 8 workers, but each block still costs 2 RTTs for new blocks (~50 ms per RTT
on real S3 → ~100 ms per block). For a 1 GB file (128 × 8 MB blocks), that is
~12.8 s of aggregate RTT work if serialized; with 8 ideal parallel workers the
wall-clock RTT floor drops to ~1.6 s, plus transfer time and S3/client queuing.

**Proposed fix:** remove the `Exists` check from `PutBlockAuto`/`PutBlock` and use a
direct PUT. S3 accepts re-PUT of the same key idempotently. Note:
`upload.AccountBlockOnce` (`seafhttp.go:1988–1995`) deduplicates by *block position*
(index), not by content SHA-256, so same-content blocks at different positions still
produce separate PUTs. The `Exists` check is the only cross-position dedup guard
today; removing it trades that guard for lower latency.

Alternative: check block existence in Cassandra (`block_references`) before hitting
S3 — one Cassandra read (<1 ms) instead of an S3 HEAD (10–80 ms).

---

## P-3 — Benchmark: ~44–48 MB/s flat, no concurrency scaling

**Severity: N/A (measurement, not a code defect)**

Numbers are consistent with P-1 serialization plus two S3 RTTs per block.
`scripts/upload-benchmark.mjs` does not exist in the repository; benchmarks are
external.

---

## S-1 — Chunk state is node-local (multi-node blocker)

**Severity: HIGH for multi-node topologies**
**Branch: pending**

**Upload tokens (clarification):** In production (`database != nil`),
`server.go:190–192` uses `NewCassandraTokenAdapter` — tokens are Cassandra-backed
and multi-node safe. The `TokenManager` in-memory fallback only activates without a
database connection.

**Chunk state (the actual problem):** `chunkManager` is a process-global variable
(`seafhttp.go:375`) backed by `map[string]*ChunkUpload` and temp files under
`os.TempDir()` — entirely node-local. If a load balancer routes chunks from the
same upload to different nodes, node B creates an empty tracker and the upload
fails at finalization.

**Immediate mitigation:** sticky sessions at the LB keyed on the upload token
(already available in Cassandra — no server-side changes required).

**Permanent fix:** distribute chunk state (Redis or Cassandra) and/or stream
blocks directly to S3 as chunks arrive, eliminating node-local staging.

---

## S-2 — `server.max_upload_mb` is not enforced on chunked uploads

**Severity: MEDIUM**
**Branch: pending**

Defined in `internal/config/config.go`. Not referenced in `seafhttp.go` for
chunked uploads. Single-shot uploads have a hardcoded 1 GiB limit (`seafhttp.go:1480`).
Without a storage quota configured for an org, a user can upload files of arbitrary
size over chunked paths.

**Fix:** read `cfg.SeafHTTP.MaxUploadMB` in `GetOrCreateUpload` before
`Truncate(totalSize)` and reject with 413 if `totalSize > max`.

---

## S-3 — Full /tmp staging before any byte reaches S3

**Severity: MEDIUM**
**Branch: pending**

`os.TempDir()` (`seafhttp.go:346`) + `Truncate(totalSize)` (`seafhttp.go:396`).
On Linux with ext4/xfs, `Truncate` creates a sparse file (no physical block
allocation until chunks are written), but disk pressure grows as chunks arrive.
There is no per-node, per-org, or per-token staging limit. The janitor cleans
orphans after 2 hours (`chunkDiskTTL`).

Under concurrent large uploads without conservative storage quotas, `/tmp` can
be exhausted.

**Fix:** a configurable maximum staging bytes per node; reject `GetOrCreateUpload`
if the limit would be exceeded.

---

## S-4 — TOCTOU quota check under concurrent same-org uploads

**Severity: MEDIUM**
**Branch: pending** (see ISSUE-QUOTA-RESERVATION-01 in `docs/KNOWN_ISSUES.md`)

The finalization permit (`acquireSeafHTTPUploadFinalizePermit`) is per-repo, not
per-org. Two uploads to different repos in the same org run concurrently. Both can
pass the quota check before either has committed storage counters. The race window
is not milliseconds — it spans the entire finalization (potentially seconds per
upload, amplified by P-1 while active).

With P-1 resolved the window shrinks but does not disappear, because concurrent
same-org finalizations to different repos can still overlap.

**Fix:** atomic byte reservation at upload start (pending reservation in Cassandra),
confirmed or released at finalization. See ISSUE-QUOTA-RESERVATION-01 for the
previous investigation and known caveats in that approach.

---

## S-5 — Client-controlled filename; content-type ignored

**Severity: LOW**

`filename := header.Filename` (`seafhttp.go:1359`) — taken directly from the
multipart form, no additional validation beyond `filepath.Join` (which neutralizes
path traversal in the final DB path). `sanitizeFilename()` (`seafhttp.go:821–824`)
only protects the temp file name. The server always returns `application/octet-stream`
on download regardless of what the client sent.

No active exploit vector. Client-controlled filename is expected behaviour in
file-sharing protocols.

---

## Summary table

| ID  | Finding                                        | Verdict        | Severity   | Status       |
|-----|------------------------------------------------|----------------|------------|--------------|
| P-1 | Permit serialized S3 PUT                       | ✅ Confirmed   | CRITICAL   | **RESOLVED** |
| P-2 | Double S3 RTT per block (Exists + PUT)         | ✅ Confirmed   | HIGH→MEDIUM| Pending      |
| P-3 | Benchmarks 44–48 MB/s, no scaling              | ❓ Plausible   | —          | External     |
| S-1 | Chunk state node-local (multi-node blocker)    | ✅ Confirmed   | HIGH       | Pending      |
| S-2 | max_upload_mb not enforced on chunked uploads  | ✅ Confirmed   | MEDIUM     | Pending      |
| S-3 | Full /tmp staging, no disk admission limit     | ✅ Confirmed   | MEDIUM     | Pending      |
| S-4 | TOCTOU quota check across concurrent uploads   | ✅ Confirmed   | MEDIUM     | Pending      |
| S-5 | Client filename, content-type ignored          | ✅ Confirmed   | LOW        | Pending      |

Note: upload tokens in production are Cassandra-backed and multi-node safe
(`server.go:190–192`). The in-memory `TokenManager` fallback only activates
without a database connection.

## Recommended branch order

| Order | ID | Change | Rationale |
|---|---|---|---|
| 1 | P-1 | Unwrap S3 PUT from metadata permit | Max impact, minimal change — **done** |
| 2 | P-2 | Remove `Exists` check from `PutBlockAuto` | Eliminates half the S3 RTTs per block |
| 3 | S-1 | Sticky sessions at LB (immediate) or distributed chunk state (complete) | Required for multi-node topology |
| 4 | S-2/S-3 | Apply `max_upload_mb` + disk admission limit | Operational hardening |
| 5 | S-4 | Atomic quota reservation at upload start | Closes the concurrent over-quota window |
