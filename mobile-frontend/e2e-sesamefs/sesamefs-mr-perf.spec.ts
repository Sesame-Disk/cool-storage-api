/**
 * SesameFS concurrent upload+download PERFORMANCE review (true-cluster only).
 *
 * Drives upload and download AT THE SAME TIME against the SAME library and reports
 * throughput/latency. This is a "measure & report" spec: it asserts correctness
 * (every byte intact, no errors) but does NOT fail on speed — absolute numbers in a
 * shared CI container aren't a stable pass/fail signal. The figures are printed so
 * a human can review them.
 *
 *   - same-region: upload a fresh file to USA while concurrently downloading an
 *     existing seed file from USA, plus extra parallel readers (contention on one
 *     library, one region).
 *   - cross-region: upload via USA while concurrently downloading the (replicated)
 *     seed via EU.
 *
 * INERT unless MR_USA_URL and MR_EU_URL are set (cluster stack only).
 */
import { expect, test, type APIRequestContext } from '@playwright/test';
import { DEV_TOKENS, SUITE_PREFIX, cleanupTestArtifacts, createRepo, uniqueName, uploadFile } from './sesamefs-helpers';

const USA_URL = process.env.MR_USA_URL;
const EU_URL = process.env.MR_EU_URL;
const REPL_TIMEOUT = Number(process.env.MR_REPLICATION_TIMEOUT_MS || 30_000);
const ADMIN = DEV_TOKENS.admin;
const PREFIX = SUITE_PREFIX.perf;
const name = (tag: string) => uniqueName(tag, PREFIX);
const haveCluster = Boolean(USA_URL && EU_URL);

// File size for the perf run (override with MR_PERF_MB). 20MB is a reasonable
// in-container default — big enough to time, small enough to stay quick.
const SIZE_MB = Number(process.env.MR_PERF_MB || 20);
const PAYLOAD = 'A'.repeat(SIZE_MB * 1024 * 1024);
const READERS = Number(process.env.MR_PERF_READERS || 3);

const headers = (token: string) => ({ Authorization: `Token ${token}`, Accept: 'application/json' });
const mbps = (bytes: number, ms: number) => (ms > 0 ? bytes / (ms / 1000) / 1e6 : 0);

async function timedUpload(req: APIRequestContext, token: string, id: string, fname: string) {
  const t0 = Date.now();
  const status = await uploadFile(req, token, id, '/', fname, PAYLOAD);
  return { status, ms: Date.now() - t0, bytes: PAYLOAD.length };
}

/** Download via the seafhttp link flow; returns size + elapsed (path-only → stays on this region). */
async function timedDownload(req: APIRequestContext, token: string, id: string, path: string) {
  const t0 = Date.now();
  const linkRes = await req.get(`/api2/repos/${id}/file/?p=${encodeURIComponent(path)}`, { headers: headers(token) });
  if (!linkRes.ok()) return { status: linkRes.status(), ms: Date.now() - t0, bytes: 0 };
  let url = (await linkRes.json()) as string;
  try {
    const u = new URL(url);
    url = u.pathname + u.search;
  } catch {
    /* relative */
  }
  const fileRes = await req.get(url, { headers: headers(token) });
  const buf = fileRes.ok() ? await fileRes.body() : Buffer.alloc(0);
  return { status: fileRes.status(), ms: Date.now() - t0, bytes: buf.length };
}

function report(title: string, rows: Array<{ op: string; r: { status: number; ms: number; bytes: number } }>) {
  // Printed for human review (no thresholds asserted).
  // eslint-disable-next-line no-console
  console.log(`\n[PERF] ${title} (payload=${SIZE_MB}MB)`);
  for (const { op, r } of rows) {
    // eslint-disable-next-line no-console
    console.log(
      `[PERF]   ${op.padEnd(28)} status=${r.status} ${(r.bytes / 1e6).toFixed(1).padStart(6)}MB  ${String(r.ms).padStart(6)}ms  ${mbps(r.bytes, r.ms).toFixed(1).padStart(6)} MB/s`,
    );
  }
}

