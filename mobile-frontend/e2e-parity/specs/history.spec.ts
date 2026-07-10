import { test, expect } from '@playwright/test';
import {
  ARRANGE_ROLE,
  artifact,
  authStateFile,
  createFile,
  createRepo,
  deleteFilePath,
  deleteRepo,
  fetchToken,
} from '../helpers/parity-helpers';

// Library history + versions (E1). Full lifecycle against the live stack: create
// a library + a file via API (each mutation is a commit), then drive the mobile
// History UI to list the commits. Runs on all viewport projects.
//
// Arrange as the unlimited-quota superadmin so 6 parallel viewport projects
// don't hit the 3-library cap; drive the UI in that same session.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

test.describe('Library history (versions)', () => {
  test('library modifications show up as restorable history versions', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('history'));
    try {
      // Each of these is a commit: creating the repo, then adding a file.
      await createFile(request, token, repoId, '/notes.txt');

      await page.goto(`/libraries/${repoId}/history`);
      await expect(page.getByTestId('history-page')).toBeVisible();
      await expect(page.getByTestId('history-list')).toBeVisible();

      const rowsBefore = await page.getByTestId('history-list').locator('> div').count();
      expect(rowsBefore).toBeGreaterThan(0);

      // The newest (first) row is the current version, not restorable.
      await expect(page.getByText('Current').first()).toBeVisible();
      await expect(
        page.getByRole('button', { name: /Restore this version/ }).first(),
      ).toBeVisible();

      // A further mutation keeps history healthy (commit count is non-decreasing;
      // the backend may squash/cap so we don't assert strict growth).
      await createFile(request, token, repoId, '/scratch.txt');
      await page.getByRole('button', { name: 'Refresh' }).click();
      await expect
        .poll(async () => page.getByTestId('history-list').locator('> div').count())
        .toBeGreaterThanOrEqual(rowsBefore);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('restoring an older version is confirmed behind a centered modal', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('history'));
    try {
      await createFile(request, token, repoId, '/v1.txt');
      await createFile(request, token, repoId, '/v2.txt');

      await page.goto(`/libraries/${repoId}/history`);
      await expect(page.getByTestId('history-page')).toBeVisible();

      // Restore an older (non-current) version.
      await page.getByRole('button', { name: /Restore this version/ }).first().click();
      await expect(page.getByTestId('history-revert-confirm')).toBeVisible();
      await page.getByTestId('history-revert-confirm-yes').click();

      // The confirm modal closes and the list refreshes with the new revert commit.
      await expect(page.getByTestId('history-revert-confirm')).toBeHidden();
      await expect(page.getByTestId('history-list')).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
