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

The remaining relay cost is the double S3 round-trip per block (Exists + PUT) in
`internal/storage/blocks.go`. That is the next performance target (P-2 in the
audit doc).