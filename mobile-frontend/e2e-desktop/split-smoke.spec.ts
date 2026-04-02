import { expect, test, type APIRequestContext, type Page } from '@playwright/test';

type Credentials = {
  email: string;
  password: string;
};

type PublicSmokeRepoResult =
  | { repoId: string; repoName: string }
  | { skipReason: string };

type PublicLinkResult =
  | { token: string }
  | { skipReason: string };

const baseURL = process.env.DESKTOP_BASE_URL || 'http://localhost:3000';
const trustedDesktopHosts = new Set(['localhost', '127.0.0.1', 'frontend']);

function isTrustedDesktopBaseURL(url: string) {
  try {
    const { hostname, protocol } = new URL(url);
    return (protocol === 'http:' || protocol === 'https:') && trustedDesktopHosts.has(hostname);
  } catch {
    return false;
  }
}

const isLocalDesktopBaseURL = isTrustedDesktopBaseURL(baseURL);

function requireEnv(name: string) {
  const value = process.env[name];
  return typeof value === 'string' && value.length > 0;
}

function assertDesktopSmokeCredentialConfig() {
  if (isLocalDesktopBaseURL) {
    return;
  }

  const requiredVars = [
    'DESKTOP_SMOKE_USER_EMAIL',
    'DESKTOP_SMOKE_USER_PASSWORD',
    'DESKTOP_SMOKE_ORG_ADMIN_EMAIL',
    'DESKTOP_SMOKE_ORG_ADMIN_PASSWORD',
    'DESKTOP_SMOKE_SYS_ADMIN_EMAIL',
    'DESKTOP_SMOKE_SYS_ADMIN_PASSWORD',
  ];
  const missingVars = requiredVars.filter((name) => !requireEnv(name));

  if (missingVars.length > 0) {
    throw new Error(
      `Set explicit DESKTOP_SMOKE_* credentials for non-local runs. Missing: ${missingVars.join(', ')}`
    );
  }
}

assertDesktopSmokeCredentialConfig();

const credentials = {
  user: {
    email: process.env.DESKTOP_SMOKE_USER_EMAIL || 'user@sesamefs.local',
    password: process.env.DESKTOP_SMOKE_USER_PASSWORD || 'dev-token-user',
  },
  orgAdmin: {
    email: process.env.DESKTOP_SMOKE_ORG_ADMIN_EMAIL || 'admin@sesamefs.local',
    password: process.env.DESKTOP_SMOKE_ORG_ADMIN_PASSWORD || 'dev-token-admin',
  },
  sysAdmin: {
    email: process.env.DESKTOP_SMOKE_SYS_ADMIN_EMAIL || 'superadmin@sesamefs.local',
    password: process.env.DESKTOP_SMOKE_SYS_ADMIN_PASSWORD || 'dev-token-superadmin',
  },
} satisfies Record<string, Credentials>;

const publicSmokeRepoPrefix = 'desktop-split-public-smoke-';
const publicLinkPassword = 'desktop-split-secret';

