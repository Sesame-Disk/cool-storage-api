# Storage & Traffic Quotas — Implementation Plan

## Update 2026-03-28 (Phase 2 backend implemented)

This document originally described traffic enforcement using natural monthly partitions only.
The current agreed design is now implemented in the backend through Phase 2:

- `traffic_counters` and `traffic_monthly` remain useful for analytics and natural-month reporting.
- Traffic quota enforcement now uses the org's current quota period, not the natural UTC month.
- Every org should carry `current_period_started_at` and `current_period_ends_at`.
- `traffic_period_usage` is the canonical aggregate for quota enforcement and Phase 2 account/subscription traffic payloads.
- `billing_cycle` remains commercial metadata only; even annual plans still enforce monthly traffic quotas.
- Storage does not use periods. Storage is simply current usage vs current limit.
- Accounts may send `current_period_ends_at` explicitly. If it does not, SesameFS derives it from `current_period_started_at` with the same shared quota-period helper used by org creation defaults and rollover: always advance one calendar month and clamp the day when the target month is shorter.
- The local rollover cron still advances expired monthly periods even for annual billing plans. Accounts can later overwrite the dates for paid orgs, but enforcement must never wait on an external sync to keep monthly traffic moving.
- Quota contract units use decimal storage units: `GB`/`TB` in plans, defaults, API payloads and quota UI mean base-1000 bytes. Binary units remain reserved for technical/internal thresholds and should be labeled `GiB`/`MiB` if exposed.
- The subscription page is informational only. Billing actions happen through SesameFS `/billing/`, which redirects authenticated users to the external billing portal. The active UI route is the org-admin subscription view at `/org/subscription/`.

Where this document refers to "monthly traffic reset" for enforcement, read it as "quota-period rollover" under the current design.

## Context

SesameFS has quota columns in the DB (`storage_quota`, `storage_used`, `quota_bytes`, `used_bytes`) but **they were never updated**. No traffic tracking existed. Statistics endpoints returned empty stubs or 501. The frontend already has chart/table pages ready and waiting for real data.

**Goal**: Implement real storage and traffic tracking, per-plan quota enforcement, and activate the statistics endpoints.

---

## Business Rules

### Current Plans

An external billing service manages plans and calls the SesameFS admin API to set limits on each org. SesameFS **does not manage billing** — it only stores the current limits and enforces them.

#### Monthly Plans

| Plan | Price | Users | Storage | Upload/mo | Download/mo | Traffic |
|------|-------|-------|---------|-----------|-------------|---------|
| **Personal Free** | Free | 1 | 2 GB | 10 GB | 10 GB | Combined* |
| **Starter** | $4/mo | 1 incl. | 250 GB | 50 TB | 250 GB | Separate |
| **StarterPlus** | $10/mo | 1 incl. | 2 TB | 50 TB | 2 TB | Separate |
| **Business** | $40/mo | 1 incl. | 8 TB | 50 TB | 8 TB | Separate |
| **Enterprise** | Custom | Custom | Custom | Custom | Custom | Custom |

*Free has 10GB **combined** (upload+download together). Paid plans have separate limits.

#### Annual Plans

Same tiers with annual discount. The difference:
- **Billing cycle** (monthly/annual) = payment and renewal frequency
- **Traffic quota period** = always monthly, regardless of billing cycle
- An annual plan with 250GB/mo download does not accumulate: each month resets to 0
- The billing service can call the API to change quotas at any time (upgrade, downgrade, renewal)

#### On-demand (Paid Plans)

Paid plans include 1 user. Beyond what is included:
- **Extra storage**: billing charges by usage tier
- **Extra traffic**: billing charges by usage tier
- **Extra users**: billing charges per additional user
- SesameFS **does not block** on paid plans — only warns (soft warning). Billing handles the charges.

### Per-Org Data Model

Billing sets these fields on each org via `PUT /admin/organizations/:org_id/`:

For org-level quotas, any value `<= 0` means SesameFS treats that dimension as unlimited for enforcement. That does not imply free billing: Accounts may still charge overages, extra included capacity, or extra seats commercially.

| Field | Type | Free | Starter | Enterprise | Meaning |
|-------|------|------|---------|------------|---------|
| `plan` | string | `"free"` | `"starter"` | `"enterprise-acme"` | Plan name |
| `billing_cycle` | string | `"monthly"` | `"monthly"` | `"annual"` | Billing cycle |
| `current_period_started_at` | timestamp | org create time | billing anchor | billing anchor | Current quota period start |
| `current_period_ends_at` | timestamp | derived or provided | derived or provided | derived or provided | Current quota period end |
| `storage_quota` | int64 | 2 GB | 250 GB | custom | Storage limit. Any value `<= 0` = unlimited in SesameFS enforcement |
| `traffic_quota` | int64 | 10 GB | -1 | custom | Combined monthly limit (upload+download). Universal field — if positive, it is the limit. Any value `<= 0` = no combined limit |
| `traffic_upload_quota` | int64 | -1 | 50 TB | custom | Monthly upload limit. Any value `<= 0` = no individual upload limit |
| `traffic_download_quota` | int64 | -1 | 250 GB | custom | Monthly download limit. Any value `<= 0` = no individual download limit |
| `max_users` | int | 1 | -1 | 50 | Hard user cap. Any value `<= 0` = unlimited in SesameFS enforcement; Accounts may still bill extra seats |

