// Vitest setup mirrors src/setupTests.js (Jest) but uses jest-dom's vitest entry.
import '@testing-library/jest-dom/vitest';

// Mock window.app — kept in sync with src/setupTests.js
window.app = {
  config: {
    siteRoot: '/',
    loginUrl: '/login',
    avatarInfo: {},
    logoPath: '/logo.png',
    mediaUrl: '/media/',
    siteTitle: 'SesameFS',
    siteName: 'SesameFS',
    logoWidth: 100,
    logoHeight: 40,
    isPro: 'True',
    isDBSqlite3: false,
    isDocs: 'False',
    lang: 'en',
    fileServerRoot: '/seafhttp/',
    useGoFileserver: true,
    seafileVersion: '9.0.0',
    serviceURL: 'http://localhost:8000',
    avatarURL: '/avatars/',
    faviconPath: '/favicon.ico',
    loginBGPath: '/login-bg.png',
    enableRepoAutoDel: false,
  },
  pageOptions: {
    name: 'Test User',
    contactEmail: 'test@example.com',
    username: 'test@example.com',
    canAddRepo: true,
    canShareRepo: true,
    canAddGroup: true,
    canGenerateShareLink: true,
    canGenerateUploadLink: true,
    canSendShareLinkEmail: true,
    canViewOrg: 'False',
    enableUploadFolder: 'True',
    enableResumableFileUpload: 'True',
    maxFileName: 255,
    enableEncryptedLibrary: true,
    userRole: 'default',
  },
};

window.gettext = (text) => text;

class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}
window.ResizeObserver = ResizeObserver;

// matchMedia mock — used by react-responsive
if (!window.matchMedia) {
  window.matchMedia = (query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  });
}
