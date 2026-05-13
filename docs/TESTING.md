# Testing Guide

This document describes how to run tests, test coverage, and testing infrastructure.

**Last updated: 2026-05-12**

---

## Quick Start

```bash
# Start the development stack
docker compose up -d --build

# Note: `docker compose up -d --build` does NOT run tests automatically.
# Run tests explicitly through the compose test profile.

# Go unit tests
docker compose --profile test run --rm --build gotest

# All Go tests (unit + integration)
docker compose --profile test run --rm --build go-all-test

# API integration tests against the running stack
docker compose --profile test run --rm --build api-test

# Frontend checks and mobile checks plus smoke
docker compose --profile test run --rm --build frontend-test
docker compose --profile test run --rm --build mobile-test

# Or use the unified runner, which now prefers docker compose services
./scripts/test.sh go
./scripts/test.sh go-all
./scripts/test.sh api
./scripts/test.sh frontend
./scripts/test.sh mobile

# Full local validation pass
./scripts/test.sh all
```

---

## Unified Test Runner

The `./scripts/test.sh` script is the main entry point for all tests and now prefers `docker compose` test services when available.

## Docker-First Workflow

Use `docker compose up -d --build` to build and start the development environment only.

Do not attach tests to `docker compose up -d --build` itself. That command should remain focused on environment startup. Tests should run as explicit one-shot services under the `test` profile so failures are visible, repeatable, and do not slow down normal container startup.

### Test Services

| Service | Purpose |
|---------|---------|
| `gotest` | Go unit tests (`go test ./... -short -cover`) |
| `go-all-test` | All Go tests (`gotest` + integration sequence) |
| `go-integration-test` | Go integration tests against running `sesamefs` |
| `api-test` | Bash API integration suites via `scripts/test.sh api` |
| `oidc-test` | OIDC shell tests via `scripts/test.sh oidc` |
| `frontend-test` | Frontend lint + Jest |
| `mobile-test` | Mobile typecheck + lint + Vitest + desktop split smoke |

### Test Categories

| Category | Description | Requirements |
|----------|-------------|--------------|
| `api` | API integration tests (permissions, file ops, batch, etc.) | Backend running |
| `oidc` | OIDC authentication tests (config, login, logout, sessions) | Backend running |
| `sync` | Seafile CLI sync protocol tests | Backend + seafile-cli container |
| `multiregion` | Multi-region connectivity, routing tests | Multi-region stack |
| `failover` | Failover scenarios with large files | Multi-region + host docker |
| `go` | Go unit tests | Docker compose test profile |
| `go-all` | All Go tests (unit + integration) | `sesamefs` + Docker compose test profile |
| `go-integration` | Go integration tests (against running backend) | `sesamefs` + Docker compose test profile |
| `frontend` | Frontend React tests | Docker compose test profile |
| `mobile` | Mobile frontend checks plus desktop smoke | Docker compose test profile |
| `all` | Run all applicable tests | Auto-detects available services |

### Options

| Option | Description |
|--------|-------------|
| `--quick` | Skip long-running tests (encrypted library, failover) |
| `--verbose` | Show detailed output |
| `--list` | List available tests without running |
| `--help` | Show help message |

---

## Test Categories in Detail

### 1. API Integration Tests (`api`)

Requires: Backend running (`docker compose up -d`)

```bash
./scripts/test.sh api
./scripts/test.sh api --quick  # Skip encrypted library tests

# Direct compose invocation
docker compose --profile test run --rm --build api-test
```

**Test Suites:**
| Suite | Script | Tests | Description |
|-------|--------|-------|-------------|
| Permission System | test-permissions.sh | 24 | Role hierarchy (admin > user > readonly > guest) |
| Admin API + Multi-Tenant | test-admin-api.sh | varies | Superadmin, org CRUD, tenant isolation |
| File Operations | test-file-operations.sh | 16 | Create, rename, move, copy, delete files/dirs |
| Batch Operations | test-batch-operations.sh | 19 | Batch move/copy, async tasks, error handling |
| Library Settings | test-library-settings.sh | 5 | History limit, auto-delete, API tokens |
| Nested Folders | test-nested-folders.sh | varies | Nested directory operations |
| Nested Move/Copy | test-nested-move-copy.sh | 91 | Move/copy at depths 1-4, batch, chained, folder with contents |
| Departments | test-departments.sh | 29 | Department CRUD, hierarchy, members, delete cascade |
| Garbage Collection | test-gc.sh | 21 | GC admin API, status, triggers, permissions |
| Encrypted Library | test-encrypted-library-security.sh | 14 | Access control, unlock flow |

**Individual Scripts:**
```bash
./scripts/test-permissions.sh
./scripts/test-admin-api.sh
./scripts/test-file-operations.sh
./scripts/test-batch-operations.sh
./scripts/test-library-settings.sh
./scripts/test-nested-folders.sh
./scripts/test-nested-move-copy.sh
./scripts/test-departments.sh
./scripts/test-gc.sh
./scripts/test-encrypted-library-security.sh
```

### 2. OIDC Authentication Tests (`oidc`)

Requires: Backend running (`docker compose up -d`)

