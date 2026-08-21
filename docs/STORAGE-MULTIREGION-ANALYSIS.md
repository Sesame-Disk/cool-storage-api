# Storage & Multiregion — Preproduction Assessment

**Date:** 2026-04-16
**Related:** [Security Assessment v4](./SECURITY-ASSESSMENT-2026-04-v4.md)

---

## Overview

This document analyzes the storage layer's readiness for multiregion production deployment where one region can fail while others continue operating. The goal: users might not be able to access data in the failed region, but should be able to create new libraries and use libraries in healthy regions.

---

## Architecture Summary

The system implements a sophisticated multiregion storage architecture:

- **Storage Manager** (`internal/storage/storage.go`): Central coordinator managing multiple backends
- **Storage Classes**: Logical grouping of backends (e.g., `hot-s3-usa`, `hot-s3-eu`, `hot-s3-asia`)
- **Region Classes**: Maps regions to storage classes (`usa` → `{hot: "hot-s3-usa", cold: "cold-glacier-usa"}`)
- **Endpoint Regions**: Maps hostnames to regions (`eu.sesamefs.com` → `eu`)
- **Health Tracking**: Each backend has health status (`Healthy`, `Degraded`, `Unhealthy`, `Failed`)
- **Failover Chains**: Each storage class can specify a `failover_class`

**Data flow:**
1. Request arrives at `eu.sesamefs.com`
2. Hostname mapped to region "eu"
3. Region "eu" mapped to storage class "hot-s3-eu"
4. Library created with `storage_class="hot-s3-eu"` as a preference for future
   first materializations
5. New block writes use the preferred class and health-aware selection; an
   actual failover class is persisted when selected
6. Existing reads, reuse and repairs resolve each block's persisted canonical
   `blocks.storage_class`, not the library preference
7. If EU is unhealthy, health-aware selection can use the configured
   `failover_class` for new materialization and selected fallback plumbing;
   changing the library preference or fallback choice does not move or
   reinterpret an existing canonical block

---

## Issues Found

### HIGH: No automated health monitoring for storage backends

**Files:** `internal/storage/storage.go` (CheckHealth, CheckAllHealth), `internal/api/server.go` (Run)

**Claim:** The Manager has `CheckHealth()` and `CheckAllHealth()` methods, but they are never called automatically. No background goroutine runs periodic health checks.

**Test:**
```bash
# Check if any goroutine starts health monitoring
grep -r "CheckAllHealth\|CheckHealth.*goroutine\|ticker.*Health" internal/api/
# Result: No matches

# Check server.go Run() function for background health monitor
# Result: None found (only GC service, rate limiters, OIDC state sweeper)
```

**Verified:** ✅ TRUE

**Impact:** A region can go down and the system won't know until an explicit health probe updates the health state. A user request that fails against the dead backend does **not** update it: ordinary storage errors never call `UpdateHealth`, so the class stays `HealthUnknown` and is still treated as healthy.

**Current behavior:**
1. EU S3 bucket becomes unreachable at 10:00 AM
2. Health status stays `HealthUnknown` (system assumes healthy)
3. User uploads file to EU library at 10:05 AM
4. Upload can fail with timeout/error
5. The storage error does not call `UpdateHealth()`
6. A later request is not guaranteed to fail over unless an explicit health probe has marked the class `Unhealthy` or `Failed`

First user sees the error. No proactive failover.

---

### MEDIUM: New-materialization and fallback paths use health-aware backend selection (CLAIM WAS FALSE)

**Files:** `internal/api/v2/files.go`, `internal/api/v2/blocks.go`, `internal/api/seafhttp.go`, `internal/api/sync.go`

**Original claim:** "Upload and download paths don't use health-aware backend selection"

**Test:**
```bash
grep -rn "GetHealthyBlockStoreForOrg" internal/api/
```

**Result:** Current call sites use `GetHealthyBlockStoreForOrg` through the
preferred-store and fallback helpers in `blocks.go`, `files.go`, `onlyoffice.go`,
`storage_resolution.go`, `seafhttp.go` and `sync.go`.

**Verified:** ❌ **FALSE** — The code uses `GetHealthyBlockStoreForOrg()` for
new-materialization and selected fallback paths.

