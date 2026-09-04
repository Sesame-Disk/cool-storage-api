# R3 liveness-continuity characterization

**Accepted architecture (2026-09-02):** funnel inventory for writer W2 / R31.
This file does not close R3. X1 closure architecture:
[`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md).

**Characterization baseline:** `c0da425a4` (`main` containing #194 and #196)
**R3a structural-refinement parent:** `9386dad` (#197 merged)
**Scope:** characterization plus internal provenance refinement; no protocol/readiness/I/O behavior change
**Status:** X1 remains open. R3 remains OPEN. `GC_ENABLED=false`.

**W2a authority overlay (2026-09-03):** publication authority is now explicit
and separate from this continuity characterization. `libraries.publication_state`
uses the same global `SERIAL` domain as HEAD: HEAD CAS operations require
`ACTIVE`, and hard-delete commits `ACTIVE -> TERMINAL` before removing canonical
rows. Hard-delete also writes the no-TTL
`library_publication_revocations` witness at `EACH_QUORUM`, so repair can prove
terminality after the library row is gone. Absence, lease expiry, timeout, and
unknown or unavailable state do not authorize cleanup; repair retains `pub:` and
its durable row until terminal authority is proven. This closes the W2 authority
gap, not the `up -> pub -> HEAD -> fs` continuity gap tracked by R31.

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

| Funnel / provenance | Liveness owner and TTL | Post-pin fence | `pub:` handoff | Concrete interleaving / missing premise | Evidence | Result |
|---|---|---|---|---|---|---|
| Materialization primitive, fresh target | Exact `up:<operation>`, 48h | `BlockDeleteFenceActive`, then single-use install | Outside this primitive | Active deleting/orphan returns before install; however `up -> fence clear -> stall >48h -> up expires -> install` is not excluded | `RegisterUploadedBlockTarget`; unit order/mutation contracts; real Cassandra race | `CONDITIONAL`; ordering and active-GC exclusion proven, TTL continuity unproven |
| Materialization primitive, canonical reuse/repair | Exact `up:<operation>`, 48h | Same fence, then tuple-bound repair | Outside this primitive | Active deleting/orphan returns before repair; however `up -> fence clear -> stall >48h -> up expires -> repair` is not excluded | Same contracts and race evidence | `CONDITIONAL`; ordering and active-GC exclusion proven, TTL continuity unproven |
| v2 stored upload, block materialized in the finalize request | Own 48h `up:` | Handshake fence proven by primitive | `stagePendingPublishedFiles` writes `pub:` | No explicit type/evidence binds the earlier successful register to staging; total request-to-stage duration is not an R3 contract | `fs_helpers.go` register and stage call chains | `CONDITIONAL` |
| v2 stored upload, reusable canonical target | Own 48h `up:` is still written by `RegisterUploadedBlockTargetAndMapping` after the reusable probe | Handshake fence proven by primitive; reusable target follows the repair path | `stagePendingPublishedFiles` writes `pub:` | `Reusable -> own up -> fence -> repair -> stage pub`; continuity still depends on the request reaching stage before its own pin expires | `UploadFile` phased materialization and unconditional registration callback | `CONDITIONAL` |
| SeafHTTP normal/streaming finalize, materialized block | Own `up:` created by register; 48h | Handshake fence proven by primitive | File finalize later stages `pub:` | Materialization and publication share the finalize call, but no explicit bounded-continuity contract connects every block result to stage | `finalizeUploadStreaming`, `RegisterUploadedBlockTarget`, filesystem update | `CONDITIONAL` |
| OnlyOffice callback, downloaded/materialized block | Own callback operation `up:`, 48h | Handshake fence proven by primitive | `stagePendingPublishedFiles` before HEAD | Same-request ordering is visible, but the full duration/continuity premise is not encoded | `saveOnlyOfficePendingBlock`, callback staging | `CONDITIONAL` |
| Sync `PutBlock` followed by HEAD | Deterministic `up:sync:<repo>:<sha256>`, 48h from the latest successful PutBlock | Handshake fence proven inside PutBlock materialization | A separate request stages a fresh `pub:` | Process/pod/restart do not remove Cassandra `up:`, but an unbounded delay can cross TTL; HEAD carries no proof of the PutBlock result | `PutBlock`, `syncBlockUploadOperationID`, `stageSyncCommitBlockDelta` | `CONDITIONAL` |
| Sync retry from another pod within the same provisional TTL | Same durable deterministic `up:` | Original PutBlock performed it | Retry stages a new attempt-local `pub:` | Cross-pod is not itself a gap; remaining TTL and association with this commit are unproven at HEAD | sync retry/finalize call chain | `CONDITIONAL` |
| Sync commit whose block had no associated PutBlock | None proven for this commit | None attributable | Staging may resolve IDs and write `pub:` | The commit graph does not prove that this writer ever held pre-publication liveness | `RecvFS`, commit delta builder and staging | `UNKNOWN` |
| `recv-fs-before-put` | FS object may arrive before bytes/metadata; no own upload pin yet | No materialization fence at RecvFS | Publication and later PutBlock are separate protocol events | Exact ordering between commit publication, mapping resolution, and later PutBlock needs a focused protocol trace; endpoint success alone is not liveness proof | `RecvFS`; `TestSyncRecvFSBeforePutBlockPublishesDownloadableFile` | `UNKNOWN` |
| `CreateFileFromBlocks`, exact session `up:` | Session-owned `up:`, renewed (or recreated, if it already lapsed) via the same `ensureCommitBlockOwnLiveness` pin/renew step BorrowedFS uses, before session claim | Bounded exact-placement re-validation (`db.ValidateBorrowedFSPublicationAuthority`, LOCAL_QUORUM), now covering SessionUpload as well as BorrowedFS | `pub:` is staged later | `up: near-expiry -> GC claim -> zero-proof -> commit D -> late renewal (does not revoke D) -> pre-HEAD check rejects`; renewal does not prevent the TTL from lapsing before pub: is staged -- what it removes is the BorrowedFS-only gap where a lapsed/absent `up:` left nothing re-validating a SessionUpload block at all. Safety through HEAD is the pre-HEAD check rejecting a post-D placement, not renewal keeping liveness continuously unbroken; continuous `up: -> pub:` overlap is not proven and remains an R31 property, matching W1's BorrowedFS argument (rows below) | `TestSessionUploadOwnLiveness` (six named legs) and W1's shared `db.ValidateBorrowedFSPublicationAuthority` proof | `CONDITIONAL`; W1-equivalent proven through HEAD for this funnel, R31 remains |
| `CreateFileFromBlocks`, foreign `fs:` reuse | Foreign permanent ref, upgraded to own `up:<session>` before session claim | Bounded exact-placement re-validation (`db.ValidateBorrowedFSPublicationAuthority`, `BlockAuthorityAdvisory`/LOCAL_QUORUM -- safety comes from the own pin already being durable, not from a downstream CAS) immediately before HEAD | `stagePendingPublishedFiles` writes `pub:` after the own pin | `foreign fs observed -> own up -> foreign fs removed -> GC claim`: EACH_QUORUM sees `up`; if D is already committed, or if GC has already fully retired the placement (Finalize + settled orphan, which a bare fence check cannot see), the pre-HEAD check returns `ErrBlockDeleteInProgress` and HEAD does not occur | W1 eight-leg real Cassandra+MinIO evidence and unit contracts | `CONDITIONAL`; W1 proven through HEAD, R31 remains |
| Dedup via foreign `fs:` with W1 own liveness | Own `up:<session>` is established once per distinct borrowed SHA-256 | Same bounded final exact-placement check, only for BorrowedFS | Later attempt-local `pub:` | Retries reuse the same `up:<session>` key; foreign `pub:` never supplies initial liveness | `CreateFileFromBlocks` provenance handoff and W1 integration evidence | `CONDITIONAL`; no liveness gap through HEAD |
| Cross-repo copy/move | Source-repo `fs:` borrowed by destination writer | No destination-owned post-pin fence | Destination `pub:` | Concurrent source deletion/retention GC can remove the borrowed ref; no cross-repo lease is demonstrated | `copyFSObjectToLibraryForPublish`, `processSingleItem` | `UNKNOWN` |
| Repair after HEAD is visible | Existing `pub:` plus durable pending publication record, when present | Not a pre-HEAD materialization decision | Promotes permanent `fs:` | This is R31 convergence, not evidence that the original pre-HEAD R3 interval was continuous | pending published files and sync repair paths | `CONDITIONAL` / separate R31 concern |

Same-repo copy/move is not listed as a block-publication funnel: the current
path does not stage a new `pub:` dependency for those operations. Same-repo move
follows `processSameRepoMove`; same-repo copy reuses the existing
content-addressed fs_object/block-reference ownership. Any race in fs_object
retention for those operations is a separate question and is not classified as
R3 publication continuity here.

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

It is **not** defined as the complete R3 work set. Future work must also consider
dependencies inherited from the old HEAD whose liveness continuity is absent or
inconclusive. Test-only vectors cover overlap, reorder, duplicates,
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

W2a preserves the normal materialize-to-publish hot path: it adds no canonical
orphan authority read and no per-block authority read. The structural budget for
`stagePendingPublishedFiles` is 18 callsites rather than the previous 17 because
its reachable missing-library repair path performs the durable
`library_publication_revocations` witness lookup. That is not a per-block
authority read and not the legacy-`NULL` `publication_state` CAS fallback;
migrated live rows take only the guarded `ACTIVE` CAS. This budget entry is not a
runtime claim of one extra request per block.

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

This structural refinement and the W2a authority overlay do not close R3. A later PR must select
one provenance category, establish its exact continuity or slow-path protocol,
and retain both zero-added-CQL-callsite and zero-added-authority-read contracts
for normal materialize-to-publish traffic. HEAD after a BorrowedFS cut is
characterized in
[R3-BORROWEDFS-HEAD-CHARACTERIZATION.md](R3-BORROWEDFS-HEAD-CHARACTERIZATION.md);
that measurement does not close R3.
