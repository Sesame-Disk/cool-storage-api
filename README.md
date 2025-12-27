# WIP: SesameFS - Enterprise File Storage Platform

> A modern, flexible, enterprise-grade file storage and sync platform built in Go. Inspired by Seafile Pro but designed for multi-cloud storage with support for immediate (S3/Disk) and archival (Glacier) storage classes. 

Notice: Test it at your own risk and create issues here. The project is somewhat AI slop, but we will get it to be better over time with Claude's help xD.

## Project Vision

SesameFS aims to be a world-class replacement for enterprise file sync and share (EFSS) solutions with these key differentiators:

1. **Smart Two-Tier Storage**: Unlike Seafile which only supports immediate-access storage, SesameFS uses intelligent policies:
   - **Hot Storage**: Immediate access for active files (auto-selects S3 Standard or IA based on access patterns)
   - **Cold Storage**: Cost-effective archival (auto-selects Glacier Instant or Deep Archive based on retention age)

2. **Distributed-First Database**: Global Cassandra cluster with tunable consistency for worldwide deployments

3. **Modern Authentication**: OIDC-native with accounts.sesamedisk.com as primary provider

4. **True Multi-Tenancy**: Complete tenant isolation with per-tenant storage backends

5. **Seafile Client Compatible**: Works with existing Seafile desktop and mobile apps

---

## Technology Stack

| Component | Technology | Version | Rationale |
|-----------|------------|---------|-----------|
| **Language** | Go | 1.25.5 | Performance, concurrency, single binary deployment |
| **Database** | Apache Cassandra | 5.0.6 | Apache 2.0 license, global distribution, Netflix/Apple scale |
| **Cache** | Redis Cluster | (Future) | Session management, hot metadata caching |
| **Object Storage** | S3-compatible | - | AWS S3, MinIO, any S3-compatible storage |
| **Archive Storage** | AWS Glacier | - | Cost-effective long-term archival with restore workflow |
| **Authentication** | OIDC | - | accounts.sesamedisk.com as primary provider |
| **API Framework** | Gin | 1.10.0 | High-performance HTTP routing |
| **Chunking** | FastCDC | - | 10x faster than Rabin CDC, excellent deduplication |
| **Container Base** | Debian Trixie | 13 slim | Minimal, secure runtime (stable Aug 2025) |

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────┐
│                              CLIENTS                                     │
│      Seafile Desktop │ Seafile Mobile │ Web App │ REST API              │
└────────────────────────────────┬────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         SESAMEFS CORE API                                │
├──────────────┬──────────────┬──────────────┬──────────────┬─────────────┤
│  Sync Proto  │   Library    │    File      │    Share     │    Admin    │
│  /seafhttp   │   Service    │   Service    │   Service    │   Service   │
└──────┬───────┴──────┬───────┴──────┬───────┴──────┬───────┴──────┬──────┘
       │              │              │              │              │
       ▼              ▼              ▼              ▼              ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         BLOCK STORAGE LAYER                              │
│         FastCDC Chunking │ SHA-256 Hashing │ Content-Addressable        │
├─────────────────────────────────────────────────────────────────────────┤
│     HOT BLOCKS (S3)     │   COLD BLOCKS (Glacier)   │  LOCAL (Disk)    │
└─────────────────────────────────────────────────────────────────────────┘
                                 │
                                 ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         DATA LAYER                                       │
│              Apache Cassandra (Global Multi-DC Cluster)                  │
│          Metadata │ Users │ Libraries │ Blocks Index │ Shares           │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Block Storage Architecture

### FastCDC Chunking (Improved over Seafile)

| Feature | Seafile | SesameFS |
|---------|---------|----------|
| **Algorithm** | Rabin CDC (slow) | **FastCDC** (10x faster) |
| **Hash** | SHA-1 (160-bit, weak) | **SHA-256** (256-bit, secure) |
| **Chunk Size** | Fixed 1MB/8MB | **Variable 512KB-8MB** (better dedup) |
| **Block Storage** | Local filesystem only | **S3 (hot) + Glacier (cold)** |
| **Security** | Fixed polynomial | **Random polynomial per-tenant** |
| **Cross-tenant Dedup** | Always on (privacy risk) | **Optional, off by default** |

### How It Works

