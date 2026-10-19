# Phase 6: execute-time fs_object TOCTOU

## Bounded plan, P6-1 (2026-10-19)

Base main@bb89d7b12 (#317). Scope: `ISSUE-GC-PHASE6-FSOBJECT-EXECUTION-TOCTOU-01`
(P1 / PRE-GC; provisional when planned, confirmed by the RED in §4). Evidence only: no runtime behaviour
change, no fix, no GC activation.

1. Reproduce the race on unchanged main runtime with real Cassandra and S3: a
   productive Sync write is in flight while the real Phase 6 selector runs,
   then publishes HEAD, then the real organization-scoped worker executes the
   stale selection. The RED is the scenario in §1 below; restore/revert is not
   needed to show it and is not part of this PR.
2. Separate the destruction actually observed from destruction that is only
   possible under further conditions: fs_object rows, the permanent
   `fs:<repo>:<fs_id>` reference, zero-ref block candidates/queue rows, the
   canonical `blocks` row and real S3 bytes are each checked and reported.
3. Keep the ordinary gate green. The real test asserts the safety property
   (HEAD's tree, its permanent reference and its bytes survive) and runs only
   behind an explicit evidence gate, so on main it is a reproducible RED that
   no ordinary suite runs. A P6-2 fix PR, if chosen, removes the gate.
4. Register the issue, keep F3's cross-reference and W2-10's X1 closure
   intact, run the relevant checks, audit and open a focused PR.

Out of scope, by decision: any guard, lock, fence or reclamation; the P6-2
behaviour change (options in §5, decided separately by the maintainer); Phase 5
sharing-aware reclamation; library lifecycle fencing; frontend and the
`auto_delete_days` product contract (registered only, as
`ISSUE-AUTO-DELETE-EXPIRY-CONTRACT-01`); PC-D1B.5. PC-D1B.4 §15 already records that
the certification-window fence does not close this race.

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

## 2. Classification

PRE-GC under [X1-CRITICAL-PATH §3](./X1-CRITICAL-PATH.md#3-finding-classification-rule):
current code under the planned activation. Ordinary deployments run with
`GC_ENABLED=false`, so no current-runtime claim is made. It is the execute-time
half of finding F3, recorded under `ISSUE-PC0-CONTENT-RESURRECTION-PUBLICATION-01`;
that issue's X1 closure (W2-10, E1-17) is unchanged, because the race is GC
destroying content HEAD already reaches, not a writer reinstalling retired P.
After #314, Phase 6 is the only productive producer of unguarded `ItemFSObject`.

## 3. Test design

`TestGCPhase6InFlightSyncPublicationKeepsHeadTree`
([test](../internal/integration/gc_phase6_fsobject_toctou_test.go)) runs only
with `SESAMEFS_REQUIRE_GC_PHASE6_TOCTOU_EVIDENCE=1`, which is wired into
TestMain's evidence chain. The standard Compose integration runners deliberately
do not set it while it is RED. It fails closed if Cassandra is unreachable or
the target backend reports GC enabled, because that backend would scan the same
library.

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
- **Legs:** `in-flight-gap` (writer inside the gap, the RED) and
  `listed-before-scan` (the same writer before the scan, the control).
- **Oracle:** the selection must be exactly the expected unguarded rows, with
  HEAD's previous tree kept. After the worker runs, HEAD's root and file
  fs_objects, the permanent reference, the absence of a block candidate or
  queue row, the canonical row and the real S3 bytes are each reported. A
  received-but-uncommitted orphan directory is the positive control: the
  worker must delete it, or the oracle would be vacuous.

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
| RED on the final source, 3 runs (2 normal, 1 `-race`) | `in-flight-gap` FAIL in each, with exactly three violations: root fs_object deleted, file fs_object deleted, permanent `fs:<repo>:<file>` removed. Only `up:sync:<repo>:<block>` remains; candidates=0, block queue rows=0, canonical and S3 bytes intact. `listed-before-scan` PASS in each. Exit 1; no data race, no cleanup error |
| Earlier iterations, 5 runs (4 normal, 1 `-race`) | the same three violations in every run. The first run had only the gap leg; the next four predate the `errors.Is` change and the drain check |
| Evidence-gate wiring and global-fanout guards | PASS in each run above |
| Same test without the gate | SKIP, exit 0 |
| `go test -short ./internal/gc`, normal and `-race` | PASS, exit 0 |
| `go vet` (tagged and untagged) on `internal/gc`, `internal/integration`; `gofmt -l` on changed Go files | clean |
| Residue | no `auto_delete` policy row left in `sesamefs_e19`; the test asserts the owned queue is empty after teardown |

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
restore/revert entry, a natural `up:` expiry to test for physical deletion, and
clients. The 19 log/exit files are local artifacts, not independent CI; their
SHA256SUMS manifest hashes to
64dc95797604954d64498464bae814d2770e0e6fcc9bded08c165553eb3b4459.

## 5. P6-2 options (maintainer decision pending)

| Option | Behaviour | Cost |
|---|---|---|
| A. Suspend Phase 6 | the scanner stops producing; the worker completes unguarded `ItemFSObject` (effective guard) without mutation, before the first destructive step | Phase 6 reclaims nothing until P6-3; this test becomes a mandatory GREEN |
| B. Age criterion | enqueue only old fs_objects | narrows the gap but leaves content-addressed reuse and restore; a bound that only makes the failure rare does not close it (§4 of the critical path) |
| C. No runtime change | the issue stays an A1 blocker until P6-3; rely on GC OFF | no code; the risk remains on development stacks with GC on and auto-delete set |

Recommended: A. Known effects of A: the Phase 6 scanner unit tests and the
unguarded `ItemFSObject` cases in `worker_test.go` (`EnqueueLibraryContents`)
and `gc_integration_test.go` (the pending-row scope regression) must be
adapted; `auto_delete_days` still reads and writes, but the PR must not claim
the feature is unchanged.

P6-3 designs a single live-library metadata reclamation architecture that also
covers failed publications and Phase 5 residue, with separate consumers and
explicit retention contracts. It must fix the `auto_delete_days` contract first
(`ISSUE-AUTO-DELETE-EXPIRY-CONTRACT-01`).

## 6. Limits

This PR proves loss of metadata and the permanent reference on one DC with one
plaintext block and one root file. It does not show S3 deletion, and it does not
certify a schedule other than the one measured. X1's accepted scope is
unchanged. PRE-GC/A1 remain OPEN; ordinary GC stays OFF in every DC.
