# OIDC Integration - SesameFS

**Last Updated**: 2026-03-26
**Status**: IMPLEMENTED - All Phases Complete (OIDC Login + Role Sync + Org Provisioning + Admin API + Group/Dept Sync + Desktop Client SSO)

---

## Overview

SesameFS will use OIDC (OpenID Connect) for user authentication and identity attachment. In the current Accounts integration contract, OIDC is the login boundary and Accounts is the operational writer through SesameFS admin APIs. The OIDC provider remains the source of truth for:

- User accounts (creation, deletion, profile data)
- Login identity claims used for attachment/reconciliation
- Any claim-driven role mapping that a deployment explicitly chooses to keep enabled
- Multi-tenant isolation

---

## OIDC Provider Details

### Discovery Endpoint

The OIDC discovery document must be available at:
```
https://<your-oidc-provider>/.well-known/openid-configuration
```

### Redirect URIs

There are **two separate redirect URIs** — one for the web login flow and one for the desktop client SSO flow. Both must be registered in the OIDC provider.

| Flow | Redirect URI | Handler |
|------|-------------|---------|
| Web login (browser) | `https://<your-domain>/sso/` | React frontend (`/sso/` page) |
| Desktop client SSO | `https://<your-domain>/oauth/callback/` | Server-side (`handleOAuthCallback`) |

| Environment | Web redirect URI | Desktop redirect URI |
|-------------|-----------------|---------------------|
| Development | `http://localhost:3000/sso/` | `http://localhost:3000/oauth/callback/` |
| Production | `https://<your-domain>/sso/` | `https://<your-domain>/oauth/callback/` |

**Important**: Todas las URIs terminan en `/`. Deben estar registradas en el proveedor OIDC y en `OIDC_REDIRECT_URIS`.

---

## Implementation Plan

### Phase 1: Basic OIDC Login - COMPLETE

**Goal**: Replace dev token authentication with OIDC login flow
**Status**: Implemented 2026-01-28

1. **Add OIDC Configuration**
   - Add OIDC settings to `internal/config/config.go`
   - Environment variables: `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_REDIRECT_URIS`
   - Support multiple redirect URIs (comma-separated list)

2. **Implement OIDC Authentication Flow**

   **Frontend (`/sso` page)**:
   - Create `frontend/src/pages/sso/sso.js` - SSO callback handler
   - Extract `code` and `state` from URL params
   - Send code to backend API for token exchange
   - Store returned session token
   - Redirect to dashboard on success

   **Backend endpoints**:
   - `GET /api/v2.1/auth/oidc/config` - Returns public OIDC configuration
   - `GET /api/v2.1/auth/oidc/login` - Returns OIDC authorization URL
   - `POST /api/v2.1/auth/oidc/callback` - Exchanges code for tokens, creates session
   - `GET /api/v2.1/auth/oidc/logout` - Returns OIDC logout URL (Single Logout)
   - Validate redirect_uri is in allowed list before processing

3. **Redirect URI Validation**
   ```go
   // Validate redirect_uri is in allowed list
   func (h *OIDCHandler) validateRedirectURI(uri string) bool {
       for _, allowed := range h.config.RedirectURIs {
           if uri == allowed {
               return true
           }
       }
       return false
   }
   ```

4. **Database Schema** (already exists)
   ```sql
   CREATE TABLE sesamefs.users_by_oidc (
       oidc_issuer TEXT,
       oidc_sub TEXT,
       user_id UUID,
       org_id UUID,
       PRIMARY KEY ((oidc_issuer), oidc_sub)
   );
   ```

### Phase 2: Organization/Tenant Mapping — COMPLETE

- OIDC claims map to SesameFS organizations via configurable `org_claim`
- Auto-provisioning creates orgs on first login if they don't exist
- Platform org claim value maps superadmin users to the platform org

### Phase 3: Role Synchronization — COMPLETE

- 5-tier role hierarchy mapped from OIDC claims
- Role sync on re-login (OIDC is source of truth)
- Admin API endpoints for org/user management