**Traffic evaluation logic (most restrictive wins):**
1. If `traffic_quota > 0` → check `upload_used + download_used <= traffic_quota`
2. If `traffic_upload_quota > 0` → check `upload_used <= traffic_upload_quota`
3. If `traffic_download_quota > 0` → check `download_used <= traffic_download_quota`
4. Any check that fails → blocked (free) or warning (paid)

`traffic_quota` is the universal field. Billing can use it alone (simple) or combine it with individual limits (granular). Examples:

| Plan | traffic_quota | traffic_upload_quota | traffic_download_quota | Effect |
|------|--------------|---------------------|----------------------|--------|
| Free | 10 GB | -1 | -1 | 10GB total, distribute as desired |
| Custom simple | 500 GB | -1 | -1 | 500GB total, no per-direction limits |
| Starter | -1 | 50 TB | 250 GB | No total limit, but download capped at 250GB |
| Enterprise custom | 100 TB | -1 | 10 TB | 100TB total AND download max 10TB |

### Enforcement by Plan Type

**Implementation note (2026-03-27)**: Enforcement is now keyed by `quota_policy` ("hard"/"soft"), not by plan name. `isFree(plan)` has been replaced by `isHardEnforcement(quotaPolicy)` in `checker.go`. All checker queries (`CheckStorageQuota`, `CheckTrafficQuota`, `CheckMaxUsers`) read `quota_policy` from the organizations table.

| Scenario | `quota_policy="hard"` (Free) | `quota_policy="soft"` (Paid) |
|----------|-----------|-----------|
| Storage exceeded | **Hard block** — reject upload with 403 | **Soft warning** — allow, billing charges overage |
| Traffic exceeded (any check) | **Hard block** — reject with 403 | **Soft warning** — allow, billing charges overage |
| Max users exceeded | **Hard block** — reject user creation with 403 | If `max_users <= 0`: allow in SesameFS, billing may charge extra seats. If `max_users > 0`: hard block |
| Warning threshold | N/A (blocked directly) | Warn at 80% of included limit |

### Per-User Quotas

Within an org, the admin can assign individual limits per user:

- `quota_bytes` (storage) — already exists in schema (exposed as `quota_total` in API responses)
- `traffic_upload_quota` — new
- `traffic_download_quota` — new
- Value `<= 0` = no individual storage limit; the org-level cap applies
- Most restrictive check wins: if the org is blocked, the user cannot upload even if they have individual quota remaining
- No `traffic_quota` (combined) at user level — only at org level

> **Resolved (2026-05-14):** Per-user storage enforcement (`quota_total`/`quota_bytes`) is enforced at upload time. `CheckStorageQuota` evaluates both org-level `storage_quota` and `users.quota_bytes`, reads the matching `storage_counters` scopes, and returns the most restrictive result.

---

## Traffic Tracking — 6 Categories

Compatible with Seafile. Upload and download are tracked separately, subdivided by channel:

| Traffic Type | Description | Handler(s) |
|-------------|-------------|------------|
| `sync-file-upload` | Desktop sync client uploads blocks | `PutBlock` (sync.go:762) |
| `sync-file-download` | Desktop sync client downloads blocks | `GetBlock` (sync.go:665) |
| `web-file-upload` | Web UI uploads files (resumable.js) | `HandleUpload` (seafhttp.go:481), `UploadFile` (files.go:2638), `UploadBlock` (blocks.go:149) |
| `web-file-download` | Web UI downloads files | `HandleDownload` (seafhttp.go:1251), `HandleZipDownload` (seafhttp.go:1584), `DownloadBlock` (blocks.go:244), `DownloadHistoricFile` (fileview.go:759) |
| `link-file-upload` | Upload via public share/upload link | `HandleUpload` with token source=link |
| `link-file-download` | Download via public share link | `handleShareLinkDownload` (sharelink_view.go:391) |

For quota enforcement, 3 checks are evaluated (most restrictive wins):
1. **Combined** (`traffic_quota`): upload_total + download_total vs combined limit
2. **Upload** (`traffic_upload_quota`): sync+web+link upload vs upload limit
3. **Download** (`traffic_download_quota`): sync+web+link download vs download limit

---

## Phase 1: Schema — New Tables and Columns

### Files to modify
- `internal/db/db.go` — add migrations
- `internal/models/models.go` — add fields to structs

### 1.1 Table `traffic_counters` (counter table)

Daily tracking per org/user/type. Partitioned by `(org_id, month)` for natural monthly scoping.

```sql
CREATE TABLE IF NOT EXISTS traffic_counters (
    org_id UUID,
    month TEXT,           -- '202603'
    day DATE,
    user_id UUID,
    traffic_type TEXT,    -- 'sync-file-upload', etc.
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month), day, user_id, traffic_type)
)
```

Existing pattern: `gc_queue_stats` already uses `COUNTER` (db.go:834).

Enables:
- Per-day queries (statistics with group_by=day)
- Monthly sums (quota check)
- Per-user breakdown (per-user statistics)

