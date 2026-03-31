# Implementation Status - SesameFS

**Last Updated**: 2026-03-30

---

## Project Completeness Summary

**Overall Production Readiness**: ~80%

| Area | Completeness | Notes |
|------|--------------|-------|
| Sync Protocol (Desktop) | 100% ✅ | 🔒 FROZEN - Working perfectly |
| Core Backend API | ~98% | GC ✅, OIDC ✅, Library Settings ✅, Monitoring ✅ |
| Admin Panels | ~95% | Superadmin ✅, Org Admin ✅, both at parity. Audit logs pending |
| Frontend UI | ~85% | All 122 modals migrated ✅, File History UI ✅, permission UI (~75% with granular flags), ~51 ModalPortal wrappers to clean up |
| Authentication | ~80% | OIDC Phase 1 complete, JWT revocation hardened (2026-03-31), dev tokens supported |
| Production Infrastructure | ✅ ~97% | GC ✅, Monitoring ✅, Health checks ✅, Structured logging ✅, Frontend/Backend separation ✅, Nginx production hardening ✅ |

**✅ Production Blockers — ALL COMPLETE**:
1. ~~OIDC Authentication~~ - ✅ COMPLETE (Phase 1 - Basic Login)
2. ~~Garbage Collection~~ - ✅ COMPLETE (Queue worker + scanner + admin API)
3. ~~Monitoring/Health Checks~~ - ✅ COMPLETE (slog logging, `/health`, `/ready`, `/metrics`)
4. ~~Frontend/Backend Separation~~ - ✅ COMPLETE (2026-03-30/31) — separate React/nginx container, bootstrap API, nginx production hardening

---

## Status Legend

| Symbol | Meaning | Stability | Safe to Modify? |
|--------|---------|-----------|-----------------|
| 🔒 **FROZEN** | Meets all freeze criteria, soak period complete | **STABLE** | ❌ No - only with user approval |
| 🟢 **RELEASE-CANDIDATE** | Meets freeze prerequisites, in soak period | **STABLE** | ⚠️ Bug fixes only - resets soak counter |
| ✅ **COMPLETE** | Implemented, basic testing done | Mostly stable | ⚠️ Caution - review tests first |
| 🟡 **PARTIAL** | Stub exists or incomplete implementation | **UNSTABLE** | ✅ Yes - active development |
| ❌ **TODO** | Not started | N/A | ✅ Yes - greenfield |

**Promotion rules**: See [RELEASE-CRITERIA.md](RELEASE-CRITERIA.md) for the full procedure.
- ✅ → 🟢: needs ≥ 80% Go coverage (60% for shared pkgs), ≥ 90% integration endpoint coverage, zero open bugs, Component Test Map entry
- 🟢 → 🔒: needs 3 consecutive clean sessions with all tests passing and no new bugs

---

## Core Components

