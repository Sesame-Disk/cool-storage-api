# X1 — Closure Options for the Physical-Delete ABA

**Status:** Analysis and option comparison. **No option is accepted. Nothing is
implemented. This is not an ADR.**

Even a future verified X1 implementation would not enable destructive GC by itself;
removing the fleet-wide `GC_ENABLED=false` gate is a separate activation change after
the closure evidence and operational rollout checks pass.

**Date:** 2026-08-14 · Supersedes `GC-X1-X2-ALTERNATIVES.md` (2026-08-13), whose X2
half is now history and whose X1 half is carried forward here, corrected and extended.

**Scope:** `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` (X1), the sole remaining
runtime blocker for destructive GC.

**R22a/R11a status (2026-08-17):** recovery treats `gc_s3_orphans_by_day` as a
discovery-only identity, reloads the canonical orphan at `EACH_QUORUM`, and uses
canonical recovery state for phase/finalization and backend selection. Physical GC
no longer performs forward-mapping cleanup. Missing or
failed canonical reads and discovery-token mismatches fail closed. Errors classified
as unavailable by `isClusterUnavailableError` at either canonical read update the
orphan blocked-path signal, while initial missing, reload missing/changed, and other
reload failures remain separately classified. A failed delete of the by-day discovery
projection is counted after canonical deletion; because that cleanup currently
returns success, an old stale row can fall behind the cursor overlap rather than
necessarily holding the cursor. If it is encountered before the cursor passes its day,
recovery still fails closed instead of skipping uncertain work. The reload is not a
Paxos settlement or lifecycle lock; exact `P` binding remains open R20/R26 work,
and the writer publication gap R3 remains open. The R22 row below records the original
defect and its remaining exact-identity requirements.

**R22a writer surface (2026-08-16).** `gc_s3_orphans_by_day` had a second, unwired
writer: `AddUpsertS3OrphanDiscoveryQuery` / `AddDeleteS3OrphanDiscoveryQuery` in
`internal/db/gc_projection_write_helpers.go`, with no production caller, writing a
partial payload (`storage_class` only, no `recovery_phase`) and no canonical-row
counterpart in the same batch. Both are removed. This is the same shape R21 removed
from the canonical table, and R22a is what made it load-bearing: recovery now retains
the day cursor when a discovery row has no canonical row and that row is encountered,
so wiring such a helper up could hold the cursor in the overlap or leave stale state
behind the cursor until the 90-day TTL. R21's gate does not cover this — its pattern
ends in `gc_s3_orphans\b`, and `_` is a word character, so `gc_s3_orphans_by_day`
never matched it. `TestR22aDiscoveryWriterSurface` now pins the projection to exactly
one INSERT writer (`upsertS3OrphanProjection`), one DELETE writer (`DeleteS3Orphan`),
and the two current helper callers (`StartBlockDeleteOrphan` and
`MarkS3OrphanMappingCleanupPending`). The cross-table publication is not atomic;
lifecycle races remain fail-closed until R20/R26 define the repair and identity rules.

**R22a/R11a availability cost (2026-08-17).** The `pending_mapping_cleanup` branch
previously issued **no** global read at all — `BlockExists` (session consistency),
`cleanupBlockMapping` and `DeleteS3Orphan`. It now requires `GetS3OrphanGlobal` plus
the commit-point reload, both `EACH_QUORUM`. R11a removes the mapping delete and the
`BlockExists` guard from that branch, but the canonical reads and the sweep-level
topology gate remain. A single-DC outage can therefore still stall post-S3 orphan
finalization, and because an orphan row is a writer fence (`ProbeBlockReuse` answers
`BlockedByGC` on mere existence), the outage still **extends upload fencing** for the
affected content for as long as it lasts. This is a liveness cost, not a reason to
let the branch clear an unverified canonical state.

**R23a (2026-08-17). PARTIAL — identity hardening, not a proven binding.** SesameFS
adopts `storage_class` as the candidate backend namespace identity `B` and hardens
every layer that declares, admits, persists or resolves a class so the value is
preserved exactly. What R23a does NOT do is bind a class name durably to the
namespace it was first used with; that history remains an operator contract, now
stated and frozen by R23b below rather than proven at runtime.
A class name that has stored objects is append-only:
it must never be rebound to another bucket, account, provider namespace, or data
set, and it must never be reused for a different backend. Moving new data to a
different namespace requires a new class name such as `hot-v2`; failover may select
another class, but recovery always uses the class persisted with the object and
does not follow failover. Configuration is treated as a deployed contract, so
runtime cannot discover a historical rebind. R23a adds canonical-name, reference
and class/legacy-name collision validation. References must resolve to a class
that can actually be registered, and runtime failover is cycle-guarded, without
adding a new identity field or persisting `P`. Persisting the exact `storage_key`
and forming `P=(storage_class, storage_key)` is the work AFTER R23b and is what
actually closes the physical ABA — but it is a series of properties, not one row.
See the sequencing note below.

**The historical risk remains real even though the current greenfield deployment
contract now prohibits both halves.** An earlier version of this section justified the
rebind half with an argument that does not hold. That argument was: repointing a
class that holds objects makes every block in it unreadable at once, so a rebind
surfaces as a loud outage rather than a quiet GC hazard. It fails on the most likely
shape of a rebind — copy bucket A to bucket B, then repoint `hot-v1` from A to B.
Reads keep working; nothing is loud. And even where a rebind would break reads,
"the system will probably make noise" is not a safety property: observability is not
revocation of authority, and the rule for a destructive path is *when in doubt, do
not delete*.

**What actually makes a migration rebind survivable is narrower, and worth stating
because it shows exactly where it stops working.** Storage keys are content
addressed — `hashToKey` derives `blocks/<org_id>/<h[0:2]>/<h[2:4]>/<hash>` with no
namespace component — and liveness lives in Cassandra, not in the bucket. So an
object at key `K` for a given org is byte-identical content in whatever namespace it
sits, and the garbage verdict that produced a `gc_s3_orphans` row still describes
that same content after the rebind. A misdirected delete therefore removes the same
condemned bytes, in the wrong namespace. It is survivable **by accident of key
derivation, not by design**, and only while the new namespace answers to the same
liveness authority.

*No-reuse* is where that accident runs out. Decommission `hot-v1`, remove it from
configuration, and create a new `hot-v1` months later on a bucket holding another
deployment's data: any surviving persisted `storage_class` (a `gc_s3_orphans` row, a
stale queue item) now resolves to a namespace governed by a different Cassandra,
where key `K` can hold live content that no verdict here ever condemned. The same
exposure exists for a rebind onto a bucket shared with another cluster.

So the two halves do differ in how often they bite, but not in kind: both silently
retarget a persisted identity if an operator violates the contract. A namespace
fingerprint recorded per class and re-checked fail-closed could add runtime
defense in depth within one keyspace, but R23b deliberately defers it. It is not a
prerequisite for R23 or X1 and is not part of request routing or any request hot
path. For this greenfield deployment, `B = storage_class` is accepted under the
append-only, never-rebind and never-reuse contract; the next X1 priority is the
minted `storage_key` that closes the physical ABA.

**Sequencing the exact-`P` series.** Minting a never-reused `storage_key` and forming
`P=(storage_class, storage_key)` opens an exact-`P` SERIES, not one row. R24 keeps its own
narrower meaning — install identity is single-use, and an ambiguous install becomes
`install-uncertain` until serial settlement — while the mint itself touches R9, R10, R12,
R13, R14, R17, R19, R20, R24 and R26 as separate properties. Minted keys make several of
those races physically observable for the first time: today two writers derive the same key
and store the same object, so a double accept is only conceptually wrong; with `W1 -> K1`
and `W2 -> K2` it is two objects.

Two constraints shape the order, and an earlier draft of this note got both wrong by
listing eight requirements and starting at the mint.

**The mint cannot be the first slice.** Every path that has to *find* those bytes still
derives its locator from the logical id (`hashToKey`, `internal/storage/blocks.go`), and no
`storage_key` column exists yet. Mint `K1` while any reader still derives `K`, and the object
sits at `K1` while readers look elsewhere: bytes unreachable with GC switched off entirely.
So the locator must become authoritative BEFORE it becomes distinct — persist and consume
`storage_key` across reads, HEAD/existence and repair while still storing the derived value,
then change what the value is.

**R12 is a prerequisite, not a member.** Raising some conditional statements on the `blocks`
partition to `SERIAL` while others stay `LOCAL_SERIAL` leaves two quorum domains, and one
straggler invalidates every other statement's guarantee — see R12's inventory of eleven
statements. That must land before any install property depends on a `SERIAL` LWT/Paxos
transaction, and it
needs no minted key to do so.

A workable split, one property per PR as the R11/R22/R23 slices were:

- **P0 — SERIAL domain (R12).** Every relevant `blocks` LWT on one consistency level. No
  minted keys, no new identity.
- **P1 — locator authority.** The persisted `storage_key` becomes the exact locator for
  reads, existence checks, repair and physical delete, still holding the currently
  derived value. It carries the delete chain that must exist before a key can differ:
  an exact-key delete primitive, the durable orphan carrying `(storage_class,
  storage_key)`, and recovery deleting that persisted key. **No destructive
  authorization or lifecycle semantics change**: while persisted `K` still equals
  `hashToKey(L)`, exact-key delete and recovery are observationally equivalent to
  today's derived delete — same authorization, same object. The deployment is
  observable before identity moves.
- **P2 — mint and canonical install (R9, R24).** `K1 != K2` for distinct incarnations, SERIAL
  canonical winner, single-use install identity with `install-uncertain` settlement, exact
  losing-`P` cleanup.
- **P3 — condemned-incarnation writer safety (R10, R13, R17).** Tuple-aware repair and writer
  fence. R13 belongs here and not later: it is the data-loss path under B, where a live
  `blocks(L) -> P1` names a tuple already authorized for retirement, so both repair and
  install must block until the canonical row stops naming `P1`.
- **P4 — exact-`P` destructive lifecycle (R14, R19, R20, R26).** Tuple-bound GC, orphan,
  candidate and projection lifecycle: claim/finalize CAS naming `P`, and `_by_day`
  identity including it. The orphan's *locator* handoff is not deferred to here — it
  lands in P1, because recovery must stop deriving before any key is minted.

R18 and R27 may attach to P2 or P3 depending on how recovery/retry is resolved. Do not read
P0-P4 as an exhaustive closure list, and do not let "`K` is unique" read as "X1 solved".

**P1 scoping note (2026-08-21), verified against the tree.** P1 is the right next
structural step, but the work is not where the one-line description suggests. The
cost is concentrated in making every writer resolve, write and persist one key;
the read paths largely already fetch what P1 needs. Three things
have to be true before persisted `storage_key` can be the authority, and only the
third is what the P1 line describes.

*1. Most writers do not persist it.* `blocks.storage_key` is written empty by
almost every materialization path. Verified call sites:

| Call site | `storageKey` argument |
|---|---|
| `internal/api/seafhttp.go` `HandleUpload` | `""` |
| `internal/api/seafhttp.go` `finalizeUploadStreaming` | `""` |
| `internal/api/sync.go` block materialization | `""` |
| `internal/api/v2/files.go` template materialization | `""` |
| `internal/api/v2/files.go` upload materialization | `""` |
| `internal/api/v2/onlyoffice.go` | real key |
| `internal/api/v2/blocks.go` `UploadBlock` -> `materializeUploadedBlock` | real key |

Seven relevant paths: five empty, two real. The web session funnel is easy to miss
because it calls `RegisterWebUploadedBlockAndMapping`, not
`RegisterUploadedBlockAndMapping`; it persists the key
`StoreUploadedBlockForProbe` resolved through `StorageKeyForHash`. It is a
production writer, so wherever a backfill runs at all it has to cope with a column
that is populated for some rows and empty for others rather than empty everywhere.

This is why every reader guards with `persistedKey != "" && persistedKey != derived`
(`internal/api/v2/upload_reuse.go`,
`internal/streaming/canonical_block_reader.go`): the empty case is the normal case,
not an exception. A column that is empty for most rows cannot be made authoritative
by flipping a branch.

**The writer work is larger than that table suggests, because the register argument
is not the only place a key is derived.** The two probe branches fail differently.
Both statements below are about the **non-web** funnels, which call the two helpers
directly; the web session funnel calls neither and is treated separately after them.

In the `Reusable` branch the key is resolved correctly and then dropped:
`EnsureReusableBlockPresent` returns the canonical key `StoreUploadedBlockForProbe`
resolved, and every non-web funnel but OnlyOffice discards it and persists `""`
(`seafhttp.go:2520`, `seafhttp.go:3031`, `sync.go:2026`, `files.go:1358`,
`files.go:3452`; OnlyOffice keeps it at `onlyoffice.go:1224`).

In the `NeedsPut` branch **every non-web funnel** discards it, OnlyOffice included.
`ResolveNeedsPutBlockStore` returns the resolved key and all six call sites drop it
(`seafhttp.go:2527`, `seafhttp.go:3034`, `sync.go:2035`, `files.go:1364`,
`files.go:3455`, `onlyoffice.go:1227`), then write the bytes with
`PutBlockAutoDirect(hash)`, which re-derives through `hashToKey`
(`internal/storage/blocks.go`). OnlyOffice, the only one that persists anything on
this branch, persists what that PUT returned — a second, independent derivation
rather than the key it resolved. The two agree today only because both call
`hashToKey`; under P2 nothing requires them to.

Only the web session funnel already has the right shape, which is why it is excluded
above: `StoreUploadedBlockForProbe` resolves `K` once, writes through
`PutObjectAutoDirect(K)`, and returns that same `K` — `UploadBlock` keeps it
(`v2/blocks.go:1013`) and hands it to `materializeUploadedBlock` to persist
(`v2/blocks.go:1021`). Stated precisely, step 1 is **one resolution of `K` per
block, the PUT issued against that `K`, and that same `K` persisted** — not "pass
the key to the register call". A structural test asserting
`storageKey != ""` at the materialization funnels pins the symptom, but would pass a
funnel that persists a re-derived key.

P1 therefore contains, in order: make every materialization path resolve, write and
persist one key; backfill any deployment that already holds rows; only then invert
the authority and harden empty from tolerated to an error. **Under the accepted
greenfield production scope the middle step has no production instance to run
against.** `blocks.storage_key TEXT` is already in `001_initial_schema.cql`, so a
fresh install starts with the column present and corrected writers populate it from
the first block. The backfill remains in scope as a compatibility path for an
already-populated deployment, and fixtures and tests still have to be updated, but
it is not a data migration this rollout has to perform.

*2. P1 adds essentially no new Cassandra reads on production paths.*
The canonical reader already performs one `blocks` read per unique block
(`lookupCanonicalBlockLocation`, fanout `canonicalBlockLocationConcurrency = 32`),
and `GetBlockStorageLocation` already selects `storage_key` alongside
`storage_class`. Every consumer of the canonical reader — v2 file reads, SeafHTTP
download, share-link and file-view raw serving — therefore pays **no additional
read and fetches no additional column** when the locator becomes authoritative. It
already has the value in hand and currently discards it in favour of the derived
key.

Two things make a naive inventory wrong here, and both were got wrong on the first
pass.

First, `blockStore` is frequently a parameter name of the interface type
`streaming.BlockReader`, which the canonical reader implements, so most call sites
spelled `blockStore.GetBlock(...)` already receive a canonical reader and already
pay the read. `internal/streaming/streaming.go` (`PrefetchBlock`, `StreamBlocks`)
and `internal/streaming/block_read_seeker.go` all take `BlockReader`, so they
derive or not purely according to what the caller injects. Classify by injected
type, not by identifier.

Second, `internal/api/sync.go` guards its funnels with `if h.db != nil { ... } else
{ legacy }`. The deriving calls sit in the `else`. Grepping for the call finds the
fallback and misses the production branch directly above it.

Already reading canonical metadata, so no new read:

| Path | What already runs |
|---|---|
| SeafHTTP download | `seafHTTPNewCanonicalBlockReaderFn` |
| SeafHTTP ZIP | `addDirToZip(..., canonicalReader, ...)`; the `blockStore` parameter inside `addFileToZip` is that reader |
| sync `check-blocks` | `syncNewCanonicalBlockCheckReaderFn`, which dispatches an explicit `location` phase metered as `SyncCheckBlocksLookupsTotal{location}` before the existence phase |
| sync block send | `syncNewCanonicalBlockReaderFn` |
| v2 file view and raw serving | `streaming.NewCanonicalBlockReader` |
| share-link raw serving | `streaming.NewCanonicalBlockReader` |

Derive-based, but only on the `h.db == nil` legacy fallback, which no production
deployment takes: `CheckBlocksParallel` in `check-blocks`, `syncBlockExistsFn` in
`PutBlock`, and `GetBlockSize`/`GetBlockReader` in the block-send `else` branch.
Making the locator authoritative there is a correctness cleanup, not a load change.

Outside the canonical reader, two production surfaces genuinely derive the physical
locator, and neither needs a new read.

**Upload reuse, existence check and repair.** `internal/api/v2/upload_reuse.go`
derives at three points — the first-writer branch, the `Reusable` branch's
`ObjectExists`/repair-PUT, and `NeedsPut` — and every upload funnel reaches them
(`seafhttp.go`, `sync.go`, `files.go` x2, `onlyoffice.go`, `v2/blocks.go`). This is
where the canonical `P` is repaired, so it is precisely the path that must consume
the authoritative locator rather than re-derive one. It needs no new read:
`ProbeBlockReuse` already selects `storage_key` into `BlockReuseProbe.StorageKey`
(`internal/db/block_references.go`), and the code currently uses that value only as
a mismatch guard.

**GC physical delete.** It needs no new read either. `internal/gc/worker.go`
deletes through `deleteS3WithRetry` ->
`BlockStoreDeleter.DeleteBlock`, which calls `hashToKey`. GC already point-reads
the row: `GetBlockInfo` runs
`SELECT storage_class, created_at, sha1 FROM blocks WHERE org_id = ? AND block_id = ?`
and its own comment notes that reading another column of the same row "adds no
extra query and no tombstone scan". P1's GC work is therefore to add `storage_key`
to `BlockInfo` and to that existing SELECT, and to delete by exact key instead of a
derived one — **an extra column on a read that already happens, plus an exact-key
delete**. That is also exactly what X1 requires of the delete path regardless.

P1's correctness inventory is therefore **canonical reader + upload
reuse/existence/repair + GC delete**, not reader plus GC. The performance conclusion
is unchanged: all three already hold the metadata the flip consumes.

**The delete path is not finished when `deleteS3WithRetry` deletes by exact key.**
Two requirements this document lists under "What both options must change" have to
land with P1 — or at the very latest before P2 — rather than with P4's tuple-bound
lifecycle:

