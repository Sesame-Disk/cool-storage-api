# SesameFS Database Guide

## Overview

SesameFS uses Apache Cassandra for metadata storage. This document explains each table, provides practical API usage examples, and outlines strategies for improving consistency guarantees.

---

## Schema Migrations

### System overview

Migrations are managed by `internal/db/migrator.go` using versioned `.cql` files embedded in the binary at compile time. Applied migrations are tracked in the `schema_migrations` table with SHA-256 checksums.

```
internal/db/migrations/
  001_initial_schema.cql     ← complete clean-boot baseline (all current tables)
  002_password_rate_limit.cql ← encrypted library password failure tracking
  NNN_description.cql        ← future incremental changes
```

### Tracking table

```cql
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INT PRIMARY KEY,
    name       TEXT,
    applied_at TIMESTAMP,
    checksum   TEXT        -- SHA-256 of the .cql file; mismatch = startup failure
);
```

### How it works

| Scenario | Behaviour |
|----------|-----------|
| Fresh install | Applies migrations in version order |
| Existing DB (first deploy of new system) | Detects legacy tables → stamps all migrations as applied without executing |
| Normal restart | Skips already-applied migrations; applies any new ones |
| Modified migration file | Checksum mismatch → server refuses to boot |
| Failed statement | Migration is not stamped → retried on next startup |

### Adding a new migration

1. Create `internal/db/migrations/NNN_description.cql` with the new CQL statements.
2. Deploy — the migration runs automatically on startup.
3. **Never edit a migration file after it has been applied.** Create a new numbered file instead.

### CLI commands

```bash
sesamefs migrate               # apply pending migrations + seed (normal operation)
sesamefs migrate --status      # show applied/pending table with checksums
sesamefs migrate --dry-run     # list pending migrations without applying them
sesamefs migrate --check       # exit non-zero if any migration is pending (CI)
```

### Key files

| File | Purpose |
|------|---------|
| `internal/db/migrator.go` | `Migrator` — `Run()`, `Status()`, `DryRun()`, `Check()` |
| `internal/db/migrations/*.cql` | Versioned schema files (embedded in binary) |
| `internal/db/db.go` | `DB.Migrate()` — calls runner + idempotent Go backfills |

---

## API Keys and Derived Sessions

`001_initial_schema.cql` currently owns both the API key tables and the session provenance needed for strong revocation semantics, because the clean-boot baseline folds those schema elements directly into version 001.

| Schema element | Purpose |
|------|---------|
| `api_keys` | Primary table keyed by `key_hash` |
| `api_keys_by_user` | Reverse index for listing a user's keys newest-first |
| `sessions.source_api_key_hash` | Records which API key minted a long-lived session |
| `sessions_by_api_key` | Reverse index used to invalidate all sessions derived from a revoked key |

Operational note: `001_initial_schema.cql` is the source of truth for fresh clean boots, but once version 001 has been applied in a live environment, any further schema change for that environment must go into a new numbered migration file. Do not modify an already-applied migration in place.

---

## Database Seeding / Bootstrap

### Problem Statement

**Current Issue**: The database schema exists but contains no user records. When the application starts:
- ✅ Tables are created via the versioned migration system (`sesamefs migrate`)
- ❌ No default organization exists
- ❌ No default admin user exists
- ❌ Permission middleware fails (queries empty `users` table)
- ❌ Dev mode authentication works but creates "ghost users" (no DB records)

**Impact**:
- Permission middleware cannot function (no roles to check)
- No way to manage users through UI/API
- Production deployment has no initial admin account

### Solution: Automatic Seeding on First Run

**Implementation Strategy**:
1. Check if default organization exists
2. If not found, create seed data:
   - Default organization
   - Default admin user with `admin` role
   - Test users for development (dev mode only)
3. Log seeding activity for audit trail

### Required Seed Data

#### 1. Default Organization
```go
org_id:   00000000-0000-0000-0000-000000000001 (fixed UUID for dev)
name:     "Default Organization"
settings: { "theme": "default", "features": "all" }
storage_quota:  1TB (1000000000000 bytes)
storage_used:   0
chunking_polynomial: 17592186044415 (default Rabin polynomial)
created_at:     now()
```

**Why fixed UUID?**
- Dev mode config already uses this org_id
- Existing dev libraries reference this org_id
- Makes dev/test predictable

#### 2. Default Admin User
```go
org_id:    00000000-0000-0000-0000-000000000001
user_id:   00000000-0000-0000-0000-000000000001 (matches dev token)
email:     "admin@sesamefs.local"
name:      "System Administrator"
role:      "admin"  // ← CRITICAL: admin role for full permissions
oidc_sub:  null
quota_bytes:  100GB (100000000000 bytes)
used_bytes:   0
created_at:   now()
```

**Dual Write Required**:
- Insert into `users` table (primary)
- Insert into `users_by_email` table (login lookup)

#### 3. Dev Mode Test Users (Optional, dev mode only)
```go
// Regular user
user_id:   00000000-0000-0000-0000-000000000002
email:     "user@sesamefs.local"
name:      "Test User"
role:      "user"  // Standard user permissions

// Read-only user
user_id:   00000000-0000-0000-0000-000000000003
email:     "readonly@sesamefs.local"
name:      "Read-Only User"
role:      "readonly"  // Can view but not modify

// Guest user
user_id:   00000000-0000-0000-0000-000000000004
email:     "guest@sesamefs.local"
name:      "Guest User"
role:      "guest"  // Limited access
```

### Implementation Files

