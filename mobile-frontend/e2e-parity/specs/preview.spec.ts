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
} from '../helpers/parity-helpers';

// File-preview parity (E6): the mobile app previews images, video, text, pdf and
// code; this spec covers the newly-added AUDIO and MARKDOWN viewers. Arrange a
// library + an .mp3 and a .md file via the API as the unlimited-quota superadmin
// (so the 6 parallel viewport projects don't hit the 3-library cap), then drive
// the real FileBrowser: tap the file row → FilePreview opens → assert the right
// viewer testid renders. Empty files are fine: the viewer is chosen purely from
// the file extension, so structure (which viewer mounts) is what we verify.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

/** Wait for the file browser to finish its initial load (breadcrumb Root button). */
async function waitForBrowser(page: Page) {
  await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
}

/** Tap a file row in the FileBrowser to open its preview. */
async function openFile(page: Page, name: string) {
  const row = page.locator(`[data-testid="file-item"][data-name="${name}"]`);
  await expect(row).toBeVisible();
  await row.click();
}

test.describe('File preview (audio + markdown)', () => {
  test('tapping an audio file opens the AudioPlayer', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('preview'));
    const name = `sound-${Date.now().toString(36)}.mp3`;
    try {
      await createFile(request, token, repoId, `/${name}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await openFile(page, name);

      // The audio viewer mounts with its <audio controls> element.
      await expect(page.getByTestId('audio-player')).toBeVisible();
      await expect(page.getByTestId('audio-element')).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('tapping a markdown file opens the MarkdownViewer', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('preview'));
    const name = `notes-${Date.now().toString(36)}.md`;
    try {
      await createFile(request, token, repoId, `/${name}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await openFile(page, name);

      // The markdown viewer is selected for .md files (content is empty here
      // since createFile makes an empty file — routing is what we verify).
      await expect(page.getByTestId('markdown-viewer')).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
