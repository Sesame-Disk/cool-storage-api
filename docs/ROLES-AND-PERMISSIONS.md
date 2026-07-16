# Roles & Permissions - SesameFS

**Last Updated**: 2026-03-27
**Status**: IMPLEMENTED — All phases complete (superadmin role, admin API, OIDC org provisioning, role sync, status/role separation)

---

## Overview

SesameFS uses a **three-layer permission model**:

1. **Platform level** — Super admins manage all tenants (cross-org)
2. **Organization level** — Org owners, org admins, and users operate within their org
3. **Resource level** — Library permissions, group roles, share links

Users are provisioned exclusively through the **OIDC provider**. SesameFS does not create users directly — it auto-provisions on first login and syncs roles from OIDC claims.

---

## Role vs Status (Separated Fields)

**Added**: 2026-03-19

The `role` and `status` fields serve different purposes and are stored independently on both `users` and `organizations` tables.

### `role` = Permission Level

Determines what actions the user is allowed to perform. Values: `superadmin`, `owner`, `admin`, `user`, `readonly`, `guest`.

The role is **never overwritten** by lifecycle operations. A deactivated admin retains `role="admin"` and gets it back on reactivation.

### `status` = Lifecycle State

Determines whether the account is active, suspended, or pending deletion. Values: `active`, `deactivated`, `deleted`.

- **`active`** — Normal operation. Full access according to `role`.
- **`deactivated`** — Suspended. All access blocked (auth middleware rejects requests). Reversible by admin.
- **`deleted`** — Pending permanent deletion. All access blocked. Reversible within grace period. After grace period, GC cascade removes all data.

### Why They Are Separate

Previously, `role` was overloaded to encode both permission level and lifecycle state (e.g., `role="deactivated"`). This caused the original role to be lost — when reactivating a user, the system could not restore them to their previous role (admin, user, etc.) and had to default to `"user"`.

With separate fields:
- Deactivating an admin: `role="admin"` stays, `status="deactivated"` is set
- Reactivating: `status="active"` is set, user is still `role="admin"`

### Status Transitions

```
Users:
  active ──────► deactivated ──────► deleted ──────► [GC cascade after grace period]
    ▲                │                   │
    └────────────────┘                   │
    (reactivate)     ◄───────────────────┘
                     (restore within grace period)

Organizations:
  active ──────► deactivated ──────► deleted ──────► [GC cascade after grace period]
    ▲                │                   │
    └────────────────┘                   │
    (reactivate)     ◄───────────────────┘
                     (restore within grace period)
```

### Enforcement Points

| Layer | What It Checks | Effect |
|-------|---------------|--------|
| `deactivateUser` / `softDeleteUser` / `deactivateOrg` / `softDeleteOrg` | N/A | Kills all sessions at source (DB + cache), sets share links `active=false` |
| `reactivateUser` / `restoreDeletedUser` / `reactivateOrg` / `restoreDeletedOrg` | N/A | Sets share links `active=true` (re-enables them) |
| `authMiddleware` (OIDC sessions) | Session existence | Sessions are killed at source — no per-request status query needed |
| `authMiddleware` (Repo API tokens) | `enforceAccountStatus()` | Queries user `status` + org `status` — blocks deactivated/deleted |
| `smartLinkAuthMiddleware` (Repo API tokens) | `enforceAccountStatus()` | Same as above |
| `provisionUser` (OIDC) | User `status` + Org `status` at login | Rejects login attempts from deactivated/deleted users/orgs |
| `resolveShareLink` | `active` flag on share link | Shows "disabled" (vs "expired") — link can be re-enabled on reactivation |

### Side Effects on Deactivate/Delete

When a user or org is deactivated/deleted, the helper functions trigger these side effects in a background goroutine:

1. **Session invalidation** — `InvalidateUserSessions()` reads all `token_hash` from `sessions_by_user`, batch-deletes from both `sessions` and `sessions_by_user`, and evicts from in-memory cache.
2. **Share link disable** — `setUserShareLinksActive(false)` / `setOrgShareLinksActive(false)` sets `active=false` on canonical link rows and the admin read models.

