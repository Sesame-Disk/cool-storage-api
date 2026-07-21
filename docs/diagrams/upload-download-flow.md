# Upload & Download Flow Diagrams

> How to read: Green nodes are security/safety checks. Blue nodes are storage operations.
> Yellow nodes are risk areas. Red nodes are potential failure points.

### How to read colors

| Color | Meaning |
|-------|---------|
| **Green** | Security check or safety mechanism |
| **Blue** | Storage / enqueue operation |
| **Yellow** | Risk area or edge case |
| **Red** | Failure or rejection |

---

## 1. Upload: Token → Store → Commit

```mermaid
flowchart TD
    Client["Client request"]
    Client -->|"GET /upload-link"| Auth["Auth + permission<br/>+ decrypt session check"]
    Auth -->|Fail| R403["403 Forbidden"]
    Auth -->|OK| Token["Create upload token<br/>16-byte random, 1h TTL"]
    Token -->|URL| Client

    Client -->|"POST /upload-api/:token"| ValidateToken["Validate token<br/>+ re-check permissions"]
    ValidateToken --> Quota["Quota pre-check<br/>BEFORE reading body"]
    Quota -->|Exceeded| R403b["403 Quota exceeded"]

    Quota -->|OK| Size{"Content-Range<br/>header?"}
    Size -->|No| SingleShot["Read full file<br/>into memory"]
    Size -->|Yes| Chunked["Write chunk<br/>to temp file"]

    Chunked --> Complete{"All chunks<br/>received?"}
    Complete -->|No| Wait["Return 200<br/>wait for next chunk"]
    Complete -->|Yes| Finalize["Read temp file<br/>in 8 MB blocks"]

    SingleShot --> Process
    Finalize --> Process["Per block:<br/>SHA-1 plaintext<br/>Encrypt (if needed)<br/>SHA-256 stored content"]

    Process --> Probe{"Canonical metadata<br/>+ GC fence probe"}
    Probe -->|Reusable| Verify["Verify object in<br/>canonical backend"]
    Probe -->|Needs PUT| S3Put["Direct PUT to<br/>canonical backend"]
    Probe -->|GC deleting| Retry["Retry with<br/>bounded backoff"]

    Verify --> Pin["Create row-per-reference<br/>provisional upload pin"]
    S3Put --> Pin
    Pin --> Mapping["Persist server-derived SHA-1<br/>representation mapping"]
    Mapping --> FSObj["Create fs_object<br/>with block_ids"]
    FSObj --> Commit["Create commit<br/>Update library head"]
    Commit --> Traffic["Record traffic<br/>(fire-and-forget)"]
    Traffic --> OK["200 OK"]

    style Auth fill:#28a745,color:#fff
    style ValidateToken fill:#28a745,color:#fff
    style Quota fill:#28a745,color:#fff
    style S3Put fill:#17a2b8,color:#fff
    style Pin fill:#17a2b8,color:#fff
    style Chunked fill:#ffc107,color:#000
    style R403 fill:#dc3545,color:#fff
    style R403b fill:#dc3545,color:#fff
```

---

## 2. Download: Token → Stream Blocks

```mermaid
flowchart TD
    Client["Client request"]
    Client -->|"GET /file/?p=/path"| Auth["Auth + permission<br/>+ decrypt session check"]
    Auth -->|Fail| R403["403"]
    Auth -->|OK| Token["Create download token<br/>1h TTL"]
    Token -->|URL| Client

    Client -->|"GET /files/:token/name"| Validate["Validate token<br/>+ permission re-check"]
    Validate --> Quota["Traffic quota check"]
    Quota --> Tree["Navigate commit tree<br/>library → head_commit<br/>→ root_fs → target file"]
    Tree --> Blocks["Get block_ids<br/>from fs_object"]
    Blocks --> Resolve["Resolve SHA-1 → SHA-256<br/>BatchResolveBlockIDs"]

    Resolve --> Type{"Video/audio?"}
    Type -->|Yes, plaintext| Seeker["BlockReadSeeker<br/>Supports Range (206)<br/>O(1 block) memory"]
    Type -->|Yes, encrypted| EncStream["Sequential decrypt stream<br/>Accept-Ranges: none<br/>plaintext offsets unavailable"]
    Type -->|No| Stream["StreamBlocks<br/>Prefetch pipeline<br/>O(2 blocks) memory"]

    Seeker --> Direct
    Stream --> Enc{"Encrypted?"}
    EncStream --> Decrypt
    Enc -->|Yes| Decrypt["Load block → decrypt<br/>AES-256-CBC"]
    Enc -->|No| Direct["Stream from S3<br/>4 MB buffer"]

    Decrypt --> Write["Write to HTTP response"]
    Direct --> Write
    Write --> RecordTraffic["Record traffic"]

    style Auth fill:#28a745,color:#fff
    style Validate fill:#28a745,color:#fff
    style Quota fill:#28a745,color:#fff
    style Resolve fill:#17a2b8,color:#fff
    style Seeker fill:#17a2b8,color:#fff
    style Stream fill:#17a2b8,color:#fff
```

