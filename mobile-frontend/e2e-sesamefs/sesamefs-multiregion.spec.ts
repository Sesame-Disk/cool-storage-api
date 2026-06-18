/**
 * SesameFS multi-region REPLICATION coverage (true 2-DC cluster only).
 *
 * Unlike the other specs (which drive a single stack through the frontend), this
 * one talks to BOTH region servers directly and proves that data crosses the
 * region boundary along both replication paths the cluster sets up:
 *
 *   - Cassandra cross-DC replication (metadata): a library + file created via one
 *     region becomes visible (listed) via the other region.
 *   - MinIO active-active bucket replication (object data): the file's actual
 *     bytes, written to the origin region's MinIO, become downloadable from the
 *     other region (whose server reads blocks from ITS OWN, mirrored, MinIO).
 *
 * Both directions are exercised (USA->EU and EU->USA) to prove active-active.
 *
 * Replication is asynchronous, so each cross-region read is polled up to
 * MR_REPLICATION_TIMEOUT_MS before failing.
 *
 * This block is INERT unless MR_USA_URL and MR_EU_URL are set (they are only set
 * by docker-compose.mr-cluster.yaml), so the single-node stack's suite skips it.
 */
import { expect, test, type APIRequestContext } from '@playwright/test';
import {
  DEV_TOKENS,
  SUITE_PREFIX,
  cleanupTestArtifacts,
  createRepo,
  downloadFileContent,
  listDirNames,
  listRepos,
  uniqueName,
  uploadFile,
} from './sesamefs-helpers';

const USA_URL = process.env.MR_USA_URL;
const EU_URL = process.env.MR_EU_URL;
const REPL_TIMEOUT = Number(process.env.MR_REPLICATION_TIMEOUT_MS || 30_000);
const ADMIN = DEV_TOKENS.admin;
const PREFIX = SUITE_PREFIX.multiregion;
const name = (tag: string) => uniqueName(tag, PREFIX);

const haveCluster = Boolean(USA_URL && EU_URL);

/** Poll `fn` until `ok(result)` is true or the timeout elapses. */
async function waitUntil<T>(
  fn: () => Promise<T>,
  ok: (v: T) => boolean,
  timeoutMs: number,
  label: string,
): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  // eslint-disable-next-line no-constant-condition
  while (true) {
    const value = await fn();
    if (ok(value)) return value;
    if (Date.now() > deadline) throw new Error(`timed out after ${timeoutMs}ms waiting for ${label}`);
    await new Promise((r) => setTimeout(r, 1000));
  }
}

(haveCluster ? test.describe : test.describe.skip)(
  'SesameFS multi-region replication (Cassandra cross-DC + MinIO active-active)',
  () => {
    // Both regions share the same dev account, so cleanup via either is enough;
    // the delete replicates back across DCs.
    test.afterEach(async ({ playwright }) => {
      const req: APIRequestContext = await playwright.request.newContext({ baseURL: USA_URL });
      await cleanupTestArtifacts(req, ADMIN, PREFIX);
      await req.dispose();
    });

    test('write via USA replicates to EU (metadata + file content)', async ({ playwright }) => {
      const usa = await playwright.request.newContext({ baseURL: USA_URL });
      const eu = await playwright.request.newContext({ baseURL: EU_URL });
      try {
        const repo = await createRepo(usa, ADMIN, name('usa2eu'));
        test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
        if ('skipReason' in repo) return;
        const id = repo.repoId;
        const body = `usa->eu replication payload ${Date.now()}`;
        expect(await uploadFile(usa, ADMIN, id, '/', 'replicated.txt', body)).toBe(200);

        // 1) Cassandra cross-DC: EU sees the library, then the file metadata.
        await waitUntil(
          () => listRepos(eu, ADMIN, 'mine'),
          (repos) => repos.some((r) => r.repo_id === id),
          REPL_TIMEOUT,
          'library to replicate USA->EU (Cassandra)',
        );
        await waitUntil(
          () => listDirNames(eu, ADMIN, id, '/'),
          (names) => names.includes('replicated.txt'),
          REPL_TIMEOUT,
          'file metadata to replicate USA->EU (Cassandra)',
        );

        // 2) MinIO replication: EU downloads the exact bytes written via USA.
        const dl = await waitUntil(
          () => downloadFileContent(eu, ADMIN, id, '/replicated.txt'),
          (r) => r.status === 200 && r.body === body,
          REPL_TIMEOUT,
          'file content to replicate USA->EU (MinIO)',
        );
        expect(dl.status).toBe(200);
        expect(dl.body).toBe(body);
      } finally {
        await usa.dispose();
        await eu.dispose();
      }
    });

    test('write via EU replicates to USA (metadata + file content)', async ({ playwright }) => {
      const usa = await playwright.request.newContext({ baseURL: USA_URL });
      const eu = await playwright.request.newContext({ baseURL: EU_URL });
      try {
        const repo = await createRepo(eu, ADMIN, name('eu2usa'));
        test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
        if ('skipReason' in repo) return;
        const id = repo.repoId;
        const body = `eu->usa replication payload ${Date.now()}`;
        expect(await uploadFile(eu, ADMIN, id, '/', 'replicated.txt', body)).toBe(200);

        await waitUntil(
          () => listRepos(usa, ADMIN, 'mine'),
          (repos) => repos.some((r) => r.repo_id === id),
          REPL_TIMEOUT,
          'library to replicate EU->USA (Cassandra)',
        );
        await waitUntil(
          () => listDirNames(usa, ADMIN, id, '/'),
          (names) => names.includes('replicated.txt'),
          REPL_TIMEOUT,
          'file metadata to replicate EU->USA (Cassandra)',
        );

        const dl = await waitUntil(
          () => downloadFileContent(usa, ADMIN, id, '/replicated.txt'),
          (r) => r.status === 200 && r.body === body,
          REPL_TIMEOUT,
          'file content to replicate EU->USA (MinIO)',
        );
        expect(dl.status).toBe(200);
        expect(dl.body).toBe(body);
      } finally {
        await usa.dispose();
        await eu.dispose();
      }
    });
  },
);
