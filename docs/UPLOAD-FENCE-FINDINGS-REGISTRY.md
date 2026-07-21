# Upload-Fence Audit — Findings Registry

**Date:** 2026-07-21
**Origin:** eight successive audits of the GC upload-fence work, 2026-07-20/21.
**Companion:** [GC-UPLOAD-FENCE-PR-PLAN.md](./GC-UPLOAD-FENCE-PR-PLAN.md) — which PR closes what.

Every row is verified against code at the cited location. Severity and `Closed by` are the state on
`main`, not on the reference branch: a row only moves to Closed when the PR that
fixes it merges.

## How to read severity

- **Blocker** — can lose or corrupt user data, or serve wrong bytes, **reachable from
  a valid production state**. Nothing has to be broken first.
- **High** — breaks a documented guarantee, causes a large performance regression, or
  can serve wrong bytes **only once metadata is already corrupt or legacy**. The
  damage is as bad, but it needs a precondition that should not exist.
- **Medium** — wrong behaviour with a bounded blast radius, or a false claim in docs.
- **Low** — semantics, hygiene, misleading names or metrics.

The Blocker/High split was sharpened after F5 and F13 were filed: both can serve
wrong bytes, which read as Blocker under the original wording, but neither is
reachable without a pre-existing corrupt dirent or a legacy path-based object. Keeping
them at High preserves the meaning of the Blocker list — the things that can bite a
correct, healthy cluster. Both were explicitly considered for Blocker and are called
out here so the decision is visible rather than implicit.

---

## Open on `main`