On reactivation/restore, step 2 is reversed (`active=true`). Sessions are not restored — the user must log in again.

### Backfill Migration

Existing data is automatically migrated on startup:
- `role="deactivated"` → `status="deactivated"`, `role="user"` (original role was lost, defaults to `"user"`)
- `role="deleted"` → `status="deleted"`, `role` preserved
- `organizations.settings['status']` → `organizations.status` column

---

## Role Hierarchy

```
superadmin          ← Platform-level (dedicated platform org)
  └── owner         ← Tenant-level highest role (billing + lifecycle)
      └── admin     ← Tenant-level admin (org-scoped)
          └── user      ← Regular tenant member
              └── readonly  ← View-only tenant member
                  └── guest     ← Limited to shared content only
```

### Role Definitions

| Role | Scope | Description |
|------|-------|-------------|
| `superadmin` | **Platform** | Manages all tenants. Belongs to the platform org (`00000000-0000-0000-0000-000000000000`). `/api/v2.1/admin/...` requires both platform org membership and `role=superadmin`. |
| `owner` | **Tenant** | Highest tenant role. Can do everything an org admin can, plus ownership transfer, org lifecycle actions, and billing-facing flows inside their org. |
| `admin` | **Tenant** | Full operational control within their organization. Can manage users, create/delete libraries, manage groups, configure org settings, and view audit logs. Cannot access other tenants. |
| `user` | **Tenant** | Regular member. Can create libraries, upload/download files, create groups, share content with other members. Standard quota applies. |
| `readonly` | **Tenant** | View-only access. Can browse libraries shared with them, download files, but cannot upload, create libraries, share, or modify content. |
| `guest` | **Tenant** | Most restricted. Can only access content explicitly shared with them. Cannot browse org libraries, create anything, or share with others. Intended for external collaborators. |

### Hierarchy Value Map (for code)

```go
roleHierarchy := map[OrganizationRole]int{
    RoleSuperAdmin: 5,
    RoleOwner:      4,
    RoleAdmin:      3,
    RoleUser:       2,
    RoleReadOnly:   1,
    RoleGuest:      0,
}
```

---

## Platform Org (Super Admin)

Super admins use a **dedicated platform organization**:

| Property | Value |
|----------|-------|
| **Org ID** | `00000000-0000-0000-0000-000000000000` |
| **Org Name** | `SesameFS Platform` |
| **Purpose** | Houses super admin users only |

### Design Rationale

- **Clean isolation**: Super admins don't "leak" into tenant data. They exist outside the tenant model.
- **No special-case boolean**: Avoids `is_super_admin` flag scattered through the codebase. The role field handles it.
- **Existing schema works**: The `users` table already partitions by `(org_id, user_id)`. Platform org is just another org_id.
- **Auditable**: All super admin actions can be filtered by platform org_id.

### Cross-Tenant Access Pattern

Super admins access tenant data by providing a **target org_id** in API requests:

```
GET /api/v2/admin/organizations/                        # List all orgs
GET /api/v2/admin/organizations/{org_id}/               # View org details
GET /api/v2/admin/organizations/{org_id}/users/         # List org users
GET /api/v2/admin/organizations/{org_id}/libraries/     # List org libraries
PUT /api/v2/admin/organizations/{org_id}/               # Update org settings
DELETE /api/v2/admin/organizations/{org_id}/             # Soft-delete org (grace period → GC cascade)
```

Super admins **cannot** use regular `/api/v2/repos/` endpoints (they have no tenant context). They must use the `/api/v2/admin/` prefix which explicitly requires a target org.

Org owners/admins use the separate `/api/v2.1/org/...` surface. Tenant roles never inherit `/api/v2.1/admin/...` access.

---

## Permission Matrix

### Organization-Level Operations

