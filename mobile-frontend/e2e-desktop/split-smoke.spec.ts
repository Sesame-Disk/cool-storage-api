import { expect, test, type Page } from '@playwright/test';

type Credentials = {
  email: string;
  password: string;
};

const credentials = {
  user: {
    email: process.env.DESKTOP_SMOKE_USER_EMAIL || 'user@sesamefs.local',
    password: process.env.DESKTOP_SMOKE_USER_PASSWORD || 'dev-token-user',
  },
  orgAdmin: {
    email: process.env.DESKTOP_SMOKE_ORG_ADMIN_EMAIL || 'admin@sesamefs.local',
    password: process.env.DESKTOP_SMOKE_ORG_ADMIN_PASSWORD || 'dev-token-admin',
  },
  sysAdmin: {
    email: process.env.DESKTOP_SMOKE_SYS_ADMIN_EMAIL || 'superadmin@sesamefs.local',
    password: process.env.DESKTOP_SMOKE_SYS_ADMIN_PASSWORD || 'dev-token-superadmin',
  },
} satisfies Record<string, Credentials>;

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

async function login(page: Page, user: Credentials, nextPath: string) {
  const encodedNext = encodeURIComponent(nextPath);

  await page.goto(`/login/?next=${encodedNext}`);
  await expect(page).toHaveURL(new RegExp(`/login/\\?next=${encodedNext}$`));

  await page.locator('#email').fill(user.email);
  await page.locator('#password').fill(user.password);
  await page.getByRole('button', { name: /log in/i }).click();

  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(nextPath)}/?$`));
  await expect(page.locator('#main')).toBeVisible();
}

test.describe('Desktop split smoke', () => {
  test('redirects unauthenticated app routes to login', async ({ page }) => {
    await page.goto('/dashboard/');

    await expect(page).toHaveURL(/\/login\/\?next=%2Fdashboard%2F$/);
    await expect(page.locator('#email')).toBeVisible();
    await expect(page.locator('#password')).toBeVisible();
  });

  test('completes login and returns to the requested dashboard route', async ({ page }) => {
    await login(page, credentials.user, '/dashboard/');

    await expect(page.getByRole('link', { name: 'All Activities' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Shared with me' })).toBeVisible();
  });

  test('logs out from the account menu', async ({ page }) => {
    await login(page, credentials.user, '/dashboard/');

    await page.locator('#my-info').click();
    await expect(page.locator('#user-info-popup')).toBeVisible();
    await page.getByRole('link', { name: 'Log out' }).click();

    await expect(page).toHaveURL(/\/login\/(\?next=%2F)?$/);
    await expect(page.locator('#email')).toBeVisible();
  });

  test('loads org admin routes for authorized users', async ({ page }) => {
    await login(page, credentials.orgAdmin, '/org/info/');

    await expect(page.locator('h3.sf-heading')).toContainText('Admin');
    await expect(page.getByRole('link', { name: 'Users' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Groups' })).toBeVisible();
  });

  test('blocks org admin routes for non-admin users before mount', async ({ page }) => {
    await login(page, credentials.user, '/org/info/');

    await expect(page.getByRole('heading', { name: 'Permission denied' })).toBeVisible();
    await expect(page.getByText('organization admin access', { exact: false })).toBeVisible();
  });

  test('loads sys admin routes for superadmins', async ({ page }) => {
    await login(page, credentials.sysAdmin, '/sys/info/');

    await expect(page.locator('h3.sf-heading')).toContainText('System Admin');
    await expect(page.getByRole('link', { name: 'Users' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Libraries' })).toBeVisible();
  });

  test('blocks sys admin routes for non-superadmins before mount', async ({ page }) => {
    await login(page, credentials.user, '/sys/info/');

    await expect(page.getByRole('heading', { name: 'Permission denied' })).toBeVisible();
    await expect(page.getByText('system admin access', { exact: false })).toBeVisible();
  });

  test('loads standalone and org subscription views when subscriptions are enabled', async ({ page }) => {
    await login(page, credentials.orgAdmin, '/dashboard/');

    const subscriptionEnabled = await page.evaluate(() => Boolean(window.app?.pageOptions?.enableSubscription));
    test.skip(!subscriptionEnabled, 'Subscriptions are disabled in the current environment.');

    await page.goto('/subscription/');
    await expect(page.locator('#current-plan')).toBeVisible();

    await page.goto('/org/subscription/');
    await expect(page.locator('#current-plan')).toBeVisible();
  });
});