| Component | Status | Stability | Protocol Tested | Last Verified | Notes |
|-----------|--------|-----------|-----------------|---------------|-------|
| **Sync Protocol (Desktop Client)** | 🔒 FROZEN | **STABLE** | ✅ Yes | 2026-01-16 | Both comparison + real client tests pass |
| **Encrypted Libraries (PBKDF2)** | 🔒 FROZEN | **STABLE** | ✅ Yes | 2026-02-04 | 90.8% unit coverage, 39 tests. Test vectors verified. |
| **File Block Encryption (AES-256-CBC)** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-01-09 | Works with desktop client |
| **Block Storage (S3)** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-02-16 | SHA-1→SHA-256 mapping working. Custom HTTP transport (64 conn/host, 128KB buffers). |
| **Block ID Mapping (SHA-1→SHA-256)** | ✅ COMPLETE | Mostly stable | ✅ Yes | 2026-02-16 | Batch IN queries (100/batch) for downloads. Per-block fallback still works. |
| **File Upload (REST API)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-04 | Replace/autorename fix: `replace=0` triggers auto-rename (`file (1).ext`), default is overwrite for desktop client compat |
| **File Download (REST API)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-16 | Optimized: shared `streaming` package — prefetch pipeline, 4MB buffers, batch block resolve, ZIP Store. ~300 MB/s for 11 GB. |
| **Directory Listing** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-08 | Frontend integration works |
| **Library CRUD** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-01-08 | Create/delete/list working |
| **Starred Files** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-08 | Fixed Cassandra query issue |
| **OnlyOffice Integration** | 🔒 FROZEN | **STABLE** | ❌ No | 2026-02-12 | Document editing stable — doc key rotation fix (was causing toolbar greying out) + JWT 8h expiry |
| **Frontend (React)** | 🟡 PARTIAL | **UNSTABLE** | N/A | 2026-03-30 | Library list works, all modals migrated, ~51 ModalPortal wrappers to remove. Plans/permissions Phase 3 is in progress: owner/plan terminology and member-limit gating now use live data in key admin screens, but legacy plan-role code paths, org-admin shell placeholders, and quota unit standardization still remain. |
| **Frontend Logout** | 🔒 FROZEN | **STABLE** | N/A | 2026-01-27 | Working - nginx proxies /accounts/ to backend |
| **User Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-26 | OIDC login + dev tokens supported. `users.last_login_at` is now persisted on successful login/session creation and exposed in admin/org-admin user responses. Historical login analytics remain unimplemented. |
| **Database Seeding** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-23 | Auto-creates default org + admin user on first run |
| **Sharing System** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-12 | Share to users/groups + share links + upload links + group permissions. Custom share permissions with granular flags. Creator info in share/upload link responses. Password-protected share links with HMAC cookie auth (SHARE_LINK_HMAC_KEY). |
| **Share Dialog UI** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-12 | 6 tabs fully implemented: Share Link (create/list/update/delete + password/expiry/permissions/batch/email), Upload Link, Internal Link, Share to User, Share to Group, Custom Sharing Permissions. «Invite Guest» and «Share to Other Server (OCM)» tabs are disabled stubs (canInvitePeople=false, enableOCM=false) — no backend endpoints exist. |
| **Groups Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-22 | Create/manage groups + members fully implemented |
| **Departments (Hierarchical Groups)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-31 | Admin CRUD + hierarchy, 29 integration tests |
| **Permission Middleware** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-11 | Backend 100% complete, applied to all routes. Enhanced with granular `PermissionFlags` (8 flags), custom share permissions, `RequirePermFlag()` middleware. 366 lines of new tests. |
| **Custom Share Permissions** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-11 | Custom permissions with granular flags, per-user CRUD, DB tables (`custom_share_permissions`, `custom_share_permissions_by_user`), flag resolution with OR merge |
| **File Tags** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | Repo tags + file tagging + cascade cleanup on delete/move + tag migration on rename |
| **Batch Operations** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-27 | Sync/async move/copy, task tracking |
| **Search** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-22 | Cassandra SASI implementation |
| **OIDC Authentication** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-31 | Phase 1 complete - SSO login working. JWT revocation hardened: revoked JWT tokens now checked against DB on cache miss (prevents re-authentication after logout/deactivation when `OIDC_JWT_SIGNING_KEY` is set) |
| **OIDC Group/Dept Sync** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-02 | Claims extraction, sync on login, full sync mode |
| **Garbage Collection** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-18 | 9 item types (incl. `user_cascade`, `library_cascade`, `org_cascade`), 12 scanner phases, soft-delete cascades for users (7-day grace), libraries (30-day trash), orgs (30-day grace). Full artifact cleanup, atomic group deletion, audit log, health metrics |
| **Admin Panel (Groups/Users)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-02 | 16 admin endpoints + OIDC group/dept sync, 29 tests |
| **Admin Dashboard KPIs** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-26 | Sysadmin and org-admin overview KPIs now use real storage, file, active-user, and this-month/this-year traffic data. Remaining gaps: device counts are unavailable and sysadmin license metadata is still stubbed. Redesign backlog remains documented in `docs/DASHBOARD-REDESIGN-PLAN.md` and `docs/ADMIN-DASHBOARD-WIREFRAMES.md`. |
| **Admin Library Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 12 endpoints in admin.go + seafile-api.js methods + trash libraries |
| **Admin Link Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 13 endpoints: share link admin (list/delete), upload links (user CRUD + admin), per-user links. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 2 |
| **Superadmin Departments/Address Book** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-05 | 9 endpoints: dept CRUD, address book groups, group-owned libraries. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 4 |
| **Org Admin Panel** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-30 | 50+ endpoints in org_admin.go. Users, groups, repos, trash, departments, links are present. Ownership transfer is now visible from the users page and member creation is gated by live `max_users` data, but there is still a confirmed statistics scope bug and some org-admin screens still depend on placeholder shell flags injected via `window.org.pageOptions`. |
| **Frontend/Backend Separation** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-31 | Go is a pure API server. React SPA served by separate `nginx:alpine` container (`frontend/Dockerfile`). Bootstrap API (`GET /api/v2.1/bootstrap/`) replaces server-side placeholder injection. `constants.js` live bindings fix org-admin orgID timing. 6 nginx production bugs fixed (client_max_body_size 100G, proxy timeouts, buffering, HTTP/1.1 keepalive). Bundle hash coupling fixed via `asset-manifest.json` HTTP fetch. |
| **Storage & Traffic Quotas** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-30 | Traffic recording (fire-and-forget), quota enforcement (free=hard block, paid=soft warning), storage counters (4 scopes), statistics API, plan management API. All upload/download paths instrumented. Library soft-delete adjusts storage counters. Frontend quota-entry migration to GB is mostly done, but UI unit consistency is still pending because some screens appear decimal (`1000^3`) while backend/utilities are binary-byte based (`1024^3`). See [QUOTAS-AND-TRAFFIC-PLAN.md](QUOTAS-AND-TRAFFIC-PLAN.md) |
| **Audit Logs** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-26 | `audit_log` table with 365-day TTL exists for deletion events (GC, groups, departments), and `users.last_login_at` now covers latest successful login. Historical login logs and file-operation/file-access event tables are still missing, so login audit pages and file statistics pages remain pending. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 3 |
| **File/Folder Trash** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-11 | List, restore, clean trash + browse deleted folders. Enhanced: filters out children of deleted directories (2026-03-11) |
| **Library Recycle Bin** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-25 | Soft-delete, restore, permanent delete. User + admin endpoints. Storage counters adjusted on delete/restore (2026-03-25) |
| **File Expiry Countdown** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-05 | `expires_at` in directory listing for auto-delete libraries |
| **Admin Library Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 12 endpoints in admin.go + seafile-api.js methods + trash libraries |
| **Admin Link Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 13 endpoints: share link admin (list/delete), upload links (user CRUD + admin), per-user links. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 2 |
| **Audit Logs** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-26 | `audit_log` table with 365-day TTL exists for deletion events (GC, groups, departments), and `users.last_login_at` now covers latest successful login. Historical login logs and file-operation/file-access event tables are still missing, so login audit pages and file statistics pages remain pending. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 3 |
| **Version History UI** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-26 | Detail sidebar History tab + full-page view + revert conflict dialog (Replace/Keep Both/Cancel) + View action + proper user name resolution. 17 integration tests. |
| **File Preview & Raw Serving** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | Inline preview for PDF/images/video/audio/text, OnlyOffice for docs. Auth token handling fixed for search results. 14 unit + 28 integration tests. |
| **Monitoring/Health Checks** | 🔒 FROZEN | **STABLE** | ❌ No | 2026-02-04 | Structured logging, `/health`, `/ready`, `/metrics`. 5 unit + 21 integration tests. |
| **Multi-Region Replication** | ❌ TODO | N/A | ❌ No | - | Future feature |

