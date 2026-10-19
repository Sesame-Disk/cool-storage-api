# Testing Guide

This document describes how to run tests, test coverage, and testing infrastructure.

**Last updated: 2026-09-14 (library HEAD SERIAL-domain inventory/runner contract)**

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
| `schema-baseline-equivalence.sh [ref]` | Proves the working tree's migration set builds exactly the schema of `ref` (default `origin/main`): identical `system_schema` rows and seeds on a disposable Cassandra. Run whenever migrations are folded into `001` | 1 | Docker only |

| Go integration tests | `internal/integration/*_test.go` | Backend regression and end-to-end invariants | Backend |

**Important**: A new bash integration test that talks to the **default** backend must be registered in `test.sh` → `run_api_tests()` so `./scripts/test.sh api` runs it. Self-managed **3-DC** evidence scripts (X2, P3, H1, W2 post-HEAD, PC-0/PC-D1, library HEAD SERIAL domain) are **not** in that list: they need the three-datacenter Cassandra fixture, not the default stack, and are invoked by their dedicated scripts under [Cross-datacenter (3-DC) legs](#cross-datacenter-3-dc-legs). Unit mutation gates (`scripts/*-mutation-validation.sh`) are also run directly; they do not belong in `run_api_tests()`. For Go integration tests, add to `internal/integration/` with the `//go:build integration` tag. For backend refactors that touch canonical/projection consistency, integration coverage is the default entry point, not an optional follow-up.

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

### G1 exact orphan identity and recovery-root evidence

G1 is a cold-path recovery change. It adds no new Paxos operation: the existing
block-delete lifecycle CAS chooses `first_seen_at`, the recovery root is an
ordinary `EACH_QUORUM` write, the canonical orphan remains the existing LWT, and
the discovery projection is an ordinary `EACH_QUORUM` write. Root publication
failure is fail-closed and must leave the canonical orphan absent.

Focused local validation:

```bash
go test ./internal/gc -count=1 -run 'TestG1|TestP4B_CanonicalVisibilityClassification'
go test ./internal/gc -count=1 -run 'TestX1PhysicalLifeHandoffCurrentWriterStillFencesOnOrphan'
go test ./internal/db -count=1 -run 'TestR26MigrationDeclaresTheExactIdentityKeys|TestG1MigrationDeclaresExactOrphanRecoveryRoot'
./scripts/g1-mutation-validation.sh
```

G1 cost report:

- Each `StartBlockDeleteOrphan` publication attempt adds one `EACH_QUORUM`
  block/handoff-authority read through `readBlockDeleteClaimEachQuorum` and one
  ordinary `EACH_QUORUM` recovery-root write.
- G1 adds zero new LWTs and zero new `SERIAL` operations; the existing lifecycle
  CAS and canonical orphan LWT are unchanged.
- Recovery scans 32 fixed root buckets with the default page size of 100, using `O(pageSize)` working memory per page.
- Each root performs one exact canonical `EACH_QUORUM` read. Every visible canonical row then performs one idempotent ordinary `EACH_QUORUM` projection upsert; a missing canonical row performs the existing lifecycle observation in the `SERIAL` domain when classification requires it.
- `DeleteS3Orphan` settlement itself adds one exact canonical `EACH_QUORUM` read. If canonical state is absent, it adds one exact recovery-root `EACH_QUORUM` read and uses the durable root token before any settlement mutation.
- Terminal root reconciliation with canonical state absent can therefore perform one canonical read during root enumeration, then one canonical read and one recovery-root read during `DeleteS3Orphan`: up to three `EACH_QUORUM` point reads plus lifecycle observation.
- The normal upload/writer hot path has zero G1 operations and zero G1 latency delta; all added work is on GC/recovery cold paths.

The mutation harness preserves the frozen contract:

| Mutations | Invariant removed |
|-----------|-------------------|
| M1-M2 | Remove delete authority `D` from canonical or discovery identity |
| M3 | Omit `D` from exact canonical deletion |
| M4 | Remove the independent durable recovery root |
| M5 | Restore the orphan TTL |
| M6 | Allow `PREPARED` to authorize physical recovery |
| M7 | Delete the root before canonical/projection settlement |
| M8 | Remove Cassandra millisecond normalization from `gc_claimed_at` |
| M9 | Treat a missing canonical row as settled |
| M10 | Make the writer fence return no fence for a pending orphan |

M11-M17 retain the newer G1 checks for storage-key identity, root publication,
lifecycle-token stability, pagination, UTC-day scheduling, and independent root
errors. Docker isolation is required for the full environment run because
Cassandra/MinIO service names resolve only inside Compose:

```bash
CASSANDRA_HOST_PORT=19043 MINIO_API_HOST_PORT=19002 MINIO_CONSOLE_HOST_PORT=19003 SESAMEFS_HOST_PORT=13081 FRONTEND_HOST_PORT=13001 docker compose -p sesamefs-g1-wsl --profile test run --rm --build gotest bash scripts/g1-mutation-validation.sh
CASSANDRA_HOST_PORT=19043 MINIO_API_HOST_PORT=19002 MINIO_CONSOLE_HOST_PORT=19003 SESAMEFS_HOST_PORT=13081 FRONTEND_HOST_PORT=13001 docker compose -p sesamefs-g1-wsl --profile test run --rm --build go-integration-test
CASSANDRA_HOST_PORT=19043 MINIO_API_HOST_PORT=19002 MINIO_CONSOLE_HOST_PORT=19003 SESAMEFS_HOST_PORT=13081 FRONTEND_HOST_PORT=13001 docker compose -p sesamefs-g1-wsl --profile test run --rm --build go-all-test
```

Final audit snapshot for this implementation:

- Rebased main/base SHA: `a13c524b2008388f6841c62c50ec4eee344bed39`
- Prior G1 audit/evidence SHA: `107f86d8b578e9c431dc563594b6077ce2a9e78c`
- Final settlement/evidence SHA: `19cc0c4f56e7d36d464f68974fa774735a395570`
- Docker isolation: the commands above use Compose project `sesamefs-g1-wsl`;
  the recorded resources were Cassandra `sesamefs-g1-wsl-cassandra-1` and
  MinIO `sesamefs-g1-wsl-minio-1`, with host ports Cassandra `19043`, MinIO
  API `19002`, MinIO console `19003`, SesameFS `13081`, and frontend `13001`.
  The project owns volumes `sesamefs-g1-wsl_cassandra_data` and
  `sesamefs-g1-wsl_minio_data` plus network `sesamefs-g1-wsl_default`
  (`172.20.0.0/16`). G1 real-Cassandra evidence is required by
  `SESAMEFS_REQUIRE_G1_ORPHAN_EVIDENCE=1`.
- Result: local and Docker mutation suites were 17/17 expected RED; the real
  Cassandra stale-token G1 case passed; `go-integration-test` and `go-all-test`
  passed all configured integration/API/OIDC suites.
- Final post-settlement validation: `go test ./... -count=1` PASS.
- Final post-settlement validation: `go vet ./...` PASS.
- Final post-settlement validation: `git diff --check` PASS.

G1 remains subject to the X1 gate; no test or evidence in this section permits
`GC_ENABLED=true`.

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

The R31-A shared post-HEAD repair classifier has its own 3-DC gate, separate from X2/P3:
PC-0 publication-protocol characterization reuses this same 3-DC fixture. It
does not start a second stack. The opt-in gate is
`SESAMEFS_REQUIRE_PC0_PUBLICATION_CHARACTERIZATION=1`; skip or a missing
fixture cannot report green when that gate is armed.
`TestPC0PublicationMultiDCCharacterization` is a 3-DC topology +
characterization-matrix gate: it proves connectivity to `dc-na`/`dc-eu`/
`dc-asia` and records rows M1–M9. It does not execute those publication
races and does not re-run the W2/X2 publication scripts. Default
`go-all-test` does not inherit the gate. See
[`docs/PUBLICATION-PROTOCOL-CHARACTERIZATION.md`](./PUBLICATION-PROTOCOL-CHARACTERIZATION.md).

Running any `-tags integration` test against the 3-DC fixture by hand (the
PC-0 gate included) needs **both** `W2_POST_HEAD_3DC_HOSTS=dc-na=cassandra-na:9042,dc-eu=cassandra-eu:9042,dc-asia=cassandra-asia:9042`
and `CASSANDRA_HOSTS=cassandra-na:9042` (plus `CASSANDRA_LOCAL_DC=dc-na`,
`CASSANDRA_KEYSPACE=sesamefs`, `CASSANDRA_REPLICATION_CLASS=NetworkTopologyStrategy`,
`CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`) in the runner's
environment, exactly as `scripts/w2-*.sh` set them. Without `CASSANDRA_HOSTS`
the test passes but `TestMain`'s cleanup verification connects to the
default dev Cassandra (PasswordAuthenticator) and fails the package.

`scripts/pc0-initial-head-xdc-probe.sh` reproduces, on the same fixture,
the multi-DC HEAD-reversion variant of
`ISSUE-LIBRARY-INITIAL-HEAD-CONCURRENCY-01` with cqlsh (fixture up, schema
applied). It has two fail-closed modes: the default bug mode runs the
**pre-fix** unconditional initializer shape from a blind DC and requires
`RESULT: HEAD REVERTED` (the record of the bug); `--expect-cas-fix` runs the
production conditional shape (`IF head_commit_id = null AND created_at != null`)
from the same blind DC and requires it to be rejected and HEAD to survive.
Both modes first assert the CAS control leg (`[applied]=False`, real HEAD
reported). The probe validates CQL shapes.

`scripts/h1-initial-head-multidc-validation.sh` is the handler-level
counterpart (resolution evidence for the same issue). It runs **one full
cycle per initializer** (`sync`, then `v2`): seed a null-HEAD library visible
in every DC, initialize it from `dc-na` through the production
`InitializeLibraryFS` while `dc-eu` is stopped, restart `dc-eu` blind
(hinted handoff off), assert `dc-eu` is blind for that library (HEAD and
commit row) immediately before driving the initializer under test
(`InitializeLibraryFS`, or the Sync `createInitialCommit` path behind
`GET /commit/HEAD`) from `dc-eu`: it must keep and return the HEAD `dc-na`
published, leave exactly one commit row, and the commit behind that HEAD
must be servable from `dc-eu` at the consistency `GET /commit/:id` uses.
Separate libraries **and** separate blind windows are required: the first
Paxos round / read repair reconciles that library, and post-restart replay
reconciles other partitions on its own schedule (a second library was
observed already visible ~0.3 s after the first leg). Gate:
`SESAMEFS_REQUIRE_H1_INITIAL_HEAD_MULTIDC_EVIDENCE=1`
(`TestH1InitialHeadBlindDCDoesNotRevert3DC`); skip under the gate is FAIL.
The script manages the fixture and the runner itself (`--keep` leaves the
fixture up).

`scripts/library-head-serial-domain-multidc-validation.sh` is the 3-DC
evidence for `ISSUE-LIBRARY-HEAD-SERIAL-DOMAIN-01` (closed 2026-09-14). It is
**not** part of `./scripts/test.sh api` / `run_api_tests()`: like the other
self-managed 3-DC scripts above, it stands up
`docker-compose.cassandra-3dc.yaml` itself. Both DCs stay up. Test sessions
use default `SerialConsistency=LOCAL_SERIAL`; production HEAD LWTs pin global
`SERIAL`. Concurrent `UpdateLibraryHead` (H0→H1 vs H0→H2) and concurrent
`InitializeLibraryHeadIfUnset` each produce exactly one global winner. Gate:
`SESAMEFS_REQUIRE_LIBRARY_HEAD_SERIAL_DOMAIN_EVIDENCE=1`. Unit mutation:
`scripts/library-head-serial-domain-mutation-validation.sh` (M1–M28 RED:
SERIAL→LOCAL_SERIAL per seam, pin removed, constant degraded, a hidden
DELETE IF that names `head_commit_id` after another predicate or uses
`sesamefs.libraries`, a `Query(fmt.Sprintf(...))` HEAD DELETE whose CQL
is not source-resolvable, a package-level FuncLit DELETE IF, an
allowlisted `UpdateLibrary` Query turned into `IF head_commit_id`, a
dynamic SET fragment or non-literal lock `fmt.Sprintf` format inside an
allowlisted caller, an `updates := []string{"head_commit_id = ?"}`
initializer, `injectHead(&updates)`, a SET loop that does not range
`updates`, `poison(&query)`, an assignment to `update` inside the SET
loop, `Migrator.apply` `stmt = strings.Join(...)`, a lock helper format
turned into `DELETE FROM libraries ... IF EXISTS`, `poison(&stmt)` after a
safe CQL initializer, a second `SerialConsistency(localSerial)` on a HEAD
LWT chain, a second HEAD `Query.Exec()` inside an inventoried writer, a
`range` rebind of a CQL ident, an embedded migration that competes for HEAD, a
migration `UPDATE ... SET head_commit_id ... IF EXISTS`, a whole-row
`DELETE FROM libraries ... IF EXISTS`, and a concat
`UPDATE libraries SET` + `head_commit_id` Query outside the inventoried
CAS writers).
`CASSANDRA_SERIAL_CONSISTENCY` may still control other LWTs.

`scripts/library-hard-delete-lease-serial-domain-multidc-validation.sh`
is the 3-DC evidence for `ISSUE-GC-HARD-DELETE-LEASE-SERIAL-DOMAIN-01`, closed
in PR #234. It attaches only to this checkout's running compose backend (or
starts it), starts a three-DC Cassandra fixture named after the same compose
project with ephemeral host ports, configures all three client sessions with
`LOCAL_SERIAL`, and races library lease acquisition from dc-na and dc-eu. A
contender whose acquire has an unknown CAS outcome must return an error; if it
still owns the lease after its best-effort release, the round is logged as the
documented residual, cleared and re-raced (at most five rounds without an
owner). Two owners in any round fails.
Operations under the owner's own token (renew, release, next-owner and
stale-takeover acquire) are retried on an unknown CAS outcome because they are
idempotent; a non-owner renew or release may return an unknown outcome and the
authoritative read decides. The runtime does not retry renew/release (see the
residual in the known issue).
`EACH_QUORUM` reads verify the sole owner, cross-DC renewal and conditional
release, acquisition by the next owner, and stale takeover. The
required-evidence gate is
`SESAMEFS_REQUIRE_LIBRARY_HARD_DELETE_LEASE_SERIAL_DOMAIN_EVIDENCE=1`.
`scripts/library-hard-delete-lease-serial-domain-mutation-validation.sh`
applies seven directed mutations to the source copied into its Docker image
(the host tree is untouched) and checks that the focused Go contract tests go
RED for each mutation's own reason.