| Operation | superadmin | owner | admin | user | readonly | guest |
|-----------|:----------:|:-----:|:-----:|:----:|:--------:|:-----:|
| List all organizations | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Create organization | ✅ | ❌ | ❌ | ❌ | ❌ | ❌ |
| Deactivate organization | ✅ | ✅ | ❌ | ❌ | ❌ | ❌ |
| View org settings | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| Modify org settings | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| List org users | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| Deactivate org user | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| Change user role (within org) | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |
| View org audit log | ✅ | ✅ | ✅ | ❌ | ❌ | ❌ |

### Library Operations

| Operation | admin | user | readonly | guest |
|-----------|:-----:|:----:|:--------:|:-----:|
| Create library | ✅ | ✅ | ❌ | ❌ |
| Delete own library | ✅ | ✅ | ❌ | ❌ |
| Delete any org library | ✅ | ❌ | ❌ | ❌ |
| Upload files | ✅ | ✅ | ❌ | ❌ |
| Download files (own/shared) | ✅ | ✅ | ✅ | ✅* |
| Browse org libraries | ✅ | ✅ | ✅ | ❌ |
| Browse shared libraries | ✅ | ✅ | ✅ | ✅ |
| Share library with user/group | ✅ | ✅ | ❌ | ❌ |
| Create share link | ✅ | ✅ | ❌ | ❌ |
| Set library password (encrypted) | ✅ | ✅ | ❌ | ❌ |

\* Guest can only download from libraries explicitly shared with them.

### Group Operations

| Operation | admin | user | readonly | guest |
|-----------|:-----:|:----:|:--------:|:-----:|
| Create group | ✅ | ✅ | ❌ | ❌ |
| Join group (if allowed) | ✅ | ✅ | ✅ | ❌ |
| Manage group members | ✅ | owner/admin | ❌ | ❌ |
| Delete group | ✅ | owner only | ❌ | ❌ |

---

## OIDC Role Provisioning

### How Roles Flow from OIDC

```
OIDC Provider (source of truth)
    │
    │  Custom claim: "roles" (configurable via roles_claim)
    │  Custom claim: "tenant_id" (configurable via org_claim)
    │
    ▼
SesameFS OIDC Client (internal/auth/oidc.go)
    │
    │  extractRoles() → reads claim from ID token or userinfo
    │  mapOIDCRole()  → maps provider role string to SesameFS role
    │  extractOrgID() → determines which org the user belongs to
    │
    ▼
provisionUser()
    │
    ├─ User exists?  → Update role if OIDC role differs (role sync)
    └─ New user?     → Create user record with mapped role
         │
         ├─ Org exists?  → Assign to existing org
         └─ New org?     → Auto-create org (if AutoProvision enabled)
```

### OIDC Role Mapping

The OIDC provider sends role strings in a custom claim. SesameFS maps them:

| OIDC Claim Value | SesameFS Role | Notes |
|------------------|---------------|-------|
| `superadmin`, `super_admin`, `platform_admin` | `superadmin` | **Must also have platform org claim; otherwise normalized to `owner`** |
| `owner`, `org_owner` | `owner` | Highest tenant role |
| `admin`, `administrator`, `tenant_admin` | `admin` | Org-scoped admin |
| `user`, `member` | `user` | Regular member |
| `readonly`, `read-only`, `viewer` | `readonly` | View-only |
| `guest`, `external` | `guest` | Limited access |
| *(anything else)* | `DefaultRole` config value | Fallback |

### Super Admin Provisioning via OIDC

Super admins are provisioned the same way as other users, but with two requirements:

1. **Role claim** must map to `superadmin`
2. **Org claim** must resolve to the platform org ID (`00000000-0000-0000-0000-000000000000`)

The OIDC provider should be configured to assign the platform org claim value to super admin users. SesameFS config maps this:

```yaml
auth:
  oidc:
    org_claim: "tenant_id"
    roles_claim: "roles"
    platform_org_claim_value: "platform"  # when org claim = "platform", maps to platform org ID
```

