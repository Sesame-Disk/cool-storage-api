# R3 liveness-continuity characterization

**Baseline parent:** `c0da425a4` (`main` containing #194 and #196)  
**Scope:** characterization only; no publication behavior changes  
**Status:** X1 remains open. R3 remains OPEN. `GC_ENABLED=false`.

The real-Cassandra evidence gate for a focused run is
`SESAMEFS_REQUIRE_P4B_EVIDENCE`. Standard full integration runs
also carry the existing P4b evidence gate and exercise the same Cassandra stack.

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

This interval is `PROVEN_CONTINUOUS`. Unit contracts pin call order and reject
cleanup of the successful `up:`. `TestR3WriterGCHandshakeAtRealCassandra`
characterizes both races:

```text
writer up -> GC claim -> EACH_QUORUM refs=true -> GC releases
```

and:

```text
GC claim -> EACH_QUORUM refs=false -> writer up
  -> writer sees deleting row or orphan -> writer rejects
```

The evidence stops at the authority decision. It does not repeat #194's
physical-delete tests.

## Provenance inventory

| Funnel / provenance | Liveness owner and TTL | Post-pin fence | `pub:` handoff | Concrete interleaving / missing premise | Evidence | Result |
|---|---|---|---|---|---|---|
| Materialization primitive, fresh target | Exact `up:<operation>`, 48h | `BlockDeleteFenceActive`, then single-use install | Outside this primitive | `up -> fence clear -> install Applied`; active deleting/orphan returns before install | `RegisterUploadedBlockTarget`; unit order/mutation contracts; real Cassandra race | `PROVEN_CONTINUOUS` for pin-through-metadata only |
| Materialization primitive, canonical reuse/repair | Exact `up:<operation>`, 48h | Same fence, then tuple-bound repair | Outside this primitive | `up -> fence clear -> exact repair`; active deleting/orphan returns before repair | Same contracts and race evidence | `PROVEN_CONTINUOUS` for pin-through-metadata only |
| v2 stored upload, block materialized in the finalize request | Own 48h `up:` | Proven by primitive | `stagePendingPublishedFiles` writes `pub:` | No explicit type/evidence binds the earlier successful register to staging; total request-to-stage duration is not an R3 contract | `fs_helpers.go` register and stage call chains | `CONDITIONAL` |
| v2 stored upload, block reused without this request materializing it | Existing `fs:` or other observed liveness | Reuse probe precedes publication pin | Later `pub:` | Foreign ref can disappear after observation, then GC can claim/verify zero before writer stages `pub:` | reuse probe plus publication stage are separate | `UNGUARDED` |
| SeafHTTP normal/streaming finalize, materialized block | Own `up:` created by register; 48h | Proven by primitive | File finalize later stages `pub:` | Materialization and publication share the finalize call, but no explicit bounded-continuity contract connects every block result to stage | `finalizeUploadStreaming`, `RegisterUploadedBlockTarget`, filesystem update | `CONDITIONAL` |
| OnlyOffice callback, downloaded/materialized block | Own callback operation `up:`, 48h | Proven by primitive | `stagePendingPublishedFiles` before HEAD | Same-request ordering is visible, but the full duration/continuity premise is not encoded | `saveOnlyOfficePendingBlock`, callback staging | `CONDITIONAL` |
| Sync `PutBlock` followed by HEAD | Deterministic `up:sync:<repo>:<sha256>`, 48h from the latest successful PutBlock | Proven inside PutBlock materialization | A separate request stages a fresh `pub:` | Process/pod/restart do not remove Cassandra `up:`, but an unbounded delay can cross TTL; HEAD carries no proof of the PutBlock result | `PutBlock`, `syncBlockUploadOperationID`, `stageSyncCommitBlockDelta` | `CONDITIONAL` |
| Sync retry from another pod within the same provisional TTL | Same durable deterministic `up:` | Original PutBlock performed it | Retry stages a new attempt-local `pub:` | Cross-pod is not itself a gap; remaining TTL and association with this commit are unproven at HEAD | sync retry/finalize call chain | `CONDITIONAL` |
| Sync commit whose block had no associated PutBlock | None proven for this commit | None attributable | Staging may resolve IDs and write `pub:` | The commit graph does not prove that this writer ever held pre-publication liveness | `RecvFS`, commit delta builder and staging | `UNKNOWN` |
| `recv-fs-before-put` | FS object may arrive before bytes/metadata; no own upload pin yet | No materialization fence at RecvFS | Publication and later PutBlock are separate protocol events | Exact ordering between commit publication, mapping resolution, and later PutBlock needs a focused protocol trace; endpoint success alone is not liveness proof | `RecvFS`; `TestSyncRecvFSBeforePutBlockPublishesDownloadableFile` | `UNKNOWN` |
| `CreateFileFromBlocks`, exact session `up:` | Session-owned `up:`, aligned nominally to 48h | Commit performs reuse/ownership checks | `pub:` is staged later | Pin can approach expiry between verification and `pub:`; no minimum remaining TTL is required | `classifyBlockForCommit`, `classifyBlockOwnership`, session commit | `CONDITIONAL` |
| `CreateFileFromBlocks`, foreign `fs:` reuse | Foreign permanent ref | Probe/ownership check is before own `pub:` | Later attempt `pub:` | `foreign fs observed -> foreign fs removed -> GC claim -> refs=0 -> writer pub` | ownership accepts `fs:`; publication is a later step | `UNGUARDED` |
| Dedup via foreign `fs:` in any funnel | Foreign `fs:` | Pre-publication observation only | Later own `pub:` | Same last-foreign-ref interleaving as above | block reference model and GC fs-object cascade | `UNGUARDED` |
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
submitted R3 canonical/orphan authority CQL operations added = 0
```

`TestR3PublicationHotPathHasNoPerBlockAuthorityReads` walks the local call graph
of publication/stage/finalize roots. It rejects canonical `blocks` authority
SELECTs, `gc_s3_orphans` SELECTs, and known authority classifiers. The mutation
suite proves that inserting such a helper into the per-block publication path
makes the guard red.

## Outcome and next steps

This characterization may change expectations, but production is not adapted
in this PR. A later PR must select one provenance category, establish its exact
continuity or slow-path protocol, and retain the zero-added-authority-read
contract for normal materialize-to-publish traffic.
