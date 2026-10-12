# Release Criteria & Stability Procedure

**Last Updated**: 2026-02-04

This document defines the rules for when a component is considered **stable**, when it can be **frozen**, and what must be true before we ship a **production release**.

---

## The Problem

Currently, freeze decisions are ad-hoc ("user says freeze it"). There's no systematic link between "component X has tests Y, Z" and "component X is stable enough to freeze." Coverage is measured but not enforced. This leads to:

- Components marked ✅ COMPLETE with no tests
- No way to know if a "complete" component has regressed
- No clear threshold for "this is production-ready"

---

## Component Lifecycle

Every component moves through these stages:

```
TODO  →  PARTIAL  →  COMPLETE  →  RELEASE-CANDIDATE  →  FROZEN
 ❌        🟡          ✅             🟢                   🔒
```

### New stage: RELEASE-CANDIDATE (🟢)

A component that meets all freeze prerequisites but hasn't been frozen yet. This is the "soak" period. The component stays 🟢 for a minimum number of sessions before it can be promoted to 🔒 FROZEN.

---

## Freeze Prerequisites (All Must Be True)

A component can move from ✅ COMPLETE to 🟢 RELEASE-CANDIDATE only when **all** of these are satisfied:

### 1. Test Coverage Thresholds

| Layer | Threshold | How to Measure |
|-------|-----------|----------------|
| **Go unit tests** | ≥ 80% line coverage for the component's package(s) | `go test -coverprofile=coverage.out ./internal/<pkg>/...` then `go tool cover -func=coverage.out` |
| **Integration tests** | ≥ 90% of the component's API endpoints have at least one integration test | Count in the Component Test Map (see below) |
| **Frontend** (if applicable) | ≥ 60% line coverage for the component's React files | `npm test -- --coverage --collectCoverageFrom='src/components/<dir>/**'` |

**Why not 90%+ everywhere?** Frontend coverage at 0.6% today means 90% is unrealistic as a gate. We start with 60% for frontend and 80% for Go — these are achievable targets that still provide meaningful safety. Raise them after the first release.

### 2. Component Test Map Entry

Every component must have an entry in the **Component Test Map** (section below) that lists:
- Which Go test files cover it
- Which integration test scripts cover it
- Which frontend test files cover it (if applicable)
- Current coverage percentage
- Date coverage was last measured

**No entry = cannot freeze.** This is the single source of truth for "what tests cover what."

### 3. Zero Open Bugs

- No open entries in `docs/KNOWN_ISSUES.md` with the component's name
- If a bug was filed and fixed, the fix must have a regression test

### 4. Soak Period

After entering 🟢 RELEASE-CANDIDATE:
- Must pass **3 consecutive sessions** with all tests green and no new bugs filed
- The session count resets to 0 if:
  - Any test covering the component fails
  - A new bug is filed against the component
  - The component's code is modified (bug fixes reset the counter)

---

## How to Promote a Component

### To RELEASE-CANDIDATE (🟢)

1. **Check coverage**: Run coverage commands, record numbers in Component Test Map
2. **Check bugs**: Search `docs/KNOWN_ISSUES.md` for the component name
3. **Check test map**: Verify entry exists and is current
4. **Update status** in `docs/IMPLEMENTATION_STATUS.md`: change to 🟢 RELEASE-CANDIDATE
5. **Record date** the soak period started

### To FROZEN (🔒)

1. **Verify soak period**: 3+ sessions since entering 🟢, all green
2. **Run full test suite**: `./scripts/test.sh all` — must pass
3. **Update status** in `docs/IMPLEMENTATION_STATUS.md`: change to 🔒 FROZEN
4. **Add to frozen list** in `CURRENT_WORK.md` and `CLAUDE.md`
5. **Record freeze date**

### Unfreezing (🔒 → back to ✅)

Only when:
- User explicitly requests it, OR
- A critical production bug is found

When unfreezing: the component drops back to ✅ COMPLETE. After the fix, it must go through the full 🟢 → 🔒 cycle again.

---

## Component Test Map

This is the authoritative registry of what tests cover what. **Update this every time you add or modify tests.**

Format:
```
### <Component Name>
- **Status**: ✅ / 🟢 / 🔒
- **Go packages**: `internal/<pkg>/`
- **Go unit test coverage**: X% (measured YYYY-MM-DD)
- **Go test files**: list
- **Integration test scripts**: list
- **Integration endpoint coverage**: X/Y endpoints (Z%)
- **Frontend files** (if any): list
- **Frontend test coverage**: X% (measured YYYY-MM-DD)
- **Open bugs**: none / list
- **Soak started**: YYYY-MM-DD (or N/A)
- **Sessions since soak start**: N
```

---

### Sync Protocol
- **Status**: 🔒 FROZEN (2026-01-16)
- **Go packages**: `internal/api/sync.go`
- **Go unit test coverage**: sync.go part of `internal/api` at ~19%
- **Go test files**: (tested via integration)
- **Integration test scripts**: `scripts/test-sync.sh`
- **Integration endpoint coverage**: 13/13 sync endpoints (100%)
- **Frontend files**: N/A (protocol only)
- **Open bugs**: none
- **Soak started**: 2026-01-13
- **Frozen**: 2026-01-16 (pre-dates this procedure — grandfathered)

### Crypto (Encryption / Key Derivation)
- **Status**: 🔒 FROZEN (2026-02-04)
- **Go packages**: `internal/crypto/`
- **Go unit test coverage**: 90.8% (measured 2026-02-04)
- **Go test files**: `internal/crypto/crypto_test.go` (14 tests + 2 benchmarks), `internal/crypto/coverage_test.go` (25 tests)
- **Integration test scripts**: `scripts/test-encrypted-library-security.sh`, `scripts/test-sync.sh`
- **Integration endpoint coverage**: 2/2 encryption endpoints (set-password, change-password) = 100%
- **Frontend files**: N/A
- **Open bugs**: none
- **Frozen**: 2026-02-04 — 90.8% Go coverage, all error paths tested, stable since 2026-01-13 (20+ sessions without code changes)

### Garbage Collection
- **Status**: ✅ COMPLETE (major overhaul 2026-03-17)
- **Go packages**: `internal/gc/`
- **Go unit test coverage**: ~65% (measured 2026-03-17)
- **Go test files**: `gc_test.go`, `queue_test.go`, `worker_test.go`, `scanner_test.go`
- **Integration test scripts**: `scripts/test-gc.sh` (21 assertions)
- **Integration endpoint coverage**: 4/4 admin GC endpoints (100%)
- **Frontend files**: N/A
- **Open bugs**: none
- **2026-03-17 overhaul**: 7 item types, 8 scanner phases, cascade deletion, artifact cleanup, stats persistence, reverse lookup table, iterative tree walk. ISSUE-GC-ORPHANS-01 resolved.

### Permission Middleware
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/middleware/`
- **Go unit test coverage**: ~30% (measured 2026-01-29)
- **Go test files**: `internal/middleware/permissions_test.go`
- **Integration test scripts**: `scripts/test-permissions.sh` (24 assertions)
- **Integration endpoint coverage**: all routes have permission checks applied
- **Frontend files**: N/A (backend only)
- **Open bugs**: none
- **Soak started**: N/A (coverage below threshold)

### File Operations (CRUD, Move, Copy, Rename, Delete)
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/files.go`, `internal/api/v2/batch_operations.go`
- **Go unit test coverage**: ~35% api/v2 (measured 2026-01-29)
- **Go test files**: `internal/api/v2/files_test.go`
- **Integration test scripts**: `scripts/test-file-operations.sh`, `scripts/test-batch-operations.sh`, `scripts/test-nested-folders.sh`, `scripts/test-nested-move-copy.sh`, `scripts/test-frontend-nested-folders.sh`
- **Integration endpoint coverage**: high (16 + 19 + variable + 91 + 11 assertions across 5 scripts)
- **Frontend files**: `src/components/dirent-list-view/`, `src/components/dirent-detail/`
- **Frontend test coverage**: ~0% (measured 2026-02-02)
- **Open bugs**: none
- **Soak started**: N/A (frontend coverage below threshold)

### File History
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/files.go` (GetFileRevisions, GetFileHistoryV21)
- **Go unit test coverage**: ~35% api/v2 (shared package, measured 2026-01-29)
- **Go test files**: `internal/api/v2/files_test.go`
- **Integration test scripts**: `scripts/test-file-history.sh` (17 assertions)
- **Integration endpoint coverage**: 3/3 endpoints (list api2, list v2.1, revert) = 100%
- **Frontend files**: `src/components/dirent-detail/file-history-panel.js`, `src/components/dirent-detail/dirent-details.js`
- **Frontend test coverage**: 0% (measured 2026-02-02)
- **Open bugs**: none
- **Soak started**: N/A (frontend coverage below threshold)

### Sharing System
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/file_shares.go`, `internal/api/v2/share_links.go`, `internal/api/v2/libraries.go`
- **Go unit test coverage**: ~35% api/v2 (shared package)
- **Integration test scripts**: `scripts/test-library-settings.sh` (partial)
- **Integration endpoint coverage**: needs audit
- **Frontend files**: share dialogs in `src/components/dialog/`
- **Open bugs**: none known
- **Soak started**: N/A (needs test map audit)

### OIDC Authentication
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/auth/oidc.go`, `internal/auth/session.go`
- **Go unit test coverage**: ~75% auth (measured 2026-01-29)
- **Go test files**: `internal/auth/oidc_test.go`, `internal/auth/session_test.go`
- **Integration test scripts**: `scripts/test-oidc.sh` (24 assertions)
- **Integration endpoint coverage**: 5/5 OIDC endpoints (100%)
- **Frontend files**: N/A (redirect-based)
- **Open bugs**: none
- **Soak started**: N/A (check coverage meets 80%)

### Admin Panel (Groups/Users)
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/admin.go`
- **Go unit test coverage**: ~35% api/v2 (shared package)
- **Integration test scripts**: `scripts/test-admin-panel.sh` (29 assertions)
- **Integration endpoint coverage**: 16/16 endpoints (100%)
- **Frontend files**: `src/pages/sys-admin/`
- **Frontend test coverage**: 0%
- **Open bugs**: none
- **Soak started**: N/A (Go coverage below threshold)

### Departments
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/departments.go`
- **Go unit test coverage**: ~35% api/v2 (shared package)
- **Integration test scripts**: `scripts/test-departments.sh` (29 assertions)
- **Integration endpoint coverage**: high
- **Frontend files**: `src/pages/sys-admin/departments/`
- **Open bugs**: none
- **Soak started**: N/A

### File Preview & Raw Serving
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/fileview.go`
- **Go unit test coverage**: ~20% api/v2 (shared package, measured 2026-02-04)
- **Go test files**: `internal/api/v2/fileview_test.go` (14 tests)
- **Integration test scripts**: `scripts/test-file-preview.sh` (28 assertions)
- **Integration endpoint coverage**: 3/3 endpoints (inline preview, raw serving, history download) = 100%
- **Frontend files**: N/A (server-rendered HTML)
- **Open bugs**: none
- **Soak started**: N/A (Go coverage below threshold — shared package blocker)

### Monitoring / Health Checks
- **Status**: 🔒 FROZEN (2026-02-04)
- **Go packages**: `internal/health/`, `internal/metrics/`, `internal/logging/`
- **Go unit test coverage**: 100% health (measured 2026-02-04)
- **Go test files**: `internal/health/health_test.go` (5 tests)
- **Integration test scripts**: `scripts/test-health.sh` (21 assertions)
- **Integration endpoint coverage**: 3/3 endpoints (/health, /ready, /metrics) = 100%
- **Frontend files**: N/A
- **Open bugs**: none
- **Frozen**: 2026-02-04 — 100% Go unit coverage, 100% integration coverage, zero bugs, component stable since 2026-01-30 (5+ sessions)

### Library CRUD
- **Status**: ✅ COMPLETE
- **Go packages**: `internal/api/v2/libraries.go`
- **Go unit test coverage**: ~35% api/v2 (shared package)
- **Integration test scripts**: `scripts/test-library-settings.sh` (5 assertions)
- **Integration endpoint coverage**: needs audit — likely low
- **Open bugs**: none
- **Soak started**: N/A (needs more integration tests)

---

## Production Release Checklist (v1.0)

Before tagging v1.0, **all** of these must be true:

### Hard Requirements (Blockers)

- [ ] **All 🔒 FROZEN components still pass their tests**
- [ ] **Zero 🔴 production blockers** in KNOWN_ISSUES.md (currently: all complete ✅)
- [ ] **Core path components are 🟢 or 🔒**: Sync Protocol, File CRUD, Library CRUD, Auth, Sharing, Permissions
- [ ] **`./scripts/test.sh all` passes** (excluding environment-dependent suites like multiregion/failover)
- [ ] **No security issues**: OWASP top 10 review on auth endpoints, input validation on file paths

### Soft Requirements (Should Have)

- [ ] **≥ 5 components at 🟢 RELEASE-CANDIDATE or 🔒 FROZEN**
- [ ] **Go unit test coverage ≥ 50% overall** (currently ~30%)
- [ ] **Integration test count ≥ 350** (currently ~335)
- [ ] **All COMPLETE components have Component Test Map entries**
- [ ] **Monitoring endpoints tested**: /health, /ready, /metrics have dedicated tests

### Nice to Have (Can Ship Without)

- [ ] Frontend test coverage ≥ 30% overall
- [ ] All 🟡 PARTIAL components either completed or explicitly deferred with rationale
- [ ] Load testing results documented
- [ ] Backup/restore procedure documented and tested

---

## Measuring Coverage: Quick Commands

### Go Unit Test Coverage (per package)

```bash
# Overall
cd /workspace/cool-storage-api
./scripts/test.sh go  # uses -cover flag

# Detailed per-package (inside Docker or with Go installed)
go test -coverprofile=coverage.out ./internal/...
go tool cover -func=coverage.out | grep total
go tool cover -func=coverage.out  # per-function breakdown

# Single package
go test -coverprofile=coverage.out ./internal/gc/...
go tool cover -func=coverage.out
```

### Integration Test Coverage (manual count)

```bash
# Count assertions per script
for f in scripts/test-*.sh; do
  count=$(grep -c 'pass\|PASS\|check_response' "$f" 2>/dev/null || echo 0)
  echo "$f: ~$count assertions"
done

# Count total passing
./scripts/test.sh api 2>&1 | grep -c '✓ PASS'
```

### Frontend Test Coverage

```bash
cd frontend
npm test -- --coverage --watchAll=false
# Look at coverage/lcov-report/index.html for detailed report
```

---

## Process: What to Do Each Session

### At Start of Session
1. Check if any 🟢 RELEASE-CANDIDATE components have completed their 3-session soak
2. If yes → promote to 🔒 FROZEN

### When Adding Tests
1. Update the **Component Test Map** entry for the component
2. Re-measure coverage if significant tests were added
3. Check if the component now meets freeze prerequisites

### When Fixing Bugs
1. Add regression test
2. If the component was 🟢 RELEASE-CANDIDATE, reset soak counter to 0
3. Update KNOWN_ISSUES.md

### At End of Session
1. For each 🟢 component: increment session counter if all tests passed and no bugs filed
2. Update `docs/IMPLEMENTATION_STATUS.md` with current status
3. Run existing `docs/SESSION_CHECKLIST.md` as usual

---

## Current Gaps (What's Blocking Freezes)

| Component | What's Missing to Reach 🟢 |
|-----------|---------------------------|
| **Crypto** (`internal/crypto/`) | ✅ **FROZEN** (2026-02-04). 90.8% Go + 100% integration. |
| **Health** (`internal/health/`) | ✅ **FROZEN** (2026-02-04). 100% Go + 21 integration tests. |
| GC | Open bug (auto_delete_days), Go coverage ~65% < 80% |
| Permissions | Go coverage 42% < 80% |
| File Operations | Frontend coverage < 60%, Go coverage < 80% (shared api/v2 package) |
| File Preview | Go coverage < 80% (shared api/v2 package) |
| File History | Frontend coverage < 60%, Go coverage < 80% |
| Sharing | Needs integration test audit, Go coverage < 80% |
| OIDC | Go coverage 56% < 80% |
| Admin Panel | Go coverage < 80%, frontend coverage < 60% |
| Library CRUD | Integration test count too low |
| Monitoring | No dedicated integration test script |

**Biggest systemic blocker**: The `internal/api/v2/` package is one giant shared package. Per-component Go coverage can't be measured independently. Options:
1. Split into sub-packages (large refactor, not worth it pre-release)
2. **Pragmatic alternative**: Measure api/v2 coverage as a whole, require ≥ 60% for the package, and rely on integration tests for per-component confidence

---

## Summary: The Rule

> A component is **production-frozen** when it has ≥ 80% Go unit test coverage (or ≥ 60% for shared packages), ≥ 90% integration endpoint coverage, zero open bugs, a completed Component Test Map entry, and 3 consecutive clean sessions in RELEASE-CANDIDATE status.

This is recorded. Follow it.
