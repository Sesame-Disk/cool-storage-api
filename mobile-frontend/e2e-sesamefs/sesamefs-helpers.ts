/**
 * Shared helpers for the SesameFS desktop Playwright suites.
 *
 * These tests drive the running dev stack (AUTH_DEV_MODE=true) as a developer:
 * they authenticate with the built-in dev tokens, exercise the REST API the
 * web UI relies on, and assert real behaviour (features, sharing, concurrency).
 *
 * NOTE: this file is intentionally NOT a *.spec.ts so Playwright does not try
 * to run it as a test file — it only exports helpers.
 *
 * All endpoint shapes were verified live against the dev backend (see the
 * curl probes used while authoring these suites).
 */
import type { APIRequestContext, BrowserContext } from '@playwright/test';

export type DevRole = 'admin' | 'user' | 'readonly' | 'guest' | 'superadmin';

/** Built-in dev tokens (AUTH_DEV_MODE). In dev mode the login password == token. */
export const DEV_TOKENS: Record<DevRole, string> = {
  admin: process.env.DEV_TOKEN_ADMIN || 'dev-token-admin',
  user: process.env.DEV_TOKEN_USER || 'dev-token-user',
  readonly: process.env.DEV_TOKEN_READONLY || 'dev-token-readonly',
  guest: process.env.DEV_TOKEN_GUEST || 'dev-token-guest',
  superadmin: process.env.DEV_TOKEN_SUPERADMIN || 'dev-token-superadmin',
};

export const DEV_EMAILS: Record<DevRole, string> = {
  admin: 'admin@sesamefs.local',
  user: 'user@sesamefs.local',
  readonly: 'readonly@sesamefs.local',
  guest: 'guest@sesamefs.local',
  superadmin: 'superadmin@sesamefs.local',
};

/** Every test artifact name starts with this so cleanup never touches real libs. */
export const TEST_REPO_PREFIX = 'pw-e2e-';

/**
 * Per-suite prefixes. Each spec file owns one so that prefix-based cleanup in a
 * file's afterAll only removes *that* file's libraries — critical when files run
 * concurrently against the same dev account.
 */
export const SUITE_PREFIX = {
  features: 'pw-e2e-feat-',
  sharing: 'pw-e2e-share-',
  concurrency: 'pw-e2e-conc-',
  multiregion: 'pw-e2e-mr-',
} as const;

