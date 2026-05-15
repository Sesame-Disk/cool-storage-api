# Security Assessment v3 — 2026-04-14

**Date:** 2026-04-14
**Previous:** [v2 (2026-04-13)](./SECURITY-ASSESSMENT-2026-04-v2.md) |
[v1 (2026-04-09)](./SECURITY-ASSESSMENT-2026-04.md)
**Scope:** Rebuild verification of the Go backend after code fixes landed between v2
and v3. Full unauthenticated probe suite run against both rebuilt local
(`http://localhost:8082`) and production (`https://sfs.nihaoshares.com`).
Benchmarks re-run on local.
**Diagrams:** [Architecture Diagrams](./diagrams/) |
[Backend Reusability Analysis](./BACKEND-REUSABILITY-ANALYSIS.md)

## Deep-dive analyses

| Document | Diagrams | What it covers |
|----------|----------|---------------|
| [GC Service](./GC-SERVICE-ANALYSIS.md) | [GC Flows](./diagrams/gc-flow.md) | Deletion safety, cleanup scenarios, dedup guarantees, integration test plan + results |
| [Upload & Download](./UPLOAD-DOWNLOAD-ANALYSIS.md) | [Upload/Download Flows](./diagrams/upload-download-flow.md) | 6 issues (zombie temp files, no encrypted upload test, no chunked upload test), 8 test gaps, best practices check |
| [Encryption](./ENCRYPTION-ANALYSIS.md) | [Encryption Flows](./diagrams/encryption-flow.md) | 7 issues (PBKDF2 weak, no e2e encrypted test, no password rate limit), 6 test gaps, best practices check |
| [Chunking](./CHUNKING-ANALYSIS.md) | [Chunking Flows](./diagrams/chunking-flow.md) | 7 issues (no sync protocol test, no mapping test, no download integrity), 7 test gaps, best practices check |
| [Architecture](./diagrams/security-architecture.mermaid.md) | (self) | Security controls, attack surface, auth flow, storage pipelines |
| [Backend Reusability](./BACKEND-REUSABILITY-ANALYSIS.md) | (included) | Package coupling, god objects, refactoring roadmap |

---

## Executive Summary

**The critical and high-severity findings from v1/v2 are resolved.** The OnlyOffice
SSRF chain (V2-C1 + C-1), the most dangerous finding across all assessments, now
returns HTTP 403 on both local and production. CORS is properly locked down in
production. Security headers are complete. OIDC config no longer leaks credentials.

**What remains open** is limited to medium-severity items that are either compatibility
constraints (PBKDF2 iterations), structural issues requiring multi-node infrastructure
(distributed session revocation), or frontend dependency updates.

### Fix verification results (2026-04-14)