### PC-D1 inherited-continuity evidence

The PC-D1 decision model remains documentation/test-only, but PC-D1A now adds
migration 025 plus authority-only primitives on the canonical `libraries` row.
The baseline contract still requires `resolve/capture exact physical incarnation
P → establish non-expiring current-library liveness → revalidate exact P + GC
authority`; a bounded-TTL pin only bridges certification and cannot justify the
witness; late liveness cannot revoke a zero-proof already won by GC. PC-D1A
does not add a certifier, backfill, productive consumer, funnel migration, or
GC activation:

```bash
docker run --rm -v ${PWD}:/build -w /build sesamefs-pcd1a-gotest go test ./internal/db ./internal/publication
bash scripts/pc-d1a-certified-frontier-mutation-validation.sh
```

The PC-D1A real moving-HEAD proof is orchestrated by the host shell but every
Cassandra command and CQL assertion runs in Docker. It applies the migration
against the canonical `libraries` table, proves competing certification
converges, rejects stale and globally-visible/established soft-deleted
authority, and verifies atomic
frontier advancement across three DCs under session `LOCAL_SERIAL` with
explicit global `SERIAL` pins:

```bash
bash scripts/pc-d1a-certified-frontier-multidc-validation.sh
```

Both new runners are fail-closed and use only the `sesamefs-pcd1a-*` resource
prefix.

