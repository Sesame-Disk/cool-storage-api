import { test, expect } from '../auth.fixture';

test('logged-in user reaches a repo / library view', async ({ loggedInPage }) => {
  // After login, navigate to libraries view
  await loggedInPage.goto('/libraries/');
  await expect(loggedInPage).toHaveURL(/\/(libraries|library|home|my-libs)\/?/);

  const overflow = await loggedInPage.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflow, `repo view overflowed by ${overflow}px`).toBeLessThanOrEqual(1);
});
