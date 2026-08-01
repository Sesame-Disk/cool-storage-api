import { test, expect } from '@playwright/test';
import type { APIRequestContext, Page } from '@playwright/test';
import {
  API_URL,
  ARRANGE_ROLE,
  CREDENTIALS,
  artifact,
  authHeaders,
  authStateFile,
  createRepo,
  deleteRepo,
  dismissPwaBanner,
  fetchToken,
  mkdir,
} from '../helpers/parity-helpers';

// Adversarial / edge-case coverage: prove hostile content can't execute script
// (XSS), and that sharing an ENCRYPTED library does not leak its contents to the
// recipient without the password.

/** Upload real file content via the upload-link flow (what the app does). */
async function uploadContent(
  request: APIRequestContext,
  token: string,
  repoId: string,
  name: string,
  content: string,
) {
  const linkRes = await request.get(`${API_URL}/api2/repos/${repoId}/upload-link/`, {
    headers: authHeaders(token),
  });
  const uploadUrl = (await linkRes.json()) as string;
  const res = await request.post(uploadUrl, {
    headers: authHeaders(token),
    multipart: {
      file: { name, mimeType: 'text/markdown', buffer: Buffer.from(content) },
      parent_dir: '/',
      replace: '1',
    },
  });
  expect(res.ok(), `upload content: ${res.status()}`).toBeTruthy();
}

/** Install probes that flip if any injected payload runs. */
async function armXssProbe(page: Page): Promise<{ dialogFired: () => boolean }> {
  let dialog = false;
  page.on('dialog', (d) => {
    dialog = true;
    d.dismiss().catch(() => {});
  });
  await page.addInitScript(() => {
    (window as any).__xss = 0;
  });
  return { dialogFired: () => dialog };
}

test.describe('XSS safety (hostile content is inert)', () => {
  test.use({ storageState: authStateFile(ARRANGE_ROLE) });

  test('malicious markdown content does not execute and renders as text', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('xssmd'));
    const name = `evil-${Date.now().toString(36)}.md`;
    const payload = [
      '# Heading',
      '<script>window.__xss=1;alert("xss")</script>',
      '<img src=x onerror="window.__xss=1">',
      '[click](javascript:window.__xss=1)',
      '**bold** and `code`',
    ].join('\n\n');
    try {
      await uploadContent(request, token, repoId, name, payload);
      const probe = await armXssProbe(page);

      await page.goto(`/libraries/${repoId}/`);
      await dismissPwaBanner(page);
      await page.locator(`[data-testid="file-item"][data-name="${name}"]`).click();
      await expect(page.getByTestId('markdown-viewer')).toBeVisible();
      await page.waitForTimeout(800);

      // No alert, no injected side-effect.
      expect(probe.dialogFired(), 'no alert() fired').toBe(false);
      expect(await page.evaluate(() => (window as any).__xss)).toBeFalsy();
      // The <script> is shown as literal, escaped text — not a DOM element.
      await expect(page.getByTestId('markdown-content')).toContainText('<script>');
      expect(await page.locator('[data-testid="markdown-content"] script').count()).toBe(0);
      expect(await page.locator('[data-testid="markdown-content"] img').count()).toBe(0);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });

  test('a folder whose NAME is an injection payload renders as inert text', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('xssdir'));
    const folder = '<img src=x onerror=window.__xss=1>';
    try {
      await mkdir(request, token, repoId, `/${folder}`);
      const probe = await armXssProbe(page);

      await page.goto(`/libraries/${repoId}/`);
      await dismissPwaBanner(page);
      await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
      await page.waitForTimeout(800);

      expect(probe.dialogFired()).toBe(false);
      expect(await page.evaluate(() => (window as any).__xss)).toBeFalsy();
      // Listed as literal text; no injected <img> in the file list.
      const row = page.locator(`[data-testid="file-item"][data-name="${folder}"]`);
      await expect(row).toBeVisible();
      expect(await row.locator('img[onerror]').count()).toBe(0);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});

test.describe('Encrypted-library sharing is gated by the password', () => {
  test.use({ storageState: authStateFile('user') }); // drive as the recipient

  test('a recipient of a shared encrypted library still needs the password', async ({ page, request }) => {
    const adminToken = await fetchToken(request, 'admin');
    const repoId = (
      await (
        await request.post(`${API_URL}/api2/repos/`, {
          headers: { ...authHeaders(adminToken), 'Content-Type': 'application/json' },
          data: { name: artifact('encshare'), encrypted: true, passwd: 'SecretPass123' },
        })
      ).json()
    ).repo_id;
    try {
      // Share the encrypted library with the end user.
      await request.put(`${API_URL}/api/v2.1/repos/${repoId}/dir/shared_items/?p=%2F`, {
        headers: { ...authHeaders(adminToken), 'Content-Type': 'application/json' },
        data: { share_type: 'user', username: [CREDENTIALS.user.email], permission: 'rw' },
      });

      // The recipient opening it is prompted for the password — contents are NOT
      // shown, and a wrong password is rejected.
      await page.goto(`/libraries/${repoId}/`);
      await dismissPwaBanner(page);
      await expect(page.getByTestId('decrypt-password-input')).toBeVisible({ timeout: 15_000 });

      await page.getByTestId('decrypt-password-input').fill('definitely-wrong');
      await page.getByTestId('decrypt-submit').click();
      await expect(page.getByTestId('decrypt-error')).toBeVisible({ timeout: 10_000 });

      // And at the API level the recipient cannot list the dir without unlocking.
      const list = await request.get(`${API_URL}/api2/repos/${repoId}/dir/?p=/`, {
        headers: authHeaders(await fetchToken(request, 'user')),
      });
      expect([400, 403]).toContain(list.status());
    } finally {
      await request.delete(`${API_URL}/api2/repos/${repoId}/`, { headers: authHeaders(adminToken) }).catch(() => {});
    }
  });
});
