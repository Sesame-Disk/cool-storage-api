# SesameFS v1.0 — Production Readiness Roadmap

**Last updated:** 2026-03-31
**Current status:** ~82% production-ready (per `IMPLEMENTATION_STATUS.md`)

This document identifies every blocker, high-priority item, and fast-follow task required before
the v1.0 production launch. Items are classified by priority and include current state analysis,
concrete implications, and pointers to the relevant code.

---

## Priority Classification

| Label | Meaning |
|-------|---------|
| **P0** | Hard blocker. Cannot ship without this. |
| **P1** | Required for v1.0. Can be staged across releases but must land before or immediately at launch. |
| **P2** | Fast-follow. Ship within the first week/month post-launch. |

---

## P0 — Hard Blockers

### 1. Accounts ↔ SesameFS Integration (Phase 2) — ✅ Phase 2 DONE (2026-03-28), remaining items below

**Current state:** Phase 1 AND Phase 2 are implemented. `quota_policy` column, `period` columns,
`EnforcementProfile` config struct, `isHardEnforcement()`, ownership transfer endpoint, `max_users`
check on OIDC login, `ResolveCapabilities()` wired to `account/info` and `bootstrap`, enforcement
checks on share links, upload links, library creation, and group creation.

**What was implemented (Phase 2, 2026-03-28):**

- ✅ **`GET /api/v2.1/account/info/`** — calls `plans.ResolveCapabilities()` (server.go:1438),
  returns resolved capabilities, `upgrade_features`, `can_upgrade`, `storage{}`, `traffic{}`.
- ✅ **`GET /api/v2.1/subscription/`** — returns real quota data from org + traffic counters.
- ✅ **Enforcement on group creation** — `groups.go:337` gates `CanAddGroup` feature flag.
- ✅ **Enforcement on library creation** — `libraries.go:447` gates `MaxLibraries` limit.
- ✅ **Enforcement on share/upload links** — `share_links.go:515` and `upload_links.go:281` gate limits.
- ✅ **`traffic_period_usage`** table for period-based enforcement.

**What is still missing:**

- **Accounts provisioning operationalization** — the authenticated channel can now be a dedicated
  platform service account API key with admin scope, but the environment-level setup, rotation,
  idempotency path, and audit/source tagging still need to be formalized.
- **Accounts → SesameFS sync** — no webhook receiver or polling loop to apply plan changes pushed
  from Accounts.

**What was added on 2026-04-08:**

- ✅ `POST /api/v2.1/admin/organizations/:org_id/preview-plan-change/` for downgrade and quota-impact simulation before apply.
- ✅ `accounts.disable_org_user_writes=true` as the default local safeguard so tenant org-admin membership writes are disabled unless explicitly re-enabled.
- ✅ Org-admin info now exposes authority flags so the SesameFS frontend can hide disabled membership actions.

**Implications of shipping without remaining items:**
- Plan upgrades still need the formal Accounts provisioning path and runbook to avoid ad-hoc manual operations.

Operational reference:
- [ACCOUNTS-PROVISIONING-RUNBOOK.md](ACCOUNTS-PROVISIONING-RUNBOOK.md) — bootstrap and rotation procedure for the dedicated platform service account API key.

**Key files:**
- `internal/plans/resolver.go` — `ResolveCapabilities()` ready to use, do not duplicate
- `internal/api/v2/enforcement.go` — `GetOrgEnforcement()`, quota counters
- `internal/api/v2/share_links.go:490-519` — reference implementation of enforcement pattern
- `internal/traffic/recorder.go`, `internal/traffic/checker.go`
- `internal/api/v2/groups.go` — needs max_groups enforcement
- `internal/api/v2/libraries.go` — needs publish enforcement

---

### 2. Go Code Reorganization — Reclassified to P2 (2026-04-02)

**Current state:** `internal/api/v2/` contains 64 Go files (~45,000 lines) with no separation
between the HTTP handler layer, business logic, and data access.

