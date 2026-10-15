# E1-3: late publication against GC pre-D proof

Status: PASS for the named orderings on codex/e1-3-late-publication-pre-d-proof.
Base: main@abb558358, merged #251. Plan frozen before implementation,
2026-10-03. X1 remains OPEN; production GC activation unchanged.

## Scope and harness

Two deterministic interleavings against the current Office CreateFile/shared
repair path, real Cassandra 5.0.9 and pinned SILO
`RELEASE.2026-09-16T00-00-00Z`. Reuse the productive G5 worker, real
candidate/queue, settled claim, global zero-proof, exact-P validation and
repair visitor. Observe driver SELECT responses on a dedicated worker session;
pause only at the chosen real boundary. Never substitute liveness results.

Use existing request barriers to prepare actual P1 and publication state.
Remove only fixture-owned temporary references through the production API as
an explicit expiry-state control, following the existing W2 harness. No TTL
policy changes or claims of elapsed 48h/35d TTL/scanner coverage. No CQL writes
manufacture HEAD, repair, claim, physical life or D authority.

## Leg A: GC claim wins before late repair acquisition

1. Start a real Office writer and pause after staging but before repair queue.
2. Model expiry of its exact temporary references; verify zero real references
   and no repair. Drive a real candidate through G5's settled claim.
3. After the final negative repair-domain read, hold GC before its second refs
   read. Resume the writer while that exact claim remains active.
4. Observe actual late repair acquisition and final exact-P rejection; require
   HEAD unchanged and no permanent fs: reference.
5. Resume GC. Accept either a safe new D or a liveness-driven veto, inspecting
   exact P/D identity and canonical/object/ref/repair state. Never require D if
   newly staged liveness legitimately vetoes it.

A no-GC writer control must succeed. Negative control: omit the productive
final exact-P validation only inside the runner; this leg must fail its
no-HEAD assertion. No mutation is a production counterexample.

## Leg B: reachable settlement wins during the proof

1. A real Office writer wins HEAD and stops before permanent promotion, leaving
   its durable repair. Model only temporary-reference expiry.
2. Drive the real worker through its settled claim and first refs=0 response.
   Pause before scanning the repair domain.
3. Run the real reachable repair visitor on a separate session. Require exact
   fs: installation before its repair disappears, unchanged HEAD/tree/P1/K1.
4. Continue GC: observe a negative repair scan and the second real refs read.
   Require the second read to see the permanent reference, claim released, no
   D, canonical life/object retained and repair settled.

Negative control: omit the second productive refs read inside the runner;
this leg must detect destructive progress despite its reachable fs:.

## Validation and disposition

Run directed real-service legs, then the complete standard Docker Go/API/OIDC
suite, normal/integration vet, the two isolated RED mutations, restored-source
race repetitions, and a final whole-diff review. Keep stacks sequential; wait
for an existing integration runner to finish before testing.

GREEN: record only the named gates/interleavings and covered funnel; no runtime
change. RED: preserve the actual counterexample, implement the minimal fix and
prove restored safety with relevant controls. Update E1/X1/current-work evidence
without closing all pre-D handoff, W2-11/14 or X1 by non-reproduction.

Excluded: schema, TTL policy, repair scheduler, W2-11 redesign, W2-14 scanner
candidate discovery, Phase 5/6, stale-claim, clock-health, PRE-GC and A1.

## Execution evidence — 2026-10-03

The plan was frozen in `52a4742ba`. `TestE13LatePublicationPreDProof` adds
three real-service legs without production runtime edits:

| Leg | Directed result | Exact observation |
|---|---|---|
| noGCWriterControl | PASS | Productive Office CreateFile wins HEAD, installs permanent fs:, settles repair and retains exact bytes. |
| claimWinsBeforeLateRepair | PASS | G5 holds exact P1 claim; first refs=0 and all 32 repair buckets empty. The writer then acknowledges a real repair INSERT, but final exact-P returns 409, HEAD stays unchanged and cleanup removes the late durable repair. The temporary up:/pub: pins were already removed by the explicit expiry-state control and remain absent. The second refs read returns zero, G5 commits D1 and terminal recovery retires P1/K1. |
| reachableSettlementWinsDuringProof | PASS | G5 holds exact P1 claim after its first refs=0 response. Productive REACHABLE repair acknowledges fs: INSERT before repair DELETE. GC then reads 32 empty repair buckets and its second refs read returns one. The claim is released, no delete lifecycle/root exists, and HEAD/tree/P1/K1 remain intact. |

