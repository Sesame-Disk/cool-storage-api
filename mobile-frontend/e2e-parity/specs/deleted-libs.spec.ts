import { test, expect } from '@playwright/test';
import {
  ARRANGE_ROLE,
  artifact,
  authStateFile,
  createRepo,
  deleteRepo,
  fetchToken,
  listRepos,
} from '../helpers/parity-helpers';

// Deleted libraries / restore (E4). Arrange as the unlimited superadmin.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

test.describe('Deleted libraries', () => {
  test('a deleted library appears and can be restored', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const name = artifact('dellib');
    const repoId = await createRepo(request, token, name);
    let restored = false;
    try {
      await deleteRepo(request, token, repoId); // soft-delete → deleted-repos

      await page.goto('/deleted-libraries/');
      await expect(page.getByTestId('deleted-libs-page')).toBeVisible();
      const row = page.getByTestId('deleted-libs-list').getByText(name);
      await expect(row).toBeVisible();

      await page.getByRole('button', { name: `Restore ${name}` }).click();
      await expect(page.getByText(name)).toBeHidden();

      // Really back among the live libraries.
      await expect
        .poll(async () => (await listRepos(request, token)).some((r) => r.name === name))
        .toBe(true);
      restored = true;
    } finally {
      if (restored) await deleteRepo(request, token, repoId);
    }
  });
});
