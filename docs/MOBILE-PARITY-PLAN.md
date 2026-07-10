# Mobile Frontend — 1:1 Parity + PWA + Multi‑Viewport E2E Plan

**Target project:** `mobile-frontend/` (Astro 5 + React 19 islands + Tailwind 4)
**Goal:** A mobile-optimized web app that (a) reaches **1:1 feature parity** with the web frontend's **end‑user and org‑admin** features (sysadmin `/sys` is out of scope), (b) is a **installable PWA** that prompts to install, adds a home‑screen shortcut, and runs standalone/full‑screen like a native app, and (c) has a **Playwright suite that exercises every operation across many screen sizes with full CRUD against a fresh disposable stack**.

**Decided constraints (from product owner):**
- Build **dedicated mobile UI** in `mobile-frontend/` reusing the existing backend APIs (not a responsive re-wrap of the web app).
- Tests do **full CRUD against a live, disposable stack** (seeded fresh per run), matching the `e2e-localauth` / `e2e-sesamefs` pattern.

---

## 0. Current state (baseline — already built)

The mobile app is **~70% complete**. Do **not** rebuild these; extend them.

| Area | Status | Where |
|------|--------|-------|
| Auth (email/password via `/api2/auth-token/`, SSO/OIDC, dev bypass) | ✅ | `src/components/auth/`, `src/lib/oidc.ts` |
| Libraries (list/create/encrypted/sort/pull‑refresh) | ✅ | `src/components/libraries/`, `pages/LibraryList` |
| File browse/rename/delete/move/copy/star/preview | ✅ | `src/components/files/`, `pages/FileBrowser` |
| Upload (file/camera/folder, queue, retry, conflict) | ✅ | `src/lib/upload.ts`, `UploadButton` |
| Sharing (public links + QR, internal user/group, share‑admin) | ✅ | `src/components/share/`, `pages/ShareAdmin` |
| Groups (list/create/members/repos) | ✅ | `pages/GroupList`, `GroupDetail` |
| Activity, Starred, Search | ✅ | `pages/ActivityFeed`, `StarredFiles`, `SearchPage` |
| Dark mode, offline (IndexedDB + SW), bottom nav | ✅ | `src/lib/offlineDb.ts`, `public/sw.js` |
| PWA scaffold (manifest, SW, offline.html, 8 icons) | 🟡 ~70% | `public/manifest.json`, `public/sw.js` |
| Tests (51 unit + 15 e2e specs) | ✅ | `src/**/__tests__`, `e2e/`, `e2e-desktop/`, `e2e-sesamefs/` |

Single integration point for the backend: **`src/lib/api.ts`** (131 exports today). All new endpoints go here. The canonical endpoint reference is the web app's **`frontend/src/utils/seafile-api.js`** (~250 methods) — mobile calls must match it **exactly**.

---

## 1. Parity gap matrix (what's missing → workstream)

Legend: ❌ missing · 🟡 partial. Each row becomes one (or more) job file(s). "Web ref" is the file/route to match exactly.

### End‑user gaps
| # | Feature | Status | Web ref |
|---|---------|--------|---------|
| E1 | **Trash / recycle bin** (list, restore item, clean) | ❌ | `pages/repo-trash/`, `api: getRepoFolderTrash/restoreDirents/deleteRepoTrash` |
| E2 | **File history & versions** (commits, view at commit, revert file/dir/repo) | ❌ | `pages/repo-history/`, `repo-snapshot/`, `file_revisions/` |
| E3 | **File tags / repo tags** (CRUD + tagged-file listing) | ❌ | `api: *repo-tags*, *file-tags*` |
| E4 | **Deleted libraries** (list + restore whole library) | ❌ | `api: getDeletedRepos/restoreDeletedRepo` |
| E5 | **Block upload protocol** for large files (session→check→blocks→commit) | 🟡 | `api: block-upload-session/blocks/upload-commit` |
| E6 | **Extra previews**: audio, markdown render, office (OnlyOffice), SeaDoc | 🟡 | `components/file-view/` |
| E7 | **Upload links** (create/list/delete public upload endpoints) | 🟡 | `pages/share-admin/upload-links.js` |
| E8 | **Custom share permissions** (CRUD profiles) | ❌ | `api: custom-share-permissions` |
| E9 | **Advanced search** (type/date/size/library filters) | 🟡 | `pages/search/advanced-search.js` |
| E10 | **Settings/profile**: avatar, language, API tokens, 2FA, linked devices, notifications, delete account | 🟡 | `pages/settings/`, `user-settings/` |
| E11 | **Local‑auth login integration** (`sesameauth` `/api/v2.1/auth/local/login`) + password change, alongside SSO | 🟡 | `docs/LOCAL-AUTH.md`, `pages/login/index.js` |

