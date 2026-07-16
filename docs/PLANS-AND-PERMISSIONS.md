# Plans & Permissions — SesameFS

**Last Updated**: 2026-03-30
**Status**: Phase 1 IMPLEMENTED — Phase 2 BACKEND COMPLETE — Phase 3 IN PROGRESS

---

## Problem Statement

Seafile encodes 4 orthogonal concepts into a single `ENABLED_ROLE_PERMISSIONS` dict:

1. **Authorization** (`default`, `guest`) — what can a user do?
2. **Plan/commercial tier** (`personalfree`, `personalpro`, `business`) — what does the org pay for?
3. **Quota exhaustion state** (`restricted`, `pay_restricted`) — is the user over quota?
4. **Owner billing capability** (`pay_restricted_owner`) — is this the billing-responsible owner?

This creates hacks like `pay_restricted_owner` (restricted + `can_view_org=True`) and makes it impossible to change plan limits without inventing new role strings.

SesameFS separates these into **3 independent dimensions**. The owner concept lives within the role hierarchy as a privileged authorization level (same pattern as group ownership), not as a separate dimension.

---

## Architecture: 3 Dimensions

```
+------------------------------------------------------------------+
|  DIMENSION 1: ROLE (users.role)                                  |
|  Authorization — what can this user ATTEMPT?                     |
|  superadmin > owner > admin > user > readonly > guest            |
|  Owner is a privileged admin role: includes billing/upgrade      |
|  capability, same pattern as group owner > admin > member.       |
|                                                                  |
|  DIMENSION 2: ENFORCEMENT PROFILE (organizations.quota_policy)   |
|  Org behavior set — features, limits, enforcement mode.          |
|  Keyed by quota_policy, NOT by plan name.                        |
|  hard = free behavior (feature limits + hard quota blocks)       |
|  soft = paid behavior (all features + soft quota warnings)       |
|                                                                  |
|  DIMENSION 3: QUOTA STATE (derived at runtime)                   |
|  Current usage vs limits — is the org/user over quota?           |
|  Computed from DB counters vs org quota fields.                  |
|  Never changes roles or features. Only blocks data operations.   |
+------------------------------------------------------------------+

Capability resolution:
  can_do(action) = role_allows(action) AND enforcement_profile_allows(action)

Quota enforcement:
  if over_quota AND quota_policy == "hard" -> BLOCK operation
  if over_quota AND quota_policy == "soft" -> WARN + ALLOW operation

Plan name:
  Opaque display string. Never read for business logic decisions.
  Stored in DB for display to user. Set by Accounts.
```

---

## Dimension 1: Role Hierarchy

### Values

```go
orgRoleHierarchy := map[OrganizationRole]int{
    "superadmin": 5,  // Platform-level (dedicated platform org)
    "owner":      4,  // Org billing + full admin (NEW)
    "admin":      3,  // Org admin (users, settings, libraries)
    "user":       2,  // Standard member
    "readonly":   1,  // View/download only
    "guest":      0,  // External collaborator
}
```

### Owner vs Admin

The `owner` role follows the **same pattern as group ownership** (see `internal/api/v2/groups.go`). Owner IS an authorization level — it's an admin with additional privileges for billing and org lifecycle.

| Capability | superadmin | owner | admin | user | readonly | guest |
|-----------|:---:|:---:|:---:|:---:|:---:|:---:|
| Manage org users | via admin API | YES | YES | - | - | - |
| Manage org settings | via admin API | YES | YES | - | - | - |
| View billing/subscription | - | YES | - | - | - | - |
| Initiate upgrade (-> Accounts) | - | YES | - | - | - | - |
| Transfer ownership | - | YES | - | - | - | - |
| Delete/deactivate org | YES | YES | - | - | - | - |
| Create libraries | - | YES | YES | YES | - | - |
| Share content | - | YES | YES | YES | - | - |
| Download files | - | YES | YES | YES | YES | YES* |

\* Guest: only from explicitly shared content.

### Why Owner is a Role (not a separate field)

Owner is an authorization level: it defines what the user CAN DO (manage billing, transfer ownership, delete org). These are permission-gated actions, just like "manage users" is gated to admin+. The fact that billing responsibility comes with it is a natural consequence of being the highest org-level authority.

If in the future we need co-owners, billing-viewer, or owner-without-admin, we can extract ownership to a separate field at that point. Today there's no use case for that, and the role model is simpler, consistent with groups, and works with all existing middleware automatically.

### Owner Lifecycle

- **Creation**: The user who creates the org gets `role=owner`. All other users get `role=user` (or from OIDC claim).
- **Transfer**: Owner can transfer ownership to any admin. Same mechanics as group ownership:
  - Old owner -> `role=admin`
  - New owner -> `role=owner`
  - Only 1 owner per org (enforced in code).
- **Superadmin override**: A platform superadmin may also assign or reassign the org owner, including bootstrapping ownership for orgs that currently have no owner. The target user must already belong to the org and be at least `admin`.
- **OIDC mapping**: Add `"owner"` to the OIDC role mapping table in `mapOIDCRole()`.

### Known Issue: OIDC Role Sync vs Manual Overrides

Current policy decision:
- Accounts is the operational authority for org membership, ownership, and quota lifecycle.
- Manual role changes done inside SesameFS should be limited to superadmin repair flows or explicit ownership-transfer flows.
- Automatic OIDC role synchronization must not silently become the primary provisioning path for tenant operations.