function publicSmokePrefixes() {
  return isLocalDesktopBaseURL
    ? [publicSmokeRepoPrefix, 'smoke-']
    : [publicSmokeRepoPrefix];
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function authHeaders(token: string) {
  return {
    'Authorization': `Token ${token}`,
    'Accept': 'application/json',
  };
}

function hasPublicSmokePrefix(value: string) {
  return publicSmokePrefixes().some((prefix) => value.startsWith(prefix));
}

async function readJSON(response: { json(): Promise<unknown> }) {
  try {
    return await response.json();
  } catch {
    return {};
  }
}

function getErrorMessage(payload: unknown) {
  if (!payload || typeof payload !== 'object') {
    return '';
  }

  const candidate = payload as Record<string, unknown>;
  const error = candidate.error ?? candidate.error_msg;
  return typeof error === 'string' ? error : '';
}

async function loginViaAPI(request: APIRequestContext, user: Credentials) {
  const response = await request.post('/api2/auth-token/', {
    form: {
      username: user.email,
      password: user.password,
    },
  });

  const payload = await readJSON(response) as Record<string, unknown>;
  if (!response.ok()) {
    throw new Error(`API login failed for ${user.email}: ${getErrorMessage(payload) || response.status()}`);
  }

  const token = payload.token;
  if (typeof token !== 'string' || token.length === 0) {
    throw new Error(`API login for ${user.email} did not return a token`);
  }

  return token;
}

async function cleanupPublicSmokeArtifacts(request: APIRequestContext, token: string) {
  const repoIds = new Set<string>();
  for (const repoId of await cleanupPublicSmokeLinks(request, token, 'share')) {
    repoIds.add(repoId);
  }
  for (const repoId of await cleanupPublicSmokeLinks(request, token, 'upload')) {
    repoIds.add(repoId);
  }
  for (const repoId of repoIds) {
    await request.delete(`/api/v2.1/repos/${encodeURIComponent(repoId)}/`, {
      headers: authHeaders(token),
    });
  }
  await cleanupPublicSmokeRepos(request, token);
}

async function cleanupPublicSmokeLinks(request: APIRequestContext, token: string, kind: 'share' | 'upload') {
  const endpoint = kind === 'share' ? '/api/v2.1/share-links/' : '/api/v2.1/upload-links/';
  const response = await request.get(endpoint, {
    headers: authHeaders(token),
  });

  if (!response.ok()) {
    return [];
  }

  const payload = await readJSON(response);
  if (!Array.isArray(payload)) {
    return [];
  }

  const repoIds: string[] = [];

  for (const item of payload) {
    if (!item || typeof item !== 'object') {
      continue;
    }

    const record = item as Record<string, unknown>;
    const repoName = record.repo_name;
    const linkToken = record.token;
    if (typeof repoName !== 'string' || !hasPublicSmokePrefix(repoName)) {
      continue;
    }
    if (typeof linkToken !== 'string' || linkToken.length === 0) {
      continue;
    }
    if (typeof record.repo_id === 'string' && record.repo_id.length > 0) {
      repoIds.push(record.repo_id);
    }

    await request.delete(`${endpoint}${encodeURIComponent(linkToken)}/`, {
      headers: authHeaders(token),
    });
  }

  return repoIds;
}

async function cleanupPublicSmokeRepos(request: APIRequestContext, token: string) {
  const response = await request.get('/api/v2.1/repos/?type=mine', {
    headers: authHeaders(token),
  });

  if (!response.ok()) {
    return;
  }

  const payload = await readJSON(response) as { repos?: Array<Record<string, unknown>> };
  const repos = Array.isArray(payload.repos) ? payload.repos : [];

  for (const repo of repos) {
    const repoName = typeof repo.repo_name === 'string' ? repo.repo_name : '';
    const repoId = typeof repo.repo_id === 'string' ? repo.repo_id : '';
    if (!hasPublicSmokePrefix(repoName) || repoId.length === 0) {
      continue;
    }

    await request.delete(`/api/v2.1/repos/${encodeURIComponent(repoId)}/`, {
      headers: authHeaders(token),
    });
  }
}

async function createPublicSmokeRepo(request: APIRequestContext, token: string, suffix: string): Promise<PublicSmokeRepoResult> {
  const repoName = `${publicSmokeRepoPrefix}${suffix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const response = await request.post('/api/v2.1/repos/', {
    headers: {
      ...authHeaders(token),
      'Content-Type': 'application/json',
    },
    data: {
      repo_name: repoName,
    },
  });

  const payload = await readJSON(response) as Record<string, unknown>;
  if (!response.ok()) {
    const error = getErrorMessage(payload);
    if (error === 'Library limit reached' || error.includes('not available on your plan')) {
      return { skipReason: error };
    }
    throw new Error(`Failed to create smoke repo: ${error || response.status()}`);
  }

  const repoId = payload.repo_id;
  if (typeof repoId !== 'string' || repoId.length === 0) {
    throw new Error(`Create repo response missing repo_id: ${JSON.stringify(payload)}`);
  }

  return { repoId, repoName };
}

async function createEmptyRepoFile(request: APIRequestContext, token: string, repoId: string, filePath: string) {
  const params = new URLSearchParams({
    p: filePath,
    operation: 'create',
  });
  const response = await request.post(`/api2/repos/${encodeURIComponent(repoId)}/file/?${params.toString()}`, {
    headers: authHeaders(token),
    form: {},
  });

  if (!response.ok() && response.status() !== 201) {
    throw new Error(`Failed to create smoke file ${filePath}: ${response.status()}`);
  }
}

async function createProtectedShareLink(request: APIRequestContext, token: string, repoId: string): Promise<PublicLinkResult> {
  const response = await request.post('/api/v2.1/share-links/', {
    headers: {
      ...authHeaders(token),
      'Content-Type': 'application/json',
    },
    data: {
      repo_id: repoId,
      path: '/',
      password: publicLinkPassword,
      permissions: JSON.stringify({ can_edit: false, can_download: true }),
    },
  });

  const payload = await readJSON(response) as Record<string, unknown>;
  if (!response.ok()) {
    const error = getErrorMessage(payload);
    if (error === 'Share link limit reached' || error.includes('not available on your plan')) {
      return { skipReason: error };
    }
    throw new Error(`Failed to create protected share link: ${error || response.status()}`);
  }

  const linkToken = payload.token;
  if (typeof linkToken !== 'string' || linkToken.length === 0) {
    throw new Error(`Create share link response missing token: ${JSON.stringify(payload)}`);
  }

  return { token: linkToken };
}

async function createProtectedUploadLink(request: APIRequestContext, token: string, repoId: string): Promise<PublicLinkResult> {
  const response = await request.post('/api/v2.1/upload-links/', {
    headers: {
      ...authHeaders(token),
      'Content-Type': 'application/json',
    },
    data: {
      repo_id: repoId,
      path: '/',
      password: publicLinkPassword,
    },
  });

  const payload = await readJSON(response) as Record<string, unknown>;
  if (!response.ok()) {
    const error = getErrorMessage(payload);
    if (error === 'Upload link limit reached' || error.includes('not available on your plan')) {
      return { skipReason: error };
    }
    throw new Error(`Failed to create protected upload link: ${error || response.status()}`);
  }

  const linkToken = payload.token;
  if (typeof linkToken !== 'string' || linkToken.length === 0) {
    throw new Error(`Create upload link response missing token: ${JSON.stringify(payload)}`);
  }

  return { token: linkToken };
}

async function login(page: Page, user: Credentials, nextPath: string) {
  const encodedNext = encodeURIComponent(nextPath);

  await page.goto(`/login/?next=${encodedNext}`);
  await expect(page).toHaveURL(new RegExp(`/login/\\?next=${encodedNext}$`));

  await page.locator('#email').fill(user.email);
  await page.locator('#password').fill(user.password);
  await page.getByRole('button', { name: /log in/i }).click();

  await expect(page).toHaveURL(new RegExp(`${escapeRegExp(nextPath)}/?$`));
  await expect(page.locator('#main')).toBeVisible();
}

test.describe('Desktop split smoke', () => {
  test('redirects unauthenticated app routes to login', async ({ page }) => {
    await page.goto('/dashboard/');

    await expect(page).toHaveURL(/\/login\/\?next=%2Fdashboard%2F$/);
    await expect(page.locator('#email')).toBeVisible();
    await expect(page.locator('#password')).toBeVisible();
  });

  test('completes login and returns to the requested dashboard route', async ({ page }) => {
    await login(page, credentials.user, '/dashboard/');

    await expect(page.getByRole('link', { name: 'All Activities' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Shared with me' })).toBeVisible();
  });

  test('logs out from the account menu', async ({ page }) => {
    await login(page, credentials.user, '/dashboard/');

    await page.locator('#my-info').click();
    await expect(page.locator('#user-info-popup')).toBeVisible();
    await page.getByRole('link', { name: 'Log out' }).click();

    await expect(page).toHaveURL(/\/login\/(\?next=%2F)?$/);
    await expect(page.locator('#email')).toBeVisible();
  });

  test('loads org admin routes for authorized users', async ({ page }) => {
    await login(page, credentials.orgAdmin, '/org/info/');

    await expect(page.locator('h3.sf-heading')).toContainText('Admin');
    await expect(page.getByRole('link', { name: 'Users' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Groups' })).toBeVisible();
  });

  test('blocks org admin routes for non-admin users before mount', async ({ page }) => {
    await login(page, credentials.user, '/org/info/');

    await expect(page.getByRole('heading', { name: 'Permission denied' })).toBeVisible();
    await expect(page.getByText('organization admin access', { exact: false })).toBeVisible();
  });

  test('loads sys admin routes for superadmins', async ({ page }) => {
    await login(page, credentials.sysAdmin, '/sys/info/');

    await expect(page.locator('h3.sf-heading')).toContainText('System Admin');
    await expect(page.getByRole('link', { name: 'Users' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Libraries' })).toBeVisible();
  });

  test('blocks sys admin routes for non-superadmins before mount', async ({ page }) => {
    await login(page, credentials.user, '/sys/info/');

    await expect(page.getByRole('heading', { name: 'Permission denied' })).toBeVisible();
    await expect(page.getByText('system admin access', { exact: false })).toBeVisible();
  });

  test('org subscription view', async ({ page }) => {
    await login(page, credentials.orgAdmin, '/dashboard/');

    const subscriptionEnabled = await page.evaluate(() => Boolean(window.app?.pageOptions?.enableSubscription));
    test.skip(!subscriptionEnabled, 'Subscriptions are disabled in the current environment.');

    await page.goto('/org/subscription/');
    await expect(page.locator('#current-plan')).toBeVisible();
  });
});

test.describe.serial('Desktop split public link smoke', () => {
  test('loads password-protected public share links through the frontend shell', async ({ page, request }) => {
    const token = await loginViaAPI(request, credentials.orgAdmin);
    await cleanupPublicSmokeArtifacts(request, token);

    const repo = await createPublicSmokeRepo(request, token, 'share');
    if ('skipReason' in repo) {
      test.skip(true, repo.skipReason);
      return;
    }

    try {
      await createEmptyRepoFile(request, token, repo.repoId, '/desktop-split-share-smoke.txt');

      const shareLink = await createProtectedShareLink(request, token, repo.repoId);
      if ('skipReason' in shareLink) {
        test.skip(true, shareLink.skipReason);
        return;
      }

      await page.goto(`/d/${shareLink.token}`);
      await expect(page.getByText('Password Protected')).toBeVisible();

      const lockedStatus = await page.evaluate(async (shareToken) => {
        const response = await fetch(`/api/v2.1/share-links/${shareToken}/dirents/?p=/`, {
          credentials: 'same-origin',
        });
        return response.status;
      }, shareLink.token);
      expect(lockedStatus).toBe(403);

      await page.locator('input[type="password"]').fill(publicLinkPassword);
      await page.getByRole('button', { name: 'Submit' }).click();

      await expect(page.locator('.shared-dir-view-main')).toBeVisible();
      await expect(page.getByText('Current path:', { exact: false })).toBeVisible();

      const unlockedState = await page.evaluate(async (shareToken) => {
        const response = await fetch(`/api/v2.1/share-links/${shareToken}/dirents/?p=/`, {
          credentials: 'same-origin',
        });
        const data = await response.json().catch(() => ({}));
        const direntList = Array.isArray((data as { dirent_list?: unknown[] }).dirent_list)
          ? (data as { dirent_list: unknown[] }).dirent_list
          : [];
        return {
          status: response.status,
          count: direntList.length,
        };
      }, shareLink.token);

      expect(unlockedState.status).toBe(200);
      expect(unlockedState.count).toBeGreaterThan(0);
    } finally {
      await cleanupPublicSmokeArtifacts(request, token);
    }
  });

  test('loads password-protected public upload links through the frontend shell', async ({ page, request }) => {
    const token = await loginViaAPI(request, credentials.orgAdmin);
    await cleanupPublicSmokeArtifacts(request, token);

    const repo = await createPublicSmokeRepo(request, token, 'upload');
    if ('skipReason' in repo) {
      test.skip(true, repo.skipReason);
      return;
    }

    try {
      const uploadLink = await createProtectedUploadLink(request, token, repo.repoId);
      if ('skipReason' in uploadLink) {
        test.skip(true, uploadLink.skipReason);
        return;
      }

      await page.goto(`/u/d/${uploadLink.token}`);
      await expect(page.getByText('Password Protected')).toBeVisible();

      const lockedStatus = await page.evaluate(async (uploadToken) => {
        const response = await fetch(`/api/v2.1/upload-links/${uploadToken}/upload/`, {
          credentials: 'same-origin',
        });
        return response.status;
      }, uploadLink.token);
      expect(lockedStatus).toBe(403);

      await page.locator('input[type="password"]').fill(publicLinkPassword);
      await page.getByRole('button', { name: 'Submit' }).click();

      await expect(page.locator('#upload-link-panel')).toBeVisible();

      const unlockedState = await page.evaluate(async (uploadToken) => {
        const response = await fetch(`/api/v2.1/upload-links/${uploadToken}/upload/`, {
          credentials: 'same-origin',
        });
        const data = await response.json().catch(() => ({}));
        const uploadLinkValue = typeof (data as { upload_link?: unknown }).upload_link === 'string'
          ? (data as { upload_link: string }).upload_link
          : '';
        return {
          status: response.status,
          uploadLink: uploadLinkValue,
        };
      }, uploadLink.token);

      expect(unlockedState.status).toBe(200);
      expect(unlockedState.uploadLink).toContain('/seafhttp/upload-api/');
    } finally {
      await cleanupPublicSmokeArtifacts(request, token);
    }
  });
});