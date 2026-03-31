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

### 1. Accounts ↔ SesameFS Integration (Phase 2)

**Current state:** Phase 1 is implemented — `quota_policy` column, `period` columns, `EnforcementProfile`
config struct, `isHardEnforcement()`, ownership transfer endpoint, `max_users` check on OIDC login.
Phase 2 is **not yet implemented**.

**What is missing:**

- **`GET /api/v2.1/account/info/`** — must return resolved capabilities to the frontend.
  `plans/resolver.go` already has `ResolveCapabilities()` fully implemented and tested, but it is
  not wired to any HTTP handler yet.
- **`GET /api/v2.1/subscription/`** — endpoint exists but returns stub data, not integrated with
  real Accounts data.
- **Rollover job** — `traffic_period_usage` is never reset. Without a scheduled job that fires at
  the start of each billing period, traffic quotas will never roll over and paying customers will
  be permanently blocked after their first month.
- **M2M auth (service tokens)** — there is no authenticated channel for the Accounts service to
  call SesameFS and update a plan, change a quota, or provision a new org. Any plan change currently
  requires a manual DB edit.
- **Accounts → SesameFS sync** — no webhook receiver or polling loop to apply plan changes pushed
  from Accounts. Manual DB edits are ephemeral; the next Accounts sync will overwrite them.
- **Enforcement on group and library creation** — share links ✓ and upload links ✓ already enforce
  quotas, but `groups.go` (max_groups) and `libraries.go` (publish feature flag) still need the
  same treatment.

**Implications of shipping without Phase 2:**
- Paid customers are subject to the same `hard` enforcement profile as free users.
- Traffic quotas are never reset → customers get permanently blocked after month 1.
- Plan upgrades cannot be applied without direct DB access.
- The `account/info` endpoint returns stale/incomplete data → frontend capabilities UI is broken.

**Key files:**
- `internal/plans/resolver.go` — `ResolveCapabilities()` ready to use, do not duplicate
- `internal/api/v2/enforcement.go` — `GetOrgEnforcement()`, quota counters
- `internal/api/v2/share_links.go:490-519` — reference implementation of enforcement pattern
- `internal/traffic/recorder.go`, `internal/traffic/checker.go`
- `internal/api/v2/groups.go` — needs max_groups enforcement
- `internal/api/v2/libraries.go` — needs publish enforcement

---

### 2. Go Code Reorganization

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

**Why this is P0, not P2:**
In production, when an incident occurs, you need to locate the bug, understand the impact, and
ship a fix fast. In a 4,000-line file with no layer separation, this is dangerous. This is an
operational risk, not just a code quality issue. Every on-call engineer who touches this codebase
carries this risk.

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

### 4. Robust Database Migration System

**Current state:** All migrations are inline string arrays in `internal/db/db.go`, executed at
every startup. There is no version registry, no checksum validation, no rollback capability,
and no way to know which migrations have been applied to a given instance. `ALTER TABLE`
statements that fail (e.g., column already exists) are silently ignored.

**Problems for production:**
- No audit trail of what schema version is running in any given environment.
- A migration that partially fails leaves the database in an unknown state with no alert.
- In a multi-region deployment, there is no mechanism to verify that all regions are on the
  same schema version.
- Rolling back a bad migration has no automated support — requires manual CQL intervention.
- The current silent-ignore behavior masks real errors during schema evolution.

**Proposed solution:**

```
internal/db/migrations/
  001_initial_schema.cql       ← current CREATE TABLE statements
  002_add_status_columns.cql   ← current ALTER TABLE statements
  003_add_traffic_tables.cql
  ...
  NNN_description.cql

Cassandra table: schema_migrations
  version    int          PRIMARY KEY
  name       text
  applied_at timestamp
  checksum   text         ← SHA-256 of the .cql file contents
```

**Migration runner (`internal/db/migrator.go`):**
- Reads all `.cql` files in `migrations/` sorted by number prefix.
- Compares against `schema_migrations` in the database.
- Applies only pending migrations, in order, within a logged batch where possible.
- Records each applied migration with timestamp and checksum.
- **Fails at startup** (non-zero exit) if a previously-applied migration's checksum no longer
  matches its file — prevents silent drift after post-application edits.
- Supports a `--dry-run` flag that reports pending migrations without applying them.
- Supports a `--check` flag (suitable for CI) that exits non-zero if any migration is pending.

**Migration:** The current inline CQL statements in `db.go` become the first numbered `.cql`
files. No schema changes are needed — this is purely a reorganization of the runner.

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
- `config.example.yaml:69-77` — region/class config already documented

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

### 7. Enforcement Phase 2 — Capability Resolver Wire-Up

**Current state:** `internal/plans/resolver.go` is fully implemented and has comprehensive tests
(`resolver_test.go`, `roles_test.go`). It is not wired to any HTTP handler.

**What is missing:**

