# X1 strict non-overlap characterization

**Characterization parent:** `c8b432ca5` (`main` containing #198)
**Branch:** `test/x1-strict-nonoverlap-quiescence`
**Scope:** investigation only; no protocol, schema, worker, or `GC_ENABLED` change
**Status:** X1 remains OPEN. R3 remains OPEN. `GC_ENABLED=false`.

The real Cassandra+MinIO evidence gate is `SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION=1`.
`TestMain` requires all **15 named legs** after `m.Run()`. Completeness is `missing()` by
name, never a counter. A missing stack or a skip cannot report green when the gate is armed.

## Question

Can `blocks(L)=P1/deleting/handoff=true` remain the only destructive authority and fence
until P1 is physically retired and no still-authorized writer can rewrite K1, and only
then may the canonical row be removed so a fresh P2 may install?

## Two planes

```text
AUTHORITY                          DISCOVERY
    │                                  │
    ▼                                  ▼
blocks(L)=P,D,deleting,handoff=true    queue / candidate / gc_pending_items
    │                                  perhaps gc_s3_orphans
    ▼                                  ▼
DeleteBlockByStorageKey(K1)            "there is pending work"
    │
    ▼
FinalizeBlockDelete
```

Discovery may say where to look. It must never authorize a DELETE. Recovery:

```text
discovery(L) → read blocks(L)
→ exact P,D,deleting,handoff=true? resume exact P : stale, no authority
```

## Current production vs candidate harness

Current `processBlock` (not modified by this PR):

```text
Claim → EACH_QUORUM refs → CommitBlockDeleteOrphanHandoff
→ StartBlockDeleteOrphan + lifecycle 020
→ FinalizeBlockDelete
→ DeleteBlockByStorageKey
```

Candidate harness (exported primitives only, no `worker.go`):

```text
Claim → EACH_QUORUM refs → CommitBlockDeleteOrphanHandoff
→ NO StartBlockDeleteOrphan
→ DeleteBlockByStorageKey
→ FinalizeBlockDelete
```

The first `FinalizeBlockDelete` LWT does not need orphan or migration 020. Those appear
only when the row is already gone and a retry must be classified `AlreadyFinalized`.

## Writer-side invariant under test

H and D2 are the same missing rule:

```text
WRITER: own liveness → fence → PUT | pub/HEAD
GC:     global zero → irreversible cut → keep canonical P → physical delete → finalize
```

## Named evidence legs

| Field | Case | Property |
|-------|------|----------|
| `writerFirst` | A | visible `up:` beats EACH_QUORUM; GC releases |
| `gcFirst` | B | `deleting` fences Register; no metadata |
| `refBeforeZeroProof` | C1 | pin before zero-proof revokes |
| `refBetweenProofAndCut` | C2 | late `up:` after zero-proof; handoff still commits; writer loses fence |
| `lateUploadRef` | D1 | post-cut `up:` may exist as a row; it must not revoke D or become `fs:`/HEAD |
| `borrowedFSPublish` | D2 | in-flight BorrowedFS staging after the cut, without own pin |
| `s3Failure` | E | failed physical delete keeps P1 canonical; P2 cannot IF NOT EXISTS |
| `postCommitResume` | F0a | crash after handoff; `CommittedOwner` resumes D from `blocks` |
| `pendingBlocksReenqueue` | F0b1 | queue absent + pending present; scanner `PendingItemExists` skips |
| `candidateBehindCursor` | F0b2 | candidate exists behind lookback/overlap; existence ≠ rediscovery |
| `postDeleteCrash` | F1 | delete success, crash before finalize; retry K1 then exact finalize |
| `ambiguousFinalizeSafety` | F2-safety | lost finalize result + P2 installed; stale D1 must not destroy K2 |
| `ambiguousFinalizeConvergence` | F2-convergence | can work items close without 020? settlement, not safety |
| `lateRepairPut` | H | residual authority→PUT via exported `put` callback |
| `nextIncarnation` | I | P2 installs only after Finalize, with `K2 != K1` |

## Measured results

Gated Docker run on 2026-08-31 against Cassandra+MinIO
(`go-integration-test`, `SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION=1`).
`TestX1StrictNonoverlapCharacterization` reported `missing=[] complete=true`.
Each row is an observation, not a production change. H RED (K1 resurrected)
still completes the leg: that is the architectural finding.

| Field | Observation | Notes |
|-------|-------------|-------|
| `writerFirst` | GREEN | visible `up:` revoked the authorizing EACH_QUORUM read; GC released |
| `gcFirst` | GREEN | `RegisterUploadedBlockTarget` returned `ErrBlockDeleteInProgress`; pin stayed out of metadata |
| `refBeforeZeroProof` | GREEN | pin before zero-proof made `BlockHasReferencesGlobal` true |
| `refBetweenProofAndCut` | GREEN | late `up:` after zero-proof; Acquired path still committed handoff; resume was `CommittedOwner` with original D |
| `lateUploadRef` | GREEN | post-cut `up:` row existed; `blocks` stayed `deleting`+handoff with original D; new-request probe `BlockedByGC` |
| `borrowedFSPublish` | RED (unguarded) | `pub_exists=true` after `AddPublishAttemptReferences`; new-request probe `BlockedByGC`. In-flight `pub:` landed without own pin or fence |
| `s3Failure` | GREEN | no physical delete; P1 remained canonical; `InstallBlockMetadata` for P2 was not `Applied` |
| `postCommitResume` | GREEN | queue still present after handoff; a different claimer resumed `CommittedOwner` D from `blocks` |
| `pendingBlocksReenqueue` | GREEN | constructed queue/pending split; `PendingItemExists==true` and queue absent; candidate still listed. Scanner lock ≠ recovery root |
| `candidateBehindCursor` | GREEN | candidate row existed 30 days behind lookback 7 / overlap window; existence ≠ rediscovery |
| `postDeleteCrash` | GREEN | `DeleteBlockByStorageKey` idempotent while canonical P1 remained; exact finalize then removed `blocks` |
| `ambiguousFinalizeSafety` | GREEN | retry finalize `not_authority`; K2 object intact. Stale D1 did not destroy the next incarnation |
| `ambiguousFinalizeConvergence` | OPEN settlement | retry without orphan/020 = `not_authority`; post-finalize claim = `missing`; `pending=true`. Classification, not a license to delete. Work items did not close |
| `lateRepairPut` | RED (prerequisite) | residual PUT `err=<nil> resurrected=true`. Pre-PUT authority is not backed by own liveness visible to GC |
| `nextIncarnation` | GREEN | P2 `InstallBlockMetadata` refused while P1 canonical; applied after delete+finalize with `K2 != K1` |

Current-protocol contrast (`TestX1CurrentProtocolFinalizeBeforeDeleteContrast`, not a gated leg):
orphan + `FinalizeBlockDelete` before physical delete removed `blocks` while K1 may still exist.

## Component classification (trusted safety core?)

| Component | Question | Working classification |
|-----------|----------|------------------------|
| `blocks` | sole destructive authority until physical delete? | candidate harness treats it as yes |
| `gc_orphan_handoff` | irreversible cut of exact (P,D)? | yes (migration 019) |
| `gc_s3_orphans` | authority, discovery, or unnecessary? | not required for first Finalize under the candidate order |
| `gc_s3_orphans_by_day` | recovery index only? | discovery projection; no payload authority |
| `gc_block_delete_lifecycles` | safety-critical tombstone? | not required to protect K2 under candidate order (F2-safety `not_authority`). Still how production classifies `AlreadyFinalized`. F2-convergence left pending work open without it |
| candidate / `gc_queue` / `gc_pending_items` | durable recovery root? | F0a: work binding only. F0b1/F0b2: scanner skip / behind-cursor existence. F2-convergence: leftover `pending=true`, claim `missing` |
| R18/R27 | safety or liveness? | still OPEN; not closed here |
| E6 | loss vs duplicate work? | still OPEN; not closed here |
| R3 | publication TOCTOU | still OPEN; D2 measured unguarded post-cut `pub:` |

## Performance

Strict non-overlap for GC reorders the cold destructive path:

```text
writer hot path: +0 SERIAL, +0 EACH_QUORUM, +0 S3
```

This PR does not promise 0 I/O to close R3. `BorrowedFS` still has no own-pin
(#198). If the prerequisite is `own pin → fence → PUT/pub`, that cost is separate.

## Verdict

Terminal state of this document must be exactly one of:

- `PROMISING`
- `PROMISING_WITH_PREREQUISITE`
- `REJECT`

**Current verdict: PROMISING_WITH_PREREQUISITE**

H resurrected K1 after `DeleteBlockByStorageKey`. D2 landed a post-cut `pub:`
without own pin or fence. Both are the same writer-side hole. The GC reorder
(keep canonical P → physical delete → finalize) held for E/F0a/F1/F2-safety/I.

Required writer prerequisite before any protocol change:

```text
own liveness → fence → PUT
own liveness → fence → pub/HEAD
```

X1 remains OPEN. R3 remains OPEN. `GC_ENABLED=false`.
