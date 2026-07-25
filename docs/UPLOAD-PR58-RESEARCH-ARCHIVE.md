# Upload PR58 — Research Archive

**Status:** research archive. PR58 was rejected as a production path and must not
be merged as-is. This file preserves what that branch proved.

**Consolidated 2026-07-25** from `UPLOAD-PERFORMANCE-PR58-AUDIT.md` and
`UPLOAD-S3-RELAY-BOTTLENECK.md`, which were two halves of the same PR58 research
with the same status and overlapping conclusions. No unique design conclusion
was intentionally dropped; narrative was merged and the archival "Current
Outcome" section from the PR58 audit is restored below. The 2026-06 updates from
the relay note are kept as the "What has since landed" section.

**Live status of anything here lives elsewhere:** current findings are in
[UPLOAD-PERFORMANCE-SECURITY-2026-06.md](./UPLOAD-PERFORMANCE-SECURITY-2026-06.md)
and [KNOWN_ISSUES.md](./KNOWN_ISSUES.md); see
[OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md) for what is still open in scope. This document
is history and design input, not a tracker.

---

## Why PR58 was rejected

PR58 started from a real user-visible symptom: browser uploads reached 100% and
then appeared to hang while the API still had to materialize blocks and publish
metadata. The branch also exposed the deeper issue behind many finalize
failures: upload finalization is not only an object-store throughput problem. It
is also a library metadata serialization problem.

The branch mixed too many concerns in one implementation — latency optimization,
upload staging state, conflict retry logic, projection repair, cleanup and
recovery, quota and accounting changes. That made it valuable as research, but
not acceptable as a merge candidate.

## The relay bottleneck (the correct half)

For chunked web uploads the browser finishes sending bytes before the API has
finished its own work. The server still needs to read the assembled temp file or
completed chunk set, hash and optionally encrypt blocks, push blocks to object
storage, write mapping and block metadata, and publish the new library HEAD and
derived state.

So user-visible wall time is not just browser-to-API transfer. The progress bar
reflects ingress into the API node; the final response is delayed by post-upload
work. In the old architecture that made the API node a store-and-forward relay —
ingress (browser → API temp file), egress (API temp file → object storage),
publish (metadata + HEAD). When ingress and egress have similar effective
bandwidth, total wait is close to their **sum** rather than their maximum.

What this still supports:

- The UI should distinguish *uploading* from *finalizing*.
- Backend performance work should overlap object-store work with ingress where
  practical.
- Large uploads need characterization on the final chunk response time, not the
  transfer progress bar.
- Non-chunked and v2 upload paths deserve the same scrutiny — they can hide
  slower whole-file paths.

What it does **not** prove: that throughput optimization alone fixes upload
failures. The main reliability issue the audit exposed was metadata contention —
many finalizers racing on the same stale HEAD even if block materialization gets
faster.

## What was worth keeping from the branch

- The bottleneck analysis above: "100% uploaded" ≠ "durable and visible".
- Chunked quota prechecks must use the declared total size from `Content-Range`,
  not per-request body size.
- Per-attempt upload identity matters for retry, janitor cleanup, and upload-link
  reuse.
- Canonical HEAD publication must not be an unconditional overwrite.
- Projection drift is real once canonical state and derived read models are
  updated separately.
- The useful tests are characterization and regression tests, especially around
  concurrent finalization, chunk ordering, upload-link reuse, and quota
  contracts.

## What was not worth keeping

- Fixed retry counts as a correctness mechanism. They reduce failure frequency
  but do not guarantee progress under bursty contention.
- A large staging and promotion model tied to transient commit IDs.
- Rolling promoted blocks back on HEAD conflict.
- Bundling projection repair, performance work, and upload recovery in one PR.
- Treating low-concurrency passing tests as evidence for real multi-file upload
  dialog behavior.

## Main technical lesson

Upload bytes can be parallelized, but accepted metadata changes for a single
library need a deterministic publication model. In practice that means one of
two shapes:

1. short canonical compare-and-set publication on the library row, with rebuild
   and retry around the whole mutation; or
2. a durable metadata pipeline that can rebase and publish exactly once after
   crashes or lost responses.

PR58 did not provide that invariant end-to-end.

## Design constraint carried forward

Any optimization must remain backend-agnostic. The system supports multiple
object-store backends, so a fix cannot depend on provider-specific presigned
upload semantics or a single cloud vendor feature.

Treat upload performance and upload correctness as separate concerns: first make
metadata publication deterministic under contention, then reduce residual
finalize latency without changing correctness.

## Recommended path from main (as written at the time)

- Keep canonical `libraries.head_commit_id` as the source of truth.
- Use compare-and-set publication only on the canonical library row.
- Treat `libraries_by_id` and admin projections as derived state that must be
  resynced from canonical data.
- Rebuild metadata from a fresh head on conflict instead of surfacing avoidable
  user-visible upload failures.
- Reintroduce performance work only after correctness is proven under
  contention.

## Outcome at the time of archival (from PR58 audit)

The main-based branch follows the safer subset of these conclusions:

- canonical HEAD compare-and-set publication
- canonical HEAD readers for functional behavior
- server-side retry and rebuild for upload metadata publication
- regression coverage for concurrent upload commit loss

Further recovery, staging, or projection repair work should be added as smaller,
separate slices.

## What has since landed

**2026-06 — the permit bottleneck.** The 2026-06 audit found that the
finalization concurrency permit (`finalizeUploadBlockMetadataConcurrency = 1`)
was acquired *before* `retrySeafHTTPBlockMaterialization`, serializing not just
the Cassandra LWT but the S3 Exists+PUT for every block across the whole process
— the 8-worker pool had no effect. Fixed in `fix/upload-permit-unwrap-s3-put`
(2026-06-15): the permit now wraps only the Cassandra LWT.

**2026-06 — the double S3 round-trip.** Exists+PUT per block was the next relay
cost, resolved in `perf/p2-cassandra-first-hot-reuse` (P-2 in the audit doc): the
pre-PUT S3 HEAD is replaced by a Cassandra `ProbeBlockReuse` deciding
reuse / direct-PUT / GC-fence before any S3 call. PR-5 of the upload-fence series
later added one *post-reference* canonical HEAD after successful materialization
to close the GC fast-clear data-loss window; the pre-PUT HEAD stays removed.

**2026-06 — generic multipart.** `internal/storage/s3.go` `PutLarge` uploads
multipart parts through a bounded worker pool instead of a strict read/upload
loop. Real improvement for any caller reaching the generic `PutAuto()` multipart
path above the 100 MB threshold — but it does **not** remove the dominant relay
cost for the default SeafHTTP web-upload path, which still splits into 8 MB
blocks and stages the complete upload under `/tmp` first, so the generic
multipart path is usually not involved there.

**Still open.** The store-and-forward staging model itself (full `/tmp` buffering
before blocks reach S3) and node-local chunk state — see S-1/S-3 in the 2026-06
audit, `ISSUE-UPLOAD-CHUNK-MULTINODE-01`, and
`ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01`.
