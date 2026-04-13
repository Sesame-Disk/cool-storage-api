# Multi-Region Testing Guide

This document describes how to set up and test the multi-region storage capabilities of SesameFS.

## Architecture

```
                                    ┌─────────────────┐
                                    │     Client      │
                                    └────────┬────────┘
                                             │
                                             ▼
                                    ┌─────────────────┐
                                    │  nginx (8080)   │
                                    │  Load Balancer  │
                                    └────────┬────────┘
                                             │
                     ┌───────────────────────┼───────────────────────┐
                     │                       │                       │
                     ▼                       ▼                       ▼
            ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
            │  us.sesamefs    │    │  eu.sesamefs    │    │    default      │
            │     .local      │    │     .local      │    │  (round-robin)  │
            └────────┬────────┘    └────────┬────────┘    └────────┬────────┘
                     │                       │                       │
                     ▼                       ▼                       ▼
            ┌─────────────────┐    ┌─────────────────┐
            │  sesamefs-usa   │    │  sesamefs-eu    │
            │  Default: USA   │    │  Default: EU    │
            │  Failover: EU   │    │  Failover: USA  │
            └────────┬────────┘    └────────┬────────┘
                     │                       │
                     └───────────┬───────────┘
                                 │
                     ┌───────────┴───────────┐
                     │                       │
                     ▼                       ▼
            ┌─────────────────┐    ┌─────────────────┐
            │  MinIO (9000)   │    │ Cassandra(9042) │
            │  sesamefs-usa   │    │    (shared)     │
            │  sesamefs-eu    │    │                 │
            │  sesamefs-arch  │    │                 │
            └─────────────────┘    └─────────────────┘
```

## Quick Start

### 1. Bootstrap the Environment

**Recommended: Use the unified bootstrap script**

```bash
# Development mode (single instance, minimal resources)
./scripts/bootstrap.sh dev

# Multi-region mode (USA + EU with nginx load balancer)
./scripts/bootstrap.sh multiregion

# Clean start (removes volumes)
./scripts/bootstrap.sh multiregion --clean

# Stop environment
./scripts/bootstrap.sh multiregion --down
```

### Alternative: Manual Start

```bash
# Build and start all services
docker-compose -f docker-compose-multiregion.yaml up -d --build

# Watch logs
docker-compose -f docker-compose-multiregion.yaml logs -f
```

### 2. Verify Services

```bash
# Check all services are healthy
docker-compose -f docker-compose-multiregion.yaml ps

# Test load balancer
curl http://localhost:8080/ping
```

### 3. Run Tests (Recommended: Container-based)

Tests run inside a container, eliminating the need to configure `/etc/hosts`:

```bash
# Run all multi-region tests
./scripts/run-tests.sh multiregion all

# Run specific test
./scripts/run-tests.sh multiregion connectivity
./scripts/run-tests.sh multiregion upload

# Run failover tests (1GB upload, failover scenarios)
./scripts/run-tests.sh failover all
```

### Alternative: Run Tests from Host

If you prefer to run tests directly on your host machine, add to `/etc/hosts`:
```
127.0.0.1 us.sesamefs.local eu.sesamefs.local sesamefs.local
```

Then run:
```bash
./scripts/test-multiregion.sh all
```

## Services

| Service | Port | Description |
|---------|------|-------------|
| nginx | 8080 | Load balancer, routes by hostname |
| sesamefs-usa | (internal) | USA region server, defaults to USA bucket |
| sesamefs-eu | (internal) | EU region server, defaults to EU bucket |
| minio | 9000, 9001 | S3-compatible storage |
| cassandra | 9042 | Shared database |

## Hostname Routing

| Hostname | Routes To | Default Bucket |
|----------|-----------|----------------|
| `us.sesamefs.local` | sesamefs-usa | sesamefs-usa |
| `eu.sesamefs.local` | sesamefs-eu | sesamefs-eu |
| `localhost` | Round-robin | Depends on server |

## Storage Buckets

| Bucket | Purpose | Used By |
|--------|---------|---------|
| `sesamefs-usa` | USA region hot storage | sesamefs-usa (primary), sesamefs-eu (failover) |
| `sesamefs-eu` | EU region hot storage | sesamefs-eu (primary), sesamefs-usa (failover) |
| `sesamefs-archive` | Cold storage | Both servers |

## Quick Test Commands

```bash
# Bootstrap the environment
./scripts/bootstrap.sh dev              # Development (single instance)
./scripts/bootstrap.sh multiregion      # Multi-region (USA + EU)

# Run tests in container (recommended - no /etc/hosts needed)
./scripts/run-tests.sh multiregion all      # Basic tests
./scripts/run-tests.sh failover all         # 1GB upload + failover

# Run tests from host (requires /etc/hosts configuration)
./scripts/test-multiregion.sh connectivity
./scripts/test-multiregion.sh all
./scripts/test-failover.sh all

# Stop environment
./scripts/bootstrap.sh --down

# Clean start (remove data)
./scripts/bootstrap.sh multiregion --clean
```