### Super Admin Bootstrap (when OIDC can't be reconfigured)

If you cannot configure the OIDC provider to send `org_claim="platform"` for superadmin users,
use the bootstrap script to promote a user directly in Cassandra:

```bash
./scripts/make-superadmin.sh your@email.com "Display Name"
```

The script:
1. Finds or creates the user in the platform org with `role=superadmin`
2. Updates the `users_by_email` lookup so login resolves to the platform org
3. Invalidates existing sessions

After running, the user must log out and log back in via OIDC.

## Session Flags Exposed to Frontends

The account-info response intentionally exposes two different booleans:

| Field | Meaning |
|-------|---------|
| `is_staff` | `true` only for a real platform superadmin (`role=superadmin` in the platform org) |
| `is_org_staff` | `true` for org-level staff roles (`owner`, `admin`) and also for platform superadmin |

This split keeps `/sys/` and `/admin/` reserved for platform operations while still enabling org-admin tools for tenant owners/admins.

**Caveat**: If OIDC provisioning is active (`OIDC_AUTO_PROVISION=true`) and the user's
org claim doesn't match `platform_org_claim_value`, re-login will re-assign them to their
OIDC-claimed tenant org, undoing the script. Either configure the OIDC provider or set
`OIDC_DEFAULT_ROLE=superadmin` combined with `OIDC_PLATFORM_ORG_CLAIM_VALUE`.

### Organization Auto-Provisioning (IMPLEMENTED)

When an OIDC user logs in with an org claim value that doesn't match any existing organization:

1. SesameFS creates a new `organizations` record with defaults (1TB quota, S3 backend)
2. Assigns default settings (quota, storage config, chunking polynomial)
3. Users get the role from their OIDC claim (not auto-promoted)
4. Subsequent users join the existing org

**Config option**: `allowed_org_claims` restricts which org values are accepted (empty = allow all).

---

## Granular Permission Flags (Custom Share Permissions)

**Added**: 2026-03-11

In addition to standard permission levels (`rw`, `r`, `cloud-edit`, `preview`), SesameFS supports **custom share permissions** with granular flags. This allows library owners to define fine-grained access controls beyond the standard levels.

### PermissionFlags Struct

```go
type PermissionFlags struct {
    Upload               bool `json:"upload"`
    Download             bool `json:"download"`
    Create               bool `json:"create"`
    Modify               bool `json:"modify"`
    Copy                 bool `json:"copy"`
    Delete               bool `json:"delete"`
    Preview              bool `json:"preview"`
    DownloadExternalLink bool `json:"download_external_link"`
}
```

### Default Flag Mappings

| Permission Level | upload | download | create | modify | copy | delete | preview | download_external_link |
|------------------|:------:|:--------:|:------:|:------:|:----:|:------:|:-------:|:---------------------:|
| `owner` / `admin` / `rw` | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| `cloud-edit` | ✅ | ❌ | ✅ | ✅ | ❌ | ✅ | ✅ | ❌ |
| `r` (read-only) | ❌ | ✅ | ❌ | ❌ | ✅ | ❌ | ✅ | ✅ |
| `preview` | ❌ | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ | ❌ |

### Custom Permission Resolution

When a user has multiple shares to the same library (e.g., direct share + group share), flags are **merged using OR logic** — the user gets the union of all capabilities.

### Key Middleware Methods

| Method | Purpose |
|--------|---------|
| `GetLibraryPermissionWithFlags()` | Returns both the permission level and granular flags for a user's library access |
| `RequirePermFlag(c, flag)` | Checks if user has a specific flag for the repo in the current request context |
| `RequirePermFlagForRepo(c, repoID, flag)` | Checks a specific flag for an explicit repo ID |
| `FlagsForPermission(perm)` | Returns default flags for a standard permission level |
| `resolveCustomPermWithFlags(permID)` | Looks up a custom permission by UUID and returns its flags |

