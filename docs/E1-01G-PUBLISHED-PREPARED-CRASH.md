# E1-01G — PUBLISHED lifecycle with PREPARED orphan process crash

Frozen plan: 2026-10-14, base `main@6f8fc2630` (#291 merged).
Advances #201 E1-01, exactly C3 inside productive PromoteBlockDeleteOrphan.
E1-01/X1 stay OPEN; ordinary/production GC OFF. Missing crash evidence is not
itself a reproduced product defect; no gate closes in this slice.

- Run the real Worker.ProcessOrgOnce for the fixture's organization, preserving
  claim, zero-liveness, Prepare and committed G2 handoff. No global ProcessOnce.
- Reuse NewWithObservers in that worker process. Capture exact P/D and original
  first_seen from the delegated promotion caller and real PREPARED row; pause
  only the successful eight-argument owned lifecycle INSERT before orphan CAS.
  The diagnostic callback is not the durable oracle; add no production hook.
- Parent independently proves exact blocks deleting/committed D1/P1, PUBLISHED
  lifecycle, PREPARED/pending_s3 orphan, root/original token/discovery present,
  candidate/queue retained, original K1 bytes intact, unchanged HEAD, zero refs
  and no repairs. A C2 pre-Promote or C4 post-Promote state must fail this oracle.
- Require OS SIGKILL and same-boundary no-kill continuation. Independent orphan
  recovery must finish promotion/retirement to exact TERMINAL without revoking
  D1 or reinstalling P1. Then separately settle/replay the owned queue through
  ProcessOrgOnce; fresh-process orphan replay must remain idempotent. Preserve
  original discovery coordinates for post-root-removal checks.
- Preserve full snapshots on failure. Another process/session compares live state
  after failed teardown BEFORE explicit owned recovery/disposal. Failed legs must
  not retry recovery or erase K1/authority under inherited cleanup.
- Separate mandatory two-leg gate, reset per invocation, parent cleanup registered
  before availability checks, credit after teardown, TestMain and both Compose
  runners. Check omitted resume, unavailable backend, early SKIP in both repetition
  orders, unavailable recovery child and incomplete no-kill child completion.

Scope: one new test-only cut, minimal shared harness/gate wiring and evidence docs.
No runtime/schema/API/writer/scheduler/GC-default/dependency change. A reproduced
product defect needs a separate bounded regression/fix PR. C0, three-DC residuals
and final closure inventory remain separate; no automatic C8/E1-01/X1 closure.

Docker validation: directed G/preservation and static guards; G race repetitions;
negative gate/recovery probes with verified disposal; complete short Go tree and
normal/integration vet; standard required integration/API/OIDC including A–G and
all preservation controls. Record actual exits, immutable image/source hashes,
real topology, pre-kill state, recovery/replay and live preservation. Audit final
scope, links, command, gates, compatibility and current main before publication.
Backward compatible: yes, integration evidence and tracking only.

## Validation and audit ledger

Frozen implementation: `61caab0021a9e97f20214a0deeed779b3dc34aa8`.
Final runner image for directed/race/negatives/standard:
`sha256:753491fa6dd52188bfe012c43c4f565906e3bb2c64ff51d12f544d5ce2944029`.
Source SHA-256 inside the image (matches the final Go files normalized to LF):
- G cut/observer/queue continuation: `f342921e77dca7628c70b766c7d7d1ce2a404852b0535ab23430d6f503fa6849`.
- Shared harness: `0d40b5368c7451aa66ae6308c226ebe8d1197e9e91d219eb388a6c31b74969e7`.
- TestMain: `0fab50b7f8034995b75ef316b31e7c9295c61b8d0bba5e29c1b07459b4736669`.

Linux Docker, real Cassandra 5.0.9, datacenter1/RF1 NetworkTopologyStrategy,
LOCAL_QUORUM/SERIAL configuration. Canonical/lifecycle reads pin SERIAL;
scoped metadata snapshots use EACH_QUORUM. SILO `RELEASE.2026-09-16T00-00-00Z`.
E19 backend image `sha256:a53db0b97cb82304f58973aae8d848ad67fa0789489c891b760df4f83223be4e`,
binary SHA-256 `e6facb5f64f3b3104793242cf57f4dbd1a1279d6bb377e5d6d11ec5596aec1e3`.
Productive source unchanged. E19/node2/node3 GC OFF; previously authorized main
development GC is outside this isolated keyspace. No GC setting changed.

Both positive legs prove exact committed D1/P1 in blocks, PUBLISHED lifecycle,
PREPARED/pending_s3 orphan, exact root/original token/discovery, retained owned
candidate/queue, original K1 bytes intact, zero refs/repairs and unchanged HEAD.
The successful exact eight-argument INSERT observer holds the real promotion
caller before the orphan COMMITTED CAS. It captures authority from the delegated
productive call and the original token from a real PREPARED read, without
fabricating state. Parent reads independently certify C3 before kill/resume.
Actual SIGKILL is asserted. Orphan recovery runs in a fresh process before any
queue settlement: parent then observes exact TERMINAL, root/orphan/discovery and
canonical/K1 absent. Separate ProcessOrgOnce settlement and replay remove owned
candidate/queue; further independent orphan replay preserves TERMINAL/HEAD.
Saved discovery coordinates are checked even when the root no longer exists.

The first directed run failed its preservation control with
`dispose verified negative library: HTTP 404` (`e101g-directed-before-fix.log`,
exit 1, 131.077s). Root/orphan/K1 and the snapshot survived, with fresh live-state
verification passing. The extra forwarding TestMain lacked the fixture-owning
process's liveRepoIDs and swept its library. G's failed-fixture child now starts
directly on E19, extending E/F's existing isolation. No block cleanup or runtime
change; A/B/C/D/E/F routes remain unchanged. The corrected control independently
verifies live state before successful explicit disposal. This was a harness
failure, not a demonstrated product defect or evidence of a broken C3 recovery.

Local ignored `tmp/` logs and explicit `.exit` records:
- `e101g-directed.log`: kill/resume, orphan recovery before queue settlement/replay,
  preservation and static gate/fanout guards PASS (109.592s, exit 0).
- `e101g-race.log`: same G cases repeated twice with race PASS (157.531s, exit 0).
  One positive SIGKILL and one preservation-control SIGKILL per repetition.
- `e101g-unit-vet.log`: complete short Go tree and normal/integration vet PASS,
  exit 0, repeated on the final image after the isolation correction.
- `e101g-gate-negatives.log`: omitted resume and unavailable backend each test
  exit 1, with only G's gate required and explicit missing/backend output.
- `e101g-skip-first.log` / `e101g-skip-second.log`: early test SKIP in either
  repetition order is rejected, test exit 1 (40.151s / 42.607s). Each healthy
  repetition independently passes kill/resume. Disposable source only.
- `e101g-s3-negative.log`: only the recovery child's S3 endpoint is faulted in
  a disposable source copy. Actual helper SKIP/exit 0 is rejected for missing
  recovery completion; parent test exit 1 (38.264s). External JSON and live
  state retain exact C3, including canonical/K1, with no snapshot read error.
- `e101g-s3-negative-disposal.log`: a new process/session compares live state
  after failed teardown BEFORE explicit recovery/disposal; PASS (1.626s, exit 0).
  Exact discovery cleanup is checked after root removal; JSON stays byte-identical.
- `e101g-nokill-negative.log`: disposable source replaces the worker completion
  log with an actual helper SKIP after ProcessOrgOnce finishes. Parent rejects
  exit 0 with no success marker, test exit 1 (35.529s). The pre-C3 snapshot is
  retained alongside the reached state: PUBLISHED/COMMITTED, canonical absent,
  root/discovery and original K1 intact, queue settled. No C3 retention claim is
  inferred for this legitimately resumed state; no automatic recovery runs.
- `e101g-nokill-negative-disposal.log`: another new process/session validates
  that reached state BEFORE explicit recovery/disposal; PASS (1.512s, exit 0),
  artifact unchanged. All negative wrappers return 0 only after asserting the
  intended semantic failures and preservation/disposal; these are not positive
  test runs and establish no product counterexample.
- `e101g-go-all.log`: standard runner PASS; short Go coverage, required integration
  (1987.949s), API 20/20 suites (96s), OIDC 25/25 tests (one suite, 2s).
  A–G matrices and all seven preservation controls PASS, alongside existing
  required G1/G4/G5 evidence, gate wiring and no-global-fanout guard. Container
  `e101g-go-all.exit` and outer `e101g-go-all.outer.exit` both record 0.
  The exact Compose command is enveloped only to preserve the actual exit before
  --rm; no existing required gate or command is removed. Conditional three-DC,
  topology/load/helper SKIPs are not positive evidence.

Scope is one C3 cut, one DC and fixture-local enumeration inherited from A–F.
No C0, global scheduling/checkpoint/fairness, P2 publication or three-DC proof is
inferred. No product violation was reproduced. E1-01/X1 stay OPEN; maintainer
closure, PRE-GC/A1 and ordinary/production activation are separate. Additional
Sync/frontend and three-DC suites are not run for this test-only slice.

Final scoped audit: no blockers identified. C3 is independently distinguished
from C2/C4; the observer matches the owned full INSERT tuple and delegates the
real promotion. Fresh orphan recovery precedes queue settlement, with exact
TERMINAL and absent canonical/K1/root/orphan/discovery checked independently.
Further queue/orphan replays, unchanged HEAD, zero refs/repairs and successful
teardown are required before credit. Failed runs preserve snapshots/live state
until explicit fresh verification and disposal. Omitted/SKIP/unavailable evidence
and missing no-kill completion are rejected; A–F semantics remain unchanged.
All three modified Go files match the frozen tested image. Runtime/schema/API,
GC defaults and dependencies have no diff from base. Local links, Compose and
git diff --check pass; current main is 6f8fc2630. Subsequent commits only update
evidence/index/history documentation. Backward compatible: yes, test/evidence only.

## Directed reproduction

With the isolated E19 Docker stack already healthy (see [testing](./TESTING.md)):

```bash
docker compose --profile test run --rm --build --no-deps --entrypoint bash go-integration-test -c '
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_E101_PUBLISHED_PREPARED_CRASH_EVIDENCE=1
export CASSANDRA_KEYSPACE=sesamefs_e19 SESAMEFS_URL=http://sesamefs-e19:8080
export SESAMEFS_URL_2=$SESAMEFS_URL SESAMEFS_URL_3=$SESAMEFS_URL
go test -tags integration -v -count=1 -timeout 10m -run "^(TestE101PublishedPreparedProcessCrash|TestE101PublishedPreparedFailureSnapshotSurvivesTeardown|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite)$" ./internal/integration
'
```

Both standard Compose runners require G's new gate. Race repetitions and the
same-process early-SKIP probes run directly on E19 with SESAMEFS_E101G_CHILD=1;
the normal single run exercises forwarding too. No productive GC flag is changed.
