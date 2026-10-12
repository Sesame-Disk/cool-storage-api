# E1-15C — post-D references, terminal retirement and P2 rematerialization

Frozen base: main@304f6da8f7c30e4ad816d17d061dcde6816c0854 (#272 merged).

> **Decision (2026-10-10):** the contract reconciliation proposed here was
> approved as alignment with G4 contract 5 ([E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)).
> The strict "no post-D reference" reading is retired. Post-D references and
> abandoned repairs are retention/convergence (PRE-GC or follow-up). The
> physical results below are unchanged.

## Question

E1-15A/B showed that current code can leave references on block L that were
written after exact COMMITTED D(P1). Do they endanger the physical life P1, or
any later legitimate life of L? Or is the demonstrated defect retention and
convergence? Concretely:

1. Do post-D `pub:` and a dead durable repair R block or revert terminal
   retirement of P1, or reinstall P1?
2. After P1 is TERMINAL, can a real rematerialization install P2 and publish
   a HEAD that depends only on P2 while R is still present?
3. Can L's later lives be collected once unreferenced while R is present?

## Contracts to reconcile

- D0 §13 ([GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md#13-late-refs))
  is physical: after irreversible D, late `up:`/`pub:` "must not make P1 live
  again". It also says post-D refs "remain a contradiction/veto (CURRENT /
  TRANSITIONAL)". Current code forbids exactly that:
  `BlockPublicationLivenessGlobal` "must not be used as a post-COMMITTED
  contradiction read".
- The E1 ledger is stricter: no current operation may "durably add a P1
  reference" after D.

## Static analysis (main@304f6da8f)

- References are keyed by logical block L, not by physical life P. A pin on L
  after a new install pins that new life.
- R classifies by commit reachability. Its commit c1 is a definitive loser
  that never becomes an ancestor of HEAD, so R stays UNKNOWN indefinitely and
  renews on every visit.
- GC's destructive proof scans pending repairs for L at EACH_QUORUM. A dead R
  for L should therefore veto D of every later life of L in that org.
- Expected: P1 stays retired (no physical RED). The demonstrated defect is
  retention/convergence, with dead R as its unbounded part. Hypothesis only.

## Classification (frozen)

- P1 PRE-X1 confirmed (physical): any path that reinstalls P1, prevents or
  reverts its terminal retirement, or publishes a HEAD that depends on
  retired P1.
- FOLLOW-UP (retention/convergence): P1 stays retired, and the only defect is
  references, repairs or space retained, including a veto that blocks
  collection of later lives. The strict-E1 reading keeps its PRE-X1 severity
  until the contract is explicitly reconciled.
- OPEN/UNKNOWN: not reproduced and not positively excluded.

## Plan

1. Office/CreateFile, real Cassandra/SILO, existing e19 GC-OFF isolation;
   one owned org/library per leg. No production change. No CQL insert/delete
   of references or repairs, no harness candidate, no fabricated HEAD/P/D/P2.
2. Reuse the E1-15B subprocess writer, the E1-15A natural-D helper and the
   E1-15B conditional finalizer. Every leg records P1 and, where present, P2
   as exact `(storage_class, storage_key)`.
3. Legs:
   - `post-d-pub-terminal` (A): crash after staging after D. Productive
     continuation to TERMINAL with the post-D `pub:` present. Assert
     lifecycle TERMINAL, K1 absent, orphan/root absent, canonical still
     absent, the post-D `pub:` still present, HEAD unchanged.
   - `post-d-repair-terminal` (B): crash after queueing after D, then two
     productive UNKNOWN sweeps renew post-D. TERMINAL with `pub:` and R
     present; same assertions, R retained.
   - `p2-published-with-dead-repair` (C1): B after TERMINAL, then a real
     in-process CreateFile of the same content in the same library. Require
     201, P2 key ≠ P1 key, K1 absent, P2 canonical and bytes present, HEAD
     tree with the new file only, its fs: present, and ~~no fs: for R's
     fs_id~~ *(corrected after review: fs_id is content-addressed, so the new
     file's fs_id must equal R's, and that shared `fs:` legitimately belongs
     to P2; the test now requires the equality and the fs: surviving R's sweep)*.
     Then a productive sweep of R: record classification and writes.
   - `dead-repair-blocks-unreferenced-p2` (C2): B after TERMINAL, then a
     real rematerialization whose writer dies after staging (in-process
     abrupt stop, E1-14 pattern). Every remaining real reference (P2 `up:`,
     P2 `pub:`, R's renewed pin) is shortened through its productive write
     API and retired by Cassandra TTL, giving global EQ zero refs. Owned-scope
     Phase 0 creates the natural P2 candidate; Phase 1 enqueues it; the
     productive worker runs. Record whether D(P2) is vetoed, the candidate is
     retained and P2 stays present, then a further R sweep.
4. Teardown: E1-11/12/13/14/15A verifiers, now applied to the current
   target (P2 where installed), plus explicit P1/K1 absence.
5. Completeness gate, gate negatives, race repeats, vets, standard
   go-all-test, cleanup/quota checks, scoped audit, PR.

## Out of scope

Fences, `AddPublishAttemptReferences`, repair worker, tables/generations/
leases/witnesses, GC Phase 0–6, hot-path checks, Sync/SeafHTTP/cross-repo,
GC activation, and any rewrite of D0. The reconciliation is a written
proposal for an explicit decision, not a change to the contract.

## Validation

Accepted results are recorded below. Nothing is inferred from this plan.

## Results (test code 9af0cda0d, no production change)

Matrix 4/4 PASS on the first complete run. Race `-count=3`: 12/12 legs,
12 TERMINAL-with-post-D-refs checks, C1 and C2 3/3 each, 60 teardown
verifications, 0 data races (198.812s package, 266s wall). Vets and three
gate negatives PASS. Container test SHA-256
96913e578d00f7bab05bffe5e866ea23c6b3e95172a024c1016a19bc39e5eec7.

- **A `post-d-pub-terminal`**: the post-D `pub:<commit>` stays present
  throughout. The productive continuation completes D1 to TERMINAL at P1; K1,
  orphan and root are absent; canonical L is not reinstalled; the references
  are unchanged; HEAD unchanged; no fs:.
- **B `post-d-repair-terminal`**: dead repair R plus two productive UNKNOWN
  sweeps that renew `pub:<repo:commit:fs>` post-D. TERMINAL is reached with
  both pins and R present; the same assertions hold; R is retained.
- **C1 `p2-published-with-dead-repair`**: after TERMINAL, a real in-process
  CreateFile of the same content returns 201. It installs **P2 with a new
  storage key**, K1 stays absent, and K2 bytes are correct. HEAD advances and
  its tree contains only the new file, not the loser. **The new file has the
  same content-addressed fs_id as R** (`520fa00a…`), so its legitimate
  `fs:<repo:fs_id>` is the very identity R would promote. A further R sweep
  stays native UNKNOWN (R classifies by commit, c1 is never an ancestor) and
  renews its pin on L, which now pins P2. HEAD and P2 are unchanged.
- **C2 `dead-repair-blocks-unreferenced-p2`**: after TERMINAL, a real
  rematerialization installs P2 (new key), and its writer dies after staging.
  Every remaining real reference is retired by its productive write API plus
  Cassandra TTL (P2 `up:`, P2 `pub:`, the original post-D `pub:` and R's
  renewed pin), giving global EACH_QUORUM zero refs. Owned-scope Phase 0
  creates the natural P2 candidate; Phase 1 enqueues it; the productive worker
  visits it and **vetoes D(P2)**: n=0, candidate retained, no P2 lifecycle,
  K2 present. The only remaining liveness is the dead R. A further R sweep
  re-pins L.

## Review corrections (two P2 evidence findings, both confirmed)

1. C1 did not require the fs_id equality it reports. The check
   `!hasNew || (repairFS != newFS && hasRepair)` passed whenever the IDs
   differed and R's `fs:` was absent, yet the settlement constraint is derived
   from their equality. C1 now requires `created fs_id == repair fs_id`, the
   shared legitimate `fs:` present after publication, and the same `fs:`
   still present after R's sweep. The frozen-plan sentence that expected "no
   fs: for R's fs_id" is struck through with a correction note.
2. C2 did not observe why the worker vetoed. `w2ClosureOwnedQueue` set
   `visited` before delegating and did not keep the answer, and
   `ProcessOrgOnce` can absorb a failed liveness read and still return n=0. The
   shared wrapper now records every productive `BlockPublicationLivenessGlobal`
   answer and error for its owned block. This is additive; the other eight
   callers are unchanged. C2 first runs the direct productive probe
   (`w2AssertGuardOnly`: zero real refs → `RepairGuardOnly`), then requires every
   answer the worker itself obtained to be `RepairGuardOnly` with no error.

Re-run on d3fcef19f (container SHA-256: E1-15C test
6ca057c5b85abf59589f849fe5eacc5d90f02aee8c5c9897111aa7d7ba5eb1fb, wrapper
71bed7a7f34d8d1035aeadcec78c21c8bd6841efa3a54ee04597e56558f5ce09):
race `-count=3` 12/12. C1 required equality 3/3. C2: the direct guard probe
passed 3/3, and the worker's own proof returned exactly one `RepairGuardOnly`
answer, without error, in each run. 60 teardown verifications, 0 data races
(231.391s package, 300s wall). Vets and three gate negatives PASS. The earlier
9af0cda0d results above are historical.

**Accepted standard regression on d3fcef19f** (30m budget): exit 0,
2026-10-08 16:26–16:57 local. Integration 1562.886s, with the E1-15C child
PASS, including C2's worker `RepairGuardOnly` answer. The shared wrapper's
other callers pass. API 20/20; OIDC 25/25; the same 75 SKIPs. Both backends
report `CLEANUP_STATUS: clean`, with quota_usage 0 and hard limits unchanged.
**The integration package now uses 87% of the 30m budget** (1562.9s of 1800s),
so the next E1 PR must raise or split the budget before adding legs. The
9af0cda0d regression below is historical.

## Standard regression and final audit

- Standard Docker go-all-test on 9af0cda0d (30m budget): exit 0,
  2026-10-08 15:01–15:30 local. `go test ./... -short` PASS; integration
  1409.711s (E1-15C isolated child PASS, all four legs); API 20/20 suites;
  OIDC 25/25. The 75 SKIPs are unchanged from #272 (optional 3-DC/topology
  plus the E1-15B parent-only helper) and are not claimed as executed. The
  suite now uses 78% of the 30m budget; the next E1 PR should re-check the
  margin.
- Afterwards both backends report `CLEANUP_STATUS: clean`, with quota_usage 0,
  storage_quota 2000000000 and policy hard unchanged.
- Scoped audit. No production file changed; the diff is one integration test,
  gate wiring, a gate-negatives script and docs. Plan items executed except
  merge/activation. Prohibitions held: no CQL insert/delete of references or
  repairs, no harness candidate, no fabricated HEAD/P/D/P2. P2 came only from
  real CreateFile rematerialization. Reference retirement in C2 used only the
  productive write APIs (`AddProvisionalBlockReferenceWithExpiry`,
  `AddBlockReference` on the same referrer) plus Cassandra TTL.
  During development a mid-leg root check reused the E1-15A teardown
  verifier, which inflated the "teardown verified" count. It was replaced by
  a local check before acceptance. No unresolved introduced P0/P1/P2. No
  severity was lowered; the reconciliation is a proposal only.

## Disposition

- **Physical safety (D0 §13), measured Office subset: no RED.** In none of
  the four schedules do post-D references or the dead repair reinstall P1,
  prevent or revert its terminal retirement, publish HEAD on retired P1,
  promote an illegitimate fs:, or alter a legitimate P2. Not proven for
  other funnels.
- **Strict E1 reading: still RED** (E1-15A/B). Durable references are
  written after D.
- **Demonstrated harm: retention and convergence, unbounded.** A dead repair
  for an attempt that can never reach HEAD stays UNKNOWN forever, renews L on
  every visit and makes GC veto D of every later life of L in that org, even
  when that life has no other reference. Classified **FOLLOW-UP** by the
  frozen rule, but it is the priority follow-up: any destructive-GC
  activation would accumulate permanently uncollectable blocks.
  `ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` is upgraded with this evidence.
- **Design constraint for that fix (measured):** a dead repair's
  `fs:<repo:fs_id>` identity can coincide with a live publication's, because
  fs_id is content-addressed. Settling a dead repair must never remove
  `fs:<repo:fs_id>`. It may only drop its own repair-owned `pub:` and the row,
  and only on authority that the attempt can never publish.

## Amendment after E1-15D (2026-10-09)

[E1-15D](./E1-15D-POST-D-CONTRACT-DECISION.md) measured that the
abandoned commit c1 behind R can still be published: real Sync
`UpdateBranch?head=c1` auto-merges its content into HEAD. "Dead repair" above
therefore means *abandoned*, not *provably unpublishable*. The C2 veto of
D(P2) protects a still-publishable commit rather than being pure dead
retention. The retention harm stands, but its cure is a settlement backed by
durable authority (E1-15D §3, design open), not deleting R today. Physical
results are unchanged.

## Reconciliation proposal (for an explicit decision; nothing is changed)

1. Make X1 safety the D0 physical invariant: after D, no operation may
   reinstall P, revert its retirement, or publish reachable content that
   depends on the retired P. This is the property the measured legs exercise.
2. Turn the E1 "no durable post-D reference" wording into a
   retention/convergence requirement with its own pre-activation gate: dead
   references and repairs must converge. It should no longer be a physical
   safety claim.
3. Remove or rewrite the D0 §13 "post-D refs remain a contradiction/veto
   (CURRENT / TRANSITIONAL)" sentence. Current code forbids post-COMMITTED
   revocation, and E1-15C shows retirement completes despite post-D refs.
4. Make the next PR the dead-repair settlement (convergence), under the
   constraint above, not a write fence on every writer.

Until that decision is taken, E1-15A/B keep their P1 PRE-X1 labels.
E1-15C does not lower any severity by itself.