See [ROLES-AND-PERMISSIONS.md](ROLES-AND-PERMISSIONS.md) for full details.

---

## OIDC Provider Requirements (What to Implement)

This section documents what the OIDC provider must emit in its tokens and how SesameFS consumes those claims. Use this as the integration spec.

### Required ID Token / UserInfo Claims

The OIDC provider must include these claims in the **ID token** (preferred) or make them available via the **userinfo endpoint**:

| Claim | Type | Required | Description |
|-------|------|----------|-------------|
| `sub` | string | **Yes** | Unique, stable user identifier. Used as the OIDC-to-SesameFS user mapping key. |
| `email` | string | **Yes** | User's email address. Used for display and as fallback identifier. |
| `name` | string | Recommended | Display name. Falls back to `preferred_username`, then email. |
| `preferred_username` | string | Optional | Used as display name fallback if `name` is absent. |
| `email_verified` | boolean | Optional | Whether email is verified. |
| **`<org_claim>`** | string | **Yes** for multi-tenant | Organization/tenant identifier. Claim name is configurable (default: `tenant_id`). See "Org Claim" below. |
| **`<roles_claim>`** | string or string[] | **Yes** for role mapping | User's role. Claim name is configurable (default: `roles`). See "Role Claim" below. |

### Org Claim (`org_claim`)

The org claim tells SesameFS which tenant/organization the user belongs to.

**Config**: `org_claim: "tenant_id"` (the claim name in the ID token)

**Value format**: The claim value is used directly as the org UUID in SesameFS, **except** when it matches the `platform_org_claim_value` — in that case it maps to the platform org UUID.

| Org Claim Value | SesameFS Behavior |
|-----------------|-------------------|
| A UUID string (e.g., `"a1b2c3d4-..."`) | Used directly as `org_id`. If org doesn't exist and `auto_provision=true`, it's created automatically. |
| The platform claim value (e.g., `"platform"`) | Maps to platform org `00000000-0000-0000-0000-000000000000`. User must also have a superadmin role. |
| Empty / missing | Falls back to `default_org_id` config, or generates a deterministic UUID from the issuer URL. |

**Example ID token payload**:
```json
{
  "sub": "user-uuid-1234",
  "email": "alice@acme.com",
  "name": "Alice Smith",
  "tenant_id": "550e8400-e29b-41d4-a716-446655440000",
  "roles": ["admin"]
}
```

**Example for a superadmin**:
```json
{
  "sub": "superadmin-uuid-5678",
  "email": "admin@example.com",
  "name": "Platform Admin",
  "tenant_id": "platform",
  "roles": ["superadmin"]
}
```

### Role Claim (`roles_claim`)

The role claim tells SesameFS what role to assign the user.

**Config**: `roles_claim: "roles"` (the claim name in the ID token)

**Accepted formats**:
- **String array**: `"roles": ["admin", "user"]` — first element is used
- **Single string**: `"roles": "admin"` — used directly

**Role mapping** (case-insensitive):

| OIDC Provider Emits | SesameFS Maps To | Access Level |
|---------------------|------------------|--------------|
| `superadmin`, `super_admin`, `platform_admin`, `SUPERADMIN` | `superadmin` | Full platform access (requires platform org claim) |
| `admin`, `administrator` | `admin` | Full tenant management |
| `user`, `member` | `user` | Standard member (create libraries, upload, share) |
| `readonly`, `read-only`, `viewer` | `readonly` | View-only (browse, download, no create/upload) |
| `guest`, `external` | `guest` | Shared content only |
| *(anything else)* | Config `default_role` (default: `user`) | Fallback |

**Important**: A `superadmin` role is only effective when the user's org claim resolves to the platform org. If OIDC or legacy DB state yields `superadmin` for a regular tenant org, SesameFS normalizes that role to `owner` and persists the correction on login.

### Provisioning Logic (What Happens on Login)

