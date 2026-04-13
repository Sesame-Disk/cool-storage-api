# Security Assessment v2 — 2026-04-13

**Date:** 2026-04-13
**Scope:** Full reassessment of sesamefs backend (Go), frontend (React), dependencies,
production deployment at `https://sfs.nihaoshares.com`, and local Docker Compose stack.
**Previous assessment:** [SECURITY-ASSESSMENT-2026-04.md](./SECURITY-ASSESSMENT-2026-04.md) (2026-04-09)
**Methodology:** Static code review + live probing of both local (`http://localhost:8082`)
and production (`https://sfs.nihaoshares.com`) using automated exploit scripts. All
unauthenticated probes were run against both targets for comparison.

---

## Architecture Diagrams

Visual diagrams of the system architecture, security controls, and attack surfaces are
available in [`docs/diagrams/`](./diagrams/). Start with the summary page and drill into
the detailed views as needed.

| Diagram | What it shows |
|---------|--------------|
| [Summary (start here)](./diagrams/security-architecture.mermaid.md) | Quick-reference: overview, fix status, attack surface, local vs prod comparison |
| [Architecture Overview](./diagrams/architecture-overview.md) | High-level components and data flow |
| [Security Layer](./diagrams/security-layer.md) | Auth decision flow, OIDC login sequence, header status |
| [Authentication Layer](./diagrams/auth-layer.md) | Token lifecycle, session invalidation, role mapping, rate limit coverage |
| [Storage & Encryption](./diagrams/storage-layer.md) | Upload/download pipelines, key management, compromise impact matrix |
| [Full Architecture](./diagrams/full-architecture.md) | Complete annotated system map + OnlyOffice attack chain |

All diagrams use a consistent color scheme: **red** = critical, **orange** = gap, **yellow** = medium, **green** = working control, **blue** = encryption. Each page includes a "how to read" guide and explains the key issues shown.

## Backend Reusability Analysis

A separate analysis of the Go backend's code structure, coupling, and reusability is
available at **[BACKEND-REUSABILITY-ANALYSIS.md](./BACKEND-REUSABILITY-ANALYSIS.md)**.

Key findings: 7 packages (crypto, chunker, config, storage, plans, health, httputil) are
standalone-ready. The API handler layer (`api/v2/`, 18K lines) has a 4,719-line god object,
434 raw database queries scattered across 45 files, and no repository pattern. A prioritized
refactoring roadmap is included.

---

## Executive Summary

Since the v1 assessment (2026-04-09), **6 of the original findings have been fixed or
significantly mitigated**. However, **the most critical finding (OnlyOffice SSRF + unauthenticated
callback) remains fully exploitable** on both local and production. Additionally, this
reassessment identified **3 new findings** not covered in v1.

### Scorecard: v1 findings

| ID | Finding | v1 Severity | Status in v2 | Notes |
|----|---------|-------------|--------------|-------|
| C-1 | OnlyOffice SSRF → file write | Critical | **OPEN** | Still no SSRF protection, still no JWT verification on callback |
| C-2 | Inline SVG/HTML XSS | Critical | **OPEN** | `Content-Disposition: inline` + `image/svg+xml` still served |
| H-1 | golang-jwt CVE-2025-30204 | High | **FIXED** | Upgraded to v5.3.1 |
| H-2 | API key timing oracle | High | **MITIGATED** | Malformed tokens normalized; DB hit/miss timing acknowledged but not fully fixed |
| H-3 | Repo token skips account-status | High | **NEEDS RETEST** | Requires deactivated user to verify |
| H-4 | OIDC role escalation | High | **FIXED** | `superadmin` blocked from claims; `mapOIDCRole` enforces allow-list |
| H-5 | Share-link enumeration oracle | High | **OPEN** | 404 vs 200 still distinguishable |
| H-6 | Share-link cookie `==` | High | **FIXED** | Now uses `subtle.ConstantTimeCompare` |
| H-7 | Weak auth rate limit | High | **IMPROVED** | 10/120 get through locally (was 24/120 in v1) |
| H-8 | Zip bomb / dir download DoS | High | **OPEN** | No file count, depth, or total byte limits |
| H-9 | OIDC DNS rebinding | High | **OPEN** | No DNS pinning on discovery/JWKS fetch |
| L-1 | OIDC `aud` not validated | Latent | **FIXED** | Full audience validation implemented with multi-aud support |
| M-1 | CSRF logout | Medium | **OPEN** | Still returns 200 without auth on both targets |
| M-2 | CORS `*` + credentials | Medium | **OPEN** | `config.prod.yaml` still ships `allowed_origins: ["*"]` |
| M-3 | Missing security headers | Medium | **PARTIALLY FIXED** | 3/5 now set by app; CSP still missing; prod nginx adds X-Frame-Options |
| M-4 | Avatar email enumeration | Medium | **NEEDS RETEST** | Requires known email |
| M-5 | `/metrics` exposed | Medium | **MITIGATED (prod)** | Blocked by nginx (403); still exposed at app level |
| M-6 | OIDC state flood | Medium | **FIXED** | State map now has TTL (10 min), cap (10k), and eviction |
| M-7 | Session invalidation node-local | Medium | **OPEN** | No distributed revocation |
| M-8 | PBKDF2 at 1000 iterations | Medium | **OPEN** | Compat mode still uses 1000 iterations (required for Seafile clients) |
| M-9 | OnlyOffice JWT TTL 8 hours | Medium | **OPEN** | Not changed |
| M-10 | Frontend dependency CVEs | Medium | **NEEDS RETEST** | Requires `npm audit` |

