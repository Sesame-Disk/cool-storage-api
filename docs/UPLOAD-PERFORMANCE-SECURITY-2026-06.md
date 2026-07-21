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

## P-2 — Double S3 round-trip per block (Exists/HEAD + PUT) (RESOLVED — perf/p2-cassandra-first-hot-reuse)

**Severity: HIGH while P-1 is active; MEDIUM after**

`PutBlockAuto` in `internal/storage/blocks.go:100–119` (the original pattern):
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

**Fix applied (Cassandra-first probe, "Option B"):** the S3 HEAD is replaced by a
Cassandra probe — `DB.ProbeBlockReuse(orgID, blockID)` in
`internal/db/block_references.go`. It classifies each block before any S3 work:

| Decision             | Meaning                                              | Action in upload path        |
|----------------------|------------------------------------------------------|------------------------------|
| `BlockReuseReusable` | canonical metadata + live references, no GC fence    | `EnsureReusableBlockPresent`: verify the canonical object (HEAD on the declared key) and repair (direct PUT) only if missing |
| `BlockReuseNeedsPut` | no metadata, or metadata with no live references     | direct PUT (preferred backend for first writer; canonical metadata backend otherwise) |
| `BlockReuseBlockedByGC` | `gc_state='deleting'` or a `gc_s3_orphans` fence  | return `ErrBlockDeleteInProgress` → retry/back-off |
| `BlockReuseUnknownError` | Cassandra read failed / corrupt metadata         | fail closed; no physical write |

`PutBlockAutoDirect` (`internal/storage/blocks.go`) issues a direct `PutAuto`
without the prior HEAD. Probe/store/materialize wrappers are wired into all seven funnels:
`HandleUpload` and `finalizeUploadStreaming` (`seafhttp.go`), `SyncHandler.PutBlock`
(`sync.go`), `FileHandler.CreateFile` template and `UploadFile` (`files.go`), and
`OnlyOffice.saveEditedDocument` (`onlyoffice.go`), plus web block-session `UploadBlock` in
`internal/api/v2/blocks.go`.

**Scope of the fix — read carefully:** this is fixed for the *server-side hot
upload paths* listed above. It is **not** a global removal of the S3 HEAD:
- The `BlockStore` methods `PutBlockAuto` / `PutBlock` / `PutBlockData`
  (`internal/storage/blocks.go`) still perform `Exists` + `PutAuto`. They remain
  in use by unmigrated callers. Metadata-governed funnels never use them as a
  probe-error fallback because canonical placement and delete fences are unknown.
- The `Reusable` path no longer skips S3 entirely. To avoid trusting Cassandra
  metadata for a physical object that could be missing (partial GC, external
  deletion, eventual consistency, bugs), `EnsureReusableBlockPresent` does a
  targeted HEAD on the canonical key and repairs in place if the object is gone.
  This is a deliberate safety/perf trade: the HEAD is back on dedup hits, but the
  point-in-time `Reusable` path does not knowingly publish a reference to an already-missing object.
  It is not an atomic guarantee against a later GC delete.

**Per-block cost change:**
- New block (common case): was `1 S3 HEAD + 1 S3 PUT`; now `≤2 Cassandra reads (~1 ms each) + 1 direct PUT`, **no HEAD**.
- Dedup hit (reusable): was `1 S3 HEAD`; now `3 Cassandra reads + 1 canonical-verify HEAD` (+ 1 repair PUT only if the object is missing).

Net effect: the main win is for *new* content (the dominant case), which loses the
HEAD entirely. Dedup hits keep a HEAD but gain self-healing.

**Correctness status — writer-side fixed, lifecycle blocker open:** `RegisterUploadedBlock` now
returns the retry sentinel immediately after observing either fence, so the outer wrapper repeats
store before metadata. Web block-session uses the same operation boundary, and existing-metadata
`NeedsPut` uses `probe.StorageClass` plus the canonical key. `EnsureReusableBlockPresent` remains a
targeted HEAD/repair, not a physical fence against a stale key-only deleter. See
`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`.

**Retry loop change:** `retrySeafHTTPBlockMaterialization` (and the shared
`v2.RetryUploadedBlockMaterialization`) now treat `ErrBlockDeleteInProgress` from
the `store` phase as retryable (previously only the `materialize` phase was), since
a Cassandra-first probe can reject the block before any S3 work starts.

