# X1 critical path — reset to the #201 roadmap

## E1-16 post-D contract alignment (2026-10-10)

The project owner approved E1-15D §1 as alignment with the G4 contract
(2026-10-02, #247), not as new architecture. The X1 invariant is physical:
after exact COMMITTED D(P1), nothing may reinstall P1, revoke or postpone D1,
or publish HEAD content that depends on retired P1. Logical `up:`/`pub:`/`fs:`
references of L written after D1 are liveness of L (D0 §3, G4 contract 5);
their retention is PRE-GC convergence. Against code read at main@5c3163871:
W2-1..9 and W2-12..14 close for X1 (see §4). The R31 safety rests on the
shared EACH_QUORUM repair guard, and the Sync, RecvFS and cross-repo source
arguments are recorded in E1-16. W2-10 stays OPEN. E1-15A/B and the R31
convergence residuals move from PRE-X1 to PRE-GC with their severity
unchanged, so they still block A1. Remaining before X1 CLOSED: W2-10, E1-01,
the three-DC residuals (suite re-measured 2026-10-10), the D0 §25 reclassification list and one PRE-X1 audit
debt. See [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md). Dated entries below
are historical snapshots, and their "remains OPEN" statements for W2 rows are
superseded by §4. GC stays OFF.

## G5 covered development contract validated (2026-10-02)

G4 is merged in #247, main@b57f5ce69. The [G5 implementation plan](./GC-G5-IMPLEMENTATION-PLAN.md)
replaces the physical day walk with bounded durable-root execution and seek
checkpoints. The same-P stale-claim settlement race reproduced before PREPARED;
its attempted candidate-retention fix is withdrawn after a reproduced fresh
zero-epoch grace regression. The race remains OPEN, P1 FOLLOW-UP / PRE-GC.
The earlier single-DC full integration passed; expanded three-DC integration
failed 30 main tests and API failed 8/20 suites at 1a1cb9629. These failures
remain recorded for E1; neither combined nor full three-DC evidence is a PASS.
E1 remains next. No W2/R31, X1, PRE-GC or activation gate is closed by this entry.
GC stays OFF.

## G4/G5 development decision (2026-10-02)

The [G4 implementation plan](./GC-G4-IMPLEMENTATION-PLAN.md) supersedes the
old all-W2-closed prerequisite for developing G4/G5. W2/R31 rows remain
recorded with their existing dispositions; evaluate residuals against the
completed GC in E1 and PRE-GC, and fix demonstrated activation blockers.
Open residuals alone do not block this development. GC remains disabled.
Dated evidence below preserves its historical conclusions.

## Greenfield first-production reconciliation (2026-09-30)

W2-0 is **CLOSED-EVIDENCE for the covered current-version mechanism** under
the [first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
The #239 durable repair / final exact-P ordering and #241 native wire loss,
real SIGKILL, independent recovery and three-DC evidence are preserved.
Both incompatible mixed-version combinations still reproduce D(P)+HEAD;
ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01 tracks them as P1 FOLLOW-UP / GENERAL,
outside v1 first activation. No compatibility gate is inferred or added.
W2-1, W2-2 and W2-6a remain OPEN through the separately tracked R31 residuals.
W2-4 now has a focused pre-HEAD fix with RED/GREEN evidence; its post-HEAD
R31 requirements remain separate. Other W2 dispositions are unchanged.
The G4/G5 development decision above supersedes the old all-W2 prerequisite; GC stays disabled.

See the [source argument, individual re-audit and drift review](./W2-0-GREENFIELD-RECONCILIATION.md).

The dated #238/#239 entries and characterization tables below are historical
evidence snapshots. Their old W2-0 OPEN / pending-evidence statements are
superseded by this reconciliation and the current X1 checklist; they do not
define today's closure state.

### PR #239 crossed audit correction (2026-09-30)

Pre-D GC now distinguishes real references, repair-only protection and zero.
Repair-only releases the exact claim and preserves candidate/discovery/queue,
postponing without retry. After COMMITTED, only actual references retain the
existing contradiction policy; a late repair cannot veto D. Both worker
regressions are included in 17 mandatory continuity legs. G2/G3 retirement and
durable physical continuation are proved; the future physical executor remains
outside this PR. W2-0/W2-6a remain OPEN and GC_ENABLED=false.
See [crossed audit evidence](./W2-0-PUBLICATION-CONTINUITY.md).

## W2-0 follow-up — durable repair gate (2026-09-29)

Covered F1/F2/F3 and Sync-provenanced writers now acquire a non-expiring repair
before final exact-P. GC reads every organization repair page at EACH_QUORUM
and repeats the reference probe after a negative scan; acquisition pins LQ.
Sync preserves its captured placement through queueing. UNKNOWN/unreachable
rows retain existing R31 behavior, so stranded repairs can inhibit collection.
W2-0/W2-6a remain OPEN: network ambiguity, OS process-kill and rollout evidence
are pending. Older GC readers must be upgraded; cold-path cost is up to 32
organization range reads plus a ref probe, proportional to pending rows.
See [plan, evidence, individual re-audit and limits](./W2-0-PUBLICATION-CONTINUITY.md).

**Roadmap decision status (historical):** DECIDED 2026-09-29. Docs-only; no runtime, schema, CQL, guard or
mutation-suite change. **Base:** `main@a5dea859a` (PR #233 merged).
`GC_ENABLED=false` remains mandatory.

This document is the source of record for **the order of work toward X1
closure** and for **how audit findings are classified**. The X1 architecture
itself is unchanged and stays frozen in
[GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md)
(D0, #201). Where another document states a different order (PC-D1B.5 →
productive consumer → PC-2 → …), this document supersedes that ordering; the
other documents keep their investigation and evidence.

## 1. Why this reset

Between #202 and #215 (2026-09-02 → 09-12) the project closed W1, the pre-HEAD
W2 slices for CreateFileFromBlocks/SessionUpload and Sync PutBlock, G1, G2, G3,
and several real runtime defects (H1, rollback ghost projections, Sync
identity). Between #216 and #233 (2026-09-12 → 09-26) most of the interval
was spent outside the #201 progression: apart from real R31 and runtime fixes
(#219 R31 convergence, #221 HEAD global SERIAL, #226 gone-check), the work went
into the PublicationCoordinator skeleton, the PC-D1 certified
baseline line (witness, certifier, identity and mapping authority, lifecycle
fence decision) and the repair-liveness protocol attempts (#220/#222
abandoned, #223/#224).

Two causes, both process rather than architecture:

1. PC-D1 answered a W2 question — "who guarantees dependencies that were first
   published with `CONDITIONAL`/`UNKNOWN` continuity?" — with an after-the-fact
   certification architecture instead of finishing W2 at the source. On a
   greenfield server (historical backfill is a non-goal) a closed W2 leaves no
   unproven inherited dependency; the remaining risk is GC destroying content
   HEAD still reaches (Phase 5 cascade, Phase 6 execute-time TOCTOU), which is
   PRE-GC regardless (§6).
2. Audit rounds turned hypothetical future bypasses into merge blockers, and
   decision records turned their own "must" lists into obligations for the next
   PR, creating a roadmap parallel to #201.

## 2. Critical path

```text
W1 + W2 core evidence + G1/G2/G3
  -> G4 -> G5 -> E1 (re-evaluate W2/R31) -> X1 CLOSED
  -> PRE-GC targeted validation -> A1 -> GC_ENABLED=true
```

- **G4/G5 development** proceeds with GC OFF after W1, covered W2 core
  evidence and G1/G2/G3. Remaining W2/R31 rows are evaluated in E1 against
  completed GC; unresolved activation requirements still block A1.
- **E1** is the only step that may declare `X1 CLOSED` (#201 §25).
- **A1** is a separate PR; X1 CLOSED is not GC enabled.
- Every PR on the critical path names the #201 step (or the §4 row) it
  advances. Work that advances no step and fixes no current-runtime defect is
  not on the critical path.

### First-production deployment contract

The supported v1 deployment is **greenfield**: a clean server, new Cassandra
keyspace and storage namespaces, with no inherited production dataset,
historical candidates/repairs/attempts, old processes or old in-flight requests.
Before first production traffic, all productive writers, repair workers,
scanners and GC workers run the same compatible audited release. All migrations
are applied before traffic. GC stays disabled fleet-wide during deployment,
startup, smoke tests, consistency/cluster validation and until PRE-GC and A1
close. This is a deployment obligation, not an implemented version registry
or a claim that GC disablement proves writer continuity.

This reconciles the existing [deployment scope](./DEPLOY.md#first-production-greenfield-contract),
[metadata-identity authority contract](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md)
and the empty-server precondition already used for Technical Debt #24 in §6.
There is no production v1 legacy fleet to upgrade. A later incompatible rolling
upgrade with active GC is outside this first-production contract; its real
counterexamples remain recorded as P1 FOLLOW-UP / GENERAL. If that deployment
model is adopted, compatibility/drain evidence must be supplied before claiming
it safe. Greenfield does not remove current-version races, wire ambiguity,
process death, cross-DC visibility, R31 or any PRE-GC/A1 prerequisite.

## 3. Finding classification rule

| The failure sequence runs on… | Classification | Effect |
|---|---|---|
| current code, current runtime (`GC_ENABLED=false`) | CURRENT-RUNTIME bug | blocks the PR if the PR introduces it or breaks its contract; otherwise a separate follow-up |
| current code with the **planned** GC activation | X1 (if it is a §4/G4/G5/E1 item) or PRE-GC (§6) | valid finding; tracked in its list, blocks X1 or A1 respectively |
| code that exists but requires an unsupported incompatible deployment (for example pre-#239 writer/GC skew) | real FOLLOW-UP / GENERAL finding; severity preserved | not a v1 X1/PRE-GC/A1 blocker; preserve counterexample and deployment limit |
| code that does not exist (a new caller, alias, wrapper, CQL variant, future migration) | not a finding | recorded as a note at most; never a blocker |

Consequences:

A strong test of an unsupported incompatible deployment remains valid evidence
of that limitation. State the deployment preconditions: test strength alone
does not make the sequence reachable under the supported v1 contract or turn
it into a v1 blocker.

- A finding must come with a concrete sequence over code that exists in the
  branch or in `main`.
- A mutation is valid when it removes a property that **current** correctness
  depends on. A mutation that invents a bypass absent from `main` to prove a
  guard would catch it is not required.
- Existing guards, inventories and mutation suites stay; no purge. New guards
  cover current call sites only.
- Decision records propose; they create obligations only through #201 steps,
  §4 or §6.
- Target: no more than two planned audit rounds per PR. The round number never
  changes a finding's severity or scope: a real finding introduced or worsened
  by the PR, or one that falsifies the PR's contract, blocks that PR whenever it
  is discovered; a real pre-existing independent finding is a follow-up
  whenever it is discovered.
- "Demonstrate first": do not assume a §4 row needs a new mechanism. A failure
  that could not be reproduced is **not** evidence of safety; closing a row
  needs one of the three closure states defined in §4.

## 4. W2 exit checklist

Derived from the `CONDITIONAL`/`UNKNOWN` rows of
[R3-LIVENESS-CONTINUITY.md](./R3-LIVENESS-CONTINUITY.md) (the #201 definition
of W2) and the R31 residuals named in #201 §16. Criterion for every row
(#201 §25 W2): once D(P1) is committed, no legitimate writer may later publish
durable liveness that depends on P1.

Reading of "depends on P1" (E1-16, 2026-10-10; D0 §3/§12, G4 contract 3–5):
a writer depends on P1 when it publishes HEAD/commit/tree content whose
resolution needs retired P1, reinstalls P1, or revokes/postpones D1. A
possibly accepted publication must also keep D-blocking liveness until its
permanent `fs:` exists (R31 safety). A logical reference of L written or
renewed after D1 does not depend on P1: G4 recovery ignores it and it protects
only a later life. Its unbounded retention is PRE-GC convergence, not a W2
violation.

Row states:

| State | Meaning |
|---|---|
| `OPEN` | not closed |
| `CLOSED-EVIDENCE` | positive, reviewable evidence that the failure sequence is unreachable under the current code/runtime contract (for example a source-level argument that an existing bound already excludes it). "We tried and did not reproduce it" is not evidence |
| `CLOSED-FIX` | a change removes the failure sequence, with evidence |
| `CLOSED-GATED` | a named **fail-closed** mechanism (for example the GC health gate) refuses the dangerous transition — GC does not commit D(P1) — whenever the row's continuity premise may not hold, so the W2 violation is unreachable under the operational contract; E1 re-checks the mechanism |

A bound that only makes the violation rare, short-lived or operationally
tolerable does not close a row: the violation is still reachable, so the row
stays `OPEN`. Residual risk acceptance is not a W2 exit state.

**Reopen rule:** a newly discovered funnel reopens W2 only if it exists and is
reachable in `main` and violates the criterion. The possibility that someone
adds a funnel later does not.

| # | Row (R3 source) | Today | Tracking | State |
|---|---|---|---|---|
| W2-0 | **Cross-cutting:** continuous own liveness through HEAD, including the 48h `up:` and 35d `pub:` TTLs | Baseline TTL pins allowed expiry after exact-P and before HEAD. Current covered chains acquire a non-expiring repair before final exact-P; destructive absence observes that repair at EACH_QUORUM, with a final reference re-read covering promotion. A late acquisition must reject the settled deleting claim | R3 table; `ISSUE-W2-PUBLISH-PIN-EXPIRY-BEFORE-HEAD-01`; R31 | **CLOSED-EVIDENCE** for the covered current-version mechanism under the first-production contract: durable repair before final exact-P, destructive EACH_QUORUM observation, final reference re-read, native wire/SIGKILL/recovery and 3-DC evidence. No independent current-version W2-0 residual identified; R31 settlement/liveness and unadopted funnels remain their own OPEN rows |
| W2-1 | `CreateFileFromBlocks`, exact session `up:` | pre-HEAD proven with the W2-0 mechanism (#204/#205) | — | **CLOSED** (E1-16): pre-HEAD CLOSED-FIX (#204/#205), re-audited against closed W2-0 and real wire/SIGKILL evidence; post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard (W2-11..14). Convergence residuals are PRE-GC |
| W2-2 | `CreateFileFromBlocks`, foreign `fs:` reuse / dedup | W1 proven through HEAD with the W2-0 mechanism (#202) | — | **CLOSED** (E1-16): pre-HEAD CLOSED-FIX (W1 #202), re-audited against closed W2-0 and real wire/SIGKILL evidence; post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard (W2-11..14). Convergence residuals are PRE-GC |
| W2-3 | Sync `PutBlock` → HEAD, and Sync retry from another pod | pre-HEAD proven for the PutBlock-provenanced subset, incl. cross-DC (#206/#210). Since W2-4, both Sync HEAD paths validate every added block's exact placement after durable repair regardless of provenance; expired provenance equals absent provenance | `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01` | **CLOSED-EVIDENCE** (E1-16): source argument over the W2-4 gate, which applies to any pod and to direct and auto-merge publication; W2-4 RED→GREEN legs, #206/#210 3-DC, E1-PUT-01. Post-HEAD through the shared repair guard |
| W2-4 | Sync commit whose block had no associated PutBlock | Every added canonical placement is captured and revalidated after durable repair; no Sync upload pin is fabricated. Ten real Cassandra/MinIO safety legs cover direct/auto-merge, GC-first rejection, fully retired P, successful controls, and repair ownership; RED on PR #244. See [investigation/fix](./W2-4-SYNC-NO-PUTBLOCK.md) | `ISSUE-W2-SYNC-NOPUTBLOCK-PREHEAD-01` | **CLOSED** (E1-16): CLOSED-FIX pre-HEAD; post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard |
| W2-5 | `recv-fs-before-put` | Ordered direct fresh/reuse/retirement subset CLOSED-EVIDENCE (E1-7); broader protocol/post-HEAD UNKNOWN | [E1-7 evidence](./E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md) / R3 table | **CLOSED-EVIDENCE** (E1-16): `RecvFS` stores fs objects only, without HEAD or block references, so every order reaches HEAD through the W2-4 Sync gate (direct or auto-merge); E1-7 measured continuations. Post-HEAD through the shared repair guard |
| W2-6 | v2 stored upload (`UploadFile`), materialized and reusable target | Adopts the W2-0 mechanism: passes its materialized placement to the shared finalizer (PR #237). Real-Cassandra evidence `TestW2UploadFileExactPlacementBeforeHead` (GC-committed and fully-retired placement before stage → 409, HEAD unchanged, `pub:` dropped); RED on the previous code | `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` (UploadFile part) | **CLOSED** (E1-16): `CLOSED-FIX` pre-HEAD; post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard |
| W2-6a | v2 `CreateFile`, Office-template block publication (empty-file path has no blocks) | Carries the actual materialized SHA-256/class/key to final exact-P after staging `pub:`. Nine real Cassandra/MinIO legs prove rejection when GC wins before validation, with 409, HEAD unchanged and cleanup. The follow-up adds real expiry-after-authority protection through a non-expiring repair gate; real wire/SIGKILL evidence now covers the current-version chain under the first-production contract | `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` (CreateFile part; PC-0 F1); `ISSUE-W2-PUBLISH-PIN-EXPIRY-BEFORE-HEAD-01`; [plan/evidence](./W2-6A-CREATEFILE-EXACT-P.md) | **CLOSED** (E1-16): exact-P and covered pre-HEAD continuity CLOSED-FIX; post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard (W2-11..14). Convergence residuals are PRE-GC |
| W2-7 | SeafHTTP normal/streaming finalize | E1-5a single-shot and E1-5b streaming retain original materialized P through retries, validate after durable repair before HEAD, reject COMMITTED/TERMINAL; streaming same-tracker retry rematerializes only rejected blocks | [E1-5b evidence](./E1-5B-SEAFHTTP-STREAMING-PUBLICATION-SAFETY.md) / [E1-5a evidence](./E1-5A-SEAFHTTP-SINGLE-PUBLICATION-SAFETY.md) | **CLOSED** (E1-16): CLOSED-FIX single-shot and streaming pre-HEAD. The final exact-P read uses canonical state, not the `up:` row, so natural and controlled expiry share the path. Post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard |
| W2-8 | OnlyOffice callback | E1-4 retains original materialized exact P through durable repair and validates before HEAD; current-code COMMITTED/TERMINAL callbacks were RED | [E1-4 evidence](./E1-4-ONLYOFFICE-PUBLICATION-SAFETY.md) | **CLOSED** (E1-16): CLOSED-FIX pre-HEAD; post-HEAD R31 safety CLOSED-EVIDENCE through the shared repair guard; post-HEAD cleanup is convergence (PRE-GC) |
| W2-9 | Cross-repo copy/move | E1-09 captures source exact P per retry and checks it after durable repair before HEAD; productive source-purge COMMITTED/TERMINAL RED -> GREEN | [E1-09](./E1-09-CROSS-REPO-PUBLICATION-SAFETY.md) | **CLOSED** (E1-16): CLOSED-FIX for the measured subset, plus CLOSED-EVIDENCE for directories and multi-block, which run the same recursive copy, full-set capture and identical-set validation. Cross-representation copy is rejected before copying. Cross-DC through the shared fence/guard 3-DC evidence. Post-HEAD R31 safety through the shared repair guard |
| W2-10 | Content resurrection: `RevertFile`, `RevertDirectory`, `RestoreTrashItem`, `RevertDirents` | Narrow retained-history schedules block GC before D. E1-10e additionally executes real COMMITTED/TERMINAL unpublished-source RED for a RevertDirents root file and adds settled historical fs: admission via LQ→EQ fallback plus original exact-P capture/per-HEAD revalidation. No own up:/pub:/repair. | [retained-file evidence](./E1-6-REVERTFILE-PUBLICATION-SAFETY.md), [E1-10d retained dirent](./E1-10D-REVERTDIRENTS-RETAINED-HISTORY.md), [E1-10e RED/fix](./E1-10E-W210-RESIDUAL-DISPOSITION.md) / `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` | CLOSED-FIX measured RevertDirents file source-admission subset only. W2-10 overall, continuous liveness through HEAD, directories/broader batches, other handlers, retention/Phase5/6 and R31 OPEN |
| W2-11 | R31: repair after HEAD — renewal after classify | Current non-expiring repair blocks a new D even with zero real refs during/after classification; REACHABLE installs fs: before removing repair; UNKNOWN/errors retain protection | [E1-11 evidence](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md) / `ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01` | **CLOSED-EVIDENCE covered current-version pre-D safety contract**; no discovery/convergence bound, concurrent cleanup closure or post-COMMITTED revocation claim |
| W2-12 | R31: known-loser durability | Office definitive-loser crash pre-D safety measured: actual negative CAS, SIGKILL, TTL expiry, productive GC guard and fresh-process UNKNOWN sweeps | [E1-12 evidence](./E1-12-KNOWN-LOSER-CRASH-SAFETY.md) / `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01` | **CLOSED-EVIDENCE for X1** (E1-16): only REACHABLE promotes, UNKNOWN retains, and the funnel-independent guard vetoes D (E1-12). Durable loser witness and cleanup/convergence → PRE-GC (`ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01`) |
| W2-13 | R31: repair discovery bound | Office real earlier-bucket backlog delays target execution; direct EQ repair guard vetoes D before target visit; fresh-process native UNKNOWN discovery/renewal | [E1-13 evidence](./E1-13-REPAIR-DISCOVERY-DELAY-SAFETY.md) / `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01` | **CLOSED-EVIDENCE for X1** (E1-16): the guard reads the repair table directly, independent of discovery, classification and writer (E1-13). Discovery, scale and convergence (no SLA, fairness or bounded completion) → PRE-GC (`ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`) |
| W2-14 | R31: `pub:` zero-ref transition | Office real `pub:` (with and without durable repair) retired by actual TTL to global zero refs: productive Phase 0/1 and worker create no candidate/queue/D; P1/K1 retained; later native UNKNOWN renews exact repair-owned `pub:`. A causal mutation giving `pub:` the `up:` expiry projection yields a productive candidate | [E1-14 evidence](./E1-14-PUB-ZERO-REF-TRANSITION.md) / `ISSUE-GC-PUB-REF-ZERO-REF-01` | **CLOSED-EVIDENCE for X1** (E1-16): `pub:` expiry alone cannot reach D (E1-14, NOT-X1-RED). Transition and convergence (dead block retained) → PRE-GC (`ISSUE-GC-PUB-REF-ZERO-REF-01`) |

E1-15A (shared repair cancellation, not a W2 row): a stale visitor that has
passed its durable re-check no longer leaves a post-D `pub:` after a
legitimate clear when its global check and withdrawal both succeed. RED on #270 main, mitigated by an
EACH_QUORUM-confirmed withdrawal after the write. The measured Office subset is
PARTIAL-FIX / CLOSED-EVIDENCE on that path only. The write itself is not
fenced, so crash, check/withdraw errors and ambiguous renewal can leave the
pin (P1). Under the E1-16 alignment, that residual pin is a logical reference
of L: it does not reinstall P1 or reach HEAD. Its scope moves from PRE-X1 to
PRE-GC with severity unchanged
([evidence](./E1-15A-STALE-REPAIR-VISITOR-CANCELLATION.md),
`ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`).

E1-15B (writer post-D staging crash, not a W2 row): a real CreateFile writer
resumed after exact COMMITTED D stages `pub:` before its final fence. Without
a crash the fence rejects it (pre-HEAD holds). SIGKILL leaves the `pub:`
durable, and after repair queueing every sweep renews it post-D. Characterized
and not fixed. The harm is retention of later lives, with no HEAD advanced and
no P1 reinstalled (E1-15C). Under E1-16 the scope is P1 PRE-GC, formerly PRE-X1
([evidence](./E1-15B-WRITER-POST-D-STAGING-CRASH.md),
`ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01`). W2-6a pre-HEAD
status is unchanged.

E1-15C (physical disposition, not a W2 row): on Office, post-D references and a
dead repair do not reinstall P1 or block its TERMINAL retirement, and a
rematerialized P2 is published legitimately. No physical (D0 §13) RED was
measured. The demonstrated harm is that an abandoned repair vetoes D of every
later life of L. The strict E1 "no post-D reference" reading was retired by
E1-16 (2026-10-10), which aligns it with G4 contract 5
([evidence](./E1-15C-POST-D-PHYSICAL-LIFE-DISPOSITION.md)). No severity is
lowered.

E1-15E (production, not a W2 row): the repair worker settles a superseded
abandoned repair (walk from the SERIAL HEAD anchor reaches the commit's
parent first, anchor ≠ parent), removing only its own `pub:` and row. Sync
publications keep their own protection (measured after, before repair and
before CAS). X1 safety unchanged; PRE-GC retention narrowed, not closed
([E1-15E](./E1-15E-ABANDONED-REPAIR-SETTLEMENT.md)). E1-15F validated it on
real 3-DC Cassandra (HEAD = parent retains; one DC down retains; recovery
settles; REACHABLE promotes) and fixed a parent-read liveness defect
([E1-15F](./E1-15F-SUPERSEDED-CROSS-DC.md)). X1 and E1 stay OPEN; GC stays OFF.

E1-15D (contract decision, not a W2 row): an abandoned v2 commit behind an
UNKNOWN repair is still publishable. Real Sync `UpdateBranch` auto-merges it
into HEAD, and after TERMINAL the Sync publication readiness gate rejects it.
Proposed for explicit approval: X1 = physical invariant; post-D references
and abandoned repairs = PRE-GC convergence; a settlement needs durable
authority over the original attempt and a concurrency analysis against
Sync promotion, with global commit cancellation only as a fallback
([decision](./E1-15D-POST-D-CONTRACT-DECISION.md)). §1 was approved on
2026-10-10 as alignment with G4 ([E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)).
Severities are unchanged; scopes moved from PRE-X1 to PRE-GC.

Not W2 exit rows (follow-ups, may be reclassified in E1):
`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`,
`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01`,
`ISSUE-PUBLISH-REPAIR-CROSS-CHUNK-CYCLE-01`,
`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`,
`ISSUE-SYNC-PUTBLOCK-READINESS-HOTPATH-COST-01`.
E1-16 (2026-10-10) moved OWNED-PUB-CLEANUP-RACE and PROGRESS-PAXOS-DOMAIN from
PRE-X1 to PRE-GC. DEAD-ROW-RETENTION stays FOLLOW-UP / GENERAL; making it a
pre-activation gate is a separate PRE-GC decision.

E1 evidence update (2026-10-03): with Cassandra plus pinned SILO
`RELEASE.2026-09-16T00-00-00Z` (MinIO-compatible S3), a real delayed Sync PutBlock after terminal D
left bytes at the retired K1 but rematerialized the canonical block at P2 and
did not restore a P1 reference. A post-terminal replay of a repair for a
commit that lost HEAD retained UNKNOWN and `pub:` only. The reachable-HEAD
late-repair race and other current funnels remain untested; these results do
not close W2-3, W2-11..14, or X1. See
[E1 execution ledger](./E1-FULL-GC-PUBLICATION-RECOVERY.md).

E1-2 adds [reachable-HEAD gate evidence](./E1-2-REACHABLE-HEAD-LATE-REPAIR.md):
after real temporary-reference TTL expiry, the Office writer's durable repair
vetoes a new D before handoff, then recovers to exact fs: using a fresh session.
This narrows the covered gate's evidence; it does not close E1-02, W2-11/14 or
X1 and does not claim COMMITTED/TERMINAL post-D execution.

E1-3 adds [two pre-D ordering controls](./E1-3-LATE-PUBLICATION-PRE-D-PROOF.md)
for Office/shared repair: final exact-P rejects a late writer under the held
claim; reachable settlement before a negative repair scan is caught by the
second global refs read. Evidence is narrow; W2-11/14 and X1 remain OPEN.

Historical suggested W2 order (superseded for G4/G5 development by the
2026-10-02 decision): W2-0 is closed for the covered shared mechanism. W2-1/2/6a were individually re-audited against that premise and remain OPEN through R31, not mixed rollout. W2-6 retains CLOSED-FIX pre-HEAD. Finish the real remaining funnels:
W2-7/W2-8 or W2-10, each starting with a
demonstration of the failure; then the Sync `UNKNOWN` rows, W2-9 and the R31
rows.

## 5. PublicationCoordinator and the PC-D1 line

- `internal/publication` (#216) and the PC-D1 line (#227, #228, #230, #231,
  #233) stay in `main` unchanged. They are **frozen**: no new work unless a §4
  row or a §6 item needs it.
- The coordinator is an optional tool for W2: migrating a funnel to it is
  acceptable when that is the minimal way to close a §4 row. PC-2 is a future
  evolution of the publication architecture, **not** an X1 prerequisite.
- A productive consumer of the certified witness is not on the critical path.
  When one is built, it owns CW-M17 (witness consumed only under global SERIAL
  or inside an LWT of the same domain).
- Identity authority (#230/#231) keeps its current-runtime value: a writer
  cannot replace a claimed commit/fs_object digest.
- E1 re-evaluates whether the certified baseline is needed at all once W2 is
  closed.
- PC-D1B.5 work in progress is parked on
  `feat/pc-d1b5-certification-window-fence-runtime` (`8a731e6bc`: migration
  `028`, E/P/S capture/intent/completion primitives, certifier capture and
  epoch predicate, `DestructionTokenV1`). It is reference material, not
  scheduled work.

## 6. PRE-GC list (A1 prerequisites)

X1 CLOSED does not authorize `GC_ENABLED=true`. Documents written before this
reset (operational notes, config comments, dated readiness reports, analysis
docs) that say GC may be activated "after X1 closes" mean after this gate
closes. **The A1 gate is every open
entry tagged `PRE-GC` or `PRE-ACTIVATION` in
[KNOWN_ISSUES.md](./KNOWN_ISSUES.md) and
[TECHNICAL-DEBT.md](./TECHNICAL-DEBT.md), plus the table below.** The table
names the known items; it is not a substitute for the tags. Enumerate the tags
before A1, for example:

```sh
grep -nE 'PRE-GC|PRE-ACTIVATION' docs/KNOWN_ISSUES.md docs/TECHNICAL-DEBT.md docs/OPEN-WORK-INDEX.md
```

A new PRE-GC finding is registered with that tag; it does not need to be
added here to count.

| Item | Tracking |
|---|---|
| G5 cycle revisit assumes advancing claim clocks with bounded, observed fleet skew and finite pre-cutoff claims. NTP/equivalent plus a verified clock-health activation/suspension procedure is required PRE-GC; G5 provides no enforcement and no fixed wall-clock revisit bound | `ISSUE-GC-ROOT-CYCLE-CLOCK-ASSUMPTION-01` |
| Phase 5 expired-version cascade keeps fs_objects shared with HEAD (P0 latent) | `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` |
| Phase 6 execute-time TOCTOU: the keep-set is computed at scan time and fs_object items carry no library guard and no execute-time reachability recheck, so a HEAD that re-references an fs_id between scan and execution loses the fs_object row. Closing W2-10 (block liveness for content resurrection) does **not** close this; the certification-window fence does not either (PC-D1B.4 §15) | recorded under `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` (PC-D1B.4 finding F3) |
| Stale-claim settle race: after `ReleaseStaleBlockClaim` reports `BlockClaimAbsent`, `settleBlockCandidate` deletes the candidate conditioned only on `candidate_at`, so a claim won by another worker in the gap can lose its recovery authority (liveness: block stranded in `deleting`, no data loss). OPEN, P1 FOLLOW-UP / PRE-GC; evaluate in E1. Outside G5 durable-root scope; closure must preserve scheduling independently of fresh zero-ref grace | `ISSUE-GC-STALE-CLAIM-SETTLE-RACE-01` |
| Certification-window fence, **only as far as the GC destroyers D1–D3 need it** (re-scope PC-D1B.4 §18 before implementing; the full CW-M1..M34 list is not automatically required). Includes `ISSUE-PCD1B-STALE-TOMBSTONE-DISPLAY-METADATA-01` if generation-timestamped deletes are adopted | `ISSUE-PCD1B4-CERTIFICATION-WINDOW-FENCE-01` |
| HEAD-less ghost `libraries` row counts as canonically absent for GC/restore | `ISSUE-PCD1B-CONTINUITY-LWT-GHOST-ROW-01` |
| Historical repair-health-gate proposal: E1-11 closes the covered current-version W2-11 pre-D safety contract through the existing non-expiring repair guard, without CLOSED-GATED or a new health gate. W2-12..14 and other PRE-GC requirements remain OPEN | [E1-11 disposition](./E1-11-CURRENT-RUNTIME-REPAIR-LIVENESS.md) |
| GC mapping resolver `CassandraStore.lookupBlockMapping()` reclassified from session consistency | `PCD1B3-PRE-GC-SESSION-CONSISTENCY-EXCEPTION` marker |
| Library hard-delete lease: global SERIAL (✅ closed by PR #234) and fenced final batch | `ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01` (closed), `ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01` (open; current-runtime, §7) |
| Post-D reference and R31 convergence moved from X1 by E1-16, severity unchanged: stale renewal after clear (E1-15A), writer post-D staging crash (E1-15B), known-loser durability (W2-12), repair discovery scale (W2-13), `pub:` zero-ref transition (W2-14), owned-`pub:` cleanup race and progress Paxos domain | `ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`, `ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01`, `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01`, `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`, `ISSUE-GC-PUB-REF-ZERO-REF-01`, `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`, `ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01` |
| Startup check refusing `GC_ENABLED=true` while any A1 gate item (tagged entry or row above) is open | first A1 item |

Closed by the greenfield precondition, not carried into A1: Technical Debt
#24 (pre-migration-017 `gc_block_candidates` rows with a null `storage_key`).
The deployment that will enable GC starts from an empty server (no legacy data
is preserved), so it cannot contain candidate rows written before migration
`017`. If GC is ever enabled on a deployment that ran pre-`017` code, #24
reopens as PRE-ACTIVATION.

## 7. Current-runtime work outside the critical path

The library hard-delete lease issues are current-runtime defects, not only
PRE-GC: restore and `PermanentDeleteRepo` run through the API independently of
`GC_ENABLED`. The SERIAL-domain issue is **config-dependent**: the shipped
`configs/config.prod.yaml` uses `serial_consistency: SERIAL`, but
`LOCAL_SERIAL` is a supported setting (used by the multi-region compose
configurations; startup only logs a warning) and the lease LWTs inherit it.
Both issues are worked in parallel with W2: first the global SERIAL pin of the lease LWTs
(`ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01`), then the generation-fenced
final batch (`ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01`). The SERIAL pin alone
does not close the non-fencing race.

Status: the global SERIAL pin landed in PR #234 for the library, user and org
leases (`ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01` closed); the generation-fenced
final batch (`ISSUE-GC-HARD-DELETE-LEASE-NONFENCING-01`) remains open.


### Confirmed E1 / PRE-GC dependency from G4 cross-audit (2026-10-02)

P1: reachable publication repair can promote permanent fs: references without
binding them to the current physical life or a non-COMMITTED D. E1 must exercise
reachable HEAD -> temporary zero -> COMMITTED D1(P1) -> late repair and prove
that no new P1-dependent durable reference can appear after D1. This blocks
X1 closure / A1 / GC ON until structurally closed or activation fails closed;
it does not block GC-OFF G4/G5 development. A last refs==0 recheck in recovery
does not eliminate the post-check publication race.

Disposition (E1-16, 2026-10-10): **CLOSED-EVIDENCE for repair-adopting
funnels.** The sequence's precondition, temporary zero → COMMITTED D1 while
HEAD reaches a commit that depends on P1, is excluded. The writer's non-expiring repair is
queued before HEAD, and the funnel-independent EACH_QUORUM guard vetoes a new
D until promotion writes `fs:`. E1-11 and E1-2 measured this schedule. A later
promotion writes a logical `fs:` on L, which cannot revoke D1 (G4). The
W2-10 handlers queue no repair and stay under W2-10. A repair for a legitimate
publication on a later life P2 (E1-15C C1) is outside this sequence.

P2 follow-up: an already-authorized delayed PUT can recreate retired K1 bytes.
Characterize physical resurrection/leak at E1/PRE-X1. (E1-PUT-01, 2026-10-03,
Sync PutBlock path: K1 bytes return without metadata and P1 is not
reinstalled, so this is a storage leak, NOT-X1-RED. Formal reclassification of
H belongs to the final E1 list.) The separate greenfield
evaluation of empty-state legacy recovery remains recorded in
GC-G4-IMPLEMENTATION-PLAN.md. The G4 plan contains the confirmed source chain
and the current integration/monitoring corrections.


### E1-10e: unpublished source counterexample and scoped fix (2026-10-06)

Base main@5071d1701624b291a95db9ade8b5ff6abdbd63d7. Productive web upload
followed by Sync PutCommit/RecvFS persists an unpublished commit/tree/file with
SHA-1 block IDs and a canonical mapping, but no permanent fs: or repair.
Only the owned temporary upload reference exists. After its controlled lapse,
real GC reaches COMMITTED or TERMINAL for exact P1 without removing history.
The original RevertDirents handler then successfully publishes HEAD depending
on that retired P1. This is a source-admission counterexample outside Phase5/6;
metadata existence does not prove retained permanent liveness.

RevertDirents non-directory items now require their own settled
fs:<repo>:<historical-file> for every canonical block, capture original exact P,
and recheck those references plus existing exact-P authority immediately before
each HEAD CAS. Missing/malformed/unknown evidence fails the item with HEAD intact.
Live unpublished metadata is deliberately rejected too; normal Sync publication
followed by deletion and restoration remains supported. No pin/pub:/repair is
acquired, no bytes are rematerialized and commit ancestry is not certified.

Measured one root file/path/plaintext block, same repo/org and one DC:
RevertDirents source-admission subset = CLOSED-FIX. Directories, other handlers,
broader batches/layouts, concurrent retention/cleanup, Phase5/6 and R31 remain
OPEN. Foreign settled fs: is not a continuous own-pin proof through HEAD.
W2-10/E1/X1 remain OPEN; production GC OFF, shared development GC unchanged.
See [frozen plan and evidence](./E1-10E-W210-RESIDUAL-DISPOSITION.md).