**Concrete problems:**

| File | Lines | Issue |
|------|-------|-------|
| `internal/api/v2/files.go` | 4,414 | HTTP handlers + file tree logic + Cassandra queries mixed |
| `internal/api/v2/admin.go` | 3,972 | Same — 30+ admin endpoints with inline DB queries |
| `internal/api/v2/org_admin.go` | 3,710 | Same |
| `internal/api/v2/admin_extra.go` | 3,032 | Same |
| `internal/api/server.go` | 2,644 | Route registration + auth handlers + frontend file serving |
| `internal/api/sync.go` | 2,147 | Sync protocol, partially acceptable |

There is **no service layer** (`internal/services/` does not exist). Handlers call `db.*` and
`storage.*` directly. There is **no repository layer**; Cassandra queries are scattered across
handler files. `internal/models/` has only ~550 lines and only defines basic structs.

**Reclassified from P0 to P2 (2026-04-02):**
The code is functional and all features work correctly. The large files create operational risk
for incident response (harder to locate and fix bugs quickly), but this is a code quality issue,
not a functional blocker. The real remaining launch-critical gaps are Accounts M2M provisioning,
GC multi-instance safety, and the general security-hardening checklist.
Reorganization should happen post-launch when stability allows for large refactors.

**Proposed structure (move code, do not rewrite):**

```
internal/
  api/
    v2/
      handlers/          — HTTP only: parse request, call service, serialize response
        files.go
        admin.go
        org_admin.go
        libraries.go
        groups.go
        ...
      services/          — Pure business logic, no HTTP types
        file_service.go
        library_service.go
        admin_service.go
        group_service.go
        ...
      repositories/      — Cassandra queries only, no business logic
        user_repo.go
        library_repo.go
        group_repo.go
        ...
  models/                — Domain structs (currently ~550 lines, needs expansion)
```

**Strategy:** Extract, do not rewrite. Start with the four largest files. Each handler method
calls a service method; each service method calls a repository method. No handler touches `db`
directly. The existing logic moves, it does not change.

**Suggested order:**
1. `files.go` → `handlers/files.go` + `services/file_service.go` + `repositories/file_repo.go`
2. `admin.go` + `admin_extra.go` → same pattern
3. `org_admin.go` → same pattern
4. `server.go` → extract route registration to `router.go`, frontend serving to `frontend.go`

---

### ~~3. Frontend / Backend Separation + Nginx Architecture~~ — ✅ DONE (2026-03-30/31)

**Implemented:**
- Go is now a pure API server. All SPA HTML/static serving removed from `internal/api/server.go`.
  - **Exception (intentional):** Go continues to serve standalone functional HTML for `/d/` (share links),
    `/u/d/` (upload links), `/lib/` (file viewer), `/onlyoffice/`, and SSO callback/logout.
    These pages require server-side data injection and are security boundaries — not SPA pages.
- `frontend/Dockerfile` — multi-stage build: `node:22-bookworm` builder + `nginx:stable-alpine` runtime.
- `frontend/nginx.conf` — SPA routing (`/sys/`→sysadmin, `/org/`→orgadmin, `/`→index) + proxy_pass for all backend routes.
- `nginx/nginx.conf.template` — separate upstreams: `sesamefs_api:8080`, `sesamefs_frontend:80`, `sesamefs_mobile:80`.
- `internal/api/bootstrap.go` — `GET /api/v2.1/bootstrap/` (public) replaces server-side placeholder injection for SPA config.
- `frontend/src/utils/constants.js` — live bindings (`export let`) for org-admin context; `_updateOrgContext()` called after bootstrap fetch so all importers see the correct `orgID` etc.
- Mobile UA routing: single authoritative location in `nginx.conf.template` (`$is_mobile` map); Go's `isMobileUA()` removed.
- `docker-compose.yaml` + `docker-compose.prod.yml` — `frontend` service added with `context: ./frontend`.

