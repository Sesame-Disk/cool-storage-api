# SesameFS API Reference

This document covers API endpoints, implementation status, and Seafile compatibility.

## Status Legend

| Status | Meaning |
|--------|---------|
| ✅ | Fully implemented and tested |
| ⚠️ | Stub exists (route defined, returns success, but no backend logic) |
| ❌ | Not implemented |

---

## Seafile Compatibility Overview

SesameFS implements a Seafile-compatible API for file operations. The implementation follows Seafile's two-step upload/download pattern:

1. Client requests an access URL from the API
2. Client performs the file operation using that URL
3. Server proxies the operation to the backend storage (S3/MinIO)

### Key Differences from Seafile

| Feature | Seafile | SesameFS |
|---------|---------|----------|
| Backend Storage | Custom block storage | S3-compatible (AWS S3, MinIO) |
| Database | SQLite/MySQL | Apache Cassandra |
| Chunking | Custom CDC | FastCDC (server-side) |
| Sync Protocol | Proprietary | **Implemented** (Desktop client compatible) |
| Authentication | Built-in + LDAP | OIDC + Dev tokens |

---

## Sync Protocol (`/seafhttp/`) - ✅ Complete

These endpoints enable Seafile Desktop client synchronization.

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/seafhttp/protocol-version` | GET | ✅ | Returns `{"version": 2}` |
| `/seafhttp/repo/:id/permission-check/` | GET | ✅ | Returns empty 200 |
| `/seafhttp/repo/:id/quota-check/` | GET | ✅ | Returns quota info |
| `/seafhttp/repo/:id/commit/HEAD` | GET/PUT | ✅ | Get/update HEAD |
| `/seafhttp/repo/:id/commit/:cid` | GET/PUT | ✅ | Get/store commit |
| `/seafhttp/repo/:id/block/:bid` | GET/PUT | ✅ | Download/upload block |
| `/seafhttp/repo/:id/check-blocks/` | POST | ✅ | Check block existence |
| `/seafhttp/repo/:id/fs/:fsid` | GET | ✅ | Get FS object |
| `/seafhttp/repo/:id/fs-id-list/` | GET | ✅ | List FS IDs (JSON array) |
| `/seafhttp/repo/:id/recv-fs/` | POST | ✅ | Receive FS objects (binary) |
| `/seafhttp/repo/:id/check-fs/` | POST | ✅ | Check FS object existence |
| `/seafhttp/repo/:id/pack-fs/` | POST | ✅ | Pack multiple FS objects |
| `/seafhttp/repo/head-commits-multi` | POST | ✅ | Multi-repo head check |

### Critical Format Requirements

| Endpoint | Requirement | Notes |
|----------|-------------|-------|
| `/commit/:id` | `parent_id: null` | Use `*string` type, not empty string |
| `/commit/:id` | `version: 1` | Must be 1, not 0 |
| `/commit/:id` | `creator: "0000...0"` | 40 zeros |
| `/fs-id-list` | JSON array `[]` | NOT newline-separated text |
| `/permission-check` | Empty body | Just HTTP 200, no JSON |
| `/recv-fs` | Binary format | 40-char hex ID + binary object data |

### Binary FS Object Format

The `recv-fs` endpoint receives FS objects in binary packed format:
```
[40-char hex FS ID][newline][object data][40-char hex FS ID][newline]...
```

Object data starts with a type byte:
- `0x01` = File object
- `0x03` = Directory object

---

## Libraries (`/api2/repos/`) - ✅ Complete

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/api2/repos/` | GET | ✅ | List libraries |
| `/api2/repos/` | POST | ✅ | Create library |
| `/api2/repos/:id/` | GET | ✅ | Get library info |
| `/api2/repos/:id/` | DELETE | ✅ | Delete library |
| `/api2/repos/:id/download-info/` | GET | ✅ | Sync info for desktop |

---