### Org‑admin area (entirely absent from mobile today — new `/org` section)
| # | Feature | Web route |
|---|---------|-----------|
| A1 | Org user management (list/create/edit/delete/reset‑password/quota/role, admins) | `/org/useradmin` |
| A2 | Org group management (list/create/edit/delete/transfer/members/repos) | `/org/groupadmin` |
| A3 | Org library management (list/search/info) + trash/restore | `/org/repoadmin`, `/org/repoadmin-trash` |
| A4 | Org share‑link admin (list/revoke) | `/org/publinkadmin` |
| A5 | Org info / web settings / branding | `/org/info`, `/org/web-settings` |
| A6 | Departments (hierarchy, dept libraries, members) *(if enabled)* | `/org/departmentadmin` |
| A7 | Device management (desktop/mobile/errors) | `/org/deviceadmin` |
| A8 | Statistics & reporting (files/storage/users/traffic) | `/org/statistics-admin` |
| A9 | Audit logs (file/update/permission) | `/org/logadmin` |
| A10 | Subscription | `/org/subscription` |
| A11 | SAML/ADFS config *(if enabled)* | `/org/samlconfig` |

> **Scope note / recommendation:** A1–A5 are the core org‑admin surface and clearly belong on mobile. A6–A11 are data/config‑heavy and desktop‑oriented (charts, log tables, SAML XML). Recommendation: ship A1–A5 as full interactive parity, and treat A6–A11 as **read‑only / deferred** unless strict 1:1 is required. **This is the one open decision** (see §7).

---

## 2. PWA completion (finish the ~30% remaining)

The manifest + service worker + offline page + icons already exist. Remaining work:

- **Install prompt (Android/Chromium):** capture `beforeinstallprompt`, stash the event, and show a custom "Install Sesame Disk" bottom sheet on first eligible visit; call `prompt()` on tap; record dismissal so it isn't nagging.
- **Install guidance (iOS/Safari):** no `beforeinstallprompt` on iOS — detect iOS Safari + not-standalone and show an "Add to Home Screen" instructions sheet (Share → Add to Home Screen).
- **App shortcuts:** add `shortcuts` to `manifest.json` (Libraries, Upload, Search, Starred) → long‑press launcher menu.
- **Standalone/full‑screen polish:** confirm `display: "standalone"` + `display_override: ["standalone","minimal-ui"]`, `viewport-fit=cover` (present), safe‑area insets on top/bottom bars, theme‑color for light/dark.
- **Update UX:** detect a waiting service worker (`updatefound`/`statechange`) and show a "New version — reload" toast that calls `skipWaiting` + reloads.
- **Richer install UI:** add `screenshots` (narrow + wide) to the manifest.
- **(Optional, "app‑like") Web Share Target:** register as a share target so the OS share sheet can send files/links into the app for upload.
- **Gate:** a Lighthouse PWA/installability check must pass in CI (see §3).

---

## 3. Multi‑viewport, full‑CRUD E2E harness (the test ask)

**New suite:** `mobile-frontend/e2e-parity/` with `playwright.parity.config.ts`. This is the backbone that makes "every operation, every screen size, real CRUD" auditable.

**Viewport matrix (Playwright `projects` — every spec runs on all):**
| Project | Device / size | Class |
|---------|---------------|-------|
| `phone-small` | iPhone SE (375×667) | small phone |
| `phone` | Pixel 5 (393×851) | standard phone |
| `phone-large` | iPhone 14 Pro Max (430×932) | large phone |
| `tablet-portrait` | iPad mini (768×1024) | small tablet |
| `tablet-large` | iPad Pro 11 (834×1194) | large tablet |
| `landscape` | Pixel 5 landscape (851×393) | rotated |

