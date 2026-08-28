# Testing Guide

This document describes how to run tests, test coverage, and testing infrastructure.

**Last updated: 2026-05-20**

---

## Quick Start

```bash
# Start the development stack
docker compose up -d --build

# Note: `docker compose up -d --build` does NOT run tests automatically.
# Run tests explicitly through the compose test profile.

# Go unit tests
docker compose --profile test run --rm --build gotest

# Go integration tests against the running multi-node backend
docker compose --profile test run --rm --build go-integration-test

# Cross-datacenter (3-DC) closure legs for X2 and P3 do NOT run here -- they need a
# separate three-datacenter Cassandra fixture. See "Cross-datacenter (3-DC) legs".

# All Go tests (unit + integration)
docker compose --profile test run --rm --build go-all-test

# P2 real Cassandra/MinIO canonical-install race -- the closure evidence for
# P2/R9/R24. SESAMEFS_REQUIRE_P2_EVIDENCE=1 makes the gate non-vacuous: without
# it this test can t.Skip its way to exit 0 (unreachable MinIO, bucket mismatch,
# empty storage_class) and print "PASS / ok" while proving nothing. With it, any
# skip becomes a FAIL, and the run also fails unless it logs
# P2_CONTENTION_EVIDENCE candidates=2. The enforcement lives in the test, not in
# shell, so it is portable across Windows and Linux hosts.
#
# A green run of this command therefore means: the test really executed, two
# distinct physical incarnations really contended, and it passed.
docker compose --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_P2_EVIDENCE=1 go-integration-test \
  go test -tags integration -run '^TestP2ConcurrentCanonicalInstall$' \
  -v -count=1 -timeout 5m ./internal/integration

# P2 follow-up regressions: post-install mapping isolation (a mapping timeout must
# never re-enter the mint-capable initial phase), the CreateFile HEAD-conflict retry
# (no resubmit of a single-use install identity, no remint, no re-PUT), the
# UploadBlock New/201-vs-200 response contract, canonical-convergence 409 mapping on
# the file surfaces, and the canonical install SHA-256 input gate.
docker compose --profile test run --rm --build gotest \
  go test -count=1 -run 'Mapping|TestCreateFileTemplateTargetSurvivesHeadConflict|TestUploadBlock(FreshPutAnswers201New|ConfirmationRepairKeeps200NotNew|InitialRepairOfExistingCanonicalKeeps200NotNew)|TestCreateFileTemplateMaterializationRequiresResetCallback|TestWrite(UploadFile|CreateFile)Error|TestInstallBlockMetadata(RejectsNonSHA256BlockID|AcceptsCanonicalBlockID)|TestIsTransientFreshInstallPreparationErrorRequestCodes|TestRegisterUploadedBlockTargetFreshInstallAuthority' \
  ./internal/api/v2 ./internal/db

# P2 phase forwarding. Proves every upload funnel forwards the retry driver's
# materialization phase instead of a constant, that no production caller reaches
# a phase-erasing wrapper, and that the desktop-sync funnel gates the storage
# quota precheck to the initial phase only.
docker compose --profile test run --rm --build gotest \
  go test -count=1 -run 'TestP2MaterializationPhaseIsForwardedByFunnels' ./internal/integration
docker compose --profile test run --rm --build gotest \
  go test -count=1 -run 'TestPutBlockForwardsPhaseAndGatesQuotaToInitial' ./internal/api

# P2 fresh pre-INSTALL preparation and exact-cleanup regressions. These prove
# transient resolver retries retain one target, canceled/permanent/exhausted
# preparation submits no INSTALL, post-submit ambiguity grants no cleanup, and
# KnownLost/pre-INSTALL cleanup retries and final metrics stay exact-key scoped.
docker compose --profile test run --rm --build gotest \
  go test -count=1 -run 'FreshInstall(Authority|PreparationAndCleanup)|FreshCleanupRetriesAndMetrics' \
  ./internal/api/v2

# P2 confirmation phase and submitted-state regressions. These prove a rowless
# confirmation never mints/PUTs a second target, confirmation retries do not
# restart the initial phase, and pre-LWT versus entered-LWT provenance is kept.
docker compose --profile test run --rm --build gotest \
  go test -count=1 -run 'Confirmation(RejectsRowlessWithoutMint|DoesNotRestartInitialPhase)|InstallBlockMetadata' \
  ./internal/api/v2 ./internal/db

# API integration tests against the running stack
docker compose --profile test run --rm --build api-test

# Real seaf-cli single-client sync protocol suite (docker-native)
docker compose --profile test run --rm --build sync-test

# Frontend checks and mobile checks plus smoke
docker compose --profile test run --rm --build frontend-test
docker compose --profile test run --rm --build mobile-test

# Unified wrapper for the same sync suite
./scripts/test.sh sync

# Or use the unified runner, which now prefers docker compose services
./scripts/test.sh go
./scripts/test.sh go-integration
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
| `sync-test` | Real Seafile CLI sync suite in a one-shot container |
| `frontend-test` | Frontend lint + Jest |
| `mobile-test` | Mobile typecheck + lint + Vitest + desktop split smoke |

`sync-test` is now the preferred Docker-native entry point for the single-
client real Seafile harness. It runs `scripts/test-sync.sh` inside the
`docker/seafile-cli` image with fresh dedicated data/config volumes.
`./scripts/test.sh sync` now runs that service first and then executes
`scripts/test-sync-active-active.sh` as the second half of the unified
Docker-first sync path. When Docker Compose is not available, the wrapper still
falls back to the long-lived `seafile-cli` debug container path.

For the active-active desktop conflict harness specifically, run
`bash ./scripts/test-sync-active-active.sh`. That script is also included in
`./scripts/test.sh sync` on the Docker-first path, and it exercises both the
non-overlapping auto-merge path and the same-path fail-closed `503`
preservation path with two real `seaf-cli` clients against different backend
nodes. Use `--scenario safe-auto-merge` or `--scenario unsafe-503` to isolate a
single branch.

The default Go integration path is now multi-instance inside the compose `test`
profile: `go-integration-test` and `go-all-test` wait for `sesamefs`,
`sesamefs-node-2`, and `sesamefs-node-3`, then export `SESAMEFS_URL`,
`SESAMEFS_URL_2`, and `SESAMEFS_URL_3` so integration tests can exercise real
cross-process races by default.

`sesamefs-node-3` is specialized for block-admission tests: its in-flight caps
are `2/2`, waiter caps `2/2`, and wait defaults to `250ms`. Set
`BLOCK_ADMISSION_FAULT_WAIT=10s`, recreate node 3, and run
`block-admission-fault-test` for the shipped-wait compatibility drill. Node 2
keeps shipped admission defaults and is the target of the opt-in memory probe.
The probe sends 24 exact-cap bodies, samples correlated RSS/heap/cgroup every
50ms from request ramp through the held-body plateau and post-release drain, and
fails if the 1.25-adjusted full-lifetime cost exceeds the 80 MiB design value:

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_MEMORY_PROBE=1 go-integration-test \
  go test -tags integration -run '^TestSyncBlockMemoryUnderSaturation$' \
  -v -count=1 -timeout 5m ./internal/integration
```

