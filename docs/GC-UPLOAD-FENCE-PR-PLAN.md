# Upload-Fence / Canonical-Storage Work — PR Split Plan

**Date:** 2026-07-21
**Reference branch:** `docs/gc-upload-fence-rematerialization` (11 commits) — **not for merge**
**Status:** planning. No PR from this series is open yet.

## Why this document exists

The reference branch grew to ~45 files across GC, upload funnels, the canonical read
path, streaming, the frontend and ten documents. It is correct as far as we can
verify locally (build, vet, unit tests, integration compile all pass) but it is too
large to review honestly in one pass — four successive audits each found real defects
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
doc edits that its own code makes true. The registry from PR-1 gets its status column
updated by the PR that closes each row.

---

## The series

### PR-1 — Findings registry and plan (docs only)

**Scope:** this file, plus a findings registry recording every open item with
severity, evidence and current status. No code, no status claimed as fixed.

**Rationale:** makes the work auditable before any of it lands, and gives every later
PR a row to close.

**Risk:** none.

---

### PR-2 — GC claim stub lifecycle

**Scope:** `internal/gc/worker.go` (re-referenced stub deleted under its claim rather
than released), `internal/db/block_references.go` (`DeleteClaimedBlockStub`,
`DeleteReleasedBlockStub`, `storage_class` invariant on write, `ProbeBlockReuse`
classifying a metadata-free stub as `NeedsPut`), `internal/api/v2/fs_helpers.go`
(writer-side stub repair). Tests for each.

**Why first among the code PRs:** it is the only one that fixes a state that can
permanently break a block id, and it depends on nothing else.

**Acceptance:** a stub left by a crashed or re-referenced claim is removed by either
side, and an upload that meets one succeeds instead of exhausting its retries. Unit
tests must drive the real `store -> materialize` loop, not call the repair directly —
the direct-call tests on the reference branch could not catch that the repair was
unreachable behind the probe.

---

### PR-3 — Store/materialize retry contract

**Scope:** `internal/api/v2/upload_reuse.go` — `StoreUploadedBlockForProbe`,
`ResolveNeedsPutBlockStore`, the cancellable retry wrapper, the three retry sentinels
and their metric labels; `internal/metrics/metrics.go`;
`internal/db/block_references.go` (`ErrBlockMetadataPermanent`);
`internal/api/v2/fs_helpers.go` (`RegisterUploadedBlock` stops waiting on the fence,
tags transient Cassandra I/O, leaves permanent failures untagged).

**Note:** this PR introduces the contract but wires **no** funnel. It is behaviour-
neutral until PR-4.

**Acceptance:** the sentinel is produced by the production helper, not only injected
in tests; a permanent metadata failure is not retried; retry reasons are
`gc_fence` / `probe` / `materialization` and never mislabel a write as a read.

---

### PR-4 — Wire the upload funnels

**Scope:** `seafhttp.go` (simple + streaming), `sync.go` `PutBlock`, `files.go`
`UploadFile` + template, `onlyoffice.go`, and the web block-session path in
`v2/blocks.go`. Traffic accounting and the staged-block reservation hoisted so a
retry cannot double-charge or double-reserve. Frontend: treat the new
`409 block_delete_in_progress` as a soft retry honouring `Retry-After`.

**Why the frontend rides along:** this PR is what makes the 409 reachable. Splitting
them would ship a user-visible upload failure for one release.

**Acceptance:** the deterministic fast-clear regression (real GC worker paused at its
post-claim liveness read) fails before this PR and passes after.

---

### PR-5 — Canonical placement for `NeedsPut`

**Scope:** existing-metadata `NeedsPut` stores through `probe.StorageClass` and the
canonical key instead of the serving node's preferred backend.

**Depends on PR-4.** Kept separate because it changes *where bytes land*, which is
the single most consequential behaviour change in the series and deserves its own
review and its own revert.

**Acceptance:** the two-bucket MinIO integration test proving the object lands in the
canonical bucket and not the preferred one.

---

### PR-6 — Canonical read path

**Scope:** `internal/streaming/canonical_block_reader.go`,
`internal/db/block_storage_location.go`, and the read call sites (`fileview.go`,
`sharelink_view.go`, `sync.go`, `seafhttp.go`, `v2/blocks.go` check).

**Must not land before PR-5** in the reverse sense either: once bytes can follow
metadata, readers that resolve by routing would look in the wrong bucket. Ship PR-5
and PR-6 in the same release even if reviewed separately.

**Acceptance:** exact-class routing, derived-key validation, cross-org key rejection,
and the bounded deduplicated lookup are covered; resolution fails before any response
header is committed.

---

### PR-7 — Download fail-closed and removal of the path-based fallback

**Scope:** `seafhttp.go` `HandleDownload` and `lookupFileBlocks` — absence proven by
Cassandra (or a readable directory with no such entry) is 404; everything else is
503; the legacy `<org>/<repo><path>` fallback and `resolveLibraryObjectStore` are
removed. Includes the 404/503 classification fix and the `errFilePathAbsent` /
`classifyPathAbsence` naming.

**Precondition:** confirmed with the product owner that production starts from empty
buckets, so no path-based object exists. **If that ever stops being true this PR must
be revisited**, because it removes the only way to read such an object.

**Acceptance:** discriminator tests both ways; encrypted-without-session never
reaches a plaintext object; end-to-end download tests against real Cassandra
(4 cases) — these belong in `internal/integration` and do not exist yet.

---

### PR-8 — Legacy no-session upload governance

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

### PR-9 — GC scanner Phase 0 and provisional-ref durability

**Scope:** `internal/gc/scanner.go` (Phase 0 defers to the Cassandra TTL instead of
deleting a possibly-renewed reference), `internal/db/provisional_block_ref_expiry.go`
(`AddProvisionalBlockReferenceWithExpiry` in one logged batch,
`DeleteProvisionalBlockReferenceExpiryIfExpiresAt` CAS on `expires_at`).

**Independent of the upload series** — could land at any point. Listed late only
because it is lower risk, not because it is blocked.

---

### PR-10 — Streaming leak fix and request hardening

**Scope:** `PrefetchBlock` / `StreamBlocks` context-aware delivery closing an
abandoned reader, `QueryBlockSizes` partial-cache reuse, body-size limits on
`PutBlock` and `check-blocks`, and the check-blocks id cap.

**Independent.** Bundled at the end because it is the least entangled; split further
if review prefers (the leak fix and the DoS limits are unrelated to each other).

**Must run `go test -race` in Docker** — this is the PR that changes goroutine and
channel semantics.

---

### PR-11 (deferred) — Remove the per-block Paxos

**Scope:** make `storage_class` deterministic per `(org_id, block_id)` so the
first-writer `INSERT ... IF NOT EXISTS` can be dropped, and collapse the three reads
of the same `blocks` row per upload.

**Deliberately last, and deliberately not designed yet.** This is the item most
likely to be superseded by a better idea, and it is the one with real performance
stakes: under `SERIAL` and a multi-DC posture the current LWT is one *global*
consensus round per block, ~128 cross-region rounds per GB at the 8 MB CAS size,
which the legacy resumable path does not pay. It is **pre-existing on `main`**
(`13e01263a`, 2026-07-08), not introduced by this series.

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
  any PR that touches concurrency merges (PR-4, PR-6, PR-10 at minimum).
- No end-to-end download tests against real Cassandra exist for the 404/503 contract.
- No multi-DC test exercises any of the cross-DC reasoning; it is derived from the
  consistency contract and the committed configuration, not from a reproduction.
