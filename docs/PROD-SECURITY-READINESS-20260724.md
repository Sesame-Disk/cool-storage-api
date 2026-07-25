# Production & Security Readiness — Upload / Download / Sharing / Roles

**Date:** 2026-07-24 · **Code-validated:** 2026-07-25
**Scope:** Readiness to run `cool-storage-api` (SesameFS) in production under our **IOCD** controls, focused on the **security posture** and the **prod-readiness of the upload, download, and sharing paths and the user-role/permission model**.
**Method:** Findings verified against source (`internal/api/**`, `configs/config.prod.yaml`, `.env.prod.example`, nginx-multiregion) and the issue backlog (`docs/KNOWN_ISSUES.md`, `SECURITY-ASSESSMENT-2026-04*`, `UPLOAD-*`). Each item carries a severity, status, and a concrete reference.

> **Empirical verification (2026-07-24):** the HIGH blockers were re-tested against a live two-instance multiregion stack (`docker-compose.mr.yaml`: `sesamefs-usa` + `sesamefs-eu` sharing one Cassandra + one MinIO behind the nginx LB). Results: **B1 CONFIRMED**, **B2 CONFIRMED** (but Medium, not High — see below), **B5 CONFIRMED**. B1/B5 are multi-instance-only (no impact single-node). Details in the "Empirical results" section at the end.

> **Code validation (2026-07-25):** every finding below was re-checked against `main` at `0dac50993`. **All findings hold.** One new disclosure was found inside NF-1's blast radius (an OnlyOffice download token minted with no password check — see NF-1), one claim was narrowed (B4 is partly stale: several surfaces *do* have limiters), one was sharpened (NF-7: the staging guard is disabled in *every* shipped config, prod included), and one was made precise (NF-6 is delete-plus-org-update, not delete-only). Line-number citations were **replaced with symbol names** throughout — PR-10 shifted `sync.go` by ~30 lines and invalidated several of them, which is exactly the drift this rewrite removes. Validation deltas are tabulated in the "Code validation" section at the end.

> **Every open finding here now carries an `ISSUE-*` id in [KNOWN_ISSUES.md](./KNOWN_ISSUES.md)**, which is the registry of record for status. This document keeps the evidence and the severity reasoning; that one keeps identity and current state. Do not track a finding's status in both. For the one-screen view across all audits, see [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md).

> Companion depth docs: [UPLOAD-PERFORMANCE-SECURITY-2026-06.md](./UPLOAD-PERFORMANCE-SECURITY-2026-06.md), [UPLOAD-RESUME-ANALYSIS-20260619.md](./UPLOAD-RESUME-ANALYSIS-20260619.md), `KNOWN_ISSUES.md`. This doc is the short readiness view, not a replacement for them.

---

## Verdict

> ⚠️ **New HIGH finding (2026-07-24 verification, live-confirmed; widened 2026-07-25):** password-protected **share links leak file content without the password** via the public `/api/v2.1/share-links/:token/bootstrap[/files]` endpoints. The raw download path (`/d/:token?dl=1`) is correctly gated (403), but the bootstrap JSON embeds `fileContent` with no password check. **Code validation found a second leak on the same missing gate:** when OnlyOffice is enabled and the shared file is an office document, the same builder mints a real **download token** and returns it to the anonymous caller — no 1 MB ceiling, not limited to text. This is a straightforward auth-control bypass (single-node reproducible; only the link token is needed, no hash) — see NF-1 and **ISSUE-SHARELINK-PASSWORD-BYPASS-01**. Treat as a go/no-go security blocker.

