# Known Issues - SesameFS

**Last Updated**: 2026-04-03

This document tracks all known bugs, limitations, and issues in SesameFS.

---

## Issue Summary by Priority

### 🔴 Production Blockers (Must Fix Before Deploy)
| Issue | Status | See |
|-------|--------|-----|
| OIDC Authentication | ✅ Complete (Phase 1) | `docs/OIDC.md` |
| Garbage Collection | ✅ Complete | `internal/gc/` — queue, worker, scanner, admin API |
| Monitoring/Health Checks | ✅ Complete | `/health`, `/ready`, `/metrics` + slog logging |
| Sync Protocol Permissions | ✅ Complete (2026-02-11) | All 15 sync endpoints enforce library permissions; `syncAuthMiddleware` hardened |
| Sync Race Condition | ✅ Fixed (2026-02-18) | 7 bugs fixed: CAS HEAD updates, parent-chain validation, empty root handling |
| Secrets/Env Management | ✅ Complete (2026-02-11) | All docker-compose vars from `.env`; no hardcoded credentials; JWT secret externalized |
| **Programmatic Auth (API keys)** | ✅ Fixed (2026-04-03) | User API keys now support desktop client, CLI, and automation auth in OIDC-only prod |

### 🟡 High Priority (Core Feature Gaps)
| Issue | Status | Details |
|-------|--------|---------|
| **Default Library on First Login** | 🟡 Pending | Seafile auto-crea una librería "My Library" al primer login del usuario. Nosotros devolvemos `exists:false` en `GET/POST /api2/default-repo/`. El cliente no bloquea, pero el usuario arranca sin ninguna librería. Ver ISSUE-DEFAULT-REPO-01 abajo. |
| Search File Paths | ✅ Fixed | Full paths now populated during sync and backfill |
| Groups Creation | ✅ Tested | User-facing CRUD + members + group sharing verified (20 integration tests) |
| Departments Support | ✅ Complete | Full CRUD, hierarchy, 29 integration tests |
| API Token Library Access | ✅ Complete | 37 integration tests, full RW/RO enforcement |
| Move/Copy Dialog Tree | ✅ Fixed | `with_parents` param missing in ListDirectoryV21 |
| GC TTL Enforcement | ✅ 3/3 Done | `version_ttl_days` ✅, share link deletion ✅, `auto_delete_days` ✅ |
| Admin Panel | ✅ Working in Docker | `/sys/` route serves sysadmin.html via nginx + Go catch-all |
| Frontend Permission UI | 🟡 ~85% Done | API layer returns real permissions on all directory/file endpoints. **Fixed**: `"owner"` permission now mapped to `"rw"` in API responses (was breaking upload button). **Enhanced (2026-03-11)**: Granular `PermissionFlags` (8 flags) now enforced backend-side via `RequirePermFlag()`. Upload/share link uploaders updated. Remaining: some UI components that conditionally render controls based on flags. |
| Modal Dialogs | ✅ All 122 Fixed | All dialog files use Bootstrap classes |
| Library Settings Backend | ✅ Complete | History, API tokens, auto-delete, transfer |
| **Desktop SSO Browser UX** | ✅ Fixed (2026-03-04) | After browser SSO login for desktop client, now shows confirmation page with auto-close. See ISSUE-SSO-01 below. |
| **Upload "Don't Replace" (Desktop Client)** | 🟡 Pending | Desktop client file browser "No" button (auto-rename) doesn't work. Client uses `update-link` vs `upload-link` to distinguish replace vs no-replace, but both map to same token/handler. Backend autorename infrastructure ready (`autoRenameIfExists`), needs token-level `Replace` flag to distinguish endpoints. Web "Don't replace" also broken (same root cause). See ISSUE-UPLOAD-REPLACE-01 below. |
| **Org Logo Upload** | 🟡 Stub | `UpdateOrgLogo` in org_admin.go accepts the file but does not persist it to storage. Returns a static path from settings. Functional as a route placeholder until an asset storage backend is available. |
| **Login Analytics History** | 🟡 Partial | `last_login` is now real and persisted in `users.last_login_at`, but there is still no historical login event dataset for trend analysis, login audit timelines, or period-based "users who logged in" charts. See ISSUE-LOGIN-ANALYTICS-01 below. |
| **File Statistics Pages Are Still Stubbed** | 🟡 Pending | `/sys/statistics/file/` currently returns all-zero series and `/org/statistics-admin/file/` is still unimplemented. Real data depends on new `file_update_logs` and `file_access_logs` tables, not on `login_logs`. See ISSUE-FILE-STATS-01 below. |
| **Org Admin Statistics Can Leak Platform Scope** | 🔴 Confirmed bug | When org-admin pages are mounted with platform-org context, traffic-based org-admin metrics can resolve to the global aggregate instead of a tenant scope. Affects at least org-admin traffic, org-admin per-user traffic, and org-admin active-users. See ISSUE-ORG-STATS-SCOPE-01 below. |

### 🟡 SeaDrive 3.x Missing Endpoints (Non-fatal, but degrade UX)
| Issue | Status | Notes |
|-------|--------|-------|
| `POST /api2/default-repo/` | ✅ Fixed (2026-02-20) | Seafile client POSTs to create "My Library" when none exists. We only had GET registered → 405. Fixed: POST now stubbed to return `{"exists": false}`. |
| `GET /seafhttp/repo/locked-files` | ❌ 404 | File lock status for virtual drive. SeaDrive logs warn but continues. |
| `GET /seafhttp/repo/:repo_id/jwt-token` | ❌ 404 | Repo-scoped JWT for SeaDrive 3.x access control. Seems non-fatal for basic sync. |
| `GET /seafhttp/accessible-repos/` | ❌ 404 | Repo accessibility check used by SeaDrive virtual drive. Non-fatal. |
| `GET /seafhttp/repo/:repo_id/block-map/:block_id` | ❌ 404 | Block composition map for differential sync. Degrades sync efficiency. |

### 🟡 File Editing UX (Text/Markdown/Code files)
| Issue | Status | Notes |
|-------|--------|-------|
| **In-browser file editing** | ❌ Not Implemented | Clicking text files (.py, .md, .json, etc.) opens a read-only preview modal (`FilePreviewDialog`). No inline editor exists. Seahub original loads a full React editor page with `window.app.pageOptions.canEditFile`. See ISSUE-FILE-EDIT-01 below. |
| **fileview.go lacks editor integration** | ❌ Not Implemented | `/lib/:repo_id/file/*` serves static HTML preview instead of loading the React editor app. Missing: `canEditFile`, `filePerm` in `pageOptions`. OnlyOffice (.docx/.xlsx/.pptx) works if configured. |

### 🟡 Owner Email Shows as UUID Instead of Real Email
| Issue | Status | Details |
|-------|--------|---------|
| **Display fields still hardcoded** | 🟡 Partial fix (2026-02-26) | Library list/detail fixed. File history modifier fixed (2026-02-26) — now resolves user name/email from `users` table. Remaining: file detail, starred files, sync token responses still return `UUID@sesamefs.local`. Safe to fix — display only. See ISSUE-EMAIL-01 below. |
| **FS object modifier hardcoded** | 🔴 Risky — needs migration analysis | `seafhttp.go` and `onlyoffice.go` write `UUID@sesamefs.local` into stored FS object modifier field, which is part of the `fs_id` hash. Changing breaks hash of existing stored objects. See ISSUE-EMAIL-01 below. |

### 🟡 Frontend Pending — New Backend Features (2026-03-18)
| Issue | Status | Details |
|-------|--------|---------|
| **Superadmin: Org Soft-Delete/Restore UI** | 🟡 Backend ready, frontend TODO | Backend has `POST /admin/organizations/:org_id/delete/` and `POST .../restore/`. Frontend superadmin dashboard needs: "Delete" button (distinct from "Deactivate"), "Restore" button for deleted orgs, status column showing active/deactivated/deleted with grace period countdown. See ISSUE-FRONTEND-ORG-DELETE-01 below. |
| **Superadmin: Deleted Orgs List/Filter** | 🟡 Backend ready, frontend TODO | `ListOrganizations` should filter/tab by status (active, deactivated, deleted). Currently shows all orgs with no status differentiation. |
| **Org Admin: Org Deletion Awareness** | 🟡 Backend ready, frontend TODO | Org admin dashboard should show a banner/warning if their org is in "deleted" state with days remaining before permanent cascade. |

### 🟢 Lower Priority (Polish/UX)
| Issue | Status | Notes |
|-------|--------|-------|
| Activities Feed + Audit Logs | 🔴 Stub only — prioritize soon | Returns empty `{events:[]}`. Needs 5 DB tables, ~15 handler integrations. See ADMIN-FEATURES.md § 3 |
| Published Libraries (Wikis) | ❌ Hidden + Stub | Nav hidden, `/api/v2.1/wikis/` returns `[]`. Needs wiki/publish backend |
| Linked Devices | ❌ Hidden + Stub | Nav hidden, `/api2/devices/` returns `[]`. Needs device tracking on sync |
| **Sysadmin Info Dashboard Has Residual Stubbed Metadata** | 🟡 Partial | `/admin/sysinfo` now returns real storage, file, active-user, and this-month/this-year traffic KPIs. Remaining gaps: device counts are still unavailable and license fields are still stubbed. See ISSUE-SYSINFO-KPI-01 below. |
| Share Admin (Libraries/Folders/Links) | 🟡 Partial | Share link list/create/delete work; admin management + upload links still missing |
| Watch/Unwatch Libraries | ❌ Deferred | Complex notification system needed |
| Thumbnails | ❌ Not Started | Visual polish |
| User Avatars | ❌ Not Started | Visual polish |
| Frontend Test Coverage | 🟡 ~0.6% | 6 test files for 620+ source files |

**For detailed implementation status, see**: `docs/IMPLEMENTATION_STATUS.md`

---

### ISSUE-SYSINFO-KPI-01: Sysadmin Info Dashboard Still Has Residual Stubbed Fields

**Status**: 🟡 Partial fix (2026-03-26)
**Severity**: Low-Medium — core KPIs are now usable; remaining gaps are secondary metadata
**Affected**: `GET /api/v2.1/admin/sysinfo/`, superadmin info dashboard

#### Problem

The core sysadmin KPI issue is mostly resolved. The endpoint now returns real values for:

- `active_users_count`
- `total_files_count`
- `total_storage`
- `traffic_month_total`, `traffic_month_upload`, `traffic_month_download`
- `traffic_year_total`, `traffic_year_upload`, `traffic_year_download`

The remaining gaps are limited to residual stubbed or unavailable fields:

- `total_devices_count` and `current_connected_devices_count` are still unavailable because device tracking is not implemented
- license-related fields are still stubbed

The page is now trustworthy for the main operational KPIs, but it still exposes non-authoritative metadata for licensing and devices.

#### Fixed

- Platform storage now comes from the `storage_counters` `platform` scope
- Platform file count now comes from the same storage snapshot
- Active users now use user lifecycle status instead of duplicating total users
- Sysadmin overview now exposes this-month and this-year traffic KPIs

#### Remaining Work

- Keep device KPIs hidden or explicitly unavailable until device tracking exists
- Either wire real licensing metadata or hide the license block in the frontend until it is authoritative

#### Related Docs

- `docs/DASHBOARD-REDESIGN-PLAN.md`
- `docs/ADMIN-DASHBOARD-WIREFRAMES.md`

---

### ISSUE-LOGIN-ANALYTICS-01: Only Point-in-Time `last_login` Exists

**Status**: 🟡 Partial fix (2026-03-26)
**Severity**: Medium — user detail pages now show a real last login, but audit/reporting remains incomplete
**Affected**: Admin user lists/details, org-admin user lists/details, any future login analytics/reporting feature

#### Problem

SesameFS now persists `users.last_login_at` on successful authentication, so the `last_login` field shown in admin and org-admin responses is no longer stubbed. However, this is only a point-in-time field on the user row.

There is still no historical login events table, which means the system cannot answer questions like:

- how many users logged in during a selected period
- login trends over time
- per-user login history / audit trail
- anomaly detection based on login frequency

#### Fixed

- Successful session creation now updates `users.last_login_at`
- Dev-token login also updates `users.last_login_at`
- Admin and org-admin user responses now serialize the real `last_login`

#### Remaining Work

- Add a dedicated login-events dataset if login audit/history becomes a product requirement
- Keep using traffic-based activity metrics for period charts until real login analytics exist

---

### ISSUE-FILE-STATS-01: File Statistics Screens Have No Real Event Dataset Yet

**Status**: 🟡 Pending (2026-03-26)
**Severity**: Medium — screens exist but do not provide trustworthy operational data
**Affected**: `/sys/statistics/file/`, `/org/statistics-admin/file/`, backend `AdminStatisticFiles`, backend `OrgStatisticFiles`

#### Problem

The file statistics pages are present in the frontend, but the backend does not yet have a real historical event source for file operations.

Current behavior:

- sysadmin `GET /api/v2.1/admin/statistics/file-operations/` returns a date range filled with zeros
- org-admin `GET /api/v2.1/org/:org_id/admin/statistics/file-operations/` is still not implemented

These screens cannot be fixed with `users.last_login_at` or a future `login_logs` table because they are about file operations, not authentication.

#### Required Data Sources

To make these screens real, SesameFS needs file-event datasets such as:

- `file_update_logs` for `added`, `modified`, `deleted`
- `file_access_logs` for `visited`

An optional `activities` table can support dashboard feeds and broader event browsing, but the file-statistics charts specifically depend on file event history.

#### Remaining Work

- Add immutable file event tables (`file_update_logs`, `file_access_logs`)
- Write file operation events from upload/create/edit/delete/move/rename/download/preview handlers
- Implement real aggregation in `AdminStatisticFiles`
- Implement `OrgStatisticFiles` using the same event source scoped by org
- Keep the UI treated as pending/stubbed until those tables exist

#### Related Docs

- `docs/ADMIN-FEATURES.md`
- `docs/CURRENT_WORK.md`

---

### ISSUE-ORG-STATS-SCOPE-01: Org-Admin Statistics Can Resolve to Platform-Wide Aggregates

**Status**: 🔴 Confirmed bug (2026-03-26)
**Severity**: High — tenant-scoped admin views can show platform-wide data under the wrong context
**Affected**: `/org/statistics-admin/traffic/`, org-admin user traffic table, `/org/statistics-admin/active-users/`

#### Problem

The org-admin frontend reads its `orgID` from the org-admin SPA shell injection (`window.org.pageOptions.orgID`). When that shell is served in platform-org context, the injected `orgID` becomes the platform UUID (`00000000-0000-0000-0000-000000000000`).

For traffic-based metrics, that UUID is not just another org value: it is also the sentinel used by backend helpers for platform-wide aggregate partitions.

As a result, some org-admin statistics can collapse to platform-wide values instead of tenant-scoped values.

#### Confirmed Affected Metrics

- org-admin traffic time series
- org-admin per-user traffic table
- org-admin active-users time series

#### Not Confirmed With The Same Failure Mode

- org-admin storage time series appears to remain tenant-scoped because it uses `org:<orgID>` storage scopes rather than the traffic aggregate sentinel
- org-admin file statistics are still pending/unimplemented, so they are not currently part of this leak

#### Root Cause

- `serveOrgAdminPanel` injects org-admin context from the authenticated user session
- platform org uses the nil UUID / all-zero UUID
- traffic/active-user statistics helpers treat that same UUID as the platform-wide aggregate partition

This is a scope-boundary bug, not a traffic math bug.

#### Remaining Work

- Decide the product rule for platform users opening `/org/...`
- Ensure org-admin routes never execute against the platform aggregate scope by accident
- Add regression tests for org-admin statistics with platform-org vs tenant-org contexts

#### Related Docs

- `docs/CURRENT_WORK.md`
- `docs/IMPLEMENTATION_STATUS.md`

---

## ✅ Fixed Issues

---

### ISSUE-S3-TRANSPORT-01: All S3 Operations Fail Until Restart — FIXED (2026-03-04)
S3 HTTP connection pool zombie connections blocked all uploads/downloads. Fixed transport settings in `internal/storage/s3.go`. See full details at bottom of this file.

---

### ISSUE-PREINDEX-USERS-01: Pre-Index Users Get "user not found" on Share Operations

**Status**: ✅ Fixed (2026-02-24)
**Severity**: High — sharing with any user created before Session 50 always fails
**Affected**: `POST/PUT/DELETE /api2/repos/:repo_id/dir/shared_items/`

