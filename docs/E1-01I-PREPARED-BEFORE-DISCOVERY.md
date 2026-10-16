# E1-01I — PREPARED before discovery process crash

## Frozen plan (2026-10-16)

Base: main@a0a767591 (#306). Advances D0 §25 G1/G2 and E1-01's explicitly
unwitnessed PREPARED-without-projection process window. No runtime defect is
presumed and no X1 closure or activation is authorized.

1. Reuse the existing Linux Docker SIGKILL/resume harness, real Cassandra/S3,
   query observer and org-scoped worker. Hold only the successful exact orphan
   INSERT inside Prepare, before its discovery publication. No fabricated CQL
   setup, new productive hooks or global worker fan-out.
2. Parent independently proves exact uncommitted blocks claim, matching root and
   PREPARED/pending_s3 orphan, absent by-day/lifecycle, intact K1/canonical/HEAD,
   zero refs/repairs and retained owned queue/candidate. Both kill and resume
   legs must reach that same state.
3. Kill must be OS SIGKILL. Fresh-process root recovery must actually visit the
   identity, republish discovery and abort only the uncommitted claim; verify
   original physical bytes/HEAD survive and orphan/root settle. Fresh replay
   must be harmless. The resume control completes normal committed deletion.
4. Preserve failed-fixture snapshots and live state before explicit disposal.
   Require both legs with a fail-closed evidence gate. Check missing-leg,
   missing-test and unavailable-service negatives; use a directed omission of
   the actual projection-repair call to prove that recovery observation matters.
5. Run named tests in Docker, race in parent and child, repeated/reversed legs,
   existing A/H compatibility and relevant guard/vet checks. Audit exact counts,
   cleanup, links and source diff; refresh main before publishing a focused PR.

Scope: one process cut with two controls, existing harness wiring and bounded
tracking. No retry/timeout/schema/API/GC-default change. One-DC evidence is not
three-DC, global scheduling, partition or full-suite certification. A reproduced
runtime defect requires its own RED/GREEN correction; report failures first.
Backward compatible: yes, test/evidence only unless a separately proven fix is needed.

## Result and audit (2026-10-16)

No productive defect was reproduced in this window. The [new test](../internal/integration/e101i_prepared_before_discovery_crash_test.go)
adds specific process evidence to D0 §25 G1/G2 and the E1-01 inventory. It does
not close E1-01/X1 or qualify global scheduling, three-DC or outage behavior.

The real ProcessOrgOnce producer pauses after the owned PREPARED LWT and before
Prepare publishes discovery. Parent reads independently certify the exact
uncommitted D/P, root/first_seen_at and PREPARED/pending_s3 row, absent discovery
and lifecycle, intact canonical/K1/HEAD, zero refs/repairs and retained work.
The observer alone is not an application oracle; no authority is seeded by CQL.

After verified OS SIGKILL, a fresh process executes root recovery. A delegating
store observes the real PublishS3OrphanDiscovery call and reads its exact row
before recovery can remove it again. The uncommitted claim then aborts; original
bytes and HEAD remain, authority is released and orphan/discovery/root settle.
Queue/candidate retention proves no queue worker substituted for root recovery.
Two fresh recovery replays preserve the result. The resume control completes
COMMITTED handoff and physical deletion, with TERMINAL and settled work.

| Docker check | Accepted result |
|---|---|
| Directed I matrix, required gate | kill + resume PASS; failure-preservation control PASS; both existing guards PASS |
| Race, count=2, shuffle seed 41 | I kill/resume and preservation PASS twice; A's four legs and H's two legs PASS twice; both guards PASS twice |
| Reverse individual invocations | resume then kill PASS; gate unset for these individual selections, not a required-matrix certificate |
| Required gate: omitted resume / whole test / unavailable backend | Each package exits 1 for its intended missing evidence; wrapper validates the rejection |
| Omit actual root-executor projection-publication call, isolated source copy | Package exits 1: recovery n=1, repaired=false; SIGKILL verified. This proves the new observation detects the omission, not a data-loss counterexample |
| Restored branch source after omission | Full directed I matrix, preservation and guards PASS again |
| GC short unit suite / integration-tag vet | PASS |

No accepted positive run skips a selected test or reports a data race. The race
parent launches the same instrumented executable for producer/recovery children;
the HTTP fixture daemon is not a race binary. Full integration/API/OIDC suites,
client sync, partitions, ambiguous LWT/S3 responses and global cursor fairness
were not run. Existing A/H controls retain their own scopes.

Initial scaffolding failures are retained: the shared snapshot did not yet query
discovery for I; an accidental terminal-only assertion rejected the correctly
preserved aborted bytes. An early overlapping negative invalidated preservation
disposal (HTTP 404); that run is discarded and accepted runs are serial. Three
retained initial fixtures were independently checked and explicitly disposed;
one disposal first reported an already-deleted library after metadata cleanup.
A final read-only audit initially expected 16 TERMINAL rows; enumerating the
actual schedules gives 15. The rejected count and corrected audit are retained.
These are harness/audit failures, not productive RED/GREEN or accepted PASS.

Final independent audit covers all 35 logged owned physical identities from
initial, positive, gate, omission, reverse and restored executions. EACH_QUORUM
reads find no owned blocks, references, candidates, queue, orphan, discovery or
recovery root; exact S3 keys are absent. SERIAL reads find only 15 retained
TERMINAL certificates, with no unsettled lifecycle. Those certificates and normal
soft-deleted library history are retained; no empty-keyspace/bucket claim or
broad cleanup is made. The omission failure's snapshot and live state were
compared before exact disposal, and its artifact remains unchanged.

Topology: one Cassandra DC (datacenter1), real S3-compatible pinned SILO fixture,
one current-main HTTP backend on the isolated e19 keyspace. Ordinary GC reports
enabled=false and last_worker_run=never. Only explicitly owned test workers run.
The task-started API, runner and one-DC Cassandra/S3 services are stopped after
verification; volumes and the retained evidence are preserved.
The reused driver image bind-mounts current source; the fixture API is rebuilt
from this branch's unchanged productive source. API binary SHA256:
ee229e9114fe9a2d46dda0492fefdc8ba5719259727cefe61d2caed16ef39870.
Local ignored artifacts: tmp/e101i-* events, logs, exits, source manifest and owned
key inventory; initial failure snapshots under internal/integration/tmp/e101-failures.
They are local execution records, not committed raw logs or independent CI.

Run only against the verified isolated Docker fixture, with other REQUIRE flags
unset and SESAMEFS_URL{,_2,_3}/CASSANDRA_KEYSPACE configured for e19:

```bash
SESAMEFS_REQUIRE_E101_PREPARED_DISCOVERY_CRASH_EVIDENCE=1 \
  go test -tags integration -count=1 -timeout 10m \
  -run '^(TestE101PreparedBeforeDiscoveryProcessCrash|TestE101PreparedBeforeDiscoveryFailureSnapshotSurvivesTeardown|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite)$' \
  ./internal/integration
# Add -race; subprocesses inherit the instrumented test executable.
```

The new gate is wired into TestMain and both existing Compose integration
commands. No new runtime hook, dependency, schema, retry, timeout budget or GC
default changes. Backward compatible: yes, test/evidence only.