export function uniqueName(tag: string, prefix: string = TEST_REPO_PREFIX): string {
  return `${prefix}${tag}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function authHeaders(token: string): Record<string, string> {
  return { Authorization: `Token ${token}`, Accept: 'application/json' };
}

/**
 * Authenticate the BROWSER as a developer for UI tests.
 *
 * This dev stack's frontend only offers SSO on the login page (password login is
 * disabled), and the backend owns a `sesamefs_auth` cookie in the form
 * `email@token`. In AUTH_DEV_MODE the token segment is matched against the built-in
 * dev tokens, so seeding that cookie logs the browser in as the chosen dev role.
 */
export async function browserDevLogin(
  context: BrowserContext,
  baseURL: string,
  role: DevRole = 'admin',
): Promise<void> {
  await context.addCookies([
    { name: 'sesamefs_auth', value: `${DEV_EMAILS[role]}@${DEV_TOKENS[role]}`, url: baseURL },
  ]);
}

function jsonHeaders(token: string): Record<string, string> {
  return { ...authHeaders(token), 'Content-Type': 'application/json' };
}

async function readJSON(response: { json(): Promise<unknown> }): Promise<any> {
  try {
    return await response.json();
  } catch {
    return {};
  }
}

function errMsg(payload: any): string {
  if (!payload || typeof payload !== 'object') return '';
  const e = payload.error ?? payload.error_msg;
  return typeof e === 'string' ? e : '';
}

export type Dirent = {
  id: string;
  name: string;
  type: 'file' | 'dir';
  size: number;
  is_locked?: boolean;
  lock_owner?: string;
  permission?: string;
};

export type AccountInfo = {
  email: string;
  name: string;
  login_id: string;
  is_staff?: boolean;
  can_add_repo?: boolean;
  [k: string]: unknown;
};

/** GET /api2/account/info/ — who is this token? */
export async function accountInfo(request: APIRequestContext, token: string): Promise<AccountInfo> {
  const res = await request.get('/api2/account/info/', { headers: authHeaders(token) });
  if (!res.ok()) throw new Error(`account info failed: ${res.status()}`);
  return (await readJSON(res)) as AccountInfo;
}

/** POST /api/v2.1/repos/ — returns repo_id. Skip-aware on plan/limit errors. */
export async function createRepo(
  request: APIRequestContext,
  token: string,
  name: string,
): Promise<{ repoId: string } | { skipReason: string }> {
  const res = await request.post('/api/v2.1/repos/', {
    headers: jsonHeaders(token),
    data: { repo_name: name },
  });
  const payload = await readJSON(res);
  if (!res.ok()) {
    const error = errMsg(payload);
    if (error === 'Library limit reached' || error.includes('not available on your plan')) {
      return { skipReason: error };
    }
    throw new Error(`createRepo failed: ${error || res.status()}`);
  }
  const repoId = payload.repo_id;
  if (typeof repoId !== 'string' || !repoId) throw new Error(`createRepo: no repo_id (${JSON.stringify(payload)})`);
  return { repoId };
}

export async function deleteRepo(request: APIRequestContext, token: string, repoId: string): Promise<void> {
  await request.delete(`/api/v2.1/repos/${encodeURIComponent(repoId)}/`, { headers: authHeaders(token) });
}

/** GET /api/v2.1/repos/?type=mine|shared — returns the repos array. */
export async function listRepos(
  request: APIRequestContext,
  token: string,
  type: 'mine' | 'shared',
): Promise<Array<Record<string, any>>> {
  const res = await request.get(`/api/v2.1/repos/?type=${type}`, { headers: authHeaders(token) });
  if (!res.ok()) throw new Error(`listRepos(${type}) failed: ${res.status()}`);
  const payload = await readJSON(res);
  return Array.isArray(payload.repos) ? payload.repos : [];
}

/** GET /api/v2.1/repos/:id/dir/?p= — directory listing. */
export async function listDir(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path = '/',
): Promise<Dirent[]> {
  const res = await request.get(
    `/api/v2.1/repos/${encodeURIComponent(repoId)}/dir/?p=${encodeURIComponent(path)}`,
    { headers: authHeaders(token) },
  );
  if (!res.ok()) throw new Error(`listDir failed: ${res.status()}`);
  const payload = await readJSON(res);
  return Array.isArray(payload.dirent_list) ? (payload.dirent_list as Dirent[]) : [];
}

export async function listDirNames(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path = '/',
): Promise<string[]> {
  return (await listDir(request, token, repoId, path)).map((d) => d.name).sort();
}

/** POST /api/v2.1/repos/:id/dir/?p=<path>&operation=mkdir */
export async function mkdir(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
): Promise<boolean> {
  const res = await request.post(
    `/api/v2.1/repos/${encodeURIComponent(repoId)}/dir/?p=${encodeURIComponent(path)}&operation=mkdir`,
    { headers: authHeaders(token) },
  );
  return res.ok();
}

/** POST /api2/repos/:id/file/?p=<path>&operation=create — empty file. */
export async function createFile(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
): Promise<boolean> {
  const res = await request.post(
    `/api2/repos/${encodeURIComponent(repoId)}/file/?p=${encodeURIComponent(path)}&operation=create`,
    { headers: authHeaders(token) },
  );
  return res.ok() || res.status() === 201;
}

/** POST /api/v2.1/repos/:id/upload/?p=<dir> — real multipart upload. Returns the API status. */
export async function uploadFile(
  request: APIRequestContext,
  token: string,
  repoId: string,
  parentDir: string,
  fileName: string,
  content: string,
): Promise<number> {
  const res = await request.post(
    `/api/v2.1/repos/${encodeURIComponent(repoId)}/upload/?p=${encodeURIComponent(parentDir)}`,
    {
      headers: authHeaders(token),
      multipart: {
        parent_dir: parentDir,
        'ret-json': '1',
        file: { name: fileName, mimeType: 'text/plain', buffer: Buffer.from(content) },
      },
    },
  );
  return res.status();
}

/** PUT /api2/repos/:id/dir/shared_items/?p=/ — share a library with another user. */
export async function shareWithUser(
  request: APIRequestContext,
  token: string,
  repoId: string,
  username: string,
  permission: 'rw' | 'r',
): Promise<{ ok: boolean; payload: any }> {
  const res = await request.put(
    `/api2/repos/${encodeURIComponent(repoId)}/dir/shared_items/?p=/`,
    { headers: jsonHeaders(token), data: { share_type: 'user', permission, username: [username] } },
  );
  return { ok: res.ok(), payload: await readJSON(res) };
}

/** PUT /api/v2.1/repos/:id/file/?p=<path> {operation: lock|unlock} */
export async function setLock(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
  operation: 'lock' | 'unlock',
): Promise<{ status: number; payload: any }> {
  const res = await request.put(
    `/api/v2.1/repos/${encodeURIComponent(repoId)}/file/?p=${encodeURIComponent(path)}`,
    { headers: jsonHeaders(token), data: { operation } },
  );
  return { status: res.status(), payload: await readJSON(res) };
}

/**
 * Download a file's bytes via the Seafile-style two-step flow:
 *   1. GET /api/v2.1/repos/:id/file/download-link/?p=<path> -> a JSON string URL
 *      of the form {server}/seafhttp/files/{token}/{name} (token minted locally
 *      by whichever region serves this request).
 *   2. GET that link -> the raw file bytes (streamed from that region's S3/MinIO).
 *
 * The link is fetched against the SAME request context, and we keep only its
 * path+query, so the whole download stays on the region under test — which is
 * exactly what lets us prove a file written in one region is fully retrievable
 * from another (its blocks must have replicated into the second region's MinIO).
 * Returns { status, body }; body is '' when the link or download is not 200.
 */
export async function downloadFileContent(
  request: APIRequestContext,
  token: string,
  repoId: string,
  path: string,
): Promise<{ status: number; body: string }> {
  const linkRes = await request.get(
    `/api/v2.1/repos/${encodeURIComponent(repoId)}/file/download-link/?p=${encodeURIComponent(path)}`,
    { headers: authHeaders(token) },
  );
  if (!linkRes.ok()) return { status: linkRes.status(), body: '' };
  let url = await readJSON(linkRes);
  if (typeof url !== 'string' || !url) return { status: 502, body: '' };
  try {
    const u = new URL(url);
    url = u.pathname + u.search; // hit the current baseURL/region, not the host the link names
  } catch {
    /* already a relative path — use as-is */
  }
  const fileRes = await request.get(url, { headers: authHeaders(token) });
  return { status: fileRes.status(), body: fileRes.ok() ? await fileRes.text() : '' };
}

/** POST /api/v2.1/repos/:id/file/copy/ */
export async function copyFile(
  request: APIRequestContext,
  token: string,
  repoId: string,
  srcPath: string,
  dstDir: string,
): Promise<number> {
  const res = await request.post(`/api/v2.1/repos/${encodeURIComponent(repoId)}/file/copy/`, {
    headers: jsonHeaders(token),
    data: { src_repo_id: repoId, src_path: srcPath, dst_repo_id: repoId, dst_dir: dstDir },
  });
  return res.status();
}

/** POST /api/v2.1/share-links/ — public (optionally password-protected) share link. */
export async function createShareLink(
  request: APIRequestContext,
  token: string,
  repoId: string,
  opts: { path?: string; password?: string } = {},
): Promise<{ token: string } | { skipReason: string }> {
  const res = await request.post('/api/v2.1/share-links/', {
    headers: jsonHeaders(token),
    data: {
      repo_id: repoId,
      path: opts.path || '/',
      ...(opts.password ? { password: opts.password } : {}),
      permissions: JSON.stringify({ can_edit: false, can_download: true }),
    },
  });
  const payload = await readJSON(res);
  if (!res.ok()) {
    const error = errMsg(payload);
    if (error === 'Share link limit reached' || error.includes('not available on your plan')) {
      return { skipReason: error };
    }
    throw new Error(`createShareLink failed: ${error || res.status()}`);
  }
  if (typeof payload.token !== 'string') throw new Error('share link: no token');
  return { token: payload.token };
}

/**
 * Delete every test artifact owned by this token (libraries + their share links)
 * whose name starts with `prefix`. Pass a per-suite prefix so concurrent spec
 * files never delete each other's in-flight libraries.
 */
export async function cleanupTestArtifacts(
  request: APIRequestContext,
  token: string,
  prefix: string = TEST_REPO_PREFIX,
): Promise<void> {
  // Share links pointing at our test repos.
  const slRes = await request.get('/api/v2.1/share-links/', { headers: authHeaders(token) });
  if (slRes.ok()) {
    const links = await readJSON(slRes);
    if (Array.isArray(links)) {
      for (const link of links) {
        if (typeof link?.repo_name === 'string' && link.repo_name.startsWith(prefix) && link.token) {
          await request.delete(`/api/v2.1/share-links/${encodeURIComponent(link.token)}/`, {
            headers: authHeaders(token),
          });
        }
      }
    }
  }
  // The libraries themselves.
  const repos = await listRepos(request, token, 'mine');
  for (const repo of repos) {
    if (typeof repo.repo_name === 'string' && repo.repo_name.startsWith(prefix) && repo.repo_id) {
      await deleteRepo(request, token, repo.repo_id);
    }
  }
}
