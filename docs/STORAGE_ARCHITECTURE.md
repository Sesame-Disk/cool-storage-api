# Storage Architecture: Classes, Policies, and Multi-Region Design

## Overview

SesameFS uses a **multi-tier, multi-region** storage architecture where:
1. **Storage Classes** define WHERE blocks can be stored (specific backends)
2. **Storage Policies** define HOW to choose which class to use
3. **Database (Cassandra)** tracks WHERE each block was actually stored

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                          STORAGE CLASSES                                     │
│                                                                              │
│  hot-s3-usa          hot-s3-china         hot-s3-africa      hot-disk-local │
│  cold-glacier-usa    cold-glacier-china   cold-disk-archive                 │
│                                                                              │
│  Each class = { name, type, tier, endpoint, bucket, region }                 │
└─────────────────────────────────────────────────────────────────────────────┘
                                  ▲
                                  │ (Policy selects class)
                                  │
┌─────────────────────────────────────────────────────────────────────────────┐
│                          STORAGE POLICIES                                    │
│                                                                              │
│  1. Endpoint Policy: Request from US endpoint → hot-s3-usa                  │
│  2. User Default: User preference → hot-s3-china                            │
│  3. Library Override: Library setting → hot-s3-africa                       │
│  4. Lifecycle: After 90 days inactive → cold-glacier-usa                    │
│                                                                              │
│  Priority: Library > User > Endpoint > Global Default                        │
└─────────────────────────────────────────────────────────────────────────────┘
                                  │
                                  │ (Store block, record location)
                                  ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                     DATABASE (Cassandra - Global)                            │
│                                                                              │
│  blocks table:                                                               │
│  ┌──────────────┬─────────────────┬──────────────────────┬─────────────────┐ │
│  │ block_id     │ storage_class   │ storage_key          │ size_bytes     │ │
│  ├──────────────┼─────────────────┼──────────────────────┼─────────────────┤ │
│  │ abc123...    │ hot-s3-usa      │ blocks/ab/c1/abc123  │ 1048576        │ │
│  │ def456...    │ hot-s3-china    │ blocks/de/f4/def456  │ 2097152        │ │
│  │ ghi789...    │ cold-glacier-usa│ archive/ghi789       │ 4194304        │ │
│  └──────────────┴─────────────────┴──────────────────────┴─────────────────┘ │
│                                                                              │
│  On retrieval: block_id → lookup storage_class → route to correct backend   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Storage Classes

A **Storage Class** is a named configuration for a specific storage backend.

### Naming Convention

```
{tier}-{type}-{region}
```

- **tier**: `hot` (immediate access) or `cold` (delayed access, cheaper)
- **type**: `s3`, `glacier`, `disk`
- **region**: `usa`, `china`, `eu`, `africa`, `local`, etc.

### Examples

| Class Name          | Type    | Tier | Endpoint                          | Bucket/Path           |
|---------------------|---------|------|-----------------------------------|-----------------------|
| `hot-s3-usa`        | S3      | hot  | s3.us-east-1.amazonaws.com        | sesamefs-usa          |
| `hot-s3-china`      | S3      | hot  | s3.cn-north-1.amazonaws.com.cn    | sesamefs-china        |
| `hot-s3-eu`         | S3      | hot  | s3.eu-west-1.amazonaws.com        | sesamefs-eu           |
| `hot-s3-africa`     | S3      | hot  | s3.af-south-1.amazonaws.com       | sesamefs-africa       |
| `hot-disk-local`    | Disk    | hot  | (local filesystem)                | /data/sesamefs        |
| `cold-glacier-usa`  | Glacier | cold | glacier.us-east-1.amazonaws.com   | sesamefs-archive-usa  |
| `cold-glacier-eu`   | Glacier | cold | glacier.eu-west-1.amazonaws.com   | sesamefs-archive-eu   |
| `cold-disk-archive` | Disk    | cold | (local filesystem)                | /archive/sesamefs     |

