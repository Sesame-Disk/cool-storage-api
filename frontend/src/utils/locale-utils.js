// supported-locales.json is the single source of truth for the UI locale set,
// shared with the Go backend (internal/api/bootstrap.go reads the same data and
// is drift-guarded by TestSupportedLocalesMatchCanonicalJSON). Add/remove a
// language here and both the selector and the i18n whitelist follow.
import SUPPORTED_LOCALES from './supported-locales.json';

export const SUPPORTED_UI_LOCALES = Object.freeze(SUPPORTED_LOCALES.map((l) => l.code));

// LOCALE_ALIASES maps a normalized (lowercased, hyphenated) code to its canonical
// form, derived from the shared locale set so it can never drift from it.
const LOCALE_ALIASES = Object.freeze(
  SUPPORTED_LOCALES.reduce((aliases, l) => {
    aliases[l.code.toLowerCase()] = l.code;
    return aliases;
  }, {})
);

export function normalizeLocaleCode(locale) {
  const key = String(locale || '').trim().replace(/_/g, '-').toLowerCase();
  return LOCALE_ALIASES[key] || '';
}

// firstValue unwraps the array i18next-http-backend passes to a loadPath function
// (languages/namespaces arrive as arrays); it returns the single value otherwise.
export function firstValue(value) {
  return Array.isArray(value) ? value[0] : value;
}

export function ensureSupportedLocale(locale, fallback = 'en') {
  return normalizeLocaleCode(locale) || fallback;
}

export function isChineseLocale(locale) {
  return ensureSupportedLocale(locale) === 'zh-CN';
}

export function resolveSeafileEditorLocaleAsset(locale) {
  return ensureSupportedLocale(locale);
}

export function resolveSdocEditorLocaleAsset(locale) {
  const normalized = ensureSupportedLocale(locale);
  return normalized === 'zh-CN' ? 'zh_CN' : normalized;
}

export function resolveCalendarLocaleKey(locale) {
  switch (ensureSupportedLocale(locale)) {
    case 'zh-CN':
      return 'zh-CN';
    case 'fr':
      return 'fr';
    case 'de':
      return 'de';
    case 'cs':
      return 'cs';
    case 'ru':
      return 'ru';
    case 'es':
    case 'es-AR':
    case 'es-MX':
      return 'es';
    default:
      return 'en';
  }
}
