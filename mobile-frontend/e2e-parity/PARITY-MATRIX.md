# Mobile Parity Coverage Matrix

Single source of truth for "every operation is implemented **and** tested on
mobile." Each row maps a web-frontend operation → mobile status → the parity
spec/test that proves it against the live stack. **Done = every row ✅.**

Status: ✅ done · 🟡 partial · ❌ not started · ➡️ redirect-to-desktop (by design)

Legend for spec column: `<spec file>::<test title>` in `e2e-parity/specs/`.

## Foundation
| Item | Status | Spec |
|------|--------|------|
| Live-stack auth seeding (user + admin + super roles) | ✅ | `smoke.spec.ts` (global-setup) |
| App shell + 5-tab nav on 6 viewports | ✅ | `smoke.spec.ts::bottom nav exposes all 5 tabs` |
| Tab routing | ✅ | `smoke.spec.ts::tab navigation routes correctly` |

## Routing — deep/dynamic links (SPA app-shell fix)
> Dynamic routes had no prebuilt HTML and dead-ended to a redirect shell. Fixed
> via `404.astro` → `AppRouter` (nginx `try_files … /404.html`). All open now.

| Link | Status | Spec |
|------|--------|------|
| Open a library → file browser | ✅ | `deep-links.spec.ts::opening a library renders the file browser` |
| Open a subfolder (deep path) | ✅ | `deep-links.spec.ts::opening a subfolder deep-links into that path` |
| Open trash | ✅ | `deep-links.spec.ts::opening trash renders the trash page` |
| Open group detail | ✅ | `deep-links.spec.ts::opening a group route renders group detail` |
| Unknown route → in-app Not Found | ✅ | `deep-links.spec.ts::an unknown route shows the in-app Not Found` |

## Auth (E11)
| Operation | Status | Spec |
|------|--------|------|
| Local-auth (sesameauth) login | ✅ | `auth.spec.ts` (+ mobile nginx local-auth proxy) |
| Auth methods advertised in login UI | ✅ | `auth.spec.ts::login page advertises local auth` |
| Wrong-password error | ✅ | `auth.spec.ts::wrong password` |
| SSO/OIDC login | 🟡 | button rendered when advertised; live SSO not e2e'd |
| Dev-token login | ✅ | global-setup |
| Change password | 🟡 | `changePassword` API added; UI/e2e pending |

## Libraries
| Operation | Status | Spec |
|------|--------|------|
| List libraries | ✅ | `libraries.spec.ts` |
| Create (plain) | ✅ | UI create in `encrypted-library.spec.ts` / `office-docs.spec.ts` |
| Create + unlock ENCRYPTED library (E2E roundtrip) | ✅ | `encrypted-library.spec.ts` (+ fixed `encrypted` string→bool & unlock endpoint `/set-password/`) |
| Rename | 🟡 | pending |
| Delete | ✅ | `deleted-libs.spec.ts` (delete→restore) |
| Deleted-libs list + restore (E4) | ✅ | `deleted-libs.spec.ts` |

## Files
| Operation | Status | Spec |
|------|--------|------|
| Browse / breadcrumb | ✅ | `deep-links.spec.ts`, `files.spec.ts` |
| Create folder | ✅ | `files.spec.ts::create a folder` (+ SW cache-invalidation fix) |
| Create file | ✅ | (SW cache-invalidation fix in NewFileDialog) |
| Create office document (docx/xlsx/pptx) | ✅ | `office-docs.spec.ts` (New File → Word/Excel/PowerPoint → opens in OnlyOffice; template seeded server-side) |
| Shared office doc opens for owner + receiver | ✅ | `office-docs.spec.ts::a shared office document opens for the owner and the receiver` |
| Upload (small) | ✅ | `files.spec.ts::upload a small file` (+ SW cache-invalidation fix) |
| Upload (block, large) (E5) | ✅ | `files.spec.ts::large file ... block-upload` (+ added /api/v2/ proxy — was 405) |
| Rename | ✅ | `files.spec.ts::rename a file` |
| Delete → trash | ✅ | `files.spec.ts::delete a file` |
| Move / copy | ✅ | `files.spec.ts::move/copy` (+ fixed broken endpoint → sync-batch-move/copy-item) |
| Star / unstar | ✅ | `files.spec.ts::star a file` (+ SW cache-invalidation fix) |
| Download (file) | ✅ | `sharing-flows.spec.ts::upload…downloads with identical bytes` |
| Trash: list / restore / clean (E1) | ✅ | `trash.spec.ts` (+ restore JSON-body & clean fixes) |
| History / versions / revert (E2) | ✅ | `history.spec.ts` |
| Tags (repo tags CRUD) (E3) | ✅ | `tags.spec.ts` |
| Preview: image/video/text/pdf/code | ✅ | (existing viewers) |
| Preview: audio + markdown (E6) | ✅ | `preview.spec.ts` |
| Preview: office (OnlyOffice) | ✅ | `onlyoffice.spec.ts` (docx/xlsx/pptx open embedded editor + signed config) |
| Preview: seadoc | ➡️ | needs SeaDoc service; deferred |

