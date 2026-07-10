import { test, expect } from '@playwright/test';
import { BASE_URL, waitForAppShell } from '../helpers/parity-helpers';

// PWA behaviors: manifest validity, service-worker registration, the Android
// install prompt, and iOS install guidance. Runs on all viewport projects.

test.describe('PWA', () => {
  test('manifest is served and installable-shaped', async ({ request }) => {
    const res = await request.get(`${BASE_URL}/manifest.json`);
    expect(res.ok()).toBeTruthy();
    const m = await res.json();
    expect(m.name).toBeTruthy();
    expect(m.start_url).toBeTruthy();
    expect(m.display).toBe('standalone');
    expect(m.display_override).toContain('standalone');
    // At least one 192 + one 512 icon (installability requirement).
    const sizes = (m.icons ?? []).map((i: { sizes: string }) => i.sizes);
    expect(sizes).toContain('192x192');
    expect(sizes).toContain('512x512');
    // App shortcuts present.
    expect(Array.isArray(m.shortcuts) && m.shortcuts.length).toBeGreaterThanOrEqual(3);
  });

  test('service worker registers', async ({ page }) => {
    await page.goto('/libraries/');
    await waitForAppShell(page);
    // Poll for a registration (ready can be slow when many contexts register
    // against the same origin in parallel).
    await expect
      .poll(
        () =>
          page.evaluate(async () => {
            if (!('serviceWorker' in navigator)) return false;
            const reg = await navigator.serviceWorker.getRegistration();
            return !!reg && !!(reg.active || reg.installing || reg.waiting);
          }),
        { timeout: 20_000 },
      )
      .toBe(true);
  });

  test.describe('Android install prompt', () => {
    test.use({
      userAgent:
        'Mozilla/5.0 (Linux; Android 13; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36',
    });
    test('banner appears and Install triggers prompt()', async ({ page }) => {
    await page.goto('/libraries/');
    await waitForAppShell(page);

    // Simulate the browser firing beforeinstallprompt (headless never does).
    // Re-dispatch until the island's listener is hydrated and the banner shows.
    const banner = page.getByTestId('pwa-install-banner');
    await expect(async () => {
      await page.evaluate(() => {
        const e: any = new Event('beforeinstallprompt');
        (window as any).__promptCalled = false;
        e.prompt = () => {
          (window as any).__promptCalled = true;
          return Promise.resolve();
        };
        e.userChoice = Promise.resolve({ outcome: 'accepted' });
        window.dispatchEvent(e);
      });
      await expect(banner).toBeVisible({ timeout: 1000 });
    }).toPass({ timeout: 10_000 });
    await page.getByTestId('pwa-install-accept').click();
    expect(await page.evaluate(() => (window as any).__promptCalled)).toBe(true);
    await expect(banner).toBeHidden();
    });
  });
});

test.describe('PWA iOS guidance', () => {
  test.use({
    userAgent:
      'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1',
  });

  test('shows Add-to-Home-Screen guidance on iOS Safari', async ({ page }) => {
    await page.goto('/libraries/');
    await waitForAppShell(page);
    await expect(page.getByTestId('pwa-ios-guidance')).toBeVisible();
  });
});