#### Problem
Session 50 added `users_by_email` dual-write for new users, and Session 51 refactored share operations to look up the target user exclusively via `users_by_email`. Users created before Session 50 have no row in that index, so share operations returned `{"failed": [{"email": "...", "error_msg": "user not found"}]}` even though the user existed in the `users` table.

#### Fix
- Added `lookupUserIDByEmail(orgID, email)` helper in `internal/api/v2/file_shares.go`
- Tries `users_by_email` first (fast path)
- Falls back to `users WHERE org_id = ? AND email = ? ALLOW FILTERING` (safe: scoped to org partition)
- Backfills `users_by_email` on fallback success (self-healing)
- All three share operations (Create, Update, Delete) use the helper
- Same fix applied to `AdminHandler.lookupUserByEmail` in `admin.go` with a global scan fallback

---

### ISSUE-PREINDEX-USERS-02: Pre-Index Users Get Duplicate Account on First SSO Login

**Status**: ✅ Fixed (2026-02-24)
**Severity**: High — user loses access to their existing libraries
**Affected**: OIDC login for users created before Session 50 who have never logged in via SSO

#### Problem
The OIDC login flow tries to match the incoming user in this order:
1. `users_by_oidc` (OIDC sub mapping)
2. `users_by_email` (email index)
3. `AutoProvision` → create new user

A user created manually (admin/script) before Session 50 has no `users_by_oidc` entry (never did SSO) and no `users_by_email` entry (pre-index). Both lookups fail, and `AutoProvision` creates a **brand new user** with a different UUID — the original account with all its libraries becomes inaccessible.

#### Fix
Added a third fallback step in `internal/auth/oidc.go` between step 2 and `AutoProvision`:
- Scans `users WHERE email = ? ALLOW FILTERING` (global, but only runs once per user)
- On match: backfills `users_by_email`, creates `users_by_oidc` mapping, updates `users.oidc_sub`, goes to `userReady`
- `AutoProvision` is now only reached for genuinely new users

---

### ISSUE-USERS-BY-EMAIL-01: OIDC and AdminAddOrgUser Missing `users_by_email` Dual-Write

**Status**: ✅ Fixed (2026-02-23)
**Severity**: High — admin operations (delete, get by email) returned 404 for OIDC-provisioned users
**Affected**: `DELETE /admin/users/:email/`, `GET /admin/users/:email/`, any email-based user lookup

#### Problem
OIDC `createUser()` wrote to `users` + `users_by_oidc` but NOT `users_by_email`. `AdminAddOrgUser` also only wrote to `users`. Any admin API that resolved users by email (`lookupUserByEmail` → `users_by_email`) would return "user not found" (404).

#### Fix
- `internal/auth/oidc.go` `createUser()`: Now inserts into `users_by_email` after creating the user
- `internal/api/v2/admin_extra.go` `AdminAddOrgUser`: Now inserts into `users_by_email` after creating the user
- All user creation paths (`CreateOrganization` owner, `AdminCreateUser`, OIDC, `AdminAddOrgUser`, seed) now dual-write to `users_by_email`

---

### ISSUE-ADMIN-USERS-01: Admin User Listing Only Showed Platform-Org Users

**Status**: ✅ Fixed (2026-02-23)
**Severity**: High — superadmin saw no tenant users in admin panel
**Affected**: `GET /admin/users/`, `GET /admin/admins/`, `GET /admin/search-user/`

#### Problem
`ListAllUsers`, `ListAdminUsers`, `SearchUsers` queried `WHERE org_id = ?` using only the caller's org. Superadmin is in platform org (`00000000-...`), so they only saw platform-org users.

#### Fix
All three handlers now check for a real platform superadmin. If so, they iterate over all orgs from the `organizations` table (same pattern as `AdminListAllLibraries`). Tenant admin uses the separate `/org` surface. Results are deduplicated by email.

Also: `ListAdminUsers` response key changed from `"data"` to `"admin_user_list"` (frontend expected `res.data.admin_user_list`), and 13 missing `sysAdmin*` frontend API functions were added to `seafile-api.js`.

---

### ISSUE-SESSION-02: Desktop Client Token Expires After 24h — FIXED

**Status**: ✅ Fixed (2026-03-04)
**Severity**: High — desktop sync clients lose access every 24 hours
**Affected**: Seafile Client, SeaDrive, seaf-cli — any client authenticating via `/api2/auth-token/` or SSO

#### Problem
All sessions (web and desktop client) used the same `session_ttl: 24h`. Seafile desktop clients and SeaDrive do **not** implement token refresh — in the original Seafile server, API tokens from `/api2/auth-token/` are permanent (never expire). With a 24h TTL, sync clients lost access daily and prompted re-login.

#### Fix
Added a separate `api_token_ttl` configuration (default: **180 days**) for desktop/mobile client tokens. Web browser sessions remain at 24h.

- `internal/config/config.go` — new `APITokenTTL` field in `OIDCConfig`
- `internal/auth/session.go` — new `CreateAPITokenSession()` and `CreateSessionWithTTL()` methods; `storeSession()` now derives Cassandra TTL from the actual session duration
- `internal/auth/oidc.go` — SSO flow uses `CreateAPITokenSession()` when return URL is `seafile://` (desktop client)
- Config: `auth.oidc.api_token_ttl` / env `OIDC_API_TOKEN_TTL`

No schema changes — same `sessions` table, different TTL per insert.

---

### ISSUE-SESSION-01: 401 Session Expiry Causes Frontend to Hang in Loading State

**Status**: ✅ Fixed (2026-02-22)
**Severity**: High — users see infinite spinner or misleading "folder does not exist" errors
**Affected**: All authenticated views when session/token expires mid-use

#### Problem
When a session expired, the frontend got stuck in a permanent loading state instead of redirecting to login. Three root causes:

1. **SeafHTTP returned 403 (not 401)** for expired operation tokens in `HandleUpload`, `HandleDownload`, `HandleZipDownload` — preventing the frontend from distinguishing "expired" from "no permission"
2. **`authMiddleware` returned generic `"invalid token"`** for expired sessions — no way for frontend to know the session expired vs an invalid credential
3. **Nested promises without `return`** in `lib-content-view.js` `showFile()` — inner promise rejections were silently lost, so `isFileLoading` was never set to `false`

#### Fix

**Backend** (`internal/api/seafhttp.go`):
- Changed 3 locations from `http.StatusForbidden` → `http.StatusUnauthorized` for expired operation tokens

**Backend** (`internal/api/server.go`):
- `authMiddleware` now detects `"expired"` in the session validation error and returns `401 {"error": "session expired"}` immediately instead of falling through to the generic error

**Frontend** (`frontend/src/utils/seafile-api.js`):
- Added global axios response interceptor that catches all 401 responses, clears `localStorage` token, and redirects to `/login/?expired=1`

**Frontend** (`frontend/src/pages/lib-content-view/lib-content-view.js`):
- Added `return` to nested `.then()` calls so promise rejections propagate to the outer `.catch()` handler

**Frontend** (`frontend/src/utils/utils.js`):
- `getErrorMsg()` now returns `"Session expired. Please log in again."` for 401 responses

**Frontend** (`frontend/src/pages/login/index.js`):
- Login page reads `?expired=1` query param and shows session expired message

---

### ISSUE-LIB-01: 404 When Creating Files in Libraries With Corrupt State

**Status**: ✅ Fixed (2026-02-21)
**Severity**: High — silently broken libraries, file creation completely blocked
**Affected**: Libraries where the initial `commits` INSERT failed at creation time

#### Symptoms

`POST /api/v2.1/repos/<id>/file/` returned 404 with body:
```json
{"error": "fs_object not found: not found"}
```

The library appeared normal (visible in the UI, browsable), but any write operation (create file, create folder) failed.

#### Root Cause

`CreateLibrary` performs 3 sequential writes to Cassandra:
1. `fs_objects` — empty root directory
2. `libraries` + `libraries_by_id` — library metadata (logged batch)
3. `commits` — initial commit pointing to the root fs_object

Step 3 had the error silently swallowed:
```go
if err := ...; err != nil {
    // Non-fatal - library was created   ← error ignored
}
```

If that INSERT failed (Cassandra timeout, transient error), the library row stored a `head_commit_id` pointing to a commit that didn't exist. On file creation:

```
CreateFile → GetRootFSID    → libraries_by_id: found head_commit_id ✓
                            → commits: found row ✓ (or not, also broken)
           → TraverseToPath → GetDirectoryEntries
                            → fs_objects WHERE fs_id = <root> → NOT FOUND → 404
```

In some cases the `commits` row existed (written in a previous retry) but the `fs_objects` row for the root directory was missing.

#### Fix

**`internal/api/v2/fs_helpers.go` — `GetDirectoryEntries`:**
On `gocql.ErrNotFound`, return an empty `[]FSEntry` and log a WARNING instead of propagating the error. The next write operation generates a correct new commit with the proper fs_object, permanently healing the library without manual intervention.

**`internal/api/v2/libraries.go` — `CreateLibrary`:**
The `commits` INSERT failure is now logged as `ERROR` instead of being silently ignored.

#### Recovery

Already-corrupt libraries self-heal on the first successful write operation (create file, create folder) with the new code. No manual DB intervention required.

---

---

### ISSUE-EMAIL-01: Hardcoded `UUID@sesamefs.local` Instead of Real User Email

**Status**: 🟡 Partial fix (2026-02-22)
**Severity**: Medium — incorrect display data exposed to clients; no auth or data integrity risk for display fields
**Tracked in**: `docs/TECHNICAL-DEBT.md` § 7

#### Background

Throughout the codebase, several endpoints were constructing a fake email by concatenating the user's UUID with `@sesamefs.local` (e.g. `a1b2c3d4-...@sesamefs.local`) instead of looking up the real email from the `users` table. This pattern was a dev shortcut that leaked into production paths.

#### Fixed (2026-02-22)

A `resolveOwnerEmail(orgID, userID string) string` helper was added to `LibraryHandler`. It queries `SELECT email FROM users WHERE org_id = ? AND user_id = ?` and falls back to `UUID@sesamefs.local` only when the user record is genuinely not found (deleted user, migration gap).

| File | Endpoints fixed |
|------|----------------|
| `internal/api/v2/libraries.go` | `ListLibraries`, `GetLibraryDetail` (v2), `ListLibrariesV21`, `GetLibraryDetailV21`, `CreateLibrary` |
| `internal/api/v2/deleted_libraries.go` | `ListDeletedRepos` |

#### Fixed — File History Modifier (2026-02-26)

`GetFileRevisions` and `GetFileHistoryV21` now resolve user name and email from the `users` table instead of using the raw UUID. A per-request cache avoids repeated queries for the same user across history entries.

| File | Line(s) | Endpoint / Context |
|------|---------|-------------------|
| `internal/api/v2/files.go` | ~3336 | `GetFileRevisions` — `CreatorName`, `CreatorEmail` |
| `internal/api/v2/files.go` | ~3421 | `GetFileHistoryV21` — `CreatorName`, `CreatorEmail` with userCache |

#### Pending — Display Fields (Safe to Fix)

These affect only what is returned to the client. No stored data is involved.

| File | Line(s) | Endpoint / Context |
|------|---------|-------------------|
| `internal/api/v2/files.go` | 1493 | `GetFileDetail` — `userEmail` in file detail response |
| `internal/api/v2/files.go` | 2557 | Sync token response — `"email"` field |
| `internal/api/seafhttp.go` | 1860 | Download-info sync token response — `"email"` field |
| `internal/api/v2/starred.go` | 127, 258 | Starred files list — `userEmail` in response |

Fix strategy: use `h.resolveOwnerEmail(orgID, userID)` (or equivalent DB query) in each location. `starred.go` and `files.go` will need a similar helper added to their respective handler structs, or access via a shared utility function.

#### Pending — FS Object Modifier (Risky — Needs Migration Analysis)

These write `UUID@sesamefs.local` into the **content** of stored Seafile FS objects. The `modifier` field is included in the hash that produces the `fs_id`. Changing the value changes the hash, so:

- Existing stored objects are unaffected (content-addressed, immutable).
- New objects would get different `fs_id` values than they would have with the old code.
- This is safe for **new** uploads but does **not** retroactively fix existing file history.

| File | Line(s) | Context |
|------|---------|---------|
| `internal/api/seafhttp.go` | 1001, 1036, 1098 | `"modifier"` field in FS objects built during upload |
| `internal/api/v2/onlyoffice.go` | 716, 730 | `Modifier` field in FS objects — code comment explicitly notes it's part of the `fs_id` hash |
| `internal/api/sync.go` | 500 | `commit.CreatorName` written into Seafile commit binary format |

Do **not** change these without a deliberate decision on whether to accept the hash change for new objects and whether any tooling needs to account for the mixed state.

---

## ⚠️ SeaDrive 3.x Missing Endpoints (Discovered 2026-02-19)

Observed in SeaDrive 3.0.19 client logs after successful SSO login and basic sync. All 4 are currently returning 404. Sync works despite these errors — they degrade UX or efficiency but are non-fatal.

---

### ISSUE-SD-01: `GET /seafhttp/repo/locked-files` — File Lock Status

**Observed**: SeaDrive logs `Bad response code for GET .../seafhttp/repo/locked-files: 404`
**When**: Immediately after repo trees are loaded, before first sync cycle
**What Seafile does**: Returns the list of files currently locked by any user across the repo. Used by SeaDrive to show lock indicators (padlock icon) on files being actively edited by someone else.
**Expected response format**:
```json
{"locked_files": [{"repo_id": "...", "path": "/filename.docx", "locked_by": "user@example.com", "lock_time": 1234567890}]}
```
**Stub response** (safe to return now): `{"locked_files": []}` — empty list means no files are locked.
**Auth**: No auth (SeaDrive sends without token, same pattern as folder-perm)
**Query params**: `repo_id` (optional, may be for a specific repo)
**Priority**: 🟡 Medium — needed for collaborative editing UX, lockout indicators, OnlyOffice/Office integration

---

### ISSUE-SD-02: `GET /seafhttp/repo/:repo_id/jwt-token` — Notification Server JWT

**Observed**: Seafile desktop client and SeaDrive log `Bad response code for GET .../seafhttp/repo/c430749e-.../jwt-token: 404`
**When**: During repo initialization cycle, after `locked-files` check

