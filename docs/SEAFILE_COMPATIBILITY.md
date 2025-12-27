# Seafile API Compatibility

SesameFS implements a Seafile-compatible API for file upload and download operations. This document explains the implementation and its compatibility with Seafile clients.

## Overview

SesameFS provides API endpoints that are compatible with Seafile's Web API, allowing Seafile clients to work with SesameFS servers. The implementation follows Seafile's two-step upload/download pattern where:

1. Client requests an access URL from the API
2. Client performs the file operation using that URL
3. Server proxies the operation to the backend storage (S3/MinIO)

## API Endpoints

### Upload Flow

#### Step 1: Get Upload Link

```
GET /api/v2/repos/{repo_id}/upload-link/?p={parent_dir}
Authorization: Token {api_token}
```

**Response:**
```
http://server:8080/seafhttp/upload-api/{upload_token}
```

#### Step 2: Upload File

```
POST /seafhttp/upload-api/{upload_token}
Content-Type: multipart/form-data

file: (binary)
parent_dir: /path/to/parent
replace: 0 or 1
```

**Response (with `?ret-json=1`):**
```json
[
  {
    "name": "filename.txt",
    "id": "file_id_hash",
    "size": 1234
  }
]
```

**Response (without `ret-json`):**
```
file_id_hash
```

### Download Flow

#### Step 1: Get Download Link

```
GET /api/v2/repos/{repo_id}/file/download-link?p={file_path}
Authorization: Token {api_token}
```

**Response:**
```
http://server:8080/seafhttp/files/{download_token}/{filename}
```

#### Step 2: Download File

```
GET /seafhttp/files/{download_token}/{filename}
```

**Response:** Binary file content with appropriate headers.

## Architecture

```
┌─────────────────┐      ┌─────────────────┐      ┌─────────────────┐
│  Seafile Client │ ───▶ │   SesameFS API  │ ───▶ │   S3 / MinIO    │
│  (or any HTTP)  │      │   (Go + Gin)    │      │   (Backend)     │
└─────────────────┘      └─────────────────┘      └─────────────────┘
         │                       │
         │  1. GET upload-link   │
         │◀─────────────────────│
         │  Returns: seafhttp URL│
         │                       │
         │  2. POST to seafhttp  │
         │──────────────────────▶│
         │                       │ ──▶ Upload to S3
         │  Returns: file ID     │
         │◀─────────────────────│
```

## Token Management

SesameFS uses temporary tokens to secure file transfer operations. When a client requests to upload or download a file, the API generates a short-lived token that grants access to that specific operation.

### How It Works

1. **Client requests access**: Client calls `/api/v2/repos/{repo_id}/upload-link` or `/api/v2/repos/{repo_id}/file/download-link`
2. **Server generates token**: A random token is created with metadata (org, repo, path, user, expiration)
3. **Client receives URL**: The URL contains the token (e.g., `/seafhttp/upload-api/{token}`)
4. **Client performs operation**: The token is validated before allowing the file transfer
5. **Token expires or is consumed**: Upload tokens are single-use; all tokens expire after TTL

### Token Types

| Type | Purpose | Usage |
|------|---------|-------|
| **Upload token** | Grants permission to upload a file to a specific path | Single-use (deleted after upload completes) |
| **Download token** | Grants permission to download a specific file | Reusable until expiration |

### Token TTL (Time-To-Live)

The TTL determines how long a token remains valid after creation. This is a security measure that limits the window during which a token can be used.

**Default**: 1 hour

**Why it matters**:
- **Too short**: Users may get "token expired" errors if uploads/downloads take time to start
- **Too long**: Increases security risk if a token URL is leaked or intercepted
- **Recommended**: 1 hour is a good balance for most use cases

**Configuration**:

```bash
# Environment variable (Go duration format)
SEAFHTTP_TOKEN_TTL=1h      # 1 hour (default)
SEAFHTTP_TOKEN_TTL=30m     # 30 minutes
SEAFHTTP_TOKEN_TTL=2h      # 2 hours
```