### Configuration

```yaml
storage:
  classes:
    hot-s3-usa:
      type: s3
      tier: hot
      endpoint: "https://s3.us-east-1.amazonaws.com"
      bucket: sesamefs-usa
      region: us-east-1

    hot-s3-china:
      type: s3
      tier: hot
      endpoint: "https://s3.cn-north-1.amazonaws.com.cn"
      bucket: sesamefs-china
      region: cn-north-1

    cold-glacier-usa:
      type: glacier
      tier: cold
      endpoint: "https://glacier.us-east-1.amazonaws.com"
      vault: sesamefs-archive-usa
      region: us-east-1

    hot-disk-local:
      type: disk
      tier: hot
      path: /data/sesamefs/blocks
```

---

## Storage Policies

**Policies** determine which storage class to use when storing a new block.

### Policy Priority (highest to lowest)

1. **Library Override** - Specific library configured to use a storage class
2. **Endpoint/Region** - Based on which API endpoint received the request (**default**)
3. **Organization Default** - Organization-level default
4. **Global Default** - System-wide fallback

**FUTURE: User-Selectable Storage**
```
In the future, users may be able to choose where to store their files:
- User preference: "Store my files in EU for GDPR compliance"
- Per-library: "This library should use China storage"
- This would be inserted between Library Override and Endpoint/Region
```

### Policy Resolution Flow

```
                    ┌───────────────────────────┐
                    │     Incoming Upload       │
                    │  (block data + context)   │
                    └─────────────┬─────────────┘
                                  │
                    ┌─────────────▼─────────────┐
                    │  Library has override?    │
                    └─────────────┬─────────────┘
                          yes │         │ no
                    ┌─────────▼───┐     │
                    │ Use library │     │
                    │   class     │     │
                    └─────────────┘     │
                                        │
                    ┌───────────────────▼───────┐
                    │    User has default?      │
                    └─────────────┬─────────────┘
                          yes │         │ no
                    ┌─────────▼───┐     │
                    │  Use user   │     │
                    │   default   │     │
                    └─────────────┘     │
                                        │
                    ┌───────────────────▼───────┐
                    │  Map endpoint to region   │
                    │  (us.api.com → usa)       │
                    └─────────────┬─────────────┘
                                  │
                    ┌─────────────▼─────────────┐
                    │  Find hot class for       │
                    │  region: hot-s3-usa       │
                    └─────────────┬─────────────┘
                                  │
                    ┌─────────────▼─────────────┐
                    │  Store block + record     │
                    │  storage_class in DB      │
                    └───────────────────────────┘
```

### Endpoint-to-Region Mapping

```yaml
policies:
  endpoint_regions:
    "us.sesamefs.com": "usa"
    "eu.sesamefs.com": "eu"
    "cn.sesamefs.com": "china"
    "*.sesamefs.com": "usa"  # Default fallback

  region_classes:
    usa:
      hot: "hot-s3-usa"
      cold: "cold-glacier-usa"
    eu:
      hot: "hot-s3-eu"
      cold: "cold-glacier-eu"
    china:
      hot: "hot-s3-china"
      cold: "cold-glacier-china"
```

---

## Database Schema

### Blocks Table (Updated)

```cql
CREATE TABLE blocks (
    org_id UUID,
    block_id TEXT,               -- SHA-256 hash (content-addressable)
    storage_class TEXT,          -- e.g., "hot-s3-usa", "cold-glacier-eu"
    storage_key TEXT,            -- Full path/key within the backend
    size_bytes BIGINT,
    ref_count INT,               -- Reference counting for GC
    created_at TIMESTAMP,
    last_accessed TIMESTAMP,
    PRIMARY KEY ((org_id), block_id)
);

-- Index for finding blocks by storage class (for lifecycle/migration)
CREATE INDEX blocks_by_class ON blocks (storage_class);
```

### Block Retrieval Flow