### New findings in v2

| ID | Finding | Severity | Notes |
|----|---------|----------|-------|
| **V2-C1** | OnlyOffice callback completely unauthenticated | **Critical** | No JWT verification, no auth middleware. Confirmed on both local and prod |
| **V2-L1** | `/ready` leaks component status | Low | Exposes database/storage health unauthenticated (local only; prod catches via nginx) |
| **V2-L2** | OIDC config leaks `client_id` and `issuer` | Low | Enables targeted phishing. Confirmed on prod: `client_id: "622935"` |

---

## Live probe comparison: Local vs Production

All unauthenticated probes were run against both targets. Key differences:

| Probe | Local (`localhost:8082`) | Production (`sfs.nihaoshares.com`) |
|-------|------------------------|-------------------------------------|
| **H-1** JWT DoS | No latency growth at 1000 dots | No latency growth at 1000 dots |
| **H-5** Share-link enum | 404 oracle confirmed (20/50 not rate-limited) | 404 oracle confirmed (50/50, no rate limiting on this path) |
| **H-7** Auth rate limit | 10/120 got through (~8%) | 26/120 got through (~22%) |
| **M-1** CSRF logout | HTTP 200 - reproduced | HTTP 200 - reproduced |
| **M-2** CORS wildcard | ACAO:* + ACAC:true (dev mode) | **ACAO:* + ACAC:true (PRODUCTION!)** |
| **M-3** Security headers | 3/5 present (no CSP, no X-Frame-Options) | 5/7 present (nginx adds X-Frame-Options, X-Robots-Tag; CSP still missing) |
| **M-5** Metrics | HTTP 200 - exposed | HTTP 403 - blocked by nginx |
| **V2-C1** OnlyOffice unauth | HTTP 200 - **reproduced** | HTTP 200 - **reproduced** |
| **V2-L1** /ready info leak | JSON with db/storage status | Caught by nginx (returns frontend HTML) |
| **V2-L2** OIDC config leak | `enabled: false` (dev mode) | **Leaks client_id, issuer, scopes** |
| **Info disclosure** | All info endpoints return 200 | All info endpoints return 200 |

### Critical difference: Production CORS

Production is running with `ACAO: *` and `ACAC: true`. This is because `config.prod.yaml`
ships `allowed_origins: ["*"]` and the CORS middleware treats `"*"` as a real origin, not
as a special dev-mode flag. The `buildCORSConfig` function in `server.go:258-285` only
uses `AllowAllOrigins` when `Auth.DevMode` is true, but when prod config has `["*"]` in
`AllowedOrigins`, gin-cors treats it as a literal `"*"` origin entry.

---

## Detailed findings

### CRITICAL

#### V2-C1 OnlyOffice callback is completely unauthenticated (NEW)

**File:** `internal/api/server_routes.go:231-232`, `internal/api/v2/onlyoffice.go:64-79`
**Severity:** Critical
**Confirmed:** Both local and production

The `/onlyoffice/editor-callback` endpoint is registered **without any auth middleware**:

```go
// server_routes.go:231-232
onlyoffice := s.router.Group("/onlyoffice")
v2.RegisterOnlyOfficeCallbackRoutes(onlyoffice, ...)

// onlyoffice.go:77
rg.POST("/editor-callback", h.EditorCallback)
```

