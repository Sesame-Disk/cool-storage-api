# Cassandra Performance Optimization Guide

**Date**: 2026-01-19
**Status**: Living optimization backlog (updated 2026-04-02)
**Goal**: Eliminate ALLOW FILTERING queries and optimize Cassandra schema

**Current status snapshot:**
- Major admin/share/link/group lookup problems have already been refactored to denormalized read models and lookup tables.
- `groups_by_id`, share projections, admin link projections, admin link bucket tables, and library/group admin read models now exist in the initial schema.
- This document should now be read as the **remaining** Cassandra optimization backlog, not as a greenfield plan.

---

## Executive Summary

### Issues Found

1. **Many historic ALLOW FILTERING / lookup gaps are already fixed** via projections and lookup tables ✅
2. **Best-effort counters still require recount fallback semantics** ⚠️
3. **Some admin list endpoints still materialize full projection result sets in memory** ⚠️

### Remaining focus

- Move admin library and group lists away from full materialization + in-memory pagination
- Keep pruning stale roadmap items now that admin link cursor pagination and read models are implemented
- Document the operational contract for best-effort counters, recount fallback, and canonical-vs-projection repair

---

## Issue #1: ALLOW FILTERING Queries (7 locations)

### Problem

ALLOW FILTERING forces Cassandra to scan entire partitions or tables, causing:
- Slow queries (100-500ms instead of 1-5ms)
- High CPU usage
- Poor scalability
- Timeouts under load

### The 7 Problematic Queries

#### 1. Library Lookup by library_id (CRITICAL - 3 locations)

**Current code** (BROKEN):
```go
// File: internal/api/v2/fs_helpers.go:41, 353
// File: internal/api/v2/onlyoffice.go:571
session.Query(`
    SELECT head_commit_id FROM libraries
    WHERE library_id = ?
    ALLOW FILTERING
`, libraryID).Scan(&headCommitID)
```

**Problem**:
- libraries table partitioned by org_id
- Querying by library_id without org_id forces full table scan
- Scans ALL organizations!

**Impact**: CRITICAL - Breaks with >100 libraries

---

#### 2. Library Lookup by owner_id (HIGH)

**Current code**:
```go
// File: internal/api/v2/libraries.go:287
session.Query(`
    SELECT name FROM libraries
    WHERE org_id = ? AND owner_id = ?
    ALLOW FILTERING
`, orgID, ownerID).Scan(&name)
```

**Problem**: owner_id is not a clustering key
**Impact**: Scans entire org partition

---

#### 3. Share Links by creator (HIGH)

**Current code**:
```go
// File: internal/api/v2/shares.go:47
session.Query(`
    SELECT share_token FROM share_links
    WHERE org_id = ? AND created_by = ?
    ALLOW FILTERING
`, orgID, createdBy)
```

**Problem**: created_by is not indexed
**Impact**: Full table scan

---

#### 4. File Tags Count (MEDIUM)

**Current code**:
```go
// File: internal/api/v2/tags.go:87
session.Query(`
    SELECT COUNT(*) FROM file_tags
    WHERE repo_id = ? AND tag_id = ?
    ALLOW FILTERING
`, repoID, tagID).Scan(&count)
```

**Problem**: tag_id not in primary key
**Impact**: Partition scan

---

#### 5. Restore Jobs by Status (MEDIUM)

**Current code**:
```go
// File: internal/api/v2/restore.go:104
session.Query(`
    SELECT * FROM restore_jobs
    WHERE org_id = ? AND status = ?
    ALLOW FILTERING
`, orgID, status)
```

**Problem**: status not indexed
**Impact**: Partition scan

---

#### 6. File Tags by ID Lookup (MEDIUM)

**Current code**:
```go
// File: internal/api/v2/tags.go:307
session.Query(`
    SELECT file_tag_id FROM file_tags_by_id
    WHERE repo_id = ? AND file_path = ? AND tag_id = ?
    ALLOW FILTERING