```
User authenticates with OIDC provider
        │
        ▼
SesameFS receives ID token + userinfo
        │
        ├─ Extract org_claim value
        │   ├─ Matches platform_org_claim_value? → org_id = "00000000-...-000000000000"
        │   ├─ Valid UUID string? → org_id = that UUID
        │   └─ Empty? → org_id = default_org_id or deterministic UUID
        │
        ├─ Extract roles_claim value
        │   └─ Map first role via mapOIDCRole() → SesameFS role string
        │
        ├─ Does org exist in DB?
        │   ├─ No + auto_provision=true → CREATE org (name from config or "Auto-provisioned Organization", 1TB quota, S3 backend)
        │   └─ Yes → continue
        │
        ├─ Does user exist? (lookup via users_by_oidc table: oidc_issuer + oidc_sub)
        │   ├─ No + auto_provision=true → CREATE user (new UUID, email from claims, normalized role)
        │   └─ Yes → ROLE SYNC: if DB role ≠ normalized OIDC role, UPDATE role in DB
        │
        └─ Create session (JWT), update users.last_login_at → return to frontend
```

      Successful OIDC authentication updates the user's `last_login_at` timestamp when the session or API-token session is created. Admin and org-admin user management endpoints expose that value as `last_login`. This is a point-in-time field only; SesameFS still does not persist a full login event history.

### Role Sync on Re-Login

Every time an existing user logs in, SesameFS compares the normalized role from the OIDC token with the role stored in the database. If they differ, the DB is updated to the normalized role. This means:

- **Promotions** (e.g., user → admin): Take effect on next login
- **Demotions** (e.g., admin → readonly): Take effect on next login
- **Platform safety override**: `superadmin` is reserved for the platform org; tenant `superadmin` claims are downgraded to `owner`
- Lifecycle state is enforced via `status`, not by pseudo-roles like `deactivated`

### What the OIDC Provider Must Support

| Requirement | Detail |
|-------------|--------|
| **OpenID Connect Discovery** | `/.well-known/openid-configuration` at the issuer URL |
| **Authorization Code Flow** | Standard OIDC auth code flow with `response_type=code` |
| **PKCE support** | Optional but recommended. Enabled via `require_pkce: true` in SesameFS config. |
| **Token endpoint** | Must accept `grant_type=authorization_code` with `client_id`, `client_secret`, `code`, `redirect_uri` |
| **ID token** | JWT with at minimum: `sub`, `email`, `iss`, `exp`, `iat` |
| **Custom claims** | Must include the org claim and roles claim in the ID token or userinfo response |
| **UserInfo endpoint** | Optional fallback. If ID token doesn't contain email/name, SesameFS fetches from userinfo. |
| **End Session endpoint** | Optional. If present in discovery doc, used for single logout. |
| **Stable `sub` claim** | The `sub` value must never change for a given user. It's the primary key for user mapping. |

---

## Admin API Endpoints (For Management UIs / OIDC Provider Webhooks)

These endpoints are available at `/api/v2.1/admin/` and allow managing tenants and users programmatically. Accounts should use these routes with a dedicated platform service account API key for provisioning, quota changes, and lifecycle actions.

### Authentication

All admin endpoints require a valid session token or dev token:
```
Authorization: Token <session_token>
```

### Organization Management (Superadmin Only)

All org endpoints require the caller to be a **superadmin in the platform org**.

#### List Organizations
```
GET /api/v2.1/admin/organizations/
```
**Response** `200`:
```json
{
  "organizations": [
    {
      "org_id": "550e8400-e29b-41d4-a716-446655440000",
      "name": "Acme Corp",
      "storage_quota": 1000000000000,
      "storage_used": 52428800,
      "settings": {"theme": "default", "features": "all"},
      "created_at": "2026-01-29T10:00:00Z"
    }
  ]
}
```

#### Create Organization
```
POST /api/v2.1/admin/organizations/
Content-Type: application/json

{
  "name": "Acme Corp",
  "storage_quota": 1000000000000
}
```
- `name` (string, **required**): Organization display name
- `storage_quota` (int64, optional): Storage quota in bytes. Default: 1TB (1000000000000)