The handler reads the JSON body directly without verifying the OnlyOffice JWT signature.
While it reads the body at line 508, there is no call to verify the JWT token against
`ONLYOFFICE_JWT_SECRET`. This means:

1. **Any internet user** can POST to `/onlyoffice/editor-callback`
2. The `users` array in the request body is **used directly** for permission checks (line 556-558)
3. When `status=2` (save), the `url` field triggers an **HTTP GET to an attacker-controlled URL** (line 660)
4. Combined with C-1 from v1, this is a **zero-auth SSRF chain**: no secret needed

**Attack chain:**
```
POST /onlyoffice/editor-callback
Content-Type: application/json

{
  "status": 2,
  "key": "known-doc-key",
  "url": "http://169.254.169.254/latest/meta-data/iam/security-credentials/",
  "users": ["valid-user-id"]
}
```

If the attacker knows (or can guess) a valid `doc_key` mapping, the server will:
1. Look up `repoID` and `filePath` from the key
2. Use the attacker-supplied user ID for permission checks
3. Fetch the attacker-controlled URL (SSRF)
4. Write the fetched content to the target file

**Fix (immediate):**
1. Add JWT verification as the **first step** in `EditorCallback` — reject if signature is invalid
2. Add auth middleware to the `/onlyoffice` route group
3. Never trust `users[]` from the callback body for permission decisions
4. Apply all SSRF protections from v1 C-1 (IP allowlist, body size cap, redirect policy)

**Reproduce:**
```bash
./docs/exploit-scripts/v2-onlyoffice-unauth-callback.sh --host https://sfs.nihaoshares.com
```

---

#### C-1 OnlyOffice SSRF → arbitrary-content file write (STILL OPEN from v1)

**File:** `internal/api/v2/onlyoffice.go:660`
**Status:** Unchanged. `http.Get(internalURL)` with no IP validation, no body size limit,
no redirect policy, no timeout configuration.

Now **more dangerous** because V2-C1 proves no authentication is needed to trigger it.

---

#### C-2 User-uploaded files served inline → stored XSS (STILL OPEN from v1)

**File:** `internal/api/v2/sharelink_view.go:907,914`, `internal/api/v2/fileview.go:650-708`
**Status:** Unchanged. `resolveInlineContentType` still maps `.svg` to `image/svg+xml`
and the response uses `Content-Disposition: inline`. While `X-Content-Type-Options: nosniff`
is now present (fixing the MIME-confusion variant), the primary SVG XSS vector remains:
browsers faithfully render `image/svg+xml` with `<script>` tags.

---

### HIGH

#### H-1 golang-jwt CVE-2025-30204 — FIXED

**go.mod** now pins `github.com/golang-jwt/jwt/v5 v5.3.1` (patched release).

---

#### H-4 OIDC role escalation — FIXED

**File:** `internal/auth/oidc.go:1186-1203`
`mapOIDCRole` now explicitly blocks `superadmin`, `super_admin`, `platform_admin` and
downgrades them to `DefaultRole`. The `extractRoles` function sets `blockedPrivilegedOIDCRole`
which prevents existing users from being escalated. Superadmin is DB-only.

---

#### H-5 Share-link enumeration oracle (STILL OPEN)

**Live results:** On production, 50/50 random token probes returned HTTP 404 with no rate
limiting on this specific path. A valid token returns 200. The oracle is confirmed.

---

#### H-6 Share-link cookie comparison — FIXED

**File:** `internal/api/v2/sharelink_view.go:1591`
Now uses `subtle.ConstantTimeCompare([]byte(cookieValue), []byte(expected)) == 1`.

---

#### H-7 Auth rate limiting — IMPROVED

**Live results:**
- Local: 10/120 (8%) got through (improved from 24/120 in v1)
- Production: 26/120 (22%) got through

Still loose for distributed credential stuffing. No per-account throttling.

---

#### H-8 Zip bomb / directory download DoS (STILL OPEN)

**File:** `internal/api/seafhttp.go:1885-1916`
`addDirToZip` still recurses without file count cap, depth cap, or total byte cap.

---

### MEDIUM

#### M-1 CSRF logout (STILL OPEN)

Confirmed on both local and production: `DELETE /api/v2.1/auth/session` returns 200 without
any authentication.

---

#### M-2 CORS wildcard in production (STILL OPEN)

**Critical for production:** `config.prod.yaml:209-210` still ships:
```yaml
cors:
  allowed_origins:
    - "*"
```

