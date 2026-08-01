import { test, expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import {
  API_URL,
  ARRANGE_ROLE,
  artifact,
  authHeaders,
  authStateFile,
  deleteRepo,
  dismissPwaBanner,
  fetchToken,
} from '../helpers/parity-helpers';

/** Create an encrypted library directly against the backend (as the app's
 * createRepo does: `encrypted` MUST be a JSON boolean — the string→bool bug is
 * covered by the api unit tests). Returns the new repo id. */
async function createEncryptedRepo(
  request: any,
  token: string,
  name: string,
  password: string,
): Promise<string> {
  const res = await request.post(`${API_URL}/api2/repos/`, {
    headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
    data: { name, encrypted: true, passwd: password },
  });
  expect(res.ok(), `create encrypted repo: ${res.status()}`).toBeTruthy();
  return (await res.json()).repo_id;
}

// Encrypted-library end-to-end through the PWA UI: create an encrypted library,
// unlock it with the password, then round-trip a file (upload → download) and
// confirm the bytes survive the server-side encrypt/decrypt. This guards two
// bugs that made encrypted libraries unusable from the app:
//   1. createRepo sent `encrypted: "true"` (string) — backend wants a bool, so
//      creation failed ("cannot unmarshal string into ... bool").
//   2. setRepoPassword posted to /api2/repos/:id/ — the real unlock endpoint is
//      POST /api/v2.1/repos/:id/set-password/, so unlocking always 400'd.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

const PASSWORD = 'SecretPass123';

async function dismissUploadSheet(page: Page) {
  const sheet = page.getByTestId('upload-progress-sheet');
  if (await sheet.isVisible().catch(() => false)) {
    await sheet.click({ position: { x: 8, y: 8 } });
    await sheet.waitFor({ state: 'hidden', timeout: 5_000 }).catch(() => {});
  }
}

test.describe('Encrypted library', () => {
  test('create → unlock → upload/download round-trip', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const name = artifact('enc');
    const repoId = await createEncryptedRepo(request, token, name, PASSWORD);
    try {
      // It is really flagged encrypted on the backend.
      await expect
        .poll(async () => {
          const r = await request.get(`${API_URL}/api2/repos/${repoId}/`, {
            headers: authHeaders(token),
          });
          return (await r.json()).encrypted;
        })
        .toBeTruthy();

      // Opening it prompts for the password → unlock (set-password endpoint fix).
      await page.goto(`/libraries/${repoId}/`);
      await expect(page.getByTestId('decrypt-password-input')).toBeVisible({ timeout: 15_000 });
      await page.getByTestId('decrypt-password-input').fill(PASSWORD);
      await page.getByTestId('decrypt-submit').click();

      // Unlocked: the file browser loads (no decrypt error).
      await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
      await expect(page.getByTestId('decrypt-error')).toBeHidden();
      await dismissPwaBanner(page);
      // Wait until the server-side decrypt session is actually ready before
      // uploading. The harness drives faster than a human and can fire the
      // upload before the set-password round-trip lands (a real user's tap
      // can't); upload-link returning 200 confirms the session is established.
      await expect
        .poll(
          async () => {
            const r = await request.get(`${API_URL}/api2/repos/${repoId}/upload-link/`, {
              headers: authHeaders(token),
            });
            return r.status();
          },
          { timeout: 15_000 },
        )
        .toBe(200);

      // Upload a file with known content, then download it: the server encrypts
      // at rest and decrypts on the way out, so the bytes must round-trip.
      const content = `encrypted body ${Date.now().toString(36)}`;
      const fileName = `secret-${Date.now().toString(36)}.txt`;
      await page.getByTestId('file-input').setInputFiles({
        name: fileName,
        mimeType: 'text/plain',
        buffer: Buffer.from(content),
      });
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${fileName}"]`),
      ).toBeVisible({ timeout: 15_000 });
      await dismissUploadSheet(page);

      await page.getByRole('button', { name: `More options for ${fileName}` }).click();
      await expect(page.getByTestId('context-menu-download')).toBeVisible();
      const downloadPromise = page.waitForEvent('download');
      await page.getByTestId('context-menu-download').click();
      const download = await downloadPromise;

      const stream = await download.createReadStream();
      const chunks: Buffer[] = [];
      for await (const chunk of stream) chunks.push(chunk as Buffer);
      expect(Buffer.concat(chunks).toString()).toBe(content);
    } finally {
      if (repoId) await deleteRepo(request, token, repoId);
    }
  });
});