**Production nginx bugs fixed (2026-03-31):**

| Bug | File | Fix |
|-----|------|-----|
| No `client_max_body_size` → nginx default 1MB blocked large API calls | `frontend/nginx.conf` | Added `client_max_body_size 100G` at server block level |
| No proxy timeouts → 60s default causes 504 on large uploads/downloads | `frontend/nginx.conf` | Added `proxy_read_timeout 3600s`, `proxy_send_timeout 3600s`, `proxy_connect_timeout 30s` at server block level |
| No `proxy_buffering off` on transfer routes → memory spikes on large files | `frontend/nginx.conf` | Added `proxy_buffering off; proxy_request_buffering off` to `/d/`, `/u/d/`, `/lib/`, `/repo/`, `/seafhttp/` |
| HTTP/1.0 to backend (no keepalive) | `frontend/nginx.conf` | Added `proxy_http_version 1.1` + `proxy_set_header Connection ""` to all proxy locations |
| No `sendfile`/`tcp_nopush`/`tcp_nodelay` | `frontend/nginx.conf` | Added at server block level |
| No `gzip_vary` → CDN/proxy cache corruption | `frontend/nginx.conf` | Added `gzip_vary on; gzip_comp_level 6` |
| No keepalive on upstreams → new TCP connection per request | `nginx/nginx.conf.template` | Added `keepalive 32/16/8` to all 3 upstream blocks |
| Missing `proxy_send_timeout` on frontend location | `nginx/nginx.conf.template` | Added `proxy_send_timeout 3600s` |
| `proxy_connect_timeout 10s` too low | `nginx/nginx.conf.template` | Changed to 30s |
| Rate limiting file transfers same zone as API calls | `nginx/nginx.conf.template` | New `transfer` zone (20r/s, burst=40) for `/seafhttp/`, `/d/`, `/u/d/`; API zone (100r/s, burst=200) for all other routes |
| No Content-Security-Policy header | `nginx/nginx.conf.template` | Added CSP header with `unsafe-inline` for share link page injection |
| Security headers missing `always` flag | `nginx/nginx.conf.template` | Added `always` to all `add_header` directives |
| `client_max_body_size 20G` | `nginx/nginx.conf.template` | Increased to `100G` |

**Bundle coupling fix (2026-03-31):**
- `internal/api/v2/sharelink_view.go` — `NewShareLinkViewHandler` now fetches `asset-manifest.json`
  from the frontend container via HTTP at startup (`GET ${FRONTEND_URL}/asset-manifest.json`).
  3-level fallback: HTTP fetch → filesystem scan → hardcoded hashes.
  `FRONTEND_URL` env var added to both docker-compose files (default `http://frontend:80`).

**Logout localStorage fix (2026-03-31):**
- `internal/api/server.go` — `handleLogout` now invalidates the server-side session via
  `SessionManager.InvalidateSession(token)` before clearing the cookie and redirecting.
- `frontend/src/components/common/logout.js` + `account.js` — logout links now clear
  `sesamefs_auth_token` and all `custom_permissions_*` keys from localStorage on click,
  so the SPA does not attempt to reuse a stale token after redirect.

---

### 4. Robust Database Migration System ✅ DONE (2026-04-01)

**Implemented:** Versioned CQL migration system with checksum validation, legacy-database
bootstrap, and CLI tooling.

**What was built:**

```
internal/db/migrations/
  001_initial_schema.cql   ← full current schema (all 50 tables, all columns)
  NNN_description.cql      ← future migrations go here

internal/db/migrator.go    ← migration runner (go:embed, checksum validation)
internal/db/db.go          ← stripped of all inline consts; Migrate() delegates to runner
```

**`schema_migrations` tracking table (created automatically):**
```
version    INT PRIMARY KEY
name       TEXT
applied_at TIMESTAMP
checksum   TEXT   ← SHA-256 of the .cql file; mismatch = startup failure
```