The driver observer scopes by exact org/block and EACH_QUORUM statements on a
dedicated worker session. It pauses after a completed response, leaving those
real rows unchanged. The late-writer barrier is after staging but before repair
queue; the settlement barrier is after the first zero-reference response,
before any repair bucket read. Recovery uses a separate real session. Channel
release/join precedes session and fixture teardown, including on failures.

One initial harness assumption was corrected: ProcessOrgOnce's processed count
also includes settlement of a re-referenced live queue item. Both successful
legs report one processed item. Safety assertions therefore inspect actual
canonical P1, claim, handoff, lifecycle/root, refs and bytes instead of treating
that count as a delete count.

The final test source copied into the Docker runner has SHA-256
`6b405a3f8d963fbfc64994771ff2d70b96fa86a71ff2eefd4a8a8e052f25af15`;
the host and runner hashes match. This follows the image build while correcting
only the test harness; production source remains the base version.

These results cover the two measured orderings for Office CreateFile/shared
repair. They do not close every pre-D schedule, any other funnel, concurrent
cleanup, W2-11/14 or X1. No OS restart or isolated multi-DC failure leg is
claimed. Temporary-reference removal is explicitly an expiry-state control;
no real TTL wait, expiry scanner discovery or TTL-policy change is claimed.

## Full validation — PASS, 2026-10-03

All service executions were sequential in the same Docker runner, after the
previous runner finished. The standard Compose go-all-test command passed Go
unit/integration, API and OIDC with required evidence gates. Integration
completed in 437.115s; all three E1-3 legs passed within that suite.
`go vet ./...` and `go vet -tags integration ./...` passed.

Two isolated container-only mutations were RED at their behavior assertions:

- Omitting the productive final exact-P check allowed the late writer to
  advance HEAD under the held GC claim. The test failed at
  `late writer published HEAD under settled GC claim`.
- Omitting the second productive refs read allowed G5 to retire canonical P1
  after the real reachable repair installed fs:. The test failed at
  `E1-3 settlement destroyed canonical P1: exists=false err=<nil>`.

Mutation setup/compilation failures were not counted as RED evidence. Both
source files were restored and byte-compared before the final race run; host
production files were never mutated. These are negative controls, not
counterexamples against the unchanged production runtime.

`go test -tags integration -race -v -count=10 -timeout 5m ./internal/integration
-run '^TestE13LatePublicationPreDProof$'` passed all ten repetitions in 33.261s
without race reports. Final formatting and whole-diff checks passed. Optional
isolated 3DC/proxy/saturation legs are not claimed as passed. E1 remains partial,
W2-11/14 and X1 OPEN, and no production GC activation gate changed.

## Scoped three-DC verification plan — 2026-10-15

