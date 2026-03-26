// @vitest-environment node
/**
 * End-to-end integration tests against the real backend in no-auth (dev) mode.
 *
 * These tests call the REAL API endpoints on localhost:3000.
 * They test the same code paths the mobile frontend uses.
 *
 * Prerequisites:
 *   - docker compose up -d sesamefs cassandra minio minio-init
 *   - AUTH_DEV_MODE=true, AUTH_ALLOW_ANONYMOUS=true (defaults)
 *
 * Run: npx vitest run src/test/integration/e2e-noauth.test.ts
 */

import { describe, it, expect, beforeAll, afterAll } from 'vitest';

const API_BASE = process.env.API_BASE || 'http://localhost:3000';

/**
 * The backend returns download URLs with its own file_server_root
 * (e.g. http://localhost/seafhttp/...). When testing through a proxy
 * on a different port, we need to rewrite these URLs to use the proxy.
 */
function fixDownloadUrl(url: string): string {
  try {
    const u = new URL(url);
    const base = new URL(API_BASE);
    u.host = base.host;
    u.protocol = base.protocol;
    return u.toString();
  } catch {
    return url;
  }
}

// ─── Helpers ────────────────────────────────────────────────────────

async function api(
  method: string,
  path: string,
  options?: {
    body?: Record<string, unknown> | URLSearchParams | FormData;
    contentType?: string;
  },
): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (options?.contentType) headers['Content-Type'] = options.contentType;

  let body: BodyInit | undefined;
  if (options?.body instanceof URLSearchParams || options?.body instanceof FormData) {
    body = options.body;
  } else if (options?.body) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(options.body);
  }

  return fetch(`${API_BASE}${path}`, { method, headers, body });
}

async function apiJson<T = unknown>(
  method: string,
  path: string,
  options?: Parameters<typeof api>[2],
): Promise<T> {
  const res = await api(method, path, options);
  if (!res.ok) {
    const text = await res.text().catch(() => '');
    throw new Error(`${method} ${path} → ${res.status}: ${text}`);
  }
  return res.json() as Promise<T>;
}

async function getUploadLink(repoId: string, parentDir: string): Promise<string> {
  const res = await fetch(
    `${API_BASE}/api2/repos/${repoId}/upload-link/?p=${encodeURIComponent(parentDir)}`,
    { headers: { Accept: 'application/json' } },
  );
  return fixDownloadUrl((await res.json()) as string);
}

async function uploadFile(
  repoId: string,
  parentDir: string,
  fileName: string,
  content: string | Blob,
): Promise<void> {
  const uploadLink = await getUploadLink(repoId, parentDir);
  const blob = typeof content === 'string' ? new Blob([content], { type: 'text/plain' }) : content;
  const formData = new FormData();
  formData.append('file', blob, fileName);
  formData.append('parent_dir', parentDir);
  formData.append('relative_path', '');
  const res = await fetch(uploadLink, { method: 'POST', body: formData });
  if (!res.ok) throw new Error(`Upload failed: ${res.status}`);
}

// ─── State ──────────────────────────────────────────────────────────

let testRepoId: string;
let cleanupRepoIds: string[] = [];

// ─── Lifecycle ──────────────────────────────────────────────────────

beforeAll(async () => {
  try {
    const res = await fetch(`${API_BASE}/api2/ping/`);
    const text = await res.text();
    if (text.trim() !== 'pong') throw new Error(`Unexpected: ${text}`);
  } catch (err) {
    throw new Error(
      `Backend not reachable at ${API_BASE}. Run: docker compose up -d sesamefs cassandra minio minio-init\n${err}`,
    );
  }
});

afterAll(async () => {
  for (const repoId of cleanupRepoIds) {
    try {
      await api('DELETE', `/api2/repos/${repoId}/`);
    } catch {}
  }
});

// ─── Tests ──────────────────────────────────────────────────────────

