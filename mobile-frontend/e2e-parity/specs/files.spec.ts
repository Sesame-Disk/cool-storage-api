import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import {
  ARRANGE_ROLE,
  API_URL,
  artifact,
  authHeaders,
  authStateFile,
  createFile,
  createRepo,
  deleteRepo,
  fetchToken,
  listDir,
  mkdir,
} from '../helpers/parity-helpers';

// File operations (create folder / rename / delete / star) — full lifecycles
// against the live stack. Arrange a base library + files via the API as the
// unlimited-quota superadmin (so 6 parallel viewport projects don't hit the
// 3-library cap), then drive the mobile FileBrowser UI and verify both the UI
// listing and the backend state.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

// The iOS Safari device presets surface a PWA "Add to Home Screen" banner
// anchored to the bottom of the screen; it can overlap bottom-anchored bottom
// sheets. Dismiss it (if present) before interacting with sheet controls.
async function dismissPwaBanner(page: Page) {
  const banner = page.getByTestId('pwa-ios-guidance');
  if (await banner.isVisible().catch(() => false)) {
    // The banner has its own dismiss control; fall back to Escape.
    const close = banner.getByRole('button').first();
    if (await close.isVisible().catch(() => false)) {
      await close.click().catch(() => {});
    } else {
      await page.keyboard.press('Escape').catch(() => {});
    }
    await banner.waitFor({ state: 'hidden', timeout: 5_000 }).catch(() => {});
  }
}

/** Wait for the file browser to finish its initial load (breadcrumb Root button). */
async function waitForBrowser(page: Page) {
  await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
}

/** Open the per-item context menu (BottomSheet) via the row's More button. */
async function openContextMenu(page: Page, name: string) {
  await page.getByRole('button', { name: `More options for ${name}` }).click();
  // The context menu is a BottomSheet — wait for a known item to render.
  await expect(page.getByTestId('context-menu-rename')).toBeVisible();
}