```
1. Client requests block: GET /seafhttp/repo/{repo_id}/block/{block_id}

2. Server looks up in Cassandra:
   SELECT storage_class, storage_key FROM blocks
   WHERE org_id = ? AND block_id = ?

   → Returns: storage_class = "hot-s3-usa", storage_key = "blocks/ab/c1/abc123"

3. Server selects storage backend by class name:
   backend = storageManager.GetBackend("hot-s3-usa")

4. Server retrieves from backend:
   data = backend.Get(ctx, storage_key)

5. Return data to client
```

---

## Lifecycle Policies

Blocks can be migrated between storage classes based on access patterns:

```yaml
lifecycle:
  rules:
    - name: "Move to cold after 90 days"
      condition:
        last_accessed_days_ago: 90
        current_tier: hot
      action:
        move_to_tier: cold
        # hot-s3-usa → cold-glacier-usa (same region)

    - name: "Delete if unused for 1 year"
      condition:
        last_accessed_days_ago: 365
        ref_count: 0
      action:
        delete: true
```

### Migration Process

```
1. Lifecycle worker scans blocks table
2. Find blocks matching condition (e.g., last_accessed > 90 days ago)
3. For each block:
   a. Download from current storage class
   b. Upload to target storage class
   c. Update blocks table: storage_class, storage_key
   d. Delete from old storage class
```

---

## Testing with MinIO

To test multi-region storage locally, we can use MinIO with multiple buckets:

### Setup

```yaml
# docker-compose.yml
services:
  minio:
    image: minio/minio
    command: server /data --console-address ":9001"
    ports:
      - "9000:9000"
      - "9001:9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    volumes:
      - minio-data:/data

# Create buckets to simulate regions
# mc mb local/sesamefs-usa
# mc mb local/sesamefs-eu
# mc mb local/sesamefs-china
# mc mb local/sesamefs-archive
```

### Test Configuration

```yaml
storage:
  classes:
    # All point to same MinIO, different buckets
    hot-s3-usa:
      type: s3
      tier: hot
      endpoint: "http://localhost:9000"
      bucket: sesamefs-usa
      region: us-east-1
      access_key: minioadmin
      secret_key: minioadmin

    hot-s3-china:
      type: s3
      tier: hot
      endpoint: "http://localhost:9000"
      bucket: sesamefs-china
      region: cn-north-1
      access_key: minioadmin
      secret_key: minioadmin

    cold-glacier-usa:
      type: s3  # MinIO simulates Glacier
      tier: cold
      endpoint: "http://localhost:9000"
      bucket: sesamefs-archive
      region: us-east-1
      access_key: minioadmin
      secret_key: minioadmin
```

### Test Scenarios

1. **Region-based routing**:
   - Request from `X-Forwarded-Host: us.sesamefs.com` → blocks go to `sesamefs-usa` bucket
   - Request from `X-Forwarded-Host: cn.sesamefs.com` → blocks go to `sesamefs-china` bucket

2. **User override**:
   - User sets default region to "china"
   - All uploads go to `sesamefs-china` regardless of endpoint

3. **Cross-region retrieval**:
   - Block stored in `hot-s3-china`
   - User in USA requests it
   - System looks up storage_class in Cassandra → routes to China backend

4. **Lifecycle migration**:
   - Block in `hot-s3-usa`, last accessed 91 days ago
   - Worker moves it to `cold-glacier-usa` (sesamefs-archive bucket)
   - Updates Cassandra: storage_class = "cold-glacier-usa"

---

## File-Level Storage Consistency

When a file is uploaded, **ALL blocks of that file** must use the same storage class. The policy is evaluated once at the start of the upload session, not per-block.