**New File: `internal/db/seed.go`**
```go
package db

import (
    "log"
    "time"
    "github.com/google/uuid"
)

// SeedDatabase creates default organization and admin user if they don't exist
func (db *DB) SeedDatabase(devMode bool) error {
    // Check if default org exists
    var orgID uuid.UUID
    defaultOrgID := uuid.MustParse("00000000-0000-0000-0000-000000000001")

    err := db.Session().Query(`
        SELECT org_id FROM organizations WHERE org_id = ?
    `, defaultOrgID).Scan(&orgID)

    if err == nil {
        log.Println("✓ Database already seeded, skipping")
        return nil
    }

    log.Println("→ Seeding database with default data...")

    // Create default organization
    if err := db.createDefaultOrganization(); err != nil {
        return err
    }

    // Create default admin user
    if err := db.createDefaultAdmin(); err != nil {
        return err
    }

    // Create test users in dev mode
    if devMode {
        if err := db.createTestUsers(); err != nil {
            return err
        }
    }

    log.Println("✓ Database seeding completed successfully")
    return nil
}

func (db *DB) createDefaultOrganization() error { /* see implementation */ }
func (db *DB) createDefaultAdmin() error { /* see implementation */ }
func (db *DB) createTestUsers() error { /* see implementation */ }
```

**Update: `cmd/sesamefs/main.go`**
```go
// After db.Migrate()
if err := database.SeedDatabase(cfg.Auth.DevMode); err != nil {
    log.Fatalf("Failed to seed database: %v", err)
}
```

### Verification

**Check seeding succeeded**:
```bash
# Verify organization exists
docker exec cool-storage-api-cassandra-1 cqlsh -e \
  "SELECT org_id, name FROM sesamefs.organizations;"

# Verify admin user exists with role
docker exec cool-storage-api-cassandra-1 cqlsh -e \
  "SELECT user_id, email, name, role FROM sesamefs.users \
   WHERE org_id = 00000000-0000-0000-0000-000000000001;"

# Verify email lookup works
docker exec cool-storage-api-cassandra-1 cqlsh -e \
  "SELECT email, user_id, org_id FROM sesamefs.users_by_email \
   WHERE email = 'admin@sesamefs.local';"
```

**Expected output**:
```
org_id                               | name
--------------------------------------+------------------------
00000000-0000-0000-0000-000000000001 | Default Organization

user_id                              | email                  | name                  | role
-------------------------------------+------------------------+-----------------------+-------
00000000-0000-0000-0000-000000000001 | admin@sesamefs.local   | System Administrator  | admin

email                  | user_id                              | org_id
-----------------------+--------------------------------------+--------------------------------------
admin@sesamefs.local   | 00000000-0000-0000-0000-000000000001 | 00000000-0000-0000-0000-000000000001
```

### Production Considerations

**Environment-Specific Behavior**:
- **Dev Mode**: Seeds fixed UUIDs matching dev tokens, creates test users
- **Production**: Seeds random UUIDs, admin only, logs credentials securely

**Security**:
- Production should generate random UUIDs (not fixed)
- Admin password should be randomly generated and logged once
- Consider requiring password change on first login

**Migration from Existing Data**:
- Seeding is idempotent (checks before creating)
- Safe to run multiple times
- Won't overwrite existing organizations/users

---

## Current Tables (see `internal/db/migrations/001_initial_schema.cql`)

### 1. `organizations`
**Purpose:** Multi-tenant organization/company records

**Schema:**
```sql
PRIMARY KEY (org_id)  -- Single partition per org
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `org_id` | UUID | Unique organization identifier |
| `name` | TEXT | Organization display name |
| `settings` | MAP | Key-value settings (theme, features, etc.) |
| `storage_quota` | BIGINT | Max storage in bytes |
| `storage_used` | BIGINT | Current usage in bytes |
| `chunking_polynomial` | BIGINT | Rabin fingerprint polynomial for CDC |
| `storage_config` | MAP | S3 bucket overrides, region preferences |
| `created_at` | TIMESTAMP | Creation time |

**API Usage:**
```
# Not directly exposed - used internally for multi-tenancy
# Every authenticated request extracts org_id from JWT/hostname
```

**Example Flow:**
1. User visits `acme.sesamefs.com`
2. Server looks up `hostname_mappings` → gets `org_id`
3. All subsequent queries filter by this `org_id`

---

### 2. `users`
**Purpose:** User accounts partitioned by organization

**Schema:**
```sql
PRIMARY KEY ((org_id), user_id)  -- Partition by org, cluster by user
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `org_id` | UUID | Parent organization |
| `user_id` | UUID | Unique user identifier |
| `email` | TEXT | Login email |
| `name` | TEXT | Display name |
| `role` | TEXT | `admin`, `user`, `guest` |
| `oidc_sub` | TEXT | OIDC subject claim (for SSO) |
| `quota_bytes` | BIGINT | Personal storage quota |
| `used_bytes` | BIGINT | Current usage |
| `created_at` | TIMESTAMP | Account creation time |

**API Usage:**
```bash
GET /api2/account/info/
# Returns: { "email": "user@acme.com", "usage": 1234567, "total": 10000000000 }
```

**Why partitioned by org_id?**
- Efficient query: "Get all users in organization X"
- Tenant isolation: Can't accidentally query across orgs

---

### 3. `users_by_email`
**Purpose:** Email → user lookup (for login)

**Schema:**
```sql
PRIMARY KEY (email)  -- Lookup by email
```

**API Usage:**
```bash
POST /api2/auth-token/
# Body: { "username": "user@acme.com", "password": "..." }
# 1. Lookup users_by_email → get user_id, org_id
# 2. Fetch full user from users table
# 3. Verify password, return JWT
```

**Consistency Concern:**
When creating a user, must write to BOTH `users` AND `users_by_email` atomically.

> **✅ As of 2026-02-23**, all user creation paths enforce this dual-write:
> OIDC provisioning (`oidc.go`), `AdminCreateUser`, `AdminAddOrgUser`, `CreateOrganization` owner, and seed data.
> Previously, OIDC and `AdminAddOrgUser` were missing the `users_by_email` write, causing
> admin email-based lookups to return 404 for those users.

---

