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

Since the v1 assessment (2026-04-09), **all critical and most high-severity findings have
been fixed or significantly mitigated**. The 4 must-fix production items (V2-C1/C-1 OnlyOffice
SSRF, C-2 inline XSS, M-2 CORS wildcard, M-3 CSP headers) are now resolved. This reassessment
identified **3 new findings** not covered in v1, of which 2 are already fixed.

### Scorecard: v1 findings

| ID | Finding | v1 Severity | Status in v2 | Notes |
|----|---------|-------------|--------------|-------|
| C-1 | OnlyOffice SSRF → file write | Critical | **FIXED** | JWT verification on callback (`verifyCallbackJWT`), URL allowlist (`validateOnlyOfficeDownloadURL`), hardened HTTP client (60s timeout, 3 redirect max, body size limits) |
| C-2 | Inline SVG/HTML XSS | Critical | **FIXED** | Dangerous MIME types (SVG, HTML, XML, XHTML) now forced to `Content-Disposition: attachment` via `forcedAttachmentTypes` map; `X-Content-Type-Options: nosniff` set globally |
| H-1 | golang-jwt CVE-2025-30204 | High | **FIXED** | Upgraded to v5.3.1 |
| H-2 | API key timing oracle | High | **MITIGATED** | Malformed tokens normalized; DB hit/miss timing acknowledged but not fully fixed |
| H-3 | Repo token skips account-status | High | **NEEDS RETEST** | Requires deactivated user to verify |
| H-4 | OIDC role escalation | High | **FIXED** | `superadmin` blocked from claims; `mapOIDCRole` enforces allow-list |
| H-5 | Share-link enumeration oracle | High | **IMPROVED** | Missing/expired/disabled now collapse to opaque unavailable responses; public routes remain per-IP throttled, but valid tokens still return 200 |
| H-6 | Share-link cookie `==` | High | **FIXED** | Now uses `subtle.ConstantTimeCompare` |
| H-7 | Weak auth rate limit | High | **IMPROVED** | 10/120 get through locally (was 24/120 in v1) |
| H-8 | Zip bomb / dir download DoS | High | **FIXED** | ZIP directory downloads now enforce centralized entry, depth, and total-byte budgets before and during streaming |
| H-9 | OIDC DNS rebinding | High | **OPEN** | No DNS pinning on discovery/JWKS fetch |
| L-1 | OIDC `aud` not validated | Latent | **FIXED** | Full audience validation implemented with multi-aud support |
| M-1 | CSRF logout | Medium | **FIXED IN CODE** | `DELETE /api/v2.1/auth/session` now requires a valid Authorization token and rejects unauthenticated callers |
| M-2 | CORS `*` + credentials | Medium | **FIXED** | `config.prod.yaml` now ships `allowed_origins: []`; origins loaded from `CORS_ALLOWED_ORIGINS` env var; production validation rejects wildcard `"*"` with explicit error |
| M-3 | Missing security headers | Medium | **FIXED** | 4/5 headers set: `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `Strict-Transport-Security`, `Content-Security-Policy` (restrictive default + per-route overrides via `SetCSP()`). Only `Permissions-Policy` remains missing (low priority) |
| M-4 | Avatar email enumeration | Medium | **FIXED** | Handler returns generic `{"url":"","is_default":true,"mtime":0}` for all emails — no enumeration oracle |
| M-5 | `/metrics` exposed | Medium | **FIXED IN CODE** | Route is now internal-only at the application layer; nginx blocking remains a secondary control |
| M-6 | OIDC state flood | Medium | **FIXED** | State map now has TTL (10 min), cap (10k), and eviction |
| M-7 | Session invalidation node-local | Medium | **OPEN** | No distributed revocation |
| M-8 | PBKDF2 at 1000 iterations | Medium | **OPEN** | Compat mode still uses 1000 iterations (required for Seafile clients) |
| M-9 | OnlyOffice JWT TTL 8 hours | Medium | **OPEN** | Not changed |
| M-10 | Frontend dependency CVEs | Medium | **NEEDS RETEST** | Requires `npm audit` |

### New findings in v2

| ID | Finding | Severity | Notes |
|----|---------|----------|-------|
| **V2-C1** | OnlyOffice callback completely unauthenticated | **Critical** | **FIXED** | JWT verification via `verifyCallbackJWT()` as first step in `EditorCallback`; rejects missing/invalid JWT with 403 |
| **V2-L1** | `/ready` leaks component status | Low | **FIXED in code** | Route is now internal-only and returns 404 to external clients |
| **V2-L2** | OIDC config leaks `client_id` and `issuer` | Low | **FIXED in code** | Public endpoint now returns only `enabled`; historical prod probe leaked `client_id: "622935"` |

---

## Live probe comparison: Local vs Production

All unauthenticated probes were run against both targets. Key differences:

| Probe | Local (`localhost:8082`) | Production (`sfs.nihaoshares.com`) |
|-------|------------------------|-------------------------------------|
| **H-1** JWT DoS | No latency growth at 1000 dots | No latency growth at 1000 dots |
| **H-5** Share-link enum | Historical probe confirmed confirmation oracle | Historical probe confirmed confirmation oracle; current code adds opaque unavailable responses for inactive links and keeps per-IP throttling |
| **H-7** Auth rate limit | 10/120 got through (~8%) | 26/120 got through (~22%) |
| **M-1** CSRF logout | Historical probe reproduced; current code returns 401 without auth token | Historical probe reproduced; current code returns 401 without auth token |
| **M-2** CORS wildcard | Historical: ACAO:* (dev mode); current code rejects `"*"` in prod, requires explicit origins via `CORS_ALLOWED_ORIGINS` env var | Historical: ACAO:* (PRODUCTION); current code rejects `"*"` in prod validation |
| **M-3** Security headers | 4/5 present (CSP, nosniff, HSTS, Referrer-Policy; Permissions-Policy missing) | 4/5 at app level + nginx adds X-Frame-Options, X-Robots-Tag |
| **M-5** Metrics | Historical probe exposed; current code restricts route to internal clients | Historical probe blocked by nginx; current code also restricts route to internal clients |
| **V2-C1** OnlyOffice unauth | Historical: HTTP 200 reproduced; current code verifies JWT and returns 403 without valid signature | Historical: HTTP 200 reproduced; current code verifies JWT and returns 403 without valid signature |
| **V2-L1** /ready info leak | Historical local behavior leaked status JSON; current code returns 404 to external clients | Historical prod path was caught by nginx; current code also restricts route to internal clients |
| **V2-L2** OIDC config leak | `enabled: false` (dev mode) | Historical probe leaked client_id/issuer/scopes; current code now returns only `enabled` |
| **Info disclosure** | All info endpoints return 200 | All info endpoints return 200 |

### Historical note: Production CORS (now fixed)

At the time of the v2 assessment, production was running with `ACAO: *` and `ACAC: true`
because `configs/config.prod.yaml` shipped `allowed_origins: ["*"]`. This has been fixed:
`config.prod.yaml` now ships `allowed_origins: []`, origins are loaded from the
`CORS_ALLOWED_ORIGINS` env var, and production validation (`config.go:Validate()`) explicitly
rejects wildcard `"*"` with a descriptive error message.

---

## Detailed findings

### CRITICAL

#### V2-C1 OnlyOffice callback was completely unauthenticated (FIXED)

**File:** `internal/api/v2/onlyoffice.go`
**Severity:** Critical
**Status:** **FIXED**

The `/onlyoffice/editor-callback` endpoint was previously unauthenticated. The fix implements
a 3-layer defense:

1. **JWT verification** (`verifyCallbackJWT`, line ~529-578): When `ONLYOFFICE_JWT_SECRET` is
   configured, the handler verifies the HS256 JWT signature as the **first step** before
   processing any payload fields. OnlyOffice wraps the callback body in a `token` field;
   the server extracts and validates it. Missing or invalid JWT returns 403.

2. **URL allowlist** (`validateOnlyOfficeDownloadURL`, line ~743-775): Before any HTTP fetch,
   the download URL host is validated against the configured `InternalURL` or `APIJSURL` host.
   Only `http`/`https` schemes are allowed. This blocks SSRF to internal services, cloud
   metadata endpoints, and arbitrary hosts.

3. **Hardened HTTP client** (`onlyOfficeHTTPClient`, line ~718-726): Dedicated client with
   60-second timeout, max 3 redirects (each re-validated), and `io.LimitReader` on response
   body (500MB cap via `MaxDocumentBytes` config).

The original attack chain (unauthenticated POST → attacker-controlled URL fetch → SSRF) is
now blocked at all three layers independently.

---

#### C-1 OnlyOffice SSRF → arbitrary-content file write (FIXED — merged with V2-C1)

**File:** `internal/api/v2/onlyoffice.go`
**Status:** **FIXED** as part of the V2-C1 remediation above. The `saveEditedDocument` function
now validates the download URL against the configured OnlyOffice host allowlist and uses
the hardened HTTP client instead of `http.Get`.

---

#### C-2 User-uploaded files served inline → stored XSS (FIXED)

**Files:** `internal/api/v2/sharelink_view.go`, `internal/api/v2/fileview.go`
**Status:** **FIXED**

Dangerous MIME types are now forced to `Content-Disposition: attachment` via a `forcedAttachmentTypes`
map that includes `image/svg+xml`, `text/html`, `application/xhtml+xml`, `text/xml`, and
`application/xml`. The `resolveInlineContentType` function still correctly identifies MIME types
(needed for Content-Type header), but the disposition is overridden to `attachment` for types
that can execute JavaScript. Combined with the global `X-Content-Type-Options: nosniff` header,
browsers cannot be tricked into rendering uploaded files as executable content.

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

#### H-5 Share-link enumeration oracle (IMPROVED, confirmation signal remains)

**Historical live results:** On production, 50/50 random token probes returned HTTP 404 with no rate
limiting on this specific path. A valid token returns 200.

**Current code status:** public share-link handlers now collapse missing, expired, and disabled
tokens into the same opaque `share link unavailable` response, while public routes remain
throttled per IP. A valid token still returns 200, so
this should now be treated as a confirmation oracle with improved throttling rather than an
unbounded enumeration path.

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

#### H-8 Zip bomb / directory download DoS (FIXED IN CODE)

**Files:** `internal/api/seafhttp.go`, `internal/api/seafhttp_test.go`
Directory ZIP downloads now run through a centralized traversal budget that enforces:
- maximum entry count
- maximum directory depth
- maximum total uncompressed bytes

The handler validates the tree before headers are committed and re-checks the same budget while
streaming, so oversized trees fail with `413` instead of consuming unbounded memory/CPU.

---

### MEDIUM

#### M-1 CSRF logout (FIXED IN CODE AFTER ASSESSMENT)

**Files:** `internal/api/v2/auth.go`, `internal/api/v2/auth_test.go`
The vulnerable behavior was the unauthenticated success path: `DELETE /api/v2.1/auth/session`
returned `200` even when the caller presented no session material. The handler now requires a
valid Authorization token, validates the session before invalidation, and returns `401` for
missing or invalid callers.

This closes the browser-driven logout primitive described in the assessment because third-party
sites cannot trigger a successful logout without the victim's bearer token.

---

#### M-2 CORS wildcard in production (FIXED)

**Status:** **FIXED**

`configs/config.prod.yaml` now ships `allowed_origins: []` (empty). Origins are loaded from
the `CORS_ALLOWED_ORIGINS` environment variable (comma-separated). Production validation in
`config.go:Validate()` explicitly rejects wildcard `"*"` with: `"cors.allowed_origins contains
wildcard \"*\" which is insecure in production; set specific origins via CORS_ALLOWED_ORIGINS
env var"`. The `.env.prod.example` template shows correct usage: `CORS_ALLOWED_ORIGINS=https://${DOMAIN}`.

