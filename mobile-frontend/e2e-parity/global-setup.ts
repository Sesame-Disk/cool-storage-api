import { chromium, request as pwRequest } from '@playwright/test';
import type { FullConfig } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { BASE_URL, TOKEN_KEY, authStateFile, fetchToken } from './helpers/parity-helpers';
import type { Role } from './helpers/parity-helpers';

/**
 * Global setup: log in each role against the LIVE stack and persist a
 * storageState whose localStorage carries the app's session token. Specs then
 * start already-authenticated on the real backend — ready for full CRUD.
 */
async function setup(_config: FullConfig) {
  const roles: Role[] = ['user', 'admin', 'super'];
  const api = await pwRequest.newContext();

  const authDir = path.join(process.cwd(), 'e2e-parity', '.auth');
  fs.mkdirSync(authDir, { recursive: true });

  const browser = await chromium.launch();
  try {
    for (const role of roles) {
      const token = await fetchToken(api, role);

      const context = await browser.newContext();
      const page = await context.newPage();
      // Must be on the app origin before writing its localStorage.
      await page.goto(BASE_URL + '/login/');
      await page.evaluate(
        ([key, value]) => window.localStorage.setItem(key, value),
        [TOKEN_KEY, token] as const,
      );
      await context.storageState({ path: authStateFile(role) });
      await context.close();
      // eslint-disable-next-line no-console
      console.log(`[parity] seeded auth for role=${role}`);
    }
  } finally {
    await browser.close();
    await api.dispose();
  }
}

export default setup;
