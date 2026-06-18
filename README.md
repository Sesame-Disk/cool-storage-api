# WIP: SesameFS - Enterprise File Storage Platform

> A modern, flexible, enterprise-grade file storage and sync platform built in Go. Inspired by Seafile Pro but designed for multi-cloud storage with support for immediate (S3/Disk) and archival (Glacier) storage classes.

Notice: Test it at your own risk and create issues here. The project is somewhat AI slop, but we will get it to be better over time with Claude's help xD.

## Project Vision

SesameFS aims to be a world-class replacement for enterprise file sync and share (EFSS) solutions with these key differentiators:

1. **Multi-Region Storage with Intelligent Routing**: Multiple backends with hostname-based routing and automatic failover
2. **Smart Two-Tier Storage**: Hot (S3 Standard/IA) and cold (Glacier IR/Deep Archive) with automatic tiering
3. **Distributed-First Architecture**: Cassandra + stateless API servers = global scale
4. **SHA-256 Internal Storage**: Modern security with transparent SHA-1 translation for Seafile compatibility
5. **Modern Authentication**: OIDC-native with accounts.sesamedisk.com plus user API keys for desktop clients, CLI, and automation
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
- [x] `POST /api2/auth-token/` - Auth token exchange (dev credentials or `email + API key`)
- [x] Sync protocol endpoints (`/seafhttp/repo/:id/*`)
- [x] Commit/FS object model in Cassandra

### Web Frontend (Seahub) - IN PROGRESS
**Status:** Core functionality working. See [docs/FRONTEND.md](docs/FRONTEND.md).

- [x] Library list (My Libraries)
- [x] Directory browsing inside libraries
- [x] File/folder icons and thumbnails
- [x] Create new library
- [x] Delete library (single and batch)
- [x] Create folder
- [x] Delete folder/file (single and batch)
- [x] File locking UI (lock icon, lock/unlock actions)
- [x] File tags display and management
- [x] Share info dialog (view shares)
- [ ] File upload via web
- [ ] File download via web
- [ ] Copy/Move operations
- [ ] History/versions
- [ ] Search

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

### Phase 4: Enterprise Features - IN PROGRESS
- [x] Directory operations (list, create, delete)
- [x] File operations (info, delete, move, copy, batch delete)
- [x] File locking (lock, unlock, check lock status)
- [x] File tagging (create, update, delete tags)
- [x] Share info display (internal shares, links)
- [ ] Quota management per org
- [ ] Admin APIs
- [ ] Audit logging
- [x] Share links (view - create/delete pending)
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

