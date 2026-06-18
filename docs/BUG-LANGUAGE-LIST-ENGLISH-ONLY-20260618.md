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