## Authentication - ✅ Basic Complete

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/api2/auth-token/` | POST | ✅ | Login (dev mode) |
| `/api2/account/info/` | GET | ✅ | User info |
| `/api2/server-info/` | GET | ✅ | Server capabilities |

---

## Upload/Download Flow

### Upload Flow

**Step 1: Get Upload Link**
```
GET /api/v2/repos/{repo_id}/upload-link/?p={parent_dir}
Authorization: Token {api_token}
```

**Response:**
```
http://server:8080/seafhttp/upload-api/{upload_token}
```

**Step 2: Upload File**
```
POST /seafhttp/upload-api/{upload_token}
Content-Type: multipart/form-data

file: (binary)
parent_dir: /path/to/parent
replace: 0 or 1
```

**Response (with `?ret-json=1`):**
```json
[{"name": "filename.txt", "id": "file_id_hash", "size": 1234}]
```

### Download Flow

**Step 1: Get Download Link**
```
GET /api/v2/repos/{repo_id}/file/download-link?p={file_path}
Authorization: Token {api_token}
```

**Response:**
```
http://server:8080/seafhttp/files/{download_token}/{filename}
```

**Step 2: Download File**
```
GET /seafhttp/files/{download_token}/{filename}
```

---

## Token Management

Tokens secure file transfer operations with metadata (org, repo, path, user, expiration).

| Type | Purpose | Usage |
|------|---------|-------|
| **Upload token** | Grants permission to upload a file to a specific path | Single-use (deleted after upload) |
| **Download token** | Grants permission to download a specific file | Reusable until expiration |

**TTL Configuration:**
```yaml
seafhttp:
  token_ttl: 1h  # Default: 1 hour
```

**Security**:
- Tokens are stored in-memory and automatically cleaned up
- Each token is cryptographically random (128-bit)
- Tokens are scoped to organization, repository, and file path
- Upload tokens are invalidated immediately after use

---

## Phase 1: Core File Operations

**Priority: HIGH** | **Status: Partially Complete**

### File Metadata & Info

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/detail/` | GET | ❌ | Get file metadata |

### File CRUD Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/` | GET | ⚠️ | Get file info |
| `/api2/repos/:id/file/` | DELETE | ⚠️ | Delete file |
| `/api2/repos/:id/file/` | POST | ❌ | Create/rename/revert file |

**POST operations (via `operation` parameter):**
```
POST /api2/repos/:id/file/?p=/path/to/file
  operation=create     → Create empty file
  operation=rename     → Rename file (needs newname param)
  operation=revert     → Revert to commit (needs commit_id param)
```

### Directory Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/dir/` | GET | ✅ | List directory contents |
| `/api2/repos/:id/dir/` | POST | ⚠️ | Create directory |
| `/api2/repos/:id/dir/` | DELETE | ⚠️ | Delete directory |
| `/api2/repos/:id/dir/detail/` | GET | ❌ | Get directory metadata |

### Move & Copy

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/move/` | POST | ⚠️ | Move file |
| `/api2/repos/:id/file/copy/` | POST | ⚠️ | Copy file |

**Parameters:**
- `src_repo_id` - Source repository
- `src_dir` - Source directory path
- `dst_repo_id` - Destination repository
- `dst_dir` - Destination directory path
- `file_names` - JSON array of filenames

### Update Link (File Overwrite)

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/update-link/` | GET | ❌ | Get URL to overwrite existing file |

---

## Phase 2: User Features

**Priority: MEDIUM**

### Starred Files

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/starredfiles/` | GET | ✅ | List user's starred files |
| `/api2/starredfiles/` | POST | ✅ | Star a file |
| `/api2/starredfiles/` | DELETE | ✅ | Unstar a file |
| `/api/v2.1/starred-items/` | GET/POST/DELETE | ✅ | v2.1 API variant |

### File Locking

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/` | PUT | ✅ | Lock/unlock file |

**Operations:**
```
PUT /api2/repos/:id/file/?p=/path
  operation=lock       → Lock file for editing
  operation=unlock     → Release lock
```