---

#### M-3 Security headers — FIXED

**Middleware:** `internal/middleware/securityheaders.go` now sets on every response:
- `X-Content-Type-Options: nosniff` (FIXED)
- `Referrer-Policy: strict-origin-when-cross-origin` (FIXED)
- `Strict-Transport-Security: max-age=31536000; includeSubDomains` (FIXED)
- `Content-Security-Policy: default-src 'none'; frame-ancestors 'self'` (FIXED — restrictive default)

**Per-route CSP overrides** via `middleware.SetCSP()`:
- OnlyOffice editor: dynamic CSP allowing the configured OnlyOffice origin for scripts/styles/frames
- Login success page: `script-src 'unsafe-inline'; img-src 'self'; style-src 'self'; frame-ancestors 'none'`
- File viewer: CSP relaxed for PDF/image preview needs

`frame-ancestors 'self'` replaces `X-Frame-Options` at the application level.

**Still missing:** `Permissions-Policy` header (low priority, nice-to-have)

---

#### M-5 Metrics — FIXED IN CODE AFTER ASSESSMENT

**Files:** `internal/api/server_routes.go`, `internal/api/server_test.go`, `internal/middleware/internal_only.go`
`/metrics` is now guarded by internal-only middleware in the Go application itself. External
clients receive `404`, while loopback/private addresses continue to work for local scraping.

