import { defineConfig, devices } from '@playwright/test';

// Mixed suite: API-driven specs (using the `request` fixture) and one
// browser-driven login spec. All run under a single Chromium project.
export default defineConfig({
  testDir: './tests',
  timeout: 45_000,
  expect: { timeout: 12_000 },
  fullyParallel: false, // lockout tests are stateful; keep deterministic ordering
  workers: 1,
  retries: 0,
  reporter: [['list'], ['json', { outputFile: 'results/results.json' }]],
  use: {
    headless: true,
    ignoreHTTPSErrors: true,
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
});
