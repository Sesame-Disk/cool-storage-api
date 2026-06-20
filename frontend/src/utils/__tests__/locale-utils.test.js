import {
  SUPPORTED_UI_LOCALES,
  ensureSupportedLocale,
  isChineseLocale,
  normalizeLocaleCode,
  resolveCalendarLocaleKey,
  resolveSdocEditorLocaleAsset,
  resolveSeafileEditorLocaleAsset,
} from '../locale-utils';

describe('locale-utils', () => {
  test('exports the supported UI locale list', () => {
    expect(SUPPORTED_UI_LOCALES).toEqual([
      'en',
      'zh-CN',
      'fr',
      'de',
      'cs',
      'es',
      'es-AR',
      'es-MX',
      'ru',
    ]);
  });

  test('normalizes legacy locale aliases to canonical codes', () => {
    expect(normalizeLocaleCode('zh-cn')).toBe('zh-CN');
    expect(normalizeLocaleCode('zh_CN')).toBe('zh-CN');
    expect(normalizeLocaleCode('es-ar')).toBe('es-AR');
    expect(normalizeLocaleCode('es_MX')).toBe('es-MX');
    expect(normalizeLocaleCode('fr')).toBe('fr');
    expect(normalizeLocaleCode('')).toBe('');
  });

  test('falls back to English for unsupported locales', () => {
    expect(ensureSupportedLocale('klingon')).toBe('en');
    expect(ensureSupportedLocale('', 'es')).toBe('es');
  });

  test('detects Chinese locale after normalization', () => {
    expect(isChineseLocale('zh-CN')).toBe(true);
    expect(isChineseLocale('zh_cn')).toBe(true);
    expect(isChineseLocale('es')).toBe(false);
  });

  test('maps locale codes to shipped editor asset directories', () => {
    expect(resolveSeafileEditorLocaleAsset('zh-cn')).toBe('zh-CN');
    expect(resolveSeafileEditorLocaleAsset('es_MX')).toBe('es-MX');
    expect(resolveSdocEditorLocaleAsset('zh-cn')).toBe('zh_CN');
    expect(resolveSdocEditorLocaleAsset('es-ar')).toBe('es-AR');
  });

  test('maps regional Spanish calendar locales to the shared Spanish pack', () => {
    expect(resolveCalendarLocaleKey('es')).toBe('es');
    expect(resolveCalendarLocaleKey('es-AR')).toBe('es');
    expect(resolveCalendarLocaleKey('es-mx')).toBe('es');
    expect(resolveCalendarLocaleKey('zh-CN')).toBe('zh-CN');
  });
});