| # | Severity | Finding | Evidence | Closed by |
|---|---|---|---|---|
| F1 | Blocker | **Fast-clearing GC fence loses the uploaded object.** An upload can PUT, add its provisional reference after GC's post-claim liveness read, wait for the fence, then publish metadata without repeating the PUT GC deleted — returning success for an absent object. The store→materialize contract exists but `RegisterUploadedBlock`'s inner wait absorbs the fence and returns `nil`, so the outer wrapper never repeats the store. | `fs_helpers.go` `RegisterUploadedBlock`; `upload_reuse.go` `RetryUploadedBlockMaterialization` | PR-3, PR-5 |
| F2 | Blocker | **GC-claim stub permanently breaks a block id.** A claim against a vanished row upserts a stub with no `storage_class`/`created_at`. If the post-claim recheck finds a reference, the claim is released and the stub survives; `ProbeBlockReuse` then hard-errors on it forever, and because the probe runs in the store phase the writer-side repair in materialize is unreachable. | `worker.go` `processBlock` hasRefs branch; `block_references.go` `ProbeBlockReuse` empty-class branch | PR-2 |
| F3 | High | **Web block-session upload funnel is unwrapped.** `v2/blocks.go` uses raw `BlockExists` + `PutBlockData` + materialize with no probe and no store retry, so it cannot honour the fence contract at all. | `v2/blocks.go` `UploadBlock` | PR-5 |
| F4 | High | **`NeedsPut` with existing metadata stores to the wrong backend.** The PUT targets the serving node's preferred store while first-writer metadata may point elsewhere, so physical placement and metadata diverge. | `sync.go`, `seafhttp.go` NeedsPut branches | PR-4 |
| F5 | High | **Download can serve stale legacy bytes.** `HandleDownload` falls back to the path-based object on *any* streaming failure, including a Cassandra timeout. On a library since written through blocks that object can hold an older version of the same path, so a transient failure answers 200 with stale content. | `seafhttp.go` `HandleDownload` fallback | PR-6 |
| F6 | Medium | **Transient retry sentinel is not produced by the production helper.** `RegisterUploadedBlock` returns plain errors, so a Cassandra timeout during materialize fails the request immediately with no retry and no metric. | `fs_helpers.go`; `upload_rollback.go` | PR-3 |
| F7 | Medium | **Readers resolve by routing, not by metadata.** Once bytes follow canonical metadata (F4), a reader that picks the backend by request routing looks in the wrong bucket. | read call sites in `fileview.go`, `sharelink_view.go`, `sync.go`, `seafhttp.go` | PR-4 |
| F8 | Medium | **Legacy no-session upload leaks S3 objects (R2).** `/api/v2/blocks/upload` without a session writes an object with no `blocks` row and no reference. Reachable by any authenticated user regardless of the block-upload feature flag. | `v2/blocks.go` legacy path | PR-7 |
| F9 | Medium | **GC Phase 0 can delete a renewed provisional reference.** The scanner removed the reference based on a stale expiry projection, which could drop liveness for a live upload that renewed the same referrer. | `gc/scanner.go` `scanExpiredProvisionalBlockRefs` | PR-8 |
| F10 | Medium | **Provisional reference and its expiry are written separately.** A failure between them leaves a reference with no discovery projection, so the zero-ref transition is never found. | `fs_helpers.go`; `provisional_block_ref_expiry.go` | PR-8 |
| F11 | Medium | **Abandoned prefetch leaks an open S3 reader.** `PrefetchBlock` buffered its result, so a consumer that stopped early left the `io.ReadCloser` unclosed. | `streaming/streaming.go` | PR-9 |
| F12 | Medium | **Unbounded request bodies.** `PutBlock` and `check-blocks` read the whole body with `io.ReadAll` and no size or id-count limit. | `sync.go` | PR-10 |
| F13 | High | **Corrupt directory listings resolve, and 404/503 semantics are inverted for a deleted path.** High, not the Low it was first filed as: two of the cases below serve bytes from the wrong FS object. Not Blocker only because it needs an already-corrupt dirent — see the severity note above. The 404/503 half on its own would be Low. `findEntryInDir` returns a plain error, so a renamed or deleted file surfaces as 503 while an absent commit row gives 404. Related, and worse: a JSON-valid but corrupt listing resolves anyway. Structural cases (`null`, `[null]`, non-string name, missing id) parse and the bad entries are skipped, reporting a false absence; semantic cases (empty name, empty or non-40-hex id, duplicate names) resolve to a wrong or missing FS object; and `encoding/json` silently keeps the **last** value for a repeated key, so `{"id":"A","id":"B"}` serves B and `{"name":"a","name":"b"}` hides `a` entirely. Both of the last two can serve arbitrary bytes or report a present file as absent. | `seafhttp.go` `findEntryInDir` | PR-6 |
| F14 | Low | **Retry metric mislabels write failures.** The SeafHTTP wrapper defaults every non-fence retry to `reason="probe"`, attributing Cassandra write errors to the read path. | `seafhttp.go` retry wrapper | PR-3 |

## Open and out of scope for this series

These are **not** closed by any PR in the plan. Destructive GC stays disabled until
the first two are resolved.