```bash
./scripts/test.sh oidc
./scripts/test.sh oidc --quick    # Skip tests requiring OIDC provider
./scripts/test.sh oidc --verbose  # Show request/response details
```

**Test Coverage:**
| Test Group | Tests | Description |
|------------|-------|-------------|
| Configuration | 4 | OIDC config endpoint, enabled status, secret exposure |
| Login URL | 5 | Authorization URL generation, parameters, PKCE |
| Callback | 3 | Code exchange, validation errors, JSON parsing |
| Logout URL | 4 | Single Logout URL, parameters, redirect handling |
| Session | 4 | Session info, token validation, logout |
| Trailing Slash | 4 | Endpoint compatibility with/without trailing slash |

**Individual Script:**
```bash
./scripts/test-oidc.sh
./scripts/test-oidc.sh --quick    # Skip provider-dependent tests
./scripts/test-oidc.sh --verbose  # Show detailed output
```

**Go Unit Tests (internal/auth):**
```bash
go test ./internal/auth/... -v
```

| Test File | Tests | Coverage |
|-----------|-------|----------|
| `session_test.go` | 12 | Session creation, validation, JWT, cleanup |
| `oidc_test.go` | 26 | Discovery, auth URL, state, logout, role mapping, parseIDToken (8 tests) |

### 3. Go Unit Tests (`go`)

Requires: Docker compose test profile

```bash
./scripts/test.sh go

# Direct compose invocation
docker compose --profile test run --rm --build gotest
```

**Coverage by Package (Updated 2026-02-02, Session 24):**
| Package | Test Files | Coverage | Notes |
|---------|-----------|----------|-------|
| `internal/health` | 1 | 100% | Health/ready/ping endpoints |
| `internal/chunker` | 3 | 78.7% | FastCDC + Adaptive + integration (500MB test skipped in -short) |
| `internal/config` | 1 | 72.7% | Config loading, validation |
| `internal/crypto` | 3 | 69.6% | Encryption, key derivation, Seafile compat |
| `internal/gc` | 4 | 65.1% | GC service, queue, worker, scanner (MockStore) |
| `internal/auth` | 2 | 55.7% | OIDC (incl. parseIDToken), sessions, JWT |
| `internal/storage` | 4 | 46.4% | S3, blocks, SpillBuffer, manager |
| `internal/middleware` | 2 | 42.1% | Permission middleware + audit middleware |
| `internal/api/v2` | 23 | 20.5% | REST handlers (14K lines) |
| `internal/api` | 4 | 19.1% | Sync protocol, SeafHTTP, hostname |
| `internal/db` | 1 | 0% | Seed tests only; DB operations require Cassandra |
| `internal/logging` | 0 | 0% | Structured logging wrapper |
| `internal/metrics` | 0 | 0% | Prometheus instrumentation |
| `internal/templates` | 0 | 0% | Email/document rendering |

**Direct compose invocation:**
```bash
docker compose --profile test run --rm --build gotest
```

### 3b. All Go Tests (`go-all`)

Requires: Backend running (`docker compose up -d`) + Docker compose test profile

```bash
./scripts/test.sh go-all

# Direct compose invocation
docker compose --profile test run --rm --build go-all-test
```

This aggregate run executes both:
- `go test ./... -short -cover`
- `go test -tags integration -v -count=1 -timeout 5m ./internal/integration/...`

### 4. Go Integration Tests (`go-integration`)

Requires: Backend running (`docker compose up -d`) + Docker compose test profile

```bash
./scripts/test.sh go-integration

# Or run directly through compose
docker compose --profile test run --rm --build go-integration-test
```

**Build tag**: `//go:build integration` — these tests are excluded from normal `go test ./...` runs.

**Representative test files:**
| File | Tests | Description |
|------|-------|-------------|
| `integration_test.go` | TestMain | Health check, client setup for 5 roles, graceful skip |
| `helpers_test.go` | — | `testClient` struct, HTTP helpers, assertion helpers |
| `libraries_test.go` | 5 | Create+List, Rename, Delete, Permissions (readonly/guest/user), Encrypted |
| `files_test.go` | 5 | CreateDirectory, FileUpload, FileDownload, Move+Copy, FileDelete |
| `permissions_test.go` | 4 | ReadonlyCannotWrite, GuestCannotCreate, AdminManageOther, CrossUserIsolation |

These tests make HTTP requests to the running backend (same model as bash scripts) and exercise the full stack: API handlers → middleware → database → storage. They don't contribute to `go test -cover` numbers since they're in a separate package making external HTTP calls.

**Docker-first default**: `test.sh` prefers the `go-integration-test` compose service, which waits for `sesamefs` and runs against the compose network.

### Upload Phase 1 Closeout Checks

For upload work touching chunked preflush, finalize behavior, cleanup, or
encrypted-library handling, use these focused checks before trusting a wider
suite result:

```bash
# Focused async preflush / cleanup unit slice
go test ./internal/api -run '^(TestHandleUploadChunked_(PreflushesContiguousBlockBeforeFinalize|DoesNotPreflushUntilMissingPrefixArrives|FinalizeWaitsForInFlightPreflush|FirstChunkReturnsBeforeBlockedPreflush)|TestPreflushChunkUploadBlocksBoundsConcurrentUploadsPerUpload|TestPreflushChunkUploadBlocksBoundsConcurrentUploadsGlobally|TestChunkUploadCleanupWaitsForInFlightPreflush|TestChunkUploadResolvePreflushedBlockFallsBackAfterFailedPreflush|TestChunkManagerCleanupDoesNotRemoveRecreatedUploadTempFile)$' -count=1

# Focused byte-equal chunked upload integrations
docker compose --profile test run --rm --build go-integration-test \
  go test -tags integration ./internal/integration \
  -run '^(TestChunkedUploadLinkReusePreflushesLargeRevisions|TestChunkedUploadOutOfOrderCompletesWhenGapFills)$' -count=1
```

The integration pair above is the current regression proof for byte-equal
chunked upload behavior after async preflush:

- `TestChunkedUploadLinkReusePreflushesLargeRevisions` verifies that a large
  preflushed chunked upload can be rewritten through the same upload-link path
  and still downloads byte-equal to the latest revision.
- `TestChunkedUploadOutOfOrderCompletesWhenGapFills` verifies that out-of-order
  chunk arrival still assembles and downloads byte-equal content once the
  missing prefix arrives.

`go-all-test` and the full `go-integration-test` service remain broader
merge-candidate gates, but they should be treated as suite-level validation,
not as a substitute for the focused upload regressions above.

### Recorded Closeout Evidence (2026-05-12)

For the PR closeout of Upload Performance Phase 1, the broader containerized
merge-gate runs were also green:

```bash
docker compose --profile test run --rm --build go-integration-test
docker compose --profile test run --rm --build go-all-test
```

Recorded outcomes from the closeout session:

- `go-integration-test` passed for the full integration package with
  `PASS ok github.com/Sesame-Disk/sesamefs/internal/integration`
- `go-all-test` completed with `All tests passed!`

Treat those results as branch-closeout evidence for the full backend test gate.
The focused upload regressions above remain the authoritative discriminator for
chunked/preflush/finalize correctness.

**Integration-test-first rule for backend refactors:**
- If a change touches dual-write behavior, denormalized projections, counters, sync `HEAD` semantics, cleanup cascades, or cursor pagination boundaries, start with an integration regression before trusting the refactor.
- Prefer HTTP-level tests in `internal/integration/` and add direct Cassandra assertions when the invariant spans multiple tables.
- Unit tests remain useful for helper logic, cursor parsing, and pure functions; they are not sufficient by themselves for projection consistency work.
- When a refactor fixes a cross-table bug, keep the regression test close to the entity area (`*_projection_regression_test.go`, `*_cursor_test.go`, etc.) so future rewrites inherit the coverage.

### Frontend Tests (`frontend`)

Requires: Docker compose test profile

```bash
./scripts/test.sh frontend

# Direct compose invocation
docker compose --profile test run --rm --build frontend-test
```

### Mobile Frontend Checks (`mobile`)

Requires: Docker compose test profile

```bash
./scripts/test.sh mobile

# Direct compose invocation
docker compose --profile test run --rm --build mobile-test
```

### 5. Sync Protocol Tests (`sync`)

Requires: Backend + seafile-cli container

```bash
# Start seafile-cli container
docker compose up -d seafile-cli

# Run sync tests
./scripts/test.sh sync

# Or run directly with options
./scripts/test-sync.sh
./scripts/test-sync.sh --verbose
./scripts/test-sync.sh --keep      # Keep test libraries after
./scripts/test-sync.sh --cleanup   # Only cleanup previous tests
```

**Tests Included:**
- Unencrypted: Remote → Local sync
- Unencrypted: Multiple files sync
- Unencrypted: File modification sync
- Unencrypted: Subdirectory sync
- Unencrypted: Large file (1.5MB) sync
- Encrypted: Remote → Local sync
- Encrypted: Large file (64KB) sync
- Encrypted: Binary file sync
- Encrypted: File modification sync

### 6. Multi-Region Tests (`multiregion`)

Requires: Multi-region stack (`./scripts/bootstrap.sh multiregion`)

```bash
# Start multi-region stack
./scripts/bootstrap.sh multiregion

# Run tests
./scripts/test.sh multiregion

# Or run specific tests
./scripts/test-multiregion.sh connectivity
./scripts/test-multiregion.sh upload
./scripts/test-multiregion.sh routing
./scripts/test-multiregion.sh failover
./scripts/test-multiregion.sh all
```

**Prerequisites:**
Add to `/etc/hosts`:
```
127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local
```

### 7. Failover Tests (`failover`)

Requires: Multi-region stack + host docker access (cannot run in container)

```bash
./scripts/test.sh failover

# Or run specific scenarios
./scripts/test-failover.sh setup       # Create test files
./scripts/test-failover.sh upload      # Test 1GB upload
./scripts/test-failover.sh download    # Stop server mid-download
./scripts/test-failover.sh upload-fail # Stop server mid-upload
./scripts/test-failover.sh recovery    # Verify after restart
./scripts/test-failover.sh cleanup     # Clean up
./scripts/test-failover.sh all         # All scenarios
```