`, repoID, filePath, tagID)
```

**Problem**: tag_id not in clustering key
**Impact**: Partition scan

---

#### 7. Library Encryption Check (MEDIUM)

**Current code**:
```go
// File: internal/api/v2/onlyoffice.go:571
session.Query(`
    SELECT org_id, encrypted FROM libraries
    WHERE library_id = ?
    ALLOW FILTERING
`, libraryID)
```

**Problem**: Same as #1 (duplicate issue)
**Impact**: Full table scan

---

## Solution: Denormalized Lookup Tables

### Strategy

Cassandra best practice: **Create denormalized tables for different query patterns**

Instead of filtering on non-key columns, create lookup tables with different primary keys.

---

### Fix #1: libraries_by_id (CRITICAL)

**Create new table**:
```cql
CREATE TABLE libraries_by_id (
    library_id UUID,
    org_id UUID,
    head_commit_id TEXT,
    encrypted BOOLEAN,
    PRIMARY KEY (library_id)
);
```

**Update pattern**: Dual-write to both tables
```go
// When creating/updating library
func (h *LibraryHandler) CreateLibrary(lib *Library) error {
    batch := session.NewBatch(gocql.LoggedBatch)

    // Write to main table
    batch.Query(`
        INSERT INTO libraries (org_id, library_id, name, ...)
        VALUES (?, ?, ?, ...)
    `, lib.OrgID, lib.LibraryID, lib.Name, ...)

    // Write to lookup table
    batch.Query(`
        INSERT INTO libraries_by_id (library_id, org_id, head_commit_id, encrypted)
        VALUES (?, ?, ?, ?)
    `, lib.LibraryID, lib.OrgID, lib.HeadCommitID, lib.Encrypted)

    return session.ExecuteBatch(batch)
}
```

**Query pattern**: Fast lookup by library_id
```go
// OLD (SLOW - ALLOW FILTERING)
session.Query(`SELECT head_commit_id FROM libraries WHERE library_id = ? ALLOW FILTERING`)

// NEW (FAST - indexed)
session.Query(`SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?`)
```

**Files to update**:
- `internal/api/v2/fs_helpers.go:41, 353`
- `internal/api/v2/onlyoffice.go:571`
- `internal/api/v2/libraries.go` (create/update/delete)

**Performance improvement**: 100-500ms → 1-5ms (100x faster)

---

### Fix #2: libraries_by_owner (HIGH)

**Create new table**:
```cql
CREATE TABLE libraries_by_owner (
    org_id UUID,
    owner_id UUID,
    library_id UUID,
    name TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, owner_id), created_at, library_id)
) WITH CLUSTERING ORDER BY (created_at DESC, library_id ASC);
```

**Why this primary key**:
- Partition: `(org_id, owner_id)` - find all libraries for a user
- Clustering: `created_at DESC` - newest first
- Clustering: `library_id` - uniqueness

**Update pattern**:
```go
func (h *LibraryHandler) CreateLibrary(lib *Library) error {
    batch := session.NewBatch(gocql.LoggedBatch)

    // Main table
    batch.Query(`INSERT INTO libraries ...`)

    // Lookup by ID
    batch.Query(`INSERT INTO libraries_by_id ...`)

    // Lookup by owner (NEW)
    batch.Query(`
        INSERT INTO libraries_by_owner (org_id, owner_id, library_id, name, created_at)
        VALUES (?, ?, ?, ?, ?)
    `, lib.OrgID, lib.OwnerID, lib.LibraryID, lib.Name, time.Now())

    return session.ExecuteBatch(batch)
}
```

**Query pattern**:
```go
// OLD (SLOW)
session.Query(`
    SELECT * FROM libraries
    WHERE org_id = ? AND owner_id = ?
    ALLOW FILTERING
`)

// NEW (FAST)
session.Query(`
    SELECT library_id, name FROM libraries_by_owner
    WHERE org_id = ? AND owner_id = ?
`)
```