### 4. `users_by_oidc`
**Purpose:** OIDC provider + subject → user lookup (for SSO)

**Schema:**
```sql
PRIMARY KEY ((oidc_issuer), oidc_sub)  -- Partition by issuer
```

**API Usage:**
```bash
# OIDC callback flow:
# 1. User authenticates with Google/Okta
# 2. Server receives issuer="https://accounts.google.com", sub="123456"
# 3. Lookup users_by_oidc → get user_id, org_id
# 4. Issue session token
```

---

### 5. `libraries`
**Purpose:** File libraries/repositories (like Seafile repos)

**Schema:**
```sql
PRIMARY KEY ((org_id), library_id)  -- Partition by org
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `library_id` | UUID | Unique library ID |
| `owner_id` | UUID | Owner user ID |
| `name` | TEXT | Library name ("My Documents") |
| `head_commit_id` | TEXT | Current HEAD commit (like git HEAD) |
| `root_commit_id` | TEXT | Initial commit |
| `encrypted` | BOOLEAN | Client-side encryption enabled |
| `storage_class` | TEXT | `hot-s3-usa`, `cold-glacier`, etc. |
| `size_bytes` | BIGINT | Total size |
| `file_count` | BIGINT | Number of files |

**API Usage:**
```bash
GET /api2/repos/
# Returns: [{ "id": "abc-123", "name": "My Documents", "size": 1234567 }]

POST /api2/repos/
# Body: { "name": "New Library" }
# Creates library + initial empty commit
```

**Critical Operation - File Upload:**
```
1. Client uploads file blocks to S3
2. Server creates new fs_object (file metadata)
3. Server creates new fs_object (updated parent directory)
4. Server creates new commit pointing to new root
5. Server updates library.head_commit_id  ← MUST BE ATOMIC
```

---

### 6. `commits`
**Purpose:** Version history (like git commits)

**Schema:**
```sql
PRIMARY KEY ((library_id), commit_id)  -- Partition by library
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `commit_id` | TEXT | SHA-256 hash of commit content |
| `parent_id` | TEXT | Previous commit (null for root) |
| `root_fs_id` | TEXT | Root directory fs_object ID |
| `creator_id` | UUID | User who made the change |
| `description` | TEXT | Commit message |
| `created_at` | TIMESTAMP | Commit time |

**API Usage:**
```bash
GET /api2/repo/file_revisions/{repo_id}/?p=/document.pdf
# Returns: [
#   { "commit_id": "abc", "description": "Updated document", "ctime": 1704067200 },
#   { "commit_id": "def", "description": "Initial upload", "ctime": 1704000000 }
# ]
```

**How versioning works:**
```
commit_3 (HEAD) → root_fs_id: "dir_v3"
    ↓ parent
commit_2 → root_fs_id: "dir_v2"
    ↓ parent
commit_1 → root_fs_id: "dir_v1"
```

---

### 7. `fs_objects`
**Purpose:** File system objects (files and directories)

**Schema:**
```sql
PRIMARY KEY ((library_id), fs_id)  -- Partition by library
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `fs_id` | TEXT | SHA-256 hash of object content |
| `obj_type` | TEXT | `file` or `dir` |
| `obj_name` | TEXT | Filename or directory name |
| `dir_entries` | TEXT | JSON array of child entries (for dirs) |
| `block_ids` | LIST | List of block hashes (for files) |
| `size_bytes` | BIGINT | File size |
| `mtime` | BIGINT | Modification timestamp |

**Directory Entry Format:**
```json
[
  {"name": "document.pdf", "id": "fs_abc123", "mode": 33188, "mtime": 1704067200, "size": 12345},
  {"name": "images", "id": "fs_def456", "mode": 16384, "mtime": 1704000000}
]
```

**API Usage:**
```bash
GET /api2/repos/{repo_id}/dir/?p=/
# 1. Get library.head_commit_id
# 2. Get commit.root_fs_id
# 3. Get fs_object for root directory
# 4. Parse dir_entries, return file list
```

---

### 8. `blocks`
**Purpose:** Block metadata (actual data in S3)

**Schema:**
```sql
PRIMARY KEY ((org_id, block_id))  -- Per-block partition: each block is its
                                  -- own Paxos partition so concurrent uploads
                                  -- never serialize. Replaces the earlier
                                  -- ((org_id), block_id) form, which made the
                                  -- platform org a single hot partition.
```

Discovery for the GC scanner is provided by `gc_block_candidates_by_day`
(see below), so we no longer rely on a `WHERE org_id = ?` partition scan to
enumerate orphaned blocks.

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `block_id` | TEXT | SHA-256 hash of block content |
| `size_bytes` | INT | Block size |
| `storage_class` | TEXT | Where stored (`hot-s3-usa`) |
| `storage_key` | TEXT | S3 object key |
| `gc_state` | TEXT | `'deleting'` while the GC worker holds a delete claim (else null) |
| `gc_claimed_at` | TIMESTAMP | When the GC claim was taken |
| `last_accessed` | TIMESTAMP | For cold storage tiering |

> `blocks` no longer carries a mutable `ref_count`. Block liveness lives in
> `block_references` (one row per `(block, referrer)`); a block is alive iff a
> reference row exists. See ARCHITECTURE.md "Block Liveness — Row-Per-Reference Model".
>
> **Proposed future design:** the greenfield generation-aware schema, pins,
> materialization state, exact-key recovery, and generation-bound references are
> specified in [GC-X1-X2-GENERATION-FENCE-ADR.md](./GC-X1-X2-GENERATION-FENCE-ADR.md).
> This ADR is not implemented by the current schema described above.

**Deduplication Example:**
```
User A uploads file.pdf (blocks: [abc, def, ghi])
User B uploads same-file.pdf (blocks: [abc, def, ghi])
→ S3: only one copy of each block stored
→ block_references: each block gets one fs:<lib>:<fs_id> row per distinct fs_object
  that contains it (identical content in the same library shares one fs_id → one row)