```
File Upload Flow:
┌──────────┐    ┌──────────────┐    ┌─────────────┐    ┌──────────────┐
│  File    │───▶│  FastCDC     │───▶│  SHA-256    │───▶│  S3 Upload   │
│  Input   │    │  Chunking    │    │  Each Block │    │  (if new)    │
└──────────┘    └──────────────┘    └─────────────┘    └──────────────┘
                     │                    │
                     ▼                    ▼
              Variable-size         Block already
              blocks (2-256MB)      exists? Skip!
                                    (deduplication)
```

### Adaptive Chunking (Network-Aware)

SesameFS automatically adjusts chunk sizes based on client network speed for optimal performance across all connection types.

```
Upload Start Sequence:
┌─────────────────────────────────────────────────────────────────────┐
│  1. SPEED PROBE                                                     │
│     Upload 1MB test chunk, measure speed                            │
│     Timeout after 30s → assume very slow connection                 │
├─────────────────────────────────────────────────────────────────────┤
│  2. CALCULATE OPTIMAL CHUNK SIZE                                    │
│     Target: ~8 seconds per chunk                                    │
│                                                                     │
│     Speed Detected    │ Calculated Chunk Size                       │
│     ──────────────────┼─────────────────────                        │
│     500 Kbps (mobile) │ 2 MB (minimum)                              │
│     5 Mbps (home)     │ 5 MB                                        │
│     50 Mbps (office)  │ 50 MB                                       │
│     500 Mbps (DC)     │ 256 MB (maximum)                            │
├─────────────────────────────────────────────────────────────────────┤
│  3. UPLOAD WITH ADAPTATION                                          │
│     • Measure each chunk upload time                                │
│     • Adjust size up/down based on actual speed                     │
│     • 60s timeout per chunk → reduce size 50% and retry             │
│     • Failed chunk → retry with exponential backoff                 │
└─────────────────────────────────────────────────────────────────────┘
```

**Why Adaptive?**
| Tenant Type | Fixed 16MB Chunks | Adaptive Chunks |
|-------------|-------------------|-----------------|
| Mobile (500 Kbps) | 4+ min/chunk, timeouts | 2MB = 32s/chunk, reliable |
| Home (10 Mbps) | 13s/chunk, OK | 10MB = 8s/chunk, optimal |
| Enterprise (100 Mbps) | 1.3s/chunk, too small | 100MB = 8s/chunk, efficient |
| Datacenter (1 Gbps) | 0.1s/chunk, way too small | 256MB = 2s/chunk, minimal overhead |

**Retry Strategy:**
```
Attempt 1: Upload chunk → Fail → Wait 1s
Attempt 2: Retry same size → Fail → Wait 2s
Attempt 3: Reduce size 50% → Retry → Wait 4s
Attempt 4: Retry smaller → Fail → Wait 8s
Attempt 5: Reduce size 50% again → Retry
Attempt 6+: Give up, report error
```

### Smart Storage Tiering

SesameFS uses a simple two-tier model (hot/cold) with intelligent backend selection:

| User Tier | Backend Selection | Access Time | Policy |
|-----------|-------------------|-------------|--------|
| **Hot** | S3 Standard → S3-IA | Milliseconds | Auto-downgrades to IA after 30 days inactive |
| **Cold** | Glacier IR → Deep Archive | Minutes to hours | Auto-downgrades to Deep after 365 days |

Users only choose "hot" or "cold" - the system handles the rest based on access patterns and retention.

---

## Seafile Client Compatibility

SesameFS implements the Seafile sync protocol (`/seafhttp/`) allowing existing clients to work:

### Supported Clients
- **Seafile Desktop** (Windows, macOS, Linux) - GPLv3
- **Seafile iOS** - Apache 2.0
- **Seafile Android** - GPLv3

### Sync Protocol Endpoints

```
/seafhttp/repo/{repo-id}/
├── /commit/HEAD              # Get latest commit
├── /commit/{commit-id}       # Get specific commit
├── /block/{block-id}         # Upload/download block
├── /check-blocks/            # Check which blocks exist
├── /fs/{fs-id}               # File system objects
├── /pack-fs/                 # Pack multiple FS objects
└── /upload-blks-api/         # Batch block upload
```

### Sync State Machine

```
init → check → commit → fs → data → update-branch → finished
```