**Tests:**
- `internal/db/block_references_test.go` — full `ProbeBlockReuse` decision matrix
  (reusable, needs-put without metadata, needs-put with metadata + no refs + distinct
  storage class, blocked by orphan fence, blocked by `gc_state='deleting'` short-circuit,
  empty-storage-class error, metadata read-error propagation).
- `internal/api/seafhttp_test.go` — `finalizeUploadStreaming` reusable path (routes
  through the canonical verify once, no legacy/direct PUT) and needs-put path (no
  legacy HEAD, 1 direct PUT, ref/mapping registered); plus `store`-phase fence retry.
- `internal/api/v2/upload_reuse_test.go` — `EnsureReusableBlockPresent` exists-skip
  (validates `probe.StorageKey`) and missing-repair (derives the key from the hash when
  `storage_key` is empty) paths.
- `internal/api/v2/upload_reuse_test.go` — shared retry helper, fast-clear, and cancellable backoff.
- `TestRetryUploadedBlockMaterializationRestoresObjectAfterFastClearingFence` composes real
  registration with the outer retry; `TestRetryUploadedBlockMaterializationWithWorkerPausedAfterPostClaimCheck`
  pauses the real worker at the destructive boundary and proves the second store.
- `TestNeedsPutUsesCanonicalMinIOBucket` uses metadata/probes plus two real MinIO buckets; the web
  canonical-mismatch round trip proves upload/check/commit/download against the same invariant.
- `internal/streaming/canonical_block_reader_test.go` covers exact class routing, derived-key
  validation, cross-org key rejection, missing legacy metadata, and bounded deduplicated lookup.
- `internal/api/handler_mapping_failure_test.go` — sync needs-put uses direct PUT,
  not the legacy Exists+PUT.

**Caveat 1 (minor — dedup granularity):** `AccountBlockOnce` (`seafhttp.go:1988–1995`)
still deduplicates by *block position* (index), not by SHA-256, so same-content
blocks at different positions in one upload produce separate probes/PUTs. The
Cassandra probe now provides cross-upload dedup (a previously-stored block is
reused), which the old S3 HEAD also did — so no dedup regression.

**Caveat 2 (updated by P10 PR-2 — derived-key invariant):**
API reads and `EnsureReusableBlockPresent` derive the deterministic org-scoped key
through `BlockStore`. Reuse accepts `storage_key` only when empty or exactly equal to
that derivation and otherwise fails closed before S3 access. Arbitrary relocated keys
remain unsupported. P10 PR-3 moved GC delete and orphan recovery to the same
org-scoped derivation and removed the org-less storage APIs. See
`docs/KNOWN_ISSUES.md` (ISSUE-BLOCK-STORAGE-KEY-READS-01).

**Caveat 3 (minor — new failure surface on reuse):** because the `Reusable` path now
issues a canonical-verify HEAD, a transient S3 error on that HEAD now fails the
upload (it is not an `ErrBlockDeleteInProgress`, so it is not retried by the
materialization loop). Previously a reusable block touched no S3 and could not fail
there. This is acceptable — refusing to publish a reference we cannot verify is the
safer behavior — but it is a behavioral change worth knowing during incident triage.
Follow-up if real S3 shows transient HEAD errors: add a small retry/backoff
specifically around `ObjectExists` in `EnsureReusableBlockPresent` so a flaky HEAD
self-heals instead of failing the upload.

---

## P-3 — Benchmark: ~44–48 MB/s flat, no concurrency scaling

**Severity: N/A (measurement, not a code defect)**

Numbers are consistent with P-1 serialization plus two S3 RTTs per block.
`scripts/upload-benchmark.mjs` does not exist in the repository; benchmarks are
external.

---

## P-4 — One global Paxos round per block on every upload path (2026-07-21)

**Severity: HIGH for general upload performance** (both upload paths, not block upload specifically)
**Branch: pending — `perf/deterministic-block-storage-class`**
**Pre-existing: yes.** This is **not** introduced by the upload-fence work. The
first-writer LWT is on `main` and dates to `13e01263a` (2026-07-08); the code
comment there calls it *"the one LWT this path has always taken"*. Verified by
`git show main:internal/db/block_references.go`. Recording it here because it was
found while auditing that branch and would otherwise be lost.

### The Paxos