## Sharing
| Operation | Status | Spec |
|------|--------|------|
| Share link create (from share sheet) | ✅ | `sharing.spec.ts::share sheet` (+ permissions string fix) |
| Share link list/delete | ✅ | `sharing.spec.ts::share link ... deletes via UI` |
| Upload link list/delete (E7) | ✅ | `sharing.spec.ts::upload link ... deletes via UI` |
| Share with user / group | ✅ | `sharing-flows.spec.ts::shares a folder with another user` (+ fixed `username` array / `p` param, share-item field mapping, SW cache-invalidation) |
| Public share link downloads (anon) | ✅ | `sharing-flows.spec.ts::share link downloads the file without authentication` |
| Custom share permissions (E8) | ✅ | `custom-permissions.spec.ts` |
| Share-admin management view | ✅ | `sharing.spec.ts` |

## Collaboration
| Operation | Status | Spec |
|------|--------|------|
| Groups list/create/delete | ✅ | `groups.spec.ts` |
| Group members add/remove/role | ✅ | `groups-departments.spec.ts` (role-gated: owner add/promote/delete, member leave) |
| Group libraries | ✅ | `groups.spec.ts` |
| Shared-with-me | ✅ | `sharing-flows.spec.ts::a library shared with the user appears` (+ fixed 404 `beshared-repos` → `/api2/repos/?type=shared`) |
| Search (full-text) | ✅ | `search.spec.ts` (+ fixed broken endpoint → `/api/v2.1/search/`) |
| Advanced search filters (E9) | ✅ | `search.spec.ts::file-type filter` |

## Settings (E10)
| Operation | Status | Spec |
|------|--------|------|
| Edit display name | ✅ | `settings.spec.ts` (PUT /api2/account/info/) |
| Storage/quota display | ✅ | `settings.spec.ts` |
| Email / role display | ✅ | `settings.spec.ts` |
| Reachable from More page | ✅ | MorePage `more-link-settings` |
| Avatar / language / notifications | 🟡 | pending |

## In-app navigation (test features one-by-one)
| Entry point | Status | Where |
|------|--------|------|
| More → Settings, Deleted Libraries | ✅ | `MorePage` feature links |
| Library → Tags, Permissions, History, Trash | ✅ | `deep-links.spec.ts::library header links` (FileBrowser header) |

## Org-admin — core (`/org/`, reachable from More→Org Admin)
| Operation | Status | Spec |
|------|--------|------|
| Users list + detail (A1) — **read-only** (backend disables org-admin user writes) | ✅ | `org-admin.spec.ts::users page` |
| Groups list (A2) | ✅ | `org-admin.spec.ts::groups page` |
| Libraries list + revoke (A3) | ✅ | `org-admin.spec.ts::libraries page` |
| Share-link admin list + revoke (A4) | ✅ | `org-admin.spec.ts::share-links page` |
| Org web-settings (A5) — read (backend PUT only accepts ext-whitelist) | ✅ | `org-admin.spec.ts::settings page` |
| Home nav + org-admin gating | ✅ | `org-admin.spec.ts::home`, `::More page exposes Org Admin` |