**Conditional-go for a single-region / sticky-routed deployment. Not ready for an unpinned multi-region deployment as-is.** The authentication, secrets, OIDC, CORS, and share-token cryptography are in good shape and largely enforced-by-default. The gating items are (a) **multi-region request routing** for stateful upload/SSO paths (B1/B5, both empirically reproduced) and (b) **no dedicated rate limiting** on the public upload/download/share surfaces (B4). A **cross-library block-read authorization gap (BOLA)** is real and reproduced but **Medium** severity (authenticated + requires knowing the target block's 256-bit hash), not a hard go/no-go blocker. Garbage collection must remain **disabled fleet-wide** (safe by design, but means no space reclamation yet).

### Must-fix before prod (blockers)

| # | Blocker | Area | Severity | Live-verified 2026-07-24 | Issue id | Code ref (symbol) |
|---|---------|------|----------|--------------------------|----------|-------------------|
| NF-1 | **Share-link password bypass** — password-protected links serve `fileContent` and an OnlyOffice download token to anonymous callers. Single-node reachable. | Sharing / Security | HIGH | ✅ **CONFIRMED** (content); OnlyOffice half found in code validation | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | `sharelink_view.go` `buildShareFileBootstrapResponse`, `buildOnlyOfficeShareBootstrap` |
| B1 | **Chunk-upload state is node-local** — multi-node/region routing that isn't sticky-by-upload-token breaks finalization. `nginx-multiregion.conf` does hostname/round-robin routing only; no sticky mechanism. Multi-instance only; no single-node impact. | Upload / Multiregion | HIGH | ✅ **CONFIRMED** — split upload (chunk0→usa, final→eu, same shared token) silently dropped the file; `eu` logged `FINAL_CHUNK_BUT_INCOMPLETE first_gap=0-4194303` and returned HTTP 200 `{"success":true}` with no dirent. | `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | `seafhttp.go` `var chunkManager` (process-global); `configs/nginx-multiregion.conf` |
| B4 | **No dedicated rate limit** on the seafhttp upload / download / block routes. ⚠️ **Narrowed 2026-07-25:** share-link routes (`slRL`), `/api/v2/blocks/check` (per-IP) and `/api/v2/blocks/upload` (per-user concurrency) **do** have limiters — the residual gap is the seafhttp group, which is the same defect as **X10** in the upload-fence registry. | Sharing / Security | HIGH | (not re-tested; narrowed by code validation) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | `seafhttp.go` `RegisterSeafHTTPRoutes`; sync block route group |
| B5 | **Desktop-client SSO pending-token store is in-memory**, not distributed — poll and callback can land on different instances → token never delivered. | Auth / Multiregion | HIGH | ✅ **CONFIRMED** — drove a full OIDC desktop-SSO flow (mock IdP) with callback on `usa`: `usa` poll → `status:success` + apiToken; `eu` poll → `status:waiting` (never received it). Multi-instance only. | `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | `server.go` `clientSSOStore`; `OIDC.md` (self-admits the gap) |

**Reclassified out of blockers:**

| # | Former blocker | New status | Issue id | Basis |
|---|----------------|-----------|----------|-------|
| B2 | Cross-library block read (BOLA) | **MEDIUM security gap** (still fix, not a hard go/no-go) | `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | ✅ Reproduced cross-user: attacker (plain user) is 403-denied on the victim's library directly, but read the victim's block **byte-for-byte** through their *own* library. Gated by knowing the block's 256-bit hash → Medium, not High. Doc's surface list is **stale**: the standalone bare-SHA v2 GET was removed and `CheckBlocks` is now upload-session-gated; the live surface is `SyncHandler.GetBlock`, which calls `checkSyncPermission` on the **URL's** repo and then resolves the block by `(org_id, representation_id, block_id)` with no library-membership check — re-confirmed in code 2026-07-25. The existing `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` Medium rating is the accurate one. |

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

| ID | Finding | Sev | Status | Issue id | Code ref |
|----|---------|-----|--------|----------|----------|
| SEC-1 | Cross-library block read (BOLA) — see **B2** | **MEDIUM** (was High) | OPEN (live-verified 2026-07-24) | `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | `sync.go` `SyncHandler.GetBlock` |
| SEC-2 | No dedicated rate limiting on the seafhttp upload/download/block surfaces — see **B4** (narrowed) | HIGH | OPEN | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | `seafhttp.go` `RegisterSeafHTTPRoutes` |
| SEC-3 | `sesamefs_auth` cookie `httpOnly=false` — the cookie's token is a live, replayable session/API bearer accepted by the auth middleware, so an XSS yields **full session-token theft** (sync-client TTL up to 180d), not merely a read surface | **HIGH** (was Medium) | BY DESIGN (reassess) | `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` | `server.go` OIDC callback cookie write; auth resolution order |
| SEC-4 | Accounts M2M provisioning authenticated by API key only — no request signing (HMAC/JWT) on `PUT /admin/organizations/:org_id` | MEDIUM | OPEN | — (no id yet; file one before go-live) | `PLANS-AND-PERMISSIONS.md` |
| SEC-5 | Multi-region sticky routing not encoded in nginx config; must live at external LB — see **B1/B5** | HIGH | OPEN | covered by `ISSUE-UPLOAD-CHUNK-MULTINODE-01` + `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | `configs/nginx-multiregion.conf` |

---

## 2. Upload readiness

| ID | Finding | Sev | Status | Issue id | Code ref |
|----|---------|-----|--------|----------|----------|
| UP-1 | Chunk-upload state node-local → multi-region blocker without sticky-by-token — **B1** | HIGH | OPEN | `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | `seafhttp.go` `var chunkManager` |
| UP-2 | 1 global Paxos LWT **per block** under multi-DC `SERIAL` (~128 cross-region rounds / 1 GB). Pre-existing, both governed upload modes pay it. Latency cost, not a blocker. **Same finding as X4 / P-4** — deferred PR-11. | HIGH (perf) | OPEN | tracked as **X4** in `UPLOAD-FENCE-FINDINGS-REGISTRY.md` | `UPLOAD-PERFORMANCE-SECURITY-2026-06.md` P-4 |
| UP-3 | TOCTOU quota check across concurrent same-org uploads to different repos | MEDIUM | OPEN | `ISSUE-QUOTA-RESERVATION-01` | UPLOAD-… S-4 |
| UP-4 | `/tmp` staging admission budget (`chunked_staging_max_bytes`) defaults to `0` = disabled. ⚠️ **Sharpened 2026-07-25:** *every* shipped config sets it to `0`, `config.prod.yaml` included — the guard is off by default everywhere, not merely unset in one file. | MEDIUM | GUARD ADDED, **CONFIG REQUIRED IN ALL PROFILES** | `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | `configs/*.yaml`; `config.go` `Validate()` |
| UP-5 | Permission not re-checked during long chunked uploads (authorized at session start only) | MEDIUM | OPEN | — (shares the mid-operation revocation class with DL-5/RB-4) | UPLOAD-DOWNLOAD-ANALYSIS |
| UP-6 | `max_upload_mb` now enforced on chunked uploads (413 fail-closed) — **config-dependent**, see NF-7 | MEDIUM | ✅ FIXED (with caveat) | `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | UPLOAD-… S-2 |
| UP-7 | Resumable `file-uploaded-bytes` is a safe stub (`0`); true resume not wired. Safe, not a blocker. | LOW | BY DESIGN | — | UPLOAD-RESUME-ANALYSIS-20260619 |

---

## 3. Download readiness

| ID | Finding | Sev | Status | Issue id | Code ref |
|----|---------|-----|--------|----------|----------|
| DL-1 | `StreamBlocks` returns void → mid-stream block/decrypt failure logs "complete" and **over-counts billed traffic**. Re-confirmed 2026-07-25: still `func StreamBlocks(...)` with no error return. PR-9 fixed the *reader leak* in this same function (F11) but deliberately did not change its signature — adjacent, not the same finding. | HIGH | OPEN | `ISSUE-STREAMBLOCKS-VOID-01` | `internal/streaming/streaming.go` `StreamBlocks` |
| DL-2 | ZIP directory download can **truncate after `200 OK`** on a late storage/decrypt error; client can't distinguish from corruption | HIGH | OPEN (preflight improved) | `ISSUE-ZIP-STREAM-LATEFAIL-01` | `seafhttp.go` `HandleZipDownload` |
| DL-3 | Encrypted `encrypted`-flag probe used to **fail open** (served plaintext as ciphertext) on Cassandra error — closed 2026-07-22 by PR-6 | CRITICAL | ✅ FIXED | `ISSUE-ENCRYPTED-FLAG-UNCHECKED-01` | `libraryIsEncrypted` (single probe, 6 call sites) |
| DL-4 | Deleted file returns **503-forever**, never 404 (cross-DC lag ≡ absence); sync clients retry unbounded. **Same finding as X8** in the upload-fence registry — accepted cost of PR-6. | MEDIUM | OPEN (multi-DC tradeoff) | `ISSUE-DOWNLOAD-NO-404-01` | `respondSeafHTTPDownloadError` |
| DL-5 | Download authorization **is** re-checked per request via a live-DB `HasLibraryAccess` call, so revocation blocks *new* requests. The real (narrower) gap is only **mid-stream**: an already-streaming large download has no in-flight re-check. | LOW | OPEN | — (same class as UP-5/RB-4) | `seafhttp.go` "the ONE download gate" |
| DL-6 | Encrypted downloads omit `Content-Length` **only on the v2 inline-view/share-raw surfaces**; the primary SeafHTTP download path sets it from plaintext `size_bytes` | LOW | OPEN (scoped) | — | `fileview.go`, `sharelink_view.go`; cf. `streamFileFromBlocks` |
| DL-7 | Block integrity not re-verified (re-hashed) on download — relies on object store | MEDIUM (defense-in-depth) | OPEN | — | UPLOAD-DOWNLOAD-ANALYSIS |

---

## 4. Sharing readiness (share links, upload links)

**Solid (verified, with corrections):** tokens are `crypto/rand` 128-bit ✅; password cookie compare is constant-time (`subtle.ConstantTimeCompare` in `verifyShareLinkPasswordCookie`) ✅; permission scopes are re-checked per request — but the enforcement is at the serve paths, **not** in the parser the original doc cited ⚠️ cite-fix. **Corrections:** expiry/download-cap enforcement is **per access but the cap increment is fire-and-forget async → the download cap and `single_use` are race-bypassable** under concurrency ⚠️ (SH-5/NF-2); and an **encrypted library is blocked only at serve/decrypt time (403), NOT at link-creation** — `CreateShareLink` has no encryption check, so a link can be minted, it just can't be decrypted without the creator's session ⚠️ (see SH-3).

| ID | Finding | Sev | Status | Issue id | Code ref |
|----|---------|-----|--------|----------|----------|
| SH-1 | Public **upload links** = unauthenticated write with no per-IP/request rate limit — see **B4**. ⚠️ Narrowed: the upload-link *routes* do carry `slRL`; the unlimited surface is the seafhttp upload endpoint they hand off to. | HIGH | OPEN | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | `server_routes.go` upload-link group; `seafhttp.go` `HandleUpload` |
| SH-2 | **No org-internal scope** for share links — every public link is token-only, accessible anonymously and cross-org; no "org members only" option | MEDIUM | OPEN / BY DESIGN | — | `BUG-SHARE-LINK-NO-INTERNAL-SCOPE-20260618.md`; `resolveShareLink` |
| SH-3 | Encrypted share link decrypts using **creator's** key, not receiver auth — mixed authorization model; breaks if creator's decrypt session is revoked | MEDIUM | OPEN | — | `sharelink_view.go` decrypt path |
| SH-4 | Link passwords hashed with **bcrypt** (`bcrypt.DefaultCost` = **10**, not the doc's "12"), not the preferred Argon2id. Re-confirmed 2026-07-25 at all three hashing sites. | LOW | OPEN (cost corrected) | — | `share_links.go` `bcrypt.GenerateFromPassword` ×3 |
| SH-5 | **Download cap / `single_use` race** — the cap check reads a counter whose increment is a fire-and-forget goroutine, so N concurrent requests all pass a cap of 1 | MEDIUM | OPEN | `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | `sharelink_view.go` `handleShareLinkDownload` |
| SH-6 | **NEW (live-confirmed):** password-protected share links leak `fileContent` **and an OnlyOffice download token** without a password on the public bootstrap endpoints — password-control bypass / content disclosure | HIGH | OPEN | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | `buildShareFileBootstrapResponse` (no gate before the content read or the OnlyOffice branch). See **NF-1**. |

---

## 5. User roles & permissions readiness

**Solid (verified, with corrections):** role hierarchy is **superadmin(5) > owner(4) > admin(3) > user(2) > readonly(1) > guest(0)** — the doc **omitted superadmin(5)**; the numeric map lives in `middleware/permissions.go` (re-confirmed 2026-07-25, and the original line cite was wrong), owner auto-allow confirmed ✅. `RequireScope()` middleware confirmed ✅. Encrypted-library "share block" is **serve-time only, not create-time** (see §4 correction) ⚠️. **RB-6 RESOLVED:** an audit of every admin route registration found **no missing guard** — all gated; cross-org isolation is enforced per-handler via `requireOrgAccess` and was live-tested (tenant admin → 403 on all other-org admin routes). ✅

| ID | Finding | Sev | Status | Issue id | Code ref |
|----|---------|-----|--------|----------|----------|
| RB-2 | OIDC role claims ↔ manual role overrides: **reconciliation/authoritative-source rule undefined** — re-sync on login can conflict with admin overrides | MEDIUM | OPEN (design debt) | — (design decision, not a defect) | `PLANS-AND-PERMISSIONS.md`; `ROLES-AND-PERMISSIONS.md` |
| RB-3 | **No `permission_audit_logs`** — share/group membership changes aren't logged (compliance/forensics gap under IOCD). See NF-6 for what the existing `audit_log` actually covers. | MEDIUM | OPEN (stub) | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | `ADMIN-FEATURES.md` |
| RB-4 | Permission checks not re-evaluated mid-operation (revoke during ZIP/bulk op continues to completion) | MEDIUM | OPEN | — (same class as UP-5/DL-5) | `libraries.go`, `seafhttp.go` |
| RB-6 | `RequireScope()` per-route coverage — **RESOLVED: no gap.** All admin route groups gated; org-scoped handlers all call `requireOrgAccess` (only 501 stubs don't). Live-tested cross-org isolation holds. **Latent fragility:** the `:org_id` match is per-handler, not middleware — a future handler that forgets it would be a silent cross-tenant BOLA (see NF-5). | LOW (was VERIFY) | ✅ VERIFIED (fragility tracked) | `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | `admin_routes.go`, `org_admin_routes.go`, `org_admin.go` `requireOrgAccess` |

---

## 6. Multi-region / operational notes

- **GC disabled fleet-wide** (X1 physical-delete ABA, X2 cross-DC reference visibility). Safe (no data loss) but **no space reclamation** — plan capacity accordingly. `config.prod.yaml:292,306`.
- **Multi-DC `SERIAL` Paxos** is the production posture (`dc-na:1,dc-eu:1,dc-asia:1`); do **not** switch block-metadata LWTs to `LOCAL_SERIAL` (diverges placement). `UPLOAD-… P-4`.
- **No pre-serve replication validation gate** — verify `nodetool status` / keyspace RF across all DCs before cutover. `DEPLOY.md`.
- **Sticky-by-upload-token routing** must be added at the external LB in front of nginx (B1/B5); nginx-multiregion.conf does not provide it.

---

## Pre-prod checklist

Each item names the issue id that tracks it. Status lives in
[KNOWN_ISSUES.md](./KNOWN_ISSUES.md), not here.

0. **Share-link password bypass** (`ISSUE-SHARELINK-PASSWORD-BYPASS-01`, HIGH — new): gate the content read **inside** `buildShareFileBootstrapResponse`, before both the OnlyOffice branch and the text branch. Cover both endpoints and both branches in the regression — a `.md`-only test misses the download-token leak. Single-node exploitable; fix before go-live.
1. **Routing** (`ISSUE-UPLOAD-CHUNK-MULTINODE-01`, `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01`): external LB sticky sessions keyed on upload token; migrate the desktop-SSO pending-token store to Cassandra before any multi-instance rollout. *(Both live-confirmed 2026-07-24.)*
2. **AuthZ** (`ISSUE-BLOCK-CROSS-LIBRARY-READ-01`, Medium): add library-scoped read authorization to `SyncHandler.GetBlock` — verify block↔repo membership, not just URL-repo access.
3. **Abuse control** (`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` = B4 = X10): per-user concurrency on the seafhttp block group and a per-IP limit on anonymous upload-link writes. Note the already-protected surfaces listed in that issue — the gap is narrower than the original B4 wording.
4. **Config** (`ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01`): set `chunked_staging_max_bytes` to a real per-node value **in every profile** (all of them currently ship `0`); add a `Validate()` invariant rejecting `max_upload_mb=0` together with staging=0; confirm `CORS_ALLOWED_ORIGINS`, `SHARE_LINK_HMAC_KEY`, external TLS + HSTS.
5. **Compliance** (`ISSUE-AUDIT-TRAIL-INCOMPLETE-01`, RB-3): stand up permission/login audit logging and close the one-sided trail (grants and membership adds are unlogged) before go-live under IOCD.
6. **Harden** (`ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01`, `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01`, `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01`): reassess the `httpOnly=false` session cookie (token-theft surface); unify the two `sesamefs_auth` cookie writers, which currently disagree on both `Secure` and `httpOnly`; hoist the org-match into `/org/:org_id/admin` middleware.
7. **Accept + document:** GC stays off (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`, `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`); 503-not-404 on deletes (`ISSUE-DOWNLOAD-NO-404-01`); resume is a safe stub; download-cap/`single_use` are best-effort (`ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01`).

---

## Summary table

| Area | Blockers | High | Medium | Verified-good |
|------|----------|------|--------|---------------|
| Security posture | B4, B5 | — | SEC-1(B2, was High), SEC-3/4 | OIDC, tokens, secrets, CORS |
| Upload | B1 | UP-2 | UP-3/4/5 | UP-6 (max_upload_mb, config-dependent) |
| Download | — | DL-1, DL-2 | DL-4/5/6/7 | DL-3 (fail-open closed) |
| Sharing | **NF-1** | SH-1 | SH-2/3, SH-5 | scopes, CT-compare, crypto tokens |
| Roles | — | — | RB-2/3/4/6 | role hierarchy, RequireScope |

> Live-verified 2026-07-24: B1 ✅, B2 ✅ (Medium), B5 ✅. See "Empirical results" below.
> Code-validated 2026-07-25: all findings hold; NF-1 widened, B4 narrowed. See "Code validation".
> **"Caps" was removed from Sharing's verified-good column** — SH-5/NF-2 shows the download cap and `single_use` are race-bypassable, so listing them as enforced was the contradiction this pass exists to remove.

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

| ID | Severity | Finding | Issue id | Evidence / status |
|----|----------|---------|----------|-------------------|
| **NF-1** | **HIGH** | **Share-link password bypass.** Password-protected share links return `fileContent` (inline text/markdown, ≤1 MB) to an anonymous, password-less caller via `GET /api/v2.1/share-links/:token/bootstrap` and `…/files/bootstrap`. The raw path `/d/:token?dl=1` is correctly gated (403). **Widened 2026-07-25:** the same missing gate also leaks an **OnlyOffice download token** — when OnlyOffice is enabled, `buildShareFileBootstrapResponse` takes the OnlyOffice branch *first* and `buildOnlyOfficeShareBootstrap` mints a real `CreateLinkDownloadToken` into the anonymous response. No 1 MB ceiling, not limited to text. | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | **LIVE-CONFIRMED** (content half): created a password link, anon bootstrap returned the secret bytes; raw path = 403. OnlyOffice half confirmed by code reading, not yet driven live. The response also sets `needPassword: true` *alongside* the content — the prompt is decorative. |
| **NF-2** | **MEDIUM** | **Download-cap / `single_use` race.** `max_downloads` and single-use are checked against a count whose increment is fire-and-forget (`go func()`), so N concurrent requests all pass a cap of 1 and single-use links can be consumed multiple times. | `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | `handleShareLinkDownload`. Static; contradicts the "caps enforced per access = verified-good" framing. Re-confirmed 2026-07-25. |
| **NF-3** | **MEDIUM** | **SEC-3 is worse than stated** — the `httpOnly=false` cookie is a live session/API bearer → XSS = token theft, not a read surface. | `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` | Confirmed: the auth middleware accepts the cookie as a credential, between dev tokens and the `Authorization` header. |
| **NF-4** | **LOW–MED** | **`handleAutoLogin` hardcodes cookie `Secure=false`** (unlike the callback path which derives it from `Request.TLS`). If reachable in prod behind external TLS, the session cookie ships without Secure. | `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` | Confirmed. Routed at `GET /client-login[/]`. **Also note the reverse inconsistency:** auto-login sets `httpOnly=true` while the callback sets `httpOnly=false` — the two paths disagree in both dimensions. |
| **NF-5** | **LOW** (latent) | **Org-scoped authz is per-handler, not middleware.** The `/org/:org_id/admin` group checks only JWT-org *role*, not that `:org_id` == caller's org; the cross-tenant gate is `requireOrgAccess` copy-pasted into ~50 handlers. All current handlers call it (verified + live-tested), but a new handler that forgets it = silent cross-tenant BOLA. | `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | Hoist an org-match check into the sub-group. |
| **NF-6** | **LOW** (compliance) | **Audit trail records deletions but not grants.** An `audit_log` table exists; share creation, group-member *adds*, permission grants, role changes and logins write nothing. A one-sided trail is arguably worse than none under IOCD. Compounds RB-3. | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | **Made precise 2026-07-25:** there are exactly six writers — `organization.update`, four group/department **deletes**, and the GC cascade. So it is "deletes plus one org-update", not strictly delete-only. Table is in `internal/db/migrations/001_initial_schema.cql` (the original cite omitted `internal/db/`). |
| **NF-7** | **LOW** (config) | **Both upload size guards can be disabled together.** `max_upload_mb=0` (documented "unlimited") + `chunked_staging_max_bytes=0` ⇒ first chunk does `Truncate(attacker_total)` unbounded. UP-6's "FIXED" is config-dependent. | `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | **Sharpened 2026-07-25:** *every* shipped config sets `chunked_staging_max_bytes: 0`, `config.prod.yaml` included — the staging guard is off by default everywhere. The only thing bounding a chunked upload in prod is `max_upload_mb: 102400` (100 GiB), so one upload may still reserve 100 GiB of node-local `/tmp`. `Validate()` rejects a negative value but has no both-zero invariant. |

### Corrections to existing claims

| Claim | Original | Correction (verified) |
|-------|----------|-----------------------|
| SEC-1 (B2) | HIGH blocker | **MEDIUM**; live surface is `SyncHandler.GetBlock`, other cited surfaces stale. |
| SH-4 | bcrypt "cost 12" | bcrypt **cost 10** (`bcrypt.DefaultCost`), at all three hashing sites. |
| §4 "encrypted library blocked server-side" | implies create-time block | **Serve-time only**; `CreateShareLink` has no encryption check. |
| §4 "scopes re-checked at `:574–601`" | cite | That range is a parser; enforcement is at the serve paths. |
| DL-5 | "not re-checked per request" | **Overstated** — per-request re-auth exists; real gap is mid-stream only. |
| DL-6 | "encrypted omit Content-Length" | **Partly stale** — main SeafHTTP path sets it; only v2 view/share surfaces omit. |
| §5 role hierarchy | "owner(4)…" | Omits **superadmin(5)**; the cited line range was also wrong. |
| §6 multi-DC layout | `dc-na:1,dc-eu:1,dc-asia:1` | Stale — shipped `config.prod.yaml` is single-DC `datacenter1: 1` (SERIAL posture correct). |
| Secrets (§1) | HMAC key "required if empty" | Stronger — also rejects the shipped **default** value in prod. |
| CORS / SEC-4 refs | `server.go:264–293` / "§4.3" | Ref drift — CORS fail-fast is in `config.go` `Validate()`; M2M route is `PUT …/admin/organizations/:org_id`. |

### Confirmed as-written (no change)
UP-2, UP-3, UP-5, UP-7; DL-1, DL-2, **DL-3 (critical fail-open genuinely closed across all surfaces)**, DL-4, DL-7; SH-2, SH-3; RB-2, RB-3, RB-4; §1 solid claims 1–5 (OIDC defaults on, sessions hashed+TTL, secrets, CORS default-deny, HSTS/nosniff); GC-disabled, SERIAL posture, no-replication-gate.

---

## Code validation (2026-07-25)

Every finding above was re-read against `main` at `0dac50993`. **No finding was
withdrawn.** Four changed shape:

| # | Change | Detail |
|---|--------|--------|
| NF-1 | **Widened** | A second disclosure on the same missing gate: `buildOnlyOfficeShareBootstrap` mints a `CreateLinkDownloadToken` for an anonymous caller when OnlyOffice is enabled and the file is an office document. It is evaluated *before* the text branch, has no 1 MB ceiling, and is not limited to text. A fix or a regression test that only covers `.md`/`.txt` will leave it open. |
| B4 | **Narrowed** | Several surfaces the original wording covered *do* have limiters: share-link and upload-link routes (`slRL`), `/api/v2/blocks/check` (per-IP), `/api/v2/blocks/upload` (per-user concurrency), `/seafhttp/zip/:token` (optional). The residual gap is the seafhttp upload/download/block group — which is **the same defect as X10** in the upload-fence registry, now merged into one issue id. |
| NF-7 | **Sharpened** | The staging guard is `0` in *every* shipped config including prod, so it is the shipped default rather than a missing value in one profile. |
| NF-6 | **Made precise** | Six `audit_log` writers, not zero-plus-deletes: one `organization.update`, four deletes, one GC cascade. "Delete-only" → "deletes plus one org-update". |

**Citation policy changed.** The original document cited findings by
`file.go:NNNN`. Between the audit and this validation, PR-10 (#146) added ~30
lines to `sync.go` and invalidated several of them — `GetBlock`, cited at
`:1139`, is now at `:1156`. Line numbers are now replaced by **symbol names**
throughout, which survive edits and are greppable. New findings should follow
that convention.

**Not re-verified live.** This pass was a code read against `main`, not a rerun
of the multiregion stack. The 2026-07-24 empirical results for B1/B2/B5 stand as
recorded; the OnlyOffice half of NF-1 is confirmed by reading only and should be
driven live when the fix is written, since a live repro is also the regression
test.
