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
  listDir,
} from '../helpers/parity-helpers';

// Trash / recycle bin (E1). Full lifecycle against the live stack: create a
// library + file via API, delete the file, then drive the mobile Trash UI to
// list, restore, and empty. Runs on all viewport projects.
//
// Arrange as the unlimited-quota superadmin so 6 parallel viewport projects
// don't hit the 3-library cap; drive the UI in that same session.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

test.describe('Trash (recycle bin)', () => {
  test('deleted file appears in trash and can be restored', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('trash'));
    try {
      await createFile(request, token, repoId, '/doomed.txt');
      await deleteFilePath(request, token, repoId, '/doomed.txt');

      await page.goto(`/libraries/${repoId}/trash`);
      await expect(page.getByTestId('trash-page')).toBeVisible();
      await expect(page.getByTestId('trash-list').getByText('doomed.txt')).toBeVisible();

      await page.getByRole('button', { name: 'Restore doomed.txt' }).click();
      await expect(page.getByText('doomed.txt')).toBeHidden();

      // It is really back in the library root.
      await expect
        .poll(async () => JSON.stringify(await listDir(request, token, repoId)))
        .toContain('doomed.txt');
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('empty trash removes all items', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('trash'));
    try {
      await createFile(request, token, repoId, '/gone.txt');
      await deleteFilePath(request, token, repoId, '/gone.txt');

      await page.goto(`/libraries/${repoId}/trash`);
      await expect(page.getByText('gone.txt')).toBeVisible();

      await page.getByTestId('trash-clean').click();
      await page.getByTestId('trash-clean-confirm-yes').click();

      await expect(page.getByText('Trash is empty')).toBeVisible();
      await expect(page.getByTestId('trash-list')).toBeHidden();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