- **There is no exact-key delete primitive at all.** `BlockStore` has
  `PutObjectAutoDirect`, `ObjectExists` and `Get*ByStorageKey`, but
  `DeleteBlock(ctx, hash)` derives (`internal/storage/blocks.go`). It has to be
  added, not merely called.
- **The durable orphan does not carry the key.** `StartBlockDeleteOrphan(orgID,
  blockID, storageClass, externalSHA1, now)` (`internal/gc/store.go`) takes no key,
  `gc_s3_orphans` has no `storage_key` column and no migration adds one
  (`001_initial_schema.cql`; 007, 009, 014 and 015 touch that table only for other
  columns), and the row is written *before* `FinalizeBlockDelete`
  removes `blocks(L)` (`internal/gc/worker.go`). Past that point the orphan is the
  only durable record of what still has to be deleted — and `RecoverS3Orphans`
  re-derives the key from the block id (`internal/gc/worker.go`).

Leaving both to P4 opens a window between P2 and P4 in which minting is live but
recovery still derives: the DELETE addresses a key nothing was ever written to,
returns success as a no-op, and the orphan is cleared as though the object were
gone — the exact S3 leak the durable record exists to prevent. The sequencing
constraint is therefore **exact-key delete primitive + the canonical orphan row
carrying `(storage_class, storage_key)` + recovery deleting that persisted key, all
before the first minted key exists**. While `K = hashToKey(L)` still holds, the
change is observably a no-op, which is why it belongs in the reversible slice rather
than after the mint.

**Persisting the key also makes it part of recovery's observed state, and that
belongs in P1 too.** `RecoverS3Orphans` reloads the canonical orphan immediately
before the irreversible step and refuses to continue when the reloaded row differs,
via `s3OrphanRecoveryStateEqual` (`internal/gc/worker.go`) — which compares org,
block, `first_seen_at`, `storage_class`, `external_sha1` and `recovery_phase`. It
cannot compare the locator today because the row does not carry one. The moment P1
adds `storage_key` to that row, **it must join the comparison and a difference must
fail closed**, or the reload silently stops covering the thing P1 just made
authoritative.

That gap is not hypothetical once `K` can differ, because none of the six compared
fields distinguishes two physical lifecycles of the same logical block.
`StartBlockDeleteOrphan` is explicit about reusing the row: on `IF NOT EXISTS`
failure it takes `effective_first_seen_at` **from the existing row** and then
`UPDATE`s `storage_class`, `external_sha1` and `recovery_phase` back to
`pending_s3` (`internal/gc/store_cassandra.go`). A second delete lifecycle for the
same block therefore presents the *same* `first_seen_at`, the same class and the
same phase as the first. Once the P2 slice mints, that is a recovery which loaded
the first incarnation `(B, K1)`, passed its own reload against a row that now
describes the second `(B, K2)`, and issues `DeleteExact(K2)` — the exact-`P`
violation the reload exists to prevent, reached without any compared field
changing. (`K1`/`K2` here are incarnations, not the P1/P2 slices.) Required gate:
**a reload whose `storage_key` differs from the loaded one performs no DELETE**.

*Compatibility note for the new orphan column.* The greenfield qualification above
applies here as well. Rows written before P1 carry no key, and for them the derived
value is by definition the right one, so a populated deployment needs an explicit
compat path before recovery may reject an empty locator. **Prefer draining the live
orphans before the flip.** A deployment that instead stamps the derived key onto
existing rows must give the new cell a TTL aligned to that orphan's remaining
original schedule, never a fresh table TTL: `default_time_to_live` applies per
written value, so a plain `UPDATE … SET storage_key = …` would hand the locator a
full new expiry while `storage_class`, `first_seen_at` and `recovery_phase` keep
expiring on the original date — reintroducing exactly the partial-orphan TTL skew
R28 describes and R28a's application-clock bound exists to prevent. Under the
accepted greenfield production scope neither path runs: there are no live canonical
orphans to convert.

**The projection stays out of P1, and that is deliberate.** Migration 014 made
`gc_s3_orphans_by_day` identity-only — every remaining column is part of
`PRIMARY KEY ((first_seen_day, bucket), first_seen_at, org_id, block_id)`, pinned by
`TestR22bProjectionWriteIsInsert` — and its own note records that `gc_s3_orphans`
keeps the payload because it is "the authority recovery reads". Recovery therefore
gets the key from the canonical row it already loads, and P1 adds a regular column
to one table. Folding `P` into the projection's *identity* (R26) is a different and
larger change: a key column cannot be added by `ALTER`, so it means a new table and
a revised identity contract. That belongs in P4, and nothing in P1 requires it.

The honest summary is that P1's cost is concentrated in the writer work of step 1
and in the delete-path plumbing above, not in read amplification. An earlier draft
of this note claimed `check-blocks` would add N Cassandra reads per request; that was wrong — it
described the location phase the production branch already performs.

`check-blocks` is still worth attention, but as an **optimization opportunity, not
a new cost**. Its accepted list is bounded by `check_blocks_max_ids`, whose default
and validation ceiling are both the inherited 100k, and the production branch
already dispatches one canonical-location lookup per unique internal id at
`check_blocks_lookup_fanout`. Those per-id lookups exist today; P1 inherits them
rather than creating them. Because the route is sync-hot and its node exposure is
already the product `check_blocks_max_inflight_per_node` x
`check_blocks_lookup_fanout` (`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`), replacing the
per-id lookups with a batched canonical-location read is worth doing — but as its
own X5/X11 optimization, **not inside P1**. It would reduce current load rather than
offset an increase, so it demonstrates nothing about locator authority, and folding
it in only makes the one slice that has to stay reversible harder to review.

Note that `internal/gc/worker.go`'s `w.store.BlockExists` is a Cassandra
`blocks`-row check, not an S3 derive; only the `BlockStoreDeleter` path is
derive-based physical work in GC.

*3. Only then does the authority flip.* The exact-key variants the flip needs
already exist and are the model: `ObjectExists`, `GetBlockByStorageKey`,
`GetBlockReaderByStorageKey`, `GetBlockSizeByStorageKey`, `PutObjectAutoDirect`.
What P1 adds is that callers resolve `P` first and use those, instead of handing a
hash to `GetBlock`/`BlockExists`/`DeleteBlock` and letting `hashToKey` decide.

*Ordering against P0.* The list above puts P0 first, and that remains correct for
anything whose correctness depends on a single serial domain. But P1 does not
change the `P` any writer produces — it keeps the derived value — so it establishes
no new install property and can land before P0 without weakening one. What must not
happen is minting in P2 before P0: that is where a global winner starts to matter.
Read the order as a dependency graph, not a queue.

**Hot-path/Paxos characterization (2026-08-20).** The companion
[X1/X4 upload hot-path analysis](./UPLOAD-PAXOS-HOT-PATH-X1-CHARACTERIZATION.md)
confirms that the shipped production default/example uses `SERIAL` for a
metadata-registering block invocation, subject to the effective environment and
replica topology, and that chunked SeafHTTP finalizations serialize their
metadata callback through one process-local permit. The
non-chunked `HandleUpload` path does not acquire that permit, so this is not a
process-wide concurrency bound. P0 is not an X4 performance fix; its sequencing
as correctness hardening remains open while the
design evaluates whether ordinary installation can remove the per-block LWT
without losing placement and incarnation authority. The two-minute final-file
context is created after `eg.Wait()` and is not the timeout for block
materialization.

**The collision check closes a rebind the code could produce on its own, with no
operator action.** Before R23a, a class whose `initStorageClass` failed was skipped
with only a warning (`internal/api/server.go`), and the legacy `backends:` loop then
registered any name not already registered. With `classes: hot` and `backends: hot`
both defined, a boot where the class initialized bound `hot` to the class bucket,
and a boot where it failed transiently bound the same name to the legacy bucket.
Same persisted `storage_class`, two physical namespaces, chosen by a transient
failure. R23a rejects the ambiguous configuration and now aborts startup when a
declared class fails to initialize, so the manager cannot silently continue with a
partial backend set.

**The canon holds at the persistence boundary, not only at admission.** A stored
`storage_class` is the identity, so the write funnel
(`UpsertBlockMetadataWithRepresentationAndSHA1`) rejects a non-canonical value as a
permanent error rather than normalizing it, and `ProbeBlockReuse` rejects one it
reads back. Readers resolve the raw stored value: the trims that canonical block
reading, upload reuse and GC used to apply are gone, because trimming resolves a
label the writer never persisted — a silent rebind performed by the reader. The
same reasoning removes the trim from GC's orphan-recovery state comparison, which
must not read two different identities as the same state. Every class declared
under `storage.classes` must be registrable, not only the referenced ones, so a
declared-but-unbuildable class cannot be selected as a library default.

**References are part of the identity.** `default_class`, each class's
`failover_class`, and `region_classes.*` must name a class that actually resolves,
under the same canon as the declared names — `IsCanonicalStorageClassName` is the
single definition, shared by configuration validation and the storage runtime, and
it checks the raw value because runtime resolution is an exact map lookup. The
previous reference check trimmed before resolving, so a padded value certified at
startup and failed at use time. `failover_class` is the sharp case: a typo there
breaks nothing until the primary backend is down, which is precisely when it is
needed, so startup validation is the only thing that can surface it in time.

**R23b (2026-08-18). The namespace contract is frozen, and the half configuration
can prove is now enforced.** R23b deliberately does NOT add the durable
class-to-namespace fingerprint table this document previously assigned to it. That
table answers one question: *did this name mean another bucket in the past?* It is
deferred defense in depth, not a prerequisite for R23 or X1, and must not become a
request-routing or request-hot-path lookup. The change that advances X1 is instead
a minted, single-use `storage_key`. The accepted greenfield deployment contract is
the current guarantee for `B`; no historical-value migration or preflight is part
of R23.

What R23b states instead is the contract itself, in the form R23 needs:

> A `storage_class` name that has ever been used is permanent. Its namespace may go
> offline or be destroyed, but the name may never be repointed at another namespace
> and may never be reused for one. New placement uses a new name (`hot-v2`). Any change
> to credentials, account/tenant or provider scope, region, endpoint or bucket is
> allowed only when it still addresses exactly the same physical namespace. For a
> multi-tenant S3-compatible service, account/tenant scope is immutable contract state
> even when configuration cannot reveal it. Encryption, tier and failover policy may
> change only insofar as they do not retarget storage. The class the PUT actually
> landed in is the class that gets persisted.

**And R23b conservatively detects one collision shape from current configuration.**
R23 is usually read as *one name, two namespaces*, which needs history. The inverse
shape — *two names with canonically matching endpoint+bucket declarations* — needs
none, and it exposed a live defect rather than a hypothetical one. Storage keys carry no
class component (`hashToKey` derives `blocks/<org_id>/<h[0:2]>/<h[2:4]>/<hash>`, and
every `BlockStore` is built with the same `blocks/` prefix), so two classes over one
bucket share an org's key space exactly. `Config.Validate` now rejects that: the
namespace collision key is the canonical `(endpoint, bucket)`. It deliberately does
not inspect credentials or infer account/tenant/provider scope, so excluded fields are
not thereby declared irrelevant to physical identity. Endpoint comparison folds host case,
one terminal DNS root dot, default ports, trailing slashes and equivalent AWS
endpoint spellings. This detects canonically equivalent declarations, including
terminal-dot variants, but does not resolve DNS and cannot prove that arbitrary
hostnames or IP addresses reach one physical service. Its path/query/bucket
equivalences may over-reject exotic providers and fail startup; that conservative
result is not proof of universal physical identity. Operators must use one canonical
endpoint spelling per service.

**The collision key is derived from the same endpoint and bucket values the S3 client
is built from.** `StorageClassConfig.EffectiveEndpoint` is shared by validation and
`initStorageClass`, and `BackendConfig.StorageClassConfig()` is the single legacy
conversion, so a legacy backend is checked from exactly the class config that gets
registered. Region remains outside the collision key, not outside the immutable
namespace contract. Every registrable entry must declare it explicitly;
`initStorageClass` trims and passes it through without the old `us-east-1` runtime fallback.

**The check immediately found the defect in a shipped file.** `config.docker.yaml`
declared a legacy `hot` backend over `http://minio:9000/sesamefs-blocks` — the exact
bucket `hot-minio-local`, the docker `default_class`, already named. The dev and
integration stack was running two storage class identities over one physical
namespace, and `hot` was selectable as an explicit `library.storage_class` through
the normal admission path because it was a configured class. R23b keeps that legacy
name over a separate `sesamefs-legacy-blocks` bucket, and `minio-init` creates both
the legacy and modern buckets. The local Compose stack defaults its generic `S3_*`
location variables to `http://minio:9000` / `sesamefs-legacy-blocks` / `us-east-1`
and lets `.env` override them; the collision check refuses a value that points the
legacy name back at the default class's bucket, so the pin is no longer the thing
preventing the alias. Modern classes retain their explicit class configuration,
overridable per class through `S3_CLASS_*`. Production single-region deployments use ordinary `S3_*` directly.

**Placement read failures are UNKNOWN, not empty placement.** The library-class
lookup now returns `(value, error)` across Sync, SeafHTTP, v2 block/file and
OnlyOffice resolution. A successful empty value alone permits hostname/default
routing. Any Cassandra read error propagates and fails closed before storage probe
or write; upload-facing handlers return their existing storage-unavailable response.

**What R23b does NOT prove at runtime, stated plainly.** Configuration cannot detect
an operator repointing a class between boots, and it cannot resolve arbitrary DNS/IP
aliases. Those actions are prohibited by the accepted deployment contract. This
repository targets a greenfield deployment, so R23 adds no migration, historical
class preflight, binding-table bootstrap, or namespace claim-marker requirement.
A durable fingerprint is optional hardening against a historical rebind within one metadata history: the same Cassandra remembers what `hot-v1` meant, so a repoint between boots can be caught. It cannot help a fresh install, whose binding table is empty and which therefore has no memory of what a class name meant elsewhere. A namespace claim marker is the cross-install one: written inside the physical namespace, it lets a foreign or fresh install discover that the namespace is already owned. Neither is part of R23, R24 or X1, and neither belongs on the request hot path. **R23 is closed by deployment contract plus a
conservative detector for canonically colliding endpoint/bucket declarations, not by
runtime proof of history or exhaustive physical identity.**

**Historical R11a canonical-state characterization (2026-08-17, before R11b-1).**
The canonical `external_sha1` and `representation_id` fields were no longer used
for mapping cleanup, but remained auxiliary canonical-state discriminators in the
commit-point reload. `StartBlockDeleteOrphan` preserves `first_seen_at` when it
resets an existing `(org_id, block_id)` row. Therefore two observations of the
same canonical recovery row could have the same `first_seen_at`, `storage_class`,
and `recovery_phase` while `external_sha1` changed through the reachable
greenfield empty-to-populated metadata-backfill path. The sole production
`blocks` INSERT validates and persists a non-empty `representation_id`; its
empty-value repair is an imported/legacy-row path. That reload detected the
SHA-1 change. This did not establish a physical target change or prove that the
discriminator was safety-load-bearing rather than defense-in-depth. R11b-1 below
resolved the reachability question for `representation_id` and retained only the
conservative `external_sha1` characterization.

**R11b-1 (2026-08-17).** The reachability audit established that
`representation_id` is not used by physical orphan recovery to select a backend,
authorize a delete, or advance a phase. Migration
`015_gc_s3_orphans_without_representation_id.cql` therefore drops it from the
canonical `gc_s3_orphans` table and the GC contract. `external_sha1` remains in
the canonical row and in `s3OrphanRecoveryStateEqual`: its reachable
empty-to-populated backfill is still retained as conservative fail-closed
coverage. This is a metadata-surface reduction, not an X1 closure; the orphan
still identifies a logical block and storage class rather than an immutable
physical `P=(B,K)`.

The older R28 discussion below describes the pre-R11b-1 writer set; its
references to `representation_id` as an orphan identity writer are historical.
After R11b-1, the remaining conditional orphan identity writers are
`external_sha1` and `recovery_phase`.

The recovery path also has two distinct post-S3 windows. If the orphan clear fails
after `pending_mapping_cleanup` is durably recorded, retry recovery does not issue
S3 again. If the process fails before that phase transition is durable, the row
remains `pending_s3` and a later retry may issue S3 again. This is not an R11a
mapping-safety regression, but it is not an at-most-once physical-delete guarantee;
the `pending_s3` block-existence guard still defers recovery when the canonical
block has been resurrected. Exact P identity is the future mechanism that makes a
repeat of P1 harmless to P2.

