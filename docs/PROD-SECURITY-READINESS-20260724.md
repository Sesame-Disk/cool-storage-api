# Production & Security Readiness — Upload / Download / Sharing / Roles

**Date:** 2026-07-24 · **Code-validated:** 2026-07-25
**Scope:** Readiness to run `cool-storage-api` (SesameFS) in production under our **IOCD** controls, focused on the **security posture** and the **prod-readiness of the upload, download, and sharing paths and the user-role/permission model**.
**Method:** Findings verified against source (`internal/api/**`, `configs/config.prod.yaml`, `.env.prod.example`, nginx-multiregion) and the issue backlog (`docs/KNOWN_ISSUES.md`, `SECURITY-ASSESSMENT-2026-04*`, `UPLOAD-*`). Each item carries a severity, status, and a concrete reference.

> **Empirical verification (2026-07-24):** the HIGH blockers were re-tested against a live two-instance multiregion stack (`docker-compose.mr.yaml`: `sesamefs-usa` + `sesamefs-eu` sharing one Cassandra + one MinIO behind the nginx LB). Results: **B1 CONFIRMED**, **B2 CONFIRMED** (but Medium, not High — see below), **B5 CONFIRMED**. B1/B5 are multi-instance-only (no impact single-node). Details in the "Empirical results" section at the end.

> **Code validation (2026-07-25):** every finding below was re-checked against `main` at `0dac50993`. **All findings hold.** One new disclosure was found inside NF-1's blast radius (an OnlyOffice download token minted with no password check — see NF-1), one claim was narrowed (B4 is partly stale: several surfaces *do* have limiters), one was sharpened (NF-7: the staging guard is disabled in *every* shipped config, prod included), and one was made precise (NF-6 is delete-plus-org-update, not delete-only). Prefer **symbol names** over `file.go:NNNN` — PR-10 shifted `sync.go` by ~30 lines and invalidated several cites. Validation deltas are tabulated in the "Code validation" section at the end.

> **Post-snapshot status changes are in one place: the "Status changes after the audit snapshot" section at the end.** Everything else below — the verdict callout, the blockers table, the per-area tables, the checklist, the summary and the empirical results — is the **2026-07-24/25 audit snapshot and is deliberately not retro-edited**, even where a finding has since been fixed. An earlier revision of this update did edit those rows in place; that turned this document back into a second live tracker competing with `KNOWN_ISSUES.md`, which is exactly what the consolidation was for. Reverted.

> **Status of record is [KNOWN_ISSUES.md](./KNOWN_ISSUES.md).** Every open defect row below links to an `ISSUE-*` id. Columns labelled **Status as of 2026-07-25** are a snapshot for this audit; do not treat them as a second live tracker. For the scoped open-work screen, see [OPEN-WORK-INDEX.md](./OPEN-WORK-INDEX.md).

> Companion depth docs: [UPLOAD-PERFORMANCE-SECURITY-2026-06.md](./UPLOAD-PERFORMANCE-SECURITY-2026-06.md), [UPLOAD-RESUME-ANALYSIS-20260619.md](./UPLOAD-RESUME-ANALYSIS-20260619.md), `KNOWN_ISSUES.md`. This doc is the short readiness view, not a replacement for them.

---

## Verdict

> ⚠️ **New HIGH finding (2026-07-24 verification, live-confirmed; widened 2026-07-25):** password-protected **share links leak file content without the password** via the public `/api/v2.1/share-links/:token/bootstrap[/files]` endpoints. The raw download path (`/d/:token?dl=1`) is correctly gated (403), but the bootstrap JSON embeds `fileContent` with no password check. **Code validation found a second leak on the same missing gate:** when OnlyOffice is enabled and the shared file is an office document, the same builder mints a real **download token** and returns it to the anonymous caller — no 1 MB ceiling, not limited to text. This is a straightforward auth-control bypass (single-node reproducible; only the link token is needed, no hash) — see NF-1 and **ISSUE-SHARELINK-PASSWORD-BYPASS-01**. Treat as a go/no-go security blocker.