test.describe('File operations', () => {
  test('create a folder via the UI shows it in the listing', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('files'));
    const folderName = artifact('files', 'folder');
    try {
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);

      // Open the FAB menu and pick "New Folder".
      await page.getByTestId('upload-fab').click();
      await page.getByTestId('new-folder-btn').click();

      // Fill the New Folder dialog (a centered modal, not a bottom sheet).
      await expect(page.getByTestId('new-folder-dialog')).toBeVisible();
      await page.getByTestId('folder-name-input').fill(folderName);
      await page.getByTestId('create-folder-btn').click();

      // It appears in the UI listing...
      await expect(page.locator(`[data-testid="file-item"][data-name="${folderName}"]`)).toBeVisible();

      // ...and really exists in the backend.
      await expect
        .poll(async () => JSON.stringify(await listDir(request, token, repoId)))
        .toContain(folderName);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('rename a file via the context menu updates the listing', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('files'));
    const oldName = 'before.txt';
    const newName = `after-${Date.now().toString(36)}.txt`;
    try {
      await createFile(request, token, repoId, `/${oldName}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await expect(page.locator(`[data-testid="file-item"][data-name="${oldName}"]`)).toBeVisible();

      await openContextMenu(page, oldName);
      await page.getByTestId('context-menu-rename').click();

      // Rename dialog (BottomSheet): input is pre-filled with the current name.
      const input = page.getByTestId('rename-input');
      await expect(input).toBeVisible();
      await input.fill(newName);
      await page.getByTestId('rename-submit').click();

      // New name shows, old name gone (in the UI)...
      await expect(page.locator(`[data-testid="file-item"][data-name="${newName}"]`)).toBeVisible();
      await expect(page.locator(`[data-testid="file-item"][data-name="${oldName}"]`)).toBeHidden();

      // ...and the backend reflects the rename.
      await expect
        .poll(async () => (await listDir(request, token, repoId)).map((e: any) => e.name))
        .toContain(newName);
      await expect
        .poll(async () => (await listDir(request, token, repoId)).map((e: any) => e.name))
        .not.toContain(oldName);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('delete a file via the UI removes it and sends it to trash', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('files'));
    const name = `delete-me-${Date.now().toString(36)}.txt`;
    try {
      await createFile(request, token, repoId, `/${name}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await expect(page.locator(`[data-testid="file-item"][data-name="${name}"]`)).toBeVisible();

      await openContextMenu(page, name);
      await page.getByTestId('context-menu-delete').click();

      // Delete confirmation (BottomSheet).
      await expect(page.getByText(`Delete "${name}"?`)).toBeVisible();
      await page.getByTestId('delete-confirm').click();

      // Gone from the UI listing...
      await expect(page.locator(`[data-testid="file-item"][data-name="${name}"]`)).toBeHidden();

      // ...gone from the backend directory...
      await expect
        .poll(async () => (await listDir(request, token, repoId)).map((e: any) => e.name))
        .not.toContain(name);

      // ...and it landed in the trash.
      await expect
        .poll(async () => {
          const res = await request.get(`${API_URL}/api/v2.1/repos/${repoId}/trash/`, {
            headers: authHeaders(token),
          });
          if (!res.ok()) return '[]';
          const data = await res.json();
          return JSON.stringify(data.data ?? []);
        })
        .toContain(name);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('star a file via the UI marks it starred (in the listing and via the Starred page)', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('files'));
    const name = `star-me-${Date.now().toString(36)}.txt`;
    try {
      await createFile(request, token, repoId, `/${name}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await expect(page.locator(`[data-testid="file-item"][data-name="${name}"]`)).toBeVisible();

      // Star via the context menu.
      await openContextMenu(page, name);
      await page.getByTestId('context-menu-star').click();

      // The listing refreshes; the row now shows a star icon (fill-yellow-500).
      const row = page.locator(`[data-testid="file-item"][data-name="${name}"]`);
      await expect(row.locator('svg.fill-yellow-500')).toBeVisible();

      // Backend confirms it is starred: it appears on the Starred page.
      await page.goto('/starred/');
      await expect(page.getByText(name, { exact: true }).first()).toBeVisible();

      // API cross-check: the starred-files endpoint lists it too.
      await expect
        .poll(async () => {
          const res = await request.get(`${API_URL}/api2/starredfiles/`, {
            headers: authHeaders(token),
          });
          if (!res.ok()) return '[]';
          const data = await res.json();
          const list = Array.isArray(data) ? data : (data?.starred_item_list ?? []);
          return JSON.stringify(list);
        })
        .toContain(name);

      // Unstar via the context menu, back in the browser.
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await openContextMenu(page, name);
      // The label flips to "Unstar", but the testid is stable.
      await page.getByTestId('context-menu-star').click();

      await expect(row.locator('svg.fill-yellow-500')).toBeHidden();
      await expect
        .poll(async () => {
          const res = await request.get(`${API_URL}/api2/starredfiles/`, {
            headers: authHeaders(token),
          });
          if (!res.ok()) return '[]';
          const data = await res.json();
          const list = Array.isArray(data) ? data : (data?.starred_item_list ?? []);
          return JSON.stringify(list);
        })
        .not.toContain(name);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});

test.describe('Upload / move / copy', () => {
  test('upload a small file via the FAB shows it in the listing and backend', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('upload'));
    const name = `upload-${Date.now().toString(36)}.txt`;
    try {
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);

      // Feed the always-present hidden <input type=file> directly (avoids the
      // native file-chooser dialog, which is flaky on short/landscape viewports).
      await page.getByTestId('file-input').setInputFiles({
        name,
        mimeType: 'text/plain',
        buffer: Buffer.from('hello world'),
      });

      // The uploaded file appears in the UI listing (dir refresh after complete)...
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${name}"]`),
      ).toBeVisible({ timeout: 15_000 });

      // ...and really exists in the backend directory.
      await expect
        .poll(async () => (await listDir(request, token, repoId)).map((e: any) => e.name))
        .toContain(name);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('move a file into a subfolder via the context menu + FolderPicker', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('move'));
    const fileName = `move-me-${Date.now().toString(36)}.txt`;
    const subfolder = `dest-${Date.now().toString(36)}`;
    try {
      await createFile(request, token, repoId, `/${fileName}`);
      await mkdir(request, token, repoId, `/${subfolder}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${fileName}"]`),
      ).toBeVisible();

      // Context menu → Move → pick the subfolder in the FolderPicker → confirm.
      await openContextMenu(page, fileName);
      await page.getByTestId('context-menu-move').click();

      await page.locator(`[data-testid="folder-picker-repo"][data-repo-id="${repoId}"]`).click();
      await page.locator(`[data-testid="folder-picker-dir"][data-name="${subfolder}"]`).click();
      await page.getByTestId('folder-picker-confirm').click();

      // Gone from the root UI listing.
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${fileName}"]`),
      ).toBeHidden({ timeout: 15_000 });

      // Backend: absent from root, present in the subfolder.
      await expect
        .poll(async () => (await listDir(request, token, repoId, '/')).map((e: any) => e.name))
        .not.toContain(fileName);
      await expect
        .poll(async () => (await listDir(request, token, repoId, `/${subfolder}`)).map((e: any) => e.name))
        .toContain(fileName);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('copy a file into a subfolder leaves it in both locations', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('copy'));
    const fileName = `copy-me-${Date.now().toString(36)}.txt`;
    const subfolder = `dest-${Date.now().toString(36)}`;
    try {
      await createFile(request, token, repoId, `/${fileName}`);
      await mkdir(request, token, repoId, `/${subfolder}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${fileName}"]`),
      ).toBeVisible();

      await openContextMenu(page, fileName);
      await page.getByTestId('context-menu-copy').click();

      await page.locator(`[data-testid="folder-picker-repo"][data-repo-id="${repoId}"]`).click();
      await page.locator(`[data-testid="folder-picker-dir"][data-name="${subfolder}"]`).click();
      await page.getByTestId('folder-picker-confirm').click();

      // Backend: present in BOTH the root and the subfolder.
      await expect
        .poll(async () => (await listDir(request, token, repoId, `/${subfolder}`)).map((e: any) => e.name))
        .toContain(fileName);
      await expect
        .poll(async () => (await listDir(request, token, repoId, '/')).map((e: any) => e.name))
        .toContain(fileName);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('upload a large file uses the block-upload flow and lands with the right size', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('blockupload'));
    const name = `big-${Date.now().toString(36)}.bin`;
    // ~1.5 MiB: above the app's 1 MiB block-upload threshold (exercises the
    // session → check → upload block → commit path) but small enough to be fast.
    const size = Math.floor(1.5 * 1024 * 1024);
    const buffer = Buffer.alloc(size);
    for (let i = 0; i < size; i++) buffer[i] = (i * 31 + 7) & 0xff; // non-uniform bytes
    try {
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);

      await page.getByTestId('file-input').setInputFiles({
        name,
        mimeType: 'application/octet-stream',
        buffer,
      });

      // Appears in the UI listing once the block commit completes.
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${name}"]`),
      ).toBeVisible({ timeout: 30_000 });

      // Backend lists it with the exact byte size we uploaded.
      await expect
        .poll(async () => {
          const entry = (await listDir(request, token, repoId)).find((e: any) => e.name === name);
          return entry ? entry.size : -1;
        }, { timeout: 30_000 })
        .toBe(size);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