Recreate `sesamefs-node-2` between evidence trials so each baseline starts from
a clean process.

Keep background GC isolated to the primary `sesamefs` service in that profile.
`sesamefs-node-2` and `sesamefs-node-3` must keep `GC_ENABLED=false`, or GC-
sensitive integration tests become nondeterministic because secondary nodes can
refresh dirty GC snapshots, requeue failed items, or purge expired share links in
parallel.

### Current Multi-Instance Coverage Gaps

- Multi-instance quota-race coverage now exists for concurrent per-user storage
  quota enforcement, but broader coverage is still missing for 3-node races,
  org-level quota contention, and quota rejection during auto-merge.
- The real desktop-client harness still does not cover quota rejection during
  auto-merge or deeper-tree active-active conflict branches.
- Handler-level integration proof now covers both `PUT /commit/HEAD` and
  `POST /update-branch` for same-tree idempotence, non-overlapping auto-merge,
  unmergeable `503`, and missing-current-head guards.

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
| `--keep-going` | Continue running remaining categories/suites after a failure. Default behavior is fail-fast. |
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

Requires: Docker Compose test profile. The direct compose command starts and
waits for the backend nodes automatically.

```bash
./scripts/test.sh go-all

# Direct compose invocation
docker compose --profile test run --rm --build go-all-test
```