**Auth against the disposable stack** (real login, two roles):
- Use a **global setup** that logs in once per role and saves `storageState` (the mobile app keeps the token in `localStorage.seahub_token`, so seed that + any cookie). Roles: **end‑user** (`user@sesamefs.local`) and **org‑admin** (`admin@sesamefs.local`).
- Prefer the app's real login path. In dev‑mode the stack accepts dev tokens; for a production‑like run, log in via `sesameauth` `/api/v2.1/auth/local/login`. Provide both via env (`PARITY_AUTH_MODE=dev-token|local`).

**Data isolation & CRUD discipline** (mirror `e2e-sesamefs` conventions):
- Every spec prefixes its artifacts (`pw-mob-<suite>-<uuid>`), creates what it needs, asserts, and cleans up in `afterEach`/global teardown. No shared mutable fixtures.
- Destructive ops (delete library, revoke share, delete org user) run only on artifacts the spec created.

**Spec organization** — one spec file per parity feature, each a full lifecycle:
```
e2e-parity/
  helpers/parity-helpers.ts     # login, api wrappers, unique-name, cleanup (model on sesamefs-helpers.ts)
  specs/auth.spec.ts            # local + SSO stub + logout + password change
  specs/libraries.spec.ts       # create→rename→encrypt→delete→restore(deleted)
  specs/files.spec.ts           # upload(small+block)→rename→move→copy→star→delete→trash-restore
  specs/preview.spec.ts         # image/video/text/pdf/audio/md render
  specs/sharing.spec.ts         # share link CRUD + upload link + internal user/group + custom perms
  specs/groups.spec.ts          # group + members + group library lifecycle
  specs/search-activity.spec.ts # advanced search + starred + activity
  specs/settings.spec.ts        # profile/avatar/language/api-token/notifications
  specs/history.spec.ts         # versions + revert
  specs/org-admin.spec.ts       # A1–A5 (role: org-admin)
  specs/pwa.spec.ts             # manifest served, SW registers, install-prompt UI, installability
```

**Coverage ledger:** maintain `e2e-parity/PARITY-MATRIX.md` mapping every web operation → spec/test name → ✅/❌. "Every operation tested" = this ledger is 100% green. No silent gaps: any operation not yet covered is listed as ❌, not omitted.