**Why the obvious repair for a stranded discovery row is not safe yet, and which R
gates it.** When canonical is absent, the tempting fix is "delete the projection row".
The gating requirement is **R20, not R26**. `StartBlockDeleteOrphan` preserves
`first_seen_at` only on the `!applied` branch — i.e. only when the canonical row still
exists; when it is absent the LWT applies and `P2` is minted at `first_seen_at = now`.
A stale `P1` clear keyed on `T` therefore cannot name `P2`'s row at `T'`, so R26's
shared-token mechanism is not what makes the repair unsafe. What makes it unsafe is
that the repair would act on a *negative* canonical read, and an ordinary `EACH_QUORUM`
SELECT is not settlement: around an unsettled LWT it may report absence for a row that
exists. R26 remains fully valid for `DeleteS3Orphan` itself, whose zero-`firstSeenAt`
path really does resolve the token from whatever canonical row is current — that is the
shared-token route. Retain-and-fail-closed stays correct until R20 is settled.

**R22b — projection payload dropped (delivered).** The payload columns on
`gc_s3_orphans_by_day` (`storage_class`, `representation_id`, `external_sha1`,
`recovery_phase`) had **zero readers** after R22a and were write-only. Migration
`014_gc_s3_orphans_by_day_identity_only.cql` drops them, which turns R22a's API
separation into a storage separation: "the worker does not read the payload" becomes
"the payload does not exist". `upsertS3OrphanProjection` fell from seven parameters to
three, and `MarkS3OrphanMappingCleanupPending` stopped reading `storage_class` and
`representation_id` back, since it fetched them solely to refill projection cells no
reader consulted. At the time of R22b, canonical `gc_s3_orphans` kept all four
columns; R11b-1 subsequently removes only its `representation_id` column.

**What R22b changed about the table itself.** Every surviving column is a primary-key
column, so a discovery row now carries no regular cells and its **primary-key liveness
is the row**. Only `INSERT` writes that liveness, which is what
`TestR22bProjectionWriteIsInsert` pins alongside the column list. The requirement is a
*conditional* guard: an `UPDATE` of this table is inexpressible today (CQL needs a `SET`
over a non-key column and none remains) but becomes possible again the moment a regular
column is re-added.

**State the UPDATE hazard correctly — the intuitive version is wrong, and an earlier
draft of this document got it wrong.** An UPDATE-created row is *not* invisible to the
scan. Cassandra treats a row as present when it has live cells even with no PK liveness
info; that is exactly why `UPDATE` upserts, and the day scan would enumerate such a row
normally. The real defect is deferred rather than immediate: the row's lifetime becomes
its payload cell's, so it disappears when that cell is deleted or expires under the
90-day TTL, dropping an identity that was still supposed to be recoverable. The property
R22b buys is that a discovery identity is durable **independently of any payload**, and
publishing via UPDATE is what would give that away. Do not restate this as "the scan
cannot see it".

Because `TTL()` cannot be applied to a key column, the row's lifetime is observable only
through the table default; `TestGC_R22bProjectionRowIsIdentityOnly` confirms against the
real engine that such a row reads back, is enumerable, and that migration 014 left
`default_time_to_live` intact.

**What R22b did NOT change.** It does not touch TTL semantics. `upsertS3OrphanProjection`
still writes without an explicit TTL, so the phase-advance republish still re-anchors the
projection's term to wall-clock while canonical `first_seen_at` keeps its original one —
before R22b it refreshed every cell, now it refreshes the row marker, and the skew is
identical either way. That is R28's open row-wide alignment item, not something this
increment closes, and R22b must not be described as removing the stall path it produces.

**R22b deployment boundary.** Migration 014 is a clean-cut schema change: this branch
assumes a greenfield deployment where all migrations run before any application traffic
and no pre-R22b binary remains in the fleet. Once 014 is applied, an older binary that
still inserts the dropped projection payload will fail after its canonical write, so
this migration is not a mixed-version rolling-upgrade contract. Rollback is forward-only
through a new migration and binary; do not roll back to an image that predates R22b.

**Gate hardening, and one gate defect (2026-08-17).** The caveats recorded here on
2026-08-16 are closed, and auditing them turned up a fourth item that was not
hardening but a real hole in the gate.

The hole: `TestR22aCanonicalOrphanReadAndDiscoverySurface` identified the canonical
read with `strings.Contains(query, "FROM gc_s3_orphans")`, and that substring is also
present in `FROM gc_s3_orphans_by_day`. Pointing `GetS3OrphanGlobal` at the discovery
projection therefore left the gate green — the precise authority inversion the whole
of R22a exists to prevent, unguarded by the test named after it. This is R21's `\b`
lesson lost on the way to R22a: R21 was written with `gc_s3_orphans\b` *because* `_`
is a word character, and the sibling gate dropped the boundary. Both table matchers
are now boundary-aware regexes (`canonicalOrphanRead`, `discoveryOrphanTable`), and
`GetS3OrphanGlobal` additionally fails if it names the projection at all.

The three previously recorded caveats, all now closed:

- `gocql.EachQuorum` is attributed to the canonical query's own call chain — the gate
  walks for a `.Consistency(...)` call whose receiver subtree contains the `Query(...)`
  holding the canonical CQL, and requires that single argument to be `gocql.EachQuorum`.
  Merely mentioning the identifier in the function no longer satisfies it.
- The discovery side inspects every statement naming `gc_s3_orphans_by_day` rather than
  only those matching an expected `SELECT` prefix, so a reworded query cannot skip the
  payload-column check; `ListS3OrphansByDay` also fails if it reads canonical.
- `TestR22aDiscoveryWriterSurface` compares helper callsites **by count per caller**,
  not set membership plus a total. The old form accepted two publications from
  `StartBlockDeleteOrphan` and none from `MarkS3OrphanMappingCleanupPending`: two
  elements, both in the allowed set, total matching — green while a lifecycle
  transition had silently stopped publishing its discovery row.

Each was verified in its red form against a deliberately mutated `store_cassandra.go`,
and each mutation was confirmed to pass the pre-fix gate. Runtime behaviour is
unchanged; only the tests moved.

**Scope of every source gate in the R21/R22 series, stated so it is not overread.**
They scan **string literals**. CQL assembled by concatenation or a query builder would
evade all of them. That is acceptable today — every CQL statement in this repo is a
literal, and a builder would be a visible architectural change rather than a quiet
one — but it means these gates are evidence that no *currently written form* of a
second writer or a payload read exists, not a proof that none is expressible. Nothing
in this document should describe them as closing a class of defect outright; they
close the shapes the codebase actually uses. The same applies to the commit-point
reload gates, which pin the three call sites that exist rather than the property that
no irreversible action may run without a reload.

**Related:**

- [KNOWN_ISSUES.md](./KNOWN_ISSUES.md) — status of record
- [UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md) — X1/X2 wording, the closed F-series, and the X list
- [GC-X2-MULTIDC-VALIDATION.md](./GC-X2-MULTIDC-VALIDATION.md) — X2's runbook and closure findings
- [GC-SERVICE-ANALYSIS.md](./GC-SERVICE-ANALYSIS.md) — current GC behaviour

> **Destructive GC stays disabled.** `GC_ENABLED=false` on **every replica in every
> DC**, the wording `DEPLOY.md` and `config.prod.yaml` use, until X1 closes. The
> single-node local Compose stack deliberately does not pin the variable; that
> exemption is documented in `docker-compose.yaml` and is not a policy gap.

---

## What changed on 2026-08-14, and why this document exists

Two things settled at once.

**X2 closed, without generations.** `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` was
fixed by pinning the read that *authorizes destruction* to `EACH_QUORUM`
(`BlockHasReferencesGlobal`, `internal/gc/store_cassandra.go`) behind a topology
gate (`ValidateDestructiveGCTopology`, in the `GCStore` interface at
`internal/gc/store.go:115`), with reference writers left at `LOCAL_QUORUM` and pinned
per statement. Evidence: five legs green on a real three-DC cluster, both mutations red.
See the registry's X2 row.

**The generational-fence protocol (r3) was abandoned.** It was drafted on
`docs/gc-x1-x2-generation-fence-final` and proposed in PR #166, which closed unmerged.
That branch is kept as investigative reference only; **no part of it is a decision of
record**, and neither the ADR nor its PR-1…PR-8 plan should be cited as accepted. Its
material contribution — that the physical-delete ABA component closes only when new
physical incarnations use never-reused physical keys — is preserved here without the
surrounding lifecycle protocol.

X2's closure separated the cross-DC liveness problem from the publication TOCTOU. r3 kept
ordinary reference writes at `LOCAL_QUORUM` and therefore proposed a publication frontier
to prove that no new reference could appear against a generation being retired. That
frontier remains one possible X1 solution; X2 did not make it unnecessary. What X2 removes
is the need to use a generation lifecycle to solve *reference visibility*: the shipped
`EACH_QUORUM` GC-side read handles that half without a writer hot-path round trip.

The smaller option below tries to replace the publication frontier with a narrower
combination: globally visible claims, validation of the canonical physical incarnation
after reference writes, and never-reused physical keys. Whether that combination is
enough is the X1 question; it is not settled by X2.

This document therefore asks one question: **what is the smallest correct way to give
each physical incarnation of a block an identity that a stale DELETE cannot name?**

---

## X1 — verified problem statement

The physical object key is derived from the logical content hash:

```text
blocks/<org_id>/<hash[0:2]>/<hash[2:4]>/<hash>
```

`BlockStore.hashToKey`, `internal/storage/blocks.go:309-317`. GC deletes by re-deriving
that key: `processBlock` → `deleteS3WithRetry` → `BlockStore.DeleteBlock(blockID)`
(`internal/storage/blocks.go:289`) → `hashToKey` again. `S3Store.Delete` issues a
key-only `DeleteObject` with no `VersionId`.

```text
GC determines refs = 0
        ↓
authorizes DELETE of blocks/.../ABC123
        ↓
DELETE is slow / retrying / already on the wire

                   writer rematerializes ABC123
                   PUT the same key
                   new logical reference
                         ↓
                   old DELETE lands
                         ↓
                   live bytes are gone
```

Cassandra claim state cannot revoke an S3 request already accepted by the object store.
Because blocks are content-addressed the new bytes are byte-identical to the old ones,
so ETag, `If-Match` or HEAD cannot tell the two lives apart.

`blocks.storage_key` already exists as a column
(`internal/db/migrations/001_initial_schema.cql:277`) but it is **not** the locator.
Three production sites derive the key and **reject** a persisted key that differs:

| Site | File |
|---|---|
| Canonical read path | `internal/streaming/canonical_block_reader.go:238` |
| `ResolveNeedsPutBlockStore` | `internal/api/v2/upload_reuse.go:142` |
| `Reusable` branch of `StoreUploadedBlockForProbe` | `internal/api/v2/upload_reuse.go:159` |

That third one is a second X1 exposure distinct from rematerialization: when the probe
says `Reusable` but the object is missing, it issues a **repair PUT at the derived key**
(`repairCanonicalBlockDirectFn`, `internal/api/v2/upload_reuse.go:172`) with no fence
re-check. Tracked below as **R10**.

Note what those three sites mean for any minted-key design: today "persisted key ≠
derived key" is a **fail-closed corruption signal**. Minting keys inverts it. That is
not a locator refactor; it is replacing an invariant with a different one (validate org,
hash prefix and format instead of exact equality), and it must be done deliberately.

**Minimum property:**

> A DELETE authorized for the old physical life of a block must never be able to delete
> the new physical life.

That requires distinct physical identities. Waiting, locking, or re-reading Cassandra
after the DELETE has been sent does not establish it.

### The premise that makes every option below viable

`L = sha256.Sum256(storedContent)` where `storedContent` is the byte sequence actually
written to the object store — **after** encryption for encrypted libraries
(`internal/api/seafhttp.go:2468-2482`; the same shape at `sync.go:1894`,
`v2/blocks.go:927`, `v2/files.go:3428`, `v2/onlyoffice.go:1181`).

Therefore **any two objects with the same `L` are byte-identical by construction.** Two
physical incarnations of one logical block are interchangeable to every reader. This is
what makes it unnecessary to bind references to a physical life: a reference means
"content `L` is live", and any live incarnation of `L` satisfies it.

This premise is load-bearing for the recommended safety baseline. If block IDs ever stop being a
hash of the stored bytes, the option below has to be re-derived.

---

## What X2's closure settled, and what it did not

Settled, and reusable by any X1 design:

- **A global liveness read exists and is gated.** `BlockHasReferencesGlobal` at
  `EACH_QUORUM`, used by `processBlock`'s claim-then-verify and by `RecoverS3Orphans`,
  refusing to run at all unless `ValidateDestructiveGCTopology` passes.
- **Reference writers are pinned.** `BlockReferenceWriteConsistency = gocql.LocalQuorum`
  (`internal/db/block_references.go:972`), set per statement by both producers, with an
  AST scan (`TestBlockReferenceProducersPinWriteConsistency`) that fails on a third
  producer that forgets. The level did not change; the pin did.
- **The zero-check is asymmetric.** A locally visible reference is proof the block is
  alive; a local zero authorizes nothing. So the pre-claim check, the scanner and
  `enqueueZeroRefBlocks` legitimately stay at session consistency, and only the
  authorizing read pays WAN.

Not settled by X2, and squarely X1's problem:

- **Physical identity.** Nothing about `EACH_QUORUM` stops a stale DELETE from naming
  the key a new upload just wrote.
- **The publication TOCTOU.** GC reads zero, a writer publishes afterwards. Globalizing
  the GC read does not close it. It is not X2 and must not be smuggled into it.

### `QUORUM` is not enough, and this is now empirical

Production is three DCs — NA, EU, Asia — at RF 1 each
(`.env.prod.example`: `CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`). A writer's
`LOCAL_QUORUM` in EU puts the row on the EU replica only; a non-local `QUORUM` read needs
2 of 3 and can be satisfied by NA+Asia. The three-DC harness confirms it: mutating
`EACH_QUORUM→QUORUM` turns leg 2b red.

> **Do not read the DC count out of `configs/`.** The versioned cluster profiles declare
> `replication_dcs: {usa: 1, eu: 1}` — two DCs, no Asia — and they are correct as they
> stand: they belong to the `docker-compose.mr-cluster.yaml` harness. Anyone sizing a
> quorum argument from `configs/` gets 2 of 2, where `QUORUM` looks sufficient, instead
> of the real 2 of 3, where it is not.

### `EACH_QUORUM` vs `ALL`

On RF 1 per DC they are equivalent — a quorum of 1 in each of three DCs is every replica.
Prefer `EACH_QUORUM` for a cross-DC writer-visible fence: it reaches a quorum in every DC
without requiring every replica when RF>1. The serial phase of the LWT is separate and
should be `SERIAL`; the regular commit can be `EACH_QUORUM`, while writer fence reads use
an explicit `LOCAL_QUORUM`. `ALL` remains a stricter policy choice, not a requirement for
intersection with a `LOCAL_QUORUM` writer read.

---

## What upload-fence already gives

PR-1…PR-10 closed F1–F14. X1 was explicitly out of scope. The series is reusable
substrate, not a close.

| Mechanism | Where | What it gives X1 | What it does not |
|---|---|---|---|
| Provisional `up:` written **before** metadata | `RegisterUploadedBlock`, `internal/api/v2/fs_helpers.go:989` | GC's claim-then-verify can see an in-flight upload | PUT still happens before `up:` (X3 leak) |
| Write-ref-then-check-fence | same helper, `:996-1003` | A writer that sees the fence rematerializes instead of reporting success | Uses the **same derived key** |
| Claim-then-verify | `processBlock`, `internal/gc/worker.go:1039` | A ref that landed before verify aborts the delete | Nothing about the physical key |
| Outer store→materialize→confirm | `retryUploadedBlockMaterialization`, `internal/api/v2/upload_reuse.go:230` | Re-PUT after a fence that cleared mid-cycle | Re-PUT uses the same derived key |
| `BlockDeleteFenceActive` | `gc_state='deleting'` **or** a `gc_s3_orphans` row | Current code makes any orphan a logical fence; B must make the fence key-aware: block while the canonical row still names the orphaned key, but allow `K2` after the row is gone | Orphan row is keyed by logical `block_id`; recovery deletes by derived hash |
| `pub:` then `fs:` | `StagePublishAttemptReferences` / `PromotePublishAttemptReferences` | Publish-attempt liveness across the HEAD CAS | **No** post-insert fence check on the `pub:` stage (unlike `RegisterUploadedBlock`) — tracked as **R3** |

`ProbeBlockReuse` (`internal/db/block_references.go:864`) classifies
`Reusable` / `NeedsPut` / `BlockedByGC` / `RepairableStub` / `UnknownError` from
`LOCAL_QUORUM` reads of `blocks`, references and orphans. Note its precedence on the
row-exists path, which matters for every option below:

```go
if activeClaim || repairClaim { return BlockedByGC }   // :913
...
if hasOrphan     { return BlockedByGC }                // :927  ← wins over Reusable
if hasReferences { return Reusable    }                // :931
return NeedsPut                                        // :934
```

Two observations that are easy to get backwards:

- **Zero references does not mean "mint a new incarnation".** A row with no references
  is a GC candidate, so the probe answers `NeedsPut` and the writer may need to
  materialize the bytes. If a healthy canonical row still exists, that materialization is
  a repair/reuse of its current `storage_key`; minting a new key while `blocks(L)` still
  exists would lose the `INSERT ... IF NOT EXISTS` race and leave the new object orphaned.
  A fresh key is minted only when a new physical incarnation is actually born, normally
  after the canonical row is absent.
- **`hasOrphan` outranks `hasReferences`.** This is the only thing that stops a writer
  from reusing an incarnation whose bytes are already condemned but whose `blocks` row
  outlived the authorization. Any design that stops treating an orphan as a fence must
  replace this, not simply delete it. See **R13**.

---

## The measured cost of waiting — the number that decides the design

The current protocol makes the writer wait behind the physical delete. Both sides of that
wait are constants in the repo, so this needs no measurement campaign.

| Quantity | Value | Source |
|---|---|---|
| Writer retry budget | 8 attempts, 50 ms doubling to a 400 ms ceiling ⇒ **≈1.95 s** of total backoff | `internal/api/v2/fs_helpers.go:680-684` |
| GC inline S3 delete | 4 attempts, 100 ms / 500 ms / 2 s ⇒ **≈2.6 s** of backoff plus S3 latency | `internal/gc/worker.go:352-356` |
| Orphan recovery cadence | Scanner **Phase 16**, on the scanner ticker: **24 h** in production, plus once at startup and on manual trigger | `internal/gc/scanner.go:138,1437`; `internal/gc/gc.go:690-706`; `configs/config.prod.yaml:386` |
| Configured orphan TTL | `default_time_to_live = 7776000` — 90 days **per written value**, not a row-lifetime ceiling: a later update refreshes the TTL of the columns it rewrites (**R28**) | `001_initial_schema.cql:1248,1263` |
| Stale claim release | 15 minutes, and only if a later candidate pass revisits the block | `internal/gc/worker.go:522` |

Read together:

- **Happy path.** The orphan row lives as long as the S3 DELETE takes — milliseconds. The
  writer's retries cover it comfortably, and each retry re-runs the probe, so the upload
  proceeds on the first attempt after GC clears the row. The fence is invisible.
- **S3 delete fails.** The row survives with `recovery_phase = pending_s3`. The next
  thing that can clear it is Phase 16 — **up to 24 hours later**. If recovery keeps
  failing there is no trustworthy upper bound: the nominal 90-day TTL expires each value
  independently, and the retry updates keep rewriting some of them, so what survives can
  be a partial canonical orphan that still fences the writer after its identity columns
  and its projection are gone (**R28**). Every upload of that content in the meantime
  spends ~2 s and then fails with `ErrBlockDeleteInProgress`.

The escape hatch is inert. `retryUploadedBlockMaterialization` accepts a `resolveFence`
hook that could end the wait early. The two SeafHTTP paths wire it
(`internal/api/seafhttp.go:2541,3046`) to `clearSeafHTTPS3OrphanFence`, which reads the
orphan row, logs that "the writer will back off and leave S3 cleanup to GC recovery", and
returns `(false, nil)` on every path (`internal/api/seafhttp.go:2664-2681`). The three
v2 paths (`v2/blocks.go:1005`, `v2/files.go:3472`, `v2/onlyoffice.go`) pass `nil`. **No
production path can shorten the wait.**

**This is the strongest availability argument for B, not the safety-baseline argument.** A design in which
the writer never waits behind a *physical* delete removes a 24-hour worst case, not a
theoretical one.

### But the 24 hours is a scheduling artifact, not a property of A+

This is worth separating before the figure is used to choose a protocol. Orphan recovery
is **Phase 16 of the scanner**, so it inherits `scan_interval`. Nothing about the work
itself requires that cadence:

- The worker already runs its own loop on `gc.worker_interval` (30 s) with its own
  trigger channel (`internal/gc/gc.go:501-519`), and `RecoverS3Orphans` is a method on
  the **worker**, not the scanner (`internal/gc/worker.go:1332`) — the scanner only calls
  it (`internal/gc/scanner.go:1437-1444`).
- A manual trigger already exists and is wired to the API layer
  (`Service.TriggerScanner`, `internal/gc/gc.go:303-310`, called from
  `internal/api/server.go:2392`), so 24 h is the *automatic* bound, not a hard one.
- The inert `resolveFence` hook is the writer-side version of the same idea: a writer
  that hits a fence could drive bounded recovery of that exact key instead of waiting for
  a sweep.

So A+'s worst case can plausibly be cut from ~24 h to the worker tick by **where the
phase is scheduled**, with no protocol change and none of B's key-aware semantics. The
residual after that is the case where S3 is persistently unavailable — where failing the
upload closed is arguably the correct answer anyway.

**Price this before treating the 24 h figure as the reason to adopt B.** If a scheduling
change gets the stall to tens of seconds, B's remaining advantage is narrow enough that
the larger proof obligation may not be worth taking on in the same change that closes the
blocker. Two caveats keep it from being free: recovery on the worker tick pays its
`EACH_QUORUM` verify and its bucket walk far more often, which lands squarely on the GC
drain-capacity question below; and the cursor/`phaseErr` semantics were written for a
daily sweep and would need re-examining at 30-second cadence.

---

## Options

| Option | Physical-delete ABA | Publication TOCTOU | Invasiveness | Verdict |
|---|---|---|---|---|
| **A+** — sequential lives plus the complete X1 closure package | Closed | Needs the strong `pub:` post-check **on every promote path** (R25) | Medium | **Recommended safety baseline; normally recovery-cadence-bound, but conservative R18(a) can keep the fence for the attempt-reference TTL plus scheduling delay — and per R27 that retry lifecycle does not exist yet** |
| **B** — overlapped lives ("d-lite"): fresh key for each new life, next life installs as soon as the metadata row is free | Closed | Needs the strong `pub:` post-check, in a stronger form | Medium-high, still far below r3 | **Post-A+ availability optimization** |
| **C** — writer references at `EACH_QUORUM` | **Open** | Open | Low | Not an X1 mechanism |
| **D** — S3 object versioning | Closed only if *every* delete is version-id-specific | Unchanged | Backend + interface | Discussable, not preferred |
| **E** — reduced fence, keep hash-derived keys | **Open** | Closable | Medium | Cannot close X1 |
| **0** — the abandoned r3 generational fence | Closed | Closed | Very high | Abandoned 2026-08-14; reference only |

Options A+ and B share the physical-identity and claim package; they differ only in
whether the old physical delete must finish before a new canonical life can exist.

In the table, **Closed** under *Physical-delete ABA* means that the option can close that
component only. It does not mean that X1 as a workstream is closed; the publication,
claim-ownership and recovery criteria remain open until implemented and evidenced.

---

## The shared core of A+ and B — never-reused physical keys

Keep the logical model exactly as it is:

```text
reference ──▶ L (SHA-256)          references stay logical, LOCAL_QUORUM, unchanged
                │
                 └── blocks row ──▶ storage_key      which incarnation currently holds L