This aggregate compose run executes, in order:
- `go test ./... -short -cover`
- `go test -tags integration -v -count=1 -timeout 10m ./internal/integration/...`
- `./scripts/test.sh api`
- `./scripts/test.sh oidc`

It does not include the real `seaf-cli` sync suite, frontend/mobile checks,
multi-region, or failover tests.

### 4. Go Integration Tests (`go-integration`)

Requires: Docker Compose test profile. The direct compose command starts and
waits for `sesamefs`, `sesamefs-node-2`, and `sesamefs-node-3` automatically.

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

What it covers in practice: only the Go HTTP-level backend regressions in
`internal/integration/...`. It does not run the bash API suites, OIDC shell
tests, frontend/mobile suites, or the real Seafile desktop/CLI sync harness.

That service now waits for all three compose-backed backend nodes and is the
default place to validate coordinator correctness under independent-process
contention. Use the dedicated multi-region stack only when the behavior under
test depends on hostname routing, region-specific storage classes, or failover
between region-pinned backends.

Current high-value coordinator-hardening coverage in that default path includes:
- mutation-only races across distinct nodes in `internal/integration/multi_instance_mutations_test.go`
- mixed races with uploads against other HEAD writers, including multi-container `seafhttp upload` vs rename, multi-container `seafhttp upload` vs same-repo move, and multi-container direct v2 upload vs delete
- upload finalization stress for both upload seams in `internal/integration/upload_finalization_race_test.go`
- dedicated two-region proofs for both upload seams when the test specifically needs the regional stack

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

Requires: Docker Compose for the preferred path. The direct compose command
starts and waits for `sesamefs` automatically. Host fallback needs the backend
reachable on `http://localhost:8080` plus the debug `seafile-cli` container.

```bash
# Preferred docker-native path
docker compose --profile test run --rm --build sync-test
docker compose --profile test run --rm --build -e SYNC_TEST_ARGS=--verbose sync-test

# Unified wrapper (stops on the first failing suite by default)
./scripts/test.sh sync
./scripts/test.sh sync --verbose
./scripts/test.sh sync --keep-going

# Manual container management, if you want direct control
docker compose --profile debug up -d --build seafile-cli
./scripts/test-sync.sh

# Or run directly with options
./scripts/test-sync.sh
./scripts/test-sync.sh --verbose
./scripts/test-sync.sh --keep      # Keep test libraries after
./scripts/test-sync.sh --cleanup   # Only cleanup previous tests
```

**Tests Included:**
- Unencrypted: Remote → Local sync
- Unencrypted: Local → Remote sync
- Unencrypted: Multiple files sync
- Unencrypted: File modification sync
- Unencrypted: Subdirectory sync
- Unencrypted: Large file (1.5MB) sync
- Encrypted: Remote → Local sync
- Encrypted: Local → Remote sync (remote presence verification)
- Encrypted: Large file (64KB) sync
- Encrypted: Binary file sync
- Encrypted: File modification sync (isolated fresh encrypted repo)

These are real single-client `seaf-cli` end-to-end checks. They validate the
desktop-compatible happy path and file integrity behavior, but they still do
not prove active-active multi-node conflict recovery under a real desktop
client.

### 6. Multi-Region Tests (`multiregion`)

Requires: Multi-region stack (`./scripts/bootstrap.sh multiregion`, which runs
compose `cassandra-bootstrap` before the app servers start)

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
- SesameFS: http://localhost:8080
- MinIO Console: http://localhost:9001 (minioadmin/minioadmin)