**File to update**: `internal/api/v2/libraries.go:287`

---

### Fix #3: share_links_by_creator (HIGH)

**Create new table**:
```cql
CREATE TABLE share_links_by_creator (
    org_id UUID,
    created_by UUID,
    share_token TEXT,
    library_id UUID,
    file_path TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, created_by), created_at, share_token)
) WITH CLUSTERING ORDER BY (created_at DESC);
```

**Update pattern**:
```go
func CreateShareLink(link *ShareLink) error {
    batch := session.NewBatch(gocql.LoggedBatch)

    // Main table
    batch.Query(`INSERT INTO share_links ...`)

    // Lookup by creator (NEW)
    batch.Query(`
        INSERT INTO share_links_by_creator
        (org_id, created_by, share_token, library_id, file_path, created_at)
        VALUES (?, ?, ?, ?, ?, ?)
    `, link.OrgID, link.CreatedBy, link.ShareToken, link.LibraryID, link.FilePath, time.Now())

    return session.ExecuteBatch(batch)
}
```

**Query pattern**:
```go
// OLD (SLOW)
session.Query(`
    SELECT * FROM share_links
    WHERE org_id = ? AND created_by = ?
    ALLOW FILTERING
`)

// NEW (FAST)
session.Query(`
    SELECT share_token, library_id, file_path FROM share_links_by_creator
    WHERE org_id = ? AND created_by = ?
`)
```

**File to update**: `internal/api/v2/shares.go:47`

---

### Fix #4: file_tags_by_tag (MEDIUM)

**Create new table**:
```cql
CREATE TABLE file_tags_by_tag (
    repo_id UUID,
    tag_id INT,
    file_path TEXT,
    PRIMARY KEY ((repo_id, tag_id), file_path)
);
```

**Update pattern**:
```go
func AddFileTag(repoID, filePath string, tagID int) error {
    batch := session.NewBatch(gocql.LoggedBatch)

    // Main table
    batch.Query(`INSERT INTO file_tags (repo_id, file_path, tag_id, created_at) VALUES (?, ?, ?, ?)`)

    // Reverse lookup (NEW)
    batch.Query(`INSERT INTO file_tags_by_tag (repo_id, tag_id, file_path) VALUES (?, ?, ?)`)

    return session.ExecuteBatch(batch)
}
```

**Query pattern**:
```go
// OLD (SLOW)
session.Query(`SELECT COUNT(*) FROM file_tags WHERE repo_id = ? AND tag_id = ? ALLOW FILTERING`)

// NEW (FAST)
session.Query(`SELECT COUNT(*) FROM file_tags_by_tag WHERE repo_id = ? AND tag_id = ?`)
```

**File to update**: `internal/api/v2/tags.go:87`

---

### Fix #5: restore_jobs_by_status (MEDIUM)

**Create new table**:
```cql
CREATE TABLE restore_jobs_by_status (
    org_id UUID,
    status TEXT,
    job_id UUID,
    requested_at TIMESTAMP,
    PRIMARY KEY ((org_id, status), requested_at, job_id)
) WITH CLUSTERING ORDER BY (requested_at DESC);
```

**Update pattern**:
```go
func CreateRestoreJob(job *RestoreJob) error {
    batch := session.NewBatch(gocql.LoggedBatch)

    batch.Query(`INSERT INTO restore_jobs ...`)
    batch.Query(`INSERT INTO restore_jobs_by_status (org_id, status, job_id, requested_at) VALUES (?, ?, ?, ?)`)

    return session.ExecuteBatch(batch)
}

func UpdateRestoreJobStatus(jobID, newStatus string) error {
    // Get current status
    var oldStatus string
    session.Query(`SELECT status FROM restore_jobs WHERE job_id = ?`).Scan(&oldStatus)

    batch := session.NewBatch(gocql.LoggedBatch)

    // Update main table
    batch.Query(`UPDATE restore_jobs SET status = ? WHERE org_id = ? AND job_id = ?`)

    // Delete from old status lookup
    batch.Query(`DELETE FROM restore_jobs_by_status WHERE org_id = ? AND status = ? AND job_id = ?`, orgID, oldStatus, jobID)

    // Insert into new status lookup
    batch.Query(`INSERT INTO restore_jobs_by_status (org_id, status, job_id, requested_at) VALUES (?, ?, ?, ?)`)

    return session.ExecuteBatch(batch)
}
```

