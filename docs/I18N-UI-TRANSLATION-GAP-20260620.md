# UI Translation Gap — `window.gettext` Is an Identity Stub (2026-06-20)

**Status**: 🔴 Known gap / tech debt — intentionally NOT fixed in branch
`fix/language-selector-locales` (scope kept small).
**Severity**: High for any non-English deployment — the language selector is wired
end-to-end but the bulk of the SPA UI does not translate.
**Tracked as**: ISSUE-I18N-UI-01 (`docs/KNOWN_ISSUES.md`),
TECHNICAL-DEBT.md §20.

---

## Summary

The language selector now works front to back (cookie persistence, `/i18n/`
backend handler, `currentLang`/`langCode` resolution, i18next + moment locale
switching — see `docs/BUG-LANGUAGE-LIST-ENGLISH-ONLY-20260618.md`). **But switching
language only translates a small slice of the product.** The vast majority of the
React UI stays in English regardless of the selected locale.

## Root cause

Two translation systems coexist in the frontend, and only the minor one is wired:

1. **`window.gettext(...)` — the primary UI i18n.** Used by **387 source files**
   (`import { gettext } from '.../constants'`). This is the Django-style catalog
   mechanism inherited from Seafile.

2. **i18next (`t()` / `useTranslation`) — editors only.** Used by **0 application
   files** directly; only the bundled `@seafile/seafile-editor` and sdoc-editor
   components call it internally. This is what the language selector, `locale-utils`,
   and the i18n whitelist actually drive.

In the frontend/backend separation, the Django `jsi18n` catalog that used to populate
`window.gettext` was dropped and replaced by an **identity stub**:

```js
// frontend/src/bootstrap/runtime-bootstrap.js
if (typeof window.gettext !== 'function') {
    window.gettext = (message) => message;   // returns the English source string as-is
}
```

So every `gettext('Some label')` returns `'Some label'` verbatim. There is **no
translation catalog anywhere in the repo** (`.po` / `.mo` / `djangojs` / gettext JSON)
and **no backend endpoint** that serves one.

### Verification (2026-06-20)

- `grep -rln "import { gettext"` → 387 files; `useTranslation|withTranslation|i18n.t(`
  in app code → 0.
- `window.gettext` is only ever assigned the identity stub
  (`runtime-bootstrap.js`, `share-runtime-bootstrap.js`).
- No `*.po` / `*.mo` / `djangojs*` files in the repo (outside `node_modules`).
- No `jsi18n` / JSON-catalog route in `internal/`.

## What actually changes when you switch language today

- ✅ Embedded markdown/sdoc editors (i18next catalogs under
  `static/locales` / `sdoc-editor/locales`).
- ✅ Date/calendar formatting (`moment`, `@seafile/seafile-calendar` via
  `date-format-utils.js`).
- ✅ A handful of hard-coded conditionals (e.g. the Chinese Social Login labels).
- ❌ Everything wrapped in `gettext(...)` — i.e. essentially the whole SPA chrome,
  dialogs, admin panels, settings, menus. Stays English.

## What a real fix requires (future project, not this branch)

This needs new infrastructure **and** translation data that does not exist yet:

1. **String extraction**: harvest `gettext()` / `ngettext()` call sites into a `.po`
   template, one catalog per supported locale (the 9 in
   `frontend/src/utils/supported-locales.json`).
2. **Translation content**: actually translate those catalogs (the missing data).
3. **Serving**: an endpoint (e.g. `GET /api/v2.1/i18n/catalog/?lang=<code>`) or static
   per-locale JSON assets, keyed off the same `lang` cookie the selector already sets.
4. **Loading**: the bootstrap fetches the active locale's catalog and installs a real
   `window.gettext` / `window.ngettext` (lookup + interpolation + plural rules) **before**
   the app bundle renders — mirroring how `config.lang` is already set pre-render in
   `loadBootstrap`.

### Possible phasing

- **Phase A (loader only)**: build steps 3–4 with empty/placeholder catalogs so the
  plumbing exists and is testable; UI still shows English until catalogs are filled.
- **Phase B (data)**: extraction + translation (steps 1–2) to actually localize the UI.

Until then, the selector should be understood as "switches editors + dates + locale
preference", not "translates the application".
