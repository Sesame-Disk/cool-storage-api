import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright config for the SesameFS dev-mode E2E workload.
 *
 * Runs every spec in ./e2e-sesamefs — drop a new `*.spec.ts` in that folder and
 * it is picked up automatically (no wiring needed). Driven by scripts/run-playwright.sh
 * inside the `playwright` container, where DESKTOP_BASE_URL points at the frontend.
 *
 * workers=1 is deliberate: the concurrency specs generate heavy concurrent load
 * inside each test; parallel workers on top overwhelm the single-node dev backend.
 */
const baseURL = process.env.DESKTOP_BASE_URL || 'http://localhost:5173';

export default defineConfig({
  testDir: './e2e-sesamefs',
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.PW_RETRIES ? Number(process.env.PW_RETRIES) : 0,
  reporter: process.env.PW_HTML ? [['list'], ['html', { open: 'never' }]] : [['list']],
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'Desktop Chrome', use: { ...devices['Desktop Chrome'] } }],
});