| # | Severity | Finding | Tracked as |
|---|---|---|---|
| X1 | Blocker | **Physical delete ABA.** `gc_s3_orphans` has no per-lifecycle claim or generation, so a previously authorized key-only S3 delete can still run after the visible orphan fence clears and after a re-upload has stored new bytes. Rematerialization does not fence it. | `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` |
| X2 | Blocker (multi-DC) | **Cross-DC reference visibility.** `block_references` are ordinary `LOCAL_QUORUM` writes that `SERIAL` does not cover. With RF 1 per DC the write and read quorums need not intersect, so GC in one DC can read zero references for a block that is live in another. The 1h grace period is mitigation, not a bound. | `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` |
| X3 | Medium | **PUT precedes durable intent.** Both upload modes write to S3 before recording any Cassandra state, so a crash between the two leaves an object nothing can discover. Closing it needs a durable intent before the PUT, or a safe physical sweeper. | `ISSUE-UPLOAD-PUT-BEFORE-INTENT-01` |
| X4 | High (perf) | **One global Paxos round per block, on every upload path.** The first-writer `INSERT ... IF NOT EXISTS` is a per-block LWT; under `SERIAL` and multi-DC it is a global consensus round, ~128 cross-region rounds per GB at the 8 MB block size. **Correction:** an earlier revision said the legacy resumable path "does not pay this at all". That is false — `finalizeUploadStreaming` splits the file into 8 MB blocks and calls `RegisterUploadedBlock` → `UpsertBlockMetadata` per block, so resumable pays the same ~128 LWTs. This is a **shared** cost of both upload paths, not a block-upload disadvantage, and removing it benefits both. `main` also reads the `blocks` row twice per upload (`ProbeBlockReuse`, `BlockDeleteFenceActive`), **but those two are not redundant and must not be merged**: the probe reads before the PUT, the fence reads after the provisional reference is durable, and that ordering is the mutual exclusion the protocol depends on. Reusing the pre-PUT observation to authorize publication reintroduces F1. Only the LWT is removable here. **Pre-existing since `13e01263a` (2026-07-08)** — not introduced by this work. | P-4 in `UPLOAD-PERFORMANCE-SECURITY-2026-06.md`; PR-11 (deferred) |
| X5 | Medium | **Canonical read fan-out unvalidated.** One Cassandra point read per unique block before the first byte. The existing benchmark substitutes an in-memory function for Cassandra, so it measures goroutines and allocations, not driver, pool, latency or cluster load. | `WEB-BLOCK-UPLOAD.md` pre-flag checklist |
| X6 | Medium | **Read-after-write across DCs.** Canonical lookups retry a missing row 3×25 ms, which covers local lag but not cross-DC. Safe (fails closed) but an availability dependency: transient 404/503 after a remote upload, `check-blocks` reporting a block missing, needless re-uploads. | same as X2 |
| X7 | Medium (perf) | **PR-2/PR-3 add a third unconditional read of the `blocks` row.** On `main` the stub-repair read (`GetBlockDeleteClaimInfo`) only fires when the delete fence is active. The reference branch calls `removeStaleUploadedBlockDeleteStub` on the unfenced path too, so every block upload pays an extra point read. **The obvious shortcut does not work:** an earlier revision of this row proposed keying the repair off "`NeedsPut` with an empty `StorageClass`", but `ProbeBlockReuse` returns exactly that for a genuinely missing row *and* for a stub, and `BlockReuseProbe` carries no `Found`, `IsStub` or claim id to tell them apart. Driving a conditional delete off that signal would fire an LWT on **every brand-new block upload** — trading one extra read for one extra Paxos round, which is strictly worse and is precisely the cost X4 is about. PR-2 must instead make the state explicit: add a distinct decision (e.g. `RepairableStub`) or carry the claim metadata the probe already read, so the repair runs only when there is something to repair. Keeping the extra read for the ambiguous `NeedsPut` case only is the acceptable fallback. | reference branch `fs_helpers.go` `RegisterUploadedBlock`; `db.BlockReuseProbe`; PR-2/PR-3 |

## X1 design space — closing the physical-delete ABA

Bucket versioning was floated as the fix (a delete becomes a marker, the new bytes
survive). **It is not the chosen direction.** It pushes the problem onto storage
configuration, has to be enabled and paid for on every backend including MinIO, needs
its own lifecycle policy to avoid unbounded retention, and leaves the protocol just
as wrong — it only makes the wrong delete survivable. Recorded here so the
alternatives are not lost.

**The shape of the problem.** GC authorizes a delete, then something re-uploads the
block, then the already-authorized delete lands and destroys the new bytes. The
delete is keyed only by object name, and because blocks are content-addressed a
re-upload is **byte-identical**, so no ETag or `If-Match` conditional can tell the
new object from the old one. That is what makes this harder than a normal ABA:
the usual "compare the value" trick is unavailable by construction.

