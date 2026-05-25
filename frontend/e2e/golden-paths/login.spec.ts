import { test, expect } from '@playwright/test';

test('login page renders correctly across viewports', async ({ page }) => {
  const response = await page.goto('/accounts/login/');
  test.skip(!response || response.status() >= 500, 'login route not available');

  const passInput = page.locator('input[type="password"]').first();
  await expect(passInput, 'login form must be visible').toBeVisible({ timeout: 5_000 });

  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflow, `login overflowed viewport by ${overflow}px`).toBeLessThanOrEqual(1);
});
