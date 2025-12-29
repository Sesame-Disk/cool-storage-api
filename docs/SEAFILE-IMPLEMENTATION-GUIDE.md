# Seafile Protocol Implementation Guide

This document captures lessons learned while implementing Seafile-compatible APIs for SesameFS. Use this as a reference when implementing new endpoints or debugging sync issues.

---

## Key Resources

### 1. Reference Seafile Server
**URL**: https://app.nihaoconsult.com/
**Credentials**: See `.seafile-reference.md`

Use this server to:
- Test API responses and compare formats
- Capture real request/response patterns
- Verify your implementation matches Seafile's behavior

### 2. Seafile Server Source Code (Go)
**Repository**: https://github.com/haiwen/seafile-server

Key files for sync protocol:
- `fileserver/sync_api.go` - Sync endpoints (pack-fs, recv-fs, check-fs, etc.)
- `fileserver/fsmgr/fsmgr.go` - FS object serialization format
- `fileserver/commitmgr/` - Commit handling
- `fileserver/blockmgr/` - Block storage

### 3. Seafile API Documentation
- Official API: https://seafile-api.readme.io/
- Admin Manual: https://manual.seafile.com/
- Web API v2.1: https://haiwen.github.io/seafile-admin-docs/

### 4. Client Logs
**macOS**: `~/.ccnet/logs/seafile.log`

This log shows exactly what the client is doing and any errors it encounters.

---

## Debugging Workflow

### Step 1: Check Client Logs
```bash
tail -f ~/.ccnet/logs/seafile.log
```

Look for:
- State transitions: `sync state transition from 'X' to 'Y'`
- Error messages: `Server error`, `Incomplete object package`
- HTTP failures: `libcurl failed to POST`

### Step 2: Check Server Logs
Add debug logging to your endpoint:
```go
log.Printf("endpoint: repo=%s, body=%q", repoID, string(body))
```

### Step 3: Compare with Real Seafile Server
```bash
# Get auth token
TOKEN=$(curl -s -X POST "https://app.nihaoconsult.com/api2/auth-token/" \
  -d "username=EMAIL" -d "password=PASSWORD" | jq -r '.token')

# Get sync token for a repo
SYNC_TOKEN=$(curl -s -H "Authorization: Token $TOKEN" \
  "https://app.nihaoconsult.com/api2/repos/$REPO_ID/download-info/" | jq -r '.token')

# Test sync endpoints
curl -s -H "Seafile-Repo-Token: $SYNC_TOKEN" \
  "https://app.nihaoconsult.com/seafhttp/repo/$REPO_ID/commit/HEAD"
```

### Step 4: Capture Binary Formats
```bash
# Use xxd to examine binary responses
curl -s -X POST -H "Seafile-Repo-Token: $TOKEN" \
  "https://app.nihaoconsult.com/seafhttp/repo/$REPO_ID/pack-fs" \
  -d '["fs_id_here"]' | xxd | head -30
```

---

## Common Pitfalls

### 1. Request Body Format
**Problem**: Client sends JSON array, code expects newline-separated strings.

**Example**: pack-fs endpoint
- Client sends: `["abc123...", "def456..."]`
- Code expected: `abc123...\ndef456...`

**Solution**: Always check both formats:
```go
var fsIDs []string
if strings.HasPrefix(bodyStr, "[") {
    json.Unmarshal(body, &fsIDs)
} else {
    fsIDs = strings.Split(bodyStr, "\n")
}
```

### 2. Binary vs JSON Response
**Problem**: Returning JSON when client expects binary (or vice versa).

**Example**: pack-fs response format
- Wrong: `{"objects": [...]}`
- Correct: `[40-byte ID][4-byte size BE][zlib-compressed JSON]`

**Solution**: Check Seafile source code for exact format:
```go
// From seafile-server/fileserver/sync_api.go
// pack-fs format: ID (40 bytes) + size (4 bytes BE) + zlib data
buf.WriteString(fsID)  // 40 bytes, no newline
binary.Write(&buf, binary.BigEndian, uint32(compressed.Len()))
buf.Write(compressed.Bytes())
```

### 3. FS Object Serialization
**Problem**: Using custom binary format instead of Seafile's JSON+zlib format.

**Correct format**:
```go
// Directory: zlib-compressed JSON
{"version": 1, "type": 3, "dirents": [...]}

// File: zlib-compressed JSON
{"version": 1, "type": 1, "block_ids": [...], "size": N}
```

**Type constants**:
- `1` = SEAF_METADATA_TYPE_FILE
- `2` = SEAF_METADATA_TYPE_LINK
- `3` = SEAF_METADATA_TYPE_DIR

### 4. Missing Endpoints
**Problem**: Client expects endpoints that aren't documented.

**Common missing endpoints**:
- `/api2/repo-tokens?repos=uuid1,uuid2` - Get sync tokens for multiple repos
- `/api2/starredfiles/` - Starred files list
- `/notification/ping` - Notification service (can return 404)

**Solution**: Watch server logs for 404s and implement as needed.

