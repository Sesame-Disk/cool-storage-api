# E1-01F — orphan-absent/discovery-present TERMINAL process crash

Frozen plan: 2026-10-14, base `main@2b282a07a` (#290 merged).
Advances #201 E1-01, one internal C8 cut in the current DeleteS3Orphan ordering.
E1-01/X1 remain OPEN; ordinary/production GC OFF. No gate closes here.

- Reuse productive upload, owned reference lapse and separate ProcessOrgOnce
  preparation with real claim, zero-liveness read, G2 handoff and retirement.
- Use existing NewWithObservers in the physical recovery child. Match successful
  DELETE FROM gc_s3_orphans on the exact org/L/P/D six-argument tuple; pause its
  real caller before discovery DELETE. No runtime hook or direct state fabrication.
- Parent independently proves exact TERMINAL, orphan/canonical/K1 absent,
  discovery present at original day/bucket/first_seen/P/D, retained exact root
  and token, settled bookkeeping, unchanged HEAD and zero refs/repairs.
- Require OS SIGKILL and a same-boundary no-kill control. Fresh-process recovery
  and another independent replay must remove discovery/root without restoring
  K1, canonical placement or changing TERMINAL/HEAD. Retain discovery coordinates
  for post-root-removal checks; do not accept completion markers alone.
- Preserve the full boundary snapshot on failure. A fresh process/session must
  compare live state after failed teardown before explicit owned disposal.
- Separate mandatory two-leg gate: reset per invocation; parent cleanup registered
  before early SKIP; credit after fixture teardown; TestMain and both Compose
  runners fail closed. Verify omitted leg, unavailable backend, early SKIP in both
  repetition orders and unavailable recovery child on disposable source copies.

Scope: one test-only cut, minimal shared harness/gate wiring and evidence docs.
No runtime/schema/API/dependency/default change. A reproduced product defect
requires a separate bounded regression/fix PR. C0/C3 and three-DC residuals remain
separate; no all-C8 or X1 closure is inferred from this one observation.

Validation in Docker: directed F/preservation plus static guards; race repetitions;
negative gate/recovery probes and post-failure disposal; short Go and normal plus
integration vet; standard required integration/API/OIDC including A/B/C/D/E/F.
Record actual exits, immutable image/source hashes, topology, durable pre-kill
state and recovery/replay. Audit final diff, links, gates, compatibility and main
before publication. Backward compatible: yes, test/evidence only.

## Validation and audit ledger

Frozen implementation: `df8852b3d4c8fa1a5541106dca917fb6de87df1c`.
Runner image for directed/race/negatives/standard:
`sha256:8ff86018a2b1a2d7001fc9204a25b1816b92412292c9ca3ccab5b716886de19c`.
Source SHA-256 inside the image (matches final Go files, normalized LF):
- F cut/observer: `0df34d1bcf7b4b06547f091403b6fc86cced7ab311be1023450368b5076df7e4`.
- Shared harness: `be77ff4f57a456c73e25321ce5f785a042523a537d026427862997f186c0f12c`.
- Physical actor/preparation: `da923d3e524777a24c8c99d1bb8f6ddce38890469304459a0a77549d77bbe5b2`.
- Terminal classification: `5c0ffb280a9b6feaf3574e27364722ad0ec3800b6442de6617808e8d12213a14`.
- TestMain: `6fa83c75b426586a16d2e7be634af6b349003d95f2fd0f2f0256dbedeaf714d5`.

Linux Docker, real Cassandra 5.0.9, datacenter1/RF1 NetworkTopologyStrategy,
LOCAL_QUORUM/SERIAL configuration; SERIAL lifecycle/canonical snapshots and
EACH_QUORUM scoped metadata reads. SILO `RELEASE.2026-09-16T00-00-00Z`.
E19 backend image `sha256:a53db0b97cb82304f58973aae8d848ad67fa0789489c891b760df4f83223be4e`,
binary SHA-256 `e6facb5f64f3b3104793242cf57f4dbd1a1279d6bb377e5d6d11ec5596aec1e3`.
Productive source is unchanged. E19/node2/node3 GC OFF; previously authorized main
development GC is outside this isolated keyspace. No GC setting changed.

Both positive legs independently prove TERMINAL, exact orphan absent, exact
by-day discovery present, root and original token retained, canonical/K1 absent,
settled owned work, zero refs/repairs and unchanged HEAD before kill/resume.
Original K1 bytes are checked before productive preparation. The observer matches
only the successful six-argument exact orphan DELETE and holds the caller before
discovery DELETE; the callback is not the persisted-state oracle. Actual SIGKILL
is asserted. Fresh-process recovery and another independent replay remove the
outstanding discovery and root while preserving TERMINAL/HEAD and absent K1.
The saved original discovery coordinate is checked after the root disappears.

Local ignored `tmp/` logs and explicit `.exit` records:
- `e101f-directed.log`: F kill/resume, independent recovery/replay, failure
  preservation, gate wiring and no-global-fanout PASS (96.964s, exit 0).
- `e101f-race.log`: same F cases repeated twice with race PASS (134.540s, exit 0).
  Four verified SIGKILLs: one positive and one preservation negative per repetition.
- `e101f-unit-vet.log`: complete short Go tree and normal/integration vet PASS,
  exit 0, on the same frozen image.
- `e101f-gate-negatives.log`: omitted resume and backend unavailable both test
  exit 1; only F's gate is required. Explicit missing-resume/backend output is
  asserted. Wrapper exit 0 means the expected semantic failures were verified.
- `e101f-skip-first.log` / `e101f-skip-second.log`: injected early test SKIP in
  either repetition order is rejected, test exit 1 (36.385s / 34.961s). Each
  healthy repetition independently passes kill/resume. Disposable source only.
- `e101f-s3-negative.log`: only the recovery child's S3 endpoint is faulted in
  a disposable source copy. Actual helper SKIP/exit 0 is rejected for missing
  recovery completion; parent test exit 1 (34.054s). External JSON preserves
  TERMINAL, orphan absent, discovery present and exact root, with no snapshot
  read error or restored canonical/K1. Failed cleanup performs no automatic retry.
- `e101f-s3-negative-disposal.log`: new process/session compares the unchanged
  live snapshot after teardown BEFORE explicit recovery/owned disposal; PASS
  (2.059s, exit 0). HTTP library disposal succeeds; JSON remains byte-identical.
  The negative wrappers return 0 only after asserting intended failure/retention.
- `e101f-go-all.log`: standard runner PASS; short Go coverage, required integration
  (1720.812s), API 20/20 suites (95s), OIDC 25/25 tests (one suite, 1s).
  A/B/C/D/E/F matrices and all six preservation controls PASS, alongside the
  existing required G1/G4/G5 evidence, gate wiring and no-global-fanout guard.
  Container `e101f-go-all.exit` and outer `e101f-go-all.outer.exit` both record 0.
  The exact Compose command is enveloped only to preserve actual exit before
  --rm; its required gate list and test commands are unchanged apart from F.
  Conditional three-DC/topology/load/helper SKIPs are not positive evidence.

No product violation was reproduced. This is one internal C8 cut, one DC and
fixture-local enumeration, inherited from A/B/C/D/E. No C0/C3, P2 publication,
global scheduling/fairness/checkpoint or three-DC proof is inferred. E1-01/X1
remain OPEN; maintainer closure and PRE-GC/A1 are separate. No ordinary or
production activation is authorized. Sync/frontend and three-DC suites are not
run for this test-only slice.

Final scoped audit: no blockers identified. The observer matches the full owned
six-argument query and delegates all productive calls; independent durable reads
certify the cut. Fresh sessions check discovery after root removal, exact TERMINAL,
absent K1/canonical, settled bookkeeping and unchanged HEAD. No automatic recovery
occurs on the preserved failure; live post-teardown comparison precedes disposal.
The separate gate rejects omitted/SKIP/unavailable evidence; A/B/C/D/E behavior is
unchanged. Image/source hashes match all five Go files. Runtime/schema/config,
GC defaults and dependencies have no diff from base. Links, Compose configuration
and git diff --check pass; main remains at 2b282a07a. Later commits update only
evidence/index/history docs. Backward compatible: yes, test/evidence only.

## Directed reproduction

With the isolated E19 Docker stack already healthy (see [testing](./TESTING.md)):

```bash
docker compose --profile test run --rm --build --no-deps --entrypoint bash go-integration-test -c '
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_E101_DISCOVERY_RETAINED_CRASH_EVIDENCE=1
export CASSANDRA_KEYSPACE=sesamefs_e19 SESAMEFS_URL=http://sesamefs-e19:8080
export SESAMEFS_URL_2=$SESAMEFS_URL SESAMEFS_URL_3=$SESAMEFS_URL
go test -tags integration -v -count=1 -timeout 10m -run "^(TestE101DiscoveryRetainedTerminalProcessCrash|TestE101DiscoveryRetainedFailureSnapshotSurvivesTeardown|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite)$" ./internal/integration
'
```

The standard `go-integration-test` and `go-all-test` commands both require F's
new gate. Do not set the forwarding child flag for the standard single run;
race repetitions and the disposable early-SKIP probes run directly on E19 to
exercise per-invocation credit/reset semantics in the same test process.
