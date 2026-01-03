# Testing Guide

This document describes test coverage, benchmarks, and how to run tests.

## Current Coverage

| Package | Coverage | Notes |
|---------|----------|-------|
| `internal/config` | 92.5% | Well covered |
| `internal/chunker` | 79.2% | FastCDC + Adaptive chunking |
| `internal/storage` | 46.6% | StorageManager, S3, BlockStore, SpillBuffer |
| `internal/api` | 21.0% | Sync protocol, token management, hostname |
| `internal/api/v2` | 13.8% | FileView, OnlyOffice, Starred, CRUD ops |
| `internal/models` | n/a | Data structures only |
| `internal/db` | 0% | Requires Cassandra (integration tests) |

*Last updated: 2026-01-03*

### Frontend Tests

The frontend (extracted from Seafile's Seahub) does not currently have unit tests. The `npm test` script exists but no test files are present in `frontend/src/`.

---

## Running Tests

```bash
# Run all tests
go test ./...

# Run with coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out

# Run specific package
go test ./internal/api/... -v

# Run with race detection
go test ./... -race

# Run short tests only
go test ./... -short
```

---

## Benchmarks

### FastCDC Chunking Performance

Benchmarks on Apple M1 Pro (10-core, 16GB RAM):

| Benchmark | Throughput | Notes |
|-----------|------------|-------|
| `BenchmarkFastCDC_ChunkAll` | **45.87 MB/s** | 256MB file, 16MB chunks |
| `BenchmarkFastCDC_2MB_Chunks` | **48.77 MB/s** | 256MB file, 2MB chunks |
| `BenchmarkFastCDC_16MB_Chunks` | **59.68 MB/s** | 256MB file, 16MB chunks |
| `BenchmarkSpeedProbe` | **881μs/op** | 1MB probe upload |

### Adaptive Chunking by Connection Speed

| Connection Type | Speed | Optimal Chunk | Est. Upload (100MB) |
|-----------------|-------|---------------|---------------------|
| Slow (mobile) | 1 Mbps | 2 MB | 13m 39s |
| Mobile (LTE) | 10 Mbps | 10 MB | 1m 20s |
| Home (cable) | 50 Mbps | 50 MB | 16s |
| Office (fiber) | 100 Mbps | 100 MB | 8s |
| Fast (enterprise) | 500 Mbps | 256 MB | 1.6s |
| Datacenter | 1 Gbps | 256 MB | 800ms |

### Running Benchmarks

```bash
# Run all chunker benchmarks
go test -bench=. -benchtime=3s ./internal/chunker/

# Run specific benchmark
go test -bench=BenchmarkFastCDC_ChunkAll -benchtime=5s ./internal/chunker/

# Run with memory allocation stats
go test -bench=. -benchmem ./internal/chunker/

# Run large file tests (skipped in -short mode)
go test -v -run "TestLargeFileChunking|TestAdaptiveChunkingWithSpeed" \
  ./internal/chunker/ -timeout 5m
```

---

## Test Files

| File | Tests |
|------|-------|
| `internal/api/sync_test.go` | Sync protocol, hash translation, commit serialization |
| `internal/api/seafhttp_test.go` | TokenManager, MockTokenStore, SeafHTTPHandler |
| `internal/api/hostname_test.go` | Hostname normalization, wildcard matching |
| `internal/api/server_test.go` | Server initialization |
| `internal/api/v2/handler_test.go` | Request binding validation |
| `internal/api/v2/fileview_test.go` | FileViewHandler, auth middleware, error pages (13 tests) |
| `internal/api/v2/onlyoffice_test.go` | OnlyOffice pure functions (10 tests) |
| `internal/api/v2/libraries_test.go` | formatSize function (32 tests) |
| `internal/api/v2/fs_helpers_test.go` | FS helper functions (10 tests) |
| `internal/api/v2/starred_test.go` | StarredFile struct, auth checks, form binding (18 tests) |
| `internal/api/v2/files_crud_test.go` | CRUD operations (25+ tests) |
| `internal/storage/manager_test.go` | StorageManager, failover, health tracking |
| `internal/storage/s3_test.go` | S3 helper functions, config structs |
| `internal/storage/blocks_test.go` | BlockStore, hash sharding |
| `internal/storage/buffer_test.go` | SpillBuffer hybrid memory/disk (20 tests) |
| `internal/config/config_test.go` | Config loading, validation, env overrides |
| `internal/chunker/fastcdc_test.go` | FastCDC algorithm, deterministic chunking |
| `internal/chunker/adaptive_test.go` | Adaptive chunking, speed probe (16 tests) |
| `internal/models/models_test.go` | Model JSON serialization |

---

## Unit Testable Code

### Configuration (`internal/config`)
- `DefaultConfig()`, `applyEnvOverrides()`, `Validate()`
- `getEnv()`, `getEnvInt()` helpers

### Token Management (`internal/api/seafhttp.go`) ✅
- `NewTokenManager()`, `CreateToken()`, `GetToken()`, `DeleteToken()`
- Token expiration logic

### Sync Protocol (`internal/api/sync.go`) ✅
- `Commit`, `FSObject`, `FSEntry` structs
- `isHexString()`, `sha1Hex()`, `GetProtocolVersion`

### Storage Manager (`internal/storage/storage.go`) ✅
- `NewManager()`, `RegisterBackend()`, `GetBackend()`
- `GetHealthyBackend()`, `GetBlockStore()`, `CheckHealth()`

### S3 Helper Functions (`internal/storage/s3.go`) ✅
- `isNotFoundError()`, `key()`, `GetAccessType()`, `Bucket()`

### BlockStore (`internal/storage/blocks.go`) ✅
- `NewBlockStore()`, `hashToKey()` with two-level sharding

### File View Handler (`internal/api/v2/fileview.go`) ✅
- `errorPageHTML()`, `onlyOfficeEditorHTML()`, `isOnlyOfficeFile()`

### OnlyOffice Pure Functions ✅
- `generateDocKey()`, `getDocumentType()`, `canEditFile()`, `signJWT()`

### FS Helpers ✅
- `normalizePath()`, `RemoveEntryFromList()`, `FindEntryInList()`
- `UpdateEntryInList()`, `AddEntryToList()`

---

## Integration Test Requirements

These require external dependencies:

### Database Operations (requires Cassandra)
- `internal/db/db.go` - Connection, CRUD, migrations
- All handlers that query the database

### Storage Operations (requires S3/MinIO)
- `internal/storage/s3.go` - `Put()`, `Get()`, `Delete()`, `Exists()`
- Glacier operations, presigned URLs
- Block storage operations

---

## Mock Implementations

### `TokenStore` Interface
```go
type TokenStore interface {
    CreateUploadToken(orgID, repoID, path, userID string) string
    CreateDownloadToken(orgID, repoID, path, userID string) string
    GetToken(token string, tokenType string) (*TokenInfo, error)
    DeleteToken(token string) error
}
```
Has: `TokenManager` (in-memory) and `MockTokenStore` (for tests)

### `Store` Interface
```go
type Store interface {
    Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error)
    Get(ctx context.Context, storageKey string) (io.ReadCloser, error)
    Delete(ctx context.Context, storageKey string) error
    Exists(ctx context.Context, storageKey string) (bool, error)
    // ...
}
```
Has: `mockStore` (in `manager_test.go`)

---

## Setting Up Integration Tests

### 1. Docker Compose Environment
```bash
docker-compose up -d cassandra minio

# Wait for Cassandra
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

---

## Memory Usage Notes

### Block Storage

| Upload Path | Max Memory Per Block |
|-------------|---------------------|
| Seafile clients | 4 MB (client-controlled) |
| Web/API | 256 MB (fast connections only) |

### SpillBuffer (Hybrid Memory/Disk)

| Data Size | Storage | Performance |
|-----------|---------|-------------|
| < 16 MB | Memory | Fast (no I/O) |
| ≥ 16 MB | Temp file | Memory-safe |

### Multipart Upload

For files >100MB, use `S3Store.PutAuto()` for automatic multipart:
- `MultipartThreshold = 100 MB`
- `MultipartPartSize = 16 MB`

---

## Known Test Issues

### Fixed Issues (2026-01-03)

1. **`v2/fileview_test.go` - Missing tokenCreator**
   - Tests were missing mock tokenCreator, causing nil pointer panics
   - Fix: Added `mockTokenCreator` struct implementing `TokenCreator` interface
   - Updated all tests that create `FileViewHandler` to include tokenCreator

2. **`v2/starred_test.go` - StarFileRequest binding tests**
   - Tests used `"p"` JSON field but expected `Path` field to be populated
   - Fix: Updated tests to properly test both `path` (v2.1 API) and `p` (v2 legacy API) bindings

### Remaining Issues

1. **`v2/handler_test.go` empty name validation**
   - Test expects 400 for empty name, returns 200
   - Fix: Add `binding:"required,min=1"` to `CreateLibraryRequest.Name`

### Model JSON Tags

The `Library` struct uses Seafile-compatible JSON tags:
- `repo_id` instead of `id`
- `repo_name` instead of `name`
- `last_modified` instead of `updated_at`

---

## Future Improvements

1. **Add database interface** - Abstract DB operations for mocking
2. **Integration test suite** - Add `_integration_test.go` files with build tags
3. **Test containers** - Use testcontainers-go for automatic Docker management
4. **E2E tests** - Full API tests with real Seafile client compatibility
