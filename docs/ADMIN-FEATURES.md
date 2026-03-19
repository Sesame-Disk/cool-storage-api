# Admin Features — Library Management, Link Management, Org Admin, Audit Logs

**Last Updated**: 2026-03-11
**Status**: Library Management ✅ DONE, Link Management ✅ DONE, Sharing Stubs ✅ DONE, Org Management ✅ DONE, Org Admin Panel ✅ DONE, Superadmin Departments ✅ DONE, Custom Share Permissions ✅ DONE, Audit Logs pending

---

## Overview

Three admin feature areas needed for production. The OIDC provider manages users, groups, departments, and tenants — so the SesameFS admin panel focuses on **storage-specific management** that only SesameFS can provide.

| Feature | Backend | Frontend | Database | Priority |
|---------|---------|----------|----------|----------|
| Admin Library Management | ✅ Complete (2026-02-12) | ✅ Exists | ✅ Exists | DONE |
| Admin Share Link & Upload Link Management | ✅ Complete (2026-02-12) | ✅ Exists | ✅ Exists | DONE |
| Admin Organization Management | ✅ Complete (2026-03-18) | 🟡 Needs update (soft-delete/restore UI) | ✅ Exists | DONE (backend), frontend TODO |
| Admin User Management | ✅ Complete (2026-02-23) | ✅ Exists | ✅ Exists | DONE |
| Custom Share Permissions | ✅ Complete (2026-03-11) | ✅ Exists | ✅ Exists | DONE |
| Audit Logs | 🟡 Stub only | ✅ Exists (unused) | ❌ Missing | MEDIUM |

---

## 0. Superadmin Bootstrap — ✅ IMPLEMENTED (2026-02-23)

### The Problem

`RequireSuperAdmin()` middleware requires BOTH:
1. `role == "superadmin"`
2. `org_id == "00000000-0000-0000-0000-000000000000"` (platform org)

OIDC users are provisioned into their tenant org, not the platform org. Even with `OIDC_DEFAULT_ROLE=superadmin`, they get 403 on org management endpoints.

### Solution A: FIRST_SUPERADMIN_EMAIL (recommended for fresh deploys)

Set in `.env` **before first deploy**:
```bash
FIRST_SUPERADMIN_EMAIL=you@yourdomain.com
```

On first startup, the seed creates a superadmin in the platform org with this email.
When the user logs in via OIDC, they are matched by email and enter as superadmin.
The seed only runs once — changing this value after the first deploy has no effect.

### Solution B: make-superadmin.sh (for existing deploys)

**File**: `scripts/make-superadmin.sh`

Directly writes to Cassandra to place a user in the platform org with superadmin role.

```bash
# Dev/test (docker-compose.yaml):
./scripts/make-superadmin.sh your@email.com "Your Name"

# Production (docker-compose.prod.yml):
./scripts/make-superadmin.sh -f docker-compose.prod.yml your@email.com "Your Name"
```

Run from the project root. The script uses `docker compose exec cassandra cqlsh` internally — no DB credentials needed unless Cassandra auth is enabled (pass `--username`/`--password` or set `CASSANDRA_USERNAME`/`CASSANDRA_PASSWORD` env vars).

**What it does:**
1. Looks up user by email in `users_by_email` (reuses existing user_id if found)
2. Upserts user record in platform org with `role=superadmin`, unlimited quota
3. Updates `users_by_email` to map email → platform org
4. Updates `users_by_oidc` to map OIDC subject → platform org
5. Invalidates existing sessions so new role takes effect on next login

### Organization Management Endpoints

| Method | Endpoint | Handler | Auth Required |
|--------|----------|---------|---------------|
| GET | `/admin/organizations/` | `ListOrganizations` | admin or superadmin (read-only for admin) |
| POST | `/admin/organizations/` | `CreateOrganization` | **superadmin only** |
| GET | `/admin/organizations/:org_id/` | `GetOrganization` | admin or superadmin |
| PUT | `/admin/organizations/:org_id/` | `UpdateOrganization` | **superadmin only** |
| DELETE | `/admin/organizations/:org_id/` | `SoftDeleteOrganization` | **superadmin only** — sets `status="deleted"`, starts grace period |
| POST | `/admin/organizations/:org_id/delete/` | `SoftDeleteOrganization` | **superadmin only** — alias for DELETE (backward compat) |
| POST | `/admin/organizations/:org_id/deactivate/` | `DeactivateOrganization` | **superadmin only** — sets `status="deactivated"` (reversible, no GC) |
| POST | `/admin/organizations/:org_id/reactivate/` | `ReactivateOrganization` | **superadmin only** — restores deactivated org to `status="active"` |
| POST | `/admin/organizations/:org_id/restore/` | `RestoreOrganization` | **superadmin only** — restores deleted org within grace period |
| GET | `/admin/organizations/:org_id/users/` | `ListOrgUsers` | admin or superadmin |
| POST | `/admin/organizations/:org_id/users/` | `AdminAddOrgUser` | admin or superadmin |
| PUT | `/admin/organizations/:org_id/users/:email/` | `AdminUpdateOrgUser` | admin or superadmin |
| DELETE | `/admin/organizations/:org_id/users/:email/` | `AdminDeleteOrgUser` | admin or superadmin |