---

## Core Concepts

### Libraries (Repositories)
A **Library** is the fundamental unit of organization - a collection of files and folders that can be:
- Encrypted (client-side or server-side)
- Shared with users/groups
- Assigned to hot or cold storage
- Versioned with full history
- Synced via Seafile clients

### Storage Policies
Libraries can define when to move files to cold storage:
```yaml
lifecycle:
  move_to_cold_after: 90d  # Move untouched files to cold storage after 90 days
```

Within each tier, the system automatically optimizes costs (hot: Standard→IA, cold: IR→Deep Archive).

---

## API Structure

### Phase 1: Core APIs (MVP)

#### Libraries
- `GET /api/v2/repos` - List all libraries
- `POST /api/v2/repos` - Create library
- `GET /api/v2/repos/{repo_id}` - Get library info
- `DELETE /api/v2/repos/{repo_id}` - Delete library
- `PUT /api/v2/repos/{repo_id}` - Update library settings

#### Files & Directories
- `GET /api/v2/repos/{repo_id}/dir` - List directory contents
- `POST /api/v2/repos/{repo_id}/dir` - Create directory
- `GET /api/v2/repos/{repo_id}/file` - Get file info
- `GET /api/v2/repos/{repo_id}/file/download-link` - Get download URL
- `POST /api/v2/repos/{repo_id}/upload-link` - Get upload URL
- `DELETE /api/v2/repos/{repo_id}/file` - Delete file
- `POST /api/v2/repos/{repo_id}/file/move` - Move file
- `POST /api/v2/repos/{repo_id}/file/copy` - Copy file

#### Authentication (Phase 1, lower priority)
- `POST /api/v2/auth/token` - Get auth token (OIDC flow)
- `POST /api/v2/auth/refresh` - Refresh token
- `DELETE /api/v2/auth/token` - Revoke token
- `GET /api/v2/auth/userinfo` - Get current user info

#### Share Links
- `POST /api/v2/share-links` - Create share link
- `GET /api/v2/share-links` - List share links
- `DELETE /api/v2/share-links/{token}` - Delete share link

#### Cold Storage Operations
- `POST /api/v2/repos/{repo_id}/file/restore` - Initiate restore from Glacier
- `GET /api/v2/repos/{repo_id}/file/restore-status` - Check restore status
- `GET /api/v2/restore-jobs` - List pending restore jobs

### Phase 2: Sync Protocol
- `/seafhttp/*` - Full Seafile sync protocol implementation

### Phase 3: Administration
- User/Group management
- Organization/Tenant management
- Audit logging
- System statistics

---

## Database Schema (Apache Cassandra)

