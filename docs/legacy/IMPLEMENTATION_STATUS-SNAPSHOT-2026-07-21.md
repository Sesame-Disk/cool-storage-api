# IMPLEMENTATION_STATUS.md snapshot (archived 2026-10-12)

**Why archived:** these sections of `docs/IMPLEMENTATION_STATUS.md` were last maintained between
January and 2026-07-21: readiness percentages, a production-blocker list, the freeze/promotion
rules (retired with `RELEASE-CRITERIA.md`), "next priorities", documentation status and metrics.
Several no longer hold. Historical bodies are preserved, with relative links repaired.

**Source:** `docs/IMPLEMENTATION_STATUS.md` at commit `5c3163871`, lines 7–31 and 422–614.
**Current status lives in:** `docs/V1-PRODUCTION-ROADMAP.md` (readiness), `docs/X1-CRITICAL-PATH.md`
(GC), `docs/KNOWN_ISSUES.md` and `docs/OPEN-WORK-INDEX.md` (open issues and blockers).

---

## Project Completeness Summary

**Overall Production Readiness**: ~86%

| Area | Completeness | Notes |
|------|--------------|-------|
| Sync Protocol (Desktop) | ~90% | Functional sync scenarios pass, and the current hardening scope is baseline-verified for active-active HEAD-conflict recovery. Broader scenario coverage remains follow-up test debt. |
| Core Backend API | ~98% | GC implementation exists but destructive GC is production-disabled pending X1; OIDC ✅, Library Settings ✅, Monitoring ✅, Quotas ✅, Plans/Permissions Phase 1+2 ✅, org storage policy backend base ✅ |
| Admin Panels | ~95% | Superadmin ✅, Org Admin ✅, both at parity. Audit logs pending |
| Frontend UI | ~85% | All 122 modals migrated ✅, File History UI ✅, permission UI (~75% with granular flags), ~51 ModalPortal wrappers to clean up, Phase 3 quota/plan UI in progress |
| Authentication | ~90% | OIDC Phase 1 complete, JWT revocation hardened (2026-03-31), and user API keys now cover desktop/CLI/automation auth in OIDC-only deployments. Device Flow and service-account flows remain future enhancements. |
| Production Infrastructure | ~95% | Destructive GC blocked by X1; Monitoring ✅, Health checks ✅, Structured logging ✅, Frontend/Backend separation ✅, Nginx production hardening ✅ |

**Production Blockers (verified against code 2026-05-20)**:
1. ~~OIDC Authentication~~ - ✅ COMPLETE (Phase 1 - Basic Login)
2. **Garbage Collection activation** - 🔴 BLOCKED: keep `GC_ENABLED=false` on every replica/DC until X1 physical-delete ABA closes. X2 cross-DC reference visibility closed 2026-08-14 under its stable-topology operational contract.
3. ~~Monitoring/Health Checks~~ - ✅ COMPLETE (slog logging, `/health`, `/ready`, `/metrics`)
4. ~~Frontend/Backend Separation~~ - ✅ COMPLETE (2026-03-30/31) — separate React/nginx container, bootstrap API, nginx production hardening
5. ~~Programmatic Auth (PATs)~~ - ✅ COMPLETE — user API keys ship via `/api/v2.1/api-keys/`, and `/api2/auth-token/` now exchanges `email + API key` for desktop/CLI tokens. Device Flow remains optional future work.
6. ~~Desktop sync active-active conflict recovery~~ - ✅ BASELINE VERIFIED for the current hardening scope. `PUT /seafhttp/repo/:repo_id/commit/HEAD` and `POST /seafhttp/repo/:repo_id/update-branch` now use parent-chain validation, CAS, bounded retry, ancestry-gated server-side auto-merge for safe stale siblings, and `503 + Retry-After` fail-closed responses for unsafe conflicts. `scripts/test-sync-active-active.sh` now proves both the non-overlapping concurrent-write race with observed `parent mismatch` plus `auto-merge`, and the same-path unsafe-conflict path with observed retry-budget `503` while both desktop clients preserve their divergent local edits. Broader scenario coverage remains follow-up test debt rather than a current blocker.
7. **GC Multi-Instance Coordination** - ✅ Lease implemented, but activation remains 🔴 BLOCKED — the lease coordinates enabled replicas but cannot close X1. Keep `GC_ENABLED=false` on every replica in every DC. Only after X1 closes and the PRE-GC / A1 gate ([X1-CRITICAL-PATH.md](../X1-CRITICAL-PATH.md) §6) closes may designated replicas in one DC participate under the lease.
8. ~~Quota Period Rollover~~ - ✅ COMPLETE — Period rollover job advances expired org quota periods and keeps monthly traffic enforcement moving
9. **Production Multi-Region Topology** - ⚠️ PARTIAL — region-aware library selection/read/write routing is implemented and covered by focused integration tests, and org-level create-time residency policy now exists in the backend. The stock production config/compose files still ship as single-region examples, and per-region `classes`, `endpoint_regions`, ingress host preservation, rollout, migration, and frontend policy controls remain operator work.

