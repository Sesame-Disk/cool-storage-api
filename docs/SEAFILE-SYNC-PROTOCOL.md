# Seafile Desktop Client Sync Protocol

**Version**: 2
**Last Verified**: 2026-05-20
**Method**: Automated comparison against production Seafile (app.nihaoconsult.com) + comprehensive sync testing

This document describes the Seafile sync protocol as verified through direct protocol comparison and real desktop client testing.

## Testing Your Implementation

```bash
cd docker/seafile-cli-debug

# 1. Quick protocol check (API-level comparison)
./run-sync-comparison.sh

# 2. Real desktop client sync test (single library)
./run-real-client-sync.sh

# 3. Comprehensive sync protocol tests (recommended)
./run-comprehensive-with-proxy.sh --quick              # Fast: 4 scenarios, ~3 min
./run-comprehensive-with-proxy.sh --test-all           # Full: 7 scenarios, ~15 min
./run-comprehensive-with-proxy.sh --test-scenario nested_folders  # Specific scenario
```

**All tests must pass for desktop client compatibility.**

From the repository root, run `bash ./scripts/test-sync-active-active.sh` to
exercise two real `seaf-cli` clients against different backend nodes and prove
active-active convergence for concurrent `PUT /seafhttp/repo/{id}/commit/HEAD`
contention.

**What gets tested:**
- ✅ Single file sync
- ✅ Multiple files sync (10 files)
- ✅ Nested folder structures (5 files in subdirectories)
- ✅ Medium files (1-5 MB, chunking verification)
- ✅ Large files (50 MB, block verification)
- ✅ Many tiny files (50 files, performance)
- ✅ Mixed content (various file sizes and folder depths)

The comprehensive proxy suite above is focused on single-client compatibility.
Active-active conflict recovery under multi-node contention is now covered by
the real desktop proof in `scripts/test-sync-active-active.sh`.

See `COMPREHENSIVE_TESTING.md` for detailed usage.

---

## Critical Protocol Requirements

**⚠️ These cause desktop client sync failures if incorrect:**

### 1. Field Type Requirements

| Endpoint | Field | Required Type | ❌ Wrong Type |
|----------|-------|---------------|---------------|
| `GET /api2/repos/{id}/download-info/` | `encrypted` | integer `0` or `1` | boolean |
| `GET /api2/repos/{id}/download-info/` | `salt` | string `""` | null |
| `GET /seafhttp/repo/{id}/commit/HEAD` | `is_corrupted` | integer `0` | boolean |
| `GET /seafhttp/repo/{id}/commit/{id}` | `encrypted` | string `"true"` | integer or boolean |
| `GET /seafhttp/repo/{id}/fs-id-list/` | response | JSON array | newline-separated text |

### 2. Authentication Headers

| Context | Header | Value |
|---------|--------|-------|
| REST API (`/api2/`, `/api/v2.1/`) | `Authorization` | `Token {api_token}` |
| Sync Protocol (`/seafhttp/`) | `Seafile-Repo-Token` | `{sync_token}` from download-info |

### 3. Encrypted Library Creation

When client sends `POST /api2/repos/` with:
```
name=Library&passwd=Password123
```

Server **MUST**:
- Detect `passwd` parameter → create encrypted library
- Return `encrypted: 1` (integer), `enc_version: 2` (integer)
- Generate `magic` (64 hex) and `random_key` (96 hex)

❌ **Common error**: Ignoring `passwd` parameter, creating unencrypted library

### 4. Commit Object Format (Encrypted)

```json
{
  "commit_id": "...",
  "root_id": "...",
  "encrypted": "true",
  "enc_version": 2,
  "magic": "...",
  "key": "...",
  "repo_desc": "",
  "repo_category": ""
}
```

**Critical:**
- `encrypted` is **string** `"true"` (not integer `1`)
- `repo_desc` is empty string `""` (not null)
- `repo_category` is empty string `""` (not null)
- **Do NOT include** `no_local_history` field (stock Seafile omits it)

### 5. fs-id-list Format

**Must return JSON array:**
```json
["4702e8382675a4a062cb49cc5dc175c093f7effe", "95957597b0480add4f18d2c5e5a57905cd645f54"]
```

❌ **Wrong**: Newline-separated text format

---

## Core Endpoints

### Protocol Version

```
GET /seafhttp/protocol-version
```

**Response:**
```json
{"version": 2}
```

### Get Sync Token

```
GET /api2/repos/{repo_id}/download-info/
Authorization: Token {api_token}
```

