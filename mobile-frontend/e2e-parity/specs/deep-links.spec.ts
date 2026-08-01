import { test, expect } from '@playwright/test';
import {
  ARRANGE_ROLE,
  artifact,
  authStateFile,
  createRepo,
  deleteRepo,
  fetchToken,
  mkdir,
} from '../helpers/parity-helpers';

// "Open everything" — the deep/dynamic routes that live OUTSIDE the static
// top-level pages (file browser, subfolders, trash, group detail) and used to
// dead-end to the redirect shell. This proves the SPA app-shell (404.astro →
// AppRouter) actually opens each one. Runs on all viewport projects.

test.use({ storageState: authStateFile(ARRANGE_ROLE) });

test.describe('Deep links open (dynamic routes)', () => {
  test('opening a library renders the file browser', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('deep'));
    try {
      await page.goto(`/libraries/${repoId}/`);
      await expect(page.getByRole('button', { name: 'Root' })).toBeVisible();
      await expect(page).toHaveURL(new RegExp(repoId)); // did NOT redirect to the list
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('opening a subfolder deep-links into that path', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('deep'));
    try {
      await mkdir(request, token, repoId, '/sub');
      await page.goto(`/libraries/${repoId}/sub`);
      await expect(page.getByRole('button', { name: 'sub' })).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('opening trash renders the trash page', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('deep'));
    try {
      await page.goto(`/libraries/${repoId}/trash`);
      await expect(page.getByTestId('trash-page')).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('opening a group route renders group detail (not a redirect)', async ({ page }) => {
    await page.goto('/groups/999999/');
    await expect(page).toHaveURL(/\/groups\/999999/); // did NOT redirect to the list
    // GroupDetail mounted: with a real group it shows the name + libraries; with
    // this synthetic id it surfaces its own error alert. Either proves the group
    // screen opened rather than the redirect shell / library list.
    await expect(page.getByRole('alert')).toBeVisible();
  });

  test('library header links open history and trash', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('deep'));
    try {
      await page.goto(`/libraries/${repoId}/`);
      await expect(page.getByRole('button', { name: 'Root' })).toBeVisible();

      await page.getByTestId('library-history-link').click();
      await page.waitForURL(/\/history/);
      await expect(page.getByTestId('history-page')).toBeVisible();

      await page.goto(`/libraries/${repoId}/`);
      await page.getByTestId('library-trash-link').click();
      await page.waitForURL(/\/trash/);
      await expect(page.getByTestId('trash-page')).toBeVisible();

      await page.goto(`/libraries/${repoId}/`);
      await page.getByTestId('library-tags-link').click();
      await page.waitForURL(/\/tags/);
      await expect(page.getByTestId('tags-page')).toBeVisible();

      await page.goto(`/libraries/${repoId}/`);
      await page.getByTestId('library-permissions-link').click();
      await page.waitForURL(/\/permissions/);
      await expect(page.getByTestId('perms-page')).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('an unknown route shows the in-app Not Found', async ({ page }) => {
    await page.goto('/this/route/does/not/exist');
    await expect(page.getByTestId('not-found')).toBeVisible();
  });
});
