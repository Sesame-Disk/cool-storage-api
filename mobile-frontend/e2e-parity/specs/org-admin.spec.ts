import { test, expect } from '@playwright/test';
import { authStateFile } from '../helpers/parity-helpers';

// Org-admin section (Phase 3, CORE ONLY). Arrange as role `admin`
// (admin@sesamefs.local) — an org admin whose seeded org already has users,
// groups, repos and links. All screens are read-only or read-mostly; we never
// create/delete org users (org-admin user writes are disabled on the backend).
test.use({ storageState: authStateFile('admin') });

test.describe('Org admin', () => {
  test('home renders with core + desktop nav links', async ({ page }) => {
    await page.goto('/org/');
    await expect(page.getByTestId('org-admin-home')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('org-nav-users')).toHaveAttribute('href', '/org/users/');
    await expect(page.getByTestId('org-nav-groups')).toHaveAttribute('href', '/org/groups/');
    await expect(page.getByTestId('org-nav-libraries')).toHaveAttribute('href', '/org/libraries/');
    await expect(page.getByTestId('org-nav-share-links')).toHaveAttribute('href', '/org/share-links/');
    await expect(page.getByTestId('org-nav-settings')).toHaveAttribute('href', '/org/settings/');
    // A desktop-redirect entry is present too.
    await expect(page.getByTestId('org-nav-statistics')).toHaveAttribute('href', '/org/statistics/');
  });

  test('users page lists at least one seeded user (read-only)', async ({ page }) => {
    await page.goto('/org/users/');
    await expect(page.getByTestId('org-users-page')).toBeVisible({ timeout: 15_000 });
    await expect(page.getByTestId('org-user-item').first()).toBeVisible({ timeout: 15_000 });
    const count = await page.getByTestId('org-user-item').count();
    expect(count).toBeGreaterThanOrEqual(1);
  });

  test('groups page renders', async ({ page }) => {
    await page.goto('/org/groups/');
    await expect(page.getByTestId('org-groups-page')).toBeVisible({ timeout: 15_000 });
  });

  test('libraries page renders', async ({ page }) => {
    await page.goto('/org/libraries/');
    await expect(page.getByTestId('org-libraries-page')).toBeVisible({ timeout: 15_000 });
  });

  test('share-links page renders', async ({ page }) => {
    await page.goto('/org/share-links/');
    await expect(page.getByTestId('org-sharelinks-page')).toBeVisible({ timeout: 15_000 });
  });

  test('settings page renders', async ({ page }) => {
    await page.goto('/org/settings/');
    await expect(page.getByTestId('org-settings-page')).toBeVisible({ timeout: 15_000 });
  });

  test('a desktop-redirect page links back to the desktop /org route', async ({ page }) => {
    await page.goto('/org/statistics/');
    const link = page.getByTestId('open-in-desktop');
    await expect(link).toBeVisible({ timeout: 15_000 });
    const href = await link.getAttribute('href');
    expect(href).toContain('/org/');
  });

  test('More page exposes the Org Admin entry for org admins', async ({ page }) => {
    await page.goto('/more/');
    const link = page.getByTestId('more-link-org-admin');
    await expect(link).toBeVisible({ timeout: 15_000 });
    await expect(link).toHaveAttribute('href', '/org/');
  });
});