Status:
- This remains **technical debt / open issue**.
- The current role-mapping support for OIDC claim values exists, but the long-term reconciliation rule between IdP role claims and manual SesameFS overrides still needs a dedicated design.
- Until that design is implemented, treat manual role changes as the effective source of truth for operational support scenarios.

### Existing checks that work automatically

All existing `HasRequiredOrgRole(role, "admin")` checks return `true` for `owner` because `owner(4) > admin(3)` in the hierarchy. No existing permission middleware needs changes — only new owner-specific checks need to be added (billing, transfer, delete).

### OIDC Role Mapping (updated)

| OIDC Claim Value | SesameFS Role |
|------------------|---------------|
| `owner`, `org_owner` | `owner` |
| `admin`, `administrator`, `tenant_admin` | `admin` |
| `user`, `member` | `user` |
| `readonly`, `read-only`, `viewer` | `readonly` |
| `guest`, `external` | `guest` |

---

## Dimension 2: Enforcement Profiles

### Design Principles

1. **Config defines enforcement behavior by `quota_policy`**, not by plan name.
2. **`plan` is an opaque display string** — never read for business logic. Only shown to user.
3. **DB org row = effective state** for quotas (storage, traffic, max_users). Accounts sets these.
4. **Config = effective state** for feature flags and numeric limits (max_libraries, max_share_links, expiry caps). These are NOT customizable per org — they derive from the enforcement profile.
5. **Accounts = last word** for quota values. Accounts overwrites DB fields. No persistent local overrides.

### Two Classes of Limits

The system has two types of limits with different persistence rules:

| Class | What | Where it lives | Customizable per org? | Who sets it |
|-------|------|----------------|:---:|---|
| **Quotas** | storage_quota, traffic_quota, traffic_upload_quota, traffic_download_quota, max_users | DB columns on organizations | YES | Accounts via API |
| **Feature limits** | max_libraries, max_share_links, max_upload_links, share_link_expire_days_max, upload_link_expire_days_max | Config enforcement profile | NO (per quota_policy) | Config file |
| **Feature flags** | can_add_group, can_invite_guest, can_publish_repo, etc. | Config enforcement profile | NO (per quota_policy) | Config file |

**Rationale**: Quotas are business-critical numbers that Accounts manages per-org (different plans have different storage/traffic). Feature flags and numeric limits are product policy (free users get 3 share links, period) — they don't vary within the same tier. If Accounts ever needs to customize feature limits per org, they can be migrated to the `organizations.settings` map without schema changes.

### Enforcement Profiles Config

```yaml
# config.yaml (new section)
enforcement_profiles:
  hard:
    # Applies to ALL orgs with quota_policy="hard" (free tier)
    features:
      can_add_repo: true
      can_share_repo: true
      can_add_group: false
      can_generate_share_link: true
      can_generate_upload_link: true
      can_send_share_link_mail: false
      can_invite_guest: false
      can_publish_repo: false
      can_use_global_address_book: false
      can_connect_with_desktop_clients: true
      can_connect_with_android_clients: true
      can_connect_with_ios_clients: true
      can_export_files_via_mobile_client: true
    limits:
      max_libraries: 3
      max_share_links: 3
      max_upload_links: 1
      share_link_expire_days_max: 3
      upload_link_expire_days_max: 3

  soft:
    # Applies to ALL orgs with quota_policy="soft" (any paid plan)
    features:
      can_add_repo: true
      can_share_repo: true
      can_add_group: true
      can_generate_share_link: true
      can_generate_upload_link: true
      can_send_share_link_mail: true
      can_invite_guest: true
      can_publish_repo: true
      can_use_global_address_book: true
      can_connect_with_desktop_clients: true
      can_connect_with_android_clients: true
      can_connect_with_ios_clients: true
      can_export_files_via_mobile_client: true
    limits:
      max_libraries: -1               # unlimited
      max_share_links: -1
      max_upload_links: -1
      share_link_expire_days_max: 0    # 0 = no forced limit
      upload_link_expire_days_max: 0
```

### Why Keyed by `quota_policy`, Not Plan Name

All paid plans (Starter, StarterPlus, Business, Enterprise) share the **same features and limits**. The only thing that varies between them is the quota numbers (storage, traffic, max_users) — and those live in DB, set by Accounts per-org.

Organizing config by `quota_policy` instead of plan name means:
- Adding a new plan in Accounts ("Premium", "Enterprise Custom ACME") requires **zero changes** in SesameFS
- Accounts sends `plan="Premium"` (display) + `quota_policy="soft"` (behavior) + quota numbers → done
- No hardcoded plan names anywhere in SesameFS code or config
- `plan` is truly opaque — a display label and nothing more

### Adding Future Tiers

If a new enforcement behavior is needed (e.g., a "trial" tier with soft enforcement but limited features):
1. Add a `trial` profile to `enforcement_profiles` in config
2. Accounts sends `quota_policy="trial"` when provisioning
3. No code changes needed — the resolver reads config generically

---

## Quota Policy

### Field

```sql
ALTER TABLE organizations ADD quota_policy TEXT;  -- "hard" | "soft"
```

### Replaces `isFree()`

Current (fragile — infers enforcement from plan name string):
```go
func isFree(plan string) bool {
    return plan == "" || plan == "free"
}
// Used 11 times in checker.go
```

New (explicit — reads the enforcement field directly):
```go
func isHardEnforcement(quotaPolicy string) bool {
    return quotaPolicy == "" || quotaPolicy == "hard"
}
```