### 5. Trailing Slashes
**Problem**: Routes registered with trailing slashes don't match requests without them.

**Solution**: Register both variants or use middleware to strip trailing slashes:
```go
repo.POST("/pack-fs", h.PackFS)
repo.POST("/pack-fs/", h.PackFS)
```

### 6. Empty Arrays in JSON
**Problem**: `omitempty` tag causes empty arrays to be omitted.

**Example**: Directory with no files
- Wrong: `{"type": 3, "version": 1}` (missing dirents)
- Correct: `{"type": 3, "version": 1, "dirents": []}`

**Solution**: Use pointer to slice:
```go
type FSObject struct {
    Entries *[]FSEntry `json:"dirents,omitempty"`
}
// Initialize: obj.Entries = &[]FSEntry{}
```

---

## Sync Protocol Flow

### Download (Client syncs from server)
```
1. GET  /seafhttp/protocol-version
2. GET  /seafhttp/repo/{id}/permission-check?op=download
3. GET  /seafhttp/repo/{id}/commit/HEAD
4. GET  /seafhttp/repo/{id}/commit/{commit_id}
5. GET  /seafhttp/repo/{id}/fs-id-list/?server-head={commit_id}
6. POST /seafhttp/repo/{id}/pack-fs  (body: JSON array of fs_ids)
7. POST /seafhttp/repo/{id}/check-blocks (body: JSON array of block_ids)
8. GET  /seafhttp/repo/{id}/block/{block_id}  (for each missing block)
```

### Upload (Client syncs to server)
```
1. GET  /seafhttp/repo/{id}/permission-check?op=upload
2. GET  /seafhttp/repo/{id}/quota-check?delta={bytes}
3. PUT  /seafhttp/repo/{id}/commit/{new_commit_id}
4. POST /seafhttp/repo/{id}/check-fs (body: JSON array of fs_ids)
5. POST /seafhttp/repo/{id}/recv-fs (body: packed fs objects)
6. POST /seafhttp/repo/{id}/check-blocks
7. PUT  /seafhttp/repo/{id}/block/{block_id} (for each new block)
8. PUT  /seafhttp/repo/{id}/commit/HEAD?head={new_commit_id}
```

---

## Key Data Formats

### Commit Object
```json
{
  "commit_id": "abc123...",
  "repo_id": "uuid",
  "root_id": "def456...",      // Root FS object ID
  "parent_id": "ghi789...",    // null for first commit
  "creator_name": "user@email",
  "creator": "0000...0000",    // 40 zeros
  "description": "Added file.txt",
  "ctime": 1234567890,
  "version": 1
}
```

### FS Object (Directory)
```json
{
  "version": 1,
  "type": 3,
  "dirents": [
    {
      "id": "abc123...",       // FS ID of child
      "mode": 33188,           // 0100644 for file, 16384 for dir
      "name": "file.txt",
      "mtime": 1234567890,
      "modifier": "user@email",
      "size": 1234             // Only for files
    }
  ]
}
```

### FS Object (File)
```json
{
  "version": 1,
  "type": 1,
  "block_ids": ["abc123...", "def456..."],
  "size": 12345
}
```

### pack-fs Binary Format
```
[40-byte hex ID][4-byte size (BE uint32)][zlib-compressed JSON]
[40-byte hex ID][4-byte size (BE uint32)][zlib-compressed JSON]
...
```

No header, no separators between objects. Objects concatenated directly.

---

## Testing Checklist

Before marking a sync feature complete:

- [ ] Test with real Seafile desktop client
- [ ] Verify against real Seafile server response format
- [ ] Check client logs for errors
- [ ] Test with empty directories
- [ ] Test with large files (multiple blocks)
- [ ] Test create, update, and delete operations
- [ ] Verify SHA-1 to SHA-256 block ID mapping works

---

## Quick Reference Commands

```bash
# Start local server
./sesamefs

# Watch server logs
tail -f /tmp/sesamefs-debug.log

# Watch client logs
tail -f ~/.ccnet/logs/seafile.log

# Test auth
curl -X POST http://localhost:8080/api2/auth-token \
  -d "username=test" -d "password=test"

# Test with dev token
curl -H "Authorization: Token dev-token-123" \
  http://localhost:8080/api2/repos

# Check database
docker exec -i cool-storage-api-cassandra-1 cqlsh -e \
  "SELECT * FROM sesamefs.libraries;"
```

---

## Change Log

| Date | Issue | Solution |
|------|-------|----------|
| 2025-12-29 | pack-fs returning 0 bytes | Client sends JSON array, not newline-separated |
| 2025-12-29 | "Incomplete object package" | pack-fs needs zlib-compressed JSON, not binary |
| 2025-12-29 | Login shows "Server Error" | Missing `/api2/repo-tokens` endpoint |
| 2025-12-29 | Trailing slash 404s | Register routes with and without trailing slashes |
| 2025-12-29 | Empty root directory issues | Use pointer to slice for dirents to include empty arrays |
