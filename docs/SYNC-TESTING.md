# Seafile Sync Protocol Testing

This guide explains how to test the Seafile desktop sync protocol using the containerized `seaf-cli` tool.

## Quick Start

### Automated Test Suite (Recommended)

Run the full sync protocol test suite with a single command:

```bash
# Preferred docker-native one-liner
docker compose --profile test run --rm --build sync-test

# Verbose docker-native run
docker compose --profile test run --rm --build -e SYNC_TEST_ARGS=--verbose sync-test

# Unified wrapper: prefers sync-test, falls back to the debug container path
./scripts/test.sh sync

# Run all tests (creates libraries, syncs, verifies, cleans up)
./scripts/test-sync.sh

# With verbose output
./scripts/test-sync.sh --verbose

# Keep test libraries after completion (for debugging)
./scripts/test-sync.sh --keep

# Clean up orphaned test libraries from previous runs
./scripts/test-sync.sh --cleanup
```

The test suite covers:
- **Unencrypted sync**: Remote→Local and Local→Remote file sync
- **Encrypted sync**: Remote→Local plus Local→Remote remote-presence verification for password-protected libraries
- **Multiple files**: Batch file sync verification
- **Large files**: 64KB+ files (multi-block)
- **Binary files**: Non-text content integrity
- **File modifications**: Update existing files
- **Subdirectory sync**: Nested folder structures

These checks are functional single-client sync scenarios. They do not currently
prove active-active conflict recovery for `PUT /seafhttp/repo/:repo_id/commit/HEAD`
or `POST /seafhttp/repo/:repo_id/update-branch` under multi-node contention.

`docker compose --profile test run --rm --build sync-test` is the preferred
Docker-native entry point. It runs the real `scripts/test-sync.sh` suite inside
the `docker/seafile-cli` image with fresh dedicated client config/data volumes
on each run. `./scripts/test.sh sync` is the convenience wrapper for that same
flow; it prefers `sync-test` automatically and only falls back to the long-
lived debug `seafile-cli` container when needed.

### Manual Testing

```bash
# Start the backend
docker compose up -d sesamefs

# Start seafile-cli only when you want manual control
docker compose --profile debug up -d --build seafile-cli

# Enter the seafile-cli container
docker exec -it sesamefs-seafile-cli-1 bash

# Or run commands directly
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh help
```

## Available Commands

| Command | Description |
|---------|-------------|
| `seaf-test.sh init` | Initialize seafile client config |
| `seaf-test.sh start` | Start seafile daemon |
| `seaf-test.sh stop` | Stop seafile daemon |
| `seaf-test.sh get-token` | Get auth token from server |
| `seaf-test.sh list-remote` | List remote libraries |
| `seaf-test.sh list-local` | List locally synced libraries |
| `seaf-test.sh status` | Show sync status |
| `seaf-test.sh sync <id> [dir]` | Sync a library |
| `seaf-test.sh download <id>` | Download a library |
| `seaf-test.sh desync <dir>` | Desync a library |
| `seaf-test.sh create <name>` | Create a new library |
| `seaf-test.sh logs` | Watch seafile logs |
| `seaf-test.sh test-cycle` | Run full test cycle |

## Full Test Workflow

### 1. Start Services

```bash
docker-compose up -d
```

### 2. Initialize and Start Seafile Client

```bash
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh init
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh start
```

### 3. List Remote Libraries

```bash
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh list-remote
```

Example output:
```json
[
  {
    "repo_id": "abc12345-1234-5678-abcd-1234567890ab",
    "repo_name": "My Library",
    "permission": "rw"
  }
]
```

### 4. Sync a Library

```bash
# Using library ID from the list
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh sync abc12345-1234-5678-abcd-1234567890ab
```

### 5. Check Sync Status

```bash
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh status
```

### 6. View Logs

```bash
docker exec -it sesamefs-seafile-cli-1 seaf-test.sh logs
```

## Testing Encrypted Libraries

To sync an encrypted library, you need to provide the library password. The seaf-cli supports this via the `-e` flag:

```bash
# Direct seaf-cli command for encrypted library
docker exec -it sesamefs-seafile-cli-1 seaf-cli sync \
  -l <library-id> \
  -s http://sesamefs:8080 \
  -d /seafile-data/<library-id> \
  -u 00000000-0000-0000-0000-000000000001 \
  -p dev-token-123 \
  -e <library-password>
```

## Debugging Sync Issues

### Check Client Logs

```bash
docker exec -it sesamefs-seafile-cli-1 cat /home/seafuser/.ccnet/logs/seafile.log
```

### Check Server Logs

```bash
docker-compose logs sesamefs
```

### Common Error Messages

| Error | Cause | Solution |
|-------|-------|----------|
| `Failed to inflate` | pack-fs not zlib compressed | Check server sends compressed data |
| `Failed to find dir` | fs_id missing from fs-id-list | Check recursive fs_id collection |
| `Error when indexing` | fs objects missing | Check pack-fs returns all requested objects |

### Test Endpoints Manually

```bash
# Get token
TOKEN=$(docker exec -it sesamefs-seafile-cli-1 seaf-test.sh get-token)

# Test HEAD commit
curl -H "Authorization: Token $TOKEN" http://localhost:8080/seafhttp/repo/<repo_id>/commit/HEAD

# Test fs-id-list
curl -H "Authorization: Token $TOKEN" "http://localhost:8080/seafhttp/repo/<repo_id>/fs-id-list/?server-head=<commit_id>"

# Test pack-fs
curl -X POST -H "Authorization: Token $TOKEN" \
  -d '["<fs_id_1>", "<fs_id_2>"]' \
  http://localhost:8080/seafhttp/repo/<repo_id>/pack-fs -o /tmp/packfs.bin

# Inspect pack-fs format
xxd /tmp/packfs.bin | head -20
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `SEAF_SERVER_URL` | `http://sesamefs:8080` | Server URL |
| `SEAF_USERNAME` | `00000000-...` | Dev user UUID |
| `SEAF_PASSWORD` | `dev-token-123` | Dev token |
| `SEAF_TOKEN` | (empty) | Pre-obtained auth token |

