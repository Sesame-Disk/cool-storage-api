# SesameFS API Reference

This document covers API endpoints, implementation status, and Seafile compatibility.

## Status Legend

| Status | Meaning |
|--------|---------|
| ✅ | Fully implemented and tested |
| ⚠️ | Stub exists (route defined, returns success, but no backend logic) |
| ❌ | Not implemented |

---

## 🔑 Desktop Client vs Web UI Endpoints

**IMPORTANT:** Not all endpoints are used by desktop clients!

| Endpoint Type | Used By | Can We Change? | Reference |
|---------------|---------|----------------|-----------|
| **`/seafhttp/`** | Desktop clients | ❌ No - FROZEN | [Sync Protocol RFC](SEAFILE-SYNC-PROTOCOL-RFC.md) |
| **`/api2/repos/` (library CRUD)** | Both | ❌ No - Required by seafile-js | [TECHNICAL-DEBT.md](TECHNICAL-DEBT.md#3-seafile-js-hardcoded-paths-no-action-needed) |
| **`/api2/` (sharing)** | Web UI only | ✅ Yes (but we match Seafile) | This doc |
| **`/api/v2.1/` (groups, settings)** | Web UI only | ✅ Yes (but we match Seafile) | This doc |

**For complete implementation status**, see [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md).

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

## Authentication and API Keys - ✅ Complete

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/api2/auth-token/` | POST | ✅ | Desktop/CLI login. In dev mode accepts dev credentials; in production accepts `email + API key` |
| `/api/v2.1/api-keys/` | GET | ✅ | List API keys for the authenticated user |
| `/api/v2.1/api-keys/` | POST | ✅ | Create user API key (raw key returned once) |
| `/api/v2.1/api-keys/:key_hash/` | DELETE | ✅ | Revoke user API key and invalidate derived sessions |
| `/api2/account/info/` | GET | ✅ | User info |
| `/api2/server-info/` | GET | ✅ | Server capabilities |

### API Key Semantics

- Scopes: `read`, `read-write`, `admin`
- The self-service and sysadmin UI forms default to `read-write`, which supports
  ordinary desktop, CLI, and API use without granting organization-administration
  authority. API callers must always send an explicit valid `scope`.
- `admin` is never a UI default. An authorized user or platform operator must
  select it explicitly for trusted administration tooling.
- Raw API keys are only returned on creation and are never persisted in plaintext
- Revoking an API key also invalidates sync/API-token sessions minted from that key
- If an API key has an expiry, the `/api2/auth-token/` session created from it cannot outlive that key

### Admin User API Keys - ✅ Complete

Platform superadmins can manage API keys for platform-org users through the admin user subresource.

| Endpoint | Method | Status | Notes |
|----------|--------|--------|-------|
| `/api/v2.1/admin/users/:email/api-keys/` | GET | ✅ | List API keys for a platform user |
| `/api/v2.1/admin/users/:email/api-keys/` | POST | ✅ | Create API key for a platform user |
| `/api/v2.1/admin/users/:email/api-keys/:key_hash/` | DELETE | ✅ | Revoke API key and invalidate derived sessions |

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

`upload-link` tokens enforce no-replace behavior. Re-uploading the same
filename through this URL auto-renames the new file even if the multipart form
tries to force `replace=1`.

For overwrite-by-default behavior, first request:

```
GET /api2/repos/{repo_id}/update-link/?p={parent_dir}
Authorization: Token {api_token}
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
| `/api2/repos/:id/dir/` | POST | ✅ | Create directory |
| `/api2/repos/:id/dir/` | DELETE | ⚠️ | Delete directory |
| `/api2/repos/:id/dir/detail/` | GET | ❌ | Get directory metadata |

**Create Directory:**
```http
POST /api2/repos/{repo_id}/dir/?p={path}&operation=mkdir
Authorization: Token {api_token}
```

**Parameters:**
- `p` - Directory path (e.g., `/folder1`, `/folder1/subfolder`)
- `operation` - Must be `mkdir`

**Response:** `200 OK` (directory created) or `400 Bad Request` (already exists or invalid path)

**Verified:** 2026-01-17 - Tested with comprehensive sync protocol framework

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
| `/api2/repos/:id/update-link/` | GET | ✅ | Get URL to overwrite existing file by default |

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
| `/api2/repo/file_revisions/:id/` | GET | ✅ | List file's revision history |
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
  enabled: false  # Required on every replica/DC while X1 remains open
  interval: 24h
  grace_period: 24h
  batch_size: 1000
  max_duration: 4h
  dry_run: false
```

The lease is not an activation signal. Keep `GC_ENABLED=false` everywhere until
`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01` (X1) closes. X2 is closed under the
stable-topology operational contract; a replication DC-set or RF change with existing
reference state requires a separately certified migration before GC can be reconsidered.
Only then may designated replicas in one DC participate under the lease; every other DC
remains disabled.

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
| `/lib/:repo_id/file/*path` | GET | ✅ | File viewer (renders OnlyOffice for supported files) |

**Server Configuration (config.yaml):**
```yaml
onlyoffice:
  enabled: true
  api_js_url: "https://office.example.com/web-apps/apps/api/documents/api.js"  # Browser-accessible URL
  jwt_secret: "your-secret-key"
  server_url: ""      # Optional explicit SesameFS URL for OnlyOffice fetches
  internal_url: ""    # Optional explicit OnlyOffice URL for callback downloads
  view_extensions: [doc, docx, ppt, pptx, xls, xlsx, odt, odp, ods]
  edit_extensions: [docx, pptx, xlsx]
```

In the standard external OnlyOffice deployment, SesameFS only needs these
runtime values:
- `ONLYOFFICE_ENABLED`
- `ONLYOFFICE_API_JS_URL`
- `ONLYOFFICE_JWT_SECRET`

Leave `server_url` empty unless OnlyOffice must fetch documents through a
different explicit SesameFS URL. Leave `internal_url` empty unless SesameFS
must download callbacks through a different explicit OnlyOffice URL than the
host implied by `api_js_url`.

**API Response Structure (Minimal - Like Seahub):**

The configuration must be minimal for reliable editing. Complex customization fields can cause view-only mode.

```json
{
  "doc": {
    "document": {
      "fileType": "docx",
      "key": "unique-doc-key-20chars",
      "title": "document.docx",
      "url": "https://files.example.com/seafhttp/files/{token}/document.docx",
      "permissions": {
        "edit": true,
        "download": true,
        "print": true,
        "copy": true,
        "review": true,
        "comment": true,
        "fillForms": true
      }
    },
    "documentType": "word",
    "editorConfig": {
      "callbackUrl": "https://files.example.com/onlyoffice/editor-callback/?doc_key=...",
      "mode": "edit",
      "user": { "id": "user-uuid", "name": "username" },
      "customization": {
        "forcesave": true,
        "submitForm": true
      }
    },
    "token": "jwt-token-here"
  },
  "api_js_url": "https://office.example.com/web-apps/apps/api/documents/api.js"
}
```

The callback is bound to the server-side `doc_key` mapping. SesameFS requires a
configured `ONLYOFFICE_JWT_SECRET` whenever OnlyOffice is enabled, resolves the
canonical `repo_id`, `file_path`, and `user_id` from that mapping during save, and
verifies the OnlyOffice JWT from either the callback body (`token`) or the
`Authorization` header before downloading the edited document.

**Critical Configuration Requirements:**

| Requirement | Details |
|-------------|---------|
| `permissions.fillForms` | Required for editing to work |
| `customization` | Keep minimal - only `forcesave` and `submitForm` |
| `mode: "edit"` | Must be "edit" for editable files |
| `token` | JWT must contain exact same fields as the config (no extra fields) |
| Document key | Stable per file version: `MD5(repo+path+fileId)[:20]` |

**Common Issues & Solutions:**

| Problem | Solution |
|---------|----------|
| Toolbar grayed out despite "Editing" mode | Simplify customization to only `forcesave` and `submitForm` |
| JWT validation errors | Ensure JWT payload matches config exactly |
| Document opens read-only | Add `fillForms: true` to permissions |
| Changes not saving in Docker/dev | Keep `api_js_url` pointed at the browser URL; SesameFS auto-detects `frontend`/`onlyoffice` for the local Compose stack when that URL is loopback |
| Stale document state | Document keys rotate automatically when the file version changes |

**Docker/Local Note:**

When OnlyOffice runs inside the same Docker Compose stack, the browser still
loads `api.js` from `localhost:8088`, but SesameFS now auto-detects the
internal Compose service URLs for callback/save traffic when `api_js_url`
points at a loopback host. Docker/local can therefore use the same runtime
inputs as production and leave `server_url` / `internal_url` empty:
```yaml
onlyoffice:
  api_js_url: "http://localhost:8088/web-apps/apps/api/documents/api.js"  # Browser URL
  server_url: ""      # Auto-detected as http://frontend for local Compose
  internal_url: ""    # Auto-detected as http://onlyoffice for local Compose
```

**Block Storage:**

Blocks must be stored using `BlockStore` with proper key sharding:
```go
blockStore, err := storage.NewOrgBlockStore(s3Store, "blocks/", orgID.String())
if err != nil {
    return err
}
legacyKey, err := blockStore.PutBlockData(ctx, &storage.BlockData{Hash: blockID, Data: content})
// legacyKey: blocks/<org_id>/XX/XX/blockID

mintedKey, err := blockStore.MintStorageKey(blockID)
storedKey, err := blockStore.PutObjectAutoDirect(ctx, mintedKey, content)
// storedKey: blocks/<org_id>/XX/XX/blockID.<lowercase-uuid>
```

`PutBlockData` remains the deterministic legacy convenience API. Canonical
writes that need a fresh physical incarnation use `MintStorageKey` and pass that
exact key to `PutObjectAutoDirect`.

There is no org-less `BlockStore` constructor. Manager-backed callers use
`GetBlockStoreForOrg` or `GetHealthyBlockStoreForOrg`; GC requires the persisted
class and key to already be canonical and never health-fails over a delete to
another backend.

Persisted `(storage_class, storage_key)` identity is byte-exact and is never
trimmed. A key must match either the legacy deterministic grammar or the minted
incarnation grammar above and must bind to the requested SHA-256 block. Fresh
rowless uploads carry the complete minted target through PUT and the target-aware
`InstallBlockMetadata` API. Existing canonical reuse/stub repair uses the stored
tuple through `UpsertBlockMetadata`; it does not mint. If the one-shot INSTALL
result is uncertain, SesameFS performs a bounded detached `SERIAL` settlement
read. An exact tuple proves Applied; a different complete tuple or no row proves
KnownLost and authorizes cleanup of only the attempted key; unavailable or
malformed settlement remains ambiguous and retains the object. A definite direct
CAS result that returns the proposed tuple is instead a single-use identity
contradiction: it grants no success or cleanup and is not retryable. Fresh targets
must pass strict minted-only locator validation before any provisional reference
or metadata mutation. This is the P2 contract, not a claim that the out-of-scope
R17/P3 repair design or durable reconciliation is complete.

**Save Types:**
- **Manual Save (Ctrl+S)**: Works with `forcesave: true` in config, sends status=6 callback
- **Auto-save on Close**: Always works, sends status=2 callback when document closes
- **Periodic Auto-save**: Requires OnlyOffice server config (`autoAssembly.enable: true`)

**Callback Status Codes:**
- `1` = Document being edited (no action needed)
- `2` = Document ready for saving (download and store)
- `4` = Document closed with no changes (cleanup doc_key)
- `6` = Force save / editing error

### File Tags

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api/v2.1/repos/:id/repo-tags/` | GET/POST | ✅ | Manage repo tags |
| `/api/v2.1/repos/:id/file-tags/` | GET/POST/DELETE | ✅ | Tag files |

### Batch Operations

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api/v2.1/repos/sync-batch-move-item/` | POST | ✅ | Sync move (same repo) |
| `/api/v2.1/repos/sync-batch-copy-item/` | POST | ✅ | Sync copy (same repo) |
| `/api/v2.1/repos/async-batch-move-item/` | POST | ✅ | Async move (cross repo) |
| `/api/v2.1/repos/async-batch-copy-item/` | POST | ✅ | Async copy (cross repo) |
| `/api/v2.1/copy-move-task/` | GET | ✅ | Query async task progress |
| `/api/v2.1/repos/batch-delete-item/` | DELETE | ✅ | Delete multiple files/folders |

### Groups & Sharing (Web UI Only - Not Used by Desktop Clients)

**For detailed status of all groups/sharing endpoints, see [IMPLEMENTATION_STATUS.md](IMPLEMENTATION_STATUS.md#rest-api---groups) lines 130-142.**

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api/v2.1/groups/` | GET | ✅ | List user's groups |
| `/api2/shared-repos/` | GET | ✅ | List libraries I shared to others |
| `/api2/beshared-repos/` | GET | ✅ | List libraries shared with me |
| `/api2/repos/:id/dir/shared_items` | GET/PUT/POST/DELETE | ✅ | Share files/folders to users/groups |
| `/api/v2.1/repos/:id/share-links/` | GET/POST/DELETE | ✅ | Manage public share links |
| `/api/v2.1/departments/` | GET | ⚠️ | List departments (stub returns empty) |

**Note:** Share links use repo-scoped URLs (`/repos/:id/share-links/`) which is MORE RESTful than Seafile's global endpoint. Desktop clients don't use these endpoints.

### Library Settings

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/api2/repos/:id/history-limit/` | GET/PUT | ✅ | Library history retention settings |
| `/api/v2.1/repos/:id/auto-delete/` | GET/PUT | ✅ | Auto-delete old files settings |
| `/api/v2.1/repos/:id/repo-api-tokens/` | GET/POST/PUT/DELETE | ✅ | Per-repo API tokens |

### File Viewer & Raw Access

| Endpoint | Method | Status | Description |
|----------|--------|--------|-------------|
| `/lib/:id/file/*path` | GET | ✅ | File viewer (OnlyOffice) |
| `/repo/:id/raw/*path` | GET | ❌ | Raw file access (images, etc.) |
| `/thumbnail/:id/:size/*path` | GET | ❌ | Image thumbnails |

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