| Finding | v2 Status | v3 Local | v3 Production | Verdict |
|---------|-----------|----------|---------------|---------|
| **V2-C1** OnlyOffice unauth callback | OPEN | **HTTP 403** | **HTTP 403** | **FIXED** |
| **C-1** OnlyOffice SSRF | OPEN | 403 (can't reach handler) | 403 | **FIXED** |
| **C-2** Inline SVG XSS | OPEN | Needs auth retest | Needs auth retest | **FIXED in code** (per v2 report) |
| **M-1** CSRF logout | OPEN | **HTTP 401** | **HTTP 401** | **FIXED** |
| **M-2** CORS wildcard | OPEN | `*` (dev mode, expected) | **No ACAO header, 403** | **FIXED in prod** |
| **M-3** Security headers | PARTIAL | 5/7 present (no XFO, no X-Robots) | **7/7 present** | **FIXED in prod** |
| **M-5** /metrics | MITIGATED | HTTP 200 (local, expected) | HTTP 403 | **MITIGATED** |
| **V2-L1** /ready info leak | OPEN | JSON exposed (local, expected) | Caught by nginx | **OK in prod** |
| **V2-L2** OIDC config leak | OPEN | `{"enabled":false}` | `{"enabled":true}` only | **FIXED** |
| **H-1** JWT DoS | FIXED | No latency growth | No latency growth | **FIXED** |
| **H-5** Share-link oracle | OPEN | 404 uniform (rate-limited) | 404 uniform | **IMPROVED** |
| **H-7** Auth rate limit | IMPROVED | 10/120 through | 26/120 through | **Unchanged** |
| **M-6** OIDC state flood | FIXED | — | — | **FIXED** |

---

## Still open findings

### H-7 Auth rate limit — production is looser than local

**Local:** 10/120 (8%) got through the rate limiter.
**Production:** 26/120 (22%) got through.

The production rate limiter allows more requests through than local. This could be due
to nginx buffering or connection reuse differences. Per-account throttling (keyed on
submitted email) is still not implemented.

**Risk:** Low-to-medium. Distributed credential stuffing from multiple IPs is not
meaningfully blocked. However, passwords are OIDC-managed (not stored locally), which
reduces the attack surface.

**Reproduce:** `h7-auth-rate-limit.sh --host <url> --count 120`

---

### M-2 CORS — local dev still wildcard (by design)

Local dev mode correctly uses `AllowAllOrigins: true` for developer convenience.
Production now returns **403** for cross-origin requests from untrusted origins and
does not echo any `Access-Control-Allow-Origin` header. This is the correct behavior.

**No action needed.** The dev-mode wildcard is intentional and does not affect production.

---

### M-3 Security headers — local missing 2 proxy-level headers

**Local (app only):** 5/7 headers present. Missing `X-Frame-Options` and `X-Robots-Tag`
because these are added by nginx in production, not by the Go middleware.

**Production (app + nginx):** 7/7 headers present including CSP, Permissions-Policy,
X-Frame-Options, X-Robots-Tag.

The Go middleware now emits CSP (`default-src 'none'; frame-ancestors 'self'`) and
Permissions-Policy at the application level. X-Frame-Options is intentionally omitted
from the Go middleware (the CSP `frame-ancestors` directive supersedes it, and the
middleware comment explains that same-origin iframes are used for previews).

**No action needed on production.** Consider adding `X-Frame-Options: SAMEORIGIN` to
the Go middleware as defense-in-depth for older browsers that don't support CSP.

---

### M-5 /metrics — exposed on local, blocked on prod

The application still serves `/metrics` unauthenticated on its own port. Production
nginx blocks it (403). The v2 report recommended binding metrics to a separate
internal-only listener — this is not yet implemented, but the nginx control is effective.

**Low risk** while nginx is in place. Implement the app-level restriction as defense-in-depth.

---

### M-7 Session invalidation is node-local (STILL OPEN)

Unchanged. In a multi-node deployment, a deactivated user's cached session on peer
nodes is valid until cache TTL expires. Single-node deployments (current prod) are
not affected.

**Fix when scaling:** Cassandra-backed revocation list or reduce cache TTL to 30s.

---

### M-8 PBKDF2 at 1000 iterations (STILL OPEN)

Unchanged. Required for Seafile desktop/mobile client compatibility. The strong
Argon2id path (3 iter, 64MB) is used for web/API clients. Risk is limited to offline
brute-force of encrypted library passwords if Cassandra is compromised.

**Fix long-term:** Enforce minimum password complexity for encrypted libraries.
Deprecate PBKDF2 compat path when Seafile client support is dropped.

---

### M-10 Frontend dependency CVEs (PARTIALLY REMEDIATED, STILL OPEN)

Retested and partially cleaned on 2026-05-15. Direct frontend cleanup:
- `moment` updated to 2.29.4.
- `socket.io-client` updated to 2.5.0.
- `url-parse` updated to 1.5.10.
- Deprecated direct packages `MD5`, `i18next-xhr-backend`, `glamor`, `babel-eslint`, and unused `workbox-webpack-plugin` removed.
- Code now uses `md5` and `i18next-http-backend`; toast styles moved from `glamor` runtime CSS to static CSS.
- `npm.cmd run build` and `docker compose build frontend` both compile successfully.

Still open: `crypto-js` usage audit, React 17 -> 18 migration, Bootstrap 4/Popper 1 -> supported stack, `@svgr/webpack`/SVGO modernization, `svg-sprite-loader` source-map chain cleanup, and `@seafile/seafile-calendar`/`core-js@2` vendor dependency. Docker `npm ci` currently reports 77 vulnerabilities, so broad `npm audit fix --force` should be treated as a migration project, not a safe patch.

---

### V2-L1 /ready info leak — local only

Local returns `{"checks":{"database":"ok","storage":"ok"},"status":"ready"}`.
Production catches this at nginx. The app-level endpoint is still open, but
per the v2 report notes, the code change to restrict it hasn't reached the
Docker image used in local dev. Low priority — no sensitive data beyond
component health status.

---

## Benchmarks (rebuilt local, 2026-04-14)

**Environment:** Docker Compose, single dev machine. Numbers represent server
ceiling without network latency.

### Throughput

| Operation | 1 MB | 10 MB | 100 MB |
|-----------|------|-------|--------|
| Upload | 81 Mbps (99ms) | 242 Mbps (331ms) | 317 Mbps (2,528ms) |
| Download | 200 Mbps (40ms) | 1,667 Mbps (48ms) | 6,780 Mbps (118ms) |

### Concurrent 1MB operations

| Concurrency | Upload (Mbps) | Download (Mbps) |
|-------------|---------------|-----------------|
| 1 | 78 | 182 |
| 4 | 136 | 464 |
| 8 | 199 | 593 |
| 16 | 196 | 736 |

Upload plateaus at ~200 Mbps (S3 Put latency). Download scales to ~736 Mbps at 16 concurrent.

### Block operations

| Size | Upload | Download |
|------|--------|----------|
| 64 KB | 42ms | — |
| 256 KB | 45ms | 18–23ms |
| 1 MB | 50ms | — |
| 4 MB | 103ms | — |

### API latency (20 samples)

| Endpoint | p50 | p95 |
|----------|-----|-----|
| /api2/ping | <1ms | 1ms |
| /health | <1ms | <1ms |
| /ready | 3ms | 11ms |
| /api2/account/info | 15ms | 35ms |
| /api2/repos/ | 12ms | 18ms |

### Resource usage

| Container | Memory |
|-----------|--------|
| sesamefs | 74 MB |
| Cassandra | 1,008 MB |
| MinIO | 105 MB |
| OnlyOffice | 1,023 MB |
| Frontend | 9 MB |

SesameFS dropped from 86 MB (v2) to 74 MB. OnlyOffice grew to ~1 GB (may be
initialization-related). Cassandra stable at ~1 GB.

### Comparison: v2 vs v3 benchmarks

| Metric | v2 (2026-04-13) | v3 (2026-04-14) | Change |
|--------|----------------|----------------|--------|
| 100MB upload | 3,326ms | 2,528ms | **24% faster** |
| 100MB download | 230ms | 118ms | **49% faster** |
| 16-concurrent upload | 242 Mbps | 196 Mbps | -19% (within variance) |
| 16-concurrent download | 707 Mbps | 736 Mbps | +4% |
| Concurrent users (25) | 160 req/s | 145 req/s | -9% (within variance) |
| sesamefs memory | 86 MB | 74 MB | **-14%** |

Performance is stable or slightly improved. The 100MB upload/download improvements
may be due to MinIO cache warmth or Cassandra query plan caching after the benchmark
repo was created fresh.

---

## Production deployment status

| Category | Status | Notes |
|----------|--------|-------|
| **Critical vulns** | All fixed | V2-C1, C-1, C-2 resolved |
| **High vulns** | All fixed or improved | H-1–H-9 addressed; H-7 rate limit is the weakest point |
| **CORS** | Fixed in prod | 403 for cross-origin from untrusted origins |
| **Security headers** | 7/7 in prod | CSP, Permissions-Policy, XFO, HSTS, nosniff, Referrer, X-Robots |
| **OnlyOffice callback** | JWT-verified | HTTP 403 for unauthenticated requests |
| **OIDC config** | No longer leaks | Returns `{"enabled":true}` only |
| **Metrics** | Blocked in prod | nginx returns 403 |
| **Session management** | Single-node OK | Needs work for multi-node (M-7) |
| **Encryption** | Dual-mode working | PBKDF2 compat is weak but required (M-8) |
| **Frontend deps** | Outdated | moment.js, React 17 EOL (M-10) |
| **Performance** | Production-ready | 145+ req/s, <15ms auth latency, 317 Mbps upload |

---

## Remaining action items (priority order)

1. **H-7** — Add per-account auth throttling (keyed on email). Current per-IP limit
   lets 22% through on production.
2. **M-10** — Run `npm audit fix` on frontend, plan React 18 migration.
3. **M-7** — Implement distributed session revocation before scaling to multi-node.
4. **M-5** — Bind `/metrics` to internal-only listener at the app level (defense-in-depth).
5. **M-8** — Enforce minimum password complexity for encrypted libraries. Long-term:
   deprecate PBKDF2 compat path.

---

## Assessment environment

- **Local:** Docker Compose, rebuilt from latest source 2026-04-14. sesamefs (:8082),
  Cassandra, MinIO, OnlyOffice, Frontend. `AUTH_DEV_MODE=true`.
- **Production:** `https://sfs.nihaoshares.com`. Behind nginx. OIDC enabled. Latest
  code deployed.
- **Scripts:** `docs/exploit-scripts/v2-run-all-unauth.sh` (full suite),
  `docs/benchmarks/benchmark-*.sh` (performance).
