# E1 final disposition audit

## Frozen plan (2026-10-16)

Base main@e132c7c64 (#307). Scope: the D0 section 25 E1 disposition list
(H, R18, R27, R28, E6, 020, R30/R31), remaining crash/three-DC qualifications
and the explicitly tracked identity-claim PRE-X1 audit debt.

1. Trace each original obligation to its current production path, existing
   regression/oracle and dated execution record. Preserve scope and failure
   evidence; distinguish source arguments, unit models, real services and
   process death. Do not infer a test run or closure from a test name.
2. Apply the approved physical X1 contract and current greenfield deployment
   contract. Propose CLOSED/SUPERSEDED/X3/LIVENESS/TECH DEBT/STILL X1 per
   obligation, splitting safety from convergence where necessary. A missing
   witness is an evidence gap, not a reproduced runtime defect.
3. Reconcile the historical three-DC residuals and E1-01 A-I limits; preserve
   real availability failures without treating every 500 as physical data loss.
   Inspect the identity guard and H8 terminal evidence without expanding this
   audit into a guard rewrite or speculative new productive mechanism.
4. Produce one finite remaining-work table: exact unmet criterion, evidence
   already available, next bounded action and closure condition. Select the
   next slice from those facts, not from the number of tests already merged.
5. Update the critical-path source and concise tracking links. Preserve dated
   history. Run documentation link/anchor/command and consistency checks plus
   relevant existing read-only source/guard tests in Docker where useful.
   No new runtime tests solely to mirror prose; no destructive fixture run
   unless a concrete unresolved claim actually requires it.
6. Audit scope/severity separately, refresh main, commit only task files and
   open a focused PR. Report tests not run and any rejected initial checks.

This audit proposes reviewable dispositions; the maintainer owns E1/X1 closure.
No activation decision, new runtime architecture or API/schema/GC-default change.
A newly reproduced runtime defect remains a separate bounded RED/GREEN fix.
Backward compatible: yes, documentation/evidence disposition only.

## Audit result and contract

**X1 remains OPEN. No runtime defect was reproduced by this audit.** The narrow
recommendations below resolve D0's named disposition questions for maintainer
review; they do not declare E1/X1 closed or authorize activation.
Source: main@e132c7c64fa7a52ca71ddff098d44d03f5e4f1d0 (#307).
Frozen plan commit: 2059840dc.

Use the [approved physical contract](./E1-16-POST-D-CONTRACT-ALIGNMENT.md): after
COMMITTED D(P1), no P1 reinstall, revocation/postponement of D1 or HEAD dependency
on retired P1. Logical refs on L and stalled collection are separate convergence
questions. The [greenfield contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract)
requires all migrations and one compatible release; it does not waive current
races, unknown outcomes or the PRE-GC/A1 gate. Severities are preserved.

## D0 section 25 dispositions

| Item | Recommended disposition | Current argument and evidence |
|---|---|---|
| H, late PUT | **STILL X1: bounded evidence gap**, not a new runtime finding | [The real Sync test](../internal/integration/e1_delayed_put_test.go) delays an authorized P1 PUT, completes D1, then observes K1 residue and a distinct P2. It does not install/protect P2 before D1's physical DELETE or attempt the later HEAD publication. [G4 coexistence](../internal/integration/g4_physical_life_coexistence_test.go) independently proves intact K2 during D1 recovery. Neither alone is D0 section 21's complete combined schedule. Keep H open; only the measured delayed-PUT subset is X3/leak. |
| R18, late logical refs veto old physical deletion | **SUPERSEDED** for current protocol; existing legacy disposition preserved | [recoverCanonicalS3Orphan](../internal/gc/worker.go) continues exact committed D without another global refs veto. [G4 late-ref and ownership controls](../internal/gc/p4b_authority_handoff_test.go) and real coexistence retain P2. The old A+ refs-deferred executor is not the current greenfield path; the known issue already records this. No quarantine protocol is required to fix an unreachable historical executor. |
| R27, immutable day projection cannot schedule retry | **SUPERSEDED** as a current discovery dependency; **LIVENESS / PRE-GC** assumptions remain | [RecoverS3Orphans](../internal/gc/worker.go) enumerates fixed-bucket durable roots, not a 90-day by-day walk. [G5 real-stack restart](../internal/integration/g5_recovery_scheduling_test.go) persists namespaced seek checkpoints and revisits a failed prefix. Advancing/bounded-skew clocks, finite pre-cutoff claims and healthy checkpoint progress are qualifications, not unconditional fairness or a deadline. |
| R28, pending authority disappears or partially expires | **CLOSED-FIX for the pending-authority TTL criterion** | [Migration 021](../internal/db/migrations/021_gc_s3_orphan_exact_identity.cql) creates exact orphan, discovery and root tables with TTL 0. Current Prepare/attempt/projection writes add no row TTL. G1 roots and G5 restart replace the coupled legacy TTL/day-horizon mechanism. Migration 001 still contains the historical 90-day tables; inspecting it alone is not the effective migrated schema. This does not certify terminal-row retention policy or unsupported legacy upgrades. |
| E6, queue arbitration | **CLOSED-EVIDENCE for block exact-D safety; LIVENESS / PRE-GC queue debt OPEN** | A dequeued row grants no D. The actual walk claims exact P/D, checks global liveness, commits handoff, and requires its exact published lifecycle for retirement. [P4a ownership](../internal/gc/p4a_claim_ownership_test.go), [P4b terminal replay](../internal/gc/p4b_lifecycle_tombstone_test.go) and G4 coexistence support the source argument below. Ordinary requeue/complete/fail batches still lack one global lifecycle authority; duplicate/stranded work is not fixed here. Other item types are not certified by the block argument. |
| Lifecycle 020 | **KEEP; retention/removal audit question answered** | [020](../internal/db/migrations/020_gc_block_delete_lifecycle.cql) is the durable P/D tombstone after canonical retirement and orphan cleanup. Current [store](../internal/gc/store_cassandra.go) classifies exact terminal D; P4b replay tests refuse renewed authorizing publication/finalization. A-I recovery uses that distinction; I's audit retains TERMINAL certificates. No removal, folding or simplification is proposed. |
| R30, old attempt refs remain | **LIVENESS / PRE-GC**, no X1 safety closure withheld solely for retention | Current [repair renewal](../internal/api/v2/publish_repair.go) owns `pub:<repo:commit:fs>`, distinct from original Sync/v2 attempt refs. The historical fresh-repair-UUID explanation is not today's repair identity. Original attempt refs can outlive settlement; per-attempt TTL is not an aggregate retention bound. Durable abandoned repair retention remains separately tracked and measured in E1-15C/E. No lineage change or acceptance of unbounded retention is made here. |
| R31, possibly accepted HEAD loses definitive protection | **CLOSED-EVIDENCE for covered X1 safety; PRE-GC convergence OPEN** | This preserves [E1-16's accepted disposition](./E1-16-POST-D-CONTRACT-ALIGNMENT.md), not a new waiver. Covered writers queue non-expiring repair before final exact-P/HEAD; the destructive EACH_QUORUM repair guard vetoes a new D until permanent fs: exists. [E1-11](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md), E1-12/13 and their three-DC union record reachability/wire/crash evidence. UNKNOWN retains authority; stalled discovery/renewal/loser settlement and W2-14 remain PRE-GC. W2-10 uses settled source admission instead of a repair. |

E6's distinction is authority, not an exactly-once execution promise: two actors
may replay the same already-committed exact D/K1. They cannot derive a new D or
retarget it to K2 from queue data. Claim/commit CAS and immutable orphan/lifecycle
identity provide that exclusion; TERMINAL rejects later authorizing replay.
The existing queue races may duplicate work or strand a fence and keep their
original severity. A fresh destructive counterexample would reopen safety.

## E1-01 and three-DC evidence disposition

Recommend accepting **the named healthy-service process-cut obligation** of
E1-01 as CLOSED-EVIDENCE: [A-I reconciliation](./E1-01-CRASH-EVIDENCE-PLAN.md)
records C0-C8, internal cleanup steps and PREPARED-without-discovery, with
same-boundary resume, SIGKILL, fresh-process recovery/replay and failure
preservation. C0's unresolved root must be retained; explicit fixture disposal
is not automatic recovery. G4's separate P2 control supplies coexistence; do
not claim that every killed A-I fixture publishes P2.

This is a scoped acceptance recommendation, not a claim of every internal
write, fault combination or topology. A-I is one-DC K evidence. G5's
[three-DC record](./GC-G5-3DC-VALIDATION.md) and real checkpoint/restart controls
remain their own dated R evidence; W2/E1 native wire faults cover their named
paths, not arbitrary GC/S3 ambiguous responses. The per-cut three-DC cross
product was not required by this audit. Review these qualifications at final E1
acceptance; if evidence is rejected, name the specific missing invariant and
existing path before adding another matrix or architecture.

Source continuity was checked, not assumed: all 11 productive internal/gc Go
files have identical comment-excluded token streams at G5's corrected
88737973e and this audit base. String literals are retained in that comparison.
G5's integration file gained an isolation/GC-OFF forwarding wrapper; it is not
byte-identical. This comparison is not a proof about every DB/API dependency
or a new execution of the older three-DC tests.

The [59-case union](./E1-COMBINED-THREE-DC-VALIDATION.md) reconciles the seven E1
fixture families; [three distinct API residuals](./E1-THREE-API-RESIDUALS.md)
record GC 3/3 PASS, SeafHTTP 3/3 FAIL and V2 2/3 FAIL in the final race run.
Those package exits remain 1. Lease/HEAD CAS timeouts are availability findings,
not a demonstrated retired-P1 publication; an unknown outcome must retain its
protection. Full-suite budget, special mapping script and encrypted-client sync
limits remain unverified/unresolved as recorded. None becomes PASS here, and
this audit does not require fixing an independent 500 to invent physical safety.

## Remaining X1 work

These are two open evidence-debt families, split into three bounded actions,
followed by the maintainer's final acceptance. This is not an inventory of all
PRE-GC/A1 work.

| Action | Exact unmet criterion | Small next action and closure condition |
|---|---|---|
| **H1 — recommended next slice** | D0 section 21's combined existing-P2 / late-K1 / publication schedule lacks a literal witness | Extend the existing real Sync delayed-PUT fixture with a legitimate protected P2 installed before D1 deletes K1. Release the already-authorized K1 PUT, exercise the actual metadata/HEAD continuation, and replay stale D1. PASS must show K2 bytes/metadata/refs intact, no P1 reinstall or HEAD dependency, and any successful publication resolves P2. Include no-GC/control/replay and exact cleanup. Fix runtime only on a reproducible RED. |
| **I1 — identity claim guard, P2 PRE-X1** | Current guard inventories literal strings rather than every Query/Batch execution site | Reuse the existing execution-site approach for the two actual identity claim operations. Require exact write-once INSERT/read and fail-closed classification of current dynamic sites; directed negatives must test the existing immutable-claim contract. No new productive registry/guard or hypothetical runtime finding. Close the recorded structural audit debt after independent checks. |
| **M1 — H8 mutation granularity, P2 PRE-X1 quality debt** | One combined mutation does not independently evidence ten supported CAS terminals | Split scripts/pc-d1b3-mapping-authority-mutation-validation.sh H8 into ten directed cases, one terminal each, requiring compile success and the matching guard rejection. H8 belongs to the mapping cold-path guard, not the identity primitive. Keep the existing guard; do not add a new runtime mechanism. |
| **Final acceptance — maintainer decision** | E1/X1 sign-off has not happened | Review these dispositions, H1/I1/M1 results, the scoped E1-01 recommendation and failed/unrun deployment evidence. Accept a stated physical-safety scope or identify a concrete unmet criterion. Then propose X1 CLOSED in the final review; no unconditional closure or activation is implied by this PR. |

I1/M1 preserve an existing PRE-X1 quality condition; neither is evidence of a
current retired-P1 publication. The maintainer may instead explicitly accept the
present source/evidence scope and reclassify either as TECH DEBT / GENERAL,
keeping P2 severity and the follow-up open. Such a decision must be recorded;
this audit neither silently waives the tag nor makes a hypothetical future caller
a new runtime blocker. H1 remains the recommended physical-safety evidence slice.

The current identity source has only its one INSERT and one SELECT in
[identity_authority.go](../internal/db/identity_authority.go). Its existing
[immutability guard](../internal/db/identity_authority_immutability_guard_test.go)
passes; that does not close its known completeness debt. The mapping guard's
execution inventory is in [TestBlockMappingMutationsAreRepositoryWideInventoried](../internal/db/block_mapping_authority_guard_test.go),
not just its older similarly named literal immutability test. H8 currently
inserts four Query CAS, four Batch CAS and two Session batch CAS calls at once.

## Validation and limits

Docker on a versioned-source archive at plan commit 2059840dc (productive source
identical to audit base): `go test -json -short -count=1 ./internal/gc ./internal/db`
exits 0. GC has 798 PASS test/subtest events; DB has 528 PASS and **9 SKIP**
events. Package PASS events are excluded from those numbers. The skipped tests
are TestSeedDatabase_FirstRun, TestSeedDatabase_Idempotent,
TestSeedDatabase_DevModeUsers, TestSeedDatabase_PlatformOrg,
TestSeedDatabase_SuperAdmin, TestSeedDatabase_UserIndexing,
TestCreateDefaultOrganization,
TestCreateDefaultAdmin and TestCreateTestUsers; these short-mode seed fixtures
were not exercised against Cassandra. This is not a full Go/integration PASS.

The token comparison exits 0 for 11 GC files. Documentation validation checks
new relative file/heading links, source names, the frozen base, disposition
consistency and diff whitespace. No new tests, source guards or mutation
experiments are added solely to mirror this prose. No stack is started and no
GC or fixture data is mutated. Integration, race, API/OIDC, client sync and
native fault matrices were **not rerun** by this docs-only audit. Historical
results above are cited with their original limits, not relabeled current runs.
Local ignored audit artifacts are tmp/e1-final-*; no independent CI is claimed.
Backward compatible: yes, documentation only; no API/schema/runtime/default change.