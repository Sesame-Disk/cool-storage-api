# Current Work - SesameFS

**Last Updated**: 2026-08-14
**Session**: X1/X2 generational GC fence ADR — protocol revision r3 (no code changes; Compose image pin and prod `GC_ENABLED=false` included)

**📏 File Size Rule**: Keep this file under **500 lines** unless unavoidable. Move detailed content to:
- `docs/KNOWN_ISSUES.md` - Detailed bug tracking
- `docs/CHANGELOG.md` - Session history
- `docs/IMPLEMENTATION_STATUS.md` - Component status
- Other appropriate documentation files

---

## 🚀 NEW SESSION? START HERE

**PROJECT STATUS**: ~85-90% production ready (see `docs/IMPLEMENTATION_STATUS.md`)

**🔴 PRODUCTION BLOCKERS** (Must complete before deploy):
1. ~~**OIDC Authentication**~~ - ✅ **COMPLETE** (Phase 1 - Basic Login)
2. **Destructive Garbage Collection** - 🔴 **BLOCKED** by X1 physical-delete ABA, now the sole blocker: X2 cross-DC reference visibility closed 2026-08-14 (destructive liveness at `EACH_QUORUM` behind a topology gate, proven on a real three-DC cluster). Keep `GC_ENABLED=false` on every replica in every DC; the implementation and lease exist but are not permission to activate deletion.
3. ~~**Monitoring/Health Checks**~~ - ✅ **COMPLETE** (Structured logging, `/health`, `/ready`, `/metrics`)