`bootstrap.sh` is a convenience wrapper only: it waits for Cassandra, runs the
compose `cassandra-bootstrap` service, and then starts SesameFS so the embedded
migration runner can apply the current schema baseline.

### Multi-Region Mode

```bash
./scripts/bootstrap.sh multiregion

# With clean start
./scripts/bootstrap.sh multiregion --clean

# Stop
./scripts/bootstrap.sh multiregion --down
```

**Services:**
- Load Balancer: http://localhost:8080
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
| `bootstrap.sh` | Environment setup wrapper around compose `cassandra-bootstrap` + app migrations | — | Docker |
| `bootstrap-multiregion.sh` | Legacy compatibility alias for `bootstrap.sh multiregion` | — | Docker |

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
| `SESAMEFS_URL_LOCAL` | `http://localhost:8080` | Host-side API URL used by the sync shell runner |
| `SESAMEFS_URL` | `http://sesamefs:8080` | In-container SesameFS URL used by `seaf-cli` |
| `DEV_API_TOKEN` | dev-token-admin | API bearer token for host-side setup/verification |
| `DEV_PASSWORD` | dev-token-123 | `seaf-cli` password for auth-token exchange |
| `CLI_CONTAINER` | sesamefs-seafile-cli-1 | Seafile CLI container |
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
| `internal/gc/worker_test.go` | 26 | Integration (mock) | Block deletion (ref_count=0 with S3+DB while the logical mapping survives), block sparing (ref_count>0), dry run mode, commit cascade (enqueues root fs_object), FS object cascade (enqueues child dirs + blocks), retry on failure, empty queue, library contents enqueue (no duplicate blocks), post-S3 orphan finalization, context cancellation, **cascade dry-run** (user/library/org), **cascade invalid UUID**, **cascade full** (user/library/org with real mock data), **cascade already-deleted** graceful skip |
| `internal/gc/scanner_test.go` | 30 | Integration (mock) | Orphaned blocks (ref_count<=0), expired share links, orphaned commits (with org lookup), orphaned fs_objects, empty DB scan, full pipeline (all 12 phases), context cancellation, idempotent enqueue, expired shares (Phase 7), expired restore jobs (Phase 8), **Phase 10**: expired deleted users (enqueue/skip), **Phase 11**: expired deleted libraries (enqueue/skip/multiple), **Phase 12**: expired deleted orgs (enqueue/skip/multiple), **Phases 10-12 via ScanOnce integration** |
| `internal/api/gc_adapter_test.go` | 7 | Unit | Invalid UUIDs, empty inputs, interface compliance, nil service, config defaults |
| `internal/api/v2/gc_hooks_test.go` | 8 | Unit | Set/get hooks, nil defaults, concurrent access, interface compile-time check, mock call recording |

Known coverage gaps from the PR 60 merge audit:
- `onlyoffice_pending_blocks` has no direct end-to-end test that seeds stale pending rows and verifies both reconciliation outcomes: reachable publish commits are cleared without rollback, and abandoned materialized blocks are decremented/enqueued through the scanner path.
- `scanOnlyOfficePendingBlocks` and the API-to-GC `OnlyOfficeReconciler` adapter are covered by compilation and Docker integration startup, but not by a focused mock scanner test that asserts org iteration, error accumulation, and phase metrics.
- `ScanOnce` still aggregates the OnlyOffice phase's "organizations reconciled" count into its generic `enqueued` total. When that observability debt is fixed, add a regression test that locks the intended counter semantics.

### Key Test Scenarios

All of these run without any external dependencies:

| Test | What It Verifies |
|------|------------------|
| `Worker_ProcessBlock_RefCountZero` | Block with ref_count=0 deleted from mock S3 + DB while the logical forward mapping survives |
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
  http://localhost:8080/api/v2.1/admin/gc/status

# Trigger worker run
curl -X POST -H "Authorization: Token dev-token-admin" \
  -H "Content-Type: application/json" \
  -d '{"type":"worker"}' \
  http://localhost:8080/api/v2.1/admin/gc/run