### 1.2 Table `traffic_monthly` (counter table — reporting / natural-month aggregate)

Aggregated monthly counters for reporting and dashboard reads.

```sql
CREATE TABLE IF NOT EXISTS traffic_monthly (
    org_id UUID,
    month TEXT,           -- '202603'
    scope TEXT,           -- 'org:upload', 'org:download', 'org:combined', '<user_id>:upload', '<user_id>:download'
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month), scope)
)
```

- `scope='org:upload'` = org's total upload (for `traffic_upload_quota` check)
- `scope='org:download'` = org's total download (for `traffic_download_quota` check)
- `scope='org:combined'` = org's total upload+download (for combined `traffic_quota` check)
- `scope='<user_id>:upload'` = user's upload
- `scope='<user_id>:download'` = user's download
- Incremented in parallel with `traffic_counters` (fire-and-forget)
- Each operation increments 3 org scopes (upload or download + combined) and 1 user scope

### 1.2.1 Table `traffic_period_usage` (counter table — fast quota enforcement)

Recommended aggregate keyed by the org's current quota period.

```sql
CREATE TABLE IF NOT EXISTS traffic_period_usage (
    org_id UUID,
    period_started_at TIMESTAMP,
    scope TEXT,           -- 'org:upload', 'org:download', 'org:combined', '<user_id>:upload', '<user_id>:download'
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, period_started_at), scope)
)
```

This table is the preferred source for quota enforcement once the current-period model is implemented.
The rollover job should operate by `current_period_ends_at <= now`, not by natural month boundaries.

### 1.3 Table `storage_counters` (counter table)

Atomic storage counters for concurrent increment/decrement.

```sql
CREATE TABLE IF NOT EXISTS storage_counters (
    scope TEXT,           -- 'org:<uuid>', 'user:<org_uuid>:<user_uuid>', 'lib:<org_uuid>:<lib_uuid>'
    bytes_used COUNTER,
    file_count COUNTER,
    PRIMARY KEY ((scope))
)
```

### 1.4 ALTER TABLE organizations

```sql
ALTER TABLE organizations ADD traffic_quota BIGINT          -- combined monthly limit (-1=N/A)
ALTER TABLE organizations ADD traffic_upload_quota BIGINT   -- upload monthly limit (-1=unlimited)
ALTER TABLE organizations ADD traffic_download_quota BIGINT -- download monthly limit (-1=unlimited)
ALTER TABLE organizations ADD max_users INT                 -- hard cap (-1=unlimited)
ALTER TABLE organizations ADD plan TEXT                     -- plan name from billing
ALTER TABLE organizations ADD billing_cycle TEXT            -- "monthly" | "annual"
ALTER TABLE organizations ADD current_period_started_at TIMESTAMP
ALTER TABLE organizations ADD current_period_ends_at TIMESTAMP
```

`storage_quota` already exists. The new fields allow the billing service to set everything via API.

### 1.5 ALTER TABLE users

```sql
ALTER TABLE users ADD traffic_upload_quota BIGINT
ALTER TABLE users ADD traffic_download_quota BIGINT
```

`quota_bytes` (storage) already exists.

### 1.6 Update structs in models.go

```go
// Organization — add:
TrafficQuota         int64  `json:"traffic_quota"`          // combined monthly limit, -1=N/A
TrafficUploadQuota   int64  `json:"traffic_upload_quota"`   // upload monthly limit, -1=unlimited
TrafficDownloadQuota int64  `json:"traffic_download_quota"` // download monthly limit, -1=unlimited
MaxUsers             int    `json:"max_users"`              // hard cap, -1=unlimited
Plan                 string `json:"plan,omitempty"`
BillingCycle         string `json:"billing_cycle,omitempty"` // "monthly" | "annual"
CurrentPeriodStartedAt time.Time `json:"current_period_started_at,omitempty"`
CurrentPeriodEndsAt    time.Time `json:"current_period_ends_at,omitempty"`

// User — add:
TrafficUploadQuota   int64 `json:"traffic_upload_quota"`   // -1=inherit from org
TrafficDownloadQuota int64 `json:"traffic_download_quota"` // -1=inherit from org
```

---

## Phase 2: TrafficRecorder — Core Tracking

### New files
- `internal/traffic/recorder.go`

### Files to modify
- `internal/api/server.go` — initialization

### 2.1 TrafficRecorder struct

```go
package traffic

type Recorder struct {
    session *gocql.Session
}

// Record logs transferred bytes. Runs in a goroutine, never blocks the request.
func (r *Recorder) Record(orgID, userID, trafficType string, bytes int64)
```

Internal logic:
1. Compute `month := time.Now().Format("200601")` and `day := time.Now().Truncate(24h)`
2. Increment `traffic_counters` with (org_id, month, day, user_id, traffic_type)
3. Determine direction: if trafficType contains "upload" → direction="upload"; if "download" → direction="download"
4. Increment `traffic_monthly` with 3 scopes for natural-month reporting:
    - `"org:<direction>"` (e.g., `"org:upload"`) — monthly directional totals
    - `"org:combined"` — monthly combined total
    - `"<userID>:<direction>"` — monthly per-user directional total
