# Security Assessment v2 — 2026-04-13

**Date:** 2026-04-13
**Scope:** Full reassessment of sesamefs backend (Go), frontend (React), dependencies,
production deployment at `https://sfs.nihaoshares.com`, and local Docker Compose stack.
**Previous assessment:** [SECURITY-ASSESSMENT-2026-04.md](./SECURITY-ASSESSMENT-2026-04.md) (2026-04-09)
**Next assessment:** [SECURITY-ASSESSMENT-2026-04-v3.md](./SECURITY-ASSESSMENT-2026-04-v3.md) (2026-04-14) — post-fix verification
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

## Deep-dive analyses

| Document | What it covers |
|----------|---------------|
| [Backend Reusability](./BACKEND-REUSABILITY-ANALYSIS.md) | Package coupling scores, god objects, refactoring roadmap |
| [GC Service](./GC-SERVICE-ANALYSIS.md) | Deletion safety, cleanup scenarios, dedup guarantees, integration tests |
| [Upload & Download](./UPLOAD-DOWNLOAD-ANALYSIS.md) | Token flows, chunked uploads, block streaming, temp files, memory usage |
| [Encryption](./ENCRYPTION-ANALYSIS.md) | Key hierarchy, decrypt sessions, Seafile compat, compromise scenarios |
| [Chunking](./CHUNKING-ANALYSIS.md) | FastCDC, SHA-1/SHA-256 mapping, dedup, client comparison |

---

## Executive Summary

Since the v1 assessment (2026-04-09), **all original critical findings and the original high-severity findings have been addressed in current code**. The remaining open items are limited to medium-severity lifecycle and compatibility concerns (`M-7`, `M-8`, `M-10`), plus residual hardening opportunities around share-link confirmation signals and rate limiting (`H-5`, `H-7`).

The three v2-only findings identified during reassessment are also fixed in current code:

- `V2-C1` OnlyOffice callback authentication/SSRF chain is closed by JWT verification, host allow-listing, and a hardened download client.
- `V2-L1` `/ready` is now internal-only.
- `V2-L2` unauthenticated OIDC config responses now expose only `enabled`.

### Scorecard: v1 findings