```

**API Usage (internal):**
```bash
POST /api/v2/blocks/upload
# 1. Hash block content → block_id
# 2. Check if block exists (dedup)
# 3. If new: upload to S3, create blocks record (metadata only)
# 4. Register a reference row (provisional up:… on upload, permanent fs:… on commit)
```

---

### 9. `block_id_mappings`
**Purpose:** SHA-1 → SHA-256 translation for Seafile client compatibility

**Schema:**
```sql
PRIMARY KEY ((org_id, representation_id, external_id))  -- Lookup by SHA-1 inside one representation domain
```

**Why needed?**
- Seafile desktop/mobile clients use SHA-1 hashes (40 chars)
- SesameFS stores blocks with SHA-256 (64 chars)
- This table translates between them

**API Usage (sync protocol):**
```bash
# Seafile client sends:
PUT /seafhttp/repo/{repo_id}/block/{sha1_hash}

# Server:
# 1. Compute SHA-256 of block data
# 2. Store block with SHA-256 key
# 3. Save mapping: external_id=sha1, internal_id=sha256
# 4. Return SHA-1 to client (they don't know about translation)
```

---

### 9b. `block_id_mappings_by_internal` — DROPPED (PR7, migration 006)
> **Removed.** This reverse table was dropped in `006_drop_block_id_mappings_by_internal.cql`. GC
> cleanup now sources a block's external SHA-1 from `blocks.sha1` (a keyed point read, captured from
> `GetBlockInfo` before the row is deleted) and deletes the single forward `block_id_mappings` row by
> `(org_id, representation_id, external_id)`. No reverse enumeration, no dual-write. The description below is retained
> for historical context only.

**Historical purpose:** Reverse lookup (SHA-256 → SHA-1) for GC cleanup before PR7.

**Schema:**
```sql
PRIMARY KEY ((org_id), internal_id, external_id)  -- Lookup by SHA-256
```

**Why it was needed:**
- GC used to delete blocks by SHA-256 and enumerate the matching SHA-1 alias(es) from this table.
- It was dual-written alongside `block_id_mappings` on every upload.
- PR7 removed that need by reading the authoritative SHA-1 from `blocks.sha1` on the canonical block row itself.

**GC Usage:**
```
Worker reads blocks.sha1 from blocks(org_id, block_id) →
    Deletes the single forward row from block_id_mappings(org_id, representation_id, external_id)
```

---

### 10. `share_links` (unified)
**Purpose:** Unified public/password-protected links (share, upload, internal/smart links)

**Schema:**
```sql
PRIMARY KEY (link_token)  -- Lookup by token
-- link_type: 'share' | 'upload' | 'internal'
-- See also: share_links_by_creator, share_links_by_library,
--           admin_links_by_created, admin_links_by_org_created
-- Full schema: docs/SHARE-LINKS-UNIFICATION.md
```

**API Usage:**
```bash
POST /api/v2.1/share-links/
# Body: { "repo_id": "abc", "path": "/document.pdf", "password": "secret" }
# Returns: { "link": "https://files.acme.com/d/abc123/" }

# Public access:
GET /d/abc123/
# 1. Lookup share_links by token
# 2. Check password, expiry, download_count
# 3. Serve file
```

---

### 11. `shares`
**Purpose:** User-to-user library sharing — canonical source of truth

**Schema:**
```sql
PRIMARY KEY ((library_id), share_id)  -- Partition by library
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `library_id` | UUID | Shared library (partition key) |
| `share_id` | UUID | Unique share ID (clustering key) |
| `org_id` | UUID | Organization (denormalized at creation) |
| `shared_by` | UUID | Creator user ID |
| `shared_by_email` | TEXT | Creator email (denormalized at creation) |
| `shared_by_name` | TEXT | Creator display name (denormalized at creation) |
| `shared_to` | UUID | Recipient user ID or group ID |
| `shared_to_type` | TEXT | `"user"` or `"group"` |
| `repo_name` | TEXT | Library name (denormalized at creation) |
| `encrypted` | BOOLEAN | Whether library is encrypted (denormalized) |
| `size_bytes` | BIGINT | Library size at share creation time |
| `permission` | TEXT | `"r"` or `"rw"` |
| `created_at` | TIMESTAMP | Share creation time |
| `expires_at` | TIMESTAMP | Optional expiry |

**Denormalization note:** `org_id`, `repo_name`, `encrypted`, `shared_by_email`, `shared_by_name` are resolved once at creation time and stored here. `ReadShareReadModelRow()` reads all fields from this table in a single query. Rows created before this schema change fall back to multi-table resolution.

**API Usage:**
```bash
# Share library with another user
POST /api2/repos/{repo_id}/
# Body: { "operation": "share", "share_to": "bob@acme.com", "permission": "rw" }

# When Bob lists libraries, includes shared ones:
GET /api2/repos/
# Query: shares WHERE shared_to = bob_user_id
```

**Dual-write targets:** Every write to `shares` must also write to the matching projection tables via `AddUpsertShareReadModelQuery` / `AddDeleteShareReadModelQuery`. See §Admin Read Models below.

---

### 12. `restore_jobs`
**Purpose:** Glacier restore job tracking

**Schema:**
```sql
PRIMARY KEY ((org_id), library_id, job_id)
```

**API Usage:**
```bash
# File is in cold storage (Glacier)
POST /api/v2/repos/{repo_id}/file/restore
# Body: { "path": "/archive/old-data.zip" }
# Returns: { "job_id": "xyz", "status": "pending", "eta_hours": 3 }

# Check status
GET /api/v2/repos/{repo_id}/restore-jobs/{job_id}
# Returns: { "status": "completed", "expires_at": "2024-01-05T00:00:00Z" }
```

---

### 13. `access_tokens`
**Purpose:** Stateless upload/download tokens