### Custom Permission CRUD Endpoints

| Method | Endpoint | Purpose |
|--------|----------|---------|
| GET | `/api/v2.1/repos/:repo_id/custom-share-permissions/` | List custom permissions |
| GET | `/api/v2.1/repos/:repo_id/custom-share-permissions/:perm_id/` | Get single permission |
| POST | `/api/v2.1/repos/:repo_id/custom-share-permissions/` | Create custom permission |
| PUT | `/api/v2.1/repos/:repo_id/custom-share-permissions/:perm_id/` | Update custom permission |
| DELETE | `/api/v2.1/repos/:repo_id/custom-share-permissions/:perm_id/` | Delete custom permission |

### Database Tables

```sql
-- Custom share permissions
CREATE TABLE custom_share_permissions (
    permission_id UUID PRIMARY KEY,
    creator_id UUID,
    name TEXT,
    description TEXT,
    permission_json TEXT,  -- JSON-encoded PermissionFlags
    created_at TIMESTAMP
);

-- Lookup by creator
CREATE TABLE custom_share_permissions_by_user (
    creator_id UUID,
    permission_id UUID,
    name TEXT,
    description TEXT,
    permission_json TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((creator_id), permission_id)
);
```

### How It Works

1. Library owner creates a custom permission with specific flags (e.g., "Can preview and download but not upload")
2. When sharing a library, the permission field can be a standard string (`"rw"`, `"r"`) **or** a custom permission UUID
3. File operation handlers call `RequirePermFlag()` to check if the user's resolved flags allow the action
4. The middleware resolves the permission by looking up the UUID in `custom_share_permissions` and extracting the flags

---

## Implementation Status

All phases are complete. Modified/created files:

| Component | File | Status |
|-----------|------|--------|
| Role constants (5-tier including `superadmin`) | `internal/middleware/permissions.go:24-30` | ✅ Complete |
| Role hierarchy check (levels 0-4) | `internal/middleware/permissions.go:354-366` | ✅ Complete |
| `RequireOrgRole()` middleware | `internal/middleware/permissions.go:74-104` | ✅ Complete |
| `RequireSuperAdmin()` middleware | `internal/middleware/permissions.go:312-347` | ✅ Complete |
| `RequireAdminOrAbove()` middleware | `internal/middleware/permissions.go:350-352` | ✅ Complete |
| Library permission system | `internal/middleware/permissions.go:106-424` | ✅ Complete |
| Granular permission flags (`PermissionFlags`) | `internal/middleware/permissions.go:257-330` | ✅ Complete (2026-03-11) |
| `GetLibraryPermissionWithFlags()` | `internal/middleware/permissions.go:342-432` | ✅ Complete (2026-03-11) |
| `RequirePermFlag()` / `RequirePermFlagForRepo()` | `internal/middleware/permissions.go:706-740` | ✅ Complete (2026-03-11) |
| Custom share permission CRUD | `internal/api/v2/file_shares.go:1092-1320` | ✅ Complete (2026-03-11) |
| Permission flags tests | `internal/middleware/permissions_test.go` | ✅ Complete (2026-03-11) |
| Group role system | `internal/middleware/permissions.go:171-391` | ✅ Complete |
| OIDC role extraction + superadmin mapping | `internal/auth/oidc.go:612-658` | ✅ Complete |
| OIDC org claim + platform org mapping | `internal/auth/oidc.go:581-610` | ✅ Complete |
| OIDC org auto-provisioning | `internal/auth/oidc.go:451-478` | ✅ Complete |
| OIDC role sync on re-login | `internal/auth/oidc.go:521-532` | ✅ Complete |
| Admin API endpoints (org + user CRUD) | `internal/api/v2/admin.go` | ✅ Complete |
| Admin API tests | `internal/api/v2/admin_test.go` | ✅ Complete |
| Platform org + superadmin seeding | `internal/db/seed.go` | ✅ Complete |
| Config: PlatformOrgID, PlatformOrgClaimValue, DevTokenEntry.Role | `internal/config/config.go` | ✅ Complete |
| Route registration + dev token role | `internal/api/server.go` | ✅ Complete |
| Integration tests | `scripts/test-admin-api.sh`, `scripts/test-permissions.sh` | ✅ Complete |