| ID | Finding | v1 Severity | Status in v2 | Notes |
|----|---------|-------------|--------------|-------|
| C-1 | OnlyOffice SSRF → file write | Critical | **FIXED** | 3-layer defense: JWT verify + URL allowlist + hardened HTTP client with `io.LimitReader` |
| C-2 | Inline SVG/HTML XSS | Critical | **FIXED** | `forcedAttachmentTypes` forces `Content-Disposition: attachment` for SVG/HTML/XML; `nosniff` global |
| H-1 | golang-jwt CVE-2025-30204 | High | **FIXED** | Upgraded to v5.3.1 |
| H-2 | API key timing oracle | High | **MITIGATED** | Malformed tokens normalized to dummy hash; DB lookup always executes regardless of token shape — residual risk negligible |
| H-3 | Repo token skips account-status | High | **FIXED** | `syncAuthMiddleware` now calls `enforceAccountStatus()` before accepting repo tokens |
| H-4 | OIDC role escalation | High | **FIXED** | `superadmin` blocked from claims; `mapOIDCRole` enforces allow-list |
| H-5 | Share-link enumeration oracle | High | **IMPROVED** | Uniform 404 for invalid/expired/disabled tokens; valid token still returns 200 (acceptable — `crypto/rand` entropy) |
| H-6 | Share-link cookie `==` | High | **FIXED** | Now uses `subtle.ConstantTimeCompare` |
| H-7 | Weak auth rate limit | High | **IMPROVED** | Tighter auth limit + zip download rate limiter (1/15s burst 3); per-account throttling pending |
| H-8 | Zip bomb / dir download DoS | High | **FIXED** | `zip_max_entries` (100k), `zip_max_depth` (64), `zip_max_bytes` (10GiB) + rate limiter |
| H-9 | OIDC DNS rebinding | High | **FIXED** | `newOIDCHTTPClient` resolves DNS once, rejects private/loopback IPs, pins to first resolved address |
| L-1 | OIDC `aud` not validated | Latent | **FIXED** | Full audience validation implemented with multi-aud support |
| M-1 | CSRF logout | Medium | **FIXED IN CODE** | `DELETE /api/v2.1/auth/session` now requires a valid Authorization token and rejects missing/invalid callers |
| M-2 | CORS `*` + credentials | Medium | **FIXED** | `allowed_origins: []` in prod config; `CORS_ALLOWED_ORIGINS` env var required; wildcard rejected at startup |
| M-3 | Missing security headers | Medium | **FIXED** | 5/5 headers global via `SecurityHeaders()` middleware (incl. `Permissions-Policy`); per-route `SetCSP()` overrides |
| M-4 | Avatar email enumeration | Medium | **FIXED** | Handler always returns `{"url":"","is_default":true,"mtime":0}` regardless of email |
| M-5 | `/metrics` exposed | Medium | **FIXED IN CODE** | Route is now guarded by internal-only middleware at the application layer; nginx remains a secondary outer control |
| M-6 | OIDC state flood | Medium | **FIXED** | State map now has TTL (10 min), cap (10k), and eviction |
| M-7 | Session invalidation node-local | Medium | **OPEN** | No distributed revocation |
| M-8 | PBKDF2 at 1000 iterations | Medium | **OPEN** | Compat mode still uses 1000 iterations (required for Seafile clients) |
| M-9 | OnlyOffice JWT TTL 8 hours | Medium | **FIXED** | Configurable via `onlyoffice.jwt_ttl_seconds` (default 3600s = 1h, range 300–28800); env var `ONLYOFFICE_JWT_TTL_SECONDS` |
| M-10 | Frontend dependency CVEs | Medium | **PARTIALLY REMEDIATED 2026-05-15** | moment -> 2.29.4, socket.io-client -> 2.5.0, url-parse -> 1.5.10; direct deprecated `MD5`, `i18next-xhr-backend`, `glamor`, `babel-eslint`, and unused `workbox-webpack-plugin` removed; React 17 EOL and transitive deprecations still pending |

### New findings in v2

| ID | Finding | Severity | Status | Notes |
|----|---------|----------|--------|-------|
| **V2-C1** | OnlyOffice callback completely unauthenticated | **Critical** | **FIXED** | JWT verify + URL allowlist + hardened client; same fix as C-1 |
| **V2-L1** | `/ready` leaks component status | Low | **FIXED IN CODE** | `/ready` is now restricted to internal clients at the application layer |
| **V2-L2** | OIDC config leaks `client_id` and `issuer` | Low | **FIXED IN CODE** | Unauthenticated OIDC config responses now return only `enabled` |

---

## Historical Live Probe Comparison: Local vs Production

The table below preserves assessment-time probe results from the vulnerable deployment state. It should be read as historical evidence, not as a description of the current code. Current status is reflected by the scorecard and detailed findings below.

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

### Historical Note: Production CORS

At assessment time, production was running with `ACAO: *` and `ACAC: true` because the deployed configuration still shipped a wildcard origin list. Current code no longer ships that state: `configs/config.prod.yaml` now uses `allowed_origins: []`, production startup requires `CORS_ALLOWED_ORIGINS`, and validation rejects wildcard `"*"` in production.

---

## Detailed findings

### CRITICAL

#### V2-C1 OnlyOffice callback authentication gap (FIXED)

**Files:** `internal/api/v2/onlyoffice.go`, `internal/api/v2/onlyoffice_test.go`
**Severity:** Critical
**Current status:** Fixed in code.

Current code verifies the OnlyOffice callback JWT before processing the payload, validates the translated download URL against the configured OnlyOffice host allow-list, and uses a hardened HTTP client with timeout, redirect cap, and body-size enforcement. The unauthenticated callback-to-SSRF chain described in the original v2 assessment no longer reflects the current implementation.

---

#### C-1 OnlyOffice SSRF → arbitrary-content file write (FIXED)

**File:** `internal/api/v2/onlyoffice.go`
**Current status:** Fixed in code.