**Key behaviours:**
- Migration files are embedded in the binary at compile time (`//go:embed migrations/*.cql`).
- Applied migrations are tracked with SHA-256 checksums. If a file is edited after application
  the server **refuses to boot** — prevents silent schema drift.
- **Legacy bootstrap**: if `schema_migrations` is empty but `organizations` already exists,
  all known migrations are stamped as applied without execution. Existing deployments upgrade
  safely on first deploy.
- A failed statement leaves the migration un-stamped; it will be retried on the next startup.

**CLI flags (`sesamefs migrate`):**
```
sesamefs migrate               # apply pending + seed (normal operation)
sesamefs migrate --status      # print applied/pending table with checksums
sesamefs migrate --dry-run     # list pending migrations without applying
sesamefs migrate --check       # exit non-zero if any migrations are pending (CI)
```

**Adding a new migration:**
1. Create `internal/db/migrations/NNN_description.cql` with the CQL statements.
2. Deploy the new binary — it applies the migration automatically on startup.
3. Never edit a migration file after it has been applied; create a new numbered file instead.

---

## P1 — Required for v1.0

### 5. Storage Classes and Multi-Region

**Current state:** The infrastructure exists — `StorageManager`, health checks, failover logic,
region config, `endpoint_regions` mapping, `region_classes` mapping. However several critical
pieces are incomplete.

**Gaps:**

| Gap | Location | Status |
|-----|----------|--------|
| `SelectBackend()` policy evaluation | `internal/storage/storage.go:391-398` | TODO stub |
| Storage class migration job | `internal/api/v2/libraries.go:998` | TODO stub |
| Cold storage / Glacier restore | `internal/api/v2/restore.go` | 3 TODOs, ~30% done |
| Region selection UI (create library) | Frontend | Not implemented |
| Cassandra `replication_factor` | `internal/db/db.go:279` | Set to 1 (dev default) |

**What is needed for v1.0:**

1. **`SelectBackend()` policy evaluation** — implement the failover policy in
   `storage.go:391-398`. Without this, the storage manager cannot make intelligent routing
   decisions when a backend is degraded.
2. **Region selection at library creation** — frontend dialog + `PATCH /api/v2.1/repos/:id/`
   endpoint to set `storage_class` on a library. Users need a way to choose where their data lives.
3. **Storage class migration job** — background GC worker that migrates blocks when a library's
   storage class is changed. The stub at `libraries.go:998` needs to enqueue a real job.
4. **Cassandra replication factor = 3** — production Cassandra must not run `replication_factor: 1`.
   This is a config change, not a code change, but it must be done before any data is written in
   production.
5. **Cold storage / Glacier** — can ship as v1.1 if needed. Document explicitly as a known
   limitation in the release notes.

**Key files:**
- `internal/storage/storage.go:391-398` — `SelectBackend()` TODO
- `internal/api/v2/restore.go` — Glacier restore (3 TODOs)
- `internal/api/v2/libraries.go:998` — storage class migration job TODO
- `configs/config.example.yaml:69-77` — region/class config already documented

---

### 6. Antivirus / Malware Scanning

**Current state:** Zero implementation. A search across the entire codebase finds no AV-related
code. The only `scan` reference is `scanBundles()` in `sharelink_view.go`, which scans for
frontend JS/CSS assets — not malware.

**Why P1, not P0:**
v1.0 can launch without AV if the limitation is explicitly documented and communicated to
customers. However, a file storage service cannot remain without AV indefinitely — this creates
legal, contractual, and reputational exposure.

**Recommended implementation — ClamAV (self-hosted, async):**

```
Upload completes
    │
    ▼
enqueue scan job (GC worker queue)
    │
    ▼
ClamAV container via TCP socket (clamd)
    │
    ├── CLEAN  → update scan_status = "clean"
    └── VIRUS  → update scan_status = "infected"
                 block downloads
                 notify org admin
                 (optionally: quarantine file)
```