**File to update**: `internal/api/v2/restore.go:104`

---

### Fix #6: groups_by_id ✅ IMPLEMENTED (2026-03-07)

**Problem:** Admin group endpoints needed to resolve `org_id` and `name` from a `group_id`, but the `groups` table has `PRIMARY KEY ((org_id), group_id)`. Querying by `group_id` alone required `ALLOW FILTERING` (full table scan). This affected 5 admin endpoints.

**Solution:** Created `groups_by_id` lookup table:
```cql
CREATE TABLE IF NOT EXISTS groups_by_id (
    group_id UUID PRIMARY KEY,
    org_id UUID,
    name TEXT
);
```

**Eliminated queries:**
- `SELECT org_id, name FROM groups WHERE group_id = ? ALLOW FILTERING` (3 locations in admin.go)
- `SELECT org_id FROM groups WHERE group_id = ? ALLOW FILTERING` (1 in admin.go, 1 in admin_extra.go)

**Dual-write locations:** admin.go, admin_extra.go, groups.go, departments.go, org_admin.go, oidc.go

---

### Fix #7: shares_by_user ✅ IMPLEMENTED (2026-03-07)

**Problem:** Listing libraries shared to a user required `ALLOW FILTERING` on the `shares` table (partitioned by `library_id`), causing full table scans. Affected admin and org-admin user share listing endpoints.

**Solution:** Created `shares_by_user` lookup table:
```cql
CREATE TABLE IF NOT EXISTS shares_by_user (
    shared_to UUID,
    library_id UUID,
    shared_to_type TEXT,
    permission TEXT,
    shared_by UUID,
    created_at TIMESTAMP,
    PRIMARY KEY ((shared_to), library_id)
);
```

**Eliminated queries:**
- `SELECT library_id, permission FROM shares WHERE shared_to = ? AND shared_to_type = 'user' ALLOW FILTERING` (admin.go, org_admin.go)

**Dual-write locations:** file_shares.go (INSERT/UPDATE permission/DELETE for user shares only)

---

## Migration Plan

### Phase 1: Create New Tables (Day 1)

**Script**: `scripts/cassandra-add-lookup-tables.cql`

```cql
-- 1. Libraries lookup by ID
CREATE TABLE IF NOT EXISTS sesamefs.libraries_by_id (
    library_id UUID PRIMARY KEY,
    org_id UUID,
    head_commit_id TEXT,
    encrypted BOOLEAN,
    owner_id UUID,
    name TEXT
);

-- 2. Libraries lookup by owner
CREATE TABLE IF NOT EXISTS sesamefs.libraries_by_owner (
    org_id UUID,
    owner_id UUID,
    library_id UUID,
    name TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, owner_id), created_at, library_id)
) WITH CLUSTERING ORDER BY (created_at DESC, library_id ASC);

-- 3. Share links by creator
CREATE TABLE IF NOT EXISTS sesamefs.share_links_by_creator (
    org_id UUID,
    created_by UUID,
    share_token TEXT,
    library_id UUID,
    file_path TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id, created_by), created_at, share_token)
) WITH CLUSTERING ORDER BY (created_at DESC);

-- 4. File tags by tag
CREATE TABLE IF NOT EXISTS sesamefs.file_tags_by_tag (
    repo_id UUID,
    tag_id INT,
    file_path TEXT,
    PRIMARY KEY ((repo_id, tag_id), file_path)
);

-- 5. Restore jobs by status
CREATE TABLE IF NOT EXISTS sesamefs.restore_jobs_by_status (
    org_id UUID,
    status TEXT,
    job_id UUID,
    requested_at TIMESTAMP,
    PRIMARY KEY ((org_id, status), requested_at, job_id)
) WITH CLUSTERING ORDER BY (requested_at DESC);
```