```cql
-- Keyspace with NetworkTopologyStrategy for global distribution
CREATE KEYSPACE sesamefs WITH replication = {
  'class': 'NetworkTopologyStrategy',
  'us-east-1': 3,
  'eu-west-1': 3,
  'ap-southeast-1': 2
};

-- Organizations/Tenants
CREATE TABLE organizations (
    org_id UUID PRIMARY KEY,
    name TEXT,
    settings MAP<TEXT, TEXT>,
    storage_quota BIGINT,
    storage_used BIGINT,
    chunking_polynomial BIGINT,  -- Per-tenant security
    created_at TIMESTAMP
);

-- Users (partitioned by org for multi-tenancy)
CREATE TABLE users (
    org_id UUID,
    user_id UUID,
    email TEXT,
    name TEXT,
    role TEXT,
    oidc_sub TEXT,               -- OIDC subject identifier
    quota_bytes BIGINT,
    used_bytes BIGINT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), user_id)
);

CREATE TABLE users_by_email (
    email TEXT PRIMARY KEY,
    user_id UUID,
    org_id UUID
);

CREATE TABLE users_by_oidc (
    oidc_issuer TEXT,
    oidc_sub TEXT,
    user_id UUID,
    org_id UUID,
    PRIMARY KEY ((oidc_issuer), oidc_sub)
);

-- Libraries
CREATE TABLE libraries (
    org_id UUID,
    library_id UUID,
    owner_id UUID,
    name TEXT,
    description TEXT,
    encrypted BOOLEAN,
    enc_version INT,
    magic TEXT,                  -- For client-side encryption
    random_key TEXT,
    root_commit_id TEXT,
    head_commit_id TEXT,
    storage_class TEXT,
    size_bytes BIGINT,
    file_count BIGINT,
    created_at TIMESTAMP,
    updated_at TIMESTAMP,
    PRIMARY KEY ((org_id), library_id)
);

-- Commits (Git-like history)
CREATE TABLE commits (
    library_id UUID,
    commit_id TEXT,
    parent_id TEXT,
    root_fs_id TEXT,
    creator_id UUID,
    description TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((library_id), commit_id)
);

-- File System Objects (directories and file metadata)
CREATE TABLE fs_objects (
    library_id UUID,
    fs_id TEXT,                  -- SHA-256 of content
    type TEXT,                   -- 'file' or 'dir'
    name TEXT,
    entries LIST<FROZEN<MAP<TEXT, TEXT>>>,  -- For directories
    block_ids LIST<TEXT>,        -- For files
    size_bytes BIGINT,
    mtime BIGINT,
    PRIMARY KEY ((library_id), fs_id)
);

-- Blocks (content-addressable)
CREATE TABLE blocks (
    org_id UUID,
    block_id TEXT,               -- SHA-256 hash
    size_bytes INT,
    storage_class TEXT,
    storage_key TEXT,            -- S3 key or Glacier archive ID
    ref_count INT,               -- Reference counting for GC
    created_at TIMESTAMP,
    last_accessed TIMESTAMP,
    PRIMARY KEY ((org_id), block_id)
);

-- Share Links
CREATE TABLE share_links (
    token TEXT PRIMARY KEY,
    org_id UUID,
    library_id UUID,
    path TEXT,
    created_by UUID,
    permission TEXT,
    password_hash TEXT,
    expires_at TIMESTAMP,
    download_count INT,
    max_downloads INT
);

-- Glacier Restore Jobs
CREATE TABLE restore_jobs (
    org_id UUID,
    job_id UUID,
    library_id UUID,
    block_ids LIST<TEXT>,
    glacier_job_id TEXT,
    status TEXT,                 -- pending, in_progress, completed, failed
    requested_at TIMESTAMP,
    completed_at TIMESTAMP,
    expires_at TIMESTAMP,
    PRIMARY KEY ((org_id), job_id)
);
```

---

## Configuration

```yaml
# config.yaml
server:
  port: 8080
  read_timeout: 30s
  write_timeout: 300s           # Long timeout for large uploads
  max_upload_size: 10GB

auth:
  provider: oidc
  oidc:
    issuer: https://accounts.sesamedisk.com
    client_id: ${OIDC_CLIENT_ID}
    client_secret: ${OIDC_CLIENT_SECRET}
    scopes: [openid, profile, email]
  # Simple token auth for initial development
  dev_mode: true
  dev_tokens:
    - token: "dev-token-123"
      user_id: "00000000-0000-0000-0000-000000000001"

database:
  type: cassandra
  hosts:
    - cassandra-us-east-1:9042
    - cassandra-eu-west-1:9042
    - cassandra-ap-southeast-1:9042
  keyspace: sesamefs
  consistency: LOCAL_QUORUM
  local_dc: us-east-1

chunking:
  algorithm: fastcdc
  hash_algorithm: sha256

  # Adaptive chunk sizing (adjusts to client network speed)
  adaptive:
    enabled: true
    absolute_min: 2097152       # 2 MB floor (terrible connections)
    absolute_max: 268435456     # 256 MB ceiling (datacenter)
    initial_size: 16777216      # 16 MB starting point
    target_seconds: 8           # Target time per chunk upload

  # Speed probe (measures connection before upload)
  probe:
    size: 1048576               # 1 MB probe
    timeout: 30                 # 30 second timeout

  # Timeout and retry
  chunk_timeout: 60             # Abort chunk after 60 seconds
  max_retries: 5
  reduce_on_timeout: 0.5        # Reduce to 50% size on timeout
  reduce_on_failure: 0.5        # Reduce to 50% size on failure

storage:
  default_class: hot
  backends:
    hot:
      type: s3
      endpoint: s3.amazonaws.com
      bucket: sesamefs-blocks
      region: us-east-1
      # Smart policy: Auto-transitions to STANDARD_IA after 30 days inactive
      auto_ia_days: 30
    cold:
      type: glacier
      bucket: sesamefs-blocks-archive
      region: us-east-1
      # Smart policy: Uses Glacier IR initially, Deep Archive after 365 days
      auto_deep_archive_days: 365
      retrieval_tier: Standard  # Standard (3-5 hours) or Bulk (5-12 hours)

lifecycle:
  enabled: true
  check_interval: 1h
  # Move files to cold storage after 90 days of no access
  move_to_cold_days: 90
```

