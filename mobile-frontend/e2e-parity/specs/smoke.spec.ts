import { test, expect } from '@playwright/test';
import { TABS, waitForAppShell } from '../helpers/parity-helpers';

// Foundation smoke: proves the seeded live-stack session works and the app
// shell renders + navigates on every viewport project. If this is green, the
// harness (auth, base URL, viewports) is sound and feature specs can build on it.

test.describe('parity foundation smoke', () => {
  test('authenticated session lands in the app (not /login)', async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveURL(/\/libraries/, { timeout: 15_000 });
    await waitForAppShell(page);
  });

  test('bottom nav exposes all 5 tabs', async ({ page }) => {
    await page.goto('/libraries/');
    await waitForAppShell(page);
    const nav = page.locator('nav').first();
    for (const tab of TABS) {
      await expect(nav.locator(`a[href*="${tab}"]`)).toHaveCount(1);
    }
  });

  test('tab navigation routes correctly', async ({ page }) => {
    await page.goto('/libraries/');
    await waitForAppShell(page);
    const nav = page.locator('nav').first();
    const go = async (tab: string, re: RegExp) => {
      const link = nav.locator(`a[href*="${tab}"]`).first();
      await link.click();
      // Generous timeout: the Astro dev server compiles each route on first hit,
      // which is slow under parallel load (the container serves static instantly).
      await page.waitForURL(re, { timeout: 30_000 });
      await page.locator('nav').first().waitFor({ state: 'visible', timeout: 30_000 });
    };
    await go('starred', /starred/);
    await go('groups', /groups/);
    await go('libraries', /libraries/);
  });

  test('brand logo renders in the app header (same mechanism as web)', async ({ page }) => {
    await page.goto('/libraries/');
    await waitForAppShell(page);
    // The config-driven <Logo> (ported from the web frontend) renders in the
    // TopBar and links home — brand parity with the web app shell.
    const logo = page.getByTestId('app-logo').first();
    await expect(logo).toBeVisible();
    // The image actually loaded (non-zero intrinsic size), not a broken img.
    await expect
      .poll(async () => logo.evaluate((img: HTMLImageElement) => img.naturalWidth))
      .toBeGreaterThan(0);
  });

  test('core pages load without critical JS errors', async ({ page }) => {
    const errors: string[] = [];
    let current = '';
    page.on('pageerror', (err) => errors.push(`${current}: ${err.message}`));
    for (const path of ['/libraries/', '/shared/', '/groups/', '/starred/', '/more/']) {
      current = path;
      await page.goto(path);
      await page.waitForLoadState('networkidle');
    }
    const critical = errors.filter(
      (e) => !e.includes('ResizeObserver') && !e.includes('ServiceWorker'),
    );
    expect(critical, `critical errors: ${critical.join(' | ')}`).toHaveLength(0);
  });
});