5. Increment `traffic_period_usage` with the same 3 scopes keyed by `current_period_started_at`
    - this is the aggregate used for quota enforcement and current-period account/subscription payloads
6. All inside `go func() { ... }()` — fire-and-forget
7. Errors are logged, never propagated

### 2.2 Constants

```go
const (
    SyncUpload   = "sync-file-upload"
    SyncDownload = "sync-file-download"
    WebUpload    = "web-file-upload"
    WebDownload  = "web-file-download"
    LinkUpload   = "link-file-upload"
    LinkDownload = "link-file-download"
)
```

### 2.3 Global injection

Existing pattern: `SetGCHooks` in gc_hooks.go.

```go
var globalRecorder struct {
    mu sync.RWMutex
    r  *Recorder
}

func SetRecorder(r *Recorder) { ... }
func Get() *Recorder { ... }
```

Initialized in `server.go` alongside other services.

---

## Phase 3: Instrumentation — Uploads

### Files to modify
- `internal/api/seafhttp.go` — HandleUpload, AccessToken struct
- `internal/api/sync.go` — PutBlock
- `internal/api/v2/blocks.go` — UploadBlock
- `internal/api/v2/files.go` — UploadFile
- `internal/db/tokens.go` — AccessToken struct (add Source)
- `internal/api/v2/sharelink_view.go` — mark tokens as "link"

### 3.1 Add `Source` to AccessToken

```go
// In both AccessToken (seafhttp.go:43 and db/tokens.go:21)
Source string // "web" (default) or "link" (share/upload link)
```

In `GetUploadLinkUploadURL` (sharelink_view.go:1593) and `GetShareLinkUploadURL` (sharelink_view.go:1685): when creating the token, set `Source: "link"`. Requires extending `CreateUploadToken` to accept source, or creating `CreateUploadTokenWithSource`.

### 3.2 Instrumentation points

| Handler | File:Line | Traffic Type | Size Source | When |
|---------|-----------|-------------|-------------|------|
| `HandleUpload` | seafhttp.go:~710 | `WebUpload` or `LinkUpload` (based on token.Source) | `finalSize` from commit | After successful commit |
| `PutBlock` | sync.go:~868 | `SyncUpload` | `len(data)` | After successful store |
| `UploadBlock` | blocks.go:~240 | `WebUpload` | `len(data)` | After successful store |
| `UploadFile` | files.go:~2700 | `WebUpload` | `len(content)` | After successful commit |

Instrumentation example:
```go
// At the end of HandleUpload, after successful commit:
if rec := traffic.Get(); rec != nil {
    tt := traffic.WebUpload
    if token.Source == "link" {
        tt = traffic.LinkUpload
    }
    rec.Record(token.OrgID, token.UserID, tt, finalSize)
}
```

---

## Phase 4: Instrumentation — Downloads

### Files to modify
- `internal/api/seafhttp.go` — HandleDownload, HandleZipDownload
- `internal/api/sync.go` — GetBlock
- `internal/api/v2/blocks.go` — DownloadBlock
- `internal/api/v2/sharelink_view.go` — handleShareLinkDownload
- `internal/api/v2/fileview.go` — DownloadHistoricFile

### 4.1 Instrumentation points

| Handler | File | Traffic Type | Size Source | When |
|---------|------|-------------|-------------|------|
| `HandleDownload` | seafhttp.go | `WebDownload` | `fileSize` from fs_objects | After successful streaming |
| `HandleZipDownload` | seafhttp.go | `WebDownload` | accumulated byte count | After zip stream |
| `GetBlock` | sync.go | `SyncDownload` | `len(data)` | After sending |
| `DownloadBlock` | blocks.go | `WebDownload` | `len(data)` | After sending |
| `handleShareLinkDownload` | sharelink_view.go | `LinkDownload` | `fileSize` from metadata | After streaming |
| `DownloadHistoricFile` | fileview.go | `WebDownload` | file size | After sending |

Note: Record AFTER successful send. If streaming fails midway, ideally count bytes sent. If not practical, don't count (the error is rare and drift is acceptable).

---

## Phase 5: Storage Tracking

### Files to modify
- `internal/traffic/storage.go` — increment/decrement helpers
- `internal/api/seafhttp.go` — increment on upload
- `internal/api/sync.go` — increment on PutBlock
- `internal/api/v2/files.go` — increment on UploadFile, decrement on DeleteFile
- `internal/api/v2/libraries.go` — decrement on delete library
- `internal/gc/worker.go` — periodic recalculation

### 5.1 Helpers in traffic/storage.go

```go
func IncrementStorageCounters(db, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64)
func DecrementStorageCounters(db, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64)
```

Each performs 4 counter updates: `platform`, `org:<orgID>`, `user:<orgID>:<userID>`, `lib:<orgID>:<libID>`. Fire-and-forget in goroutine.

### 5.2 Instrumentation