describe('E2E No-Auth Integration Tests', () => {
  // ── Auth & Account ──────────────────────────────────────────────

  describe('Authentication & Account', () => {
    it('ping returns pong', async () => {
      const res = await fetch(`${API_BASE}/api2/ping/`);
      expect(await res.text()).toContain('pong');
    });

    it('account info accessible without token (anonymous/dev mode)', async () => {
      const info = await apiJson<Record<string, unknown>>('GET', '/api2/account/info/');
      expect(info.email).toBeTruthy();
      expect(info.name).toBeTruthy();
      expect(info.can_add_repo).toBe(true);
    });

    it('server info returns expected fields', async () => {
      const info = await apiJson<Record<string, unknown>>('GET', '/api2/server-info/');
      expect(info.version).toBeTruthy();
      expect(info.features).toBeDefined();
    });
  });

  // ── Library Management ──────────────────────────────────────────

  describe('Library (Repo) Management', () => {
    it('creates a new library', async () => {
      const repo = await apiJson<Record<string, unknown>>('POST', '/api2/repos/', {
        body: { name: `e2e-test-${Date.now()}` },
      });
      expect(repo.repo_id).toBeTruthy();
      testRepoId = repo.repo_id as string;
      cleanupRepoIds.push(testRepoId);
    });

    it('lists repos including the new library', async () => {
      const repos = await apiJson<Record<string, unknown>[]>('GET', '/api2/repos/');
      expect(repos.length).toBeGreaterThan(0);
      const found = repos.find((r) => (r.id || r.repo_id) === testRepoId);
      expect(found).toBeTruthy();
    });

    it('renames a library', async () => {
      const newName = `e2e-renamed-${Date.now()}`;
      const res = await api('POST', `/api2/repos/${testRepoId}/?op=rename`, {
        body: new URLSearchParams({ repo_name: newName }),
      });
      expect(res.ok).toBe(true);

      const repos = await apiJson<Record<string, unknown>[]>('GET', '/api2/repos/');
      const found = repos.find((r) => (r.id || r.repo_id) === testRepoId);
      expect(found?.name || found?.repo_name).toBe(newName);
    });

    it('lists empty directory in new repo', async () => {
      const dirents = await apiJson<unknown[]>('GET', `/api2/repos/${testRepoId}/dir/?p=/`);
      expect(Array.isArray(dirents)).toBe(true);
      expect(dirents.length).toBe(0);
    });
  });

  // ── File Upload ─────────────────────────────────────────────────

  describe('File Upload', () => {
    it('gets an upload link', async () => {
      const link = await getUploadLink(testRepoId, '/');
      expect(link).toContain('upload-api');
    });

    it('uploads a small text file', async () => {
      await uploadFile(testRepoId, '/', 'hello.txt', `Hello ${Date.now()}`);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      expect(dirents.find((d) => d.name === 'hello.txt')).toBeTruthy();
    });

    it('uploads a 100KB binary file', async () => {
      const content = new Blob(['X'.repeat(100 * 1024)], { type: 'application/octet-stream' });
      await uploadFile(testRepoId, '/', 'large.bin', content);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      const file = dirents.find((d) => d.name === 'large.bin');
      expect(file).toBeTruthy();
      expect(file!.size).toBe(100 * 1024);
    });

    it('uploads 3 files in sequence (batch simulation)', async () => {
      for (let i = 0; i < 3; i++) {
        await uploadFile(testRepoId, '/', `batch-${i}.txt`, `batch ${i}`);
      }

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      const batch = dirents.filter((d) => (d.name as string).startsWith('batch-'));
      expect(batch.length).toBe(3);
    });
  });

  // ── File Download ───────────────────────────────────────────────

  describe('File Download', () => {
    it('gets a download link', async () => {
      const res = await fetch(
        `${API_BASE}/api2/repos/${testRepoId}/file/?p=/hello.txt`,
        { headers: { Accept: 'application/json' } },
      );
      expect(res.ok).toBe(true);
      const link = (await res.json()) as string;
      expect(link).toContain('hello.txt');
    });

    it('downloads file content correctly', async () => {
      const res = await fetch(
        `${API_BASE}/api2/repos/${testRepoId}/file/?p=/hello.txt`,
        { headers: { Accept: 'application/json' } },
      );
      const downloadLink = fixDownloadUrl((await res.json()) as string);
      const dlRes = await fetch(downloadLink);
      expect(dlRes.ok).toBe(true);
      const content = await dlRes.text();
      expect(content).toContain('Hello');
    });

    it('downloads the 100KB file with correct size', async () => {
      const res = await fetch(
        `${API_BASE}/api2/repos/${testRepoId}/file/?p=/large.bin`,
        { headers: { Accept: 'application/json' } },
      );
      const downloadLink = fixDownloadUrl((await res.json()) as string);
      const dlRes = await fetch(downloadLink);
      expect(dlRes.ok).toBe(true);
      const blob = await dlRes.blob();
      expect(blob.size).toBe(100 * 1024);
    });
  });

  // ── Directory Operations ────────────────────────────────────────

  describe('Directory Operations', () => {
    it('creates a folder', async () => {
      const res = await api(
        'POST',
        `/api2/repos/${testRepoId}/dir/?p=/subfolder`,
        { body: new URLSearchParams({ operation: 'mkdir' }) },
      );
      expect(res.ok).toBe(true);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      expect(dirents.find((d) => d.name === 'subfolder' && d.type === 'dir')).toBeTruthy();
    });

    it('uploads a file into the subfolder', async () => {
      await uploadFile(testRepoId, '/subfolder', 'nested.txt', 'nested content');

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/subfolder`,
      );
      expect(dirents.find((d) => d.name === 'nested.txt')).toBeTruthy();
    });

    it('renames a file', async () => {
      const res = await api(
        'POST',
        `/api2/repos/${testRepoId}/file/?p=/hello.txt`,
        { body: new URLSearchParams({ operation: 'rename', newname: 'renamed.txt' }) },
      );
      expect(res.ok).toBe(true);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      expect(dirents.find((d) => d.name === 'renamed.txt')).toBeTruthy();
      expect(dirents.find((d) => d.name === 'hello.txt')).toBeFalsy();
    });

    it('copies a file to subfolder', async () => {
      const res = await api(
        'POST',
        `/api2/repos/${testRepoId}/file/?p=/renamed.txt`,
        {
          body: new URLSearchParams({
            operation: 'copy',
            dst_repo: testRepoId,
            dst_dir: '/subfolder',
            src_path: '/renamed.txt',
          }),
        },
      );
      expect(res.ok).toBe(true);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/subfolder`,
      );
      expect(dirents.find((d) => d.name === 'renamed.txt')).toBeTruthy();
    });

    it('moves a file back from subfolder', async () => {
      // Upload a fresh file to move
      await uploadFile(testRepoId, '/subfolder', 'to-move.txt', 'move me');

      const res = await api(
        'POST',
        `/api2/repos/${testRepoId}/file/?p=/subfolder/to-move.txt`,
        {
          body: new URLSearchParams({
            operation: 'move',
            dst_repo: testRepoId,
            dst_dir: '/',
            src_path: '/subfolder/to-move.txt',
          }),
        },
      );
      expect(res.ok).toBe(true);

      const rootDirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      expect(rootDirents.find((d) => d.name === 'to-move.txt')).toBeTruthy();
    });

    it('deletes a file', async () => {
      const res = await api(
        'DELETE',
        `/api2/repos/${testRepoId}/file/?p=/to-move.txt`,
      );
      expect(res.ok).toBe(true);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      expect(dirents.find((d) => d.name === 'to-move.txt')).toBeFalsy();
    });
  });

  // ── Share Links ─────────────────────────────────────────────────

  describe('Share Links', () => {
    it('creates a share link for a file', async () => {
      const link = await apiJson<Record<string, unknown>>(
        'POST',
        '/api/v2.1/share-links/',
        { body: { repo_id: testRepoId, path: '/renamed.txt' } },
      );
      expect(link.token).toBeTruthy();
      expect(link.link).toBeTruthy();
      expect(link.repo_id).toBe(testRepoId);
    });

    it('creates a share link for a folder', async () => {
      const link = await apiJson<Record<string, unknown>>(
        'POST',
        '/api/v2.1/share-links/',
        {
          body: {
            repo_id: testRepoId,
            path: '/subfolder/',
            permissions: 'r',
          },
        },
      );
      expect(link.token).toBeTruthy();
      expect(link.is_dir).toBe(true);
    });

    it('lists share links for the repo', async () => {
      const links = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api/v2.1/share-links/?repo_id=${testRepoId}&path=/renamed.txt`,
      );
      expect(links.length).toBeGreaterThan(0);
      expect(links[0].token).toBeTruthy();
      expect(links[0].link).toBeTruthy();
    });

    it('deletes a share link', async () => {
      const link = await apiJson<Record<string, unknown>>(
        'POST',
        '/api/v2.1/share-links/',
        { body: { repo_id: testRepoId, path: '/renamed.txt' } },
      );
      const token = link.token as string;
      const res = await api('DELETE', `/api/v2.1/share-links/${token}/`);
      expect(res.ok).toBe(true);
    });
  });

  // ── File Locking ────────────────────────────────────────────────

  describe('File Locking', () => {
    it('locks a file', async () => {
      const res = await api(
        'PUT',
        `/api/v2.1/repos/${testRepoId}/file/?p=/renamed.txt`,
        { body: new URLSearchParams({ operation: 'lock' }) },
      );
      expect(res.ok).toBe(true);
      const data = await res.json();
      expect(data.is_locked).toBe(true);
    });

    it('shows lock status in directory listing', async () => {
      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      const file = dirents.find((d) => d.name === 'renamed.txt');
      expect(file).toBeTruthy();
      expect(file!.is_locked).toBeDefined();
    });

    it('unlocks a file', async () => {
      const res = await api(
        'PUT',
        `/api/v2.1/repos/${testRepoId}/file/?p=/renamed.txt`,
        { body: new URLSearchParams({ operation: 'unlock' }) },
      );
      expect(res.ok).toBe(true);
      const data = await res.json();
      expect(data.is_locked).toBe(false);
    });
  });

  // ── Star / Unstar ──────────────────────────────────────────────

  describe('Star / Unstar', () => {
    it('stars a file', async () => {
      const res = await api('POST', '/api2/starredfiles/', {
        body: new URLSearchParams({ repo_id: testRepoId, p: '/renamed.txt' }),
      });
      expect(res.ok).toBe(true);
    });

    it('lists starred files', async () => {
      const data = await apiJson<Record<string, unknown>>('GET', '/api2/starredfiles/');
      // Backend returns { starred_item_list: [...] }
      const list = (data.starred_item_list ?? data) as unknown[];
      expect(Array.isArray(list)).toBe(true);
      expect(list.length).toBeGreaterThan(0);
    });

    it('unstars a file', async () => {
      const res = await api(
        'DELETE',
        `/api2/starredfiles/?repo_id=${testRepoId}&p=/renamed.txt`,
      );
      expect(res.ok).toBe(true);
    });
  });

  // ── Notifications ──────────────────────────────────────────────

  describe('Notifications', () => {
    it('lists notifications', async () => {
      const res = await api('GET', '/api/v2.1/notifications/');
      // Endpoint may or may not exist in all backends; accept 200 or 404
      expect([200, 404]).toContain(res.status);
    });
  });

  // ── Activities ─────────────────────────────────────────────────

  describe('Activities', () => {
    it('lists activities (may be empty)', async () => {
      const res = await api('GET', '/api/v2.1/activities/');
      expect(res.ok).toBe(true);
    });
  });

  // ── Groups ────────────────────────────────────────────────────

  describe('Groups', () => {
    let groupId: number;

    it('creates a group', async () => {
      const group = await apiJson<Record<string, unknown>>(
        'POST',
        '/api/v2.1/groups/',
        { body: { name: `e2e-group-${Date.now()}` } },
      );
      expect(group.id).toBeTruthy();
      groupId = group.id as number;
    });

    it('lists groups', async () => {
      const res = await api('GET', '/api/v2.1/groups/');
      expect(res.ok).toBe(true);
    });

    it('deletes a group', async () => {
      if (!groupId) return;
      const res = await api('DELETE', `/api/v2.1/groups/${groupId}/`);
      expect(res.ok).toBe(true);
    });
  });

  // ── Zip Download ──────────────────────────────────────────────

  describe('Zip Download', () => {
    it('creates a zip task and queries progress', async () => {
      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      const fileNames = dirents
        .filter((d) => d.type === 'file')
        .map((d) => d.name as string)
        .slice(0, 2);

      if (fileNames.length < 1) return; // skip if no files

      const res = await api(
        'POST',
        `/api/v2.1/repos/${testRepoId}/zip-task/`,
        {
          body: new URLSearchParams({
            parent_dir: '/',
            dirents: JSON.stringify(fileNames),
          }),
        },
      );
      expect(res.ok).toBe(true);
      const data = await res.json();
      expect(data.zip_token).toBeTruthy();

      // Query progress
      const progressRes = await api(
        'GET',
        `/api/v2.1/query-zip-progress/?token=${data.zip_token}`,
      );
      expect(progressRes.ok).toBe(true);
    });
  });

  // ── Trash ─────────────────────────────────────────────────────

  describe('Trash / Recycle Bin', () => {
    it('deletes a file (sends to trash)', async () => {
      const res = await api(
        'DELETE',
        `/api2/repos/${testRepoId}/file/?p=/batch-0.txt`,
      );
      expect(res.ok).toBe(true);
    });

    it('lists trash items', async () => {
      const data = await apiJson<Record<string, unknown>>(
        'GET',
        `/api/v2.1/repos/${testRepoId}/trash/`,
      );
      expect(data.data).toBeDefined();
      expect(Array.isArray(data.data)).toBe(true);
    });
  });

  // ── Full Upload → Download → Share roundtrip ──────────────────

  describe('Full roundtrip: upload → download → share link → verify', () => {
    const roundtripContent = `Roundtrip test ${Date.now()}`;
    let shareToken: string;
    let shareLinkUrl: string;

    it('uploads a file', async () => {
      await uploadFile(testRepoId, '/', 'roundtrip.txt', roundtripContent);

      const dirents = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api2/repos/${testRepoId}/dir/?p=/`,
      );
      expect(dirents.find((d) => d.name === 'roundtrip.txt')).toBeTruthy();
    });

    it('downloads the file and verifies content', async () => {
      const res = await fetch(
        `${API_BASE}/api2/repos/${testRepoId}/file/?p=/roundtrip.txt`,
        { headers: { Accept: 'application/json' } },
      );
      const link = fixDownloadUrl((await res.json()) as string);
      const dlRes = await fetch(link);
      const text = await dlRes.text();
      expect(text).toBe(roundtripContent);
    });

    it('creates a share link', async () => {
      const link = await apiJson<Record<string, unknown>>(
        'POST',
        '/api/v2.1/share-links/',
        { body: { repo_id: testRepoId, path: '/roundtrip.txt' } },
      );
      shareToken = link.token as string;
      shareLinkUrl = link.link as string;
      expect(shareToken).toBeTruthy();
      expect(shareLinkUrl).toBeTruthy();
    });

    it('share link is listed', async () => {
      const links = await apiJson<Record<string, unknown>[]>(
        'GET',
        `/api/v2.1/share-links/?repo_id=${testRepoId}&path=/roundtrip.txt`,
      );
      expect(links.some((l) => l.token === shareToken)).toBe(true);
    });

    it('cleans up the share link', async () => {
      const res = await api('DELETE', `/api/v2.1/share-links/${shareToken}/`);
      expect(res.ok).toBe(true);
    });
  });

  // ── Cleanup ───────────────────────────────────────────────────

  describe('Cleanup', () => {
    it('deletes the test library', async () => {
      const res = await api('DELETE', `/api2/repos/${testRepoId}/`);
      expect(res.ok).toBe(true);
      cleanupRepoIds = cleanupRepoIds.filter((id) => id !== testRepoId);
    });
  });
});
