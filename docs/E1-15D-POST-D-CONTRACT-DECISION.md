# E1-15D — post-D contract decision and repair cancellation authority

Frozen base: main@24372ba95fad (#273 merged).

## Purpose

E1-15A/B/C produced enough evidence to decide what X1 guarantees and what must
converge before GC activation. This PR records that decision as a proposal for
explicit approval and defines what authority a future repair settlement needs.
It changes no production code. One cheap evidence pair checks the premise
the authority depends on: can an abandoned v2 commit still be published?

## Static analysis (main@24372ba95)

- `publishedBlockReferenceRepairCommitDefinitelyNotReachable` exists, but the
  classifier deliberately has no emitter for it. Even if it were emitted,
  settlement would retain the row ("no durable cleanup authority").
- HEAD writers, all CAS on the expected HEAD at SERIAL:
  - v2 `UpdateLibraryHeadFromSnapshot`: new commit, parent = expected HEAD;
  - Sync `updateLibraryHeadWithStats`: target with parent = HEAD
    (fast-forward), or a fresh merge commit with parent = HEAD;
  - `InitializeLibraryHeadIfUnset`: only when HEAD is null;
  - `AdvanceLibraryCertifiedFrontier`: no production caller.

  Every write moves HEAD to a child of the current HEAD; commits are
  immutable (PutCommit write-once, #208). By code reading, HEAD never returns
  to an earlier commit.
- Publication paths for an existing commit c1 (an abandoned v2 attempt):
  1. The stale v2 writer's CAS `IF head = c1.parent` can apply while
     HEAD == c1.parent.
  2. Sync `UpdateBranch?head=c1`: fast-forward if c1.parent == HEAD, otherwise
     **auto-merge** when HEAD descends from c1.parent. With monotonic HEAD
     that ancestry never stops being true, but `syncCommitHasAncestor` walks
     at most 1024 commits from HEAD; past that it returns an error and the
     handler answers 500 ("failed to inspect sync commit ancestry") without
     publishing. So auto-merge of c1 stays possible while HEAD is within 1024
     first-parent commits of c1.parent, not at every depth. The bound is an
     implementation constant, not an authority. The merge commit records
     only the current HEAD as its parent, not c1.
- Consequence (hypothesis to measure): the E1-15B/C repair is not provably
  dead. c1's content stays publishable through Sync. The E1-15C C2 veto of P2
  protects a still-publishable commit rather than pure dead retention.

## Evidence (one test, two legs, no production change)

Real Office/CreateFile writer subprocess, SIGKILL after repair queueing and
insertCommit (E1-15B schedule); real blockless competitor advances HEAD; real
in-process Sync `UpdateBranch?head=c1`, the existing W2 closure invocation:

- `sync-promotes-abandoned-commit`: no GC. Expected: Sync auto-merges c1's
  content into HEAD. Record status, the new HEAD, its parent, the published
  file and fs:, and the repair's later native classification.
- `sync-promote-after-terminal`: the same, but after a natural exact
  COMMITTED D(P1) completed to TERMINAL (E1-15A/B/C helpers). Expected: the
  existing Sync exact-P/fence path rejects; HEAD unchanged; P1 not
  reinstalled; K1 absent; no fs: for c1's fs_id.

Same prohibitions as E1-15A–C: no CQL insert/delete of references/repairs,
no harness candidate, no fabricated HEAD/P/D. Teardown, completeness gate,
gate negatives, race repeats, vets, standard go-all-test, cleanup/quota.

## Evidence results (test 24d88a6b0, no production change)

`TestAbandonedCommitPublishability`, e19 isolated child.

- **`sync-promotes-abandoned-commit` — confirmed.** The writer subprocess is
  SIGKILLed after queueing and insertCommit, leaving durable R (UNKNOWN) and
  a complete commit c1 with parent H0. A competitor advances HEAD to H1. Real
  Sync `UpdateBranch?head=c1` returns **200**, auto-merges and publishes
  HEAD M with parent = H1 (not c1). M's tree contains c1's file with c1's
  fs_id, whose `fs:` is now present on exact P1. **R was protecting a
  commit that could still be published.** A later productive repair sweep
  (real `RunPublishedBlockReferenceRepairSweepAt` on an observed session, R
  aged into eligibility) still classifies R **natively UNKNOWN**: M names
  H1, not c1, so the ancestry classifier cannot see that c1's content is
  now published. The sweep retains R, renews only R's own
  `pub:<repo:c1:fs>`, deletes nothing, and the `fs:<repo:fs_id>` that Sync
  published stays. Added after the cross audit (P2-2); see Validation.
- **`sync-promote-after-terminal` — fail-closed.** Same, but after a natural
  exact COMMITTED D(P1) completed to TERMINAL: the promotion is rejected
  (HTTP 500, "failed to auto-merge sync head"). The test captures the
  handler log and requires the cause: "publication readiness check failed
  for auto-merged commit … block … is not currently reusable (… block delete
  is in progress)". HEAD stays H1; P1 not reinstalled; K1 absent; no `fs:`.
  The status mapping (500, not a retryable 409) is a minor UX observation;
  not changed here.

Accepted checks: matrix 2/2; race `-count=3` 6/6 (3 auto-merge publications,
3 gate-attributed rejections), 30 teardown verifications, 0 data races
(153.757s package, 263s wall); vets and three gate negatives PASS. Container
SHA-256 7e988a8bb98f9dfa1239921b42f4167218d8d5ab153957ca9a8045f3338f713f.

## Standard regression and final audit

- Integration budget raised 30m → 40m in both Compose runners: #273's
  accepted run used 87% of 30m, and E1-15D adds another mandatory isolated
  child. Time budget only.
- First standard go-all-test: exit 1. Two failures in tests this PR does
  not touch:
  - `TestKnownLoserCrashSafety/crash-pending`: "wrong definitive loser:
    (empty)". This is an E1-12 harness race: `e112AwaitFile` only stats the
    marker, and the parent read it between the child's create and write.
    Fixed in db1cb229a (test only): the reader now polls for non-empty
    content. E1-12 then passed 3/3 isolated.
  - `TestLibraryProjectionRegression_ReconcilePendingStorageCountersAfterSoftDelete`:
    a 5s async-projection timeout, in the known single-node projection flake
    class. It passed 3/3 isolated; not changed.
  Not accepted as evidence.
- **Accepted** go-all-test on db1cb229a: exit 0, 2026-10-08 20:44–21:17
  local. Integration 1736.896s, with the E1-15D child PASS: one auto-merge
  publication and one gate-attributed rejection. API 20/20; OIDC 25/25;
  the same 75 SKIPs. The run used 72% of the 40m budget; it would have used
  96% of the former 30m.
- Afterwards both backends report `CLEANUP_STATUS: clean`, with quota_usage 0
  and hard limits unchanged.
- Scoped audit. No production file changed. The diff is the E1-15D test,
  gate wiring, a gate-negatives script, the E1-12 marker-read fix, the budget
  change and docs. Prohibitions held. A first draft asserted only a non-200
  rejection; before acceptance it was strengthened to require the exact Sync
  readiness-gate cause from the handler log. One unpushed commit message was
  corrected to mention the Compose gate wiring. No unresolved introduced
  P0/P1/P2. No severity was changed: every disposition here is a proposal
  pending approval.

## Decision proposal (requires explicit approval)

**Approval status (2026-10-10).** The project owner approved section 1 as an
alignment with the G4 contract (2026-10-02, #247; G4 contract 5: "References
of L after D1 do not authorize, cancel or postpone deleting P1"), not as new
architecture. See [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md). Consequences
there: E1-15A/B move from PRE-X1 to PRE-GC with severity unchanged. Which
convergence items gate A1 is still decided per issue tag;
`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` kept its FOLLOW-UP / GENERAL
scope here. Its own decision (2026-10-12) made it P2 PRE-GC, measure first. Section 2 remains the authority record, except
that its row "HEAD ≠ c1.parent" is superseded by the SUPERSEDED settlement of
[E1-15E](./E1-15E-ABANDONED-REPAIR-SETTLEMENT.md) and
[E1-15F](./E1-15F-SUPERSEDED-CROSS-DC.md). The text below preserves the
proposal as written.

### 1. Two contracts, stated separately

| Contract | Statement | Measured status |
| --- | --- | --- |
| **X1 — physical safety** | After exact COMMITTED D(P1), no operation may reinstall P1, revoke or revert its retirement, or publish HEAD with reachable content that depends on retired P1. | Office: no RED (E1-15C, plus E1-15D's after-TERMINAL rejection). Other funnels are not proven. |
| **PRE-GC — convergence** | Abandoned references and repairs must not indefinitely prevent reclaiming later lives of the same block (P2…), and must converge without weakening X1. | Not met: E1-15C (a repair vetoes D(P2)). |

The E1 ledger sentence "no current operation may durably add a P1 reference
after D" is not deleted. It is re-scoped as a convergence obligation, because
E1-15C/D show post-D references neither revive P1 nor bypass the publication
gate. E1-15A/B keep their P1 PRE-X1 labels until this split is approved. The
D0 §13 "post-D refs remain a contradiction/veto (TRANSITIONAL)" sentence must
be rewritten to match the code, which forbids post-COMMITTED revocation.

### 2. Cancellation-authority matrix for a repair R(c1)

| Situation | Authority today | Action |
| --- | --- | --- |
| c1 positively REACHABLE from HEAD | yes (existing classifier) | normal settlement (promote fs:, drop R) |
| writer observed applied=false | yes, but **process-local**: the request clears R synchronously | existing known-loser cleanup; a crash before cleanup loses the authority (E1-12) |
| c1 not found on HEAD's first-parent chain | **no** | retain. Sync can still auto-merge c1's content into a HEAD that never names c1, and the classifier then still says UNKNOWN (E1-15D) |
| HEAD ≠ c1.parent (stale v2 CAS can no longer apply) | **partial**. With monotonic HEAD the Office attempt can never publish c1, but whether removing R is then safe against a concurrent Sync promotion of c1 is not analyzed (section 3) | retain |
| R UNKNOWN, old, or retries exhausted | **no**. Time is never authority | retain |
| ambiguous read / DC unavailable | no | fail closed (retain) |

`DefinitelyNotReachable` must not get an emitter until an authority source
exists. Today none does.

### 3. What a future settlement of R needs (open design)

R protects the *Office attempt* c1: its staged references for c1's content
until that attempt is published or definitively lost. Sync publication of
c1's content is a different publication with its own protection. Two
obligations, kept separate:

1. **Authority that the Office attempt itself can no longer publish c1.**
   The stale v2 CAS `IF head = c1.parent` is defeated once HEAD ≠ c1.parent,
   and HEAD monotonicity (audit above) makes that durable. No new state is
   needed for this part. What is missing is durable authority to *act* on
   it from the repair path: the classifier has no such emitter, and the
   request-local `applied=false` dies with the process (E1-12).
2. **Any later Sync publication of c1's content takes its own protection.**
   By code reading it already does: `handleSyncHeadPromotion` stages its own
   block delta, passes publication readiness (exact P) and queues its own
   repair before the HEAD CAS; `tryAutoMergeSyncHeadPromotion` builds a new
   merge commit and does the same for it. E1-15D measured both outcomes:
   with P1 live, Sync publishes and installs its own `fs:`; after TERMINAL,
   readiness rejects. Neither depends on R.

The runtime already relies on this split: on a definitive
`ErrLibraryHeadConflict`, CreateFile runs `CleanupFailedPublishAttempt` and
`clearPendingPublishedFileRepairs` for its losing commit, without cancelling
that commit globally, although Sync could later auto-merge its content.

This does **not** prove that a new repair-path settlement is safe. Still
open, and required before any settlement code:

- the durable authority source for (1): who may emit it, from what read,
  at what consistency;
- concurrency between removing R's `pub:` and a Sync promotion of c1 that is
  already in flight (staged but not yet past readiness, or past readiness
  but before its HEAD CAS), and the GC zero-proof in between;
- the relation to new publications of the same fs_id.

Design options, none chosen:

- (A) Settle R on (1) alone, relying on (2) for Sync, after the concurrency
  analysis above and its RED→GREEN. Smallest change; no Sync change if the
  analysis holds.
- (B) Conservative: additionally make c1 globally unpublishable, for
  example an LWT-retired commit row or cancellation marker that Sync
  promotion checks in the HEAD CAS serial domain. New protocol in Sync and
  possibly schema. A fallback if (A) fails its analysis, not a demonstrated
  requirement.

Whatever option is chosen, a settlement may remove exclusively:

- R's own repair-owned `pub:<repo:commit:fs>`;
- the R row.

It must never remove `fs:<repo:fs_id>`. fs_id is content-addressed, and
E1-15C/D measured a live publication sharing it. It must also never touch
another attempt's `pub:<commitID>`, any `up:`, other repairs, HEAD/commits, or
a later life P2.

### 4. Corrections to E1-15C wording

E1-15C described R as "dead" and the P2 veto as caused "solely" by a dead
repair. E1-15D shows c1 stays publishable through Sync while R exists. The
C2 veto therefore protects a commit that is abandoned but still publishable.
The retention harm is real; its cure is a settlement backed by durable
authority (section 3), not deleting R today. The E1-15C and DEAD-ROW-RETENTION records are amended
accordingly. No severity changes.

### 5. Recommended sequence

1. Approve or amend sections 1–2. This is a contract decision; nothing is
   changed by this PR.
2. Next PR: evaluate the minimal settlement first (section 3, option A):
   the authority source for obligation 1 and the concurrency analysis
   against in-flight Sync promotion, with RED→GREEN, including a leg where
   Sync promotes c1 while or after R is settled. Option B (global commit
   cancellation) only if A fails that analysis. No commitment to B here.
3. Then the settlement of R under the exact removal scope of section 3.

## Out of scope

Any change to `publish_repair.go`, Sync, writers, GC Phase 0–6, schema,
migrations, hot paths, automatic UNKNOWN deletion, GC activation. No severity
is lowered without explicit approval.

## Validation

Accepted results are recorded below. Nothing is inferred from this plan.

### Cross-audit follow-up (2026-10-09)

Two THIS-PR P2 findings from the cross audit of c043d7b8e, both accepted:

- **P2-1 (contract).** Section 3 presented global cancellation of c1 as
  mandatory without evidence. Rewritten: settlement authority for the
  Office attempt is separated from the protection a later Sync publication
  takes for itself; global cancellation is option B, not a requirement.
  The "auto-merge holds forever" wording is corrected to the 1024-commit
  ancestry walk bound. Matrix row "HEAD ≠ c1.parent", sections 4 and 5 and
  the linked records are aligned. No severity changed.
- **P2-2 (evidence).** `sync-promotes-abandoned-commit` now runs a productive
  sweep after the auto-merge and requires: native classification UNKNOWN,
  exactly one renewal (R's own `pub:`) and no deletes, R the only queued
  repair before and after, the Sync-published `fs:<repo:fs_id>` still
  present and HEAD unchanged.

Rerun on the amended test, isolated e19 child, unrelated `REQUIRE_*` gates
unset: race `-count=3` 6/6 PASS plus completeness 3/3; 3 post-merge native
UNKNOWN sweeps, 3 gate-attributed rejections, 30 teardown verifications, 0
data races (149.048s package, 214s wall). Three gate negatives and
`go vet -tags integration ./internal/integration/` PASS. No production
file changed. The standard go-all-test above ran on db1cb229a, before this
test-only addition; it was not repeated.