**Response** `201`:
```json
{
  "org_id": "generated-uuid",
  "name": "Acme Corp",
  "storage_quota": 1000000000000,
  "created_at": "2026-01-29T10:00:00Z"
}
```

#### Get Organization
```
GET /api/v2.1/admin/organizations/:org_id/
```
**Response** `200`:
```json
{
  "org_id": "550e8400-...",
  "name": "Acme Corp",
  "storage_quota": 1000000000000,
  "storage_used": 52428800,
  "settings": {"theme": "default", "features": "all"},
  "created_at": "2026-01-29T10:00:00Z"
}
```

#### Update Organization
```
PUT /api/v2.1/admin/organizations/:org_id/
Content-Type: application/json

{
  "name": "Acme Corp (Renamed)",
  "storage_quota": 2199023255552
}
```
Both fields are optional. Only provided fields are updated.

**Response** `200`: `{"success": true}`

#### Deactivate Organization
```
DELETE /api/v2.1/admin/organizations/:org_id/
```
Sets `settings['status'] = 'deactivated'` on the org. The platform org (`00000000-0000-0000-0000-000000000000`) cannot be deactivated.

**Response** `200`: `{"success": true}`
**Response** `403`: `{"error": "cannot deactivate platform organization"}`

### User Management Through Admin Surfaces

Platform-wide user endpoints under `/api/v2.1/admin/...` now require a real platform superadmin. Org-scoped user administration lives under `/api/v2.1/org/...`.

#### List Org Users
```
GET /api/v2.1/admin/organizations/:org_id/users/
```
**Permission**: Platform superadmin only.

**Response** `200`:
```json
{
  "users": [
    {
      "user_id": "a1b2c3d4-...",
      "email": "alice@acme.com",
      "name": "Alice Smith",
      "role": "admin",
      "quota_bytes": -2,
      "used_bytes": 1048576,
      "created_at": "2026-01-29T10:00:00Z"
    }
  ]
}
```
Note: `quota_bytes = -2` means "use org default quota".

#### Get User
```
GET /api/v2.1/admin/users/:user_id/
```
**Limitation**: The platform-superadmin lookup by bare user id still gets `501 Not Implemented` because Cassandra lookup needs `org_id`; use the org users list endpoint instead.

#### Update User
```
PUT /api/v2.1/admin/users/:user_id/
Content-Type: application/json

{
  "role": "readonly",
  "quota_bytes": 5368709120
}
```
- `role` (string, optional): New role. Valid values: `admin`, `user`, `readonly`, `guest`. Only superadmin can assign `superadmin`.
- `quota_bytes` (int64, optional): Per-user storage quota in bytes.

**Response** `200`: `{"success": true}`
**Response** `403`: `{"error": "only superadmin can assign superadmin role"}`

**Note**: If claim-driven role sync is enabled for a deployment, role changes via this endpoint can be overwritten on the user's next OIDC login. The current Accounts dashboard contract should treat the admin API as the operational write path and keep claim behavior aligned with that policy.

#### Deactivate User
```
DELETE /api/v2.1/admin/users/:user_id/
```
Sets user's `status` to `deactivated` and invalidates active sessions. Cannot deactivate yourself.

**Response** `200`: `{"success": true}`
**Response** `400`: `{"error": "cannot deactivate your own account"}`

**Note**: Deactivated users remain blocked because lifecycle is enforced via `status`. To prevent future reactivation, also disable the user in the OIDC provider.

---

## Group & Department Claims

SesameFS can sync group and department memberships from OIDC claims on each login. The OIDC provider is the source of truth for memberships.

### Configuration

```yaml
auth:
  oidc:
    groups_claim: "groups"              # Claim containing group memberships
    departments_claim: "departments"    # Claim containing department memberships
    sync_groups_on_login: true          # Sync on each login
    sync_departments_on_login: true     # Sync on each login
    full_sync_groups: false             # true = remove from groups not in claims
    full_sync_departments: false        # true = remove from depts not in claims
```

### Expected Claim Formats

