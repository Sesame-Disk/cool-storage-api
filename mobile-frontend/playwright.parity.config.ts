import { defineConfig, devices } from '@playwright/test';
import { BASE_URL, authStateFile } from './e2e-parity/helpers/parity-helpers';

// ---------------------------------------------------------------------------
// Multi-viewport, full-CRUD parity suite. Every spec runs on ALL viewport
// projects against a LIVE disposable stack (default http://localhost:18073).
// Auth is seeded once per role in global-setup (token in localStorage), so the
// default storageState logs in as the end user; org-admin specs opt in via
//   test.use({ storageState: authStateFile('admin') })
// ---------------------------------------------------------------------------

// NOTE on secure context: the PWA uses Service Workers + crypto.subtle (upload
// hashing), which browsers only enable on a SECURE context (HTTPS or
// http://localhost). For containerized runs that reach the app via a container
// host (http://mobile-frontend), point PARITY_BASE_URL at http://localhost:<port>
// and set PARITY_PROXY_TARGET so global-setup starts a loopback forwarder to the
// service (see helpers/secure-proxy.ts). No Chromium flags are needed.

const VIEWPORTS = [
  { name: 'phone-small', device: devices['iPhone SE'] },
  { name: 'phone', device: devices['Pixel 5'] },
  { name: 'phone-large', device: devices['iPhone 14 Pro Max'] },
  { name: 'tablet-portrait', device: devices['iPad Mini'] },
  { name: 'tablet-large', device: devices['iPad Pro 11'] },
  {
    name: 'landscape',
    device: { ...devices['Pixel 5'], viewport: { width: 851, height: 393 } },
  },
];

export default defineConfig({
  testDir: './e2e-parity/specs',
  globalSetup: './e2e-parity/global-setup.ts',
  // Backend state is shared; keep artifact names unique (see unique()) so
  // parallel viewport projects don't collide. Serial fallback via PW_WORKERS=1.
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // One retry by default: multi-viewport e2e against a live stack has inherent
  // timing flakiness (parallel service-worker registration, backend latency).
  // A flaky-then-pass is reported as "flaky", not a failure.
  retries: process.env.PW_RETRIES ? Number(process.env.PW_RETRIES) : 1,
  workers: process.env.PW_WORKERS ? Number(process.env.PW_WORKERS) : undefined,
  timeout: 60_000,
  expect: { timeout: 12_000 },
  reporter: process.env.PW_HTML ? [['list'], ['html', { open: 'never' }]] : [['list']],
  use: {
    baseURL: BASE_URL,
    storageState: authStateFile('user'),
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    ignoreHTTPSErrors: true,
  },
  // Force the Chromium engine for every size project so the matrix is about
  // viewport/geometry, not browser engine (some iPhone/iPad presets default to
  // WebKit). A dedicated real-WebKit iOS project is added in Phase 4 hardening.
  projects: [
    ...VIEWPORTS.map((v) => ({
      name: v.name,
      use: { ...v.device, browserName: 'chromium' as const, defaultBrowserType: 'chromium' as const },
    })),
    // Real-WebKit iOS Safari, scoped to the foundation specs (routing, SW,
    // install prompt) — the surface most likely to behave differently on Safari.
    // The full CRUD matrix runs on the Chromium size-projects above.
    {
      name: 'ios-safari',
      testMatch: /(smoke|pwa|deep-links)\.spec\.ts/,
      use: { ...devices['iPhone 13'] },
    },
  ],
});