---

## Development Roadmap

### Phase 1: Foundation (MVP) ✅
1. [x] Project structure and Go modules setup
2. [x] Configuration management (YAML + env overrides)
3. [x] Cassandra connection and schema
4. [x] Library CRUD operations
5. [x] S3 storage integration (MinIO compatible)
6. [x] Basic file upload/download via `/seafhttp/`
7. [x] Token-based file access (configurable TTL)
8. [x] FastCDC chunking with adaptive sizing
9. [x] Block storage layer (content-addressable)
10. [x] Block check/upload/download endpoints
11. [x] Distributed token store (Cassandra-backed, stateless)

### 🚀 PRIORITY: Seafile Client Compatibility
**Goal: Test with Seafile Desktop and CLI clients**

```
Immediate (for CLI testing):
├── [x] Add /api2/ legacy route aliases
├── [x] GET /api2/repos/ - List libraries
├── [x] GET /api2/repos/:id/dir/?p=/ - Directory listing  ← CRITICAL
├── [x] GET /api2/auth-token/ - Auth token endpoint
└── [ ] Test with: seaf-cli sync

For Desktop client (sync protocol):
├── [ ] GET /seafhttp/repo/:id/commit/HEAD - Latest commit
├── [ ] GET /seafhttp/repo/:id/commit/:cid - Get commit
├── [ ] POST /seafhttp/repo/:id/check-blocks/ - Check blocks
├── [ ] GET /seafhttp/repo/:id/block/:bid - Download block
├── [ ] POST /seafhttp/repo/:id/recv-fs/ - Receive FS object
├── [ ] GET /seafhttp/repo/:id/fs/:fsid - Get FS object
└── [ ] Commit/FS object model in Cassandra
```

### Phase 2: Stateless Distributed Architecture ✅
```
Completed:
├── [x] Content-addressable block storage (S3)
├── [x] Block deduplication (by SHA256)
├── [x] Distributed token store (Cassandra TTL)
├── [x] Any server can handle any request (stateless)
└── [x] No sticky sessions required

Pending:
├── [ ] POST /api/v2/files/commit - Finalize chunked upload
└── [ ] Upload session tracking (for resume across servers)
```

### Phase 3: Multi-Hostname Multi-Tenancy
**Goal: Multiple domains → Same backend cluster**

```
Architecture:
┌─────────────────────────────────────────────────────────────┐
│  storage.acme.com ──┐                                       │
│  files.globex.io ───┼──► Load Balancer ──► Stateless Pool  │
│  cloud.initech.de ──┘         │                             │
│                               ▼                             │
│              Hostname → Org Middleware                      │
│              storage.acme.com → org: "acme-123"             │
│                               │                             │
│                               ▼                             │
│         S3 (multi-region) ◄── Backend ──► Cassandra (global)│
└─────────────────────────────────────────────────────────────┘

Implementation:
├── [ ] hostname_mappings table in Cassandra
├── [ ] Tenant resolution middleware (hostname → org_id)
├── [ ] URL generation uses request hostname
├── [ ] Per-org storage configuration (S3 regions)
├── [ ] Per-org settings and quotas
└── [ ] Multi-region S3 routing (nearest to user)

Benefits over Seafile:
├── Unlimited hostnames per cluster (vs one per instance)
├── Shared infrastructure, isolated data
├── Global distribution with Cassandra
├── Automatic failover (any server handles any tenant)
└── Per-tenant compliance settings (data residency)
```

### Phase 4: Enterprise Features
- [ ] Directory operations (list, create, delete)
- [ ] File operations (info, delete, move, copy)
- [ ] Quota management per org
- [ ] Admin APIs
- [ ] Audit logging
- [ ] Share links (basic)
- [ ] OIDC authentication integration
- [ ] Glacier integration (upload + restore)