**Containerization (bind mounts DON'T work here — must COPY at build):**
- `mobile-frontend/Dockerfile.parity` (base `mcr.microsoft.com/playwright:v1.58.2-noble`) copies `mobile-frontend/` and runs `npx playwright test --config=playwright.parity.config.ts`.
- Compose service `mobile-parity-e2e` under a `mobile-parity-test` profile: `depends_on` sesamefs (healthy) + mobile-frontend; env `BASE_URL=http://mobile-frontend:80`, `API_URL=http://sesamefs:8080`, `AUTH_URL=http://sesameauth:8080`.
- Screenshots/report written to a JSON reporter (host bind mounts are unavailable; pull artifacts via `docker cp` if needed, per the known constraint).
- Orchestration script `scripts/run-mobile-parity.sh up|test|down` seeds a fresh stack each run so CRUD is deterministic.

---

## 4. Phases & sequencing (how agents go corner‑to‑corner)

Each phase is a set of independent job files; within a feature, **UI + api.ts method + multi‑viewport CRUD spec + unit test** ship together (definition of done). A feature is not "done" until its parity‑matrix row is green in the disposable‑stack run.

- **Phase 0 — Test foundation (do first; everything else depends on it).**
  `playwright.parity.config.ts` + viewport matrix, `parity-helpers.ts`, global‑setup auth (both roles), `Dockerfile.parity` + compose service + `run-mobile-parity.sh`, empty `PARITY-MATRIX.md`, CI wiring. Also add a smoke spec that logs in and asserts the dashboard on all viewports.

- **Phase 1 — PWA completion (§2).** Independent, high visibility. Ends with `pwa.spec.ts` + Lighthouse installability gate green.

- **Phase 2 — End‑user parity gaps (§1 E1–E11).** One job per feature; each lands its spec. Suggested order: E11 (auth/local) → E1 (trash) → E2 (history) → E7 (upload links) → E5 (block upload) → E3 (tags) → E8 (custom perms) → E4 (deleted libs) → E6 (previews) → E9 (advanced search) → E10 (settings).

- **Phase 3 — Org‑admin (§1 A1–A5, plus A6–A11 per §7 decision).** New `/org` section: role‑gated nav, then one job per admin function, each with `org-admin.spec.ts` coverage under the org‑admin role.

- **Phase 4 — Hardening.** Parity‑matrix audit to 100%, performance budget (interactive < 3s on throttled 3G, nav < 500ms), touch‑target/a11y pass (≥44px), iOS Safari pass, docs (`TESTING.md`, `docs/MOBILE.md`), and a final full disposable‑stack run.

**Rough size:** Phase 0 ≈ 3–4 jobs · Phase 1 ≈ 4–5 · Phase 2 ≈ 11–14 · Phase 3 ≈ 6–12 (depends on §7) · Phase 4 ≈ 3–4. **≈ 30–40 job files total.**

---

## 5. Definition of done (per feature job)
1. Mobile UI implemented with existing mobile patterns (BottomSheet, Toast, SwipeableListItem, FAB, skeletons, pull‑to‑refresh).
2. `src/lib/api.ts` methods match `seafile-api.js` **exactly** (endpoint, params, body).
3. Multi‑viewport CRUD spec added to `e2e-parity/`, passing on **all** viewport projects against the fresh disposable stack.
4. Vitest unit/component test added.
5. `PARITY-MATRIX.md` row flipped to ✅.
6. `npm run check` (typecheck + lint + unit) green; feature works in the real containerized app.

## 6. Risks / advice
- **Org‑admin is ~40% of the surface.** Don't let it balloon the project — resolve §7 before Phase 3.
- **iOS PWA limits:** no `beforeinstallprompt`, constrained SW/background — plan iOS‑specific UX and don't test‑assert Android‑only behaviors on WebKit.
- **External‑service previews** (OnlyOffice office docs, SeaDoc) depend on the `onlyoffice` service and SeaDoc backend — gate those specs on availability.
- **Auth divergence:** mobile uses `/api2/auth-token/` + `localStorage` token; the new local‑auth uses `sesameauth` + `sesamefs_auth` cookie. E11 must reconcile these so one login UI serves dev‑token, local, and SSO.
- **Keep `api.ts` the single seam** — never call `fetch` from components; this keeps the 250‑endpoint surface auditable.

## 7. Org‑admin scope — DECIDED
**Core only.** A1–A5 (users, groups, libraries, share‑link admin, org info/web‑settings) are built **fully interactive** on mobile. A6–A11 (statistics, audit logs, devices, departments, subscription, SAML) are **NOT built on mobile**; the mobile `/org` section shows those entries with an **"Open in desktop version" redirect** to the web frontend origin (deep‑link to the corresponding `/org/...` route). This keeps every feature *reachable* from mobile while avoiding desktop‑shaped UIs on a phone.

## Locked requirements (build target)
1. **Org‑admin:** Core only (A1–A5) interactive; A6–A11 → redirect to desktop web `/org/...`.
2. **Auth:** one mobile login supporting local‑auth (`sesameauth`) + SSO/OIDC + dev‑token, plus password change — identical behavior to web.
3. **Testing:** Playwright, multi‑viewport (6 projects), **full CRUD against the current running local deployment** (dev‑mode auth by default; opt‑in local‑auth mode). Roles: end‑user + org‑admin.
4. **Coverage:** `PARITY-MATRIX.md` 100% green; nothing silently skipped.
5. **PWA:** install prompt (Android + iOS guidance), shortcuts, standalone/safe‑area polish, update‑reload flow, installability gate.
6. **Execution:** build end‑to‑end in one continuous effort (the `.claude-agents` job pipeline is retired/buggy — not used).