**Schema:**
```sql
PRIMARY KEY (token)  -- Direct lookup
```

**API Usage:**
```bash
# Get upload link
GET /api2/repos/{repo_id}/upload-link/?p=/folder/
# Returns: "https://files.acme.com/upload/abc123"

# Token "abc123" stored in access_tokens with:
# - token_type: "upload"
# - repo_id, file_path, user_id
# - Uses Cassandra TTL for auto-expiry (e.g., 1 hour)

# Client uploads to that URL
POST /upload/abc123
# Server validates token, processes upload
```

---

### 14. `hostname_mappings`
**Purpose:** Domain → organization routing

**Schema:**
```sql
PRIMARY KEY (hostname)
```

**API Usage:**
```bash
# Request comes to: files.acme.com
# 1. Lookup hostname_mappings WHERE hostname = 'files.acme.com'
# 2. Get org_id
# 3. All subsequent queries use this org_id
```

---

### 15. `onlyoffice_doc_keys`
**Purpose:** OnlyOffice callback URL mappings

**Schema:**
```sql
PRIMARY KEY (doc_key)
```

**API Usage:**
```bash
# User opens document in OnlyOffice
GET /api/v2.1/repos/{repo_id}/onlyoffice/?p=/document.docx
# 1. Generate unique doc_key
# 2. Store mapping: doc_key → (repo_id, file_path, user_id)
# 3. Return OnlyOffice editor config with callback URL

# OnlyOffice calls back when document saved
POST /api/v2.1/onlyoffice/callback/
# Body: { "key": "doc_key_123", "status": 2, "url": "..." }
# 1. Lookup onlyoffice_doc_keys by key
# 2. Download new content, save to library
```

### 15.b. `onlyoffice_pending_blocks`
**Purpose:** Durable pending cleanup for OnlyOffice save callbacks from the pre-upload stage through successful library-head publication

**Schema:**
```sql
PRIMARY KEY ((org_id), operation_id)
```

Rows also carry a 7-day Cassandra TTL as a safety net. Normal cleanup is expected within minutes via inline reconciliation on later saves and the GC scanner's OnlyOffice reconciliation phase.

**API Usage:**
```bash
# OnlyOffice callback save path records intent before the upload starts
POST /onlyoffice/editor-callback/
# 1. Insert onlyoffice_pending_blocks row before PutBlockData
# 2. Upload the block to storage, then call IncrementOrCreateBlock
# 3. Create commit candidate and persist publish_commit_id before CAS on libraries.head_commit_id
# 4. Clear the row after publish success or immediate rollback

# Later OnlyOffice saves and the GC scanner reconcile stale rows conservatively
# 1. Read onlyoffice_pending_blocks older than the staleness window
# 2. If publish_commit_id is reachable from the current library head, drop the row
# 3. Otherwise decrement the materialized block ref and enqueue zero-ref cleanup
# 4. Limitation: a crash after PutBlockData but before IncrementOrCreateBlock can still leave a physical storage object orphaned because this reconciler does not delete storage objects directly
```

---

### 16. `starred_files`
**Purpose:** User favorites/bookmarks

**Schema:**
```sql
PRIMARY KEY ((user_id), repo_id, path)  -- Partition by user
```

**API Usage:**
```bash
# Star a file
POST /api2/starredfiles/
# Body: { "repo_id": "abc", "p": "/important.pdf" }

# List starred files
GET /api2/starredfiles/
# Query: starred_files WHERE user_id = current_user
```

---

## Tables Defined But Not Yet Created

### 17. `locked_files`
**Purpose:** File locking for collaborative editing

**Schema:**
```sql
PRIMARY KEY ((repo_id), path)
```

**Future API:**
```bash
PUT /api/v2.1/repos/{repo_id}/file/?p=/document.docx
# Body: { "operation": "lock" }
# Prevents others from editing until unlocked
```

---

### 18. `repo_tags`
**Purpose:** Repository-level tag definitions

**Schema:**
```sql
PRIMARY KEY ((repo_id), tag_id)
```

**API Usage:**
```bash
POST /api/v2.1/repos/{repo_id}/repo-tags/
# Body: { "name": "Important", "color": "#FF0000" }
```

---

### 19. `file_tags`
**Purpose:** Associate files with tags

**Schema:**
```sql
PRIMARY KEY ((repo_id), file_path, tag_id)
```

**API Usage:**
```bash
POST /api/v2.1/repos/{repo_id}/file-tags/
# Body: { "file_path": "/document.pdf", "repo_tag_id": 1 }
```

---

### 20. `repo_tag_counters`
**Purpose:** Auto-increment tag IDs per repository

**Schema:**
```sql
PRIMARY KEY (repo_id)
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `repo_id` | UUID | Repository identifier |
| `next_tag_id` | INT | Next available tag ID |

---

### 21. `file_tag_counters`
**Purpose:** Auto-increment file tag IDs per repository (for unique file_tag_id values)

**Schema:**
```sql
PRIMARY KEY (repo_id)
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `repo_id` | UUID | Repository identifier |
| `next_file_tag_id` | INT | Next available file tag ID |

**Implementation Notes:**
- Uses Lightweight Transactions (LWT) for atomic counter initialization
- Each file-tag association gets a unique `file_tag_id` for direct lookup

---

### 22. `file_tags_by_id`
**Purpose:** Lookup table to find file tags by their unique ID (enables DELETE by file_tag_id)

**Schema:**
```sql
PRIMARY KEY ((repo_id), file_tag_id)
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `repo_id` | UUID | Repository identifier |
| `file_tag_id` | INT | Unique identifier for this file-tag association |
| `file_path` | TEXT | Path to the tagged file |
| `tag_id` | INT | Reference to repo_tags.tag_id |
| `created_at` | TIMESTAMP | When the tag was added |

**API Usage:**
```bash
# Delete a file tag by its unique ID
DELETE /api/v2.1/repos/{repo_id}/file-tags/{file_tag_id}/

