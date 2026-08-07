# Development Decisions - SesameFS

This document tracks key development decisions and the development approach for SesameFS.

**Last Updated**: 2026-02-01

---

## Development Approach

### ⚠️ CRITICAL: Protocol-Driven Development

SesameFS follows a **protocol-first** approach (different from typical frontend-driven projects):

```
1. Protocol Compliance First → Match Seafile protocol exactly
2. Verify Against Reference → Test every change against stock Seafile
3. Freeze When Verified → Lock protocol endpoints after client testing passes
4. Extend Cautiously → Add new features without breaking protocol
```

#### Why Protocol-Driven?

**Non-negotiable constraint**: Desktop/mobile client compatibility

- Desktop clients use **compiled protocol** (can't change like web API)
- Protocol defines requirements (we know what responses clients expect)
- Stock Seafile is the **source of truth** (documentation is incomplete/wrong)
- Breaking protocol = breaking **all** desktop/mobile clients (thousands of users)

**Key insight**: Unlike web APIs where you control both client and server, Seafile sync protocol is **frozen in compiled apps**. We must match it exactly.

---

## Stability Workflow (6 Steps)

### Step 1: Implement Protocol Endpoint

**Before writing code**:
- Read stock Seafile source code (if available): https://github.com/haiwen/seafile-server
- Check existing documentation: `docs/SEAFILE-SYNC-PROTOCOL-RFC.md`
- Review related test failures/errors from desktop client logs

**Implementation**:
- Follow Seafile behavior exactly (not what makes sense)
- Use correct field types (integer vs string vs boolean matters!)
- Match field ordering (affects hash computation)

**Example**:
```go
// ❌ WRONG - using boolean
response := map[string]interface{}{
    "is_corrupted": false,  // Desktop client expects integer!
}

// ✅ CORRECT - using integer
response := map[string]interface{}{
    "is_corrupted": 0,  // Desktop client requires integer 0
}
```

### Step 2: Compare Against Stock Seafile

**Run protocol comparison**:
```bash
cd docker/seafile-cli-debug
./run-sync-comparison.sh
```

This script:
1. Creates encrypted library on both servers (stock + local)
2. Uploads file via API to both
3. Calls all sync protocol endpoints on both
4. Compares JSON responses field-by-field
5. Reports any structural differences

**Expected output**:
```
Found 3 difference(s):
  download_info.token: different_values  # ✅ OK - different tokens expected
  commit.magic: different_values         # ✅ OK - different encryption keys
  commit.ctime: different_values         # ✅ OK - different timestamps
```

**❌ Unacceptable differences**:
- Missing fields
- Different field types (int vs string)
- Different response structure
- Wrong array vs object format

### Step 3: Test With Real Desktop Client

**Run real client sync**:
```bash
cd docker/seafile-cli-debug
./run-real-client-sync.sh
```

This script:
1. Creates encrypted library on both servers
2. Uploads file via API
3. Syncs library using **real seaf-cli** (actual desktop client)
4. Compares synced files byte-by-byte

**Expected output**:
```
✓ ALL FILES MATCH!

Synced files: {'test_file.txt'}

Both servers synced identical files via desktop client.
```

**If sync fails**:
1. Check client logs: `~/Seafile/.seafile-data/logs/seafile.log`
2. Common errors:
   - "Failed to inflate" → pack-fs data not zlib compressed
   - "Failed to find dir X" → fs_id not in fs-id-list
   - "Error when indexing" → fs_id hash mismatch (field ordering issue)

### Step 4: Fix Differences

**Stock Seafile is ALWAYS right**

Even if stock Seafile behavior seems wrong:
- Follow it exactly
- Document the quirk
- Don't try to "fix" it

**Example**: `no_local_history` field
- Documentation said it was required
- Stock Seafile doesn't include it
- We removed it from our implementation
- Desktop client now works ✅

### Step 5: Verify Again

Both tests must pass:
- ✅ `./run-sync-comparison.sh` - Only expected differences (tokens, timestamps)
- ✅ `./run-real-client-sync.sh` - Files sync successfully

### Step 6: Freeze and Document

1. **Mark as frozen** in `docs/IMPLEMENTATION_STATUS.md`:
   ```markdown
   | `GET /seafhttp/repo/:id/fs-id-list/` | 🔒 FROZEN | **STABLE** | 2026-01-16 | Returns JSON array |
   ```

2. **Document in RFC** if new behavior discovered:
   - Update `docs/SEAFILE-SYNC-PROTOCOL-RFC.md`
   - Add test vector if applicable
   - Update quick reference: `docs/SEAFILE-SYNC-PROTOCOL.md`

3. **Update CURRENT_WORK.md**:
   ```markdown
   ## Frozen/Stable Components 🔒
   - `internal/api/sync.go` lines 949-952 - fs-id-list format (JSON array)
   ```

4. **Add comment in code**:
   ```go
   // FROZEN (2026-01-16): Stock Seafile returns JSON array, not newline-separated text
   // Verified with run-sync-comparison.sh and run-real-client-sync.sh
   // DO NOT CHANGE without testing against stock Seafile
   c.JSON(http.StatusOK, fsIDs)
   ```

---

## Frontend Development (Secondary Priority)

Frontend follows **existing Seafile API** (not protocol-driven):

### Frontend Workflow

1. **API Endpoints Exist** - Backend implements Seafile REST API
2. **Use seafile-js Client** - Use existing Seafile frontend library (cannot modify it)
3. **Match Seafile UI Patterns** - Study Seahub (Seafile's frontend) for reference
4. **Test Integration** - Verify frontend works with backend

**Why frontend is secondary**:
- Users can use desktop clients without web UI
- Web UI changes don't break protocol compatibility
- More flexibility in implementation

**Frontend-specific issues**:
- Modal dialogs must use plain Bootstrap classes (not reactstrap Modal)
- Icon paths must match exact Seafile patterns
- seafile-js has hardcoded paths (can't change without forking)

---

## Architecture Decisions

### 1. Seafile Protocol Compatibility (CRITICAL)

**Decision**: Implement Seafile sync protocol v2 for desktop/mobile client compatibility

**Context**:
- Users need desktop client sync (non-negotiable requirement)
- Protocol is undocumented (must reverse-engineer from source code)
- Breaking changes = broken clients (can't update compiled apps)

**Consequences**:
- ✅ Desktop/mobile clients work out of the box
- ✅ Users can migrate from Seafile without changing workflow
- ❌ Locked into Seafile's quirks (e.g., SHA-1, weak PBKDF2)
- ❌ Must maintain compatibility layer forever

**Implementation**:
- Stock Seafile as reference (https://app.nihaoconsult.com)
- Automated protocol comparison testing
- Real client sync testing in Docker container

**Status**: 🔒 FROZEN (verified working 2026-01-16)

**Evidence**: Both `run-sync-comparison.sh` and `run-real-client-sync.sh` passing

---

### 2. SHA-1 → SHA-256 Block ID Translation

**Decision**: Accept SHA-1 block IDs from clients, translate to SHA-256 for storage

**Context**:
- Seafile clients use SHA-1 for block_id and fs_id (hardcoded in compiled apps)
- SHA-1 is cryptographically broken (collision attacks demonstrated)
- Need modern security without breaking clients

**Alternatives considered**:
1. ❌ Use SHA-1 everywhere → Security vulnerability
2. ❌ Force clients to upgrade → Can't change compiled apps
3. ✅ Translation layer → Best of both worlds

**Implementation**:
```
Client Request: block_id = SHA-1 (40 hex chars)
              ↓
Translation Table: external_id (SHA-1) → internal_id (SHA-256)
              ↓
S3 Storage: block stored with SHA-256 key (64 hex chars)
```

**Database**:
```sql
CREATE TABLE block_id_mappings (
    org_id UUID,
    representation_id TEXT,
    external_id TEXT,  -- SHA-1 (what clients use)
    internal_id TEXT,  -- SHA-256 (how we store)
    PRIMARY KEY ((org_id, representation_id, external_id))
);
```

**Consequences**:
- ✅ Client compatibility maintained
- ✅ Modern hash function for storage
- ✅ Can migrate to SHA-3 later without breaking clients
- ⚠️ Extra database lookup on block operations (mitigated: batch IN queries, 100/batch, since 2026-02-16)
- ❌ Storage overhead for mapping table

**Status**: ✅ COMPLETE (working since 2026-01-09, batch-optimized 2026-02-16)

---

### 3. Dual-Mode Encryption (PBKDF2 + Argon2id)

**Decision**: Validate passwords with PBKDF2 (Seafile compatibility), store with Argon2id (security)

**Context**:
- Seafile uses weak PBKDF2 with only 1000 iterations
- Desktop clients have hardcoded PBKDF2 implementation
- Modern standards require 600,000+ iterations (OWASP 2023)
- Can't change client encryption (compiled into apps)

**Alternatives considered**:
1. ❌ PBKDF2 only → Vulnerable to brute-force (1000 iterations is trivial)
2. ❌ Argon2id only → Breaks desktop client compatibility
3. ✅ Dual-mode → Compatibility + security

**Implementation**:
```go
// Client password verification (PBKDF2 for compatibility)
magic := DeriveKeyPBKDF2(password, repoID, salt, version)
if magic != library.Magic {
    return ErrWrongPassword
}

// Server-side storage (Argon2id for security)
argon2Hash := argon2id.Hash(password, library.Salt)
library.MagicStrong = argon2Hash  // 300× slower to brute-force
```

**Database columns**:
- `magic` - PBKDF2 hash (for desktop client compatibility)
- `magic_strong` - Argon2id hash (for server-side validation)
- `random_key` - PBKDF2-encrypted file key (for desktop clients)
- `random_key_strong` - Argon2id-encrypted file key (for web/API clients)

**Consequences**:
- ✅ Desktop client compatibility maintained
- ✅ 300× slower brute-force vs PBKDF2-only
- ✅ Future-proof (can add scrypt/bcrypt/etc.)
- ❌ Complexity: two password verification paths
- ❌ Storage: double the encrypted keys

**Security improvement**:
- PBKDF2 (1000 iterations): ~1 million attempts/sec on GPU
- Argon2id (64MB memory, 3 iterations): ~3,000 attempts/sec on GPU
- **Effective speed reduction**: 300×

**Status**: 🔒 FROZEN (verified 2026-01-13)

**Test vectors**: See `docs/SEAFILE-SYNC-PROTOCOL-RFC.md` Section 11.1

---

### 4. Cassandra for Metadata Storage

**Decision**: Use Apache Cassandra for metadata (not PostgreSQL/MySQL)

**Context**:
- Need multi-region replication (global deployment)
- Need horizontal scalability (millions of users)
- Seafile uses SQLite/MySQL (not suitable for distributed systems)

**Alternatives considered**:
1. PostgreSQL + logical replication → Complex, master-slave only
2. MySQL + Galera Cluster → Works but limited scalability
3. CockroachDB → Good but expensive licensing
4. **Cassandra** → Proven at scale, open-source, multi-region native

**Tradeoffs**:

**Advantages**:
- ✅ Multi-region replication built-in
- ✅ Horizontal scaling (add nodes without downtime)
- ✅ High availability (no single point of failure)
- ✅ Tunable consistency (per-query)
- ✅ Proven at scale (Netflix, Apple, Discord)

**Disadvantages**:
- ❌ No JOINs (must denormalize data)
- ❌ No transactions across partitions
- ❌ Learning curve for operators
- ❌ More complex queries (CQL vs SQL)

**Schema design patterns**:
```sql
-- Query-driven design (not normalization-driven)
-- Each table optimized for specific query pattern

-- Example: Get all libraries for user
CREATE TABLE libraries_by_owner (
    user_id UUID,
    repo_id UUID,
    repo_name TEXT,
    created_at TIMESTAMP,
    PRIMARY KEY (user_id, created_at, repo_id)
) WITH CLUSTERING ORDER BY (created_at DESC);

-- Example: Get library by ID
CREATE TABLE libraries (
    org_id UUID,
    repo_id UUID,
    repo_name TEXT,
    owner_id UUID,
    PRIMARY KEY (org_id, repo_id)
);
```

**Status**: ✅ COMPLETE (schema finalized)

**Documentation**: See `docs/DATABASE-GUIDE.md`

---

### 5. S3-Compatible Block Storage

**Decision**: Use S3-compatible storage (AWS S3, MinIO, R2) for file blocks

**Context**:
- Seafile uses custom block storage on local filesystem
- Need cloud-native, scalable storage
- Need multi-region replication
- Need cost-effective scaling

**Alternatives considered**:
1. Local filesystem → Not scalable, no replication
2. Custom distributed storage → Reinventing the wheel
3. **S3-compatible** → Industry standard, battle-tested

**Implementation**:
```go
// Block storage interface (can swap backends)
type BlockStorage interface {
    Get(ctx context.Context, key string) ([]byte, error)
    Put(ctx context.Context, key string, data []byte) error
    Delete(ctx context.Context, key string) error
}

// S3 implementation
type S3Storage struct {
    client *s3.Client
    bucket string
}
```

**Supported backends**:
- AWS S3 (production)
- MinIO (local development)
- Cloudflare R2 (cost-effective alternative)
- Any S3-compatible API

**Consequences**:
- ✅ Industry standard (vast ecosystem)
- ✅ Built-in redundancy (99.999999999% durability)
- ✅ Cost-effective scaling (pay for what you use)
- ✅ Multi-region replication (S3 Cross-Region Replication)
- ✅ Lifecycle policies (archive old blocks to Glacier)
- ❌ Not compatible with Seafile's block storage (migration needed)
- ⚠️ API call overhead (mitigated: custom HTTP transport with 64 conn/host, prefetch pipeline since 2026-02-16)

**Status**: ✅ COMPLETE (working, transport-optimized 2026-02-16)

---

### 6. Server-Side Chunking (FastCDC)

**Decision**: Use server-side FastCDC for web uploads, client-side Rabin CDC for desktop sync

**Context**:
- Desktop clients use hardcoded Rabin CDC (256KB-4MB chunks)
- Web uploads can use modern chunking algorithms
- Larger chunks = fewer S3 API calls = lower cost
- Content-defined chunking = better deduplication

**Implementation**:

**Desktop sync path** (protocol-driven):
```
Desktop Client
  ↓ Rabin CDC chunking (256KB-4MB)
  ↓ SHA-1 block ID
  ↓ PUT /seafhttp/repo/:id/block/:sha1
Server
  ↓ SHA-1 → SHA-256 translation
  ↓ Store to S3 with SHA-256 key
```

**Web upload path** (server-driven):
```
Browser
  ↓ POST entire file
  ↓ /seafhttp/upload-api/:token
Server
  ↓ FastCDC chunking (2-256MB adaptive)
  ↓ SHA-256 block ID
  ↓ Store to S3 with SHA-256 key
```

**Adaptive chunk sizing**:
```go
// Small files: 2-8 MB chunks (minimize overhead)
// Large files: 64-256 MB chunks (reduce API calls)
chunkSize := AdaptiveChunkSize(fileSize)
```

**Consequences**:
- ✅ Desktop client compatibility (Rabin CDC preserved)
- ✅ Web upload optimization (larger chunks, fewer API calls)
- ✅ Better deduplication (content-defined boundaries)
- ✅ Cost savings (fewer S3 PUT operations)
- ❌ Two code paths (Rabin for sync, FastCDC for web)
- ❌ Block ID mismatch (desktop vs web uploads of same file)

**Deduplication note**: Blocks are still deduplicated globally. If desktop client uploads a file, web client downloading same file will reuse blocks (via fs_object metadata).

**Status**: ✅ COMPLETE

---

### 7. No Redis/NATS (Cassandra-Only)

**Decision**: Use Cassandra for everything (no separate cache/queue services)

**Context**:
- Typical architecture: PostgreSQL + Redis + NATS/RabbitMQ
- Each service adds operational complexity
- Cassandra can handle caching and queuing

**Alternatives considered**:
1. PostgreSQL + Redis + NATS → Standard but complex
2. **Cassandra only** → Simpler deployment
3. Add Redis/NATS later if needed → Progressive complexity

**Implementation**:

**Caching** (using Cassandra TTL):
```sql
CREATE TABLE cache_entries (
    cache_key TEXT PRIMARY KEY,
    value BLOB,
    expires_at TIMESTAMP
) WITH default_time_to_live = 3600;
```

**Task queue** (using Cassandra as queue):
```sql
CREATE TABLE task_queue (
    queue_name TEXT,
    task_id UUID,
    created_at TIMESTAMP,
    task_data BLOB,
    status TEXT,
    PRIMARY KEY (queue_name, created_at, task_id)
) WITH CLUSTERING ORDER BY (created_at ASC);
```

**Consequences**:
- ✅ Simpler deployment (fewer services)
- ✅ Fewer moving parts (easier to operate)
- ✅ One database to backup/monitor/scale
- ✅ Can add Redis later if benchmarks show need
- ❌ Cassandra queries more complex than Redis GET/SET
- ❌ Less optimal for high-frequency cache access
- ❌ Queue semantics not native (need app-level logic)

**When to add Redis**:
- Cache hit ratio < 80%
- Cache latency > 50ms p99
- Queue throughput > 10,000 jobs/sec

**Status**: ✅ COMPLETE (cache_entries and task_queue tables exist)

**Future**: Can add Redis as cache layer without schema changes

---

## Testing Decisions

### 1. Automated Protocol Verification

**Decision**: Every sync protocol change must pass automated comparison against stock Seafile

**Context**:
- Protocol bugs break desktop clients (high severity)
- Manual testing misses edge cases (field types, ordering)
- Stock Seafile is source of truth (documentation is incomplete)

**Tools**:

**`run-sync-comparison.sh`** - API-level protocol comparison
- Creates libraries on both servers
- Calls all sync endpoints
- Compares JSON responses field-by-field
- Reports structural differences

**`run-real-client-sync.sh`** - Real desktop client test
- Uses actual seaf-cli (Seafile desktop client)
- Syncs files from both servers
- Compares synced files byte-by-byte
- Verifies end-to-end functionality

**Process**:
```bash
# Before freezing any sync protocol changes
cd docker/seafile-cli-debug
./run-sync-comparison.sh    # Must pass (only token/timestamp diffs)
./run-real-client-sync.sh    # Must pass (files match)
```

**Consequences**:
- ✅ Catches protocol bugs before deployment
- ✅ Prevents desktop client breakage
- ✅ Documents expected behavior
- ✅ Enables confident refactoring
- ❌ Test setup complexity (Docker, Python, seaf-cli)
- ❌ Requires stock Seafile credentials

**Status**: ✅ COMPLETE (tools working, both tests passing)

---

### 2. Test Vectors for Cryptography

**Decision**: Generate verified test vectors for all crypto operations

**Context**:
- Crypto bugs are critical (data loss, security vulnerability)
- Independent implementations need verification
- RFC specification requires test vectors

**Implementation**:

**Test vector generator** (`scripts/generate_test_vectors.py`):
```python
def compute_magic(repo_id, password):
    """Compute magic for password verification"""
    salt = bytes([0xda, 0x90, 0x45, 0xc3, 0x06, 0xc7, 0xcc, 0x26])
    input_data = (repo_id + password).encode('utf-8')
    key = pbkdf2_sha256(input_data, salt, 1000, 32)
    iv = pbkdf2_sha256(key, salt, 10, 16)
    magic = binascii.hexlify(key) + binascii.hexlify(iv)
    return magic

# Test Vector 1: PBKDF2
repo_id = "00000000-0000-0000-0000-000000000000"
password = "password"
expected_magic = "7b936d1d...1311b21e..."  # Known correct value
```

**Verification process**:
1. Generate test vectors with Python (using PyCrypto)
2. Verify against Go implementation
3. Document in RFC (Section 11)
4. Use for regression testing

**Consequences**:
- ✅ Catches crypto implementation bugs
- ✅ Enables independent implementations
- ✅ Documents expected behavior
- ✅ Prevents regression
- ❌ Requires maintaining test vector generator

**Status**: ✅ COMPLETE (test vectors in RFC Section 11)

---

## Documentation Decisions

### 1. RFC-Style Specification

**Decision**: Maintain formal RFC-style specification for Seafile sync protocol

**Context**:
- Seafile protocol is undocumented
- Desktop client source code is complex C code
- Need authoritative reference for independent implementations

**Implementation**: `docs/SEAFILE-SYNC-PROTOCOL-RFC.md`

**Structure** (following RFC 2119):
- Abstract
- Requirements language (MUST/SHOULD/MAY)
- Protocol overview
- Message formats (with ABNF)
- Binary formats (exact byte layout)
- Encryption specifications
- Test vectors
- Conformance requirements
- Security considerations

**Consequences**:
- ✅ Complete technical specification
- ✅ Independent implementations possible
- ✅ Authoritative reference (vs incomplete docs)
- ✅ Test vectors for verification
- ❌ High maintenance effort (keep in sync with code)

**Status**: ✅ COMPLETE (950+ lines, test vectors verified)

---

### 2. Session Continuity Tracking

**Decision**: Maintain `CURRENT_WORK.md` for session-to-session context preservation

**Context**:
- AI assistant loses context between sessions
- Repeating same explanations wastes time
- Need to track what's frozen vs unstable
- Need to handoff work between sessions

**Implementation**:

**`CURRENT_WORK.md`** structure:
```markdown
## What Was Just Completed ✅
- Session accomplishments

## What's Next (Priority Order) 🎯
- Prioritized task list

## Known Issues 🐛
- Bug tracker

## Frozen/Stable Components 🔒
- What NOT to touch

## Context for Next Session 📝
- Critical facts to remember
- Files modified this session
- Testing locations
```

**Update process**:
- Update at **end of every session**
- Move completed items from "What's Next" to "What Was Just Completed"
- Add new known issues
- Update frozen components list

**Consequences**:
- ✅ Faster session startup (read CURRENT_WORK.md first)
- ✅ No repeated explanations
- ✅ Clear handoff between sessions
- ✅ Tracks what's safe to modify
- ❌ Requires discipline to update

**Status**: ✅ COMPLETE

---

## Rejected Decisions

### ❌ Frontend-Driven Development

**Decision**: Do NOT follow frontend-driven approach (unlike Trove project)

**Reason**: SesameFS is protocol-driven, not feature-driven
- Desktop clients define requirements (not UI mockups)
- Protocol is frozen (compiled into apps)
- Frontend is secondary (users can use desktop clients only)

**What we do instead**: Protocol-driven development (see top of document)

---

### ❌ Use Seafile's Block Storage Format

**Decision**: Do NOT use Seafile's filesystem-based block storage

**Reason**:
- Not cloud-native (requires local filesystem)
- No built-in replication
- Not cost-effective at scale
- S3 is industry standard

**Tradeoff**: Need migration path from Seafile (accept for now)

---

### ❌ Fix Seafile's "Quirks"

**Decision**: Do NOT "improve" Seafile protocol (even when it seems wrong)

**Example**: `no_local_history` field
- Documentation said it was required
- Seemed logical to include it
- Stock Seafile doesn't include it
- **Decision**: Follow stock Seafile exactly

**Reason**: Desktop clients are the source of truth, not our assumptions

---

## Proposed Decisions

### Generational GC Fence For X1/X2

**Status:** Proposed; implementation not started

**Decision record:** [GC-X1-X2-GENERATION-FENCE-ADR.md](./GC-X1-X2-GENERATION-FENCE-ADR.md)

The proposed greenfield design keeps writer pins and ordinary references at
`LOCAL_QUORUM`, uses `SERIAL` plus `EACH_QUORUM` only for destructive GC fences and
final checks, and prevents physical-delete ABA with UUID generations and immutable
storage keys. X1 and X2 remain open, and destructive GC remains disabled until the
ADR acceptance criteria are implemented and verified.

---

## Future Decisions (To Be Made)

### Multi-Region Replication Strategy
- **Options**: Active-active vs active-passive
- **Tradeoffs**: Consistency vs availability
- **Dependencies**: Cassandra multi-DC setup
- **Timeline**: After v1.0 launch

### OIDC Provider Support
- **Options**: Keycloak, Auth0, Okta, Google, etc.
- **Decision needed**: Which to support first?
- **Timeline**: Before v1.0 launch

### Version History Storage
- **Options**: S3 Glacier, S3 Infrequent Access, keep in standard S3
- **Decision needed**: Retention policy defaults
- **Timeline**: After basic version history implemented

### Conflict Resolution Strategy
- **Options**: Last-write-wins, versioning, manual merge
- **Decision needed**: For multi-region writes
- **Timeline**: After multi-region replication

---

## References

- **Stock Seafile**: https://app.nihaoconsult.com (protocol reference)
- **Seafile Source**: https://github.com/haiwen/seafile
- **Seafile Server Source**: https://github.com/haiwen/seafile-server
- **Testing Framework**: `docker/seafile-cli-debug/`
- **RFC 2119**: Key words for requirement levels (MUST/SHOULD/MAY)
