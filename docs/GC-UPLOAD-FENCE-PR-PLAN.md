# Upload-Fence / Canonical-Storage Work — PR Split Plan

**Date:** 2026-07-21
**Reference branch:** `docs/gc-upload-fence-rematerialization` (16 commits) — **not for merge**
**Status:** PR-1 proposed; no code PRs open yet.

## Why this document exists

The reference branch grew to 63 files across GC, upload funnels, the canonical read
path, streaming, the frontend and 16 documents. It is correct as far as we can
verify locally (build, vet, unit tests, integration compile all pass) but it is too
large to review honestly in one pass — eight successive audits each found real defects
in it, which is itself the argument against merging it whole.

So it stays as a **reference target**: the shape the code should end up in. We land
the same work through small, individually reviewable PRs cut against `main`, and the
last PR should leave the tree materially equivalent to the reference — unless a
better idea appears along the way, which is the other reason for going slowly.

## Ground rules for the series

1. **Each PR is independently reviewable and independently revertible.** If a PR
   cannot be described in one sentence, it is too big.
2. **Docs land with the code that makes them true.** See the note below — this is the
   one place where the original "documentation first" instinct needs adjusting.
3. **No PR may leave `main` in a state that serves wrong bytes or deletes live data.**
   Ordering below is chosen so that every intermediate state is safe, not just the
   final one.
4. **Destructive GC stays disabled** for the whole series. The two open blockers
   (physical-delete ABA, cross-DC reference visibility) are out of scope here and
   gate enabling it, independently of this work.
5. Every PR states its verification: `go build`, `go vet`, `go vet -tags=integration`,
   unit tests, and — for anything touching concurrency — `go test -race` in Docker,
   which cannot run on the Windows dev box (no gcc).

### Why PR-1 is not "all the documentation"