### Organization Lifecycle — ✅ COMPLETE (2026-03-18, updated 2026-03-19)

Three distinct org states tracked via the dedicated `status` column (separate from `settings`):

| State | Trigger | Reversible | GC Cascade |
|-------|---------|------------|------------|
| **active** | Default / `ReactivateOrganization` / `RestoreOrganization` | — | No |
| **deactivated** | `DeactivateOrganization` (POST .../deactivate/) | Yes via `ReactivateOrganization` (POST .../reactivate/) | No |
| **deleted** | `SoftDeleteOrganization` (DELETE) | Yes within grace period via `RestoreOrganization` (POST .../restore/) | Yes — after `OrgGraceDays` (default 30 days) |

**Note (2026-03-19)**: Org status is now stored in a dedicated `status` column on the `organizations` table instead of `settings['status']`. This mirrors the user model where `status` (lifecycle state) is separated from `role` (permission level). A backfill migration automatically converts existing `settings['status']` values to the new column.

**Cascade flow** (after grace period expires):
1. GC Scanner Phase 12 finds org with `deleted_at` past `OrgGraceDays`
2. Enqueues `ItemOrgCascade` → Worker `processOrgCascade`:
   - All libraries → enqueued as `ItemLibraryCascade` (content cleanup)
   - All users → shares, starred, monitored cleaned + hard-deleted
   - All groups → `DeleteGroupFull` (members, by_member, by_id, record)
   - Org record → hard-deleted from `organizations` table
   - Audit log entry written

**Remaining frontend TODO** (see ISSUE-FRONTEND-ORG-DELETE-01 in KNOWN_ISSUES.md):
- Superadmin dashboard: "Delete" button (distinct from "Deactivate"), "Restore" button, status column
- `ListOrganizations` should filter by status (active/deactivated/deleted)
- Org admin dashboard: warning banner for orgs in "deleted" state

---

### CreateOrganization — seafile-js Compatibility (2026-02-23)

The frontend `sysAdminAddOrg(orgName, ownerEmail, password)` (seafile-js) sends FormData.
Backend now accepts both formats:

**FormData** (seafile-js native):
```
org_name=Acme Corp
owner_email=alice@acme.com
password=ignored  ← accepted but not used (OIDC-only system)
```

**JSON** (direct API calls):
```json
{ "name": "Acme Corp", "owner_email": "alice@acme.com", "storage_quota": 1099511627776 }
```

If `owner_email` is provided, an admin user is created in the new org (dual-write to
`users` + `users_by_email` with `IF NOT EXISTS` to avoid overwriting existing OIDC sessions).

---

## 1.5. Admin User Management — ✅ IMPLEMENTED (2026-02-23)

### Endpoints

| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/admin/users/` | `ListAllUsers` | Paginated. Superadmin sees ALL orgs; tenant admin sees own org |
| POST | `/admin/users/` | `AdminCreateUser` | Creates user in caller's org. Dual-writes to `users` + `users_by_email` |
| GET | `/admin/users/:email/` | `GetUserByEmail` | Resolves via `users_by_email` table |
| PUT | `/admin/users/:email/` | `UpdateUser` | Update role, quota, name |
| DELETE | `/admin/users/:email/` | `DeleteUserByEmail` | Soft-delete (sets `status="deleted"`, `deleted_at=now()`, starts 7-day grace period) |
| GET | `/admin/admins/` | `ListAdminUsers` | Lists admin+superadmin users. Response key: `admin_user_list` |
| GET | `/admin/search-user/` | `SearchUsers` | Search by email or name substring |

### Multi-Org Behavior (2026-02-23 fix)

- **Superadmin**: `ListAllUsers`, `ListAdminUsers`, `SearchUsers` query ALL orgs by iterating `SELECT org_id FROM organizations` + platform org. Results deduplicated by email.
- **Tenant admin**: Only sees users in their own org.
- Same pattern used by `AdminListAllLibraries`.

### `users_by_email` Dual-Write (2026-02-23 fix)

All user creation paths now write to BOTH `users` AND `users_by_email`:
- `AdminCreateUser` — already had dual-write
- `CreateOrganization` (owner) — already had dual-write
- `AdminAddOrgUser` — **fixed**: was missing `users_by_email` insert
- OIDC `createUser` — **fixed**: was missing `users_by_email` insert
- Seed data — already had dual-write

### Frontend API Functions

13 `sysAdmin*` functions added to `frontend/src/utils/seafile-api.js`:
`sysAdminListUsers`, `sysAdminListAdmins`, `sysAdminGetUser`, `sysAdminUpdateUser`, `sysAdminDeleteUser`, `sysAdminAddUser`, `sysAdminSearchUsers`, `sysAdminBatchDeleteUsers`, `sysAdminSetUserQuotaInBatch`, `sysAdminImportUsers`, `sysAdminSetAdminUsers`, `sysAdminListUserRepos`, `sysAdminListUserSharedRepos`

---

## 1. Admin Library Management — ✅ IMPLEMENTED (2026-02-12)

### Implementation

- **File**: `internal/api/v2/admin.go` — Phase 3 section (after user/group admin endpoints)
- **Frontend API**: `frontend/src/utils/seafile-api.js` — all `sysAdmin*Repo*` methods wired
- **Frontend pages**: `frontend/src/pages/sys-admin/repos/` — `all-repos.js`, `search-repos.js`, `repos.js`, `trash-repos.js`, `dir-view.js`

### Endpoints Implemented

| Method | Endpoint | Handler | Status |
|--------|----------|---------|--------|
| GET | `/admin/libraries/` | `AdminListAllLibraries` | ✅ `?page=&per_page=&order_by=` |
| GET | `/admin/search-libraries/` | `AdminSearchLibraries` | ✅ `?name_or_id=&page=&per_page=` |
| GET | `/admin/libraries/:library_id/` | `AdminGetLibrary` | ✅ |
| POST | `/admin/libraries/` | `AdminCreateLibrary` | ✅ JSON `{name, owner}` |
| DELETE | `/admin/libraries/:library_id/` | `AdminDeleteLibrary` | ✅ Soft-delete |
| PUT | `/admin/libraries/:library_id/transfer/` | `AdminTransferLibrary` | ✅ JSON `{owner}` |
| GET | `/admin/libraries/:library_id/dirents/` | `AdminListDirents` | ✅ `?path=` |
| GET | `/admin/libraries/:library_id/history-setting/` | `AdminGetHistorySetting` | ✅ |
| PUT | `/admin/libraries/:library_id/history-setting/` | `AdminUpdateHistorySetting` | ✅ JSON `{keep_days}` |
| GET | `/admin/libraries/:library_id/shared-items/` | `AdminListSharedItems` | ✅ `?share_type=user\|group` |
| GET | `/admin/trash-libraries/` | `AdminListTrashLibraries` | ✅ `?page=&per_page=&owner=` |
| DELETE | `/admin/trash-libraries/` | `AdminCleanTrashLibraries` | ✅ Permanently deletes all soft-deleted libs; superadmin cleans all orgs |

### Key Design Decisions

- **No permission filter**: Admin sees ALL libraries in their org; superadmin sees ALL libraries across all orgs
- **Library lookup**: `libraries` table (partitioned by `org_id`) for listing, `libraries_by_id` for single lookups
- **Transfer**: Dual-write to `libraries` + `libraries_by_id` tables
- **Search**: Application-level case-insensitive substring match + ID prefix match
- **Delete**: Soft-delete via `deleted_at` / `deleted_by` columns (same pattern as user delete)
- **JSON + FormData**: Create and transfer endpoints accept both content types
- **Clean trash**: `AdminCleanTrashLibraries` calls the same cleanup chain as `PermanentDeleteRepo` — GC enqueue (blocks/commits/fs_objects), tag cleanup, hard-delete of library rows. Returns `{"success": true, "cleaned": N}`.
- **Known gap**: `shares` for deleted libraries are not cleaned up yet. Share/upload links are now cleaned via `share_links_by_library` (2026-03-13). See `docs/TECHNICAL-DEBT.md` § 9 and `KNOWN_ISSUES.md` ISSUE-GC-ORPHANS-01.

---

## 2. Admin Share Link & Upload Link Management — ✅ IMPLEMENTED (2026-02-12)

### Implementation

- **Admin share/upload handlers**: `internal/api/v2/admin_extra.go` — AdminListShareLinks, AdminDeleteShareLink, AdminListUploadLinks, AdminDeleteUploadLink, AdminListUserShareLinks, AdminListUserUploadLinks. All use unified `share_links` tables (quad-delete via `deleteShareLink` helper).
- **User upload link CRUD**: `internal/api/v2/upload_links.go` — ListUploadLinks, CreateUploadLink, DeleteUploadLink, ListRepoUploadLinks. Uses unified `share_links` tables via `ShareLinkHandler` helpers.
- **Database**: `internal/db/db.go` — Unified into 4 tables: `share_links`, `share_links_by_creator`, `share_links_by_org`, `share_links_by_library`. See `docs/SHARE-LINKS-UNIFICATION.md` for full schema.
- **Route registration**: `internal/api/server.go` — `RegisterUploadLinkRoutes`, `RegisterShareLinkRoutes`
- **Frontend API**: `frontend/src/utils/seafile-api.js` — Added 6 sysAdmin methods

### What Was Built

#### Admin Share Link Endpoints — ✅ DONE

| Method | Endpoint | Handler | seafile-js method | Status |
|--------|----------|---------|-------------------|--------|
| GET | `/admin/share-links/` | `AdminListShareLinks` | `sysAdminListShareLinks` | ✅ Single-partition query on `share_links_by_org` |
| DELETE | `/admin/share-links/:token/` | `AdminDeleteShareLink` | `sysAdminDeleteShareLink` | ✅ Quad-delete via `deleteShareLink` helper |

#### Upload Links — Full Feature (User + Admin) — ✅ DONE

**Database**: Upload links are stored in the unified `share_links` tables with `link_type = 'upload'`. See `docs/SHARE-LINKS-UNIFICATION.md` for full schema.

**User endpoints** (file `internal/api/v2/upload_links.go`):

| Method | Endpoint | Handler | Status |
|--------|----------|---------|--------|
| GET | `/api/v2.1/upload-links/` | `ListUploadLinks` | ✅ User's own upload links (optional `?repo_id=` filter) |
| POST | `/api/v2.1/upload-links/` | `CreateUploadLink` | ✅ Creates token, dual-writes, optional password+expiry |
| DELETE | `/api/v2.1/upload-links/:token/` | `DeleteUploadLink` | ✅ Verifies ownership, dual-deletes |
| GET | `/api/v2.1/repos/:repo_id/upload-links/` | `ListRepoUploadLinks` | ✅ List upload links for specific repo |

**Admin endpoints** (in `internal/api/v2/admin_extra.go`):

| Method | Endpoint | Handler | seafile-js method | Status |
|--------|----------|---------|-------------------|--------|
| GET | `/admin/upload-links/` | `AdminListUploadLinks` | `sysAdminListAllUploadLinks` | ✅ |
| DELETE | `/admin/upload-links/:token/` | `AdminDeleteUploadLink` | `sysAdminDeleteUploadLink` | ✅ |

#### Admin Per-User Link Endpoints — ✅ DONE

| Method | Endpoint | Handler | seafile-js method | Status |
|--------|----------|---------|-------------------|--------|
| GET | `/admin/users/:email/share-links/` | `AdminListUserShareLinks` | `sysAdminListShareLinksByUser` | ✅ Resolves email→user_id, queries share_links_by_creator |
| GET | `/admin/users/:email/upload-links/` | `AdminListUserUploadLinks` | `sysAdminListUploadLinksByUser` | ✅ Resolves email→user_id, queries upload_links_by_creator |

### Response Formats

**AdminListShareLinks**:
```json
{
  "share_link_list": [
    {
      "token": "abc123",
      "repo_id": "uuid",
      "repo_name": "Library Name",
      "path": "/path/to/file.pdf",
      "creator_email": "user@example.com",
      "creator_name": "User Name",
      "ctime": "2026-01-01T00:00:00Z",
      "expire_date": "2026-02-01T00:00:00Z",
      "is_expired": false,
      "view_cnt": 5,
      "permissions": {"can_download": true, "can_edit": false}
    }
  ],
  "page_info": {"has_next_page": false, "current_page": 1}
}
```

### Implementation Notes

- **Admin share link listing**: Queries `share_links` table (full scan) with application-level pagination. Resolves repo names from `libraries` table (not `libraries_by_id` which lacks `name` column). Caches user lookups to avoid N+1 queries.
- **Admin delete**: Uses read-first-then-batch-delete pattern — reads `created_by`+`org_id` from `share_links`/`upload_links`, then issues `gocql.LoggedBatch` to delete from both primary and lookup tables.
- **Upload links**: Full feature implemented — DB tables auto-created via migration, user CRUD with dual-write pattern, admin list/delete with same batch-delete pattern.
- **Future**: Upload handler at `/seafhttp/upload-api/:token` should also accept upload link tokens for anonymous file uploads via link.

---

## 2.5. Custom Share Permissions — ✅ IMPLEMENTED (2026-03-11)

### Overview

Custom share permissions allow library owners to define fine-grained access controls beyond standard permission levels (`rw`, `r`, `cloud-edit`, `preview`). Each custom permission is a set of 8 boolean flags that can be individually toggled.

### Endpoints

| Method | Endpoint | Handler | Purpose |
|--------|----------|---------|---------|
| GET | `/repos/:repo_id/custom-share-permissions/` | `ListCustomSharePermissions` | List custom permissions created by current user |
| GET | `/repos/:repo_id/custom-share-permissions/:perm_id/` | `GetCustomSharePermission` | Get single custom permission by UUID |
| POST | `/repos/:repo_id/custom-share-permissions/` | `CreateCustomSharePermission` | Create new custom permission with flags |
| PUT | `/repos/:repo_id/custom-share-permissions/:perm_id/` | `UpdateCustomSharePermission` | Update permission flags (owner only) |
| DELETE | `/repos/:repo_id/custom-share-permissions/:perm_id/` | `DeleteCustomSharePermission` | Delete custom permission (owner only) |

### Permission Flags

8 granular flags: `upload`, `download`, `create`, `modify`, `copy`, `delete`, `preview`, `download_external_link`.

### Database

- `custom_share_permissions` — Primary table (key: `permission_id` UUID)
- `custom_share_permissions_by_user` — Lookup by creator (key: `creator_id`, `permission_id`)
- Both tables auto-created via migration in `db.go`

### Integration

- Share endpoints now accept custom permission UUIDs as the `permission` field (in addition to standard strings)
- `ShareResponse` includes `permission_name` for display
- File operation handlers use `RequirePermFlag()` to enforce granular flags
- See [ROLES-AND-PERMISSIONS.md](ROLES-AND-PERMISSIONS.md) for full flag mapping details

### Files

- `internal/api/v2/file_shares.go:1092-1320` — CRUD handlers
- `internal/middleware/permissions.go:257-740` — `PermissionFlags`, `GetLibraryPermissionWithFlags()`, `RequirePermFlag()`
- `internal/middleware/permissions_test.go` — 366 lines of flag tests
- `internal/api/v2/libraries.go:178-187` — Route registration

---

## 3. Audit Logs

### What Exists

- **Middleware**: `internal/middleware/audit.go` — defines action types and basic structure, but **only logs to console** (no database persistence)
- **Stub endpoint**: `GET /activities` returns empty `{"events": []}` (in `internal/api/server.go:1173`)
- **Frontend pages**: Full UI in `frontend/src/pages/sys-admin/logs-page/` — `login-logs.js`, `file-access-logs.js`, `file-update-logs.js`, `share-permission-logs.js`
- **Dashboard**: `frontend/src/pages/dashboard/files-activities.js` — calls `seafileAPI.listActivities()`
- **seafile-js methods**: `sysAdminListLoginLogs`, `sysAdminListFileAccessLogs`, `sysAdminListFileUpdateLogs`, `sysAdminListSharePermissionLogs`, `sysAdminListAdminLogs`, `listActivities`
- **API-REFERENCE.md**: Documents planned `activities` table schema (line 627) but never implemented

### What's Missing — Everything

#### Database Tables

5 new Cassandra tables needed (all use TIMEUUID clustering for time-ordered queries):

```cql
-- 1. Login logs
CREATE TABLE login_logs (
    org_id UUID,
    log_id TIMEUUID,
    user_id UUID,
    email TEXT,
    name TEXT,
    login_ip TEXT,
    user_agent TEXT,
    success BOOLEAN,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), log_id)
) WITH CLUSTERING ORDER BY (log_id DESC)
  AND default_time_to_live = 7776000;  -- 90-day TTL

