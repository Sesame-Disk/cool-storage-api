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

Accounts sets limits. SesameFS enforces them.

Important fields on `organizations`:

- `plan`: opaque display string. Do not use it as a source of backend business logic.
- `quota_policy`: `hard` or `soft`.
- `storage_quota`
- `traffic_quota`
- `traffic_upload_quota`
- `traffic_download_quota`
- `max_users`
- `current_period_started_at`
- `current_period_ends_at`
- `billing_cycle`

Important rules:

- Storage and traffic contract units are decimal bytes. `GB` and `TB` mean base-1000 units.
- `storage_quota <= 0` means no SesameFS storage cap.
- `traffic_quota <= 0`, `traffic_upload_quota <= 0`, and `traffic_download_quota <= 0` mean no SesameFS traffic cap for that dimension.
- `max_users <= 0` means no SesameFS member cap.
- Traffic quota periods are monthly, even if `billing_cycle="annual"`.
- If `current_period_ends_at` is omitted, SesameFS derives the next boundary from `current_period_started_at` using the monthly clamped-month helper.
- `<= 0` means "unlimited inside SesameFS enforcement", not "free in billing". Accounts billing may still charge overages or extra included capacity beyond the default tier while leaving SesameFS with no hard cap.

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

#### `GET /admin/search-organization/`

Search organizations by name.

Query parameters:

- `query` optional

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

#### `GET /admin/organizations/:org_id/`

Read the current organization state.

Response fields of interest:

- `org_id`
- `org_name`
- `status`
- `plan`
- `quota_policy`
- `billing_cycle`
- `storage_quota`
- `quota_usage`
- `traffic_quota`
- `traffic_upload_quota`
- `traffic_download_quota`
- `traffic_combined_used`
- `traffic_upload_used`
- `traffic_download_used`
- `current_period_started_at`
- `current_period_ends_at`
- `max_users`
- `users_count`
- `repos_count`
- `groups_count`
- `owner_email`
- `owner_name`

#### `PUT /admin/organizations/:org_id/`

Update organization plan and quota configuration.

Request body fields are all optional:

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
  "current_period_started_at": "2026-04-01T00:00:00Z",
  "current_period_ends_at": "2026-05-01T00:00:00Z"
}
```

Behavior:

- Accounts is expected to be the authoritative writer for these fields.
- SesameFS immediately persists the new limits and uses them for enforcement.
- Any quota field sent as `<= 0` is treated as unlimited by SesameFS for that dimension.
- `quota_policy="hard"` means quota excess blocks operations.
- `quota_policy="soft"` means quota excess warns but does not hard-block storage and traffic writes.

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

- `would_exceed_*` evaluates current org usage against proposed limits.
- Proposed quota values `<= 0` are treated as unlimited, so the corresponding `would_exceed_*` flag remains `false`.
- `new_user_creation_would_be_blocked` matches SesameFS `max_users` semantics. If current membership is equal to the proposed limit, new user creation will already be blocked.
- `users_to_deactivate_count` is an operational estimate that assumes owner and admins remain active first and regular members are the deactivation candidates.
- `writes_would_be_blocked` is only about storage and traffic write operations under proposed `quota_policy="hard"`.
- If Accounts proposes a new `current_period_started_at`, the traffic preview is evaluated against that period start. A new period with no counters will preview as zero traffic used.

#### `POST /admin/organizations/:org_id/deactivate/`

Set organization status to `deactivated`.

#### `POST /admin/organizations/:org_id/reactivate/`

Set organization status back to `active`.

#### `DELETE /admin/organizations/:org_id/`

Soft-delete the organization. Grace period and GC cascade happen later.

#### `POST /admin/organizations/:org_id/restore/`

Restore a soft-deleted organization within the grace period.

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

#### `POST /admin/organizations/:org_id/users/`

Create a local user shadow row inside the organization.

Request body:

```json
{
  "email": "member@acme.com",
  "name": "Member Name",
  "password": "ignored"
}
```

Notes:

- `password` is accepted for compatibility but ignored. SesameFS does not manage local passwords.
- This endpoint enforces `max_users`.
- Accounts should call this only after it has already created the identity in Accounts or during a tightly controlled bootstrap flow.

#### `PUT /admin/organizations/:org_id/users/:email/`

Update a user shadow row.

Supported request body fields:

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

Important behavior:

- `role="owner"` triggers ownership-transfer logic, not a trivial role update.
- `active=false` maps to `status=deactivated`.
- User quota fields are validated against organization-level quota ceilings.

#### `DELETE /admin/organizations/:org_id/users/:email/`

Soft-delete the user inside the specified organization.

Behavior:

- preserves existing SesameFS lifecycle semantics
- invalidates sessions and API keys
- marks the user `deleted` for grace-period handling

### 5.3 Platform admin global user endpoints

These are useful when the Accounts dashboard needs lookup or restore by email across orgs.

#### `GET /admin/users/`

List users globally.

Query parameters:

- `page`
- `per_page`
- `status`

#### `GET /admin/search-user/`

Search users globally.

Query parameters:

- `query`
- `page`
- `per_page`
- `org_id` optional restriction for superadmin callers

#### `GET /admin/users/:email/`

Get one user by email.

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

#### `DELETE /admin/users/:email/`

Soft-delete one user by email.

#### `PUT /admin/users/:email/restore/`

Restore one soft-deleted user by email.

This matters because there is currently no `/admin/organizations/:org_id/users/:email/restore/` route. If Accounts needs restore through the admin surface, it should use the global restore-by-email route.

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

#### `PUT /org/:org_id/admin/users/:email/restore/`

Restore a soft-deleted user within a specific organization. Use this only when the org-specific context is needed and the global restore-by-email route is not convenient.

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

Traffic quota periods are monthly, regardless of annual or monthly billing.

If Accounts sends only `current_period_started_at`, SesameFS can derive the end date.

If Accounts sends both `current_period_started_at` and `current_period_ends_at`, they should be coherent.

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