# E1-01 — bounded crash-evidence plan

## Current acceptance disposition (2026-10-16)

[Final E1 decision](./E1-FINAL-DISPOSITION.md#final-acceptance-decision-2026-10-16)
accepts **named A-I healthy-service one-DC process cuts** on maintainer merge;
before merge this is a proposal. This includes PREPARED without projection,
not every internal durable write or topology/fault cross-product. Root-only C0
retention is not autonomous recovery. Qualified three-DC evidence is separate.
Scoped X1 closes only on that acceptance; PRE-GC/A1 stay OPEN, ordinary GC OFF.
The dated inventory and execution plans below retain their historical status;
the final decision supersedes their X1/E1-01 pending conclusions within this scope.

Historical inventory status: SOURCE INVENTORY / OPEN, reconciled 2026-10-15 against `main@2e74e4cad215b1b59ca1fdb0030e21d0376eb67c` (#294).
Original planning base: `main@73b7bcc702c155a02d721e6132cce4ac001df14e` (#279); its dated inventory and execution plan remain below.
The original planning/inventory PRs changed documentation only. Execution results
belong to the linked A/B/C/D/E/F/G/H ledgers; this inventory closes no gate.
X1 remains OPEN; production GC remains OFF. Deliberate destructive GC in an isolated development fixture is required for the drills.

## 0. Current reconciliation and next bounded execution (2026-10-14)

The source-inventory step compares
current Cassandra/worker call order with merged process evidence and existing
U/R controls. No new product counterexample was found in this review; missing
process evidence remains an evidence obligation, not a demonstrated runtime bug.

### 0.1 Recorded evidence by exact cut

All entries below have recorded Linux Docker process-death evidence on one DC.
`P1` is the exact physical target, `D1` its claim, and `K1` its immutable object.
The named tests link to current instrumentation/oracles; the A–I ledgers retain
frozen versions, outcomes, negative controls and limitations. These are recorded
results, not new executions in this reconciliation PR.

| Cut | Current hold and independent durable precondition | Recorded kill/recovery result |
|---|---|---|
| C0 | [H](../internal/integration/e101h_root_before_prepared_crash_test.go): successful exact root INSERT, before PREPARED submission; uncommitted blocks claim, root present, orphan/discovery/lifecycle absent, canonical/K1 intact | [#294 / H ledger](./E1-01H-ROOT-BEFORE-PREPARED-CRASH.md): fresh recovery visits absent orphan/unpublished lifecycle and retains the complete snapshot while paused and after SIGKILL/replay. Resume reaches TERMINAL. Later explicit fixture disposal is not automatic recovery. |
| C0→C1 internal | [I](../internal/integration/e101i_prepared_before_discovery_crash_test.go): successful exact PREPARED INSERT before discovery publication; root/PREPARED present, discovery/lifecycle absent, uncommitted claim and canonical/K1 intact | [I ledger](./E1-01I-PREPARED-BEFORE-DISCOVERY.md): SIGKILL followed by fresh root recovery republishes discovery, aborts only uncommitted D and preserves canonical/K1; fresh replay is harmless. Resume completes normal deletion. One DC; no runtime defect reproduced. |
| C1 | [A](../internal/integration/e101_g2_process_crash_test.go), `prepared`: successful Prepare return before Commit; exact PREPARED/root, uncommitted blocks handoff, no lifecycle, K1 intact | [#281 / A ledger](./E1-01A-G2-PROCESS-CRASH.md): abort only uncommitted D1; preserve canonical/K1 and repair authority; replay harmless. Resume completes productive deletion. |
| C2 | A, `committed`: successful blocks handoff before Promote; exact PREPARED/root, committed D1, no lifecycle, K1 intact | A ledger: independent recovery promotes/retires exact D1/P1, deletes K1 and reaches TERMINAL; replay harmless. |
| C3 | [G](../internal/integration/e101g_published_prepared_crash_test.go): successful exact lifecycle INSERT before orphan COMMITTED CAS; committed blocks handoff, PUBLISHED lifecycle, PREPARED orphan, canonical/K1 intact | [#292 / G ledger](./E1-01G-PUBLISHED-PREPARED-CRASH.md): orphan recovery runs before queue settlement, exercises PREPARED promotion and reaches TERMINAL; replay settles exact work. |
| C4 | [B](../internal/integration/e101b_g3_process_crash_test.go), `before-retirement`: successful Promote return before Finalize; COMMITTED orphan/root, PUBLISHED lifecycle, committed blocks and K1 intact | [#283 / B ledger](./E1-01B-G3-PROCESS-CRASH.md): fresh org-scoped queue replay retires P1 and settles work while preserving continuation/K1, then independent physical recovery reaches TERMINAL. |
| C5 | B, `after-retirement`: successful Finalize before candidate/queue settlement; blocks absent, exact COMMITTED orphan/root/PUBLISHED lifecycle and K1 intact | B ledger: queue replay settles retained work without altering continuation/K1; independent physical recovery reaches TERMINAL. |
| C6 | [C](../internal/integration/e101c_physical_recovery_crash_test.go), `after-delete`: before phase call after real S3 DELETE; blocks/K1 absent, COMMITTED/pending_s3 orphan, root and PUBLISHED lifecycle | [#287 / C ledger](./E1-01C-PHYSICAL-RECOVERY-CRASH.md): fresh recovery repeats the exact physical-life operation and reaches TERMINAL; replay preserves absence. |
| C7 | C, `after-phase`: successful phase return before terminal transition; same identity/absence, pending_mapping_cleanup and PUBLISHED lifecycle | C ledger: fresh recovery completes lifecycle metadata and reaches TERMINAL; replay harmless. Historical phase name does not require a mapping DELETE. |
| C8 entry | [D](../internal/integration/e101d_terminal_cleanup_crash_test.go): before aggregate DeleteS3Orphan; exact TERMINAL, COMMITTED orphan/discovery/root/token present, blocks/K1 absent | [#288 / D ledger](./E1-01D-TERMINAL-CLEANUP-CRASH.md): independent metadata recovery removes exact orphan/discovery/root and preserves TERMINAL/absence on replay. |
| C8 after orphan DELETE | [F](../internal/integration/e101f_discovery_retained_crash_test.go): successful owned orphan DELETE before discovery DELETE; TERMINAL, orphan absent, exact discovery/root/token retained | [#291 / F ledger](./E1-01F-DISCOVERY-RETAINED-CRASH.md): root-only recovery uses durable token to remove discovery then root; replay preserves TERMINAL/absence. |
| C8 after discovery DELETE | [E](../internal/integration/e101e_root_only_terminal_crash_test.go): successful owned discovery DELETE before root DELETE; TERMINAL, orphan/discovery absent, exact root/token retained | [#290 / E ledger](./E1-01E-ROOT-ONLY-TERMINAL-CRASH.md): fresh root-only recovery removes retained root; replay preserves TERMINAL/absence. |

All cuts have same-boundary resume controls and independent post-action checks.
Shared [e101RunMatrix/e101AssertRecovered](../internal/integration/e101_g2_process_crash_test.go)
verify SIGKILL, fresh recovery/replay and HEAD/refs/repairs; H's killed branch
instead requires unchanged safe retention. After final root deletion the settled
state and fresh replay are asserted; no remaining continuation at that point is
identified here as a new crash drill. Eight preservation controls separately
verify failed-fixture snapshots/live state before explicit owned disposal.

### 0.2 C0/C3 source obligations and limits

In [PrepareBlockDeleteOrphan](../internal/gc/store_cassandra.go), the exact blocks
claim is checked, the root is published and read back, then the PREPARED orphan
LWT is attempted. A C0 drill must independently confirm the exact handoff is
still uncommitted, its root exists and its orphan does not; entering Prepare or
observing an earlier claim read cannot prove those facts under concurrency.
That root alone is not a certificate to delete K1. Worker root recovery observes the exact
lifecycle when the orphan is absent and retains an unsettled root. Deleting that
root merely because the orphan is missing could strand a late PREPARED producer.
`TestG2RecoveryRetainsRootAcrossLatePreparedProducer` and
`TestG2RecoveryRetainsRootWhilePreparedOwnerCanStillCommit` model this with mocks.
G1's `root without canonical survives worker restart and remains durable` uses
real Cassandra but historical StartBlockDeleteOrphan, direct scoped row removal,
MockStorageProvider and a new worker in the same process. It is R, not a
productive C0/SILO/SIGKILL observation. H now records that narrow process cut; C0 remains OPEN for final evidence disposition or an
explicitly justified disposition; automatic root removal is not proposed.

In PromoteBlockDeleteOrphan, confirmed blocks handoff and exact PREPARED orphan
precede insertBlockDeleteLifecycle; the orphan PREPARED-to-COMMITTED CAS follows
that insertion. C3 has durable D1/P1/root/PUBLISHED plus a PREPARED orphan, unlike
C2's pre-Promote boundary and C4's completed promotion. Recovery classifies the
committed blocks handoff and retries promotion before canonical retirement.
`TestG2RecoveryPromotesCommittedHandoffAfterCrash` models that route in a mock.
The ambiguous-promotion mock flag returns before lifecycle insertion; its test
must not be labelled a C3 middle-of-promotion witness. The G1 source guard checks
historical StartBlockDeleteOrphan ordering, not the current Prepare method.
These U/R controls supply no C0/C3 K claim by themselves; G separately records
the current-producer C3 process cut. H records current-producer C0 safe retention, not automatic reclamation. Final dispositions and overall E1-01/X1 remain OPEN.

### 0.3 Selected next drill: one internal C8 root-only TERMINAL cut

Historical selection from #289, now merged in #290. A later selected drill is
section 0.4. Execution record: [E1-01E](./E1-01E-ROOT-ONLY-TERMINAL-CRASH.md);
results and validation limits are in that ledger. Its bounded plan is preserved below.

[DeleteS3Orphan](../internal/gc/store_cassandra.go) removes, in order, the exact
orphan, the exact by-day discovery row, then the recovery root. The selected E slice
is only **after the discovery DELETE returns successfully, before the root
DELETE starts**. Root-only recovery takes a different branch from D's intact
orphan: a TERMINAL lifecycle permits metadata settlement using the root's durable
first_seen_at. G1 controls model this branch; D's kill-before-call does not kill
an actor already inside it. The earlier orphan-only-delete cut stays separate.

Reuse D's fixture, SIGKILL/resume, independent recovery/replay and failure
preservation. Prefer the existing [NewWithObservers](../internal/db/db.go)
diagnostic session to pause the successful owned discovery query; a GCStore
wrapper around the aggregate call cannot hold an internal Cassandra substep.
The observer's result alone is not the oracle: the parent must independently
verify TERMINAL, orphan/discovery absent, exact root/first_seen_at retained,
canonical/K1 absent, unchanged HEAD, zero refs/repairs and settled work before
SIGKILL. Require exactly that state in both kill and resume controls.

A fresh process must enumerate the retained root, finish metadata settlement,
and replay without restoring K1 or changing TERMINAL/HEAD. Failure must preserve
the root and external snapshot before explicit owned disposal. Keep the required
gate fail-closed for missing/Skipped legs and unavailable recovery; prove omission
and both repetition orders. Scope: one cut, two positive legs, existing preservation
control, existing harness and no runtime/schema/writer/scheduler/GC-default change.
Do not fabricate authority with direct setup CQL; add no new productive hook if
the existing observer can hold the cut. If it cannot, report that instrumentation
limit before expanding scope. The E ledger records the observed drill; this
inventory does not expand its scope or close a gate.
The earlier C8 substep is tracked in section 0.4. C0/C3, global scheduling and three-DC residuals remain OPEN.

### 0.4 Selected next drill: discovery retained after orphan deletion

[E1-01F](./E1-01F-DISCOVERY-RETAINED-CRASH.md) is bounded to the successful exact
orphan DELETE before discovery DELETE. Parent must prove TERMINAL, orphan absent,
exact discovery/root and original token present, canonical/K1 absent and settled
work. Fresh-process recovery must remove the still-present projection using the
root token, then remove the root; replay must preserve TERMINAL and unchanged HEAD.
E proves the later root-only state; F records this outstanding discovery deletion
with its validation limits and frozen sources in the linked ledger.
Reuse observer/harness and fail-closed gate, with SIGKILL/resume and preserved
failure evidence. No runtime change presumed; C0/C3 and three-DC remain separate.

### 0.5 Selected next drill: PUBLISHED lifecycle, PREPARED orphan

[E1-01G](./E1-01G-PUBLISHED-PREPARED-CRASH.md) records exactly C3, after the
successful lifecycle INSERT inside PromoteBlockDeleteOrphan and before the
orphan's COMMITTED CAS. Parent must independently prove committed blocks D1/P1,
PUBLISHED lifecycle, PREPARED orphan, original root/discovery/token and canonical/
K1 intact, with owned candidate/queue retained and zero refs/repairs. Use the real
ProcessOrgOnce claim/liveness/handoff and existing query observer, not direct CQL
setup. Independent orphan recovery goes before separate queue settlement/replay
so the killed leg exercises PREPARED recovery. SIGKILL/resume, preserved failure,
fail-closed gate and Docker validation are required. No runtime change presumed;
C0, three-DC and final gate dispositions remain separate.

### 0.6 C0 execution and next bounded reconciliation (2026-10-15)

[E1-01H](./E1-01H-ROOT-BEFORE-PREPARED-CRASH.md) adds only the root-persisted,
before-PREPARED process cut. Parent durable reads distinguish it from C1 and
require an uncommitted exact claim, absent orphan/lifecycle/discovery and intact
canonical/K1. Fresh productive root recovery must visit and classify this exact
unsettled authority and retain the entire snapshot, both while its producer is
paused and after confirmed SIGKILL/replay. Resume must still finish normally.
Explicit fixture disposal revokes the dead producer's exact uncommitted claim
before root cleanup; it is not an automatic recovery/convergence witness.

The next bounded step selected by H was documentation/source reconciliation of merged A–H
against each C0–C8 obligation and qualification, before selecting any new runtime
work; its outcome is now recorded in section 0.7. Do not infer closure from counting cut labels. Three-DC residuals, global
scheduling and maintainer-owned E1-01/X1 dispositions remain separate.

### 0.7 Reconciliation outcome and open dispositions (2026-10-15)

The current worker/store order matches the table: Prepare checks the exact
claim, publishes/reads its root, then submits PREPARED; Commit precedes Promote;
Promote inserts PUBLISHED before the orphan COMMITTED CAS; Finalize precedes
candidate/queue settlement. Recovery deletes exact K1 before phase persistence,
then terminates the lifecycle before DeleteS3Orphan removes orphan, discovery
and root in that order. Source: [worker](../internal/gc/worker.go),
[Cassandra store](../internal/gc/store_cassandra.go). The K evidence reconciled
here corresponds to the named C0–C8 observation points; it does not establish
coverage of every internal durable write between them.

In particular, Prepare can persist the exact PREPARED orphan LWT before
`ensureS3OrphanProjectionResult` publishes `gc_s3_orphans_by_day`. This leaves
root and PREPARED present with discovery still absent. H pauses before the
orphan INSERT; A pauses after successful Prepare return, including projection
publication. Neither is specific K evidence for this intermediate window. The additional
[E1-01I drill](./E1-01I-PREPARED-BEFORE-DISCOVERY.md) now records that exact
window through the current producer, SIGKILL, independent root recovery and replay
on one DC.
Root recovery calls `PublishS3OrphanDiscovery` before canonical recovery, but
that route alone does not prove process-death behavior at this window. Its
specific process evidence is recorded in I; final E1-01 disposition and broader
qualifications remain pending. No runtime defect was reproduced by that drill.
This is a source/evidence reconciliation, not proof of every error/concurrency schedule.

| Obligation | Current disposition and next evidence needed |
|---|---|
| Enumerated C0–C8 process cuts | Recorded K evidence on one DC, with resume/replay and failure preservation. [Final audit](./E1-FINAL-DISPOSITION.md) recommends scoped healthy-service CLOSED-EVIDENCE; maintainer acceptance remains required, with no overall X1 closure. |
| Internal PREPARED-without-projection window | Root and exact PREPARED orphan present, discovery absent. [I](./E1-01I-PREPARED-BEFORE-DISCOVERY.md) records a specific one-DC K witness: root recovery republishes discovery, aborts uncommitted D and preserves canonical/K1; resume completes deletion. The final audit recommends accepting this named cut within the A-I healthy-service scope; wider qualifications remain explicit. |
| C0 unresolved root | Safe retention is the expected outcome under current authority rules. H's Abort/DeletePrepared fixture disposal is separate. No automatic reclamation deadline or product defect is established. |
| Global G5 scheduling, persisted checkpoints and fairness | A–H use [w2OwnedRecoveryStore](../internal/integration/w2_worker_repair_lifecycle_test.go): real root reads filtered to owned identity; LoadGCStats returns not-found and SaveGCStats is a no-op. They do not certify global progress or cursor durability. Existing [G5 real-stack controls](../internal/integration/g5_recovery_scheduling_test.go) retain their R scope; do not invent a new runtime fix from that limitation. |
| Three-DC and ambiguity/outage coverage | A–H are single-DC healthy-service recovery schedules, not three-DC, every LWT/S3 ambiguous result or outage proof. Cross-check existing controls/results before requiring more tests. The [seven-family union](./E1-COMBINED-THREE-DC-VALIDATION.md) now passes its named cases; the [three-API ledger](./E1-THREE-API-RESIDUALS.md) preserves independent availability failures. #293's withdrawn loader change supplies no diagnosis/fix. No per-cut three-DC cross product is inferred or newly required here. |
| Physical coexistence and final reclassification | A–H do not create a P2 publication/coexistence witness. Existing G4/G5 controls retain their scope. [The final audit](./E1-FINAL-DISPOSITION.md) now records the D0 dispositions and separates H1's missing combined late-PUT witness from G4/G5 coexistence controls. Maintainer acceptance remains required. |

**Historical next selection (2026-10-15), superseded by the [final audit](./E1-FINAL-DISPOSITION.md):**
The inventory originally selected one of the existing seven three-DC
fixture-progress failures, reproduce it on frozen current code in an isolated
Docker topology, and record actual replication/local DC, exact candidate,
worker visits and durable state. For example, the existing E1-11
[TestCurrentRuntimeRepairLiveness](../internal/integration/e111_repair_liveness_test.go)
is a candidate to diagnose; it is not yet selected as a proven cause or fix.
Require the original gate and owned cleanup; do not bypass topology checks or
assume openIntegrationProjectionDB is involved. If the failure reproduces,
classify product versus fixture/availability and make only the demonstrated
cause's bounded RED→GREEN correction. If it does not, preserve that result and
leave the obligation open. Add no extra cut or generic harness refactor here.

E1-01/X1 and PRE-GC/A1 remain OPEN; ordinary/production GC remains OFF.
The identity audit and other X1/PRE-GC rows remain in the
[critical-path source of record](./X1-CRITICAL-PATH.md).

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

## 2. Original source inventory (2026-10-13)

The table below preserves the planning snapshot before A/B/C/D merged. Section 0
is the current reconciliation; its later K evidence supersedes missing-K claims below.

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

## 3. Original first execution plan: C1 and C2 only

Implemented by merged #281; retained as the dated plan, not the next slice.

Deliver one test-only PR covering two contiguous G2 boundaries. Do not implement C0/C3–C8 in that PR.
If accepted current evidence already proves one cut, cite and reuse it rather than duplicating it.

1. Freeze the execution SHA. Rebuild/verify the tested current sources. Record image/binary identity,
   Cassandra topology/consistencies and SILO version; do not assume a healthy old container runs that SHA.
2. Use a fixture-owned org/keyspace and objects with no peer destructive worker. Verify GC configuration,
   topology and service readiness before collecting evidence. Use the existing isolated e19 machinery where appropriate.
3. A child runs **production `Worker.ProcessOrgOnce(ctx, orgID)`** for the fixture-owned organization,
   preserving the current `processOrg` -> `processBlock` path, including the actual claim, liveness probe
   and Prepare/Commit/Promote handoff. Keep the existing `TestNoGlobalGCFanoutInIntegrationSuite` guard;
   do not drain unrelated organizations. Pause after successful PREPARED (C1), or after confirmed handoff commit
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

## 5. Original later-slice order and continuing gates

Historical slice order, now implemented by merged A–H. Section 0.7 records the current qualifications; this list does not select another drill.

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

## 7. Original remaining-cut inventory audit (#289, 2026-10-14)

Historical validation of the documentation-only inventory is preserved below.
Current execution validation belongs to the E1-01E ledger.

Reviewed against main@bd8cab8529e34ff0a245e76e4db268919cfe164b. Re-read real
Prepare/Promote/DeleteS3Orphan ordering, worker root-only/committed recovery,
MockStore's ambiguity injection, G1 R setup and merged A/B/C/D cut oracles.
Current/referenced evidence and the dated initial planning snapshot are separated;
no mock replay or fresh Worker is relabelled process death. No new runtime
counterexample or closure is claimed. Diff: four documentation files only.

Relative file links and all 19 named test functions resolve. The new section
anchor resolves in the source headings. Source order checks confirm root
publication/read before PREPARED insert, lifecycle insertion before orphan
COMMITTED CAS, and orphan -> discovery -> root deletion. `git diff --check` PASS.

Docker image built with Dockerfile.gotest:
`sha256:97bce0146108e35fc224fd0e6e617818e3ffc4c7c291ae016e5179118457a9cc`.
`go test -count=1 -timeout 5m -run '^Test(G[1-5]|X1PhysicalLifeHandoff)' ./internal/gc`
PASS (0.068s, exit 0); local ignored logs `tmp/e101-inventory-build.log` and
`tmp/e101-inventory-unit.log`, both recorded exits 0. These are existing unit/source
guards, not new real-service or K evidence. No new test was added to mirror prose.
Not run for this documentation-only diff: integration, race, API/OIDC, Sync and
three-DC tests. Prior A/B/C/D results retain their own ledgers and frozen sources.
E1-01/X1 remain OPEN; production GC stays OFF and no development GC flag changed.
## 8. Merged A–H reconciliation audit (2026-10-15)

Reviewed against `main@2e74e4cad215b1b59ca1fdb0030e21d0376eb67c` (#294).
Current pause/oracle code, worker/store order, merged PR identities and the
A–H frozen ledgers were inspected. The original H full-run log confirms all
A–H matrices/eight preservation controls and both recorded runner exits 0;
those remain historical results, not new integration execution here.

Five Markdown files only. All 281 relative file links in the changed documents
and 20 named test functions in this plan resolve; new section anchors resolve,
and the existing section-0 anchor is preserved. Source checks confirm Prepare,
Promote, physical-delete/phase and terminal/orphan/discovery/root ordering.
All eight gates remain in TestMain and both Compose commands. Fixture-local
checkpoint overrides were checked against their actual implementations.
`git diff --check` PASS. The initial scoped audit missed an overly broad
coverage conclusion; the crossed-audit correction below supersedes it.

Docker image `sesamefs-e101-reconciliation`:
`sha256:dab75325dc6d5168cd17c1f92545d1e28d1ba38c8f7717e7f46a8f3d3f4040dc`.
`go test -count=1 -timeout 5m -run '^Test(G[1-5]|X1PhysicalLifeHandoff)' ./internal/gc`
PASS (0.073s, exit 0); ignored logs `tmp/e101-reconciliation-build.log` and
`tmp/e101-reconciliation-unit.log`, recorded unit exit 0. Only documentation
changed after this image build. No new tests were added to mirror prose.
Not run for this docs-only PR: full Go tree, vet, integration, race, API/OIDC,
Sync/frontend and three-DC suites. No new service/crash evidence or gate closure.

### Crossed-audit correction — named cuts versus internal writes

The P2 THIS-PR documentation finding is accepted: the initial conclusion about
no additional uncovered successful-transition cut exceeded the named-point
reconciliation. Section 0.7 and its CURRENT_WORK/index/changelog summaries now
limit K coverage to those points and explicitly retain PREPARED-without-projection
as pending evidence equivalence/additional-drill disposition. No runtime defect
is established, no new experiment is added and no gate disposition changes.

Correction checks: all 281 relative file links and the three section anchors
still resolve; source order and A/H pause locations were rechecked; no broad
absence-of-additional-cuts claim remains in the new inventory/summaries.
`git diff --check` PASS; final diff remains five Markdown files. Prior Docker
unit results above predate this prose correction. Runtime tests, integration,
race and three-DC suites were not repeated for the correction.