-- 2. File access logs (downloads, previews)
CREATE TABLE file_access_logs (
    org_id UUID,
    log_id TIMEUUID,
    user_id UUID,
    email TEXT,
    name TEXT,
    repo_id UUID,
    repo_name TEXT,
    file_path TEXT,
    event_type TEXT,  -- 'download', 'preview', 'view'
    ip TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), log_id)
) WITH CLUSTERING ORDER BY (log_id DESC)
  AND default_time_to_live = 7776000;

-- 3. File update logs (create, edit, delete, move, rename)
CREATE TABLE file_update_logs (
    org_id UUID,
    log_id TIMEUUID,
    user_id UUID,
    email TEXT,
    name TEXT,
    repo_id UUID,
    repo_name TEXT,
    file_path TEXT,
    operation TEXT,  -- 'create', 'edit', 'delete', 'move', 'rename'
    detail TEXT,     -- e.g. "Renamed from old.txt to new.txt"
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), log_id)
) WITH CLUSTERING ORDER BY (log_id DESC)
  AND default_time_to_live = 7776000;

-- 4. Permission/share audit logs
CREATE TABLE permission_audit_logs (
    org_id UUID,
    log_id TIMEUUID,
    user_id UUID,
    email TEXT,
    name TEXT,
    repo_id UUID,
    repo_name TEXT,
    operation TEXT,  -- 'share-library', 'unshare-library', 'create-share-link', 'delete-share-link'
    target TEXT,     -- email, group name, or link token
    target_type TEXT, -- 'user', 'group', 'link'
    permission TEXT,  -- 'r', 'rw', 'admin'
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), log_id)
) WITH CLUSTERING ORDER BY (log_id DESC)
  AND default_time_to_live = 7776000;