- **Upload**: after successful commit, apply the visible path delta with `AdjustStorageCountersByDeltaSync(db, orgID, userID, libID, deltaBytes, deltaFiles)`. Replaces use `newSize-oldSize`; autorename/new files use `newSize,+1`.
- **Delete file**: in `DeleteFile`, `DecrementStorageCounters(db, orgID, userID, libID, fileSize, 1)`
- **Delete library**: on soft-delete (trash), decrement aggregates but preserve lib counter. On hard-delete (GC cascade), clean up lib counter row.

### 5.3 Deduplication

Only increment storage when the block is **new** (`ref_count` goes from 0 to 1). If it already exists (dedup), don't increment. The handlers already detect this.

### 5.4 Periodic Recalculation (GC Phase 13)

New phase in `gc/worker.go`: `RecalculateStorageCounters`
1. For each org: sum `size_bytes` of all active libraries → UPDATE `organizations.storage_used`
2. For each user: sum `size_bytes` of their libraries → UPDATE `users.used_bytes`
3. For each library: sum `size_bytes` of head commit's fs_objects → UPDATE `libraries.size_bytes`
4. Runs 1x/day. Corrects accumulated drift.

---

## Phase 6: Quota Enforcement

### New files
- `internal/traffic/checker.go`

### Files to modify
- `internal/api/sync.go` — QuotaCheck (line 1636)
- `internal/api/seafhttp.go` — pre-check in HandleUpload
- `internal/api/v2/files.go` — pre-check in UploadFile
- `internal/api/v2/blocks.go` — pre-check in UploadBlock
- `internal/api/v2/admin.go` — pre-check on user creation (max_users)

### 6.1 QuotaChecker

```go
type QuotaStatus struct {
    Allowed    bool   // can proceed
    Warning    bool   // >80% of limit (paid plans only)
    UsedBytes  int64  // current usage
    LimitBytes int64  // limit of the check that failed (-1=unlimited)
    Reason     string // "storage", "traffic-combined", "traffic-upload", "traffic-download", "max-users"
    Plan       string // plan name
}

type Checker struct {
    session *gocql.Session
}

func (c *Checker) CheckStorageQuota(orgID, userID string, additionalBytes int64) (QuotaStatus, error)
func (c *Checker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (QuotaStatus, error)
func (c *Checker) CheckMaxUsers(orgID string) (QuotaStatus, error)
```

### 6.2 CheckTrafficQuota Logic

`direction` is `"upload"` or `"download"`. A single method evaluates all 3 traffic checks and returns the most restrictive.

```
CheckTrafficQuota(orgID, userID, direction, additionalBytes):

    1. SELECT traffic_quota, traffic_upload_quota, traffic_download_quota, plan,
         current_period_started_at
     FROM organizations WHERE org_id = ?

    2. period_started_at = current_period_started_at if present,
         else first day of current UTC month (backward-compatible fallback)

  3. CHECK 1: Combined quota (traffic_quota)
         If traffic_quota > 0:
             SELECT bytes FROM traffic_period_usage
             WHERE org_id=? AND period_started_at=? AND scope='org:combined'
       Evaluate: combined_used + additional > traffic_quota

  4. CHECK 2: Direction quota (traffic_upload_quota or traffic_download_quota)
     quota = traffic_upload_quota if direction=="upload", else traffic_download_quota
         If quota > 0:
             SELECT bytes FROM traffic_period_usage
             WHERE org_id=? AND period_started_at=? AND scope='org:<direction>'
       Evaluate: direction_used + additional > quota

  5. CHECK 3: Per-user quota
     If userID != "":
       SELECT traffic_upload_quota, traffic_download_quota FROM users WHERE org_id=? AND user_id=?
       user_quota = the one corresponding to direction
       If user_quota != -1:
                 SELECT bytes FROM traffic_period_usage
                 WHERE org_id=? AND period_started_at=? AND scope='<userID>:<direction>'
         Evaluate: user_used + additional > user_quota

  6. For each check that fails:
     If plan == "" or plan == "free" → Allowed=false (hard block)
     If paid plan → Allowed=true, Warning=true

  7. Return the MOST restrictive status of the 3 checks
     Reason = the check that failed ("traffic-combined", "traffic-upload", "traffic-download")
```

### 6.3 Handler Integration

**QuotaCheck endpoint** (sync.go:1636 — desktop client):
```go
func (h *SyncHandler) QuotaCheck(c *gin.Context) {
    orgID := c.GetString("org_id")
    userID := c.GetString("user_id")
    status, _ := checker.CheckStorageQuota(orgID, userID, 0)
    c.JSON(200, gin.H{
        "has_quota":  status.Allowed,
        "remaining":  status.LimitBytes - status.UsedBytes,
    })
}
```

**Upload pre-check** (before reading request data):
```go
storageStatus, _ := checker.CheckStorageQuota(orgID, userID, contentLength)
if !storageStatus.Allowed {
    c.JSON(403, gin.H{"error": "storage quota exceeded"})
    return
}
trafficStatus, _ := checker.CheckTrafficQuota(orgID, userID, "upload", contentLength)
if !trafficStatus.Allowed {
    c.JSON(403, gin.H{"error": "traffic quota exceeded", "reason": trafficStatus.Reason})
    return
}
if trafficStatus.Warning {
    c.Header("X-Quota-Warning", trafficStatus.Reason)
}
// proceed with upload...
```