The natural instinct is to land every doc change first. That does not work here: the
docs on the reference branch describe the **fixed** state ("registration propagates
the fence", "all seven funnels retry", "the legacy fallback was removed"). Landing
those against `main` would assert things that are not true yet, which is worse than
no documentation — an operator reading `KNOWN_ISSUES.md` would believe a blocker is
closed while the code still has it.

So PR-1 lands the **findings and this plan** — content that is true on `main` today,
because it describes problems and intent, not resolutions. Each later PR carries the
doc edits that its own code makes true. The registry's **Closed by** column names the
PR that owns each row; the PR that lands moves its rows out of the open table.

---

## The series

### PR-1 — Findings registry and plan (docs only)

**Scope:** three documents, no code.

1. this file;
2. `UPLOAD-FENCE-FINDINGS-REGISTRY.md`, recording every open item with severity,
   evidence and the PR that closes it;
3. a **selective** addition to `UPLOAD-PERFORMANCE-SECURITY-2026-06.md` — the P-4
   section, its summary row and its entry in the recommended order, and nothing
   else. Both documents above link to P-4, so without it they would carry dangling
   references. The rest of that file is deliberately untouched: the reference branch
   also rewrites its claims about upload funnels and canonical readers, and those are
   not true on `main` yet.

No status is claimed as fixed anywhere.

**Rationale:** makes the work auditable before any of it lands, and gives every later
PR a row to close.

**Risk:** none.

---

### PR-2 — GC claim stub lifecycle

**Scope:** `internal/gc/worker.go` (re-referenced stub deleted under its claim rather
than released), `internal/db/block_references.go` (`DeleteClaimedBlockStub`,
`DeleteReleasedBlockStub`, `storage_class` invariant on write, and a probe result
that makes a metadata-free stub **distinguishable**), `internal/api/v2/fs_helpers.go`
(writer-side stub repair). Tests for each.

**The stub must be an explicit state.** Classifying it as plain `NeedsPut` is not
enough: `ProbeBlockReuse` returns `NeedsPut` with an empty `StorageClass` for a
genuinely missing row too, and `BlockReuseProbe` carries no `Found`, `IsStub` or
claim id to separate them. Add a distinct decision (e.g. `RepairableStub`) or carry
the claim metadata the probe already read. Without that the repair either cannot
target the stub, or fires an LWT on every brand-new block — see X7.

**Why first among the code PRs:** it is the only one that fixes a state that can
permanently break a block id, and it depends on nothing else.

**Acceptance:** a stub left by a crashed or re-referenced claim is removed by either
side, and an upload that meets one succeeds instead of exhausting its retries. Unit
tests must drive the real `store -> materialize` loop, not call the repair directly —
the direct-call tests on the reference branch could not catch that the repair was
unreachable behind the probe.

---

### PR-3 — Store/materialize retry contract

**Scope:** the cancellable retry wrapper, the three retry sentinels and their metric
labels in `internal/api/v2/upload_reuse.go`; `internal/metrics/metrics.go`;
`internal/db/block_references.go` (`ErrBlockMetadataPermanent`);
`internal/api/v2/fs_helpers.go` (`RegisterUploadedBlock` stops waiting on the fence,
tags transient Cassandra I/O, leaves permanent failures untagged).

**Explicitly NOT in this PR:** `StoreUploadedBlockForProbe`'s canonical placement and
`ResolveNeedsPutBlockStore`. An earlier draft put them here, which would have let
PR-4 start writing to the canonical backend before any canonical reader existed —
the exact hole that merging placement with readers was supposed to close. Placement
stays with the readers in PR-4. If this PR introduces a store helper at all, it must
preserve today's preferred-backend behaviour byte for byte.

**⚠ This PR is NOT behaviour-neutral.** An earlier draft of this plan claimed it was,
on the assumption that no funnel was wired yet. That is wrong: `main` already wraps
six funnels in a store→materialize retry (SeafHTTP simple and streaming, sync
`PutBlock`, V2 `UploadFile`, template `CreateFile`, OnlyOffice). The moment
`RegisterUploadedBlock` stops waiting internally and propagates the fence, all six
begin repeating store→materialize in production. Review and test it as a live
behaviour change, not as dormant plumbing.

**Acceptance:** the sentinel is produced by the production helper, not only injected
in tests; a permanent metadata failure is not retried; retry reasons are
`gc_fence` / `probe` / `materialization` and never mislabel a write as a read; and
the six already-wired funnels are exercised under a fence to confirm the new repeat
behaviour is what we want.

---

### PR-4 — Canonical placement and canonical reads (single PR)

**Scope:** `StoreUploadedBlockForProbe` / `ResolveNeedsPutBlockStore` — existing-
metadata `NeedsPut` stores through `probe.StorageClass` and the canonical key instead
of the serving node's preferred backend — **together with**
`internal/streaming/canonical_block_reader.go`,
`internal/db/block_storage_location.go`, the read call sites (`fileview.go`,
`sharelink_view.go`, `sync.go`, `seafhttp.go`) and the canonical `/blocks/check` in
`v2/blocks.go`.

**Why these are one PR and not two.** An earlier draft split them and papered over
the gap with "ship in the same release", which contradicts rule 1. They are not
independently safe in either direction:

- writer first → metadata points at bucket A, the writer repairs into A, an old
  reader still resolves by routing to B, and the block reads as missing;
- reverting the reader after both landed reintroduces exactly the same break.

The honest options were to merge them or to introduce a transitional reader that
accepts both resolutions, flip the writer, then retire the compatibility. The
transitional reader is more moving parts for a system with no production data yet,
so: one PR. It is the largest in the series and should be reviewed as the one that
changes where bytes live.

**Why it now comes before the web funnel.** A second draft had the web funnel here
and canonical work after it. That was the same bug one step removed: the web funnel
consumes `StoreUploadedBlockForProbe`, so wiring it first would start writing to the
canonical backend while every reader still resolved by routing — newly published
files unreadable. Canonical placement and canonical reads must both exist before any
new caller of that helper lands.

**Acceptance:** the two-bucket MinIO integration test proving the object lands in the
canonical bucket and not the preferred one; exact-class routing, derived-key
validation, cross-org key rejection and the bounded deduplicated lookup all covered;
resolution fails before any response header is committed.

---

### PR-5 — Cover the remaining funnel and normalise the wrappers

**Scope:** the web block-session path in `v2/blocks.go`, which is the one funnel with
no probe and no retry wrapper at all. Traffic accounting and the staged-block
reservation hoisted so a retry cannot double-charge or double-reserve. Normalise the
SeafHTTP wrapper's retry-reason labels against the generic one. Frontend: treat the
new `409 block_delete_in_progress` as a soft retry honouring `Retry-After`.

**Not** "the moment everything gets connected" — PR-3 already did that for six
funnels. This closes the seventh and makes the wrappers consistent.

**Depends on PR-4**, not merely follows it: this funnel calls the canonical store
helper, so the canonical readers have to be in place first.

**Why the frontend rides along:** this PR is what makes the 409 reachable from the
web client. Splitting them would ship a user-visible upload failure for one release.

**Acceptance:** the deterministic fast-clear regression (real GC worker paused at its
post-claim liveness read) fails before this PR and passes after.

---

### PR-6 — Download fail-closed and removal of the path-based fallback

**Scope:** `seafhttp.go` `HandleDownload`, `lookupFileBlocks` and `findEntryInDir`.
The legacy `<org>/<repo><path>` fallback and `resolveLibraryObjectStore` are removed,
and the 404/503 contract is:

- **404 only** when a directory was read, fully validated, and does not list the
  entry. That is the single signal that proves the file is gone.
- **503 for everything else**, including a bare `gocql.ErrNotFound` on a referenced
  row. A missing head commit, root fs_object, or the fs_object a dirent names is
  dangling metadata — premature GC, a partial write, cross-DC lag — not proof the
  path does not exist. An earlier draft of this plan said "absence proven by
  Cassandra is 404"; that was wrong and would tell a client to stop retrying a file
  that is still there.

Directory listings must be validated before absence can be claimed: reject blank or
JSON-null values, null entries, empty names, non-40-hex ids, duplicate names, and
duplicate JSON keys within an entry (`encoding/json` keeps the last silently, which
can serve the wrong FS object or hide a present file).

**Precondition:** confirmed with the product owner that production starts from empty
buckets, so no path-based object exists. **If that ever stops being true this PR must
be revisited**, because it removes the only way to read such an object.

**Acceptance:** classifier tests both ways, including that a referenced-row
`ErrNotFound` does *not* become 404; the full corrupt-listing matrix routed through
the parse-and-match path rather than the parser alone; encrypted-without-session
never reaches a plaintext object; end-to-end download tests against real Cassandra
(4 cases) — these belong in `internal/integration` and do not exist yet.

**Suggested additional acceptance:** a property-based test over the parse-and-match
helper asserting that absence is *never* reported except for a well-formed listing
with no match. Three separate audit rounds each found a new corrupt-listing shape
that hand-written cases had missed; generated input is the way to stop that.

---

### PR-7 — Legacy no-session upload governance

**Scope:** `v2/blocks.go` legacy path writes canonical metadata plus a deterministic
provisional pin; integration teardown extended to the `blocks` row, the provisional
expiry and its by-day projection.

**Note:** the no-session branch of `/api/v2/blocks/upload` has no legitimate client —
desktop and mobile use `/seafhttp/` — but it is reachable by any authenticated user
regardless of the block-upload feature flag, which is why it is worth governing
rather than deleting outright. Deleting it is a defensible alternative; decide in
review.

**Acceptance:** `-run TestWebBlockUpload` drains to a zero delta on
blocks/mappings/refs/provisional rows/S3 objects, the property this work must not
regress.

---

### PR-8 — GC scanner Phase 0 and provisional-ref durability

**Scope:** `internal/gc/scanner.go` (Phase 0 defers to the Cassandra TTL instead of
deleting a possibly-renewed reference), `internal/db/provisional_block_ref_expiry.go`
(`AddProvisionalBlockReferenceWithExpiry` in one logged batch,
`DeleteProvisionalBlockReferenceExpiryIfExpiresAt` CAS on `expires_at`).

**Independent of the upload series** — could land at any point. Listed late only
because it is lower risk, not because it is blocked.

---

### PR-9 — Streaming leak fix

**Scope:** `PrefetchBlock` / `StreamBlocks` context-aware delivery closing an
abandoned reader, and `QueryBlockSizes` partial-cache reuse.

**Must run `go test -race` in Docker** — this is the PR that changes goroutine and
channel semantics.

---

### PR-10 — HTTP request hardening

**Scope:** body-size limits on `PutBlock` and `check-blocks`, and the check-blocks id
cap.

**Split from PR-9** because nothing forces them to ship together: a reader leak and a
DoS bound share no code and no deploy dependency. Rule 1 applies to this series too.

---

### PR-11 (deferred) — Remove the per-block Paxos

**Scope:** make `storage_class` deterministic per `(org_id, block_id)` so the
first-writer `INSERT ... IF NOT EXISTS` can be dropped.

**Not in scope: merging the two `blocks` reads.** An earlier draft listed that as a
mechanical win. It is not — the probe reads before the PUT and the fence reads *after*
the provisional reference is durable, and that ordering is the mutual exclusion that
makes the whole protocol work. Serving the second from a cached first would let GC
claim and authorize a delete in the window between them while the writer replays a
stale "no fence" and publishes, which is F1 verbatim. Optimize inside a single
observation point if useful; never reuse the pre-PUT observation to authorize
publication.

**Deliberately last, and deliberately not designed yet.** This is the item most
likely to be superseded by a better idea, and it is the one with real performance
stakes: under `SERIAL` and a multi-DC posture the current LWT is one *global*
consensus round per block, ~128 cross-region rounds per GB at the 8 MB block size.
It is **pre-existing on `main`** (`13e01263a`, 2026-07-08), not introduced by this
series.

**Correction worth carrying:** an earlier revision of this plan said the legacy
resumable path "does not pay" this. That is false. `finalizeUploadStreaming` splits a
resumable upload into 8 MB blocks and calls `RegisterUploadedBlock` per block, so it
pays the same ~128 LWTs per GB. The cost is **shared by both upload paths**, which
makes this a general optimization rather than something block upload has to fix to
justify itself — and it means the win, if taken, applies to every upload surface.

**Do not start this before measuring.** There is no per-statement latency metric for
that INSERT yet; add it, get the production number, then decide. Full analysis: P-4
in `UPLOAD-PERFORMANCE-SECURITY-2026-06.md`.

---

## Out of scope for the entire series

These stay open and keep destructive GC disabled:

- **Physical delete ABA** — `gc_s3_orphans` has no per-lifecycle claim or generation,
  so an already-authorized key-only S3 delete can run after the visible fence clears.
- **Cross-DC reference visibility** — `block_references` are ordinary `LOCAL_QUORUM`
  writes that `SERIAL` does not cover; with RF 1 per DC the write and read quorums
  need not intersect (`ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`).
- **`ISSUE-UPLOAD-PUT-BEFORE-INTENT-01`** — the physical PUT still precedes
  materialization, so a crash between them leaves an undiscoverable S3 object.
- **Canonical read fan-out at scale** — one Cassandra point read per unique block
  before the first byte; the existing benchmark replaces Cassandra with an in-memory
  function and therefore measures nothing about the cluster.

## Verification debt carried by the whole series

- `go test -race` has never run: the dev box has no gcc. Must run in Docker before
  any PR that touches concurrency merges: **PR-3, PR-4, PR-5 and PR-9**. PR-4 in
  particular introduces the concurrent canonical reader and must not merge without it.
- No end-to-end download tests against real Cassandra exist for the 404/503 contract.
- No multi-DC test exercises any of the cross-DC reasoning; it is derived from the
  consistency contract and the committed configuration, not from a reproduction.