-- 5. Activities feed (aggregated for dashboard)
CREATE TABLE activities (
    org_id UUID,
    activity_id TIMEUUID,
    user_id UUID,
    author_email TEXT,
    author_name TEXT,
    repo_id UUID,
    repo_name TEXT,
    path TEXT,
    obj_type TEXT,   -- 'file', 'dir', 'repo'
    op_type TEXT,    -- 'create', 'edit', 'delete', 'rename', 'move', 'recover'
    old_path TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), activity_id)
) WITH CLUSTERING ORDER BY (activity_id DESC)
  AND default_time_to_live = 7776000;
```

#### Backend Endpoints

**Admin log endpoints** (new file `internal/api/v2/audit.go`):

| Method | Endpoint | Handler | seafile-js method | Notes |
|--------|----------|---------|-------------------|-------|
| GET | `/admin/logs/login/` | `ListLoginLogs` | `sysAdminListLoginLogs` | `?page=&per_page=` |
| GET | `/admin/logs/file-access/` | `ListFileAccessLogs` | `sysAdminListFileAccessLogs` | `?page=&per_page=&email=&repo_id=` |
| GET | `/admin/logs/file-update/` | `ListFileUpdateLogs` | `sysAdminListFileUpdateLogs` | `?page=&per_page=` |
| GET | `/admin/logs/share-permission/` | `ListPermissionAuditLogs` | `sysAdminListSharePermissionLogs` | `?page=&per_page=` |

**User activity endpoint** (replace stub in `server.go`):

| Method | Endpoint | Handler | seafile-js method | Notes |
|--------|----------|---------|-------------------|-------|
| GET | `/api/v2.1/activities/` | `ListActivities` | `listActivities` | `?page=&per_page=` — user's dashboard feed |

#### Logging Integration Points

Where to insert audit log writes in existing handlers:

| Event | File | Handler | Log Table |
|-------|------|---------|-----------|
| Login (OIDC) | `internal/auth/oidc.go` | `provisionUser()` | `login_logs` |
| Login (dev token) | `internal/middleware/auth.go` | auth middleware | `login_logs` |
| File download | `internal/api/seafhttp.go` | `HandleDownload()` | `file_access_logs` |
| File preview/view | `internal/api/v2/files.go` | `GetFileDetail()` | `file_access_logs` |
| File upload | `internal/api/seafhttp.go` | upload handler | `file_update_logs` + `activities` |
| File create | `internal/api/v2/files.go` | `CreateFile()` | `file_update_logs` + `activities` |
| File delete | `internal/api/v2/files.go` | `DeleteFile()` | `file_update_logs` + `activities` |
| File rename | `internal/api/v2/files.go` | `RenameFile()` | `file_update_logs` + `activities` |
| File move/copy | `internal/api/v2/files.go` | move/copy handlers | `file_update_logs` + `activities` |
| Share to user/group | `internal/api/v2/file_shares.go` | share handlers | `permission_audit_logs` |
| Create share link | `internal/api/v2/share_links.go` | `CreateShareLink()` | `permission_audit_logs` |
| Delete share link | `internal/api/v2/share_links.go` | `DeleteShareLink()` | `permission_audit_logs` |
| Library create | `internal/api/v2/libraries.go` | `CreateLibrary()` | `activities` |
| Library delete | `internal/api/v2/libraries.go` | `DeleteLibrary()` | `activities` |

#### Response Formats

**ListLoginLogs**:
```json
{
  "login_log_list": [
    {
      "email": "user@example.com",
      "name": "User Name",
      "login_ip": "192.168.1.1",
      "login_time": "2026-02-01T10:30:00Z",
      "login_success": true
    }
  ],
  "total_count": 150,
  "page_info": {"has_next_page": true, "current_page": 1}
}
```

**ListActivities** (dashboard feed):
```json
{
  "events": [
    {
      "author_email": "user@example.com",
      "author_name": "User Name",
      "author_contact_email": "user@example.com",
      "avatar_url": "",
      "repo_id": "uuid",
      "repo_name": "My Library",
      "obj_type": "file",
      "op_type": "edit",
      "path": "/documents/report.docx",
      "time": "2026-02-01T14:30:00Z"
    }
  ]
}
```

### Implementation Notes

- **Async writes**: Audit log writes should NOT block request handling. Use a buffered channel + goroutine worker to write logs asynchronously.
- **TTL**: All tables use 90-day TTL by default. Make configurable via `config.yaml`.
- **Update existing middleware**: `internal/middleware/audit.go` already defines action types — refactor to use DB persistence instead of console logging.
- **Performance**: TIMEUUID clustering gives natural time ordering. Cassandra handles high write throughput well.

---

## 4. Superadmin Panel — Departments, Address Book, Group-Owned Libraries — ✅ IMPLEMENTED (2026-03-05)

### Implementation

- **Route registration**: `internal/api/v2/admin.go` — RegisterAdminRoutes
- **Handler implementations**: `internal/api/v2/admin_extra.go`

### Endpoints Implemented

| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/admin/organizations/:org_id/departments/` | `AdminListOrgDepartments` | Filters `is_department=true` groups for target org |
| GET | `/admin/address-book/groups/` | `AdminListAddressBookGroups` | Lists departments for caller's org |
| POST | `/admin/address-book/groups/` | `AdminAddAddressBookGroup` | FormData: `parent_group`, `group_name`, `group_owner`, `group_staff` |
| GET | `/admin/address-book/groups/:group_id/` | `AdminGetAddressBookGroup` | Supports `?return_ancestors=true` for hierarchy |
| PUT | `/admin/address-book/groups/:group_id/` | `AdminUpdateAddressBookGroup` | FormData: `group_name` |
| DELETE | `/admin/address-book/groups/:group_id/` | `AdminDeleteAddressBookGroup` | Deletes group + members via `groups_by_member` cleanup |
| POST | `/admin/groups/:group_id/group-owned-libraries/` | `AdminAddGroupOwnedLibrary` | Creates library + `libraries_by_id` + share to group ("rw") |
| DELETE | `/admin/groups/:group_id/group-owned-libraries/:library_id/` | `AdminDeleteGroupOwnedLibrary` | Soft-delete via `deleted_at`/`deleted_by` |