---

---

## Code Freeze Rules

### 🔒 FROZEN Components - DO NOT MODIFY Without:

1. **User explicitly requests change** (documented in session)
2. **Desktop client sync breaks** (verified with `run-real-client-sync.sh`)
3. **Protocol comparison fails** (verified with `run-sync-comparison.sh`)

**Frozen Files**:
- `internal/crypto/crypto.go` - PBKDF2 implementation
- `internal/api/sync.go` lines 949-952 - fs-id-list format
- `internal/api/sync.go` lines 125-130 - commit object format
- `internal/api/sync.go` lines 500-509 - encryption fields in commit
- `internal/api/v2/encryption.go` - set-password/change-password endpoints
- `internal/api/v2/onlyoffice.go` - OnlyOffice integration (working correctly, 2026-01-22)
- `internal/config/config.go` - OnlyOffice configuration

**Frozen Behaviors**:
- fs-id-list returns JSON array (not newline-separated)
- Commit objects omit `no_local_history` field
- `encrypted` field type: integer in download-info, string in commits
- `is_corrupted` field type: integer 0 (not boolean false)
- `Seafile-Repo-Token` header for `/seafhttp/` authentication
- pack-fs binary format: 40-byte ID + 4-byte size (BE) + zlib-compressed JSON
- PBKDF2 inputs: magic uses `repo_id+password`, random_key uses `password` only

### ✅ COMPLETE Components - Modify With Caution:

**Before modifying**:
1. Review existing tests
2. Understand current behavior
3. Add tests for new functionality
4. Check if changes affect desktop client (even if not frozen)

### 🟡 PARTIAL / ❌ TODO - Safe to Modify:

- Active development area
- No stability guarantees yet
- Experiment freely

---

## Testing Requirements for Stability Promotion

| Promotion | Requirements |
|-----------|-------------|
| ❌ TODO → 🟡 PARTIAL | Basic implementation complete, compiles |
| 🟡 PARTIAL → ✅ COMPLETE | Unit tests passing, manual testing done, documented |
| ✅ COMPLETE → 🔒 FROZEN | Desktop client tested OR protocol comparison verified, test vectors generated (if crypto) |

**Verification Commands**:
```bash
# Protocol comparison (for sync endpoints)
cd docker/seafile-cli-debug && ./run-sync-comparison.sh

# Real desktop client test (for sync endpoints)
cd docker/seafile-cli-debug && ./run-real-client-sync.sh

# Real desktop client active-active proof (two clients, two backend nodes)
bash ./scripts/test-sync-active-active.sh

# Unit tests (for all components)
go test ./...
```

---

## Production Blockers 🔴

These MUST be completed before production deployment:

### 1. ~~OIDC Authentication~~ ✅ COMPLETE (2026-01-28)
- **Impact**: ~~Cannot deploy with real users~~ Now supports OIDC SSO
- **Status**: Phase 1 complete - Basic login flow working
- **Files Created**: `internal/auth/oidc.go`, `internal/auth/session.go`, `internal/api/v2/auth.go`, `frontend/src/pages/sso/index.js`
- **Provider**: https://t-accounts.sesamedisk.com/openid (test environment)
- **Remaining (Phase 2-3)**: Org/tenant mapping, role synchronization

### 2. Garbage Collection — implementation complete, production activation blocked
- **Impact**: Reclamation is unavailable in production while the safety gate is active
- **Status**: Worker, scanner, admin API, and LWT lease are implemented. Keep `GC_ENABLED=false` on every replica in every DC until X1 and the PRE-GC / A1 gate (X1-CRITICAL-PATH.md §6) close; only then may designated replicas in one DC participate under the lease.
- **Files**: `internal/gc/` — gc.go, queue.go, worker.go, scanner.go, store.go, store_mock.go, store_cassandra.go, gc_hooks.go, gc_adapter.go
- **Tests**: Unit, race, and Docker integration coverage exists under `internal/gc/`, `internal/db/`, `internal/api/`, and `internal/integration/`
- **Admin API**: `GET /api/v2.1/admin/gc/status`, `POST /api/v2.1/admin/gc/run`