---

## Protocol Endpoints (Seafile Compatibility)

### Sync Protocol (`/seafhttp/`) - 🔒 FROZEN

**DO NOT MODIFY** these endpoints without explicit user request or desktop client breakage.

| Endpoint | Status | Stability | Last Verified | Critical Details |
|----------|--------|-----------|---------------|------------------|
| `GET /seafhttp/protocol-version` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Returns `{"version": 2}` |
| `GET /seafhttp/repo/:id/permission-check/` | 🔒 FROZEN | **STABLE** | 2026-02-11 | Checks real library permissions via PermissionMiddleware; returns 200 OK or 403 |
| `GET /seafhttp/repo/:id/quota-check/` | 🔒 FROZEN | **STABLE** | 2026-02-11 | Verifies read access, returns quota info |
| `GET /seafhttp/repo/:id/commit/HEAD` | 🔒 FROZEN | **STABLE** | 2026-01-16 | `is_corrupted` MUST be integer 0 |
| `PUT /seafhttp/repo/:id/commit/HEAD` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Update HEAD pointer |
| `GET /seafhttp/repo/:id/commit/:cid` | 🔒 FROZEN | **STABLE** | 2026-01-16 | `encrypted: "true"` (string), NO `no_local_history` |
| `PUT /seafhttp/repo/:id/commit/:cid` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Store commit object |
| `GET /seafhttp/repo/:id/fs-id-list/` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Returns JSON array (NOT newline-separated) |
| `GET /seafhttp/repo/:id/fs/:fsid` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Get single FS object |
| `POST /seafhttp/repo/:id/pack-fs` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Binary: 40-byte ID + 4-byte size (BE) + zlib |
| `POST /seafhttp/repo/:id/recv-fs` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Receive FS objects (binary format) |
| `POST /seafhttp/repo/:id/check-fs` | 🔒 FROZEN | **STABLE** | 2026-01-16 | JSON array input/output |
| `POST /seafhttp/repo/:id/check-blocks` | 🔒 FROZEN | **STABLE** | 2026-01-16 | JSON array input/output |
| `GET /seafhttp/repo/:id/block/:bid` | ✅ COMPLETE | Mostly stable | 2026-01-09 | SHA-1→SHA-256 mapping works |
| `PUT /seafhttp/repo/:id/block/:bid` | ✅ COMPLETE | Mostly stable | 2026-01-09 | Block upload works |
| `POST /seafhttp/repo/head-commits-multi` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Multi-repo HEAD check |
| `GET /seafhttp/repo/folder-perm` | ✅ COMPLETE | Mostly stable | 2026-02-19 | Folder-level permissions (stub: returns empty = full access) |

**Protocol Requirements** (from `docs/SEAFILE-SYNC-PROTOCOL-RFC.md`):
- Authentication: `Seafile-Repo-Token` header (NOT `Authorization`)
- fs-id-list: MUST return JSON array
- Commit objects: MUST omit `no_local_history` field
- Field types: See Critical Format Requirements table below

