# GC Service Flow Diagrams

> How to read: Follow arrows top-to-bottom. Green nodes are safety mechanisms.
> Red nodes are deletion actions. Yellow nodes are potential risk areas.
> Diamonds are decision points.

### How to read colors

| Color | Meaning |
|-------|---------|
| **Red** | Destructive action (deletion) |
| **Yellow** | Risk area or potential issue |
| **Green** | Safety mechanism |
| **Blue** | Enqueue / scheduling |

---

## 1. Worker Loop

How the worker goroutine runs continuously, processing batches.

```mermaid
flowchart TD
    Start["Service.Start()"] --> Tick{"Tick every 30s<br/>or manual trigger?"}
    Tick -->|Tick| WO["Worker.ProcessOnce()"]
    Tick -->|Trigger| WO

    WO --> ListOrgs["List orgs with<br/>queued items"]
    ListOrgs --> ForOrg["For each org"]

    ForOrg --> Dequeue["DequeueBatch<br/>100 items where<br/>queued_at < now - grace"]
    Dequeue --> ForItem["For each item"]

    ForItem --> Dispatch{"Item type?"}
    Dispatch --> Block["processBlock"]
    Dispatch --> Commit["processCommit"]
    Dispatch --> FSObj["processFSObject"]
    Dispatch --> Cascade["processCascade<br/>(user/lib/org)"]
    Dispatch --> Other["processShare/Link/<br/>RestoreJob/Mapping"]

    Block --> OK{"Success?"}
    Commit --> OK
    FSObj --> OK
    Cascade --> OK
    Other --> OK

    OK -->|Yes| Complete["Complete: remove<br/>from gc_queue"]
    OK -->|No| Retry{"Retry < 5?"}
    Retry -->|Yes| Requeue["Requeue to back<br/>with new timestamp"]
    Retry -->|No| TTL["Let TTL expire<br/>(7 day table TTL)"]

    Complete --> ForItem
    Requeue --> ForItem
    TTL --> ForItem

    style Dequeue fill:#28a745,color:#fff
    style Complete fill:#dc3545,color:#fff
    style Requeue fill:#17a2b8,color:#fff
```

---

## 2. Block Deletion (Critical Path)

The most dangerous operation — deleting data from S3.

```mermaid
flowchart TD
    Item["Block item dequeued<br/>past grace period"]
    Item --> DryRun{"Dry run?"}
    DryRun -->|Yes| Log["Log: would delete"]

    DryRun -->|No| LWT["LWT: UPDATE blocks<br/>SET ref_count = -999<br/>IF ref_count <= 0"]

    LWT --> Applied{"LWT applied?"}
    Applied -->|No| Skip["ref_count > 0<br/>or already deleted<br/>SKIP S3 delete"]
    Skip --> CleanCandidate1["Delete GC candidate"]

    Applied -->|Yes| DBDel["DELETE FROM blocks"]
    DBDel --> S3Del["S3: DeleteBlock"]
    S3Del --> S3OK{"S3 success?"}

    S3OK -->|Yes| CleanMappings["Delete SHA-1 → SHA-256<br/>block mappings"]
    CleanMappings --> CleanCandidate2["Delete GC candidate"]
    CleanCandidate2 --> Stats["Increment<br/>blocks_deleted"]

    S3OK -->|No| Orphan["WARNING: S3 orphan<br/>DB record gone<br/>S3 block remains<br/>Never recovered"]

    style LWT fill:#28a745,color:#fff
    style Skip fill:#28a745,color:#fff
    style DBDel fill:#dc3545,color:#fff
    style S3Del fill:#dc3545,color:#fff
    style Orphan fill:#ffc107,color:#000
```

---

## 3. FS Object Processing (Decrement + Cascade)

How file deletion triggers block cleanup with idempotency protection.

```mermaid
flowchart TD
    Item["FS object item<br/>dequeued"]
    Item --> Get["Get fs_object<br/>from DB"]
    Get --> Found{"Found?"}
    Found -->|No| Done["Already deleted,<br/>skip"]

    Found -->|Yes| IsDir{"Directory?"}
    IsDir -->|Yes| EnqKids["Enqueue all children<br/>with parent's QueuedAt<br/>(skip grace period)"]

    IsDir -->|No| HasBlocks{"Has blocks?"}
    HasBlocks -->|No| Delete

    HasBlocks -->|Yes| TaskID["Create deterministic<br/>taskID = MD5(fsID + timestamp)"]
    TaskID --> Mark["MarkItemProcessed<br/>INSERT IF NOT EXISTS"]
    Mark --> First{"First time?"}

    First -->|Yes| Decrement["For each block:<br/>DecrementRefCount"]
    Decrement --> Check["GetRefCount<br/>for each block"]
    Check --> Zero["Collect blocks<br/>where ref_count <= 0"]
    Zero --> Enqueue["Enqueue zero-ref blocks<br/>for deletion"]

    First -->|No| SkipDec["Skip decrement<br/>(already processed)"]

    EnqKids --> Delete
    Enqueue --> Delete
    SkipDec --> Delete
    Delete["Delete fs_object<br/>from DB"]

    style Mark fill:#28a745,color:#fff
    style SkipDec fill:#28a745,color:#fff
    style Decrement fill:#ffc107,color:#000
    style Delete fill:#dc3545,color:#fff
    style Enqueue fill:#17a2b8,color:#fff
```