The save-document path no longer uses a bare `http.Get`. Current code validates the translated OnlyOffice download URL against configured trusted hosts, rejects unexpected schemes/hosts, uses a dedicated HTTP client with bounded redirects and timeout, and caps the response body via `MaxDocumentBytes` before writing content.

---

#### C-2 User-uploaded files served inline → stored XSS (FIXED)

**Files:** `internal/api/v2/sharelink_view.go`, `internal/api/v2/fileview.go`, `internal/middleware/securityheaders.go`
**Current status:** Fixed in code.

Current code forces dangerous MIME types such as SVG, HTML, XML, and XHTML to download as attachments instead of rendering inline, and emits `X-Content-Type-Options: nosniff` globally. The original same-origin inline-execution path described in the assessment no longer reflects the current file-serving behavior.

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

#### H-5 Share-link enumeration oracle (IMPROVED)

**Current status:** Improved, not fully eliminated.

Current code collapses missing, expired, and disabled share-link states into the same unavailable response and keeps public share routes behind per-IP throttling. A valid token still returns success, so a residual confirmation signal remains if an attacker already has a high-confidence guess, but brute-force enumeration is bounded by token entropy and rate limits.

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

#### H-8 Zip bomb / directory download DoS (FIXED)

**Files:** `internal/api/seafhttp.go`, `internal/config/config.go`, `internal/api/seafhttp_test.go`
**Current status:** Fixed in code.

Directory ZIP downloads now enforce centralized traversal budgets for entry count, directory depth, and total uncompressed bytes both before streaming and during archive generation. These limits are configurable (`zip_max_entries`, `zip_max_depth`, `zip_max_bytes`) and covered by regression tests.

---

### MEDIUM

#### M-1 CSRF logout (FIXED IN CODE)

**Files:** `internal/api/v2/auth.go`, `internal/api/v2/auth_test.go`
**Current status:** Fixed in code.

`DELETE /api/v2.1/auth/session` now requires a valid Authorization token, validates the session before invalidation, and rejects missing or invalid callers with `401`. Historical probe evidence from the assessment remains relevant to the previously deployed build, but not to current code.

---

#### M-2 CORS wildcard in production (FIXED)

**Files:** `configs/config.prod.yaml`, `internal/config/config.go`
**Current status:** Fixed in code and config.

Production config now ships `allowed_origins: []`, startup requires `CORS_ALLOWED_ORIGINS`, and validation rejects wildcard `"*"` in production. The assessment-time wildcard behavior is preserved above as historical deployment evidence only.

---

#### M-3 Security headers — FIXED

**Status: FIXED.** `SecurityHeaders()` middleware now emits on every response: `X-Content-Type-Options: nosniff`, `Referrer-Policy`, `Strict-Transport-Security`, `Content-Security-Policy: default-src 'none'; frame-ancestors 'self'`, and `Permissions-Policy: accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()`. HTML routes use `SetCSP()` overrides.

---

#### M-5 Metrics — FIXED IN CODE

**Files:** `internal/api/server_routes.go`, `internal/middleware/internal_only.go`, `internal/api/server_test.go`
**Current status:** Fixed in code.

`/metrics` is now guarded by the same internal-only middleware as `/ready`, so external clients receive `404` at the application layer even without relying on nginx. The reverse proxy `403` remains useful as a second layer, but the application no longer exposes the endpoint publicly by default.

---

#### M-6 OIDC state flood — FIXED

**File:** `internal/auth/oidc.go:1237-1261`
State map now has:
- TTL: 10 minutes (pruneExpiredStatesLocked)
- Hard cap: configurable (maxPendingStates)
- Eviction: oldest state evicted when at capacity

---

### LOW / INFORMATIONAL

#### V2-L1 /ready leaks component status (FIXED IN CODE)

**Files:** `internal/api/server_routes.go`, `internal/middleware/internal_only.go`, `internal/api/server_test.go`
**Current status:** Fixed in code.

`/ready` is now restricted to internal clients at the application layer. External callers receive `404`, so database and storage readiness details are no longer exposed on the public surface.