---

## 5. Org Admin Panel — ✅ IMPLEMENTED (2026-03-05)

### Implementation

- **File**: `internal/api/v2/org_admin.go`
- **Route registration**: `RegisterOrgAdminRoutes` in `org_admin.go`
- **Auth pattern**: `requireOrgAccess()` middleware validates org admin role

### Route Groups

The org admin uses two route groups:
- **`/api/v2.1/org/admin/`** — Endpoints without org_id in URL (org from JWT): links, upload-links, logs
- **`/api/v2.1/org/:org_id/admin/`** — Endpoints with org_id in URL: users, groups, repos, trash, etc.

### Fully Implemented Endpoints

#### Org Info (2 endpoints)
| Method | Endpoint | Handler |
|--------|----------|---------|
| GET | `/org/admin/info/` | `GetOrgInfo` |
| PUT | `/org/admin/info/` | `UpdateOrgInfo` |

#### Users (12 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/:org_id/admin/users/` | `ListOrgUsers` | Paginated, batch user resolution |
| POST | `/org/:org_id/admin/users/` | `AddOrgUser` | Creates user in org |
| GET | `/org/:org_id/admin/users/:email/` | `GetOrgUser` | |
| PUT | `/org/:org_id/admin/users/:email/` | `UpdateOrgUser` | Role, quota, name |
| DELETE | `/org/:org_id/admin/users/:email/` | `DeleteOrgUser` | |
| PUT | `/org/:org_id/admin/users/:email/set-password/` | `ResetOrgUserPassword` | |
| GET | `/org/:org_id/admin/users/:email/repos/` | `GetOrgUserOwnedRepos` | |
| GET | `/org/:org_id/admin/users/:email/beshared-repos/` | `GetOrgUserBesharedRepos` | |
| GET | `/org/:org_id/admin/search-user/` | `SearchOrgUser` | |
| POST | `/org/:org_id/admin/import-users/` | `ImportOrgUsers` | CSV import |
| POST | `/org/:org_id/admin/invite-users/` | `InviteOrgUsers` | Email invitations |

