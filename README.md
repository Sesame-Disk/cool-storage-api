# WIP: SesameFS - Enterprise File Storage Platform

> A modern, flexible, enterprise-grade file storage and sync platform built in Go. Inspired by Seafile Pro but designed for multi-cloud storage with support for immediate (S3/Disk) and archival (Glacier) storage classes.

Notice: Test it at your own risk and create issues here. The project is somewhat AI slop, but we will get it to be better over time with Claude's help xD.

## Project Vision

SesameFS aims to be a world-class replacement for enterprise file sync and share (EFSS) solutions with these key differentiators:

1. **Multi-Region Storage with Intelligent Routing**: Multiple backends with hostname-based routing and automatic failover
2. **Smart Two-Tier Storage**: Hot (S3 Standard/IA) and cold (Glacier IR/Deep Archive) with automatic tiering
3. **Distributed-First Architecture**: Cassandra + stateless API servers = global scale
4. **SHA-256 Internal Storage**: Modern security with transparent SHA-1 translation for Seafile compatibility
5. **Modern Authentication**: OIDC-native with accounts.sesamedisk.com
6. **True Multi-Tenancy**: Complete tenant isolation with per-tenant storage backends
7. **Seafile Client Compatible**: Works with existing Seafile desktop and mobile apps

## Technology Stack

| Component | Technology | Version |
|-----------|------------|---------|
| **Language** | Go | 1.25.5 |
| **Database** | Apache Cassandra | 5.0.6 |
| **Object Storage** | S3-compatible | - |
| **Archive Storage** | AWS Glacier | - |
| **Authentication** | OIDC | - |
| **API Framework** | Gin | 1.10.0 |
| **Chunking** | FastCDC | - |
| **Container Base** | Debian Trixie | 13 slim |

---

## Key Improvements Over Seafile

| Feature | Seafile | SesameFS |
|---------|---------|----------|
| **Storage Backend** | Local filesystem only | S3, Glacier, Disk - configurable |
| **Multi-Region Storage** | Single backend | Multiple backends with hostname routing |
| **Storage Failover** | None | Automatic failover to healthy backends |
| **Cold Storage** | Not supported | Smart cold tier (auto-selects Glacier IR/Deep) |
| **Database** | MySQL/PostgreSQL (single node) | Cassandra (global, distributed) |
| **Chunking** | Rabin CDC, fixed sizes | FastCDC, adaptive to network speed |
| **Chunk Sizes** | Fixed 1-8MB | Adaptive 2-256MB based on connection |
| **Hash Security** | SHA-1 everywhere | SHA-256 internally (SHA-1 translated) |
| **Authentication** | Custom + LDAP | OIDC-native |
| **Multi-tenancy** | One hostname per instance | Multiple hostnames per cluster |
| **Session State** | Sticky sessions required | Stateless (any server, any request) |
| **Upload Resume** | Same server only | Any server (distributed tokens) |
| **Security Scanning** | ClamAV only (optional) | ClamAV + YARA + URL scanning |
| **Phishing Detection** | Not available | YARA rules + document analysis |
| **Deployment** | C + Python (complex) | Go (single binary) |

---

## Development Roadmap

### Phase 1: Foundation (MVP) - COMPLETE
- [x] Project structure and Go modules setup
- [x] Configuration management (YAML + env overrides)
- [x] Cassandra connection and schema
- [x] Library CRUD operations
- [x] S3 storage integration (MinIO compatible)
- [x] Basic file upload/download via `/seafhttp/`
- [x] Token-based file access (configurable TTL)
- [x] FastCDC chunking with adaptive sizing
- [x] Block storage layer (content-addressable)
- [x] Block check/upload/download endpoints
- [x] Distributed token store (Cassandra-backed, stateless)

### Seafile Client Compatibility - COMPLETE
**Tested with:** Seafile Desktop Client for macOS - login, sync, file upload all working.

- [x] `/api2/` legacy route aliases
- [x] `GET /api2/repos/` - List libraries
- [x] `GET /api2/repos/:id/dir/?p=/` - Directory listing
- [x] `GET /api2/auth-token/` - Auth token endpoint
- [x] Sync protocol endpoints (`/seafhttp/repo/:id/*`)
- [x] Commit/FS object model in Cassandra

### Phase 2: Stateless Distributed Architecture - COMPLETE
- [x] Content-addressable block storage (S3)
- [x] Block deduplication (by SHA256)
- [x] Distributed token store (Cassandra TTL)
- [x] Any server can handle any request (stateless)
- [ ] `POST /api/v2/files/commit` - Finalize chunked upload
- [ ] Upload session tracking (for resume across servers)

### Phase 3: Multi-Hostname Multi-Tenancy
- [ ] `hostname_mappings` table in Cassandra
- [ ] Tenant resolution middleware (hostname -> org_id)
- [ ] URL generation uses request hostname
- [ ] Per-org storage configuration (S3 regions)
- [ ] Per-org settings and quotas
- [ ] Multi-region S3 routing (nearest to user)

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
- [ ] ClamAV integration (TCP, INSTREAM protocol)
- [ ] YARA rules engine (phishing detection)
- [ ] URL extraction and scanning (Safe Browsing, PhishTank)
- [ ] Office document analysis (oletools)
- [ ] PDF analysis (pdfid/pdf-parser)
- [ ] Share link abuse prevention

