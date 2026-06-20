import { ensureAppGlobals } from '../share-runtime-bootstrap';

function clearLangCookie() {
  document.cookie = 'lang=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/';
}

describe('share-runtime-bootstrap ensureAppGlobals locale', () => {
  beforeEach(() => {
    clearLangCookie();
    delete window.app;
    delete window.SESAMEFS_CONFIG;
  });

  afterEach(() => {
    clearLangCookie();
  });

  test('applies a valid lang cookie to config.lang so public viewers init in that locale', () => {
    document.cookie = 'lang=fr; path=/';
    ensureAppGlobals();
    expect(window.app.config.lang).toBe('fr');
  });

  test('normalizes a legacy-cased cookie to the canonical code', () => {
    document.cookie = 'lang=zh-cn; path=/';
    ensureAppGlobals();
    expect(window.app.config.lang).toBe('zh-CN');
  });

  test('falls back to English when no cookie is present (anonymous visitor)', () => {
    ensureAppGlobals();
    expect(window.app.config.lang).toBe('en');
  });

  test('ignores an unsupported cookie value and keeps the default', () => {
    document.cookie = 'lang=klingon; path=/';
    ensureAppGlobals();
    expect(window.app.config.lang).toBe('en');
  });

  test('tolerates a malformed percent-encoded cookie without breaking the page', () => {
    document.cookie = 'lang=%; path=/';
    expect(() => ensureAppGlobals()).not.toThrow();
    expect(window.app.config.lang).toBe('en');
  });
});
