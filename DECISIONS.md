# Architecture Decisions

This document tracks architectural decisions for SesameFS.

---

## Confirmed Decisions

### Decision 1: Database - Apache Cassandra 5.0

**Status**: CONFIRMED

**Choice**: Apache Cassandra 5.0

**Rationale**:
- **Apache 2.0 License** - Fully permissive, no restrictions at any scale
- **Global Distribution** - NetworkTopologyStrategy for multi-DC replication
- **Self-Healing** - Automatic repair with tunable consistency (LOCAL_QUORUM)
- **Battle-Tested** - Netflix, Apple, Discord, Instagram scale
- **Excellent Documentation** - Massive community and resources

**Rejected Alternatives**:
- ScyllaDB - License changed to Source Available (Dec 2024), free tier limited to 50 vCPU/10 TB
- CockroachDB - BSL license, restrictive for commercial use
- FoundationDB - Smaller community, steeper learning curve

---

### Decision 2: Chunking Algorithm - FastCDC

**Status**: CONFIRMED

**Choice**: FastCDC with SHA-256

**Rationale**:
- **10x faster** than Seafile's Rabin CDC
- **Same deduplication ratio** as Rabin-based approaches
- **SHA-256** instead of SHA-1 (more secure)
- **Variable chunk sizes** (512KB-8MB) for better deduplication
- **Per-tenant polynomial** for security (prevents cross-tenant attacks)

**Configuration**:
```yaml
chunking:
  algorithm: fastcdc
  min_size: 524288      # 512 KB
  avg_size: 2097152     # 2 MB
  max_size: 8388608     # 8 MB
  hash_algorithm: sha256
```