**Canonical lookup qualification:** `internal/api/sync.go` first tries the
persisted class with `GetBlockStoreForOrg`; if that exact store is unavailable,
its lookup helper can prepare a health-aware fallback before the reader is built.

**Conclusion:** Health-aware selection is implemented for new materialization and
selected fallback paths. Existing canonical resolution, reuse and repair resolve
the persisted block class and do not silently reinterpret canonical metadata
through a failover class.

---

### MEDIUM: Failover chains are tested but not all edge cases

**Files:** `internal/storage/manager_test.go` (TestManagerGetHealthyBackend)

**Claim:** Failover chains and circular-detection edge cases are not fully tested.

**Existing tests:**
- ✅ Single-level failover (`TestManagerGetHealthyBackend`)
- ❌ Multi-level chain (EU → USA → Asia)
- ✅ Circular failover detection (EU → USA → EU) — `TestManagerGetHealthyBackendRejectsFailoverCycle`
- ❌ All backends in chain unhealthy

**Test result:**
```bash
go test -v ./internal/storage/... -run TestManagerGetHealthyBackend
# PASS: Single failover and circular rejection are covered
# No tests for multi-level chains or all backends unhealthy
```

**Verified:** ⚠️ **PARTIALLY TRUE** — Basic failover tested; edge cases not tested.

**Current code:** `GetHealthyBackend` now uses a visited set and returns a cycle
error instead of recursing indefinitely. The cycle is covered by
`TestManagerGetHealthyBackendRejectsFailoverCycle`; the remaining risk is
insufficient regression coverage for multi-level and all-unhealthy cases.

---

### MEDIUM: No cross-region integration tests

**Files:** `internal/integration/*_test.go`

**Claim:** No integration tests simulate backend failure and verify failover during upload/download.

**Existing tests:**
- ✅ `sharelink_region_test.go` — Creates library at EU endpoint
- ✅ `org_storage_policy_test.go` — Tests strict/flexible policy
- ❌ New materialization when preferred region is unhealthy
- ❌ Existing canonical download from a failed region, which should fail closed
  unless an explicit migration/replica path exists
- ❌ New block materialization after library creation when the preferred class is down

**Test:**
```bash
find internal/integration -name "*region*test.go" -o -name "*failover*test.go" -o -name "*multiregion*test.go"
# Result: Only sharelink_region_test.go found
```

**Verified:** ✅ TRUE — No failover integration tests exist.

**Gap:** System behavior during actual backend failure is untested end-to-end.

---

### LOW: Health check timeout is global (5s), not configurable

**Files:** `internal/storage/storage.go` (`CheckHealth`)

**Code:**
```go
// Line 337
start := time.Now()
_, err := store.Exists(ctx, "__health_check__")
elapsed := time.Since(start)

// Line 351-358
if elapsed > 5*time.Second {
    status = HealthDegraded
}
```

**Issue:** All regions use same 5-second timeout. EU region (300ms base latency) and USA region (10ms latency) treated identically.

**Verified:** ✅ TRUE — Timeout is hardcoded, not configurable per backend.

**Impact:** Distant regions may be marked `Degraded` due to geographic latency, not actual problems.

---

### LOW: No storage health metrics exposed

**Files:** `internal/api/server_routes.go` (no `/internal/storage/health` endpoint)

**Claim:** Health status is tracked internally but not exposed for monitoring.

**Test:**
```bash
curl -s http://localhost:8082/metrics | grep storage
# Result: No storage_backend_health metrics
# No storage_failover_total metrics
```

**Verified:** ✅ TRUE — No Prometheus metrics for storage health.

**Gap:** Operations team cannot monitor region health or set alerts.

---

## Test Coverage

### Existing tests: 13

| What's tested | Count | Type |
|---------------|-------|------|
| Manager backend registration | 1 | Unit |
| Region-based storage class resolution | 7 | Unit |
| Healthy backend selection (single failover) | 3 | Unit |
| Health check (healthy/unhealthy/nonexistent) | 3 | Unit |
| BlockStore caching | 1 | Unit |
| Health status string representation | 1 | Unit |
| Org storage policy (strict/flexible) | 4 | Integration |
| Share link region pinning | 1 | Integration |

### Critical gaps: 5