**Why async (not inline):** Scanning inline during upload would add latency to every upload.
Large files can take seconds to scan. The async approach keeps upload performance unaffected
while ensuring all files are eventually scanned.

**Implementation checklist:**
- Add `scan_status text` column to `fs_objects` or `blocks` table: `pending | clean | infected | error`
- New package `internal/av/` with `Scanner` interface + `ClamAVScanner` implementation
  using `github.com/dutchcoders/go-clamd`
- New GC worker type `ScanWorker` in `internal/gc/`
- Post-upload hook in `internal/api/seafhttp.go` to enqueue scan jobs
- Download handler in `internal/api/seafhttp.go` to check `scan_status` before serving
- Add `clamav/clamav` service to `docker-compose.prod.yml`
- Admin UI: show scan status in file detail view; list infected files in admin panel

**Alternative — External API (VirusTotal, Metadefender):**
Simpler to integrate but introduces rate limits, per-scan costs, and — critically — customer
file contents are sent to a third-party service. Not recommended for a file storage product.

---

### 7. Enforcement Phase 2 — Capability Resolver Wire-Up — ✅ DONE (2026-03-28)

**Verified against code 2026-04-02:** Fully implemented and wired.

**What was implemented:**

- ✅ **`GET /api/v2.1/account/info/`** — calls `plans.ResolveCapabilities()` at `server.go:1438`.
  Returns resolved capability map, `upgrade_features` list, `can_upgrade`, `is_org_owner`,
  `storage{}` and `traffic{}` pre-digested objects, and all `can_*` feature flags.
- ✅ **`GET /api/v2.1/bootstrap/`** — also calls `ResolveCapabilities()` at `bootstrap.go:271`.
- ✅ **Group creation enforcement** — `groups.go:337` checks `!enforcement.Profile.Features.CanAddGroup`.
  Also checks `MaxLibraries` for group-owned library creation at `groups.go:903`.
- ✅ **Library creation enforcement** — `libraries.go:447` checks `MaxLibraries` via `CountActiveLibraries()`.
- ✅ **Share link enforcement** — `share_links.go:515` checks `MaxShareLinks` (single + batch at :927).
- ✅ **Upload link enforcement** — `upload_links.go:281` checks `MaxUploadLinks`.

**Minor remaining items (not blocking):**

- `can_invite_guest` — no invite endpoint exists yet (feature deferred, see User Creation section in PLANS-AND-PERMISSIONS.md)
- `can_publish_repo` — wiki/publish feature is a stub (nav hidden, endpoint returns `[]`)
- `CountActiveGroups()` — not needed because `can_add_group` is a boolean feature flag (not a numeric limit). Free plan has `can_add_group=false`, paid has `true`. No count needed.

---

### 8. Persistent Audit Logs

**Current state:** `internal/middleware/audit.go` defines an `AuditLogger` with well-structured
event types (library.create, file.upload, file.delete, permission.change, access.denied, etc.).
All events are currently written **only to stdout/structured log**. Line 87 has a
`// TODO: Store in audit_logs table when we create it` comment.

**Why this is required for production:**
Audit logs are a compliance requirement for enterprise customers. They are also the primary
debugging tool when a customer reports unauthorized access, unexpected data loss, or a
permissions issue. Without persistent audit logs, incident response is blind.

**Implementation:**

1. Add migration `NNN_audit_logs.cql`:

```cql
CREATE TABLE IF NOT EXISTS audit_logs (
    org_id      uuid,
    created_at  timestamp,
    event_id    uuid,
    actor_id    uuid,
    actor_email text,
    action      text,
    resource_type text,
    resource_id text,
    metadata    text,
    ip          text,
    user_agent  text,
    PRIMARY KEY ((org_id), created_at, event_id)
) WITH CLUSTERING ORDER BY (created_at DESC)
   AND default_time_to_live = 7776000;  -- 90 days default retention
```