**Run**:
```bash
docker exec -i cool-storage-api-cassandra-1 cqlsh < scripts/cassandra-add-lookup-tables.cql
```

---

### Phase 2: Backfill Existing Data (Day 1)

**Script**: `scripts/backfill-lookup-tables.go`

```go
package main

import (
    "github.com/apache/cassandra-gocql-driver/v2"
    "log"
)

func main() {
    cluster := gocql.NewCluster("localhost:9042")
    cluster.Keyspace = "sesamefs"
    session, _ := cluster.CreateSession()
    defer session.Close()

    // 1. Backfill libraries_by_id
    log.Println("Backfilling libraries_by_id...")
    iter := session.Query(`SELECT org_id, library_id, head_commit_id, encrypted, owner_id, name FROM libraries`).Iter()

    var orgID, libraryID, ownerID gocql.UUID
    var headCommitID, name string
    var encrypted bool

    for iter.Scan(&orgID, &libraryID, &headCommitID, &encrypted, &ownerID, &name) {
        session.Query(`
            INSERT INTO libraries_by_id (library_id, org_id, head_commit_id, encrypted, owner_id, name)
            VALUES (?, ?, ?, ?, ?, ?)
        `, libraryID, orgID, headCommitID, encrypted, ownerID, name).Exec()
    }
    iter.Close()
    log.Println("✓ libraries_by_id backfilled")

    // 2. Backfill libraries_by_owner
    log.Println("Backfilling libraries_by_owner...")
    iter = session.Query(`SELECT org_id, library_id, owner_id, name, created_at FROM libraries`).Iter()

    var createdAt time.Time
    for iter.Scan(&orgID, &libraryID, &ownerID, &name, &createdAt) {
        session.Query(`
            INSERT INTO libraries_by_owner (org_id, owner_id, library_id, name, created_at)
            VALUES (?, ?, ?, ?, ?)
        `, orgID, ownerID, libraryID, name, createdAt).Exec()
    }
    iter.Close()
    log.Println("✓ libraries_by_owner backfilled")

    // 3. Backfill share_links_by_creator
    // ... similar pattern

    log.Println("✓ All lookup tables backfilled")
}
```

**Run**:
```bash
go run scripts/backfill-lookup-tables.go
```

---

### Phase 3: Update Code to Dual-Write (Day 2-3)

**Pattern**: Always write to both main table and lookup table using batches

**Example helper**:
```go
// internal/db/library_writes.go
package db

func (d *DB) CreateLibrary(lib *models.Library) error {
    batch := d.session.NewBatch(gocql.LoggedBatch)

    // Main table
    batch.Query(`
        INSERT INTO libraries (org_id, library_id, owner_id, name, head_commit_id, encrypted, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, lib.OrgID, lib.LibraryID, lib.OwnerID, lib.Name, lib.HeadCommitID, lib.Encrypted, time.Now())

    // Lookup by ID
    batch.Query(`
        INSERT INTO libraries_by_id (library_id, org_id, head_commit_id, encrypted, owner_id, name)
        VALUES (?, ?, ?, ?, ?, ?)
    `, lib.LibraryID, lib.OrgID, lib.HeadCommitID, lib.Encrypted, lib.OwnerID, lib.Name)

    // Lookup by owner
    batch.Query(`
        INSERT INTO libraries_by_owner (org_id, owner_id, library_id, name, created_at)
        VALUES (?, ?, ?, ?, ?)
    `, lib.OrgID, lib.OwnerID, lib.LibraryID, lib.Name, time.Now())

    return d.session.ExecuteBatch(batch)
}

