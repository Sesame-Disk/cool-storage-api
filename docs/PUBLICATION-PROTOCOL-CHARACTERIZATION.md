# PC-0 — Multi-DC Publication Protocol Characterization

## Greenfield first-production reconciliation (2026-09-30)

W2-0 is **CLOSED-EVIDENCE for the covered current-version mechanism** under
the [first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
The #239 durable repair / final exact-P ordering and #241 native wire loss,
real SIGKILL, independent recovery and three-DC evidence are preserved.
Both incompatible mixed-version combinations still reproduce D(P)+HEAD;
ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01 tracks them as P1 FOLLOW-UP / GENERAL,
outside v1 first activation. No compatibility gate is inferred or added.
W2-1, W2-2 and W2-6a remain OPEN through the separately tracked R31 residuals.
Other W2 rows are unchanged. W2 completion -> G4 -> G5 -> E1 -> X1 CLOSED ->
PRE-GC -> A1 -> GC ON remains the #201/#236 path; GC stays disabled.

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

**Status:** characterization complete (PC-0, #211). PC-1 (2026-09-11) added the
common types and the `PublicationCoordinator` skeleton in `internal/publication`
— **zero funnels migrated, zero runtime change, zero productive importers**; the
coordinator executes no publication sequence. See §14 "PC-1 skeleton".
**Baseline:** PC-0 rebased onto `main` at `7b9102af9` (contains #209/#210/#212/#213);
PC-1 was rebased onto `main` at `e1e0092fe` (contains #211, H1 #214,
and #215 rollback recovery).
Originally characterized against `a0eef9fb3` (contains #206/#208; did **not**
contain #209/#210); this file was re-characterized after #210, #212, and #213 landed
in `main`. #210's real 3-DC `EACH_QUORUM` fallback for Sync PutBlock cross-DC
provenance is recorded in §7, §8, §11, and §13. #213's shared repair
reachability classifier is recorded in §7–§9, §13, and §15: one `SERIAL`
HEAD read plus at most 1024 sequential `EACH_QUORUM` parent reads under a
30-second context; inconclusive evidence remains `UNKNOWN` and retains repair.
#219 keeps that per-chunk bound and may, after a clean genesis, re-observe
HEAD and walk a second 1024-node chunk in the same 30s context. SERIAL HEAD
is not re-read on later retries of the same durable anchor.
#209 (G2 PREPARED→COMMITTED handoff) touches only `internal/gc` and is
orthogonal to the publication funnels characterized here. The merged #212 (G3 canonical retirement) change is also GC-side and orthogonal to the publication funnels characterized here; no PC-0 funnel re-characterization is required.
**Scope:** PC-D1A authority foundation and PC-D1B.1 cold-path certifier, source-contract tests, documentation, and isolated 3-DC evidence. No productive publication funnel is migrated.
**PC-D1 (2026-09-12):** inherited-dependency continuity is owned by the
**certified baseline frontier**. `WorkSetScopeNewlyLive` remains the sole
incremental candidate and is complete only with a valid durable witness;
PC-D1A provides the canonical witness state, fail-closed validity, and HEAD-fenced/atomic global-SERIAL authority primitives. PR #228 implements the PC-D1B.1 read-only identity-authority gate: it verifies the captured commit and every reachable fs_object against durable claims, revalidates source projections before witness settlement, and fails closed on incomplete, conflicting, or unavailable identity evidence. M14a/b and M15a/b, together with real 3-DC divergence/outage cases, exercise that boundary. PC-D1B.3 mapping authority/M18-M19 is implemented in PR #233 (merged); lifecycle serialization and the first productive consumer remain before PC-2. Historical backfill is a greenfield non-goal.
The baseline is GC-aware per dependency: capture exact physical incarnation P,
establish non-expiring current-library liveness visible to GC, then revalidate
exact P plus current GC authority before accepting the dependency. A bounded-TTL
pin may bridge certification but cannot justify the witness; legacy deterministic
locators must be rematerialized to minted, never-reused P before certification.
**Not closed:** W2, R31, X1, G4/G5. G3 canonical retirement is implemented by #212. `GC_ENABLED=false` remains required.
**Verdict:** `PROCEED WITH COORDINATOR` — see §14.
**Audit pass 2026-09-10 (ninth — deep audit with report):** the inventory was incomplete in three ways
that this pass closes as characterization only. (1) `libraries.head_commit_id`
has **six** writers, not two: two CAS primitives, two creation-time INSERTs,
and two **unconditional UPDATE initializers** that call no HEAD helper and
were invisible to the lexical guard; the unconditional shape was reproduced
reverting an LWT-published HEAD from a blind DC on the real 3-DC fixture
(§3.4, §7, §15). (2) `RevertFile` / `RevertDirectory` / `RestoreTrashItem` /
`RevertDirents` were misclassified as tree-only; they publish a positive
borrowed block-dependency delta with no `pub:`, repair, or fence (§3.5).
(3) GC Phase 5's expired-version cascade deletes fs_objects shared with the
live HEAD, so "ordinary GC reachability" is **not** an available answer to
`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01` (§6, §10, §15). §11 (PutCommit
timing) and §12 (per-publish tree-stats walk) were corrected. None of these
is fixed here; all are pre-existing.

This file answers:

> What distributed protocol do today's writers already execute when they turn
> prepared blocks into a safe HEAD publication, and what is the correct boundary
> of a future multi-DC coordinator?

PC-0 itself did **not** introduce the coordinator (PC-1 later added the
skeleton/types only, §14), migrate funnels, change Cassandra
schema, change production consistency, or close W2/R31.

Related live documents:

- [R3-LIVENESS-CONTINUITY.md](./R3-LIVENESS-CONTINUITY.md) — per-provenance
  liveness inventory. This file does not replace it.
- [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md) — live status.
- [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) — findings of record.
- [GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md) — R31/X1.

---

## 1. Scope

### In scope

Characterize every productive HEAD publisher that can stage new block
liveness (`pub:` / publish-attempt refs) and CAS library HEAD; inventory
**every** writer of `libraries.head_commit_id`, including raw-CQL writers that
call no HEAD helper; reconstruct the
real `up → pub → HEAD → fs` protocol; classify consistency/visibility;
record crash/ambiguity/cleanup semantics; separate universal steps from
funnel-specific preparation; investigate whether Sync already has a durable
PutBlock→HEAD identity; pin selected inventory/consistency tokens with source
contracts.

### Out of scope

Implementing `PublicationCoordinator`; migrating funnels; production behavior
changes; schema/CL/TTL/GC/G2–G5; closing W2/R31/X1; Seafile client protocol
changes; new receipts/tokens; absorbing discovered bugs into this PR.

Findings are recorded, not fixed.

### Runtime change

**Observed production diff for this PR: documentation + tests/test hooks only.**
No CQL, schema, config, or productive branch.

---

## 2. Terminology

These names are **control-flow phases**, not Cassandra tables, unless a later
row says a phase is durable.

| Name | Meaning |
|---|---|
| **Funnel** | An endpoint/path that can publish new block liveness through HEAD. |
| **Adapter / evidence provider** | Funnel-specific preparation: bytes, canonical IDs, provenance, exact P, and the work that turns a classified block into a *publishable* input. Classification is not authorization. |
| **Own liveness** | A writer-owned `up:` referrer this process (or an equivalent retry) created. |
| **Exact P** | The currently observed canonical physical placement `(storage_class, storage_key)`. |
| **Expected P** | The placement an adapter captured and carries forward on `PublishableInput` (e.g. F3's `commitBlockPlacement`). In the current F3 path it is captured during verification for every ready `SessionUpload` or `BorrowedFS` block, before `ensureCommitBlockOwnLiveness`; it is a value to be checked later, not proof placement still holds, and capturing it is **not** the final exact-P revalidation. |
| **Publication authority / continuity** | Every physical dependency that a HEAD will newly live on must arrive at that HEAD with continuous valid liveness for its provenance. Depending on provenance, that may be own pin + exact-P, continuous renewal/overlap, or both. Exact-P revalidation is one mechanism, not the universal recipe. |
| **Classified input** | A block sorted as `OWNED` / `BORROWED` / `UNPROVENANCED` / `ERROR`. Classification does not make it publishable. |
| **Publishable input** | Target coordinator input: classified input that has durable writer-owned liveness for every physical dependency HEAD will newly live on. `OWNED` keeps/renews its `up:`. `BORROWED` must **acquire** durable own `up:` first (W1); exact-P revalidation of the foreign `fs:` does **not** substitute for that pin. `UNPROVENANCED` and `ERROR` are rejected and **do not** produce `PublishableInput`. When the adapter carries `ExpectedP`, that value is captured evidence, **not** the final exact-P revalidation; the final revalidation happens after stage and before HEAD, while its order relative to repair is funnel-specific (§4, §10, §14). Today's funnels do not all satisfy this target contract. |
| **`pub:` / publish attempt** | Attempt-local provisional referrer keyed by the publication attempt/commit. |
| **Durable repair** | `published_block_reference_repairs` row that can outlive the request. |
| **HEAD CAS** | Conditional `UPDATE libraries ... IF head_commit_id = ?`. |
| **APPLIED** | The CAS is known to have made `commitID` the canonical HEAD. |
| **KNOWN_LOSER** | CAS returned `applied=false` with a definite current HEAD. |
| **UNKNOWN** | The CAS may have applied; confirmation failed or was never attempted. |
| **Settlement** | Promote `pub:` → `fs:`, retain repair, or exact cleanup of a known-loser attempt. |
| **Coordinator** | Logical owner of the common protocol, runnable identically on every node. Not a leader, mutex, or home-DC process. |

Do not reuse GC names `PREPARED` / `COMMITTED` for these phases.

`OBSERVED` means the current code does this. `REQUIRED` means a future
coordinator (or W2) would need this property. Freezing an OBSERVED CL does
not make it REQUIRED.

**Scope caveat (`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`):** "newly
live on" above is deliberately the same shape as R3's
`LogicalPositiveBlockDelta` (new HEAD's reachable blocks minus old HEAD's).
R3 itself states that delta is **not** the complete work set: dependencies
inherited unchanged from the old HEAD, whose continuity was never proven or
was only `CONDITIONAL`/`UNKNOWN` when they first entered some earlier HEAD,
are not covered by "newly live on" and are not covered by `PublishableInput`
below. The current GC is **not** a defense for those inherited dependencies:
Phase 5's expired-version cascade deletes fs_objects still reachable from
HEAD (§6 PUBL-10, counterexample frozen in `internal/gc`). The decision is
closed by PC-D1: the **certified baseline frontier** owns inherited continuity.
A valid durable witness certifies a library through a specific HEAD under a
contract version only after every dependency completes the ordered
`resolve/capture exact physical incarnation P → non-expiring current-library
liveness → revalidate exact P + GC authority` handshake. A TTL pin is only a
certification bridge and cannot justify the witness. Only then may the coordinator use
`LogicalPositiveBlockDelta` incrementally. Without that witness, certification
is required before promotion. GC remains a separate negative-retention
concern; see the PC-D1 decision record and §6 (PUBL-1/PUBL-2/PUBL-10).

---

## 3. Inventory of HEAD publishers

Guards: `TestPC0AllHeadCallersAreInventoried` (lexical named HEAD calls in
the inventoried functions, not every callsite shape),
`TestPC0RawHeadColumnWritersAreInventoried` (every production string literal
under `internal/` and `cmd/` that writes `libraries.head_commit_id`, with its
write shape — the raw-CQL blind spot of the lexical guard),
`TestPC0ContentResurrectionPathsObservedWithoutPublicationSeams` (§3.5),
`TestPC0BlockPublicationFunnelsHaveMappedSeams`,
`TestPC0R3StageToHeadInventoryIsSubset` (compares against the live R3
`r3PublicationStageToHeadBoundaries` list, not a duplicate copy),
`TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls` (R3 baseline).

The lexical parser retains receiver identity for methods, so same-named
methods in one source file cannot silently overwrite one another.

### 3.1 Block-publication funnels (enter `pub:` then HEAD)

| ID | Endpoint / wrapper | Prepare | Provenance | Stage | Readiness / exact P | Repair | HEAD helper | Settlement |
|---|---|---|---|---|---|---|---|---|
| F1 | `v2.CreateFile` | Office template PUT **or** empty file | Template: `RegisterUploadedBlockTargetAndMapping` with fresh `up:<uuid>`; empty file: no blocks | `stagePendingPublishedFiles` | Office: actual materialized SHA-256/class/key via `validateCommitBlockPublicationFences`, after repair/insert commit and immediately before HEAD (partial W2-6a); empty: no placements | `queuePendingPublishedFileRepairs` | `UpdateLibraryHeadFromSnapshot` | promote / schedule repair; known-loser request-local cleanup |
| F2 | `v2.UploadFile` → `finalizeStoredUploadMetadata` → `Once` | stored upload / SeafHTTP-compatible v2 | `RegisterUploadedBlockTarget` during upload (`up:<operation>`) | `stagePendingPublishedFiles` | `validateCommitBlockPublicationFences(commitBlocks)` — UploadFile passes its materialized exact placement (W2-6, PR #237; was `nil`) | `queuePendingPublishedFileRepairs` | `UpdateLibraryHeadFromSnapshot` | promote / schedule; known-loser cleanup |
| F3 | `v2.CreateFileFromBlocks` → same `Once` | session blocks + BorrowedFS | `ensureCommitBlockOwnLiveness` (`up:<session>`) before claim | same `Once` | **yes**: `validateCommitBlockPublicationFences(commitBlocks)` after stage and repair, before HEAD | same | same | same; session claim is funnel-specific |
| F4 | `v2.processSingleItem` cross-repo copy/move | `copyFSObjectToLibraryForPublish` | source-repo `fs:` borrowed; **no destination own pin proven** | `stagePendingPublishedFiles` when `pendingCopiedFiles > 0` | **none** | `queuePendingPublishedFileRepairs` | dest `UpdateLibraryHeadFromSnapshot` | promote dest refs; source HEAD (move) is a separate tree mutation |
| F5 | `v2.publishEditedDocumentMetadata` (OnlyOffice) | callback download + `saveOnlyOfficePendingBlock` | callback `up:<operation>` | `stagePendingPublishedFiles` | Original materialized exact P, revalidated after repair immediately before HEAD (E1-4 / W2-8) | `queuePendingPublishedFileRepairs` | `UpdateLibraryHeadFromSnapshot` | promote / schedule; pending-commit-id is OO-specific |
| F6 | `SeafHTTP.commitUploadedFileOnce` | single-block upload in this request | register during upload | `stageSeafHTTPPublishAttemptReferences` | `validateSeafHTTPSinglePublicationPlacement` retains original materialized exact P through retries; after durable repair, before HEAD (E1-5a) | `queuePublishedFSObjectBlockReferenceRepairFn` | `UpdateLibraryHeadFromSnapshot` | `finalizeSeafHTTPPublishedBlockReferences` |
| F7 | `SeafHTTP.commitUploadedFileMultiBlockOnce` | multi-block upload | register during upload; exact P retained per accounted position | same | E1-5b validates every distinct original P after durable repair before each HEAD attempt; rejected digest positions rematerialize productively on same-tracker retry | same | same | same |
| F8 | `Sync.handleSyncHeadPromotion` | client PutBlock / RecvFS / PutCommit | Scope gate: LQ hit ⇒ provenanced; clean LQ miss ⇒ EQ hit provenanced / EQ miss unprovenanced / EQ error abort; unprovenanced blocks skip readiness | `stageSyncCommitBlockDelta` **before** readiness | `prepareSyncCommitBlockPublicationReadiness` captures provenanced exact P; final validation after repair queue | `queueSyncCommitBlockReferenceRepairsFn` **after** readiness | `updateLibraryHeadWithStats` | `finalizeSyncCommitBlockDeltaAndSettleRepairIntent`; shared repair never cleared on request-local loss |
| F9 | `Sync.tryAutoMergeSyncHeadPromotion` | merge commit of target onto current HEAD | same complete LQ→EQ scope gate and provenanced subset | `stageSyncCommitBlockDelta` | same | queue after readiness; auto-merge commit ID includes a fresh UUID | `updateLibraryHeadWithStats` | structurally unique attempt: cleanup of `pub:` is safe; settlement still required |

`CreateFileFromBlocks` is **not** a distinct HEAD callsite. It is an adapter
in front of F2's finalizer. Both F3 and UploadFile (F2, since #237) populate
`commitBlocks` so the shared finalizer's fence runs. CreateFile Office (F1,
#238) carries its actual materialized placement in its own publication closure
and calls the same validator immediately before HEAD. These checks do not
prove TTL-bound own-pin continuity through HEAD (W2-0 remains OPEN).

### 3.2 HEAD mutations that are not block publication

These CAS HEAD but do not stage new `pub:` block liveness. They are inventoried
so a future publisher cannot hide as "just another tree mutation":

`CreateDirectory`, `RenameDirectory`, `RenameFile`, `DeleteDirectory`,
`DeleteFile`, `BatchDeleteItems`, `copyItemWithinRepoWithRetry` (same-repo
copy), `processSameRepoMove`, and the **source-library** HEAD in a
cross-repo move (`processSingleItem`).

R3 already excludes same-repo copy/move from the publication inventory. PC-0
agrees.

`RevertFile`, `RevertDirectory`, `RestoreTrashItem`, and `RevertDirents` were
listed here until the 2026-09-10 audit. They are **not** tree-only: they make
the new HEAD depend on historical fs_objects the old HEAD did not depend on.
They are reclassified as content-resurrection publication paths in §3.5.

`TestPC0TreeMutationsDoNotCallBlockPublicationStageSeams` is the negative
classification guard: an entry currently classified as a tree mutation must
not call the known block-publication stage seams
(`stagePendingPublishedFiles`, `stageSeafHTTPPublishAttemptReferences`, or
`stageSyncCommitBlockDelta`). If a future tree path does call one, the entry
must be reclassified and remapped as a publication funnel rather than silently
inheriting the tree-only classification.

### 3.3 Indirect wrappers

| Wrapper | Delegates to |
|---|---|
| `CreateFileFromBlocks` | `finalizeStoredUploadMetadata` |
| `finalizeStoredUploadMetadata` | `finalizeStoredUploadMetadataOnce` (retry on `ErrLibraryHeadConflict`) |
| `UploadFile` | `finalizeStoredUploadMetadata(..., []commitBlockPlacement{materialized placement})` (W2-6; was `nil`) |
| `commitUploadedFile` / `commitUploadedFileMultiBlock` | corresponding `Once` |

The two CAS primitives are `FSHelper.UpdateLibraryHead` (LWT;
`UpdateLibraryHeadFromSnapshot` is the v2/SeafHTTP/OnlyOffice entry) and
Sync's `updateLibraryHeadWithStats`. They are **not** the only writers of
`libraries.head_commit_id` — see §3.4.

### 3.4 HEAD initializers (resolved 2026-09-11: conditional, inside the CAS domain)

**Status:** the unconditional shape described below was **retired on
2026-09-11** (branch `fix/h1-conditional-head-initializer`,
`ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` resolved). Both initializers now
publish through `FSHelper.InitializeLibraryHeadIfUnset`
(`IF head_commit_id = null AND created_at != null`), so
`TestPC0RawHeadColumnWritersAreInventoried` now lists **five** writers (two
CAS advances, one CAS initializer, two creation-time INSERTs) and
`TestPC0NoUnconditionalHeadUpdateRemains` fails on any reintroduction.
Handler-level 3-DC evidence: `scripts/h1-initial-head-multidc-validation.sh`.
The characterization of the pre-fix state is kept below as the record of
what the audit found and why it was a coordinator prerequisite.

At audit time (2026-09-10) `TestPC0RawHeadColumnWritersAreInventoried`
scanned every production string literal that writes
`libraries.head_commit_id`. There were six, in three shapes:

| Writer | Shape | Trigger | Row exists before? |
|---|---|---|---|
| `FSHelper.UpdateLibraryHead` | CAS `IF head_commit_id = ?` | every v2/SeafHTTP/OO publish | yes |
| `SyncHandler.updateLibraryHeadWithStats` | CAS `IF head_commit_id = ?` | Sync direct HEAD / auto-merge | yes |
| `CreateLibrary` (`libraries.go`) | `INSERT` at creation, fresh UUID partition | user library creation | no |
| `AdminCreateLibrary` (`admin_libraries.go`) | `INSERT` at creation, fresh UUID partition | admin library creation | no |
| `FSHelper.InitializeLibraryFS` | **was:** `UPDATE … SET head_commit_id = ?` in a `LoggedBatch`, no `IF`; **now:** `InitializeLibraryHeadIfUnset` (CAS) | group / org-admin / admin-extra library creation, a separate batch **after** the `libraries` INSERT | yes |
| `SyncHandler.createInitialCommit` | **was:** `UPDATE … SET head_commit_id = ?` in a `LoggedBatch`, no `IF`; **now:** `InitializeLibraryHeadIfUnset` (CAS) | **`GET /seafhttp/repo/:id/commit/HEAD`** whenever a session-consistency read returns `""` | yes |

The two creation-time INSERTs write a partition nobody else can address yet;
they are inventoried, not flagged. The two `UPDATE` initializers were the
finding: they mutated an **existing** row after a `LOCAL_QUORUM` read, with no
LWT, and a `GET` could trigger one of them. `TECHNICAL-DEBT.md` §19.e recorded
the premise "the library has no concurrent writers at first-touch"; that
premise is false in multi-DC:

```text
dc-na:  library row exists, head=''      (INSERT replicated everywhere)
dc-na:  HEAD := C1  via CAS IF head_commit_id=''   (SERIAL, applied)
dc-eu:  replica still holds head=''      (lag, partition, hints pending)
dc-eu:  GET /commit/HEAD → LOCAL_QUORUM read '' → createInitialCommit
        → LoggedBatch UPDATE head=C0 (no IF)
result: every DC converges to C0 by timestamp; C1 (with files) is no longer HEAD
```

Reproduced 2026-09-10 on the real 3-DC fixture with the exact CQL shapes
(`scripts/pc0-initial-head-xdc-probe.sh`; hints off, dc-eu stopped during the
CAS, restarted blind): `dc-na SERIAL head='C0-initial-empty'` in every DC. The
control leg — the same initialization expressed as `IF head_commit_id = ''`
from the blind DC — is rejected and reports the real HEAD. This is
`ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` (its multi-DC reversion variant is
newly recorded); it was pre-existing, not introduced by PC-0, and was fixed
in the separate follow-up as a coordinator prerequisite: a coordinator that
classifies `APPLIED` on the CAS domain cannot be correct while a non-CAS
writer can move HEAD backwards. The fix is the conditional initializer
(`IF head_commit_id = null AND created_at != null`; the `created_at` guard is
load-bearing because `IF head_commit_id = null` alone upserts a phantom row
on a missing partition). The outcome is tri-state (APPLIED /
ALREADY_INITIALIZED = demonstrated KNOWN_LOSER / UNKNOWN); UNKNOWN never
discards; a demonstrated KNOWN_LOSER discards its attempt-unique commit row,
best effort, and so does a definitive rejection (missing/invalid row) whose
CAS demonstrably never published it. `GET /commit/HEAD`
still initializes an uninitialized library, but only through that path, and
returns the HEAD the Paxos round settled on once that HEAD's own `commits`
row is confirmed servable locally (else `503 Retry-After`) — the blind
datacenter is guaranteed a HEAD whose commit row will not 404, not that the
tree behind it (root/subtree `fs_objects`) is already locally present; see
`ISSUE-LIBRARY-HEAD-ADOPTED-TREE-VISIBILITY-01` for that separate,
pre-existing cross-DC metadata-visibility gap, which this initializer does
not close. The guard freezes the five remaining writers and their shapes and
pins both CAS clauses;
`scripts/pc0-initial-head-xdc-probe.sh --expect-cas-fix` validates the CQL
shape and `scripts/h1-initial-head-multidc-validation.sh` validates the
production code on the real 3-DC fixture.

### 3.5 Content-resurrection publication paths (R1–R4)

| ID | Endpoint | What it publishes | Provenance | `pub:` | Repair | Fence |
|---|---|---|---|---|---|---|
| R1 | `v2.RevertFile` | file fs_object taken from a historical commit | BORROWED (historical `fs:`) | none | none | none |
| R2 | `v2.RevertDirectory` | directory subtree from a historical commit | BORROWED | none | none | none |
| R3 | `v2.RestoreTrashItem` | deleted entry taken from the commit that still had it | BORROWED (trash) | none | none | none |
| R4 | `v2.RevertDirents` | batch of historical entries | BORROWED | none | none | E1-10e files: settled historical fs: admission + original exact-P before each HEAD; directories unchanged |

Each reads an old commit, lifts an `oldEntry`/fs_object out of it, inserts
it into the current tree, creates a new commit, and CASes HEAD. No bytes move,
but the new HEAD **newly lives on** every block under that fs_object — a
positive `LogicalPositiveBlockDelta` by PC-0's own definition (§2). When previously settled, their
existing liveness is the historical `fs:<library>:<fs_id>` reference,
which GC retention (trash / version TTL) is entitled to remove concurrently.
Today these paths are weaker than cross-repo (F4), which at least stages
`pub:` and queues repair. They are inventoried as
`pc0HeadContentResurrection`; `TestPC0ContentResurrectionPathsObservedWithoutPublicationSeams`
checks direct seams only, without tracing admission helpers, and requires reclassification as a
block-publication funnel when one of them is migrated. Recorded as
`ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` (§15); the target coordinator
must treat them as `BORROWED` adapters that acquire a durable own pin on the
resurrected fs_object's blocks (and `ExpectedP`) before staging. E1-10e adds only file source admission and indirect exact-P validation to R4;
full continuous own-liveness migration remains OPEN.

---

## 4. Reconstructed current protocol

Observed productive control flow, not a new state machine table. There is no
universal `BLOCKS_CLASSIFIED → PUBLISHABLE → PUB_STAGED` sequence in today's
code: CFFB captures placement and acquires own liveness before staging, while
Sync stages before its provenance/readiness checks. The stable common kernel
starts only once a funnel has chosen to stage:

```text
FUNNEL-SPECIFIC PREPARE / CLASSIFY
        ↓
PUB_STAGED               // attempt-local pub: — durable row, TTL-bound
        ↓
        ┌───────────────────────────────┐
        │ durable repair intent          │
        │ (for every block dependency)   │  required before HEAD;
        │ funnel-specific readiness      │  readiness is optional and
        │ / final exact-P fence           │  also before HEAD when present.
        └───────────────────────────────┘
        ↓
HEAD_ATTEMPTED          // LWT on libraries.head_commit_id
        ├── APPLIED
        ├── KNOWN_LOSER
        └── UNKNOWN
                ↓
          SETTLEMENT
                ↓
       FS_DURABLE / CLEANED / RETAINED
```

For every current block-bearing funnel **F1–F9**, the durable repair intent
precedes HEAD: `stage pub < durable repair < HEAD`. An empty-file path with
zero physical dependencies may legitimately produce no repair row; that
degenerate case does not make repair optional when dependencies exist. The
kernel below does **not** describe the content-resurrection paths R1–R4
(§3.5: positive delta, no stage, no repair, no fence) nor the HEAD
initializers (§3.4: no CAS at all); both are recorded gaps, not exceptions
the kernel tolerates. Readiness, when a funnel has
it, also precedes HEAD, but its order relative to repair remains funnel-specific.
These facts are frozen by the funnel seam contract and the order tests below;
the coordinator diagram in §14 is a target boundary, not a description of every
current funnel.

### Which phases are durable vs control-flow

| Phase | Durable today? | Notes |
|---|---|---|
| Funnel prepare (bytes in object store, session, OO download) | funnel-specific | Not a common table. |
| Own `up:` | yes, `block_references` + TTL | Identity is funnel-specific. |
| Exact P observation | attempt-local memory (`commitBlockPlacement` / `syncCommitBlockPlacement`) | Not persisted as a publication intent. |
| `pub:` stage | yes, TTL-bound provisional refs | Attempt-local referrer. |
| Repair row | yes, ordinary write | Shared by Sync direct-HEAD `commit_id`; unique per v2/SeafHTTP/OO attempt commit. |
| HEAD | yes, `libraries` LWT | Paxos/serial domain. |
| Classification APPLIED/LOSER/UNKNOWN | **not** a persisted enum | Inferred from CAS result + optional SERIAL confirm (v2) or `errSyncHeadCASUncertain` (Sync). |
| `fs:` promotion | yes | Settlement. |
| Known-loser witness | **no** durable loser row | `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01`. |

A new Cassandra table is **not** implied by this state machine. Repair + `pub:`
+ HEAD already carry crash recovery for APPLIED/UNKNOWN. Known-loser is the
gap.

### Order is not identical across funnels

There is **no** universal `stage → readiness → repair → HEAD` sequence.
`TestPC0ObservedRepairReadinessPartialOrder` plus
`TestPC0BlockBearingFunnelsKeepDurableRepairBeforeHEAD` freeze the observed
orders below. Unifying them is a later PR with explicit evidence, not a PC-1
default.

```text
CreateFileFromBlocks / shared Once (when commitBlocks is populated):
  own up:<session>  →  claim session  →  stage pub  →  queue repair  →  exact-P  →  HEAD

Sync direct HEAD / auto-merge:
  stage pub  →  readiness (renew up + capture exact-P, provenanced only)  →  queue repair  →  captured exact-P  →  HEAD

CreateFile Office / stored UploadFile (W2-6a / #237):
  stage pub → queue repair → insert commit → exact-P → HEAD
  (exact-P checks authority; own pub: has a 35d TTL, not proven continuity through HEAD)

OnlyOffice / SeafHTTP / cross-repo:
  stage pub  →  queue repair  →  HEAD
  (no pre-HEAD readiness/exact-P; own liveness is whatever the prepare step left)
```

The common kernel is the following **partial order** for block-bearing
publications:

```text
                 ┌→ durable repair intent ──┐
stage pub ────────┤                           ├→ HEAD
                 └→ readiness/final exact-P ┘
                    (when present; also before HEAD; relative order is funnel-specific)
```

An empty-file path with no physical dependencies may skip the repair row. Do
not freeze readiness-before-repair as the coordinator spine: CFFB requires
repair-before-fence, while Sync requires initial readiness-before-repair and a final captured exact-P check after repair. CreateFile Office
and stored UploadFile, OnlyOffice E1-4 and SeafHTTP single-shot E1-5a and streaming E1-5b validate exact-P after repair;
cross-repo uses stage → repair → HEAD. Any future unification
must preserve the observed orders unless a separate PR provides evidence.

---

## 5. Per-funnel matrix

W2 status uses R3's exact classification vocabulary
(`docs/R3-LIVENESS-CONTINUITY.md`): `PROVEN_CONTINUOUS` / `CONDITIONAL` /
`UNGUARDED` / `UNKNOWN`. Every row in the per-funnel matrix below currently
lands on `CONDITIONAL` or `UNKNOWN`; none is `PROVEN_CONTINUOUS`, and none
is `UNGUARDED` (no funnel has a proven concrete zero-reference interleaving).
PC-0 does not shorten this to a three-value scheme and does not rename
`PROVEN_CONTINUOUS` to `PROVEN`.

### F1 — v2 CreateFile

| Dimension | Observed |
|---|---|
| Content origin | Server-side Office template bytes, or empty file |
| Operation identity | Fresh UUID `uploadOperationID` per HEAD retry |
| Block identity | SHA-256 at template hash time; SHA-1 stored as external id |
| Provenance | `RegisterUploadedBlockTargetAndMapping` after PUT/reuse |
| Own liveness | `up:<uuid>`; empty files have none |
| TTL | 48h provisional |
| Dedup | `ProbeBlockReuse` on template |
| Exact P | W2-6a: actual materialized SHA-256/class/key re-validated immediately before HEAD, after durable `pub:` |
| Fence | W2-6a: existing `validateCommitBlockPublicationFences`; condemned/changed/retired P returns retryable 409 with cleanup before HEAD |
| `pub:` | `stagePendingPublishedFiles` / `AddPublishAttemptReferences` |
| Durable repair | yes, before insertCommit/HEAD |
| HEAD | `UpdateLibraryHeadFromSnapshot` |
| Success | CAS `applied=true`; derived-state failure is logged, not rolled back |
| Known loser | `ErrLibraryHeadConflict` → `CleanupFailedPublishAttempt` + clear repair |
| Unknown | `ErrLibraryHeadPublicationUnknown` does **not** take the conflict cleanup branch |
| Cleanup | request-local on known loser; UNKNOWN retains |
| Crash | repair worker; no loser witness |
| Multi-process | repair row is Cassandra; another pod can settle APPLIED |
| Multi-DC | HEAD is global LWT (shipped `SERIAL`); `pub:`/repair ordinary writes |
| CL | session `LOCAL_QUORUM`; HEAD LWT uses session `serial_consistency` |
| Paxos | HEAD CAS only (plus any materialize install LWT, funnel-specific) |
| Cost | O(1) file; stage 0 or 1 block; W2-6a adds two LOCAL_QUORUM point reads per Office-template HEAD attempt (zero for empty CreateFile) |
| W2 | W2-6a `OPEN`: exact-P GC-before-validation cases fixed (nine Office legs + empty control); 35d pub: expiry leaves validator-to-HEAD continuity unresolved; R31 remains / n/a empty |
| Common | stage, repair, HEAD, classify, settle |
| Specific | template materialize, empty-file, UUID operation, retry wrapper |

### F2 — stored v2 upload (`UploadFile` / `finalizeStoredUploadMetadataOnce`)

Since PR #237, UploadFile passes the actual materialized SHA-256, storage class
and storage key to the same finalizer as F3. It stages `pub:`, queues repair,
inserts the commit and calls `validateCommitBlockPublicationFences` immediately
before HEAD. The old nil-placement characterization was pre-existing doc drift.
This authority check does not prove continuous pin liveness through HEAD:
`pub:` is TTL-bound (35d), as tracked under W2-0.

| Dimension | Observed |
|---|---|
| Content origin | Client bytes via v2 upload-link / `UploadFile` |
| Operation identity | Upload operation id from materialize |
| Block identity | SHA-256 at PUT; fs_object may hold SHA-1 |
| Provenance | `RegisterUploadedBlockTarget` `up:<operation>` |
| Own liveness | 48h `up:`; not renewed at finalize |
| Exact P / fence | actual materialized SHA-256/class/key validated after pub:/repair, immediately before HEAD (#237) |
| Repair / HEAD / settle | same as F3's finalizer |
| W2 | `CONDITIONAL` (R3): final authority check present; own-pin continuity under 35d pub: expiry remains unresolved (W2-0) |
| Common | stage, repair, HEAD, settle |
| Specific | upload materialize; actual confirmed placement passed to finalizer |

Finding: `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` (publication-readiness/authority gap
by provenance; not a prescription that every funnel must run exact-P).

### F3 — CreateFileFromBlocks

| Dimension | Observed |
|---|---|
| Content origin | Previously uploaded session blocks and/or foreign `fs:` |
| Operation identity | `session_id` (durable) + derived commit id |
| Block identity | Canonical SHA-256 before claim; SHA-1 for fs_object |
| Provenance | `SessionUpload` vs `BorrowedFS` vs `None` |
| Own liveness | `ensureCommitBlockOwnLiveness` upgrades/renews `up:<session>` after placement capture |
| Exact P | `commitBlockPlacement` captured at verify for every ready `SessionUpload` and `BorrowedFS` block; fenced immediately before HEAD |
| Fence | `ValidateBorrowedFSPublicationAuthority` @ `BlockAuthorityAdvisory` (LQ) |
| Repair | after stage, before fence/HEAD |
| Crash | session claim + repair row; session result recorded after success |
| Multi-process | session LWT claim is the adapter lock; repair is shared Cassandra |
| Multi-DC | pin is durable; session claim is a Cassandra serial-domain LWT; fence is LQ (safe because own pin is already written) |
| Cost | Pre-HEAD: O(distinct blocks) readiness + one session-claim LWT + one HEAD LWT; a successful request may add one slot-release LWT; O(files) repair |
| W2 | `CONDITIONAL` through pre-HEAD (W1/W2 slices); R31 open |
| Common | proven blocks, stage, repair, exact-P, HEAD, settle |
| Specific | session claim, `/blocks/check`, BorrowedFS classify, digest idempotency |

This is the **clearest** current embodiment of the candidate coordinator
kernel, but it is not a universal current funnel. `commitBlockPlacement` is
the `ExpectedP` carrier for both `SessionUpload` and `BorrowedFS`; the observed
order is verify/capture placement → acquire or renew own liveness → stage →
repair → final exact-P fence → HEAD. Its repair-then-fence order is not frozen
as the common spine.

### F4 — cross-repo copy/move

| Dimension | Observed |
|---|---|
| Content origin | Source library fs_objects/blocks, not a new PUT |
| Operation identity | dest commit id |
| Provenance | source `fs:`; destination does not take `up:<dest-session>` |
| Own liveness | **not proven** (R3 `UNKNOWN`) |
| Exact P | none |
| `pub:` | dest attempt refs for copied files |
| Repair | yes when copied files exist |
| HEAD | dest first; move then mutates source HEAD (tree-only) |
| Multi-DC | dest publication is a normal HEAD; borrowed source liveness is not fenced |
| W2 | `UNKNOWN` |
| Common | stage/repair/HEAD/settle **if** copied files exist |
| Specific | copy fs_objects across libraries, conflict policy, source deletion |

Cross-repo is the weakest adapter. It still uses the same stage→repair→HEAD
spine once files are copied.

### F5 — OnlyOffice

| Dimension | Observed |
|---|---|
| Content origin | OO callback download |
| Operation identity | `pendingOperationID` + commit id |
| Provenance | callback `up:` |
| Exact P | Original materialized SHA-256/class/key, validated after durable repair immediately before HEAD (E1-4) |
| Extra durable step | `updateOnlyOfficePendingBlockCommitID` before HEAD |
| W2 | `CONDITIONAL` (R3) |
| Common | stage, repair, HEAD, settle |
| Specific | OO download, pending-block commit id, storage-delta vs original size |

### E1-5a current SeafHTTP single-shot disposition (2026-10-04)

F6 retains original materialized exact P through metadata retries and validates
it after durable repair, immediately before HEAD. Productive COMMITTED/TERMINAL
baseline failures, fixed 409 rejection/owned cleanup, terminal P2 replay and
real HEAD-conflict retry are recorded in [E1-5a](./E1-5A-SEAFHTTP-SINGLE-PUBLICATION-SAFETY.md).
At the E1-5a snapshot F7 streaming remained without final exact-P; E1-5b below supersedes that streaming disposition. This closes only measured
single-shot pre-HEAD safety, not W2-7, R31, E1 or X1 as a whole. Older dated source
snapshots below must be read with this disposition and E1-4 OnlyOffice's pre-HEAD
fix; production GC stays OFF.

### F6/F7 — SeafHTTP single and multiblock

| Dimension | Observed |
|---|---|
| Content origin | Seafile HTTP upload |
| Operation identity | derived commit id (`sha1(repo:root:desc:unixnano)`) |
| Provenance | register during upload, not renewed at commit |
| Exact P | F6 original materialized SHA-256/class/key validated after repair and before each HEAD attempt (E1-5a); F7 every retained original P after durable repair, on every HEAD retry (E1-5b) |
| Stage | `stageSeafHTTPPublishAttemptReferences` |
| Extra | direct `INSERT INTO commits` in the stage→HEAD window (frozen R3 CQL=1) |
| Repair | yes |
| W2 | F6/F7 measured pre-HEAD `CLOSED-FIX`; post-HEAD/R31 OPEN |
| Common | stage, repair, HEAD, settle |
| Specific | Seafile fs_object layout, context cancel checks, commit INSERT |

### F8/F9 — Sync direct HEAD and auto-merge

| Dimension | Observed |
|---|---|
| Content origin | Desktop client PutBlock / RecvFS / existing blocks |
| Operation identity | **target commit id** (client PutCommit); auto-merge adds UUID attempt |
| Block identity | positional canonical map at stage |
| Provenance | `BlockReferenceExistsLocalQuorum(up:sync:<repo>:<block>)`; a clean local miss escalates to `BlockReferenceExistsEachQuorum` before absence is accepted |
| Own liveness | renewed/acquired only for the provenanced subset; a clean global miss never fabricates liveness |
| TTL | 48h; expiry ⇒ treated as unprovenanced (`ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`) |
| Dedup | CheckBlocks can skip PutBlock entirely ⇒ no `up:` |
| Exact P | only provenanced subset; readiness captures placement; after repair queue, final validation checks that same placement without re-scoping expired provenance |
| `pub:` | staged **before** readiness |
| Durable repair | after readiness; shared by `commit_id` for direct HEAD |
| HEAD | `updateLibraryHeadWithStats` (not `UpdateLibraryHead`) |
| Success | `applied=true` then counters + derived state |
| Known loser | `syncHeadConflictError`; same-target treated as idempotent success; divergent retains shared repair, cleans this attempt `pub:` |
| Unknown | **any** CAS error → `errSyncHeadCASUncertain`; **no SERIAL confirm** |
| Cleanup | attempt `pub:` only; shared repair retained until positive settlement |
| Crash | repair + `pub:`; crash after known loser before cleanup ⇒ UNKNOWN retain |
| Multi-process | yes for settlement; PutBlock identity is the deterministic `up:` key |
| Multi-DC | LQ miss escalates to an `EACH_QUORUM` fallback before being treated as absence (#210, resolved 2026-09-08); a **global** miss still ⇒ skip readiness for that block (unprovenanced path, not a rejection) |
| CL | scope gate: LQ for every candidate, EQ only after a clean LQ miss; remaining readiness for provenanced blocks; fence LQ; HEAD LWT; repair cold path SERIAL + parent EACH_QUORUM |
| Paxos | HEAD CAS; not per-block |
| Cost | scope gate: O(all distinct candidate blocks) LQ plus one EQ for each clean local miss; remaining readiness is O(provenanced blocks); O(added files) repair; O(1) HEAD |
| W2 | `CONDITIONAL` for PutBlock-visible subset; `UNKNOWN` without PutBlock |
| Common | stage, readiness/capture, repair, captured exact-P, HEAD, classify, settle |
| Specific | commit parent/ancestry, auto-merge, RecvFS, CheckBlocks, stats/counters |

### I1/I2 — HEAD initializers (`InitializeLibraryFS`, `createInitialCommit`)

| Dimension | Observed |
|---|---|
| Content origin | empty root fs_object + initial commit row |
| Operation identity | commit id derived from `(repo, root, time)`; same-second Sync requests derive the same id |
| Provenance / own liveness | none needed (no blocks) |
| HEAD | **unconditional** `LoggedBatch` `UPDATE libraries SET head_commit_id` at session `LOCAL_QUORUM`; no `IF` |
| Classification | none — there is no APPLIED/LOSER/UNKNOWN; a stale read simply wins by timestamp |
| Multi-DC | a blind DC's `""` read authorizes an overwrite of a HEAD another DC published by CAS (reproduced, §3.4) |
| W2 | n/a for blocks; **HEAD monotonicity is not guaranteed** |
| Disposition | pre-existing `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01`; conditional initializer is a separate follow-up and a coordinator prerequisite |

### R1–R4 — content resurrection (`RevertFile`, `RevertDirectory`, `RestoreTrashItem`, `RevertDirents`)

| Dimension | Observed |
|---|---|
| Content origin | historical fs_object from an old commit / trash |
| Block identity | whatever the historical fs_object records |
| Provenance | BORROWED (historical `fs:` reference only) |
| Own liveness | **none** |
| Exact P / fence | none |
| `pub:` / repair | none |
| HEAD | `UpdateLibraryHeadFromSnapshot` (CAS) |
| Multi-DC | the borrowed `fs:` may be removed by GC retention in any DC while HEAD is being published |
| W2 | `UNKNOWN` (no proven continuity for the resurrected blocks) |
| Common | HEAD, classify |
| Specific | history/trash lookup, conflict policy, storage-quota delta |

Cost buckets for the scope-gate read, post-#210 (see §12 for the full table):

```text
local hit:                     LQ → remaining readiness → repair → HEAD
local error:                   LQ error → ABORT (no EQ, repair, or HEAD)
clean local miss + remote hit: LQ + EQ → remaining readiness → repair → HEAD
clean local miss + EQ error:   LQ + EQ error → ABORT (no repair or HEAD)
clean local miss + global miss: LQ + EQ miss → unprovenanced → repair → HEAD
```

Only a clean local miss starts the EQ fallback. A local error fails closed
without EQ; an EQ error fails closed after the LQ+EQ attempt. In the observed
Sync order, `pub:` may already be staged before these readiness outcomes, but
error paths do not queue repair or attempt HEAD.

---

## 6. Candidate invariants (PUBL-1 … PUBL-10)

| ID | Universal in today's code? | Notes |
|---|---|---|
| PUBL-1 Proven/publishable input | **No** | Classification exists in some adapters; Sync unprovenanced blocks and cross-repo borrowed `fs:` still enter `stage pub:`; content-resurrection paths (§3.5) publish borrowed historical `fs:` without any pin, stage, or repair. `UNPROVENANCED` and `ERROR` are not publishable. `BORROWED` is not publishable until the adapter acquires durable own liveness; observing/revalidating foreign `fs:` is the W1 TOCTOU. The coordinator may accept only `PublishableInput`. Classifying those states inside the coordinator and then staging them would centralize the W2 hole (Sync without PutBlock still has no liveness attributable to the commit). `PublishableInput` as defined is scoped to dependencies newly live on the HEAD being published, not to dependencies inherited unchanged from the old HEAD (`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`). PC-D1 resolves the responsibility boundary with a certified baseline frontier: newly-live is incremental only when its durable witness is valid; otherwise baseline certification is required. |
| PUBL-2 Publication authority / continuity | **No** | Exact-P before HEAD exists for F1 Office placements (#238), F2 materialized placements (#237), F3 placements and Sync-provenanced blocks. Empty F1 has no block dependencies. OnlyOffice (E1-4) and SeafHTTP single-shot (E1-5a) now validate their original materialized exact P after durable repair, before HEAD; SeafHTTP streaming (E1-5b) now validates all retained original P; cross-repo lacks an equivalent final exact-P check. Authority checks do not establish continuous own-pin liveness through HEAD: W2-0 and W2-6a remain OPEN under the 48h up:/35d pub: TTLs. BorrowedFS/late pin needs authority validation because the pin may arrive after GC won; cross-repo also lacks a proven destination own pin, and content-resurrection paths lack own pins; E1-10e adds exact-P only to RevertDirents files (§3.5). |
| PUBL-3 No liveness gap | **Unproven (W2-0 and R31)** | Ordering aims at overlap; 48h up: and 35d pub: TTLs leave pre-HEAD validator-to-HEAD continuity unresolved as well as post-HEAD continuity. |
| PUBL-4 Durable ambiguity | **Mostly** | UNKNOWN does not take known-loser cleanup. Repair row is the durable witness. Finite `pub:` TTL remains R31 (`ISSUE-GC-PUB-REF-ZERO-REF-01`). |
| PUBL-5 Known loser ≠ unknown | **Yes in request-local paths; no durable loser** | Classified differently; crash before cleanup collapses to UNKNOWN retain. |
| PUBL-6 Multi-DC absence | **Fixed for the scope-gate decision (#210, resolved)** | A `LOCAL_QUORUM` miss no longer settles the answer: `syncBlockHasOwnLivenessProvenanceFn` escalates to `BlockReferenceExistsEachQuorum` first, real 3-DC evidence attached. Only a **global** miss is treated as "no currently observable provenance" — still fail-open into the unprovenanced path (a separate, already-tracked W2 gap, not what #210 closed). Repair reachability fail-closes (retain). Destructive GC uses EACH_QUORUM (X2 closed) — different domain. |
| PUBL-7 Fail closed | **Yes for unavailable/error observations; by design open on a clean global miss** | Readiness failures abort before HEAD. A local scope-gate **error** fails closed without EQ. After a clean local miss, an `EACH_QUORUM` **error** also fails closed; an unavailable observation must not become absence. A clean global **miss** (successful read, no row) is treated as "no currently observable provenance" and takes the unprovenanced path. The #210 trade-off is specifically the clean **local** miss → EQ fallback needed to distinguish remote visibility from absence; #210 does not close or guarantee the global-miss W2 gap. Error and miss are distinct outcomes; do not collapse them. |
| PUBL-8 Restart independence | **Partial** | APPLIED/UNKNOWN can be settled from another process via repair. Original process is not required. Known-loser cleanup is request-local. |
| PUBL-9 HEAD advances only through the CAS domain | **Yes (since 2026-09-11)** | At audit time two unconditional `UPDATE` initializers (§3.4) moved HEAD outside the LWT domain after a session-consistency read, one reachable from a `GET`; reproduced reverting a CAS-published HEAD from a blind DC. Resolved by `InitializeLibraryHeadIfUnset`; pinned by `TestPC0NoUnconditionalHeadUpdateRemains`. |
| PUBL-10 Inherited dependencies are protected by GC reachability | **No universal guarantee** | Historical Phase 5 counterexample fixed by [metadata-only unguarded commit retirement](./GC-PHASE5-SHARED-FSOBJECTS.md): shared trees/refs survive. The preservation regression replaces the unsafe characterization. Phase 6/concurrent metadata deletion and exclusive-tree reclamation remain PRE-GC; this does not certify inherited continuity or activate GC. |

None of these refutes a coordinator. They show the coordinator must **own**
PUBL-1…10 rather than copy today's omissions. PUBL-9 was fixed outside the
coordinator (2026-09-11) so that its APPLIED classification can mean
something.

---

## 7. Multi-DC visibility matrix

Shipped production: `CASSANDRA_CONSISTENCY=LOCAL_QUORUM`,
`CASSANDRA_SERIAL_CONSISTENCY=SERIAL`, NTS `dc-na/dc-eu/dc-asia`.
`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01`: closed 2026-09-14. Canonical HEAD
authority pins global `SERIAL` explicitly and does not inherit
`CASSANDRA_SERIAL_CONSISTENCY`. OBSERVED default remains SERIAL for other LWTs.
REQUIRED for a coordinator: do not silently accept `LOCAL_SERIAL` as global
publication authority; current HEAD writers already refuse that inheritance.

| Operation | Local hit sufficient? | Local miss authoritative? | Global proof required? | Failure mode |
|---|---:|---:|---:|---|
| Own-liveness evidence (Sync scope gate) | yes for **presence** (fast path) | **no** | **yes, on a clean local miss** (`BlockReferenceExistsEachQuorum`, #210, resolved 2026-09-08) | local miss escalates to `EACH_QUORUM`; a **global** miss ⇒ block left untouched, not fabricated (still not proof PutBlock never happened, see TTL expiry); global error fails closed exactly like a local error |
| CFFB/session `up:` write/renew | n/a (plain quorum upsert; an existing row renews and an expired row can be recreated) | n/a | no | upsert error aborts before HEAD |
| Exact-P validation | yes (advisory LQ) **given own pin already durable** | no (`Changed`/`Blocked`/error abort) | no | reject before HEAD; does not fabricate Authorized |
| `pub:` stage | n/a (write) | n/a | durability = session LQ write | stage failure ⇒ cleanup attempt, no HEAD |
| Repair intent | n/a (ordinary write) | n/a | must survive remote recovery | queue failure: Sync retains possible applied insert; v2 cleans attempt |
| HEAD CAS | Paxos domain (`SERIAL` default) | n/a | classify | UNKNOWN retained |
| v2 ambiguous CAS confirm | SERIAL read of HEAD | n/a | yes for APPLIED vs not | confirm error ⇒ `ErrLibraryHeadPublicationUnknown` |
| Sync CAS error | n/a | n/a | **no confirm** | always UNKNOWN (`errSyncHeadCASUncertain`) |
| Settlement promote `fs:` | session LQ writes | n/a | success is local quorum of the write | failure ⇒ schedule repair, do not unpublish HEAD |
| Repair reachability | no | **no** | each ancestry chunk ≤1024 sequential EACH_QUORUM parent reads under a 30-second context; SERIAL HEAD when creating/replacing the anchor; a clean-genesis re-anchor visit may do a second HEAD and a second 1024-node chunk | positive reachability promotes; missing/error/timeout/cycle/bound/unavailable ⇒ UNKNOWN and retain (`ISSUE-PUBLISH-REPAIR-REACHABILITY-01` closed for the shared classifier; `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` closed for moving-HEAD resume; broader R31 remains open) |
| Known-loser cleanup | request-local | n/a | must not run on UNKNOWN | crash ⇒ retain as UNKNOWN |
| HEAD initialization (`GET /commit/HEAD` → `createInitialCommit`; `InitializeLibraryFS`) | n/a | **no (since 2026-09-11)** — a session-CL `""` read only *proposes* initialization; the CAS decides | Paxos (HEAD serial domain) | blind DC's proposal is rejected and the real HEAD is returned (`scripts/h1-initial-head-multidc-validation.sh`); before the fix a `""` read authorized an unconditional overwrite (reproduced 2026-09-10, `scripts/pc0-initial-head-xdc-probe.sh` bug mode) |
| Content resurrection (R1–R4) | R4 files require settled historical fs: (E1-10e); other paths unchanged | R4 original exact P before each HEAD | no own pin | concurrent retention/cleanup remains OPEN; single-DC admission evidence is not multi-DC continuity |

**Never:** `LOCAL_QUORUM miss` ⇒ globally absent. At audit time exactly one
productive path violated this — HEAD initialization (§3.4), which treated a
local `""` read as "no HEAD exists" and overwrote; resolved 2026-09-11. Since #210, the Sync scope
gate no longer treats a local miss as absence on its own — it escalates to
`EACH_QUORUM` first, matching this rule's spirit. It still uses a **global**
miss to skip W2 readiness for that block, not to authorize GC; that remains a
different, narrower gap than X2 (which is closed) and is not what #210
closed.

---

## 8. Consistency map (OBSERVED vs REQUIRED)

`TestPC0CriticalConsistencyPrimitivesArePinned` pins **selected source tokens**
at named primitives. It does **not** freeze this whole table. Canonical
`libraries` HEAD `serial_consistency` **is** pinned to global `SERIAL`
(`ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01`, closed 2026-09-14) via
`TestPC0HeadSerialDomainPinsGlobalSerial` on the MapScanCAS chain; other LWTs
may still inherit the session default.

| Primitive | OBSERVED CL | REQUIRED for coordinator? |
|---|---|---|
| `BlockReferenceExistsLocalQuorum` | `LOCAL_QUORUM` | OBSERVED fast path; same-DC, zero added WAN. A hit or a read error both settle the answer without escalating. |
| `BlockReferenceExistsEachQuorum` | `EACH_QUORUM` | OBSERVED cross-DC fallback (#210, resolved), reached only on a clean `BlockReferenceExistsLocalQuorum` miss; REQUIRED to stay scoped to that one-shot pre-HEAD gate escalation and not become a per-block default. |
| `ValidateBorrowedFSPublicationAuthority` | `BlockAuthorityAdvisory` = LQ | REQUIRED: must stay off SERIAL on the publish hot path. |
| `ProbeBlockReuse` | session (prod LQ) | OBSERVED |
| `AddProvisionalBlockReferenceWithExpiry` | pinned producer (quorum write; see existing producer test) | OBSERVED renew; not Paxos |
| `AddPublishAttemptReferences` / stage | session LQ | OBSERVED |
| Repair INSERT/DELETE | ordinary session write, **no LWT** | OBSERVED; REQUIRED not to become per-block Paxos |
| `UpdateLibraryHead` CAS | `IF head_commit_id` + explicit `SerialConsistency(db.LibraryHeadSerialConsistency)` = global `SERIAL` | REQUIRED: global serial domain for HEAD (do not design around `LOCAL_SERIAL`) |
| `confirmLibraryHeadCommitVisible` | `Consistency(SERIAL)` | OBSERVED v2 classify; candidate common classify step |
| Sync `updateLibraryHeadWithStats` CAS | same LWT + explicit `SerialConsistency(db.LibraryHeadSerialConsistency)` = global `SERIAL`, **no confirm** | OBSERVED split classifier |
| Repair HEAD read | SERIAL | OBSERVED cold path; one read when creating or replacing the durable anchor under the shared 30-second classifier deadline; later retries of that snapshot do not re-read HEAD; a clean-genesis re-anchor visit may observe HEAD twice |
| Repair parent walk | EACH_QUORUM | OBSERVED cold path; at most 1024 sequential reads **per ancestry chunk** under the shared 30-second deadline; a visit that re-anchors after clean genesis may walk a second chunk (≤2048 parent reads); one DC unavailable yields UNKNOWN/retain |
| `BlockHasReferencesGlobal` | EACH_QUORUM | **not** on publication hot path (GC) |
| `InitializeLibraryHeadIfUnset` (used by `InitializeLibraryFS` / `createInitialCommit`) | `IF head_commit_id = null AND created_at != null` + explicit `SerialConsistency(db.LibraryHeadSerialConsistency)` = global `SERIAL` (since 2026-09-14; 2026-09-11 was session `SerialConsistency`) | OBSERVED; pinned by `TestPC0RawHeadColumnWritersAreInventoried` / `TestPC0NoUnconditionalHeadUpdateRemains` / `TestPC0HeadSerialDomainPinsGlobalSerial` |
| `CalculateLibraryStats` / `calculateDirStats` (v2 `UpdateLibraryHead`; Sync `commitTreeStats` ×2) | session reads, one per directory, recursive | OBSERVED cost inside the stage→HEAD window (§12); REQUIRED: not part of the coordinator's HEAD step as a full walk |

This PR must not add authority reads, CQL callsites, Paxos, or WAN
operations to production. R3 budgets remain the hot-path baseline.

---

## 9. Crash / ambiguity semantics

### v2 / SeafHTTP / OnlyOffice / cross-repo (`UpdateLibraryHead`)

1. LWT error that is not ambiguous → wrapped failure; callers typically do
   **not** treat it as conflict cleanup.
2. Ambiguous LWT → SERIAL confirm:
   - HEAD already `commitID` → treat **APPLIED** (`return nil`).
   - HEAD is something else → treat as failed update (not
     `ErrLibraryHeadConflict`); **no** conflict cleanup. Strictly this is
     UNKNOWN, not a demonstrated loss: the CAS may have applied and HEAD may
     already have advanced past `commitID` before the confirm read (the case
     `InitializeLibraryHeadIfUnset` models explicitly as `InitialHeadUnknown`).
     Today the result is not used as destructive authority, so it is a
     classification imprecision for PC-1 to unify, not a runtime defect
     (§15, "HEAD classify split").
   - confirm error → `ErrLibraryHeadPublicationUnknown` (UNKNOWN, no
     conflict cleanup).
3. `applied=false` → `ErrLibraryHeadConflict` (KNOWN_LOSER) → request-local
   attempt cleanup (`CleanupFailedPublishAttempt`,
   `cleanupSeafHTTPFailedPublishAttempt`, or
   `cleanupOnlyOfficeFailedPublishAttempt`) which typically also clears the
   repair row. Crash before that cleanup is
   `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01`.

### Sync (`updateLibraryHeadWithStats`)

1. Any LWT error → `errSyncHeadCASUncertain` (UNKNOWN). Retain `pub:` and
   shared repair. HTTP 503 retry.
2. `applied=false` + `currentHead==targetHead` → idempotent success (another
   writer won the same target). Clean this attempt `pub:` only. **Do not**
   clear shared repair.
3. `applied=false` + divergent HEAD → retry / 503. Clean this `pub:`.
   Retain shared repair.
4. Post-CAS counter/derived failure → HEAD **did apply**; 503 pending
   reconciliation; do not unpublish.

### HEAD initializers (`InitializeLibraryFS`, `createInitialCommit`)

Since 2026-09-11 they publish through `InitializeLibraryHeadIfUnset`, which
classifies like the advance primitives: applied ⇒ this call initialized;
not applied with a head ⇒ adopt that head (demonstrated KNOWN_LOSER: its own
losing commit row is discarded, best effort — a failed DELETE or a crash
before it can leave it dangling, see `DiscardLosingInitialCommit`);
not applied without a row ⇒ `ErrLibraryHeadNotFound`; ambiguous ⇒ SERIAL
confirm, else `ErrLibraryHeadPublicationUnknown`. At audit time no
classification existed: the write was an unconditional `LoggedBatch` and a
stale read simply won by timestamp (§3.4).

### Repair worker (shared)

Positive reachability only promotes. Everything else retains. The shared repair
classifier records one SERIAL canonical HEAD as a durable anchor on the repair
row, then walks at most 1024 parent rows sequentially with EACH_QUORUM
**per ancestry chunk** under one 30-second deadline from the persisted
cursor. SERIAL HEAD is observed when that anchor is created or replaced;
later retries of the same snapshot do not re-read HEAD. The cursor is the next
unread commit after a safely completed prefix; timeout or a later parent-read
error persist that node instead of restarting the prefix. Missing/error,
timeout, cycle, malformed ancestry, natural genesis, bound exhaustion, or an
unavailable DC stay UNKNOWN/retain. A missing repair row is a terminal no-op
(never REACHABLE, never `pub:` renewal). Timeout/lease is **not** cleanup
authority (closed `ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01`). A later live HEAD does not restart an in-flight walk. After a clean walk to
genesis, that snapshot is persisted as exhausted before the SERIAL HEAD
re-read; a newer SERIAL HEAD may then replace it and walk a second 1024-node
chunk in the same visit so a repair that ran before the target was published
can still converge
(`ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01`,
closed 2026-09-12). Unresolved repairs can write/refresh a per-row
`pub:<repo:commit:fsID>` for the staged block IDs **after** classification
while the row is still pending
(`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`). That is not a gap-free
handoff: if prior liveness expires before that write, a zero-ref interval
exists even if the later renewal recreates `pub:`. If discovery starts after
expiry, the gap already existed. The shared worker
best-effort removes that identity before deleting the repair row. Ordinary
Sync success does not walk blocks to delete it. Concurrent renewal of the same
row can still leave TTL-bounded repair-owned `pub:`
(`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`).
Progress LWTs share the 32 bucket partitions of the ordinary discovery table
(`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`). 6h is only process-local retry backoff
(`ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01`). Broader
R31 (known-loser durability, `pub:` zero-ref discovery, other funnels) remains
open.

### Mapping onto the PC-1 common `HeadOutcome` (documentation only)

PC-1 introduced `publication.HeadOutcome` (`applied` / `known-loser` /
`unknown`) as knowledge about whether the **target** is canonical. It is not
the raw CAS applied bit and grants no cleanup authority. `SettlementDecision`
separately pairs that outcome with an adapter-established disposition and
rejects UNKNOWN cleanup/promotion, KNOWN_LOSER promotion, and APPLIED cleanup
when attempt id equals the canonical target commit id. **No productive
classifier produces these types yet.** The table records the required future
mapping and where today's result surface is insufficient:

| Classifier | Today's result | `HeadOutcome` | 1:1? |
|---|---|---|---|
| v2 `UpdateLibraryHead` | `nil` | `applied` | yes |
| v2 `UpdateLibraryHead` | `ErrLibraryHeadConflict`, found HEAD == target | `applied` | **no** — the sentinel is not a structured same-target outcome today |
| v2 `UpdateLibraryHead` | `ErrLibraryHeadConflict`, divergent | `known-loser` | yes once the found HEAD is classified |
| v2 `UpdateLibraryHead` | `ErrLibraryHeadPublicationUnknown` | `unknown` | yes |
| v2 `resolveLibraryHeadUpdateError` | ambiguous CAS + confirm shows a different HEAD → plain wrapped failure, no cleanup | strictly `unknown` | **no** — today it is neither conflict nor `ErrLibraryHeadPublicationUnknown`; unifying it is the §15 "HEAD classify split" PR |
| Sync `updateLibraryHeadWithStats` | `nil`, `errSyncHeadPostCAS`, `errSyncHeadRepairPending` | `applied` (HEAD did apply) | yes |
| Sync `updateLibraryHeadWithStats` | `errSyncHeadCASUncertain` (any CAS error, no confirm) | `unknown` | yes |
| Sync `syncHeadConflictError`, current HEAD == target | idempotent success; only this attempt's `pub:` cleaned, shared repair kept | target `applied`; disposition `cleanup-attempt` from separate Sync evidence (attempt id ≠ commit id) | adapter-specific |
| Sync `syncHeadConflictError`, divergent | retry / 503, this `pub:` cleaned | `known-loser` | yes |
| `InitializeLibraryHeadIfUnset` | `InitialHeadApplied` | `applied` | yes |
| `InitializeLibraryHeadIfUnset` | `InitialHeadAlreadyInitialized`, adopted HEAD == target | target `applied`; canonical commit cleanup forbidden (`losingCommitID == winningHead`) | conditional |
| `InitializeLibraryHeadIfUnset` | `InitialHeadAlreadyInitialized`, adopted HEAD differs | target `known-loser`; exact attempt cleanup may be established separately | conditional |
| `InitializeLibraryHeadIfUnset` | `InitialHeadUnknown` | `unknown`; retain only | yes |

---

## 10. Common vs funnel-specific

### Belongs in a future coordinator (universal kernel)

```text
accept PublishableInput only
  (OWNED with own liveness, or BORROWED after acquiring durable own up:,
   capturing ExpectedP when required; observing foreign fs: without an
   own pin is not enough)
stage attempt-local pub:
durable repair intent for every block dependency
  (an empty-file path with no physical dependencies may have no repair row)
publication readiness when this funnel has it
  (also before HEAD; relative order is funnel-specific;
   the FINAL exact-P revalidation against ExpectedP, when required,
   happens here -- not before staging)
HEAD attempt + classify target APPLIED | KNOWN_LOSER | UNKNOWN
settlement from separate attempt-scoped evidence:
  APPLIED → promote, retain, or exact cleanup of a distinct same-target attempt
  KNOWN_LOSER → exact attempt cleanup or retain; never promote
  UNKNOWN → retain only
```

No arrow above is cleanup authority by itself. `cleanup-attempt` is limited to
resources proven exclusive to that attempt; it never implicitly includes the
target commit or shared repair state. Consequently APPLIED + cleanup is valid
only when `Attempt != AttemptID(TargetCommitID)`; KNOWN_LOSER + cleanup may
still use attempt == target because the target was proven non-canonical.

`UNPROVENANCED` and `ERROR` never enter this kernel; rejecting them does not
produce `PublishableInput`. Adapters must prove/renew own liveness for
`OWNED`, or acquire durable own liveness for `BORROWED` and capture
`ExpectedP` before a target coordinator stages. The current F3 sequence is
`verify/classify → capture commitBlockPlacement(ExpectedP) for every ready
SessionUpload or BorrowedFS block → own up: → stage → repair → final exact-P
revalidation → HEAD`; the adapter step captures observed placement and
acquires the pin, **not** the final exact-P check. Doing that final check
before staging would reopen the W1 TOCTOU.
Today's Sync can still publish after a clean global miss; the target
coordinator must reject that unprovenanced input. Centralizing it without
it only centralizes the hole. Cross-repo still publishes borrowed source
`fs:` without a destination own pin — that is today's gap, not a permitted
`PublishableInput` shape.

This kernel only ever stages `PublishableInput` for dependencies the HEAD
being published will *newly* live on. It does not revisit dependencies the
new HEAD inherits unchanged from the old HEAD, even when that inherited
dependency's own continuity was `CONDITIONAL`/`UNKNOWN` when it first
entered an earlier HEAD (`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`,
tracking R3's own `LogicalPositiveBlockDelta` caveat). The current GC cannot
be assumed to make that safe: Phase 5's expired-version cascade deletes
fs_objects still reachable from HEAD (§6 PUBL-10, §15) — reachability is
enforced at scan time in Phase 6 and not at all in Phase 5's commit cascade.
PC-D1 closes this boundary with one owner: the **certified baseline frontier**.
Certification observes a concrete HEAD, proves the full canonical dependency
tree under contract version V, including the per-dependency
`resolve/capture exact physical incarnation P → establish non-expiring
current-library liveness → revalidate exact P + GC authority` handshake, and
commits a durable witness only if that HEAD is still current. With a valid
witness, PC-2 may use `LogicalPositiveBlockDelta` for newly-live dependencies;
without it, the library is not eligible for the incremental path and must be
certified first. PC-1 remains behavior-preserving, with zero funnels migrated; PC-D1A adds authority-only witness state and HEAD CAS primitives. PR #228 implements the PC-D1B.1 read-only identity-authority gate and complete-tree certifier. PC-D1B.3 mapping coverage is implemented in PR #233 (merged); lifecycle serialization and productive-consumer integration remain separate PC-D1B work. GC remains a separate PRE-GC safety owner.

Two more prerequisites sit outside the kernel and must not be absorbed into
it as flags: HEAD initialization had to move into the CAS domain (§3.4; done
2026-09-11), and content resurrection (§3.5) must become an adapter rather
than stay "tree-only".

### Belongs in adapters (must not become coordinator flags)

| Adapter | Owns |
|---|---|
| Sync | PutBlock, RecvFS, PutCommit, CheckBlocks, parent/ancestry, auto-merge, stats/counters, `up:sync:<repo>:<block>` evidence |
| SeafHTTP | Seafile upload protocol, fs_object layout, commit INSERT |
| V2 stored upload | upload-link, chunk assemble, `UploadFile` |
| CreateFileFromBlocks | session, `/blocks/check`, BorrowedFS classify, session claim LWT |
| OnlyOffice | callback download, pending-block row |
| Cross-repo | copy fs_objects, conflict policy, source HEAD |
| CreateFile | template materialize / empty file |
| Content resurrection (R1–R4) | history/trash lookup, resurrected fs_object → durable own pin on its blocks + `ExpectedP` before stage (E1-10e R4 files: retained-source admission/exact-P only, no own pin) |

Rejected coordinator shape:

```text
PublicationCoordinator(isSync, isOnlyOffice, borrowed, autoMerge, ...)
```

Also rejected: one elected leader, global library lock, SERIAL per block by
default, EACH_QUORUM on every normal op, "coordinator instance in dc-na".

---

## 11. Sync PutBlock → HEAD identity

**Question:** is there a durable identity that binds a PutBlock to a future HEAD?

| Candidate | Exists? | Unique per publication? | Survives retry/pod/DC? | Written before dependence? | Distinguishes PutBlock vs dedup? |
|---|---|---|---|---|---|
| Commit ID | the `commits` row exists at `PutCommit`, which the Seafile client sends **before** fs objects and blocks; HEAD moves later via `update-branch` / `PUT /commit/HEAD` | per commit, not per PutBlock | yes if committed | the row exists before the blocks are PUT, but **no PutBlock is written to it** — `PutBlock` carries only `(repo, block)` | no |
| fs ID | at RecvFS | per file object | yes | not at PutBlock | no |
| repo ID | yes | no | yes | yes | no |
| `up:sync:<repo>:<block>` | yes | **no** — deterministic per (repo, block) | row survives; visible at `LOCAL_QUORUM` same-DC, and cross-DC via the `EACH_QUORUM` fallback on a clean local miss (#210, resolved) | yes at PutBlock | **no** — same key for real PUT and later renew; absent after TTL or CheckBlocks skip |
| Upload token | auth only | no | session | n/a | no |
| Request identity | no durable | n/a | no | n/a | no |
| Server session | none for Sync blocks | n/a | n/a | n/a | no |
| Client protocol metadata | block id + bytes | content hash | n/a | n/a | CheckBlocks is the opposite of PutBlock |

**Finding (must not be softened):**

```text
current Sync evidence fundamentally requires inference
```

The only durable PutBlock residue is the deterministic `up:` referrer. It is
not a publication attempt id. #210 changed its *visibility domain* (a clean
local miss now escalates to `EACH_QUORUM` before being treated as absence),
not its identity: the fallback still infers presence/absence from the same
`up:sync:<repo>:<block>` key, still cannot distinguish a real PutBlock from a
later renewal, and still goes absent past its 48h TTL
(`ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`) or when `CheckBlocks` skipped
PutBlock entirely. The finding above is unchanged by #210.

The 2026-09-10 audit corrected this section's
earlier wording that the commit only appears at HEAD time: `PutCommit` stores
the commit row first, so a *pending commit* exists server-side while blocks
arrive. That does **not** yet give PutBlock a durable, unambiguous binding —
retries, concurrent uploads from the same client, dedup, and auto-merge all
need a demonstrated association before "pending commit" can be used as
identity. Recorded as design material, not adopted:

- **DESIGN HYPOTHESIS (follow-up):** bind PutBlock to the pending commit(s)
  recorded by PutCommit for the same repo/token.
- **DESIGN OPTION (follow-up):** have `CheckBlocks` pin (fenced) the blocks
  it reports as existing, converting the dedup path into a provenanced one —
  O(N) writes with TTL, retry, and GC-race semantics that must be designed.

Neither changes the plan to migrate the Sync adapter last.

**Re-characterized after rebase** (this baseline now contains #210): the
cross-DC blind spot this section originally flagged is closed — a PutBlock
acknowledged in `dc-eu` is now recoverable from a request handled in `dc-na`
before hints/repair land, with real 3-DC evidence
(`scripts/w2-sync-putblock-xdc-provenance-validation.sh`,
`internal/integration/sync_w2_putblock_xdc_provenance_multidc_test.go`; see
§13 M2). The trade-off: a commit made entirely of genuinely-unprovenanced
(dedup-only) blocks now shares the same `EACH_QUORUM` availability
dependency, so one datacenter down can newly block that commit's HEAD publish
where it previously could not
(`ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01`, accepted trade-off,
decision recorded 2026-09-09). This is an availability-domain change, not an
identity change.

---

## 12. Cost / hot-path

No production cost is added by this PR.

**Correction (2026-09-10):** the table originally omitted the per-publish
tree-stats walk. Every v2/SeafHTTP/OnlyOffice/cross-repo/resurrection publish
pays `UpdateLibraryHead → CalculateLibraryStats → calculateDirStats`: one
session-consistency `fs_objects` read **per directory** of the new tree,
recursively, with no cache, *inside* the stage→HEAD window (after `pub:` and
repair are written, before the CAS). Sync pays it **twice** before its CAS
(`commitTreeStats(previous)` + `commitTreeStatsStrict(new)` in
`updateLibraryHeadWithStats`). For a library with D directories that is
O(D) sequential reads per publish (2·O(D) for Sync), lengthening the
`pub:`-only window and the LWT contention window under concurrency. Rows
marked `O(1)` below are O(1) in *mutation* work only; add the stats walk to
every row. Recorded as `ISSUE-PUBLISH-HEAD-TREE-STATS-COST-01` (P2,
FOLLOW-UP); not optimized here.

| Funnel | DB shape (+ stats walk O(D), see above) | Local | Cross-DC / WAN | Paxos |
|---|---|---|---|---|
| CreateFile (empty) | O(1) tree + O(D) stats walk + HEAD | tree reads | HEAD | 1 HEAD LWT |
| CreateFile (template) | + materialize | LQ pin/fence/install | HEAD; install LWT if new | HEAD + possible install |
| UploadFile stored | O(blocks) stage | LQ | HEAD | HEAD |
| CreateFileFromBlocks | O(blocks) renew+fence + session claim + optional successful slot release | LQ × N + quorum upserts | session-claim LWT + HEAD LWT under the configured serial domains; no added EQ on the normal path | session claim + HEAD; full successful requests also include the slot-release LWT when the cap is enabled |
| OnlyOffice | O(1) + O(D) stats walk | LQ | HEAD | HEAD |
| SeafHTTP | O(blocks) stage + 1 commit INSERT | LQ | HEAD | HEAD |
| Cross-repo | O(copied files/blocks) | LQ | dest HEAD (+ source HEAD) | dest/source HEAD |
| Sync scope gate (N distinct candidates) | O(N) LQ scope reads | LQ × N | 1 EACH_QUORUM for each clean LQ miss; LQ errors abort without EQ | none beyond HEAD |
| Sync provenanced subset (P candidates) | remaining readiness/renewal for P after scope gate | LQ/EQ scope gate + funnel-specific readiness | repair cold SERIAL/EQ; HEAD | HEAD only |
| Sync unprovenanced subset (U global misses) | stage/repair/HEAD, no Sync readiness for those blocks | LQ/EQ scope gate still paid | no per-block readiness; HEAD | HEAD |
| Sync HEAD step (any subset) | 2·O(D) stats walks before the CAS | session reads | HEAD | HEAD |
| Content resurrection R1–R4 | history/trash read + O(1) tree + O(D) stats walk + HEAD; E1-10e R4 files additionally resolve/check references/physical placements | tree reads | HEAD | HEAD |

The CreateFileFromBlocks row has two useful scopes. Before HEAD, the path
pays one session-claim LWT and one HEAD LWT, in addition to its bounded
per-block quorum upserts and advisory exact-P reads. A successful request also
calls `CleanupCommittedBlockUploadSessionCaps`; when the staging cap is
enabled, that conditional slot-release is another LWT after the idempotency
result is recorded. It is not part of the pre-HEAD protocol cost.

Only a **clean** local miss escalates to EACH_QUORUM. A local read **error**
fails closed without escalation; an EQ error fails closed after the EQ attempt
and before repair/HEAD (§7, PUBL-7). The clean-local-miss fallback is the
availability/cost trade-off #210 introduced; the clean-global-miss path is a
separate W2 gap and must not be folded into a flat "Sync readiness" line.

Local fast path that **must** be preserved: LQ presence of own `up:` and LQ
exact-P **after** a durable own pin. Do not put EACH_QUORUM or SERIAL on that
path by default.

R3 declared exception for Sync readiness remains the only accepted O(N)
authority-shaped publish cost. PC-0 does not raise that budget.

The shared repair cold path is bounded separately from the writer hot path:
each ancestry chunk is at most 1024 sequential EACH_QUORUM parent reads under
a 30-second total context. SERIAL HEAD is observed when creating or replacing
the durable anchor. After a clean genesis a visit may re-observe HEAD and walk
a second chunk (at most two SERIAL HEAD observations / 2048 parent reads).
This is the narrow shared-classifier
closure from #213 plus #219's resumable walk; it does not close broader R31
lifecycle, discovery, or other-funnel obligations.

---

## 13. 3-DC topology + characterization matrix

Reuse, do not duplicate:

- `scripts/x2-multidc-validation.sh` — GC EACH_QUORUM / topology
- `scripts/w2-post-head-multidc-validation.sh` — local blindness cannot cleanup
  a publication made in another DC
- `scripts/w2-sync-putblock-xdc-provenance-validation.sh` /
  `internal/integration/sync_w2_putblock_xdc_provenance_multidc_test.go` —
  Sync pre-HEAD scope-gate cross-DC recovery (#210, resolved 2026-09-08,
  now in this baseline); reuse for M2, do not duplicate

Gate: `SESAMEFS_REQUIRE_PC0_PUBLICATION_CHARACTERIZATION=1`.
Skip under that gate is FAIL. Missing fixture is FAIL. Required named **matrix
rows** must be recorded. Default Docker `go-all-test` does **not** inherit
this gate.

This is a **3-DC topology + characterization matrix** gate, not an evidence
runner. When armed it connects to `dc-na`/`dc-eu`/`dc-asia` and records the
matrix. It does **not** re-run `w2-post-head-multidc-validation.sh` or
`x2-multidc-validation.sh`, and it does **not** execute publication races
M1–M9. Rows marked prior evidence cite those scripts; they are not new
proofs from this PR. `UNKNOWN` / `GAP` / `PRIOR-EVIDENCE-NOT-RERUN` may
make the matrix complete. Reserve `executed evidence` / `leg executed` for
scenarios that actually ran a publication race.

Harness prints the recorded matrix rows. UNKNOWN is explicit, never silent
green.

| Row | Claim | Result on this baseline |
|---|---|---|
| M1 Local fast path | LQ presence / local tree / exact-P / stage writes stay local; funnel-specific LWTs remain serial-domain coordination | **OBSERVED** (source). Live 3-DC not required to see there is no EQ on those operations. |
| M2 Remote provenance before repair | PutBlock in dc-eu, HEAD in dc-na before hints | **PRIOR EVIDENCE** (`scripts/w2-sync-putblock-xdc-provenance-validation.sh`, #210 resolved 2026-09-08, real 3-DC RED→GREEN). Not re-executed by PC-0's own gate. A clean local miss now escalates to `EACH_QUORUM` before being treated as absence; a genuine global miss still skips W2 readiness for that block (separate, already-tracked gap, not what #210 closed). |
| M3 One DC down | EQ/SERIAL ops fail closed | **MIXED / PRIOR EVIDENCE — PARTIAL**: #213's shared repair classifier proves the cold-path SERIAL HEAD/EACH_QUORUM ancestry boundary fails closed and retains repair when one DC is unavailable; #210 separately proves the Sync scope-gate EACH_QUORUM fallback fails closed. Publication HEAD (`SERIAL`) can still proceed if its serial domain remains available — **not re-measured here**. Funnel-complete M3 (every funnel, every EQ/SERIAL primitive) = still GAP. |
| M4 Cross-DC HEAD settlement | attempt in eu, repair in na | **PRIOR EVIDENCE — PARTIAL**: #213's shared classifier recognizes a target as an ancestor after HEAD advances and retains repair when one DC is unavailable; `scripts/w2-post-head-multidc-validation.sh` also proves cross-DC HEAD blindness does not authorize cleanup. Full remote replay/settlement, especially Sync-specific M4, remains **GAP** and was not re-executed by PC-0. |
| M5 Concurrent publishers | writer A na, writer B eu | CAS winner is Paxos-level **OBSERVED**. Live two-DC concurrent HEAD advances under session default `LOCAL_SERIAL` = **EXECUTED** (`scripts/library-head-serial-domain-multidc-validation.sh`, `ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` closed 2026-09-14): exactly one of H0→H1 / H0→H2 applies. Full funnel-vs-funnel publication races remain GAP. |
| M6 Cross-DC repair | pub/repair from one DC, worker in another | **EVIDENCE GAP** for the concrete DC-A write → DC-B discovery → settlement-worker proof; the W2 script's local-miss-not-cleanup observation is not that end-to-end proof, and PC-0 did not re-execute it. |
| M7 Stale placement | P changes before pre-HEAD fence | **MIXED**: F1 Office and F2 materialized placements have real Cassandra/MinIO GC-before-validation RED/GREEN evidence (`TestW2CreateFileOfficeTemplateExactPlacementBeforeHead`, `TestW2UploadFileExactPlacementBeforeHead`); F3 fence is **OBSERVED** (W1 retired-placement); Sync provenanced blocks have source/existing evidence for exact-P. OnlyOffice E1-4 and SeafHTTP single-shot E1-5a have COMMITTED/TERMINAL RED/GREEN evidence; SeafHTTP streaming E1-5b has victim COMMITTED/TERMINAL RED/GREEN, selective same-tracker recovery and real HEAD-conflict evidence; cross-repo lacks equivalent pre-HEAD exact-P = **GAP**. These observations do not close TTL-bound own-pin continuity through HEAD (W2-0 OPEN; W2-6a OPEN). |
| M8 Funnel-specific | Sync, CFFB, stored v2, SeafHTTP, OO, cross-repo | **MIXED/PARTIAL**: CFFB/shared has classifier evidence, not full end-to-end multi-DC funnel proof; Sync xDC is **PRIOR EVIDENCE** (#210, see M2/M3); full 3-DC proof for OO/SeafHTTP/cross-repo remains **EVIDENCE GAP**. |
| M9 Initial HEAD from a blind DC | initializer vs CAS-published HEAD | **PRIOR EVIDENCE, resolved**: audit 2026-09-10 (`scripts/pc0-initial-head-xdc-probe.sh` bug mode, real 3-DC) — the pre-fix `createInitialCommit`/`InitializeLibraryFS` shape from blind dc-eu reverted an LWT-published HEAD in every DC; 2026-09-11 (`scripts/h1-initial-head-multidc-validation.sh`, real 3-DC, handler-level) — both production initializers driven from blind dc-eu keep and return the HEAD dc-na published. Not re-executed by the gate. |

Audit re-execution note (2026-09-10): the #210 and #213 scripts cited as
prior evidence were re-run on this baseline. `w2-sync-putblock-xdc-provenance-validation.sh`
passed 4/4 on its second run (first run aborted in the N=1000 cost leg
right after node restarts; because of `set -e` the fail-closed leg did not
run). `w2-post-head-multidc-validation.sh` passed 6/6 on its third run (two
runs failed the EACH_QUORUM seed with `received only 2 responses` seconds
after `migrate`; the same seed passes in 0.03 s from a warm runner). Harness
startup sensitivity is tech debt, not an architectural result (§15).

The table is a characterization matrix. Completeness means every row has a
status string, not that M1–M9 ran as publication races (M9 is a separate
cqlsh probe, `scripts/pc0-initial-head-xdc-probe.sh`, not executed by the gate).

---

## 14. Architectural conclusion

### Verdict

```text
PROCEED WITH COORDINATOR
```

### Why the idea was not refuted

The inventory supports two different statements; they must not be collapsed.

#### Observed common kernel today

There is no universal `classified → publishable → stage` order in the current
funnels. For every block-bearing publication, the stable observed kernel begins
once a funnel stages:

```text
                 ┌→ durable repair intent ──┐
stage pub ────────┤                           ├→ HEAD
                 └→ readiness/final exact-P ┘
                    (when present; also before HEAD; relative order is funnel-specific)
```

An empty-file path with no physical dependencies may have no repair row. CFFB
observes `verify/capture placement → own-liveness work → stage → repair →
final exact-P fence → HEAD`; Sync observes `stage → provenance/readiness →
repair → HEAD`, and can discover `UNPROVENANCED`/`ERROR` after staging. The
CreateFile Office and stored UploadFile observe `stage → repair → exact-P → HEAD`;
OnlyOffice and SeafHTTP single-shot/streaming now observe `stage → repair → final exact-P → HEAD`; cross-repo still observes `stage → repair → HEAD`. The target
coordinator contract below deliberately adds a stronger adapter boundary to
close the classification gap; it is not a claim about every current funnel.

#### Target coordinator contract

```text
adapter classification/evidence
→ PublishableInput (target invariant: no UNPROVENANCED/ERROR)
→ PublicationCoordinator
```

Differences are:

1. **Which adapter proves blocks** (session, PutBlock inference, OO download,
   copy) and which classes remain `UNPROVENANCED`/`ERROR`. The target
   coordinator must reject those before `stage pub:`; current Sync can stage
   before its scope gate discovers that outcome.
2. **Which funnels omit publication-authority/continuity at HEAD** (gap by
   provenance, not a second protocol). Exact-P is one mechanism; renewal/
   overlap can close own-`up:` materialization. The target adapter must acquire
   durable own `up:` for `BORROWED` and carry `ExpectedP` where applicable;
   the final exact-P revalidation remains after stage and before HEAD, with its
   relation to repair kept funnel-specific.
   F3 carries `ExpectedP` for both `SessionUpload` and `BorrowedFS`; the
   adapter-specific order between capture and pin acquisition is not frozen.
   Cross-repo still needs a destination own pin.
3. **Two HEAD classifiers** (v2 SERIAL confirm vs Sync uncertain-on-any-error).
4. **Repair ownership** (unique attempt commit vs shared Sync `commit_id`).
5. **Repair vs readiness order** (CFFB repair-then-fence vs Sync
   readiness-then-repair). Do not unify this as a silent PC-1/PC-2 default.

Those are extraction and migration problems, not evidence that a common
protocol does not exist. Centralizing the spine would *reduce* W2 surface
once publishable input is required and UNKNOWN/loser rules stop being
re-proven per funnel.

The 2026-09-10 audit strengthened, not weakened, this conclusion: the act of
enumerating every path that changes block liveness through HEAD surfaced two
HEAD writers outside the CAS model (§3.4), four resurrection paths that the
"tree-only" label had hidden (§3.5), and the GC negative side interacting
with shared fs_objects (§6 PUBL-10). A coordinator is the place where such
paths become impossible to hide; the characterization is what found them.

### Why not "implement it in this PR"

The plan forbids it. Also: Sync identity is still inference (§11, unchanged
by #210); publication authority/continuity is not universal
(`ISSUE-PC0-EXACT-P-FUNNEL-GAP-01`); the coordinator boundary's scope to
newly-live dependencies is now decided by PC-D1: it is admissible incrementally
only with a valid certified-baseline witness; otherwise the current HEAD must
be fully certified (`ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01`); unifying HEAD
classify or readiness/repair order is a behavior-sensitive change that needs
its own PR.

### Candidate boundary (CANDIDATE / NOT IMPLEMENTED)

```text
          FUNNEL-SPECIFIC

SyncEvidenceProvider
SeafHTTPEvidenceProvider
V2UploadEvidenceProvider
CreateFileFromBlocksEvidenceProvider
OnlyOfficeEvidenceProvider
CrossRepoEvidenceProvider
          │
          ▼
   ClassifiedPublicationInput
     OWNED | BORROWED | UNPROVENANCED | ERROR
          │
          ▼
   OWNED: prove / renew own liveness
    BORROWED / provenance requiring ExpectedP:
      { capture ExpectedP where required;
        acquire or renew durable own up: }
      (the order between these adapter steps is adapter-specific;
       both precede target staging; this is NOT the final check --
       foreign fs: observation alone, without the own pin, is not publishable)
   reject ERROR            ── does not produce PublishableInput
   resolve or reject UNPROVENANCED
          │
          ▼
   PublishableInput
     - canonical blocks that are actually publishable
     - durable own liveness for every physical dependency
      - ExpectedP where the adapter requires it (F3: SessionUpload or BorrowedFS;
        captured, not yet revalidated)
     - attempt identity (commit/attempt id)
          │
          ▼
       COMMON  (stateless orchestration on every node)

 PublicationCoordinator
          │
          ├─ stage liveness          (PublishableInput only)
          ├─ repair durable          (for every block dependency; an empty-file
          │                            path may have no row)
          ├─ publication readiness   (optional; renew own liveness and/or FINAL
          │  revalidation against ExpectedP -- after stage and before HEAD;
          │  its order relative to repair is funnel-specific)
          ├─ HEAD attempt + classify
          └─ settlement
```

Do not freeze Go APIs. The names above are documentation. For CFFB, PC-2
must preserve `stage < repair < final exact-P revalidation < HEAD` (§4, §5
F3); `PublishableInput` carrying `ExpectedP` is not itself proof the
revalidation happened.

### PC-1 skeleton (2026-09-11): what exists, what does not

`internal/publication` now holds the common vocabulary of the boundary above.
It is a standard-library-only package with no I/O, no table, no CQL, no
consistency level, and no TTL:

| Type | Represents | Frozen? |
|---|---|---|
| `AttemptIdentity` / `AttemptID` | publication attempt identity: org, repo, `pub:` attempt id, target commit, expected HEAD. Attempt id and target commit are separate fields because Sync mints a fresh UUID while v2/SeafHTTP/OO reuse the commit id | shape only |
| `HeadOutcome` | target canonical knowledge: `applied` / `known-loser` / `unknown`; `""` invalid; no cleanup-authority method | vocabulary + UNKNOWN distinction frozen (PUBL-4/5) |
| `SettlementDisposition`, `SettlementDecision` | disposition bound to an exact valid `AttemptIdentity`: `promote` / `cleanup-attempt` / `retain`; validates UNKNOWN→retain-only, forbids KNOWN_LOSER→promote, and permits APPLIED+cleanup only for a distinct same-target attempt id | safety constraints frozen; adapter evidence not frozen |
| `PublishableInput`, `DependencyEvidence`, `WorkSetScope` | the opaque adapter→coordinator evidence boundary. Only `WorkSetScopeNewlyLive` (the `LogicalPositiveBlockDelta` shape) is declared, as the sole incremental scope; there is deliberately no block-list accessor | **decision frozen by PC-D1** — `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01` is resolved; PC-D1A provides canonical witness/HEAD authority. PR #228 implements the PC-D1B.1 fail-closed identity-authority gate, and PC-D1B.3 mapping authority/M18-M19 is implemented by PR #233 (merged). Certification-window lifecycle fencing and a productive consumer remain prerequisites before PC-2; historical backfill is a greenfield non-goal. The certifier itself is not a productive consumer. |
| `Phase` | `stage` / `repair-intent` / `readiness` / `head` / `settlement` labels; **no order method** | partial order only (§4) |
| `PublicationCoordinator` | zero-field value; one method, `ValidateSettlement` (pure; never derives a disposition). No `Publish`/`Stage`/`Repair`/`Head`/`Settle` | all package methods inventoried by `TestPC1PublicationPackageMethodAndFunctionSetsAreInventoried` |

What PC-1 did **not** do, by contract: migrate any of F1–F9, R1–R4, or the
initializers; change `stagePendingPublishedFiles`,
`queuePendingPublishedFileRepairs`, `validateCommitBlockPublicationFences`,
`stageSeafHTTPPublishAttemptReferences`, `stageSyncCommitBlockDelta`,
`ensureSyncCommitBlockPublicationReadiness`, `UpdateLibraryHeadFromSnapshot`,
`updateLibraryHeadWithStats`, or any settlement helper; convert
`InitialHeadOutcome`, the v2 sentinels, or the Sync errors to `HeadOutcome`
(the §9 mapping shows one shape whose classification would change); impose a
universal stage→readiness→repair order; add process-local state. The PC-1
source contracts in `internal/db/pc1_publication_coordinator_contract_test.go`
and mutation legs M11–M16 (§16) hold each of these.

`productive funnels using coordinator = 0`, `runtime behavior changes = 0`,
`new Cassandra I/O = 0`, `schema changes = 0`, `consistency changes = 0`,
`GC changes = 0`. PC-1 does not close W2, R31, or X1.

### Coordinator must be multi-DC from PC-1

Valid:

```text
attempt begins in dc-eu → process dies → repair continues in dc-na
→ dc-asia has a stale LQ view → system still converges
```

Invalid: home-DC coordinator, in-memory lock, "not visible locally ⇒ absent".

Durable coordination remains Cassandra + appropriate CL/LWT domains. HEAD
itself advances **only** through that domain: the unconditional initializers
(§3.4) violated this at audit time and were converted on 2026-09-11, outside
the coordinator, before PC-1.

### Relation to W2 / R31 / G3 / G4 / #209 / #210 / #212 / #213

- **Coordinator ≠ W2 closed.** W2 still must prove: once D(P1) committed, no
  legitimate writer publishes durable liveness on P1.
- **Coordinator ≠ R31 closed.** Possibly-applied HEAD must not lose
  definitive liveness because `pub:` TTL expired.
- **G4** allows orphan COMMITTED(P1,D1) + blocks(L)=P2. Do not do G4 until
  every physical dependency reaches HEAD with continuous valid publication
  authority/liveness for its provenance (own pin + exact-P, renewal/overlap,
  or both). Do not freeze "every funnel must obey exact-P" as that G4
  condition. This characterization is a prerequisite for that uniformity,
  not a G3 blocker.
- **GC Phase 5 (negative side).** `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01`
  (P0 latent, PRE-GC): the expired-version cascade deletes fs_objects still
  reachable from HEAD. Not a coordinator concern to fix, but it removes "GC
  already protects inherited dependencies" from the option list and must be
  fixed before any `GC_ENABLED=true` with `version_ttl_days > 0`.
- **G3 is implemented by #212.** The GC-side canonical-retirement change is already in this baseline and is orthogonal to the publication funnels; no PC-0 funnel re-characterization is required.
- **#209/#210/#212/#213:** done in their narrow scopes. This branch is rebased onto
  `main` at `7b9102af9` (§ baseline note). Sync M2 / LQ-miss→EQ-fallback / the
  consistency map (§7, §8) / §11 / §13 have been re-characterized against
  #210's merged `BlockReferenceExistsEachQuorum` fallback and its real 3-DC
  evidence; the known issue it closed
  (`ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01`) is resolved, not
  reimplemented here. #213's shared repair classifier is also represented in
  §7–§9 and M3/M4: SERIAL HEAD, bounded 1024-node EACH_QUORUM ancestry, a
  30-second deadline, and UNKNOWN-retain on inconclusive evidence. That does
  not close broader R31, OnlyOffice's independent legacy traversal, Sync
  expired provenance, or M6's cross-DC discovery/settlement proof. #209 (G2
  PREPARED→COMMITTED handoff) touches only `internal/gc` and needed no further
  publication-funnel re-characterization. #212 (G3 canonical retirement) is GC-side
  and likewise required no further publication-funnel re-characterization. Source contracts were rerun after
  the rebase (see below).

### Recommended next PR sequence (not frozen, not implemented)

> **Superseded as a roadmap (2026-09-29):** the order of work toward X1 is
> [X1-CRITICAL-PATH.md](./X1-CRITICAL-PATH.md). PC-2 and the PC-D1B stages
> below are not X1 prerequisites; the coordinator may be used when migrating a
> funnel is the minimal way to close a W2 exit row. The sequence is kept as
> the coordinator's own evolution plan.

```text
PC-0  this PR (characterization)
  → H1 follow-up (separate, small, prioritized): conditional HEAD initializer
     — coordinator prerequisite (§3.4) — DONE 2026-09-11
  → PC-1  coordinator skeleton / common types, behavior-preserving, zero funnels migrated;
          does not freeze full-work-set semantics — DONE 2026-09-11 (internal/publication)
  → PC-D1 certified-baseline frontier decision — DONE 2026-09-12
  → PC-D1A authority foundation: witness schema/validity, HEAD-fenced baseline CAS,
     and atomic HEAD+witness global-SERIAL primitives — DONE 2026-09-19
  → PC-D1B.1 cold-path complete-tree certifier, exact minted-P/liveness/GC
     revalidation, physical-byte proof, and witness settlement — IMPLEMENTED
     by PR #228; identity checks are read-only, no productive consumer
  → PC-D1B remaining mapping authority/M18-M19, certification-window
     lifecycle fence, and first productive consumer — OPEN before PC-2
     (historical backfill remains a greenfield non-goal)
     (GC Phase 5 fix is PRE-GC regardless; R31 convergence is PRE-X1)
  → PC-2  migrate the best-understood funnel (CreateFileFromBlocks / shared Once)
          preserving today's stage < repair < final exact-P revalidation < HEAD
          order unless a separate PR unifies it with evidence
  → PC-3… remaining v2/SeafHTTP/OnlyOffice/cross-repo, and the content-resurrection
     adapter (R1–R4) as BORROWED with a real pin
  → Sync adapter last
  → only then consider a Sync provenance redesign if identity remains inference
```

If PC-1 cannot unify HEAD classify without a behavior change, that change is a
**separate** explicit PR, not a silent extra in PC-1. Outcome: PC-1 did not
unify it — the §9 mapping table shows v2's "ambiguous + different HEAD" shape
would change classification — so the unification remains its own PR.

---

## 15. Findings (not fixed here)

| ID | Sev | Scope | Finding |
|---|---|---|---|
| `ISSUE-PC0-INHERITED-DEPENDENCY-CONTINUITY-01` | P1 | **DECIDED / PC-D1A LANDED / PC-D1B.1 CERTIFIER GATE IMPLEMENTED / ROLLOUT OPEN** | PC-D1 assigns inherited continuity to the certified baseline frontier. PC-D1A supplies canonical witness state and global-SERIAL HEAD authority; PR #228 verifies the captured commit and all reachable fs_objects against durable identity claims before physical/liveness work, revalidates metadata before witness settlement, and preserves exact-P, physical-byte, EACH_QUORUM liveness, GC-authority, and final HEAD-fenced CAS checks. M1-M15 (17 mutation legs: M14a/b and M15a/b) and real 3-DC complete fs_object A/B, commit H-to-R1/R2, partial-row, authority-outage, and EACH_QUORUM outage legs close the certifier correctness gate. SHA-1-only files without a proven mapping remain `identity_unproven`; PR #233 implements PC-D1B.3 mapping authority/M18-M19 (pending merge). Lifecycle serialization and the first productive consumer remain before PC-2. Historical backfill is a greenfield non-goal. The PC-D1B.1 certifier itself is not a productive importer; `GC_ENABLED=false` and the separate Phase 5 PRE-GC fix remain.
| `ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` (multi-DC reversion variant) | P1 → **resolved 2026-09-11** | was FOLLOW-UP, separate and prioritized; coordinator prerequisite (pre-existing) | Two unconditional `UPDATE libraries SET head_commit_id` initializers (`InitializeLibraryFS`, `createInitialCommit`, the latter reachable from `GET /commit/HEAD`) lived outside the CAS domain; reproduced on the real 3-DC fixture reverting an LWT-published HEAD from a blind DC (§3.4, M9). Fixed by `InitializeLibraryHeadIfUnset` with unit, single-cluster and handler-level 3-DC evidence; `TestPC0NoUnconditionalHeadUpdateRemains` + mutation leg M10 pin it. |
| `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01` | P1 | FOLLOW-UP / W2 / funnel migration (pre-existing, newly classified) | `RevertFile`, `RevertDirectory`, `RestoreTrashItem`, `RevertDirents` publish a positive borrowed block-dependency delta with no pin, `pub:`, repair, or fence (§3.5). Reclassified from tree-only; not fixed here. |
| `ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01` | Historical **P0 latent** cascade | PRE-GC follow-up | [Scoped fix/evidence](./GC-PHASE5-SHARED-FSOBJECTS.md) makes unguarded commit retirement metadata-only. Sharing-aware reclamation and Phase 6/concurrent metadata destruction remain OPEN; GC stays OFF. |
| `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` | P1 → **resolved 2026-09-12** | PRE-X1 / PRE-GC / R31 (pre-existing; not a #213 regression) | Shared repair walk is now resumable from a durable SERIAL HEAD anchor + cursor (next unread commit, including after timeout/EQ error); a clean genesis is persisted as exhausted before the SERIAL HEAD re-read and may re-anchor to a later SERIAL HEAD (second 1024-node chunk in the same 30s context); a re-anchor CAS loser does not replay an already-exhausted snapshot; UNKNOWN still retains; missing repair row is a no-op; unresolved visits can write/refresh per-row `pub:<repo:commit:fsID>` after classification while pending (`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`) — that can recreate an expired `pub:` and does not close a zero-ref gap; the worker best-effort removes that identity before the repair row. Ordinary Sync success does not pay per-block repair-owned DELETE. Concurrent renewal can leave TTL-bounded `pub:` (`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`). Progress LWTs share 32 bucket partitions (`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`). Broader R31 (known-loser, zero-ref discovery) remains open (§9). |
| `ISSUE-PUBLISH-HEAD-TREE-STATS-COST-01` | P2 | FOLLOW-UP (cost) | Every publish pays a recursive per-directory stats walk inside the stage→HEAD window; Sync pays two before its CAS. §12 corrected; not optimized here. |
| Raw-CQL HEAD writers invisible to the lexical guard | P2 | THIS-PR (hardening, closed) | `TestPC0RawHeadColumnWritersAreInventoried` + mutation leg M7; the finding is not hypothetical (§3.4). Method-value / aliased-callee coverage stays documented as out of scope: P2 TECH DEBT. |
| §11 protocol-order wording | P2 | THIS-PR (fixed) | `PutCommit` stores the commit before blocks arrive; PutBlock↔pending-commit binding is a DESIGN HYPOTHESIS and `CheckBlocks` pins a DESIGN OPTION, both follow-ups, neither adopted. Sync remains last. |
| Harness startup sensitivity | P2 | TECH DEBT | #210/#213 scripts abort on non-evidence legs (cost) or EACH_QUORUM timeouts seconds after `migrate`/node restarts; integration runs against the 3-DC fixture need `CASSANDRA_HOSTS` pointed at a fixture node or `TestMain` cleanup fails against the dev Cassandra (§13, `docs/TESTING.md`). No architectural impact. |
| `ISSUE-PC0-EXACT-P-FUNNEL-GAP-01` | P1 | FOLLOW-UP / W2 (newly registered by PC-0, not introduced by it) | Publication-authority/continuity before HEAD is not uniform by provenance. Exact-P exists for CreateFileFromBlocks placements, UploadFile's materialized placement (W2-6, PR #237; previously `nil`) and Sync-provenanced blocks. `CreateFile` Office-template publication now checks exact P before HEAD (W2-6a), but remains OPEN because TTL-bound `pub:` does not prove continuity through HEAD; OnlyOffice measured pre-HEAD (E1-4) and SeafHTTP single-shot measured pre-HEAD (E1-5a) are CLOSED-FIX; SeafHTTP streaming measured pre-HEAD is also CLOSED-FIX under E1-5b; their post-HEAD/R31 and cross-repo remain open. This does **not** prescribe exact-P as the only fix. |
| Sync PutBlock identity | P1 | already `ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`; cross-DC visibility slice closed by `ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01` (#210, resolved) | Evidence is still inference from `up:sync:<repo>:<block>` — #210 widened its visibility domain, not its identity (§11). The target coordinator must reject input with no provenanced PutBlock; today's Sync can still publish after a clean global miss, which remains the open W2 gap. |
| `ISSUE-LIBRARY-ROLLBACK-GHOST-PROJECTIONS-01` | P2 | ✅ Resolved 2026-09-11 (`fix/library-rollback-ghost-projections`) | Durable `library_rollback_pending` marker + bounded **fair** reaper (clustering cursor, rotating start bucket). Marker is discovery only; cleanup authority remains `DELETE libraries ... IF head_commit_id = null`. Fault-injection covers authority-applied → crash → recovery. Soft-delete alternative not taken (`InitializeLibraryHeadIfUnset` interaction). |
| HEAD classify split | P2 | FOLLOW-UP / PC-1 | v2 confirms ambiguous CAS with SERIAL; Sync maps every CAS error to UNKNOWN without confirm. Also (2026-09-11, H1 review): v2's `resolveLibraryHeadUpdateError` reports `ambiguous + a different current HEAD` as the original ambiguous failure, although the commit may have applied and been succeeded — the initializer classifies that same shape as UNKNOWN. Not destructive authority today. PC-1 (2026-09-11) introduced the common tri-state `publication.HeadOutcome` and documented the mapping (§9) but deliberately did **not** convert any classifier, because the v2 ambiguous-plus-different-HEAD shape would change classification; the unification is a separate explicit PR after PC-1. |
| `ISSUE-GROUP-LIBRARY-CREATION-RESUMABILITY-01` | P2 | FOLLOW-UP (registered by the H1 review, deliberately out of H1) | A group-library creation preserved on an UNKNOWN initial-HEAD publish, a share write error past publication, or a refused rollback is durable and cannot be resumed — repeating the POST mints another library; its group share is `not_attempted` or `unconfirmed` depending on the failure phase. A durable single-owner claim protocol was designed and audited, then split out of #214 as its own subsystem (`docs/KNOWN_ISSUES.md`). |
| Cross-repo own liveness | P1 | already R3 `UNKNOWN` | Destination does not take own `up:`. Exact-P alone would still be TOCTOU. |
| Known-loser durability | P2 | already `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01` | No durable loser witness. |
| Repair reachability | P1 (closed for shared classifier + convergence) | `ISSUE-PUBLISH-REPAIR-REACHABILITY-01` closed by #213; `ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01` closed 2026-09-12; broader R31 remains open | Shared cold path is at most 1024 sequential EACH_QUORUM parent reads **per ancestry chunk** under 30 seconds, resumed from a durable next-unread cursor; SERIAL HEAD is observed when creating or replacing the anchor; a visit that clean-walks to genesis may re-observe HEAD and walk a second chunk (≤2048 parent reads); a re-anchor CAS loser does not replay an exhausted snapshot; missing repair row is a no-op; inconclusive evidence retains repair. An unresolved visit can write/refresh repair-owned `pub:` after classify (`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`); that can recreate an expired pin and does not close a zero-ref gap. Eager repair-owned `pub:` cleanup after settlement is best-effort (`ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`). Progress LWTs share 32 bucket partitions (`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`). |
| `pub:` TTL | P1 | already R31 / `ISSUE-GC-PUB-REF-ZERO-REF-01` | Finite TTL can still open a liveness gap. |
| M3/M4/M5/M8 3-DC (remaining) | — | MATRIX GAP | M2/M3/M8's Sync-specific slice now has prior evidence (#210, §13). Still GAP: M3's funnel-complete claim (every funnel, every EQ/SERIAL primitive), M4's Sync-specific slice, M5 (live two-DC concurrent publishers), and M8's OO/SeafHTTP/cross-repo slice. |

W2, R31, and X1 remain OPEN.

---

## 16. Tests in this PR

| Test | Property |
|---|---|
| `TestPC0AllHeadCallersAreInventoried` | functions and package-level `var = func` literals that lexically call named HEAD helpers must be classified; method values/aliases/second branches remain out of scope |
| `TestPC0BlockPublicationFunnelsHaveMappedSeams` | each publication funnel has characteristic seams/stage/repair/HEAD/settle symbols; characteristic seams are not assumed to be a universal pre-stage phase |
| `TestPC0R3StageToHeadInventoryIsSubset` | live R3 `r3PublicationStageToHeadBoundaries` labels are a subset of the PC-0 mapping |
| `TestPC0BlockBearingFunnelsKeepDurableRepairBeforeHEAD` | every mapped block-bearing funnel keeps `stage < durable repair < HEAD`; an empty-file branch may produce no repair row |
| `TestPC0ObservedRepairReadinessPartialOrder` | CFFB `stage < repair < fence < HEAD`; Sync `stage < readiness < repair < final exact-P < HEAD`; auto-merge caller `stage < helper < HEAD` |
| `TestPC0CriticalConsistencyPrimitivesArePinned` | selected source tokens at named primitives; not the full consistency map; HEAD serial domain *is* pinned (needles plus `TestPC0HeadSerialDomainPinsGlobalSerial`) |
| ~~`TestPC0PublicationCoordinatorTypeIsNotImplemented`~~ | **retired by PC-1 (2026-09-11)**: it froze "no `PublicationCoordinator` anywhere under `internal/`"; replaced by the PC-1 contracts below, which freeze the opposite boundary (exactly one, in `internal/publication`, adopted by nothing productive) |
| `TestPC1PublicationCoordinatorIsDeclaredExactlyOnce` | exactly one top-level `PublicationCoordinator` type under `internal/` and `cmd/`, at `internal/publication/coordinator.go`, a concrete struct with zero fields (no mutex, no in-memory ownership) |
| `TestPC1PublicationPackageHasZeroProductiveImporters` | no production file outside `internal/publication` imports the package: zero call edges from any endpoint, hence zero new CQL/CL/TTL reachable through the coordinator |
| `TestPC1ProductiveFunnelsDoNotReferenceCoordinator` | no inventoried HEAD caller (F1–F9, tree mutations, R1–R4, primitive) or wrapper alias references `publication.X` / `PublicationCoordinator` / `NewPublicationCoordinator`, and no production function outside the package mentions those identifiers — catches an injected call before an import exists |
| `TestPC1PublicationPackageIsStatelessAndStorageFree` | `internal/publication` imports exactly `errors`/`fmt`; package-level vars are limited to four `errors.New` sentinels; code inside this package may neither reassign them nor take their address. They remain exported Go vars, but PC-1 has zero productive importers |
| `TestPC1PublicationPackageTypeSurfaceIsInventoried` | positive inventory of every declared type name/kind, struct field, interface method, and function signature; rejects capability smuggling through interfaces, named function types, or function-typed fields |
| `TestPC1PublicationPackageMethodAndFunctionSetsAreInventoried` | every concrete package method is allowlisted (`AttemptIdentity`, `HeadOutcome`, `SettlementDisposition`, `SettlementDecision`, coordinator), and the only package function is `NewPublicationCoordinator` |
| `TestPC1PublicationPackageCallSetIsInventoried` | every production call expression and occurrence count is allowlisted; catches I/O such as `fmt.Println` inserted inside an existing method, plus builtin/dynamic calls |
| `TestPC1PC0InventoryIsUnchanged` | content pin of `pc0ExpectedHeadCallers` (21 rows), `pc0BlockPublicationFunnels` (8), `pc0PublicationWrappers` (5), `pc0ExpectedHeadColumnWriters` (7, including the two PC-D1A authority-only primitives), and the stage seams: PC-1 reclassified, remapped, and migrated nothing |
| `internal/publication` unit tests | HEAD outcomes and dispositions are distinct/valid; every decision requires a valid exact attempt identity; target outcome and attempt disposition stay separate; same-target `applied` + exact cleanup is representable only with a distinct attempt id; UNKNOWN cleanup/promotion, KNOWN_LOSER promotion, APPLIED cleanup with attempt==target, and invalid values fail closed both directly and through `PublicationCoordinator.ValidateSettlement`; `AttemptIdentity.Validate` accepts attempt==commit (v2) and attempt≠commit (Sync); assignability-aware type resolution proves only `WorkSetScopeNewlyLive` is declared and pins its value to `"newly-live"` |
| `TestPC0PublicationWrappersRemainAliases` | CreateFileFromBlocks/UploadFile/SeafHTTP wrappers still delegate |
| `TestPC0TreeMutationsDoNotCallBlockPublicationStageSeams` | tree-only HEAD callers do not invoke the known block-publication stage seams |
| `TestPC0StoredUploadRevalidatesMaterializedExactPlacement` | UploadFile passes its materialized exact placement so the exact-P fence runs before HEAD (W2-6; inverted from the former `...IsNoOpWhenCommitBlocksNil` characterization) |
| `TestPC0RawHeadColumnWritersAreInventoried` | every production string literal (internal/, cmd/) writing `libraries.head_commit_id` is inventoried with its shape (`cas` / `insert-create` / `update-unconditional`); closes the raw-CQL blind spot; flipping an initializer to CAS must flip its shape |
| `TestPC0ContentResurrectionPathsObservedWithoutPublicationSeams` | R1–R4 direct stage/repair/fence seam inventory; does not trace E1-10e indirect admission/fence helpers or certify their absence |
| gc `TestPhase5CommitRetirementPreservesFSObjectsSharedWithHEAD` | preservation regression inverted from the historical unsafe characterization; real-stack companion `TestPhase5SharedTreesRealStack` records RED/GREEN for PUBL-10 / Phase 5 |
| `scripts/pc0-initial-head-xdc-probe.sh` | real 3-DC probe of §3.4 with two fail-closed modes: bug mode runs the pre-fix unconditional shape from a blind DC and requires HEAD reverted (the record of the bug); `--expect-cas-fix` runs the production conditional shape (`IF head_commit_id = null AND created_at != null`) from the same blind DC and requires it rejected and HEAD survived; the CAS control leg asserts `[applied]=False` and the real HEAD. CQL-shape level |
| `scripts/h1-initial-head-multidc-validation.sh` + `TestH1InitialHead*3DC` | real 3-DC, handler-level, one dc-eu stop/restart cycle per initializer on its own library: `InitializeLibraryFS` and Sync `createInitialCommit`, each driven from a DC asserted blind immediately before it runs, keep and return the HEAD another DC published, and that HEAD's commit is servable locally at the consistency `GET /commit/:id` uses; gate `SESAMEFS_REQUIRE_H1_INITIAL_HEAD_MULTIDC_EVIDENCE=1`; RED against the pre-fix production files |
| `TestPC0NoUnconditionalHeadUpdateRemains` + clause pins | no production UPDATE of `libraries.head_commit_id` without `IF`; `TestPC0CriticalConsistencyPrimitivesArePinned` pins `IF head_commit_id = null` and `AND created_at != null` independently; mutation legs M10/M10a/M10b require RED |
| `TestPC0HeadSerialDomainPinsGlobalSerial` + `TestPC0HeadAuthorityDeleteGuardsAreInventoried` + `TestLibraryHeadSerialConsistencyIsGlobalSerial` + `TestPC0HeadAuthorityDeleteCQLRecognition` + `TestPC0HeadAuthorityQueryResolvesIndirectCQL` + `TestPC0HeadAuthorityQueryDiscoversPackageFuncLit` + `TestPC0UnresolvedHeadQueriesStayOutOfHeadDomain` | every cas-shaped `libraries.head_commit_id` writer and the creation-rollback DELETE IF pin `SerialConsistency(LibraryHeadSerialConsistency)` on the MapScanCAS chain; the constant is `gocql.Serial` and never configurable; Query/Bind discovery of competing HEAD mutations includes package-level `var fn = func`, with R12-style table/IF folding so `IF other AND head_commit_id`, `sesamefs.libraries`, `SET head IF EXISTS`, and concat UPDATE fragments cannot hide; resolves const/concat/append; fails closed on unresolvable CQL; shape-pins each unresolved allowlist exception (no skipped dynamic SET/`fmt.Sprintf` fragments); and forbids HEAD-authority statements in embedded `migrations/*.cql` |
| `scripts/library-head-serial-domain-mutation-validation.sh` | M1–M4 SERIAL→LOCAL_SERIAL per seam, M5 pin removed, M6 constant = LocalSerial, M7 hidden `IF created_at AND head_commit_id`, M8 hidden `sesamefs.libraries`, M9 `Query(fmt.Sprintf(DELETE IF))`, M10 package-level FuncLit DELETE IF, M11 allowlisted `UpdateLibrary` becomes HEAD LWT, M12 dynamic SET fragment, M13 non-literal lock `fmt.Sprintf`, M14 embedded migration HEAD LWT, M15 migration `SET head_commit_id IF EXISTS`, M16 migration whole-row `DELETE IF EXISTS`, M17 concat `UPDATE libraries SET` + `head_commit_id`, M18 `UpdateLibrary` `updates` initializer preloads HEAD, M19 `injectHead(&updates)`, M20 SET loop ranges a HEAD literal, M21 `poison(&query)`, M22 `update = ...` inside the SET loop, M23 `Migrator.apply` `stmt = strings.Join(...)`, M24 lock helper `DELETE FROM libraries IF EXISTS`, M25 `poison(&stmt)` after a safe CQL initializer, M26 second `SerialConsistency(localSerial)`, M27 second HEAD `Query.Exec()` inside an inventoried writer, M28 `range` rebind of a CQL ident: all RED |
| `scripts/library-head-serial-domain-multidc-validation.sh` + `TestLibraryHeadSerialDomain*3DC` | real 3-DC, session default `LOCAL_SERIAL`, both DCs up: concurrent v2 HEAD advance and concurrent initial HEAD each have exactly one global winner; gate `SESAMEFS_REQUIRE_LIBRARY_HEAD_SERIAL_DOMAIN_EVIDENCE=1`; self-managed (not `./scripts/test.sh api`) |
| integration `TestPC0PublicationMultiDCCharacterization` | 3-DC topology + matrix rows; gate cannot skip-green; GAP/UNKNOWN may complete the matrix |
| `scripts/pc0-publication-inventory-mutation-validation.sh` | 12/12 PC-0 mutation legs RED (M1–M10b). PC-1 grows it to 30/30: M11 funnel import; M12 funnel coordinator call; M13 mutex field; M14 `sync` import; M15 second coordinator; M16 uninventoried coordinator `Publish`; M17 mutable owner slice; M18 package `Publish`; M19 `fmt.Println` inside an allowed method; M20 `AttemptIdentity.Publish` outside the coordinator; M21 inferred `WorkSetScope`; M22 sentinel reassignment; M23 sentinel address-taking/indirect mutation; M24 coordinator validation weakened to attempt identity only; M25 capability interface; M26 function-typed struct field; M27 untyped assignable `WorkSetScope`; M28 changed canonical scope literal |

| `TestPCD1LogicalPositiveDeltaOmitsUncertifiedInheritedDependencies` | executable H1/H2/A-B-C-D counterexample: `LogicalPositiveBlockDelta` returns only newly-live D and omits inherited A/B/C whose continuity is UNKNOWN/CONDITIONAL |
| `TestPCD1MovingHeadCannotCertifyObservedHeadAsNewHead` + `TestPCD1LegacyHeadAdvanceInvalidatesWitness` + `TestPCD1CertifiedHeadAdvanceIsAtomicInduction` + `TestPCD1CertifiedHeadAdvanceRejectsInvalidPredecessor` + `TestPCD1BaselineRevalidationRejectsChangedPlacement` + `TestPCD1BaselineRevalidationRejectsChangedPhysicalP` | source-level witness model: stale observed HEAD cannot certify H′, legacy HEAD advances invalidate a witness, the only valid induction is `(H,H,V) → (H′,H′,V)`, invalid predecessors are rejected without mutation, and changed physical P is rejected |
| `scripts/pc-d1-inherited-continuity-mutation-validation.sh` | 12/12 source-contract mutations are expected RED: induction proof removed, certification condition weakened, owner changed, the GC-aware baseline handshake reordered, TTL-only witness liveness allowed, merge baseline removed, global SERIAL prerequisite weakened, or any of the three inductive predecessor predicates/two atomic HEAD+witness updates weakened; every model mutation requires its specific failure, so an arbitrary interrupted test is not accepted as RED |
| `scripts/pc-d1-inherited-continuity-validation.sh` | real Cassandra 3-DC ephemeral-table probe: stale DC cannot certify observed H after HEAD moves; canonical SERIAL reads prove H′ and no accidental witness |
| `internal/db/library_continuity_test.go` + `TestLibraryContinuityCertifiedFrontier3DC` | PC-D1A authority contracts: canonical columns, live-row/already-visible-deleted-state validity, HEAD and witness predecessor predicates, global SERIAL pins, fail-closed UNKNOWN classification, established-deleted-state rejection, and atomic frontier convergence |
| `scripts/pc-d1a-certified-frontier-mutation-validation.sh` | 13/13 directed source mutations are expected RED: baseline/frontier HEAD predicates, both atomic witness SET fields, both predecessor fields, both global SERIAL pins, unsupported version, stale/deleted witness validity, and both deleted-row LWT guards |
| `scripts/pc-d1a-certified-frontier-multidc-validation.sh` | real Cassandra 3-DC canonical-table evidence: migration 025, competing baseline certificates, stale certification after a legacy HEAD move, globally-visible/established deleted-state authority rejection, and atomic H1→H2 convergence under LOCAL_SERIAL sessions with explicit global SERIAL primitives |
| `TestContinuityFileCompleteness` + `TestContinuityFSObjectFromScannedFieldsPreservesColumnPresence` + `TestCertifierUsesPresenceAwareFSObjectScan` | a present zero-sized empty file remains valid; missing size or block identity becomes `NOT_CERTIFIED/incomplete_fs_object`; legacy rows may omit the optional external SHA-1 column; the reachable-tree walker must use a pointer-to-pointer scan that preserves CQL `NULL` and invoke the completeness guard |
| `scripts/pc-d1b1-certified-baseline-mutation-validation.sh` | 13/13 directed mutations are expected RED; M13 removes the incomplete-file guard and must fail its targeted completeness test |
| `TestLibraryBaselineCertifier3DC` + `TestLibraryBaselineCertifierPartialFSObject3DC` | isolated Cassandra 3-DC and MinIO proof under LOCAL_SERIAL client sessions: exact-P bytes and liveness, P/HEAD races, ambiguous witness settlement, EACH_QUORUM outage, and a remote-complete/local-partial reachable file that is rejected with no witness |
| `scripts/pc-d1b1-certified-baseline-multidc-validation.sh` | owns only the `sesamefs-pcd1b1-*` fixture; disables handoff while creating a local-only partial row, restores remote DCs and handoff, proves the local certifier fails closed, then runs the unavailable-datacenter leg with `GC_ENABLED=false` |
Existing suite remains the no-runtime-change check together with
`git diff --check` on this branch's production `.go` files (expected empty).
For PC-1 the no-runtime-change evidence is `git diff --stat main -- internal
cmd ':!*_test.go' ':!internal/publication'` (empty: no production file
outside the new package changed) plus
`TestPC1PublicationPackageHasZeroProductiveImporters` (the new package has no
production importer, so no call graph, CQL, consistency level, or TTL changed
through it).

### E1-5b current SeafHTTP streaming disposition (2026-10-04)

F7 retains original confirmed placements with ChunkUpload accounting and across
metadata retries. After durable repair, bounded authority reads validate every
distinct original P immediately before each HEAD attempt. Blocked/Changed
rejects with owned cleanup and selective invalidation; same-tracker retry
repeats productive materialization only for rejected digests. Real chunked
COMMITTED/TERMINAL RED/GREEN, normal control, terminal selective recovery,
HEAD-conflict and omission evidence are in [E1-5b](./E1-5B-SEAFHTTP-STREAMING-PUBLICATION-SAFETY.md).
Only measured streaming pre-HEAD and same-process retry are CLOSED-FIX. Dated
snapshots of F7 lacking exact-P are superseded, not broader continuity claims.
Post-HEAD/R31, W2-11..14, E1/X1 and cross-repo remain OPEN; production GC OFF.


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
