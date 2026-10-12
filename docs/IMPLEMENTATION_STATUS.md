# Implementation Status - SesameFS

**Last Updated**: 2026-07-21

---

This file is the feature and endpoint matrix. Readiness, blockers and priorities are not tracked
here: see [V1-PRODUCTION-ROADMAP.md](V1-PRODUCTION-ROADMAP.md), [X1-CRITICAL-PATH.md](X1-CRITICAL-PATH.md)
(destructive GC stays disabled) and [OPEN-WORK-INDEX.md](OPEN-WORK-INDEX.md). The older readiness
summary, freeze rules and metrics are archived in
[legacy/IMPLEMENTATION_STATUS-SNAPSHOT-2026-07-21.md](legacy/IMPLEMENTATION_STATUS-SNAPSHOT-2026-07-21.md).

## Status Legend

| Symbol | Meaning | Stability | Safe to Modify? |
|--------|---------|-----------|-----------------|
| 🔒 **FROZEN** | Meets all freeze criteria, soak period complete | **STABLE** | ❌ No - only with user approval |
| 🟢 **RELEASE-CANDIDATE** | Meets freeze prerequisites, in soak period | **STABLE** | ⚠️ Bug fixes only - resets soak counter |
| ✅ **COMPLETE** | Implemented, basic testing done | Mostly stable | ⚠️ Caution - review tests first |
| 🟡 **PARTIAL** | Stub exists or incomplete implementation | **UNSTABLE** | ✅ Yes - active development |
| ❌ **TODO** | Not started | N/A | ✅ Yes - greenfield |

**The freeze/promotion model is retired (2026-10-12).** A 🔒 FROZEN or 🟢 label below now means
*compatibility-sensitive*: a change needs evidence that Seafile desktop/mobile clients still work,
not a soak period. The old procedure is in
[legacy/RELEASE-CRITERIA-2026-03-17.md](legacy/RELEASE-CRITERIA-2026-03-17.md).

---

## Core Components