### Upload Session Flow (with HA)

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        FILE UPLOAD SESSION                                   │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  1. Client starts upload (recv-fs, then blocks)                             │
│                                                                              │
│  2. Server evaluates policy ONCE:                                           │
│     - Library override? → use it                                            │
│     - Endpoint region? → use it (default)                                   │
│     → Preferred: storage_class = "hot-s3-usa"                               │
│                                                                              │
│  3. Check backend health:                                                   │
│     ┌─────────────────────────────────────────────────────────────────────┐ │
│     │ IF hot-s3-usa is DOWN:                                              │ │
│     │   → Use failover: hot-s3-usa-west (same region, different AZ)      │ │
│     │   → OR use hot-s3-eu (cross-region failover)                        │ │
│     │   → Decision is FINAL for this session                              │ │
│     └─────────────────────────────────────────────────────────────────────┘ │
│                                                                              │
│  4. Session stores: { session_id, storage_class: "hot-s3-eu" } (failover)   │
│                                                                              │
│  5. ALL blocks in this session use "hot-s3-eu" CONSISTENTLY:                │
│     Block 1 → hot-s3-eu                                                     │
│     Block 2 → hot-s3-eu                                                     │
│     Block 3 → hot-s3-eu                                                     │
│     ...                                                                      │
│                                                                              │
│  6. Commit records file→blocks mapping + storage_class                      │
│                                                                              │
│  NOTE: Failover decision is made ONCE at session start, NOT per-block.      │
│        This ensures all blocks of a file are in the same location.          │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Implementation: Upload Session

```go
// UploadSession tracks storage decisions for a file upload
type UploadSession struct {
    SessionID     string
    RepoID        string
    StorageClass  string    // Resolved once at session start
    CreatedAt     time.Time
    ExpiresAt     time.Time
}

// On recv-fs or first block upload:
func (h *SyncHandler) getOrCreateSession(ctx context.Context, repoID, userID string) (*UploadSession, error) {
    // Check for existing session
    session := h.sessionStore.Get(repoID, userID)
    if session != nil {
        return session, nil
    }

    // Evaluate policy to determine storage class
    storageClass := h.policyEngine.ResolveStorageClass(ctx, PolicyContext{
        OrgID:     ctx.Value("org_id").(string),
        UserID:    userID,
        RepoID:    repoID,
        Endpoint:  ctx.Value("endpoint").(string),
    })

    session = &UploadSession{
        SessionID:    generateSessionID(),
        RepoID:       repoID,
        StorageClass: storageClass,
        CreatedAt:    time.Now(),
        ExpiresAt:    time.Now().Add(1 * time.Hour),
    }

    h.sessionStore.Put(session)
    return session, nil
}

// All blocks use session's storage class
func (h *SyncHandler) PutBlock(c *gin.Context) {
    session, _ := h.getOrCreateSession(ctx, repoID, userID)

    // Store to the session's storage class
    backend := h.storageManager.GetBackend(session.StorageClass)
    backend.Put(ctx, blockID, data)

    // Record in database with storage class
    h.db.InsertBlock(blockID, session.StorageClass, storageKey)
}
```

### Cross-Session Deduplication

What if the same block is uploaded in different sessions with different storage classes?

**First-Write Wins (Current Implementation)**
```
Session 1 (USA): Block abc123 → stored in hot-s3-usa, recorded in DB
Session 2 (China): Block abc123 → DB lookup finds it exists in hot-s3-usa
                   → Skip upload, reference existing block
                   → China user retrieves from USA (cross-region)
```

This is simpler and ensures true deduplication. Cross-region retrieval has higher latency but saves storage costs.

**FUTURE: Regional Block Cache (Good to Have)**
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                     REGIONAL BLOCK CACHE (Future)                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Scenario: Block abc123 stored in hot-s3-usa, frequently accessed from China│
│                                                                              │
│  1. China user requests block abc123                                        │
│  2. DB lookup → primary location: hot-s3-usa                                │
│  3. Fetch from USA, serve to user                                           │
│  4. Background: Cache copy to hot-s3-china (if access count > threshold)    │
│  5. DB records: abc123 → [hot-s3-usa (primary), hot-s3-china (cache)]       │
│  6. Next China request → serve from hot-s3-china (local cache)              │
│                                                                              │
│  Cache eviction: LRU or time-based (e.g., 30 days unused)                   │
│  Cache is read-only replica, primary is source of truth                     │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