# The handler looks up the file_tag_id → gets (file_path, tag_id) → deletes from both tables
```

**Consistency Pattern:**
When adding a file tag:
1. Generate unique `file_tag_id` via counter
2. Write to `file_tags` table (for efficient file-based queries)
3. Write to `file_tags_by_id` table (for efficient ID-based deletion)

---

### 23. `groups_by_id`
**Purpose:** Lookup table for fast group metadata resolution by group_id (avoids ALLOW FILTERING on `groups` table)

**Schema:**
```sql
PRIMARY KEY (group_id)
```

**Fields:**
| Field | Type | Description |
|-------|------|-------------|
| `group_id` | UUID | Group identifier (partition key) |
| `org_id` | UUID | Organization this group belongs to |
| `name` | TEXT | Group display name |

**API Usage:**
```bash
# Admin endpoints resolve group org_id before authorization checks:
GET /admin/groups/:group_id/members/
POST /admin/groups/:group_id/members/
DELETE /admin/groups/:group_id/members/:email/
GET /admin/groups/:group_id/libraries/
PUT /admin/groups/:group_id/members/:email/
```

**Consistency Pattern:**
Dual-write with `groups` table — every INSERT/UPDATE/DELETE on `groups` must also write to `groups_by_id`.

---

### 24–31. Admin Read Model Tables

These tables are **denormalized projections** maintained by dual-write. They are never the source of truth — always derive from `libraries`, `groups`, `shares`, `share_links`.

**Projection contract:**
- Canonical tables are authoritative. If a projection row disagrees with the canonical row, repair the projection, not the source row.
- Default pattern: write canonical row plus all affected projections in the same `LoggedBatch`.
- Cassandra exception: conditional writes cannot span multiple tables. For sync `HEAD` updates, the canonical `libraries` row advances first via CAS, then `libraries_by_id` plus the admin library projection are resynced immediately from canonical state.
- COUNTER tables are best-effort accelerators, not source of truth. `admin_link_counts_by_org` may be invalidated and rebuilt by recount.

**Current launch caveats:**
- `size_bytes` / `file_count` for libraries are derived values. They are recomputed asynchronously after sync `HEAD` updates, so admin views can observe brief staleness.
- Admin library and group list endpoints now avoid earlier table-scan query shapes, but they still materialize full projection result sets and paginate in Go. That is acceptable for initial scale, not for very large cardinality.
- New projection work should preserve immutable primary keys where possible. Mutable sort fields such as `updated_at` should stay as regular columns unless there is a strong reason to pay the delete+reinsert cost.

**Implementation files:**
- `internal/db/admin_library_read_models.go`
- `internal/db/admin_group_read_models.go`
- `internal/db/admin_link_read_models.go`
- `internal/db/share_read_models.go`

---

#### `libraries_by_owner`
**Purpose:** Per-owner library listing for admin "filter by owner" view

```sql
PRIMARY KEY ((org_id, owner_id), library_id)
CLUSTERING ORDER BY (library_id ASC)
```

`updated_at` remains a regular column. Rows are sorted in Go after read, so the projection no longer needs delete+reinsert churn on every mutable update.

---

#### `libraries_by_org_updated`
**Purpose:** Org-wide library listing ordered by last update — default admin view

```sql
PRIMARY KEY ((org_id), library_id)
CLUSTERING ORDER BY (library_id ASC)
-- Denormalized: owner_email, owner_name
```

---

#### `libraries_admin_global_by_updated`
**Purpose:** Superadmin global library listing across all orgs, bucketed by day

```sql
PRIMARY KEY ((bucket_day), org_id, library_id)
CLUSTERING ORDER BY (org_id ASC, library_id ASC)
-- bucket_day = created_at truncated to YYYY-MM-DD (UTC)
```

Use `library_admin_global_buckets` to enumerate active `bucket_day` values before querying.

---

#### `libraries_deleted_by_org`
**Purpose:** Soft-deleted libraries per org (trash view)

```sql
PRIMARY KEY ((org_id), deleted_at, library_id)
CLUSTERING ORDER BY (deleted_at DESC, library_id ASC)
```

---

#### `library_admin_global_buckets`
**Purpose:** Index of active day-buckets for global library queries

```sql
PRIMARY KEY (bucket_day)
```

One row per creation day that has at least one library. Scanned by `ListAdminGlobalLibraryRows` before iterating bucket partitions.

---

#### `groups_admin_global_by_created`
**Purpose:** Superadmin global group listing, bucketed by creation day

```sql
PRIMARY KEY ((bucket_day), created_at, org_id, group_id)
CLUSTERING ORDER BY (created_at DESC, org_id ASC, group_id ASC)
-- Denormalized: owner_email, owner_name, parent_group_id, is_department
```

`parent_group_id` is always included in the INSERT (passed as `nil` when group is at root level) to ensure upserts correctly clear the column when a group moves from child to root.

---

#### `group_admin_global_buckets`
**Purpose:** Index of active day-buckets for global group queries

```sql
PRIMARY KEY (bucket_day)
```

---

#### `admin_links_by_created`
**Purpose:** Superadmin global share/upload link listing

```sql
PRIMARY KEY ((link_type, bucket_day), created_at, org_id, link_token)
-- link_type: 'share' | 'upload'
-- TTL set from expires_at when the link has an expiry
```

---

#### `admin_links_by_org_created`
**Purpose:** Org-scoped share/upload link listing

```sql
PRIMARY KEY ((org_id, link_type, bucket_day), created_at, link_token)
```

---

#### `admin_link_buckets` / `admin_link_buckets_by_org`
**Purpose:** Bucket indexes for link queries

```sql
admin_link_buckets:        PRIMARY KEY (link_type, bucket_day)
admin_link_buckets_by_org: PRIMARY KEY ((org_id, link_type), bucket_day)
```

---

#### `admin_link_counts_by_org`
**Purpose:** COUNTER — cached active link count per org and link type, used for enforcement limits

```sql
PRIMARY KEY (org_id, link_type)
-- link_count COUNTER
```

Incremented/decremented atomically via `AdjustAdminOrgLinkCount`. On counter miss or negative value, `CountAdminOrgLinks` performs a full recount by iterating bucket partitions.

---

#### `shares_by_group`
**Purpose:** Group shares — admin group detail view; group-deletion cleanup

```sql
PRIMARY KEY ((org_id, group_id), created_at, library_id, share_id)
CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC)
-- Denormalized: shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
```

Used by `collectGroupShareReadModelRows` and `gc.ListSharesByGroup` (both look up `org_id` from `groups_by_id` first).

---

#### `shares_by_user_org`
**Purpose:** User shares — org admin "user's received shares" view

```sql
PRIMARY KEY ((org_id, user_id), created_at, library_id, share_id)
CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC)
-- Denormalized: shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
```

Queried by `GET /org/admin/users/:email/beshared-repos/`.

---

#### `shares_by_creator`
**Purpose:** Shares created by a specific user

```sql
PRIMARY KEY ((org_id, shared_by), created_at, library_id, share_id)
CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC)
```

---

#### `shares_by_recipient`
**Purpose:** All shares received by an entity (user or group), unified view

```sql
PRIMARY KEY ((org_id, shared_to_type, shared_to), created_at, library_id, share_id)
CLUSTERING ORDER BY (created_at DESC, library_id ASC, share_id ASC)
```

Covers both user and group recipients. Complement to `shares_by_user_org` (which is user-only but has no `shared_to_type` in the key, making it slightly more efficient for user-only queries).

---

## Consistency Challenges & Solutions

### Problem Areas

#### 1. Multi-Table Writes (No ACID)
**Scenario:** Creating a user requires writing to 3 tables:
- `users`
- `users_by_email`
- `users_by_oidc` (if SSO)

**Risk:** Partial write leaves orphan records.

#### 2. Counter Updates (Race Conditions)
**Scenario:** Two users upload same file simultaneously:
- Both read `blocks.ref_count = 5`
- Both write `ref_count = 6`
- Actual should be `7`

**Status: ✅ RESOLVED — SUPERSEDED (2026-05-27).** The shared mutable counter was
removed entirely. References are modeled as rows in `block_references`
(`INSERT`/`DELETE` per `(block, referrer)`), so this whole "two writers race on one
integer" scenario no longer exists — there is nothing to read-modify-write. See
`ARCHITECTURE.md` "Block Liveness — Row-Per-Reference Model". (Historical: from
2026-04-10 to 2026-05-27 this was mitigated with LWT/CAS retry loops on `ref_count`.)

#### 3. Commit Chain Integrity
**Scenario:** Creating a commit requires:
1. Write `fs_objects` (new file)
2. Write `fs_objects` (updated directory)
3. Write `commits` (new commit)
4. Update `libraries.head_commit_id`

**Risk:** If step 4 fails, orphan commit exists but library points to old commit.

---

## Improvement Plan: Stronger Consistency

### Phase 1: Use Lightweight Transactions (LWT)

Cassandra supports conditional writes using `IF` clauses:

```sql
-- Atomic user creation with uniqueness check
INSERT INTO users_by_email (email, user_id, org_id)
VALUES ('user@acme.com', uuid, org_uuid)
IF NOT EXISTS;

