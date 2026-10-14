# E1-01C — physical recovery process-crash evidence

Frozen plan: 2026-10-13, base `main@acc6c6f45efdfb0cca7826c943beeb6a7229c316` (#283).
Only C6/C7 of [E1-01](./E1-01-CRASH-EVIDENCE-PLAN.md); E1-01/X1 OPEN, production GC OFF.
Missing process-death evidence is not a demonstrated runtime defect.

The current recovery executes exact retirement -> S3 DELETE -> phase persistence ->
TERMINAL -> orphan/discovery/root removal. Existing G4 post-delete failure tests use mocks;
E1-01A/B kill the work-processing actor before physical recovery, not this recovery actor.

- C6 `after-delete`: pause before the real MarkS3OrphanMappingCleanupPending call, after
  productive DELETE returned success. Parent verifies K1 absent, canonical retired, exact
  COMMITTED orphan in pending_s3, published lifecycle and durable root.
- C7 `after-phase`: pause after that real phase write succeeds, before TERMINAL. Parent
  verifies pending_mapping_cleanup, the same exact D/root/published lifecycle, K1 absent.
- Real fixture upload and ProcessOrgOnce create and settle the committed handoff. A distinct
  recovery process uses owned root enumeration and production RecoverS3Orphans. No CQL state
  fabrication, productive hook, new storage interface or fake successful outcome.
- Each cut has SIGKILL and same-boundary resume controls; parent verifies the durable cut
  before death. Independent fresh-process recovery and another replay must reach TERMINAL,
  remove exact orphan/root, retain canonical/K1 absence, unchanged HEAD and zero refs/repairs/work.
- Reuse the existing scoped subprocess, snapshots, teardown guards and four-leg gate. A failed
  C7 leg must preserve the post-delete continuation through all cleanups; prove live state
  from a fresh session before explicit disposal. Do not require retained bytes/queue after deletion.
- Required gate in both Docker runners rejects omitted legs, unavailable backend and recovery
  child SKIP. Shared fixtures run sequentially. Freeze source/image identities and exact topology.

Budget: one small integration file, minimal shared helper/gate changes, Docker gate and this ledger;
roughly five files and 350 added lines. C8 cleanup substeps, C0/C3, P2 publication and 3-DC residuals
remain separate. No runtime/schema/writer/scheduler/default/activation changes.

Validation: directed A+B+C and preservation controls; race repeat; omission/unavailable/SKIP
negatives; normal/integration vet and complete short Go tree; standard Docker integration/API/OIDC
with G1–G5 controls. Audit authority, cut order, successful-call delegation, independent sessions,
required markers, preservation and exact cleanup before PR. A reproduced product defect gets its
own small fix PR, with the unmodified-source failing schedule retained first.

Results and remaining verification are recorded below; no E1-01/X1 closure claimed.

## Evidence and audit ledger

Frozen implementation: `b34a604b00988828e822aeb0797ebc3d879e82d9`.
SHA-256 read inside the tested image:
- Shared A/B/C harness: `4bf39e38a4c7198b09e4686aa8b8a6513baf1be9de1f75143d22673e2189bdb8`.
- Physical recovery test: `ec253de2dd8d155cd9863635d27881d22dc92abb460e269003709a40062cb6d6`.
Runner `go-integration-test`: `sha256:88283da950fccc4c1e7d23ae4f335543ce9929a29049a05d06e6990f0a9f6ea7`.
Runner `go-all-test`: `sha256:f56f0f48a0774de5bc617beba782fb227eb4eaac91a8a477737d8261c946c4bc`.
E19 backend: `sha256:a53db0b97cb82304f58973aae8d848ad67fa0789489c891b760df4f83223be4e`;
binary SHA-256 `e6facb5f64f3b3104793242cf57f4dbd1a1279d6bb377e5d6d11ec5596aec1e3`.
Productive GC/API/DB/storage/config/dependency sources are unchanged since merged #281.

Linux Docker, Cassandra 5.0.9, actual `datacenter1`/RF1 NetworkTopologyStrategy,
LOCAL_QUORUM/SERIAL configuration, EACH_QUORUM exact reads where the protocol uses them;
SILO `RELEASE.2026-09-16T00-00-00Z`. All backends healthy. E19/node2/node3 GC OFF;
the pre-existing explicitly authorized main development GC is outside the isolated e19 keyspace.
No GC configuration was changed. This is single-DC evidence, not a three-DC/outage proof.

| Cut | Parent durable proof before death | SIGKILL / resume / independent recovery and replay |
| --- | --- | --- |
| C6 after-delete | SERIAL canonical absence; exact COMMITTED/pending_s3 orphan; matching root and published lifecycle; K1 absent | PASS; exact TERMINAL and orphan/root/work absence, K1 remains absent |
| C7 after-phase | Same identity and absence, actual pending_mapping_cleanup; lifecycle still published | PASS; exact TERMINAL and orphan/root/work absence, K1 remains absent |

Original K1 bytes are verified before starting the recovery actor. Preparation, paused recovery,
first recovery and replay are distinct OS processes with fresh sessions. HEAD/refs/repairs remain
unchanged. Authority/retirement/phase/lifecycle/S3 mutations delegate the real implementations.
C7's successful phase return is also
checked against durable state; a nil no-op return cannot satisfy that boundary.

Local ignored `tmp/` evidence:
- `e101c-directed.log`: initial A+B+C 12/12 legs, all three preservation controls and gate PASS (86.376s).
- `e101c-race.log`: final source, race/count=2; A+B+C 24/24 legs and three preservation controls twice
  PASS (332.693s). 18 verified SIGKILLs: 12 positive and six deliberate preservation failures.
- `e101c-unit-vet.log`: complete short Go tree and normal/integration vet PASS.
- `e101c-unavailable.log`: the physical gate alone rejects absent backend (test exit 1).
- `e101c-omission.log`: C6/kill passes, but the gate rejects the three omitted named legs (test exit 1).
- `e101c-s3-negative.log`: only a disposable image copy faults the recovery child's S3 endpoint.
  The real helper SKIP/exit 0 is rejected by the parent; the C6 continuation and external snapshot
  survive every failed-child cleanup. `e101c-s3-negative-disposal.log` observes identical live state
  from a fresh session before explicit owned disposal (PASS); the artifact stays byte-identical.
- `e101c-go-all.log`: standard required runner completed the full short Go tree and mandatory
  integration (959.236s, A+B+C and all preservation controls), API 20/20 suites (103s) and
  OIDC 25/25 tests (one suite, 1s), all PASS. G1/G4/G5 and the global-fanout guard PASS.
  Conditional three-DC/topology/load/helper SKIPs retain their declared scope. The original outer
  process handle was unavailable after client resume; its raw exit code was not recovered.
  These results use the complete component summaries, not an assumed daemon exit status.
  Reproduce on the configured healthy fleet with `docker compose --profile test run --rm go-all-test`.

The first directed launch failed before Go because a login shell reset PATH; it is not test evidence.
The first S3-negative launch used an unsupported Compose mount flag and ran no test; it is not evidence.
Both were corrected without changing product code or weakening any assertion.

Audit scope: five implementation/ledger files plus two short work-index/history updates.
The snapshot verifier retains the original upload SHA-1 explicitly, because K1 is already absent
at C6/C7; disposal must not derive the mapping identity from empty bytes. A/B controls still pass.
Root enumeration and checkpoints retain the inherited fixture-local filtering; no global scheduling,
seek persistence or fairness claim. C8's separate cleanup substeps, C0/C3 and P2 publication remain
outside this PR. No product violation reproduced in these schedules; E1-01 and X1 remain OPEN.

Main update audit (2026-10-14): rebased onto `main@e091d9756ec03c9b0e8aa0fee886ef3027541059`.
Equivalent reviewable implementation commit: `1452f0fb6` (the frozen pre-rebase source above
is retained by a local backup ref). Only CURRENT_WORK/CHANGELOG had conflicts; both main's
new entries and this evidence were preserved. All four test/runner files are byte-identical
in Git to the frozen implementation. New main changes affect documentation and the separate
multi-region bootstrap path, not this executed single-DC runner; no runtime suite was repeated
solely for the rebase. Final relative links, Compose configuration and diff checks PASS.