**Reference Implementation**: [github.com/restic/chunker](https://github.com/restic/chunker)

---

### Decision 3: Client Compatibility - Seafile Protocol

**Status**: CONFIRMED & IMPLEMENTED

**Choice**: Implement Seafile sync protocol (`/seafhttp/`) for client compatibility

**Rationale**:
- Leverage existing Seafile desktop/mobile apps
- iOS app is Apache 2.0 licensed
- Desktop and Android are GPLv3 (usable as clients)
- Reduces time-to-market significantly

**Implementation**: ✅ Complete - Desktop clients can sync via `/seafhttp/` endpoints

---

### Decision 4: Storage Strategy - Block-Based with S3

**Status**: CONFIRMED

**Choice**: Content-addressable block storage on S3 with Glacier tiering

**Architecture**:
```
File → FastCDC Chunks → SHA-256 Hash → S3 (hot) → Glacier (cold)
```

**Key Features**:
- Blocks stored by hash (deduplication)
- Blocks can tier to Glacier automatically
- Reference counting for garbage collection
- Per-tenant isolation (optional cross-tenant dedup)

---

### Decision 5: Authentication - OIDC (Lower Priority in Phase 1)

**Status**: CONFIRMED

**Choice**: OIDC with accounts.sesamedisk.com, implemented later in Phase 1

**Rationale**:
- Focus on core storage functionality first
- Use dev tokens for initial development
- OIDC integration comes after basic file operations work

**Dev Mode Config**:
```yaml
auth:
  dev_mode: true
  dev_tokens:
    - token: "dev-token-123"
      user_id: "00000000-0000-0000-0000-000000000001"
```

---

### Decision 6: Phase 1 Priority Order

**Status**: CONFIRMED (updated December 2025)

**Completed**:
- [x] Project structure and Go modules
- [x] Configuration management (Viper)
- [x] Cassandra connection and schema
- [x] Library CRUD operations
- [x] Block storage layer (S3)
- [x] Seafile sync protocol (`/seafhttp/`) - Desktop client compatible
- [x] Multi-region storage with nginx failover
- [x] SHA-1 → SHA-256 block ID mapping
- [x] Container-based testing infrastructure

**Remaining**:
- [ ] File operations with commit creation (for web UI)
- [ ] Directory operations with commit creation
- [ ] Share links (basic)
- [ ] OIDC authentication integration
- [ ] Glacier integration (upload + restore)
- [ ] FastCDC chunking implementation (currently using fixed blocks)

---

### Decision 7: Multi-Tenancy Model

**Status**: CONFIRMED

**Choice**: Logical separation initially, with configurable per-customer isolation in Phase 2

**Phase 1 - Logical Separation**:
- Single Cassandra keyspace, data partitioned by `org_id`
- Shared S3 bucket with org-based prefixes: `s3://sesamefs-blocks/{org_id}/`
- Per-tenant chunking polynomial for security
- No cross-tenant deduplication (isolation by default)

**Phase 2 - Configurable Per-Customer Isolation**:
- Allow specific customers to configure:
  - Dedicated S3 buckets
  - Different storage classes per customer
  - Different regions for data residency compliance
  - Custom lifecycle policies
- Configuration stored in `organizations` table:
  ```cql
  storage_config MAP<TEXT, TEXT>  -- bucket, region, storage_class, etc.
  ```

**Example Phase 2 Config**:
```yaml
organizations:
  - org_id: "acme-corp"
    storage:
      bucket: "acme-dedicated-bucket"      # Their own bucket
      region: "eu-west-1"                   # EU data residency
      storage_class: "GLACIER_IR"           # Cost optimization
  - org_id: "startup-xyz"
    storage:
      bucket: "sesamefs-blocks"             # Shared bucket
      prefix: "startup-xyz/"                # Logical separation
      region: "us-east-1"
```

---

### Decision 12: Runtime Versions

**Status**: CONFIRMED

**Current Versions** (as of December 2025):

| Component | Version | Notes |
|-----------|---------|-------|
| **Go** | 1.25.5 | Latest stable (Dec 2, 2025) |
| **Debian** | Trixie 13 slim | `debian:trixie-slim` (stable Aug 2025) |
| **Cassandra** | 5.0.6 | Latest (Oct 29, 2025) |
| **gocql driver** | v2.0.0 | Apache official driver |
| **aws-sdk-go-v2** | v1.41.0 | Latest (Dec 8, 2025) |
| **Gin** | v1.10.0 | HTTP framework |
| **godotenv** | v1.5.1 | .env file loading |
| **yaml.v3** | v3.0.1 | YAML config parsing |

**Dockerfile Base**:
```dockerfile
FROM golang:1.25-trixie AS builder
FROM debian:trixie-slim AS runtime
```

**Go Module Requirements**:
```go
go 1.25

require (
    github.com/apache/cassandra-gocql-driver/v2 v2.0.0
    github.com/aws/aws-sdk-go-v2 v1.41.0
    github.com/aws/aws-sdk-go-v2/config v1.29.0
    github.com/aws/aws-sdk-go-v2/service/s3 v1.76.0
    github.com/aws/aws-sdk-go-v2/service/glacier v1.28.0
    github.com/gin-gonic/gin v1.10.0
    github.com/joho/godotenv v1.5.1
    gopkg.in/yaml.v3 v3.0.1
)
```

---

### Decision 8: Project Name - SesameFS

**Status**: CONFIRMED

**Choice**: SesameFS

**Rationale**: Consistent with sesamedisk.com branding, clear file system connotation.

---

### Decision 9: License - MIT (Initial)

**Status**: CONFIRMED (may change)

**Choice**: MIT License initially

**Notes**:
- Open source from the start
- MIT is simple and permissive
- May transition to different license later based on business needs
- Core will remain open source

---

### Decision 10: Versioning Strategy - All Versions with TTL

**Status**: CONFIRMED

**Choice**: Keep all versions with configurable TTL per library

**Implementation**:
- Every file change creates a new version
- Versions stored as separate commits (Git-like model)
- TTL configurable per library (default: 90 days)
- Expired versions eligible for garbage collection
- Option to keep versions indefinitely (TTL = 0)

**Schema**:
```cql
-- In libraries table
version_ttl_days INT,           -- 0 = keep forever, default 90

-- Versions are stored in commits table with created_at timestamp
-- GC job deletes commits older than TTL
```

**Configuration**:
```yaml
versioning:
  default_ttl_days: 90          # Default for new libraries
  min_ttl_days: 7               # Minimum allowed TTL
  gc_interval: 24h              # How often to run version cleanup
```

---

### Decision 13: Multi-Region Storage Architecture

**Status**: CONFIRMED

**Choice**: Hostname-based routing with nginx load balancer and automatic failover

**Architecture**:
```
Client → nginx (hostname routing) → Regional SesameFS instance → Regional S3 bucket
                                  ↘ Failover to other region if primary fails
```

**Key Features**:
- `us.sesamefs.local` → USA backend (primary: sesamefs-usa bucket)
- `eu.sesamefs.local` → EU backend (primary: sesamefs-eu bucket)
- Automatic failover at nginx level
- Shared Cassandra for metadata (multi-DC replication)
- Each region can fail over to other region's storage

**Configuration**:
```yaml
storage:
  classes:
    hot-primary:
      bucket: "sesamefs-usa"
    hot-failover:
      bucket: "sesamefs-eu"
```

**Documentation**: See [MULTIREGION-TESTING.md](docs/MULTIREGION-TESTING.md)

---

### Decision 14: SHA-1 to SHA-256 Block ID Mapping

**Status**: CONFIRMED

**Choice**: Store blocks with SHA-256 internally, translate SHA-1 from Seafile clients

**Rationale**:
- SHA-256 is more secure for content addressing
- Seafile clients use SHA-1 (40 chars) - can't change client behavior
- Transparent translation via `block_id_mappings` table

**Schema**:
```cql
CREATE TABLE block_id_mappings (
    org_id UUID,
    external_id TEXT,    -- SHA-1 from Seafile client (40 chars)
    internal_id TEXT,    -- SHA-256 used in storage (64 chars)
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), external_id)
);
```

**Flow**:
1. Client uploads block with SHA-1 ID
2. Server computes SHA-256 of content
3. Stores block under SHA-256 key
4. Creates mapping: SHA-1 → SHA-256
5. Client requests block by SHA-1, server translates and fetches

---

## Open Decisions

---

### Decision 15: Migration Strategy (Seafile → SesameFS)

**Status**: OPEN

**Context**: For users with existing Seafile deployments (e.g., 20TB multi-tenant), need a migration path with minimal downtime.

**Options**:
1. **Lazy migration** - Read from Seafile storage, copy to SesameFS on access
2. **Bulk migration** - Offline migration with maintenance window
3. **Shadow mode** - Run both systems, compare responses, gradual cutover

**Current Recommendation**: Lazy migration with shadow mode validation

**Documentation**: See [MIGRATION-FROM-SEAFILE.md](docs/MIGRATION-FROM-SEAFILE.md)

---

### Decision 11: Encryption Strategy

**Status**: DEFERRED TO PHASE 4

**Options**:
1. Server-side only (S3 SSE) - Simplest
2. Client-side encryption - Zero-knowledge, like Seafile
3. Both - Let users choose

**Current Plan**: Start with S3 SSE, add client-side in Phase 4

---

## Technical Notes

### Why Not ScyllaDB?

As of December 2024, ScyllaDB changed from AGPL to a "Source Available" license:
- Free tier limited to **50 vCPU and 10 TB** per organization
- Beyond that requires commercial license
- This makes it unsuitable for a scaling cloud storage business

Source: [ScyllaDB License Change Announcement](https://www.scylladb.com/2024/12/18/why-were-moving-to-a-source-available-license/)

### Why FastCDC over Rabin?

| Metric | Rabin CDC | FastCDC |
|--------|-----------|---------|
| Speed | Baseline | **10x faster** |
| Dedup Ratio | Excellent | Excellent (same) |
| Implementation | Complex | Simpler |

FastCDC achieves speed through:
1. Gear-based rolling hash (faster than Rabin)
2. Cut-point skipping (skip sub-minimum chunks)
3. Normalized chunking (consistent size distribution)

### Seafile Sync Protocol Complexity

The `/seafhttp/` protocol is undocumented but reverse-engineerable:
- Git-like commit/tree model
- Block-based file storage
- State machine: init → check → commit → fs → data → update-branch

Implementation requires studying:
- [seafile-server fileserver code](https://github.com/haiwen/seafile-server/tree/master/fileserver)
- [seafile client sync code](https://github.com/haiwen/seafile/tree/master/daemon)

---

## Change Log

| Date | Decision | Status |
|------|----------|--------|
| 2025-12-26 | Database: Cassandra 5.0.6 | Confirmed |
| 2025-12-26 | Chunking: FastCDC | Confirmed |
| 2025-12-26 | Clients: Seafile-compatible | Confirmed |
| 2025-12-26 | Storage: Block-based S3 | Confirmed |
| 2025-12-26 | Auth: OIDC (lower priority) | Confirmed |
| 2025-12-26 | Phase 1 priorities | Confirmed |
| 2025-12-26 | Multi-tenancy: Logical → Per-customer | Confirmed |
| 2025-12-26 | Runtime versions: Go 1.25.5, Debian Trixie | Confirmed |
| 2025-12-26 | Project name: SesameFS | Confirmed |
| 2025-12-26 | License: MIT (initial) | Confirmed |
| 2025-12-26 | Versioning: All versions with TTL | Confirmed |
| 2025-12-29 | Multi-region architecture with nginx failover | Confirmed |
| 2025-12-29 | SHA-1 → SHA-256 block ID mapping | Confirmed |
| 2025-12-29 | Phase 1 priorities updated (sync protocol complete) | Updated |
| 2025-12-29 | Migration strategy from Seafile | Open |
