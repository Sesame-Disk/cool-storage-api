# E1-01A — G2 process-crash evidence

Frozen plan: 2026-10-13, base `main@b0d3a5361a6914ac9063fbb8298a704e12ddbe42` (#280).
Implements only C1/C2 of [the bounded E1-01 plan](./E1-01-CRASH-EVIDENCE-PLAN.md).
Test-only evidence; no runtime/schema/GC activation change. X1 and E1-01 remain OPEN.

- C1: real worker PREPARED return, before handoff commit. SIGKILL; independent root recovery aborts D1,
  preserves canonical P1/K1, and a second process replay remains harmless.
- C2: real blocks handoff committed, before Promote. SIGKILL; independent root recovery preserves exact D1,
  promotes/retire/deletes only K1 and reaches TERMINAL; a second process replay remains harmless.
- A no-kill control at each same boundary resumes the actual worker and completes physical recovery.
- Existing G4 P1/P2 coexistence is a separate unchanged regression; no extra crash cut or publication funnel.

Use current web upload to obtain P1; release only its owned temporary reference and enqueue its exact candidate.
The child calls `ProcessOrgOnce(ctx, orgID)` with a real-store wrapper scoped to that queue identity.
Prepare/Commit delegate real mutations and pause only confirmed returns. Parent verifies durable P/D/root/orphan,
bytes and handoff flag before killing; no fixture-fabricated lifecycle state. Recovery runs in another OS process
with new DB/storage sessions through current root enumeration. Parent verifies state before teardown.
Only complete/aborted authority may be removed during GC fixture cleanup; no lifecycle deletion ahead of recovery.

Require all four named legs, reject filtered/unavailable runs, preserve subtest filters through isolated forwarding.
Use the existing GC-OFF e19 keyspace/backend; fail if background GC is enabled. Keep production topology checks.
Wire the required gate into TestMain and both standard Compose Go runners. Scope gates in directed runs.
Validation: directed four-leg Docker matrix, independent replay and teardown; omission and unavailable negatives;
race repeat; G1–G5 regression/guards; normal/integration vet; standard Go unit/integration/API/OIDC Docker runner.
A reproduced product defect is a separate small fix PR. A harness failure is not physical RED or PASS.

## Original evidence at PR opening (before the P2 correction)

Validated source: `50287101c` (2026-10-13). Test source SHA-256:
`30365de3b88a17f8beda7facbf28f35054b8332faddf9c86cde9f83fa720de8d`.
Linux Docker, Cassandra 5.0.9 (`datacenter1`, RF=1, `LOCAL_QUORUM`/`SERIAL`),
SILO `RELEASE.2026-09-16T00-00-00Z`; e19 background GC OFF.
Original `go-all-test` image: `sha256:522b70fc7ca58c1eb706715781e06a7ab47449fa478ca3c78cad2c2548e28a4d`.
Unchanged e19 backend image: `sha256:a69c9c7b6e2ab7f15f38ae003277580b5d9dfd1bf4e40235a5f3a76fa6dc9914`.
Running image IDs and the test source hash were checked before integration evidence.

| Boundary | Worker action | Independent recovery and replay | Result |
| --- | --- | --- | --- |
| PREPARED, handoff NULL, lifecycle absent | OS SIGKILL | Abort unpublished D1; canonical P1/K1 preserved; replay harmless | PASS |
| Same PREPARED boundary | Resume | Actual handoff/promotion/retirement, physical K1 deletion and TERMINAL; replay harmless | PASS |
| PREPARED, handoff true, lifecycle absent | OS SIGKILL | Exact D1 promoted/retired; K1 deleted; TERMINAL; replay harmless | PASS |
| Same committed-handoff boundary | Resume | Actual worker completion and physical recovery; replay harmless | PASS |

All four boundaries are read from real Cassandra before the kill/resume. Each recovery/replay
uses a fresh OS process, DB session and storage client, and must emit its completion marker.
The parent reads exact state, HEAD, references, repairs and K1 before authority-aware teardown.

Docker validation artifacts (local ignored `tmp/`, not committed):

- `e101-race-final.log`: final source, `-race -count=2`; 8/8 legs, four verified OS SIGKILLs,
  independent replays and cleanup PASS. `TestEveryEvidenceGateIsWiredIntoTestMain` PASS.
- `e101-omission.log`: filtering to `prepared/kill` fails the required gate with the other three
  named legs missing; isolated forwarding preserves that filter.
- `e101-unavailable.log`: unavailable backend fails TestMain instead of returning a zero-test PASS.
- `e101-s3-negative-confirmed.log`: disposable source copy faults only the first recovery child's
  class-specific S3 endpoint. Its SKIP is rejected by the parent despite exit 0; subsequent normal
  recovery cleans up the exact root. This is a harness-negative check, not a product defect.
- `e101-unit.log`, `e101-vet.log`, `e101-vet-integration.log`, `e101-vet-final.log`: full short Go
  suite and normal/integration vet PASS; final integration vet includes the completion-marker guard.
- `e101-go-all.log`: standard final-image `go-all-test` exit 0; short Go units, complete tagged
  integration (789.334s, including all four E1-01A legs and unchanged G1/G4/G5/fan-out regressions),
  API and OIDC PASS. Topology/load opt-ins and parent-only subprocess helpers retain intended SKIPs;
  this is not a full three-DC or load-profile result.
  Reproduce with `docker compose --profile test run --rm go-all-test` against the configured fleet.

Audit: `ProcessOrgOnce` retains productive claim/zero-liveness/`processBlock`/G2 mutations.
The wrapper changes only observation timing after real successful Prepare/Commit; no runtime hook,
schema, GC activation or fixture-fabricated lifecycle was added. Cassandra authority timestamps are
compared at the existing persisted millisecond precision. Both standard Go runners require all four
legs, and cleanup success is necessary for evidence accounting.

C1/C2 found no physical product violation in this single-DC schedule. Their positive evidence does
not close E1-01 or X1. Recovery enumeration is filtered to the owned org/L and uses local checkpoints;
this does not prove global cursor persistence, fairness, outage handling or three-DC behavior.
Other crash boundaries and the original E1-01 inventory remain for separate bounded PRs.

## P2 correction — preserve failure evidence before teardown

Validated correction source: `c286cfef1`; test SHA-256:
`a30e3ce91eb8a473a48690beff9ea1b7c6685a2169af76c8bca1e5664609bccb`.
The original teardown could recover/delete evidence after a failed assertion. A failed/skipped leg
now stops its worker, captures before/after exact P/D, blocks, orphan/root, lifecycle, candidate,
references, repairs, HEAD and K1 bytes, and does not retry recovery or dispose the fixture.
The JSON is emitted in full to the test log and written atomically outside the failed leg's TempDir
(`tmp/e101-failures` by default, overridable with `SESAMEFS_E101_EVIDENCE_DIR`). Read errors are
recorded as UNKNOWN; preservation still applies when the snapshot is incomplete.

Opt-in preservation also covers inherited upload-artifact, upload-session and library cleanups;
the library remains protected from TestMain's stale sweep. Other fixtures keep existing cleanup.
These real inherited paths require touching two additional existing helper files; no new product
cut, worker behavior, schema, scheduling policy or GC activation change is included.

`TestE101FailureSnapshotSurvivesTeardown` deliberately fails the parent after C2 SIGKILL.
It requires exit 1, no automatic recovery, equal complete before/after snapshots and a surviving
artifact; a fresh OS process/session verifies the live state after child cleanups/TestMain.
Only then does the verifier explicitly recover/dispose its own fixture, and the artifact remains
byte-identical. This is a harness-negative control, not a reproduced product violation.

- `e101-preservation-directed-final.log`: C1/C2 4/4, failure-preservation control and gate PASS.
- `e101-preservation-race.log`: 8/8 C1/C2 legs plus two preservation controls PASS with race.
- `e101-preservation-unit-vet.log`: complete short Go tree and normal/integration vet PASS.
- The initial diagnostic negative caught the inherited session cleanup still erasing blocks/K1;
  the corrected control verifies those survive. The two diagnostic fixture residues were captured
  and explicitly disposed by exact identity (`e101-preservation-diagnostic-disposal.log`).
- `e101-preservation-go-all.log`: corrected standard `go-all-test` exit 0; short Go units,
  complete integration (871.604s, including C1/C2, preservation negative and G1/G4/G5/fan-out),
  API 20/20 suites and OIDC 25/25 tests PASS. Expected topology/load/helper SKIPs remain;
  this is single-DC evidence. Runner image:
  `sha256:97dcfc9006c42a493cfe54b3af81db45045a8254d7e59bcff54feb94feda3299`.
  Its running ID and test source hash matched the frozen correction before collecting evidence.

The THIS-PR failure-preservation P2 is corrected and validated. E1-01 and X1 remain OPEN.