**Then review**:
1. **"What's Next"** → Top priorities (work on #1 unless user specifies)
2. **"Frozen Components"** → What NOT to touch (breaks desktop clients)
3. **"Critical Context"** → Essential facts to remember

### Quick Context
1. **Sync Protocol**: Baseline-verified for the current desktop sync hardening scope. Do not treat it as frozen; compatibility-sensitive follow-up coverage still exists.
2. **Backend API**: ~98% complete - OIDC ✅, GC implementation present; destructive activation blocked by X1 alone (X2 closed 2026-08-14), Library Settings ✅, Monitoring ✅, Departments ✅, Admin Panel (groups/users) ✅, OIDC Group/Dept Sync ✅, Tag cascade ✅, Admin Link Management ✅, Upload Links ✅, Org Admin Panel ✅, Superadmin Departments ✅, Custom Share Permissions ✅
3. **Frontend UI**: ~85% complete (all modals migrated, About modal rebranded, File History UI ✅, History Download ✅, Snapshot View ✅, Restore from History ✅, Share Dialog all 8 tabs ✅, permission UI ~75% with granular flags, ~51 ModalPortal wrappers to clean up, folder icons ✅). Plans/permissions Phase 3 is in progress, not closed.
4. **Test flow**: Prefer Docker-first validation. `./scripts/test.sh sync` now runs the single-client sync suite plus the real active-active desktop harness; default behavior is fail-fast and `--keep-going` is opt-in.
5. **Current risk shape**: destructive GC must remain disabled fleet-wide. Of the two confirmed live-data safety blockers, X2 is closed (fix proven on a real three-DC cluster, regression mutation-verified) and X1 remains open — so the activation gate now genuinely rests on X1 alone. The upload-fence PR series addresses separate writer/GC races and does not close it.
6. **X1 design record**: the accepted/frozen greenfield protocol is documented in [`docs/GC-X1-X2-GENERATION-FENCE-ADR.md`](docs/GC-X1-X2-GENERATION-FENCE-ADR.md); implementation has not started and `GC_ENABLED=false` remains mandatory.

### Inter-session Update (2026-05-21)

- PR61 focus is desktop sync conflict hardening and closeout, not audit-log expansion.
- Sync HEAD promotion now has branch-parity coverage for both `PUT /seafhttp/repo/:repo_id/commit/HEAD` and `POST /seafhttp/repo/:repo_id/update-branch`.
- The hardening baseline is now verified for same-tree idempotence, safe non-overlapping auto-merge, and fail-closed retryable `503` behavior for unsafe conflicts.
- Real active-active desktop proof now exists via two `seaf-cli` clients in Docker hitting separate backend nodes.
- `./scripts/test.sh sync` now chains the single-client sync suite and the active-active harness; it stops on the first failing suite by default, with `--keep-going` available when you need aggregate failure reporting.
- `./scripts/test.sh` now also prints failure excerpts for compose-backed and script-backed suites, so Docker/test-runner failures are immediately visible without digging through full logs.
- Sync cleanup was tightened to release/delete stale `sync-test-*` and `sync-aa-*` libraries so the suite no longer drifts into `Library limit reached` failures.
- A broader canonical quota-reservation prototype was audited and explicitly split out of this branch; the confirmed defects are documented in `docs/KNOWN_ISSUES.md`, and PR61 should keep only the test-runner improvement from that line of work.
- Remaining sync follow-up debt is narrower: deeper-tree active-active branches, quota rejection during auto-merge, and broader 3-node/org-level quota contention races.

### Step 2: Before Making ANY Code Changes
- ✅ Check `docs/IMPLEMENTATION_STATUS.md` - Is component 🔒 FROZEN?
- ✅ If FROZEN → DO NOT MODIFY without explicit user approval
- ✅ If ✅ COMPLETE → Modify with caution, verify tests pass
- ✅ If 🟡 PARTIAL / ❌ TODO → Safe to actively develop

### Step 3: At End of Session - Update Documentation
**📋 MANDATORY: Run [docs/SESSION_CHECKLIST.md](docs/SESSION_CHECKLIST.md)**

---

## Current Branch Summary ✅

**Date**: 2026-08-12
**Focus**: X1/X2 Generational GC Fence ADR — protocol revision r3

### Completed In This Branch Slice

- Accepted and froze the implementation contract in `docs/GC-X1-X2-GENERATION-FENCE-ADR.md` after a final three-DC audit.
- Added the `SERIAL + ALL` `ACTIVE -> RETIRING` visibility fence and ambiguity reaffirmation required for Paxos v2 at RF3.
- Added the same per-DC visibility rule to pointer-selected quarantine, plus topology-maintenance and drained-cutover gates.
- Made serial settlement mandatory before activation classification and added transitive lineage handling for historical winners.
- Specified cross-DC projection discovery, explicit pin authorization, clock-health, authoritative storage, and coordinated rollout gates.
- Added exact `dc-na`/`dc-eu`/`dc-asia` RF1/RF3 acceptance requirements for Paxos v1 and v2.
- Closed a critical r2 regression: `RETIRED/QUARANTINE -> ACTIVE` falsified the terminality of `RETIRED` that the `DELETING` authorization depends on, letting a stalled delete worker remove bytes under a live reference. Resolution now recertifies forward to `RETIRED/GC_RETIRE`; a `RETIRED` pointer never returns to `ACTIVE`.
- Added the durable `RESOLVING` work state so an authorized quarantine resolution is distinguishable from an in-progress quarantine after a crash.
- **r3:** closed a wedge in the quarantine-resolution abort, which failed three ways at once. It classified from an ordinary read, so a rollback could be overtaken by a live resolver or an accepted-but-unlearned pointer proposal; it fenced at most the pointer partition, while the `QUARANTINED -> null` clear names no pointer column and by correction 68 may not, so a resolver stalled before its clear survived; and it left no durable record of the abort, so a crash after the fences was indistinguishable from an ordinary claim takeover and a scanner resumed the very resolution being abandoned. The abort now records `RESOLVING -> ABORTING` first, serially settles `blocks`, and fences **both** partitions — the generation's new monotonic `resolution_epoch` and the pointer's quarantine claim — before any rollback.
- **r3:** removed a verification/X2-closure requirement that asserted `RESOLVING -> REJECTED` must not apply, which contradicted the abort contract and made the normative suite unsatisfiable; gave the post-commit abort the quarantine form matching the pointer's actual state; made a quarantined `MATERIALIZING` generation resolvable; froze the delete-escalation columns; fixed the projection-family count, one step off-by-one, and the stale version inventory.
- Kept X1/X2 open. No code changes and destructive GC stays disabled, but this is **not** a docs-only branch: it also pins the Cassandra image to 5.0.9 across the Compose files and sets `GC_ENABLED=false` explicitly in the production Compose.
- Files: `CURRENT_WORK.md`, `README.md`, `docker-compose.yaml`, `docker-compose.mr.yaml`, `docker-compose.mr-cluster.yaml`, `docker-compose-multiregion.yaml`, `docker-compose.prod.yml`, `docs/ARCHITECTURE.md`, `docs/CHANGELOG.md`, `docs/DECISIONS.md`, `docs/GC-UPLOAD-FENCE-PR-PLAN.md`, `docs/GC-X1-X2-GENERATION-FENCE-ADR.md`, `docs/KNOWN_ISSUES.md`, `docs/OPEN-WORK-INDEX.md`, `docs/PROD-SECURITY-READINESS-20260724.md`, `docs/UPLOAD-FENCE-FINDINGS-REGISTRY.md`, `docs/UPLOAD-PERFORMANCE-SECURITY-2026-06.md`, `docs/VERSIONS.md`.

### Remaining Follow-up Debt

- Complete Phase-0 measurements and the explicit go/no-go, pin the Cassandra 5.0.9 image/digest, and build the three-DC RF1/RF3 harness; then implement PR-1 through PR-8.
- Keep `GC_ENABLED=false` on every replica/DC until both X1 and X2 acceptance lists pass.
- Close the canonical issues only after code, crash tests, clock/storage gates, and RF1/RF3 multi-DC evidence exist.

## Historical Session Summary ✅

**Date**: 2026-03-31
**Focus**: Frontend/Backend Split Audit + Nginx Production Hardening + Bug Fixes

### Completed This Session (Session 59)

#### Nginx frontend container — 6 production bugs fixed ✅
All were silent failures that would only surface under production load:
- `client_max_body_size 100G` at server block (was missing → nginx default 1MB blocked large uploads)
- Proxy timeouts: `proxy_read_timeout 3600s`, `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s` at server block
- `proxy_buffering off; proxy_request_buffering off` on transfer routes (`/d/`, `/u/d/`, `/lib/`, `/repo/`, `/seafhttp/`)
- `proxy_http_version 1.1` + `proxy_set_header Connection ""` on all proxy locations (HTTP/1.1 keepalive)
- `sendfile on; tcp_nopush on; tcp_nodelay on` at server block
- `gzip_vary on; gzip_comp_level 6`

#### Nginx production reverse proxy — improvements ✅
- Upstream keepalive: `keepalive 32/16/8` on all 3 upstream blocks
- Separate rate limit zone for file transfers (`transfer` zone 20r/s vs `api` zone 100r/s)
- Content-Security-Policy header added
- All `add_header` directives now use `always`
- `client_max_body_size 100G` (was 20G)
- Frontend location: `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s`

#### Bundle hash coupling fix ✅
- `internal/api/v2/sharelink_view.go` — `fetchBundleManifest()` fetches `asset-manifest.json` from
  frontend container at startup. 3-level fallback: HTTP → filesystem scan → hardcoded.
  `FRONTEND_URL` env var added to both docker-compose files.

#### Logout fixes ✅
- `internal/api/server.go` — `handleLogout` now calls `SessionManager.InvalidateSession(token)`
  before clearing cookie and redirecting (server-side session was never invalidated before)
- `frontend/src/components/common/logout.js` + `account.js` — clear `sesamefs_auth_token` and
  `custom_permissions_*` from localStorage on click (was left behind after backend redirect)

**Files changed**: `frontend/nginx.conf`, `nginx/nginx.conf.template`, `internal/api/v2/sharelink_view.go`, `internal/api/server.go`, `frontend/src/components/common/logout.js`, `frontend/src/components/common/account.js`, `docker-compose.yaml`, `docker-compose.prod.yml`, `docs/V1-PRODUCTION-ROADMAP.md`, `docs/CHANGELOG.md`, `CURRENT_WORK.md`

### Previous Session (Session 55) — Org Admin Panel + Superadmin Parity

**Date**: 2026-03-05

#### Org Admin Panel — Full Implementation ✅

Implemented complete org admin panel in `internal/api/v2/org_admin.go` with 50+ endpoints covering:

- **Users**: CRUD, password reset, owned/shared repos, search, import, invite (12 endpoints)
- **Groups**: CRUD, members, group libraries, search (13 endpoints)
- **Repositories**: List, delete, transfer, browse dirents (4 endpoints)
- **Trash Libraries**: List, clean, delete single, restore (4 endpoints)
- **Departments & Address Book**: List departments, full address book group CRUD with ancestors (6 endpoints)
- **Group Owned Libraries**: Create + soft-delete (2 endpoints)
- **Share Links**: List + delete with org ownership verification (2 endpoints)
- **Upload Links**: List + delete with org ownership verification (2 endpoints)
- **Devices**: Empty responses — no device table (3 endpoints)

**Performance fixes applied:**
- `resolveUsersMap()` — batch user resolution replacing N+1 queries
- No ALLOW FILTERING — `ListOrgGroupLibraries` iterates org libs + checks shares by partition key
- `sort.Slice` — replaced O(n²) bubble sort in `ListOrgRepos`
- Group quotas stored in `organizations.settings['group_quota_{groupID}']`

#### Superadmin Parity — Departments/Address Book/Group-Owned Libs ✅

Added 9 new endpoints to superadmin panel in `internal/api/v2/admin_extra.go`:
- `AdminListOrgDepartments`, `AdminListAddressBookGroups`, `AdminAddAddressBookGroup`
- `AdminGetAddressBookGroup` (with ancestors), `AdminUpdateAddressBookGroup`, `AdminDeleteAddressBookGroup`
- `AdminAddGroupOwnedLibrary`, `AdminDeleteGroupOwnedLibrary`
- `AdminUpdateGroupMemberRole`

Routes registered in `internal/api/v2/admin.go`.

#### Documentation Updated ✅

- `docs/ADMIN-FEATURES.md` — Added §4 (Superadmin departments), §5 (Org Admin Panel full docs), §6 (Parity table)
- `docs/ENDPOINT-REGISTRY.md` — Registered all 50+ org admin + 9 superadmin endpoints
- `docs/IMPLEMENTATION_STATUS.md` — Updated admin panel rows, added org admin entry, updated metrics
- `CURRENT_WORK.md` — This update

**Files changed**: `org_admin.go`, `admin.go`, `admin_extra.go`, `departments.go`, `ADMIN-FEATURES.md`, `ENDPOINT-REGISTRY.md`, `IMPLEMENTATION_STATUS.md`, `CURRENT_WORK.md`

### Previous Session (Session 54) — Upload File Replace/Autorename Fix

**Problem**: `replace=0` in upload was not triggering auto-rename (`file (1).ext`), default was overwriting.
**Fix**: Updated upload handler to check `replace` param correctly.

### Previous Sessions (53 and earlier — see docs/CHANGELOG.md)

- **Session 53**: Admin trash libraries 405 fix + cleanup handler + orphan data documentation
- **Session 52**: Retrocompat fix — pre-index users, admin `/sys/users/` multi-org fix
- **Session 45**: Superadmin script (`make-superadmin.sh`) + CreateOrganization seafile-js compat
- **Session 44**: Desktop client file browser fixes (oid header, upload/download protocol, trailing slash)
- **Session 33-34**: Admin share link + upload link management (13 endpoints) + verification
- **Session 32**: Bug fix sprint (5 bugs) + tag management enhancement
- **Session 30**: Snapshot view, revert with conflict handling
- **Sessions 22-29**: Admin panel, OIDC sync, File History UI, GC metrics, search, trash
- **Sessions 12-21**: GC, Monitoring, Departments, modal migration, move/copy fixes
- **Sessions 1-11**: Core API, tags, permissions, OIDC, library settings, OnlyOffice

---

## What's Next (Priority Order) 🎯

### 🔴 PRIORITY 1: PR61 Desktop Sync Hardening Closeout

**Status**: 🟡 Baseline verification is complete; the remaining work is merge hygiene and narrower follow-up coverage, not core sync correctness.
**Details**: `internal/api/sync.go`, `internal/integration/library_projection_regression_test.go`, `internal/integration/multi_instance_mutations_test.go`, `scripts/test-sync-active-active.sh`, `scripts/test.sh`

**Use this branch for:**

1. Running Docker-first closeout validation before merge (`./scripts/test.sh sync` first).
2. Extending the active-active sync matrix into deeper-tree conflicts or quota-rejection branches.
3. Keeping branch-status docs aligned with the validated behavior actually present in code.

**Do not let this branch drift into:**

1. Audit-log expansion.
2. Broad frontend cleanup unrelated to desktop sync.
3. New role/ownership behavior changes without a separate branch discussion.
4. Canonical quota reservation/resync experiments; keep that work in a dedicated follow-up branch.

### ✅ ~~PRIORITY 1: Admin Library Management~~ — DONE (2026-02-12)

**Status**: ✅ Complete — 12 endpoints implemented in `internal/api/v2/admin.go`
**Details**: [docs/ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 1

All admin library endpoints implemented: list, search, get, create, delete, transfer, browse dirents, history settings, shared items, trash libraries. Frontend `seafile-api.js` methods already wired.

### ✅ ~~PRIORITY 2: Admin Share Link & Upload Link Management~~ — DONE (2026-02-12)

**Status**: ✅ Complete — 13 endpoints across 5 files
**Details**: [docs/ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 2

Admin share link list/delete fixed; upload links full feature (DB tables, user CRUD, admin list/delete); per-user link endpoints; frontend API methods added.

### 🔴 PRIORITY 3: Audit Logs & Activity Logs — PRIORITIZE NEXT

**Status**: 🟡 Partial foundations exist: `audit_log` for deletion events and `users.last_login_at` for latest successful login, but no historical login/file activity dataset or APIs
**Details**: [docs/ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 3

**Two related systems need implementation:**

1. **Audit Logs** (admin-facing): Login logs, file access logs, file update logs, permission audit logs. Needed for compliance and admin visibility. Frontend pages exist at `frontend/src/pages/sys-admin/logs-page/` and `frontend/src/pages/org-admin/org-logs-*.js`.

2. **Activity Feed** (user-facing): The `/api/v2.1/activities/` endpoint currently returns stub `{"events": []}`. The dashboard activities feed and file activity panels depend on this. Frontend components exist (`frontend/src/pages/dashboard/activity-item.js`, `frontend/src/models/activity.js`).

**What exists today:**
- `internal/middleware/audit.go` — 13 action types defined, `AuditEvent` struct, console-only logging, 8 unit tests
- `audit_log` table — persists deletion/compliance events for GC, groups, departments
- `users.last_login_at` — real latest-login timestamp updated on successful auth and exposed in admin/org-admin user responses
- Frontend UI components for both admin logs and user activity feed

**Next logical slice:**
- Implement `login_logs` first as the bridge between point-in-time `last_login_at` and real historical audit/reporting
- Reuse the successful-auth hooks already touching `users.last_login_at`
- Expose login history to the existing admin login-log pages before expanding to file/activity logs

**Explicit pending gap:**
- `/sys/statistics/file/` and `/org/statistics-admin/file/` still cannot be made real without `file_update_logs` and `file_access_logs`
- That dependency is separate from `login_logs`; login audit work does not unblock file-operation charts by itself
- There is also a confirmed scope bug in org-admin statistics: traffic-based org-admin metrics can resolve to platform-wide aggregates when the org-admin shell is mounted with platform-org context

**Remaining full backlog:**
- 5 dedicated Cassandra tables (login_logs, file_access_logs, file_update_logs, permission_audit_logs, activities) with 90-day TTL
- New `internal/api/v2/audit.go` handler file (~5 endpoints)
- Async DB write integration (buffered channel pattern) across ~15 existing handlers
- Wire up frontend pages to real API endpoints

### ~~🟡 PRIORITY 4: File History UI Wiring~~ — ✅ COMPLETE (Session 23)

Detail sidebar now has Info | History tabs for files. Full-page history also works. Integration tests: 17 assertions passing.

### 📋 PRIORITY 4: Test Coverage Improvement

**Status**: Go integration test framework built (Session 24), coverage gaps identified

**Current unit test coverage** (from `go test -cover`):
| Package | Coverage | Lines | Priority |
|---------|----------|-------|----------|
| `internal/crypto` | 90.8% | ~600 | ✅ ABOVE THRESHOLD (was 69.6%) |
| `internal/api/v2` | 20.5% | 14,136 | HIGH — biggest codebase, most untested |
| `internal/api` | 19.1% | 4,769 | HIGH — sync protocol edge cases |
| `internal/db` | 0% | 1,139 | MEDIUM — all DB access only via integration |
| `internal/middleware` | 42.1% | 752 | MEDIUM — permission logic |
| `internal/storage` | 46.4% | 1,561 | MEDIUM — S3/block edge cases |
| `internal/templates` | 0% | 327 | LOW — email rendering |
| `internal/logging` | 0% | 66 | LOW — instrumentation |
| `internal/metrics` | 0% | 111 | LOW — instrumentation |

**Next steps** (in priority order):
1. **Add more Go integration tests** — share links, admin endpoints, groups, batch ops (parallels existing bash tests)
2. **DB interface mock** — define `Store` interface for `internal/db`, implement mock, unlock unit tests for all handlers
3. **API v2 handler unit tests** — error paths, validation edge cases in `files.go` (3,564 lines), `admin.go` (1,462 lines)
4. **Concurrent access tests** — race detector integration tests for simultaneous uploads/downloads
5. **testcontainers-go** — real Cassandra in CI for `internal/db` unit tests

**Frontend Testing Strategy** (7 test files currently, need expansion):
- Current: `utils.test.js`, `dirent.test.js`, `modal-pattern.test.js`, `seafile-api-tags.test.js`, `seafile-api-oidc.test.js`, `permission-checks.test.js`, `dirent-list-item.test.js`
- **Metrics to track**: Component coverage (% of components with tests), critical path coverage (login→upload→share flow), API mock coverage
- **Priority areas**: Dialog components (conflict dialogs, restore dialogs), API integration layer, permission-based UI visibility
- **Tools**: Jest + React Testing Library (already configured), consider adding Cypress for E2E

### 📋 PRIORITY 5: Frontend Cleanup (Lower)

- **ModalPortal Wrapper Cleanup** — ~51 parent components have unnecessary `<ModalPortal>` wrappers (harmless, cosmetic)
- **Frontend Permission UI** — ~60% complete, readonly/guest users still see some buttons they can't use

---

## Strategic Roadmap

### Phase 1: Production Blockers 🔴 — Platform Foundations Complete; Destructive GC Blocked

| Item | Status | Notes |
|------|--------|-------|
| **OIDC Authentication** | ✅ DONE | Phase 1 complete |
| **Garbage Collection** | 🟡 FOUNDATIONS DONE / ACTIVATION BLOCKED | Queue worker + scanner + admin API exist; destructive generation-aware GC remains disabled until X1/X2 acceptance |
| **Health Checks/Monitoring** | ✅ DONE | `/health`, `/ready`, `/metrics`, slog logging |

### Phase 2: Core Feature Completion

| Item | Status | Notes |
|------|--------|-------|
| **Admin Panel (Groups/Users)** | ✅ DONE | Option A (OIDC-managed). 16 endpoints + OIDC sync. 29 tests. |
| **Admin Library Management** | ✅ DONE | 12 endpoints in admin.go. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 1 |
| **Admin Link Management** | ✅ DONE | Share + upload links. 13 endpoints. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 2 |
| **Superadmin Departments/Address Book** | ✅ DONE | 9 endpoints. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 4 |
| **Org Admin Panel** | ✅ DONE | 50+ endpoints. Full parity with superadmin. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 5 |
| **Org Delete (3-state lifecycle)** | ✅ DONE (backend) | active → deactivated → deleted (30-day grace → cascade). Frontend TODO: ISSUE-FRONTEND-ORG-DELETE-01 |
| **Audit Logs** | ❌ TODO | 5 tables, ~5 endpoints, ~15 handler integrations. See [ADMIN-FEATURES.md](docs/ADMIN-FEATURES.md) § 3 |
| **File History UI** | ✅ DONE | Detail sidebar History tab + full-page view. 17 integration tests. |
| **GC TTL Enforcement** | ✅ DONE | Scanner Phase 5 (version_ttl_days) + Phase 6 (auto_delete_days) + share link deletion |
| **Frontend Modal Migration** | ✅ 122/122 | All done; ~51 ModalPortal wrappers to clean up |
| **Library Settings Backend** | ✅ DONE | History, API tokens, auto-delete, transfer |
| **Department Management** | ✅ DONE | Admin CRUD + hierarchy, 29 integration tests |
| **Frontend Permission UI** | 🟡 ~60% | Hide/disable based on role |

### Phase 3: Already Complete ✅

| Item | Status | Completed |
|------|--------|-----------|
| Sync Protocol | ✅ 🔒 FROZEN | 2026-01-16 |
| File Operations Backend | ✅ COMPLETE | 2026-01-27 |
| Batch Move/Copy | ✅ COMPLETE | 2026-01-27 |
| Sharing System | ✅ COMPLETE | 2026-01-22 |
| Groups Management | ✅ COMPLETE | 2026-01-22 |
| Department Management | ✅ COMPLETE | 2026-01-31 |
| Admin Panel (Groups/Users) | ✅ COMPLETE | 2026-02-02 |
| OIDC Group/Dept Sync | ✅ COMPLETE | 2026-02-02 |
| File Tags | ✅ COMPLETE | 2026-02-12 (cascade+rename) |
| Permission Middleware | ✅ COMPLETE | 2026-01-27 |
| OnlyOffice Integration | ✅ 🔒 FROZEN | 2026-01-29 |
| Search | ✅ COMPLETE | 2026-01-22 |

### Phase 4: Future Features (Lower Priority)

| Item | Priority | Notes |
|------|----------|-------|
| Thumbnails | LOW | Visual polish |
| File Comments | LOW | Collaboration feature |
| Watch/Unwatch | LOW | Needs notification system |
| Multi-region Replication | LOW | Future scaling |

---

## Frozen/Stable Components 🔒

**Freeze procedure**: See [docs/RELEASE-CRITERIA.md](docs/RELEASE-CRITERIA.md) for the formal stability rules and Component Test Map. Components need ≥ 80% Go coverage, ≥ 90% integration endpoint coverage, zero open bugs, and 3 clean sessions in 🟢 RELEASE-CANDIDATE before reaching 🔒 FROZEN.

### ⚠️ CRITICAL: Sync Code FROZEN (2026-01-19)
**User directive**: DO NOT MODIFY sync code without explicit approval

### Code Files - Sync Protocol 🔒
- `internal/api/sync.go` (lines 949-952, 125-130, 1405-1492) - Protocol formats
- `internal/api/v2/encryption.go` - Password endpoints

### Code Files - Crypto 🔒 (Frozen 2026-02-04)
- `internal/crypto/crypto.go` - PBKDF2, Argon2id, AES-256-CBC (90.8% unit test coverage, 39 tests)

### Code Files - Monitoring/Health 🔒 (Updated 2026-02-04)
- `internal/health/health.go` - Liveness and readiness probes 🔒
- `internal/metrics/metrics.go` - Prometheus metric definitions (GC metrics expanded Session 28)
- `internal/metrics/middleware.go` - Request metrics middleware 🔒
- `internal/logging/logging.go` - Structured logging setup 🔒

### Code Files - OnlyOffice 🔒 (Frozen 2026-01-29)
- `internal/api/v2/fileview.go` - File view auth wrapper + OnlyOffice editor HTML (json.Marshal config). Note: History download handler added (Session 25) — OnlyOffice code paths unchanged.
- `internal/api/v2/onlyoffice.go` - OnlyOffice API endpoint + JWT signing + editor callback

### Code Files - Web Downloads (Updated 2026-02-16)
- `internal/api/seafhttp.go` - `streamFileFromBlocks()` (primary download path — prefetch pipeline, 4MB buffers)
- `internal/api/seafhttp.go` - `HandleDownload()` (token validation, 4MB streaming buffer)
- `internal/api/seafhttp.go` - `addFileToZip()` (ZIP Store method, batch block resolve, 4MB buffers)
- `internal/api/seafhttp.go` - `resolveBlockIDs()` (batch Cassandra IN queries, 100/batch)
- `internal/api/v2/fileview.go` - `ServeRawFile()` / `DownloadHistoricFile()` (batch resolve + 4MB buffers)
- `internal/api/v2/sharelink_view.go` - Share link raw file streaming (batch resolve + 4MB buffers)
- `internal/storage/s3.go` - Custom HTTP transport (64 conn/host, 128KB read buffers)
- ⚠️ `getFileFromBlocks()` is DEPRECATED — kept only for upload metadata path

### Frontend Components 🔒 (Frozen 2026-01-23)
- `frontend/src/pages/my-libs/` - Library list view
- `frontend/src/pages/starred/` - Starred files & libraries
- `frontend/src/components/dirent-list-view/` - File download functionality

### Protocol Behaviors 🔒
- fs-id-list: JSON array (NOT newline-separated)
- Commit objects: OMIT `no_local_history` field
- `encrypted` field: integer in download-info, string in commits
- `is_corrupted` field: integer 0 (NOT boolean)
- `/seafhttp/` auth: `Seafile-Repo-Token` header (NOT `Authorization`)

---

## Critical Context for Next Session 📝

### 🎯 Project Goal
**Mission**: Build complete Seafile replacement ready for production
**Target Users**: Global cloud storage, especially needing China access
**Timeline**: ASAP but thorough - "want it soon, do it right"

### 📊 Current State (Updated 2026-03-05)
- **Sync Protocol**: 100% working, desktop clients fully compatible 🔒 FROZEN
- **Backend API**: ~98% implemented — OIDC ✅, GC implementation present; destructive activation blocked by X1 alone (X2 closed 2026-08-14), Library Settings ✅, OnlyOffice ✅, Tags cascade ✅, Org Admin Panel ✅, Superadmin Departments ✅
- **Frontend UI**: ~83% functional (all modals migrated, folder icons ✅, ~51 ModalPortal wrappers to clean up)
- **Production Ready**: blocked for destructive GC until X1 closes (X2 closed 2026-08-14); keep `GC_ENABLED=false` on every replica/DC
- **Admin Panels**: Both superadmin and org admin at feature parity
- **Active Bugs**: tracked canonically in `docs/KNOWN_ISSUES.md`; X1 is the sole remaining GC blocker (X2 closed)

### Critical Facts to Remember

**Permissions System** (UPDATED 2026-01-27):
- Backend: ✅ 100% COMPLETE - All endpoints check permissions
- Frontend: 🟡 ~30% - "New Library" button done, many features remain
- API returns: `can_add_repo`, `can_share_repo`, `can_add_group`, etc.
- Check `window.app.pageOptions.canAddRepo` in render methods

**User Roles**:
- `admin` → Full access, `is_staff: true`
- `user` → Can create libraries, share, upload
- `readonly` → View only, no write operations
- `guest` → Most restricted, view only

**Test Users** (password: `password` for all):
- `admin@sesamefs.local` (token: `dev-token-admin`)
- `user@sesamefs.local` (token: `dev-token-user`)
- `readonly@sesamefs.local` (token: `dev-token-readonly`)
- `guest@sesamefs.local` (token: `dev-token-guest`)

---

## Documentation Map 📚

### Session Continuity (Read First Every Session)
- **[CURRENT_WORK.md](CURRENT_WORK.md)** - This file - Session state, priorities
- **[docs/KNOWN_ISSUES.md](docs/KNOWN_ISSUES.md)** - Detailed bug tracking
- **[docs/CHANGELOG.md](docs/CHANGELOG.md)** - Session history
- **[docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md)** - Component stability matrix

### Protocol & Sync (🔒 Reference Implementation)
- **[docs/SEAFILE-SYNC-PROTOCOL-RFC.md](docs/SEAFILE-SYNC-PROTOCOL-RFC.md)** - Formal RFC with test vectors 🔒
- **[docs/ENCRYPTION.md](docs/ENCRYPTION.md)** - Encrypted libraries, PBKDF2, Argon2id

### Implementation Guides
- **[docs/API-REFERENCE.md](docs/API-REFERENCE.md)** - API endpoints, implementation status
- **[docs/ENDPOINT-REGISTRY.md](docs/ENDPOINT-REGISTRY.md)** - ⚠️ CHECK BEFORE ADDING ENDPOINTS
- **[docs/FRONTEND.md](docs/FRONTEND.md)** - React frontend patterns, modal fixes
- **[CLAUDE.md](CLAUDE.md)** - Complete project context for AI assistant

---

## Quick Commands

```bash
# Run server
docker compose up -d sesamefs frontend

# Rebuild after changes
docker compose build --no-cache sesamefs frontend && docker compose up -d

# Test API with different users
curl -H "Authorization: Token dev-token-admin" http://localhost:8082/api2/account/info/
curl -H "Authorization: Token dev-token-readonly" http://localhost:8082/api2/account/info/

# Run tests (ALWAYS use test.sh)
./scripts/test.sh api              # Bash integration tests (335+ assertions)
./scripts/test.sh go               # Go unit tests
./scripts/test.sh go-integration   # Go integration tests (requires backend)
./scripts/test.sh all              # Everything
./scripts/test.sh api --quick      # Skip slow tests
```

---

## End of Session Checklist

**📋 See [docs/SESSION_CHECKLIST.md](docs/SESSION_CHECKLIST.md) for complete checklist**

Quick reminders:
- [x] Update `CURRENT_WORK.md` (what was done, next priorities)
- [x] Update `docs/KNOWN_ISSUES.md` (bugs fixed/discovered)
- [x] Update `docs/CHANGELOG.md` (add session entry)
- [x] Keep `CURRENT_WORK.md` under 500 lines