This results in `ACAO: *` with `ACAC: true` on **production**. Confirmed live.

---

#### M-3 Security headers ��� PARTIALLY FIXED

**New middleware:** `internal/middleware/securityheaders.go` now sets:
- `X-Content-Type-Options: nosniff` (FIXED)
- `Referrer-Policy: strict-origin-when-cross-origin` (FIXED)
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` (FIXED)

**Still missing at application level:**
- `Content-Security-Policy` (MISSING on both)
- `X-Frame-Options` (added by nginx in prod, missing at app level)
- `Permissions-Policy` (MISSING on both)

---

#### M-5 Metrics — MITIGATED in production

Blocked by nginx (403) on production. Still exposed unauthenticated at the application level.

---

#### M-6 OIDC state flood — FIXED

**File:** `internal/auth/oidc.go:1237-1261`
State map now has:
- TTL: 10 minutes (pruneExpiredStatesLocked)
- Hard cap: configurable (maxPendingStates)
- Eviction: oldest state evicted when at capacity

---

### LOW / INFORMATIONAL

#### V2-L1 /ready leaks component status (NEW)

**Local:** Returns `{"checks":{"database":"ok","storage":"ok"},"status":"ready"}` unauthenticated.
**Production:** Caught by nginx (returns frontend HTML). Not directly exploitable in prod,
but the app-level endpoint is still exposed.

---

#### V2-L2 OIDC config leaks IdP details (NEW)

**Production confirmed:**
```json
{
  "client_id": "622935",
  "enabled": true,
  "issuer": "https://accounts.sesamedisk.com/openid",
  "scopes": ["openid", "profile", "email"]
}
```
Enables targeted phishing with a pixel-perfect fake login page.

---

#### L-1 OIDC audience validation — FIXED

**File:** `internal/auth/oidc.go:551-570`
Full audience validation implemented. Checks token `aud` against `config.ClientID`.
Supports multi-audience tokens (iterates the `aud` array).
Respects `ValidateAudience` config flag with secure default.

---

## Encryption & Compromise Scenario Analysis

### Library encryption architecture

SesameFS implements a **dual-mode encryption system** (`internal/crypto/crypto.go`):

| Mode | KDF | Iterations | Purpose |
|------|-----|-----------|---------|
| Seafile compat (v2) | PBKDF2-HMAC-SHA256 | 1,000 | Desktop/mobile client compatibility |
| SesameFS strong (v10/v12) | Argon2id | 3 iter, 64MB mem, 4 threads | Web/API clients |

**Key management:**
- Per-library random 32-byte file key (AES-256)
- File key encrypted with user password (two separate derivations: PBKDF2 + Argon2id)
- Password verification via HMAC magic tokens with constant-time comparison
- File content encrypted with AES-256-CBC per block

### Compromise scenarios

#### Scenario 1: S3 bucket compromised

**Impact:** All unencrypted library blocks are exposed. Encrypted libraries remain protected
by AES-256-CBC encryption (file key is NOT stored in S3).

**Gap:** S3 `Put` operations do NOT use `ServerSideEncryption` (`internal/storage/s3.go:158-170`).
Data at rest relies entirely on S3 bucket-level default encryption policy. If the bucket policy
is not configured, blocks are stored unencrypted.

**Recommendation:** Add `ServerSideEncryption: types.ServerSideEncryptionAes256` to all
`PutObjectInput` calls as defense-in-depth.

#### Scenario 2: Cassandra database compromised

**Impact:** Attacker gains access to:
- User records (email, hashed passwords, roles, org memberships)
- Session token hashes (SHA-256 — cannot be reversed to steal sessions)
- Library metadata (names, permissions, encryption params including salt and magic)
- File system tree (directory structure, file names, block ID lists)
- API key hashes (SHA-256 — cannot be reversed)

**Protected:** Encrypted library file keys are stored as ciphertext (encrypted with user
password). Attacker cannot decrypt without the user's password. PBKDF2 at 1000 iterations
is the weak link — offline dictionary attacks against encrypted library passwords are feasible
for weak passwords.

**Recommendation:** For encrypted libraries, enforce minimum password complexity. Consider
deprecating PBKDF2 compat mode or raising iterations significantly.

#### Scenario 3: Application server compromised

**Impact:** Full access to all data in transit. Can intercept:
- User sessions and tokens
- Decrypt sessions (file keys in memory for unlocked libraries)
- S3 credentials (in environment variables)
- Cassandra credentials
- OnlyOffice JWT secret

**Mitigation:** Limit blast radius by:
- Running sesamefs as non-root (Dockerfile uses Debian slim, verify user)
- Separating secrets into mounted volumes or secret managers
- Network segmentation (Cassandra on private subnet)

#### Scenario 4: OnlyOffice compromised (or spoofed)

**Impact:** Combined with V2-C1 (unauthenticated callback), an attacker who controls
OnlyOffice (or simply knows the endpoint exists) can:
1. Trigger SSRF to internal services
2. Write arbitrary content to any library file (if they know/guess a valid doc_key)
3. Exfiltrate internal service responses via the save-document path

**This is the highest-risk scenario** because it requires ZERO authentication.

#### Scenario 5: Network MITM between sesamefs and S3/Cassandra

**Impact:** S3 SDK uses TLS by default (`ForceAttemptHTTP2: true`). Cassandra connection
depends on configuration — if not using TLS, an attacker on the private subnet can intercept
all database traffic.

---

## Production readiness gaps

### Must-fix before production

1. **V2-C1 + C-1:** OnlyOffice callback auth + SSRF protection
2. **C-2:** Serve user-uploaded SVG/HTML as `attachment`, not `inline`
3. **M-2:** Replace `allowed_origins: ["*"]` in `config.prod.yaml` with actual domain(s)
4. **CSP header:** Add `Content-Security-Policy` to the security headers middleware

### Should-fix soon

5. **H-5:** Uniform response for share-link token lookup + rate limiting
6. **H-7:** Tighter auth rate limit + per-account throttling
7. **H-8:** Add file count/depth/size limits to zip download
8. **V2-L2:** Strip `client_id` from unauthenticated OIDC config response
9. **M-5:** Bind `/metrics` to internal-only listener
10. **S3 SSE:** Add `ServerSideEncryption` to Put operations

### Nice to have

11. **M-1:** CSRF protection on session DELETE
12. **M-7:** Distributed session revocation
13. **M-8:** Raise PBKDF2 iterations or deprecate compat mode
14. **Permissions-Policy** header
15. **Block integrity verification** on download (re-hash and compare)

---

## Benchmarking

Benchmark scripts are provided in [`docs/benchmarks/`](./benchmarks/):

| Script | What it measures |
|--------|-----------------|
| `benchmark-upload-download.sh` | Single-file and concurrent upload/download throughput |
| `benchmark-concurrent-users.sh` | Concurrent authenticated user simulation |
| `benchmark-storage-ops.sh` | Block-level storage operations + endpoint latency profiling |

### Local benchmark results (2026-04-13)

**Environment:** Docker Compose on a single dev machine (sesamefs + Cassandra + MinIO).
These numbers reflect local performance without network latency — useful as a ceiling
for what the Go server and storage layer can do.

#### Upload / Download throughput

| Operation | Size | Time | Throughput |
|-----------|------|------|------------|
| Single upload | 1 MB | 90ms | 89 Mbps |
| Single upload | 10 MB | 286ms | 280 Mbps |
| Single upload | 100 MB | 3,326ms | 241 Mbps |
| Single download | 1 MB | 39ms | 205 Mbps |
| Single download | 10 MB | 59ms | 1,356 Mbps |
| Single download | 100 MB | 230ms | 3,478 Mbps |

Downloads are faster than uploads because MinIO serves reads from memory/disk cache
while uploads require chunking, hashing, and S3 Put.

#### Concurrent throughput (1 MB files)

| Concurrency | Upload total | Upload Mbps | Download total | Download Mbps |
|-------------|-------------|-------------|----------------|---------------|
| 1 | 105ms | 76 | 33ms | 242 |
| 4 | 230ms | 139 | 49ms | 653 |
| 8 | 437ms | 147 | 100ms | 640 |
| 16 | 529ms | 242 | 181ms | 707 |

Upload throughput scales sub-linearly (bottlenecked by S3 Put latency). Download
throughput scales well up to 8 concurrent, then plateaus around 700 Mbps.

#### Block-level operations

| Operation | Size | Time | Notes |
|-----------|------|------|-------|
| Block upload | 64 KB | 51ms | POST /api/v2/blocks/upload |
| Block upload | 256 KB | 31ms | Includes hash + dedup check |
| Block upload | 1 MB | 59ms | |
| Block upload | 4 MB | 109ms | |
| Block download | 256 KB | 20–25ms | GET /api/v2/blocks/:hash, 5 runs |

#### API endpoint latency

| Endpoint | p50 | p95 | Notes |
|----------|-----|-----|-------|
| /api2/ping | <1ms | 1ms | No auth, no DB |
| /health | <1ms | <1ms | No auth, lightweight check |
| /ready | 2ms | 8ms | Checks DB + storage health |
| /api2/account/info | 12ms | 14ms | Auth + Cassandra query |
| /api2/repos/ | 8ms | 11ms | Auth + Cassandra query |

#### Concurrent user capacity

| Users | Requests | Success | Fail | Wall time | Req/s | Avg latency |
|-------|----------|---------|------|-----------|-------|-------------|
| 1 | 20 | 20 | 0 | 514ms | 39 | 23ms |
| 5 | 100 | 100 | 0 | 898ms | 111 | 37ms |
| 10 | 200 | 200 | 0 | 1,492ms | 134 | 62ms |
| 25 | 500 | 500 | 0 | 3,123ms | 160 | 128ms |

Zero failures at 25 concurrent users. Throughput scales from 39 to 160 req/s.
Latency increases linearly (23ms at 1 user, 128ms at 25 users) — consistent with
serialized Cassandra queries and no connection pooling saturation.

#### Resource usage (Docker containers, idle + post-benchmark)

| Container | CPU | Memory |
|-----------|-----|--------|
| sesamefs | <1% | 86 MB |
| Cassandra | 1.1% | 953 MB |
| MinIO | <1% | 135 MB |
| OnlyOffice | <1% | 713 MB |
| Frontend (nginx) | <1% | 8 MB |

SesameFS itself is very lightweight at 86 MB. Cassandra dominates memory at ~1 GB
(expected — JVM heap). OnlyOffice is the second-heaviest container.

### Running benchmarks

```bash
# Against local (use dev token from config.docker.yaml)
./docs/benchmarks/benchmark-storage-ops.sh \
    --host http://localhost:8082 --token dev-token-superadmin --repo <repo-id>