**Groups** — array of strings or objects:
```json
// Simple: ID and Name are both the string value
"groups": ["engineering", "design"]

// Structured: explicit ID and Name
"groups": [
  {"id": "eng-001", "name": "Engineering"},
  {"id": "des-002", "name": "Design Team"}
]
```

**Departments** — same pattern, with optional `parent_id` for hierarchy:
```json
"departments": [
  {"id": "sales", "name": "Sales Division", "parent_id": "corp"},
  {"id": "corp", "name": "Corporate"}
]
```

### Sync Behavior

- **Additive (default)**: User is added to groups/departments from claims. Existing memberships are preserved.
- **Full sync** (`full_sync_groups: true`): User is removed from groups not present in the claims.
- Groups/departments are created on first encounter (upsert semantics).
- Internal group UUIDs are deterministic: `uuid.NewSHA1(namespace, orgID + ":group:" + externalID)`.

### Full Reference

See [OIDC-CLAIMS-REFERENCE.md](OIDC-CLAIMS-REFERENCE.md) for the complete claims reference including example token payloads.

---

## Files Created/Modified

### Backend — Phase 1 (OIDC Login)

| File | Purpose | Status |
|------|---------|--------|
| `internal/auth/oidc.go` | OIDC auth, token exchange, role mapping, org provisioning, role sync | CREATED + MODIFIED |
| `internal/auth/session.go` | Session management (JWT creation) | CREATED |
| `internal/config/config.go` | OIDC config + PlatformOrgID + DevTokenEntry.Role | MODIFIED |
| `internal/api/server.go` | Register OIDC + admin routes, dev token role context | MODIFIED |
| `internal/api/v2/auth.go` | OIDC endpoint handlers | CREATED |
| `internal/db/db.go` | Add sessions table migration | MODIFIED |

### Backend — Phase 2-4 (Roles, Admin API, Provisioning)

| File | Purpose | Status |
|------|---------|--------|
| `internal/middleware/permissions.go` | 5-tier role hierarchy, RequireSuperAdmin, PlatformOrgID | MODIFIED |
| `internal/api/v2/admin.go` | Admin API: org CRUD + user management | CREATED |
| `internal/api/v2/admin_test.go` | Admin API unit tests | CREATED |
| `internal/db/seed.go` | Platform org + superadmin user seeding | MODIFIED |
| `internal/models/models.go` | Role field comment update | MODIFIED |
| `internal/api/v2/libraries.go` | Fixed superadmin in role hierarchy | MODIFIED |
| `internal/api/v2/files.go` | Fixed superadmin in role hierarchy | MODIFIED |
| `internal/api/v2/batch_operations.go` | Fixed superadmin in role hierarchy | MODIFIED |
| `scripts/test-admin-api.sh` | Integration tests (56 assertions) | CREATED |
| `scripts/test-admin-panel.sh` | Admin panel integration tests (groups + email-based users) | CREATED |
| `scripts/test-permissions.sh` | Superadmin permission tests | MODIFIED |

### Backend — Phase 5 (Group & Department Sync)

| File | Purpose | Status |
|------|---------|--------|
| `internal/config/config.go` | Added GroupsClaim, DepartmentsClaim, sync config fields | MODIFIED |
| `internal/auth/oidc.go` | Group/dept claim extraction + sync on login | MODIFIED |
| `internal/api/v2/admin.go` | Admin group endpoints + email-based user endpoints | MODIFIED |
| `docs/OIDC-CLAIMS-REFERENCE.md` | Full OIDC claims reference for provider implementers | CREATED |

### Frontend (Phase 1)

| File | Purpose | Status |
|------|---------|--------|
| `frontend/src/pages/sso/index.js` | SSO callback page (`/sso` route) | CREATED |
| `frontend/src/pages/login/index.js` | Added SSO login button | MODIFIED |
| `frontend/src/utils/seafile-api.js` | OIDC API methods + setAuthToken | MODIFIED |
| `frontend/src/app.js` | Handle `/sso` route | MODIFIED |

---

## Configuration Example

