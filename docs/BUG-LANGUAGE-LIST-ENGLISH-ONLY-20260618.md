# Bug: profile language selector only offers English

**Date:** 2026-06-18
**Severity:** Low (feature gap — translations exist but can't be selected)
**Area:** `internal/api/bootstrap.go` (bootstrap `langList`), `frontend/src/components/user-settings/language-setting.js`
**Guard test:** `sesamefs-bugs.spec.ts` → "profile language list offers more than English (translations exist)" (`@bug`).

## What happens

In **Settings → Language**, the language dropdown shows only **English**, even though the app ships with several translated locales. The dropdown is populated from the bootstrap payload's `app_page_options.langList`, which the backend hard-codes to a single English entry.

## Root cause

`GET /api/v2.1/bootstrap/` builds page options in `buildAppBootstrapPageOptions`, where `langList` is hard-coded:

- [`internal/api/bootstrap.go:421-426`](../internal/api/bootstrap.go#L421)
```go
"langList": []gin.H{
    {
        "langCode": "en",
        "langName": "en",
    },
},
```

The frontend renders the dropdown straight from this list:
- `frontend/src/components/user-settings/language-setting.js` → `options` from `pageOptions.langList` (falls back to just the current language when the list is empty/short).

Meanwhile the editors' i18n config already declares the supported locales:
- `frontend/src/_i18n/i18n-seafile-editor.js` and `frontend/src/_i18n/i18n-sdoc-editor.js`:
  `whitelist: ['en', 'zh-CN', 'fr', 'de', 'cs', 'es', 'es-AR', 'es-MX', 'ru']`

So translations for **9 locales** exist (loaded at runtime from `media/.../locales/<lang>/...`), but the profile selector never offers them because `langList` lists only English.

## Proposed fix

Populate `langList` from the supported locales instead of the single hard-coded entry. Minimal change in [`internal/api/bootstrap.go`](../internal/api/bootstrap.go#L421):

```go
"langList": []gin.H{
    {"langCode": "en",    "langName": "English"},
    {"langCode": "zh-CN", "langName": "中文"},
    {"langCode": "fr",    "langName": "Français"},
    {"langCode": "de",    "langName": "Deutsch"},
    {"langCode": "cs",    "langName": "Čeština"},
    {"langCode": "es",    "langName": "Español"},
    {"langCode": "es-AR", "langName": "Español (Argentina)"},
    {"langCode": "es-MX", "langName": "Español (México)"},
    {"langCode": "ru",    "langName": "Русский"},
},
```

Better: derive the list from a single source of truth (a config key or the same locale set the i18n whitelist uses) so the selector and the loadable catalogs can't drift. Also fix the current-language label (`currentLang.langName` is `"en"` rather than `"English"`).

Note: there is currently **no persisted per-user language field** in the profile API — language is applied via an `i18n/?lang=<code>` redirect that sets a cookie. If language should persist per user, add a `language` field to the account/profile and have the SPA apply it on load. The guard test only checks that the selector offers more than English; persistence is a separate enhancement.

## Status (2026-06-19): partially fixed — selector now lists all locales

`buildAppBootstrapPageOptions` now emits the full locale set (`bootstrapLangList`, single
source `supportedLocales`) instead of a single English entry, so the **selector offers all
9 languages** — the reported bug is resolved.

## Status (2026-06-20): fixed — language switching wired end-to-end

Language selection now works front to back:

1. **Selector lists all locales** — `buildAppBootstrapPageOptions` emits the full
   `supportedLocales` set (single source of truth) via `bootstrapLangList()`.
2. **`/i18n/` backend handler** (`handleLanguageChange` in `internal/api/bootstrap.go`,
   registered in `server_routes.go`) validates `?lang=<code>` against `supportedLocales`,
   persists it in the `lang` cookie (1y, path `/`), and redirects back to the originating
   page (`sameOriginRedirectTarget` — Referer if same-origin, else `/`; no open-redirect).
   Unsupported codes write no cookie but still redirect, so a garbage value can't strand
   the user.
3. **`currentLang`/`langCode` reflect the cookie** — `handleBootstrap` calls
   `resolveBootstrapLocale(c)` and passes it into `buildAppBootstrapPageOptions`, so after a
   reload the selector shows the user's active choice (label via `localeDisplayName`).
4. **i18next/moment load the chosen catalog** — `loadBootstrap` mirrors the resolved
   `langCode` into `window.app.config.lang` before app bundles import `constants.js`, so the
   i18n init picks it up.
5. **nginx** — `frontend/nginx.conf` proxies `/i18n/` to the backend (not the SPA).

### Single source of truth for the locale set

`frontend/src/utils/supported-locales.json` is the canonical list (code + display name).
`locale-utils.js` derives `SUPPORTED_UI_LOCALES` and the alias map from it; the Go backend
mirrors it in `internal/api/bootstrap.go` (`supportedLocales`) and the mirror is drift-guarded
by `TestSupportedLocalesMatchCanonicalJSON` (fails if codes/names/order diverge). Add a language
in the JSON + the Go slice together.

### 🔴 IMPORTANT — most of the UI still does NOT translate

This branch makes the selector and locale plumbing work, but **switching language only localizes
the embedded editors and date/calendar formatting**, not the bulk of the SPA. The primary UI i18n
is `window.gettext(...)` (~387 files), which is currently an **identity stub** with no translation
catalog behind it. This is a separate, larger gap tracked as **ISSUE-I18N-UI-01** — see
`docs/I18N-UI-TRANSLATION-GAP-20260620.md` and TECHNICAL-DEBT.md §20. Do not read "language
switching fixed" as "UI is translated".

### ⚠️ Persistence: cookie only — NOT stored in the DB per user

The selected language is persisted **exclusively in the `lang` browser cookie**. There is
**no per-user `language` column in ScyllaDB and no profile/account field** that records the
choice. Concrete implications:

- The preference is **per browser/device**, not per account. The same user on a different
  browser, an incognito window, or after clearing cookies starts again from the default
  (`en`).
- Nothing in the user record, the `users` table, or the Accounts identity reflects the
  language — `resolveBootstrapLocale` reads the cookie and nothing else.
- Server-side rendered/functional pages (share links, file viewer, etc.) do **not** know the
  user's language unless they also carry the cookie.

This is an intentional, low-risk scope choice — it makes the selector fully functional
without schema changes or Accounts integration. Adding true per-user persistence (a
`language` field on the profile/account, applied on login) remains a **possible future
enhancement**; it is explicitly out of scope here.

Guard tests in `internal/api/bootstrap_test.go`: `TestBootstrapLangListOffersAllSupportedLocales`
(pins the original >1-locale regression), `TestBuildAppBootstrapPageOptionsReflectsLocale`,
`TestResolveBootstrapLocale`, `TestHandleLanguageChange` (cookie + redirect + cross-origin).

---

### Original triage notes (2026-06-19)

`buildAppBootstrapPageOptions` was emitting a single hard-coded English `langList` entry, and
`currentLang.langCode` was hard-coded to `"en"`; the `i18n/?lang=…` URL had no backend handler.
All three are addressed above.
