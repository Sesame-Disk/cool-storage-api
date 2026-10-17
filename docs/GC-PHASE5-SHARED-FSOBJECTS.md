# Phase 5: preserve shared fs_objects

## Bounded plan (2026-10-17)

Base main@1b01e5d77 (#313). Scope: ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01.

1. Invert the existing unsafe characterization into a preservation regression.
   Add real Cassandra/S3 evidence using owned random org/library fixtures and
   ProcessOrgOnce, exercising the real Phase 5 scanner selection and worker.
   Record RED before changing runtime; no shared/global worker activation.
2. Make unguarded commit retirement metadata-only. A commit root is not owned by
   that commit: HEAD and retained history may share every node. Preserve guarded
   library-deletion cascades and their existing fail-closed tests. No schema,
   queue format, API or GC-default change; existing queued unguarded commits get
   the safe behavior too.
3. Prove HEAD and retained history trees, permanent refs, canonical metadata and
   actual S3 bytes survive; the expired commit is removed, with no descendant
   queue work. Cover root identity and shared subtrees plus guarded cleanup.
4. Run relevant Docker normal/race/unit and isolated real-stack checks, audit
   diff/compatibility/cleanup and update current issue/gate/history tracking.
   Refresh main and open a focused PR; report failures, skips and unrun checks.

Trade-off: unguarded expired commits no longer reclaim their tree by cascade.
Exclusive fs_objects and references may be retained until a sharing-aware pass;
Phase 6's independent execute-time TOCTOU remains OPEN, not certified here.
Concurrent commit publication/metadata destruction and hard-delete fencing stay
under their existing PRE-GC obligations. No universal retention/concurrency bound
is claimed. PRE-GC/A1 OPEN; ordinary GC OFF in every DC.

Backward compatible: yes for API/schema/queue contracts. Unsafe deletion of shared
data is corrected; reduced reclamation is explicit and needs disposition before A1.

## Result and contract

The original cascade is reproduced on unchanged main runtime before the fix.
`processCommit` now enqueues a root only when the existing effective library guard
is non-empty. Unguarded Phase 5 items delete only their commit row. The guarded
library path, its checks/retries and legacy deleted-check fallback remain intact.
Only worker.go changes productive behavior; there is no scanner/schema/queue/API
or configuration change.

The real test uses the production Phase 5 selector (real library-policy discovery,
filtered read-only to one owned library) and ProcessOrgOnce. Two schedules cover
shared subtrees and an identical root; retained commits include HEAD, its old
parent and a recent off-chain commit. Commits/fs_objects are created through the
current identity authorization/materialization gateways, not claim-less mock rows.
The oracle checks retained tree metadata, two permanent refs, canonical P and a
real S3 read; this is not HTTP/Sync/client certification. A positive assertion
requires the expired commit to disappear and no descendant queue work to remain.

The old-runtime real test stops after root/directory processing, before queued
file/block destruction. Thus RED demonstrates actual shared-directory loss, not
an injected error or a claim that S3 bytes were deleted. GREEN checks tree/ref/
object preservation and owned cleanup. Cleanup checks Cassandra source partitions,
queue emptiness and S3 absence, and removes only exact fixture-owned identities,
policy/GC projections and object keys.

## Docker evidence (2026-10-17)

All runs use a clean versioned-source archive, with the task files copied in;
ignored historical Go diagnostics are not part of the build. Go 1.25.14; real
fixtures run in the existing one-DC sesamefs_e19 keyspace and org-scoped S3,
with random org/library IDs. No background scanner/worker is enabled by this PR.
The e19 backend reports GC_ENABLED=false. The separate local development primary
was already GC_ENABLED=true; it was not changed, and this is not evidence that
the entire local stack is OFF or authorized for ordinary deployment.

| Run | Result |
|---|---|
| Inverted mock regression, old runtime | RED: shared/expired tree nodes disappear; package exit 1 |
| Real-stack regression, old runtime | RED: both shared-subtree and identical-root schedules lose retained directories; exit 1; no cleanup error |
| Normal GC / DB / untagged integration source guards | 798 / 528 / 98 PASS events; DB 9 seed SKIP; packages exit 0. This precedes the three added guarded-cascade subtests |
| Final GC / DB race | 801 / 528 PASS events, DB 9 seed SKIP; packages exit 0; no race report |
| Real-stack GREEN, normal and race | Both schedules PASS in each run (2 subtests plus parent), no SKIP/race; exit 0 |
| Final targeted commit controls and real normal rerun | PASS, exit 0; guarded, legacy, metadata-only and cleanup checks |
| go vet -tags integration ./internal/gc; gofmt; added local links; diff whitespace | PASS |

Inside the Docker source runner (CONFIG_PATH, Cassandra auth/keyspace and storage
class settings must point at the isolated test stack; ordinary GC stays false):

```sh
go test -json -short -count=1 ./internal/gc ./internal/db ./internal/integration
go test -race -json -short -count=1 ./internal/gc ./internal/db
SESAMEFS_PHASE5_REAL_STACK=1 go test -json -tags integration -count=1 -timeout 4m -run '^TestPhase5SharedTreesRealStack$' ./internal/gc
SESAMEFS_PHASE5_REAL_STACK=1 go test -race -json -tags integration -count=1 -timeout 4m -run '^TestPhase5SharedTreesRealStack$' ./internal/gc
```

Initial runner/fixture failures were excluded, not counted as RED evidence:
missing production HMAC config, OnlyOffice JWT config and Cassandra auth; then
claim-less fixture metadata was correctly refused with `metadata identity
authority conflict`. The accepted RED uses valid production identity gateways.
The mock fixture's pre-existing synthetic reference count is captured before
processing; the regression tests preservation rather than an invented count.

The nine short-mode DB seed fixtures were not exercised against Cassandra
(they are the seed tests recorded in E1-FINAL-DISPOSITION). Full application
integration, API/OIDC, three-DC, native fault/crash matrices and clients were not
rerun. The real tests here cover Phase 5 only; no broader safety/retention claim.
Accepted JSON events, exits, source copies and runner adaptations are retained
locally in tmp/phase5-evidence-20261017/. Its SHA256SUMS lists 32 files; the manifest
SHA-256 is f0fd387c32858ddf076fdfeefc61ac39c8f754068480bbb6a72aa18b93b5f407.
Initial setup failures remain in tmp/phase5-real-red*. These are local artifacts,
not independent CI.

## Limits and rollback

Exclusive trees/refs can remain indefinitely when no sharing-aware cleanup runs;
this intentionally trades reclamation for safety. This does not certify Phase 6
as safe or establish a retention bound. Previously queued fs_object children are
not retroactively classified; already deleted data are not repaired. Keep ordinary
GC OFF across rollout, inspect any pre-existing destructive backlog before A1,
and resolve the complete PRE-GC inventory, not just this defect. A rollback needs
no schema migration but restores the unsafe cascade, so GC must remain OFF.

X1's accepted physical-life scope is unchanged. PRE-GC/A1 remain OPEN. Concurrent
commit publication/destruction, Phase 6's execute-time TOCTOU and hard-delete
fencing are separate obligations; this PR does not claim to close them.