## Focused Integrity Tests

For the current safe slice, the most important backend checks are the Go integration tests
that verify region-pinned libraries keep reading and writing from the persisted storage
class instead of the request host default.

That safe slice now also includes org-level create-time policy for new libraries:

- `flexible` keeps hostname-first routing for new libraries
- `strict` pins new libraries to the org-approved region
- existing libraries are not moved by policy changes

```bash
# Create-library explicit/default region selection
docker compose run --build --rm -e SESAMEFS_URL=http://sesamefs:8080 \
  gotest go test -tags integration \
  -run TestCreateLibraryStorageSelection -count=1 -v ./internal/integration/...

# Read-path integrity for region-pinned libraries
docker compose run --build --rm -e SESAMEFS_URL=http://sesamefs:8080 \
  gotest go test -tags integration \
  -run 'TestRegionPinnedLibraryReadPaths|TestRegionPinnedHistoricReadPaths|TestRegionPinnedShareLinkRaw' \
  -count=1 -v ./internal/integration/...

# Org-level residency policy enforcement on new-library create flows
docker compose run --build --rm -e SESAMEFS_URL=http://sesamefs:8080 \
  gotest go test -tags integration \
  -run 'TestOrgStoragePolicyStrictAcrossCreateFlows|TestOrgStoragePolicyFlexibleAcrossCreateFlows' \
  -count=1 -v ./internal/integration/...
```

These tests currently cover:

- explicit `storage_id` on create-library
- hostname-derived default region on create-library
- `seafhttp/upload-api` + `seafhttp/files/:token`
- `/repo/:repo_id/raw/*filepath`
- `/repo/:repo_id/history/download`
- `/repo/:repo_id/history/raw`
- `/d/:token?raw=1` share-link serving
- org `storage_policy` enforcement for new libraries in `strict` and `flexible` modes
- personal library create flow
- group-owned library create flow
- org-admin group-owned library create flow
- superadmin create-on-behalf-of-user flow

If backend code changes, rebuild the running service before trusting integration results:

```bash
docker compose up -d --build sesamefs
```

Current limitations of this slice:

- no migration workflow for existing non-empty libraries
- no create-time primary placement into cold classes
- no policy-driven relocation of libraries created before a policy change

## Available Test Scripts

| Script | Purpose |
|--------|---------|
| `bootstrap.sh` | **Recommended** - Unified bootstrap for dev and multi-region modes |
| `run-tests.sh` | Runs tests in container (no /etc/hosts needed) |
| `test-multiregion.sh` | Basic connectivity, upload, routing tests (host-based) |
| `test-failover.sh` | Large file upload (1GB), failover during operations (host-based) |

### run-tests.sh Options (Recommended)

```bash
# Multi-region tests
./scripts/run-tests.sh multiregion all          # Run all tests
./scripts/run-tests.sh multiregion connectivity # Test endpoints
./scripts/run-tests.sh multiregion upload       # Test block upload/download
./scripts/run-tests.sh multiregion routing      # Test regional routing

# Failover tests
./scripts/run-tests.sh failover all             # Run all failover tests
./scripts/run-tests.sh failover setup           # Create 1GB test file
./scripts/run-tests.sh failover upload          # Upload 1GB file
```

### test-failover.sh Options (Host-based)

```bash
./scripts/test-failover.sh setup       # Create 1GB test file and repo
./scripts/test-failover.sh upload      # Upload 1GB (128 x 8MB blocks)
./scripts/test-failover.sh download    # Download with mid-way server stop
./scripts/test-failover.sh upload-fail # Upload with mid-way server stop
./scripts/test-failover.sh recovery    # Verify data after server restart
./scripts/test-failover.sh cleanup     # Remove test files
./scripts/test-failover.sh all         # Run all tests sequentially
```

## Testing Scenarios

### 1. Basic Connectivity

```bash
# Test each endpoint
curl http://localhost:8080/ping
curl http://us.sesamefs.local:8080/ping
curl http://eu.sesamefs.local:8080/ping

# Check auth
curl http://localhost:8080/api2/account/info/ \
  -H "Authorization: Token dev-token-123"
```

### 2. Regional Upload/Download

```bash
TOKEN="dev-token-123"

# Create a repo via USA endpoint
REPO=$(curl -s -X POST "http://us.sesamefs.local:8080/api2/repos" \
  -H "Authorization: Token $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"usa-test"}' | jq -r '.id')

# Upload a block to USA
DATA="Test data for USA"
HASH=$(echo -n "$DATA" | shasum -a 1 | cut -d' ' -f1)
curl -X PUT "http://us.sesamefs.local:8080/seafhttp/repo/$REPO/block/$HASH" \
  -H "Authorization: Token $TOKEN" \
  --data-raw "$DATA"

# Verify block is in USA bucket
docker exec sesamefs-multiregion-minio-1 mc ls local/sesamefs-usa/
```

### 2.1 Region-Pinned Library Behavior

Expected behavior in the current implementation:

- A library created with explicit `storage_id` stays pinned to that storage class.
- A library created without `storage_id` inherits the region mapped from the request hostname.
- Later reads must follow the persisted library choice even if the request hits a different hostname.
- Historic downloads/raw previews and raw share-link responses must behave the same way.

What is not covered by this safe slice:

- migrating existing libraries between regions after data already exists
- automatic rebalancing of old blocks between regions
- region-isolated deduplication/GC redesign

### 3. Failover Testing

```bash
# Stop USA server
docker-compose -f docker-compose-multiregion.yaml stop sesamefs-usa

# Verify requests still work (nginx failover to EU)
curl http://localhost:8080/ping

# Check nginx logs
docker-compose -f docker-compose-multiregion.yaml logs nginx | tail -20

# Restart USA server
docker-compose -f docker-compose-multiregion.yaml start sesamefs-usa
```

### 4. Storage Failover

```bash
# To test storage backend failover (not just nginx failover):
# 1. Mark USA storage as unhealthy in the code
# 2. Verify blocks are written to EU bucket instead
# 3. Check logs for failover messages

docker-compose -f docker-compose-multiregion.yaml logs sesamefs-usa | grep -i failover
docker-compose -f docker-compose-multiregion.yaml logs sesamefs-eu | grep -i failover
```

## MinIO Console

Access MinIO console at http://localhost:9001
- Username: `minioadmin`
- Password: `minioadmin`

View buckets and uploaded blocks directly.

## Debugging

### Check Container Status
```bash
docker-compose -f docker-compose-multiregion.yaml ps
```

### View Logs
```bash
# All services
docker-compose -f docker-compose-multiregion.yaml logs -f

# Specific service
docker-compose -f docker-compose-multiregion.yaml logs -f sesamefs-usa
docker-compose -f docker-compose-multiregion.yaml logs -f nginx
```

### Check Bucket Contents
```bash
# List all buckets
docker exec sesamefs-multiregion-minio-1 mc ls local/

# List USA bucket
docker exec sesamefs-multiregion-minio-1 mc ls local/sesamefs-usa/

# List EU bucket
docker exec sesamefs-multiregion-minio-1 mc ls local/sesamefs-eu/
```

### Check Database
```bash
docker exec sesamefs-multiregion-cassandra-1 cqlsh -e "
  SELECT * FROM sesamefs.block_id_mappings LIMIT 10;
"
```

## Cleanup

```bash
# Stop and remove containers
docker-compose -f docker-compose-multiregion.yaml down

# Also remove volumes (data)
docker-compose -f docker-compose-multiregion.yaml down -v
```

## Configuration Files

| File | Purpose |
|------|---------|
| `docker-compose.yaml` | Development stack (single instance) |
| `docker-compose-multiregion.yaml` | Multi-region stack definition |
| `Dockerfile.test` | Test container image |
| `configs/nginx-multiregion.conf` | Nginx load balancer config |
| `configs/config-usa.yaml` | USA server structural configuration; credentials and other secrets still come from compose env vars |
| `configs/config-eu.yaml` | EU server structural configuration; credentials and other secrets still come from compose env vars |
| `scripts/bootstrap.sh` | Unified environment setup (dev/multiregion) |
| `scripts/run-tests.sh` | Container-based test runner |
| `scripts/test-multiregion.sh` | Multi-region test script |
| `scripts/test-failover.sh` | Failover test script |

The multiregion YAML files are intentionally complete under the current config schema. They still keep `auth.dev_tokens` because this stack is test-oriented, but S3/MinIO credentials and other secrets belong in compose env vars, not in the YAML files.

## Differences from Single-Server Setup

| Aspect | Single Server | Multi-Region |
|--------|---------------|--------------|
| Entry point | Direct to server | nginx load balancer |
| Storage | One bucket | Multiple regional buckets |
| Failover | None | Automatic (nginx + storage) |
| Config | One config.yaml | Per-server configs |
| Hostname routing | Limited | Full support |

## Test Results (Reference)

These are the expected results from running the test suite:

### 1GB Upload Test
```
Total chunks: 128
Successful: 128
Failed: 0
Duration: ~21s
Rate: ~6 chunks/sec
Data rate: ~49 MB/sec
```

### Download Failover Test
- Downloaded first 64 blocks (512MB) successfully
- USA server stopped mid-download
- nginx seamlessly failed over to EU server
- Downloaded remaining 64 blocks (512MB) successfully
- **Result: 128/128 blocks verified, 0 failures**

### Upload Failover Test
- Uploaded first 32 blocks (256MB) successfully
- EU server stopped mid-upload
- nginx seamlessly failed over to USA server
- Uploaded remaining 32 blocks (256MB) successfully
- **Result: 64/64 blocks uploaded, 0 failures**

### Key Observations

1. **Seamless Failover**: nginx automatically routes to healthy backends with no client-side retry needed
2. **Data Integrity**: All blocks verified after failover - no data loss or corruption
3. **Shared Storage**: Both servers access the same MinIO buckets, so data uploaded through one is available from the other
4. **Recovery**: After server restart, all data remains accessible
