import { test, expect } from '@playwright/test';

test.describe('smoke', () => {
  test('home page loads without horizontal overflow', async ({ page }) => {
    await page.goto('/');
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    );
    expect(overflow, `document overflowed viewport by ${overflow}px`).toBeLessThanOrEqual(1);
  });

  test('login page renders without horizontal overflow', async ({ page }) => {
    const response = await page.goto('/accounts/login/');
    expect(response?.status(), 'login route should respond').toBeLessThan(500);
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    );
    expect(overflow, `login overflowed viewport by ${overflow}px`).toBeLessThanOrEqual(1);
  });
});
