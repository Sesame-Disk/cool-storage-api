# E1-16 — post-D contract alignment with G4

Base: main@5c3163871803 (#261 merged). Documentation only: no runtime,
schema, CQL, test, guard, mutation-suite or configuration change.
`GC_ENABLED=false` remains mandatory. X1 remains OPEN.

## Decision (approved 2026-10-10)

The project owner approved E1-15D §1 as an **alignment with the G4 contract
already decided on 2026-10-02 and implemented in #247**, not as a new
architecture:

> **X1 physical invariant.** After exact COMMITTED D(P1), no current-version
> operation may reinstall P1 or make it canonical again, revoke or postpone
> D1, or publish HEAD/commit/tree content whose resolution depends on retired
> P1. A possibly accepted publication must keep D-blocking liveness until its
> permanent `fs:` exists (R31 safety).
>
> **Logical references are not physical dependence.** `up:`/`pub:`/`fs:`
> rows written or renewed for L after D1 are liveness of L. They cannot
> affect P1 (G4) and protect only the current or a later life. Their
> unbounded retention is convergence work, tracked as PRE-GC or follow-up.

The E1 ledger sentence "no legitimate current-version operation may durably
add a P1 reference" is replaced by the statement above. Nothing in this
document lowers a severity; every moved scope still blocks A1.

## Why this is not a new decision

The strict reading came from one ledger sentence. Every architectural source
already treated references as logical:

| Source | Statement |
|---|---|
| D0 §3 (#201) | `block_references` answer "is there a logical operation that needs content L?"; they are "not physical GC authority and not physical generations" |
| D0 §12 | COMMITTED recovery must not ask "did new refs of L appear?" to revoke D1 |
| D0 §13 | post-D refs stay a veto only as `CURRENT / TRANSITIONAL`, "until the writer PRs are merged" |
| D0 §25 G4 | "late `refs(L)` no longer cancel or postpone D1" |
| [G4 contract](./GC-G4-IMPLEMENTATION-PLAN.md) 3–5 | a writer mints K2 for P2; "absence never authorizes repairing or reinstalling P1"; "References of L after D1 do not authorize, cancel or postpone deleting P1" |
| [E1 frozen matrix](./E1-FULL-GC-PUBLICATION-RECOVERY.md) | "Missing refs, leaked bytes or retained repair alone do not falsify X1"; E1-12 RED = "unreachable loser is promoted as P1 after D"; E1-13 "slowness/retention alone is not RED"; E1-15 "ownerless pub over-retention … remains a follow-up" |

Code at the base, read for this record:

- [internal/gc/worker.go:2656](../internal/gc/worker.go#L2656): the COMMITTED
  path retires only P1 "Never consult[ing] L's new references as authority
  over the retired P1".
- [internal/db/publication_liveness.go:42-56](../internal/db/publication_liveness.go#L42-L56):
  a pending repair "vetoes a NEW D, but is neither a real reference nor a
  contradiction against an already committed D", and the liveness read "must
  not be used as a post-COMMITTED contradiction read".

Measured: G4 `TestG4RecoveryDeletesOnlyRetiredLifeWithReplacement` and
`TestG4CassandraMinIOPhysicalLifeCoexistence`; E1-15C completes TERMINAL
with post-D references and a dead repair present, and publishes a
rematerialized P2 legitimately; E1-15D shows Sync rejects promotion of an
abandoned commit after TERMINAL.

## Mechanisms verified at the base

**Shared post-HEAD guard (R31 safety).** It is funnel-independent:

- `published_block_reference_repairs` has `default_time_to_live = 0`
  ([001_initial_schema.cql:769-779](../internal/db/migrations/001_initial_schema.cql#L769-L779)).
  Rows are deleted only by pre-HEAD rollback, the clear path and settlement
  (`publish_repair.go` call sites of `deletePublishedBlockReferenceRepairFn`).
- `blockHasPendingPublicationGlobal`
  ([publication_liveness.go:16](../internal/db/publication_liveness.go#L16))
  reads every one of the 32 buckets for the organization at EACH_QUORUM and
  matches `staged_block_ids`. It ignores the repair's classification and its
  writer. `publicationLivenessBeforeDestruction` re-reads real references after
  a negative scan, because promotion writes `fs:` before settlement.
- Writers that queue a repair before HEAD: v2 `CreateFileFromBlocks`/
  `UploadFile`/`CreateFile` (`files.go`), cross-repo copy/move
  (`batch_operations.go`), OnlyOffice (`onlyoffice.go`), SeafHTTP
  (`seafhttp.go`) and Sync (`sync.go`). The W2-10 content-resurrection
  handlers queue none.

**Sync HEAD publication.** Both `handleSyncHeadPromotion` and
`tryAutoMergeSyncHeadPromotion` run `prepareSyncCommitBlockPublicationReadiness`
(exact placement of every added block), queue the durable repair, re-validate
the captured placements, then CAS HEAD
([sync.go:4977-5007](../internal/api/sync.go#L4977-L5007), 5224-5232,
5474-5508). "Provenance scopes only upload-pin renewal, never publication
authority." Expired `up:sync` provenance is observationally identical to
absent provenance (`ISSUE-SYNC-PUTBLOCK-EXPIRED-PROVENANCE-01`). The
already-reachable repair path (`repairPublishedSyncCommitBlockDelta`) only
re-queues and renews; it publishes nothing new. `RecvFS`
([sync.go:3183](../internal/api/sync.go#L3183)) stores fs objects only: no
HEAD and no block reference.

**Cross-repo copy/move.** `copyFSObjectToLibraryForPublish` recurses into
directories and returns every nested file
([fs_helpers.go:1836-1837](../internal/api/v2/fs_helpers.go#L1836-L1837)).
`captureCopiedBlockPlacements` captures every block of every file, and
`validateCopiedBlockPublication` requires the identical set after repair
([cross_repo_publication.go](../internal/api/v2/cross_repo_publication.go)).
A copy across different block representations is rejected before copying
([batch_operations.go:611](../internal/api/v2/batch_operations.go#L611)).

## W2 row dispositions

States follow X1 §4. "Shared guard" means the R31 safety mechanism above,
with W2-11..14 evidence.

| Row | Before | After | Basis |
|---|---|---|---|
| W2-1 | OPEN through R31 | **CLOSED** — pre-HEAD CLOSED-FIX (#204/#205); post-HEAD CLOSED-EVIDENCE (shared guard) | R31 residual is convergence |
| W2-2 | OPEN through R31 | **CLOSED** — pre-HEAD CLOSED-FIX (W1 #202); post-HEAD CLOSED-EVIDENCE (shared guard) | same |
| W2-3 | OPEN | **CLOSED-EVIDENCE** | Sync validates every added block regardless of provenance or pod; W2-4 RED→GREEN legs (direct/auto-merge, D before stage/repair, fully retired P); #206/#210 provenanced subset incl. 3-DC; E1-PUT-01 |
| W2-4 | CLOSED-FIX pre-HEAD; post-HEAD OPEN | **CLOSED** — CLOSED-FIX pre-HEAD; post-HEAD CLOSED-EVIDENCE | shared guard |
| W2-5 | OPEN (ordered subset only) | **CLOSED-EVIDENCE** | RecvFS publishes nothing; every order reaches HEAD only through the Sync gate above (direct and auto-merge); E1-7 measured fresh/reuse/COMMITTED/TERMINAL |
| W2-6 | CLOSED-FIX pre-HEAD | **CLOSED** — adds post-HEAD CLOSED-EVIDENCE | shared guard |
| W2-6a | OPEN through R31 | **CLOSED** — pre-HEAD CLOSED-FIX; post-HEAD CLOSED-EVIDENCE | shared guard |
| W2-7 | CLOSED-FIX measured; post-HEAD OPEN | **CLOSED** | the final exact-P read uses canonical state, not the `up:` row, so natural and controlled expiry take the same path; shared guard |
| W2-8 | CLOSED-FIX measured; post-HEAD OPEN | **CLOSED** | shared guard; post-HEAD cleanup is convergence |
| W2-9 | CLOSED-FIX measured subset; broader OPEN | **CLOSED** — CLOSED-FIX (E1-09) + CLOSED-EVIDENCE for directories/multi-block | same capture/validate code; cross-representation unreachable; cross-DC through the shared fence and guard (P3/X2/RepairGuard 3-DC) |
| W2-10 | OPEN | **OPEN (unchanged)** | E1-10e RED class: RevertFile, RevertDirectory, RestoreTrashItem and directory/batch RevertDirents queue no repair and have no equivalent source-admission guard |
| W2-11 | CLOSED-EVIDENCE | unchanged | — |
| W2-12 | OPEN overall | **CLOSED-EVIDENCE (X1)**; loser-authority/convergence → PRE-GC | only REACHABLE promotes `fs:`. SUPERSEDED removes only the repair's own `pub:` and row, for a commit that HEAD has passed without containing (monotonic HEAD; E1-15E/F 3-DC). UNKNOWN and DefinitelyNotReachable retain (`settlePublishedBlockReferenceRepair`). The guard vetoes D (E1-12) |
| W2-13 | OPEN overall | **CLOSED-EVIDENCE (X1)**; discovery/scale/convergence → PRE-GC | the guard reads the repair table directly, independent of discovery (E1-13) |
| W2-14 | OPEN overall | **CLOSED-EVIDENCE (X1)**; transition/convergence → PRE-GC | `pub:` expiry creates no candidate (E1-14) |

A repair that is REACHABLE after D(P1) is not a violation by itself. A
legitimate publication on a later life P2 has its own repair after D1: E1-15C
C1 rematerializes P2, advances HEAD, and keeps the published file only on P2.
What X1 excludes is narrower. A reachable repair whose publication still
depends on retired P1 cannot exist without a prior violation: its commit
reached HEAD only through a pre-HEAD exact-P gate on P1, and its non-expiring
row vetoes D(P1) until promotion writes `fs:`. This also disposes the
2026-10-02 G4 cross-audit dependency (X1 §7): E1-2 measured that exact
schedule (reachable HEAD → temporary references expire → the durable repair
vetoes D). A later promotion writes a logical `fs:` on L.

## Scope moves (severity unchanged)

| Issue | Before | After |
|---|---|---|
| `ISSUE-PUBLISH-REPAIR-STALE-RENEWAL-AFTER-CLEAR-01` (E1-15A) | P1 PRE-X1 | P1 PRE-GC |
| `ISSUE-PUBLICATION-POST-D-LIVENESS-BEFORE-VALIDATION-01` (E1-15B) | P1 PRE-X1 | P1 PRE-GC |
| `ISSUE-PUBLISH-REPAIR-KNOWN-LOSER-DURABILITY-01` (W2-12) | P2, blocked X1 via W2-12 | P2 PRE-GC |
| `ISSUE-PUBLISH-REPAIR-DISCOVERY-SCALE-01` (W2-13) | P2, blocked X1 via W2-13 | P2 PRE-GC |
| `ISSUE-GC-PUB-REF-ZERO-REF-01` (W2-14) | P2, blocked X1 via W2-14 | P2 PRE-GC |
| `ISSUE-PUBLISH-REPAIR-OWNED-PUB-CLEANUP-RACE-01` | P2 PRE-X1 | P2 PRE-GC |
| `ISSUE-PUBLISH-REPAIR-PROGRESS-PAXOS-DOMAIN-01` | P2 PRE-X1 | P2 PRE-GC |

`ISSUE-PUBLISH-REPAIR-DEAD-ROW-RETENTION-01` was left unchanged here. That
separate decision was taken on 2026-10-12: P2 PRE-GC, with a measure-first
plan recorded in the issue. E1-15D §2 stays the authority record, except that its row
"HEAD ≠ c1.parent" is superseded by E1-15E SUPERSEDED settlement.

## What still blocks `X1 CLOSED`

1. **W2-10 residual (E1-17).** Demonstrate first on the real handlers; fix
   only a reproduced RED.
2. **E1-01 crash/restart.** First inventory the existing G1–G5 crash evidence
   (G2 crash matrix, G3 retirement, G4 restart and terminal replay, G5 restart
   and 3-DC) against the E1-01 RED condition, then run only uncovered points.
3. **Full three-DC suite.** Re-measured on 2026-10-10 (see Validation): 28
   of the 30 G5 failures pass and API is 20/20. Open residuals:
   `ISSUE-SEAFHTTP-UPLOAD-FINALIZE-LEASE-ERROR-500-01`, one unconfirmed
   Seafile sync timeout, and E1 in-process-GC legs that cannot run on three
   DCs until the harness declares the fixture topology.
4. **D0 §25 E1 reclassification list.** H (E1-PUT-01: K1 bytes return without
   metadata, P1 not reinstalled), R18, R27, R28, E6, 020 and R30/R31 (R31
   X1 safety is covered by the rows above).
5. **Remaining PRE-X1 tag.** "PRE-X1 audit debt: identity claim-table CQL
   inventory" (P2) needs an E1-final disposition.

Only the final E1 audit may declare `X1 CLOSED`. A1 follows PRE-GC.

## Validation

### Full three-DC suite (2026-10-10)

This re-measures the 2026-10-02 G5 FAIL (30 integration tests; API 8/20).

**Setup.** Source: main@5c3163871803 plus this branch's documentation edits;
runtime code is identical to main. Fixture: fresh isolated Cassandra 5.0.9,
NetworkTopologyStrategy, RF1 in dc-na/dc-eu/dc-asia, SILO storage. Three
current-source daemons ran (GC on the first), plus an isolated GC-OFF
`sesamefs_e19` daemon for the E1 children. The keyspace was created with the
same three-DC replication, and every current `go-all-test` evidence gate was
required. The driver derives from the G5 driver.

**Results:**

| Phase | Result |
|---|---|
| Unit (all packages, includes the D0 doc guard) | PASS |
| API | 20/20 suites PASS |
| OIDC | PASS |
| Seafile Docker sync | 10/11 PASS |
| Integration | 10 top-level FAIL (986.6s) |

**Against the G5 list.** Of the 30 tests that failed on 2026-10-02, 28 now
PASS, including the Sync HEAD, P3, W2, web block upload, identity-authority
and X1 non-overlap characterization tests. `TestBlockMappingAuthority3DC`
SKIPs because it needs its own script. Only
`TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles` still fails.

**Classification of the 10 integration failures.** No failure is an X1/W2
safety violation:

- **E1-3, E1-7, E1-09, E1-10e, E1-11, E1-12, E1-13: fixture topology.** Every
  block logs "destructive topology gate rejected the G2 handoff". The
  in-process GC connects through `openIntegrationProjectionDB`, which declares
  only the local DC, so the gate correctly refuses to destroy on a three-DC
  keyspace. The failing assertions are fixture progress only ("GC retirement
  n=0", "worker did not visit owned real candidate", "timed out waiting for
  … refs=0"). The legs that do not need GC produced their normal evidence.
  These E1 legs are single-DC evidence by design. Running them on three DCs
  would need the harness to declare the fixture topology; this branch does not
  change the harness.
- **`TestGC_WorkerRetiresCanonicalRowAndPreservesPlainEncryptedSiblings`**
  (a queue row still visible after retirement) and
  **`TestConcurrentTwoRegionV2UploadsNoLostCommits`** (500 "failed to update
  library"): intermittent. Both PASS 3/3 when re-run in isolation on the same
  fixture.
- **`TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles`:** intermittent
  (1 of 3 isolated re-runs FAIL) and pre-existing. The cause is not
  identified because the finalize-permit error is unlogged; it is registered
  as `ISSUE-SEAFHTTP-UPLOAD-FINALIZE-LEASE-ERROR-500-01` (P2,
  CURRENT-RUNTIME availability).

**Seafile sync.** "Encrypted: Binary file sync" timed out ("File not synced
after 5 attempts", 4 KB file) with no integrity error. The other encrypted
cases, including hash verification, passed. It was not re-run because the
fixture had already stopped, so whether it is flaky is not established.

Exit item 3 is therefore re-measured, not closed. Its open residuals are the
finalize-lease 500, the unconfirmed sync timeout, and E1 legs that are not
runnable on three DCs.

### Standard suite (2026-10-10)

Docker `go-all-test` on the rebuilt single-DC stack, with every mandatory
evidence gate.

**First run.** The unit phase passed. Integration hit
`panic: test timed out after 40m0s` while `TestW2NativeHEADAmbiguity` was
still progressing. In the same run, `TestE16RevertFileRetainedHistory` failed
on fixture setup ("expected exactly one owned real queue row, found 0"): the
shared development daemon runs the aggressive GC configuration, which can
consume the leg's queue row first. That is the same background-GC
interference E1-09 recorded.

**Rerun.** The identical command with only `-timeout 75m` exited 0:

| Phase | Result |
|---|---|
| Integration | 533 PASS / 0 FAIL / 75 SKIP, 2399.523s |
| API | 20/20 suites |
| OIDC | 25/25 |

`TestE16RevertFileRetainedHistory` passed (44.79s). The suite finished 0.5s
under the 40m limit. That budget pressure and the three-DC harness limit
above are registered in TECHNICAL-DEBT ("E1-16 test-infrastructure
follow-ups").

**Doc guards.** `TestX1PhysicalLifeHandoff*` passes locally and in both
unit phases. `TestR3CharacterizationBaseIsDocumented` passes in the rerun
over the edited R3 document.
