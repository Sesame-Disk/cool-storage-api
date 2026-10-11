# E1-15A — stale repair visitor vs concurrent durable cancellation

Frozen base: main@3583d989d39fc6c0e2706b426dde1131c3dcb3a7 (#270 merged).

> **Scope update (2026-10-10, [E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md)):**
> the X1 invariant is aligned with G4 contract 5. The residual post-D pin is
> a logical reference of L, with no reachable HEAD and no P1 reinstall, so its
> P1 label moves from PRE-X1 to PRE-GC. Evidence and severity below are
> unchanged.

## Question

A repair visitor has already loaded repair R and classified it UNKNOWN. While
it is in flight, a legitimate actor durably removes R: the writer's real
known-loser cleanup. GC then commits D(P1). Can the stale visitor, once it
resumes, durably create liveness on P1 after D?

## Static analysis (main@3583d989d)

In `repairPublishedBlockReferenceRepairVisit`, the UNKNOWN path after
`repairAfterClassifyBarrier` runs two durable re-checks
(`publishedBlockReferenceRepairStillPending`): one in the visit, and one at
the start of `renewPublishedBlockReferenceRepairLivenessIfPending`. Only then
does it call `renewPublishedBlockReferenceRepairLivenessFn` →
`AddPublishAttemptReferences` → `AddBlockReference` at LOCAL_QUORUM. That
write checks no claim, fence or D. A post-renewal re-check that observes
"gone" deliberately does not remove the new `pub:<repo:commit:fs>`: since
`ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01`, local absence is not
destructive authority, and the pin is left to its 35-day TTL.

Two windows:

- W-pre: cancellation after classify, before the re-checks. Expected to be
  stopped by the re-checks.
- W-post: cancellation after the last re-check observed R present, before
  the renewal write. Code reading predicts a post-D `pub:` in current runtime.
  This is a hypothesis to demonstrate, not a claim.

CreateFile retries on `ErrLibraryHeadConflict` (`retryLibraryHeadMutation`,
8 attempts, new `up:`/`pub:`/R per attempt). A definitive real loser therefore
needs a real competitor HEAD at every attempt, until the request fails.

## Classification (frozen before runtime)

RED: any durable `block_references` row for the block, created by the stale
visitor after exact COMMITTED D(P1) and present once that visit returns. This
follows the E1-11 ledger criterion "renewal/promotion creates durable P1
after D" and the X1 invariant ("durably add a P1 reference"). Practical harm
(a dead pin that keeps a future incarnation P2 over-retained, no reachable
HEAD) is recorded separately; it does not downgrade the RED. A transient
renewal write that the same visit withdraws before returning is not RED. It
is recorded, and a crash in that window is a documented residual.

## Plan

1. Office/CreateFile writer, real Cassandra/SILO, existing e19 GC-OFF
   isolation; each leg owns an org/library. No CQL insert/delete of refs, no
   repair removal by the harness, no harness candidate, no fabricated HEAD/P/D.
2. Writer attempt 1 is held in-process at `W2PublicationAfterAuthorityBarrier`,
   with durable R1, exact-P authority and no HEAD. R1 eligibility follows the
   E1-13 control: created_at −1h, real lease +1h ahead of the shared daemon;
   the dedicated sweep runs at +2h.
3. The productive sweep visits R1 and classifies natively UNKNOWN, then pauses
   at the existing after-classify observer (W-pre) or at a new
   integration-only observer immediately before the renewal write (W-post;
   no-op in normal builds).
4. Cancellation: release the writer. A real blockless competitor publishes
   HEAD before each of the 8 attempts; every attempt gets applied=false, runs
   the real known-loser cleanup and clears its own R. Require: request not
   201, 8 observed loser cleanups, no repair rows, no `pub:`, no `fs:`, loser
   commits unreachable.
5. Move every real `up:` with the productive renewal API to a common
   seconds-scale deadline and await actual TTL, giving global EQ zero refs.
   Owned-scope Phase 0 must create exactly one natural candidate at exact P1.
   Owned-scope Phase 1 enqueues it; the productive worker over the owned queue
   reaches exact COMMITTED (lifecycle PUBLISHED, orphan COMMITTED, root).
6. Resume the stale visitor and wait for the sweep to return. A query observer
   on the visitor's session records every write to the block's references.
7. Legs: `retained-control` (R kept; renewal written; writer then wins),
   `clear-before-recheck` (W-pre → D → resume), `clear-after-recheck`
   (W-post → D → resume), `fresh-sweep-after-clear` (new sweep after
   cancellation does not visit or renew).
8. Mutations (disposable container): M-pre neutralizes
   `publishedBlockReferenceRepairStillPending`, so the W-pre leg must observe
   a post-D renewal write. If W-post is RED and gets fixed, M-fix reverts that
   fix and must reproduce the W-post RED.
9. On RED: preserve the RED evidence, then make the smallest fix consistent
   with GONE-CHECK-XDC. After renewal, local absence may only retain; it must
   be escalated to an EACH_QUORUM read; confirmed global absence withdraws
   exactly the pins this renewal wrote; read failure keeps the pin (fail
   closed for liveness). Then GREEN, unit tests for the decider, regression.
10. Teardown: TERMINAL continuation of every COMMITTED root, E1-11 metadata/
    repair/K1, E1-12 expiry, E1-13 owners, E1-14 candidates/queue/moved
    projections, plus recovery roots. Completeness gate, negatives, race
    repeats, vets, standard go-all-test, cleanup/quota checks, scoped audit, PR.

## Out of scope

Sync/SeafHTTP/OnlyOffice/cross-repo matrices, bucket contention, Paxos-domain
redesign, bounded discovery, durable loser witness, `pub:` projection,
dead-row or owned-pub over-retention fixes, multi-DC measurement (single-DC
Docker only; the re-check reads at LOCAL_QUORUM, the GC guard at EACH_QUORUM),
Phase5/6, PRE-GC, A1, GC activation.

## Validation

Accepted results are recorded below. Nothing is inferred from this plan.

## Harness

`TestStaleRepairVisitorCancellation` runs in the existing e19 isolated child
(GC OFF on every endpoint it addresses). Each leg owns a new org/library.

- Writer: real CreateFile (Office template) in a goroutine. The existing
  `SetW2PublicationAfterAuthorityForTest` callback holds attempt 1 after
  exact-P authority and durable R1, before HEAD. On release the leg either lets
  it win, or runs a real blockless competitor CreateFile inside the callback
  before every attempt. The existing known-loser observer counts one real
  `ErrLibraryHeadConflict` cleanup per attempt.
- Visitor: the productive sweep body (integration entry at +2h eligibility,
  E1-13 control) on its own Cassandra session. A query observer records every
  `block_references` INSERT/DELETE for the block on that session. Exact
  session/library observers: before visit, after native classify, and the new
  before-renewal observer (after the last durable re-check, before the write).
  The new observer is integration-tagged; normal builds compile a no-op.
- Natural D: every real `up:` (one per attempt) is moved by the productive
  renewal API to a common seconds-scale deadline and retired by actual TTL
  (global EQ zero refs). Owned-scope Phase 0 creates exactly one candidate at
  exact P1; owned-scope Phase 1 enqueues it; the productive worker over the
  owned queue reaches exact COMMITTED: lifecycle PUBLISHED at P1, orphan
  COMMITTED, recovery root present, canonical row retired. The harness never
  creates the candidate.
- After the checks, `w2AssertCommittedContinuation` completes the owned root
  to TERMINAL. Teardown is verified for every fixture (E1-11/12/13/14 plus
  recovery roots).

## RED on current runtime (2700790c9, production code unchanged)

First complete run, single pass:

- `retained-control` PASS: R present at resume; native UNKNOWN renewed
  `pub:<repo:commit:fs>`; writer then won and promoted fs:.
- `clear-before-recheck` PASS: status 409, 8 attempts, 8 real loser
  cleanups, repairs/pub:/fs: absent, loser not in HEAD; natural candidate;
  exact COMMITTED D(P1). Resumed visitor: 0 renewal entries, 0 reference writes,
  refs=[] (the durable re-checks suppress it).
- `clear-after-recheck` **RED**: same cancellation and exact COMMITTED D(P1).
  The visitor, paused after its last re-check observed R, resumed and wrote
  `pub:<repo:commit:fs>`; the post-renewal local gone observation left it.
  Final state: `refs=[pub:<repo:commit:fs>]`, `BlockHasReferencesGlobal=true`,
  canonical row retired, no fs:, HEAD unchanged. Harm: no reachable HEAD
  depends on P1. The dead pin would over-retain a future incarnation of the
  same block id for up to 35 days. Per the frozen rule this is RED.
- `fresh-sweep-after-clear` PASS: a new sweep makes 0 visits and 0 writes.
- Teardown clean in every leg, including the RED leg (20 verifications).

## Fix (7c59cc63a)

`renewPublishedBlockReferenceRepairLivenessIfPending`: after the renewal, a
local "gone" re-check is no longer final. It is escalated to
`publishedBlockReferenceRepairGoneGloballyFn`, an EACH_QUORUM point read of
the repair row (a progress-only residue counts as gone).

- Global absence: `removePublishedBlockReferenceRepairOwnedLivenessFn`
  withdraws exactly this repair-owned `pub:<repo:commit:fs>`; returns gone.
- Globally present: the renewal is owned; returns nil.
- Global read error: retains the pin until TTL and logs; returns gone (the
  pre-fix behaviour).

Why it closes the window: GC's destructive liveness read is refs → EACH_QUORUM
repair scan → refs again. The renewal is a LOCAL_QUORUM write, and the
EACH_QUORUM re-read intersects its quorum. If D committed, the renewal was not
acknowledged before that re-read began. The negative repair scan therefore
precedes the visitor's post-write EACH_QUORUM read, which then observes the
same tombstone. Withdrawal uses global authority, consistent with
`ISSUE-PUBLISH-REPAIR-GONE-CHECK-XDC-AUTHORITY-01`. Its "why not
under-retention" analysis of every tombstone writer in main applies unchanged.

Residuals (OPEN, not fixed): a crash, or a failed withdrawal, between the
renewal write and the withdrawal leaves the dead pin until TTL; so does an
unavailable EACH_QUORUM read. Multi-DC behaviour is argued, not measured.
This is a shared-worker change: every funnel using the shared repair now
withdraws on confirmed global absence.

Design-record compliance
([rejected designs](./PUBLISH-REPAIR-LIVENESS-REJECTED-DESIGNS.md)):
- Not a cherry-pick of #220/#222. It adds no durable producer state, witness,
  lease, generation, pre-pass or deadline (the properties that killed them,
  §2.4/§3.3); the only new effect is a delete of an existing pin.
- It satisfies the mandatory §2.6 rules: local absence never withdraws, the
  destructive decision uses EACH_QUORUM, and unavailable authority fails
  closed (retain). The record certifies EACH_QUORUM for the one-replica-per-DC
  topology; the intersection argument above is the general quorum one, and
  only single-DC Docker is measured.
- It changes the earlier answer to §8.8 question F ("keep the TTL-bounded pin
  and let it expire"). E1-15A shows that pin can be a durable post-D
  reference, so for confirmed global absence it is now withdrawn. Accepting
  the TTL pin stays the answer for crash/unavailable cases.

Unit tests: `TestRenewIfPendingWithdrawsRenewalOnlyOnGlobalAbsence`
(absence withdraws exactly once with the per-repair identity; present and
unavailable retain; the global read follows the write);
`TestRenewIfPendingDoesNotConsultGlobalAuthorityWhileLocallyPending`. The
existing source guard now also requires withdrawal to appear only after the
EACH_QUORUM decider. Existing GONE-CHECK tests pass unchanged; with a nil
session the decider is unavailable and retains.

## Accepted checks on 7c59cc63a

Container SHA-256: test e115a f467afdef944940cfd39b08de9d861fa578be5682b56a546a8b32d5e28f77928,
publish_repair.go cf8e7f4ef05a9a588c87430b85672ea345b2ddeb36bc79ec68533c3ce7d85ceb.

- Matrix after the fix: 4/4 PASS. `clear-after-recheck` records exactly one
  transient write of `pub:<repo:commit:fs>` and exactly one withdrawal of it
  by the same visit; final refs=[]; no fs:/HEAD change.
- Race `-race -count=3`: 12/12 legs across three fresh isolated children,
  6 GREEN post-D legs, 0 RED, 60 teardown verifications, 0 data races;
  150.221s package, 238s wall.
- `go test ./internal/api/v2/` PASS; both vets PASS.
- Three own gate negatives PASS (filtered, unavailable, filtered child).
- Causal mutations PASS (`scripts/e115a-stale-visitor-mutation.sh`):
  M-fix (decider forced to "not gone") reproduces the RED on
  `clear-after-recheck`; M-pre (re-check treats an absent row as pending)
  produces a durable post-D pin on `clear-before-recheck`. Both reach exact
  COMMITTED first and pass all teardown verifiers. A first M-pre attempt did
  not compile (the comment swallowed `, nil`); the script rejected it as
  build-failed and it is not evidence.
- Prior matrices with the fix: E1-11, E1-12, E1-13, E1-14 with their
  mandatory gates, exit 0 (188.538s).

## Standard regression and final audit

- Standard Docker go-all-test on 7c59cc63a: exit 0, 2026-10-07 19:08–19:31
  local. `go test ./... -short` PASS; integration 1009.387s (E1-15A isolated
  child PASS, 4 E1-15A teardown verifications); API 20/20 suites; OIDC 25/25
  tests. The 74 SKIPs are the existing optional 3-DC/topology suites and are
  not claimed as executed, so the shared-worker change has no multi-DC run.
- Afterwards both backends report `CLEANUP_STATUS: clean`, with quota_usage 0,
  storage_quota 2000000000 and policy hard unchanged.
- Scoped audit. The production diff is the EACH_QUORUM decider, the
  conditional withdrawal and a no-op barrier call; no schema, migration, GC,
  scanner, TTL or config change. Plan items 1–10 were executed except merge
  and activation. The frozen RED rule was applied as written. Prohibitions
  held: no CQL insert/delete of references, no harness repair removal or
  candidate, no fabricated HEAD/P/D. Two items were corrected during
  development, before acceptance: the M-pre build failure (rejected by the
  script) and the leg check, strengthened to require the exact transient
  write plus its withdrawal. No unresolved introduced P0/P1/P2 in the
  measured scope. The design-record and GONE-CHECK documents now carry dated
  notes, because `main` has an EACH_QUORUM absence check again.

## Disposition

- E1-15A shared repair cancellation, measured Office single-block schedules:
  **PARTIAL-FIX / CLOSED-EVIDENCE only when the global check and the withdrawal
  both succeed**. A stale visitor that has already classified leaves no durable
  reference after a legitimate durable clear followed by exact COMMITTED D(P1)
  only on that path. A visit that ends normally can still leave the pin (see
  the residual paths below).
  It is suppressed before the re-checks; after them it writes and then
  withdraws (best-effort compensation after the write, not a fence).
- Strict post-D no-reference guarantee: **OPEN**.
  E1-15A shows that an Office visit which completes its global check and
  withdrawal leaves no post-D reference. The fix does not prevent the transient
  creation of that reference, and it does not guarantee withdrawal after a crash,
  a failed DELETE or unavailable authority. The strict X1 guarantee therefore
  stays OPEN: the measured mitigation is proven, but it is not a publication fence.
- `ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01`: **P1 PRE-X1, mitigated,
  OPEN residual**.
- E1-15 overall: **OPEN** (Sync/SeafHTTP/OnlyOffice/cross-repo specifics,
  bucket contention, owned-pub cleanup race, cancel/retry interleavings).
- Residual paths (OPEN, bounded by TTL), all reachable without any code
  change, each leaving a durable post-D pin:
  1. crash after the acknowledged renewal INSERT, before the global check or
     the withdrawal;
  2. EACH_QUORUM check error: a warning is logged and Gone returned, so the
     visit ends normally with the pin retained;
  3. withdrawal DELETE error after confirmed absence: a warning is logged and
     Gone returned, so the visit ends normally with the pin retained;
  4. ambiguous or partial renewal: `renewPublishedBlockReferenceRepairLivenessFn`
     returns an error after `AddPublishAttemptReferences` already wrote some
     blocks, or after a timed-out INSERT that applied. The function returns at
     once, without the re-check or the global check, so nothing withdraws
     those pins. This path predates #271.
- W2/R31, E1/X1 and GC activation: OPEN.

## Review reclassification (2026-10-07)

An external review of #271 accepted the RED, the harness, the fix and the
evidence, and found no productive regression. It rejected the unqualified
CLOSED-FIX label: the fix removes the post-D reference but does not stop it
from being written. The frozen plan already listed the crash window as a
residual, but the label promised more than the strict X1 rule
("durably add a P1 reference"). Runtime is unchanged by this reclassification.

Shared residual class (hypothesis from code reading, not measured): writer
funnels use the same write-before-validate order. CreateFile stages `pub:`,
queues its repair and only then validates exact-P fences, cleaning up on
rejection. If D is already COMMITTED, that `pub:` is also written post-D, and a
crash before cleanup leaves it (plus a repair row the sweep may renew). Those
rows are CLOSED-FIX for pre-HEAD publication and were never measured against
the strict reading. Whatever criterion is adopted for E1-15A should apply to
this whole class: liveness written post-D before validation, left behind by a
crash. Their rows are not reclassified until measured.

Multi-DC, after-recheck positive local read (unmeasured, P1 PRE-X1). For the
visitor to still see R in DC-B after GC's EACH_QUORUM scan observed absence,
the DC-B replicas it reads would have to lack the tombstone. That EACH_QUORUM
scan reads a quorum in every DC, including DC-B, and with Cassandra 5's
default BLOCKING read repair it writes the tombstone to those replicas before
answering. The repair table does not override `read_repair` in any migration.
D comes after that scan, so the visitor's later LOCAL_QUORUM read intersects
the repaired quorum, also with one replica per DC. This argument depends on
read repair staying enabled for the table and is not measured: OPEN until a
3-DC leg runs.