The old nginx `403` remains useful as an outer layer, but it is no longer the only barrier.

---

#### M-6 OIDC state flood — FIXED

**File:** `internal/auth/oidc.go:1237-1261`
State map now has:
- TTL: 10 minutes (pruneExpiredStatesLocked)
- Hard cap: configurable (maxPendingStates)
- Eviction: oldest state evicted when at capacity

---

### LOW / INFORMATIONAL

#### V2-L1 /ready leaks component status (FIXED IN CODE AFTER ASSESSMENT)

**Files:** `internal/api/server_routes.go`, `internal/api/server_test.go`, `internal/middleware/internal_only.go`
`/ready` is now protected by the same internal-only middleware as `/metrics`. External clients
receive `404`, so database/storage component health is no longer exposed on the public surface.

---

#### V2-L2 OIDC config leaks IdP details (FIXED IN CODE AFTER ASSESSMENT)

**Historical production probe:**
```json
{
  "client_id": "622935",
  "enabled": true,
  "issuer": "https://accounts.sesamedisk.com/openid",
  "scopes": ["openid", "profile", "email"]
}
```
That response enabled targeted phishing with a pixel-perfect fake login page.

**Current code status:** `GET /api/v2.1/auth/oidc/config` now returns only `{ "enabled": true|false }`
for unauthenticated callers. The login shell only needs to know whether browser SSO is available;
the login URL itself is still obtained from the dedicated login endpoint.

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

