# Phase 6: fs_object destruction against a newer HEAD

## P6-2: Phase 6 suspended (2026-10-19)

Base main@d886ffe9d (#318). The maintainer chose option A (§5) on 2026-10-19.
Scope: close the two destructive paths reproduced in P6-1. Reclamation of
live-library metadata is not restored here; it is tracked as
`ISSUE-GC-LIVE-LIBRARY-METADATA-RECLAMATION-01` (PRE-GC / A1).

Contract:

1. `scanAutoDeleteExpiredObjects` is an explicit no-op. It logs the suspension,
   still records `gc_scanner_last_phase_run_timestamp_seconds{phase="auto_delete"}`
   and enqueues nothing. Its selector and the `walkFSTree` keep-set walker,
   used only by it, are removed; git history keeps them for P6-3.
   `ScanAutoDeleteExpiredObjectsOnce` stays, so integration tests can verify
   the no-op.
2. `processFSObject` first checks the effective guard
   (`effectiveLibraryGuardMode`, which honours the legacy
   `RequiresLibraryDeletedCheck` flag). An unguarded item is completed before any
   read, dry-run log, child enqueue, reference removal or delete, including
   rows queued before this change. It increments
   `gc_unguarded_fs_object_items_suspended_total` (no labels) and logs one line.
3. Guarded fs_object items (`CanonicalMustBeAbsent`, `DeletedAtIdentity`, and the
   legacy flag) keep their existing path. Library, user and org cascades and
   Phase 4 are unchanged.
4. The queue lifecycle is unchanged: a nil return completes the row, so there is
   no retry and no DLQ. Global dry-run semantics are unchanged too: a dry-run
   worker completes nothing, so a suspended row stays queued and is counted
   again on each tick. The new counter therefore counts decisions, not distinct
   rows. A completed suspended row also increments the existing
   `gc_items_processed_total{type="fs_object"}`, although nothing was deleted;
   read the suspension counter to tell them apart.

Known interactions, accepted:

- **Dedupe with a stale unguarded row.** Guarded cascades skip a child that
  already has any pending row. If an unguarded row for that child predates the
  change, the guarded parent skips it, and the worker then completes the stale
  row without mutation. The child is retained, not lost: once that row is gone,
  Phase 4 re-enqueues the deleted library's remaining fs_objects with a guard on
  its next scan. This needs rows queued before this change; on the greenfield
  v1 deployment GC has never run.
- **Representation drift signal.** The removed selector counted
  representation drift for auto-delete libraries before refusing to enqueue
  under a wrong representation. With nothing enqueued, there is nothing for it
  to guard; other phases and the delete paths keep their own checks.
- **Log volume.** One log line per suspended row per pass, the same volume as
  the old per-row dry-run "would delete" line.

Not done here: rejecting unguarded `ItemFSObject` rows at enqueue time. No
production producer writes them after #314 and this change (Phase 4 and library
cascades are guarded; `EnqueueLibraryContents` has test callers only, and its
comment now says it reclaims commits only). The worker check also covers rows
queued earlier. An enqueue-time rejection remains a possible hardening for P6-3.