```yaml
# config.yaml
auth:
  type: oidc
  oidc:
    issuer_url: https://<your-oidc-provider>
    client_id: "<your-client-id>"
    client_secret: "${OIDC_CLIENT_SECRET}"

    # Allowed redirect URIs - supports multiple for different environments
    # All URIs must also be registered with the OIDC provider.
    # Two URIs per environment: /sso/ (web) and /oauth/callback/ (desktop client)
    redirect_uris:
      - http://localhost:3000/sso/                     # Development — web
      - http://localhost:3000/oauth/callback/          # Development — desktop client
      - https://<your-domain>/sso/                     # Production — web
      - https://<your-domain>/oauth/callback/          # Production — desktop client

    scopes:
      - openid
      - profile
      - email

    # Custom claims for org/role mapping
    org_claim: "tenant_id"              # Claim name containing org/tenant identifier
    roles_claim: "roles"                # Claim name containing user role(s)

    # Platform org configuration (for superadmin users)
    platform_org_id: "00000000-0000-0000-0000-000000000000"   # UUID for the platform org
    platform_org_claim_value: "platform"                       # When org_claim = this value, map to platform org

    # Auto-provisioning
    auto_provision: true                # Create orgs/users on first OIDC login
    default_role: "user"                # Fallback role when OIDC claim is unrecognized
    default_org_name: "New Organization" # Name for auto-provisioned orgs
```

### Environment Variables

```bash
# Required
OIDC_ISSUER_URL=https://<your-oidc-provider>
OIDC_CLIENT_ID=<your-client-id>
OIDC_CLIENT_SECRET=<your-client-secret>

# Redirect URIs (comma-separated). Two per environment: /sso/ (web) + /oauth/callback/ (desktop)
OIDC_REDIRECT_URIS=http://localhost:3000/sso/,http://localhost:3000/oauth/callback/,https://<your-domain>/sso/,https://<your-domain>/oauth/callback/

# Platform org (optional, has defaults)
OIDC_PLATFORM_ORG_ID=00000000-0000-0000-0000-000000000000
OIDC_PLATFORM_ORG_CLAIM_VALUE=platform
```

---

## Testing

### Manual Testing

```bash
# 1. Start authorization flow (redirects to OIDC provider login page)
open "https://<your-oidc-provider>/authorize?client_id=<client-id>&response_type=code&scope=openid%20profile%20email&redirect_uri=http://localhost:3000/sso/"

# 2. After login, user is redirected to http://localhost:3000/sso/?code=AUTHORIZATION_CODE
# 3. Frontend sends code to backend, which exchanges it for tokens:
curl -X POST "https://<your-oidc-provider>/token" \
  -d "grant_type=authorization_code" \
  -d "client_id=<client-id>" \
  -d "client_secret=<client-secret>" \
  -d "code=AUTHORIZATION_CODE" \
  -d "redirect_uri=http://localhost:3000/sso/"

# 4. Verify ID token claims
```

### SSO Endpoint Flow — Web (browser)

```
1. User clicks "Login with SSO" → frontend calls GET /api/v2.1/auth/oidc/login
2. Backend returns authorization URL (redirect_uri = /sso/)
3. Frontend redirects to OIDC provider
4. User authenticates with OIDC provider
5. OIDC provider redirects to: https://domain/sso/?code=xxx&state=yyy
6. Frontend /sso/ page extracts code, POSTs to /api/v2.1/auth/oidc/callback
7. Backend exchanges code for tokens, creates session, returns session token
8. Frontend stores token in localStorage, redirects to dashboard
```

### SSO Endpoint Flow — Desktop client (Seafile v9+)

