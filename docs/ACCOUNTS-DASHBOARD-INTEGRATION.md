# Accounts Dashboard Integration Guide for SesameFS

**Audience:** Accounts team implementing organization provisioning, membership management, plan updates, and quota previews over SesameFS.

**Status:** Current integration contract as of 2026-04-08.

## 1. Design Principle

All SesameFS organizations should be treated as Accounts-managed organizations.

That means:

- Accounts is the single source of truth for organization membership, user lifecycle, organization ownership, and organization-level roles.
- SesameFS remains the source of truth for storage-domain entities such as libraries, groups, shares, quota usage, and internal permission enforcement.
- OIDC remains the login boundary. SesameFS uses Accounts-issued identity claims to attach and reconcile the authenticated user with a local shadow record.
- Tenant org-admin user write flows in SesameFS should be considered legacy or disabled. Accounts should use the platform admin API by default. Platform superadmins still bypass the tenant org-admin lock as an operational fallback, but that should not become the normal Accounts write path.
- The local safeguard `accounts.disable_org_user_writes` is enabled by default.

## 2. What Accounts Must Know About SesameFS

### 2.1 Identity model

- SesameFS persists a local user row per organization in the `users` table.
- Each local user has a `user_id` UUID, `org_id`, `email`, `role`, and `status`.
- OIDC login can attach by `oidc_sub`, but it also falls back to matching by `email` when a user was pre-created locally before first login.
- The practical consequence is that Accounts can pre-create the owner or members in SesameFS and the first OIDC login will reconcile the identity to the existing local row.

### 2.2 Organization role model

SesameFS uses organization-level roles:

- `superadmin`: platform-only role, only valid in the platform organization.
- `owner`: billing authority and highest tenant role.
- `admin`: tenant admin.
- `user`: normal member.
- `readonly`: read-only member.
- `guest`: lowest-privilege member.

`owner` is not a separate field. It is a normal role value with higher privileges.

### 2.2.1 Owner vs admin policy for the Accounts dashboard

- The effective role hierarchy is `superadmin > owner > admin > user > readonly > guest`.
- Both `owner` and `admin` are org-staff-level roles for SesameFS org-admin read surfaces.
- Ownership transfer is a special operation. On the org-admin fallback surface, only the current `owner` or a platform `superadmin` can initiate it.
- On the org-admin fallback surface, only a platform `superadmin` can directly modify or delete the current `owner`.
- Accounts should not rely on the platform admin API to infer which human operator initiated the action. Accounts calls SesameFS with a platform service credential, so Accounts must enforce its own owner-versus-admin product policy in its own backend and UI.

### 2.3 Lifecycle model

User lifecycle is separate from role:

- `status=active`
- `status=deactivated`
- `status=deleted`

Deletion in SesameFS is soft-delete first. The existing lifecycle semantics must be preserved even when Accounts initiates the action.

### 2.4 Quota model

Accounts sets limits. SesameFS enforces them on storage writes (uploads), traffic writes (uploads/downloads), and member creation.

Persisted fields on `organizations`:

| Field | Type | Meaning |
|---|---|---|
| `plan` | string | Opaque display label set by Accounts. **Never read by SesameFS for business logic.** |
| `quota_policy` | `"hard"` or `"soft"` | How SesameFS reacts when a quota is exceeded. Empty defaults to `"hard"`. |
| `billing_cycle` | string | Display-only on SesameFS. Period reset is **always monthly**, regardless of value. |
| `storage_quota` | int64 bytes | Org-level storage cap. |
| `traffic_quota` | int64 bytes | Org-level **combined** upload+download cap for the active period. |
| `traffic_upload_quota` | int64 bytes | Org-level **upload-only** cap for the active period. |
| `traffic_download_quota` | int64 bytes | Org-level **download-only** cap for the active period. |
| `max_users` | int | Org-level member cap. |
| `current_period_started_at` | timestamp UTC | Anchor of the active traffic-quota period. |
| `current_period_ends_at` | timestamp UTC | Anchor of when the period rolls over and traffic counters reset. |

Persisted per-user quota fields on `users`:

| Field | Type | Meaning |
|---|---|---|
| `quota_total` | int64 bytes | Per-user storage cap. Validated against the org's `storage_quota` on write ([internal/api/v2/write_helpers.go:901-912](internal/api/v2/write_helpers.go#L901-L912)): a per-user value cannot exceed the org cap. |
| `traffic_upload_quota` | int64 bytes | Per-user upload cap for the active period. Enforced ([internal/traffic/checker.go:164-197](internal/traffic/checker.go#L164-L197)). |
| `traffic_download_quota` | int64 bytes | Per-user download cap for the active period. Enforced. |

There is **no per-user combined `traffic_quota`** field. Per-user traffic enforcement is upload-only and download-only.

A per-user quota value of `<= 0` means "no per-user override; the org-level cap applies". The validation helper accepts any non-positive value identically and never writes a per-user cap above the org cap.

#### 2.4.1 Units and the meaning of `<= 0`

- All byte values are **decimal**. `1 GB = 1_000_000_000` bytes. `1 TB = 1_000_000_000_000` bytes. SesameFS does not use base-1024.
- A quota field with value `<= 0` (typical conventions: `-1` or `0`) is **not enforced by SesameFS**. The dimension behaves as on-demand: writes proceed, traffic accumulates, members are created. SesameFS will not block.
- Important: `<= 0` does **not** mean "the user has no upper limit at all". It means "SesameFS does not gate this; whatever overage occurs is Accounts' responsibility to bill or to cap by another mechanism". Accounts is the only system that knows the contractual included tier; SesameFS just records and reports.
- The traffic counters keep accumulating even when the corresponding quota is `<= 0`. Accounts can read them via the endpoints in §5.5 to invoice usage above the included tier.

#### 2.4.2 Combined `traffic_quota` vs separated upload/download — when each applies

All three caps (`traffic_quota`, `traffic_upload_quota`, `traffic_download_quota`) are evaluated in parallel on every transfer. The **most restrictive** result wins. A cap is **skipped entirely** when its value is `<= 0`. Real usage patterns:

- **Free orgs (`quota_policy="hard"`)** typically use the **combined** cap. The built-in free template ships with `traffic_quota = 10 GB`, `traffic_upload_quota = -1`, `traffic_download_quota = -1` ([internal/config/config.go:189-206](internal/config/config.go#L189-L206)). Only the combined cap is enforced; once the org reaches 10 GB of upload+download in the period, **all** transfers are hard-blocked.
- **Paid orgs (`quota_policy="soft"`)** typically use **separated** upload/download caps so Accounts can bill them as distinct line items (egress and ingress are different costs). Accounts sets `traffic_upload_quota` and `traffic_download_quota` to the included tier values and leaves `traffic_quota = -1` (combined cap off). At 80% of either separated cap SesameFS surfaces a warning; at 100% SesameFS does not block and traffic continues flowing — Accounts bills overage based on counter readings.
- Mixed setups are supported but unusual. The contract does not forbid them; the most-restrictive rule applies.

The same most-restrictive evaluation runs against per-user `traffic_upload_quota` and `traffic_download_quota` ([internal/traffic/checker.go:164-197](internal/traffic/checker.go#L164-L197)). A per-user cap that is `<= 0` is skipped.

#### 2.4.3 `quota_policy` — what `hard` and `soft` actually do

| Dimension | `quota_policy="hard"` (free) | `quota_policy="soft"` (paid) |
|---|---|---|
| `storage_quota` | Block uploads when projected usage exceeds the cap. | Allow uploads, surface a warning at ≥ 80%. Never blocks. |
| `traffic_quota` (combined) | Block uploads/downloads when projected usage exceeds the cap. | Allow, warn at ≥ 80%. Never blocks. |
| `traffic_upload_quota` | Block uploads when projected usage exceeds the cap. | Allow, warn at ≥ 80%. Never blocks. |
| `traffic_download_quota` | Block downloads when projected usage exceeds the cap. | Allow, warn at ≥ 80%. Never blocks. |
| `max_users` | Block member creation when current member count is at or above the cap. Hard-blocks in both `hard` and `soft` once a positive cap is hit, because creating an extra member breaks Accounts billing reality ([internal/traffic/checker.go:232-240](internal/traffic/checker.go#L232-L240)). |

A cap of `<= 0` on **any** of these dimensions — including `max_users` — means "SesameFS does not enforce this dimension" ([internal/traffic/checker.go:59-61, 113, 141, 215](internal/traffic/checker.go#L59)). Same on-demand behavior described in §2.4.1.

Empty `quota_policy` is treated as `"hard"` server-side ([internal/traffic/checker.go:36-38](internal/traffic/checker.go#L36-L38)). The default behavior is conservative.

`quota_policy` is a single value for the whole org. It applies to all enforced dimensions. Mixing `hard` for storage and `soft` for traffic is not supported.

#### 2.4.4 Period semantics

- Periods are **always monthly**, no matter what `billing_cycle` says. `billing_cycle` is a display label that Accounts owns; SesameFS does not branch on it.
- `current_period_started_at` and `current_period_ends_at` define the active window for traffic counters. Once `now >= current_period_ends_at`, Accounts is expected to roll the boundary forward via `PUT /admin/organizations/:org_id/`. SesameFS does not roll periods automatically on `UpdateOrganization`.
- On `PUT /admin/organizations/:org_id/` the two period fields **must be sent together**. Sending only one returns 400 ([internal/api/v2/admin.go:765-770](internal/api/v2/admin.go#L765-L770)).
- On `POST /admin/organizations/:org_id/preview-plan-change/` Accounts can send only `current_period_started_at`; SesameFS derives the end with the monthly clamped-month helper ([internal/config/config.go:281-306](internal/config/config.go#L281-L306)). 31-Jan + 1 month = 28-Feb (or 29-Feb in leap years); same logic for other short months.
- Per-period traffic readings live in `traffic_period_usage` and are queried via the endpoints in §5.5. The natural-month traffic reading lives in `traffic_monthly` and is queried with `?month=YYYYMM`. They are not the same table; the period reading respects Accounts-anchored boundaries, the monthly reading respects calendar months UTC.

## 3. Authentication Contract for Accounts

Accounts should authenticate to SesameFS with a dedicated platform service account that has:

- membership in the platform organization `00000000-0000-0000-0000-000000000000`
- role `superadmin`
- an admin-scoped API key

Use the standard header:

```http
Authorization: Token <raw_api_key>
```

Accounts should not use browser session flows for provisioning.

### 3.1 Browser login and SSO handoff for end users

For human browser login into SesameFS, the low-level OIDC start endpoint is:

#### `GET /auth/oidc/login/`

Query parameters:

- `redirect_uri` optional, but should normally be the SesameFS web callback such as `https://files.example.com/sso/`
- `return_url` optional

Response:

```json
{
  "authorization_url": "https://accounts.example.com/openid/authorize?...",
  "redirect_uri": "https://files.example.com/sso/"
}
```

Important current behavior:

- `redirect_uri` must be allowlisted in SesameFS OIDC configuration via `OIDC_REDIRECT_URIS`.
- The standard web callback is `/sso/`. The frontend callback page then exchanges the code with `POST /api/v2.1/auth/oidc/callback/`.
- The current web frontend stores the post-login destination in SesameFS browser `sessionStorage` and uses that client-side value after `/sso/` completes.
- Accounts should not assume that passing `return_url` to `GET /auth/oidc/login/` by itself is enough to drive the final browser redirect after login.
- There is now a dedicated public SesameFS URL whose purpose is "start browser SSO immediately for this user and preserve the next path": `GET /login/sso/?next=/desired/path/`.
- There is currently no first-class support on this endpoint for forwarding IdP-specific parameters such as `login_hint`, `prompt`, or account-selection hints.

HTTP status codes: `200 OK` — returns `authorization_url`. `503 Service Unavailable` — OIDC is not enabled on this SesameFS deployment. `500 Internal Server Error` — failed to generate the authorization URL (e.g. OIDC provider not reachable during URL generation).

Security properties of `GET /login/sso/`:

- The `next` value is validated client-side to a site-relative path only. Absolute URLs and protocol-relative URLs are rejected and collapse to `/`.
- The route clears only the local SesameFS session before starting OIDC. It does not perform IdP logout.
- The actual OIDC redirect target still goes through SesameFS backend validation. The backend-generated authorization URL is based on a `redirect_uri` that must be present in the configured OIDC redirect allowlist.
- The route is safe for one-click handoff from Accounts because it does not accept an external destination and does not trust Accounts to provide a raw IdP URL.

Practical guidance for Accounts today:

1. For service-to-service provisioning and admin operations, use the API-key-backed admin API only.
2. For human browser login into SesameFS, Accounts can now use the direct entrypoint `https://files.example.com/login/sso/?next=/desired/path/`.
3. `GET /login/sso/` clears the current local SesameFS session, preserves the site-relative `next` target, and immediately starts the browser OIDC flow.
4. This local logout does not log the user out of the Accounts IdP. That is intentional: it forces SesameFS to re-authenticate against the current Accounts browser session.
5. The older login shell entrypoint `https://files.example.com/login/?next=/` still works, but it is no longer required for one-click SSO handoff from Accounts.

## 4. Recommended High-Level Flows

### 4.1 Organization bootstrap after signup

When a user signs up in Accounts and activates SesameFS-backed cloud storage:

1. Accounts creates the organization in SesameFS.
2. Accounts sets the current user as the initial `owner`.
3. Accounts stores the returned `org_id`.
4. Accounts optionally updates plan and quotas.
5. On first OIDC login, SesameFS matches the user by email and attaches the OIDC identity.

### 4.2 Ongoing membership management

For add, update, deactivate, delete, restore, and ownership transfer:

- Accounts remains the authoritative writer.
- SesameFS stores and enforces the local shadow state.
- Tenant org-admin UI in SesameFS should not be used as the authoritative write path. When local org-admin writes are disabled, SesameFS should deep-link the operator into Accounts for identity and membership actions instead of attempting a local write.

### 4.3 Plan changes and quota updates

Accounts should use the preview endpoint first, then apply the actual organization update.

Recommended sequence:

1. `POST /api/v2.1/admin/organizations/:org_id/preview-plan-change/`
2. Show impact in Accounts UI.
3. If accepted, `PUT /api/v2.1/admin/organizations/:org_id/`

### 4.4 External URL contract for the SesameFS org-admin frontend

SesameFS can keep local read surfaces for storage-domain information while redirecting identity and membership actions to Accounts.

The configuration field:

```yaml
accounts:
  org_user_management_url: "https://accounts.example.com/orgs/{org_id}/users/"
```

is treated as the base Accounts URL for organization member management.

Important contract rules:

- SesameFS resolves `{org_id}` before exposing the URL to the frontend.
- SesameFS opens all Accounts links in a new tab and shows an external-link icon.
- SesameFS keeps local views for org-member quotas, quota usage, owned libraries, shared libraries, and other storage-domain data.
- SesameFS appends query parameters to the base URL so Accounts can route the operator to the right screen or pre-open the right workflow.
- When `org_user_writes_disabled=true`, the current org-admin frontend already uses this contract in code. Accounts should treat these query parameters as the live UI integration contract, not as a future proposal.

Accounts responsibility for org-admin user management:

- All organization-member identity workflows should now live in Accounts.
- That includes add user, invite users, add admin, transfer ownership, search users, manage user, edit name, edit contact email, activate/deactivate, delete, restore, reset password, and revoke admin.
- SesameFS should remain the read surface for storage-domain data and quota details on the member profile, but not the source of truth for identity writes.
- Accounts should respond to the emitted query parameters by rendering the corresponding organization dashboard view or pre-opening the requested workflow for the target user.

Query parameters SesameFS appends:

- `source=sesamefs-org-admin`
- `view=members|admins|user`
- `action=...`
- `user_email=...` when a specific member is targeted
- `status=...` when the target workflow needs a status or status filter
- `query=...` when the current SesameFS screen is a user search

Recommended action values Accounts should implement:

- `add-user`
- `invite-users`
- `add-admin`
- `transfer-ownership`
- `search-users`
- `manage-user`
- `edit-name`
- `edit-contact-email`
- `set-status`
- `delete-user`
- `restore-user`
- `reset-password`
- `revoke-admin`

Recommended mappings from SesameFS UI to Accounts URLs:

- Org users list root action bar: `view=members`
- Add user: `view=members&action=add-user`
- Invite users: `view=members&action=invite-users`
- Add admin: `view=admins&action=add-admin`
- Transfer ownership: `view=members&action=transfer-ownership`
- Search users result page: `view=members&action=search-users&query=...&status=...`
- User identity page in Accounts: `view=user&action=manage-user&user_email=...`
- Edit display name: `view=user&action=edit-name&user_email=...`
- Edit contact email: `view=user&action=edit-contact-email&user_email=...`
- Activate/deactivate user: `view=user&action=set-status&user_email=...&status=active|deactivated`
- Delete user: `view=user&action=delete-user&user_email=...`
- Restore user: `view=user&action=restore-user&user_email=...`
- Reset password or equivalent credential flow in Accounts: `view=user&action=reset-password&user_email=...`
- Revoke org-admin role: `view=user&action=revoke-admin&user_email=...`

This gives Accounts one stable org-member base route and lets SesameFS derive all external user-management links without hardcoding many different Accounts paths.

Current frontend coverage using this contract:

- Org users list action bar: add user, invite users, transfer ownership.
- Org admins list action bar: add admin.
- Org user profile and side navigation: manage user, edit name, edit contact email.
- Org user row overflow actions: manage user, activate/deactivate, delete, restore, reset password, revoke admin.
- Org user search page: search users with `query` and `status` propagated.

## 5. Endpoints Accounts Should Use

All routes below are rooted at `/api/v2.1`.

### 5.1 Platform admin organization endpoints

#### `GET /admin/organizations/`

List organizations.

Query parameters:

- `page` optional, default `1`
- `per_page` optional, default `25`
- `status` optional: `all`, `active`, `deactivated`, `deleted`

Response fields of interest:

- `organizations[].org_id`
- `organizations[].org_name`
- `organizations[].owner_email`
- `organizations[].owner_name`
- `organizations[].status`
- `organizations[].plan`
- `organizations[].quota`
- `organizations[].quota_usage`
- `organizations[].users_count`
- `organizations[].ctime`

HTTP status codes: `200 OK`. `400 Bad Request` — `status` query param is not one of `all`, `active`, `deactivated`, `deleted`. `403 Forbidden` — caller is not a platform superadmin. `500 Internal Server Error` — database failure.

#### `GET /admin/search-organization/`

Search organizations by name.

Query parameters:

- `query` optional

HTTP status codes: `200 OK` (returns empty array if no match). `403 Forbidden` — caller is not a platform superadmin. `500 Internal Server Error` — database failure.

#### `POST /admin/organizations/`

Create an organization and optionally bootstrap its initial owner.

Request body:

```json
{
  "name": "Acme Storage",
  "org_name": "Acme Storage",
  "storage_quota": 2000000000,
  "owner_email": "owner@acme.com"
}
```

Notes:

- `org_name` is the canonical field. `name` is accepted for compatibility.
- If `owner_email` is present, SesameFS creates a local user with role `owner`.
- The owner must not already exist in `users_by_email`.
- The organization inherits template defaults for plan, quota policy, period, and limits.

Response:

```json
{
  "org_id": "uuid",
  "org_name": "Acme Storage",
  "owner_email": "owner@acme.com",
  "owner_name": "owner",
  "status": "active",
  "deleted_at": null,
  "plan": "free",
  "quota_usage": 0,
  "quota": 2000000000,
  "ctime": "2026-04-08T10:00:00Z",
  "users_count": 1
}
```

HTTP status codes: `201 Created`. `400 Bad Request` — malformed body; `org_name` and `name` are both absent; `owner_email` already exists in `users_by_email`. `403 Forbidden` — caller is not a platform superadmin. `500 Internal Server Error` — database failure.

#### `GET /admin/organizations/:org_id/`

Read the current organization state. This is the **rich single-call source of truth** for an org: configured limits + current usage + member counts in one response. Use it to drive an "org detail" page in the Accounts dashboard.

Response fields ([internal/api/v2/admin.go:319-348](internal/api/v2/admin.go#L319-L348)):

Identity and lifecycle:

- `org_id`
- `org_name`
- `owner_email`, `owner_name`
- `status`
- `deleted_at` (RFC3339 string, or `null` while active)
- `ctime` (RFC3339 string)

Plan and policy:

- `plan` (opaque label)
- `quota_policy` (`"hard"` or `"soft"`; empty defaults to `"hard"`)
- `billing_cycle` (display only)

Configured quotas:

- `storage_quota`
- `traffic_quota`
- `traffic_upload_quota`
- `traffic_download_quota`
- `max_users`

Current usage:

- `quota_usage` (storage bytes currently in use)
- `traffic_combined_used` (bytes upload+download in the active **period**, not calendar month)
- `traffic_upload_used` (bytes upload in the active period)
- `traffic_download_used` (bytes download in the active period)
- `users_count`, `repos_count`, `groups_count`

Period anchors:

- `current_period_started_at` (RFC3339 UTC, or `null` if Accounts has never set it; usage falls back to first-of-month UTC server-side)
- `current_period_ends_at` (RFC3339 UTC, or `null`)

Storage placement (multi-region installations):

- `storage_policy.data_residency` (e.g. `"eu"`, `"us"`)
- `storage_policy.default_region`
- `available_storage_regions[]` (regions configured on this SesameFS deployment that the org is allowed to choose from)

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — org not found. `500 Internal Server Error` — invalid storage policy configuration or database failure.

#### `PUT /admin/organizations/:org_id/`

Update organization plan and quota configuration. Sparse: every field is optional and only the fields present in the body are written ([internal/api/v2/admin.go:596-786](internal/api/v2/admin.go#L596-L786)).

Request body:

```json
{
  "name": "Acme Storage",
  "storage_quota": 500000000000,
  "traffic_quota": 100000000000,
  "traffic_upload_quota": 50000000000,
  "traffic_download_quota": 50000000000,
  "max_users": 25,
  "plan": "pro-monthly",
  "quota_policy": "soft",
  "billing_cycle": "monthly",
  "storage_policy": {
    "data_residency": "eu",
    "default_region": "eu-west-1"
  },
  "current_period_started_at": "2026-04-01T00:00:00Z",
  "current_period_ends_at": "2026-05-01T00:00:00Z"
}
```

Field rules:

- `quota_policy` only accepts `"hard"` or `"soft"`. Anything else returns 400.
- `storage_policy.data_residency` and `storage_policy.default_region` are validated against the deployment's configured regions (the same set returned in `GET` as `available_storage_regions`). Unknown regions return 400.
- `current_period_started_at` and `current_period_ends_at` **must be sent together**. Sending only one returns 400. End must be strictly after start. PUT does not derive the missing field — only `preview-plan-change` does.
- All byte fields are decimal (`1 GB = 1_000_000_000`).

Enforcement behavior after the write:

- SesameFS persists the new limits immediately and uses them for the very next quota pre-check.
- A quota field set to `<= 0` is **not enforced by SesameFS for that dimension**. The dimension behaves as on-demand: writes proceed, traffic accumulates, members are created. SesameFS will record the usage but will not block. This is the "Accounts will bill the overage beyond the included tier" mode — it is **not** "the user is unlimited"; SesameFS just stops gating that dimension.
- `quota_policy="hard"`: when a positive cap is hit, the corresponding write is blocked.
- `quota_policy="soft"`: positive caps surface a warning at ≥ 80% but do not block storage or traffic writes. `max_users` is the exception — it always blocks at the cap regardless of `quota_policy` (see §2.4.3).
- Updating `current_period_started_at` / `current_period_ends_at` rolls the active traffic-quota window. The new period starts with zero traffic counters until usage accumulates (counters live in `traffic_period_usage`, keyed by the period start anchor).

HTTP status codes: `200 OK` (including when the body contains no recognized fields — treated as a no-op). `400 Bad Request` — malformed body; `quota_policy` not `"hard"` or `"soft"`; unknown storage region; `current_period_started_at` sent without `current_period_ends_at` or vice-versa; `current_period_ends_at` is not after `current_period_started_at`. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — org not found. `500 Internal Server Error` — database failure.

#### `POST /admin/organizations/:org_id/preview-plan-change/`

Preview the operational impact of a proposed plan or quota update before applying it.

Request body fields are the same shape as the mutable quota-related fields on `PUT /admin/organizations/:org_id/`:

```json
{
  "plan": "free",
  "quota_policy": "hard",
  "billing_cycle": "monthly",
  "storage_quota": 2000000000,
  "traffic_quota": 10000000000,
  "traffic_upload_quota": -1,
  "traffic_download_quota": -1,
  "max_users": 1,
  "current_period_started_at": "2026-04-01T00:00:00Z",
  "current_period_ends_at": "2026-05-01T00:00:00Z"
}
```

Response:

```json
{
  "org_id": "uuid",
  "safe_to_apply": false,
  "would_exceed_storage": true,
  "would_exceed_traffic": false,
  "would_exceed_upload_traffic": false,
  "would_exceed_download_traffic": false,
  "would_exceed_max_users": true,
  "new_user_creation_would_be_blocked": true,
  "users_to_deactivate_count": 3,
  "writes_would_be_blocked": true,
  "traffic_would_reset_on": "2026-05-01T00:00:00Z",
  "storage_used": 3500000000,
  "traffic_combined_used": 2000000000,
  "traffic_upload_used": 500000000,
  "traffic_download_used": 1500000000,
  "current_members": 7,
  "active_members": 6,
  "protected_active_members": 2,
  "regular_active_members": 4,
  "current": {
    "plan": "pro-monthly",
    "quota_policy": "soft",
    "billing_cycle": "monthly",
    "storage_quota": 500000000000,
    "traffic_quota": 100000000000,
    "traffic_upload_quota": 50000000000,
    "traffic_download_quota": 50000000000,
    "max_users": 25,
    "current_period_started_at": "2026-04-01T00:00:00Z",
    "current_period_ends_at": "2026-05-01T00:00:00Z"
  },
  "proposed": {
    "plan": "free",
    "quota_policy": "hard",
    "billing_cycle": "monthly",
    "storage_quota": 2000000000,
    "traffic_quota": 10000000000,
    "traffic_upload_quota": -1,
    "traffic_download_quota": -1,
    "max_users": 1,
    "current_period_started_at": "2026-04-01T00:00:00Z",
    "current_period_ends_at": "2026-05-01T00:00:00Z"
  },
  "warnings": [
    "Current storage usage is above the proposed storage quota.",
    "Current organization membership exceeds the proposed max_users limit."
  ]
}
```

Semantics:

- `would_exceed_*` evaluates current org usage against the proposed limit. The flag answers "if I apply this body via PUT right now, will this dimension be over its cap?".
- Proposed quota values `<= 0` mean "SesameFS will not enforce this dimension after the change", so the corresponding `would_exceed_*` flag is `false` (no enforcement = nothing to exceed). It does **not** mean usage drops to zero — counters keep their real values; only the gate is removed.
- `new_user_creation_would_be_blocked` matches SesameFS `max_users` semantics: if current membership is at or above the proposed cap, the next member creation will be blocked. `max_users` always hard-blocks regardless of `quota_policy` (see §2.4.3).
- `users_to_deactivate_count` is an **operational estimate** for downgrades. It assumes the owner and admins stay active; only regular members are deactivation candidates. SesameFS does not actually deactivate anyone during preview — applying the PUT does not trigger automatic deactivation either; this number is purely advisory for Accounts UI.
- `writes_would_be_blocked` only applies to storage and traffic writes under proposed `quota_policy="hard"`. Under proposed `"soft"` it is `false` even when `would_exceed_*` is `true`.
- Period: preview accepts only `current_period_started_at`. If the end is omitted, SesameFS derives it via the clamped-month helper ([internal/config/config.go:281-306](internal/config/config.go#L281-L306)). The traffic preview is read from the period anchored at the proposed `current_period_started_at`. If that period is brand-new (no counters yet), traffic-used previews as zero — that is correct behavior, not a bug. (Contrast with `PUT`, which requires both period fields together.)

HTTP status codes: `200 OK`. `400 Bad Request` — malformed body; `quota_policy` not `"hard"` or `"soft"`; `current_period_ends_at` is before `current_period_started_at`. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — org not found. `500 Internal Server Error` — failed to evaluate member stats.

#### `POST /admin/organizations/:org_id/deactivate/`

Set organization status to `deactivated`.

HTTP status codes: `200 OK`. `400 Bad Request` — org is already `deleted`; restore it first before deactivating. `403 Forbidden` — caller is not a platform superadmin; or `org_id` is the platform organization. `404 Not Found` — org not found. `500 Internal Server Error` — database failure.

#### `POST /admin/organizations/:org_id/reactivate/`

Set organization status back to `active`.

HTTP status codes: `200 OK`. `400 Bad Request` — org is not in `deactivated` state. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — org not found. `500 Internal Server Error` — database failure.

#### `DELETE /admin/organizations/:org_id/`

Soft-delete the organization. Grace period and GC cascade happen later.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin; or `org_id` is the platform organization. `404 Not Found` — org not found. `500 Internal Server Error` — database failure.

#### `POST /admin/organizations/:org_id/restore/`

Restore a soft-deleted organization within the grace period.

HTTP status codes: `200 OK`. `400 Bad Request` — org is not in `deleted` state. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — org not found. `500 Internal Server Error` — database failure.

### 5.2 Platform admin organization user endpoints

These are the primary user-management routes Accounts should use.

#### `GET /admin/organizations/:org_id/users/`

List users in an organization.

Query parameters:

- `status` optional: `all`, `active`, `deactivated`, `deleted`

Response fields of interest:

- `email`
- `name`
- `role`
- `status`
- `quota_total`
- `quota_usage`
- `traffic_upload_quota`
- `traffic_download_quota`
- `create_time`
- `last_login`
- `org_id`

HTTP status codes: `200 OK` (returns empty array if no users match). `403 Forbidden` — insufficient permissions. `500 Internal Server Error` — database failure.

#### `POST /admin/organizations/:org_id/users/`

Create a local user shadow row inside the organization ([internal/api/v2/admin_extra_organizations.go:64-129](internal/api/v2/admin_extra_organizations.go#L64-L129)).

Request body:

```json
{
  "email": "member@acme.com",
  "name": "Member Name",
  "password": "ignored"
}
```

Behavior:

- `password` is accepted for compatibility but ignored. SesameFS is OIDC-first and does not manage local passwords.
- `email` must not already exist in `users_by_email` globally (across all orgs); otherwise 400.
- If `name` is empty, SesameFS uses the local-part of `email` as the display name.
- The new user is created with `role="user"` and `status="active"`. Use `PUT` to elevate to `admin` or `owner` afterwards.
- The new user is created with no per-user storage or traffic override (`quota_total`, `traffic_upload_quota`, `traffic_download_quota` are all `<= 0`). The org-level caps gate the user. If Accounts wants a per-user cap, send a follow-up `PUT`.
- This endpoint enforces `max_users`. If the org is at the cap, returns 403 `user limit reached for this organization`.
- Accounts should call this only after it has already created the identity in Accounts.

Response (201 Created):

```json
{
  "email": "member@acme.com",
  "name": "Member Name",
  "status": "active",
  "active": true,
  "is_org_staff": false,
  "quota_usage": 0,
  "quota_total": 0,
  "create_time": "2026-04-21T10:00:00Z",
  "last_login": null,
  "org_id": "uuid"
}
```

HTTP status codes: `201 Created`. `400 Bad Request` — malformed body; `email` is empty; email already exists in `users_by_email` (globally). `403 Forbidden` — caller is not a platform superadmin; or `max_users` cap is reached for the org. `500 Internal Server Error` — database failure.

#### `PUT /admin/organizations/:org_id/users/:email/`

Update a user shadow row. Sparse: only the fields present in the body are written ([internal/api/v2/admin_extra_organizations.go:134-356](internal/api/v2/admin_extra_organizations.go#L134-L356)).

Request body:

```json
{
  "active": true,
  "is_org_staff": true,
  "is_staff": true,
  "name": "New Name",
  "role": "admin",
  "quota_total": 1000000000,
  "traffic_upload_quota": 500000000,
  "traffic_download_quota": 500000000
}
```

Field rules and side effects:

- `active`: `true` → `status="active"` and clears any deletion timestamp. `false` → `status="deactivated"`. To soft-delete, use the `DELETE` endpoint, not `active=false`.
- `role`: accepted values are `admin`, `user`, `readonly`, `guest`, **and `owner` (special)**. Unknown values are silently ignored (the role does not change). `superadmin` is platform-only and cannot be set via this endpoint.
- `is_org_staff` / `is_staff`: legacy boolean toggles for the `admin` role. `true` promotes a non-staff role to `admin`; `false` demotes `admin` back to `user`. They are aliases. If both are present, `is_org_staff` wins.
- `name`: empty string is ignored (the name does not change). Use a non-empty string to update.
- `quota_total`: per-user storage cap in bytes. Validated against the org's `storage_quota`; if both are positive and the requested value exceeds the org cap, returns 400 ([internal/api/v2/write_helpers.go:901-912](internal/api/v2/write_helpers.go#L901-L912)). Any `<= 0` value is accepted and means "no per-user override; the org cap applies".
- `traffic_upload_quota` and `traffic_download_quota`: per-user period caps. Validated against the org's corresponding caps; if both are positive and the requested value exceeds the org cap, returns 400. Also validated against the org's combined `traffic_quota` (each direction must fit, and if both are set their sum must not exceed the combined cap). `<= 0` means SesameFS does not enforce that direction for this user (org cap still applies).

Special behavior — **`role="owner"` is the canonical ownership-transfer**:

- This is **not** a normal role update. Sending `role="owner"` triggers the same ownership-transfer logic as `PUT /org/:org_id/admin/transfer-ownership/`, but is the recommended path for Accounts because it is on the platform admin surface (no tenant-side authorization required).
- Effect: the current `owner` (if any) is demoted to `admin` in the same batch; the target user becomes `owner`. If the target user's current role is below `admin`, SesameFS **forces them up to `admin` first** before promoting to owner — so a `user` can be made owner directly via this endpoint without a prior promote-to-admin call.
- Sessions of the demoted owner are invalidated (they must reauthenticate to get the new admin scope).
- If the target is already the current owner, the call is a no-op (still 200 OK).
- This is the primary ownership-transfer path. The `/org/:org_id/admin/transfer-ownership/` route in §5.4 is a fallback for tenant-side flows.

Side effects on status changes:

- Activate/deactivate runs the same lifecycle side effects as the dedicated lifecycle endpoints: invalidates sessions and API keys when the status crosses to non-usable; reactivation rehydrates them.
- Demoting from `owner` or `admin` to a lower role invalidates that user's sessions (so the new role takes effect immediately rather than after token expiry).

Response (200 OK):

```json
{
  "email": "member@acme.com",
  "name": "New Name",
  "role": "admin",
  "status": "active",
  "active": true,
  "is_org_staff": true,
  "quota_usage": 1234567,
  "quota_total": 1000000000,
  "traffic_upload_quota": 500000000,
  "traffic_download_quota": 500000000,
  "org_storage_quota": 500000000000,
  "org_traffic_quota": 100000000000,
  "org_traffic_upload_quota": 50000000000,
  "org_traffic_download_quota": 50000000000,
  "create_time": "2026-04-01T10:00:00Z",
  "last_login": "2026-04-21T09:15:00Z",
  "org_id": "uuid"
}
```

The `org_*` fields in the response are the **org-level ceilings** ([internal/api/v2/admin_extra_organizations.go:348-351](internal/api/v2/admin_extra_organizations.go#L348-L351)). They are returned so Accounts knows the maximums it may set on per-user fields in subsequent calls without round-tripping through `GET /admin/organizations/:org_id/`.

HTTP status codes: `200 OK`. `400 Bad Request` — malformed body; invalid `role` value; per-user quota exceeds org cap (storage or traffic). `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — user not found by email or not in the target org. `500 Internal Server Error` — database failure (including batch execution failure during ownership transfer).

#### `DELETE /admin/organizations/:org_id/users/:email/`

Soft-delete the user inside the specified organization.

Behavior:

- preserves existing SesameFS lifecycle semantics
- invalidates sessions and API keys
- marks the user `deleted` for grace-period handling

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — user not found in the target org. `500 Internal Server Error` — database failure.

### 5.3 Platform admin global user endpoints

These are useful when the Accounts dashboard needs lookup or restore by email across orgs.

#### `GET /admin/users/`

List users globally.

Query parameters:

- `page`
- `per_page`
- `status`

HTTP status codes: `200 OK`. `400 Bad Request` — invalid `status` filter. `403 Forbidden` — caller is not a platform superadmin. `500 Internal Server Error` — database failure.

#### `GET /admin/search-user/`

Search users globally.

Query parameters:

- `query`
- `page`
- `per_page`
- `org_id` optional restriction for superadmin callers

HTTP status codes: `200 OK` (returns empty array when `query` is empty or no match). `403 Forbidden` — caller is not a platform superadmin. `500 Internal Server Error` — database failure.

#### `GET /admin/users/:email/`

Get one user by email.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — user not found.

#### `PUT /admin/users/:email/`

Update one user by email.

Supported fields:

- `role`
- `name`
- `quota_total`
- `traffic_upload_quota`
- `traffic_download_quota`
- `is_active`
- `is_staff`

HTTP status codes: `200 OK`. `400 Bad Request` — malformed body; invalid `role`; quota validation fails. `403 Forbidden` — caller is not a platform superadmin; or assigning `superadmin` to a non-platform-org user. `404 Not Found` — user not found. `500 Internal Server Error` — database failure.

#### `DELETE /admin/users/:email/`

Soft-delete one user by email.

HTTP status codes: `200 OK`. `400 Bad Request` — caller is attempting to delete their own account (self-delete is blocked). `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — user not found. `500 Internal Server Error` — database failure.

#### `PUT /admin/users/:email/restore/`

Restore one soft-deleted user by email.

This matters because there is currently no `/admin/organizations/:org_id/users/:email/restore/` route. If Accounts needs restore through the admin surface, it should use the global restore-by-email route.

HTTP status codes: `200 OK`. `400 Bad Request` — identifier is not an email address; user is not in `deleted` state. `403 Forbidden` — caller is not a platform superadmin. `404 Not Found` — user not found. `500 Internal Server Error` — database failure.

### 5.4 Superadmin fallback routes under `/org`

Platform superadmin still bypasses the tenant org-admin lock and can use org-admin routes as an operational fallback.

These are acceptable fallback cases today:

#### `PUT /org/:org_id/admin/transfer-ownership/`

Request body:

```json
{
  "new_owner": "new-owner@example.com"
}
```

Behavior:

- platform superadmin may bootstrap ownership if the org has no owner
- current owner is demoted to `admin`
- new owner must already exist in the org and be at least `admin`

HTTP status codes: `200 OK` (including when target is already the owner — no-op). `400 Bad Request` — `new_owner` parameter missing; new owner is not at least `admin` in the org. `401 Unauthorized` — caller not authenticated. `403 Forbidden` — insufficient permissions; or `accounts.disable_org_user_writes=true` and caller is not a platform superadmin. `404 Not Found` — new owner user not found in the org. `500 Internal Server Error` — database failure.

#### `PUT /org/:org_id/admin/users/:email/restore/`

Restore a soft-deleted user within a specific organization. Use this only when the org-specific context is needed and the global restore-by-email route is not convenient.

HTTP status codes: `200 OK`. `400 Bad Request` — user is not in `deleted` state. `401 Unauthorized` — caller not authenticated. `403 Forbidden` — insufficient permissions; or `accounts.disable_org_user_writes=true`. `404 Not Found` — user not found in the org. `500 Internal Server Error` — database failure.

### 5.5 Reading usage for billing and dashboards

These endpoints exist so Accounts can build billing rollups, top-consumer tables, and time-series charts without scraping primary tables. They are pure reads; they never mutate state.

All counter-backed values are decimal bytes. All timestamps are RFC3339 UTC.

#### Single-call rich snapshot

For an "org detail" dashboard view, prefer **`GET /admin/organizations/:org_id/`** (§5.1). It already returns configured limits + current usage + member counts in one call, so most dashboards do not need to combine the time-series endpoints below with a separate config read.

#### `GET /admin/statistics/total-storage`

Time-series of platform-wide total storage in use ([internal/api/v2/admin_extra_stats.go:322-342](internal/api/v2/admin_extra_stats.go#L322-L342)).

Query parameters:

- `start` — `YYYY-MM-DD` or `YYYY-MM-DD HH:MM:SS`. Truncated to midnight UTC. Default: 7 days ago.
- `end` — same formats. Default: today (UTC).
- `group_by` — `day`, `week`, `month`. Default: `day`.

Response:

```json
[
  {"datetime": "2026-04-14T00:00:00+00:00", "total_storage": 12345678901},
  {"datetime": "2026-04-15T00:00:00+00:00", "total_storage": 12389901002},
  ...
]
```

Notes:

- Reconstructed by walking backwards from current platform counter using daily deltas in `storage_daily_delta`. The earliest date for which history is meaningful is the date the daily-delta table started receiving writes.
- Always returns one row per bucket in the requested range. Buckets with no recorded delta carry the prior value forward.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin.

#### `GET /admin/statistics/system-traffic`

Time-series of platform-wide traffic broken down by source/direction ([internal/api/v2/admin_extra_stats.go:354-362](internal/api/v2/admin_extra_stats.go#L354-L362)).

Query parameters: `start`, `end`, `group_by` (same as above).

Response:

```json
[
  {
    "datetime": "2026-04-14T00:00:00+00:00",
    "sync-file-upload": 0,
    "sync-file-download": 0,
    "web-file-upload": 1234567,
    "web-file-download": 9876543,
    "link-file-upload": 0,
    "link-file-download": 543210
  },
  ...
]
```

Notes:

- The six columns are fixed and always present; zeros are returned when there is no traffic of that type in the bucket.
- "sync" = desktop client, "web" = browser via web upload, "link" = anonymous via share/upload links. Combined upload/download for any direction = sum of `*-upload` or `*-download` columns.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin.

#### `GET /admin/statistics/active-users`

Time-series of distinct users that recorded any traffic in each bucket ([internal/api/v2/admin_extra_stats.go:344-352](internal/api/v2/admin_extra_stats.go#L344-L352)).

Query parameters: `start`, `end`, `group_by`.

Response:

```json
[
  {"datetime": "2026-04-14T00:00:00+00:00", "count": 12},
  ...
]
```

Notes:

- "Active" = generated traffic in the bucket. A user that only browsed (no transfer) does not count.
- A user counts once per bucket regardless of status at query time. Deactivated users still appear in historical buckets where they were active.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin.

#### `GET /admin/statistics/org-traffic`

Per-org traffic totals for a single calendar month — the right endpoint for "top consumers by org" tables ([internal/api/v2/admin_extra_stats.go:519-580](internal/api/v2/admin_extra_stats.go#L519-L580)).

Query parameters:

- `month` — `YYYYMM`. Default: current month UTC.
- `page` — default `1`.
- `per_page` — default `25`. Capped at 100 server-side.
- `order_by` — `total_bytes`, `upload_bytes`, `download_bytes`, `org_name`. Append `_asc` or `_desc` (default `_desc`). Empty defaults to `total_bytes_desc`.

Response:

```json
{
  "org_traffic_list": [
    {
      "org_id": "uuid",
      "name": "Acme Storage",
      "upload_bytes": 1234567,
      "download_bytes": 9876543,
      "total_bytes": 11111110
    }
  ],
  "has_next_page": true
}
```

Notes:

- Reads `traffic_monthly` (calendar months UTC), **not** `traffic_period_usage` (Accounts-anchored periods). This endpoint is for billing rollups against the natural month, not against the org's quota period.
- All orgs are listed. Orgs with zero traffic in the month appear with all zeros.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin.

#### `GET /admin/statistics/user-traffic`

Per-user traffic totals for a single calendar month, broken down by source/direction ([internal/api/v2/admin_extra_stats.go:582-662](internal/api/v2/admin_extra_stats.go#L582-L662)).

Query parameters:

- `month` — `YYYYMM`. Default: current month UTC.
- `org_id` — optional. When omitted, aggregates across all orgs (uses the platform aggregate partition for speed; falls back to per-org scan if the aggregate is incomplete).
- `page`, `per_page`, `order_by` — same as `org-traffic`. `order_by` accepts column names from the response below; default `link_file_download_desc`.

Response:

```json
{
  "user_monthly_traffic_list": [
    {
      "email": "user@acme.com",
      "name": "User Name",
      "sync_file_upload": 0,
      "sync_file_download": 0,
      "web_file_upload": 1234567,
      "web_file_download": 9876543,
      "link_file_upload": 0,
      "link_file_download": 543210
    }
  ],
  "has_next_page": true
}
```

Notes:

- Same six-column breakdown as `system-traffic`, per user.
- Use `org_id` to scope a billing-per-org breakdown; omit it for a platform-wide top-N report.

HTTP status codes: `200 OK`. `400 Bad Request` — `org_id` present but not a valid UUID. `403 Forbidden` — caller is not a platform superadmin.

#### `GET /admin/statistics/file-operations`

**Currently stubbed.** Returns one row per bucket with `added`, `deleted`, `modified`, `visited` all zero ([internal/api/v2/admin_extra_stats.go:301-320](internal/api/v2/admin_extra_stats.go#L301-L320)). Accounts should not depend on this endpoint for billing or display until the file-operations counter pipeline is implemented. The endpoint exists to keep the route reserved.

HTTP status codes: `200 OK`. `403 Forbidden` — caller is not a platform superadmin.

#### `GET /org/admin/info/`

Org-scoped rich snapshot — same shape concept as `GET /admin/organizations/:org_id/` but called with org-scoped credentials (the org-admin SPA uses it). Accounts can use it on behalf of a tenant if it is logged in as an org admin, but for platform service-account workflows prefer the platform admin variant ([internal/api/v2/org_admin.go:246-355](internal/api/v2/org_admin.go#L246-L355)).

Response includes (in addition to the fields documented in §9.2):

- Identity and counts: `org_id`, `org_name`, `ctime`, `repos_count`, `groups_count`, `member_usage`, `member_quota`, `active_members`
- Storage: `storage_quota`, `storage_usage`, `total_files_count`
- Plan and policy: `plan`, `quota_policy`, `billing_cycle`, `storage_policy`, `available_storage_regions`
- Period anchors: `current_period_started_at`, `current_period_ends_at`
- Period traffic (Accounts-anchored): `traffic_quota`, `traffic_upload_quota`, `traffic_download_quota`, `traffic_combined_used`, `traffic_upload_used`, `traffic_download_used`
- Calendar-month traffic: `traffic_month_total`, `traffic_month_upload`, `traffic_month_download`
- Year-to-date traffic (sum of calendar months Jan–current of the current UTC year): `traffic_year_total`, `traffic_year_upload`, `traffic_year_download`
- `max_users`
- The org-user-management authority fields covered in §9.2.

The two traffic blocks (period vs calendar month vs year-to-date) are intentional. Use:

- `traffic_*_used` to evaluate against the active **quota period** (when does the user run out of their bucket).
- `traffic_month_*` for billing rollups against the **calendar month**.
- `traffic_year_*` for compliance / yearly-cap displays.

HTTP status codes: `200 OK`. `403 Forbidden` — insufficient permissions (caller must be at least org admin). `404 Not Found` — org not found. `500 Internal Server Error` — invalid storage policy configuration.

## 6. Endpoints Accounts Should Not Use for Tenant-Admin User Writes

When `accounts.disable_org_user_writes=true`, tenant org-admin callers should treat these routes as disabled for user lifecycle writes:

- `POST /org/:org_id/admin/users/`
- `PUT /org/:org_id/admin/users/:email/`
- `DELETE /org/:org_id/admin/users/:email/`
- `PUT /org/:org_id/admin/users/:email/restore/`
- `PUT /org/:org_id/admin/users/:email/set-password/`
- `POST /org/:org_id/admin/import-users/`
- `POST /org/:org_id/admin/invite-users/`
- `PUT /org/:org_id/admin/transfer-ownership/`

Tenant org-admin read routes remain useful for SesameFS UI, but Accounts should not build its core dashboard on those write paths.

## 7. Seafile Compatibility Notes

SesameFS keeps broad Seafile-style API compatibility, but Accounts should understand the intentional differences:

- `password` fields can appear in requests for compatibility, but SesameFS is OIDC-first and ignores local password management in org user flows.
- Email-based user addressing is widely supported for Seahub and seafile-js compatibility.
- `org_name` and `name` can both appear in organization creation payloads. Prefer `org_name`.
- Some responses retain Seafile naming such as `quota_total`, `quota_usage`, `create_time`, `page_next`, or `is_org_staff`.

## 8. Quota and Plan Guidance for Accounts

### 8.1 How to update quotas

Use `PUT /admin/organizations/:org_id/`.

Typical changes Accounts will own:

- upgrade or downgrade `plan`
- switch `quota_policy`
- adjust `storage_quota`
- adjust `traffic_quota`
- adjust directional traffic quotas
- adjust `max_users`
- roll the current quota period boundaries forward

Operational interpretation:

- `storage_quota <= 0` means SesameFS will not block storage writes for storage-cap reasons.
- `traffic_quota <= 0` means SesameFS will not block on combined traffic.
- `traffic_upload_quota <= 0` and `traffic_download_quota <= 0` mean no per-direction block.
- `max_users <= 0` means SesameFS will not block member creation by count.
- Accounts billing may still charge for storage, traffic, or members beyond the default included tier even when SesameFS is configured with unlimited enforcement.

### 8.2 How to evaluate a downgrade safely

Use `POST /admin/organizations/:org_id/preview-plan-change/` first.

Recommended UI logic inside Accounts:

1. Present warnings if any `would_exceed_*` field is `true`.
2. If `writes_would_be_blocked` is `true`, explain that hard enforcement will block write operations immediately.
3. If `new_user_creation_would_be_blocked` is `true`, explain that membership is already at or above the proposed cap.
4. If `users_to_deactivate_count > 0`, explain the operational impact separately from quota enforcement.

### 8.3 Monthly period semantics

Traffic quota periods are always monthly, regardless of `billing_cycle`. SesameFS does not branch on `billing_cycle` for enforcement.

The two endpoints have different rules for the period fields:

- **`PUT /admin/organizations/:org_id/`**: `current_period_started_at` and `current_period_ends_at` must be sent **together**. Sending only one returns 400. End must be strictly after start. PUT does not derive the missing field — Accounts is the source of truth ([internal/api/v2/admin.go:765-770](internal/api/v2/admin.go#L765-L770)).
- **`POST /admin/organizations/:org_id/preview-plan-change/`**: `current_period_started_at` may be sent alone. SesameFS derives the end via the clamped-month helper (31-Jan + 1 month = 28-Feb or 29-Feb in leap years; same logic for other short months) ([internal/config/config.go:281-306](internal/config/config.go#L281-L306)).

Why the difference: PUT mutates persistent state; ambiguity there would corrupt enforcement. Preview is read-only and used in interactive Accounts UI flows, so it is convenient to let it derive defaults.

When the active period elapses (`now >= current_period_ends_at`), Accounts is expected to call `PUT /admin/organizations/:org_id/` with the next period's start and end. SesameFS does not roll periods automatically. Counters are keyed by the period start anchor (in `traffic_period_usage`), so a brand-new period starts at zero usage; the previous period's counters are preserved for historical reads.

## 9. Existing Local SesameFS Safeguards Relevant to Accounts

### 9.1 Org-admin user writes can be disabled

This safeguard is enabled by default.

Configuration:

```yaml
accounts:
  disable_org_user_writes: true
```

Environment override:

```bash
ACCOUNTS_DISABLE_ORG_USER_WRITES=true
```

When enabled:

- tenant org-admin membership writes are blocked server-side
- the org-admin frontend should replace identity and membership actions with external Accounts links whenever `accounts_org_user_management_url` is available
- platform superadmins may still use org-admin user write routes when there is no admin equivalent

### 9.2 Org-admin info now exposes the authority flag

`GET /org/admin/info/` includes:

- `org_user_writes_disabled`
- `user_management_authority`
- `accounts_org_user_management_url`

This is intended for the SesameFS org-admin frontend so it can suppress local membership write actions and derive external Accounts links from a single base URL.

## 10. Safe Local Changes to Keep or Extend in SesameFS

These changes are safe and aligned with the Accounts-managed design:

- keep the platform admin organization and user APIs as the canonical write surface for Accounts
- keep org-admin read surfaces for SesameFS UI
- keep OIDC attach-by-email behavior so pre-created owners and members reconcile on first login
- keep the org-admin frontend aware of `org_user_writes_disabled`
- keep the org-admin frontend using `accounts_org_user_management_url` as a base URL for identity and membership deep links into Accounts
- keep the preview endpoint in front of quota and plan changes

## 11. Recommended Follow-Up Work

These are not required for the current Accounts dashboard, but they are useful follow-up work:

- add explicit audit tagging for requests initiated by Accounts service credentials
- add webhook or polling reconciliation for out-of-band Accounts-side user changes if direct API orchestration is not always synchronous
- add an org-scoped admin restore route under `/admin/organizations/:org_id/users/:email/restore/` if Accounts wants full symmetry in the platform admin surface
- add an explicit Accounts-oriented organization membership API if future ergonomics matter more than Seafile compatibility
- extend the existing `/login/sso/` handoff route only if future product requirements need safe support for `login_hint`, `prompt`, or account-selection hints

## 12. Current Boundary Summary

Accounts owns:

- organization creation bootstrap
- owner bootstrap
- plan and quota updates
- member creation and deletion
- role updates
- ownership transfers
- restore decisions

SesameFS owns:

- local shadow user persistence
- libraries
- groups
- shares and upload links
- quota usage measurement
- storage and traffic enforcement
- deletion side effects and GC semantics

That is the intended contract for the current phase.