### REST API - Libraries (`/api2/repos/`, `/api/v2.1/repos/`)

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api2/repos/` | ✅ COMPLETE | Mostly stable | List libraries |
| `POST /api2/repos/` | ✅ COMPLETE | Mostly stable | Create library (supports `passwd` param) |
| `GET /api2/repos/:id/` | ✅ COMPLETE | Mostly stable | Get library info |
| `POST /api2/repos/:id/?op=rename` | ✅ COMPLETE | Mostly stable | Rename library |
| `DELETE /api2/repos/:id/` | ✅ COMPLETE | Mostly stable | Delete library |
| `GET /api2/repos/:id/download-info/` | 🔒 FROZEN | **STABLE** | Sync token + real permission level (2026-02-11: no longer hardcoded "rw") |
| `GET /api/v2.1/repos/` | ✅ COMPLETE | Mostly stable | v2.1 API variant |
| `DELETE /api/v2.1/repos/:id/` | ✅ COMPLETE | Mostly stable | v2.1 delete variant |

### REST API - File Operations

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api2/repos/:id/dir/` | ✅ COMPLETE | Mostly stable | List directory contents |
| `POST /api/v2.1/repos/:id/dir/?operation=mkdir` | ✅ COMPLETE | Mostly stable | Create directory |
| `POST /api/v2.1/repos/:id/dir/?operation=rename` | ✅ COMPLETE | Mostly stable | Rename directory |
| `DELETE /api/v2.1/repos/:id/dir/` | ✅ COMPLETE | Mostly stable | Delete directory |
| `GET /api2/repos/:id/file/` | ✅ COMPLETE | Mostly stable | Get file info |
| `POST /api/v2.1/repos/:id/file/?operation=create` | ✅ COMPLETE | Mostly stable | Create file |
| `POST /api/v2.1/repos/:id/file/?operation=rename` | ✅ COMPLETE | Mostly stable | Rename file |
| `DELETE /api/v2.1/repos/:id/file/` | ✅ COMPLETE | Mostly stable | Delete file |
| `PUT /api2/repos/:id/file/` | ✅ COMPLETE | Mostly stable | Lock/unlock file |
| `GET /api2/repos/:id/upload-link/` | ✅ COMPLETE | Mostly stable | Get upload URL |
| `POST /seafhttp/upload-api/:token` | ✅ COMPLETE | Mostly stable | Upload file (multipart) |
| `GET /api2/repos/:id/file/download-link` | ✅ COMPLETE | Mostly stable | Get download URL |
| `GET /seafhttp/files/:token/:filename` | ✅ COMPLETE | Mostly stable | Download file |
| `POST /api/v2.1/repos/:id/file/move/` | ✅ COMPLETE | Mostly stable | Move file (supports batch) |
| `POST /api/v2.1/repos/:id/file/copy/` | ✅ COMPLETE | Mostly stable | Copy file (supports batch) |
| `GET /api/v2.1/repos/:id/dir/` | ✅ COMPLETE | Mostly stable | v2.1 list directory |
| `GET /api/v2.1/repos/:id/file/?p=:path` | ✅ COMPLETE | Stable | Get file metadata with view_url (for "View on Cloud") |

### REST API - Encrypted Libraries

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `POST /api/v2.1/repos/:id/set-password/` | 🔒 FROZEN | **STABLE** | Unlock library (verify password) |
| `PUT /api/v2.1/repos/:id/set-password/` | 🔒 FROZEN | **STABLE** | Change library password |

### REST API - Sharing

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/repos/:id/share-links/` | ✅ COMPLETE | Mostly stable | List share links (2026-01-22) |
| `POST /api/v2.1/repos/:id/share-links/` | ✅ COMPLETE | Mostly stable | Create share link with optional password + expiry (2026-01-22). Bcrypt password hash stored. |
| `PUT /api/v2.1/share-links/:token/` | ✅ COMPLETE | Mostly stable | Update password / expiry / permissions for existing link |
| `DELETE /api/v2.1/repos/:id/share-links/:token/` | ✅ COMPLETE | Mostly stable | Delete share link (2026-01-22) |
| `POST /d/:token/check-password/` | ✅ COMPLETE | Mostly stable | Verify share link password → sets HMAC cookie (`sesamefs_slpwd_*`) |
| `POST /u/:token/check-password/` | ✅ COMPLETE | Mostly stable | Verify upload link password → sets HMAC cookie (`sesamefs_ulpwd_*`) |
| `GET /api2/repos/:id/dir/shared_items/` | ✅ COMPLETE | Mostly stable | List shares for file/folder (2026-01-22) |
| `PUT /api2/repos/:id/dir/shared_items/` | ✅ COMPLETE | Mostly stable | Share to users/groups (2026-01-22) |
| `POST /api2/repos/:id/dir/shared_items/` | ✅ COMPLETE | Mostly stable | Update share permission (2026-01-22) |
| `DELETE /api2/repos/:id/dir/shared_items/` | ✅ COMPLETE | Mostly stable | Remove share (2026-01-22) |
| `GET /api2/shared-repos/` | ✅ COMPLETE | Mostly stable | List repos I shared (2026-01-22) |
| `GET /api2/beshared-repos/` | ✅ COMPLETE | Mostly stable | List repos shared with me (2026-01-22) |
| `GET/POST/PUT/DELETE /repos/:id/custom-share-permissions/` | ✅ COMPLETE | Mostly stable | Custom share permission CRUD with granular flags (2026-03-11) |

### REST API - Groups

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/groups/` | ✅ COMPLETE | Mostly stable | List my groups (2026-01-22) |
| `POST /api/v2.1/groups/` | ✅ COMPLETE | Mostly stable | Create group (2026-01-22) |
| `GET /api/v2.1/groups/:id/` | ✅ COMPLETE | Mostly stable | Get group details (2026-01-22) |
| `PUT /api/v2.1/groups/:id/` | ✅ COMPLETE | Mostly stable | Update group (rename) (2026-01-22) |
| `DELETE /api/v2.1/groups/:id/` | ✅ COMPLETE | Mostly stable | Delete group (2026-01-22) |
| `GET /api/v2.1/groups/:id/members/` | ✅ COMPLETE | Mostly stable | List group members (2026-01-22) |
| `POST /api/v2.1/groups/:id/members/` | ✅ COMPLETE | Mostly stable | Add member to group (2026-01-22) |
| `DELETE /api/v2.1/groups/:id/members/:email/` | ✅ COMPLETE | Mostly stable | Remove member from group (2026-01-22) |

