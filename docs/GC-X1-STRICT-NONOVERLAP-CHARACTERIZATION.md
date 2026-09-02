# X1 strict non-overlap characterization

**Accepted architecture (2026-09-02):** this document is characterization
evidence, not the closure plan. The candidate “keep P1 canonical until
`DeleteExact(K1)`, then Finalize” is **not** the accepted D0 architecture.
Orphan after a confirmed handoff **is** the accepted post-`blocks` delete
authority; that supersedes Prerequisite B’s “orphan/020 as discovery, not
delete authority” as a target. Measured results below are unchanged
observations. See
[`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`](./GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md).

**Characterization parent:** `c8b432ca5` (`main` containing #198)
**Branch:** `test/x1-strict-nonoverlap-quiescence`
**Scope:** investigation only; no protocol, schema, worker, or production `GC_ENABLED` change.
Scanner rediscovery hooks used by F0b live in `internal/gc/x1_integration_hooks.go`
(`//go:build integration`) so the production `gc` API is unchanged.
**Production/fleet status:** X1 remains OPEN. R3 remains OPEN. Destructive GC remains
disabled for deployment (`GC_ENABLED=false` fleet-wide). The Docker integration
profile may run background GC on the primary test node; that is not the production
activation state.

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

H and D2 are writer-side holes. This PR characterizes H through the residual PUT
and D2 through post-cut `pub:` staging. Subsequent HEAD is not characterized.
The writer protocol through HEAD is not yet fully specified.

```text
WRITER (characterized): residual PUT after authority (H)
                        post-cut pub: staging without own pin (D2)
WRITER (not characterized): continuity through HEAD
GC:     global zero → irreversible cut → keep canonical P → physical delete → finalize
DISCOVERY: queue / pending / cursor are not a durable recovery root for a committed delete
```

## Named evidence legs

| Field | Case | Property |
|-------|------|----------|
| `writerFirst` | A | visible `up:` beats EACH_QUORUM; GC releases |
| `gcFirst` | B | `deleting` fences Register; no metadata |
| `refBeforeZeroProof` | C1 | pin before zero-proof revokes |
| `refBetweenProofAndCut` | C2 | late `up:` after zero-proof; handoff still commits; writer loses fence |
| `lateUploadRef` | D1 | post-cut `up:` may exist; it must not revoke D. Becoming `fs:`/HEAD is not characterized |
| `borrowedFSPublish` | D2 | post-cut `pub:` staging is unguarded. Writer prerequisite is not yet fully specified; continuity through HEAD is not characterized |
| `physicalDeleteFailure` | E | `DeleteBlockByStorageKey` rejected before the backend (foreign tenant prefix, not a MinIO 5xx). K1 and P1 stay canonical; P2 cannot IF NOT EXISTS |
| `postCommitResume` | F0a | crash after handoff; `CommittedOwner` resumes D from `blocks` |
| `pendingBlocksReenqueue` | F0b1 | queue-loss without DLQ; scanner pending lock skips reenqueue |
| `candidateBehindCursor` | F0b2 | old candidate + real cursor; `ScanOrphanedBlocksOnce` does not reenqueue |
| `postDeleteCrash` | F1 | delete success, crash before finalize; retry K1 then exact finalize |
| `ambiguousFinalizeSafety` | F2-safety | discarded successful finalize + P2 installed; stale D1 must not destroy K2 |
| `ambiguousFinalizeConvergence` | F2-convergence | can work items close without 020? settlement, not safety |
| `lateRepairPut` | H | residual authority→PUT via exported `put` callback |
| `nextIncarnation` | I | P2 installs only after Finalize, with `K2 != K1` |

## Measured results

Gated Docker re-run on 2026-08-31 after F0b1/F0b2/D1/D2/E evidence corrections
(`go-integration-test`, `SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION=1`).
`TestX1StrictNonoverlapCharacterization` reported `missing=[] complete=true`.
Each row is an observation, not a production change. H is fail-closed: if K1
is not resurrected, the characterization baseline fails. A passing H means the
writer hole reproduced (`resurrected=true`).

| Field | Observation | Notes |
|-------|-------------|-------|
| `writerFirst` | GREEN | visible `up:` revoked the authorizing EACH_QUORUM read; GC released |
| `gcFirst` | GREEN | `RegisterUploadedBlockTarget` returned `ErrBlockDeleteInProgress`; pin stayed out of metadata |
| `refBeforeZeroProof` | GREEN | pin before zero-proof made `BlockHasReferencesGlobal` true |
| `refBetweenProofAndCut` | GREEN | late `up:` after zero-proof; Acquired path still committed handoff; resume was `CommittedOwner` with original D |
| `lateUploadRef` | GREEN | post-cut `up:` row existed; `blocks` stayed `deleting`+handoff with original D; new-request probe `BlockedByGC`; no `fs:` referrer. Subsequent HEAD is not characterized |
| `borrowedFSPublish` | RED (unguarded staging) | `pub_exists=true` after `AddPublishAttemptReferences`; new-request probe `BlockedByGC`. Pre-HEAD hole only. Writer prerequisite is not yet fully specified; continuity through HEAD is not characterized |
| `physicalDeleteFailure` | GREEN | foreign-tenant `DeleteBlockByStorageKey` rejected before MinIO (prefix). K1 remained; P1 canonical; P2 `InstallBlockMetadata` was not `Applied`. Not an S3 timeout/5xx |
| `postCommitResume` | GREEN | queue still present after handoff; a different claimer resumed `CommittedOwner` D from `blocks` |
| `pendingBlocksReenqueue` | GREEN | exact `gc_queue` row deleted; pending kept; no DLQ; `ScanOrphanedBlocksOnce` with cursor `today-1` enqueued=0 for this item. Scanner lock ≠ recovery root |
| `candidateBehindCursor` | GREEN | 30-day candidate, queue/pending/DLQ absent; cursor `gc.scan.block_candidates.last_candidate_day=today-1`; after `ScanOrphanedBlocksOnce` (enqueued=0) candidate still existed and queue still absent. Existence ≠ rediscovery |
| `postDeleteCrash` | GREEN | `DeleteBlockByStorageKey` idempotent while canonical P1 remained; exact finalize then removed `blocks` |
| `ambiguousFinalizeSafety` | GREEN | discarded first finalize (not a lost LWT); retry `not_authority`; K2 object intact. Stale D1 did not destroy the next incarnation |
| `ambiguousFinalizeConvergence` | OPEN settlement | asserted retry `not_authority`, claim `missing`, leftover `pending=true`. Classification, not a license to delete. Work items did not close |
| `lateRepairPut` | RED (prerequisite) | residual PUT `err=<nil> resurrected=true`. Test fails if K1 is not resurrected. Pre-PUT authority is not backed by own liveness visible to GC |
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
| candidate / `gc_queue` / `gc_pending_items` | durable recovery root? | F0a: work binding only. F0b1: queue-loss without DLQ did not reenqueue (pending lock). F0b2: behind-cursor candidate was not rediscovered by the real scanner. F2-convergence: leftover `pending=true`, claim `missing` |
| R18/R27 | safety or liveness? | still OPEN; not closed here |
| E6 | loss vs duplicate work? | still OPEN; not closed here |
| R3 | publication TOCTOU | still OPEN; D2 measured unguarded post-cut `pub:` staging. Writer prerequisite is not yet fully specified; continuity through HEAD is not characterized. Follow-up: `docs/R3-BORROWEDFS-HEAD-CHARACTERIZATION.md` |

## Performance

Strict non-overlap for GC reorders the cold destructive path:

```text
writer hot path: +0 SERIAL, +0 EACH_QUORUM, +0 S3
```

This PR does not promise 0 I/O to close R3. `BorrowedFS` still has no own-pin
(#198). Writer cost through HEAD is not specified here.

## Verdict

Terminal state of this document must be exactly one of:

- `PROMISING`
- `PROMISING_WITH_PREREQUISITE`
- `REJECT`

**Current verdict: PROMISING_WITH_PREREQUISITE**

H resurrected K1 after `DeleteBlockByStorageKey`. D2 landed a post-cut `pub:`
staging row without own pin or fence. F0b1/F0b2 showed queue/pending/cursor are
not a durable recovery root. F2-convergence showed post-Finalize work items do
not close. The candidate authority/lifetime model held across E/F0a/F1/F2-safety/I.
The physical-delete-before-finalize sequence itself is directly exercised by
F1, F2-safety and I; E and F0a characterize its failure/resume states before
physical retirement.

Two independent prerequisites before any protocol switch:

```text
Prerequisite A — writer quiescence / publication
  own liveness → fence → PUT
  D2 proves a pre-HEAD pub: staging hole.
  Writer prerequisite is not yet fully specified;
  continuity through HEAD requires a follow-up characterization
  before any protocol switch. Subsequent HEAD is not characterized.
  Follow-up measurement (not a protocol switch):
  docs/R3-BORROWEDFS-HEAD-CHARACTERIZATION.md

Prerequisite B — committed-delete recovery
  durable rediscovery after queue loss
  unambiguous post-Finalize settlement
  (orphan/020 may remain temporarily as discovery/settlement,
   not as delete authority)
```

X1 remains OPEN. R3 remains OPEN. Production remains `GC_ENABLED=false` fleet-wide.

## Mutations

These are **source/AST contract mutations**, not runtime mutation testing of a
mutated binary against Cassandra+MinIO. `go test ./internal/gc` executes the
unmodified repository; `X1_SOURCE_ROOT` points the AST contracts at an isolated
fixture. Fifteen mutations staying RED means the static contracts detected those
alterations.

Load-bearing AST contracts live in `internal/gc/x1_nonoverlap_harness_contract_test.go`.
They require `DeleteBlockByStorageKey` before `FinalizeBlockDelete` in F1, F2-safety,
F2-convergence, and I individually; H must `Fatal` if K1 is not resurrected;
committed handoff must resume original D; E must keep retry identity at exact K1.
The isolated-fixture script must stay RED when those predicates are removed.
Perl is in `Dockerfile.gotest`. Run:

```bash
docker compose --profile test run --rm --build gotest bash scripts/x1-nonoverlap-mutation-validation.sh
```