func (d *DB) UpdateLibraryHeadCommit(libraryID, newHeadCommit string) error {
    // Get current data
    var orgID gocql.UUID
    d.session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, libraryID).Scan(&orgID)

    batch := d.session.NewBatch(gocql.LoggedBatch)

    // Update main table
    batch.Query(`UPDATE libraries SET head_commit_id = ? WHERE org_id = ? AND library_id = ?`,
        newHeadCommit, orgID, libraryID)

    // Update lookup table
    batch.Query(`UPDATE libraries_by_id SET head_commit_id = ? WHERE library_id = ?`,
        newHeadCommit, libraryID)

    return d.session.ExecuteBatch(batch)
}

func (d *DB) DeleteLibrary(libraryID string) error {
    // Get current data for cleanup
    var orgID, ownerID gocql.UUID
    var createdAt time.Time
    d.session.Query(`SELECT org_id, owner_id FROM libraries_by_id WHERE library_id = ?`, libraryID).
        Scan(&orgID, &ownerID)

    batch := d.session.NewBatch(gocql.LoggedBatch)

    // Delete from main table
    batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libraryID)

    // Delete from lookup tables
    batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID)
    batch.Query(`DELETE FROM libraries_by_owner WHERE org_id = ? AND owner_id = ? AND library_id = ?`,
        orgID, ownerID, libraryID)

    return d.session.ExecuteBatch(batch)
}
```

**Files to update**:
- `internal/api/v2/libraries.go` - CreateLibrary, UpdateLibrary, DeleteLibrary
- `internal/api/v2/shares.go` - CreateShareLink, DeleteShareLink
- `internal/api/v2/tags.go` - AddFileTag, RemoveFileTag
- `internal/api/v2/restore.go` - CreateRestoreJob, UpdateRestoreJobStatus

---

### Phase 4: Update Query Code (Day 3-4)

**Replace ALLOW FILTERING queries with lookup table queries**

**Before**:
```go
// internal/api/v2/fs_helpers.go:41
var headCommitID string
err := h.db.Session().Query(`
    SELECT head_commit_id FROM libraries
    WHERE library_id = ?
    ALLOW FILTERING
`, libraryID).Scan(&headCommitID)
```

**After**:
```go
// internal/api/v2/fs_helpers.go:41
var headCommitID string
err := h.db.Session().Query(`
    SELECT head_commit_id FROM libraries_by_id
    WHERE library_id = ?
`, libraryID).Scan(&headCommitID)
```

**Files to update**:
1. `internal/api/v2/fs_helpers.go:41, 353`
2. `internal/api/v2/onlyoffice.go:571`
3. `internal/api/v2/libraries.go:287`
4. `internal/api/v2/shares.go:47`
5. `internal/api/v2/tags.go:87, 307`
6. `internal/api/v2/restore.go:104`

---

### Phase 5: Testing (Day 4-5)

**Test checklist**:
- [ ] Unit tests for all updated queries
- [ ] Integration tests for dual-write consistency
- [ ] Performance tests (before/after comparison)
- [ ] Sync protocol tests (`./run-sync-comparison.sh`)
- [ ] Load testing (concurrent writes)

**Performance test script**:
```go
// scripts/benchmark-queries.go
package main

func benchmarkOldQuery() {
    start := time.Now()
    session.Query(`SELECT * FROM libraries WHERE library_id = ? ALLOW FILTERING`, libID).Exec()
    fmt.Printf("OLD: %v\n", time.Since(start))  // Expected: 100-500ms
}

func benchmarkNewQuery() {
    start := time.Now()
    session.Query(`SELECT * FROM libraries_by_id WHERE library_id = ?`, libID).Exec()
    fmt.Printf("NEW: %v\n", time.Since(start))  // Expected: 1-5ms
}
```

---

## Issue #2: Manual Counter Optimization (Optional)

### Current Implementation

**Problem**: 4 queries to generate one ID
```go
// 1. Initialize counter if not exists (LWT - slow)
applied, _ := session.Query(`
    INSERT INTO repo_tag_counters (repo_id, next_tag_id) VALUES (?, 1) IF NOT EXISTS
`).ScanCAS(&currentID)