---

## 3. Chunked Upload: Temp File Lifecycle

```mermaid
flowchart TD
    C1["Chunk 1 arrives<br/>Content-Range: 0-8M/50M"]
    C1 --> Create["Create temp file<br/>/tmp/sesamefs_upload_*<br/>Truncate to 50M"]
    Create --> Write1["Seek(0) → Write 8M"]
    Write1 --> R1["Return 200<br/>wait for next"]

    C2["Chunk 2 arrives<br/>Content-Range: 8M-16M/50M"]
    C2 --> Write2["Seek(8M) → Write 8M"]
    Write2 --> R2["Return 200"]

    CN["Final chunk arrives"]
    CN --> WriteN["Write final bytes"]
    WriteN --> Check{"IsComplete?<br/>ReceivedEnd >= total-1"}
    Check -->|Yes| Final["Finalize:<br/>read temp in 8M blocks<br/>hash + encrypt + store"]
    Check -->|No| RN["Return 200<br/>wait for more"]

    Final --> Cleanup["Delete temp file"]
    Cleanup --> Done["Return 200 + file info"]

    subgraph Failure["Failure Scenarios"]
        F1["Client disconnects<br/>→ temp file orphaned"]
        F2["Token expires (1h)<br/>→ temp file orphaned"]
        F3["Server crash<br/>→ temp file orphaned"]
    end

    style Create fill:#17a2b8,color:#fff
    style Cleanup fill:#28a745,color:#fff
    style F1 fill:#ffc107,color:#000
    style F2 fill:#ffc107,color:#000
    style F3 fill:#ffc107,color:#000
```

---

## 4. ZIP Directory Download

```mermaid
flowchart TD
    Request["Request: download dir as ZIP"]
    Request --> Auth["Auth + permission"]
    Auth --> Preflight["Preflight: traverse tree<br/>count files, depth, bytes"]
    Preflight --> Limits{"Within limits?<br/>100k files, 64 depth, 10GB"}
    Limits -->|No| Reject["400/413: exceeds limit"]
    Limits -->|Yes| Stream["Stream ZIP to client"]

    Stream --> ForFile["For each file in dir"]
    ForFile --> GetBlocks["Get block_ids"]
    GetBlocks --> Enc{"Encrypted?"}
    Enc -->|Yes| LoadDecrypt["Load block → decrypt"]
    Enc -->|No| StreamBlock["Stream from S3"]
    LoadDecrypt --> ZipWrite["Write to ZIP entry<br/>(Store, no compression)"]
    StreamBlock --> ZipWrite
    ZipWrite --> ForFile

    ForFile -->|Done| Close["Close ZIP"]
    Close --> Traffic["Record traffic"]

    style Auth fill:#28a745,color:#fff
    style Preflight fill:#28a745,color:#fff
    style Limits fill:#28a745,color:#fff
    style Reject fill:#dc3545,color:#fff
```

---

## 5. Share Link Download (Public)

```mermaid
flowchart TD
    Public["Public user<br/>GET /d/:token/files/?dl=1"]
    Public --> Lookup["Lookup share token<br/>Check expiry, view limit"]
    Lookup -->|Expired| Gone["410 Gone"]
    Lookup -->|Password| PW["Check password cookie<br/>constant-time compare"]
    PW -->|Wrong| R403["403"]
    Lookup -->|OK| Navigate["Navigate to shared file"]
    PW -->|OK| Navigate

    Navigate --> Blocks["Get block_ids"]
    Blocks --> Resolve["Resolve SHA-1 → SHA-256"]
    Resolve --> Enc{"Encrypted?"}
    Enc -->|Yes| NeedSession{"Decrypt<br/>session active?"}
    NeedSession -->|No| R403b["403: not unlocked"]
    NeedSession -->|Yes| Decrypt["Decrypt blocks"]
    Enc -->|No| Stream["Stream blocks"]

    Decrypt --> Headers["Content-Disposition: inline<br/>Content-Type from ext"]
    Stream --> Headers
    Headers --> Client["Stream to client"]
    Client --> Traffic["Record traffic<br/>against link CREATOR"]

    style Lookup fill:#28a745,color:#fff
    style PW fill:#28a745,color:#fff
    style Traffic fill:#17a2b8,color:#fff
```