**What Seafile actually does** (confirmed from [fileserver/sync_api.go](https://github.com/haiwen/seafile-server/blob/master/fileserver/sync_api.go)):
```go
func getJWTTokenCB(rsp http.ResponseWriter, r *http.Request) *appError {
    if !option.EnableNotification {
        return &appError{nil, "", http.StatusNotFound}  // 404 if notifications disabled
    }
    exp := time.Now().Add(time.Hour * 72).Unix()
    tokenString, err := utils.GenNotifJWTToken(repoID, user, exp)
    // ...
    data := fmt.Sprintf("{\"jwt_token\":\"%s\"}", tokenString)
}
```

**Key findings**:
- **Purpose**: JWT for the **notification server** (WebSocket real-time push), NOT for sync auth or relay switching
- **Response field is `jwt_token`** (not `token`) — `{"jwt_token": "<signed-jwt>"}`
- **Official Seafile also returns 404** when `EnableNotification = false` — our 404 is correct behavior
- **Does NOT affect relay_addr or sync mode** — the `localhost:3000/protocol-version` attempts in logs are **unrelated** to this 404; they come from the client's cached `relay_addr` (stored in `.ccnet/` from when the library was first added)
- **Non-fatal for sync**: files sync correctly without this endpoint; only real-time change notifications are missing

**Expected response format** (when implemented):
```json
{"jwt_token": "<HS256-signed-jwt>"}
```
JWT payload: `{"repo_id": "...", "user": "user@example.com", "exp": <unix+72h>}`

**Auth**: Requires `syncAuthMiddleware` (repo sync token in `Seafile-Repo-Token` header)
**Priority**: 🟢 Low — 404 is safe; only needed to enable real-time file change notifications via notification server

---

### ISSUE-SD-03: `GET /seafhttp/accessible-repos/` — Repo Accessibility Check

**Observed**: SeaDrive logs `Bad response code for GET .../seafhttp/accessible-repos/?repo_id=c430749e-...: 404`
**When**: ~10 seconds after initial sync completes (periodic check)
**What Seafile does**: Verifies that the user still has access to the specified repo. Used by SeaDrive to detect permission revocations without waiting for the next full sync cycle. If a repo is removed from the response, SeaDrive un-mounts it from the virtual drive.
**Expected response format**:
```json
{"accessible_repos": ["c430749e-61b9-45fc-a2fc-0e2e13134b34"]}
```
**Stub response** (safe): Return all repo IDs from the query as accessible — `{"accessible_repos": [repo_id]}`.
**Auth**: Likely requires API token (regular `authMiddleware`)
**Query params**: `repo_id` (comma-separated list of repo UUIDs to check)
**Priority**: 🟢 Low — non-fatal; SeaDrive continues syncing. Only affects permission-revocation detection latency.

---

### ISSUE-SD-04: `GET /seafhttp/repo/:repo_id/block-map/:block_id` — Block Composition Map

**Observed**: SeaDrive logs `Bad response code for GET .../seafhttp/repo/.../block-map/119cdbf0...: 404` then `Failed to get block map for file object 119cdbf0...`
**When**: During file download/sync, when SeaDrive tries to fetch a specific file object
**What Seafile does**: Returns the ordered list of block IDs that compose a file object (identified by its fs_object ID / SHA-1). Enables **differential sync** — instead of re-downloading an entire file, SeaDrive only downloads blocks that changed. This is the core of Seafile's deduplication and efficient sync.
**Expected response format**: JSON array of block IDs in order:
```json
["block-id-1-hex", "block-id-2-hex", "block-id-3-hex"]
```
**Implementation notes**:
- `block_id` in the URL is the **fs_object ID** (file's SHA-1 in the FS tree), NOT a block ID
- Need to look up the fs_object in Cassandra → get its `block_ids` array → return it
- The fs_object stores `block_ids` as an ordered list already (used in `GetBlock`)
- This is already partially implemented in `GetFSObject` — just needs a dedicated endpoint
**Auth**: Requires `syncAuthMiddleware` (sync token in `Seafile-Repo-Token` header)
**Priority**: 🟠 Medium-High — without this, SeaDrive falls back to full-file downloads instead of block-level differential sync. Impacts bandwidth and sync speed for large files.

---

## ✅ RECENTLY FIXED (2026-02-20)

### Desktop Client File Browser Broken — Missing `oid` Response Header — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: Seafile desktop client 9.0.x file browser ("Navegador de Archivos") showed "Fallo al obtener información de archivos / Por favor reintentar" when clicking into any library. Server logs showed two rapid identical `GET /api2/repos/:id/dir/?p=/` requests returning 200 with correct JSON body (271 bytes).

**Root Cause**: The Seafile Qt client reads `reply.rawHeader("oid")` and `reply.rawHeader("dir_perm")` from the directory listing response. Our `ListDirectory` handler returned the correct JSON array but did not set these headers. Without `oid`, the client considers the response invalid and shows the error.

**Fix**: Added `c.Header("oid", currentFSID)` and `c.Header("dir_perm", "rw")` to all success paths in `ListDirectory` (`internal/api/v2/files.go`).

### Desktop Client Upload/Download Fails — "Protocol ttps/ttp is unknown" — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: File upload and download from the desktop client file browser failed. Client logs:
```
[file server task] network error: Protocol "ttps" is unknown   (production, https)
[file server task] error: Protocol "ttp" is unknown             (local dev, http)
```
Server logs showed `GET /api2/repos/:id/upload-link` and `GET /api2/repos/:id/file/?p=...&reuse=1` returning 200 but no subsequent upload/download POST.

**Root Cause**: Three functions returned URLs via `c.String()` (plain text): `GetUploadLink`, `GetDownloadLink`, and `getFileDownloadURL`. The Seafile Qt client expects the URL as a **JSON-quoted string** (e.g., `"https://..."`) and calls `response.mid(1, response.size()-2)` to strip the surrounding quotes. Without quotes, the client stripped the first character (`h`) → `ttps://` or `ttp://` → unknown protocol error.

**Fix**: Changed `c.String(http.StatusOK, url)` → `c.JSON(http.StatusOK, url)` in all three functions. `c.JSON` automatically serializes the string with JSON double quotes.

**Files**: `internal/api/v2/files.go`

### `head-commits-multi` Trailing Slash 502 — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: Client log: `Bad response code for POST https://sfs.nihaoshares.com/seafhttp/repo/head-commits-multi/: 502`. Server log showed the endpoint working for requests without trailing slash, but the client sends the URL WITH trailing slash.

**Root Cause**: Only `POST /seafhttp/repo/head-commits-multi` was registered (no trailing slash). With `router.RedirectTrailingSlash = false`, the trailing-slash variant returned 404, which nginx proxied as 502.

**Fix**: Added `router.POST("/seafhttp/repo/head-commits-multi/", h.GetHeadCommitsMulti)` in `internal/api/sync.go`.

---

### `relay_addr` / `relay_id` Returns `"localhost"` — Seafile Client Tries Wrong Server — FIXED ✅

**Fixed**: 2026-02-20
**Observed**: After syncing, the Seafile desktop client (SeaDrive 3.x and SeafDrive) connects to `localhost:3000` instead of the real server hostname. Client logs:
```
libcurl failed to GET http://localhost:3000/seafhttp/protocol-version: Couldn't connect to server.
libcurl failed to GET http://localhost:8082/protocol-version: Couldn't connect to server.
```
**Preceded by**: 404s for `/seafhttp/repo/locked-files` and `/seafhttp/repo/:id/jwt-token` — these are unrelated to the localhost issue. The `jwt-token` 404 is expected (it's for the notification server, not relay auth — official Seafile also returns 404 when notifications are disabled). The `localhost` attempts come from the client's cached `relay_addr`, not from these 404s.

**Root Causes** (4 bugs):

1. **`docker-compose.yaml` — default `SERVER_URL=http://localhost:3000`** (deployment bug):
   The dev docker-compose had `SERVER_URL=${SERVER_URL:-http://localhost:3000}`. When `SERVER_URL` was not set in `.env`, the container received `SERVER_URL=http://localhost:3000`. Since this env var is non-empty, `getEffectiveHostname()` processed it and extracted `relay_addr=localhost`. Fixed by changing to `SERVER_URL=${SERVER_URL}` (no fallback), so the container gets an empty var and auto-detection works via `c.Request.Host`. Production `docker-compose.prod.yml` was already correct (`SERVER_URL=https://${DOMAIN}`).

2. **`v2/libraries.go` — hardcoded `"localhost"`** (most impactful):
   `CreateLibrary` (POST /api2/repos/) returned `"relay_addr": "localhost"` and `"relay_id": "localhost"` unconditionally. The Seafile client **caches** this value when a library is first added. All subsequent sync operations targeting that library use the cached address — which was `localhost`. Even after restarting or re-logging, the client retries `localhost` until the library is removed and re-added.

2. **`sync.go` `GetDownloadInfo` — ignored `X-Forwarded-Host`**:
   Used `normalizeHostname(c.Request.Host)` directly. Behind a reverse proxy that terminates SSL, `c.Request.Host` is the internal backend address (`localhost:3000`), not the external hostname.

3. **`v2/files.go` `GetDownloadInfo` — ignored `X-Forwarded-Host`**:
   Same issue as #2 in the v2 API path's download-info response.

**Also fixed**: `getBaseURLFromRequest` (used for `file_server_root` in server-info) had the same `X-Forwarded-Host` gap.

**Fix**: All four locations now use this priority order:
1. `SERVER_URL` env var (most reliable — explicitly configured)
2. `X-Forwarded-Host` header (set by nginx/traefik when proxying)
3. `c.Request.Host` (last resort — correct for direct connections)

Added `getEffectiveHostname(c *gin.Context) string` helper in `server.go` for the `api` package; inline equivalent logic added to `v2/libraries.go` and `v2/files.go` (separate package).

**Action required after deploy**: Users whose clients have `localhost` cached must remove and re-add the affected library in SeaDrive/SeafDrive to pick up the correct `relay_addr`. The library data itself is not affected — only the client's cached server address.

**Files**: `internal/api/server.go`, `internal/api/sync.go`, `internal/api/v2/libraries.go`, `internal/api/v2/files.go`

---

## ✅ RECENTLY FIXED (2026-02-19)

### SeaDrive Sync 405/401 on `/seafhttp/repo/folder-perm` — FIXED ✅
**Fixed**: 2026-02-19
**Was**: SeaDrive stuck in `error: 'Error occurred in download.'` loop. Server returned 405 then 401 on `POST /seafhttp/repo/folder-perm`.
**Root Causes** (3 sequential bugs):
1. Previous commit replaced static `router.GET("/seafhttp/repo/folder-perm")` with `repo.GET("")` inside the wildcard group — Gin returned 405 for both GET and POST.
2. After fixing routing, POST still returned 405 because only GET was registered.
3. After adding POST, both returned 401 because SeaDrive sends folder-perm requests with NO auth token.
**Fix**: Register both GET and POST as static routes (no auth middleware) before the wildcard group. Response is always `{}` so no auth is needed.
**Files**: `internal/api/sync.go`

---

## ✅ RECENTLY FIXED (2026-02-18)

### Production File Upload 500 — Storage Backend Not Registered — FIXED ✅
**Fixed**: 2026-02-18
**Was**: All file uploads in production returned HTTP 500 after successful streaming. Server log: `Finalization failed: block store not available: no healthy backend available for class hot`.
**Root Cause**: `initStorageManager` only iterated `cfg.Storage.Classes` (new multi-region format). `config.prod.yaml` uses the legacy `backends:` key — so the storage manager started with zero backends. `finalizeUploadStreaming` called `storageManager.GetHealthyBlockStore("")` → resolved default class `"hot"` → not found → 500.
**Fix**: Added a second loop in `initStorageManager` that also registers backends from `cfg.Storage.Backends` (legacy format), skipping any name already registered via `classes:`. Both formats produce identical entries in the manager.
**Files**: `internal/api/server.go`, `config.prod.yaml` (comment only)

---

### Desktop Sync Race Condition — Web-Uploaded Files Disappear — FIXED ✅
**Fixed**: 2026-02-18
**Was**: When the Seafile desktop client deleted all local files and re-synced, it overwrote the server HEAD with an empty-root commit, causing files uploaded via the web UI to disappear. The desktop client then entered an infinite sync retry loop every ~30 seconds.

**Root Cause**: Seven interrelated bugs across the sync protocol, upload pipeline, and directory listing:

**Bug 1 — PutCommit race condition (4 sub-fixes)**:
- **1A**: The non-HEAD `PUT /commit/:id` path was unconditionally updating HEAD, bypassing the Seafile protocol's separate HEAD update step (`PUT /commit/HEAD` or `POST /update-branch`). A stale/retried commit from the desktop client could silently overwrite a HEAD that had been advanced by web uploads.
- **1B**: `PUT /commit/HEAD` had no parent-chain validation. Any commit could replace HEAD regardless of whether it was a descendant of the current HEAD.
- **1C**: `POST /update-branch` had the same missing parent-chain validation as 1B.
- **1D**: `updateLibraryHeadWithStats()` used an unconditional batch write. Two concurrent callers could both read the same HEAD and then both write, with the last writer winning silently.

**Bug 2 — HandleUpload swallows errors**:
- Single-shot upload (`HandleUpload`) logged filesystem metadata failures but returned 200 OK to the client, masking data inconsistencies.
- Streaming upload (`finalizeUploadStreaming`) swallowed errors similarly.

**Bug 3 — ListDirectory returns empty on errors**:
- When the commit lookup or root fs_object lookup failed, `ListDirectory` and `ListDirectoryV21` returned HTTP 200 with an empty dirent list instead of an error. This made the desktop client believe the library was empty and sync a deletion.

**Bug 4 — CheckFS reports EMPTY_SHA1 as missing (infinite sync loop)**:
- The all-zeros ID (`0000000000000000000000000000000000000000`) is Seafile's canonical constant for an empty directory root. The desktop client treats it as a well-known value and never uploads it via `recv-fs`. When `CheckFS` reported it as missing, the client waited and retried every ~30 seconds indefinitely.

**Bug 5 — GetHeadCommitsMulti returns "not found" for valid repos**:
- The `libraries` table partitions by `(org_id)`. When the sync auth token carried a different `org_id` than the library's actual partition, the query returned no rows. This is the same class of issue documented elsewhere in the codebase (partition key mismatch), solved by falling back to `libraries_by_id WHERE library_id = ?`.

**Bug 6 — ListDirectory 500 on all-zeros root**:
- After the desktop client legitimately synced an empty library (all files deleted), the commit's `root_fs_id` was `0000...0`. `ListDirectory` tried to find this fs_object in the database, failed, and returned 500 Internal Server Error.

**Bug 7 — createInitialCommit uses hardcoded all-zeros instead of proper SHA-1**:
- `createInitialCommit()` in sync.go used `fmt.Sprintf("%040x", 0)` to generate the root fs_id. The v2 REST API in `libraries.go` uses proper content-addressable hashing: `sha1.Sum([]byte("1\n[]"))`. The hardcoded zeros caused special-casing throughout the codebase because the all-zeros ID doesn't exist as a real `fs_object`.

**Fixes Applied**:

1. **Bug 1A**: Removed HEAD update from non-HEAD PutCommit. The commit is stored but HEAD is only advanced by the dedicated `PUT /commit/HEAD` or `POST /update-branch` endpoints.
2. **Bug 1B/1C**: Added parent-chain validation to both `PUT /commit/HEAD` and `POST /update-branch`. Before updating HEAD, the commit's `parent_id` must match the current HEAD. If not, the update is rejected (returns 200 OK for Seafile desktop client compatibility — the client detects HEAD did not advance on next sync check).
3. **Bug 1D**: Added Cassandra LWT (Lightweight Transaction / compare-and-swap) support to `updateLibraryHeadWithStats()`. New optional `expectedHead` parameter enables `IF head_commit_id = ?` in the UPDATE statement. Returns `ErrHeadConflict` sentinel error if another writer changed HEAD concurrently.
4. **Bug 2A/2B**: `HandleUpload` and `finalizeUploadStreaming` now return proper HTTP errors when filesystem metadata updates fail instead of silently succeeding.
5. **Bug 3**: `ListDirectory` and `ListDirectoryV21` now return HTTP 500 with descriptive error messages when commit or fs_object lookups fail, instead of returning empty arrays.
6. **Bug 4**: `CheckFS` skips the all-zeros ID (`strings.Repeat("0", 40)`) before querying the database, breaking the infinite sync loop.
7. **Bug 5**: `GetHeadCommitsMulti` falls back to `libraries_by_id WHERE library_id = ?` when the primary `libraries WHERE org_id = ? AND library_id = ?` query fails.
8. **Bug 6**: `ListDirectory` and `ListDirectoryV21` treat the all-zeros root as a valid empty library — returns empty dirent list for root path `/`, returns 404 for subdirectories.
9. **Bug 7**: `createInitialCommit()` now computes the root fs_id as `sha1.Sum([]byte("1\n[]"))` (matching the v2 REST API in `libraries.go`) and stores a real `fs_object` with that ID. All-zeros checks are kept as defense-in-depth since existing libraries or desktop clients may still reference the old format.

**Files Changed**:
- `internal/api/sync.go` — Bugs 1A-1D, 4, 5, 7: PutCommit HEAD separation, parent-chain validation, CAS updates, CheckFS EMPTY_SHA1 skip, GetHeadCommitsMulti fallback, createInitialCommit SHA-1 alignment
- `internal/api/seafhttp.go` — Bug 2A/2B: HandleUpload and finalizeUploadStreaming error propagation
- `internal/api/v2/files.go` — Bugs 3, 6: ListDirectory/ListDirectoryV21 error handling and empty-root handling

---

## ✅ RECENTLY FIXED (2026-02-12)

### Files Opened from Search Return 404/500 — FIXED ✅
**Fixed**: 2026-02-12
**Was**: Clicking search results to open files (especially .docx and .pdf) returned either 404 "File Not Found" or 500 Internal Server Error.

**Root Causes** (3 separate issues):

1. **404 on .docx files (OnlyOffice)**: `getFileID()` in `onlyoffice.go` queried the `libraries` table with `WHERE org_id = ? AND library_id = ?`. When `org_id` from the auth context didn't match the library's partition key, Cassandra returned no rows → 404 error page.

2. **500 on .pdf files (inline preview)**: `serveInlinePreview()` in `fileview.go` extracted the auth token from query params or Authorization header to build the raw file embed URL. When users arrived without a token (anonymous/dev mode), it generated `?token=` (empty string) in the `<embed src="/repo/:id/raw/:path?token=">` URL → the browser's sub-request to the raw endpoint failed with 500.

3. **Missing token in URLs**: All 6 `onSearchedClick()` handlers across the frontend (app.js, settings.js, repo-history.js, repo-snapshot.js, repo-folder-trash.js, pages/search/index.js) opened files in new tabs via `window.open()` **without** including the auth token in the URL. New browser tabs don't have access to the parent's `localStorage` or ability to set request headers → unauthenticated requests.

**Fixes**:
- **Backend (OnlyOffice)**: Changed `getFileID()` to query `libraries_by_id WHERE library_id = ?` (no `org_id` dependency), matching the pattern used by `FSHelper.GetRootFSID()`.
- **Backend (Preview)**: Enhanced token extraction in `serveInlinePreview()` to support both `Token` and `Bearer` prefixes, added fallback to dev token when in dev mode and token is empty.
- **Frontend**: Updated all 6 `onSearchedClick()` handlers to call `getToken()` and append `?token=` to file URLs.

**Files Changed**:
- `internal/api/v2/fileview.go` — Enhanced token extraction with dev token fallback
- `internal/api/v2/onlyoffice.go` — Fixed `getFileID()` to use `libraries_by_id` table
- `frontend/src/app.js` — Added token import and URL parameter
- `frontend/src/settings.js` — Added token to file URLs
- `frontend/src/repo-history.js` — Added token to file URLs
- `frontend/src/repo-snapshot.js` — Added token to file URLs
- `frontend/src/repo-folder-trash.js` — Added token to file URLs
- `frontend/src/pages/search/index.js` — Added token to file URLs

---

## ✅ RECENTLY FIXED (2026-02-06)

### Search File Paths Incorrect — FIXED ✅
**Fixed**: 2026-02-06
**Was**: Files in subdirectories showed wrong path (e.g., `/file.txt` instead of `/folder/file.txt`) → clicking results gave 404.
**Root cause**: `full_path` field was never populated — search only had the filename without parent directory context.
**Fix**:
- Added `full_path` column to `fs_objects` table via database migration
- Added `updateFullPaths()` helper in `internal/api/sync.go` that traverses directory tree from root
- Called async from `PostCommit`, `PutCommit HEAD`, and `UpdateBranch` handlers after commit is received
- Updated `backfill-search-index` CLI command to also populate `full_path` for existing data
- Search handler (`internal/api/v2/search.go`) now returns correct `fullpath` from database
**Files**: `internal/api/sync.go`, `internal/api/v2/search.go`, `cmd/sesamefs/main.go`, `internal/db/db.go`

### Search Returns No Results — FIXED ✅
**Fixed**: 2026-02-06
**Was**: `GET /api/v2.1/search/?q=test` returned `{"results":null,"total":0}` even when files named "test.docx" existed.
**Root cause**: Two issues:
1. `obj_name` field in `fs_objects` table was never populated during sync (empty string "")
2. SASI indexes disabled in Cassandra 5.x, search queries failed silently
**Fix**:
- Modified `internal/api/sync.go` to extract child names from directory `dir_entries` and update child `obj_name`
- Changed `internal/api/v2/search.go` to use in-memory filtering instead of SASI LIKE queries
- Added `backfill-search-index` CLI command to populate `obj_name` for existing data
- Fixed UUID marshaling errors (use strings instead of `uuid.UUID` with gocql)
**Files**: `internal/api/sync.go`, `internal/api/v2/search.go`, `cmd/sesamefs/main.go`, `internal/db/db.go`

## ✅ RECENTLY FIXED (2026-02-05)

### Search Returns 404 — FIXED ✅
**Fixed**: 2026-02-05
**Was**: `GET /api2/search/?q=test&search_repo=all` → 404. Search route only registered under `/api/v2.1/` but `seafile-js` calls `/api2/search/`.
**Fix**: Added `v2.RegisterSearchRoutes(protected, s.db)` to `/api2/` route group.
**File**: `internal/api/server.go`

### Tag Deletion 500 Error — FIXED ✅
**Fixed**: 2026-02-05
**Was**: `DELETE /api/v2.1/repos/:repo_id/repo-tags/:id/` → 500. Counter table DELETE mixed with non-counter batch.
**Fix**: Separated counter DELETE from LoggedBatch (same pattern as AddFileTag/RemoveFileTag).
**File**: `internal/api/v2/tags.go`

### Tags `#` in URL Causes "Folder Does Not Exist" — FIXED ✅
**Fixed**: 2026-02-05
**Was**: Clicking "Create a new tag" link appended `#` to URL. Reloading showed "Folder does not exist".
**Fix**: Added `e.preventDefault()` to tag link onClick, and strip hash fragments in URL parser.
**Files**: `frontend/src/components/dialog/edit-filetag-dialog.js`, `frontend/src/pages/lib-content-view/lib-content-view.js`

### File/Folder Trash (Recycle Bin) — IMPLEMENTED ✅
**Fixed**: 2026-02-05
**Was**: Trash feature had no backend endpoints. Clicking recycle bin icon failed.
**Fix**: Created `internal/api/v2/trash.go` with 5 endpoints: list trash items (commit-history based), restore file/folder, clean trash, browse deleted folders. Added 5 frontend API methods.
**Files**: `internal/api/v2/trash.go` (new), `frontend/src/utils/seafile-api.js`

### Library Recycle Bin (Soft-Delete) — IMPLEMENTED ✅
**Fixed**: 2026-02-05
**Was**: Deleting a library was permanent with no recovery. Frontend had full UI but backend had no soft-delete.
**Fix**: Added `deleted_at`/`deleted_by` columns to libraries table. `DeleteLibrary` now soft-deletes. Added list/restore/permanent-delete endpoints. Filtered soft-deleted libraries from all list and get endpoints. Added 7 frontend API methods.
**Files**: `internal/api/v2/deleted_libraries.go` (new), `internal/api/v2/libraries.go`, `internal/db/db.go`, `frontend/src/utils/seafile-api.js`

### File Expiry Countdown — IMPLEMENTED ✅
**Fixed**: 2026-02-05
**Was**: No indication of when files expire in libraries with `auto_delete_days`.
**Fix**: Added `expires_at` field to directory listing API response. Computed from `mtime + auto_delete_days * 86400`.
**File**: `internal/api/v2/files.go`

---

## ✅ RECENTLY FIXED (2026-02-04)

### Raw File Preview / Inline Serving 500 Error — FIXED ✅
**Fixed**: 2026-02-04
**Was**: All inline file previews (images, PDFs, documents, shared files) returned 500 Internal Server Error. Error: `Undefined column name size in table sesamefs.fs_objects`
**Root Cause**: `ServeRawFile()` queried `SELECT block_ids, size FROM fs_objects` but the actual column is `size_bytes`.
**Fix**: Changed `size` → `size_bytes` in the query.
**File**: `internal/api/v2/fileview.go:551`

### Image Lightbox aria-hidden on body — FIXED ✅
**Fixed**: 2026-02-04
**Was**: Opening image lightbox set `aria-hidden="true"` on `<body>`, hiding the entire accessibility tree from screen readers. Browser console warning: "Blocked aria-hidden on a `<body>` element."
**Root Cause**: `@seafile/react-image-lightbox` uses `react-modal` internally, which sets `aria-hidden="true"` on body by default when a modal opens.
**Fix**: Added `reactModalProps={{ shouldFocusAfterRender: true, ariaHideApp: false }}` to the Lightbox component to disable the body aria-hidden behavior.
**File**: `frontend/src/components/dialog/image-dialog.js`

### File History Showing Duplicate Entries — FIXED ✅
**Fixed**: 2026-02-04
**Was**: File history page showed duplicate records (e.g., 18 identical entries for a file modified only twice). Same timestamp, same size, same modifier for most entries.
**Root Cause**: `GetFileHistoryV21` iterated all commits for the library and included a history entry for every commit where the file existed — even if the file content was unchanged (e.g., another file in the library was modified).
**Fix**: After collecting all commits containing the file, deduplicate by `RevFileID` (fs_id). Only include an entry when the file's fs_id changes compared to the previous commit, indicating the file was actually modified.
**File**: `internal/api/v2/files.go:3244-3305`

---

## 🔴 OPEN ISSUES

### ISSUE-GC-MULTIINSTANCE-01: GC is not safe with multiple instances

**Status**: 🟡 Pending with temporary prod workaround
**Discovered**: 2026-03-17
**Priority**: 🟡 High — required before scaling to multiple replicas
**Affected**: `internal/gc/worker.go`, `internal/gc/scanner.go`, `internal/gc/gc.go`

**Problem:**
The GC (worker + scanner) has no coordination mechanism between instances. If multiple server replicas are running, all of them execute the worker and scanner in parallel, causing:

1. **DequeueBatch without locking**: `SELECT ... LIMIT ?` returns the same items to all instances. Both process the same items simultaneously.
2. **Scanner without leader election**: Multiple scanners enqueue the same orphans as duplicates (the PK includes `queued_at = time.Now()`, so each INSERT creates a distinct row).
3. **gc_queue_stats counters out of sync**: Duplicate work causes extra counter decrements, skewing admin metrics.

**Is there data loss?** No. Destructive operations are protected:
- `DeleteBlock` uses LWT two-phase (only one instance wins the `IF ref_count <= 0`)
- `MarkItemProcessed` uses `INSERT ... IF NOT EXISTS` to prevent double-decrement of ref_count
- Cassandra DELETEs are idempotent

**Actual impact**: Wasted work (CPU/network overhead) and slightly incorrect admin counters. No risk of data loss.

**Current operational decision (2026-04-04):**
- Production may proceed with a temporary guard: set `GC_ENABLED=true` on exactly one backend replica and `GC_ENABLED=false` on all others.
- This avoids concurrent worker/scanner execution without introducing distributed coordination right now.
- This is an operational workaround only; it does not solve failover or automatic leader transfer.

**Proposed solution — Leader Election via LWT:**
```sql
CREATE TABLE IF NOT EXISTS gc_leader (
    role TEXT PRIMARY KEY,
    instance_id TEXT,
    heartbeat TIMESTAMP
) WITH default_time_to_live = 30;
```
```go
// Only the leader runs worker/scanner. If it dies, the TTL expires and another takes over.
applied, _ := session.Query(`
    INSERT INTO gc_leader (role, instance_id, heartbeat)
    VALUES (?, ?, ?) IF NOT EXISTS
`, role, instanceID, time.Now()).ScanCAS()
```
- Heartbeat renewal every 10s with `UPDATE ... IF instance_id = ?`
- If heartbeat expires (TTL 30s), another instance can take leadership
- Separate roles: `worker` and `scanner` (can run on different instances)

**Recommended future direction:**
- Keep the temporary `GC_ENABLED` guard for short-term production unblock.
- Replace it later with a Cassandra LWT lease so failover is automatic and operators no longer need to pin one GC replica manually.

**Multi-region deployment note (2026-04-10):**
Running GC in a single DC is **critical** for multi-region deployments with Cassandra replication. Even though LWT operations use `SERIAL` consistency (global Paxos) by default, running GC on multiple DCs would cause:
- `DequeueBatch` (non-LWT SELECT) returning the same items to workers in different DCs
- Scanner in both DCs enqueueing duplicate orphans
- Unnecessary cross-DC Paxos contention on every LWT

The existing `GC_ENABLED=true` on exactly one DC / `GC_ENABLED=false` on all others is the correct topology for multi-region. The LWT leader election proposal above still applies — it would provide automatic failover within a single DC or across DCs.

All block-level operations (`IncrementOrCreateBlock`, `decrementBlockRefCount`, `DeleteBlock` Phase 1) use LWT which defaults to `SERIAL` (global Paxos). Do NOT change to `LOCAL_SERIAL` — this would break cross-DC serialization and allow split-brain scenarios where GC in DC-A claims a block that an upload in DC-B is concurrently referencing.

**Alternative — Org partitioning:**
Each instance processes `hash(orgID) % numInstances == myIndex`. No coordination needed but requires knowing the total number of instances.

**Alternative — Accept duplication:**
If only 2-3 instances will run, overhead is minimal and all logic is already idempotent. Counters can be recalculated with a periodic scan.

---

### ISSUE-FILE-EDIT-01: No In-Browser Editing for Text/Markdown/Code Files

**Status**: ❌ Not Implemented
**Discovered**: 2026-02-22
**Priority**: 🟡 High — core UX gap, users expect to edit files by clicking them

**Current Behavior:**
- Clicking a text file (`.py`, `.md`, `.json`, `.txt`, `.css`, `.js`, etc.) opens `FilePreviewDialog` — a read-only modal that renders `<pre><code>` with no edit capability.
- The `isModalPreviewable()` function in `lib-content-view.js:1395` intercepts these file types before they ever reach `fileview.go`.
- For non-intercepted files, `fileview.go` serves backend-rendered preview HTML or OnlyOffice — it does NOT load a React editor/app shell for authenticated file editing.

**Expected Behavior:**
- Clicking a `.md` file should open an editor experience instead of a read-only preview.
- Clicking other text files should open an authenticated editor/view page with `window.app.pageOptions` containing `canEditFile`, `filePerm`, `fileType`, etc.
- The `FileToolbar` component (`frontend/src/components/file-view/file-toolbar.js`) reads `canEditFile` from `pageOptions` to show Save/Edit buttons.

**What Works Today:**
- OnlyOffice editing (`.docx`, `.xlsx`, `.pptx`) works if OnlyOffice is configured — `fileview.go:serveOnlyOfficeEditor()` renders the editor correctly.
- File download works for all types.
- Legacy standalone preview bundles (`frontend/src/file-view.js`, `history-trash-file-view.js`, `view-file-*.js`) were removed because the live backend preview flow no longer loads them.

**Implementation Plan:**
1. **Option A (Quick):** Remove text file types from `isModalPreviewable()` so clicks go to `/lib/:repo_id/file/*`, then update `fileview.go` to serve an authenticated editor shell (with `pageOptions`) instead of static/backend-rendered preview HTML for editable text files.
2. **Option B (Full):** Build an in-app editor component (CodeMirror/Monaco) embedded in the `FilePreviewDialog` modal, with save-back-to-API capability.
3. Either option needs: permission check in `fileview.go` to set `canEditFile` based on `GetLibraryPermission()` result.

**Files Involved:**
- `frontend/src/pages/lib-content-view/lib-content-view.js` — `isModalPreviewable()`, `onItemClick()`
- `frontend/src/components/dialog/file-preview-dialog.js` — read-only preview modal
- `internal/api/v2/fileview.go` — `ViewFile()`, `serveInlinePreview()`
- `frontend/src/components/file-view/file-toolbar.js` — reads `canEditFile` from `pageOptions`
- `frontend/src/pages/markdown-editor/` — existing Markdown editor (separate entry point)

---

### ISSUE-SSO-01: Desktop Client SSO — Browser Shows No Confirmation After Login

**Status**: ✅ Fixed (2026-03-04)
**Discovered**: 2026-02-20
**Severity**: Medium — functional but poor UX; users are confused after completing SSO login

**Fix**: When `result.ReturnURL` starts with `seafile://` (desktop client SSO), `handleOAuthCallback` now serves a lightweight HTML confirmation page instead of redirecting to `/`. The page:
1. Shows "Login Successful — You can close this tab and return to the application."
2. Attempts `window.close()` to auto-close the tab (works when opened via `ShellExecute`/`xdg-open`)
3. Uses `<meta http-equiv="refresh">` to redirect to `seafile://client-login/` as fallback to activate the client

Web browser logins are unaffected — they still redirect to `/`.

**Files changed**: `internal/api/server.go` → `handleOAuthCallback()` (lines 1811–1846)

---

### Programmatic Auth in OIDC-only Production — FIXED ✅
**Status**: ✅ Fixed (2026-04-03)
**Discovered**: 2026-02-18
**Severity**: Resolved for desktop client sync, CLI tools, and user-scoped automation

**Current behavior**:
- Users can create/revoke user API keys via `GET/POST/DELETE /api/v2.1/api-keys/`
- Desktop clients, SeaDrive, and CLI tools call `POST /api2/auth-token/` with `username=<email>` and `password=<raw API key>`
- The server returns a long-lived session token for sync/API access
- Revoking the API key invalidates any sessions minted from that key
- If the API key expires, the derived session cannot outlive the key

**What remains out of scope**:
- OIDC Device Flow is still not implemented
- There is still no separate service-account or client-credentials flow for userless automation

**Relevant files**:
- `internal/api/server.go` — `/api2/auth-token/` API-key exchange + self-service revoke path
- `internal/auth/session.go` — session provenance and invalidation by API key
- `internal/api/v2/admin_api_keys.go` — superadmin API key management for platform users
- `frontend/src/components/user-settings/api-keys.js` — self-service profile UI

---

### `head-commits-multi` Authentication in Production — FIXED ✅
**Status**: ✅ Fixed (2026-02-19)
**Discovered**: 2026-02-17

**Issue**: The Seafile desktop client 9.0.16 (Windows) sends `POST /seafhttp/repo/head-commits-multi` **without any auth headers** — no `Authorization`, no `Seafile-Repo-Token`, nothing. In production with OIDC, this endpoint was returning 401 every ~30s.

**Root cause confirmed**: Inspected official Seafile fileserver source (`fileserver/sync_api.go` v11.0.13). The endpoint is registered with **no auth middleware** and `headCommitsMultiCB` does not call `validateToken()`. Unauthenticated access is intentional — repo UUIDs are unguessable and only commit hashes are returned.

**Fix**: Removed `authMiddleware` from the route registration. Updated `GetHeadCommitsMulti` to handle both authenticated and unauthenticated callers: authenticated requests use org_id partitioned query + ACL check; unauthenticated requests query `libraries_by_id` directly without ACL filtering.

**Files**: `internal/api/sync.go` — `RegisterSyncRoutes()`, `GetHeadCommitsMulti()`

### ISSUE-DEFAULT-REPO-01: No Default Library Created on First Login

**Status**: 🟡 Pending
**Discovered**: 2026-02-20
**Severity**: Medium — funcional pero el usuario arranca sin ninguna librería visible

**Issue**: Seafile crea automáticamente una librería "My Library" (llamada `default_repo`) la primera vez que el usuario hace login. En nuestro sistema, `POST /api2/default-repo/` devuelve `{"exists": false}` como stub y no crea nada. El cliente desktop y la web no bloquean, pero el usuario ve una lista de librerías vacía al conectarse por primera vez.

**Comportamiento Seafile real** (`DefaultRepoView.post()`):
1. Verifica si el usuario ya tiene una `default_repo` en `UserOptions`
2. Si no existe (o fue eliminada), llama a `create_default_library(request)` que crea una librería llamada con el email del usuario
3. Guarda el `repo_id` en `UserOptions` con `KEY_DEFAULT_REPO`
4. Devuelve `{"exists": true, "repo_id": "<uuid>"}`

**Nuestro comportamiento actual**:
- `GET /api2/default-repo/` → `{"exists": false, "repo_id": ""}` (stub)
- `POST /api2/default-repo/` → `{"exists": false, "repo_id": ""}` (stub, añadido 2026-02-20 para evitar 405)
- No se crea ninguna librería; el usuario debe crearla manualmente

**Implementación pendiente**:
1. En el handler `POST /api2/default-repo/`, crear una librería con nombre derivado del email del usuario (ej. `"Mi librería"` o `<username>-files`)
2. Persistir el `repo_id` en una tabla de preferencias de usuario (equivalente a `UserOptions` con `KEY_DEFAULT_REPO`)
3. Devolver `{"exists": true, "repo_id": "<uuid>"}` una vez creada
4. En el handler `GET`, leer esa preferencia y devolver el estado real

**Alternativa más simple**: Crear la librería por defecto directamente en el handler OIDC callback (`handleOAuthCallback`) al primer login del usuario, antes de redirigir. Esto garantiza que la librería existe incluso si el cliente nunca llama al endpoint `POST /api2/default-repo/`.

**Archivos relevantes**:
- `internal/api/server.go` → `handleDefaultRepo()` (línea ~1072)
- `internal/api/v2/libraries.go` → lógica de creación de librerías (referencia para el handler)

---

### Version History — Remaining Gaps (Enhancements)
**Status**: 🟡 Core complete, enhancements pending
**Discovered**: 2026-02-01
**Detail**: File-level version history is fully functional (list, download revision, revert, history limit config, pagination, encryption). Remaining gaps:
1. **Library-wide commit history** — `GET /api/v2.1/repos/:id/history/` endpoint exists and is paginated. ✅ Implemented.
2. **Diff view between versions** — Frontend infrastructure exists but no backend diff endpoint. Seafile uses `/api2/repos/:id/file/diff/`. Needs a text diff algorithm (e.g., unified diff on file content).
3. **History TTL enforcement** — `version_ttl_days` stored in `libraries` table. GC Phase 5 (`scanExpiredVersions`) walks the HEAD commit chain and enqueues expired orphan commits. ✅ Implemented, needs validation.
4. **Directory revert** — `POST /api/v2.1/repos/:id/dir/?operation=revert` exists in code + `revertFolder()` in seafile-js. ✅ Implemented, needs validation.
5. ~~**File revert 409 not handled in UI**~~ — ✅ Fixed (2026-02-26). All 3 file history components now show a conflict dialog (Replace / Keep Both / Cancel) when reverting to a version where the file already exists with different content.
6. ~~**Modifier shows UUID instead of user name**~~ — ✅ Fixed (2026-02-26). `GetFileRevisions` and `GetFileHistoryV21` now resolve creator name/email from the `users` table.
7. ~~**No View action in history**~~ — ✅ Fixed (2026-02-26). All history views now include a "View" option that opens an inline preview page (`/history/view`) with proper MIME-based rendering (images, PDF, text, video, audio). Non-previewable files redirect to download.

### Share Links — Relative URLs + Stub Endpoint — FIXED ✅
**Status**: ✅ Fixed (2026-02-03, Session 26)
**Detail**: Share links showed relative paths (`/d/token`) instead of full copyable URLs. The repo-specific endpoint (`/api/v2.1/repos/:repo_id/share-links/`) was a stub returning empty `[]`, causing the admin share link panel to show no results. Fixed by adding `serverURL` to `ShareLinkHandler`, using `getBrowserURL()` for full URLs, and implementing `ListRepoShareLinks` handler.

### Tagged Files List Shows Deleted Files — FIXED ✅
**Status**: ✅ Fixed (2026-02-12)
**Reported**: 2026-02-03
**Detail**: The tagged files list no longer shows deleted files. `ListTaggedFiles` filters via `TraverseToPath()`. Cascade cleanup (`CleanupFileTagsByPath`) is wired into `DeleteFile`, `DeleteDirectory`, `MoveFile`, and batch delete. Tags are preserved on rename via `MoveFileTagsByPath` (files) and `MoveFileTagsByPrefix` (directories). `PermanentDeleteRepo` now calls `CleanupAllLibraryTags` to remove all tag data when a library is permanently deleted.

### Groups Creation — TESTED ✅
**Status**: ✅ Tested and working (2026-02-10)
**Reported**: 2026-01-31
**Tested**: 2026-02-10
**Detail**: User-facing group CRUD fully tested via `scripts/test-groups.sh` (20 assertions). All operations working: create, list, get, rename, add/remove members, share library to group, delete. Also fixed `ListBeSharedRepos` to resolve group shares (members can now see libraries shared to their groups via `/api2/beshared-repos/`).
**Files**: `internal/api/v2/groups.go`, `internal/api/v2/file_shares.go`, `scripts/test-groups.sh`

### Departments Support — COMPLETE ✅
**Status**: ✅ Complete (2026-01-31)
**Detail**: Full department CRUD implemented — list, create, get (with members/sub-depts/ancestors), update, delete. Hierarchical department system with parent/child relationships. 29 integration tests passing. See `internal/api/v2/departments.go` and `scripts/test-departments.sh`.

### API Token Library Access — COMPLETE ✅
**Status**: ✅ Complete (2026-01-31)
**Detail**: Repo API tokens now work for authentication. Token `b81b9683...` grants RW access to library "test". Implementation: reverse-lookup table `repo_api_tokens_by_token`, auth middleware checks token → resolves repo_id + permission, permission middleware enforces scope. Read-only tokens can list but not write; tokens can only access their designated library.

### GC TTL Enforcement — COMPLETE ✅
**Status**: ✅ 3 of 3 items done
**Reported**: 2026-01-31
**Updated**: 2026-02-04

**1. `auto_delete_days` enforcement** — ✅ DONE (2026-02-04)
- Scanner Phase 6 (`scanAutoDeleteExpiredObjects`) walks HEAD + recent commit trees, enqueues orphaned fs_objects
- 5 unit tests (basic, preserves HEAD tree, preserves recent commits, skips zero, nested dirs)

**2. `version_ttl_days` enforcement** — ✅ DONE (2026-02-02)
- Scanner Phase 5 (`scanExpiredVersions`) walks HEAD commit chain, enqueues expired non-HEAD commits
- 4 unit tests (expired enqueue, HEAD preserved, skip negative TTL, skip zero TTL)

**3. Expired share links deletion** — ✅ DONE (2026-02-02)
- `processShareLink()` now calls `DeleteShareLink()` instead of just logging

### Admin Panel — WORKING ✅
**Status**: ✅ Working in Docker (2026-02-12)
**Reported**: 2026-02-02
**Fixed**: 2026-02-12

The sys-admin panel is fully accessible at `/sys/` in Docker deployments. Webpack builds `sysadmin.html` as a separate entry point, nginx serves it via `try_files`, and the Go backend catch-all serves it for non-Docker setups. All ~70 React routes load correctly.

**What exists in frontend** (all React components, now accessible):
- Users: list, search, create, edit, LDAP, admins
- Groups: list, search, create, members, libraries
- Departments: list, create, hierarchy, members, libraries
- Organizations: list, search, create, users, groups, repos
- Institutions, Logs, Devices, Statistics, Web Settings, Notifications

**What exists in backend**:
- Organizations CRUD: ✅ Full (`/admin/organizations/`)
- Departments CRUD: ✅ Full (`/admin/address-book/groups/`)
- User management: 🟡 Partial (per-org list, update role/quota, deactivate — no create, no global list)
- Admin groups: ❌ Missing (user-facing group CRUD exists, but admin-level endpoints don't)
- Admin libraries: ❌ Missing
- Admin user search: ❌ Missing

**Key decision**: Should groups/departments be managed via OIDC provider (claims-based sync) or locally in SesameFS? See `CURRENT_WORK.md` → "PRIORITY 1" for full analysis with 3 options.

**Key files**:
- Frontend: `frontend/src/pages/sys-admin/` (all components), `frontend/config/webpack.entry.js` (entry points)
- Backend: `internal/api/v2/admin.go` (org/user handlers), `internal/api/v2/groups.go` (user-facing groups)
- Config: `frontend/src/utils/constants.js` lines 152-173 (`window.sysadmin.pageOptions`)

---

## ✅ RECENTLY FIXED (2026-01-31 Session 15)

### Download URLs Used Wrong Port (ERR_CONNECTION_REFUSED) - FIXED ✅
**Fixed**: 2026-01-31
**Was**: Download URLs pointed to `http://localhost:8082/seafhttp/...` (backend's internal port), but the browser accesses the app at `http://localhost:3000` (nginx). Browser got ERR_CONNECTION_REFUSED.
**Root Cause**: `SERVER_URL=http://localhost:8082` in docker-compose, but browser-facing URLs should use the request's Host header.
**Fix**: Added `getBrowserURL()` helper that uses `X-Forwarded-Proto` + `Host` headers from the request to generate browser-reachable URLs. Applied to `GetDownloadLink`, `GetUploadLink`, `GetFileInfo`, and `redirectToDownload`.
**Files**: `internal/api/v2/files.go`, `internal/api/v2/fileview.go`

### File Download Returned JSON Instead of Download URL - FIXED ✅
**Fixed**: 2026-01-31
**Was**: Clicking download on a file showed JSON metadata (`{"id":"...","name":"test.md",...}`) instead of downloading.
**Root Cause**: `seafile-js` calls `GET /api2/repos/{id}/file/?p={path}&reuse=1` expecting a plain download URL string. Our `GetFileInfo` handler returned JSON metadata for all requests.
**Fix**: `GetFileInfo` now detects api2 download requests (via `reuse` parameter or `/api2/` URL prefix) and returns a plain download URL string instead of JSON.
**Files**: `internal/api/v2/files.go` — new `getFileDownloadURL()` method + `getBrowserURL()` helper

### Search User 404 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `GET /api2/search-user/?q=a` returned 404 (Not Found)
**Impact**: Transfer ownership dialog, share dialog user search didn't work
**Fix**: Implemented `handleSearchUser` endpoint that searches users by email/name within the same organization
**Files**: `internal/api/server.go`

### Multi-Share-Links 404 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `POST /api/v2.1/multi-share-links/` returned 404
**Impact**: "Generate Share Link" feature didn't work
**Fix**: Added `/multi-share-links/` route aliases pointing to existing share link handlers
**Files**: `internal/api/v2/share_links.go`

### Copy/Move Progress 404 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `GET /api/v2.1/query-copy-move-progress/?task_id=...` returned 404 (operations still worked)
**Root Cause**: Backend had `/api/v2.1/copy-move-task/` but `seafile-js` calls `/api/v2.1/query-copy-move-progress/`
**Fix**: Added alias routes for both URL patterns
**Files**: `internal/api/v2/batch_operations.go`

### File History Restore 400 Error - FIXED ✅
**Fixed**: 2026-01-31
**Was**: `POST /api/v2.1/repos/{id}/file/?p=/test.md` with `operation=revert` returned 400
**Root Cause**: `FileOperation` handler didn't support the `revert` operation
**Fix**: Added `RevertFile` handler that restores a file from a previous commit by traversing the old commit's tree, extracting the file entry, and creating a new commit in the current HEAD
**Files**: `internal/api/v2/files.go`

---

### Hardcoded Role Hierarchies Missing Superadmin - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Role hierarchy maps in `libraries.go`, `files.go`, `batch_operations.go` only had `admin(3), user(2), readonly(1), guest(0)`. The `superadmin` role was missing, so superadmin users got role level 0 (unknown key) and were denied write operations.
**Root Cause**: Role hierarchy was duplicated as inline `map[OrganizationRole]int` in 3 handler files instead of using a shared constant or the middleware's `hasRequiredOrgRole()`.
**Fix**: Added `RoleSuperAdmin: 4` to all 3 inline role hierarchy maps. Also added to `permissions.go` (the authoritative source).
**Files**: `internal/api/v2/libraries.go`, `internal/api/v2/files.go`, `internal/api/v2/batch_operations.go`
**Note**: ✅ Technical debt resolved (2026-02-12) — inline maps were removed, all 3 files now delegate to `middleware.HasRequiredOrgRole()`. The canonical maps live only in `internal/middleware/permissions.go`.

### Account Info `can_generate_share_link` Field Name
**Status**: ℹ️ Documentation note
**Discovered**: 2026-01-29
**Detail**: The account info endpoint returns `can_generate_share_link` (not `can_generate_shared_link`). Integration tests initially used the wrong field name. Not a bug in the API — just a test expectation mismatch.

### Anonymous Auth Bypasses Admin API Endpoints — REMOVED ✅
**Status**: Removed 2026-04-10 — `AllowAnonymous` config option and its anonymous fallback in `authMiddleware` have been deleted. Dev tokens must be provided explicitly.

### Change Password Shows for Non-Encrypted Libraries - FIXED ✅
**Fixed**: 2026-01-28
**Was**: "Change Password" menu item appeared for non-encrypted libraries
**Root Cause**: Truthy check `if (repo.encrypted)` may have had edge cases
**Fix**: Made check explicit: `if (repo.encrypted === true || repo.encrypted === 1)`
**Files**: `frontend/src/pages/my-libs/mylib-repo-menu.js`

### Watch/Unwatch File Changes - NOT IMPLEMENTED
**Status**: ❌ BACKEND NOT IMPLEMENTED
**Reported**: 2026-01-28
**Error**: `POST http://localhost:8080/api/v2.1/monitored-repos/ 404 (Not Found)`

**Missing Endpoints**:
- `POST /api/v2.1/monitored-repos/` - Add library to monitored list
- `DELETE /api/v2.1/monitored-repos/{repo_id}/` - Remove from monitored list
- `GET /api/v2.1/monitored-repos/` - List monitored libraries

**Current State**:
- Frontend UI toggle exists (shows/hides monitor icon)
- Backend endpoints return 404
- No notification system implemented

**Required Work** (if implementing):
1. Create `monitored_repos` table in Cassandra
2. Implement CRUD endpoints for monitored repos
3. Design notification system (email, websocket, polling?)
4. Implement backend notification triggers on file changes
5. Connect frontend to display notifications

**Note**: This is a complex feature requiring significant backend work. Consider deferring.

### Test Scripts Don't Fully Clean Up — FIXED ✅
**Status**: ✅ All scripts have cleanup (2026-02-10)
**Reported**: 2026-01-28
**Fixed**: 2026-02-10
**Symptom**: Running tests leaves test libraries/files in the database
**Resolution**: All test scripts now have `cleanup()` function with `trap cleanup EXIT` to remove test-created resources on exit (success or failure).
**Scripts with cleanup**: `test-file-operations.sh`, `test-batch-operations.sh`, `test-permissions.sh`, `test-library-settings.sh`, `test-encrypted-library-security.sh`, `test-groups.sh`

### Pre-Existing Go Unit Test Failures (4 tests) — FIXED ✅
**Fixed**: 2026-01-29 (Session 11)
**Was**: 4 tests failing due to nil-pointer dereferences in test setup
**Fix**: Fixed SessionManager init (nil cache → NewSessionManager), fixed JSON format expectations in OnlyOffice tests

### Frontend Unit Test Coverage Extremely Low
**Status**: CRITICAL GAP
**Reported**: 2026-01-28
**Symptom**: Only 4 test files for 620+ frontend source files (~0.6% coverage)

**Current State**:
| Category | Source Files | Test Files |
|----------|-------------|------------|
| Components | 347 | 1 |
| Pages | 260 | 0 |
| Dialogs | 159 | 1 |
| Utils | 15 | 1 |
| Models | ~10 | 1 |
| **Total** | **~620+** | **4** |

**Priority Tests Needed**:
1. **Utils functions** - Pure functions, easy to test
2. **Models** - Data transformation logic
3. **API client methods** - Mock responses, verify calls
4. **Dialog components** - Render tests, user interactions
5. **Permission checks** - Verify UI hides/shows based on role

**Test Pattern**: Use documentation-style tests (like modal-pattern.test.js) that verify file contents without full React rendering to avoid @testing-library/react ES module issues.

### Frontend E2E Tests Not Implemented
**Status**: NEEDS DESIGN
**Reported**: 2026-01-28
**Symptom**: No Cypress/Playwright tests that test actual UI with running backend
**Expected**: Should have E2E tests for login, file operations, sharing, etc.
**Required Work**:
1. Choose E2E framework (Cypress or Playwright)
2. Set up test fixtures and test user accounts
3. Write integration tests for key workflows

### Many Dialogs Need Modal Pattern Fix
**Status**: MOSTLY FIXED (2026-01-28)
**Reported**: 2026-01-28
**Symptom**: Multiple dialogs in `mylib-repo-list-item.js` may not open properly

**FIXED Dialogs** (converted to plain Bootstrap):
- ✅ ShareDialog (already fixed)
- ✅ DeleteRepoDialog (already fixed)
- ✅ TransferDialog (fixed 2026-01-28)
- ✅ LibHistorySettingDialog (fixed 2026-01-28)
- ✅ ChangeRepoPasswordDialog (already fixed)
- ✅ ResetEncryptedRepoPasswordDialog (fixed 2026-01-28)
- ✅ LabelRepoStateDialog (fixed 2026-01-28)
- ✅ LibSubFolderPermissionDialog (fixed 2026-01-28)
- ✅ RepoAPITokenDialog (fixed 2026-01-28)
- ✅ RepoSeaTableIntegrationDialog (fixed 2026-01-28)
- ✅ RepoShareAdminDialog (fixed 2026-01-28)
- ✅ LibOldFilesAutoDelDialog (fixed 2026-01-28)
- ✅ ListTaggedFilesDialog (fixed 2026-01-28)
- ✅ EditFileTagDialog (fixed 2026-01-28)
- ✅ CreateTagDialog (fixed 2026-01-28)

**Remaining**: ~90+ dialogs in sysadmin and other areas still use reactstrap Modal
**Fix Pattern**: See [docs/FRONTEND.md](FRONTEND.md) → "Modal Pattern"

### Library Transfer Not Working
**Status**: NOT IMPLEMENTED
**Reported**: 2026-01-28
**Symptom**: Clicking "Transfer" on a library does nothing, no errors shown
**Root Cause**: The `seafileAPI.transferRepo()` method doesn't exist in the seafile-js library
**Required Work**:
1. Add `transferRepo(repoID, email)` method to `frontend/src/utils/seafile-api.js`
2. Create backend endpoint `PUT /api2/repos/{repo_id}/owner/`
3. Implement ownership change in database (update `libraries.owner_id`)

### Sharing / Multiple Owners / Group Ownership
**Status**: DESIGN NEEDED
**Reported**: 2026-01-28
**Requirement**: Libraries should support:
- Owners should be able to share their libraries
- Multiple owners for one library
- Group ownership (a group can own a library)
**Current State**:
- `libraries` table has single `owner_id` field
- Sharing exists via `shares` table but doesn't grant ownership
**Required Work**:
1. Design data model for multi-owner / group owner support
2. Create `library_owners` table or modify `libraries` schema
3. Update permission checks to allow any owner to share
4. Add frontend UI for managing library owners

---

## ✅ RECENTLY FIXED (2026-01-29 Sessions 7-9)

### OnlyOffice "Invalid Token" Error - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Opening Word/Excel/PPT documents via OnlyOffice showed "Invalid Token — The provided authentication token is not valid"
**Root Cause (auth)**: File view endpoint (`/lib/:repo_id/file/*`) had a custom auth middleware that only supported dev tokens, not OIDC sessions.
**Root Cause (JWT)**: Go `html/template` applied JavaScript-context escaping (`\/`, `\u0026`, extra whitespace around booleans) when building the config object, causing a mismatch with the JWT payload signed by `json.Marshal`.
**Fix**: (1) Replaced custom auth middleware with thin wrapper that delegates to server's standard auth. (2) Replaced `html/template` field-by-field config with `json.Marshal` output — guarantees byte-identical config/JWT. (3) Added `url.QueryEscape` for file_path in callback URL.
**File**: `internal/api/v2/fileview.go`
**Status**: 🔒 FROZEN — OnlyOffice integration stable and verified

### CreateFile in Nested Folder Corrupts Tree - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Creating a file (e.g., Word docx) inside any subfolder via the v2.1 API caused "Folder does not exist" when navigating back
**Root Cause**: `CreateFile` called `RebuildPathToRoot(result, newParentFSID)` without grandparent handling. For non-root parents, the modified subfolder was set as `root_fs_id` instead of updating root to point to the new subfolder.
**Fix**: Added `if parentPath == "/" / else { grandparent rebuild }` pattern matching `CreateDirectory`
**File**: `internal/api/v2/files.go` — CreateFile function

### Nested Directory Creation (depth 3+) Corrupts Root FS - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Creating directories at depth 3+ produced incorrect root_fs_id → "Folder does not exist"
**Root Cause**: Re-traversed uncommitted HEAD for grandparent rebuild, producing wrong ancestor data
**Fix**: Used original traversal result's ancestor chain for `RebuildPathToRoot`
**Files**: `internal/api/v2/files.go`, `internal/api/v2/batch_operations.go`

### Batch Move/Copy Destination Rebuild Bug - FIXED ✅
**Fixed**: 2026-01-29
**Was**: Batch move/copy into nested directories could corrupt destination tree
**Root Cause**: Same stale HEAD re-traversal bug on destination side of batch operations
**Fix**: Same pattern — use original traversal result
**File**: `internal/api/v2/batch_operations.go`

---

## ✅ RECENTLY FIXED (2026-01-28 Session 3)

### File Creation 409 Conflict in Nested Folders - FIXED ✅
**Fixed**: 2026-01-28
**Error**: `POST /api/v2.1/repos/{repo_id}/file/?p={path} 409 (Conflict)`
**Symptom**: Creating a file inside a nested folder (e.g., `/test0035/test0035/file.docx`) returned 409 incorrectly

**Root Cause**:
In `CreateFile`, `TraverseToPath("/parent/child")` returns:
- `result.Entries` = entries of `/parent` (grandparent)
- `result.TargetFSID` = FSID of `/parent/child` (actual parent)

Code was checking `result.Entries` instead of getting entries from `result.TargetFSID`.
If a name existed at the grandparent level, it would incorrectly return 409.

**Fix**: Get entries from `result.TargetFSID` (matches `CreateFolder` function pattern)
**File**: `internal/api/v2/files.go` - CreateFile function

### Modal Pattern Applied to 15 Dialogs - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Multiple dialogs in library menu didn't open when using ModalPortal + reactstrap Modal
**Root Cause**: reactstrap Modal creates its own portal, doesn't render correctly inside ModalPortal
**Fix**: Converted all affected dialogs to plain Bootstrap modal classes
**Files Fixed**:
- `frontend/src/components/dialog/transfer-dialog.js`
- `frontend/src/components/dialog/lib-history-setting-dialog.js`
- `frontend/src/components/dialog/reset-encrypted-repo-password-dialog.js`
- `frontend/src/components/dialog/label-repo-state-dialog.js`
- `frontend/src/components/dialog/lib-sub-folder-permission-dialog.js`
- `frontend/src/components/dialog/repo-api-token-dialog.js`
- `frontend/src/components/dialog/repo-seatable-integration-dialog.js`
- `frontend/src/components/dialog/lib-old-files-auto-del-dialog.js`
- `frontend/src/components/dialog/edit-filetag-dialog.js`
- `frontend/src/components/dialog/create-tag-dialog.js`

---

## ✅ RECENTLY FIXED (2026-01-28 Session 2)

### Share Admin Dialog Not Opening - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Clicking "Share Admin" menu item did nothing
**Root Cause**: RepoShareAdminDialog uses reactstrap Modal inside ModalPortal
**Fix**: Converted to plain Bootstrap modal classes
**Files**: `frontend/src/components/dialog/repo-share-admin-dialog.js`

### Tagged Files Dialog Not Opening - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Clicking tag file count (e.g., "1 file") did nothing, even though API returned data
**Root Cause**: ListTaggedFilesDialog uses reactstrap Modal inside ModalPortal
**Fix**: Converted to plain Bootstrap modal classes
**Files**: `frontend/src/components/dialog/list-taggedfiles-dialog.js`

### Create Repo Tag 500 Error - FIXED ✅
**Fixed**: 2026-01-28
**Was**: `POST /api/v2.1/repos/:repo_id/repo-tags/` returned 500 "failed to initialize tag counter"
**Root Cause**: Cassandra LWT (ScanCAS) was incorrectly used for counter initialization
**Fix**: Replaced LWT with simple SELECT then INSERT/UPDATE pattern
**Files**: `internal/api/v2/tags.go` - CreateRepoTag function

### File Tags 500 Error - FIXED ✅
**Fixed**: 2026-01-28
**Was**: `POST /api/v2.1/repos/:repo_id/file-tags/` returned 500 Internal Server Error
**Root Cause**: Counter updates mixed with non-counter operations in Cassandra logged batch
**Fix**: Separated counter updates from logged batch (counter must be in separate query)
**Files**:
- `internal/api/v2/tags.go` - AddFileTag, RemoveFileTag: moved counter updates outside batch

### Copy/Move Dialog Not Showing Libraries - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Copy/Move dialogs showed empty library list (only current library visible)
**Root Cause**: API returned `permission: "owner"` but frontend filtered by `permission === 'rw'`
**Fix**: Added `apiPermission()` helper to translate "owner" to "rw" in API responses
**Files**:
- `internal/api/v2/libraries.go` - Added apiPermission() function, applied to all permission fields

### Tagged Files Feature Not Working - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Clicking tag file count (e.g., "3 files") did nothing
**Root Cause**:
1. Backend endpoint `GET /api/v2.1/repos/:repo_id/tagged-files/:tag_id/` was not implemented
2. Frontend `seafile-api.js` was missing all tag-related API methods (not in upstream seafile-js)
**Fix**:
1. Implemented `ListTaggedFiles` backend handler with correct response format
2. Added all tag API methods to `frontend/src/utils/seafile-api.js`
**Files**:
- `internal/api/v2/tags.go` - Added TaggedFileInfo struct and ListTaggedFiles handler
- `frontend/src/utils/seafile-api.js` - Added listRepoTags, createRepoTag, updateRepoTag, deleteRepoTag, getFileTags, addFileTag, deleteFileTag, listTaggedFiles, getShareLinkTaggedFiles

---

## ✅ RECENTLY FIXED (2026-01-28)

### Encrypted Library Password Cancel - FIXED ✅
**Fixed**: 2026-01-28
**Was**: Infinite loading spinner when closing password dialog
**Root Cause**: `onLibDecryptDialog` callback didn't distinguish between success and cancel
**Fix**: Added `success` parameter to callback; cancel now redirects to library list
**Files**:
- `frontend/src/components/dialog/lib-decrypt-dialog.js` - Pass true/false to callback
- `frontend/src/pages/lib-content-view/lib-content-view.js` - Handle success vs cancel

### Share Links API 500 Error - FIXED ✅
**Fixed**: 2026-01-28
**Was**: 500 Internal Server Error when opening Share dialog
**Root Cause**: Missing `share_links_by_creator` table in Cassandra schema
**Fix**: Created table and fixed UUID marshaling in queries
**Files**:
- `internal/api/v2/share_links.go` - Use `gocql.ParseUUID` instead of `uuid.Parse`
- `scripts/bootstrap.sh` - Added `share_links_by_creator` table
- `scripts/bootstrap-multiregion.sh` - Same

---

## ✅ RECENTLY FIXED (2026-01-27)

### Logout Button - FIXED ✅ 🔒 FROZEN
**Fixed**: 2026-01-27
**Status**: Working correctly - DO NOT MODIFY
**Issue**: Clicking logout went to `/accounts/logout/` but nothing happened
**Root Cause**: Frontend nginx wasn't proxying `/accounts/` routes to backend
**Fix**: Added `/accounts/` location block to `frontend/nginx.conf`
**Files**: `frontend/nginx.conf` (lines 77-83)

### Anonymous Access for Testing — REMOVED ✅
**Removed**: 2026-04-10
**Was**: `AUTH_ALLOW_ANONYMOUS=true` allowed unauthenticated requests to be injected as the first dev token user.
**Why removed**: Redundant — `AUTH_DEV_MODE=true` with an `Authorization: Token <dev-token>` header achieves the same without an implicit bypass. The feature was deleted along with `AllowAnonymous` config field and `applyAnonymousDevAuth()`.

### Frontend Login Bypass - IMPLEMENTED ✅
**Implemented**: 2026-01-27
**Status**: Working - FOR TESTING ONLY
**Feature**: Set `REACT_APP_BYPASS_LOGIN=true` to skip login page
**Files**: `frontend/src/utils/seafile-api.js`, `frontend/.env`

---

## ✅ RECENTLY FIXED (2026-01-24)

### Media File Viewer Fix - FIXED ✅ (Pending manual testing)
**Fixed**: 2026-01-23
**Was**: CRITICAL UX bug
**Root Cause**: Mobile view missing `onClick` handler, causing direct navigation to download URL
**Files Fixed**:
- `frontend/src/components/dirent-list-view/dirent-list-item.js` line 798

**What Works Now** (pending manual testing):
- ✅ Clicking images should open image popup viewer
- ✅ Clicking PDFs should open in-browser PDF viewer
- ✅ Clicking videos should open video player
- ✅ Mobile view now has same click handling as desktop view

**Manual Testing Required**:
- Test clicking various file types on mobile view
- Test clicking images (should open popup)
- Test clicking PDFs (should open viewer)
- Test clicking videos (should open player)

### Permission Middleware Integration - COMPLETE ✅ (Pending full testing)
**Completed**: 2026-01-23
**Status**: Core implementation done, example checks integrated
**Files Implemented**:
- `internal/middleware/permissions.go` - Full permission middleware (371 lines)
- `internal/api/server.go` - Initialized and integrated
- `internal/api/v2/libraries.go` - Example permission checks

**What's Implemented**:
- ✅ Organization role checking (admin/user/readonly/guest)
- ✅ Library permission checking (owner/rw/r)
- ✅ Group role checking (owner/admin/member)
- ✅ Group permission resolution (users inherit group library permissions)
- ✅ CreateLibrary: Requires "user" role or higher
- ✅ DeleteLibrary: Requires library ownership

**Manual Testing Required**:
- Test CreateLibrary with different user roles
- Test DeleteLibrary with non-owner users
- Test group permission inheritance
- Add permission checks to remaining handlers incrementally

### Database Seeding - COMPLETE ✅
**Completed**: 2026-01-23
**Status**: Fully implemented and tested
**Files Implemented**:
- `internal/db/seed.go` - Database seeding implementation (220 lines)
- `cmd/sesamefs/main.go` - Integrated into startup

**What's Seeded**:
- ✅ Default organization (1TB quota)
- ✅ Admin user (role: admin)
- ✅ Test users (user, readonly, guest roles) - dev mode only
- ✅ Users indexed in users_by_email for login

### Test Coverage Improvements - COMPLETE ✅
**Completed**: 2026-01-24
**Status**: Comprehensive tests added for all new features

**Backend Tests Created**:
- `internal/db/seed_test.go` - Database seeding tests (9 tests, all passing)
  - Tests UUID uniqueness, idempotency, dev vs production modes
  - Tests organization creation, admin user, test users
  - Tests email indexing for login
- `internal/api/v2/libraries_test.go` - Permission middleware tests (3 test suites)
  - Tests role hierarchy (admin > user > readonly > guest)
  - Tests library creation permission (requires "user" role or higher)
  - Tests library deletion permission (requires ownership)
  - Tests group permission resolution

**Frontend Tests Created**:
- `frontend/src/components/dirent-list-view/__tests__/dirent-list-item.test.js`
  - Documents media viewer fix behavior
  - Tests file type detection (images, PDFs, videos)
  - Tests onClick handler presence (desktop and mobile views)
  - Regression test for line 798 fix

**Test Results**:
- ✅ All backend tests passing
- ✅ Backend coverage: 23.4% overall (stable)
- ✅ internal/db: 0.0% (tests are documentation-style, skip DB operations)
- ✅ internal/api/v2: 18.4% coverage (improved from adding tests)

**Type Error Fixed**:
- Fixed `internal/api/v2/libraries_test.go:468` - Changed `Encrypted: false` (bool) to `Encrypted: 0` (int)
- This is NOT a protocol change - API already returns int (0/1) for Seafile compatibility

### Share Modal 500 Error - FIXED ✅
**Fixed**: 2026-01-23
**Was**: CRITICAL regression
**Root Cause**: Missing `org_id` in Cassandra queries (partition key required)
**Files Fixed**:
- `internal/api/v2/share_links.go` lines 125, 153
- `internal/api/v2/file_shares.go` lines 116, 138, 146, 651
- `internal/middleware/permissions.go` line 242 (group permission resolution)

**What Works Now**:
- ✅ Share modal loads without errors
- ✅ Group names display correctly (not UUIDs)
- ✅ Users see libraries shared to their groups
- ✅ User emails display correctly (not UUIDs)

---

## ✅ FIXED SECURITY/PERMISSION ISSUES (Fixed 2026-01-24 to 2026-01-27)

**Status**: ✅ ALL FIXED - Backend permission system complete
**Testing**: Manual testing passed with all 4 user roles

### Issue 1: All Users Can See All Libraries - FIXED ✅
**Severity**: CRITICAL - Complete privacy violation
**Discovered**: 2026-01-24 manual testing

**Bug**: User logged in as `user@sesamefs.local` can see libraries owned by `admin@sesamefs.local`

**Expected Behavior**:
- Users should ONLY see their own libraries
- Exception: Libraries explicitly shared with them

**Actual Behavior**:
- `GET /api/v2.1/repos/` returns ALL libraries in organization
- No filtering by ownership or shares

**Root Cause**: `ListLibraries()` in `internal/api/v2/libraries.go` has NO permission filtering

**Impact**:
- Zero privacy between users
- Users can see library names, sizes, encryption status of all libraries
- Violates basic multi-tenant isolation

**Files**: `internal/api/v2/libraries.go` - `ListLibraries()` function

---

### Issue 2: Users Can Access Other Users' Libraries - FIXED ✅
**Severity**: CRITICAL - Complete access control failure
**Discovered**: 2026-01-24 manual testing

**Bug**: Any user can access any library by direct URL or navigation

**Test Cases**:
- `user@sesamefs.local` browsed libraries owned by `admin@sesamefs.local`
- `guest@sesamefs.local` accessed library owned by `user@sesamefs.local`
- All directory contents visible to unauthorized users

**Expected Behavior**:
- Users can only access own libraries
- Access to other libraries ONLY if explicitly shared
- Should get 403 Forbidden if attempting unauthorized access

**Actual Behavior**:
- NO permission checks on directory listing endpoints
- NO permission checks on library detail endpoints
- Complete access to all libraries regardless of ownership

**Root Cause**: Missing permission checks on:
- `GET /api/v2.1/repos/:repo_id` (GetLibrary)
- `GET /api/v2.1/repos/:repo_id/dir/` (ListDirectory)

**Impact**:
- Users can read all files from all libraries
- Zero access control
- Data breach scenario

---

### Issue 3: Readonly Users Can Write to Other Users' Libraries - FIXED ✅
**Severity**: CRITICAL - Role-based access control failure
**Discovered**: 2026-01-24 manual testing

**Bug**: User `readonly@sesamefs.local` successfully edited Word docx files in encrypted libraries owned by other users

**Expected Behavior**:
- readonly role = read-only access to own libraries ONLY
- Should get 403 on write attempts (upload, edit, delete)
- Should have ZERO access to other users' libraries

**Actual Behavior**:
- readonly user can upload files to any library
- readonly user can edit documents in any library (via OnlyOffice)
- NO enforcement of role restrictions

**Root Cause**: Missing permission checks on:
- File upload endpoints (`/seafhttp/upload-api/`)
- OnlyOffice save callback (`internal/api/v2/onlyoffice.go`)
- File create/edit/delete operations

**Impact**:
- Role system is non-functional
- readonly and guest roles have same permissions as admin
- Data corruption risk

---

### Issue 4: Guest User Can Modify Libraries and Cause Data Loss - FIXED ✅
**Severity**: CRITICAL - Data corruption + access control failure
**Discovered**: 2026-01-24 manual testing

**Bug**: User `guest@sesamefs.local` accessed library owned by `user@sesamefs.local`, created file, caused original files to disappear

**Timeline**:
1. guest@ logged in
2. Navigated to library owned by user@ (test0034)
3. Created new file `test-guest.docx` (2.2 KB)
4. After creation, user@'s original files disappeared from directory listing

**Expected Behavior**:
- guest role should have ZERO access to other users' libraries
- guest should only see own libraries (if any)
- Creating files should not cause existing files to disappear

**Actual Behavior**:
- guest can access any library
- guest can create files in any library
- File creation caused data corruption (files disappeared)

**Root Cause**:
- Missing permission checks (same as Issues 1-3)
- Possible commit/fs_object corruption in multi-user scenario

**Impact**:
- Data loss
- Complete lack of user isolation
- Potential filesystem corruption

**Files**:
- Permission checks needed in all file operation endpoints
- Investigate fs_object/commit corruption issue

---

### Issue 5: Encrypted Libraries Not Protected from Sharing - FIXED ✅
**Severity**: CRITICAL - Security policy violation
**Discovered**: 2026-01-24 (known issue, not yet enforced)

**Policy**: Password-encrypted libraries CANNOT be shared (sharing would require sharing encryption key)

**Status**: NOT ENFORCED in backend

**Expected Behavior**:
- Attempting to share encrypted library should return 403
- Clear error message: "Cannot share encrypted libraries. Move files to a non-encrypted library to share them."

**Actual Behavior**:
- Backend allows share creation on encrypted libraries
- Frontend shows loading spinner (stuck) when trying to share encrypted files

**Root Cause**: No validation in share creation endpoints

**Files**: `internal/api/v2/file_shares.go` - Share creation functions

**Impact**:
- Security vulnerability
- Encrypted data could be shared inappropriately
- Encryption key management violated

---

## 📋 Comprehensive Fix Plan

**See**: `docs/PERMISSION-ROLLOUT-PLAN.md` for full implementation plan

**Summary**:
- Phase 1: Library access control (filter ListLibraries, check GetLibrary, check directory listing)
- Phase 2: File operations (upload, edit, delete, rename, move)
- Phase 3: Encrypted library policy enforcement
- Estimated time: 2-3 days
- Approach: Systematic application of permission middleware to ALL endpoints

---

## ✅ FIXED (2026-02-11) - Sync Protocol Security + Environment Management

### Sync Protocol Permission Enforcement - FIXED ✅
**Fixed**: 2026-02-11
**Was**: 🔴 CRITICAL - All 15 sync endpoints had ZERO permission checks. Any authenticated user could read/write ANY library.

**What was fixed**:
- Added `permMiddleware` to `SyncHandler` struct
- `checkSyncPermission()` helper checks `HasLibraryAccess()` before every operation
- 9 READ endpoints require `PermissionR`: GetHeadCommit, GetCommit, GetBlock, CheckBlocks, GetFSIDList, GetFSObject, PackFS, CheckFS, GetDownloadInfo
- 4 WRITE endpoints require `PermissionRW`: PutCommit, PutBlock, RecvFS, UpdateBranch
- `GetHeadCommitsMulti`: silently filters repos user cannot access
- `PermissionCheck` endpoint: no longer a stub, calls `GetLibraryPermission()` and returns 403 if denied
- `QuotaCheck` endpoint: now verifies read access before responding
- `GetDownloadInfo`: returns actual user permission instead of hardcoded `"rw"`
- `HandleDownload` in `seafhttp.go`: now checks `PermissionR` (matching `HandleUpload` pattern)

**Files**: `internal/api/sync.go`, `internal/api/server.go`, `internal/api/seafhttp.go`

### Sync Auth Middleware Hardened - FIXED ✅
**Fixed**: 2026-02-11
**Was**: 🔴 CRITICAL - No token = silent dev-user fallback; invalid token in dev mode = silent dev-user fallback

**What was fixed**:
- No token = 401 Unauthorized (always)
- Invalid token = 401 Unauthorized (always)
- Valid dev tokens still work in dev mode (intentional)

**Files**: `internal/api/server.go` (`syncAuthMiddleware`)

### Docker Compose Secrets Externalized - FIXED ✅
**Fixed**: 2026-02-11
**Was**: Production credentials (email/password) hardcoded in `docker-compose.yaml`, JWT secret hardcoded in `config.docker.yaml`

**What was fixed**:
- All values now use `${VAR:-default}` syntax, read from `.env`
- `.env.example` documents all variables with safe defaults
- `seafile-cli-debug` moved to `profiles: [debug]` (not started by default)
- JWT secret uses env var `ONLYOFFICE_JWT_SECRET`
- `.reference.md` added to `.gitignore`

**Files**: `docker-compose.yaml`, `docker-compose-multiregion.yaml`, `.env`, `.env.example`, `config.docker.yaml`, `.gitignore`

---

## ✅ RECENTLY FIXED (2026-01-27) - Security & Permissions

### Encrypted Libraries Load Without Password - FIXED ✅
**Fixed**: 2026-01-27
**Was**: 🔴 CRITICAL - Security bypass
**Status**: ✅ FIXED - Encrypted libraries now properly protected

**Bug Was**: Frontend loaded encrypted library contents even without entering password

**Root Cause Found**: Frontend was making directory listing API calls without checking `libNeedDecrypt` state first

**Fix Applied**:
- Added encryption check to `loadDirentList()` - returns early if `libNeedDecrypt` is true
- Added encryption check to `loadDirData()` - returns early if `libNeedDecrypt` is true
- Added encryption check to `loadSidePanel()` - returns early if `libNeedDecrypt` is true

**Files Fixed**: `frontend/src/pages/lib-content-view/lib-content-view.js`

**Behavior Now**:
- ✅ Password dialog appears first
- ✅ NO API calls made until password verified
- ✅ Directory listing blocked until decrypt session active
- ✅ Backend returns 403 if no decrypt session (double protection)

### User Profile Shows UUIDs Instead of Names - FIXED ✅
**Fixed**: 2026-01-27
**Was**: User profiles showed UUIDs like "00000000-0000-0000-0..."

**Fix Applied**:
- Backend `handleAccountInfo` now queries actual user data from database
- Returns proper `name`, `email`, `role` from users table

**Files Fixed**: `internal/api/server.go:822-893`

### Role-Based UI Permissions - IMPLEMENTED ✅
**Implemented**: 2026-01-27
**Status**: ✅ Backend complete, Frontend ~30% complete

**Features**:
- Backend returns permission flags: `can_add_repo`, `can_share_repo`, etc.
- Frontend loads permissions on startup
- "New Library" button hidden for readonly/guest users
- Empty library message changed for restricted users

**Files**:
- `internal/api/server.go` - Permission flags in account info
- `frontend/src/app.js` - `loadUserPermissions()` function
- `frontend/src/components/toolbar/repo-view-toobar.js` - Conditional button rendering
- `frontend/src/pages/my-libs/my-libs.js` - Role-aware empty message

**Remaining Frontend Work**: See CURRENT_WORK.md for list of UI elements needing permission checks

---

## 🔴 CRITICAL UX BUGS

**None currently!** 🎉 (Pending manual testing)

---

## ✅ LIBRARY SETTINGS - IMPLEMENTED (Session 6)

**Status**: ✅ Backend complete (implemented 2026-01-29 Session 6)

| Feature | Endpoint | Status |
|---------|----------|--------|
| Watch/Unwatch | `POST /api/v2.1/monitored-repos/` | ❌ Not implemented (needs notification system) |
| History Setting | `GET/PUT /api/v2.1/repos/{id}/history-limit/` | ✅ Complete |
| API Token | `GET/POST/PUT/DELETE /api/v2.1/repos/{id}/repo-api-tokens/` | ✅ Complete |
| Auto Deletion | `GET/PUT /api/v2.1/repos/{id}/auto-delete/` | ✅ Complete |
| Library Transfer | `PUT /api2/repos/{id}/owner/` | ✅ Complete |

**File**: `internal/api/v2/library_settings.go`

### Library Settings Frontend Errors — FIXED ✅ (2026-01-30)

| Error | Root Cause | Fix |
|-------|-----------|-----|
| `POST repo-api-tokens/ 400` | Backend used `ShouldBindJSON`, frontend sends FormData | Changed to `ShouldBind` (auto-detects content type) |
| `PUT auto-delete/ 400` | Same — JSON-only binding vs FormData | Changed to `ShouldBind` |
| `PUT history-limit/ 400` | Same — JSON-only binding vs FormData | Changed to `ShouldBind` |
| `"disabled by Admin"` | `enableRepoHistorySetting: false` in index.html | Set to `true` |
| `enableRepoAutoDel: 'False'` | Auto-delete feature flag disabled | Set to `'True'` |

**File**: `internal/api/v2/library_settings.go` — all 5 handlers now accept both JSON and FormData (matching stock Seafile's `request.data` behavior)
**File**: `frontend/public/index.html` — enabled `enableRepoHistorySetting` and `enableRepoAutoDel`

**Note**: `POST monitored-repos/ 404` remains expected (not implemented — needs notification system)

---

## ✅ FILE OPERATIONS - COMPLETE

Move/Copy operations fully implemented (batch sync + async variants) with conflict resolution:
- **Conflict policies**: `replace`, `autorename`, `skip` — applied to both sync and async (cross-repo) paths
- **Pre-flight check**: Returns HTTP 409 with `conflicting_items` when no policy specified
- **137 integration tests** in `scripts/test-nested-move-copy.sh` (nested ops, conflicts, cross-repo, autorename)
- See also `scripts/test-batch-operations.sh` for basic batch operation tests.

---

## ⚠️ UI/UX ISSUES

### Thumbnails Not Implemented
**Severity**: MEDIUM
**Impact**: Visual polish

**Missing**:
- No image thumbnails in file list
- Grid view has no previews

### User Avatars Not Implemented
**Severity**: LOW
**Impact**: Visual polish

**Missing**:
- No profile pictures for users
- Generic icon shown

### Missing File Type Icons — FIXED ✅
**Severity**: LOW
**Impact**: Visual polish
**Fixed**: 2026-02-12

**Issue**: Folder icon variants returned 404 (read-only, shared-out, combo)
**Fix**: Created 6 missing folder icon PNGs in `frontend/public/static/img/`: `folder-read-only-{24,192}.png`, `folder-shared-out-{24,192}.png`, `folder-read-only-shared-out-{24,192}.png`

---

## 🚧 BACKEND NOT IMPLEMENTED

### Garbage Collection — COMPLETE ✅
**Status**: ✅ Fully implemented (2026-01-30), major overhaul (2026-03-17)
**Files**: `internal/gc/` — gc.go, queue.go, worker.go, scanner.go, store.go, store_cassandra.go, gc_hooks.go, gc_adapter.go
**Tests**: 55 Go unit tests + 21 bash integration tests
**Admin API**: `GET /api/v2.1/admin/gc/status`, `POST /api/v2.1/admin/gc/run`

**2026-03-17 overhaul:**
- Worker: 7 item types (block, commit, fs_object, block_mapping, share_link, share, restore_job)
- Scanner: 8 phases (orphaned blocks/commits/fs_objects, expired share links/versions/auto-delete/shares/restore jobs)
- Commit deletion now cascades → root fs_object → child entries → blocks (was missing cascade)
- Library deletion enqueues all artifacts (shares, tags, tokens, locked files)
- Reverse lookup table `block_id_mappings_by_internal` eliminates full-table scans on block deletion
- `walkFSTree` converted from recursive to iterative (prevents stack overflow)
- Stats persisted to `gc_stats` table on shutdown, restored on startup (survives container restarts)
- Scanner runs immediately on startup before entering 24h ticker loop

### Authentication — COMPLETE ✅
**Status**: ✅ OIDC Phase 1 complete (2026-01-28) + dev tokens
**Files**: `internal/auth/oidc.go`, `internal/auth/session.go`, `internal/api/v2/auth.go`

**Security hardening (2026-02-20):**
- ✅ **JWT signature verification via JWKS**: `parseIDToken()` now fetches the provider's JWKS keys and verifies RS256/ES256 signatures using `golang-jwt/v5`. JWKS keys are cached for 1 hour with automatic refresh on unknown `kid` (key rotation support).
- ✅ **Rate limiting on auth endpoints**: Per-IP token-bucket rate limiter (~10 req/min) applied to `POST /api2/auth-token`, `POST /api2/client-sso-link`, `GET /oauth/callback`, and `POST /api/v2.1/auth/oidc/callback`. Returns 429 Too Many Requests when exceeded. Implementation: `internal/middleware/ratelimit.go`.

### Permission Middleware - COMPLETE ✅
**Status**: ✅ FULLY IMPLEMENTED AND INTEGRATED (2026-01-24)

**What's Working**:
- ✅ Database schema complete
- ✅ Middleware implementation complete (`internal/middleware/permissions.go`)
- ✅ Applied to ALL routes in `internal/api/server.go`
- ✅ Centralized permission enforcement
- ✅ Org-level role enforcement (admin vs user vs readonly vs guest)
- ✅ Library-level permission checking (owner vs collaborator)
- ✅ User isolation (users can only see/access their own libraries + shared)
- ✅ Write operations blocked for readonly/guest roles

**Priority**: ✅ COMPLETE - Ready for production multi-tenant deployment

### Encrypted Library Sharing Policy - ENFORCED ✅
**Status**: ✅ FULLY ENFORCED (2026-01-24)

**Policy**: Password-encrypted libraries CANNOT be shared
**Reason**: Sharing encrypted files requires sharing the encryption key, breaking security

**Implementation Status**: ✅ ENFORCED
- ✅ Backend blocks share creation on encrypted libraries with 403 error
- ✅ Clear error message returned to frontend

**Files**: `internal/api/v2/file_shares.go` - `CreateShare()` function

---

## ✅ FRONTEND MODAL ISSUES — RESOLVED

### Modal Dialog Migration — COMPLETE ✅
**Status**: ✅ All dialog files migrated (verified 2026-01-30)
**Detail**: Zero dialog files in `frontend/src/components/dialog/` import `Modal` from reactstrap. All use plain Bootstrap modal classes.
**Remaining reactstrap usage**: Some dialog files still import `Button`, `Input`, `Form` from reactstrap — these are form components (not Modal) and work correctly.
**Page-level Modal imports**: 4 page files (`app.js`, `institution-admin/index.js`, `sys-admin/index.js`, `wiki/index.js`) still import Modal from reactstrap for non-dialog purposes.

---

## ⚠️ PRODUCTION READINESS GAPS

### Error Handling & Monitoring — ✅ IMPLEMENTED
**Severity**: HIGH for production
**Status**: ✅ Complete (2026-01-30)

**Implemented**:
- ✅ Structured logging via `log/slog` (JSON in prod, text in dev)
- ✅ Prometheus metrics (`/metrics` endpoint)
- ✅ Health check endpoints (`/health` liveness, `/ready` readiness)
- ✅ Request logging middleware (method, path, status, latency)
- ⚠️ Alerting hooks not yet configured (Prometheus AlertManager can scrape `/metrics`)

### Documentation
**Severity**: HIGH for production
**Status**: Partial

**Missing**:
- User documentation
- Admin documentation
- Production deployment guide
- Backup/restore procedures
- Migration guide (from Seafile)

---

## ✅ RECENTLY FIXED (2026-01-22 - 2026-01-23)

### Encrypted Library Sharing Warning - FIXED
**Fixed**: 2026-01-22
**Issue**: Internal Link tab showed infinite loading spinner in encrypted libraries
**Root Cause**: Backend returned `encrypted: true` (boolean), frontend expected `encrypted: 1` (integer)
**Fix**: Changed all library endpoints to return integer (0/1)
**Files**: `internal/api/v2/libraries.go`

### Search Backend - IMPLEMENTED
**Completed**: 2026-01-22
**Issue**: Search returned empty stub results
**Fix**: Full Cassandra SASI search implementation
**Features**: Search libraries/files by name, filter by repo/type
**Files**: `internal/db/db.go`, `internal/api/v2/search.go`, `internal/api/server.go`

### Docker Build Memory Issues - FIXED
**Fixed**: 2026-01-22
**Issue**: Frontend build killed with "cannot allocate memory"
**Fix**: Increased Node memory to 4GB, removed Elasticsearch (saved 2GB)
**Files**: `frontend/Dockerfile`, `docker-compose.yaml`

### lib-decrypt-dialog Close Button - FIXED
**Fixed**: 2026-01-23
**Issue**: Close button showed square □ instead of × icon
**Root Cause**: Browser cache serving old JavaScript despite correct source code
**Solution**: Code was correct (`className="close"` with `<span>&times;</span>`)
**Files**: `frontend/src/components/dialog/lib-decrypt-dialog.js:72-74`

---

## 🟡 PLANNED ENHANCEMENTS

### Tenant Quota & Billing Features — NOT YET IMPLEMENTED
**Reported**: 2026-01-29
**Priority**: HIGH (required for multi-tenant production)

The organizations table currently only has `storage_quota` and `storage_used`. The following tenant-level features are needed:

1. **Storage quota (space)**: 0 to unlimited (currently exists but basic)
   - Need enforcement on upload (block uploads when quota exceeded)
   - Need quota usage tracking (periodic recalculation from blocks)
   - Need admin API to set/update quotas per tenant

2. **User count limits**: Max number of users per tenant
   - Need `max_users` field on organizations table
   - Need enforcement during user provisioning (OIDC auto-provision + admin API create)
   - Need admin API to set/update user limits

3. **Upload/download bandwidth metering**: Measurable for billing
   - Need per-org tracking of upload bytes and download bytes
   - Need time-bucketed counters (daily/monthly) for billing reports
   - Need admin API to query usage stats per org per time period
   - Consider Cassandra counter tables for efficient increment

4. **Billing integration (optional)**:
   - Need webhook or API to report usage to external billing system
   - Need configurable billing periods (monthly, etc.)
   - Need usage report endpoint for billing dashboards

**Database changes needed**:
```sql
-- Add to organizations table
ALTER TABLE organizations ADD max_users INT;
ALTER TABLE organizations ADD billing_enabled BOOLEAN;

-- New table for metered usage
CREATE TABLE org_usage_counters (
    org_id UUID,
    period TEXT,          -- e.g., "2026-01" (monthly bucket)
    upload_bytes COUNTER,
    download_bytes COUNTER,
    api_calls COUNTER,
    PRIMARY KEY ((org_id), period)
);
```

**Files to modify**:
- `internal/config/config.go` — billing config
- `internal/db/db.go` — new table
- `internal/api/v2/admin.go` — usage stats endpoints, quota enforcement
- `internal/api/seafhttp.go` — metering on upload/download
- `internal/api/v2/files.go` — metering on REST upload/download

---

## Low Priority / Future Enhancements

### Features Not Started
- Multi-factor authentication
- Activity logs/notifications stubbed
- AI search not implemented
- SeaTable integration not started
- Wiki features partially stubbed

### Admin Features
- Most org admin features stubbed
- System admin features mostly stubbed

---

### ISSUE-GC-ORPHANS-01: Orphaned shares/links After Library Permanent Delete or Auto-Delete

**Status**: ✅ Resolved (2026-03-17)
**Discovered**: 2026-02-24
**Priority**: ~~🟡 Medium~~ → Resolved

**Resolution (2026-03-17):**
All library artifacts are now cleaned on permanent delete via `enqueueLibraryArtifacts()` in the GC worker:
- ✅ `shares` + `shares_by_user` — cleaned via `ListSharesByLibrary` → `DeleteShare`
- ✅ `share_links` (all 4 tables) — cleaned via `DeleteShareLinksByLibrary`
- ✅ `repo_tags` + `file_tags` — cleaned via `cleanupLibraryTags`
- ✅ `repo_api_tokens` — cleaned via `ListRepoAPITokensByLibrary` → `DeleteRepoAPIToken`
- ✅ `locked_files` — cleaned via `DeleteLockedFilesByLibrary`

Additionally, GC scanner **Phase 7** now catches expired user-to-user shares (`expires_at < now`) independently of library deletion.

Historical orphans from before this change will be caught by scanner Phase 3/4 (orphaned commits/fs_objects) on the next 24h scan cycle.

---

### ISSUE-TRASH-CLEAN-01: `CleanRepoTrash` is a No-Op Stub

**Status**: ⚠️ Known gap — not yet implemented
**Discovered**: 2026-02-24
**Priority**: 🟡 Medium — user action has no effect; frontend shows success but nothing is cleaned

**Affected endpoint:**
`DELETE /api/v2.1/repos/:repo_id/trash/?keep_days=N` (`trash.go:404`)

**Current Behavior:**
When a user clicks "Clean Trash" on their file recycle bin, the handler immediately returns `{"success": true}` without doing anything. The comment in code says "handled by GC" but GC Phase 6 only runs on libraries with `auto_delete_days` configured — it does not respond to user-triggered trash clean requests.

**What It Should Do:**
1. Get all commits for the library sorted by timestamp
2. Keep: HEAD commit + any commit within `keep_days` of today
3. Enqueue expired commits' fs_objects via `getLibraryEnqueuer()` so GC deletes actual file data
4. Delete the expired commit rows from `commits` table

**Fix Plan:**
Tracked in `docs/TECHNICAL-DEBT.md` § 9, Gap B.

**Files involved:**
- `internal/api/v2/trash.go` — implement `CleanRepoTrash`
- `internal/gc/store.go` / `store_cassandra.go` — may need `ListCommitsWithTimestamps` per library

---

### ISSUE-S3-TRANSPORT-01: All S3 Operations Fail Until Container Restart — FIXED

**Discovered**: 2026-03-04 (production)
**Status**: ✅ Fixed
**Severity**: 🔴 Production outage — all uploads/downloads fail, requires container restart
**Symptom**: Every request to `/seafhttp/upload-api/` and download endpoints returns HTTP 500. Cassandra operations (login, create library, browse) continue working normally.

**Root Cause**: The Go `http.Transport` used by the AWS SDK S3 client had `MaxConnsPerHost: 64`. When AWS S3 experienced a transient network blip, TCP connections in the pool entered a half-open/zombie state (local OS thinks they're alive, remote endpoint already closed them). With all 64 connection slots occupied by zombies, the transport refused to create new connections — blocking **all** S3 traffic indefinitely. Cassandra uses a separate connection pool (gocql), so it was unaffected.

**Evidence**:
- Structured log: `{"status":500,"body_size":33}` → matches `{"error":"failed to store block"}` or `{"error":"failed to upload file"}`
- Login/logout, library creation worked (Cassandra path) — only S3 operations failed
- `docker-compose down && docker-compose build && docker-compose up` fixed it (fresh HTTP transport with new connections)

**Fix** (commit TBD):
Changed `internal/storage/s3.go` HTTP transport settings:

| Setting | Before | After | Why |
|---------|--------|-------|-----|
| `MaxConnsPerHost` | `64` | `0` (unlimited) | **Key fix.** Zombie connections can't block new ones |
| `IdleConnTimeout` | `120s` | `30s` | Detect and discard stale connections 4x faster |
| `TLSHandshakeTimeout` | not set | `5s` | Prevents hung TLS negotiations from blocking forever |
| `ExpectContinueTimeout` | not set | `1s` | For PUT/POST, validates S3 accepts before sending body |
| `ForceAttemptHTTP2` | not set | `true` | HTTP/2 multiplexing — better throughput, more resilient |

**Files Changed**: `internal/storage/s3.go` (transport config only — no API changes)

---

### ISSUE-UPLOAD-REPLACE-01: Upload "Don't Replace" Doesn't Work (Desktop Client + Web)

**Status**: 🟡 Pending
**Discovered**: 2026-03-04
**Severity**: Medium — files get silently overwritten when user explicitly chooses not to replace

**Problem**: When uploading a file that already exists:
- **Desktop client file browser**: Shows dialog "¿Desea reemplazarlo? (Elija No para subirlo con un nombre alternativo)". Clicking "No" should auto-rename but still overwrites.
- **Web UI**: Shows "Replace / Don't replace / Cancel" dialog. "Don't replace" should auto-rename but still overwrites.

**Root Cause**: The Seafile desktop client distinguishes "replace" vs "don't replace" by which endpoint it calls:
- "Sí" (replace) → `GET /api2/repos/{id}/update-link` → upload
- "No" (don't replace) → `GET /api2/repos/{id}/upload-link` → upload

Both endpoints (`update-link` and `upload-link`) map to the same handler `GetUploadLink` and create identical tokens. The server has no way to know which endpoint was used when the upload arrives.

The client also sends `replace=1` in both cases, so the form parameter doesn't help.

**Backend Infrastructure Ready**:
- `autoRenameIfExists()` function generates unique names: `file (1).txt`, `file (2).txt`, etc.
- `replace` parameter propagated through entire chain: `HandleUpload` → `finalizeUploadStreaming` → `commitUploadedFileMultiBlock` → `addFileToDirectory` → `traverseAndAddFile`
- All commit/directory functions return `actualFilename` (may differ if auto-renamed)
- Currently defaults to `replace=1` (overwrite) for backward compatibility

**Fix Required**:
1. Add `Replace bool` field to `AccessToken` struct
2. Create `CreateUpdateToken()` that sets `Replace=true` (for `update-link` endpoint)
3. Keep `CreateUploadToken()` with `Replace=false` (for `upload-link` endpoint)
4. Add `GetUpdateLink` handler that uses `CreateUpdateToken`
5. In `HandleUpload`: use `token.Replace` as default, allow form param to override
6. Frontend: "Don't replace" button should send `replace=0` in formData
7. Update `TokenStore` interface, `CassandraTokenAdapter`, `db.TokenStore`, and test mocks

**Files to Modify**:
- `internal/api/seafhttp.go` — AccessToken, TokenStore interface, TokenManager, HandleUpload
- `internal/api/token_adapter.go` — CassandraTokenAdapter (track update tokens in-memory)
- `internal/api/v2/files.go` — Add GetUpdateLink handler, route update-link to it
- `internal/db/tokens.go` — Add CreateUpdateToken, update TokenCreator interface
- `frontend/src/components/file-uploader/file-uploader.js` — Send `replace=0` in uploadFile()
- Test files: `seafhttp_test.go`, `fileview_test.go` — Update mocks

---

## ~~ISSUE-FRONTEND-ORG-DELETE-01~~: Superadmin Org Soft-Delete/Restore UI — ✅ RESOLVED

**Status**: ✅ Complete (2026-03-25)
**Date identified**: 2026-03-18 | **Date resolved**: 2026-03-25

Fully implemented in `frontend/src/pages/sys-admin/orgs/orgs-content.js`, `orgs.js`, and `search-orgs.js`:
- Status column with color-coded badges (active/deactivated/deleted)
- Separate Deactivate, Delete, Reactivate, and Restore actions with confirmation dialogs
- Status filter support in org listing
- Search results also support all lifecycle actions

---

## See Also

- [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md) - Component completion status
- [API-REFERENCE.md](API-REFERENCE.md) - API endpoint documentation
- [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md) - Architectural issues
- [CURRENT_WORK.md](../CURRENT_WORK.md) - Active priorities