**Container-Based Runner:**
```bash
./scripts/run-tests.sh multiregion all
./scripts/run-tests.sh failover all
```

### 8. Frontend Tests (`frontend`)

Requires: Node.js + npm

```bash
./scripts/test.sh frontend

# Or run directly
cd frontend
npm test                         # Watch mode
npm test -- --watchAll=false     # Single run
npm test -- --coverage           # With coverage
```

**Test Files:**
| File | Tests |
|------|-------|
| `src/models/__tests__/dirent.test.js` | 5 tests - Dirent model |
| `src/utils/__tests__/utils.test.js` | 50 tests - Utility functions |

---

## Environment Bootstrap

### Development Mode (Single Instance)

```bash
./scripts/bootstrap.sh dev
# or just
./scripts/bootstrap.sh

# With clean start
./scripts/bootstrap.sh dev --clean

# Stop
./scripts/bootstrap.sh --down

# Show status
./scripts/bootstrap.sh --status
```

**Services:**
- SesameFS: http://localhost:8082
- MinIO Console: http://localhost:9001 (minioadmin/minioadmin)

### Multi-Region Mode

```bash
./scripts/bootstrap.sh multiregion

# With clean start
./scripts/bootstrap.sh multiregion --clean

# Stop
./scripts/bootstrap.sh multiregion --down
```

**Services:**
- Load Balancer: http://localhost:8082
- USA Endpoint: http://us.sesamefs.local:8080
- EU Endpoint: http://eu.sesamefs.local:8080
- MinIO Console: http://localhost:9001

---

## Test Scripts Reference

| Script | Purpose | Tests | Requirements |
|--------|---------|-------|--------------|
| `test.sh` | **Unified test runner** | — | Varies by category |
| `test-permissions.sh` | Permission system tests | 24 | Backend |
| `test-admin-api.sh` | Admin API + multi-tenant | varies | Backend |
| `test-file-operations.sh` | File/dir CRUD tests | 16 | Backend |
| `test-batch-operations.sh` | Batch move/copy tests | 19 | Backend |
| `test-library-settings.sh` | Library settings API | 5 | Backend |
| `test-nested-folders.sh` | Nested directory tests | varies | Backend |
| `test-nested-move-copy.sh` | Nested move/copy (depth 1-4) | 91 | Backend |
| `test-departments.sh` | Department CRUD + hierarchy | 29 | Backend |
| `test-gc.sh` | GC admin API | 21 | Backend |
| `test-encrypted-library-security.sh` | Encrypted lib access | 14 | Backend |
| `test-oidc.sh` | OIDC authentication | 24 | Backend |
| `test-sync.sh` | Seafile sync protocol | varies | Backend + seafile-cli |
| `test-multiregion.sh` | Multi-region tests | varies | Multi-region stack |
| `test-failover.sh` | Failover scenarios | varies | Multi-region + host docker |
| `run-tests.sh` | Container-based runner | — | Multi-region stack |
| `bootstrap.sh` | Environment setup | — | Docker |
| `bootstrap-multiregion.sh` | Legacy multi-region setup | — | Docker |

| Go integration tests | `internal/integration/*_test.go` | Backend regression and end-to-end invariants | Backend |

**Important**: When adding a new bash integration test script, always register it in `test.sh` → `run_api_tests()` so it runs as part of the unified suite. For Go integration tests, add to `internal/integration/` with the `//go:build integration` tag. For backend refactors that touch canonical/projection consistency, integration coverage is the default entry point, not an optional follow-up.

---

## Benchmarks

### FastCDC Chunking Performance

| Benchmark | Throughput | Notes |
|-----------|------------|-------|
| `BenchmarkFastCDC_ChunkAll` | **45.87 MB/s** | 256MB file, 16MB chunks |
| `BenchmarkFastCDC_2MB_Chunks` | **48.77 MB/s** | 256MB file, 2MB chunks |
| `BenchmarkFastCDC_16MB_Chunks` | **59.68 MB/s** | 256MB file, 16MB chunks |

### Running Benchmarks

```bash
go test -bench=. -benchtime=3s ./internal/chunker/
go test -bench=. -benchmem ./internal/chunker/
```

---

## Test Infrastructure

### Authentication Tokens

| Token | User Role | Use Case |
|-------|-----------|----------|
| `dev-token-admin` | Admin | Full access |
| `dev-token-user` | User | Standard access |
| `dev-token-readonly` | Readonly | Read-only access |
| `dev-token-123` | Default dev | Legacy tests |

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SESAMEFS_URL` | `http://localhost:8082` | Backend URL (host-mapped port) |
| `DEV_TOKEN` | dev-token-123 | Auth token |
| `CLI_CONTAINER` | cool-storage-api-seafile-cli-1 | Seafile CLI container |
| `ENCRYPTED_PASSWORD` | testpass123 | Encrypted library password |

---

## Mock Implementations

### TokenStore Interface
```go
type TokenStore interface {
    CreateUploadToken(orgID, repoID, path, userID string) string
    CreateDownloadToken(orgID, repoID, path, userID string) string
    GetToken(token string, tokenType string) (*TokenInfo, error)
    DeleteToken(token string) error
}
```
Has: `TokenManager` (in-memory) and `MockTokenStore` (for tests)

