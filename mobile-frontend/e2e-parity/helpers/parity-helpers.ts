import { expect } from '@playwright/test';
import type { APIRequestContext, Page } from '@playwright/test';
import path from 'node:path';

// ---------------------------------------------------------------------------
// Parity E2E helpers. These drive the REAL backend (a live disposable stack)
// through the mobile app's own origin, so tests exercise the same nginx proxy
// + API the app uses. Auth uses dev-mode tokens by default; set
// PARITY_AUTH_MODE=local to exercise the sesameauth local-auth login instead.
// ---------------------------------------------------------------------------

// 'user' = plain end user (has a low library quota — good for permission/parity
// checks). 'admin' = org admin. 'super' = platform superadmin with an UNLIMITED
// library quota — use it to arrange repos in CRUD specs so parallel viewport
// projects don't collide with the 3-library cap on user/admin.
export type Role = 'user' | 'admin' | 'super';

/** Role used to arrange repos in CRUD specs (unlimited library quota). */
export const ARRANGE_ROLE: Role = 'super';

/** Mobile app origin under test (nginx that proxies /api2 + /api/v2.1). */
export const BASE_URL = process.env.PARITY_BASE_URL ?? 'http://localhost:18073';
/** API origin — same as BASE_URL by default (same-origin proxy). */
export const API_URL = process.env.PARITY_API_URL ?? BASE_URL;

export const AUTH_MODE = (process.env.PARITY_AUTH_MODE ?? 'dev-token') as
  | 'dev-token'
  | 'local';

/** Seeded dev users. `admin@` is an org admin; `user@` is a plain end user. */
export const CREDENTIALS: Record<Role, { email: string; password: string }> = {
  user: { email: 'user@sesamefs.local', password: 'dev-token-user' },
  admin: { email: 'admin@sesamefs.local', password: 'dev-token-admin' },
  super: { email: 'superadmin@sesamefs.local', password: 'dev-token-superadmin' },
};

/** Where global-setup stashes each role's storageState (token in localStorage). */
export const authStateFile = (role: Role) =>
  path.join(process.cwd(), 'e2e-parity', '.auth', `${role}.json`);

/** Localstorage key the mobile app reads its session token from. */
export const TOKEN_KEY = 'seahub_token';

/** Collision-resistant name for isolated test artifacts. */
export function unique(prefix: string): string {
  return `${prefix}-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
}

/** All parity artifacts share this root prefix so cleanup can find strays. */
export const ARTIFACT_ROOT = 'pw-mob';
export function artifact(suite: string, kind = 'repo'): string {
  return unique(`${ARTIFACT_ROOT}-${suite}-${kind}`);
}

/**
 * Obtain a session token for a role directly from the backend. In dev-token
 * mode this hits /api2/auth-token/ (returns the dev token); in local mode it
 * uses the sesameauth login endpoint.
 */
export async function fetchToken(request: APIRequestContext, role: Role): Promise<string> {
  const { email, password } = CREDENTIALS[role];
  if (AUTH_MODE === 'local') {
    const res = await request.post(`${API_URL}/api/v2.1/auth/local/login`, {
      data: { email, password },
    });
    expect(res.ok(), `local-auth login for ${role}: ${res.status()}`).toBeTruthy();
    return (await res.json()).token;
  }
  // Dev-token mode: the session token IS the static dev token (equal to the
  // password), so return it directly. Hitting /api2/auth-token/ on every test
  // trips the login rate limiter (429) under parallel load.
  return password;
}

export function authHeaders(token: string): Record<string, string> {
  return { Authorization: `Token ${token}`, Accept: 'application/json' };
}

// --------------------------- API wrappers (CRUD) ---------------------------
// Thin wrappers so specs can arrange/cleanup state without clicking through UI.

export async function createRepo(
  request: APIRequestContext,
  token: string,
  name: string,
): Promise<string> {
  const res = await request.post(`${API_URL}/api2/repos/`, {
    headers: authHeaders(token),
    form: { name },
  });
  expect(res.ok(), `createRepo ${name}: ${res.status()}`).toBeTruthy();
  return (await res.json()).repo_id;
}

export async function deleteRepo(request: APIRequestContext, token: string, repoId: string) {
  await request.delete(`${API_URL}/api2/repos/${repoId}/`, { headers: authHeaders(token) });
}

export async function createFile(
  request: APIRequestContext,
  token: string,
  repoId: string,
  filePath: string,
) {
  const res = await request.post(
    `${API_URL}/api2/repos/${repoId}/file/?p=${encodeURIComponent(filePath)}`,
    { headers: authHeaders(token), form: { operation: 'create' } },
  );
  expect(res.ok(), `createFile ${filePath}: ${res.status()}`).toBeTruthy();
  return res.json();
}

export async function deleteFilePath(
  request: APIRequestContext,
  token: string,
  repoId: string,
  filePath: string,
) {
  const res = await request.delete(
    `${API_URL}/api2/repos/${repoId}/file/?p=${encodeURIComponent(filePath)}`,
    { headers: authHeaders(token) },
  );
  expect(res.ok(), `deleteFilePath ${filePath}: ${res.status()}`).toBeTruthy();
}

export async function mkdir(
  request: APIRequestContext,
  token: string,
  repoId: string,
  dirPath: string,
) {
  const res = await request.post(
    `${API_URL}/api2/repos/${repoId}/dir/?p=${encodeURIComponent(dirPath)}`,
    { headers: authHeaders(token), form: { operation: 'mkdir' } },
  );
  expect(res.ok(), `mkdir ${dirPath}: ${res.status()}`).toBeTruthy();
}

export async function listGroups(request: APIRequestContext, token: string): Promise<any[]> {
  const res = await request.get(`${API_URL}/api/v2.1/groups/`, { headers: authHeaders(token) });
  if (!res.ok()) return [];
  const data = await res.json();
  return Array.isArray(data) ? data : (data.groups ?? []);
}

export async function listDir(
  request: APIRequestContext,
  token: string,
  repoId: string,
  dirPath = '/',
): Promise<any[]> {
  const res = await request.get(
    `${API_URL}/api2/repos/${repoId}/dir/?p=${encodeURIComponent(dirPath)}`,
    { headers: authHeaders(token) },
  );
  expect(res.ok(), `listDir ${dirPath}: ${res.status()}`).toBeTruthy();
  return res.json();
}

export async function listRepos(request: APIRequestContext, token: string): Promise<any[]> {
  const res = await request.get(`${API_URL}/api2/repos/`, { headers: authHeaders(token) });
  expect(res.ok(), `listRepos: ${res.status()}`).toBeTruthy();
  return res.json();
}

/** Delete every repo whose name starts with the given prefix (best effort). */
export async function cleanupReposByPrefix(
  request: APIRequestContext,
  token: string,
  prefix: string,
) {
  let repos: any[] = [];
  try {
    repos = await listRepos(request, token);
  } catch {
    return;
  }
  for (const r of repos) {
    if (typeof r?.name === 'string' && r.name.startsWith(prefix)) {
      await deleteRepo(request, token, r.repo_id ?? r.id);
    }
  }
}

// --------------------------- UI helpers ---------------------------

/** Bottom-nav tab hrefs the app exposes. */
export const TABS = ['libraries', 'shared', 'groups', 'starred', 'more'] as const;

/** Wait for the app shell (bottom nav) to be interactive after navigation. */
export async function waitForAppShell(page: Page) {
  await expect(page.locator('nav').first()).toBeVisible({ timeout: 15_000 });
}
