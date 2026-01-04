# SesameFS - Project Context for Claude

## What is SesameFS?

A Seafile-compatible cloud storage API with modern internals (Go, Cassandra, S3).

## Critical Constraints

1. **Seafile desktop/mobile client chunking cannot be changed** - compiled into apps (Rabin CDC, 256KB-4MB, SHA-1)
2. **SHA-1→SHA-256 translation for sync protocol only** - Desktop/mobile clients use `/seafhttp/` with SHA-1 block IDs; server translates to SHA-256 for storage. Web frontend uses REST API with server-side SHA-256 chunking.
3. **Block size for web/API**: 2-256MB (server-controlled, adaptive FastCDC)
4. **SpillBuffer threshold**: 16MB (memory below, temp file above)

### Upload Paths

| Client | Protocol | Chunking | Block Hash |
|--------|----------|----------|------------|
| Desktop/Mobile | `/seafhttp/` (sync) | Client-side Rabin CDC | SHA-1 → translated to SHA-256 |
| Web Frontend | REST API | Server-side FastCDC | SHA-256 (no translation) |
| API clients | REST API | Server-side FastCDC | SHA-256 (no translation) |

## Key Code Locations

| Feature | File |
|---------|------|
| Seafile sync protocol | `internal/api/sync.go` |
| File upload/download | `internal/api/seafhttp.go` |
| S3 storage backend | `internal/storage/s3.go` |
| Block storage | `internal/storage/blocks.go` |
| Multi-backend manager | `internal/storage/storage.go` |
| FastCDC chunking | `internal/chunker/fastcdc.go` |
| Adaptive chunking | `internal/chunker/adaptive.go` |
| Database schema | `internal/db/db.go` |
| API v2 handlers | `internal/api/v2/*.go` |
| Configuration | `internal/config/config.go` |

## Documentation

| Document | Contents |
|----------|----------|
| [README.md](README.md) | Quick start, features overview, roadmap |
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | Design decisions, storage architecture, database schema |
| [docs/API-REFERENCE.md](docs/API-REFERENCE.md) | API endpoints, implementation status, compatibility |
| [docs/DATABASE-GUIDE.md](docs/DATABASE-GUIDE.md) | Cassandra tables, examples, consistency |
| [docs/FRONTEND.md](docs/FRONTEND.md) | React frontend: setup, patterns, Docker, troubleshooting |
| [docs/TESTING.md](docs/TESTING.md) | Test coverage, benchmarks, running tests |
| [docs/LICENSING.md](docs/LICENSING.md) | Legal considerations for Seafile compatibility |

## External References

| Resource | URL |
|----------|-----|
| Seafile Web API v2.1 | https://manual.seafile.com/latest/develop/web_api_v2.1/ |
| seafile-js (frontend API client) | https://github.com/haiwen/seafile-js |

## Quick Commands

```bash
# Run tests
go test ./...

# Run with coverage
go test ./... -coverprofile=coverage.out

# Start dev server
go run cmd/sesamefs/main.go

# Docker compose
docker-compose up -d
```

## Frontend Development

**Full guide**: [docs/FRONTEND.md](docs/FRONTEND.md) - Complete setup, patterns, Docker, troubleshooting

### Quick Reference

```bash
# Docker build caching fix (if changes don't appear)
docker-compose stop frontend && docker-compose rm -f frontend && docker rmi cool-storage-api-frontend
docker-compose build --no-cache frontend
docker-compose up -d frontend

# Local dev (faster iteration)
cd frontend && npm install && npm start  # runs on port 3001
```

### Key Files

| File | Purpose |
|------|---------|
| `frontend/src/models/dirent.js` | Parses API response (is_locked, file_tags, etc.) |
| `frontend/src/components/dirent-list-view/` | Directory listing, file rows, lock icons |
| `frontend/src/components/dialog/` | Modal dialogs (share, rename, tags) |
| `frontend/src/utils/seafile-api.js` | API client wrapper |
| `frontend/src/css/dirent-list-item.css` | File row styling, lock icon positioning |

### Adding New File Properties

1. **Backend**: Add to `Dirent` struct in `internal/api/v2/files.go`
2. **Frontend model**: Parse in `src/models/dirent.js` constructor
3. **Component**: Render: `{dirent.property && <Component/>}`