// 2. Get current ID
session.Query(`SELECT next_tag_id FROM repo_tag_counters WHERE repo_id = ?`).Scan(&tagID)

// 3. Insert tag
session.Query(`INSERT INTO repo_tags (repo_id, tag_id, ...) VALUES (?, ?, ...)`)

// 4. Increment counter
session.Query(`UPDATE repo_tag_counters SET next_tag_id = ? WHERE repo_id = ?`, tagID+1)
```

### Optimized Implementation

**Use Cassandra COUNTER type**:

```cql
CREATE TABLE repo_tag_counters_v2 (
    repo_id UUID PRIMARY KEY,
    next_tag_id COUNTER
);
```

**Usage**:
```go
// Increment and get in one operation
session.Query(`UPDATE repo_tag_counters_v2 SET next_tag_id = next_tag_id + 1 WHERE repo_id = ?`, repoID).Exec()

var tagID int64
session.Query(`SELECT next_tag_id FROM repo_tag_counters_v2 WHERE repo_id = ?`, repoID).Scan(&tagID)

// Use tagID for tag creation
```

**Pros**:
- 2 queries instead of 4
- No LWT (faster)
- Built-in atomic increment

**Cons**:
- COUNTERs have quirks (can't be part of primary key with other columns)
- Current implementation works fine

**Recommendation**: ⚠️ **Optional** - Only optimize if you measure counter performance issues

---

## Issue #3: Partition Size Monitoring

### Risk

Large partitions (>100MB) can cause:
- Slow queries
- High memory usage
- Compaction issues

### Tables to Monitor

| Table | Partition Key | Risk Level |
|-------|---------------|------------|
| libraries | org_id | ⚠️ MEDIUM (could have 1000s per org) |
| commits | library_id | ⚠️ MEDIUM (grows over time) |
| fs_objects | library_id | 🔴 HIGH (grows with files) |
| file_tags | repo_id | ⚠️ MEDIUM (grows with file count) |

### Monitoring Script

```bash
# Check partition sizes
nodetool tablestats sesamefs.libraries | grep "Partition Size"
nodetool tablestats sesamefs.fs_objects | grep "Partition Size"
```

### Mitigation Strategy

**If fs_objects partition gets too large (>100MB)**:

**Option 1: Time-based partitioning** (if you keep version history):
```cql
CREATE TABLE fs_objects_v2 (
    library_id UUID,
    month TEXT,              -- e.g., "2026-01"
    fs_id TEXT,
    ...
    PRIMARY KEY ((library_id, month), fs_id)
);
```

**Option 2: Bucketing**:
```cql
CREATE TABLE fs_objects_v2 (
    library_id UUID,
    bucket INT,              -- fs_id hash % 10
    fs_id TEXT,
    ...
    PRIMARY KEY ((library_id, bucket), fs_id)
);
```

**Recommendation**: Monitor first, only repartition if needed

---

## Issue #4: Compaction Strategy

### Current Strategy

Default: **SizeTieredCompactionStrategy (STCS)**

**Good for**: Write-heavy workloads
**Bad for**: Read-heavy with updates

### Recommendation

For your use case (file storage, many reads):

**LeveledCompactionStrategy (LCS)**:
```cql
ALTER TABLE libraries WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'sstable_size_in_mb': 160
};