### Trash / Recycle Bin

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/trash/` | GET | ❌ | List deleted files |
| `/api2/repos/:id/trash/` | DELETE | ❌ | Empty trash |
| `/api2/repos/:id/trash/revert/` | POST | ❌ | Restore file from trash |

### File History & Revisions

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/history/` | GET | ❌ | List file's revision history |
| `/api2/repos/:id/file/revision/` | GET | ❌ | Download specific revision |

**Response format:**
```json
{
  "data": [
    {
      "commit_id": "abc123...",
      "rev_file_id": "fs_id_here",
      "ctime": 1704067200,
      "description": "Modified via web",
      "creator_name": "user@example.com",
      "size": 1048576,
      "path": "/docs/readme.md"
    }
  ]
}
```

### Background Workers

| Worker | Interval | Priority | Description |
|--------|----------|----------|-------------|
| **GC Worker** | 24h | HIGH | Delete expired versions and orphaned blocks |
| **Lifecycle Worker** | 1h | MEDIUM | Move cold files to Glacier tier |
| **Metrics Worker** | 5m | LOW | Collect storage stats per org |

**GC Configuration:**
```yaml
gc:
  enabled: true
  interval: 24h
  grace_period: 24h
  batch_size: 1000
  max_duration: 4h
  dry_run: false
```

---

## Phase 3: Productivity Features

**Priority: MEDIUM** | **Status: OnlyOffice complete**