### 3. ~~Monitoring/Health Checks~~ ✅ COMPLETE (2026-01-30)
- **Impact**: ~~No visibility into system health~~ Full observability stack deployed
- **Status**: Fully implemented — structured logging, health probes, Prometheus metrics
- **Files Created**: `internal/logging/logging.go`, `internal/health/health.go`, `internal/metrics/metrics.go`, `internal/metrics/middleware.go`
- **Files Modified**: `internal/config/config.go`, `internal/db/db.go`, `internal/storage/s3.go`, `internal/api/server.go`, `cmd/sesamefs/main.go`
- **Endpoints**: `GET /health` (liveness), `GET /ready` (readiness with DB+S3 checks), `GET /metrics` (Prometheus)
- **Also fixed**: Cassandra keyspace bootstrap bug (pre-existing)

---

## Next Priorities (Post-Blockers)

### Priority 1: Desktop Client Compatibility - Functional, But Not Yet Desktop-GA-Safe

- Sync protocol functional scenarios pass (7 core test scenarios)
- Active-active desktop HEAD-conflict recovery still needs explicit proof before calling desktop sync production-safe
- "View on Cloud" feature implemented (2026-01-18)

### Priority 2: Frontend Polish

1. **Frontend modal dialog migration** ✅ COMPLETE
   - All dialog files use plain Bootstrap modal classes (verified 2026-01-30)
   - Zero dialog files import `Modal` from reactstrap

2. **Frontend permission UI** (~50% remaining)
   - Hide/disable buttons based on user role
   - Toolbars done, many edge cases remain
   - Granular permission flags now enforced backend-side (2026-03-11)
   - Upload link and share link uploaders updated for permission-aware behavior
   - Status: 🟡 PARTIAL

### Priority 3: Library Settings Backend - PARTIAL

3. **Library settings endpoints** — Partially implemented (updated 2026-05-15)
   - API tokens and transfer are working
   - History limit and auto-delete persist settings, but their runtime semantics are not fully aligned with the UI labels
   - See `docs/TECHNICAL-DEBT.md` section 18 and ISSUE-LIB-RETENTION-01
   - File: `internal/api/v2/library_settings.go`

### Priority 4: Documentation

4. **Missing documentation**
   - End-user documentation (how to use key workflows)
   - Operational admin runbooks (backup/restore, incident response)
   - Expanded org-admin and sysadmin usage guides

### Future Features (Lower Priority)

- Thumbnails
- File comments
- Activity logs/notifications
- Watch/unwatch libraries
- Multi-region replication

---

## Documentation Status

| Document | Status | Purpose |
|----------|--------|---------|
| `CURRENT_WORK.md` | ✅ COMPLETE | Session-to-session tracking (updated every session) |
| `docs/IMPLEMENTATION_STATUS.md` | ✅ COMPLETE | This file - component stability matrix |
| `docs/DECISIONS.md` | ✅ COMPLETE | Protocol-driven development workflow |
| `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` | 🔒 FROZEN | Formal protocol specification with test vectors |
| `docs/SEAFILE-SYNC-PROTOCOL.md` | ✅ COMPLETE | Quick reference for developers |
| `docs/ENCRYPTION.md` | ✅ COMPLETE | Encryption implementation guide |
| `docs/FRONTEND.md` | ✅ COMPLETE | Frontend patterns, modal dialog fixes, debugging |
| `docs/ARCHITECTURE.md` | ✅ COMPLETE | Design decisions, storage architecture |
| `docs/DATABASE-GUIDE.md` | ✅ COMPLETE | Cassandra schema, examples |
| `docs/API-REFERENCE.md` | ✅ COMPLETE | API endpoints, implementation status |
| `docs/TESTING.md` | ✅ COMPLETE | Test coverage, benchmarks |
| `docs/SYNC-TESTING.md` | ✅ COMPLETE | Protocol testing with seaf-cli |
| `docs/ADMIN-FEATURES.md` | ✅ COMPLETE | Admin library mgmt, link mgmt, audit log specs |
| `docs/OIDC-CLAIMS-REFERENCE.md` | ✅ COMPLETE | OIDC provider implementer reference |
| `CLAUDE.md` | ✅ COMPLETE | AI assistant context |
| `README.md` | ✅ COMPLETE | Quick start, features |

