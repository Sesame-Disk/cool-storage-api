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

## Disposition after baseline reproduction

The three-API baseline is not GREEN: GC passes 3/3; SeafHTTP fails 3/3;
V2 fails 2/3. The second, temporary diagnostic run preserves assertions and
adds only container-local error logging: SeafHTTP passes 2/3, V2 fails 3/3.
It captures native CAS WriteTimeout (SERIAL, Received=1, BlockFor=2) on
SeafHTTP lease acquisition and V2 HEAD publication. A V2 confirmation read
also fails and correctly preserves ErrLibraryHeadPublicationUnknown.
The same-repo move in the first run independently times out at HEAD CAS.

The safe bounded implementation is server-side diagnostics at the two generic
upload error writers. Preserve the original client status/body and publication,
lease, rollback, retry and timeout semantics. This corrects the reproduced
missing-error observability defect; it does not fix the availability failures.
A generic retry or conversion of uncertain publication into a conflict requires
its own contract/regression and is not added here. The intermittent issue stays
OPEN; subsequent failed functional runs are not relabeled PASS. Existing HTTP
error-map tests and real three-API requests verify response compatibility and
the newly retained diagnostic; no mirror-only log-string unit test is added.

## Accepted results and audit (2026-10-16)

| Check / source | GC sibling retirement | SeafHTTP upload + move | Two-region V2 upload | Package exit |
|---|---:|---:|---:|---:|
| Untouched main baseline, count=3 | 3 PASS | 3 FAIL | 1 PASS / 2 FAIL | 1 |
| Temporary diagnostic only, count=3 | not selected | 2 PASS / 1 FAIL | 3 FAIL | 1 |
| Final two-line logging change, APIs and driver with race, count=3 | 3 PASS | 3 FAIL | 1 PASS / 2 FAIL | 1 |

Every selected name has three RUN and completion events; no SKIP, build failure
or test timeout is accepted. No race report appears in the final driver or any
of the three final APIs. These functional failures remain failures. The diagnostic
phase has different timing and no statistically meaningful rate comparison is
claimed. A final log also records a library-deleted HEAD read; that line is not
attributed to the same CAS cause or promoted into a separately proved defect.

The two runtime source changes add one log line in each generic 500 branch:
writeSeafHTTPUploadError and writeUploadFileError. Known 409/403 branches, JSON
bodies, headers, counters, leases, rollback, publication outcome classification,
retry budgets, Cassandra configuration and standard-suite timeouts are unchanged.
Client responses continue to hide the internal error. An uncertain CAS is not
reclassified as a known conflict; no availability fix or E1 closure is claimed.

Docker checks on final source:

- API and API v2 package short suites PASS; same packages with race PASS.
- Existing sentinel/unknown-error HTTP response-map tests PASS, retaining exact
  status/body expectations. Their generic-error controls also emit the new
  server-side cause; real concurrent HTTP failures emit lease/HEAD details.
- Targeted vet PASS. No log-string-only test, new guard, new API or dependency.
- The temporary full error-chain diagnostics were removed before final binaries
  were built. Only the reviewed two log lines remain in productive Go sources.

Three distinct containers have separate host PIDs and addresses:
sesamefs-residual-na, sesamefs-residual-eu and sesamefs-residual-asia, all port 8080
on the dedicated Docker network. Each uses its local Cassandra contact/DC and
validated dc-na:1,dc-eu:1,dc-asia:1 declaration. Cassandra is 5.0.9; S3 is the
retained pinned SILO fixture, shared by all three API processes. Endpoint/auth
probes and GC status succeed on every distinct URL; all APIs report GC disabled
and last_worker_run=never. This is not a single daemon aliased three times.

The GC test uses only ProcessOrgOnce on its unique synthetic org. It has no
physical-delete backend and creates no S3 object for its synthetic hot-class key;
its PASS proves the existing G2/G3 assertions, including empty queue, preserved
encrypted sibling and COMMITTED handoff, not physical deletion/recovery.

Six exact authorities (three baseline, three final) remained after original GC
fixture cleanup. Ownership is matched to the block/claim retirement log from
that exact named test. A disposable utility requires absent blocks/referrers,
absent key in the S3 fixture, SERIAL PUBLISHED lifecycle matching claim/time/P,
and exact COMMITTED orphan/root. It calls DeleteS3Orphan with the durable token,
then deletes only the same owned lifecycle key. Per-fixture EACH_QUORUM counts
verify lifecycle/orphan/root/discovery absence. This is post-run fixture disposal,
not a protocol test or automatic cleanup fix. The first utility attempt refused
at a UUID binding error before any mutation; string binding was corrected and
all six exact disposals passed. The failure log is retained separately.

Final independent EACH_QUORUM counts: orphan/root/discovery = 0; repairs = 5.
The five repair rows' repo IDs occur in the current test API logs and are retained
because publication outcomes and repair convergence are not resolved here. There
is no global cleanup or forced success. All 73 prior S3 keys remain present;
198 uploaded objects are added, for 271 total. Ordinary test-library soft deletion
retains history/bytes with GC OFF. No checksum certificate or physical purge of
these upload fixtures is claimed. APIs are stopped before the final frozen DB
snapshot; the dedicated stack is stopped with volumes retained. One-DC fixture
and unrelated application containers are outside this work.

Provenance: main@331166aa5dee13286445c773b22f085499b0dea9, plan bedb3f1d7.
Baseline image sha256:eb5e425389713d6062fc4c988a71cba10e8c03710a1905477f3a264c26fdf870;
final-source image sha256:f15c18e79d33b65e925d811e5eff1927acfafc21848a16165bd06cc5617217ee.
Initial binary SHA256 agrees across all three nodes:
b871c757f8a1ee378d45fb355ac2cbd911a38bd31dec983ea2f89255405ebdef.
Final race binary SHA256 also agrees across nodes:
4f4408fb3d09cb34aa20efb4879078b7c622e1104b9f6296e88cb6830125adcd.
Local ignored artifacts: tmp/residual-* JSON events, exits, API logs, source
manifests, repair and object snapshots; tmp/residual_dispose_test.go is disposable.
They are not independent CI review or committed raw-log evidence.

Reproduce inside the isolated Docker test container after verifying the three
separate API endpoints, local-DC configs, auth and GC-OFF status:

```bash
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
go test -json -tags integration -count=3 -timeout 12m \
  -run '^(TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles|TestConcurrentTwoRegionV2UploadsNoLostCommits|TestGC_WorkerRetiresCanonicalRowAndPreservesPlainEncryptedSiblings)$' \
  ./internal/integration
# Final validation adds -race; the final API binaries are also built with -race.
```

Not run: full integration/API/OIDC suites, special identity/mapping script,
client sync, partitions, TTL policy or long-term fairness/convergence. The #304
59-case union is not repeated or re-certified by these three tests. Full-suite
residuals, E1-01/D0/identity audits, X1/PRE-GC/A1 remain OPEN; ordinary GC OFF.
Backward compatible: yes, internal diagnostics only; no client response change.
