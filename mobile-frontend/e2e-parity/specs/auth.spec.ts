import { test, expect } from '@playwright/test';
import { CREDENTIALS, TOKEN_KEY, waitForAppShell } from '../helpers/parity-helpers';

// Unified authentication (E11). Drives the REAL login page against the live
// stack with sesameauth's local-auth advertised. Unlike the other parity specs
// these run UNauthenticated (fresh storageState) so the login form actually
// renders and we exercise a full local-auth login end to end.
//
// Uses the same seeded superadmin credential the rest of the harness logs in
// with (parity-helpers CREDENTIALS.super), so there is a single source of truth
// for the bootstrap password rather than a hardcoded literal that can drift.
test.use({ storageState: { cookies: [], origins: [] } });

const LOCAL_CREDS = {
  email: CREDENTIALS.super.email, // superadmin@sesamefs.local
  password: CREDENTIALS.super.password,
};

test.describe('Unified auth: local login', () => {
  test('login page advertises local auth and renders the email/password form', async ({ page }) => {
    await page.goto('/login/');

    // The form island mounts client-side; wait for its inputs.
    await expect(page.getByTestId('login-email')).toBeVisible();
    await expect(page.getByTestId('login-password')).toBeVisible();
    await expect(page.getByTestId('login-submit')).toBeVisible();
  });

  test('local login navigates out of /login and establishes a session', async ({ page }) => {
    await page.goto('/login/');
    await expect(page.getByTestId('login-email')).toBeVisible();

    await page.getByTestId('login-email').fill(LOCAL_CREDS.email);
    await page.getByTestId('login-password').fill(LOCAL_CREDS.password);
    await page.getByTestId('login-submit').click();

    // On success the app redirects to /libraries/ and stores the session token.
    await expect(page).not.toHaveURL(/\/login\/?/, { timeout: 20_000 });

    const token = await page.evaluate((key) => window.localStorage.getItem(key), TOKEN_KEY);
    expect(token, 'session token stored after local login').toBeTruthy();

    // And a real authenticated view loads.
    await page.goto('/libraries/');
    await waitForAppShell(page);
  });

  test('wrong password surfaces a login error and stays on /login', async ({ page }) => {
    await page.goto('/login/');
    await expect(page.getByTestId('login-email')).toBeVisible();

    await page.getByTestId('login-email').fill(LOCAL_CREDS.email);
    await page.getByTestId('login-password').fill('definitely-not-the-password');
    await page.getByTestId('login-submit').click();

    await expect(page.getByTestId('login-error')).toBeVisible({ timeout: 15_000 });
    // No session was established.
    const token = await page.evaluate((key) => window.localStorage.getItem(key), TOKEN_KEY);
    expect(token).toBeFalsy();
  });
});
