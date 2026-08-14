# GC Service Flow Diagrams

**Corrected:** 2026-07-10 · **Code:** `4333be1b3`

> Current model: keyed `block_references`, durable candidates, LWT claim + live-ref recheck,
> and write-ahead `gc_s3_orphans`. The retired `blocks.ref_count = -999` protocol is preserved
> in Git history, not in this live diagram. The physical block-delete sequence is conservative,
> and both the transient-error fail-open (P6a) and execution-time canonical revalidation gap
> (P6b) are fixed. Markerless artifact discovery remains P7. Full audit:
> [../GC-DELETE-CLEANUP-INVESTIGATION.md](../GC-DELETE-CLEANUP-INVESTIGATION.md).
>
> **Operator gate:** keep `GC_ENABLED=false` on every replica in every DC while X1
> physical-delete ABA remains open. X2 cross-DC reference visibility is closed under
> its stable-topology operational contract. The lease does not close X1. Only after
> X1 closes may designated replicas in one DC participate under the lease.

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

The sequence above is conservative **up to S3 delete authorization**, given correct upstream classification/reference ownership. It does not close X1: Cassandra authorization/claim generations cannot revoke an S3 DELETE already in flight; only never-reused generational physical keys prevent a stale delete from targeting re-uploaded bytes. The P6
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

Scanner-created Phase 3/4 orphan work carries durable guard mode `canonical_absent`. At execution,
the worker takes the library lock, point-reads canonical `libraries[(org_id, library_id)]`, skips a
present library, fails closed on read errors, and synchronously fences destructive mutations. Normal
cascade children use `deleted_at_identity`; legacy true booleans map to that mode. This closes P6b.

## 4. Library delete and purge handoff

```mermaid
flowchart TD
    Soft[Soft delete] --> Marker[Stamp deleted_libraries representation + deleted_at]
    Marker --> Retention[Phase 13 after retention]
    Retention --> LibCascade[Durable ItemLibraryCascade]

    Permanent[Direct permanent delete] --> Resolve[Resolve/validate representation]
    Resolve --> HardRows[Delete library/read models + stamp marker]
    HardRows --> Immediate[Best-effort immediate ItemLibraryCascade enqueue]
    Immediate --> MarkerRescue[Durable purge marker retained for Phase 13 recovery]
    MarkerRescue --> LibCascade

    LibCascade --> Enumerate[Enqueue commits/fs_objects/artifacts]
    Enumerate --> Counter[Delete library storage counter]
    Counter --> Final[Delete library marker + policy indexes]
```

Current code stamps `purge_requested_at` so permanent deletes are immediately Phase-13 eligible;
the immediate enqueue is only an accelerator and the durable marker is the recovery path.

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
- `LibraryExists`/`GroupExists` fail **closed** on Cassandra errors (P6a), and queued orphan work
  is canonically revalidated at execution time (P6b); both are fixed.
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