-- Only update head if it hasn't changed (optimistic locking)
UPDATE libraries
SET head_commit_id = 'new_commit'
WHERE org_id = ? AND library_id = ?
IF head_commit_id = 'expected_old_commit';
```

**Implementation:** `internal/db/transactions.go`

### Phase 2: Use Cassandra Batches

Logged batches provide atomicity within a single partition:

```sql
BEGIN BATCH
  INSERT INTO fs_objects (library_id, fs_id, ...) VALUES (...);
  INSERT INTO commits (library_id, commit_id, ...) VALUES (...);
APPLY BATCH;
```

**Limitation:** Only works for same partition key.

### Phase 3: Implement Saga Pattern

For cross-partition operations, use compensating transactions:

```go
// CreateUser saga
func CreateUser(user User) error {
    // Step 1: Write to users
    if err := writeUsers(user); err != nil {
        return err
    }

    // Step 2: Write to users_by_email
    if err := writeUsersByEmail(user); err != nil {
        // Compensate: delete from users
        deleteUsers(user.ID)
        return err
    }

    // Step 3: Write to users_by_oidc
    if err := writeUsersByOIDC(user); err != nil {
        // Compensate: delete from users and users_by_email
        deleteUsersByEmail(user.Email)
        deleteUsers(user.ID)
        return err
    }

    return nil
}
```

### Phase 4: ~~Use Counters Properly~~ — SUPERSEDED

~~Replace `ref_count INT` with Cassandra counter columns.~~

**Decision (2026-04-10):** Counter columns are **not compatible with LWT (IF clauses)**. Since GC's two-phase delete requires `UPDATE SET ref_count = -999 IF ref_count <= 0`, and uploads require `UPDATE SET ref_count = ? IF ref_count = ?` for CAS, we must keep `ref_count` as a regular INT column with explicit SELECT→UPDATE→LWT cycles. This is now implemented with retry loops in `IncrementOrCreateBlock` and `decrementBlockRefCount`.

Cassandra counters (`ref_count COUNTER`) would provide atomic `ref_count = ref_count + 1` without read-before-write, but:
- Cannot use `IF` conditions (LWT) with counter tables
- Cannot set sentinel values for GC coordination
- Cannot conditionally gate deletion on current value
- Race between "increment counter" and "read counter for GC decision" is worse than the current LWT approach

### Phase 5: Consistency Level Configuration

Set appropriate consistency levels per operation:

| Operation | Consistency Level | Why |
|-----------|-------------------|-----|
| User login | `LOCAL_QUORUM` | Must be consistent |
| File listing | `LOCAL_ONE` | Can be slightly stale |
| Commit creation | `QUORUM` | Must be durable |
| Block reference add/remove (`block_references`) | `LOCAL_QUORUM` | Idempotent INSERT/DELETE — no cross-DC Paxos in steady state |
| Block metadata first-writer (`INSERT ... IF NOT EXISTS`) | `SERIAL` (production default) | Pins one canonical storage class/key per `(org_id, block_id)`; one global Paxos round per metadata-registering uploaded block |
| Block identity repair (`representation_id` / `sha1` backfill) | `SERIAL` (production default) | Conditional repair of pre-existing metadata; not taken by the successful first-writer hot path |
| GC candidate lifecycle (`INSERT IF NOT EXISTS`, conditional replacement) | `SERIAL` (production default) | Preserves the canonical candidate timestamp under concurrent enqueue/replacement |
| GC block lifecycle (`gc_state` claim/release/finalize and conditional orphan transitions) | `SERIAL` (production default) | Guards ownership and irreversible delete transitions; do NOT change production to `LOCAL_SERIAL` |
| Block upload (non-LWT reads) | `LOCAL_QUORUM` | Reads must see latest state |
| Share link validation | `LOCAL_QUORUM` | Security-critical |

The dedicated `config-usa.cluster.yaml` and `config-eu.cluster.yaml` profiles are
test/development harnesses and intentionally use `LOCAL_SERIAL`; they are not the
production configuration. They therefore do not reproduce the production cross-DC
`SERIAL` contract and cannot validate global first-writer serialization.

**Implementation in config.yaml:**
```yaml
database:
  consistency:
    default: LOCAL_QUORUM
    reads: LOCAL_ONE
    writes: LOCAL_QUORUM
    critical: QUORUM