This is a future enhancement to improve cross-region latency for frequently accessed blocks.

---

## High Availability (HA)

### Database HA (Cassandra)

Cassandra is inherently HA with multi-DC replication:
```
        ┌─────────────────────────────────────────────────────────────┐
        │                     CASSANDRA CLUSTER                        │
        │                                                              │
        │    DC: us-east-1         DC: eu-west-1        DC: cn-north-1│
        │    ┌─────────────┐       ┌─────────────┐      ┌────────────┐ │
        │    │ Node 1      │       │ Node 1      │      │ Node 1     │ │
        │    │ Node 2      │  ←──► │ Node 2      │ ←──► │ Node 2     │ │
        │    │ Node 3      │       │ Node 3      │      │ Node 3     │ │
        │    └─────────────┘       └─────────────┘      └────────────┘ │
        │                                                              │
        │    Replication Factor: 3 per DC                              │
        │    Write Consistency: LOCAL_QUORUM (2 of 3 in local DC)      │
        │    Read Consistency: LOCAL_QUORUM                             │
        └─────────────────────────────────────────────────────────────┘
```

If a Cassandra node fails, queries automatically route to other nodes.

### Storage Backend HA

Each storage backend should have its own HA strategy:

**S3 (AWS/MinIO)**:
- AWS S3: Built-in HA, 99.99% availability SLA
- MinIO: Deploy as distributed cluster (4+ nodes)

