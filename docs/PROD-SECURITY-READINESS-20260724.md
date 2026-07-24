# Production & Security Readiness — Upload / Download / Sharing / Roles

**Date:** 2026-07-24
**Scope:** Readiness to run `cool-storage-api` (SesameFS) in production under our **IOCD** controls, focused on the **security posture** and the **prod-readiness of the upload, download, and sharing paths and the user-role/permission model**.
**Method:** Findings verified against source (`internal/api/**`, `configs/config.prod.yaml`, `.env.prod.example`, nginx-multiregion) and the issue backlog (`docs/KNOWN_ISSUES.md`, `SECURITY-ASSESSMENT-2026-04*`, `UPLOAD-*`). Each item carries a severity, status, and a concrete reference.

> **Empirical verification (2026-07-24):** the HIGH blockers were re-tested against a live two-instance multiregion stack (`docker-compose.mr.yaml`: `sesamefs-usa` + `sesamefs-eu` sharing one Cassandra + one MinIO behind the nginx LB). Results: **B1 CONFIRMED**, **B2 CONFIRMED** (but Medium, not High — see below), **B5 CONFIRMED**. B1/B5 are multi-instance-only (no impact single-node). Details in the "Empirical results" section at the end.

> Companion depth docs: [UPLOAD-PERFORMANCE-SECURITY-2026-06.md](./UPLOAD-PERFORMANCE-SECURITY-2026-06.md), [UPLOAD-RESUME-ANALYSIS-20260619.md](./UPLOAD-RESUME-ANALYSIS-20260619.md), `KNOWN_ISSUES.md`. This doc is the short readiness view, not a replacement for them.

---

## Verdict

> ⚠️ **New HIGH finding (2026-07-24 verification, live-confirmed):** password-protected **share links leak file content without the password** via the public `/api/v2.1/share-links/:token/bootstrap[/files]` endpoints for inline text/markdown files (≤1 MB). The raw download path (`/d/:token?dl=1`) is correctly gated (403), but the bootstrap JSON embeds `fileContent` with no password check. This is a straightforward auth-control bypass (single-node reproducible; only the link token is needed, no hash) and is **new work not in the original doc** — see NF-1 in the Verification addendum. Treat as a go/no-go security blocker.

**Conditional-go for a single-region / sticky-routed deployment. Not ready for an unpinned multi-region deployment as-is.** The authentication, secrets, OIDC, CORS, and share-token cryptography are in good shape and largely enforced-by-default. The gating items are (a) **multi-region request routing** for stateful upload/SSO paths (B1/B5, both empirically reproduced) and (b) **no dedicated rate limiting** on the public upload/download/share surfaces (B4). A **cross-library block-read authorization gap (BOLA)** is real and reproduced but **Medium** severity (authenticated + requires knowing the target block's 256-bit hash), not a hard go/no-go blocker. Garbage collection must remain **disabled fleet-wide** (safe by design, but means no space reclamation yet).

### Must-fix before prod (blockers)

| # | Blocker | Area | Severity | Live-verified 2026-07-24 | Ref |
|---|---------|------|----------|--------------------------|-----|
| B1 | **Chunk-upload state is node-local** — multi-node/region routing that isn't sticky-by-upload-token breaks finalization. `nginx-multiregion.conf` does hostname/round-robin routing only; no sticky mechanism. Multi-instance only; no single-node impact. | Upload / Multiregion | HIGH | ✅ **CONFIRMED** — split upload (chunk0→usa, final→eu, same shared token) silently dropped the file; `eu` logged `FINAL_CHUNK_BUT_INCOMPLETE first_gap=0-4194303` and returned HTTP 200 `{"success":true}` with no dirent. | `seafhttp.go:565` (var chunkManager; doc's `:375` was imprecise); `KNOWN_ISSUES.md` ISSUE-UPLOAD-CHUNK-MULTINODE-01; `configs/nginx-multiregion.conf` |
| B4 | **No dedicated rate limit** on `upload` / `download` / `/api/v2/blocks/upload` / share-link paths — public upload links are unauthenticated writes with only generic traffic quota. | Sharing / Security | HIGH | (not re-tested; static finding stands) | `SECURITY-ASSESSMENT-2026-04.md` H-5 |
| B5 | **Desktop-client SSO pending-token store is in-memory**, not distributed — poll and callback can land on different instances → token never delivered. | Auth / Multiregion | HIGH | ✅ **CONFIRMED** — drove a full OIDC desktop-SSO flow (mock IdP) with callback on `usa`: `usa` poll → `status:success` + apiToken; `eu` poll → `status:waiting` (never received it). Multi-instance only. | `server.go:53–63` (in-memory `clientSSOStore`), write `:2157`, read `:1396`; `OIDC.md:644` (self-admits the gap) |