**Download pre-check** (before streaming):
```go
trafficStatus, _ := checker.CheckTrafficQuota(orgID, userID, "download", fileSize)
if !trafficStatus.Allowed {
    c.JSON(403, gin.H{"error": "traffic quota exceeded", "reason": trafficStatus.Reason})
    return
}
if trafficStatus.Warning {
    c.Header("X-Quota-Warning", trafficStatus.Reason)
}
```

**Create user** (admin.go, org_admin.go):
```go
usersStatus, _ := checker.CheckMaxUsers(orgID)
if !usersStatus.Allowed {
    c.JSON(403, gin.H{"error": "user limit reached"})
    return
}
```

### 6.4 Warning Header

For paid plans with soft warning, set header `X-Quota-Warning: storage|traffic-upload|traffic-download` so the frontend can show a notice without blocking.

### 6.5 Share Link Enforcement

Share link downloads/uploads: traffic counts against the link creator's org. If the creator's org is on free and has exceeded quota, the share link returns 403. The `trafficOverLimit` field in share link responses (currently hardcoded to `false` in sharelink_view.go) evaluates against real quota.

---

## Phase 7: Statistics API

### Files to modify
- `internal/api/v2/admin_extra.go` — replace stubs
- `internal/api/v2/admin.go` — add new routes
- `internal/api/v2/org_admin.go` — replace 501 stubs
- `frontend/src/utils/seafile-api.js` — fix broken URLs

### 7.1 Replace Existing Stubs

**`AdminStatisticTraffic`** (admin_extra.go:152):
1. Reuse existing `generateDateRange(c)` to parse start/end/group_by
2. For each date: query `traffic_counters` partition `(platform_org_id, month)` filtering by `day`
3. For superadmin: iterate all organizations, or use a special "platform" partition that accumulates cross-organization traffic
4. Sum by traffic_type, return existing format:
```json
[{"datetime": "2026-03-24T00:00:00+00:00", "sync-file-upload": 12345, "sync-file-download": 67890, "web-file-upload": 11111, "web-file-download": 22222, "link-file-upload": 3333, "link-file-download": 4444}]
```

**`AdminStatisticStorage`** (admin_extra.go:116):
- Query `storage_counters` for each org, sum totals per day
- Return `[{datetime, total_storage}]`

**`OrgStatisticTraffic`** (org_admin.go — currently 501):
- Same as AdminStatisticTraffic but scoped to the caller's org

**`OrgStatisticUserTraffic`** (org_admin.go — currently 501):
- Query `traffic_counters` for a month, group by user_id
- Return paginated list:
```json
{
  "user_monthly_traffic_list": [
    {"email": "user@example.com", "name": "User", "sync_file_upload": 123, "sync_file_download": 456, "web_file_upload": 789, "web_file_download": 012, "link_file_upload": 345, "link_file_download": 678}
  ],
  "has_next_page": false
}
```

### 7.2 New Endpoints

| Route | Handler | Description |
|-------|---------|-------------|
| `GET /admin/statistics/user-traffic/` | `AdminListUserTraffic` | Per-user traffic cross-org (superadmin) |
| `GET /admin/statistics/org-traffic/` | `AdminListOrgTraffic` | Per-org traffic summary (superadmin) |

### 7.3 Frontend Fix (seafile-api.js)

Current bugs:
- `orgAdminStatisticSystemTraffic()` points to `/total-storage/` → fix to `/system-traffic/`
- `orgAdminListUserTraffic()` points to `/total-storage/` → fix to `/user-traffic/`

Missing functions to add:
- `sysAdminListUserTraffic(month, page, perPage, orderBy)` → `/admin/statistics/user-traffic/`
- `sysAdminListOrgTraffic(month, page, perPage, orderBy)` → `/admin/statistics/org-traffic/`

---

## Phase 8: Plan/Quota API (for External Billing Service)

### Files to modify
- `internal/api/v2/admin.go` — extend PUT org, PUT user endpoints
- `internal/api/v2/admin_extra.go` — subscription info endpoint
- `internal/api/v2/org_admin.go` — expose quota status to org admin

### 8.1 Extend PUT /admin/organizations/:org_id/

Already accepts `storage_quota`. Add support for all fields that billing sends:

```
storage_quota            (int64, bytes) — already exists
traffic_quota            (int64, bytes/mo combined, -1=N/A) — NEW
traffic_upload_quota     (int64, bytes/mo, -1=unlimited) — NEW
traffic_download_quota   (int64, bytes/mo, -1=unlimited) — NEW
max_users                (int, -1=unlimited) — NEW
plan                     (string, plan name) — NEW
billing_cycle            (string, "monthly"|"annual") — NEW
```

**Example: billing sets free plan**
```json
{
    "plan": "free",
    "billing_cycle": "monthly",
    "storage_quota": 2000000000,
    "traffic_quota": 10000000000,
    "traffic_upload_quota": -1,
    "traffic_download_quota": -1,
    "max_users": 1
}
```

**Example: billing sets Starter plan**
```json
{
    "plan": "starter",
    "billing_cycle": "monthly",
    "storage_quota": 250000000000,
    "traffic_quota": -1,
    "traffic_upload_quota": 50000000000000,
    "traffic_download_quota": 250000000000,
    "max_users": -1
}
```