# Trigger scanner run (dry run)
curl -X POST -H "Authorization: Token dev-token-admin" \
  -H "Content-Type: application/json" \
  -d '{"type":"scanner","dry_run":true}' \
  http://localhost:8080/api/v2.1/admin/gc/run
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

## Cross-datacenter (3-DC) legs

Two closure properties can only be observed across real datacenters, so they run
against a separate fixture from everything above: **X2** (a destructive read must
intersect every DC that could have acknowledged a reference) and **P3** (GC must not
publish a fence a writer in another DC cannot see).

A single process cannot observe a consistency level. That is the entire reason this
fixture exists, and it is why these legs are not part of `go-all-test`.

### TL;DR for an agent picking this up cold

```bash
# 1. Three real Cassandra datacenters: dc-na, dc-eu, dc-asia, RF 1 each.
docker compose -f docker-compose.cassandra-3dc.yaml up -d

# 2. Everything below runs INSIDE a container (see "Windows / blocked binaries").
docker build -f Dockerfile.gotest -t sesamefs-3dc .
docker run -d --name dc3run --network sesamefs-cassandra-3dc_default sesamefs-3dc sleep 7200
docker network connect sesamefs_default dc3run     # so TestMain can reach the app

# 3. Schema.
docker run --rm --network sesamefs-cassandra-3dc_default \
  -e CASSANDRA_HOSTS=cassandra-na:9042 -e CASSANDRA_LOCAL_DC=dc-na \
  -e CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy \
  -e CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1 \
  sesamefs-3dc go run ./cmd/sesamefs migrate

# 4. P3 leg 1 -- fail-closed publication. dc-na MUST be stopped.
docker compose -f docker-compose.cassandra-3dc.yaml stop cassandra-na
docker exec \
  -e X2_DC_HOSTS='dc-na=cassandra-na:9042,dc-eu=cassandra-eu:9042,dc-asia=cassandra-asia:9042' \
  -e SESAMEFS_URL=http://sesamefs:8080 \
  -e CASSANDRA_HOSTS=cassandra-eu:9042 -e CASSANDRA_LOCAL_DC=dc-eu \
  -e P3_EXPECT_DC_DOWN=1 \
  dc3run go test -tags integration -count=1 ./internal/integration/ \
  -run TestP3_FencePublicationFailsClosedWhenADatacenterIsDown -v

# 5. P3 leg 2 -- the claim->orphan->finalize handoff, observed from dc-na.
#    All three DCs must be up, and P3_EXPECT_DC_DOWN must NOT be set.
docker compose -f docker-compose.cassandra-3dc.yaml start cassandra-na
docker exec \
  -e X2_DC_HOSTS='dc-na=cassandra-na:9042,dc-eu=cassandra-eu:9042,dc-asia=cassandra-asia:9042' \
  -e SESAMEFS_URL=http://sesamefs:8080 \
  -e CASSANDRA_HOSTS=cassandra-na:9042 -e CASSANDRA_LOCAL_DC=dc-na \
  dc3run go test -tags integration -count=1 ./internal/integration/ \
  -run TestP3_WriterInAnotherDatacenterObservesTheFence -v

# 6. Tear down.
docker rm -f dc3run
docker compose -f docker-compose.cassandra-3dc.yaml down -v
```

### On a host where Go binaries run normally

The script does all of the above in one command:

```bash
./scripts/x2-multidc-validation.sh --keep     # X2 legs, leaves the fixture up
./scripts/x2-multidc-validation.sh --p3       # P3 legs (skips the X2 legs entirely)
```

`--p3` runs the schema step and then the P3 legs, and exits before X2's
hinted-handoff disable and divergence build. It does NOT inherit X2 fixture state.

### Windows / blocked binaries

On hosts where an Application Control policy blocks executing freshly built
binaries, `go run` and `go test` fail from the host with:

```
fork/exec ...\xxx.test.exe: An Application Control policy has blocked this file.
```

`go build` and `go vet` still work; only *execution* is blocked. The script drives
`go` from the host, so it cannot be used there. Use the container route in the
TL;DR above -- that is how the current evidence in
`docs/GC-X2-MULTIDC-VALIDATION.md` was actually produced.

