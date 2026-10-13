# E1-01 — bounded crash-evidence plan

Status: PLAN / source inventory, 2026-10-13. Base: `main@73b7bcc702c155a02d721e6132cce4ac001df14e` (#279).
This PR changes documentation only. It records no new crash result, fixes no runtime defect and closes no gate.
X1 remains OPEN; production GC remains OFF. Deliberate destructive GC in an isolated development fixture is required for the drills.

## 1. Question and scope

Can the **current** worker lose, revoke or misapply exact physical-life authority across a process crash?
Evaluate the E1-01 row of [the E1 matrix](./E1-FULL-GC-PUBLICATION-RECOVERY.md#frozen-matrix),
under [the supported first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
Use one compatible release, real Cassandra and SILO in Linux Docker. P is `(storage_class, storage_key)`;
D is `(gc_claim_id, gc_claimed_at)`. Scope every observation and fault to `(org, L, P1, D1)`.

The productive sequence is:

```text
Worker.processBlock: claim + global liveness -> PrepareBlockDeleteOrphan
  [root -> PREPARED orphan] -> CommitBlockDeleteOrphanHandoff
  -> PromoteBlockDeleteOrphan [published lifecycle -> COMMITTED orphan]
  -> FinalizeBlockDelete [canonical retirement] -> candidate/queue settlement
Worker.RecoverS3Orphans: exact durable root -> canonical/lifecycle verification
  -> exact retirement -> DeleteBlockByStorageKey(K1)
  -> MarkS3OrphanMappingCleanupPending -> TerminateBlockDeleteLifecycle
  -> DeleteS3Orphan [orphan -> by-day discovery -> recovery root]
```

Trace: [worker](../internal/gc/worker.go), [Cassandra store](../internal/gc/store_cassandra.go).
The historical `pending_mapping_cleanup` phase now finishes lifecycle metadata; do not add a mapping-delete requirement.
`StartBlockDeleteOrphan` and delete-before-Finalize candidate harnesses are not the current worker protocol.

## 2. Existing evidence: what it does and does not prove

Levels: **U** = mock/source guard; **R** = real-service state/replay; **K** = OS-killed process and independent recovery.
An R test creating a fresh Worker is useful durable-state evidence, but is not K. A test name containing `Crash`
is not a process-death observation. Existing PASS logs retain their original source/topology scope.

| Cut in current sequence | Existing evidence to inspect/reuse | Remaining qualification |
|---|---|---|
| C0: root persisted, before canonical PREPARED | U: `TestG1RootWithoutCanonicalIsRetained`, `TestG2RootOnlyRecoveryNeverDeletesAbsentCanonical`; R: G1 `root without canonical survives worker restart and remains durable` | Root-only retention is safe, not an invented obligation to reclaim. Listed R test uses mock storage and a fresh worker, not K. |
| C1: PREPARED returned, before handoff commit | U: `TestG2PreparedAbortCleansOnlyPreparedState`, `TestG2AbortAndCommitRaceHasOneIrreversibleWinner`; R: `TestG2AbortAndCommitRaceAtRealCassandra` | Current abort/commit authority is covered; OS death of the productive worker at this cut is not established by these tests. |
| C2: handoff committed on blocks, before promotion | U: `TestG2RecoveryPromotesCommittedHandoffAfterCrash` | Exact stored D is covered in a mock. The similarly named P4B real-Cassandra crash tests call historical `StartBlockDeleteOrphan`; they are API controls, not this current-worker K drill. |
| C3: published lifecycle, before orphan COMMITTED CAS | U: G2 ambiguous-promotion/no-touch cases; R: G1 exact identity/replay | Re-read promotion order; do not infer this internal cut from a completed promotion. K remains unestablished in the cited controls. |
| C4: orphan COMMITTED, before canonical retirement | U: `TestG3CrashBeforeFinalizeConverges`; R: G4 productive handoff/coexistence | G3 models restart; G4 calls store steps directly. Neither kills the current worker at this cut. |
| C5: canonical retirement applied, before candidate/queue cleanup | U: `TestG3CrashAfterFinalizeAppliedConverges`, G3 candidate-cleanup failure cases; R: G4/G5 recovery of retired P1 beside P2 | Preserve exact orphan/lifecycle authority. Do not demand that a PREPARED-only root prove retirement. |
| C6: S3 DELETE succeeded, before phase persistence | U: `TestG4RecoveryPostDeleteFailuresPreserveReplacement/persist_phase` | Pins repeat DELETE of the same immutable K1 and intact P2. Old X1 `postDeleteCrash` deletes before Finalize: retain it as historical characterization, not current ordering evidence. |
| C7: phase persisted, before terminal lifecycle | U: `TestWorker_RecoverS3Orphans_CompletesPendingMappingCleanupWithoutS3`, G4 post-delete cases | Exercise productive phase/lifecycle ordering if a stronger current-cut proof is still missing. |
| C8: terminal lifecycle, during orphan/discovery/root removal | U: `TestG1TerminalLifecycleCleansRootAfterCanonicalLoss`, `TestG1DeleteRemovesProjectionWhenCanonicalIsAlreadyMissing`; R: G1 terminal-settlement case | Keep each cleanup substep separately inventoried; do not collapse a partially removed orphan and a root-only terminal row. |
| G5 scheduling loss/restart (cross-cutting control) | U: `TestG5CancellationDoesNotCheckpointAnUnattemptedRoot`, `TestG5FailedPrefixDoesNotStarveAfterRestart`; R: `TestG5CassandraMinIOOldLifeWithoutDayScheduling`, `TestG5CassandraMinIOBoundedRecoveryAcrossRestart` | Real durable seek/restart evidence exists. It does not establish K at every handoff cut or a wall-clock recovery bound. |

Sources: [G1 U](../internal/gc/g1_orphan_exact_identity_test.go), [G2 U](../internal/gc/g2_prepared_committed_test.go),
[G3 U](../internal/gc/g3_committed_canonical_retirement_test.go), [G4 U](../internal/gc/g4_physical_life_coexistence_test.go),
[G5 U](../internal/gc/g5_recovery_scheduling_test.go), [phase U](../internal/gc/s3_orphan_recovery_test.go),
[G1 R](../internal/integration/g1_orphan_exact_identity_test.go), [G2/P4B R](../internal/integration/p4b_claim_orphan_authority_test.go),
[G4 R](../internal/integration/g4_physical_life_coexistence_test.go), [G5 R](../internal/integration/g5_recovery_scheduling_test.go),
[historical X1](../internal/integration/x1_strict_nonoverlap_characterization_test.go).
The 37 W2 wire/SIGKILL legs kill **writers**, not GC at C0–C8; reuse their subprocess mechanics, not their coverage label.

This is a source inventory, not an exhaustive K result. Before adding a drill, search current tests and accepted logs
for that exact cut, process, storage backend, durable observations and recovery entry point. Record any additional
positive proof. Missing evidence is not a demonstrated runtime bug and does not mandate a new mechanism.

## 3. First execution PR: C1 and C2 only

Deliver one test-only PR covering two contiguous G2 boundaries. Do not implement C0/C3–C8 in that PR.
If accepted current evidence already proves one cut, cite and reuse it rather than duplicating it.

1. Freeze the execution SHA. Rebuild/verify the tested current sources. Record image/binary identity,
   Cassandra topology/consistencies and SILO version; do not assume a healthy old container runs that SHA.
2. Use a fixture-owned org/keyspace and objects with no peer destructive worker. Verify GC configuration,
   topology and service readiness before collecting evidence. Use the existing isolated e19 machinery where appropriate.
3. A child runs **production `Worker.ProcessOnce`**, including the actual claim, liveness probe,
   Prepare/Commit/Promote path. Pause after successful PREPARED (C1), or after confirmed handoff commit
   and before Promote (C2). Prefer an integration-test `GCStore` wrapper that delegates the actual Prepare/Commit
   call and pauses only its confirmed return for the owned identity; no new productive hook is planned.
   Add a minimal `integration`-tagged seam only if that existing interface cannot hold the cut.
   Do not change productive semantics or fabricate return outcomes.
4. The parent independently confirms the exact durable precondition before issuing Linux `SIGKILL`.
   C1: exact root + PREPARED, handoff not committed, K1 present. C2: the **blocks handoff** is committed
   for D1, while the orphan may still be PREPARED and lifecycle not yet published. Do not demand
   orphan COMMITTED as the C2 precondition. Assert the exit really is SIGKILL; child defers cannot count as cleanup.
5. Start recovery in a **different process with a new DB/storage session**, through current G5 root enumeration
   and the production worker. Inspect before fixture cleanup. C1 should abort only the uncommitted D1
   without deleting K1; subsequent authorized work is a separate transition. C2 must preserve/recover D1,
   promote the orphan and finish only K1 after confirmed retirement. UNKNOWN retains authority and reports uncertainty.
6. Run a no-kill successful control for each cut and an idempotent independent replay. Run the existing G4
   coexistence regression unchanged as a separate control for intact K2/metadata/references after D1 recovery.
   Do not add another kill cut, P2 publication funnel or extra fixture to this first evidence PR.
7. Parent-owned teardown completes legitimate committed authority before removing fixture lifecycle rows.
   On failure preserve the before/after snapshot and identify the residue; cleanup must not erase the failure.

Use productive setup/fault order; no direct CQL fabrication of a committed, deleting, terminal or missing-lifecycle
state as an E1 counterexample. Direct scoped reads/assertions and final fixture cleanup are permitted.
Test-only wrappers may observe/delegate current calls; they must not invent successful outcomes or substitute a fake store.

Budget: two kill legs + their successful/replay controls, one fixture/helper, minimal boundary instrumentation,
one Docker runner/gate and a short evidence ledger. Target at most five touched files and roughly 350 handwritten
lines for that execution PR. If reuse cannot keep it reviewable, split the two cuts into separate PRs;
never delete assertions or hide a new framework to meet a line budget.
No schema, config-default, writer, scheduling or GC algorithm changes belong to this evidence PR.

## 4. Oracles and stop rules

- **X1 RED:** current reachable code reinstalls P1, revokes/postpones committed D1, publishes HEAD content
  depending on retired P1, deletes/retires P2 under D1, or commits D while an authoritative live dependency exists.
- **Recovery defect:** a legitimate committed authority is lost or cannot resume after services recover;
  record the exact failed obligation separately from physical safety. One timeout is not proof of non-convergence.
- **Safe retention:** UNKNOWN, a root without sufficient authority, or P1-dependent publication refused with
  unchanged HEAD is not physical RED. Bytes returning alone and post-D logical refs(L) are not physical resurrection.
- **Harness failure:** cut not reached, wrong topology, peer GC consumed the fixture, service unavailable,
  missing gate or teardown interfered. Return nonzero/UNPROVEN, fix only the harness cause; do not relabel PASS.

On any real failure, first reproduce against the frozen unmodified runtime and retain the log/snapshot.
Write the smallest sequence over current calls; distinguish product, fixture, availability and PRE-GC retention.
A reproducible product defect gets **one separate fix PR per root cause**, then the same failing schedule
must turn GREEN with its controls. Do not combine an unrelated fix with crash instrumentation or expand architecture
because a gap exists. Non-reproduction alone cannot close an obligation: use reviewable positive evidence or an
explicit, justified disposition under the existing contract. No synthetic future caller, rollout or bypass is a finding.

## 5. Later slices and gates

After C1/C2 evidence is reviewed, select only still-unproven current cuts from the inventory:
canonical-retirement C4/C5 in one small PR; physical-delete/terminal cleanup C6–C8 split at the phase or terminal boundary;
internal root/promotion cuts C0/C3 only if the final inventory shows a distinct uncovered obligation.
These are conditional evidence slices, not pre-authorized mechanisms or a promise that three PRs close E1-01.

Each result records source SHA, command/exit, exact org/L/P/D, actor/process/DC, pre-kill marker,
SERIAL/EACH_QUORUM observations where the protocol uses them, K1/K2 bytes, refs/repair/HEAD state,
independent recovery and teardown result. A passing process must not omit a required named leg.
The runner scopes mandatory gates to the selected legs and proves omission/unavailability returns nonzero.
Run shared-fixture suites sequentially. Bound the runner; classify a timeout as UNPROVEN before changing its budget.

For a test-only execution PR: directed kill/control/replay matrix, appropriate race repeat and relevant existing
G1–G5 regressions, normal/integration vet, then the standard required Docker integration/API/OIDC checks before merge.
A runtime fix additionally runs the full Go tree and relevant API/Sync/three-DC regression for its actual changed path.
One-DC evidence is labelled one-DC; no destructive fixture may bypass the topology gate to manufacture 3-DC progress.
E1-01 closes only when every applicable inventory obligation has an explicit positive proof or justified disposition,
and final review reconciles it with D0 §25. It does not close all X1, PRE-GC or A1.

Separate work: 3-DC residuals, identity claim-table audit, D0 §25 final reclassification, Phase 5/6,
hard-delete fencing and retention convergence. Existing development roots without lifecycle need their own exact
provenance audit; an antecedent in E1-15B is not causality and does not authorize deleting unidentified roots.

## 6. Audit of this planning PR

Check every cited test exists and its mechanism matches the U/R/K label; trace every cut against the current worker/store.
Check current ordering versus the historical APIs/harness, PREPARED versus committed handoff versus orphan COMMITTED,
physical safety versus progress, isolated fixture/cleanup, and unchanged gate states.
Verify relative links, `git diff --check`, documentation guards and a two-file documentation-only diff.
Record validation below before publishing. No new test or runtime behavior is needed to validate this document.

Validation (2026-10-13): every relative file link and named test function resolves; current worker/store order
and U/R/K limitations reviewed; staged diff checked (two documentation files only), `git diff --cached --check` PASS.
Docker image rebuilt with `Dockerfile.gotest`; `go test -count=1 -timeout 5m -run '^Test(G[1-5]|X1PhysicalLifeHandoff)' ./internal/gc` PASS.
This validates existing unit/source/document guards, not a new real-service crash drill or E1-01 closure.
