# Upload-Fence / Canonical-Storage Work — PR Split Plan

**Date:** 2026-07-21
**Research branch:** `docs/gc-upload-fence-rematerialization` — **not for merge**
**Status:** PR-1 merged as [#137](https://github.com/Sesame-Disk/sesamefs/pull/137);
PR-2 merged as [#138](https://github.com/Sesame-Disk/sesamefs/pull/138) (closed F2/X7).
PR-3 merged as [#139](https://github.com/Sesame-Disk/sesamefs/pull/139), closing F6,
F14 and the **observed-fence half** of F1. PR-4 merged as
[#140](https://github.com/Sesame-Disk/sesamefs/pull/140), closing F4 and F7. PR-5 merged
as [#141](https://github.com/Sesame-Disk/sesamefs/pull/141), closing F1 and F3. PR-6 is
implemented on `fix/gc-download-fail-closed` and pending review. X1/X2 remain open and
keep destructive GC disabled.

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
  irrecoverable failures are permanent. This closes F6. **Exception:** the
  provisional-expiry write fails closed — on its failure the helper releases the
  reference and enqueues it rather than leaving a reference with no GC-discovery
  projection (the F10 interim guard); it is not retried because a retry would re-add
  the reference into the same split write.
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
remains on preferred-store routing until PR-7 governs and canonicalizes both together.

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

**Current implementation (pending review):** every materialization wrapper now runs
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

**Current implementation (pending review):** `parseValidatedDirEntries` in
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

The discarded-probe-error shape that made ZIP stream ciphertext as a 200 **also
exists in `internal/api/v2`**, outside this PR's SeafHTTP scope: `UploadFile` can
persist plaintext into an encrypted library, and `ServeRawFile` can serve ciphertext
as content. Tracked as `ISSUE-ENCRYPTED-FLAG-UNCHECKED-01` in `docs/KNOWN_ISSUES.md`;
`seafHTTPLookupLibraryEncryptedFn` is the corrected shape to copy.

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
metadata-registering upload surface. F8's no-session branch is the exception because
it currently skips metadata registration.

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