- `""` (empty) defaults to `"hard"` — safe default, no org gets free pass by accident.
- The plan name is **never** read for enforcement decisions. Only `quota_policy` matters.

### Enforcement Matrix

| Scenario | `quota_policy=hard` | `quota_policy=soft` |
|----------|:---:|:---:|
| Storage exceeded | **BLOCK** (403) | ALLOW + warning |
| Traffic exceeded | **BLOCK** (403) | ALLOW + warning |
| Max users exceeded | **BLOCK** (403) | **BLOCK** (403)* |
| 80% threshold | N/A (blocked directly when hit) | WARN via `X-Quota-Warning` header |

\* Max users is always a hard block. If `max_users<=0`, there's no limit in SesameFS. The billing service may still charge for extra users or included overages — but SesameFS only knows whether a hard cap exists.

---

## Capability Resolution

### The 4-Layer Check

Every user-facing action goes through these layers in order:

```
1. AUTH        -> Is the user authenticated? Is their status "active"?
2. ROLE        -> Does the user's role allow this action?
3. PLAN        -> Does the enforcement profile include this feature?
4. QUOTA/LIMIT -> Is there quota/count remaining?
```

If any layer says NO, the action is denied. The response indicates WHY:
- Layer 2 denial: 403 "Permission denied" (you don't have the right role)
- Layer 3 denial: 403 "Feature not available on your plan" + `upgrade_required: true`
- Layer 4 denial: 403 "Quota exceeded" or "Limit reached" + specific reason

### Role Permission Map (Layer 2)

Hardcoded in Go. These define the **ceiling** of what a role can attempt:

| Flag | superadmin | owner | admin | user | readonly | guest |
|------|:---:|:---:|:---:|:---:|:---:|:---:|
| `can_add_repo` | Y | Y | Y | Y | - | - |
| `can_share_repo` | Y | Y | Y | Y | - | - |
| `can_add_group` | Y | Y | Y | Y | - | - |
| `can_generate_share_link` | Y | Y | Y | Y | - | - |
| `can_generate_upload_link` | Y | Y | Y | Y | - | - |
| `can_send_share_link_mail` | Y | Y | Y | Y | - | - |
| `can_invite_guest` | Y | Y | Y | Y | - | - |
| `can_publish_repo` | Y | Y | Y | Y | - | - |
| `can_use_global_address_book` | Y | Y | Y | Y | - | - |
| `can_connect_with_desktop_clients` | Y | Y | Y | Y | Y | - |
| `can_connect_with_android_clients` | Y | Y | Y | Y | Y | - |
| `can_connect_with_ios_clients` | Y | Y | Y | Y | Y | - |
| `can_export_files_via_mobile_client` | Y | Y | Y | Y | Y | - |

**Note**: The role says "you CAN attempt this". The enforcement profile says "your org HAS this".

### Enforcement Profile Feature Map (Layer 3)

Loaded from config, keyed by `quota_policy`:

| Flag | hard (free) | soft (paid) |
|------|:---:|:---:|
| `can_add_repo` | Y | Y |
| `can_share_repo` | Y | Y |
| `can_add_group` | - | Y |
| `can_generate_share_link` | Y | Y |
| `can_generate_upload_link` | Y | Y |
| `can_send_share_link_mail` | - | Y |
| `can_invite_guest` | - | Y |
| `can_publish_repo` | - | Y |
| `can_use_global_address_book` | - | Y |
| `can_connect_with_desktop_clients` | Y | Y |
| `can_connect_with_android_clients` | Y | Y |
| `can_connect_with_ios_clients` | Y | Y |
| `can_export_files_via_mobile_client` | Y | Y |

### Numeric Limits (Layer 4)

Feature limits come from config (enforcement profile). Quotas come from DB (set by Accounts).

| Limit | Source | hard (free) | soft (paid) |
|-------|--------|------|----------|
| `storage_quota` | DB | 2 GB (default) | unlimited (`<=0`) |
| `traffic_quota` (combined/month) | DB | 10 GB (default) | unlimited (`<=0`) |
| `traffic_upload_quota` | DB | no sub-limit (`<=0`) | set by Accounts |
| `traffic_download_quota` | DB | no sub-limit (`<=0`) | set by Accounts |
| `max_users` | DB | 1 (default) | unlimited (`<=0`) |
| `max_libraries` | Config | 3 | unlimited (-1) |
| `max_share_links` | Config | 3 | unlimited (-1) |
| `max_upload_links` | Config | 1 | unlimited (-1) |
| `share_link_expire_days_max` | Config | 3 days | no limit (0) |
| `upload_link_expire_days_max` | Config | 3 days | no limit (0) |

### Resolution (pseudocode)

```go
func ResolveCapabilities(role string, quotaPolicy string, profile *EnforcementProfile) CapabilityResult {
    caps := make(map[string]bool)
    upgradeFeatures := []string{}

    for flag, roleAllows := range RolePermissions[role] {
        profileAllows := profile.Features[flag]
        caps[flag] = roleAllows && profileAllows

        // Track features blocked by plan (not by role) for upgrade CTA
        if roleAllows && !profileAllows {
            upgradeFeatures = append(upgradeFeatures, flag)
        }
    }

    return CapabilityResult{
        Capabilities:    caps,
        UpgradeFeatures: upgradeFeatures,
    }
}
```

---

## Frontend Contract

### What `/api2/account/info/` returns (updated)

```json
{
  "email": "user@example.com",
  "name": "User Name",
  "role": "owner",

  "plan": "Starter",
  "is_org_owner": true,
  "can_upgrade": true,
  "billing_cycle": "monthly",
  "current_period_started_at": "2026-03-15T00:00:00Z",
  "current_period_ends_at": "2026-04-14T23:59:59Z",

  "storage": {
    "used": 1500000000,
    "quota": 2000000000,
    "percent": 75.0,
    "over_quota": false
  },

  "traffic": {
    "used": 8000000000,
    "quota": 10000000000,
    "percent": 80.0,
    "over_quota": false,
    "upload_used": 2000000000,
    "upload_quota": -1,
    "upload_over_quota": false,
    "download_used": 6000000000,
    "download_quota": -1,
    "download_over_quota": false,
    "reset_date": "2026-04-14"
  },

  "can_add_repo": true,
  "can_share_repo": true,
  "can_add_group": false,
  "can_generate_share_link": true,
  "can_generate_upload_link": true,
  "can_send_share_link_mail": false,
  "can_invite_guest": false,
  "can_publish_repo": false,
  "can_use_global_address_book": false,
  "can_connect_with_desktop_clients": true,
  "can_connect_with_android_clients": true,
  "can_connect_with_ios_clients": true,
  "can_export_files_via_mobile_client": true,

  "share_link_expire_days_max": 3,
  "upload_link_expire_days_max": 3,

  "upgrade_features": ["add_group", "invite_guest", "publish_repo",
                        "send_share_link_mail", "use_global_address_book"]
}
```

### Key Design Decisions

- **`plan`**: Opaque display string from Accounts. Shown to user in UI labels. **Never used for logic.** Could be "Starter", "Business", "Enterprise Custom ACME", anything.
- **`billing_cycle`**: Commercial cycle only. It tells how the plan is billed, not how monthly traffic enforcement is computed.
- **`current_period_started_at` / `current_period_ends_at`**: Current quota period boundaries for the org. `current_period_ends_at` is the canonical source for reset timing and rollover checks.
- **`is_org_owner`**: Derived from `role == "owner"`. Determines if the user has billing authority.
- **`can_upgrade`**: `true` when the user is the org owner AND there's a reason to upgrade. See below.
- **Subscription scope**: The contract is always org-level. A "personal" account is modeled as an org with a single active user, so the same org quota/subscription semantics apply in both personal and org-admin UI.
- **Billing path**: The subscription page is informational only. Plan changes, renewals, extra users, extra storage, and traffic overages are handled in the billing service via the single `/billing/` redirect path.
- **`storage`**: Simple quota state. Storage has no monthly period. Either usage is within limit or writes are blocked.
- **`traffic`**: Pre-digested traffic state for the current quota period. Frontend reads `over_quota`, `percent`, `reset_date` and upload/download sub-limits directly — no calculations needed.
- **`can_X` flags**: Already resolved by `role AND enforcement_profile`. Frontend treats as final booleans.
- **`upgrade_features`**: List of features blocked by plan (not by role). Backend computes this. Frontend uses it for upgrade CTAs without duplicating business logic.
- **`share_link_expire_days_max` / `upload_link_expire_days_max`**: From enforcement profile. Frontend enforces in UI datepicker. 0 = no limit.
- **Quota sentinel values**: For Accounts-managed org quotas, any quota field `<=0` means SesameFS should treat it as unlimited for enforcement. That does not imply free billing.

### `can_upgrade` Logic

`can_upgrade` is `true` when the owner has a reason to consider upgrading:

```go
func computeCanUpgrade(role string, quotaPolicy string, storagePercent float64, trafficPercent float64, storageOverQuota bool, trafficOverQuota bool) bool {
    if role != "owner" {
        return false  // only owner can initiate upgrade
    }

    // Free plan owner: always show upgrade (upgrade to paid)
    if isHardEnforcement(quotaPolicy) {
        return true
    }

    // Paid plan owner: show upgrade when approaching or exceeding limits
    if storageOverQuota || trafficOverQuota {
        return true
    }
    if storagePercent >= 80.0 || trafficPercent >= 80.0 {
        return true
    }

    return false
}
```

| Scenario | is_org_owner | can_upgrade | What frontend shows |
|----------|:---:|:---:|---|
| Free owner, any usage | true | true | "Upgrade" button always visible |
| Paid owner, usage < 80% | true | false | Plan info, no upgrade CTA |
| Paid owner, storage at 85% | true | true | "Storage running low. Upgrade?" |
| Paid owner, traffic exceeded | true | true | "Traffic limit reached. Upgrade?" |
| Admin, any situation | false | false | Info only. "Contact your admin" if over quota |
| User, any situation | false | false | Info only. "Contact your admin" if over quota |

### Frontend Usage Patterns

**Zero business logic in the frontend.** All decisions are pre-computed by backend.

```javascript
// Quota warnings
if (info.storage.over_quota) {
    showBanner("Storage full.");
} else if (info.storage.percent >= 80) {
    showBanner(`Storage ${info.storage.percent}% used.`);
}

if (info.traffic.over_quota) {
    showBanner(`Traffic limit reached. Resets ${info.traffic.reset_date}.`);
} else if (info.traffic.percent >= 80) {
    showBanner(`Traffic ${info.traffic.percent}% used.`);
}

if (info.traffic.upload_over_quota) {
  showBanner("Upload traffic limit reached.");
}

if (info.traffic.download_over_quota) {
  showBanner("Download traffic limit reached.");
}

// Upgrade CTA (only shown to owner when relevant)
if (info.can_upgrade) {
    showUpgradeButton();
}

// Non-owner over quota: different message
if (!info.is_org_owner && (info.storage.over_quota || info.traffic.over_quota)) {
    showMessage("Contact your organization admin.");
}

// Feature blocked by plan: show locked badge with upgrade hint
if (info.upgrade_features.includes("add_group")) {
    showLockedButton("Create Group", "Available with upgrade");
}
```

### What each user sees

| State | Owner | Admin / User |
|-------|-------|------------|
| All OK | Plan info + usage stats | Plan info + usage stats |
| Usage > 80% | Warning + "Upgrade" button | Warning + "Contact admin" |
| Over quota (hard) | Blocked + "Upgrade NOW" | Blocked + "Contact admin" |
| Over quota (soft) | Warning + "Upgrade to increase limits" | Warning only |
| Feature blocked by plan | Locked + "Upgrade" | Locked + "Contact admin" |

---

## Default Plan on Org Creation

Every org MUST be born with an explicit plan and quota_policy. No more ambiguous `plan=""`.

### Defaults applied at all 3 creation paths

| Field | Default Value |
|-------|--------------|
| `plan` | `"free"` |
| `quota_policy` | `"hard"` |
| `billing_cycle` | `"monthly"` |
| `current_period_started_at` | org creation time |
| `current_period_ends_at` | from Accounts if provided, else SesameFS derives it from start using the shared monthly quota-period helper |
| `storage_quota` | 2 GB |
| `traffic_quota` | 10 GB |
| `traffic_upload_quota` | -1 |
| `traffic_download_quota` | -1 |
| `max_users` | 1 |

### Creation paths to update

1. **`internal/db/seed.go` — `createDefaultOrganization()`**
   - Default org should get `business` profile (it's the platform's main org)
   - Set `quota_policy="soft"`, all quota fields to -1 (unlimited)

2. **`internal/api/v2/admin.go` — `CreateOrganization()`**
   - Apply free defaults (above) unless caller specifies otherwise
   - Creator user gets `role=owner` (currently `role=admin`)

3. **`internal/auth/oidc.go` — `provisionUser()` (auto-provision org)**
   - Apply free defaults
   - First user in auto-provisioned org gets `role=owner`

### Exception: Platform Org

The platform org (`00000000-...`) gets `quota_policy="soft"` with unlimited everything. It is reserved for platform identities and administration, while remaining a functional org partition for internal libraries and file data.

---

## Provisioning from Accounts

### Flow

```
Owner clicks "Upgrade" in SesameFS
        |
        v
Redirect to Accounts (with org_id or external reference)
        |
        v
User completes payment in Accounts
        |
        v
Accounts calls PUT /api/v2.1/admin/organizations/:org_id/
with a dedicated platform service account API key (admin scope)
with new plan + quota_policy + quota values + current period values
        |
        v
SesameFS updates org: plan (display), quota_policy, current period fields and quota fields
        |
        v
Next request from any org member -> account/info returns
new capabilities, new limits, upgrade_features is now empty
```

### Accounts Always Wins

- Superadmin can manually edit org quotas via admin panel (useful for debugging, emergencies)
- Next sync from Accounts **overwrites** any manual changes
- This is by design — Accounts is the source of truth for billing
- No persistent local overrides. If a custom deal is needed, it's configured in Accounts
- Accounts may send `current_period_started_at` and optionally `current_period_ends_at`
- If Accounts sends `current_period_ends_at`, SesameFS uses it as authoritative
- If Accounts sends only `current_period_started_at`, SesameFS derives `current_period_ends_at` with the shared quota-period helper used by org creation and rollover (always advance one calendar month, clamping the day when the target month is shorter)
- If Accounts sends both start and end, SesameFS should validate that they are coherent before persisting them
- If Accounts sets any quota field to `<=0`, SesameFS treats that dimension as unlimited while Accounts remains free to bill overages commercially

### Internal Handler Separation

The same HTTP route (`PUT /admin/organizations/:id/`) serves both manual admin edits and M2M provisioning from Accounts. Internally, the handler separates the two paths:

```go
// Same route, different auth and audit paths
if isServiceAuth {
    // M2M path from Accounts:
    // - Enforce idempotency via version/updated_at
    // - Audit with source="accounts", actor=service_name
    // - Reject stale updates (incoming version <= stored version)
} else {
    // Manual superadmin path:
    // - Audit with source="manual", actor=admin_email
    // - No idempotency enforcement (human edits are ephemeral)
}
// Shared: validation, DB update, response
```

If the M2M flow needs a dedicated endpoint later, the handler internals are already separated.

### Accounts Provisioning Auth (current plan)

Accounts will authenticate with a dedicated technical user in the platform org, not with a human session.

Runbook:
- See [ACCOUNTS-PROVISIONING-RUNBOOK.md](ACCOUNTS-PROVISIONING-RUNBOOK.md) for the exact bootstrap, rotation, smoke-test, and revocation procedure.

Operational contract:
- create a non-human platform user such as `accounts-provisioner`
- keep that user in the platform org with role `superadmin`
- generate an admin-scope API key for that user
- Accounts sends the raw key through the normal API auth header: `Authorization: Token <raw_api_key>`
- Accounts calls the existing admin/org-admin endpoints directly; it does not use `/api2/auth-token/` or browser-style session flows
- use one service account key per environment and rotate it explicitly
- label the key clearly and treat it as a machine credential, not as a shared human token

Why this is acceptable now:
- platform admin routes already require both platform-superadmin privilege and admin API key scope
- the auth path is already implemented and tested through the normal API key middleware
- this avoids adding a second auth system before launch when the existing API key model already satisfies the privilege boundary

### Future Hardening Option

A dedicated `service_tokens` model remains optional future work if we later need stronger separation between human API keys and machine identities.

Possible future design:
- dedicated `service_tokens` table with hashed tokens, name, scope, expiry
- separate middleware (`serviceAuthMiddleware`) for non-user machine identities
- granular scopes such as `provisioning.organizations.write`, `provisioning.users.write`, `provisioning.subscriptions.write`

This is now a hardening/cleanliness improvement, not a launch blocker.

---

## User Creation: Accounts Integration (PENDING)

### Current State

`AddOrgUser` in `org_admin.go` creates users **locally** in SesameFS only. No identity in Accounts, no password, no way to log in via OIDC. This is conceptually broken when Accounts manages identity.

### Target State

```
Owner wants to add a user
        |
        v
Option A: "Invite" flow
  Owner gets invite link/email with org context
  User signs up in Accounts
  User logs in via OIDC -> auto-provisioned in SesameFS -> joins org
        |
Option B: Accounts provisions via API
  Owner creates user in Accounts UI
  Accounts calls SesameFS provisioning API to create local user
  User already has Accounts identity, can log in immediately
```

### What works today

- OIDC auto-provision (`oidc.go:780-807`) creates users on first login
- Users created via OIDC already have identity in the provider
- `max_users` enforcement already exists both in `AddOrgUser` and in OIDC auto-provision

### What needs to happen

1. Keep the existing `max_users` check in OIDC `provisionUser()` aligned with any future invite/provisioning flow
2. Design invite flow (SesameFS generates invite, user completes signup in Accounts)
3. Repurpose `AddOrgUser` endpoint as internal provisioning API (Accounts calls it, not UI)
4. **This is documented as a pending integration item, not blocking plan/permissions work**

---

## Commercial Lifecycle States (FUTURE)

`quota_policy` covers enforcement behavior (block vs warn). `organizations.status` covers lifecycle (active/deactivated/deleted). If Accounts needs to model additional commercial states, the current approach is:

| Commercial event | How SesameFS handles it today |
|---|---|
| Non-payment / suspension | Accounts sets `status=deactivated` via API. All access blocked. Reactivate on payment. |
| Downgrade (paid → free) | Accounts sets `quota_policy=hard` + free quota values. If traffic is over limit, traffic-blocked operations stay blocked until next period reset. If storage is over limit, all writes are blocked. If max_users is exceeded, SesameFS may deactivate regular users but keeps owner and admins active for coordination and warnings. Org-admin APIs must not reactivate users if that would exceed the effective limit, but superadmin is not subject to this operational restriction. Existing data is preserved and downloads remain allowed. |
| Grace period | Accounts manages timing and gives one month of grace after payment expiry before automatic downgrade/deactivation. |

After the org pays again and Accounts upgrades it back to a paid profile, user reactivation can be manual by the owner or admins. A future improvement may restore the same users who were active before downgrade if SesameFS stores a pre-downgrade activation snapshot.

Traffic enforcement uses the org's current quota period, not the natural UTC month. Every org has `current_period_started_at` and `current_period_ends_at`, and traffic usage is evaluated within that window even for annual billing plans. The rollover job should select orgs by `current_period_ends_at <= now`, advance them in a loop until the end is in the future, and never delete historical counters.

Even when `billing_cycle="annual"`, traffic still resets monthly. Accounts may update the period boundaries and remains the source of truth for paid orgs, but the local rollover cron still advances expired monthly periods so enforcement never stalls if Accounts is delayed.

When Accounts does not send the end explicitly, SesameFS derives monthly period ends with the same shared quota-period helper used during org creation defaults and rollover: advance one calendar month and clamp the day when the target month is shorter. `current_period_ends_at` is a quota-period boundary, not a commercial billing anchor.

Quota contract units use decimal storage units: `GB`/`TB` in plan defaults, API payloads, and quota UI mean base-1000 bytes. Binary units are reserved for technical/internal thresholds and should be labeled `GiB`/`MiB` if they ever surface in user-facing text.

Storage does not use rolling periods. Storage is simple: either current usage is within the limit or additional writes are blocked.

If finer-grained commercial states are needed (e.g., "past_due" where reads work but writes don't), a `billing_status` field can be added to organizations. This is explicitly deferred until Accounts requires it.

## Preview / Evaluate Endpoint

Accounts should have an optional preview capability before applying a change.

Purpose:
- evaluate whether a proposed downgrade or plan change would leave the org over storage, traffic or max_users
- decide whether the owner must clean up before downgrade
- explain the operational impact before applying the change

Suggested contract:
- input: `org_id`, proposed `quota_policy`, proposed quota values, optional proposed `current_period_started_at`, optional proposed `current_period_ends_at`
- output:
  - `would_exceed_storage`
  - `would_exceed_traffic`
  - `would_exceed_upload_traffic`
  - `would_exceed_download_traffic`
  - `would_exceed_max_users`
  - `users_to_deactivate_count`
  - `writes_would_be_blocked`
  - `traffic_would_reset_on`
  - optional human-readable warnings for UI/logging

This endpoint is not required for core enforcement, but it is useful for downgrade UX and Accounts-side decision making, so it is worth including early.

---

## Implementation Plan

### Phase 1: Backend Model (no frontend changes) — DONE (2026-03-27)

| Step | What | Files | Status |
|------|------|-------|--------|
| 1.1 | Add `"owner"` to role hierarchy + `IsOrgStaff()` helper | `permissions.go`, `server.go`, `admin.go`, `admin_extra.go`, `org_admin.go`, `oidc.go` | ✅ |
| 1.2 | Add `quota_policy` column + migration | `db.go`, `models.go` | ✅ |
| 1.3 | Add `current_period_started_at` + `current_period_ends_at` to organizations | `db.go`, `models.go` | ✅ |
| 1.4 | Add enforcement profiles config struct + loader (`EnforcementProfile`, `GetEnforcementProfile()`, defaults) | `config.go` | ✅ |
| 1.5 | Replace `isFree(plan)` with `isHardEnforcement(quotaPolicy)` — queries read `quota_policy` | `checker.go` | ✅ |
| 1.6 | Set defaults on org creation (seed, admin, OIDC), including plan, quota_policy, period fields | `seed.go`, `admin.go`, `oidc.go` | ✅ |
| 1.7 | Set `role=owner` for org creator (admin create + OIDC first user in new org) | `admin.go`, `oidc.go` | ✅ |
| 1.8 | Add ownership transfer endpoint `PUT /org/:org_id/admin/transfer-ownership/` | `org_admin.go` | ✅ |
| 1.9 | Add `max_users` check to OIDC auto-provision (skip for new org) | `oidc.go` | ✅ |

Implementation notes:
- `IsOrgStaff(role)` centralizes the `role == "admin" || role == "superadmin"` pattern — now includes owner automatically via hierarchy check.
- All 5 inline `isOrgStaff` checks across `server.go`, `admin.go`, `admin_extra.go`, `org_admin.go` replaced with `middleware.IsOrgStaff()`.
- `GetOrganization` and `GetOrgInfo` queries updated to SELECT and return `quota_policy`, `current_period_started_at`, `current_period_ends_at`.
- `UpdateOrganization` accepts `quota_policy` (validated: must be "hard" or "soft"), `current_period_started_at`, `current_period_ends_at`.
- Seed orgs (platform + default) get `quota_policy="soft"` + unlimited quotas. Admin-created and OIDC-provisioned orgs get `quota_policy="hard"` + free defaults (2GB storage, 10GB traffic, 1 user).
- OIDC `mapOIDCRole()` extended with `"owner"`, `"org_owner"`, `"tenant_admin"` mappings.

### Phase 2: Capability Resolution — DONE (2026-03-28)

| Step | What | Files | Status |
|------|------|-------|--------|
| 2.1 | Create enforcement profile resolver | New: `internal/plans/resolver.go` | ✅ |
| 2.2 | Create role permission map (hardcoded) | New: `internal/plans/roles.go` | ✅ |
| 2.3 | Create `ResolveCapabilities(role, quotaPolicy)` with `upgrade_features` | `internal/plans/resolver.go` | ✅ |

### Pending Compatibility Cleanup

- Wire/frontend naming still uses legacy fields such as `is_staff` and `is_org_staff`.
- Canonical semantics are now: platform superadmin vs org staff.
- Pending follow-up: introduce clearer API/frontend names such as `isSuperAdmin` or `isPlatformSuperAdmin`, keep legacy aliases during migration, then remove ambiguity once clients are updated.
| 2.4 | Add `traffic_period_usage` aggregate table for enforcement performance | `internal/db/db.go`, `internal/traffic/recorder.go` | ✅ |
| 2.5 | Update `account/info` to return resolved capabilities + quota state + current period fields | `internal/api/server.go` | ✅ |
| 2.6 | Update `subscription` endpoint with quota state objects and current period fields | `internal/api/server.go` | ✅ |
| 2.7 | Add enforcement-profile-based checks to share link creation | `internal/api/v2/share_links.go`, `internal/api/v2/upload_links.go` | ✅ |
| 2.8 | Add enforcement-profile-based checks to library creation | `internal/api/v2/libraries.go`, `internal/api/v2/groups.go` | ✅ |
| 2.9 | Add enforcement-profile-based checks to group creation | `internal/api/v2/groups.go` | ✅ |

Phase 2 implementation notes:
- `traffic_period_usage` is now the canonical source for quota enforcement and for the Phase 2 `traffic{}` objects returned by `account/info` and `subscription`.
- `traffic_monthly` remains in place for natural-month reporting and dashboards.
- `upgrade_features` now follows the documented frontend contract and returns short names such as `add_group` instead of raw `can_*` keys.
- `traffic.reset_date` now reflects `current_period_ends_at` when present and otherwise derives the boundary from `current_period_started_at` with the shared monthly quota-period helper.

### Phase 3: Frontend Migration

| Step | What | Files | Effort |
|------|------|-------|--------|
| 3.1 | Consume new `account/info` fields: `storage{}`, `traffic{}` with upload/download details, `current_period_*`, `upgrade_features`, `can_upgrade`, `is_org_owner` | `frontend/src/app.js` | Low |
| 3.2 | Replace `isFreeUser` with `upgrade_features`-based checks | `frontend/src/utils/constants.js` | Low |
| 3.3 | Rewrite `footer-upgrade.js` to use `can_upgrade` + `is_org_owner` | `frontend/src/services/footer-upgrade.js` | Medium |
| 3.4 | Rewrite `ad.js` to use `upgrade_features` | `frontend/src/services/ad.js` | Low |
| 3.5 | Add upgrade CTA badges on features in `upgrade_features` list | Various components | Medium |
| 3.6 | Add share link expiry enforcement from `share_link_expire_days_max` | Share link creation UI | Low |
| 3.7 | Add quota warning banners from `storage.over_quota` / `traffic.over_quota` | Layout/header components | Medium |
| 3.8 | Remove all `personalfree`/`business`/`pay_restricted*` references | Multiple files | Medium |

### Phase 3 Progress Update (2026-04-02, verified against code)

**Already landed:**
- ✅ `frontend/src/app.js` consumes new account/subscription contract fields (plan, can_upgrade, maxUsers, quota objects, current period data).
- ✅ Sysadmin org surfaces present org semantics as `owner` and `plan`, not `creator` and fake `role`.
- ✅ Ownership transfer exposed from sysadmin and org-admin users pages.
- ✅ Critical member-limit UI reads live org data (sysadmin + org-admin users pages).
- ✅ Legacy plan-role code (`personalfree`, `business`, `pay_restricted*`) **removed from frontend**. Only `isFreeUser` remains as `@deprecated` alias in `constants.js:232`.

**Remaining checklist to close Phase 3:**

| # | Item | Files | Effort | Status |
|---|------|-------|--------|--------|
| 3.1 | Axios interceptor for `X-Quota-Warning` header → toast/banner | `seafile-api.js` or Axios defaults | 1 session | ✅ |
| 3.2 | Permanent quota banners for `storage.over_quota` / `traffic.over_quota` | Layout/header components | 1 session | ❌ |
| 3.3 | Standardize quota unit labels: decimal GB/TB for quota contract, binary GiB for technical thresholds | `frontend/src/utils/utils.js`, quota inputs | 1 session | ✅ |
| 3.4 | Remove `window.org.pageOptions` placeholder deps in org-admin | `org-group-info/members/repos.js`, `org-user-profile/repos/shared-repos.js` | 1 session | ❌ |
| 3.5 | Org-owner subscription entry | Account menu link to org-admin subscription view consuming `GET /api/v2.1/subscription/` | 1 session | ✅ |
| 3.6 | Remove `isFreeUser` deprecated alias | `constants.js:232` + consumers | 30 min | ❌ |

**Conclusion:**
- **Phase 3 is mostly done.** Legacy code is clean. 5 remaining items are frontend-only, independent, ~1 session each.
- None block backend functionality or production deployment.
- Items 3.1-3.2 (quota warnings) are the most user-visible.

### Phase 4: Provisioning Integration (when Accounts is ready)

| Step | What | Effort |
|------|------|--------|
| 4.1 | Add preview/evaluate endpoint for proposed plan/quota changes | Medium |
| 4.2 | ✅ Periodic quota-period rollover job for orgs with `current_period_ends_at <= now` | Done |
| 4.3 | Provision dedicated platform service account + admin API key for Accounts | Low |
| 4.4 | Add/finish M2M code path in admin org update handler (idempotency, audit, source tagging) | Medium |
| 4.5 | Design invite flow for user creation | Medium |
| 4.6 | ✅ `max_users` enforcement in OIDC provision | Done |

---

## Seafile Legacy Mapping Reference

For developers migrating frontend code, here's the complete translation:

| Seafile Role | What It Actually Meant | SesameFS Equivalent |
|-------------|----------------------|-------------------|
| `default` | Basic user permissions | `role=user` + capabilities from enforcement profile |
| `guest` | External collaborator | `role=guest` (unchanged) |
| `restricted` | Free user, quota exceeded | `quota_policy=hard` + `storage.over_quota` or `traffic.over_quota`. No role change. |
| `personalfree` | Free plan user | `quota_policy=hard`. Plan name is display-only. |
| `personalpro` | Pro plan individual | `quota_policy=soft`. Plan name is display-only. |
| `pro` | Pro plan | Same as personalpro |
| `business` | Business plan | `quota_policy=soft`. Plan name is display-only. |
| `pay_restricted` | Paid user, traffic exceeded | `quota_policy=soft` + `traffic.over_quota`. Warn, don't block. |
| `pay_restricted_owner` | Restricted owner | `role=owner`. Admin panel never blocked by quotas. `can_upgrade=true`. |

**Key insight**: In SesameFS, quota exhaustion NEVER changes the user's role or feature access. It only blocks specific data operations based on `quota_policy`. The user's capabilities remain intact — they just can't perform data operations until quota resets or they upgrade.

**Frontend migration rule**: If old code compares `userRole` against a plan-related string, replace it:
- Don't use `plan` for comparisons (plan is opaque display string)
- Use `can_upgrade`, `upgrade_features`, `storage.over_quota`, `traffic.over_quota` instead
- These are pre-computed booleans/lists — no business logic needed in JS

---

## Related Documentation

- [ROLES-AND-PERMISSIONS.md](ROLES-AND-PERMISSIONS.md) — Role hierarchy, OIDC provisioning, permission middleware
- [QUOTAS-AND-TRAFFIC-PLAN.md](QUOTAS-AND-TRAFFIC-PLAN.md) — Quota enforcement, traffic tracking, counter tables
- [ARCHITECTURE.md](ARCHITECTURE.md) — Multi-tenancy model, org_id partitioning
- [OIDC.md](OIDC.md) — OIDC provider config, login flow
- [ADMIN-FEATURES.md](ADMIN-FEATURES.md) — Admin API endpoints
- `quotas-pending-issues.txt` — Tracking document for quota/plan work
