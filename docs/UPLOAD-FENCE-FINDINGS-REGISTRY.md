# Upload-Fence Audit — Findings Registry

**Date:** 2026-07-21 · **Last updated:** 2026-07-25
**Origin:** eight successive audits of the GC upload-fence work, 2026-07-20/21.
**Companion:** [GC-UPLOAD-FENCE-PR-PLAN.md](./GC-UPLOAD-FENCE-PR-PLAN.md) — which PR closes what.
**Index:** [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md) — scoped open-work screen + migration table (not the entire backlog).
**Status of record:** [KNOWN_ISSUES.md](./KNOWN_ISSUES.md). This file owns reasoning and evidence; Open/Closed tables below are a **dated snapshot of the upload-fence series**, not a second live status tracker. When status changes, update `KNOWN_ISSUES.md` and add a dated note here if needed.
**Series progress: the code series is complete.** PR-1 merged as
[#137](https://github.com/Sesame-Disk/sesamefs/pull/137);
PR-2 as [#138](https://github.com/Sesame-Disk/sesamefs/pull/138), closing F2 and
X7. PR-3 as [#139](https://github.com/Sesame-Disk/sesamefs/pull/139), closing
F6, F14 and the observed-fence half of F1. PR-4 as
[#140](https://github.com/Sesame-Disk/sesamefs/pull/140), closing F4/F7. PR-5 as
[#141](https://github.com/Sesame-Disk/sesamefs/pull/141), closing F1 and F3. PR-6 as
[#142](https://github.com/Sesame-Disk/sesamefs/pull/142), closing F5 and F13. PR-7 as
[#143](https://github.com/Sesame-Disk/sesamefs/pull/143), closing F8. PR-8 as
[#144](https://github.com/Sesame-Disk/sesamefs/pull/144), closing F9 and F10. PR-9 as
[#145](https://github.com/Sesame-Disk/sesamefs/pull/145), closing F11. PR-10 as
[#146](https://github.com/Sesame-Disk/sesamefs/pull/146), closing F12.

**Every F row is now closed.** What remains is the X list — items the series
never scoped, deferred, or knowingly accepted. PR-11 (remove the per-block Paxos,
X4) is deferred pending a production latency measurement. **X1/X2 remain open and
destructive GC stays disabled fleet-wide.**

Every row is verified against code at the cited location, except where the row
explicitly identifies engine-dependent behavior that still needs a non-skipping
production-engine regression. Severity and `Closed by` are the state on `main`, not
on the research branch: a row only moves to Closed when the PR that fixes it merges.

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

**This scale measures data correctness, not availability** (added 2026-07-25).
Every level above is phrased in terms of losing, corrupting or misrepresenting
bytes, because that is what the upload-fence series was auditing. Abuse and
resource-exhaustion findings — X10 (no concurrency bound on the block routes) and
X11 (work amplification) — have no natural slot on it: they never serve a wrong
byte, so a literal reading of *this* scale alone would file them at Medium
regardless of damage.

**X10 canonical severity is High for its own impact**, not by inheritance from
B4: authenticated `PutBlock` still fully buffers up to ~257 MiB, the route group
has no concurrency bound (unlike the web path's `blockUploadConcurrencyLimiter`),
and N concurrent uploads cost N × the cap — a memory/DoS availability defect on
the highest-cost sync write surface. The umbrella
`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` is also High because it covers additional
anonymous upload and download surfaces (subcontracts A/D); X10 is only
subcontract B. Read availability severities from `KNOWN_ISSUES.md`; do not
re-derive them from the upload-fence correctness wording above.

---

## Open on `main`

**None — every F row has merged.** The remaining work is in
**## Open, deferred, or constraining the series** below, which is the X list.

This section previously held F12, closed by PR-10 ([#146](https://github.com/Sesame-Disk/sesamefs/pull/146)),
and a stale duplicate of F13, which PR-6 had already closed and moved to
**## Closed** — the row was left behind in both tables at once. A finding lives
in exactly one table; carrying it in two is how a registry starts contradicting
itself.

## Closed

Rows move here only once the PR that fixes them **merges**.

| # | Severity | Finding | Closed by |
|---|---|---|---|
| F2 | Blocker (engine-dependent) | A materialized GC-claim stub permanently breaks a block id. | PR-2 ([#138](https://github.com/Sesame-Disk/sesamefs/pull/138)) |
| X7 | Medium (perf) | Design constraint: stub repair must not add a hot-path `blocks` read. Closed by exposing `RepairableStub` from the existing probe plus a metadata-LWT backstop — no extra read on the absent-row path. | PR-2 ([#138](https://github.com/Sesame-Disk/sesamefs/pull/138)) |
| F6 | Medium | Production metadata registration did not produce the transient retry sentinel. PR-3 tags transient Cassandra I/O while leaving permanent metadata failures untagged. | PR-3 ([#139](https://github.com/Sesame-Disk/sesamefs/pull/139)) |
| F14 | Low | Retry metrics mislabeled materialization failures as probe failures. PR-3 derives the label from the failing phase. | PR-3 ([#139](https://github.com/Sesame-Disk/sesamefs/pull/139)) |
| F4 | High | `NeedsPut` with existing metadata stored to the preferred backend instead of immutable canonical placement. | PR-4 ([#140](https://github.com/Sesame-Disk/sesamefs/pull/140)) |
| F7 | Medium | Readers resolved storage by request routing instead of canonical block metadata. | PR-4 ([#140](https://github.com/Sesame-Disk/sesamefs/pull/140)) |
| F1 | Blocker | Fast-clearing GC fence lost the uploaded object. PR-3 closed the observed-fence half; PR-5 added the mandatory post-reference canonical confirmation that repairs bytes when a whole GC cycle cleared its fence before the writer could observe it, proven by a deterministic regression against a real `gc.Worker`. | PR-3 + PR-5 ([#141](https://github.com/Sesame-Disk/sesamefs/pull/141)) |
| F3 | High | Web block-session upload funnel was unwrapped. PR-5 wrapped it in the bounded store→materialize→confirm cycle with single-shot admission, traffic and metrics, and a retryable coded `409 block_delete_in_progress`. | PR-5 ([#141](https://github.com/Sesame-Disk/sesamefs/pull/141)) |
| F5 | High | Download served stale legacy bytes: `HandleDownload` fell back to the path-based object on any streaming failure. PR-6 removed the fallback and `resolveLibraryObjectStore`; block storage is the only download path and every metadata/read failure is a retryable 503. | PR-6 ([#142](https://github.com/Sesame-Disk/sesamefs/pull/142)) |
| F13 | High | Corrupt directory listings resolved and unproven absence could become 404. PR-6 validates listings all-or-nothing (duplicate keys, unsafe names, invalid modes, mode↔obj_type disagreement all fail closed) and maps even a validated local miss to 503, since a LOCAL_QUORUM snapshot cannot prove global absence. Accepted cost recorded as X8 / ISSUE-DOWNLOAD-NO-404-01. | PR-6 ([#142](https://github.com/Sesame-Disk/sesamefs/pull/142)) |
| F8 | Medium | Legacy no-session upload leaked S3 objects (R2): `/api/v2/blocks/upload` without a session wrote an object with no `blocks` row and no reference. PR-7 removed the path outright — both `/blocks/upload` and its paired `/blocks/check` oracle answer 400 `block_upload_session_required` before any store I/O, and the frontend throws instead of silently dropping the session header. | PR-7 ([#143](https://github.com/Sesame-Disk/sesamefs/pull/143)) |
| F9 | Medium | GC Phase 0 could delete a provisional reference a live upload had just renewed. PR-8 makes the `up:` reference and canonical tracker retire only through their derived Cassandra TTL; Phase 0 never mutates them, checks the exact reference before resolving liveness, defers a still-present row (holding the day cursor), and sweeps only resolved projections. | PR-8 ([#144](https://github.com/Sesame-Disk/sesamefs/pull/144)) |
| F10 | Medium | Provisional reference and its expiry were written separately, so a failure between them left an undiscoverable reference. PR-8 writes the TTL-bound reference, longer-lived tracker and durable by-day projection in one logged batch and removes eager provisional release entirely, so every failure is retryable and there is no provisional-row LWT/CAS. | PR-8 ([#144](https://github.com/Sesame-Disk/sesamefs/pull/144)) |
| F11 | Medium | Abandoned streaming prefetch leaked an open S3 reader. PR-9 makes `streamOneBlock` close the streamed block's reader via defer (panic-safe) and `StreamBlocks` cancel a child prefetch context and drain/close the block prefetched one ahead on every exit; the next block is prefetched only when the current one succeeds, and `PrefetchBlock` skips the fetch when its context is already canceled. The adjacent false-success/over-billing issue (`ISSUE-STREAMBLOCKS-VOID-01`) was fixed on 2026-08-03 by returning stream errors and recording delivered bytes. | PR-9 ([#145](https://github.com/Sesame-Disk/sesamefs/pull/145)) |
| F12 | Medium | Unbounded request bodies on `PutBlock` and `check-blocks`. PR-10 adds the shared `readLimitedRequestBody` helper (swaps the body for an `http.MaxBytesReader`, answers 413 on overflow); `PutBlock` fast-rejects an oversized declared `ContentLength` before reading, capped just above the 256 MiB adaptive-chunk ceiling; `check-blocks` bounds the raw body and rejects an oversized id list **during** the parse rather than after it. `GetLockedFiles` was folded onto the same helper. **F12 closes; the class does not** — the four sibling sync handlers are X9, and what a per-request cap cannot do is X10/X11. | PR-10 ([#146](https://github.com/Sesame-Disk/sesamefs/pull/146)) |

## Open, deferred, or constraining the series

X1-X3, X5, X6 and X9-X11 are not closed by the immediate code PRs; X4 is deferred to PR-11;
X7 was closed by PR-2 (see **## Closed**); X8 is a cost PR-6 accepts knowingly rather
than a defect to fix. Destructive GC stays disabled until X1 and X2 are resolved.

PR-2 also removed writer-side active-claim release from `internal/api/v2/fs_helpers.go`.
Only the GC owner may release or delete an active claim; writers must wait/retry or
fail closed. This is part of F2's lifecycle fix. PR-3 goes further on the writer side:
`RegisterUploadedBlock` no longer waits internally at all — it propagates the fence to
the outer store→materialize wrapper.

PR-8's provisional protocol intentionally accepts delayed cleanup: successful and
rolled-back `up:` references can remain for up to 48 hours, the canonical tracker TTL
adds the default 24-hour scanner margin, and the by-day projection stays durable until
Phase 0 resolves it. Session admission, committed-session state, staging limits,
traffic charging and logical-storage accounting are independent of provisional-row
retirement and keep their existing cleanup/idempotency behavior.

The table preserves each finding's discovery-time wording. X10's row is
historical and is superseded by the dated 2026-07-30 closure note immediately
below the table.

| # | Severity | Finding | Tracked as |
|---|---|---|---|
| X1 | Blocker | **Physical delete ABA.** A previously authorized key-only S3 delete can still run after the visible orphan fence clears and after a re-upload has stored new bytes. Rematerialization does not fence it. Cassandra authorization/claim generations alone cannot revoke a DELETE already in flight; X1 closes only with never-reused generational physical keys, so stale deletes can target only old keys. | `ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` |
| X2 | ✅ **Closed 2026-08-13** | **Cross-DC reference visibility.** `block_references` are ordinary `LOCAL_QUORUM` writes that `SERIAL` does not cover. With RF 1 per DC the write and read quorums need not intersect, so GC in one DC can read zero references for a block that is live in another. The 1h grace period is mitigation, not a bound. **Implemented without r3** by pinning the *authorizing* read to `EACH_QUORUM`: `BlockHasReferencesGlobal` backs `processBlock`'s claim-then-verify, while the pre-claim check, the scanner and `enqueueZeroRefBlocks` stay at session consistency because the zero-check is asymmetric — a local positive is proof, a local zero authorizes nothing. `RecoverS3Orphans` performs its own global verify: it *could* inherit authorization transitively from an orphan row that cannot exist unless that verify passed, but that implication only runs forward in time and rested on a greenfield precondition code cannot enforce and that fails silently. A topology gate (`ValidateDestructiveGCTopology`) refuses both destructive paths unless live keyspace replication gives `EACH_QUORUM` per-DC meaning **and the live map equals the declared one** — a map shrunk after references were acknowledged elsewhere passes every structural check while `EACH_QUORUM` stops being obliged to reach them. The gate lives in `GCStore`, so dropping it is a compile error. Needed **no** generations, physical incarnations, writer hot-path change, or `SERIAL+ALL` — that fence serves the publication TOCTOU, a different property. **Closure evidence:** the three-DC regression on `docker-compose.cassandra-3dc.yaml` at RF 1 ran green — `LOCAL_QUORUM(dc-na)=false` and `EACH_QUORUM(dc-na)=true` against the same divergent state, fail-closed with a DC down, and both gate halves — and leg 1 was confirmed to go red under a `LOCAL_QUORUM` downgrade. Two DCs reproduce the defect but cannot rule out the wrong fix: a non-local `QUORUM` is 2 of 2 there, so it intersects by accident and looks as good as `EACH_QUORUM`; only at three does it become 2 of 3 and able to miss the replica holding the reference. **X2 closed does not enable GC** — `GC_ENABLED=false` stands; X1 is now the sole runtime activation blocker. | `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01` |
| X8 | Medium (accepted) | **Download can never report a file as gone.** The same `LOCAL_QUORUM` cross-DC reasoning that forces PR-6 to drop 404 leaves the SeafHTTP download surface with no absence answer at all: a genuinely deleted file returns 503 forever, so clients retry a request that cannot succeed, and `internal/api/v2` still answers 404 for the same missing path. Accepted in PR-6 because a wrong 404 makes a sync client delete its local copy; reintroducing 404 needs a read that proves *global* absence. | `ISSUE-DOWNLOAD-NO-404-01` |
| X3 | Medium | **PUT precedes durable physical-object intent.** Upload paths write to S3 before recording GC-discoverable block metadata/reference or another durable row that identifies the physical object for reclamation. Session-mode staged accounting can already exist, but it does not close this discovery gap. A crash between PUT and registration leaves an object nothing can discover safely. Closing it needs durable physical-object intent before the PUT, or a safe physical sweeper. | `ISSUE-UPLOAD-PUT-BEFORE-INTENT-01` |
| X4 | High (perf) | **One global Paxos round per block on every metadata-registering upload path.** The first-writer `INSERT ... IF NOT EXISTS` is a per-block LWT; under production `SERIAL` and multi-DC it is a global consensus round, ~128 cross-region rounds per GB at the 8 MB block size. **Correction:** an earlier revision said the legacy resumable path "does not pay this at all". That is false — `finalizeUploadStreaming` splits the file into 8 MB blocks and calls `RegisterUploadedBlock` → `UpsertBlockMetadata` per block, so resumable pays the same ~128 LWTs. The defective no-session path in F8 was the exception: it left only an S3 object and never registered metadata — but PR-7 removed that path, so every remaining upload path registers metadata. This is a **shared** cost of the governed upload paths, not a block-upload disadvantage, and removing it benefits all of them. The pre-store `ProbeBlockReuse` and post-reference `BlockDeleteFenceActive` observations are not redundant and must not be merged: their ordering is the mutual exclusion the protocol depends on. PR-5 adds a third post-materialization confirmation observation to close fast-clear; it is also required and must not be served from either earlier result. Only the LWT is removable here. **Pre-existing since `e3883aa5d` (2026-05-28)** — not introduced by this work; `13e01263a` only made it representation-aware. | `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01`; P-4 in `UPLOAD-PERFORMANCE-SECURITY-2026-06.md`; PR-11 (deferred) |
| X5 | Medium | **Canonical read fan-out unvalidated.** One Cassandra point read per unique block before the first byte. The existing benchmark substitutes an in-memory function for Cassandra, so it measures goroutines and allocations, not driver, pool, latency or cluster load. | `ISSUE-CANONICAL-READ-FANOUT-01` |
| X9 | Medium | **Unbounded request bodies on the remaining sync handlers.** `PutCommit`, `PackFS`, `RecvFS` and `CheckFS` in `sync.go` still read the whole body with an unbounded `io.ReadAll(c.Request.Body)`, the same defect PR-10 fixed for `PutBlock` and `check-blocks` — re-verified present 2026-07-25. F12 was scoped to the two block routes, so PR-10 closes F12 but **not** the class: an authenticated client can drive the identical memory-pressure DoS through any of the four. Each needs a cap derived from its own payload profile; `readLimitedRequestBody` makes each a one-line change once the caps are chosen. | `ISSUE-SYNC-UNBOUNDED-BODIES-01` (filed 2026-07-25 — this row previously named an id that had never been created) |
| X10 | High | **The sync block routes have no concurrency or rate limit, so PR-10's per-request cap is not an aggregate bound.** PR-10 makes one `PutBlock` cost at most ~257 MiB instead of unbounded, which is what closes F12 — but the body is still fully buffered by `io.ReadAll` before hashing, and the route group carries only `authMiddleware`. N concurrent uploads therefore still cost N × the cap. The asymmetry is explicit: the web block path already has both a per-user concurrency limiter (`blockUploadConcurrencyLimiter`, default 8) and a per-IP rate limiter on its check oracle; the seafhttp path has neither. Note the cap cannot simply pre-size the read buffer from `Content-Length` — that length is attacker-controlled and unverified, so pre-sizing would let an empty body allocate the full cap. The real fixes are a concurrency bound on the route and/or streaming the block to storage instead of buffering it. **Partly addressed 2026-07-28:** the per-request cap was right-sized from 257 MiB to a configurable 16 MiB default (`seafhttp.sync_block_max_bytes`, ceiling 64 MiB), since the 257 MiB figure came from the web uploader's adaptive-chunk ceiling — a path that never governed this route, and whose chunker has no production caller. That cuts the per-request ceiling 16× but **does not close X10**: N concurrent uploads still cost N × the cap, and the aggregate bound is the actual defect. The remaining work is a cap on in-flight block readers acquired before `io.ReadAll`, answering **503 + Retry-After after a bounded wait** — never 429, which the official client does not classify as retryable, and never immediate rejection, since one legitimate desktop can run ~15 concurrent PutBlocks (5 sync tasks × 3 block threads). **X10 is subcontract B of readiness B4** under `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` — same umbrella, narrower surface (authenticated block PUT / aggregate memory). **Availability: High** on its own impact (`N × the configured cap` full-buffer + no concurrency bound); the upload-fence correctness scale does not apply. | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` subcontract B; `sync.go` block route group, `PutBlock` |
| X11 | Medium | **`maxCheckBlockIDs` (100k) bounds the parser, not the work an accepted request triggers.** PR-10's cap is a memory bound on parsing and is correct as such. Downstream, an accepted list still drives one `GetBlockIDMapping` Cassandra point read **per legacy SHA-1 id, sequentially** in the `CheckBlocks` loop, then `CheckBlocksExist` at fan-out 10 — so a single accepted 100k-id request can issue ~100k serial reads while holding the handler. That is a request-amplification and latency concern, not a memory one, and the 100k figure was chosen as a safe parse bound rather than validated against Cassandra, the S3 pool, response size or client cancellation. Related to X5, which flags the same unvalidated fan-out on the canonical read path. | `ISSUE-CHECKBLOCKS-WORK-AMPLIFICATION-01`; also subcontract C of `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` |
| X6 | Medium | **Read-after-write across DCs.** Canonical lookups retry a missing row 3×25 ms, which covers local lag but not cross-DC. Safe (fails closed) but an availability dependency: transient 404/503 after a remote upload, `check-blocks` reporting a block missing, needless re-uploads. | `ISSUE-READ-AFTER-WRITE-CROSS-DC-01` (related to X2) |

### Dated note — X9, 2026-08-12

X9 is **closed**. All four handlers read through `readLimitedRequestBody`:
`PutCommit` at 1 MiB, `PackFS`/`CheckFS` at 16 MiB (plain consts — the same
id-list shape as `check-blocks`), `RecvFS` at a configurable 128 MiB default
(`seafhttp.recv_fs_max_bytes`, no ceiling on purpose, zero rejected at boot),
since it carries a real object batch with no measured client size to anchor a
fixed number on.

Choosing the caps surfaced a second half the row does not describe. `pack-fs` and
`check-fs` parsed their id list with `strings.Split`/`json.Unmarshal` over the
whole body, so a body *under* the byte cap still expanded ~17x — the amplification
PR-10 had already removed from `check-blocks` when it closed F12 by rejecting an
oversized id list *during* the parse. Both now share that parser
(`parseBoundedIDList`), with id caps derived from their byte caps so they are
unreachable for well-formed input and fire only on degenerate bodies.

Deriving those caps that way bounds what a body can expand into and nothing else.
It says nothing about what an accepted list costs — X11's territory, not F12's —
and these two routes have none of the controls X11 grew: no dedup before lookup,
no context on the per-id read, no fan-out bound, no admission capacity. Filed as
`ISSUE-SYNC-FSID-WORK-AMPLIFICATION-01`.

Choosing the `recv-fs` cap also surfaced something the row does not contemplate at
all: the cap bounds the **compressed** body, and `RecvFS` then inflates each packed
object with an unbounded `io.ReadAll` over a `zlib.Reader`. DEFLATE's measured best
case here is 1029:1, so 128 MiB of body inflates to ~126 GiB — the buffered body is
not this handler's dominant allocation. Pre-existing, not caused by X9's fix, and
filed as `ISSUE-RECVFS-DECOMPRESSION-AMPLIFICATION-01`. X9 stays closed on its own
terms (unbounded body reads), but no one should read it as "recv-fs is bounded".

What X9 also does **not** close is the aggregate: a per-request cap bounds one request,
and `RecvFS` at 128 MiB × N concurrent is the same shape X10 described for the
block routes. X10's fix is scoped to `PutBlock` and does not cover these routes, so
that layer is now `ISSUE-SYNC-METADATA-CONCURRENCY-01` rather than an unowned
caveat. Its first step should be measuring what the official client actually sends
per `recv-fs`: a measured cap may be cheaper than an admission gate.

### Dated note — X10, 2026-07-30

The aggregate bound and its post-implementation hardening are complete; X10 is
**closed**.

`PutBlock` acquires a bounded global entry ticket, then per-`(org, user)` and
per-node in-flight admissions before
`readLimitedRequestBody`, holds it until the handler returns, and answers
**503 + `Retry-After`** — never 429 — after a bounded wait. Per-user/node waiter
queues and admitted processing are also bounded. The deadline sets a read deadline
on the connection — the only mechanism that can interrupt a stalled body read,
since net/http's body `Read` and `Close` share a mutex and `server.read_timeout` is
deliberately `0` — and cancels object-storage I/O; Cassandra phases stop at callback
boundaries and each in-progress query has the driver's required finite timeout. A
real-TCP regression drives a connection that goes silent mid-body and fails if the
admission is not returned; companion regressions cover an earlier parent deadline
and configured server read timeout. A 1,000-identity burst proves the pre-gate
ticket bounds transient user-gate cardinality.

The original plateau-only 64 MiB design failed the stronger full-lifetime probe.
Three clean-process trials at the revised 24-slot default sampled request ramp,
held-body plateau and post-release drain; every worst RSS/cgroup sample occurred
post-release, with a worst 59.5 MiB raw / 74.4 MiB after the 1.25 factor. The
rounded 80 MiB design places 24 slots at 1.875 GiB inside an explicit,
validation-enforced 2 GiB budget.

All seven original criteria are now met. The real-client harness uses disposable
state and controlled slow PUT holders, checks an explicit 503 with positive
`Retry-After`, proves the subsequent rejections came from real `seaf-cli`, then
requires post-fault admission, stable sync, byte-for-byte remote payloads, and
zero stranded admissions. It passed twice consecutively in fast mode and again
at the shipped 10-second wait with `Retry-After: 10`.

A follow-up review on 2026-07-31 found the connection deadline itself was
bypassable: `gin-contrib/gzip` wraps `c.Writer` in a type that embeds the
`gin.ResponseWriter` interface, which does not declare `Unwrap()`, so
`http.NewResponseController` could not reach the socket and the deadline
silently degraded to the ineffective body-close fallback. One request header
(`Accept-Encoding: gzip`) was enough to hold an admission indefinitely. The
block route is now excluded from gzip, and a deadline that cannot be installed
on a server-handled request drops the connection and increments
`sync_put_block_read_deadline_unsupported_total` instead of proceeding
unprotected.

The implementation audit's lifetime, waiter, unsafe configuration-combination,
timing-dependent integration and memory-proof findings are resolved. Full detail
is under subcontract B in `docs/KNOWN_ISSUES.md`.

### Dated note — X11, 2026-07-31

The work an accepted `check-blocks` request triggers is now bounded; X11 is
**closed**.

The row above is right that the 100k cap is a parse bound. Two things it did not
name turned out to matter as much as the fan-out: the mapping read took no
`context` at all, so a client disconnect could not stop the loop, and identical
ids were resolved once per occurrence rather than once. So the cheapest possible
request — one id repeated, then abandoned — was also among the most expensive to
serve.

The route now takes an admission from its **own** limiter instance (separate
capacity from X10's; one storming must not spend the other's slots) before the
body is read, deduplicates ids before any lookup, resolves them through the new
`db.GetBlockIDMappingContext` at a configured fan-out that also replaces the
hardcoded 32/10 of the existence phase, and holds an admitted lifetime. The
node's exposure is the stated product `check_blocks_max_inflight_per_node ×
check_blocks_lookup_fanout` (8 × 8, ceiling 256), enforced at boot. The
validated fan-out ceiling is 32, matching the canonical reader's actual maximum.
Cancellation stops dispatching new work; a query already issued remains bounded
by the driver's finite timeout.

The accepted cardinality was deliberately **not** lowered: 100k stays the default
and becomes the validation ceiling, so `check_blocks_max_ids` can only be
lowered, and `sync_check_blocks_ids_per_request` was added as the evidence a
future reduction needs. Lowering it on a guess would trade a bounded amplification
for an unbounded risk of 413-ing a legitimate initial sync.

X10's gzip finding was still live here: the route was not in the exclusion list,
so this route's new admitted lifetime could not reach the socket and the
fail-closed path dropped every request. The integration suite against the real
stack caught it before merge; the route is excluded and a real-TCP regression over
the shipped middleware covers it. Full detail is under subcontract C in
`docs/KNOWN_ISSUES.md`.


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
   case and therefore cannot close X1: Cassandra cannot revoke an S3 request already
   on the wire. Only never-reused generational physical keys close that ABA.
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

- **PR-2 through PR-5 race validation passed in Docker.** PR-5's first full run
  exposed an existing unsynchronized SeafHTTP lease-test counter; after protecting
  it, both the isolated regression and complete race rerun passed on 2026-07-22.
  PR-9 must still run its own validation because it changes channel behavior. PR-4's
  focused real-Cassandra/two-bucket-MinIO acceptance and full integration suite passed
  in Compose on 2026-07-21; PR-5's full integration rerun passed on 2026-07-22.
- **PR-6 adds an end-to-end download contract test against real Cassandra** for
  present, deleted, dangling and corrupt metadata, in addition to classifier tests.
- **No multi-DC test** exercises X2 or X6. Both are reasoned from the production
  consistency contract, not reproduced. PR-6 therefore maps even a validated local
  absence to 503 rather than assuming cross-DC freshness. The dedicated USA/EU cluster test profiles
  use `LOCAL_SERIAL`, so they do not model production's `SERIAL` cross-DC LWT
  behavior; they are test profiles, not production configuration.
- **No production latency measurement** for the per-block LWT (X4). Decide PR-11 on
  the number, not the estimate.

## Audit provenance

Eight successive reviews between 2026-07-20 and 2026-07-21. Each found real defects in
the previous round's fix, including two cases where the fix was correct but
unreachable in the production flow and the tests could not detect it because they
called the helper directly. That pattern is the reason this work is being split
rather than merged whole.
