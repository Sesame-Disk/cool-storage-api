# R3 liveness-continuity characterization

**Baseline parent:** `c0da425a4` (`main` containing #194 and #196)
**Scope:** characterization only; no publication behavior changes
**Status:** X1 remains open. R3 remains OPEN. `GC_ENABLED=false`.

The real-Cassandra evidence gate is `SESAMEFS_REQUIRE_R3_CHARACTERIZATION=1`.
Both Docker integration services set it. `TestMain` requires all three race legs
(writer wins, canonical deleting fence, and orphan-only fence) to complete after
`m.Run()`. A missing stack, a skip, or a partial `-run` subtest filter therefore
cannot report green.

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
| `CreateFileFromBlocks`, exact session `up:` | Session-owned `up:`, aligned nominally to 48h | Commit performs reuse/ownership checks | `pub:` is staged later | Pin can approach expiry between verification and `pub:`; no minimum remaining TTL is required | `classifyBlockForCommit`, `classifyBlockOwnership`, session commit | `CONDITIONAL` |
| `CreateFileFromBlocks`, foreign `fs:` reuse | Foreign permanent ref | Probe/ownership check is before own `pub:` | Later attempt `pub:` | `foreign fs observed -> foreign fs removed -> GC claim -> refs=0 -> writer pub` | ownership accepts `fs:`; publication is a later step | `UNGUARDED` |
| Dedup via foreign `fs:` where the publication path establishes no own `up:` | Foreign `fs:` | Pre-publication observation only | Later own `pub:` | Same last-foreign-ref interleaving as above; this excludes funnels such as `UploadFile` that register an own pin after reuse | `CreateFileFromBlocks` ownership path, block reference model and GC fs-object cascade | `UNGUARDED` |
| Copy/move within one repo | Source immutable fs-object normally owns `fs:` | No writer-owned post-pin fence | Destination stage writes `pub:` | Source retention may bridge staging, but its exact relation to concurrent retention GC and destination stage is not encoded | copy/move stage call chains | `UNKNOWN` |
| Cross-repo copy | Source-repo `fs:` borrowed by destination writer | No destination-owned post-pin fence | Destination `pub:` | Concurrent source deletion/retention GC can remove the borrowed ref; no cross-repo lease is demonstrated | `copyFSObjectToLibraryForPublish`, batch operations | `UNKNOWN` |
| Repair after HEAD is visible | Existing `pub:` plus durable pending publication record, when present | Not a pre-HEAD materialization decision | Promotes permanent `fs:` | This is R31 convergence, not evidence that the original pre-HEAD R3 interval was continuous | pending published files and sync repair paths | `CONDITIONAL` / separate R31 concern |

The table intentionally records `UNKNOWN` where a source walk has not proved a
temporal premise. This PR does not turn those rows green by assumption.

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
additional structurally reachable per-block CQL callsites = 0
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
follows local aliases of directly resolvable functions, and freezes the number
of structurally reachable submitted CQL callsites for every guarded root. These
are static source callsites, not physical network RTTs. Any intentional new CQL
therefore requires explicit review and a baseline update. This is a deliberately
scoped source analyzer, not a claim of universal Go compiler/type analysis.

The guarded roots include the v2 staging primitive (`AddPublishAttemptReferences`)
and the normal stage/promote/finalize paths. They intentionally exclude
`repairPublishedSyncCommitBlockDelta`: it is post-HEAD R31 convergence, not the
normal pre-HEAD R3 hot path. Thirteen isolated mutations prove red for the seven
handshake/cost regressions plus a hidden FuncLit authority read, a cross-package
wrapper, qualified inline authority CQL, a typed cross-package receiver method,
a local function alias, and an additional non-authority per-block CQL INSERT.

## Outcome and next steps

This characterization may change expectations, but production is not adapted
in this PR. A later PR must select one provenance category, establish its exact
continuity or slow-path protocol, and retain both zero-added-CQL-callsite and
zero-added-authority-read contracts for normal materialize-to-publish traffic.
