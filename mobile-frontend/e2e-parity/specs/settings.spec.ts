import { test, expect } from '@playwright/test';
import {
  ARRANGE_ROLE,
  API_URL,
  authHeaders,
  authStateFile,
  fetchToken,
  artifact,
} from '../helpers/parity-helpers';

// Settings / profile (E10). Drives the mobile Settings UI against the live
// stack: edits the display name and asserts it persisted via the account API,
// and that the storage/quota UI renders.
//
// The super account is SHARED across specs/viewport projects, so we capture the
// original display name first and ALWAYS restore it in `finally`.
test.use({ storageState: authStateFile(ARRANGE_ROLE) });

async function getAccountName(request: any, token: string): Promise<string> {
  const res = await request.get(`${API_URL}/api2/account/info/`, {
    headers: authHeaders(token),
  });
  expect(res.ok(), `getAccountInfo: ${res.status()}`).toBeTruthy();
  return (await res.json()).name ?? '';
}

async function setAccountName(request: any, token: string, name: string) {
  const res = await request.put(`${API_URL}/api2/account/info/`, {
    headers: { ...authHeaders(token), 'Content-Type': 'application/json' },
    data: { name },
  });
  expect(res.ok(), `setAccountName: ${res.status()}`).toBeTruthy();
}

test.describe('Settings / profile', () => {
  test('edit display name persists and storage UI renders', async ({ page, request }) => {
    const token = await fetchToken(request, ARRANGE_ROLE);
    const original = await getAccountName(request, token);
    const newName = artifact('settings', 'name');

    try {
      await page.goto('/settings/');
      await expect(page.getByTestId('settings-page')).toBeVisible();

      // Storage / quota UI renders.
      await expect(page.getByTestId('storage-usage-bar')).toBeVisible();

      // Edit the display name through the UI.
      const input = page.getByTestId('settings-name-input');
      await expect(input).toBeVisible();
      await input.fill(newName);
      await page.getByTestId('settings-name-save').click();

      // Persisted server-side.
      await expect
        .poll(async () => getAccountName(request, token))
        .toBe(newName);
    } finally {
      // Restore the shared account's original name no matter what.
      await setAccountName(request, token, original);
    }
  });
});
