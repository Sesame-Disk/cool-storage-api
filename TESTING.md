# Testing Guide

This document describes the test coverage status and what requires integration testing.

## Current Coverage

| Package | Coverage | Notes |
|---------|----------|-------|
| `internal/config` | 96.7% | Well covered, only yaml parse error paths not tested |
| `internal/storage` | 31.4% | StorageManager, health checks, policy resolution |
| `internal/api` | 25.3% | Sync protocol, token management, hostname resolution, hash translation |
| `internal/api/v2` | 3.1% | Requires database integration |
| `internal/chunker` | 63.2% | FastCDC algorithm well covered |
| `internal/models` | n/a | Data structures only |

*Last updated: 2025-12-28*

## Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Run specific package tests
go test ./internal/api/... -v

# Run with race detection
go test ./... -race
```

## Unit Testable Code (Easy to Test)

The following components can be tested in isolation:

### Configuration (`internal/config`)
- `DefaultConfig()` - returns default config values
- `applyEnvOverrides()` - environment variable parsing
- `Validate()` - config validation rules
- `getEnv()`, `getEnvInt()` - helper functions

### Token Management (`internal/api/seafhttp.go`)
- `NewTokenManager()` - creates in-memory token store
- `CreateToken()` / `CreateUploadToken()` / `CreateDownloadToken()` - token generation
- `GetToken()` - token retrieval with type validation
- `DeleteToken()` - token cleanup
- Token expiration logic

### Sync Protocol Structs (`internal/api/sync.go`)
- `Commit` - JSON serialization/deserialization with pointer types for null fields
- `FSObject` - file and directory objects
- `FSEntry` - directory entries
- `isHexString()` - hex string validation for FS IDs
- `sha1Hex()` - SHA-1 compatible hash generation (40 chars)
- `GetProtocolVersion` - returns protocol version 2
- `PermissionCheck` - returns empty body (Seafile format)
- `QuotaCheck` - returns quota status
- Commit struct null field serialization (`parent_id: null`)

### Hash Translation (`internal/api/sync_test.go`)
- `TestBlockIDFormats` - SHA-1 (40 char) vs SHA-256 (64 char) detection
- `TestSHA256Computation` - SHA-256 hash format validation
- `TestHashTypeParameter` - `?hash_type=sha256` query parameter handling
- `TestExternalToInternalMapping` - external→internal ID mapping logic
- `TestCheckBlocksMapping` - CheckBlocks with SHA-1→SHA-256 translation
- `TestBlockHashValidation` - hash verification for direct SHA-256 uploads

### Hostname Resolution (`internal/api/hostname.go`)
- `normalizeHostname()` - hostname normalization
- `HostnameResolver` - in-memory cache behavior
- Wildcard matching logic
- Middleware context setting

### Storage Manager (`internal/storage/storage.go`)
- `NewManager()` - creates storage manager with empty backends
- `RegisterBackend()` - registers backends with failover configuration
- `GetBackend()` - retrieves backend by name
- `GetHealthyBackend()` - returns healthy backend with automatic failover
- `GetBlockStore()` - returns BlockStore for storage class (with caching)
- `GetHealthyBlockStore()` - returns healthy BlockStore with failover
- `ResolveStorageClass()` - policy resolution (hostname → region → class)
- `CheckHealth()` - health check operations
- `UpdateHealth()` - consecutive fail tracking
- `ListBackends()` - list all registered backends
- `GetHotBackends()` / `GetColdBackends()` - filter by access type
- `HealthStatus.String()` - status to string conversion

### HTTP Handlers (without dependencies)
- `PermissionCheck` - always returns "rw"
- `QuotaCheck` - always returns has_quota: true
- Ping/health endpoints
- Server info endpoints

## Integration Test Requirements (Hard to Test)

The following code paths require external dependencies:

### Database Operations (requires Cassandra)
- `internal/db/db.go`
  - `New()` - establishes Cassandra connection
  - All CRUD operations for files, libraries, versions
  - Migrations and schema creation

- `internal/api/v2/*.go`
  - `ListLibraries()`, `CreateLibrary()`, `GetLibrary()`, `DeleteLibrary()`
  - `ListShareLinks()`, `CreateShareLink()`, `DeleteShareLink()`
  - `InitiateRestore()`, `GetRestoreStatus()`, `ListRestoreJobs()`
  - All handlers that query the database

- `internal/api/sync.go`
  - `GetHeadCommit()` - reads from database
  - `PutCommit()` - writes commit to database
  - `GetFS()`, `RecvFS()` - FS object storage

### Storage Operations (requires S3/MinIO)
- `internal/storage/s3.go`
  - `NewS3Store()` - creates S3 client
  - `PutBlock()`, `GetBlock()`, `DeleteBlock()`
  - `BlockExists()` - check block existence

- `internal/storage/block.go`
  - `BlockStore` operations
  - Block caching logic

- `internal/api/seafhttp.go`
  - `HandleUpload()` - file upload to S3
  - `HandleDownload()` - file download from S3

- `internal/api/sync.go`
  - `GetBlock()`, `PutBlock()` - block transfer
  - `CheckBlocks()` - block existence check

### Server Lifecycle
- `internal/api/server.go`
  - `NewServer()` - initializes all dependencies
  - `initS3Storage()` - S3 client creation
  - `Run()` - HTTP server start
  - `Shutdown()` - graceful shutdown

### Chunking (partially)
- `internal/chunker/chunker.go`
  - Speed probing (requires network)
  - Actual upload operations

## Setting Up Integration Tests

To run integration tests, you need:

### 1. Docker Compose Environment
```bash
# Start services
docker-compose up -d cassandra minio

# Wait for Cassandra to be ready
docker-compose exec cassandra cqlsh -e "DESCRIBE KEYSPACES"
```

### 2. Environment Variables
```bash
export CASSANDRA_HOSTS=localhost:9042
export CASSANDRA_KEYSPACE=sesamefs_test
export S3_ENDPOINT=http://localhost:9000
export S3_BUCKET=test-bucket
export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin
```

### 3. Test Database Setup
```sql
CREATE KEYSPACE IF NOT EXISTS sesamefs_test
  WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};
```

## Mock Implementations

The codebase provides interfaces that can be mocked:

### `TokenStore` Interface
```go
type TokenStore interface {
    CreateUploadToken(orgID, repoID, path, userID string) string
    CreateDownloadToken(orgID, repoID, path, userID string) string
    GetToken(token string, tokenType string) (*TokenInfo, error)
    DeleteToken(token string) error
}
```

Already has: `TokenManager` (in-memory) and `MockTokenStore` (for tests)

### Database Interface (TODO)
Consider adding a `DBInterface` to allow mocking database operations:
```go
type DBInterface interface {
    GetLibrary(orgID, repoID string) (*Library, error)
    CreateLibrary(lib *Library) error
    // ...
}
```

### Storage Interface (TODO)
Consider adding a `StorageInterface` for S3 operations:
```go
type StorageInterface interface {
    PutBlock(ctx context.Context, blockID string, data []byte) error
    GetBlock(ctx context.Context, blockID string) ([]byte, error)
    BlockExists(ctx context.Context, blockID string) (bool, error)
}
```

## Upload Architecture

The codebase has **three separate upload paths** that do NOT share internal API calls:

| Path | Endpoint | Used By | Chunking | Storage Backend |
|------|----------|---------|----------|-----------------|
| **Sync Protocol** | `/seafhttp/repo/:id/block/:id` | Seafile Desktop | Client-side (SHA-1→SHA-256) | **StorageManager** (multi-region) |
| **v2 Block API** | `/api/v2/blocks/upload` | Web/API clients | Client-side (SHA-256) | **StorageManager** (multi-region) |
| **SeafHTTP Upload** | `/seafhttp/upload-api/:token` | Web forms | No chunking | S3Store direct |
| **v2 File Upload** | `/api2/repos/:id/upload` | API clients | No chunking | S3Store direct |

**Key differences:**
- **Sync Protocol** - Uses `StorageManager` for multi-region storage routing, translates SHA-1→SHA-256 internally
- **v2 Block API** - Uses `StorageManager` for multi-region storage routing (updated 2025-12-28)
- **Web uploads** - Store whole files path-addressed (not content-addressed)

### Hash Translation (SHA-1 to SHA-256)

Seafile clients use SHA-1 (40 chars) for block IDs, but SesameFS stores everything as SHA-256 (64 chars) internally:

```
Seafile Client (SHA-1)           SesameFS (SHA-256)
─────────────────────────────────────────────────────────
PUT block/abc123... (40)         → Compute SHA-256 of data
                                 → Store with internal SHA-256 ID
                                 → Save mapping: abc123... → SHA-256
─────────────────────────────────────────────────────────
GET block/abc123... (40)         → Lookup mapping: abc123... → SHA-256
                                 → Retrieve using internal SHA-256 ID
─────────────────────────────────────────────────────────
New clients (SHA-256):           → Use ?hash_type=sha256
PUT block/SHA-256... (64)        → Store directly (no mapping needed)
```

The `block_id_mappings` table in Cassandra stores external→internal translations.

## Future Improvements

1. **Add database interface** - Abstract DB operations behind interface for mocking
2. **Add storage interface** - Abstract S3 operations for easier testing
3. **Integration test suite** - Add `_integration_test.go` files with build tags
4. **Test containers** - Use testcontainers-go for automatic Docker management
5. **E2E tests** - Full API tests with real Seafile client compatibility checks