Not promised: `auto_delete_days` still reads and writes, but Phase 6 performs no
reclamation in live libraries ([TECHNICAL-DEBT §18](./TECHNICAL-DEBT.md#18-library-history-retention-and-auto-delete-semantics-2026-05-15)
records that it never deleted current files either). Phase 5's exclusive-tree
retention stays open. Nothing here authorizes GC activation; PRE-GC/A1 remain
OPEN and ordinary GC stays OFF.

Backward compatible: yes for API, schema, queue format and configuration. The
behaviour change is deliberate and limited to GC with `auto_delete_days` set:
Phase 6 no longer deletes anything. Rollback is a code revert with no migration;
it restores both destructive paths, so GC must stay OFF if it is rolled back.

Tests (RED on main, GREEN here):

- Unit, ordinary suite:
  - `TestWorker_ProcessFSObject_UnguardedItemCompletesWithoutMutation`: a file
    still referenced and an orphan directory sharing that file are both
    completed, with no child, block, retry or DLQ row and the reference intact.
  - `..._UnguardedItemSuspendedBeforeDryRun`: dry-run keeps the row queued
    and mutates nothing.
  - `..._LegacyDeletedCheckFlagIsNotSuspended`: a row with only the legacy flag
    still runs its guarded destructive path after a completed hard delete.
  - `TestScanner_ScanAutoDeleteExpiredObjects_SuspendedEnqueuesNothing`: a
    fixture the old selector enqueued (an orphan, and an off-chain commit root
    sharing HEAD's file) produces no fs_object work through the phase entry
    point or a full `ScanOnce`.
- The eleven old selector unit tests are removed, because no selector remains
  to test. The fs_object mechanics tests (cascade of blocks and children, retry
  idempotence, library-scoped dedupe, queued representation, dry-run) now stage
  legitimate guarded rows after a completed hard delete, so the guarded path
  keeps its coverage. The pending-item scope regression
  (`ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01`) now uses the Phase 4 shape.
- Real stack: `TestGCPhase6InFlightSyncPublicationKeepsHeadTree` runs in both
  standard Compose runners through the isolated e19 re-execution used by the
  E1-01 tests, with its gate set there. Legs:

  | Leg | Schedule |
  |---|---|
  | `selector-suspended` | the P6-1 race and cascade inputs; the selector must enqueue nothing and not even list fs_objects |
  | `stale-in-flight-selection` | the exact rows the old selector produced for the race, queued before HEAD publishes |
  | `stale-orphan-shares-head-file` | a stale orphan directory row whose child is HEAD's file |
  | `stale-orphan-shares-aged-head-file` | the same with the Sync `up:` pin lapsed |

  The worker must dequeue and suspend every stale row (counter delta equal to
  the seeded rows), and the oracle checks fs_objects, the permanent `fs:`, the
  absence of block candidates, canonical P and the S3 bytes.

Evidence: see [P6-2 evidence](#p6-2-evidence-2026-10-19).

## Bounded plan, P6-1 (2026-10-19)

Base main@bb89d7b12 (#317). Scope: `ISSUE-GC-PHASE6-FSOBJECT-EXECUTION-TOCTOU-01`
(P1 / PRE-GC; provisional when planned, confirmed by the RED in §4). The audit of
this PR found and reproduced a second Phase 6 defect,
`ISSUE-GC-PHASE6-DIRECTORY-CASCADE-SHARED-FSOBJECTS-01` (§1b). Evidence only: no
runtime behaviour change, no fix, no GC activation.

1. Reproduce the race on unchanged main runtime with real Cassandra and S3: a
   productive Sync write is in flight while the real Phase 6 selector runs,
   then publishes HEAD, then the real organization-scoped worker executes the
   stale selection. Restore/revert is not needed to show it and is not part of
   this PR.
2. Separate the destruction actually observed from destruction that is only
   possible under further conditions: fs_object rows, the permanent
   `fs:<repo>:<fs_id>` reference, zero-ref block candidates/queue rows, the
   canonical `blocks` row and real S3 bytes are each checked and reported.
3. Keep the ordinary gate green. The real test asserts the safety property
   (HEAD's tree, its permanent reference and its bytes survive) and runs only
   behind an explicit evidence gate, so on main it is a reproducible RED that
   no ordinary suite runs. A P6-2 fix PR, if chosen, removes the gate.
4. Register the issues, keep F3's cross-reference and W2-10's X1 closure
   intact, run the relevant checks, audit and open a focused PR.

Out of scope, by decision: any guard, lock, fence or reclamation; the P6-2
behaviour change (options in §5, decided separately by the maintainer); Phase 5
sharing-aware reclamation; library lifecycle fencing; frontend and the
`auto_delete_days` product contract (registered only, as
`ISSUE-AUTO-DELETE-EXPIRY-CONTRACT-01`); PC-D1B.5. PC-D1B.4 §15 already records
that the certification-window fence does not close the Phase 6 race.

Backward compatible: yes. The only non-test change is an exported single-phase
scanner entry point for integration tests, matching the two existing ones.

## 1. The race

`scanAutoDeleteExpiredObjects` ([scanner.go](../internal/gc/scanner.go)) keeps
the whole HEAD ancestry plus commits newer than `auto_delete_days`, walks their
trees, and enqueues every other fs_object of the library as `ItemFSObject`
with no library guard. It reads the commit list first and the fs_object list
last; nothing ties an enqueued item to the HEAD it was computed against, and
the fs_object's own age is not checked.

`processFSObject` ([worker.go](../internal/gc/worker.go)) with an effective
`LibraryGuardNone` gets a no-op fence. It enqueues directory children, removes
the permanent `fs:` references of the object's blocks, enqueues blocks left
with zero references, and deletes the fs_object row. It does not re-establish
that the selection is still valid.

Schedule exercised by the test:

1. HEAD = H1. The scanner lists the library's commits (T1).
2. A Sync client uploads: `PutCommit` stores commit C2 (root R2, parent H1)
   and `RecvFS` stores R2 and file object F. Neither is in the T1 keep set.
3. The scanner lists fs_objects (T2) and enqueues R2 and F.
4. The client finishes: `PutBlock`, then `PUT commit/HEAD` publishes C2; the
   productive settlement installs the permanent `fs:<repo>:F` reference.
5. The worker executes the stale items: F's permanent reference and the F and
   R2 rows are removed while HEAD = C2 depends on them.

The T1→T2 gap is the scanner's own work (the keep-tree walk is proportional to
the retained history), so it needs no particular client upload order.
`created_at` of a Sync commit is server time, so a commit already listed at T1
protects its tree; one that lands inside the gap does not. The grace period
counts from `QueuedAt` and only delays execution.

## 1b. Directory cascade without the keep set (found in this PR's audit)

The scanner checks the keep set only for the objects it lists. When the worker
processes a selected directory, it enqueues every child that is not already
pending, copying the unguarded mode, and never consults the keep set. A child
that HEAD still reaches is then deleted like any other. No race is needed: the
worker's cascade is the Phase 5 shared-tree pattern (#314) inside Phase 6.

Selected directories that share children with HEAD are ordinary: the root of a
commit outside HEAD's ancestry once it is older than `auto_delete_days`
(an abandoned upload or an off-chain commit), whose unchanged subtrees are
HEAD's, and received directories that no commit references. The test measures
the second form. Once a HEAD file's `up:` pin has lapsed, the permanent `fs:`
is its only reference, so its removal makes the worker itself turn HEAD's block
into a GC candidate and enqueue it.

## 2. Classification

Both are PRE-GC under [X1-CRITICAL-PATH §3](./X1-CRITICAL-PATH.md#3-finding-classification-rule):
current code under the planned activation. Ordinary deployments run with
`GC_ENABLED=false`, so no current-runtime claim is made. The race is the
execute-time half of finding F3, recorded under
`ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`; that issue's X1 closure (W2-10,
E1-17) is unchanged, because both defects are GC destroying content HEAD
already reaches, not a writer reinstalling retired P. After #314, Phase 6 is
the only productive origin of unguarded `ItemFSObject`; the worker's directory
cascade propagates that mode to every child it enqueues.

## 3. Test design

This section describes the P6-1 RED test. P6-2 rebuilt it as the GREEN test
described at the top; the schedules and oracle below are kept as the RED record.

`TestGCPhase6InFlightSyncPublicationKeepsHeadTree`
([test](../internal/integration/gc_phase6_fsobject_toctou_test.go)) runs only
with `SESAMEFS_REQUIRE_GC_PHASE6_TOCTOU_EVIDENCE=1`, which is wired into
TestMain's evidence chain. The standard Compose integration runners deliberately
do not set it while it is RED. It fails closed if Cassandra is unreachable, and
through the shared `e19RequireNoBackgroundGC` check if it is not in the Docker
evidence runner or any API replica (`SESAMEFS_URL`, `_2`, `_3`, defaulting to
node-2/node-3) does not report GC disabled, because such a replica would scan
the same library. A leg that skips before its oracle (for example, S3 or the
storage class unavailable) fails the test instead of passing as evidence.

- **Writer:** productive Sync handlers in-process (`PutCommit`, `RecvFS`,
  `PutBlock`, `PUT commit/HEAD`), the same objects as the E1-7 fresh leg.
- **Policy:** the productive `PUT /api/v2.1/repos/{id}/auto-delete/` route
  writes `auto_delete_days` and its GC projection; teardown resets it to 0.
- **Selector:** the real `scanAutoDeleteExpiredObjects` through the new
  `ScanAutoDeleteExpiredObjectsOnce` entry point. A store wrapper narrows the
  library listing to the owned library and runs the writer once at the start
  of the fs_object listing. It answers nothing on its own.
- **Worker:** the real `ProcessOrgOnce`. The fixture library lives in a shared
  org, so dequeue returns only that library's fs_object rows; block rows are
  reported, not executed. The run must drain that selection, or it fails.
  Owned rows are found through `DequeueBatch` on the shared org, as in the E1
  helpers; its 100000-row bound would hide rows only in a far busier org.
- **Legs:**

  | Leg | Schedule | On main |
  |---|---|---|
  | `in-flight-gap` | writer inside the selector's gap, HEAD after the scan | RED (race) |
  | `listed-before-scan` | the same writer before the scan, HEAD after it | PASS (control) |
  | `orphan-shares-head-file` | HEAD published before the scan; a received orphan directory has HEAD's file as its child | RED (cascade) |
  | `orphan-shares-aged-head-file` | the same after an explicit lapse of the Sync `up:` pin (its 48h TTL), as E1-7 models it | RED (cascade, block candidate) |

- **Oracle:** the selection must be exactly the expected unguarded rows, with
  HEAD's tree kept. After the worker runs, HEAD's root and file fs_objects,
  the permanent reference, the absence of a block candidate or queue row, the
  canonical row and the real S3 bytes are each reported. The orphan directory
  is the positive control: the worker must delete it, or the oracle would be
  vacuous.

The positive control describes main. Whichever P6-2 option is chosen changes
what happens to that orphan, so the fix PR must replace the control with one
that fits the chosen contract when it removes the gate.

## 4. Evidence (2026-10-19)

Runner: the persistent Go 1.25.14 container on the local compose network, with
a clean `git archive` of main@bb89d7b12 plus the three changed Go files (LF).
Target: the isolated `sesamefs-e19` backend (keyspace `sesamefs_e19`, one DC,
`GC_ENABLED=false`, image built 2026-10-19T15:37Z), which is used only for
library creation, the policy route and the GC status check. All Sync, scanner
and worker code ran from the branch source. The primary development backend
(GC enabled) was not used.

| Run | Result |
|---|---|
| Final source, 3 runs (2 normal, 1 `-race`) | Identical in each. `in-flight-gap`: root fs_object deleted, file fs_object deleted, permanent `fs:<repo>:<file>` removed; only `up:sync` remains, no candidate, canonical and S3 bytes intact. `listed-before-scan`: PASS. `orphan-shares-head-file`: file fs_object deleted and permanent `fs:` removed; root intact; only `up:sync` remains, no candidate. `orphan-shares-aged-head-file`: file fs_object deleted, permanent `fs:` removed, zero references left, HEAD's block in `gc_block_candidates` and queued (candidates=1, queued=1); canonical row and bytes still intact because the block item was not executed. Exit 1; no data race, no cleanup error |
| Earlier iterations, 12 runs (9 normal, 3 `-race`) | the same race violations and a passing control in all 12; the same cascade violations in the 4 that had the cascade legs. The first had only the gap leg; four predate the `errors.Is` change and the drain check; three predate the replica check and the cascade legs; four predate the shared isolation helper and the leg-reached guard |
| Harness fail-closed check | with `SESAMEFS_TEST_IN_CONTAINER` unset and the gate set, the test fails before any fixture is created |
| Evidence-gate wiring and global-fanout guards | PASS in each run above |
| Same test without the gate | SKIP, exit 0 |
| `go test -short ./internal/gc`, normal and `-race` | PASS, exit 0 |
| `go vet` (tagged and untagged) on `internal/gc`, `internal/integration`; `gofmt -l` on changed Go files | clean |
| Residue | no `auto_delete` policy row left in `sesamefs_e19`; no candidate, `blocks` or `block_references` row for the seven aged-leg blocks; the test asserts the owned queue is empty after teardown |

```sh
for v in ${!SESAMEFS_REQUIRE_@}; do unset "$v"; done
export SESAMEFS_REQUIRE_GC_PHASE6_TOCTOU_EVIDENCE=1 CASSANDRA_KEYSPACE=sesamefs_e19
export SESAMEFS_URL=http://sesamefs-e19:8080 SESAMEFS_URL_2=http://sesamefs-e19:8080 SESAMEFS_URL_3=http://sesamefs-e19:8080
export GC_ENABLED=false CONFIG_PATH="$(pwd)/configs/config.docker.yaml"
go test -tags integration -v -count=1 -timeout 10m \
  -run '^(TestGCPhase6InFlightSyncPublicationKeepsHeadTree|TestEveryEvidenceGateIsWiredIntoTestMain|TestNoGlobalGCFanoutInIntegrationSuite)$' \
  ./internal/integration
```

Not run: full integration/API/OIDC suites, three-DC, crash/process-death, the
restore/revert entry, the off-chain-commit form of the cascade, execution of
the queued block item (so S3 deletion is not measured), a natural `up:`
expiry, and clients. Logs are local artifacts, not independent CI; see the PR
for the manifest hash.

## 5. P6-2 options (decided 2026-10-19: A)

The maintainer chose A; it is implemented in [P6-2](#p6-2-phase-6-suspended-2026-10-19).
B and C were rejected for the reasons in the table.

| Option | Behaviour | Cost |
|---|---|---|
| A. Suspend Phase 6 | the scanner stops producing; the worker completes every unguarded `ItemFSObject` (effective guard) without mutation, before it enqueues children or touches references. That covers the race, the cascade and any child already queued | Phase 6 reclaims nothing until P6-3; this test becomes a mandatory GREEN |
| B. Age criterion | enqueue only old fs_objects | does nothing for the cascade, and for the race it leaves content-addressed reuse and restore; a bound that only makes the failure rare does not close it (§4 of the critical path) |
| C. No runtime change | both issues stay A1 blockers until P6-3; rely on GC OFF | no code; the risk remains on development stacks with GC on and auto-delete set |

Recommended: A. Known effects of A: the Phase 6 scanner unit tests and the
unguarded `ItemFSObject` cases in `worker_test.go` (`EnqueueLibraryContents`)
and `gc_integration_test.go` (the pending-row scope regression) must be
adapted; `auto_delete_days` still reads and writes, but the PR must not claim
the feature is unchanged.

P6-3 designs a single live-library metadata reclamation architecture that also
covers failed publications and Phase 5 residue, with separate consumers and
explicit retention contracts. Its destroyer must respect sharing for every
object it reaches, not only those the selector listed. It must fix the
`auto_delete_days` contract first (`ISSUE-AUTO-DELETE-EXPIRY-CONTRACT-01`).

## 6. Limits

This PR proves loss of HEAD metadata and of its permanent reference, and that
the cascade turns a HEAD block into a queued GC candidate, on one DC with one
plaintext block and one root file. It does not execute the block item or show
S3 deletion, and it certifies no schedule other than the ones measured. X1's
accepted scope is unchanged. PRE-GC/A1 remain OPEN; ordinary GC stays OFF in
every DC.

## P6-2 evidence (2026-10-19)

The standard suite ran in a private Compose project (`-p sesamefs-p62`): fresh
volumes, no published ports, every image built from a clean `git archive` of
the P6-2 commit `406633d0e`, and a cold boot with `sesamefs` first. The existing
development stack was not used, because its keyspaces predate the v1 baseline
fold (#316) and would need a destructive `down -v`. Directed runs used a Go
1.25.14 runner on the local compose network against the isolated, GC-OFF
`sesamefs-e19`, through the same isolated re-execution the standard runners use.

| Run | Result |
|---|---|
| `go-all-test` (`go test ./... -short -cover`, the required integration run with every standard evidence gate plus the Phase 6 gate, `test.sh api`, `test.sh oidc`) | exit 0. Short Go: 23 packages ok, 0 FAIL. Integration: `ok` in 955.7s; `TestGCPhase6InFlightSyncPublicationKeepsHeadTree` PASS in all four legs through the e19 re-execution. API: 20/20 suites. OIDC: 1/1 suite. 0 top-level FAIL. The 31 nested `--- FAIL` lines are the E1-01 harness controls that inject a failure into a child process (`E101_INJECTED_FAILURE`); every parent passes. 77 conditional SKIP lines are not positive evidence |
| Phase 6 real-stack test, directed, 3 runs (2 normal, 1 `-race`) | PASS, all four legs; no data race; owned queue empty after teardown |
| The same test on main's GC runtime (main `worker.go` and `scanner.go`, branch test) | FAIL in all four legs: the selector enqueues 3 rows; the stale race rows delete HEAD's root, file and the orphan and remove the permanent `fs:`; both cascade legs delete HEAD's file and its `fs:`; the lapsed-pin leg also queues HEAD's block as a candidate. The worker suspends 0 rows |
| `go test -short ./internal/gc ./internal/metrics`, normal and `-race` | PASS |
| Mutations | removing the worker barrier fails both suspension unit tests; a barrier on the raw field only (ignoring the legacy flag) fails the legacy-flag test; main's selector fails the scanner suspension test; dropping the phase-run timestamp fails it too |
| `go vet ./...`, `go vet -tags integration ./internal/integration`, `gofmt -l` on changed Go files, `git diff --check` | clean |

After `go-all-test`, the two review rounds changed the runtime only in comments,
the counter's help string, and an `Add(0)` that keeps
`gc_items_enqueued_total{phase="auto_delete"}` present. Tests changed only in
the scanner gauge reset, an exact-row seed helper and comments. On that final
source the GC/metrics unit tests (normal and `-race`), the gauge mutation and
the directed Phase 6 real-stack test (normal and `-race`, four legs, no data
race) were rerun and pass. `go-all-test` was not repeated.

Not run: three-DC, crash/process-death, sync clients (`sync-test`), frontend
suites. The fs_object mechanics under a guard are covered by unit tests and the
pending-item scope regression only, not by a new real-stack leg.
