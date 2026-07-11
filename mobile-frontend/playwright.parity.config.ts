import { defineConfig, devices } from '@playwright/test';
import { BASE_URL, authStateFile } from './e2e-parity/helpers/parity-helpers';

// ---------------------------------------------------------------------------
// Multi-viewport, full-CRUD parity suite. Every spec runs on ALL viewport
// projects against a LIVE disposable stack (default http://localhost:18073).
// Auth is seeded once per role in global-setup (token in localStorage), so the
// default storageState logs in as the end user; org-admin specs opt in via
//   test.use({ storageState: authStateFile('admin') })
// ---------------------------------------------------------------------------

// When the app is served over a non-localhost, non-HTTPS origin (e.g. the
// containerized parity run hits http://mobile-frontend), the browser treats it
// as an INSECURE context and disables secure-context-only APIs the PWA relies
// on: Service Workers and crypto.subtle (used by the upload SHA-256 hashing).
// A real end user reaches the app via http://localhost:<port>, which IS a
// secure context. Set PARITY_SECURE_ORIGIN=<origin> to tell Chromium to treat
// that origin as secure so the run faithfully mirrors the localhost experience.
const SECURE_ORIGIN = process.env.PARITY_SECURE_ORIGIN;
const chromiumLaunchOptions = SECURE_ORIGIN
  ? {
      launchOptions: {
        args: [`--unsafely-treat-insecure-origin-as-secure=${SECURE_ORIGIN}`],
      },
    }
  : {};

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
      use: { ...v.device, browserName: 'chromium' as const, defaultBrowserType: 'chromium' as const, ...chromiumLaunchOptions },
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