Two details that are easy to get wrong:

- **Two networks.** The test container needs `sesamefs-cassandra-3dc_default` to
  reach the three Cassandra nodes by name, *and* `sesamefs_default` to reach the
  app, because the integration `TestMain` exits 0 when `SESAMEFS_URL` is
  unreachable -- a run that proved nothing would look like a pass. `docker run`
  takes one network, so attach the second with `docker network connect`.
- **Git Bash mangles the env values.** `X2_DC_HOSTS` and
  `CASSANDRA_REPLICATION_DCS` contain colons and are rewritten by MSYS path
  conversion. Prefix the command with `MSYS_NO_PATHCONV=1`, or run it from
  PowerShell.

### The guards that stop a vacuous pass

Each of these exists because the obvious version of the test proves nothing.

| Guard | What it prevents |
|---|---|
| `P3_EXPECT_DC_DOWN=1` | leg 1 passing against a healthy cluster, where the publications would simply succeed |
| leg 2 skips when `P3_EXPECT_DC_DOWN` is set | running the handoff leg against a cluster with a DC missing |
| `p3RequireUnavailableAtEachQuorum` | `err != nil` counting as proof on the orphan `NotPublished` path. A write timeout on an LWT (`WriteType: CAS`) may still have been accepted, so only `Unavailable` **at EACH_QUORUM** is accepted there |
| claim `BlockClaimAmbiguous` (not `claimErr != nil`) | P4a SERIAL settlement returning `Ambiguous` with `err=nil` after it confirms an unowned row, **or** SERIAL seeing our claim while the renewed `EACH_QUORUM` visibility proof fails (that path carries a non-nil error). The worker fail-closes on Outcome; treating nil error as success is a false P3 regression. SERIAL ownership is not `Acquired` |
| post-refusal fence read from dc-eu **and** dc-asia | treating "the LWT errored" as "nothing exists". Required only when orphan publication is `NotPublished` (SERIAL-confirmed absence). `Ambiguous` may leave a partial row on survivors and must still not authorize `FinalizeBlockDelete` |
| `BlockExists` check after finalize in leg 2 | measuring an orphan read instead of the rowless-mint gate |
| fresh org/block id per run | a previous run's rows deciding this run's assertions |
| script requires the `--- PASS:` line | a skipped package reporting success |
| `SESAMEFS_REQUIRE_P4A_EVIDENCE=1` | the P4a claim-authority legs skipping their way to exit 0 against a stack that never came up. It is pinned in `docker-compose.yaml` **and** listed in the integration `TestMain` OR-chain — a gate missing from that chain still fails its own skip, but cannot stop `TestMain` from exiting 0 when nothing ran at all |
| `SESAMEFS_REQUIRE_P4B_EVIDENCE=1` | the P4b orphan-publication legs skipping their real-Cassandra write-once, SERIAL, lifecycle-advanced and read-repair assertions. It is pinned in `docker-compose.yaml` and listed in the integration `TestMain` OR-chain |
| `p4aRequireEvidence`'s `gate.observed` check | a leg that runs, passes, and asserts nothing: the test must reach its evidence log line, not merely avoid failing |
| `mutate()` in `scripts/p4a-mutation-validation.sh` compares the file after patching | a mutation whose pattern matched nothing being reported as "the guard held". Line endings and indentation differ between the working tree and the container's COPY, which is exactly how the X2 script caught an inert mutation |
| `expect_red` greps for the specific P4a assertion | a mutation that turns the suite red for an unrelated reason counting as evidence for the guard it was aimed at. A mutation that fails to COMPILE trips this too, which is how the takeover mutation was caught proving nothing |
| `m_stale_release_ignores_incarnation` mutates the store AND the mock | a green run hiding that the incarnation check lives in two mirrored places with neither protecting the other: the unit suite drives `MockStore`, so mutating production alone leaves it green |
| `TestP4A_*` destructive-path tests use `testSHA256BlockID` | passing for the wrong reason. A non-SHA-256 id is rejected by `ValidatePhysicalLocator` before the walk reaches the step under test, so the assertion would hold whether or not the invariant does |
| `TestP4A_MockSettledOwnClaimRequiresExactClaimedAt` | the in-memory uncertain-LWT path accepting a claim that production rejects because its canonical `claimed_at` does not match |