## Org-admin — redirect to desktop (by design)
| Operation | Status | Spec |
|------|--------|------|
| Statistics (A8) | ➡️ | `org-admin.spec.ts::redirects` |
| Audit logs (A9) | ➡️ | `org-admin.spec.ts::redirects` |
| Devices (A7) | ➡️ | `org-admin.spec.ts::redirects` |
| Departments (A6) | ✅ | `groups-departments.spec.ts` (org admin create/add-member/delete; users view their departments) |
| Subscription (A10) | ➡️ | `org-admin.spec.ts::redirects` |
| SAML (A11) | ➡️ | `org-admin.spec.ts::redirects` |

## Folder Sync (Seafile-style, one-way local→remote MVP)
> New capability (not a web-frontend parity row): pick a local disk folder via
> the File System Access API and differentially push changes to a library on
> app-focus + manual "Sync now". Gated — hidden/disabled on iOS Safari / Firefox
> (no `showDirectoryPicker`). Uses OUR modern block/multipart upload (SHA-256,
> `/api/v2/blocks/*`), never the legacy Seafile sync protocol / SHA-1.

| Operation | Status | Spec |
|------|--------|------|
| Differential plan (add/modify/delete/nested/no-change) | ✅ | `src/lib/sync/__tests__/diff.test.ts` (unit) |
| Capability gate (`supportsFolderSync`) | ✅ | `src/lib/sync/__tests__/capabilities.test.ts` (unit) |
| Per-library manifest + config store (IndexedDB) | ✅ | `src/lib/sync/__tests__/offlineDb-sync.test.ts` (unit) |
| Sync page: unsupported note when API absent | ✅ | `sync.spec.ts::sync page shows the unsupported note` |
| Sync page: synced-folders view when API present | ✅ | `sync.spec.ts::sync page shows the synced-folders view` |
| Library context menu: sync disabled when API absent | ✅ | `sync.spec.ts::library context menu disables sync` |
| Library context menu: "Sync this folder" when API present | ✅ | `sync.spec.ts::library context menu offers "Sync this folder"` |
| Real FS round-trip (pick → initial push → delta → unsync) | ➡️ | manual on Chrome/Edge/Android (`showDirectoryPicker` is a native dialog Playwright can't drive) |

## PWA
| Operation | Status | Spec |
|------|--------|------|
| Manifest served + valid (icons, display) | ✅ | `pwa.spec.ts::manifest is served and installable-shaped` |
| Service worker registers | ✅ | `pwa.spec.ts::service worker registers` |
| Install prompt (Android) | ✅ | `pwa.spec.ts::Android install prompt` |
| Install guidance (iOS) | ✅ | `pwa.spec.ts::PWA iOS guidance` |
| App shortcuts | ✅ | `pwa.spec.ts::manifest ... shortcuts` |
| Update-reload flow | 🟡 | PwaManager (waiting-worker prompt); e2e needs 2nd SW build |
| Installability gate (Lighthouse) | ❌ | Phase 4 CI check |

## Real WebKit / iOS Safari
| Item | Status | Spec |
|------|--------|------|
| Foundation on real Safari (smoke/pwa/deep-links) | ✅ | `playwright.parity.config.ts` project `ios-safari` (webkit) |

## Security / edge cases (adversarial)
| Case | Status | Spec / note |
|------|--------|-------------|
| XSS: malicious markdown content is inert | ✅ | `edge-cases.spec.ts` (escaped; `javascript:` links neutered; no `<script>`/`<img>` in DOM) |
| XSS: injection payload as file/folder NAME | ✅ | `edge-cases.spec.ts` (React-escaped, renders as text) |
| Encrypted library shared with a user still needs the password | ✅ | `edge-cases.spec.ts` (decrypt prompt; wrong pwd rejected; dir list 403 until unlocked) |
| Read-only recipient cannot write/delete/re-share | ✅ | verified at API (403 on upload-link/delete/rename/share) |
| Unauthorized access to private repo / admin endpoints denied | ✅ | verified at API (403 across the board) |
| Malformed / hostile `repo_id` → 404 (was **500**) | ✅ | backend fix in `db.ReadLibraryState` + `HasLibraryAccess` |