```

For the physical protocol, define `B` as an **immutable backend namespace identity** and
`P = (B, storage_key)`. In the current implementation, `storage_class` can serve as `B`
only if R23's append-only namespace contract is enforced; otherwise `B` must be a
persisted immutable `backend_id`. The key may be a valid CAS token within one backend
namespace, but storage I/O also needs `B` to select that namespace. Every candidate,
claim, orphan, finalize, recovery and exact delete carries `P`; prose below may use
`K1` as shorthand only when `B1` is already fixed.

Every **new physical incarnation** of `L` writes to a freshly minted key that is never
reused. A repair of an incarnation that is still canonical and not condemned keeps its
existing key:

```text
L  = sha256:abc…

life 1:  K1 = blocks/<org>/ab/c…/abc….<uuid-1>
life 2:  K2 = blocks/<org>/ab/c…/abc….<uuid-2>
```

GC records the exact key it was authorized to destroy and deletes **that key only**. A
delayed DELETE of `K1` cannot name `K2`. That closes the stale physical-delete ABA
component of X1; publication validation, claim ownership and recovery liveness remain
separate X1 criteria.

**`storage_key` is sufficient as the incarnation/CAS identity within one immutable `B`.**
An earlier draft proposed a separate `physical_id` column and a `delete_id`; neither is
needed. If the physical tuple `P = (B, storage_key)` is globally fresh and never reused,
it *is* the identity. The exact storage I/O locator is the same tuple, because `B`
selects the immutable backend namespace:

```sql
INSERT INTO gc_s3_orphans (…, backend_identity, storage_key) VALUES (…, B1, K1) IF NOT EXISTS
DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
  IF backend_identity = B1 AND storage_key = K1
```

**References do not become generation-aware.** They keep meaning "content `L` is live",
which any incarnation satisfies, because of the byte-identity premise above.

### What both options must change, regardless of the A/B decision

1. **A delete-by-exact-key surface.** *Scheduled into P1 — see the P1 scoping note;
   recovery must stop deriving before any key is minted.* `BlockStore` has exact-key `Put`
   (`PutObjectAutoDirect`), `Exists` (`ObjectExists`) and `Get`
   (`GetBlockByStorageKey`), but **no exact-key delete** — only
   `DeleteBlock(ctx, hash)`. Both destructive callers pass a logical block ID today:
   `deleteS3WithRetry` (`worker.go:1234`) and `RecoverS3Orphans`
   (`worker.go:1557`). The replacement must validate `(org, L, P)` and the minted-key
   format before selecting `backend(P).DeleteExact(storage_key)`; malformed persisted
   locators fail closed.
2. **`gc_s3_orphans` carries the exact physical tuple**, on the canonical table and on
   the `_by_day` projection, and recovery stops deriving. *The canonical half —
   the orphan row carrying the exact key, and recovery deleting it instead of
   deriving — is scheduled into P1; see the P1 scoping note. The projection half is
   a primary-key change against an identity-only table (migration 014) and stays
   with P4/R26.* Both rows must be confirmed
   durable before `FinalizeBlockDelete` removes `blocks(L)`; a canonical orphan without
   its discovery projection can strand recovery behind the cursor.
3. **The claim must name the physical locator.** `ClaimBlockDelete` is
   `UPDATE blocks … IF gc_state != 'deleting'` (`store_cassandra.go`) — it
   does not mention the physical locator. It must become `IF gc_state != 'deleting' AND
   backend_identity = B1 AND storage_key = K1`, and the candidate/discovery identity must
   preserve `P1` until claim, finalize and cleanup. Otherwise a candidate enqueued for one
   life can claim or clear another. The `claimID` must also be a fresh UUID per worker
   attempt, not a deterministic value derived from `candidateAt`; stale takeover and
   every release or finalize must compare the previous attempt's `(P1, claimID,
   claimed_at)`. The current `bool` result is not enough by itself: `!applied` must not
   mean "complete the candidate" when another fresh attempt owns the same `P1`.
4. **`FinalizeBlockDelete` binds the life.** Its existing
   `IF gc_state = ? AND gc_claim_id = ?` (`store_cassandra.go`) grows
   `AND backend_identity = B1 AND storage_key = K1`.
5. **One serial domain** — see R12, and note the inventory below is larger than
   previously recorded.
6. **The `pub:` stage grows a post-insert check** — see R3.
7. **No repair-PUT onto a condemned key** — see R10.
8. **The orphan TTL package** — see the dedicated section.
9. **Physical reconciliation** — see R9.
10. **Readers and the dedup oracle stop deriving keys** — see the call-site inventory.
11. **Validate the physical locator before destruction.** `ValidatePhysicalLocator(org,
    L, B, storage_key)` must validate org scope, logical hash binding and the
    minted-key format before any exact-key DELETE. A mismatch fails closed and emits an
    alert; persisted `storage_key` becoming authoritative must not turn corrupt metadata
    into permission to delete an arbitrary object. Recovery applies the same validation.

**Minted keys are necessary but not sufficient, and X1 is the whole workstream.** The
registry's X1 row carries four closure criteria, and the physical-identity change is only
one of them. In particular the claim id is derived from the candidate timestamp
(`blockDeleteClaimID(candidateAt)`, `internal/gc/worker.go:1276`) and is therefore
*shared* by every attempt on the same logical candidate: one worker can drop the
publication fence while another is still deleting under it, and the reuse probe can then
hand a writer back the very incarnation being destroyed. Physical-key uniqueness cannot
prevent that, because no new incarnation is created. Neither option below closes it on its own;
read the criteria in Registry X1 alongside this document.

---

### Option A+ — sequential lives, hardened safety baseline

GC drops the `blocks` row first, as today, and the writer keeps waiting until the whole
destructive window is over:

```text
orphan(L,P1) durable → drop blocks row → DELETE exact P1 → clear orphan(P1)
                                                          ↓
                                    writer wakes, mints P2, INSERT IF NOT EXISTS
```

`P1` and `P2` never coexist **as canonical lives**. State the guarantee that way rather
than "only one object can exist", which R9 disproves: two writers leaving the wait
together can both PUT before the install CAS picks a winner, so two *objects* for one `L`
are reachable. What A+ guarantees is that no canonical successor exists while the
predecessor's destructive lifecycle is still open. Because the canonical lives are
serialized, most of the machinery people expect is structurally unnecessary:
`ProbeBlockReuse` keeps its current meaning (an orphan blocks the logical block, because
there is no live successor to distinguish it from), no "one outstanding delete" invariant
has to be stated, and R11 cannot fire.

A+ is not merely "add a UUID". It combines sequential canonical lives with the complete
X1 closure package: physical identity `P` never reused; every destructive action bound to
`(L, P, claimID)`; a fresh claim UUID per worker attempt with CAS-protected takeover;
durable orphan creation before row removal; exact-key recovery and conditional orphan
clear; a strong post-write publication check; repair strictly separated from install
(R17); and ambiguous LWT outcomes settled in the serial domain (R20). The existing X2
global liveness verification remains in orphan recovery under A+ — **subject to R18,
which is the one place that argument does not carry over unexamined.** A stale takeover
must itself compare the prior `(P1, claimID, claimed_at)` — `B` included, since
`P` and not `storage_key` is the identity; age alone is not authority to release a claim
owned by another attempt.

For A+, any `gc_s3_orphans(L)` row remains a logical writer fence: after staging `up:` or
`pub:`, publication succeeds only if `blocks(L)` exists, has a non-empty canonical
`storage_key`, has no destructive or repair claim, and no orphan row exists for `L`.
The check is stronger than merely asking whether a generic fence was observed earlier.

**The linearization points have to be spelled out, or that sentence reads as an impossible
requirement.** For a *new* install there is a legitimate instant where `up:` exists and
`blocks(L)` does not — creating it is what `INSTALL` does. The rule is a verdict on
publication, not an immediate post-stage assertion, and R17's split of `REPAIR` from
`INSTALL` makes the distinction load-bearing. The ADR must name three separate shapes:

```text
NEW INSTALL            EXISTING INCARNATION        pub: STAGING
  prepare P2             (repair of P1)              canonical already exists
  write up:                write/keep up:            stage pub:
  PRE-INSTALL CHECK        check canonical == P1     POST-STAGE CHECK
    no destructive claim   no claim / no orphan        blocks(L) exists, P usable
    A+: no orphan(L)       repair exact P1             no destructive/repair claim
  INSTALL(P2) @ SERIAL     no create-capable call      A+: no orphan(L)
  POST-INSTALL CHECK       final canonical/fence     ─────────────────────────────
    blocks(L) exists         validation              publication may succeed
    canonical P usable
    no claim, A+: no orphan
```

This is a specification ambiguity, not a race found in the code: the existing
write-ref-then-check-fence order already matches the `NEW INSTALL` column
(`RegisterUploadedBlock` writes `up:`, checks the fence, then calls `UpsertBlockMetadata`).
What R17 adds is that the pre-install check no longer authorizes a *repair* to create the
row, and what R3 adds is the post-write half.

**Cost.** The writer is blocked for as long as the orphan row lives. Absent a later
attempt reference, automatic recovery is normally bounded by the current **approximately
24-hour scanner cadence** unless scheduling changes; that is an operational choice, not a
protocol bound. Under conservative R18(a), however, the fence can also outlast any
attempt reference written after the authorizing read: up to **48 h** for `up:` and up to
**35 days** for `pub:`. State the availability price as **the attempt-reference TTL plus
recovery scheduling delay** — the fence cannot lift the instant the reference expires, only
at the next sweep that reaches the row — rather than as a flat 24-hour bound or a flat 35
days. And note the honest form of that claim today: per **R27** the current projection
cannot reschedule a deferred orphan into a future day at all, so until that lifecycle
exists the bound is not long, it is **absent**. Quote it as "up to the attempt-reference
TTL plus recovery scheduling delay, once the deferred-orphan retry lifecycle of R27 is
implemented". Nor is the 90-day orphan TTL an outer ceiling on the row: per **R28** it
expires each value independently and retries extend some of them, which is one more reason
the TTL package below is indivisible. A+ can close X1 only when the complete package above
is implemented and evidenced; never-reused keys alone close only the stale physical-delete
ABA component.

The existing `resolveFence` hook can later become a writer-assisted exact-key recovery
optimization: attempt bounded recovery of the same `K1`, clear it conditionally only
after success, then re-probe and mint `K2`. If S3 remains unavailable, the writer still
fails closed and no successor life is created. That optimization is separate from the
initial A+ safety closure.

---

### Option B — overlapped lives ("d-lite") — **post-A+ optimization**

Same drop-row-first ordering, but the writer stops waiting on the *physical* delete. The
orphan row stops meaning "logical block `L` is blocked" and starts meaning "physical
incarnation `K1` is being deleted".

```text
t0  GC claims the row (gc_state='deleting', P1=(B1,K1))        ← writer blocked here
t1  BlockHasReferencesGlobal @ EACH_QUORUM == 0
t2  INSERT orphan(L, P1) IF NOT EXISTS                        ← durable authorization record
t3  DELETE blocks row IF claim AND P1                         ← writer released here
t4  DELETE K1  (slow, may fail, may be retried for days)      ← blocks nobody
        ║
        ╚═ concurrently: writer mints P2, PUT P2, INSERT blocks(L→P2) IF NOT EXISTS
