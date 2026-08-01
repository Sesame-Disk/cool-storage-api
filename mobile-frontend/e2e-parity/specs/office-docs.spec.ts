import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import {
  API_URL,
  ARRANGE_ROLE,
  CREDENTIALS,
  artifact,
  authHeaders,
  authStateFile,
  createFile,
  createRepo,
  deleteRepo,
  dismissPwaBanner,
  fetchToken,
} from '../helpers/parity-helpers';

// Office documents: create a new .docx/.xlsx/.pptx from the app (the backend
// seeds a valid template on create, so it opens straight into the OnlyOffice
// editor), and confirm a shared office document opens for BOTH the sharing user
// and the receiver.

async function waitForBrowser(page: Page) {
  await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
}

/** Fetch the OnlyOffice editor config for a doc as a given token (receiver check). */
async function ooConfig(request: any, token: string, repoId: string, path: string) {
  const res = await request.get(
    `${API_URL}/api/v2.1/repos/${repoId}/onlyoffice/?p=${encodeURIComponent(path)}`,
    { headers: authHeaders(token) },
  );
  return res;
}

test.describe('Create office documents', () => {
  test.use({ storageState: authStateFile(ARRANGE_ROLE) });

  for (const { ext, type } of [
    { ext: 'docx', type: 'Word' },
    { ext: 'xlsx', type: 'Excel' },
    { ext: 'pptx', type: 'PowerPoint' },
  ]) {
    test(`create a .${ext} (${type}) and open it in the OnlyOffice editor`, async ({ page, request }) => {
      const token = await fetchToken(request, ARRANGE_ROLE);
      const repoId = await createRepo(request, token, artifact('office'));
      const name = `doc-${Date.now().toString(36)}.${ext}`;
      try {
        await page.goto(`/libraries/${repoId}/`);
        await waitForBrowser(page);
        await dismissPwaBanner(page);

        // FAB → New File → pick the office type → name it → create.
        await page.getByTestId('upload-fab').click();
        await page.getByTestId('new-file-btn').click();
        await expect(page.getByTestId('new-file-dialog')).toBeVisible();
        await page.getByTestId(`type-.${ext}`).click();
        await page.getByTestId('file-name-input').fill(name);
        await page.getByTestId('create-file-btn').click();

        // It shows in the listing...
        const row = page.locator(`[data-testid="file-item"][data-name="${name}"]`);
        await expect(row).toBeVisible({ timeout: 15_000 });

        // ...and tapping it opens the OnlyOffice viewer with a signed editor config.
        const configResponse = page.waitForResponse(
          (r: any) => r.url().includes('/onlyoffice/') && r.request().method() === 'GET',
          { timeout: 15_000 },
        );
        await row.click();
        await expect(page.getByTestId('onlyoffice-viewer')).toBeVisible();
        const res = await configResponse;
        expect(res.status()).toBe(200);
        const body = await res.json();
        expect(body.doc?.documentType).toBeTruthy();
        expect(body.doc?.token, 'editor config is JWT-signed').toBeTruthy();
        // The template is a real, non-empty document.
        const dir = await (
          await request.get(`${API_URL}/api2/repos/${repoId}/dir/?p=/`, { headers: authHeaders(token) })
        ).json();
        const entry = dir.find((e: any) => e.name === name);
        expect(entry?.size, 'created office file is a non-empty template').toBeGreaterThan(0);
      } finally {
        await deleteRepo(request, token, repoId);
      }
    });
  }
});

test.describe('Share an office document (both sides)', () => {
  // Same-org sharer (admin@) + receiver (user@); needs the Default org on the
  // unlimited plan (provision-local-users.mjs sets quota_policy=soft).
  test.use({ storageState: authStateFile('admin') });

  test('a shared office document opens for the owner and the receiver', async ({ page, request }) => {
    const token = await fetchToken(request, 'admin');
    const userToken = await fetchToken(request, 'user');
    const repoId = await createRepo(request, token, artifact('officeshare'));
    const docPath = '/report.docx';
    try {
      await createFile(request, token, repoId, docPath); // backend seeds a template

      // Share the library with the end user (read-write).
      const shareRes = await request.put(
        `${API_URL}/api/v2.1/repos/${repoId}/dir/shared_items/?p=%2F`,
        {
          headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
          data: { share_type: 'user', username: [CREDENTIALS.user.email], permission: 'rw' },
        },
      );
      expect(shareRes.ok(), `share: ${shareRes.status()}`).toBeTruthy();

      // OWNER opens the doc through the real app UI → OnlyOffice viewer.
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await dismissPwaBanner(page);
      await page.locator('[data-testid="file-item"][data-name="report.docx"]').click();
      await expect(page.getByTestId('onlyoffice-viewer')).toBeVisible();

      // RECEIVER (the user the library was shared with) can also open/edit it:
      // the backend serves them a valid, JWT-signed editor config.
      const recv = await ooConfig(request, userToken, repoId, docPath);
      expect(recv.status(), 'receiver can open the shared office doc').toBe(200);
      const body = await recv.json();
      expect(body.doc?.editorConfig?.mode).toBe('edit');
      expect(body.doc?.token).toBeTruthy();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});
