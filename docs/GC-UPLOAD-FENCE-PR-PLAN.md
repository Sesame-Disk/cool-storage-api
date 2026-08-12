# Upload-Fence / Canonical-Storage Work — PR Split Plan

**Date:** 2026-07-21 · **Last updated:** 2026-07-25
**Research branch:** `docs/gc-upload-fence-rematerialization` — **not for merge**

**Successor design:** [GC-X1-X2-GENERATION-FENCE-ADR.md](./GC-X1-X2-GENERATION-FENCE-ADR.md)
defines the accepted greenfield X1/X2 contract. This historical PR plan does not
claim that contract is implemented.

**Status: PR-1 through PR-10 have all merged. The code series is complete and
every F finding is closed.** The table below is **authoritative for PR merge
progress only**. Live defect status lives in
[KNOWN_ISSUES.md](./KNOWN_ISSUES.md); this plan does not compete as a second
status tracker.

| PR | Merged as | Closes |
|----|-----------|--------|
| PR-1 | [#137](https://github.com/Sesame-Disk/sesamefs/pull/137) | docs only (registry + this plan) |
| PR-2 | [#138](https://github.com/Sesame-Disk/sesamefs/pull/138) | F2, X7 |
| PR-3 | [#139](https://github.com/Sesame-Disk/sesamefs/pull/139) | F6, F14, observed-fence half of F1 |
| PR-4 | [#140](https://github.com/Sesame-Disk/sesamefs/pull/140) | F4, F7 |
| PR-5 | [#141](https://github.com/Sesame-Disk/sesamefs/pull/141) | F1, F3 |
| PR-6 | [#142](https://github.com/Sesame-Disk/sesamefs/pull/142) | F5, F13 |
| PR-7 | [#143](https://github.com/Sesame-Disk/sesamefs/pull/143) | F8 |
| PR-8 | [#144](https://github.com/Sesame-Disk/sesamefs/pull/144) | F9, F10 |
| PR-9 | [#145](https://github.com/Sesame-Disk/sesamefs/pull/145) | F11 |
| PR-10 | [#146](https://github.com/Sesame-Disk/sesamefs/pull/146) | F12 |
| PR-11 | **deferred** | X4 — do not start before measuring |

**What is left:** the X list in
[UPLOAD-FENCE-FINDINGS-REGISTRY.md](./UPLOAD-FENCE-FINDINGS-REGISTRY.md) —
items this series never scoped, deferred, or knowingly accepted.
**X1/X2 remain open and keep destructive GC disabled fleet-wide.**

Per-PR sections below are kept as the design and verification record of what
landed. They are history, not a plan; the PR merge table above is authoritative
for *which PR merged*, not for whether an X/ISSUE row is still open.

## Why this document exists

The research branch grew to 63 files across GC, upload funnels, the canonical read
path, streaming, the frontend and 16 documents. It compiles and passes the local
verification that was run, but it contains known design and implementation defects;
eight successive audits each found real defects in it. It is therefore neither a
merge candidate nor a normative description of the final state.

It stays only as a **research prototype and evidence archive**: useful code, failed
approaches and tests can be reconstructed from it, but every change must be derived
and reviewed independently against `main`. The series is defined by the findings and
safety contracts in this plan, not by material equivalence to that branch.

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
docs on the research branch describe the **fixed** state ("registration propagates
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

**Scope:** six documents, no code.

1. this file;
2. `UPLOAD-FENCE-FINDINGS-REGISTRY.md`, recording every open item with severity,
   evidence and the PR that closes it;
3. a **selective** addition to `UPLOAD-PERFORMANCE-SECURITY-2026-06.md` — the P-4
   section, its summary row and its entry in the recommended order, and nothing
   else. Both documents above link to P-4, so without it they would carry dangling
   references. The rest of that file is deliberately untouched: the research branch
   also rewrites its claims about upload funnels and canonical readers, and those are
   not true on `main` yet;
4. corrections in `ARCHITECTURE.md` and `DATABASE-GUIDE.md` so their Paxos inventory
   matches the current first-writer metadata LWT and conditional GC transitions;
5. canonical entries in `KNOWN_ISSUES.md` for the three out-of-series findings that
   already have stable issue identifiers.

No status is claimed as fixed anywhere.

**Rationale:** makes the work auditable before any of it lands, and gives every later
PR a row to close.

**Risk:** none.

---

### PR-2 — GC claim stub lifecycle

**Scope:** `internal/gc/worker.go` (re-referenced stub deleted under its claim rather
than released), `internal/db/block_references.go` (`DeleteClaimedBlockStub`,
`RepairReleasedBlockStub`, `storage_class` invariant on write, and an explicit
`RepairableStub` probe decision), `internal/gc/store.go`,
`internal/gc/store_cassandra.go` and `internal/gc/store_mock.go` (conditional-delete
contract and adapters), `internal/api/v2/upload_reuse.go` (shared released-stub
repair), `internal/api/v2/fs_helpers.go` (remove writer-side active-claim release;
writers must wait/retry or fail closed, never clear GC ownership), plus the existing probe switches in `internal/api/sync.go`,
`internal/api/seafhttp.go`, `internal/api/v2/files.go` and
`internal/api/v2/onlyoffice.go`. The metadata-upsert path also provides a race
backstop for the unprobed web-session path in `internal/api/v2/blocks.go`. Unit/API
tests and non-skipping production-engine lifecycle regressions in
`internal/integration/gc_integration_test.go` travel with it.

**The stub is an explicit store-phase state.** Classifying it as plain `NeedsPut` is not
enough: `ProbeBlockReuse` returns `NeedsPut` with an empty `StorageClass` for a
genuinely missing row too, and `BlockReuseProbe` carries no `Found`, `IsStub` or
claim id to separate them. PR-2 adds `RepairableStub` from the same metadata read.
`created_at IS NULL` is the lifecycle discriminator; incidental `sha1` or
`representation_id` backfilled by an earlier failed materialization do not make the
stub complete. Each funnel claims the released stub with a deterministic per-block
`repairing_stub` token, rechecks `gc_s3_orphans`, removes only that owned repair
claim, and follows its ordinary `NeedsPut` store path only after the repair succeeds.
Active claims remain `BlockedByGC`; unexpected ownership state, a new orphan fence,
and failed CAS application all stop the PUT and retry/fail closed. The deterministic
token also lets a retry confirm and resume an ambiguously-applied Cassandra LWT.
This avoids both an unconditional third read and an LWT on every brand-new block —
see X7.

**Why first among the code PRs:** it is the only one that fixes a state that can
permanently break a block id, and it depends on nothing else.

**Acceptance:** a stub left by a crashed or re-referenced claim is removed by either
side, and an upload that meets one on any of the six retry-wrapped funnels succeeds
instead of exhausting its retries. The seventh (web-session) funnel is out of scope
here: it stays fail-closed and returns `500` on a fence/contended-stub until PR-5
gives it a re-probing `409` + `Retry-After` alongside the traffic/staging hoist — a
characterization test pins that boundary so PR-5 must consciously flip it. Unit tests
must drive the real `store -> materialize` loop, not call the repair directly — the
direct-call tests on the research branch could not catch that the repair was
unreachable behind the probe.

**Execution checklist:**

1. Extend the existing probe read with `created_at`, `gc_claim_id` and
   `gc_claimed_at`. Classify in this order: absent row -> existing orphan-fence check,
   then `NeedsPut`; active deleting owner -> `BlockedByGC`; unexpected/stale ownership
   -> error; non-null `created_at` plus blank class -> malformed-row error. For an
   unowned row with null `created_at` and blank class, check `gc_s3_orphans` before
   returning `RepairableStub`; an observed orphan remains `BlockedByGC`. Do not
   require `sha1` or `representation_id` to be empty on a stub.
2. Add `db.RepairReleasedBlockStub(...)(bool, error)`: acquire an upload-owned
   `repairing_stub` token only from null `created_at`/`storage_class` and absent GC
   ownership, recheck `gc_s3_orphans`, then delete only that token. This two-LWT
   shape is required because Cassandra can report a null-only conditional DELETE as
   applied for an already-absent row. Add the narrower worker-side
   `GCStore.DeleteClaimedBlockStub(...)(bool, error)`, fenced by claim id and the stub
   lifecycle. Prove all predicates against real Cassandra/Scylla, not only mocks.
3. Reject whitespace-only `storage_class` before issuing a metadata LWT. When the
   first-writer insert does not apply, make metadata materialization recognize a
   released stub, claim/repair it and retry the insert. This is the race
   backstop for a stub appearing after a probe and for the unprobed web-session
   upload path; complete/malformed rows still fail closed.
4. Extend the GC Store interface, Cassandra adapter and mock. In `processBlock`,
   delete a re-referenced stub under the owned claim instead of releasing it; retain
   the ordinary release behavior for a complete metadata row. A false CAS result is
   a stale observation and must not be treated as success. Remove the writer-side
   active-claim release in `internal/api/v2/fs_helpers.go`; only the GC owner may
   release or delete an active claim. Writers wait/retry or fail closed.
5. Handle `RepairableStub` through one shared helper in all six existing upload probe
   switches. A completed repair continues through the exact existing `NeedsPut`
   branch. A **lost CAS or a reappeared orphan fence is a benign concurrency loss,
   not a hard error**: `RepairReleasedBlockStub` returns `(false, nil)`, the funnel
   performs no PUT and returns the existing retryable `ErrBlockDeleteInProgress` so
   the materialization wrapper re-probes and converges to `Reusable` (a concurrent
   uploader finished) or `BlockedByGC` (GC re-fenced). Only a genuine Cassandra error
   fails closed as `UnknownError`. The metadata-upsert backstop applies the same rule
   at the helper boundary: a contended stub repair surfaces the
   `db.ErrBlockStubRepairContended` sentinel, which `RegisterUploadedBlock` translates
   into `ErrBlockDeleteInProgress`, so the six funnels wrapped in
   `RetryUploadedBlockMaterialization` re-probe and converge instead of exhausting
   retries. The unprobed web-session funnel (`v2/blocks.go UploadBlock`) still surfaces
   that signal as a `500`: it has no retry wrapper and no `409` mapping yet, which is
   **PR-5's job** (server `409 block_delete_in_progress` + `Retry-After`, landed with
   the frontend soft-retry). This is pre-existing — `RegisterUploadedBlock` already
   returned `ErrBlockDeleteInProgress` on a plain GC fence before PR-2 — so PR-2 only
   makes the backstop's DB contract correct and does not change the web funnel's HTTP
   surface. Do not add a read to the ordinary absent-row path and do not
   import PR-3's new retry sentinels or metrics. Note: `BlockDeleteFenceActive` is
   deliberately **not** extended to fence on `repairing_stub` — the upsert path must
   stay reachable to self-heal a stuck deterministic repair token.
6. Unit-test the full decision and race matrix: absent row (with/without orphan
   fence), released PK-only and partially identity-backfilled stubs, released stub
   with an orphan fence (the repair removes its own `repairing_stub` token but leaves
   the orphan fence intact and performs no PUT), active stub, complete active row,
   malformed complete row, stale ownership, both successful deletions, lost CAS with
   no PUT, worker stub-delete versus complete-row release, all six probe switches,
   web-session materialization, and pre-LWT class validation. Include the retryable
   convergence: a lost-CAS repair re-probes and finishes as `Reusable`, and a
   contended metadata-upsert backstop translates to the retryable fence signal.
   Assert `/blocks/check` and `file_from_blocks` never advertise a stub as reusable.
7. Keep the engine-observation test, but add deterministic real-Cassandra fixtures
   using a primary-key-only INSERT for a released stub and explicit ownership columns
   for an active stub. Verify the rows are visible, prove the conditional CQL, and run
   a real probe -> repair -> store -> materialize -> reusable lifecycle that cannot
   skip based on engine behavior.
8. Run focused unit tests, `go test -race` for affected packages in Docker, build,
   vet, integration-tag vet, and focused Cassandra integration regressions. Record
   exact commands/results in the PR, but move only F2 and X7 to closed after PR-2
   merges. X1/X2 remain open, later-PR behavior stays excluded and destructive GC
   remains disabled.

Verification completed 2026-07-21 (the full integration service retains its built-in
health waits; its command was not overridden):

```bash
docker compose --profile test run --rm --build gotest go test -count=1 ./internal/db ./internal/gc ./internal/api/...
docker compose --profile test run --rm --build gotest go test -race -count=1 ./internal/db ./internal/gc ./internal/api/...
docker compose --profile test run --rm --build gotest go build ./...
docker compose --profile test run --rm --build gotest go vet ./...
docker compose --profile test run --rm --build gotest go vet -tags=integration ./...
docker compose --profile test run --rm --build go-integration-test
```

All six commands passed. The first integration run found one stale fixture that set
only `gc_state='deleting'`; it was corrected to seed the full claim identity and the
complete integration suite then passed. The race run also exposed an unsynchronized
test counter in `blocks_test.go`; the counter is now atomic and the rerun passed.

Re-verified 2026-07-21 after reclassifying the benign lost stub-repair race as
retryable (`RepairReleasedBlockStub` returns `(false, nil)`; the metadata-upsert
backstop returns `db.ErrBlockStubRepairContended`, which `RegisterUploadedBlock`
maps to the retryable fence signal). Build, vet, vet -tags=integration,
`go test -race` for `./internal/db ./internal/gc ./internal/api/...`, and the full
integration suite were re-run in Docker; the integration suite passed twice
consecutively (~257–266s each), so the single earlier failure was a one-node-stack
flake, not a regression.

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
`gc_fence` / `probe` / `materialization` and never label a materialize-phase write as
`probe`; and each of the three retry wrappers is exercised under a fence to confirm
the repeat behaviour shared by the six wired funnels. Full six-call-site coverage and
the deterministic fast-clear regression remain PR-5 acceptance.

**Implementation notes (as landed):**

- `RegisterUploadedBlock` is now linear: it reads the delete fence **once** and
  returns `ErrBlockDeleteInProgress` immediately on an active fence, leaving the
  provisional reference in place. The old inner wait loop is gone; the outer wrapper
  owns retrying and re-PUTs the object on the next store pass. This closes the
  **observed-fence half** of F1 — the store→materialize contract now re-PUTs whenever
  the fence is seen. It does **not** close the F1 fast-clear window where a full GC
  cycle completes before that single fence read (the fence is never observed); that is
  PR-5's deterministic real-worker regression. Destructive GC stays disabled meanwhile.
- Cassandra I/O in the helper (and the mapping write in
  `RegisterUploadedBlockAndMapping`) is tagged `v2.ErrBlockMaterializationTransient`
  with the cause preserved via `%w`; `db.ErrBlockMetadataPermanent` (empty class,
  malformed sha1, conflicting representation/sha1, corrupt GC ownership) is returned
  untagged and not retried. Default bias is transient — only deterministically
  irrecoverable failures are permanent. This closes F6. **Exception (superseded by
  PR-8 — describes PR-3 as shipped):** the provisional-expiry write failed closed —
  on its failure the helper released the reference and enqueued it rather than
  leaving a reference with no GC-discovery projection (the F10 interim guard); it
  was not retried because a retry would re-add the reference into the same split
  write. PR-8 writes both rows in one batch, so there is no split to compensate and
  that failure is now plainly retryable.
- The retry metric `block_upload_materialization_retries_total{surface,reason}` is
  emitted by all three retry wrappers — the generic `RetryUploadedBlockMaterialization`,
  the SeafHTTP `retrySeafHTTPBlockMaterialization`, and the template-CreateFile
  `retryCreateFileTemplateBlockMaterialization`. The reason is derived from the failing
  **phase** (store→`probe`, materialize→`materialization`), overridden to `gc_fence`
  only when the block is fenced — so a materialize-phase metadata write is never
  labeled `probe`. This closes F14. Note `probe` denotes the store phase (probe/HEAD/
  PUT), not a "read side", and is reached only if a store callback opts into the
  transient sentinel. PR-5 later made canonical HEAD/repair failures transient in all
  funnels and the web-session direct PUT transient through
  `StoreUploadedBlockForProbe`; raw probe errors and the six older manual direct-PUT
  branches remain non-retryable. All three wrappers retry on the transient sentinel
  and re-PUT on a fence. The planned structural consolidation did not land in PR-5
  and remains explicit debt in `TECHNICAL-DEBT.md` section 32.
- Added the cancellable `RetryUploadedBlockMaterializationContext`. Only the three
  funnels that use the **generic** wrapper are wired to it — UploadFile
  (`c.Request.Context()`), sync PutBlock (`c.Request.Context()`) and OnlyOffice (its
  `ctx` arg) — so an aborted request there stops the retry backoff instead of burning
  the budget. The two bespoke wrappers (SeafHTTP and template-CreateFile) keep their
  own non-cancellable backoff in PR-3. PR-5 made the SeafHTTP wrapper context-aware,
  but only `HandleUpload` passes its request context; `finalizeUploadStreaming` starts
  its worker group from `context.Background()`. Template CreateFile also retains
  non-cancellable bounded backoff. Those sleep-on-abort cases are debt, not a
  correctness gap.
- **Test coverage note:** the retry behaviour is verified at the level of the three
  retry wrappers (generic, SeafHTTP, template), which is the shared mechanism the six
  funnels drive, plus the linear-helper (`RegisterUploadedBlock`) and mapping unit
  tests. It does **not** yet drive all six production call sites under a live fence.
  PR-5 added the deterministic fast-clear regression with a real GC worker, wrapper
  coverage for all three implementations and web-session handler coverage. It did
  not independently drive all six pre-existing production call sites through a live
  fence; that per-handler matrix remains test debt rather than completed acceptance.

Verification completed 2026-07-21 (local, Windows dev box has no gcc so no `-race`
locally; `-race` + integration run in Docker per the verification debt below):

```bash
go build ./...
go vet ./internal/api/... ./internal/db/... ./internal/metrics/...
go test ./internal/api/... ./internal/db/... ./internal/metrics/...
go vet -tags=integration ./...
docker compose --profile test run --rm --build gotest go test -race -count=1 ./internal/db ./internal/gc ./internal/api/...
docker compose --profile test run --rm --build go-integration-test
```

Known pre-existing flake (not in PR-3's diff): a wider `-race ./internal/api/...` run
can intermittently trip `TestAcquireSeafHTTPDistributedUploadFinalizeLeaseRenewsWhileHeld`
(a distributed-lease renewal test, unrelated to the retry contract); an isolated rerun
passes. It predates PR-3 and is tracked separately; do not read PR-3's `-race` result as
covering it.

---

### PR-4 — Canonical placement and canonical reads (single PR)

**Scope:** `StoreUploadedBlockForProbe` / `ResolveNeedsPutBlockStore` — existing-
metadata `NeedsPut` stores through `probe.StorageClass` and the canonical key instead
of the serving node's preferred backend — **together with**
`internal/streaming/canonical_block_reader.go`,
`internal/db/block_storage_location.go`, the read call sites (`fileview.go`,
`sharelink_view.go`, `sync.go`, `seafhttp.go`) and canonical placement checks in the
session upload/check/commit funnel. The metadata-free no-session check/upload pair
remained on preferred-store routing until PR-7, which removed it outright (reject,
not govern) rather than canonicalizing it.

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
canonical resolution fails before a block-stream body is committed. The legacy
path-based object fallback remains an explicit F5/PR-6 issue; conditional `304` cache
validation remains an intentional no-body short circuit.

**Merged implementation:** existing-metadata `NeedsPut` writes use
the immutable `storage_class` and derived org-scoped key through
`internal/api/v2/upload_reuse.go`; `internal/db/block_storage_location.go` supplies
bounded canonical metadata lookups; and
`internal/streaming/canonical_block_reader.go` resolves reads before response headers.
The session upload, check, and commit paths resolve the same metadata placement, so a
repair cannot be committed in a preferred bucket that canonical readers will ignore.
The API/SeafHTTP read surfaces in `internal/api/seafhttp.go`, `internal/api/sync.go`,
`internal/api/v2/blocks.go`, `internal/api/v2/fileview.go`,
`internal/api/v2/sharelink_view.go`, and related V2 readers consume that canonical
location rather than request routing. `internal/integration/gc_upload_fence_test.go`
creates a bucket-B-pinned library, seeds zero-reference metadata in bucket A, drives
the production Sync PUT endpoint, proves bytes exist only in A while metadata and the
SHA-1 mapping remain canonical, then reads the bytes back through the production Sync
GET endpoint from that B-preferring library. It then removes A's object and drives the
real web session check/upload/commit/download flow, including uppercase hash inputs,
to prove the session repairs and verifies A without writing B.

PR-4 verification completed on 2026-07-21:

```bash
# completed: PASS
go test ./internal/db ./internal/storage ./internal/streaming ./internal/api/...

# completed: PASS (real Cassandra + two MinIO buckets)
docker compose --profile test run --rm --build go-integration-test go test -tags=integration -v -count=1 -timeout 3m -run '^TestNeedsPutUsesCanonicalMinIOBucket$' ./internal/integration

# completed: PASS
docker compose --profile test run --rm --build gotest go test -race -count=1 ./internal/db ./internal/storage ./internal/streaming ./internal/api/...

# completed: PASS (230.573s)
docker compose --profile test run --rm --build go-integration-test
```

---

### PR-5 — Cover the remaining funnel and normalise the wrappers

**Scope:** wrap the web block-session path in `v2/blocks.go`, which PR-4 now probes and
routes canonically but which still has no bounded store-to-materialize retry. Traffic
accounting and staged-block reservation remain single-shot across retries. Normalise
the SeafHTTP wrapper's retry-reason labels against the generic one. Frontend: treat
the new `409 block_delete_in_progress` as a soft retry honouring `Retry-After`.

**Not** "the moment everything gets connected" — PR-3 already did that for six
funnels. This closes the seventh and makes the wrappers consistent.

**Depends on PR-4**, not merely follows it: this funnel already calls the canonical
store helper, so its retry contract must preserve that placement.

**Why the frontend rides along:** this PR is what makes the 409 reachable from the
web client. Splitting them would ship a user-visible upload failure for one release.

**Acceptance:** the deterministic fast-clear regression (real GC worker paused at its
post-claim liveness read) fails before this PR and passes after.

**Implementation as merged (#141):** every materialization wrapper now runs
`store -> materialize -> canonical store confirmation`. The confirmation executes
after the provisional reference is durable and repairs bytes when a complete GC cycle
deleted them and cleared its fence before materialization could observe it. The web
session funnel uses the same bounded cycle; staged admission, traffic accounting and
staged metrics remain single-shot. An exhausted fence returns coded
`409 block_delete_in_progress` with `Retry-After: 1`, and the browser treats only that
explicit conflict as a bounded soft retry. The deterministic component regression
drives a real `gc.Worker` paused after its post-claim zero-reference read and proves
the fence is already clear before publication and confirmation repair.

**Deferred from the original PR-5 acceptance:** the generic, SeafHTTP and template
CreateFile wrappers remain separate. `HandleUpload` backoff is request-cancellable;
streaming SeafHTTP currently roots its worker context at `context.Background()`, and
template CreateFile backoff is also not request-cancellable. Coverage proves the three
wrapper mechanisms, the web-session handler and the real-worker fast-clear
interleaving, but does not execute all six older handlers independently under a live
fence. These are documented debts; they do not weaken the
`store -> materialize -> confirm` safety contract that landed.

PR-5 verification completed on 2026-07-22:

```bash
# completed: PASS
go test ./internal/db ./internal/storage ./internal/streaming ./internal/gc ./internal/api/...
go build ./...
go vet ./...
go vet -tags=integration ./...

# completed: PASS
docker compose --profile test run --rm --build gotest go test -race -count=1 ./internal/db ./internal/storage ./internal/streaming ./internal/gc ./internal/api/...

# completed: PASS (265.648s final rerun; 228.669s initial run)
docker compose --profile test run --rm --build go-integration-test

# completed: PASS
npx jest --runInBand src/components/file-uploader/__tests__/block-upload-orchestrator.test.js
npx eslint src/components/file-uploader/block-upload-orchestrator.js src/components/file-uploader/__tests__/block-upload-orchestrator.test.js
```

The first full race run exposed an unsynchronized counter in an existing SeafHTTP
lease-renewal test. The counter is now mutex-protected; both the isolated regression
and the complete Docker race rerun passed.

---

### PR-6 — Download fail-closed and removal of the path-based fallback

**Scope:** `seafhttp.go` `HandleDownload`, `lookupFileBlocks`, directory resolution
and ZIP preflight.
The legacy `<org>/<repo><path>` fallback and `resolveLibraryObjectStore` are removed.
The fail-closed HTTP contract is:

- **503 with `Retry-After` for every metadata/read failure, including a validated
  local listing without the entry.** Production reads use `LOCAL_QUORUM`; a valid
  listing can still be an older cross-DC snapshot because `access_tokens` and
  `libraries` are independent partitions with no global replication order. A local
  absence observation therefore cannot safely produce 404.
- **403 `lib_need_decrypt`** when an encrypted library has no decrypt session.
- ZIP traversal limits remain **413**.

Directory listings are validated all-or-nothing before they may resolve anything:
reject blank or JSON-null values, null entries, empty or unsafe path-component names,
non-40-hex ids, duplicate names, duplicate JSON keys, missing/null/fractional or
unknown modes, and disagreement between a dirent's mode and `fs_objects.obj_type`.
`encoding/json` otherwise keeps the last duplicate key silently, which can serve the
wrong FS object or hide a present file.

**Precondition:** confirmed with the product owner that production starts from empty
buckets, so no path-based object exists. **If that ever stops being true this PR must
be revisited**, because it removes the only way to read such an object.

**Acceptance:** classifier tests including that neither a validated local absence nor
a referenced-row `ErrNotFound` becomes 404; the full corrupt-listing matrix routed
through the parse-and-match path rather than the parser alone;
encrypted-without-session never reaches a plaintext object; and end-to-end download
tests against real Cassandra for present, deleted, dangling and corrupt metadata.

**Suggested additional acceptance:** a property-based test over the parse-and-match
helper asserting that its internal absence sentinel is *never* reported except for a
well-formed listing with no match. The HTTP layer must still map that sentinel to 503.
Successive audits found corrupt shapes hand-written cases missed, so generated input
also covers unsafe names and invalid mode variants.

**Implementation as merged (#142):** `parseValidatedDirEntries` in
`internal/api/seafhttp.go` validates a listing by walking each entry's JSON tokens
instead of unmarshalling into a map, so a repeated `id` or `name` key is rejected
rather than silently resolved to its last value. Validation is **all-or-nothing**:
any malformed entry fails the whole listing. A first revision returned a valid match
when only a *sibling* was corrupt, so one bad dirent could not make a healthy file
unreadable; review showed that exception serves the wrong FS object when the corrupt
entry carries the requested name (or hides it behind a repeated `name` key), so the
listing is ambiguous exactly where it matters. The internal `errDirEntryAbsent`
sentinel is produced only when the whole listing validated and nothing matched, but
it maps to HTTP 503 because a `LOCAL_QUORUM` snapshot may be stale across DCs.

`findValidatedEntryInDir` no longer turns a read failure into absence and preserves
the validated mode so callers can enforce file/directory type.
`respondSeafHTTPDownloadError` is the single place that applies the contract —
`v2.ErrLibraryEncryptedNotUnlocked` to the app-wide 403 `lib_need_decrypt`, and every
metadata/read failure (including `errDirEntryAbsent`) to 503 with `Retry-After` — and
it writes nothing once streaming has committed headers. `HandleDownload` lost its
path-based fallback and `resolveLibraryObjectStore` was deleted with its only caller.

Both zip paths share the validated parser: the directory walk previously answered
404 for any directory-resolution failure, and `prepareZipDirectory` still parsed into a
map — keeping the last value of a repeated key, silently skipping entries with no
name or id, and reading a blank listing as an empty directory — so a corrupt listing
could produce a `200` zip with wrong or missing content. Names must now be one safe
path component (no `.`, `..`, slash, backslash or NUL); mode is required, exact and
restricted to a regular file or directory; preflight also verifies mode against the
referenced `fs_objects.obj_type`. ZIP encryption is probed through the same
injectable helper as file download and **before** the head/commit walk: a probe
I/O failure is 503 (never "not encrypted"), and a confirmed encrypted library
without a decrypt session is 403 `lib_need_decrypt`. Head/commit misses, block-store
resolution, canonical placement, and non-limit ZIP preflight failures all use the
same 503 classifier and `Retry-After` as file downloads.

The discarded-probe-error shape that made ZIP stream ciphertext as a 200 turned out
to exist at **six** more sites in `internal/api/v2`, in two classes: four that took
the plaintext branch (`UploadFile` persisting plaintext into an encrypted library,
`ServeRawFile`, and both share-link readers) and two that converted the error into
permission outright (`requireDecryptSession`, guarding 20 call sites, and
`checkDecryptSession`). All six are fixed here behind a single `libraryIsEncrypted`
probe, each failing closed as its own contract allows. Recorded with the full
inventory and the per-surface reasoning in `ISSUE-ENCRYPTED-FLAG-UNCHECKED-01`.

`findValidatedDirEntry` is the single parse-and-match path, shared by the production
lookup and by the corrupt-listing matrix. An intermediate revision left a separate
`findDirEntryID` that only the tests still called, which would have let a change to
the matching rule keep the matrix green while breaking the real caller.

**Accepted cost, tracked as `ISSUE-DOWNLOAD-NO-404-01` in `docs/KNOWN_ISSUES.md`:**
dropping 404 entirely means a genuinely deleted file answers 503 forever, so clients
retry a request that can never succeed, and the SeafHTTP surface now disagrees with
`internal/api/v2`, which still answers 404. Reintroducing 404 needs a read that can
prove *global* absence; that issue lists the options.

Coverage: the corrupt-listing matrix routed through parse-and-match rather than the
parser alone, including a corrupt copy of the requested name in both orders; a seeded
generative property test asserting the internal absence sentinel is never produced
for a listing that is unparseable, holds an invalid entry, or names the target; the
classifier all three ways, including that local absence and a bare
`gocql.ErrNotFound` are 503 and that encrypted-without-session is 403; that a
committed response is never rewritten; that a present path-based object is *not*
served; the zip walk's use of the validated parser (safe names, required exact mode,
repeated `id`, missing `id`, blank listing) and the shared retry classifier; and
`TestDownloadFailClosedContract` in `internal/integration`, which drives the four
end-to-end cases against real Cassandra through the production endpoint — present
file, deleted file, dangling `fs_object`, and corrupt listing.

**Both** public share-link bootstrap endpoints are pinned by behavioural
integration tests, not only the first. `/share-links/:token/bootstrap/` and
`/share-links/:token/files/bootstrap/?p=` each get a healthy 200 first and then a
deterministic canonical-reference failure, asserting 503 + `Retry-After` and a body
free of block ids, bucket names and Cassandra/S3 detail. The second endpoint needed
its own test because neither the first test nor the AST check constrains it: the AST
check only proves no *other* method calls `respondShareBootstrapError`, so
`GetShareLinkFileBootstrap` could have gone back to `c.JSON(status, err.Error())`
with every test still green. Verified by injecting exactly that regression: the new
test fails, the sibling test does not.

---

### PR-7 — Legacy no-session upload removed (F8)

**Decision (taken in review): reject, do not govern.** The plan offered two options
for the no-session branch of `/api/v2/blocks/upload` — govern it (write canonical
metadata + a deterministic provisional pin so GC can reclaim the object) or delete
it. Deletion won, because governing would have invented a third class of
provisional reference — one with no session to own it and no natural expiry — right
before PR-8 hardens provisional-ref durability and while X3 (PUT-before-durable-intent)
is still open. Rejecting removes the leak by construction instead of adding
machinery to clean up after it, and it matches the project's "hard errors over
fallbacks that mask corruption" stance.

**Evidence it has no client.** The web frontend always sends `X-Block-Upload-Session`
(`frontend/src/utils/seafile-api.js`); mobile-frontend never references the endpoint;
desktop/mobile sync use `/seafhttp/`. The only callers were integration tests and a
benchmark script that already documented it as "not a correctness benchmark".

**Scope:**
- `v2/blocks.go` `UploadBlock`: a session-less request is rejected with **400
  `block_upload_session_required`** before the body is read, so it can neither
  allocate the block buffer nor leave an unreferenced S3 object (F8). The whole
  legacy branch — `getBlockStore` → `BlockExists` → per-block quota → `PutBlockData`
  — is deleted. Past the guard, `resolution` is provably `uploadSessionValid`, so the
  session branch was unwrapped and the unreachable tail removed rather than kept as a
  defensive assertion: a 400 no request can reach asserts nothing.
- `v2/blocks.go` `CheckBlocks`: the paired no-session existence oracle is removed the
  same way. A code comment had explicitly tied it to the no-session upload it served
  ("PR-7 will govern and canonicalize both together"); with the writer gone it is a
  bare-hash intra-org content oracle with nothing to justify it, so it too returns
  400. `respondBlockUploadSessionRequired` is the single 400 both endpoints emit.
- `frontend/src/utils/seafile-api.js` `withBlockUploadSessionHeader`: previously
  dropped the header silently when `session` was falsy, so a frontend bug that left
  `session` undefined fell through to the legacy path — bytes uploaded, orphaned,
  and the later commit refused, all with no error surfaced. It now throws, turning
  that silent failure into a visible one at its source.
- `getBlockStore` (the generic hostname-routed store) is left in place though it now
  has no production caller: its two unit tests pin the fail-closed P10 routing
  contract (never serve an org-less global store), and the routes comment reserves
  it for a future repo-scoped block-read flow. Removing it would delete
  security-relevant regression coverage for no tidiness gain.

**Acceptance (met):**
- `TestNoSessionBlockEndpointsRejected` (integration) drives both endpoints without
  a session, asserts 400 `block_upload_session_required`, and — the property that
  mattered — asserts the rejected upload stored **no** S3 object, proving F8 closed
  end to end.
- `TestUploadBlock_NoSessionIsRejected` and `TestCheckBlocks_NoSessionIsRejected`
  (unit) pin the same contract at the handler.
- Legacy-path quota tests were retired with the path: the two per-block
  storage-quota dedup tests were legacy-only semantics (session uploads charge the
  file delta at commit, not per block) and were removed; `TestV2BlockUploadTrafficQuotaExceeded`
  was converted to a session upload, since the traffic-quota check runs before
  session admission and is now the only block-upload path. Session storage-quota
  behaviour stays covered by `TestWebBlockUploadSessionStagingSkipsLogicalStorageQuota`
  and `TestWebBlockUploadCommitEnforcesLogicalDelta`.
- The S3-only-block fixture (`TestWebBlockUploadRejectsUncommittableBlocks`) now
  writes straight to the store via `stageS3OnlyBlock` instead of through the deleted
  endpoint — a more honest way to fabricate the "physically present, metadata-absent"
  state it needs.
- Full Compose integration suite green.

---

### PR-8 — GC scanner Phase 0 and provisional-ref durability

**Scope:** `internal/gc/scanner.go` (Phase 0 defers to the Cassandra TTL instead of
deleting a possibly-renewed reference), `internal/db/provisional_block_ref_expiry.go`
(`AddProvisionalBlockReferenceWithExpiry` in one normal logged batch with TTL-bound
reference and canonical tracker rows), the GC store adapters, and the upload success
and rollback paths that previously released provisional rows eagerly. The unmerged
generation migration was deleted; the final provisional protocol has no generation
identity and no conditional mutation on `block_references` or
`gc_provisional_block_refs`.

**Independent of the upload series** — could land at any point. Listed late only
because it is lower risk, not because it is blocked.

**The final protocol deliberately uses TTL instead of ownership compares.**

1. **Creation and renewal use one normal logged batch.**
   `AddProvisionalBlockReferenceWithExpiry` writes the `block_references` `up:` row
   with a TTL derived from `expires_at`, writes the canonical
   `gc_provisional_block_refs` row with the same deadline and a longer TTL, writes
   the current by-day discovery projection, and retracts the previously observed
   projection when the deadline moves. There is no `IF`, serial consistency,
   generation column or Paxos operation on either provisional row.
2. **The discovery projection is durable.** The by-day row has no TTL. The
   canonical tracker normally outlives the reference by the default 24-hour scanner
   interval, but correctness does not require a scan inside that margin: after the
   canonical row expires, the projection still drives the canonical-missing recovery
   path.
3. **Success and rollback never release provisional rows eagerly.** Upload publish,
   materialization rollback and retry cleanup leave the `up:` reference, canonical
   tracker and projection to their TTL/scanner lifecycle. This removes the race
   rather than trying to win it with a compare. Session admission slots, committed
   session state, staged-block ledgers, traffic charging, logical-storage accounting
   and their idempotency markers are independent and continue to follow their own
   cleanup rules.
4. **Phase 0 never mutates the reference or canonical tracker.** For every due
   projection it first checks the exact provisional reference. If that row is still
   present, Phase 0 defers the record, preserves the projection and holds the day
   cursor back. If it is absent, Phase 0 checks all block references, durably
   persists a GC candidate only when the block is at zero references through
   `EnsureBlockGCCandidate`, and only then deletes the processed projection. This
   keeps every candidate producer on the same LWT protocol, preserves the earliest
   candidate identity, and repairs a missing by-day projection. The
   canonical-present and canonical-missing paths use the same ordering.
5. **Failures remain retryable.** A reference read, liveness read, candidate write
   or projection delete failure leaves the projection and cursor available for a
   later scan. A logged batch provides atomic application/replay, not isolation;
   stale renewal projections are therefore expected and safe because Phase 0 checks
   the exact live reference before sweeping any projection.

**Operational trade-off:** successful and rolled-back `up:` references can remain
for up to the 48-hour provisional TTL instead of disappearing at request completion.
The canonical tracker carries an additional 24-hour scanner margin, while the
projection remains until Phase 0 resolves it. This temporarily retains rows and can
delay reclaiming an abandoned block, but it removes provisional-row Paxos from hot
upload paths and keeps every unresolved zero-reference transition discoverable.

**The user-visible half of that trade-off, stated explicitly.** It is not only
*abandoned* blocks. A file **deleted shortly after it was uploaded** does not free
its physical space until the same TTL elapses: the library cascade removes the
permanent `fs:` reference, but `BlockHasReferences`
([`worker.go`](../internal/gc/worker.go) `removeFSObjectBlockReferences`) still sees
the surviving `up:` row, so the block is never enqueued as a zero-ref candidate.
Reclamation happens later, when the `up:` reference expires and Phase 0 resolves its
projection — correct and convergent, but up to ~48h after the delete. Before PR-8 the
success path released `up:` at commit, so this delete freed space immediately.

Anyone reading physical-usage counters or S3 spend should expect that lag. Logical
library/user quota accounting stays governed by filesystem deltas and is **not**
intentionally delayed by the surviving provisional reference — the independence note
above covers exactly this — so a "I deleted it and my quota did not drop" report is a
separate bug, not this lag, unless a specific test proves that quota depends on the
block's physical reclamation. The lever if the physical lag proves unacceptable is
`ProvisionalBlockReferenceTTLSeconds` (currently 48h, sized for long resumable
uploads) — **not** reinstating eager release, which is where all three release-side
races came from.

**Dead code removed with the superseded designs:** eager provisional release helpers
and hooks, generation-aware reference/tracker deletes, release-CAS tests, generation
fields and migration `013`, tracking-only writes, Phase 0 tracker deletion, and the
alternate normal-write candidate helper. The test-only controlled teardown helper
remains an unconditional normal batch solely to clean fixtures; production creation,
renewal and recovery do not call it.

**Acceptance (met):**
- `TestScanner_ScanExpiredProvisionalBlockRefs_NeverDeletesALiveReference` — reference still
  present at deadline: it survives, no candidate is created, the tracker stays, and the
  cursor does not advance past its day. Verified by reintroducing the old
  `RemoveBlockReference` call: the test fails.
- `TestScanner_ScanExpiredProvisionalBlockRefs_KeepsTrackerWhenRenewedMidPass` — a renewal
  injected during the liveness decision leaves the renewed tracker and reference intact;
  Phase 0 has no tracker mutation to race.
- `TestRegisterUploadedBlock_WritesReferenceAndExpiryAtomically` — one call, nothing runs
  after its failure, nothing is compensated, and the error is retryable.
- `TestScanner_ScanExpiredProvisionalBlockRefs_PromotesUnreferencedBlockBehindOrphanedProjection`
  — the recovery path: an orphaned projection over an unreferenced block yields a candidate
  before the projection is swept. Verified by removing the promotion: the test fails.
- `TestWebBlockUploadWritesReferenceAndExpiryTogether` (integration, real Cassandra) — after
  a session upload, all three rows exist, the reference's TTL expiry interval brackets its
  tracked deadline, the canonical tracker has its scanner margin, renewal refreshes both
  horizons and retracts the superseded projection, and successful commit leaves all three
  rows intact while cleaning the independent session admission state.
- `TestProvisionalRollbackPreservesTTLRows` (integration, real Cassandra) — a
  post-materialization mapping conflict drives the production rollback boundary and leaves
  the provisional reference, longer-lived tracker and durable projection intact.
- `TestProvisionalProductionCQLDoesNotUseLWT` (integration) — the production provisional
  source contains no generation state, CAS API or conditional CQL against
  `block_references` / `gc_provisional_block_refs`; the audit intentionally does not
  prohibit the established `gc_block_candidates` LWT.
- `TestProvisionalPhase0CanonicalPresentAndMissingRecovery` and
  `TestProvisionalPhase0FailuresPreserveProjectionForRetry` (integration, real Cassandra)
  cover live-reference deferral, promotion-before-sweep with and without the canonical
  tracker, and retry after a failed liveness/candidate decision.
- Existing Phase 0 tests were retargeted rather than deleted: they now stage the state the
  TTL actually produces (reference already gone) instead of the state only the old
  self-deleting scanner produced.

---

### PR-9 — Streaming leak fix

**Scope:** `PrefetchBlock` / `StreamBlocks` context-aware delivery closing an
abandoned reader, and `QueryBlockSizes` partial-cache reuse.

**Must run `go test -race` in Docker** — this is the PR that changes goroutine and
channel semantics.

**Implemented on `fix/streaming-prefetch-reader-leak`.** `StreamBlocks` runs one
block ahead, so on any early exit it dropped the prefetched next block with its S3
`io.ReadCloser` still open (F11). The reader of the block being streamed is closed by
`streamOneBlock` via `defer`, so it is released even if the write/copy panics; the
block prefetched one ahead is owned by `StreamBlocks`, which tracks it and, in a
defer, closes any reader on every exit path (block error, write error, copy error,
panic) — the handle is nil whenever nothing is in flight, so normal completion never
blocks. Prefetches run under a **child context** that the cleanup cancels *before*
draining, so an abandonment whose cause does not cancel `ctx` (a write error) cannot
block the handler for a whole S3 fetch/timeout; a ctx-respecting `GetBlockReader`
returns at once and its reader, if any, is still drained and closed. The next block
is prefetched only once the current one is known good (a failed current block ends
the stream, so opening its successor is wasted S3). `PrefetchBlock` also delivers
`ctx.Err()` without opening an S3 request when its context is already canceled.
`QueryBlockSizes` was reviewed for the audited partial-cache concern and left
unchanged: the reuse is correct (cached sizes kept, the rest fall through to DB then
S3) and its bounded fan-outs are buffered to capacity, so an early return leaks no
goroutine and it holds no readers.

**Acceptance (met):** `internal/streaming/stream_blocks_test.go` uses an instrumented
`io.ReadCloser` counting opens and closes and proves opens==closes (no leak, no
double-close) on a mid-stream write error, a write **panic**, a current-block fetch
error (the next block is never opened), and the happy path (every reader once,
without hanging); a prefetch that blocks until its context is canceled proves the
cleanup cancels in-flight work before draining and returns promptly rather than
waiting out the fetch (that test hangs to timeout against the unpatched drain); and
`PrefetchBlock` skips the fetch on a pre-canceled context. Streaming unit tests pass
under `go test -race` in Docker; `internal/api` and `internal/api/v2` pass in Docker.
All download-related integration tests passed (round-trip, chunked, encrypted, zip,
region-pinned, share-link); the full suite had one unrelated pre-existing
eventual-consistency flake (`TestShareLinkGCProjection_UpdateExpirationRekeysProjection`)
that passed in isolation.

---

### PR-10 — HTTP request hardening

**Scope:** body-size limits on `PutBlock` and `check-blocks`, and the check-blocks id
cap.

**Split from PR-9** because nothing forces them to ship together: a reader leak and a
DoS bound share no code and no deploy dependency. Rule 1 applies to this series too.

**Implemented on `fix/sync-unbounded-request-bodies`.** A shared
`readLimitedRequestBody(c, maxBytes)` swaps the request body for an
`http.MaxBytesReader` before `io.ReadAll`, so an oversized body is never fully
buffered; on overflow it answers `413` and the caller returns. `PutBlock` also fast-
rejects an oversized declared `ContentLength` before reading, with the cap
(`maxSyncBlockBytes`, 257 MiB) sitting just above the 256 MiB adaptive-chunk ceiling
plus a cipher-padding margin. `check-blocks` reads under `maxCheckBlocksBodyBytes`
(16 MiB) and, after parsing, rejects a list longer than `maxCheckBlockIDs` (100k)
with `413` before any per-id classification or DB lookup.

**The id cap is enforced during the parse, not after it.** An audit of the first
revision found the cardinality bound applied *after* `strings.Split` /
`json.Unmarshal` had already materialized the list — allocating precisely what the
cap exists to bound. The 16 MiB body cap does not constrain this: `TrimSpace` cannot
collapse `"a" + "\n"*n + "a"`, so a body at the cap still reaches ~16.7M ids
(**272 MB** of string headers from `strings.Split`'s single pre-sized `make`), and a
JSON array of empty strings reaches ~5.6M ids (**198 MB**, since `json.Unmarshal`
grows the slice to completion first). Both measured. The newline path now counts
delimiters before splitting, and the JSON path decodes element by element through a
`json.Decoder`, stopping at the cap — while still requiring the closing bracket and
rejecting trailing content, so it is no laxer than the `json.Unmarshal` it replaced.
The same bodies now cost **16 MB** and **34 MB**.

**DRY.** `GetLockedFiles` had grown the same `MaxBytesReader` + count-cap pattern
independently; it now shares `readLimitedRequestBody`, which moves its oversized-body
answer from `400` to the semantically correct `413` (an abusive-caller path only — the
desktop client sends one entry per synced library and never approaches 256 KiB).

**Deliberately scoped to F12.** `PutCommit`, `PackFS`, `RecvFS` and `CheckFS` on the
same sync handler share the identical unbounded `io.ReadAll(c.Request.Body)` pattern.
They are the same class but are **not** part of F12; each needs its own safe cap from
its payload profile. Tracked as **X9** in the findings registry rather than as a
narrative note, so PR-10 is not mistaken for the end of HTTP body hardening. The
shared helper makes each a one-line change once those caps are chosen.

**What a per-request cap is not.** Review raised two limits of the fix that are real
but out of scope, now tracked as **X10** and **X11**. X10: the seafhttp block routes
carry only `authMiddleware` — no concurrency or rate limit — so N concurrent uploads
still cost N × 257 MiB, where the web block path already has both a per-user
concurrency limiter and a per-IP rate limiter. Pre-sizing the read buffer from
`Content-Length` is *not* the fix: that length is attacker-controlled, so pre-sizing
would let an empty body allocate the full cap. X11: the 100k id cap bounds the
parser's memory, not the work an accepted request triggers — 100k ids can still drive
~100k sequential Cassandra point reads in the `CheckBlocks` loop. Both are bounds
worth choosing from load data; neither reopens F12, whose defect was *unbounded*.

**Acceptance (met):** `internal/api/sync_body_limits_test.go` covers the helper's
byte boundary directly (a body up to and including the cap returns intact with no
response written; one byte over is `413` via `MaxBytesReader` without allocating a
real 257 MiB body), a plain read error staying `400` so it is distinguishable from an
overflow, a chunked body with no declared length still cut at the cap by the endpoint,
`PutBlock` rejecting an oversized declared `ContentLength` while letting a block
declared exactly at the cap through the size gate, the id cap on **both** body formats
at and one over the boundary, the JSON decoder's strictness (unterminated array,
trailing content, a second array, non-string element), and a `TotalAlloc` assertion —
cumulative and therefore GC-independent — that the two pathological bodies are cut
during the parse rather than after materialization. The `internal/api`,
`internal/api/v2` and `internal/streaming` suites pass.

---

### PR-11 (deferred) — Remove the per-block Paxos

**Scope:** make `storage_class` deterministic per `(org_id, block_id)` so the
first-writer `INSERT ... IF NOT EXISTS` can be dropped.

**Not in scope: merging the lifecycle observations.** An earlier draft proposed
merging the pre-store probe and post-reference fence reads. That ordering is the mutual
exclusion that makes the protocol work: serving the fence result from the cached probe
would let GC authorize a delete while the writer publishes from stale state. PR-5's
third, post-materialization confirmation is also distinct; it repairs the fast-clear
case after the fence has disappeared. Optimize inside one observation point if useful,
but never reuse one point to answer another.

**Deliberately last, and deliberately not designed yet.** This is the item most
likely to be superseded by a better idea, and it is the one with real performance
stakes: under `SERIAL` and a multi-DC posture the current LWT is one *global*
consensus round per block, ~128 cross-region rounds per GB at the 8 MB block size.
It is **pre-existing on `main`** (`e3883aa5d`, 2026-05-28), not introduced by this
series. `13e01263a` later made the same write representation-aware; it did not
introduce the LWT.

**Correction worth carrying:** an earlier revision of this plan said the legacy
resumable path "does not pay" this. That is false. `finalizeUploadStreaming` splits a
resumable upload into 8 MB blocks and calls `RegisterUploadedBlock` per block, so it
pays the same ~128 LWTs per GB. The cost is **shared by both governed upload modes**,
which makes this a general optimization rather than something block upload has to
fix to justify itself — and it means the win, if taken, applies to every
metadata-registering upload surface. F8's no-session branch was the exception because
it skipped metadata registration — but PR-7 removed that branch, so every remaining
upload path registers metadata and pays this cost.

**Scope correction (2026-08-11):** supported surfaces can still bypass metadata
registration for individual blocks through browser/sync dedup preflight. X4 is one LWT
per block invocation that reaches `RegisterUploadedBlock`; ~128/1 GiB describes new
content/full registration at 8 MiB blocks, not every upload. The probe fast path helps
an existing complete row but not first content or rematerialization.

**Do not start this before measuring.** There is no per-statement latency metric for
that INSERT yet; add it, get the production number, then decide. Full analysis: P-4
in `UPLOAD-PERFORMANCE-SECURITY-2026-06.md`.

---

## Out of scope for the entire series

These stay open and keep destructive GC disabled:

- **Physical delete ABA** — an already-authorized key-only S3 delete can run after
  the visible fence clears. Cassandra authorization/claim generations cannot revoke
  a DELETE already in flight; only never-reused generational physical keys close X1.
- **Cross-DC reference visibility** — `block_references` are ordinary `LOCAL_QUORUM`
  writes that `SERIAL` does not cover; with RF 1 per DC the write and read quorums
  need not intersect (`ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`).
- **`ISSUE-UPLOAD-PUT-BEFORE-INTENT-01`** — the physical PUT still precedes
  materialization, so a crash between them leaves an undiscoverable S3 object.
- **Canonical read fan-out at scale** — one Cassandra point read per unique block
  before the first byte; the existing benchmark replaces Cassandra with an in-memory
  function and therefore measures nothing about the cluster.

## Verification debt carried by the whole series

- PR-2 through PR-5 ran `go test -race` in Docker. PR-9 must still run its own race
  validation because it changes separate channel behavior.
- PR-6 adds `TestDownloadFailClosedContract` against real Cassandra for present,
  deleted, dangling and corrupt metadata; the full Compose integration suite remains
  part of its merge verification.
- No multi-DC test exercises any of the cross-DC reasoning; it is derived from the
  production consistency contract, not from a reproduction. PR-6 therefore never
  converts a local absence observation into 404. The dedicated
  `config-usa.cluster.yaml` / `config-eu.cluster.yaml` test profiles use
  `LOCAL_SERIAL`; their inline "multi-DC standard" wording describes the harness,
  not production. That harness specifically does not reproduce production's
  `SERIAL` cross-DC first-writer contract.