### Phase 6: Office Integration (OnlyOffice/Collabora)
- [ ] WOPI protocol endpoints (CheckFileInfo, GetFile, PutFile)
- [ ] JWT authentication
- [ ] Co-authoring with real-time sync
- [ ] File locking during edit
- [ ] Document conversion

### Phase 7: Advanced
- [ ] Search (Elasticsearch)
- [ ] Thumbnails and previews
- [ ] Client-side encryption
- [ ] Real-time notifications (WebSocket)
- [ ] File versioning UI

---

## Getting Started

### Prerequisites

- **Go 1.25+** - [Install Go](https://go.dev/doc/install)
- **Docker & Docker Compose** - [Install Docker](https://docs.docker.com/get-docker/)

### Quick Start

```bash
# Clone the repository
git clone https://github.com/Sesame-Disk/sesamefs.git
cd sesamefs

# Start development environment (Cassandra, MinIO, SesameFS)
./scripts/bootstrap.sh dev

# Test the API
curl http://localhost:8080/ping
# -> "pong"

# Test with dev token
curl http://localhost:8080/api2/account/info/ \
  -H "Authorization: Token dev-token-123"

# Stop when done
./scripts/bootstrap.sh --down
```

### Local Development

```bash
# 1. Start infrastructure (Cassandra + MinIO + schema)
./scripts/bootstrap.sh dev

# 2. Stop the SesameFS container (keep infrastructure running)
docker-compose stop sesamefs

# 3. Run SesameFS locally
go run ./cmd/sesamefs serve

# 4. Run tests
go test ./...
```

### Multi-Region Testing

```bash
# Bootstrap multi-region environment
./scripts/bootstrap.sh multiregion

# Run tests in container
./scripts/run-tests.sh multiregion all

# Stop the environment
./scripts/bootstrap.sh multiregion --down
```

See [docs/MULTIREGION-TESTING.md](docs/MULTIREGION-TESTING.md) for detailed testing scenarios.

---

## Web UI (Frontend)

SesameFS includes a web interface extracted from Seafile Pro (Seahub), modified to work as a standalone React SPA.

```bash
# Start backend
./scripts/bootstrap.sh dev

# In another terminal, start frontend
cd frontend
npm ci --legacy-peer-deps
npm start

# Open http://localhost:3001
# Login: admin@sesamefs.local / dev-token-123
```

See [docs/FRONTEND.md](docs/FRONTEND.md) for detailed setup guide.

---

## Project Structure

```
sesamefs/
├── cmd/sesamefs/              # Main application entry point
├── internal/
│   ├── api/                   # HTTP handlers
│   │   ├── v2/                # REST API v2
│   │   └── sync.go            # Seafile sync protocol
│   ├── chunker/               # FastCDC implementation
│   ├── storage/               # Storage backends (S3, Glacier, Disk)
│   ├── db/                    # Cassandra repository layer
│   └── models/                # Domain models
├── frontend/                  # React web UI
├── scripts/                   # Dev/test scripts
├── docs/                      # Detailed documentation
└── docker-compose.yaml        # Development stack
```

---

## Single-Port Architecture

Unlike traditional Seafile which uses multiple ports (8000 for web, 8082 for sync), SesameFS runs everything on a **single port** (default 8080):
- `/api2/`, `/api/v2/` - REST API
- `/seafhttp/` - Sync protocol

This is intentional for cloud-native deployments (easier load balancing, K8s, etc.).

---

## Documentation

| Document | Contents |
|----------|----------|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Design decisions, storage architecture, GC, schemas |
| [docs/API-REFERENCE.md](docs/API-REFERENCE.md) | API endpoints, implementation status, compatibility |
| [docs/TESTING.md](docs/TESTING.md) | Test coverage, benchmarks, running tests |
| [docs/MULTIREGION-TESTING.md](docs/MULTIREGION-TESTING.md) | Multi-region testing guide |
| [docs/FRONTEND.md](docs/FRONTEND.md) | Web UI setup, patterns, Docker, troubleshooting |
| [docs/MIGRATION-FROM-SEAFILE.md](docs/MIGRATION-FROM-SEAFILE.md) | Seafile migration guide |
| [docs/LICENSING.md](docs/LICENSING.md) | Legal considerations |

---

## References

- [FastCDC Paper (USENIX ATC'16)](https://www.usenix.org/conference/atc16/technical-sessions/presentation/xia)
- [Apache Cassandra 5.0](https://cassandra.apache.org/)
- [Seafile Architecture](https://github.com/haiwen/seafile)

---

## License

MIT License (may change in future)

See [LICENSE](LICENSE) for details.

**Note on Seafile Compatibility:** SesameFS implements a Seafile-compatible API for interoperability purposes. SesameFS is an independent project, not affiliated with Seafile Ltd. See [docs/LICENSING.md](docs/LICENSING.md) for details.

---

## Contributing

See `CONTRIBUTING.md` (coming soon)