- **Docker & Docker Compose v2** - [Install Docker](https://docs.docker.com/get-docker/)
- **Go 1.25+** - [Install Go](https://go.dev/doc/install) (only needed to run outside Docker)

### Quick Start (Development)

```bash
# Clone the repository
git clone https://github.com/Sesame-Disk/sesamefs.git
cd sesamefs

# Create your local config (defaults work out of the box)
cp .env.example .env

# Start the full dev stack (Cassandra + MinIO + SesameFS + OnlyOffice)
docker compose up --build

# Test the API
curl http://localhost:3000/ping
# -> "pong"

# Test with a dev token
curl http://localhost:3000/api2/account/info/ \
  -H "Authorization: Token dev-token-admin"

# Billing portal redirect (Docker dev defaults to the Sesame test billing URL)
curl -I http://localhost:3000/billing/
curl -I http://localhost:3000/accounts/delete/

# Stop when done
docker compose down
```

`/billing/` is always an internal SesameFS route. The backend checks authentication and redirects to the external portal configured by `BILLING_URL`. In local Docker Compose, that env var defaults to `https://t-accounts.sesamedisk.com/billing/` for testing only.

`/accounts/delete/` follows the same pattern. SesameFS validates authentication first, then redirects to the external Accounts URL configured by `ACCOUNTS_DELETE_ACCOUNT_URL`.



### Local Development (Run Go outside Docker)

```bash
# 1. Start the infrastructure
docker compose up -d cassandra minio

# 2. Run the one-shot Cassandra bootstrap and wait for it to finish
docker compose up cassandra-bootstrap

# 3. Start MinIO bucket initialization
docker compose up -d minio-init

# 4. Run SesameFS locally against it
go run ./cmd/sesamefs serve

# 5. Run tests
go test ./...
```

The bootstrap step prepares Cassandra auth/keyspace/replication only. The
application applies the embedded schema migrations when `go run` starts.

### Production Deployment

```bash
cp .env.prod.example .env
# Fill in all values in .env, then bring up Cassandra first:
docker compose -f docker-compose.prod.yml up -d cassandra

# Run the one-shot Cassandra bootstrap explicitly from the designated node:
docker compose -f docker-compose.prod.yml --profile bootstrap up cassandra-bootstrap

# Start the normal app services:
docker compose -f docker-compose.prod.yml up -d sesamefs frontend
```

If SesameFS runs behind nginx or another reverse proxy, set `server.trusted_proxies` in your YAML config or `SERVER_TRUSTED_PROXIES` in `.env` to the exact proxy IP/CIDR values that are allowed to supply `X-Forwarded-For` and `X-Real-IP`. In the supported production chain `client -> central nginx -> internal SesameFS nginx -> Go`, the internal nginx, typically the nginx inside the `frontend` container, preserves the real client IP already resolved by the central nginx, so Go only needs to trust the internal nginx hop. This assumes that internal nginx is private and only reachable from trusted internal paths. Leaving it empty is the secure default and makes SesameFS use the direct peer IP instead.

See [docs/DEPLOY.md](docs/DEPLOY.md) for the full production guide (DNS, SSL, firewall, etc.).

### Multi-Region Testing

Two stacks are available; both are driven by a single wrapper script (it auto-detects
this host's Docker socket, so no extra setup):

**True cluster (recommended)** — `docker-compose.mr-cluster.yaml`: a real 2-DC
Cassandra cluster (`usa` + `eu`), one MinIO per region with active-active bucket
replication, both region servers, an nginx LB, and a web UI **per region**.

```bash
# Build + start the cluster (regions, LB, USA + EU web UIs)
./scripts/run-mr-cluster.sh up

# Fast infra proof: Cassandra 2-DC up + MinIO mirrors an object across regions
./scripts/run-mr-cluster.sh replication-test

# Full Playwright E2E suite: features, sharing, concurrency, cross-region
# replication, multi-user collaboration, and upload/download performance
./scripts/run-mr-cluster.sh test

./scripts/run-mr-cluster.sh status        # service status + topology + URLs
./scripts/run-mr-cluster.sh logs [svc]    # tail logs
./scripts/run-mr-cluster.sh down          # stop (add -v to wipe volumes)
```

Default URLs: USA UI http://localhost:5173 · EU UI http://localhost:5174 ·
LB http://localhost:8000 · USA API :8088 · EU API :8081 · MinIO consoles :9101/:9103.
(8080 is reserved by a host process here, so the LB uses 8000.)

**Single-node (lighter)** — `docker-compose.mr.yaml`: one shared Cassandra + one
shared MinIO. Same verbs via `./scripts/run-playwright.sh up | test | down`.

> **Logging into a web UI:** the login page is SSO-only (password login disabled in
> dev). Seed the dev cookie in the browser console, then reload:
> `document.cookie = "sesamefs_auth=admin@sesamefs.local@dev-token-admin; path=/"; location.href="/dashboard/"`
> Swap `admin`/`dev-token-admin` for `user`/`dev-token-user`, etc.

See [docs/MULTIREGION-TESTING.md](docs/MULTIREGION-TESTING.md) for detailed scenarios,
and [docs/BUG-FILE-DETAIL-MODIFIER-20260618.md](docs/BUG-FILE-DETAIL-MODIFIER-20260618.md)
for a known file-attribution bug surfaced by these tests.

---

## Web UI (Frontend)

SesameFS includes a web interface extracted from Seafile Pro (Seahub), modified to work as a standalone React SPA.

```bash
# Start backend + frontend nginx
docker compose up -d sesamefs frontend

# Open http://localhost:3000
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
│   ├── Dockerfile             # Multi-stage: node builder + nginx:alpine runtime
│   └── nginx.conf             # SPA routing + proxy_pass to Go backend
├── nginx/
│   └── nginx.conf.template    # Nginx config (SSL, proxy, OnlyOffice)
├── scripts/                   # Dev/test scripts
├── docs/                      # Detailed documentation
├── docker-compose.yaml        # Development stack (MinIO, dev tokens)
├── docker-compose.prod.yml    # Production stack (S3, OIDC, SSL)
├── configs/                   # YAML config files (dev, prod, regions)
│   ├── config.docker.yaml     # Config baked into the dev Docker image
│   ├── config.prod.yaml       # Config mounted in production
│   ├── config.example.yaml    # Base example config
│   ├── config-eu.yaml         # EU multiregion sample
│   └── config-usa.yaml        # USA multiregion sample
├── .env.example               # Dev environment template
└── .env.prod.example          # Production environment template
```

---

## Container Architecture

Unlike traditional Seafile (multiple ports), SesameFS uses a clean two-container split behind a single nginx entry point:

```
Internet → nginx (TLS, rate limiting)
               ├── /api2/, /api/v2/, /api/v2.1/, /seafhttp/, /d/, /u/d/, /lib/  → sesamefs (Go :8080)
               └── /                                                             → frontend (nginx:alpine :80)
```

- **Go backend** (`sesamefs:8080`): pure API + Seafile sync protocol. No SPA serving.
- **React frontend** (`frontend:80`): nginx:alpine serving the React build with SPA routing.
- **Outer nginx**: TLS termination, rate limiting, CSP headers, mobile UA routing.

This is intentional for cloud-native deployments — frontend and backend can be built, scaled, and deployed independently.

---

## Documentation

| Document | Contents |
|----------|----------|
| [docs/DEPLOY.md](docs/DEPLOY.md) | **Production deployment guide** (VPS, SSL, S3, OIDC) |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Design decisions, storage architecture, GC, schemas |
| [docs/API-REFERENCE.md](docs/API-REFERENCE.md) | API endpoints, implementation status, compatibility |
| [docs/TESTING.md](docs/TESTING.md) | Test coverage, benchmarks, running tests |
| [docs/MULTIREGION-TESTING.md](docs/MULTIREGION-TESTING.md) | Multi-region testing guide |
| [docs/BUG-FILE-DETAIL-MODIFIER-20260618.md](docs/BUG-FILE-DETAIL-MODIFIER-20260618.md) | Known bug: `file/detail` last-modifier attribution |
| [docs/FRONTEND.md](docs/FRONTEND.md) | Web UI setup, patterns, Docker, troubleshooting |
| [docs/OIDC.md](docs/OIDC.md) | OIDC authentication configuration |
| [docs/TECHNICAL-DEBT.md](docs/TECHNICAL-DEBT.md) | Known issues, migration plans, incremental fixes |
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
