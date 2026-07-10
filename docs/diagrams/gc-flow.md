# GC Service Flow Diagrams

**Corrected:** 2026-07-10 · **Code:** `4333be1b3`

> Current model: keyed `block_references`, durable candidates, LWT claim + live-ref recheck,
> and write-ahead `gc_s3_orphans`. The retired `blocks.ref_count = -999` protocol is preserved
> in Git history, not in this live diagram. The physical block-delete sequence is conservative,
> and both live-data classification exposures are now fixed: the transient-error fail-open (P6a, 1D)
> and the execution-time canonical revalidation of orphan work (P6b, 1E). Full audit:
> [../GC-DELETE-CLEANUP-INVESTIGATION.md](../GC-DELETE-CLEANUP-INVESTIGATION.md).

## 1. Worker loop

```mermaid
flowchart TD
    Tick[Worker tick / admin trigger] --> Active[List gc_active_orgs]
    Active --> Dequeue[Dequeue eligible rows older than grace]
    Dequeue --> Dispatch{item_type}
    Dispatch -->|block| Block[Block protocol]
    Dispatch -->|commit/fs_object| Content[Content cascade]
    Dispatch -->|library/user/org| Cascade[Identity-guarded cascade]
    Dispatch -->|other| Artifact[Artifact cleanup]
    Block --> Result{result}
    Content --> Result
    Cascade --> Result
    Artifact --> Result
    Result -->|success| Complete[Logged batch: delete queue + pending]
    Result -->|transient, retry < 5| Requeue[Logged batch: move queue row; retain pending]
    Result -->|retry cap| DLQ[Logged batch: gc_failed_items + pending]
    Result -->|hard-delete lock busy| Postpone[Requeue without retry increment]
```

## 2. Physical block deletion

```mermaid
flowchart TD
    Start[Block candidate queue item] --> Refs{Any block_references row?}
    Refs -->|yes| Stale[Delete stale candidate; keep block]
    Refs -->|no| Exists{Canonical block exists?}
    Exists -->|no| CleanupMissing[Best-effort mapping/candidate cleanup]
    Exists -->|yes| Claim[LWT set gc_state=deleting + stable claim_id]
    Claim --> Won{Claim owned by this logical candidate?}
    Won -->|no| Skip[Skip / retry owner]
    Won -->|yes| Recheck{Reference appeared after claim?}
    Recheck -->|yes| Release[Release claim; delete stale candidate]
    Recheck -->|no| WAL[Persist gc_s3_orphans before DB removal]
    WAL --> Finalize[LWT finalize canonical block delete]
    Finalize --> S3[Delete S3 with bounded retry]
    S3 -->|failure| Recover[Keep orphan row for scanner recovery]
    S3 -->|success| Mapping[Delete exact forward mapping]
    Mapping --> Clear[Clear orphan + candidate]
```

The sequence above is safe **given correct upstream classification/reference ownership**. The P6
fail-open path that could enqueue live fs_objects (causing GC itself to remove legitimate `fs:`
refs before this block protocol runs) is now fixed: existence reads fail closed (1D).

## 3. FS object cleanup

```mermaid
flowchart TD
    Item[fs_object item] --> Guard{Requires deleted-library identity check?}
    Guard -->|yes| Identity[Validate deleted_at / live-library state under lease]
    Guard -->|no| Load[Load fs_object]
    Identity -->|stale/restored| Stop[Complete without deletion]
    Identity -->|valid| Load
    Load --> Children[Enqueue directory children first]
    Children --> Resolve[Resolve IDs in stamped representation]
    Resolve --> Remove[Delete exact fs:library:fs_id refs]
    Remove --> Zero{Block has any refs left?}
    Zero -->|yes| DeleteFS[Delete fs_object]
    Zero -->|no| Candidate[EnsureBlockGCCandidate + queue]
    Candidate --> DeleteFS
```

Scanner-created orphan work uses durable `library_guard_mode=canonical_absent`; normal cascade
children use `deleted_at_identity`. P6a closed the fail-open existence read, and P6b (1E,
`ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01`) makes the worker acquire the shared restore/GC lifecycle
lease and fail closed on an O(1) point read of the canonical `(org_id, library_id)` row before
deletion. Library UUIDs are globally unique and immutable and are never moved between orgs, so the
queue's org partition is authoritative. Projection drift and scanner→worker restore races fail closed.

## 4. Library delete and purge handoff

```mermaid
flowchart TD
    Soft[Soft delete] --> Marker[Stamp deleted_libraries representation + deleted_at]
    Marker --> Retention[Phase 13 after retention]
    Retention --> LibCascade[Durable ItemLibraryCascade]

    Permanent[Direct permanent delete] --> Resolve[Resolve/validate representation]
    Resolve --> HardRows[Delete library/read models + stamp marker]
    HardRows --> Route{caller}
    Route -->|org-admin| NoImmediate[P1: no immediate content enqueue]
    Route -->|superadmin| Async[P2: go fn content-only accelerator]
    NoImmediate --> MarkerRescue[Marker retained; Phase 13 delayed]
    Async --> MarkerRescue
    MarkerRescue --> LibCascade

    LibCascade --> Enumerate[Enqueue commits/fs_objects/artifacts]
    Enumerate --> Counter[Delete library storage counter]
    Counter --> Final[Delete library marker + policy indexes]
```

Roadmap 6A/6B adds `purge_requested_at` so permanent deletes become immediately Phase-13 eligible;
the goroutine remains only an accelerator.

## 5. Scanner phases and known boundaries

`ScanOnce` currently runs 17 named phases in this order:

```mermaid
flowchart LR
    P0[expired provisional refs] --> P1[orphaned block candidates]
    P1 --> P2[expired links]
    P2 --> P3[orphaned commits]
    P3 --> P4[orphaned fs_objects]
    P4 --> P5[expired versions]
    P5 --> P6a[auto delete]
    P6a --> P7s[expired shares]
    P7s --> P8[expired restore jobs]
    P8 --> P9[orphaned group shares]
    P9 --> P10[expired failed items]
    P10 --> P11[expired deleted users]
    P11 --> P12[storage counter reconciliation]
    P12 --> P13[expired deleted libraries]
    P13 --> P14[expired deleted orgs]
    P14 --> P15[OnlyOffice pending blocks]
    P15 --> P16[S3 orphan recovery]
```

Important limits:

- P3/P4 discovery currently enumerates only `libraries_by_id` + `deleted_libraries`, so
  markerless artifact partitions are invisible (audit P7).
- `LibraryExists`/`GroupExists` now fail **closed** on Cassandra errors (audit P6a, fixed 1D);
  orphan commit/fs_object work is revalidated against the canonical `libraries` table at worker
  time (audit P6b, fixed 1E).
- Phase 9 streams `shares_by_group` with bounded process memory and cancellation, but the
  Cassandra read remains a global scan until bucketed partition discovery lands (audit P8).
- `up:` expiry has durable discovery; `pub:` expiry does not (audit P4).
- Phase 13 currently logs some delivery failures without returning them (audit P5).

## 6. Invariants for follow-up work

1. API handlers never release `fs:` refs; fs_object cleanup is the sole owner.
2. Library cascades never release `pub:` refs; publish/expiry owns them.
3. Zero-ref creates a candidate; only the worker performs physical deletion.
4. Keep claim + post-claim reference recheck + write-ahead S3 recovery.
5. Tests must use exact fixture/org scope; never run a nil-storage global worker over shared data.
6. Never truncate references/mappings as cleanup.