**Response (Encrypted Library):**
```json
{
  "token": "25979dd59d1b4ea95aa1c86028db4b0ad7498e61",
  "repo_id": "256b7b88-d9cf-44d1-ba46-a5bb0bf0ebf7",
  "encrypted": 1,
  "enc_version": 2,
  "salt": "",
  "magic": "eee7b4a7a2539f2e4fb1c88a40121077152a5dc2c223f607f8b5e0838affde61",
  "random_key": "406ec194a0d0f7985b831b040034d829a4c68fbd354982f58101d0b6edd0232efed903cd1c404d0a66892473be968f19",
  "head_commit_id": "0342dc93ceca5acc782059a13347ef249055ea12"
}
```

**Critical:**
- `token` → Use as `Seafile-Repo-Token` header for all `/seafhttp/` calls
- `encrypted` is integer `1` (in download-info)
- `salt` is empty string `""` for enc_version 2

### Get HEAD Commit

```
GET /seafhttp/repo/{repo_id}/commit/HEAD
Seafile-Repo-Token: {sync_token}
```

**Response:**
```json
{
  "is_corrupted": 0,
  "head_commit_id": "268281145ad162113359a04cefe1bf806fb8a5c9"
}
```

### Get Commit Object

```
GET /seafhttp/repo/{repo_id}/commit/{commit_id}
Seafile-Repo-Token: {sync_token}
```

**Response (Encrypted):**
```json
{
  "commit_id": "0342dc93ceca5acc782059a13347ef249055ea12",
  "repo_id": "256b7b88-d9cf-44d1-ba46-a5bb0bf0ebf7",
  "root_id": "87199e6b76b84e56eef6b572bffdaa1067556489",
  "creator_name": "user@example.com",
  "creator": "0000000000000000000000000000000000000000",
  "description": "Added file.txt",
  "ctime": 1768225934,
  "parent_id": "8125065ad7abe077c1b1dcfbb4f491eb41987415",
  "second_parent_id": null,
  "repo_name": "Encrypted Library",
  "repo_desc": "",
  "repo_category": "",
  "encrypted": "true",
  "enc_version": 2,
  "magic": "eee7b4a7a2539f2e4fb1c88a40121077152a5dc2c223f607f8b5e0838affde61",
  "key": "406ec194a0d0f7985b831b040034d829a4c68fbd354982f58101d0b6edd0232efed903cd1c404d0a66892473be968f19",
  "version": 1
}
```

**Critical:**
- `encrypted` is **string** `"true"` (different type than download-info!)
- Include `repo_desc` and `repo_category` as empty strings
- Do NOT include `no_local_history`

### Get FS Object IDs

```
GET /seafhttp/repo/{repo_id}/fs-id-list/?server-head={commit_id}
Seafile-Repo-Token: {sync_token}
```

**Response:**
```json
["4702e8382675a4a062cb49cc5dc175c093f7effe", "95957597b0480add4f18d2c5e5a57905cd645f54"]
```

Returns JSON array of all FS object IDs (directories and files) in the commit.

### Download FS Objects

```
POST /seafhttp/repo/{repo_id}/pack-fs
Seafile-Repo-Token: {sync_token}
Content-Type: application/json

["4702e8382675a4a062cb49cc5dc175c093f7effe", "95957597b0480add4f18d2c5e5a57905cd645f54"]
```

**Response:** Binary format
```
[40-byte fs_id][4-byte size BE][zlib-compressed JSON]
[40-byte fs_id][4-byte size BE][zlib-compressed JSON]
...
```

**FS Object Types:**

**Directory (type: 3):**
```json
{
  "dirents": [
    {
      "id": "534d4ba7a4a21939cf5bb4db7962d74e4f2b483a",
      "mode": 33188,
      "modifier": "user@example.com",
      "mtime": 1768543179,
      "name": "file.txt",
      "size": 8
    }
  ],
  "type": 3,
  "version": 1
}
```

**File (type: 1):**
```json
{
  "block_ids": ["9bc34549d565d9505b287de0cd20ac77be1d3f2c"],
  "size": 8,
  "type": 1,
  "version": 1
}
```

**Critical:**
- FS object JSON must be zlib-compressed
- `fs_id` = SHA-1 hash of decompressed JSON
- Directory entries must include `modifier` field
- Directory entry fields in alphabetical order: `id`, `mode`, `modifier`, `mtime`, `name`, `size`

### Check FS Objects

```
POST /seafhttp/repo/{repo_id}/check-fs
Seafile-Repo-Token: {sync_token}
Content-Type: application/json

["fs_id1", "fs_id2"]
```

**Response:**
```json
["fs_id_that_does_not_exist"]
```

Returns array of fs_ids that **do NOT exist** on server. Empty array `[]` means all exist.

### Upload Blocks

```
POST /seafhttp/repo/{repo_id}/check-blocks
Seafile-Repo-Token: {sync_token}
Content-Type: application/json

["block_id1", "block_id2"]
```

**Response:**
```json
["block_id_that_needs_upload"]
```

Returns array of block_ids that **need to be uploaded**. Empty array `[]` means all exist.

