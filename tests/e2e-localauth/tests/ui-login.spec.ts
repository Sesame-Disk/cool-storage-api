import { test, expect } from '@playwright/test';

// Browser-driven login through the real frontend (nginx) origin. This exercises
// the full chain: the login page fetches /auth/methods (proxied to sesameauth),
// renders the local form, and submits to /auth/local/login (also proxied),
// which sets the session cookie and redirects into the app.
const FRONTEND = process.env.FRONTEND_URL ?? 'http://frontend:80';
const ADMIN_EMAIL = process.env.ADMIN_EMAIL ?? 'superadmin@sesamefs.local';
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD ?? 'BootstrapAdmin123';

test('the login page renders the local form when local auth is enabled', async ({ page }) => {
  await page.goto(`${FRONTEND}/login/`);
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible();
  await expect(page.getByLabel('Email')).toBeVisible();
  await expect(page.getByLabel('Password')).toBeVisible();
});

test('valid credentials log in via the UI and redirect out of /login', async ({ page }) => {
  await page.goto(`${FRONTEND}/login/`);

  await page.getByLabel('Email').fill(ADMIN_EMAIL);
  await page.getByLabel('Password').fill(ADMIN_PASSWORD);

  const [resp] = await Promise.all([
    page.waitForResponse((r) => r.url().includes('/api/v2.1/auth/local/login')),
    page.getByRole('button', { name: 'Sign in' }).click(),
  ]);
  expect(resp.status(), 'login request must succeed through the nginx proxy').toBe(200);

  // The app navigates away from the login page on success.
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 });

  // The session cookie set by sesameauth must be first-party on the app origin.
  const cookies = await page.context().cookies();
  expect(cookies.some((c) => c.name === 'sesamefs_auth')).toBe(true);
});

test('invalid credentials show an error and stay on the login page', async ({ page }) => {
  await page.goto(`${FRONTEND}/login/`);

  await page.getByLabel('Email').fill(ADMIN_EMAIL);
  await page.getByLabel('Password').fill('definitely-wrong-password');
  await page.getByRole('button', { name: 'Sign in' }).click();

  await expect(page.getByRole('alert')).toBeVisible();
  await expect(page).toHaveURL(/\/login/);
});
