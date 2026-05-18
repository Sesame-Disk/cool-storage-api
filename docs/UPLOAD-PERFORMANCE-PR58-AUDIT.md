# Upload Performance PR58 Audit

Status: research only. Do not merge PR58 as-is.

This document preserves the useful conclusions from PR58 after the branch was
rejected as a production path. The value of that branch was not the exact code;
it was the evidence it produced about upload finalization, metadata safety, and
the limits of fixed retry counts.

## Summary

PR58 started from a real user-visible symptom: browser uploads reached 100% and
then appeared to hang while the API still had to materialize blocks and publish
metadata. The branch also exposed the deeper issue behind many finalize
failures: upload finalization is not only an object-store throughput problem. It
is also a library metadata serialization problem.

The branch mixed too many concerns in one implementation:

- latency optimization
- upload staging state
- conflict retry logic
- projection repair
- cleanup and recovery
- quota and accounting changes

That made it valuable as research, but not acceptable as a merge candidate.

## What Was Worth Keeping

- The bottleneck analysis was correct: the API behaves like a store-and-forward
  relay, so "100% uploaded" is not the same as "file is durable and visible".
- Chunked quota prechecks must use the declared total size from
  `Content-Range`, not per-request body size.
- Per-attempt upload identity matters for retry, janitor cleanup, and upload
  link reuse.
- Canonical HEAD publication must not be an unconditional overwrite.
- Projection drift is real once canonical state and derived read models are
  updated separately.
- The useful tests are characterization and regression tests, especially around
  concurrent finalization, chunk ordering, upload-link reuse, and quota
  contracts.

## What Was Not Worth Keeping

- Fixed retry counts as a correctness mechanism. They reduce failure frequency
  but do not guarantee progress under bursty contention.
- A large staging and promotion model tied to transient commit IDs.
- Rolling promoted blocks back on HEAD conflict.
- Bundling projection repair, performance work, and upload recovery in one PR.
- Treating low-concurrency passing tests as evidence for real multi-file upload
  dialog behavior.

## Main Technical Lesson

Upload bytes can be parallelized, but accepted metadata changes for a single
library need a deterministic publication model. In practice that means one of
these two shapes:

1. short canonical compare-and-set publication on the library row, with rebuild
   and retry around the whole mutation
2. a durable metadata pipeline that can rebase and publish exactly once after
   crashes or lost responses

PR58 did not provide that invariant end-to-end.

## Salvage Decision

The new implementation track should keep only selected artifacts from PR58:

- bottleneck analysis as design input
- audit conclusions as branch history
- characterization and contract tests that match current behavior

It should not import PR58 implementation wholesale.

## Recommended Path From Main

- Keep canonical `libraries.head_commit_id` as the source of truth.
- Use compare-and-set publication only on the canonical library row.
- Treat `libraries_by_id` and admin projections as derived state that must be
  resynced from canonical data.
- Rebuild metadata from a fresh head on conflict instead of surfacing avoidable
  user-visible upload failures.
- Reintroduce performance work only after correctness is proven under
  contention.

## Current Outcome

The main-based branch follows the safer subset of these conclusions:

- canonical HEAD compare-and-set publication
- canonical HEAD readers for functional behavior
- server-side retry and rebuild for upload metadata publication
- regression coverage for concurrent upload commit loss

Further recovery, staging, or projection repair work should be added as smaller,
separate slices.