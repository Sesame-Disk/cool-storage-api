# E1 three-API residual reproduction

## Frozen plan (2026-10-16)

Base main@331166aa5dee13286445c773b22f085499b0dea9 (#305).
Scope is the three independent failures left outside the #304 59-case union:
TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles,
TestConcurrentTwoRegionV2UploadsNoLostCommits, and
TestGC_WorkerRetiresCanonicalRowAndPreservesPlainEncryptedSiblings.

1. Build current source in Docker. Start three distinct API processes/containers,
   each using its local Cassandra DC (NA/EU/Asia), one dedicated RF1 three-DC
   keyspace and S3 fixture. Verify distinct container identities/URLs, auth,
   binary provenance and GC_ENABLED=false on every API. Preserve volumes/data.
2. Execute existing tests before edits, serially with count=3, with all unrelated
   REQUIRE flags unset. Reject SKIP and verify each exact name runs three times.
   Keep per-API logs and original assertions; setup failures are not product RED.
3. Diagnose each reproduced failure against current code. The GC test may run
   only its already-owned synthetic org/block worker, never global fan-out.
   Capture exact owned retirement metadata for guarded post-run disposal when
   original fixture cleanup does not remove it; do not erase unrelated data.
4. A runtime fix requires a reproducible public-handler/package regression RED
   before GREEN, real Cassandra/S3 verification and relevant race/compatibility
   checks. Keep this PR to one demonstrated cause; independent defects stay in
   follow-up tracking with their severity. Do not invent a fix for a clean run.
5. If SeafHTTP fails with an opaque 500, identify the finalize error through a
   minimal diagnostic experiment before choosing a fix; never relabel missing
   evidence as PASS. No full-suite, partition, TTL-policy or client certification.
6. Audit source diff, fixture ownership, logs, named execution, cleanup and any
   required negative control. Refresh tracking/history and main, open a small PR.

This advances E1 residual reconciliation, not X1/PRE-GC/A1 closure. The three
historical results remain dated evidence even if current runs pass. No shared
or production GC activation, schema/API break or standard timeout-budget raise.
Backward compatible: yes unless a separately assessed real fix proves otherwise.