./docs/benchmarks/benchmark-upload-download.sh \
    --host http://localhost:8082 --token dev-token-superadmin --repo <repo-id>

./docs/benchmarks/benchmark-concurrent-users.sh \
    --host http://localhost:8082 --token dev-token-superadmin

# Against production (use a real token)
./docs/benchmarks/benchmark-upload-download.sh \
    --host https://sfs.nihaoshares.com --token <real-token> --repo <repo-id>
```

**Note:** Resource usage (`docker stats`) in the script output only reflects local
containers. When benchmarking a remote host, those numbers are irrelevant to the target.

---

## Reproduction scripts

### V2 new scripts

| Script | Finding | Auth needed? |
|--------|---------|-------------|
| `v2-onlyoffice-unauth-callback.sh` | V2-C1 unauthenticated callback | No |
| `v2-cors-prod-wildcard.sh` | CORS wildcard check | No |
| `v2-inline-content-disposition.sh` | C-2 inline SVG | Yes (--token, --repo) |
| `v2-security-headers-check.sh` | Updated M-3 check | No |
| `v2-ready-info-leak.sh` | V2-L1 /ready info leak | No |
| `v2-oidc-config-leak.sh` | V2-L2 OIDC config leak | No |
| `v2-zip-no-limits.sh` | H-8 zip limits check | Yes (--token, --repo) |
| `v2-run-all-unauth.sh` | Runs all unauthenticated probes (v1 + v2) | No |

### Full unauthenticated sweep

```bash
# Against local
./docs/exploit-scripts/v2-run-all-unauth.sh --host http://localhost:8082

# Against production
./docs/exploit-scripts/v2-run-all-unauth.sh --host https://sfs.nihaoshares.com
```

---

## Assessment environment

- **Local stack:** Docker Compose with sesamefs (:8082), Cassandra, MinIO, OnlyOffice, Frontend
  - `AUTH_DEV_MODE=true` (expected for local)
  - go.mod: golang-jwt v5.3.1
- **Production:** `https://sfs.nihaoshares.com`
  - Behind nginx reverse proxy (confirmed via `Server: nginx` header)
  - OIDC enabled against `https://accounts.sesamedisk.com/openid`
  - Metrics blocked by nginx (403)
  - /ready caught by nginx (returns frontend HTML)
  - X-Frame-Options: SAMEORIGIN (added by nginx)
  - CORS: `ACAO: *` + `ACAC: true` (from `config.prod.yaml`)
- **Assessment date:** 2026-04-13