Every block upload — web session, seafhttp, sync `PutBlock`, OnlyOffice — ends in
`UpsertBlockMetadata`, which is an `INSERT INTO blocks ... IF NOT EXISTS`
([block_references.go:132](../internal/db/block_references.go#L132)): **one
lightweight transaction per block**.

It is load-bearing, not incidental. `storage_class`/`storage_key` are not globally
fixed per block — uploads pick a class per library and per routing region — so
first-writer-wins pins one canonical physical location. Without it a
last-writer-wins INSERT could repoint metadata at a class holding no copy, which
breaks reads and makes GC act on the wrong object.

**Why it costs more than it looks.** `configs/config.prod.yaml` commits
`serial_consistency: SERIAL`, and the documented production posture is multi-DC
(`dc-na:1,dc-eu:1,dc-asia:1`). That makes each of these a **global** Paxos round,
not a DC-local one. At the default 8 MB CAS block size a 1 GB file is ~128
cross-region consensus rounds. They parallelize with client upload concurrency.

**Correction (2026-07-21): this is NOT specific to block upload.** An earlier
revision of this section said "the legacy resumable path pays none of them". That is
false. `finalizeUploadStreaming` splits a resumable upload into `uploadBlockSize`
(8 MB) blocks and calls `registerUploadedBlockAndMappingForUploadFn` →
`RegisterUploadedBlock` → `UpsertBlockMetadata` for each one, so a 1 GB resumable
upload pays the same ~128 LWTs. Both upload paths carry the identical cost.

That changes what this finding means. It is **not** a reason block upload might lose
to resumable — neither is cheaper. It is a general per-block cost on every upload
surface, and removing it improves all of them at once, which strengthens rather than
weakens the case for doing it.

The LWT is scoped to the `(org_id, block_id)` partition, so writers of *different*
blocks never contend. The cost is latency per block, not lock contention.

### The two fence observations (not redundant reads)

Independently of Paxos, a single new-block upload reads the **same `blocks` row
twice** on `main`:

1. `ProbeBlockReuse` → `probeBlockReuseMetadataFn`
2. `BlockDeleteFenceActive` → `BlockGCState`

`gc_s3_orphans` is likewise read twice (probe and fence). Rough per-block cost for
a brand-new block in the web session flow: ~7–8 Cassandra reads/writes, one
`LoggedBatch` (provisional ref + expiry + by-day projection), and one LWT.

**These two reads are not redundant** — see fix 2 below. They observe different
moments, and the second one is what authorizes publication. Only the *third* read
that the fence work would add is genuinely removable.

**A third read is a cost the fence work would add, not one that exists today.** On
`main`, `GetBlockDeleteClaimInfo` (the stub-repair read) only runs when the delete
fence is active. The reference branch calls `removeStaleUploadedBlockDeleteStub` on
the unfenced path too, making it unconditional. That is avoidable and should not
ship as-is. Note the obvious shortcut does **not** work: `ProbeBlockReuse` returns
`NeedsPut` with an empty `StorageClass` both for a genuinely missing row and for a
stub, and `BlockReuseProbe` carries no `Found`, `IsStub` or claim id to separate
them, so keying a conditional delete off that signal would fire an LWT on every
brand-new block — trading a read for a Paxos round, which is worse. The state has to
become explicit in the probe (a distinct decision, or the claim metadata it already
read). Tracked as X7 in the findings registry.

### Proposed fixes

1. **Remove the Paxos: make `storage_class` deterministic per `(org_id, block_id)`.**
   Derive it from a stable routing function instead of the serving node's preferred
   backend. If every writer computes the same value, there is nothing to serialize:
   a plain last-writer-wins INSERT always writes the same class/key and the LWT can
   be dropped outright. This is a design change (routing must become a pure function
   of org+block, and existing rows must keep resolving), not a mechanical edit.
2. ~~**Collapse the two `blocks` reads into one** request-scoped fetch shared by the
   probe and the fence check.~~ **Withdrawn — this would reintroduce F1.** The two
   reads are not duplicated work; they are two points in time, and the second one is
   load-bearing:

   ```
   ProbeBlockReuse        read #1  — before the PUT, decides whether to store
   ...PUT...
   AddBlockReference      write    — publish our flag
   BlockDeleteFenceActive read #2  — AFTER the reference, decides whether to publish
   UpsertBlockMetadata    write
   ```

   The ordering is the whole mutual-exclusion protocol: the writer publishes its
   reference *before* reading the fence, so either GC's post-claim recheck sees the
   reference and abandons, or the writer sees the fence and retries. Serving read #2
   from a cached pre-PUT observation breaks that. GC could claim, recheck, find no
   reference, and authorize its delete in the window between the two, while the
   writer replays a stale "no fence" and publishes metadata for a block that is about
   to be deleted — exactly F1 again.

   Query-level work is still fair game (fewer columns, a single statement covering
   `blocks` and `gc_s3_orphans` for the *same* observation point). What must not
   change is that the fence observation used to authorize publication is **fresh and
   taken after the provisional reference is durable**. Treat the third read that the
   fence work would add separately — see X7; it needs an explicit stub state in the
   probe, and it is genuinely avoidable in a way these two are not.
3. **Measure before choosing.** Instrument the latency of that specific INSERT so the
   real production number replaces this estimate. `block_upload_materialization_retries_total`
   already exists; per-statement latency does not.

Do **not** attempt to fix this by switching the statement to `LOCAL_SERIAL`: with
per-DC first writers that lets two DCs each "win" locally and diverge on placement,
which is the exact corruption the LWT exists to prevent.

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
**Branch: fixed in `upload/chunked-hardening-limits`**

Chunked uploads now enforce `server.max_upload_mb` before a new tracker creates
or truncates its temp file. Requests above the configured cap fail closed with
HTTP 413 instead of consuming local staging first.

---

## S-3 — Full /tmp staging before any byte reaches S3

**Severity: MEDIUM**
**Branch: mitigated in `upload/chunked-hardening-limits`**

The `/tmp` staging model still exists, but the node can now enforce
`seafhttp.chunked_staging_max_bytes` as a reservation budget across active
chunked uploads. If the sum of tracked upload sizes plus a new upload would
exceed that limit, tracker creation fails with HTTP 507 before local staging is
extended.

Important limit: the default stays `0` (disabled) for backwards compatibility,
so operators still need to choose and roll out a real node-local budget.

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
| P-2 | Double S3 RTT per block (Exists + PUT)         | ✅ Confirmed   | HIGH→MEDIUM| **RESOLVED** |
| P-3 | Benchmarks 44–48 MB/s, no scaling              | ❓ Plausible   | —          | External     |
| P-4 | 1 global Paxos/block on BOTH upload paths. (The 2 `blocks` reads are two required observation points, **not** part of the optimization.) | ✅ Confirmed | HIGH | Pending (pre-existing, not from the fence branch) |
| S-1 | Chunk state node-local (multi-node blocker)    | ✅ Confirmed   | HIGH       | Pending      |
| S-2 | max_upload_mb not enforced on chunked uploads  | ✅ Fixed       | MEDIUM     | Complete     |
| S-3 | Full /tmp staging, no disk admission limit     | ✅ Mitigated   | MEDIUM     | Guard added; config still required |
| S-4 | TOCTOU quota check across concurrent uploads   | ✅ Confirmed   | MEDIUM     | Pending      |
| S-5 | Client filename, content-type ignored          | ✅ Confirmed   | LOW        | Pending      |

Note: upload tokens in production are Cassandra-backed and multi-node safe
(`server.go:190–192`). The in-memory `TokenManager` fallback only activates
without a database connection.

## Recommended branch order

| Order | ID | Change | Rationale |
|---|---|---|---|
| 1 | P-1 | Unwrap S3 PUT from metadata permit | Max impact, minimal change — **done** |
| 2 | P-2 | Cassandra-first probe; direct PUT, no HEAD | Eliminates the S3 HEAD per block — **done** (`perf/p2-cassandra-first-hot-reuse`) |
| 3 | S-1 | Sticky sessions at LB (immediate) or distributed chunk state (complete) | Required for multi-node topology |
| 4 | S-2/S-3 | Roll out a real `chunked_staging_max_bytes` value per node | Operational hardening follow-through |
| 5 | S-4 | Atomic quota reservation at upload start | Closes the concurrent over-quota window |
| 6 | P-4 | Deterministic per-`(org, block)` storage class, then drop the first-writer LWT. **Preserve the fresh post-reference fence read** — do not merge it with the pre-PUT probe. | Removes one global Paxos round per block. Both upload paths pay it equally, so the win applies to every upload surface. Measure first. |