2. Implement the TODO in `audit.go:87` — write to the table in addition to the log.
3. Add `GET /org/admin/audit-log/` for org admins to query their org's audit log
   (filterable by action, actor, date range).
4. Add `GET /admin/audit-log/` for superadmins (cross-org query).
5. Expose retention period as a configurable value in `config.yaml`.

---

### 9. Security Hardening

**Current state:** Nginx (`nginx/nginx.conf.template`) already sets HSTS, X-Frame-Options,
X-Content-Type-Options, and CSP. However, the Go server has no security headers of its own,
which means direct access to the Go port (bypassing Nginx) is unprotected. Additionally,
several HTTP server configuration gaps exist.

**Concrete items:**

**A. Security headers middleware (new file `internal/middleware/security.go`, ~50 lines):**
```go
// Sets: Strict-Transport-Security, X-Content-Type-Options: nosniff,
// X-Frame-Options: SAMEORIGIN, Referrer-Policy: strict-origin-when-cross-origin
// CSP: limited default policy
```
Register this middleware in `server.go` for all routes.

**B. HTTP server configuration gaps (`internal/api/server.go:2346-2361`):**
```go
s.server = &http.Server{
    Addr:              ...,
    ReadTimeout:       ...,    // already set
    WriteTimeout:      ...,    // already set
    // MISSING:
    IdleTimeout:       120 * time.Second,
    MaxHeaderBytes:    1 << 20,  // 1MB
}
```

**C. Brute force protection:**
The existing rate limiter in `internal/middleware/ratelimit.go` is per-IP (token bucket,
~10 req/6s on auth endpoints). This does not protect against distributed brute force or
credential stuffing. Add per-username lockout after N failed attempts (e.g., 10 failures
in 5 minutes → 15-minute lockout stored in a Cassandra counter or in-memory LRU).

**D. CORS production verification:**
`dev_mode: true` sets `AllowAllOrigins: true` in the CORS middleware. Verify that production
config always overrides this. Add a startup assertion that fails if `dev_mode = true` and
`SESAME_ENV = production`.

**E. API token revocation:**
User API keys and sessions derived from them are now revoked on deactivate/delete, and API-key-derived
sessions now preserve scope metadata with central sync/library enforcement. The remaining hardening debt is:
- dedicated rate limiting for direct API key auth in `authMiddleware`
- route-audit coverage for endpoints that rely only on bare authentication and not on central library/scope checks
- Go-side security headers and HTTP server limits still missing

---

### 10. Backup and Disaster Recovery

**Current state:** No backup scripts, no restore runbook, no defined RTO/RPO, no documented
disaster recovery procedure exists anywhere in the repository.

**This is a prerequisite for production.** Without a tested backup and restore procedure,
data loss in production (hardware failure, accidental deletion, ransomware, cloud provider
outage) has no recovery path.

**Required before launch:**

1. **Cassandra backup script** (`scripts/backup-cassandra.sh`):
   - `nodetool snapshot` to create a consistent snapshot
   - Upload snapshot files to S3 backup bucket
   - Verify snapshot integrity
   - Schedule via cron or Kubernetes CronJob

2. **S3/MinIO backup** (`scripts/backup-s3.sh`):
   - Cross-bucket replication for production S3 buckets
   - Or: `aws s3 sync source-bucket backup-bucket` scheduled job
   - Include lifecycle rules for backup retention (e.g., 30-day retention for daily backups)

3. **Configuration backup:**
   - `config.yaml` and secrets (env vars) must be stored in a secrets manager
     (AWS Secrets Manager, HashiCorp Vault, or equivalent)
   - Document the recovery procedure for config restoration

4. **Restore runbook** (`docs/DISASTER-RECOVERY.md`):
   - Step-by-step procedure for full cluster restore from backup
   - Step-by-step procedure for single-keyspace restore
   - Step-by-step procedure for S3 restore
   - Estimated restore time for each scenario