## Architecture

```
┌─────────────────┐     ┌─────────────────┐
│  seafile-cli    │────▶│    sesamefs     │
│   container     │     │    container    │
│                 │     │                 │
│ • seaf-cli      │     │ • /seafhttp/*   │
│ • seaf-daemon   │     │ • /api2/*       │
│ • seaf-test.sh  │     │ • Cassandra     │
└─────────────────┘     │ • MinIO (S3)    │
                        └─────────────────┘
```

## Sync Protocol Flow

```
Client                          Server
  │                               │
  │ GET /seafhttp/protocol-version│
  │──────────────────────────────▶│
  │◀──────────────────────────────│
  │                               │
  │ GET /api2/repos/              │
  │──────────────────────────────▶│
  │◀──────────────────────────────│
  │                               │
  │ GET /download-info            │
  │──────────────────────────────▶│
  │◀──────────────────────────────│
  │                               │
  │ GET /commit/HEAD              │
  │──────────────────────────────▶│
  │◀──────────────────────────────│
  │                               │
  │ GET /fs-id-list               │
  │──────────────────────────────▶│
  │◀──────────────────────────────│
  │                               │
  │ POST /pack-fs                 │
  │──────────────────────────────▶│
  │◀──────────────────────────────│
  │                               │
  │ GET /block/:id (for each)     │
  │──────────────────────────────▶│
  │◀──────────────────────────────│
```

## Troubleshooting

### Container Won't Start

```bash
# Check container status
docker-compose ps

# View container logs
docker-compose logs seafile-cli
```

### Cannot Connect to Server

```bash
# Test network connectivity
docker exec -it sesamefs-seafile-cli-1 curl http://sesamefs:8080/ping

# Should return: {"message":"pong"}
```

### Auth Token Failed

```bash
# Test auth endpoint directly
docker exec -it sesamefs-seafile-cli-1 curl -X POST \
  http://sesamefs:8080/api2/auth-token/ \
  -d "username=00000000-0000-0000-0000-000000000001" \
  -d "password=dev-token-123"
```

## Comparing Local vs Reference Server

Use the `seafile-cli-debug` container to compare responses between local SesameFS and a reference Seafile server.

### Quick Comparison Script

```bash
# Get reference server credentials from .seafile-reference.md
export SEAFILE_TOKEN=$(curl -s -X POST "https://app.nihaoconsult.com/api2/auth-token/" \
  -d "username=abel.aguzmans%40gmail.com" \
  -d "password=Qwerty123%21" | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")
export SEAFILE_REPO_ID="aa89cce8-f2f1-44ad-98f0-1ea87ca6a3ed"
export SEAFILE_SYNC_TOKEN=$(curl -s -H "Authorization: Token $SEAFILE_TOKEN" \
  "https://app.nihaoconsult.com/api2/repos/$SEAFILE_REPO_ID/download-info/" | \
  python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

# Compare commit/HEAD
echo "=== Reference ===" && curl -s -H "Seafile-Repo-Token: $SEAFILE_SYNC_TOKEN" \
  "https://app.nihaoconsult.com/seafhttp/repo/$SEAFILE_REPO_ID/commit/HEAD"
echo -e "\n=== Local ===" && curl -s -H "Seafile-Repo-Token: $LOCAL_SYNC_TOKEN" \
  "http://localhost:8080/seafhttp/repo/$LOCAL_REPO_ID/commit/HEAD"
```

### Key Fields to Verify

| Endpoint | Field | Expected |
|----------|-------|----------|
| `/commit/HEAD` | `is_corrupted` | Integer `0`, not boolean `false` |
| `/commit/{id}` | `repo_desc` | Always present (even if empty) |
| `/check-fs` | Response | JSON array `[]` |
| `/check-blocks` | Response | JSON array `[]` |
| `/pack-fs` | Dirent keys | Alphabetical order: `id`, `mode`, `modifier`, `mtime`, `name`, `size` |

### Debugging Pack-FS Format

```bash
# Fetch and inspect pack-fs binary format
curl -s -X POST -H "Seafile-Repo-Token: $SYNC_TOKEN" \
  -H "Content-Type: application/json" \
  "http://localhost:8080/seafhttp/repo/$REPO_ID/pack-fs" \
  -d '["<fs_id>"]' > /tmp/pack-fs.bin

# Parse and display
python3 << 'PYEOF'
import zlib, json
with open('/tmp/pack-fs.bin', 'rb') as f:
    data = f.read()
fs_id = data[0:40].decode('ascii')
size = int.from_bytes(data[40:44], 'big')
obj = json.loads(zlib.decompress(data[44:44+size]))
print(json.dumps(obj, indent=2))
PYEOF
```

## References

- [Seafile CLI Documentation](https://help.seafile.com/syncing_client/linux-cli/)
- [Seafile Source Code](https://github.com/haiwen/seafile)
- [CLAUDE.md - Sync Protocol Details](../CLAUDE.md)
- [SEAFILE-SYNC-PROTOCOL.md](SEAFILE-SYNC-PROTOCOL.md) - Full protocol specification