### Store Interface
```go
type Store interface {
    Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error)
    Get(ctx context.Context, storageKey string) (io.ReadCloser, error)
    Delete(ctx context.Context, storageKey string) error
    Exists(ctx context.Context, storageKey string) (bool, error)
}
```
Has: `mockStore` (in `manager_test.go`)

---

## Garbage Collection (GC) Tests

### Overview

The GC system uses a `GCStore` interface to abstract all database operations, allowing tests to run with an in-memory `MockStore` instead of requiring Cassandra. This gives full integration-level test coverage without external dependencies.

```bash
# Run all GC tests (no database needed)
go test ./internal/gc/... -v

# Run adapter tests
go test ./internal/api/... -v -run TestGC

# Run hooks tests
go test ./internal/api/v2/... -v -run TestGC
go test ./internal/api/v2/... -v -run TestSetGCHooks
```

### Architecture: GCStore Interface + MockStore

The GC system is decoupled from Cassandra via:

| File | Purpose |
|------|---------|
| `internal/gc/store.go` | `GCStore` interface — all DB operations used by queue, worker, scanner |
| `internal/gc/store_cassandra.go` | `CassandraStore` — production implementation using `*db.DB` |
| `internal/gc/store_mock.go` | `MockStore` — in-memory implementation for tests, with helpers for seeding data |

The `MockStore` provides test helper methods:
- **Seeders**: `AddBlock`, `AddCommit`, `AddFSObject`, `AddLibrary`, `AddShareLink`, `AddOrganization`, `AddOrganizationWithName`, `AddDeletedOrg`, `AddUser`, `AddDeletedUser`, `AddGroupForOrg`, `AddGroupMembership`, `AddDeletedLibrary`, `AddShareByUser`, `AddStarredFile`, `AddMonitoredRepo`
- **Assertions**: `GetBlock`, `GetCommit`, `GetFSObj`, `HasUser`, `HasGroup`, `HasOrg`, `HasStarredFiles`, `HasMonitoredRepos`, `AuditLogEntries`
- **Queue inspection**: `QueueLen`, `QueueItems`

A `MockStorageProvider` and `mockBlockDeleter` simulate S3 and track deleted block IDs.

### Test Files

| File | Tests | Type | What's Tested |
|------|-------|------|---------------|
| `internal/gc/gc_test.go` | 20 | Unit (mock) | Stats (atomic counters, concurrency), GCStatus formatting, Service creation, config propagation, SetDryRun, disabled service, trigger channels, status with mock store |
| `internal/gc/queue_test.go` | 12 | Integration (mock) | Enqueue+Dequeue round-trip, grace period filtering, retry increment, ListOrgsWithQueuedItems, GetQueueSize, GetTotalQueueSize, multiple item types (incl. share, restore_job, user/library/org cascade), Complete removes items |
| `internal/gc/worker_test.go` | 26 | Integration (mock) | Block deletion (ref_count=0 with S3+DB+reverse mapping cleanup), block sparing (ref_count>0), dry run mode, commit cascade (enqueues root fs_object), FS object cascade (enqueues child dirs + blocks), retry on failure, empty queue, library contents enqueue (no duplicate blocks), block mapping deletion, context cancellation, **cascade dry-run** (user/library/org), **cascade invalid UUID**, **cascade full** (user/library/org with real mock data), **cascade already-deleted** graceful skip |
| `internal/gc/scanner_test.go` | 30 | Integration (mock) | Orphaned blocks (ref_count<=0), expired share links, orphaned commits (with org lookup), orphaned fs_objects, empty DB scan, full pipeline (all 12 phases), context cancellation, idempotent enqueue, expired shares (Phase 7), expired restore jobs (Phase 8), **Phase 10**: expired deleted users (enqueue/skip), **Phase 11**: expired deleted libraries (enqueue/skip/multiple), **Phase 12**: expired deleted orgs (enqueue/skip/multiple), **Phases 10-12 via ScanOnce integration** |
| `internal/api/gc_adapter_test.go` | 7 | Unit | Invalid UUIDs, empty inputs, interface compliance, nil service, config defaults |
| `internal/api/v2/gc_hooks_test.go` | 8 | Unit | Set/get hooks, nil defaults, concurrent access, interface compile-time check, mock call recording |

### Key Test Scenarios

All of these run without any external dependencies:

| Test | What It Verifies |
|------|------------------|
| `Worker_ProcessBlock_RefCountZero` | Block with ref_count=0 deleted from mock S3 + DB, block mappings cleaned |
| `Worker_ProcessBlock_RefCountPositive` | Block with ref_count>0 is NOT deleted (safety check) |
| `Worker_ProcessBlock_DryRun` | Dry run mode logs but doesn't delete anything |
| `Worker_ProcessFSObject_CascadeBlocks` | FS object deletion decrements block ref_counts, enqueues blocks that hit 0 |
| `Worker_ProcessCommit_CascadesFSObjects` | Commit deletion fetches commit → enqueues root fs_object for cascade |
| `Worker_ProcessFSObject_CascadesDirEntries` | Directory deletion enqueues child entries recursively |
| `Worker_EnqueueLibraryContents_NoDuplicateBlocks` | Library enqueue only adds commits+fs_objects (blocks cascade from fs_object processing) |
| `Worker_RetryOnFailure` | Unknown item type triggers retry count increment |
| `Scanner_ScanOrphanedBlocks` | Finds blocks with ref_count<=0 across orgs |
| `Scanner_ScanExpiredShareLinks` | Finds share links past expiry, skips permanent ones |
| `Scanner_ScanExpiredShares` | Expired user-to-user shares cleaned (Phase 7) |
| `Scanner_ScanExpiredRestoreJobs` | Completed/failed/expired restore jobs cleaned (Phase 8) |
| `Scanner_ScanOnce_FullPipeline` | All 12 scanner phases run in sequence |
| `Scanner_ScanExpiredDeletedUsers_EnqueuesExpired` | Phase 10: expired soft-deleted users get enqueued as `user_cascade` |
| `Scanner_ScanExpiredDeletedLibraries_EnqueuesExpired` | Phase 11: expired soft-deleted libraries get enqueued as `library_cascade` |
| `Scanner_ScanExpiredDeletedOrgs_EnqueuesExpired` | Phase 12: expired soft-deleted orgs get enqueued as `org_cascade` |
| `Scanner_ScanOnce_IncludesPhases10to12` | Full pipeline integration: all 3 new phases produce items via ScanOnce |
| `Worker_ProcessUserCascade_FullCascade` | User cascade: removes groups, shares, starred, monitored, hard-deletes user, audit log |
| `Worker_ProcessLibraryCascade_FullCascade` | Library cascade: enqueues commits+fs_objects, hard-deletes library, 2 audit entries |
| `Worker_ProcessOrgCascade_FullCascade` | Org cascade: deletes users/groups, enqueues libraries as LibraryCascade, hard-deletes org |
| `Worker_ProcessUserCascade_DryRun` | Dry run skips user deletion |
| `Worker_ProcessOrgCascade_DryRun` | Dry run skips org deletion |
| `Worker_ProcessUserCascade_AlreadyDeleted` | Gracefully skips when user doesn't exist |
| `Worker_ProcessOrgCascade_AlreadyDeleted` | Gracefully skips when org doesn't exist |
| `SoftDeleteOrganization_PlatformOrgProtection` | Platform org cannot be soft-deleted (403) |
| `RestoreOrganization_NonExistentOrgHitsDB` | Route wiring works, reaches DB layer |
| `Queue_DequeueBatch_GracePeriod` | Items newer than grace period are not dequeued |

### Manual GC Verification

After deploying, verify GC via the admin API:

```bash
# Check GC status
curl -H "Authorization: Token dev-token-admin" \
  http://localhost:8082/api/v2.1/admin/gc/status

# Trigger worker run
curl -X POST -H "Authorization: Token dev-token-admin" \
  -H "Content-Type: application/json" \
  -d '{"type":"worker"}' \
  http://localhost:8082/api/v2.1/admin/gc/run

# Trigger scanner run (dry run)
curl -X POST -H "Authorization: Token dev-token-admin" \
  -H "Content-Type: application/json" \
  -d '{"type":"scanner","dry_run":true}' \
  http://localhost:8082/api/v2.1/admin/gc/run
```

### End-to-End GC Test Scenario

Manual test flow to verify the full GC pipeline:

1. **Upload** a file to a library
2. **Verify** block exists in S3 (`blocks/` prefix)
3. **Delete** the file via API
4. **Check** `gc_queue` has a block entry (after ref_count hits 0)
5. **Wait** for grace period (1h default, or trigger worker manually)
6. **Verify** block deleted from S3 and `blocks` table
7. **Delete** the library
8. **Verify** all commits and fs_objects enqueued and eventually cleaned up

---

## Known Issues

### Tests Requiring Database

Some tests are skipped because they require a real database connection:
- `TestHandleAccountInfo` - Needs DB session (unconditional skip)
- `TestAccountInfoTotalSpace` - Needs DB session (unconditional skip)
- `TestCreateShare_Integration` - Needs DB for encrypted library check (unconditional skip)
- `TestCLIChunkingDemo` - Manual demo requiring `CHUNKING_DEMO=1` env var
These are tested via integration tests instead.

### GC Admin API Tests (bash)

The `scripts/test-gc.sh` script tests the GC admin endpoints against a live backend:

```bash
# Run GC admin API tests
./scripts/test-gc.sh

# With verbose output
./scripts/test-gc.sh --verbose
```

Tests: 21 assertions covering status endpoint, permission enforcement (403 for non-admin), worker/scanner triggers, dry_run override, status updates after triggers, edge cases (empty body, invalid JSON).

Also wired into `./scripts/test.sh api` as the "Garbage Collection Admin API" suite.

### Tests Updated in 2026-03-18 (Soft-Delete Cascade Tests)

