# G5 expanded three-DC validation, 2026-10-02

> **Re-measured 2026-10-10 ([E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md#full-three-dc-suite-2026-10-10)):**
> on a fresh three-DC fixture at main@5c3163871, 28 of the 30 integration
> failures listed below pass. API is 20/20, OIDC passes and Seafile sync is
> 10/11. `TestBlockMappingAuthority3DC` SKIPs, as it needs its own script.
> `TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles` remains
> intermittent (`ISSUE-SEAFHTTP-UPLOAD-FINALIZE-LEASE-ERROR-500-01`). The new
> failures are classified in E1-16; none is an X1/W2 safety violation. The
> record below is the original 2026-10-02 snapshot.

This record separates complete PASS, observed FAIL and unexecuted phases. The
full suite is not a global PASS, and absence of regressions is not established.
First production is greenfield; no old deployment or mixed-version recovery
was tested. Destructive production GC remains OFF.

Contract clarification (2026-10-03): finite-cycle/restart results demonstrate
bounded pages and wrap for the tested arrivals. They do not demonstrate an
insertion snapshot or unconditional revisits under arbitrary clock skew or
regression. `Until` bounds claim event-time keys; later-published in-range roots
may join the active cycle. The G5 plan now states advancing/bounded-skew clock,
finite pre-cutoff claim and discovery/checkpoint progress assumptions. Clock
synchronization and clock-health activation controls remain OPEN PRE-GC under
`ISSUE-GC-ROOT-CYCLE-CLOCK-ASSUMPTION-01`. No historical PASS is upgraded into
proof of those operational controls, and exact `(P,D)` authority is unchanged.

## Sources and isolation

- Expanded full suite: G5 runtime fa73da717 with the test-only isolation fix
  77de4490a; the later orphan fixture correction is 1a1cb9629.
- Cross-audit correction and directed revalidation: runtime/test HEAD
  88737973ef011fd87b57e0740b3c398cc7cbbc8a. All tracked Go files match the
  disposable test source after LF normalization; no mutation backups remain.
- Cassandra 5.0.9, NetworkTopologyStrategy, RF1 in dc-na/dc-eu/dc-asia;
  MinIO real storage. Backend/test code executes in Docker. Drivers are SH.
- Fresh isolated full-suite fixtures used current-source backends and explicit
  topology. Later independent matrices reused an owned three-DC fixture in
  sequence. A process's global fixture cleanup never overlaps another matrix.
- Only owned nodes were stopped during outage phases. Original user services
  were preserved; owned fixture volumes and local logs are retained.

## Results

| Check | Source scope | Result |
|---|---|---|
| Complete Go tree | Corrected 88737973e | PASS |
| DB/GC race | Corrected 88737973e | PASS, DB 91.815s / GC 4.834s |
| Normal and integration vet | Corrected 88737973e | PASS |
| Fresh zero-epoch grace regression | Before/after correction | Semantic RED at 1a1cb9629; both pre-check and post-claim cases GREEN after withdrawal of retention; handoff only after full fresh grace |
| Required G4 + all current G5 Cassandra/MinIO legs | Corrected 88737973e | PASS, 9.758s; first attempt failed bounded mapping cleanup EACH_QUORUM recv2 and is retained |
| G5 omission control | Corrected 88737973e | PASS: named source gate ran; missing old-life/bounded/fresh-grace evidence produced exact required nonzero exit |
| Full three-DC integration | Expanded suite before cross-audit correction | FAIL, 30 top-level tests, 885.888s; G4 and then-current G5 legs PASS |
| API | Same full-suite fixture | 20 suites executed, 12 PASS / 8 FAIL |
| OIDC | Same full-suite fixture | 25/25 PASS |
| Seafile Docker sync | Same full-suite fixture | 11/11 PASS, 116s |
| G3 physical deletion/isolation/current orphan recovery | 1a1cb9629 fixture correction | All three PASS, 87.702s; prior full-suite orphan failure retained |
| PC-D1B.1 baseline | Independent real 3DC | Complete PASS |
| PC-D1B.3 mapping | Independent real 3DC | Healthy SERIAL cases FAIL; independent outage and recovery phases PASS; final command exits 1 |
| PC-D1B.4 certification window | Independent real 3DC | Complete retry PASS with semantic RED controls; first seed failure retained; R12/M34 remain characterized residuals |
| W2 post-HEAD | Independent real 3DC before correction; same covered classifier runtime | Complete PASS: blind-NA remote HEAD, ancestor after advancement, unavailable-DC UNKNOWN/retention and cursor/anchor resume from NA/EU |
| Sync PutBlock provenance | Same covered runtime | Complete PASS: blind-NA EACH_QUORUM fallback, N=1/10/100/1000 cost and unavailable Asia refusal |
| W2 RepairGuard | Corrected source | Complete PASS: EU-only guard, semantic LOCAL_QUORUM RED, global visibility, permanent-before-clear handoff, unavailable EU UNKNOWN, restoration/cleanup |
| X2 | Corrected source | Complete PASS (1/2/2b/3a/3b), including reference-DC outage and under-declared topology refusal |
| X2 LOCAL_QUORUM / QUORUM | Corrected-source disposable mutants | Both expected semantic RED confirmed, source/ring/hints restored |
| P3 | Corrected source | Complete isolated PASS: unavailable NA refuses publication; EU fence visible in NA; after retirement P2 accepted and stale P1 repair rejected |
| P3 LOCAL_QUORUM / QUORUM | Corrected-source disposable mutants | Both expected semantic RED confirmed: claim acquired, handoff committed and orphan published with NA down; source/ring/hints restored |
| Final readiness | Owned real 3DC | EACH_QUORUM write/read and global SERIAL LWT/settling read PASS from all three coordinators; own probe rows deleted |
| H1 Sync | Corrected source, after readiness | Seed FAIL, EACH_QUORUM recv1, 2.890s; blind window not executed. Two earlier seed failures retained |
| H1 V2, separately attempted | Corrected source | Seed FAIL, EACH_QUORUM recv2, 2.511s; blind window not executed |

The H1 failures occur before the initializers. They provide no complete evidence
for their blind windows and do not establish an initializer safety defect.
Passing gc_stats probes is not proof that subsequent library writes will meet
quorum deadlines. No consistency downgrade or inflated assertion timeout was
used to turn these results green.

## Findings disposition

The confirmed G5 grace regression is corrected by withdrawing referenced same-P
candidate retention and restoring main's settlement behavior. The pre-PREPARED
new-owner settlement race remains OPEN, P1 FOLLOW-UP / PRE-GC. Its future closure
must preserve recovery scheduling independently of fresh zero-ref eligibility.

The scanner contract now describes exact COMMITTED root continuation. The removed
empty-state/day-cursor/90-day referenced-orphan issue is SUPERSEDED /
LEGACY-NOT-REACHABLE under greenfield v1; its counterexample remains documented.
Root publication documentation reflects its actual EACH_QUORUM LWT with SERIAL.

No full-suite failure is attributed automatically to quorum: mapping concurrent
claims and quota revert also had semantic assertion failures. Availability,
these residuals, late P1-dependent publication and late K1 PUT remain E1/PRE-GC
work. Passing a later isolated P3 or orphan fixture test does not replace the
prior full-suite FAIL. No X1/A1/activation gate is closed by this record.

## Full-suite top-level failures

- `TestAdminIdentityProjectionRegression_ReactivateOrganizationRepairsPartialActivation`
- `TestBlockMappingAuthority3DC`
- `TestGC_S3OrphanRecovery_DeletesLingeringObject`
- `TestGC_BlockDeletion_RefusesForeignStorageKey`
- `TestIdentityAuthorityConcurrentCrossDCClaimsHaveOneWinner3DC`
- `TestSyncHeadRejectsNonEmptyParentWhenCurrentHeadMissing`
- `TestUpdateBranchRejectsNonEmptyParentWhenCurrentHeadMissing`
- `TestSyncHeadConflictAutoMergesNonOverlappingEntries`
- `TestUpdateBranchConflictAutoMergesNonOverlappingEntries`
- `TestUpdateBranchSameHeadReturnsOKWithoutProjectionChange`
- `TestSyncHeadSameHeadRepairsMissingOwnerProjection`
- `TestUpdateBranchSameHeadRepairsMissingOwnerProjection`
- `TestSyncHeadSameHeadRepairsMissingOrgProjection`
- `TestUpdateBranchSameHeadRepairsMissingOrgProjection`
- `TestSyncHeadSameHeadRepairsMissingGlobalProjection`
- `TestUpdateBranchSameHeadRepairsMissingGlobalProjection`
- `TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles`
- `TestP3CondemnedIncarnationCannotBeRepaired`
- `TestP3ResidualRaceDoesNotRecreateCanonicalRow`
- `TestP3_WriterInAnotherDatacenterObservesTheFence`
- `TestRevertFileEnforcesPerUserStorageQuota`
- `TestW2SyncNoPutBlock`
- `TestW2NativeHEADAmbiguity`
- `TestW2ProcessKillContinuity`
- `TestW2WorkerRepairLifecycle`
- `TestWebBlockUploadWritesReferenceAndExpiryTogether`
- `TestProvisionalRollbackPreservesTTLRows`
- `TestWebBlockUploadDeduplicatesAcrossLibrariesInSameOrg`
- `TestWebBlockUploadIdenticalBytesUseDistinctOrgKeys`
- `TestX1StrictNonoverlapCharacterization`

The orphan fixture failure was subsequently corrected and passed targeted G3.
P3 fence visibility passed the later isolated matrix. Neither replaces a fresh
complete full-suite result on the corrected HEAD.

API failures: Nested Folders, Nested Move/Copy, File History API, File Preview &
Raw Serving, Tag API, Search API, Repo History API, Encrypted Library Security.

## Local evidence files

Logs are ignored disposable artifacts under tmp/. The durable outcome and source
scope are recorded here; raw files remain on this machine for characterization.

- g5-grace-static-audit-retry.log; g5-grace-gc-compile.log
- g5-grace-3dc-evidence.log; g5-grace-3dc-evidence-retry.log
- g5-grace-omission-connected.log (earlier disconnected control was invalid,
  because no backend was available and the gate never ran)
- full-3dc-continued-driver.log and
  sesamefs-full-3dc-20261002225403-28921/continued-evidence.log,
  continued-sync.log, continued-backend.log
- full-3dc-g3-physical-driver.log; full-3dc-baseline-retry.log
- full-3dc-mapping.log; full-3dc-certification-window.log;
  full-3dc-certification-window-retry.log
- full-3dc-w2-post-head-retry.log; full-3dc-w2-sync-xdc.log;
  full-3dc-w2-repair.log
- full-3dc-x2.log; full-3dc-x2-mutate-local.log;
  full-3dc-x2-mutate-quorum.log
- full-3dc-p3.log; full-3dc-p3-mutate-local.log;
  full-3dc-p3-mutate-quorum.log
- full-3dc-final-readiness.log; full-3dc-h1.log;
  full-3dc-h1-retry.log; full-3dc-h1-final.log;
  full-3dc-h1-v2-only.log
- full-3dc-validation-report.md: detailed chronological local journal.

No new PS1 driver was added. Runtime/test checks and mutations ran in Docker;
mutations were restored before the next matrix. Full-suite results on earlier
sources are preserved explicitly rather than relabeled for the corrected HEAD.

## G5 clock-contract correction verification, 2026-10-03

The correction changes documentation and Go comments only, based on
`f08cd818929923cf910d538d16199de6b5dbdc4f`. A Go scanner comparison ignoring
comments found identical executable token streams in `s3_orphan_root_cursor.go`,
`store.go` and `worker.go`. No scheduler, schema, query or authority change.

A disposable MockStore probe in a rebuilt Go Docker runner captured `Until`,
then published a root with a timestamp between `After` and `Until`. The next
page selected that later-published root: PASS, confirming that the upper key
is not an insertion snapshot. This proves range admission, not infinite
starvation, and is not presented as a real-Cassandra clock-skew experiment.
The probe is retained locally in `tmp/g5-clock-contract-probe.go`; it is not
added to the repository test suite.

The same single runner then executed `go test ./internal/gc -run
"^(TestG5|TestAuditG5LatePublicationJoinsCapturedRange)" -count=1 -v` and
`go vet ./internal/gc`: PASS, container exit 0. Log:
`tmp/g5-clock-contract-check.log` (local, not a published CI artifact).
`git diff --check` passed. The full final-runtime evidence at f08cd818 remains
scoped to that runtime; it is not a new full-suite execution for this
comment/documentation-only correction. No test stacks ran concurrently.

The THIS-PR unconditional wording P2 is corrected. Its operational clock-health
prerequisite remains OPEN PRE-GC/A1; runtime and exact-P safety findings are
unchanged. GC remains OFF and no merge/activation is performed by this check.