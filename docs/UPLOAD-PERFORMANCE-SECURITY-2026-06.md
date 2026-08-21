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
| `BlockReuseNeedsPut` | no metadata, or metadata with no live references     | `PutBlockAutoDirect` (no HEAD)|
| `BlockReuseBlockedByGC` | `gc_state='deleting'` or a `gc_s3_orphans` fence  | return `ErrBlockDeleteInProgress` → retry/back-off |
| `BlockReuseUnknownError` | Cassandra read failed / corrupt metadata         | fail closed before S3 PUT |

`PutBlockAutoDirect` (`internal/storage/blocks.go`) issues a direct `PutAuto`
without the prior HEAD. The probe is wired into all seven governed upload funnels:
`HandleUpload` + `finalizeUploadStreaming` (`seafhttp.go`), `SyncHandler.PutBlock`
(`sync.go`), `FileHandler.CreateFile` template + `UploadFile` (`files.go`), and
`OnlyOffice.saveEditedDocument` (`onlyoffice.go`), plus session-mode
`BlockHandler.UploadBlock` (`v2/blocks.go`).

**Scope of the fix — read carefully:** this is fixed for the *server-side hot
upload paths* listed above. It is **not** a global removal of the S3 HEAD:
- The `BlockStore` methods `PutBlockAuto` / `PutBlock` / `PutBlockData`
  (`internal/storage/blocks.go`) remain for legacy callers. Session-mode
  `v2/blocks.go` is migrated; its metadata-free no-session branch was removed in
  PR-7 (finding F8) — a session-less `/blocks/upload` now returns 400 before any
  store I/O.
- The `Reusable` path no longer skips S3 entirely. To avoid trusting Cassandra
  metadata for a physical object that could be missing (partial GC, external
  deletion, eventual consistency, bugs), `EnsureReusableBlockPresent` does a
  targeted HEAD on the canonical key and repairs in place if the object is gone.
  This is a deliberate safety/perf trade: the HEAD is back on dedup hits, but the
  upload can never publish a reference to a missing object.

**Per-block cost change (web session, successful no-retry cycle):** the narrow GC
protocol accounts for 7 Cassandra point reads on a new block and 8 on a dedup hit:
2/3 initial classification reads, 2 materialization fence reads and 3 confirmation
classification reads. Those are not the complete materialization cost. Including the
expiry lookup, two representation resolutions, mapping lookup and the dedup path's
post-non-applied-LWT identity read, the full `store -> materialize -> confirm` cycle is:

- New block: 11 plain Cassandra point reads, one applied metadata LWT, ordinary
  reference/mapping writes, one expiry logged batch, one direct S3 PUT and one
  post-materialization canonical HEAD.
- Dedup hit: 13 plain Cassandra point reads, one non-applied metadata LWT, the
  ordinary provisional-reference write, one expiry logged batch and two canonical
  HEADs (plus a repair PUT only if the object is missing).

These cycle totals exclude request-level session, library-routing, permission, quota
and staging-admission reads. Other funnels can omit mapping reads or carry additional
handler-specific work, so these numbers must not be presented as a universal endpoint
total.

The pre-PUT HEAD remains removed, preserving PUT pipeline latency, but PR-5
deliberately restores a post-reference HEAD to close the fast-clear data-loss window.
This is a correctness cost of one object-store RTT per newly materialized block.

**Correctness — why skipping the PUT is safe:** the GC-race safety never relied on
the PUT. It derives from the *reference-first, then fence-check* protocol in
`FSHelper.RegisterUploadedBlock` (`fs_helpers.go:1006–1034`) versus GC's
*claim-then-verify-references* in `worker.go:processBlock` (L410–443) — a
Dekker-style mutual flagging. The `gc_s3_orphans` fence is written *before* the S3
`DeleteObject` and cleared only *after* it, so deletes inside the durable orphan
lifecycle are visible to both the probe and materialize step. The probe fails
closed when ownership cannot be established (BlockedByGC → retry, UnknownError →
error, NeedsPut → direct PUT). This does not close X1: a delayed same-key S3 delete
can outlive its Cassandra authorization state. Never-reused physical keys close that
physical-delete ABA component, but the publication, claim-ownership and recovery criteria
remain. Keep GC disabled fleet-wide until the complete X1 workstream closes. See the
audit thread for the full interleaving analysis.

