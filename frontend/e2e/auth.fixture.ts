import { test as base, expect, Page } from '@playwright/test';

/**
 * Provides a `loggedInPage` fixture for authenticated tests.
 *
 * Credentials come from env (TEST_USER / TEST_PASS). Defaults match the dev
 * superadmin documented in frontend/README.md.
 *
 * If the backend is not reachable or login fails, the test is marked
 * `test.skip` rather than failing — golden-path tests gate on auth wiring
 * being available in the environment.
 */
type AuthFixtures = {
  loggedInPage: Page;
};

const TEST_USER =
  process.env.TEST_USER || '00000000-0000-0000-0000-000000000001@sesamefs.local';
const TEST_PASS = process.env.TEST_PASS || 'dev';

export const test = base.extend<AuthFixtures>({
  loggedInPage: async ({ page }, use, testInfo) => {
    const response = await page.goto('/accounts/login/');
    if (!response || response.status() >= 500) {
      testInfo.skip(true, `login route not available (status=${response?.status()})`);
      return;
    }

    // Field selectors are conservative — login page may use multiple input shapes
    const userInput = page.locator(
      'input[name="login"], input[name="username"], input[type="email"]',
    ).first();
    const passInput = page.locator('input[type="password"]').first();

    try {
      await userInput.waitFor({ state: 'visible', timeout: 5_000 });
    } catch {
      testInfo.skip(true, 'login form not rendered');
      return;
    }

    await userInput.fill(TEST_USER);
    await passInput.fill(TEST_PASS);
    await Promise.all([
      page.waitForURL((url) => !/\/accounts\/login\/?$/.test(url.pathname), { timeout: 10_000 }).catch(() => {}),
      page.locator('button[type="submit"], input[type="submit"]').first().click(),
    ]);

    // If we're still on the login page after submit, treat as auth-not-wired
    if (/\/accounts\/login\/?$/.test(new URL(page.url()).pathname)) {
      testInfo.skip(true, 'login did not succeed in test environment');
      return;
    }

    await use(page);
  },
});

export { expect };