Candidate directions, none yet designed:

1. **Generational physical keys — currently the only candidate that closes it.**
   Never reuse a physical key for new bytes: derive it as
   `blocks/<org>/<hash>.<generation>` (or any monotonic suffix) and record the live
   generation in metadata. A stale delete then names a key nothing will ever write
   again, so it is harmless by construction — the re-upload lives at a different key.
   This is the one option that survives the byte-identical property, because it stops
   depending on distinguishing content at all.

   **Cost, stated honestly: the canonical reader does not support this today.** An
   earlier revision claimed it "already does exactly this" — it does not.
   `canonical_block_reader.go` reads `storage_key` from metadata but then *derives*
   the key with `store.StorageKeyForHash(blockID)` and **rejects** any persisted key
   that differs, so `blocks/<org>/<hash>.<generation>` would be refused outright.
   Adopting generational keys means changing that contract: the persisted key becomes
   authoritative, and the reader must instead validate that it belongs to the right
   org, matches the expected hash, and conforms to the generational format — the
   validation currently provided for free by deriving the key. Dedup also has to key
   on hash while the physical key carries the generation. This is real design work,
   not a config change.
2. **Per-lifecycle claim/generation on `gc_s3_orphans`.** The minimum needed to stop
   two recoverers acting on the same lifecycle. Necessary regardless of what else is
   chosen and probably the first increment, but it does **not** close the in-flight
   case: Cassandra cannot revoke an S3 request already on the wire.
3. **Fencing token with a bounded authorization window.** The recoverer's delete is
   valid only while its claim is unexpired, and writers refuse to publish until any
   outstanding claim has expired. Bounds the window rather than eliminating it; only
   useful combined with (1) or (2).
4. **Do not delete on the hot path at all.** Move physical deletion to a single
   serialized sweeper that owns the bucket, reducing the problem to that sweeper's
   own read-verify-delete window. Does not eliminate the window either.

**Two-phase delete via a quarantine prefix does NOT close this on its own** — an
earlier revision of this document claimed it did, and that was wrong. S3 has no
atomic rename, so "move to quarantine" is `Copy K → Q` followed by `Delete K`, and
that `Delete K` carries the identical ABA:

```
GC copies K -> Q
GC issues DELETE K, then stalls
the claim is recovered or cleared
an upload writes K again
the old DELETE lands -> the new bytes are gone
```

The second phase only makes deleting `Q` safe; it does nothing for the live key.
Quarantine is still worth having as a recovery affordance — the bytes survive
somewhere, so the loss is repairable if detected — but it must be described as that,
not as a fix, and it only becomes a fix combined with (1).

Whichever is chosen, it must hold for a **byte-identical** re-upload (no ETag or
`If-Match` conditional can distinguish new from old, because content-addressed blocks
re-upload to the same bytes), must not depend on storage-provider features that MinIO
and S3 implement differently, and must be testable without a live multi-region
cluster. Destructive GC stays disabled until one lands.

## Verification debt

Applies to every PR in the series, not to any single finding.

- **`go test -race` has never run.** The Windows dev box has no gcc. It must run in
  Docker before PR-3, PR-4, PR-5 and PR-9 merge — those change goroutine and channel
  semantics.
- **No end-to-end download tests against real Cassandra** exist for the 404/503
  contract (F5, F13). The unit tests cover the classifier, not the handler.
- **No multi-DC test** exercises X2 or X6. Both are reasoned from the consistency
  contract and the committed configuration, not reproduced.
- **No production latency measurement** for the per-block LWT (X4). Decide PR-11 on
  the number, not the estimate.

## Audit provenance

Eight successive reviews between 2026-07-20 and 2026-07-21. Each found real defects in
the previous round's fix, including two cases where the fix was correct but
unreachable in the production flow and the tests could not detect it because they
called the helper directly. That pattern is the reason this work is being split
rather than merged whole.