#### Groups (8 endpoints + members 4 + libraries 1)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/:org_id/admin/groups/` | `ListOrgGroups` | Batch user resolution via `resolveUsersMap` |
| GET | `/org/:org_id/admin/groups/:gid/` | `GetOrgGroup` | |
| PUT | `/org/:org_id/admin/groups/:gid/` | `UpdateOrgGroup` | Supports quota via org settings |
| DELETE | `/org/:org_id/admin/groups/:gid/` | `DeleteOrgGroup` | |
| GET | `/org/:org_id/admin/groups/:gid/members/` | `ListOrgGroupMembers` | |
| POST | `/org/:org_id/admin/groups/:gid/members/` | `AddOrgGroupMember` | |
| DELETE | `/org/:org_id/admin/groups/:gid/members/:email/` | `DeleteOrgGroupMember` | |
| PUT | `/org/:org_id/admin/groups/:gid/members/:email/` | `UpdateOrgGroupMember` | Change role |
| GET | `/org/:org_id/admin/groups/:gid/libraries/` | `ListOrgGroupLibraries` | No ALLOW FILTERING |
| GET | `/org/:org_id/admin/search-group/` | `SearchOrgGroup` | |

#### Group Owned Libraries (2 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| POST | `/org/:org_id/admin/groups/:gid/group-owned-libraries/` | `AddOrgGroupOwnedLibrary` | Creates lib + share to group |
| DELETE | `/org/:org_id/admin/groups/:gid/group-owned-libraries/:rid/` | `DeleteOrgGroupOwnedLibrary` | Soft-delete |

#### Repositories (4 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/:org_id/admin/repos/` | `ListOrgRepos` | `sort.Slice` for order_by |
| DELETE | `/org/:org_id/admin/repos/:rid/` | `DeleteOrgRepo` | Soft-delete |
| PUT | `/org/:org_id/admin/repos/:rid/` | `TransferOrgRepo` | Change owner |
| GET | `/org/:org_id/admin/repos/:rid/dirents/` | `ListOrgRepoDirents` | fs_objects traversal |

#### Trash Libraries (4 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/:org_id/admin/trash-libraries/` | `ListOrgTrashLibraries` | |
| DELETE | `/org/:org_id/admin/trash-libraries/` | `CleanOrgTrashLibraries` | Permanent delete all |
| DELETE | `/org/:org_id/admin/trash-libraries/:rid/` | `DeleteOrgTrashLibrary` | Delete single |
| PUT | `/org/:org_id/admin/trash-libraries/:rid/` | `RestoreOrgTrashLibrary` | Restore |

#### Departments (1 endpoint)
| Method | Endpoint | Handler |
|--------|----------|---------|
| GET | `/org/:org_id/admin/departments/` | `ListOrgDepartments` |