```
PUT /seafhttp/repo/{repo_id}/block/{block_id}
Seafile-Repo-Token: {sync_token}

{binary block data}
```

### Download Blocks

```
GET /seafhttp/repo/{repo_id}/block/{block_id}
Seafile-Repo-Token: {sync_token}
```

**Response:** Raw block data (encrypted if library is encrypted)

---

## Encryption Specifications

### Password Derivation (PBKDF2)

**Magic (password verification):**
```
input = repo_id + password
key = PBKDF2-SHA256(input, salt, 1000 iterations, 32 bytes)
iv = PBKDF2-SHA256(key, salt, 10 iterations, 16 bytes)
magic = hex(key) + hex(iv) = 64 hex chars
```

**Random Key Encryption:**
```
input = password  # NOT repo_id + password
encKey = PBKDF2-SHA256(input, salt, 1000 iterations, 32 bytes)
encIV = PBKDF2-SHA256(encKey, salt, 10 iterations, 16 bytes)
secretKey = random 48 bytes
randomKey = AES-256-CBC-Encrypt(secretKey, encKey, encIV)
randomKey_hex = 96 hex chars
```

**Static Salt (enc_version 2):**
```
{0xda, 0x90, 0x45, 0xc3, 0x06, 0xc7, 0xcc, 0x26}
```

### Block Encryption

**For encrypted libraries:**
```
encryptedBlock = [16-byte IV][AES-256-CBC encrypted content with PKCS7 padding]
```

**File key derivation:**
```
fileKey = secretKey[:32]  # First 32 bytes of decrypted random_key
fileIV = secretKey[32:]   # Last 16 bytes of decrypted random_key
```

---

## Sync Flow

```
1. GET /api2/repos/{id}/download-info/
   → Get sync token

2. GET /seafhttp/repo/{id}/commit/HEAD
   → Get current HEAD commit_id

3. GET /seafhttp/repo/{id}/commit/{commit_id}
   → Get commit object with root_id

4. GET /seafhttp/repo/{id}/fs-id-list/?server-head={commit_id}
   → Get all FS object IDs

5. POST /seafhttp/repo/{id}/pack-fs
   → Download FS objects
   → Parse pack-fs binary format
   → Decompress each FS object
   → Verify SHA-1 hash matches fs_id

6. For each file FS object:
   POST /seafhttp/repo/{id}/check-blocks
   → Check which blocks need download

   GET /seafhttp/repo/{id}/block/{block_id}
   → Download missing blocks
   → If encrypted: decrypt using file key
   → Reconstruct file from blocks
```

---

## Validation Checklist

### Automated Tests (REQUIRED)
- [ ] `./run-sync-comparison.sh` shows no protocol differences
- [ ] `./run-real-client-sync.sh` successfully syncs files
- [ ] All field types match (int vs string)
- [ ] fs-id-list returns JSON array (not newline-separated)
- [ ] Commit objects omit `no_local_history` field
- [ ] `repo_desc`/`repo_category` are `""` not null
- [ ] `Seafile-Repo-Token` header works

### Manual Tests
- [ ] Seafile desktop client syncs encrypted libraries
- [ ] Files uploaded via OnlyOffice sync to client
- [ ] Wrong password returns error (not crash)
- [ ] Large files (>1GB) sync successfully

---

## Common Errors

### "Error when indexing"
**Cause**: fs_id hash mismatch
**Fix**: Ensure directory entries include `modifier` field in correct alphabetical order

### "Failed to find dir X in repo Y"
**Cause**: root_fs_id not in fs-id-list response
**Fix**: Ensure fs-id-list includes ALL fs_ids recursively

### "Failed to inflate"
**Cause**: pack-fs data not zlib compressed
**Fix**: Compress each FS object JSON with zlib before returning

### "token is null"
**Cause**: Using wrong authentication header
**Fix**: Use `Seafile-Repo-Token` header (not `Authorization`) for `/seafhttp/` endpoints

### Desktop client shows library but no files
**Cause**: Protocol responses correct but commit field missing
**Fix**: Check commit includes `repo_desc: ""` and `repo_category: ""`

---

## Implementation Notes

1. **Magic and random_key are repo-specific** - Different for each library even with same password
2. **Type inconsistency is intentional** - `encrypted` is string in commits, integer in download-info
3. **Empty string vs null matters** - `repo_desc: ""` works, `repo_desc: null` breaks client
4. **Field order matters for fs_id** - Directory entries must be alphabetically ordered
5. **SHA-1 is required** - Even though weak, protocol uses SHA-1 for fs_id and block_id

---

## References

- Production Seafile Server: https://app.nihaoconsult.com
- Testing Framework: `docker/seafile-cli-debug/`
- Seafile Source: https://github.com/haiwen/seafile
- Desktop Client Source: https://github.com/haiwen/seafile-client
