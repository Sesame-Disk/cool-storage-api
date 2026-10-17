# E1 final disposition audit

## Final acceptance decision (2026-10-16)

**Decision submitted for maintainer merge: accept the scoped E1 evidence and
close X1 for the physical-life contract below.** Before merge, this is a proposal
and main's X1 remains OPEN. The maintainer's merge is the acceptance event;
after it, X1 is CLOSED within this scope. PRE-GC/A1 remain OPEN and ordinary
GC remains OFF in every DC. This decision is not a production-readiness or
activation certificate.

### Accepted contract and deployment

After COMMITTED D(P1), no reinstall of P1, revocation/postponement of D1 or HEAD
dependency on retired P1 is allowed. A later P2 has independent physical
identity/key; replay of D1 must not destroy K2. Logical references to L and
collection progress remain separate from this physical contract. Accept the
[D0 dispositions](#d0-section-25-dispositions), including **H = X3/leak for the
measured late-K1 schedule**, with I1 CLOSED-FIX and M1 CLOSED-EVIDENCE for their
named quality criteria. This does not accept unbounded leak/retention for A1.

The [first-production deployment contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract)
requires empty greenfield state, all migrations and the same compatible audited
release on all writers and GC before traffic. Unsupported incompatible mixed
rollouts are not certified; their existing findings retain their severity and
GENERAL scope. These assumptions do not waive races in the current code.

### Evidence accepted, by obligation

| Obligation | Accepted basis | Qualification retained |
|---|---|---|
| Writer-first / GC-first; W1/W2; BorrowedFS and R3 publication funnels | [W2 closure inventory](./X1-CRITICAL-PATH.md#4-w2-exit-checklist), [E1-16 contract](./E1-16-POST-D-CONTRACT-ALIGNMENT.md), [E1-17 source admission](./E1-17-W2-10-RESIDUAL-HANDLERS.md), named [three-DC union](./E1-COMBINED-THREE-DC-VALIDATION.md) | Own exact-P protection/repair or settled source admission; not a promise that arbitrary logical post-D refs are forbidden. Concurrent Phase 5/6 metadata deletion stays PRE-GC. |
| G1-G5 handoff, durable recovery, P1/P2 coexistence and stale D1 | [G5 corrected validation](./GC-G5-3DC-VALIDATION.md), [G4 coexistence controls](../internal/gc/g4_physical_life_coexistence_test.go), [H1 real protected successor](./E1-H1-PROTECTED-SUCCESSOR.md) | H1 is one DC with healthy services. Initial missing K1 is a legitimate repair trigger; G4 separately starts with both objects. Source continuity below is not a new execution of older matrices. |
| Crash at handoff phases | [A-I exact-cut inventory](./E1-01-CRASH-EVIDENCE-PLAN.md#01-recorded-evidence-by-exact-cut) | Accept named healthy-service one-DC cuts C0-C8, including PREPARED without projection and internal cleanup cuts. Not every durable write or cut/fault/topology cross-product. Root-only C0 retention is safety, not autonomous completion. |
| Queue/pending/cursor/discovery loss and restart | Source [worker](../internal/gc/worker.go)/[store](../internal/gc/store_cassandra.go), [G1 root/projection unit controls](../internal/gc/g1_orphan_exact_identity_test.go), G5 real checkpoint/restart, A-I durable-state/replay controls | Exact root/canonical/lifecycle authority, not discovery or a queue row, authorizes deletion. Pending authority has no TTL. Source/model loss cases are not all native disk-loss experiments; clock/fairness and queue convergence stay PRE-GC. |
| LWT ambiguity, stale orphan mutation and S3 ambiguity | Exact-authority source argument below; [G2 ambiguity](../internal/gc/g2_prepared_committed_test.go), [G3 ambiguity](../internal/gc/g3_committed_canonical_retirement_test.go), G4 refusal/post-delete failure unit tests; [native publication wire evidence](./E1-COMBINED-THREE-DC-VALIDATION.md) | Native publication outcomes cover their named funnels. GC unknown-outcome/S3 retry safety is accepted from source plus unit/refusal evidence, not represented as native GC/S3 fault certification. |
| R31 possibly accepted HEAD protection | [E1-11](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md), E1-12/13 in the union, E1-16 disposition | Non-expiring repair before HEAD and EACH_QUORUM veto until permanent refs; W2-10 instead uses settled source admission. Discovery/renewal/loser settlement is still PRE-GC. |
| Three-DC coverage | G5 dated real-service controls and 59-case E1 normal/race union | The union uses one API daemon over three-DC Cassandra. Actual [three-API runs](./E1-THREE-API-RESIDUALS.md) retain SeafHTTP 3/3 FAIL and V2 2/3 FAIL; package exits 1. Those are independent availability findings, not physical safety PASS. |
| Identity and terminal evidence quality | [I1 execution inventory](./I1-IDENTITY-EXECUTION-INVENTORY.md), [M1 ten independent CAS negatives](./M1-INDEPENDENT-CAS-EVIDENCE.md) | Current-source guard evidence, not an architectural proof against hypothetical new mutation paths. |

In `recoverCanonicalS3Orphan`, exact lifecycle observation and settlement precede
physical deletion; unknown/mismatched authority refuses. After a fresh topology
check, recovery reloads the same canonical orphan, validates its physical locator
with its organization's store, and calls `DeleteBlockByStorageKey` with that
orphan's immutable K1. A DELETE error retains recovery authority; a lost successful
response or failed phase advance may repeat K1. It cannot select replacement K2
from current block metadata. Exact terminal settlement prevents renewed authority.
`TestG4RecoveryFailsClosedWithoutExactSettlement` and
`TestG4RecoveryPostDeleteFailuresPreserveReplacement` support this argument. This accepts safe repetition, not exactly-once execution or a
native S3 timeout experiment. Loss of the durable authority itself grants no
permission; auxiliary loss must not manufacture a token.

### Certified baseline and reopening

Resolve [the certified-baseline question](./X1-CRITICAL-PATH.md#5-publicationcoordinator-and-the-pc-d1-line):
the covered W2 protections suffice for this X1 decision. A productive certified
witness consumer, PC-D1B.5 completion and its full certification-window matrix
are not required to close this physical gate. Existing coordinator, identity and
mapping protections stay intact/frozen. A future consumer owns its CW-M17/global
SERIAL obligations; GC destroyer requirements remain on PRE-GC's own list.

Reopen X1 for a concrete current-code sequence violating the accepted physical
contract, a falsified evidence claim, or a deployment change invalidating an
accepted premise. A new topology/fault requirement needs its named invariant and
path; this decision is not universal fault certification. Known availability,
retention and metadata-cascade failures retain their severity and tracking.
Older full-suite failures, special mapping failures/skips and encrypted-client
limits are not converted into PASS. Dated OPEN snapshots below and in linked
ledgers describe their original point in history; this decision supersedes their
X1 pending lists only within the accepted scope. The frozen D0 stays unchanged.

**Next gate: PRE-GC/A1, still OPEN.** Its source is the complete tagged inventory
plus [critical-path section 6](./X1-CRITICAL-PATH.md#6-pre-gc-list-a1-prerequisites),
not just the latest PR. Next bounded runtime slice: reproduce the tracked Phase 5
shared-fs_object cascade on owned real-stack fixtures, then fix only the verified
failure with RED/GREEN evidence. That implementation is outside this PR.

### Validation of this acceptance PR

Docker Go 1.25.14, tracked source at plan commit 00f802e03 plus the updated docs:
`go test -json -short -count=1 ./internal/gc ./internal/db` exits 0; GC 798
PASS test/subtest events, DB 528 PASS and the same nine seed SKIP named in the
historical validation below. These seed fixtures were not exercised against
Cassandra. Existing documentation/source guards pass; no new tests were added.
All added local links/headings and diff whitespace were checked. All productive
`cmd`/`internal` sources are unchanged from #308's base e132c7c64; the 11 GC Go
files also retain token equality with corrected G5 88737973e (comments/positions
excluded, literals retained). No new integration, race, API/client or native
fault run is claimed for this docs-only decision. No shared stack was started
or changed. Local ignored evidence: tmp/e1-acceptance-*.

## Status reconciliation correction (2026-10-17)

Review of #312 found that the new decision banner did not dispose the canonical
`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` entry or its live summaries. That
was a THIS-PR documentation blocker: merge would leave contradictory statuses.
The [issue now maps each original criterion to current evidence](./KNOWN_ISSUES.md#issue-gc-upload-fence-rematerialization-01-physical-delete-aba-can-remove-re-uploaded-bytes),
with closure conditional on the same maintainer merge. Historical Blocker severity
and discovery narrative are retained. Registry reasoning stays dated; deployment,
API, architecture and blocker summaries consistently keep PRE-GC/A1 OPEN and GC OFF. No new
runtime fix or new execution of historical integration evidence is claimed.

Correction validation: Docker `go test -json -short -count=1 ./internal/gc ./internal/db`
exits 0 (GC 798 PASS, DB 528 PASS and the same nine seed SKIP). Final-document
`TestX1PhysicalLifeHandoff*` guards pass; 61 added local links/headings and diff
whitespace pass. Frozen D0 and productive source stay unchanged. Integration,
race and API/client/native fault checks were not rerun; a reported quota-test
integration failure is not converted into PASS by this documentation correction.

## Final acceptance plan (2026-10-16)

Base main@aa5d1d69e (#311). This slice proposes a maintainer decision, not
another runtime change. Its merge accepts only the stated physical-life scope.

1. Reconcile D0 section 25 with W1/W2, G1-G5, A-I and H1; distinguish source,
   unit, real-stack, process-death and three-DC evidence. Preserve failed and
   unrun cases, including three distinct API residuals and native S3 ambiguity.
2. Record the exact accepted contract, deployment assumptions, H=X3 disposition
   and certified-baseline decision. Identify what would reopen X1. Make the
   maintainer's merge the explicit acceptance event; keep PRE-GC/A1 OPEN.
3. Update current summaries and the named A-I inventory; retain dated snapshots
   and the frozen D0 record. Do not change runtime, guards, schema or defaults.
4. Check links, commands, source continuity and existing GC/DB short tests in
   Docker. Report seed skips and integration/race checks not rerun. Audit the
   final diff, refresh main, commit task files and open a focused PR.

Backward compatible: yes, documentation only. Ordinary GC remains OFF.

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

Historical #308 snapshot; the final acceptance decision above supersedes its
pending conclusions only for the stated scope.

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
| H, late PUT | **H1 measured; recommend X3/leak for the named schedule**, pending maintainer acceptance | The original audit lacked P2-before-DELETE plus HEAD. [H1](./E1-H1-PROTECTED-SUCCESSOR.md) now combines authorized K1 PUT, real D1 retirement, legitimate protected P2 before physical recovery, late K1 residue, actual metadata/HEAD/canonical download and stale D1 discovery refusal. K2 metadata/bytes/refs and HEAD survive; normal/race controls pass. This is one-DC healthy-service evidence, not universal fault coverage or overall X1 closure. |
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

H1 supplies the named combined witness and [I1's guard criterion is now met](./I1-IDENTITY-EXECUTION-INVENTORY.md).
[M1 now supplies the independent terminal evidence](./M1-INDEPENDENT-CAS-EVIDENCE.md).
The final acceptance decision above is submitted for maintainer merge; no further
H1/I1/M1 implementation is listed here. This is not an inventory of all PRE-GC/A1 work.

| Action | Exact unmet criterion | Small next action and closure condition |
|---|---|---|
| **H1 — measured; disposition pending acceptance** | Named existing-P2 / late-K1 / publication witness is now supplied | [H1 result](./E1-H1-PROTECTED-SUCCESSOR.md): real protected P2 precedes D1 physical recovery; late PUT and actual HEAD resolve P2; stale discovery refuses without a durable token; fresh discovery is empty. Controls, repeats, race, directed negatives and owned cleanup pass. Recommend X3 for observed K1 residue within this scope; final E1 decision remains the maintainer's. |
| **I1 — CLOSED-FIX, P2 guard quality** | Execution-site criterion met for current classified source | [I1 evidence](./I1-IDENTITY-EXECUTION-INVENTORY.md) reuses the existing Query/Batch inventory, pins one exact INSERT/read at their approved owners and preserves schema/literal checks. Two old-guard blind spots are demonstrated; 13 compiled negatives and two controls pass, as do DB normal/race with nine short seed skips. No runtime defect or overall gate closure is claimed. |
| **M1 — CLOSED-EVIDENCE, P2 quality** | Independent-terminal criterion met | [M1 evidence](./M1-INDEPENDENT-CAS-EVIDENCE.md): ten separately selectable mutations compile and require their own terminal diagnostic; non-CAS control passes. A disabled ScanCAS recognition stays hidden in the old aggregate predicate but is refused by the new selector. Existing guard/runtime unchanged; final E1 acceptance remains separate. |
| **Final acceptance — effective on maintainer merge** | Decision submitted above | Merge accepts the stated evidence/qualifications and closes scoped X1. Before merge this remains a proposal. PRE-GC/A1 stay OPEN; no activation is authorized. |

I1 and M1 resolve the two recorded P2 PRE-X1 quality conditions within their
stated source/evidence scopes. Neither demonstrates a current retired-P1
publication. H1's combined witness is supplied; the maintainer must still
accept or reject the stated E1 scope and qualifications. No quality fix silently
closes X1, waives failed/unrun evidence or authorizes GC activation.

The current identity source has only its one INSERT and one SELECT in
[identity_authority.go](../internal/db/identity_authority.go). Its
[immutability guard](../internal/db/identity_authority_immutability_guard_test.go)
now also requires the shared execution inventory; the former literal-only
completeness debt is resolved by I1. The mapping guard's
execution inventory is in [TestBlockMappingMutationsAreRepositoryWideInventoried](../internal/db/block_mapping_authority_guard_test.go),
not just its older similarly named literal immutability test. H8 now
checks four Query CAS, four Batch CAS and two Session batch CAS terminals
independently; the legacy selector runs that ten-case sequence.

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