(haveCluster ? test.describe : test.describe.skip)('SesameFS concurrent upload+download performance', () => {
  test.afterEach(async ({ playwright }) => {
    const req = await playwright.request.newContext({ baseURL: USA_URL });
    await cleanupTestArtifacts(req, ADMIN, PREFIX);
    await req.dispose();
  });

  test('same-region: simultaneous upload + downloads against one library (USA)', async ({ playwright }) => {
    const usa = await playwright.request.newContext({ baseURL: USA_URL });
    try {
      const repo = await createRepo(usa, ADMIN, name('perf-same'));
      test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
      if ('skipReason' in repo) return;
      const id = repo.repoId;

      // Seed file to read while we write a different one.
      expect(await uploadFile(usa, ADMIN, id, '/', 'seed.bin', PAYLOAD)).toBe(200);

      // Fire one upload + READERS downloads of the seed, all at once.
      const [up, ...downs] = await Promise.all([
        timedUpload(usa, ADMIN, id, 'upload.bin'),
        ...Array.from({ length: READERS }, () => timedDownload(usa, ADMIN, id, '/seed.bin')),
      ]);

      report('same-region USA: 1 upload + N downloads concurrently', [
        { op: 'upload.bin (write)', r: up },
        ...downs.map((r, i) => ({ op: `seed.bin (read #${i + 1})`, r })),
      ]);

      // Correctness: write succeeded, every concurrent read returned the full seed.
      expect(up.status).toBe(200);
      for (const d of downs) {
        expect(d.status).toBe(200);
        expect(d.bytes).toBe(PAYLOAD.length);
      }
      // The just-uploaded file is intact when read back.
      const back = await timedDownload(usa, ADMIN, id, '/upload.bin');
      expect(back.status).toBe(200);
      expect(back.bytes).toBe(PAYLOAD.length);
    } finally {
      await usa.dispose();
    }
  });

  test('cross-region: upload via USA while downloading the replicated seed via EU', async ({ playwright }) => {
    const usa = await playwright.request.newContext({ baseURL: USA_URL });
    const eu = await playwright.request.newContext({ baseURL: EU_URL });
    try {
      const repo = await createRepo(usa, ADMIN, name('perf-x'));
      test.skip('skipReason' in repo, 'skipReason' in repo ? (repo as any).skipReason : '');
      if ('skipReason' in repo) return;
      const id = repo.repoId;
      expect(await uploadFile(usa, ADMIN, id, '/', 'seed.bin', PAYLOAD)).toBe(200);

      // Wait until the seed is fully downloadable via EU (replication complete).
      const deadline = Date.now() + REPL_TIMEOUT;
      // eslint-disable-next-line no-constant-condition
      while (true) {
        const probe = await timedDownload(eu, ADMIN, id, '/seed.bin');
        if (probe.status === 200 && probe.bytes === PAYLOAD.length) break;
        if (Date.now() > deadline) throw new Error('seed did not replicate to EU within timeout');
        await new Promise((r) => setTimeout(r, 1000));
      }

      // Concurrent: write to USA, read the replicated copy from EU.
      const [up, down] = await Promise.all([
        timedUpload(usa, ADMIN, id, 'upload.bin'),
        timedDownload(eu, ADMIN, id, '/seed.bin'),
      ]);

      report('cross-region: upload@USA + download@EU concurrently', [
        { op: 'upload.bin @USA (write)', r: up },
        { op: 'seed.bin @EU (read)', r: down },
      ]);

      expect(up.status).toBe(200);
      expect(down.status).toBe(200);
      expect(down.bytes).toBe(PAYLOAD.length);
    } finally {
      await usa.dispose();
      await eu.dispose();
    }
  });
});