| Gap | Risk | What to add |
|-----|------|-------------|
| **Automated health monitoring** | High | Verify background goroutine calls CheckAllHealth every 30s |
| **New-materialization failover integration test** | High | New block with unhealthy preferred class → verify the actual failover class is persisted |
| **Canonical-read failure integration test** | High | Existing block with failed canonical class → verify no preference-based reinterpretation and a fail-closed response |
| **Multi-level failover chain** | Medium | 3-region chain (EU → USA → Asia) with middle down |
| **Circular failover detection regression** | Medium | EU → USA → EU returns an explicit cycle error |

---

## How to Test

```bash
# Unit tests
go test -v ./internal/storage/...

# Integration tests
go test -tags integration -v -run "TestOrgStoragePolicy\|TestShareLinkRegion" \
    ./internal/integration/...

# Test health check manually
# 1. Start system
docker-compose up -d

# 2. Create library via API
TOKEN="dev-token-superadmin"  # From configs/config.docker.yaml
REPO=$(curl -s -X POST http://localhost:8082/api2/repos/ \
    -H "Authorization: Token $TOKEN" \
    -d "name=test-library&desc=test" | jq -r .id)

# 3. Upload file
echo "test content" > /tmp/test.txt
curl -X POST http://localhost:8082/seafhttp/upload-api/$REPO \
    -H "Authorization: Token $TOKEN" \
    -F "file=@/tmp/test.txt" \
    -F "parent_dir=/"

# 4. Check backend health (currently must be done programmatically)
# TODO: Add /internal/storage/health endpoint

# 5. Stop MinIO to simulate region failure
docker stop cool-storage-api-minio-1

# 6. Try a new block upload again
# Expected: Should use failover if configured, and persist the actual class
# Actual: Will fail because no automated health monitoring

# 7. Restart MinIO
docker start cool-storage-api-minio-1
```

---

## Test Results Against Production