- **Updated: `internal/gc/store_mock.go`** — Major enhancement: added `mockUser`, `mockDeletedLibrary`, `mockShareByUser` types; new fields for users, groups, groupMembers, groupsByMember, deletedLibraries, sharesByUser, starredFiles, monitoredRepos, auditLog; 12 new seeders (`AddDeletedOrg`, `AddUser`, `AddDeletedUser`, `AddGroupForOrg`, `AddGroupMembership`, `AddDeletedLibrary`, `AddShareByUser`, `AddStarredFile`, `AddMonitoredRepo`, etc.); 6 new assertion helpers (`HasUser`, `HasGroup`, `HasOrg`, `HasStarredFiles`, `HasMonitoredRepos`, `AuditLogEntries`); replaced all nil-returning stubs with real in-memory implementations for 19 GCStore methods
- **Updated: `internal/gc/worker_test.go`** — 26 tests (was 12): added 10 cascade tests — 3 dry-run (user/library/org), 2 invalid UUID, 3 full cascade with real mock data (user cascade cleans groups/shares/starred/monitored, library cascade enqueues contents, org cascade deletes users+groups and enqueues libraries), 2 already-deleted graceful skip
- **Updated: `internal/gc/scanner_test.go`** — 30 tests (was 11): added 9 tests — Phase 10 expired deleted users (enqueue expired, skip non-expired), Phase 11 expired deleted libraries (enqueue/skip/multiple), Phase 12 expired deleted orgs (enqueue/skip/multiple), full ScanOnce integration covering all 12 phases
- **Updated: `internal/api/v2/admin_test.go`** — 26 tests (was 23): added `SoftDeleteOrganization_PlatformOrgProtection` (403), `SoftDeleteOrganization_NonPlatformOrgHitsDB`, `RestoreOrganization_NonExistentOrgHitsDB`; added 2 new routes to `RegisterAdminRoutes_RoutesExist` (delete/, restore/)

### Tests Updated in 2026-03-17 (GC Major Overhaul)

- **Updated: `internal/gc/worker_test.go`** — commit cascade (enqueues root fs_object), fs_object cascade (enqueues child dirs), library enqueue (no duplicate blocks), block deletion with reverse mapping cleanup
- **Updated: `internal/gc/scanner_test.go`** — 11 tests: added Phase 7 (expired shares) + Phase 8 (expired restore jobs), full pipeline now covers all 8 phases
- **Updated: `internal/gc/store_mock.go`** — complete rewrite: new mock types (shares, restore jobs, file tags, API tokens), all new GCStore interface methods implemented

### Tests Updated in 2026-01-30 (GC Mock Refactoring)

- **Refactored: `internal/gc/gc_test.go`** — 12 tests using MockStore (Stats, lifecycle, config, dry run, triggers, status)
- **Rewritten: `internal/gc/queue_test.go`** — 10 real tests (enqueue/dequeue round-trip, grace period, retry, org listing, queue size, multiple types)
- **Rewritten: `internal/gc/worker_test.go`** — 12 real tests (block deletion with S3+DB, ref_count safety, dry run, commit/fs_object/block_mapping, cascade, retry, library contents, context cancellation)
- **Rewritten: `internal/gc/scanner_test.go`** — 9 real tests (orphaned blocks, expired share links, orphaned commits/fs_objects, empty DB, full pipeline, context cancellation, idempotent enqueue)
- **Updated: `internal/api/gc_adapter_test.go`** — 7 tests using MockStore (UUID validation, interfaces, nil safety)
- **Unchanged: `internal/api/v2/gc_hooks_test.go`** — 8 tests for GC hooks (set/get, thread safety, mock recording)
- **New: `scripts/test-gc.sh`** — 21 admin API integration tests (status, permissions, triggers, edge cases)
- **Updated: `docs/TESTING.md`** — Added comprehensive GC testing section

### Tests Updated in 2026-01-29 (Session 11)

- **Fixed 4 pre-existing test failures** — `TestGetSessionInfo` (nil cache), `TestOnlyOfficeEditorHTML*` (JSON format mismatch)
- **New: `search_test.go`** — 6 tests for search handler validation (missing/empty query, missing org_id, routes)
- **New: `batch_operations_test.go`** — 15 tests for batch operations (invalid JSON, missing fields, task progress CRUD, routes)
- **New: `library_settings_test.go`** — 11 tests for library settings (auth, invalid UUID, API token permissions, history limits, routes)
- **New: `restore_test.go`** — 5 tests for restore handler (missing path, invalid job_id, request binding, routes)
- **New: `blocks_test.go`** — 13 tests for block handler (hash validation, empty/too many hashes, nil store, upload, routes)
- **New: `audit_test.go`** — 9 tests for audit middleware (all HTTP methods, GET success/error, LogAudit/LogAccessDenied/LogPermissionChange)
- **Enabled `TestCreateShare_Validation`** — split from skipped `TestCreateShare`, runs validation paths without DB

### Tests Updated in 2026-01-29 (Session 10)