```yaml
# config.yaml
seafhttp:
  token_ttl: 1h
```

### Security Considerations

- Tokens are stored in-memory and automatically cleaned up after expiration
- Each token is cryptographically random (128-bit)
- Tokens are scoped to a specific organization, repository, and file path
- Upload tokens are invalidated immediately after use

## Key Differences from Seafile

| Feature | Seafile | SesameFS |
|---------|---------|----------|
| Backend Storage | Custom block storage | S3-compatible (AWS S3, MinIO) |
| Database | SQLite/MySQL | Apache Cassandra |
| Chunking | Custom CDC | FastCDC (planned) |
| Sync Protocol | Proprietary | **Implemented** (Desktop client compatible) |
| Authentication | Built-in + LDAP | OIDC + Dev tokens |

## Sync Protocol (`/seafhttp/`)

SesameFS implements the Seafile sync protocol, allowing Seafile desktop clients to sync files directly.

### Supported Endpoints

```
/seafhttp/protocol-version          GET     Protocol version (returns {"version": 2})
/seafhttp/repo/head-commits-multi   POST    Check multiple repos at once
/seafhttp/repo/:repo_id/
├── /commit/HEAD                    GET     Get HEAD commit ID
├── /commit/HEAD?head=<id>          PUT     Update HEAD pointer
├── /commit/:commit_id              GET     Get commit details
├── /commit/:commit_id              PUT     Store commit
├── /block/:block_id                GET     Download block
├── /block/:block_id                PUT     Upload block
├── /check-blocks/                  POST    Check which blocks exist
├── /fs-id-list/                    GET     List FS object IDs (JSON array)
├── /fs/:fs_id                      GET     Get FS object
├── /pack-fs/                       POST    Pack multiple FS objects
├── /recv-fs/                       POST    Receive FS objects (binary format)
├── /check-fs/                      POST    Check which FS objects exist
├── /permission-check/              GET     Check permissions (empty body = OK)
├── /quota-check/                   GET     Check quota
└── /update-branch/                 POST    Update branch head
```

### Critical Format Requirements

These format details are essential for Seafile client compatibility:

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

### Single-Port Architecture

Unlike Seafile which uses multiple ports (8000 for API, 8082 for seafhttp), SesameFS runs everything on a single port (default 8080). The `relay_port` in download-info responses must match the actual server port.

## Configuration

### Environment Variables

```bash
# S3 Backend
S3_ENDPOINT=http://localhost:9000  # MinIO endpoint
S3_BUCKET=sesamefs-blocks
AWS_ACCESS_KEY_ID=minioadmin
AWS_SECRET_ACCESS_KEY=minioadmin
AWS_REGION=us-east-1

# Server URL (for generating seafhttp URLs)
SERVER_URL=http://localhost:8080

# Token TTL (how long upload/download tokens are valid)
# Format: Go duration string (e.g., "1h", "30m", "2h30m")
SEAFHTTP_TOKEN_TTL=1h
```

### Config File (config.yaml)

```yaml
server:
  port: ":8080"

storage:
  default_class: hot
  backends:
    hot:
      type: s3
      endpoint: "http://localhost:9000"
      bucket: sesamefs-blocks
      region: us-east-1

seafhttp:
  token_ttl: 1h  # How long upload/download tokens are valid
```

## Testing with curl

### Complete Upload/Download Flow

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

## Implementation Files

- `internal/api/seafhttp.go` - SeafHTTP handlers and token management
- `internal/api/v2/files.go` - File API endpoints (upload-link, download-link)
- `internal/api/server.go` - Server setup and route registration
- `internal/storage/s3.go` - S3 backend implementation

## Future Enhancements

- [ ] Streaming for large files (avoid loading into memory)
- [ ] Resumable uploads
- [ ] Multi-part upload support
- [ ] Download token reuse tracking
- [ ] Rate limiting per token
