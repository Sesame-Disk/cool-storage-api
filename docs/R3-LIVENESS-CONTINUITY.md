# R3 liveness-continuity characterization

## E1-16 post-D contract alignment (2026-10-10)

[E1-16](./E1-16-POST-D-CONTRACT-ALIGNMENT.md) aligns the W2 criterion with G4
contract 5: a logical reference of L written after COMMITTED D(P1) is not a
P1 dependency. With that reading, and source arguments over current code,
[X1 §4](./X1-CRITICAL-PATH.md#4-w2-exit-checklist) records the Sync rows
(PutBlock → HEAD, retry from another pod, no-PutBlock, `recv-fs-before-put`)
and the other adopted funnels as CLOSED for X1. Their post-HEAD R31 safety
rests on the shared EACH_QUORUM repair guard. Content resurrection (W2-10)
stays OPEN, so R3 remains OPEN. The provenance table below is a
characterization snapshot; its `CONDITIONAL`/`UNKNOWN` cells describe
temporal premises of each row and do not define current closure state.

## Greenfield first-production reconciliation (2026-09-30)

W2-0 is **CLOSED-EVIDENCE for the covered current-version mechanism** under
the [first-production contract](./X1-CRITICAL-PATH.md#first-production-deployment-contract).
The #239 durable repair / final exact-P ordering and #241 native wire loss,
real SIGKILL, independent recovery and three-DC evidence are preserved.
Both incompatible mixed-version combinations still reproduce D(P)+HEAD;
ISSUE-W2-INCOMPATIBLE-MIXED-ROLLOUT-01 tracks them as P1 FOLLOW-UP / GENERAL,
outside v1 first activation. No compatibility gate is inferred or added.
W2-1, W2-2 and W2-6a remain OPEN through the separately tracked R31 residuals.
W2-4 closes the demonstrated no-PutBlock pre-HEAD gap with all-block exact-P
validation after durable repair; its historical counterexample is preserved.
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

**Accepted architecture (2026-09-02):** funnel inventory for writer W2 / R31.
**W2 exit checklist (2026-09-29):** the `CONDITIONAL`/`UNKNOWN` rows below are
tracked row by row in [`docs/X1-CRITICAL-PATH.md`](./X1-CRITICAL-PATH.md) §4;
that checklist, not a new inventory, decides when G4 may start.
This file does not close R3. X1 closure architecture:
[`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md).
The reconstructed `up → pub → HEAD → fs` protocol, HEAD-publisher inventory,
consistency map, and coordinator-boundary recommendation live in
[`docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md)
(PC-0). That document does not close W2/R31. PC-1 (2026-09-11) added the
`PublicationCoordinator` skeleton and common types in `internal/publication`
without migrating any funnel or changing runtime. PC-D1 (2026-09-12) resolves
the R3 work-set responsibility: a **certified baseline frontier** owns
inherited continuity. `WorkSetScopeNewlyLive` remains the incremental shape,
admissible only with a valid durable witness; otherwise full baseline
certification is required. Classified input is not publishable; `BORROWED`
must acquire durable own liveness first. The coordinator kernel is a partial
order for block-bearing publication: stage, then durable repair before HEAD;
readiness is optional and also precedes HEAD when present, with funnel-specific
repair/readiness order.
For baseline certification, each dependency follows the GC-aware order
`resolve/capture exact physical incarnation P → establish non-expiring
current-library liveness → revalidate exact P + GC authority`; a bounded-TTL
`up:`/`pub:` pin may bridge certification but cannot justify the witness, and a
late liveness write cannot revoke destructive authority already won by a
GC zero-proof. Legacy deterministic locators must be rematerialized to minted,
never-reused P before certification.

The separate PC-D1B metadata-identity authority prerequisite for baseline
certification is recorded in
[PC-D1B-METADATA-IDENTITY-AUTHORITY.md](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md).
It does not replace the exact-P / GC-authority handshake or authorize a
witness for historical identities without provenance.

**Characterization baseline:** `c0da425a4` (`main` containing #194 and #196)
**R3a structural-refinement parent:** `9386dad` (#197 merged)
**Scope:** characterization plus internal provenance refinement; no protocol/readiness/I/O behavior change
**Status:** X1 remains open. R3 remains OPEN. `GC_ENABLED=false`.

The real-Cassandra evidence gate is `SESAMEFS_REQUIRE_R3_CHARACTERIZATION=1`.
The default full integration command of both Docker services sets it only for
that command. `TestMain` requires all three race legs (writer wins, canonical
deleting fence, and orphan-only fence) to complete after `m.Run()`. A missing
stack or a skip therefore cannot report green in the full gate, while a caller
that replaces the command with a directed `-run` does not inherit an unrelated
R3 requirement. A directed R3 run opts in explicitly with
`-e SESAMEFS_REQUIRE_R3_CHARACTERIZATION=1`.

## Question and vocabulary

R3 asks whether a writer can observe or create usable block bytes, lose every
reference that keeps that physical incarnation alive, and later publish a new
reference after GC has already obtained destructive authority.

The classification unit is a **block provenance inside a funnel**, not the
endpoint. One request can contain a block materialized by this writer, a block
pinned by an earlier request, and a block borrowed from a foreign `fs:` row.

- `PROVEN_CONTINUOUS`: the cited chain proves no zero-own-liveness state in the
  characterized interval.
- `CONDITIONAL`: continuity depends on an unproven TTL, request, retry, or host
  premise.
- `UNGUARDED`: a concrete interleaving reaches zero references before `pub:`.
- `UNKNOWN`: the current code/evidence is insufficient to classify it.

`PROVEN_CONTINUOUS` is deliberately narrow. Proving the materialization
handshake does not automatically prove a whole endpoint through HEAD.

## Proven primitive handshake

`FSHelper.RegisterUploadedBlockTarget` establishes this interval:

```text
AddProvisionalBlockReferenceWithExpiry(up:)
  -> BlockDeleteFenceActive
  -> InstallBlockMetadata OR RepairBlockMetadataIfCurrent
  -> success leaves up: to expire by TTL
```

The ordering and two-sided exclusion of this handshake are proven. Liveness
continuity through metadata remains `CONDITIONAL`: the code does not impose a
hard upper bound below the 48-hour `up:` TTL, so `up -> fence clear -> stall
past TTL -> metadata` is not excluded. Unit contracts pin call order and reject
cleanup of the successful `up:`. `TestR3WriterGCHandshakeAtRealCassandra`
characterizes both races:

```text
writer up -> GC claim -> EACH_QUORUM refs=true -> GC releases
```

and two GC-first variants whose `up:` is created by the productive writer call:

```text
GC claim -> EACH_QUORUM refs=false
  -> writer RegisterUploadedBlockTarget writes exact up:
  -> canonical gc_state=deleting fence -> writer rejects before metadata

GC claim -> EACH_QUORUM refs=false -> irreversible orphan handoff
  -> finalize removes canonical blocks row
  -> assert canonical absent + exact orphan present
  -> writer RegisterUploadedBlockTarget writes exact up:
  -> orphan-only fence -> writer rejects before metadata
```

The evidence stops at the authority decision. It does not repeat #194's
physical S3-delete tests. A separate runtime contract makes the active-fence
branch fatal if repair, representation resolution, or install metadata is
reached.

## Provenance inventory

The table preserves characterization-era evidence and classifications, not
today's W2 exit states. For the current individual re-audit, W2-1/2/6a have
proved covered pre-HEAD continuity under the first-production contract and
remain OPEN through R31 W2-11..14. Other funnel rows are unchanged; consult
[X1 §4](./X1-CRITICAL-PATH.md#4-w2-exit-checklist) for current closure states.

**Historical PR #238 cross-audit caveat (superseded for covered W2-0 by #239/#241 and the reconciliation above):** historical pre-HEAD closure language in other
rows does not prove survival of TTL-bound own references through HEAD.
The shared exact-P ordering proof assumes a live pin at the GC zero-proof read;
W2-0 tracks that unresolved premise and W2-6a remains OPEN. The other funnels
require a separate continuity audit; their runtime is unchanged here.

| Funnel / provenance | Liveness owner and TTL | Post-pin fence | `pub:` handoff | Concrete interleaving / missing premise | Evidence | Result |
|---|---|---|---|---|---|---|
| Materialization primitive, fresh target | Exact `up:<operation>`, 48h | `BlockDeleteFenceActive`, then single-use install | Outside this primitive | Active deleting/orphan returns before install; however `up -> fence clear -> stall >48h -> up expires -> install` is not excluded | `RegisterUploadedBlockTarget`; unit order/mutation contracts; real Cassandra race | `CONDITIONAL`; ordering and active-GC exclusion proven, TTL continuity unproven |
| Materialization primitive, canonical reuse/repair | Exact `up:<operation>`, 48h | Same fence, then tuple-bound repair | Outside this primitive | Active deleting/orphan returns before repair; however `up -> fence clear -> stall >48h -> up expires -> repair` is not excluded | Same contracts and race evidence | `CONDITIONAL`; ordering and active-GC exclusion proven, TTL continuity unproven |
| v2 stored upload, block materialized in the finalize request | Own 48h `up:` | Handshake fence proven by primitive | `stagePendingPublishedFiles` writes `pub:` | No explicit type/evidence binds the earlier successful register to staging; total request-to-stage duration is not an R3 contract | `fs_helpers.go` register and stage call chains; `TestW2UploadFileExactPlacementBeforeHead` | `CONDITIONAL` through stage; pre-HEAD W2 criterion closed (W2-6, PR #237): the materialized placement is re-validated after `pub:` and immediately before HEAD, so a stall past the `up:` TTL is rejected; post-HEAD is R31 |
| v2 stored upload, reusable canonical target | Own 48h `up:` is still written by `RegisterUploadedBlockTargetAndMapping` after the reusable probe | Handshake fence proven by primitive; reusable target follows the repair path | `stagePendingPublishedFiles` writes `pub:` | `Reusable -> own up -> fence -> repair -> stage pub`; continuity still depends on the request reaching stage before its own pin expires | `UploadFile` phased materialization and unconditional registration callback; `TestW2UploadFileExactPlacementBeforeHead` | `CONDITIONAL` through stage; pre-HEAD W2 criterion closed (W2-6, PR #237) by the same final exact-P revalidation of the reused canonical placement; post-HEAD is R31 |
| v2 `CreateFile`, Office-template block (fresh or reused target) | Own per-attempt 48h `up:<uuid>` registered after materialization; empty files have no block | Actual materialized SHA-256/class/key is passed to the existing `validateCommitBlockPublicationFences` / `db.ValidateBorrowedFSPublicationAuthority` at LOCAL_QUORUM immediately before HEAD | `stagePendingPublishedFiles` writes durable `pub:` before the final exact-P gate | A lapsed `up:` does not authorize publication against condemned or retired P: 409, HEAD unchanged, `pub:` cleaned, D unrevoked / terminal P absent. Empty CreateFile stays blockless | W2-6a `TestW2CreateFileOfficeTemplateExactPlacementBeforeHead`: writerFirst (fresh + reuse), committed D and full physical retirement for each `.docx/.xlsx/.pptx`; empty-file control; scoped mutations/evidence gate | `OPEN` (W2-6a): partial exact-P fix; 35d pub: may expire before HEAD and validator-to-HEAD continuity is unproven; post-HEAD R31 remains OPEN |
| SeafHTTP single-shot, materialized block | Own `up:` created by register; 48h | E1-5a retains original SHA-256/class/key through retries to exact-P after durable repair | File finalize stages `pub:` before repair and HEAD | COMMITTED/TERMINAL rejection and live-P retry are measured with explicit expiry-state control; post-HEAD/R31 OPEN | [E1-5a evidence](./E1-5A-SEAFHTTP-SINGLE-PUBLICATION-SAFETY.md) | `CLOSED-FIX` measured single-shot pre-HEAD; broader W2-7 OPEN |
| SeafHTTP streaming finalize, materialized blocks | Own up: created by register; 48h | E1-5b retains original per-position SHA-256/class/key across tracker/HEAD retries; bounded final exact-P after durable repair | stage pub: before repair and HEAD | COMMITTED/TERMINAL rejection; same-tracker rematerializes rejected victim only; missing/contradictory state fails closed; natural expiry/post-HEAD/R31 OPEN | [E1-5b evidence](./E1-5B-SEAFHTTP-STREAMING-PUBLICATION-SAFETY.md) | CLOSED-FIX measured streaming pre-HEAD and selective same-process retry; broader W2-7 OPEN |
| OnlyOffice callback, downloaded/materialized block | Own callback operation `up:`, 48h | E1-4 carries original materialized SHA-256/class/key to shared final exact-P after durable repair | `stagePendingPublishedFiles` before repair and HEAD | COMMITTED/TERMINAL P1 rejection and P2 replay are proved with explicit expiry-state control; post-HEAD R31 remains OPEN | [E1-4 callback evidence](./E1-4-ONLYOFFICE-PUBLICATION-SAFETY.md) | `CLOSED-FIX` measured pre-HEAD contract; broader W2-8 OPEN |
| Sync `PutBlock` followed by HEAD | Observed deterministic `up:sync:<repo>:<sha256>` alone scopes renewal, never fabricated. Existing LOCAL_QUORUM fast path and EACH_QUORUM fallback remain | W2-4 captures every added block's exact placement, including absent/expired provenance, and revalidates all captured placements after repair | `pub:` -> readiness -> durable per-file repair -> final exact-P -> HEAD; positive settlement promotes `fs:` | A late repair cannot revoke D. Expired provenance no longer skips exact-P, but independent W2-3 expiry/retry evidence and post-HEAD R31 requirements remain separate | `prepareSyncCommitBlockPublicationReadiness`, direct/auto-merge final checks; existing PutBlock and cross-DC evidence plus W2-4 no-PutBlock safety legs | `CONDITIONAL`; W2-3 remains OPEN, R31 remains |
| Sync retry from another pod within the same provisional TTL | Same deterministic `up:` when observable and renewed by the retrying pod | Every retry re-validates exact placement before HEAD | Attempt-local `pub:` plus a durable repair row shared by target `commit_id` | A readiness failure creates no durable repair row. After readiness succeeds and the shared row is queued, queue ambiguity, ambiguous-CAS, and divergent-CAS request-local outcomes retain it; only successful settlement clears it. Expired provenance remains unproven and is R31 follow-up | Sync retry/finalize call chain and `handleSyncHeadPromotion` ownership contract | `CONDITIONAL`; scoped PutBlock-provenanced pre-HEAD evidence, R31 remains |
| Sync commit whose block had no associated PutBlock | No Sync `up:` is fabricated; real web-upload + `CheckBlocks` dedup under SHA-1/SHA-256 supplies bytes | Every added canonical block's exact placement is captured before repair and revalidated after durable acquisition | Attempt-local `pub:` precedes readiness; non-expiring per-file repair follows readiness and precedes final exact-P/HEAD | If GC wins first, deleting/changed/retired P rejects with HEAD unchanged. If repair wins first, destructive EACH_QUORUM cannot prove zero even after temporary refs expire. Shared direct repair survives final rejection; unique auto-merge repair is cleaned. | Ten required real Cassandra/MinIO safety legs, RED on PR #244; direct and auto-merge successful controls; see [investigation/fix](./W2-4-SYNC-NO-PUTBLOCK.md) | `CLOSED-FIX` pre-HEAD; post-HEAD R31 remains OPEN |
| `recv-fs-before-put` | RecvFS creates SHA-1 metadata, no own pin; canonical P may already exist | No materialization fence at RecvFS; actual PutBlock materializes/reuses with own pin | PutCommit object is distinct from HEAD; existing repair/final exact-P settles ordered direct publication | E1-7 fresh/reuse, COMMITTED/TERMINAL continuation and early-HEAD controls are GREEN; broader order/auto-merge/post-HEAD remain open | `RecvFS`; `TestE17SyncRecvFSBeforePutBlock`; [E1-7](./E1-7-SYNC-RECVFS-BEFORE-PUTBLOCK.md) | Ordered subset `CLOSED-EVIDENCE`; overall `UNKNOWN`/OPEN |
| `CreateFileFromBlocks`, exact session `up:` | Session-owned `up:`, renewed when present or recreated if lapsed through the same bounded `ensureCommitBlockOwnLiveness` step used by BorrowedFS | Exact observed placement re-validated for every distinct ready block with `db.ValidateBorrowedFSPublicationAuthority` at LOCAL_QUORUM immediately before HEAD | `pub:` is staged before the final gate | A late renewal does not revoke a delete already committed against the old placement; the final exact-P check rejects blocked, changed, or fully retired placement. Continuous `up: -> pub:` overlap remains unproven and is R31 | `commitBlockPlacement`, `ensureCommitBlockOwnLiveness`, `validateCommitBlockPublicationFences`, six-leg Docker evidence | `CONDITIONAL`; SessionUpload parity is proven through pre-HEAD for this funnel, R31 remains |
| `CreateFileFromBlocks`, foreign `fs:` reuse | Foreign permanent ref, upgraded to own `up:<session>` before session claim | Bounded exact-placement re-validation (`db.ValidateBorrowedFSPublicationAuthority`, `BlockAuthorityAdvisory`/LOCAL_QUORUM -- safety comes from the own pin already being durable, not from a downstream CAS) immediately before HEAD | `stagePendingPublishedFiles` writes `pub:` after the own pin | `foreign fs observed -> own up -> foreign fs removed -> GC claim`: EACH_QUORUM sees `up`; if D is already committed, or if GC has already fully retired the placement (Finalize + settled orphan, which a bare fence check cannot see), the pre-HEAD check returns `ErrBlockDeleteInProgress` and HEAD does not occur | W1 eight-leg real Cassandra+MinIO evidence and unit contracts | `CONDITIONAL`; W1 proven through HEAD, R31 remains |
| Dedup via foreign `fs:` with W1 own liveness | Own `up:<session>` is established once per distinct borrowed SHA-256 | Same bounded final exact-placement check over the shared placement type | Later attempt-local `pub:` | Retries reuse the same `up:<session>` key; foreign `pub:` never supplies initial liveness | `CreateFileFromBlocks` provenance handoff and W1/W2 integration evidence | `CONDITIONAL`; no liveness gap through pre-HEAD for this funnel |
| Cross-repo copy/move | Source fs: borrowed; destination pub: plus non-expiring durable repair | E1-09 captures each exact source P per attempt, verifies staged identities and final exact-P after repair | Destination pub: -> repair -> final exact-P -> HEAD -> fs: | Productive source library purge can release source fs: before destination protection; COMMITTED/TERMINAL RED -> rejection. Repair-first guards GC even after actual pub: expiry | `copyFSObjectToLibraryForPublish`, `processSingleItem`, [E1-09](./E1-09-CROSS-REPO-PUBLICATION-SAFETY.md) | CLOSED-FIX measured single-file/block plaintext same-org/representation pre-HEAD; broader and R31 OPEN |
| Repair after HEAD is visible | Existing `pub:` plus durable pending publication record, when present; unresolved visits can write/refresh per-row `pub:<repo:commit:fsID>` after classify while pending (`ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01`) — that can recreate an expired pin. E1-11 proves the matching non-expiring current-version repair still vetoes a new D at EACH_QUORUM during that zero-real-ref interval; the worker best-effort removes that identity (concurrent renewal may leave TTL-bounded `pub:`; `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01`) | Not a pre-HEAD materialization decision | The shared repair classifier promotes permanent `fs:` only after bounded positive evidence from a durable SERIAL HEAD anchor (`EACH_QUORUM` ancestry, 1024/chunk/30s, resumable cursor of the next unread commit; clean genesis is persisted as exhausted before re-observing HEAD and may re-anchor with a second 1024-node chunk in the same context); every inconclusive or unsupported-negative result retains `pub:` and the repair; a missing repair row is a no-op | R31-C1 closes moving-HEAD non-convergence, pre-HEAD anchor poisoning, and partial-progress loss under the 30s/EQ budget for this worker. E1-11 separately closes covered current-version pre-D renewal-after-classify safety using the durable repair guard, without gap-free `pub:` renewal. Known-loser durability, discovery scale, `pub:` zero-ref projection and per-repair Paxos isolation remain open (`ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01`) | pending published files and Sync repair paths; see [ISSUE-PUBLISH-REPAIR-TIMEOUT-CLEANUP-01](KNOWN_ISSUES.md#issue-publish-repair-timeout-cleanup-01), [ISSUE-PUBLISH-REPAIR-REACHABILITY-01](KNOWN_ISSUES.md#issue-publish-repair-reachability-01), [ISSUE-PUBLISH-REPAIR-REACHABILITY-CONVERGENCE-01](KNOWN_ISSUES.md#issue-publish-repair-reachability-convergence-01), [ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01](KNOWN_ISSUES.md#issue-publish-repair-owned-pub-cleanup-race-01), [ISSUE-PUBLISH-REPAIR-RENEWAL-AFTER-CLASSIFY-01](KNOWN_ISSUES.md#issue-publish-repair-renewal-after-classify-01), [ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01](KNOWN_ISSUES.md#issue-publish-repair-progress-paxos-domain-01) | `CONDITIONAL`; R31-A/C1 classifier+convergence closed for this worker, overall R31 open |

Same-repo copy/move is not listed as a block-publication funnel: the current
path does not stage a new `pub:` dependency for those operations. Same-repo move
follows `processSameRepoMove`; same-repo copy reuses the existing
content-addressed fs_object/block-reference ownership. Any race in fs_object
retention for those operations is a separate question and is not classified as
R3 publication continuity here.

`RevertFile`, `RevertDirectory`, `RestoreTrashItem`, and `RevertDirents` add
historical fs_objects to a HEAD that did not depend on them, a positive logical
block delta with borrowed provenance. They still acquire no own up:/pub:/repair.
E1-10e disproves metadata existence as a permanent-liveness witness: real
PutCommit/RecvFS metadata can coexist with COMMITTED/TERMINAL retired P1 before
any Sync HEAD publication. RevertDirents non-directory file admission now requires
settled historical fs: via LQ→EQ fallback, captures original exact P and rechecks
both before every HEAD CAS. That measured unpublished-source subset is
CLOSED-FIX; the other handlers and directory shapes have no equivalent guard.
Continuous own liveness through HEAD, broader batches, retention/Phase5/6 and
R31 remain OPEN; W2-10 overall is OPEN. See
[E1-10e evidence](./E1-10E-W210-RESIDUAL-DISPOSITION.md) and
`ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`.

The table intentionally records `UNKNOWN` where a source walk has not proved a
temporal premise. This PR does not turn those rows green by assumption.

BorrowedFS through HEAD was first measured as an unguarded characterization in
[R3-BORROWEDFS-HEAD-CHARACTERIZATION.md](R3-BORROWEDFS-HEAD-CHARACTERIZATION.md).
W1 now productizes that handshake: the writer records own `up:<session>` for
each distinct BorrowedFS block, then re-validates the exact observed physical
placement immediately before HEAD (`db.ValidateBorrowedFSPublicationAuthority`
-- not a bare fence check, since a fence-only check cannot see a block GC has
already fully retired), and aborts before HEAD/fs promotion when that check
rejects the placement. W1 does not close the broader `up -> pub -> HEAD -> fs`
crash/reconciliation interval tracked by R31/W2.

## Logical positive block delta

The logical set operation is:

```text
LogicalPositiveBlockDelta =
  UniqueCanonicalSHA256(ReachableBlocks(new HEAD))
  - UniqueCanonicalSHA256(ReachableBlocks(old HEAD))
```

It is **not** defined as the complete R3 work set. PC-D1 resolves the ownership
boundary with a certified baseline frontier: `LogicalPositiveBlockDelta` is
admissible incrementally only when a valid witness certifies the current HEAD.
Absent or stale evidence fails closed to full baseline certification. Test-only vectors cover overlap, reorder, duplicates,
metadata-only changes, and external IDs resolving to one canonical SHA-256.
Production sync staging is unchanged.

## Hot-path performance contract

The baseline is structural and deliberately does not claim to count physical
network RTTs:

```text
normal materialize -> publish
static reachable CQL callsite budget remains at its recorded baseline
known-loop authorized staging sink remains one call per guarded loop element
unlisted direct database calls at guarded stage -> HEAD boundaries = 0
pre-acquired session Query/Bind entry points (including Bind of a Query created before stage, and Query whose text is a literal, constant, variable, or builder) and local db aliases in those boundaries = 0
canonical/orphan authority reads added = 0
```

W2-6 (PR #237) activates the already-reachable
`validateCommitBlockPublicationFences` seam for `UploadFile` by passing its
materialized placement instead of `nil`: no new CQL source callsite and no
static budget change. Declared runtime cost: two `LOCAL_QUORUM` point reads
(canonical `blocks` row, then `gc_s3_orphans`) per finalize attempt, the
same per-block cost `CreateFileFromBlocks` already pays. An uncontended
`UploadFile` pays two reads; each `ErrLibraryHeadConflict` retry repeats
stage, validation and HEAD CAS, bounded by the existing
`uploadMetadataRetryAttempts` (20) finalize limit, so at most 40 reads.

`TestR3PublicationHotPathHasNoPerBlockAuthorityReads` retains the lightweight
local source check. `TestR3PublicationHotPathIsFailClosed` indexes the db, v2,
and API packages and follows direct calls, cross-package selectors,
package-level function aliases, and function literals stored in seams. It
rejects known authority classifiers and resolvable canonical `blocks` or
`gc_s3_orphans` SELECTs, including qualified/quoted CQL. An unknown called
function seam fails closed; a seam with an explicit alias is followed.

`TestR3PublicationHotPathTypedReceiversAndCQLBudget` adds explicit receiver-type
resolution for indexed struct fields and methods (for example `h.db.Method`),
follows local aliases of directly resolvable functions, and fails closed for a
local method value on an indexed receiver when it cannot resolve the target.
It freezes the number of structurally reachable CQL source callsites for
every guarded root. `Query` with a statement argument and every `Bind`
(including a single bound value) are CQL source entry points. These are
static source entry points, not physical Cassandra submissions or a proof
of arbitrary runtime multiplicity. Any intentional new CQL
therefore requires explicit review and a baseline update. This is a deliberately
scoped source analyzer, not a claim of universal Go compiler/type analysis.

`TestR3PublicationKnownFanoutIsSinglePass` supplies the narrow multiplicity
contract the static budget cannot provide: it freezes the authorized staging
sink in two known loops (`stagePendingPublishedFilesAddReferencesFn` per
`pendingFiles` element, `addPublishAttemptReferenceFn` per `NormalizeBlockIDs`
element), fail-closes any other named call or nested FuncLit in those loop
bodies, and requires that the allowed helper seams (`PersistFn`, `ResolveFn`,
rollback, `createPendingPublishedFileRow`) do not themselves reach a
publication primitive. That is a freeze of the authorized route in those
loops, not a proof of arbitrary runtime multiplicity or of helpers defined
outside the inspected seams. Companion runtime tests count seam invocations
on the current functions. The scope is intentionally these known loops, not a
generic complexity analyzer.
`TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls` freezes the direct
database surface in the enumerated v2, OnlyOffice, cross-repo copy/move
(`processSingleItem`), SeafHTTP, and sync stage-to-HEAD boundaries. The
interval is after stage through evaluation of HEAD's receiver and arguments
(those run before HEAD is invoked); the outer HEAD call itself is excluded.
Direct access includes `ident.db.Method`, a local alias of that field, a
method value taken from it, `Query` entry points, and `Bind` entry points
regardless of how the session was obtained. `Bind` always consumes the CQL
source-callsite budget because its arguments are bound values, not the
statement. `Query` consumes that budget when the statement cannot be
reconstructed or reconstructs as CQL; resolved text is used only to
classify canonical `blocks` / `gc_s3_orphans` SELECTs. Those authority
SELECTs are rejected even when the CQL source-callsite count is unchanged.
These are lexical CQL entry points, not a count of physically submitted
Cassandra requests. SeafHTTP keeps its existing commit INSERT as the frozen
baseline. The test does not claim to prove every possible interprocedural
path.
`TestR3MaterializationHasNoUnlistedDirectDBCall` similarly prevents a direct
post-metadata database read being appended to `RegisterUploadedBlockTarget`,
including a local `h.db` alias or method value; the established pin/fence/
metadata seams remain its allowed I/O.

The guarded roots include the v2 staging primitive (`AddPublishAttemptReferences`)
and the normal stage/promote/finalize paths. They intentionally exclude
`repairPublishedSyncCommitBlockDelta`: it is post-HEAD R31 convergence, not the
normal pre-HEAD R3 hot path. The mutation suite covers the original thirty-four
characterization mutations plus the two R3a provenance mutations. The
characterization mutations prove red for the seven handshake/cost
regressions plus hidden FuncLit/cross-package/qualified authority reads, typed
receiver and method-value dispatch, an extra per-block CQL INSERT, a
single-argument `Bind` on a typed root, duplicated existing fan-out calls, a
second named wrapper in each known loop, a second publication sink hidden in an
allowed persist seam or nested FuncLit, v2 `h.db`/`fsHelper.db`/session/`db`
alias/method-value and sync stage-to-HEAD reads, Query/Bind whose statement is
a constant or variable, a Bind of a Query acquired before stage, a DB read
inside a HEAD argument, and post-metadata materialization reads including a
local `db` alias. The two R3a mutations prove that the session provenance check
remains an exact referrer comparison and that `classifyBlockOwnership` cannot
acquire an unlisted call.

### Declared exception: Sync PutBlock-provenanced readiness

The zero-added-authority-read baseline above is structural for the guarded
roots it lists. It is **not** the baseline for
`ensureSyncCommitBlockPublicationReadiness` (`internal/api/sync.go`, called
from both `handleSyncHeadPromotion` and `tryAutoMergeSyncHeadPromotion`
before the HEAD CAS), which #206 introduces to close the "Sync `PutBlock`
followed by HEAD" and "Sync retry from another pod" rows above. That
function is deliberately outside `TestR3PublicationHotPathIsFailClosed`'s
roots (which start only from `stageSyncCommitBlockDelta`/
`finalizeSyncCommitBlockDelta`, neither of which calls it) and is only
scanned flatly, not walked into, by
`TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls`'s stage->HEAD span
check. Neither guard therefore observes its cost, and this section exists so
that fact is a documented, reviewed decision rather than a silent gap:

```text
Per PutBlock-provenanced canonical block newly referenced by a commit:
  BlockReferenceExistsLocalQuorum ....... 1 read  (scope gate, same-DC fast path)
  BlockReferenceExistsEachQuorum ........ 0 or 1 read (scope gate cross-DC fallback,
                                           only on a clean local miss -- see below)
  ProbeBlockReuse ........................ up to 3 reads (placement probe)
  AddProvisionalBlockReferenceWithExpiry . 1 read + 1 logged-batch write (renew)
  ValidateBorrowedFSPublicationAuthority . 2 reads (final exact-placement fence)
bounded concurrency per stage: syncCommitBlockPlacementConcurrency (20)
```

This is O(N) in the number of distinct newly-referenced canonical blocks with
existing `up:sync:<repo>:<block>` provenance -- not O(1), and not covered by
the "known-loop authorized staging sink" or "unlisted direct database calls
... = 0" lines above. It is the intentional, reviewed cost of closing those
two `CONDITIONAL` rows, mirroring the cost `CreateFileFromBlocks`/W1 already
pays for its own funnel (`commitBlockPlacement`/
`validateCommitBlockPublicationFences`). Explicitly:

- no new per-block `SERIAL`/Paxos operation;
- **one** deliberate, scoped `EACH_QUORUM` exception:
  `BlockReferenceExistsEachQuorum`, added to close
  `ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01` (a `LOCAL_QUORUM`
  scope-gate miss does not prove PutBlock never happened -- it may only mean
  this datacenter has not yet observed a write acknowledged elsewhere). It
  never runs on the fast path: a local hit or a local read error both settle
  the scope-gate answer first, with zero added WAN. It runs only when the
  local read cleanly reports absent, so its cost is bounded by the clean
  local-miss rate across the blocks the scope gate evaluates -- including
  genuinely-unprovenanced (dedup) blocks, not only provenanced-elsewhere
  ones, since the gate cannot tell the two apart before running the
  fallback, only after -- not charged to every block or every commit. See
  "Cross-DC provenance fallback: cost and availability"
  below for the measured cost of that miss rate, including the genuinely-
  unprovenanced (dedup) case and single-DC-unavailable behavior. No other
  primitive in this exception uses `EACH_QUORUM`, and this remains the only
  `EACH_QUORUM` operation in the Sync PutBlock pre-HEAD hot path;
- every other read/write in the table above is `LOCAL_QUORUM` or
  session-inherited (production runs that session at `LOCAL_QUORUM`) -- see
  `TestValidateBorrowedFSPublicationAuthorityUsesAdvisoryReads` and
  `TestP3FenceReadConsistencyIsLocalQuorum` for the pinned consistency levels
  of the individual reads;
- a block with no existing provenance anywhere (local or global) pays at
  most the scope-gate reads and is left untouched -- the O(N) cost of the
  remaining four primitives applies strictly to the PutBlock-provenanced
  subset, per the funnel's row above;
- every other R3 publication funnel's baseline above is unchanged by this
  exception.

`TestR3SyncPutBlockReadinessDeclaredExceptionIsFrozen`
(`internal/db/r3_sync_putblock_readiness_exception_test.go`) freezes *which*
`internal/db` primitives this root can reach: unlike the zero-tolerance
guards above, it does not fail merely because an authority-shaped read is
reachable from this root -- that is its accepted job -- but it walks the
same type-aware interprocedural graph
`TestR3PublicationHotPathTypedReceiversAndCQLBudget` uses on the
`internal/api`/`internal/api/v2` side (so a call reached only through a
package-level function-variable indirection or a struct-field method value,
the exact shape that let this cost go undeclared in the original PR, cannot
hide from it either) and fails closed the moment it reaches anything outside
the five calls listed above, or an unresolved method on a tracked receiver
type. It stops at the `internal/db` method boundary by design and does not
descend into `BlockReferenceExistsLocalQuorum`,
`BlockReferenceExistsEachQuorum`, `ProbeBlockReuse`,
`AddProvisionalBlockReferenceWithExpiry`, or
`ValidateBorrowedFSPublicationAuthority` themselves -- it cannot see a
consistency level or an internal call count a future change makes *inside*
one of those five functions. Its `SERIAL`/`EACH_QUORUM` identifier check
only covers the walked `internal/api`-side code between this root and that
boundary (it would catch a new raw `gocql.EachQuorum`/`gocql.Serial`
reference added directly to that wrapper code). What each of those five
primitives itself does internally is the job of their own existing,
narrower tests -- `TestValidateBorrowedFSPublicationAuthorityUsesAdvisoryReads`
and `TestP3FenceReadConsistencyIsLocalQuorum` pin advisory/fence read
consistency, `TestBlockReferenceProducersPinWriteConsistency` pins every
`block_references` writer including the one inside
`AddProvisionalBlockReferenceWithExpiry`, and
`TestSyncBlockReferenceCrossDCFallbackConsistencyIsEachQuorum` pins
`BlockReferenceExistsEachQuorum`'s named
`SyncBlockReferenceCrossDCFallbackConsistency` constant to `gocql.EachQuorum`
-- plus the deployed session default (`LOCAL_QUORUM`, confirmed at
connection time in the runtime config log). Adding, removing, or strengthening a
reachable call requires updating this new test's allow-list and this
section in the same change; changing what one of the five primitives does
internally is caught by that primitive's own existing tests, not by this
one.

### Cross-DC provenance fallback: cost and availability

`ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01` is closed for the
scope-gate decision by the `BlockReferenceExistsEachQuorum` fallback above.
The quorum-intersection argument is identical to `BlockHasReferencesGlobal`'s
(the existing GC destructive-authorization read): a write acknowledged at
`LOCAL_QUORUM` in datacenter D reaches a quorum of D's own replicas, and an
`EACH_QUORUM` read -- which requires a quorum in *every* datacenter holding
replicas -- necessarily intersects that write in D once it has landed,
independent of whether async cross-DC replication has converged yet. It is
deliberately the exact-referrer analog of that primitive, not a reuse of it:
`BlockHasReferencesGlobal` answers "does *any* reference exist", which an
unrelated `fs:` or foreign `up:` row would satisfy without proving this
specific PutBlock happened. The same `NetworkTopologyStrategy`-with-every-
replica-holding-DC assumption applies, and a topology that does not satisfy
it degrades this decision to, at worst, the same "left untouched" residual
already accepted for a block with no local provenance -- unlike GC's
destructive path, a wrong answer here cannot authorize an irreversible
action.

This does **not** change the "no per-block `EACH_QUORUM`" character of the
*fast* path: a local hit or a local read error both settle the answer before
the fallback is ever reached. What it does add is a WAN round trip on every
*clean local miss* for a provenanced block, and the same round trip is paid
-- without ever finding anything -- for a block with no PutBlock provenance
at all (client-side dedup via `CheckBlocks`, or reuse from another commit),
since a local miss is observationally identical between "genuinely
unprovenanced" and "provenanced but not yet locally visible" until the
fallback resolves it. Measured cost/availability characterization across
local-hit, cross-DC-hit, genuinely-unprovenanced, mixed, and single-DC-down
scenarios -- including a real 3-DC all-cross-DC-hit measurement on a real
3-DC Cassandra fixture, not only the single-DC dev-stack numbers -- lives
in `docs/KNOWN_ISSUES.md`, along with the explicit decision and the analysis
of why genuinely-unprovenanced blocks cannot be classified without either
this fallback's cross-DC dependency or a wider redesign. A local read error
and a fallback read error are both treated identically: fail closed, never
interpreted as absence.

The fan-out that calls this scope gate once per distinct block
(`syncCommitProvenancedBlockIDs`) is bounded fail-fast: it uses
`errgroup.WithContext` so the first fatal error (local or global) stops
admitting additional block-level provenance checks -- later blocks'
goroutines still get created, they just return immediately via the
cancelled context instead of making their own external call -- capping the
blast radius of a degraded/unreachable datacenter at at most
`syncCommitBlockPlacementConcurrency` (20) block-level provenance checks
admitted/in flight per commit, not a cap on total database reads: a check
already past its own cancellation gate is not cancelled mid-flight, and each
admitted check may itself issue both a `LOCAL_QUORUM` and an `EACH_QUORUM`
read.

## Explicit block-commit provenance

The file-from-blocks classifier preserves three internal outcomes from the one
`ListBlockReferrers` partition read:

```text
SessionUpload  exact up:<session> for this commit
BorrowedFS     any fs: prefix with no exact session up:
None           no accepted reference
```

`SessionUpload` takes precedence over `BorrowedFS` regardless of referrer row
order. The current readiness policy deliberately maps both `SessionUpload` and
`BorrowedFS` to ready, while `None` remains `needs_upload`. `/blocks/check` and
the commit path use that same policy, so this refinement changes no HTTP result,
deduplication behavior, or storage I/O. `BorrowedFS` remains the borrowed,
unguarded R3 provenance; the race where its last foreign reference disappears
before this writer stages `pub:` is still open and is not resolved here.

## Outcome and next steps

This structural refinement changes no protocol behavior. A later PR must select
one provenance category, establish its exact continuity or slow-path protocol,
and retain both zero-added-CQL-callsite and zero-added-authority-read contracts
for normal materialize-to-publish traffic. HEAD after a BorrowedFS cut is
characterized in
[R3-BORROWEDFS-HEAD-CHARACTERIZATION.md](R3-BORROWEDFS-HEAD-CHARACTERIZATION.md);
that measurement does not close R3.


### G4 evidence alignment (2026-10-02)

The third required current leg is retired_tuple_rejected, replacing the
historical orphan_fence expectation: P1 retirement leaves exact D1 recovery
authority, a stale P1 repair cannot recreate the row, and the orphan does not
fence a fresh P2 writer. The writer-wins and active deleting-claim legs remain
required. This alignment does not close R3 or permit GC activation.
