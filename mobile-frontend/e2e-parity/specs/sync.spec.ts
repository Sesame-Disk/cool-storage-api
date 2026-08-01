import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import {
  ARRANGE_ROLE,
  authStateFile,
  artifact,
  createRepo,
  deleteRepo,
  fetchToken,
  dismissPwaBanner,
} from '../helpers/parity-helpers';

// Folder sync is gated on the File System Access API (`showDirectoryPicker`),
// which is absent on iOS Safari / Firefox. The agreed behaviour there is to
// HIDE/DISABLE the feature with a note. `showDirectoryPicker` is a native
// dialog Playwright can't drive, so we don't exercise a real FS round-trip
// here (that's manual on Chrome/Edge/Android) — we lock the platform GATING:
// with the API removed the sync UI must disappear; with it present the entry
// points must appear.

test.use({ storageState: authStateFile(ARRANGE_ROLE) });

const REMOVE_FS_API = `try { delete window.showDirectoryPicker; } catch (e) {}`;
const ADD_FS_API = `window.showDirectoryPicker = () => Promise.resolve({ name: 'mock', kind: 'directory' });`;

/** Long-press a library card by dispatching only pointerdown (no pointerup, so
 * the card's click-to-open never fires) and wait out the 500ms long-press. */
async function longPressLibrary(page: Page, name: string) {
  const card = page.getByRole('button', { name: new RegExp(name) }).first();
  await card.scrollIntoViewIfNeeded();
  await card.dispatchEvent('pointerdown');
  await page.waitForTimeout(650);
  // Context menu (BottomSheet) opened — its base "Open" action proves it.
  await expect(page.getByRole('button', { name: 'Open', exact: true })).toBeVisible();
}

test.describe('Folder sync — platform gating', () => {
  test('sync page shows the unsupported note when the FS Access API is absent', async ({ page }) => {
    await page.addInitScript(REMOVE_FS_API);
    await page.goto('/sync/');
    await expect(page.getByTestId('sync-unsupported')).toBeVisible();
  });

  test('sync page shows the synced-folders view when the FS Access API is present', async ({ page }) => {
    await page.addInitScript(ADD_FS_API);
    await page.goto('/sync/');
    await expect(page.getByTestId('sync-unsupported')).toHaveCount(0);
    await expect(page.getByText('No synced folders')).toBeVisible();
  });

  test('library context menu disables sync when the FS Access API is absent', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const name = artifact('sync');
    const repoId = await createRepo(request, token, name);
    try {
      await page.addInitScript(REMOVE_FS_API);
      await page.goto('/');
      await dismissPwaBanner(page);
      await longPressLibrary(page, name);
      await expect(page.getByTestId('sync-unavailable')).toBeVisible();
      await expect(page.getByTestId('sync-start')).toHaveCount(0);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('library context menu offers "Sync this folder" when the FS Access API is present', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const name = artifact('sync');
    const repoId = await createRepo(request, token, name);
    try {
      await page.addInitScript(ADD_FS_API);
      await page.goto('/');
      await dismissPwaBanner(page);
      await longPressLibrary(page, name);
      await expect(page.getByTestId('sync-start')).toBeVisible();
      await expect(page.getByTestId('sync-unavailable')).toHaveCount(0);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