| Component | Status | Stability | Protocol Tested | Last Verified | Notes |
|-----------|--------|-----------|-----------------|---------------|-------|
| **Sync Protocol (Desktop Client)** | ✅ COMPLETE | Mostly stable | ✅ Yes | 2026-05-20 | Functional and comparison tests pass; the real-client active-active harness now proves both the non-overlapping auto-merge race and the same-path fail-closed `503` preservation path, with broader scenario coverage remaining as follow-up debt |
| **Encrypted Libraries (PBKDF2)** | 🔒 FROZEN | **STABLE** | ✅ Yes | 2026-02-04 | 90.8% unit coverage, 39 tests. Test vectors verified. |
| **File Block Encryption (AES-256-CBC)** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-01-09 | Works with desktop client |
| **Block Storage (S3)** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-02-16 | SHA-1→SHA-256 mapping working. Custom HTTP transport (64 conn/host, 128KB buffers). |
| **Block ID Mapping (SHA-1→SHA-256)** | ✅ COMPLETE | Mostly stable | ✅ Yes | 2026-02-16 | Batch IN queries (100/batch) for downloads. Per-block fallback still works. |
| **File Upload (REST API)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-04-06 | Replace/autorename fix: `replace=0` triggers auto-rename (`file (1).ext`), default is overwrite for desktop client compat. Region-aware libraries now propagate `storage_class` as the preference for new materializations through `upload-link` and `seafhttp/upload-api`. |
| **File Download (REST API)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-04-06 | Optimized: shared `streaming` package — prefetch pipeline, 4MB buffers, batch block resolve, ZIP Store. Existing block reads resolve each block's persisted canonical storage class instead of the current library preference or host default. |
| **Directory Listing** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-01-08 | Frontend integration works |
| **Library CRUD** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-04-09 | Create/delete/list working. Library create flows now support explicit `storage_id`, hostname-derived default region selection, and org-level `storage_policy` (`strict` / `flexible`) for new libraries. Migration/change-region for non-empty libraries is still not implemented. |
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
| **OIDC Authentication** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-04-03 | Phase 1 complete - SSO login working. JWT revocation hardened: revoked JWT tokens now checked against DB on cache miss (prevents re-authentication after logout/deactivation when `OIDC_JWT_SIGNING_KEY` is set). User API keys now cover non-browser auth and `/api2/auth-token/` exchange for desktop/CLI clients. |
| **OIDC Group/Dept Sync** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-02 | Claims extraction, sync on login, full sync mode |
| **Garbage Collection** | 🟡 IMPLEMENTED / PROD DISABLED | Blocked by X1 + PRE-GC / A1 gate | ❌ No | 2026-09-29 | Worker/scanner/lease exist; keep `GC_ENABLED=false` until X1 and the PRE-GC / A1 gate (X1-CRITICAL-PATH.md §6) close. Accepted architecture is documented (D0 physical-life handoff) but not implemented. X2 closed 2026-08-14. After activation, only designated replicas in one DC may participate under the lease. |
| **Admin Panel (Groups/Users)** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-02 | 16 admin endpoints + OIDC group/dept sync, 29 tests |
| **Admin Dashboard KPIs** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-04-02 | Sysadmin and org-admin overview KPIs use real storage, file, active-user, and traffic data. Storage time-series uses `storage_daily_delta` for historical charts. Traffic stats use `traffic_counters` with per-type desglose. Remaining gaps: device counts unavailable, sysadmin license metadata stubbed. |
| **Admin Library Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 12 endpoints in admin.go + seafile-api.js methods + trash libraries |
| **Admin Link Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 13 endpoints: share link admin (list/delete), upload links (user CRUD + admin), per-user links. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 2 |
| **Superadmin Departments/Address Book** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-05 | 9 endpoints: dept CRUD, address book groups, group-owned libraries. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 4 |
| **Org Admin Panel** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-30 | 50+ endpoints in org_admin.go. Users, groups, repos, trash, departments, links are present. Ownership transfer is now visible from the users page and member creation is gated by live `max_users` data, but there is still a confirmed statistics scope bug and some org-admin screens still depend on placeholder shell flags injected via `window.org.pageOptions`. |
| **Frontend/Backend Separation** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-31 | Go is a pure API server. React SPA served by separate `nginx:alpine` container (`frontend/Dockerfile`). Bootstrap API (`GET /api/v2.1/bootstrap/`) replaces server-side placeholder injection. `constants.js` live bindings fix org-admin orgID timing. 6 nginx production bugs fixed (client_max_body_size 100G, proxy timeouts, buffering, HTTP/1.1 keepalive). Bundle hash coupling fixed via `asset-manifest.json` HTTP fetch. |
| **Storage & Traffic Quotas** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-30 | Traffic recording (fire-and-forget), quota enforcement (free=hard block, paid=soft warning), storage counters (4 scopes), statistics API, plan management API. All upload/download paths instrumented. Library soft-delete adjusts storage counters. Frontend quota-entry migration to GB is mostly done, but UI unit consistency is still pending because some screens appear decimal (`1000^3`) while backend/utilities are binary-byte based (`1024^3`). See [QUOTAS-AND-TRAFFIC-PLAN.md](QUOTAS-AND-TRAFFIC-PLAN.md) |
| **Audit Logs** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-26 | `audit_log` table with 365-day TTL exists for deletion events (GC, groups, departments), and `users.last_login_at` now covers latest successful login. Historical login logs and file-operation/file-access event tables are still missing, so login audit pages and file statistics pages remain pending. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 3 |
| **File/Folder Trash** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-11 | List, restore, clean trash + browse deleted folders. Enhanced: filters out children of deleted directories (2026-03-11) |
| **Library Recycle Bin** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-03-25 | Soft-delete, restore, permanent delete. User + admin endpoints. Storage counters adjusted on delete/restore (2026-03-25) |
| **File Expiry Countdown** | Partial | Needs semantic alignment | ❌ No | 2026-05-15 | `expires_at` is emitted for auto-delete libraries, but current GC behavior does not delete HEAD-visible files by `mtime`. See `docs/TECHNICAL-DEBT.md` section 18. |
| **Admin Library Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 12 endpoints in admin.go + seafile-api.js methods + trash libraries |
| **Admin Link Management** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-12 | 13 endpoints: share link admin (list/delete), upload links (user CRUD + admin), per-user links. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 2 |
| **Audit Logs** | 🟡 PARTIAL | Mostly stable | ❌ No | 2026-03-26 | `audit_log` table with 365-day TTL exists for deletion events (GC, groups, departments), and `users.last_login_at` now covers latest successful login. Historical login logs and file-operation/file-access event tables are still missing, so login audit pages and file statistics pages remain pending. See [ADMIN-FEATURES.md](ADMIN-FEATURES.md) § 3 |
| **Version History UI** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-02-26 | Detail sidebar History tab + full-page view + revert conflict dialog (Replace/Keep Both/Cancel) + View action + proper user name resolution. 17 integration tests. |
| **File Preview & Raw Serving** | ✅ COMPLETE | Mostly stable | ❌ No | 2026-04-06 | Inline preview for PDF/images/video/audio/text, OnlyOffice for docs. Auth token handling fixed for search results. Region-pinned library coverage now includes raw file, historic raw/download, and share-link raw readers. |
| **Region-Aware Library Storage Selection** | ✅ COMPLETE | Mostly stable | ⚠️ Partial | 2026-04-09 | Safe slice complete: bootstrap exposes region options, personal/group/admin create flows persist `storage_class`, sys-admin and org-admin info pages can manage org `storage_policy`, and new materializations prefer that value. Existing reads, reuse and repairs resolve each block's persisted canonical `blocks.storage_class`; active upload sessions do not snapshot the preference and can mix actual classes. Focused integrations cover user, group, org-admin, and superadmin create flows under `strict` and `flexible` policy modes. Remaining work is explicit migration of existing data, cold-tier primary placement design, and broader multi-region operations. |
| **Monitoring/Health Checks** | 🔒 FROZEN | **STABLE** | ❌ No | 2026-02-04 | Structured logging, `/health`, `/ready`, `/metrics`. 5 unit + 21 integration tests. |
| **Multi-Region Replication** | ❌ TODO | N/A | ❌ No | - | Future feature beyond the current safe slice. Current work prefers a class for new block materializations and keeps historical reads/reuse/repair bound to each block's persisted canonical class; it does not replicate or migrate existing physical data. |