**Reclassified out of blockers:**

| # | Former blocker | New status | Basis |
|---|----------------|-----------|-------|
| B2 | Cross-library block read (BOLA) | **MEDIUM security gap** (still fix, not a hard go/no-go) | ✅ Reproduced cross-user: attacker (plain user) is 403-denied on the victim's library directly, but read the victim's block **byte-for-byte** through their *own* library. Gated by knowing the block's 256-bit hash → Medium, not High. Doc's surface list is **stale**: the standalone bare-SHA v2 GET was removed and `CheckBlocks` is now upload-session-gated; the live surface is `SyncHandler.GetBlock` (`sync.go:1139` checks the URL repo, not the block's owning library). `KNOWN_ISSUES.md:317`'s Medium rating is the accurate one. |

Accepted-but-track for this release: **GC stays disabled** on every replica/DC (X1 physical-delete ABA, X2 cross-DC reference visibility) — `config.prod.yaml:292,306`.

---

## 1. Security posture (auth, secrets, transport)

**What is solid (verified, enforced by default):**

- **OIDC**: audience validation, PKCE, nonce, and signature verification are on by default (`config.prod.yaml:191–192`; `oidc.go:615–633`). ✅
- **Tokens/sessions**: upload tokens are Cassandra-backed and multi-node safe (`server.go:190–192`); session tokens are DB-stored, hashed, and TTL'd (`session.go:323–360`). ✅
- **Secrets**: externalized to env — no hardcoded credentials; `SHARE_LINK_HMAC_KEY` is **required at boot** in prod (server refuses to start without it) (`config.prod.yaml:152`, `.env.prod.example:332`). ✅
- **CORS**: wildcard `*` rejected in prod; empty allowlist denies by default, operator must set `CORS_ALLOWED_ORIGINS` (`server.go:264–293`). ✅
- **Headers/TLS**: HSTS + nosniff via `SecurityHeaders()` in prod mode; cookie `Secure` flag driven by `Request.TLS`. TLS termination is assumed **external** (central nginx) — no in-process TLS. ✅ / ⚠ operator-dependent.

**Gaps:**

| ID | Finding | Sev | Status | Ref |
|----|---------|-----|--------|-----|
| SEC-1 | Cross-library block read (BOLA) — see **B2** | **MEDIUM** (was High) | OPEN (live-verified 2026-07-24) | `KNOWN_ISSUES.md:317`; live surface `sync.go:1139` `GetBlock` |
| SEC-2 | No dedicated rate limiting on upload/download/blocks/share surfaces — see **B4** | HIGH | OPEN | `SECURITY-ASSESSMENT-2026-04.md` H-5 |
| SEC-3 | `sesamefs_auth` cookie `httpOnly=false` (`/oauth/callback/`) — the cookie's token is a live, replayable session/API bearer (`server.go:626–628`), so an XSS yields **full session-token theft** (sync-client TTL up to 180d), not merely a read surface | **HIGH** (was Medium) | BY DESIGN (reassess) | `server.go:2169`, `:626` |
| SEC-4 | Accounts M2M provisioning authenticated by API key only — no request signing (HMAC/JWT) on `/admin/organizations/:id` | MEDIUM | OPEN | `PLANS-AND-PERMISSIONS.md` §4.3 |
| SEC-5 | Multi-region sticky routing not encoded in nginx config; must live at external LB — see **B1/B5** | HIGH | OPEN | `configs/nginx-multiregion.conf` |

---

## 2. Upload readiness

| ID | Finding | Sev | Status | Ref |
|----|---------|-----|--------|-----|
| UP-1 | Chunk-upload state node-local → multi-region blocker without sticky-by-token — **B1** | HIGH | OPEN | `seafhttp.go:375` |
| UP-2 | 1 global Paxos LWT **per block** under multi-DC `SERIAL` (~128 cross-region rounds / 1 GB). Pre-existing, both governed upload modes pay it. Latency cost, not a blocker. | HIGH (perf) | OPEN | `UPLOAD-PERFORMANCE-SECURITY-2026-06.md` P-4 |
| UP-3 | TOCTOU quota check across concurrent same-org uploads to different repos | MEDIUM | OPEN | ISSUE-QUOTA-RESERVATION-01; UPLOAD-… S-4 |
| UP-4 | `/tmp` staging admission budget (`chunked_staging_max_bytes`) defaults to `0` = disabled — operator must set a real per-node value | MEDIUM | GUARD ADDED, CONFIG REQUIRED | UPLOAD-… S-3 |
| UP-5 | Permission not re-checked during long chunked uploads (authorized at session start only) | MEDIUM | OPEN | UPLOAD-DOWNLOAD-ANALYSIS |
| UP-6 | `max_upload_mb` now enforced on chunked uploads (413 fail-closed) | MEDIUM | ✅ FIXED | UPLOAD-… S-2 |
| UP-7 | Resumable `file-uploaded-bytes` is a safe stub (`0`); true resume not wired. Safe, not a blocker. | LOW | BY DESIGN | UPLOAD-RESUME-ANALYSIS-20260619 |

---

## 3. Download readiness

| ID | Finding | Sev | Status | Ref |
|----|---------|-----|--------|-----|
| DL-1 | `StreamBlocks` returns void → mid-stream block/decrypt failure logs "complete" and **over-counts billed traffic** | HIGH | OPEN | `KNOWN_ISSUES.md:816` ISSUE-STREAMBLOCKS-VOID-01 |
| DL-2 | ZIP directory download can **truncate after `200 OK`** on a late storage/decrypt error; client can't distinguish from corruption | HIGH | OPEN (preflight improved) | `KNOWN_ISSUES.md:785` ISSUE-ZIP-STREAM-LATEFAIL-01 |
| DL-3 | Encrypted `encrypted`-flag probe used to **fail open** (served plaintext as ciphertext) on Cassandra error — closed 2026-07-22 | CRITICAL | ✅ FIXED | `KNOWN_ISSUES.md:4751` ISSUE-ENCRYPTED-FLAG-UNCHECKED-01 |
| DL-4 | Deleted file returns **503-forever**, never 404 (cross-DC lag ≡ absence); sync clients retry unbounded | MEDIUM | OPEN (multi-DC tradeoff) | `KNOWN_ISSUES.md:4664` ISSUE-DOWNLOAD-NO-404-01 |
| DL-5 | Download authorization **is** re-checked per request via a live-DB `HasLibraryAccess` call (`seafhttp.go:3490`, `:3544`, `:4080`), so revocation blocks *new* requests. The real (narrower) gap is only **mid-stream**: an already-streaming large download has no in-flight re-check. | LOW | OPEN | `seafhttp.go:3490` "the ONE download gate" |
| DL-6 | Encrypted downloads omit `Content-Length` **only on the v2 inline-view/share-raw surfaces** (`fileview.go:717`, `sharelink_view.go:944`); the primary SeafHTTP download path sets it from plaintext `size_bytes` (`streamFileFromBlocks`, `seafhttp.go:3769`) | LOW | OPEN (scoped) | `seafhttp.go:3769`; `fileview.go:717` |
| DL-7 | Block integrity not re-verified (re-hashed) on download — relies on object store | MEDIUM (defense-in-depth) | OPEN | UPLOAD-DOWNLOAD-ANALYSIS |

---

## 4. Sharing readiness (share links, upload links)

**Solid (verified, with corrections):** tokens are `crypto/rand` 128-bit (`share_links.go:680`) ✅; password cookie compare is constant-time (`subtle.ConstantTimeCompare`, `sharelink_view.go:1702`) ✅; permission scopes are re-checked per request — but the enforcement is at the serve paths (`sharelink_view.go:773/1455/1625`), **not** the cited `:574–601` (which is just a parser) ⚠️ cite-fix. **Corrections:** expiry/download-cap enforcement is **per access but the cap increment is fire-and-forget async → the download cap and `single_use` are race-bypassable** under concurrency (`sharelink_view.go:788–799`) ⚠️; and an **encrypted library is blocked only at serve/decrypt time (403), NOT at link-creation** — `CreateShareLink` has no encryption check, so a link can be minted, it just can't be decrypted without the creator's session ⚠️ (see SH-3).

| ID | Finding | Sev | Status | Ref |
|----|---------|-----|--------|-----|
| SH-1 | Public **upload links** = unauthenticated write with no per-IP/request rate limit — see **B4** | HIGH | OPEN | `SECURITY-ASSESSMENT-2026-04.md` H-5; `sharelink_view.go` |
| SH-2 | **No org-internal scope** for share links — every public link is token-only, accessible anonymously and cross-org; no "org members only" option | MEDIUM | OPEN / BY DESIGN | `BUG-SHARE-LINK-NO-INTERNAL-SCOPE-20260618.md`; `sharelink_view.go` resolveShareLink |
| SH-3 | Encrypted share link decrypts using **creator's** key, not receiver auth — mixed authorization model; breaks if creator's decrypt session is revoked | MEDIUM | OPEN | `sharelink_view.go:872` |
| SH-4 | Link passwords hashed with **bcrypt** (`bcrypt.DefaultCost` = **10**, not the doc's "12"), not the preferred Argon2id | LOW | OPEN (cost corrected) | `share_links.go:563` (`bcrypt.DefaultCost`) |
| SH-6 | **NEW (live-confirmed):** password-protected share links leak inline text/markdown `fileContent` **without a password** on the public bootstrap endpoints — password-control bypass / content disclosure | HIGH | OPEN | `sharelink_view.go:1135–1154` (no `verifyShareLinkPasswordCookie` gate before content read); routes `server_routes.go:311–314`. See **NF-1**. |

---

## 5. User roles & permissions readiness

**Solid (verified, with corrections):** role hierarchy is **superadmin(5) > owner(4) > admin(3) > user(2) > readonly(1) > guest(0)** — the doc **omitted superadmin(5)**; numeric map is at `middleware/permissions.go:667–674` (not the cited `:114–120`), owner auto-allow confirmed ✅. `RequireScope()` middleware confirmed ✅. Encrypted-library "share block" is **serve-time only, not create-time** (see §4 correction) ⚠️. **RB-6 RESOLVED:** an audit of every admin route registration (`admin_routes.go:15`, `org_admin_routes.go:21`, GC `server_routes.go:204`, api-keys `:269`) found **no missing guard** — all gated; cross-org isolation is enforced per-handler via `requireOrgAccess` and was live-tested (tenant admin → 403 on all other-org admin routes). ✅

| ID | Finding | Sev | Status | Ref |
|----|---------|-----|--------|-----|
| RB-2 | OIDC role claims ↔ manual role overrides: **reconciliation/authoritative-source rule undefined** — re-sync on login can conflict with admin overrides | MEDIUM | OPEN (design debt) | `PLANS-AND-PERMISSIONS.md`; `ROLES-AND-PERMISSIONS.md` |
| RB-3 | **No `permission_audit_logs`** — share/group membership changes aren't logged (compliance/forensics gap under IOCD) | MEDIUM | OPEN (stub) | `ADMIN-FEATURES.md`; `KNOWN_ISSUES.md` |
| RB-4 | Permission checks not re-evaluated mid-operation (revoke during ZIP/bulk op continues to completion) | MEDIUM | OPEN | `libraries.go`, `seafhttp.go` (mirrors DL-5/UP-5) |
| RB-6 | `RequireScope()` per-route coverage — **RESOLVED: no gap.** All admin route groups gated; org-scoped handlers all call `requireOrgAccess` (only 501 stubs don't). Live-tested cross-org isolation holds. **Latent fragility:** the `:org_id` match is per-handler, not middleware — a future handler that forgets it would be a silent cross-tenant BOLA (see NF-5). | LOW (was VERIFY) | ✅ VERIFIED | `admin_routes.go:15`, `org_admin_routes.go:21`, `org_admin.go:66` |

---

## 6. Multi-region / operational notes

- **GC disabled fleet-wide** (X1 physical-delete ABA, X2 cross-DC reference visibility). Safe (no data loss) but **no space reclamation** — plan capacity accordingly. `config.prod.yaml:292,306`.
- **Multi-DC `SERIAL` Paxos** is the production posture (`dc-na:1,dc-eu:1,dc-asia:1`); do **not** switch block-metadata LWTs to `LOCAL_SERIAL` (diverges placement). `UPLOAD-… P-4`.
- **No pre-serve replication validation gate** — verify `nodetool status` / keyspace RF across all DCs before cutover. `DEPLOY.md`.
- **Sticky-by-upload-token routing** must be added at the external LB in front of nginx (B1/B5); nginx-multiregion.conf does not provide it.

---

## Pre-prod checklist

0. **Share-link password bypass (NF-1, HIGH — new):** gate the `…/bootstrap[/files]` `fileContent` read behind `verifyShareLinkPasswordCookie` (`sharelink_view.go:1135`). Single-node exploitable; fix before go-live.
1. **Routing (B1/B5):** external LB sticky sessions keyed on upload token; migrate desktop-SSO pending-token store to Cassandra before any multi-instance rollout. *(Both live-confirmed 2026-07-24.)*
2. **AuthZ (B2 — Medium):** add library-scoped read authorization to `SyncHandler.GetBlock` (`sync.go:1139`) — verify block↔repo membership, not just URL-repo access.
3. **Abuse control (B4):** dedicated rate limits on upload/download/blocks paths; throttle anonymous upload-link writes. *(Note: share-link routes already carry a per-IP limiter `slRL`.)*
4. **Config:** set `chunked_staging_max_bytes` (UP-4); reject `max_upload_mb=0` + staging=0 together (NF-7); confirm `CORS_ALLOWED_ORIGINS`, `SHARE_LINK_HMAC_KEY`, external TLS + HSTS.
5. **Compliance (RB-3/NF-6):** stand up permission/login audit logging — and fix the **delete-only** audit trail (grants/membership adds currently unlogged) before go-live under IOCD.
6. **Harden (NF-3/NF-4/NF-5):** reassess the `httpOnly=false` session cookie (token-theft surface); fix `handleAutoLogin` `Secure=false`; hoist org-match into `/org/:org_id/admin` middleware.
7. **Accept + document:** GC stays off; 503-not-404 on deletes; resume is a safe stub; download-cap/`single_use` are best-effort (race, NF-2).

---

## Summary table

| Area | Blockers | High | Medium | Verified-good |
|------|----------|------|--------|---------------|
| Security posture | B4, B5 | — | SEC-1(B2, was High), SEC-3/4 | OIDC, tokens, secrets, CORS |
| Upload | B1 | UP-2 | UP-3/4/5 | UP-6 (max_upload_mb) |
| Download | — | DL-1, DL-2 | DL-4/5/6/7 | DL-3 (fail-open closed) |
| Sharing | B4 | SH-1 | SH-2/3 | expiry, caps, scopes, CT-compare, crypto tokens |
| Roles | — | — | RB-2/3/4/6 | role hierarchy, RequireScope |

> Live-verified 2026-07-24: B1 ✅, B2 ✅ (Medium), B5 ✅. See "Empirical results" below.

---

## Empirical results (2026-07-24)

**Test bed:** `docker-compose.mr.yaml` — two app instances (`sesamefs-usa` :8088, `sesamefs-eu` :8000) sharing **one** Cassandra + **one** MinIO, behind the nginx LB (:18080). Both instances reachable directly on the host, so requests could be deterministically routed to a specific node. `AUTH_DEV_MODE=true` (dev tokens: admin=`dev-token-admin` org `…001`, user=`dev-token-user` org `…001`). This is the exact "shared state in Cassandra/MinIO, node-local state per process" topology the blockers concern.

| # | Result | What was observed |
|---|--------|-------------------|
| **B1** | ✅ CONFIRMED (multi-node only) | Upload token is Cassandra-backed (shared), so the same token is valid on both nodes — but staged chunk bytes are node-local (`os.TempDir()` temp file tracked by a process-global in-memory `chunkManager`, `seafhttp.go:565`). **Control** (both chunks → usa): file finalized intact, `size=8388608`. **Split** (chunk0 → usa, final chunk → eu, same token): `eu` created a fresh empty tracker, logged `FINAL_CHUNK_BUT_INCOMPLETE (prefix missing; possible tracker split) first_gap=0-4194303 received=4194304/8388608`, returned HTTP 200 `{"success":true}` **with no dirent**, and the file **never appeared**. Silent data loss on non-sticky routing. |
| **B2** | ✅ CONFIRMED — Medium | Cross-user: victim (admin) stored a secret block in library V; attacker (plain user 002) owns library A. Attacker → `GET /seafhttp/repo/{V}/block/{hash}` = **403** (per-library isolation holds). Attacker → `GET /seafhttp/repo/{A}/block/{hash}` (own library, victim's hash) = **200 with the victim's exact bytes**. `GetBlock` (`sync.go:1139`) authorizes the URL's repo but fetches the block by `(org, sha256)` with no library-membership check. Severity Medium: authenticated, and requires knowing the 256-bit content hash (supplied directly in the test). |
| **B5** | ✅ CONFIRMED (multi-instance only) | Drove a full real OIDC desktop-SSO flow (mock IdP; discovery/authorize/token/JWKS + RS256 signature, nonce, issuer all validated) with the callback completing on `usa`. `usa` poll → `{"status":"success","apiToken":"…","username":"ssouser@…"}`; `eu` poll → `{"status":"waiting"}`. `clientSSOStore` (`server.go:53`) is a per-process in-memory map — success written on one instance is invisible on another, so a poll routed to a different instance than the callback never gets the token. "SSO works in the desktop client" only exercises the same-instance happy path and does not refute this. |

**Net change to the readiness picture:** blockers are **B1, B4, B5** (B1/B5 multi-instance-only; all three moot on a single sticky-routed node), **plus NF-1 (share-link password bypass, HIGH, single-node)**. B2 is a real **Medium** BOLA to fix but not a hard go/no-go.

---

## Verification addendum (2026-07-24)

The whole document's remaining assertions (Sections 1–6) were re-checked against source, with live tests where feasible. Below: **new issues found**, then a **corrections table** for claims that were wrong/overstated/stale. Most section claims held; only the deltas are listed.

### New issues (not in the original doc)

| ID | Severity | Finding | Evidence / status |
|----|----------|---------|-------------------|
| **NF-1** | **HIGH** | **Share-link password bypass.** Password-protected share links to inline text/markdown files (≤1 MB) return `fileContent` to an anonymous, password-less caller via `GET /api/v2.1/share-links/:token/bootstrap` and `…/files/bootstrap`. The raw path `/d/:token?dl=1` is correctly gated (403). | **LIVE-CONFIRMED**: created a password link, anon bootstrap returned the secret bytes; raw path = 403. Fix: gate `buildShareFileBootstrapResponse` content read on `verifyShareLinkPasswordCookie` (`sharelink_view.go:1135`). |
| **NF-2** | **MEDIUM** | **Download-cap / `single_use` race.** `max_downloads` and single-use are checked against a count whose increment is fire-and-forget (`go func()`), so N concurrent requests all pass a cap of 1 and single-use links can be consumed multiple times. | `sharelink_view.go:788–799`. Static; contradicts the "caps enforced per access = verified-good" framing. |
| **NF-3** | **MEDIUM** | **SEC-3 is worse than stated** — the `httpOnly=false` cookie is a live session/API bearer → XSS = token theft, not a read surface. | `server.go:626–628`, `:2169`. See SEC-3 row. |
| **NF-4** | **LOW–MED** | **`handleAutoLogin` hardcodes cookie `Secure=false`** (unlike the callback path which derives it from `Request.TLS`). If reachable in prod behind external TLS, the session cookie ships without Secure. | `server.go:1440`. Confirm the route's prod exposure. |
| **NF-5** | **LOW** (latent) | **Org-scoped authz is per-handler, not middleware.** The `/org/:org_id/admin` group checks only JWT-org *role*, not that `:org_id` == caller's org; the cross-tenant gate is `requireOrgAccess` copy-pasted into ~50 handlers. All current handlers call it (verified + live-tested), but a new handler that forgets it = silent cross-tenant BOLA. | `org_admin_routes.go:21`, `org_admin.go:66`. Hoist an org-match check into the sub-group. |
| **NF-6** | **LOW** (compliance) | **Audit trail is delete-only.** An `audit_log` table exists but only deletion/GC cascades write to it; share creation and group-member *adds* write nothing. A one-sided trail is arguably worse than none under IOCD. Compounds RB-3. | `migrations/001_initial_schema.cql:1316`; no INSERT in `share*.go`/`org_admin_groups.go`. |
| **NF-7** | **LOW** (config) | **Both upload size guards can be disabled together.** `max_upload_mb=0` (documented "unlimited") + default `chunked_staging_max_bytes=0` ⇒ first chunk does `Truncate(attacker_total)` unbounded. Shipped prod/usa/eu configs set non-zero `max_upload_mb`, so latent. UP-6's "FIXED" is config-dependent. | `seafhttp.go:1384,606,648`. Add a config invariant rejecting both-zero. |

### Corrections to existing claims

| Claim | Original | Correction (verified) |
|-------|----------|-----------------------|
| SEC-1 (B2) | HIGH blocker | **MEDIUM**; live surface is `GetBlock` (`sync.go:1139`), other cited surfaces stale. |
| SH-4 | bcrypt "cost 12" | bcrypt **cost 10** (`bcrypt.DefaultCost`). |
| §4 "encrypted library blocked server-side" | implies create-time block | **Serve-time only**; `CreateShareLink` has no encryption check. |
| §4 "scopes re-checked at `:574–601`" | cite | `:574–601` is a parser; enforcement is at `:773/1455/1625`. |
| DL-5 | "not re-checked per request" | **Overstated** — per-request re-auth exists (`seafhttp.go:3490`); real gap is mid-stream only. |
| DL-6 | "encrypted omit Content-Length" | **Partly stale** — main SeafHTTP path sets it; only v2 view/share surfaces omit. |
| §5 role hierarchy | "owner(4)…, `:114–120`" | Omits **superadmin(5)**; map at `:667–674`. |
| §6 multi-DC layout | `dc-na:1,dc-eu:1,dc-asia:1` | Stale — shipped config is single-DC `datacenter1:1` (SERIAL posture correct). |
| Secrets (§1) | HMAC key "required if empty" | Stronger — also rejects the shipped **default** value in prod (`config.go:1526`). |
| CORS / SEC-4 refs | `server.go:264–293` / "§4.3" | Ref drift — CORS fail-fast is `config.go:1529`; M2M route is `PUT …/admin/organizations/:org_id`. |

### Confirmed as-written (no change)
UP-2, UP-3, UP-4, UP-5, **UP-6 (live-verified 413)**, UP-7; DL-1, DL-2, **DL-3 (critical fail-open genuinely closed across all surfaces)**, DL-4, DL-7; SH-2, SH-3; RB-2, RB-3, RB-4; §1 solid claims 1–5 (OIDC defaults on, sessions hashed+TTL, secrets, CORS default-deny, HSTS/nosniff); GC-disabled, SERIAL posture, no-replication-gate. Full details in the five verification sub-reports (scratchpad).
