import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import {
  ARRANGE_ROLE,
  artifact,
  authStateFile,
  createFile,
  createRepo,
  deleteRepo,
  fetchToken,
  unique,
} from '../helpers/parity-helpers';

// Advanced search (E9). Arrange a library + a uniquely-named file via API as the
// unlimited-quota superadmin, then drive the mobile /search/ UI: full-text
// search, a files-only filter (file still shows), and a folders-only filter
// (the file is excluded). Runs on all viewport projects.
//
// The Go backend's /api/v2.1/search/ is a live case-insensitive CONTAINS query
// over the fs_objects table — indexing is synchronous, so a freshly created
// file is searchable immediately. We still poll the UI with a generous timeout
// to stay resilient to any propagation lag under parallel load.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

/** Type the token and wait (polling) for it to appear in the results list. */
async function searchFor(page: Page, token: string) {
  const input = page.getByTestId('search-input');
  await input.fill('');
  await input.fill(token);
  await input.press('Enter'); // the submit button is sr-only; Enter submits the form
}

test.describe('Search (advanced filters)', () => {
  test('finds a file and honours the file-type filter', async ({ page, request }) => {
    const authToken = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, authToken, artifact('search'));
    // Unique token embedded in the file name so the search is unambiguous.
    const token = unique('needle');
    const fileName = `${token}.txt`;
    try {
      await createFile(request, authToken, repoId, `/${fileName}`);

      await page.goto('/search/');
      await expect(page.getByTestId('search-page')).toBeVisible();

      // --- Full-text search: the file appears in the results list. ---
      const resultItem = page.getByTestId('search-result-item').filter({ hasText: token });
      await expect(async () => {
        await searchFor(page, token);
        await expect(resultItem.first()).toBeVisible({ timeout: 5_000 });
      }).toPass({ timeout: 30_000 });

      // --- Filter: Files-only. The file is a file, so it must still show. ---
      await page.getByTestId('search-filter-toggle').click();
      await expect(page.getByTestId('search-filters')).toBeVisible();
      await page.getByTestId('filter-type-file').click();
      await expect(async () => {
        await searchFor(page, token);
        await expect(resultItem.first()).toBeVisible({ timeout: 5_000 });
      }).toPass({ timeout: 20_000 });

      // --- Filter: Folders-only. A file must be EXCLUDED. ---
      await page.getByTestId('filter-type-dir').click();
      await expect(async () => {
        await expect(
          page.getByTestId('search-result-item').filter({ hasText: fileName }),
        ).toHaveCount(0, { timeout: 5_000 });
      }).toPass({ timeout: 20_000 });
    } finally {
      await deleteRepo(request, authToken, repoId);
    }
  });
});
