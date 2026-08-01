import { test, expect, request as pwRequest } from '@playwright/test';
import type { APIRequestContext, Page } from '@playwright/test';
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
  mkdir,
} from '../helpers/parity-helpers';

// End-to-end sharing + transfer flows that the existing sharing.spec / files.spec
// don't cover: sharing a folder WITH ANOTHER USER (internal share), a real
// upload→download round-trip through the UI, and confirming a public share link
// actually resolves to a download. These exercise the app's own api.ts contract
// against the live backend.

/** GET the user/group shares on a repo path (the app's listRepoShareItems endpoint). */
async function getSharedItems(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
): Promise<any[]> {
  const res = await request.get(
    `${API_URL}/api/v2.1/repos/${repoId}/dir/shared_items/?p=${encodeURIComponent(path)}`,
    { headers: authHeaders(token) },
  );
  if (!res.ok()) return [];
  const data = await res.json();
  return Array.isArray(data) ? data : [];
}

async function waitForBrowser(page: Page) {
  await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
}

async function openContextMenu(page: Page, name: string) {
  await page.getByRole('button', { name: `More options for ${name}` }).click();
  await expect(page.getByTestId('context-menu-share')).toBeVisible();
}

/**
 * After a UI upload the progress sheet opens as a full-screen overlay and does
 * NOT auto-close; its backdrop intercepts pointer events, so any later tap (e.g.
 * a row's context menu) is blocked until it's dismissed. Tap the backdrop to
 * close it, exactly as a user would.
 */
async function dismissUploadSheet(page: Page) {
  const sheet = page.getByTestId('upload-progress-sheet');
  if (await sheet.isVisible().catch(() => false)) {
    await sheet.click({ position: { x: 8, y: 8 } });
    await sheet.waitFor({ state: 'hidden', timeout: 5_000 }).catch(() => {});
  }
}