5. **RTO/RPO definition:**
   - Recovery Time Objective (RTO): maximum acceptable downtime
   - Recovery Point Objective (RPO): maximum acceptable data loss window
   - These values must be agreed with stakeholders and documented

6. **Restore test:** Run a full restore drill before the production launch. A backup that
   has never been tested is not a backup.

---

## P2 — Fast-Follow (first week/month post-launch)

### 11. Email / Notification System

**Current state:** The capability flag `can_send_share_link_mail` exists in
`internal/plans/roles.go:13` and is gated by the enforcement profile. However, no email
sending code exists anywhere in the codebase.

**What is needed:**
- Add `email` section to `internal/config/config.go`:
  `smtp_host`, `smtp_port`, `smtp_user`, `smtp_password`, `from_address`, `from_name`
- New package `internal/email/` with `Sender` interface + SMTP implementation
- Transactional email templates (HTML):
  - Organization invite
  - Share link by email (the flagged feature)
  - Password reset / account recovery
- Wire invite email into `AdminAddOrgUser()` and OIDC auto-provisioning flow
- Wire share link email into `CreateShareLink()` when `send_link_url` param is present

---

### 12. Cursor-Based Pagination

**Current state:**
- Admin and org-admin share/upload link endpoints already support cursor pagination using bucketed read models and opaque `next_cursor` tokens.
- Legacy `page/per_page` remains for backward compatibility on those endpoints.
- The main remaining pagination debt is **not** admin links anymore; it is admin library/group lists, which still load projection rows into memory and paginate after sorting in Go.

**Remaining work:**
- Decide whether admin library/group lists should adopt the same bucketed cursor pattern or a simpler capped streaming strategy.
- Keep offset pagination only where backward compatibility is required and result sets are known to stay small.

---

### 13. Cold Storage / Glacier

**Current state:** `internal/api/v2/restore.go` has three TODO stubs:
- Get file info and check if it's in cold storage
- Get block IDs for the file
- Initiate Glacier restore job

The S3 + Glacier configuration infrastructure already exists. This requires implementing
the AWS S3 `RestoreObject` API call and a polling loop to check restore status.

**Recommendation:** Ship v1.0 with cold storage documented as "Coming in v1.1". Do not block
the launch on Glacier since it requires AWS Glacier infrastructure setup and testing.

---

## Executive Summary

**Last verified against code**: 2026-04-04

| # | Area | Priority | Current State | Estimated Effort |
|---|------|----------|---------------|-----------------|
| 1 | Accounts ↔ SesameFS integration | **P0** | Phase 1 ✅, Phase 2 ✅ (2026-03-28). Remaining: operationalize dedicated platform service account API key + provisioning sync/idempotency/audit from Accounts | 1–2 weeks |
| 2 | Go code reorganization | **P2** | Not started. Functional but operationally risky for incident response | 3–4 weeks |
| 3 | Frontend/Backend separation + Nginx | ✅ DONE | ✅ DONE (2026-03-30/31) | — |
| 4 | Robust DB migration system | ✅ DONE | ✅ DONE (2026-04-01) | — |
| 5 | Storage classes & multi-region | **P1** | Infra ready, ~30% | 2 weeks |
| 6 | Antivirus / malware scanning | **P1** | 0% | 1–2 weeks |
| 7 | Enforcement Phase 2 wire-up | ✅ DONE | ✅ DONE (2026-03-28). `ResolveCapabilities` wired to `account/info` (server.go:1438) + `bootstrap` (bootstrap.go:271). Group creation gates `CanAddGroup`. Library creation gates `MaxLibraries`. Share/upload links gate `MaxShareLinks`/`MaxUploadLinks`. | — |
| 8 | Persistent audit logs | **P1** | Framework only, not persisted (TODO audit.go:87) | 3–4 days |
| 9 | Security hardening | **P1** | Partial. API key scope hardening landed (2026-04-04), but Go security headers, HTTP server limits, and direct API key rate limiting still remain | 2–3 days |
| 10 | Backup and disaster recovery | **P1** | Nothing exists | 1 week |
| 11 | Email / notifications | **P2** | 0% | 1 week |
| 12 | Cursor-based pagination | **P2** | Admin links done; library/group admin lists still pending | 2-4 days |
| 13 | Cold storage / Glacier | **P2** | ~30% | 2–3 weeks |
| **14** | **User-scoped programmatic auth (API keys)** | **✅ DONE** | **User API keys + `/api2/auth-token/` are live. 2026-04-04 hardening now propagates scope to derived sessions, caps effective role, and enforces central library/sync scope checks. Accounts can reuse the same API key system through a dedicated platform service account; the remaining work belongs to item #1.** | — |
| **15** | **GC multi-instance safety** | **P0** | **Temporary prod guard landed: `GC_ENABLED` can disable GC on non-GC replicas. Real leader election/lease is still pending because `gc.go:99` Start() has no distributed lock. For multi-region: GC MUST run in exactly one DC. LWT operations use `SERIAL` (global Paxos) — do NOT change to `LOCAL_SERIAL`. Two-phase LWT block deletion with sentinel (-999) + upload backoff hardened (2026-04-10).** | **1 day** |
| 16 | Frontend Phase 3 cleanup | **P1** | Mostly done. Legacy `personalfree/business/pay_restricted*` removed. Remaining: pageOptions placeholders and minor cleanup | 2–3 days |