### PC-D1B identity-authority primitive evidence

The PC-D1B commits/fs_objects primitive (migration 026,
`internal/db/identity_authority.go`) is wired by PR #231 through
`internal/db/identity_gateway.go`. Every semantic writer and deleter uses the
gateway; `TestIdentityProductionWritersUseGateway`, the source-derived inventory
and the literal-CQL guard freeze the no-bypass boundary.
`TestIdentityAuthorityPrimitiveHasNoRawProductionCallerOutsideGateway` permits
raw claim primitives only inside the authority module and gateway. It uses `commit` and
`fs_object` claim keys; file/directory subtype stays in the digest. Mapping
authority representation and M18/M19 promotion are a separate follow-up, so a
SHA-1-only identity whose dependency is mapping-only remains Unproven and
ineligible for a #228 witness until that follow-up lands. Its own contract is
stated at the claim and digest layer, with no certifier in the picture:

```bash
docker run --rm -v "${PWD}:/build" -w /build sesamefs-pcd1b-gotest go test ./internal/db -short -run 'TestIdentity|TestFileIdentity|TestCommitIdentity|TestClaimIdentity|TestClassifyIdentity|TestIdentityAuthorityClaimsUsePerIdentityPartitionWithoutTTL'
PCD1B_MUTATION_IMAGE=sesamefs-pcd1b-gotest bash scripts/pcd1b-identity-authority-mutation-validation.sh
```

The contracts cover M16 (a claim survives source-row deletion, a different
digest or file/directory subtype conflicts on re-create, and a missing CAS
read-back is never a fresh first claim). A repo-wide Go/CQL guard permits only
the primitive's first-claim INSERT, authority SELECT and migration-026 CREATE;
it rejects UPDATE, DELETE, TRUNCATE, TTL and unapproved ALTER/DROP operations.
The serial-read contract also asserts <code>gocql.Serial</code> directly. M17 binds every immutable commit field
and the canonical SHA-256 file list, with ordered lists and length-delimited
fields. The 26 legacy M16/M17 directed mutations plus the B1-B12 wiring mutations prove that dropping the commit creator,
description or timestamp, dropping the file SHA-256 list, accepting noncanonical
UUID/digest spellings, omitting commit fields from the semantic UPDATE inventory,
weakening the claim/SERIAL pin, adding a repository-wide claim mutation, or
weakening the authority-only scope makes its specific assertion fail. The shared fs_object key is exercised against real Cassandra in
both delete/re-create directions. Each mutation must fail for its specific reason, not merely exit
non-zero. The canonical invocation runs `go test` inside the `sesamefs-pcd1b-gotest` Docker image via `PCD1B_MUTATION_IMAGE`; the host is not part of the validation path.

Real-Cassandra evidence is gated by `SESAMEFS_REQUIRE_IDENTITY_AUTHORITY_EVIDENCE=1`
(wired into `TestMain`, so an unreachable stack cannot print `ok`). Against
the default single-node stack it proves write-once/idempotent/conflict, claim
survival across a source-row delete and re-create, and one winner among
concurrent first claims. The isolated 3-DC leg proves that concurrent first
claims issued from three datacenters over `LOCAL_SERIAL` sessions still have
the stored winner read identically from every DC, including a concurrent
file/directory race for one `fs_id`, and survival across a cross-DC
delete/re-create. The assertion permits zero observed `Established` outcomes
if the winner's acknowledgement is ambiguous; the SERIAL read must still find
one digest matching exactly one contender:

```bash
SESAMEFS_URL=http://localhost:8080 SESAMEFS_REQUIRE_IDENTITY_AUTHORITY_EVIDENCE=1 \
  CASSANDRA_HOSTS=localhost:9042 go test -tags integration ./internal/integration/ -run 'TestIdentityAuthority.*OnRealCassandra'
bash scripts/pcd1b-identity-authority-multidc-validation.sh
```

The 3-DC runner uses only the `sesamefs-pcd1b-*` resource prefix and never
attaches to or stops the default stack. The mutation harness requires the specific failure for each directed
mutation rather than accepting any non-zero `go test` exit. Keep
`GC_ENABLED=false`; this evidence does not activate GC or migrate a funnel.

### PC-D1B.1 certified-baseline certifier

PR #228 implements the cold-path certifier for one library at one observed
HEAD. The certifier consumes the #230/#231 identity claims read-only, walks the
complete reachable tree, verifies each strict commit/fs_object projection,
then proves exact minted P, physical bytes, permanent current-library liveness,
and current GC authority before a HEAD-fenced witness. All Go commands run in
Docker. The isolated 3-DC runner owns its Cassandra keyspace, network, volumes,
backend, and MinIO under the `sesamefs-pcd1b1-*` prefix; it does not stop or
attach to the regular application stack. The fixture keeps `GC_ENABLED=false`.

```bash
# Full short suite, race checks, and static analysis

docker compose --profile test run --rm --build gotest
docker compose --profile test run --rm --build --entrypoint go gotest test -race ./internal/db
docker compose --profile test run --rm --build --entrypoint go gotest vet ./...

# Full Compose integration profile; start the normal test stack first as in §1.
docker compose --profile test run --rm --build go-integration-test

# Directed source mutations and isolated real 3-DC certifier evidence
bash scripts/pc-d1b1-certified-baseline-mutation-validation.sh
bash scripts/pc-d1b1-certified-baseline-multidc-validation.sh
```

The M1-M15 mutation runner edits only the certifier source, restores it on
success or exit, and requires each targeted contract to turn RED for its own
protocol regression. M14a bypasses reachable fs_object identity verification;
M14b bypasses commit H-to-R identity verification. M15a permits an
unauthoritative mapping to resolve a SHA-1-only dependency; M15b permits a
paired mapping to disagree with the claim-bound canonical list. The 15 frozen
contracts execute as 17 targeted legs (M1-M13 once, M14 and M15 twice each);
the meanings of M16/M17 remain unchanged. SHA-1-only files that require
unauthoritative mapping must return
`NOT_CERTIFIED/identity_unproven` before physical or liveness work.

Real-Cassandra certifier edge tests cover an authority-verified zero-block file, missing authority for that shape, a present but unauthoritative SHA-1 mapping, whitespace-bound root/directory-entry IDs, and Seafile's `EMPTY_SHA1` (`000...000`) as an authority-verified empty root and as empty file/directory entries with no `fs_objects` row; an `EMPTY_SHA1` root whose commit claim is missing or conflicting fails closed. Rejection cases assert no witness or physical/liveness work. The 3-DC runner executes these edge tests in its main certifier leg as well.

The 3-DC runner uses `LOCAL_SERIAL` client sessions while metadata claims,
HEAD/witness CAS, and ambiguous-settlement reads pin global `SERIAL`. It proves
exact-P bytes in MinIO, complete-tree certification, permanent liveness visible
at `EACH_QUORUM`, physical/GC revalidation, a P-change race, retry idempotence,
stale-HEAD rejection, and applied/non-applied ambiguous witness settlement. It
also proves complete fs_object A/B and commit H-to-R1/R2 source divergence fail
closed against the durable identity claims, unavailable SERIAL identity
authority returns UNKNOWN with no witness, and paired-mapping disagreement
cannot override authority. The partial-row phase uses hinted-handoff-disabled,
isolated remote-DC outages to retain a complete EU row while NA has a partial
file; certification fails without liveness, physical revalidation, or witness.
A separate outage leg proves missing `EACH_QUORUM` liveness also fails closed.
The fixture restores every stopped Cassandra node and hinted handoff before
cleanup; only its own prefixed resources are removed unless `--keep` is used.

This slice adds no mapping promotion/M18-M19, lifecycle serialization, first
productive consumer, PC-2, historical backfill, or GC activation. It preserves
`GC_ENABLED=false` and does not migrate a funnel.

### PC-D1B.3 Mapping Authority

