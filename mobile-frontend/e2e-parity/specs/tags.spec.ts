import { test, expect } from '@playwright/test';
import {
  API_URL,
  ARRANGE_ROLE,
  artifact,
  authHeaders,
  authStateFile,
  createRepo,
  deleteRepo,
  fetchToken,
  unique,
} from '../helpers/parity-helpers';

// Library tags (E3). Full lifecycle against the live stack: create a library
// via API, then drive the mobile Tags UI to add a uniquely-named tag, assert it
// shows in the list AND in the backend repo-tags API, then delete it via the UI
// (confirm) and assert it's gone from both.
//
// Arrange as the unlimited-quota superadmin so 6 parallel viewport projects
// don't hit the 3-library cap; drive the UI in that same session.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

async function apiListTagNames(
  request: import('@playwright/test').APIRequestContext,
  token: string,
  repoId: string,
): Promise<string[]> {
  const res = await request.get(`${API_URL}/api/v2.1/repos/${repoId}/repo-tags/`, {
    headers: authHeaders(token),
  });
  expect(res.ok(), `list repo-tags: ${res.status()}`).toBeTruthy();
  const data = await res.json();
  return (data.repo_tags ?? []).map((t: { tag_name: string }) => t.tag_name);
}

test.describe('Library tags', () => {
  test('add a tag via UI then delete it', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('tags'));
    const tagName = unique('tag');
    try {
      await page.goto(`/libraries/${repoId}/tags`);
      await expect(page.getByTestId('tags-page')).toBeVisible();

      // Add a uniquely-named tag through the form.
      await page.getByTestId('tag-add-name').fill(tagName);
      await page.getByTestId('tag-add-submit').click();

      // Appears in the UI list...
      await expect(page.getByTestId('tags-list').getByText(tagName)).toBeVisible();

      // ...and in the backend.
      await expect
        .poll(async () => await apiListTagNames(request, token, repoId))
        .toContain(tagName);

      // Delete it via the UI (behind the centered confirm modal).
      await page.getByTestId('tag-delete').click();
      await expect(page.getByTestId('tag-delete-confirm')).toBeVisible();
      await page.getByTestId('tag-delete-confirm-yes').click();

      // Gone from the UI...
      await expect(page.getByTestId('tags-list').getByText(tagName)).toBeHidden();

      // ...and from the backend.
      await expect
        .poll(async () => await apiListTagNames(request, token, repoId))
        .not.toContain(tagName);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