**No-go as-is.** After closing the share-link password bypass (NF-1) **and** the seafhttp abuse-control umbrella (B4 / `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` with its closable subcontracts), a **conditional-go** for a single-node or sticky-routed deployment can be reconsidered. Before any multi-instance rollout, B1 and B5 must also close (both empirically reproduced). Authentication, secrets, OIDC, CORS, and share-token cryptography are in good shape and largely enforced-by-default. A **cross-library block-read authorization gap (BOLA)** is real and reproduced but **Medium** severity (authenticated + requires knowing the target block's 256-bit hash), not a hard go/no-go blocker. Garbage collection must remain **disabled fleet-wide** (safe by design, but means no space reclamation yet).

### Must-fix before prod (blockers)

| # | Blocker | Area | Severity | Live-verified 2026-07-24 | Issue id | Code ref (symbol) |
|---|---------|------|----------|--------------------------|----------|-------------------|
| NF-1 | **Share-link password bypass** — password-protected links serve `fileContent` and an OnlyOffice download token to anonymous callers. Single-node reachable. | Sharing / Security | HIGH | ✅ **CONFIRMED** (content); OnlyOffice half found in code validation | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | `sharelink_view.go` `buildShareFileBootstrapResponse`, `buildOnlyOfficeShareBootstrap` |
| B1 | **Chunk-upload state is node-local** — multi-node/region routing that isn't sticky-by-upload-token breaks finalization. `nginx-multiregion.conf` does hostname/round-robin routing only; no sticky mechanism. Multi-instance only; no single-node impact. | Upload / Multiregion | HIGH | ✅ **CONFIRMED** — split upload (chunk0→usa, final→eu, same shared token) silently dropped the file; `eu` logged `FINAL_CHUNK_BUT_INCOMPLETE first_gap=0-4194303` and returned HTTP 200 `{"success":true}` with no dirent. | `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | `seafhttp.go` `var chunkManager` (process-global); `configs/nginx-multiregion.conf` |
| B4 | **No dedicated rate limit** on the seafhttp upload / download / block routes. ⚠️ **Narrowed 2026-07-25:** share-link routes (`slRL`), `/api/v2/blocks/check` (per-IP) and `/api/v2/blocks/upload` (per-user concurrency) **do** have limiters. Residual gap is the seafhttp group. **X10 is the block-PUT / aggregate-bound slice** of this umbrella, not the whole B4 surface — see subcontracts on the issue. | Sharing / Security | HIGH | (not re-tested; narrowed by code validation) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | `seafhttp.go` `RegisterSeafHTTPRoutes`; sync block route group |
| B5 | **Desktop-client SSO pending-token store is in-memory**, not distributed — poll and callback can land on different instances → token never delivered. | Auth / Multiregion | HIGH | ✅ **CONFIRMED** — drove a full OIDC desktop-SSO flow (mock IdP) with callback on `usa`: `usa` poll → `status:success` + apiToken; `eu` poll → `status:waiting` (never received it). Multi-instance only. | `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | `server.go` `clientSSOStore`; `OIDC.md` (self-admits the gap) |

**Reclassified out of blockers:**

| # | Former blocker | New status | Issue id | Basis |
|---|----------------|-----------|----------|-------|
| B2 | Cross-library block read (BOLA) | **MEDIUM security gap** (still fix, not a hard go/no-go) | `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | ✅ Reproduced cross-user: attacker (plain user) is 403-denied on the victim's library directly, but read the victim's block **byte-for-byte** through their *own* library. Gated by knowing the block's 256-bit hash → Medium, not High. Doc's surface list is **stale**: the standalone bare-SHA v2 GET was removed and `CheckBlocks` is now upload-session-gated; the live surface is `SyncHandler.GetBlock`, which calls `checkSyncPermission` on the **URL's** repo and then resolves the block by `(org_id, representation_id, block_id)` with no library-membership check — re-confirmed in code 2026-07-25. The existing `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` Medium rating is the accurate one. |

Accepted-but-track for this release: **GC stays disabled** on every replica/DC (X1 physical-delete ABA, X2 cross-DC reference visibility) — `gc.enabled` / related flags in `configs/config.prod.yaml`.

---

## 1. Security posture (auth, secrets, transport)

**What is solid (verified, enforced by default):**

- **OIDC**: audience validation, PKCE, nonce, and signature verification are on by default (`configs/config.prod.yaml` OIDC block; `internal/auth/oidc.go`). ✅
- **Tokens/sessions**: upload tokens are Cassandra-backed and multi-node safe (`NewCassandraTokenAdapter`); session tokens are DB-stored, hashed, and TTL'd. ✅
- **Secrets**: externalized to env — no hardcoded credentials; `SHARE_LINK_HMAC_KEY` is **required at boot** in prod. ✅
- **CORS**: wildcard `*` rejected in prod; empty allowlist denies by default via `config.Validate()`; operator must set `CORS_ALLOWED_ORIGINS`. ✅
- **Headers/TLS**: HSTS + nosniff via `SecurityHeaders()` in prod mode; cookie `Secure` flag driven by `Request.TLS`. TLS termination is assumed **external** (central nginx) — no in-process TLS. ✅ / ⚠ operator-dependent.
- **Accounts M2M channel (code-verified 2026-07-25):** platform superadmin + `apikeys.ScopeAdmin` on `/admin`; Accounts can create/update/delete orgs and users through the existing admin API today. What remains is hardening (idempotency / `source=accounts` audit tag / optional request signing / runbook), not inventing the channel.

**Gaps:**

| ID | Finding | Sev | Status as of 2026-07-25 | Issue id | Code ref |
|----|---------|-----|-------------------------|----------|----------|
| SEC-1 | Cross-library block read (BOLA) — see **B2** | **MEDIUM** (was High) | OPEN (live-verified 2026-07-24) | `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` | `sync.go` `SyncHandler.GetBlock` |
| SEC-2 | No dedicated rate limiting on the seafhttp upload/download/block surfaces — see **B4** (narrowed) | HIGH | OPEN | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | `seafhttp.go` `RegisterSeafHTTPRoutes` |
| SEC-3 | `sesamefs_auth` cookie `httpOnly=false` — the cookie's token is a live, replayable session/API bearer accepted by the auth middleware, so an XSS yields **full session-token theft** (sync-client TTL up to 180d), not merely a read surface | **HIGH** (was Medium) | BY DESIGN (reassess) | `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` | `server.go` OIDC callback cookie write; auth resolution order |
| SEC-4 | No request signing (HMAC/JWT) beyond admin API key on Accounts M2M routes | LOW (hardening) | ACCEPTED FOR V1 — channel is API key by design; optional post-v1 hardening | `ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01` | `admin_routes.go` `RequireScope(ScopeAdmin)`; companion `ISSUE-ACCOUNTS-M2M-PATH-01` |
| SEC-5 | Multi-region sticky routing not encoded in nginx config; must live at external LB — see **B1/B5** | HIGH | OPEN (multi-instance) | covered by `ISSUE-UPLOAD-CHUNK-MULTINODE-01` + `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01` | `configs/nginx-multiregion.conf` |

---

## 2. Upload readiness

| ID | Finding | Sev | Status as of 2026-07-25 | Issue id | Code ref |
|----|---------|-----|-------------------------|----------|----------|
| UP-1 | Chunk-upload state node-local → multi-region blocker without sticky-by-token — **B1** | HIGH | OPEN | `ISSUE-UPLOAD-CHUNK-MULTINODE-01` | `seafhttp.go` `var chunkManager` |
| UP-2 | 1 global Paxos LWT **per block** under multi-DC `SERIAL` (~128 cross-region rounds / 1 GB). Pre-existing, both governed upload modes pay it. Latency cost, not a blocker. **Same finding as X4 / P-4** — deferred PR-11. | HIGH (perf) | OPEN | `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01` (= X4) | `UPLOAD-PERFORMANCE-SECURITY-2026-06.md` P-4 |
| UP-3 | TOCTOU quota check across concurrent same-org uploads to different repos | MEDIUM | OPEN | `ISSUE-QUOTA-RESERVATION-01` | UPLOAD-… S-4 |
| UP-4 | `/tmp` staging admission budget (`chunked_staging_max_bytes`) defaults to `0` = disabled. ⚠️ **Sharpened 2026-07-25:** *every* shipped config sets it to `0`, `config.prod.yaml` included, **and this field has no `.env` override** — unlike most prod settings it can only be changed by editing the YAML. | MEDIUM | GUARD ADDED, **YAML EDIT REQUIRED IN ALL PROFILES** | `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | `configs/*.yaml`; `config.go` `applyEnvOverrides()` (absent) + `Validate()` |
| UP-5 | Permission not re-checked during long chunked uploads (authorized at session start only) | MEDIUM | OPEN | `ISSUE-MID-OPERATION-REVOCATION-01` | UPLOAD-DOWNLOAD-ANALYSIS |
| UP-6 | `max_upload_mb` now enforced on chunked uploads (413 fail-closed) — **config-dependent**, see NF-7 | MEDIUM | ✅ FIXED (with caveat) | `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | UPLOAD-… S-2 |
| UP-7 | Resumable `file-uploaded-bytes` is a safe stub (`0`); true resume not wired. Safe, not a blocker. | LOW | BY DESIGN | — (accepted stub) | UPLOAD-RESUME-ANALYSIS-20260619 |

---

## 3. Download readiness

| ID | Finding | Sev | Status as of 2026-07-25 | Issue id | Code ref |
|----|---------|-----|-------------------------|----------|----------|
| DL-1 | `StreamBlocks` now returns errors and `streamFileFromBlocks` records delivered bytes rather than nominal size; post-header failures remain client-visible truncation by HTTP design. | HIGH | FIXED 2026-08-03 | `ISSUE-STREAMBLOCKS-VOID-01` | `internal/streaming/streaming.go` `StreamBlocks` |
| DL-2 | ZIP directory download can **truncate after `200 OK`** on a late storage/decrypt error; client can't distinguish from corruption | HIGH | OPEN (preflight improved) | `ISSUE-ZIP-STREAM-LATEFAIL-01` | `seafhttp.go` `HandleZipDownload` |
| DL-3 | Encrypted `encrypted`-flag probe used to **fail open** (served plaintext as ciphertext) on Cassandra error — closed 2026-07-22 by PR-6 | CRITICAL | ✅ FIXED | `ISSUE-ENCRYPTED-FLAG-UNCHECKED-01` | `libraryIsEncrypted` (single probe, 6 call sites) |
| DL-4 | Deleted file returns **503-forever**, never 404 (cross-DC lag ≡ absence); sync clients retry unbounded. **Same finding as X8** in the upload-fence registry — accepted cost of PR-6. | MEDIUM | OPEN (multi-DC tradeoff) | `ISSUE-DOWNLOAD-NO-404-01` | `respondSeafHTTPDownloadError` |
| DL-5 | Download authorization **is** re-checked per request via a live-DB `HasLibraryAccess` call, so revocation blocks *new* requests. The real (narrower) gap is only **mid-stream**: an already-streaming large download has no in-flight re-check. | LOW | OPEN | `ISSUE-MID-OPERATION-REVOCATION-01` | `seafhttp.go` "the ONE download gate" |
| DL-6 | Encrypted downloads omit `Content-Length` **only on the v2 inline-view/share-raw surfaces**; the primary SeafHTTP download path sets it from plaintext `size_bytes` | LOW | OPEN (scoped) | `ISSUE-ENCRYPTED-VIEW-CONTENT-LENGTH-01` | `fileview.go`, `sharelink_view.go`; cf. `streamFileFromBlocks` |
| DL-7 | Block integrity not re-verified (re-hashed) on download — relies on object store | MEDIUM (defense-in-depth) | OPEN | `ISSUE-DOWNLOAD-NO-REHASH-01` | UPLOAD-DOWNLOAD-ANALYSIS |

---

## 4. Sharing readiness (share links, upload links)

**Solid (verified, with corrections):** tokens are `crypto/rand` 128-bit ✅; password cookie compare is constant-time (`subtle.ConstantTimeCompare` in `verifyShareLinkPasswordCookie`) ✅; permission scopes are re-checked per request — but the enforcement is at the serve paths, **not** in the parser the original doc cited ⚠️ cite-fix. **Corrections:** expiry/download-cap enforcement is **per access but the cap increment is fire-and-forget async → the download cap and `single_use` are race-bypassable** under concurrency ⚠️ (SH-5/NF-2); and an **encrypted library is blocked only at serve/decrypt time (403), NOT at link-creation** — `CreateShareLink` has no encryption check, so a link can be minted, it just can't be decrypted without the creator's session ⚠️ (see SH-3).

| ID | Finding | Sev | Status as of 2026-07-25 | Issue id | Code ref |
|----|---------|-----|-------------------------|----------|----------|
| SH-1 | Public **upload links** = unauthenticated write with no per-IP/request rate limit — see **B4**. ⚠️ Narrowed: the upload-link *routes* do carry `slRL`; the unlimited surface is the seafhttp upload endpoint they hand off to. | HIGH | OPEN | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` (subcontract A) | `server_routes.go` upload-link group; `seafhttp.go` `HandleUpload` |
| SH-2 | **No org-internal scope** for share links — every public link is token-only, accessible anonymously and cross-org; no "org members only" option | MEDIUM | OPEN / BY DESIGN | `ISSUE-SHARELINK-NO-ORG-SCOPE-01` | `BUG-SHARE-LINK-NO-INTERNAL-SCOPE-20260618.md`; `resolveShareLink` |
| SH-3 | Encrypted share link decrypts using **creator's** key, not receiver auth — mixed authorization model; breaks if creator's decrypt session is revoked | MEDIUM | OPEN | `ISSUE-SHARELINK-CREATOR-KEY-01` | `sharelink_view.go` decrypt path |
| SH-4 | Link passwords hashed with **bcrypt** (`bcrypt.DefaultCost` = **10**, not the doc's "12"), not the preferred Argon2id. Re-confirmed 2026-07-25 at all three hashing sites. | LOW | OPEN (cost corrected) | `ISSUE-SHARELINK-BCRYPT-COST-01` | `share_links.go` `bcrypt.GenerateFromPassword` ×3 |
| SH-5 | **Download cap / `single_use` race** — the cap check reads a counter whose increment is a fire-and-forget goroutine, so N concurrent requests all pass a cap of 1 | MEDIUM | OPEN | `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | `sharelink_view.go` `handleShareLinkDownload` |
| SH-6 | **NEW (live-confirmed):** password-protected share links leak `fileContent` **and an OnlyOffice download token** without a password on the public bootstrap endpoints — password-control bypass / content disclosure | HIGH | OPEN | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | `buildShareFileBootstrapResponse` (no gate before the content read or the OnlyOffice branch). See **NF-1**. |

---

## 5. User roles & permissions readiness

**Solid (verified, with corrections):** role hierarchy is **superadmin(5) > owner(4) > admin(3) > user(2) > readonly(1) > guest(0)** — the doc **omitted superadmin(5)**; the numeric map lives in `middleware/permissions.go` (re-confirmed 2026-07-25, and the original line cite was wrong), owner auto-allow confirmed ✅. `RequireScope()` middleware confirmed ✅. Encrypted-library "share block" is **serve-time only, not create-time** (see §4 correction) ⚠️. **RB-6 RESOLVED:** an audit of every admin route registration found **no missing guard** — all gated; cross-org isolation is enforced per-handler via `requireOrgAccess` and was live-tested (tenant admin → 403 on all other-org admin routes). ✅

| ID | Finding | Sev | Status as of 2026-07-25 | Issue id | Code ref |
|----|---------|-----|-------------------------|----------|----------|
| RB-2 | OIDC role claims ↔ manual role overrides: **reconciliation/authoritative-source rule undefined** — re-sync on login can conflict with admin overrides | MEDIUM | OPEN (design debt) | `ISSUE-OIDC-ROLE-RECONCILIATION-01` | `PLANS-AND-PERMISSIONS.md`; `ROLES-AND-PERMISSIONS.md` |
| RB-3 | **No `permission_audit_logs`** — share/group membership changes aren't logged (compliance/forensics gap under IOCD). See NF-6 for what the existing `audit_log` actually covers. | MEDIUM | OPEN (stub) | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | `ADMIN-FEATURES.md` |
| RB-4 | Permission checks not re-evaluated mid-operation (revoke during ZIP/bulk op continues to completion) | MEDIUM | OPEN | `ISSUE-MID-OPERATION-REVOCATION-01` | `libraries.go`, `seafhttp.go` |
| RB-6 | `RequireScope()` per-route coverage — **RESOLVED: no gap.** All admin route groups gated; org-scoped handlers all call `requireOrgAccess` (only 501 stubs don't). Live-tested cross-org isolation holds. **Latent fragility:** the `:org_id` match is per-handler, not middleware — a future handler that forgets it would be a silent cross-tenant BOLA (see NF-5). | LOW (was VERIFY) | ✅ VERIFIED (fragility tracked) | `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | `admin_routes.go`, `org_admin_routes.go`, `org_admin.go` `requireOrgAccess` |

---

## 6. Multi-region / operational notes

- **GC disabled fleet-wide** (X1 physical-delete ABA, X2 cross-DC reference visibility). Safe (no data loss) but **no space reclamation** — plan capacity accordingly. See `gc.enabled` in `configs/config.prod.yaml`.
- **Multi-DC `SERIAL` Paxos is the production posture** (`dc-na` / `dc-eu` /
  `dc-asia`, RF 1 each); do **not** switch block-metadata LWTs to `LOCAL_SERIAL`
  (diverges placement). `UPLOAD-… P-4` / `ISSUE-UPLOAD-PER-BLOCK-PAXOS-01`.

  **Three layers — do not conflate them** (this line was mis-corrected twice by
  reading only one layer):

  1. **YAML defaults** in `configs/config.prod.yaml`: `local_dc` /
     `replication_dcs: {datacenter1: 1}` — a single-DC placeholder. The file
     documents that env may replace these.
  2. **Shipped prod template** `.env.prod.example` (active lines, not commented):
     `CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1`,
     `CASSANDRA_LOCAL_DC=dc-na`, `CASSANDRA_DC=dc-na`,
     `CASSANDRA_CONSISTENCY=LOCAL_QUORUM`,
     `CASSANDRA_SERIAL_CONSISTENCY=SERIAL`.
     ⚠️ **These two DC variables are not interchangeable**, they only happen to
     hold the same value in the template. `applyEnvOverrides()` reads **only
     `CASSANDRA_LOCAL_DC`**; the binary never looks at `CASSANDRA_DC`, which is a
     compose-side variable naming the Cassandra container's own DC (and, in the
     multiregion compose files, the value `CASSANDRA_LOCAL_DC` is *derived* from).
     Changing only `CASSANDRA_DC` on a node moves the database's DC while leaving
     the app's `local_dc` pointing at the old one.
  3. **Real SesameFS production** is multi-DC **NA / EU / Asia** (operator-
     confirmed deployment posture). Operators copy `.env.prod.example` → `.env`
     and adjust the per-node DC; the topology list stays the triple. Per the
     warning above, set **both** DC variables together on each node.

  **How the binary applies it:** `Config.Load()` parses YAML, then
  `applyEnvOverrides()` replaces `Database.ReplicationDCs` / consistency fields
  from the environment, then `Validate()` checks the result. `Validate()` does
  **not** apply overrides. With a prod `.env` derived from the template, the
  YAML `datacenter1: 1` value does not survive boot.
  X2, X6 and the no-404 decision are reasoned against this three-DC RF-1
  topology (also recorded in `ISSUE-DOWNLOAD-NO-404-01`).
  **Do not "fix" topology by reading only the YAML.**
- **No pre-serve replication validation gate** — verify `nodetool status` / keyspace RF across all DCs before cutover. `DEPLOY.md`.
- **Sticky-by-upload-token routing** must be added at the external LB in front of nginx (B1/B5); nginx-multiregion.conf does not provide it.

---

## Pre-prod checklist

Each item names the issue id that tracks it. Status lives in
[KNOWN_ISSUES.md](./KNOWN_ISSUES.md), not here.

0. **Share-link password bypass** (`ISSUE-SHARELINK-PASSWORD-BYPASS-01`, HIGH — new): gate the content read **inside** `buildShareFileBootstrapResponse`, before both the OnlyOffice branch and the text branch. Cover both endpoints and both branches in the regression — a `.md`-only test misses the download-token leak. Single-node exploitable; **blocks go-live**.
1. **Abuse control** (`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` = B4 umbrella; X10 is subcontract B): close all four subcontracts (anonymous seafhttp upload; authenticated block PUT concurrency; check-blocks rate/work amplification; download/GET abuse control) — **blocks go-live**. Closing only block-PUT concurrency does **not** close the umbrella.
2. **Routing** (`ISSUE-UPLOAD-CHUNK-MULTINODE-01`, `ISSUE-SSO-PENDING-TOKEN-NODE-LOCAL-01`): external LB sticky sessions keyed on upload token; migrate the desktop-SSO pending-token store to Cassandra before any multi-instance rollout. *(Both live-confirmed 2026-07-24.)*
3. **AuthZ** (`ISSUE-BLOCK-CROSS-LIBRARY-READ-01`, Medium): add library-scoped read authorization to `SyncHandler.GetBlock` — verify block↔repo membership, not just URL-repo access.
4. **Config** (`ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01`): set `chunked_staging_max_bytes` to a real per-node value **in every YAML profile** (all currently ship `0`); there is **no** env override for this field or for `server.max_upload_mb` today — add `SEAFHTTP_CHUNKED_STAGING_MAX_BYTES` / `SERVER_MAX_UPLOAD_MB` (or equivalent) in `applyEnvOverrides()`, plus a `Validate()` invariant rejecting `max_upload_mb=0` together with staging=0; confirm `CORS_ALLOWED_ORIGINS`, `SHARE_LINK_HMAC_KEY`, external TLS + HSTS.
5. **Compliance** (`ISSUE-AUDIT-TRAIL-INCOMPLETE-01`, RB-3): stand up permission/login audit logging and close the one-sided trail (grants and membership adds are unlogged) before go-live under IOCD.
6. **Harden** (`ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01`, `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01`, `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01`): reassess the `httpOnly=false` session cookie (token-theft surface); unify the two `sesamefs_auth` cookie writers, which currently disagree on both `Secure` and `httpOnly`; hoist the org-match into `/org/:org_id/admin` middleware.
7. **Accept + document:** GC stays off (`ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01`, `ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01`); 503-not-404 on deletes (`ISSUE-DOWNLOAD-NO-404-01`); resume is a safe stub; download-cap/`single_use` are best-effort (`ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01`); Accounts M2M uses admin API key by design (`ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01` deferred).

---

## Summary table

| Area | Blockers | High | Medium | Verified-good |
|------|----------|------|--------|---------------|
| Security posture | **NF-1**, B4, B5 | SEC-3 | SEC-1(B2, was High); SEC-4 accepted-for-v1 | OIDC, tokens, secrets, CORS, Accounts admin API-key channel |
| Upload | B1 | UP-2 | UP-3/4/5 | UP-6 (max_upload_mb, config-dependent) |
| Download | — | DL-1, DL-2 | DL-4/5/6/7 | DL-3 (fail-open closed) |
| Sharing | **NF-1** | SH-1 | SH-2/3, SH-5 | scopes, CT-compare, crypto tokens |
| Roles | — | — | RB-2/3/4/6 | role hierarchy, RequireScope |

> Live-verified 2026-07-24: B1 ✅, B2 ✅ (Medium), B5 ✅. See "Empirical results" below.
> Code-validated 2026-07-25: all findings hold; NF-1 widened, B4 narrowed; verdict corrected to **no-go as-is**. See "Code validation".
> **"Caps" was removed from Sharing's verified-good column** — SH-5/NF-2 shows the download cap and `single_use` are race-bypassable, so listing them as enforced was the contradiction this pass exists to remove.

---

## Empirical results (2026-07-24)

**Test bed:** `docker-compose.mr.yaml` — two app instances (`sesamefs-usa` :8088, `sesamefs-eu` :8000) sharing **one** Cassandra + **one** MinIO, behind the nginx LB (:18080). Both instances reachable directly on the host, so requests could be deterministically routed to a specific node. `AUTH_DEV_MODE=true` (dev tokens: admin=`dev-token-admin` org `…001`, user=`dev-token-user` org `…001`). This is the exact "shared state in Cassandra/MinIO, node-local state per process" topology the blockers concern.

| # | Result | What was observed |
|---|--------|-------------------|
| **B1** | ✅ CONFIRMED (multi-node only) | Upload token is Cassandra-backed (shared), so the same token is valid on both nodes — but staged chunk bytes are node-local (`os.TempDir()` temp file tracked by process-global `var chunkManager`). **Control** (both chunks → usa): file finalized intact, `size=8388608`. **Split** (chunk0 → usa, final chunk → eu, same token): `eu` created a fresh empty tracker, logged `FINAL_CHUNK_BUT_INCOMPLETE (prefix missing; possible tracker split) first_gap=0-4194303 received=4194304/8388608`, returned HTTP 200 `{"success":true}` **with no dirent**, and the file **never appeared**. Silent data loss on non-sticky routing. |
| **B2** | ✅ CONFIRMED — Medium | Cross-user: victim (admin) stored a secret block in library V; attacker (plain user 002) owns library A. Attacker → `GET /seafhttp/repo/{V}/block/{hash}` = **403** (per-library isolation holds). Attacker → `GET /seafhttp/repo/{A}/block/{hash}` (own library, victim's hash) = **200 with the victim's exact bytes**. `SyncHandler.GetBlock` authorizes the URL's repo but fetches the block by `(org, sha256)` with no library-membership check. Severity Medium: authenticated, and requires knowing the 256-bit content hash (supplied directly in the test). |
| **B5** | ✅ CONFIRMED (multi-instance only) | Drove a full real OIDC desktop-SSO flow (mock IdP; discovery/authorize/token/JWKS + RS256 signature, nonce, issuer all validated) with the callback completing on `usa`. `usa` poll → `{"status":"success","apiToken":"…","username":"ssouser@…"}`; `eu` poll → `{"status":"waiting"}`. `clientSSOStore` is a per-process in-memory map — success written on one instance is invisible on another, so a poll routed to a different instance than the callback never gets the token. "SSO works in the desktop client" only exercises the same-instance happy path and does not refute this. |

**Net change to the readiness picture:** **no-go as-is.** Blockers include **NF-1** and **B4** (single-node reachable) plus **B1/B5** (multi-instance only). B2 is a real **Medium** BOLA to fix but not a hard go/no-go.

---

## Verification addendum (2026-07-24)

The whole document's remaining assertions (Sections 1–6) were re-checked against source, with live tests where feasible. Below: **new issues found**, then a **corrections table** for claims that were wrong/overstated/stale. Most section claims held; only the deltas are listed.

### New issues (not in the original doc)

| ID | Severity | Finding | Issue id | Evidence / status |
|----|----------|---------|----------|-------------------|
| **NF-1** | **HIGH** | **Share-link password bypass.** Password-protected share links return `fileContent` (inline text/markdown, ≤1 MB) to an anonymous, password-less caller via `GET /api/v2.1/share-links/:token/bootstrap` and `…/files/bootstrap`. The raw path `/d/:token?dl=1` is correctly gated (403). **Widened 2026-07-25:** the same missing gate also leaks an **OnlyOffice download token** — when OnlyOffice is enabled, `buildShareFileBootstrapResponse` takes the OnlyOffice branch *first* and `buildOnlyOfficeShareBootstrap` mints a real `CreateLinkDownloadToken` into the anonymous response. No 1 MB ceiling, not limited to text. | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | **LIVE-CONFIRMED** (content half): created a password link, anon bootstrap returned the secret bytes; raw path = 403. OnlyOffice half confirmed by code reading, not yet driven live. The response also sets `needPassword: true` *alongside* the content — the prompt is decorative. |
| **NF-2** | **MEDIUM** | **Download-cap / `single_use` race.** `max_downloads` and single-use are checked against a count whose increment is fire-and-forget (`go func()`), so N concurrent requests all pass a cap of 1 and single-use links can be consumed multiple times. | `ISSUE-SHARELINK-DOWNLOAD-CAP-RACE-01` | `handleShareLinkDownload`. Static; contradicts the "caps enforced per access = verified-good" framing. Re-confirmed 2026-07-25. |
| **NF-3** | **HIGH** | **SEC-3 is worse than originally stated** — the `httpOnly=false` cookie is a live session/API bearer → XSS = token theft, not a read surface. Elevated to HIGH to match SEC-3 / `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01`. | `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01` | Confirmed: the auth middleware accepts the cookie as a credential, between dev tokens and the `Authorization` header. |
| **NF-4** | **LOW–MED** | **`handleAutoLogin` hardcodes cookie `Secure=false`** (unlike the callback path which derives it from `Request.TLS`). If reachable in prod behind external TLS, the session cookie ships without Secure. | `ISSUE-AUTOLOGIN-COOKIE-INSECURE-01` | Confirmed. Routed at `GET /client-login[/]`. **Also note the reverse inconsistency:** auto-login sets `httpOnly=true` while the callback sets `httpOnly=false` — the two paths disagree in both dimensions. |
| **NF-5** | **LOW** (latent) | **Org-scoped authz is per-handler, not middleware.** The `/org/:org_id/admin` group checks only JWT-org *role*, not that `:org_id` == caller's org; the cross-tenant gate is `requireOrgAccess` copy-pasted into ~50 handlers. All current handlers call it (verified + live-tested), but a new handler that forgets it = silent cross-tenant BOLA. | `ISSUE-ORG-SCOPE-CHECK-PER-HANDLER-01` | Hoist an org-match check into the sub-group. |
| **NF-6** | **LOW** (compliance) | **Audit trail records deletions but not grants.** An `audit_log` table exists; share creation, group-member *adds*, permission grants, role changes and logins write nothing. A one-sided trail is arguably worse than none under IOCD. Compounds RB-3. | `ISSUE-AUDIT-TRAIL-INCOMPLETE-01` | **Made precise 2026-07-25:** there are exactly six writers — `organization.update`, four group/department **deletes**, and the GC cascade. So it is "deletes plus one org-update", not strictly delete-only. Table is in `internal/db/migrations/001_initial_schema.cql` (the original cite omitted `internal/db/`). |
| **NF-7** | **LOW** (config) | **Both upload size guards can be disabled together.** `max_upload_mb=0` (documented "unlimited") + `chunked_staging_max_bytes=0` ⇒ first chunk does `Truncate(attacker_total)` with an unbounded **logical** size. UP-6's "FIXED" is config-dependent. | `ISSUE-UPLOAD-SIZE-GUARDS-BOTH-ZERO-01` | **Sharpened 2026-07-25:** *every* shipped config sets `chunked_staging_max_bytes: 0`, `config.prod.yaml` included. The only bound in prod is `max_upload_mb: 102400` (100 GiB): a single upload may declare a **logical** size of up to 100 GiB. On sparse-file-capable filesystems (ext4/xfs) `Truncate` does **not** allocate 100 GiB immediately; physical pressure grows as chunks are written. Concurrent sessions can still exhaust `/tmp` because no aggregate staging budget is enabled. `Validate()` rejects a negative value but has no both-zero invariant. **Neither `chunked_staging_max_bytes` nor `server.max_upload_mb` has an env override** (the `SEAFHTTP_*` set covers only token TTL and the zip limits), so unlike the replication settings there is no `.env` lever here — the YAML value is the effective one. |

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
| ~~§6 multi-DC layout~~ | ~~`dc-na:1,dc-eu:1,dc-asia:1`~~ | ❌ **This "correction" was itself wrong — withdrawn 2026-07-25.** It claimed the triple was stale because `config.prod.yaml` ships `datacenter1: 1`. That reads only the YAML. The shipped prod template `.env.prod.example` sets `CASSANDRA_REPLICATION_DCS=dc-na:1,dc-eu:1,dc-asia:1` (and LOCAL_QUORUM/SERIAL) as **active** lines; `Config.Load()` → `applyEnvOverrides()` replaces `ReplicationDCs` before `Validate()`. Real production is multi-DC NA/EU/Asia. **The original document was right.** See §6. |
| Secrets (§1) | HMAC key "required if empty" | Stronger — also rejects the shipped **default** value in prod. |
| CORS / SEC-4 refs | `server.go:264–293` / "§4.3" | Ref drift — CORS fail-fast is in `config.go` `Validate()`; M2M route is `PUT …/admin/organizations/:org_id`. |
| Verdict | Conditional-go single-region | **No-go as-is** — NF-1 and B4 are single-node blockers. |
| SEC-4 | "API key only = open Medium gap" | **Reframed:** admin API key **is** the intended Accounts channel and already works. Optional signing deferred (`ISSUE-ACCOUNTS-M2M-REQUEST-SIGNING-01`); M2M hygiene is `ISSUE-ACCOUNTS-M2M-PATH-01`. |
| NF-3 severity | MEDIUM in addendum | **HIGH** — aligned with SEC-3 / `ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01`. |
| NF-7 `/tmp` | "reserve 100 GiB" | **Logical size** up to 100 GiB; sparse files do not allocate immediately. |

### Confirmed as-written (no change)
UP-2, UP-3, UP-5, UP-7; DL-1, DL-2, **DL-3 (critical fail-open genuinely closed across all surfaces)**, DL-4, DL-7; SH-2, SH-3; RB-2, RB-3, RB-4; §1 solid claims 1–5 (OIDC defaults on, sessions hashed+TTL, secrets, CORS default-deny, HSTS/nosniff); GC-disabled, SERIAL posture, no-replication-gate.

---

## Code validation (2026-07-25)

Every finding above was re-read against `main` at `0dac50993`. **No finding was
withdrawn.** Shape changes:

| # | Change | Detail |
|---|--------|--------|
| NF-1 | **Widened** | A second disclosure on the same missing gate: `buildOnlyOfficeShareBootstrap` mints a `CreateLinkDownloadToken` for an anonymous caller when OnlyOffice is enabled and the file is an office document. It is evaluated *before* the text branch, has no 1 MB ceiling, and is not limited to text. A fix or a regression test that only covers `.md`/`.txt` will leave it open. |
| B4 | **Narrowed + scoped** | Several surfaces the original wording covered *do* have limiters. Residual gap is the seafhttp upload/download/block group. **X10 is the authenticated block-PUT / aggregate-bound slice** of that umbrella, not the whole B4 surface; both live under `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` with four closable subcontracts. |
| NF-7 | **Sharpened** | The staging guard is `0` in *every* shipped config including prod; Truncate wording corrected to logical vs sparse physical allocation. |
| NF-6 | **Made precise** | Six `audit_log` writers, not zero-plus-deletes: one `organization.update`, four deletes, one GC cascade. |
| Verdict | **Corrected** | Conditional-go → **no-go as-is** (NF-1 + B4 are single-node). |
| SEC-4 | **Reframed** | Accounts admin API-key channel already works; not an auth gap. |
| §6 multi-DC | **A prior "correction" withdrawn** | The addendum had marked `dc-na:1,dc-eu:1,dc-asia:1` stale on the strength of `config.prod.yaml` saying `datacenter1: 1`. That was a YAML-only reading. `.env.prod.example` ships the triple as **active** lines; `applyEnvOverrides()` applies them before `Validate()`; real production is NA/EU/Asia. **The original claim was correct.** |
| UP-4 / NF-7 | **Extended** | Neither `chunked_staging_max_bytes` nor `server.max_upload_mb` has an env override, so for these the YAML *is* the effective config and an operator configuring through `.env` cannot enable the guard at all. |

**Citation policy.** Prefer symbol names over `file.go:NNNN`. PR-10 (#146) shifted
`sync.go` and invalidated several line cites (e.g. `SyncHandler.GetBlock`).

**Config-reading policy (added after the §6 mistake).** Effective configuration is
`configs/*.yaml` **plus** the environment. `Config.Load()` parses YAML, then
`applyEnvOverrides()` replaces selected fields, then `Validate()` checks the
result — `Validate()` does **not** apply overrides. Never call a config value
stale from the YAML alone — check `.env.prod.example` and `applyEnvOverrides()`
first. Both directions bite: replication settings are env-driven (YAML is a
placeholder), while the upload-size guards have no env override at all.

**Not re-verified live.** This pass was a code read against `main`, not a rerun
of the multiregion stack. The 2026-07-24 empirical results for B1/B2/B5 stand as
recorded; the OnlyOffice half of NF-1 is confirmed by reading only and should be
driven live when the fix is written.

---

## Status changes after the audit snapshot

Everything above is the audit as written on its date. This is the only section
that moves. It records **what changed and when** — not a second status column;
the authoritative state is always `KNOWN_ISSUES.md`.

| Audit id | Issue id | Change | Date |
|---|---|---|---|
| NF-1 / SH-6 | `ISSUE-SHARELINK-PASSWORD-BYPASS-01` | **Fixed.** Password resolved before the inline read and before the OnlyOffice token mint; the bundle builder drops protected content it is handed; the OnlyOffice helper fails closed on its own. Coverage: integration proves both halves live — both public endpoints for inline content, and a `.docx` fixture for the OnlyOffice credential, since `.md` never enters that branch. Both mutation-verified by reverting the guards and rebuilding the server. | 2026-07-25 |
| B4 (subcontract A) / SH-1 | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **A1 and A2 closed under the precise post-token subcontract.** The initial A1 guard landed 2026-07-28; stable remint identity and fail-closed blank-`SourceID` handling completed A1 on 2026-07-29. Every new link token must have a non-empty stable source ID; writers reject blanks, and `HandleUpload` fails closed. After a valid token resolves as link-origin, A1 counts attempts per link and per (IP, stable link identity), then A2 applies non-blocking process-local caps (defaults `16` per source / `128` per node; ceilings `4096` / `65536`, with per-source <= per-node when both are enabled). A1 reserves the stable source first, so an exhausted link cannot grow per-client state with attacker-controlled IPs; if the later per-client reservation rejects, only the accepted source reservation is rolled back. A2 429s still consume successful A1 reservations. `Source` is unknown before token resolution, so arbitrary invalid-token traffic and Cassandra lookup remain outside the guards. Remints preserve the exact source ID, verified by a live Cassandra test that also checks migration 013 and blank writer rejection. Metrics cover rejection count, current node in-flight work, and source occupancy without source labels. Browser retries use `max(Retry-After, capped exponential backoff)`, one-sided jitter with a server floor, a 16-second exponential cap, and a throttled ceiling of 30; behavioral coverage proves recovery after 13 consecutive 429s. State remains process-local, authenticated web uploads remain untouched, and B/C/D remain open. | 2026-07-29 |
| B4 (subcontract B) / X10 | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **Reduced, not closed.** The `PutBlock` per-request body cap was right-sized 257 MiB → configurable 16 MiB default (ceiling 64 MiB); the old figure came from the web uploader's adaptive-chunk ceiling, which never governed this route. The per-request buffered-body bound drops approximately 16×. **The aggregate bound — the actual defect — is untouched:** N concurrent uploads still cost N × the cap. B4 remains a blocker; B, C, and D remain open even though A1/A2 are closed. | 2026-07-29 |
| B4 (subcontract B) / X10 | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **Superseded by the two later 2026-07-30 entries.** Aggregate admissions landed, but this intermediate state still lacked reproducible client evidence and used the original uncorrected memory estimate. | 2026-07-30 |
| B4 (subcontract B) / X10 | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **Superseded by the closure entry below.** Client recovery became reproducible; the implementation audit then identified lifetime, waiter, budget-enforcement and proof gaps. | 2026-07-30 |
| B4 (subcontract B) / X10 | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **B closed.** A pre-gate global ticket plus per-user/node admissions bound active, transitioning and parked requests before body reads. A real connection read deadline interrupts stalled HTTP bodies while respecting earlier parent/server deadlines; object-storage cancellation and finite Cassandra boundaries cover later phases. The original plateau-only 64 MiB design failed the stronger probe and was replaced rather than waived: three clean 24-slot processes sampled request ramp, held-body plateau and post-release drain, measured worst 59.5 MiB raw / 74.4 MiB safety-adjusted per admission, and support a rounded 80 MiB design under the explicit 2 GiB budget. Deterministic integration proves both gates and a 1,000-identity regression proves bounded transient cardinality. Real `seaf-cli` recovered from the shipped `10s → 503 → Retry-After: 10` cycle, published byte-identical payloads and drained every slot. **B4 remains a blocker only because C and D are untouched.** | 2026-07-30 |
| B4 (subcontract C) / X11 | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **C closed.** `check-blocks` now takes an admission from its **own** limiter instance — separate capacity from B, asserted in both directions, because the two routes exhaust different resources — before the body is read, and holds an admitted lifetime. Accepted work is bounded three ways rather than by the parse cap alone: ids are deduplicated before any lookup, both metadata phases run at the configured `check_blocks_lookup_fanout` (validated ceiling 32, replacing hardcoded defaults), and mapping plus canonical location/existence dispatches are cancellation-aware. Cancellation stops dispatching new work; already-issued Cassandra queries remain bounded by the driver's finite timeout. The node budget is the enforced product `check_blocks_max_inflight_per_node × check_blocks_lookup_fanout` (8 × 8, ceiling 256). The id cap became configuration capped at its inherited 100k — lowerable only, with `sync_check_blocks_ids_per_request` as the evidence for doing so later; that histogram measures parsed lists, including malformed traffic that reached the parser, and the cap was deliberately not lowered on a guess. The opt-in real Cassandra/MinIO probe completed 100k legacy mappings in 57.62s and 100k canonical location/existence checks in 2m10.30s, below the shipped 5m lifetime. X10's gzip finding was still live on this route, so the new lifetime could not reach the socket and the fail-closed path dropped every request; the integration suite caught it before merge and the route is now excluded, with a real-TCP regression over the shipped middleware. Real `seaf-cli` was refused, retried, published byte-identical payloads and drained every slot. **B4 remains a blocker only because D is untouched.** | 2026-07-31 |
| B4 (subcontract D) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **D0 contract recorded; no runtime status change.** The contract expands D from two named GETs to every storage-backed byte/inline-content producer: seafhttp files, ZIP, block GET, current/history raw, share raw and public inline text. It freezes one atomic process-local node admission with namespaced authenticated-user and stable-link/client dimensions, bounded identity state, strict download-token `SourceID` wiring in D2, actual-route gzip/writer reachability, independent preparation and idle-write lifetimes, and block GET streaming without nominal-size accounting regression. D1-D6 is the reviewable PR sequence. The Compose anonymous-object-storage policy is separately tracked as `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`; byte-rate shaping remains a separate deferred residual. See `docs/SEAFHTTP-DOWNLOAD-ADMISSION-D0.md`. | 2026-08-01 |
| B4 (subcontract D2) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **D2 closed.** All public download-token mint paths now derive the same stable non-secret `SourceID` from the share-link token: normal download, public OnlyOffice and public ZIP. Token writers reject blank identities, remints preserve the exact identity, and `HandleDownload` / `HandleZipDownload` fail closed before protected work when a link token lacks one. Focused unit tests cover the three flows, strict consumers and the token stores; a live Cassandra test covers download-token remints and migration 013. Admission producer wiring remains deferred to D4. | 2026-08-01 |
| B4 (subcontract D3) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **D3 closed.** Added a reusable idle-write writer that probes connection reachability before headers, requires cancellation, uses an absolute deadline from the last successful output progress, rejects short writes, cancels after an idle-progress timeout, invalidates stale timer callbacks, clears deadlines on completion, and fails closed when `ResponseController` cannot reach the connection. A deferred Gin status does not start the timer; an immediately committed status does. Corrected Go gzip exclusions for the actual `/repo` raw/history paths, `/d/...`, and both public inline bootstrap routes; the supported frontend nginx configuration disables both gzip and proxy buffering for raw/history, share raw, bootstrap and `/seafhttp/` transfers. Focused writer tests include deadline progress, cancellation, error callbacks, `WriterTo` behavior, unreachable writers and a real TCP slow-client timeout; configuration regression and nginx syntax checks guard the frontend directives. Producer acquisition/release remains deferred to D4, while D6 owns the real-nginx slow-client drill and production capacities. | 2026-08-02 |
| B4 (subcontract D4) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **D4 closed.** One bootstrapped process-local coordinator is shared by SeafHTTP, FileView and ShareLink handlers. File, ZIP, authenticated raw/history, public share raw and inline text/Markdown acquire before protected preparation, propagate the preparation context through Cassandra and S3 setup, install D3's idle-write writer before output and release after the real response lifetime; ZIP holds through `zipWriter.Close()`. Focused tests cover wiring, profiles, public-link identity, refusal before reader setup, panic/error release and preparation-context propagation. D5 block GET streaming and D6 measured capacity/real-nginx evidence remain open. | 2026-08-02 |
| B4 (subcontract D5) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **D5 closed.** Authenticated `GET /seafhttp/repo/:repo_id/block/:block_id` now takes `ProfileBlock` on the same bootstrapped coordinator and streams through the existing `CanonicalBlockReader.GetBlockSize`/`GetBlockReader` instead of buffering the whole block into `[]byte`. Admission is acquired after the cheap permission/id/mapping gates and before the canonical reader is constructed; the preparation context bounds reader resolution and sizing; D3's idle-write writer is installed before the body; the lease is released after the reader is closed and the response ends. The authoritative opaque size drives the quota pre-check and `Content-Length`, while recorded traffic uses bytes successfully written, so a partial transfer does not regress to nominal-size overbilling; the quota pre-check itself runs on the preparation context, and `last_accessed` — which keeps its current placement after the quota check and before the body — now does too, so neither can hold a slot past the preparation deadline on a Cassandra stall. A reader that reaches EOF early is released as `storage_error`, not `completed`: `io.Copy` reports no error there, so the bytes copied are compared against the authoritative size, which turns the content-addressing expectation into an asserted invariant instead of an assumption. Focused tests cover disabled-transparency, saturated-profile refusal before reader construction, cheap rejects not consuming slots, the `size → touch → reader` ordering with zero buffered `GetBlock` calls, panic release, and — through a recording seam, because `traffic.Get()` is nil under test and an inline call would make the assertion vacuous — that a truncated transfer bills exactly the delivered bytes and not the nominal size. Billing the nominal size and dropping the short-read guard each fail their test, mutation-verified. Two real-socket regressions cover writer reachability on this route, which is what makes the idle-write deadline a bound at all: the shipped gzip stack must leave the block GET writer unwrapped, and a middleware that hides the connection must fail the transfer closed with an observable `download_admission_writer_unreachable_total` rather than stream unprotected. Removing the block gzip exclusion turns the first into a 503, mutation-verified. Real `seaf-cli` sync (11/11, plain and encrypted) exercised the refactored route end to end. D6 measured capacity/real-nginx evidence remains open. Two findings are explicitly **not** closed by D5 and must not be read as covered by it: `ISSUE-BLOCK-CROSS-LIBRARY-READ-01` (authorization is still evaluated against the route's `repo_id` while the physical lookup is keyed by `(org_id, block_id)`), and `ISSUE-SYNC-LINK-TOKEN-AUTH-01` — D5 classifies this route as authenticated traffic because the sync surface has no legitimate public flow, but that is a dependency on `syncAuthMiddleware`, not something D5 proves. While the latter is open, criterion 4 fairness is not end-to-end proven here; it is not a D5 regression and does not gate D5, since subcontracts B and C were closed keying their admissions on identities from the same middleware. | 2026-08-03 |
| B4 (subcontract D) | `ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01` | **New, recorded open — blocks D6 enablement.** Found reviewing D5. An admitted request has no D-owned deadline between the end of preparation and its first response write: `StartStreaming` cancels the preparation context and returns a deadline-free cancelable context, while the idle-write timer is armed only by actual output progress. Gin's deferred status means `c.Status(200)` does not commit headers, so it does not arm it either. A stalled first storage read — `GetBlockReader` on the D5 path, `StreamBlocks`' block-0 prefetch on the D4 path — therefore holds its slot until the client disconnects or the object-store SDK times out, neither of which is the configured bound. Shared by D4 and D5 rather than introduced by either, and latent while `download_admission.enabled` is `false` in every shipped configuration. The fix belongs in the shared lifecycle, not per producer. D6 is consequently **not** measurement and nginx evidence alone. | 2026-08-03 |
| B4 (subcontract D) | `ISSUE-DOWNLOAD-ADMISSION-PRE-FIRST-WRITE-GAP-01` | **Closed.** Fixed in the shared lifecycle as two rules, because arming alone is not sufficient. `IdleWriteWriter.StartIdleInterval` opens the interval at the streaming phase change — after the writer is installed, before the preparation deadline is retired, outside the lifecycle mutex since the timer callback claims the terminal cause, with a full rollback if it fails. And a deferred Gin header now *preserves* that interval instead of clearing it: `c.Status(200)` sits exactly where the old `clearDeadlineWithoutProgress` branch would have erased it moments after arming. Real progress still restarts the interval from the progress instant, so the span since the phase change can approach `2 × idle_write_timeout`; the initial callback must not survive it, which is regressed. The timeout claims the cause and cancels the work, but the lease is released only by the producer's deferred `Finish` — freeing capacity whose read is still executing would let the coordinator admit past its real ceiling, and that ordering is now pinned on both producers. Coverage: seven writer-level regressions, a blocked `GetBlockReader` (D5), a blocked first prefetch (D4) and fast-failure cases proving arming does not commit headers so a quick open failure still answers 404/500. Bounding the phase also exposed a terminal state that could not occur while it hung: a failed writer rejects every later write, including the producer's own pre-header error, so Gin committed its default `200` through the underlying writer and a timed-out download reached the client as an empty file that transferred successfully — indistinguishable, on block GET, from a legitimately empty block. `Finish` now answers `503` with `Retry-After` when a transfer ends on a non-`completed` cause having committed no output (`completed`, `client_disconnect` and `panic` excluded, the last so Gin's recovery keeps its `500`), and never touches a response that already started. Swapping the status alone would not have worked: producers stage the file's representation headers before their first storage read, so the `503` would have inherited a `Content-Length` for the whole file, `net/http` would have closed the connection on the undelivered body, and the client would read an unexpected EOF instead of the `Retry-After` contract — breaking the retry path the fix exists to enable. The entity headers are dropped and `Cache-Control: no-store` / `Content-Length: 0` set, by named list rather than blanket clear so CORS, security and quota-warning headers survive. A classification race closed with it: the writer commits to failed under its own mutex and only then calls back to claim the cause, so a handler finishing inside that handoff could record a killed transfer as `completed`; `finishCause` now consults the writer's terminal error first. Mutation-verified in every direction, including that the D4 producer test fails on the deferred-header rule alone. D6 returns to being measurement and nginx evidence. | 2026-08-03 |
| B4 (subcontract D6) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **D6 closed, and with it the B4 umbrella.** Per-admission cost was measured rather than estimated: 4.0 MiB for plaintext, ~68 MiB measured / 72 MiB design for encrypted streaming at the accepted 16 MiB block, and ~184.5 MiB measured / 192 MiB design for the 32 MiB encrypted iWork preview. The iWork source cap applies only to the buffering preview branch, preserving ordinary raw streams. Auto capacity derives the effective budget from the cgroup percentage or 2 GiB fallback, applies 20% safety headroom, and ships the clean baseline as 16 active slots with 4 raw and 12 other streams in all seven configs. Evidence: real `seaf-cli` produced an attributed profile=block refusal and still reached `synchronized` with every file pulled and every slot drained; a stalled client through the real frontend nginx is classified as `idle_write_timeout`, not `client_disconnect`; `raw` and `file` live simultaneously at the real node ceiling with `active_current == sum(active_by_profile)` and `entries_current == active + waiters`; an opt-in probe measures RSS, heap and cgroup deltas under real storage and middleware, though it holds ordinary raw streams rather than the modelled iWork worst case and no figure from it is recorded here; both directions of public-link/owner isolation, each gated on the other side genuinely refusing first; 20,000 distinct identities drain in the coordinator unit test; byte-for-byte identical delivery direct and proxied. **Explicitly not closed:** `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01`, and `ISSUE-DOWNLOAD-BYTE-RATE-SHAPING-01`, now quantified at 4.8x aggregate scaling over a six-way budget. Throughput figures are loopback numbers and need re-measuring on production hardware. | 2026-08-03 |
| B4 (subcontract D6 follow-up) | `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01` | **Evidence re-audited 2026-08-04.** The fault drill now sums only retryable `profile=block` reasons (`admission_timeout`, `auth_user_full`, `node_full`, `profile_full`, `auth_user_queue_full`, `node_queue_full`), excludes `client_gone`, and recorded 33 such refusals. The queue-full pair is counted because `admission_wait` is non-zero and `max_waiters_per_identity` is 4, so a refused client can be turned away at the queue rather than at the gate; both are equally attributable capacity refusals. Under the same saturated holders it observed HTTP 503 with `Retry-After: 10`; real `seaf-cli` reached `synchronized`, pulled all three files and drained every admission. That historical 2 GiB combination is superseded by the auto-derived clean baseline with 20% design headroom; the current config and regression coverage derive 16 slots with 4 raw and 12 stream capacity. | 2026-08-04 |
| Object-storage posture | `ISSUE-OBJECT-STORAGE-ANONYMOUS-DOWNLOAD-01` | **Recorded open.** Supported Compose definitions currently grant anonymous bucket downloads, which bypass application auth, quotas, traffic recording and D admission when a bucket/key is known. The clean production deployment must prove effective private-bucket policy and reject unauthenticated object GETs. | 2026-08-01 |
| Sync authentication | `ISSUE-SYNC-LINK-TOKEN-AUTH-01` | **New single-node blocker, recorded open.** `syncAuthMiddleware` accepts any valid `TokenTypeDownload` token as a repository credential without rejecting `Source == "link"`, requiring the repository-root token shape, or binding the token's `RepoID` to the route's `:repo_id`. A public share-link download bearer — which the `dl=1` redirect hands to the anonymous visitor — therefore authenticates to the `/seafhttp/repo/:repo_id/*` sync surface as the link creator, and `checkSyncPermission` then evaluates the creator's library permissions rather than the link's narrower grant. Found during the D2 audit and **pre-existing**: D2 changes download-token `SourceID` wiring and does not touch this middleware. This supersedes the "Sync Protocol Permissions ✅ Complete / `syncAuthMiddleware` hardened" status recorded on 2026-02-11, which is now open in `KNOWN_ISSUES.md`. | 2026-08-02 |

**Effect on the verdict: none.** It stays **no-go as-is**. B4
(`ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`) is closed as an application-level guard,
but anonymous object storage and the sync-link authorization blocker remain
single-node reachable; closing NF-1 and B4 does not remove those production
conditions.

> **Dated note, 2026-08-02.** The sentence above counts the single-node blockers
> as they stood when NF-1 closed. `ISSUE-SYNC-LINK-TOKEN-AUTH-01` has since been
> recorded as a third one; see its row above. The verdict is unchanged — still
> **no-go as-is** — but the blocker set is now B4, the sync public-link token
> auth gap, and the separately tracked object-storage posture issue.

**A taxonomy wrinkle worth naming rather than silently fixing.** The summary
table lists SH-1 under *High* for Sharing while listing B4 as a *blocker* under
Security posture — but SH-1 **is** subcontract A of B4 (the anonymous
upload-link write path). So "Sharing" inherits a blocker through SH-1 no matter
what its own row says, and with NF-1 fixed that is now the only blocker Sharing
carries. The table is left as written because it is snapshot prose; read the
blocker set from the blockers table and from `ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01`'s
subcontracts, not from the per-area summary.