---

## Database Schema

The schema uses separate `role` and `status` columns for users, and a `status` column for organizations:

```sql
-- Users table: role = permission level, status = lifecycle state
CREATE TABLE users (
    org_id UUID,
    user_id UUID,
    email TEXT,
    name TEXT,
    role TEXT,           -- "superadmin", "admin", "user", "readonly", "guest"
    status TEXT,         -- "active", "deactivated", "deleted"
    ...
    PRIMARY KEY ((org_id), user_id)
);

-- Sessions: role cached for fast auth
CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id UUID,
    org_id UUID,
    email TEXT,
    role TEXT,           -- Cached from users table
    ...
);

-- Sessions by user: reverse index for bulk invalidation on deactivate/delete
CREATE TABLE sessions_by_user (
    org_id UUID,
    user_id UUID,
    token_hash TEXT,
    PRIMARY KEY ((org_id, user_id), token_hash)
);

-- Organizations: status tracks lifecycle state
CREATE TABLE organizations (
    org_id UUID PRIMARY KEY,
    name TEXT,
    status TEXT,         -- "active", "deactivated", "deleted"
    ...
);
```

Both `role` and `status` columns are `TEXT`. The `status` column defaults to `"active"` for new records. A backfill migration handles existing data where `role` was overloaded for lifecycle state.

---

## Frontend Implications

The Seafile frontend uses `window.app.pageOptions` flags from the backend:

| Flag | Set When |
|------|----------|
| `canAddRepo` | role >= `user` |
| `canShareRepo` | role >= `user` |
| `canAddGroup` | role >= `user` |
| `canGenerateShareLink` | role >= `user` |
| `canInvitePeople` | role >= `admin` |
| `isStaff` | legacy frontend alias; currently maps to real platform superadmin only |
| `isOrgStaff` | org staff roles (`owner`, `admin`) and platform superadmin |
| `isPlatformAdmin` | legacy/conceptual alias for platform superadmin; not the canonical wire name |

The super admin frontend experience will need a **separate admin panel** (or admin section) that shows the multi-tenant management UI. This is outside the regular Seafile frontend.

Pending naming cleanup:
- Backend semantics are already split between platform superadmin and org staff.
- Frontend/mobile payload names still use legacy fields like `is_staff` and `is_org_staff` for compatibility.
- A future compatibility-preserving migration should introduce explicit names such as `isSuperAdmin` while keeping old fields as aliases during transition.

---

## Security Considerations

1. **Super admin cannot directly browse tenant files** — They use admin API endpoints, not regular file endpoints. This prevents accidental data exposure.
2. **OIDC is source of truth** — Roles cannot be changed through SesameFS API alone. The admin endpoints can deactivate users but not promote them. Role changes come from OIDC.
3. **Platform org reservation** — The platform org is reserved for platform identities and administration, but it remains a functional org partition. Internal libraries and file data are allowed and use the same org-scoped storage isolation as tenant orgs.
4. **Audit logging** — All admin API calls should be logged with the acting super admin's identity.
5. **Session role caching** — When a role changes via OIDC re-login, existing sessions still carry the old role until they expire. Consider invalidating sessions on role change.

---

## Related Documentation

- [OIDC.md](OIDC.md) — OIDC provider config, login flow, claim mapping
- [ARCHITECTURE.md](ARCHITECTURE.md) — Multi-tenancy model, org_id partitioning
- [ENDPOINT-REGISTRY.md](ENDPOINT-REGISTRY.md) — API route registry (add admin routes here)
- [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md) — Component status tracking
- `internal/middleware/permissions.go` — Permission middleware code
- `internal/auth/oidc.go` — OIDC provisioning code