### 8.2 Extend PUT /admin/organizations/:org_id/users/:email/

Already accepts `quota_total` (storage). Add:
```
traffic_upload_quota   (int64, bytes/mo)
traffic_download_quota (int64, bytes/mo)
```

The org admin can assign individual traffic limits.

### 8.3 GET /api/v2.1/subscription/ (new)

Returns current plan info for the authenticated user's org. Consumed by the frontend subscription component.

```json
{
    "plan": "free",
    "billing_cycle": "monthly",
    "storage_quota": 2000000000,
    "storage_used": 1000000000,
    "storage_percent": 50.0,
    "traffic_quota": 10000000000,
    "traffic_combined_used": 5000000000,
    "traffic_upload_quota": -1,
    "traffic_upload_used": 2500000000,
    "traffic_download_quota": -1,
    "traffic_download_used": 2500000000,
    "traffic_reset_date": "2026-04-01",
    "max_users": 1,
    "current_users": 1
}
```

### 8.4 Extend GET /org/admin/info/

Already returns `storage_quota` and `storage_usage`. Add:
```json
{
    "traffic_quota": 10000000000,
    "traffic_combined_used": 5000000000,
    "traffic_upload_quota": -1,
    "traffic_upload_used": 2500000000,
    "traffic_download_quota": -1,
    "traffic_download_used": 2500000000,
    "max_users": 1,
    "plan": "free",
    "billing_cycle": "monthly"
}
```

### 8.5 Extend GET /api/v2.1/account/info/

Already returns `total` (storage quota) and `usage` (storage used). Add per-user traffic info:
```json
{
    "traffic_upload_quota": -1,
    "traffic_upload_used": 536870912,
    "traffic_download_quota": -1,
    "traffic_download_used": 1073741824
}
```

---

## Implementation Order

```
Phase 1 (Schema)
    |
    v
Phase 2 (TrafficRecorder core)
    |
    +---> Phase 3 (Upload instrumentation)  +---> Phase 5 (Storage tracking)
    |                |                              |
    |                v                              v
    |         Phase 4 (Download instr.)      Phase 6 (Quota enforcement)
    |                |                              |
    |                v                              |
    +---------> Phase 7 (Statistics API) <----------+
                     |
                     v
              Phase 8 (Plan/Quota API)
```

- Phases 3+5 are parallel (no dependencies between them)
- Phase 4 depends on Phase 3 (same mechanics, complements it)
- Phase 6 depends on Phase 5 (needs storage counters)
- Phase 7 needs Phases 3+4 (needs traffic data)
- Phase 8 is mostly independent but goes last (needs Phase 1 fields)

---

## Critical Files (Summary)

| File | Changes |
|------|---------|
| `internal/db/db.go` | 3 new tables (traffic_counters, traffic_monthly, storage_counters) + 6 ALTER TABLE |
| `internal/models/models.go` | New fields in Organization, User |
| `internal/traffic/recorder.go` | **New** — TrafficRecorder |
| `internal/traffic/checker.go` | **New** — QuotaChecker |
| `internal/traffic/storage.go` | **New** — Storage counter helpers (Increment/Decrement/Read/Adjust/Delete) |
| `internal/api/seafhttp.go` | AccessToken.Source, instrument HandleUpload/HandleDownload, pre-checks |
| `internal/api/sync.go` | Instrument PutBlock/GetBlock, implement real QuotaCheck |
| `internal/api/v2/blocks.go` | Instrument UploadBlock/DownloadBlock, pre-checks |
| `internal/api/v2/files.go` | Instrument UploadFile, decrement on DeleteFile |
| `internal/api/v2/write_helpers.go` | softDeleteLibrary/restoreDeletedLibrary (delegates to traffic package) |
| `internal/api/v2/admin_extra.go` | Replace statistics stubs, subscription endpoint |
| `internal/api/v2/admin.go` | New statistics routes, extend PUT org |
| `internal/api/v2/org_admin.go` | Replace 501 stubs, extend endpoints |
| `internal/api/v2/sharelink_view.go` | Token source=link, real trafficOverLimit |
| `internal/api/v2/fileview.go` | Instrument DownloadHistoricFile |
| `internal/api/v2/libraries.go` | Decrement storage on delete |
| `internal/api/server.go` | Initialize TrafficRecorder and QuotaChecker |
| `internal/gc/store_cassandra.go` | SoftDeleteLibrary delegates to traffic.AdjustAggregateStorageCounters |
| `internal/db/tokens.go` | AccessToken.Source field |
| `frontend/src/utils/seafile-api.js` | Fix broken URLs, add missing functions |

---

## ScyllaDB Considerations

### Counter Tables
- We use counter tables for `traffic_counters`, `traffic_monthly`, and `storage_counters`
- Counter tables do NOT support TTL — manual cleanup of old months if necessary
- Counter tables do NOT support conditional updates (LWT) — not a problem for our use case
- Counters are eventually consistent — acceptable for quotas (temporary drift is ok)