---

## Metrics

**Last Updated**: 2026-07-21

| Metric | Value | Notes |
|--------|-------|-------|
| Sync Protocol Endpoints | 13/13 (100%) | Endpoint surface implemented; active-active desktop conflict recovery still open |
| REST API Endpoints (Core) | ~60/62 (97%) | Missing: monitored-repos, audit logs. New: 5 custom share permission endpoints |
| Superadmin Endpoints | ~90+ | Libraries, links, users, groups, orgs, departments, address book, group-owned libs |
| Org Admin Endpoints | ~50+ | Full panel: users, groups, repos, trash, departments, links, address book |
| Frontend Components | ~85% complete | All modals migrated, permission-aware uploaders, ~51 ModalPortal wrappers to clean up |
| Desktop Client Compatibility | ⚠️ Functional coverage only | Core sync scenarios pass, but active-active HEAD-conflict recovery for desktop sync is still unproven end-to-end |
| Test Coverage (Go) | ~30% overall | chunker 79%, crypto 90.8%, config 73%, auth 56%, health 100% |
| Integration Tests | 335+ tests | All passing (incl. OIDC, GC, file preview) |
| Frontend Tests | 165+ tests | 7 test files (incl. OIDC API) |
| Documentation Coverage | ~85% | Core API, deploy, migration, testing, architecture, and admin feature docs exist. Missing: end-user guides and deeper operational runbooks. |

**Stability Breakdown**:
- 🔒 FROZEN: ~21 components (encryption, OnlyOffice, monitoring/health)
- ✅ COMPLETE: ~45 components (CRUD, sharing, groups, tags, batch ops, OIDC, GC, monitoring, admin panels, org admin, custom share permissions)
- 🟡 PARTIAL: ~15 components (frontend UI, permission UI, org admin stubs)
- ❌ TODO: ~2 components (audit logs, monitored-repos)

**Production Readiness**:
- Backend: ~98% for web/API surfaces, but destructive GC is production-disabled pending X1. The distributed lease exists; it coordinates designated participants only after that blocker closes.
- Desktop sync: not yet ready to call fully production-safe for active-active multi-node deployments until HEAD-conflict recovery is explicitly proven or hardened
- Frontend: ~85% (modals done, granular permission flags enforced, missing: some permission UI edge cases, ~51 ModalPortal wrapper cleanup)
- Infrastructure: ~95% (monitoring ✅, health checks ✅, GC implementation present but production activation blocked by X1)
- Documentation: ~85% (deployment, migration, testing, API, architecture, and feature docs exist; missing: end-user guides and operational admin runbooks)


## Historical promotion thresholds (retired)

**Source:** docs/IMPLEMENTATION_STATUS.md at 5c3163871, lines 43–45.

**Promotion rules**: See [RELEASE-CRITERIA-2026-03-17.md](RELEASE-CRITERIA-2026-03-17.md) for the full historical procedure.
- ✅ → 🟢: needs ≥ 80% Go coverage (60% for shared pkgs), ≥ 90% integration endpoint coverage, zero open bugs, Component Test Map entry
- 🟢 → 🔒: needs 3 consecutive clean sessions with all tests passing and no new bugs


## Historical status legend (retired)

Source: docs/IMPLEMENTATION_STATUS.md at 5c3163871. These approval and soak requirements
are historical; use [../IMPLEMENTATION_STATUS.md](../IMPLEMENTATION_STATUS.md#status-legend).

## Status Legend

| Symbol | Meaning | Stability | Safe to Modify? |
|--------|---------|-----------|-----------------|
| 🔒 **FROZEN** | Meets all freeze criteria, soak period complete | **STABLE** | ❌ No - only with user approval |
| 🟢 **RELEASE-CANDIDATE** | Meets freeze prerequisites, in soak period | **STABLE** | ⚠️ Bug fixes only - resets soak counter |
| ✅ **COMPLETE** | Implemented, basic testing done | Mostly stable | ⚠️ Caution - review tests first |
| 🟡 **PARTIAL** | Stub exists or incomplete implementation | **UNSTABLE** | ✅ Yes - active development |
| ❌ **TODO** | Not started | N/A | ✅ Yes - greenfield |