- **`GET /api/v2.1/account/info/`** — must call `plans.ResolveCapabilities()` and return the
  resolved capability map, upgrade features list, limits, and storage/traffic digests. The frontend
  currently consumes a legacy response format that does not include the new capability system.
- **Group creation enforcement** — `internal/api/v2/groups.go` must check `max_groups` limit via
  `enforcement.CountActiveGroups()` (function does not exist yet, needs to be added to
  `enforcement.go` following the same pattern as `CountActiveLibraries()`).
- **Guest invitation enforcement** — `can_invite_guests` feature flag must gate the invite endpoint.
- **Publish repository enforcement** — `can_publish_repo` feature flag must gate library publishing.
- **Frontend migration** — `frontend/src/utils/seafile-api.js` and the components that consume
  `account/info` need to be updated to read from the new capabilities contract.

**Key files:**
- `internal/plans/resolver.go:28-56` — `ResolveCapabilities()`, ready to use
- `internal/plans/roles.go` — `AllFeatureFlags`, `RolePermissions`, `ProfileFeatureMap()`
- `internal/api/v2/enforcement.go` — add `CountActiveGroups()` here
- `internal/api/v2/groups.go` — wire enforcement on group creation
- `internal/api/v2/libraries.go` — wire enforcement on publish
- `frontend/src/utils/seafile-api.js` — update `getAccountInfo()` consumer

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
API tokens have a 180-day TTL with no bulk revocation. When an org admin deactivates a user,
their API tokens are currently not invalidated. Add API token invalidation to `deactivateUser()`
in `write_helpers.go`, following the same pattern as `InvalidateUserSessions()`.

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
- `internal/api/v2/share_links.go:408` — `// In-memory pagination (TODO: migrate to PageState cursor-based pagination)`
- `internal/api/v2/upload_links.go:191` — same comment

The current implementation fetches all rows for an org and paginates in memory. For orgs with
thousands of links, this loads unbounded data into RAM on every page request.

**Fix:** Use gocql's `PageState` for cursor-based pagination:
```go
query.PageSize(pageSize)
query.PageState(decodedCursor)
iter := query.Iter()
// ...
nextCursor := base64.StdEncoding.EncodeToString(iter.PageState())
```
Return `next_cursor` in the response. Clients pass `cursor=` on subsequent requests.

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

| # | Area | Priority | Current State | Estimated Effort |
|---|------|----------|---------------|-----------------|
| 1 | Accounts ↔ SesameFS Phase 2 | **P0** | Phase 1 done, Phase 2 pending | 2–3 weeks |
| 2 | Go code reorganization | **P0** | Not started | 3–4 weeks |
| 3 | Frontend/Backend separation + Nginx | **P0** | ✅ DONE (2026-03-30/31) | — |
| 4 | Robust DB migration system | **P0** | Not started | 1 week |
| 5 | Storage classes & multi-region | **P1** | Infra ready, ~30% | 2 weeks |
| 6 | Antivirus / malware scanning | **P1** | 0% | 1–2 weeks |
| 7 | Enforcement Phase 2 wire-up | **P1** | Resolver ready, not wired | 1 week |
| 8 | Persistent audit logs | **P1** | Framework only, not persisted | 3–4 days |
| 9 | Security hardening | **P1** | Partial (Nginx has headers) | 2–3 days |
| 10 | Backup and disaster recovery | **P1** | Nothing exists | 1 week |
| 11 | Email / notifications | **P2** | 0% | 1 week |
| 12 | Cursor-based pagination | **P2** | TODO stubs | 3 days |
| 13 | Cold storage / Glacier | **P2** | ~30% | 2–3 weeks |

---

## Recommended Execution Order

### Sprint 1 — Infrastructure Foundation
- [#4] DB migration system — enables safe schema changes for all subsequent work
- [#3] Frontend/backend separation + Nginx — unblocks independent deploys

### Sprint 2 — Code Quality + Security Baseline
- [#2] Go code reorganization — start with the four largest files
- [#9] Security hardening — small effort, high impact
- [#8] Persistent audit logs — framework already exists, wire it up

### Sprint 3 — Business Logic
- [#1] Accounts ↔ SesameFS Phase 2 — rollover job, M2M auth, enforcement
- [#7] Enforcement Phase 2 — wire `account/info`, groups, guests, publish

### Sprint 4 — Storage + Safety
- [#5] Storage classes and multi-region UI
- [#6] Antivirus / ClamAV
- [#10] Backup and disaster recovery

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
| `internal/db/db.go` | Current inline migrations — migrate to runner |
| `internal/api/server.go` | Frontend serving logic to remove |
| `nginx/nginx.conf.template` | Base for updated Nginx architecture |
| `docker-compose.prod.yml` | Starting point for production deployment |
| `internal/middleware/audit.go` | Audit framework — wire to DB |
| `internal/middleware/ratelimit.go` | Rate limiting — extend for per-user brute force |
| `docs/PLANS-AND-PERMISSIONS.md` | Capabilities system design reference |