The Seafile desktop client uses a browser-based OAuth flow with a **pending token + polling**
mechanism (matches seahub's `ClientSSOToken` design). The server advertises support via the
`client-sso-via-local-browser` feature in `/api2/server-info`.

```
1.  Desktop client calls GET /api2/server-info
2.  Server responds with features: ["seafile-basic", "seafile-pro", "file-search",
    "client-sso-via-local-browser"]
3.  Client detects client-sso-via-local-browser → calls POST /api2/client-sso-link
4.  Server creates a pending token T (160-bit random, 15-min TTL) and returns:
    {"link": "https://domain/client-sso/T/"}
    The client parses T from the last path segment of the link URL. The path
    /client-sso/T/ matches seahub's reverse('client_sso', args=[token]).
5.  Client opens the returned link in the system browser
    Client simultaneously begins polling every ~3 seconds:
    GET /api2/client-sso-link/T/
    (token is a path segment, matching seahub's URL pattern)
6.  Server (handleOAuthLogin / GET /client-sso/:token/) extracts token=T from path,
    stores it in the OIDC state parameter, generates authorization URL
    (redirect_uri = /oauth/callback/) and redirects browser to OIDC provider
7.  User authenticates at OIDC provider
8.  OIDC provider redirects browser to:
    https://domain/oauth/callback/?code=xxx&state=yyy
9.  Server (handleOAuthCallback):
    - Exchanges code for tokens, creates session (API_TOKEN)
    - Extracts pending token T from state, marks it as success with API_TOKEN
    - Sets sesamefs_auth cookie = "email@API_TOKEN" (7 days, httpOnly=false)
    - Redirects browser to / (home page, matches seahub behavior)
10. Client polling GET /api2/client-sso-link/T/ receives:
    {"status": "success", "username": "user@example.com", "apiToken": "API_TOKEN"}
11. Client uses apiToken for all subsequent API calls
```

**Key endpoints:**
- `POST /api2/client-sso-link` — creates the pending token, returns the browser URL with token in path: `{"link": "https://domain/client-sso/T/"}`
- `GET /api2/client-sso-link/T/` — polls for completion (Seafile desktop passes token as path segment), returns `{"status":"waiting"}` or `{"status":"success","username":"...","apiToken":"..."}`
- `GET /client-sso/:token/` — entry point for the browser SSO flow (seahub-compatible path, matches `reverse('client_sso', args=[token])`)
- `GET /oauth/login/` — alias for `/client-sso/` (for direct access without a pending token)
- `GET /oauth/callback/` — server-side code exchange, marks pending token as success, redirects browser to `/`

**Security notes:**
- The pending token is 160-bit random (crypto/rand) — not guessable
- It expires after 15 minutes regardless of authentication outcome
- The `sesamefs_auth` cookie is set with `httpOnly=false` intentionally (embedded WebView needs JS access)
- In a **multi-instance** deployment behind a load balancer, the in-memory `ssoStore` is not shared across instances — a request to the polling endpoint may reach a different instance than the one that processed the callback, causing the client to never receive the token. For multi-instance deployments, the pending token store should be moved to the shared database (Cassandra sessions table).

### Integration Tests

Create `scripts/test-oidc.sh` to automate:
- Discovery document fetch
- Token endpoint availability
- User info endpoint
- Full login flow simulation

---

## Security Considerations

1. **Client Secret Storage**
   - Store in environment variable, NOT in code
   - Use secrets manager in production

2. **Token Storage**
   - Use HttpOnly cookies for session
   - Implement PKCE for added security

3. **Logout** ✅ IMPLEMENTED
   - Single Logout (SLO) via OIDC `end_session_endpoint`
   - Clears local session AND redirects to OIDC provider logout
   - Provider logout endpoint: discovered automatically via `end_session_endpoint` in the OIDC discovery document

---

## Dependencies

- Go OIDC library: `github.com/coreos/go-oidc/v3`
- OAuth2 library: `golang.org/x/oauth2`

---

## Related Documentation

- [ROLES-AND-PERMISSIONS.md](ROLES-AND-PERMISSIONS.md) - Full role hierarchy, permission matrix, implementation status
- [ARCHITECTURE.md](ARCHITECTURE.md) - Overall system architecture
- [CURRENT_WORK.md](../CURRENT_WORK.md) - Current priorities
- [API-REFERENCE.md](API-REFERENCE.md) - API endpoints
- [ENDPOINT-REGISTRY.md](ENDPOINT-REGISTRY.md) - Complete route registry