---

#### V2-L2 OIDC config leaks IdP details (FIXED IN CODE)

**Files:** `internal/api/v2/auth.go`, `internal/api/v2/auth_test.go`
**Current status:** Fixed in code.

Unauthenticated OIDC config responses now return only `{ "enabled": true|false }`. The public login shell still has the single signal it needs, while issuer, client ID, scopes, and redirect allow-lists are no longer exposed to unauthenticated callers.

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

**Status: FIXED.** `S3Store` now applies `ServerSideEncryption` to every `PutObject` and
`CreateMultipartUpload` request when configured. Modes: `AES256` or `aws:kms` (with optional
`sse_kms_key_id`). Configurable per storage class (`storage.classes.*.server_side_encryption`),
per legacy backend (`storage.backends.*.server_side_encryption`), and via env vars
(`S3_SERVER_SIDE_ENCRYPTION`, `S3_SSE_KMS_KEY_ID`). `config.prod.yaml` + `config.example.yaml`
default to `AES256` as the recommended defense-in-depth setting. Validation rejects unsupported
modes and KMS key IDs without `aws:kms`.

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

**Current status:** Reduced blast radius compared with the assessed build.

Current code requires a valid OnlyOffice JWT, validates download hosts against the configured OnlyOffice endpoints, and bounds the fetch path with a hardened HTTP client. A compromise of the OnlyOffice tier is still operationally serious, but the unauthenticated callback and arbitrary-host download chain described in the original assessment is no longer present in current code.

#### Scenario 5: Network MITM between sesamefs and S3/Cassandra

**Impact:** S3 SDK uses TLS by default (`ForceAttemptHTTP2: true`). Cassandra connection
depends on configuration — if not using TLS, an attacker on the private subnet can intercept
all database traffic.

---

## Production readiness gaps

### ~~Must-fix before production~~ — ALL COMPLETED

1. ~~**V2-C1 + C-1:** OnlyOffice callback auth + SSRF protection~~ — FIXED: 3-layer defense (JWT verify + URL allowlist + hardened HTTP client)
2. ~~**C-2:** Serve user-uploaded SVG/HTML as `attachment`, not `inline`~~ — FIXED: `forcedAttachmentTypes` for SVG/HTML/XML
3. ~~**M-2:** Replace `allowed_origins: ["*"]` in prod config~~ — FIXED: env var `CORS_ALLOWED_ORIGINS`, wildcard rejected in `Validate()`
4. ~~**CSP header:** Add `Content-Security-Policy`~~ — FIXED: global restrictive default + per-route `SetCSP()` overrides
5. ~~**H-3:** Repo token skips `enforceAccountStatus`~~ — FIXED: `syncAuthMiddleware` calls `enforceAccountStatus()` before accepting repo tokens
6. ~~**H-9:** OIDC DNS rebinding~~ — FIXED: `newOIDCHTTPClient` with DNS pinning + private IP rejection
7. ~~**M-9:** OnlyOffice JWT TTL 8 hours~~ — FIXED: configurable `jwt_ttl_seconds` (default 1h)
8. ~~**H-8:** Zip bomb / dir download DoS~~ — FIXED: configurable `zip_max_entries`/`zip_max_depth`/`zip_max_bytes` + rate limiter

### Should-fix soon

9. **H-5:** Uniform response for share-link token lookup + rate limiting
10. **H-7:** Tighter auth rate limit + per-account throttling
11. ~~**S3 SSE:** Add `ServerSideEncryption` to Put operations~~ — FIXED: configurable SSE (`AES256`/`aws:kms`) applied to `PutObject` + `CreateMultipartUpload`

### Nice to have

12. **M-7:** Distributed session revocation
13. **M-8:** Raise PBKDF2 iterations or deprecate compat mode
14. ~~**Permissions-Policy** header~~ — FIXED: `SecurityHeaders()` now emits `accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()`
15. **Block integrity verification** on download (re-hash and compare)
16. **M-10:** Update frontend dependencies (moment.js, socket.io-client, url-parse)

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