**Local (http://localhost:8082):**
```bash
# Health endpoint
curl -s http://localhost:8082/health
# Result: {"status":"ok"}

# Storage metrics
curl -s http://localhost:8082/metrics | grep -i storage
# Result: No output - NO storage metrics exposed ✅ Confirms issue

# Note: Local stack had connection issues during testing
# Container logs showed: "Failed to connect to database: failed to resolve Cassandra hostnames"
# Requires docker-compose recreation to fix networking issues
```

**Production (https://sfs.nihaoshares.com):**
```bash
# Health endpoint
curl -s https://sfs.nihaoshares.com/health
# Result: OK

# Storage health metrics
curl -s https://sfs.nihaoshares.com/metrics | grep storage
# Result: Blocked (403) - /metrics endpoint not publicly exposed in production ✅ Correct security posture

# Production testing conclusion: Cannot test storage internals from external endpoint
# Would need internal access to test failover behavior
```

---

## Best Practices Check

| Practice | Status | Notes |
|----------|--------|-------|
| **Multiregion architecture** | ✅ Yes | Region-to-class mapping, endpoint routing implemented |
| **Health-aware selection and fallback plumbing** | ✅ Yes | `GetHealthyBlockStoreForOrg()` is used for new placement and selected fallback; canonical reads do not reinterpret persisted placement |
| **Automated health monitoring** | ❌ No | CheckHealth() exists but never called automatically |
| **Failover chains** | ⚠️ Partial | Iterative visited-set cycle detection is covered; multi-level/all-unhealthy coverage is incomplete |
| **Cross-region testing** | ❌ No | Zero integration tests for failover scenarios |
| **Storage class migration** | ❌ No | `ChangeStorageClass` updates preference only; explicit data migration is not implemented |
| **Region quotas** | ❌ No | Global quotas only |
| **Failover notifications** | ❌ No | Server logs only |
| **Per-region metrics** | ❌ No | No Prometheus metrics for backend health |
| **Geographic latency handling** | ❌ No | Global 5s timeout; not configurable |
| **Data residency compliance** | ⚠️ Partial | `strict` constrains **new-library creation** only. `ChangeStorageClass` re-applies neither the region nor the hot-tier requirement (`ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01`), configured `failover_class` edges are not policy-gated, and existing data is never migrated |

---

## Recommendations

**Two readiness questions, not one.** Availability/resilience readiness and strict
residency enforcement are separate, and items 1-4 below only address the first:

```text
availability / resilience readiness   !=   strict residency enforcement
```

Automating health monitoring is the right fix for availability, but it makes the
configured cross-region `failover_class` edges live for the first time — the marking
logic exists in `CheckHealth`, but with no automatic caller no class is ever marked
`Unhealthy` or `Failed`, so failover never fires today. Doing item 1
without policy-gating placement therefore *increases* the chance that a `strict`
organization's new bytes land outside its region. The residency items are tracked as
`ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01` and must be decided alongside item 1, not
after it.

### Must-fix before multiregion production (HIGH priority)

1. **Implement automated health monitoring**
   - Add background goroutine in `server.go` calling `CheckAllHealth()` every 30s
   - Update health status based on probe results
   - Increment `ConsecutiveFails` on errors

2. **Add multi-level and all-unhealthy failover regression coverage**
   - Exercise a 3-region chain with the middle region down
   - Verify an all-unhealthy chain returns an error without looping

3. **Add cross-region integration tests**
   - New materialization when primary down → verify failover and persisted actual class
   - Existing canonical download from failed class → verify fail-closed behavior
   - 3-region chain with middle down → verify skip to end

4. **Expose health status in observability**
   - Prometheus metrics: `storage_backend_health`, `storage_failover_total`
   - `/internal/storage/health` JSON endpoint for debugging

### Should-fix soon (MEDIUM priority)

5. **Implement explicit storage class migration**
   - Keep `ChangeStorageClass` as a preference update
   - Add a separate durable job with byte/cost estimate, copy, verification and
     reference-safe cleanup

6. **Add per-region quotas**
   - Config: `region_quotas.{region}.max_total_gb`
   - Track usage in Cassandra

### Nice-to-have (LOW priority)

7. **Configurable per-region health check timeouts**
   - Account for geographic latency differences

8. **Failover notification webhooks**
   - Alert ops team when region down

---

## Conclusion

**Architecture:** ✅ Excellent design with multiregion support, failover chains, and health tracking.

**Implementation:** ⚠️ Core logic exists and is mostly used correctly
(`GetHealthyBlockStoreForOrg` is used for new placement and selected fallback), but critical gaps:
- No automated health monitoring (manual-only)
- Untested failover edge cases (multi-level and all-unhealthy chains; cycle rejection is covered)
- No integration tests for failures

**Testing:** ❌ Insufficient. Basic unit tests exist, but no failover integration tests.

**Residency enforcement:** ⚠️ Partial — `strict` binds new-library creation only.
`ChangeStorageClass` re-applies neither the region nor the hot-tier check, and
`failover_class` is not policy-gated. Items 1-4 do not close this, and item 1 makes
the failover half reachable.

**Production readiness for multiregion:** **Not ready**, on both counts:

- *availability/resilience* — blocked on items 1-4 above;
- *strict residency* — blocked on policy-gating `ChangeStorageClass` and failover
  (`ISSUE-LIBRARY-CLASS-CHANGE-RESIDENCY-01`), plus the missing permission gate on
  the same endpoint (`ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01`).

Single-region deployments with external monitoring do not need items 1-4: with one
region there is no cross-region failover to gate and no residency edge to cross.
That is a **storage-topology** statement and not a production-readiness verdict —
the missing permission gate on the same endpoints
(`ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01`) is topology-independent and
applies to a single-region install unchanged. A multiregion deployment that must
honour a residency commitment needs the residency items too, not only items 1-4.

**Corrections to v4 report:**
- ❌ "Upload/download paths don't use health-aware selection" — **FALSE** for new materialization and selected fallback plumbing; canonical reads use persisted placement
- ✅ "No automated health monitoring" — **TRUE**
- ⚠️ "Failover chains untested" — **PARTIALLY TRUE** (basic tested, edge cases not)
- ✅ "No cross-region integration tests" — **TRUE**
- ✅ "No health metrics exposed" — **TRUE**

---

## Assessment Environment

- **Local testing:** Docker Compose on macOS, port 8082
- **Stack:** sesamefs, cassandra, minio (multiregion buckets: usa, eu, china), onlyoffice
- **Code review date:** 2026-04-16
- **Branch:** security-and-architecture-report-v4
- **Storage layer:** `internal/storage/*` (815 lines)