```

**What the writer's wait becomes.** Not zero — that claim must not be made. `blocks` is
one row per logical block (`PRIMARY KEY ((org_id, block_id))`) and the install is
`INSERT … IF NOT EXISTS`, so while any row for `L` exists the writer cannot install `K2`.
The wait shrinks from *"until the physical delete finishes"* to *"until GC releases the
metadata row"* — one `EACH_QUORUM` read plus two or three LWTs. **The physical delete
stops blocking anyone.** That is the entire value proposition, and it is worth stating
in exactly those terms.

Eliminating the residual wait as well would require a conditional `K1 → K2` update on the
claimed row. That is a genuinely different design with a new CAS shape and is **not**
recommended: the residual is bounded by GC's own metadata machinery, and the CAS is the
first step back toward a generation lifecycle.

#### B.1 — Authorization must stop being asked about `L`

**This is the deepest change in the option, and the one most easily missed.**

Every authorization in the destructive path is keyed by the *logical* ID. Under
overlapped lives, `K1` pending and `K2` live share the same `block_id`, so those
questions stop answering the question that matters:

| Check | Where | What it does today | What it must do once `K2` exists |
|---|---|---|---|
| `BlockExists(L)` | `worker.go:1441-1452` | If the canonical row exists, defer recovery **and set `phaseErr`** | Replace existence with a canonical-locator read: if the row still points at `K1`, defer and let GC finish metadata finalization; if absent or pointing at `K2`, recovery of `K1` may continue. A logical existence check alone freezes the cursor permanently. |
| `BlockHasReferencesGlobal(L)` | `worker.go:1459` | If references exist, refuse to delete the bytes and log for an operator | Do not use `refs(L)` to re-authorize the physical delete: references to `K2` are irrelevant to `K1`. The orphan's exact key and the canonical-locator check carry the prior authorization. Legacy or keyless orphan rows cannot use this shortcut and must remain fail-closed. |
| `StartBlockDeleteOrphan` | `store_cassandra.go` | *"always resets the phase to `pending_s3`, even when a stale row from an older delete already exists for the same `block_id`"* | Overwrites `K1`'s record with `K2`'s ⇒ **`K1`'s only durable memory is destroyed** |

The resolution is a single principle:

> **`EACH_QUORUM == 0` authorizes the retirement of `K1` once, before the orphan row is
> written. The orphan row *is* the durable record of that authorization. Recovery
> finishes a previously authorized physical delete; it does not re-authorize it.**

Under never-reused keys that argument is sound in a way it is not today: *"`K1` was
authorized for retirement at time T"* cannot authorize a future life, because no future
life can ever be called `K1`. Recovery still must confirm that the canonical row no longer
points at `K1` before issuing the S3 DELETE. The re-verification of `refs(L)` is what
becomes redundant; the physical-locator check does not.

**This is an explicit amendment to X2's closure argument and must be recorded as one.**
The registry's X2 row currently states the opposite: that `RecoverS3Orphans` *"could
inherit authorization transitively from an orphan row that cannot exist unless that
verify passed, but that implication only runs forward in time and rested on a greenfield
precondition code cannot enforce and that fails silently."* Under minted keys the
precondition **is** enforceable by code — the orphan row names the exact bytes. Whoever
implements B rewrites that paragraph with the new argument. Deleting the re-verify
without rewriting the argument is how X2 gets silently reopened.

Concretely:

- `StartBlockDeleteOrphan` becomes a real mutual exclusion: `INSERT … IF NOT EXISTS`,
  and on conflict **do not overwrite** — release the claim on `K2` and *postpone* its
  candidate (a postpone that does not spend the queue item's retry budget; the walk
  already has `blockClaimNotYetStaleError` for exactly this shape). That is where the
  **single outstanding physical delete per logical block** invariant is actually born,
  and it lets the orphan table keep its one-row-per-`(org, L)` primary key.
- `RecoverS3Orphans` compares the current canonical locator with the orphan's exact key,
  deletes only after the row is absent or points at a different key, and clears the row
  with `IF backend_identity = B1 AND storage_key = K1`. The clear GC actually executes is
  `DeleteS3Orphan` (`internal/gc/store_cassandra.go`), a plain unconditional `DELETE`
  reached from `processBlock` and from all three recovery exits (`worker.go:1261, 1411,
  1429, 1584`) — that is the statement the fence clear must make conditional, or a delayed
  clear from `K1`'s lifecycle can lift a fence belonging to a later one. A **second**
  unconditional clear, formerly exposed as `DeleteBlockS3Orphan`, was removed by the
  R21 authority-surface PR because it had no caller anywhere in the repo — not even
  tests. The active `DeleteS3Orphan` clear remains separate R26 work and must still be
  made lifecycle-bound before the orphan becomes load-bearing.
- The accepted price: while `K1` cannot be deleted, `K2` is not GC-eligible. Storage is
  not reclaimed until the earlier delete completes. That is fail-closed and correct, but
  it is an operational behaviour that should be named before it surprises someone.

#### B.1a — Canonical incarnation states

The logical row and the orphan row together define which physical incarnation is usable.
The minimum state machine is:

| Canonical `blocks(L)` | Orphan physical tuple | Writer outcome |
|---|---|---|
| absent | `Pold` | Allow a new incarnation; mint `Pnew`. |
| `P1`, active | absent or a different tuple | Reuse or repair `P1`; do not mint. |
| `P1`, deleting/repair-claimed | any | Block and retry. |
| `P1` | `P1` | Block and retry; `P1` remains the condemned canonical locator until the row is removed. |
| `P2`, active | `P1` | **B only:** `P2` is usable; recover `P1` independently. **A+:** this state is not allowed. |

This is the distinction the existing `NeedsPut` name hides. `NeedsPut` can mean that the
current canonical object needs repair; it does not, by itself, authorize a new physical
key. A new key is minted only for a new incarnation after the old canonical row is no
longer the row being installed into. `RegisterUploadedBlock` therefore becomes
key-aware: a repair preserves the current key, while a post-delete materialization uses a
new key and an insert that wins the canonical row.

#### Orphan creation outcomes — A+ and B

`StartBlockDeleteOrphan` must distinguish an applied insert, an idempotent retry, a
different lifecycle and an ambiguous database outcome. Treating all non-success results
as conflicts is unsafe:

| Result | Required action |
|---|---|
| `applied=true`, stored tuple `P1=(B1,K1)` | Proceed with `P1`; durably ensure the canonical orphan and its discovery projection before continuing. |
| `applied=false`, existing tuple `P1` | Treat as an idempotent resume of the same lifecycle; repair/ensure the projection and **do not release the claim**. |
| `applied=false`, existing tuple differs from `P1` | Another physical delete is pending for this logical block; do not overwrite or reset it. Release the `P1` claim through its own CAS and postpone without spending the retry budget. |
| Timeout, transport error or otherwise ambiguous CAS result | Do not release the claim. **Settle the outcome in the serial domain** — preferably an idempotent no-op retry of the same LWT; otherwise a `SELECT` at query consistency `SERIAL` in the same Paxos domain (R20). Never use a non-serial consistency read as negative authority. An ambiguous result may mean `orphan(L,P1)` already exists. If settlement cannot be established, the state stays ambiguous: retain the claim and the candidate and stop. |

The storage interface may expose these outcomes directly or return enough existing-row
identity for the worker to classify them. A retry that finds its own `P1` must never be
treated as a competing lifecycle, while an ambiguous result must never reopen the claim
window between `blocks(L)` and `orphan(L,P1)` — and per **R20** it is settled in the
serial domain, never by a non-serial consistency read used as negative authority.

**"Durable" needs an operational definition, not just the word.** The canonical
`blocks(L)` row must not be removed until both the canonical orphan row and its `_by_day`
recovery projection are durable, which means:

| Write | Requirement |
|---|---|
| Canonical `gc_s3_orphans` row | LWT, serial phase `SERIAL`, regular commit `EACH_QUORUM` — same global visibility as the claim |
| `gc_s3_orphans_by_day` projection | Ordinary write — no Paxos needed — but at **`EACH_QUORUM`**, acknowledged before `FinalizeBlockDelete` |

"Explicit consistency" is too weak a rule to state here, because it fixes no property:
`upsertS3OrphanProjection` is an ordinary insert inheriting the session, which production
sets to `LOCAL_QUORUM`. Pick `EACH_QUORUM` concretely. This adds another WAN write and
its own latency/write load, but it does not introduce a stricter availability dependency
than the destructive path already has: that path is gated on every DC being reachable.
It also stops a GC failover to another DC from depending on hinted or eventual delivery
of the index. The alternative, if the extra cost is ever unacceptable, is a durable way
to rebuild missing projection rows from the canonical ones; what is not acceptable is
leaving the level implicit.

The projection is load-bearing for recovery *liveness*, not for authorization: losing it
after the canonical row is removed is safe against data loss but leaves a fence that the
day-cursor sweep never rediscovers. **And "never a source of authorization" has to be
enforced, not merely asserted** — today recovery destroys from projection-sourced fields
without reloading the canonical row (R22). `upsertS3OrphanProjection` already returns its error
to `StartBlockDeleteOrphan`, and `processBlock` already fails closed before
`FinalizeBlockDelete` (`store_cassandra.go`), so **the ordering is correct
today** — this rule exists so a refactor cannot lose it, and so the projection's
consistency stops being implicit.

#### B.2 — The publication post-check must name the incarnation

`StagePublishAttemptReferences` (`internal/db/block_references.go:495-513`) inserts the
`pub:` rows and returns; there is no post-insert fence check, unlike
`RegisterUploadedBlock`. Under B the check cannot simply ask "is there an orphan?",
because an old orphan deliberately no longer blocks the writer. The correct invariant is:

> No newly written reference may become a successful publication unless, after the write,
> there exists a canonical incarnation of `L` that is active, has no destructive or
> repair claim, and whose `P` is not the one recorded in an orphan row.

All conditions are load-bearing. Requiring only "a canonical row exists and is not
deleting" is satisfied by a stale row still pointing at a condemned `K1` — see R13 — and
requiring only "the key is not orphaned" misses the window between GC's claim and its
orphan insert. The post-check must reject both a condemned locator and an active claim.

The race is concrete: GC can claim `K1` and obtain `EACH_QUORUM == 0`, then a writer can
insert `pub:` before GC has inserted `orphan(L,P1)`. A check that sees only "row exists"
and "no orphan yet" would accept a publication that GC is already authorized to remove.
The post-check therefore requires `blocks(L)` to exist, `gc_state` and repair-claim state
to be clear, and the canonical `P` not to match a pending orphan. If any part
is inconclusive, the call must remove the `pub:` rows staged by that call and retry rather
than succeeding. The rollback must be scoped to this attempt; if it fails, return an
error/recovery signal rather than leaving a rejected publication live. The existing
`pub:` TTL (35 days) is a leak bound, not a substitute for this rollback.

`RegisterFSObjectBlockReferences` never checks anything itself: it verifies the
`fs_object` exists and resolves block ids (`fs_helpers.go:1037-1059`), then writes
permanent `fs:` rows. It runs only inside `PromotePublishAttemptReferences` — all three
promote sites (`seafhttp.go:3161`, `sync.go — finalizeSyncCommitBlockDelta`, `fs_helpers.go:1236`) sit inside a
stage/promote pair. **That structure is real, but the inference drawn from it is not:
`PromotePublishAttemptReferences` never verifies that a `pub:` row exists**
(`internal/db/block_references.go — PromotePublishAttemptReferences` calls `registerPermanent()` and then deletes whatever
`pub:` rows it finds), so "promote implies a checked stage" holds only for callers that
actually establish the handshake. See **R25**, which recorded the one production path that
did not.

Availability note: that post-check reads `blocks` at `LOCAL_QUORUM` while `K2` may have
been installed by an LWT in another DC. It can miss it and retry. That costs latency, not
safety.

#### B.3 — The SHA-1 mapping belongs to `L`, not to a key

Before R11a, `cleanupBlockMapping` (`internal/gc/worker.go:707-726`) deleted from
`block_id_mappings`, which maps an **external SHA-1 → internal SHA-256**. That row
belongs to the logical block, not to any incarnation, so "check the mapping still belongs
to `K1`" is not expressible without making the mapping generation-aware — exactly what
this option avoids.

The better move is to **decouple the mapping's lifecycle from the physical object's**:
physical recovery of `K1` should not clean the logical mapping at all. Two facts support
it:

- The runtime already treats a leftover mapping as benign metadata: a desktop bare-SHA-1
  GET can return 404 and the association self-heals when the identical block is re-uploaded.
- The conflict that table protects against is *"an external SHA-1 already maps to a
  **DIFFERENT** internal SHA-256"* (`internal/db/block_references.go:50`). `K1` and `K2`
  share the same `L`, so no incarnation change can ever trigger it.

**R11a delivered the decoupling.** Physical GC no longer deletes the mapping, and the
post-S3 phase is now a legacy finalization state rather than a mapping-cleanup action.
The price is that nothing reaps the mapping when `L` genuinely dies, so the table grows
by one small row per ever-deleted block with an external SHA-1. This PR accepts that
growth explicitly; the logical-death reaper is tracked separately as
`ISSUE-GC-LOGICAL-MAPPING-RETENTION-01`.

#### B.4 — What B deliberately does not add

`block_generations`, `block_generation_uses`, generation-bound references, retirement
evidence, a publication-frontier protocol, quarantine, abort, successor states, pointer
epochs. None of it is reachable from the invariants above.

---

### Option C — writer references at `EACH_QUORUM`

Recorded for completeness and to keep it out of X1's scope. Making
`AddBlockReference` / provisional / publish-attempt / removals write at `EACH_QUORUM`
would close cross-DC visibility from the writer side, which X2 already closed more
cheaply from the reader side. It does **not** close X1 and does **not** close the
publication TOCTOU. If it is ever revisited it is a latency experiment, not a design:
do not model it as RTT × rounds, because `SERIAL` waits on a global response order
statistic, ordinary `EACH_QUORUM` waits for a local quorum in every DC, and `ALL` waits
for the slowest replica.

---

### Option D — S3 object versioning

Version-aware PUT/GET/DELETE can separate two lives on the **same** key:

```text
PUT ABC → versionId=V1 ; GC DELETE ABC?versionId=V1 ; new PUT → V2 ; delayed DELETE V1 → V2 survives
```

Same *shape* as `K1`/`K2`, sourced from the backend instead of from us.

**Why it is not preferred.** Product direction is that versioning, if any, lives in
Cassandra so SesameFS does not depend on a specific object backend. The runtime `Store`
interface is key-only (`Put`/`Get`/`Delete(storageKey)`), the only implemented backend is
`S3Store`, and MinIO/AWS behaviour is not identical.

**The delete-marker caveat is why "just enable versioning" is unsafe.** On a versioned
bucket a **key-only** `DeleteObject` does not remove old versions — it writes a delete
marker and makes an unversioned GET see a 404. A delayed key-only DELETE after a new PUT
still hides the live current version. Versioning closes X1 **only if every delete path is
version-id-specific**, orphan recovery included. Falling back to key-only delete reopens
X1 from the reader's point of view.

---

### Option E — reduced fence, keep hash-derived keys

Fence at `SERIAL+ALL`, keep `blocks/<org>/…/<hash>`. **X1 stays open**: the stale DELETE
still names the only key the new upload will use. Useful only as the publication half of
A or B. Discarded as an X1 close.

---

### Explicitly discarded

None of the following close X1 while the old DELETE and the new PUT share one physical
identity:

- **Longer grace** (1 h / 24 h / 7 d). Reduces probability; does not bound an in-flight
  DELETE, a retry, or an outage.
- **ETag / `If-Match` / HEAD-before-DELETE.** Byte-identical rematerialization makes the
  values equal — that is the whole point of content addressing.
- **Cassandra TTL lock, GC lease, extra `ref_count`, re-read `blocks` immediately before
  DELETE.** Cassandra cannot retract a request the object store already accepted.
- **Raise RF and keep LQ/LQ.** `LOCAL_QUORUM` is per-DC; an EU write and an NA read still
  need not intersect.
- **Quarantine-prefix Copy+Delete.** The `Delete` of the live key has the same ABA. It
  remains useful as a *recovery affordance* — the bytes survive — which is a different
  claim from closing X1.
- **Two-phase "wait then HEAD".** Grace plus ETag, and fails for both reasons.

X3 (PUT before durable discoverable intent) is a storage leak, not live-data deletion,
and is out of scope here.

---

## Race matrix

Assume: references stay `LOCAL_QUORUM`; GC's authorizing read is `EACH_QUORUM`; the claim
fence is globally visible; every **new incarnation** mints a fresh key, while repairs of
an active canonical incarnation preserve its key.

**R23 row reconciliation (2026-08-20).** The R23 row below is historical rationale.
The current requirement superseding earlier proposals is:
R23 is closed for this greenfield deployment by the append-only, never-rebind and
never-reuse contract plus conservative checks for canonically colliding endpoint/bucket
declarations. Credentials, account/tenant/provider scope, region, endpoint and bucket
may change only if the exact physical namespace remains unchanged. Validation includes
one terminal DNS dot but cannot resolve arbitrary DNS/IP aliases or infer tenant scope;
its equivalences can over-reject exotic configurations. A durable fingerprint is
optional same-metadata historical-rebind hardening; a namespace claim marker is the
cross-install ownership one. Both are outside R23, R24 and X1, and no historical
migration, preflight, binding bootstrap, or claim marker is required.

| # | Sequence | Required outcome |
|---|---|---|
| R1 | EU `up:` acked, then NA GC reads at `EACH_QUORUM` | GC sees the reference; no DELETE. Closed by X2. |
| R2 | NA GC sees 0, then EU `up:` acked, then the writer's fence check | Writer sees the fence and returns a fence error; the wrapper retries. GC's DELETE names `K1` only, and the `blocks` cleanup is bound to `K1`. |
| R3 | Writer stalled after `Probe=Reusable` (LQ, no fence yet); GC fences, sees 0, deletes `K1`; the writer then stages `pub:` | **Must not succeed as "pin `K1`".** The `pub:` stage needs the post-insert check of B.2. `fs:` needs no separate check — it runs inside the promote, under a still-live checked `pub:` row. |
| R4 | GC sees 0, claims, verify still 0, row dropped, delayed S3 DELETE, writer PUTs | **A+:** the writer remains blocked by orphan `P1`; it cannot create `P2` until exact `P1` deletion and orphan clear complete. **B:** `P2` may exist after the canonical row is removed, and the delayed DELETE must still target only validated exact `P1`, never `hashToKey(L)`. |
| R5 | Writer's probe misses a remote claim because the claim commits at `LOCAL_QUORUM` | **Fails both options.** The claim must commit with global visibility (`EACH_QUORUM`, or stricter `ALL`). **A+:** any orphan for `L` remains a logical writer fence. **B:** after `blocks(L)` is gone, orphan `P1` is recovery/exclusion state for that physical locator and does not block installation of `P2`. |
| R6 | A non-local `QUORUM` GC read (2 of 3), writer LQ in the DC not contacted | **Forbidden.** Empirically red on the three-DC harness. |
| R7 | One DC down during the authorizing read | Fail closed; no DELETE. Already the shipped behaviour. |
| R8 | Who installs the next life, and with what CAS | `blocks` is one row per logical block and the install is `INSERT … IF NOT EXISTS`. **A+:** the writer waits until both the row and orphan are gone, then a plain insert creates `P2`. **B:** the writer waits only until GC drops the row, then may install `P2` while orphan `P1` remains. If the row still exists and is healthy, `NeedsPut` repairs the current `P` instead; it must not mint a losing second object. Neither needs a `P1→P2` successor CAS or a second generation table. |
| R9 | Writers in two DCs both leave the wait and both mint a key | Exactly one incarnation becomes **canonical**. `UpsertBlockMetadata` sets no serial level, so it inherits the session's — `LOCAL_SERIAL` in the shipped cluster profiles (`configs/config-eu.cluster.yaml:27`, `configs/config-usa.cluster.yaml:27`) — and two local Paxos transactions can both apply. Harmless while keys are derived (both write the same key); **not** harmless once each DC mints its own. The installing statement needs a `SERIAL` serial phase. `SERIAL` picks a canonical winner; it does **not** prevent the loser's PUT. The losing writer still knows its exact key and should best-effort delete it, or persist that exact key for cleanup. A crash before any durable intent remains X3, not an X1 bucket-inventory requirement. |
| R10 | Writer stalls after `Probe=Reusable(K1)`; GC fences, sees 0, authorizes `DELETE K1`; the writer resumes, finds the object missing, and repair-PUTs | Must not re-PUT a condemned key. Confirmed live: the `Reusable` branch of `StoreUploadedBlockForProbe` (`upload_reuse.go:152-174`) does `ObjectExists` → repair-PUT with **no** fence re-check, and `EnsureReusableBlockPresent` passes `beforePut = nil` (`:205`). The one caller that supplies `beforePut` (`v2/blocks.go:996`) uses it for the staging cap, not for the fence. Under minted keys the clean rule is **repair an active current incarnation with its current key; never repair a condemned incarnation — wait and mint a new key after the row is free**. |
| R11 | `K1`'s delete completes, `K2` is created and live, then stale `K1` cleanup runs | **CLOSED by R11a/B.3.** Physical GC no longer deletes `block_id_mappings`, so the mapping survives independently of the physical lifecycle. The untagged `TestR11aPhysicalGCNeverDeletesBlockIDMappings` source gate pins that absence of physical-GC mapping-delete authority. `BlockExists` remains only in `pending_s3`, where it protects against repeating a physical S3 delete while a canonical block row exists; it is no longer consulted by `pending_mapping_cleanup`. This closes the mapping-loss race, but not the exact physical `P` identity problem tracked by R26 and the remaining X1 work. |
| R12 | Any conditional statement on the `blocks` partition still runs at `LOCAL_SERIAL` after the others are raised | **Fails the whole fence.** The two levels are different quorum domains, so a `LOCAL_SERIAL` round can miss an in-flight `SERIAL` proposal and one straggler invalidates every other statement's guarantee. See the inventory below — it is **eleven** statements, not six. |
| R13 | `INSERT orphan(L,P1)` succeeds, `DELETE blocks` row fails persistently, and a later candidate pass may release the claim once it is at least 15 minutes old | **New, and it is a data-loss path under B.** The row is now live, unclaimed, and pointing at a physical tuple already authorized for retirement. Today `ProbeBlockReuse` refuses it because `hasOrphan` outranks everything (`block_references.go:927`); B must replace that logical fence with a tuple-aware one. The corrected outcome is not to mint `P2` while `blocks(L) -> P1` still exists: both repair and install paths must block because the canonical tuple is condemned. Once the row is removed, `P2` may be minted. This makes step 6 of the naive protocol ("`P1` is irrevocably retired once the orphan is written") false: retirement completes when the canonical row stops naming `P1`. |
| R14 | A candidate enqueued for `P1=(B1,K1)` is processed after `P1` died and `P2` was installed | The claim CAS must bind the physical tuple (`IF … AND backend_identity = B1 AND storage_key = K1`), or GC claims a life it never verified. The candidate/discovery work item must carry `P1` far enough for claim, finalize and candidate cleanup to reject stale work instead of touching `P2`. `processBlock` re-reads `GetBlockInfo` after the claim, which limits the damage, but the CAS should still name the life. |
| R15 | `StartBlockDeleteOrphan` returns a conflict or ambiguous error after the orphan insert may have applied | Same-identity conflict is an idempotent resume and must retain the claim; a different identity is a confirmed competing lifecycle and may release/postpone by CAS; a timeout or error must not release the claim until the outcome has been **settled in the serial domain** (R20) — never by a non-serial consistency read. Treating every non-applied result as a conflict reopens the claim/orphan gap. |
| R16 | A fresh attempt `D2` calls `ClaimBlockDelete(P1,D2)` while `P1` is already claimed by fresh `D1` | `!applied` is not completion. Classify row absent, same `P1` with fresh `D1` (postpone and preserve the candidate), same `P1` with stale `D1` (take over with CAS), different `P` (stale candidate; never touch it), and ambiguous timeout (settle serially per R20; no candidate cleanup). A fresh claim UUID invalidates the current `!applied → DeleteBlockGCCandidate` shortcut. |
| R17 | An **existing-incarnation operation** that read `P1` from a live canonical row completes its metadata write after `P1`'s whole destructive lifecycle finished | **Reopens X1, and revalidating before the repair PUT does not close it.** The dangerous step is the metadata install, not the PUT. `RegisterUploadedBlock` ends at `UpsertBlockMetadata`, whose first statement is `INSERT … IF NOT EXISTS` (`block_references.go:167-171`), and the `storageKey` it inserts is the one captured during the store phase (`StoreUploadedBlockForProbe` → `RegisterUploadedBlockAndMapping` → `RegisterUploadedBlock`). Sequence: writer revalidates `P1` and repair-PUTs it, then stalls; GC claims, verifies zero, orphans, drops the row, and its first exact DELETE times out ambiguously while a retry reports success; GC clears the orphan; the writer resumes, sees **no** fence (row and orphan are both gone), and its `INSERT … IF NOT EXISTS` **applies**, re-installing `blocks(L) → P1`; the confirm phase re-PUTs the object; the ambiguous first DELETE lands and the live bytes vanish. **Required outcome: repair and install must be different operations.** `REPAIR(P1)` may only *update* a canonical row that still names `P1` and must never create an absent row — if `blocks(L)` disappeared or changed, it returns retryable and the wrapper re-probes from scratch, minting `P2` if a new incarnation is warranted. Only `INSTALL(P2)`, on a key minted in this attempt, may `INSERT … IF NOT EXISTS`. **Do not scope this to the branch literally named "repair".** It covers *every* path that takes `P` from an existing canonical row and hands it to a create-capable primitive — the `Reusable` repair, and equally `NeedsPut` on an existing row, which resolves its key from `probe.StorageClass`/`probe.StorageKey` in `ResolveNeedsPutBlockStore`. Cleanest form: an existing-incarnation operation calls no create-capable metadata primitive at all, and updates — if it needs any — are `IF backend_identity = B1 AND storage_key = K1`, with an absent row meaning start over. |
| R18 | An **attempt reference written after the authorizing read** — rejected or abandoned — vetoes recovery of the very key it was rejected for | **A+-specific, because A+ keeps `BlockHasReferencesGlobal(L)` in recovery. Fail-closed: the consequence is a stuck fence, not deleted live data.** Both attempt-reference kinds do it. `up:`: `RegisterUploadedBlock` writes the provisional reference **before** checking the fence and deliberately does **not** roll it back when the fence is active (`fs_helpers.go:989-1003`), TTL **48 h**. `pub:`: `StagePublishAttemptReferences` cleans up a *partial* stage, but a stage that completes and then loses its process — before the new post-check or its rollback — leaves the rows live, and their TTL is **35 days** (`PublishAttemptReferenceTTLSeconds`), 17× worse. Recovery then reads `refs(L) > 0` and refuses to delete `P1` — and that branch deliberately sets no `phaseErr`, so the cursor advances and the row **leaves the working set permanently** (`worker.go:1502-1530`, filed as `ISSUE-GC-REFERENCED-ORPHAN-LIFECYCLE-01`). Rolling back is not a sufficient answer: the writer can die between the insert and the fence observation. Three ways out, cheapest first: **(a)** keep the re-check but stop dropping a referenced orphan out of the working set — re-project and retry until the attempt TTLs expire, which is correct and costs only availability; **(b)** distinguish durably-published `fs:` from rejected/abandoned `up:`/`pub:`; **(c)** adopt B's historical-authorization argument, whose **R21** provenance prerequisite is now closed. **(a) is the conservative choice for a first closure; do not reach for (c) merely to optimize this.** |
| R19 | A non-creating orphan mutation resurrects a cleared orphan row | **FIXED for resurrection-after-clear** — `UpdateS3OrphanAttempt` is conditional on the expected `first_seen_at`; an absent row or a row with a different observed token is a no-op, gated by `TestGC_UpdateS3OrphanAttempt_DoesNotResurrectClearedOrphan`. Note where the defect hid: `MockStore.UpdateS3OrphanAttempt` only ever mutated a key already present, so the whole unit suite agreed with the fix while production carried the defect — the gate had to be an integration test. Historically: it was a plain `UPDATE … WHERE org_id = ? AND block_id = ?` with no `IF` (`store_cassandra.go`), and in Cassandra that is an upsert. A recoverer whose S3 delete failed can write it after another path already cleared the row, recreating a **partial** row with `last_attempt_at`/`retry_count`/`last_error` and no `storage_class`, no `recovery_phase`, no `first_seen_at` — and no `_by_day` projection row, because that mutation does not touch the projection. The result is worse than a stale row: under A+ it is a **writer fence** (`ProbeBlockReuse` answers `BlockedByGC` on any orphan) that recovery can never enumerate, and if the TTL is removed as the TTL package proposes, it never expires either. **This fix makes `UpdateS3OrphanAttempt` non-creating and conditional on the expected `first_seen_at`. R21 now separately guarantees that `StartBlockDeleteOrphan` is the only production creator, enforced by the untagged authority-surface gate.** The `_by_day` table is a discovery index and never a source of authorization. |
| R20 | An LWT returns a timeout or otherwise ambiguous result and the caller resolves it with an ordinary read | **An ordinary consistency read is never authority to conclude that a claim or orphan does not exist.** Cassandra can accept a Paxos proposal that the client never learns, and a normal `LOCAL_QUORUM`/`QUORUM` read need not materialize it — this is exactly the defect already filed as `ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01` and documented at `store_cassandra.go`, where the comment defers the fix to "the serial-domain decision X1 has to make anyway". Every "read back and reconcile" in R15 and R16 means **settle in the serial domain**. Prefer an idempotent no-op retry of the same LWT when that is sufficient; otherwise use a `SELECT` executed with query consistency `SERIAL` in the same Paxos domain. In gocql, do not confuse this with `Query.SerialConsistency(...)`: that configures the serial phase of conditional mutations and is ignored for ordinary `SELECT`s. A serial `SELECT` is the supported settling read because it can materialize/complete an outstanding Paxos proposal. If neither mechanism settles (timeout, DC unavailable, serial quorum unreachable), the state **stays ambiguous** and the response is: retain the claim, retain the candidate, do not finalize, do not release, do not clear the orphan. Fail closed. |

| R21 | A second API can create an orphan row, so the orphan cannot be trusted as an authorization certificate | **CLOSED by the R21 authority-surface PR.** `RecordS3Orphan` was removed from `GCStore`, `CassandraStore` and `MockStore`; all fixtures now enter through `StartBlockDeleteOrphan`, and failed-attempt state uses `UpdateS3OrphanAttempt`. The unused exported `DeleteBlockS3Orphan` helper was also removed. An untagged AST/source gate requires no forbidden production identifiers, exactly one production `INSERT INTO gc_s3_orphans`, conditional `UPDATE gc_s3_orphans` statements, and exactly one authorized production callsite in `Worker.processBlock`. This closes provenance of the orphan row only; it does not close X1, physical identity, publication fencing or lifecycle identity. |
| R22 | Recovery destroys bytes using data read from the discovery projection, without re-reading the canonical orphan row | **Payload-authority half closed by R22a (2026-08-16) and made structural by R22b (2026-08-17); exact-`P` half still open.** The original defect: `RecoverS3Orphans` took its `S3OrphanInfo` straight from `ListS3OrphansByDay` and used those fields — `StorageClass` included — to resolve the backend and issue the delete, never reloading `gc_s3_orphans`, so a stale or diverged projection row could select the wrong backend for a physical delete or skip the physical delete entirely on a stale `pending_mapping_cleanup`. R22a removed the payload from the discovery type outright (`S3OrphanDiscoveryInfo` carries only `org_id`/`block_id`/`first_seen_at`), so the class is now a compile error rather than a rule; **R22b then dropped the four payload columns from `gc_s3_orphans_by_day` itself (migration 014), so the guarantee no longer rests on "no reader consults them" but on the columns not existing** — a re-wire is a Cassandra rejection, not a code review miss; recovery reloads canonical at `EACH_QUORUM` and re-reads it once more immediately before each irreversible action. R11a separately removes physical-GC authority over the logical forward mapping, so `pending_mapping_cleanup` now finalizes only the orphan row. **Still required and not delivered: an exact `P` match.** The correlation token is `first_seen_at`, which `StartBlockDeleteOrphan` reuses across a lifecycle reset, so matching tokens do not imply the same physical lifecycle; and the delete is still issued by logical block id plus storage class, not by an immutable `P=(B,K)`. A missing canonical row fails closed and retains the cursor when encountered, but repairing or dropping that projection entry remains gated on R20 because an ordinary read is not Paxos settlement. A discovery-token mismatch also fails closed and is conservatively deferred, but its identity/lifecycle resolution is not the same negative-authority problem and remains open under R23/R26. |
| R23 | `storage_class` is rebound to a different bucket, account or backend namespace, or one namespace answers to two class names | **CLOSED BY CONTRACT, not by runtime proof — plus conservative collision detection.** R23a made every layer preserve the persisted class exactly: canonical names validated on the raw value from one shared definition, class/legacy-name collisions rejected, references required to resolve to a registrable class, recovery using the persisted class without following failover. **R23b freezes the contract** — a used class name is permanent, its namespace may die but the name may never be repointed or reused, and new placement takes a new name. Credentials, account/tenant/provider scope, region, endpoint and bucket may change only if they still address exactly the same physical namespace; multi-tenant scope remains immutable even when config cannot infer it. `Config.Validate` rejects two names with the same conservative canonical `(endpoint, bucket)` collision key. That key is not exhaustive identity: it cannot infer credentials or tenant scope, resolve arbitrary DNS/IP aliases, or establish universal path/query/bucket semantics, and may over-reject exotic configurations at startup. The check found the defect live in `config.docker.yaml`, where a legacy `hot` backend named the same MinIO bucket as the default class `hot-minio-local`; the dev profile now preserves both names over separate buckets. **Still not proven at runtime:** an in-place rebind between boots or reuse by a fresh install. A durable fingerprint is optional same-metadata historical-rebind hardening and a namespace claim marker is the cross-install ownership one; neither is required, and both sit outside R23, R24 and X1. **Removing the orphan TTL makes the contract permanent rather than 90-day-bounded — a stale identity then survives indefinitely.** |
| R24 | An ambiguous install outcome is reused or cleaned before its status is settled | **The same ABA as X1, produced by the writer's own cleanup instead of by GC.** R9 has the losing writer best-effort delete its key; R17 forbids a stale *repair* from installing. Neither covers a stale **install**: `W2` loses the CAS to `W1`, schedules cleanup of `P2`, and that DELETE is slow; later GC removes `P1` and `blocks(L)` goes absent; a lingering retry of `W2` re-runs `INSERT … IF NOT EXISTS` with `P2`, which now applies — and the old cleanup DELETE lands on live bytes. **Rule: a minted `P` is a single-use installation identity.** A failed install whose outcome is known lost makes `P` burned and cleanup-eligible; an ambiguous outcome makes `P` **install-uncertain**: it is non-retryable, cannot be reused for another install, and is not cleanup-authorized until serial settlement proves it is not canonical. Settlement proving applied moves `P` to canonical; settlement proving another locator won moves `P` to burned and permits exact-key cleanup. If settlement cannot be established, `P` stays install-uncertain and the safe result is a possible X3 leak, not deletion. |
| R25 | A promote path creates permanent `fs:` references without ever having staged a checked `pub:` | **FIXED structurally** — the published-head repair now stages the `pub:` rows with a fresh attempt ID before finalizing, gated by `TestRepairPublishedSyncCommitBlockDeltaEstablishesHandshakeBeforeFinalizing` and `TestPublishedSyncRepairPartialStageFailureDoesNotFinalize`. Before this fix, `repairPublishedSyncCommitBlockDelta` rebuilt the delta and called `finalizeSyncCommitBlockDelta` directly, bypassing the handshake. The repair now uses the same `StagePublishAttemptReferences` operation as first publication; its rollback is scoped to the fresh repair ID, so a partial repair cannot retract another attempt's liveness. The *primitive* half remains unchanged: `PromotePublishAttemptReferences` does not require a staged row, so a future promote caller can reintroduce the bypass. **R25 was the highest-severity item after R17, and it broke R3's argument as written.** R3 itself remains open: the post-stage canonical-incarnation check does not exist yet. **Reachability, stated precisely rather than as "any retry":** `repairPublishedSyncCommitBlockDelta` first consults `finalizedBlockDeltas`, a per-process two-generation set with 4096 entries per generation (up to 8192 retained pairs) (`sync.go:138-194`), marked only *after* a finalize fully succeeds (`sync.go — finalizeSyncCommitBlockDelta`). A warm same-instance retry therefore short-circuits. The path runs when the retry lands on **another instance**, when **the original finalize failed**, after a process restart, or after eviction. The normal sync path, the auto-merge path, the SeafHTTP path and the v2 path already establish the handshake. **Rule: every known path that can promote permanent `fs:` references must execute Stage before Promote; preservation of the established handshake against concurrent cleanup remains R29/R3.** R3's eventual post-stage check must be added to Stage. |
| R26 | A stale lifecycle's clear removes another lifecycle's discovery row | **Liveness, not data loss — but under a removed TTL it is permanent.** R22 makes recovery *read* the projection non-authoritatively; nothing yet constrains *writes* to it. `DeleteS3Orphan` clears the canonical row and then deletes the projection by timestamp identity — `first_seen_day`/`bucket`/`first_seen_at`/`org`/`block` (`store_cassandra.go`) — and when the caller passes a zero `firstSeenAt` it *resolves that timestamp from whatever canonical row is current* (`store_cassandra.go`), which may already belong to `P2`. Making only the canonical clear conditional on `P` (B.1) therefore leaves the projection half unbound: a delayed `P1` cleanup can still erase `P2`'s discoverability while `P2`'s canonical orphan correctly keeps fencing the writer. The result is a fence recovery cannot enumerate — R19's failure mode reached by a different route. **Rule: every repair or delete of `_by_day` is scoped to the expected `P`, or `P` becomes part of the projection's identity. Prefer the second.** A conditional `DELETE … IF P = P1` buys a conditional Paxos transaction on a structure that is explicitly *liveness, never authorization* — the wrong place to spend consensus. Fold `P` into the discovery row's identity instead and the problem disappears by construction: a stale `P1` clear simply cannot name `P2`'s row. That is the same reasoning that makes A+ attractive in the first place — an identity that is never reused beats a check that has to be remembered. `_by_day` is never authorization, but stale lifecycle work must never remove another lifecycle's recovery discoverability. |
| R27 | R18(a) says "re-project and retry", but the current projection cannot schedule a retry into the future | **R18(a) is the recommended resolution and is not yet implementable.** `gc_s3_orphans_by_day` is partitioned by `(first_seen_day, bucket)` and clustered on `first_seen_at`, and `upsertS3OrphanProjection` always derives the day from the *original* `firstSeenAt` (`store_cassandra.go`). Re-projecting a deferred orphan therefore rewrites it into the same past day the cursor already passed, and the next sweep starts only `gcScanOverlapDays = 2` days back (`store_cassandra.go`). "Re-project and retry until the attempt TTLs expire" has no mechanism behind it. **Separate the two facts the code currently conflates:** `first_seen_at` is an immutable lifecycle fact, and retry scheduling needs a mutable `next_retry_at`. **The separation only works if the partition key derives from the mutable fact.** Adding `next_retry_at` as another clustering column under an immutable `first_seen_day` partition does nothing: the row still lives physically in the August partition, and a scanner working September never reads it. What is required is a discovery structure partitioned on `(next_retry_day, bucket)` and clustered on `(next_retry_at, org_id, block_id, …P)`, or a separate durable retry queue queried by retry time — which is arguably cleaner, since it stops overloading a first-seen index with scheduling. Holding the cursor instead is correct but lets one row block a whole day/partition. Until this exists, A+'s availability bound under R18(a) is not merely long — it is **unbounded**, because nothing automatically returns the orphan to the working set after the `pub:` TTL expires. |
| R28 | A partial orphan appears with no upsert-after-clear, purely from per-value TTL expiry | **R28a MITIGATED for `UpdateS3OrphanAttempt`'s application-clock schedule; row-wide TTL alignment and expiry remain open.** `UpdateS3OrphanAttempt` no longer grants its diagnostic cells a fresh full table TTL; it bounds them to the remaining application-derived schedule. This does not observe Cassandra's coordinator expiry, and other orphan writers may still refresh selected cells. The gate is `TestGC_UpdateS3OrphanAttempt_AnchorsDiagnosticTTLOnFirstSeenAt`, whose red form measures the defect exactly — `TTL(last_error) = 7776000` where the application-derived `first_seen_at` schedule had 60 days remaining. Rewriting the identity columns to realign them was rejected: `representation_id`, `external_sha1` and `recovery_phase` all have other conditional writers, so echoing back a just-read value trades a TTL race for a lost-update race including a `recovery_phase` regression. **What remains is the TTL itself**: the row still expires, and expiry still destroys the durable record that an object needs deleting. Removing it is the four-change package below, and it cannot ship alone — `gcS3OrphanInitialScanLookbackDays = 90` is pinned to this same value, so dropping the TTL without redefining the cold-start horizon makes any orphan older than 90 days permanently invisible after a cursor loss, which is R27's unsolved partitioning problem. **The original finding, unchanged:** R19's failure mode has a second cause, and it explains why the TTL package is indivisible. Cassandra applies `default_time_to_live` per written value, and an `UPDATE` rewrites only the values it touches, with a fresh expiry. `UpdateS3OrphanAttempt` writes `last_attempt_at`/`retry_count`/`last_error` and leaves `storage_class`, `first_seen_at` and `recovery_phase` untouched. A retry on day 89 therefore pushes the diagnostic values to day 179 while the identity values still expire on day 90, and the `_by_day` projection — never rewritten — expires with them. Between those dates the row has a live primary key, no `storage_class`, no `first_seen_at` and no discovery entry. Both fence reads select only `block_id` (`block_references.go:851`, `1138`), which a partially expired row still returns, so the writer stays fenced while `GetBlockS3OrphanInfo` reads nulls. **Consequence for the wording elsewhere in this document: the 90-day TTL is not a ceiling on the row.** It bounds each value independently, and retries extend the diagnostic ones. **Consequence for R19:** partial orphan state arises both from an unconditional upsert after a clear *and* from per-value TTL skew, so making non-creating mutations conditional and removing the TTL are both required — neither alone closes it. |
| R29 | A concurrent publisher can retract another publisher's `pub:` handshake | Sync previously used `targetCommitID` as the publication attempt ID, so concurrent requests for the same target shared `pub:<targetCommitID>`. A loser could delete the winner's row during CAS-conflict cleanup, and a partial `StagePublishAttemptReferences` rollback could do the same before CAS. The fix uses a fresh `publishAttemptID` per sync publication delta and retains refs for ambiguous CAS or post-CAS outcomes. **Required outcome:** cleanup may address only the current request's unique attempt; an uncertain CAS result retains its refs for retry, and a confirmed same-target winner enters idempotent repair. R25 remains structurally fixed, but preservation of an established handshake is an open X1/R3 criterion until this identity/outcome contract is evidenced in the integration leg. |
| R30 | Superseded or abandoned publication attempts retain `pub:` rows until TTL | **Liveness/retention, not data loss.** After a publisher wins HEAD and crashes, a later repair creates `pub:<repair-uuid>` because the original attempt ID is not persisted. Promote removes the repair ref, but cannot identify the crashed publisher's `pub:<original-uuid>`. A complete repair stage followed by another crash/finalize failure leaves the same shape for the repair UUID; partial stages now roll back their own fresh ID. Each row is bounded by the 35-day TTL, but repeated failed repairs can create successive retained attempts and extend aggregate retention. **Required decision:** accept this per-attempt bounded retention as the cost of non-shared identity, or persist publication-attempt lineage so successful repair can retire superseded refs. This is separate from R29's safety fix and must not be described as X1 closure. |
| R31 | A possibly successful published HEAD has no durable repair path after its `pub:` TTL expires | **Blocker for X1 safety closure.** A CAS timeout or a post-CAS block-reference failure can leave `HEAD = T` while only `pub:<attempt>` protects the blocks. Sync repair currently runs from a later idempotent client request; it is not a durable worker or queue. If no request arrives before the 35-day TTL, the temporary liveness can expire while `fs:` refs are still absent, allowing GC to see zero refs. **Required outcome:** settle the CAS serially and finalize when published, or persist a durable pending-publication/reconciliation record that a background worker can process independently of client retries. |

> **R19 implementation note:** `UpdateS3OrphanAttempt` carries the expected
> `first_seen_at` and uses `IF first_seen_at = ?`, which closes resurrection and
> mismatches against a differently-tokened row. `StartBlockDeleteOrphan` can reset
> an existing row while preserving that token, so the existing-row lifecycle reuse
> remains open. R21 is now closed: `StartBlockDeleteOrphan` is the sole production
> creator, and the untagged authority-surface gate protects that invariant. See
> checklist item 10 and the two R19 integration gates below.

> **R28 implementation note:** this is a mitigation for
> `UpdateS3OrphanAttempt`'s application-derived schedule, not row-wide or
> coordinator-clock closure. Its diagnostic cells no longer receive a fresh full
> table TTL. `StartBlockDeleteOrphan`,
> `MarkS3OrphanMappingCleanupPending` and projection writes still refresh selected
> cells with the table default TTL, and the actual Cassandra coordinator expiry is
> not observed by this helper. `TestS3OrphanTTLConstantMatchesSchema` covers the
> base schema and `TestGC_S3OrphanEffectiveTTLMatchesMigrationChain` covers the
> effective migrated schema. Row-wide TTL alignment remains open.

R8, R13 and R15 decide whether Option B is viable. R16, R17, R19, R20, R22, R23, R24, R25,
R26, R28, R29, R31, R3, R9, R10 and R12 are common safety-closure criteria — R16 and R20 become newly
load-bearing when claim IDs move from candidate identity to per-attempt UUIDs, and R17 is one
of the direct same-physical-identity paths that reopens X1; R24 is the corresponding
stale-install form. **R25 was the one that invalidated a conclusion this document previously
drew** rather than adding a new requirement to it: R3's "the gap is one function, not two"
was wrong, because one production path promoted without ever staging. It is now fixed and
gated; R3 itself remains open, and the structural fix is not evidence toward it.
**R18 is A+-specific and is the one open question A+ does not inherit from X2 for free;
R27 is what makes its recommended resolution implementable at all.**
R21 is now closed, so B and any R18 resolution that treats the orphan as a durable
authorization certificate no longer have this provenance prerequisite. The conservative
A+ option (a) does not depend on that inference.

Note the shape of the last five: R26 and R28 both reach R19's failure mode — a fence no
sweep can enumerate — by routes R19 does not cover (a stale clear of another lifecycle's
discovery row, and per-value TTL expiry). R29 is separate but related: it can remove the
publication fence before permanent `fs:` refs take over. R30 is a bounded
availability/retention consequence of fixing R29 with fresh identity, not a safety-closure
criterion. R31 is the safety failure where that temporary fence expires before durable
`fs:` repair. Treat "partial or undiscoverable orphan", "retractable publication handshake",
"superseded attempt retention" and "non-durable published repair" as failure classes, not
single call-site defects.

**The invariant these add up to.** `P` has a monotonic authority lifecycle, and no
authority transition ever runs backwards. An install outcome can be uncertain without
granting authority to clean or reuse the key:

```text
                         ┌──── applied ───────→ canonical → condemned → burned forever