### Phase 5: Security Scanning
**Goal: Detect malware and phishing in uploaded files**

```
Architecture:
┌─────────────────────────────────────────────────────────────────────┐
│  File Upload ──► Pre-scan Queue ──► Security Pipeline              │
│                                            │                        │
│         ┌──────────────────────────────────┼────────────────┐       │
│         │                                  │                │       │
│         ▼                                  ▼                ▼       │
│  ┌─────────────┐   ┌───────────────┐   ┌──────────────────────┐    │
│  │  ClamAV     │   │  YARA Engine  │   │  URL/Link Scanner    │    │
│  │  (TCP)      │   │  (Phishing    │   │  (Safe Browsing,     │    │
│  │  Malware    │   │   Patterns)   │   │   PhishTank)         │    │
│  └─────────────┘   └───────────────┘   └──────────────────────┘    │
│         │                  │                      │                 │
│         └──────────────────┴──────────────────────┘                 │
│                            │                                        │
│                            ▼                                        │
│                   Scan Result → Clean / Quarantine / Reject         │
└─────────────────────────────────────────────────────────────────────┘

ClamAV Integration (Malware):
├── [ ] Connect via TCP (clamd INSTREAM protocol)
├── [ ] Scan on upload before committing to storage
├── [ ] Configurable: block, quarantine, or log-only
├── [ ] Scan queue with retry for clamd failures
├── [ ] Per-org enable/disable setting
└── [ ] Signature update status monitoring

Phishing Detection (Files + Share Links):
├── [ ] YARA rules engine for pattern matching
│       ├── Phishing kit detection (fake login forms)
│       ├── Credential harvesting scripts
│       ├── Malicious macros in Office files
│       └── Suspicious JavaScript in PDFs
├── [ ] URL extraction and scanning
│       ├── Extract links from documents (Office, PDF, HTML)
│       ├── Google Safe Browsing API check
│       ├── PhishTank lookup
│       ├── OpenPhish feed integration
│       └── VirusTotal URL scan (optional, paid)
├── [ ] Office document analysis (oletools)
│       ├── Macro detection and risk scoring
│       ├── Embedded object inspection
│       └── External link detection
├── [ ] PDF analysis (pdfid/pdf-parser)
│       ├── JavaScript detection
│       ├── Auto-open action detection
│       └── Embedded file extraction
└── [ ] Share link abuse prevention
        ├── Monitor download patterns (bulk scraping)
        ├── Geographic anomaly detection
        └── Report/flag suspicious shares

Configuration:
├── ClamAV: host, port, timeout, max file size
├── YARA: rule directories, update frequency
├── URL scanning: API keys, rate limits
├── Actions: block/quarantine/log per threat type
└── Per-org overrides (enterprise can disable)
```

**Why not rspamd for files?**
rspamd is email-focused (headers, SMTP patterns, sender reputation). For file content analysis, YARA rules + oletools + URL scanning provides better coverage. However, we can use rspamd if files are shared via email notifications.

### Phase 6: Office Integration (OnlyOffice/Collabora)
**Goal: Real-time collaborative document editing**

