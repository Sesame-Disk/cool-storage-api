# README roadmap (archived 2026-10-12)

**Why archived:** the phase checklist in `README.md` had not tracked reality: done items were unchecked (web upload/download, quotas, admin APIs, OIDC, encrypted libraries, versioning UI) and planned items described approaches not taken (Elasticsearch search; WOPI instead of the OnlyOffice API). Moved here unchanged.

**Source:** `README.md` at commit `5c3163871`, lines 56–152.
**Current status lives in:** `docs/V1-PRODUCTION-ROADMAP.md`, `docs/OPEN-WORK-INDEX.md`, `docs/KNOWN_ISSUES.md`, `docs/X1-CRITICAL-PATH.md`.

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

