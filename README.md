# SesameFS - Enterprise File Storage Platform

> A modern, flexible, enterprise-grade file storage and sync platform built in Go. Inspired by Seafile Pro but designed for multi-cloud storage with support for immediate (S3/Disk) and archival (Glacier) storage classes.

## Project Vision

SesameFS aims to be a world-class replacement for enterprise file sync and share (EFSS) solutions with these key differentiators:

1. **Multi-Storage Class Architecture**: Unlike Seafile which only supports immediate-access storage, SesameFS supports:
   - **Hot Storage (S3/Disk)**: Immediate access for active files
   - **Cold Storage (Glacier)**: Cost-effective archival with retrieval delays
   - **Local Disk**: On-premise storage for compliance requirements

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
│      Seafile Desktop │ Seafile Mobile │ Web App │ WebDAV │ REST API     │
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
| **Block Storage** | Local filesystem only | **S3 + Glacier tiered** |
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
              blocks (512KB-8MB)    exists? Skip!
                                    (deduplication)
```

### Storage Class Tiering

| Class | Backend | Access Time | Use Case | Block Age Threshold |
|-------|---------|-------------|----------|---------------------|
| `hot` | S3 Standard | Milliseconds | Active files | Default |
| `warm` | S3-IA | Milliseconds | 30+ days inactive |
| `cold` | Glacier Instant | Milliseconds | 90+ days, rare access |
| `frozen` | Glacier Deep | 12-48 hours | 365+ days, compliance |

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
- Assigned to specific storage classes
- Versioned with full history
- Synced via Seafile clients

### Storage Lifecycle Policies
Libraries can define automatic transitions:
```yaml
lifecycle:
  - transition_after: 30d
    to_class: warm
  - transition_after: 90d
    to_class: cold
  - transition_after: 365d
    to_class: frozen
```

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
  min_size: 524288              # 512 KB
  avg_size: 2097152             # 2 MB
  max_size: 8388608             # 8 MB
  hash_algorithm: sha256

storage:
  default_class: hot
  backends:
    hot:
      type: s3
      endpoint: s3.amazonaws.com
      bucket: sesamefs-blocks-hot
      region: us-east-1
    warm:
      type: s3
      bucket: sesamefs-blocks-warm
      storage_class: STANDARD_IA
      region: us-east-1
    cold:
      type: s3
      bucket: sesamefs-blocks-cold
      storage_class: GLACIER_IR
      region: us-east-1
    frozen:
      type: glacier
      vault: sesamefs-deep-archive
      retrieval_tier: Bulk
      region: us-east-1

lifecycle:
  enabled: true
  check_interval: 1h
  rules:
    - age_days: 30
      from_class: hot
      to_class: warm
    - age_days: 90
      from_class: warm
      to_class: cold
    - age_days: 365
      from_class: cold
      to_class: frozen
```

---

## Development Roadmap

### Phase 1: Foundation (MVP)
1. [x] Project structure and Go modules setup
2. [ ] Configuration management (godotenv + YAML)
3. [ ] Cassandra connection and schema
4. [ ] Library CRUD operations
5. [ ] FastCDC chunking implementation
6. [ ] Block storage layer (S3)
7. [ ] File upload (chunked to S3)
8. [ ] File download (reassemble from blocks)
9. [ ] Directory operations
10. [ ] Share links (basic)
11. [ ] OIDC authentication integration
12. [ ] Glacier integration (upload + restore)

### Phase 2: Seafile Sync Protocol
- [ ] `/seafhttp/` endpoint implementation
- [ ] Commit/FS object model
- [ ] Block check/upload/download
- [ ] Sync state machine
- [ ] Desktop client compatibility testing

### Phase 3: Enterprise Features
- [ ] Multi-tenancy (organizations)
- [ ] Quota management
- [ ] Admin APIs
- [ ] Audit logging
- [ ] File versioning UI

### Phase 4: Advanced
- [ ] WebDAV interface
- [ ] Search (Elasticsearch)
- [ ] Thumbnails and previews
- [ ] Client-side encryption
- [ ] Real-time notifications (WebSocket)

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
| **Cold Storage** | Not supported | Native Glacier with restore workflow |
| **Database** | MySQL/PostgreSQL (single node) | Cassandra (global, distributed) |
| **Chunking Speed** | Rabin CDC (baseline) | FastCDC (10x faster) |
| **Hash Security** | SHA-1 (deprecated) | SHA-256 |
| **Authentication** | Custom + LDAP | OIDC-native |
| **Multi-tenancy** | Limited | Full isolation with per-tenant encryption |
| **Storage Lifecycle** | Manual | Automatic policies |
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

## References

- [FastCDC Paper (USENIX ATC'16)](https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia)
- [Restic Chunker (Go Library)](https://github.com/restic/chunker)
- [Apache Cassandra 5.0](https://cassandra.apache.org/)
- [Seafile Architecture](https://github.com/haiwen/seafile)

---

## License

MIT License (may change in future)

See [LICENSE](LICENSE) for details.

---

## Contributing

See `CONTRIBUTING.md` (coming soon)
