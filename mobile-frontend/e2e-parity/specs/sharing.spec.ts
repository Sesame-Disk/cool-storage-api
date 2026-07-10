import { test, expect } from '@playwright/test';
import type { APIRequestContext } from '@playwright/test';
import {
  API_URL,
  ARRANGE_ROLE,
  artifact,
  authHeaders,
  authStateFile,
  createFile,
  createRepo,
  deleteRepo,
  fetchToken,
} from '../helpers/parity-helpers';

// Sharing (share links + upload links). Full lifecycles against the live stack:
// arrange links via the SAME endpoints the mobile app's api.ts uses, then drive
// the mobile "My Shares" (ShareAdmin) UI to list + delete them; plus a real
// share-link CREATE through the FileBrowser share sheet. Runs on all viewport
// projects.
//
// Arrange as the unlimited-quota superadmin so the 6 parallel viewport projects
// don't hit the 3-library cap; drive the UI in that same session.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

// --------------------------------------------------------------------------
// Link arrangement helpers — replicate the exact requests the mobile app makes
// (src/lib/api.ts: createShareLink / createUploadLink-equivalent). We can't call
// the app's api.ts from a spec, so we POST to the same endpoints directly.
// --------------------------------------------------------------------------

/** Mirrors api.ts createShareLink: POST /api/v2.1/share-links/ {repo_id, path}. */
async function createShareLinkApi(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
): Promise<{ token: string; link: string }> {
  const res = await request.post(`${API_URL}/api/v2.1/share-links/`, {
    headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
    data: { repo_id: repoId, path },
  });
  expect(res.ok(), `createShareLink ${path}: ${res.status()} ${await res.text()}`).toBeTruthy();
  return res.json();
}

/** POST /api/v2.1/upload-links/ {repo_id, path} — same endpoint ShareAdmin lists. */
async function createUploadLinkApi(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
): Promise<{ token: string }> {
  const res = await request.post(`${API_URL}/api/v2.1/upload-links/`, {
    headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
    data: { repo_id: repoId, path },
  });
  expect(res.ok(), `createUploadLink ${path}: ${res.status()} ${await res.text()}`).toBeTruthy();
  return res.json();
}

async function listShareLinkTokens(
  request: APIRequestContext,
  token: string,
): Promise<string[]> {
  const res = await request.get(`${API_URL}/api/v2.1/share-links/`, {
    headers: authHeaders(token),
  });
  if (!res.ok()) return [];
  const data = await res.json();
  return (Array.isArray(data) ? data : []).map((l: { token: string }) => l.token);
}

async function listUploadLinkTokens(
  request: APIRequestContext,
  token: string,
): Promise<string[]> {
  const res = await request.get(`${API_URL}/api/v2.1/upload-links/`, {
    headers: authHeaders(token),
  });
  if (!res.ok()) return [];
  const data = await res.json();
  return (Array.isArray(data) ? data : []).map((l: { token: string }) => l.token);
}

test.describe('Sharing (My Shares)', () => {
  test('share link created via API lists in ShareAdmin and deletes via UI', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('sharing'));
    const fileName = artifact('sharing', 'file') + '.txt';
    const filePath = `/${fileName}`;
    let linkToken = '';
    try {
      await createFile(request, token, repoId, filePath);
      ({ token: linkToken } = await createShareLinkApi(request, token, repoId, filePath));

      await page.goto('/share-admin/');
      await expect(page.getByTestId('share-admin-page')).toBeVisible();

      // Share tab is the default; the arranged link shows up in the list.
      const item = page.locator(`[data-testid="share-link-item"][data-token="${linkToken}"]`);
      await expect(item).toBeVisible();
      await expect(item.getByText(fileName, { exact: true })).toBeVisible();

      // Delete via the UI: inline delete -> confirm dialog -> confirm.
      await item.getByRole('button', { name: `Delete share link ${fileName}` }).click();
      await expect(page.getByTestId('share-admin-delete-dialog')).toBeVisible();
      await page.getByTestId('share-admin-delete-confirm').click();

      await expect(item).toBeHidden();

      // It is really gone from the backend.
      await expect
        .poll(async () => (await listShareLinkTokens(request, token)).includes(linkToken))
        .toBe(false);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('upload link created via API lists in ShareAdmin and deletes via UI', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('sharing'));
    let linkToken = '';
    try {
      // Upload links target a directory; use the library root.
      ({ token: linkToken } = await createUploadLinkApi(request, token, repoId, '/'));

      await page.goto('/share-admin/');
      await expect(page.getByTestId('share-admin-page')).toBeVisible();

      // Switch to the Upload Links tab.
      await page.getByTestId('share-admin-tab-upload').click();

      const item = page.locator(`[data-testid="upload-link-item"][data-token="${linkToken}"]`);
      await expect(item).toBeVisible();

      // Delete via the UI: inline delete -> confirm dialog -> confirm.
      await item.getByRole('button', { name: /Delete upload link/ }).click();
      await expect(page.getByTestId('share-admin-delete-dialog')).toBeVisible();
      await page.getByTestId('share-admin-delete-confirm').click();

      await expect(item).toBeHidden();

      await expect
        .poll(async () => (await listUploadLinkTokens(request, token)).includes(linkToken))
        .toBe(false);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('share link can be created from the file browser share sheet', async ({
    page,
    request,
  }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('sharing'));
    const fileName = artifact('sharing', 'file') + '.txt';
    try {
      await createFile(request, token, repoId, `/${fileName}`);

      await page.goto(`/libraries/${repoId}/`);
      const fileItem = page.getByTestId('file-item').filter({ hasText: fileName });
      await expect(fileItem).toBeVisible();

      // Open the context menu for the file, then choose Share.
      await page.getByRole('button', { name: `More options for ${fileName}` }).click();
      await page.getByTestId('context-menu-share').click();

      // ShareSheet opens on the Share Link tab by default; generate the link.
      await expect(page.getByTestId('tab-share-link')).toBeVisible();
      await page.getByTestId('generate-link-btn').click();

      // Success: the generated URL + QR code render.
      await expect(page.getByTestId('share-link-url')).toBeVisible();
      await expect(page.getByTestId('qr-code')).toBeVisible();

      // And the backend now has a share link for this file.
      await expect
        .poll(async () => (await listShareLinkTokens(request, token)).length)
        .toBeGreaterThan(0);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
