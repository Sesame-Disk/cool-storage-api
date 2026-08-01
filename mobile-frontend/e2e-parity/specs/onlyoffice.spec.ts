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

// OnlyOffice document parity: office documents (.docx/.xlsx/.pptx/…) must open
// in the embedded OnlyOffice editor on mobile, at parity with the web frontend
// — not dead-end at the download-only generic view. Arrange a library + an
// office file via the API as the unlimited-quota superadmin, then drive the
// real FileBrowser: tap the file → the OnlyOffice viewer mounts and the backend
// serves a signed editor config. The document server's own api.js render is not
// asserted here (that needs the external OnlyOffice container reachable from the
// browser); what we verify is the mobile ROUTING + backend integration, which
// is exactly the parity gap this closes.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

/** Wait for the file browser to finish its initial load (breadcrumb Root button). */
async function waitForBrowser(page: Page) {
  await expect(page.getByRole('button', { name: 'Root' })).toBeVisible({ timeout: 15_000 });
}

async function openFile(page: Page, name: string) {
  const row = page.locator(`[data-testid="file-item"][data-name="${name}"]`);
  await expect(row).toBeVisible();
  await row.click();
}

test.describe('OnlyOffice document viewer', () => {
  for (const ext of ['docx', 'xlsx', 'pptx']) {
    test(`tapping a .${ext} opens the OnlyOffice viewer and fetches a signed config`, async ({
      page,
      request,
    }) => {
      const token = await fetchToken(request, ARRANGE_ROLE);
      const repoId = await createRepo(request, token, artifact('onlyoffice'));
      const name = `doc-${Date.now().toString(36)}.${ext}`;
      try {
        await createFile(request, token, repoId, `/${name}`);

        await page.goto(`/libraries/${repoId}/`);
        await waitForBrowser(page);

        // The viewer's config request to the backend proves backend integration.
        const configResponse = page.waitForResponse(
          (r) => r.url().includes(`/onlyoffice/`) && r.request().method() === 'GET',
          { timeout: 15_000 },
        );

        await openFile(page, name);

        // The office file routes to the OnlyOffice viewer (not GenericFileView).
        const viewer = page.getByTestId('onlyoffice-viewer');
        await expect(viewer).toBeVisible();

        // Graceful degradation: even if the document server can't be reached,
        // the viewer always exposes a download affordance, so an office file is
        // never a dead end (parity with web, which also offers download). Match
        // the header button exactly so it isn't confused with the error-state
        // "Download instead" button (substring matching would hit both).
        await expect(
          viewer.getByRole('button', { name: 'Download', exact: true }),
        ).toBeVisible();

        const res = await configResponse;
        expect(res.status(), 'backend serves an OnlyOffice editor config').toBe(200);
        const body = await res.json();
        expect(body.api_js_url, 'config carries the document-server api.js URL').toBeTruthy();
        expect(body.doc?.token, 'editor config is JWT-signed').toBeTruthy();
      } finally {
        await deleteRepo(request, token, repoId);
      }
    });
  }
});
