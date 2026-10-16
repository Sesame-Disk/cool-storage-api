# E1 combined three-DC validation and reconciliation

## Frozen plan (2026-10-15)

Base: main@221997f4552c2246c87b9b7a475e97e906cc0e36 (#303).
Scope: a single serial Go integration invocation containing E1-3 (3), E1-7 (6),
E1-09 (14), E1-10e (4), E1-11 (6), E1-12 (6), E1-13 (5), plus E1-17 (15):
59 named behavioral legs. Existing process-death children remain enabled, but
family-level isolated-URL delegation is unset so the matrices share one process.

1. Reuse the dedicated healthy Cassandra three-DC RF1/S3 fixture, ordinary GC OFF.
   Snapshot recovery/repair tables and retained object probes before execution.
2. Require all seven existing family evidence gates and the existing completeness,
   inventory, isolation and no-global-GC-fanout checks. E1-3 has no family gate:
   independently verify an explicit 59-name result manifest, not only exit 0.
3. Run the union normally and with race, each in one fresh Go process. Do not
   change productive/test sources or silently increase standard-suite budgets.
   Check exact named PASS events and reject skips, missing cases or failures.
4. Negative controls: each existing gate rejects omitted functional evidence;
   the manifest rejects a filtered E1-3 run. Preserve original per-family
   behavioral mutation evidence by reference, without claiming it was rerun.
5. Audit owned teardown and retained unrelated state before/after. Any reproduced
   independent runtime defect receives its own bounded fix, not scope expansion.
6. Reconcile the historical seven-family fixture failures against current union
   evidence, preserving unsupported-attribution corrections and the other three
   historical integration failures, special-script skip and sync residual.
   Refresh active/recent summaries and tracking; do not claim full-suite PASS.
7. Fetch main before final review, audit links/diff/provenance and open the PR.

This advances E1 three-DC evidence toward X1. It closes neither the full-suite
budget issue nor E1-01, D0/identity audit, X1, PRE-GC or A1. Older retained
one-DC authorities stay untouched. No schema, API, client or GC activation change.

Backward compatible: yes, planned scope is documentation/evidence only.

## Current evidence and historical reconciliation

The normal serial union passes 59/59 named legs in one fresh Go process, with
all seven existing family gates required. No family isolated-URL delegation is
used. E1-12's real SIGKILL/restart and E1-13's independent process/sweep crash
children still run. Completeness, E1-09 isolation, gate inventory and the
no-global-GC-fanout guards pass. An explicit result manifest requires each
behavioral name to have exactly one RUN and PASS, all eight parents and package
PASS, and no FAIL/SKIP event. Exit zero alone is insufficient, especially E1-3.

| Historical E1 family | Cases in this union | Prior causal evidence / scope |
|---|---:|---|
| [E1-3](./E1-3-LATE-PUBLICATION-PRE-D-PROOF.md#three-dc-reproduction-and-scoped-correction-2026-10-15) | 3 | #299 reproduced two failures; local observed-worker topology declaration corrected |
| [E1-7](./E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md#three-dc-reproduction-and-scoped-correction-2026-10-15) | 6 | #300 reproduced two failures; e17CommitGC declaration corrected |
| [E1-09](./E1-09-CROSS-REPO-PUBLICATION-SAFETY.md#three-dc-validation-on-current-main-2026-10-15) | 14 | Current success uses #300; no independent reproduction of every original failure's cause |
| [E1-10e](./E1-10E-W210-RESIDUAL-DISPOSITION.md#three-dc-validation-on-current-main-2026-10-15) | 4 | Current success uses #300; exact COMMITTED disposal corrected separately in #303 |
| [E1-11](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md#three-dc-reproduction-and-scoped-correction-2026-10-15) | 6 | #296 reproduced five worker-visit failures; e111GC declaration corrected |
| [E1-12](./E1-12-KNOWN-LOSER-CRASH-SAFETY.md#three-dc-validation-on-current-main-2026-10-15) | 6 | Unchanged matrix uses corrected e111GC; current PASS is not universal historical causality |
| [E1-13](./E1-13-REPAIR-DISCOVERY-DELAY-SAFETY.md#three-dc-validation-on-current-main-2026-10-15) | 5 | Same e111GC correction; current PASS with discovery-delay/process-crash evidence |
| [E1-17 compatibility](./E1-OWNED-TEARDOWN-PLAN.md) | 15 | Five resurrection shapes, live/COMMITTED/terminal; #303 exact teardown retained |

The first seven rows supply the 44-case historical-family union; E1-17 brings
the batch to 59. The original attribution to openIntegrationProjectionDB was
withdrawn in #293. The proved omissions were in specific native observed-session
wrappers, not shareProjectionDBForTest. That shared helper already loaded config;
no global-helper correction or single cause for every historical failure follows.

The [2026-10-10 full-suite record](./E1-16-POST-D-CONTRACT-ALIGNMENT.md#full-three-dc-suite-2026-10-10)
had ten failing integration parents: these seven E1 families and the three rows
below. The [original G5 thirty-failure ledger](./GC-G5-3DC-VALIDATION.md) is a
separate, older population. Its dated 28 PASS / one SKIP / one failure comparison
is preserved; this 59-case union does not rerun all thirty tests or every gate.

| Independent historical residual | Current disposition in this PR |
|---|---|
| TestGC_WorkerRetiresCanonicalRowAndPreservesPlainEncryptedSiblings | Not rerun; earlier queue-residue failure and 3/3 isolated PASS remain historical evidence |
| TestConcurrentTwoRegionV2UploadsNoLostCommits | Not rerun; earlier HEAD-update 500 and 3/3 isolated PASS remain historical evidence |
| TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles | Not rerun; [Finalize-lease 500](./KNOWN_ISSUES.md#issue-seafhttp-upload-finalize-lease-error-500-01) remains P2 CURRENT-RUNTIME availability |
| TestBlockMappingAuthority3DC | Special-script evidence not run; historical SKIP is not converted to PASS |
| Encrypted binary Seafile sync timeout | Not rerun; whether intermittent remains unestablished |

API/OIDC results in the older full-suite record also retain their original date
and source. Full-suite budget policy, broad three-DC rerun/reconciliation, E1-01
final disposition, D0/identity audits and X1/PRE-GC/A1 remain OPEN. This is current
joint evidence for named E1 fixtures under isolated GC-OFF ownership, not a
complete-deployment, failure-injection or production-readiness certificate.

## Execution, audit and limits (2026-10-15 local / 2026-10-16 UTC logs)

The same 59-case serial batch also passes with race in a separate fresh Go
process: 59/59 named RUN/PASS pairs, eight parents and package PASS, no FAIL,
SKIP or race report. Family delegation stays unset; process-crash subprocesses
are preserved. This is 59 normal plus 59 race cases, not a broader suite result.

Fixture: Cassandra 5.0.9, NTS RF1 in dc-na/dc-eu/dc-asia, pinned SILO storage,
dedicated keyspace and bucket. One current-source API daemon runs in dc-na;
all three API isolation-check URLs alias that same GC-OFF daemon. This is real
three-DC Cassandra evidence, not a three-API-replica or cross-coordinator load
run. No network partition, outage or TTL-policy experiment is added here.

Accepted EACH_QUORUM snapshots show orphan/root/discovery zero before the normal
batch, and those three tables plus published_block_reference_repairs zero after
normal/before race and after race. The ordinary daemon stays GC disabled.
Original per-fixture teardown assertions remain enabled, including #303's
19 exact owned certificates per batch. All post-race physical objects classify
as discovery probes: 65 objects (63 retained, two new). The prior 63 keys remain
present; no physical fixture K remains among the enumerated objects. No manual
fixture disposal or broad delete is used by this PR.

Two inspection attempts are excluded from accepted evidence: the initial repairs
query used a nonexistent table name (the initial repairs baseline is unmeasured),
and an S3 inventory attempted while race fixtures were active encountered a live
test object and exited 1. The correct repairs table is measured before/after race;
S3 classification is performed again only after the test process exits. These
inspection failures are not behavioral findings and are not reported as PASS.

Original per-family admission/guard/worker mutation results remain in the linked
ledgers; they are not rerun or newly certified here. Full integration, all other
mandatory evidence gates, API/OIDC, standard one-DC suite, frontend/client sync,
special identity script and the historical intermittent cases are not rerun.
The existing standard-suite timeout is unchanged. A serial healthy-fixture union
cannot prove arbitrary scheduling fairness, production activation or absence of
interference with every unselected test.

Provenance: main@221997f4552c2246c87b9b7a475e97e906cc0e36; frozen plan
224e45368; image
`sha256:f7d0c878d10955af485be63a87336a41895d4c457a6325767194186320657ff4`.
The internal source tree remains
`5b058e212fdd451b6dac0339d8b52e096066e947` in main and this branch. No productive
or test source is instrumented or mutated. Local ignored JSON events, exit codes,
manifest/verification scripts and snapshots are under `tmp/combined-*`.

The Docker runner requires the seven family gates listed below. It unsets
SESAMEFS_E19_ISOLATED_URL, SESAMEFS_E110E_ISOLATED_URL,
SESAMEFS_E117_ISOLATED_URL, SESAMEFS_E111_ISOLATED_URL,
SESAMEFS_E112_ISOLATED_URL and SESAMEFS_E113_ISOLATED_URL. Other REQUIRE gates
are outside this bounded batch and unset. Inside the configured isolated Docker
container (not on the host):

```bash
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
unset SESAMEFS_E19_ISOLATED_URL SESAMEFS_E110E_ISOLATED_URL SESAMEFS_E117_ISOLATED_URL
unset SESAMEFS_E111_ISOLATED_URL SESAMEFS_E112_ISOLATED_URL SESAMEFS_E113_ISOLATED_URL
export SESAMEFS_REQUIRE_E17_SYNC_RECVFS_CHARACTERIZATION=1
export SESAMEFS_REQUIRE_E19_CROSS_REPO_EVIDENCE=1
export SESAMEFS_REQUIRE_E110E_UNPUBLISHED_RESURRECTION_EVIDENCE=1
export SESAMEFS_REQUIRE_E111_REPAIR_LIVENESS_EVIDENCE=1
export SESAMEFS_REQUIRE_E112_KNOWN_LOSER_EVIDENCE=1
export SESAMEFS_REQUIRE_E113_DISCOVERY_DELAY_EVIDENCE=1
export SESAMEFS_REQUIRE_E117_RESIDUAL_RESURRECTION_EVIDENCE=1
run='TestE13LatePublicationPreDProof|TestE17SyncRecvFSBeforePutBlock|TestE19CrossRepoPublication|TestUnpublishedResurrectionSourceAdmission|TestCurrentRuntimeRepairLiveness|TestKnownLoserCrashSafety|TestRepairDiscoveryDelaySafety|TestE117ResidualResurrectionSourceAdmission'
checks='TestE17SyncRecvFSEvidenceRequiresEveryNamedLeg|TestE19EvidenceRequiresEveryNamedLeg|TestE19IsolationFailsClosed|TestUnpublishedResurrectionSourceAdmissionRequiresEveryNamedLeg|TestCurrentRuntimeRepairLivenessCompleteness|TestKnownLoserCrashSafetyCompleteness|TestRepairDiscoveryDelaySafetyCompleteness|TestE117ResidualResurrectionRequiresEveryNamedLeg|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite'
go test -json -tags integration -count=1 -timeout 15m \
  -run "^($run|$checks)$" ./internal/integration
# Repeat in a fresh process with -race, keeping count=1 and the same selector.
```

The explicit manifest also requires the three E1-3 names noGCWriterControl,
claimWinsBeforeLateRepair and reachableSettlementWinsDuringProof; the frozen
named arrays provide the other 56 names (copy/move times seven E1-09 phases;
five E1-17 shapes times three states). Missing/repeated evidence is rejected.

Final negatives: each of the seven gates exits 1 with its specific missing-leg
message when only the inventory check is selected. An E1-3 noGCWriterControl-only
run exits 0 in Go, but the exact-name manifest rejects the missing
claimWinsBeforeLateRepair (and therefore the batch is not accepted). No new
repository guard is introduced. These are seven expected Go failures and one
external completeness rejection, not eight failing functional tests.

Final independent EACH_QUORUM counts are zero for orphan/root/discovery/repairs.
S3 has 66 classified discovery probes: all original 63 keys remain and three
probes were added (normal, race, filtered control). No manual fixture disposal.
Daemon status is enabled=false, last_worker_run=never. Dedicated stack/runner
are stopped with volumes retained; the older one-DC stack is not started or
modified. Main was refreshed and remains 221997f45; no rebase is needed.

Final audit: 345 relative file links resolve, the documented selector matches
the executed eighteen top-level tests, and diff whitespace checks pass. Only
Markdown files change; the internal source tree matches main. No local audit
blocker was found. Independent CI review and full-suite PASS are not claimed.