### File Viewer Routes - ✅ Implemented

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/lib/:repo_id/file/*path` | GET | ✅ | File viewer (OnlyOffice or download) |

**Authentication:** Accepts token from `Authorization` header or `?token=` query parameter.

**Behavior:**
- Office files (docx, xlsx, pptx, etc.) → Renders OnlyOffice editor
- `?dl=1` parameter → Force download
- Other files → 302 redirect to download URL

### OnlyOffice Integration - ✅ Implemented

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api/v2.1/repos/:id/onlyoffice/` | GET | ✅ | Get editor configuration |
| `/onlyoffice/editor-callback/` | POST | ✅ | Handle save callback |

**Configuration:**
```yaml
onlyoffice:
  enabled: true
  api_js_url: "https://office.sesamedisk.com/web-apps/apps/api/documents/api.js"
  jwt_secret: "your-secret-key"
  view_extensions: [doc, docx, ppt, pptx, xls, xlsx, odt, odp, ods]
  edit_extensions: [docx, pptx, xlsx]
```

**Callback Status Codes:**
- `1` = Document being edited
- `2` = Document ready for saving
- `4` = Document closed with no changes
- `6` = Document editing error

### File Tags

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api/v2.1/repos/:id/repo-tags/` | GET/POST | ✅ | Manage repo tags |
| `/api/v2.1/repos/:id/file-tags/` | GET/POST/DELETE | ✅ | Tag files |

### Batch Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/batch-copy-item/` | POST | ❌ | Copy multiple files |
| `/api2/repos/batch-move-item/` | POST | ❌ | Move multiple files |
| `/api2/repos/batch-delete-item/` | POST | ❌ | Delete multiple files |

### Activities & Events

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/events/` | GET | ❌ | User's activity feed |
| `/api2/repo-history/:id/` | GET | ❌ | Library change history |

---

## Phase 4: Advanced Features

**Priority: LOW**

### Search

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/search/` | GET | ❌ | Search files by name/content |

### Thumbnails & Preview

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/thumbnail/` | GET | ❌ | Get image thumbnail |
| `/api2/repos/:id/file/preview/` | GET | ❌ | Preview document |

### File Comments

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/file/comments/` | GET/POST | ❌ | Manage comments |
| `/api2/repos/:id/file/comments/:id/` | PUT/DELETE | ❌ | Edit/delete comment |

### Folder Permissions (Pro feature)

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/dir/shared_items/` | GET/PUT/DELETE | ❌ | Manage folder shares |

---

## Phase 5: Frontend Modernization

**Priority: LOW (Post-Launch)**

### Recommended: Tailwind CSS Migration

| Phase | Duration | Description |
|-------|----------|-------------|
| 5.1 Setup | 1 week | Add Tailwind to build pipeline |
| 5.2 Core Layout | 2 weeks | Responsive side panel, header, modals |
| 5.3 Data Components | 2 weeks | File list, library list, breadcrumb |
| 5.4 Forms | 1 week | Touch-friendly inputs, mobile file picker |
| Testing | 1 week | Polish and QA |

### Success Criteria

- [ ] UI works on mobile devices (320px+)
- [ ] Touch-friendly interactions
- [ ] No horizontal scrolling on mobile
- [ ] Lighthouse mobile score > 80
- [ ] File upload works on mobile browsers

---

## Implementation Guidelines

### Creating New Commits

All file/directory modifications should:
1. Create a new root FS object with updated structure
2. Create a new commit pointing to the new root
3. Update `libraries.head_commit_id`

### File Path Handling

- Always normalize paths: start with `/`, no trailing `/`
- Handle both URL-encoded and decoded paths
- Seafile uses `p` query parameter for paths

### Error Responses

```json
{"error_msg": "File not found"}
```

Or for validation errors:
```json
{"error": "path is required"}
```

---

## Database Schema Additions

Tables needed for full feature support:

```cql
-- Starred files (implemented)
CREATE TABLE starred_files (
    user_id UUID,
    repo_id UUID,
    path TEXT,
    starred_at TIMESTAMP,
    PRIMARY KEY ((user_id), repo_id, path)
);

-- File locks (implemented)
CREATE TABLE locked_files (
    repo_id UUID,
    path TEXT,
    locked_by UUID,
    locked_at TIMESTAMP,
    PRIMARY KEY ((repo_id), path)
);

-- Activities (planned)
CREATE TABLE activities (
    org_id UUID,
    activity_id TIMEUUID,
    user_id UUID,
    repo_id UUID,
    path TEXT,
    op_type TEXT,
    old_path TEXT,
    details MAP<TEXT, TEXT>,
    created_at TIMESTAMP,
    PRIMARY KEY ((org_id), activity_id)
) WITH CLUSTERING ORDER BY (activity_id DESC);

-- File comments (planned)
CREATE TABLE file_comments (
    repo_id UUID,
    path TEXT,
    comment_id TIMEUUID,
    user_id UUID,
    content TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY ((repo_id, path), comment_id)
);

-- File tags (planned)
CREATE TABLE file_tags (
    repo_id UUID,
    tag_id UUID,
    name TEXT,
    color TEXT,
    PRIMARY KEY ((repo_id), tag_id)
);

CREATE TABLE file_tag_mappings (
    repo_id UUID,
    path TEXT,
    tag_id UUID,
    PRIMARY KEY ((repo_id, path), tag_id)
);
```

---

## Testing with curl

```bash
# 1. Get upload link
UPLOAD_URL=$(curl -s \
  "http://localhost:8080/api/v2/repos/{repo_id}/upload-link?p=/" \
  -H "Authorization: Token dev-token-123")

# 2. Upload file
curl -X POST "$UPLOAD_URL?ret-json=1" \
  -F "file=@myfile.txt" \
  -F "parent_dir=/"

# 3. Get download link
DOWNLOAD_URL=$(curl -s \
  "http://localhost:8080/api/v2/repos/{repo_id}/file/download-link?p=/myfile.txt" \
  -H "Authorization: Token dev-token-123")

# 4. Download file
curl -O "$DOWNLOAD_URL"
```

---

## References

- [Seafile API Reference](https://seafile-api.readme.io/)
- [Seafile Admin Manual](https://manual.seafile.com/12.0/develop/web_api_v2.1/)
- [Implementation](../internal/api/v2/files.go)
