# Scoped E1-10e / E1-17 owned teardown plan

Base: main@d4e375cfbe2e80e5380b2506daf79109f0e22e76 (#302).

Problem: COMMITTED fixtures delete their exact object and block/lifecycle rows,
but retain canonical orphan, independent recovery root and discovery projection.
This is P2 FOLLOW-UP / GENERAL test hygiene, not a productive data-loss finding.

1. Add a teardown regression to the existing E1-10e (four) and E1-17 (fifteen)
   public-handler legs. Require exact owned block/refs/lifecycle/K and recovery
   metadata absence after original cleanup. Reproduce six COMMITTED failures.
2. Correct only these two fixture families. Capture the actual returned delete
   authority; preserve all original COMMITTED/admission assertions. After original
   teardown and verified block/refs/lifecycle/K absence, dispose only that exact
   authority through existing CassandraStore APIs, then verify all three recovery
   surfaces are absent at EACH_QUORUM. Use a bounded background object context:
   testing.T.Context is already canceled during Cleanup. No global helper,
   productive worker, schema, protocol or configuration changes.
3. Run Docker RED/GREEN against isolated one-DC and three-DC Cassandra/S3;
   repeat with race, retain required gates and admission controls. Explicitly
   distinguish regression failures from setup failures. Preserve unrelated rows.
4. Audit ownership, LIFO cleanup ordering, early-failure behavior, unchanged
   functional assertions, source diff and tracking. Update from main, open a
   focused PR, and report checks actually run and skipped.

Automatic cleanup is fixture disposal, not productive recovery evidence. Older
retained authorities and auxiliary discovery probes remain outside this scope.
No combined/full-suite PASS or X1/PRE-GC/A1 closure; ordinary GC stays OFF.

Backward compatible: yes, test-only teardown; public behavior is unchanged.

## Execution and audit (2026-10-15, local date)

The implementation changes only the two fixture callbacks and a scoped helper.
Callbacks retain their original bounded background S3 context. Object deletion
or existence-check failure returns without disposing recovery metadata. The
helper requires EACH_QUORUM block/reference/lifecycle absence, reads the exact
captured authority, and calls existing DeleteS3Orphan with its durable first-seen
token. A terminal fixture with no remaining orphan/root needs no delete. Final
EACH_QUORUM counts require orphan/root/discovery absence for the owned block.
Missing authority never authorizes deletion; residuals are reported as failures.

All original handler calls, COMMITTED assertions, named legs, required gates,
parent/child deadlines and productive sources remain unchanged. x1Cleanup and
other consumers are untouched. Cleanup certificates certify disposal only;
they are emitted even on a failing functional leg and cannot turn it into PASS.

| Docker check | Result |
|---|---|
| Regression only, one DC | Six COMMITTED legs FAIL; thirteen other legs PASS |
| Regression only, three DCs | Same six failures; each leaves one orphan/root/projection |
| Corrected one DC | 19/19 handler legs and 19 cleanup certificates PASS |
| Corrected three DCs | 19/19 handler legs and 19 cleanup certificates PASS |
| Three-DC race, real isolated parent/child, count=2 | 38/38 child handler legs and cleanup certificates PASS; no race report |

Regression images add only post-teardown absence assertions to the original
callbacks. Both RED runs exit 1 with eighteen recovery-row assertion failures,
not build/setup failures. Original block/refs/lifecycle/K checks do not fail.
The correction uses the same absence contract; no assertion is weakened.

The one-DC run preserves all 26 retained authorities (20 older, six RED) across
all three recovery surfaces, compared as complete sorted JSON rows before/after
GREEN. The six owned RED authorities are subsequently disposed by the existing
guarded post-run utility: exact execution-log ownership plus absent
block/refs/lifecycle/K are required. The twenty older authorities remain.
No manual disposal is needed for corrected functional or race fixtures.

Not run: full integration/API/OIDC suite, other E1 matrices, full Go unit suite,
frontend/desktop/mobile suites, partitions or TTL-policy experiments. This is
neither combined required-suite evidence nor X1/PRE-GC/A1 closure. Ordinary GC
remains OFF. Retained auxiliary discovery probes are outside this fix.

Additional checks: the restored-source EU-coordinator matrix passes 19/19 after
the omission experiment. Six gate negatives (filtered direct/child and unavailable
API, for each family) exit 1 at the expected gate. Two admission omissions in a
disposable container disable only RevertDirents capture/recheck: three E1-10e
file legs and three E1-17 directory legs each publish unsafe HEAD and fail at the
original behavioral assertion. All six still emit cleanup certificates. These
controls prove failure-path disposal without weakening admission detection;
they are not positive protocol evidence. Productive source is restored and
compared byte-for-byte before the final EU run.

All six three-DC RED authorities remain byte-identical across orphan/root/
discovery through GREEN, race, negatives and EU (18 sorted JSON rows). Only
then does the guarded post-run utility dispose those six owned RED authorities;
final EACH_QUORUM recovery-table counts are zero. One-DC final comparison
preserves all 60 rows belonging to the twenty older authorities. No corrected
fixture needs manual disposal. The three-DC bucket retains 63 auxiliary discovery
probes (50 prior, 13 created here), classified by their gc-test-discover.txt payload;
this is not a claim that the bucket or keyspace is empty. API status still reports
GC enabled=false and worker last_run=never. Dedicated stacks are stopped after
verification, with volumes retained.

Provenance: frozen plan commit fb0941eed; RED image
`sha256:8737fc9516d5a65a0d5f7794be933de7cb293c98ba6a682e78e0a086b8d67cd0`;
GREEN image
`sha256:9bfe99ada78466250d6b1e7f3ae39c015fe6680dac78503a22de9d9f6552f35c`.
Host and restored-container SHA256 agree for the helper
`df425e82f2109ffbe93f2197fa587d9457628771ab9fb28dfc45b10ff910c217`,
E1-10e `62832a2d0cc97ebe6bea105168d33e998b0fc51e61d8777f610ce5b04c9d0dd3`
and E1-17 `6cc1068cb40fc63c860dce6fc0e147e767e13d0eadd58c805976b0bd62679140`.
Logs/exits and snapshots are local ignored artifacts under `tmp/owned-*`;
three-DC RED disposal uses `tmp/e110e-3dc-owned-red-*`. Images contain the
corresponding assertion-only/fixed sources; this is not an independent CI review.

Reproduction inside the isolated Docker test container, with Cassandra/S3/API
endpoints configured and ordinary GC disabled:

```bash
SESAMEFS_REQUIRE_E110E_UNPUBLISHED_RESURRECTION_EVIDENCE=1 \
SESAMEFS_REQUIRE_E117_RESIDUAL_RESURRECTION_EVIDENCE=1 \
go test -tags integration -v -count=1 -timeout 12m \
  -run '^(TestUnpublishedResurrectionSourceAdmission|TestE117ResidualResurrectionSourceAdmission)$' \
  ./internal/integration
```

The race run adds `-race -count=2` and sets both existing isolated-URL variables
to the fixture API, preserving the original child deadlines. Completeness,
evidence-gate inventory and no-global-GC-fanout guards also pass. No positive
run contains SKIP or FAIL. Link/format/diff checks complement the behavioral
checks; no unsupported production-readiness claim follows.

Final audit: 436 relative file links resolve; changed Go files are gofmt-clean;
diff whitespace checks pass. Main was fetched again and remains d4e375cfb,
so no rebase or published-history rewrite was needed. No local audit blocker
was identified. An independent CI review is not claimed.