### Proving the legs can fail

A green leg means nothing until it has been shown to go red against the defect it
exists to catch. Both mutations are entry points, and both need a fixture that is
already up:

```bash
./scripts/x2-multidc-validation.sh --p3-mutate          # publishers -> LOCAL_QUORUM
./scripts/x2-multidc-validation.sh --p3-mutate-quorum   # publishers -> QUORUM
```

Each patches `internal/gc/store_cassandra.go`, re-runs leg 1, requires it to FAIL
with a `P3 REGRESSION` assertion, and restores the file. **`--p3-mutate-quorum` is
why the fixture has three datacenters**: at two DCs with RF 1, `QUORUM` is 2 of 2
and fails with a DC down exactly like `EACH_QUORUM`, so leg 1 would stay green and
the wrong fix would hide.

To run a mutation by hand in the container route, patch inside the container and
restore afterwards -- note the pattern has **no leading dot**, unlike X2's, because
the GC store puts `Consistency(...)` on its own line:

```bash
docker exec dc3run sh -c 'cp internal/gc/store_cassandra.go /tmp/sc.bak &&
  perl -0pi -e "s/\QConsistency(gocql.EachQuorum)\E/Consistency(gocql.Quorum)/g" \
  internal/gc/store_cassandra.go'
# ... re-run leg 1, expect FAIL ...
docker exec dc3run sh -c 'cp /tmp/sc.bak internal/gc/store_cassandra.go'
```

### P4a and P4b mutation evidence

The P4a mutation script covers the exact claim authority, serial settlement, settled-claim
`EACH_QUORUM` confirmation, the explicit no-retry/no-speculation LWT policy and the
`blocks.read_repair=BLOCKING` premise. Run it in Docker:

```bash
docker compose --profile test run --rm --build gotest bash scripts/p4a-mutation-validation.sh
```

### P4b orphan publication evidence

The P4b unit contract covers the write-once LWT, SERIAL settlement, canonical
`EACH_QUORUM` visibility and `pending_s3` before `SameTarget` success, stored
`first_seen_at`, target conflicts, advanced-phase refusal and projection uncertainty.
The mutation script proves those guards are load-bearing:

```bash
docker compose run --rm --build gotest bash scripts/p4b-mutation-validation.sh
```

The real-Cassandra legs are `TestP4B_OrphanPublicationIsWriteOnceAtRealCassandra`,
`TestP4B_SerialSettlementClassifiesRealCassandra`,
`TestP4B_LifecycleAdvancedAtRealCassandra`, and
`TestP4B_CanonicalOrphanReadRepairIsBlocking`.
The standard integration containers run them with `SESAMEFS_REQUIRE_P4B_EVIDENCE=1`.
P4a pins the same effective `read_repair=BLOCKING` contract on `blocks` via
`TestP4A_CanonicalBlockReadRepairIsBlocking` under `SESAMEFS_REQUIRE_P4A_EVIDENCE=1`,
because settled own-claim confirmation is an `EACH_QUORUM` read of that table.

### What these legs do NOT cover

The pinned **read** level. `BlockFenceReadConsistency` is `LOCAL_QUORUM` because
`database.consistency` accepts `ONE` and a `ONE` read can miss a committed fence.
With RF 1 per datacenter, `LOCAL_QUORUM` and `ONE` both resolve to the single local
replica, so they are indistinguishable on this fixture. That pin defends a
deployment with RF > 1 inside a datacenter, and stays covered by
`TestP3FenceReadConsistencyIsLocalQuorum` and the AST guard in
`internal/integration/p3_repair_authority_guard_test.go`.

Full runbook and closure findings: `docs/GC-X2-MULTIDC-VALIDATION.md`.

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
- Fixed all test scripts to use the then-current host-mapped backend port
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