- Rewrote `admin_test.go` — replaced logic-reimplementation tests with real gin HTTP handler tests
- Added middleware gin handler tests to `permissions_test.go` — RequireAuth, RequireSuperAdmin, RequireOrgRole
- Added 8 `parseIDToken` direct tests to `oidc_test.go` — valid/expired/issuer/nonce/format/custom claims
- Fixed pre-existing compile errors in `fileview_test.go` — `h.fileViewAuthMiddleware()` → `fileViewAuthWrapper()`
- Fixed `TestRegisterFileViewRoutes` — passed real auth middleware instead of nil
- Fixed all test scripts to use port 8082 (host-mapped port)
- Fixed `test.sh` nested folders invocation (script name vs args split)
- Removed legacy `test-all.sh` (replaced by unified `test.sh`)

### Tests Updated in 2026-01-28

- Fixed `NewSeafHTTPHandler` test signature (added `permMiddleware` parameter)
- Fixed `middleware.Permission` → `middleware.LibraryPermission` type
- Fixed test scripts to use unique library names (prevents 409 conflicts)
- Created unified test runner (`test.sh`)

---

## Test Coverage Improvement Plan

**Last Updated**: 2026-02-02

### Current State

50+ test files across 10 packages (~300+ passing unit tests across api/v2, gc, middleware, auth, etc.) plus 14 Go integration tests (19 subtests) and 19 bash test scripts (~827 assertions). Coverage is strong in health/chunker/config/crypto/gc. The biggest gap is `internal/api/v2` (14K lines at 20.5%) and `internal/db` (1.1K lines at 0%).

### Go Integration Test Framework (Session 24) ✅

Built `internal/integration/` package with HTTP-based tests against running backend:
- 14 test functions covering libraries, files, permissions, encryption
- Exercises full stack (API → middleware → DB → storage) but doesn't contribute to `go test -cover`
- Docker fallback when local Go version insufficient
- Run via `./scripts/test.sh go-integration`

### Pre-Existing Test Failures — ✅ ALL FIXED (Session 11)

All 4 previously failing tests are now fixed:
- ~~`TestGetSessionInfo`~~ — Fixed: use `auth.NewSessionManager()` instead of `&auth.SessionManager{}`
- ~~`TestOnlyOfficeEditorHTML`~~ — Fixed: match `json.Marshal` compact format (no spaces after colons)
- ~~`TestOnlyOfficeEditorHTMLWithoutToken`~~ — Fixed: same
- ~~`TestOnlyOfficeEditorHTMLCustomizations`~~ — Fixed: `submitForm` omitted by `omitempty` when false

### Priority 1: ✅ DONE — Previously Untested Handler Files

All 6 handler files + audit middleware now have tests (Session 11):

| File | Test File | Tests Added | Coverage |
|------|-----------|-------------|----------|
| `api/v2/search.go` | `search_test.go` | 6 | Missing query, empty query, missing org_id, JSON format, constructor, routes |
| `api/v2/batch_operations.go` | `batch_operations_test.go` | 15 | Invalid JSON, missing fields, task progress (CRUD), JSON binding, routes, TaskStore |
| `api/v2/library_settings.go` | `library_settings_test.go` | 11 | Auth middleware, invalid UUID, API token validation, history limit, auto-delete, transfer, routes |
| `api/v2/restore.go` | `restore_test.go` | 5 | Missing path, invalid job_id, missing body, routes, request binding |
| `api/v2/blocks.go` | `blocks_test.go` | 13 | Invalid JSON, empty/too many hashes, nil blockstore, invalid hash, upload, response formats, routes |
| `middleware/audit.go` | `audit_test.go` | 9 | All HTTP methods, GET success/error, LogAudit no-org, LogAccessDenied, LogPermissionChange, constants |
| `api/v2/file_shares.go` | `file_shares_test.go` | 2 (new) | Split `TestCreateShare` → validation tests run without DB |
| `db/tokens.go` | — | — | Still requires Cassandra (Priority 3) |

### Priority 2: Partially Tested Files (Missing Handler Coverage)

| File | What's Tested | What's Missing |
|------|--------------|----------------|
| `api/v2/files.go` (3060 lines) | Batch, CRUD, lock | UploadFile, GetDownloadLink, CopyFile, MoveFile, GetFileRevisions |
| `api/sync.go` | Data structures, protocol format | Handler functions: GetHeadCommit, PutCommit, GetBlock, PutBlock, PackFS, RecvFS |
| `api/v2/libraries.go` (1085 lines) | Permission checks, list | CreateLibrary end-to-end, UpdateLibrary, DeleteLibrary |

### Priority 3: Infrastructure Improvements

| Improvement | Impact | Effort | Status |
|------------|--------|--------|--------|
| **DB interface mock** | Unlocks unit tests for all handlers with DB deps | High — define interface, implement mock, refactor handlers | Not started |
| ~~**Fix 4 pre-existing test failures**~~ | ~~Clean CI output~~ | ~~Low~~ | ✅ **DONE** (Session 11) |
| **Test containers (testcontainers-go)** | Real DB integration tests in CI | Medium — Docker-in-Docker setup | Not started |
| **Frontend E2E tests (Playwright)** | Full UI workflow coverage | High — framework setup + test authoring | Not started |
| **Frontend component tests** | Modal dialogs, share components | Medium — need to resolve @testing-library/react ESM issues | Not started |