---

## Protocol Endpoints (Seafile Compatibility)

### Sync Protocol (`/seafhttp/`) - Compatibility-Sensitive

Functional coverage exists, but active-active desktop conflict recovery is still an open validation gap. Changes here should preserve Seafile client compatibility and carry focused regression coverage.

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
| `GET/POST /seafhttp/repo/folder-perm` | ✅ COMPLETE | Mostly stable | 2026-07-02 | Folder-level permissions (stub: returns `[]` = full access everywhere; array shape confirmed 2026-07-02 against a real Seafile Pro 11.0.16 server) |
| `POST /seafhttp/repo/locked-files` | ✅ COMPLETE | New | 2026-07-02 | Real lock data from `locked_files` table; authenticated per repo via `download-info` body tokens; `by_me` compares the lock holder to the token user |

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
| `GET/PUT /api2/repos/:id/history-limit/` | Partial | Needs semantic alignment | Persists settings, but `keep_days=0` does not round-trip and history APIs do not enforce the window. See `docs/TECHNICAL-DEBT.md` section 18. |
| `GET/PUT /api/v2.1/repos/:id/auto-delete/` | Partial | Needs semantic alignment | Persists settings, but current GC purges orphan/history fs_objects rather than deleting current stale files. See `docs/TECHNICAL-DEBT.md` section 18. |
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
| `POST /api2/auth-token/` | ✅ COMPLETE | Mostly stable | Login via dev credentials or `email + API key` exchange |
| `GET /api/v2.1/api-keys/` | ✅ COMPLETE | Mostly stable | List authenticated user's API keys |
| `POST /api/v2.1/api-keys/` | ✅ COMPLETE | Mostly stable | Create authenticated user's API key |
| `DELETE /api/v2.1/api-keys/:key_hash/` | ✅ COMPLETE | Mostly stable | Revoke API key and invalidate derived sessions |
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