#### Address Book Groups (5 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/:org_id/admin/address-book/groups/` | `ListOrgAddressBookGroups` | |
| POST | `/org/:org_id/admin/address-book/groups/` | `AddOrgAddressBookGroup` | With parent_group, owner, staff |
| GET | `/org/:org_id/admin/address-book/groups/:gid/` | `GetOrgAddressBookGroup` | Supports ancestors |
| PUT | `/org/:org_id/admin/address-book/groups/:gid/` | `UpdateOrgAddressBookGroup` | |
| DELETE | `/org/:org_id/admin/address-book/groups/:gid/` | `DeleteOrgAddressBookGroup` | |

#### Share Links (2 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/admin/links/` | `ListOrgLinks` | Iterates `share_links_by_creator` per user |
| DELETE | `/org/admin/links/:token/` | `DeleteOrgLink` | Verifies org ownership, dual-delete |

#### Upload Links (2 endpoints)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/admin/upload-links/` | `ListOrgUploadLinks` | Iterates `upload_links_by_creator` per user |
| DELETE | `/org/admin/upload-links/:token/` | `DeleteOrgUploadLink` | Verifies org, dual-delete |

#### Devices (3 endpoints — empty responses)
| Method | Endpoint | Handler | Notes |
|--------|----------|---------|-------|
| GET | `/org/:org_id/admin/devices/` | `ListOrgDevices` | No device table |
| DELETE | `/org/:org_id/admin/devices/` | `UnlinkOrgDevice` | No-op |
| GET | `/org/:org_id/admin/devices-errors/` | `ListOrgDeviceErrors` | No device table |

### Stub Implementations (return 501)

| Category | Endpoints | Priority |
|----------|-----------|----------|
| Statistics | 5 (file-ops, storage, active-users, traffic, user-traffic) | Low |
| Logs | 3 (file access, file update, repo permission) | Medium |
| Web Settings | 3 (get, set, logo) | Low |
| SAML Config | 3 (get, update, verify domain) | Low |

**Total org admin stubs remaining: 14 endpoints**

### Performance Optimizations

- **Batch user resolution**: `resolveUsersMap()` loads all org users in a single query, replacing per-row N+1 queries
- **No ALLOW FILTERING**: `ListOrgGroupLibraries` iterates org libraries and checks shares per `library_id` (partition key)
- **`sort.Slice`**: `ListOrgRepos` uses stdlib sort instead of O(n²) bubble sort
- **Quota storage**: Group quotas stored in `organizations.settings['group_quota_{groupID}']`

---

## 6. Parity Status — Superadmin vs Org Admin (2026-03-05)

| Feature | Superadmin | Org Admin |
|---------|------------|-----------|
| User CRUD | ✅ | ✅ |
| Group CRUD | ✅ | ✅ |
| Group Members | ✅ | ✅ |
| Library CRUD | ✅ | ✅ |
| Library Dirents | ✅ | ✅ |
| Trash mgmt | ✅ | ✅ |
| Departments | ✅ | ✅ |
| Address Book Groups | ✅ | ✅ |
| Group Owned Libraries | ✅ | ✅ |
| Share Links | ✅ | ✅ |
| Upload Links | ✅ | ✅ |
| Web Settings | ✅ | ❌ stub |
| Branding | ✅ | ❌ stub |
| Statistics | ❌ stub | ❌ stub |
| Logs | ❌ stub | ❌ stub |
| Devices | ❌ stub | ❌ empty |
| Notifications | ❌ stub | N/A |
| Institutions | ❌ stub | N/A |
| SAML/SSO config | N/A | ❌ stub |

---

## Implementation Order

1. ~~**Admin Library Management**~~ — ✅ DONE (2026-02-12)
2. ~~**Admin Share Link & Upload Link Management**~~ — ✅ DONE (2026-02-12)
3. ~~**Superadmin Departments/Address Book/Group-Owned Libs**~~ — ✅ DONE (2026-03-05)
4. ~~**Org Admin Panel**~~ — ✅ DONE (2026-03-05)
5. **Audit Logs** — Largest scope (5 tables, integration across ~15 handlers), medium priority

---

## Related Documentation

- [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md) — Component stability matrix
- [ENDPOINT-REGISTRY.md](ENDPOINT-REGISTRY.md) — Route registry (update when implementing)
- [API-REFERENCE.md](API-REFERENCE.md) — API documentation (update when implementing)
- [DATABASE-GUIDE.md](DATABASE-GUIDE.md) — Cassandra schema reference
- [ROLES-AND-PERMISSIONS.md](ROLES-AND-PERMISSIONS.md) — Admin role requirements
- [OIDC.md](OIDC.md) — OIDC manages users/groups/tenants; SesameFS admin handles storage
- Existing admin code: `internal/api/v2/admin.go` (group + user admin endpoints)
- Existing share links: `internal/api/v2/share_links.go`
- Existing audit middleware: `internal/middleware/audit.go`
- Frontend admin pages: `frontend/src/pages/sys-admin/`