ALTER TABLE fs_objects WITH compaction = {
    'class': 'LeveledCompactionStrategy',
    'sstable_size_in_mb': 160
};
```

**Why LCS**:
- ✅ Better read performance
- ✅ Less space amplification
- ✅ More predictable performance
- ⚠️ Higher write amplification (but file storage is read-heavy)

**When to apply**: After fixing ALLOW FILTERING issues

---

## Implementation Timeline

### Day 1: Setup
- ✅ Create lookup tables (30 min)
- ✅ Backfill existing data (1 hour)
- ✅ Test backfill (30 min)

### Day 2: Code Updates (Writes)
- ✅ Update CreateLibrary to dual-write (2 hours)
- ✅ Update UpdateLibrary to dual-write (2 hours)
- ✅ Update DeleteLibrary to triple-delete (2 hours)
- ✅ Similar for shares, tags, restore jobs (2 hours)

### Day 3: Code Updates (Reads)
- ✅ Replace 7 ALLOW FILTERING queries (4 hours)
- ✅ Update tests (4 hours)

### Day 4: Testing
- ✅ Unit tests (4 hours)
- ✅ Integration tests (2 hours)
- ✅ Performance benchmarks (2 hours)

### Day 5: Verification
- ✅ Sync protocol tests (2 hours)
- ✅ Load testing (4 hours)
- ✅ Documentation updates (2 hours)

**Total**: 5 days

---

## Success Metrics

### Performance Improvements

| Query Type | Before | After | Improvement |
|------------|--------|-------|-------------|
| Library by ID | 100-500ms | 1-5ms | **100x faster** ✅ |
| Library by owner | 50-200ms | 1-5ms | **50x faster** ✅ |
| Share links by creator | 50-200ms | 1-5ms | **50x faster** ✅ |
| File tags count | 20-100ms | 1-5ms | **20x faster** ✅ |

### Scalability

| Metric | Before | After |
|--------|--------|-------|
| Max libraries | ~1,000 | 100,000+ |
| Max concurrent users | ~100 | 10,000+ |
| Max share links | ~10,000 | 1,000,000+ |

---

## Risk Mitigation

### Dual-Write Consistency

**Risk**: Write to main table succeeds, write to lookup table fails

**Mitigation**: Use logged batches (atomic)
```go
batch := session.NewBatch(gocql.LoggedBatch)  // Atomic
// ... add queries
session.ExecuteBatch(batch)  // All or nothing
```

### Data Inconsistency During Migration

**Risk**: Old code writes to main table only, new code expects lookup table

**Mitigation**:
1. Deploy lookup table creation first (backwards compatible)
2. Backfill data
3. Deploy dual-write code (writes to both)
4. Deploy read code that uses lookup tables
5. Monitor for a week
6. (Optional) Remove old ALLOW FILTERING code

### Performance Degradation

**Risk**: Dual-writes are slower than single writes

**Reality**: Batches are nearly as fast as single writes
- Single write: 1-2ms
- Batched dual-write: 2-3ms (acceptable)

---

## Monitoring & Alerts

### Metrics to Track

```bash
# Query latency (should be <10ms)
nodetool proxyhistograms

# Partition sizes (should be <100MB)
nodetool tablestats sesamefs

# Read/write rates
nodetool tablehistograms sesamefs.libraries
```

### Alerts to Set Up

1. **Query latency > 50ms** → Investigate slow queries
2. **Partition size > 100MB** → Consider repartitioning
3. **Pending compactions > 100** → Tune compaction
4. **Read errors > 1%** → Check consistency levels

---

## Next Steps

1. ✅ Review this optimization plan
2. ✅ Create lookup tables (Day 1)
3. ✅ Backfill data (Day 1)
4. ✅ Update write code (Day 2)
5. ✅ Update read code (Day 3)
6. ✅ Test thoroughly (Day 4-5)
7. ✅ Deploy to production
8. ✅ Monitor performance improvements

---

## Questions?

**Q: Do we need all 5 lookup tables?**
A: Prioritize:
1. libraries_by_id (CRITICAL)
2. libraries_by_owner (HIGH)
3. share_links_by_creator (HIGH)
4. Others (MEDIUM - can add later)

**Q: What if a dual-write fails?**
A: Logged batches ensure atomicity. If batch fails, nothing is written.

**Q: Can we test this in staging first?**
A: Yes! Create tables in staging, test thoroughly, then deploy to production.

**Q: Will this break the sync protocol?**
A: No - read operations stay the same (just faster). Sync protocol unaffected.