### Partitioning
- `traffic_counters`: partition `(org_id, month)` — one partition per org per month (~6 types * ~30 days * N users rows)
- `traffic_monthly`: partition `(org_id, month)` — few rows per partition (2 org scopes + 2 per user)
- `storage_counters`: partition `(scope)` — one row per entity (org, user, library)

### Hot Partitions
- Organizations with massive traffic could create hot partitions in `traffic_counters`
- Mitigation: partitioning by `(org_id, month)` limits the size. If needed, shard by week
- Premature optimization — evaluate after real data

---

## Implementation Status (2026-03-28)

| Phase | Description | Status | Notes |
|-------|-------------|--------|-------|
| **Phase 1** | Schema — tables + ALTER TABLE | ✅ COMPLETE | 3 counter tables + 8 ALTER TABLE migrations in db.go |
| **Phase 2** | TrafficRecorder core | ✅ COMPLETE | `internal/traffic/recorder.go` — semaphore-bounded goroutines, UUID validation, platform aggregate |
| **Phase 3** | Upload instrumentation | ✅ COMPLETE | HandleUpload, PutBlock, UploadBlock, UploadFile. Token.Source distinguishes link vs web |
| **Phase 4** | Download instrumentation | ✅ COMPLETE | HandleDownload, HandleZipDownload, GetBlock, DownloadBlock, DownloadHistoricFile, handleShareLinkDownload |
| **Phase 5** | Storage tracking | ✅ COMPLETE | Increment/DecrementStorageCounters in `traffic/storage.go` (4 scopes), negative delta guard, library soft-delete/restore adjusts aggregates |
| **Phase 6** | Quota enforcement | ✅ COMPLETE for upload/download | CheckStorageQuota, CheckTrafficQuota, CheckMaxUsers. Free=hard block, paid=soft warning. Uploads check visible storage deltas, sync HEAD publication checks the committed tree delta and applies the counter delta before returning, downloads check traffic. Non-upload storage-growing mutations are tracked separately in ISSUE-QUOTA-COVERAGE-01, and split-phase publish/counter atomicity remains technical debt. |
| **Phase 7** | Statistics API | ✅ COMPLETE | AdminStatisticTraffic, AdminStatisticStorage, OrgStatisticTraffic, OrgStatisticUserTraffic, AdminListOrgTraffic, AdminListUserTraffic — all real data |
| **Phase 8** | Plan/Quota API | ✅ COMPLETE | PUT org accepts all plan fields, PUT user accepts traffic quotas, GET subscription/account info expose current-period traffic state and resolved plan capabilities |

### Phase 2 backend completion notes (2026-03-28)
- Added `traffic_period_usage` counter table to persist org-period aggregates.
- `TrafficRecorder` now writes both `traffic_monthly` and `traffic_period_usage` on every transfer.
- `QuotaChecker` now enforces traffic quotas against `traffic_period_usage` keyed by `current_period_started_at`.
- `account/info` and `subscription` now expose current-period traffic usage and use `current_period_ends_at` for reset timing.
- Natural-month analytics remain on `traffic_monthly`; period rollover is now handled by the quota-period job that advances orgs whose `current_period_ends_at <= now`.

### Post-implementation fixes (2026-03-25)
- **Recorder semaphore**: Moved `select` outside goroutine — drops records without spawning goroutines under load
- **DecrementStorageCounters**: Added early return guard for negative deltaBytes
- **Library soft-delete storage accounting**: `softDeleteLibrary()` decrements aggregates, `restoreDeletedLibrary()` re-adds them, `DeleteLibraryStorageCounter()` cleans up lib-scope row on permanent delete. GC delegates to shared `traffic` package.
- **Storage counter consolidation**: Moved `IncrementStorageCounters`, `DecrementStorageCounters`, `ReadStorageUsed`, `AdjustAggregateStorageCounters`, `DeleteLibraryStorageCounter` from `write_helpers.go` to `internal/traffic/storage.go`. Eliminated duplicated `decrementLibraryFromAggregates` in GC.

### Known scalability debt (v2)
- `COUNT(*)` for `CheckMaxUsers` — full partition scan. Replace with counter in v2. See `TECHNICAL-DEBT.md` § 12a.
- Platform traffic aggregate uses single hot partition. Shard in v2. See `TECHNICAL-DEBT.md` § 12b.

---

## Verification

### Unit Tests
- TrafficRecorder: mock session, verify generated queries
- QuotaChecker: test with free (hard block), paid (soft warning), and org quotas set to values `<= 0` (treated as unlimited in SesameFS)
- Storage counters: correct increment/decrement

### Integration Tests
- Upload file → verify traffic_counters incremented with correct type
- Upload file → verify traffic_monthly incremented (org:upload, org:combined, user:upload)
- Upload file → verify storage_counters incremented
- Download file → verify traffic_counters incremented
- Share link download → verify type `link-file-download`
- Sync upload → verify type `sync-file-upload`
- Free org with traffic_quota=10GB → upload 6GB + download 5GB → second op blocked (combined)
- Starter org with traffic_download_quota=250GB → download >250GB → warning header (soft)
- Free org with storage_quota=2GB → upload >2GB → 403 rejection
- Free org with max_users=1 → create second user → 403 rejection