Base: main@7fa8b26995c3f9f5945839d1dd44fd808be545f7 (merged #298).
Freeze this plan before changing Go source. Scope is the existing three E1-3
legs; do not change productive GC, publication, schema, observer barriers or
shared session helpers.

1. Reproduce the unchanged test against the isolated Cassandra 5.0.9 / SILO
   stack with NetworkTopologyStrategy RF1 in dc-na, dc-eu and dc-asia. Record
   actual replication, GC-disabled backend, named PASS/FAIL and cleanup state.
2. If the worker's local adapter demonstrably omits the validated replication
   declaration, wrap its existing observed session with config.Load().Database,
   following e111GC. Preserve the productive topology gate and real proof reads.
   Otherwise report the measured cause before broadening this scope.
3. Repeat all three legs, race repetitions, an EU-coordinator run and single-DC
   compatibility. Re-run both original behavior mutations only in disposable
   containers: omitted final exact-P check and omitted second refs read. Require
   the documented behavior assertion, then restore and compare source.
4. Run relevant Go short tests and vet in Docker; audit the complete diff,
   source identity, owned cleanup, retained discovery probes and current main.
   Record executed and omitted checks, then open a focused PR.

This is evidence for named healthy-topology schedules, not a partition, TTL,
restart, all-funnel or gate-closure claim. E1/X1 remain open and ordinary GC off.

## Three-DC reproduction and scoped correction (2026-10-15)

### Failure first and exact scope

Frozen plan `eefa1fbef`, base main@7fa8b26995c3f9f5945839d1dd44fd808be545f7
(merged #298). The unchanged three-leg test reproduces 1 PASS / 2 FAIL, exit 1,
without SKIPs. Both worker legs fail at their original 15s proof barrier:
`timed out waiting for negative repair scan` and
`timed out waiting for first real refs=0 response`. Before those timeouts the
productive gate reports actual `dc-asia:1,dc-eu:1,dc-na:1` versus declared
`dc-na:1`; it correctly refuses G2. The no-GC writer control passes.

The cause is local to e13StartWorker's w2EvidenceSession wrapper: its minimal
DatabaseConfig omits ReplicationDCs. Nine added Go lines load the fixture's
validated DatabaseConfig and wrap that SAME observed native session. Existing
w2RepairFixture already requires config.Load via shareProjectionDBForTest, so
this introduces no new application/auth configuration requirement for its two
callers (both inside E1-3). Session lifetime, observer, query consistency,
barriers, productive claim/liveness/handoff and original assertions are intact.
No shared helper, runtime, schema, runner, default or dependency changes.

### Provenance and environment

- Original E1-3 LF-normalized SHA256:
  `6b405a3f8d963fbfc64994771ff2d70b96fa86a71ff2eefd4a8a8e052f25af15`;
  corrected host/runner:
  `ac4e18b8c4b514415de1a8f50e50aa06f48eb1e4f0c9d59737d9fe16a5471b33`.
  Shared w2_native_wire_test.go remains
  `d22a369ca0b7150463589480823ca84805b7c535345edaa8fe4fcf58e556a842`.
- Docker red image `sesamefs-e13-3dc-red`:
  `sha256:015625405491be27ee6f158fde062fc94c00e357fec48e81962178f6b5411059`;
  green `sesamefs-e13-3dc-green`:
  `sha256:03a915e5069361e05205f7430fc2555b1b3384c1a866b3c0e5e208d02ad19b3c`.
  Green runner source was normalized to LF before execution; semantic source
  matches the image. An initial format precheck rejected mixed CRLF/LF and
  exited 1 before running tests; it is not counted as runtime evidence.
- Reused private sesamefs-e111-3dc stack, migrated sesamefs_e19 keyspace,
  Cassandra 5.0.9 with actual NetworkTopologyStrategy RF1 in each of dc-na,
  dc-eu and dc-asia. All nodes healthy; actual map checked before and after.
  SILO RELEASE.2026-09-16T00-00-00Z image
  `sha256:635197cb9f36d01bee221d34d1c7d7960f6a95c48b0b6c01d99cd13bdae51a46`.
- Same base production backend for RED/GREEN, binary SHA256
  `f95399fbbcf07e5f853c04067839247222280b1bf02511e0142bfb95177cc6ab`.
  Its admin GC status explicitly reports enabled=false and worker never run.
  API/seed uses dc-na; test sessions use dc-na, then dc-eu in the EU run.
  Single-DC compatibility uses private sesamefs-e101h, datacenter1 RF1 and
  a backend built from the same source, GC OFF. Real-service test executions were
  sequential.

### Executed checks

Ignored logs and explicit exits under tmp/:

| Log | Result |
|---|---|
| e13-3dc-red.log | Original matrix: control PASS, both worker barriers FAIL; package exit 1. |
| e13-3dc-green.log | Same 3/3 legs PASS, evidence-gate inventory and no-global-fanout guards PASS; exit 0. |
| e13-3dc-race.log | -race -count=2: 6/6 legs PASS, exit 0. |
| e13-3dc-eu.log | All 3 legs through dc-eu PASS, exit 0. |
| e13-3dc-mutation-exact.log | Omit final advisory exact-P validation: late writer advances HEAD under claim; original behavior assertion FAIL, expected exit 1. |
| e13-3dc-mutation-refs.log | Omit second real refs read: canonical P1 destroyed after reachable fs: settlement; original behavior assertion FAIL, expected exit 1. |
| e13-3dc-cleanup-audit.log | Restored runtime: 3/3 legs plus GC-OFF check PASS; each fixture verifies metadata/repairs/K1 and captured expiry coordinates absent. |
| e13-3dc-negatives.log | Both expected behavior markers/cleanup checks, source restoration and byte comparisons PASS; wrapper exit 0. |
| e13-one-dc.log | Same 3/3 legs and both static guards PASS, package/runner exit 0. |
| e13-3dc-restored-race.log | After mutations and byte comparisons, original source -race -count=1: 3/3 legs PASS, exit 0. |
| e13-unit-vet.log | Complete short Go tree, ordinary vet and integration-package vet PASS, exit 0. |

All directed positive matrices ran without SKIPs or race reports. E1-3 has no
mandatory TestMain evidence gate: exact named subtest PASS counts were checked,
not merely a zero package exit. The three-DC TestMain cleanup helper still logs
its pre-existing partial-topology warning; it is not the corrected worker.

Mutations never entered host source. The second mutation records the original
canonical-loss assertion before an extra container-only exact COMMITTED
continuation disposes D1; then it fails the test. A container-only audit wrapper
registers existing e111VerifyCleanup and e112VerifyExpiryCleanup around the
original fixture teardown, snapshotting expiry coordinates without adding any
cleanup writes. Both mutation fixtures and three restored fixtures verify
cleanup. Audit instrumentation was removed and all three touched container
sources byte-compared before the final original-source race run.

Independent EACH_QUORUM counts before/after: recovery roots=0, orphans=0,
publication repairs=0. The private bucket retains 25 auxiliary storage-class
probes (17 before + 8 from these executions); every retained payload matches
`gc-test-discover.txt-*`. They are separate from random-org E1-3 K1 fixtures;
API library deletion is soft and ordinary GC stays OFF. No empty-bucket claim.
Private stacks were stopped after verification, volumes retained.

### Reproduction and disposition

Inside the Docker image, with isolated GC-OFF API/SILO, migrated keyspace,
complete explicit CASSANDRA_HOSTS/LOCAL_DC/REPLICATION_DCS and SESAMEFS_URL:

```bash
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
go test -tags integration -v -count=1 -timeout 5m -run '^(TestE13LatePublicationPreDProof|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite)$' ./internal/integration
```

Repeat the E1-3 selector with -race -count=2, then with dc-eu host/local DC.
This corrects a demonstrated fixture blocker and measures the two named healthy
multi-DC schedules. No outage, divergent replica, actual TTL wait, OS writer
restart, every funnel or full-suite claim. Not run here: full standard
integration/API/OIDC, standalone Sync/frontend or E1-7/E1-09/E1-10e. Historical
2026-10-03 full-suite evidence above is preserved, not rerun by this slice.
Those three cases and the P2 umbrella/full-suite obligations remain OPEN;
E1-01 (including PREPARED without discovery), X1, PRE-GC/A1 remain OPEN.
Backward compatible: yes, runtime contracts and supported configurations unchanged.

Final scoped audit: no introduced blocker identified. The complete diff retains
all original test oracles, ProcessOrgOnce ownership, observer/timeouts, native
session and productive fences. Only the local worker's declared fixture config
changes. Relative file links (256), new evidence anchor, source hashes, exact
positive/negative exits and git diff --check pass. Current main remains the
frozen base; no rebase or published-branch rewrite was required.
