# Upload S3 Relay Bottleneck

Status: preserved analysis from PR58 research.

This note keeps the useful architectural conclusion behind the old upload
performance branch: web uploads do not become durable when the browser reaches
100%. The final request can still be waiting on server-side block materialization
and metadata publication.

## Core Observation

For chunked web uploads, the browser finishes sending bytes before the API has
finished its own work. The server still needs to:

- read the assembled temp file or completed chunk set
- hash and optionally encrypt blocks
- push blocks to object storage
- write mapping and block metadata
- publish the new library HEAD and derived state

That means the user-visible wall time is not just browser-to-API transfer.

## Why The Progress Bar Lies

The browser progress bar usually reflects ingress into the API node. The final
server response is delayed by post-upload work. In the old architecture that
made the API node a store-and-forward relay:

- ingress: browser to API temp file
- egress: API temp file to object storage
- publish: API writes metadata and advances HEAD

When ingress and egress have similar effective bandwidth, total wait time is
close to their sum instead of their maximum.

## What This Analysis Still Supports

- The UI should distinguish uploading from finalizing.
- Backend performance work should overlap object-store work with ingress when
  practical.
- Large uploads need characterization on the final chunk response time, not
  just the transfer progress bar.
- Non-chunked and v2 upload paths deserve the same scrutiny because they can
  hide slower whole-file paths.

## What This Analysis Does Not Prove

It does not prove that throughput optimization alone fixes upload failures.
The main reliability issue exposed during the PR58 audit was metadata
contention: many finalizers can race on the same stale HEAD even if block
materialization becomes faster.

## Design Constraint Kept On The New Branch

Any optimization should remain backend-agnostic. The system already supports
multiple object-store backends, so the fix cannot depend on provider-specific
presigned upload semantics or a single cloud vendor feature.

## Practical Takeaway

Treat upload performance and upload correctness as separate concerns:

- first, make metadata publication deterministic under contention
- then, reduce residual finalize latency without changing correctness

That separation is why the current main-based branch kept the canonical HEAD
publication fixes and deferred broader performance work.

## 2026-06 Update: Permit Bottleneck Identified And Fixed

A 2026-06 audit (`docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md`) identified that
the finalization concurrency permit (`finalizeUploadBlockMetadataConcurrency = 1`)
was acquired before `retrySeafHTTPBlockMaterialization`, serializing not just the
Cassandra LWT but also the S3 Exists+PUT for every block across the entire process.
The 8-worker pool had no effect.

Fixed in branch `fix/upload-permit-unwrap-s3-put` (2026-06-15): permit now wraps
only the Cassandra LWT; S3 operations run in parallel across all 8 workers.

The double S3 round-trip per block (Exists + PUT) was the next relay cost. It was
resolved in branch `perf/p2-cassandra-first-hot-reuse` (2026-06-15, P-2 in the
audit doc): the pre-PUT S3 HEAD is replaced by a Cassandra `ProbeBlockReuse` that
decides reuse / direct-PUT / GC-fence before any S3 call. Reusable blocks now touch
S3 zero times; new blocks pay one direct PUT and no HEAD.

The remaining relay concern is the store-and-forward staging itself (full `/tmp`
buffering before blocks reach S3) and node-local chunk state — see S-1/S-3 in the
audit doc.

## 2026-06 Update: Generic multipart uploads no longer serialize parts

`internal/storage/s3.go` `PutLarge` now uploads multipart parts through a bounded
worker pool instead of sending each part in a strict read/upload loop. This is a
real improvement for any caller that reaches the generic `PutAuto()` multipart
path above the 100 MB threshold.

Important limit: this does not remove the dominant relay cost for the default
SeafHTTP web-upload path. Web finalization still splits uploads into 8 MB blocks
and stages the complete upload under `/tmp` before those blocks are materialized,
so the generic multipart path is usually not involved there.

So the practical reading is:

- generic large-object multipart uploads are better than before
- the main web-upload finalize bottlenecks are still the node-local chunk state
  and the full temp-file staging model
