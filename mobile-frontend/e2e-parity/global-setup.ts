import { chromium, request as pwRequest } from '@playwright/test';
import type { FullConfig } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { BASE_URL, TOKEN_KEY, authStateFile, fetchToken } from './helpers/parity-helpers';
import type { Role } from './helpers/parity-helpers';
import { maybeStartSecureProxy } from './helpers/secure-proxy';

/**
 * Global setup: log in each role against the LIVE stack and persist a
 * storageState whose localStorage carries the app's session token. Specs then
 * start already-authenticated on the real backend — ready for full CRUD.
 *
 * Returns a teardown function (Playwright awaits it after the run) that stops
 * the secure-context proxy, if one was started for a containerized run.
 */
async function setup(_config: FullConfig): Promise<() => Promise<void>> {
  // Start the loopback secure-context proxy FIRST (if PARITY_PROXY_TARGET is
  // set) so the BASE_URL/localhost requests below already route through it.
  const stopProxy = await maybeStartSecureProxy();

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
  } catch (err) {
    // Setup failed — don't leak the proxy listener.
    await stopProxy();
    throw err;
  } finally {
    await browser.close();
    await api.dispose();
  }

  return stopProxy;
}

export default setup;