**Retry loop change:** `retrySeafHTTPBlockMaterialization`, the shared
`v2.RetryUploadedBlockMaterialization`, and the template-CreateFile wrapper treat a GC
delete fence (`ErrBlockDeleteInProgress`) in **either** phase as retryable, plus any
failure **tagged** `v2.ErrBlockMaterializationTransient` (produced today by the
materialize phase — `RegisterUploadedBlock` Cassandra I/O and the mapping write — and
by canonical HEAD/repair failures in `StoreUploadedBlockForProbe`, which also tags the
web-session funnel's direct PUT). As of
PR-3, `RegisterUploadedBlock` no longer waits out the fence internally — it propagates
it, so the wrapper re-PUTs the object when the fence is observed. PR-5 adds a second
canonical store observation after materialization; because the provisional reference
is then durable, it repairs an object removed by an already-completed GC cycle and
closes the never-observed fast-clear window. A permanent metadata
failure (`db.ErrBlockMetadataPermanent`) is not retried, and the retry metric
`block_upload_materialization_retries_total{surface,reason}` labels the reason by the
failing phase (`probe` = store phase, `materialization` = metadata materialize phase),
so a metadata write is never counted as `probe` (closes F14).

**Tests:**
- `internal/db/block_references_test.go` — full `ProbeBlockReuse` decision matrix
  (reusable, needs-put without metadata, needs-put with metadata + no refs + distinct
  storage class, blocked by orphan fence, blocked by `gc_state='deleting'` short-circuit,
  empty-storage-class error, metadata read-error propagation).
- `internal/api/seafhttp_test.go` — `finalizeUploadStreaming` reusable path (initial
  canonical HEAD plus post-materialization confirmation, no legacy/direct PUT) and
  needs-put path (no pre-PUT legacy HEAD, 1 direct PUT, ref/mapping registered, then
  canonical confirmation); plus `store`-phase fence retry.
- `internal/api/v2/upload_reuse_test.go` — `EnsureReusableBlockPresent` exists-skip
  (honors `probe.StorageKey`) and missing-repair (derives the key from the hash when
  `storage_key` is empty) paths.
- `internal/api/v2/upload_reuse_test.go` — shared retry helper and probe errors.
- `internal/api/handler_mapping_failure_test.go` — sync needs-put uses a direct PUT,
  not the legacy Exists+PUT, and then performs the required canonical confirmation.

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
issues canonical-verify HEADs, storage failure can fail the upload where reuse
previously touched no S3. Canonical HEAD and repair-PUT failures are tagged
`ErrBlockMaterializationTransient` and consume the bounded materialization retry
budget. Raw Cassandra probe errors and direct new-block PUTs in the six older manual
store callbacks remain untagged/non-retryable; the web-session callback uses
`StoreUploadedBlockForProbe`, which tags its direct PUT too.

---

## P-3 — Benchmark: ~44–48 MB/s flat, no concurrency scaling

**Severity: N/A (measurement, not a code defect)**

Numbers are consistent with P-1 serialization plus two S3 RTTs per block.
`scripts/upload-benchmark.mjs` does not exist in the repository; benchmarks are
external.

---

## P-4 — One `SERIAL` LWT/Paxos transaction per block on metadata-registering upload paths (2026-07-21)

**2026-08-20 characterization correction:** the shipped production
default/example configures `serial_consistency: SERIAL`, so the current metadata
LWT inherits that effective session setting when it is not overridden. The
transaction is cross-DC only when the effective replica topology spans DCs.
P0/R12 would make the intended serial domain explicit; it would not introduce
this latency to deployments already using `SERIAL`. The companion
[X1/X4 hot-path characterization](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md)
also records that chunked SeafHTTP finalizations use one process-local metadata
permit, while non-chunked `HandleUpload` bypasses it, and that the two-minute
final-file context begins only after `eg.Wait()`.

**Severity: HIGH for general upload performance** (both governed upload modes, not block upload specifically)
**Branch: pending — `perf/deterministic-block-storage-class`**
**Pre-existing: yes.** This is **not** introduced by the upload-fence work. The
first-writer LWT is on `main` and dates to `e3883aa5d` (2026-05-28); the code
comment calls it *"the one LWT this path has always taken"*. Commit `13e01263a`
later made that write representation-aware but did not introduce the LWT. Verified
from Git history and `main:internal/db/block_references.go`. Recording it here
because it was found while auditing that branch and would otherwise be lost.

### The LWT/Paxos transaction

Every upload path that registers canonical block metadata — web session, seafhttp,
sync `PutBlock`, OnlyOffice — ends in `UpsertBlockMetadata`, which is an
`INSERT INTO blocks ... IF NOT EXISTS`
([`internal/db/block_references.go`](../internal/db/block_references.go),
`upsertBlockMetadataInsertWithRepresentationFn`): **one lightweight transaction
per block**. The defective no-session legacy path tracked
as F8 used to be the production exception: it wrote only the S3 object and never
reached metadata registration. PR-7 removed that path, so every remaining
supported production upload surface registers metadata.

It is load-bearing, not incidental. `storage_class`/`storage_key` are not globally
fixed per block — a new materialization picks a preferred class from the library
or request routing, while an existing canonical row retains its persisted class
— so first-writer-wins pins one canonical physical location. Without it a
last-writer-wins INSERT could repoint metadata at a class holding no copy, which
breaks reads and makes GC act on the wrong object.

**Why it costs more than it looks.** The shipped production default/example
sets `serial_consistency: SERIAL`, but `CASSANDRA_SERIAL_CONSISTENCY` can
override it. When the effective setting is `SERIAL` and the replica topology is
multi-DC, each registration is a cross-DC `SERIAL` LWT/Paxos transaction rather
than a DC-local one. At the default 8 MB CAS block size a 1 GB file is ~128
such transactions. The number of network round-trips depends on Cassandra's
Paxos variant, and the transactions parallelize with client upload concurrency
subject to the SeafHTTP worker slots retained while callbacks wait on the
process-local metadata permit.

**Correction (2026-07-21): this is NOT specific to block upload.** An earlier
revision of this section said "the legacy resumable path pays none of them". That is
false. `finalizeUploadStreaming` splits a resumable upload into `uploadBlockSize`
(8 MB) blocks and calls `registerUploadedBlockAndMappingForUploadFn` →
`RegisterUploadedBlock` → `UpsertBlockMetadata` for each one, so a 1 GB resumable
upload pays the same ~128 LWTs. Both governed upload modes carry the identical cost.
The F8 no-session branch used to skip registration entirely; PR-7 removed it, so
there is no longer a supported public upload path that stores block bytes without
metadata.

That changes what this finding means. It is **not** a reason block upload might lose
to resumable — neither is cheaper. It is a general per-block cost on every upload
surface that registers metadata, and removing it improves all of them at once, which
strengthens rather than weakens the case for doing it.

**Scope correction (2026-08-11).** “Every upload surface registers metadata” does not
mean every logical block reaches registration on every request. Browser and sync
preflight can classify complete deduplicated blocks before `RegisterUploadedBlock`;
those bypassed blocks pay no metadata LWT. New content and every block invocation that
does reach metadata materialization still pay it. Thus ~128 LWTs/1 GiB is a
new-content/full-registration sensitivity at 8 MiB blocks, not a universal per-file
charge. The X4 probe fast path can remove a non-applying LWT for an existing complete
row, but cannot help first content, a first-writer race, or rematerialization.

The LWT is scoped to the `(org_id, block_id)` partition, so writers of *different*
blocks never contend. The cost is latency per block, not lock contention.

### The three lifecycle observations (not redundant reads)

Independently of Paxos, PR-5's successful cycle has three ordered observation points:

1. `ProbeBlockReuse` classifies the block before storage.
2. `BlockDeleteFenceActive` checks the fence after the provisional reference is durable.
3. The confirmation store callback classifies again after materialization and verifies
   or repairs the canonical object.

The first two observations form the mutual-exclusion protocol: they deliberately
observe different moments, and the second authorizes publication. The third closes
the fast-clear window where GC deleted the object and cleared its fence before the
materializer could observe it. Reusing or caching an earlier observation for either
later step would reintroduce F1.

For a new web-session block, these lifecycle checks account for 7 point reads; the
full cycle is 11 point reads plus one metadata LWT and its writes, as itemized above.
The provisional reference is an ordinary write; expiry and its by-day projection use
a separate logged batch. PR-2's explicit `RepairableStub` decision closed X7 without
adding an unconditional stub-repair LWT to every new block.

### Proposed fixes and current decision boundary

The options below are historical candidates from the performance investigation.
The current reference model remains mutable library preference plus the
org-global `(org_id, block_id)` canonical row. No item below authorizes removing
the first-writer LWT without a separately approved identity, locator and GC
liveness design.

1. **Do not remove the Paxos as a mechanical performance fix.** A deterministic
   class derived per `(org_id, block_id)` is Option C, not the current product
   contract: it removes library placement authority and requires a versioned
   placement function, stable class membership, existing-row compatibility and
   migration rules. Option B changes the deduplication identity instead. Either
   option must be approved separately before the LWT can be reconsidered.

   The detailed comparison of library-fixed, class-scoped and hash-home placement,
   including the B schema and GC implications, is in
   [STORAGE-CLASS-PLACEMENT-OPTIONS.md](./STORAGE-CLASS-PLACEMENT-OPTIONS.md).
2. ~~**Collapse the pre-store probe and post-reference fence reads into one**
   request-scoped fetch.~~ **Withdrawn — this would reintroduce F1.** These first two
   observations are not duplicated work; they occur at different times, and the
   second one is load-bearing:

   ```
   ProbeBlockReuse        read #1  — before the PUT, decides whether to store
   ...PUT...
   AddBlockReference      write    — publish our flag
   BlockDeleteFenceActive read #2  — AFTER the reference, decides whether to publish
   UpsertBlockMetadata    write
   ProbeBlockReuse        read #3  — confirmation after materialization
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
   taken after the provisional reference is durable**. PR-5's later confirmation is
   independently required: it closes the fast-clear window by reclassifying and
   verifying/repairing the canonical object after materialization. X7's separate
   unconditional stub-repair read was avoided by PR-2's explicit `RepairableStub`
   state; it is not this confirmation observation.
3. **Measure before choosing.** Instrument the latency of that specific INSERT so the
   real production number replaces this estimate. `block_upload_materialization_retries_total`
   already exists; per-statement latency does not.

Do **not** attempt to fix production by switching the statement to `LOCAL_SERIAL`: with
per-DC first writers that lets two DCs each "win" locally and diverge on placement,
which is the exact corruption the LWT exists to prevent.

The dedicated `config-usa.cluster.yaml` and `config-eu.cluster.yaml` profiles do use
`LOCAL_SERIAL`, but they are test/development harnesses rather than production
configuration. Their inline "multi-DC standard" wording describes that harness, not
the production safety contract. Consequently, the harness does not reproduce
production cross-DC `SERIAL` and cannot validate this part of P-4.

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
| P-4 | 1 `SERIAL` LWT/Paxos transaction per metadata-registering governed upload invocation when the effective setting is `SERIAL` (F8 no-session exception removed in PR-7). Browser/sync dedup preflight may bypass registration for an individual block. (The pre-store and post-reference `blocks` reads are required observation points, and PR-5 adds a required post-materialization confirmation; none is part of the Paxos optimization.) | ✅ Confirmed | HIGH | Pending (pre-existing, not from the fence branch) |
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
| 2 | P-2 | Cassandra-first probe; direct PUT without a pre-PUT HEAD | Eliminates the legacy pre-PUT existence HEAD; PR-5's required post-materialization confirmation remains — **done** (`perf/p2-cassandra-first-hot-reuse`) |
| 3 | S-1 | Sticky sessions at LB (immediate) or distributed chunk state (complete) | Required for multi-node topology |
| 4 | S-2/S-3 | Roll out a real `chunked_staging_max_bytes` value per node | Operational hardening follow-through |
| 5 | S-4 | Atomic quota reservation at upload start | Closes the concurrent over-quota window |
| 6 | P-4 | Measure the metadata LWT and characterize A-prime versus the explicit B/C alternatives. **Preserve the fresh post-reference fence read** — do not merge it with the pre-PUT probe. | The LWT currently arbitrates the global canonical class. Do not remove it until an approved alternative proves identity, locator, repair and GC safety. |