PC-D1B.3 adds the write-once `block_mapping_authority_claims` table and the
cold-path promotion the certifier uses for SHA-1-only dependencies (see
[PC-D1B-METADATA-IDENTITY-AUTHORITY.md](./PC-D1B-METADATA-IDENTITY-AUTHORITY.md#pc-d1b3-mapping-authority-implementation-2026-09-23)).
All commands run in Docker:

```bash
# Private Docker project for the PC-D1B.3 checks below
PROJECT="sesamefs-pcd1b3-validation-$(date -u +%Y%m%d-%H%M%S)-$$"

# Unit, AST and migration contracts
docker compose -p "$PROJECT" --profile test run --rm --build --entrypoint go gotest test ./internal/db -run 'Mapping|Continuity|Certif|BlockMappingAuthorityClaims'

# Real Cassandra + MinIO in the same private project
SESAMEFS_HOST_PORT=0 CASSANDRA_HOST_PORT=0 MINIO_API_HOST_PORT=0 MINIO_CONSOLE_HOST_PORT=0 FRONTEND_HOST_PORT=0 ONLYOFFICE_HOST_PORT=0 \
docker compose -p "$PROJECT" --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION=0 \
  -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE=0 \
  go-integration-test go test -tags integration -count=1 ./internal/integration/ \
  -run '^TestBlockMappingAuthorityCertifierRealCassandra$|^TestUploadMappingWritersIssueNoAuthorityPaxosRealCassandra$'

# Directed mutations (67 source legs plus real-Cassandra T1) and isolated 3-DC evidence
bash scripts/pc-d1b3-mapping-authority-mutation-validation.sh --with-integration
bash scripts/pc-d1b3-mapping-authority-multidc-validation.sh

# Remove only the private project created for the commands above
docker compose -p "$PROJECT" down --volumes --remove-orphans
```

Final cross-audit verification (2026-09-26) ran in Docker: all 67 source
mutations and real-Cassandra T1 were RED as required (68/68);
`go test ./... -count=1`, `go vet ./...`, and
`go test -race -short -timeout 20m ./...` passed. The full
`go-integration-test` Compose profile passed in 287.127s, and the isolated
3-DC mapping-authority harness passed cross-DC promotion, concurrent conflicts,
SERIAL outage refusal, and recovery.

The two X1/BorrowedFS variables are set to `0` only for this focused `-run`.
The service enables those characterization gates for the full profile, and
they fail any filtered run that does not include their legs.

The real-Cassandra tests cover these cases:

- No mutable mapping, a mapping converged on bytes that do not hash to the
  external SHA-1, or hash-valid bytes from another representation, stays
  `identity_unproven` with no claim.
- A provable mapping is claimed, its projection is frozen, and the library
  certifies.
- These ordinary writes after promotion are all inert, and certification
  resolves only A:
  - a later write;
  - a late pre-fence write carrying an older timestamp;
  - a `DELETE`;
  - a write racing certification through `AfterLiveness`;
  - a write between the final recheck and the witness CAS through
    `BeforeWitnessCAS`.
- A claim whose projection diverged before it was frozen, a claim with
  unsupported evidence, or a claim whose block moved to another representation
  is `identity_conflict` with no witness and no repair.
- The upload mapping writers issue no LWT or authority statement on an
  observed session.

`--with-integration` adds T1. It rebuilds the integration image with the freeze
downgraded to an ordinary-timestamp rewrite, and requires the recheck-to-CAS
reproducer to fail because readers resolve B after the witness.

The 3-DC runner uses the `sesamefs-pcd1b3-*` prefix. It runs
MAPPING-3DC-1/1b/2/4/4b/5 plus the edge tests in one leg, then MAPPING-3DC-3 in
three phases: prepare, a global-`SERIAL` outage with dc-eu and dc-asia stopped,
and recovery. `GC_ENABLED=false` is unchanged.

Local-stack note: with GC enabled locally (`configs/config.docker.yaml`) and
G3 canonical retirement merged (#212), a later integration run can hit
`409 block_delete_in_progress` when it re-uploads a SHA-256 that GC already
retired and left as a COMMITTED orphan "retained for the future physical
executor" (X1 open). That is fixture state, not a product regression.

Fixed-size fixtures that only care about byte length (quota delta, 8 MiB CAS
blocks) must use `uniqueFixedSizeBlock`. Other upload bodies that are compared
for equality inside a single test but must not collide across runs should use
`uniqueText`. Tests that intentionally reuse a deterministic identity (for
example the S3-orphan fence that expects `block_delete_in_progress`), or
older fixtures not yet converted, may still require
`docker compose down -v` before a re-run.

The two 3-DC evidence scripts cited by PC-0 (`w2-sync-putblock-xdc-provenance-validation.sh`,
`w2-post-head-multidc-validation.sh`) were re-executed on 2026-09-10 and
pass (4/4 and 6/6), but both are sensitive to the seconds right after
`migrate` or a node restart (a cost leg timing out aborts the first; an
`EACH_QUORUM` seed timing out aborts the second). Re-run once the ring has
been `UN`×3 for a couple of minutes before treating a failure as a regression
(see `docs/TECHNICAL-DEBT.md`, "PC-0 audit follow-ups").

`scripts/w2-post-head-multidc-validation.sh`. It seeds a globally visible base
HEAD, publishes a child HEAD only in `dc-eu` while `dc-na` and `dc-asia`
are stopped, then runs the production repair classifier from `dc-na` after the
nodes return. The local `LOCAL_QUORUM` read must remain stale, while the
classifier must return `reachable` or fail closed as `unknown`; local
blindness must never authorize cleanup. It then converges that publication,
advances HEAD once more, proves the original target remains `reachable` as an
ancestor from another DC, and stops one DC to prove incomplete `EACH_QUORUM`
ancestry evidence returns `unknown` while retaining the repair row. After that
outage, a second pair of legs persists the SERIAL anchor/cursor without false
progress, then resumes the same row from two DCs after the live HEAD moved.
Those legs prove durable anchor/resume across an outage; they do not race two
workers through a 1024-node cursor CAS (unit tests cover cursor monotonicity).
This also
exercises delayed commit visibility: the original target commit was written
only in `dc-eu` before the classifier's authority reads. The W2 real Cassandra/MinIO evidence
separately pauses a writer before its HEAD CAS, runs repair, and verifies that
the queued row and `pub:` reference survive until the writer completes.

The ancestor leg also checks that the observed canonical HEAD equals the
generated advanced commit, so a target-at-HEAD result cannot satisfy the
ancestor assertion. The unavailable-DC leg requires an `EACH_QUORUM` ancestry
read to fail and the production bridge to return `(unknown, error)` before it
checks that the durable repair row remains.

Run it from the repository root; the script builds and runs its Go test runner
inside Docker and tears down the 3-DC fixture when complete:

```bash
./scripts/w2-post-head-multidc-validation.sh
# If the local backend Compose project is not named `sesamefs`:
COMPOSE_PROJECT_NAME=sesamefs-dev-wsl ./scripts/w2-post-head-multidc-validation.sh
```

The associated unit mutation gate now contains 32 mutations. In addition to
the earlier lease/settlement guards, it must go red if ancestry is skipped,
the 1024-node limit or a parent error becomes negative authority, partial
timeout progress is dropped, a missing repair row becomes `REACHABLE`, a
pre-HEAD genesis snapshot never re-anchors, clean genesis exhaustion is not
durable before a HEAD re-read, a re-anchor CAS loser that replays an
already-exhausted snapshot, a re-anchor loser that re-reads SERIAL HEAD past
the per-visit budget, a resumed chunk that forgets the anchored HEAD and
rotates through a cross-chunk cycle, the sweep listing progress-only residue
forever, the residue reaper becoming an unconditional delete or a whole-row
delete, a listed-live repair that keeps acting after it became residue, the
commit read
is weakened from `EACH_QUORUM`, or classification is reduced to HEAD-only:

```bash
docker compose --profile test run --rm --build gotest bash scripts/w2-post-head-mutation-validation.sh
```

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

# 4. P3 leg 1 -- fail-closed publication (claim, orphan handoff, orphan). dc-na MUST be stopped.
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

The P4a/R26 mutation script covers the exact claim authority, serial settlement,
settled-claim `EACH_QUORUM` confirmation, the explicit no-retry/no-speculation LWT
policy, the `blocks.read_repair=BLOCKING` premise and the cross-surface exact identity
keys. The three claim-policy mutations independently pin the source contract; they do
not claim that removing one makes the current driver defaults retry or speculate. Run
it in Docker:

```bash
docker compose --profile test run --rm --build gotest bash scripts/p4a-mutation-validation.sh
```

X1 strict-nonoverlap characterization uses source/AST contract mutation
validation (`scripts/x1-nonoverlap-mutation-validation.sh`): an isolated fixture
is parsed by the AST contracts; it is not runtime mutation testing of a mutated
binary. Perl is installed in `Dockerfile.gotest`. Run:

```bash
docker compose --profile test run --rm --build gotest bash scripts/x1-nonoverlap-mutation-validation.sh
```

BorrowedFS W1 own-liveness evidence is a separate eight-leg gate. The original
HEAD characterization remains a historical report in
`docs/R3-BORROWEDFS-HEAD-CHARACTERIZATION.md`; W1's production behavior is covered
by `internal/integration/borrowedfs_own_liveness_test.go`. It does not inflate
X1's 15 legs. Directed run:

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_P2_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P3_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4A_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4B_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R26_EVIDENCE= \
  -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE=1 \
  go-integration-test \
  go test -tags integration -run '^TestBorrowedFSOwnLiveness|^TestBorrowedFSHeadCharacterizationIsDocumented$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v -count=1 -timeout 15m ./internal/integration
```

X1 physical-life handoff plan (`docs/GC-X1-PHYSICAL-LIFE-HANDOFF-PLAN.md`) is a
docs freeze (D0). The G1/G2 runtime implementation is tracked by PRs #207/#209;
contract tests in `internal/gc/x1_physical_life_handoff_plan_test.go` pin CURRENT
vs DECIDED and the still-open P4c-orphan / fence facts:

```bash
docker compose --profile test run --rm --build gotest \
  go test ./internal/gc -count=1 -run 'TestX1PhysicalLifeHandoff'
```

SessionUpload W2 evidence is a separate six-leg gate, covered by
`internal/integration/sessionupload_own_liveness_test.go` and backed by real
Cassandra/MinIO. The retry leg forces a pre-HEAD failure, releases the session
claim, and commits again; it does not exercise the already-Committed early
return. The exact-placement legs also cover GC-first and fully-retired-before-
renewal, proving that recreating `up:` after D does not revoke D and that the
pre-HEAD check rejects the condemned placement.

Directed SessionUpload run (unset every unrelated gate because `TestMain` checks enabled
gates after `-run` filtering):

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_P2_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P3_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4A_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4B_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R26_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R3_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE=1 \
  go-integration-test \
  go test -tags integration -run '^TestSessionUploadOwnLiveness|^TestSessionUploadOwnLivenessEvidenceRequiresEveryNamedLeg$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v -count=1 -timeout 15m ./internal/integration
```

Directed W2-6 UploadFile exact-placement run (`docs/X1-CRITICAL-PATH.md` §4;
three named legs: `writerFirst`, `gcCommittedBeforeStage`,
`gcFullyRetiredBeforeStage`; uses the integration-only
`SetUploadFileAfterMaterializedBarrierForTest` hook):

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_P2_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P3_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4A_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4B_EVIDENCE= \
  -e SESAMEFS_REQUIRE_G1_ORPHAN_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R26_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R3_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_W2_UPLOADFILE_EXACT_P_EVIDENCE=1 \
  go-integration-test \
  go test -tags integration -run '^TestW2UploadFileExactPlacementBeforeHead$|^TestW2UploadFileExactPEvidenceRequiresEveryNamedLeg$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v -count=1 -timeout 15m ./internal/integration
```

Directed W2 post-HEAD run (the W2 gate is explicit here; all unrelated gates are
unset):

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_P2_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P3_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4A_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4B_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R26_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R3_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_W2_POST_HEAD_EVIDENCE=1 \
  go-integration-test \
  go test -tags integration -run '^TestW2CreateFilePostHeadEvidenceAgainstRealCassandra$|^TestPublishedBlockReferenceRepairWorker_ReplaysReachableQueuedRepairAfterRestart$|^TestW2PublishedRepairReachabilityConvergesUnderMovingHEAD$|^TestW2PublishedRepairSweepReapsProgressOnlyResidue$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v -count=1 -timeout 15m ./internal/integration
```

Repair settlement intentionally remains an ordinary idempotent delete, matching
the ordinary repair-row insert. Retry backoff is process-local advisory state;
the unit suite verifies it is stored and that forgetting it on restart is safe.
There is no retry mutation to race with settlement or to resurrect a deleted row.

W2 source mutation evidence is also Docker-only:

```bash
docker compose --profile test run --rm --build gotest bash scripts/w2-post-head-mutation-validation.sh
```

The script currently covers 35 mutations and must report 35/35 expected RED.
The contract guards cover conditional settlement delete/insert regressions,
loss of process-local retry state, loss of expired retry-hint pruning, a retry
that re-anchors to a live HEAD on bound/timeout (forbidden), a pre-HEAD genesis
that never re-anchors after the target is published (required), clean genesis
exhaustion that is not durable before a HEAD re-read, post-renewal gone
compensation of a stable repair-owned `pub:`, a re-anchor CAS loser
that replays an already-exhausted snapshot, root-as-negative-authority,
queue INSERT writing cursor columns, UNKNOWN skipping `pub:` renewal, repair
liveness reusing the commit-scoped `pub:<commitID>` identity, progress LWTs
ignoring the loaded `created_at` generation, an unbounded re-anchor SERIAL HEAD
budget, a resumed chunk without the anchored-HEAD cycle seed, and the
progress-only residue reaper being removed, made unconditional, or turned into
a whole-row delete, and hydrate trusting a listed copy's ordinary cells after
the row became residue; plus the two observability guards (a sweep that
could not list a bucket still stamping the complete-sweep timestamp and
publishing a short backlog; a visit whose durable renewal failed reported as
retained — `docs/PUBLISH-REPAIR-OBSERVABILITY.md`). The W2 evidence gate also requires the real-Cassandra
`progress_residue_reap` leg (`TestW2PublishedRepairSweepReapsProgressOnlyResidue`):
an UPDATE-only residue row is reaped by one production sweep while a queued row
survives both the conditional reap and the sweep; a requeue landed with
`USING TIMESTAMP` one minute older than the reaper's tombstones (the
reconciliation outcome of an ordinary INSERT the Paxos quorum had not yet seen)
survives the cell-only reaper and is not reaped again, and a negative control
shows a whole-row conditional DELETE loses that same requeue. This
suite does not claim that scheduler scaling or X1 is closed.

Canonical full run: `docker compose --profile test run --rm --build go-integration-test`
(or `go-all-test`). Both canonical commands pass the W2 gate and the W1/R3/X1
gates inline; the service environments leave W2 disabled for directed runs. The
W2 evidence combines the shared upload-link engine with a real in-process
`CreateFileFromBlocks` pre-HEAD/CAS race; it does not claim every publication
funnel is closed. The existing X2/P3 multi-DC harness is a separate workflow and
is not changed by this PR. Its local-blindness wording does not by itself claim
that SERIAL is indispensable.

### W2 Sync `PutBlock` -> HEAD publication continuity evidence

`SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_HEAD_EVIDENCE=1` gates `TestW2SyncPutBlockHeadEvidence`
(`internal/integration/sync_w2_putblock_head_integration_test.go`), which provides scoped evidence
for the PutBlock-provenanced subset of the two `CONDITIONAL` Sync rows of
`docs/R3-LIVENESS-CONTINUITY.md` — it does not close the complete W2/R31 rows. It proves own
liveness is renewed and exact placement is fail-closed validated before HEAD, in both
`handleSyncHeadPromotion` and `tryAutoMergeSyncHeadPromotion`, with a durable per-file repair
row staged before HEAD and settled only on positive reachability. Unlike the W1/W2 BorrowedFS
legs, this suite drives the real Sync HTTP protocol end to end (`PUT .../commit/{id}`,
`POST .../recv-fs`, `PUT .../block/{sha1}`, `PUT .../commit/HEAD?head=...`) rather than an
in-process handler + `gin.Context`, because that is how every other Sync integration test in
this package already covers the protocol, and because the crash leg below needs the real
deployed server process to exit. Five primary named legs, each real Cassandra/MinIO, no mocks,
plus a separately gated crash leg:

- `normalFlowRenewsLivenessAndSettles` — forces the `up:sync:<repo>:<block>`
  reference's Cassandra-native TTL down to a few seconds right after `PutBlock`,
  then proves HEAD renews it back to ~48h before publishing, the permanent
  `fs:` reference is registered, and the durable repair row is cleared.
- `crossNodeIdempotentRepairSettlesWithoutProcessMemory` — commits and
  publishes HEAD on one node, strips the permanent `fs:` reference to simulate
  a crash between the HEAD CAS and finalize, then replays the same already-applied
  target HEAD as an idempotent retry from a *different* node (so the first
  node's in-process `finalizedBlockDeltas` memo cannot short-circuit the repair
  this leg is proving) and confirms `repairPublishedSyncCommitBlockDelta`
  re-promotes the reference and clears the repair row.
- `activeGCClaimBlocksHeadFailClosed` — after a real `PutBlock`, acquires a
  real GC delete claim (`gcpkg.CassandraStore.ClaimBlockDelete`) on the block's
  exact canonical placement, then proves HEAD fails closed (503, HEAD
  unchanged) rather than publishing a placement GC is actively working to
  reclaim.
- `divergentCASLoserRetainsSharedRepairRow` — races two real, genuinely divergent commits at the same parent HEAD; the winner settles,
  the loser gains no permanent fs reference, and its shared repair row remains durable.
  Request-local loser cleanup removes only the attempt-local pub references.
  This is the ownership contract for direct HEAD repair rows.
  It does not claim durable known-loser classification or R31 closure.
- `autoMergeProductionPathSettlesWithRealBlocks` — drives the same
  non-overlapping-entries auto-merge shape `TestSyncHeadConflictAutoMergesNonOverlappingEntries`
  already covers structurally, but with real `PutBlock`-provenanced blocks on
  both branches, so the pre-HEAD readiness gate actually runs against
  canonical blocks on `tryAutoMergeSyncHeadPromotion`, not synthetic empty
  `fs_objects`.

- `crashAfterHeadCASBeforeFinalizeReplays` - with the crash gate and service failpoint enabled, the node exits after HEAD CAS and before finalize; another node waits for restart, replays the same HEAD, promotes the permanent fs reference, and confirms the repair row is settled.

Scope, stated the same way the production code itself scopes it: only blocks
with an already-established `up:sync:<repo>:<block>` reference are renewed and
fenced. A block whose own liveness has already lapsed (TTL expired before this
readiness gate runs) is observationally identical to a block with no PutBlock
provenance at all, and is deliberately left untouched rather than reclassified
into this guarantee — see `docs/KNOWN_ISSUES.md`'s extension of
`ISSUE-PUBLISH-REPAIR-REACHABILITY-01`. This suite does not attempt to
construct that boundary case as a "must fail closed" leg; doing so would
require broadening the fail-closed check to every added block regardless of
provenance, which is the exact scope expansion this slice's own audit rejected.

Directed run (unset every unrelated gate):

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_REQUIRE_P2_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P3_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4A_EVIDENCE= \
  -e SESAMEFS_REQUIRE_P4B_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R26_EVIDENCE= \
  -e SESAMEFS_REQUIRE_R3_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION= \
  -e SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_SESSIONUPLOAD_OWN_LIVENESS_EVIDENCE= \
  -e SESAMEFS_REQUIRE_W2_POST_HEAD_EVIDENCE= \
  -e SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_HEAD_EVIDENCE=1 \
  go-integration-test \
  go test -tags integration -run '^TestW2SyncPutBlockHeadEvidence$|^TestW2SyncPutBlockHeadEvidenceRequiresEveryNamedLeg$|^TestEveryEvidenceGateIsWiredIntoTestMain$' -v -count=1 -timeout 15m ./internal/integration
# Optional real process-crash leg: set SESAMEFS_SYNC_W2_CRASH_AFTER_HEAD_CAS_ONCE=1 for the test-profile services and SESAMEFS_REQUIRE_W2_SYNC_PUTBLOCK_HEAD_CRASH_EVIDENCE=1 for the test runner.
```

Mutation evidence is also Docker-only:

```bash
docker compose --profile test run --rm --build gotest bash scripts/w2-sync-putblock-head-mutation-validation.sh
```

Canonical full run: `docker compose --profile test run --rm --build go-integration-test`
(or `go-all-test`); these commands run the primary scoped gate inline alongside the other W1/W2/R3/X1
gates. Remaining W2/R31 funnels ("Sync commit whose block had no associated
PutBlock", `recv-fs-before-put`, SeafHTTP full W2, OnlyOffice full W2, cross-repo
publication continuity), G1, and X1 are unaffected and remain open.

### W2 Sync PutBlock cross-DC provenance evidence (ISSUE-SYNC-PUTBLOCK-CROSS-DC-PROVENANCE-VISIBILITY-01)

Unit-level routing (local hit/error never reach the fallback; local miss +
global hit/miss/error) and bounded fail-fast (a failing fallback stops
admitting additional block-level provenance checks after at most
`syncCommitBlockPlacementConcurrency` (20) are admitted/in flight, not after
`N` -- each admitted check may itself issue both a `LOCAL_QUORUM` and an
`EACH_QUORUM` read, and later blocks' goroutines still get created, they just
return immediately via the cancelled context instead of making their own
external call) are covered by `internal/api/sync_w2_putblock_xdc_provenance_test.go`
and run as part of the normal `gotest` service. `internal/db/block_references_test.go`
separately pins the fallback's `EACH_QUORUM` consistency by value
(`TestSyncBlockReferenceCrossDCFallbackConsistencyIsEachQuorum`), not just
that it declares one, and pins that `BlockReferenceExistsEachQuorum`'s call
site actually binds to that named constant
(`TestBlockReferenceExistsEachQuorumBindsTheNamedConsistencyConstant`).
Mutation evidence (M12 bypass the fallback, M13 remove fan-out cancellation,
M14 weaken the consistency constant, M15 rebind the call site away from the
named constant, M16 Sync success deletes repair-owned `pub:` on the hot path) is included in the same mutation script as the rest of the
slice:

```bash
docker compose --profile test run --rm --build gotest bash scripts/w2-sync-putblock-head-mutation-validation.sh
```

The real cross-DC recovery itself needs an actual 3-DC Cassandra fixture (a
single-DC keyspace cannot distinguish `LOCAL_QUORUM` from `EACH_QUORUM`) and
is a separate, standalone script, following the same pattern as
`scripts/w2-post-head-multidc-validation.sh`:

```bash
./scripts/w2-sync-putblock-xdc-provenance-validation.sh
```

It simulates a real `PutBlock` landing in `dc-eu` (via the production
`AddProvisionalBlockReferenceWithExpiry` primitive, not a reimplementation)
while `dc-na` and `dc-asia` are stopped with hinted handoff disabled, then
restarts them, waits for gossip to actually re-converge to three `UN` nodes
(Docker's healthcheck alone can report "healthy" slightly before that), then
polls a real `EACH_QUORUM` read against the already-migrated schema until it
actually succeeds (gossip alone proved insufficient -- a restarted node can
be ring-visible before it reliably participates in `EACH_QUORUM`
coordination), and immediately queries the real production scope-gate function
(`syncBlockHasOwnLivenessProvenanceFn`, via the
`SyncBlockHasOwnLivenessProvenanceForIntegration` `//go:build integration`
wrapper in `internal/api/sync_w2_putblock_xdc_integration.go`) from `dc-na`,
before any hint/repair delivery could have converged the write. The same
write phase also seeds `W2_SYNC_XDC_BLOCK_COUNT` (default 1111, the disjoint N=1/10/100/1000 scenarios' combined total) additional
blocks under a shared prefix so `TestW2SyncXDCAllCrossDCHitCostAtN3DC` can
measure the all-cross-DC-hit cost scenario at N=1/10/100/1000 -- the one
scenario the single-DC characterization below cannot produce, since a
single-DC keyspace can't tell `LOCAL_QUORUM` from `EACH_QUORUM`. The script
then stops `dc-asia` alone and confirms the fallback fails closed (a bounded
error, not a hang, not a silent absence) rather than treating "one DC down"
as ordinary absence. No `sesamefs` application instance is required per
datacenter -- every leg calls the real production function directly through
a `*db.DB` connected to a specific datacenter, the same pattern
`internal/integration/publish_repair_multidc_integration_test.go` already
uses for the post-HEAD classifier.

Cost/availability characterization (single-DC dev stack, with the WAN
caveat documented in the test's own doc comment and in
`docs/KNOWN_ISSUES.md`):

```bash
docker compose --profile test run --rm --build \
  -e SESAMEFS_MEASURE_W2_SYNC_XDC_COST=1 \
  go-integration-test \
  go test -tags integration -run '^TestW2SyncXDCProvenanceCostCharacterization$' -v -count=1 -timeout 15m ./internal/integration
```

### P4b orphan publication evidence

The P4b unit contract covers the write-once LWT, SERIAL settlement, canonical
`EACH_QUORUM` visibility and `pending_s3` before `SameAuthority` success, stored
`first_seen_at`, target conflicts, advanced-phase refusal and projection uncertainty.
The mutation script proves those guards are load-bearing:

```bash
docker compose run --rm --build gotest bash scripts/p4b-mutation-validation.sh
```

P4b-2 / R14b adds the irreversible orphan-handoff commit and exact `(P, D)` binding,
plus the audit-closure tombstone on `gc_block_delete_lifecycles` (migration `020`).
`AlreadyCommitted` / `CommittedOwner` require `EACH_QUORUM` visibility. `AlreadyFinalized`
is classification only (exact published certificate); only applied `Finalized`
authorizes `processBlock` S3. Publication SERIAL-re-reads the tombstone after the
orphan INSERT; `pending_s3` recovery SERIAL-observes D before S3. Its sibling
mutation script must stay red when those predicates are removed:

```bash
docker compose run --rm --build gotest bash scripts/p4b-authority-mutation-validation.sh
```

The real-Cassandra legs are `TestP4B_OrphanPublicationIsWriteOnceAtRealCassandra`,
`TestP4B_SerialSettlementClassifiesRealCassandra`,
`TestP4B_LifecycleAdvancedAtRealCassandra`,
`TestP4B_CanonicalOrphanReadRepairIsBlocking`,
`TestP4B_ClaimOrphanAuthorityIsBoundAtRealCassandra`, and the P4b-2 audit legs in
the same file (late loser, crash post-handoff, crash post-orphan, second
`CommitHandoff`, finalize vs terminal, P1/P2, replay-after-terminal,
recovery terminal+orphan does not DELETE S3).
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