fresh → install-uncertain
                         └──── lost ──────────→ burned / cleanup-eligible
                       (unsettled: stay here; no reuse, no cleanup)
```

Never `condemned → canonical`, never `burned → canonical`, and never
`install-uncertain → cleanup` without settlement proving the install lost. R17 forbids
the first through a stale existing-incarnation path; R24 forbids the second through a
stale install.

---

## R12 — the one-serial-domain inventory, corrected

An earlier count named six conditional statements on the `blocks` partition. **There are
eleven.** R12's own rule is that one statement left behind invalidates the guarantee for
all the others, so the count is not a detail.

| # | Statement | Location |
|---|---|---|
| 1 | `upsertBlockMetadataInsertWithRepresentationFn` — first-writer `INSERT … IF NOT EXISTS` | `internal/db/block_references.go:169` |
| 2 | `claimReleasedBlockStubForRepairFn` | `internal/db/block_references.go:204` |
| 3 | `deleteRepairClaimedBlockStubFn` | `internal/db/block_references.go:217` |
| 4 | `deleteClaimedBlockStubFn` | `internal/db/block_references.go:237` |
| 5 | `backfillBlockSHA1Fn` | `internal/db/block_references.go:253` |
| 6 | `backfillBlockRepresentationIDFn` | `internal/db/block_references.go:262` |
| 7 | `ReleaseBlockDeleteClaim` | `internal/db/block_references.go:1180` |
| 8 | `ReleaseStaleBlockClaim` | `internal/gc/store_cassandra.go` |
| 9 | `ClaimBlockDelete` | `internal/gc/store_cassandra.go` |
| 10 | `ReleaseBlockClaim` | `internal/gc/store_cassandra.go` |
| 11 | `FinalizeBlockDelete` | `internal/gc/store_cassandra.go` |

Statements 4, 5, 6, 7 and 8 were absent from the earlier list.

**And the rule does not stop at `blocks`.** Under any drop-row-first design,
`gc_s3_orphans` carries the durable physical-delete authorization after the canonical row
is dropped, so its conditional operations need the same **global `SERIAL` discipline**.
The two partitions do not share a Paxos log and `SERIAL` does not make orphan insertion
and canonical-row deletion atomic; R13 remains a real two-step crash window. The protocol
must provide the ordering and recovery checks explicitly.

The orphan partition carries four remaining conditional statements in the
`StartBlockDeleteOrphan`, mapping-phase and attempt-update lifecycle plus the two
unconditional `DELETE` statements in the active `DeleteS3Orphan` method
(`internal/gc/store_cassandra.go`), which B.1 requires to become conditional.
The former `DeleteBlockS3Orphan` helper was removed by R21. Every relevant LWT in both partitions uses
serial phase `SERIAL`, with `EACH_QUORUM` as the default regular commit for global
claim/orphan visibility (`ALL` is stricter but not required for intersection with an
explicit `LOCAL_QUORUM` writer read). The regular consistency for `INSTALL` remains an
explicit open decision: `LOCAL_QUORUM` provides local read-your-write behaviour, while
`EACH_QUORUM` provides ordinary visibility in every DC before acknowledgement. Do not
summarize this property as `QUORUM or higher`. This is a consistency discipline, not
cross-table atomicity.

There is a related defect already filed against the same decision:
`ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`, documented at length in
`internal/gc/store_cassandra.go` — `ReleaseStaleBlockClaim` reads claim state
with an ordinary read that can miss a cross-DC claim or an accepted-but-uncommitted
Paxos value. The comment there explicitly defers the fix to "the serial-domain decision
X1 has to make anyway". This is that decision.

---

## The orphan TTL package — four changes, not one

`gc_s3_orphans` carries `default_time_to_live = 7776000` (90 days) on the canonical table
and on the `_by_day` projection. A persistently failing delete therefore has its recovery
record expire silently.

The physical-delete ABA component would remain closed under B even then — with no `blocks` row the
writer probes `NeedsPut`, mints a fresh key, and a stale DELETE of the old key can never
reach it. What expiry destroys is **the durable record that the old key still has to be
deleted**: the bytes leak with nothing pointing at them, and under B the
single-outstanding-delete invariant silently becomes untrue. X1 as a whole is not closed
by that key separation alone.

The cold-start recovery horizon is deliberately pinned to the TTL:
`gcS3OrphanInitialScanLookbackDays = 90` (`internal/gc/worker.go:1644`), whose comment
says to match it "so the first pass can still see every live orphan row". Drop the TTL
and leave the horizon alone and any orphan older than 90 days becomes permanently
invisible after a cursor loss — precisely the case where the orphan matters most.

The package is: remove the canonical TTL; remove the `_by_day` projection TTL; redefine
the cold-start horizon and cursor semantics; and guarantee discovery of an arbitrarily
old orphan. Expiry, if kept at all, must raise an alert instead of deleting the row.

**And the TTL is not a 90-day lease on the row — see R28.** Cassandra expires each written
value independently, so `UpdateS3OrphanAttempt` refreshes only the three diagnostic columns
it writes and leaves `storage_class`, `first_seen_at` and `recovery_phase` on their
original schedule. A late retry therefore produces exactly R19's partial orphan by ordinary
expiry: a live primary key that still fences the writer, with no identity columns and no
`_by_day` row. That is the reason the four changes are indivisible rather than a list to
pick from — removing the canonical TTL while leaving the projection TTL, or vice versa,
manufactures the same undiscoverable fence on purpose.

Note the cursor's current shape is itself part of this: the sweep advances the day cursor
only when the pass had no `phaseErr` (`worker.go:1602`), and the "still referenced"
branch deliberately does not set one — so that row falls out of the working set and is
never revisited. Under B that branch changes meaning entirely (B.1).

---

## Call-site inventory

A conceptual diff, not an implementation list.

| Surface | Today | What minted keys need |
|---|---|---|
| `hashToKey` / `StorageKeyForHash` | Deterministic locator | Prefix/format helper only; never the authoritative live locator. The live physical tuple is persisted `(B, storage_key)`; the concrete field is `storage_class` only if R23 selects that as the immutable `B` |
| `DeleteBlock(hash)` / `BlockStoreDeleter` | Derives the key from the hash | Delete the exact key captured in the GC work. **No exact-key delete exists today** |
| `GetBlock` / `BlockExists` / `PutBlock` / `PutBlockData` / `PutBlockAuto` / `PutBlockAutoDirect` | All derive via `hashToKey` | Take an exact key, or resolve one first. Exact-key `Put`/`Exists`/`Get` variants already exist and are the model |
| `CheckBlocksExist` (canonical reader, primary `/check-blocks`) and `CheckBlocks` / `CheckBlocksParallel` (legacy fallback) | Resolve the canonical location, then HEAD a key derived from the hash | Keep the physical existence check: `L → DB resolves (B, storage_key) → HEAD exact storage_key → exists/missing`. This is a derived-HEAD to DB-resolved exact-key change, not a database-only dedup answer. |
| `S3Store.Put(ctx, blockID, …)` | Second derivation layer: `s.key(blockID)` on what callers already pass as a key | Make the key parameter mean a key |
| `canonical_block_reader.go:238` | Rejects persisted ≠ derived | Use the persisted key; validate org/hash/format instead |
| `upload_reuse.go` — `ResolveNeedsPutBlockStore` **and** the `Reusable` branch of `StoreUploadedBlockForProbe` | Two reject sites; the second also repair-PUTs at the derived key | Immediately before repair PUT, re-read the canonical row and require the same `storage_key`, no destructive/repair claim and no orphan. Repair/reuse that active key; mint only when a new incarnation is allowed; never repair a condemned incarnation (R10) |
| `UpsertBlockMetadata` | `INSERT … IF NOT EXISTS` inheriting the session serial level | Store the exact key; raise the serial phase to `SERIAL` so one incarnation wins globally (R9) |
| `ClaimBlockDelete` / `FinalizeBlockDelete` | Conditional on `gc_state` / `gc_claim_id` only | Bind the life: `AND backend_identity = B1 AND storage_key = K1`, with fresh per-attempt claim identity (R14/R16) |
| `ReleaseBlockClaim`, `ReleaseStaleBlockClaim`, `ReleaseBlockDeleteClaim`, the stub-repair pair, both backfills | Conditional statements on `blocks`, inheriting the session serial level | Serial phase `SERIAL` — the one-serial-domain rule admits no exceptions on this partition (R12) |
| `gc_s3_orphans` (+ `gc_s3_orphans_by_day`) | Canonical `gc_s3_orphans` has PK `((org_id, block_id))` and carries `external_sha1`/`recovery_phase` (migration 007); R11b-1 removes the redundant `representation_id`, while R22b makes `gc_s3_orphans_by_day` identity-only with PK `((first_seen_day, bucket), first_seen_at, org_id, block_id)` (migration 014) | Add exact `(B, storage_key)` to both; the concrete backend field is `storage_class` only if R23 selects it as `B`. Recovery and `ListS3OrphansByDay` must not `hashToKey`; the clear becomes conditional on both tuple fields |
| `StartBlockDeleteOrphan` | `INSERT … IF NOT EXISTS`, then **resets** an existing row to `pending_s3` | Return/classify applied, same-key idempotent, different-key conflict and ambiguous error; never overwrite/reset, never release on an ambiguous result, and postpone only a confirmed different lifecycle (B.1) |
| `gcS3OrphanInitialScanLookbackDays = 90` | Cold-start horizon, matched to the TTL | Redefine together with the TTL removal |
| `RecoverS3Orphans` | Re-verifies `BlockExists(L)` and `BlockHasReferencesGlobal(L)` | **A+:** retain `BlockHasReferencesGlobal(L)` and the canonical-row check, then delete exact validated `P1`. **B:** the orphan may carry the historical authorization for `P1`, but recovery still validates the canonical locator and legacy/keyless rows fail closed. |
| `block_id_mappings` physical-GC cleanup | Before R11a, `processBlock` and `RecoverS3Orphans` deleted the forward mapping after physical cleanup | **R11a/B.3 delivered:** physical GC never deletes the logical mapping. A future logical-death reaper is intentionally separate. |
| `StagePublishAttemptReferences` | Insert only | Post-check the active canonical incarnation. **A+:** any orphan for `L` blocks success. **B:** the canonical `P` must differ from the pending orphan `P1`; both also require no destructive/repair claim. Roll back this call's `pub:` rows on failure. |
| `RegisterUploadedBlock` | Write `up:` then check the logical fence, then `UpsertBlockMetadata`; the provisional ref is **not** rolled back when the fence is found active (`fs_helpers.go:989-1003`) | Split repair from install (**R17**): a repair may only update a row that still names the same `P`, never create an absent one. Make the path tuple-aware: reuse/repair the active canonical `P`, block a condemned/deleting one, mint only for a genuine new incarnation. And settle what the surviving `up:` row does to recovery (**R18**) |
| `UpdateS3OrphanAttempt` | Reads the expected `first_seen_at`, then performs `UPDATE … IF first_seen_at = ?` with a bound TTL; absent or differently-tokened rows are no-ops, while an existing-row lifecycle reset can still reuse the token | Guard the actual non-reusable lifecycle identity `P` and fail when the row is gone or reset to another lifecycle (**R19**) |
| `RecordS3Orphan` | **Removed by the R21 authority-surface PR.** It was a second `INSERT … IF NOT EXISTS` creator of `gc_s3_orphans` with no production caller. | Test fixtures use `StartBlockDeleteOrphan` and `UpdateS3OrphanAttempt`; the untagged R21 gate requires exactly one production creator (**R21 closed**) |
| `RecoverS3Orphans` discovery | **R22a implemented, R22b made structural:** `ListS3OrphansByDay` returns only `(org_id, block_id, first_seen_at)`, and migration 014 dropped the projection's payload columns so no other value exists to return; recovery reloads canonical state at `EACH_QUORUM` and uses it for phase and backend selection. R11a removes mapping deletion from both recovery phases. A second canonical reload guards each irreversible action or orphan finalization. | Missing/error canonical reads and discovery-token mismatches fail closed and retain the cursor when the row is encountered. Missing-canonical repair remains gated on R20's serial-settlement requirement; token-mismatch cleanup remains conservatively deferred until lifecycle/projection identity rules are finalized. This is not exact `P` matching or lifecycle exclusion; R20/R26 remain open. |
| `DeleteS3Orphan` | The fence clear GC actually runs: unconditional `DELETE` (`store_cassandra.go`) from `processBlock` and all three recovery exits (`worker.go:1261, 1411, 1429, 1584`); its projection half deletes by timestamp identity and resolves a zero `firstSeenAt` from whatever canonical row is current | Condition the canonical clear on the expected `P`, so a delayed clear from `K1`'s lifecycle cannot lift a fence belonging to a later one (B.1) — **and bind the projection clear the same way** (**R26**), or the stale lifecycle still erases the newer one's discoverability |
| `upsertS3OrphanProjection` | Always derives `first_seen_day` from the original `firstSeenAt` (`store_cassandra.go`), so a re-projection lands in the day the cursor already passed | Give retry scheduling its own mutable fact (`next_retry_at`), separate from the immutable `first_seen_at`; without it R18(a)'s "re-project and retry" has no mechanism (**R27**) |
| `repairPublishedSyncCommitBlockDelta` | Before R25, rebuilt the delta and called `finalizeSyncCommitBlockDelta` directly, promoting `fs:` without establishing `pub:` | Rebuild → fresh-ID `StagePublishAttemptReferences` with rollback scoped to that repair → R3 post-check → promote (**R25**) |
| `PromotePublishAttemptReferences` | Calls `registerPermanent()` and deletes whatever attempt rows exist; never requires a `pub:` row to be present (`internal/db/block_references.go — PromotePublishAttemptReferences`) | Either keep it dumb and fix the callers (preferred, see R25), or require durable proof that this attempt's `pub:` rows were validated |
| `DeleteBlockS3Orphan` | **Removed by the R21 authority-surface PR.** It was a second unconditional orphan `DELETE` with no caller anywhere in the repo. | The active cleanup surface remains `DeleteS3Orphan`; its lifecycle binding is separate R26 work |
| `B` → backend | Today this is the logical `storage_class` resolved through `m.backends[className]` (`internal/storage/storage.go:493`); bucket and endpoint come from config (`internal/config/config.go:3526,3532`) | Define `B` as an immutable namespace identity. `storage_class` is acceptable only under R23's append-only/non-reuse contract; otherwise persist an immutable `backend_id` and define `P = (backend_id, storage_key)` |

---

## Open questions to settle before anything is accepted

Item 0 comes first because it is a direct same-physical-identity path that reopens X1;
R24 is the corresponding stale-install form. The rest are ordered so the cheapest
decisions come first.

0. **R17 — repair must never become install.** One of the direct same-physical-identity
   paths that reopens X1. Split the metadata path in two: `REPAIR(P1)`
   updates a canonical row that still names `P1` and **may not create an absent row**;
   only `INSTALL(P2)`, on a key minted in this attempt, may `INSERT … IF NOT EXISTS`.
   Settle this before anything else, because "revalidate before the repair PUT" reads
   like a fix and is not one. Settle the three linearization shapes with it — the
   pre-install fence check, the post-install success check, and the post-stage `pub:`
   check are different observations and the ADR must say so.
0b. **R25 — every known promote path must first establish `pub:`. ✅ SETTLED
   structurally by implementation.** It was the other place where the design's argument,
   not just its implementation, was untrue. The fix shape chosen was **fresh-ID stage on
   repair**: a published-head repair uses `StagePublishAttemptReferences`, and any partial
   rollback is scoped to that repair's unique ID.
   Gated by `TestRepairPublishedSyncCommitBlockDeltaEstablishesHandshakeBeforeFinalizing` and
   `TestPublishedSyncRepairPartialStageFailureDoesNotFinalize`. **What remains open here
   is deliberate:** `PromotePublishAttemptReferences` still registers without requiring a
   staged row, so the invariant lives in the callers. If a future promote caller is added,
   this is the item to re-read. The "checked" half remains R3 — see item 8.
0c. **R29 — publication attempt identity and CAS outcome.** `targetCommitID` must not be
   the referrer identity for concurrent requests. Each sync publication needs a fresh
   attempt ID carried through Stage/Promote/cleanup. A confirmed CAS loser may clean
   only its own attempt; a same-target winner must enter idempotent repair, and an ambiguous
   CAS or post-CAS error must retain its refs and return retryable. The unit gate covers the
   fresh-ID shape; an integration leg still needs to exercise the cleanup policy and two
   concurrent publishers against Cassandra.
0d. **R30 — superseded publication-attempt retention.** A repair UUID is intentionally
   fresh, so it cannot identify and remove a crashed original publisher's UUID ref. The
   original `pub:` row remains a conservative liveness pin until its 35-day TTL expires.
   Decide whether that bounded retention is acceptable or whether publication-attempt
   lineage must become durable; do not treat the repair's successful `fs:` promotion as
   proof that the old temporary row was retired.
0e. **R31 — durable repair after a possibly successful publication.** A CAS timeout or a
   post-CAS finalize failure can leave a visible HEAD protected only by a TTL-bound `pub:`
   row. The current sync repair runs only when a later client request reaches the
   idempotent path. X1 cannot close until a durable queue/record or equivalent background
   reconciliation can converge `pub:` to permanent `fs:` without relying on that retry.
1. **A+ claim identity.** Replace the candidate-derived claim with a fresh UUID per
   worker attempt; carry `(P, claimID, claimed_at)` through claim, release, finalize,
   stale takeover and candidate cleanup.
2. **A+ CAS outcomes (R15/R16/R20).** Distinguish applied, same-physical-identity
   idempotent, different-identity conflict, existing fresh owner, stale owner and
   ambiguous results. Settle ambiguity in the serial domain; never release, finalize or
   complete a candidate on a non-serial consistency read used as negative authority. This
   subsumes `ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01`, which was explicitly deferred to this
   decision.
3. **R21 — orphan authorization provenance. ✅ CLOSED.** `RecordS3Orphan` was removed
   from the interface, Cassandra store and mock; `DeleteBlockS3Orphan` was removed as
   its unused destructive twin. Fixtures now use `StartBlockDeleteOrphan`, and the
   untagged authority-surface gate requires exactly one production orphan creator,
   conditional canonical orphan updates, and the authorized `Worker.processBlock`
   callsite. The conservative A+ option (a) does not depend on historical
   authorization from the orphan, but B and R18(c) can now rely on the provenance
   invariant when their other criteria are implemented.
4. **R18 — post-authorization attempt-reference poisoning.** Decide whether A+ keeps
   `BlockHasReferencesGlobal(L)` in recovery. As written, a rejected or abandoned `up:`
   (48 h) or `pub:` (35 days) row can veto recovery of the key it was rejected for, and
   the refusal branch drops the orphan out of the working set. **Take the conservative
   option (a) for a first closure** — keep the re-check, stop dropping referenced orphans,
   re-project and retry — and treat the availability cost as the price. This is the one
   A+ question X2's closure does not answer. **It cannot be accepted without R27**, which
   supplies the mechanism "re-project and retry" currently lacks.
4b. **R27 — deferred-orphan retry scheduling.** Separate the immutable `first_seen_at`
   from a mutable `next_retry_at`, and **partition the discovery structure on the mutable
   one** — `(next_retry_day, bucket)`, or a separate retry queue read by retry time. A
   `next_retry_at` added as a clustering column under an immutable `first_seen_day` leaves
   the row in the partition the cursor already passed and changes nothing. Re-projecting
   on today's key has the same defect, so option (a) has no bound at all until this exists.
5. **R22 / R26 — the discovery index is bound to `P` in both directions.** Recovery must
   reload the canonical orphan and match `P` exactly before any physical delete; a
   projection mismatch repairs the index and never destroys. Symmetrically, every repair
   or delete *of* the index is scoped to the expected `P`, or a stale lifecycle's clear
   erases a newer lifecycle's discoverability.
6. **R23 — immutable backend namespace (settled).** `storage_class` is the accepted
   `B` under the greenfield append-only, never-rebind and never-reuse deployment
   contract. Credentials, account/tenant/provider scope, region, endpoint and bucket
   may change only if the same physical namespace remains addressed. Configuration's
   endpoint+bucket key conservatively rejects canonical collisions; it is not exhaustive
   identity and may over-reject exotic configurations. A durable fingerprint is optional
   same-metadata historical-rebind hardening; a namespace claim marker is the cross-install
   ownership one. Both are outside R23, R24 and X1. No `backend_id`, historical migration
   or preflight is required.
7. **R24 — install identity is single-use.** A minted `P` whose install is known lost is
   burned and cleanup-eligible; an ambiguous install becomes `install-uncertain`, cannot
   be reused or cleaned, and stays there until serial settlement proves applied or lost.
   Only a proven-lost identity may be cleaned, and retries that still need an incarnation
   mint fresh. This bounds R20's "idempotent retry of the same CAS": it is safe for claim
   and orphan statements, but not for an install whose history is uncertain.
8. **R3 — the `pub:` post-check.** Require an active canonical row, no destructive or
   repair claim, and a usable non-orphaned key. A failed post-check must roll back the
   `pub:` rows staged by that call, with rollback failure treated as recovery/error rather
   than publication success.
9. **R10 — never repair a condemned incarnation.** Revalidate the exact canonical key,
   claim state and orphan state immediately before repair PUT. Necessary but not
   sufficient on its own — see R17.
10. **R19 / R28 — partial and undiscoverable orphans. ⚠️ PARTLY SETTLED.**
   `UpdateS3OrphanAttempt` now carries the expected `first_seen_at` and performs
   `IF first_seen_at = ?`, so an absent row and a row with a different observed
   token are non-applicable. It also floors its application-clock schedule, skips a
   future `first_seen_at`, and avoids dynamic prepared-statement text. **Three things
   are still open, and none is cosmetic.** First, `StartBlockDeleteOrphan` can reset
   an existing row while preserving `first_seen_at`, so a delayed P1 update can still
   match P2; a fresh lifecycle identity is needed. Second, the rule binds *every* non-creating
    mutation, and only this one has been done: the phase/clear statements still key on
    `(org, block)` rather
   than on the expected `P` — they cannot key on `P` until the physical identity is
   persisted, which is the A+ package. Third, the actual coordinator-clock expiry is
   not proven by the application schedule, and the TTL itself remains, so an orphan
   can still expire and take its recovery record with it; removing it needs the
   cold-start horizon and cursor semantics redefined at the same time, which is
   R27's unsolved partitioning problem, not a one-line schema change.
11. **R12 — one serial domain**, across eleven `blocks` statements and the
   `gc_s3_orphans` partition; use `SERIAL` for the serial phase and `EACH_QUORUM` as the
   default regular visibility level for the global fence.
12. **B-specific R13/B.1.** If B is pursued, settle the tuple-aware probe/install rules and
   rewrite the recovery argument so `P1` authorization is not confused with `L` liveness.
13. **R9 — the losing PUT.** `SERIAL` picks a canonical winner, but the loser's minted key
   is not in `blocks` or an orphan row. The losing writer still knows that exact key and
   should best-effort delete it or persist it for cleanup. A crash after PUT but before
   any durable intent remains the existing X3 leak; do not make bucket inventory a
   prerequisite for X1.
14. **B.3 — the SHA-1 mapping.** **Delivered by R11a:** decouple physical GC and
    accept the metadata growth for now; a logical-death reaper remains optional follow-up.
15. **The TTL package**, all four changes together — and note R19 first: removing the TTL
    without closing the resurrection path turns a partial resurrected orphan into a
    permanent, undiscoverable writer fence.
16. **GC drain capacity.** The queue loop in `internal/gc/worker.go` is strictly serial at
    `gc.batch_size = 100` on a `gc.worker_interval = 30s` tick. Any design that fences
    globally adds per-block WAN LWTs to that loop. This is a throughput question, not a
    latency one, and "GC is not the hot path" does not answer it. Independent of which
    option wins.
17. **Fail-closed observability.** `EACH_QUORUM` couples GC availability to every DC, and
    at RF 1 the tolerance is zero. Correct under "when in doubt, do not delete", but a
    silent stall defers the user/library/org purge SLAs and leaves space uncollected.

**When this option stops being small.** If R13's predicate cannot be expressed without a
second physical-identity table, or R8 turns out to need a conditional `K1 → K2` update, or
the `pub:` post-check cannot be made to hold, then the missing piece is a publication
frontier — and that is what the abandoned r3 document already contains. In that case
retrieve it from the reference branch rather than inventing a third protocol.

---

## Executable evidence — what exists and what does not

X2 did not close because its analysis was convincing; the analysis had existed for weeks.
It closed when a three-DC fixture plus two mutations went red on live data. X1 has had no
such instrument at all: every race above was prose. That is the difference between the two
blockers today, and it is a bigger difference than the difficulty of either design.

All instruments live in `internal/api/sync_publish_handshake_test.go` unless noted.

| Leg | Property | Status |
|---|---|---|
| **R25 handshake** | The idempotent-repair entry point establishes `pub:` before promoting permanent `fs:` — `TestRepairPublishedSyncCommitBlockDeltaEstablishesHandshakeBeforeFinalizing` drives the real helper | **GREEN** since the R25 fix. **Mutation:** revert `repairPublishedSyncCommitBlockDelta` to build the delta and call `finalizeSyncCommitBlockDelta` directly ⇒ **RED**. `TestPublishedSyncRepairPartialStageFailureDoesNotFinalize` also keeps a failed repair stage from reaching promote. |
| R25 primitive | `PromotePublishAttemptReferences` registers without requiring a staged row — `TestPromotePublishAttemptReferencesDoesNotRequireAStagedRow` | PASS — pins current behaviour; invert it if promote is made to demand proof |
| Fix shape | A fix that always restages must not make every warm retry pay the full reconciliation — `TestRepairSkipsEverythingOnceThisProcessFinalized` | PASS |
| Staged shape | `stage → finalize` satisfies the handshake and promotes exactly what it staged — `TestStagedPublicationShapeSatisfiesTheHandshake` | PASS |
| R29 identity | Two sync publications of the same target receive distinct attempt IDs — `TestSyncPublicationAttemptsUseDistinctIDsForTheSameTarget` | PASS — structural gate; concurrent Cassandra cleanup still needs integration evidence |
| R29 outcome | A same-target CAS loser and an ambiguous CAS/post-CAS result retain the correct publication liveness | not built — needs a controlled CAS/cleanup integration leg |
| R30 retention | A successful fresh-UUID repair retires the original crashed publisher's `pub:` refs | not built — availability/retention follow-up; requires durable attempt lineage or an explicit accepted 35-day retention bound |
| R31 durable repair | A possibly successful published HEAD converges to permanent `fs:` without a later client request | **BLOCKER / not built** — requires a durable reconciliation record or background repair independent of the 35-day TTL |
| **R19 non-creating** | `UpdateS3OrphanAttempt` may not resurrect a cleared orphan — `TestGC_UpdateS3OrphanAttempt_DoesNotResurrectClearedOrphan` (integration; the mock never had the defect, so a unit test could not gate it) | **GREEN.** Red against the pre-fix statement: `UpdateS3OrphanAttempt resurrected a cleared orphan row` |
| **R19 timestamp guard** | A delayed P1 attempt may not mutate a row with a different observed `first_seen_at` — `TestGC_UpdateS3OrphanAttempt_RejectsDifferentLifecycleToken` | **GREEN for differing tokens.** Existing-row lifecycle reset reuse remains open |
| **R28a application schedule** | `UpdateS3OrphanAttempt` must not schedule diagnostics beyond its application-derived expiry — `TestGC_UpdateS3OrphanAttempt_AnchorsDiagnosticTTLOnFirstSeenAt` (integration) plus `TestS3OrphanRemainingTTLSecondsKeepsOneExpirySchedule` (unit, property loop) | **GREEN for the tested schedule.** Not proof of coordinator-clock expiry alignment |
| **R28 schema** | The code's base-schema TTL assumption must match the effective migrated schema — `TestS3OrphanTTLConstantMatchesSchema` (base), `TestGC_S3OrphanEffectiveTTLMatchesMigrationChain` (schema integration), plus `TestGC_UpdateS3OrphanAttemptMatchesEffectiveTTL` (fresh-row behavior) | **GREEN for the current migration chain and update path.** A future TTL package must update both the constant and these gates |
| R28b other orphan writers | All canonical/projection writers must preserve one row-wide expiry schedule | **OPEN** |
| R28 expiry | An orphan whose recovery keeps failing must not lose its durable record | not built — the TTL is still in the schema; removing it is blocked on R27's retry-scheduling partitioning |
| R3 safety | A block under an active delete fence must not gain a permanent `fs:` reference through the retry path | not built — needs a second instance for a cold memo, and cannot go green until R3's post-check exists on any path |
| Physical ABA | An authorized DELETE landing after a byte-identical rematerialization destroys live bytes | not built — needs deterministic control of the S3 delete between authorization and landing |

**What the unit gate does not cover.** It gates the idempotent-repair handshake directly and
pins the intended `stage → finalize` invariant. It is **not** control-flow coverage of the
normal and auto-merge production branches: the staged-shape test calls
`stageSyncCommitBlockDelta` itself, so a refactor that dropped staging from
`handleSyncHeadPromotion` or `tryAutoMergeSyncHeadPromotion` would not turn it red. Those
branches run inside the HEAD CAS retry loop and need a DB session, which puts them in the
integration leg. Do not cite the unit file as proof that every production path stages.

**What R25's closure does and does not mean.** It restores the structural handshake, so
every known promote path now establishes `pub:` and R3's post-check — once written — can
apply to all of them instead of being bypassed by one, subject to R29's ownership and
outcome-preservation contract. It does **not** add that check. The
publication TOCTOU is still open exactly as before: between `stage pub:` and `finalize`,
GC can still claim and authorize. R31 adds the independent requirement that a visible HEAD
must have durable repair beyond the temporary TTL. Read the status as **R25 fixed, R3/R31
open, X1 open**, and
do not let a structural fix be quoted as evidence toward closing X1.

**Why the physical-ABA leg is last rather than first.** Its obligatory mutation — derived
key instead of minted key must go red — presupposes that minting exists. Today keys are
always derived and `canonical_block_reader.go:238` actively rejects a persisted key that
differs from the derived one, so there is no correct version to mutate away from. Its red
half can be built against today's code; its green half arrives with A+. That is unlike X2,
where the fix was a consistency level on existing code and the mutation was a one-liner.

Until the physical-ABA leg exists in at least its red half, "X1 closed" remains a claim in
a document — which is the failure mode this document has spent several revisions avoiding.

---

## Status of the abandoned design

`docs/gc-x1-x2-generation-fence-final` holds the r3 generational-fence ADR (~8.9k lines
of protocol specification) and PR #166, closed unmerged on 2026-08-14. It is retained as
**investigative reference only**. Nothing in it is accepted, frozen, or scheduled, and no
document should cite it as a design of record. Its lasting contributions are recorded
above: never-reused physical keys as the thing that closes the physical-delete ABA
component (not X1 by itself), the one-serial-domain rule, and the exact-key recovery
requirement.