// ---------------------------------------------------------------------------
// Share a folder WITH A USER (internal share).
//
// Sharing is org-scoped: sharer and sharee must be in the same organization, so
// this drives the org-admin session (admin@) and shares with the end user
// (user@), both in the Default org. (The regression it guards: the app sent
// `username` as a scalar with the path in the body; the backend wants
// `username: []` and the path as the `p` query param, so user sharing 400'd
// every time.) Requires the Default org on an unlimited-library plan — the
// provisioning step (provision-local-users.mjs) sets quota_policy=soft.
// ---------------------------------------------------------------------------
test.describe('Share with a user (internal share)', () => {
  test.use({ storageState: authStateFile('admin') });

  test('shares a folder with another user via the share sheet', async ({ page, request }) => {
    const token = await fetchToken(request, 'admin');
    const repoId = await createRepo(request, token, artifact('intshare'));
    const folder = `team-${Date.now().toString(36)}`;
    const sharee = CREDENTIALS.user.email; // user@sesamefs.local (same org)
    try {
      await mkdir(request, token, repoId, `/${folder}`);

      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);

      // Open the folder's share sheet → "With users" tab.
      await openContextMenu(page, folder);
      await page.getByTestId('context-menu-share').click();
      await page.getByTestId('tab-internal-share').click();

      // Search for the user, pick the result, choose Read-Write, share.
      await page.getByLabel('Search users').fill('user');
      const result = page.locator('button', { hasText: sharee });
      await expect(result.first()).toBeVisible({ timeout: 10_000 });
      await result.first().click();
      await page.locator('#permission-select').selectOption('rw');
      await page.getByRole('button', { name: /Share with 1 user/ }).click();

      // The backend now records the user share on that folder...
      await expect
        .poll(async () => {
          const items = await getSharedItems(request, token, repoId, `/${folder}`);
          return items.some((i) => i.share_type === 'user' && i.share_to === sharee);
        }, { timeout: 10_000 })
        .toBe(true);

      // ...and the UI reflects it: the "Shared with users" section now renders
      // (the list refreshed after the share and shows at least one entry).
      await expect(page.getByText('Shared with users')).toBeVisible();
      await expect(page.getByRole('button', { name: /^Remove / })).toBeVisible();
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});

// ---------------------------------------------------------------------------
// Upload → download round-trip through the UI (arranged as the unlimited-quota
// superadmin so it runs on every viewport project).
// ---------------------------------------------------------------------------
test.describe('Upload and download', () => {
  test.use({ storageState: authStateFile(ARRANGE_ROLE) });

  test('a file uploaded via the UI downloads with identical bytes', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('dl'));
    const name = `roundtrip-${Date.now().toString(36)}.txt`;
    const content = `hello parity ${Date.now().toString(36)} download`;
    try {
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);

      // Upload via the always-present hidden file input.
      await page.getByTestId('file-input').setInputFiles({
        name,
        mimeType: 'text/plain',
        buffer: Buffer.from(content),
      });
      const row = page.locator(`[data-testid="file-item"][data-name="${name}"]`);
      await expect(row).toBeVisible({ timeout: 15_000 });
      await dismissUploadSheet(page);

      // Download via the context menu and capture the browser download.
      await openContextMenu(page, name);
      const downloadPromise = page.waitForEvent('download');
      await page.getByTestId('context-menu-download').click();
      const download = await downloadPromise;

      const stream = await download.createReadStream();
      const chunks: Buffer[] = [];
      for await (const chunk of stream) chunks.push(chunk as Buffer);
      expect(Buffer.concat(chunks).toString()).toBe(content);
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});

// ---------------------------------------------------------------------------
// Public share link actually resolves to a downloadable file (unauthenticated).
// ---------------------------------------------------------------------------
test.describe('Public share link', () => {
  test.use({ storageState: authStateFile(ARRANGE_ROLE) });

  test('a created share link downloads the file without authentication', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const repoId = await createRepo(request, token, artifact('publink'));
    const name = `public-${Date.now().toString(36)}.txt`;
    const content = `public link body ${Date.now().toString(36)}`;
    try {
      // Upload real content through the UI so the link has something to serve.
      await page.goto(`/libraries/${repoId}/`);
      await waitForBrowser(page);
      await page.getByTestId('file-input').setInputFiles({
        name,
        mimeType: 'text/plain',
        buffer: Buffer.from(content),
      });
      await expect(
        page.locator(`[data-testid="file-item"][data-name="${name}"]`),
      ).toBeVisible({ timeout: 15_000 });

      // Create a public share link (UI creation is covered in sharing.spec).
      const linkRes = await request.post(`${API_URL}/api/v2.1/share-links/`, {
        headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
        data: { repo_id: repoId, path: `/${name}` },
      });
      expect(linkRes.ok(), `create link: ${linkRes.status()}`).toBeTruthy();
      const link = await linkRes.json();
      expect(link.permissions?.can_download).toBe(true);

      // The download endpoint resolves for an ANONYMOUS request (no auth headers).
      const anon = await pwRequest.newContext();
      try {
        const dl = await anon.get(`${link.link}/?dl=1`, { maxRedirects: 0 });
        // 302 → redirect to the storage/file server (proves the link is live and
        // downloadable) or a direct 200. Either way it is NOT an auth wall.
        expect([200, 301, 302]).toContain(dl.status());
        if (dl.status() === 200) {
          expect((await dl.body()).toString()).toBe(content);
        } else {
          expect(dl.headers()['location']).toBeTruthy();
        }
      } finally {
        await anon.dispose();
      }
    } finally {
      await deleteRepo(request, token, repoId);
    }
  });
});

// Shared-with-me: a library shared WITH the end user shows up on their Shared
// page. (Regression: the app fetched /api/v2.1/beshared-repos/ which 404s, so
// the whole "Shared with me" tab silently failed — the web frontend lists these
// via /api2/repos/?type=shared.)
test.describe('Shared with me', () => {
  test.use({ storageState: authStateFile('user') });

  test('a library shared with the user appears on the Shared page', async ({ page, request }) => {
    const adminToken = await fetchToken(request, 'admin');
    const repoName = artifact('sharedwith');
    const repoId = await createRepo(request, adminToken, repoName);
    try {
      await request.put(`${API_URL}/api/v2.1/repos/${repoId}/dir/shared_items/?p=%2F`, {
        headers: { ...authHeaders(adminToken), 'Content-Type': 'application/json' },
        data: { share_type: 'user', username: [CREDENTIALS.user.email], permission: 'rw' },
      });

      await page.goto('/shared/');
      await dismissPwaBanner(page);
      // "Shared with me" is the default tab; the shared library is listed.
      await expect(page.getByText(repoName, { exact: false }).first()).toBeVisible({ timeout: 15_000 });
    } finally {
      await deleteRepo(request, adminToken, repoId);
    }
  });
});