### REST API - Departments (Hierarchical Groups)

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/admin/address-book/groups/` | ✅ COMPLETE | Mostly stable | List all departments (admin) (2026-01-31) |
| `POST /api/v2.1/admin/address-book/groups/` | ✅ COMPLETE | Mostly stable | Create department (2026-01-31) |
| `GET /api/v2.1/admin/address-book/groups/:id/` | ✅ COMPLETE | Mostly stable | Get dept with members/sub-depts/ancestors (2026-01-31) |
| `PUT /api/v2.1/admin/address-book/groups/:id/` | ✅ COMPLETE | Mostly stable | Rename department (2026-01-31) |
| `DELETE /api/v2.1/admin/address-book/groups/:id/` | ✅ COMPLETE | Mostly stable | Delete (blocks if has children) (2026-01-31) |
| `GET /api/v2.1/departments/` | ✅ COMPLETE | Mostly stable | List departments user belongs to (2026-01-31) |

### REST API - File Tags

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/repos/:id/repo-tags/` | ✅ COMPLETE | Mostly stable | List repository tags (2026-01-22) |
| `POST /api/v2.1/repos/:id/repo-tags/` | ✅ COMPLETE | Mostly stable | Create tag (2026-01-22) |
| `PUT /api/v2.1/repos/:id/repo-tags/:tag_id/` | ✅ COMPLETE | Mostly stable | Update tag (2026-01-22) |
| `DELETE /api/v2.1/repos/:id/repo-tags/:tag_id/` | ✅ COMPLETE | Mostly stable | Delete tag (2026-01-22) |
| `GET /api/v2.1/repos/:id/file-tags/` | ✅ COMPLETE | Mostly stable | Get tags for file (2026-01-22) |
| `POST /api/v2.1/repos/:id/file-tags/` | ✅ COMPLETE | Mostly stable | Add tag to file (2026-01-22) |
| `DELETE /api/v2.1/repos/:id/file-tags/:file_tag_id/` | ✅ COMPLETE | Mostly stable | Remove tag from file (2026-01-22) |

