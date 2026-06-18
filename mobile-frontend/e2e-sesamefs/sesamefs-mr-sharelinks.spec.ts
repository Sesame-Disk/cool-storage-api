/**
 * SesameFS share-link & intra-org access control ACROSS REGIONS (cluster only).
 *
 * Shares and share links live in the shared Cassandra keyspace, so they replicate
 * across the 2-DC cluster. These tests exercise access *from the other region*:
 *
 *   - Intra-org library share: owner (USA) shares a library with specific users;
 *     the granted users can reach it via the EU region, while a same-org user who
 *     was NOT granted, and a different-org user, are both DENIED.
 *   - Public share link: a link created in USA is reachable anonymously from BOTH
 *     regions (token is the only gate — by design, public).
 *
 * NOTE: SesameFS share LINKS have no "internal/org-only" scope — any token holder
 * (any org, even anonymous) can open them. The ability to scope a *link* to the org
 * (so a non-recipient is denied) does not exist; that gap is covered as a @bug
 * fix-target in sesamefs-bugs.spec.ts and docs/BUG-SHARE-LINK-NO-INTERNAL-SCOPE-20260618.md.
 *
 * INERT unless MR_USA_URL and MR_EU_URL are set (cluster stack only).
 */
import { expect, test, type APIRequestContext } from '@playwright/test';
import {
  DEV_EMAILS,
  DEV_TOKENS,
  SUITE_PREFIX,
  authHeaders,
  cleanupTestArtifacts,
  createRepo,
  createShareLink,
  listDirNames,
  shareWithUser,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const USA_URL = process.env.MR_USA_URL;
const EU_URL = process.env.MR_EU_URL;
const REPL_TIMEOUT = Number(process.env.MR_REPLICATION_TIMEOUT_MS || 30_000);
const OWNER = DEV_TOKENS.admin; // org 1
const PREFIX = SUITE_PREFIX.sharelinks;
const name = (tag: string) => uniqueName(tag, PREFIX);
const haveCluster = Boolean(USA_URL && EU_URL);

async function waitUntil<T>(fn: () => Promise<T>, ok: (v: T) => boolean, timeoutMs: number, label: string): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  // eslint-disable-next-line no-constant-condition
  while (true) {
    const value = await fn();
    if (ok(value)) return value;
    if (Date.now() > deadline) throw new Error(`timed out after ${timeoutMs}ms waiting for ${label}`);
    await new Promise((r) => setTimeout(r, 1000));
  }
}

(haveCluster ? test.describe : test.describe.skip)('SesameFS share-link & intra-org access across regions', () => {
  test.afterEach(async ({ playwright }) => {
    const req: APIRequestContext = await playwright.request.newContext({ baseURL: USA_URL });
    await cleanupTestArtifacts(req, OWNER, PREFIX);
    await req.dispose();
  });

  test('intra-org share: granted users reach it cross-region; non-shared and cross-org users are denied', async ({
    playwright,
  }) => {
    const usa = await playwright.request.newContext({ baseURL: USA_URL });
    const eu = await playwright.request.newContext({ baseURL: EU_URL });
    try {
      const repo = await createRepo(usa, OWNER, name('intra'));
      test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
      if ('skipReason' in repo) return;
      const id = repo.repoId;
      await uploadFile(usa, OWNER, id, '/', 'doc.txt', 'org content');

      // Owner (USA) shares with two same-org users: user (rw) and readonly (r).
      expect((await shareWithUser(usa, OWNER, id, DEV_EMAILS.user, 'rw')).ok).toBe(true);
      expect((await shareWithUser(usa, OWNER, id, DEV_EMAILS.readonly, 'r')).ok).toBe(true);

      // Granted users reach the library via the EU region (after share replicates).
      const safeNames = async (token: string) => {
        try {
          return await listDirNames(eu, token, id, '/');
        } catch {
          return [] as string[];
        }
      };
      await waitUntil(() => safeNames(DEV_TOKENS.user), (n) => n.includes('doc.txt'), REPL_TIMEOUT, 'rw share to reach EU');
      expect(await safeNames(DEV_TOKENS.readonly)).toContain('doc.txt');

      // A same-org user who was NOT granted (guest) is denied at the EU region.
      const guestRes = await eu.get(`/api/v2.1/repos/${encodeURIComponent(id)}/dir/?p=/`, {
        headers: authHeaders(DEV_TOKENS.guest),
      });
      expect(guestRes.status(), 'non-shared same-org user must be denied').toBeGreaterThanOrEqual(400);

      // A different-org user (superadmin, org 0) is denied too.
      const crossOrgRes = await eu.get(`/api/v2.1/repos/${encodeURIComponent(id)}/dir/?p=/`, {
        headers: authHeaders(DEV_TOKENS.superadmin),
      });
      expect(crossOrgRes.status(), 'cross-org user must be denied').toBeGreaterThanOrEqual(400);
    } finally {
      await usa.dispose();
      await eu.dispose();
    }
  });

  test('public share link is reachable anonymously from either region', async ({ playwright }) => {
    const usa = await playwright.request.newContext({ baseURL: USA_URL });
    const eu = await playwright.request.newContext({ baseURL: EU_URL });
    try {
      const repo = await createRepo(usa, OWNER, name('public'));
      test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
      if ('skipReason' in repo) return;
      const id = repo.repoId;
      await uploadFile(usa, OWNER, id, '/', 'public.txt', 'public content');

      const link = await createShareLink(usa, OWNER, id, {});
      test.skip('skipReason' in link, 'skipReason' in link ? (link as any).skipReason : '');
      if ('skipReason' in link) return;
      const token = link.token;
      const direntsPath = `/api/v2.1/share-links/${encodeURIComponent(token)}/dirents/?p=/`;

      // Anonymous (no auth) access works at the origin region...
      const usaRes = await usa.get(direntsPath);
      expect(usaRes.status()).toBe(200);

      // ...and at the other region once the link replicates (still anonymous).
      const euStatus = await waitUntil(
        () => eu.get(direntsPath).then((r) => r.status()),
        (s) => s === 200,
        REPL_TIMEOUT,
        'public share link to replicate to EU',
      );
      expect(euStatus).toBe(200);

      const euBody = await (await eu.get(direntsPath)).text();
      expect(euBody).toContain('public.txt');
    } finally {
      await usa.dispose();
      await eu.dispose();
    }
  });
});