---

## 4. Library Cascade

What happens when a soft-deleted library's retention period expires.

```mermaid
flowchart TD
    Trigger["Library trash retention<br/>expired (default 30 days)"]
    Trigger --> Scanner["Scanner finds it in<br/>scanExpiredDeletedLibraries"]
    Scanner --> Enqueue["Enqueue as<br/>ItemLibraryCascade"]

    Enqueue --> Worker["Worker picks up item"]
    Worker --> Stale1["Check deleted_at<br/>still matches"]
    Stale1 --> Lock["Acquire hard-delete lock<br/>(blocks concurrent restore)"]
    Lock --> Stale2["Re-check deleted_at<br/>after lock acquired"]

    Stale2 --> Restored{"Restored<br/>since enqueue?"}
    Restored -->|Yes| Release["Release lock, skip"]

    Restored -->|No| EnqCommits["Enqueue all commits"]
    EnqCommits --> EnqFS["Enqueue all fs_objects"]
    EnqFS --> CleanArtifacts["Delete shares, tags,<br/>tokens, starred,<br/>monitored, restore jobs"]
    CleanArtifacts --> DelCounter["Delete storage counter"]
    DelCounter --> HardDel["Hard-delete library<br/>record from DB"]
    HardDel --> Audit["Write audit log"]
    Audit --> ReleaseLock["Release lock"]

    EnqCommits -.->|"Worker processes"| CommitDel["processCommit<br/>→ enqueue root fs_object"]
    CommitDel -.-> FSDel["processFSObject<br/>→ decrement blocks"]
    FSDel -.-> BlockDel["processBlock<br/>→ LWT → S3 delete"]

    style Lock fill:#28a745,color:#fff
    style Stale1 fill:#28a745,color:#fff
    style Stale2 fill:#28a745,color:#fff
    style HardDel fill:#dc3545,color:#fff
    style BlockDel fill:#dc3545,color:#fff
```

---

## 5. Scanner Phases

The 13-phase safety sweep that catches orphaned items.

```mermaid
flowchart TD
    Start["ScanOnce()<br/>Runs on startup + every 24h"]

    Start --> P1["Phase 1: Orphaned Blocks<br/>ref_count <= 0 not in queue"]
    P1 --> P2["Phase 2: Expired Share Links<br/>expires_at < now"]
    P2 --> P3["Phase 3: Orphaned Commits<br/>library no longer exists"]
    P3 --> P4["Phase 4: Orphaned FS Objects<br/>library no longer exists"]
    P4 --> P5["Phase 5: Expired Versions<br/>commits older than version_ttl"]
    P5 --> P6["Phase 6: Auto-Delete Expired<br/>objects older than auto_delete_days"]
    P6 --> P7["Phase 7: Expired Shares<br/>user shares past expires_at"]
    P7 --> P8["Phase 8: Expired Restore Jobs<br/>completed/expired jobs"]
    P8 --> P9["Phase 9: Orphaned Group Shares<br/>shares to deleted groups"]
    P9 --> P10["Phase 10: Expired Deleted Users<br/>soft-deleted > grace days"]
    P10 --> P11["Phase 11: Storage Counter<br/>Reconciliation"]
    P11 --> P12["Phase 12: Expired Deleted Libraries<br/>trash > retention days"]
    P12 --> P13["Phase 13: Expired Deleted Orgs<br/>soft-deleted > grace days"]
    P13 --> Done["Update LastScanRun"]

    style P1 fill:#17a2b8,color:#fff
    style P10 fill:#dc3545,color:#fff
    style P12 fill:#dc3545,color:#fff
    style P13 fill:#dc3545,color:#fff
```

---

## 6. End-to-End: File Delete → Block Gone

The complete journey from API call to S3 block removal.

```mermaid
flowchart TD
    API["API: DELETE /file"]
    API --> UpdateFS["Update commit tree<br/>(new commit without file)"]
    UpdateFS --> DecRef["Decrement block ref_counts<br/>(background goroutine)"]
    DecRef --> FindZero["Find blocks where<br/>ref_count hit 0"]
    FindZero --> Adapter["gc_adapter: EnqueueBlocks()"]
    Adapter --> Ensure["EnsureBlockGCCandidate<br/>INSERT IF NOT EXISTS"]
    Ensure --> Enqueue["EnqueueItem into gc_queue<br/>with current timestamp"]

    Enqueue --> Wait["Wait for grace period<br/>(default 1 hour)"]
    Wait --> Dequeue["Worker: DequeueBatch<br/>items older than grace"]
    Dequeue --> LWT["LWT: IF ref_count <= 0"]
    LWT --> DBDel["DELETE from blocks table"]
    DBDel --> S3Del["DELETE from S3"]
    S3Del --> Clean["Clean mappings +<br/>GC candidate"]
    Clean --> Done["Block fully removed"]

    style API fill:#6c757d,color:#fff
    style LWT fill:#28a745,color:#fff
    style Wait fill:#28a745,color:#fff
    style DBDel fill:#dc3545,color:#fff
    style S3Del fill:#dc3545,color:#fff
```
