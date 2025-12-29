# Migration Guide: Seafile to SesameFS

This document describes the migration strategy for transitioning from an existing Seafile deployment to SesameFS with minimal downtime.

## Overview

SesameFS can operate alongside Seafile during migration, reading from Seafile's existing S3 storage while gradually migrating blocks to the new storage format. This enables:

- **Zero-downtime shadow testing** before cutover
- **Lazy block migration** (blocks copied on first access)
- **Seafile as fallback** during transition period
- **Gradual user migration** if preferred

## Architecture Comparison

| Component | Seafile | SesameFS |
|-----------|---------|----------|
| Database | MySQL/MariaDB | Apache Cassandra |
| Block Storage | S3 or local filesystem | S3 (multi-region) |
| Block Hash | SHA-1 (40 chars) | SHA-256 (64 chars) internally |
| Block Path | `blocks/{repo_id}/{aa}/{bb}/{block_id}` | `{org_id}/{block_id}` |
| API | `/api2/`, `/seafhttp/` | Same (compatible) |
| Multi-tenancy | Per-database or shared | Native org_id isolation |

## Prerequisites

Before starting migration:

- [ ] SesameFS deployed and tested (see [Getting Started](../README.md#getting-started))
- [ ] Access to Seafile's MySQL database (read-only)
- [ ] Access to Seafile's S3 bucket (read-only for SesameFS)
- [ ] Staging environment with copy of production data
- [ ] Monitoring and alerting configured

## Migration Phases

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        MIGRATION TIMELINE                                │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  Phase 1          Phase 2         Phase 3        Phase 4      Phase 5   │
│  Preparation      Shadow Mode     Read-Through   Cutover      Cleanup   │
│  ───────────      ───────────     ────────────   ───────      ───────   │
│  2-3 weeks        1 week          1 week         5-15 min     2-4 weeks │
│                                                                          │
│  [No downtime]    [No downtime]   [No downtime]  [Brief]     [No down]  │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## Phase 1: Preparation

### 1.1 Storage Architecture

Configure SesameFS to read from both its own storage and Seafile's existing blocks:

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           S3 BUCKET                                      │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  Seafile blocks (existing):                                     │    │
│  │  blocks/{repo_id}/{block_id[0:2]}/{block_id[2:4]}/{block_id}   │    │
│  │                                                                  │    │
│  │  Example: blocks/abc123/de/ad/deadbeef1234567890...             │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────────┐    │
│  │  SesameFS blocks (new writes):                                  │    │
│  │  sesamefs/{org_id}/{block_id}                                   │    │
│  │                                                                  │    │
│  │  Example: sesamefs/org-uuid/sha256hash...                       │    │
│  └─────────────────────────────────────────────────────────────────┘    │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Configuration

Create a migration-specific configuration:

```yaml
# config-migration.yaml
storage:
  default_class: "hot-primary"

  classes:
    hot-primary:
      type: s3
      tier: hot
      endpoint: "https://s3.us-east-1.amazonaws.com"
      bucket: "your-existing-bucket"
      prefix: "sesamefs/"  # New blocks written here
      region: us-east-1

  # Seafile fallback configuration
  seafile_fallback:
    enabled: true
    bucket: "your-existing-bucket"
    region: us-east-1
    # Seafile's block path format
    block_path_template: "blocks/{{.RepoID}}/{{.BlockID | slice 0 2}}/{{.BlockID | slice 2 4}}/{{.BlockID}}"

    # Copy blocks to SesameFS on read (lazy migration)
    migrate_on_read: true

    # Rate limit migration to avoid S3 throttling
    migration_rate_limit: 100  # blocks per second

# Database sync settings
migration:
  seafile_mysql:
    host: "seafile-db.example.com"
    port: 3306
    database: "seafile_db"
    user: "readonly_user"
    # password from SEAFILE_MYSQL_PASSWORD env var

  # Mapping: Seafile repo owners → SesameFS orgs
  org_mapping:
    # Option 1: Single org (simple)
    default_org_id: "00000000-0000-0000-0000-000000000001"

    # Option 2: Map by email domain
    # domain_to_org:
    #   "company-a.com": "org-uuid-a"
    #   "company-b.com": "org-uuid-b"

    # Option 3: Custom mapping table
    # mapping_table: "seafile_org_mapping"
```

### 1.3 Metadata Sync

Create the metadata sync job to copy Seafile's MySQL data to Cassandra:

**Seafile MySQL Tables → SesameFS Cassandra Tables:**

| Seafile Table | SesameFS Table | Notes |
|---------------|----------------|-------|
| `RepoOwner` | `libraries` | Map owner email → org_id |
| `Repo` | `libraries` | Library metadata |
| `Branch` | `libraries.head_commit_id` | HEAD pointer |
| `Commit` | `commits` | Commit history |
| `FsManager` | `fs_objects` | Directory/file structure |
| `BlockInfo` | `blocks` | Block metadata (not content) |
| `SharedRepo` | `shares` | Library shares |
| `FileShare` | `share_links` | Public share links |

**Sync command:**

```bash
# Initial full sync (run once)
./sesamefs migrate sync-metadata \
  --source-mysql="user:pass@tcp(seafile-db:3306)/seafile_db" \
  --dry-run  # Remove to execute

# Verify sync
./sesamefs migrate verify-metadata --table=libraries
./sesamefs migrate verify-metadata --table=commits

# Continuous sync (run until cutover)
./sesamefs migrate sync-metadata --mode=cdc --poll-interval=10s
```

### 1.4 Block ID Mapping

Seafile uses SHA-1 (40 chars), SesameFS stores SHA-256 internally. Create mappings:

```sql
-- block_id_mappings table
-- Populated during migration or on first access

INSERT INTO block_id_mappings (org_id, external_id, internal_id, created_at)
VALUES (
  org-uuid,
  'deadbeef1234...',           -- SHA-1 from Seafile
  'sha256hash...',             -- Computed when block is read
  now()
);
```

---

## Phase 2: Shadow Mode

Run SesameFS alongside Seafile, receiving a copy of all traffic without serving responses:

### 2.1 Load Balancer Configuration

```nginx
# nginx.conf - Shadow mode configuration

upstream seafile_cluster {
    server seafile-1:8000 weight=1;
    server seafile-2:8000 weight=1;
    keepalive 32;
}

upstream sesamefs_cluster {
    server sesamefs-1:8080 weight=1;
    server sesamefs-2:8080 weight=1;
    keepalive 32;
}

server {
    listen 443 ssl http2;
    server_name files.example.com;

    # API endpoints
    location /api2/ {
        # Primary: Seafile serves the response
        proxy_pass http://seafile_cluster;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;

        # Shadow: SesameFS receives copy (response discarded)
        mirror /sesamefs_mirror;
        mirror_request_body on;
    }

    # Sync protocol
    location /seafhttp/ {
        proxy_pass http://seafile_cluster;
        proxy_set_header Host $host;

        mirror /sesamefs_mirror;
        mirror_request_body on;
    }

    # Mirror endpoint (internal)
    location = /sesamefs_mirror {
        internal;
        proxy_pass http://sesamefs_cluster$request_uri;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Shadow-Request "true";
    }
}
```

### 2.2 Monitoring Shadow Traffic

Compare responses between Seafile and SesameFS:

```bash
# Check SesameFS logs for shadow request handling
docker compose logs -f sesamefs | grep "X-Shadow-Request"

# Compare response codes
./sesamefs migrate shadow-report --since=1h

# Example output:
# Endpoint                    Seafile  SesameFS  Match%
# GET /api2/repos/            200      200       100%
# GET /api2/repos/:id/dir/    200      200       100%
# PUT /seafhttp/.../block/    200      200       100%
# POST /seafhttp/.../recv-fs  200      200       99.8%
```

### 2.3 Fix Compatibility Issues

Address any differences found during shadow testing before proceeding.

---

## Phase 3: Read-Through Migration

Enable Seafile fallback so SesameFS can serve traffic while reading from Seafile's blocks:

### 3.1 Enable Fallback Storage

```yaml
# config.yaml
storage:
  seafile_fallback:
    enabled: true
    migrate_on_read: true  # Copy blocks to SesameFS storage
```

### 3.2 Block Read Flow

```
┌─────────────────────────────────────────────────────────────────────────┐
│                        BLOCK READ FLOW                                   │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                          │
│  1. Client requests block                                                │
│     GET /seafhttp/repo/{repo_id}/block/{block_id}                       │
│                                                                          │
│  2. SesameFS checks primary storage                                      │
│     └── Found? → Return block                                            │
│     └── Not found? → Continue to step 3                                  │
│                                                                          │
│  3. SesameFS checks Seafile fallback                                     │
│     Path: blocks/{repo_id}/{block_id[0:2]}/{block_id[2:4]}/{block_id}   │
│     └── Found? → Return block + async migrate                           │
│     └── Not found? → Return 404                                          │
│                                                                          │
│  4. Async migration (background)                                         │
│     - Compute SHA-256 of block data                                      │
│     - Store in SesameFS: sesamefs/{org_id}/{sha256}                     │
│     - Create block_id_mapping: sha1 → sha256                            │
│                                                                          │
└─────────────────────────────────────────────────────────────────────────┘
```

### 3.3 Monitor Migration Progress

```bash
# Check migration progress
./sesamefs migrate status

# Example output:
# Block Migration Status
# ──────────────────────────────────────────────────
# Total blocks (Seafile):     2,450,000
# Migrated to SesameFS:       1,234,567 (50.4%)
# Pending:                    1,215,433 (49.6%)
# Migration rate:             ~15,000 blocks/hour
# Estimated completion:       81 hours
#
# Storage Usage
# ──────────────────────────────────────────────────
# Seafile prefix:             20.0 TB
# SesameFS prefix:            10.1 TB (migrated)
# Deduplication savings:      2.3 TB (blocks shared)
```

### 3.4 Gradual Traffic Shift

Optionally shift traffic gradually before full cutover:

```nginx
# nginx.conf - Gradual traffic shift

upstream seafile_cluster { ... }
upstream sesamefs_cluster { ... }

# Split map based on consistent hashing of user
split_clients "${remote_addr}${http_authorization}" $backend {
    10%   sesamefs_cluster;  # Start with 10%
    *     seafile_cluster;
}

server {
    location /api2/ {
        proxy_pass http://$backend;
    }
}
```

Increase percentage as confidence grows: 10% → 25% → 50% → 100%

---

## Phase 4: Cutover

### 4.1 Pre-Cutover Checklist

```
[ ] All metadata synced and verified
    ./sesamefs migrate verify-metadata --all

[ ] Shadow mode showing 100% compatibility
    ./sesamefs migrate shadow-report --require-match=99.9%

[ ] At least 80% of hot blocks migrated
    ./sesamefs migrate status --check-hot-blocks

[ ] Rollback procedure documented and tested

[ ] Maintenance window scheduled and communicated

[ ] On-call team ready

[ ] Monitoring dashboards prepared
```

### 4.2 Cutover Procedure

**Estimated downtime: 5-15 minutes**

```bash
#!/bin/bash
# cutover.sh - Execute during maintenance window

set -e

echo "=== PHASE 4: CUTOVER ==="
echo "Started at: $(date)"

# Step 1: Announce maintenance
echo "[1/7] Maintenance mode active"
# Your notification system here

# Step 2: Set Seafile to read-only
echo "[2/7] Setting Seafile to read-only..."
ssh seafile-admin "seafile-admin set-readonly"

# Step 3: Final metadata sync
echo "[3/7] Final metadata sync..."
./sesamefs migrate sync-metadata --final --timeout=60s

# Step 4: Verify counts match
echo "[4/7] Verifying metadata..."
./sesamefs migrate verify-metadata --strict

# Step 5: Update load balancer
echo "[5/7] Switching traffic to SesameFS..."
# Option A: Update nginx upstream
ssh nginx-lb "cp /etc/nginx/conf.d/sesamefs-primary.conf /etc/nginx/conf.d/active.conf && nginx -s reload"

# Option B: Update DNS (longer propagation)
# aws route53 change-resource-record-sets --hosted-zone-id XXX --change-batch file://dns-cutover.json

# Option C: Update ALB target group
# aws elbv2 modify-listener --listener-arn XXX --default-actions Type=forward,TargetGroupArn=sesamefs-tg

# Step 6: Verify SesameFS is serving traffic
echo "[6/7] Verifying SesameFS is primary..."
for i in {1..10}; do
    RESPONSE=$(curl -s -o /dev/null -w "%{http_code}" https://files.example.com/api2/ping/)
    if [ "$RESPONSE" == "200" ]; then
        echo "  Health check $i/10: OK"
    else
        echo "  Health check $i/10: FAILED ($RESPONSE)"
        echo "  ROLLING BACK..."
        ./rollback.sh
        exit 1
    fi
    sleep 5
done

# Step 7: Complete
echo "[7/7] Cutover complete!"
echo "Finished at: $(date)"
echo ""
echo "=== POST-CUTOVER MONITORING ==="
echo "Watch dashboard: https://monitoring.example.com/sesamefs"
echo "Logs: docker compose logs -f sesamefs"
```

### 4.3 Post-Cutover Verification

```bash
# Verify API functionality
curl -H "Authorization: Token dev-token" https://files.example.com/api2/repos/

# Verify sync protocol
curl https://files.example.com/seafhttp/protocol-version
# Expected: {"version": 2}

# Check error rates
./sesamefs metrics --endpoint=/api2/ --window=5m

# Monitor block fallback rate (should decrease over time)
./sesamefs metrics --counter=seafile_fallback_reads --window=5m
```

---

## Phase 5: Background Migration & Cleanup

### 5.1 Migrate Remaining Blocks

After cutover, continue migrating cold blocks in background:

```bash
# Start background migration
./sesamefs migrate blocks \
  --source-bucket="your-bucket" \
  --source-prefix="blocks/" \
  --rate-limit=1000 \  # blocks per second
  --workers=10 \
  --run-in-background

# Monitor progress
./sesamefs migrate status --watch

# Estimated time for 20TB:
# - At 100 MB/s: ~55 hours
# - With rate limiting: 1-2 weeks
```

### 5.2 Verify Complete Migration

```bash
# List any blocks still only in Seafile
./sesamefs migrate find-unmigrated --output=unmigrated.txt

# Verify all blocks accessible
./sesamefs migrate verify-blocks --sample=10000

# Compare checksums
./sesamefs migrate verify-checksums --random-sample=1%
```

### 5.3 Decommission Seafile

Once fully migrated and stable (recommend 2-4 weeks post-cutover):

```bash
# 1. Disable Seafile fallback
# config.yaml: seafile_fallback.enabled: false

# 2. Stop Seafile services
ssh seafile-admin "systemctl stop seafile seahub"

# 3. Archive Seafile database
mysqldump seafile_db | gzip > seafile_db_backup_$(date +%Y%m%d).sql.gz

# 4. Delete Seafile S3 prefix (DESTRUCTIVE - ensure backup exists!)
# aws s3 rm s3://your-bucket/blocks/ --recursive --dryrun
# Remove --dryrun when ready

# 5. Update documentation and runbooks
```

---

## Rollback Procedure

If issues arise after cutover:

```bash
#!/bin/bash
# rollback.sh - Emergency rollback to Seafile

echo "=== EMERGENCY ROLLBACK ==="
echo "Started at: $(date)"

# Step 1: Switch traffic back to Seafile
ssh nginx-lb "cp /etc/nginx/conf.d/seafile-primary.conf /etc/nginx/conf.d/active.conf && nginx -s reload"

# Step 2: Disable Seafile read-only mode
ssh seafile-admin "seafile-admin unset-readonly"

# Step 3: Notify team
# Your notification system here

echo "Rollback complete at: $(date)"
echo ""
echo "NOTE: Any writes to SesameFS since cutover need manual recovery."
echo "Run: ./sesamefs migrate export-writes --since='CUTOVER_TIMESTAMP'"
```

### Recovering Writes After Rollback

If users wrote data to SesameFS during the failed cutover:

```bash
# Export commits created in SesameFS since cutover
./sesamefs migrate export-writes \
  --since="2024-01-15T10:00:00Z" \
  --output=sesamefs_writes.json

# Import to Seafile (manual process)
# This requires custom scripting based on your Seafile setup
```

---

## Mapping Reference

### Seafile Repo → SesameFS Library

```
Seafile MySQL                      SesameFS Cassandra
─────────────────────────────────────────────────────────────
Repo.repo_id                   →   libraries.library_id
RepoOwner.owner_id             →   libraries.owner_id (map email → user_id)
Repo.name                      →   libraries.name
Repo.desc                      →   libraries.description
Repo.encrypted                 →   libraries.encrypted
Repo.enc_version               →   libraries.enc_version
Branch.commit_id (master)      →   libraries.head_commit_id
VirtualRepo.origin_repo        →   (not supported, flatten)
```

### Seafile Commit → SesameFS Commit

```
Seafile                            SesameFS
─────────────────────────────────────────────────────────────
Commit.commit_id               →   commits.commit_id
Commit.repo_id                 →   commits.library_id
Commit.parent_id               →   commits.parent_id
Commit.root_id                 →   commits.root_fs_id
Commit.creator_name            →   commits.creator_id (map to user_id)
Commit.desc                    →   commits.description
Commit.ctime                   →   commits.created_at
```

### Seafile Block Path → SesameFS Block Path

```
Seafile S3 Key:
  blocks/{repo_id}/{block_id[0:2]}/{block_id[2:4]}/{block_id}
  Example: blocks/abc123-def456/de/ad/deadbeef1234567890abcdef12345678

SesameFS S3 Key:
  sesamefs/{org_id}/{sha256_block_id}
  Example: sesamefs/org-uuid-here/a1b2c3d4e5f6...
```

---

## Troubleshooting

### Block Not Found After Migration

```bash
# Check if block exists in Seafile
aws s3 ls s3://bucket/blocks/REPO_ID/BL/OC/BLOCK_ID

# Check if mapping exists
./sesamefs query block-mapping --external-id=BLOCK_ID

# Check SesameFS storage
aws s3 ls s3://bucket/sesamefs/ORG_ID/INTERNAL_BLOCK_ID
```

### Metadata Mismatch

```bash
# Compare library counts
./sesamefs migrate compare-counts --table=libraries

# Find missing libraries
./sesamefs migrate find-missing --table=libraries --output=missing.txt

# Re-sync specific library
./sesamefs migrate sync-library --repo-id=abc123
```

### Performance Issues During Migration

```yaml
# Reduce migration rate if S3 throttling
storage:
  seafile_fallback:
    migration_rate_limit: 50  # Reduce from 100

# Or pause migration during peak hours
# ./sesamefs migrate pause
# ./sesamefs migrate resume --after="22:00"
```

---

## Related Documentation

- [Storage Architecture](STORAGE_ARCHITECTURE.md) - Multi-region storage design
- [Multi-Region Testing](MULTIREGION-TESTING.md) - Testing the multi-region setup
- [API Roadmap](API-ROADMAP.md) - Pending API endpoints
- [Seafile Compatibility](SEAFILE_COMPATIBILITY.md) - Protocol compatibility details
