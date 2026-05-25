import { test, expect } from '../auth.fixture';

test.describe('mobile hamburger menu', () => {
  test('hamburger toggle is visible on mobile and opens the side drawer', async ({
    loggedInPage,
  }, testInfo) => {
    test.skip(!testInfo.project.name.includes('mobile'), 'mobile-only test');

    const toggle = loggedInPage.locator('.side-nav-toggle').first();
    await expect(toggle, 'hamburger must be visible at mobile width').toBeVisible();

    const box = await toggle.boundingBox();
    expect(box, 'hamburger should have a bounding box').not.toBeNull();
    expect(box!.width).toBeGreaterThanOrEqual(44);
    expect(box!.height).toBeGreaterThanOrEqual(44);

    await expect(toggle).toHaveAttribute('aria-label', /menu/i);

    // Open the drawer: side-panel.js applies `.left-zero` when open.
    await toggle.click();
    const panel = loggedInPage.locator('.side-panel');
    await expect(panel).toHaveClass(/left-zero/);

    // Backdrop closes the drawer
    await loggedInPage.locator('.side-panel-backdrop.show').click();
    await expect(panel).not.toHaveClass(/left-zero/);
  });

  test('hamburger toggle is hidden on desktop', async ({ loggedInPage }, testInfo) => {
    test.skip(!testInfo.project.name.includes('desktop'), 'desktop-only test');
    const toggle = loggedInPage.locator('.side-nav-toggle').first();
    await expect(toggle).toBeHidden();
  });
});