```
Architecture:
┌─────────────────────────────────────────────────────────────────────┐
│  User Browser                                                        │
│       │                                                              │
│       ▼                                                              │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │  SesameFS Web UI                                             │    │
│  │  Load OnlyOffice JS: /onlyoffice/api/documents/api.js        │    │
│  │  Initialize editor in iframe                                 │    │
│  └─────────────────────────────────────────────────────────────┘    │
│       │                      │                                       │
│       ▼                      ▼                                       │
│  ┌──────────────┐    ┌─────────────────────────────────────────┐    │
│  │  SesameFS    │    │  OnlyOffice Document Server              │    │
│  │  API         │◄───│  (or Collabora Online)                   │    │
│  │              │    │                                          │    │
│  │  WOPI Host   │───►│  Fetches doc via GetFile                 │    │
│  │  Endpoints   │◄───│  Saves doc via PutFile callback          │    │
│  └──────────────┘    └─────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘

WOPI Protocol Endpoints (SesameFS implements as WOPI Host):
├── [ ] GET  /wopi/files/:file_id              - CheckFileInfo
│       └── Returns: file name, size, permissions, user info, JWT
├── [ ] GET  /wopi/files/:file_id/contents     - GetFile
│       └── Returns: raw file bytes
├── [ ] POST /wopi/files/:file_id/contents     - PutFile
│       └── Receives: updated file from OnlyOffice
├── [ ] POST /wopi/files/:file_id              - Lock/Unlock/RefreshLock
│       └── Header: X-WOPI-Override = LOCK|UNLOCK|REFRESH_LOCK
├── [ ] POST /wopi/files/:file_id              - PutRelativeFile
│       └── Creates new file (Save As)
└── [ ] POST /wopi/files/:file_id              - RenameFile

Integration Features:
├── [ ] JWT authentication (ONLYOFFICE_JWT_SECRET)
├── [ ] Force save on button press (not just on close)
├── [ ] Auto-save interval configuration
├── [ ] Co-authoring with real-time sync
├── [ ] File locking during edit
├── [ ] Document conversion (doc → docx, etc.)
└── [ ] Mobile editing support

Supported File Types:
├── View/Edit: docx, xlsx, pptx
├── View only: doc, xls, ppt, odt, ods, odp
└── Convert on open: doc → docx, xls → xlsx, ppt → pptx

Configuration:
├── ONLYOFFICE_URL: Document Server URL
├── ONLYOFFICE_JWT_SECRET: Shared secret for JWT
├── ONLYOFFICE_FORCE_SAVE: Enable save button
└── ONLYOFFICE_MAX_SIZE: Max document size (default 100MB)

Alternative: Collabora Online (LibreOffice-based)
├── Same WOPI protocol, different document server
├── Better compatibility with ODF formats
└── Can run both and let users choose
```

### Phase 7: Advanced
- [ ] Search (Elasticsearch)
- [ ] Thumbnails and previews
- [ ] Client-side encryption
- [ ] Real-time notifications (WebSocket)
- [ ] File versioning UI

### Future
- [ ] Redis cluster for caching
- [ ] Custom desktop client improvements
- [ ] Mobile app enhancements
- [ ] Real-time collaboration

---

## Key Improvements Over Seafile

| Feature | Seafile | SesameFS |
|---------|---------|----------|
| **Storage Backend** | Local filesystem only | S3, Glacier, Disk - configurable |
| **Cold Storage** | Not supported | Smart cold tier (auto-selects Glacier IR/Deep) |
| **Database** | MySQL/PostgreSQL (single node) | Cassandra (global, distributed) |
| **Chunking** | Rabin CDC, fixed sizes | FastCDC, adaptive to network speed |
| **Chunk Sizes** | Fixed 1-8MB | Adaptive 2-256MB based on connection |
| **Hash Security** | SHA-1 (deprecated) | SHA-256 |
| **Authentication** | Custom + LDAP | OIDC-native |
| **Multi-tenancy** | One hostname per instance | Multiple hostnames per cluster |
| **Session State** | Sticky sessions required | Stateless (any server, any request) |
| **Upload Resume** | Same server only | Any server (distributed tokens) |
| **Horizontal Scaling** | Per-tenant instances | Shared stateless pool |
| **Storage Lifecycle** | Manual | Auto hot/cold with smart backend selection |
| **Geo-distribution** | Complex replication | Native Cassandra multi-DC |
| **Security Scanning** | ClamAV only (optional) | ClamAV + YARA + URL scanning |
| **Phishing Detection** | Not available | YARA rules + document analysis |
| **Deployment** | C + Python (complex) | Go (single binary) |
| **License** | AGPLv3 (server) | TBD (permissive) |

---

## Getting Started

```bash
# Clone the repository
git clone https://github.com/Sesame-Disk/sesamefs.git
cd sesamefs

# Copy and configure
cp config.example.yaml config.yaml
# Edit config.yaml with your settings

# Run with Docker Compose (includes Cassandra)
docker-compose up -d

# Or run locally (requires Cassandra)
go run ./cmd/sesamefs

# Run tests
go test ./...
```

---

## Project Structure