---

## Recommended Execution Order

### Sprint 1 — Infrastructure Foundation ✅ COMPLETE
- [#3] ✅ Frontend/backend separation + Nginx — DONE (2026-03-30/31)
- [#4] ✅ DB migration system — DONE (2026-04-01)
- [#7] ✅ Enforcement Phase 2 wire-up — DONE (2026-03-28)

### Sprint 2 — Hard Blockers (CURRENT)
- [#15] GC multi-instance safety — temporary `GC_ENABLED` guard is available; Cassandra LWT leader lease still pending
- [#9] Security hardening — small effort, high impact
- [#1] Remaining Accounts integration — formalize service-account API key auth, provisioning endpoint, idempotency/audit

### Sprint 3 — Production Essentials
- [#8] Persistent audit logs — framework already exists, wire it up
- [#10] Backup and disaster recovery — scripts + runbook + restore drill
- [#17] Frontend Phase 3 — quota warning banners, unit standardization

### Sprint 4 — Robustness
- [#5] Storage classes and multi-region UI
- [#6] Antivirus / ClamAV
- [#2] Go code reorganization (operational risk reduction, not a feature)

### Post-launch
- [#11] Email notifications
- [#12] Cursor-based pagination
- [#13] Cold storage / Glacier (v1.1)

---

## Key Files Reference

| File | Relevance |
|------|-----------|
| `internal/plans/resolver.go` | `ResolveCapabilities()` — use this, do not duplicate |
| `internal/api/v2/enforcement.go` | Quota counters for enforcement checks |
| `internal/traffic/checker.go` | Storage and traffic quota checks |
| `internal/traffic/recorder.go` | Traffic recording (upload/download) |
| `internal/db/db.go` | DB connection bootstrap + `Migrate()` entry point |
| `internal/db/migrator.go` | Migration runner — `Run()`, `Status()`, `DryRun()`, `Check()` |
| `internal/db/migrations/` | CQL migration files (embedded in binary at compile time) |
| `internal/api/server.go` | Frontend serving logic to remove |
| `nginx/nginx.conf.template` | Base for updated Nginx architecture |
| `docker-compose.prod.yml` | Starting point for production deployment |
| `internal/middleware/audit.go` | Audit framework — wire to DB |
| `internal/middleware/ratelimit.go` | Rate limiting — extend for per-user brute force |
| `docs/PLANS-AND-PERMISSIONS.md` | Capabilities system design reference |