**For SesameFS, we handle backend failures at the application layer:**

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        STORAGE BACKEND HA                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Storage Class: hot-s3-usa                                                  │
│                                                                              │
│  Primary Endpoint: s3.us-east-1.amazonaws.com                               │
│  Failover Endpoints:                                                         │
│    - s3.us-west-2.amazonaws.com (cross-region failover)                     │
│    - s3-fips.us-east-1.amazonaws.com (FIPS endpoint)                        │
│                                                                              │
│  On primary failure:                                                         │
│  1. Retry with exponential backoff                                          │
│  2. If persistent failure, try failover endpoint                            │
│  3. If all fail, return error (don't silently use different region)         │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Backend Health Monitoring

```go
type StorageBackend struct {
    Name           string
    Endpoints      []Endpoint  // Primary + failovers
    HealthStatus   HealthStatus
    LastHealthCheck time.Time
}

type HealthStatus int
const (
    Healthy HealthStatus = iota
    Degraded  // Slow responses, retry succeeds
    Unhealthy // Primary down, using failover
    Failed    // All endpoints down
)

// Background health checker
func (sm *StorageManager) healthCheckLoop() {
    for {
        for _, backend := range sm.backends {
            status := sm.checkBackendHealth(backend)
            if status != backend.HealthStatus {
                sm.updateBackendStatus(backend.Name, status)
                sm.notifyHealthChange(backend.Name, status)
            }
        }
        time.Sleep(30 * time.Second)
    }
}
```

### Handling Backend Outages

**Scenario: hot-s3-usa is down**

```
UPLOAD (writes):
┌─────────────────────────────────────────────────────────────────────────────┐
│ 1. Policy selects hot-s3-usa                                                │
│ 2. Backend health check: FAILED                                             │
│ 3. Options:                                                                 │
│    a. Fail upload immediately (strict region compliance)                   │
│    b. Use failover class (hot-s3-usa-failover → us-west-2)                 │
│    c. Queue upload, retry when backend recovers                            │
│                                                                              │
│ Recommended: Option (b) with failover within same region/compliance zone    │
└─────────────────────────────────────────────────────────────────────────────┘

DOWNLOAD (reads):
┌─────────────────────────────────────────────────────────────────────────────┐
│ 1. Lookup block in Cassandra → storage_class = "hot-s3-usa"                │
│ 2. Try primary endpoint → FAILED                                           │
│ 3. Try failover endpoints for hot-s3-usa                                   │
│ 4. If all fail → Return error 503 (not silently serve from different region)│
│                                                                              │
│ Note: Never serve USA data from China backend (data sovereignty)            │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Configuration

```yaml
storage:
  classes:
    hot-s3-usa:
      type: s3
      tier: hot
      endpoints:
        - url: "https://s3.us-east-1.amazonaws.com"
          priority: 1  # Primary
        - url: "https://s3.us-west-2.amazonaws.com"
          priority: 2  # Failover
      bucket: sesamefs-usa
      region: us-east-1

      health_check:
        interval: 30s
        timeout: 5s
        unhealthy_threshold: 3  # Failures before marking unhealthy

      failover:
        mode: "same-region"  # Only failover within same region/compliance zone
        fallback_class: "hot-s3-usa-west"  # Explicit failover class
```

---

## Implementation Checklist

### Phase 1: Storage Classes & Registry
- [ ] Define `StorageClass` struct (name, type, tier, endpoints, bucket, region)
- [ ] Create `StorageClassRegistry` to manage multiple backends
- [ ] Load classes from config at startup
- [ ] Add health check for each backend
- [ ] Add failover endpoint support per class

### Phase 2: Policy Engine
- [ ] Define `StoragePolicy` interface
- [ ] Implement endpoint-to-region mapping (hostname → region)
- [ ] Add library storage override (libraries table: `storage_class` column)
- [ ] Create policy resolver with priority chain
- [ ] Integrate health check into policy resolution (failover if backend down)

### Phase 3: Upload Session Management
- [ ] Create `UploadSession` struct (session_id, repo_id, storage_class, created_at)
- [ ] Store sessions in Cassandra (for cross-server consistency)
- [ ] Resolve storage class ONCE at session start (with failover)
- [ ] All blocks in session use the resolved storage class

### Phase 4: Block Storage Update
- [ ] Update `PutBlock` to use session's storage class
- [ ] Store `storage_class` in blocks table
- [ ] Update `GetBlock` to lookup storage_class and route to correct backend
- [ ] Implement first-write-wins deduplication (check DB before storing)

### Phase 5: MinIO Multi-Bucket Testing
- [ ] Create docker-compose with MinIO
- [ ] Create buckets: sesamefs-usa, sesamefs-eu, sesamefs-china, sesamefs-archive
- [ ] Configure test storage classes pointing to different buckets
- [ ] Write integration tests:
  - [ ] Endpoint-based routing (hostname → bucket)
  - [ ] Library override
  - [ ] Backend failover
  - [ ] Cross-region retrieval (block in USA, request from China endpoint)
  - [ ] Deduplication (same block uploaded twice)

### Phase 6: Lifecycle & Migration
- [ ] Implement lifecycle worker (background job)
- [ ] Add migration between storage classes (hot → cold)
- [ ] Handle Glacier restore workflow

### Future Enhancements (Good to Have)
- [ ] User-selectable storage preference
- [ ] Regional block cache (replicate frequently accessed blocks)
- [ ] Per-organization storage quotas per region
- [ ] Storage cost analytics

---

## Key Insight: Why Cassandra?

The reason for using a **globally-replicated database (Cassandra)** is critical:

```
User in USA uploads file → Blocks stored in hot-s3-usa
                           ↓
                        Cassandra records: block_id → "hot-s3-usa"
                           ↓
                        Cassandra replicates to all DCs
                           ↓
User in China downloads → Looks up in local Cassandra DC
                           ↓
                        Finds: block_id → "hot-s3-usa"
                           ↓
                        Routes request to USA S3 (cross-region)
```

Without global replication, a user in China wouldn't know that their block is in USA. The Cassandra lookup is what enables routing to the correct backend.