```
sesamefs/
├── cmd/
│   └── sesamefs/           # Main application entry point
├── internal/
│   ├── api/                # HTTP handlers
│   │   ├── v2/             # REST API v2
│   │   └── seafhttp/       # Seafile sync protocol
│   ├── auth/               # OIDC authentication
│   ├── chunker/            # FastCDC implementation
│   ├── storage/            # Storage backends (S3, Glacier, Disk)
│   ├── db/                 # Cassandra repository layer
│   ├── models/             # Domain models
│   └── services/           # Business logic
├── pkg/                    # Public packages
├── config/                 # Configuration
├── migrations/             # Cassandra schema migrations
├── _legacy/                # Archived prototype code
└── docker-compose.yaml
```

---

## Legacy Code

The original prototype code has been archived in `_legacy/` for reference:
- Basic Gin HTTP server
- MySQL database layer
- AWS Glacier upload/download
- Token-based authentication

---

## Documentation

- [Seafile API Compatibility](docs/SEAFILE_COMPATIBILITY.md) - How the Seafile-compatible API works
- [Licensing Guide](docs/LICENSING.md) - Legal considerations for using and distributing SesameFS

---

## Development & Testing

### Seafile API Comparison Testing

For testing API compatibility with a real Seafile server, use this reference server:

```
Server: https://app.nihaoconsult.com/
Email: abel.aguzmans@gmail.com
Password: Qwerty123!
```

**Authentication:**
```bash
# Get auth token
curl -X POST "https://app.nihaoconsult.com/api2/auth-token/" \
  -d "username=abel.aguzmans@gmail.com" \
  -d "password=Qwerty123!"

# Use token for API calls
curl -H "Authorization: Token <token>" "https://app.nihaoconsult.com/api2/repos/"
```

**Sync Protocol Testing (with token from download-info):**
```bash
# Get download-info (includes sync token)
curl -H "Authorization: Token <api_token>" \
  "https://app.nihaoconsult.com/api2/repos/<repo_id>/download-info/"

# Use Seafile-Repo-Token header for sync endpoints
curl -H "Seafile-Repo-Token: <sync_token>" \
  "https://app.nihaoconsult.com/seafhttp/repo/<repo_id>/commit/HEAD"
```

### Key API Format Differences (Seafile vs SesameFS)

These are the response format requirements discovered through testing with real Seafile:

| Endpoint | Field | Seafile Format | Notes |
|----------|-------|----------------|-------|
| `/commit/{id}` | `parent_id` | `null` (not `""`) | Use pointer type for null JSON |
| `/commit/{id}` | `second_parent_id` | `null` | Always include, even if null |
| `/commit/{id}` | `repo_name` | String | Include library name |
| `/commit/{id}` | `repo_desc` | String | Include library description |
| `/commit/{id}` | `repo_category` | `null` | Always null |
| `/commit/{id}` | `no_local_history` | `1` | Integer, not boolean |
| `/commit/{id}` | `creator` | 40 zeros | Format: `"0000...0000"` (40 chars) |
| `/commit/{id}` | `version` | `1` | Must be 1, not 0 |
| `/fs-id-list` | Response | `[]` (JSON array) | NOT newline-separated text |
| `/permission-check` | Response | Empty body | Just HTTP 200, no JSON |
| `/protocol-version` | Response | `{"version": 2}` | JSON object |
| `/download-info` | `encrypted` | `""` (empty string) | Not `false` boolean |

### Single-Port Architecture

Unlike traditional Seafile which uses multiple ports:
- Port 8000: Seahub (web UI & API)
- Port 8082: Seafile fileserver (seafhttp)

SesameFS runs everything on a **single port** (default 8080):
- `/api2/`, `/api/v2/` - REST API
- `/seafhttp/` - Sync protocol

This is intentional for cloud-native deployments (easier load balancing, K8s, etc.).

---

## References

- [FastCDC Paper (USENIX ATC'16)](https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia)
- [Restic Chunker (Go Library)](https://github.com/restic/chunker)
- [Apache Cassandra 5.0](https://cassandra.apache.org/)
- [Seafile Architecture](https://github.com/haiwen/seafile)

---

## License

MIT License (may change in future)

See [LICENSE](LICENSE) for details.

**Note on Seafile Compatibility:** SesameFS implements a Seafile-compatible API for interoperability purposes. SesameFS is an independent project, not affiliated with Seafile Ltd. See [docs/LICENSING.md](docs/LICENSING.md) for details on why this is legally permissible.

---

## Contributing

See `CONTRIBUTING.md` (coming soon)
