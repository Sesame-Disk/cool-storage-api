# E1-01H — root persisted before PREPARED process crash

Frozen plan: 2026-10-15, base `main@a71eec36a` (#292 merged).
Advances #201 E1-01, exactly C0 inside current PrepareBlockDeleteOrphan.
E1-01/X1 stay OPEN; ordinary/production GC OFF. Safe retention is a valid
result, not a reproduced defect or an obligation to reclaim an unsettled root.

- Use productive upload, controlled owned-reference lapse and real
  Worker.ProcessOrgOnce for the fixture organization: claim and zero-liveness
  remain real. Capture exact P/D from its delegated Prepare call.
- Existing NewWithObservers pauses only the successful owned nine-argument
  recovery-root INSERT, before the producer can submit its PREPARED LWT. No
  productive hook or direct setup CQL fabricates root, orphan or authority.
- Parent independently proves exact deleting/uncommitted blocks claim, root and
  original token present, orphan/discovery/lifecycle absent, candidate/queue
  retained, original K1 bytes intact, zero refs/repairs and unchanged HEAD.
- Before either action, an independent real recovery process must report zero
  completed orphan recoveries, prove the exact missing-orphan/unpublished-lifecycle
  executor visits, and leave the full C0 snapshot unchanged while the
  producer is paused. Resume must still let that producer finish normal G2/G3.
- SIGKILL leg: verify OS death, then two fresh recovery/replay processes must
  retain exactly C0, including K1 and the unresolved root/claim. No bounded
  reclamation or convergence is inferred. Resume leg: require productive child
  completion, independent physical recovery/replay and exact TERMINAL settlement.
- Keep original discovery coordinates for all after-root-removal checks. Only
  AFTER positive retention proof and producer death may explicit owned fixture
  disposal revoke the exact uncommitted claim and use the existing serial
  DeletePreparedBlockDeleteOrphan cleanup. That is test disposal, not automatic
  root recovery. Never run it as failed-leg teardown.
- Failed legs preserve full snapshots/live state. A new session compares that
  state after teardown BEFORE separately authorized fixture disposal. Run the
  failed-fixture child directly on E19 to retain its liveRepoIDs.
- Separate mandatory two-leg gate: reset per invocation, register parent cleanup
  before availability checks, credit after successful teardown, TestMain and both
  standard Compose runners. Negatives: omitted resume, unavailable backend,
  early SKIP in both repetition orders, unavailable retention child after SIGKILL,
  and missing no-kill completion; verify preserved live state before disposal.

Scope: one C0 test-only cut, minimal shared harness/gate wiring and lean tracking
and evidence docs. No runtime/schema/API/writer/scheduler/GC-default/dependency
change. No automatic root removal, lease expiry rule or new recovery deadline.
A reproduced product defect requires its own bounded regression/fix PR. C0–C8
coverage reconciliation, three-DC residuals and gate closure remain separate.

Docker validation: directed H/preservation plus static guards; H race repetitions;
negative probes and fresh verified disposal; complete short Go tree and normal/
integration vet; standard required integration/API/OIDC including A–H and all
preservation controls. Record image/source hashes, real topology, actual exits,
pre-action and post-kill retention, resume recovery/replay, and disposal separately.
Audit final scope, links, command, gate semantics, compatibility and current main.
Backward compatible: yes, integration evidence and tracking only.

The prior SesameFS Docker project was absent. Validation uses a separate
`sesamefs-e101h` project with fresh private volumes/network and no published
host ports. Database/storage endpoints are checked to resolve only inside that
project. Existing local test GC settings are preserved: only its owned standard-
runner namespace has the development scheduler; E19/node2/node3 GC stay OFF.
Other projects and ordinary/production deployments are untouched.

## Frozen execution and validation (2026-10-15)

Implementation commit: `5a9e56eb65aab1f0260f02f89a92b6e74841c0c4`.
Runner `sesamefs-e101h-directed`:
`sha256:3b1b5e39e542de3754ef5333363187ac3af2348a5d17e6138afdd76fbb69ae4c`.
All subsequent tracked changes are documentation only. The three modified Go
files match the image byte-for-byte after normalizing host CRLF to LF:

| File in internal/integration | SHA-256 |
|---|---|
| e101h_root_before_prepared_crash_test.go | `a290079356acc7064adeae2acc49695c577b78701051621e6ce86366f1f8e6d1` |
| e101_g2_process_crash_test.go | `897ed7e72354f4e7dd3697f11b396d3d51c83d82698d9eed4f5322707aedcc10` |
| integration_test.go | `a142a11790be07e1fce5f257422855e6683c12acddd4633707f53013def09ddd` |

Private project `sesamefs-e101h`, fresh owned volumes, no published ports.
Cassandra 5.0.9: one UN node, datacenter1, actual NetworkTopologyStrategy RF1,
LOCAL_QUORUM plus global SERIAL. SILO S3 image
`docker.io/pgsty/silo:RELEASE.2026-09-16T00-00-00Z`,
`sha256:635197cb9f36d01bee221d34d1c7d7960f6a95c48b0b6c01d99cd13bdae51a46`.
All four cached backend containers run the same binary SHA-256
`e6facb5f64f3b3104793242cf57f4dbd1a1279d6bb377e5d6d11ec5596aec1e3`.
The test runner contains this PR's Go sources and executes the producer/recovery
in child processes; the backend handles productive fixture HTTP operations.
This PR changes no backend runtime source. Main local development scheduler is
ON only in the owned standard-runner namespace, as previously authorized;
E19/node2/node3 GC are OFF. These are three application pods in ONE Cassandra
DC, not a three-DC experiment. Database/S3 operations use private services; no shared deployment used.

Local ignored `tmp/` logs and explicit `.exit` records:

- `e101h-directed.log`: C0 kill/resume, preservation, gate/fanout guards PASS,
  15.455s, exit 0. Before each action, a fresh process visits exact root and
  classifies missing orphan/unpublished lifecycle with n=0; full snapshot equal.
  Confirmed OS SIGKILL is followed by two additional independent retention
  passes (`producer-killed`, `killed-replay`), with root/claim/canonical/K1 kept.
  Resume requires productive completion (including owned queue settlement),
  independent physical recovery/replay, exact TERMINAL, absent root/orphan/
  discovery/canonical/K1 and independently confirmed settled candidate/queue.
- `e101h-race.log`: H and preservation repeated twice with race PASS, 47.394s,
  exit 0. One positive and one preservation-control SIGKILL per repetition.
- `e101h-unit-vet.log`: entire short Go tree and normal/integration vet PASS,
  exit 0, using the frozen image.
- `e101h-gate-negatives.log`: only H gate required; omitted resume and unavailable
  backend each test exit 1 with explicit missing/backend diagnostics. Wrapper 0.
- `e101h-skip-first.log` / `e101h-skip-second.log`: actual early SKIP in each
  repetition order rejected, test exit 1; healthy repetition independently passes
  both legs. Parent cleanup prevents accumulated prior credit hiding skipped legs.
- `e101h-endpoint-negative.log`: only the post-SIGKILL retention child's S3
  endpoint is unavailable in a disposable source copy. Pre-action retention and
  OS SIGKILL are proved. Actual helper SKIP/exit 0 is rejected:
  `C0 retention completion not proved: <nil>`, parent test exit 1.
- `e101h-unvisited-negative.log`: disposable post-kill enumeration returns no
  roots. n=0 is rejected because `absent=false unpublished=false`; helper and
  parent fail, test exit 1. This is an harness control, not a product finding.
- `e101h-endpoint-disposal.log` / `e101h-unvisited-disposal.log`: each new process
  compares its external JSON with live retained C0 AFTER failed teardown BEFORE
  explicit disposal; PASS, exits 0. Exact uncommitted claim is revoked with
  AbortBlockDeleteHandoff Applied, then serial prepared/root cleanup removes
  the root. Canonical/K1/HEAD are independently preserved and original P remains
  repair-authorized until ordinary fixture teardown. JSON stays byte-identical.
- `e101h-nokill-negative.log`: disposable source changes productive completion
  log to a real helper SKIP after ProcessOrgOnce returns. Parent rejects exit 0
  without worker completion, test exit 1. Reached state is legitimately
  PUBLISHED/COMMITTED, canonical absent, root/discovery and original K1 present,
  queue settled. This is not labelled retained C0. No automatic recovery runs.
- `e101h-nokill-disposal.log`: another fresh process compares that reached live
  state BEFORE explicit recovery/disposal; PASS, exit 0; JSON unchanged.
  Every negative wrapper exits 0 only after asserting the intended semantic
  test failure. Disposable source mutations are absent from the frozen image/PR.
- `e101h-go-all.log`: standard short Go coverage and required integration PASS
  (903.561s), API 20/20 suites (91s), OIDC 25/25 tests (one suite, 1s).
  A–H matrices and all eight preservation controls PASS, as do gate wiring
  and no-global-fanout guards. Actual container `e101h-go-all.exit` and outer
  `e101h-go-all.outer.exit` both record 0. The exact standard Compose command
  is enveloped only to retain its exit before --rm. Conditional topology,
  helper/load and three-DC SKIPs are not positive evidence.

No positive H run has failed. Expected negative test failures above prove the
harness; they establish no runtime defect. Safe C0 retention is distinct from
explicit owned-fixture disposal; root removal during disposal is never credited
as automatic recovery or bounded convergence. No new runtime fix is proposed.

## Directed reproduction

With an isolated E19 Docker stack healthy (see [testing](./TESTING.md)):

```bash
docker compose --profile test run --rm --build --no-deps --entrypoint bash go-integration-test -c '
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_E101_ROOT_PREPARED_CRASH_EVIDENCE=1
export CASSANDRA_KEYSPACE=sesamefs_e19 SESAMEFS_URL=http://sesamefs-e19:8080
export SESAMEFS_URL_2=$SESAMEFS_URL SESAMEFS_URL_3=$SESAMEFS_URL
go test -tags integration -v -count=1 -timeout 10m -run "^(TestE101RootBeforePreparedProcessCrash|TestE101RootBeforePreparedFailureSnapshotSurvivesTeardown|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite)$" ./internal/integration
'
```

Both standard Compose runners require H's new gate. Race repetitions and
same-process early-SKIP probes run directly on E19 with SESAMEFS_E101H_CHILD=1;
the normal single run also exercises forwarding. The private execution used
`-p sesamefs-e101h` and an ignored image/ports override, retaining the exact
standard runner command. No tracked GC default changes.

Not run: additional standalone Sync/frontend or three-DC suites. No global
scheduling/fairness, automatic root reclamation, all-C0–C8/E1-01/X1 closure or
PRE-GC/A1/activation claim is inferred. Maintainer-owned dispositions stay open.
## Final scoped audit

No blockers identified in this PR. The owned full nine-value INSERT observer
only pauses the productive call; independent SERIAL reads prove the durable C0
state. Every retention pass requires exact missing-orphan/unpublished-lifecycle
visits, n=0 and full before/after snapshot equality. Kill is OS SIGKILL; resume
requires productive completion. Recovery/disposal are separate, and failed legs
retain their live state/snapshots until fresh comparison and explicit disposal.
Negatives reject missing/Skipped/unavailable evidence; A–G remain green.

The three Go files still match the frozen tested image. Diff scope is nine
planned files: integration tests, two Compose gate additions, and documentation.
Relative file links, reproduction shell syntax, Compose consistency (exactly the
new H gate on the two unchanged commands/settings) and git diff --check PASS.
Fetched main remains `a71eec36a04614e5b3646ae523a406488062a4be`; no rebase needed.
No new runtime defect or gate closure is claimed. Backward compatible: yes,
test/evidence only. Subsequent commits update evidence/tracking documentation.