**Update:** This scenario is now mitigated by the V2-C1 fix (JWT verification + URL allowlist).
An attacker would need to know the `ONLYOFFICE_JWT_SECRET` to forge a valid callback.

#### Scenario 5: Network MITM between sesamefs and S3/Cassandra

**Impact:** S3 SDK uses TLS by default (`ForceAttemptHTTP2: true`). Cassandra connection
depends on configuration — if not using TLS, an attacker on the private subnet can intercept
all database traffic.

---

## Production readiness gaps

### Must-fix before production — ALL RESOLVED

1. ~~**V2-C1 + C-1:** OnlyOffice callback auth + SSRF protection~~ **FIXED** (JWT verify + URL allowlist + hardened client)
2. ~~**C-2:** Serve user-uploaded SVG/HTML as `attachment`, not `inline`~~ **FIXED** (`forcedAttachmentTypes` + `nosniff`)
3. ~~**M-2:** Replace `allowed_origins: ["*"]` in `configs/config.prod.yaml` with actual domain(s)~~ **FIXED** (env var + prod validation rejects `*`)
4. ~~**CSP header:** Add `Content-Security-Policy` to the security headers middleware~~ **FIXED** (restrictive default + per-route overrides)

### Should-fix soon

5. **H-3:** Repo API tokens skip `enforceAccountStatus` — deactivated users can still sync
6. **H-7:** Tighter auth rate limit + per-account throttling
7. **M-9:** OnlyOffice JWT TTL still at 8 hours — reduce to 1h and make configurable
8. **H-9:** OIDC DNS rebinding — add DNS pinning and private IP rejection on discovery/JWKS fetch
9. **S3 SSE:** Add `ServerSideEncryption` to Put operations
10. **M-10:** Outdated frontend deps (moment@2.22, socket.io-client@2.2)

### Nice to have

11. **M-7:** Distributed session revocation (only matters for multi-node deployment)
12. **M-8:** Raise PBKDF2 iterations or deprecate compat mode
13. **Permissions-Policy** header
14. **Block integrity verification** on download (re-hash and compare)
15. **H-2:** Constant-time API key comparison for malformed token branch

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
# Against local (use dev token from configs/config.docker.yaml)
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
  - CORS: `ACAO: *` + `ACAC: true` (from `configs/config.prod.yaml`)
- **Assessment date:** 2026-04-13
