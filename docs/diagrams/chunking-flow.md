# Chunking & Block Storage Flow Diagrams

> How to read: Blue nodes are storage/hashing operations. Green nodes are
> optimization steps. Yellow nodes are compatibility concerns.

### How to read colors

| Color | Meaning |
|-------|---------|
| **Blue** | Storage or hashing operation |
| **Green** | Optimization (dedup, check-blocks) |
| **Yellow** | Compatibility concern or limitation |
| **Gray** | Client-side operation |

---

## 1. FastCDC Boundary Detection

```mermaid
flowchart TD
    File["File bytes"] --> Window["64-byte rolling<br/>Gear hash window"]

    Window --> Phase1{"Position < minSize<br/>(2 MB)?"}
    Phase1 -->|Yes| Skip["Skip — too small<br/>for a boundary"]
    Skip --> Window

    Phase1 -->|No| Phase2{"Position < avgSize<br/>(16 MB)?"}
    Phase2 -->|Yes| MaskS["Apply maskS<br/>(easy match)"]
    MaskS --> Match1{"Hash matches<br/>boundary pattern?"}
    Match1 -->|Yes| Cut["Cut here → new block"]
    Match1 -->|No| Window

    Phase2 -->|No| Phase3{"Position < maxSize<br/>(256 MB)?"}
    Phase3 -->|Yes| MaskL["Apply maskL<br/>(hard match)"]
    MaskL --> Match2{"Hash matches?"}
    Match2 -->|Yes| Cut
    Match2 -->|No| Window

    Phase3 -->|No| ForceCut["Force cut at maxSize"]
    Cut --> Next["Start next block"]
    ForceCut --> Next
```

---

## 2. Three Client Upload Paths

```mermaid
flowchart LR
    subgraph Desktop["Seafile Desktop"]
        D1["Client-side FastCDC<br/>variable blocks"]
        D1 --> D2["check-blocks<br/>(send SHA-1 list)"]
        D2 --> D3["Upload ONLY<br/>missing blocks"]
        D3 --> D4["Create commit"]
    end

    subgraph Web["Web UI"]
        W1["Full file POST<br/>or Content-Range chunks"]
        W1 --> W2["Server splits<br/>into 8 MB blocks"]
        W2 --> W3["Store all blocks<br/>(dedup at S3 level)"]
        W3 --> W4["Create commit"]
    end

    subgraph Mobile["Mobile App"]
        M1["App chunks file<br/>(app-specific)"]
        M1 --> M2["POST /blocks/check<br/>(send SHA-256 list)"]
        M2 --> M3["Upload ONLY<br/>missing blocks"]
        M3 --> M4["Create file via API"]
    end

    style D2 fill:#28a745,color:#fff
    style D3 fill:#28a745,color:#fff
    style M2 fill:#28a745,color:#fff
    style M3 fill:#28a745,color:#fff
    style W2 fill:#ffc107,color:#000
```

---

## 3. Block Identity: SHA-1 ↔ SHA-256 Mapping

```mermaid
flowchart TD
    subgraph Upload["On Upload"]
        Plain["Plaintext block"]
        Plain --> SHA1["SHA-1(plaintext)<br/>= 40 hex chars<br/>Seafile protocol ID"]
        Plain --> Enc{"Encrypt?"}
        Enc -->|Yes| Cipher["AES encrypt"]
        Enc -->|No| AsIs["Use plaintext"]
        Cipher --> SHA256["SHA-256(stored content)<br/>= 64 hex chars<br/>S3 storage key"]
        AsIs --> SHA256
        SHA256 --> DualWrite["Dual-write mappings"]
    end

    subgraph Tables["Cassandra Tables"]
        T1["block_id_mappings<br/>SHA-1 → SHA-256<br/>(forward lookup)"]
        T2["block_id_mappings_by_internal<br/>SHA-256 → SHA-1<br/>(reverse lookup)"]
    end

    subgraph Download["On Download"]
        FS["fs_object.block_ids<br/>(SHA-1 list)"]
        FS --> Resolve["BatchResolveBlockIDs<br/>SHA-1 → SHA-256"]
        Resolve --> S3Get["S3 Get by SHA-256"]
    end

    DualWrite --> T1
    DualWrite --> T2
    Resolve --> T1

    style SHA1 fill:#ffc107,color:#000
    style SHA256 fill:#17a2b8,color:#fff
    style DualWrite fill:#17a2b8,color:#fff
```

---

## 4. Deduplication Within an Org

```mermaid
flowchart TD
    subgraph UserA["User A uploads report.pdf"]
        A1["3 blocks: B1, B2, B3"]
        A1 --> A2["ref_count:<br/>B1=1, B2=1, B3=1"]
    end

    subgraph UserB["User B uploads same file"]
        B1["Same 3 blocks:<br/>B1, B2, B3"]
        B1 --> B2["Dedup: blocks exist<br/>Skip S3 write"]
        B2 --> B3["ref_count:<br/>B1=2, B2=2, B3=2"]
    end

    subgraph DeleteA["User A deletes file"]
        DA["Decrement refs"]
        DA --> DA2["ref_count:<br/>B1=1, B2=1, B3=1"]
        DA2 --> DA3["ref > 0: no GC"]
    end

    subgraph DeleteB["User B deletes file"]
        DB["Decrement refs"]
        DB --> DB2["ref_count:<br/>B1=0, B2=0, B3=0"]
        DB2 --> DB3["Enqueue for GC<br/>→ delete from S3"]
    end

    style B2 fill:#28a745,color:#fff
    style DA3 fill:#28a745,color:#fff
    style DB3 fill:#dc3545,color:#fff
```

---

## 5. Incremental Change (Why Content-Defined Chunking Matters)

```mermaid
flowchart LR
    subgraph Before["Before edit"]
        V1["report.pdf = [B1, B2, B3, B4, B5]<br/>50 MB, 5 blocks ~10 MB each"]
    end

    subgraph Edit["User edits page 2 (in B2)"]
        Change["Only B2 content changes<br/>B1, B3, B4, B5 identical"]
    end

    subgraph After["After edit (FastCDC)"]
        V2["report.pdf = [B1, B2', B3, B4, B5]<br/>Only B2' is new"]
    end

    subgraph Sync["Desktop sync upload"]
        S1["check-blocks: B1,B2',B3,B4,B5"]
        S1 --> S2["Server: B2' missing, rest exist"]
        S2 --> S3["Upload only B2' (~10 MB)<br/>NOT full 50 MB"]
    end

    Before --> Edit --> After --> Sync

    style S3 fill:#28a745,color:#fff
```

---

## 6. Cross-Method Dedup Limitation

```mermaid
flowchart TD
    Same["Same 50 MB file"]
    Same --> Desktop["Desktop uploads<br/>FastCDC: 5 variable blocks<br/>[12M, 8M, 14M, 9M, 7M]"]
    Same --> Web["Web UI uploads<br/>Fixed 8 MB: 7 blocks<br/>[8M, 8M, 8M, 8M, 8M, 8M, 2M]"]

    Desktop --> S3a["S3: 5 blocks stored"]
    Web --> S3b["S3: 7 blocks stored"]

    S3a --> Total["Total: 12 blocks in S3<br/>= 100 MB stored<br/>for ONE 50 MB file"]

    style Total fill:#ffc107,color:#000
```

**No cross-method dedup.** Different chunking produces different SHA-256 hashes.
The same file uploaded via web and synced via desktop creates two sets of blocks.