### REST API - File Preview & Raw Serving

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /lib/:repo_id/file/*filepath` | ✅ COMPLETE | Mostly stable | Inline file preview (HTML wrapper) (2026-02-04) |
| `GET /repo/:repo_id/raw/*filepath` | ✅ COMPLETE | Mostly stable | Raw file serving with MIME detection (2026-02-04) |
| `GET /repo/:repo_id/history/download` | ✅ COMPLETE | Mostly stable | Download historic file version (2026-02-03) |

### REST API - Starred Files

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api2/starredfiles/` | ✅ COMPLETE | Mostly stable | List starred files |
| `POST /api2/starredfiles/` | ✅ COMPLETE | Mostly stable | Star file |
| `DELETE /api2/starredfiles/` | ✅ COMPLETE | Mostly stable | Unstar file |
| `GET /api/v2.1/starred-items/` | ✅ COMPLETE | Mostly stable | v2.1 variant (fixed 2026-01-08) |

### REST API - OnlyOffice

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/onlyoffice/editor/:id` | 🔒 FROZEN | **STABLE** | Get editor config (2026-01-22) |
| `POST /api/v2.1/onlyoffice/callback/` | 🔒 FROZEN | **STABLE** | OnlyOffice save callback (2026-01-22) |

### REST API - Admin Panel (Groups, Users, Departments)

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/admin/groups/` | ✅ COMPLETE | Mostly stable | List all groups (2026-02-02) |
| `POST /api/v2.1/admin/groups/` | ✅ COMPLETE | Mostly stable | Create group (FormData) (2026-02-02) |
| `DELETE /api/v2.1/admin/groups/:id/` | ✅ COMPLETE | Mostly stable | Delete group (2026-02-02) |
| `PUT /api/v2.1/admin/groups/:id/` | ✅ COMPLETE | Mostly stable | Transfer ownership (2026-02-02) |
| `GET /api/v2.1/admin/groups/:id/members/` | ✅ COMPLETE | Mostly stable | List members (2026-02-02) |
| `POST /api/v2.1/admin/groups/:id/members/` | ✅ COMPLETE | Mostly stable | Add member (2026-02-02) |
| `DELETE /api/v2.1/admin/groups/:id/members/:email/` | ✅ COMPLETE | Mostly stable | Remove member (2026-02-02) |
| `GET /api/v2.1/admin/groups/:id/libraries/` | ✅ COMPLETE | Mostly stable | Group libraries (2026-02-02) |
| `GET /api/v2.1/admin/search-group/` | ✅ COMPLETE | Mostly stable | Search groups (2026-02-02) |
| `GET /api/v2.1/admin/users/` | ✅ COMPLETE | Mostly stable | List users — platform superadmin surface, all orgs (2026-02-23, hardened 2026-03-27) |
| `POST /api/v2.1/admin/users/` | ✅ COMPLETE | Mostly stable | Create user, dual-write users + users_by_email (2026-02-02) |
| `GET /api/v2.1/admin/users/:email/` | ✅ COMPLETE | Mostly stable | Get user by email (2026-02-02) |
| `PUT /api/v2.1/admin/users/:email/` | ✅ COMPLETE | Mostly stable | Update user (2026-02-02) |
| `DELETE /api/v2.1/admin/users/:email/` | ✅ COMPLETE | Mostly stable | Deactivate user (2026-02-02) |
| `GET /api/v2.1/admin/search-user/` | ✅ COMPLETE | Mostly stable | Search users — platform superadmin surface, all orgs (2026-02-23, hardened 2026-03-27) |
| `GET /api/v2.1/admin/admins/` | ✅ COMPLETE | Mostly stable | List elevated users — platform superadmin surface, response key: `admin_user_list` (2026-02-23, hardened 2026-03-27) |
| `GET /api/v2.1/admin/libraries/` | ✅ COMPLETE | Mostly stable | 12 endpoints. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 1 (2026-02-12) |
| `GET /api/v2.1/admin/share-links/` | ✅ COMPLETE | Mostly stable | 13 endpoints. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 2 (2026-02-12) |
| `GET /api/v2.1/admin/address-book/groups/` | ✅ COMPLETE | Mostly stable | 6 endpoints + group-owned libs. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 4 (2026-03-05) |
| `GET /api/v2.1/admin/logs/*` | ❌ TODO | N/A | See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 3 |

### REST API — Org Admin Panel

Pending compatibility cleanup:
- Frontend and mobile clients still use legacy wire names such as `is_staff` and `is_org_staff`.
- Canonical semantics are already split in backend docs/code, but the public naming migration to something like `isSuperAdmin` remains pending to avoid breaking existing clients.

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/org/admin/info/` | ✅ COMPLETE | Mostly stable | Org info (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/users/` | ✅ COMPLETE | Mostly stable | User CRUD — 12 endpoints (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/groups/` | ✅ COMPLETE | Mostly stable | Group CRUD + members — 13 endpoints (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/repos/` | ✅ COMPLETE | Mostly stable | Repos CRUD + dirents — 4 endpoints (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/trash-libraries/` | ✅ COMPLETE | Mostly stable | Trash mgmt — 4 endpoints (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/departments/` | ✅ COMPLETE | Mostly stable | Departments + address book — 6 endpoints (2026-03-05) |
| `GET /api/v2.1/org/admin/links/` | ✅ COMPLETE | Mostly stable | Share links + upload links — 4 endpoints (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/groups/:gid/group-owned-libraries/` | ✅ COMPLETE | Mostly stable | Group-owned libs — 2 endpoints (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/devices/` | ✅ COMPLETE | Mostly stable | Empty responses — no device table (2026-03-05) |
| `GET /api/v2.1/org/:org_id/admin/statistics/*` | 🟡 PARTIAL | **UNSTABLE** | Stub (501) — 5 endpoints |
| `GET /api/v2.1/org/admin/logs/*` | 🟡 PARTIAL | **UNSTABLE** | Stub (501) — 3 endpoints |

### REST API - Batch Operations

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `POST /api/v2.1/repos/sync-batch-move-item/` | ✅ COMPLETE | Mostly stable | Synchronous move (same repo) |
| `POST /api/v2.1/repos/sync-batch-copy-item/` | ✅ COMPLETE | Mostly stable | Synchronous copy (same repo) |
| `POST /api/v2.1/repos/async-batch-move-item/` | ✅ COMPLETE | Mostly stable | Async move, returns task_id |
| `POST /api/v2.1/repos/async-batch-copy-item/` | ✅ COMPLETE | Mostly stable | Async copy, returns task_id |
| `GET /api/v2.1/copy-move-task/` | ✅ COMPLETE | Mostly stable | Query task progress |
| `DELETE /api/v2.1/repos/batch-delete-item/` | ✅ COMPLETE | Mostly stable | Delete multiple items |

### REST API - Library Settings

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET/PUT /api2/repos/:id/history-limit/` | ✅ COMPLETE | Mostly stable | History retention settings (2026-01-29) |
| `GET/PUT /api/v2.1/repos/:id/auto-delete/` | ✅ COMPLETE | Mostly stable | Auto-delete old files (2026-01-29) |
| `GET/POST/PUT/DELETE /api/v2.1/repos/:id/repo-api-tokens/` | ✅ COMPLETE | Mostly stable | Library API tokens (2026-01-29) |
| `PUT /api2/repos/:id/owner/` | ✅ COMPLETE | Mostly stable | Library transfer (2026-01-29) |
| `POST/DELETE /api/v2.1/monitored-repos/` | ❌ TODO | N/A | Watch/unwatch (needs notification system) |

### REST API - Garbage Collection Admin

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `GET /api/v2.1/admin/gc/status` | ✅ COMPLETE | Mostly stable | GC status + stats (2026-01-30) |
| `POST /api/v2.1/admin/gc/run` | ✅ COMPLETE | Mostly stable | Trigger worker/scanner (2026-01-30) |

### REST API - Authentication

| Endpoint | Status | Stability | Notes |
|----------|--------|-----------|-------|
| `POST /api2/auth-token/` | ✅ COMPLETE | Mostly stable | Login (dev mode only) |
| `GET /api2/account/info/` | ✅ COMPLETE | Mostly stable | User info with permission flags |
| `GET /api2/server-info/` | ✅ COMPLETE | Mostly stable | Server capabilities |
| `GET /api/v2.1/auth/oidc/config/` | ✅ COMPLETE | Mostly stable | Public OIDC configuration (2026-01-28) |
| `GET /api/v2.1/auth/oidc/login/` | ✅ COMPLETE | Mostly stable | OIDC login redirect (2026-01-28) |
| `POST /api/v2.1/auth/oidc/callback/` | ✅ COMPLETE | Mostly stable | OIDC code exchange (2026-01-28) |
| `GET /api/v2.1/auth/oidc/logout/` | ✅ COMPLETE | Mostly stable | OIDC Single Logout (SLO) (2026-01-28) |
| `GET /api2/auth/ping/` | ✅ COMPLETE | Mostly stable | Authenticated ping for token validation (2026-02-19) |

---

## Critical Format Requirements

**These MUST match exactly or desktop client breaks:**

| Endpoint | Field | Required Type | ❌ Wrong Type |
|----------|-------|---------------|---------------|
| `GET /api2/repos/{id}/download-info/` | `encrypted` | integer `0` or `1` | boolean |
| `GET /api2/repos/{id}/download-info/` | `salt` | string `""` | null |
| `GET /seafhttp/repo/{id}/commit/HEAD` | `is_corrupted` | integer `0` | boolean |
| `GET /seafhttp/repo/{id}/commit/{id}` | `encrypted` | string `"true"` | integer or boolean |
| `GET /seafhttp/repo/{id}/commit/{id}` | `repo_desc` | string `""` | null |
| `GET /seafhttp/repo/{id}/commit/{id}` | `repo_category` | string `""` | null |
| `GET /seafhttp/repo/{id}/commit/{id}` | `no_local_history` | **MUST OMIT** | any value breaks client |
| `GET /seafhttp/repo/{id}/fs-id-list/` | response | JSON array `[]` | newline-separated text |

---

## Encryption Components

| Component | Status | Stability | Verified Against | Notes |
|-----------|--------|-----------|------------------|-------|
| **PBKDF2 Key Derivation (Magic)** | 🔒 FROZEN | **STABLE** | Stock Seafile | Input: repo_id + password |
| **PBKDF2 Key Derivation (Random Key)** | 🔒 FROZEN | **STABLE** | Stock Seafile | Input: password ONLY |
| **Static Salt (enc_version 2)** | 🔒 FROZEN | **STABLE** | Stock Seafile | `{0xda,0x90,0x45,0xc3,0x06,0xc7,0xcc,0x26}` |
| **File Block Encryption (AES-256-CBC)** | ✅ COMPLETE | Mostly stable | Desktop client | Format: [16-byte IV][encrypted content] |
| **Decrypt Session Manager** | ✅ COMPLETE | Mostly stable | Manual testing | 1-hour TTL, in-memory |
| Argon2id (server-side storage) | ✅ COMPLETE | Mostly stable | Unit tests | 300× slower brute-force vs PBKDF2 |

**Test Vectors**: See `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` Section 11

---

## Frontend Components

| Component | Status | Stability | Notes |
|-----------|--------|-----------|-------|
| Library List | ✅ COMPLETE | Mostly stable | Delete, rename, star working |
| Directory Browser | ✅ COMPLETE | Mostly stable | File list, navigation works |
| File Upload (Drag & Drop) | ✅ COMPLETE | Mostly stable | Works with backend |
| File Download | ✅ COMPLETE | Mostly stable | Works with backend |
| Encrypted Library Unlock Dialog | ✅ COMPLETE | Mostly stable | Fixed 2026-01-08 (uses Bootstrap modal) |
| Library Password Change Dialog | ✅ COMPLETE | Mostly stable | Fixed 2026-01-08 (uses Bootstrap modal) |
| Delete Library Dialog | ✅ COMPLETE | Mostly stable | Fixed (uses Bootstrap modal) |
| Create Library Dialog | ✅ COMPLETE | Mostly stable | Fixed (uses Bootstrap modal) |
| Rename File/Folder Dialog | ✅ COMPLETE | Mostly stable | Fixed (uses Bootstrap modal) |
| Share Dialog | ✅ COMPLETE | Mostly stable | UI complete, backend stub |
| **Modal Dialogs** | ✅ COMPLETE | Mostly stable | All dialogs migrated to Bootstrap (2026-01-30) |
| OnlyOffice Editor Integration | 🟡 PARTIAL | **UNSTABLE** | Opens but toolbar sometimes greyed |
| Icon Loading | 🟡 PARTIAL | **UNSTABLE** | Some 404s, needs audit |

**Frontend Note**: All dialog files have been migrated from reactstrap Modal to plain Bootstrap modal classes (verified 2026-01-30). Some dialogs still import reactstrap for `Button`/`Input`/`Form` components — these work correctly and are cosmetic only.

---

## Database Schema (Cassandra)

| Table | Status | Notes |
|-------|--------|-------|
| `organizations` | ✅ COMPLETE | Org metadata |
| `users` | ✅ COMPLETE | User accounts |
| `user_sessions` | ✅ COMPLETE | Auth sessions |
| `libraries` | ✅ COMPLETE | Repository metadata |
| `fs_objects` | ✅ COMPLETE | File/directory metadata |
| `blocks` | ✅ COMPLETE | Block metadata |
| `block_id_mappings` | ✅ COMPLETE | SHA-1 → SHA-256 translation |
| `commits` | ✅ COMPLETE | Commit history |
| `starred_files` | ✅ COMPLETE | User starred items |
| `file_locks` | ✅ COMPLETE | File lock state |
| `share_links` | ✅ COMPLETE | Share link metadata |
| `library_shares` | ✅ COMPLETE | Library sharing |
| `file_shares` | ✅ COMPLETE | File/folder sharing |
| All other tables | ✅ COMPLETE | See `docs/DATABASE-GUIDE.md` |

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

### 2. ~~Garbage Collection~~ ✅ COMPLETE (2026-01-30)
- **Impact**: ~~Storage costs grow forever~~ Now automatically cleaned up
- **Status**: Fully implemented — queue worker + safety scanner + admin API
- **Files**: `internal/gc/` — gc.go, queue.go, worker.go, scanner.go, store.go, store_mock.go, store_cassandra.go, gc_hooks.go, gc_adapter.go
- **Tests**: 88 Go unit tests + 21 bash integration tests
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

### Priority 1: Desktop Client Compatibility ✅ COMPLETE

- Sync protocol working perfectly (7 test scenarios, 100% success)
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

### Priority 3: Library Settings Backend ✅ COMPLETE

3. **Library settings endpoints** — All implemented (2026-01-29)
   - History limit, auto-delete, API tokens, transfer — all working
   - File: `internal/api/v2/library_settings.go`

### Priority 4: Documentation

4. **Missing documentation**
   - User documentation (how to use)
   - Admin documentation (deployment, backup)
   - Production deployment guide
   - Migration guide (from Seafile)

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

**Last Updated**: 2026-03-11

| Metric | Value | Notes |
|--------|-------|-------|
| Sync Protocol Endpoints | 13/13 (100%) | All frozen ✅ |
| REST API Endpoints (Core) | ~60/62 (97%) | Missing: monitored-repos, audit logs. New: 5 custom share permission endpoints |
| Superadmin Endpoints | ~90+ | Libraries, links, users, groups, orgs, departments, address book, group-owned libs |
| Org Admin Endpoints | ~50+ | Full panel: users, groups, repos, trash, departments, links, address book |
| Frontend Components | ~85% complete | All modals migrated, permission-aware uploaders, ~51 ModalPortal wrappers to clean up |
| Desktop Client Compatibility | ✅ Working | Both tests passing |
| Test Coverage (Go) | ~30% overall | chunker 79%, crypto 90.8%, config 73%, auth 56%, health 100% |
| Integration Tests | 335+ tests | All passing (incl. OIDC, GC, file preview) |
| Frontend Tests | 165+ tests | 7 test files (incl. OIDC API) |
| Documentation Coverage | ~90% | Missing: user/admin docs |

**Stability Breakdown**:
- 🔒 FROZEN: ~22 components (sync protocol, encryption, OnlyOffice, monitoring/health)
- ✅ COMPLETE: ~45 components (CRUD, sharing, groups, tags, batch ops, OIDC, GC, monitoring, admin panels, org admin, custom share permissions)
- 🟡 PARTIAL: ~15 components (frontend UI, permission UI, org admin stubs)
- ❌ TODO: ~2 components (audit logs, monitored-repos)

**Production Readiness**:
- Backend: ~98% (all production blockers complete, both admin panels implemented)
- Frontend: ~85% (modals done, granular permission flags enforced, missing: some permission UI edge cases, ~51 ModalPortal wrapper cleanup)
- Infrastructure: ~95% (monitoring ✅, health checks ✅, GC ✅)
- Documentation: ~70% (missing: user/admin guides, deployment guide)