```

---

## Action Items

### Completed
- [x] Create `repo_tags` table (repo-level tag definitions)
- [x] Create `file_tags` table (file-tag associations by path)
- [x] Create `repo_tag_counters` table (auto-increment repo tag IDs)
- [x] Create `locked_files` table (file locking)
- [x] Create `file_tag_counters` table (auto-increment file tag IDs)
- [x] Create `file_tags_by_id` table (lookup file tags by unique ID)
- [x] Implement LWT for tag counter initialization

### Pending
- [ ] Implement LWT for user creation
- [ ] Implement LWT for `head_commit_id` updates (optimistic locking)
- [x] ~~Convert `blocks.ref_count` to counter table~~ — SUPERSEDED: LWT (IF clauses) incompatible with counter columns. Solved with SELECT→CAS retry loops instead (2026-04-10)
- [x] Retire decrement idempotency markers in favor of row-per-reference liveness — DONE: active GC no longer uses `gc_processed_items`; block liveness is derived from `block_references` plus delete-fence verification (2026-05-30)

---

## Audit Log Table

```sql
CREATE TABLE IF NOT EXISTS audit_log (
    org_id UUID,
    timestamp TIMESTAMP,
    action TEXT,         -- e.g. "delete_group", "gc_library_artifacts_cleaned", "delete_department"
    target_type TEXT,    -- e.g. "group", "library", "department"
    target_id TEXT,
    actor_id TEXT,       -- user_id or "gc_worker"/"gc_scanner"
    details TEXT,        -- JSON with extra context
    PRIMARY KEY ((org_id), timestamp, action, target_id)
) WITH CLUSTERING ORDER BY (timestamp DESC, action ASC, target_id ASC)
  AND default_time_to_live = 31536000  -- 365 days
```

**Purpose**: Tracks deletion events for compliance and traceability. Written by GC worker (library cleanup), group/department deletion handlers, and scanner phases.

**Query patterns**:
- Recent events for an org: `SELECT * FROM audit_log WHERE org_id = ? LIMIT 100`
- Events by action: `SELECT * FROM audit_log WHERE org_id = ? AND timestamp > ? AND action = ? ALLOW FILTERING`

**Scope note**: `audit_log` is not a general-purpose login/activity history store. It currently covers deletion/compliance events only.

### Next Logical Audit Table: `login_logs`

Now that `users.last_login_at` is persisted as the latest successful login timestamp, the next logical audit/reporting step is a dedicated immutable `login_logs` table.

Suggested first version:

```sql
CREATE TABLE IF NOT EXISTS login_logs (
        org_id UUID,
        log_id TIMEUUID,
        user_id UUID,
        email TEXT,
        name TEXT,
        auth_source TEXT,
        login_ip TEXT,
        user_agent TEXT,
        created_at TIMESTAMP,
        PRIMARY KEY ((org_id), log_id)
) WITH CLUSTERING ORDER BY (log_id DESC)
    AND default_time_to_live = 7776000;
```

**Purpose**: Historical login audit trail and period reporting. Complements `users.last_login_at` instead of replacing it.

**Query patterns**:
- Recent login events for an org: `SELECT * FROM login_logs WHERE org_id = ? LIMIT 100`
- Login history for a time window: `SELECT * FROM login_logs WHERE org_id = ? AND log_id > minTimeuuid(?) AND log_id < maxTimeuuid(?)`

**Design note**: Start with successful login events only. Failed-login tracking can be added later if compliance requirements justify the extra write volume and schema fields.

---

## References

- [Cassandra Lightweight Transactions](https://docs.datastax.com/en/cql-oss/3.x/cql/cql_using/useInsertLWT.html)
- [Cassandra Batches](https://docs.datastax.com/en/cql-oss/3.x/cql/cql_reference/cqlBatch.html)
- [Cassandra Counters](https://docs.datastax.com/en/cql-oss/3.x/cql/cql_using/useCounters.html)
- [Saga Pattern](https://microservices.io/patterns/data/saga.html)
